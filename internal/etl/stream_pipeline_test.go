package etl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/etl/backend/internal/model"
	"github.com/xuri/excelize/v2"
)

func TestUnifiedStreamStoreCleansDeduplicatesAndKeepsBoundedPreview(t *testing.T) {
	store, err := newUnifiedStreamStore(filepath.Join(t.TempDir(), "stream.sqlite"))
	if err != nil {
		t.Fatalf("new stream store: %v", err)
	}
	defer store.Close()

	row := model.TransactionRow{
		"交易时间": "1/1/24 00:04",
		"交易金额": "1,200.50元",
		"收付标志": "C",
		"交易卡号": "CNYO62220001",
		"数据来源": "fixture.csv:2",
	}
	if err := store.Add(row); err != nil {
		t.Fatalf("add row: %v", err)
	}
	if err := store.Add(model.TransactionRow{
		"交易时间": "2024-01-01 00:04:00",
		"交易金额": "1200.50",
		"收付标志": "进",
		"交易卡号": "62220001",
		"数据来源": "duplicate.csv:2",
	}); err != nil {
		t.Fatalf("add duplicate: %v", err)
	}
	if err := store.Add(model.TransactionRow{"交易时间": "", "交易金额": "1", "收付标志": "进"}); err != nil {
		t.Fatalf("add invalid row: %v", err)
	}
	if err := store.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if store.rowsIn != 3 || store.rowsOut != 1 || store.removedDuplicates != 1 || store.removedEmptyRequired != 1 {
		t.Fatalf("unexpected counts: in=%d out=%d duplicates=%d empty=%d",
			store.rowsIn, store.rowsOut, store.removedDuplicates, store.removedEmptyRequired)
	}
	if got := store.preview[0]["交易时间"]; got != "2024-01-01 00:04:00" {
		t.Fatalf("unexpected normalized datetime %q", got)
	}
	if got := store.preview[0]["数据来源"]; got != "fixture.csv:2" {
		t.Fatalf("unexpected source %q", got)
	}
}

func TestExportStreamStoreSplitsSheets(t *testing.T) {
	outputDir := t.TempDir()
	store, err := newUnifiedStreamStore(filepath.Join(outputDir, "stream.sqlite"))
	if err != nil {
		t.Fatalf("new stream store: %v", err)
	}
	defer store.Close()
	for i := 0; i < 3; i++ {
		row := model.TransactionRow{
			"交易时间":  "2024-01-01 00:00:0" + string(rune('0'+i)),
			"交易金额":  "1",
			"收付标志":  "进",
			"交易流水号": string(rune('A' + i)),
		}
		if err := store.Add(row); err != nil {
			t.Fatalf("add row %d: %v", i, err)
		}
	}
	if err := store.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	path, sheets, err := exportStreamStoreToExcelWithLimit(store.db, outputDir, "split", 2)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if sheets != 2 {
		t.Fatalf("expected 2 sheets, got %d", sheets)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat output: %v", err)
	}
	workbook, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer workbook.Close()
	if got := workbook.GetSheetList(); len(got) != 2 {
		t.Fatalf("expected 2 workbook sheets, got %v", got)
	}
}

func TestDeduplicateInputFilesByContent(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.csv")
	second := filepath.Join(dir, "second.csv")
	third := filepath.Join(dir, "third.csv")
	if err := os.WriteFile(first, []byte("same"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("same"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(third, []byte("diff"), 0644); err != nil {
		t.Fatal(err)
	}
	kept, skipped, err := deduplicateInputFiles([]string{second, third, first})
	if err != nil {
		t.Fatalf("deduplicate files: %v", err)
	}
	if len(kept) != 2 || skipped != 1 {
		t.Fatalf("unexpected result kept=%v skipped=%d", kept, skipped)
	}
}

func TestEnsureDataSourcePreservesCanonicalAndFallsBack(t *testing.T) {
	canonical := model.TransactionRow{"数据来源": "canonical.csv", "来源": "fallback.csv"}
	ensureDataSource(canonical)
	if canonical["数据来源"] != "canonical.csv" {
		t.Fatalf("canonical source overwritten: %#v", canonical)
	}
	fallback := model.TransactionRow{"来源文件": "bank.csv"}
	ensureDataSource(fallback)
	if fallback["数据来源"] != "bank.csv" {
		t.Fatalf("fallback source not mapped: %#v", fallback)
	}
}
