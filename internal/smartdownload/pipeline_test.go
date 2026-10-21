package smartdownload

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockingDeferredCloud struct {
	syncStarted chan struct{}
	release     chan struct{}
	result      *ProviderResult
}

func (p *blockingDeferredCloud) Name() string         { return "sqd_cloud" }
func (p *blockingDeferredCloud) Supports(string) bool { return true }
func (p *blockingDeferredCloud) Available() bool      { return true }
func (p *blockingDeferredCloud) Probe(context.Context, ProbeRequest) (ProbeResult, error) {
	return ProbeResult{}, nil
}
func (p *blockingDeferredCloud) ExecuteRange(context.Context, RangeRequest) (*ProviderResult, error) {
	panic("synchronous Cloud path used")
}
func (p *blockingDeferredCloud) MaxAddressGroupSize(string) int { return 25 }
func (p *blockingDeferredCloud) SupportedDatasetBundles() [][]string {
	return [][]string{{DatasetTokenTransfers}}
}
func (p *blockingDeferredCloud) ExecuteGroupRange(context.Context, GroupRangeRequest) (map[string]map[string]*ProviderResult, error) {
	panic("synchronous Cloud group path used")
}
func (p *blockingDeferredCloud) remote() *cloudRemoteResult {
	return &cloudRemoteResult{rows: uint64(len(p.result.Records)), sync: func(ctx context.Context) (*ProviderResult, error) {
		close(p.syncStarted)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.release:
			return p.result, nil
		}
	}}
}
func (p *blockingDeferredCloud) ExecuteRemoteRange(context.Context, RangeRequest) (*cloudRemoteResult, error) {
	return p.remote(), nil
}
func (p *blockingDeferredCloud) ExecuteRemoteGroupRange(context.Context, GroupRangeRequest) (*cloudRemoteResult, error) {
	return p.remote(), nil
}

func deferredFixture(t *testing.T, sharedWorkID string) (*Service, *claimedRange) {
	t.Helper()
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	batch := &BatchJob{ID: "batch", ChainKey: "bsc", ChainID: 56, Mode: DownloadModeTurbo, Status: BatchRunning, CreatedAt: now, UpdatedAt: now}
	address := &AddressJob{ID: "address", BatchID: batch.ID, Address: "0x1111111111111111111111111111111111111111", ChainKey: "bsc", ChainID: 56, Status: AddressDownloading, CreatedAt: now, UpdatedAt: now}
	ds := &DatasetJob{ID: "dataset", BatchID: batch.ID, AddressJobID: address.ID, Address: address.Address, ChainKey: "bsc", Dataset: DatasetTokenTransfers, Status: DatasetRunning, CreatedAt: now, UpdatedAt: now}
	rangeJob := &RangeJob{ID: "range", SharedWorkID: sharedWorkID, DatasetJobID: ds.ID, BatchID: batch.ID, AddressJobID: address.ID, Address: address.Address, Dataset: ds.Dataset, FromBlock: 10, ToBlock: 20, Provider: "sqd_cloud", Owner: RangeOwnerCloud, Status: RangeRunning, CreatedAt: now, UpdatedAt: now, StartedAt: &now}
	if err := store.SaveBatch(batch); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAddress(address); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDataset(ds); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRange(rangeJob); err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	cp := &CheckpointV3{DatasetJobID: ds.ID, Dataset: ds.Dataset, Address: ds.Address, RequestedFrom: 10, RequestedTo: 20, PendingRanges: []BlockRange{{From: 10, To: 20}}}
	if err := svc.cp.Save(cp); err != nil {
		t.Fatal(err)
	}
	return svc, &claimedRange{rangeID: rangeJob.ID, datasetJobID: ds.ID, addressJobID: address.ID, batchID: batch.ID, provider: "sqd_cloud", req: RangeRequest{DatasetJobID: ds.ID, Mode: DownloadModeTurbo, Address: ds.Address, Dataset: ds.Dataset, ChainKey: "bsc", ChainID: 56, FromBlock: 10, ToBlock: 20}}
}

func deferredRecord(address string) Record {
	return Record{ChainID: 56, BlockNumber: 15, TransactionHash: "0xabc", LogIndex: 1, Dataset: DatasetTokenTransfers, Address: address, Payload: map[string]any{"from_address": address}}
}

func TestPipelineStageBoundedQueueBlocksWithoutDropping(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	stage := newPipelineStage(ctx, 1, 1, &wg)
	block := make(chan struct{})
	var completed atomic.Int64
	if err := stage.enqueueBlocking("one", func(context.Context) pipelineOutcome {
		<-block
		completed.Add(1)
		return pipelineOutcome{rows: 1, bytes: 10}
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for stage.snapshot().Running != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := stage.enqueueBlocking("two", func(context.Context) pipelineOutcome {
		completed.Add(1)
		return pipelineOutcome{rows: 2, bytes: 20}
	}); err != nil {
		t.Fatal(err)
	}
	enqueued := make(chan error, 1)
	go func() {
		enqueued <- stage.enqueueBlocking("three", func(context.Context) pipelineOutcome {
			completed.Add(1)
			return pipelineOutcome{rows: 3, bytes: 30}
		})
	}()
	select {
	case err := <-enqueued:
		t.Fatalf("third enqueue did not apply bounded backpressure: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(block)
	select {
	case err := <-enqueued:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("third task was lost while queue was full")
	}
	deadline = time.Now().Add(time.Second)
	for completed.Load() != 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if completed.Load() != 3 {
		t.Fatalf("completed=%d, want 3", completed.Load())
	}
	snapshot := stage.snapshot()
	if snapshot.Capacity != 1 || snapshot.Workers != 1 || snapshot.QueuedTotal != 3 || snapshot.CompletedTotal != 3 || snapshot.RowsTotal != 6 || snapshot.BytesTotal != 60 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	cancel()
	wg.Wait()
}

func TestPipelineStageDeduplicatesSameGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	stage := newPipelineStage(ctx, 16, 1, &wg)
	block := make(chan struct{})
	var runs atomic.Int64
	run := func(context.Context) pipelineOutcome { <-block; runs.Add(1); return pipelineOutcome{} }
	if !stage.tryEnqueue("dataset:1", run) || !stage.tryEnqueue("dataset:1", run) {
		t.Fatal("same generation should be accepted as deduplicated")
	}
	close(block)
	deadline := time.Now().Add(time.Second)
	for stage.snapshot().CompletedTotal != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if runs.Load() != 1 || stage.snapshot().QueuedTotal != 1 {
		t.Fatalf("runs=%d snapshot=%+v", runs.Load(), stage.snapshot())
	}
	cancel()
	wg.Wait()
}

func TestPipelineDefaultCapacityAndWorkers(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	snapshot := svc.PipelineSnapshot()
	for name, stage := range map[string]PipelineStageSnapshot{"local_sync": snapshot.LocalSync, "validation": snapshot.Validation, "clickhouse": snapshot.ClickHouse} {
		if stage.Capacity != 16 || stage.Workers != 2 {
			t.Fatalf("%s = %+v", name, stage)
		}
	}
	svc.Shutdown()
}

func TestCloudRemoteCompletedReleasesRangeWorkerBeforeLocalSync(t *testing.T) {
	svc, claim := deferredFixture(t, "")
	adapter := &blockingDeferredCloud{syncStarted: make(chan struct{}), release: make(chan struct{}), result: &ProviderResult{Records: []Record{deferredRecord(claim.req.Address)}, CompletedTo: 20}}
	claim.adapter = adapter
	returned := make(chan struct{})
	go func() { svc.executeRange(claim); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("Range Worker remained blocked after REMOTE_COMPLETED")
	}
	select {
	case <-adapter.syncStarted:
	case <-time.After(time.Second):
		t.Fatal("Local Sync worker did not start")
	}
	if got := svc.store.GetRange(claim.rangeID).Status; got != RangeRunning {
		t.Fatalf("range committed before Local Sync: %s", got)
	}
	close(adapter.release)
	deadline := time.Now().Add(time.Second)
	for svc.store.GetRange(claim.rangeID).Status != RangeCompleted && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := svc.store.GetRange(claim.rangeID); got.Status != RangeCompleted || got.RowsCommitted != 1 {
		t.Fatalf("deferred range not committed: %+v", got)
	}
	svc.Shutdown()
}

func TestCloudGroupRemoteCompletedReleasesRangeWorkerBeforeLocalSync(t *testing.T) {
	svc, rangeClaim := deferredFixture(t, "shared")
	adapter := &blockingDeferredCloud{syncStarted: make(chan struct{}), release: make(chan struct{}), result: &ProviderResult{Records: []Record{deferredRecord(rangeClaim.req.Address)}, CompletedTo: 20}}
	now := time.Now().UTC()
	work := &SharedWork{ID: "shared", ChainKey: "bsc", ChainID: 56, Datasets: []string{DatasetTokenTransfers}, Addresses: []string{rangeClaim.req.Address}, FromBlock: 10, ToBlock: 20, Owner: RangeOwnerCloud, Status: sharedWorkRunning, RefCount: 1, Provider: "sqd_cloud", OwnerBatchID: rangeClaim.batchID, CreatedAt: now, UpdatedAt: now, Refs: []SharedWorkRef{{BatchID: rangeClaim.batchID, AddressJobID: rangeClaim.addressJobID, DatasetJobID: rangeClaim.datasetJobID, RangeJobID: rangeClaim.rangeID, Address: rangeClaim.req.Address, Dataset: DatasetTokenTransfers}}}
	svc.v33.mu.Lock()
	svc.v33.works[work.ID] = work
	svc.v33.mu.Unlock()
	claim := &claimedSharedWork{work: work, group: adapter, adapter: adapter, mode: DownloadModeTurbo, priority: 90}
	returned := make(chan struct{})
	go func() { svc.executeSharedWork(claim); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("Group Range Worker remained blocked after REMOTE_COMPLETED")
	}
	select {
	case <-adapter.syncStarted:
	case <-time.After(time.Second):
		t.Fatal("Group Local Sync worker did not start")
	}
	if got := svc.store.GetRange(rangeClaim.rangeID).Status; got != RangeRunning {
		t.Fatalf("group range committed before Local Sync: %s", got)
	}
	close(adapter.release)
	deadline := time.Now().Add(time.Second)
	for svc.store.GetRange(rangeClaim.rangeID).Status != RangeCompleted && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := svc.store.GetRange(rangeClaim.rangeID); got.Status != RangeCompleted || got.RowsCommitted != 1 {
		t.Fatalf("deferred group range not committed: %+v", got)
	}
	svc.Shutdown()
}
