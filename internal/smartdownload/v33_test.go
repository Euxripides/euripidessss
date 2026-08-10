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
}

func (a *v33MockGroupAdapter) Name() string { return "v33_group_mock" }
func (a *v33MockGroupAdapter) Supports(dataset string) bool {
	return dataset == DatasetTokenTransfers || dataset == DatasetLogs
}
func (a *v33MockGroupAdapter) Available() bool { return true }
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
