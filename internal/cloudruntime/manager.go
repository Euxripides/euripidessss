package cloudruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/logger"
	"github.com/etl/backend/internal/s3store"
	"github.com/google/uuid"
)

// Manager Cloud Worker 运行时管理器（设计 §27 + Phase 4 Job Queue）。
// 单进程内互斥锁 + 进程间 Deploy 锁文件保证单 Worker。
type Manager struct {
	mu          sync.Mutex
	cfg         Config
	store       ObjectStore
	state       WorkerState
	reason      string
	workerID    string
	jobs        map[string]*Job
	runningJob  string
	lastActive  time.Time
	cooldownEnd time.Time
	proc        *exec.Cmd
	procDone    chan error
	stop        chan struct{}
	loopStarted bool
	statePath   string
	jobsDir     string
}

// New 创建运行时管理器。
func New(cfg Config) *Manager {
	if cfg.IdleRemoveAfter <= 0 {
		cfg.IdleRemoveAfter = 20 * time.Minute
	}
	if cfg.IdleReapInterval <= 0 {
		cfg.IdleReapInterval = 30 * time.Second
	}
	if cfg.DeployTimeout <= 0 {
		cfg.DeployTimeout = 10 * time.Minute
	}
	if cfg.RuntimeFailureCooldown <= 0 {
		cfg.RuntimeFailureCooldown = 15 * time.Minute
	}
	if cfg.Organization == "" {
		cfg.Organization = "supreme"
	}
	if cfg.WorkerName == "" {
		cfg.WorkerName = "bsc-emergency-worker"
	}
	if cfg.WorkerSlot == "" {
		cfg.WorkerSlot = "v2"
	}
	if cfg.JobsRoot == "" {
		cfg.JobsRoot = DefaultConfig().JobsRoot
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeAuto
	}
	if cfg.Mode == ModeAuto {
		cfg.Mode = ModeNone
		if cfg.DeployKey != "" {
			cfg.Mode = ModeCloud
		} else if cfg.WorkerProjectDir != "" {
			if _, err := os.Stat(filepath.Join(cfg.WorkerProjectDir, "lib", "main.js")); err == nil {
				cfg.Mode = ModeLocal
			}
		}
	}
	if cfg.Store == nil && cfg.Mode != ModeCloud {
		// local/mock 使用本地文件存储模拟 R2 Job Queue；cloud 模式必须显式注入（R2/S3）。
		cfg.Store = s3store.NewLocalStore(filepath.Join(cfg.JobsRoot, "store"))
	}
	m := &Manager{
		cfg:       cfg,
		store:     cfg.Store,
		state:     WorkerAbsent,
		jobs:      make(map[string]*Job),
		stop:      make(chan struct{}),
		procDone:  make(chan error, 1),
		statePath: filepath.Join(cfg.JobsRoot, "runtime_state.json"),
		jobsDir:   filepath.Join(cfg.JobsRoot, "jobs"),
	}
	switch cfg.Mode {
	case ModeCloud:
		if cfg.DeployKey == "" {
			m.state = WorkerNotConfigured
			m.reason = "SQD Cloud 模式缺少 SQD_DEPLOY_KEY（密钥仅允许来自环境变量）"
		} else if cfg.Store == nil {
			m.state = WorkerNotConfigured
			m.reason = "SQD Cloud 模式缺少 R2/S3 Job Queue 配置（R2_ENDPOINT/R2_BUCKET/R2_ACCESS_KEY_ID/R2_SECRET_ACCESS_KEY）"
		}
	case ModeLocal:
		if _, err := os.Stat(filepath.Join(cfg.WorkerProjectDir, "lib", "main.js")); err != nil {
			m.state = WorkerNotConfigured
			m.reason = fmt.Sprintf("Cloud Worker 项目不可用: %v", err)
		}
	case ModeNone:
		m.state = WorkerNotConfigured
		m.reason = "SQD Cloud 未启用（设置 SQD_CLOUD_MODE=local/cloud 或 SQD_DEPLOY_KEY）"
	case ModeMock:
		// 测试模式：无需外部资源
	default:
		m.state = WorkerNotConfigured
		m.reason = "未知 SQD_CLOUD_MODE: " + string(cfg.Mode)
	}
	_ = os.MkdirAll(m.jobsDir, 0o755)
	m.loadState()
	if m.cfg.Mode == ModeCloud && m.state != WorkerNotConfigured {
		go m.idleReaperLoop()
		go m.leaseReaperLoop()
	}
	return m
}

// Status 返回运行时状态快照。
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

func (m *Manager) statusLocked() Status {
	pending, leased := m.queueCountsLocked()
	state := m.state
	// Cloud 模式的持久状态只表示 Worker 是否已经部署；真正的 READY/BUSY/IDLE
	// 必须由远端队列事实派生，不能在有 lease 时仍向前端报告 IDLE。
	if m.cfg.Mode == ModeCloud && (state == WorkerReady || state == WorkerBusy || state == WorkerIdle) {
		switch {
		case leased > 0 || m.runningJob != "":
			state = WorkerBusy
		case pending > 0:
			state = WorkerReady
		default:
			state = WorkerIdle
		}
	}
	st := Status{
		State:                   state,
		Mode:                    m.cfg.Mode,
		Reason:                  m.reason,
		WorkerID:                m.workerID,
		QueuedJobs:              pending,
		LeasedJobs:              leased,
		RunningJob:              m.runningJob,
		DeploymentKeyConfigured: m.cfg.DeployKey != "",
		R2Configured:            m.cfg.R2Configured,
	}
	if !m.lastActive.IsZero() {
		v := m.lastActive
		st.LastProgressAt = &v
	}
	if m.runningJob != "" {
		if j, ok := m.jobs[m.runningJob]; ok {
			st.CurrentChunk = j.ChunkID
			st.RowsExported = j.Rows
		}
	}
	if !m.cooldownEnd.IsZero() && time.Now().Before(m.cooldownEnd) {
		v := m.cooldownEnd
		st.FailureCooldownUntil = &v
	}
	switch st.State {
	case WorkerReady, WorkerBusy, WorkerIdle:
		st.Available = true
	}
	if !st.Available && m.state != WorkerNotConfigured && time.Now().Before(m.cooldownEnd) {
		cooldownReason := fmt.Sprintf("运行时失败冷却中（至 %s）", m.cooldownEnd.Format(time.RFC3339))
		if st.Reason == "" {
			st.Reason = cooldownReason
		} else {
			st.Reason += "；" + cooldownReason
		}
	}
	return st
}

// idleReaperLoop cloud 模式空闲回收（Phase 5 §37-38）：
// pending/leased/running 均为 0 且超过 IdleRemoveAfter 才 sqd remove；
// 队列计数读取失败（网络异常）时不删除，避免误删活跃 Worker。
func (m *Manager) idleReaperLoop() {
	for {
		select {
		case <-m.stop:
			return
		case <-time.After(m.cfg.IdleReapInterval):
		}
		pending, leased, err := m.queueCountsErr()
		if err != nil {
			logger.Log.Warn().Err(err).Msg("cloud_idle_reaper_queue_unknown_skip")
			continue
		}
		m.mu.Lock()
		running := m.runningJob != ""
		state := m.state
		lastActive := m.lastActive
		m.mu.Unlock()
		if pending == 0 && leased == 0 && !running && (state == WorkerIdle || state == WorkerReady) {
			if !lastActive.IsZero() && time.Since(lastActive) > m.cfg.IdleRemoveAfter {
				logger.Log.Info().Str("org", m.cfg.Organization).Str("worker", m.cfg.WorkerName).
					Msg("cloud_idle_reaper_removing")
				if err := m.RemoveWorker(context.Background()); err != nil {
					logger.Log.Warn().Err(err).Msg("cloud_idle_reaper_remove_failed")
				}
			}
		}
	}
}

// leaseReaperLoop cloud 模式 Lease 过期回收（Phase 5.2 §5/§7）：
// 过期 lease（heartbeat 停止）→ 清理 lease/status → 以同一 job_id 重新入队 pending
// → Worker 再次领取并从 checkpoint 恢复；completed 已存在时只做 tombstone。
func (m *Manager) leaseReaperLoop() {
	interval := m.cfg.IdleReapInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	for {
		select {
		case <-m.stop:
			return
		case <-time.After(interval):
		}
		logger.Log.Debug().Msg("cloud_lease_reaper_tick")
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Log.Error().Interface("panic", r).Msg("cloud_lease_reaper_panic")
				}
			}()
			if err := m.reclaimExpiredLeases(context.Background()); err != nil {
				logger.Log.Warn().Err(err).Msg("cloud_lease_reclaim_failed")
			}
		}()
	}
}

// reclaimExpiredLeases 扫描 leased/ 下过期 lease 并恢复 pending（Phase 5.2 §5/§8）。
func (m *Manager) reclaimExpiredLeases(ctx context.Context) error {
	if m.store == nil || m.cfg.Mode != ModeCloud {
		return nil
	}
	objs, err := m.store.List(ctx, queuePrefix+"leased/")
	if err != nil {
		return err
	}
	logger.Log.Debug().Int("leases", len(objs)).Msg("cloud_lease_reclaim_scan")
	for _, o := range objs {
		if !strings.HasSuffix(o.Key, "/lease.json") {
			continue
		}
		payload, err := m.store.Get(ctx, o.Key)
		if err != nil {
			continue
		}
		logger.Log.Debug().Str("key", o.Key).Msg("cloud_lease_reclaim_item")
		var lease struct {
			JobID          string `json:"job_id"`
			ChunkID        string `json:"chunk_id"`
			LeaseExpiresAt string `json:"lease_expires_at"`
		}
		if json.Unmarshal(payload, &lease) != nil || lease.JobID == "" {
			continue
		}
		expires, err := time.Parse(time.RFC3339Nano, lease.LeaseExpiresAt)
		if err != nil {
			expires, err = time.Parse(time.RFC3339, lease.LeaseExpiresAt)
		}
		if err != nil || time.Now().Before(expires) {
			continue // 未过期：Worker 仍在心跳
		}
		doneKey := completedChunkDir(lease.JobID, lease.ChunkID) + "/_SUCCESS"
		if ok, _ := m.store.Exists(ctx, doneKey); ok {
			// completed 已是终态：只清理残留 lease
			leased := leasedChunkDir(lease.JobID, lease.ChunkID)
			_ = m.store.Delete(ctx, leased+"/lease.json")
			_ = m.store.Delete(ctx, leased+"/status.json")
			continue
		}
		job := m.loadPersistedJob(lease.JobID, lease.ChunkID)
		logger.Log.Debug().Str("job", lease.JobID).Bool("job_found", job != nil).Msg("cloud_lease_reclaim_job")
		if job == nil {
			continue // 本地无该 Job 证据，不自行重建（避免幽灵任务）
		}
		leased := leasedChunkDir(lease.JobID, lease.ChunkID)
		_ = m.store.Delete(ctx, leased+"/lease.json")
		_ = m.store.Delete(ctx, leased+"/status.json")
		if err := m.enqueuePending(ctx, job); err != nil {
			logger.Log.Warn().Err(err).Str("job", lease.JobID).Msg("cloud_lease_requeue_failed")
			continue
		}
		marker, _ := json.MarshalIndent(map[string]any{
			"job_id": lease.JobID, "chunk_id": lease.ChunkID,
			"reason": "LEASE_EXPIRED", "requeued_at": time.Now().Format(time.RFC3339),
		}, "", "  ")
		_ = m.store.Put(ctx, requeuedPrefix+lease.JobID+"/"+lease.ChunkID+"/requeue.json", marker)
		logger.Log.Info().Str("job", lease.JobID).Str("chunk", lease.ChunkID).
			Msg("cloud_lease_expired_requeued")
	}
	logger.Log.Debug().Msg("cloud_lease_reclaim_done")
	return nil
}

// loadPersistedJob 从本地审计目录加载 Job（lease 恢复时重建 pending 用）。
func (m *Manager) loadPersistedJob(jobID, chunkID string) *Job {
	path := filepath.Join(m.jobsDir, jobID, chunkID, "status.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var job Job
	if json.Unmarshal(payload, &job) != nil {
		return nil
	}
	return &job
}

// CancelJob 写入 Cancel Marker（Phase 5.2 §6）：bsc/jobs/cancel/<job_id>.json。
func (m *Manager) CancelJob(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if err := validateQueueID("job id", id); err != nil {
		return err
	}
	if m.store == nil {
		return errors.New("Cloud Job Queue 未配置，无法取消任务")
	}
	job, err := m.JobStatus(id)
	if err != nil {
		return fmt.Errorf("拒绝取消未知 Cloud Job %q: %w", id, err)
	}
	switch job.State {
	case "done", "failed", "cancelled":
		return fmt.Errorf("Cloud Job %q 已是终态 %s，不能取消", id, job.State)
	}
	payload, _ := json.MarshalIndent(map[string]any{
		"job_id": id, "requested_at": time.Now().Format(time.RFC3339),
	}, "", "  ")
	if err := m.store.Put(ctx, cancelPrefix+id+".json", payload); err != nil {
		return err
	}
	m.mu.Lock()
	if job, ok := m.jobs[id]; ok {
		job.State = "cancelled"
		now := time.Now()
		job.FinishedAt = &now
	}
	m.mu.Unlock()
	logger.Log.Info().Str("job", id).Msg("cloud_cancel_marker_written")
	return nil
}

// EnsureWorker 确保 Worker 可用。
// local/mock：校验后就绪（任务由本地循环执行）；cloud：执行 sqd deploy（需要密钥）。
func (m *Manager) EnsureWorker(ctx context.Context) error {
	m.mu.Lock()
	if m.cfg.Mode == ModeNone || m.state == WorkerNotConfigured {
		err := errors.New(m.reason)
		m.mu.Unlock()
		return err
	}
	if time.Now().Before(m.cooldownEnd) {
		err := fmt.Errorf("Cloud 运行时失败冷却中（至 %s）", m.cooldownEnd.Format(time.RFC3339))
		m.mu.Unlock()
		return err
	}
	switch m.state {
	case WorkerReady, WorkerBusy, WorkerIdle, WorkerStarting:
		// Cloud 状态可能来自旧进程的持久化快照。真实 Submit 前必须用
		// sqd list 再次确认 Worker 存在；local/mock 则无需远端对账。
		if m.cfg.Mode == ModeCloud {
			break
		}
		m.mu.Unlock()
		return nil
	case WorkerDeploying:
		deadline := time.Now().Add(m.cfg.DeployTimeout)
		m.mu.Unlock()
		for time.Now().Before(deadline) {
			if err := ctx.Err(); err != nil {
				return err
			}
			time.Sleep(500 * time.Millisecond)
			m.mu.Lock()
			done := m.state == WorkerReady || m.state == WorkerBusy || m.state == WorkerIdle
			m.mu.Unlock()
			if done {
				return nil
			}
		}
		return errors.New("Cloud Worker 部署超时")
	}
	m.mu.Unlock()

	// Phase 5 §8：cloud 模式先查 sqd list，已存在托管 Worker 直接复用，禁止重复 Deploy。
	if m.cfg.Mode == ModeCloud {
		exists, listErr := m.cloudWorkerListed(ctx)
		if listErr != nil {
			m.markDegraded("sqd list 无法验证 Cloud Worker: " + listErr.Error())
			return fmt.Errorf("检查 SQD Cloud Worker 失败，未执行部署: %w", listErr)
		}
		if exists {
			m.mu.Lock()
			m.state = WorkerIdle
			m.reason = "Reconcile：复用已部署托管 Worker（" + m.cfg.WorkerName + "/" + m.cfg.WorkerSlot + "）"
			m.lastActive = time.Now()
			m.mu.Unlock()
			m.saveState()
			logger.Log.Info().Str("org", m.cfg.Organization).Str("worker", m.cfg.WorkerName).
				Msg("cloud_worker_reused_no_deploy")
			return nil
		}
	}

	if err := m.acquireDeployLock(ctx); err != nil {
		return err
	}
	defer m.releaseDeployLock()
	// 获得进程间锁后二次对账，避免另一进程已完成部署时重复付费部署。
	if m.cfg.Mode == ModeCloud {
		exists, listErr := m.cloudWorkerListed(ctx)
		if listErr != nil {
			m.markDegraded("获得部署锁后 sqd list 对账失败: " + listErr.Error())
			return fmt.Errorf("获得部署锁后检查 SQD Cloud Worker 失败: %w", listErr)
		}
		if exists {
			m.mu.Lock()
			m.state = WorkerIdle
			m.reason = "复用其他进程已部署的托管 Worker（" + m.cfg.WorkerName + "/" + m.cfg.WorkerSlot + "）"
			m.lastActive = time.Now()
			m.mu.Unlock()
			m.saveState()
			return nil
		}
	}

	m.mu.Lock()
	m.state = WorkerDeploying
	m.reason = ""
	m.workerID = "sqd-cloud-" + uuid.NewString()[:8]
	m.mu.Unlock()
	m.saveState()

	var deployErr error
	switch m.cfg.Mode {
	case ModeCloud:
		deployErr = m.deployCloudWorker(ctx)
		if deployErr == nil {
			deployErr = m.waitForCloudWorker(ctx)
		}
	case ModeLocal, ModeMock:
		deployErr = nil // 本机/模拟模式无需常驻部署，任务按 Job 启动
	}
	m.mu.Lock()
	if deployErr != nil {
		m.state = WorkerFailed
		m.reason = "Worker 部署失败: " + deployErr.Error()
		m.cooldownEnd = time.Now().Add(m.cfg.RuntimeFailureCooldown)
	} else {
		m.state = WorkerReady
		m.reason = "SQD Cloud Worker 已部署并通过 sqd list 对账"
		m.lastActive = time.Now()
	}
	m.mu.Unlock()
	m.saveState()
	return deployErr
}

// SubmitJob 提交一个 Cloud 应急任务（Phase 4：写入 pending 队列）。
// cloud 模式：EnsureWorker → enqueue（Worker 轮询 R2）；local/mock：enqueue 后本地循环执行。
func (m *Manager) SubmitJob(ctx context.Context, job Job) (string, error) {
	// 先完成路径相关字段校验，再触发任何外部部署，避免无效输入
	// 创建付费 Worker 或构造对象存储路径穿越。
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	// TS Worker 的 job_id 与队列路径 ID 必须一致，否则会把状态写到另一个任务目录。
	job.JobID = job.ID
	if job.ChunkID == "" {
		job.ChunkID = "chunk-1"
	}
	if err := validateQueueID("job id", job.ID); err != nil {
		return "", err
	}
	if err := validateQueueID("chunk id", job.ChunkID); err != nil {
		return "", err
	}
	if job.ToBlock < job.FromBlock {
		return "", fmt.Errorf("无效区块范围: from_block=%d to_block=%d", job.FromBlock, job.ToBlock)
	}

	m.mu.Lock()
	if m.cfg.Mode == ModeNone || m.state == WorkerNotConfigured {
		err := errors.New(m.reason)
		m.mu.Unlock()
		return "", err
	}
	if time.Now().Before(m.cooldownEnd) {
		err := fmt.Errorf("Cloud 运行时失败冷却中（至 %s）", m.cooldownEnd.Format(time.RFC3339))
		m.mu.Unlock()
		return "", err
	}
	m.mu.Unlock()

	if m.cfg.Mode == ModeCloud {
		if err := m.EnsureWorker(ctx); err != nil {
			return "", err
		}
	}
	if job.ChainKey == "" {
		job.ChainKey = "bsc"
	}
	if job.ChainID == 0 {
		job.ChainID = 56 // BSC（V1）
	}
	if job.Dataset == "" {
		job.Dataset = "token_transfer"
	}
	if job.TokenContract == "" && len(job.Addresses) == 0 {
		job.TokenContract = "0x55d398326f99059ff775485246999027b3197955" // BSC USDT（V1）
	}
	job.Mode = m.cfg.Mode
	job.State = "queued"
	job.CreatedAt = time.Now()
	if err := m.enqueuePending(ctx, &job); err != nil {
		return "", fmt.Errorf("Cloud Job 入队失败（worker=%s）: %w", m.cfg.WorkerName, err)
	}
	m.persistJob(&job) // Lease 过期恢复/审计依赖本地 Job 证据（Phase 5.2 §5/§7）
	m.mu.Lock()
	m.jobs[job.ID] = &job
	startLoop := !m.loopStarted && (m.cfg.Mode == ModeLocal || m.cfg.Mode == ModeMock)
	if startLoop {
		m.loopStarted = true
	}
	m.mu.Unlock()
	if startLoop {
		go m.workerLoop()
	}
	return job.ID, nil
}

// JobStatus 返回任务状态。cloud 模式从远端队列读取；local/mock 从内存读取（循环更新）。
func (m *Manager) JobStatus(id string) (Job, error) {
	id = strings.TrimSpace(id)
	if err := validateQueueID("job id", id); err != nil {
		return Job{}, err
	}
	if m.cfg.Mode == ModeCloud {
		remote, err := m.remoteJobStatus(context.Background(), id)
		if err != nil {
			return Job{}, err
		}
		m.mu.Lock()
		if existing, ok := m.jobs[id]; ok {
			existing.State = remote.State
			existing.Rows = remote.Rows
			existing.Error = remote.Error
			if remote.ChunkID != "" {
				existing.ChunkID = remote.ChunkID
			}
			if remote.StartedAt != nil {
				existing.StartedAt = remote.StartedAt
			}
			if remote.FinishedAt != nil {
				existing.FinishedAt = remote.FinishedAt
			}
			remote = *existing
		} else {
			copyJob := remote
			m.jobs[id] = &copyJob
		}
		m.mu.Unlock()
		return remote, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.jobs[id]; ok {
		return *j, nil
	}
	return Job{}, errors.New("Cloud 任务不存在: " + id)
}

// MaterializeJobResult downloads certified remote Cloud artifacts into the
// local job directory. Paths and SHA256 values come only from the completed
// manifest; traversal and checksum mismatches fail closed.
func (m *Manager) MaterializeJobResult(ctx context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	if err := validateQueueID("job id", id); err != nil {
		return "", err
	}
	job, err := m.remoteJobStatus(ctx, id)
	if err != nil {
		return "", err
	}
	if job.State != "done" || job.ChunkID == "" {
		return "", fmt.Errorf("Cloud 任务 %s 尚未完成", id)
	}
	completedDir := completedChunkDir(id, job.ChunkID)
	payload, err := m.store.Get(ctx, completedDir+"/manifest.json")
	if err != nil {
		return "", err
	}
	var manifest publishedManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return "", err
	}
	if err := m.validatePublishedManifest(ctx, manifest, id, job.ChunkID); err != nil {
		return "", err
	}
	destRoot := filepath.Join(m.jobsDir, id, job.ChunkID, "remote-result")
	stageRoot := destRoot + ".partial-" + uuid.NewString()
	if err := os.MkdirAll(stageRoot, 0o755); err != nil {
		return "", err
	}
	defer os.RemoveAll(stageRoot)
	for _, file := range manifest.Files {
		rel, err := validateArtifactPath(file.Path)
		if err != nil {
			return "", err
		}
		// The immutable completed manifest/_SUCCESS are the publication gate;
		// large parquet parts stay under the leased artifact prefix so completion
		// is an O(1) metadata commit instead of an object-store copy operation.
		body, err := m.store.Get(ctx, leasedChunkDir(id, job.ChunkID)+"/"+filepath.ToSlash(rel))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(body)
		if int64(len(body)) != file.Bytes {
			return "", fmt.Errorf("Cloud artifact 字节数不匹配: %s（manifest=%d actual=%d）", file.Path, file.Bytes, len(body))
		}
		if !strings.EqualFold(file.SHA256, hex.EncodeToString(sum[:])) {
			return "", fmt.Errorf("Cloud artifact SHA256 不匹配: %s", file.Path)
		}
		dest := filepath.Join(stageRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		tmp := dest + ".tmp"
		if err := os.WriteFile(tmp, body, 0o644); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, dest); err != nil {
			return "", err
		}
	}
	// 只有全部文件通过路径、大小和哈希校验后才发布；失败时不会留下一个
	// 看起来完整的 remote-result 目录。
	if err := os.RemoveAll(destRoot); err != nil {
		return "", err
	}
	if err := os.Rename(stageRoot, destRoot); err != nil {
		return "", err
	}
	return destRoot, nil
}

// Jobs 返回全部已知任务（新→旧）。
func (m *Manager) Jobs() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// RemoveWorker 移除 Worker（Phase 4 §38：pending/leased/running 均为 0 才允许）。
func (m *Manager) RemoveWorker(ctx context.Context) error {
	m.mu.Lock()
	if m.state == WorkerAbsent || m.state == WorkerNotConfigured {
		m.mu.Unlock()
		return nil
	}
	if m.runningJob != "" {
		m.mu.Unlock()
		return errors.New("存在运行中的 Cloud Job，禁止移除 Worker")
	}
	pending, leased := m.queueCountsLocked()
	if pending > 0 || leased > 0 {
		m.mu.Unlock()
		return fmt.Errorf("存在 pending(%d)/leased(%d) Job，禁止移除 Worker", pending, leased)
	}
	m.state = WorkerRemoving
	m.saveStateLocked()
	if m.proc != nil && m.proc.Process != nil {
		_ = m.proc.Process.Kill()
	}
	m.mu.Unlock()
	if m.cfg.Mode == ModeCloud {
		if err := m.removeCloudWorker(ctx); err != nil {
			m.mu.Lock()
			m.state = WorkerDegraded
			m.reason = "Cloud Worker 移除失败: " + err.Error()
			m.mu.Unlock()
			m.saveState()
			return err
		}
	}
	m.mu.Lock()
	m.state = WorkerAbsent
	m.workerID = ""
	m.reason = ""
	m.runningJob = ""
	m.mu.Unlock()
	m.saveState()
	return nil
}

// Close 停止后台循环并回收 Worker。
func (m *Manager) Close() {
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = m.RemoveWorker(ctx)
}

// workerLoop 本地/模拟模式串行消费 pending 队列（Phase 4：lease → run → upload → completed）。
func (m *Manager) workerLoop() {
	idleSince := time.Now()
	for {
		select {
		case <-m.stop:
			return
		case <-time.After(1 * time.Second):
		}
		ctx := context.Background()
		job, err := m.acquirePendingJob(ctx)
		if err != nil {
			logger.Log.Warn().Err(err).Msg("cloud_queue_acquire_failed")
			continue
		}
		if job == nil {
			// 空闲回收：无 pending/leased/running 且超过 IdleRemoveAfter
			m.mu.Lock()
			pending, leased := m.queueCountsLocked()
			if pending == 0 && leased == 0 && m.runningJob == "" && time.Since(idleSince) > m.cfg.IdleRemoveAfter {
				m.mu.Unlock()
				if err := m.RemoveWorker(ctx); err == nil {
					idleSince = time.Now()
				}
				continue
			}
			if m.runningJob == "" {
				idleSince = time.Now()
			}
			m.mu.Unlock()
			continue
		}

		now := time.Now()
		job.State = "running"
		job.StartedAt = &now
		m.mu.Lock()
		m.runningJob = job.ID
		m.state = WorkerBusy
		m.lastActive = now
		m.jobs[job.ID] = job
		m.mu.Unlock()
		m.persistJob(job)
		m.writeLeasedStatus(ctx, job, "RUNNING", 0, job.FromBlock)

		runErr := m.runJob(job)
		if runErr == nil {
			uploadErr := m.uploadAndComplete(ctx, job)
			if uploadErr != nil {
				runErr = uploadErr
			}
		}

		m.mu.Lock()
		finished := time.Now()
		job.FinishedAt = &finished
		if runErr != nil {
			job.State = "failed"
			job.Error = runErr.Error()
			m.state = WorkerFailed
			m.reason = "Cloud Job 执行失败: " + runErr.Error()
			m.cooldownEnd = time.Now().Add(m.cfg.RuntimeFailureCooldown)
			_ = m.writeFailedLocked(ctx, job, runErr)
			// 清理 leased 标记（证据保留在 failed/error.json + 本地日志）
			leased := leasedChunkDir(job.ID, job.ChunkID)
			_ = m.store.Delete(ctx, leased+"/status.json")
			_ = m.store.Delete(ctx, leased+"/lease.json")
		} else {
			job.State = "done"
			m.state = WorkerIdle
			m.reason = "Cloud Job 已完成，Worker 空闲"
			m.lastActive = finished
			leased := leasedChunkDir(job.ID, job.ChunkID)
			_ = m.store.Delete(ctx, leased+"/status.json")
			_ = m.store.Delete(ctx, leased+"/lease.json")
		}
		m.runningJob = ""
		idleSince = finished
		m.mu.Unlock()
		m.persistJob(job)
	}
}

// runJob 执行单个 Cloud Job（输出到本地临时目录，成功后上传）。
func (m *Manager) runJob(job *Job) error {
	outDir := filepath.Join(m.jobsDir, job.ID, job.ChunkID, "output")
	job.OutputDir = outDir
	_ = os.MkdirAll(outDir, 0o755)
	m.persistJob(job)
	if m.cfg.RunJob != nil {
		return m.cfg.RunJob(context.Background(), job, outDir)
	}
	switch m.cfg.Mode {
	case ModeLocal:
		return m.runLocalProcessor(job, outDir)
	case ModeMock:
		return writeJobSuccess(outDir, job, 0)
	default:
		return fmt.Errorf("模式 %s 不支持本地执行", m.cfg.Mode)
	}
}

// uploadAndComplete 上传 parquet 到 leased 目录，写 manifest/_SUCCESS（最后写），并登记 completed。
func (m *Manager) uploadAndComplete(ctx context.Context, job *Job) error {
	outDir := job.OutputDir
	rows, ok := successRows(outDir)
	if !ok {
		return fmt.Errorf("任务输出缺少 _SUCCESS/产物（%s）", outDir)
	}
	job.Rows = rows
	leasedDir := leasedChunkDir(job.ID, job.ChunkID)
	files, err := uploadParquetFiles(ctx, m.store, outDir, leasedDir)
	if err != nil {
		return err
	}
	manifest := buildManifest(job, files)
	manifestPayload, _ := json.MarshalIndent(manifest, "", "  ")
	successPayload, _ := json.MarshalIndent(map[string]any{
		"job_id": job.ID, "chunk_id": job.ChunkID, "rows": rows, "completed": true,
	}, "", "  ")
	// 写入顺序：checkpoint(final) → manifest → _SUCCESS（设计 §23）
	_ = m.writeLeasedStatus(ctx, job, "EXPORTING", rows, job.ToBlock)
	if err := m.store.Put(ctx, leasedDir+"/manifest.json", manifestPayload); err != nil {
		return err
	}
	if err := m.store.Put(ctx, leasedDir+"/_SUCCESS", successPayload); err != nil {
		return err
	}
	completedDir := completedChunkDir(job.ID, job.ChunkID)
	if err := m.store.Put(ctx, completedDir+"/manifest.json", manifestPayload); err != nil {
		return err
	}
	if err := m.store.Put(ctx, completedDir+"/_SUCCESS", successPayload); err != nil {
		return err
	}
	_ = m.writeLeasedStatus(ctx, job, "COMPLETED", rows, job.ToBlock)
	return nil
}

// acquirePendingJob 原子领取一个 pending Job（Lease + 防重复，Phase 4 §8-10）。
func (m *Manager) acquirePendingJob(ctx context.Context) (*Job, error) {
	objs, err := m.store.List(ctx, queuePrefix+"pending/")
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, o := range objs {
		if strings.HasSuffix(o.Key, "/request.json") {
			keys = append(keys, o.Key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		jobID, chunkID := parseChunkKey(key, "pending/")
		if jobID == "" {
			continue
		}
		// 幂等：completed 已有 _SUCCESS → 丢弃 pending
		doneKey := completedChunkDir(jobID, chunkID) + "/_SUCCESS"
		if ok, _ := m.store.Exists(ctx, doneKey); ok {
			_ = m.store.Delete(ctx, key)
			continue
		}
		payload, err := m.store.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		var job Job
		if err := json.Unmarshal(payload, &job); err != nil {
			return nil, fmt.Errorf("解析 pending request %s: %w", key, err)
		}
		// Lease：写 lease.json（若已存在则跳过，另一个 Worker 已领取）
		leaseKey := leasedChunkDir(jobID, chunkID) + "/lease.json"
		if ok, _ := m.store.Exists(ctx, leaseKey); ok {
			continue
		}
		lease := map[string]any{
			"job_id": jobID, "chunk_id": chunkID,
			"worker_id": m.workerID, "leased_at": time.Now().Format(time.RFC3339),
			"lease_expires_at": time.Now().Add(10 * time.Minute).Format(time.RFC3339),
			"heartbeat_at":     time.Now().Format(time.RFC3339),
		}
		leasePayload, _ := json.MarshalIndent(lease, "", "  ")
		if err := m.store.Put(ctx, leaseKey, leasePayload); err != nil {
			return nil, err
		}
		_ = m.store.Delete(ctx, key)
		return &job, nil
	}
	return nil, nil
}

type publishedManifest struct {
	SchemaVersion int        `json:"schema_version"`
	JobID         string     `json:"job_id"`
	ChunkID       string     `json:"chunk_id"`
	ChainID       int        `json:"chain_id"`
	Dataset       string     `json:"dataset"`
	FromBlock     uint64     `json:"from_block"`
	ToBlock       uint64     `json:"to_block"`
	Addresses     []string   `json:"addresses"`
	RowCount      int64      `json:"row_count"`
	Files         []FileInfo `json:"files"`
	Completed     bool       `json:"completed"`
}

type completionMarker struct {
	JobID     string `json:"job_id"`
	ChunkID   string `json:"chunk_id"`
	Rows      int64  `json:"rows"`
	Completed bool   `json:"completed"`
}

// validatePublishedManifest 只验证发布协议和请求身份，不下载大文件。
// 文件内容的 bytes/SHA256 在 MaterializeJobResult 再做强校验。
func (m *Manager) validatePublishedManifest(ctx context.Context, manifest publishedManifest, id, chunkID string) error {
	if manifest.JobID != id || manifest.ChunkID != chunkID {
		return fmt.Errorf("Cloud manifest 身份不匹配")
	}
	if err := validateQueueID("manifest job id", manifest.JobID); err != nil {
		return err
	}
	if err := validateQueueID("manifest chunk id", manifest.ChunkID); err != nil {
		return err
	}
	if !manifest.Completed {
		return errors.New("Cloud manifest 未声明 completed=true")
	}
	if manifest.RowCount < 0 {
		return fmt.Errorf("Cloud manifest row_count 非法: %d", manifest.RowCount)
	}
	if manifest.ToBlock < manifest.FromBlock {
		return fmt.Errorf("Cloud manifest 区块范围非法: %d-%d", manifest.FromBlock, manifest.ToBlock)
	}
	if manifest.RowCount > 0 && len(manifest.Files) == 0 {
		return errors.New("Cloud manifest 有数据行但没有 Parquet 文件")
	}
	seen := make(map[string]struct{}, len(manifest.Files))
	for _, file := range manifest.Files {
		rel, err := validateArtifactPath(file.Path)
		if err != nil {
			return err
		}
		key := strings.ToLower(filepath.ToSlash(rel))
		if _, ok := seen[key]; ok {
			return fmt.Errorf("Cloud manifest 含重复文件路径: %s", file.Path)
		}
		seen[key] = struct{}{}
		if file.Bytes <= 0 {
			return fmt.Errorf("Cloud manifest 文件大小非法: %s", file.Path)
		}
		decoded, err := hex.DecodeString(file.SHA256)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("Cloud manifest SHA256 非法: %s", file.Path)
		}
		if ok, err := m.store.Exists(ctx, leasedChunkDir(id, chunkID)+"/"+filepath.ToSlash(rel)); err != nil {
			return fmt.Errorf("检查 Cloud artifact %s: %w", file.Path, err)
		} else if !ok {
			return fmt.Errorf("Cloud manifest 引用的 artifact 不存在: %s", file.Path)
		}
	}

	successPayload, err := m.store.Get(ctx, completedChunkDir(id, chunkID)+"/_SUCCESS")
	if err != nil {
		return fmt.Errorf("读取 Cloud _SUCCESS: %w", err)
	}
	var success completionMarker
	if err := json.Unmarshal(successPayload, &success); err != nil {
		return fmt.Errorf("解析 Cloud _SUCCESS: %w", err)
	}
	if !success.Completed || success.JobID != id || success.ChunkID != chunkID || success.Rows != manifest.RowCount {
		return fmt.Errorf("Cloud _SUCCESS 与 manifest 不一致")
	}

	m.mu.Lock()
	expected, hasExpected := m.jobs[id]
	var expectedCopy Job
	if hasExpected {
		expectedCopy = *expected
	}
	m.mu.Unlock()
	if hasExpected {
		if expectedCopy.ChunkID != "" && expectedCopy.ChunkID != manifest.ChunkID {
			return fmt.Errorf("Cloud manifest chunk 与请求不一致")
		}
		if expectedCopy.FromBlock != manifest.FromBlock || expectedCopy.ToBlock != manifest.ToBlock {
			return fmt.Errorf("Cloud manifest 区块范围与请求不一致: request=%d-%d manifest=%d-%d",
				expectedCopy.FromBlock, expectedCopy.ToBlock, manifest.FromBlock, manifest.ToBlock)
		}
		if expectedCopy.ChainID != 0 && manifest.ChainID != expectedCopy.ChainID {
			return fmt.Errorf("Cloud manifest chain_id 与请求不一致: request=%d manifest=%d", expectedCopy.ChainID, manifest.ChainID)
		}
		if expectedCopy.Dataset != "" && normalizeCloudDataset(manifest.Dataset) != normalizeCloudDataset(expectedCopy.Dataset) {
			return fmt.Errorf("Cloud manifest dataset 与请求不一致: request=%s manifest=%s", expectedCopy.Dataset, manifest.Dataset)
		}
		if !sameCloudAddresses(expectedCopy.Addresses, manifest.Addresses) {
			return errors.New("Cloud manifest addresses 与请求不一致")
		}
	}
	return nil
}

func validateArtifactPath(value string) (string, error) {
	if value == "" || strings.Contains(value, "\\") {
		return "", fmt.Errorf("Cloud manifest 含非法路径: %q", value)
	}
	cleanSlash := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if cleanSlash == "." || strings.HasPrefix(cleanSlash, "/") || cleanSlash == ".." || strings.HasPrefix(cleanSlash, "../") {
		return "", fmt.Errorf("Cloud manifest 含非法路径: %q", value)
	}
	if !strings.EqualFold(filepath.Ext(cleanSlash), ".parquet") {
		return "", fmt.Errorf("Cloud manifest 含非 Parquet 产物: %q", value)
	}
	return filepath.FromSlash(cleanSlash), nil
}

func normalizeCloudDataset(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), "s")
}

func sameCloudAddresses(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	normalize := func(values []string) []string {
		out := make([]string, len(values))
		for i, value := range values {
			out[i] = strings.ToLower(strings.TrimSpace(value))
		}
		sort.Strings(out)
		return out
	}
	aa, bb := normalize(a), normalize(b)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

// remoteJobStatus 从远端队列读取 Job 状态（cloud 模式）。
func (m *Manager) remoteJobStatus(ctx context.Context, id string) (Job, error) {
	if err := validateQueueID("job id", id); err != nil {
		return Job{}, err
	}
	// 按 job_id 查找：completed/cancelled/failed/leased/pending
	if objs, err := m.store.List(ctx, queuePrefix+"completed/"+id+"/"); err == nil {
		for _, o := range objs {
			if strings.HasSuffix(o.Key, "/manifest.json") {
				payload, err := m.store.Get(ctx, o.Key)
				if err != nil {
					continue
				}
				var manifest publishedManifest
				if json.Unmarshal(payload, &manifest) == nil && manifest.JobID == id {
					if ok, _ := m.store.Exists(ctx, completedChunkDir(id, manifest.ChunkID)+"/_SUCCESS"); !ok {
						continue
					}
					if err := m.validatePublishedManifest(ctx, manifest, id, manifest.ChunkID); err != nil {
						return Job{ID: id, ChunkID: manifest.ChunkID, State: "failed", Error: "Cloud 完成产物质量校验失败: " + err.Error()}, nil
					}
					job := Job{ID: id, ChunkID: manifest.ChunkID, State: "done", Rows: manifest.RowCount}
					finished := time.Now()
					job.FinishedAt = &finished
					// P0-3：completed 为最终幂等判据；active lease/status 清理为 tombstone
					leased := leasedChunkDir(id, manifest.ChunkID)
					_ = m.store.Delete(ctx, leased+"/lease.json")
					_ = m.store.Delete(ctx, leased+"/status.json")
					return job, nil
				}
			}
		}
	}
	// Cancel Marker（Phase 5.2 §6）：cancel/<job_id>.json 或 cancelled/ 终态标记
	if ok, _ := m.store.Exists(ctx, cancelPrefix+id+".json"); ok {
		return Job{ID: id, State: "cancelled"}, nil
	}
	if ok, _ := m.store.Exists(ctx, cancelledPrefix+id+"/_CANCELLED"); ok {
		return Job{ID: id, State: "cancelled"}, nil
	}
	if objs, err := m.store.List(ctx, queuePrefix+"failed/"+id+"/"); err == nil {
		for _, o := range objs {
			if strings.HasSuffix(o.Key, "/error.json") {
				payload, _ := m.store.Get(ctx, o.Key)
				var failed struct {
					JobID string `json:"job_id"`
					Error string `json:"error"`
				}
				if json.Unmarshal(payload, &failed) == nil && failed.JobID == id {
					job := Job{ID: id, State: "failed", Error: failed.Error}
					return job, nil
				}
			}
		}
	}
	if objs, err := m.store.List(ctx, queuePrefix+"leased/"+id+"/"); err == nil {
		for _, o := range objs {
			if strings.HasSuffix(o.Key, "/status.json") {
				payload, _ := m.store.Get(ctx, o.Key)
				var st struct {
					JobID   string `json:"job_id"`
					ChunkID string `json:"chunk_id"`
					Status  string `json:"status"`
					Rows    int64  `json:"rows_written"`
				}
				if json.Unmarshal(payload, &st) == nil && st.JobID == id {
					state := "running"
					switch st.Status {
					case "COMPLETED":
						state = "done"
					case "FAILED":
						state = "failed"
					}
					return Job{ID: id, ChunkID: st.ChunkID, State: state, Rows: st.Rows}, nil
				}
			}
		}
		// 远端 Worker 已领取但尚未写 status.json（处理中）→ 视为 running，
		// 避免本地调度器误判“任务不存在”而无限等待。
		for _, o := range objs {
			if strings.HasSuffix(o.Key, "/lease.json") {
				return Job{ID: id, State: "running"}, nil
			}
		}
	}
	if objs, err := m.store.List(ctx, queuePrefix+"pending/"+id+"/"); err == nil {
		for _, o := range objs {
			if strings.HasSuffix(o.Key, "/request.json") {
				return Job{ID: id, State: "queued"}, nil
			}
		}
	}
	return Job{}, errors.New("远端 Cloud 任务不存在: " + id)
}

// Reconcile 重启对账（Phase 4 §60-61）：sqd list --org {org} 检查托管 Worker 是否已存在。
func (m *Manager) Reconcile(ctx context.Context) {
	if m.cfg.Mode != ModeCloud || m.state == WorkerNotConfigured {
		return
	}
	text, err := m.listCloudWorkers(ctx)
	if err != nil {
		m.markDegraded("Reconcile：sqd list 对账失败: " + err.Error())
		logger.Log.Warn().Err(err).Msg("cloud_reconcile_list_failed")
		return
	}
	if cloudWorkerMatches(text, m.cfg.WorkerName, m.cfg.WorkerSlot) {
		m.mu.Lock()
		if m.state != WorkerReady && m.state != WorkerBusy {
			m.state = WorkerIdle
			m.reason = "Reconcile：检测到已部署托管 Worker（" + m.cfg.WorkerName + "/" + m.cfg.WorkerSlot + "）"
			m.lastActive = time.Now()
		}
		m.mu.Unlock()
		m.saveState()
		logger.Log.Info().Str("org", m.cfg.Organization).Str("worker", m.cfg.WorkerName).Msg("cloud_worker_reconciled")
	} else {
		m.mu.Lock()
		m.state = WorkerAbsent
		m.reason = "Reconcile：未检测到托管 Worker（sqd list 无匹配）"
		m.mu.Unlock()
		m.saveState()
	}
}

func (m *Manager) markDegraded(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == WorkerNotConfigured {
		return
	}
	m.state = WorkerDegraded
	m.reason = reason
	m.saveStateLocked()
}

// cloudWorkerListed 查询 sqd list 是否已存在托管 Worker（供 EnsureWorker 复用）。
func (m *Manager) cloudWorkerListed(ctx context.Context) (bool, error) {
	text, err := m.listCloudWorkers(ctx)
	if err != nil {
		return false, err
	}
	return cloudWorkerMatches(text, m.cfg.WorkerName, m.cfg.WorkerSlot), nil
}

// waitForCloudWorker 验证 deploy 不只是命令退出码为 0，而是托管 Worker
// 确实出现在 sqd list 中。这是入队前的质量门，避免产生永久无法消费的 pending Job。
func (m *Manager) waitForCloudWorker(ctx context.Context) error {
	deadline := time.Now().Add(m.cfg.DeployTimeout)
	var lastErr error
	for {
		exists, err := m.cloudWorkerListed(ctx)
		if err == nil && exists {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("sqd deploy 已返回成功，但 sqd list 对账持续失败: %w", lastErr)
			}
			return fmt.Errorf("sqd deploy 已返回成功，但 %s/%s 未在 sqd list 中出现", m.cfg.WorkerName, m.cfg.WorkerSlot)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待 SQD Cloud Worker 对账: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func cloudWorkerMatches(output, workerName, workerSlot string) bool {
	output = strings.ToLower(output)
	workerName = strings.ToLower(strings.TrimSpace(workerName))
	if workerName == "" || !strings.Contains(output, workerName) {
		return false
	}
	workerSlot = strings.ToLower(strings.TrimSpace(workerSlot))
	if workerSlot == "" {
		return true
	}
	// sqd CLI 历史版本常见输出：name@slot、name/slot、name slot v2。
	// 必须同时命中 Worker 名和目标 slot，不能把旧版本 v1 当作 v2 复用。
	patterns := []string{
		workerName + "@" + workerSlot,
		workerName + "/" + workerSlot,
		workerName + " " + workerSlot,
		workerName + " slot " + workerSlot,
	}
	for _, pattern := range patterns {
		if strings.Contains(output, pattern) {
			return true
		}
	}
	return false
}

func validateQueueID(label, value string) error {
	if value == "" {
		return fmt.Errorf("%s 不能为空", label)
	}
	if len(value) > 128 {
		return fmt.Errorf("%s 过长（最大 128 字符）", label)
	}
	if value == "." || value == ".." || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return fmt.Errorf("%s 不能以点开头或结尾", label)
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("%s 含非法字符 %q（仅允许字母、数字、-_.）", label, r)
	}
	base := strings.ToUpper(strings.SplitN(value, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
		(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
		return fmt.Errorf("%s 使用了 Windows 保留名称", label)
	}
	return nil
}

// listCloudWorkers 执行 sqd list --org {org}（CommandRunner 可注入测试）。
func (m *Manager) listCloudWorkers(ctx context.Context) (string, error) {
	run := m.cfg.CommandRunner
	if run == nil {
		run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			return cmd.CombinedOutput()
		}
	}
	out, err := run(ctx, "sqd", "list", "--org", m.cfg.Organization)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ── 队列与产物辅助 ──

const (
	queuePrefix     = "bsc/jobs/"
	cancelPrefix    = "bsc/jobs/cancel/"
	cancelledPrefix = "bsc/jobs/cancelled/"
	requeuedPrefix  = "bsc/jobs/requeued/"
)

func pendingChunkDir(jobID, chunkID string) string {
	return queuePrefix + "pending/" + jobID + "/" + chunkID
}

func leasedChunkDir(jobID, chunkID string) string {
	return queuePrefix + "leased/" + jobID + "/" + chunkID
}

func completedChunkDir(jobID, chunkID string) string {
	return queuePrefix + "completed/" + jobID + "/" + chunkID
}

func failedChunkDir(jobID, chunkID string) string {
	return queuePrefix + "failed/" + jobID + "/" + chunkID
}

func (m *Manager) enqueuePending(ctx context.Context, job *Job) error {
	payload, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	return m.store.Put(ctx, pendingChunkDir(job.ID, job.ChunkID)+"/request.json", payload)
}

func (m *Manager) writeLeasedStatus(ctx context.Context, job *Job, status string, rows int64, currentBlock uint64) error {
	payload, _ := json.MarshalIndent(map[string]any{
		"job_id": job.ID, "chunk_id": job.ChunkID, "status": status,
		"worker_id": m.workerID, "started_at": job.StartedAt,
		"current_block": currentBlock, "rows_written": rows,
		"last_progress_at": time.Now().Format(time.RFC3339),
	}, "", "  ")
	return m.store.Put(ctx, leasedChunkDir(job.ID, job.ChunkID)+"/status.json", payload)
}

func (m *Manager) writeFailedLocked(ctx context.Context, job *Job, runErr error) error {
	payload, _ := json.MarshalIndent(map[string]any{
		"job_id": job.ID, "chunk_id": job.ChunkID, "error": runErr.Error(),
		"request_ref": pendingChunkDir(job.ID, job.ChunkID) + "/request.json",
		"status_ref":  leasedChunkDir(job.ID, job.ChunkID) + "/status.json",
		"failed_at":   time.Now().Format(time.RFC3339),
	}, "", "  ")
	return m.store.Put(ctx, failedChunkDir(job.ID, job.ChunkID)+"/error.json", payload)
}

func (m *Manager) queueCountsLocked() (pending, leased int) {
	pending, leased, _ = m.queueCountsErr()
	return
}

func (m *Manager) queueCountsErr() (pending, leased int, err error) {
	if m.store == nil {
		return 0, 0, nil
	}
	ctx := context.Background()
	if objs, err := m.store.List(ctx, queuePrefix+"pending/"); err == nil {
		for _, o := range objs {
			if strings.HasSuffix(o.Key, "/request.json") {
				pending++
			}
		}
	} else {
		return 0, 0, err
	}
	if objs, err := m.store.List(ctx, queuePrefix+"leased/"); err == nil {
		leasedChunks := map[string]struct{}{}
		for _, o := range objs {
			// 远端 Worker 领取时先写 lease.json，运行中才写 status.json；
			// 两者都证明 leased，但同一 Chunk 只能计数一次。
			if strings.HasSuffix(o.Key, "/status.json") || strings.HasSuffix(o.Key, "/lease.json") {
				jobID, chunkID := parseChunkKey(o.Key, "leased/")
				if jobID != "" && chunkID != "" {
					leasedChunks[jobID+"/"+chunkID] = struct{}{}
				}
			}
		}
		leased = len(leasedChunks)
	} else {
		return 0, 0, err
	}
	return pending, leased, nil
}

func parseChunkKey(key, queue string) (jobID, chunkID string) {
	rest := strings.TrimPrefix(key, queuePrefix+queue)
	parts := strings.Split(rest, "/")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

// runLocalProcessor 以本机 Processor 进程执行 Job（env 参数驱动，含 TO_BLOCK 有界退出）。
func (m *Manager) runLocalProcessor(job *Job, outDir string) error {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		return fmt.Errorf("未找到 node: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, nodeBin, filepath.Join("lib", "main.js"))
	cmd.Dir = m.cfg.WorkerProjectDir
	cmd.Env = append(os.Environ(),
		"JOB_ID="+job.ID,
		"CHUNK_ID="+job.ChunkID,
		"PORTAL_URL=https://portal.sqd.dev/datasets/binance-mainnet",
		"TOKEN_CONTRACT="+job.TokenContract,
		"FROM_BLOCK="+fmt.Sprintf("%d", job.FromBlock),
		"TO_BLOCK="+fmt.Sprintf("%d", job.ToBlock),
		"WATCH_ADDRESSES="+strings.Join(job.Addresses, ","),
		"OUTPUT_DIRECTORY="+outDir,
		"FORCE_FLUSH_BLOCKS=50000",
		"SQD_DEBUG=sqd:error",
	)
	logPath := filepath.Join(m.jobsDir, job.ID, job.ChunkID, "worker.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("创建 Worker 日志失败: %w", err)
	}
	defer logFile.Close()
	cmd.Stdout = io.MultiWriter(logFile)
	cmd.Stderr = io.MultiWriter(logFile)
	m.mu.Lock()
	m.proc = cmd
	m.mu.Unlock()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 Processor 失败: %w", err)
	}
	waitErr := cmd.Wait()
	m.mu.Lock()
	m.proc = nil
	m.mu.Unlock()
	if waitErr != nil {
		if ctx.Err() != nil {
			return errors.New("Processor 被取消")
		}
		return fmt.Errorf("Processor 异常退出: %w（日志：%s）", waitErr, logPath)
	}
	rows, ok := successRows(outDir)
	if !ok {
		return fmt.Errorf("Processor 退出但缺少 _SUCCESS/parquet 产物（日志：%s）", logPath)
	}
	job.Rows = rows
	return nil
}

// deployCloudWorker 调用 sqd deploy 部署固定 Worker Release（V1：需要部署密钥）。
func (m *Manager) deployCloudWorker(ctx context.Context) error {
	deployCtx, cancel := context.WithTimeout(ctx, m.cfg.DeployTimeout)
	defer cancel()
	run := m.cfg.CommandRunner
	if run == nil {
		sqdBin, err := exec.LookPath("sqd")
		if err != nil {
			return fmt.Errorf("未找到 sqd CLI: %w", err)
		}
		cmd := exec.CommandContext(deployCtx, sqdBin, "deploy", ".", "-o", m.cfg.Organization, "--no-interactive", "--allow-update")
		cmd.Dir = m.cfg.WorkerProjectDir
		cmd.Env = append(os.Environ(),
			"SQD_DEPLOY_KEY="+m.cfg.DeployKey,
			"SQUID_DEPLOY_KEY="+m.cfg.DeployKey,
			"SQUID_ORG="+m.cfg.Organization,
			"SQUID_WORKER="+m.cfg.WorkerName,
			"SQUID_SLOT="+m.cfg.WorkerSlot,
		)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("启动 sqd deploy 失败: %w", err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case err := <-done:
				// Some CLI versions can return a non-zero wrapper status after the
				// remote deploy has already succeeded. The remote list is the
				// authoritative result, not the wrapper exit code alone.
				if exists, _ := m.cloudWorkerListed(deployCtx); exists {
					err = nil
				}
				if err != nil {
					firstLine := sanitizeCloudCLIOutput(out.String(), m.cfg.DeployKey)
					logger.Log.Error().Err(err).Str("sqd", firstLine).Msg("cloud_worker_deploy_failed")
					return fmt.Errorf("sqd deploy 失败: %s", firstLine)
				}
				goto deployed
			case <-ticker.C:
				exists, listErr := m.cloudWorkerListed(deployCtx)
				if listErr == nil && exists {
					// On Windows the SQD CLI can keep streaming after the Worker is
					// already DEPLOYED. Stop only this exact deploy process tree so
					// SubmitJob can continue to the queue quality gate.
					stopCommandProcessTree(cmd)
					select {
					case <-done:
					case <-time.After(5 * time.Second):
					}
					goto deployed
				}
			case <-deployCtx.Done():
				stopCommandProcessTree(cmd)
				select {
				case <-done:
				case <-time.After(5 * time.Second):
				}
				return fmt.Errorf("sqd deploy 超时或取消: %w", deployCtx.Err())
			}
		}
	deployed:
		logger.Log.Info().Str("project", m.cfg.WorkerProjectDir).Str("org", m.cfg.Organization).
			Str("worker", m.cfg.WorkerName).Msg("cloud_worker_deployed")
		return nil
	}
	out, err := run(deployCtx, "sqd", "deploy", ".", "-o", m.cfg.Organization, "--no-interactive", "--allow-update")
	if err != nil {
		firstLine := sanitizeCloudCLIOutput(string(out), m.cfg.DeployKey)
		logger.Log.Error().Err(err).Str("sqd", firstLine).Msg("cloud_worker_deploy_failed")
		return fmt.Errorf("sqd deploy 失败: %s", firstLine)
	}
	logger.Log.Info().Str("project", m.cfg.WorkerProjectDir).Str("org", m.cfg.Organization).
		Str("worker", m.cfg.WorkerName).Msg("cloud_worker_deployed")
	return nil
}

func stopCommandProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		// PID comes from os.Process and is passed as a separate argument; no
		// shell or path expansion is involved.
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
		return
	}
	_ = cmd.Process.Kill()
}

// removeCloudWorker 调用 sqd remove 移除 Cloud 资源（Phase 4 §63：仅移除托管 Worker，禁止模糊删除 org）。
func (m *Manager) removeCloudWorker(ctx context.Context) error {
	run := m.cfg.CommandRunner
	if run == nil {
		run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Env = append(os.Environ(), "SQD_DEPLOY_KEY="+m.cfg.DeployKey, "SQUID_DEPLOY_KEY="+m.cfg.DeployKey)
			return cmd.CombinedOutput()
		}
	}
	removeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := run(removeCtx, "sqd", "remove", "-o", m.cfg.Organization, "-n", m.cfg.WorkerName, "-s", m.cfg.WorkerSlot, "--no-interactive", "-f")
	if err != nil {
		safeOut := sanitizeCloudCLIOutput(string(out), m.cfg.DeployKey)
		logger.Log.Warn().Err(err).Str("out", safeOut).Msg("cloud_worker_remove_failed")
		return fmt.Errorf("sqd remove 失败: %s", safeOut)
	}
	logger.Log.Info().Str("org", m.cfg.Organization).Str("worker", m.cfg.WorkerName).Msg("cloud_worker_removed")
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func sanitizeCloudCLIOutput(output, deployKey string) string {
	firstLine := strings.TrimSpace(strings.SplitN(output, "\n", 2)[0])
	if deployKey != "" {
		firstLine = strings.ReplaceAll(firstLine, deployKey, "[REDACTED]")
	}
	if firstLine == "" {
		firstLine = "CLI 未返回详细错误"
	}
	return truncate(firstLine, 200)
}

// ── 持久化与锁 ──

func (m *Manager) saveState() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveStateLocked()
}

func (m *Manager) saveStateLocked() {
	type persisted struct {
		State    WorkerState `json:"state"`
		Reason   string      `json:"reason,omitempty"`
		WorkerID string      `json:"worker_id,omitempty"`
	}
	payload, err := json.MarshalIndent(persisted{State: m.state, Reason: m.reason, WorkerID: m.workerID}, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(m.statePath), 0o755)
	_ = os.WriteFile(m.statePath+".tmp", payload, 0o644)
	_ = os.Rename(m.statePath+".tmp", m.statePath)
}

func (m *Manager) loadState() {
	payload, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var persisted struct {
		State WorkerState `json:"state"`
	}
	if json.Unmarshal(payload, &persisted) != nil {
		return
	}
	// 重启后不保留运行态（Phase 4 §60：由 Reconcile 对账真实 Worker）；
	// 但 NOT_CONFIGURED（缺少凭据/Worker 项目）必须保留，不能伪装成可部署。
	if m.state != WorkerNotConfigured {
		m.state = WorkerAbsent
	}
}

func (m *Manager) persistJob(job *Job) {
	dir := filepath.Join(m.jobsDir, job.ID, job.ChunkID)
	_ = os.MkdirAll(dir, 0o755)
	payload, _ := json.MarshalIndent(job, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "status.json"), payload, 0o644)
}

var errDeployLockBusy = errors.New("Cloud Worker 正在部署中（其他实例持有锁）")

// acquireDeployLock 进程内+文件双保险（设计 §28：避免并发 Deploy）。
func (m *Manager) acquireDeployLock(ctx context.Context) error {
	lockPath := filepath.Join(m.cfg.JobsRoot, "deploy.lock")
	deadline := time.Now().Add(m.cfg.DeployTimeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "pid=%d time=%s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			_ = f.Close()
			return nil
		}
		if !os.IsExist(err) {
			return err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > m.cfg.DeployTimeout {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return errDeployLockBusy
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (m *Manager) releaseDeployLock() {
	_ = os.Remove(filepath.Join(m.cfg.JobsRoot, "deploy.lock"))
}

// ── 产物校验（Phase 4 §24/§25）──

// successRows 校验 _SUCCESS；rows=0 且无 parquet 视为合法（0 行 Chunk）。
func successRows(outDir string) (int64, bool) {
	successPath := filepath.Join(outDir, "_SUCCESS")
	payload, err := os.ReadFile(successPath)
	if err != nil {
		return 0, false
	}
	var info struct {
		Rows int64 `json:"rows"`
	}
	if json.Unmarshal(payload, &info) != nil {
		return 0, false
	}
	if info.Rows == 0 {
		return 0, true // 0 行 Chunk：completed=true 合法（Phase 4 §25）
	}
	parquet, err := filepath.Glob(filepath.Join(outDir, "*", "*.parquet"))
	if err != nil || len(parquet) == 0 {
		parquet, err = filepath.Glob(filepath.Join(outDir, "*.parquet"))
		if err != nil || len(parquet) == 0 {
			return 0, false
		}
	}
	return info.Rows, true
}

// writeJobSuccess 写 _SUCCESS（mock/测试与本地 Processor 共用契约）。
func writeJobSuccess(outDir string, job *Job, rows int64) error {
	files, _ := filepath.Glob(filepath.Join(outDir, "*", "*.parquet"))
	if len(files) == 0 {
		files, _ = filepath.Glob(filepath.Join(outDir, "*.parquet"))
	}
	manifest := map[string]any{
		"job_id": job.ID, "chunk_id": job.ChunkID, "provider": "SQD_CLOUD_EXPORT",
		"chain_id": 56, "dataset": "token_transfer",
		"from_block": job.FromBlock, "to_block": job.ToBlock,
		"row_count": rows, "files": files, "completed": true,
	}
	payload, _ := json.MarshalIndent(manifest, "", "  ")
	success := map[string]any{"job_id": job.ID, "chunk_id": job.ChunkID, "rows": rows, "completed": true}
	successPayload, _ := json.MarshalIndent(success, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "manifest.json"), payload, 0o644)
	_ = os.WriteFile(filepath.Join(outDir, "_SUCCESS"), successPayload, 0o644)
	return nil
}
