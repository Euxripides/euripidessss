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
	"github.com/google/uuid"
)

var evmAddressRE = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// Options 服务配置。
type Options struct {
	Workers         int    // 单批次并发 Worker 数
	RetryLimit      int    // 单个 Range 重试上限
	DefaultEndBlock uint64 // FULL 模式未显式给 to_block 时的默认终点
	RangeChunkSize  uint64 // Range 切分大小（区块数）
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
	duckdbEngine  *duckdb.Engine
	rangeCoverage RangeCoverageSource
	results       *ResultProcessor
	onIndexed     func(*IndexedResult)
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
		store:     store,
		cp:        NewCheckpointStore(store.Root()),
		opts:      opts,
		writer:    writer,
		adapters:  map[string]ProviderAdapter{},
		scheduler: NewSmartScheduler(),
		events:    NewEventBus(300 * time.Millisecond),
		eta:       map[string]*etaState{},
		ctx:       ctx,
		cancel:    cancel,
		workers:   map[string]bool{},
		cpCache:   map[string]*CheckpointV3{},
	}
	svc.validator = NewValidator(svc)
	svc.results = NewResultProcessor(svc)
	return svc
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
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.SaveBatch(batch); err != nil {
		return nil, err
	}
	datasetJobs, rangeJobs := 0, 0
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
				} else if len(missing) == 0 {
					if err := s.store.SaveDataset(dsJob); err != nil {
						return nil, err
					}
					if err := s.markDatasetReused(dsID, requested, reused, 0); err != nil {
						return nil, err
					}
					rangeJobs += len(reused)
					continue
				} else {
					if err := s.store.SaveDataset(dsJob); err != nil {
						return nil, err
					}
					if err := s.createReuseDataset(dsID, dsJob, addrID, batchID, addr, ds, requested, reused, missing, now); err != nil {
						return nil, err
					}
					rangeJobs += len(missing)
					continue
				}
			}
			cp := &CheckpointV3{}
			cp.Init(dsID, addr, ds, requested, s.opts.RangeChunkSize)
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
		Batch:       batch,
		Valid:       len(valid),
		Invalid:     invalid,
		Duplicates:  duplicates,
		DatasetJobs: datasetJobs,
		RangeJobs:   rangeJobs,
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
			ds.EstimatedRows = dp.EstimatedRows
			ds.EstimatedBytes = dp.EstimatedBytes
			ds.PreferredProvider = dp.PreferredProvider
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
					ds.UpdatedAt = time.Now().UTC()
					_ = s.store.SaveDataset(ds)
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
					return &claimedRange{
						rangeID:      r.ID,
						datasetJobID: ds.ID,
						addressJobID: a.ID,
						batchID:      batchID,
						provider:     provider,
						adapter:      adapter,
						req: RangeRequest{
							DatasetJobID: ds.ID,
							Address:      a.Address,
							Dataset:      ds.Dataset,
							ChainKey:     a.ChainKey,
							ChainID:      a.ChainID,
							FromBlock:    r.FromBlock,
							ToBlock:      r.ToBlock,
						},
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
	ds := s.store.GetDataset(claim.datasetJobID)
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
		s.mu.Unlock()
		s.events.Publish(Event{Type: EventResultReady, DatasetJobID: dsID, Status: "VALIDATED", Message: "完整性校验通过"})
		go s.indexDataset(dsID)
	}
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
		rj := &RangeJob{
			ID: uuid.NewString(), DatasetJobID: dsID, BatchID: ds.BatchID,
			AddressJobID: ds.AddressJobID, Address: ds.Address, Dataset: ds.Dataset,
			FromBlock: r.From, ToBlock: r.To, Status: RangeReady,
			CreatedAt: now, UpdatedAt: now,
		}
		_ = s.store.SaveRange(rj)
		_ = NewLedger(s.store.Root(), dsID).Append(LedgerEntry{
			Event: LedgerRangeCreated, DatasetJobID: dsID,
			RangeID: r.Key(), FromBlock: r.From, ToBlock: r.To,
		})
	}
	logger.Log.Info().Str("dataset_job", dsID).Int("repair_ranges", len(report.MissingRanges)).
		Int("round", ds.RepairRounds).Msg("smartdownload_gap_repair_created")
	return true
}

func (s *Service) finalizeAddressIfDoneLocked(addressID string) {
	a := s.store.GetAddress(addressID)
	if a == nil || a.Status.Terminal() {
		return
	}
	datasets := s.store.ListDatasetsByAddress(addressID)
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
	done := 0
	var rows, blocks uint64
	for _, r := range ranges {
		if r.Status == RangeCompleted || r.Status == RangeEmpty {
			done++
			rows += r.RowsCommitted
			blocks += r.ToBlock - r.FromBlock + 1
		}
	}
	ds.Progress = ProgressSnapshot{
		Percent:       float64(done) / float64(len(ranges)),
		RowsCurrent:   rows,
		RowsTotal:     ds.EstimatedRows,
		BlocksCurrent: blocks,
		BlocksTotal:   totalBlocks(ranges),
		BytesCurrent:  rows * 128,
		BytesTotal:    ds.EstimatedBytes,
	}
	s.applyETA(ds)
	_ = s.store.SaveDataset(ds)
	a := s.store.GetAddress(ds.AddressJobID)
	if a != nil {
		datasets := s.store.ListDatasetsByAddress(a.ID)
		if len(datasets) > 0 {
			total := 0.0
			rows = 0
			for _, d := range datasets {
				total += d.Progress.Percent
				rows += d.Progress.RowsCurrent
			}
			a.Progress = ProgressSnapshot{
				Percent:         total / float64(len(datasets)),
				RowsCurrent:     rows,
				RowsTotal:       sumRowsTotal(datasets),
				BytesTotal:      sumBytesTotal(datasets),
				SpeedRowsPerSec: avgSpeed(datasets),
				ETASeconds:      maxETA(datasets),
				ETAConfidence:   avgConfidence(datasets),
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

// applyETA 按 EWMA 计算数据集速度与 ETA（切换 Provider 后由新 Range 的进度自然重建）。
func (s *Service) applyETA(ds *DatasetJob) {
	st := s.eta[ds.ID]
	if st == nil {
		st = &etaState{}
		s.eta[ds.ID] = st
	}
	now := time.Now()
	if st.started && !st.lastTime.IsZero() {
		dt := now.Sub(st.lastTime).Seconds()
		if dt > 0 {
			curRows := float64(ds.Progress.RowsCurrent-st.lastRows) / dt
			speed := ewmaSpeed(st.prevSpeedRows, curRows, true)
			st.prevSpeedRows = speed
			ds.Progress.SpeedRowsPerSec = speed
			remaining := float64(0)
			if ds.EstimatedRows > ds.Progress.RowsCurrent {
				remaining = float64(ds.EstimatedRows - ds.Progress.RowsCurrent)
			}
			if speed > 0 && remaining > 0 {
				ds.Progress.ETASeconds = remaining / speed
				ds.Progress.ETAConfidence = 0.9
			} else {
				// 行数估算不可靠时退回区块级 ETA（remaining blocks / blocks per sec）
				curBlocks := float64(ds.Progress.BlocksCurrent-st.lastBlocks) / dt
				blkSpeed := ewmaSpeed(st.prevSpeedBlk, curBlocks, true)
				st.prevSpeedBlk = blkSpeed
				remainingBlocks := float64(0)
				if ds.Progress.BlocksTotal > ds.Progress.BlocksCurrent {
					remainingBlocks = float64(ds.Progress.BlocksTotal - ds.Progress.BlocksCurrent)
				}
				if blkSpeed > 0 && remainingBlocks > 0 {
					ds.Progress.ETASeconds = remainingBlocks / blkSpeed
					ds.Progress.ETAConfidence = 0.8
				} else {
					ds.Progress.ETAConfidence = 0
				}
			}
		}
	}
	st.lastRows = ds.Progress.RowsCurrent
	st.lastBlocks = ds.Progress.BlocksCurrent
	st.lastTime = now
	st.started = true
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
