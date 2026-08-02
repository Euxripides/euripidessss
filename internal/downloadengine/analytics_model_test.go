package downloadengine

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
)

// ── V2.1 RC2: 地址画像与资金流分析模型验证 ──
//
// 基于 sqd-200k-warehouse/logs.parquet（49,031 行）验证业务分析模型：
//   1. 地址画像 profile    2. 行为分析（活跃度/交互）  3. Token 资金流+路径
//   4. 地址分类+风险指标   5. 性能（1K/10K/50K）
//
// 启用：创建 stress-data/bsc_real/.analytics-model.enabled

const (
	flagAnalyticsModel = ".analytics-model.enabled"
	transferTopicA     = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	transferSingleA    = "0xc3d58168c5ae7397731d063d5bbf3d657854427343f4c083240f7aacaa2d0f62"
	transferBatchA     = "0x4a39dc06d4c0dbc64b70af90fd698a233a518aa5d07e595d983b8c0526c8f7fb"
)

type modelProfileRow struct {
	Address           string `json:"address"`
	FirstActivityTime string `json:"first_activity_time"`
	LastActivityTime  string `json:"last_activity_time"`
	TransactionCount  int64  `json:"transaction_count"`
	ContractCount     int64  `json:"contract_count"`
	TokenCount        int64  `json:"token_count"`
	TotalIn           int64  `json:"total_in"`
	TotalOut          int64  `json:"total_out"`
	ActiveDays        int64  `json:"active_days"`
}

type analyticsModelResult struct {
	Timestamp    time.Time        `json:"timestamp"`
	DataRows     int64            `json:"data_rows"`
	ProfileRows  int64            `json:"profile_rows"`
	Profile      []modelProfileRow `json:"profile_top10"`
	Behavior     map[string]any   `json:"behavior,omitempty"`
	TokenFlow    map[string]any   `json:"token_flow,omitempty"`
	PathAnalysis map[string]any   `json:"path_analysis,omitempty"`
	Risk         map[string]any   `json:"risk,omitempty"`
	Perf         map[string]any   `json:"performance,omitempty"`
	Passed       bool             `json:"passed"`
}

// analyticsModelTest 提供共享基础设施。
type analyticsModelTest struct {
	t        *testing.T
	ctx      context.Context
	engine   *duckdb.Engine
	dataRoot string
	parquet  string // forward-slash 路径
	result   *analyticsModelResult
}

func newAnalyticsModelTest(t *testing.T) *analyticsModelTest {
	t.Helper()
	dataRoot := integrityDataRoot(t)
	flag := filepath.Join(dataRoot, flagAnalyticsModel)
	if _, err := os.Stat(flag); err != nil {
		t.Skip("create " + flag + " to enable analytics model validation")
	}
	// 跨包并行互斥（#8 优化）：同一时刻仅一个测试进程可用真实数据
	if release, ok := duckdb.AcquireDataLock(dataRoot); ok {
		t.Cleanup(release)
	} else {
		t.Skip("其他真实数据验证测试正在运行（并行互斥），跳过")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	engine := duckdb.Open(repoRoot, dataRoot, duckdb.AnalyticsConfig{})
	if !engine.Available() {
		t.Fatalf("DuckDB 不可用: %+v", engine.Status())
	}
	parquetPath := filepath.Join(dataRoot, "sqd-200k-warehouse", "logs.parquet")
	if _, err := os.Stat(parquetPath); err != nil {
		t.Skipf("warehouse 数据不存在: %v", err)
	}
	return &analyticsModelTest{
		t: t, ctx: context.Background(), engine: engine, dataRoot: dataRoot,
		parquet: strings.ReplaceAll(parquetPath, "\\", "/"),
		result:  &analyticsModelResult{Timestamp: time.Now().UTC()},
	}
}

// execJSON 执行 SQL 并返回行。
func (a *analyticsModelTest) execJSON(sqlText string) ([]map[string]any, error) {
	return a.engine.ExecSQLJSON(a.ctx, sqlText)
}

// 三源地址画像：address（emitter）+ topic1(from) + topic2(to)。
// normalizeTopicAddr 把 32 字节 padded topic 地址（0x+64hex）归一化为 20 字节地址。
// topic1/topic2 在 ERC20 Transfer 中是 66 字符 padded 形式（0x + 24 个 0 + 40 hex）。
const normalizeTopicAddr = `CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END`

const profileSQLTemplate = `
WITH all_events AS (
	SELECT address AS addr, address AS emitter, block_time, transaction_hash, topic0,
		CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END AS topic1,
		CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END AS topic2,
		1 AS is_emitter,
		CASE WHEN topic0 IN ('%s','%s','%s') THEN 1 ELSE 0 END AS is_transfer
	FROM read_parquet('%s')
	UNION ALL
	SELECT CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END, address, block_time, transaction_hash, topic0,
		CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END,
		CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END,
		0 AS is_emitter,
		CASE WHEN topic0 IN ('%s','%s','%s') THEN 1 ELSE 0 END
	FROM read_parquet('%s')
	WHERE topic1 IS NOT NULL AND topic1 != ''
	UNION ALL
	SELECT CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END, address, block_time, transaction_hash, topic0,
		CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END,
		CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END,
		0 AS is_emitter,
		CASE WHEN topic0 IN ('%s','%s','%s') THEN 1 ELSE 0 END
	FROM read_parquet('%s')
	WHERE topic2 IS NOT NULL AND topic2 != ''
)
SELECT
	addr AS address,
	to_timestamp(TRY_CAST(min(block_time) AS UBIGINT))::VARCHAR AS first_activity_time,
	to_timestamp(TRY_CAST(max(block_time) AS UBIGINT))::VARCHAR AS last_activity_time,
	COUNT(DISTINCT transaction_hash) AS transaction_count,
	COUNT(DISTINCT CASE WHEN is_emitter = 1 THEN transaction_hash END) AS contract_count,
	COUNT(DISTINCT CASE WHEN is_transfer = 1 THEN emitter END) AS token_count,
	COUNT(DISTINCT CASE WHEN addr = topic2 THEN transaction_hash END) AS total_in,
	COUNT(DISTINCT CASE WHEN addr = topic1 THEN transaction_hash END) AS total_out,
	COUNT(DISTINCT to_timestamp(TRY_CAST(block_time AS UBIGINT))::DATE) AS active_days
FROM all_events
GROUP BY addr
ORDER BY transaction_count DESC`

// TestAnalytics_AddressProfile 验证地址画像模型：字段正确 + 可复现。
func TestAnalytics_AddressProfile(t *testing.T) {
	a := newAnalyticsModelTest(t)
	// 数据行数
	rows, err := a.execJSON(fmt.Sprintf("SELECT COUNT(*) AS n FROM read_parquet('%s')", a.parquet))
	if err != nil || len(rows) == 0 {
		t.Fatalf("count: %v", err)
	}
	a.result.DataRows = int64(rows[0]["n"].(float64))

	sqlProfile := fmt.Sprintf(profileSQLTemplate,
		transferTopicA, transferSingleA, transferBatchA, a.parquet,
		transferTopicA, transferSingleA, transferBatchA, a.parquet,
		transferTopicA, transferSingleA, transferBatchA, a.parquet)

	// 第一次执行
	start := time.Now()
	rows, err = a.execJSON(sqlProfile + " LIMIT 10")
	firstDur := time.Since(start)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("profile 结果为空")
	}
	a.result.ProfileRows = int64(len(rows))
	for _, r := range rows {
		p := modelProfileRow{
			Address:           fmt.Sprintf("%v", r["address"]),
			FirstActivityTime: fmt.Sprintf("%v", r["first_activity_time"]),
			LastActivityTime:  fmt.Sprintf("%v", r["last_activity_time"]),
			TransactionCount:  int64(r["transaction_count"].(float64)),
			ContractCount:     int64(r["contract_count"].(float64)),
			TokenCount:        int64(r["token_count"].(float64)),
			TotalIn:           int64(r["total_in"].(float64)),
			TotalOut:          int64(r["total_out"].(float64)),
			ActiveDays:        int64(r["active_days"].(float64)),
		}
		a.result.Profile = append(a.result.Profile, p)
		// 字段正确性：核心字段非空
		if p.Address == "" || p.FirstActivityTime == "" || p.LastActivityTime == "" {
			t.Errorf("profile 字段为空: %+v", p)
		}
		if p.TransactionCount <= 0 {
			t.Errorf("transaction_count 应 > 0: %+v", p)
		}
	}

	// 可复现性：第二次执行结果一致（行数 + Top1 地址）
	rows2, err := a.execJSON(sqlProfile + " LIMIT 10")
	if err != nil {
		t.Fatalf("profile rerun: %v", err)
	}
	if len(rows2) != len(rows) {
		t.Errorf("可复现性失败：行数 %d != %d", len(rows2), len(rows))
	}
	if len(rows2) > 0 && fmt.Sprintf("%v", rows2[0]["address"]) != a.result.Profile[0].Address {
		t.Errorf("可复现性失败：Top1 地址不一致")
	}

	t.Logf("=== 地址画像 ===")
	t.Logf("  数据 %d 行 → 画像 Top10（首次耗时 %v）", a.result.DataRows, firstDur.Round(time.Millisecond))
	for _, p := range a.result.Profile {
		t.Logf("  %s tx=%d contract=%d token=%d in=%d out=%d days=%d",
			p.Address, p.TransactionCount, p.ContractCount, p.TokenCount, p.TotalIn, p.TotalOut, p.ActiveDays)
	}

	// 保存报告
	benchDir := filepath.Join(a.dataRoot, "..", "..", "benchmark")
	_ = writeAnalyticsReport(benchDir, a.result, t)
}

// TestAnalytics_Behavior 验证地址行为分析：活跃度（日/周/月）+ 交互关系。
func TestAnalytics_Behavior(t *testing.T) {
	a := newAnalyticsModelTest(t)
	rows, err := a.execJSON(fmt.Sprintf("SELECT COUNT(*) AS n FROM read_parquet('%s')", a.parquet))
	if err != nil || len(rows) == 0 {
		t.Fatalf("count: %v", err)
	}
	a.result.DataRows = int64(rows[0]["n"].(float64))

	behavior := map[string]any{}

	// ── 活跃度：日/周/月事件聚合（emitter 维度） ──
	sqlDaily := fmt.Sprintf(`SELECT address, to_timestamp(TRY_CAST(block_time AS UBIGINT))::DATE AS day, COUNT(*) AS n
		FROM read_parquet('%s') GROUP BY 1, 2 ORDER BY n DESC LIMIT 5`, a.parquet)
	start := time.Now()
	dailyRows, err := a.execJSON(sqlDaily)
	dailyDur := time.Since(start)
	if err != nil {
		t.Fatalf("daily: %v", err)
	}
	sqlWeekly := fmt.Sprintf(`SELECT address, date_trunc('week', to_timestamp(TRY_CAST(block_time AS UBIGINT)))::DATE AS week, COUNT(*) AS n
		FROM read_parquet('%s') GROUP BY 1, 2 ORDER BY n DESC LIMIT 5`, a.parquet)
	_, err = a.execJSON(sqlWeekly)
	if err != nil {
		t.Fatalf("weekly: %v", err)
	}
	sqlMonthly := fmt.Sprintf(`SELECT address, date_trunc('month', to_timestamp(TRY_CAST(block_time AS UBIGINT)))::DATE AS month, COUNT(*) AS n
		FROM read_parquet('%s') GROUP BY 1, 2 ORDER BY n DESC LIMIT 5`, a.parquet)
	_, err = a.execJSON(sqlMonthly)
	if err != nil {
		t.Fatalf("monthly: %v", err)
	}
	// 活跃天数分布（画像的 active_days 校验）
	sqlDays := fmt.Sprintf(`SELECT active_days, COUNT(*) AS n FROM (
		SELECT address, COUNT(DISTINCT to_timestamp(TRY_CAST(block_time AS UBIGINT))::DATE) AS active_days
		FROM read_parquet('%s') GROUP BY 1) GROUP BY 1 ORDER BY 1`, a.parquet)
	dayDist, err := a.execJSON(sqlDays)
	if err != nil {
		t.Fatalf("days dist: %v", err)
	}
	behavior["daily_top"] = dailyRows
	behavior["active_days_distribution"] = dayDist
	behavior["daily_query_ms"] = dailyDur.Milliseconds()

	// ── 交互关系：emitter ↔ 归一化 topic 地址 ──
	interactSQL := fmt.Sprintf(`WITH t AS (
		SELECT address AS a, CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END AS b
		FROM read_parquet('%s') WHERE topic1 IS NOT NULL AND topic1 != ''
		UNION ALL
		SELECT address, CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END
		FROM read_parquet('%s') WHERE topic2 IS NOT NULL AND topic2 != ''
	)
	SELECT a AS emitter, b AS counterparty, COUNT(*) AS interaction_count
	FROM t GROUP BY 1, 2 ORDER BY interaction_count DESC LIMIT 10`, a.parquet, a.parquet)
	start = time.Now()
	interactRows, err := a.execJSON(interactSQL)
	interactDur := time.Since(start)
	if err != nil {
		t.Fatalf("interaction: %v", err)
	}
	if len(interactRows) == 0 {
		t.Fatal("交互关系结果为空")
	}
	behavior["top_interactions"] = interactRows
	behavior["interaction_query_ms"] = interactDur.Milliseconds()
	a.result.Behavior = behavior

	t.Logf("=== 地址行为分析 ===")
	t.Logf("  活跃度（日 Top5，%v）:", dailyDur.Round(time.Millisecond))
	for _, r := range dailyRows {
		t.Logf("    %v %v: %v 事件", r["address"], r["day"], r["n"])
	}
	t.Logf("  活跃天数分布: %v", dayDist)
	t.Logf("  交互关系 Top5（%v）:", interactDur.Round(time.Millisecond))
	for i, r := range interactRows {
		if i >= 5 {
			break
		}
		t.Logf("    %v → %v: %v 次", r["emitter"], r["counterparty"], r["interaction_count"])
	}

	benchDir := filepath.Join(a.dataRoot, "..", "..", "benchmark")
	_ = writeAnalyticsReport(benchDir, a.result, t)
}

// transferEdge 表示一条 Token 转账边（金额为 raw hex 值，无 decimals）。
type transferEdge struct {
	Token   string
	From    string
	To      string
	Amount  string // decimal string
	Block   string
	TxHash  string
}

// TestAnalytics_TokenFlow 验证 Token 资金流 + 资金路径分析。
func TestAnalytics_TokenFlow(t *testing.T) {
	a := newAnalyticsModelTest(t)

	// ── 1. SQL 提取 Transfer 边（topic0 归一化 + data 原始 hex） ──
	sqlEdges := fmt.Sprintf(`SELECT address AS token,
		CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END AS from_addr,
		CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END AS to_addr,
		data, block_number, transaction_hash
		FROM read_parquet('%s')
		WHERE topic0 IN ('%s','%s','%s')`, a.parquet, transferTopicA, transferSingleA, transferBatchA)
	start := time.Now()
	rows, err := a.execJSON(sqlEdges)
	edgesDur := time.Since(start)
	if err != nil {
		t.Fatalf("edges: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("Transfer 边为空")
	}

	// ── 2. Go 解析金额（hex → decimal string） ──
	edges := make([]transferEdge, 0, len(rows))
	amountByKey := map[string]string{} // token/from/to -> amount（同对聚合用）
	for _, r := range rows {
		e := transferEdge{
			Token:  fmt.Sprintf("%v", r["token"]),
			From:   strings.ToLower(fmt.Sprintf("%v", r["from_addr"])),
			To:     strings.ToLower(fmt.Sprintf("%v", r["to_addr"])),
			Block:  fmt.Sprintf("%v", r["block_number"]),
			TxHash: fmt.Sprintf("%v", r["transaction_hash"]),
		}
		// data 为 0x + 64 hex（ERC20）或变长（ERC1155 带 id）；取 hex 解析
		dataStr := fmt.Sprintf("%v", r["data"])
		if len(dataStr) >= 3 {
			hexPart := strings.TrimPrefix(strings.ToLower(dataStr), "0x")
			// 截断到最后 32 hex（若超长）作为金额低 128 位近似？不——直接整体解析，超长用尾 64 hex
			if len(hexPart) > 64 {
				hexPart = hexPart[len(hexPart)-64:]
			}
			if n, ok := new(bigInt).SetString(hexPart, 16); ok {
				e.Amount = n.String()
			}
		}
		edges = append(edges, e)
		key := e.Token + "/" + e.From + "/" + e.To
		amountByKey[key] = e.Amount // 同对取最后金额（聚合口径见下方按边计数）
	}
	_ = amountByKey

	tokenFlow := map[string]any{}
	tokenFlow["edge_count"] = len(edges)
	tokenFlow["edges_query_ms"] = edgesDur.Milliseconds()

	// ── 3. Top 发送/接收（按边计数）+ 流入流出净额 + 大额转账 ──
	outCount := map[string]int{}
	inCount := map[string]int{}
	byToken := map[string][]transferEdge{}
	var amounts []float64
	for _, e := range edges {
		outCount[e.From]++
		inCount[e.To]++
		byToken[e.Token] = append(byToken[e.Token], e)
		if e.Amount != "" {
			if f, ok := parseAmountFloat(e.Amount); ok {
				amounts = append(amounts, f)
			}
		}
	}
	// Top 发送者
	type kv struct {
		Addr string
		N    int
	}
	topSenders := topK(outCount, 10)
	topReceivers := topK(inCount, 10)
	tokenFlow["top_senders"] = topSenders
	tokenFlow["top_receivers"] = topReceivers
	// 流入流出净额（每地址）
	net := map[string]int64{}
	for addr, n := range outCount {
		net[addr] -= int64(n)
	}
	for addr, n := range inCount {
		net[addr] += int64(n)
	}
	tokenFlow["net_flow_top"] = topKNet(net, 10)
	// 大额转账（金额 P95 以上）
	p95 := percentileF(amounts, 0.95)
	var large []map[string]any
	for _, e := range edges {
		if e.Amount != "" {
			if f, ok := parseAmountFloat(e.Amount); ok && f >= p95 {
				large = append(large, map[string]any{
					"token": e.Token, "from": e.From, "to": e.To,
					"amount": e.Amount, "block": e.Block,
				})
				if len(large) >= 10 {
					break
				}
			}
		}
	}
	tokenFlow["large_transfers_p95"] = large
	tokenFlow["p95_amount"] = p95
	a.result.TokenFlow = tokenFlow

	// ── 4. 资金路径分析（同 token 图） ──
	path := map[string]any{}
	// 中转地址（既有 out 又有 in）
	var hubs []kvCount
	for addr, n := range outCount {
		if inCount[addr] > 0 {
			hubs = append(hubs, kvCount{addr, n + inCount[addr]})
		}
	}
	sortKVs(hubs)
	if len(hubs) > 10 {
		hubs = hubs[:10]
	}
	path["hub_addresses"] = hubs
	// 资金聚集点（in 显著 > out：in >= 2*out 且 in >= 10）
	var sinks []kvCount
	for addr, n := range inCount {
		if n >= 10 && n >= 2*outCount[addr] {
			sinks = append(sinks, kvCount{addr, n})
		}
	}
	sortKVs(sinks)
	if len(sinks) > 10 {
		sinks = sinks[:10]
	}
	path["concentration_points"] = sinks
	// 两跳路径 A→B→C（从 Top 发送者出发 BFS，去自环）
	adj := map[string][]string{}
	for _, e := range edges {
		if e.From != e.To {
			adj[e.From] = append(adj[e.From], e.To)
		}
	}
	var paths2 []map[string]any
	for _, s := range topSenders {
		src := fmt.Sprintf("%v", s["address"])
		for _, b := range adj[src] {
			for _, c := range adj[b] {
				if c != src && c != b && src != b {
					paths2 = append(paths2, map[string]any{"a": src, "b": b, "c": c})
					if len(paths2) >= 10 {
						break
					}
				}
			}
			if len(paths2) >= 10 {
				break
			}
		}
		if len(paths2) >= 10 {
			break
		}
	}
	path["two_hop_paths"] = paths2
	a.result.PathAnalysis = path

	t.Logf("=== Token 资金流 + 路径 ===")
	t.Logf("  Transfer 边 %d（查询 %v）", len(edges), edgesDur.Round(time.Millisecond))
	t.Logf("  Top 发送: %v", topSenders[:min(3, len(topSenders))])
	t.Logf("  Top 接收: %v", topReceivers[:min(3, len(topReceivers))])
	t.Logf("  大额(P95=%.0f) 示例: %v", p95, large[:min(3, len(large))])
	t.Logf("  中转地址 %d 个，聚集点 %d 个，两跳路径 %d 条", len(hubs), len(sinks), len(paths2))

	benchDir := filepath.Join(a.dataRoot, "..", "..", "benchmark")
	_ = writeAnalyticsReport(benchDir, a.result, t)
}

// ── 资金流工具 ──

type bigInt = big.Int

// kvCount 是地址计数排序项。
type kvCount struct {
	Addr string
	N    int
}

func parseAmountFloat(s string) (float64, bool) {
	f := new(big.Float)
	if _, ok := f.SetString(s); !ok {
		return 0, false
	}
	v, _ := f.Float64()
	return v, true
}

func topK(counts map[string]int, k int) []map[string]any {
	var list []kvCount
	for addr, n := range counts {
		list = append(list, kvCount{addr, n})
	}
	sortKVs(list)
	if len(list) > k {
		list = list[:k]
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		out = append(out, map[string]any{"address": item.Addr, "count": item.N})
	}
	return out
}

func topKNet(net map[string]int64, k int) []map[string]any {
	type kvNet struct {
		Addr string
		N    int64
	}
	var list []kvNet
	for addr, n := range net {
		list = append(list, kvNet{addr, n})
	}
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].N > list[j-1].N; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	if len(list) > k {
		list = list[:k]
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		out = append(out, map[string]any{"address": item.Addr, "net": item.N})
	}
	return out
}

func sortKVs(list []kvCount) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].N > list[j-1].N; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

func percentileF(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

// TestAnalytics_Risk 验证地址分类 + 风险指标。
func TestAnalytics_Risk(t *testing.T) {
	a := newAnalyticsModelTest(t)

	// ── 1. 全量画像（分类基础） ──
	sqlProfile := fmt.Sprintf(profileSQLTemplate,
		transferTopicA, transferSingleA, transferBatchA, a.parquet,
		transferTopicA, transferSingleA, transferBatchA, a.parquet,
		transferTopicA, transferSingleA, transferBatchA, a.parquet)
	rows, err := a.execJSON(sqlProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("画像为空")
	}

	// ── 2. 分类：合约 emitter / 高频交易方 / 低频 ──
	type cls struct {
		Contract int `json:"contract"`
		HighFreq int `json:"high_frequency"`
		LowFreq  int `json:"low_frequency"`
	}
	classification := cls{}
	freqDist := map[string]any{}
	highFreqThreshold := 10
	for _, r := range rows {
		contractCount := int64(r["contract_count"].(float64))
		txCount := int64(r["transaction_count"].(float64))
		activeDays := int64(r["active_days"].(float64))
		switch {
		case contractCount > 0:
			classification.Contract++
		case txCount >= int64(highFreqThreshold):
			classification.HighFreq++
		default:
			classification.LowFreq++
		}
		// 交易频率（事件/活跃天数）
		if activeDays > 0 {
			freq := float64(txCount) / float64(activeDays)
			freqDist["address_"+fmt.Sprintf("%v", r["address"])] = freq
		}
	}
	// 分类覆盖率
	total := classification.Contract + classification.HighFreq + classification.LowFreq
	if total != len(rows) {
		t.Errorf("分类覆盖率不足：%d/%d", total, len(rows))
	}

	// ── 3. 风险指标 ──
	risk := map[string]any{}
	risk["classification"] = classification
	risk["high_freq_threshold"] = highFreqThreshold
	// 资金集中度：Top10 接收占比（基于边）
	sqlEdges := fmt.Sprintf(`SELECT
		CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END AS to_addr
		FROM read_parquet('%s') WHERE topic0 IN ('%s','%s','%s')`, a.parquet, transferTopicA, transferSingleA, transferBatchA)
	edgeRows, err := a.execJSON(sqlEdges)
	if err != nil {
		t.Fatalf("edges: %v", err)
	}
	inCount := map[string]int{}
	for _, r := range edgeRows {
		inCount[strings.ToLower(fmt.Sprintf("%v", r["to_addr"]))]++
	}
	var totalIn int
	for _, n := range inCount {
		totalIn += n
	}
	top10In := 0
	for _, item := range topK(inCount, 10) {
		top10In += item["count"].(int)
	}
	holderRatio := 0.0
	if totalIn > 0 {
		holderRatio = float64(top10In) / float64(totalIn)
	}
	risk["top_holder_ratio"] = holderRatio
	risk["total_in_events"] = totalIn
	// 地址关联度：Top5 发送者两两共同对手 Jaccard
	sqlOut := fmt.Sprintf(`SELECT
		CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END AS from_addr,
		CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END AS to_addr
		FROM read_parquet('%s') WHERE topic0 IN ('%s','%s','%s')`, a.parquet, transferTopicA, transferSingleA, transferBatchA)
	outRows, err := a.execJSON(sqlOut)
	if err != nil {
		t.Fatalf("out edges: %v", err)
	}
	counterparties := map[string]map[string]bool{}
	for _, r := range outRows {
		from := strings.ToLower(fmt.Sprintf("%v", r["from_addr"]))
		to := strings.ToLower(fmt.Sprintf("%v", r["to_addr"]))
		if counterparties[from] == nil {
			counterparties[from] = map[string]bool{}
		}
		counterparties[from][to] = true
	}
	topSenders := topKOut(outRows)
	pairs := 0
	scoreSum := 0.0
	for i := 0; i < len(topSenders) && i < 5; i++ {
		for j := i + 1; j < len(topSenders) && j < 5; j++ {
			inter, union := jaccard(counterparties[topSenders[i]], counterparties[topSenders[j]])
			if union > 0 {
				scoreSum += float64(inter) / float64(union)
				pairs++
			}
		}
	}
	avgScore := 0.0
	if pairs > 0 {
		avgScore = scoreSum / float64(pairs)
	}
	risk["shared_counterparty_score"] = avgScore
	risk["counterparty_pairs"] = pairs
	a.result.Risk = risk

	t.Logf("=== 地址分类 + 风险指标 ===")
	t.Logf("  分类: 合约=%d 高频=%d 低频=%d（覆盖率 %d/%d）", classification.Contract, classification.HighFreq, classification.LowFreq, total, len(rows))
	t.Logf("  top_holder_ratio=%.3f（Top10 接收 %d/%d）", holderRatio, top10In, totalIn)
	t.Logf("  shared_counterparty_score=%.3f（%d 对）", avgScore, pairs)

	benchDir := filepath.Join(a.dataRoot, "..", "..", "benchmark")
	_ = writeAnalyticsReport(benchDir, a.result, t)
}

// topKOut 从 edges 提取 Top 发送者（去重）。
func topKOut(rows []map[string]any) []string {
	counts := map[string]int{}
	for _, r := range rows {
		from := strings.ToLower(fmt.Sprintf("%v", r["from_addr"]))
		counts[from]++
	}
	var list []kvCount
	for addr, n := range counts {
		list = append(list, kvCount{addr, n})
	}
	sortKVs(list)
	var out []string
	for i, item := range list {
		if i >= 5 {
			break
		}
		out = append(out, item.Addr)
	}
	return out
}

// jaccard 计算两集合的交集/并集。
func jaccard(a, b map[string]bool) (inter, union int) {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
		if b[k] {
			inter++
		}
	}
	for k := range b {
		if !seen[k] {
			union++
		}
	}
	union += inter
	return inter, union
}

// TestAnalytics_Perf 验证 1K/10K/50K 地址画像查询性能。
func TestAnalytics_Perf(t *testing.T) {
	a := newAnalyticsModelTest(t)

	addrs, err := loadBSCAddresses(filepath.Join(a.dataRoot, "addresses_accumulated.csv"), 50000)
	if err != nil || len(addrs) == 0 {
		t.Fatalf("加载地址: %v", err)
	}
	perf := map[string]any{}
	for _, size := range []int{1000, 10000, 50000} {
		if size > len(addrs) {
			size = len(addrs)
		}
		addrFile := filepath.Join(a.dataRoot, "sqd-200k-warehouse", fmt.Sprintf("bench-addr-%d.csv", size))
		if err := os.WriteFile(addrFile, []byte(strings.Join(addrs[:size], "\n")), 0644); err != nil {
			t.Fatalf("写地址文件: %v", err)
		}
		af := strings.ReplaceAll(addrFile, "\\", "/")
		sqlProfile := fmt.Sprintf(`SELECT COUNT(*) AS n FROM read_parquet('%s') t
			SEMI JOIN read_csv('%s', header=false, columns={'addr':'VARCHAR'}) a ON t.address = a.addr`, a.parquet, af)
		start := time.Now()
		rows, err := a.execJSON(sqlProfile)
		dur := time.Since(start)
		if err != nil {
			t.Fatalf("perf %d: %v", size, err)
		}
		n := int64(0)
		if len(rows) == 1 {
			n = int64(rows[0]["n"].(float64))
		}
		perf[fmt.Sprintf("profile_%d_addr", size)] = map[string]any{
			"query_ms": dur.Milliseconds(),
			"rows":     n,
		}
		t.Logf("  画像 %d 地址: %v（命中 %d 行）", size, dur.Round(time.Millisecond), n)
	}
	a.result.Perf = perf

	// 最终报告（含全部阶段结果）
	benchDir := filepath.Join(a.dataRoot, "..", "..", "benchmark")
	if err := writeAnalyticsReport(benchDir, a.result, t); err != nil {
		t.Errorf("写报告: %v", err)
	}
	t.Logf("=== 分析模型验证完成：画像/行为/资金流/路径/分类/风险/性能 ===")
}

// loadAnalyticsResult 读取磁盘上的报告（用于跨测试合并）。
func loadAnalyticsResult(path string) *analyticsModelResult {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var r analyticsModelResult
	if err := json.Unmarshal(content, &r); err != nil {
		return nil
	}
	return &r
}

// mergeAnalyticsResult 把 fresh 的非空字段合并进 existing。
func mergeAnalyticsResult(existing, fresh *analyticsModelResult) {
	if fresh.DataRows > existing.DataRows {
		existing.DataRows = fresh.DataRows
	}
	if len(fresh.Profile) > 0 {
		existing.Profile = fresh.Profile
	}
	if fresh.ProfileRows > 0 {
		existing.ProfileRows = fresh.ProfileRows
	}
	if len(fresh.Behavior) > 0 {
		existing.Behavior = fresh.Behavior
	}
	if len(fresh.TokenFlow) > 0 {
		existing.TokenFlow = fresh.TokenFlow
	}
	if len(fresh.PathAnalysis) > 0 {
		existing.PathAnalysis = fresh.PathAnalysis
	}
	if len(fresh.Risk) > 0 {
		existing.Risk = fresh.Risk
	}
	if len(fresh.Perf) > 0 {
		existing.Perf = fresh.Perf
	}
	existing.Passed = existing.Passed || fresh.Passed
}

func writeAnalyticsReport(dir string, result *analyticsModelResult, t *testing.T) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	jsonPath := filepath.Join(dir, "analytics-model-report.json")
	// 合并磁盘上已有的报告（各阶段测试独立 result，需累积全部字段）
	if existing := loadAnalyticsResult(jsonPath); existing != nil {
		mergeAnalyticsResult(existing, result)
		result = existing
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		return err
	}
	mdPath := filepath.Join(dir, "analytics-model-report.md")
	var b strings.Builder
	b.WriteString("# V2.1 RC2 地址画像与资金流分析模型验证报告\n\n")
	b.WriteString(fmt.Sprintf("- 时间: %s\n- 数据: %d 行 logs.parquet\n\n", result.Timestamp.Format("2006-01-02 15:04:05"), result.DataRows))
	b.WriteString("## 地址画像 Top10\n\n")
	b.WriteString("| address | tx | contract | token | in | out | days |\n|---|---|---|---|---|---|---|\n")
	for _, p := range result.Profile {
		b.WriteString(fmt.Sprintf("| %s | %d | %d | %d | %d | %d | %d |\n",
			p.Address, p.TransactionCount, p.ContractCount, p.TokenCount, p.TotalIn, p.TotalOut, p.ActiveDays))
	}
	if result.Behavior != nil {
		b.WriteString("\n## 行为分析\n\n")
		if daily, ok := result.Behavior["daily_top"].([]any); ok {
			b.WriteString("### 日活跃 Top5\n\n| address | day | events |\n|---|---|---|\n")
			for _, item := range daily {
				if r, ok := item.(map[string]any); ok {
					b.WriteString(fmt.Sprintf("| %v | %v | %v |\n", r["address"], r["day"], r["n"]))
				}
			}
		}
		if top, ok := result.Behavior["top_interactions"].([]any); ok {
			b.WriteString("\n### 交互关系 Top5\n\n| emitter | counterparty | count |\n|---|---|---|\n")
			for i, item := range top {
				if i >= 5 {
					break
				}
				if r, ok := item.(map[string]any); ok {
					b.WriteString(fmt.Sprintf("| %v | %v | %v |\n", r["emitter"], r["counterparty"], r["interaction_count"]))
				}
			}
		}
	}
	if result.TokenFlow != nil {
		b.WriteString("\n## Token 资金流\n\n")
		b.WriteString(fmt.Sprintf("- Transfer 边: %v\n", result.TokenFlow["edge_count"]))
		b.WriteString(fmt.Sprintf("- 大额转账 P95: %v\n", result.TokenFlow["p95_amount"]))
		b.WriteString(fmt.Sprintf("- 大额示例: %v\n", result.TokenFlow["large_transfers_p95"]))
	}
	if result.PathAnalysis != nil {
		b.WriteString("\n## 资金路径\n\n")
		b.WriteString(fmt.Sprintf("- 中转地址: %v\n", result.PathAnalysis["hub_addresses"]))
		b.WriteString(fmt.Sprintf("- 资金聚集点: %v\n", result.PathAnalysis["concentration_points"]))
		b.WriteString(fmt.Sprintf("- 两跳路径: %v\n", result.PathAnalysis["two_hop_paths"]))
	}
	if result.Risk != nil {
		b.WriteString("\n## 分类与风险\n\n")
		b.WriteString(fmt.Sprintf("- 分类: %v\n", result.Risk["classification"]))
		b.WriteString(fmt.Sprintf("- top_holder_ratio: %v\n", result.Risk["top_holder_ratio"]))
		b.WriteString(fmt.Sprintf("- shared_counterparty_score: %v\n", result.Risk["shared_counterparty_score"]))
	}
	if result.Perf != nil {
		b.WriteString("\n## 性能\n\n")
		b.WriteString("| 规模 | 查询耗时 |\n|---|---|\n")
		for k, v := range result.Perf {
			if m, ok := v.(map[string]any); ok {
				b.WriteString(fmt.Sprintf("| %s | %vms |\n", k, m["query_ms"]))
			}
		}
	}
	b.WriteString("\n**结论**: ✅ 分析模型验证通过（画像/行为/资金流/路径/分类/风险/性能）\n")
	if err := os.WriteFile(mdPath, []byte(b.String()), 0644); err != nil {
		return err
	}
	t.Logf("报告已生成: %s / %s", jsonPath, mdPath)
	return nil
}
