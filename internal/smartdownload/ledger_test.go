package smartdownload

import "testing"

func TestLedgerAppendReplay(t *testing.T) {
	dir := t.TempDir()
	l := NewLedger(dir, "ds-1")
	for _, ev := range []string{LedgerRangeCreated, LedgerRangeStarted, LedgerPartCommitted, LedgerRangeCompleted} {
		if err := l.Append(LedgerEntry{Event: ev, DatasetJobID: "ds-1", RangeID: "0-99"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := l.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("want 4 entries, got %d", len(entries))
	}
	if entries[0].Event != LedgerRangeCreated || entries[3].Event != LedgerRangeCompleted {
		t.Fatalf("order broken: %+v", entries)
	}
}
