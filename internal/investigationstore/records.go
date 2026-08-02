package investigationstore

import "time"

// ── Plan / Task Storage（V1 设计 §6/§7）──
//
// 调查计划与任务的持久化记录（与业务类型解耦，迁移 DuckDB 时接口不变）。
// plans/plan-{inv}.json：Agent 规划结果 + 任务列表 + 优先级。
// tasks/{inv}/{task}.json：执行状态/输入/输出/错误。

// PlannedTaskRecord 是计划中的一条任务。
type PlannedTaskRecord struct {
	Type        string `json:"type"`                  // 任务类型（FORWARD_TRACE / FLOW_GRAPH ...）
	Priority    int    `json:"priority"`              // 优先级（越小越优先）
	Description string `json:"description,omitempty"` // 任务描述
}

// PlanRecord 是一次调查计划（V1 设计 §6 示例扩展）。
type PlanRecord struct {
	ID        string              `json:"id"`                   // plan-{inv}
	RequestID string              `json:"request_id,omitempty"` // 关联调查请求
	Target    string              `json:"target,omitempty"`     // 目标地址
	Mode      string              `json:"mode,omitempty"`       // 调查模式
	Tasks     []PlannedTaskRecord `json:"tasks,omitempty"`      // 任务清单（有序）
	CreatedAt time.Time           `json:"created_at"`
}

// TaskRecord 是一条调查任务（V1 设计 §7 示例扩展；Runtime V2 补依赖/重试/超时）。
type TaskRecord struct {
	ID              string    `json:"id"`                         // 任务 ID
	InvestigationID string    `json:"investigation_id,omitempty"` // 所属调查
	Type            string    `json:"type"`                       // 任务类型
	Status          string    `json:"status"`                     // pending/running/done/skipped/failed
	Input           string    `json:"input,omitempty"`            // 输入摘要
	Output          string    `json:"output,omitempty"`           // 输出摘要
	ResultRef       string    `json:"result_ref,omitempty"`       // 结果引用（文件/路径）
	Error           string    `json:"error,omitempty"`            // 错误信息
	Priority        int       `json:"priority,omitempty"`         // 优先级
	Round           int       `json:"round,omitempty"`            // 所属轮次
	UpdatedAt       time.Time `json:"updated_at"`

	// ── Runtime V2（设计 §5/§11）──
	Dependencies []string `json:"dependencies,omitempty"` // 依赖任务 ID
	MaxRetries   int      `json:"max_retries,omitempty"`  // 失败最大重试次数
	RetryCount   int      `json:"retry_count,omitempty"`  // 已重试次数
	TimeoutSec   int      `json:"timeout_sec,omitempty"`  // 执行超时秒数
	StartedAt    int64    `json:"started_at,omitempty"`   // 最近开始执行时间戳（heartbeat）
}

// PlanStore 是调查计划存储（JSON 文件，原子写）。
type PlanStore = JSONStore[PlanRecord]

// NewPlanStore 创建计划存储。dir 为空则仅内存。
func NewPlanStore(dir string) *PlanStore {
	return NewJSONStore[PlanRecord](dir)
}

// TaskStore 是调查任务存储（JSON 文件，原子写）。
type TaskStore = JSONStore[TaskRecord]

// NewTaskStore 创建任务存储。dir 为空则仅内存。
func NewTaskStore(dir string) *TaskStore {
	return NewJSONStore[TaskRecord](dir)
}
