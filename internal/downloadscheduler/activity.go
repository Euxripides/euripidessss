package downloadscheduler

// ── Address Activity Score 与 Chunk 智能调整（V3 设计 §7）──
//
// 根据地址活跃度动态决定每任务（chunk）地址数：
//   低活跃：500 addresses/chunk
//   普通：  100 addresses/chunk
//   高活跃：20  addresses/chunk
// 活跃度评分依据：数据集内交易/事件数量（tx 数）、Token/Log 数量与历史失败率
// 由 CoverageSource 提供（当前用交易笔数近似；数据集内交易多 = 活跃地址）。

// ActivityLevel 地址活跃度分档。
type ActivityLevel string

const (
	ActivityLow    ActivityLevel = "low"    // 低活跃：≤1 笔
	ActivityNormal ActivityLevel = "normal" // 普通：2-100 笔
	ActivityHigh   ActivityLevel = "high"   // 高活跃：>100 笔
)

// classifyActivity 按交易笔数分类活跃度。
func classifyActivity(txCount int64) ActivityLevel {
	switch {
	case txCount <= 1:
		return ActivityLow
	case txCount <= 100:
		return ActivityNormal
	default:
		return ActivityHigh
	}
}

// ChunkSizeFor 返回该活跃度档位的 chunk 大小（每任务地址数）。
func ChunkSizeFor(level ActivityLevel) int {
	switch level {
	case ActivityLow:
		return 500 // 低活跃：大批合并
	case ActivityHigh:
		return 20 // 高活跃：小批细跑（防单流过大/失败重跑代价高）
	default:
		return 100 // 普通
	}
}

// activityBucket 记录活跃度分桶与每个地址的档位。
type activityBucket struct {
	level  ActivityLevel
	addrs  []string
	chunks int // 切片后的任务数
}

// bucketByActivity 将地址列表按活跃度分桶（查询失败按普通处理）。
// 返回低/普通/高三桶（仅包含非空桶）。
func bucketByActivity(txCountOf func(addr string) int64, addresses []string) []activityBucket {
	buckets := map[ActivityLevel][]string{
		ActivityLow:    {},
		ActivityNormal: {},
		ActivityHigh:   {},
	}
	for _, addr := range addresses {
		var n int64
		if txCountOf != nil {
			n = txCountOf(addr)
		}
		level := classifyActivity(n)
		buckets[level] = append(buckets[level], addr)
	}
	out := make([]activityBucket, 0, 3)
	for _, level := range []ActivityLevel{ActivityLow, ActivityNormal, ActivityHigh} {
		if len(buckets[level]) == 0 {
			continue
		}
		chunk := ChunkSizeFor(level)
		chunks := (len(buckets[level]) + chunk - 1) / chunk
		out = append(out, activityBucket{level: level, addrs: buckets[level], chunks: chunks})
	}
	return out
}
