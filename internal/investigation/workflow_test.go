package investigation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/analyticsapi"
)

// ── V2.1 RC2: 调查工作流与资金追踪验证 ──
// 启用：创建 stress-data/bsc_real/.investigation.enabled

const (
	flagInvestigation = ".investigation.enabled"
	targetContract    = "0x55d398326f99059ff775485246999027b3197955" // USDT 合约（高频）
	targetActive      = "0x238a358808379702088667322f80ac48bad5e6c4"  // 活跃交易方
)

type invReport struct {
	Timestamp    time.Time          `json:"timestamp"`
	SingleAddress map[string]any    `json:"single_address"`
	Trace         map[string]any    `json:"trace_funds"`
	Relations     map[string]any    `json:"relations"`
	RiskScenario  map[string]any    `json:"risk_scenario"`
	Reproducible  bool              `json:"reproducible"`
	Perf          map[string]any    `json:"performance"`
	Passed        bool              `json:"passed"`
}

func newInvestigationTest(t *testing.T) (*Investigator, *analyticsapi.Service, string) {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(repoRoot, "stress-data", "bsc_real")
	if _, err := os.Stat(filepath.Join(dataRoot, flagInvestigation)); err != nil {
		t.Skip("create " + filepath.Join(dataRoot, flagInvestigation) + " to enable investigation validation")
	}
	engine := duckdb.Open(repoRoot, dataRoot, duckdb.AnalyticsConfig{})
	if !engine.Available() {
		t.Fatalf("DuckDB 不可用: %+v", engine.Status())
	}
	parquetPath := filepath.Join(dataRoot, "sqd-200k-warehouse", "logs.parquet")
	if _, err := os.Stat(parquetPath); err != nil {
		t.Skipf("warehouse 数据不存在: %v", err)
	}
	svc := analyticsapi.New(engine, parquetPath)
	return New(svc, engine, parquetPath), svc, dataRoot
}

func writeInvReport(dir string, r *invReport, t *testing.T) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	path := filepath.Join(dir, "investigation-report.json")
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
	t.Logf("报告已生成: %s", path)
}

// TestInvestigation_SingleAddress 单地址调查全流程。
func TestInvestigation_SingleAddress(t *testing.T) {
	inv, _, dataRoot := newInvestigationTest(t)
	ctx := context.Background()

	start := time.Now()
	summary, err := inv.Investigate(ctx, targetActive)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("investigate: %v", err)
	}
	if summary.Profile == nil || summary.Risk == nil {
		t.Fatal("画像/风险缺失")
	}
	if summary.Profile.TransactionCount <= 0 {
		t.Errorf("交易数异常: %d", summary.Profile.TransactionCount)
	}
	if summary.AddressType == "" {
		t.Error("地址类型缺失")
	}
	if summary.PathCount <= 0 {
		t.Errorf("路径数应 > 0: %d", summary.PathCount)
	}
	t.Logf("=== 单地址调查（%v）===", elapsed.Round(time.Millisecond))
	t.Logf("  类型=%s tx=%d in=%d out=%d top_token=%s risk=%.1f(%s) paths=%d related=%d",
		summary.AddressType, summary.Profile.TransactionCount, summary.InCount, summary.OutCount,
		summary.TopToken, summary.Risk.RiskScore, summary.Risk.RiskLevel, summary.PathCount, summary.RelatedCount)
	t.Logf("  耗时: %v", summary.QueryDuration)

	writeInvReport(filepath.Join(dataRoot, "..", "..", "benchmark"), &invReport{
		Timestamp: time.Now().UTC(),
		SingleAddress: map[string]any{
			"type": summary.AddressType, "tx": summary.Profile.TransactionCount,
			"in": summary.InCount, "out": summary.OutCount,
			"risk": summary.Risk.RiskScore, "paths": summary.PathCount,
			"duration_ms": elapsed.Milliseconds(),
		},
		Passed: true,
	}, t)

	// 完整调查证据（snapshots/evidence.json + paths.csv + related_addresses.csv）
	tracePaths, err := inv.TraceFunds(ctx, targetActive, 3)
	if err != nil {
		t.Fatalf("trace for evidence: %v", err)
	}
	riskEvidence, err := inv.RiskScenario(ctx, targetActive)
	if err != nil {
		t.Fatalf("risk for evidence: %v", err)
	}
	relatedAll, err := inv.DiscoverRelations(ctx, []string{targetActive}, 20)
	if err != nil {
		t.Fatalf("related for evidence: %v", err)
	}
	evidence := &Evidence{
		Timestamp:  time.Now().UTC(),
		Target:     targetActive,
		Summary:    summary,
		TracePaths: tracePaths,
		Risk:       riskEvidence,
		Related:    relatedAll,
	}
	if err := GenerateReport(filepath.Join(dataRoot, "..", "..", "benchmark"), evidence, relatedAll, t); err != nil {
		t.Errorf("生成证据: %v", err)
	}
}

// TestInvestigation_TraceFunds 多跳资金追踪。
func TestInvestigation_TraceFunds(t *testing.T) {
	inv, _, dataRoot := newInvestigationTest(t)
	ctx := context.Background()

	paths, err := inv.TraceFunds(ctx, targetActive, 3)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("路径为空")
	}
	noCycle := true
	maxHops := 0
	for _, p := range paths {
		if len(p.Nodes) > maxHops {
			maxHops = len(p.Nodes)
		}
		seen := map[string]bool{}
		for _, n := range p.Nodes {
			if seen[n] {
				noCycle = false
			}
			seen[n] = true
		}
	}
	if !noCycle {
		t.Error("存在自环路径")
	}
	t.Logf("=== 多跳资金追踪 ===  路径 %d 条，最大深度 %d，无环=%v", len(paths), maxHops, noCycle)
	for i := 0; i < 3 && i < len(paths); i++ {
		t.Logf("  %s", strings.Join(paths[i].Nodes, " → "))
	}
	writeInvReport(filepath.Join(dataRoot, "..", "..", "benchmark"), &invReport{
		Timestamp: time.Now().UTC(),
		Trace: map[string]any{
			"paths": len(paths), "max_depth": maxHops, "no_cycle": noCycle,
		},
		Passed: noCycle,
	}, t)
	if !noCycle {
		t.Error("资金追踪验证未通过")
	}
}

// TestInvestigation_Relations 地址关联发现。
func TestInvestigation_Relations(t *testing.T) {
	inv, _, dataRoot := newInvestigationTest(t)
	ctx := context.Background()

	related, err := inv.DiscoverRelations(ctx, []string{targetContract, targetActive}, 10)
	if err != nil {
		t.Fatalf("relations: %v", err)
	}
	if len(related) == 0 {
		t.Fatal("关联地址为空")
	}
	t.Logf("=== 关联地址发现 ===  Top10（score 降序）")
	for _, r := range related[:min(5, len(related))] {
		t.Logf("  %s score=%.3f shared=%d", r.Address, r.Score, r.SharedCounterparties)
	}
	writeInvReport(filepath.Join(dataRoot, "..", "..", "benchmark"), &invReport{
		Timestamp: time.Now().UTC(),
		Relations: map[string]any{"found": len(related)},
		Passed:    len(related) > 0,
	}, t)
}

// TestInvestigation_RiskScenario 高风险调查场景。
func TestInvestigation_RiskScenario(t *testing.T) {
	inv, _, dataRoot := newInvestigationTest(t)
	ctx := context.Background()

	evidence, err := inv.RiskScenario(ctx, targetActive)
	if err != nil {
		t.Fatalf("risk scenario: %v", err)
	}
	if evidence.Risk == nil {
		t.Fatal("风险缺失")
	}
	t.Logf("=== 风险调查场景 ===")
	t.Logf("  模式: %s", evidence.Pattern)
	t.Logf("  大额转入 %d 笔，快速转出 %d 笔，分散目标 %d 个", len(evidence.LargeInflows), len(evidence.RapidOutflows), len(evidence.SpreadTargets))
	for _, e := range evidence.LargeInflows[:min(3, len(evidence.LargeInflows))] {
		t.Logf("  转入: %s → %s %s（block %s）", e.From, e.To, e.Amount, e.Block)
	}
	writeInvReport(filepath.Join(dataRoot, "..", "..", "benchmark"), &invReport{
		Timestamp: time.Now().UTC(),
		RiskScenario: map[string]any{
			"pattern": evidence.Pattern, "large_in": len(evidence.LargeInflows),
			"rapid_out": len(evidence.RapidOutflows), "spread": len(evidence.SpreadTargets),
		},
		Passed: len(evidence.LargeInflows) > 0,
	}, t)
}

// TestInvestigation_ReproducibleAndPerf 可复现性 + 性能（单地址/100/1000）。
func TestInvestigation_ReproducibleAndPerf(t *testing.T) {
	inv, _, dataRoot := newInvestigationTest(t)
	ctx := context.Background()

	// 可复现：两次 Investigate 一致
	s1, err := inv.Investigate(ctx, targetActive)
	if err != nil {
		t.Fatalf("investigate 1: %v", err)
	}
	s2, err := inv.Investigate(ctx, targetActive)
	if err != nil {
		t.Fatalf("investigate 2: %v", err)
	}
	reproducible := s1.Profile.EventCount == s2.Profile.EventCount &&
		s1.PathCount == s2.PathCount && s1.InCount == s2.InCount
	if !reproducible {
		t.Error("调查结果不可复现")
	}

	// 性能：100/1000 地址批量调查（每个地址 profile+risk）
	perf := map[string]any{}
	loadAddr := func() []string {
		content, _ := os.ReadFile(filepath.Join(dataRoot, "addresses_accumulated.csv"))
		var out []string
		for _, line := range strings.Split(string(content), "\n") {
			a := strings.ToLower(strings.TrimSpace(line))
			if len(a) == 42 && strings.HasPrefix(a, "0x") {
				out = append(out, a)
			}
		}
		return out
	}
	addrs := loadAddr()
	for _, size := range []int{100, 1000} {
		if size > len(addrs) {
			size = len(addrs)
		}
		start := time.Now()
		for _, a := range addrs[:size] {
			if _, err := inv.svc.Profile(ctx, a); err != nil {
				t.Fatalf("batch profile: %v", err)
			}
		}
		dur := time.Since(start)
		perf[fmt.Sprintf("batch_%d", size)] = map[string]any{
			"ms": dur.Milliseconds(), "avg_ms_per_addr": float64(dur.Milliseconds()) / float64(size),
		}
		t.Logf("  批量 %d 地址调查: %v（平均 %.1fms/地址）", size, dur.Round(time.Millisecond), float64(dur.Milliseconds())/float64(size))
	}

	writeInvReport(filepath.Join(dataRoot, "..", "..", "benchmark"), &invReport{
		Timestamp: time.Now().UTC(),
		Reproducible: reproducible,
		Perf:         perf,
		Passed:       reproducible,
	}, t)
	if !reproducible {
		t.Error("可复现性验证未通过")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
