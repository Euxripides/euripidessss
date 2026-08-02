package intelligence

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/dynamicinvestigation"
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
	ai             *AIAgent        // AI 驱动调查（§3，可为 nil）
	aiChatter      AIChatter       // AI 对话实现（NewAgent 默认 deepseek；测试可注入 fake）
	aiMemory       *AIMemoryStore  // AI 记忆共享实例（rebuild 复用，防并发丢记忆）
	aiMemoryDir    string          // AI 记忆持久化目录
	cfg            IntelligenceConfig

	mu      sync.Mutex
	active  map[string]*Investigation // id → 进行中的调查
	history map[string]*Investigation // id → 已完成的调查
	nextID  int
	// subscribers 是 SSE 订阅者（id → channel 集合，#7 优化）
	subscribers map[string]map[chan *Investigation]struct{}
}

// AgentOptions 是代理依赖注入选项。
type AgentOptions struct {
	Service        *analyticsapi.Service
	FlowSource     FlowSource
	Expansion      *ExpansionEngine
	Recognizer     interface{} // *dynamicinvestigation.Recognizer（可选）
	DeepSeekKey    string
	MemoryDir      string
	Config         IntelligenceConfig
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
		svc:         opts.Service,
		flowSource:  flowSource,
		ranker:      ranker,
		tracer:      NewFundTracer(flowSource, ranker, cfg),
		planner:     NewPlanner(cfg),
		detector:    NewPatternDetector(cfg),
		report:      NewReportAgent(cfg),
		contextBuilder: NewAIContextBuilder(cfg),
		deepseek:    NewDeepSeekClient(opts.DeepSeekKey, cfg.AIModel, cfg.AITimeoutMS, cfg.MaxTokens),
		deepseekKey: opts.DeepSeekKey,
		cfg:         cfg,
		active:      make(map[string]*Investigation),
		history:     make(map[string]*Investigation),
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
	agent.aiChatter = agent.deepseek                        // 默认使用 DeepSeek；测试可注入 fake
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
	}
	if len(cfgOverride) > 0 && cfgOverride[0] != nil {
		c := *cfgOverride[0]
		inv.cfgOverride = &c // 仅本调查生效，不污染全局配置
	}
	a.active[id] = inv
	a.memories.New(id, target)
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

// run 执行调查闭环（规划 → 执行 → 观察 → 判断 → 重新规划，直到完成条件）。
// 注意：inv 字段写入全部经 setField 锁内进行，避免与 Get/List 的锁内读竞争；
// 子组件使用启动时快照（snapshot），避免与其他调查的 rebuild 写竞争。
func (a *InvestigationAgent) run(ctx context.Context, inv *Investigation, snap agentSnapshot) {
	if err := NewLoopEngine().Run(ctx, a, inv, snap); err != nil {
		a.fail(inv, err)
		return
	}
	a.mu.Lock()
	a.history[inv.ID] = inv
	a.mu.Unlock()
	if a.memories != nil {
		_ = a.memories.Save(inv.ID)
	}
}

// setField 在锁内更新调查字段（与 Get/List 的锁内读保持一致）。
func (a *InvestigationAgent) setField(inv *Investigation, mutate func(*Investigation)) {
	a.mu.Lock()
	defer a.mu.Unlock()
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

// setStage 更新调查阶段与进度。
func (a *InvestigationAgent) setStage(inv *Investigation, status InvestigationStatus, detail string, progress float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	inv.Status = status
	inv.StageDetail = detail
	inv.Progress = progress
	inv.UpdatedAt = time.Now().UTC()
	a.active[inv.ID] = inv
	a.notifyLocked(inv)
}

// fail 标记调查失败（终态，通知 SSE 订阅者关闭连接）。
func (a *InvestigationAgent) fail(inv *Investigation, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	inv.Status = InvestigationFailed
	inv.Error = err.Error()
	inv.UpdatedAt = time.Now().UTC()
	a.active[inv.ID] = inv
	a.history[inv.ID] = inv
	a.notifyLocked(inv)
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
