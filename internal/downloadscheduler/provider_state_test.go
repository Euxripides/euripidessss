package downloadscheduler

import (
	"errors"
	"testing"
	"time"
)

func TestClassifyProviderError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		httpStatus int
		want       ProviderState
	}{
		{"http429", nil, 429, ProviderRateLimited},
		{"http403", nil, 403, ProviderRiskControlled},
		{"http401", nil, 401, ProviderAuthBlocked},
		{"http503", nil, 503, ProviderDegraded},
		{"rateLimitText", errors.New("rate limit exceeded"), 0, ProviderRateLimited},
		{"captcha", errors.New("captcha required"), 0, ProviderRiskControlled},
		{"auth", errors.New("unauthorized apikey"), 0, ProviderAuthBlocked},
		{"timeout", errors.New("context deadline exceeded"), 0, ProviderDegraded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifyProviderError(c.err, c.httpStatus); got != c.want {
				t.Fatalf("ClassifyProviderError() = %s, want %s", got, c.want)
			}
		})
	}
}

func TestProviderHealthTrackerBreaker(t *testing.T) {
	cfg := DefaultProviderHealthConfig()
	cfg.FailureToDegrade = 2
	cfg.FailureToOpen = 3
	cfg.CooldownService = 50 * time.Millisecond
	tr := NewProviderHealthTracker(cfg)

	tr.RecordResult(ProviderSQD, false, ProviderDegraded)
	if got := tr.State(ProviderSQD); got != ProviderDegraded {
		t.Fatalf("1st failure state = %s, want DEGRADED", got)
	}
	tr.RecordResult(ProviderSQD, false, ProviderDegraded)
	if got := tr.State(ProviderSQD); got != ProviderDegraded {
		t.Fatalf("2nd failure state = %s, want DEGRADED", got)
	}
	tr.RecordResult(ProviderSQD, false, ProviderDegraded)
	if got := tr.State(ProviderSQD); got != ProviderCircuitOpen {
		t.Fatalf("3rd failure state = %s, want CIRCUIT_OPEN", got)
	}
	if !tr.Exhausted(ProviderSQD) {
		t.Fatal("circuit open should be exhausted")
	}

	// 冷却到期自动恢复
	time.Sleep(70 * time.Millisecond)
	if got := tr.State(ProviderSQD); got != ProviderHealthy {
		t.Fatalf("after cooldown state = %s, want HEALTHY", got)
	}
	tr.RecordResult(ProviderSQD, true, ProviderHealthy)
	if got := tr.State(ProviderSQD); got != ProviderHealthy {
		t.Fatalf("success state = %s, want HEALTHY", got)
	}
}

func TestProviderHealthTrackerRateLimitOpens(t *testing.T) {
	cfg := DefaultProviderHealthConfig()
	cfg.RateLimitOpenAfter = 2
	tr := NewProviderHealthTracker(cfg)
	tr.RecordResult(ProviderSQD, false, ProviderRateLimited)
	if got := tr.State(ProviderSQD); got != ProviderRateLimited {
		t.Fatalf("1st 429 state = %s, want RATE_LIMITED", got)
	}
	tr.RecordResult(ProviderSQD, false, ProviderRateLimited)
	if got := tr.State(ProviderSQD); got != ProviderCircuitOpen {
		t.Fatalf("2nd 429 state = %s, want CIRCUIT_OPEN", got)
	}
}

func TestNormalProvidersUsable(t *testing.T) {
	healthy := func(k ProviderKind) ProviderState { return ProviderHealthy }
	candidates := []ProviderScore{
		{Provider: ProviderSQD, Tier: TierNormal, Available: true},
		{Provider: ProviderAWS, Tier: TierNormal, Available: true},
		{Provider: ProviderBrowser, Tier: TierNormal, ManualOnly: true},
		{Provider: ProviderSQDCloud, Tier: TierEmergencyCloud},
	}
	if !NormalProvidersUsable(candidates, healthy) {
		t.Fatal("healthy providers should be usable")
	}
	allOpen := func(k ProviderKind) ProviderState {
		if k == ProviderSQDCloud {
			return ProviderHealthy
		}
		return ProviderCircuitOpen
	}
	if NormalProvidersUsable(candidates, allOpen) {
		t.Fatal("all normal providers open should be exhausted (manual/cloud ignored)")
	}
}
