package intelligence

import (
	"sync"
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

// Next 返回下一个 pending 任务（按优先级升序，同优先级先入先出）。
func (q *TaskQueue) Next() *InvestigationTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	var best *InvestigationTask
	for _, t := range q.tasks {
		if t.Status != TaskPending {
			continue
		}
		if best == nil || t.Priority < best.Priority {
			best = t
		}
	}
	return best
}

// Mark 更新任务状态/结果/错误。
// 状态流转守卫：仅允许从 ""/pending/running 流转（终态 done/skipped/failed 不可再变），
// 防止已完成任务被重新执行。返回更新后的任务副本（未变更时返回当前副本）。
func (q *TaskQueue) Mark(id, status, result, errMsg string) *InvestigationTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range q.tasks {
		if t.ID != id {
			continue
		}
		switch t.Status {
		case TaskDone, TaskSkipped, TaskFailed:
			if status != t.Status {
				cp := *t
				return &cp // 终态不可流转
			}
		}
		t.Status = status
		t.Result = result
		t.Error = errMsg
		cp := *t
		return &cp
	}
	return nil
}

// Get 按 ID 返回任务副本。
func (q *TaskQueue) Get(id string) (*InvestigationTask, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range q.tasks {
		if t.ID == id {
			cp := *t
			return &cp, true
		}
	}
	return nil, false
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
