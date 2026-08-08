package prefetch

import (
	"math"
	"strings"
)

// ScoreInput 是预取评分输入（设计 §13-§15）。
type ScoreInput struct {
	FlowValueScore          float64 // 0-1 金额占比
	InteractionFrequency    float64 // 0-1 频次占比
	PathImportance          float64 // 0-1 路径重要性
	InvestigationRelevance  float64 // 0-1 调查相关性
	AddressRisk             float64 // 0-100 风险分
	UserExpansionProbability float64 // 0-1 用户点击概率
	CacheReuseProbability   float64 // 0-1 复用概率
	EstimatedBytes          uint64
	DatasetCount            int
}

// Score 按设计 §14 公式计算（0-100）。
func Score(in ScoreInput) float64 {
	flow := clamp01(in.FlowValueScore) * 0.25
	freq := clamp01(in.InteractionFrequency) * 0.20
	path := clamp01(in.PathImportance) * 0.15
	rel := clamp01(in.InvestigationRelevance) * 0.15
	risk := clamp01(in.AddressRisk/100) * 0.10
	expand := clamp01(in.UserExpansionProbability) * 0.10
	reuse := clamp01(in.CacheReuseProbability) * 0.05
	base := flow + freq + path + rel + risk + expand + reuse
	// CostPenalty：预估字节（GB 封顶 0.3）；SizePenalty：数据集数量（0.1/个，封顶 0.2）
	bytesGB := float64(in.EstimatedBytes) / (1024 * 1024 * 1024)
	costPenalty := math.Min(bytesGB, 0.3)
	sizePenalty := math.Min(float64(in.DatasetCount)*0.1, 0.2)
	score := (base - costPenalty - sizePenalty) * 100
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return math.Round(score*10) / 10
}

// PriorityFor 按分数映射三档优先级（设计 §17-§19）。
func PriorityFor(score float64) Priority {
	switch {
	case score >= 70:
		return PriorityHOT
	case score >= 45:
		return PriorityWARM
	default:
		return PriorityCOLD
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// defaultDatasets 是预取最小 Bundle（设计 §20-§22）。
var defaultDatasets = []string{"transactions", "token_transfers", "internal_transactions", "balances"}

// MinimalBundle 返回调查最小数据包。
func MinimalBundle() []string {
	out := make([]string, len(defaultDatasets))
	copy(out, defaultDatasets)
	return out
}

// GraphBundle 返回关系图展开优先数据包。
func GraphBundle() []string {
	return []string{"transactions", "token_transfers", "internal_transactions"}
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

