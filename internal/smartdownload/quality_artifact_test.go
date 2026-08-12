package smartdownload

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func qualityRecord(block uint64, hashDigit string, logIndex uint64) Record {
	return Record{
		ChainID: 56, BlockNumber: block, TransactionHash: "0x" + strings.Repeat(hashDigit, 64),
		LogIndex: logIndex, Dataset: DatasetTokenTransfers,
		Payload: map[string]any{
			"token_address": "0x" + strings.Repeat("a", 40),
			"from_address":  "0x" + strings.Repeat("b", 40),
			"to_address":    "0x" + strings.Repeat("c", 40),
			"value_raw":     "1",
		},
	}
}

func TestPartWriterDeduplicatesCanonicalEvents(t *testing.T) {
	writer := NewJSONLPartWriter(t.TempDir())
	record := qualityRecord(105, "1", 7)
	written, err := writer.WritePart(context.Background(), PartMeta{
		DatasetJobID: "dataset", PartName: "part.jsonl", FromBlock: 100, ToBlock: 110,
	}, []Record{record, record})
	if err != nil {
		t.Fatal(err)
	}
	if written.Rows != 1 {
		t.Fatalf("written rows=%d, want unique rows=1", written.Rows)
	}
	records, err := ReadPartRecords(written.Path)
	if err != nil || len(records) != 1 {
		t.Fatalf("part records=%d err=%v", len(records), err)
	}
}

func TestValidationRejectsOverlappingDuplicatePartsAndReconcilesRows(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	writer := NewJSONLPartWriter(root)
	svc := NewService(store, DefaultOptions(), writer)
	t.Cleanup(svc.Shutdown)
	now := time.Now().UTC()
	ds := &DatasetJob{ID: "duplicate-dataset", Address: "0x" + strings.Repeat("d", 40), ChainKey: "bsc",
		Dataset: DatasetTokenTransfers, Status: DatasetValidating,
		RequestedRange: RangeSpec{Mode: RangeModeBlock, FromBlock: 100, ToBlock: 120}, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveDataset(ds); err != nil {
		t.Fatal(err)
	}
	record := qualityRecord(105, "1", 7)
	w1, err := writer.WritePart(context.Background(), PartMeta{DatasetJobID: ds.ID, PartName: "part-1.jsonl", FromBlock: 100, ToBlock: 110}, []Record{record})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := writer.WritePart(context.Background(), PartMeta{DatasetJobID: ds.ID, PartName: "part-2.jsonl", FromBlock: 100, ToBlock: 120}, []Record{record})
	if err != nil {
		t.Fatal(err)
	}
	cp := &CheckpointV3{DatasetJobID: ds.ID, Dataset: ds.Dataset, Address: ds.Address,
		RequestedFrom: 100, RequestedTo: 120, RowsCommitted: 2,
		CompletedRanges: []BlockRange{{From: 100, To: 110}, {From: 100, To: 120}},
		Parts: []PartInfo{
			{Name: "part-1.jsonl", SHA256: w1.SHA256, Rows: w1.Rows, Bytes: w1.Bytes, RangeFrom: 100, RangeTo: 110},
			{Name: "part-2.jsonl", SHA256: w2.SHA256, Rows: w2.Rows, Bytes: w2.Bytes, RangeFrom: 100, RangeTo: 120},
		}}
	if err := svc.cp.Save(cp); err != nil {
		t.Fatal(err)
	}
	for _, part := range cp.Parts {
		if err := NewLedger(root, ds.ID).Append(LedgerEntry{Event: LedgerPartCommitted, DatasetJobID: ds.ID,
			FromBlock: part.RangeFrom, ToBlock: part.RangeTo, Part: part.Name, SHA256: part.SHA256, Rows: part.Rows}); err != nil {
			t.Fatal(err)
		}
	}
	report, err := NewValidator(svc).ValidateDataset(context.Background(), ds.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "FAILED" || report.DuplicateCount != 1 || report.UniqueKeyCount != 1 {
		t.Fatalf("duplicate validation escaped: %+v", report)
	}
	if report.Coverage != 1 || report.BlockCoverage != 1 {
		t.Fatalf("overlapping completed ranges should use interval coverage: %+v", report)
	}
	reconciled, err := svc.cp.Load(ds.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.RowsCommitted != 1 {
		t.Fatalf("checkpoint rows=%d, want unique rows=1", reconciled.RowsCommitted)
	}

	ds = store.GetDataset(ds.ID)
	ds.Status = DatasetIndexing
	ds.Validation = &ValidationReport{Status: "VALIDATED", Coverage: 1, BlockCoverage: 1}
	if err := store.SaveDataset(ds); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.results.MergeDataset(context.Background(), ds.ID); err == nil || !strings.Contains(err.Error(), "重复唯一键") {
		t.Fatalf("merge accepted duplicate parts: %v", err)
	}
}

func TestCertificationReadinessFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		report *ValidationReport
	}{
		{"partial", &ValidationReport{Status: "PARTIAL", Coverage: 1, BlockCoverage: 1}},
		{"row-coverage-zero", &ValidationReport{Status: "VALIDATED", Coverage: 0, BlockCoverage: 1}},
		{"block-coverage-partial", &ValidationReport{Status: "VALIDATED", Coverage: 1, BlockCoverage: .5}},
		{"duplicate", &ValidationReport{Status: "VALIDATED", Coverage: 1, BlockCoverage: 1, DuplicateCount: 1}},
		{"duplicate-part", &ValidationReport{Status: "VALIDATED", Coverage: 1, BlockCoverage: 1, PartsDuplicateSHA: 1}},
		{"missing", &ValidationReport{Status: "VALIDATED", Coverage: 1, BlockCoverage: 1, MissingRanges: []BlockRange{{From: 1, To: 1}}}},
		{"artifact-error", &ValidationReport{Status: "VALIDATED", Coverage: 1, BlockCoverage: 1, Errors: []string{"missing file"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if validationReadyForCertification(tc.report) {
				t.Fatalf("invalid report was certification-ready: %+v", tc.report)
			}
		})
	}
	if !validationReadyForCertification(&ValidationReport{Status: "VALIDATED", Coverage: 1, BlockCoverage: 1}) {
		t.Fatal("clean complete report was rejected")
	}
}

func TestMergeInputVerificationRejectsMissingOutOfRangeAndUnevidencedEmpty(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	writer := NewJSONLPartWriter(root)
	svc := NewService(store, DefaultOptions(), writer)
	t.Cleanup(svc.Shutdown)
	ds := &DatasetJob{ID: "artifact-boundary", Dataset: DatasetTokenTransfers, DownloadedRows: 1}

	missing := &CheckpointV3{DatasetJobID: ds.ID, RequestedFrom: 100, RequestedTo: 120, RowsCommitted: 1,
		Parts: []PartInfo{{Name: "missing.jsonl", SHA256: "missing", Rows: 1, RangeFrom: 100, RangeTo: 120}}}
	if _, err := svc.results.verifyMergeInputs(context.Background(), ds, missing, true); err == nil || !strings.Contains(err.Error(), "缺失") {
		t.Fatalf("missing artifact was accepted: %v", err)
	}

	record := qualityRecord(121, "3", 0)
	written, err := writer.WritePart(context.Background(), PartMeta{DatasetJobID: ds.ID, PartName: "out.jsonl", FromBlock: 100, ToBlock: 130}, []Record{record})
	if err != nil {
		t.Fatal(err)
	}
	outOfRange := &CheckpointV3{DatasetJobID: ds.ID, RequestedFrom: 100, RequestedTo: 120, RowsCommitted: 1,
		Parts: []PartInfo{{Name: "out.jsonl", SHA256: written.SHA256, Rows: 1, Bytes: written.Bytes, RangeFrom: 100, RangeTo: 130}}}
	if _, err := svc.results.verifyMergeInputs(context.Background(), ds, outOfRange, true); err == nil || !strings.Contains(err.Error(), "超出请求") {
		t.Fatalf("out-of-range artifact was accepted: %v", err)
	}

	ds.DownloadedRows = 0
	empty := &CheckpointV3{DatasetJobID: ds.ID, RequestedFrom: 100, RequestedTo: 120}
	if _, err := svc.results.verifyMergeInputs(context.Background(), ds, empty, true); err == nil || !strings.Contains(err.Error(), "空区间证据") {
		t.Fatalf("unevidenced empty dataset was accepted: %v", err)
	}
	empty.ConfirmedEmptyRanges = []BlockRange{{From: 100, To: 120}}
	if rows, err := svc.results.verifyMergeInputs(context.Background(), ds, empty, true); err != nil || rows != 0 {
		t.Fatalf("fully evidenced empty dataset was rejected: rows=%d err=%v", rows, err)
	}
}

func TestImportTextSkipsHeaderBlankTailAndCountsExactly(t *testing.T) {
	a := "0x" + strings.Repeat("1", 40)
	b := "0x" + strings.Repeat("2", 40)
	result, err := importText(bytes.NewBufferString("address\r\n" + a + "\r\n" + a + "\r\ninvalid\r\n" + b + "\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 4 || result.Valid != 3 || result.Duplicates != 1 || result.Invalid != 1 {
		t.Fatalf("TXT counters are not data-row exact: %+v", result)
	}
	if result.DetectedColumns[0].NonEmpty != 4 || result.DetectedColumns[0].Confidence != .75 {
		t.Fatalf("TXT column profile incorrect: %+v", result.DetectedColumns[0])
	}
}

func TestAnalyzeColumnsSkipsHeaderAndBlankRows(t *testing.T) {
	a := "0x" + strings.Repeat("1", 40)
	result, err := analyzeColumns([][]string{{"address"}, {a}, {""}, {a}, {"bad"}, nil})
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 3 || result.Valid != 2 || result.Duplicates != 1 || result.Invalid != 1 {
		t.Fatalf("tabular counters are not data-row exact: %+v", result)
	}
}

func TestQueryParquetNoMatchReturnsEmptySuccess(t *testing.T) {
	engine := openTestDuckDB(t)
	if engine == nil {
		t.Skip("DuckDB unavailable")
	}
	root := t.TempDir()
	path := filepath.Join(root, "result.parquet")
	_, err := engine.ExecSQL(context.Background(), "COPY (SELECT '0xabc'::VARCHAR AS transaction_hash, 1::BIGINT AS block_number) TO '"+
		filepath.ToSlash(path)+"' (FORMAT PARQUET)")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(root))
	svc.SetDuckDB(engine)
	t.Cleanup(svc.Shutdown)
	rows, total, err := svc.results.queryParquet(context.Background(), path, 1, 20, "block_number", "transaction_hash:0xmissing")
	if err != nil {
		t.Fatal(err)
	}
	if rows == nil || len(rows) != 0 || total != 0 {
		t.Fatalf("no-match result must be []/0/nil: rows=%v total=%d", rows, total)
	}
}
