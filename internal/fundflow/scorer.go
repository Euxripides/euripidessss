package fundflow

import (
	"math"
	"strings"
)

// scorePath 计算路径评分（设计 §29-§33）与置信度（§32）。
func scorePath(p *Path, goal string) (float64, float64) {
	value := valueScore(p)
	profit := profitRelevance(p)
	settle := settlementLikelihood(p)
	entity := entityRelevance(p, goal)
	temporal := temporalContinuity(p)
	confidence := pathConfidence(p)
	novelty := 0.05
	noise := noisePenalty(p)
	goalWeight := goalWeights(goal)
	score := goalWeight.value*value +
		goalWeight.profit*profit +
		goalWeight.settlement*settle +
		goalWeight.entity*entity +
		0.10*temporal +
		0.10*confidence +
		0.05*novelty -
		noise
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return math.Round(score*1000) / 1000, math.Round(confidence*1000) / 1000
}

type goalWeightsT struct{ value, profit, settlement, entity float64 }

// goalWeights 按调查目标调整权重（设计 §48-§50，基础版）。
func goalWeights(goal string) goalWeightsT {
	switch strings.ToLower(strings.TrimSpace(goal)) {
	case "settlement":
		return goalWeightsT{0.25, 0.15, 0.30, 0.10}
	case "profit":
		return goalWeightsT{0.20, 0.35, 0.15, 0.10}
	case "collector":
		return goalWeightsT{0.25, 0.15, 0.15, 0.25}
	default: // cashout
		return goalWeightsT{0.25, 0.20, 0.15, 0.20}
	}
}

// valueScore 相对根地址总流量的金额重要性（§30）。
func valueScore(p *Path) float64 {
	total := float64(0)
	for _, n := range p.Nodes {
		if v, ok := parseFloatAmount(n.InAmount); ok {
			total += v
		}
	}
	if total <= 0 {
		return 0
	}
	// 相对量：直接以 1 - 1/(1+log10(total+1)) 归一，避免绝对金额偏差
	return 1 - 1/(1+math.Log10(total+1))
}

func profitRelevance(p *Path) float64 {
	if p.TerminalType == "EXCHANGE" || p.TerminalType == "CEX_DEPOSIT" || p.TerminalType == "PAYMENT_SERVICE" {
		return 0.9
	}
	if p.PathType == "DIRECT_CASHOUT" || p.PathType == "MULTI_HOP_CASHOUT" {
		return 0.8
	}
	return 0.3
}

func settlementLikelihood(p *Path) float64 {
	switch p.TerminalType {
	case "DORMANT_WALLET", "COLD_STORAGE", "TREASURY", "SETTLEMENT":
		return 0.9
	case "UNKNOWN_SERVICE":
		return 0.6
	default:
		return 0.2
	}
}

func entityRelevance(p *Path, goal string) float64 {
	if p.TerminalType == "EXCHANGE" || p.TerminalType == "CEX_DEPOSIT" || p.TerminalType == "CEX_HOT_WALLET" {
		return 0.9
	}
	if goal == "collector" && p.PathType == "COLLECT_AND_SETTLE" {
		return 0.85
	}
	return 0.3
}

// temporalContinuity 时间连续性（§31）：当前无区块时间，按同路径区块间隔粗略评分。
func temporalContinuity(p *Path) float64 {
	if len(p.Nodes) <= 1 {
		return 0.5
	}
	// 路径内区块跨度越小越连续
	first := p.Nodes[0].BlockNumber
	last := p.Nodes[len(p.Nodes)-1].BlockNumber
	if last == 0 || first == 0 || last <= first {
		return 0.5
	}
	gap := last - first
	switch {
	case gap < 1000:
		return 0.95
	case gap < 10000:
		return 0.8
	case gap < 100000:
		return 0.5
	default:
		return 0.2
	}
}

func pathConfidence(p *Path) float64 {
	conf := 0.6
	for _, n := range p.Nodes {
		if n.EntityID != "" {
			conf += 0.05
		}
	}
	if conf > 0.9 {
		conf = 0.9
	}
	return conf
}

// noisePenalty 噪声惩罚（§33）：DEX Router/高频服务/热门合约降低路径分。
func noisePenalty(p *Path) float64 {
	penalty := 0.0
	for _, n := range p.Nodes {
		switch strings.ToUpper(n.EntityType) {
		case "ROUTER", "DEX":
			penalty += 0.15
		case "CONTRACT":
			penalty += 0.05
		}
	}
	if penalty > 0.3 {
		penalty = 0.3
	}
	return penalty
}

func parseFloatAmount(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "<nil>" {
		return 0, false
	}
	var f float64
	if _, err := fmtSscanf(s, &f); err != nil {
		return 0, false
	}
	return f, true
}

