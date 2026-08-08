package prefetch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/graphcache"
	invcache "github.com/etl/backend/internal/investigation/cache"
	"github.com/etl/backend/internal/smartdownload"
)

// Config 是预取管理器配置。
type Config struct {
	Interval         time.Duration
	SavedWaitSeconds float64 // 预取命中为用户节省的等待时间（默认 43.8s）
	Progressive      bool                // 渐进式 7d/90d 窗口
	ProgressiveStages []ProgressiveStage
}

// DefaultConfig 返回默认配置。
func DefaultConfig() Config {
	return Config{Interval: 15 * time.Second, SavedWaitSeconds: 43.8,
		Progressive: true, ProgressiveStages: DefaultProgressiveStages()}
}

// BatchCallbacks 是管理器与 Smart Download 的桥接回调。
type BatchCallbacks struct {
	Create        func(ctx context.Context, req smartdownload.CreateBatchRequest) (*smartdownload.CreateBatchResponse, error)
	Start         func(batchID string) error
	Pause         func(batchID string) error
	Resume        func(batchID string) error
	BatchStatus   func(batchID string) (status string, terminal bool)
	CoverageQuery func(chainKey, address, dataset string, from, to uint64) graphcache.CoverageInfo
	ChainID       func(chainKey string) int64
	ActiveUserTasks func() int
	HeadBlock     func() uint64
}

// Manager 是 Smart Prefetch 调度器（设计 §28-§32、§59、§66、§69-§75）。
type Manager struct {
	mu         sync.Mutex
	queue      *Queue
	budget     *BudgetStore
	feedback   *Feedback
	graph      *graphcache.Cache
	invStore   *invcache.Store
	planner    *Planner
	callbacks  BatchCallbacks
	cfg        Config
	diskPolicy DiskPolicy
	interval   time.Duration
	dataRoot   string
	active     *ActiveRegistry
	leases     *LeaseStore
	reorg      ReorgPolicy

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	lastRun  *time.Time
	upgrades int
}

// NewManager 创建预取管理器。
func NewManager(queue *Queue, budget *BudgetStore, feedback *Feedback,
	graph *graphcache.Cache, invStore *invcache.Store, cb BatchCallbacks, cfg Config) *Manager {
	if cfg.Interval <= 0 {
		cfg.Interval = 15 * time.Second
	}
	active, _ := NewActiveRegistry(queue.Root())
	leases := NewLeaseStore(queue.Root(), 2*time.Minute)
	return &Manager{
		queue:      queue,
		budget:     budget,
		feedback:   feedback,
		graph:      graph,
		invStore:   invStore,
		planner:    NewPlanner(coverageAdapter{cb: cb}),
		callbacks:  cb,
		cfg:        cfg,
		diskPolicy: DefaultDiskPolicy(),
		interval:   cfg.Interval,
		active:     active,
		leases:     leases,
		reorg:      ReorgPolicy{SafetyBlocks: 20},
		stop:       make(chan struct{}),
	}
}

// SetReorgPolicy 覆盖重组织安全窗口。
func (m *Manager) SetReorgPolicy(p ReorgPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reorg = p
}

// SetDiskPolicy 覆盖磁盘策略。
func (m *Manager) SetDiskPolicy(p DiskPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.diskPolicy = p
}

// SetDataRoot 设置磁盘策略检查根目录。
func (m *Manager) SetDataRoot(root string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dataRoot = root
}

// Start 启动后台低优先级循环。
func (m *Manager) Start() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		ctx := context.Background()
		_ = m.tick(ctx)
		for {
			select {
			case <-m.stop:
				return
			case <-ticker.C:
				_ = m.tick(ctx)
			}
		}
	}()
}

// Stop 停止后台循环。
func (m *Manager) Stop() {
	m.stopOnce.Do(func() { close(m.stop) })
	m.wg.Wait()
}

// Plan 生成候选并入队（P0：Candidate Generator + Score + Coverage 联动）。
func (m *Manager) Plan(ctx context.Context, invID, chainKey string, chainID int64,
	focus string, snap invcache.ContextSnapshot) ([]Candidate, error) {
	key := graphcache.Key{
		ChainID:    chainID,
		Address:    focus,
		Direction:  string(graphcache.DirectionAll),
		DatasetSet: GraphBundle(),
		TokenFilter: firstToken(snap.Tokens),
		FromBlock:  snap.FromBlock,
		ToBlock:    snap.ToBlock,
		Depth:      1,
		AggregationVersion: 1,
	}
	res, _, err := m.graph.GetOrBuild(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("prefetch: 图扩展缓存构建失败: %w", err)
	}
	candidates, err := m.planner.Plan(ctx, invID, chainKey, chainID, res, snap)
	if err != nil {
		return nil, err
	}
	if m.invStore != nil {
		_, _ = m.invStore.AddGraphKey(invID, key.Hash())
	}
	for _, c := range candidates {
		if c.Priority == PriorityCOLD {
			// COLD 只保留 Discovery 元数据，不预取（设计 §19）
			if m.invStore != nil {
				_, _ = m.invStore.UpsertCandidate(invID, &invcache.CandidateSummary{
					Address: c.Address, ParentAddress: c.ParentAddress, Score: c.Score,
					Priority: string(c.Priority), Status: "COLD_ONLY",
					Reasons: c.Reason, RequiredDatasets: c.RequiredDatasets,
					FromBlock: c.FromBlock, ToBlock: c.ToBlock,
				})
			}
			continue
		}
		job, created, err := m.queue.Enqueue(c)
		if err != nil {
			return nil, err
		}
		if m.invStore != nil {
			_, _ = m.invStore.UpsertCandidate(invID, &invcache.CandidateSummary{
				Address: c.Address, ParentAddress: c.ParentAddress, Score: c.Score,
				Priority: string(c.Priority), Status: string(job.Status), BatchID: job.BatchID,
				Reasons: c.Reason, RequiredDatasets: c.RequiredDatasets,
				EstimatedRows: c.EstimatedRows, EstimatedBytes: c.EstimatedBytes,
				FromBlock: c.FromBlock, ToBlock: c.ToBlock,
			})
		}
		_ = created
	}
	return candidates, nil
}

// Pin 手工固定预取地址（设计 §64）：HOT 立即进入队列。
func (m *Manager) Pin(invID, chainKey string, chainID int64, address, reason string, from, to uint64) (Candidate, error) {
	c := Candidate{
		ChainID: chainID, ChainKey: chainKey, Address: strings.ToLower(strings.TrimSpace(address)),
		Reason: []string{reason}, Score: 100, RequiredDatasets: MinimalBundle(),
		Priority: PriorityHOT, FromBlock: from, ToBlock: to, InvestigationID: invID,
		Pinned: true, CreatedAt: time.Now().UTC(),
	}
	job, _, err := m.queue.Enqueue(c)
	if err != nil {
		return Candidate{}, err
	}
	if job.Status == StatusPaused || job.Status == StatusPrefetching || job.Status == StatusPending {
		_ = m.queue.UpdateStatus(job.ID, StatusPending)
	}
	if m.invStore != nil {
		_, _ = m.invStore.UpsertCandidate(invID, &invcache.CandidateSummary{
			Address: c.Address, ParentAddress: c.ParentAddress, Score: c.Score,
			Priority: string(PriorityHOT), Status: string(job.Status), BatchID: job.BatchID,
			Reasons: c.Reason, RequiredDatasets: c.RequiredDatasets,
			FromBlock: c.FromBlock, ToBlock: c.ToBlock,
		})
	}
	return c, nil
}

// Upgrade 用户点击地址 → Interactive Upgrade（设计 §53-§54）：任务 ID 不变，继续进度。
func (m *Manager) Upgrade(invID, chainKey, address string) error {
	jobs := m.queue.FindByAddress(chainKey, address)
	if len(jobs) == 0 {
		return fmt.Errorf("prefetch: 没有该地址的预取任务")
	}
	var lastErr error
	for _, j := range jobs {
		terminal := false
		status := ""
		if j.BatchID != "" && m.callbacks.BatchStatus != nil {
			status, terminal = m.callbacks.BatchStatus(j.BatchID)
		}
		if terminal && status == "COMPLETED" {
			// 数据已就绪：无需恢复批处理，直接标记已使用（任务 ID 不变）
			if err := m.queue.Upgrade(j.ID); err != nil {
				lastErr = err
				continue
			}
			_ = m.queue.UpdateStatus(j.ID, StatusReady)
		} else if j.BatchID != "" && !terminal && m.callbacks.Resume != nil {
			if err := m.callbacks.Resume(j.BatchID); err != nil {
				lastErr = err
				continue
			}
			if err := m.queue.Upgrade(j.ID); err != nil {
				lastErr = err
				continue
			}
		} else {
			// 尚未启动批处理：升级为 INTERACTIVE，由后台循环立即启动
			if err := m.queue.Upgrade(j.ID); err != nil {
				lastErr = err
				continue
			}
		}
		if m.feedback != nil {
			_ = m.feedback.RecordUse(invID, address, j.BatchID, m.cfg.SavedWaitSeconds)
		}
		m.mu.Lock()
		m.upgrades++
		m.mu.Unlock()
	}
	return lastErr
}

// OnDatasetIndexed 数据入库后：失效图缓存并推进预取任务状态。
func (m *Manager) OnDatasetIndexed(chainKey, address, dataset string) {
	if m.graph != nil && m.graph.Store() != nil {
		if m.callbacks.ChainID != nil {
			_ = m.graph.Store().InvalidateDataset(m.callbacks.ChainID(chainKey), address, dataset)
		}
	}
	for _, j := range m.queue.FindByAddress(chainKey, address) {
		if j.BatchID == "" {
			continue
		}
		if m.callbacks.BatchStatus == nil {
			continue
		}
		status, terminal := m.callbacks.BatchStatus(j.BatchID)
		if !terminal {
			continue
		}
		if status == "COMPLETED" {
			_ = m.queue.UpdateStatus(j.ID, StatusReady)
			_ = m.budget.Release()
			_ = m.budget.RecordDownload(j.Candidate.EstimatedBytes, j.Candidate.EstimatedBytes)
			m.releaseJobResources(j)
		} else {
			_ = m.queue.UpdateStatus(j.ID, StatusFailed)
			_ = m.budget.Release()
			m.releaseJobResources(j)
		}
	}
}

// Status 返回调查预取状态（设计 §51、§63）。
func (m *Manager) Status(invID string) map[string]any {
	var jobs []*Job
	if m.queue != nil {
		jobs = m.queue.ListByInvestigation(invID)
	}
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, map[string]any{
			"id": j.ID, "address": j.Candidate.Address, "parent_address": j.Candidate.ParentAddress,
			"score": j.Candidate.Score, "priority": j.Candidate.Priority, "status": j.Status,
			"batch_id": j.BatchID, "batch_status": j.BatchStatus,
			"reasons": j.Candidate.Reason, "required_datasets": j.Candidate.RequiredDatasets,
			"from_block": j.Candidate.FromBlock, "to_block": j.Candidate.ToBlock,
			"upgrade_count": j.UpgradeCount, "created_at": j.CreatedAt, "updated_at": j.UpdatedAt,
		})
	}
	stats := m.Stats()
	return map[string]any{
		"investigation_id": invID,
		"candidates":       out,
		"stats":            stats,
	}
}

// Stats 返回管理器汇总。
func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := 0
	if m.queue != nil {
		total = len(m.queue.List())
	}
	st := Stats{
		TotalJobs: total, ActiveJobs: m.queue.Active(), ReadyJobs: m.queue.ReadyCount(),
		InteractiveUpgrades: m.upgrades,
		Budget: m.budget.Config(), Counters: m.budget.Counters(),
		Feedback: m.feedback.Stats(),
		LastRun: m.lastRun,
	}
	return st
}

func (m *Manager) tick(ctx context.Context) error {
	now := time.Now().UTC()
	m.mu.Lock()
	m.lastRun = &now
	policy := m.diskPolicy
	m.mu.Unlock()
	usedPct := 0.0
	if m.dataRoot != "" {
		if pct, err := DiskUsage(m.dataRoot); err == nil {
			usedPct = pct
		}
	}
	if m.callbacks.ActiveUserTasks != nil && m.callbacks.ActiveUserTasks() > 0 {
		// 前台任务占用时暂停预取（设计 §29-§30 Case C）
		m.pauseAllPrefetch()
		return nil
	}
	action := policy.Action(usedPct)
	if action == DiskBlockNew || action == DiskPauseAll {
		m.pauseAllPrefetch()
		return nil
	}
	// 启动新任务
	for _, j := range m.queue.List() {
		if j.Status != StatusPending && !(j.Status == StatusInteractive && j.BatchID == "") {
			continue
		}
		if action == DiskPauseWarm && j.Candidate.Priority == PriorityWARM {
			continue
		}
		if m.budget.Allow() != nil {
			break
		}
		if j.Candidate.Priority == PriorityCOLD {
			continue
		}
		if m.reorg.SafetyBlocks > 0 && m.callbacks.HeadBlock != nil {
			if !m.reorg.SafeToFinalize(j.Candidate.ToBlock, m.callbacks.HeadBlock()) {
				continue // 最近区块仍在 Reorg Safety Window 内，暂不启动
			}
		}
		// Coverage 联动（设计 §26）：FULL HIT 无需下载
		if m.coverageFull(j.Candidate) {
			_ = m.queue.UpdateStatus(j.ID, StatusReady)
			continue
		}
		if err := m.startJob(ctx, j); err != nil {
			_ = m.queue.UpdateStatus(j.ID, StatusFailed)
			continue
		}
	}
	// 推进进行中/暂停任务
	m.reconcileJobs()
	return nil
}

func (m *Manager) startJob(ctx context.Context, j *Job) error {
	if m.callbacks.Create == nil || m.callbacks.Start == nil {
		return fmt.Errorf("prefetch: 批处理回调未配置")
	}
	req := smartdownload.CreateBatchRequest{
		ChainKey:  j.Candidate.ChainKey,
		Addresses: []string{j.Candidate.Address},
		Datasets:  j.Candidate.RequiredDatasets,
		DefaultRange: &smartdownload.RangeSpec{
			Mode:      smartdownload.RangeModeBlock,
			FromBlock: j.Candidate.FromBlock,
			ToBlock:   j.Candidate.ToBlock,
		},
		SkipCovered: boolPtr(true),
		Prefetch:    true,
		PrefetchPriority: priorityNum(j.Candidate.Priority),
	}
	resp, err := m.callbacks.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("prefetch: 创建批处理失败: %w", err)
	}
	if resp == nil || resp.Batch == nil {
		return fmt.Errorf("prefetch: 创建批处理返回为空")
	}
	if err := m.callbacks.Start(resp.Batch.ID); err != nil {
		return fmt.Errorf("prefetch: 启动批处理失败: %w", err)
	}
	if err := m.queue.SetBatch(j.ID, resp.Batch.ID); err != nil {
		return err
	}
	if m.active != nil {
		m.active.Acquire(j.Candidate.ChainKey, j.Candidate.Address, resp.Batch.ID,
			j.Candidate.FromBlock, j.Candidate.ToBlock)
	}
	if m.leases != nil {
		_ = m.leases.Acquire(j.ID, resp.Batch.ID)
	}
	if err := m.queue.UpdateStatus(j.ID, StatusPrefetching); err != nil {
		return err
	}
	return m.budget.Consume()
}

func (m *Manager) reconcileJobs() {
	for _, j := range m.queue.List() {
		if j.Status != StatusPrefetching && j.Status != StatusInteractive && j.Status != StatusPaused {
			continue
		}
		if j.BatchID == "" || m.callbacks.BatchStatus == nil {
			continue
		}
		status, terminal := m.callbacks.BatchStatus(j.BatchID)
		_ = m.queue.UpdateBatchStatus(j.ID, status)
		if terminal {
			if status == "COMPLETED" {
				_ = m.queue.UpdateStatus(j.ID, StatusReady)
			} else {
				_ = m.queue.UpdateStatus(j.ID, StatusFailed)
			}
			m.releaseJobResources(j)
			_ = m.budget.Release()
			_ = m.budget.RecordDownload(j.Candidate.EstimatedBytes, j.Candidate.EstimatedBytes)
			continue
		}
		if m.leases != nil {
			_ = m.leases.Renew(j.ID)
		}
		if j.Status == StatusPaused && m.callbacks.Resume != nil {
			_ = m.callbacks.Resume(j.BatchID)
			_ = m.queue.UpdateStatus(j.ID, StatusPrefetching)
		}
		if j.Status == StatusInteractive && m.callbacks.Resume != nil {
			_ = m.callbacks.Resume(j.BatchID)
			_ = m.queue.UpdateStatus(j.ID, StatusPrefetching)
		}
	}
	// 7 天未使用的 READY 数据 → 反馈降权 + 驱逐标记（设计 §34、§56-§57）
	cutoff := time.Now().UTC().Add(-UnusedTTL)
	for _, j := range m.queue.List() {
		if j.Status != StatusReady {
			continue
		}
		if j.FinishedAt != nil && j.FinishedAt.Before(cutoff) {
			_ = m.feedback.RecordUnused(j.Candidate.InvestigationID, j.Candidate.Address,
				j.BatchID, j.Candidate.EstimatedBytes)
			_ = m.queue.UpdateStatus(j.ID, StatusEvicted)
			m.releaseJobResources(j)
		}
	}
	// Progressive 7d/90d：READY 任务扩展下一阶段窗口
	if m.cfg.Progressive && len(m.cfg.ProgressiveStages) > 0 {
		for _, j := range m.queue.List() {
			if j.Status != StatusReady {
				continue
			}
			next, ok := NextStageCandidate(j.Candidate, m.cfg.ProgressiveStages)
			if !ok {
				continue
			}
			_, _, err := m.queue.Enqueue(next)
			if err == nil {
				_ = m.queue.UpdateStatus(j.ID, StatusReady) // 原任务保持 READY
			}
		}
	}
}

func (m *Manager) pauseAllPrefetch() {
	for _, j := range m.queue.List() {
		if (j.Status == StatusPrefetching || j.Status == StatusInteractive) && j.BatchID != "" {
			if m.callbacks.Pause != nil {
				_ = m.callbacks.Pause(j.BatchID)
			}
			_ = m.queue.UpdateStatus(j.ID, StatusPaused)
		}
	}
}

func (m *Manager) coverageFull(c Candidate) bool {
	if m.callbacks.CoverageQuery == nil {
		return false
	}
	for _, ds := range c.RequiredDatasets {
		ci := m.callbacks.CoverageQuery(c.ChainKey, c.Address, ds, c.FromBlock, c.ToBlock)
		if ci.Ratio < 1 {
			return false
		}
	}
	return true
}

func (m *Manager) releaseJobResources(j *Job) {
	if m.active != nil && j.BatchID != "" {
		m.active.Release(j.Candidate.ChainKey, j.Candidate.Address, j.BatchID,
			j.Candidate.FromBlock, j.Candidate.ToBlock)
	}
	if m.leases != nil {
		m.leases.Release(j.ID)
	}
}

func firstToken(tokens []string) string {
	for _, t := range tokens {
		if strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

func boolPtr(v bool) *bool {
	return &v
}

func priorityNum(p Priority) int {
	switch p {
	case PriorityHOT:
		return 3
	case PriorityWARM:
		return 4
	default:
		return 5
	}
}

type coverageAdapter struct {
	cb BatchCallbacks
}

func (a coverageAdapter) QueryCoverage(chainKey, address, dataset string, from, to uint64) graphcache.CoverageInfo {
	if a.cb.CoverageQuery == nil {
		return graphcache.CoverageInfo{}
	}
	return a.cb.CoverageQuery(chainKey, address, dataset, from, to)
}
