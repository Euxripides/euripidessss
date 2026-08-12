package smartdownload

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCreatePersistsExactLifecycleTree(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	skip := false
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA, addrA},
		Datasets: []string{DatasetTransactions, DatasetTransactions}, SkipCovered: &skip,
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 101, ToBlock: 101},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Valid != 1 || resp.Duplicates != 1 || resp.DatasetJobs != 1 || resp.RangeJobs != 1 {
		t.Fatalf("create response does not match deduplicated tree: %+v", resp)
	}
	batch := store.GetBatch(resp.Batch.ID)
	addresses := store.ListAddressesByBatch(batch.ID)
	if batch.Status != BatchCreated || len(addresses) != 1 || addresses[0].Status != AddressWaiting {
		t.Fatalf("unexpected persisted parent tree: batch=%+v addresses=%+v", batch, addresses)
	}
	datasets := store.ListDatasetsByAddress(addresses[0].ID)
	if len(datasets) != 1 || datasets[0].Status != DatasetPending {
		t.Fatalf("unexpected persisted datasets: %+v", datasets)
	}
	ranges := store.ListRangesByDataset(datasets[0].ID)
	if len(ranges) != 1 || ranges[0].Status != RangePending || ranges[0].FromBlock != 101 || ranges[0].ToBlock != 101 {
		t.Fatalf("unexpected persisted ranges: %+v", ranges)
	}
}

func TestTimeRangeResolvesExactlyAndNeverFallsBackToFull(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	skip := false
	req := CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions}, SkipCovered: &skip,
		DefaultRange: &RangeSpec{Mode: RangeModeTime, StartTime: "2026-08-01T00:00:00Z", EndTime: "2026-08-01T00:01:00Z"},
	}
	if _, err := svc.Preflight(context.Background(), req); err == nil || !strings.Contains(err.Error(), "\u62d2\u7edd\u9000\u5316\u4e3a FULL") {
		t.Fatalf("TIME without resolver must fail closed, err=%v", err)
	}
	if len(store.ListBatches()) != 0 {
		t.Fatal("failed TIME preflight persisted task state")
	}
	var calls int
	svc.SetTimeRangeResolver(func(_ context.Context, chainKey string, start, end time.Time) (uint64, uint64, error) {
		calls++
		if chainKey != "bsc" || !start.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) || !end.Equal(time.Date(2026, 8, 1, 0, 1, 0, 0, time.UTC)) {
			t.Fatalf("resolver input mismatch: chain=%s start=%s end=%s", chainKey, start, end)
		}
		return 123_456, 123_456, nil
	})
	resp, err := svc.CreateBatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("TIME resolver calls=%d, want 1", calls)
	}
	a := store.ListAddressesByBatch(resp.Batch.ID)[0]
	if a.Range.Mode != RangeModeTime || a.Range.FromBlock != 123_456 || a.Range.ToBlock != 123_456 {
		t.Fatalf("resolved TIME metadata mismatch: %+v", a.Range)
	}
	ranges := store.ListRanges()
	if len(ranges) != 1 || ranges[0].FromBlock != 123_456 || ranges[0].ToBlock != 123_456 {
		t.Fatalf("TIME expanded beyond resolved one-block interval: %+v", ranges)
	}
}

func TestPreflightRejectsInvalidModeBeforePersistence(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	_, err := svc.Preflight(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadMode("FAST"), Addresses: []string{addrA},
		Datasets: []string{DatasetTransactions}, DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 1, ToBlock: 1},
	})
	if err == nil || !strings.Contains(err.Error(), "FAST") {
		t.Fatalf("invalid mode preflight err=%v", err)
	}
	if len(store.ListBatches()) != 0 {
		t.Fatal("invalid preflight persisted a batch")
	}
}

func TestDatasetPauseResumeAPIAndCancelPersistConsistentTree(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers, opts.AdaptiveRanges = 1, false
	provider := newCancelAwareProvider()
	svc := NewService(store, opts, NewJSONLPartWriter(root))
	svc.RegisterAdapter(provider)
	t.Cleanup(svc.Shutdown)
	skip := false
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions}, SkipCovered: &skip,
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 10, ToBlock: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := store.ListAddressesByBatch(created.Batch.ID)[0]
	ds := store.ListDatasetsByAddress(a.ID)[0]
	paused, err := svc.PauseDataset(ds.ID)
	if err != nil || paused.Status != DatasetPaused || paused.PauseRequested {
		t.Fatalf("idle dataset pause did not settle synchronously: ds=%+v err=%v", paused, err)
	}
	h := NewHandler(svc, nil)
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/datasets/"+ds.ID+"/resume", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("resume API code=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var apiDataset DatasetJob
	if err := json.Unmarshal(recorder.Body.Bytes(), &apiDataset); err != nil {
		t.Fatal(err)
	}
	if apiDataset.ID != ds.ID || apiDataset.Status != DatasetRunning || apiDataset.PauseRequested {
		t.Fatalf("resume API data mismatch: %+v", apiDataset)
	}
	select {
	case <-provider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("resumed dataset did not execute")
	}
	canceled, err := svc.CancelDataset(ds.ID)
	if err != nil || canceled.Status != DatasetCanceled {
		t.Fatalf("dataset cancel=%+v err=%v", canceled, err)
	}
	select {
	case <-provider.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("dataset cancel did not propagate to provider context")
	}
	if got := store.GetAddress(a.ID); got.Status != AddressCanceled {
		t.Fatalf("address=%+v, want CANCELED", got)
	}
	if got := store.GetBatch(created.Batch.ID); got.Status != BatchCanceled {
		t.Fatalf("batch=%+v, want CANCELED", got)
	}
	for _, r := range store.ListRangesByDataset(ds.ID) {
		if r.Status != RangeCanceled {
			t.Fatalf("range did not settle canceled: %+v", r)
		}
	}
}

func TestBatchPauseResumeAndModeRoundTripPreserveCompletedRanges(t *testing.T) {
	store, svc := newTurboService(t)
	t.Cleanup(svc.Shutdown)
	skip := false
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeAuto, Addresses: []string{addrA},
		Datasets: []string{DatasetTokenTransfers}, SkipCovered: &skip,
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 100, ToBlock: 699},
	})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := svc.PauseBatch(created.Batch.ID)
	if err != nil || paused.Status != BatchPaused || paused.PauseRequested {
		t.Fatalf("created batch pause did not synchronously settle: %+v err=%v", paused, err)
	}
	for _, a := range store.ListAddressesByBatch(created.Batch.ID) {
		if a.Status != AddressPaused {
			t.Fatalf("paused batch child address=%+v", a)
		}
		for _, ds := range store.ListDatasetsByAddress(a.ID) {
			if ds.Status != DatasetPaused {
				t.Fatalf("paused batch child dataset=%+v", ds)
			}
		}
	}
	// Return the tree to CREATED-like idle state for deterministic mode checks.
	batch := store.GetBatch(created.Batch.ID)
	batch.Status, batch.PauseRequested = BatchCreated, false
	if err := store.SaveBatch(batch); err != nil {
		t.Fatal(err)
	}
	for _, a := range store.ListAddressesByBatch(batch.ID) {
		a.Status = AddressWaiting
		if err := store.SaveAddress(a); err != nil {
			t.Fatal(err)
		}
		for _, ds := range store.ListDatasetsByAddress(a.ID) {
			ds.Status = DatasetPending
			if err := store.SaveDataset(ds); err != nil {
				t.Fatal(err)
			}
		}
	}
	ranges := store.ListRanges()
	completed := ranges[0]
	now := time.Now().UTC()
	completed.Status, completed.Provider, completed.FinishedAt = RangeCompleted, "existing", &now
	if err := store.SaveRange(completed); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []DownloadMode{DownloadModeTurbo, DownloadModeEmergency, DownloadModeAuto} {
		updated, err := svc.SetBatchMode(batch.ID, mode)
		if err != nil || updated.Mode != mode || store.GetBatch(batch.ID).Mode != mode {
			t.Fatalf("switch mode %s: batch=%+v persisted=%+v err=%v", mode, updated, store.GetBatch(batch.ID), err)
		}
		if got := store.GetRange(completed.ID); got.Status != RangeCompleted || got.Provider != "existing" {
			t.Fatalf("mode %s changed completed evidence: %+v", mode, got)
		}
	}
	for _, r := range store.ListRanges() {
		if r.ID != completed.ID && (r.Owner != "" || r.Lane != "") {
			t.Fatalf("AUTO did not release unfinished lane ownership: %+v", r)
		}
	}
}

func TestPreferredProviderControlsAutoExecutionUntilFailure(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	svc.RegisterAdapter(NewMockNamedProvider("aaa-provider"))
	svc.RegisterAdapter(NewMockNamedProvider("zzz-preferred"))
	skip := false
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions}, SkipCovered: &skip,
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 10, ToBlock: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := store.ListAddressesByBatch(created.Batch.ID)[0]
	ds := store.ListDatasetsByAddress(a.ID)[0]
	ds.PreferredProvider = "zzz-preferred"
	if err := store.SaveDataset(ds); err != nil {
		t.Fatal(err)
	}
	claim := svc.claimNext(created.Batch.ID)
	if claim == nil || claim.provider != "zzz-preferred" {
		t.Fatalf("preferred provider was display-only: %+v", claim)
	}
	failed := store.GetRange(claim.rangeID)
	failed.Status = RangeReady
	failed.FailedProviders = []string{"zzz-preferred"}
	if err := store.SaveRange(failed); err != nil {
		t.Fatal(err)
	}
	claim = svc.claimNext(created.Batch.ID)
	if claim == nil || claim.provider == "zzz-preferred" {
		t.Fatalf("failed preferred provider did not fall back: %+v", claim)
	}
}

func TestOptionalWarehouseFailureCompletesCertifiedParquet(t *testing.T) {
	writer := &fakeIndexedWriter{fail: true, result: IndexedWriteResult{InputRows: 2}}
	svc, dsID := indexedFixture(t, writer)
	t.Cleanup(svc.Shutdown)
	svc.SetWarehouseRequired(false)
	callbacks := 0
	svc.SetOnDatasetIndexed(func(*IndexedResult) { callbacks++ })
	svc.indexDataset(dsID)
	ds := svc.store.GetDataset(dsID)
	if ds.Status != DatasetCompleted || ds.Certification != CertificationDataset || ds.WarehouseStatus != "FAILED_OPTIONAL" || ds.WarehouseError == "" {
		t.Fatalf("optional warehouse incorrectly blocked canonical result: %+v", ds)
	}
	entries := svc.results.List()
	if len(entries) != 1 || entries[0].Certification != "CERTIFIED" || entries[0].RowCount != 2 {
		t.Fatalf("optional warehouse revoked canonical result certification: %+v", *entries[0])
	}
	if callbacks != 0 {
		t.Fatal("warehouse-dependent callback fired without warehouse write")
	}
}

func TestRequiredWarehouseUnavailableRemainsRetryable(t *testing.T) {
	svc, dsID := indexedFixture(t, nil)
	t.Cleanup(svc.Shutdown)
	svc.SetWarehouseRequired(true)
	svc.indexDataset(dsID)
	ds := svc.store.GetDataset(dsID)
	if ds.Status != DatasetDBWriteFailed || ds.Certification != CertificationPending || ds.FinishedAt != nil || ds.WarehouseStatus != "FAILED_REQUIRED" {
		t.Fatalf("required warehouse failure was not retained for retry: %+v", ds)
	}
}

func TestTemplateInstantiateAppliesWhitelistedOverridesAndRevalidates(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	template, err := svc.SaveTemplate(SaveTemplateRequest{Name: "lifecycle-template", Request: CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions},
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 1, ToBlock: 9},
	}})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(svc, nil)
	body := `{"addresses":["` + addrB + `"],"mode":"AUTO","default_range":{"mode":"BLOCK","from_block":22,"to_block":22}}`
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/templates/"+template.ID+"/instantiate", strings.NewReader(body)))
	if w.Code != http.StatusCreated {
		t.Fatalf("instantiate code=%d body=%s", w.Code, w.Body.String())
	}
	var response CreateBatchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	addresses := store.ListAddressesByBatch(response.Batch.ID)
	if len(addresses) != 1 || addresses[0].Address != addrB || addresses[0].Range.FromBlock != 22 || addresses[0].Range.ToBlock != 22 {
		t.Fatalf("template overrides were ignored: %+v", addresses)
	}
	before := len(store.ListBatches())
	for _, invalid := range []string{`{"mode":"FAST"}`, `{"unknown_field":true}`} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/templates/"+template.ID+"/instantiate", strings.NewReader(invalid)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid overrides code=%d body=%s", w.Code, w.Body.String())
		}
	}
	if len(store.ListBatches()) != before {
		t.Fatal("invalid template overrides persisted a batch")
	}
}

func TestCoverageQueryRejectsInvalidSemanticInputs(t *testing.T) {
	_, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	h := NewHandler(svc, nil)
	cases := []string{
		`{"chain_key":"unknown-chain","address":"` + addrA + `","dataset":"transactions","from_block":1,"to_block":2}`,
		`{"chain_key":"bsc","address":"` + addrA + `","dataset":"not-a-dataset","from_block":1,"to_block":2}`,
		`{"chain_key":"bsc","address":"` + addrA + `","dataset":"transactions","from_block":2,"to_block":1}`,
	}
	for _, body := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/coverage/query", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "detail") {
			t.Fatalf("invalid coverage input code=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestRecoverySettlesPersistedAddressCancelRequestAcrossDescendants(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	skip := false
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions}, SkipCovered: &skip,
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 10, ToBlock: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	batch := store.GetBatch(created.Batch.ID)
	batch.Status, batch.StartedAt = BatchRunning, &now
	_ = store.SaveBatch(batch)
	a := store.ListAddressesByBatch(batch.ID)[0]
	a.Status, a.CancelRequested = AddressDownloading, true
	_ = store.SaveAddress(a)
	ds := store.ListDatasetsByAddress(a.ID)[0]
	ds.Status = DatasetRunning
	_ = store.SaveDataset(ds)
	rangeJob := store.ListRangesByDataset(ds.ID)[0]
	rangeJob.Status, rangeJob.StartedAt = RangeRunning, &now
	_ = store.SaveRange(rangeJob)
	if err := svc.RecoverAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.GetAddress(a.ID); got.Status != AddressCanceled || got.CancelRequested {
		t.Fatalf("address cancel request not normalized: %+v", got)
	}
	if got := store.GetDataset(ds.ID); got.Status != DatasetCanceled {
		t.Fatalf("dataset below canceled address revived: %+v", got)
	}
	if got := store.GetRange(rangeJob.ID); got.Status != RangeCanceled {
		t.Fatalf("range below canceled address revived: %+v", got)
	}
	if got := store.GetBatch(batch.ID); got.Status != BatchCanceled {
		t.Fatalf("single canceled address batch did not settle: %+v", got)
	}
}

func TestRecoveryNormalizesPersistedBatchPauseRequest(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	skip := false
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions}, SkipCovered: &skip,
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 10, ToBlock: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	batch := store.GetBatch(created.Batch.ID)
	batch.Status, batch.PauseRequested, batch.StartedAt = BatchRunning, true, &now
	_ = store.SaveBatch(batch)
	a := store.ListAddressesByBatch(batch.ID)[0]
	a.Status = AddressDownloading
	_ = store.SaveAddress(a)
	ds := store.ListDatasetsByAddress(a.ID)[0]
	ds.Status = DatasetRunning
	_ = store.SaveDataset(ds)
	rangeJob := store.ListRangesByDataset(ds.ID)[0]
	rangeJob.Status, rangeJob.StartedAt = RangeRunning, &now
	_ = store.SaveRange(rangeJob)
	if err := svc.RecoverAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.GetBatch(batch.ID); got.Status != BatchPaused || got.PauseRequested {
		t.Fatalf("batch pause request not normalized: %+v", got)
	}
	if got := store.GetAddress(a.ID); got.Status != AddressPaused {
		t.Fatalf("address below paused batch inconsistent: %+v", got)
	}
	if got := store.GetDataset(ds.ID); got.Status != DatasetPaused {
		t.Fatalf("dataset below paused batch inconsistent: %+v", got)
	}
	if got := store.GetRange(rangeJob.ID); got.Status != RangeReady {
		t.Fatalf("in-flight range was not safely requeued at pause checkpoint: %+v", got)
	}
}

func TestBlockRangeWithEqualBoundsStaysSingleBlock(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	skipCovered := false
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions},
		SkipCovered:  &skipCovered,
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 94_800_000, ToBlock: 94_800_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	ranges := store.ListRanges()
	if len(ranges) != 1 || ranges[0].FromBlock != 94_800_000 || ranges[0].ToBlock != 94_800_000 {
		t.Fatalf("single-block request expanded: %+v", ranges)
	}
	if resp.RangeJobs != 1 {
		t.Fatalf("range_jobs=%d, want 1", resp.RangeJobs)
	}
}

func TestProgressUsesLogicalUnionAcrossRepairReshardAndHedge(t *testing.T) {
	store, svc := newTurboService(t)
	t.Cleanup(svc.Shutdown)
	skipCovered := false
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeTurbo,
		Addresses: []string{addrA}, Datasets: []string{DatasetTransactions},
		SkipCovered:  &skipCovered,
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 100, ToBlock: 199},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := store.ListAddressesByBatch(resp.Batch.ID)[0]
	ds := store.ListDatasetsByAddress(a.ID)[0]
	original := store.ListRangesByDataset(ds.ID)[0]
	now := time.Now().UTC()
	original.Status, original.FromBlock, original.ToBlock = RangeCompleted, 100, 149
	original.RowsCommitted, original.FinishedAt = 5, &now
	if err := store.SaveRange(original); err != nil {
		t.Fatal(err)
	}
	duplicates := []*RangeJob{
		{ID: "reshard-child", DatasetJobID: ds.ID, BatchID: resp.Batch.ID, AddressJobID: a.ID, Address: addrA, Dataset: ds.Dataset,
			FromBlock: 150, ToBlock: 199, ParentRangeID: original.ID, ReshardDepth: 1, Status: RangeCompleted, RowsCommitted: 5, FinishedAt: &now, CreatedAt: now, UpdatedAt: now},
		{ID: "repair-overlap", DatasetJobID: ds.ID, BatchID: resp.Batch.ID, AddressJobID: a.ID, Address: addrA, Dataset: ds.Dataset,
			FromBlock: 100, ToBlock: 199, Purpose: "REPAIR", Status: RangeCompleted, RowsCommitted: 10, FinishedAt: &now, CreatedAt: now, UpdatedAt: now},
		{ID: "hedge-overlap", DatasetJobID: ds.ID, BatchID: resp.Batch.ID, AddressJobID: a.ID, Address: addrA, Dataset: ds.Dataset,
			FromBlock: 100, ToBlock: 199, HedgeOf: original.ID, HedgeWinner: false, Status: RangeCanceled, CreatedAt: now, UpdatedAt: now},
	}
	for _, r := range duplicates {
		if err := store.SaveRange(r); err != nil {
			t.Fatal(err)
		}
	}
	svc.mu.Lock()
	svc.updateProgressLocked(ds.ID)
	svc.mu.Unlock()
	progress := store.GetDataset(ds.ID).Progress
	if progress.BlocksTotal != 100 || progress.BlocksCurrent != 100 || progress.Percent != 1 {
		t.Fatalf("logical progress inflated by derived ranges: %+v", progress)
	}
	status, err := svc.TurboStatus(resp.Batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.TotalBlocks != 100 || status.CoveredBlocks != 100 || status.CoveragePercent != 100 {
		t.Fatalf("turbo progress inflated by derived ranges: %+v", status)
	}
}

type cancelAwareProvider struct {
	started chan struct{}
	stopped chan struct{}
}

func newCancelAwareProvider() *cancelAwareProvider {
	return &cancelAwareProvider{started: make(chan struct{}), stopped: make(chan struct{})}
}

func (*cancelAwareProvider) Name() string         { return "cancel-aware" }
func (*cancelAwareProvider) Supports(string) bool { return true }
func (*cancelAwareProvider) Available() bool      { return true }
func (*cancelAwareProvider) Probe(context.Context, ProbeRequest) (ProbeResult, error) {
	return ProbeResult{EstimatedRows: 1, EstimatedBytes: 128, Confidence: 1}, nil
}
func (p *cancelAwareProvider) ExecuteRange(ctx context.Context, _ RangeRequest) (*ProviderResult, error) {
	close(p.started)
	<-ctx.Done()
	close(p.stopped)
	return nil, ctx.Err()
}

func TestCancelBatchCancelsInflightProviderAndSettlesImmediately(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 1
	opts.AdaptiveRanges = false
	provider := newCancelAwareProvider()
	svc := NewService(store, opts, NewJSONLPartWriter(root))
	svc.RegisterAdapter(provider)
	t.Cleanup(svc.Shutdown)
	skipCovered := false
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions},
		SkipCovered:  &skipCovered,
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 100, ToBlock: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(resp.Batch.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not start")
	}
	canceled, err := svc.CancelBatch(resp.Batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != BatchCanceled {
		t.Fatalf("cancel returned status=%s, want CANCELED", canceled.Status)
	}
	select {
	case <-provider.stopped:
	case <-time.After(time.Second):
		t.Fatal("in-flight provider context was not canceled within one second")
	}
	for _, r := range store.ListRanges() {
		if r.Status != RangeCanceled {
			t.Fatalf("range status=%s, want CANCELED", r.Status)
		}
	}
	addresses := store.ListAddressesByBatch(resp.Batch.ID)
	if len(addresses) != 1 || addresses[0].Status != AddressCanceled {
		t.Fatalf("address tree not canceled: %+v", addresses)
	}
	datasets := store.ListDatasetsByAddress(addresses[0].ID)
	if len(datasets) != 1 || datasets[0].Status != DatasetCanceled {
		t.Fatalf("dataset tree not canceled: %+v", datasets)
	}
	// The provider cancellation error must not reopen or downgrade the terminal tree.
	time.Sleep(100 * time.Millisecond)
	if got := store.GetBatch(resp.Batch.ID); got.Status != BatchCanceled {
		t.Fatalf("terminal batch changed after provider returned: %+v", got)
	}
}

func TestValidationQualityReconcilesUniqueRowsAndCertification(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	now := time.Now().UTC()
	ds := &DatasetJob{
		ID: "quality-reconcile", Status: DatasetValidating, DownloadedRows: 6,
		Progress:      ProgressSnapshot{RowsCurrent: 6, RowsTotal: 6},
		Certification: CertificationDataset, RelevantCertified: true, RelevantCertifiedAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveDataset(ds); err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	svc.applyValidationQualityLocked(ds, &ValidationReport{
		Status: "PARTIAL", Coverage: 1, BlockCoverage: 1,
		Rows: 6, UniqueKeyCount: 3, DuplicateCount: 3,
	})
	svc.mu.Unlock()
	if ds.DownloadedRows != 3 || ds.ValidatedRows != 3 || ds.Progress.RowsCurrent != 3 {
		t.Fatalf("unique row reconciliation failed: %+v", ds)
	}
	if ds.Certification != CertificationPending || ds.RelevantCertified || ds.RelevantCertifiedAt != nil {
		t.Fatalf("partial result retained certification: %+v", ds)
	}
	svc.mu.Lock()
	svc.applyValidationQualityLocked(ds, &ValidationReport{
		Status: "VALIDATED", Coverage: 1, BlockCoverage: 1,
		Rows: 3, UniqueKeyCount: 3,
	})
	svc.mu.Unlock()
	if ds.Certification != CertificationPending {
		t.Fatalf("validation promoted dataset before canonical merge: %+v", ds)
	}
}

func TestRangeCompletionDoesNotEmitDatasetCertification(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	skipCovered := false
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions},
		SkipCovered:  &skipCovered,
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 10, ToBlock: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := store.ListAddressesByBatch(resp.Batch.ID)[0]
	ds := store.ListDatasetsByAddress(a.ID)[0]
	rj := store.ListRangesByDataset(ds.ID)[0]
	rj.Status = RangeCompleted
	svc.mu.Lock()
	svc.certifyRangeOnlyLocked(rj)
	svc.mu.Unlock()
	if got := store.GetDataset(ds.ID); got.Certification == CertificationDataset {
		t.Fatalf("range completion prematurely certified dataset: %+v", got)
	}
	entries, err := NewLedger(store.Root(), ds.ID).Replay()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Event == LedgerDatasetCertified {
			t.Fatalf("premature dataset certification event: %+v", entry)
		}
	}
}

func TestRecoveryNormalizesCanceledParentDescendants(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	skipCovered := false
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions},
		SkipCovered:  &skipCovered,
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 10, ToBlock: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	batch := store.GetBatch(resp.Batch.ID)
	batch.Status, batch.FinishedAt = BatchCanceled, &now
	if err := store.SaveBatch(batch); err != nil {
		t.Fatal(err)
	}
	a := store.ListAddressesByBatch(batch.ID)[0]
	a.Status, a.FinishedAt = AddressCanceled, &now
	if err := store.SaveAddress(a); err != nil {
		t.Fatal(err)
	}
	ds := store.ListDatasetsByAddress(a.ID)[0]
	ds.Status = DatasetRunning
	if err := store.SaveDataset(ds); err != nil {
		t.Fatal(err)
	}
	rj := store.ListRangesByDataset(ds.ID)[0]
	rj.Status = RangeRunning
	if err := store.SaveRange(rj); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecoverAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.GetDataset(ds.ID); got.Status != DatasetCanceled {
		t.Fatalf("dataset status=%s, want CANCELED", got.Status)
	}
	if got := store.GetRange(rj.ID); got.Status != RangeCanceled {
		t.Fatalf("range status=%s, want CANCELED", got.Status)
	}
}

func TestRepairRangeIsClampedToOriginalSingleBlockRequest(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	skipCovered := false
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions},
		SkipCovered:  &skipCovered,
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 94_800_000, ToBlock: 94_800_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := store.ListAddressesByBatch(resp.Batch.ID)[0]
	ds := store.ListDatasetsByAddress(a.ID)[0]
	svc.mu.Lock()
	created := svc.repairDatasetGapsLocked(ds.ID, &ValidationReport{
		Status: "PARTIAL", MissingRanges: []BlockRange{{From: 0, To: 115_000_000}},
	})
	svc.mu.Unlock()
	if !created {
		t.Fatal("repair was not planned")
	}
	for _, r := range store.ListRangesByDataset(ds.ID) {
		if r.FromBlock != 94_800_000 || r.ToBlock != 94_800_000 {
			t.Fatalf("repair escaped original request: %+v", r)
		}
	}
}
