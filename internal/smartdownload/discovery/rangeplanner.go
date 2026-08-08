package discovery

// Adaptive Range Planner（设计 §12-§14）：
// 根据活动密度动态决定区块跨度，使单 Range 行数/耗时均衡。

// DefaultRangeTargets 推荐目标。
const (
	DefaultTargetRowsPerRange = 50_000
	MinBlockSpan              = 500
	MaxBlockSpan              = 500_000
	TargetRangeRuntimeMin     = 30
	TargetRangeRuntimeMax     = 180
)

// AdaptiveSpan 按密度返回建议区块跨度（clamp 到 [min,max]）。
func AdaptiveSpan(density float64, targetRowsPerRange uint64, minSpan, maxSpan uint64) uint64 {
	if minSpan == 0 {
		minSpan = MinBlockSpan
	}
	if maxSpan == 0 {
		maxSpan = MaxBlockSpan
	}
	if targetRowsPerRange == 0 {
		targetRowsPerRange = DefaultTargetRowsPerRange
	}
	if density <= 0 {
		return maxSpan
	}
	span := uint64(float64(targetRowsPerRange) / density)
	if span < minSpan {
		span = minSpan
	}
	if span > maxSpan {
		span = maxSpan
	}
	return span
}

// PlanSegments 把分段换算为自适应 Range 列表（覆盖 [from,to]，按段内密度决定跨度）。
func PlanSegments(from, to uint64, segments []ActivitySegment, targetRowsPerRange uint64) []BlockRange {
	if len(segments) == 0 {
		span := AdaptiveSpan(0, targetRowsPerRange, 0, 0)
		return splitSpan(from, to, span)
	}
	SortSegments(segments)
	var out []BlockRange
	for _, seg := range segments {
		lo, hi := seg.FromBlock, seg.ToBlock
		if lo < from {
			lo = from
		}
		if hi > to {
			hi = to
		}
		if hi < lo {
			continue
		}
		span := AdaptiveSpan(seg.Density, targetRowsPerRange, 0, 0)
		out = append(out, splitSpan(lo, hi, span)...)
	}
	if len(out) == 0 {
		return []BlockRange{{From: from, To: to}}
	}
	return out
}

// BlockRange 区块区间（避免与 smartdownload 类型耦合）。
type BlockRange struct {
	From uint64
	To   uint64
}

func splitSpan(from, to, span uint64) []BlockRange {
	var out []BlockRange
	for cur := from; cur <= to && cur+span-1 >= cur; {
		end := cur + span - 1
		if end > to || end < cur {
			end = to
		}
		out = append(out, BlockRange{From: cur, To: end})
		if end == to {
			break
		}
		cur = end + 1
	}
	return out
}
