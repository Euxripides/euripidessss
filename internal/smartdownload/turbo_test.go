package smartdownload

import (
	"context"
	"strings"
	"testing"
	"time"
)

func newTurboService(t *testing.T) (*Store, *Service) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 4
	opts.DefaultEndBlock = 999
	opts.RangeChunkSize = 200
	opts.TurboTailBlocks = 200
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	svc.RegisterAdapter(NewMockNamedProvider("rpc"))
	svc.RegisterAdapter(NewMockNamedProvider("sqd_cloud"))
	return store, svc
}

func TestTurboPlannerAssignsNonOverlappingCloudAndRPCLanes(t *testing.T) {
	store, svc := newTurboService(t)
	t.Cleanup(svc.Shutdown)
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeTurbo,
		Addresses: []string{addrA}, Datasets: []string{DatasetTokenTransfers},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Batch.Mode != DownloadModeTurbo {
		t.Fatalf("mode=%s", resp.Batch.Mode)
	}
	seen := map[string]RangeOwner{}
	cloud, rpc := 0, 0
	for _, r := range store.ListRanges() {
		key := BlockRange{From: r.FromBlock, To: r.ToBlock}.Key()
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("range %s 被重复分配", key)
		}
		seen[key] = r.Owner
		if r.Priority < turboBulkPriority {
			t.Fatalf("Turbo range %s priority=%d, want >=%d", key, r.Priority, turboBulkPriority)
		}
		switch r.Owner {
		case RangeOwnerCloud:
			cloud++
		case RangeOwnerRPC:
			rpc++
		default:
			t.Fatalf("range %s owner=%s", key, r.Owner)
		}
	}
	if cloud == 0 || rpc == 0 {
		t.Fatalf("expected both lanes, cloud=%d rpc=%d", cloud, rpc)
	}
	if err := svc.Start(resp.Batch.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "Turbo batch complete", func() bool {
		return svc.GetBatch(resp.Batch.ID).Status == BatchCompleted
	})
	for _, r := range store.ListRanges() {
		if want := ownerProvider(r.Owner); r.Provider != want {
			t.Fatalf("range %d-%d provider=%s want owner provider=%s", r.FromBlock, r.ToBlock, r.Provider, want)
		}
	}
	status, err := svc.TurboStatus(resp.Batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.CoveragePercent != 100 || status.CompletedRanges != len(store.ListRanges()) {
		t.Fatalf("unexpected turbo status: %+v", status)
	}
}

func TestBatchModeSwitchReassignsOnlyUnfinishedRanges(t *testing.T) {
	store, svc := newTurboService(t)
	t.Cleanup(svc.Shutdown)
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeAuto,
		Addresses: []string{addrA}, Datasets: []string{DatasetTokenTransfers},
	})
	if err != nil {
		t.Fatal(err)
	}
	ranges := store.ListRanges()
	completed := ranges[0]
	completed.Status = RangeCompleted
	completed.Provider = "existing"
	if err := store.SaveRange(completed); err != nil {
		t.Fatal(err)
	}
	batch, err := svc.SetBatchMode(resp.Batch.ID, DownloadModeTurbo)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Mode != DownloadModeTurbo || batch.ModeSwitchedAt == nil {
		t.Fatalf("mode switch not persisted: %+v", batch)
	}
	if got := store.GetRange(completed.ID); got.Provider != "existing" || got.Owner != "" {
		t.Fatalf("completed range was reassigned: %+v", got)
	}
	for _, r := range store.ListRanges() {
		if r.ID != completed.ID && r.Owner == "" {
			t.Fatalf("unfinished range %s not assigned", r.ID)
		}
	}
	if _, err := svc.SetBatchMode(resp.Batch.ID, DownloadModeAuto); err != nil {
		t.Fatal(err)
	}
	for _, r := range store.ListRanges() {
		if r.ID != completed.ID && r.Owner != "" {
			t.Fatalf("AUTO did not release range owner: %+v", r)
		}
	}
}

func TestTurboRejectsRegularOnlyProvider(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(dir))
	t.Cleanup(svc.Shutdown)
	svc.RegisterAdapter(NewMockNamedProvider("sqd"))
	_, err = svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeTurbo,
		Addresses: []string{addrA}, Datasets: []string{DatasetTokenTransfers},
	})
	if err == nil {
		t.Fatal("Turbo must not fall back to regular SQD/CSV/AWS providers")
	}
}

func TestCloudWorkerSchemaIsCanonicalTokenTransfer(t *testing.T) {
	row := map[string]any{
		"chain_id": 56, "block_number": 94_805_374,
		"block_timestamp":  1_700_000_000,
		"transaction_hash": "0x" + strings.Repeat("a", 64),
		"log_index":        17, "token_address": addrA,
	}
	if got := datasetFromColumns(row); got != DatasetTokenTransfers {
		t.Fatalf("Cloud Worker schema dataset=%s, want %s", got, DatasetTokenTransfers)
	}
	if got := firstNumber(row, "block_time", "block_timestamp"); got != 1_700_000_000 {
		t.Fatalf("block timestamp fallback=%v", got)
	}
}

func TestFailedDatasetPropagatesToAddressAndBatch(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(t.TempDir()))
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTokenTransfers},
	})
	if err != nil {
		t.Fatal(err)
	}
	address := store.ListAddressesByBatch(created.Batch.ID)[0]
	dataset := store.ListDatasetsByAddress(address.ID)[0]
	dataset.Status = DatasetFailed
	if err := store.SaveDataset(dataset); err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	svc.finalizeAddressIfDoneLocked(address.ID)
	svc.mu.Unlock()
	if got := store.GetAddress(address.ID).Status; got != AddressFailed {
		t.Fatalf("address status=%s, want %s", got, AddressFailed)
	}
	if !svc.trySettle(created.Batch.ID) {
		t.Fatal("failed address should settle batch")
	}
	if got := store.GetBatch(created.Batch.ID).Status; got != BatchFailed {
		t.Fatalf("batch status=%s, want %s", got, BatchFailed)
	}
}
