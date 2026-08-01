package intelligence

import (
	"context"
	"sort"
	"strings"

	"github.com/etl/backend/internal/analyticsapi"
)

// ── 资金追踪数据源 ──

// FlowSource 提供资金流边（时间维度）。真实实现包装 analyticsapi.Service。
type FlowSource interface {
	// Flows 返回地址的资金流边（含时间/区块）。
	Flows(ctx context.Context, address string) ([]FundEdge, error)
}

// AnalyticsFlowSource 将 analyticsapi.Service.Flows 适配为 FlowSource。
type AnalyticsFlowSource struct {
	svc *analyticsapi.Service
}

// NewAnalyticsFlowSource 创建适配器。
func NewAnalyticsFlowSource(svc *analyticsapi.Service) *AnalyticsFlowSource {
	return &AnalyticsFlowSource{svc: svc}
}

// Flows 实现 FlowSource：查询资金流并转换（含时间维度）。
func (s *AnalyticsFlowSource) Flows(ctx context.Context, address string) ([]FundEdge, error) {
	if s.svc == nil {
		return nil, nil
	}
	edges, err := s.svc.Flows(ctx, address, "")
	if err != nil {
		return nil, err
	}
	out := make([]FundEdge, 0, len(edges))
	for _, e := range edges {
		block := parseUint64(e.Block)
		out = append(out, FundEdge{
			From:      counterpartyForDirection(e, address),
			To:        addressForDirection(e, address),
			Token:     e.Token,
			Amount:    e.Amount,
			TxHash:    e.TxHash,
			Block:     block,
			Timestamp: 0, // 由调用方补充（block_time 未在 FlowEdge 中）
			LogIdx:    "",
		})
	}
	return out, nil
}

func counterpartyForDirection(e analyticsapi.FlowEdge, address string) string {
	if e.Direction == "incoming" {
		return e.Counterparty
	}
	return address
}

func addressForDirection(e analyticsapi.FlowEdge, address string) string {
	if e.Direction == "incoming" {
		return address
	}
	return e.Counterparty
}

// ── Beam Search 资金追踪 ──

// FundTracer 使用 Beam Search 做多跳资金追踪（非简单 BFS）：
// 发现路径 → 计算评分 → 保留 Top K → 继续深入。
type FundTracer struct {
	source FlowSource
	ranker *PathRanker
	cfg    IntelligenceConfig
}

// NewFundTracer 创建追踪器。
func NewFundTracer(source FlowSource, ranker *PathRanker, cfg IntelligenceConfig) *FundTracer {
	return &FundTracer{source: source, ranker: ranker, cfg: cfg}
}

// Trace 从目标地址出发，Beam Search 追踪资金路径（双向：来源与去向）。
// maxHops 控制深度，beamWidth 控制每层保留路径数。
func (t *FundTracer) Trace(ctx context.Context, address string, maxHops, beamWidth int) ([]FundPath, error) {
	if t.source == nil {
		return nil, nil // 无数据源时安全返回空（调查降级）
	}
	address = strings.ToLower(address)
	if maxHops <= 0 {
		maxHops = t.cfg.MaxHops
	}
	if maxHops > 8 {
		maxHops = 8
	}
	if beamWidth <= 0 {
		beamWidth = t.cfg.BeamWidth
	}

	// 出边追踪（资金去向）+ 入边追踪（资金来源）
	outPaths, err := t.beamSearch(ctx, address, maxHops, beamWidth, true)
	if err != nil {
		return nil, err
	}
	inPaths, err := t.beamSearch(ctx, address, maxHops, beamWidth, false)
	if err != nil {
		return nil, err
	}
	return append(outPaths, inPaths...), nil
}

// beamSearch 执行单方向 Beam Search。
// outgoing=true 沿出边（A→B→C）；outgoing=false 沿入边（C→B→A 反向）。
func (t *FundTracer) beamSearch(ctx context.Context, start string, maxHops, beamWidth int, outgoing bool) ([]FundPath, error) {
	type beamItem struct {
		path FundPath
	}
	// 当前层路径
	current := []beamItem{{path: FundPath{Nodes: []string{start}}}}
	var results []FundPath
	seen := map[string]bool{start: true}

	for hop := 0; hop < maxHops; hop++ {
		var next []beamItem
		for _, item := range current {
			last := item.path.Nodes[len(item.path.Nodes)-1]
			edges, err := t.source.Flows(ctx, last)
			if err != nil {
				continue // 单地址失败不阻断
			}
			for _, e := range edges {
				// 方向过滤
				var neighbor string
				if outgoing && e.From == last {
					neighbor = e.To
				} else if !outgoing && e.To == last {
					neighbor = e.From
				} else {
					continue
				}
				neighbor = strings.ToLower(neighbor)
				if neighbor == "" || neighbor == last || seen[neighbor] {
					continue // 无环
				}
				if !t.aboveMinAmount(e.Amount) {
					continue // 金额阈值
				}
				newPath := FundPath{
					Nodes: append(append([]string(nil), item.path.Nodes...), neighbor),
					Edges: append(append([]FundEdge(nil), item.path.Edges...), e),
					Hops:  hop + 1,
				}
				next = append(next, beamItem{path: newPath})
			}
		}
		if len(next) == 0 {
			break
		}
		// Beam：按路径评分排序，保留 Top beamWidth
		sort.Slice(next, func(i, j int) bool {
			return t.ranker.RankPath(next[i].path, nil).Total > t.ranker.RankPath(next[j].path, nil).Total
		})
		if len(next) > beamWidth {
			next = next[:beamWidth]
		}
		// 收集结果
		for _, item := range next {
			results = append(results, item.path)
			seen[item.path.Nodes[len(item.path.Nodes)-1]] = true
		}
		current = next
	}
	return results, nil
}

// aboveMinAmount 判断金额是否高于最小阈值。
func (t *FundTracer) aboveMinAmount(amount string) bool {
	min := t.cfg.MinAmount
	if min == "" || min == "0" {
		return true
	}
	a, okA := parseAmountFloat(amount)
	b, okB := parseAmountFloat(min)
	if !okA || !okB {
		return false
	}
	return a >= b
}
