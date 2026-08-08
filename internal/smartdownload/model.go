// Package smartdownload 实现智能下载统一入口的第一阶段任务层：
// BatchJob → AddressJob → DatasetJob → RangeJob 四层模型、FS 状态存储、
// Universal Checkpoint V3、Range Ledger、Recovery、Pause/Resume/Cancel。
//
// 设计依据：智能下载系统_从V2.2到完整实现_实施方案_V1.1（Phase 1）。
// 本阶段不重写下载器：ProviderAdapter 只负责“把某个 Range 的原始数据拿回来”。
package smartdownload

import (
	"time"
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
)

var validDatasets = map[string]bool{
	DatasetTransactions:         true,
	DatasetInternalTransactions: true,
	DatasetTokenTransfers:       true,
	DatasetLogs:                 true,
	DatasetBalances:             true,
	DatasetTokenMetadata:        true,
	DatasetNFTTransfers:         true,
}

// ValidDataset 校验数据集类型。
func ValidDataset(name string) bool {
	return validDatasets[name]
}

// ── 状态机（Phase 1 最小集；Phase 2 增加 SWITCHING_PROVIDER/VALIDATING）──

type BatchStatus string

const (
	BatchCreated   BatchStatus = "CREATED"
	BatchRunning   BatchStatus = "RUNNING"
	BatchPaused    BatchStatus = "PAUSED"
	BatchCompleted BatchStatus = "COMPLETED"
	BatchPartial   BatchStatus = "PARTIAL"
	BatchFailed    BatchStatus = "FAILED"
	BatchCanceled  BatchStatus = "CANCELED"
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
	DatasetCompleted  DatasetStatus = "COMPLETED"
	DatasetPartial    DatasetStatus = "PARTIAL"
	DatasetFailed     DatasetStatus = "FAILED"
	DatasetCanceled   DatasetStatus = "CANCELED"
)

func (s DatasetStatus) Terminal() bool {
	return s == DatasetCompleted || s == DatasetPartial || s == DatasetFailed || s == DatasetCanceled
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
	Percent         float64 `json:"percent"`
	RowsCurrent     uint64  `json:"rows_current"`
	RowsTotal       uint64  `json:"rows_total"`
	BlocksCurrent   uint64  `json:"blocks_current"`
	BlocksTotal     uint64  `json:"blocks_total"`
	BytesCurrent    uint64  `json:"bytes_current,omitempty"`
	BytesTotal      uint64  `json:"bytes_total,omitempty"`
	SpeedRowsPerSec float64 `json:"speed_rows_per_sec,omitempty"`
	ETASeconds      float64 `json:"eta_seconds,omitempty"`
	ETAConfidence   float64 `json:"eta_confidence,omitempty"`
}

// ── 四层任务模型 ──

// BatchJob 一次用户提交。
type BatchJob struct {
	ID              string      `json:"id"`
	ChainKey        string      `json:"chain_key"`
	ChainID         int64       `json:"chain_id"`
	Status          BatchStatus `json:"status"`
	AddressCount    int         `json:"address_count"`
	DatasetTypes    []string    `json:"dataset_types"`
	PauseRequested  bool        `json:"pause_requested,omitempty"`
	CancelRequested bool        `json:"cancel_requested,omitempty"`
	Error           string      `json:"error,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
	StartedAt       *time.Time  `json:"started_at,omitempty"`
	FinishedAt      *time.Time  `json:"finished_at,omitempty"`
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
	Error           string           `json:"error,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	StartedAt       *time.Time       `json:"started_at,omitempty"`
	FinishedAt      *time.Time       `json:"finished_at,omitempty"`
}

// DatasetJob 同一个地址的不同 Dataset 可使用不同 Provider。
type DatasetJob struct {
	ID                string            `json:"id"`
	BatchID           string            `json:"batch_id"`
	AddressJobID      string            `json:"address_job_id"`
	Address           string            `json:"address"`
	ChainKey          string            `json:"chain_key"`
	Dataset           string            `json:"dataset"`
	Status            DatasetStatus     `json:"status"`
	CurrentProvider   string            `json:"current_provider,omitempty"`
	PreferredProvider string            `json:"preferred_provider,omitempty"`
	EstimatedRows     uint64            `json:"estimated_rows,omitempty"`
	EstimatedBytes    uint64            `json:"estimated_bytes,omitempty"`
	DownloadedRows    uint64            `json:"downloaded_rows"`
	ValidatedRows     uint64            `json:"validated_rows,omitempty"`
	RequestedRange    RangeSpec         `json:"requested_range"`
	PauseRequested    bool              `json:"pause_requested,omitempty"`
	CancelRequested   bool              `json:"cancel_requested,omitempty"`
	Progress          ProgressSnapshot  `json:"progress"`
	Validation        *ValidationReport `json:"validation,omitempty"`
	RepairRounds      int               `json:"repair_rounds,omitempty"`
	Error             string            `json:"error,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
	StartedAt         *time.Time        `json:"started_at,omitempty"`
	FinishedAt        *time.Time        `json:"finished_at,omitempty"`
}

// RangeJob Provider 切换和断点续传的最小单位。
type RangeJob struct {
	ID              string      `json:"id"`
	DatasetJobID    string      `json:"dataset_job_id"`
	BatchID         string      `json:"batch_id"`
	AddressJobID    string      `json:"address_job_id"`
	Address         string      `json:"address"`
	Dataset         string      `json:"dataset"`
	FromBlock       uint64      `json:"from_block"`
	ToBlock         uint64      `json:"to_block"`
	Provider        string      `json:"provider,omitempty"`
	FailedProviders []string    `json:"failed_providers,omitempty"`
	Status          RangeStatus `json:"status"`
	RowsCommitted   uint64      `json:"rows_committed"`
	Bytes           uint64      `json:"bytes"`
	Attempts        int         `json:"attempts"`
	Error           string      `json:"error,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
	StartedAt       *time.Time  `json:"started_at,omitempty"`
	FinishedAt      *time.Time  `json:"finished_at,omitempty"`
}

// CreateBatchRequest 创建批量任务的 API 请求（规格 §34 简化版）。
type CreateBatchRequest struct {
	ChainKey              string               `json:"chain_key"`
	Addresses             []string             `json:"addresses"`
	Datasets              []string             `json:"datasets"`
	DefaultRange          *RangeSpec           `json:"default_range,omitempty"`
	AddressOverrides      map[string]RangeSpec `json:"address_overrides,omitempty"`
	AddressChainOverrides map[string]string    `json:"address_chain_overrides,omitempty"`
	SkipCovered           *bool                `json:"skip_covered,omitempty"`
}

// CreateBatchResponse 创建结果。
type CreateBatchResponse struct {
	Batch       *BatchJob `json:"batch"`
	Valid       int       `json:"valid_addresses"`
	Invalid     []string  `json:"invalid_addresses,omitempty"`
	Duplicates  int       `json:"duplicates"`
	DatasetJobs int       `json:"dataset_jobs"`
	RangeJobs   int       `json:"range_jobs"`
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
