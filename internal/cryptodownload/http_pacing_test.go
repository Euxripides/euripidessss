package cryptodownload

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPHostPacerWaitEnforcesInterval(t *testing.T) {
	var nowNano atomic.Int64
	started := time.Now()
	nowNano.Store(started.UnixNano())
	pacer := newHTTPHostPacer(100*time.Millisecond, time.Second)
	pacer.now = func() time.Time { return time.Unix(0, nowNano.Load()) }

	if err := pacer.wait(context.Background(), "oklink.com"); err != nil {
		t.Fatal(err)
	}
	// Second wait must be delayed until the first interval elapses.
	nowNano.Store(started.Add(50 * time.Millisecond).UnixNano())
	done := make(chan error, 1)
	go func() { done <- pacer.wait(context.Background(), "oklink.com") }()
	select {
	case <-done:
		t.Fatal("second wait returned before interval elapsed")
	case <-time.After(80 * time.Millisecond):
	}
	nowNano.Store(started.Add(300 * time.Millisecond).UnixNano())
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("second wait did not return after interval elapsed")
	}
}

func TestHTTPHostPacerExemptsLocalhost(t *testing.T) {
	pacer := newHTTPHostPacer(100*time.Millisecond, time.Second)
	if shouldPaceHTTPHost("localhost") {
		t.Fatal("localhost must not be paced")
	}
	if shouldPaceHTTPHost("127.0.0.1") {
		t.Fatal("loopback IP must not be paced")
	}
	if shouldPaceHTTPHost("192.168.1.5") {
		t.Fatal("private IP must not be paced")
	}
	if !shouldPaceHTTPHost("oklink.com") {
		t.Fatal("external host must be paced")
	}
	if pacer.pacedInterval() == 0 {
		t.Fatal("expected non-zero interval")
	}
}

func TestHTTPHostPacerJitterWithinRange(t *testing.T) {
	pacer := newHTTPHostPacer(100*time.Millisecond, time.Second)
	for i := 0; i < 50; i++ {
		got := pacer.pacedInterval()
		if got < 100*time.Millisecond || got >= 150*time.Millisecond {
			t.Fatalf("jitter out of range: %v", got)
		}
	}
	if zero := newHTTPHostPacer(0, 0).pacedInterval(); zero != 0 {
		t.Fatalf("zero interval must stay zero, got %v", zero)
	}
}

func TestParseHTTPRetryAfter(t *testing.T) {
	now := time.Now()
	cases := []struct {
		header string
		want   time.Duration
		ok     bool
	}{
		{"30", 30 * time.Second, true},
		{"0", 0, true},
		{"-5", 0, false},
		{"abc", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseHTTPRetryAfter(tc.header, now)
		if ok != tc.ok {
			t.Fatalf("parseHTTPRetryAfter(%q) ok=%v, want %v", tc.header, ok, tc.ok)
		}
		if ok && got != tc.want {
			t.Fatalf("parseHTTPRetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
	// HTTP-date form: future date yields the remaining delta, past yields zero.
	future := now.Add(90 * time.Second).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
	delta, ok := parseHTTPRetryAfter(future, now)
	if !ok || delta <= 89*time.Second || delta > 90*time.Second {
		t.Fatalf("future HTTP-date = %v ok=%v, want ~90s", delta, ok)
	}
	past := now.Add(-10 * time.Second).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
	delta, ok = parseHTTPRetryAfter(past, now)
	if !ok || delta != 0 {
		t.Fatalf("past HTTP-date = %v ok=%v, want 0 true", delta, ok)
	}
}

func TestHTTPHostPacerObserve429Cooldown(t *testing.T) {
	now := time.Now()
	pacer := newHTTPHostPacer(50*time.Millisecond, 30*time.Second)
	pacer.now = func() time.Time { return now }
	pacer.observe("oklink.com", http.StatusTooManyRequests, "")
	pacer.mu.Lock()
	allowed := pacer.nextAllowed["oklink.com"]
	pacer.mu.Unlock()
	want := now.Add(30 * time.Second)
	if !allowed.Equal(want) {
		t.Fatalf("expected 429 cooldown until %v, got %v", want, allowed)
	}
}

func TestHTTPHostPacerObserveRetryAfterOverridesCooldown(t *testing.T) {
	now := time.Now()
	pacer := newHTTPHostPacer(50*time.Millisecond, 30*time.Second)
	pacer.now = func() time.Time { return now }
	pacer.observe("oklink.com", http.StatusTooManyRequests, "5")
	pacer.mu.Lock()
	allowed := pacer.nextAllowed["oklink.com"]
	pacer.mu.Unlock()
	want := now.Add(5 * time.Second)
	if !allowed.Equal(want) {
		t.Fatalf("expected Retry-After cooldown until %v, got %v", want, allowed)
	}
}
