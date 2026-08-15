package control

import (
	"path/filepath"
	"testing"
)

func TestAddressLibraryUpsertAndList(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	addresses := []string{
		"0x0000000000000000000000000000000000000011",
		"0x0000000000000000000000000000000000000012",
	}
	if got, err := store.UpsertAddressAssets("bsc", 56, addresses, "file", "addresses.txt"); err != nil || got != 2 {
		t.Fatalf("first upsert got=%d err=%v", got, err)
	}
	if got, err := store.UpsertAddressAssets("bsc", 56, addresses[:1], "file", "again.txt"); err != nil || got != 1 {
		t.Fatalf("second upsert got=%d err=%v", got, err)
	}
	if got, err := store.EnsureAddressAssets("bsc", 56, addresses, "smart-download-history"); err != nil || got != 0 {
		t.Fatalf("history backfill must be idempotent got=%d err=%v", got, err)
	}
	items, total, err := store.ListAddressAssets("bsc", "001", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("unexpected list total=%d items=%d", total, len(items))
	}
	for _, item := range items {
		if item.Address == addresses[0] && item.ImportCount != 2 {
			t.Fatalf("expected import count 2, got %+v", item)
		}
	}
}
