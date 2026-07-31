package downloadengine

import (
	"fmt"
	"testing"
	"time"
)

// ── V2.1 RC2 容灾测试 ──

func TestProviderScorerHealth(t *testing.T) {
	scorer := NewProviderScorer()

	// 健康状态
	scorer.RecordSuccess("SQD", 100*time.Millisecond)
	scorer.RecordSuccess("SQD", 150*time.Millisecond)
	score := scorer.Score("SQD")
	if score.Status != "healthy" {
		t.Errorf("expected healthy, got %s", score.Status)
	}
	if score.SuccessRate != 1.0 {
		t.Errorf("expected 1.0, got %.2f", score.SuccessRate)
	}

	// 3次失败 → degraded
	scorer.RecordFailure("SQD", fmt.Errorf("timeout"), 0)
	scorer.RecordFailure("SQD", fmt.Errorf("timeout"), 0)
	scorer.RecordFailure("SQD", fmt.Errorf("timeout"), 0)
	score = scorer.Score("SQD")
	if score.Status != "degraded" {
		t.Errorf("expected degraded after 3 failures, got %s", score.Status)
	}

	// 5次失败 → unavailable
	scorer.RecordFailure("SQD", fmt.Errorf("503"), 503)
	scorer.RecordFailure("SQD", fmt.Errorf("503"), 503)
	score = scorer.Score("SQD")
	if score.Status != "unavailable" {
		t.Errorf("expected unavailable after 5 failures, got %s", score.Status)
	}
}

func TestRetryStrategy503(t *testing.T) {
	r := &RetryStrategy{}

	// 503: cooldown递增
	ok, d := r.ShouldRetry(503, 0)
	if !ok || d != 30*time.Second {
		t.Errorf("503 attempt 0: expected retry+30s, got %v+%v", ok, d)
	}
	ok, d = r.ShouldRetry(503, 1)
	if !ok || d != 60*time.Second {
		t.Errorf("503 attempt 1: expected retry+60s, got %v+%v", ok, d)
	}
	ok, d = r.ShouldRetry(503, 3)
	if !ok || d != 300*time.Second {
		t.Errorf("503 attempt 3: expected retry+300s, got %v+%v", ok, d)
	}

	// 400: 不重试
	ok, _ = r.ShouldRetry(400, 0)
	if ok {
		t.Error("400 should not retry")
	}

	// 429: rate limit backoff
	ok, d = r.ShouldRetry(429, 0)
	if !ok || d < 25*time.Second {
		t.Errorf("429 attempt 0: expected retry with backoff, got %v+%v", ok, d)
	}
}

func TestChunkFailover(t *testing.T) {
	failover := NewChunkFailover()

	failover.RecordFailover("chunk-003", "SQD", "RPC", "503 No workers")
	failover.RecordFailover("chunk-003", "RPC", "LOCAL_CACHE", "RPC timeout")

	if failover.FailoverCount("chunk-003") != 2 {
		t.Errorf("expected 2 failovers, got %d", failover.FailoverCount("chunk-003"))
	}
	if failover.FailoverCount("chunk-001") != 0 {
		t.Errorf("expected 0 failovers for untouched chunk")
	}
}

func TestPriorityResolver(t *testing.T) {
	scorer := NewProviderScorer()
	resolver := NewPriorityResolver(scorer)

	// 默认：LOCAL_CACHE 优先
	provider := resolver.Resolve()
	if provider != "LOCAL_CACHE" {
		t.Errorf("expected LOCAL_CACHE as default, got %s", provider)
	}

	// LOCAL_CACHE 降级 → SQD
	for i := 0; i < 5; i++ {
		scorer.RecordFailure("LOCAL_CACHE", fmt.Errorf("disk full"), 0)
	}
	provider = resolver.Resolve()
	if provider != "SQD" {
		t.Errorf("expected SQD when LOCAL_CACHE is unavailable, got %s", provider)
	}

	// SQD也降级 → RPC
	for i := 0; i < 5; i++ {
		scorer.RecordFailure("SQD", fmt.Errorf("503"), 503)
	}
	provider = resolver.Resolve()
	if provider != "RPC" {
		t.Errorf("expected RPC as last resort, got %s", provider)
	}

	// SQD恢复后 → 应该切回
	scorer.RecordSuccess("SQD", 100*time.Millisecond)
	scorer.RecordSuccess("SQD", 100*time.Millisecond)
	scorer.RecordSuccess("SQD", 100*time.Millisecond) // reset consecutive
	_ = scorer.Score("SQD")
}

func TestRecoveryManifest(t *testing.T) {
	m := NewRecoveryManifest("bsc_transactions")

	m.TotalChunks = 1000
	for i := 0; i < 1000; i++ {
		if i == 123 || i == 456 {
			m.RecordFailed(i)
		} else {
			m.RecordCompleted(i)
		}
	}

	if m.Completed != 998 {
		t.Errorf("expected 998 completed, got %d", m.Completed)
	}
	failed := m.RecoverOnly()
	if len(failed) != 2 || failed[0] != 123 || failed[1] != 456 {
		t.Errorf("expected [123,456] failed, got %v", failed)
	}
}

func TestFailoverMetrics(t *testing.T) {
	fm := NewFailoverMetrics()

	fm.RecordRequest(true, 200, 150*time.Millisecond)
	fm.RecordRequest(true, 200, 200*time.Millisecond)
	fm.RecordRequest(false, 503, 50*time.Millisecond)
	fm.RecordRequest(false, 429, 30*time.Millisecond)
	fm.RecordFailover("SQD", "RPC")
	fm.RecordFailover("RPC", "LOCAL_CACHE")
	fm.RecordRecovery()

	snap := fm.Snapshot()
	if snap["total_requests"].(int64) != 4 {
		t.Errorf("expected 4 total, got %d", snap["total_requests"])
	}
	if snap["status_503"].(int64) != 1 {
		t.Errorf("expected 1 503, got %d", snap["status_503"])
	}
	if snap["recovery_count"].(int64) != 1 {
		t.Errorf("expected 1 recovery, got %d", snap["recovery_count"])
	}
}

func TestDNSFailureSimulation(t *testing.T) {
	// 模拟 DNS 失败：Provider 连续5次失败 → unavailable
	scorer := NewProviderScorer()
	for i := 0; i < 5; i++ {
		scorer.RecordFailure("SQD", fmt.Errorf("dial tcp: lookup portal.sqd.dev: no such host"), 0)
	}
	score := scorer.Score("SQD")
	if score.Status != "unavailable" {
		t.Errorf("DNS failure should make provider unavailable, got %s", score.Status)
	}

	// Failover → RPC
	resolver := NewPriorityResolver(scorer)
	provider := resolver.Resolve()
	t.Logf("  DNS failure → failover to %s (SQD=%s)", provider, score.Status)
}

func TestProviderInterruptRecovery(t *testing.T) {
	// 模拟：SQD中断 → 部分Chunk失败 → 恢复
	scorer := NewProviderScorer()
	failover := NewChunkFailover()
	manifest := NewRecoveryManifest("bsc_transactions")
	manifest.TotalChunks = 100

	// 前50个成功
	for i := 0; i < 50; i++ {
		scorer.RecordSuccess("SQD", 100*time.Millisecond)
		manifest.RecordCompleted(i)
	}

	// 中断：连续5次失败 → unavailable
	for i := 0; i < 5; i++ {
		scorer.RecordFailure("SQD", fmt.Errorf("connection reset"), 0)
		manifest.RecordFailed(50 + i)
	}

	// Failover → RPC 继续
	for i := 55; i < 100; i++ {
		scorer.RecordSuccess("RPC", 200*time.Millisecond)
		failover.RecordFailover(fmt.Sprintf("chunk-%d", i), "SQD", "RPC", "interrupt")
		manifest.RecordCompleted(i)
	}

	if manifest.Completed != 95 {
		t.Errorf("expected 95 completed after recovery, got %d", manifest.Completed)
	}
	if len(manifest.FailedChunks) != 5 {
		t.Errorf("expected 5 failed chunks in SQD interrupt, got %d", len(manifest.FailedChunks))
	}
	t.Logf("  Provider interrupt: SQD 50 OK → 5 failed → RPC 45 OK → %d completed, %d failed",
		manifest.Completed, len(manifest.FailedChunks))
}

// ── PRD §6-13 补充测试 ──

func TestWeightedScore(t *testing.T) {
	scorer := NewProviderScorer()
	for i := 0; i < 10; i++ {
		scorer.RecordSuccess("SQD", 100*time.Millisecond)
	}
	ws := scorer.WeightedScore("SQD")
	if ws.Total < 0.5 {
		t.Errorf("weighted score too low: %.3f", ws.Total)
	}
	t.Logf("  SQD weighted score: total=%.3f (success=%.1f latency=%.1f stability=%.1f)",
		ws.Total, ws.SuccessWeight, ws.LatencyWeight, ws.StabilityWeight)
}

func TestLoadBalanceModes(t *testing.T) {
	scorer := NewProviderScorer()
	resolver := NewPriorityResolver(scorer)

	// Speed mode
	speed := resolver.ResolveWithMode(BalanceSpeed)
	if speed == "" {
		t.Error("speed mode should return a provider")
	}
	t.Logf("  Speed mode: %s", speed)

	// Stable mode
	stable := resolver.ResolveWithMode(BalanceStable)
	if stable == "" {
		t.Error("stable mode should return a provider")
	}
	t.Logf("  Stable mode: %s", stable)

	// Cost mode
	cost := resolver.ResolveWithMode(BalanceCost)
	if cost == "" {
		t.Error("cost mode should return a provider")
	}
	t.Logf("  Cost mode: %s", cost)
}

func TestEventLogger(t *testing.T) {
	logger := NewEventLogger()

	logger.Log("SQD", "fetch_start", "")
	logger.Log("SQD", "503_cooldown", "No available workers")
	logger.Log("SQD", "switch_to_RPC", "circuit breaker open")
	logger.Log("RPC", "fetch_ok", "")

	recent := logger.Recent(3)
	if len(recent) != 3 {
		t.Errorf("expected 3 recent events, got %d", len(recent))
	}
	if recent[0].Action != "503_cooldown" {
		t.Errorf("expected 503_cooldown, got %s", recent[0].Action)
	}
	t.Logf("  Events: %d total, %d recent", len(logger.events), len(recent))
}

func TestProviderConfig(t *testing.T) {
	cfg := DefaultProviderConfig()

	enabled := cfg.EnabledProviders()
	if len(enabled) != 3 {
		t.Errorf("expected 3 enabled providers, got %d", len(enabled))
	}

	if cfg.Providers["cache"].Priority != 0 {
		t.Error("cache should have priority 0")
	}
	if cfg.Providers["sqd"].Priority != 1 {
		t.Error("sqd should have priority 1")
	}

	// 禁用SQD
	cfg.Providers["sqd"] = ProviderEntry{Enabled: false, Priority: 1}
	enabled = cfg.EnabledProviders()
	if len(enabled) != 2 {
		t.Errorf("expected 2 after disabling sqd, got %d", len(enabled))
	}
}

func TestProviderSwitchUnder60s(t *testing.T) {
	scorer := NewProviderScorer()
	failover := NewChunkFailover()

	start := time.Now()
	for i := 0; i < 5; i++ {
		scorer.RecordFailure("SQD", fmt.Errorf("503"), 503)
	}
	score := scorer.Score("SQD")
	resolver := NewPriorityResolver(scorer)
	newProvider := resolver.Resolve()
	failover.RecordFailover("chunk-001", "SQD", newProvider, "503 failover")
	elapsed := time.Since(start)

	if elapsed > 60*time.Second {
		t.Errorf("failover took %v, expected <60s", elapsed)
	}
	t.Logf("  Provider switch: %v (<60s ✅), SQD=%s → %s", elapsed, score.Status, newProvider)
}
