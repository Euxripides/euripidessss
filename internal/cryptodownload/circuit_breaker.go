package cryptodownload

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when a request is blocked by the circuit breaker.
var ErrCircuitOpen = errors.New("circuit breaker open")

type circuitState int

const (
	circuitClosed circuitState = iota
	circuitOpen
	circuitHalfOpen
)

type hostCircuit struct {
	mu            sync.Mutex
	state         circuitState
	failures      int
	openedAt      time.Time
	halfOpenProbe bool

	// Configuration
	maxFailures  int
	openDuration time.Duration
	now          func() time.Time
}

func newHostCircuit(maxFailures int, openDuration time.Duration) *hostCircuit {
	return &hostCircuit{
		state:        circuitClosed,
		maxFailures:  maxFailures,
		openDuration: openDuration,
		now:          time.Now,
	}
}

// Allow checks whether a request to this host is permitted.
func (c *hostCircuit) Allow() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.state {
	case circuitClosed:
		return nil
	case circuitOpen:
		if c.now().Sub(c.openedAt) >= c.openDuration {
			c.state = circuitHalfOpen
			c.halfOpenProbe = false
			return nil
		}
		return ErrCircuitOpen
	case circuitHalfOpen:
		if c.halfOpenProbe {
			return ErrCircuitOpen
		}
		c.halfOpenProbe = true
		return nil
	default:
		return nil
	}
}

// RecordFailure records a failed attempt and transitions state if needed.
func (c *hostCircuit) RecordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.state {
	case circuitClosed:
		c.failures++
		if c.failures >= c.maxFailures {
			c.state = circuitOpen
			c.openedAt = c.now()
		}
	case circuitHalfOpen:
		c.state = circuitOpen
		c.openedAt = c.now()
		c.failures = 0
	default:
	}
}

// RecordSuccess resets the circuit after a successful probe.
func (c *hostCircuit) RecordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.failures = 0
	c.state = circuitClosed
	c.halfOpenProbe = false
}

// CircuitBreaker manages per-host circuit breakers.
type CircuitBreaker struct {
	mu       sync.Mutex
	circuits map[string]*hostCircuit
	config   circuitConfig
}

type circuitConfig struct {
	maxFailures  int
	openDuration time.Duration
}

var sharedCircuitBreaker = &CircuitBreaker{
	circuits: make(map[string]*hostCircuit),
	config: circuitConfig{
		maxFailures:  5,
		openDuration: 30 * time.Second,
	},
}

// Allow checks whether the host circuit is closed.
func (cb *CircuitBreaker) Allow(host string) error {
	cb.mu.Lock()
	c, ok := cb.circuits[host]
	if !ok {
		c = newHostCircuit(cb.config.maxFailures, cb.config.openDuration)
		cb.circuits[host] = c
	}
	cb.mu.Unlock()
	return c.Allow()
}

// RecordResult updates the host circuit with the outcome of a request.
func (cb *CircuitBreaker) RecordResult(host string, success bool) {
	cb.mu.Lock()
	c, ok := cb.circuits[host]
	if !ok {
		c = newHostCircuit(cb.config.maxFailures, cb.config.openDuration)
		cb.circuits[host] = c
	}
	cb.mu.Unlock()

	if success {
		c.RecordSuccess()
	} else {
		c.RecordFailure()
	}
}
