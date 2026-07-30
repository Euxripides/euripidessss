// Package scheduler provides task queuing and concurrency control for SQD streams.
//
// It enforces resource limits (max parallel streams, large/small job caps)
// and provides fair scheduling with priority support to prevent SQD-side 503
// "No available workers" errors.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrSchedulerFull is returned when the queue is at capacity.
var ErrSchedulerFull = errors.New("sqd scheduler: queue full")

// ErrSchedulerClosed is returned when trying to submit to a closed scheduler.
var ErrSchedulerClosed = errors.New("sqd scheduler: closed")

// Priority for task scheduling.
type Priority int

const (
	PriorityLow    Priority = 0
	PriorityNormal Priority = 5
	PriorityHigh   Priority = 10
)

// TaskKind classifies the resource profile of a task.
type TaskKind string

const (
	KindStream   TaskKind = "stream"    // targeted address query, limited block range
	KindLargeJob TaskKind = "large_job" // full chain history scan
)

// Config holds scheduler configuration.
type Config struct {
	MaxParallelStreams int `json:"max_parallel_streams"` // total concurrent SQD streams
	MaxLargeJobs       int `json:"max_large_jobs"`       // max concurrent large jobs
	MaxSmallJobs       int `json:"max_small_jobs"`       // max concurrent small jobs (stream)
	QueueSize          int `json:"queue_size"`           // max pending tasks in queue
}

// DefaultConfig returns sensible defaults that protect SQD from overload.
func DefaultConfig() Config {
	return Config{
		MaxParallelStreams: 1,
		MaxLargeJobs:       1,
		MaxSmallJobs:       2,
		QueueSize:          100,
	}
}

// Task represents a schedulable SQD operation.
type Task struct {
	ID       string
	Kind     TaskKind
	Priority Priority
	Created  time.Time
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	err      error
}

// Stats captures scheduler runtime metrics.
type Stats struct {
	ActiveStreams   int   `json:"active_streams"`
	ActiveLargeJobs int   `json:"active_large_jobs"`
	ActiveSmallJobs int   `json:"active_small_jobs"`
	Queued          int   `json:"queued"`
	Completed       int64 `json:"completed"`
	Rejected        int64 `json:"rejected"`
}

// Scheduler manages SQD task concurrency.
type Scheduler struct {
	mu      sync.Mutex
	config  Config
	queue   []*Task // priority queue ordered by priority DESC, then created ASC
	active  map[string]*Task
	running bool
	closed  bool

	completed int64
	rejected  int64

	notify chan struct{} // signals task completion
}

// New creates a new Scheduler with the given config.
func New(config Config) *Scheduler {
	return &Scheduler{
		config:  config,
		queue:   make([]*Task, 0),
		active:  make(map[string]*Task),
		running: true,
		notify:  make(chan struct{}, 1),
	}
}

// Submit adds a task to the scheduler queue. Returns a channel that closes
// when the task is granted execution permission. The caller must call Done()
// when the task completes.
func (s *Scheduler) Submit(ctx context.Context, id string, kind TaskKind, priority Priority) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrSchedulerClosed
	}
	if len(s.queue) >= s.config.QueueSize {
		s.rejected++
		return nil, ErrSchedulerFull
	}

	taskCtx, cancel := context.WithCancel(ctx)
	task := &Task{
		ID:       id,
		Kind:     kind,
		Priority: priority,
		Created:  time.Now(),
		ctx:      taskCtx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}

	// Insert into priority queue (higher priority first, then FIFO)
	s.queue = insertSorted(s.queue, task)
	s.trySchedule()
	return task, nil
}

// Done marks a task as complete, freeing its slot.
func (s *Scheduler) Done(task *Task) {
	s.mu.Lock()
	delete(s.active, task.ID)
	s.completed++
	// Drain the task's done channel to avoid goroutine leak
	select {
	case <-task.done:
	default:
	}
	s.trySchedule()
	s.mu.Unlock()

	// Signal waiters
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Cancel aborts a queued task and removes it from the queue.
// Returns true if the task was found and cancelled.
func (s *Scheduler) Cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check queue
	for i, task := range s.queue {
		if task.ID == id {
			task.cancel()
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			s.trySchedule()
			return true
		}
	}
	// Check active
	if task, ok := s.active[id]; ok {
		task.cancel()
		return true
	}
	return false
}

// Wait blocks until there are no active tasks.
func (s *Scheduler) Wait(ctx context.Context) error {
	for {
		s.mu.Lock()
		activeCount := len(s.active)
		queueLen := len(s.queue)
		s.mu.Unlock()

		if activeCount == 0 && queueLen == 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.notify:
			// task completed, re-check
		case <-time.After(5 * time.Second):
			// periodic re-check to avoid missing signals
		}
	}
}

// WaitFor waits for a task to be granted execution.
func (t *Task) WaitFor(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.ctx.Done():
		return t.ctx.Err()
	case <-t.done:
		return nil
	}
}

// Stats returns current scheduler metrics.
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()

	var large, small int
	for _, task := range s.active {
		switch task.Kind {
		case KindLargeJob:
			large++
		case KindStream:
			small++
		}
	}
	return Stats{
		ActiveStreams:   len(s.active),
		ActiveLargeJobs: large,
		ActiveSmallJobs: small,
		Queued:          len(s.queue),
		Completed:       s.completed,
		Rejected:        s.rejected,
	}
}

// Close shuts down the scheduler, cancelling all queued tasks.
func (s *Scheduler) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true
	for _, task := range s.queue {
		task.cancel()
	}
	s.queue = nil
	return nil
}

// trySchedule must be called with mu held. It moves eligible tasks from
// queue to active, respecting concurrency limits.
func (s *Scheduler) trySchedule() {
	if len(s.queue) == 0 {
		return
	}

	var largeActive, smallActive int
	for _, task := range s.active {
		switch task.Kind {
		case KindLargeJob:
			largeActive++
		case KindStream:
			smallActive++
		}
	}

	remaining := s.config.MaxParallelStreams - len(s.active)
	if remaining <= 0 {
		return
	}

	var scheduled []*Task
	remainingQueue := make([]*Task, 0, len(s.queue))

	for _, task := range s.queue {
		if remaining <= 0 {
			remainingQueue = append(remainingQueue, task)
			continue
		}

		// Check kind-specific limits
		switch task.Kind {
		case KindLargeJob:
			if largeActive >= s.config.MaxLargeJobs {
				remainingQueue = append(remainingQueue, task)
				continue
			}
		case KindStream:
			if smallActive >= s.config.MaxSmallJobs {
				remainingQueue = append(remainingQueue, task)
				continue
			}
		}

		// Grant execution
		s.active[task.ID] = task
		close(task.done)

		switch task.Kind {
		case KindLargeJob:
			largeActive++
		case KindStream:
			smallActive++
		}
		remaining--
		scheduled = append(scheduled, task)
	}

	s.queue = remainingQueue
}

// insertSorted inserts task into queue maintaining priority DESC, created ASC order.
func insertSorted(queue []*Task, task *Task) []*Task {
	pos := 0
	for i, t := range queue {
		if task.Priority > t.Priority {
			pos = i
			break
		}
		if task.Priority == t.Priority && task.Created.Before(t.Created) {
			pos = i
			break
		}
		pos = i + 1
	}
	// Insert at pos
	queue = append(queue, nil)
	copy(queue[pos+1:], queue[pos:])
	queue[pos] = task
	return queue
}

// String returns a human-readable status.
func (s *Scheduler) String() string {
	stats := s.Stats()
	return fmt.Sprintf("SQD Scheduler[active=%d large=%d small=%d queued=%d completed=%d]",
		stats.ActiveStreams, stats.ActiveLargeJobs, stats.ActiveSmallJobs, stats.Queued, stats.Completed)
}
