// Package cloudruntime 实现 SQD Cloud 应急 Worker 的运行时管理（设计 V3.0 §27-31 + Phase 4）：
// Worker 状态机、单实例 Deploy 锁、Job Queue（pending/leased/completed/failed）、
// Lease/Heartbeat/Checkpoint/Manifest/_SUCCESS 协议、空闲自动回收与失败冷却。
//
// 运行模式：
//   - local：本机 Processor 模式（E:\Code\Processor-only，env 参数驱动，带 TO_BLOCK 有界执行）
//   - cloud：Squid Cloud 模式（需要 SQD_DEPLOY_KEY + R2/S3；Worker 轮询 R2 Job Queue）
//   - mock：测试/故障注入模式（RunJob 回调驱动，不启动真实进程）
package cloudruntime

import (
	"context"
	"time"

	"github.com/etl/backend/internal/s3store"
)

// Mode 运行时模式。
type Mode string

const (
	ModeAuto  Mode = "auto"  // 有部署密钥 → cloud；否则 local（Worker 项目存在时）
	ModeLocal Mode = "local" // 本机 Processor（开发/验证）
	ModeCloud Mode = "cloud" // Squid Cloud（生产，需密钥）
	ModeMock  Mode = "mock"  // 测试/故障注入
	ModeNone  Mode = "none"  // 未启用
)

// WorkerState Worker 生命周期状态（设计 §29）。
type WorkerState string

const (
	WorkerAbsent        WorkerState = "ABSENT"
	WorkerDeploying     WorkerState = "DEPLOYING"
	WorkerStarting      WorkerState = "STARTING"
	WorkerReady         WorkerState = "READY"
	WorkerBusy          WorkerState = "BUSY"
	WorkerIdle          WorkerState = "IDLE"
	WorkerDegraded      WorkerState = "DEGRADED"
	WorkerFailed        WorkerState = "FAILED"
	WorkerRemoving      WorkerState = "REMOVING"
	WorkerNotConfigured WorkerState = "NOT_CONFIGURED"
)

// Job Cloud 应急任务（Phase 4：pending/<job>/<chunk>/request.json）。
// V1 仅支持 BSC Token Transfer；TokenContract 缺省为 BSC USDT。
type Job struct {
	ID                       string          `json:"id"`
	JobID                    string          `json:"job_id,omitempty"` // TS Worker 兼容：与 ID 双写
	ChunkID                  string          `json:"chunk_id,omitempty"`
	PlanID                   string          `json:"plan_id,omitempty"`
	TaskID                   string          `json:"task_id,omitempty"`
	ChainKey                 string          `json:"chain_key"`
	ChainID                  int             `json:"chain_id,omitempty"`
	Dataset                  string          `json:"dataset,omitempty"`
	Addresses                []string        `json:"addresses"`
	TokenContract            string          `json:"token_contract,omitempty"`
	FromBlock                uint64          `json:"from_block"`
	ToBlock                  uint64          `json:"to_block"`
	Priority                 int             `json:"priority,omitempty"`
	Attempt                  int             `json:"attempt,omitempty"`
	Tier                     string          `json:"tier,omitempty"` // Cloud S/L/XL（弹性调度 V1.0）
	Mode                     Mode            `json:"mode"`
	State                    string          `json:"state"` // queued/running/done/failed
	OutputDir                string          `json:"output_dir,omitempty"`
	Rows                     int64           `json:"rows,omitempty"`
	Error                    string          `json:"error,omitempty"`
	CreatedAt                time.Time       `json:"created_at"`
	StartedAt                *time.Time      `json:"started_at,omitempty"`
	FinishedAt               *time.Time      `json:"finished_at,omitempty"`
	Stage                    string          `json:"stage,omitempty"`
	QueuedAt                 *time.Time      `json:"queued_at"`
	ClaimStartedAt           *time.Time      `json:"claim_started_at,omitempty"`
	LeaseAcquiredAt          *time.Time      `json:"lease_acquired_at,omitempty"`
	ClaimedAt                *time.Time      `json:"claimed_at,omitempty"`
	LeasedAt                 *time.Time      `json:"leased_at"`
	WorkerStartedAt          *time.Time      `json:"worker_started_at"`
	SQDQueryStartedAt        *time.Time      `json:"sqd_query_started_at"`
	SQDFirstByteAt           *time.Time      `json:"sqd_first_byte_at"`
	SQDQueryFinishedAt       *time.Time      `json:"sqd_query_finished_at"`
	ParquetStartedAt         *time.Time      `json:"parquet_started_at"`
	ParquetFinishedAt        *time.Time      `json:"parquet_finished_at"`
	R2UploadStartedAt        *time.Time      `json:"r2_upload_started_at"`
	R2UploadFinishedAt       *time.Time      `json:"r2_upload_finished_at"`
	ManifestStartedAt        *time.Time      `json:"manifest_started_at"`
	ManifestFinishedAt       *time.Time      `json:"manifest_finished_at"`
	LocalSyncStartedAt       *time.Time      `json:"local_sync_started_at"`
	LocalSyncFinishedAt      *time.Time      `json:"local_sync_finished_at"`
	ValidationStartedAt      *time.Time      `json:"validation_started_at"`
	ValidationFinishedAt     *time.Time      `json:"validation_finished_at"`
	CoverageCommitStartedAt  *time.Time      `json:"coverage_commit_started_at"`
	CoverageCommitFinishedAt *time.Time      `json:"coverage_commit_finished_at"`
	CompletedAt              *time.Time      `json:"completed_at"`
	FailedAt                 *time.Time      `json:"failed_at"`
	Durations                *JobDurations   `json:"durations,omitempty"`
	Diagnostics              *JobDiagnostics `json:"diagnostics,omitempty"`
	QueuePollMS              *int64          `json:"queue_poll_ms,omitempty"`
	JobPayloadLoadMS         *int64          `json:"job_payload_load_ms,omitempty"`
	LeaseAcquireMS           *int64          `json:"lease_acquire_ms,omitempty"`
	LeaseWriteMS             *int64          `json:"lease_write_ms,omitempty"`
	ClaimMS                  *int64          `json:"claim_ms,omitempty"`
}

// JobDiagnostics contains optional Processor-owned observability evidence.
// The fields are deliberately additive so older manifests/status objects
// remain readable. Legacy stderr event counters are retained separately and
// must not be used as the denominator for physical request rates.
type JobDiagnostics struct {
	SQDRequestsTotal               int64   `json:"sqd_requests_total,omitempty"`
	SQDRequestsSuccessTotal        int64   `json:"sqd_requests_success_total,omitempty"`
	SQDRequestsFailedTotal         int64   `json:"sqd_requests_failed_total,omitempty"`
	SQDRequests503Total            int64   `json:"sqd_requests_503_total,omitempty"`
	SQDRequests429Total            int64   `json:"sqd_requests_429_total,omitempty"`
	SQDRequests3XXTotal            int64   `json:"sqd_requests_3xx_total,omitempty"`
	SQDRequests4XXOtherTotal       int64   `json:"sqd_requests_4xx_other_total,omitempty"`
	SQDRequests5XXOtherTotal       int64   `json:"sqd_requests_5xx_other_total,omitempty"`
	SQDRequestsHTTPOtherTotal      int64   `json:"sqd_requests_http_other_total,omitempty"`
	SQDRequestsTimeoutTotal        int64   `json:"sqd_requests_timeout_total,omitempty"`
	SQDRequestsNetworkErrorTotal   int64   `json:"sqd_requests_network_error_total,omitempty"`
	SQDRequestsDNSErrorTotal       int64   `json:"sqd_requests_dns_error_total,omitempty"`
	SQDRequestsCancelledTotal      int64   `json:"sqd_requests_cancelled_total,omitempty"`
	SQDRequestsRetryTotal          int64   `json:"sqd_requests_retry_total,omitempty"`
	SQDRequestsRetrySuccessTotal   int64   `json:"sqd_requests_retry_success_total,omitempty"`
	SQDRequestsRetryExhaustedTotal int64   `json:"sqd_requests_retry_exhausted_total,omitempty"`
	SQDLogicalQueriesTotal         int64   `json:"sqd_logical_queries_total,omitempty"`
	SQDLogicalQueriesSuccessTotal  int64   `json:"sqd_logical_queries_success_total,omitempty"`
	SQDLogicalQueriesFailedTotal   int64   `json:"sqd_logical_queries_failed_total,omitempty"`
	SQDRequestDurationMSSamples    []int64 `json:"sqd_request_duration_ms_samples,omitempty"`
	RequestDurationMSSamplesLegacy []int64 `json:"request_duration_ms_samples,omitempty"`
	SQDRetryBackoffMSTotal         int64   `json:"sqd_retry_backoff_ms_total,omitempty"`

	HTTP503Count int64 `json:"http_503_count,omitempty"`
	HTTP429Count int64 `json:"http_429_count,omitempty"`
	TimeoutCount int64 `json:"timeout_count,omitempty"`
	RetryCount   int64 `json:"retry_count,omitempty"`
}

// JobDurations contains only durations whose two endpoint timestamps are
// present in persisted Cloud evidence. Unknown durations remain null.
type JobDurations struct {
	QueueWaitMS       *int64 `json:"queue_wait_ms"`
	ClaimMS           *int64 `json:"claim_ms"`
	LeaseAcquireMS    *int64 `json:"lease_acquire_ms"`
	ClaimToFetchMS    *int64 `json:"claim_to_fetch_ms"`
	PreFetchPrepareMS *int64 `json:"pre_fetch_prepare_ms"`
	LeaseToStartMS    *int64 `json:"lease_to_start_ms"`
	SQDTTFAMS         *int64 `json:"sqd_ttfa_ms"`
	SQDScanMS         *int64 `json:"sqd_scan_ms"`
	ParquetWriteMS    *int64 `json:"parquet_write_ms"`
	R2UploadMS        *int64 `json:"r2_upload_ms"`
	ManifestMS        *int64 `json:"manifest_ms"`
	LocalSyncMS       *int64 `json:"local_sync_ms"`
	ValidationMS      *int64 `json:"validation_ms"`
	CoverageCommitMS  *int64 `json:"coverage_commit_ms"`
	TotalJobMS        *int64 `json:"total_job_ms"`
}

// WorkerSlotStatus is the Processor-owned heartbeat for one execution slot.
// Alive is derived by Manager from UpdatedAt; it is never trusted from the
// object payload itself.
type WorkerSlotStatus struct {
	WorkerID                 string     `json:"worker_id"`
	Slot                     int        `json:"slot"`
	State                    string     `json:"state"`
	JobID                    string     `json:"job_id,omitempty"`
	ChunkID                  string     `json:"chunk_id,omitempty"`
	ConfiguredConcurrency    int        `json:"configured_concurrency"`
	MaxSQDInflightRequests   int        `json:"max_sqd_inflight_requests"`
	UpdatedAt                time.Time  `json:"updated_at"`
	BusyStartedAt            *time.Time `json:"busy_started_at,omitempty"`
	CompletedJobs            int64      `json:"completed_jobs"`
	FailedJobs               int64      `json:"failed_jobs"`
	BusyMillisTotal          int64      `json:"busy_millis_total"`
	R2UploadWorkers          int        `json:"r2_upload_workers,omitempty"`
	R2UploadQueueCapacity    int        `json:"r2_upload_queue_capacity,omitempty"`
	R2UploadActive           int        `json:"r2_upload_active,omitempty"`
	R2UploadOutstanding      int        `json:"r2_upload_outstanding,omitempty"`
	IdleReason               string     `json:"idle_reason,omitempty"`
	WorkerIdleBetweenJobsMS  int64      `json:"worker_idle_between_jobs_ms,omitempty"`
	QueuePollMS              int64      `json:"queue_poll_ms,omitempty"`
	JobPayloadLoadMS         int64      `json:"job_payload_load_ms,omitempty"`
	LeaseAcquireMS           int64      `json:"lease_acquire_ms,omitempty"`
	LeaseWriteMS             int64      `json:"lease_write_ms,omitempty"`
	ClaimMS                  int64      `json:"claim_ms,omitempty"`
	QueueClaimTotal          int64      `json:"queue_claim_total,omitempty"`
	QueueClaimSuccessTotal   int64      `json:"queue_claim_success_total,omitempty"`
	QueueClaimEmptyTotal     int64      `json:"queue_claim_empty_total,omitempty"`
	QueueClaimFailedTotal    int64      `json:"queue_claim_failed_total,omitempty"`
	LeaseAcquireTotal        int64      `json:"lease_acquire_total,omitempty"`
	LeaseAcquireSuccessTotal int64      `json:"lease_acquire_success_total,omitempty"`
	LeaseAcquireFailedTotal  int64      `json:"lease_acquire_failed_total,omitempty"`
	Alive                    bool       `json:"alive"`
}

// Status Worker 与运行时状态快照（API /api/scheduler/cloud/runtime 展示）。
type Status struct {
	State                   WorkerState        `json:"state"`
	Mode                    Mode               `json:"mode"`
	Reason                  string             `json:"reason,omitempty"`
	WorkerID                string             `json:"worker_id,omitempty"`
	QueuedJobs              int                `json:"queued_jobs"`
	LeasedJobs              int                `json:"leased_jobs"`
	RunningJob              string             `json:"running_job,omitempty"`
	CurrentChunk            string             `json:"current_chunk,omitempty"`
	RowsExported            int64              `json:"rows_exported,omitempty"`
	LastProgressAt          *time.Time         `json:"last_progress_at,omitempty"`
	DeploymentKeyConfigured bool               `json:"deployment_key_configured"`
	R2Configured            bool               `json:"r2_configured"`
	Available               bool               `json:"available"`
	FailureCooldownUntil    *time.Time         `json:"failure_cooldown_until,omitempty"`
	ConfiguredConcurrency   int                `json:"configured_concurrency,omitempty"`
	WorkersTotal            int                `json:"workers_total,omitempty"`
	WorkersAlive            int                `json:"workers_alive,omitempty"`
	WorkersBusy             int                `json:"workers_busy,omitempty"`
	WorkersIdle             int                `json:"workers_idle,omitempty"`
	MaxSQDInflightRequests  int                `json:"max_sqd_inflight_requests,omitempty"`
	WorkerSlots             []WorkerSlotStatus `json:"worker_slots,omitempty"`
}

// ObjectStore Job Queue 对象存储接口（s3store 实现；local/mock 使用本地文件存储）。
type ObjectStore = s3store.ObjectStore

// Config 运行时配置。DeployKey / R2 Secret 只允许来自环境变量，绝不落盘/日志/回传。
type Config struct {
	Mode                   Mode
	WorkerProjectDir       string
	JobsRoot               string
	IdleRemoveAfter        time.Duration
	IdleReapInterval       time.Duration
	DeployTimeout          time.Duration
	RuntimeFailureCooldown time.Duration
	DeployKey              string
	Organization           string
	WorkerName             string
	WorkerSlot             string
	// Store 为 Job Queue 对象存储（cloud 模式必填；local/mock 缺省使用 JobsRoot/queue 本地存储）。
	Store ObjectStore
	// R2Configured 由装配层注入：true 表示 R2/S3 凭据已配置（本地文件存储回退时为 false）。
	R2Configured bool
	// CommandRunner 可注入命令执行器（Reconcile/测试）；nil 时使用 exec.Command。
	CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)
	// RunJob 为 mock/测试模式的任务执行回调；nil 时使用内置 local/cloud 后端。
	RunJob func(ctx context.Context, job *Job, outDir string) error
}

// DefaultConfig 返回 V1 推荐配置（设计 §82/§107 + Phase 4 §74）。
func DefaultConfig() Config {
	return Config{
		Mode:                   ModeAuto,
		WorkerProjectDir:       `E:\Code\Processor-only`,
		JobsRoot:               `E:\codex\bsc_analytics\sqd-cloud`,
		IdleRemoveAfter:        20 * time.Minute,
		IdleReapInterval:       30 * time.Second,
		DeployTimeout:          10 * time.Minute,
		RuntimeFailureCooldown: 15 * time.Minute,
		Organization:           "supreme",
		WorkerName:             "bsc-emergency-worker",
		WorkerSlot:             "v2",
	}
}
