// Package downloadscheduler 实现 Smart Download Orchestrator 智能下载调度：
// 分析数据需求 → 覆盖检查 → 规则+评分选择 Provider → 生成下载计划 → 状态机执行（重试/切换）→ 就绪供图谱。
//
// 设计依据：Smart Download Orchestrator 智能下载调度系统 V2.0（Agent 自治决策版）。
package downloadscheduler

import (
	"time"

	"github.com/etl/backend/internal/objectiveplanner"
	"github.com/etl/backend/internal/parquetdownload"
)

// Dataset 数据需求类型（Layer 1 规则引擎的输入维度）。
type Dataset string

const (
	DatasetBalance       Dataset = "balance"        // 实时余额 → RPC
	DatasetTransactions  Dataset = "transactions"   // 历史交易 → SQD/Parquet
	DatasetTokenTransfer Dataset = "token_transfer" // Token 转账事件 → SQD
	DatasetLabels        Dataset = "labels"         // 标签信息 → Browser（需手动）
)

// ValidDataset 校验数据集类型。
func ValidDataset(d Dataset) bool {
	switch d {
	case DatasetBalance, DatasetTransactions, DatasetTokenTransfer, DatasetLabels:
		return true
	}
	return false
}

// ProviderKind 数据获取能力类型。
type ProviderKind string

const (
	ProviderRPC      ProviderKind = "rpc"       // 实时余额/合约状态
	ProviderSQD      ProviderKind = "sqd"       // 历史交易/Token Transfer/Logs/Trace
	ProviderAWS      ProviderKind = "aws"       // S3 公共 Parquet（BSC 原生交易，设计 §9 首选）
	ProviderBrowser  ProviderKind = "browser"   // CSV/网页爬取（标签、公开资料）
	ProviderSQDCloud ProviderKind = "sqd_cloud" // SQD Cloud 应急兜底（Tier 100，最后一级）
)

// Requirement 单条数据需求。
type Requirement struct {
	ID        string   `json:"id"`
	PlanID    string   `json:"plan_id,omitempty"`
	Dataset   Dataset  `json:"dataset"`
	ChainKey  string   `json:"chain_key"`
	Addresses []string `json:"addresses"`
	StartDate string   `json:"start_date,omitempty"`
	EndDate   string   `json:"end_date,omitempty"`
	FromBlock uint64   `json:"from_block,omitempty"` // 显式区块范围（Chunk 级任务控制，设计 §33）
	ToBlock   uint64   `json:"to_block,omitempty"`
	Direction string   `json:"direction,omitempty"` // 图联动：upstream/downstream（预留，最小版不参与执行）
	Depth     int      `json:"depth,omitempty"`     // 图联动深度（预留）
	Note      string   `json:"note,omitempty"`
	// CloudEligible 是否允许触发应急 Cloud（设计 §78）：nil=允许（交互/调查/手动）。
	CloudEligible *bool `json:"cloud_eligible,omitempty"`
	// Objective 驱动规划（Phase 5.4 §7-§9）：目标决定数据集，不指定 Provider。
	ObjectiveType        string                         `json:"objective_type,omitempty"`
	ObjectiveDescription string                         `json:"objective_description,omitempty"`
	ObjectiveConstraints objectiveplanner.Constraints   `json:"objective_constraints,omitempty"`
}

// CloudAllowed 判断任务是否允许触发 Cloud（nil 默认允许）。
func (r Requirement) CloudAllowed() bool {
	return r.CloudEligible == nil || *r.CloudEligible
}

// ProviderScore 三层决策模型 Layer 2：Provider 评分。
// 分项 0-100，Total = 加权求和（coverage/accuracy/speed/cost/reliability）。
type ProviderScore struct {
	Provider    ProviderKind  `json:"provider"`
	Name        string        `json:"name"`
	Tier        ProviderTier  `json:"tier"`
	State       ProviderState `json:"state,omitempty"`
	Coverage    int           `json:"coverage"`
	Accuracy    int           `json:"accuracy"`
	Speed       int           `json:"speed"`
	Cost        int           `json:"cost"`
	Reliability int           `json:"reliability"`
	Total       int           `json:"total"`
	Available   bool          `json:"available"`   // 该 Provider 当前是否已配置可用
	ManualOnly  bool          `json:"manual_only"` // 仅能人工执行（如浏览器登录态采集）
	Reasons     []string      `json:"reasons"`
}

// TaskResult 任务执行结果。
type TaskResult struct {
	JobID   string `json:"job_id,omitempty"`   // 下游任务 ID（如 parquetdownload job）
	Output  string `json:"output,omitempty"`   // 产物路径/摘要
	Summary string `json:"summary,omitempty"`  // 人类可读摘要
	Rows    int64  `json:"rows,omitempty"`     // 获取的数据行数
	NewData bool   `json:"new_data,omitempty"` // 是否实际新增了数据
}

// ProviderAttempt Provider 调用审计（设计 §21 provider_attempts）。
type ProviderAttempt struct {
	Provider   ProviderKind  `json:"provider"`
	Tier       ProviderTier  `json:"tier"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
	Success    bool          `json:"success"`
	State      ProviderState `json:"state,omitempty"`
	Error      string        `json:"error,omitempty"`
	Rows       int64         `json:"rows,omitempty"`
	LatencyMS  int64         `json:"latency_ms,omitempty"`
}

// PlanTask 计划中的单个任务。
type PlanTask struct {
	ID          string            `json:"id"`
	Requirement Requirement       `json:"requirement"`
	Candidates  []ProviderScore   `json:"candidates"` // 按总分降序
	Provider    ProviderKind      `json:"provider"`   // 当前选中的 Provider
	Status      string            `json:"status"`     // pending/running/done/failed/skipped
	Retries     int               `json:"retries"`
	JobID       string            `json:"job_id,omitempty"`
	Progress    float64           `json:"progress"`
	Error       string            `json:"error,omitempty"`
	Result      *TaskResult       `json:"result,omitempty"`
	StartedAt   *time.Time        `json:"started_at,omitempty"`
	FinishedAt  *time.Time        `json:"finished_at,omitempty"`
	Attempts    []ProviderAttempt `json:"attempts,omitempty"` // Provider 调用审计
	Cloud       *CloudTaskInfo    `json:"cloud,omitempty"`    // Cloud Admission 兜底信息
}

// Plan 下载计划（状态机 §13）。
type Plan struct {
	ID          string                                `json:"id"`
	Status      PlanStatus                            `json:"status"`
	StageDetail string                                `json:"stage_detail,omitempty"`
	Tasks       []*PlanTask                           `json:"tasks"`
	Budget      Budget                                `json:"budget"`
	Recovery    []*parquetdownload.RecoveryMergeStats `json:"recovery,omitempty"` // MERGING 合并统计（RPC 恢复层 §9/§10，按链）
	Cloud       *CloudRunInfo                         `json:"cloud,omitempty"`    // Cloud 兜底汇总
	CreatedAt   time.Time                             `json:"created_at"`
	StartedAt   *time.Time                            `json:"started_at,omitempty"`
	FinishedAt  *time.Time                            `json:"finished_at,omitempty"`
	Error       string                                `json:"error,omitempty"`
}

// PlanStatus 计划状态机（设计文档 §13）。
type PlanStatus string

const (
	StatusAnalyzing      PlanStatus = "ANALYZING_REQUIREMENT"
	StatusSelecting      PlanStatus = "SELECTING_PROVIDER"
	StatusBuilding       PlanStatus = "BUILDING_PLAN"
	StatusExecuting      PlanStatus = "EXECUTING"
	StatusRetrying       PlanStatus = "RETRYING"
	StatusFallback       PlanStatus = "FALLBACK"
	StatusValidating     PlanStatus = "VALIDATING"
	StatusMerging        PlanStatus = "MERGING"
	StatusCloudAdmission PlanStatus = "CLOUD_ADMISSION" // 应急 Cloud 准入
	StatusCloudQueued    PlanStatus = "CLOUD_QUEUED"    // 应急 Cloud 排队
	StatusCloudRunning   PlanStatus = "CLOUD_RUNNING"   // 应急 Cloud 执行
	StatusWaitingRetry   PlanStatus = "WAITING_RETRY"   // Cloud 拒绝/失败，等待重试
	StatusReady          PlanStatus = "READY_FOR_GRAPH"
	StatusFailed         PlanStatus = "FAILED"
	StatusCancelRequested PlanStatus = "CANCEL_REQUESTED" // Phase 5.4 §5：用户请求取消
	StatusCancelled      PlanStatus = "CANCELLED"         // 独立终态：cancelled != failed
)

// Terminal 判断是否为终态。
func (s PlanStatus) Terminal() bool {
	return s == StatusReady || s == StatusFailed || s == StatusCancelled
}

// Budget 下载预算（设计文档 §16：地址数量限制/下载预算）。
type Budget struct {
	MaxAddressesPerTask int         `json:"max_addresses_per_task"` // 单任务地址数上限
	MaxTasksPerPlan     int         `json:"max_tasks_per_plan"`     // 单计划任务数上限
	MaxConcurrentPlans  int         `json:"max_concurrent_plans"`   // 并发计划数上限（1 = 串行，parquetdownload 单任务限制）
	MaxRetriesPerTask   int         `json:"max_retries_per_task"`   // 单任务重试上限
	Cloud               CloudBudget `json:"cloud"`                  // SQD Cloud 预算 Guard
}

// DefaultBudget 返回默认预算。
// MaxAddressesPerTask 是地址硬上限（V3 §7：低活跃 chunk 500 / 普通 100 / 高活跃 20）。
func DefaultBudget() Budget {
	return Budget{
		MaxAddressesPerTask: 500,
		MaxTasksPerPlan:     5,
		MaxConcurrentPlans:  1,
		MaxRetriesPerTask:   1,
		Cloud:               DefaultCloudBudget(),
	}
}

// Coverage 单个数据集覆盖情况。
type Coverage struct {
	Dataset Dataset `json:"dataset"`
	Have    bool    `json:"have"`     // 本地数据集内是否已有该地址数据
	TxCount int64   `json:"tx_count"` // 数据集内交易笔数（0 = 无）
	Note    string  `json:"note,omitempty"`
}

// CoverageResult 覆盖检查结果（设计文档 §10 Coverage Resolver）。
type CoverageResult struct {
	ChainKey  string     `json:"chain_key"`
	Addresses []string   `json:"addresses"`
	Items     []Coverage `json:"items"`
}
