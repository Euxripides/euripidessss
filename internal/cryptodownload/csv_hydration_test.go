package cryptodownload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHydrateCSVKindAllowsCustomRangeWhenKindHasNoLegacySegments(t *testing.T) {
	rawDir := t.TempDir()
	cfg := Config{
		Address:      "0x57136ea9b2be6cd4ad74c3ca5b24172f87c9cb8d",
		CSVStartTime: 1785024000,
		CSVEndTime:   1785129327,
	}
	store, err := NewCSVCheckpointStore(rawDir, "bsc", cfg.Address, csvRawFingerprint(cfg, "bsc"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	client := &CSVExportClient{rawDir: rawDir}
	kind := csvExportKind{Name: "token_transfers", Endpoint: "tokenTransfer", Sheet: "token", TimeRange: true}

	hydrated, err := client.hydrateCSVKind(cfg, "bsc", kind, cfg.CSVStartTime, cfg.CSVEndTime)
	if err != nil {
		t.Fatalf("unexpected hydration error: %v", err)
	}
	if hydrated.NextSegment != 1 || hydrated.NextEndExclusive != cfg.CSVEndTime {
		t.Fatalf("unexpected empty hydration state: %+v", hydrated)
	}
}

func TestMarkCSVKindCheckpointCompleteSkipsFutureResume(t *testing.T) {
	rawDir := t.TempDir()
	cfg := Config{
		Address:      "0x57136ea9b2be6cd4ad74c3ca5b24172f87c9cb8d",
		CSVStartTime: 1785024000,
		CSVEndTime:   1785129327,
	}
	client := &CSVExportClient{rawDir: rawDir}
	kind := csvExportKind{Name: "transactions", Endpoint: "normalTransaction", Sheet: "transaction", TimeRange: true}
	if err := client.markCSVKindCheckpointComplete(cfg, "bsc", kind); err != nil {
		t.Fatalf("mark complete failed: %v", err)
	}

	hydrated, err := client.hydrateCSVKind(cfg, "bsc", kind, cfg.CSVStartTime, cfg.CSVEndTime)
	if err != nil {
		t.Fatalf("hydrate complete checkpoint failed: %v", err)
	}
	if hydrated.NextEndExclusive != cfg.CSVStartTime {
		t.Fatalf("expected completed kind to skip resume, got next end %d", hydrated.NextEndExclusive)
	}
}
