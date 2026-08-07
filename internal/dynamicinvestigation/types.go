// Package dynamicinvestigation 实现 V2.1 RC2 动态地址扩展与智能采集路由：
// 分析过程中自动发现关联地址 → 地址评分 → 实体识别 → 智能采集路由 → 数据等级升级。
// 只影响任务生成层：引擎产出采集任务，执行复用现有下载引擎与 SQD 数据源。
package dynamicinvestigation

import (
	"sync"
	"time"
)

// ── 地址发现队列状态 ──

// DiscoveryStatus 表示地址在扩展队列中的生命周期状态。
type DiscoveryStatus string

const (
	StatusDiscovered DiscoveryStatus = "DISCOVERED" // 已发现，等待评分
	StatusScoring    DiscoveryStatus = "SCORING"    // 评分中
	StatusApproved   DiscoveryStatus = "APPROVED"   // 评分通过，批准采集
	StatusAcquiring  DiscoveryStatus = "ACQUIRING"  // 采集中（数据等级升级中）
	StatusCompleted  DiscoveryStatus = "COMPLETED"  // 采集完成
	StatusIgnored    DiscoveryStatus = "IGNORED"    // 低价值/超限，忽略（仅保存关系）
)

// ValidTransitions 定义队列状态机的合法迁移。
var ValidTransitions = map[DiscoveryStatus][]DiscoveryStatus{
	StatusDiscovered: {StatusScoring, StatusIgnored},
	StatusScoring:    {StatusApproved, StatusIgnored},
	StatusApproved:   {StatusAcquiring, StatusIgnored},
	StatusAcquiring:  {StatusCompleted, StatusAcquiring, StatusIgnored},
	StatusCompleted:  {}, // 终态
	StatusIgnored:    {}, // 终态
}

// ── 实体识别 ──

// EntityType 表示地址实体分类。
type EntityType string

const (
	EntityWallet   EntityType = "wallet"   // 普通钱包
	EntityExchange EntityType = "exchange" // 交易所热钱包
	EntityBridge   EntityType = "bridge"   // 跨链桥
	EntityDex      EntityType = "dex"      // DEX 路由器
	EntityRouter   EntityType = "router"   // 聚合器/路由器
	EntityContract EntityType = "contract" // 合约
	EntityUnknown  EntityType = "unknown"  // 未知
)

// ── 智能采集路由 ──

// AcquisitionMode 表示地址的采集方式。
type AcquisitionMode string

const (
	AcquisitionSQDLogs         AcquisitionMode = "SQD_LOGS"         // 普通钱包：SQD Logs 增量
	AcquisitionSQDTransactions AcquisitionMode = "SQD_TRANSACTIONS" // 数据等级升级：Transactions
	AcquisitionSQDTrace        AcquisitionMode = "SQD_TRACE"        // 数据等级升级：Trace
	AcquisitionCSVDirect       AcquisitionMode = "CSV_DIRECT"       // 大型实体：CSV 直链下载
	AcquisitionRelationsOnly   AcquisitionMode = "RELATION_ONLY"    // 低价值：仅保存关系
)

// ── 数据等级 ──

// DataLevel 表示地址的数据获取深度，先低成本判断再提高采集级别。
type DataLevel int

const (
	LevelDiscover     DataLevel = 0 // 发现（关系已知）
	LevelLogs         DataLevel = 1 // Logs（ERC20 转账事件）
	LevelTransfer     DataLevel = 2 // Transfer（转账明细）
	LevelTransactions DataLevel = 3 // Transactions（交易明细）
	LevelTrace        DataLevel = 4 // Trace（内部调用追踪）
)

// String 返回数据等级名称。
func (l DataLevel) String() string {
	switch l {
	case LevelDiscover:
		return "DISCOVER"
	case LevelLogs:
		return "LOGS"
	case LevelTransfer:
		return "TRANSFER"
	case LevelTransactions:
		return "TRANSACTIONS"
	case LevelTrace:
		return "TRACE"
	default:
		return "UNKNOWN"
	}
}

// ── 扩展队列条目 ──

// DiscoveredAddress 是地址发现队列中的一条记录。
type DiscoveredAddress struct {
	Address        string             `json:"address"`                   // 地址（小写）
	Source         string             `json:"source"`                    // 发现来源（目标地址/上一跳）
	Amount         string             `json:"amount"`                    // 关联金额（raw hex 或 decimal 字符串）
	Token          string             `json:"token"`                     // 关联 Token
	Depth          int                `json:"depth"`                     // 扩展深度（目标地址=0）
	Status         DiscoveryStatus    `json:"status"`                    // 队列状态
	Score          float64            `json:"score,omitempty"`           // Expansion Score
	ScoreBreakdown map[string]float64 `json:"score_breakdown,omitempty"` // 评分分项
	Entity         EntityType         `json:"entity,omitempty"`          // 实体类型
	Acquisition    AcquisitionMode    `json:"acquisition,omitempty"`     // 采集方式
	TargetLevel    DataLevel          `json:"target_level"`              // 路由目标数据等级（未升级前）
	DataLevel      DataLevel          `json:"data_level"`                // 当前已获取数据等级
	RiskScore      float64            `json:"risk_score,omitempty"`      // 风险评分（来源：analyticsapi.Risk）
	TxCount        int64              `json:"tx_count,omitempty"`        // 交易笔数
	Label          string             `json:"label,omitempty"`           // 实体标签（如 Binance Hot Wallet）
	JobID          string             `json:"job_id,omitempty"`          // 关联的下载任务 ID
	IgnoredReason  string             `json:"ignored_reason,omitempty"`  // 忽略原因
	DiscoveredAt   time.Time          `json:"discovered_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

// ── 引擎配置 ──

// ExpansionConfig 控制扩展边界（验收标准：最大深度/最大地址数/金额阈值）。
type ExpansionConfig struct {
	MaxDepth            int     `json:"max_depth"`             // 最大扩展深度（默认 2）
	MaxAddresses        int     `json:"max_addresses"`         // 最大队列地址数（默认 500）
	AmountThreshold     string  `json:"amount_threshold"`      // 金额阈值（raw decimal，低于则忽略）
	MinScore            float64 `json:"min_score"`             // 最低通过评分（默认 30）
	RiskWeight          float64 `json:"risk_weight"`           // 风险权重
	RelationWeight      float64 `json:"relation_weight"`       // 关联强度权重
	ActivityWeight      float64 `json:"activity_weight"`       // 活跃度权重
	AmountWeight        float64 `json:"amount_weight"`         // 金额权重
	EntityPenalty       float64 `json:"entity_penalty"`        // 实体惩罚基数
	RelationsPerAddress int     `json:"relations_per_address"` // 每地址最多发现的关联地址数
	ChainID             string  `json:"chain_id"`              // 默认链（bsc）
	UseSQD              bool    `json:"use_sqd"`               // 是否允许 SQD 增量采集
	UseCSVDirect        bool    `json:"use_csv_direct"`        // 是否允许 CSV 直链（大型实体）
}

// DefaultConfig 返回默认扩展配置。
func DefaultConfig() ExpansionConfig {
	return ExpansionConfig{
		MaxDepth:            2,
		MaxAddresses:        500,
		AmountThreshold:     "0",
		MinScore:            30,
		RiskWeight:          25,
		RelationWeight:      20,
		ActivityWeight:      15,
		AmountWeight:        25,
		EntityPenalty:       15,
		RelationsPerAddress: 20,
		ChainID:             "bsc",
		UseSQD:              true,
		UseCSVDirect:        true,
	}
}

// ── 评分输入 ──

// ScoreInput 是一次地址评分所需的信号。
type ScoreInput struct {
	Address       string     `json:"address"`
	Entity        EntityType `json:"entity"`
	Amount        string     `json:"amount"`         // 关联金额
	RiskScore     float64    `json:"risk_score"`     // 0-100
	SharedCounter int        `json:"shared_counter"` // 共同对手数（关联强度）
	RelationScore float64    `json:"relation_score"` // 关联强度 0-1
	TxCount       int64      `json:"tx_count"`       // 交易笔数（活跃度）
	Degree        int        `json:"degree"`         // 图度（活跃度补充）
}

// ScoreResult 是评分结果。
type ScoreResult struct {
	Score     float64            `json:"score"`
	Breakdown map[string]float64 `json:"breakdown"` // amount/risk/relation/activity/entity_penalty
	Decision  Decision           `json:"decision"`
	Reason    string             `json:"reason"`
}

// Decision 表示评分后的采集决策。
type Decision string

const (
	DecisionAcquire Decision = "ACQUIRE" // 批准采集
	DecisionHold    Decision = "HOLD"    // 暂缓（未达阈值但保留）
	DecisionIgnore  Decision = "IGNORE"  // 忽略（低价值）
)

// ── 采集任务 ──

// AcquisitionTask 是引擎产出的采集任务（任务生成层输出）。
type AcquisitionTask struct {
	TaskID      string          `json:"task_id"`
	Address     string          `json:"address"`
	ChainID     string          `json:"chain_id"`
	Entity      EntityType      `json:"entity"`
	Mode        AcquisitionMode `json:"mode"`
	TargetLevel DataLevel       `json:"target_level"`
	FromLevel   DataLevel       `json:"from_level"`
	Addresses   []string        `json:"addresses,omitempty"` // CSV 直链/批量用
	StartBlock  uint64          `json:"start_block,omitempty"`
	EndBlock    uint64          `json:"end_block,omitempty"`
	StartDate   string          `json:"start_date,omitempty"`
	EndDate     string          `json:"end_date,omitempty"`
	Priority    int             `json:"priority"`

	// 以下可变字段由 mu 保护：Status/JobID/Error/UpdatedAt 会被引擎、
	// 执行器与 GET /tasks 并发访问。JSON 输出使用 TaskView 视图。
	mu        sync.Mutex
	status    string
	jobID     string
	errMsg    string
	updatedAt time.Time

	CreatedAt time.Time `json:"created_at"`
}

// Status 返回任务状态（线程安全）。
func (t *AcquisitionTask) Status() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

// SetStatus 设置任务状态（线程安全）。
func (t *AcquisitionTask) SetStatus(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status = s
	t.updatedAt = time.Now().UTC()
}

// JobID 返回关联下载任务 ID（线程安全）。
func (t *AcquisitionTask) JobID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.jobID
}

// SetJobID 设置关联下载任务 ID（线程安全）。
func (t *AcquisitionTask) SetJobID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.jobID = id
	t.updatedAt = time.Now().UTC()
}

// Error 返回任务错误信息（线程安全）。
func (t *AcquisitionTask) Error() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.errMsg
}

// SetError 设置任务错误信息（线程安全）。
func (t *AcquisitionTask) SetError(e string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errMsg = e
	t.updatedAt = time.Now().UTC()
}

// Touch 更新时间戳（线程安全）。
func (t *AcquisitionTask) Touch() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.updatedAt = time.Now().UTC()
}

// UpdatedAt 返回更新时间（线程安全）。
func (t *AcquisitionTask) UpdatedAt() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.updatedAt
}

// Snapshot 返回任务可变状态快照（线程安全）。
func (t *AcquisitionTask) Snapshot() (status, jobID, errMsg string, updatedAt time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status, t.jobID, t.errMsg, t.updatedAt
}

// TaskView 是任务的无锁只读视图（JSON 输出用，避免拷贝含锁值）。
type TaskView struct {
	TaskID      string          `json:"task_id"`
	Address     string          `json:"address"`
	ChainID     string          `json:"chain_id"`
	Entity      EntityType      `json:"entity"`
	Mode        AcquisitionMode `json:"mode"`
	TargetLevel DataLevel       `json:"target_level"`
	FromLevel   DataLevel       `json:"from_level"`
	Addresses   []string        `json:"addresses,omitempty"`
	StartBlock  uint64          `json:"start_block,omitempty"`
	EndBlock    uint64          `json:"end_block,omitempty"`
	StartDate   string          `json:"start_date,omitempty"`
	EndDate     string          `json:"end_date,omitempty"`
	Priority    int             `json:"priority"`
	Status      string          `json:"status"`
	JobID       string          `json:"job_id,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// View 返回任务的无锁只读视图（线程安全）。
func (t *AcquisitionTask) View() TaskView {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TaskView{
		TaskID:      t.TaskID,
		Address:     t.Address,
		ChainID:     t.ChainID,
		Entity:      t.Entity,
		Mode:        t.Mode,
		TargetLevel: t.TargetLevel,
		FromLevel:   t.FromLevel,
		Addresses:   append([]string(nil), t.Addresses...),
		StartBlock:  t.StartBlock,
		EndBlock:    t.EndBlock,
		StartDate:   t.StartDate,
		EndDate:     t.EndDate,
		Priority:    t.Priority,
		Status:      t.status,
		JobID:       t.jobID,
		Error:       t.errMsg,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.updatedAt,
	}
}

// ── 引擎状态 ──

// EngineStats 是引擎运行统计。
type EngineStats struct {
	TotalDiscovered int                     `json:"total_discovered"`
	TotalApproved   int                     `json:"total_approved"`
	TotalCompleted  int                     `json:"total_completed"`
	TotalIgnored    int                     `json:"total_ignored"`
	TotalTasks      int                     `json:"total_tasks"`
	ByEntity        map[EntityType]int      `json:"by_entity"`
	ByAcquisition   map[AcquisitionMode]int `json:"by_acquisition"`
	LastRun         *time.Time              `json:"last_run,omitempty"`
	Config          ExpansionConfig         `json:"config"`
}
