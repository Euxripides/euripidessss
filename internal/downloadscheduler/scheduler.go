package downloadscheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/cloudruntime"
	"github.com/etl/backend/internal/datasetsync"
	"github.com/etl/backend/internal/logger"
	"github.com/etl/backend/internal/objectiveplanner"
	"github.com/etl/backend/internal/parquetdownload"
	"github.com/google/uuid"
)

var evmAddressRE = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// Scheduler 智能下载调度器（设计文档 §4/§13）。
// 流程：分析需求（去重+预算裁剪）→ 覆盖检查 → Provider 评分选择 → 计划 → 状态机执行（重试/切换）→ 就绪。
type Scheduler struct {
	mu              sync.Mutex
	registry        *Registry
	coverage        *CoverageResolver
	plans           map[string]*Plan
	planDir         string
	budget          Budget
	recovery        RecoveryWriter     // RPC 恢复数据合并器（Token Transfer Recovery Layer，可为 nil）
	runningID       string             // 当前正在执行的计划（预算 MaxConcurrentPlans=1 串行）
	cancel          context.CancelFunc // 当前执行计划的取消函数（保留供将来 Cancel API）
	cloud           CloudRuntime       // SQD Cloud 应急运行时（可为 nil）
	health          *ProviderHealthTracker
	usage           *CloudUsageStore
	gate            *CloudAdmissionGate
	fault           FaultInjection
	syncer          *datasetsync.Syncer
	dsRegistry      *datasetsync.Registry
	syncMu          sync.Mutex
	dataIndexedHook func([]*datasetsync.Entry) // Phase 5：索引完成通知（Investigation/Graph）
}

// errTaskCancelled 用户/上游取消哨兵（Phase 5.4 §5：cancelled != failed）。
var errTaskCancelled = errors.New("任务已取消")

// NewScheduler 创建调度器。
// planDir 为计划持久化目录（如 backend/data/download_scheduler/plans）；为空则不落盘。
func NewScheduler(registry *Registry, coverage *CoverageResolver, planDir string, budget Budget) *Scheduler {
	if budget.MaxAddressesPerTask <= 0 {
		budget.MaxAddressesPerTask = 100
	}
	if budget.MaxTasksPerPlan <= 0 {
		budget.MaxTasksPerPlan = 5
	}
	if budget.MaxConcurrentPlans <= 0 {
		budget.MaxConcurrentPlans = 1
	}
	if budget.MaxRetriesPerTask <= 0 {
		budget.MaxRetriesPerTask = 1
	}
	s := &Scheduler{
		registry: registry,
		coverage: coverage,
		plans:    make(map[string]*Plan),
		planDir:  planDir,
		budget:   budget,
		health:   NewProviderHealthTracker(DefaultProviderHealthConfig()),
		usage:    NewCloudUsageStore(""),
	}
	s.gate = NewCloudAdmissionGate(s.usage, s.health, budget.Cloud)
	if planDir != "" {
		_ = os.MkdirAll(planDir, 0o755)
		s.loadPlans()
	}
	return s
}

// Budget 返回当前预算配置。
func (s *Scheduler) Budget() Budget {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.budget
}

// Registry 返回 Provider 注册表（供 API 层展示评分）。
func (s *Scheduler) Registry() *Registry { return s.registry }

// WithRecoveryWriter 注入恢复数据合并器（Token Transfer Recovery Layer §9 MERGING）。
func (s *Scheduler) WithRecoveryWriter(w RecoveryWriter) *Scheduler {
	s.recovery = w
	return s
}

// WithCloudFallback 注入 SQD Cloud 应急兜底（V3：Cloud Admission Gate + 用量审计 + 故障注入）。
func (s *Scheduler) WithCloudFallback(rt CloudRuntime, usage *CloudUsageStore, fault FaultInjection) *Scheduler {
	s.cloud = rt
	if usage != nil {
		s.usage = usage
	}
	s.fault = fault
	s.gate = NewCloudAdmissionGate(s.usage, s.health, s.budget.Cloud)
	return s
}

// WithDataPlane 注入 Cloud 数据面（Local Sync / Registry，Phase 4 §26/§30）。
func (s *Scheduler) WithDataPlane(syncer *datasetsync.Syncer, registry *datasetsync.Registry) *Scheduler {
	s.syncer = syncer
	s.dsRegistry = registry
	return s
}

// WithDataIndexedHook 注册索引完成钩子（Phase 5 §30/§32：自动继续调查、图谱缓存失效）。
func (s *Scheduler) WithDataIndexedHook(fn func([]*datasetsync.Entry)) *Scheduler {
	s.dataIndexedHook = fn
	return s
}

// Coverage 返回覆盖检查器。
func (s *Scheduler) Coverage() *CoverageResolver { return s.coverage }

// ProviderHealth 返回 Provider 健康快照（API /providers/health）。
func (s *Scheduler) ProviderHealth() map[ProviderKind]ProviderStateInfo {
	return s.health.Snapshot()
}

// CloudUsage 返回当日 Cloud 用量（API /cloud/runtime）。
func (s *Scheduler) CloudUsage() CloudUsage {
	if s.usage == nil {
		return CloudUsage{}
	}
	return s.usage.Usage()
}

// CloudRuntimeStatus 返回 Cloud 运行时状态快照（API /cloud/runtime）。
func (s *Scheduler) CloudRuntimeStatus() CloudRuntimeStatus {
	if s.cloud == nil {
		return CloudRuntimeStatus{
			State:     string(cloudruntime.WorkerNotConfigured),
			Available: false,
			Reason:    "SQD Cloud 运行时未装配",
		}
	}
	st := s.cloud.Status()
	rt := CloudRuntimeStatus{
		State:                   string(st.State),
		Mode:                    string(st.Mode),
		Available:               st.Available,
		Reason:                  st.Reason,
		QueuedJobs:              st.QueuedJobs,
		LeasedJobs:              st.LeasedJobs,
		RunningJob:              st.RunningJob,
		DeploymentKeyConfigured: st.DeploymentKeyConfigured,
		R2Configured:            st.R2Configured,
	}
	if st.FailureCooldownUntil != nil {
		rt.FailureCooldownUntil = st.FailureCooldownUntil.Format(time.RFC3339)
	}
	return rt
}

// CancelCloudJob 写入 Cloud Cancel Marker（Phase 5.2 §6）。
func (s *Scheduler) CancelCloudJob(ctx context.Context, id string) error {
	if s.cloud == nil {
		return errors.New("SQD Cloud 运行时未装配")
	}
	return s.cloud.CancelJob(ctx, id)
}

// CloudJobs 返回已知 Cloud 任务（API /cloud/jobs）。
func (s *Scheduler) CloudJobs() []cloudruntime.Job {
	if s.cloud == nil {
		return nil
	}
	if lister, ok := s.cloud.(interface{ Jobs() []cloudruntime.Job }); ok {
		return lister.Jobs()
	}
	return nil
}

// CloudSync 触发 Local Sync（API POST /cloud/sync + Phase 5 自动同步共用）。
// 互斥防止事件触发与后台轮询并发扫描；索引完成后触发 dataIndexedHook。
func (s *Scheduler) CloudSync(ctx context.Context) ([]datasetsync.SyncResult, error) {
	if s.syncer == nil {
		return nil, nil
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	results, err := s.syncer.SyncAll(ctx)
	if err == nil && s.dataIndexedHook != nil {
		var entries []*datasetsync.Entry
		for _, r := range results {
			// 只有本轮真正完成本地校验和索引的结果才能触发覆盖/预取升级。
			// FAILED 结果也会出现在 SyncAll 返回列表中；把它们送入 hook 会让
			// 下游错误地对账未认证数据，历史坏 manifest 较多时还会阻塞请求。
			if r.Skipped || r.Status != "INDEXED" {
				continue
			}
			if e := s.dsRegistry.Get(r.ChunkKey); e != nil {
				entries = append(entries, e)
			}
		}
		if len(entries) > 0 {
			s.dataIndexedHook(entries)
		}
	}
	return results, err
}

// StartAutoSync 启动后台自动同步轮询（Phase 5 §23：事件 + polling 双保险）。
func (s *Scheduler) StartAutoSync(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.CloudSync(ctx); err != nil {
					logger.Log.Warn().Err(err).Msg("scheduler_cloud_auto_sync_failed")
				}
			}
		}
	}()
}

// CloudFallbackRatio Cloud 兜底占比（Phase 5 §40 KPI：目标尽量低）。
func (s *Scheduler) CloudFallbackRatio() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	total, cloud := len(s.plans), 0
	for _, p := range s.plans {
		if p.Cloud != nil && p.Cloud.AdmittedTasks > 0 {
			cloud++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(cloud) / float64(total)
}

// CloudRegistry 返回 Cloud 数据集登记表。
func (s *Scheduler) CloudRegistry() *datasetsync.Registry {
	return s.dsRegistry
}

// Plan 返回指定计划副本（nil 表示不存在）。
func (s *Scheduler) Plan(id string) *Plan {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.plans[id]; ok {
		return clonePlan(p)
	}
	return nil
}

// Plans 返回全部计划（新→旧）。
func (s *Scheduler) Plans() []*Plan {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Plan, 0, len(s.plans))
	for _, p := range s.plans {
		out = append(out, clonePlan(p))
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt.After(out[i].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Running 返回正在运行的计划 ID（空 = 无）。
func (s *Scheduler) Running() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runningID
}

// Submit 分析需求并生成计划（状态机 ANALYZING→SELECTING→BUILDING）。
// 不做覆盖剔除（前端先展示覆盖结果，由调用方决定是否跳过已覆盖数据集）；
// 预算裁剪与 V3 活跃度分桶（§7 chunk 智能调整）在此执行。
func (s *Scheduler) Submit(ctx context.Context, reqs []Requirement) (*Plan, error) {
	if len(reqs) == 0 {
		return nil, errors.New("没有数据需求")
	}
	now := time.Now()
	plan := &Plan{
		ID:        uuid.NewString(),
		Status:    StatusAnalyzing,
		Budget:    s.Budget(),
		CreatedAt: now,
	}
	// 预算：任务数封顶、地址裁剪去重；同 (dataset, chain) 合并地址为单任务
	type groupKey struct {
		dataset Dataset
		chain   string
	}
	groups := map[groupKey][]string{}
	groupDates := map[groupKey]struct{ start, end string }{}
	groupBlocks := map[groupKey]struct{ from, to uint64 }{}
	groupCloudEligible := map[groupKey]*bool{}
	order := []groupKey{}
	// Phase 5.4 §7-§9：Objective 驱动展开为数据集需求（目标决定数据，不指定 Provider）
	var expanded []Requirement
	for _, r := range reqs {
		if strings.TrimSpace(r.ObjectiveType) != "" {
			obj := objectiveplanner.Objective{
				Type:        r.ObjectiveType,
				Description: r.ObjectiveDescription,
				Constraints: r.ObjectiveConstraints,
			}
			pl, err := objectiveplanner.Build(obj, r.ChainKey, r.Addresses,
				r.FromBlock, r.ToBlock, 200, 1000, 50)
			if err != nil {
				return nil, err
			}
			if pl.Estimate.Rejected {
				return nil, errors.New(pl.Estimate.RejectReason)
			}
			for _, need := range pl.Needs {
				cp := r
				cp.Dataset = Dataset(need.Dataset)
				cp.Direction = need.Direction
				if !need.CloudEligible {
					f := false
					cp.CloudEligible = &f
				}
				expanded = append(expanded, cp)
			}
			continue
		}
		expanded = append(expanded, r)
	}
	for _, r := range expanded {
		r.ChainKey = strings.ToLower(strings.TrimSpace(r.ChainKey))
		if r.ChainKey == "" {
			r.ChainKey = "bsc"
		}
		if _, err := chain.Resolve(r.ChainKey); err != nil {
			continue // 未知链直接丢弃
		}
		if !ValidDataset(r.Dataset) {
			continue
		}
		k := groupKey{dataset: r.Dataset, chain: r.ChainKey}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r.Addresses...)
		// 保留首个非空日期范围（RPC 恢复通道按此估算区块窗口）
		dates := groupDates[k]
		if dates.start == "" && r.StartDate != "" {
			dates.start = r.StartDate
		}
		if dates.end == "" && r.EndDate != "" {
			dates.end = r.EndDate
		}
		groupDates[k] = dates
		if groupCloudEligible[k] == nil && r.CloudEligible != nil {
			groupCloudEligible[k] = r.CloudEligible
		}
		blocks := groupBlocks[k]
		if r.FromBlock > 0 && (blocks.from == 0 || r.FromBlock < blocks.from) {
			blocks.from = r.FromBlock
		}
		if r.ToBlock > blocks.to {
			blocks.to = r.ToBlock
		}
		groupBlocks[k] = blocks
	}
	taskID := 0
	for _, k := range order {
		if len(plan.Tasks) >= plan.Budget.MaxTasksPerPlan {
			break
		}
		addresses := trimAddresses(groups[k], plan.Budget.MaxAddressesPerTask)
		if len(addresses) == 0 {
			continue
		}
		// V3 §7：按地址活跃度分桶 → chunk 切片（交易类数据集查活跃度；余额/标签按普通档）
		var buckets []activityBucket
		if k.dataset == DatasetTransactions || k.dataset == DatasetTokenTransfer {
			buckets = bucketByActivity(func(addr string) int64 { return s.coverage.TxCount(ctx, addr) }, addresses)
		} else {
			n := (len(addresses) + ChunkSizeFor(ActivityNormal) - 1) / ChunkSizeFor(ActivityNormal)
			buckets = []activityBucket{{level: ActivityNormal, addrs: addresses, chunks: n}}
		}
		for _, b := range buckets {
			chunk := ChunkSizeFor(b.level)
			for i := 0; i < len(b.addrs); i += chunk {
				if len(plan.Tasks) >= plan.Budget.MaxTasksPerPlan {
					logger.Log.Warn().Str("plan_id", plan.ID).Str("dataset", string(k.dataset)).
						Str("chain", k.chain).Int("remaining_addresses", len(b.addrs)-i).
						Int("max_tasks", plan.Budget.MaxTasksPerPlan).
						Msg("scheduler_budget_truncated_addresses")
					break
				}
				end := i + chunk
				if end > len(b.addrs) {
					end = len(b.addrs)
				}
				taskID++
				dates := groupDates[k]
				blocks := groupBlocks[k]
				r := Requirement{
					ID:            fmt.Sprintf("%s-%d", plan.ID, taskID),
					PlanID:        plan.ID,
					Dataset:       k.dataset,
					ChainKey:      k.chain,
					Addresses:     b.addrs[i:end],
					StartDate:     dates.start,
					EndDate:       dates.end,
					FromBlock:     blocks.from,
					ToBlock:       blocks.to,
					CloudEligible: groupCloudEligible[k],
					Note:          fmt.Sprintf("活跃度 %s（chunk %d）", b.level, end-i),
				}
				plan.Tasks = append(plan.Tasks, &PlanTask{
					ID:          r.ID,
					Requirement: r,
					Status:      "pending",
					Progress:    0,
				})
			}
		}
	}
	if len(plan.Tasks) == 0 {
		return nil, errors.New("需求裁剪后没有可执行任务（检查地址/数据集/预算）")
	}
	// SELECTING_PROVIDER：为每个任务选择 Provider
	plan.Status = StatusSelecting
	for _, t := range plan.Tasks {
		candidates := s.registry.Candidates(t.Requirement.Dataset)
		// AWS 数据源仅 BSC：非 BSC 链的 transactions 任务剔除 AWS 候选（避免选后失败再切换）
		if t.Requirement.ChainKey != "bsc" {
			filtered := make([]ProviderScore, 0, len(candidates))
			for _, c := range candidates {
				if c.Provider != ProviderAWS {
					filtered = append(filtered, c)
				}
			}
			candidates = filtered
		}
		t.Candidates = candidates
		score, provider := s.registry.SelectFrom(candidates)
		if provider == nil {
			t.Status = "failed"
			t.Error = "没有可用的 Provider"
			continue
		}
		t.Provider = score.Provider
		if score.ManualOnly {
			t.Status = "skipped"
			t.Error = "该数据集需要人工采集（浏览器登录态）：请在「虚拟币-数据下载」页手动执行"
			continue
		}
		t.Status = "pending"
	}
	plan.Status = StatusBuilding
	plan.StageDetail = "下载计划已生成"

	s.mu.Lock()
	s.plans[plan.ID] = plan
	s.mu.Unlock()
	s.persist(plan)
	logger.Log.Info().Str("plan_id", plan.ID).Int("tasks", len(plan.Tasks)).Msg("scheduler_plan_built")
	return clonePlan(plan), nil
}

// Run 异步执行计划（EXECUTING→RETRYING/FALLBACK→VALIDATING→MERGING→READY_FOR_GRAPH/FAILED）。
// 注意：执行生命周期独立于传入的 ctx（HTTP 请求结束不会中断下载）；ctx 仅作预算检查用。
func (s *Scheduler) Run(ctx context.Context, planID string) error {
	s.mu.Lock()
	if s.runningID != "" && s.runningID != planID {
		s.mu.Unlock()
		return fmt.Errorf("已有计划 %s 正在执行（预算 MaxConcurrentPlans=%d）", s.runningID, s.budget.MaxConcurrentPlans)
	}
	plan, ok := s.plans[planID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("计划 %s 不存在", planID)
	}
	if plan.Status.Terminal() {
		s.mu.Unlock()
		return fmt.Errorf("计划 %s 已处于终态 %s", planID, plan.Status)
	}
	// 防重复执行：执行中的中间态（EXECUTING/RETRYING/FALLBACK/VALIDATING/MERGING）拒绝再次 Run
	if !runnableStatus(plan.Status) {
		s.mu.Unlock()
		return fmt.Errorf("计划 %s 正在执行（%s），不能重复启动", planID, plan.Status)
	}
	plan.Status = StatusExecuting
	plan.StageDetail = "开始执行"
	now := time.Now()
	plan.StartedAt = &now
	s.runningID = planID
	s.mu.Unlock()
	s.persist(plan)

	// 执行生命周期独立于调用方上下文（HTTP 请求结束/取消不中断下载）；
	// 保留独立 cancel 供将来的取消 API 使用。
	execCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.cancel = cancel
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			if s.runningID == planID {
				s.runningID = ""
				s.cancel = nil
			}
			s.mu.Unlock()
		}()
		s.execute(execCtx, plan)
	}()
	return nil
}

// runnableStatus 判断计划状态是否可启动执行（仅未开始的中间态可运行）。
func runnableStatus(st PlanStatus) bool {
	switch st {
	case StatusAnalyzing, StatusSelecting, StatusBuilding:
		return true
	}
	return false
}

// updatePlan 在互斥锁内修改计划状态并持久化（执行 goroutine 专用）。
// 锁保护 plan/task 字段的写-读（Plan()/Plans() 在锁内 clonePlan 深拷贝）。
func (s *Scheduler) updatePlan(plan *Plan, fn func(*Plan)) {
	s.mu.Lock()
	fn(plan)
	s.mu.Unlock()
	s.persist(plan)
}

// execute 串行执行计划内全部任务（parquetdownload 单任务并发限制）。
// 所有计划状态修改经 updatePlan 持锁（与 Plan()/Plans() 的 clonePlan 读互斥）。
func (s *Scheduler) execute(ctx context.Context, plan *Plan) {
	failures := 0
	for _, t := range plan.Tasks {
		if err := ctx.Err(); err != nil {
			s.updatePlan(plan, func(p *Plan) {
				p.Status = StatusFailed
				p.Error = "执行被取消"
				p.StageDetail = "执行被取消"
			})
			return
		}
		if t.Status == "skipped" || t.Status == "done" {
			continue
		}
		if t.Status == "failed" {
			failures++
			continue
		}
		// 重试 + 切换候选（Layer 3 简单仲裁：固定重试次数后切到下一候选）
		s.updatePlan(plan, func(p *Plan) {
			t.Status = "running"
			started := time.Now()
			t.StartedAt = &started
		})

		attempt := s.executeTaskWithFallback(ctx, plan, t)
		s.updatePlan(plan, func(p *Plan) {
			finished := time.Now()
			t.FinishedAt = &finished
			if attempt != nil {
				t.Status = "done"
				t.Result = attempt
				p.Status = StatusValidating
				p.StageDetail = fmt.Sprintf("任务 %s 完成（%s）", t.ID, t.Provider)
			} else if t.Status == "cancelled" {
				p.Status = StatusCancelled
				p.StageDetail = fmt.Sprintf("任务 %s 已取消（用户/上游取消）", t.ID)
			} else {
				t.Status = "failed"
				failures++
				p.Status = StatusFailed
				p.StageDetail = fmt.Sprintf("任务 %s 失败：%s", t.ID, lastError(t))
			}
		})
	}
	// MERGING：合并数据资产（RPC 恢复层 §9/§10：恢复数据 + 仓库数据按唯一键合并去重）
	s.updatePlan(plan, func(p *Plan) {
		if p.Status != StatusFailed && p.Status != StatusCancelled {
			p.Status = StatusMerging
			p.StageDetail = "合并数据资产（唯一化）"
		}
	})
	s.mergeRecoveryData(ctx, plan)
	done := 0
	cancelled := 0
	for _, t := range plan.Tasks {
		if t.Status == "done" {
			done++
		}
		if t.Status == "cancelled" {
			cancelled++
		}
	}
	s.updatePlan(plan, func(p *Plan) {
		if cancelled > 0 {
			p.Status = StatusCancelled
			p.StageDetail = fmt.Sprintf("%d 个任务已取消（成功 %d）", cancelled, done)
		} else if failures == 0 {
			p.Status = StatusReady
			p.StageDetail = fmt.Sprintf("全部 %d 个任务就绪，数据已可供图谱使用", done)
		} else {
			p.Status = StatusFailed
			p.StageDetail = fmt.Sprintf("%d 个任务失败（成功 %d）", failures, done)
		}
		finished := time.Now()
		p.FinishedAt = &finished
	})
	logger.Log.Info().Str("plan_id", plan.ID).Str("status", string(plan.Status)).
		Int("done", done).Int("failed", failures).Msg("scheduler_plan_finished")
}

// mergeRecoveryData MERGING 阶段：对已完成 token_transfer 任务的链执行恢复数据合并
// （锁外 I/O：恢复 parquet + 仓库既有 token_transfers 按唯一键去重，结果写回计划）。
func (s *Scheduler) mergeRecoveryData(ctx context.Context, plan *Plan) {
	if s.recovery == nil {
		return
	}
	// 锁内收集需要合并的链（仅计划未失败时）
	s.mu.Lock()
	if plan.Status == StatusFailed || plan.Status == StatusCancelled {
		s.mu.Unlock()
		return
	}
	var chains []string
	seen := map[string]bool{}
	for _, t := range plan.Tasks {
		if t.Status == "done" && t.Requirement.Dataset == DatasetTokenTransfer && !seen[t.Requirement.ChainKey] {
			seen[t.Requirement.ChainKey] = true
			chains = append(chains, t.Requirement.ChainKey)
		}
	}
	s.mu.Unlock()
	if len(chains) == 0 {
		return
	}
	for _, chainKey := range chains {
		network, err := chain.Resolve(chainKey)
		if err != nil {
			continue
		}
		stats, mergeErr := s.recovery.MergeTokenTransfers(ctx, plan.ID, network)
		if mergeErr != nil {
			logger.Log.Info().Str("plan_id", plan.ID).Str("chain", chainKey).Err(mergeErr).Msg("scheduler_recovery_merge_skipped")
			continue
		}
		s.updatePlan(plan, func(p *Plan) {
			p.Recovery = append(p.Recovery, stats)
			p.StageDetail = fmt.Sprintf("合并完成：恢复 %d 行 + 仓库 %d 行 → 唯一 %d 行（去重 %d）",
				stats.RecoveryRows, stats.WarehouseRows, stats.MergedRows, stats.DuplicateRows)
		})
	}
}

// executeTaskWithFallback 执行单个任务：最多重试 MaxRetriesPerTask 次，然后切换下一候选 Provider；
// 全部常规 Provider 耗尽后进入 Cloud Admission Gate（设计 §15/§18/§19）。
func (s *Scheduler) executeTaskWithFallback(ctx context.Context, plan *Plan, t *PlanTask) *TaskResult {
	candidates := t.Candidates
	if len(candidates) == 0 {
		candidates = s.registry.Candidates(t.Requirement.Dataset)
	}
	// 从当前选中 Provider 开始尝试
	startIdx := 0
	for i, c := range candidates {
		if c.Provider == t.Provider {
			startIdx = i
			break
		}
	}
	if s.fault.AllNormalProvidersFail {
		startIdx = len(candidates)
	}
	for i := startIdx; i < len(candidates); i++ {
		c := candidates[i]
		if c.ManualOnly {
			continue
		}
		provider := s.registry.byKind(c.Provider)
		if provider == nil {
			continue
		}
		maxRetries := plan.Budget.MaxRetriesPerTask
		for attempt := 0; attempt <= maxRetries; attempt++ {
			if attempt > 0 {
				s.updatePlan(plan, func(p *Plan) {
					t.Retries++
					p.Status = StatusRetrying
					p.StageDetail = fmt.Sprintf("任务 %s 重试 %d/%d（Provider=%s）", t.ID, attempt, maxRetries, c.Provider)
				})
			}
			if i > startIdx {
				s.updatePlan(plan, func(p *Plan) {
					p.Status = StatusFallback
					p.StageDetail = fmt.Sprintf("任务 %s 切换 Provider：%s → %s", t.ID, t.Provider, c.Provider)
					t.Provider = c.Provider
					t.Error = ""
				})
			}
			attemptStart := time.Now()
			result, err := provider.Execute(ctx, t.Requirement)
			state := ProviderHealthy
			if err != nil {
				state = ClassifyProviderError(err, 0)
			}
			var rows int64
			if err == nil && result != nil {
				rows = result.Rows
			}
			if err == nil {
				// 注意：t.Provider 已在 FALLBACK 分支（updatePlan 持锁）设置，此处无需再写
				// SQD 异步任务：轮询直到终态
				if result.JobID != "" {
					if err := s.waitSQDJob(ctx, plan, t, result.JobID, result); err != nil {
						if errors.Is(err, errTaskCancelled) {
							s.updatePlan(plan, func(p *Plan) {
								t.Status = "cancelled"
								t.Error = err.Error()
								p.Status = StatusCancelled
								p.StageDetail = "任务已取消（用户/上游取消）"
							})
							return nil
						}
						state = ClassifyProviderError(err, 0)
						s.health.RecordResult(c.Provider, false, state)
						s.appendAttempt(plan, t, ProviderAttempt{
							Provider: c.Provider, Tier: c.Tier, StartedAt: attemptStart,
							FinishedAt: time.Now(), Success: false, State: state,
							Error: sanitizeError(err), Rows: rows,
							LatencyMS: time.Since(attemptStart).Milliseconds(),
						})
						if attempt < maxRetries || i < len(candidates)-1 {
							continue // 重试或切换
						}
						s.updatePlan(plan, func(p *Plan) { t.Error = err.Error() })
						return nil
					}
				}
				s.health.RecordResult(c.Provider, true, ProviderHealthy)
				s.appendAttempt(plan, t, ProviderAttempt{
					Provider: c.Provider, Tier: c.Tier, StartedAt: attemptStart,
					FinishedAt: time.Now(), Success: true, State: ProviderHealthy,
					Rows: rows, LatencyMS: time.Since(attemptStart).Milliseconds(),
				})
				return result
			}
			s.health.RecordResult(c.Provider, false, state)
			s.appendAttempt(plan, t, ProviderAttempt{
				Provider: c.Provider, Tier: c.Tier, StartedAt: attemptStart,
				FinishedAt: time.Now(), Success: false, State: state,
				Error: sanitizeError(err), Rows: rows,
				LatencyMS: time.Since(attemptStart).Milliseconds(),
			})
			s.updatePlan(plan, func(p *Plan) { t.Error = sanitizeError(err) })
			if attempt >= maxRetries {
				break
			}
		}
	}
	// 全部常规 Provider 耗尽 → Cloud Admission Gate（设计 §15）
	return s.tryCloudFallback(ctx, plan, t, candidates)
}

// appendAttempt 记录 Provider 调用审计（provider_attempts，设计 §21）。
func (s *Scheduler) appendAttempt(plan *Plan, t *PlanTask, a ProviderAttempt) {
	s.updatePlan(plan, func(p *Plan) {
		t.Attempts = append(t.Attempts, a)
	})
}

// tryCloudFallback 常规 Provider 全部耗尽后的应急 Cloud 通道（设计 §14/§15/§19）。
// Cloud 只执行缺失 Chunk；拒绝时任务失败并给出可审计原因。
func (s *Scheduler) tryCloudFallback(ctx context.Context, plan *Plan, t *PlanTask, candidates []ProviderScore) *TaskResult {
	if s.cloud == nil {
		s.updatePlan(plan, func(p *Plan) {
			t.Status = "failed"
			t.Error = "应急 Cloud 未装配（运行时不可用）"
			p.Status = StatusWaitingRetry
			p.StageDetail = "常规数据源均不可用，且 Cloud 运行时未装配"
		})
		return nil
	}
	coverage, _ := s.coverage.CheckRequirement(ctx, t.Requirement)
	states := s.health.Snapshot()
	if s.fault.AllNormalProvidersFail {
		// 故障注入：模拟全部常规 Provider 熔断（设计 §96，仅测试环境）
		for _, c := range candidates {
			if c.Tier < TierEmergencyCloud {
				states[c.Provider] = ProviderStateInfo{
					Provider: c.Provider, State: ProviderCircuitOpen,
					Reasons: []string{"故障注入：模拟常规 Provider 全部不可用"},
				}
			}
		}
	}
	decision := s.gate.CanUseSQDCloud(t.Requirement, coverage, candidates, states, s.CloudRuntimeStatus())
	if !decision.Allowed {
		s.updatePlan(plan, func(p *Plan) {
			t.Status = "failed"
			t.Error = "应急 Cloud 未启用：" + decision.Reason
			t.Cloud = &CloudTaskInfo{Decision: decision}
			if p.Cloud == nil {
				p.Cloud = &CloudRunInfo{}
			}
			p.Cloud.RejectedTasks++
			p.Cloud.RejectReasons = appendUnique(p.Cloud.RejectReasons, decision.Reason)
			p.Status = StatusWaitingRetry
			p.StageDetail = "任务失败：" + decision.Reason
		})
		logger.Log.Warn().Str("plan_id", plan.ID).Str("task", t.ID).Str("reason", decision.Reason).
			Msg("scheduler_cloud_rejected")
		return nil
	}

	s.updatePlan(plan, func(p *Plan) {
		t.Status = "running"
		t.Provider = ProviderSQDCloud
		t.Cloud = &CloudTaskInfo{Decision: decision}
		if p.Cloud == nil {
			p.Cloud = &CloudRunInfo{}
		}
		p.Cloud.AdmittedTasks++
		p.Status = StatusCloudAdmission
		p.StageDetail = "常规数据源均不可用，系统已自动启用应急 Cloud 数据通道"
	})
	logger.Log.Warn().Str("plan_id", plan.ID).Str("task", t.ID).Str("reason", decision.Reason).
		Msg("scheduler_cloud_admitted")

	attemptStart := time.Now()
	provider := s.registry.byKind(ProviderSQDCloud)
	if provider == nil {
		s.updatePlan(plan, func(p *Plan) { t.Error = "Cloud Provider 未注册"; t.Status = "failed"; p.Status = StatusFailed })
		return nil
	}
	result, err := provider.Execute(ctx, t.Requirement)
	state := ProviderHealthy
	if err != nil {
		state = ClassifyProviderError(err, 0)
	} else {
		s.updatePlan(plan, func(p *Plan) {
			p.Status = StatusCloudQueued
			t.JobID = result.JobID
			t.Cloud.JobID = result.JobID
			p.StageDetail = "应急 Cloud 任务已提交，等待 Worker"
		})
		if waitErr := s.waitSQDJob(ctx, plan, t, result.JobID, result); waitErr != nil {
			if errors.Is(waitErr, errTaskCancelled) {
				s.updatePlan(plan, func(p *Plan) {
					t.Status = "cancelled"
					t.Error = "应急 Cloud 任务已取消"
					p.Status = StatusCancelled
					p.StageDetail = "应急 Cloud 任务已取消"
				})
				return nil
			}
			err = waitErr
			state = ClassifyProviderError(waitErr, 0)
			s.updatePlan(plan, func(p *Plan) { p.Status = StatusCloudRunning })
		}
	}
	success := err == nil
	s.health.RecordResult(ProviderSQDCloud, success, state)
	var rows int64
	var output string
	var jobDurationMin int
	if result != nil {
		rows = result.Rows
		if job, jobErr := s.cloud.JobStatus(result.JobID); jobErr == nil {
			output = job.OutputDir
			rows = job.Rows
			if job.StartedAt != nil && job.FinishedAt != nil {
				d := int(job.FinishedAt.Sub(*job.StartedAt).Minutes())
				if d < 1 {
					d = 1
				}
				jobDurationMin = d
			}
		}
	}
	s.appendAttempt(plan, t, ProviderAttempt{
		Provider: ProviderSQDCloud, Tier: TierEmergencyCloud, StartedAt: attemptStart,
		FinishedAt: time.Now(), Success: success, State: state,
		Error: func() string {
			if err != nil {
				return sanitizeError(err)
			}
			return ""
		}(), Rows: rows, LatencyMS: time.Since(attemptStart).Milliseconds(),
	})
	if s.usage != nil && result != nil {
		_ = s.usage.Record(CloudUsageRecord{
			JobID: result.JobID, PlanID: plan.ID, TaskID: t.ID,
			Mode: s.CloudRuntimeStatus().Mode, StartedAt: attemptStart,
			FinishedAt: time.Now(), DurationMinutes: jobDurationMin,
			Rows: rows, Output: output, Success: success,
		})
	}
	if !success {
		s.updatePlan(plan, func(p *Plan) {
			t.Status = "failed"
			t.Error = "应急 Cloud 任务失败：" + sanitizeError(err)
			if t.Cloud != nil {
				t.Cloud.Output = output
			}
			p.Status = StatusWaitingRetry
			p.StageDetail = "应急 Cloud 任务失败：" + sanitizeError(err)
		})
		return nil
	}
	s.updatePlan(plan, func(p *Plan) {
		t.Status = "done"
		t.Result = result
		t.Result.Rows = rows
		t.Result.Summary = fmt.Sprintf("应急 Cloud 完成：%d 行 Token Transfer（job=%s，输出 %s）", rows, result.JobID, output)
		if t.Cloud != nil {
			t.Cloud.Output = output
			t.Cloud.Mode = s.CloudRuntimeStatus().Mode
		}
		p.Status = StatusValidating
		p.StageDetail = fmt.Sprintf("应急 Cloud 任务 %s 完成，开始校验", result.JobID)
	})
	// Phase 5 §22-23：Cloud 导出完成后事件触发自动同步（与任务状态解耦，同步失败不回滚 Cloud 结果）。
	if s.syncer != nil {
		go func() {
			if _, err := s.CloudSync(context.Background()); err != nil {
				logger.Log.Warn().Str("plan_id", plan.ID).Err(err).Msg("scheduler_cloud_sync_after_job_failed")
			}
		}()
	}
	return result
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// waitSQDJob 轮询下游 Parquet 任务直到终态（VALIDATING 阶段；SQD/AWS Provider 统一轮询）。
func (s *Scheduler) waitSQDJob(ctx context.Context, plan *Plan, t *PlanTask, jobID string, result *TaskResult) error {
	s.updatePlan(plan, func(p *Plan) {
		t.JobID = jobID
		p.Status = StatusValidating
		p.StageDetail = fmt.Sprintf("任务 %s 等待 Parquet 任务 %s 完成", t.ID, jobID)
	})
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var poller jobPoller
	for _, pr := range s.registry.All() {
		jp, ok := pr.(jobPoller)
		if !ok {
			continue
		}
		if pr.Kind() == t.Provider { // 优先用与任务相同的 Provider 轮询
			poller = jp
			break
		}
		if poller == nil {
			poller = jp
		}
	}
	if poller == nil {
		return errors.New("没有可用的任务进度轮询器")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			progress, status, err := poller.JobProgress(ctx, jobID)
			if err != nil {
				return fmt.Errorf("查询任务进度失败: %s", sanitizeError(err))
			}
			s.updatePlan(plan, func(p *Plan) {
				t.Progress = progress
			})
			switch status {
			case parquetdownload.StatusDone:
				s.updatePlan(plan, func(p *Plan) {
					t.Progress = 1
					result.Summary = fmt.Sprintf("Parquet 任务 %s 完成（进度 100%%）", jobID)
				})
				return nil
			case parquetdownload.StatusFailed:
				return fmt.Errorf("Parquet 任务 %s 结束于 %s", jobID, status)
			case parquetdownload.StatusCanceled:
				return errTaskCancelled // cancelled != failed（Phase 5.4 §5）
			case parquetdownload.StatusSkipped:
				return fmt.Errorf("Parquet 任务 %s 被跳过（无可用数据源）", jobID)
			}
		}
	}
}

// trimAddresses 校验 + 去重 + 预算裁剪。
func trimAddresses(addresses []string, max int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(addresses))
	for _, a := range addresses {
		a = strings.ToLower(strings.TrimSpace(a))
		if !evmAddressRE.MatchString(a) || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
		if len(out) >= max {
			break
		}
	}
	return out
}

func lastError(t *PlanTask) string {
	if t.Error != "" {
		return t.Error
	}
	return "未知错误"
}

// sanitizeError 错误脱敏：RPC 节点地址/密钥不回传。
func sanitizeError(err error) string {
	msg := err.Error()
	for _, marker := range []string{"http://", "https://", "ws://", "wss://"} {
		if idx := strings.Index(msg, marker); idx >= 0 {
			msg = msg[:idx] + "[RPC 端点已脱敏]"
			break
		}
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}

// clonePlan 深拷贝计划（map 浅拷贝即可，任务指针复制）。
func clonePlan(p *Plan) *Plan {
	if p == nil {
		return nil
	}
	cp := *p
	cp.Tasks = make([]*PlanTask, len(p.Tasks))
	for i, t := range p.Tasks {
		ct := *t
		ct.Candidates = append([]ProviderScore(nil), t.Candidates...)
		if t.Result != nil {
			r := *t.Result
			ct.Result = &r
		}
		if t.StartedAt != nil {
			v := *t.StartedAt
			ct.StartedAt = &v
		}
		if t.FinishedAt != nil {
			v := *t.FinishedAt
			ct.FinishedAt = &v
		}
		ct.Attempts = append([]ProviderAttempt(nil), t.Attempts...)
		if t.Cloud != nil {
			c := *t.Cloud
			c.Decision.ProviderStates = map[string]string{}
			for k, v := range t.Cloud.Decision.ProviderStates {
				c.Decision.ProviderStates[k] = v
			}
			ct.Cloud = &c
		}
		cp.Tasks[i] = &ct
	}
	if p.StartedAt != nil {
		v := *p.StartedAt
		cp.StartedAt = &v
	}
	if p.FinishedAt != nil {
		v := *p.FinishedAt
		cp.FinishedAt = &v
	}
	if p.Cloud != nil {
		c := *p.Cloud
		c.RejectReasons = append([]string(nil), p.Cloud.RejectReasons...)
		cp.Cloud = &c
	}
	return &cp
}

// ── 持久化（backend/data/download_scheduler/plans/{id}.json）──

func (s *Scheduler) persist(plan *Plan) {
	if s.planDir == "" {
		return
	}
	payload, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(s.planDir, plan.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func (s *Scheduler) loadPlans() {
	entries, err := os.ReadDir(s.planDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(s.planDir, e.Name()))
		if err != nil {
			continue
		}
		var p Plan
		if err := json.Unmarshal(payload, &p); err != nil {
			continue
		}
		// 重启恢复：未到达终态的计划标记为失败（无断点续跑；避免 stuck EXECUTING 被重复执行）
		if !p.Status.Terminal() {
			now := time.Now()
			p.Status = StatusFailed
			p.StageDetail = "服务重启，计划未完成"
			p.FinishedAt = &now
			for _, t := range p.Tasks {
				if t.Status != "done" && t.Status != "failed" && t.Status != "skipped" {
					t.Status = "failed"
					t.Error = "服务重启，任务未完成"
				}
			}
			s.persist(&p)
		}
		s.plans[p.ID] = &p
	}
}
