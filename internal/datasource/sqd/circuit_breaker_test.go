package sqd

import (
	"testing"
	"time"
)

func TestCircuitBreaker_InitialState(t *testing.T) {
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig())
	if cb.State() != CircuitClosed {
		t.Errorf("expected CLOSED, got %s", cb.StateString())
	}
}

func TestCircuitBreaker_Allow_Closed(t *testing.T) {
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig())
	if err := cb.Allow(); err != nil {
		t.Errorf("expected nil in CLOSED, got %v", err)
	}
}

func TestCircuitBreaker_OpenAfterFailures(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  3,
		OpenDuration: 100 * time.Millisecond,
		MinSuccesses: 1,
	})

	// Record 3 failures
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	if cb.State() != CircuitOpen {
		t.Errorf("expected OPEN after 3 failures, got %s", cb.StateString())
	}

	// Should block requests
	if err := cb.Allow(); err != ErrCircuitOpen {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_HalfOpen_ThenClose(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  2,
		OpenDuration: 50 * time.Millisecond,
		MinSuccesses: 1,
	})

	// Trip the breaker
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatalf("expected OPEN, got %s", cb.StateString())
	}

	// Wait for half-open
	time.Sleep(60 * time.Millisecond)

	// Should allow one probe
	if err := cb.Allow(); err != nil {
		t.Errorf("expected nil in HALF_OPEN probe, got %v", err)
	}

	// Second request should be blocked
	if err := cb.Allow(); err != ErrCircuitOpen {
		t.Errorf("expected ErrCircuitOpen for second probe, got %v", err)
	}

	// Record success to close
	cb.RecordSuccess()

	if cb.State() != CircuitClosed {
		t.Errorf("expected CLOSED after success, got %s", cb.StateString())
	}
}

func TestCircuitBreaker_HalfOpen_FailAgain(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  2,
		OpenDuration: 50 * time.Millisecond,
		MinSuccesses: 2,
	})

	// Trip
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for half-open
	time.Sleep(60 * time.Millisecond)

	// Probe allowed
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected nil probe, got %v", err)
	}

	// Record failure in half-open → back to OPEN
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Errorf("expected OPEN after half-open failure, got %s", cb.StateString())
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures: 2,
	})

	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatalf("expected OPEN, got %s", cb.StateString())
	}

	cb.Reset()

	if cb.State() != CircuitClosed {
		t.Errorf("expected CLOSED after reset, got %s", cb.StateString())
	}

	if err := cb.Allow(); err != nil {
		t.Errorf("expected nil after reset, got %v", err)
	}
}

func TestCircuitBreaker_Stats(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures: 5,
	})

	cb.RecordFailure()
	cb.RecordSuccess()

	stats := cb.Stats()
	if stats.State != "NORMAL" {
		t.Errorf("expected NORMAL in stats, got %s", stats.State)
	}
	if stats.Failures != 0 {
		t.Errorf("expected 0 failures after success, got %d", stats.Failures)
	}
}
