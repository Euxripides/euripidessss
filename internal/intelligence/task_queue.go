package intelligence

import (
	"sync"
	"time"
)

// ── Task Queue（设计 §7）──
//
// 调查任务队列：7 种任务类型（ADDRESS_PROFILE / FLOW_ANALYSIS / PATH_TRACE /
// ENTITY_CHECK / RISK_SCAN / EXPAND_ADDRESS / GENERATE_REPORT），
// 按优先级顺序执行，状态可追踪（pending/running/done/skipped/failed）。

// TaskQueue 是调查任务队列。每个调查闭环独立一个队列。
type TaskQueue struct {
	mu    sync.Mutex
	tasks []*InvestigationTask
	seq   int // 任务序号（ID 生成）
}

// NewTaskQueue 创建空任务队列。
func NewTaskQueue() *TaskQueue {
	return &TaskQueue{}
}

// TotalCount 返回累计入队任务数（含已执行，V2.1 预算检查用）。
func (q *TaskQueue) TotalCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.seq
}

// Enqueue 加入任务。同轮次内同 type+target 幂等去重（返回已存在任务）。
// round 由调用方传入；ID 自动生成（r<round>-<type>-<seq>）。
func (q *TaskQueue) Enqueue(task InvestigationTask) *InvestigationTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range q.tasks {
		if t.Round == task.Round && t.Type == task.Type && t.Target == task.Target {
			return t
		}
	}
	q.seq++
	if task.ID == "" {
		task.ID = "t" + itoa(q.seq)
	}
	if task.Status == "" {
		task.Status = TaskPending
	}
	cp := task // 值拷贝，后续经 Mark 更新
	q.tasks = append(q.tasks, &cp)
	return &cp
}

// Next 返回下一个可执行任务（按优先级升序，同优先级先入先出）。
// Runtime V2（设计 §5）：依赖门控——存在未完成依赖的任务不返回，
// 等待中的依赖任务统计计入 PendingCount。
func (q *TaskQueue) Next() *InvestigationTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	var best *InvestigationTask
	for _, t := range q.tasks {
		if t.Status != TaskPending {
			continue
		}
		if !q.depsSatisfiedLocked(t) {
			continue // 依赖未全部完成，等待
		}
		if best == nil || t.Priority < best.Priority {
			best = t
		}
	}
	return best
}

// depsSatisfiedLocked 判断任务依赖是否全部完成，必须在持锁状态调用。
// 依赖 failed/skipped（终态非 done）视为不满足（依赖方不会执行）。
func (q *TaskQueue) depsSatisfiedLocked(t *InvestigationTask) bool {
	for _, depID := range t.Dependencies {
		dep, ok := q.findLocked(depID)
		if !ok {
			return false
		}
		if dep.Status != TaskDone {
			// 依赖已终态但失败/跳过：依赖方永久阻塞（由调用方经 BlockedByFailedDep 标记 skipped）
			return false
		}
	}
	return true
}

// BlockedByFailedDep 判断任务是否被失败/跳过的依赖永久阻塞（设计 §5：依赖失败不执行下游）。
func (q *TaskQueue) BlockedByFailedDep(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.findLocked(id)
	if !ok || t.Status != TaskPending {
		return false
	}
	for _, depID := range t.Dependencies {
		dep, ok := q.findLocked(depID)
		if !ok {
			continue
		}
		if dep.Status == TaskFailed || dep.Status == TaskSkipped {
			return true
		}
	}
	return false
}

// findLocked 按 ID 查找任务，必须在持锁状态调用。
func (q *TaskQueue) findLocked(id string) (*InvestigationTask, bool) {
	for _, t := range q.tasks {
		if t.ID == id {
			return t, true
		}
	}
	return nil, false
}

// Mark 更新任务状态/结果/错误。
// 状态流转守卫：仅允许从 ""/pending/running 流转（终态 done/skipped/failed 不可再变），
// 防止已完成任务被重新执行。返回更新后的任务副本（未变更时返回当前副本）。
// Runtime V2（设计 §5）：
// - failed 且 RetryCount < MaxRetries 时自动回到 pending（重试计数 +1）；
// - running 时记录 StartedAt（heartbeat 超时判断用）。
func (q *TaskQueue) Mark(id, status, result, errMsg string) *InvestigationTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.findLocked(id)
	if !ok {
		return nil
	}
	switch t.Status {
	case TaskDone, TaskSkipped, TaskFailed:
		if status != t.Status {
			cp := *t
			return &cp // 终态不可流转
		}
	}
	if status == TaskRunning {
		t.StartedAt = time.Now().Unix()
	}
	t.Status = status
	t.Result = result
	t.Error = errMsg
	// 失败重试：未达上限回到 pending（仅当任务配置了 MaxRetries）
	if status == TaskFailed && t.MaxRetries > 0 && t.RetryCount < t.MaxRetries {
		t.RetryCount++
		t.Status = TaskPending
		t.Error = "" // 重试等待重新执行，旧错误暂存于 Result 历史之外（保留在 Error 字段由调用方覆盖）
	}
	cp := *t
	return &cp
}

// IsExpired 判断运行中任务是否超时（heartbeat 用，设计 §11）。
// timeoutSec <= 0 表示不超时；超过 timeoutSec 秒且仍为 running 视为过期。
func (q *TaskQueue) IsExpired(id string, timeoutSec int, now int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.findLocked(id)
	if !ok || t.Status != TaskRunning || timeoutSec <= 0 {
		return false
	}
	return now-t.StartedAt > int64(timeoutSec)
}

// Get 按 ID 返回任务副本。
func (q *TaskQueue) Get(id string) (*InvestigationTask, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.findLocked(id)
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

// Snapshot 返回全部任务视图副本（锁内深拷贝，JSON 安全）。
func (q *TaskQueue) Snapshot() []InvestigationTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]InvestigationTask, 0, len(q.tasks))
	for _, t := range q.tasks {
		cp := *t
		out = append(out, cp)
	}
	return out
}

// PendingCount 返回未完成任务数。
func (q *TaskQueue) PendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, t := range q.tasks {
		if t.Status == TaskPending || t.Status == TaskRunning {
			n++
		}
	}
	return n
}

// itoa 简易整数转字符串（避免引入 strconv 以外的依赖，队列序号很小）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
