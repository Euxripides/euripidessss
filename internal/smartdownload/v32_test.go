package smartdownload

import (
	"context"
	"testing"
	"time"
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
	ds.Status, ds.Validation, ds.UpdatedAt = DatasetRunning, &ValidationReport{Status: "PASS", Coverage: 1}, finish
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
