// Package smartdownload 实现智能下载统一入口的第一阶段任务层：
// BatchJob → AddressJob → DatasetJob → RangeJob 四层模型、FS 状态存储、
// Universal Checkpoint V3、Range Ledger、Recovery、Pause/Resume/Cancel。
//
// 设计依据：智能下载系统_从V2.2到完整实现_实施方案_V1.1（Phase 1）。
// 本阶段不重写下载器：ProviderAdapter 只负责“把某个 Range 的原始数据拿回来”。
package smartdownload

import (
	"context"
	"time"

	"github.com/etl/backend/internal/smartdownload/discovery"
)

// ── Dataset 类型（Phase 1 枚举；Phase 2 接入真实 Adapter 时扩展）──

const (
	DatasetTransactions         = "transactions"
	DatasetInternalTransactions = "internal_transactions"
	DatasetTokenTransfers       = "token_transfers"
	DatasetLogs                 = "logs"
	DatasetBalances             = "balances"
	DatasetTokenMetadata        = "token_metadata"
	DatasetNFTTransfers         = "nft_transfers"
	DatasetContractCreations    = "contract_creations"
)

var validDatasets = map[string]bool{
	DatasetTransactions:         true,
	DatasetInternalTransactions: true,
	DatasetTokenTransfers:       true,
	DatasetLogs:                 true,
	DatasetBalances:             true,
	DatasetTokenMetadata:        true,
	DatasetNFTTransfers:         true,
	DatasetContractCreations:    true,
}

// ValidDataset 校验数据集类型。
func ValidDataset(name string) bool {
	return validDatasets[name]
}

// ── 状态机（Phase 1 最小集；Phase 2 增加 SWITCHING_PROVIDER/VALIDATING）──

type BatchStatus string

const (
	BatchCreated BatchStatus = "CREATED"
	BatchRunning BatchStatus = "RUNNING"
	BatchPaused  BatchStatus = "PAUSED"
	// BatchPausedByPriority is a checkpoint-safe scheduler pause. It is
	// automatically resumed after the higher-priority batch settles.
	BatchPausedByPriority BatchStatus = "PAUSED_BY_PRIORITY"
	BatchCompleted        BatchStatus = "COMPLETED"
	BatchPartial          BatchStatus = "PARTIAL"
	BatchFailed           BatchStatus = "FAILED"
	BatchCanceled         BatchStatus = "CANCELED"
)

func (s BatchStatus) Terminal() bool {
	return s == BatchCompleted || s == BatchPartial || s == BatchFailed || s == BatchCanceled
}

type AddressStatus string

const (
	AddressWaiting     AddressStatus = "WAITING"
	AddressDownloading AddressStatus = "DOWNLOADING"
	AddressPaused      AddressStatus = "PAUSED"
	AddressCompleted   AddressStatus = "COMPLETED"
	AddressPartial     AddressStatus = "PARTIAL"
	AddressFailed      AddressStatus = "FAILED"
	AddressCanceled    AddressStatus = "CANCELED"
)

func (s AddressStatus) Terminal() bool {
	return s == AddressCompleted || s == AddressPartial || s == AddressFailed || s == AddressCanceled
}

type DatasetStatus string

const (
	DatasetPending    DatasetStatus = "PENDING"
	DatasetRunning    DatasetStatus = "RUNNING"
	DatasetPaused     DatasetStatus = "PAUSED"
	DatasetValidating DatasetStatus = "VALIDATING"
	DatasetIndexing   DatasetStatus = "INDEXING"
	// DatasetDBWriteFailed 表示下载、Parquet 合并和校验均已完成，但写入分析库失败。
	// 它故意不是终态：RecoverAll/RetryIndexedDataset 可直接重试写库，不重新下载。
	DatasetDBWriteFailed DatasetStatus = "DB_WRITE_FAILED"
	DatasetCompleted     DatasetStatus = "COMPLETED"
	DatasetPartial       DatasetStatus = "PARTIAL"
	DatasetFailed        DatasetStatus = "FAILED"
	DatasetCanceled      DatasetStatus = "CANCELED"
)

func (s DatasetStatus) Terminal() bool {
	return s == DatasetCompleted || s == DatasetPartial || s == DatasetFailed || s == DatasetCanceled
}

// IndexedWriteRequest 是认证 Parquet 写入链上数仓的稳定边界。
type IndexedWriteRequest struct {
	DatasetJobID      string
	ChainKey          string
	ChainID           int64
	Dataset           string
	Address           string
	FromBlock         uint64
	ToBlock           uint64
	RowCount          int64
	MergedParquet     string
	SourceProvider    string
	ParserVersion     string
	NormalizerVersion string
	SchemaVersion     uint16
}

// IndexedWriteResult 对账 writer_input = inserted + rejected。
// ClickHouse ReplacingMergeTree 仅提供逻辑键替换语义，不代表物理行唯一。
type IndexedWriteResult struct {
	InputRows    int64 `json:"writer_input"`
	InsertedRows int64 `json:"writer_success"`
	RejectedRows int64 `json:"writer_reject"`
	ActivityRows int64 `json:"activity_rows"`
	VerifiedRows int64 `json:"db_logical_rows,omitempty"`
}

// IndexedWriter 写入认证数据集；具体 ClickHouse 客户端由 datawarehouse 包适配。
type IndexedWriter interface {
	WriteIndexed(ctx context.Context, req IndexedWriteRequest) (IndexedWriteResult, error)
}

type RangeStatus string

const (
	RangePending   RangeStatus = "PENDING"
	RangeReady     RangeStatus = "READY"
	RangeRunning   RangeStatus = "RUNNING"
	RangeCompleted RangeStatus = "COMPLETED"
	RangeEmpty     RangeStatus = "EMPTY"
	RangeFailed    RangeStatus = "FAILED"
	RangeCanceled  RangeStatus = "CANCELED"
)

func (s RangeStatus) Terminal() bool {
	return s == RangeCompleted || s == RangeEmpty || s == RangeFailed || s == RangeCanceled
}

// DownloadMode controls orchestration. AUTO keeps the scored multi-provider
// scheduler; TURBO reserves missing ranges for SQD Cloud bulk and RPC fast lanes.
type DownloadMode string

const (
	DownloadModeAuto      DownloadMode = "AUTO"
	DownloadModeTurbo     DownloadMode = "TURBO"
	DownloadModeEmergency DownloadMode = "EMERGENCY"
)

func (m DownloadMode) Valid() bool {
	return m == DownloadModeAuto || m == DownloadModeTurbo || m == DownloadModeEmergency
}

// JobPriority is the externally visible batch queue. RangePriority is the
// finer P0-P4 ordering inside a batch.
type JobPriority string

const (
	PriorityUrgent     JobPriority = "URGENT"
	PriorityHigh       JobPriority = "HIGH"
	PriorityNormal     JobPriority = "NORMAL"
	PriorityBackground JobPriority = "BACKGROUND"
)

func (p JobPriority) Valid() bool {
	return p == PriorityUrgent || p == PriorityHigh || p == PriorityNormal || p == PriorityBackground
}

type RangePriority string

const (
	RangePriorityP0 RangePriority = "P0" // current Explorer range
	RangePriorityP1 RangePriority = "P1" // current investigation range
	RangePriorityP2 RangePriority = "P2" // next-hop fund-flow range
	RangePriorityP3 RangePriority = "P3" // latest tail
	RangePriorityP4 RangePriority = "P4" // remaining history
)

func (p RangePriority) Valid() bool {
	return p == RangePriorityP0 || p == RangePriorityP1 || p == RangePriorityP2 || p == RangePriorityP3 || p == RangePriorityP4
}

type CertificationLevel string

const (
	CertificationPending        CertificationLevel = "PENDING"
	CertificationRange          CertificationLevel = "RANGE_CERTIFIED"
	CertificationDatasetPartial CertificationLevel = "DATASET_PARTIAL_CERTIFIED"
	CertificationDataset        CertificationLevel = "DATASET_CERTIFIED"
	CertificationBatch          CertificationLevel = "BATCH_CERTIFIED"
)

type RangeOwner string

const (
	RangeOwnerCloud RangeOwner = "SQD_CLOUD"
	RangeOwnerRPC   RangeOwner = "RPC"
)

// ── 范围与进度 ──

// RangeMode 时间/区块范围模式。
type RangeMode string

const (
	RangeModeFull  RangeMode = "FULL"
	RangeModeTime  RangeMode = "TIME"
	RangeModeBlock RangeMode = "BLOCK"
)

// RangeSpec 单个地址请求的数据范围。
type RangeSpec struct {
	Mode      RangeMode `json:"mode"`
	FromBlock uint64    `json:"from_block,omitempty"`
	ToBlock   uint64    `json:"to_block,omitempty"`
	StartTime string    `json:"start_time,omitempty"`
	EndTime   string    `json:"end_time,omitempty"`
}

// ProgressSnapshot 统一进度快照（Phase 3 增加速度/ETA 细分）。
type ProgressSnapshot struct {
	Percent              float64 `json:"percent"`
	RowsCurrent          uint64  `json:"rows_current"`
	RowsTotal            uint64  `json:"rows_total"`
	BlocksCurrent        uint64  `json:"blocks_current"`
	BlocksTotal          uint64  `json:"blocks_total"`
	BytesCurrent         uint64  `json:"bytes_current,omitempty"`
	BytesTotal           uint64  `json:"bytes_total,omitempty"`
	SpeedRowsPerSec      float64 `json:"speed_rows_per_sec,omitempty"`
	ETASeconds           float64 `json:"eta_seconds,omitempty"`
	ETAConfidence        float64 `json:"eta_confidence,omitempty"`
	ETALowerBoundSeconds float64 `json:"eta_lower_bound_seconds,omitempty"`
	ETAUpperBoundSeconds float64 `json:"eta_upper_bound_seconds,omitempty"`
	ETARecalculating     bool    `json:"eta_recalculating,omitempty"`
	ETABasedOn           string  `json:"eta_based_on,omitempty"`
}

// ── 四层任务模型 ──

// BatchJob 一次用户提交。
type BatchJob struct {
	ID               string             `json:"id"`
	ChainKey         string             `json:"chain_key"`
	ChainID          int64              `json:"chain_id"`
	Status           BatchStatus        `json:"status"`
	AddressCount     int                `json:"address_count"`
	DatasetTypes     []string           `json:"dataset_types"`
	Mode             DownloadMode       `json:"mode"`
	Priority         JobPriority        `json:"priority,omitempty"`
	ResourceProfile  ResourceProfile    `json:"resource_profile,omitempty"`
	Preflight        *PreflightEstimate `json:"preflight,omitempty"`
	ModeSwitchedAt   *time.Time         `json:"mode_switched_at,omitempty"`
	Prefetch         bool               `json:"prefetch,omitempty"`          // 后台预取任务（低优先级，不占用前台交互资源）
	PrefetchPriority int                `json:"prefetch_priority,omitempty"` // 3=HOT / 4=WARM / 5=COLD
	PauseRequested   bool               `json:"pause_requested,omitempty"`
	CancelRequested  bool               `json:"cancel_requested,omitempty"`
	PausedByPriority bool               `json:"paused_by_priority,omitempty"`
	PreemptedBy      string             `json:"preempted_by,omitempty"`
	Error            string             `json:"error,omitempty"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
	StartedAt        *time.Time         `json:"started_at,omitempty"`
	FinishedAt       *time.Time         `json:"finished_at,omitempty"`
}

// AddressJob 每个地址独立。
type AddressJob struct {
	ID              string           `json:"id"`
	BatchID         string           `json:"batch_id"`
	Address         string           `json:"address"`
	ChainKey        string           `json:"chain_key"`
	ChainID         int64            `json:"chain_id"`
	Range           RangeSpec        `json:"range"`
	Status          AddressStatus    `json:"status"`
	PauseRequested  bool             `json:"pause_requested,omitempty"`
	CancelRequested bool             `json:"cancel_requested,omitempty"`
	Progress        ProgressSnapshot `json:"progress"`
	CurrentDataset  string           `json:"current_dataset,omitempty"`
	CurrentProvider string           `json:"current_provider,omitempty"`
	CloudTier       string           `json:"cloud_tier,omitempty"`
	Error           string           `json:"error,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	StartedAt       *time.Time       `json:"started_at,omitempty"`
	FinishedAt      *time.Time       `json:"finished_at,omitempty"`
}

// DatasetJob 同一个地址的不同 Dataset 可使用不同 Provider。
type DatasetJob struct {
	ID                           string                      `json:"id"`
	BatchID                      string                      `json:"batch_id"`
	AddressJobID                 string                      `json:"address_job_id"`
	Address                      string                      `json:"address"`
	ChainKey                     string                      `json:"chain_key"`
	Dataset                      string                      `json:"dataset"`
	Status                       DatasetStatus               `json:"status"`
	CurrentProvider              string                      `json:"current_provider,omitempty"`
	PreferredProvider            string                      `json:"preferred_provider,omitempty"`
	EstimatedRows                uint64                      `json:"estimated_rows,omitempty"`
	EstimatedBytes               uint64                      `json:"estimated_bytes,omitempty"`
	DownloadedRows               uint64                      `json:"downloaded_rows"`
	ValidatedRows                uint64                      `json:"validated_rows,omitempty"`
	RequestedRange               RangeSpec                   `json:"requested_range"`
	PauseRequested               bool                        `json:"pause_requested,omitempty"`
	CancelRequested              bool                        `json:"cancel_requested,omitempty"`
	Progress                     ProgressSnapshot            `json:"progress"`
	Validation                   *ValidationReport           `json:"validation,omitempty"`
	RepairRounds                 int                         `json:"repair_rounds,omitempty"`
	CloudTier                    string                      `json:"cloud_tier,omitempty"`
	CloudScore                   float64                     `json:"cloud_score,omitempty"`
	CloudReasons                 []string                    `json:"cloud_reasons,omitempty"`
	CloudEstimatedCost           float64                     `json:"cloud_estimated_cost,omitempty"`
	CloudEstimatedRuntimeSeconds float64                     `json:"cloud_estimated_runtime_seconds,omitempty"`
	DiscoveryConfidence          float64                     `json:"discovery_confidence,omitempty"`
	SuggestedRangeSpan           uint64                      `json:"suggested_range_span,omitempty"`
	ActivitySegments             []discovery.ActivitySegment `json:"activity_segments,omitempty"`
	Certification                CertificationLevel          `json:"certification,omitempty"`
	WarehouseStatus              string                      `json:"warehouse_status,omitempty"`
	WarehouseError               string                      `json:"warehouse_error,omitempty"`
	RelevantCertified            bool                        `json:"relevant_certified,omitempty"`
	RelevantCertifiedAt          *time.Time                  `json:"relevant_certified_at,omitempty"`
	Error                        string                      `json:"error,omitempty"`
	CreatedAt                    time.Time                   `json:"created_at"`
	UpdatedAt                    time.Time                   `json:"updated_at"`
	StartedAt                    *time.Time                  `json:"started_at,omitempty"`
	FinishedAt                   *time.Time                  `json:"finished_at,omitempty"`
}

// RangeJob Provider 切换和断点续传的最小单位。
type RangeJob struct {
	ID              string        `json:"id"`
	SharedWorkID    string        `json:"shared_work_id,omitempty"`
	DatasetJobID    string        `json:"dataset_job_id"`
	BatchID         string        `json:"batch_id"`
	AddressJobID    string        `json:"address_job_id"`
	Address         string        `json:"address"`
	Dataset         string        `json:"dataset"`
	FromBlock       uint64        `json:"from_block"`
	ToBlock         uint64        `json:"to_block"`
	Provider        string        `json:"provider,omitempty"`
	Owner           RangeOwner    `json:"owner,omitempty"`
	Lane            string        `json:"lane,omitempty"`
	Priority        int           `json:"priority,omitempty"`
	PriorityClass   RangePriority `json:"priority_class,omitempty"`
	Relevant        bool          `json:"relevant,omitempty"`
	ExpectedRows    uint64        `json:"expected_rows,omitempty"`
	ETASeconds      float64       `json:"eta_seconds,omitempty"`
	Certified       bool          `json:"certified,omitempty"`
	CertifiedAt     *time.Time    `json:"certified_at,omitempty"`
	ParentRangeID   string        `json:"parent_range_id,omitempty"`
	ReshardDepth    int           `json:"reshard_depth,omitempty"`
	HedgeOf         string        `json:"hedge_of,omitempty"`
	HedgeWinner     bool          `json:"hedge_winner,omitempty"`
	FailedProviders []string      `json:"failed_providers,omitempty"`
	Purpose         string        `json:"purpose,omitempty"` // PRIMARY/REPAIR/VERIFY（Validation V3）
	Status          RangeStatus   `json:"status"`
	RowsCommitted   uint64        `json:"rows_committed"`
	Bytes           uint64        `json:"bytes"`
	Attempts        int           `json:"attempts"`
	Error           string        `json:"error,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
	StartedAt       *time.Time    `json:"started_at,omitempty"`
	FinishedAt      *time.Time    `json:"finished_at,omitempty"`
}

// CreateBatchRequest 创建批量任务的 API 请求（规格 §34 简化版）。
type CreateBatchRequest struct {
	ChainKey              string               `json:"chain_key"`
	Mode                  DownloadMode         `json:"mode,omitempty"`
	Priority              JobPriority          `json:"priority,omitempty"`
	ResourceProfile       ResourceProfile      `json:"resource_profile,omitempty"`
	Addresses             []string             `json:"addresses"`
	Datasets              []string             `json:"datasets"`
	DefaultRange          *RangeSpec           `json:"default_range,omitempty"`
	AddressOverrides      map[string]RangeSpec `json:"address_overrides,omitempty"`
	AddressChainOverrides map[string]string    `json:"address_chain_overrides,omitempty"`
	SkipCovered           *bool                `json:"skip_covered,omitempty"`
	Prefetch              bool                 `json:"prefetch,omitempty"`
	PrefetchPriority      int                  `json:"prefetch_priority,omitempty"`
	// RelevantRanges are prioritized ahead of historical coverage and feed TTFR.
	// RelevantByAddress overrides the batch-wide list for a specific address.
	RelevantRanges    []RangeSpec            `json:"relevant_ranges,omitempty"`
	RelevantRange     *RangeSpec             `json:"relevant_range,omitempty"`
	RelevantByAddress map[string][]RangeSpec `json:"relevant_ranges_by_address,omitempty"`
	EmergencyBurst    bool                   `json:"emergency_burst,omitempty"`
	BurstLevel        string                 `json:"burst_level,omitempty"`
}

// TurboStatus is an auditable, coverage-based snapshot of Turbo execution.
type TurboStatus struct {
	BatchID                   string       `json:"batch_id"`
	Mode                      DownloadMode `json:"mode"`
	Priority                  JobPriority  `json:"priority,omitempty"`
	CloudRanges               int          `json:"cloud_ranges"`
	RPCRanges                 int          `json:"rpc_ranges"`
	PendingRanges             int          `json:"pending_ranges"`
	RunningRanges             int          `json:"running_ranges"`
	CompletedRanges           int          `json:"completed_ranges"`
	FailedRanges              int          `json:"failed_ranges"`
	CoveredBlocks             uint64       `json:"covered_blocks"`
	TotalBlocks               uint64       `json:"total_blocks"`
	CoveragePercent           float64      `json:"coverage_percent"`
	RowsPerSecond             float64      `json:"rows_per_second,omitempty"`
	ETASeconds                float64      `json:"eta_seconds,omitempty"`
	TimeToFirstDataSecs       float64      `json:"time_to_first_data_seconds,omitempty"`
	TimeToFirstRelevantSecs   float64      `json:"time_to_first_relevant_seconds,omitempty"`
	RelevantRanges            int          `json:"relevant_ranges,omitempty"`
	RelevantCertifiedRanges   int          `json:"relevant_certified_ranges,omitempty"`
	RelevantCertification     string       `json:"relevant_certification,omitempty"`
	CloudRunning              int          `json:"cloud_running,omitempty"`
	RPCRunning                int          `json:"rpc_running,omitempty"`
	CloudRowsPerSecond        float64      `json:"cloud_rows_per_second,omitempty"`
	RPCRowsPerSecond          float64      `json:"rpc_rows_per_second,omitempty"`
	DownloadedRowsPerSecond   float64      `json:"downloaded_rows_per_second,omitempty"`
	ParsedRowsPerSecond       float64      `json:"parsed_rows_per_second,omitempty"`
	InsertedRowsPerSecond     float64      `json:"inserted_rows_per_second,omitempty"`
	ClickHouseInsertP95Millis float64      `json:"clickhouse_insert_p95_ms,omitempty"`
	ClickHouseMergeQueue      int          `json:"clickhouse_merge_queue,omitempty"`
	ClickHouseActiveParts     int          `json:"clickhouse_active_parts,omitempty"`
	Bottleneck                string       `json:"bottleneck,omitempty"`
	ClaimsLimit               int          `json:"claims_limit,omitempty"`
	CloudClaimsLimit          int          `json:"cloud_claims_limit,omitempty"`
	RPCClaimsLimit            int          `json:"rpc_claims_limit,omitempty"`
	CloudBurstLevel           string       `json:"cloud_burst_level,omitempty"`
	CloudBurstJobs            int          `json:"cloud_burst_jobs,omitempty"`
	CloudPausedByGovernor     bool         `json:"cloud_paused_by_governor,omitempty"`
	BurstActive               bool         `json:"burst_active,omitempty"`
	BackpressureActive        bool         `json:"backpressure_active,omitempty"`
	PreemptionActive          bool         `json:"preemption_active,omitempty"`
	WorkStealingActive        bool         `json:"work_stealing_active,omitempty"`
	ReshardActive             bool         `json:"reshard_active,omitempty"`
	HedgeActive               bool         `json:"hedge_active,omitempty"`
	AllocatorLastRunAt        *time.Time   `json:"allocator_last_run_at,omitempty"`
	CloudAvailable            bool         `json:"cloud_available"`
	RPCAvailable              bool         `json:"rpc_available"`
}

// CreateBatchResponse 创建结果。
type CreateBatchResponse struct {
	Batch            *BatchJob `json:"batch"`
	Valid            int       `json:"valid_addresses"`
	Invalid          []string  `json:"invalid_addresses,omitempty"`
	Duplicates       int       `json:"duplicates"`
	DatasetJobs      int       `json:"dataset_jobs"`
	RangeJobs        int       `json:"range_jobs"`
	LocalFullHits    int       `json:"local_full_hits"`
	LocalPartialHits int       `json:"local_partial_hits"`
	LocalMisses      int       `json:"local_misses"`
	ReusedRanges     int       `json:"reused_ranges"`
}

// ── 详情视图（API 返回用）──

// BatchDetail 批量任务详情（含地址/数据集/Range 树）。
type BatchDetail struct {
	Batch     *BatchJob        `json:"batch"`
	Addresses []*AddressDetail `json:"addresses"`
}

// AddressDetail 地址详情。
type AddressDetail struct {
	Address  *AddressJob      `json:"address"`
	Datasets []*DatasetDetail `json:"datasets"`
}

// DatasetDetail 数据集详情。
type DatasetDetail struct {
	Dataset *DatasetJob `json:"dataset"`
	Ranges  []*RangeJob `json:"ranges"`
}
