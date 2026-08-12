package smartdownload

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func TestCanceledTreeRemainsCanceledAfterReconcile(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions},
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 10, ToBlock: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	svc.cancelBatchLocked(resp.Batch.ID)
	svc.mu.Unlock()
	if err := svc.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.GetBatch(resp.Batch.ID); got.Status != BatchCanceled {
		t.Fatalf("canceled batch flipped to %s", got.Status)
	}
	addresses := store.ListAddressesByBatch(resp.Batch.ID)
	if len(addresses) != 1 || addresses[0].Status != AddressCanceled {
		t.Fatalf("canceled address tree changed: %+v", addresses)
	}
}

func TestMixedCompletedAndCanceledAddressesSettlePartial(t *testing.T) {
	store, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA, addrB}, Datasets: []string{DatasetTransactions},
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 10, ToBlock: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	addresses := store.ListAddressesByBatch(resp.Batch.ID)
	addresses[0].Status, addresses[0].FinishedAt = AddressCompleted, &now
	addresses[1].Status, addresses[1].FinishedAt = AddressCanceled, &now
	if err := store.SaveAddress(addresses[0]); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAddress(addresses[1]); err != nil {
		t.Fatal(err)
	}
	if !svc.trySettle(resp.Batch.ID) {
		t.Fatal("terminal child tree did not settle")
	}
	if got := store.GetBatch(resp.Batch.ID); got.Status != BatchPartial {
		t.Fatalf("mixed completed/canceled batch = %s, want PARTIAL", got.Status)
	}
}

func TestPauseWaitingAddressTransitionsSynchronously(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.DefaultEndBlock = 20
	svc := NewService(store, opts, NewJSONLPartWriter(root))
	svc.RegisterAdapter(NewSlowMockProvider(2 * time.Second))
	t.Cleanup(svc.Shutdown)
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTransactions},
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 10, ToBlock: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := store.ListAddressesByBatch(resp.Batch.ID)[0]
	paused, err := svc.PauseAddress(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Status != AddressPaused || paused.PauseRequested {
		t.Fatalf("waiting pause did not settle synchronously: %+v", paused)
	}
	for _, ds := range store.ListDatasetsByAddress(a.ID) {
		if ds.Status != DatasetPaused || ds.PauseRequested {
			t.Fatalf("child dataset not paused: %+v", ds)
		}
	}
	if _, err := svc.ResumeAddress(a.ID); err != nil {
		t.Fatal(err)
	}
}

func TestLocalHitUsesAuthoritativeRowCount(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.DefaultEndBlock = 94_810_000
	svc := NewService(store, opts, NewJSONLPartWriter(root))
	t.Cleanup(svc.Shutdown)
	artifact := filepath.Join(root, "authoritative.parquet")
	if err := os.WriteFile(artifact, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := []*IndexedResult{{
		ChunkKey: "existing", DatasetJobID: "existing-dataset", ChainKey: "bsc", ChainID: 56,
		Dataset: DatasetTokenTransfers, Address: addrA, FromBlock: 94_800_000, ToBlock: 94_810_000,
		RowCount: 1135, MergedParquet: artifact, Validation: "VALIDATED", Certification: "CERTIFIED",
		IndexedAt: time.Now().UTC(),
	}}
	payload, _ := json.Marshal(entry)
	if err := os.MkdirAll(filepath.Dir(svc.results.path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.results.path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	skip := true
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetTokenTransfers}, SkipCovered: &skip,
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 94_800_000, ToBlock: 94_810_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.LocalFullHits != 1 {
		t.Fatalf("local full hits = %d", resp.LocalFullHits)
	}
	ds := store.ListDatasetsByAddress(store.ListAddressesByBatch(resp.Batch.ID)[0].ID)[0]
	if ds.CurrentProvider != "local_hit" || ds.DownloadedRows != 1135 || ds.Progress.RowsCurrent != 1135 {
		t.Fatalf("local hit lost authoritative rows: %+v", ds)
	}
	if ds.Status != DatasetCompleted || ds.Certification != CertificationDataset || !validationReadyForCertification(ds.Validation) {
		t.Fatalf("authoritative local hit was not certified consistently: %+v", ds)
	}
	if ds.Validation.DatasetJobID != ds.ID || ds.Validation.Rows != 1135 || ds.Validation.RawRows != 1135 ||
		ds.Validation.UniqueKeyCount != 1135 || ds.Validation.Score != 1 || ds.Validation.CrossCheck.Status != "PASS" ||
		ds.Validation.ValidatedAt.IsZero() {
		t.Fatalf("authoritative local hit validation report is incomplete: %+v", ds.Validation)
	}
	if got := len(svc.results.List()); got != 1 {
		t.Fatalf("local hit created duplicate registry version: %d", got)
	}
	address := store.GetAddress(ds.AddressJobID)
	if address.Status != AddressCompleted || address.Progress.Percent != 1 {
		t.Fatalf("completed local-hit address has contradictory progress: %+v", address)
	}
}

func TestPreflightRejectsDatasetWithoutExecutableProvider(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	t.Cleanup(svc.Shutdown)
	_, err = svc.Preflight(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Addresses: []string{addrA}, Datasets: []string{DatasetNFTTransfers},
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 10, ToBlock: 20},
	})
	if err == nil {
		t.Fatal("unsupported dataset preflight unexpectedly passed")
	}
}

func TestCapabilityReadDoesNotBlockOnLifecycleMutex(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	svc.RegisterAdapter(NewMockProvider())
	t.Cleanup(svc.Shutdown)

	svc.mu.Lock()
	done := make(chan []string, 1)
	go func() { done <- svc.AvailableDatasets("bsc", DownloadModeAuto) }()
	select {
	case datasets := <-done:
		if len(datasets) == 0 {
			t.Fatal("registered executable provider was not reflected in capability snapshot")
		}
	case <-time.After(250 * time.Millisecond):
		svc.mu.Unlock()
		t.Fatal("capability read blocked on lifecycle mutex")
	}
	svc.mu.Unlock()
}

func TestMergeDatasetDoesNotCertifyPartialValidation(t *testing.T) {
	svc, dsID := indexedFixture(t, nil)
	ds := svc.store.GetDataset(dsID)
	ds.Validation = &ValidationReport{Status: "PARTIAL", Coverage: 0.5, BlockCoverage: 0.5}
	if err := svc.store.SaveDataset(ds); err != nil {
		t.Fatal(err)
	}
	entry, err := svc.results.MergeDataset(context.Background(), dsID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Certification == "CERTIFIED" || entry.Validation == "VALIDATED" {
		t.Fatalf("partial validation was certified: %+v", entry)
	}
}

func TestDBWriteFailureIsNotReusableCoverage(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	t.Cleanup(svc.Shutdown)
	svc.results.mu.Lock()
	payload, _ := json.Marshal([]*IndexedResult{{
		DatasetJobID: "failed-write", ChainKey: "bsc", Dataset: DatasetLogs, Address: addrA,
		FromBlock: 10, ToBlock: 20, RowCount: 10, Validation: "VALIDATED",
		Certification: "DB_WRITE_FAILED", IndexedAt: time.Now().UTC(),
	}})
	if err := os.MkdirAll(filepath.Dir(svc.results.path), 0o755); err != nil {
		svc.results.mu.Unlock()
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.results.path, payload, 0o644); err != nil {
		svc.results.mu.Unlock()
		t.Fatal(err)
	}
	svc.results.mu.Unlock()
	if covered := svc.registryCoverage("bsc", addrA, DatasetLogs, 10, 20); len(covered) != 0 {
		t.Fatalf("DB_WRITE_FAILED result reused as certified coverage: %+v", covered)
	}
}

func TestCertifiedEmptyEvidenceIsReusableButUnevidencedZeroIsNot(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	t.Cleanup(svc.Shutdown)
	now := time.Now().UTC()
	ds := &DatasetJob{ID: "empty-evidence", Dataset: DatasetInternalTransactions, Status: DatasetCompleted,
		RequestedRange: RangeSpec{Mode: RangeModeBlock, FromBlock: 10, ToBlock: 20},
		Validation:     &ValidationReport{Status: "VALIDATED", Coverage: 1, BlockCoverage: 1}, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveDataset(ds); err != nil {
		t.Fatal(err)
	}
	rangeJob := &RangeJob{ID: "empty-range", DatasetJobID: ds.ID, Dataset: ds.Dataset, FromBlock: 10, ToBlock: 20,
		Status: RangeEmpty, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveRange(rangeJob); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRange(&RangeJob{ID: "failed-parallel-lane", DatasetJobID: ds.ID, Dataset: ds.Dataset,
		FromBlock: 10, ToBlock: 20, Status: RangeFailed, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	svc.certifyRangeLocked(rangeJob)
	svc.mu.Unlock()
	if ds = store.GetDataset(ds.ID); ds.Certification != CertificationDatasetPartial {
		t.Fatalf("range evidence prematurely produced full dataset certification: %+v", ds)
	}
	entries := []*IndexedResult{
		{DatasetJobID: ds.ID, ChainKey: "bsc", Dataset: ds.Dataset, Address: addrA, FromBlock: 10, ToBlock: 20,
			Validation: "VALIDATED", Certification: "CERTIFIED", IndexedAt: now},
		{DatasetJobID: "legacy-zero", ChainKey: "bsc", Dataset: ds.Dataset, Address: addrB, FromBlock: 10, ToBlock: 20,
			Validation: "VALIDATED", Certification: "CERTIFIED", IndexedAt: now},
	}
	payload, _ := json.Marshal(entries)
	if err := os.MkdirAll(filepath.Dir(svc.results.path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.results.path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if covered := svc.registryCoverage("bsc", addrA, ds.Dataset, 10, 20); len(covered) != 1 {
		t.Fatalf("certified EMPTY evidence was not reusable: %+v", covered)
	}
	if covered := svc.registryCoverage("bsc", addrB, ds.Dataset, 10, 20); len(covered) != 0 {
		t.Fatalf("unevidenced legacy zero was reused: %+v", covered)
	}
}

func TestCSVToXLSXSetsAuditableColumnWidths(t *testing.T) {
	root := t.TempDir()
	csvPath := filepath.Join(root, "input.csv")
	xlsxPath := filepath.Join(root, "output.xlsx")
	if err := os.WriteFile(csvPath, []byte("transaction_hash,from_address,value_raw\n0x1234567890abcdef,0x1111111111111111111111111111111111111111,246122\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := csvToXLSX(csvPath, xlsxPath); err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenFile(xlsxPath)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	for _, col := range []string{"A", "B"} {
		width, err := book.GetColWidth("Sheet1", col)
		if err != nil {
			t.Fatal(err)
		}
		if width < 18 {
			t.Fatalf("column %s width=%v, want readable width", col, width)
		}
	}
}

func TestCSVToXLSXPersistsExactStreamingDimension(t *testing.T) {
	root := t.TempDir()
	csvPath := filepath.Join(root, "input.csv")
	xlsxPath := filepath.Join(root, "output.xlsx")
	file, err := os.Create(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	w := csv.NewWriter(file)
	header := make([]string, 13)
	for i := range header {
		header[i] = fmt.Sprintf("column_%d", i+1)
	}
	if err := w.Write(header); err != nil {
		t.Fatal(err)
	}
	for row := 0; row < 1135; row++ {
		values := make([]string, 13)
		for col := range values {
			values[col] = fmt.Sprintf("r%d-c%d", row+1, col+1)
		}
		if err := w.Write(values); err != nil {
			t.Fatal(err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := csvToXLSX(csvPath, xlsxPath); err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenFile(xlsxPath)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	dimension, err := book.GetSheetDimension("Sheet1")
	if err != nil {
		t.Fatal(err)
	}
	if dimension != "A1:M1136" {
		t.Fatalf("sheet dimension = %s, want A1:M1136", dimension)
	}
	rows, err := book.Rows("Sheet1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Error(); err != nil {
		t.Fatal(err)
	}
	if count != 1136 {
		t.Fatalf("streamed row count = %d, want 1136", count)
	}
}

func TestResultQueryRejectsInvalidFilterAndSortWithoutSQLLeak(t *testing.T) {
	svc, dsID := indexedFixture(t, nil)
	for _, tc := range []struct {
		name, sort, filter string
	}{
		{name: "invalid filter column", filter: "not_a_column:value"},
		{name: "malformed filter", filter: "transaction_hash"},
		{name: "empty filter value", filter: "transaction_hash:"},
		{name: "invalid sort column", sort: "not_a_column"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := svc.results.QueryResults(context.Background(), dsID, 1, 20, tc.sort, tc.filter)
			if err == nil || !IsResultQueryParamError(err) {
				t.Fatalf("invalid query was not a typed parameter error: %v", err)
			}
			lower := strings.ToLower(err.Error())
			if strings.Contains(lower, "binder") || strings.Contains(lower, "select ") || strings.Contains(lower, "read_parquet") {
				t.Fatalf("query error leaked SQL internals: %v", err)
			}
		})
	}
}
