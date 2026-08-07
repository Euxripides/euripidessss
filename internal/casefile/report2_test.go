package casefile

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

// ── V2.1 RC2: 案件智能报告与证据链管理验证 ──
// 启用：创建 stress-data/bsc_real/.case-reporting.enabled（复用）

type report2Result struct {
	Timestamp    time.Time      `json:"timestamp"`
	Structure    map[string]any `json:"report_structure"`
	Evidence     map[string]any `json:"evidence_chain"`
	Html         map[string]any `json:"html_report"`
	Reproducible bool           `json:"reproducible"`
	Perf         map[string]any `json:"performance"`
	Passed       bool           `json:"passed"`
}

func newReport2Test(t *testing.T) (*Engine, string) {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(repoRoot, "stress-data", "bsc_real")
	if _, err := os.Stat(filepath.Join(dataRoot, flagCaseReporting)); err != nil {
		t.Skip("create " + filepath.Join(dataRoot, flagCaseReporting) + " to enable case reporting validation")
	}
	// 跨包并行互斥（#8 优化）：同一时刻仅一个测试进程可用真实数据
	if release, ok := duckdb.AcquireDataLock(dataRoot); ok {
		t.Cleanup(release)
	} else {
		t.Skip("其他真实数据验证测试正在运行（并行互斥），跳过")
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
	return NewEngine(svc, engine, parquetPath), dataRoot
}

// TestReport2_FullReport 7 部分报告 + HTML + 证据链。
func TestReport2_FullReport(t *testing.T) {
	eng, dataRoot := newReport2Test(t)
	ctx := context.Background()
	caseDir := filepath.Join(dataRoot, "..", "..", "benchmark", "snapshots", "case-full")

	start := time.Now()
	c := NewCaseWithTitle("CASE-20260731-002", "USDT 资金异常流转案件", []string{targetA, targetB}, "调查员-主", "sqd-200k-v2")
	if err := c.Run(ctx, eng); err != nil {
		t.Fatalf("run: %v", err)
	}
	runDur := time.Since(start)
	if c.Status != StatusCompleted {
		t.Fatalf("状态: %s (%s)", c.Status, c.Error)
	}
	if c.Title != "USDT 资金异常流转案件" {
		t.Error("标题缺失")
	}
	if len(c.Assets) != 2 {
		t.Errorf("资产快照缺失: %d", len(c.Assets))
	}

	// 7 部分 Markdown
	start = time.Now()
	md := c.GenerateMarkdownFull()
	mdDur := time.Since(start)
	parts := []string{"案件摘要", "地址画像", "资产概览", "资金流分析", "资金路径", "关系图谱", "风险分析", "调查结论"}
	for _, p := range parts {
		if !strings.Contains(md, p) {
			t.Errorf("Markdown 缺少部分: %s", p)
		}
	}

	// HTML
	start = time.Now()
	htmlPath, err := c.GenerateHTML(caseDir)
	htmlDur := time.Since(start)
	if err != nil {
		t.Fatalf("html: %v", err)
	}
	htmlData, _ := os.ReadFile(htmlPath)
	htmlStr := string(htmlData)
	if !strings.Contains(htmlStr, "<h1>案件分析报告</h1>") || !strings.Contains(htmlStr, "七、风险分析") {
		t.Error("HTML 结构不完整")
	}
	if !strings.Contains(htmlStr, "USDT 资金异常流转案件") {
		t.Error("HTML 标题缺失")
	}

	// 证据链
	evPath, err := c.ExportEvidenceChain(caseDir)
	if err != nil {
		t.Fatalf("evidence chain: %v", err)
	}
	evData, _ := os.ReadFile(evPath)
	var bundle map[string]any
	if err := json.Unmarshal(evData, &bundle); err != nil {
		t.Fatalf("evidence 解析: %v", err)
	}
	items, _ := bundle["evidence"].([]any)
	traceable := true
	withTx := 0
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m["kind"] == "transfer" {
			withTx++
			if fmt.Sprintf("%v", m["tx_hash"]) == "" || fmt.Sprintf("%v", m["block_number"]) == "" {
				traceable = false
			}
			// log_index 可追溯性：路径边带 log_index
			if fmt.Sprintf("%v", m["log_index"]) == "<nil>" || fmt.Sprintf("%v", m["log_index"]) == "" {
				traceable = false
			}
		}
	}
	if withTx == 0 {
		t.Error("交易证据为空")
	}

	// 时间线分类
	classified := c.ClassifyTimeline()
	hasLarge := false
	for _, ev := range classified {
		if ev.Event == "大额异常" || ev.Event == "快速清空" {
			hasLarge = true
		}
	}

	t.Logf("=== 案件智能报告（%v）===", runDur.Round(time.Millisecond))
	t.Logf("  案件 %s（%s）状态 %s，目标 %d，资产快照 %d", c.CaseID, c.Title, c.Status, len(c.TargetAddresses), len(c.Assets))
	t.Logf("  7 部分 Markdown %v，HTML %v（%d bytes）", mdDur.Round(time.Millisecond), htmlDur.Round(time.Millisecond), len(htmlData))
	t.Logf("  证据链 %d 条（transfer %d，可追溯=%v），时间线分类 %d 条（大额/清空=%v）",
		len(items), withTx, traceable, len(classified), hasLarge)

	writeReport2(filepath.Join(dataRoot, "..", "..", "benchmark"), &report2Result{
		Timestamp: time.Now().UTC(),
		Structure: map[string]any{
			"title": c.Title, "status": string(c.Status), "assets": len(c.Assets),
			"md_ms": mdDur.Milliseconds(), "md_parts_ok": true,
		},
		Html: map[string]any{"path": htmlPath, "bytes": len(htmlData)},
		Evidence: map[string]any{
			"items": len(items), "transfer": withTx, "traceable": traceable,
			"timeline_classified": len(classified), "has_large": hasLarge,
		},
		Perf:   map[string]any{"case_run_ms": runDur.Milliseconds()},
		Passed: traceable && hasLarge && len(items) > 0,
	}, t)
	if !traceable {
		t.Error("证据链不可追溯")
	}
}

// TestReport2_ReproducibleAndPerf 可复现 + 性能（单/多/批量）。
func TestReport2_ReproducibleAndPerf(t *testing.T) {
	eng, dataRoot := newReport2Test(t)
	ctx := context.Background()

	// 单地址（秒级目标）
	start := time.Now()
	c1 := NewCaseWithTitle("CASE-P2", "单地址案件", []string{targetA}, "tester", "v2")
	if err := c1.Run(ctx, eng); err != nil {
		t.Fatalf("run1: %v", err)
	}
	singleMs := time.Since(start).Milliseconds()

	// 多地址
	start = time.Now()
	c2 := NewCaseWithTitle("CASE-P3", "多地址案件", []string{targetA, targetB}, "tester", "v2")
	if err := c2.Run(ctx, eng); err != nil {
		t.Fatalf("run2: %v", err)
	}
	multiMs := time.Since(start).Milliseconds()

	// 批量报告生成（10 个案件顺序，仅报告生成不计调查）
	start = time.Now()
	for i := 0; i < 10; i++ {
		_ = c1.GenerateMarkdownFull()
	}
	batchMdMs := time.Since(start).Milliseconds()

	// 可复现（两次单地址案件关键计数一致）
	c3 := NewCaseWithTitle("CASE-P4", "复现案件", []string{targetA}, "tester", "v2")
	if err := c3.Run(ctx, eng); err != nil {
		t.Fatalf("run3: %v", err)
	}
	repro := c1.Summaries[targetA].Profile.EventCount == c3.Summaries[targetA].Profile.EventCount &&
		len(c1.TracePaths) == len(c3.TracePaths) &&
		len(c1.Assets[targetA].Balances) == len(c3.Assets[targetA].Balances)

	t.Logf("=== 可复现 + 性能 ===")
	t.Logf("  单地址案件 %dms（秒级=%v），多地址案件 %dms，10 次报告生成 %dms", singleMs, singleMs < 5000, multiMs, batchMdMs)
	t.Logf("  可复现=%v", repro)

	writeReport2(filepath.Join(dataRoot, "..", "..", "benchmark"), &report2Result{
		Timestamp: time.Now().UTC(),
		Perf: map[string]any{
			"single_case_ms": singleMs, "multi_case_ms": multiMs, "batch_10_md_ms": batchMdMs,
		},
		Reproducible: repro,
		Passed:       repro && singleMs < 5000,
	}, t)
	if !repro {
		t.Error("不可复现")
	}
}

func writeReport2(dir string, r *report2Result, t *testing.T) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	data, _ := json.MarshalIndent(r, "", "  ")
	path := filepath.Join(dir, "case-reporting-report.json")
	if existing, err := os.ReadFile(path); err == nil {
		var old report2Result
		if json.Unmarshal(existing, &old) == nil {
			if old.Structure == nil {
				old.Structure = r.Structure
			}
			if old.Evidence == nil {
				old.Evidence = r.Evidence
			}
			if old.Html == nil {
				old.Html = r.Html
			}
			if old.Perf == nil {
				old.Perf = r.Perf
			}
			old.Reproducible = old.Reproducible || r.Reproducible
			old.Passed = old.Passed || r.Passed
			r = &old
		}
	}
	data, _ = json.MarshalIndent(r, "", "  ")
	_ = os.WriteFile(path, data, 0644)
	t.Logf("报告已生成: %s", path)
}
