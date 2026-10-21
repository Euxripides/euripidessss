package smartdownload

import (
	"context"
	"fmt"
	"testing"
	"time"
)

const largeTurboPersistenceRangeCount = 512

func newTurboPersistenceTestService(t *testing.T) (*Store, *Service) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 4
	opts.AdaptiveRanges = false
	opts.RangeChunkSize = 1
	opts.TurboTailBlocks = 32
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	svc.RegisterAdapter(NewMockNamedProvider("rpc"))
	svc.RegisterAdapter(NewMockNamedProvider("sqd_cloud"))
	t.Cleanup(svc.Shutdown)
	return store, svc
}

func createTurboPersistenceBatch(t *testing.T, svc *Service, address string, rangeCount int) string {
	t.Helper()
	skipCovered := false
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:    "bsc",
		Mode:        DownloadModeTurbo,
		Priority:    PriorityHigh,
		Addresses:   []string{address},
		Datasets:    []string{DatasetTokenTransfers},
		SkipCovered: &skipCovered,
		DefaultRange: &RangeSpec{
			Mode:      RangeModeBlock,
			FromBlock: 1,
			ToBlock:   uint64(rangeCount),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.RangeJobs != rangeCount {
		t.Fatalf("range jobs=%d, want %d", created.RangeJobs, rangeCount)
	}
	if err := svc.RebalanceBatch(created.Batch.ID); err != nil {
		t.Fatal(err)
	}
	return created.Batch.ID
}

func storeRevisionForTest(store *Store) uint64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.revision
}

func TestNoOpTurboRebalanceDoesNotPersistUnchangedLargeBatch(t *testing.T) {
	store, svc := newTurboPersistenceTestService(t)
	batchID := createTurboPersistenceBatch(t, svc, addrA, largeTurboPersistenceRangeCount)

	rangesBefore := store.ListRanges()
	before := storeRevisionForTest(store)
	if err := svc.RebalanceBatch(batchID); err != nil {
		t.Fatal(err)
	}
	if writes := storeRevisionForTest(store) - before; writes != 0 {
		t.Fatalf("no-op rebalance persisted %d unchanged jobs, want 0", writes)
	}

	rangesAfter := store.ListRanges()
	if len(rangesAfter) != len(rangesBefore) {
		t.Fatalf("range count changed after no-op rebalance: before=%d after=%d", len(rangesBefore), len(rangesAfter))
	}
	afterByID := make(map[string]*RangeJob, len(rangesAfter))
	for _, afterRange := range rangesAfter {
		afterByID[afterRange.ID] = afterRange
	}
	for _, beforeRange := range rangesBefore {
		afterRange := afterByID[beforeRange.ID]
		if afterRange == nil || beforeRange.Owner != afterRange.Owner ||
			beforeRange.Lane != afterRange.Lane || beforeRange.Priority != afterRange.Priority ||
			!beforeRange.UpdatedAt.Equal(afterRange.UpdatedAt) {
			t.Fatalf("stable range changed during no-op rebalance: before=%+v after=%+v", beforeRange, afterRange)
		}
	}
}

func TestPauseRequestedBatchDoesNotClaimAnotherRangeWhileDraining(t *testing.T) {
	store, svc := newTurboPersistenceTestService(t)
	batchID := createTurboPersistenceBatch(t, svc, "0x00000000000000000000000000000000000000f1", 2)

	addresses := store.ListAddressesByBatch(batchID)
	if len(addresses) != 1 {
		t.Fatalf("addresses=%d, want 1", len(addresses))
	}
	datasets := store.ListDatasetsByAddress(addresses[0].ID)
	if len(datasets) != 1 {
		t.Fatalf("datasets=%d, want 1", len(datasets))
	}
	ranges := store.ListRangesByDataset(datasets[0].ID)
	if len(ranges) != 2 {
		t.Fatalf("ranges=%d, want 2", len(ranges))
	}
	now := time.Now().UTC()
	ranges[0].Status = RangeRunning
	ranges[0].Provider = "sqd_cloud"
	ranges[0].StartedAt = &now
	if err := store.SaveRange(ranges[0]); err != nil {
		t.Fatal(err)
	}
	batch := store.GetBatch(batchID)
	batch.Status = BatchRunning
	if err := store.SaveBatch(batch); err != nil {
		t.Fatal(err)
	}
	paused, err := svc.PauseBatch(batchID)
	if err != nil {
		t.Fatal(err)
	}
	if !paused.PauseRequested || paused.Status != BatchRunning {
		t.Fatalf("pause should drain the active range first: %+v", paused)
	}
	if claimed := svc.claimNext(batchID); claimed != nil {
		t.Fatalf("pause-requested batch claimed another range: %+v", claimed)
	}
	if got := store.GetBatch(batchID); !got.PauseRequested || got.Status != BatchRunning {
		t.Fatalf("batch pause checkpoint changed unexpectedly: %+v", got)
	}
}

func TestTurboClaimPersistsOnlyClaimedTreeAfterNoOpRebalance(t *testing.T) {
	store, svc := newTurboPersistenceTestService(t)
	batchID := createTurboPersistenceBatch(t, svc, addrA, largeTurboPersistenceRangeCount)

	batch := store.GetBatch(batchID)
	now := time.Now().UTC()
	batch.Status = BatchRunning
	batch.StartedAt = &now
	batch.UpdatedAt = now
	if err := store.SaveBatch(batch); err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	svc.v31StateLocked(batchID).LastAllocation = time.Time{}
	svc.mu.Unlock()

	before := storeRevisionForTest(store)
	claim := svc.claimNext(batchID)
	if claim == nil {
		t.Fatal("expected a Turbo range claim")
	}
	writes := storeRevisionForTest(store) - before
	if writes > 4 {
		t.Fatalf("claim persisted %d jobs; unchanged allocator ranges must not be rewritten", writes)
	}
	if got := store.GetRange(claim.rangeID); got == nil || got.Status != RangeRunning {
		t.Fatalf("claimed range was not persisted as RUNNING: %+v", got)
	}
}

func TestLargeNoOpTurboRebalanceDoesNotDelayIndependentCancelWithRangeWrites(t *testing.T) {
	store, svc := newTurboPersistenceTestService(t)
	largeBatchID := createTurboPersistenceBatch(t, svc, addrA, largeTurboPersistenceRangeCount)
	smallBatchID := createTurboPersistenceBatch(t, svc, addrB, 1)

	before := storeRevisionForTest(store)
	errs := make(chan error, 2)
	go func() { errs <- svc.RebalanceBatch(largeBatchID) }()
	go func() {
		_, err := svc.CancelBatch(smallBatchID)
		errs <- err
	}()

	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("no-op rebalance and independent cancel did not finish")
		}
	}

	if got := store.GetBatch(smallBatchID); got == nil || got.Status != BatchCanceled {
		t.Fatalf("independent batch was not canceled: %+v", got)
	}
	// Canceling the one-range tree legitimately persists the request and final
	// states. The stable 512-range allocator pass must add no writes of its own.
	if writes := storeRevisionForTest(store) - before; writes > 12 {
		t.Fatalf("concurrent no-op rebalance added redundant persistence: writes=%d", writes)
	}
}

func TestStoreRangeDatasetIndexTracksRangeMoves(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rangeJob := &RangeJob{
		ID:           "indexed-range",
		DatasetJobID: "dataset-a",
		FromBlock:    1,
		ToBlock:      2,
		Status:       RangePending,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := store.SaveRange(rangeJob); err != nil {
		t.Fatal(err)
	}
	if got := store.ListRangesByDataset("dataset-a"); len(got) != 1 || got[0].ID != rangeJob.ID {
		t.Fatalf("dataset-a index mismatch: %+v", got)
	}

	rangeJob.DatasetJobID = "dataset-b"
	if err := store.SaveRange(rangeJob); err != nil {
		t.Fatal(err)
	}
	if got := store.ListRangesByDataset("dataset-a"); len(got) != 0 {
		t.Fatalf("stale dataset-a index entries: %+v", got)
	}
	if got := store.ListRangesByDataset("dataset-b"); len(got) != 1 || got[0].ID != rangeJob.ID {
		t.Fatalf("dataset-b index mismatch: %+v", got)
	}
}

func TestTurboClaimsReserveCapacityForCloudLane(t *testing.T) {
	store, svc := newTurboPersistenceTestService(t)
	batchID := createTurboPersistenceBatch(t, svc, addrA, 64)
	batch := store.GetBatch(batchID)
	now := time.Now().UTC()
	batch.Status = BatchRunning
	batch.StartedAt = &now
	batch.UpdatedAt = now
	if err := store.SaveBatch(batch); err != nil {
		t.Fatal(err)
	}

	providers := map[string]int{}
	for i := 0; i < 4; i++ {
		var claim *claimedRange
		if i == 0 {
			claim = svc.claimNextForOwner(batchID, RangeOwnerCloud)
		} else {
			claim = svc.claimNext(batchID)
		}
		if claim == nil {
			t.Fatalf("claim %d was nil", i+1)
		}
		if i == 0 && claim.provider != "sqd_cloud" {
			t.Fatalf("Cloud-preferred claim used %s", claim.provider)
		}
		providers[claim.provider]++
	}
	if providers["rpc"] == 0 || providers["sqd_cloud"] == 0 {
		t.Fatalf("TURBO claims did not use both lanes: %+v", providers)
	}
}

func TestTurboDetachesPersistedCloudRangesFromRPCSharedWork(t *testing.T) {
	store, svc := newTurboPersistenceTestService(t)
	now := time.Now().UTC()
	batch := &BatchJob{
		ID: "turbo-cloud-shared", ChainKey: "bsc", ChainID: 56,
		Status: BatchRunning, Mode: DownloadModeTurbo, Priority: PriorityHigh,
		CreatedAt: now, UpdatedAt: now, StartedAt: &now,
	}
	if err := store.SaveBatch(batch); err != nil {
		t.Fatal(err)
	}
	workID := "stale-rpc-shared-work"
	addresses := []string{addrA, addrB}
	refs := make([]SharedWorkRef, 0, len(addresses))
	for index, address := range addresses {
		rangeJob := &RangeJob{
			ID: fmt.Sprintf("cloud-range-%d", index), SharedWorkID: workID,
			DatasetJobID: fmt.Sprintf("dataset-%d", index), BatchID: batch.ID,
			AddressJobID: fmt.Sprintf("address-%d", index), Address: address,
			Dataset: DatasetTokenTransfers, FromBlock: 1, ToBlock: 2,
			Owner: RangeOwnerCloud, Lane: "bulk", Status: RangeReady,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := store.SaveRange(rangeJob); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, SharedWorkRef{
			BatchID: batch.ID, AddressJobID: rangeJob.AddressJobID,
			DatasetJobID: rangeJob.DatasetJobID, RangeJobID: rangeJob.ID,
			Address: address, Dataset: DatasetTokenTransfers,
		})
	}
	svc.v33.mu.Lock()
	svc.v33.works[workID] = &SharedWork{
		ID: workID, ChainKey: "bsc", ChainID: 56,
		Addresses: addresses, Datasets: []string{DatasetTokenTransfers},
		FromBlock: 1, ToBlock: 2, Status: sharedWorkReady,
		RefCount: len(refs), Refs: refs, CreatedAt: now, UpdatedAt: now,
	}
	svc.v33.mu.Unlock()

	svc.mu.Lock()
	svc.detachTurboCloudSharedWorkLocked()
	svc.mu.Unlock()
	if claim := svc.claimSharedWork(batch.ID); claim != nil {
		t.Fatalf("Cloud-owned stale SharedWork was claimed by %s", claim.adapter.Name())
	}
	for index := range addresses {
		if got := store.GetRange(fmt.Sprintf("cloud-range-%d", index)); got == nil || got.SharedWorkID != "" {
			t.Fatalf("Cloud range was not detached: %+v", got)
		}
	}
	svc.v33.mu.Lock()
	work := cloneSharedWork(svc.v33.works[workID])
	svc.v33.mu.Unlock()
	if work == nil || work.Status != sharedWorkSplit || work.RefCount != 0 {
		t.Fatalf("stale SharedWork was not retired: %+v", work)
	}
}

func TestTurboLaneCapacityCountsSharedWorkAsOneExecution(t *testing.T) {
	store, svc := newTurboPersistenceTestService(t)
	now := time.Now().UTC()
	batchID := "shared-capacity-batch"
	for workIndex := 0; workIndex < 2; workIndex++ {
		workID := fmt.Sprintf("shared-work-%d", workIndex)
		for refIndex := 0; refIndex < 21; refIndex++ {
			rangeJob := &RangeJob{
				ID:           fmt.Sprintf("shared-range-%d-%d", workIndex, refIndex),
				SharedWorkID: workID, DatasetJobID: fmt.Sprintf("dataset-%d", refIndex),
				BatchID: batchID, Owner: RangeOwnerRPC, Status: RangeRunning,
				FromBlock: uint64(workIndex + 1), ToBlock: uint64(workIndex + 1),
				CreatedAt: now, UpdatedAt: now,
			}
			if err := store.SaveRange(rangeJob); err != nil {
				t.Fatal(err)
			}
		}
		if workIndex == 0 {
			svc.mu.Lock()
			state := svc.v31StateLocked(batchID)
			state.Governor.ClaimsLimit = 4
			state.Governor.RPCClaimsLimit = 4
			state.Burst.Jobs = 2
			canClaimRPC := svc.canClaimLaneLocked(batchID, RangeOwnerRPC)
			svc.mu.Unlock()
			if !canClaimRPC {
				t.Fatal("one 21-ref SharedWork should consume one RPC execution slot")
			}
		}
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if svc.canClaimLaneLocked(batchID, RangeOwnerRPC) {
		t.Fatal("two RPC SharedWorks should fill the two RPC slots reserved by TURBO")
	}
	if !svc.canClaimLaneLocked(batchID, RangeOwnerCloud) {
		t.Fatal("two RPC SharedWorks must leave Cloud execution capacity")
	}
}
