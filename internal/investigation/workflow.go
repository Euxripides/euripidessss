// Package investigation 实现链上调查工作流：单地址调查、多跳资金追踪、
// 地址关联发现、风险场景分析，并生成调查证据报告。
package investigation

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/analyticsapi"
)

// Summary 是单地址调查摘要。
type Summary struct {
	Address       string                `json:"address"`
	AddressType   string                `json:"address_type"` // 合约 / 活跃交易方 / 低频
	Profile       *analyticsapi.Profile `json:"profile"`
	Risk          *analyticsapi.Risk    `json:"risk"`
	InCount       int                   `json:"in_count"`
	OutCount      int                   `json:"out_count"`
	TopToken      string                `json:"top_token"`
	PathCount     int                   `json:"path_count"`
	RelatedCount  int                   `json:"related_count"`
	Related       []RelatedAddress      `json:"related_top5"`
	QueryDuration map[string]string     `json:"query_duration_ms"`
}

// TraceEdge 是一条可追踪的转账边。
type TraceEdge struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Token   string `json:"token"`
	Amount  string `json:"amount"`
	TxHash  string `json:"tx_hash"`
	Block   string `json:"block"`
	BlockTs string `json:"block_time"`
	LogIdx  string `json:"log_index"`
}

// TracePath 是一条多跳资金路径。
type TracePath struct {
	Nodes []string    `json:"nodes"`
	Edges []TraceEdge `json:"edges"`
	Hops  int         `json:"hops"`
}

// RelatedAddress 是关联地址（共同对手 Jaccard）。
type RelatedAddress struct {
	Address              string  `json:"address"`
	Score                float64 `json:"shared_counterparty_score"`
	SharedCounterparties int     `json:"shared_counterparties"`
}

// RiskEvidence 是高风险调查证据。
type RiskEvidence struct {
	Address       string             `json:"address"`
	Risk          *analyticsapi.Risk `json:"risk"`
	LargeInflows  []TraceEdge        `json:"large_inflows_top5"`
	RapidOutflows []TraceEdge        `json:"rapid_outflows_top5"`
	SpreadTargets []SpreadTarget     `json:"spread_targets_top5"`
	Pattern       string             `json:"pattern"`
}

// SpreadTarget 是资金分散目标。
type SpreadTarget struct {
	Address string `json:"address"`
	Count   int    `json:"outgoing_count"`
	Total   string `json:"total_amount"`
}

// Investigator 执行调查工作流。
type Investigator struct {
	svc     *analyticsapi.Service
	engine  *duckdb.Engine
	parquet string
}

// New 创建调查器。
func New(svc *analyticsapi.Service, engine *duckdb.Engine, parquetPath string) *Investigator {
	return &Investigator{
		svc:     svc,
		engine:  engine,
		parquet: strings.ReplaceAll(parquetPath, "\\", "/"),
	}
}

// Investigate 执行单地址调查全流程：画像→风险→资金流→路径→关联。
func (i *Investigator) Investigate(ctx context.Context, address string) (*Summary, error) {
	addr := strings.ToLower(address)
	s := &Summary{Address: addr, QueryDuration: map[string]string{}}
	start := time.Now()
	profile, err := i.svc.Profile(ctx, addr)
	if err != nil {
		return nil, err
	}
	s.Profile = profile
	s.QueryDuration["profile_ms"] = fmt.Sprintf("%d", time.Since(start).Milliseconds())

	start = time.Now()
	risk, err := i.svc.Risk(ctx, addr)
	if err != nil {
		return nil, err
	}
	s.Risk = risk
	s.QueryDuration["risk_ms"] = fmt.Sprintf("%d", time.Since(start).Milliseconds())

	// 地址类型
	switch {
	case profile.ContractCount > 0:
		s.AddressType = "合约"
	case profile.TransactionCount >= 10:
		s.AddressType = "活跃交易方"
	default:
		s.AddressType = "低频"
	}

	start = time.Now()
	flows, err := i.svc.Flows(ctx, addr, "")
	if err != nil {
		return nil, err
	}
	s.QueryDuration["flows_ms"] = fmt.Sprintf("%d", time.Since(start).Milliseconds())
	tokenCount := map[string]int{}
	for _, f := range flows {
		if f.Direction == "incoming" {
			s.InCount++
		} else {
			s.OutCount++
		}
		tokenCount[f.Token]++
	}
	// Top token
	maxN := 0
	for token, n := range tokenCount {
		if n > maxN {
			maxN = n
			s.TopToken = token
		}
	}

	start = time.Now()
	paths, err := i.svc.Path(ctx, addr)
	if err != nil {
		return nil, err
	}
	s.PathCount = len(paths)
	s.QueryDuration["path_ms"] = fmt.Sprintf("%d", time.Since(start).Milliseconds())

	// 关联地址（Top5）
	start = time.Now()
	related, err := i.DiscoverRelations(ctx, []string{addr}, 5)
	if err != nil {
		return nil, err
	}
	s.Related = related
	s.RelatedCount = len(related)
	s.QueryDuration["relations_ms"] = fmt.Sprintf("%d", time.Since(start).Milliseconds())

	return s, nil
}

// TraceFunds 多跳资金追踪（BFS，无环，最多 maxHops 跳）。
func (i *Investigator) TraceFunds(ctx context.Context, address string, maxHops int) ([]TracePath, error) {
	addr := strings.ToLower(address)
	if maxHops < 1 {
		maxHops = 2
	}
	if maxHops > 4 {
		maxHops = 4
	}
	edges, err := i.allTransferEdges(ctx)
	if err != nil {
		return nil, err
	}
	// 邻接表 + 边信息
	adj := map[string][]TraceEdge{}
	for _, e := range edges {
		if e.From == e.To {
			continue
		}
		adj[e.From] = append(adj[e.From], e)
	}

	var paths []TracePath
	type node struct {
		addr  string
		edges []TraceEdge
		depth int
	}
	queue := []node{{addr: addr, depth: 0}}
	seen := map[string]bool{addr: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxHops {
			continue
		}
		for _, e := range adj[cur.addr] {
			if seen[e.To] {
				continue // 无环
			}
			newEdges := append(append([]TraceEdge(nil), cur.edges...), e)
			seen[e.To] = true
			paths = append(paths, TracePath{
				Nodes: pathNodes(newEdges),
				Edges: newEdges,
				Hops:  len(newEdges),
			})
			queue = append(queue, node{addr: e.To, edges: newEdges, depth: cur.depth + 1})
			if len(paths) >= 50 {
				break
			}
		}
		if len(paths) >= 50 {
			break
		}
	}
	return paths, nil
}

func pathNodes(edges []TraceEdge) []string {
	nodes := []string{edges[0].From}
	for _, e := range edges {
		nodes = append(nodes, e.To)
	}
	return nodes
}

// allTransferEdges 加载全部 Transfer 边（含时间）。
func (i *Investigator) allTransferEdges(ctx context.Context) ([]TraceEdge, error) {
	norm1 := `CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END`
	norm2 := `CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END`
	sqlText := fmt.Sprintf(`SELECT %[1]s AS f, %[2]s AS t, address AS token, data, transaction_hash,
		block_number, block_time, log_index
		FROM read_parquet('%[3]s')
		WHERE topic0 IN ('%[4]s','%[5]s','%[6]s')`, norm1, norm2, i.parquet,
		analyticsapi.TransferTopic, analyticsapi.TransferSingle, analyticsapi.TransferBatch)
	rows, err := i.engine.ExecSQLJSON(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	edges := make([]TraceEdge, 0, len(rows))
	for _, r := range rows {
		e := TraceEdge{
			From:    strings.ToLower(fmt.Sprintf("%v", r["f"])),
			To:      strings.ToLower(fmt.Sprintf("%v", r["t"])),
			Token:   fmt.Sprintf("%v", r["token"]),
			TxHash:  fmt.Sprintf("%v", r["transaction_hash"]),
			Block:   fmt.Sprintf("%v", r["block_number"]),
			BlockTs: fmt.Sprintf("%v", r["block_time"]),
			LogIdx:  fmt.Sprintf("%v", r["log_index"]),
		}
		if d := fmt.Sprintf("%v", r["data"]); len(d) >= 3 {
			hexPart := strings.TrimPrefix(strings.ToLower(d), "0x")
			if len(hexPart) > 64 {
				hexPart = hexPart[len(hexPart)-64:]
			}
			if n, ok := new(big.Int).SetString(hexPart, 16); ok {
				e.Amount = n.String()
			}
		}
		edges = append(edges, e)
	}
	return edges, nil
}

// DiscoverRelations 发现地址间的关联（共同对手 Jaccard）。
func (i *Investigator) DiscoverRelations(ctx context.Context, addresses []string, limit int) ([]RelatedAddress, error) {
	edges, err := i.allTransferEdges(ctx)
	if err != nil {
		return nil, err
	}
	// 每个地址的对手集合
	counter := map[string]map[string]bool{}
	for _, e := range edges {
		if counter[e.From] == nil {
			counter[e.From] = map[string]bool{}
		}
		counter[e.From][e.To] = true
		if counter[e.To] == nil {
			counter[e.To] = map[string]bool{}
		}
		counter[e.To][e.From] = true
	}
	targets := map[string]bool{}
	for _, a := range addresses {
		targets[strings.ToLower(a)] = true
	}
	var related []RelatedAddress
	for addr := range counter {
		if targets[addr] {
			continue
		}
		// 与所有目标的共同对手并集
		shared := map[string]bool{}
		for _, a := range addresses {
			ta := strings.ToLower(a)
			for c := range counter[ta] {
				if counter[addr][c] {
					shared[c] = true
				}
			}
		}
		if len(shared) == 0 {
			continue
		}
		union := len(counter[addr]) + len(shared)
		score := float64(len(shared)) / float64(union)
		related = append(related, RelatedAddress{
			Address:              addr,
			Score:                score,
			SharedCounterparties: len(shared),
		})
	}
	sort.Slice(related, func(i2, j int) bool { return related[i2].Score > related[j].Score })
	if limit > 0 && len(related) > limit {
		related = related[:limit]
	}
	return related, nil
}

// RiskScenario 生成高风险调查证据：大额转入→快速转出→多地址分散。
func (i *Investigator) RiskScenario(ctx context.Context, address string) (*RiskEvidence, error) {
	addr := strings.ToLower(address)
	risk, err := i.svc.Risk(ctx, addr)
	if err != nil {
		return nil, err
	}
	edges, err := i.allTransferEdges(ctx)
	if err != nil {
		return nil, err
	}
	var inflows, outflows []TraceEdge
	for _, e := range edges {
		if e.To == addr {
			inflows = append(inflows, e)
		}
		if e.From == addr {
			outflows = append(outflows, e)
		}
	}
	// 大额转入（金额 P90 以上）
	amounts := make([]float64, 0, len(inflows))
	for _, e := range inflows {
		if f, ok := parseFloat(e.Amount); ok {
			amounts = append(amounts, f)
		}
	}
	threshold := percentile(amounts, 0.9)
	var large []TraceEdge
	for _, e := range inflows {
		if f, ok := parseFloat(e.Amount); ok && f >= threshold {
			large = append(large, e)
		}
	}
	sortEdgesByAmountDesc(large)
	if len(large) > 5 {
		large = large[:5]
	}
	// 快速转出（按块序取前 5）
	sortEdgesByBlock(outflows)
	var rapid []TraceEdge
	if len(outflows) > 5 {
		rapid = outflows[:5]
	} else {
		rapid = outflows
	}
	// 分散目标（outgoing 目标计数 + 总额）
	targets := map[string]*SpreadTarget{}
	for _, e := range outflows {
		if targets[e.To] == nil {
			targets[e.To] = &SpreadTarget{Address: e.To}
		}
		targets[e.To].Count++
		if f, ok := parseFloat(e.Amount); ok {
			if total, ok2 := parseFloat(targets[e.To].Total); ok2 {
				targets[e.To].Total = formatFloat(total + f)
			} else {
				targets[e.To].Total = e.Amount
			}
		}
	}
	var spread []SpreadTarget
	for _, t := range targets {
		spread = append(spread, *t)
	}
	sort.Slice(spread, func(i2, j int) bool { return spread[i2].Count > spread[j].Count })
	if len(spread) > 5 {
		spread = spread[:5]
	}

	pattern := "常规"
	if len(large) > 0 && len(rapid) > 0 {
		pattern = "大额转入-快速转出"
	}
	if len(spread) >= 3 {
		pattern += "-多地址分散"
	}
	return &RiskEvidence{
		Address:       addr,
		Risk:          risk,
		LargeInflows:  large,
		RapidOutflows: rapid,
		SpreadTargets: spread,
		Pattern:       pattern,
	}, nil
}

// ── 工具 ──

func parseFloat(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	f := new(big.Float)
	if _, ok := f.SetString(s); !ok {
		return 0, false
	}
	v, _ := f.Float64()
	return v, true
}

func formatFloat(v float64) string {
	return new(big.Float).SetFloat64(v).Text('f', 0)
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func sortEdgesByAmountDesc(edges []TraceEdge) {
	sort.Slice(edges, func(i, j int) bool {
		fi, _ := parseFloat(edges[i].Amount)
		fj, _ := parseFloat(edges[j].Amount)
		return fi > fj
	})
}

func sortEdgesByBlock(edges []TraceEdge) {
	sort.Slice(edges, func(i, j int) bool {
		return edges[i].Block < edges[j].Block
	})
}

// ── 证据生成 ──

// Evidence 是调查证据包（写入 snapshots/）。
type Evidence struct {
	Timestamp  time.Time        `json:"timestamp"`
	Target     string           `json:"target"`
	Summary    *Summary         `json:"summary"`
	TracePaths []TracePath      `json:"trace_paths"`
	Risk       *RiskEvidence    `json:"risk_evidence"`
	Related    []RelatedAddress `json:"related_addresses"`
}

// GenerateReport 生成调查证据（investigation-report.json/md + snapshots/*）。
func GenerateReport(dir string, evidence *Evidence, relatedAll []RelatedAddress, t interface{ Logf(string, ...any) }) error {
	if err := os.MkdirAll(filepath.Join(dir, "snapshots"), 0755); err != nil {
		return err
	}
	// evidence.json
	evPath := filepath.Join(dir, "snapshots", "evidence.json")
	evData, _ := json.MarshalIndent(evidence, "", "  ")
	if err := os.WriteFile(evPath, evData, 0644); err != nil {
		return err
	}
	// paths.csv
	pathsCSV, err := os.Create(filepath.Join(dir, "snapshots", "paths.csv"))
	if err != nil {
		return err
	}
	cw := csv.NewWriter(pathsCSV)
	_ = cw.Write([]string{"path", "hop", "token", "amount", "tx_hash", "block"})
	for _, p := range evidence.TracePaths {
		for _, e := range p.Edges {
			_ = cw.Write([]string{strings.Join(p.Nodes, "→"), fmt.Sprintf("%d", p.Hops), e.Token, e.Amount, e.TxHash, e.Block})
		}
	}
	cw.Flush()
	pathsCSV.Close()
	// related_addresses.csv
	relCSV, err := os.Create(filepath.Join(dir, "snapshots", "related_addresses.csv"))
	if err != nil {
		return err
	}
	rw := csv.NewWriter(relCSV)
	_ = rw.Write([]string{"address", "shared_counterparty_score", "shared_counterparties"})
	for _, r := range relatedAll {
		_ = rw.Write([]string{r.Address, fmt.Sprintf("%.4f", r.Score), fmt.Sprintf("%d", r.SharedCounterparties)})
	}
	rw.Flush()
	relCSV.Close()

	// investigation-report.json + md
	jsonPath := filepath.Join(dir, "investigation-report.json")
	report := map[string]any{
		"timestamp":      evidence.Timestamp,
		"target":         evidence.Target,
		"summary":        evidence.Summary,
		"trace_paths":    len(evidence.TracePaths),
		"risk_evidence":  evidence.Risk,
		"related_total":  len(relatedAll),
		"evidence_files": []string{"snapshots/evidence.json", "snapshots/paths.csv", "snapshots/related_addresses.csv"},
	}
	reportData, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(jsonPath, reportData, 0644); err != nil {
		return err
	}

	mdPath := filepath.Join(dir, "investigation-report.md")
	var b strings.Builder
	b.WriteString("# V2.1 RC2 调查工作流与资金追踪验证报告\n\n")
	b.WriteString(fmt.Sprintf("- 目标地址: %s\n- 时间: %s\n\n", evidence.Target, evidence.Timestamp.Format("2006-01-02 15:04:05")))
	if evidence.Summary != nil {
		s := evidence.Summary
		b.WriteString("## 调查摘要\n\n")
		b.WriteString("| 项 | 值 |\n|---|---|\n")
		b.WriteString(fmt.Sprintf("| 地址类型 | %s |\n", s.AddressType))
		b.WriteString(fmt.Sprintf("| 交易数 | %d（in %d / out %d）|\n", s.Profile.TransactionCount, s.InCount, s.OutCount))
		b.WriteString(fmt.Sprintf("| Token 数 | %d（Top: %s）|\n", s.Profile.TokenCount, s.TopToken))
		b.WriteString(fmt.Sprintf("| 风险 | %.2f（%s）|\n", s.Risk.RiskScore, s.Risk.RiskLevel))
		b.WriteString(fmt.Sprintf("| 路径数 | %d |\n", s.PathCount))
		b.WriteString(fmt.Sprintf("| 查询耗时 | profile=%sms risk=%sms flows=%sms path=%sms relations=%sms |\n",
			s.QueryDuration["profile_ms"], s.QueryDuration["risk_ms"], s.QueryDuration["flows_ms"],
			s.QueryDuration["path_ms"], s.QueryDuration["relations_ms"]))
	}
	if evidence.Risk != nil {
		b.WriteString("\n## 风险证据\n\n")
		b.WriteString(fmt.Sprintf("- 模式: %s\n", evidence.Risk.Pattern))
		b.WriteString(fmt.Sprintf("- 大额转入 Top5: %v\n", len(evidence.Risk.LargeInflows)))
		b.WriteString(fmt.Sprintf("- 快速转出 Top5: %v\n", len(evidence.Risk.RapidOutflows)))
		b.WriteString(fmt.Sprintf("- 分散目标 Top5: %v\n", len(evidence.Risk.SpreadTargets)))
	}
	b.WriteString("\n## 证据文件\n\n")
	b.WriteString("- `snapshots/evidence.json`\n- `snapshots/paths.csv`\n- `snapshots/related_addresses.csv`\n")
	if err := os.WriteFile(mdPath, []byte(b.String()), 0644); err != nil {
		return err
	}
	if t != nil {
		//nolint:govet // 保持接口简单
		t.Logf("调查证据已生成: %s / %s / snapshots/*", jsonPath, mdPath)
	}
	return nil
}
