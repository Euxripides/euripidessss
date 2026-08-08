package smartdownload

import (
	"path/filepath"
	"testing"
)

func TestSplitBlockRange(t *testing.T) {
	got := SplitBlockRange(40_000_000, 40_149_999, 50_000)
	if len(got) != 3 {
		t.Fatalf("want 3 ranges, got %d: %v", len(got), got)
	}
	if got[0] != (BlockRange{From: 40_000_000, To: 40_049_999}) {
		t.Fatalf("first range mismatch: %+v", got[0])
	}
	if got[2] != (BlockRange{From: 40_100_000, To: 40_149_999}) {
		t.Fatalf("last range mismatch: %+v", got[2])
	}
}

func TestCheckpointCompleteEmptyRemaining(t *testing.T) {
	cp := &CheckpointV3{}
	cp.Init("ds1", "0xabc", "transactions", BlockRange{From: 0, To: 199}, 100)
	if len(cp.PendingRanges) != 2 {
		t.Fatalf("want 2 pending, got %d", len(cp.PendingRanges))
	}
	cp.CompleteRange(cp.PendingRanges[0], &PartInfo{Name: "part-000001.jsonl", Rows: 10, SHA256: "s1", RangeFrom: 0, RangeTo: 99})
	cp.ConfirmEmpty(BlockRange{From: 100, To: 199})
	if len(cp.Remaining()) != 0 {
		t.Fatalf("want no remaining, got %v", cp.Remaining())
	}
	if cp.RowsCommitted != 10 {
		t.Fatalf("rows committed = %d, want 10", cp.RowsCommitted)
	}
	if cp.NextPartName(".jsonl") != "part-000002.jsonl" {
		t.Fatalf("next part name = %s", cp.NextPartName(".jsonl"))
	}
	if !cp.IsRangeDone(BlockRange{From: 0, To: 99}) || !cp.IsRangeDone(BlockRange{From: 100, To: 199}) {
		t.Fatalf("ranges should be done")
	}
}

func TestCheckpointStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)
	cp := &CheckpointV3{}
	cp.Init("ds-x", "0xabc", "logs", BlockRange{From: 10, To: 59}, 50)
	cp.CompleteRange(cp.PendingRanges[0], &PartInfo{Name: "part-000001.jsonl", Rows: 7, SHA256: "sha"})
	if err := store.Save(cp); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("ds-x")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 3 || loaded.RowsCommitted != 7 || len(loaded.CompletedRanges) != 1 {
		t.Fatalf("loaded checkpoint mismatch: %+v", loaded)
	}
	if loaded.RequestedFrom != 10 || loaded.RequestedTo != 59 {
		t.Fatalf("requested range mismatch")
	}
	_ = filepath.Join(dir)
}
