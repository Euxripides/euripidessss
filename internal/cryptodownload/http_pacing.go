package cryptodownload

import (
	"context"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultHTTPHostInterval = 350 * time.Millisecond
	defaultHTTP429Cooldown  = 30 * time.Second
)

type httpHostPacer struct {
	mu              sync.Mutex
	nextAllowed     map[string]time.Time
	interval        time.Duration
	defaultCooldown time.Duration
	now             func() time.Time
}

func newHTTPHostPacer(interval, defaultCooldown time.Duration) *httpHostPacer {
	return &httpHostPacer{
		nextAllowed:     make(map[string]time.Time),
		interval:        maxDuration(interval, 0),
		defaultCooldown: maxDuration(defaultCooldown, 0),
		now:             time.Now,
	}
}

func (p *httpHostPacer) wait(ctx context.Context, host string) error {
	host = normalizeHTTPHost(host)
	if host == "" {
		return nil
	}
	for {
		p.mu.Lock()
		now := p.now()
		allowed := p.nextAllowed[host]
		if !now.Before(allowed) {
			p.nextAllowed[host] = now.Add(p.pacedInterval())
			p.mu.Unlock()
			return nil
		}
		wait := allowed.Sub(now)
		p.mu.Unlock()
		if err := waitHTTPPacing(ctx, wait); err != nil {
			return err
		}
	}
}

// pacedInterval returns the base interval with ±50% random jitter to avoid
// harmonic resonance when multiple workers target the same host.
func (p *httpHostPacer) pacedInterval() time.Duration {
	if p.interval <= 0 {
		return 0
	}
	half := p.interval / 2
	if half <= 0 {
		return p.interval
	}
	return p.interval + time.Duration(rand.Int63n(int64(half)))
}

func (p *httpHostPacer) observe(host string, status int, retryAfter string) {
	host = normalizeHTTPHost(host)
	if host == "" {
		return
	}
	now := p.now()
	delay, ok := parseHTTPRetryAfter(retryAfter, now)
	if !ok && status == http.StatusTooManyRequests {
		delay = p.defaultCooldown
	}
	if delay <= 0 {
		return
	}
	next := now.Add(delay)
	p.mu.Lock()
	if next.After(p.nextAllowed[host]) {
		p.nextAllowed[host] = next
	}
	p.mu.Unlock()
}

func waitHTTPPacing(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseHTTPRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	if !when.After(now) {
		return 0, true
	}
	return when.Sub(now), true
}

func normalizeHTTPHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

func maxDuration(value, minimum time.Duration) time.Duration {
	if value < minimum {
		return minimum
	}
	return value
}

// jitterDuration returns value plus a random duration in [0, maxJitter).
func jitterDuration(maxJitter time.Duration) time.Duration {
	if maxJitter <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(maxJitter)))
}

func shouldPaceHTTPHost(host string) bool {
	host = normalizeHTTPHost(host)
	if host == "" || host == "localhost" {
		return false
	}
	if address := net.ParseIP(host); address != nil {
		return !address.IsLoopback() && !address.IsPrivate() && !address.IsUnspecified()
	}
	return true
}

var sharedHTTPHostPacer = newHTTPHostPacer(defaultHTTPHostInterval, defaultHTTP429Cooldown)
