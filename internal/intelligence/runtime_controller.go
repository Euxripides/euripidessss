package intelligence

import (
	"sync"
	"time"
)

// ── Runtime Controller（V2 设计 §3/§4）──
//
// 管理调查生命周期状态流转的轻量控制器（不新建并行执行链，增量改造现有
// LoopEngine：Controller 负责状态机，LoopEngine 负责任务调度与执行）。
//
// 状态机（设计 §4）：
//
//	CREATED → PLANNING → RUNNING → … 执行阶段 … → COMPLETED / FAILED
//	                        │
//	                        └→ WAITING（任务等待依赖）→ RUNNING
//	任意运行态 → STOPPED（用户取消，预留）

// RuntimeState 是运行时控制器状态（与 InvestigationStatus 对齐的轻量子集）。
type RuntimeState string

const (
	RuntimeCreated   RuntimeState = "CREATED"
	RuntimePlanned   RuntimeState = "PLANNED"
	RuntimeRunning   RuntimeState = "RUNNING"
	RuntimeWaiting   RuntimeState = "WAITING"
	RuntimeCompleted RuntimeState = "COMPLETED"
	RuntimeFailed    RuntimeState = "FAILED"
	RuntimeStopped   RuntimeState = "STOPPED"
)

// RuntimeStatus 是运行时状态视图（API 输出，设计 §14）。
type RuntimeStatus struct {
	InvestigationID string       `json:"investigation_id"`
	State           RuntimeState `json:"state"`
	WaitingTasks    int          `json:"waiting_tasks"`   // 等待依赖的任务数
	RunningTasks    int          `json:"running_tasks"`   // 执行中任务数
	CompletedTasks  int          `json:"completed_tasks"` // 已完成任务数
	FailedTasks     int          `json:"failed_tasks"`    // 失败任务数
	TotalTasks      int          `json:"total_tasks"`
	StartedAt       time.Time    `json:"started_at,omitempty"`
	UpdatedAt       time.Time    `json:"updated_at"`
	StopCode        StopCode     `json:"stop_code,omitempty"` // STOPPED 时携带原因
}

// RuntimeController 管理单个调查的生命周期状态。
type RuntimeController struct {
	mu        sync.Mutex
	state     RuntimeState
	startedAt time.Time
	updatedAt time.Time
	stopCode  StopCode
	// 任务统计（由 LoopEngine 每轮刷新）
	waiting   int
	running   int
	completed int
	failed    int
	total     int
}

// NewRuntimeController 创建控制器（初始 CREATED）。
func NewRuntimeController() *RuntimeController {
	now := time.Now().UTC()
	return &RuntimeController{state: RuntimeCreated, updatedAt: now}
}

// SetState 更新状态并记录时间戳。已进入终态后不可回退（STOPPED/COMPLETED/FAILED）。
func (c *RuntimeController) SetState(s RuntimeState, stopCode StopCode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if isRuntimeTerminal(c.state) && c.state != s {
		return // 终态不回退
	}
	if s == RuntimeRunning && c.startedAt.IsZero() {
		c.startedAt = time.Now().UTC()
	}
	c.state = s
	c.updatedAt = time.Now().UTC()
	if stopCode != "" {
		c.stopCode = stopCode
	}
}

// SyncFromStatus 从调查状态同步控制器（启动恢复用，设计 §11）。
func (c *RuntimeController) SyncFromStatus(status InvestigationStatus, stopCode StopCode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = statusToRuntime(status)
	c.updatedAt = time.Now().UTC()
	if stopCode != "" {
		c.stopCode = stopCode
	}
	if c.state == RuntimeRunning && c.startedAt.IsZero() {
		c.startedAt = time.Now().UTC()
	}
}

// RefreshTasks 刷新任务统计（LoopEngine 每轮队列快照后调用）。
func (c *RuntimeController) RefreshTasks(waiting, running, completed, failed, total int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waiting = waiting
	c.running = running
	c.completed = completed
	c.failed = failed
	c.total = total
	c.updatedAt = time.Now().UTC()
}

// Status 返回状态视图（API 输出，防御性拷贝）。
func (c *RuntimeController) Status(investigationID string) RuntimeStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return RuntimeStatus{
		InvestigationID: investigationID,
		State:           c.state,
		WaitingTasks:    c.waiting,
		RunningTasks:    c.running,
		CompletedTasks:  c.completed,
		FailedTasks:     c.failed,
		TotalTasks:      c.total,
		StartedAt:       c.startedAt,
		UpdatedAt:       c.updatedAt,
		StopCode:        c.stopCode,
	}
}

// State 返回当前状态。
func (c *RuntimeController) State() RuntimeState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// statusToRuntime 调查状态 → 运行时状态。
func statusToRuntime(s InvestigationStatus) RuntimeState {
	switch s {
	case InvestigationCreated:
		return RuntimeCreated
	case InvestigationPlanning:
		return RuntimePlanned
	case InvestigationRunning, InvestigationAnalyzing, InvestigationExpanding,
		InvestigationVerifying, InvestigationReporting, InvestigationTracing:
		return RuntimeRunning
	case InvestigationWaiting:
		return RuntimeWaiting
	case InvestigationCompleted:
		return RuntimeCompleted
	case InvestigationFailed:
		return RuntimeFailed
	case InvestigationStopped:
		return RuntimeStopped
	}
	return RuntimeCreated
}

// isRuntimeTerminal 判断运行时状态是否为终态。
func isRuntimeTerminal(s RuntimeState) bool {
	return s == RuntimeCompleted || s == RuntimeFailed || s == RuntimeStopped
}
