package semanticjobs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// CanonicalSource reparses an existing canonical/raw source. The narrow
// contract prevents the job service from silently falling back to a download.
type CanonicalSource interface {
	Reparse(context.Context, Job, ProgressReporter) error
}

// EnrichmentRunner updates semantic assets only. It has no downloader method
// and receives the immutable range recorded on the job.
type EnrichmentRunner interface {
	Reenrich(context.Context, Job, ProgressReporter) error
}

type Runner interface {
	CanonicalSource
	EnrichmentRunner
}

type ProgressReporter func(Progress) error

type Service struct {
	store  Store
	runner Runner
	now    func() time.Time

	mu      sync.Mutex
	jobs    map[string]Job
	cancels map[string]context.CancelFunc
	closed  bool
	wg      sync.WaitGroup
}

func NewService(store Store, runner Runner) (*Service, error) {
	if store == nil || runner == nil {
		return nil, errors.New("semantic job store and runner are required")
	}
	jobs, err := store.List()
	if err != nil {
		return nil, err
	}
	s := &Service{store: store, runner: runner, now: time.Now, jobs: make(map[string]Job), cancels: make(map[string]context.CancelFunc)}
	for _, job := range jobs {
		if err := validatePersistedJob(job); err != nil {
			return nil, fmt.Errorf("invalid persisted semantic job %q: %w", job.ID, err)
		}
		s.jobs[job.ID] = job.Clone()
	}
	return s, nil
}

func (s *Service) Submit(req Request) (Job, bool, error) {
	normalized, err := NormalizeAndValidate(req)
	if err != nil {
		return Job{}, false, err
	}
	id, key, err := identity(normalized)
	if err != nil {
		return Job{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, false, errors.New("semantic job service is closed")
	}
	if existing, ok := s.jobs[id]; ok {
		return existing.Clone(), false, nil
	}
	now := s.now().UTC()
	total := normalized.EndBlock - normalized.StartBlock + 1
	job := Job{ID: id, IdempotencyKey: key, Request: normalized, Status: StatusQueued, Progress: Progress{Total: total}, CreatedAt: now, UpdatedAt: now}
	if err := s.store.Save(job); err != nil {
		return Job{}, false, err
	}
	s.jobs[id] = job
	s.startLocked(id)
	return job.Clone(), true, nil
}

func (s *Service) Get(id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	return job.Clone(), nil
}

func (s *Service) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		result = append(result, job.Clone())
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}

// Recover resumes QUEUED and interrupted RUNNING jobs. Recovery never creates
// a new semantic identity and never expands the persisted block range.
func (s *Service) Recover() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("semantic job service is closed")
	}
	for id, job := range s.jobs {
		if job.Status != StatusQueued && job.Status != StatusRunning {
			continue
		}
		if _, active := s.cancels[id]; active {
			continue
		}
		if job.Status == StatusRunning {
			if err := transition(&job, StatusQueued); err != nil {
				return err
			}
		}
		job.RecoveryCount++
		job.UpdatedAt = s.now().UTC()
		if err := s.store.Save(job); err != nil {
			return err
		}
		s.jobs[id] = job
		s.startLocked(id)
	}
	return nil
}

func (s *Service) Retry(id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	if job.Status != StatusFailed {
		return Job{}, fmt.Errorf("only FAILED jobs can be retried, current status is %s", job.Status)
	}
	if err := transition(&job, StatusQueued); err != nil {
		return Job{}, err
	}
	job.Error, job.CompletedAt = "", nil
	job.UpdatedAt = s.now().UTC()
	if err := s.store.Save(job); err != nil {
		return Job{}, err
	}
	s.jobs[id] = job
	s.startLocked(id)
	return job.Clone(), nil
}

func (s *Service) Cancel(id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	if job.Status == StatusCompleted || job.Status == StatusFailed || job.Status == StatusCancelled {
		return job.Clone(), nil
	}
	if cancel := s.cancels[id]; cancel != nil {
		cancel()
	}
	now := s.now().UTC()
	if err := transition(&job, StatusCancelled); err != nil {
		return Job{}, err
	}
	job.UpdatedAt, job.CompletedAt = now, &now
	if err := s.store.Save(job); err != nil {
		return Job{}, err
	}
	s.jobs[id] = job
	return job.Clone(), nil
}

// Close stops local workers. Interrupted jobs remain RUNNING on disk so a new
// process can recover them explicitly with Recover.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	for _, cancel := range s.cancels {
		cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Service) startLocked(id string) {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancels[id] = cancel
	s.wg.Add(1)
	go s.run(ctx, id)
}

func (s *Service) run(ctx context.Context, id string) {
	defer s.wg.Done()
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok || job.Status != StatusQueued {
		delete(s.cancels, id)
		s.mu.Unlock()
		return
	}
	now := s.now().UTC()
	if err := transition(&job, StatusRunning); err != nil {
		delete(s.cancels, id)
		s.mu.Unlock()
		return
	}
	job.StartedAt, job.UpdatedAt = &now, now
	job.Attempts++
	if err := s.store.Save(job); err != nil {
		_ = transition(&job, StatusFailed)
		job.Error = err.Error()
		s.jobs[id] = job
		delete(s.cancels, id)
		s.mu.Unlock()
		return
	}
	s.jobs[id] = job
	s.mu.Unlock()

	reporter := func(progress Progress) error { return s.updateProgress(id, progress) }
	var err error
	if job.Request.Type == JobTypeReparse {
		err = s.runner.Reparse(ctx, job.Clone(), reporter)
	} else {
		err = s.runner.Reenrich(ctx, job.Clone(), reporter)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, id)
	current, exists := s.jobs[id]
	if !exists || current.Status == StatusCancelled || s.closed && errors.Is(err, context.Canceled) {
		return
	}
	finished := s.now().UTC()
	current.UpdatedAt, current.CompletedAt = finished, &finished
	if err != nil {
		_ = transition(&current, StatusFailed)
		current.Error = err.Error()
	} else {
		_ = transition(&current, StatusCompleted)
		current.Error = ""
		current.Progress.Completed = current.Progress.Total
		current.Progress.LastBlock = current.Request.EndBlock
	}
	if saveErr := s.store.Save(current); saveErr != nil {
		// The durable state could not be published. Keep the in-memory state
		// failed even though the previous on-disk state remains recoverable.
		current.Status, current.Error = StatusFailed, "persist terminal state: "+saveErr.Error()
	}
	s.jobs[id] = current
}

func (s *Service) updateProgress(id string, progress Progress) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if job.Status != StatusRunning {
		return fmt.Errorf("job is not running: %s", job.Status)
	}
	if progress.Total != job.Progress.Total || progress.Completed < job.Progress.Completed || progress.Completed > progress.Total {
		return errors.New("invalid or non-monotonic semantic job progress")
	}
	if progress.LastBlock != 0 && (progress.LastBlock < job.Request.StartBlock || progress.LastBlock > job.Request.EndBlock) {
		return errors.New("progress last_block is outside the immutable job range")
	}
	job.Progress, job.UpdatedAt = progress, s.now().UTC()
	if err := s.store.Save(job); err != nil {
		return err
	}
	s.jobs[id] = job
	return nil
}

func identity(req Request) (string, string, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(data)
	key := fmt.Sprintf("%x", sum[:])
	// RFC 4122-compatible deterministic UUID (version 5 shape, SHA-256 input).
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	return id, key, nil
}

func validatePersistedJob(job Job) error {
	if !jobIDPattern.MatchString(job.ID) {
		return errors.New("invalid job id")
	}
	normalized, err := NormalizeAndValidate(job.Request)
	if err != nil {
		return err
	}
	id, key, err := identity(normalized)
	if err != nil || id != job.ID || key != job.IdempotencyKey {
		return errors.New("job identity does not match immutable request")
	}
	switch job.Status {
	case StatusQueued, StatusRunning, StatusCompleted, StatusFailed, StatusCancelled:
	default:
		return fmt.Errorf("invalid status %q", job.Status)
	}
	total := normalized.EndBlock - normalized.StartBlock + 1
	if job.Progress.Total != total || job.Progress.Completed > total {
		return errors.New("invalid persisted progress")
	}
	return nil
}
