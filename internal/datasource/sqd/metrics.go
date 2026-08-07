package sqd

import (
	"sync"
	"sync/atomic"
	"time"
)

// SQDErrorKind classifies the type of error for metrics tracking.
type SQDErrorKind string

const (
	ErrorNone        SQDErrorKind = ""
	Error503         SQDErrorKind = "503"
	Error429         SQDErrorKind = "429"
	ErrorTimeout     SQDErrorKind = "timeout"
	ErrorDNS         SQDErrorKind = "dns"
	ErrorNetwork     SQDErrorKind = "network"
	ErrorOther       SQDErrorKind = "other"
	ErrorCircuitOpen SQDErrorKind = "circuit_open"
	ErrorCooldown    SQDErrorKind = "cooldown"
)

// ProviderMetrics tracks SQD provider-level metrics for observability.
// All counters are safe for concurrent access.
type ProviderMetrics struct {
	mu sync.Mutex

	// Counters
	requestTotal      int64
	successTotal      int64
	failedTotal       int64
	retryTotal        int64
	error503Total     int64
	error429Total     int64
	errorTimeoutTotal int64
	errorDNSTotal     int64
	errorNetworkTotal int64

	// Performance
	latencySumMS   int64
	latencySamples int64
	txTotal        int64
	byteTotal      int64

	// Timing
	startedAt     time.Time
	lastRequestAt time.Time
	lastSuccessAt time.Time
	lastFailureAt time.Time
}

// NewProviderMetrics creates a new metrics tracker.
func NewProviderMetrics() *ProviderMetrics {
	return &ProviderMetrics{
		startedAt: time.Now(),
	}
}

// RecordRequest increments the total request counter.
func (m *ProviderMetrics) RecordRequest() {
	atomic.AddInt64(&m.requestTotal, 1)
	m.mu.Lock()
	m.lastRequestAt = time.Now()
	m.mu.Unlock()
}

// RecordSuccess records a successful request with latency.
func (m *ProviderMetrics) RecordSuccess(latency time.Duration) {
	atomic.AddInt64(&m.successTotal, 1)
	atomic.AddInt64(&m.latencySumMS, latency.Milliseconds())
	atomic.AddInt64(&m.latencySamples, 1)
	m.mu.Lock()
	m.lastSuccessAt = time.Now()
	m.mu.Unlock()
}

// RecordFailure records a failed request with error classification.
func (m *ProviderMetrics) RecordFailure(kind SQDErrorKind) {
	atomic.AddInt64(&m.failedTotal, 1)
	switch kind {
	case Error503:
		atomic.AddInt64(&m.error503Total, 1)
	case Error429:
		atomic.AddInt64(&m.error429Total, 1)
	case ErrorTimeout:
		atomic.AddInt64(&m.errorTimeoutTotal, 1)
	case ErrorDNS:
		atomic.AddInt64(&m.errorDNSTotal, 1)
	case ErrorNetwork:
		atomic.AddInt64(&m.errorNetworkTotal, 1)
	}
	m.mu.Lock()
	m.lastFailureAt = time.Now()
	m.mu.Unlock()
}

// RecordRetry increments the retry counter.
func (m *ProviderMetrics) RecordRetry() {
	atomic.AddInt64(&m.retryTotal, 1)
}

// RecordThroughput records transactions and bytes processed.
func (m *ProviderMetrics) RecordThroughput(txCount int64, byteCount int64) {
	atomic.AddInt64(&m.txTotal, txCount)
	atomic.AddInt64(&m.byteTotal, byteCount)
}

// Snapshot returns a point-in-time metrics snapshot.
type MetricsSnapshot struct {
	RequestTotal      int64   `json:"sqd_request_total"`
	SuccessTotal      int64   `json:"sqd_success_total"`
	FailedTotal       int64   `json:"sqd_failed_total"`
	RetryTotal        int64   `json:"sqd_retry_count"`
	Error503Total     int64   `json:"sqd_503_total"`
	Error429Total     int64   `json:"sqd_429_total"`
	ErrorTimeoutTotal int64   `json:"sqd_timeout_total"`
	ErrorDNSTotal     int64   `json:"sqd_dns_error_total"`
	ErrorNetworkTotal int64   `json:"sqd_network_error_total"`
	AvgLatencyMS      float64 `json:"sqd_latency_ms"`
	TxTotal           int64   `json:"sqd_tx_total"`
	ByteTotal         int64   `json:"sqd_byte_total"`
	UptimeSeconds     float64 `json:"sqd_uptime_seconds"`
}

// Snapshot returns current metrics.
func (m *ProviderMetrics) Snapshot() MetricsSnapshot {
	var avgLatency float64
	samples := atomic.LoadInt64(&m.latencySamples)
	if samples > 0 {
		avgLatency = float64(atomic.LoadInt64(&m.latencySumMS)) / float64(samples)
	}
	m.mu.Lock()
	uptime := time.Since(m.startedAt).Seconds()
	m.mu.Unlock()

	return MetricsSnapshot{
		RequestTotal:      atomic.LoadInt64(&m.requestTotal),
		SuccessTotal:      atomic.LoadInt64(&m.successTotal),
		FailedTotal:       atomic.LoadInt64(&m.failedTotal),
		RetryTotal:        atomic.LoadInt64(&m.retryTotal),
		Error503Total:     atomic.LoadInt64(&m.error503Total),
		Error429Total:     atomic.LoadInt64(&m.error429Total),
		ErrorTimeoutTotal: atomic.LoadInt64(&m.errorTimeoutTotal),
		ErrorDNSTotal:     atomic.LoadInt64(&m.errorDNSTotal),
		ErrorNetworkTotal: atomic.LoadInt64(&m.errorNetworkTotal),
		AvgLatencyMS:      avgLatency,
		TxTotal:           atomic.LoadInt64(&m.txTotal),
		ByteTotal:         atomic.LoadInt64(&m.byteTotal),
		UptimeSeconds:     uptime,
	}
}

// Reset zeroes all counters (useful for per-job tracking).
func (m *ProviderMetrics) Reset() {
	atomic.StoreInt64(&m.requestTotal, 0)
	atomic.StoreInt64(&m.successTotal, 0)
	atomic.StoreInt64(&m.failedTotal, 0)
	atomic.StoreInt64(&m.retryTotal, 0)
	atomic.StoreInt64(&m.error503Total, 0)
	atomic.StoreInt64(&m.error429Total, 0)
	atomic.StoreInt64(&m.errorTimeoutTotal, 0)
	atomic.StoreInt64(&m.errorDNSTotal, 0)
	atomic.StoreInt64(&m.errorNetworkTotal, 0)
	atomic.StoreInt64(&m.latencySumMS, 0)
	atomic.StoreInt64(&m.latencySamples, 0)
	atomic.StoreInt64(&m.txTotal, 0)
	atomic.StoreInt64(&m.byteTotal, 0)
	m.mu.Lock()
	m.startedAt = time.Now()
	m.mu.Unlock()
}
