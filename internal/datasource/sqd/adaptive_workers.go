package sqd

import (
	"sync"
	"sync/atomic"
	"time"
)

// WorkerTier represents the concurrency level tier.
type WorkerTier int

const (
	TierEmergency WorkerTier = iota // 1 worker — emergency mode
	TierDegraded                    // 4 workers — degraded mode
	TierNormal                      // 8 workers — normal mode
)

func (t WorkerTier) String() string {
	switch t {
	case TierEmergency:
		return "EMERGENCY"
	case TierDegraded:
		return "DEGRADED"
	case TierNormal:
		return "NORMAL"
	default:
		return "UNKNOWN"
	}
}

// AdaptiveWorkers manages a dynamic concurrency pool that scales up/down
// based on SQD health signals (503 errors, circuit breaker state).
//
// Scaling:
//
//	NORMAL (8) --503↑--> DEGRADED (4) --503↑↑--> EMERGENCY (1)
//	EMERGENCY --recovery--> DEGRADED (4) --recovery--> NORMAL (8)
type AdaptiveWorkers struct {
	mu sync.Mutex

	config WorkersConfig

	current     int32       // current worker count (atomic for reads)
	tier        WorkerTier  // current tier
	lastScale   time.Time   // last time we scaled
	scaleCount  int         // number of scale events

	// Recovery tracking
	consecutiveSuccesses int
	last503Time          time.Time
	consecutive503       int

	// Notification channel
	onScale func(from, to int, reason string)
}

// NewAdaptiveWorkers creates a new adaptive worker pool.
func NewAdaptiveWorkers(config WorkersConfig) *AdaptiveWorkers {
	config = validatedWorkersConfig(config)
	return &AdaptiveWorkers{
		config:  config,
		current: int32(config.Normal),
		tier:    TierNormal,
	}
}

func validatedWorkersConfig(c WorkersConfig) WorkersConfig {
	if c.Normal <= 0 {
		c.Normal = 8
	}
	if c.Degraded <= 0 {
		c.Degraded = 4
	}
	if c.Emergency <= 0 {
		c.Emergency = 1
	}
	return c
}

// Current returns the current allowed worker count (safe for concurrent reads).
func (w *AdaptiveWorkers) Current() int {
	return int(atomic.LoadInt32(&w.current))
}

// Tier returns the current tier.
func (w *AdaptiveWorkers) Tier() WorkerTier {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tier
}

// OnScale registers a callback invoked when the worker count changes.
func (w *AdaptiveWorkers) OnScale(fn func(from, to int, reason string)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onScale = fn
}

// Record503 records a 503 "No available workers" error. Triggers scale-down.
// Returns true if the worker count changed.
func (w *AdaptiveWorkers) Record503() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	w.consecutiveSuccesses = 0
	w.consecutive503++
	w.last503Time = now

	switch w.tier {
	case TierNormal:
		return w.scaleTo(TierDegraded, "503 error — scaling down from NORMAL to DEGRADED")
	case TierDegraded:
		return w.scaleTo(TierEmergency, "continued 503 errors — scaling down to EMERGENCY")
	case TierEmergency:
		// Already at minimum
		return false
	}
	return false
}

// RecordSuccess records a successful request. May trigger scale-up recovery.
// Recovery is gradual: 1 → 2 → 4 → 8 (doubling), so the pool ramps back up
// without hammering SQD right after a failure burst.
// Returns true if the worker count changed.
func (w *AdaptiveWorkers) RecordSuccess() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.consecutive503 = 0
	w.consecutiveSuccesses++

	// Recovery: need sustained success before scaling up
	recoveryThreshold := 5
	switch w.tier {
	case TierEmergency:
		if w.consecutiveSuccesses >= recoveryThreshold {
			next := w.nextRecoveryCount()
			if next > w.currentCount() {
				return w.scaleToCount(next, "recovering from EMERGENCY (gradual)")
			}
		}
	case TierDegraded:
		if w.consecutiveSuccesses >= recoveryThreshold {
			return w.scaleTo(TierNormal, "recovering from DEGRADED to NORMAL")
		}
	case TierNormal:
		// Already at maximum
		return false
	}
	return false
}

// RecordRecovery explicitly signals recovery (used by external health checks).
func (w *AdaptiveWorkers) RecordRecovery() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.consecutive503 = 0
	w.consecutiveSuccesses = 10 // force recovery on next success

	switch w.tier {
	case TierEmergency:
		next := w.nextRecoveryCount()
		if next > w.currentCount() {
			return w.scaleToCount(next, "explicit recovery from EMERGENCY (gradual)")
		}
	case TierDegraded:
		return w.scaleTo(TierNormal, "explicit recovery from DEGRADED to NORMAL")
	default:
		return false
	}
	return false
}

// nextRecoveryCount returns the next worker count when recovering from
// EMERGENCY. It doubles the current count up to the DEGRADED level:
// 1 → 2 → 4. Reaching DEGRADED level flips the tier; the final hop to
// NORMAL (8) is handled by the DEGRADED branch.
func (w *AdaptiveWorkers) nextRecoveryCount() int {
	current := w.currentCount()
	next := current * 2
	if next > w.config.Degraded {
		next = w.config.Degraded
	}
	return next
}

func (w *AdaptiveWorkers) currentCount() int {
	return int(atomic.LoadInt32(&w.current))
}

// Reset forcibly resets to NORMAL tier.
func (w *AdaptiveWorkers) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.consecutive503 = 0
	w.consecutiveSuccesses = 0
	w.scaleTo(TierNormal, "manual reset")
}

func (w *AdaptiveWorkers) scaleTo(target WorkerTier, reason string) bool {
	if w.tier == target {
		return false
	}
	return w.scaleToCount(w.countForTier(target), reason)
}

func (w *AdaptiveWorkers) countForTier(target WorkerTier) int {
	switch target {
	case TierNormal:
		return w.config.Normal
	case TierDegraded:
		return w.config.Degraded
	case TierEmergency:
		return w.config.Emergency
	default:
		return w.config.Normal
	}
}

// tierForCount maps a worker count back to its tier. Used when recovering
// gradually (e.g. 1 → 2 → 4): intermediate counts stay in the EMERGENCY tier
// until the DEGRADED level is reached.
func (w *AdaptiveWorkers) tierForCount(count int) WorkerTier {
	switch {
	case count >= w.config.Normal:
		return TierNormal
	case count >= w.config.Degraded:
		return TierDegraded
	default:
		return TierEmergency
	}
}

// scaleToCount sets the worker count to an arbitrary value and derives the
// tier from it. Caller must hold w.mu.
func (w *AdaptiveWorkers) scaleToCount(toCount int, reason string) bool {
	fromCount := w.currentCount()
	if fromCount == toCount {
		return false
	}
	w.tier = w.tierForCount(toCount)
	atomic.StoreInt32(&w.current, int32(toCount))
	w.lastScale = time.Now()
	w.scaleCount++
	// Reset success streak so the next hop needs a fresh burst of successes.
	w.consecutiveSuccesses = 0

	if w.onScale != nil {
		w.onScale(fromCount, toCount, reason)
	}

	return true
}

// Stats returns current adaptive worker statistics.
type AdaptiveWorkerStats struct {
	CurrentWorkers       int        `json:"current_workers"`
	Tier                 string     `json:"tier"`
	Consecutive503       int        `json:"consecutive_503"`
	ConsecutiveSuccesses int        `json:"consecutive_successes"`
	LastScaleAt          *time.Time `json:"last_scale_at,omitempty"`
	ScaleCount           int        `json:"scale_count"`
}

// Stats returns current statistics.
func (w *AdaptiveWorkers) Stats() AdaptiveWorkerStats {
	w.mu.Lock()
	defer w.mu.Unlock()

	var lastScale *time.Time
	if !w.lastScale.IsZero() {
		cp := w.lastScale
		lastScale = &cp
	}

	return AdaptiveWorkerStats{
		CurrentWorkers:       int(atomic.LoadInt32(&w.current)),
		Tier:                 w.tier.String(),
		Consecutive503:       w.consecutive503,
		ConsecutiveSuccesses: w.consecutiveSuccesses,
		LastScaleAt:          lastScale,
		ScaleCount:           w.scaleCount,
	}
}
