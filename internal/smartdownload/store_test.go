package smartdownload

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreSaveLoadList(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	b := &BatchJob{ID: "b1", ChainKey: "bsc", ChainID: 56, Status: BatchCreated, AddressCount: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.SaveBatch(b); err != nil {
		t.Fatal(err)
	}
	a := &AddressJob{ID: "a1", BatchID: "b1", Address: "0xabc", Status: AddressWaiting, CreatedAt: now, UpdatedAt: now}
	if err := s.SaveAddress(a); err != nil {
		t.Fatal(err)
	}
	ds := &DatasetJob{ID: "d1", BatchID: "b1", AddressJobID: "a1", Dataset: DatasetTransactions, Status: DatasetPending, CreatedAt: now, UpdatedAt: now}
	if err := s.SaveDataset(ds); err != nil {
		t.Fatal(err)
	}
	r := &RangeJob{ID: "r1", DatasetJobID: "d1", BatchID: "b1", AddressJobID: "a1", FromBlock: 0, ToBlock: 99, Status: RangePending, CreatedAt: now, UpdatedAt: now}
	if err := s.SaveRange(r); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.GetBatch("b1") == nil || reloaded.GetAddress("a1") == nil || reloaded.GetDataset("d1") == nil || reloaded.GetRange("r1") == nil {
		t.Fatal("reload missing entries")
	}
	if len(reloaded.ListAddressesByBatch("b1")) != 1 || len(reloaded.ListDatasetsByAddress("a1")) != 1 || len(reloaded.ListRangesByDataset("d1")) != 1 {
		t.Fatal("list queries failed")
	}
}

func TestStoreParallelLoadKeepsAllValidRecords(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	records := map[string]map[string]any{
		"batches": {
			"batch.json": &BatchJob{ID: "b1", ChainKey: "bsc", Status: BatchCreated, CreatedAt: now, UpdatedAt: now},
		},
		"addresses": {
			"address.json": &AddressJob{ID: "a1", BatchID: "b1", Address: "0xabc", Status: AddressWaiting, CreatedAt: now, UpdatedAt: now},
		},
		"datasets": {
			"dataset.json": &DatasetJob{ID: "d1", BatchID: "b1", AddressJobID: "a1", Dataset: DatasetTransactions, Status: DatasetPending, CreatedAt: now, UpdatedAt: now},
		},
	}
	for kind, files := range records {
		kindDir := filepath.Join(dir, kind)
		if err := os.MkdirAll(kindDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, record := range files {
			writeStoreTestJSON(t, filepath.Join(kindDir, name), record)
		}
	}

	rangesDir := filepath.Join(dir, "ranges")
	if err := os.MkdirAll(rangesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const rangeCount = 1500
	for index := 0; index < rangeCount; index++ {
		id := fmt.Sprintf("r-%04d", index)
		writeStoreTestJSON(t, filepath.Join(rangesDir, id+".json"), &RangeJob{
			ID: id, DatasetJobID: "d1", BatchID: "b1", AddressJobID: "a1",
			FromBlock: uint64(index * 100), ToBlock: uint64(index*100 + 99),
			Status: RangePending, CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := os.WriteFile(filepath.Join(rangesDir, "broken.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rangesDir, "ignored.txt"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(loaded.ListRanges()); got != rangeCount {
		t.Fatalf("loaded range count = %d, want %d", got, rangeCount)
	}
	last := loaded.GetRange("r-1499")
	if last == nil || last.FromBlock != 149900 || last.ToBlock != 149999 {
		t.Fatalf("last range mismatch: %#v", last)
	}
	if loaded.GetBatch("b1") == nil || loaded.GetAddress("a1") == nil || loaded.GetDataset("d1") == nil {
		t.Fatal("parallel load missed a valid non-range record")
	}
}

func TestStoreLoadPreservesStableDuplicateIDOverride(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ranges")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	writeStoreTestJSON(t, filepath.Join(dir, "a.json"), &RangeJob{ID: "same", FromBlock: 1, ToBlock: 10, Status: RangePending, CreatedAt: now, UpdatedAt: now})
	writeStoreTestJSON(t, filepath.Join(dir, "z.json"), &RangeJob{ID: "same", FromBlock: 20, ToBlock: 30, Status: RangeCompleted, CreatedAt: now, UpdatedAt: now})

	loaded, err := NewStore(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	rangeJob := loaded.GetRange("same")
	if rangeJob == nil || rangeJob.FromBlock != 20 || rangeJob.Status != RangeCompleted {
		t.Fatalf("stable override mismatch: %#v", rangeJob)
	}
}

func TestStoreSnapshotFastPathAndCounts(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBatch(&BatchJob{ID: "b1", ChainKey: "bsc", Status: BatchCreated, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRange(&RangeJob{ID: "r1", BatchID: "b1", FromBlock: 1, ToBlock: 10, Status: RangePending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	fromFiles, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if fromFiles.loadedFromSnapshot {
		t.Fatal("mutated store unexpectedly used stale snapshot")
	}
	fromSnapshot, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if !fromSnapshot.loadedFromSnapshot {
		t.Fatal("unchanged store did not use validated snapshot")
	}
	if len(fromSnapshot.ListBatches()) != 1 || len(fromSnapshot.ListRanges()) != 1 {
		t.Fatalf("snapshot counts mismatch: batches=%d ranges=%d", len(fromSnapshot.ListBatches()), len(fromSnapshot.ListRanges()))
	}
}

func TestStoreSnapshotInvalidatesAfterAtomicStateChange(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRange(&RangeJob{ID: "r1", FromBlock: 1, ToBlock: 10, Status: RangePending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(root); err != nil { // fallback 后建立当前快照
		t.Fatal(err)
	}
	if cached, err := NewStore(root); err != nil || !cached.loadedFromSnapshot {
		t.Fatalf("snapshot baseline unavailable: loaded=%v err=%v", cached != nil && cached.loadedFromSnapshot, err)
	}

	time.Sleep(10 * time.Millisecond)
	replacement := &RangeJob{ID: "r1", FromBlock: 20, ToBlock: 30, Status: RangeCompleted, CreatedAt: now, UpdatedAt: now}
	replaceStoreTestJSON(t, filepath.Join(root, "ranges", "r1.json"), replacement)

	reloaded, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.loadedFromSnapshot {
		t.Fatal("directory mutation did not invalidate snapshot")
	}
	got := reloaded.GetRange("r1")
	if got == nil || got.FromBlock != 20 || got.Status != RangeCompleted {
		t.Fatalf("fallback did not recover authoritative state: %#v", got)
	}
}

func TestStoreSnapshotCorruptionFallsBackToAuthoritativeFiles(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRange(&RangeJob{ID: "r1", FromBlock: 1, ToBlock: 10, Status: RangePending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, storeSnapshotDataName), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.loadedFromSnapshot || reloaded.GetRange("r1") == nil {
		t.Fatal("corrupt snapshot did not fall back to authoritative range JSON")
	}
}

func TestStoreTerminalBatchRefreshesSnapshot(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	batch := &BatchJob{ID: "b1", ChainKey: "bsc", Status: BatchRunning, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveBatch(batch); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRange(&RangeJob{ID: "r1", BatchID: "b1", FromBlock: 1, ToBlock: 10, Status: RangeCompleted, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	batch.Status = BatchCompleted
	batch.UpdatedAt = time.Now().UTC()
	if err := store.SaveBatch(batch); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.loadedFromSnapshot {
		t.Fatal("terminal batch did not refresh snapshot")
	}
	if got := reloaded.GetBatch("b1"); got == nil || got.Status != BatchCompleted {
		t.Fatalf("terminal batch snapshot mismatch: %#v", got)
	}
}

func writeStoreTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
}

func replaceStoreTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	tmp := path + ".replacement"
	writeStoreTestJSON(t, tmp, value)
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}
