package investigationstore

// ── Lifecycle：生命周期管理（V1 设计 §13）──
//
// Active：运行中的调查（requests/tasks 未终态）
// History：已完成调查
// 建议上限：active ≤ 5，history ≤ 200，超出部分移入 archive/。
//
// 保证内存与查询稳定；归档记录移入 storeDir/archive/，
// 重启后不再载入（loadAll 跳过 archive/）。

// Lifecycle 是生命周期归档策略。
type Lifecycle struct {
	MaxActive  int // 活跃记录上限（默认 5）
	MaxHistory int // 历史记录上限（默认 200）
}

// DefaultLifecycle 返回默认策略：active ≤ 5，history ≤ 200。
func DefaultLifecycle() Lifecycle {
	return Lifecycle{MaxActive: 5, MaxHistory: 200}
}

// Archive 对存储执行归档：按 key 排序，
// 活跃（非终态）超出 MaxActive 的旧记录、历史（终态）超出 MaxHistory 的旧记录
// 移入 archive/ 子目录。返回归档数量。
// isTerminal 由业务类型提供（如请求状态 finished/failed）。
// 仅内存模式（dir 为空）直接返回 0。
func Archive[T any](store *JSONStore[T], isTerminal func(*T) bool, lc Lifecycle) (int, error) {
	if lc.MaxActive <= 0 {
		lc.MaxActive = 5
	}
	if lc.MaxHistory <= 0 {
		lc.MaxHistory = 200
	}
	keys := store.Keys()
	// key 字典序即创建顺序（ID 自增格式 req-N / ev-N），稳定的旧→新
	sortStrings(keys)

	var active, history []string
	for _, k := range keys {
		v, ok := store.Get(k)
		if !ok {
			continue
		}
		if isTerminal(&v) {
			history = append(history, k)
		} else {
			active = append(active, k)
		}
	}

	archived := 0
	// 历史超出上限：归档最旧（字典序最小）的记录
	excessHistory := len(history) - lc.MaxHistory
	for i := 0; i < excessHistory && i < len(history); i++ {
		if err := store.MoveToArchive(history[i]); err != nil {
			return archived, err
		}
		archived++
	}
	// 活跃超出上限：归档最旧活跃记录
	excessActive := len(active) - lc.MaxActive
	for i := 0; i < excessActive && i < len(active); i++ {
		if err := store.MoveToArchive(active[i]); err != nil {
			return archived, err
		}
		archived++
	}
	return archived, nil
}
