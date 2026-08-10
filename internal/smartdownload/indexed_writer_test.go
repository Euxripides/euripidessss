package smartdownload

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	v3 "github.com/etl/backend/internal/smartdownload/validation"
)

type fakeIndexedWriter struct {
	mu     sync.Mutex
	fail   bool
	calls  int
	result IndexedWriteResult
	reqs   []IndexedWriteRequest
}

func (f *fakeIndexedWriter) WriteIndexed(_ context.Context, req IndexedWriteRequest) (IndexedWriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.reqs = append(f.reqs, req)
	if f.fail {
		return f.result, fmt.Errorf("injected clickhouse failure")
	}
	return f.result, nil
}

func (f *fakeIndexedWriter) setFail(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = v
}

func indexedFixture(t *testing.T, writer IndexedWriter) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	batch := &BatchJob{ID: "b1", ChainKey: "bsc", ChainID: 56, Status: BatchRunning, CreatedAt: now, UpdatedAt: now}
	address := &AddressJob{ID: "a1", BatchID: batch.ID, Address: "0xabc", ChainKey: "bsc", ChainID: 56,
		Status: AddressDownloading, CreatedAt: now, UpdatedAt: now}
	ds := &DatasetJob{ID: "d1", BatchID: batch.ID, AddressJobID: address.ID, Address: address.Address,
		ChainKey: "bsc", Dataset: DatasetTransactions, Status: DatasetIndexing, CurrentProvider: "sqd",
		DownloadedRows: 2,
		CreatedAt:      now, UpdatedAt: now, Validation: &ValidationReport{Status: "PASS", Coverage: 1}}
	if err := store.SaveBatch(batch); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAddress(address); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDataset(ds); err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), nil)
	svc.SetIndexedWriter(writer)
	if err := svc.cp.Save(&CheckpointV3{DatasetJobID: ds.ID, Dataset: ds.Dataset, Address: ds.Address,
		RequestedFrom: 10, RequestedTo: 20}); err != nil {
		t.Fatal(err)
	}
	certStore := v3.NewGapStore(root, ds.ID)
	if err := certStore.SaveCertificate(&v3.Certificate{DatasetJobID: ds.ID, Status: "PASS", Coverage: 1, CertifiedAt: now}); err != nil {
		t.Fatal(err)
	}
	return svc, ds.ID
}

func TestIndexedWriterSuccessCompletesAndFiresCallback(t *testing.T) {
	writer := &fakeIndexedWriter{result: IndexedWriteResult{InputRows: 2, InsertedRows: 2, ActivityRows: 4}}
	svc, dsID := indexedFixture(t, writer)
	callbacks := 0
	svc.SetOnDatasetIndexed(func(*IndexedResult) { callbacks++ })
	svc.indexDataset(dsID)
	if ds := svc.store.GetDataset(dsID); ds.Status != DatasetCompleted {
		t.Fatalf("dataset = %+v", ds)
	}
	if callbacks != 1 {
		t.Fatalf("callbacks = %d, want 1", callbacks)
	}
	entries := svc.results.List()
	if len(entries) != 1 || entries[0].Certification != "CERTIFIED" || entries[0].Writer == nil {
		t.Fatalf("registry = %+v", entries)
	}
}

func TestRawLogsAreWarehouseDataset(t *testing.T) {
	if !isWarehouseDataset(DatasetLogs) {
		t.Fatal("certified raw logs must be decoded and indexed before downstream price rebuild")
	}
}

func TestIndexedWriterFailureIsRetryableWithoutDownload(t *testing.T) {
	writer := &fakeIndexedWriter{fail: true, result: IndexedWriteResult{InputRows: 2, InsertedRows: 2}}
	svc, dsID := indexedFixture(t, writer)
	callbacks := 0
	svc.SetOnDatasetIndexed(func(*IndexedResult) { callbacks++ })
	svc.indexDataset(dsID)
	if ds := svc.store.GetDataset(dsID); ds.Status != DatasetDBWriteFailed {
		t.Fatalf("dataset = %+v", ds)
	}
	if callbacks != 0 {
		t.Fatalf("callback fired on DB failure")
	}
	if entries := svc.results.List(); len(entries) != 1 || entries[0].Certification != "DB_WRITE_FAILED" {
		t.Fatalf("registry = %+v", entries)
	}
	cert, err := v3.NewGapStore(svc.store.Root(), dsID).LoadCertificate()
	if err != nil || cert.Status != "DB_WRITE_FAILED" {
		t.Fatalf("certificate = %+v err=%v", cert, err)
	}
	checkpointBefore, _ := svc.cp.Load(dsID)
	writer.setFail(false)
	if err := svc.RetryIndexedDataset(dsID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "DB write retry", func() bool {
		return svc.store.GetDataset(dsID).Status == DatasetCompleted
	})
	checkpointAfter, _ := svc.cp.Load(dsID)
	if checkpointBefore.UpdatedAt != checkpointAfter.UpdatedAt || len(svc.store.ListRangesByDataset(dsID)) != 0 {
		t.Fatalf("retry touched download checkpoint/ranges: before=%+v after=%+v", checkpointBefore, checkpointAfter)
	}
	if callbacks != 1 || writer.calls != 2 {
		t.Fatalf("callbacks=%d writer_calls=%d", callbacks, writer.calls)
	}
	if got := svc.results.List()[0]; got.Certification != "CERTIFIED" || got.WriteError != "" {
		t.Fatalf("retry registry = %+v", got)
	}
	cert, err = v3.NewGapStore(svc.store.Root(), dsID).LoadCertificate()
	if err != nil || cert.Status != "PASS" {
		t.Fatalf("retry certificate = %+v err=%v", cert, err)
	}
}

func TestRecoverAllRetriesDBWriteFailureWithoutRedownload(t *testing.T) {
	writer := &fakeIndexedWriter{fail: true, result: IndexedWriteResult{InputRows: 1, InsertedRows: 1}}
	svc, dsID := indexedFixture(t, writer)
	svc.indexDataset(dsID)
	writer.setFail(false)
	store2, err := NewStore(svc.store.Root())
	if err != nil {
		t.Fatal(err)
	}
	svc2 := NewService(store2, DefaultOptions(), nil)
	svc2.SetIndexedWriter(writer)
	if err := svc2.RecoverAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "RecoverAll DB write", func() bool {
		return svc2.store.GetDataset(dsID).Status == DatasetCompleted
	})
	if len(svc2.store.ListRangesByDataset(dsID)) != 0 {
		t.Fatal("RecoverAll created download ranges for DB-only retry")
	}
	if filepath.Base(svc2.results.List()[0].DatasetJobID) != dsID {
		t.Fatalf("unexpected registry: %+v", svc2.results.List())
	}
}
