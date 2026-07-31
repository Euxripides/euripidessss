package sqd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fastTestConfig returns a reliability config with short backoff/cooldown so
// integration tests finish quickly.
func fastTestConfig() ReliabilityConfig {
	cfg := DefaultReliabilityConfig()
	cfg.Backoff.Interval = []time.Duration{
		5 * time.Millisecond,
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		80 * time.Millisecond,
	}
	cfg.Retry.MaxAttempts = 5
	// One postWithRetry call records exactly one breaker failure (at exit),
	// so threshold=2 means two failed calls open the circuit.
	cfg.Circuit.Threshold = 2
	cfg.Circuit.Cooldown = 300 * time.Millisecond
	return cfg
}

// newTestReliableClient builds a client against the mock server and registers
// cleanup so the event log file is closed before TempDir removal.
func newTestReliableClient(t *testing.T, server *httptest.Server, timeout time.Duration) *Client {
	t.Helper()
	client := &http.Client{Timeout: timeout}
	c, err := NewReliable(client, server.URL, "", t.TempDir())
	if err != nil {
		t.Fatalf("NewReliable: %v", err)
	}
	t.Cleanup(c.Close)
	c.SetReliabilityConfig(fastTestConfig())
	return c
}

// TestMock503_TriggersCooldownAndWorkerDegrade verifies that a 503 with
// "No available workers" enters cooldown immediately and degrades the
// adaptive worker pool.
func TestMock503_TriggersCooldownAndWorkerDegrade(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"message":"No available workers in pool"}`)
	}))
	defer server.Close()

	c := newTestReliableClient(t, server, 5*time.Second)

	_, err := c.postWithRetry(context.Background(), server.URL+"/dataset/finalized-stream", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error from 503")
	}
	if !c.IsInCooldown() {
		t.Error("expected cooldown after 503 No available workers")
	}
	if c.Workers() == nil || c.Workers().Current() != 4 {
		t.Errorf("expected workers degraded to 4, got %v", c.Workers())
	}
	if calls.Load() != 1 {
		t.Errorf("expected exactly 1 call (no retry for no-available-worker 503), got %d", calls.Load())
	}
	snap := c.Metrics().Snapshot()
	if snap.Error503Total != 1 {
		t.Errorf("expected 1 recorded 503, got %d", snap.Error503Total)
	}
}

// TestMock503_RetriesThenOpensCircuit verifies that a generic 503 (not the
// no-worker variant) is retried up to max attempts, then the breaker opens
// after enough failed calls.
func TestMock503_RetriesThenOpensCircuit(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"message":"temporarily overloaded"}`)
	}))
	defer server.Close()

	c := newTestReliableClient(t, server, 5*time.Second)

	ctx := context.Background()
	endpoint := server.URL + "/dataset/finalized-stream"
	// First call: 1 initial + 5 retries, all 503 → breaker failure count 1
	if _, err := c.postWithRetry(ctx, endpoint, []byte(`{}`)); err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if calls.Load() != 6 {
		t.Errorf("expected 6 total calls (1 + 5 retries), got %d", calls.Load())
	}
	// Second call: another 6 → breaker failure count 2 = threshold → OPEN
	if _, err := c.postWithRetry(ctx, endpoint, []byte(`{}`)); err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if calls.Load() != 12 {
		t.Errorf("expected 12 total calls, got %d", calls.Load())
	}
	if c.Breaker().State() != CircuitOpen {
		t.Errorf("expected breaker OPEN after consecutive failures, got %s", c.Breaker().StateString())
	}
	if c.Metrics().Snapshot().RetryTotal != 10 {
		t.Errorf("expected 10 recorded retries (5 per call), got %d", c.Metrics().Snapshot().RetryTotal)
	}
}

// TestMock503_RecoversAfterFailures verifies end-to-end: failures open the
// breaker, cooldown elapses, a probe succeeds and the breaker recovers.
func TestMock503_RecoversAfterFailures(t *testing.T) {
	var calls atomic.Int64
	const failUntil = 12 // first 12 requests fail, then succeed
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= failUntil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"message":"temporarily overloaded"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := newTestReliableClient(t, server, 5*time.Second)

	ctx := context.Background()
	endpoint := server.URL + "/dataset/finalized-stream"
	// Two failed calls (6 requests each = 12) trip the breaker (threshold=2)
	if _, err := c.postWithRetry(ctx, endpoint, []byte(`{}`)); err == nil {
		t.Fatal("expected error from retries exhausted")
	}
	if _, err := c.postWithRetry(ctx, endpoint, []byte(`{}`)); err == nil {
		t.Fatal("expected error from retries exhausted")
	}
	if c.Breaker().State() != CircuitOpen {
		t.Fatalf("expected breaker OPEN, got %s", c.Breaker().StateString())
	}
	// While OPEN, requests are rejected immediately without hitting the server
	if _, err := c.postWithRetry(ctx, endpoint, []byte(`{}`)); err == nil {
		t.Fatal("expected ErrCircuitOpen while breaker OPEN")
	}

	// Wait for cooldown, then the next call is a probe which succeeds → recovery
	time.Sleep(400 * time.Millisecond)
	resp, err := c.postWithRetry(ctx, endpoint, []byte(`{}`))
	if err != nil {
		t.Fatalf("expected probe success, got %v", err)
	}
	resp.Body.Close()
	if c.Breaker().State() != CircuitNormal {
		t.Errorf("expected breaker NORMAL after recovery, got %s", c.Breaker().StateString())
	}
}

// TestMock429_RateLimitedRetries verifies 429 responses are retried and
// counted in metrics.
func TestMock429_RateLimitedRetries(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"message":"rate limit exceeded"}`)
	}))
	defer server.Close()

	c := newTestReliableClient(t, server, 5*time.Second)

	_, err := c.postWithRetry(context.Background(), server.URL+"/dataset/finalized-stream", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error after 429 retries exhausted")
	}
	if calls.Load() != 6 {
		t.Errorf("expected 6 total calls (1 + 5 retries), got %d", calls.Load())
	}
	snap := c.Metrics().Snapshot()
	if snap.Error429Total != 6 {
		t.Errorf("expected 6 recorded 429 errors (one per attempt), got %d", snap.Error429Total)
	}
	if snap.RetryTotal != 5 {
		t.Errorf("expected 5 recorded retries, got %d", snap.RetryTotal)
	}
}

// TestMockTimeout_TriggersCircuitBreaker verifies timeouts are retried,
// classified, and accumulate failures that open the breaker.
func TestMockTimeout_TriggersCircuitBreaker(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(500 * time.Millisecond) // always slower than client timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Client timeout of 60ms ensures every request times out
	c := newTestReliableClient(t, server, 60*time.Millisecond)

	ctx := context.Background()
	endpoint := server.URL + "/dataset/finalized-stream"
	// First call: 6 timeouts → breaker failure 1
	if _, err := c.postWithRetry(ctx, endpoint, []byte(`{}`)); err == nil {
		t.Fatal("expected timeout error")
	}
	// Second call: 6 more timeouts → breaker failure 2 = threshold → OPEN
	if _, err := c.postWithRetry(ctx, endpoint, []byte(`{}`)); err == nil {
		t.Fatal("expected timeout error")
	}
	if calls.Load() != 12 {
		t.Errorf("expected 12 total calls, got %d", calls.Load())
	}
	snap := c.Metrics().Snapshot()
	if snap.ErrorTimeoutTotal != 12 {
		t.Errorf("expected 12 recorded timeouts, got %d", snap.ErrorTimeoutTotal)
	}
	if c.Breaker().State() != CircuitOpen {
		t.Errorf("expected breaker OPEN after timeouts, got %s", c.Breaker().StateString())
	}
}

// TestMockSuccess_ResetsFailures verifies a successful request resets the
// failure counter and keeps the breaker NORMAL.
func TestMockSuccess_ResetsFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := newTestReliableClient(t, server, 5*time.Second)

	for i := 0; i < 3; i++ {
		resp, err := c.postWithRetry(context.Background(), server.URL+"/dataset/finalized-stream", []byte(`{}`))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}
	if c.Breaker().State() != CircuitNormal {
		t.Errorf("expected breaker NORMAL, got %s", c.Breaker().StateString())
	}
	if c.Metrics().Snapshot().SuccessTotal != 3 {
		t.Errorf("expected 3 successes, got %d", c.Metrics().Snapshot().SuccessTotal)
	}
}
