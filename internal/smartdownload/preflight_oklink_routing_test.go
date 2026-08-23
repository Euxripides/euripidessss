package smartdownload

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/etl/backend/internal/cryptodownload"
)

func TestPreflightOKLinkCountRoutesSmallCSVAndLargeSQD(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rows     int64
		provider string
	}{
		{name: "small", rows: 9_999, provider: "csv"},
		{name: "large", rows: 10_001, provider: "sqd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := NewStore(root)
			if err != nil {
				t.Fatal(err)
			}
			opts := DefaultOptions()
			opts.AdaptiveRanges = tc.provider == "csv"
			svc := NewService(store, opts, NewJSONLPartWriter(root))
			t.Cleanup(svc.Shutdown)
			csv := configuredCSVAdapter(cryptodownload.ExportData{})
			calls := 0
			csv.countRows = func(context.Context, cryptodownload.Config, string, string) (int64, error) {
				calls++
				return tc.rows, nil
			}
			svc.RegisterAdapter(csv)
			svc.RegisterAdapter(NewMockNamedProvider("sqd"))
			req := CreateBatchRequest{ChainKey: "bsc", Addresses: []string{addrA},
				Datasets:     []string{DatasetTransactions},
				DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 0, ToBlock: 1_000}}
			preflight, err := svc.Preflight(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if len(preflight.DatasetEstimates) != 1 {
				t.Fatalf("dataset estimates=%d, want 1", len(preflight.DatasetEstimates))
			}
			estimate := preflight.DatasetEstimates[0]
			if estimate.Source != "oklink_count" || estimate.EstimatedRows != uint64(tc.rows) || estimate.PreferredProvider != tc.provider {
				t.Fatalf("unexpected preflight route: %+v", estimate)
			}
			created, err := svc.CreateBatch(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			datasets := store.ListDatasets()
			if len(datasets) != 1 || datasets[0].PreferredProvider != tc.provider || datasets[0].EstimatedRows != uint64(tc.rows) {
				t.Fatalf("created dataset did not reuse preflight: %+v", datasets)
			}
			if calls != 1 {
				t.Fatalf("OKLink count calls=%d, create must reuse preflight cache", calls)
			}
			if created.RangeJobs == 0 {
				t.Fatal("created task has no ranges")
			}
			if tc.provider == "csv" && created.RangeJobs != 1 {
				t.Fatalf("CSV task range jobs=%d, want one all-address export window", created.RangeJobs)
			}
		})
	}
}

func TestSingleAddressFullCreationUsesBatchPackForThousandsOfRanges(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.RangeChunkSize = 50_000
	svc := NewService(store, opts, NewJSONLPartWriter(root))
	t.Cleanup(svc.Shutdown)
	svc.RegisterAdapter(NewMockNamedProvider("sqd"))
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions},
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 0, ToBlock: 150_000_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.RangeJobs <= packThreshold {
		t.Fatalf("range jobs=%d, want > pack threshold", created.RangeJobs)
	}
	if _, err := os.Stat(filepath.Join(root, "packs", created.Batch.ID+".json")); err != nil {
		t.Fatalf("batch pack missing: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "ranges"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("creation wrote %d individual range files instead of one pack", len(entries))
	}
}
