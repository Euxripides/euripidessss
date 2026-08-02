package intelligence

import "context"

// ── Executor Pool（V2 设计 §6/§7）──
//
// 统一执行器接口 + 注册表。现有 12 种任务执行器通过闭包适配接入
// （不重写执行逻辑），executeTask 改为查注册表分发。
//
// 执行结果（设计 §7）：
//
//	{status:"SUCCESS", findings:[], evidence:[]} —— 由 Task Result 摘要 + Evidence
//	Extractor 下游消费，此处 Executor 返回结果摘要字符串（保持现有契约）。

// ExecutorResult 是执行器返回的结果（设计 §7 简化：摘要 + 是否跳过）。
type ExecutorResult struct {
	Status   string // SUCCESS / SKIPPED / FAILED
	Summary  string // 结果摘要（写入 Task.Result）
	Findings []string
}

// Executor 是统一任务执行器接口（设计 §7）。
type Executor interface {
	// Type 返回任务类型（Task* 常量）。
	Type() string
	// Execute 执行任务，返回结果摘要。
	Execute(ctx context.Context, a *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, plan *InvestigationPlan, inv *Investigation, st *roundState) (ExecutorResult, error)
	// Validate 前置校验（数据源可用性等）；返回非 nil 表示任务应跳过。
	Validate(a *InvestigationAgent, snap agentSnapshot) error
}

// executorFunc 是 Executor 的闭包适配实现（包装现有包级执行函数）。
type executorFunc struct {
	taskType string
	validate func(a *InvestigationAgent, snap agentSnapshot) error
	execute  func(ctx context.Context, a *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, plan *InvestigationPlan, inv *Investigation, st *roundState) (ExecutorResult, error)
}

// Type 返回任务类型。
func (e *executorFunc) Type() string { return e.taskType }

// Validate 前置校验。
func (e *executorFunc) Validate(a *InvestigationAgent, snap agentSnapshot) error {
	if e.validate != nil {
		return e.validate(a, snap)
	}
	return nil
}

// Execute 执行任务。
func (e *executorFunc) Execute(ctx context.Context, a *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, plan *InvestigationPlan, inv *Investigation, st *roundState) (ExecutorResult, error) {
	return e.execute(ctx, a, snap, task, plan, inv, st)
}

// ExecutorRegistry 是执行器注册表（设计 §6 注册机制）。
type ExecutorRegistry struct {
	executors map[string]Executor
}

// NewExecutorRegistry 创建注册表。
func NewExecutorRegistry() *ExecutorRegistry {
	return &ExecutorRegistry{executors: make(map[string]Executor)}
}

// Register 注册执行器（同类型重复注册以最后为准）。
func (r *ExecutorRegistry) Register(e Executor) {
	if e == nil || e.Type() == "" {
		return
	}
	r.executors[e.Type()] = e
}

// Get 按任务类型查询执行器。
func (r *ExecutorRegistry) Get(taskType string) (Executor, bool) {
	e, ok := r.executors[taskType]
	return e, ok
}

// Types 返回已注册的任务类型列表。
func (r *ExecutorRegistry) Types() []string {
	out := make([]string, 0, len(r.executors))
	for t := range r.executors {
		out = append(out, t)
	}
	return out
}
