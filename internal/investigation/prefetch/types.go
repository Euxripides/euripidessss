// Package prefetch 实现 Smart Prefetch Planner V1（设计 V1.0 §11-§36、§51-§58、§66-§75）：
// 候选生成、评分、HOT/WARM 优先级、Coverage 联动、低优先级后台队列、
// Interactive 升级、预算、反馈与缓存驱逐策略。全部纯文件存储。
package prefetch

import "time"

// Priority 预取优先级（设计 §16-§19）。
type Priority string

const (
	PriorityHOT  Priority = "HOT"
	PriorityWARM Priority = "WARM"
	PriorityCOLD Priority = "COLD"
)

// Status 预取任务状态。
type Status string

const (
	StatusPending     Status = "PENDING"
	StatusPrefetching Status = "PREFETCHING"
	StatusReady       Status = "READY"
	StatusPaused      Status = "PAUSED"
	StatusInteractive Status = "INTERACTIVE"
	StatusFailed      Status = "FAILED"
	StatusEvicted     Status = "EVICTED"
)

// Candidate 是预取候选（设计 §12）。
type Candidate struct {
	ChainID          int64    `json:"chain_id"`
	ChainKey         string   `json:"chain_key"`
	Address          string   `json:"address"`
	ParentAddress    string   `json:"parent_address,omitempty"`
	Reason           []string `json:"reason,omitempty"`
	Score            float64  `json:"score"`
	EstimatedRows    uint64   `json:"estimated_rows,omitempty"`
	EstimatedBytes   uint64   `json:"estimated_bytes,omitempty"`
	RequiredDatasets []string `json:"required_datasets,omitempty"`
	Priority         Priority `json:"priority"`
	TokenFilter      string   `json:"token_filter,omitempty"`
	FromBlock        uint64   `json:"from_block"`
	ToBlock          uint64   `json:"to_block"`
	InvestigationID  string   `json:"investigation_id"`
	Pinned           bool       `json:"pinned,omitempty"`
	ProgressiveStage int        `json:"progressive_stage,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// Job 是队列中的预取任务（含 Smart Download Batch 关联）。
type Job struct {
	ID           string     `json:"id"`
	Candidate    Candidate  `json:"candidate"`
	Status       Status     `json:"status"`
	BatchID      string     `json:"batch_id,omitempty"`
	BatchStatus  string     `json:"batch_status,omitempty"`
	UpgradeCount int        `json:"upgrade_count,omitempty"`
	UsedAt       *time.Time `json:"used_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

// Budget 是预取预算配置（设计 §33）。
type Budget struct {
	MaxDiskPerDayGB      float64 `json:"max_disk_per_day_gb"`
	MaxNetworkPerDayGB   float64 `json:"max_network_per_day_gb"`
	MaxCloudCostPerDay   float64 `json:"max_cloud_cost_per_day"`
	MaxPrefetchAddresses int     `json:"max_prefetch_addresses"`
	MaxActivePrefetchJobs int    `json:"max_active_prefetch_jobs"`
}

// DefaultBudget 返回设计默认值（预取资源占比 < 15%）。
func DefaultBudget() Budget {
	return Budget{
		MaxDiskPerDayGB:       2,
		MaxNetworkPerDayGB:    1,
		MaxCloudCostPerDay:    0,
		MaxPrefetchAddresses:  20,
		MaxActivePrefetchJobs: 2,
	}
}

// Counters 是当日预算计数。
type Counters struct {
	Day        string  `json:"day"`
	DiskGB     float64 `json:"disk_gb"`
	NetworkGB  float64 `json:"network_gb"`
	CloudCost  float64 `json:"cloud_cost"`
	Addresses  int     `json:"addresses"`
	ActiveJobs int     `json:"active_jobs"`
}

// FeedbackRecord 是预取反馈记录（设计 §35）。
type FeedbackRecord struct {
	InvestigationID string    `json:"investigation_id"`
	Address         string    `json:"address"`
	BatchID         string    `json:"batch_id,omitempty"`
	Used            bool      `json:"used"`
	TimeToUseSeconds float64  `json:"time_to_use_seconds,omitempty"`
	SavedWaitSeconds float64  `json:"saved_wait_seconds,omitempty"`
	DownloadCostBytes uint64  `json:"download_cost_bytes,omitempty"`
	RecordedAt      time.Time `json:"recorded_at"`
}

// FeedbackStats 是预取成效指标（设计 §36-§37、§77）。
type FeedbackStats struct {
	Total            int     `json:"total"`
	Used             int     `json:"used"`
	Unused           int     `json:"unused"`
	HitRate          float64 `json:"hit_rate"`
	SavedLatencyAvg  float64 `json:"saved_latency_avg"`
	WastedBytes      uint64  `json:"wasted_bytes"`
}

// DiskPolicy 是磁盘阈值策略（设计 §58）。
type DiskPolicy struct {
	PauseWarmAt float64 `json:"pause_warm_at"`
	PauseAllAt  float64 `json:"pause_all_at"`
	BlockNewAt  float64 `json:"block_new_at"`
}

// DefaultDiskPolicy 返回默认阈值。
func DefaultDiskPolicy() DiskPolicy {
	return DiskPolicy{PauseWarmAt: 0.8, PauseAllAt: 0.9, BlockNewAt: 0.95}
}

// DiskAction 是磁盘策略动作。
type DiskAction string

const (
	DiskNone     DiskAction = "NONE"
	DiskPauseWarm DiskAction = "PAUSE_WARM"
	DiskPauseAll DiskAction = "PAUSE_ALL"
	DiskBlockNew DiskAction = "BLOCK_NEW"
)

// Stats 是预取管理器汇总。
type Stats struct {
	TotalJobs         int            `json:"total_jobs"`
	ActiveJobs        int            `json:"active_jobs"`
	ReadyJobs         int            `json:"ready_jobs"`
	InteractiveUpgrades int          `json:"interactive_upgrades"`
	Budget            Budget         `json:"budget"`
	Counters          Counters       `json:"counters"`
	Feedback          FeedbackStats  `json:"feedback"`
	DiskUsedPct       float64        `json:"disk_used_pct"`
	DiskAction        DiskAction     `json:"disk_action"`
	LastRun           *time.Time     `json:"last_run,omitempty"`
}
