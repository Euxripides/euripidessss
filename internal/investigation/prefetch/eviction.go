package prefetch

import (
	"math"
	"time"
)

// EvictionScore 返回缓存驱逐分（设计 §57：越高越优先清理）。
func EvictionScore(ageHours, unusedPenalty, sizeMB, importance, reuseProbability float64) float64 {
	score := ageHours/24 +
		unusedPenalty +
		math.Min(sizeMB/1024, 2.0) -
		math.Min(importance, 2.0) -
		math.Min(reuseProbability, 1.0)
	if score < 0 {
		return 0
	}
	return score
}

// Action 根据磁盘使用率返回策略动作（设计 §58）。
func (p DiskPolicy) Action(usedPct float64) DiskAction {
	switch {
	case usedPct >= p.BlockNewAt:
		return DiskBlockNew
	case usedPct >= p.PauseAllAt:
		return DiskPauseAll
	case usedPct >= p.PauseWarmAt:
		return DiskPauseWarm
	default:
		return DiskNone
	}
}

// UnusedTTL 是预取数据被判为无价值的等待时长（设计 §34：7 天）。
const UnusedTTL = 7 * 24 * time.Hour

