package intelligence

import (
	"math"
	"math/big"
	"strings"
)

// ── 路径排名 ──
//
// PathScore = 金额权重 + 时间连续性 + 风险权重 + 关系强度 − 实体惩罚
// 用于 Beam Search 每层保留 Top K 路径，以及最终路径排序。

// PathRanker 计算路径评分。
type PathRanker struct {
	// 权重（默认值见 defaults）
	AmountWeight     float64
	TimeWeight       float64
	RiskWeight       float64
	RelationWeight   float64
	EntityPenaltyMax float64
}

// DefaultPathRanker 返回默认权重排名器。
func DefaultPathRanker() *PathRanker {
	return &PathRanker{
		AmountWeight:     40,
		TimeWeight:       20,
		RiskWeight:       20,
		RelationWeight:   20,
		EntityPenaltyMax: 20,
	}
}

// RankPath 计算路径评分。
// entities 可为 nil（未知实体时不施加惩罚）。
func (r *PathRanker) RankPath(path FundPath, entities map[string]string) PathScore {
	if r.AmountWeight == 0 && r.TimeWeight == 0 && r.RiskWeight == 0 && r.RelationWeight == 0 {
		r = DefaultPathRanker()
	}
	totalWeight := r.AmountWeight + r.TimeWeight + r.RiskWeight + r.RelationWeight
	if totalWeight <= 0 {
		totalWeight = 1
	}

	amount := r.amountScore(path)
	timeC := r.timeContinuityScore(path)
	risk := r.riskScore(path, entities)
	relation := r.relationScore(path)
	penalty := r.entityPenalty(path, entities)

	score := (amount*r.AmountWeight + timeC*r.TimeWeight + risk*r.RiskWeight + relation*r.RelationWeight) / totalWeight
	score = score * (1 - penalty/100.0)
	if score < 0 {
		score = 0
	}

	return PathScore{
		Amount:         math.Round(amount*100) / 100,
		TimeContinuity: math.Round(timeC*100) / 100,
		Risk:           math.Round(risk*100) / 100,
		Relation:       math.Round(relation*100) / 100,
		EntityPenalty:  math.Round(penalty*100) / 100,
		Total:          math.Round(score*100) / 100,
	}
}

// amountScore 路径总金额归一化（log 尺度假定，0-100）。
func (r *PathRanker) amountScore(path FundPath) float64 {
	var total float64
	for _, e := range path.Edges {
		if f, ok := parseAmountFloat(e.Amount); ok {
			total += f
		}
	}
	if total <= 0 {
		return 0
	}
	// log10 尺度：1 → 0, 1e9 → 90, 1e12 → 100
	score := math.Log10(total) * 10
	return math.Min(score, 100)
}

// timeContinuityScore 时间连续性：相邻边时间间隔越小得分越高（0-100）。
func (r *PathRanker) timeContinuityScore(path FundPath) float64 {
	if len(path.Edges) < 2 {
		return 50 // 单跳路径给予中等分
	}
	totalGap := 0.0
	for i := 1; i < len(path.Edges); i++ {
		gap := math.Abs(float64(path.Edges[i].Timestamp - path.Edges[i-1].Timestamp))
		// 时间戳缺失时按 Block 差估算
		if gap == 0 && path.Edges[i].Block > path.Edges[i-1].Block {
			gap = float64(path.Edges[i].Block-path.Edges[i-1].Block) * 3 // 约 3 秒/块
		}
		totalGap += gap
	}
	avgGap := totalGap / float64(len(path.Edges)-1)
	// 1 小时内连续 → 高分；>7 天 → 低分
	switch {
	case avgGap <= 3600:
		return 90
	case avgGap <= 86400:
		return 70
	case avgGap <= 7*86400:
		return 40
	default:
		return 10
	}
}

// riskScore 路径风险：目标/中转地址风险分均值（0-100）。
func (r *PathRanker) riskScore(path FundPath, entities map[string]string) float64 {
	// 无实体风险信息时按路径节点数给出基础分（路径越长风险越高）
	if len(entities) == 0 {
		n := float64(len(path.Nodes))
		if n >= 4 {
			return 70
		}
		return n * 15
	}
	var sum float64
	count := 0
	for _, n := range path.Nodes {
		if _, ok := entities[n]; ok {
			sum += 50 // 有实体信息的节点风险基础分
			count++
		}
	}
	if count == 0 {
		return 30
	}
	return sum / float64(count)
}

// relationScore 关系强度：路径节点间共同关联（基于边数量，0-100）。
func (r *PathRanker) relationScore(path FundPath) float64 {
	// 每跳一条边，边越多关系链越丰富；但过长路径关系稀释
	if len(path.Edges) == 0 {
		return 0
	}
	score := math.Min(float64(len(path.Edges))*20, 100)
	return score
}

// entityPenalty 实体惩罚：交易所/桥/DEX 等大型实体降低路径优先级（0-100）。
func (r *PathRanker) entityPenalty(path FundPath, entities map[string]string) float64 {
	if len(entities) == 0 {
		return 0
	}
	penalty := 0.0
	for _, n := range path.Nodes {
		switch entities[n] {
		case "exchange":
			penalty += 0.6
		case "bridge", "dex":
			penalty += 0.4
		case "router":
			penalty += 0.3
		case "contract":
			penalty += 0.2
		}
	}
	p := math.Min(penalty/float64(len(path.Nodes))*r.EntityPenaltyMax, r.EntityPenaltyMax)
	if p < 0 {
		p = 0
	}
	return p
}

// ── 工具 ──

func parseAmountFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if strings.HasPrefix(s, "0x") {
		n, ok := new(big.Int).SetString(strings.TrimPrefix(s, "0x"), 16)
		if !ok {
			return 0, false
		}
		f, _ := new(big.Float).SetInt(n).Float64()
		return f, true
	}
	if n, ok := new(big.Int).SetString(s, 10); ok {
		f, _ := new(big.Float).SetInt(n).Float64()
		return f, true
	}
	return 0, false
}

func parseUint64(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var n uint64
	if _, err := fmtSscanfUint(s, &n); err == nil {
		return n
	}
	return 0
}

func fmtSscanfUint(s string, n *uint64) (int, error) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errInvalidUint
		}
		*n = *n*10 + uint64(c-'0')
	}
	return 1, nil
}

type invalidUintError struct{}

func (e *invalidUintError) Error() string { return "invalid uint" }

var errInvalidUint = &invalidUintError{}
