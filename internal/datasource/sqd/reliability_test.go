package sqd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- ReliabilityConfig ---

func TestReliabilityConfig_Defaults(t *testing.T) {
	cfg := DefaultReliabilityConfig()
	if cfg.Retry.MaxAttempts != 5 {
		t.Errorf("expected 5 max attempts, got %d", cfg.Retry.MaxAttempts)
	}
	if cfg.Backoff.Base != 2*time.Second {
		t.Errorf("expected 2s base backoff, got %v", cfg.Backoff.Base)
	}
	if cfg.Backoff.Max != 60*time.Second {
		t.Errorf("expected 60s max backoff, got %v", cfg.Backoff.Max)
	}
	if len(cfg.Backoff.Interval) != 5 {
		t.Errorf("expected 5 backoff intervals, got %d", len(cfg.Backoff.Interval))
	}
	if cfg.Workers.Normal != 8 {
		t.Errorf("expected 8 normal workers, got %d", cfg.Workers.Normal)
	}
	if cfg.Workers.Degraded != 4 {
		t.Errorf("expected 4 degraded workers, got %d", cfg.Workers.Degraded)
	}
	if cfg.Workers.Emergency != 1 {
		t.Errorf("expected 1 emergency worker, got %d", cfg.Workers.Emergency)
	}
	if cfg.Circuit.Threshold != 5 {
		t.Errorf("expected 5 circuit threshold, got %d", cfg.Circuit.Threshold)
	}
	if cfg.Circuit.Cooldown != 60*time.Second {
		t.Errorf("expected 60s circuit cooldown, got %v", cfg.Circuit.Cooldown)
	}
}

func TestReliabilityConfig_Validate(t *testing.T) {
	cfg := ReliabilityConfig{}
	cfg.Validate()
	if cfg.Retry.MaxAttempts != 5 {
		t.Errorf("expected 5 after validate, got %d", cfg.Retry.MaxAttempts)
	}
}

// --- AdaptiveWorkers ---

func TestAdaptiveWorkers_InitialState(t *testing.T) {
	w := NewAdaptiveWorkers(WorkersConfig{Normal: 8, Degraded: 4, Emergency: 1})
	if w.Current() != 8 {
		t.Errorf("expected 8 workers, got %d", w.Current())
	}
	if w.Tier() != TierNormal {
		t.Errorf("expected NORMAL tier, got %s", w.Tier())
	}
}

func TestAdaptiveWorkers_ScaleDownOn503(t *testing.T) {
	w := NewAdaptiveWorkers(WorkersConfig{Normal: 8, Degraded: 4, Emergency: 1})

	// First 503: NORMAL → DEGRADED
	if !w.Record503() {
		t.Error("expected scale-down on first 503")
	}
	if w.Current() != 4 {
		t.Errorf("expected 4 workers after first 503, got %d", w.Current())
	}
	if w.Tier() != TierDegraded {
		t.Errorf("expected DEGRADED tier, got %s", w.Tier())
	}

	// Second 503: DEGRADED → EMERGENCY
	if !w.Record503() {
		t.Error("expected scale-down on second 503")
	}
	if w.Current() != 1 {
		t.Errorf("expected 1 worker after second 503, got %d", w.Current())
	}
	if w.Tier() != TierEmergency {
		t.Errorf("expected EMERGENCY tier, got %s", w.Tier())
	}

	// Third 503: already at minimum, no change
	if w.Record503() {
		t.Error("expected no scale-down at EMERGENCY tier")
	}
}

func TestAdaptiveWorkers_ScaleUpOnSuccess(t *testing.T) {
	w := NewAdaptiveWorkers(WorkersConfig{Normal: 8, Degraded: 4, Emergency: 1})

	// Force to EMERGENCY
	w.Record503() // → DEGRADED
	w.Record503() // → EMERGENCY
	if w.Tier() != TierEmergency {
		t.Fatalf("expected EMERGENCY tier, got %s", w.Tier())
	}
	if w.Current() != 1 {
		t.Fatalf("expected 1 worker, got %d", w.Current())
	}

	// Gradual recovery path: 1 → 2 → 4 → 8 (doubling, 5 successes per hop)
	recoverHop := func(from, to int) {
		t.Helper()
		for i := 0; i < 4; i++ {
			if w.RecordSuccess() {
				t.Errorf("hop %d→%d: expected no scale-up before 5th success (at success %d)", from, to, i+1)
			}
		}
		if !w.RecordSuccess() {
			t.Errorf("hop %d→%d: expected scale-up on 5th consecutive success", from, to)
		}
		if w.Current() != to {
			t.Errorf("hop %d→%d: expected %d workers, got %d", from, to, to, w.Current())
		}
	}

	recoverHop(1, 2) // EMERGENCY intermediate step
	recoverHop(2, 4) // reaches DEGRADED level
	if w.Tier() != TierDegraded {
		t.Errorf("expected DEGRADED tier at 4 workers, got %s", w.Tier())
	}
	recoverHop(4, 8) // back to NORMAL
	if w.Tier() != TierNormal {
		t.Errorf("expected NORMAL tier at 8 workers, got %s", w.Tier())
	}
}

func TestAdaptiveWorkers_ScaleUpStopsAtMax(t *testing.T) {
	w := NewAdaptiveWorkers(WorkersConfig{Normal: 8, Degraded: 4, Emergency: 1})

	// Already at NORMAL — success should not change anything
	for i := 0; i < 10; i++ {
		if w.RecordSuccess() {
			t.Errorf("expected no scale-up at NORMAL tier (success %d)", i+1)
		}
	}
	if w.Current() != 8 {
		t.Errorf("expected 8 workers, got %d", w.Current())
	}
}

func TestAdaptiveWorkers_Reset(t *testing.T) {
	w := NewAdaptiveWorkers(WorkersConfig{Normal: 8, Degraded: 4, Emergency: 1})
	w.Record503()
	w.Record503()
	if w.Tier() == TierNormal {
		t.Fatal("expected non-normal tier")
	}
	w.Reset()
	if w.Tier() != TierNormal {
		t.Errorf("expected NORMAL after reset, got %s", w.Tier())
	}
	if w.Current() != 8 {
		t.Errorf("expected 8 workers after reset, got %d", w.Current())
	}
}

// --- ProviderMetrics ---

func TestProviderMetrics_Counters(t *testing.T) {
	m := NewProviderMetrics()

	m.RecordRequest()
	m.RecordRequest()
	m.RecordSuccess(100 * time.Millisecond)
	m.RecordSuccess(200 * time.Millisecond)
	m.RecordFailure(Error503)
	m.RecordFailure(Error429)
	m.RecordFailure(ErrorTimeout)
	m.RecordFailure(ErrorDNS)
	m.RecordFailure(ErrorNetwork)

	snap := m.Snapshot()
	if snap.RequestTotal != 2 {
		t.Errorf("expected 2 requests, got %d", snap.RequestTotal)
	}
	if snap.SuccessTotal != 2 {
		t.Errorf("expected 2 successes, got %d", snap.SuccessTotal)
	}
	if snap.FailedTotal != 5 {
		t.Errorf("expected 5 failures, got %d", snap.FailedTotal)
	}
	if snap.Error503Total != 1 {
		t.Errorf("expected 1 503 error, got %d", snap.Error503Total)
	}
	if snap.Error429Total != 1 {
		t.Errorf("expected 1 429 error, got %d", snap.Error429Total)
	}
	if snap.ErrorTimeoutTotal != 1 {
		t.Errorf("expected 1 timeout error, got %d", snap.ErrorTimeoutTotal)
	}
	if snap.ErrorDNSTotal != 1 {
		t.Errorf("expected 1 DNS error, got %d", snap.ErrorDNSTotal)
	}
	if snap.ErrorNetworkTotal != 1 {
		t.Errorf("expected 1 network error, got %d", snap.ErrorNetworkTotal)
	}
	if snap.AvgLatencyMS != 150 {
		t.Errorf("expected 150ms avg latency, got %.0f", snap.AvgLatencyMS)
	}
}

func TestProviderMetrics_Retry(t *testing.T) {
	m := NewProviderMetrics()
	m.RecordRetry()
	m.RecordRetry()
	m.RecordRetry()

	snap := m.Snapshot()
	if snap.RetryTotal != 3 {
		t.Errorf("expected 3 retries, got %d", snap.RetryTotal)
	}
}

func TestProviderMetrics_Throughput(t *testing.T) {
	m := NewProviderMetrics()
	m.RecordThroughput(1000, 50000)

	snap := m.Snapshot()
	if snap.TxTotal != 1000 {
		t.Errorf("expected 1000 tx, got %d", snap.TxTotal)
	}
	if snap.ByteTotal != 50000 {
		t.Errorf("expected 50000 bytes, got %d", snap.ByteTotal)
	}
}

func TestProviderMetrics_Reset(t *testing.T) {
	m := NewProviderMetrics()
	m.RecordRequest()
	m.RecordSuccess(10 * time.Millisecond)
	m.RecordFailure(Error503)
	m.Reset()

	snap := m.Snapshot()
	if snap.RequestTotal != 0 {
		t.Errorf("expected 0 requests after reset, got %d", snap.RequestTotal)
	}
}

// --- SQD Event Log ---

func TestSQDEventLog(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "sqd-events-test")
	defer os.RemoveAll(dir)

	events, err := NewSQDEventLog(dir)
	if err != nil {
		t.Fatalf("create event log: %v", err)
	}
	defer events.Close()

	events.LogRetry(3, 15*time.Second, errTest("connection reset"))
	events.Log503(4, "No available workers")
	events.Log429("Rate limit exceeded")
	events.LogCircuitOpen(5, 60*time.Second)
	events.LogCircuitHalfOpen()
	events.LogCircuitRecovery()
	events.LogWorkerScale(8, 4, "503 error")
	events.LogDNSFailure(errTest("no such host"))
	events.LogTimeout(90 * time.Second)
	events.LogRecovery("Job resumed successfully")

	// Verify file exists and has content
	logPath := filepath.Join(dir, "sqd-events.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}
	if len(data) == 0 {
		t.Error("event log is empty")
	}
}

func TestSQDEventLog_FormatRetry(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "sqd-events-retry-test")
	defer os.RemoveAll(dir)

	events, err := NewSQDEventLog(dir)
	if err != nil {
		t.Fatalf("create event log: %v", err)
	}
	defer events.Close()

	events.LogRetry(1, 2*time.Second, errTest("timeout"))
	events.LogRetry(2, 5*time.Second, errTest("timeout"))
	events.LogRetry(5, 60*time.Second, errTest("timeout"))

	logPath := filepath.Join(dir, "sqd-events.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}
	content := string(data)
	if len(content) == 0 {
		t.Error("event log is empty")
	}
}

// TestSQDEventLog_Format503
func TestSQDEventLog_Rotate(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "sqd-events-rotate-test")
	defer os.RemoveAll(dir)

	// Tiny max size (200 bytes) forces rotation quickly
	events, err := NewSQDEventLogWithMaxSize(dir, 200)
	if err != nil {
		t.Fatalf("create event log: %v", err)
	}
	defer events.Close()

	// Write enough events to exceed 200 bytes (each line is ~100+ bytes)
	for i := 0; i < 10; i++ {
		events.Log503(4, "No available workers — rotation test event with padding to grow the file")
	}

	// Verify the current file exists and is smaller than 2 event lines
	logPath := filepath.Join(dir, "sqd-events.log")
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat current log: %v", err)
	}
	// After rotation the fresh file holds at most a couple of lines
	// (rotation check runs before each write, so up to 2 lines can land
	// between rotations)
	if info.Size() > 400 {
		t.Errorf("current log should be small after rotation, got %d bytes", info.Size())
	}

	// Verify at least one archive exists
	matches, err := filepath.Glob(filepath.Join(dir, "sqd-events-*.log"))
	if err != nil || len(matches) == 0 {
		t.Error("expected rotated archive sqd-events-*.log")
	}
}

func TestSQDEventLog_Format503(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "sqd-events-503-test")
	defer os.RemoveAll(dir)

	events, err := NewSQDEventLog(dir)
	if err != nil {
		t.Fatalf("create event log: %v", err)
	}
	defer events.Close()

	events.Log503(4, "No available workers in pool")
	events.Log503(1, "No_available_worker")

	logPath := filepath.Join(dir, "sqd-events.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}
	content := string(data)
	if len(content) == 0 {
		t.Error("event log is empty after 503 events")
	}
}

// --- CircuitBreaker Degraded tests ---

func TestCircuitBreaker_DegradedState(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:      5,
		DegradeThreshold: 2,
		OpenDuration:     60 * time.Second,
		MinSuccesses:     1,
	})

	// 1 failure: still NORMAL
	cb.RecordFailure()
	if cb.State() != CircuitNormal {
		t.Errorf("expected NORMAL after 1 failure, got %s", cb.StateString())
	}

	// 2 failures: should be DEGRADED
	cb.RecordFailure()
	if cb.State() != CircuitDegraded {
		t.Errorf("expected DEGRADED after 2 failures, got %s", cb.StateString())
	}
	if err := cb.Allow(); err != nil {
		t.Errorf("expected nil in DEGRADED, got %v", err)
	}

	// Recovery: 1 success in DEGRADED → NORMAL
	cb.RecordSuccess()
	if cb.State() != CircuitNormal {
		t.Errorf("expected NORMAL after success, got %s", cb.StateString())
	}
}

func TestCircuitBreaker_DegradedToOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:      3,
		DegradeThreshold: 2,
		OpenDuration:     100 * time.Millisecond,
		MinSuccesses:     1,
	})

	cb.RecordFailure() // NORMAL (1)
	cb.RecordFailure() // DEGRADED (2)
	cb.RecordFailure() // OPEN (3)

	if cb.State() != CircuitOpen {
		t.Errorf("expected OPEN after 3 failures, got %s", cb.StateString())
	}
}

func TestCircuitBreaker_IsHealthy(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:      5,
		DegradeThreshold: 2,
	})

	if !cb.IsHealthy() {
		t.Error("expected healthy (NORMAL)")
	}

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.IsHealthy() {
		t.Error("expected not healthy (DEGRADED)")
	}
}

// errTest is a helper for creating test errors.
type errTest string

func (e errTest) Error() string { return string(e) }
