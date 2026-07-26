package cryptodownload

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RateLimitEvent records a single rate-limit or server-error occurrence for
// post-hoc analysis of which endpoints trigger throttling.
type RateLimitEvent struct {
	Timestamp   string `json:"ts"`
	Host        string `json:"host"`
	Path        string `json:"path"`
	Status      int    `json:"status"`
	Attempt     int    `json:"attempt"`
	Backoff     string `json:"backoff,omitempty"`
	RetryAfter  string `json:"retry_after,omitempty"`
	Remaining   int    `json:"remaining,omitempty"`
	Description string `json:"description,omitempty"`
}

var (
	eventLogMu   sync.Mutex
	eventLogPath string
)

// SetEventLogPath configures the destination file for rate-limit event logs.
// The file is written as JSON Lines (one object per line).
func SetEventLogPath(path string) {
	eventLogMu.Lock()
	defer eventLogMu.Unlock()
	eventLogPath = path
}

// LogRateLimitEvent appends a rate-limit event to the configured log file.
// Each call writes one JSON line; concurrent calls are serialised via mutex.
func LogRateLimitEvent(event RateLimitEvent) {
	eventLogMu.Lock()
	defer eventLogMu.Unlock()
	if eventLogPath == "" {
		return
	}
	event.Timestamp = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.Marshal(event)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(eventLogPath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(eventLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, string(encoded))
}
