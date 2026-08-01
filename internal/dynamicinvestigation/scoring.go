package dynamicinvestigation

import (
	"math"
	"math/big"
	"strings"
)

// ── Expansion Score ──
//
// Expansion Score = 资金金额 + 风险权重 + 关联强度 + 活跃度 − 实体惩罚
// 各分项归一化到 0-100，按权重合成，最后按实体类型施加惩罚。
// 用于判断是否继续下载（是否批准采集）。

// ── 金额归一化 ──

// 金额分档：使用 log 尺度的经验分档，避免极端值主导。
var amountBuckets = []struct {
	threshold *big.Float
	score     float64
}{
	{big.NewFloat(0), 0},
	{big.NewFloat(1e3), 10},   // ≥ 1K
	{big.NewFloat(1e5), 25},   // ≥ 100K
	{big.NewFloat(1e6), 40},   // ≥ 1M
	{big.NewFloat(1e7), 60},   // ≥ 10M
	{big.NewFloat(1e8), 80},   // ≥ 100M
	{big.NewFloat(1e9), 100},  // ≥ 1B
}

// amountScore 将 raw decimal 金额映射到 0-100。
func amountScore(amount string) float64 {
	amount = strings.TrimSpace(amount)
	if amount == "" || amount == "0" || amount == "0x0" {
		return 0
	}
	var f *big.Float
	if strings.HasPrefix(amount, "0x") {
		if n, ok := new(big.Int).SetString(strings.TrimPrefix(amount, "0x"), 16); ok {
			f = new(big.Float).SetInt(n)
		} else {
			return 0
		}
	} else if n, ok := new(big.Int).SetString(amount, 10); ok {
		f = new(big.Float).SetInt(n)
	} else {
		return 0
	}
	if f.Sign() <= 0 {
		return 0
	}
	score := 0.0
	for _, b := range amountBuckets {
		if f.Cmp(b.threshold) >= 0 {
			score = b.score
		} else {
			break
		}
	}
	return math.Min(score, 100)
}

// riskScore 归一化风险分（0-100 直接可用）。
func riskScore(r float64) float64 {
	return math.Max(0, math.Min(100, r))
}

// relationScore 关联强度：0-1 → 0-100。
func relationScore(s float64) float64 {
	return math.Max(0, math.Min(1, s)) * 100
}

// activityScore 活跃度：交易笔数与图度。
func activityScore(txCount int64, degree int) float64 {
	// 交易笔数：≥1000 笔得满分
	tx := math.Min(float64(txCount)/1000.0, 1.0) * 70
	// 图度：≥50 度得 30 分
	d := math.Min(float64(degree)/50.0, 1.0) * 30
	return tx + d
}

// entityPenalty 实体惩罚：大型实体（交易所/桥/DEX）降低扩展优先级，
// 防止把整个交易所生态拉入队列。
func entityPenalty(entity EntityType) float64 {
	switch entity {
	case EntityExchange:
		return 1.0
	case EntityBridge, EntityDex:
		return 0.7
	case EntityRouter:
		return 0.5
	case EntityContract:
		return 0.3
	default:
		return 0
	}
}

// ── 评分主函数 ──

// Score 计算地址的 Expansion Score 并给出采集决策。
// 权重来自 ExpansionConfig（未设置时使用默认权重）。
func Score(input ScoreInput, cfg ExpansionConfig) ScoreResult {
	// 权重兜底
	if cfg.AmountWeight == 0 && cfg.RiskWeight == 0 && cfg.RelationWeight == 0 && cfg.ActivityWeight == 0 {
		cfg = DefaultConfig()
	}
	totalWeight := cfg.AmountWeight + cfg.RiskWeight + cfg.RelationWeight + cfg.ActivityWeight
	if totalWeight <= 0 {
		totalWeight = 1
	}

	a := amountScore(input.Amount)
	r := riskScore(input.RiskScore)
	rel := relationScore(input.RelationScore)
	act := activityScore(input.TxCount, input.Degree)

	weighted := (a*cfg.AmountWeight + r*cfg.RiskWeight + rel*cfg.RelationWeight + act*cfg.ActivityWeight) / totalWeight

	penalty := entityPenalty(input.Entity)
	if penalty > 0 && cfg.EntityPenalty > 0 {
		// 实体惩罚按权重比例折算
		penaltyScore := penalty * cfg.EntityPenalty
		weighted = weighted * (1 - penaltyScore/100.0)
	}

	score := math.Round(weighted*100) / 100

	decision := DecisionHold
	reason := ""
	switch {
	case score <= 0 && input.Amount == "" && input.TxCount == 0:
		decision = DecisionIgnore
		reason = "无资金关系且无交易活动"
	case score >= cfg.MinScore:
		decision = DecisionAcquire
		reason = "Expansion Score 达标"
	case score >= cfg.MinScore*0.5:
		decision = DecisionHold
		reason = "未达批准阈值，保留观察"
	default:
		decision = DecisionIgnore
		reason = "低价值地址，仅保存关系"
	}

	return ScoreResult{
		Score: score,
		Breakdown: map[string]float64{
			"amount":        math.Round(a*100) / 100,
			"risk":          math.Round(r*100) / 100,
			"relation":      math.Round(rel*100) / 100,
			"activity":      math.Round(act*100) / 100,
			"entity_penalty": math.Round(penaltyScore(input.Entity, cfg)*100) / 100,
		},
		Decision: decision,
		Reason:   reason,
	}
}

// penaltyScore 返回实际扣除的分值（0-100）。
func penaltyScore(entity EntityType, cfg ExpansionConfig) float64 {
	p := entityPenalty(entity)
	if p <= 0 || cfg.EntityPenalty <= 0 {
		return 0
	}
	return p * cfg.EntityPenalty
}
