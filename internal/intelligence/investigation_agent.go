package intelligence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/dynamicinvestigation"
	"github.com/etl/backend/internal/investigationstore"
)

// ── Investigation Agent ──
//
// 全自动调查主流程：
// 输入地址 → 地址画像 → 行为分析 → 资金流发现 → 路径评分 → 地址扩展 →
// 实体识别 → 风险检测 → DeepSeek 分析 → 生成报告。

// InvestigationAgent 编排完整调查流程。
type InvestigationAgent struct {
	svc            *analyticsapi.Service
	flowSource     FlowSource
	tracer         *FundTracer
	ranker         *PathRanker
	planner        *Planner
	detector       *PatternDetector
	entityResolver *EntityResolver
	expansion      Expander
	contextBuilder *AIContextBuilder
	deepseek       *DeepSeekClient
	deepseekKey    string // 注入的 API Key（rebuild 时保留）
	report         *ReportAgent
	memories       *MemoryStore
	ai             *AIAgent                              // AI 驱动调查（§3，可为 nil）
	aiChatter      AIChatter                             // AI 对话实现（NewAgent 默认 deepseek；测试可注入 fake）
	aiMemory       *AIMemoryStore                        // AI 记忆共享实例（rebuild 复用，防并发丢记忆）
	aiMemoryDir    string                                // AI 记忆持久化目录
	requests       *RequestStore                         // V2：调查请求存储（终态同步请求状态，可为 nil）
	evidence       *EvidenceStore                        // V2.1：调查证据存储（Evidence Layer，可为 nil）
	knowledge      *InvestigationMemoryStore             // V2.1：跨案件知识记忆（可为 nil）
	plans          *investigationstore.PlanStore         // V1：调查计划存储（可为 nil）
	tasks          *investigationstore.TaskStore         // V1：调查任务存储（可为 nil）
	profile        *investigationstore.ScoreProfileStore // V1：评分权重配置（可为 nil）
	eventLog       *RuntimeEventLog                      // Runtime V2：运行时事件日志（可为 nil）
	cfg            IntelligenceConfig

	mu      sync.Mutex
	active  map[string]*Investigation // id → 进行中的调查
	history map[string]*Investigation // id → 已完成的调查
	nextID  int
	// controllers 是每个调查的运行时控制器（Runtime V2 设计 §4，随调查创建/退役）
	controllers map[string]*RuntimeController
	// resuming 标记正在恢复执行的调查（防 /runtime/start 重复触发并发 resumeRun）
	resuming map[string]bool
	// subscribers 是 SSE 订阅者（id → channel 集合，#7 优化）
	subscribers map[string]map[chan *Investigation]struct{}
}

// AgentOptions 是代理依赖注入选项。
type AgentOptions struct {
	Service         *analyticsapi.Service
	FlowSource      FlowSource
	Expansion       *ExpansionEngine
	Recognizer      interface{} // *dynamicinvestigation.Recognizer（可选）
	DeepSeekKey     string
	MemoryDir       string
	RequestStore    *RequestStore                         // V2：调查请求存储（终态同步请求状态，可为 nil）
	EvidenceStore   *EvidenceStore                        // V2.1：调查证据存储（Evidence Layer，可为 nil）
	KnowledgeMemory *InvestigationMemoryStore             // V2.1：跨案件知识记忆（可为 nil）
	Plans           *investigationstore.PlanStore         // V1：调查计划存储（可为 nil）
	Tasks           *investigationstore.TaskStore         // V1：调查任务存储（可为 nil）
	Profile         *investigationstore.ScoreProfileStore // V1：评分权重配置（可为 nil）
	EventLog        *RuntimeEventLog                      // Runtime V2：运行时事件日志（可为 nil）
	Config          IntelligenceConfig
}

// NewAgent 创建调查代理。
func NewAgent(opts AgentOptions) *InvestigationAgent {
	cfg := opts.Config
	if cfg.MaxHops == 0 {
		cfg = DefaultConfig()
	}
	flowSource := opts.FlowSource
	if flowSource == nil && opts.Service != nil {
		flowSource = NewAnalyticsFlowSource(opts.Service)
	}
	ranker := DefaultPathRanker()
	agent := &InvestigationAgent{
		svc:            opts.Service,
		flowSource:     flowSource,
		ranker:         ranker,
		tracer:         NewFundTracer(flowSource, ranker, cfg),
		planner:        NewPlanner(cfg),
		detector:       NewPatternDetector(cfg),
		report:         NewReportAgent(cfg),
		contextBuilder: NewAIContextBuilder(cfg),
		deepseek:       NewDeepSeekClient(opts.DeepSeekKey, cfg.AIModel, cfg.AITimeoutMS, cfg.MaxTokens),
		deepseekKey:    opts.DeepSeekKey,
		requests:       opts.RequestStore,    // V2：请求终态同步（可为 nil）
		evidence:       opts.EvidenceStore,   // V2.1：证据存储（可为 nil）
		knowledge:      opts.KnowledgeMemory, // V2.1：跨案件知识记忆（可为 nil）
		plans:          opts.Plans,           // V1：计划存储（可为 nil）
		tasks:          opts.Tasks,           // V1：任务存储（可为 nil）
		profile:        opts.Profile,         // V1：评分权重（可为 nil）
		eventLog:       opts.EventLog,        // Runtime V2：事件日志（可为 nil）
		cfg:            cfg,
		active:         make(map[string]*Investigation),
		history:        make(map[string]*Investigation),
		controllers:    make(map[string]*RuntimeController),
		resuming:       make(map[string]bool),
	}
	// 实体解析器：复用 dynamicinvestigation 识别能力 + analyticsapi 画像信号
	var entitySource dynamicinvestigation.DiscoverySource
	if opts.Service != nil {
		entitySource = dynamicinvestigation.NewAnalyticsSource(opts.Service)
	}
	agent.entityResolver = NewEntityResolver(nil, &entitySource)
	agent.expansion = opts.Expansion
	agent.memories = NewMemoryStore(opts.MemoryDir)
	agent.aiMemoryDir = aiMemoryDir(opts.MemoryDir)
	agent.aiMemory = NewAIMemoryStore(agent.aiMemoryDir) // 共享实例：并发调查/rebuild 复用，防 last-writer-wins 丢记忆
	agent.aiChatter = agent.deepseek                     // 默认使用 DeepSeek；测试可注入 fake
	agent.ai = NewAIAgentWithStore(agent.aiChatter, flowSource, cfg, agent.aiMemory)
	return agent
}

// GenerateReport 生成调查报告（线程安全：锁内快照 report 引用）。
func (a *InvestigationAgent) GenerateReport(inv *Investigation, format ReportFormat) (*ReportOutput, error) {
	a.mu.Lock()
	report := a.report
	a.mu.Unlock()
	if report == nil {
		return nil, fmt.Errorf("报告代理未初始化")
	}
	return report.Generate(inv, format)
}

// Config 返回代理配置（线程安全副本）。
func (a *InvestigationAgent) Config() IntelligenceConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

// UpdateConfig 更新代理配置（线程安全，避免与 run goroutine 的读竞争）。
func (a *InvestigationAgent) UpdateConfig(cfg IntelligenceConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg = cfg
}

// rebuildSubcomponents 用当前配置重建子组件（tracer/planner/detector/report/deepseek/ai）。
// 使 POST /config 的更新对后续调查生效。必须在持锁状态下调用（重建写与快照读一致）。
func (a *InvestigationAgent) rebuildSubcomponentsLocked() {
	cfg := a.cfg
	a.ranker = DefaultPathRanker()
	a.tracer = NewFundTracer(a.flowSource, a.ranker, cfg)
	a.planner = NewPlanner(cfg)
	a.detector = NewPatternDetector(cfg)
	a.report = NewReportAgent(cfg)
	a.contextBuilder = NewAIContextBuilder(cfg)
	// 复用 DeepSeek 客户端实例（#10 优化：用量统计跨调查累计），仅更新配置字段
	if a.deepseek == nil {
		a.deepseek = NewDeepSeekClient(a.deepseekKey, cfg.AIModel, cfg.AITimeoutMS, cfg.MaxTokens)
	} else {
		a.deepseek.ApplyConfig(cfg.AIModel, cfg.AITimeoutMS, cfg.MaxTokens)
	}
	a.ai = NewAIAgentWithStore(a.aiChatter, a.flowSource, cfg, a.aiMemory)
}

// Start 启动一次异步调查（立即返回调查对象，后台执行）。
// cfgOverride 可选：仅本调查生效的配置覆盖（不修改全局配置，不影响其他调查）。
func (a *InvestigationAgent) Start(ctx context.Context, target, chainID string, cfgOverride ...*IntelligenceConfig) (*Investigation, error) {
	return a.StartWithRequest(ctx, target, chainID, nil, cfgOverride...)
}

// StartWithRequest 以调查请求启动一次异步调查（V2：携带目的/期望结果/模式）。
// req 可为 nil（等价于 Start）。
func (a *InvestigationAgent) StartWithRequest(ctx context.Context, target, chainID string, req *InvestigationRequest, cfgOverride ...*IntelligenceConfig) (*Investigation, error) {
	// 用最新配置重建子组件：POST /config 更新（如 max_hops/beam_width/min_amount/ai_model）
	// 对后续调查生效。重建与快照在同一锁内完成，避免并发 Start/GenerateReport 竞争。
	a.mu.Lock()
	a.rebuildSubcomponentsLocked()
	snapshot := agentSnapshot{
		tracer:         a.tracer,
		ranker:         a.ranker,
		planner:        a.planner,
		detector:       a.detector,
		report:         a.report,
		contextBuilder: a.contextBuilder,
		deepseek:       a.deepseek,
		flowSource:     a.flowSource,
		expansion:      a.expansion,
		ai:             a.ai,
	}
	a.mu.Unlock()
	target = strings.ToLower(strings.TrimSpace(target))
	if !validEVMAddress(target) {
		return nil, fmt.Errorf("目标地址不是合法的 EVM 地址")
	}
	if chainID == "" {
		chainID = "bsc"
	}
	a.mu.Lock()
	// MEDIUM-2：进行中调查并发上限锁内检查（消除 check-then-act 竞态，权威校验）
	if a.activeCountLocked() >= maxActiveInvestigations {
		a.mu.Unlock()
		return nil, ErrTooManyActive
	}
	a.nextID++
	id := fmt.Sprintf("inv-%d", a.nextID)
	now := time.Now().UTC()
	inv := &Investigation{
		ID:        id,
		Target:    target,
		ChainID:   chainID,
		Status:    InvestigationCreated,
		CreatedAt: now,
		UpdatedAt: now,
		Request:   req, // V2 调查请求（目的/期望结果/模式）
	}
	// 锁内回填请求关联字段：req 是调用方传入的请求（RequestStore 深拷贝副本），
	// 回填先于 inv 放入 active，任何并发读（Get/List）必然看到完整关联；
	// store 侧由 handler 在启动后经 Link 双写同步。
	if req != nil {
		req.InvestigationID = id
		req.Status = RequestStarted
		req.UpdatedAt = now
	}
	if len(cfgOverride) > 0 && cfgOverride[0] != nil {
		c := *cfgOverride[0]
		inv.cfgOverride = &c // 仅本调查生效，不污染全局配置
	}
	a.active[id] = inv
	a.memories.New(id, target)
	// Runtime V2：启动即创建控制器并同步初始状态（异步 run 前，供 /runtime/status 立即可读）
	if a.controllers == nil {
		a.controllers = make(map[string]*RuntimeController)
	}
	c, ok := a.controllers[id]
	if !ok {
		c = NewRuntimeController()
		a.controllers[id] = c
	}
	c.SyncFromStatus(inv.Status, "")
	a.mu.Unlock()

	// 关键：后台调查必须使用独立 context（不继承请求 ctx），
	// 否则 POST /start 返回后请求 ctx 被取消，DuckDB 查询全部失败。
	runCtx := context.Background()
	go a.run(runCtx, inv, snapshot)
	// 返回防御性副本：run 的 setStage 可能正在锁内写 inv（HTTP json 编码时避免竞争）
	a.mu.Lock()
	defer a.mu.Unlock()
	copy := *inv
	return &copy, nil
}

// ErrTooManyActive 是进行中调查并发超限错误（MEDIUM-2：create 返回 429）。
var ErrTooManyActive = errors.New("进行中的调查过多")

// maxActiveInvestigations 是进行中调查并发上限（security review MEDIUM-2：
// 无认证端点防异步负载洪泛；超限时 create 返回 429）。
const maxActiveInvestigations = 5

// maxHistoryLength 是已完成调查 history 保留上限（防内存无界增长）。
const maxHistoryLength = 200

// ActiveCount 返回进行中（非终态）调查数。
func (a *InvestigationAgent) ActiveCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.activeCountLocked()
}

// activeCountLocked 统计进行中调查数，必须在持锁状态下调用。
func (a *InvestigationAgent) activeCountLocked() int {
	n := 0
	for _, inv := range a.active {
		if inv.Status != InvestigationCompleted && inv.Status != InvestigationFailed {
			n++
		}
	}
	return n
}

// retireLocked 将调查移入 history 并从 active 移除（终态清理，防 map 无界增长），
// history 超过 maxHistoryLength 时循环淘汰最旧记录。必须在持锁状态下调用。
func (a *InvestigationAgent) retireLocked(inv *Investigation) {
	a.history[inv.ID] = inv
	delete(a.active, inv.ID)
	for len(a.history) > maxHistoryLength {
		// 淘汰最旧（按 UpdatedAt）
		oldestID := ""
		var oldest time.Time
		for id, h := range a.history {
			if oldestID == "" || h.UpdatedAt.Before(oldest) {
				oldestID = id
				oldest = h.UpdatedAt
			}
		}
		if oldestID == "" {
			break
		}
		delete(a.history, oldestID)
	}
}

// Get 查询调查（进行中或已完成）。
func (a *InvestigationAgent) Get(id string) (*Investigation, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if inv, ok := a.active[id]; ok {
		copy := *inv
		return &copy, true
	}
	if inv, ok := a.history[id]; ok {
		copy := *inv
		return &copy, true
	}
	return nil, false
}

// List 列出全部调查（去重：完成的调查仅从 history 返回一次）。
func (a *InvestigationAgent) List() []Investigation {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Investigation, 0, len(a.active)+len(a.history))
	seen := map[string]bool{}
	// 进行中的优先
	for _, inv := range a.active {
		if !seen[inv.ID] {
			out = append(out, *inv)
			seen[inv.ID] = true
		}
	}
	for _, inv := range a.history {
		if !seen[inv.ID] {
			out = append(out, *inv)
			seen[inv.ID] = true
		}
	}
	return out
}

// agentSnapshot 是调查启动时锁内快照的子组件引用（避免并发 rebuild 写竞争）。
type agentSnapshot struct {
	tracer         *FundTracer
	ranker         *PathRanker
	planner        *Planner
	detector       *PatternDetector
	report         *ReportAgent
	contextBuilder *AIContextBuilder
	deepseek       *DeepSeekClient
	flowSource     FlowSource
	expansion      Expander
	ai             *AIAgent
}

// Resume 从持久化状态恢复执行（Runtime V2 设计 §11）：
// 读取 TaskStore 中该调查的 RUNNING 任务（heartbeat 超时标记 failed），
// 将未完成任务重新入队并运行一轮 LoopEngine 恢复执行。
// 返回恢复的任务数量；调查不存在、已终态或正在运行中返回错误。
func (a *InvestigationAgent) Resume(invID string) (int, error) {
	inv, ok := a.Get(invID)
	if !ok {
		return 0, fmt.Errorf("调查不存在: %s", invID)
	}
	if TerminalStatuses[inv.Status] {
		return 0, fmt.Errorf("调查已结束: %s", inv.Status)
	}
	// 安全（锁内原子检查+置位）：仅当调查处于 CREATED/WAITING（无主循环执行中）
	// 才允许恢复。PLANNED 及之后（RUNNING/ANALYZING/...）一律拒绝——主循环一旦进入
	// 规划即持有执行权，恢复会导致双队列并发执行同一批任务（setField 互相覆盖、
	// persistTasks 并发写、AI 双倍调用）；终态也拒绝（锁内权威重查，消除锁外
	// Get 副本的 TOCTOU 窗口）。state 检查与 resuming 置位在同一锁内，
	// 与 setStage 的锁内 SetState 串行化。
	a.mu.Lock()
	if a.resuming == nil {
		a.resuming = make(map[string]bool)
	}
	state := RuntimeCreated
	if c, ok := a.controllers[invID]; ok {
		state = c.State()
	}
	switch state {
	case RuntimeCreated, RuntimeWaiting:
		// 放行：主循环未持有执行权
	default:
		// PLANNED / RUNNING / 终态：主循环持有执行权或已结束，拒绝恢复
		a.mu.Unlock()
		return 0, fmt.Errorf("调查不可恢复（状态 %s）: %s", state, invID)
	}
	if a.resuming[invID] {
		a.mu.Unlock()
		return 0, fmt.Errorf("调查正在恢复执行中: %s", invID)
	}
	a.resuming[invID] = true
	a.mu.Unlock()
	recovered := a.RecoverTasks(invID)
	if len(recovered) == 0 {
		// 无持久化任务可恢复：清除标记（无 resumeRun 执行）
		a.mu.Lock()
		delete(a.resuming, invID)
		a.mu.Unlock()
		// 安全（MEDIUM 修复）：若主循环已被让位中止（调查处于 WAITING，
		// 且恢复路径无任务可执行），统一收尾防悬挂
		if a.Controller(invID).State() == RuntimeWaiting {
			a.finishInvestigation(invID, a.snapshot())
		}
		return 0, nil
	}
	// 后台恢复执行（复用现有闭环；规划已存在则直接执行未完成任务）。
	// 注意：不在此显式置 RUNNING——执行权归 resumeRun，状态由 resumeRun 内
	// setField/终态收尾管理（避免"显示 RUNNING 但执行权在恢复协程"的语义混淆）。
	// resuming 标记由 resumeRun 完成后清除（保证 setStage 让位检查覆盖整个恢复期）
	snap := a.snapshot()
	go a.resumeRun(invID, recovered, snap)
	return len(recovered), nil
}

// resumeRun 后台执行恢复的任务（复用 LoopEngine 单轮执行语义）。
func (a *InvestigationAgent) resumeRun(invID string, recovered []InvestigationTask, snap agentSnapshot) {
	// 统一清理 resuming 标记（所有退出路径）
	defer func() {
		a.mu.Lock()
		delete(a.resuming, invID)
		a.mu.Unlock()
	}()
	// 安全（MEDIUM 修复）：入口锁内复核——若主循环已接管（controller 离开
	// CREATED/WAITING，如 Start 后立即并发 runtime/start 的微秒窗口），放弃恢复，
	// 防止 resumeRun 与主 LoopEngine 双队列并发执行同一批任务
	a.mu.Lock()
	state := RuntimeCreated
	if c, ok := a.controllers[invID]; ok {
		state = c.State()
	}
	if state != RuntimeCreated && state != RuntimeWaiting {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	inv, ok := a.Get(invID)
	if !ok {
		return
	}
	queue := NewTaskQueue()
	for _, t := range recovered {
		// 仅恢复可执行任务：pending（未完成）或超时失败且配置了重试且未达上限
		// MaxRetries=0 语义为"不重试"：failed 任务不恢复执行（防无限执行）
		if t.Status == TaskPending {
			queue.Enqueue(t)
			continue
		}
		if t.Status == TaskFailed && t.MaxRetries > 0 && t.RetryCount < t.MaxRetries {
			t.Status = TaskPending // 超时失败任务重新执行（重试计数保留）
			queue.Enqueue(t)
		}
	}
	if queue.PendingCount() == 0 {
		// 安全（MEDIUM 修复）：无任务可执行（全部终态不可重试）也统一收尾，
		// 防调查悬挂 WAITING
		a.finishInvestigation(invID, snap)
		return
	}
	cfg := a.Config()
	if inv.cfgOverride != nil {
		cfg = *inv.cfgOverride
	}
	plan := inv.Plan
	if plan == nil {
		plan = snap.planner.Plan(a.planInput(context.Background(), inv.Target))
	}
	e := NewLoopEngine()
	st := &roundState{focus: []string{inv.Target}, flowsByAddr: map[string][]FundEdge{}}
	for {
		t := queue.Next()
		if t == nil {
			// 依赖失败/跳过的等待任务标记 skipped（与主循环一致，防恢复后悬挂）
			blocked := queue.Snapshot()
			changed := false
			for i := range blocked {
				if blocked[i].Status == TaskPending && queue.BlockedByFailedDep(blocked[i].ID) {
					queue.Mark(blocked[i].ID, TaskSkipped, "依赖失败，任务跳过", "")
					changed = true
				}
			}
			if changed {
				a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
				a.persistTasks(inv.ID, queue.Snapshot())
				continue
			}
			break
		}
		a.eventLog.TaskCreated(inv.ID, t)
		queue.Mark(t.ID, TaskRunning, "", "")
		a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
		a.persistTasks(inv.ID, queue.Snapshot())
		result, err := e.executeTask(context.Background(), a, snap, t, plan, inv, st, cfg)
		if err != nil {
			var skip *skipError
			if errors.As(err, &skip) {
				queue.Mark(t.ID, TaskSkipped, skip.reason, "")
			} else {
				markRes := queue.Mark(t.ID, TaskFailed, "", err.Error())
				if markRes != nil && markRes.Status == TaskPending {
					a.eventLog.TaskRetried(inv.ID, markRes, markRes.RetryCount)
				} else if markRes != nil {
					a.eventLog.TaskFailed(inv.ID, markRes, err.Error())
				}
			}
			a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
			a.persistTasks(inv.ID, queue.Snapshot())
			continue
		}
		queue.Mark(t.ID, TaskDone, result, "")
		a.eventLog.TaskExecuted(inv.ID, t, result)
		a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
		a.persistTasks(inv.ID, queue.Snapshot())
	}
	// 全部任务完成后同步任务列表并终态收尾（防调查悬挂 RUNNING/WAITING）
	a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
	a.persistTasks(inv.ID, queue.Snapshot())
	a.finishInvestigation(invID, snap)
}

// finishInvestigation 统一终态收尾（恢复执行完成 / 无任务可恢复时调用，
// 防调查悬挂 WAITING/RUNNING）：COMPLETED + retireLocked + 内存 Request 深拷贝
// 同步 + store 双写。与主 run 收尾语义一致。snap 为调用方持有的子组件快照
// （AI 固化用其 ai 指针，避免锁外读 a.ai 与 rebuild 写竞争）。
func (a *InvestigationAgent) finishInvestigation(invID string, snap agentSnapshot) {
	cur, ok := a.Get(invID)
	if !ok {
		return
	}
	a.mu.Lock()
	// 锁内权威终态检查：读 active/history 当前状态（非锁外快照），防并发重复收尾
	authoritative := cur
	if activeInv, ok := a.active[invID]; ok {
		authoritative = activeInv
	} else if histInv, ok := a.history[invID]; ok {
		authoritative = histInv
	}
	if TerminalStatuses[authoritative.Status] {
		a.mu.Unlock()
		return // 已终态（含历史归档），无需重复收尾
	}
	// SetState 在锁内检查通过之后执行（顺序语义：先验证可收尾，再改 controller 状态）。
	// 直接访问 controllers map（不调 Controller()——其内部获取 a.mu 会死锁）；
	// controller 有独立锁，锁序 a.mu → controller.mu 与 setStage 一致，无死锁。
	if c, ok := a.controllers[invID]; ok {
		c.SetState(RuntimeCompleted, authoritative.StopCode)
	}
	authoritative.Status = InvestigationCompleted
	authoritative.Progress = 100
	authoritative.UpdatedAt = time.Now().UTC()
	// 同步内存 Request 终态（与主 run 路径一致：锁内替换深拷贝指针）
	if authoritative.Request != nil {
		cloned := cloneRequest(authoritative.Request)
		cloned.Status = RequestFinished
		cloned.UpdatedAt = time.Now().UTC()
		authoritative.Request = cloned
	}
	a.active[invID] = authoritative
	a.retireLocked(authoritative)
	a.notifyLocked(authoritative)
	a.mu.Unlock()
	// 同步请求终态到 store（双写，与主 run 收尾一致）
	if authoritative.Request != nil {
		reqID := authoritative.Request.ID
		if a.requests != nil {
			_ = a.requests.Link(reqID, invID, RequestFinished)
		}
	}
	// 记忆/知识收尾（与主 run 收尾一致；恢复完成的调查同样持久化调查记忆
	// 与跨案件知识——主循环让位后不再执行这些收尾，须在此补齐）
	if a.memories != nil {
		_ = a.memories.Save(invID)
	}
	if a.knowledge != nil && authoritative != nil {
		a.knowledge.recordKnowledge(authoritative)
	}
	// AI 记忆固化（与主 run 收尾一致，设计 §13）。
	// 使用调用方快照中的 ai 指针（锁内快照，避免与 rebuildSubcomponentsLocked
	// 的持锁写 a.ai 构成 data race）。
	if snapAI := snap.ai; snapAI != nil {
		snapAI.Remember(authoritative)
		snapAI.SaveMemory()
	}
}

// snapshot 返回子组件快照（供恢复执行使用）。
func (a *InvestigationAgent) snapshot() agentSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return agentSnapshot{
		tracer:         a.tracer,
		ranker:         a.ranker,
		planner:        a.planner,
		detector:       a.detector,
		report:         a.report,
		contextBuilder: a.contextBuilder,
		deepseek:       a.deepseek,
		flowSource:     a.flowSource,
		expansion:      a.expansion,
		ai:             a.ai,
	}
}

// run 执行调查闭环（规划 → 执行 → 观察 → 判断 → 重新规划，直到完成条件）。
// 注意：inv 字段写入全部经 setField 锁内进行，避免与 Get/List 的锁内读竞争；
// 子组件使用启动时快照（snapshot），避免与其他调查的 rebuild 写竞争。
func (a *InvestigationAgent) run(ctx context.Context, inv *Investigation, snap agentSnapshot) {
	if err := NewLoopEngine().Run(ctx, a, inv, snap); err != nil {
		a.fail(inv, err)
		return
	}
	// Runtime V2 安全：主循环被让位中止（调查处于 WAITING，resumeRun 持有执行权）或
	// 已终态（resumeRun 抢先完成收尾）时均不在此收尾——终态由 resumeRun 统一处理，
	// 防双终态收尾竞争/终态回退覆盖（isYielding 覆盖 WAITING 与全部终态）
	if isYielding(a.Controller(inv.ID).State()) {
		return
	}
	a.Controller(inv.ID).SetState(RuntimeCompleted, inv.StopCode)
	a.mu.Lock()
	a.retireLocked(inv)
	a.mu.Unlock()
	if a.memories != nil {
		_ = a.memories.Save(inv.ID)
	}
	// V2：调查完成时同步请求状态为 finished（内存锁内替换指针 + store 双写）
	if inv.Request != nil {
		var reqID string
		a.mu.Lock()
		cloned := cloneRequest(inv.Request)
		cloned.Status = RequestFinished
		cloned.UpdatedAt = time.Now().UTC()
		reqID = cloned.ID
		inv.Request = cloned
		a.mu.Unlock()
		if a.requests != nil {
			_ = a.requests.Link(reqID, inv.ID, RequestFinished)
		}
	}
	// V2.1：调查完成时写入跨案件知识记忆（地址/实体/案件/资金关联）
	if a.knowledge != nil {
		a.knowledge.recordKnowledge(inv)
	}
}

// setField 在锁内更新调查字段（与 Get/List 的锁内读保持一致）。
// Runtime V2 安全（LOW 加固）：controller 已终态时跳过更新——防主循环残留
// setField（如 AI 规划窗口）把已 retire 的终态调查短暂写回 active（复活闪回）。
func (a *InvestigationAgent) setField(inv *Investigation, mutate func(*Investigation)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c, ok := a.controllers[inv.ID]; ok && isRuntimeTerminal(c.State()) {
		return // 终态调查不更新（防复活闪回；resumeRun 收尾后不再 setField）
	}
	mutate(inv)
	a.active[inv.ID] = inv
	a.notifyLocked(inv)
}

// hasStopReason 检查决策理由是否包含指定子串（#5 优化用）。
func hasStopReason(reasons []string, sub string) bool {
	for _, r := range reasons {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

// Usage 返回 AI 用量统计（#10 优化）：DeepSeek 客户端统计 + 配置状态。
type AgentUsage struct {
	Configured bool    `json:"configured"`
	Usage      AIUsage `json:"usage"`
}

func (a *InvestigationAgent) Usage() AgentUsage {
	a.mu.Lock()
	configured := a.deepseek != nil && a.deepseek.Configured()
	var usage AIUsage
	if a.deepseek != nil {
		usage = a.deepseek.Usage()
	}
	a.mu.Unlock()
	return AgentUsage{Configured: configured, Usage: usage}
}

// ── SSE 订阅（#7 优化：前端进度实时推送，替代轮询）──

// maxSubscribersPerInvestigation 单个调查最大 SSE 订阅数（security review MEDIUM：
// 防连接耗尽 DoS——每个订阅是常驻连接+goroutine，无上限可被滥用）。
const maxSubscribersPerInvestigation = 4

// Subscribe 订阅调查状态变更（返回带缓冲 channel，调查进入终态时自动关闭）。
// 原子性：若订阅时调查已终态（active 或 history），立即发送终态快照并关闭 channel，
// 避免 Get 与 Subscribe 之间的完成竞态导致订阅挂起。
// 超出订阅上限时返回 nil（调用方应拒绝连接）。
func (a *InvestigationAgent) Subscribe(id string) chan *Investigation {
	ch := make(chan *Investigation, 8)
	a.mu.Lock()
	defer a.mu.Unlock()
	if inv := a.active[id]; inv != nil {
		if inv.Status == InvestigationCompleted || inv.Status == InvestigationFailed {
			ch <- inv
			close(ch)
			return ch
		}
	} else if inv := a.history[id]; inv != nil {
		// history 中的调查均为终态
		ch <- inv
		close(ch)
		return ch
	}
	if len(a.subscribers[id]) >= maxSubscribersPerInvestigation {
		return nil // 订阅超限：拒绝（防连接耗尽 DoS）
	}
	if a.subscribers == nil {
		a.subscribers = map[string]map[chan *Investigation]struct{}{}
	}
	if a.subscribers[id] == nil {
		a.subscribers[id] = map[chan *Investigation]struct{}{}
	}
	a.subscribers[id][ch] = struct{}{}
	return ch
}

// Unsubscribe 取消订阅（SSE 连接断开时调用，防 channel 泄漏）。
func (a *InvestigationAgent) Unsubscribe(id string, ch chan *Investigation) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if subs := a.subscribers[id]; subs != nil {
		if _, ok := subs[ch]; ok {
			delete(subs, ch)
		}
		if len(subs) == 0 {
			delete(a.subscribers, id)
		}
	}
}

// notifyLocked 非阻塞广播最新快照；终态时关闭全部订阅（须持锁调用）。
func (a *InvestigationAgent) notifyLocked(inv *Investigation) {
	for ch := range a.subscribers[inv.ID] {
		select {
		case ch <- inv:
		default: // 订阅者处理慢则丢弃（SSE 场景总是能消费）
		}
	}
	if inv.Status == InvestigationCompleted || inv.Status == InvestigationFailed {
		for ch := range a.subscribers[inv.ID] {
			close(ch)
		}
		delete(a.subscribers, inv.ID)
	}
}

// planInput 收集规划输入信号（画像/风险/资金概览）。
func (a *InvestigationAgent) planInput(ctx context.Context, target string) PlanInput {
	input := PlanInput{Target: target}
	if a.svc != nil {
		profile, err := a.svc.Profile(ctx, target)
		if err == nil && profile != nil {
			input.InCount = profile.TotalIn
			input.OutCount = profile.TotalOut
			input.HasFlows = profile.TransactionCount > 0
			risk, err := a.svc.Risk(ctx, target)
			if err == nil && risk != nil {
				input.RiskScore = risk.RiskScore
			}
		}
	}
	return input
}

// addConclusions 生成调查结论并写入记忆。
// 调用方需保证 inv.Paths/Patterns/AI 已由 setField 锁内写入完成（闭环顺序保证）。
func (a *InvestigationAgent) addConclusions(inv *Investigation) {
	mem, _ := a.memories.Get(inv.ID)
	if mem == nil {
		return
	}
	if len(inv.Paths) > 0 {
		top := inv.Paths[0]
		a.memories.AddConclusion(inv.ID, fmt.Sprintf("发现 %d 条资金路径，Top 路径：%s（%.1f 分）",
			len(inv.Paths), strings.Join(top.Path.Nodes, "→"), top.Score.Total))
	}
	if len(inv.Patterns) > 0 {
		a.memories.AddConclusion(inv.ID, fmt.Sprintf("检测到 %d 个风险模式（最高：%s）", len(inv.Patterns), inv.Patterns[0].Type))
	}
	if inv.AI != nil && inv.AI.Summary != "" {
		summary := strings.ReplaceAll(inv.AI.Summary, "\n", " ")
		if len(summary) > 200 {
			summary = summary[:200] + "..."
		}
		a.memories.AddConclusion(inv.ID, "AI 总结："+summary)
	}
	a.setField(inv, func(i *Investigation) {
		mem, _ := a.memories.Get(i.ID)
		i.Memory = mem
	})
}

// Controller 返回调查的运行时控制器（懒创建；测试直接构造 agent 时 map 可能为 nil）。
func (a *InvestigationAgent) Controller(id string) *RuntimeController {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.controllers == nil {
		a.controllers = make(map[string]*RuntimeController)
	}
	c, ok := a.controllers[id]
	if !ok {
		c = NewRuntimeController()
		a.controllers[id] = c
	}
	return c
}

// RuntimeStatus 返回调查的运行时状态视图（API 用，设计 §14）。
func (a *InvestigationAgent) RuntimeStatus(id string) (RuntimeStatus, bool) {
	if _, ok := a.Get(id); !ok {
		return RuntimeStatus{}, false
	}
	return a.Controller(id).Status(id), true
}

// setStage 更新调查阶段与进度。
func (a *InvestigationAgent) setStage(inv *Investigation, status InvestigationStatus, detail string, progress float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Runtime V2 安全（MEDIUM 修复）：
	// 1) 任何路径下 controller 已终态（resumeRun 收尾完成或调查已完成）→ 跳过内存
	//    更新，防主循环复活终态调查（不依赖 resuming 标志，覆盖所有交错窗口）；
	// 2) resuming 期间主循环不得进入执行阶段（PLANNED/RUNNING/...）→ 让位 WAITING，
	//    防双队列并发执行同一批任务。LoopEngine 检查点检测到 WAITING 即中止。
	if c, ok := a.controllers[inv.ID]; ok && isRuntimeTerminal(c.State()) {
		return
	}
	if a.resuming != nil && a.resuming[inv.ID] {
		runtime := statusToRuntime(status)
		if runtime != RuntimeCreated && runtime != RuntimeWaiting && !isRuntimeTerminal(runtime) {
			status = InvestigationWaiting
			detail = "等待恢复执行完成（resumeRun 持有执行权）"
		}
	}
	inv.Status = status
	inv.StageDetail = detail
	inv.Progress = progress
	inv.UpdatedAt = time.Now().UTC()
	a.active[inv.ID] = inv
	// Runtime V2：控制器状态同步（懒创建 + 终态携带 StopCode）
	if a.controllers == nil {
		a.controllers = make(map[string]*RuntimeController)
	}
	c, ok := a.controllers[inv.ID]
	if !ok {
		c = NewRuntimeController()
		a.controllers[inv.ID] = c
	}
	c.SetState(statusToRuntime(status), inv.StopCode)
	a.notifyLocked(inv)
}

// persistPlan 持久化调查计划（V1 Storage Layer §6，store 未配置时 no-op）。
func (a *InvestigationAgent) persistPlan(inv *Investigation, plan *InvestigationPlan) {
	if a.plans == nil || plan == nil {
		return
	}
	reqID := ""
	if inv.Request != nil {
		reqID = inv.Request.ID
	}
	rec := investigationstore.PlanRecord{
		ID:        "plan-" + inv.ID,
		RequestID: reqID,
		Target:    plan.Target,
		Mode:      string(plan.Mode),
		CreatedAt: plan.GeneratedAt,
	}
	for _, t := range plan.Tasks {
		rec.Tasks = append(rec.Tasks, investigationstore.PlannedTaskRecord{
			Type:        t.Type,
			Priority:    t.Priority,
			Description: t.Description,
		})
	}
	_ = a.plans.Save(rec.ID, rec)
}

// persistTasks 持久化任务快照（V1 Storage Layer §7 + Runtime V2 §5 扩展字段，store 未配置时 no-op）。
func (a *InvestigationAgent) persistTasks(invID string, tasks []InvestigationTask) {
	if a.tasks == nil {
		return
	}
	for _, t := range tasks {
		rec := investigationstore.TaskRecord{
			ID:              t.ID,
			InvestigationID: invID,
			Type:            t.Type,
			Status:          t.Status,
			Input:           t.Target, // 任务输入摘要 = 作用地址（恢复时还原 Target）
			Output:          t.Result,
			Error:           t.Error,
			Priority:        t.Priority,
			Round:           t.Round,
			UpdatedAt:       time.Now().UTC(),
			// ── Runtime V2（设计 §5/§11）：依赖/重试/超时/heartbeat 落盘 ──
			Dependencies: append([]string(nil), t.Dependencies...),
			MaxRetries:   t.MaxRetries,
			RetryCount:   t.RetryCount,
			TimeoutSec:   t.TimeoutSec,
			StartedAt:    t.StartedAt,
		}
		_ = a.tasks.Save(invID+"/"+t.ID, rec)
	}
}

// RecoverTasks 启动恢复（设计 §11）：读取 TaskStore 中该调查的 RUNNING 任务，
// heartbeat 超时（StartedAt 超过 TimeoutSec）标记 failed（可重试，RetryCount 未达上限）。
// 返回恢复的任务快照（恢复后的状态），供上层重新执行。
func (a *InvestigationAgent) RecoverTasks(invID string) []InvestigationTask {
	if a.tasks == nil {
		return nil
	}
	now := time.Now().Unix()
	var out []InvestigationTask
	for _, key := range a.tasks.Keys() {
		rec, ok := a.tasks.Get(key)
		if !ok || rec.InvestigationID != invID {
			continue
		}
		task := taskRecordToTask(rec)
		// heartbeat：RUNNING 且超时 → failed（可重试，但已达重试上限则保持终态不可再恢复）
		if task.Status == TaskRunning && rec.TimeoutSec > 0 && now-rec.StartedAt > int64(rec.TimeoutSec) {
			task.Status = TaskFailed
			task.Error = "heartbeat 超时（运行超过 " + itoa(rec.TimeoutSec) + "s）"
			task.Result = ""
			// 安全（MEDIUM 修复）：已达重试上限的超时任务保持 failed 终态，
			// resumeRun 不再将其重置为 pending（防止无限重试）
			if rec.MaxRetries > 0 && rec.RetryCount >= rec.MaxRetries {
				task.Status = TaskFailed
				task.Error += "（已达重试上限 " + itoa(rec.MaxRetries) + "）"
			}
			rec.Status = task.Status
			rec.Error = task.Error
			rec.UpdatedAt = time.Now().UTC()
			_ = a.tasks.Save(key, rec)
		}
		out = append(out, task)
	}
	return out
}

// taskRecordToTask 存储记录 → 业务任务（恢复用）。
func taskRecordToTask(rec investigationstore.TaskRecord) InvestigationTask {
	return InvestigationTask{
		ID:           rec.ID,
		Type:         rec.Type,
		Priority:     rec.Priority,
		Target:       rec.Input, // 还原任务作用地址
		Status:       rec.Status,
		Result:       rec.Output,
		Error:        rec.Error,
		Round:        rec.Round,
		Dependencies: append([]string(nil), rec.Dependencies...),
		MaxRetries:   rec.MaxRetries,
		RetryCount:   rec.RetryCount,
		TimeoutSec:   rec.TimeoutSec,
		StartedAt:    rec.StartedAt,
	}
}

// fail 标记调查失败（终态，通知 SSE 订阅者关闭连接）。
// V2：同步关联请求状态为 failed（store 双写）。
func (a *InvestigationAgent) fail(inv *Investigation, err error) {
	var reqID string
	a.Controller(inv.ID).SetState(RuntimeFailed, StopError) // Runtime V2：失败终态
	a.mu.Lock()
	inv.Status = InvestigationFailed
	inv.Error = err.Error()
	inv.StopCode = StopError // V2.1 Stop Strategy：调查出错
	inv.UpdatedAt = time.Now().UTC()
	// 锁内替换 Request 指针（深拷贝后写终态），避免锁外 JSON 编码读到被写对象
	if inv.Request != nil {
		cloned := cloneRequest(inv.Request)
		cloned.Status = RequestFailed
		cloned.UpdatedAt = time.Now().UTC()
		reqID = cloned.ID
		inv.Request = cloned
	}
	a.retireLocked(inv)
	a.notifyLocked(inv)
	a.mu.Unlock()
	if reqID != "" && a.requests != nil {
		_ = a.requests.Link(reqID, inv.ID, RequestFailed)
	}
}

// validEVMAddress 校验 EVM 地址（0x + 40 hex）。
func validEVMAddress(addr string) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if len(addr) != 42 || !strings.HasPrefix(addr, "0x") {
		return false
	}
	for _, c := range addr[2:] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// summarizePath 生成路径摘要（AI 上下文用）。
func summarizePath(p FundPath) string {
	if len(p.Edges) == 0 {
		return strings.Join(p.Nodes, " → ")
	}
	total := 0.0
	for _, e := range p.Edges {
		if f, ok := parseAmountFloat(e.Amount); ok {
			total += f
		}
	}
	first := p.Edges[0]
	return fmt.Sprintf("%s → %s（%s 总量 %.0f，%d 跳，起始 tx %s）",
		first.From, p.Edges[len(p.Edges)-1].To, first.Token, total, p.Hops, first.TxHash)
}
