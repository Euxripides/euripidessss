// Package intelligence 实现 V2.1 RC2 全自动链上调查平台 Intelligence Layer：
// 输入一个地址 → 调查规划 → Beam Search 资金追踪 → 路径排名 → 实体识别 →
// 地址扩展 → 风险检测 → AI 上下文构建 → DeepSeek 分析 → 报告生成。
//
// 设计原则：AI 不替代计算。交易解析/金额计算/路径搜索/图计算/余额计算/证据定位
// 全部由程序完成；AI 负责调查规划、行为解释、路径分析、结论总结、报告生成。
package intelligence

import "time"

// ── 调查任务 ──

// InvestigationStatus 是调查生命周期状态。
type InvestigationStatus string

const (
	InvestigationCreated   InvestigationStatus = "CREATED"
	InvestigationPlanning  InvestigationStatus = "PLANNING"
	InvestigationRunning   InvestigationStatus = "RUNNING" // 执行调查任务队列
	InvestigationAnalyzing InvestigationStatus = "ANALYZING"
	InvestigationExpanding InvestigationStatus = "EXPANDING" // 决策 + 扩展下一轮
	InvestigationVerifying InvestigationStatus = "VERIFYING" // 验证结果并固化记忆
	InvestigationReporting InvestigationStatus = "REPORTING"
	InvestigationCompleted InvestigationStatus = "COMPLETED"
	InvestigationFailed    InvestigationStatus = "FAILED"
	// ── Runtime V2（设计 §4 状态机扩展）──
	InvestigationWaiting InvestigationStatus = "WAITING" // 任务等待依赖/资源
	InvestigationStopped InvestigationStatus = "STOPPED" // 用户取消（对应 StopUserCancel 预留）
	// 兼容旧状态（不再作为主流程阶段使用）
	InvestigationTracing InvestigationStatus = "TRACING"
)

// TerminalStatuses 是全部终态（生命周期判断用）。
var TerminalStatuses = map[InvestigationStatus]bool{
	InvestigationCompleted: true,
	InvestigationFailed:    true,
	InvestigationStopped:   true,
}

// Investigation 是一次完整调查。
type Investigation struct {
	ID          string               `json:"id"`
	Target      string               `json:"target"` // 目标地址（EVM 校验后）
	ChainID     string               `json:"chain_id"`
	Status      InvestigationStatus  `json:"status"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	Plan        *InvestigationPlan   `json:"plan,omitempty"`
	Paths       []RankedPath         `json:"paths,omitempty"`
	Entities    []EntityInfo         `json:"entities,omitempty"`
	Patterns    []RiskPattern        `json:"patterns,omitempty"`
	Expansions  []ExpansionResult    `json:"expansions,omitempty"`
	AI          *AIAnalysis          `json:"ai_analysis,omitempty"`
	Memory      *InvestigationMemory `json:"memory,omitempty"`
	Report      *ReportOutput        `json:"report,omitempty"`
	Progress    float64              `json:"progress"` // 0-100
	Error       string               `json:"error,omitempty"`
	StageDetail string               `json:"stage_detail,omitempty"`

	// ── 调查闭环（V2.1 RC2 智能调查闭环与自主决策引擎）──
	Round        int                 `json:"round"`                  // 当前调查轮次
	Rounds       []RoundRecord       `json:"rounds,omitempty"`       // 每轮决策记录（全过程可追踪）
	Tasks        []InvestigationTask `json:"tasks,omitempty"`        // 任务队列执行记录
	Observations []Observation       `json:"observations,omitempty"` // 调查观察结果
	Decision     *Decision           `json:"decision,omitempty"`     // 最近一轮决策
	StopReason   string              `json:"stop_reason,omitempty"`  // 智能停止原因
	CompletedAt  time.Time           `json:"completed_at,omitempty"`

	// 启动时配置覆盖（POST /start 的 config，仅本调查生效，不序列化、不污染全局配置）
	cfgOverride *IntelligenceConfig

	// ── AI 驱动调查（V2.1 RC2 DeepSeek 驱动自主调查 Agent）──
	Strategy     *AIStrategy       `json:"strategy,omitempty"`      // AI 调查策略
	Hypotheses   []AIHypothesis    `json:"hypotheses,omitempty"`    // 调查假设
	Findings     []VerifiedFinding `json:"findings,omitempty"`      // 经证据验证的 AI 发现
	AISuggestion *AISuggestion     `json:"ai_suggestion,omitempty"` // AI 下一步建议

	// ── V2 调查请求与六维评分（设计 §4/§9）──
	Request *InvestigationRequest `json:"request,omitempty"`             // 用户调查请求（目的/期望结果/模式）
	Score   *InvestigationScore   `json:"investigation_score,omitempty"` // 六维调查价值评分
	Profit  *ProfitReport         `json:"profit_report,omitempty"`       // 获利/沉淀检测报告（PROFIT_DETECTION）

	// ── V2.1 Evidence Layer（设计 §1）──
	Evidence []Evidence `json:"evidence,omitempty"` // 证据链（交易/地址/时间/风险/获利）

	// ── Runtime V2（设计 §8/§9 动态追加与 Re-plan）──
	Replans []ReplanSignal `json:"replans,omitempty"` // Re-plan 触发记录（事件型动态扩展）

	// ── V2.1 Stop Strategy（设计 §4）──
	StopCode StopCode `json:"stop_code,omitempty"` // 停止原因枚举（终态时）
}

// ── 获利检测报告（V2 设计 §10：Fund Score 信号源）──

// ProfitChecklistItem 是获利检测依据明细（✓/✗/?）。
type ProfitChecklistItem struct {
	OK    bool   `json:"ok"`    // true=✓ 依据成立；false=✗ 依据不成立；OK 缺失由 Present 表示 ?
	Label string `json:"label"` // 依据项描述
	// Present 表示该依据是否可评估（false = ? 信息缺失，如缺少历史价格）
	Present bool `json:"present"`
}

// ProfitReport 是获利/沉淀检测结果（V2.1：估算金额 + 可信度 + 依据明细）。
// 无链上历史价格 oracle：稳定币部分按净额估算，非稳定币部分标注缺少历史价格。
type ProfitReport struct {
	Detected     bool                  `json:"detected"`               // 检测到获利或沉淀结构
	Kind         string                `json:"kind"`                   // profit（买卖对账）/ holding（沉淀）/ both
	Tokens       []string              `json:"tokens,omitempty"`       // 涉及 Token
	EstimateUSD  float64               `json:"estimate_usd,omitempty"` // 估算金额（USD；仅稳定币沉淀部分可估）
	Confidence   float64               `json:"confidence"`             // 可信度 0-1（无 oracle 封顶 0.85）
	Checklist    []ProfitChecklistItem `json:"checklist,omitempty"`    // 依据明细 ✓/✗/?
	Summary      string                `json:"summary"`                // 检测摘要
	EstimateNote string                `json:"estimate_note"`          // 估算口径说明
}

// ── AI 驱动调查（V2.1 RC2 DeepSeek 驱动自主调查 Agent）──

// AITask 是 AI 生成的调查任务（结构化输出，§5/§11）。
type AITask struct {
	Type     string         `json:"type"`             // 对应 7 种任务类型（Task* 常量）
	Priority float64        `json:"priority"`         // 0-1
	Target   string         `json:"target,omitempty"` // 作用地址
	Reason   string         `json:"reason,omitempty"` // 生成理由
	Params   map[string]any `json:"params,omitempty"`
}

// AIStrategy 是 AI 调查策略（Planner Agent 输出）。
type AIStrategy struct {
	Strategy   string   `json:"strategy"` // trace_outgoing / trace_incoming / entity_focus / risk_scan ...
	Tasks      []AITask `json:"tasks"`
	Rationale  string   `json:"rationale"`  // 策略理由
	Confidence float64  `json:"confidence"` // 0-1
}

// AIFinding 是 AI 分析发现（AML/Forensic Analyst 输出，§11 结构化约束）。
type AIFinding struct {
	Type       string   `json:"type"` // rapid_transfer / layering / smurfing / concentration ...
	Address    string   `json:"address,omitempty"`
	Detail     string   `json:"detail"`
	Confidence float64  `json:"confidence"` // 0-1
	Evidence   []string `json:"evidence"`   // tx_hash / block_number 引用
}

// EvidenceStatus 是证据验证状态（§12 Evidence Guard）。
type EvidenceStatus string

const (
	EvidenceVerified   EvidenceStatus = "VERIFIED"   // 证据已在链上数据确认
	EvidenceRejected   EvidenceStatus = "REJECTED"   // 证据与数据不符
	EvidenceUnverified EvidenceStatus = "UNVERIFIED" // 缺少可验证证据
)

// VerifiedFinding 是经 Evidence Guard 验证的 AI 发现。
type VerifiedFinding struct {
	Finding    AIFinding      `json:"finding"`
	Status     EvidenceStatus `json:"status"`
	Reason     string         `json:"reason"`
	VerifiedAt time.Time      `json:"verified_at"`
}

// AIHypothesis 是调查假设（§7 Hypothesis Agent）。
type AIHypothesis struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Confidence  float64   `json:"confidence"` // 0-1
	Source      string    `json:"source"`     // rule / ai
	Status      string    `json:"status"`     // proposed / verifying / evaluated
	Tasks       []AITask  `json:"tasks"`      // 验证任务
	Note        string    `json:"note,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	TaskIDs     []string  `json:"-"` // 验证任务入队后的 ID（状态门控用，不序列化）
}

// AISuggestion 是 AI 下一步建议（决策输入，§6：AI 建议 → Decision Engine 验证）。
type AISuggestion struct {
	Action     string   `json:"action"` // EXPAND / STOP / DEEP_ANALYSIS / VERIFY
	Target     string   `json:"target,omitempty"`
	Reasons    []string `json:"reasons"`
	Confidence float64  `json:"confidence"`
	Source     string   `json:"source"` // planner / hypothesis / analysis
}

// ── 调查任务队列 ──

// 调查任务类型（设计 §7；V2 扩展至 12 种，旧常量保留兼容）。
const (
	TaskAddressProfile = "ADDRESS_PROFILE" // 地址画像
	TaskFlowAnalysis   = "FLOW_ANALYSIS"   // 资金流分析（旧）
	TaskPathTrace      = "PATH_TRACE"      // 路径追踪（旧）
	TaskEntityCheck    = "ENTITY_CHECK"    // 实体检查（旧）
	TaskRiskScan       = "RISK_SCAN"       // 风险扫描（旧）
	TaskExpandAddress  = "EXPAND_ADDRESS"  // 地址扩展（旧）
	TaskGenerateReport = "GENERATE_REPORT" // 报告生成（旧）

	// ── V2 12 种任务类型（Investigation Agent Planner V2 §6）──
	TaskBalanceAnalysis = "BALANCE_ANALYSIS"   // 余额分析
	TaskTokenAnalysis   = "TOKEN_ANALYSIS"     // Token 分析（持仓/分布）
	TaskProfitDetection = "PROFIT_DETECTION"   // 获利检测（买卖对账/沉淀）
	TaskForwardTrace    = "FORWARD_TRACE"      // 正向资金追踪（去向）
	TaskBackwardTrace   = "BACKWARD_TRACE"     // 反向资金追踪（来源）
	TaskFlowGraph       = "FLOW_GRAPH"         // 资金流图构建
	TaskExchangeDetect  = "EXCHANGE_DETECTION" // 交易所入口识别
	TaskEntityCluster   = "ENTITY_CLUSTER"     // 实体聚类
	TaskRiskAnalysis    = "RISK_ANALYSIS"      // 风险分析（RISK_SCAN 别名）
	TaskIdentityLookup  = "IDENTITY_LOOKUP"    // 身份线索查找
	TaskReportGenerate  = "REPORT_GENERATE"    // 智能报告生成（GENERATE_REPORT 别名）
)

// AllTaskTypes 是全部调查任务类型（含旧兼容类型）。
var AllTaskTypes = []string{
	TaskAddressProfile, TaskBalanceAnalysis, TaskTokenAnalysis, TaskProfitDetection,
	TaskForwardTrace, TaskBackwardTrace, TaskFlowGraph, TaskExchangeDetect,
	TaskEntityCluster, TaskRiskAnalysis, TaskIdentityLookup, TaskReportGenerate,
	TaskFlowAnalysis, TaskPathTrace, TaskEntityCheck, TaskRiskScan, TaskExpandAddress, TaskGenerateReport,
}

// 任务状态。
const (
	TaskPending = "pending"
	TaskRunning = "running"
	TaskDone    = "done"
	TaskSkipped = "skipped"
	TaskFailed  = "failed"
)

// InvestigationTask 是一条已进入执行队列的调查任务。
// Runtime V2（设计 §5）：新增依赖/重试/超时/开始时间字段（omitempty 向后兼容）。
type InvestigationTask struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Priority    int    `json:"priority"`
	Target      string `json:"target,omitempty"` // 任务作用地址
	Status      string `json:"status"`
	Result      string `json:"result,omitempty"` // 结果摘要
	Error       string `json:"error,omitempty"`
	Round       int    `json:"round"` // 所属轮次

	// ── Runtime V2（设计 §5 任务模型扩展）──
	Dependencies []string `json:"dependencies,omitempty"` // 依赖任务 ID（全部 done 后才可执行）
	MaxRetries   int      `json:"max_retries,omitempty"`  // 失败最大重试次数（0 = 不重试）
	RetryCount   int      `json:"retry_count,omitempty"`  // 已重试次数
	TimeoutSec   int      `json:"timeout_sec,omitempty"`  // 执行超时秒数（0 = 不超时）
	StartedAt    int64    `json:"started_at,omitempty"`   // 最近一次开始执行的时间戳（heartbeat 用）
}

// ── 调查观察结果 ──

// ObservationType 是观察结果类型（设计 §8）。
type ObservationType string

const (
	ObsNewAddress     ObservationType = "NEW_ADDRESS"     // 新地址
	ObsNewPath        ObservationType = "NEW_PATH"        // 新路径
	ObsNewTransaction ObservationType = "NEW_TRANSACTION" // 新交易
	ObsRiskEvent      ObservationType = "RISK_EVENT"      // 风险事件
)

// Observation 是调查过程中发现的一条观察结果。
type Observation struct {
	ID        string          `json:"id"`
	Type      ObservationType `json:"type"`
	Address   string          `json:"address,omitempty"`
	Detail    string          `json:"detail"`
	Source    string          `json:"source"` // 来源（round N / 任务类型）
	Value     float64         `json:"value"`  // 金额或风险分
	Timestamp int64           `json:"timestamp"`
}

// ── 自主决策 ──

// StopCode 是停止原因枚举（V2.1 设计 §4 Stop Strategy）。
type StopCode string

const (
	StopTargetFound StopCode = "TARGET_FOUND"   // 已达成调查目标（如发现交易所入口）
	StopNoValue     StopCode = "NO_VALUE"       // 无调查价值（无新发现/重复关系）
	StopLowConf     StopCode = "LOW_CONFIDENCE" // 证据可信度不足（低价值候选）
	StopBudgetLimit StopCode = "BUDGET_LIMIT"   // 达到预算上限（轮次/时间/地址/任务）
	StopUserCancel  StopCode = "USER_CANCEL"    // 用户取消（预留）
	StopError       StopCode = "ERROR"          // 调查出错
)

// DecisionAction 是决策动作（设计 §9）。
type DecisionAction string

const (
	DecisionExpand       DecisionAction = "EXPAND"        // 继续：扩展高价值地址
	DecisionStop         DecisionAction = "STOP"          // 停止：无价值/已达限制
	DecisionDeepAnalysis DecisionAction = "DEEP_ANALYSIS" // 深入：AI 深入分析
)

// DecisionScores 是决策评分分项。
type DecisionScores struct {
	PathScore      float64 `json:"path_score"`      // 资金路径价值
	RiskScore      float64 `json:"risk_score"`      // 风险评分
	EntityScore    float64 `json:"entity_score"`    // 实体评分
	ExpansionScore float64 `json:"expansion_score"` // 扩展价值评分

	// ── V2 六维评分（设计 §9；Fund 维度由 InvestigationScorer 计算填充）──
	BehaviorScore float64 `json:"behavior_score,omitempty"` // 行为价值
	GraphScore    float64 `json:"graph_score,omitempty"`    // 图价值
	IdentityScore float64 `json:"identity_score,omitempty"` // 身份价值
	FundScore     float64 `json:"fund_score,omitempty"`     // 资金价值
}

// Decision 是一轮调查的决策结果。
type Decision struct {
	Action      DecisionAction `json:"action"`
	Round       int            `json:"round"`
	Scores      DecisionScores `json:"scores"`
	Reasons     []string       `json:"reasons"`
	NextTargets []string       `json:"next_targets,omitempty"` // EXPAND 时下一轮目标地址
	StopCode    StopCode       `json:"stop_code,omitempty"`    // V2.1：停止原因枚举（STOP 时）
	MadeAt      time.Time      `json:"made_at"`
}

// RoundRecord 是一轮调查的追踪记录。
type RoundRecord struct {
	Round      int            `json:"round"`
	Decision   DecisionAction `json:"decision,omitempty"`
	Note       string         `json:"note,omitempty"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt time.Time      `json:"finished_at"`
}

// ── 调查规划 ──

// InvestigationPlan 是调查规划器输出：调查任务清单。
type InvestigationPlan struct {
	Target      string        `json:"target"`
	Objectives  []string      `json:"objectives"` // 调查目标（来源/去向/高价值路径/实体关系）
	Tasks       []PlannedTask `json:"tasks"`      // 调查任务清单
	MaxHops     int           `json:"max_hops"`   // 追踪最大跳数
	BeamWidth   int           `json:"beam_width"` // Beam Search 宽度
	TopPaths    int           `json:"top_paths"`  // 保留 Top K 路径
	MinAmount   string        `json:"min_amount"` // 金额阈值
	GeneratedAt time.Time     `json:"generated_at"`

	// ── V2（设计 §7/§12：mode 驱动 + 预计时长）──
	Mode             InvestigationMode `json:"mode,omitempty"`              // 计划依据的调查模式
	EstimatedMinutes int               `json:"estimated_minutes,omitempty"` // 预计执行时长（分钟）
}

// PlannedTask 是一条调查任务。
type PlannedTask struct {
	ID          string `json:"id"`
	Type        string `json:"type"` // FUND_SOURCE / FUND_FLOW / HIGH_VALUE_PATH / ENTITY_RELATION / RISK_CHECK
	Description string `json:"description"`
	Priority    int    `json:"priority"`
}

// ── 资金追踪（Beam Search） ──

// FundEdge 是一条带时间维度的资金边。
type FundEdge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Token     string `json:"token"`
	Amount    string `json:"amount"`
	TxHash    string `json:"tx_hash"`
	Block     uint64 `json:"block"`
	Timestamp int64  `json:"timestamp"`
	LogIdx    string `json:"log_index"`
}

// FundPath 是一条候选资金路径（Beam Search 保留）。
type FundPath struct {
	Nodes []string   `json:"nodes"`
	Edges []FundEdge `json:"edges"`
	Hops  int        `json:"hops"`
}

// ── 路径排名 ──

// PathScore 是路径评分分项。
type PathScore struct {
	Amount         float64 `json:"amount"`          // 金额权重
	TimeContinuity float64 `json:"time_continuity"` // 时间连续性
	Risk           float64 `json:"risk"`            // 风险权重
	Relation       float64 `json:"relation"`        // 关系强度
	EntityPenalty  float64 `json:"entity_penalty"`  // 实体惩罚
	Total          float64 `json:"total"`
}

// RankedPath 是排名后的路径。
type RankedPath struct {
	Path    FundPath  `json:"path"`
	Score   PathScore `json:"score"`
	Summary string    `json:"summary"` // 路径摘要（AI 上下文用）
}

// ── 实体识别 ──

// EntityInfo 是地址实体信息。
type EntityInfo struct {
	Address string         `json:"address"`
	Entity  string         `json:"entity"` // wallet/exchange/bridge/dex/router/contract/unknown
	Label   string         `json:"label,omitempty"`
	Risk    float64        `json:"risk"`
	TxCount int64          `json:"tx_count"`
	Signals map[string]any `json:"signals,omitempty"`
}

// ── 风险模式 ──

// RiskPatternType 是风险模式类型。
type RiskPatternType string

const (
	PatternRapidTransfer RiskPatternType = "RAPID_TRANSFER" // 快速转移：收到资金后短时间转出
	PatternMultiSplit    RiskPatternType = "MULTI_SPLIT"    // 多地址拆分：A → B1/B2/B3
	PatternConcentration RiskPatternType = "CONCENTRATION"  // 归集：B1/B2/B3 → C
	PatternLargeInflow   RiskPatternType = "LARGE_INFLOW"   // 大额进入
	PatternRapidDrain    RiskPatternType = "RAPID_DRAIN"    // 快速清空
)

// RiskPattern 是一次风险模式检测结果。
type RiskPattern struct {
	Type       RiskPatternType `json:"type"`
	Address    string          `json:"address"`
	Severity   string          `json:"severity"` // high/medium/low
	Detail     string          `json:"detail"`
	Edges      []FundEdge      `json:"edges,omitempty"`
	DetectedAt time.Time       `json:"detected_at"`
}

// ── 地址扩展 ──

// ExpansionResult 是地址扩展结果。
type ExpansionResult struct {
	Address     string  `json:"address"`
	Entity      string  `json:"entity"`
	Score       float64 `json:"score"`
	Acquisition string  `json:"acquisition"` // SQD_LOGS/CSV_DIRECT/RELATION_ONLY
	Depth       int     `json:"depth"`
	Reason      string  `json:"reason"`
}

// ── AI 分析 ──

// AIAnalysis 是 DeepSeek 分析结果。
type AIAnalysis struct {
	Summary     string   `json:"summary"`      // 行为解释/结论总结
	Insights    []string `json:"insights"`     // 洞察列表
	Suggestions []string `json:"suggestions"`  // 下一步建议
	RiskComment string   `json:"risk_comment"` // 风险解释
	Model       string   `json:"model"`
	DurationMs  int64    `json:"duration_ms"`
}

// AIContext 是发送给 DeepSeek 的上下文（分析摘要，非原始交易）。
type AIContext struct {
	Target     string         `json:"target"`
	Profile    map[string]any `json:"profile"`
	TopPaths   []string       `json:"top_paths"`
	RiskEvents []string       `json:"risk_events"`
	Entities   []string       `json:"entities"`
	Timeline   []string       `json:"timeline"`
	History    []string       `json:"history,omitempty"` // 历史调查/AI 结论（§13 AI Memory）
	Token      string         `json:"token"`

	// ── V2 调查请求（设计 §3/§4：目的/期望结果/模式注入 AI 规划）──
	Objective      string   `json:"objective,omitempty"`
	ExpectedResult []string `json:"expected_result,omitempty"`
	Mode           string   `json:"mode,omitempty"`
}

// ── 调查记忆 ──

// InvestigationMemory 保存调查状态，避免重复分析。
type InvestigationMemory struct {
	InvestigationID string               `json:"investigation_id"`
	Target          string               `json:"target"`
	DiscoveredAt    map[string]time.Time `json:"discovered_at"`    // 已发现地址
	AnalyzedPaths   []string             `json:"analyzed_paths"`   // 已分析路径（签名）
	IgnoredEntities []string             `json:"ignored_entities"` // 已忽略实体
	CompletedTasks  []string             `json:"completed_tasks"`  // 已完成任务（ID 列表）
	Conclusions     []string             `json:"conclusions"`      // 调查结论
	UpdatedAt       time.Time            `json:"updated_at"`
}

// ── 报告 ──

// ReportFormat 是报告格式。
type ReportFormat string

const (
	ReportMarkdown ReportFormat = "markdown"
	ReportHTML     ReportFormat = "html"
	ReportJSON     ReportFormat = "json"
)

// ReportOutput 是报告代理输出。
type ReportOutput struct {
	Format   ReportFormat `json:"format"`
	Content  string       `json:"content"`
	Filename string       `json:"filename"`
}

// ── 调查配置 ──

// IntelligenceConfig 是调查引擎配置。
type IntelligenceConfig struct {
	MaxHops      int    `json:"max_hops"`      // 追踪最大跳数（默认 4）
	BeamWidth    int    `json:"beam_width"`    // Beam Search 宽度（默认 8）
	TopPaths     int    `json:"top_paths"`     // Top K 路径（默认 10）
	MinAmount    string `json:"min_amount"`    // 金额阈值
	UseAI        bool   `json:"use_ai"`        // 是否调用 DeepSeek（默认 true）
	AIModel      string `json:"ai_model"`      // 默认 deepseek-v4-flash
	AITimeoutMS  int    `json:"ai_timeout_ms"` // DeepSeek 超时（默认 60000）
	MaxExpansion int    `json:"max_expansion"` // 扩展地址数上限

	// ── 调查闭环限制（设计 §10 自动扩展策略）──
	MaxRounds          int     `json:"max_rounds"`          // 最大调查轮次（默认 3）
	MaxRuntimeMS       int     `json:"max_runtime_ms"`      // 最长运行时间 ms（默认 300000=5 分钟，0=不限）
	MaxAddresses       int     `json:"max_addresses"`       // 最多发现地址数（默认 200）
	ExpansionThreshold float64 `json:"expansion_threshold"` // 扩展候选评分门槛（默认 50，0-100）
	MaxTasks           int     `json:"max_tasks"`           // V2.1：总任务数预算（默认 50，防动态任务无限扩张）

	// ── Runtime V2（设计 §5/§11：任务级超时与重试）──
	TaskTimeoutSec int `json:"task_timeout_sec"` // 单任务执行超时秒数（默认 120，0=不超时）
	TaskMaxRetries int `json:"task_max_retries"` // 单任务失败最大重试次数（默认 1，0=不重试）

	// ── AI 驱动调查（§15 DeepSeek 接口 / §17 调用限额）──
	MaxTokens  int `json:"max_tokens"`   // DeepSeek 输出上限（默认 2000）
	MaxAICalls int `json:"max_ai_calls"` // 单个调查最多 AI 调用次数（默认 10，≤0 时按默认）
}

// DefaultConfig 返回默认调查配置。
func DefaultConfig() IntelligenceConfig {
	return IntelligenceConfig{
		MaxHops:            4,
		BeamWidth:          8,
		TopPaths:           10,
		MinAmount:          "0",
		UseAI:              true,
		AIModel:            "deepseek-v4-flash",
		AITimeoutMS:        60000,
		MaxExpansion:       50,
		MaxRounds:          3,
		MaxRuntimeMS:       300000,
		MaxAddresses:       200,
		ExpansionThreshold: 50,
		MaxTasks:           50,   // V2.1 总任务预算（防动态任务无限扩张）
		TaskTimeoutSec:     120,  // Runtime V2：单任务超时 120s
		TaskMaxRetries:     1,    // Runtime V2：失败重试 1 次
		MaxTokens:          4096, // deepseek-v4-flash 含推理阶段，2000 易被推理占满致 content 截断/为空
		MaxAICalls:         10,
	}
}
