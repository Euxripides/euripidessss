package smartdownload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/logger"
	"github.com/etl/backend/internal/smartdownload/cloudplanner"
	"github.com/etl/backend/internal/smartdownload/discovery"
	"github.com/etl/backend/internal/smartdownload/feedback"
	pg "github.com/etl/backend/internal/smartdownload/progress"
	reg "github.com/etl/backend/internal/smartdownload/registry"
	v3 "github.com/etl/backend/internal/smartdownload/validation"
	"github.com/google/uuid"
)

var evmAddressRE = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// Options 服务配置。
type Options struct {
	Workers         int    // 单批次并发 Worker 数
	RetryLimit      int    // 单个 Range 重试上限
	DefaultEndBlock uint64 // FULL 模式未显式给 to_block 时的默认终点
	RangeChunkSize  uint64 // Range 切分大小（区块数）
	AdaptiveRanges  bool   // Discovery 驱动自适应 Range（默认固定分块）
}

// DefaultOptions 返回 Phase 1 默认值。
func DefaultOptions() Options {
	return Options{
		Workers:         4,
		RetryLimit:      2,
		DefaultEndBlock: 50_000_000,
		RangeChunkSize:  50_000,
	}
}

// Service SmartDownloadService（实施方案 §1：任务层是系统中心）。
type Service struct {
	mu            sync.Mutex
	store         *Store
	cp            *CheckpointStore
	opts          Options
	writer        PartWriter
	adapters      map[string]ProviderAdapter
	scheduler     *SmartScheduler
	validator     *Validator
	events        *EventBus
	eta           map[string]*etaState
	etaEngines    map[string]*pg.ETAEngine
	duckdbEngine  *duckdb.Engine
	rangeCoverage RangeCoverageSource
	results       *ResultProcessor
	onIndexed     func(*IndexedResult)
	cloudPlanner  *cloudplanner.Planner
	history       *feedback.History
	coverageIndex *reg.Store
	ctx           context.Context
	cancel        context.CancelFunc
	workers       map[string]bool
	cpCache       map[string]*CheckpointV3
	wg            sync.WaitGroup
}

// NewService 创建服务。
func NewService(store *Store, opts Options, writer PartWriter) *Service {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.RetryLimit < 0 {
		opts.RetryLimit = 0
	}
	if opts.RangeChunkSize == 0 {
		opts.RangeChunkSize = 50_000
	}
	if opts.DefaultEndBlock == 0 {
		opts.DefaultEndBlock = 50_000_000
	}
	ctx, cancel := context.WithCancel(context.Background())
	svc := &Service{
		store:      store,
		cp:         NewCheckpointStore(store.Root()),
		opts:       opts,
		writer:     writer,
		adapters:   map[string]ProviderAdapter{},
		scheduler:  NewSmartScheduler(),
		events:     NewEventBus(300 * time.Millisecond),
		eta:        map[string]*etaState{},
		etaEngines: map[string]*pg.ETAEngine{},
		ctx:        ctx,
		cancel:     cancel,
		workers:    map[string]bool{},
		cpCache:    map[string]*CheckpointV3{},
	}
	svc.validator = NewValidator(svc)
	svc.results = NewResultProcessor(svc)
	svc.cloudPlanner = cloudplanner.NewPlanner(cloudplanner.BudgetGuard{})
	svc.history = feedback.NewHistory(store.Root())
	svc.coverageIndex = reg.NewStore(store.Root(), "1")
	svc.coverageIndex.OnUpdate = func(chainKey, address, dataset string) {
		svc.events.Publish(Event{Type: EventCoverageUpdated, DatasetJobID: dataset,
			Message: "coverage updated", Payload: map[string]any{
				"chain_key": chainKey, "address": address,
			}})
	}
	svc.scheduler.SetHistoryBonus(func(chainID int64, dataset, provider, bucket string) float64 {
		return svc.history.ScoreBonus(chainID, dataset, provider, bucket)
	})
	return svc
}

// SetCloudBudget 配置 Cloud 预算守卫（弹性调度 V1.0 §22）。
func (s *Service) SetCloudBudget(b cloudplanner.BudgetGuard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cloudPlanner = cloudplanner.NewPlanner(b)
}

// SetOnDatasetIndexed 注册结果入库回调（API 层接图谱/调查联动）。
func (s *Service) SetOnDatasetIndexed(fn func(*IndexedResult)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onIndexed = fn
}

// Results 返回结果处理器（API 查询用）。
func (s *Service) Results() *ResultProcessor { return s.results }

// SetDuckDB 注入 DuckDB 引擎（Parquet Part 读写/校验用；测试可注入）。
func (s *Service) SetDuckDB(engine *duckdb.Engine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.duckdbEngine = engine
}

func (s *Service) duckdb() *duckdb.Engine {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.duckdbEngine
}

// AdapterByName 按名称取 Adapter（校验交叉验证用）。
func (s *Service) AdapterByName(name string) (ProviderAdapter, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.adapters[name]
	return a, ok
}

// Options 返回服务选项。
func (s *Service) Options() Options { return s.opts }

// RegisterAdapter 注册 Provider Adapter（Phase 2 将注册 SQD/AWS/Browser/Cloud）。
func (s *Service) RegisterAdapter(a ProviderAdapter) {
	if a == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adapters[a.Name()] = a
	s.scheduler.Register(a)
}

// AdapterFor 返回支持该 Dataset 的第一个可用 Adapter。
func (s *Service) AdapterFor(dataset string) (ProviderAdapter, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.adapterForLocked(dataset)
}

func (s *Service) adapterForLocked(dataset string) (ProviderAdapter, bool) {
	var names []string
	for name := range s.adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		a := s.adapters[name]
		if a.Available() && a.Supports(dataset) {
			return a, true
		}
	}
	return nil, false
}

// AdapterNames 返回已注册 Adapter 名（API 展示）。
func (s *Service) AdapterNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.adapters))
	for name := range s.adapters {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Shutdown 取消全部 Worker（测试“kill”用；生产服务退出时调用）。
func (s *Service) Shutdown() {
	s.cancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
}

// ── 创建 ──

// CreateBatch 创建批量任务（Batch → Address → Dataset → Range 全树落盘）。
func (s *Service) CreateBatch(ctx context.Context, req CreateBatchRequest) (*CreateBatchResponse, error) {
	network, err := chain.Resolve(req.ChainKey)
	if err != nil {
		return nil, err
	}
	if len(req.Datasets) == 0 {
		return nil, fmt.Errorf("至少选择一个数据集")
	}
	datasets := make([]string, 0, len(req.Datasets))
	for _, d := range req.Datasets {
		if ValidDataset(d) {
			datasets = append(datasets, d)
		}
	}
	if len(datasets) == 0 {
		return nil, fmt.Errorf("没有合法的数据集")
	}
	spec := RangeSpec{Mode: RangeModeFull}
	if req.DefaultRange != nil && req.DefaultRange.Mode != "" {
		spec = *req.DefaultRange
	}
	if spec.Mode == "" {
		spec.Mode = RangeModeFull
	}

	seen := map[string]bool{}
	var valid, invalid []string
	duplicates := 0
	for _, a := range req.Addresses {
		a = strings.ToLower(strings.TrimSpace(a))
		if !evmAddressRE.MatchString(a) {
			invalid = append(invalid, a)
			continue
		}
		if seen[a] {
			duplicates++
			continue
		}
		seen[a] = true
		valid = append(valid, a)
	}
	if len(valid) == 0 {
		return nil, fmt.Errorf("没有有效地址（无效 %d）", len(invalid))
	}

	batchID := uuid.NewString()
	now := time.Now().UTC()
	batch := &BatchJob{
		ID:           batchID,
		ChainKey:     network.Key,
		ChainID:      network.ID,
		Status:       BatchCreated,
		AddressCount: len(valid),
		DatasetTypes: datasets,
		Prefetch:     req.Prefetch,
		PrefetchPriority: req.PrefetchPriority,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.SaveBatch(batch); err != nil {
		return nil, err
	}
	datasetJobs, rangeJobs := 0, 0
	localFullHits, localPartialHits, localMisses, reusedRanges := 0, 0, 0, 0
	packMode := len(valid)*len(datasets) > packThreshold
	var pack *BatchPack
	if packMode {
		pack = &BatchPack{Batch: batch}
	}
	skipCovered := req.SkipCovered != nil && *req.SkipCovered
	chains := map[string]bool{network.Key: true}
	for _, addr := range valid {
		addrSpec := spec
		if ov, ok := req.AddressOverrides[addr]; ok && ov.Mode != "" {
			addrSpec = ov
		}
		addrNetwork := network
		if ov, ok := req.AddressChainOverrides[addr]; ok && strings.TrimSpace(ov) != "" {
			resolved, err := chain.Resolve(ov)
			if err != nil {
				return nil, fmt.Errorf("地址 %s 指定了未知链 %q", addr, strings.TrimSpace(ov))
			}
			addrNetwork = resolved
		}
		chains[addrNetwork.Key] = true
		addrID := uuid.NewString()
		addrJob := &AddressJob{
			ID:        addrID,
			BatchID:   batchID,
			Address:   addr,
			ChainKey:  addrNetwork.Key,
			ChainID:   addrNetwork.ID,
			Range:     addrSpec,
			Status:    AddressWaiting,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if packMode {
			pack.Addresses = append(pack.Addresses, addrJob)
		} else {
			if err := s.store.SaveAddress(addrJob); err != nil {
				return nil, err
			}
		}
		for _, ds := range datasets {
			dsID := uuid.NewString()
			requested := s.requestedBlocks(addrSpec, ds)
			dsJob := &DatasetJob{
				ID:             dsID,
				BatchID:        batchID,
				AddressJobID:   addrID,
				Address:        addr,
				ChainKey:       addrNetwork.Key,
				Dataset:        ds,
				Status:         DatasetPending,
				RequestedRange: addrSpec,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			datasetJobs++
			// Phase 5：Range 级 LOCAL HIT 差量复用（全覆盖跳过；部分覆盖只补缺失区间）
			if skipCovered && !packMode && ds != DatasetBalances {
				covered := s.coveredRangesFor(ctx, addrNetwork.Key, addr, ds, requested.From, requested.To)
				reused, missing := planReuse(requested, covered)
				if len(reused) == 0 {
					// 无覆盖：正常下载
					localMisses++
				} else if len(missing) == 0 {
					if err := s.store.SaveDataset(dsJob); err != nil {
						return nil, err
					}
					if err := s.markDatasetReused(dsID, requested, reused, 0); err != nil {
						return nil, err
					}
					localFullHits++
					reusedRanges += len(reused)
					rangeJobs += len(reused)
					continue
				} else {
					if err := s.store.SaveDataset(dsJob); err != nil {
						return nil, err
					}
					if err := s.createReuseDataset(dsID, dsJob, addrID, batchID, addr, ds, requested, reused, missing, now); err != nil {
						return nil, err
					}
					localPartialHits++
					reusedRanges += len(reused)
					rangeJobs += len(missing)
					continue
				}
			}
			pendingRanges := SplitBlockRange(requested.From, requested.To, s.opts.RangeChunkSize)
			if s.opts.AdaptiveRanges && !packMode {
				if dr, derr := s.discoverDataset(ctx, dsJob, requested.From, requested.To); derr == nil &&
					len(dr.Segments) > 0 && dr.Confidence > 0 {
					spans := discovery.PlanSegments(requested.From, requested.To, dr.Segments, 50_000)
					pendingRanges = pendingRanges[:0]
					for _, sp := range spans {
						pendingRanges = append(pendingRanges, BlockRange{From: sp.From, To: sp.To})
					}
					dsJob.EstimatedRows = dr.EstimatedRows
					dsJob.EstimatedBytes = dr.EstimatedBytes
					dsJob.DiscoveryConfidence = dr.Confidence
					dsJob.SuggestedRangeSpan = dr.SuggestedRangeSpan
					dsJob.ActivitySegments = dr.Segments
				} else if derr != nil {
					logger.Log.Warn().Str("dataset_job", dsID).Str("address", addr).
						Err(derr).Msg("smartdownload_adaptive_discovery_failed")
				} else {
					logger.Log.Warn().Str("dataset_job", dsID).Str("address", addr).
						Float64("confidence", dr.Confidence).Int("segments", len(dr.Segments)).
						Msg("smartdownload_adaptive_discovery_low_confidence")
				}
			}
			cp := &CheckpointV3{}
			cp.Init(dsID, addr, ds, requested, s.opts.RangeChunkSize)
			cp.PendingRanges = pendingRanges
			if packMode {
				for _, r := range cp.PendingRanges {
					pack.Ranges = append(pack.Ranges, &RangeJob{
						ID: uuid.NewString(), DatasetJobID: dsID, BatchID: batchID,
						AddressJobID: addrID, Address: addr, Dataset: ds,
						FromBlock: r.From, ToBlock: r.To, Status: RangePending,
						CreatedAt: now, UpdatedAt: now,
					})
				}
				pack.Datasets = append(pack.Datasets, dsJob)
				rangeJobs += len(cp.PendingRanges)
				continue
			}
			if err := s.store.SaveDataset(dsJob); err != nil {
				return nil, err
			}
			if err := s.cp.Save(cp); err != nil {
				return nil, err
			}
			ledger := NewLedger(s.store.Root(), dsID)
			for _, r := range cp.PendingRanges {
				rangeID := uuid.NewString()
				rj := &RangeJob{
					ID:           rangeID,
					DatasetJobID: dsID,
					BatchID:      batchID,
					AddressJobID: addrID,
					Address:      addr,
					Dataset:      ds,
					FromBlock:    r.From,
					ToBlock:      r.To,
					Status:       RangePending,
					CreatedAt:    now,
					UpdatedAt:    now,
				}
				if err := s.store.SaveRange(rj); err != nil {
					return nil, err
				}
				rangeJobs++
				_ = ledger.Append(LedgerEntry{
					Event:        LedgerRangeCreated,
					DatasetJobID: dsID,
					RangeID:      r.Key(),
					FromBlock:    r.From,
					ToBlock:      r.To,
				})
			}
		}
	}
	if packMode {
		if err := s.store.SaveBatchPack(pack); err != nil {
			return nil, err
		}
	}
	if len(chains) > 1 {
		batch.ChainKey = "multi"
		batch.ChainID = 0
		batch.UpdatedAt = time.Now().UTC()
		if err := s.store.SaveBatch(batch); err != nil {
			return nil, err
		}
	}
	logger.Log.Info().Str("batch_id", batchID).Str("chain", network.Key).
		Int("addresses", len(valid)).Int("dataset_jobs", datasetJobs).Int("range_jobs", rangeJobs).
		Bool("pack_mode", packMode).Msg("smartdownload_batch_created")
	return &CreateBatchResponse{
		Batch:            batch,
		Valid:            len(valid),
		Invalid:          invalid,
		Duplicates:       duplicates,
		DatasetJobs:      datasetJobs,
		RangeJobs:        rangeJobs,
		LocalFullHits:    localFullHits,
		LocalPartialHits: localPartialHits,
		LocalMisses:      localMisses,
		ReusedRanges:     reusedRanges,
	}, nil
}

func (s *Service) requestedBlocks(spec RangeSpec, dataset string) BlockRange {
	if dataset == DatasetBalances {
		return BlockRange{From: 0, To: 0}
	}
	switch spec.Mode {
	case RangeModeBlock:
		if spec.ToBlock > spec.FromBlock {
			return BlockRange{From: spec.FromBlock, To: spec.ToBlock}
		}
	case RangeModeTime:
		if spec.ToBlock > spec.FromBlock {
			return BlockRange{From: spec.FromBlock, To: spec.ToBlock}
		}
	}
	return BlockRange{From: 0, To: s.opts.DefaultEndBlock}
}

// ── 生命周期控制 ──

// Start 启动批次 Worker（幂等；PAUSED 恢复时也调用）。
func (s *Service) Start(batchID string) error {
	batch := s.store.GetBatch(batchID)
	if batch == nil {
		return fmt.Errorf("批次不存在: %s", batchID)
	}
	if batch.Status.Terminal() {
		return fmt.Errorf("批次已处于终态 %s", batch.Status)
	}
	// 先探测、后下载（Phase 2）：探测失败不阻断，沿用默认候选
	s.planBatchIfNeeded(batchID)
	s.mu.Lock()
	if s.workers[batchID] {
		s.mu.Unlock()
		return nil
	}
	s.workers[batchID] = true
	s.mu.Unlock()
	s.wg.Add(1)
	go s.runBatch(batchID)
	return nil
}

// PlanBatch 对批次内全部 Address × Dataset 执行 Discovery/Probe 并生成执行计划。
func (s *Service) PlanBatch(ctx context.Context, batchID string) (*ExecutionPlan, error) {
	batch := s.store.GetBatch(batchID)
	if batch == nil {
		return nil, fmt.Errorf("批次不存在: %s", batchID)
	}
	plan := &ExecutionPlan{BatchID: batchID}
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			if ds.Status.Terminal() {
				continue
			}
			ranges := s.store.ListRangesByDataset(ds.ID)
			if len(ranges) == 0 {
				continue
			}
			req := ProbeRequest{
				Address:   a.Address,
				Dataset:   ds.Dataset,
				ChainKey:  a.ChainKey,
				ChainID:   a.ChainID,
				FromBlock: ranges[0].FromBlock,
				ToBlock:   ranges[len(ranges)-1].ToBlock,
			}
			dp, err := s.scheduler.PlanDataset(ctx, req)
			if err != nil {
				logger.Log.Warn().Str("dataset_job", ds.ID).Err(err).Msg("smartdownload_plan_dataset_failed")
				continue
			}
			// 已有估算（人工/历史）优先，探测只做向上细化，避免小采样覆盖大估算
			if ds.EstimatedRows == 0 || dp.EstimatedRows > ds.EstimatedRows {
				ds.EstimatedRows = dp.EstimatedRows
			}
			if ds.EstimatedBytes == 0 || dp.EstimatedBytes > ds.EstimatedBytes {
				ds.EstimatedBytes = dp.EstimatedBytes
			}
			if dp.PreferredProvider != "" {
				ds.PreferredProvider = dp.PreferredProvider
			}
			if dr, derr := s.discoverDataset(ctx, ds, req.FromBlock, req.ToBlock); derr == nil && dr.Confidence > 0 {
				if ds.EstimatedRows == 0 || dr.EstimatedRows > ds.EstimatedRows {
					ds.EstimatedRows = dr.EstimatedRows
				}
				if ds.EstimatedBytes == 0 || dr.EstimatedBytes > ds.EstimatedBytes {
					ds.EstimatedBytes = dr.EstimatedBytes
				}
				ds.DiscoveryConfidence = dr.Confidence
				ds.SuggestedRangeSpan = dr.SuggestedRangeSpan
				ds.ActivitySegments = dr.Segments
			}
			ds.UpdatedAt = time.Now().UTC()
			_ = s.store.SaveDataset(ds)
			plan.Datasets = append(plan.Datasets, dp)
		}
	}
	return plan, nil
}

func (s *Service) planBatchIfNeeded(batchID string) {
	planCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := s.PlanBatch(planCtx, batchID); err != nil {
		logger.Log.Warn().Str("batch_id", batchID).Err(err).Msg("smartdownload_plan_skipped")
	}
}

// discoverDataset 运行 Discovery（L0 Metadata → L1/L2 自适应采样 → 分段 → 缓存）。
func (s *Service) discoverDataset(ctx context.Context, ds *DatasetJob, from, to uint64) (*discovery.DiscoveryResult, error) {
	if ds == nil {
		return nil, fmt.Errorf("dataset 为空")
	}
	engine := discovery.NewEngine(s.store.Root(), discoveryMetadata{svc: s},
		func(ctx context.Context, wFrom, wTo uint64) (uint64, error) {
			return s.sampleWindow(ctx, ds, wFrom, wTo)
		})
	return engine.Discover(ctx, discovery.Input{
		ChainID:     s.chainIDOfDataset(ds),
		ChainKey:    ds.ChainKey,
		Address:     ds.Address,
		Dataset:     ds.Dataset,
		FromBlock:   from,
		ToBlock:     to,
		BytesPerRow: 160,
		Activity:    int64(ds.EstimatedRows),
	})
}

// sampleWindow 用当前最佳 Adapter 对窗口计数（ProbeWith 窗口估算 ≈ 窗口行数）。
func (s *Service) sampleWindow(ctx context.Context, ds *DatasetJob, from, to uint64) (uint64, error) {
	cands := s.scheduler.Candidates(ds.Dataset)
	s.mu.Lock()
	var adapters []ProviderAdapter
	for _, c := range cands {
		if c.ManualOnly || !c.Available {
			continue
		}
		if a, ok := s.adapters[c.Name]; ok {
			adapters = append(adapters, a)
		}
	}
	s.mu.Unlock()
	var lastErr error
	for _, a := range adapters {
		res, err := ProbeWith(ctx, a, ProbeRequest{
			Address: ds.Address, Dataset: ds.Dataset, ChainKey: ds.ChainKey,
			ChainID: s.chainIDOfDataset(ds), FromBlock: from, ToBlock: to,
		})
		if err == nil && res.Confidence > 0 && res.EstimatedRows > 0 {
			return res.EstimatedRows, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return 0, lastErr
	}
	return 0, fmt.Errorf("采样窗口无有效结果（%d 个候选 Provider）", len(adapters))
}

func (s *Service) chainIDOfDataset(ds *DatasetJob) int64 {
	if ds == nil {
		return 0
	}
	if a := s.store.GetAddress(ds.AddressJobID); a != nil {
		return a.ChainID
	}
	return 0
}

// discoveryMetadata L0：本地 Registry 全覆盖时直接返回 total（confidence 0.95）。
type discoveryMetadata struct{ svc *Service }

func (m discoveryMetadata) TotalRows(ctx context.Context, chainKey, address, dataset string, from, to uint64) (uint64, bool, error) {
	svc := m.svc
	covered := svc.coveredRangesFor(ctx, chainKey, address, dataset, from, to)
	_, missing := planReuse(BlockRange{From: from, To: to}, covered)
	if len(missing) > 0 {
		return 0, false, nil
	}
	var total uint64
	for _, e := range svc.results.List() {
		if e.ChainKey != chainKey || e.Dataset != dataset || e.Validation != "VALIDATED" {
			continue
		}
		if !strings.EqualFold(e.Address, address) {
			continue
		}
		if e.ToBlock < from || e.FromBlock > to {
			continue
		}
		total += uint64(e.RowCount)
	}
	if total > 0 {
		return total, true, nil
	}
	return 0, false, nil
}

// recordHistory 记录 Provider 历史画像（传输层 + 最终验证）。
func (s *Service) recordHistory(ds *DatasetJob, provider string, rows int64, runtime, latency time.Duration,
	success, final bool, httpClass string) {
	if ds == nil || provider == "" || provider == "local_hit" {
		return
	}
	s.history.Record(feedback.Record{
		ChainID:      s.chainIDOfDataset(ds),
		Dataset:      ds.Dataset,
		Provider:     provider,
		ScaleBucket:  feedback.ScaleBucket(ds.EstimatedRows),
		Rows:         rows,
		Runtime:      runtime,
		Latency:      latency,
		Success:      success,
		FinalSuccess: final,
		HTTPClass:    httpClass,
	})
}

// emitFeedbackAction 记录/推送反馈动作（Execution Feedback Loop Phase D）。
func (s *Service) emitFeedbackAction(ds *DatasetJob, d feedback.Decision) {
	if ds == nil || d.Action == feedback.Keep {
		return
	}
	_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
		Event: LedgerFeedbackAction, DatasetJobID: ds.ID,
		Provider: ds.CurrentProvider, Error: fmt.Sprintf("action=%s reason=%s", d.Action, d.Reason),
	})
	s.events.Publish(Event{
		Type: EventFeedbackAction, DatasetJobID: ds.ID, Provider: ds.CurrentProvider,
		Status: string(d.Action), Message: d.Reason,
	})
}

func (s *Service) PauseBatch(batchID string) (*BatchJob, error) {
	return s.flagBatch(batchID, true, false)
}

func (s *Service) ResumeBatch(batchID string) (*BatchJob, error) {
	batch := s.store.GetBatch(batchID)
	if batch == nil {
		return nil, fmt.Errorf("批次不存在: %s", batchID)
	}
	if batch.Status == BatchCanceled || batch.Status.Terminal() {
		return nil, fmt.Errorf("批次已处于终态 %s", batch.Status)
	}
	s.mu.Lock()
	batch = s.store.GetBatch(batchID)
	batch.PauseRequested = false
	batch.Status = BatchRunning
	batch.UpdatedAt = time.Now().UTC()
	if batch.StartedAt == nil {
		now := time.Now().UTC()
		batch.StartedAt = &now
	}
	_ = s.store.SaveBatch(batch)
	addresses := s.store.ListAddressesByBatch(batchID)
	for _, a := range addresses {
		if a.Status == AddressPaused {
			a.PauseRequested = false
			a.Status = AddressDownloading
			a.UpdatedAt = time.Now().UTC()
			_ = s.store.SaveAddress(a)
			for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
				if ds.Status == DatasetPaused {
					ds.PauseRequested = false
					ds.Status = DatasetRunning
					ds.UpdatedAt = time.Now().UTC()
					_ = s.store.SaveDataset(ds)
				}
				_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
					Event: LedgerResumed, DatasetJobID: ds.ID, Provider: ds.CurrentProvider,
				})
			}
		}
	}
	s.mu.Unlock()
	_ = s.Start(batchID)
	return s.store.GetBatch(batchID), nil
}

func (s *Service) CancelBatch(batchID string) (*BatchJob, error) {
	return s.flagBatch(batchID, false, true)
}

func (s *Service) PauseAddress(addressID string) (*AddressJob, error) {
	a := s.store.GetAddress(addressID)
	if a == nil {
		return nil, fmt.Errorf("地址任务不存在: %s", addressID)
	}
	if a.Status.Terminal() {
		return nil, fmt.Errorf("地址任务已处于终态 %s", a.Status)
	}
	s.mu.Lock()
	a = s.store.GetAddress(addressID)
	a.PauseRequested = true
	a.UpdatedAt = time.Now().UTC()
	_ = s.store.SaveAddress(a)
	for _, ds := range s.store.ListDatasetsByAddress(addressID) {
		if !ds.Status.Terminal() {
			ds.PauseRequested = true
			ds.UpdatedAt = time.Now().UTC()
			_ = s.store.SaveDataset(ds)
			_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
				Event: LedgerPaused, DatasetJobID: ds.ID, Provider: ds.CurrentProvider,
			})
		}
	}
	s.mu.Unlock()
	return s.store.GetAddress(addressID), nil
}

func (s *Service) ResumeAddress(addressID string) (*AddressJob, error) {
	a := s.store.GetAddress(addressID)
	if a == nil {
		return nil, fmt.Errorf("地址任务不存在: %s", addressID)
	}
	if a.Status.Terminal() {
		return nil, fmt.Errorf("地址任务已处于终态 %s", a.Status)
	}
	s.mu.Lock()
	a = s.store.GetAddress(addressID)
	a.PauseRequested = false
	a.Status = AddressDownloading
	a.UpdatedAt = time.Now().UTC()
	if a.StartedAt == nil {
		now := time.Now().UTC()
		a.StartedAt = &now
	}
	_ = s.store.SaveAddress(a)
	for _, ds := range s.store.ListDatasetsByAddress(addressID) {
		if ds.Status == DatasetPaused {
			ds.PauseRequested = false
			ds.Status = DatasetRunning
			ds.UpdatedAt = time.Now().UTC()
			_ = s.store.SaveDataset(ds)
			_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
				Event: LedgerResumed, DatasetJobID: ds.ID, Provider: ds.CurrentProvider,
			})
		}
	}
	// 确保批次处于 RUNNING 且 Worker 存活
	batch := s.store.GetBatch(a.BatchID)
	if batch != nil && !batch.Status.Terminal() {
		batch.PauseRequested = false
		batch.Status = BatchRunning
		batch.UpdatedAt = time.Now().UTC()
		_ = s.store.SaveBatch(batch)
	}
	s.mu.Unlock()
	_ = s.Start(a.BatchID)
	return s.store.GetAddress(addressID), nil
}

func (s *Service) CancelAddress(addressID string) (*AddressJob, error) {
	a := s.store.GetAddress(addressID)
	if a == nil {
		return nil, fmt.Errorf("地址任务不存在: %s", addressID)
	}
	if a.Status.Terminal() {
		return nil, fmt.Errorf("地址任务已处于终态 %s", a.Status)
	}
	s.mu.Lock()
	a = s.store.GetAddress(addressID)
	a.CancelRequested = true
	a.UpdatedAt = time.Now().UTC()
	_ = s.store.SaveAddress(a)
	for _, ds := range s.store.ListDatasetsByAddress(addressID) {
		if !ds.Status.Terminal() {
			ds.CancelRequested = true
			ds.UpdatedAt = time.Now().UTC()
			_ = s.store.SaveDataset(ds)
			_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
				Event: LedgerCanceled, DatasetJobID: ds.ID, Provider: ds.CurrentProvider,
			})
		}
	}
	s.mu.Unlock()
	return s.store.GetAddress(addressID), nil
}

func (s *Service) PauseDataset(datasetJobID string) (*DatasetJob, error) {
	return s.flagDataset(datasetJobID, true, false)
}

func (s *Service) CancelDataset(datasetJobID string) (*DatasetJob, error) {
	return s.flagDataset(datasetJobID, false, true)
}

func (s *Service) flagDataset(datasetJobID string, pause, cancel bool) (*DatasetJob, error) {
	ds := s.store.GetDataset(datasetJobID)
	if ds == nil {
		return nil, fmt.Errorf("数据集任务不存在: %s", datasetJobID)
	}
	if ds.Status.Terminal() {
		return nil, fmt.Errorf("数据集任务已处于终态 %s", ds.Status)
	}
	s.mu.Lock()
	ds = s.store.GetDataset(datasetJobID)
	ds.PauseRequested = pause
	ds.CancelRequested = cancel
	ds.UpdatedAt = time.Now().UTC()
	_ = s.store.SaveDataset(ds)
	event := LedgerPaused
	if cancel {
		event = LedgerCanceled
	}
	_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
		Event: event, DatasetJobID: ds.ID, Provider: ds.CurrentProvider,
	})
	s.mu.Unlock()
	return s.store.GetDataset(datasetJobID), nil
}

func (s *Service) flagBatch(batchID string, pause, cancel bool) (*BatchJob, error) {
	batch := s.store.GetBatch(batchID)
	if batch == nil {
		return nil, fmt.Errorf("批次不存在: %s", batchID)
	}
	if batch.Status.Terminal() {
		return nil, fmt.Errorf("批次已处于终态 %s", batch.Status)
	}
	s.mu.Lock()
	batch = s.store.GetBatch(batchID)
	batch.PauseRequested = pause
	batch.CancelRequested = cancel
	batch.UpdatedAt = time.Now().UTC()
	_ = s.store.SaveBatch(batch)
	event := LedgerPaused
	if cancel {
		event = LedgerCanceled
	}
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		if a.Status.Terminal() {
			continue
		}
		a.PauseRequested = pause
		a.CancelRequested = cancel
		a.UpdatedAt = time.Now().UTC()
		_ = s.store.SaveAddress(a)
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			if !ds.Status.Terminal() {
				ds.PauseRequested = pause
				ds.CancelRequested = cancel
				ds.UpdatedAt = time.Now().UTC()
				_ = s.store.SaveDataset(ds)
				_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
					Event: event, DatasetJobID: ds.ID, Provider: ds.CurrentProvider,
				})
			}
		}
	}
	s.mu.Unlock()
	return s.store.GetBatch(batchID), nil
}

// ── Worker 循环 ──

type claimedRange struct {
	rangeID      string
	datasetJobID string
	addressJobID string
	batchID      string
	provider     string
	adapter      ProviderAdapter
	req          RangeRequest
}

func (s *Service) runBatch(batchID string) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.workers, batchID)
		s.mu.Unlock()
	}()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		claim := s.claimNext(batchID)
		if claim == nil {
			done := s.trySettle(batchID)
			if done {
				return
			}
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(300 * time.Millisecond):
			}
			continue
		}
		s.executeRange(claim)
	}
}

// claimNext 领取下一个可执行 Range（原子：同一时刻只被一个 Worker 领取）。
func (s *Service) claimNext(batchID string) *claimedRange {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := s.store.GetBatch(batchID)
	if batch == nil || batch.Status.Terminal() {
		return nil
	}
	if batch.CancelRequested {
		return nil // 等当前 Range 结束后 settle 统一取消
	}
	if batch.PauseRequested && !s.batchHasRunningRange(batchID) {
		s.transitionBatchPausedLocked(batchID)
		return nil
	}
	if batch.Status == BatchCreated {
		batch.Status = BatchRunning
		batch.UpdatedAt = time.Now().UTC()
		if batch.StartedAt == nil {
			now := time.Now().UTC()
			batch.StartedAt = &now
		}
		_ = s.store.SaveBatch(batch)
	}
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		if a.Status.Terminal() {
			continue
		}
		if a.Status == AddressPaused {
			continue
		}
		if a.CancelRequested {
			s.cancelAddressLocked(a.ID)
			continue
		}
		if a.PauseRequested && !s.addressHasRunningRange(a.ID) {
			s.transitionAddressPausedLocked(a.ID)
			continue
		}
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			if ds.Status.Terminal() {
				continue
			}
			if ds.Status == DatasetValidating {
				continue
			}
			if ds.Status == DatasetPaused {
				continue
			}
			if ds.CancelRequested {
				s.cancelDatasetLocked(ds.ID)
				continue
			}
			if ds.PauseRequested && !s.datasetHasRunningRange(ds.ID) {
				if !ds.Status.Terminal() {
					ds.Status = DatasetPaused
					ds.UpdatedAt = time.Now().UTC()
					_ = s.store.SaveDataset(ds)
				}
				continue
			}
			// Phase 1：同一 Dataset 的 Range 串行执行（避免并发写同一 Checkpoint/Part）。
			if s.datasetHasRunningRange(ds.ID) {
				continue
			}
			for _, r := range s.store.ListRangesByDataset(ds.ID) {
				if r.Status == RangePending || r.Status == RangeReady {
					adapter, provider, ok := s.selectProviderLocked(ds, r)
					if !ok {
						adapter, provider = nil, ""
					}
					previous := r.Provider
					r.Status = RangeRunning
					r.Provider = provider
					r.UpdatedAt = time.Now().UTC()
					if r.StartedAt == nil {
						now := time.Now().UTC()
						r.StartedAt = &now
					}
					_ = s.store.SaveRange(r)
					if previous != "" && previous != provider {
						_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
							Event:        LedgerProviderSwitched,
							DatasetJobID: ds.ID,
							RangeID:      BlockRange{From: r.FromBlock, To: r.ToBlock}.Key(),
							FromBlock:    r.FromBlock,
							ToBlock:      r.ToBlock,
							Provider:     provider,
							Error:        "previous=" + previous + " (失败切换)",
						})
						s.events.Publish(Event{
							Type: EventProviderSwitched, BatchID: batchID, AddressJobID: a.ID,
							DatasetJobID: ds.ID, RangeID: BlockRange{From: r.FromBlock, To: r.ToBlock}.Key(),
							Provider: provider, Message: "previous=" + previous,
						})
					}
					ds.Status = DatasetRunning
					ds.CurrentProvider = provider
					if previous != "" && previous != provider {
						s.resetETA(ds)
					}
					ds.UpdatedAt = time.Now().UTC()
					_ = s.store.SaveDataset(ds)
					s.ensureETAStarted(ds)
					a.Status = AddressDownloading
					a.UpdatedAt = time.Now().UTC()
					_ = s.store.SaveAddress(a)
					_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
						Event:        LedgerRangeStarted,
						DatasetJobID: ds.ID,
						RangeID:      BlockRange{From: r.FromBlock, To: r.ToBlock}.Key(),
						FromBlock:    r.FromBlock,
						ToBlock:      r.ToBlock,
						Provider:     provider,
					})
					req := RangeRequest{
						DatasetJobID: ds.ID,
						Address:      a.Address,
						Dataset:      ds.Dataset,
						ChainKey:     a.ChainKey,
						ChainID:      a.ChainID,
						FromBlock:    r.FromBlock,
						ToBlock:      r.ToBlock,
					}
					if provider == "sqd_cloud" {
						s.ensureCloudPlanLocked(ds)
						req.CloudTier = ds.CloudTier
					}
					return &claimedRange{
						rangeID:      r.ID,
						datasetJobID: ds.ID,
						addressJobID: a.ID,
						batchID:      batchID,
						provider:     provider,
						adapter:      adapter,
						req:          req,
					}
				}
			}
		}
	}
	return nil
}

func (s *Service) executeRange(claim *claimedRange) {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Minute)
	defer cancel()
	adapter := claim.adapter
	if adapter == nil || claim.provider == "" {
		s.failRange(claim, fmt.Errorf("无可用 Provider（常规 Provider 均失败或未装配；Cloud 兜底不可用）"))
		return
	}
	result, err := adapter.ExecuteRange(ctx, claim.req)
	if err != nil {
		s.failRange(claim, err)
		return
	}
	s.mu.Lock()
	ds := s.store.GetDataset(claim.datasetJobID)
	cp := s.checkpointLocked(claim.datasetJobID)
	if ds == nil || cp == nil {
		s.mu.Unlock()
		s.failRange(claim, fmt.Errorf("数据集/checkpoint 缺失"))
		return
	}
	blockRange := BlockRange{From: claim.req.FromBlock, To: claim.req.ToBlock}
	if len(result.Records) == 0 {
		cp.ConfirmEmpty(blockRange)
		_ = s.cp.Save(cp)
		_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
			Event: LedgerRangeEmpty, DatasetJobID: ds.ID,
			RangeID: blockRange.Key(), FromBlock: blockRange.From, ToBlock: blockRange.To,
			Provider: claim.provider,
		})
		rj := s.store.GetRange(claim.rangeID)
		rj.Status = RangeEmpty
		rj.Provider = claim.provider
		now := time.Now().UTC()
		rj.FinishedAt = &now
		rj.UpdatedAt = now
		_ = s.store.SaveRange(rj)
		s.updateProgressLocked(ds.ID)
		s.finalizeDatasetIfDoneLocked(ds.ID)
		s.mu.Unlock()
		s.scheduler.Health().RecordSuccess(claim.provider)
		s.events.Publish(Event{
			Type: EventRangeCompleted, BatchID: claim.batchID, AddressJobID: claim.addressJobID,
			DatasetJobID: claim.datasetJobID, RangeID: blockRange.Key(),
			Provider: claim.provider, Status: "EMPTY",
		})
		return
	}
	partName := cp.NextPartName(s.writer.Extension())
	s.mu.Unlock() // Part 写入是 I/O，不持锁
	written, werr := s.writer.WritePart(ctx, PartMeta{
		DatasetJobID: claim.datasetJobID,
		PartName:     partName,
		Provider:     claim.provider,
		FromBlock:    claim.req.FromBlock,
		ToBlock:      claim.req.ToBlock,
	}, result.Records)
	if werr != nil {
		s.failRange(claim, fmt.Errorf("Part 写入失败: %w", werr))
		return
	}
	s.mu.Lock()
	ds = s.store.GetDataset(claim.datasetJobID)
	cp = s.checkpointLocked(claim.datasetJobID)
	part := PartInfo{
		Name: partName, SHA256: written.SHA256, Rows: written.Rows,
		Bytes: written.Bytes, RangeFrom: blockRange.From, RangeTo: blockRange.To,
	}
	cp.CompleteRange(blockRange, &part)
	_ = s.cp.Save(cp)
	_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
		Event: LedgerPartCommitted, DatasetJobID: ds.ID,
		RangeID: blockRange.Key(), FromBlock: blockRange.From, ToBlock: blockRange.To,
		Provider: claim.provider, Part: partName, SHA256: part.SHA256, Rows: part.Rows,
	})
	_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
		Event: LedgerRangeCompleted, DatasetJobID: ds.ID,
		RangeID: blockRange.Key(), FromBlock: blockRange.From, ToBlock: blockRange.To,
		Provider: claim.provider, Part: partName, Rows: part.Rows,
	})
	rj := s.store.GetRange(claim.rangeID)
	rj.Status = RangeCompleted
	rj.Provider = claim.provider
	rj.FailedProviders = nil // 成功后清空失败记录（当前 Range 已闭环）
	rj.RowsCommitted = uint64(part.Rows)
	rj.Bytes = uint64(part.Bytes)
	now := time.Now().UTC()
	rj.FinishedAt = &now
	rj.UpdatedAt = now
	_ = s.store.SaveRange(rj)
	runtime := time.Duration(0)
	if rj.StartedAt != nil {
		runtime = time.Since(*rj.StartedAt)
	}
	s.recordHistory(ds, claim.provider, part.Rows, runtime, runtime, true, false, "")
	s.completeRepair(ds, rj, part.Rows)
	ds.DownloadedRows += uint64(part.Rows)
	s.updateProgressLocked(ds.ID)
	s.finalizeDatasetIfDoneLocked(ds.ID)
	s.mu.Unlock()
	s.scheduler.Health().RecordSuccess(claim.provider)
	s.events.Publish(Event{
		Type: EventRangeCompleted, BatchID: claim.batchID, AddressJobID: claim.addressJobID,
		DatasetJobID: claim.datasetJobID, RangeID: blockRange.Key(),
		Provider: claim.provider, Status: "COMPLETED",
		Payload: map[string]any{"rows": part.Rows, "part": partName},
	})
}

// failRange 记录 Range 失败并决定重试/终态。
func (s *Service) failRange(claim *claimedRange, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rj := s.store.GetRange(claim.rangeID)
	ds := s.store.GetDataset(claim.datasetJobID)
	runtime := time.Duration(0)
	if rj.StartedAt != nil {
		runtime = time.Since(*rj.StartedAt)
	}
	httpClass := feedback.ClassifyHTTPClass(err.Error())
	s.recordHistory(ds, claim.provider, 0, runtime, runtime, false, false, httpClass)
	s.emitFeedbackAction(ds, feedback.Reevaluate(feedback.ExecutionMetrics{
		Provider:         claim.provider,
		HTTP503Rate:      boolRate(httpClass == "503"),
		HTTP429Rate:      boolRate(httpClass == "429"),
		TimeoutCount:     boolInt(httpClass == "timeout"),
		CircuitOpen:      s.scheduler.Health().Exhausted(claim.provider),
		CompletedPercent: progressPercent(ds),
	}))
	rj.Attempts++
	rj.Error = err.Error()
	rj.UpdatedAt = time.Now().UTC()
	s.scheduler.Health().RecordFailure(claim.provider, err)
	if claim.provider != "" && !containsString(rj.FailedProviders, claim.provider) {
		rj.FailedProviders = append(rj.FailedProviders, claim.provider)
	}
	s.events.Publish(Event{
		Type: EventError, BatchID: claim.batchID, AddressJobID: claim.addressJobID,
		DatasetJobID: claim.datasetJobID, Provider: claim.provider,
		Message: sanitizeText(err.Error()),
	})
	ledger := NewLedger(s.store.Root(), claim.datasetJobID)
	blockRange := BlockRange{From: claim.req.FromBlock, To: claim.req.ToBlock}
	_ = ledger.Append(LedgerEntry{
		Event: LedgerProviderFailed, DatasetJobID: claim.datasetJobID,
		RangeID: blockRange.Key(), FromBlock: blockRange.From, ToBlock: blockRange.To,
		Provider: claim.provider, Error: err.Error(),
	})
	_ = ledger.Append(LedgerEntry{
		Event: LedgerRangeFailed, DatasetJobID: claim.datasetJobID,
		RangeID: blockRange.Key(), FromBlock: blockRange.From, ToBlock: blockRange.To,
		Provider: claim.provider, Error: err.Error(),
	})
	// 未超过总尝试预算 → 重新入队（下次领取自动切换或重试同 Provider）
	if rj.Attempts < s.opts.RetryLimit+1 {
		rj.Status = RangeReady
		_ = s.store.SaveRange(rj)
		return
	}
	rj.Status = RangeFailed
	now := time.Now().UTC()
	rj.FinishedAt = &now
	_ = s.store.SaveRange(rj)
	if ds != nil && !ds.Status.Terminal() {
		ds.Status = DatasetFailed
		ds.Error = err.Error()
		ds.UpdatedAt = now
		_ = s.store.SaveDataset(ds)
	}
	a := s.store.GetAddress(claim.addressJobID)
	if a != nil && !a.Status.Terminal() {
		a.Status = AddressFailed
		a.Error = err.Error()
		a.UpdatedAt = now
		_ = s.store.SaveAddress(a)
	}
}

func boolRate(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func progressPercent(ds *DatasetJob) float64 {
	if ds == nil {
		return 0
	}
	return ds.Progress.Percent
}

// selectProviderLocked 为 Range 选择 Provider（跳过已失败/冷却 Provider；Cloud 最后兜底）。
// 调用方必须持 service.mu。
func (s *Service) selectProviderLocked(ds *DatasetJob, rj *RangeJob) (ProviderAdapter, string, bool) {
	name, ok := s.scheduler.SelectProvider(ds.Dataset, rj.FailedProviders)
	if !ok {
		// 全部候选已失败：允许同 Provider 重试（瞬态抖动），由 Attempts 预算兜底
		for _, c := range s.scheduler.Candidates(ds.Dataset) {
			if !c.ManualOnly && c.Available {
				a := s.adapters[c.Name]
				if a != nil {
					return a, c.Name, true
				}
			}
		}
		return nil, "", false
	}
	a := s.adapters[name]
	if a == nil || !a.Available() {
		return nil, "", false
	}
	return a, name, true
}

// finalizeDatasetIfDoneLocked 数据集全部 Range 终态后推进 Dataset/Address 终态（调用方持 service.mu）。
func (s *Service) finalizeDatasetIfDoneLocked(datasetJobID string) {
	ds := s.store.GetDataset(datasetJobID)
	if ds == nil || ds.Status.Terminal() {
		return
	}
	ranges := s.store.ListRangesByDataset(datasetJobID)
	if len(ranges) == 0 {
		ds.Status = DatasetCompleted
		now := time.Now().UTC()
		ds.FinishedAt = &now
		ds.UpdatedAt = now
		_ = s.store.SaveDataset(ds)
		s.finalizeAddressIfDoneLocked(ds.AddressJobID)
		return
	}
	done := 0
	for _, r := range ranges {
		if r.Status.Terminal() {
			done++
		}
	}
	if done != len(ranges) {
		return
	}
	now := time.Now().UTC()
	switch {
	case ds.CancelRequested:
		ds.Status = DatasetCanceled
	case ds.PauseRequested:
		ds.Status = DatasetPaused
	default:
		// 下载完成 ≠ 任务完成：进入 VALIDATING（Phase 3 Validation Pipeline）
		ds.Status = DatasetValidating
		go s.validateDatasetAndFinalize(ds.ID)
		ds.UpdatedAt = now
		_ = s.store.SaveDataset(ds)
		return
	}
	ds.UpdatedAt = now
	_ = s.store.SaveDataset(ds)
	s.finalizeAddressIfDoneLocked(ds.AddressJobID)
}

// validateDatasetAndFinalize 校验完成后推进终态：VALIDATED→COMPLETED；缺口→自动补洞；否则 PARTIAL/FAILED。
func (s *Service) validateDatasetAndFinalize(dsID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report, err := s.validator.ValidateDataset(ctx, dsID)
	s.events.Publish(Event{
		Type: EventValidationUpdated, DatasetJobID: dsID,
		Status: reportStatus(report), Message: validationSummary(report, err),
	})
	s.mu.Lock()
	ds := s.store.GetDataset(dsID)
	if ds == nil || ds.Status != DatasetValidating {
		s.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	switch {
	case err != nil || report.Status == "FAILED":
		msg := errMsg(err)
		if report != nil {
			msg = strings.Join(report.Errors, "; ")
			ds.Validation = report
		}
		ds.Status = DatasetFailed
		ds.Error = msg
		ds.FinishedAt = &now
		ds.UpdatedAt = now
		_ = s.store.SaveDataset(ds)
		s.finalizeAddressIfDoneLocked(ds.AddressJobID)
		s.recordFinalHistory(ds, false)
		s.mu.Unlock()
		s.events.Publish(Event{Type: EventError, DatasetJobID: dsID, Status: "FAILED", Message: msg})
		return
	case report.Status == "PARTIAL":
		if s.repairDatasetGapsLocked(dsID, report) {
			s.mu.Unlock()
			return
		}
		ds.Status = DatasetPartial
		ds.Validation = report
		ds.FinishedAt = &now
		ds.UpdatedAt = now
		_ = s.store.SaveDataset(ds)
		s.finalizeAddressIfDoneLocked(ds.AddressJobID)
		s.recordFinalHistory(ds, false)
		if report != nil && report.Coverage < 1 {
			s.emitFeedbackAction(ds, feedback.Reevaluate(feedback.ExecutionMetrics{
				Provider: ds.CurrentProvider, SilentGap: true,
			}))
		}
		s.mu.Unlock()
		s.events.Publish(Event{Type: EventResultReady, DatasetJobID: dsID, Status: "PARTIAL", Message: "存在未覆盖区间"})
		return
	default:
		ds.Status = DatasetCompleted
		ds.Validation = report
		ds.FinishedAt = &now
		ds.UpdatedAt = now
		_ = s.store.SaveDataset(ds)
		s.finalizeAddressIfDoneLocked(ds.AddressJobID)
		s.recordFinalHistory(ds, true)
		s.mu.Unlock()
		s.events.Publish(Event{Type: EventResultReady, DatasetJobID: dsID, Status: "VALIDATED", Message: "完整性校验通过"})
		go s.indexDataset(dsID)
	}
}

// recordFinalHistory 记录最终结果（Download + Validation 成功率，设计 §43/§44）。
func (s *Service) recordFinalHistory(ds *DatasetJob, validated bool) {
	if ds == nil {
		return
	}
	runtime := time.Duration(0)
	if ds.StartedAt != nil {
		runtime = time.Since(*ds.StartedAt)
	}
	s.recordHistory(ds, ds.CurrentProvider, int64(ds.DownloadedRows), runtime, 0, true, validated, "")
}

// indexDataset 结果入库：合并 warehouse Parquet + Registry 登记 + 下游事件。
func (s *Service) indexDataset(dsID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	entry, err := s.results.MergeDataset(ctx, dsID)
	if err != nil {
		logger.Log.Warn().Str("dataset_job", dsID).Err(err).Msg("smartdownload_index_failed")
		return
	}
	// Coverage Index V2：只有 CERTIFIED 才写认证覆盖；余额类写快照（TTL 300s）
	if s.coverageIndex != nil && entry.Certification == "CERTIFIED" {
		var snapshot *reg.SnapshotCoverage
		if entry.Dataset == DatasetBalances {
			snapshot = &reg.SnapshotCoverage{
				Block: entry.ToBlock, Time: time.Now().UTC(), TTLSeconds: 300,
			}
		}
		_ = s.coverageIndex.AddCertified(entry.ChainKey, entry.ChainID, entry.Address,
			entry.Dataset, []reg.Interval{{From: entry.FromBlock, To: entry.ToBlock}},
			entry.RowCount, snapshot)
	}
	s.mu.Lock()
	fn := s.onIndexed
	s.mu.Unlock()
	if fn != nil {
		fn(entry)
	}
}

// repairDatasetGapsLocked 创建缺失 Range 的补洞任务（调用方持 service.mu；上限 2 轮）。
func (s *Service) repairDatasetGapsLocked(dsID string, report *ValidationReport) bool {
	ds := s.store.GetDataset(dsID)
	if ds == nil || ds.RepairRounds >= 2 || len(report.MissingRanges) == 0 {
		return false
	}
	gapStore := v3.NewGapStore(s.store.Root(), dsID)
	ledgerEntries, _ := NewLedger(s.store.Root(), dsID).Replay()
	used := map[string]bool{}
	providerOfRange := map[string]string{}
	for _, e := range ledgerEntries {
		if e.Provider != "" {
			used[e.Provider] = true
		}
		if e.RangeID != "" && (e.Event == LedgerRangeCompleted || e.Event == LedgerRangeEmpty || e.Event == LedgerPartCommitted) {
			providerOfRange[e.RangeID] = e.Provider
		}
	}
	blacklist := map[string]bool{}
	for _, g := range report.Gaps {
		if g.Type == v3.GapSuspiciousEmpty || g.Type == v3.GapCountGap {
			key := fmt.Sprintf("%d_%d", g.FromBlock, g.ToBlock)
			if p := providerOfRange[key]; p != "" {
				blacklist[p] = true
			}
		}
	}
	var available []string
	for name, a := range s.adapters {
		if !a.Available() {
			continue
		}
		m, ok := a.(interface{ ManualOnly() bool })
		if ok && m.ManualOnly() {
			continue
		}
		available = append(available, name)
	}
	var usedList, blacklistList []string
	for p := range used {
		usedList = append(usedList, p)
	}
	for p := range blacklist {
		blacklistList = append(blacklistList, p)
	}
	planner := v3.NewRepairPlanner(available, usedList, blacklistList)
	chosen := planner.Select()
	if chosen == "" {
		chosen = "" // 无可用补洞 Provider：保持缺口 → PARTIAL
	}
	ds.RepairRounds++
	ds.Status = DatasetRunning
	ds.Validation = report
	ds.Error = ""
	ds.UpdatedAt = time.Now().UTC()
	_ = s.store.SaveDataset(ds)
	if cp := s.checkpointLocked(dsID); cp != nil {
		cp.PendingRanges = append([]BlockRange(nil), report.MissingRanges...)
		_ = s.cp.Save(cp)
	}
	now := time.Now().UTC()
	for _, r := range report.MissingRanges {
		gapID := fmt.Sprintf("%d_%d", r.From, r.To)
		attempts, _ := gapStore.RepairCount(gapID)
		if attempts >= v3.MaxRepairAttempts {
			continue // 超过补洞上限 → 保持 PARTIAL（设计 §28/§59）
		}
		var failed []string
		if chosen != "" {
			for _, a := range available {
				if a != chosen {
					failed = append(failed, a)
				}
			}
		}
		rj := &RangeJob{
			ID: uuid.NewString(), DatasetJobID: dsID, BatchID: ds.BatchID,
			AddressJobID: ds.AddressJobID, Address: ds.Address, Dataset: ds.Dataset,
			FromBlock: r.From, ToBlock: r.To, Status: RangeReady,
			Provider: chosen, FailedProviders: failed,
			Purpose:   string(v3.PurposeRepair),
			CreatedAt: now, UpdatedAt: now,
		}
		_ = s.store.SaveRange(rj)
		_ = NewLedger(s.store.Root(), dsID).Append(LedgerEntry{
			Event: LedgerRangeCreated, DatasetJobID: dsID,
			RangeID: r.Key(), FromBlock: r.From, ToBlock: r.To,
			Provider: chosen, Error: "REPAIR",
		})
		_ = gapStore.AppendRepair(v3.RepairAttempt{
			GapID: gapID, Provider: chosen, Attempt: attempts + 1,
			StartedAt: now,
		})
		s.events.Publish(Event{
			Type: EventRepairStarted, DatasetJobID: dsID, RangeID: r.Key(),
			Provider: chosen, Status: "REPAIR", Message: fmt.Sprintf("补洞 %d-%d", r.From, r.To),
		})
	}
	_ = gapStore.SaveState(v3.StateRepairing, "repair")
	s.recordGapRepairHistory(ds, true, false)
	logger.Log.Info().Str("dataset_job", dsID).Int("repair_ranges", len(report.MissingRanges)).
		Int("round", ds.RepairRounds).Msg("smartdownload_gap_repair_created")
	return true
}

// finishValidationPipeline 校验收尾：状态机 + Gap Ledger + Validation Certificate + 事件（Validation V3）。
func (s *Service) finishValidationPipeline(dsID string, report *ValidationReport) {
	cp, err := s.cp.Load(dsID)
	if err != nil {
		return
	}
	ledgerEntries, _ := NewLedger(s.store.Root(), dsID).Replay()
	store := v3.NewGapStore(s.store.Root(), dsID)
	providers := map[string]bool{}
	switches := 0
	for _, e := range ledgerEntries {
		if e.Provider != "" {
			providers[e.Provider] = true
		}
		if e.Event == LedgerProviderSwitched {
			switches++
		}
	}
	var providersList []string
	for p := range providers {
		providersList = append(providersList, p)
	}
	for _, g := range report.Gaps {
		_ = store.AppendGap(g)
		s.events.Publish(Event{Type: EventGapDetected, DatasetJobID: dsID,
			RangeID: fmt.Sprintf("%d_%d", g.FromBlock, g.ToBlock),
			Status:  string(g.Type), Message: g.Reason})
	}
	allGaps, _ := store.LoadGaps()
	latest := map[string]v3.GapStatus{}
	for _, g := range allGaps {
		latest[g.GapID] = g.Status
	}
	detected, repaired, remaining := len(latest), 0, 0
	for _, st := range latest {
		switch st {
		case v3.GapRepaired:
			repaired++
		case v3.GapDetected, v3.GapRepairing:
			remaining++
		}
	}
	if remaining == 0 && len(report.MissingRanges) > 0 {
		remaining = len(report.MissingRanges)
	}
	certStatus := "PASS"
	state := v3.StatePass
	switch report.Status {
	case "PARTIAL":
		certStatus, state = "PARTIAL", v3.StatePartial
	case "FAILED":
		certStatus, state = "FAILED", v3.StateFailed
	}
	if len(report.Gaps) > 0 && state == v3.StatePass {
		state = v3.StateRepairing
	}
	cert := &v3.Certificate{
		DatasetJobID: dsID, Status: certStatus,
		RequestedRange: v3.BlockInterval{From: cp.RequestedFrom, To: cp.RequestedTo},
		Coverage:       report.BlockCoverage,
		RowsRaw:        report.RawRows, RowsNormalized: report.Rows,
		RowsUnique: report.UniqueKeyCount, RowsFinal: report.UniqueKeyCount,
		DuplicatesRemoved: report.DuplicateCount,
		PartsCount:        len(cp.Parts), DuplicateSHA: report.PartsDuplicateSHA,
		GapsDetected: detected, GapsRepaired: repaired,
		GapsRemaining:          remaining,
		CrossCheckSampleRanges: report.CrossCheck.Windows,
		CrossCheckMatched:      report.CrossCheck.Windows - report.CrossCheck.Mismatch,
		ProvidersUsed:          providersList, ProviderSwitches: switches,
		CertifiedAt: time.Now().UTC(),
	}
	_ = store.SaveState(state, "certified")
	_ = store.SaveCertificate(cert)
	s.events.Publish(Event{Type: EventValidationCompleted, DatasetJobID: dsID,
		Status: certStatus, Message: fmt.Sprintf("coverage=%.4f gaps=%d", cert.Coverage, len(report.Gaps))})
	if len(report.Gaps) > 0 {
		s.recordGapRepairHistory(s.store.GetDataset(dsID), true, false)
	}
}

// completeRepair 补洞成功后标记 gap repaired 并写入历史（调用方持 service.mu）。
func (s *Service) completeRepair(ds *DatasetJob, rj *RangeJob, rows int64) {
	if ds == nil || rj == nil || rj.Purpose != string(v3.PurposeRepair) {
		return
	}
	gapID := fmt.Sprintf("%d_%d", rj.FromBlock, rj.ToBlock)
	store := v3.NewGapStore(s.store.Root(), ds.ID)
	attempts, _ := store.RepairCount(gapID)
	_ = store.AppendRepair(v3.RepairAttempt{
		GapID: gapID, Provider: rj.Provider, Attempt: attempts + 1,
		Success: true, Rows: rows,
		StartedAt: nowTime(rj.StartedAt), FinishedAt: time.Now().UTC(),
	})
	_ = store.AppendGap(v3.GapRecord{
		GapID: gapID, Type: v3.GapRangeGap, FromBlock: rj.FromBlock, ToBlock: rj.ToBlock,
		Status: v3.GapRepaired, Provider: rj.Provider, Rows: rows,
		CreatedAt: time.Now().UTC(),
	})
	s.events.Publish(Event{Type: EventRepairCompleted, DatasetJobID: ds.ID,
		RangeID: gapID, Provider: rj.Provider, Status: "REPAIRED",
		Message: fmt.Sprintf("补洞完成 %d 行", rows)})
	s.recordGapRepairHistory(ds, false, true)
}

// recordGapRepairHistory 把缺口/修复反馈写入 Provider 历史画像（Validation → Scheduler 闭环）。
func (s *Service) recordGapRepairHistory(ds *DatasetJob, gap, repair bool) {
	if ds == nil || ds.CurrentProvider == "" {
		return
	}
	s.history.Record(feedback.Record{
		ChainID: s.chainIDOfDataset(ds), Dataset: ds.Dataset,
		Provider: ds.CurrentProvider, ScaleBucket: feedback.ScaleBucket(ds.EstimatedRows),
		Gap: gap, Repair: repair,
	})
}

func nowTime(t *time.Time) time.Time {
	if t == nil {
		return time.Now().UTC()
	}
	return t.UTC()
}

func (s *Service) finalizeAddressIfDoneLocked(addressID string) {
	a := s.store.GetAddress(addressID)
	if a == nil || a.Status.Terminal() {
		return
	}
	datasets := s.store.ListDatasetsByAddress(addressID)
	logger.Log.Warn().Str("address", addressID).Int("datasets", len(datasets)).
		Msg("DEBUG_finalize_address")
	done := 0
	partial := false
	for _, ds := range datasets {
		if ds.Status.Terminal() {
			done++
		}
		if ds.Status == DatasetPartial {
			partial = true
		}
	}
	if done != len(datasets) {
		return
	}
	now := time.Now().UTC()
	switch {
	case a.CancelRequested:
		a.Status = AddressCanceled
	case a.PauseRequested:
		a.Status = AddressPaused
	case partial:
		a.Status = AddressPartial
	default:
		a.Status = AddressCompleted
		a.FinishedAt = &now
	}
	a.UpdatedAt = now
	_ = s.store.SaveAddress(a)
}

func reportStatus(r *ValidationReport) string {
	if r == nil {
		return ""
	}
	return r.Status
}

func validationSummary(r *ValidationReport, err error) string {
	if err != nil {
		return sanitizeText(err.Error())
	}
	if r == nil {
		return ""
	}
	return fmt.Sprintf("score=%.0f coverage=%.2f dup=%d unknown=%d",
		r.Score, r.Coverage, r.DuplicateCount, len(r.UnknownRanges))
}

func errMsg(err error) string {
	if err == nil {
		return ""
	}
	return sanitizeText(err.Error())
}

// trySettle 检查批次是否可进入终态/暂停态；返回 true 表示 Worker 应退出。
func (s *Service) trySettle(batchID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := s.store.GetBatch(batchID)
	if batch == nil || batch.Status.Terminal() {
		return true
	}
	if batch.CancelRequested && !s.batchHasRunningRange(batchID) {
		s.cancelBatchLocked(batchID)
		return true
	}
	if batch.PauseRequested && !s.batchHasRunningRange(batchID) {
		s.transitionBatchPausedLocked(batchID)
		return true
	}
	addresses := s.store.ListAddressesByBatch(batchID)
	if len(addresses) == 0 {
		batch.Status = BatchFailed
		batch.Error = "批次没有地址任务"
		batch.UpdatedAt = time.Now().UTC()
		_ = s.store.SaveBatch(batch)
		return true
	}
	done, failed, canceled, partial := 0, 0, 0, 0
	for _, a := range addresses {
		if a.Status.Terminal() {
			switch a.Status {
			case AddressCompleted:
				done++
			case AddressFailed:
				failed++
			case AddressCanceled:
				canceled++
			case AddressPartial:
				partial++
			}
		}
	}
	if done+failed+canceled+partial == len(addresses) {
		now := time.Now().UTC()
		switch {
		case (failed > 0 || partial > 0) && done > 0:
			batch.Status = BatchPartial
			batch.Error = fmt.Sprintf("%d 个地址失败/部分，%d 个成功", failed+partial, done)
		case failed > 0:
			batch.Status = BatchFailed
			batch.Error = fmt.Sprintf("%d 个地址失败", failed)
		case partial > 0:
			batch.Status = BatchPartial
			batch.Error = fmt.Sprintf("%d 个地址数据不完整", partial)
		case canceled > 0 && canceled == len(addresses):
			batch.Status = BatchCanceled
		default:
			batch.Status = BatchCompleted
		}
		batch.FinishedAt = &now
		batch.UpdatedAt = now
		_ = s.store.SaveBatch(batch)
		return true
	}
	return false
}

// ── 暂停/取消的原子转换（调用方必须持 service.mu）──

func (s *Service) transitionBatchPausedLocked(batchID string) {
	batch := s.store.GetBatch(batchID)
	if batch == nil || batch.Status.Terminal() {
		return
	}
	batch.PauseRequested = false
	batch.Status = BatchPaused
	batch.UpdatedAt = time.Now().UTC()
	_ = s.store.SaveBatch(batch)
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		if !a.Status.Terminal() {
			a.PauseRequested = false
			a.Status = AddressPaused
			a.UpdatedAt = time.Now().UTC()
			_ = s.store.SaveAddress(a)
		}
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			if !ds.Status.Terminal() {
				ds.PauseRequested = false
				ds.Status = DatasetPaused
				ds.UpdatedAt = time.Now().UTC()
				_ = s.store.SaveDataset(ds)
			}
		}
	}
}

func (s *Service) transitionAddressPausedLocked(addressID string) {
	a := s.store.GetAddress(addressID)
	if a == nil || a.Status.Terminal() {
		return
	}
	a.PauseRequested = false
	a.Status = AddressPaused
	a.UpdatedAt = time.Now().UTC()
	_ = s.store.SaveAddress(a)
	for _, ds := range s.store.ListDatasetsByAddress(addressID) {
		if !ds.Status.Terminal() {
			ds.PauseRequested = false
			ds.Status = DatasetPaused
			ds.UpdatedAt = time.Now().UTC()
			_ = s.store.SaveDataset(ds)
		}
	}
}

func (s *Service) cancelAddressLocked(addressID string) {
	a := s.store.GetAddress(addressID)
	if a == nil || a.Status.Terminal() {
		return
	}
	for _, ds := range s.store.ListDatasetsByAddress(addressID) {
		s.cancelDatasetLocked(ds.ID)
	}
	a.Status = AddressCanceled
	now := time.Now().UTC()
	a.FinishedAt = &now
	a.UpdatedAt = now
	_ = s.store.SaveAddress(a)
}

func (s *Service) cancelDatasetLocked(datasetJobID string) {
	ds := s.store.GetDataset(datasetJobID)
	if ds == nil || ds.Status.Terminal() {
		return
	}
	for _, r := range s.store.ListRangesByDataset(datasetJobID) {
		if r.Status == RangePending || r.Status == RangeReady || r.Status == RangeFailed {
			r.Status = RangeCanceled
			now := time.Now().UTC()
			r.FinishedAt = &now
			r.UpdatedAt = now
			_ = s.store.SaveRange(r)
		}
	}
	ds.Status = DatasetCanceled
	now := time.Now().UTC()
	ds.FinishedAt = &now
	ds.UpdatedAt = now
	_ = s.store.SaveDataset(ds)
}

func (s *Service) cancelBatchLocked(batchID string) {
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		s.cancelAddressLocked(a.ID)
	}
	batch := s.store.GetBatch(batchID)
	batch.CancelRequested = false
	batch.Status = BatchCanceled
	now := time.Now().UTC()
	batch.FinishedAt = &now
	batch.UpdatedAt = now
	_ = s.store.SaveBatch(batch)
}

// ── 进度与查询 ──

func (s *Service) updateProgressLocked(datasetJobID string) {
	ds := s.store.GetDataset(datasetJobID)
	if ds == nil {
		return
	}
	ranges := s.store.ListRangesByDataset(datasetJobID)
	if len(ranges) == 0 {
		ds.Progress = ProgressSnapshot{Percent: 1, RowsTotal: ds.EstimatedRows, BytesTotal: ds.EstimatedBytes}
		_ = s.store.SaveDataset(ds)
		return
	}
	var rows, blocks uint64
	weighted := make([]pg.RangeProgress, 0, len(ranges))
	for _, r := range ranges {
		pct := 0.0
		if r.Status == RangeCompleted || r.Status == RangeEmpty {
			pct = 1
			rows += r.RowsCommitted
			blocks += r.ToBlock - r.FromBlock + 1
		}
		weighted = append(weighted, pg.RangeProgress{Weight: s.rangeWeight(ds, r), Percent: pct})
	}
	ds.Progress = ProgressSnapshot{
		Percent:       pg.WeightedProgress(weighted),
		RowsCurrent:   rows,
		RowsTotal:     ds.EstimatedRows,
		BlocksCurrent: blocks,
		BlocksTotal:   totalBlocks(ranges),
		BytesCurrent:  rows * 128,
		BytesTotal:    ds.EstimatedBytes,
	}
	s.applyETA(ds)
	s.monitorCloudTierLocked(ds)
	_ = s.store.SaveDataset(ds)
	a := s.store.GetAddress(ds.AddressJobID)
	if a != nil {
		datasets := s.store.ListDatasetsByAddress(a.ID)
		if len(datasets) > 0 {
			totalW, wp := 0.0, 0.0
			rows = 0
			recalc := false
			for _, d := range datasets {
				w := float64(d.EstimatedRows) * cloudplanner.DatasetComplexity(d.Dataset)
				if w <= 0 {
					w = float64(d.Progress.BlocksTotal) + 1
				}
				totalW += w
				wp += w * d.Progress.Percent
				rows += d.Progress.RowsCurrent
				if d.Progress.ETARecalculating {
					recalc = true
				}
			}
			if totalW <= 0 {
				totalW = 1
			}
			a.Progress = ProgressSnapshot{
				Percent:              wp / totalW,
				RowsCurrent:          rows,
				RowsTotal:            sumRowsTotal(datasets),
				BytesTotal:           sumBytesTotal(datasets),
				SpeedRowsPerSec:      avgSpeed(datasets),
				ETASeconds:           maxETA(datasets),
				ETAConfidence:        avgConfidence(datasets),
				ETALowerBoundSeconds: maxLower(datasets),
				ETAUpperBoundSeconds: maxUpper(datasets),
				ETARecalculating:     recalc,
			}
			_ = s.store.SaveAddress(a)
		}
	}
	s.events.Publish(Event{
		Type: EventDatasetUpdated, BatchID: ds.BatchID, AddressJobID: ds.AddressJobID,
		DatasetJobID: ds.ID, Status: string(ds.Status),
		Payload: map[string]any{
			"percent": ds.Progress.Percent, "rows": ds.Progress.RowsCurrent,
			"speed": ds.Progress.SpeedRowsPerSec, "eta": ds.Progress.ETASeconds,
		},
	})
}

// applyETA 使用 ETA Engine（EWMA + 滚动中位数 + 置信度 + 冷却 + 切换重算）。
func (s *Service) applyETA(ds *DatasetJob) {
	engine := s.etaEngines[ds.ID]
	if engine == nil || engine.Provider() != ds.CurrentProvider {
		engine = pg.NewETAEngine(ds.CurrentProvider)
		s.etaEngines[ds.ID] = engine
	}
	st := s.eta[ds.ID]
	if st == nil {
		st = &etaState{}
		s.eta[ds.ID] = st
	}
	now := time.Now()
	if st.started && !st.lastTime.IsZero() {
		dt := now.Sub(st.lastTime)
		if dt > 0 {
			deltaRows := ds.Progress.RowsCurrent - st.lastRows
			if ds.Progress.RowsCurrent < st.lastRows {
				deltaRows = 0
			}
			deltaBlocks := ds.Progress.BlocksCurrent - st.lastBlocks
			if ds.Progress.BlocksCurrent < st.lastBlocks {
				deltaBlocks = 0
			}
			_, _, _, _ = engine.Update(deltaRows, deltaBlocks, dt)
			rate := engine.RowsRate()
			blockRate := engine.BlocksRate()
			ds.Progress.SpeedRowsPerSec = rate
			remaining := float64(0)
			if ds.EstimatedRows > ds.Progress.RowsCurrent {
				remaining = float64(ds.EstimatedRows - ds.Progress.RowsCurrent)
			}
			remainingBlocks := float64(0)
			if ds.Progress.BlocksTotal > ds.Progress.BlocksCurrent {
				remainingBlocks = float64(ds.Progress.BlocksTotal - ds.Progress.BlocksCurrent)
			}
			recalc := ds.Progress.ETARecalculating
			if recalc && engine.SampleCount() >= 3 {
				recalc = false
			}
			eta := pg.ComputeETA(remaining, rate, remainingBlocks, blockRate,
				recalc, engine.SampleCount(), ds.DiscoveryConfidence,
				s.providerCooldown(ds.CurrentProvider))
			ds.Progress.ETASeconds = float64(eta.Seconds)
			ds.Progress.ETAConfidence = etaConfidenceFloat(eta.Confidence)
			ds.Progress.ETALowerBoundSeconds = float64(eta.LowerBoundSeconds)
			ds.Progress.ETAUpperBoundSeconds = float64(eta.UpperBoundSeconds)
			ds.Progress.ETARecalculating = eta.Recalculating
			ds.Progress.ETABasedOn = eta.BasedOn
		}
	}
	st.lastRows = ds.Progress.RowsCurrent
	st.lastBlocks = ds.Progress.BlocksCurrent
	st.lastTime = now
	st.started = true
}

// rangeWeight Range 权重：Discovery 分段估算行数优先，否则区块跨度（设计 §9）。
func (s *Service) rangeWeight(ds *DatasetJob, rj *RangeJob) float64 {
	if rj == nil {
		return 1
	}
	if ds != nil {
		for _, seg := range ds.ActivitySegments {
			if rj.FromBlock >= seg.FromBlock && rj.ToBlock <= seg.ToBlock && seg.EstimatedRows > 0 {
				segSpan := seg.ToBlock - seg.FromBlock + 1
				rjSpan := rj.ToBlock - rj.FromBlock + 1
				if segSpan > 0 {
					return float64(seg.EstimatedRows) * float64(rjSpan) / float64(segSpan)
				}
			}
		}
	}
	return float64(rj.ToBlock - rj.FromBlock + 1)
}

// resetETA Provider/Cloud Tier 切换时重置 ETA 引擎并标记重新计算（设计 §20/§21）。
func (s *Service) resetETA(ds *DatasetJob) {
	if ds == nil {
		return
	}
	s.etaEngines[ds.ID] = pg.NewETAEngine(ds.CurrentProvider)
	if st := s.eta[ds.ID]; st != nil {
		st.started = false
		st.prevSpeedRows, st.prevSpeedBlk = 0, 0
	}
	ds.Progress.ETARecalculating = true
	ds.Progress.ETAConfidence = 0
	ds.Progress.ETASeconds = 0
}

// providerCooldown 返回 Provider 当前熔断冷却剩余时间（ETA 叠加）。
func (s *Service) providerCooldown(provider string) time.Duration {
	if provider == "" {
		return 0
	}
	for name, info := range s.scheduler.Health().Snapshot() {
		if name != provider || info.CooldownUntil.IsZero() {
			continue
		}
		rem := time.Until(info.CooldownUntil)
		if rem < 0 {
			return 0
		}
		return rem
	}
	return 0
}

func etaConfidenceFloat(c string) float64 {
	switch c {
	case "HIGH":
		return 0.9
	case "MEDIUM":
		return 0.7
	case "LOW":
		return 0.4
	default:
		return 0
	}
}

func maxLower(datasets []*DatasetJob) float64 {
	var m float64
	for _, d := range datasets {
		if d.Progress.ETALowerBoundSeconds > m {
			m = d.Progress.ETALowerBoundSeconds
		}
	}
	return m
}

func maxUpper(datasets []*DatasetJob) float64 {
	var m float64
	for _, d := range datasets {
		if d.Progress.ETAUpperBoundSeconds > m {
			m = d.Progress.ETAUpperBoundSeconds
		}
	}
	return m
}

// ensureETAStarted 在 Range 领取时初始化 EWMA 基线（首次进度更新即可算速度/ETA）。
func (s *Service) ensureETAStarted(ds *DatasetJob) {
	if ds == nil {
		return
	}
	st := s.eta[ds.ID]
	if st == nil {
		st = &etaState{}
		s.eta[ds.ID] = st
	}
	if !st.started {
		st.started = true
		st.lastRows = ds.Progress.RowsCurrent
		st.lastTime = time.Now()
	}
}

func sumRowsTotal(datasets []*DatasetJob) uint64 {
	var n uint64
	for _, d := range datasets {
		n += d.Progress.RowsTotal
	}
	return n
}

func sumBytesTotal(datasets []*DatasetJob) uint64 {
	var n uint64
	for _, d := range datasets {
		n += d.Progress.BytesTotal
	}
	return n
}

func avgSpeed(datasets []*DatasetJob) float64 {
	if len(datasets) == 0 {
		return 0
	}
	var n float64
	for _, d := range datasets {
		n += d.Progress.SpeedRowsPerSec
	}
	return n / float64(len(datasets))
}

func maxETA(datasets []*DatasetJob) float64 {
	var m float64
	for _, d := range datasets {
		if d.Progress.ETASeconds > m {
			m = d.Progress.ETASeconds
		}
	}
	return m
}

func avgConfidence(datasets []*DatasetJob) float64 {
	if len(datasets) == 0 {
		return 0
	}
	var n float64
	for _, d := range datasets {
		n += d.Progress.ETAConfidence
	}
	return n / float64(len(datasets))
}

// ensureCloudPlanLocked 为 SQD Cloud 数据集生成/刷新资源分档（调用方持 service.mu）。
func (s *Service) ensureCloudPlanLocked(ds *DatasetJob) {
	if ds == nil || ds.CurrentProvider != "sqd_cloud" {
		return
	}
	// 已分档（含运行期自动升级）不再重算覆盖，避免每次领取把升级结果打回原档
	if ds.CloudTier != "" {
		return
	}
	ranges := s.store.ListRangesByDataset(ds.ID)
	runtime := ds.CloudEstimatedRuntimeSeconds
	if runtime <= 0 {
		if ds.EstimatedRows > 0 {
			runtime = float64(ds.EstimatedRows) / 10_000 // 粗估 10k rows/s
		} else {
			runtime = 600
		}
	}
	in := cloudplanner.ProbeInput{
		EstimatedRows:           ds.EstimatedRows,
		EstimatedBytes:          ds.EstimatedBytes,
		EstimatedRuntimeSeconds: runtime,
		RangeCount:              len(ranges),
		Dataset:                 ds.Dataset,
	}
	plan := s.cloudPlanner.Plan(in)
	if ds.CloudTier == string(plan.Tier) && ds.CloudScore == plan.Score {
		return
	}
	ds.CloudTier = string(plan.Tier)
	ds.CloudScore = plan.Score
	ds.CloudReasons = append([]string(nil), plan.Reasons...)
	ds.CloudEstimatedCost = plan.EstimatedCost
	ds.CloudEstimatedRuntimeSeconds = runtime
	ds.UpdatedAt = time.Now().UTC()
	_ = s.store.SaveDataset(ds)
	_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
		Event: LedgerCloudTierAssigned, DatasetJobID: ds.ID,
		Provider: "sqd_cloud", Error: fmt.Sprintf("tier=%s score=%.1f cost=¥%.2f",
			plan.Tier, plan.Score, plan.EstimatedCost),
	})
	s.events.Publish(Event{
		Type: EventResourceSwitched, DatasetJobID: ds.ID, Provider: "sqd_cloud",
		Status: string(plan.Tier), Message: "Cloud 资源分档",
		Payload: map[string]any{"score": plan.Score, "cost": plan.EstimatedCost},
	})
	logger.Log.Info().Str("dataset_job", ds.ID).Str("tier", string(plan.Tier)).
		Float64("score", plan.Score).Msg("smartdownload_cloud_tier_assigned")
}

// monitorCloudTierLocked 运行期监控：命中升级/降级条件则切换 Cloud 资源档（调用方持 service.mu）。
func (s *Service) monitorCloudTierLocked(ds *DatasetJob) {
	if ds == nil || ds.CurrentProvider != "sqd_cloud" || ds.CloudTier == "" {
		return
	}
	current := cloudplanner.ResourcePlan{Tier: cloudplanner.CloudTier(ds.CloudTier)}
	current.ApplySpecs()
	metrics := cloudplanner.RuntimeMetrics{
		RowsPerSecond:            ds.Progress.SpeedRowsPerSec,
		CompletedPercent:         ds.Progress.Percent,
		ETA:                      time.Duration(ds.Progress.ETASeconds * float64(time.Second)),
		OriginalEstimatedRuntime: time.Duration(ds.CloudEstimatedRuntimeSeconds * float64(time.Second)),
	}
	next := s.cloudPlanner.Reevaluate(current, metrics)
	if next.Tier == current.Tier {
		return
	}
	event := LedgerCloudTierUpgraded
	if next.Tier == cloudplanner.CloudL && current.Tier == cloudplanner.CloudXL {
		event = LedgerCloudTierDowngraded
	}
	ds.CloudTier = string(next.Tier)
	ds.CloudReasons = append(ds.CloudReasons, next.Reasons...)
	ds.UpdatedAt = time.Now().UTC()
	_ = s.store.SaveDataset(ds)
	_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
		Event: event, DatasetJobID: ds.ID, Provider: "sqd_cloud",
		Error: fmt.Sprintf("tier %s → %s：%s", current.Tier, next.Tier,
			strings.Join(next.Reasons, "；")),
	})
	s.events.Publish(Event{
		Type: EventResourceSwitched, DatasetJobID: ds.ID, Provider: "sqd_cloud",
		Status: string(next.Tier), Message: strings.Join(next.Reasons, "；"),
		Payload: map[string]any{"from": string(current.Tier), "to": string(next.Tier)},
	})
	logger.Log.Info().Str("dataset_job", ds.ID).Str("from", string(current.Tier)).
		Str("to", string(next.Tier)).Msg("smartdownload_cloud_tier_switched")
}

func totalBlocks(ranges []*RangeJob) uint64 {
	var total uint64
	for _, r := range ranges {
		total += r.ToBlock - r.FromBlock + 1
	}
	return total
}

func (s *Service) batchHasRunningRange(batchID string) bool {
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			for _, r := range s.store.ListRangesByDataset(ds.ID) {
				if r.Status == RangeRunning {
					return true
				}
			}
		}
	}
	return false
}

func (s *Service) addressHasRunningRange(addressID string) bool {
	for _, ds := range s.store.ListDatasetsByAddress(addressID) {
		for _, r := range s.store.ListRangesByDataset(ds.ID) {
			if r.Status == RangeRunning {
				return true
			}
		}
	}
	return false
}

func (s *Service) datasetHasRunningRange(datasetJobID string) bool {
	for _, r := range s.store.ListRangesByDataset(datasetJobID) {
		if r.Status == RangeRunning {
			return true
		}
	}
	return false
}

// ── Checkpoint 缓存 ──

func (s *Service) checkpointLocked(datasetJobID string) *CheckpointV3 {
	if cp, ok := s.cpCache[datasetJobID]; ok {
		return cp
	}
	cp, err := s.cp.Load(datasetJobID)
	if err != nil {
		// Pack 大批次延迟建 checkpoint：从 Range Job 重建
		ranges := s.store.ListRangesByDataset(datasetJobID)
		ds := s.store.GetDataset(datasetJobID)
		if len(ranges) == 0 || ds == nil {
			return nil
		}
		cp = &CheckpointV3{
			DatasetJobID:  datasetJobID,
			Address:       ds.Address,
			Dataset:       ds.Dataset,
			RequestedFrom: ranges[0].FromBlock,
			RequestedTo:   ranges[len(ranges)-1].ToBlock,
		}
		for _, r := range ranges {
			cp.PendingRanges = append(cp.PendingRanges, BlockRange{From: r.FromBlock, To: r.ToBlock})
		}
		_ = s.cp.Save(cp)
	}
	s.cpCache[datasetJobID] = cp
	return cp
}

// Checkpoint 返回指定数据集的 Checkpoint V3（API 展示）。
func (s *Service) Checkpoint(datasetJobID string) (*CheckpointV3, error) {
	return s.cp.Load(datasetJobID)
}

// LedgerEntries 返回指定数据集的 Range Ledger（API 展示）。
func (s *Service) LedgerEntries(datasetJobID string) ([]LedgerEntry, error) {
	return NewLedger(s.store.Root(), datasetJobID).Replay()
}

// RepairDatasetGaps 手动触发补洞：重新校验，若存在覆盖缺口则创建补洞 Range（上限 2 轮）。
func (s *Service) RepairDatasetGaps(dsID string) (*ValidationReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report, err := s.validator.ValidateDataset(ctx, dsID)
	if err != nil {
		return report, err
	}
	if report.Status != "PARTIAL" || len(report.MissingRanges) == 0 {
		return report, nil
	}
	s.mu.Lock()
	ds := s.store.GetDataset(dsID)
	if ds == nil {
		s.mu.Unlock()
		return report, fmt.Errorf("数据集不存在: %s", dsID)
	}
	switch ds.Status {
	case DatasetValidating, DatasetPartial:
		if ds.Status == DatasetPartial {
			ds.Status = DatasetValidating
			ds.UpdatedAt = time.Now().UTC()
			_ = s.store.SaveDataset(ds)
		}
		s.repairDatasetGapsLocked(dsID, report)
		s.mu.Unlock()
		return report, nil
	default:
		s.mu.Unlock()
		return report, fmt.Errorf("数据集状态 %s 不允许手动补洞", ds.Status)
	}
}

// ── 查询 ──

func (s *Service) ListBatches() []*BatchJob { return s.store.ListBatches() }

func (s *Service) GetBatch(id string) *BatchJob { return s.store.GetBatch(id) }

func (s *Service) GetAddress(id string) *AddressJob { return s.store.GetAddress(id) }

func (s *Service) GetDataset(id string) *DatasetJob { return s.store.GetDataset(id) }

// SnapshotBatch 返回完整任务树。
func (s *Service) SnapshotBatch(batchID string) *BatchDetail {
	batch := s.store.GetBatch(batchID)
	if batch == nil {
		return nil
	}
	detail := &BatchDetail{Batch: batch}
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		ad := &AddressDetail{Address: a}
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			dd := &DatasetDetail{Dataset: ds, Ranges: s.store.ListRangesByDataset(ds.ID)}
			ad.Datasets = append(ad.Datasets, dd)
		}
		detail.Addresses = append(detail.Addresses, ad)
	}
	return detail
}

// BatchSnapshot 计算批次加权进度快照（Weighted Address → Batch；设计 §11）。
func (s *Service) BatchSnapshot(batchID string) *pg.ProgressSnapshot {
	batch := s.store.GetBatch(batchID)
	if batch == nil {
		return nil
	}
	snap := &pg.ProgressSnapshot{
		EntityType: "batch", EntityID: batchID,
		Status: string(batch.Status), UpdatedAt: time.Now().UTC(),
	}
	addresses := s.store.ListAddressesByBatch(batchID)
	byAddr := map[string][]*DatasetJob{}
	for _, d := range s.store.ListDatasets() {
		if d.BatchID != batchID {
			continue
		}
		byAddr[d.AddressJobID] = append(byAddr[d.AddressJobID], d)
	}
	items := make([]pg.RangeProgress, 0, len(addresses))
	completed, failed := 0, 0
	var maxETA, confSum float64
	var rows uint64
	recalc := false
	for _, a := range addresses {
		w, p := weightedFromDatasets(byAddr[a.ID])
		items = append(items, pg.RangeProgress{Weight: w, Percent: p})
		switch a.Status {
		case AddressCompleted:
			completed++
		case AddressFailed:
			failed++
		}
		if a.Progress.ETASeconds > maxETA {
			maxETA = a.Progress.ETASeconds
		}
		confSum += a.Progress.ETAConfidence
		rows += a.Progress.RowsCurrent
		if a.Progress.ETARecalculating {
			recalc = true
		}
	}
	snap.ProgressPercent = pg.WeightedProgress(items)
	snap.RangesCurrent = uint64(completed)
	snap.RangesTotal = uint64(len(addresses))
	snap.RowsCurrent = rows
	conf := float64(0)
	if len(addresses) > 0 {
		conf = confSum / float64(len(addresses))
	}
	snap.ETA = pg.ETASnapshot{
		Seconds: int64(maxETA), Confidence: etaConfidenceLevel(conf), Recalculating: recalc,
	}
	_ = failed
	return snap
}

// addressWeightedProgress 地址加权进度（Dataset 权重 = 估算行数 × 复杂度；设计 §10）。
func (s *Service) addressWeightedProgress(a *AddressJob) (weight, percent float64) {
	if a == nil {
		return 0, 0
	}
	return weightedFromDatasets(s.store.ListDatasetsByAddress(a.ID))
}

func weightedFromDatasets(datasets []*DatasetJob) (weight, percent float64) {
	var wSum, wp float64
	for _, d := range datasets {
		w := float64(d.EstimatedRows) * cloudplanner.DatasetComplexity(d.Dataset)
		if w <= 0 {
			w = float64(d.Progress.BlocksTotal) + 1
		}
		wSum += w
		wp += w * d.Progress.Percent
	}
	if wSum <= 0 {
		return 1, 0
	}
	return wSum, wp / wSum
}

func etaConfidenceLevel(c float64) string {
	switch {
	case c >= 0.8:
		return "HIGH"
	case c >= 0.5:
		return "MEDIUM"
	case c > 0:
		return "LOW"
	default:
		return "UNKNOWN"
	}
}

// PartsDir 返回 parts 根目录（recovery/API 用）。
func (s *Service) PartsDir() string {
	if w, ok := s.writer.(*JSONLPartWriter); ok {
		return w.PartsDir()
	}
	return filepath.Join(s.store.Root(), "smart_download", "parts")
}

// fileSHA256 计算文件 SHA256。
func fileSHA256(path string) (string, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
