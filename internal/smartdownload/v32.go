package smartdownload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/google/uuid"
)

// ResourceProfile is the bounded, user-facing alternative to tuning workers.
type ResourceProfile string

const (
	ResourceStandard    ResourceProfile = "STANDARD"
	ResourcePerformance ResourceProfile = "PERFORMANCE"
	ResourceExtreme     ResourceProfile = "EXTREME"
)

func (p ResourceProfile) Valid() bool {
	return p == ResourceStandard || p == ResourcePerformance || p == ResourceExtreme
}

type ResourceProfileConfig struct {
	Workers    int `json:"workers"`
	CloudJobs  int `json:"cloud_jobs"`
	RPCWorkers int `json:"rpc_workers"`
}

type V32ResourceMetrics struct {
	DiskFreeBytes           uint64  `json:"disk_free_bytes,omitempty"`
	DiskReserveBytes        uint64  `json:"disk_reserve_bytes,omitempty"`
	RPCQuotaRemaining       uint64  `json:"rpc_quota_remaining,omitempty"`
	RPCHardLimit            uint64  `json:"rpc_hard_limit,omitempty"`
	CloudBudgetRemaining    float64 `json:"cloud_budget_remaining,omitempty"`
	CloudHardLimit          float64 `json:"cloud_hard_limit,omitempty"`
	CloudRowsPerSecond      float64 `json:"cloud_rows_per_second,omitempty"`
	RPCRowsPerSecond        float64 `json:"rpc_rows_per_second,omitempty"`
	ParserRowsPerSecond     float64 `json:"parser_rows_per_second,omitempty"`
	ClickHouseRowsPerSecond float64 `json:"clickhouse_rows_per_second,omitempty"`
	CloudStartupSeconds     float64 `json:"cloud_startup_seconds,omitempty"`
}

// V32ResourceMetricsSource is deliberately narrow so composition can adapt
// disk/RPC/Cloud implementations without importing them here.
type V32ResourceMetricsSource interface {
	SmartDownloadResourceMetrics(ctx context.Context, chainKey string) (V32ResourceMetrics, error)
}

type ETAEstimateV2 struct {
	Seconds           float64  `json:"seconds"`
	LowerBoundSeconds float64  `json:"lower_bound_seconds"`
	UpperBoundSeconds float64  `json:"upper_bound_seconds"`
	Confidence        string   `json:"confidence"`
	Basis             []string `json:"basis"`
}

type PreflightEstimate struct {
	Blocks          uint64                `json:"blocks"`
	Addresses       int                   `json:"addresses"`
	Datasets        int                   `json:"datasets"`
	Rows            uint64                `json:"rows"`
	Bytes           uint64                `json:"bytes"`
	CloudJobs       uint64                `json:"cloud_jobs"`
	RPCCalls        uint64                `json:"rpc_calls"`
	DiskGrowthBytes uint64                `json:"disk_growth_bytes"`
	ETA             ETAEstimateV2         `json:"eta"`
	ResourceProfile ResourceProfile       `json:"resource_profile"`
	Profile         ResourceProfileConfig `json:"profile"`
}

type GuardDecision struct {
	Status          string  `json:"status"`
	EstimatedBytes  uint64  `json:"estimated_bytes,omitempty"`
	AvailableBytes  uint64  `json:"available_bytes,omitempty"`
	ReserveBytes    uint64  `json:"reserve_bytes,omitempty"`
	EstimatedCalls  uint64  `json:"estimated_calls,omitempty"`
	RemainingCalls  uint64  `json:"remaining_calls,omitempty"`
	HardCallLimit   uint64  `json:"hard_limit_calls,omitempty"`
	EstimatedCost   float64 `json:"estimated_cost,omitempty"`
	RemainingBudget float64 `json:"remaining_budget,omitempty"`
	HardBudget      float64 `json:"hard_limit,omitempty"`
	Reason          string  `json:"reason,omitempty"`
}

type PreflightGuards struct {
	Allowed bool          `json:"allowed"`
	Storage GuardDecision `json:"storage"`
	RPC     GuardDecision `json:"rpc"`
	Cloud   GuardDecision `json:"cloud"`
}

func (g PreflightGuards) Reason() string {
	var reasons []string
	for _, d := range []GuardDecision{g.Storage, g.RPC, g.Cloud} {
		if d.Status == "BLOCK" && d.Reason != "" {
			reasons = append(reasons, d.Reason)
		}
	}
	if len(reasons) == 0 {
		return "资源守卫拒绝"
	}
	return strings.Join(reasons, "; ")
}

type PreflightResult struct {
	Estimate   PreflightEstimate `json:"estimate"`
	Guards     PreflightGuards   `json:"guards"`
	Confidence string            `json:"confidence"`
	Basis      []string          `json:"basis"`
}

type PipelineStatus struct {
	DownloadRowsPerSecond   float64 `json:"download_rows_per_second"`
	ParseRowsPerSecond      float64 `json:"parse_rows_per_second"`
	ClickHouseRowsPerSecond float64 `json:"clickhouse_rows_per_second"`
}

type StallStatus struct {
	Detected   bool      `json:"detected"`
	Stage      string    `json:"stage,omitempty"`
	Scope      string    `json:"scope,omitempty"`
	ScopeID    string    `json:"scope_id,omitempty"`
	Since      time.Time `json:"since,omitempty"`
	Seconds    float64   `json:"seconds,omitempty"`
	Recovering bool      `json:"recovering,omitempty"`
}

type RecoveryAction struct {
	At        time.Time `json:"at"`
	BatchID   string    `json:"batch_id"`
	DatasetID string    `json:"dataset_id,omitempty"`
	RangeID   string    `json:"range_id,omitempty"`
	Stage     string    `json:"stage"`
	Action    string    `json:"action"`
	Result    string    `json:"result"`
}

type FailureSummary struct {
	Stage             string  `json:"stage,omitempty"`
	Dataset           string  `json:"dataset,omitempty"`
	DatasetID         string  `json:"dataset_id,omitempty"`
	Range             string  `json:"range,omitempty"`
	Provider          string  `json:"provider,omitempty"`
	ErrorType         string  `json:"error_type,omitempty"`
	CompletedPercent  float64 `json:"completed_percent,omitempty"`
	ResumePoint       string  `json:"resume_point,omitempty"`
	RecommendedAction string  `json:"recommended_action,omitempty"`
}

type HardeningStatus struct {
	BatchID         string                `json:"batch_id"`
	ResourceProfile ResourceProfile       `json:"resource_profile"`
	Profile         ResourceProfileConfig `json:"profile"`
	Bottleneck      string                `json:"bottleneck"`
	Pipeline        PipelineStatus        `json:"pipeline"`
	ETA             ETAEstimateV2         `json:"eta"`
	Stall           StallStatus           `json:"stall"`
	SelfRecovery    []RecoveryAction      `json:"self_recovery,omitempty"`
	Guards          PreflightGuards       `json:"guards"`
	Failure         *FailureSummary       `json:"failure_summary,omitempty"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

type TaskTemplate struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Request     CreateBatchRequest `json:"request"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

type SaveTemplateRequest struct {
	ID          string             `json:"id,omitempty"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Request     CreateBatchRequest `json:"request"`
}

type PerformanceRun struct {
	BatchID          string          `json:"batch_id"`
	Mode             DownloadMode    `json:"mode"`
	ResourceProfile  ResourceProfile `json:"resource_profile"`
	Dataset          string          `json:"dataset"`
	RangeSize        uint64          `json:"range_size"`
	AddressCount     int             `json:"address_count"`
	CloudRowsPerSec  float64         `json:"cloud_rows_per_second"`
	RPCRowsPerSec    float64         `json:"rpc_rows_per_second"`
	DBRowsPerSec     float64         `json:"db_rows_per_second"`
	TotalDurationSec float64         `json:"total_duration_seconds"`
	Rows             uint64          `json:"rows"`
	CreatedAt        time.Time       `json:"created_at"`
}

type JobReport struct {
	BatchID           string          `json:"batch_id"`
	Mode              DownloadMode    `json:"mode"`
	ResourceProfile   ResourceProfile `json:"resource_profile"`
	Providers         []string        `json:"providers"`
	Rows              uint64          `json:"rows"`
	Coverage          float64         `json:"coverage"`
	Duplicates        uint64          `json:"duplicates"`
	TTFASeconds       float64         `json:"ttfa_seconds"`
	TotalTimeSeconds  float64         `json:"total_time_seconds"`
	PeakThroughput    float64         `json:"peak_throughput_rows_per_second"`
	AverageThroughput float64         `json:"average_throughput_rows_per_second"`
	RetryCount        int             `json:"retry_count"`
	GapRepairCount    int             `json:"gap_repair_count"`
	Certification     string          `json:"certification"`
	Status            BatchStatus     `json:"status"`
	GeneratedAt       time.Time       `json:"generated_at"`
}

type CompareRunsRequest struct {
	BatchA string `json:"batch_a"`
	BatchB string `json:"batch_b"`
}

type CompareRunsResult struct {
	RunA  *JobReport         `json:"run_a"`
	RunB  *JobReport         `json:"run_b"`
	Delta map[string]float64 `json:"delta"`
}

type v32Runtime struct {
	mu            sync.Mutex
	source        V32ResourceMetricsSource
	stallTimeout  time.Duration
	checkInterval time.Duration
	diskReserve   uint64
	lastProgress  map[string]rangeProgressMark
	recoveringDB  map[string]bool
}

type rangeProgressMark struct {
	Rows uint64
	At   time.Time
}

func newV32Runtime(opts Options) *v32Runtime {
	return &v32Runtime{stallTimeout: opts.StallTimeout, checkInterval: opts.StallCheckInterval,
		diskReserve: opts.DiskReserveBytes, lastProgress: map[string]rangeProgressMark{}, recoveringDB: map[string]bool{}}
}

func (s *Service) SetV32ResourceMetricsSource(source V32ResourceMetricsSource) {
	s.v32.mu.Lock()
	s.v32.source = source
	s.v32.mu.Unlock()
}

func (s *Service) resourceProfile(profile ResourceProfile) (ResourceProfile, ResourceProfileConfig) {
	if profile == "" {
		profile = ResourceStandard
	}
	maxCloud := maxInt(1, s.opts.CloudBurstMaxJobs)
	maxRPC := maxInt(1, s.opts.RPCHardClaims)
	switch profile {
	case ResourcePerformance:
		return profile, ResourceProfileConfig{Workers: minInt(16, maxInt(8, s.opts.Workers)), CloudJobs: minInt(3, maxCloud), RPCWorkers: minInt(12, maxRPC)}
	case ResourceExtreme:
		return profile, ResourceProfileConfig{Workers: minInt(32, maxInt(16, s.opts.Workers)), CloudJobs: maxCloud, RPCWorkers: maxRPC}
	default:
		return ResourceStandard, ResourceProfileConfig{Workers: minInt(8, maxInt(1, s.opts.Workers)), CloudJobs: minInt(2, maxCloud), RPCWorkers: minInt(8, maxRPC)}
	}
}

func (s *Service) profileConfigForBatch(batchID string) ResourceProfileConfig {
	b := s.store.GetBatch(batchID)
	if b == nil {
		_, c := s.resourceProfile(ResourceStandard)
		return c
	}
	_, c := s.resourceProfile(b.ResourceProfile)
	return c
}

func normalizePreflightRequest(req CreateBatchRequest) (CreateBatchRequest, []string, []string, error) {
	if _, err := chain.Resolve(req.ChainKey); err != nil {
		return req, nil, nil, err
	}
	if req.DefaultRange != nil {
		if err := validatePreflightRange(*req.DefaultRange, "default_range"); err != nil {
			return req, nil, nil, err
		}
	}
	for address, spec := range req.AddressOverrides {
		if err := validatePreflightRange(spec, "address_overrides["+address+"]"); err != nil {
			return req, nil, nil, err
		}
	}
	for i, spec := range req.RelevantRanges {
		if err := validatePreflightRange(spec, fmt.Sprintf("relevant_ranges[%d]", i)); err != nil {
			return req, nil, nil, err
		}
	}
	if req.RelevantRange != nil {
		if err := validatePreflightRange(*req.RelevantRange, "relevant_range"); err != nil {
			return req, nil, nil, err
		}
	}
	for address, specs := range req.RelevantByAddress {
		for i, spec := range specs {
			if err := validatePreflightRange(spec, fmt.Sprintf("relevant_ranges_by_address[%s][%d]", address, i)); err != nil {
				return req, nil, nil, err
			}
		}
	}
	seen := map[string]bool{}
	addresses := make([]string, 0, len(req.Addresses))
	for _, raw := range req.Addresses {
		a := strings.ToLower(strings.TrimSpace(raw))
		if evmAddressRE.MatchString(a) && !seen[a] {
			seen[a] = true
			addresses = append(addresses, a)
		}
	}
	if len(addresses) == 0 {
		return req, nil, nil, fmt.Errorf("没有有效地址")
	}
	datasetSeen := map[string]bool{}
	datasets := make([]string, 0, len(req.Datasets))
	for _, d := range req.Datasets {
		if ValidDataset(d) && !datasetSeen[d] {
			datasetSeen[d] = true
			datasets = append(datasets, d)
		}
	}
	if len(datasets) == 0 {
		return req, nil, nil, fmt.Errorf("没有合法的数据集")
	}
	return req, addresses, datasets, nil
}

func validatePreflightRange(spec RangeSpec, field string) error {
	mode := spec.Mode
	if mode == "" {
		mode = RangeModeFull
	}
	switch mode {
	case RangeModeFull:
		return nil
	case RangeModeBlock:
		if spec.ToBlock < spec.FromBlock {
			return fmt.Errorf("%s 的 to_block 不能小于 from_block", field)
		}
		return nil
	case RangeModeTime:
		if strings.TrimSpace(spec.StartTime) == "" || strings.TrimSpace(spec.EndTime) == "" {
			return fmt.Errorf("%s 的 TIME 模式必须提供 start_time 和 end_time", field)
		}
		return nil
	default:
		return fmt.Errorf("%s 使用非法范围模式 %q（仅支持 FULL/TIME/BLOCK）", field, spec.Mode)
	}
}

func (s *Service) Preflight(ctx context.Context, req CreateBatchRequest) (*PreflightResult, error) {
	_, addresses, datasets, err := normalizePreflightRequest(req)
	if err != nil {
		return nil, err
	}
	profile, cfg := s.resourceProfile(req.ResourceProfile)
	if req.ResourceProfile != "" && !req.ResourceProfile.Valid() {
		return nil, fmt.Errorf("非法资源档位 %q", req.ResourceProfile)
	}
	history, _ := s.loadPerformanceHistory()
	rowsPerBlock := historicalRowsPerBlock(history)
	var blocks, rows uint64
	for _, address := range addresses {
		spec := RangeSpec{Mode: RangeModeFull}
		if req.DefaultRange != nil {
			spec = *req.DefaultRange
		}
		if override, ok := req.AddressOverrides[address]; ok {
			spec = override
		}
		for _, dataset := range datasets {
			r := s.requestedBlocks(spec, dataset)
			span := uint64(1)
			if r.To >= r.From {
				span = r.To - r.From + 1
			}
			blocks += span
			density := rowsPerBlock[dataset]
			if density <= 0 {
				density = defaultRowsPerBlock(dataset)
			}
			estimated := uint64(math.Ceil(float64(span) * density))
			if dataset == DatasetBalances || dataset == DatasetTokenMetadata {
				estimated = 1
			}
			rows += estimated
		}
	}
	bytes := rows * 144
	diskGrowth := uint64(float64(bytes) * 1.35)
	shards := (blocks + s.opts.RangeChunkSize - 1) / s.opts.RangeChunkSize
	if shards == 0 {
		shards = 1
	}
	cloudJobs, rpcCalls := estimateProviderWork(req.Mode, profile, shards, blocks, cfg)
	metrics, metricsErr := s.resourceMetrics(ctx, req.ChainKey)
	eta := estimateETAV2(rows, blocks, profile, metrics, history)
	est := PreflightEstimate{Blocks: blocks, Addresses: len(addresses), Datasets: len(addresses) * len(datasets),
		Rows: rows, Bytes: bytes, CloudJobs: cloudJobs, RPCCalls: rpcCalls, DiskGrowthBytes: diskGrowth,
		ETA: eta, ResourceProfile: profile, Profile: cfg}
	guards := s.evaluateGuards(est, metrics, metricsErr)
	confidence := eta.Confidence
	basis := []string{"requested block ranges", "dataset density defaults"}
	if len(history) > 0 {
		basis = append(basis, "persisted performance history")
	}
	if metricsErr == nil && s.v32Source() != nil {
		basis = append(basis, "live resource guard metrics")
	}
	return &PreflightResult{Estimate: est, Guards: guards, Confidence: confidence, Basis: basis}, nil
}

func (s *Service) v32Source() V32ResourceMetricsSource {
	s.v32.mu.Lock()
	defer s.v32.mu.Unlock()
	return s.v32.source
}

func (s *Service) resourceMetrics(ctx context.Context, chainKey string) (V32ResourceMetrics, error) {
	source := s.v32Source()
	if source == nil {
		return V32ResourceMetrics{}, errors.New("resource metrics source unavailable")
	}
	return source.SmartDownloadResourceMetrics(ctx, chainKey)
}

func estimateProviderWork(mode DownloadMode, profile ResourceProfile, shards, blocks uint64, cfg ResourceProfileConfig) (uint64, uint64) {
	if mode == "" {
		mode = DownloadModeAuto
	}
	cloudShare := 0.0
	if mode == DownloadModeTurbo {
		cloudShare = .65
	}
	if mode == DownloadModeEmergency || profile == ResourceExtreme {
		cloudShare = .8
	}
	cloud := uint64(math.Ceil(float64(shards) * cloudShare))
	if cloud > uint64(cfg.CloudJobs) && cfg.CloudJobs > 0 {
		cloud = uint64(cfg.CloudJobs)
	}
	rpcBlocks := uint64(float64(blocks) * (1 - cloudShare))
	rpcCalls := (rpcBlocks + 1_999) / 2_000
	if rpcCalls == 0 && cloud == 0 {
		rpcCalls = 1
	}
	return cloud, rpcCalls
}

func defaultRowsPerBlock(dataset string) float64 {
	switch dataset {
	case DatasetTransactions:
		return .20
	case DatasetTokenTransfers:
		return .12
	case DatasetLogs:
		return .40
	case DatasetInternalTransactions:
		return .04
	case DatasetNFTTransfers:
		return .03
	default:
		return .01
	}
}

func historicalRowsPerBlock(history []PerformanceRun) map[string]float64 {
	sum, count := map[string]float64{}, map[string]int{}
	for _, h := range history {
		if h.RangeSize == 0 || h.Rows == 0 {
			continue
		}
		sum[h.Dataset] += float64(h.Rows) / float64(h.RangeSize)
		count[h.Dataset]++
	}
	out := map[string]float64{}
	for k, v := range sum {
		out[k] = v / float64(count[k])
	}
	return out
}

func estimateETAV2(rows, blocks uint64, profile ResourceProfile, metrics V32ResourceMetrics, history []PerformanceRun) ETAEstimateV2 {
	rate := map[ResourceProfile]float64{ResourceStandard: 25_000, ResourcePerformance: 75_000, ResourceExtreme: 150_000}[profile]
	basis := []string{"estimated rows", "resource profile baseline"}
	var observed []float64
	for _, v := range []float64{metrics.CloudRowsPerSecond + metrics.RPCRowsPerSecond, metrics.ParserRowsPerSecond, metrics.ClickHouseRowsPerSecond} {
		if v > 0 {
			observed = append(observed, v)
		}
	}
	if len(observed) > 0 {
		rate = observed[0]
		for _, v := range observed[1:] {
			if v < rate {
				rate = v
			}
		}
		basis = append(basis, "live Cloud/RPC/Parser/ClickHouse throughput")
	} else {
		var total float64
		var n int
		for _, h := range history {
			if h.TotalDurationSec > 0 && h.Rows > 0 {
				total += float64(h.Rows) / h.TotalDurationSec
				n++
			}
		}
		if n > 0 {
			rate = total / float64(n)
			basis = append(basis, "historical completed runs")
		}
	}
	if rate < 1 {
		rate = math.Max(1, float64(blocks)/600)
	}
	startup := metrics.CloudStartupSeconds
	if startup <= 0 {
		startup = 15
	}
	seconds := float64(rows)/rate + startup
	confidence := "LOW"
	spread := .55
	if len(observed) >= 2 {
		confidence, spread = "HIGH", .20
	} else if len(history) > 0 || len(observed) == 1 {
		confidence, spread = "MEDIUM", .35
	}
	return ETAEstimateV2{Seconds: seconds, LowerBoundSeconds: math.Max(1, seconds*(1-spread)),
		UpperBoundSeconds: seconds * (1 + spread), Confidence: confidence, Basis: basis}
}

func (s *Service) evaluateGuards(est PreflightEstimate, m V32ResourceMetrics, metricsErr error) PreflightGuards {
	reserve := m.DiskReserveBytes
	if reserve == 0 {
		reserve = s.v32.diskReserve
	}
	storage := GuardDecision{Status: "UNKNOWN", EstimatedBytes: est.DiskGrowthBytes, AvailableBytes: m.DiskFreeBytes, ReserveBytes: reserve}
	rpc := GuardDecision{Status: "UNKNOWN", EstimatedCalls: est.RPCCalls, RemainingCalls: m.RPCQuotaRemaining, HardCallLimit: m.RPCHardLimit}
	cost := float64(est.CloudJobs)*.01 + float64(est.Bytes)/(1<<30)*.02
	cloud := GuardDecision{Status: "UNKNOWN", EstimatedCost: cost, RemainingBudget: m.CloudBudgetRemaining, HardBudget: m.CloudHardLimit}
	if metricsErr != nil {
		storage.Reason, rpc.Reason, cloud.Reason = "live metric unavailable", "live metric unavailable", "live metric unavailable"
	} else {
		storage.Status = "PASS"
		if m.DiskFreeBytes > 0 && (est.DiskGrowthBytes > m.DiskFreeBytes || m.DiskFreeBytes-est.DiskGrowthBytes < reserve) {
			storage.Status, storage.Reason = "BLOCK", "预计磁盘增长将突破安全保留空间"
		}
		rpc.Status = "PASS"
		if m.RPCHardLimit > 0 && est.RPCCalls > m.RPCHardLimit {
			rpc.Status, rpc.Reason = "BLOCK", "预计 RPC 调用超过任务 Hard Limit"
		} else if m.RPCQuotaRemaining > 0 && est.RPCCalls > m.RPCQuotaRemaining {
			rpc.Status, rpc.Reason = "BLOCK", "预计 RPC 调用超过剩余额度"
		}
		cloud.Status = "PASS"
		if m.CloudHardLimit > 0 && cost > m.CloudHardLimit {
			cloud.Status, cloud.Reason = "BLOCK", "预计 Cloud 消耗超过 Hard Limit"
		} else if m.CloudBudgetRemaining > 0 && cost > m.CloudBudgetRemaining {
			cloud.Status, cloud.Reason = "BLOCK", "预计 Cloud 消耗超过剩余预算"
		}
	}
	allowed := storage.Status != "BLOCK" && rpc.Status != "BLOCK" && cloud.Status != "BLOCK"
	return PreflightGuards{Allowed: allowed, Storage: storage, RPC: rpc, Cloud: cloud}
}

func (s *Service) PreflightBatch(ctx context.Context, batchID string) (*PreflightResult, error) {
	b := s.store.GetBatch(batchID)
	if b == nil {
		return nil, fmt.Errorf("批次不存在: %s", batchID)
	}
	est := b.Preflight
	if est == nil {
		profile, cfg := s.resourceProfile(b.ResourceProfile)
		fallback := PreflightEstimate{Addresses: b.AddressCount, Datasets: b.AddressCount * len(b.DatasetTypes), ResourceProfile: profile, Profile: cfg}
		for _, a := range s.store.ListAddressesByBatch(batchID) {
			for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
				fallback.Rows += ds.EstimatedRows
				fallback.Bytes += ds.EstimatedBytes
				for _, r := range s.store.ListRangesByDataset(ds.ID) {
					fallback.Blocks += rangeBlockCount(r)
				}
			}
		}
		if fallback.Bytes == 0 {
			fallback.Bytes = fallback.Rows * 144
		}
		fallback.DiskGrowthBytes = uint64(float64(fallback.Bytes) * 1.35)
		shards := uint64(1)
		if s.opts.RangeChunkSize > 0 {
			shards = (fallback.Blocks + s.opts.RangeChunkSize - 1) / s.opts.RangeChunkSize
		}
		fallback.CloudJobs, fallback.RPCCalls = estimateProviderWork(b.Mode, profile, shards, fallback.Blocks, cfg)
		history, _ := s.loadPerformanceHistory()
		fallback.ETA = estimateETAV2(fallback.Rows, fallback.Blocks, profile, V32ResourceMetrics{}, history)
		est = &fallback
	}
	m, err := s.resourceMetrics(ctx, b.ChainKey)
	guards := s.evaluateGuards(*est, m, err)
	return &PreflightResult{Estimate: *est, Guards: guards, Confidence: est.ETA.Confidence, Basis: est.ETA.Basis}, nil
}

func (s *Service) startV32Monitor() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.v32.checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case now := <-ticker.C:
				for _, b := range s.store.ListBatches() {
					if !b.Status.Terminal() && b.Status != BatchPaused && b.Status != BatchPausedByPriority {
						_, _ = s.DetectAndRecoverStalls(b.ID, now)
					}
				}
				_ = s.ReconcileAll(context.Background())
			}
		}
	}()
}

func (s *Service) DetectAndRecoverStalls(batchID string, now time.Time) ([]RecoveryAction, error) {
	b := s.store.GetBatch(batchID)
	if b == nil {
		return nil, fmt.Errorf("批次不存在: %s", batchID)
	}
	var actions []RecoveryAction
	var retryDB []string
	restartWorkers := map[string]bool{}
	s.mu.Lock()
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			if (ds.Status == DatasetIndexing || ds.Status == DatasetDBWriteFailed) && now.Sub(ds.UpdatedAt) >= s.v32.stallTimeout && s.markDBRecovery(ds.ID) {
				action := RecoveryAction{At: now, BatchID: batchID, DatasetID: ds.ID, Stage: "CLICKHOUSE", Action: "RETRY_DB_STAGE", Result: "QUEUED"}
				actions = append(actions, action)
				retryDB = append(retryDB, ds.ID)
				_ = s.appendRecoveryAction(action)
				_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{Event: LedgerSelfRecovery, DatasetJobID: ds.ID, Error: "RETRY_DB_STAGE"})
			}
			hasRunning, hasPending := false, false
			for _, r := range s.store.ListRangesByDataset(ds.ID) {
				hasRunning = hasRunning || r.Status == RangeRunning
				hasPending = hasPending || r.Status == RangePending || r.Status == RangeReady
				if r.Status != RangeRunning || now.Sub(r.UpdatedAt) < s.v32.stallTimeout {
					continue
				}
				stage, actionName := "NETWORK", "RETRY_RANGE"
				if r.Owner == RangeOwnerCloud || r.Provider == "sqd_cloud" {
					stage, actionName = "CLOUD", "RESTART_SHARD"
					if rpc := s.adapters["rpc"]; adapterAvailableForMode(rpc, ds.ChainKey, b.Mode) && rpc.Supports(ds.Dataset) {
						actionName = "RESTART_SHARD_SWITCH_RPC"
						r.Owner, r.Lane = RangeOwnerRPC, "recovery"
					}
				} else if r.Owner == RangeOwnerRPC || r.Provider == "rpc" {
					stage, actionName = "RPC", "SWITCH_ENDPOINT_RETRY_RANGE"
				}
				r.Status, r.Provider, r.StartedAt, r.FinishedAt = RangeReady, "", nil, nil
				r.UpdatedAt = now
				_ = s.store.SaveRange(r)
				action := RecoveryAction{At: now, BatchID: batchID, DatasetID: ds.ID, RangeID: r.ID, Stage: stage, Action: actionName, Result: "REQUEUED"}
				actions = append(actions, action)
				_ = s.appendRecoveryAction(action)
				_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{Event: LedgerSelfRecovery, DatasetJobID: ds.ID,
					RangeID: r.ID, FromBlock: r.FromBlock, ToBlock: r.ToBlock, Owner: string(r.Owner), Error: actionName})
			}
			if ds.Status == DatasetRunning && !hasRunning && hasPending && now.Sub(ds.UpdatedAt) >= s.v32.stallTimeout {
				ds.UpdatedAt = now
				_ = s.store.SaveDataset(ds)
				action := RecoveryAction{At: now, BatchID: batchID, DatasetID: ds.ID, Stage: "PARSER", Action: "RESTART_WORKER", Result: "QUEUED"}
				actions = append(actions, action)
				restartWorkers[batchID] = true
				_ = s.appendRecoveryAction(action)
				_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{Event: LedgerSelfRecovery, DatasetJobID: ds.ID, Error: "RESTART_WORKER"})
			}
		}
	}
	s.mu.Unlock()
	for _, dsID := range retryDB {
		s.indexDataset(dsID)
		s.clearDBRecovery(dsID)
	}
	for id := range restartWorkers {
		s.wg.Add(1)
		go func(batchID string) { defer s.wg.Done(); s.runBatchWorker(batchID) }(id)
	}
	return actions, nil
}

func (s *Service) markDBRecovery(datasetID string) bool {
	s.v32.mu.Lock()
	defer s.v32.mu.Unlock()
	if s.v32.recoveringDB[datasetID] {
		return false
	}
	s.v32.recoveringDB[datasetID] = true
	return true
}

func (s *Service) clearDBRecovery(datasetID string) {
	s.v32.mu.Lock()
	delete(s.v32.recoveringDB, datasetID)
	s.v32.mu.Unlock()
}

func (s *Service) HardeningStatus(batchID string) (*HardeningStatus, error) {
	b := s.store.GetBatch(batchID)
	if b == nil {
		return nil, fmt.Errorf("批次不存在: %s", batchID)
	}
	profile, cfg := s.resourceProfile(b.ResourceProfile)
	turbo, _ := s.TurboStatus(batchID)
	status := &HardeningStatus{BatchID: batchID, ResourceProfile: profile, Profile: cfg, Bottleneck: "SOURCE", UpdatedAt: time.Now().UTC()}
	if turbo != nil {
		status.Pipeline = PipelineStatus{DownloadRowsPerSecond: turbo.DownloadedRowsPerSecond,
			ParseRowsPerSecond: turbo.ParsedRowsPerSecond, ClickHouseRowsPerSecond: turbo.InsertedRowsPerSecond}
		status.Bottleneck = normalizeBottleneck(turbo.Bottleneck, status.Pipeline, turbo)
	}
	if pf, err := s.PreflightBatch(context.Background(), batchID); err == nil {
		status.Guards = pf.Guards
		status.ETA = liveETAV2(b, pf.Estimate, status.Pipeline)
	} else if b.Preflight != nil {
		status.ETA = b.Preflight.ETA
	}
	actions, _ := s.loadRecoveryActions(batchID)
	if len(actions) > 20 {
		actions = actions[len(actions)-20:]
	}
	status.SelfRecovery = actions
	status.Stall = s.detectStall(batchID, status.UpdatedAt)
	status.Failure = s.failureSummary(batchID)
	return status, nil
}

func normalizeBottleneck(current string, p PipelineStatus, t *TurboStatus) string {
	valid := map[string]bool{"SOURCE": true, "NETWORK": true, "RPC": true, "CLOUD": true, "PARSER": true, "CLICKHOUSE": true, "DISK": true, "VALIDATION": true}
	if valid[current] {
		return current
	}
	if t != nil && t.RPCRunning > 0 && t.RPCAvailable == false {
		return "RPC"
	}
	if p.ClickHouseRowsPerSecond > 0 && p.DownloadRowsPerSecond > p.ClickHouseRowsPerSecond*1.1 {
		return "CLICKHOUSE"
	}
	if p.ParseRowsPerSecond > 0 && p.DownloadRowsPerSecond > p.ParseRowsPerSecond*1.1 {
		return "PARSER"
	}
	if t != nil && t.CloudRunning > 0 && !t.CloudAvailable {
		return "CLOUD"
	}
	return "SOURCE"
}

func liveETAV2(b *BatchJob, est PreflightEstimate, p PipelineStatus) ETAEstimateV2 {
	eta := est.ETA
	rate := p.DownloadRowsPerSecond
	for _, v := range []float64{p.ParseRowsPerSecond, p.ClickHouseRowsPerSecond} {
		if v > 0 && (rate <= 0 || v < rate) {
			rate = v
		}
	}
	if rate > 0 {
		elapsedRows := uint64(0)
		_ = elapsedRows
		eta.Seconds = float64(est.Rows) / rate
		eta.LowerBoundSeconds, eta.UpperBoundSeconds = eta.Seconds*.8, eta.Seconds*1.25
		eta.Confidence = "HIGH"
		eta.Basis = append(append([]string{}, eta.Basis...), "live slowest pipeline throughput")
	}
	_ = b
	return eta
}

func (s *Service) detectStall(batchID string, now time.Time) StallStatus {
	oldest := StallStatus{}
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			if now.Sub(ds.UpdatedAt) < s.v32.stallTimeout {
				continue
			}
			stage := ""
			scope := "DATASET"
			if ds.Status == DatasetIndexing || ds.Status == DatasetDBWriteFailed {
				stage = "CLICKHOUSE"
			}
			if ds.Status == DatasetValidating {
				stage = "VALIDATION"
			}
			if stage != "" {
				candidate := StallStatus{Detected: true, Stage: stage, Scope: scope, ScopeID: ds.ID, Since: ds.UpdatedAt, Seconds: now.Sub(ds.UpdatedAt).Seconds(), Recovering: true}
				if !oldest.Detected || candidate.Since.Before(oldest.Since) {
					oldest = candidate
				}
			}
		}
	}
	for _, r := range s.store.ListRanges() {
		if r.BatchID != batchID || r.Status != RangeRunning || now.Sub(r.UpdatedAt) < s.v32.stallTimeout {
			continue
		}
		stage := "NETWORK"
		if r.Owner == RangeOwnerRPC {
			stage = "RPC"
		}
		if r.Owner == RangeOwnerCloud {
			stage = "CLOUD"
		}
		candidate := StallStatus{Detected: true, Stage: stage, Scope: "RANGE", ScopeID: r.ID, Since: r.UpdatedAt,
			Seconds: now.Sub(r.UpdatedAt).Seconds(), Recovering: true}
		if !oldest.Detected || candidate.Since.Before(oldest.Since) {
			oldest = candidate
		}
	}
	return oldest
}

func (s *Service) failureSummary(batchID string) *FailureSummary {
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			for _, r := range s.store.ListRangesByDataset(ds.ID) {
				if r.Status == RangeFailed {
					return &FailureSummary{Stage: stageForRange(r), Dataset: ds.Dataset, DatasetID: ds.ID,
						Range: fmt.Sprintf("%d-%d", r.FromBlock, r.ToBlock), Provider: r.Provider,
						ErrorType: classifyFailure(r.Error), CompletedPercent: ds.Progress.Percent * 100,
						ResumePoint: fmt.Sprintf("range:%s", r.ID), RecommendedAction: "自动切换 Provider 并重试该 Range"}
				}
			}
			if ds.Error != "" {
				return &FailureSummary{Stage: stageForDataset(ds), Dataset: ds.Dataset, DatasetID: ds.ID,
					ErrorType: classifyFailure(ds.Error), CompletedPercent: ds.Progress.Percent * 100,
					ResumePoint: "dataset:" + ds.ID, RecommendedAction: "从当前 Dataset checkpoint 恢复"}
			}
		}
	}
	return nil
}

func stageForRange(r *RangeJob) string {
	if r.Owner == RangeOwnerRPC {
		return "RPC"
	}
	if r.Owner == RangeOwnerCloud {
		return "CLOUD"
	}
	return "NETWORK"
}
func stageForDataset(ds *DatasetJob) string {
	if ds.Status == DatasetDBWriteFailed || strings.Contains(ds.Error, "DB_WRITE") {
		return "CLICKHOUSE"
	}
	if ds.Status == DatasetValidating {
		return "VALIDATION"
	}
	return "PARSER"
}
func classifyFailure(message string) string {
	m := strings.ToUpper(message)
	if strings.Contains(m, "429") {
		return "RPC_RATE_LIMIT"
	}
	if strings.Contains(m, "TIMEOUT") {
		return "TIMEOUT"
	}
	if strings.Contains(m, "DB_WRITE") {
		return "DB_WRITE_FAILED"
	}
	return "PROVIDER_ERROR"
}

func (s *Service) appendRecoveryAction(action RecoveryAction) error {
	s.v32.mu.Lock()
	defer s.v32.mu.Unlock()
	return appendNDJSON(filepath.Join(s.v32Root(), "recovery", action.BatchID+".ndjson"), action)
}

func (s *Service) loadRecoveryActions(batchID string) ([]RecoveryAction, error) {
	if !safeID(batchID) {
		return nil, fmt.Errorf("非法 batch id")
	}
	var out []RecoveryAction
	err := readNDJSON(filepath.Join(s.v32Root(), "recovery", batchID+".ndjson"), func(line []byte) error {
		var a RecoveryAction
		if err := json.Unmarshal(line, &a); err != nil {
			return err
		}
		out = append(out, a)
		return nil
	})
	return out, err
}

func (s *Service) ReconcileAll(ctx context.Context) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ds := range s.store.ListDatasets() {
		if ds.Status == DatasetIndexing || ds.Status == DatasetDBWriteFailed || ds.Status == DatasetCanceled {
			continue
		}
		ranges := s.store.ListRangesByDataset(ds.ID)
		if len(ranges) == 0 {
			continue
		}
		allTerminal, failures := true, 0
		for _, r := range ranges {
			if !r.Status.Terminal() {
				allTerminal = false
			}
			if r.Status == RangeFailed {
				failures++
			}
		}
		if allTerminal {
			next := ds.Status
			if failures > 0 {
				if failures == len(ranges) {
					next = DatasetFailed
				} else {
					next = DatasetPartial
				}
			} else if ds.Validation != nil && strings.EqualFold(ds.Validation.Status, "PASS") {
				next = DatasetCompleted
			} else if !ds.Status.Terminal() {
				next = DatasetValidating
			}
			if next != ds.Status {
				ds.Status, ds.UpdatedAt = next, time.Now().UTC()
				if next.Terminal() {
					now := ds.UpdatedAt
					ds.FinishedAt = &now
				}
				_ = s.store.SaveDataset(ds)
			}
		}
	}
	for _, a := range s.store.ListAddresses() {
		s.reconcileAddressLocked(a.ID)
	}
	for _, b := range s.store.ListBatches() {
		s.reconcileBatchLocked(b.ID)
	}
	return nil
}

func (s *Service) reconcileAddressLocked(id string) {
	a := s.store.GetAddress(id)
	if a == nil || a.CancelRequested {
		return
	}
	ds := s.store.ListDatasetsByAddress(id)
	if len(ds) == 0 {
		return
	}
	all, failed, partial := true, false, false
	for _, d := range ds {
		all = all && d.Status.Terminal()
		failed = failed || d.Status == DatasetFailed
		partial = partial || d.Status == DatasetPartial
	}
	previousStatus := a.Status
	previousFinishedNil := a.FinishedAt == nil
	if !all {
		if a.Status.Terminal() {
			a.Status = AddressDownloading
			a.FinishedAt = nil
		}
	} else if failed {
		a.Status = AddressFailed
	} else if partial {
		a.Status = AddressPartial
	} else {
		a.Status = AddressCompleted
	}
	changed := a.Status != previousStatus || (a.FinishedAt == nil) != previousFinishedNil
	if !changed && (!a.Status.Terminal() || a.FinishedAt != nil) {
		return
	}
	a.UpdatedAt = time.Now().UTC()
	if a.Status.Terminal() && a.FinishedAt == nil {
		n := a.UpdatedAt
		a.FinishedAt = &n
	}
	_ = s.store.SaveAddress(a)
}

func (s *Service) reconcileBatchLocked(id string) {
	b := s.store.GetBatch(id)
	if b == nil || b.CancelRequested {
		return
	}
	addresses := s.store.ListAddressesByBatch(id)
	if len(addresses) == 0 {
		return
	}
	all, failed, partial := true, false, false
	for _, a := range addresses {
		all = all && a.Status.Terminal()
		failed = failed || a.Status == AddressFailed
		partial = partial || a.Status == AddressPartial
	}
	previousStatus := b.Status
	previousFinishedNil := b.FinishedAt == nil
	if !all {
		if b.Status.Terminal() {
			b.Status = BatchRunning
			b.FinishedAt = nil
		}
	} else if failed || partial {
		b.Status = BatchPartial
	} else {
		b.Status = BatchCompleted
	}
	changed := b.Status != previousStatus || (b.FinishedAt == nil) != previousFinishedNil
	if !changed && (!b.Status.Terminal() || b.FinishedAt != nil) {
		return
	}
	b.UpdatedAt = time.Now().UTC()
	if b.Status.Terminal() && b.FinishedAt == nil {
		n := b.UpdatedAt
		b.FinishedAt = &n
	}
	_ = s.store.SaveBatch(b)
	if b.Status == BatchCompleted || b.Status == BatchPartial || b.Status == BatchFailed {
		_, _ = s.ensureJobReportLocked(id)
	}
}

func (s *Service) v32Root() string { return filepath.Join(s.store.Root(), "v32") }

func (s *Service) SaveTemplate(input SaveTemplateRequest) (*TaskTemplate, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 120 {
		return nil, fmt.Errorf("模板名称不能为空且最多 120 字符")
	}
	if _, _, _, err := normalizePreflightRequest(input.Request); err != nil {
		return nil, err
	}
	id := input.ID
	now := time.Now().UTC()
	created := now
	if id == "" {
		id = uuid.NewString()
	} else if !safeID(id) {
		return nil, fmt.Errorf("非法模板 id")
	} else if old, _ := s.GetTemplate(id); old != nil {
		created = old.CreatedAt
	}
	t := &TaskTemplate{ID: id, Name: name, Description: strings.TrimSpace(input.Description), Request: input.Request, CreatedAt: created, UpdatedAt: now}
	if err := atomicWriteJSON(filepath.Join(s.v32Root(), "templates", id+".json"), t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Service) GetTemplate(id string) (*TaskTemplate, error) {
	if !safeID(id) {
		return nil, fmt.Errorf("非法模板 id")
	}
	var t TaskTemplate
	if err := readJSON(filepath.Join(s.v32Root(), "templates", id+".json"), &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Service) ListTemplates() ([]*TaskTemplate, error) {
	dir := filepath.Join(s.v32Root(), "templates")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []*TaskTemplate{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*TaskTemplate
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var t TaskTemplate
		if readJSON(filepath.Join(dir, e.Name()), &t) == nil {
			out = append(out, &t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (s *Service) DeleteTemplate(id string) error {
	if !safeID(id) {
		return fmt.Errorf("非法模板 id")
	}
	err := os.Remove(filepath.Join(s.v32Root(), "templates", id+".json"))
	if os.IsNotExist(err) {
		return fmt.Errorf("模板不存在: %s", id)
	}
	return err
}

func (s *Service) InstantiateTemplate(ctx context.Context, id string) (*CreateBatchResponse, error) {
	t, err := s.GetTemplate(id)
	if err != nil {
		return nil, err
	}
	return s.CreateBatch(ctx, t.Request)
}

func (s *Service) ensureJobReportLocked(batchID string) (*JobReport, error) {
	b := s.store.GetBatch(batchID)
	if b == nil {
		return nil, fmt.Errorf("批次不存在: %s", batchID)
	}
	if existing, err := s.readJobReport(batchID); err == nil && existing.Status == b.Status && b.Status.Terminal() {
		return existing, nil
	}
	r := &JobReport{BatchID: batchID, Mode: b.Mode, ResourceProfile: b.ResourceProfile, Status: b.Status, GeneratedAt: time.Now().UTC(), Certification: string(CertificationBatch)}
	providers := map[string]bool{}
	var first *time.Time
	var duration float64
	var blocks, covered uint64
	if b.StartedAt != nil && b.FinishedAt != nil {
		duration = b.FinishedAt.Sub(*b.StartedAt).Seconds()
		r.TotalTimeSeconds = duration
	}
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			r.Rows += ds.DownloadedRows
			r.GapRepairCount += ds.RepairRounds
			if ds.Validation != nil && ds.Validation.DuplicateCount > 0 {
				r.Duplicates += uint64(ds.Validation.DuplicateCount)
			}
			for _, rg := range s.store.ListRangesByDataset(ds.ID) {
				if rg.Provider != "" {
					providers[rg.Provider] = true
				}
				blocks += rangeBlockCount(rg)
				if rg.Status == RangeCompleted || rg.Status == RangeEmpty {
					covered += rangeBlockCount(rg)
				}
				if rg.Attempts > 1 {
					r.RetryCount += rg.Attempts - 1
				}
				if rg.RowsCommitted > 0 && rg.StartedAt != nil && rg.FinishedAt != nil && rg.FinishedAt.After(*rg.StartedAt) {
					rate := float64(rg.RowsCommitted) / rg.FinishedAt.Sub(*rg.StartedAt).Seconds()
					if rate > r.PeakThroughput {
						r.PeakThroughput = rate
					}
					if first == nil || rg.FinishedAt.Before(*first) {
						v := *rg.FinishedAt
						first = &v
					}
				}
			}
		}
	}
	if blocks > 0 {
		r.Coverage = float64(covered) / float64(blocks) * 100
	}
	if duration > 0 {
		r.AverageThroughput = float64(r.Rows) / duration
	}
	if first != nil && b.StartedAt != nil {
		r.TTFASeconds = first.Sub(*b.StartedAt).Seconds()
	}
	for p := range providers {
		r.Providers = append(r.Providers, p)
	}
	sort.Strings(r.Providers)
	if r.Coverage < 100 || b.Status != BatchCompleted {
		r.Certification = string(CertificationDatasetPartial)
	}
	if err := atomicWriteJSON(filepath.Join(s.v32Root(), "reports", batchID+".json"), r); err != nil {
		return nil, err
	}
	_ = s.appendPerformanceRunLocked(r)
	return r, nil
}

func (s *Service) GetJobReport(batchID string) (*JobReport, error) {
	if !safeID(batchID) {
		return nil, fmt.Errorf("非法 batch id")
	}
	b := s.store.GetBatch(batchID)
	if b == nil {
		return nil, fmt.Errorf("批次不存在: %s", batchID)
	}
	if !b.Status.Terminal() {
		return nil, fmt.Errorf("批次尚未完成，报告将在终态自动生成")
	}
	if r, err := s.readJobReport(batchID); err == nil && r.Status == b.Status {
		return r, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureJobReportLocked(batchID)
}
func (s *Service) readJobReport(id string) (*JobReport, error) {
	var r JobReport
	if err := readJSON(filepath.Join(s.v32Root(), "reports", id+".json"), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Service) appendPerformanceRunLocked(report *JobReport) error {
	history, _ := s.loadPerformanceHistory()
	b := s.store.GetBatch(report.BatchID)
	if b == nil {
		return nil
	}
	run := PerformanceRun{BatchID: report.BatchID, Mode: report.Mode, ResourceProfile: report.ResourceProfile, AddressCount: b.AddressCount, TotalDurationSec: report.TotalTimeSeconds, Rows: report.Rows, CreatedAt: report.GeneratedAt}
	if len(b.DatasetTypes) == 1 {
		run.Dataset = b.DatasetTypes[0]
	}
	if b.Preflight != nil {
		run.RangeSize = b.Preflight.Blocks
	}
	if state := s.v31StateLocked(report.BatchID); state != nil {
		run.CloudRowsPerSec = state.Pipeline.DownloadedRowsPerSecond
		run.DBRowsPerSec = state.Pipeline.InsertedRowsPerSecond
	}
	for _, h := range history {
		if h.BatchID == run.BatchID {
			return nil
		}
	}
	history = append(history, run)
	if len(history) > 5000 {
		history = history[len(history)-5000:]
	}
	return atomicWriteJSON(filepath.Join(s.v32Root(), "performance_history.json"), history)
}

func (s *Service) loadPerformanceHistory() ([]PerformanceRun, error) {
	var h []PerformanceRun
	err := readJSON(filepath.Join(s.v32Root(), "performance_history.json"), &h)
	if os.IsNotExist(err) {
		return []PerformanceRun{}, nil
	}
	return h, err
}
func (s *Service) PerformanceHistory() ([]PerformanceRun, error) {
	h, err := s.loadPerformanceHistory()
	if err != nil {
		return nil, err
	}
	sort.Slice(h, func(i, j int) bool { return h[i].CreatedAt.After(h[j].CreatedAt) })
	return h, nil
}

func (s *Service) CompareRuns(req CompareRunsRequest) (*CompareRunsResult, error) {
	a, err := s.GetJobReport(req.BatchA)
	if err != nil {
		return nil, err
	}
	b, err := s.GetJobReport(req.BatchB)
	if err != nil {
		return nil, err
	}
	return &CompareRunsResult{RunA: a, RunB: b, Delta: map[string]float64{"ttfa_seconds": b.TTFASeconds - a.TTFASeconds, "total_time_seconds": b.TotalTimeSeconds - a.TotalTimeSeconds, "average_throughput_rows_per_second": b.AverageThroughput - a.AverageThroughput, "coverage": b.Coverage - a.Coverage}}, nil
}

func safeID(id string) bool {
	if id == "" || len(id) > 100 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func atomicWriteJSON(path string, v any) error {
	payload, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + "." + uuid.NewString() + ".tmp"
	if err = os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
func readJSON(path string, v any) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, v)
}
func appendNDJSON(path string, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(append(payload, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
func readNDJSON(path string, consume func([]byte) error) error {
	payload, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(payload), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err = consume([]byte(line)); err != nil {
			return err
		}
	}
	return nil
}
