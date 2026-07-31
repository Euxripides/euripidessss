package sqd

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SQDEvent represents a logged SQD reliability event.
type SQDEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"` // INFO, WARN, ERROR
	Event     string    `json:"event"`
	Retry     int       `json:"retry,omitempty"`
	Backoff   string    `json:"backoff,omitempty"`
	Workers   int       `json:"workers,omitempty"`
	HTTPCode  int       `json:"http_code,omitempty"`
	Error     string    `json:"error,omitempty"`
	Message   string    `json:"message"`
}

// DefaultSQDEventLogMaxSize is the default size at which sqd-events.log
// rotates (10 MB).
const DefaultSQDEventLogMaxSize = 10 * 1024 * 1024

// SQDEventLog writes structured SQD events to a separate sqd-events.log file.
// The file auto-rotates once it exceeds maxSize: the old file is renamed to
// sqd-events-<timestamp>.log and a fresh file is started.
type SQDEventLog struct {
	mu      sync.Mutex
	file    *os.File
	maxSize int64
}

// NewSQDEventLog creates a new event logger writing to the given directory.
func NewSQDEventLog(logDir string) (*SQDEventLog, error) {
	return NewSQDEventLogWithMaxSize(logDir, DefaultSQDEventLogMaxSize)
}

// NewSQDEventLogWithMaxSize creates an event logger with a custom rotation size.
func NewSQDEventLogWithMaxSize(logDir string, maxSize int64) (*SQDEventLog, error) {
	if maxSize <= 0 {
		maxSize = DefaultSQDEventLogMaxSize
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("create sqd events log dir: %w", err)
	}
	path := filepath.Join(logDir, "sqd-events.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open sqd-events.log: %w", err)
	}
	return &SQDEventLog{file: file, maxSize: maxSize}, nil
}

// Close flushes and closes the log file.
func (l *SQDEventLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// rotateIfNeeded renames the current log file and opens a fresh one when the
// size threshold is exceeded. Caller must hold l.mu.
func (l *SQDEventLog) rotateIfNeeded() {
	if l.file == nil {
		return
	}
	info, err := l.file.Stat()
	if err != nil || info.Size() < l.maxSize {
		return
	}
	path := l.file.Name()
	ts := time.Now().Format("20060102_150405")
	archive := path[:len(path)-len(".log")] + "-" + ts + ".log"
	_ = l.file.Close()
	if err := os.Rename(path, archive); err != nil {
		// Rename failed (e.g. file locked): reopen the original path.
		file, openErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if openErr != nil {
			return
		}
		l.file = file
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	l.file = file
}

// Log writes an event to the log file.
func (l *SQDEventLog) Log(event SQDEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	l.rotateIfNeeded()
	event.Timestamp = event.Timestamp.UTC()
	line := formatSQDEvent(event)
	l.file.WriteString(line + "\n")
}

func formatSQDEvent(e SQDEvent) string {
	base := fmt.Sprintf("%s SQD %s %s",
		e.Timestamp.Format("2006-01-02 15:04:05"),
		e.Level,
		e.Event,
	)
	if e.HTTPCode > 0 {
		base += fmt.Sprintf(" HTTP %d", e.HTTPCode)
	}
	if e.Retry > 0 {
		base += fmt.Sprintf(" retry=%d", e.Retry)
	}
	if e.Backoff != "" {
		base += fmt.Sprintf(" backoff=%s", e.Backoff)
	}
	if e.Workers > 0 {
		base += fmt.Sprintf(" workers=%d", e.Workers)
	}
	if e.Error != "" {
		base += fmt.Sprintf(" error=%s", e.Error)
	}
	if e.Message != "" {
		base += fmt.Sprintf(" %s", e.Message)
	}
	return base
}

// Convenience methods for common event types.

// LogRetry logs a retry attempt.
func (l *SQDEventLog) LogRetry(attempt int, backoff time.Duration, err error) {
	l.Log(SQDEvent{
		Level:   "WARN",
		Event:   "retry",
		Retry:   attempt,
		Backoff: backoff.String(),
		Error:   err.Error(),
		Message: fmt.Sprintf("Retry attempt %d after %v", attempt, backoff),
	})
}

// Log503 logs a 503 No Available Workers event.
func (l *SQDEventLog) Log503(workers int, message string) {
	l.Log(SQDEvent{
		Level:    "ERROR",
		Event:    "503_no_workers",
		HTTPCode: 503,
		Workers:  workers,
		Message:  message,
	})
}

// Log429 logs a rate limit event.
func (l *SQDEventLog) Log429(message string) {
	l.Log(SQDEvent{
		Level:    "WARN",
		Event:    "429_rate_limited",
		HTTPCode: 429,
		Message:  message,
	})
}

// LogCircuitOpen logs when the circuit breaker opens.
func (l *SQDEventLog) LogCircuitOpen(failureCount int, cooldown time.Duration) {
	l.Log(SQDEvent{
		Level:   "ERROR",
		Event:   "circuit_open",
		Retry:   failureCount,
		Backoff: cooldown.String(),
		Message: fmt.Sprintf("Circuit breaker OPEN after %d consecutive failures, cooldown %v", failureCount, cooldown),
	})
}

// LogCircuitHalfOpen logs when the circuit transitions to half-open.
func (l *SQDEventLog) LogCircuitHalfOpen() {
	l.Log(SQDEvent{
		Level:   "WARN",
		Event:   "circuit_half_open",
		Message: "Circuit breaker HALF_OPEN — probing",
	})
}

// LogCircuitRecovery logs when the circuit recovers.
func (l *SQDEventLog) LogCircuitRecovery() {
	l.Log(SQDEvent{
		Level:   "INFO",
		Event:   "circuit_recovery",
		Message: "Circuit breaker recovered to NORMAL",
	})
}

// LogWorkerScale logs a worker pool scale event.
func (l *SQDEventLog) LogWorkerScale(from, to int, reason string) {
	l.Log(SQDEvent{
		Level:   "WARN",
		Event:   "worker_scale",
		Workers: to,
		Message: fmt.Sprintf("Worker pool scaled: %d→%d (%s)", from, to, reason),
	})
}

// LogDNSFailure logs a DNS resolution failure.
func (l *SQDEventLog) LogDNSFailure(err error) {
	l.Log(SQDEvent{
		Level:   "ERROR",
		Event:   "dns_failure",
		Error:   err.Error(),
		Message: "DNS resolution failed",
	})
}

// LogTimeout logs a timeout event.
func (l *SQDEventLog) LogTimeout(duration time.Duration) {
	l.Log(SQDEvent{
		Level:   "WARN",
		Event:   "timeout",
		Backoff: duration.String(),
		Message: fmt.Sprintf("Request timed out after %v", duration),
	})
}

// LogRecovery logs a general recovery event.
func (l *SQDEventLog) LogRecovery(message string) {
	l.Log(SQDEvent{
		Level:   "INFO",
		Event:   "recovery",
		Message: message,
	})
}
