package cryptodownload

import (
	"context"
	"sync"
	"time"
)

type GUIBatchSchedulerSnapshot struct {
	Active        int
	Waiting       int
	CooldownUntil time.Time
}

type GUIBatchScheduler struct {
	mu            sync.Mutex
	capacity      int
	active        int
	waiting       int
	nextTicket    uint64
	servingTicket uint64
	cancelled     map[uint64]bool
	notify        chan struct{}
	cooldownUntil time.Time
	now           func() time.Time
}

func NewGUIBatchScheduler(capacity int) *GUIBatchScheduler {
	if capacity < 1 {
		capacity = 1
	}
	return &GUIBatchScheduler{
		capacity:  capacity,
		cancelled: map[uint64]bool{},
		notify:    make(chan struct{}),
		now:       time.Now,
	}
}

func (s *GUIBatchScheduler) Acquire(ctx context.Context, _ *GUIJob) (func(), error) {
	s.mu.Lock()
	ticket := s.nextTicket
	s.nextTicket++
	s.waiting++
	s.mu.Unlock()

	for {
		s.mu.Lock()
		now := s.now()
		if ticket == s.servingTicket && s.active < s.capacity && !now.Before(s.cooldownUntil) {
			s.active++
			s.waiting--
			s.servingTicket++
			s.advanceCancelledLocked()
			s.signalLocked()
			s.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() { s.release() })
			}, nil
		}
		notify := s.notify
		cooldownUntil := s.cooldownUntil
		s.mu.Unlock()

		var timer <-chan time.Time
		var stop func()
		if now.Before(cooldownUntil) {
			pending := time.NewTimer(time.Until(cooldownUntil))
			timer = pending.C
			stop = func() { pending.Stop() }
		}
		select {
		case <-ctx.Done():
			if stop != nil {
				stop()
			}
			s.cancel(ticket)
			return nil, ctx.Err()
		case <-notify:
			if stop != nil {
				stop()
			}
		case <-timer:
		}
	}
}

func (s *GUIBatchScheduler) StartCooldown(duration time.Duration) time.Time {
	if duration <= 0 {
		duration = time.Second
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	until := s.now().Add(duration)
	if until.After(s.cooldownUntil) {
		s.cooldownUntil = until
		s.signalLocked()
	}
	return s.cooldownUntil
}

func (s *GUIBatchScheduler) Snapshot() GUIBatchSchedulerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return GUIBatchSchedulerSnapshot{Active: s.active, Waiting: s.waiting, CooldownUntil: s.cooldownUntil}
}

func (s *GUIBatchScheduler) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active > 0 {
		s.active--
		s.signalLocked()
	}
}

func (s *GUIBatchScheduler) cancel(ticket uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ticket < s.servingTicket {
		return
	}
	s.cancelled[ticket] = true
	s.waiting--
	s.advanceCancelledLocked()
	s.signalLocked()
}

func (s *GUIBatchScheduler) advanceCancelledLocked() {
	for s.cancelled[s.servingTicket] {
		delete(s.cancelled, s.servingTicket)
		s.servingTicket++
	}
}

func (s *GUIBatchScheduler) signalLocked() {
	close(s.notify)
	s.notify = make(chan struct{})
}
