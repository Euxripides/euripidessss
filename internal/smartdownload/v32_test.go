package smartdownload

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	reg "github.com/etl/backend/internal/smartdownload/registry"
)

type staticV32Metrics struct{ value V32ResourceMetrics }

func (m staticV32Metrics) SmartDownloadResourceMetrics(context.Context, string) (V32ResourceMetrics, error) {
	return m.value, nil
}

func newV32TestService(t *testing.T) (*Store, *Service) {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.DefaultEndBlock = 999
	opts.RangeChunkSize = 200
	opts.StallTimeout = time.Minute
	opts.StallCheckInterval = time.Hour
	svc := NewService(store, opts, NewJSONLPartWriter(store.Root()))
	// Planning/lifecycle tests use explicit TURBO-capable fixtures because
	// production preflight rejects runtimes with no executable provider.
	svc.RegisterAdapter(NewMockNamedProvider("sqd_cloud"))
	svc.RegisterAdapter(NewMockNamedProvider("rpc"))
	svc.SetV32ResourceMetricsSource(staticV32Metrics{V32ResourceMetrics{
		DiskFreeBytes: 100 << 30, DiskReserveBytes: 1 << 30,
		RPCQuotaRemaining: 1_000_000, RPCHardLimit: 1_000_000,
		CloudBudgetRemaining: 100, CloudHardLimit: 100,
		CloudRowsPerSecond: 50_000, RPCRowsPerSecond: 20_000,
		ParserRowsPerSecond: 60_000, ClickHouseRowsPerSecond: 45_000,
	}})
	t.Cleanup(svc.Shutdown)
	return store, svc
}

func v32Request() CreateBatchRequest {
	return CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA, addrB},
		Datasets:     []string{DatasetTransactions, DatasetTokenTransfers},
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 100, ToBlock: 1_099},
		Mode:         DownloadModeTurbo, ResourceProfile: ResourcePerformance,
	}
}

// Case A: preflight estimates production dimensions and writes no task state.
func TestV32CaseAPreflightEstimateNoSideEffect(t *testing.T) {
	store, svc := newV32TestService(t)
	before := len(store.ListBatches())
	result, err := svc.Preflight(context.Background(), v32Request())
	if err != nil {
		t.Fatal(err)
	}
	if len(store.ListBatches()) != before {
		t.Fatal("preflight created persistent task state")
	}
	if result.Estimate.Blocks == 0 || result.Estimate.Rows == 0 || result.Estimate.Bytes == 0 ||
		result.Estimate.CloudJobs == 0 || result.Estimate.RPCCalls == 0 || result.Estimate.ETA.UpperBoundSeconds <= result.Estimate.ETA.LowerBoundSeconds {
		t.Fatalf("incomplete estimate: %+v", result.Estimate)
	}
	if !result.Guards.Allowed || result.Confidence == "" || len(result.Basis) == 0 {
		t.Fatalf("incomplete decision: %+v", result)
	}
}

func TestV32PreflightRejectsUnknownRangeMode(t *testing.T) {
	_, svc := newV32TestService(t)
	req := v32Request()
	req.DefaultRange = &RangeSpec{Mode: "CUSTOM", FromBlock: 1, ToBlock: 2}
	if _, err := svc.Preflight(context.Background(), req); err == nil {
		t.Fatal("unknown range mode must fail closed instead of expanding to full history")
	}
}

func TestV32PreflightProviderWorkMatchesExecutableDatasetLanes(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.RangeChunkSize = 200
	svc := NewService(store, opts, NewJSONLPartWriter(store.Root()))
	t.Cleanup(svc.Shutdown)
	allModes := map[DownloadMode]bool{DownloadModeAuto: true, DownloadModeTurbo: true, DownloadModeEmergency: true}
	svc.RegisterAdapter(&schedulerMatrixAdapter{name: "sqd", available: true, modeOK: allModes,
		datasets: map[string]bool{DatasetTransactions: true, DatasetTokenTransfers: true, DatasetLogs: true, DatasetInternalTransactions: true}})
	svc.RegisterAdapter(&schedulerMatrixAdapter{name: "rpc", available: true, modeOK: allModes,
		datasets: map[string]bool{DatasetBalances: true, DatasetTokenTransfers: true, DatasetLogs: true}})
	svc.RegisterAdapter(&schedulerMatrixAdapter{name: "sqd_cloud", available: true, modeOK: allModes,
		datasets: map[string]bool{DatasetTokenTransfers: true}})
	svc.SetV32ResourceMetricsSource(staticV32Metrics{V32ResourceMetrics{
		DiskFreeBytes: 100 << 30, DiskReserveBytes: 1 << 30,
		RPCQuotaRemaining: 1_000_000, RPCHardLimit: 1_000_000,
		CloudBudgetRemaining: 100, CloudHardLimit: 100,
	}})
	skip := false
	cases := []struct {
		name      string
		mode      DownloadMode
		profile   ResourceProfile
		dataset   string
		wantCloud bool
		wantRPC   bool
	}{
		{name: "auto extreme balance is rpc only", mode: DownloadModeAuto, profile: ResourceExtreme, dataset: DatasetBalances, wantRPC: true},
		{name: "auto extreme transaction remains sqd", mode: DownloadModeAuto, profile: ResourceExtreme, dataset: DatasetTransactions},
		{name: "turbo logs cannot invent cloud", mode: DownloadModeTurbo, profile: ResourcePerformance, dataset: DatasetLogs, wantRPC: true},
		{name: "emergency transfer uses both real lanes", mode: DownloadModeEmergency, profile: ResourceExtreme, dataset: DatasetTokenTransfers, wantCloud: true, wantRPC: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := svc.preflight(context.Background(), CreateBatchRequest{
				ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{tc.dataset},
				Mode: tc.mode, ResourceProfile: tc.profile, SkipCovered: &skip,
				DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 100, ToBlock: 1_099},
			}, false)
			if err != nil {
				t.Fatal(err)
			}
			if got := result.Estimate.CloudJobs > 0; got != tc.wantCloud {
				t.Fatalf("cloud_jobs=%d, wantCloud=%v", result.Estimate.CloudJobs, tc.wantCloud)
			}
			if got := result.Estimate.RPCCalls > 0; got != tc.wantRPC {
				t.Fatalf("rpc_calls=%d, wantRPC=%v", result.Estimate.RPCCalls, tc.wantRPC)
			}
		})
	}
}

// 回归：预检必须按地址区分（本地覆盖复用 + Discovery 采样），
// 不能所有地址返回同一估算（此前只按数据集密度做算术，秒回且结果相同）。
func TestV32PreflightDifferentiatesAddresses(t *testing.T) {
	store, svc := newV32TestService(t)
	svc.RegisterAdapter(NewMockProvider())
	ctx := context.Background()
	// addrA 的 transactions 100-1099 已在本地覆盖索引认证。
	if err := svc.coverageIndex.AddCertified("bsc", 56, addrA, DatasetTransactions,
		[]reg.Interval{{From: 100, To: 1_099}}, 25, nil); err != nil {
		t.Fatal(err)
	}
	req := v32Request()
	req.Addresses, req.Datasets = []string{addrA}, []string{DatasetTransactions}
	resA, err := svc.Preflight(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resA.Estimate.Rows != 0 || resA.Estimate.Blocks != 0 {
		t.Fatalf("addrA 应全部复用本地覆盖（0 行 0 块），got rows=%d blocks=%d", resA.Estimate.Rows, resA.Estimate.Blocks)
	}
	req.Addresses = []string{addrB}
	resB, err := svc.Preflight(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resB.Estimate.Rows == 0 {
		t.Fatalf("addrB 无覆盖应执行采样估算，got rows=0: %+v", resB.Estimate)
	}
	if resA.Estimate.Rows == resB.Estimate.Rows {
		t.Fatal("不同地址预检结果必须不同")
	}
	if !strings.Contains(strings.Join(resB.Basis, "/"), "L0/L1/L2 sampling") {
		t.Fatalf("addrB 应声明采样依据，basis=%v", resB.Basis)
	}
	if len(store.ListBatches()) != 0 {
		t.Fatal("preflight 不应创建任务状态")
	}
}

// 回归：低活跃/无交易地址的 0 行采样是有效证据，预检应估算为 0 行，
// 而不是回退默认密度（此前 0 行探测被丢弃，导致所有地址都显示默认行数）。
func TestV32PreflightZeroActivityAddressEstimatesZero(t *testing.T) {
	_, svc := newV32TestService(t)
	svc.RegisterAdapter(zeroActivityProbe{name: "sqd_cloud"})
	svc.RegisterAdapter(zeroActivityProbe{name: "rpc"})
	req := v32Request()
	req.Addresses, req.Datasets = []string{addrA}, []string{DatasetTransactions}
	res, err := svc.Preflight(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Estimate.Rows != 0 {
		t.Fatalf("无交易地址应估算 0 行，got rows=%d（默认密度回退）", res.Estimate.Rows)
	}
	if !strings.Contains(strings.Join(res.Basis, "/"), "L0/L1/L2 sampling") {
		t.Fatalf("0 行采样应计入采样依据，basis=%v", res.Basis)
	}
}

// 回归：FULL 模式终点应取链当前高度（RPC eth_blockNumber），而不是写死
// DefaultEndBlock（50M）——否则预检与实际下载都只覆盖旧高度，数据不对。
func TestV32FullModeUsesHeadBlock(t *testing.T) {
	store, svc := newV32TestService(t)
	svc.SetHeadBlockFunc(func(_ context.Context, chainKey string) (uint64, error) {
		return 115_000_000, nil
	})
	ctx := context.Background()
	req := v32Request()
	req.Addresses, req.Datasets = []string{addrA}, []string{DatasetTransactions}
	req.DefaultRange = &RangeSpec{Mode: RangeModeFull}
	req.Mode = DownloadModeAuto
	res, err := svc.Preflight(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Estimate.Blocks != 115_000_001 {
		t.Fatalf("FULL 模式应按链头估算 115000001 块，got blocks=%d", res.Estimate.Blocks)
	}
	svc.opts.RangeChunkSize = 1_000_000 // 缩小测试创建的任务 Range 数量
	resp, err := svc.CreateBatch(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var maxTo uint64
	for _, a := range store.ListAddressesByBatch(resp.Batch.ID) {
		for _, ds := range store.ListDatasetsByAddress(a.ID) {
			for _, rj := range store.ListRangesByDataset(ds.ID) {
				if rj.ToBlock > maxTo {
					maxTo = rj.ToBlock
				}
			}
		}
	}
	if maxTo != 115_000_000 {
		t.Fatalf("FULL 模式创建任务应覆盖到链头 115000000，got maxTo=%d", maxTo)
	}
}

// zeroActivityProbe 始终返回 0 行且置信度有效（模拟无交易地址）。
type zeroActivityProbe struct{ name string }

func (p zeroActivityProbe) Name() string { return p.name }
func (zeroActivityProbe) Supports(d string) bool {
	return d == DatasetTransactions
}
func (zeroActivityProbe) Available() bool { return true }
func (zeroActivityProbe) Probe(context.Context, ProbeRequest) (ProbeResult, error) {
	return ProbeResult{EstimatedRows: 0, EstimatedBytes: 0, Confidence: 0.7}, nil
}
func (zeroActivityProbe) ExecuteRange(context.Context, RangeRequest) (*ProviderResult, error) {
	return nil, nil
}

// Case B: the slowest DB stage is diagnosed as CLICKHOUSE.
func TestV32CaseBClickHouseBottleneck(t *testing.T) {
	_, svc := newV32TestService(t)
	req := v32Request()
	req.Mode = DownloadModeAuto
	resp, err := svc.CreateBatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	svc.UpdateThroughput(resp.Batch.ID, ThroughputSnapshot{
		DownloadedRowsPerSecond: 150_000, ParsedRowsPerSecond: 140_000, InsertedRowsPerSecond: 30_000,
	})
	status, err := svc.HardeningStatus(resp.Batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Bottleneck != "CLICKHOUSE" {
		t.Fatalf("bottleneck=%s pipeline=%+v", status.Bottleneck, status.Pipeline)
	}
}

// Case C: RPC failure recovery requeues only the stalled range; completed work remains immutable.
func TestV32CaseCRPCStallScopedRecovery(t *testing.T) {
	store, svc := newV32TestService(t)
	req := v32Request()
	req.Mode = DownloadModeAuto
	req.Addresses, req.Datasets = []string{addrA}, []string{DatasetTransactions}
	resp, err := svc.CreateBatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	a := store.ListAddressesByBatch(resp.Batch.ID)[0]
	ds := store.ListDatasetsByAddress(a.ID)[0]
	ranges := store.ListRangesByDataset(ds.ID)
	if len(ranges) < 2 {
		t.Fatal("need at least two ranges")
	}
	now := time.Now().UTC()
	ranges[0].Status, ranges[0].RowsCommitted, ranges[0].FinishedAt = RangeCompleted, 9, &now
	if err := store.SaveRange(ranges[0]); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-2 * time.Minute)
	ranges[1].Status, ranges[1].Owner, ranges[1].Provider, ranges[1].StartedAt, ranges[1].UpdatedAt = RangeRunning, RangeOwnerRPC, "rpc", &old, old
	if err := store.SaveRange(ranges[1]); err != nil {
		t.Fatal(err)
	}
	actions, err := svc.DetectAndRecoverStalls(resp.Batch.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Action != "SWITCH_ENDPOINT_RETRY_RANGE" {
		t.Fatalf("actions=%+v", actions)
	}
	if got := store.GetRange(ranges[0].ID); got.Status != RangeCompleted || got.RowsCommitted != 9 {
		t.Fatalf("completed range changed: %+v", got)
	}
	if got := store.GetRange(ranges[1].ID); got.Status != RangeReady || got.Provider != "" {
		t.Fatalf("stalled range not narrowly requeued: %+v", got)
	}
}

// Case D: restart reconciliation repairs parent state without reopening completed ranges.
func TestV32CaseDRestartReconcilePreservesCompletedRanges(t *testing.T) {
	store, svc := newV32TestService(t)
	req := v32Request()
	req.Mode = DownloadModeAuto
	req.Addresses, req.Datasets = []string{addrA}, []string{DatasetTransactions}
	resp, err := svc.CreateBatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	a := store.ListAddressesByBatch(resp.Batch.ID)[0]
	ds := store.ListDatasetsByAddress(a.ID)[0]
	now := time.Now().UTC()
	for _, r := range store.ListRangesByDataset(ds.ID) {
		r.Status, r.FinishedAt, r.UpdatedAt = RangeCompleted, &now, now
		if err := store.SaveRange(r); err != nil {
			t.Fatal(err)
		}
	}
	ds.Status, ds.Validation, ds.UpdatedAt = DatasetRunning, &ValidationReport{Status: "PASS", Coverage: 1}, now
	if err := store.SaveDataset(ds); err != nil {
		t.Fatal(err)
	}
	a.Status = AddressDownloading
	if err := store.SaveAddress(a); err != nil {
		t.Fatal(err)
	}
	b := store.GetBatch(resp.Batch.ID)
	b.Status, b.StartedAt = BatchRunning, &now
	if err := store.SaveBatch(b); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.GetDataset(ds.ID); got.Status != DatasetCompleted {
		t.Fatalf("dataset=%s", got.Status)
	}
	if got := store.GetAddress(a.ID); got.Status != AddressCompleted {
		t.Fatalf("address=%s", got.Status)
	}
	if got := store.GetBatch(b.ID); got.Status != BatchCompleted {
		t.Fatalf("batch=%s", got.Status)
	}
	for _, r := range store.ListRangesByDataset(ds.ID) {
		if r.Status != RangeCompleted {
			t.Fatalf("completed range reopened: %+v", r)
		}
	}
	settledAt := store.GetBatch(b.ID).UpdatedAt
	if err := svc.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.GetBatch(b.ID).UpdatedAt; !got.Equal(settledAt) {
		t.Fatalf("unchanged terminal batch timestamp drifted: before=%s after=%s", settledAt, got)
	}
}

// Case E: insufficient disk is blocked before any batch is persisted.
func TestV32CaseEStorageGuardBlocksCreate(t *testing.T) {
	store, svc := newV32TestService(t)
	svc.SetV32ResourceMetricsSource(staticV32Metrics{V32ResourceMetrics{DiskFreeBytes: 1024, DiskReserveBytes: 512}})
	result, err := svc.Preflight(context.Background(), v32Request())
	if err != nil {
		t.Fatal(err)
	}
	if result.Guards.Storage.Status != "BLOCK" || result.Guards.Allowed {
		t.Fatalf("storage guard=%+v", result.Guards)
	}
	if _, err := svc.CreateBatch(context.Background(), v32Request()); err == nil {
		t.Fatal("create should be blocked")
	}
	if len(store.ListBatches()) != 0 {
		t.Fatal("blocked create persisted batch")
	}
}

// Case F: terminal reconciliation automatically persists a production report.
func TestV32CaseFAutomaticJobReport(t *testing.T) {
	store, svc := newV32TestService(t)
	req := v32Request()
	req.Mode = DownloadModeAuto
	req.Addresses, req.Datasets = []string{addrA}, []string{DatasetTransactions}
	resp, err := svc.CreateBatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	a := store.ListAddressesByBatch(resp.Batch.ID)[0]
	ds := store.ListDatasetsByAddress(a.ID)[0]
	start, finish := time.Now().UTC().Add(-10*time.Second), time.Now().UTC()
	for _, r := range store.ListRangesByDataset(ds.ID) {
		r.Status, r.RowsCommitted, r.StartedAt, r.FinishedAt, r.UpdatedAt = RangeCompleted, 100, &start, &finish, finish
		if err := store.SaveRange(r); err != nil {
			t.Fatal(err)
		}
		ds.DownloadedRows += 100
	}
	ds.Status, ds.Certification, ds.Validation, ds.UpdatedAt = DatasetRunning, CertificationDataset, &ValidationReport{Status: "VALIDATED", Coverage: 1, BlockCoverage: 1}, finish
	if err := store.SaveDataset(ds); err != nil {
		t.Fatal(err)
	}
	b := store.GetBatch(resp.Batch.ID)
	b.Status, b.StartedAt, b.FinishedAt = BatchRunning, &start, nil
	if err := store.SaveBatch(b); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	report, err := svc.GetJobReport(resp.Batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Coverage != 100 || report.Duplicates != 0 || report.Certification != string(CertificationBatch) || report.TTFASeconds <= 0 || report.TotalTimeSeconds <= 0 || report.AverageThroughput <= 0 {
		t.Fatalf("report=%+v", report)
	}
	if history, err := svc.PerformanceHistory(); err != nil || len(history) != 1 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

func TestV32PartialReportNeverCarriesCertificationAndRepairsCachedHistory(t *testing.T) {
	store, svc := newV32TestService(t)
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTokenTransfers},
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 1, ToBlock: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	batch := store.GetBatch(resp.Batch.ID)
	finished := time.Now().UTC()
	batch.Status, batch.FinishedAt = BatchPartial, &finished
	if err := store.SaveBatch(batch); err != nil {
		t.Fatal(err)
	}
	stale := &JobReport{BatchID: batch.ID, Status: BatchPartial, Certification: string(CertificationDatasetPartial), GeneratedAt: finished}
	if err := atomicWriteJSON(filepath.Join(svc.v32Root(), "reports", batch.ID+".json"), stale); err != nil {
		t.Fatal(err)
	}
	report, err := svc.GetJobReport(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != BatchPartial || report.Certification != string(CertificationPending) {
		t.Fatalf("partial report retained false certification: %+v", report)
	}
}

func TestV32TemplatePersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.StallCheckInterval = time.Hour
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	template, err := svc.SaveTemplate(SaveTemplateRequest{Name: "BSC transactions", Request: v32Request()})
	if err != nil {
		t.Fatal(err)
	}
	svc.Shutdown()
	store2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc2 := NewService(store2, opts, NewJSONLPartWriter(dir))
	defer svc2.Shutdown()
	got, err := svc2.GetTemplate(template.ID)
	if err != nil || got.Name != template.Name {
		t.Fatalf("template=%+v err=%v", got, err)
	}
}

func TestSaveTemplateRejectsConfigurationThatCannotEnterPreflight(t *testing.T) {
	_, svc := newV32TestService(t)
	base := v32Request()
	cases := []struct {
		name string
		edit func(*CreateBatchRequest)
	}{
		{"mode", func(req *CreateBatchRequest) { req.Mode = DownloadMode("FAST") }},
		{"profile", func(req *CreateBatchRequest) { req.ResourceProfile = ResourceProfile("HYPER") }},
		{"priority", func(req *CreateBatchRequest) { req.Priority = JobPriority("NOW") }},
		{"block-range", func(req *CreateBatchRequest) {
			req.DefaultRange = &RangeSpec{Mode: RangeModeBlock, FromBlock: 2, ToBlock: 1}
		}},
		{"time-format", func(req *CreateBatchRequest) {
			req.DefaultRange = &RangeSpec{Mode: RangeModeTime, StartTime: "not-a-time", EndTime: time.Now().UTC().Format(time.RFC3339)}
		}},
		{"time-order", func(req *CreateBatchRequest) {
			req.DefaultRange = &RangeSpec{Mode: RangeModeTime, StartTime: "2026-01-02T00:00:00Z", EndTime: "2026-01-01T00:00:00Z"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			tc.edit(&req)
			if _, err := svc.SaveTemplate(SaveTemplateRequest{Name: "invalid", Request: req}); err == nil {
				t.Fatal("invalid template was persisted")
			}
		})
	}
	items, err := svc.ListTemplates()
	if err != nil || len(items) != 0 {
		t.Fatalf("invalid templates changed persistence: items=%d err=%v", len(items), err)
	}
}
