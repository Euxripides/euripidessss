package smartdownload

import (
	"context"
	"sync"
	"testing"
	"time"
)

type v33MockGroupAdapter struct {
	mu       sync.Mutex
	calls    int
	failures int
	modeOK   *bool
}

func (a *v33MockGroupAdapter) Name() string { return "v33_group_mock" }
func (a *v33MockGroupAdapter) Supports(dataset string) bool {
	return dataset == DatasetTokenTransfers || dataset == DatasetLogs
}
func (a *v33MockGroupAdapter) Available() bool { return true }
func (a *v33MockGroupAdapter) AvailableForMode(string, DownloadMode) bool {
	return a.modeOK == nil || *a.modeOK
}
func (a *v33MockGroupAdapter) Probe(context.Context, ProbeRequest) (ProbeResult, error) {
	return ProbeResult{}, nil
}
func (a *v33MockGroupAdapter) ExecuteRange(context.Context, RangeRequest) (*ProviderResult, error) {
	return &ProviderResult{}, nil
}
func (a *v33MockGroupAdapter) MaxAddressGroupSize(string) int { return 100 }
func (a *v33MockGroupAdapter) SupportedDatasetBundles() [][]string {
	return [][]string{{DatasetTokenTransfers, DatasetLogs}}
}
func (a *v33MockGroupAdapter) ExecuteGroupRange(_ context.Context, req GroupRangeRequest) (map[string]map[string]*ProviderResult, error) {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	results := make(map[string]map[string]*ProviderResult, len(req.Addresses))
	for _, address := range req.Addresses {
		results[address] = make(map[string]*ProviderResult, len(req.Datasets))
		for _, dataset := range req.Datasets {
			results[address][dataset] = &ProviderResult{CompletedTo: req.ToBlock}
		}
	}
	return results, nil
}
func (a *v33MockGroupAdapter) Calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func v33Request(addresses ...string) CreateBatchRequest {
	return CreateBatchRequest{
		ChainKey: "bsc", Addresses: addresses,
		Datasets:     []string{DatasetTokenTransfers, DatasetLogs},
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 100, ToBlock: 120},
	}
}

func TestV33PlannerPreviewHasNoSideEffectsAndBundles(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	defer svc.Shutdown()
	svc.RegisterAdapter(&v33MockGroupAdapter{})
	before := len(store.ListBatches())
	plan, err := svc.PlannerV2(context.Background(), v33Request(
		"0x0000000000000000000000000000000000000003",
		"0x0000000000000000000000000000000000000001",
		"0x0000000000000000000000000000000000000002",
		"0x0000000000000000000000000000000000000001",
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(store.ListBatches()); got != before {
		t.Fatalf("planner preview mutated store: before=%d after=%d", before, got)
	}
	if plan.Metrics.InputJobs != 6 || plan.Metrics.MergedWorkloads != 1 {
		t.Fatalf("unexpected coalescing metrics: %+v", plan.Metrics)
	}
	if len(plan.DatasetBundles) != 1 || !plan.DatasetBundles[0].Bundled {
		t.Fatalf("expected explicit transfer+logs bundle: %+v", plan.DatasetBundles)
	}
	if len(plan.Groups) != 1 || len(plan.Groups[0].Addresses) != 3 {
		t.Fatalf("expected one sorted address group: %+v", plan.Groups)
	}
}

func TestV33PlannerDoesNotUseGroupProviderUnavailableForChainMode(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	defer svc.Shutdown()
	modeOK := false
	svc.RegisterAdapter(&v33MockGroupAdapter{modeOK: &modeOK})
	plan, err := svc.PlannerV2(context.Background(), v33Request(
		"0x0000000000000000000000000000000000000001",
		"0x0000000000000000000000000000000000000002",
	))
	if err == nil || plan != nil {
		t.Fatalf("unavailable dataset must fail closed, plan=%+v err=%v", plan, err)
	}
}

func TestV33PlannerRejectsInvalidModeRelevantRangeAndUnavailableDataset(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	t.Cleanup(svc.Shutdown)
	svc.RegisterAdapter(&v33MockGroupAdapter{})

	invalidMode := v33Request(addrA)
	invalidMode.Mode = DownloadMode("FAST")
	if plan, err := svc.PlannerV2(context.Background(), invalidMode); err == nil || plan != nil {
		t.Fatalf("invalid mode silently downgraded: plan=%+v err=%v", plan, err)
	}

	invalidRelevant := v33Request(addrA)
	invalidRelevant.RelevantRange = &RangeSpec{Mode: RangeModeBlock, FromBlock: 121, ToBlock: 120}
	if plan, err := svc.PlannerV2(context.Background(), invalidRelevant); err == nil || plan != nil {
		t.Fatalf("reversed relevant range was ignored: plan=%+v err=%v", plan, err)
	}

	unavailable := CreateBatchRequest{ChainKey: "bsc", Mode: DownloadModeTurbo,
		Addresses: []string{addrA}, Datasets: []string{DatasetTransactions},
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 100, ToBlock: 100}}
	if plan, err := svc.PlannerV2(context.Background(), unavailable); err == nil || plan != nil {
		t.Fatalf("Turbo unavailable dataset was advertised: plan=%+v err=%v", plan, err)
	}
}

func TestV33PlannerKeepsAddressOverrideRangesSeparate(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	t.Cleanup(svc.Shutdown)
	svc.RegisterAdapter(&v33MockGroupAdapter{})
	second := "0x0000000000000000000000000000000000000002"
	req := v33Request(addrA, second)
	req.AddressOverrides = map[string]RangeSpec{second: {Mode: RangeModeBlock, FromBlock: 200, ToBlock: 220}}
	plan, err := svc.PlannerV2(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.SharedWorkloads) != 2 {
		t.Fatalf("different address ranges were incorrectly coalesced: %+v", plan.SharedWorkloads)
	}
	got := map[string]bool{}
	for _, work := range plan.SharedWorkloads {
		got[BlockRange{From: work.FromBlock, To: work.ToBlock}.Key()] = true
		if len(work.Addresses) != 1 {
			t.Fatalf("range-specific workload mixed addresses: %+v", work)
		}
	}
	if !got["100-120"] || !got["200-220"] {
		t.Fatalf("planner ranges=%v", got)
	}
}

func TestV33FingerprintIsCanonical(t *testing.T) {
	a := canonicalSharedFingerprint("BSC", []string{"logs", "token_transfers"}, []string{
		"0x0000000000000000000000000000000000000002",
		"0x0000000000000000000000000000000000000001",
	}, 1, 10)
	b := canonicalSharedFingerprint("bsc", []string{"token_transfers", "logs", "logs"}, []string{
		"0x0000000000000000000000000000000000000001",
		"0x0000000000000000000000000000000000000002",
	}, 1, 10)
	if a != b {
		t.Fatalf("canonical fingerprint drift: %s != %s", a, b)
	}
}

func TestV33AcceleratorDoesNotStealCloudOwnedRanges(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	t.Cleanup(svc.Shutdown)
	now := time.Now().UTC()
	batch := &BatchJob{ID: "cloud-lane-batch", ChainKey: "bsc", Status: BatchCreated, CreatedAt: now, UpdatedAt: now}
	address := &AddressJob{ID: "cloud-lane-address", BatchID: batch.ID, Address: "0x0000000000000000000000000000000000000001", ChainKey: "bsc", Status: AddressWaiting, CreatedAt: now, UpdatedAt: now}
	dataset := &DatasetJob{ID: "cloud-lane-dataset", BatchID: batch.ID, AddressJobID: address.ID, Address: address.Address, ChainKey: "bsc", Dataset: DatasetTokenTransfers, Status: DatasetPending, CreatedAt: now, UpdatedAt: now}
	if err = store.SaveBatch(batch); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveAddress(address); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveDataset(dataset); err != nil {
		t.Fatal(err)
	}
	cloudRange := &RangeJob{ID: "cloud-range", BatchID: batch.ID, AddressJobID: address.ID, DatasetJobID: dataset.ID, Owner: RangeOwnerCloud, Status: RangePending, FromBlock: 1, ToBlock: 10, CreatedAt: now, UpdatedAt: now}
	rpcRange := &RangeJob{ID: "rpc-range", BatchID: batch.ID, AddressJobID: address.ID, DatasetJobID: dataset.ID, Owner: RangeOwnerRPC, Status: RangePending, FromBlock: 11, ToBlock: 20, CreatedAt: now, UpdatedAt: now}
	if err = store.SaveRange(cloudRange); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveRange(rpcRange); err != nil {
		t.Fatal(err)
	}
	refs := svc.batchRangeRefs(batch.ID)
	if len(refs) != 1 || refs[0].Range.ID != rpcRange.ID {
		t.Fatalf("V3.3 accelerator stole Cloud ownership: %+v", refs)
	}
}

func TestV33CrossBatchJoinCancelAndFanout(t *testing.T) {
	root := t.TempDir()
	// Dataset validation/indexing is intentionally asynchronous; let its final
	// atomic registry rename finish before TempDir performs Windows cleanup.
	t.Cleanup(func() { time.Sleep(300 * time.Millisecond) })
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 2
	svc := NewService(store, opts, NewJSONLPartWriter(root))
	defer svc.Shutdown()
	adapter := &v33MockGroupAdapter{}
	svc.RegisterAdapter(adapter)
	req := v33Request(
		"0x0000000000000000000000000000000000000011",
		"0x0000000000000000000000000000000000000012",
		"0x0000000000000000000000000000000000000013",
	)
	first, err := svc.CreateBatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateBatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	plan := svc.BatchAccelerator(second.Batch.ID)
	if plan == nil || len(plan.SharedWorkloads) != 1 || plan.Metrics.DuplicateWorkAvoided != 6 {
		t.Fatalf("second batch did not join exact active work: %+v", plan)
	}
	if _, err = svc.CancelBatch(first.Batch.ID); err != nil {
		t.Fatal(err)
	}
	plan = svc.BatchAccelerator(second.Batch.ID)
	if plan.SharedWorkloads[0].RefCount != 6 {
		t.Fatalf("canceling one batch released the provider prematurely: ref_count=%d", plan.SharedWorkloads[0].RefCount)
	}
	if err = svc.Start(second.Batch.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		allTerminal := true
		for _, address := range store.ListAddressesByBatch(second.Batch.ID) {
			for _, dataset := range store.ListDatasetsByAddress(address.ID) {
				for _, rangeJob := range store.ListRangesByDataset(dataset.ID) {
					allTerminal = allTerminal && rangeJob.Status.Terminal()
				}
			}
		}
		if allTerminal {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if adapter.Calls() != 1 {
		t.Fatalf("expected one shared provider request, got %d", adapter.Calls())
	}
	for _, address := range store.ListAddressesByBatch(second.Batch.ID) {
		for _, dataset := range store.ListDatasetsByAddress(address.ID) {
			for _, rangeJob := range store.ListRangesByDataset(dataset.ID) {
				if !rangeJob.Status.Terminal() {
					t.Fatalf("fan-out did not settle range %s: %s", rangeJob.ID, rangeJob.Status)
				}
			}
		}
	}
}

func TestV33SingleWorkHonorsPreferredProviderBeforeGroupAdapter(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	t.Cleanup(svc.Shutdown)
	csv := NewMockNamedProvider("csv")
	svc.RegisterAdapter(csv)
	svc.RegisterAdapter(&v33MockGroupAdapter{})

	now := time.Now().UTC()
	batch := &BatchJob{ID: "single-preferred-batch", ChainKey: "bsc", Mode: DownloadModeAuto,
		Status: BatchCreated, CreatedAt: now, UpdatedAt: now}
	address := &AddressJob{ID: "single-preferred-address", BatchID: batch.ID, Address: addrA,
		ChainKey: "bsc", Status: AddressWaiting, CreatedAt: now, UpdatedAt: now}
	dataset := &DatasetJob{ID: "single-preferred-dataset", BatchID: batch.ID, AddressJobID: address.ID,
		Address: address.Address, ChainKey: "bsc", Dataset: DatasetTokenTransfers,
		PreferredProvider: "csv", Status: DatasetPending, CreatedAt: now, UpdatedAt: now}
	rangeJob := &RangeJob{ID: "single-preferred-range", SharedWorkID: "single-preferred-work",
		BatchID: batch.ID, AddressJobID: address.ID, DatasetJobID: dataset.ID,
		Status: RangeReady, FromBlock: 1, ToBlock: 2, CreatedAt: now, UpdatedAt: now}
	for _, save := range []func() error{
		func() error { return store.SaveBatch(batch) }, func() error { return store.SaveAddress(address) },
		func() error { return store.SaveDataset(dataset) }, func() error { return store.SaveRange(rangeJob) },
	} {
		if err := save(); err != nil {
			t.Fatal(err)
		}
	}
	svc.v33.mu.Lock()
	svc.v33.works[rangeJob.SharedWorkID] = &SharedWork{ID: rangeJob.SharedWorkID, ChainKey: "bsc",
		Datasets: []string{DatasetTokenTransfers}, Addresses: []string{address.Address}, FromBlock: 1, ToBlock: 2,
		Status: sharedWorkReady, RefCount: 1, Refs: []SharedWorkRef{{BatchID: batch.ID, AddressJobID: address.ID,
			DatasetJobID: dataset.ID, RangeJobID: rangeJob.ID, Address: address.Address, Dataset: dataset.Dataset}}}
	svc.v33.mu.Unlock()

	claim := svc.claimSharedWork(batch.ID)
	if claim == nil || claim.adapter == nil {
		t.Fatal("single shared work was not claimable")
	}
	if claim.group != nil || claim.adapter.Name() != "csv" {
		t.Fatalf("single work ignored preferred provider: adapter=%s group=%T", claim.adapter.Name(), claim.group)
	}
}

func TestV33RegistryRecoveryResetsRunningOnly(t *testing.T) {
	root := t.TempDir()
	runtime := newV33Runtime(root)
	now := time.Now().UTC()
	runtime.mu.Lock()
	runtime.works["running"] = &SharedWork{ID: "running", Status: sharedWorkRunning, OwnerBatchID: "batch", UpdatedAt: now}
	runtime.works["done"] = &SharedWork{ID: "done", Status: sharedWorkCompleted, UpdatedAt: now}
	if err := runtime.persistLocked(); err != nil {
		runtime.mu.Unlock()
		t.Fatal(err)
	}
	runtime.mu.Unlock()
	recovered := newV33Runtime(root)
	if recovered.works["running"].Status != sharedWorkReady || recovered.works["running"].OwnerBatchID != "" {
		t.Fatalf("running work was not safely requeued: %+v", recovered.works["running"])
	}
	if recovered.works["done"].Status != sharedWorkCompleted {
		t.Fatalf("terminal work was rewritten: %+v", recovered.works["done"])
	}
}

func TestV33BinarySplitPreservesReferences(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	defer svc.Shutdown()
	work := &SharedWork{ID: "parent", ChainKey: "bsc", Datasets: []string{DatasetLogs},
		Addresses: []string{"a", "b", "c", "d"}, FromBlock: 1, ToBlock: 2, Status: sharedWorkRunning}
	for _, address := range work.Addresses {
		work.Refs = append(work.Refs, SharedWorkRef{Address: address, Dataset: DatasetLogs, RangeJobID: address})
	}
	svc.v33.mu.Lock()
	svc.v33.works[work.ID] = work
	svc.splitSharedWorkLocked(work)
	children, refs := 0, 0
	for _, candidate := range svc.v33.works {
		if candidate.ParentID == work.ID {
			children++
			refs += len(candidate.Refs)
		}
	}
	svc.v33.mu.Unlock()
	if children != 2 || refs != 4 || !work.Split || work.Status != sharedWorkSplit {
		t.Fatalf("invalid binary split: children=%d refs=%d parent=%+v", children, refs, work)
	}
}
