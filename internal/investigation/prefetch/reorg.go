package prefetch

// ReorgPolicy 是 Reorg Safety Window（设计 P1：最近区间需留安全窗口再认定）。
type ReorgPolicy struct {
	SafetyBlocks uint64 `json:"safety_blocks"`
}

// SafeToFinalize 返回 toBlock 是否已越过重组织安全窗口。
// headBlock==0 表示未知高度，保守返回 false（不最终化）。
func (p ReorgPolicy) SafeToFinalize(toBlock, headBlock uint64) bool {
	if headBlock == 0 {
		return false
	}
	if toBlock == 0 {
		return true
	}
	return toBlock+p.SafetyBlocks <= headBlock
}

