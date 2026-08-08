package smartdownload

import (
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
