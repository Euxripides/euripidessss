package cloudruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	st := Status{
		State:                   m.state,
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
	switch m.state {
	case WorkerReady, WorkerBusy, WorkerIdle, WorkerDeploying, WorkerStarting, WorkerAbsent:
		st.Available = true
	}
	if !st.Available && m.state != WorkerNotConfigured && time.Now().Before(m.cooldownEnd) {
		st.Reason = fmt.Sprintf("运行时失败冷却中（至 %s）", m.cooldownEnd.Format(time.RFC3339))
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
		if err := m.reclaimExpiredLeases(context.Background()); err != nil {
			logger.Log.Warn().Err(err).Msg("cloud_lease_reclaim_failed")
		}
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
	for _, o := range objs {
		if !strings.HasSuffix(o.Key, "/lease.json") {
			continue
		}
		payload, err := m.store.Get(ctx, o.Key)
		if err != nil {
			continue
		}
		var lease struct {
			JobID          string `json:"job_id"`
			ChunkID        string `json:"chunk_id"`
			LeaseExpiresAt string `json:"lease_expires_at"`
		}
		if json.Unmarshal(payload, &lease) != nil || lease.JobID == "" {
			continue
		}
		expires, err := time.Parse(time.RFC3339, lease.LeaseExpiresAt)
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
	if strings.TrimSpace(id) == "" {
		return errors.New("job id 不能为空")
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
		if exists, listErr := m.cloudWorkerListed(ctx); listErr == nil && exists {
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
		m.lastActive = time.Now()
	}
	m.mu.Unlock()
	m.saveState()
	return deployErr
}

// SubmitJob 提交一个 Cloud 应急任务（Phase 4：写入 pending 队列）。
// cloud 模式：EnsureWorker → enqueue（Worker 轮询 R2）；local/mock：enqueue 后本地循环执行。
func (m *Manager) SubmitJob(ctx context.Context, job Job) (string, error) {
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
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	if job.JobID == "" {
		job.JobID = job.ID
	}
	if job.ChunkID == "" {
		job.ChunkID = "chunk-1"
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
	if job.TokenContract == "" {
		job.TokenContract = "0x55d398326f99059ff775485246999027b3197955" // BSC USDT（V1）
	}
	job.Mode = m.cfg.Mode
	job.State = "queued"
	job.CreatedAt = time.Now()
	if err := m.enqueuePending(ctx, &job); err != nil {
		return "", err
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
	if m.cfg.Mode == ModeCloud {
		return m.remoteJobStatus(context.Background(), id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.jobs[id]; ok {
		return *j, nil
	}
	return Job{}, errors.New("Cloud 任务不存在: " + id)
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
		m.removeCloudWorker(ctx)
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
			m.cooldownEnd = time.Now().Add(m.cfg.RuntimeFailureCooldown)
			_ = m.writeFailedLocked(ctx, job, runErr)
			// 清理 leased 标记（证据保留在 failed/error.json + 本地日志）
			leased := leasedChunkDir(job.ID, job.ChunkID)
			_ = m.store.Delete(ctx, leased+"/status.json")
			_ = m.store.Delete(ctx, leased+"/lease.json")
		} else {
			job.State = "done"
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

// remoteJobStatus 从远端队列读取 Job 状态（cloud 模式）。
func (m *Manager) remoteJobStatus(ctx context.Context, id string) (Job, error) {
	// 按 job_id 查找：completed/cancelled/failed/leased/pending
	if objs, err := m.store.List(ctx, queuePrefix+"completed/"+id+"/"); err == nil {
		for _, o := range objs {
			if strings.HasSuffix(o.Key, "/manifest.json") {
				payload, err := m.store.Get(ctx, o.Key)
				if err != nil {
					continue
				}
				var manifest struct {
					JobID    string `json:"job_id"`
					ChunkID  string `json:"chunk_id"`
					RowCount int64  `json:"row_count"`
				}
				if json.Unmarshal(payload, &manifest) == nil && manifest.JobID == id {
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
		logger.Log.Warn().Err(err).Msg("cloud_reconcile_list_failed")
		return
	}
	if strings.Contains(text, m.cfg.WorkerName) || strings.Contains(text, m.cfg.WorkerSlot) {
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

// cloudWorkerListed 查询 sqd list 是否已存在托管 Worker（供 EnsureWorker 复用）。
func (m *Manager) cloudWorkerListed(ctx context.Context) (bool, error) {
	text, err := m.listCloudWorkers(ctx)
	if err != nil {
		return false, err
	}
	return strings.Contains(text, m.cfg.WorkerName) || strings.Contains(text, m.cfg.WorkerSlot), nil
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
	queuePrefix    = "bsc/jobs/"
	cancelPrefix   = "bsc/jobs/cancel/"
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
		for _, o := range objs {
			// 远端 Worker 领取时先写 lease.json，运行中才写 status.json；
			// 两者都算 leased，否则 Idle Reaper 会误删仍在处理 Job 的 Worker。
			if strings.HasSuffix(o.Key, "/status.json") || strings.HasSuffix(o.Key, "/lease.json") {
				leased++
			}
		}
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
		cmd := exec.CommandContext(deployCtx, sqdBin, "deploy", ".", "-o", m.cfg.Organization, "--no-interactive")
		cmd.Dir = m.cfg.WorkerProjectDir
		cmd.Env = append(os.Environ(),
			"SQUID_DEPLOY_KEY="+m.cfg.DeployKey,
			"SQUID_ORG="+m.cfg.Organization,
			"SQUID_WORKER="+m.cfg.WorkerName,
			"SQUID_SLOT="+m.cfg.WorkerSlot,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			firstLine := strings.SplitN(string(out), "\n", 2)[0]
			if len(firstLine) > 200 {
				firstLine = firstLine[:200]
			}
			logger.Log.Error().Err(err).Str("sqd", firstLine).Msg("cloud_worker_deploy_failed")
			return fmt.Errorf("sqd deploy 失败（详见服务日志）")
		}
		logger.Log.Info().Str("project", m.cfg.WorkerProjectDir).Str("org", m.cfg.Organization).
			Str("worker", m.cfg.WorkerName).Msg("cloud_worker_deployed")
		return nil
	}
	out, err := run(deployCtx, "sqd", "deploy", ".", "-o", m.cfg.Organization, "--no-interactive")
	if err != nil {
		firstLine := strings.SplitN(string(out), "\n", 2)[0]
		if len(firstLine) > 200 {
			firstLine = firstLine[:200]
		}
		logger.Log.Error().Err(err).Str("sqd", firstLine).Msg("cloud_worker_deploy_failed")
		return fmt.Errorf("sqd deploy 失败（详见服务日志）")
	}
	logger.Log.Info().Str("project", m.cfg.WorkerProjectDir).Str("org", m.cfg.Organization).
		Str("worker", m.cfg.WorkerName).Msg("cloud_worker_deployed")
	return nil
}

// removeCloudWorker 调用 sqd remove 移除 Cloud 资源（Phase 4 §63：仅移除托管 Worker，禁止模糊删除 org）。
func (m *Manager) removeCloudWorker(ctx context.Context) {
	run := m.cfg.CommandRunner
	if run == nil {
		run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Env = append(os.Environ(), "SQUID_DEPLOY_KEY="+m.cfg.DeployKey)
			return cmd.CombinedOutput()
		}
	}
	removeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := run(removeCtx, "sqd", "remove", "-o", m.cfg.Organization, "-n", m.cfg.WorkerName, "-s", m.cfg.WorkerSlot, "--no-interactive", "-f")
	if err != nil {
		logger.Log.Warn().Err(err).Str("out", truncate(string(out), 200)).Msg("cloud_worker_remove_failed")
		return
	}
	logger.Log.Info().Str("org", m.cfg.Organization).Str("worker", m.cfg.WorkerName).Msg("cloud_worker_removed")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
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
