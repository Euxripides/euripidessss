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
	ID            string     `json:"id"`
	JobID         string     `json:"job_id,omitempty"` // TS Worker 兼容：与 ID 双写
	ChunkID       string     `json:"chunk_id,omitempty"`
	PlanID        string     `json:"plan_id,omitempty"`
	TaskID        string     `json:"task_id,omitempty"`
	ChainKey      string     `json:"chain_key"`
	ChainID       int        `json:"chain_id,omitempty"`
	Dataset       string     `json:"dataset,omitempty"`
	Addresses     []string   `json:"addresses"`
	TokenContract string     `json:"token_contract,omitempty"`
	FromBlock     uint64     `json:"from_block"`
	ToBlock       uint64     `json:"to_block"`
	Priority      int        `json:"priority,omitempty"`
	Attempt       int        `json:"attempt,omitempty"`
	Tier          string     `json:"tier,omitempty"` // Cloud S/L/XL（弹性调度 V1.0）
	Mode          Mode       `json:"mode"`
	State         string     `json:"state"` // queued/running/done/failed
	OutputDir     string     `json:"output_dir,omitempty"`
	Rows          int64      `json:"rows,omitempty"`
	Error         string     `json:"error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// Status Worker 与运行时状态快照（API /api/scheduler/cloud/runtime 展示）。
type Status struct {
	State                   WorkerState `json:"state"`
	Mode                    Mode        `json:"mode"`
	Reason                  string      `json:"reason,omitempty"`
	WorkerID                string      `json:"worker_id,omitempty"`
	QueuedJobs              int         `json:"queued_jobs"`
	LeasedJobs              int         `json:"leased_jobs"`
	RunningJob              string      `json:"running_job,omitempty"`
	CurrentChunk            string      `json:"current_chunk,omitempty"`
	RowsExported            int64       `json:"rows_exported,omitempty"`
	LastProgressAt          *time.Time  `json:"last_progress_at,omitempty"`
	DeploymentKeyConfigured bool        `json:"deployment_key_configured"`
	R2Configured            bool        `json:"r2_configured"`
	Available               bool        `json:"available"`
	FailureCooldownUntil    *time.Time  `json:"failure_cooldown_until,omitempty"`
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
