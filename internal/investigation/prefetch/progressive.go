package prefetch

// ProgressiveStage 是渐进式预取阶段窗口（设计 §25、P1）。
type ProgressiveStage struct {
	WindowBlocks uint64
	Label        string
}

// DefaultProgressiveStages 约 7 天 / 90 天（BSC 3s/块）。
func DefaultProgressiveStages() []ProgressiveStage {
	return []ProgressiveStage{
		{WindowBlocks: 201600, Label: "7d"},
		{WindowBlocks: 2592000, Label: "90d"},
	}
}

// NextStageCandidate 将候选扩展到下一阶段窗口（保持 ToBlock，向前扩展 FromBlock）。
func NextStageCandidate(c Candidate, stages []ProgressiveStage) (Candidate, bool) {
	stage := c.ProgressiveStage
	if stage >= len(stages) {
		return c, false
	}
	next := c
	next.ProgressiveStage = stage + 1
	next.Reason = append([]string{"progressive_stage_" + stages[stage].Label}, c.Reason...)
	if next.ToBlock > stages[stage].WindowBlocks {
		next.FromBlock = next.ToBlock - stages[stage].WindowBlocks
	} else {
		next.FromBlock = 0
	}
	return next, true
}
