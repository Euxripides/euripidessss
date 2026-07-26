package cryptodownload

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitState holds rate-limit signals parsed from HTTP response headers.
type RateLimitState struct {
	Limit     int
	Remaining int
	Reset     time.Time
}

// ParseRateLimitHeaders extracts rate-limit information from response headers.
// Recognises X-RateLimit-Limit, X-RateLimit-Remaining, and X-RateLimit-Reset
// (Unix timestamp in seconds).
func ParseRateLimitHeaders(header http.Header) *RateLimitState {
	state := &RateLimitState{}
	if v := header.Get("X-RateLimit-Limit"); v != "" {
		state.Limit, _ = strconv.Atoi(strings.TrimSpace(v))
	}
	if v := header.Get("X-RateLimit-Remaining"); v != "" {
		state.Remaining, _ = strconv.Atoi(strings.TrimSpace(v))
	}
	if v := header.Get("X-RateLimit-Reset"); v != "" {
		if sec, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			state.Reset = time.Unix(sec, 0)
		}
	}
	return state
}

// Exhausted reports whether the rate-limit window has no remaining capacity.
func (s *RateLimitState) Exhausted() bool {
	return s.Limit > 0 && s.Remaining <= 0
}

// NearLimit reports whether the rate-limit window is below the given fraction (e.g. 0.2 for 20%).
func (s *RateLimitState) NearLimit(fraction float64) bool {
	if s.Limit <= 0 {
		return false
	}
	return float64(s.Remaining) < float64(s.Limit)*fraction
}

// DurationUntilReset returns how long to wait before the rate-limit window resets.
// Returns 0 when Reset is in the past or unknown.
func (s *RateLimitState) DurationUntilReset(now time.Time) time.Duration {
	if s.Reset.IsZero() || !s.Reset.After(now) {
		return 0
	}
	return s.Reset.Sub(now)
}

// observeRateLimitHeaders parses rate-limit signals from response headers and
// adjusts shared pacing when capacity is low.
func observeRateLimitHeaders(header http.Header) {
	state := ParseRateLimitHeaders(header)
	if state == nil || state.Limit <= 0 {
		return
	}
	if state.Exhausted() {
		wait := state.DurationUntilReset(time.Now())
		if wait > 0 && wait < 10*time.Second {
			time.Sleep(wait)
		}
	}
}
