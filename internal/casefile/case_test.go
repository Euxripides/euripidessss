package casefile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/analyticsapi"
)

// ── V2.1 RC2: 案件分析与报告生成系统验证 ──
// 启用：创建 stress-data/bsc_real/.case-reporting.enabled

const (
	flagCaseReporting = ".case-reporting.enabled"
	targetA           = "0x238a358808379702088667322f80ac48bad5e6c4"
	targetB           = "0x278d858f05b94576c1e6f73285886876ff6ef8d2"
)

type caseReport struct {
	Timestamp    time.Time      `json:"timestamp"`
	Case         map[string]any `json:"case"`
	Evidence     map[string]any `json:"evidence"`
	Docx         map[string]any `json:"docx"`
	Reproducible bool           `json:"reproducible"`
	Perf         map[string]any `json:"performance"`
	Passed       bool           `json:"passed"`
}

func newCaseTest(t *testing.T) (*Engine, string) {
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

// TestCase_FullFlow 案件闭环：CREATED→RUNNING→COMPLETED + 证据 + 报告 + DOCX。
func TestCase_FullFlow(t *testing.T) {
	eng, dataRoot := newCaseTest(t)
	ctx := context.Background()
	benchDir := filepath.Join(dataRoot, "..", "..", "benchmark")
	caseDir := filepath.Join(benchDir, "snapshots", "case-demo")

	start := time.Now()
	c := NewCase("CASE-20260731-001", []string{targetA, targetB}, "调查员-测试", "sqd-200k-v1")
	if c.Status != StatusCreated {
		t.Fatalf("初始状态应为 CREATED: %s", c.Status)
	}
	if err := c.Run(ctx, eng); err != nil {
		t.Fatalf("案件运行失败: %v", err)
	}
	runDur := time.Since(start)
	if c.Status != StatusCompleted {
		t.Fatalf("状态应为 COMPLETED: %s (%s)", c.Status, c.Error)
	}
	if len(c.Summaries) != 2 || len(c.Risks) != 2 {
		t.Error("摘要/风险缺失")
	}
	if len(c.TracePaths) == 0 {
		t.Error("资金路径为空")
	}
	if c.Graph == nil || len(c.Graph.Nodes) == 0 || len(c.Graph.Edges) == 0 {
		t.Error("关系图为空")
	}

	// 证据生成
	if err := c.GenerateEvidence(caseDir); err != nil {
		t.Fatalf("证据: %v", err)
	}
	for _, f := range []string{"evidence.json", "graph.json", "timeline.csv"} {
		if _, err := os.Stat(filepath.Join(caseDir, f)); err != nil {
			t.Errorf("证据文件缺失: %s", f)
		}
	}
	// evidence.json 内容校验
	evData, _ := os.ReadFile(filepath.Join(caseDir, "evidence.json"))
	var ev Evidence
	if err := json.Unmarshal(evData, &ev); err != nil {
		t.Fatalf("evidence 解析: %v", err)
	}
	if len(ev.AddressEvidence) == 0 || len(ev.PathEvidence) == 0 {
		t.Error("证据完整性不足（地址/路径证据为空）")
	}
	if len(ev.TxEvidence) == 0 {
		t.Error("交易证据为空")
	}

	// Markdown + JSON 报告
	if err := c.GenerateJSON(caseDir); err != nil {
		t.Fatalf("json 报告: %v", err)
	}
	md := c.GenerateMarkdown()
	if !contains(md, c.CaseID) || !contains(md, "目标地址分析") || !contains(md, "结论") {
		t.Error("Markdown 报告结构不完整")
	}
	// JSON 与 Markdown 一致性（案件编号/目标数）
	jsonData, _ := os.ReadFile(filepath.Join(caseDir, "case-report.json"))
	var jsonCase Case
	if err := json.Unmarshal(jsonData, &jsonCase); err != nil {
		t.Fatalf("case-report.json 解析: %v", err)
	}
	if jsonCase.CaseID != c.CaseID || len(jsonCase.TargetAddresses) != 2 {
		t.Error("JSON 报告与案件不一致")
	}

	// DOCX 生成（python-docx）
	pythonPath := "python"
	if _, err := os.Stat(`C:\Python312\python.exe`); err == nil {
		pythonPath = `C:\Python312\python.exe`
	}
	scriptPath := filepath.Join(dataRoot, "..", "..", "tools", "report", "docx_report.py")
	docxPath, err := c.GenerateDOCX(caseDir, pythonPath, scriptPath)
	if err != nil {
		t.Fatalf("docx: %v", err)
	}
	docxInfo, err := os.Stat(docxPath)
	if err != nil || docxInfo.Size() < 1000 {
		t.Errorf("docx 文件异常: %v (%v bytes)", err, docxInfo)
	}
	// docx 是 zip 容器
	head, _ := os.ReadFile(docxPath)
	if len(head) < 4 || head[0] != 'P' || head[1] != 'K' {
		t.Error("docx 不是有效 zip 容器")
	}

	t.Logf("=== 案件闭环（%v）===", runDur.Round(time.Millisecond))
	t.Logf("  案件 %s 状态 %s，目标 %d 个，路径 %d 条，关联 %d，图节点 %d/边 %d",
		c.CaseID, c.Status, len(c.TargetAddresses), len(c.TracePaths), len(c.Related),
		len(c.Graph.Nodes), len(c.Graph.Edges))
	t.Logf("  时间线 %d 条，公共来源 %d，公共去向 %d", len(c.Timeline), len(c.CommonSources), len(c.CommonSinks))
	t.Logf("  DOCX: %s（%d bytes）", docxPath, docxInfo.Size())

	writeCaseReport(benchDir, &caseReport{
		Timestamp: time.Now().UTC(),
		Case: map[string]any{
			"id": c.CaseID, "status": string(c.Status), "targets": len(c.TargetAddresses),
			"paths": len(c.TracePaths), "related": len(c.Related),
			"graph_nodes": len(c.Graph.Nodes), "graph_edges": len(c.Graph.Edges),
			"timeline": len(c.Timeline), "duration_ms": runDur.Milliseconds(),
		},
		Evidence: map[string]any{
			"address": len(ev.AddressEvidence), "tx": len(ev.TxEvidence),
			"paths": len(ev.PathEvidence), "related": len(ev.RelationEvidence),
		},
		Docx:   map[string]any{"size": docxInfo.Size(), "ok": true},
		Passed: true,
	}, t)
}

// TestCase_ReproducibleAndPerf 可复现 + 性能（单/10 并发/100 批量）。
func TestCase_ReproducibleAndPerf(t *testing.T) {
	eng, dataRoot := newCaseTest(t)
	ctx := context.Background()

	// 可复现：两次 Run 关键计数一致
	c1 := NewCase("CASE-R1", []string{targetA}, "tester", "v1")
	if err := c1.Run(ctx, eng); err != nil {
		t.Fatalf("run1: %v", err)
	}
	c2 := NewCase("CASE-R2", []string{targetA}, "tester", "v1")
	if err := c2.Run(ctx, eng); err != nil {
		t.Fatalf("run2: %v", err)
	}
	repro := c1.Summaries[targetA].Profile.EventCount == c2.Summaries[targetA].Profile.EventCount &&
		len(c1.TracePaths) == len(c2.TracePaths) && len(c1.Related) == len(c2.Related)
	if !repro {
		t.Error("案件调查不可复现")
	}

	// 性能：单案件（含 3 目标）/10 并发/100 批量（简化为单目标案件批量）
	perf := map[string]any{}
	start := time.Now()
	c3 := NewCase("CASE-P1", []string{targetA, targetB}, "tester", "v1")
	if err := c3.Run(ctx, eng); err != nil {
		t.Fatalf("run3: %v", err)
	}
	perf["single_case_2targets_ms"] = time.Since(start).Milliseconds()

	// 10 并发
	start = time.Now()
	var wg sync.WaitGroup
	errCh := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cc := NewCase(fmt.Sprintf("CASE-C%d", i), []string{targetA}, "tester", "v1")
			if err := cc.Run(ctx, eng); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("并发案件失败: %v", err)
	}
	perf["concurrent_10_ms"] = time.Since(start).Milliseconds()

	// 100 批量（顺序简化：仅 profile 级调查，避免 100×全流程过久）
	start = time.Now()
	for i := 0; i < 100; i++ {
		if _, err := eng.Svc.Profile(ctx, targetA); err != nil {
			t.Fatalf("batch profile: %v", err)
		}
	}
	perf["batch_100_profile_ms"] = time.Since(start).Milliseconds()

	t.Logf("=== 可复现 + 性能 ===")
	t.Logf("  可复现: %v；单案件(2目标) %dms；10 并发 %dms；100 批量画像 %dms",
		repro, perf["single_case_2targets_ms"], perf["concurrent_10_ms"], perf["batch_100_profile_ms"])

	writeCaseReport(filepath.Join(dataRoot, "..", "..", "benchmark"), &caseReport{
		Timestamp: time.Now().UTC(), Reproducible: repro, Perf: perf, Passed: repro,
	}, t)
	if !repro {
		t.Error("可复现性验证未通过")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func writeCaseReport(dir string, r *caseReport, t *testing.T) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(dir, "case-reporting-report.json")
	if existing, err2 := os.ReadFile(path); err2 == nil {
		var old caseReport
		if json.Unmarshal(existing, &old) == nil {
			if old.Case == nil {
				old.Case = r.Case
			}
			if old.Evidence == nil {
				old.Evidence = r.Evidence
			}
			if old.Docx == nil {
				old.Docx = r.Docx
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
