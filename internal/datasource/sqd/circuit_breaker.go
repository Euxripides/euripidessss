package sqd

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when the circuit breaker is open.
var ErrCircuitOpen = errors.New("sqd: circuit breaker open")

// CircuitState represents the state of the circuit breaker.
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // normal operation
	CircuitOpen                         // requests blocked
	CircuitHalfOpen                     // single probe allowed
)

func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "CLOSED"
	case CircuitOpen:
		return "OPEN"
	case CircuitHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// CircuitBreaker implements a circuit breaker pattern for SQD API calls.
// It protects against cascading failures by blocking requests after
// consecutive failures exceed a threshold.
type CircuitBreaker struct {
	mu           sync.Mutex
	state        CircuitState
	failures     int
	successes    int
	lastFailure  time.Time
	lastSuccess  time.Time
	openedAt     time.Time
	halfOpenUsed bool

	maxFailures  int
	openDuration time.Duration
	minSuccesses int // successes needed in half-open to close
	now          func() time.Time
}

// CircuitBreakerConfig holds configuration for the circuit breaker.
type CircuitBreakerConfig struct {
	MaxFailures  int           // consecutive failures before opening
	OpenDuration time.Duration // how long to stay open
	MinSuccesses int           // successes in half-open to close (default 2)
}

// DefaultCircuitBreakerConfig returns sensible defaults.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		MaxFailures:  5,
		OpenDuration: 30 * time.Second,
		MinSuccesses: 2,
	}
}

// NewCircuitBreaker creates a new circuit breaker.
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	if config.MaxFailures <= 0 {
		config.MaxFailures = 5
	}
	if config.OpenDuration <= 0 {
		config.OpenDuration = 30 * time.Second
	}
	if config.MinSuccesses <= 0 {
		config.MinSuccesses = 2
	}
	return &CircuitBreaker{
		state:        CircuitClosed,
		maxFailures:  config.MaxFailures,
		openDuration: config.OpenDuration,
		minSuccesses: config.MinSuccesses,
		now:          time.Now,
	}
}

// Allow checks whether a request is permitted. Returns nil if allowed,
// or ErrCircuitOpen if the circuit is open.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return nil
	case CircuitOpen:
		if cb.now().Sub(cb.openedAt) >= cb.openDuration {
			cb.state = CircuitHalfOpen
			cb.halfOpenUsed = true // consume the probe slot
			return nil
		}
		return ErrCircuitOpen
	case CircuitHalfOpen:
		if cb.halfOpenUsed {
			return ErrCircuitOpen
		}
		cb.halfOpenUsed = true
		return nil
	default:
		return nil
	}
}

// RecordFailure records a failed request. May open the circuit.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailure = cb.now()

	switch cb.state {
	case CircuitClosed:
		cb.failures++
		if cb.failures >= cb.maxFailures {
			cb.state = CircuitOpen
			cb.openedAt = cb.now()
		}
	case CircuitHalfOpen:
		cb.state = CircuitOpen
		cb.openedAt = cb.now()
		cb.failures = 0
		cb.successes = 0
	case CircuitOpen:
		// already open, nothing to do
	}
}

// RecordSuccess records a successful request. May close the circuit if enough
// consecutive successes accumulate in half-open state.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastSuccess = cb.now()

	switch cb.state {
	case CircuitClosed:
		cb.failures = 0
	case CircuitHalfOpen:
		cb.successes++
		if cb.successes >= cb.minSuccesses {
			cb.state = CircuitClosed
			cb.failures = 0
			cb.successes = 0
		}
	case CircuitOpen:
		// ignore — success shouldn't happen when open (request blocked earlier)
	}
}

// State returns the current state of the circuit breaker.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// StateString returns the state as a string.
func (cb *CircuitBreaker) StateString() string {
	return cb.State().String()
}

// Stats returns diagnostic information.
type CircuitStats struct {
	State         string    `json:"state"`
	Failures      int       `json:"failures"`
	Successes     int       `json:"successes"`
	LastFailureAt time.Time `json:"last_failure_at,omitempty"`
	LastSuccessAt time.Time `json:"last_success_at,omitempty"`
	OpenedAt      time.Time `json:"opened_at,omitempty"`
}

// Stats returns current circuit breaker statistics.
func (cb *CircuitBreaker) Stats() CircuitStats {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return CircuitStats{
		State:         cb.state.String(),
		Failures:      cb.failures,
		Successes:     cb.successes,
		LastFailureAt: cb.lastFailure,
		LastSuccessAt: cb.lastSuccess,
		OpenedAt:      cb.openedAt,
	}
}

// Reset forcibly resets the circuit breaker to closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitClosed
	cb.failures = 0
	cb.successes = 0
	cb.halfOpenUsed = false
	cb.openedAt = time.Time{}
}
