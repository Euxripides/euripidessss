// Package casefile implements 案件分析与资金追踪报告生成系统。
// 案件模型（状态机）+ 调查流程 + 多目标分析 + 时间线 + 关系图 + 报告（md/json/docx）。
package casefile

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/balance"
	"github.com/etl/backend/internal/investigation"
)

// CaseStatus 案件状态。
type CaseStatus string

const (
	StatusCreated   CaseStatus = "CREATED"
	StatusRunning   CaseStatus = "RUNNING"
	StatusCompleted CaseStatus = "COMPLETED"
	StatusFailed    CaseStatus = "FAILED"
	StatusArchived  CaseStatus = "ARCHIVED"
)

// Case 是调查案件模型。
type Case struct {
	CaseID          string    `json:"case_id"`
	Title           string    `json:"title"`
	TargetAddresses []string  `json:"target_addresses"`
	CreatedAt       time.Time `json:"created_at"`
	Status          CaseStatus  `json:"status"`
	Investigator    string    `json:"investigator"`
	DatasetVersion  string    `json:"dataset_version"`
	Error           string    `json:"error,omitempty"`

	// 调查结果
	Summaries      map[string]*investigation.Summary `json:"summaries"`
	TracePaths     []investigation.TracePath         `json:"trace_paths"`
	Related        []investigation.RelatedAddress    `json:"related_addresses"`
	Risks          map[string]*investigation.RiskEvidence `json:"risk_evidence"`
	CommonSources  []CommonFlow                      `json:"common_sources"`
	CommonSinks    []CommonFlow                      `json:"common_sinks"`
	Timeline       []TimelineEvent                   `json:"timeline"`
	Graph          *Graph                            `json:"graph"`
	Assets         map[string]*balance.Snapshot      `json:"assets,omitempty"`
}

// CommonFlow 是公共资金来源/去向。
type CommonFlow struct {
	Address  string   `json:"address"`
	Targets  []string `json:"targets"`
	Count    int      `json:"count"`
	Tokens   []string `json:"tokens,omitempty"`
}

// TimelineEvent 是时间线事件。
type TimelineEvent struct {
	Time    string `json:"time"`
	Address string `json:"address"`
	Event   string `json:"event"`
	Token   string `json:"token,omitempty"`
	Amount  string `json:"amount"`
	TxHash  string `json:"tx_hash"`
	Block   string `json:"block,omitempty"`
	LogIdx  string `json:"log_index,omitempty"`
}

// Graph 是地址关系图。
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// GraphNode 是图节点。
type GraphNode struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	RiskScore float64 `json:"risk_score"`
	Degree   int     `json:"degree"`
}

// GraphEdge 是图边。
type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"` // transfer / interaction / relation
	Token  string `json:"token,omitempty"`
	Amount string `json:"amount,omitempty"`
}

// Engine 封装调查依赖。
type Engine struct {
	Inv     *investigation.Investigator
	Svc     *analyticsapi.Service
	Engine  *duckdb.Engine
	Parquet string
	Bal     *balance.BalanceEngine
}

// NewEngine 创建调查引擎。
func NewEngine(svc *analyticsapi.Service, engine *duckdb.Engine, parquetPath string) *Engine {
	return &Engine{
		Inv:     investigation.New(svc, engine, parquetPath),
		Svc:     svc,
		Engine:  engine,
		Parquet: strings.ReplaceAll(parquetPath, "\\", "/"),
		Bal:     balance.New(engine, parquetPath),
	}
}

// NewCase 创建案件（CREATED）。
func NewCase(caseID string, targets []string, investigator, datasetVersion string) *Case {
	return NewCaseWithTitle(caseID, caseID, targets, investigator, datasetVersion)
}

// NewCaseWithTitle 创建带标题的案件。
func NewCaseWithTitle(caseID, title string, targets []string, investigator, datasetVersion string) *Case {
	norm := make([]string, 0, len(targets))
	seen := map[string]bool{}
	for _, t := range targets {
		t = strings.ToLower(strings.TrimSpace(t))
		if len(t) == 42 && !seen[t] {
			seen[t] = true
			norm = append(norm, t)
		}
	}
	return &Case{
		CaseID:          caseID,
		Title:           title,
		TargetAddresses: norm,
		CreatedAt:       time.Now().UTC(),
		Status:          StatusCreated,
		Investigator:    investigator,
		DatasetVersion:  datasetVersion,
	}
}

// Run 执行完整调查流程：CREATED→RUNNING→COMPLETED/FAILED。
func (c *Case) Run(ctx context.Context, eng *Engine) error {
	c.Status = StatusRunning

	c.Summaries = map[string]*investigation.Summary{}
	c.Risks = map[string]*investigation.RiskEvidence{}
	for _, addr := range c.TargetAddresses {
		summary, err := eng.Inv.Investigate(ctx, addr)
		if err != nil {
			c.Status = StatusFailed
			c.Error = fmt.Sprintf("investigate %s: %v", addr, err)
			return err
		}
		c.Summaries[addr] = summary
		risk, err := eng.Inv.RiskScenario(ctx, addr)
		if err != nil {
			c.Status = StatusFailed
			c.Error = fmt.Sprintf("risk %s: %v", addr, err)
			return err
		}
		c.Risks[addr] = risk
	}

	// 资产快照（余额/历史最高/时间线）
	c.Assets = map[string]*balance.Snapshot{}
	for _, addr := range c.TargetAddresses {
		snap, err := eng.Bal.BuildSnapshot(ctx, addr)
		if err != nil {
			c.Status = StatusFailed
			c.Error = fmt.Sprintf("assets %s: %v", addr, err)
			return err
		}
		c.Assets[addr] = snap
	}

	// 多目标资金追踪（每个目标 2 跳）
	for _, addr := range c.TargetAddresses {
		paths, err := eng.Inv.TraceFunds(ctx, addr, 2)
		if err != nil {
			c.Status = StatusFailed
			c.Error = fmt.Sprintf("trace %s: %v", addr, err)
			return err
		}
		c.TracePaths = append(c.TracePaths, paths...)
	}

	// 关联发现（全部目标）
	related, err := eng.Inv.DiscoverRelations(ctx, c.TargetAddresses, 20)
	if err != nil {
		c.Status = StatusFailed
		c.Error = fmt.Sprintf("relations: %v", err)
		return err
	}
	c.Related = related

	// 多目标分析：公共来源/去向
	c.CommonSources, c.CommonSinks = c.commonFlows(ctx, eng)

	// 时间线
	c.Timeline = c.buildTimeline(ctx, eng)

	// 关系图
	c.Graph = c.buildGraph(ctx, eng)

	c.Status = StatusCompleted
	return nil
}

// commonFlows 计算公共资金来源（多个目标共同的 from）与公共去向。
func (c *Case) commonFlows(ctx context.Context, eng *Engine) ([]CommonFlow, []CommonFlow) {
	targets := map[string]bool{}
	for _, t := range c.TargetAddresses {
		targets[t] = true
	}
	norm1 := `CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END`
	norm2 := `CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END`
	rows, err := eng.Engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT %[1]s AS f, %[2]s AS t, address AS token FROM read_parquet('%[3]s')
		 WHERE topic0 IN ('%[4]s','%[5]s','%[6]s')`,
		norm1, norm2, eng.Parquet,
		analyticsapi.TransferTopic, analyticsapi.TransferSingle, analyticsapi.TransferBatch))
	if err != nil {
		return nil, nil
	}
	sources := map[string]map[string]bool{} // from -> targets
	sinks := map[string]map[string]bool{}   // to -> targets
	for _, r := range rows {
		f := strings.ToLower(fmt.Sprintf("%v", r["f"]))
		t := strings.ToLower(fmt.Sprintf("%v", r["t"]))
		if targets[t] && !targets[f] {
			if sources[f] == nil {
				sources[f] = map[string]bool{}
			}
			sources[f][t] = true
		}
		if targets[f] && !targets[t] {
			if sinks[t] == nil {
				sinks[t] = map[string]bool{}
			}
			sinks[t][f] = true
		}
	}
	// 简化：按覆盖目标数排序
	toCommon := func(m map[string]map[string]bool, limit int) []CommonFlow {
		type kv struct {
			addr    string
			covered int
		}
		var list []kv
		for addr, covered := range m {
			if len(covered) >= 2 {
				list = append(list, kv{addr, len(covered)})
			}
		}
		sort.Slice(list, func(i, j int) bool { return list[i].covered > list[j].covered })
		if len(list) > limit {
			list = list[:limit]
		}
		var out []CommonFlow
		for _, item := range list {
			var targetsList []string
			for t := range m[item.addr] {
				if !strings.Contains(t, "\x00") {
					targetsList = append(targetsList, t)
				}
			}
			out = append(out, CommonFlow{Address: item.addr, Targets: targetsList, Count: item.covered})
		}
		return out
	}
	return toCommon(sources, 10), toCommon(sinks, 10)
}

// buildTimeline 生成时间线（按 block_time 排序）。
func (c *Case) buildTimeline(ctx context.Context, eng *Engine) []TimelineEvent {
	norm1 := `CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END`
	norm2 := `CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END`
	targets := strings.Join(c.TargetAddresses, "','")
	rows, err := eng.Engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT %[1]s AS f, %[2]s AS t, address AS token, data, transaction_hash, block_time
		 FROM read_parquet('%[3]s')
		 WHERE topic0 IN ('%[4]s','%[5]s','%[6]s')
		   AND (%[1]s IN ('%[7]s') OR %[2]s IN ('%[7]s'))
		 ORDER BY block_time`, norm1, norm2, eng.Parquet,
		analyticsapi.TransferTopic, analyticsapi.TransferSingle, analyticsapi.TransferBatch, targets))
	if err != nil {
		return nil
	}
	var events []TimelineEvent
	for _, r := range rows {
		amount := ""
		if d := fmt.Sprintf("%v", r["data"]); len(d) >= 3 {
			hexPart := strings.TrimPrefix(strings.ToLower(d), "0x")
			if len(hexPart) > 64 {
				hexPart = hexPart[len(hexPart)-64:]
			}
			if n, ok := newBigInt(hexPart); ok {
				amount = n
			}
		}
		events = append(events, TimelineEvent{
			Time:    fmt.Sprintf("%v", r["block_time"]),
			Address: fmt.Sprintf("%v %v→%v", r["token"], r["f"], r["t"]),
			Event:   "TRANSFER",
			Amount:  amount,
			TxHash:  fmt.Sprintf("%v", r["transaction_hash"]),
		})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Time < events[j].Time })
	if len(events) > 100 {
		events = events[:100]
	}
	return events
}

func newBigInt(hexPart string) (string, bool) {
	n := new(big.Int)
	if _, ok := n.SetString(hexPart, 16); !ok {
		return "", false
	}
	return n.String(), true
}

// buildGraph 构建地址关系图（transfer 边 + relation 边）。
func (c *Case) buildGraph(ctx context.Context, eng *Engine) *Graph {
	g := &Graph{}
	nodeMap := map[string]*GraphNode{}
	addNode := func(addr, typ string, risk float64) {
		if _, ok := nodeMap[addr]; !ok {
			nodeMap[addr] = &GraphNode{ID: addr, Type: typ, RiskScore: risk}
		}
	}
	for _, addr := range c.TargetAddresses {
		addNode(addr, "target", 0)
	}
	for _, r := range c.Related {
		addNode(r.Address, "related", r.Score)
	}
	// transfer 边（目标参与）
	norm1 := `CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END`
	norm2 := `CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END`
	targets := strings.Join(c.TargetAddresses, "','")
	rows, err := eng.Engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT %[1]s AS f, %[2]s AS t, address AS token, data
		 FROM read_parquet('%[3]s')
		 WHERE topic0 IN ('%[4]s','%[5]s','%[6]s')
		   AND (%[1]s IN ('%[7]s') OR %[2]s IN ('%[7]s'))
		 LIMIT 200`, norm1, norm2, eng.Parquet,
		analyticsapi.TransferTopic, analyticsapi.TransferSingle, analyticsapi.TransferBatch, targets))
	if err == nil {
		edgeSeen := map[string]bool{}
		for _, r := range rows {
			f := strings.ToLower(fmt.Sprintf("%v", r["f"]))
			t := strings.ToLower(fmt.Sprintf("%v", r["t"]))
			amount := ""
			if d := fmt.Sprintf("%v", r["data"]); len(d) >= 3 {
				hexPart := strings.TrimPrefix(strings.ToLower(d), "0x")
				if len(hexPart) > 64 {
					hexPart = hexPart[len(hexPart)-64:]
				}
				if n, ok := newBigInt(hexPart); ok {
					amount = n
				}
			}
			addNode(f, "address", 0)
			addNode(t, "address", 0)
			key := f + "|" + t
			if !edgeSeen[key] {
				edgeSeen[key] = true
				g.Edges = append(g.Edges, GraphEdge{
					Source: f, Target: t, Kind: "transfer",
					Token: fmt.Sprintf("%v", r["token"]), Amount: amount,
				})
			}
		}
	}
	// relation 边（目标↔关联）
	for _, r := range c.Related {
		for _, t := range c.TargetAddresses {
			g.Edges = append(g.Edges, GraphEdge{Source: t, Target: r.Address, Kind: "relation"})
		}
	}
	// degree
	degree := map[string]int{}
	for _, e := range g.Edges {
		degree[e.Source]++
		degree[e.Target]++
	}
	for _, n := range nodeMap {
		n.Degree = degree[n.ID]
	}
	for _, n := range nodeMap {
		g.Nodes = append(g.Nodes, *n)
	}
	return g
}
