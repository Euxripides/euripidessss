package prefetch

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/etl/backend/internal/graphcache"
	invcache "github.com/etl/backend/internal/investigation/cache"
)

// Planner 生成并评分预取候选（设计 §11-§19、§23-§26）。
type Planner struct {
	coverage graphcache.CoverageQuerier
	maxHOT   int
	maxWARM  int
}

// NewPlanner 创建规划器。
func NewPlanner(coverage graphcache.CoverageQuerier) *Planner {
	return &Planner{coverage: coverage, maxHOT: 3, maxWARM: 10}
}

// Plan 从图扩展结果生成候选。
// result 为 nil 时返回空（调用方应先生成图扩展缓存）。
func (p *Planner) Plan(_ context.Context, invID, chainKey string, chainID int64,
	result *graphcache.Result, snap invcache.ContextSnapshot) ([]Candidate, error) {
	if result == nil {
		return nil, nil
	}
	pathSet := map[string]bool{}
	for _, a := range snap.CurrentPath {
		pathSet[strings.ToLower(strings.TrimSpace(a))] = true
	}
	tokenSet := map[string]bool{}
	for _, t := range snap.Tokens {
		tokenSet[strings.ToLower(strings.TrimSpace(t))] = true
	}
	// 金额与频次归一化基座
	maxTx := int64(1)
	maxAmount := float64(1)
	amountOf := map[string]float64{}
	for _, e := range result.Edges {
		if e.TxCount > maxTx {
			maxTx = e.TxCount
		}
		if f, ok := parseFloat(e.Inflow); ok && f > maxAmount {
			maxAmount = f
		}
		if f, ok := parseFloat(e.Outflow); ok && f > maxAmount {
			maxAmount = f
		}
		key := e.Counterparty + "|" + e.Direction + "|" + e.Token
		if f, ok := parseFloat(e.Inflow); ok {
			amountOf[key] += f
		}
		if f, ok := parseFloat(e.Outflow); ok {
			amountOf[key] += f
		}
	}
	// 按对手聚合多方向/多 Token，避免同一地址重复候选
	byCP := map[string]*candAgg{}
	for _, e := range result.Edges {
		cp := strings.ToLower(strings.TrimSpace(e.Counterparty))
		if cp == "" || cp == result.Key.Address {
			continue
		}
		a := byCP[cp]
		if a == nil {
			a = &candAgg{address: cp, datasets: GraphBundle()}
			byCP[cp] = a
		}
		a.txCount += e.TxCount
		a.amount += amountOf[e.Counterparty+"|"+e.Direction+"|"+e.Token]
		if e.Token != "" && !contains(a.tokens, e.Token) {
			a.tokens = append(a.tokens, e.Token)
		}
	}
	var candidates []Candidate
	for _, a := range byCP {
		covered := p.coverageFor(chainKey, a.address, snap.FromBlock, snap.ToBlock)
		reuse := 1 - covered
		if covered >= 1 {
			continue // FULL HIT：预取成本 0，不需要入队（设计 §26）
		}
		flowScore := a.amount / maxAmount
		freqScore := float64(a.txCount) / float64(maxTx)
		pathScore := 0.0
		if pathSet[a.address] {
			pathScore = 1
		} else if pathSet[result.Key.Address] {
			pathScore = 0.6 // 焦点地址在当前路径上，对手为下一跳
		}
		rel := 0.3
		if len(tokenSet) == 0 || anyToken(a.tokens, tokenSet) {
			rel = 1
		}
		risk := 30.0
		for _, n := range result.Nodes {
			if strings.EqualFold(n.Address, a.address) {
				risk = 30
				break
			}
		}
		score := Score(ScoreInput{
			FlowValueScore:           flowScore,
			InteractionFrequency:     freqScore,
			PathImportance:           pathScore,
			InvestigationRelevance:   rel,
			AddressRisk:              risk,
			UserExpansionProbability: 0.5,
			CacheReuseProbability:    reuse,
			DatasetCount:             len(a.datasets),
		})
		pri := PriorityFor(score)
		reasons := []string{}
		if a.amount > 0 {
			reasons = append(reasons, "high_value")
		}
		if a.txCount >= 2 {
			reasons = append(reasons, "frequent_interaction")
		}
		if pathScore >= 0.6 {
			reasons = append(reasons, "path_next_hop")
		}
		if covered > 0 {
			reasons = append(reasons, "partial_coverage")
		}
		candidates = append(candidates, Candidate{
			ChainID:          chainID,
			ChainKey:         chainKey,
			Address:          a.address,
			ParentAddress:    result.Key.Address,
			Reason:           reasons,
			Score:            score,
			RequiredDatasets: a.datasets,
			Priority:         pri,
			TokenFilter:      result.Key.TokenFilter,
			FromBlock:        snap.FromBlock,
			ToBlock:          snap.ToBlock,
			InvestigationID:  invID,
			CreatedAt:        nowUTC(),
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	// 优先级按排名映射（设计 §17-§19：前 3 大资金对手 = HOT，Top 10 = WARM，其余 COLD）
	for i := range candidates {
		switch {
		case i < p.maxHOT:
			candidates[i].Priority = PriorityHOT
		case i < p.maxHOT+p.maxWARM:
			candidates[i].Priority = PriorityWARM
		default:
			candidates[i].Priority = PriorityCOLD
		}
	}
	out := make([]Candidate, 0, len(candidates))
	for i := range candidates {
		if i >= p.maxHOT+p.maxWARM+10 {
			break // COLD 只保留前 10 条元数据
		}
		out = append(out, candidates[i])
	}
	return out, nil
}

// coverageFor 取数据集 Bundle 的最小覆盖。
func (p *Planner) coverageFor(chainKey, address string, from, to uint64) float64 {
	if p.coverage == nil {
		return 0
	}
	ratio := 1.0
	for _, ds := range GraphBundle() {
		ci := p.coverage.QueryCoverage(chainKey, address, ds, from, to)
		if ci.Ratio < ratio {
			ratio = ci.Ratio
		}
	}
	return ratio
}

type candAgg struct {
	address  string
	datasets []string
	txCount  int64
	amount   float64
	tokens   []string
}

func anyToken(have []string, want map[string]bool) bool {
	for _, t := range have {
		if want[strings.ToLower(t)] {
			return true
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func parseFloat(s string) (float64, bool) {
	if strings.TrimSpace(s) == "" {
		return 0, false
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return 0, false
	}
	return f, true
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
