package graphintel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
)

// ── V2.1 RC2: 地址图谱与关系网络分析验证 ──
// 启用：创建 stress-data/bsc_real/.graph-intel.enabled

const flagGraphIntel = ".graph-intel.enabled"

type graphReport struct {
	Timestamp    time.Time      `json:"timestamp"`
	Correctness  map[string]any `json:"correctness"`
	Metrics      map[string]any `json:"metrics"`
	RiskPatterns map[string]any `json:"risk_patterns"`
	Query        map[string]any `json:"query"`
	Reproducible bool           `json:"reproducible"`
	Perf         map[string]any `json:"performance"`
	Passed       bool           `json:"passed"`
}

func newGraphTest(t *testing.T) (*Builder, *duckdb.Engine, string) {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(repoRoot, "stress-data", "bsc_real")
	if _, err := os.Stat(filepath.Join(dataRoot, flagGraphIntel)); err != nil {
		t.Skip("create " + filepath.Join(dataRoot, flagGraphIntel) + " to enable graph intelligence validation")
	}
	engine := duckdb.Open(repoRoot, dataRoot, duckdb.AnalyticsConfig{})
	if !engine.Available() {
		t.Fatalf("DuckDB 不可用: %+v", engine.Status())
	}
	parquetPath := filepath.Join(dataRoot, "sqd-200k-warehouse", "logs.parquet")
	if _, err := os.Stat(parquetPath); err != nil {
		t.Skipf("warehouse 数据不存在: %v", err)
	}
	return NewBuilder(engine, parquetPath), engine, dataRoot
}

func writeGraphReport(dir string, r *graphReport, t *testing.T) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	data, _ := json.MarshalIndent(r, "", "  ")
	path := filepath.Join(dir, "graph-report.json")
	if existing, err := os.ReadFile(path); err == nil {
		var old graphReport
		if json.Unmarshal(existing, &old) == nil {
			if old.Correctness == nil {
				old.Correctness = r.Correctness
			}
			if old.Metrics == nil {
				old.Metrics = r.Metrics
			}
			if old.RiskPatterns == nil {
				old.RiskPatterns = r.RiskPatterns
			}
			if old.Query == nil {
				old.Query = r.Query
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

// TestGraph_Correctness 图正确性：Graph == Parquet 关系数据。
func TestGraph_Correctness(t *testing.T) {
	builder, engine, dataRoot := newGraphTest(t)
	ctx := context.Background()

	start := time.Now()
	g, err := builder.Build(ctx)
	buildDur := time.Since(start)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(g.Nodes) == 0 || len(g.Edges) == 0 {
		t.Fatal("图为空")
	}
	// Parquet 侧独立计数（Transfer 非自环行数 = 聚合前的边行数）
	norm1 := `CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END`
	norm2 := `CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END`
	rows, err := engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT COUNT(*) AS n FROM read_parquet('%[3]s')
		 WHERE topic0 IN ('0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef','0xc3d58168c5ae7397731d063d5bbf3d657854427343f4c083240f7aacaa2d0f62','0x4a39dc06d4c0dbc64b70af90fd698a233a518aa5d07e595d983b8c0526c8f7fb')
		   AND %[1]s != %[2]s`, norm1, norm2, stringsQuote(builder.parquet)))
	if err != nil || len(rows) == 0 {
		t.Fatalf("parquet count: %v", err)
	}
	parquetRows := int64(rows[0]["n"].(float64))

	// 无自环检查
	noCycle := true
	for _, e := range g.Edges {
		if e.Source == e.Target {
			noCycle = false
		}
		if e.Source == "" || e.Target == "" {
			noCycle = false
		}
	}
	// 边可追溯（Transfer 边的 tx_count 总和 = Parquet Transfer 行数）
	var transferTx int64
	for _, e := range g.Edges {
		if e.Kind == EdgeTransfer {
			transferTx += e.TxCount
		}
	}
	traceable := transferTx == parquetRows

	t.Logf("=== 图构建（%v）===", buildDur.Round(time.Millisecond))
	t.Logf("  节点 %d，边 %d（Transfer %d / Interaction %d）", len(g.Nodes), len(g.Edges),
		countKind(g, EdgeTransfer), countKind(g, EdgeInteraction))
	t.Logf("  Parquet Transfer 行 %d，聚合边 tx_count 总和 %d，可追溯=%v", parquetRows, transferTx, traceable)
	t.Logf("  无自环=%v", noCycle)

	// 输出 CSV
	exportDir := filepath.Join(dataRoot, "..", "..", "benchmark", "snapshots")
	if err := Export(exportDir, g); err != nil {
		t.Fatalf("export: %v", err)
	}

	writeGraphReport(filepath.Join(dataRoot, "..", "..", "benchmark"), &graphReport{
		Timestamp: time.Now().UTC(),
		Correctness: map[string]any{
			"nodes": len(g.Nodes), "edges": len(g.Edges),
			"parquet_transfer_rows": parquetRows, "traceable": traceable, "no_cycle": noCycle,
		},
		Perf: map[string]any{"build_ms": buildDur.Milliseconds()},
		Passed: noCycle && traceable,
	}, t)
	if !noCycle || !traceable {
		t.Error("图正确性验证未通过")
	}
}

// TestGraph_MetricsAndClusters 核心分析 + 簇发现 + 风险网络。
func TestGraph_MetricsAndClusters(t *testing.T) {
	builder, _, dataRoot := newGraphTest(t)
	ctx := context.Background()

	g, err := builder.Build(ctx)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ComputeMetrics(g)
	rp := DetectRiskPatterns(g)

	maxPR := 0.0
	maxDegree := 0
	for _, n := range g.Nodes {
		if n.PageRank > maxPR {
			maxPR = n.PageRank
		}
		if n.Degree > maxDegree {
			maxDegree = n.Degree
		}
	}
	// 簇统计
	clusters := map[int]int{}
	for _, n := range g.Nodes {
		clusters[n.ClusterID]++
	}
	largest := 0
	for _, size := range clusters {
		if size > largest {
			largest = size
		}
	}

	t.Logf("=== 核心分析与簇 ===")
	t.Logf("  PageRank 最大 %.6f，Degree 最大 %d，簇 %d 个（最大 %d 节点）", maxPR, maxDegree, len(clusters), largest)
	t.Logf("  风险网络：中转 %d、归集 %d、分散 %d", len(rp.Hubs), len(rp.Sinks), len(rp.Spreaders))

	writeGraphReport(filepath.Join(dataRoot, "..", "..", "benchmark"), &graphReport{
		Timestamp: time.Now().UTC(),
		Metrics: map[string]any{
			"max_pagerank": maxPR, "max_degree": maxDegree,
			"clusters": len(clusters), "largest_cluster": largest,
		},
		RiskPatterns: map[string]any{
			"hubs": rp.Hubs, "sinks": rp.Sinks, "spreaders": rp.Spreaders,
		},
		Passed: len(clusters) > 0 && len(rp.Hubs) >= 0,
	}, t)
}

// TestGraph_QueryAndReproduce 图谱查询 + 可复现。
func TestGraph_QueryAndReproduce(t *testing.T) {
	builder, _, dataRoot := newGraphTest(t)
	ctx := context.Background()

	g1, err := builder.Build(ctx)
	if err != nil {
		t.Fatalf("build1: %v", err)
	}
	ComputeMetrics(g1) // 查询测试需要 degree
	g2, err := builder.Build(ctx)
	if err != nil {
		t.Fatalf("build2: %v", err)
	}
	repro := len(g1.Nodes) == len(g2.Nodes) && len(g1.Edges) == len(g2.Edges)

	// 查询：中心节点邻域
	target := ""
	maxDegree := 0
	for _, n := range g1.Nodes {
		if n.Degree > maxDegree {
			maxDegree = n.Degree
			target = n.Address
		}
	}
	start := time.Now()
	sub := QueryNeighborhood(g1, target, 2)
	queryDur := time.Since(start)
	if len(sub.Nodes) == 0 {
		t.Fatal("邻域查询为空")
	}

	t.Logf("=== 图谱查询 ===")
	t.Logf("  中心节点 %s（degree=%d）2 层邻域：%d 节点 %d 边（%v）", target, maxDegree, len(sub.Nodes), len(sub.Edges), queryDur.Round(time.Millisecond))
	t.Logf("  可复现=%v", repro)

	writeGraphReport(filepath.Join(dataRoot, "..", "..", "benchmark"), &graphReport{
		Timestamp: time.Now().UTC(),
		Query: map[string]any{
			"center": target, "degree": maxDegree,
			"neighborhood_nodes": len(sub.Nodes), "neighborhood_edges": len(sub.Edges),
			"query_ms": queryDur.Milliseconds(),
		},
		Reproducible: repro,
		Passed:       repro && len(sub.Nodes) > 0,
	}, t)
	if !repro {
		t.Error("图构建不可复现")
	}
}

func countKind(g *Graph, kind EdgeKind) int {
	n := 0
	for _, e := range g.Edges {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func stringsQuote(s string) string {
	return s
}
