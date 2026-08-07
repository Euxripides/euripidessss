// Package downloadengine — V2 企业级区块链数据下载引擎 领域模型
package downloadengine

import "time"

// ── Job 类型 ──

type JobType string

const (
	JobAddressSingle   JobType = "ADDRESS_SINGLE"
	JobAddressBatch    JobType = "ADDRESS_BATCH"
	JobToken           JobType = "TOKEN"
	JobContract        JobType = "CONTRACT"
	JobNFT             JobType = "NFT"
	JobDatasetRange    JobType = "DATASET_RANGE"
	JobFullHistory     JobType = "FULL_HISTORY"
	JobIncrementalSync JobType = "INCREMENTAL_SYNC"
	JobRepair          JobType = "REPAIR"
	JobReindex         JobType = "REINDEX"
)

// ── 双层状态模型：JobStatus(生命周期) + JobStage(处理阶段) ──

// JobStatus 表示任务生命周期终态/运行态。只有 Transition() 方法可以修改。
type JobStatus string

const (
	StatusCreated    JobStatus = "CREATED"
	StatusValidating JobStatus = "VALIDATING"
	StatusQueued     JobStatus = "QUEUED"
	StatusRunning    JobStatus = "RUNNING"
	StatusPausing    JobStatus = "PAUSING"
	StatusPaused     JobStatus = "PAUSED"
	StatusCanceling  JobStatus = "CANCELING"
	StatusCancelled  JobStatus = "CANCELLED"
	StatusCompleted  JobStatus = "COMPLETED"
	StatusFailed     JobStatus = "FAILED"
)

// JobStage 表示当前处理阶段，不表示生命周期。
type JobStage string

const (
	StageIdle             JobStage = "IDLE"
	StageDiscovering      JobStage = "DISCOVERING"
	StageResolvingRange   JobStage = "RESOLVING_RANGE"
	StagePlanning         JobStage = "PLANNING"
	StageAwaitingSchedule JobStage = "AWAITING_SCHEDULE"
	StageDownloading      JobStage = "DOWNLOADING"
	StageWriting          JobStage = "WRITING"
	StageIndexing         JobStage = "INDEXING"
	StageValidatingOutput JobStage = "VALIDATING_OUTPUT"
	StageFinalizing       JobStage = "FINALIZING"
)

// ── 范围模式 ──

type RangeMode string

const (
	RangeAutoFirstSeen RangeMode = "AUTO_FIRST_SEEN"
	RangeTime          RangeMode = "TIME_RANGE"
	RangeBlock         RangeMode = "BLOCK_RANGE"
	RangeFullHistory   RangeMode = "FULL_HISTORY"
	RangeResume        RangeMode = "RESUME"
	RangeIncremental   RangeMode = "INCREMENTAL"
)

// ── 优先级 ──

type Priority int

const (
	PriorityCritical   Priority = 0
	PriorityHigh       Priority = 1
	PriorityNormal     Priority = 2
	PriorityLow        Priority = 3
	PriorityBackground Priority = 4
)

// ── 统一错误码 ──

type ErrorCode string

const (
	ErrInvalidRequest        ErrorCode = "INVALID_REQUEST"
	ErrInvalidAddress        ErrorCode = "INVALID_ADDRESS"
	ErrUnsupportedChain      ErrorCode = "UNSUPPORTED_CHAIN"
	ErrUnsupportedDataset    ErrorCode = "UNSUPPORTED_DATASET"
	ErrFirstSeenNotFound     ErrorCode = "FIRST_SEEN_NOT_FOUND"
	ErrFirstSeenPartial      ErrorCode = "FIRST_SEEN_PARTIAL"
	ErrSQDNoWorkers          ErrorCode = "SQD_NO_AVAILABLE_WORKERS"
	ErrSQDRateLimited        ErrorCode = "SQD_RATE_LIMITED"
	ErrSQDCircuitOpen        ErrorCode = "SQD_CIRCUIT_OPEN"
	ErrAWSFileNotFound       ErrorCode = "AWS_FILE_NOT_FOUND"
	ErrRPCUnavailable        ErrorCode = "RPC_UNAVAILABLE"
	ErrStoragePathInvalid    ErrorCode = "STORAGE_PATH_INVALID"
	ErrDiskSpaceInsufficient ErrorCode = "DISK_SPACE_INSUFFICIENT"
	ErrCheckpointCorrupted   ErrorCode = "CHECKPOINT_CORRUPTED"
	ErrParquetWriteFailed    ErrorCode = "PARQUET_WRITE_FAILED"
	ErrManifestInconsistent  ErrorCode = "MANIFEST_INCONSISTENT"
	ErrDuckDBIndexFailed     ErrorCode = "DUCKDB_INDEX_FAILED"
	ErrValidationFailed      ErrorCode = "VALIDATION_FAILED"
	ErrJobCancelled          ErrorCode = "JOB_CANCELLED"
	ErrStartTimeRequired     ErrorCode = "START_TIME_REQUIRED"
	ErrEndTimeInFuture       ErrorCode = "END_TIME_IN_FUTURE"
	ErrDateRangeInvalid      ErrorCode = "DATE_RANGE_INVALID"
)

// ── Chunk ──

type ChunkStatus string

const (
	ChunkPending   ChunkStatus = "PENDING"
	ChunkQueued    ChunkStatus = "QUEUED"
	ChunkRunning   ChunkStatus = "RUNNING"
	ChunkRetryWait ChunkStatus = "RETRY_WAIT"
	ChunkSucceeded ChunkStatus = "SUCCEEDED"
	ChunkFailed    ChunkStatus = "FAILED"
	ChunkSkipped   ChunkStatus = "SKIPPED"
	ChunkCancelled ChunkStatus = "CANCELLED"
)

type Chunk struct {
	ID             string      `json:"chunk_id"`
	JobID          string      `json:"job_id"`
	ChainID        string      `json:"chain_id"`
	AddressGroupID string      `json:"address_group_id"`
	DatasetType    string      `json:"dataset_type"`
	StartBlock     uint64      `json:"start_block"`
	EndBlock       uint64      `json:"end_block"`
	Provider       string      `json:"provider"`
	Attempt        int         `json:"attempt"`
	Status         ChunkStatus `json:"status"`
	RowsWritten    int64       `json:"rows_written"`
	BytesWritten   int64       `json:"bytes_written"`
	Checksum       string      `json:"checksum,omitempty"`
	StartedAt      *time.Time  `json:"started_at,omitempty"`
	CompletedAt    *time.Time  `json:"completed_at,omitempty"`
	ErrorCode      ErrorCode   `json:"error_code,omitempty"`
	ErrorMessage   string      `json:"error_message,omitempty"`
}

// ── 地址发现结果 ──

type FirstSeenStatusV2 string

const (
	FSFound                  FirstSeenStatusV2 = "FOUND"
	FSPartial                FirstSeenStatusV2 = "PARTIAL"
	FSNotFound               FirstSeenStatusV2 = "NOT_FOUND"
	FSTemporarilyUnavailable FirstSeenStatusV2 = "TEMPORARILY_UNAVAILABLE"
	FSFailed                 FirstSeenStatusV2 = "FAILED"
)

type CoverageStatusV2 string

const (
	CoverageV2Full    CoverageStatusV2 = "FULL"
	CoverageV2Partial CoverageStatusV2 = "PARTIAL"
	CoverageV2Unknown CoverageStatusV2 = "UNKNOWN"
)

type AddressDiscovery struct {
	Address         string            `json:"address"`
	FirstSeenBlock  *uint64           `json:"first_seen_block,omitempty"`
	FirstSeenTime   *string           `json:"first_seen_time,omitempty"`
	FirstSeenSource string            `json:"first_seen_source,omitempty"`
	Status          FirstSeenStatusV2 `json:"status"`
	Coverage        CoverageStatusV2  `json:"coverage"`
}

type DiscoveryResult struct {
	Total                  int                `json:"total"`
	Found                  int                `json:"found"`
	Partial                int                `json:"partial"`
	NotFound               int                `json:"not_found"`
	TemporarilyUnavailable int                `json:"temporarily_unavailable"`
	Failed                 int                `json:"failed"`
	Items                  []AddressDiscovery `json:"items,omitempty"`
}

// ── 有效范围 ──

type EffectiveRange struct {
	StartBlock     uint64 `json:"start_block"`
	EndBlock       uint64 `json:"end_block"`
	StartTime      string `json:"start_time"`
	EndTime        string `json:"end_time"`
	RangeSource    string `json:"range_source"` // FIRST_SEEN / USER_SELECTED / INCREMENTAL
	CoverageStatus string `json:"coverage_status"`
}

// ── Provider 能力 ──

type ProviderCapabilities struct {
	Name              string   `json:"name"`
	SupportsStreaming bool     `json:"supports_streaming"`
	SupportsObject    bool     `json:"supports_object"`
	SupportsLookup    bool     `json:"supports_lookup"`
	DatasetTypes      []string `json:"dataset_types"`
	MaxBlockRange     uint64   `json:"max_block_range,omitempty"`
	SupportsResume    bool     `json:"supports_resume"`
}

// ── Provider 健康 ──

type ProviderHealthStatus string

const (
	ProviderHealthy     ProviderHealthStatus = "HEALTHY"
	ProviderDegraded    ProviderHealthStatus = "DEGRADED"
	ProviderNoWorker    ProviderHealthStatus = "NO_WORKER"
	ProviderRateLimited ProviderHealthStatus = "RATE_LIMITED"
	ProviderUnavailable ProviderHealthStatus = "UNAVAILABLE"
	ProviderRecovering  ProviderHealthStatus = "RECOVERING"
	ProviderDisabled    ProviderHealthStatus = "DISABLED"
)

type ProviderHealth struct {
	Name      string               `json:"name"`
	Status    ProviderHealthStatus `json:"status"`
	LatencyMs float64              `json:"latency_ms"`
	ErrorRate float64              `json:"error_rate"`
	LastCheck time.Time            `json:"last_check"`
}
