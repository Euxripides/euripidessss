package etl

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"
	"github.com/etl/backend/internal/scanner"
	"github.com/rs/zerolog/log"
	"github.com/xuri/excelize/v2"
	_ "modernc.org/sqlite"
)

type duplicateInputFile struct {
	KeptPath      string
	DuplicatePath string
	SHA256        string
	Size          int64
}

const (
	streamPreviewLimit   = 1000
	excelMaxDataRows     = 1048575
	streamSQLiteCacheKiB = 65536
)

type unifiedStreamStore struct {
	db                    *sql.DB
	tx                    *sql.Tx
	insert                *sql.Stmt
	duplicateInsert       *sql.Stmt
	rejectedInsert        *sql.Stmt
	preview               []model.TransactionRow
	rowsIn                int
	rowsOut               int
	removedEmptyRequired  int
	removedFailedFeedback int
	removedBadDirection   int
	removedDuplicates     int
	inCount               int
	outCount              int
	totalIn               float64
	totalOut              float64
}

func runUnifiedStreamingPipeline(scan *scanner.DirectoryScan, outputDir, jobID string, startTime time.Time) (*model.PipelineResult, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}
	stageDir, err := os.MkdirTemp(outputDir, ".etl_stream_*")
	if err != nil {
		return nil, fmt.Errorf("create streaming stage: %w", err)
	}
	defer os.RemoveAll(stageDir)

	store, err := newUnifiedStreamStore(filepath.Join(stageDir, "transactions.sqlite"))
	if err != nil {
		return nil, err
	}
	defer store.Close()

	providerGroups := categorizeByProvider(scan)
	duplicateFiles := 0
	for i := range providerGroups {
		var skipped int
		providerGroups[i].Paths, skipped, err = deduplicateInputFiles(providerGroups[i].Paths)
		if err != nil {
			return nil, err
		}
		duplicateFiles += skipped
	}

	sort.Slice(providerGroups, func(i, j int) bool {
		return providerGroups[i].Provider < providerGroups[j].Provider
	})
	for _, group := range providerGroups {
		if err := streamProviderTransactions(group, outputDir, PipelineOptions{}, store.Add); err != nil {
			return nil, err
		}
	}
	if err := store.Commit(); err != nil {
		return nil, err
	}

	outputPath, sheetCount, err := exportStreamStoreToExcel(store.db, outputDir, jobID)
	if err != nil {
		return nil, fmt.Errorf("export streaming result: %w", err)
	}

	duration := time.Since(startTime)
	summary := map[string]interface{}{
		"rows_in":                 store.rowsIn,
		"rows_out":                store.rowsOut,
		"total_rows":              store.rowsOut,
		"duration_ms":             duration.Milliseconds(),
		"columns":                 UnifiedOutputColumns,
		"in_count":                store.inCount,
		"out_count":               store.outCount,
		"total_in":                roundMoney(store.totalIn),
		"total_out":               roundMoney(store.totalOut),
		"output_sheets":           sheetCount,
		"duplicate_files_skipped": duplicateFiles,
		"streaming":               true,
	}
	if store.rowsOut > len(store.preview) {
		summary["preview_rows"] = len(store.preview)
		summary["flow_graph_sampled"] = true
	}

	result := &model.PipelineResult{
		Transactions: store.preview,
		OutputPath:   outputPath,
		Summary:      summary,
		Report: model.QualityReport{
			Files:                 make([]model.FileReport, 0),
			RowsIn:                store.rowsIn,
			RowsOut:               store.rowsOut,
			RemovedEmptyRequired:  store.removedEmptyRequired,
			RemovedFailedFeedback: store.removedFailedFeedback,
			RemovedBadDirection:   store.removedBadDirection,
			RemovedDuplicates:     store.removedDuplicates,
		},
		MergeMode: "unified",
	}

	log.Info().
		Int("rows_in", store.rowsIn).
		Int("rows_out", store.rowsOut).
		Int("duplicate_files_skipped", duplicateFiles).
		Int("output_sheets", sheetCount).
		Int64("duration_ms", duration.Milliseconds()).
		Str("output", outputPath).
		Msg("pipeline_done")
	return result, nil
}

func newUnifiedStreamStore(path string) (*unifiedStreamStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open streaming sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	pragmas := []string{
		"PRAGMA journal_mode=OFF",
		"PRAGMA synchronous=OFF",
		"PRAGMA temp_store=FILE",
		fmt.Sprintf("PRAGMA cache_size=-%d", streamSQLiteCacheKiB),
		"PRAGMA locking_mode=EXCLUSIVE",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure streaming sqlite: %w", err)
		}
	}

	columnDefs := make([]string, len(unifiedStorageColumns))
	columnNames := make([]string, len(unifiedStorageColumns))
	placeholders := make([]string, len(unifiedStorageColumns)+1)
	placeholders[0] = "?"
	for i := range unifiedStorageColumns {
		columnNames[i] = fmt.Sprintf("c%d", i)
		columnDefs[i] = columnNames[i] + " TEXT NOT NULL DEFAULT ''"
		placeholders[i+1] = "?"
	}
	schema := fmt.Sprintf(
		"CREATE TABLE transactions (id INTEGER PRIMARY KEY, dedup_key TEXT NOT NULL UNIQUE, %s)",
		strings.Join(columnDefs, ", "),
	)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create streaming table: %w", err)
	}
	auditSchema := `
CREATE TABLE duplicates (
	id INTEGER PRIMARY KEY,
	dedup_type TEXT NOT NULL,
	dedup_key TEXT NOT NULL,
	kept_transaction_id INTEGER NOT NULL,
	kept_source_record_id TEXT NOT NULL DEFAULT '',
	duplicate_source_record_id TEXT NOT NULL DEFAULT '',
	source_type TEXT NOT NULL DEFAULT '',
	source_file TEXT NOT NULL DEFAULT '',
	source_sheet TEXT NOT NULL DEFAULT '',
	source_row TEXT NOT NULL DEFAULT '',
	transaction_time TEXT NOT NULL DEFAULT '',
	amount TEXT NOT NULL DEFAULT '',
	direction TEXT NOT NULL DEFAULT '',
	subject_account TEXT NOT NULL DEFAULT '',
	counterparty_account TEXT NOT NULL DEFAULT '',
	transaction_serial TEXT NOT NULL DEFAULT ''
);
CREATE TABLE rejected (
	id INTEGER PRIMARY KEY,
	reason TEXT NOT NULL,
	source_record_id TEXT NOT NULL DEFAULT '',
	source_type TEXT NOT NULL DEFAULT '',
	source_file TEXT NOT NULL DEFAULT '',
	source_sheet TEXT NOT NULL DEFAULT '',
	source_row TEXT NOT NULL DEFAULT '',
	transaction_time TEXT NOT NULL DEFAULT '',
	amount TEXT NOT NULL DEFAULT '',
	direction TEXT NOT NULL DEFAULT '',
	subject_status TEXT NOT NULL DEFAULT '',
	subject_basis TEXT NOT NULL DEFAULT '',
	transaction_serial TEXT NOT NULL DEFAULT ''
);`
	if _, err := db.Exec(auditSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create audit tables: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("begin streaming transaction: %w", err)
	}
	query := fmt.Sprintf(
		"INSERT OR IGNORE INTO transactions (dedup_key, %s) VALUES (%s)",
		strings.Join(columnNames, ", "),
		strings.Join(placeholders, ", "),
	)
	stmt, err := tx.Prepare(query)
	if err != nil {
		tx.Rollback()
		db.Close()
		return nil, fmt.Errorf("prepare streaming insert: %w", err)
	}
	duplicateInsert, err := tx.Prepare(`
INSERT INTO duplicates (
	dedup_type, dedup_key, kept_transaction_id, kept_source_record_id, duplicate_source_record_id,
	source_type, source_file, source_sheet, source_row, transaction_time, amount, direction,
	subject_account, counterparty_account, transaction_serial
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		stmt.Close()
		tx.Rollback()
		db.Close()
		return nil, fmt.Errorf("prepare duplicate audit insert: %w", err)
	}
	rejectedInsert, err := tx.Prepare(`
INSERT INTO rejected (
	reason, source_record_id, source_type, source_file, source_sheet, source_row,
	transaction_time, amount, direction, subject_status, subject_basis, transaction_serial
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		duplicateInsert.Close()
		stmt.Close()
		tx.Rollback()
		db.Close()
		return nil, fmt.Errorf("prepare rejected audit insert: %w", err)
	}
	return &unifiedStreamStore{
		db: db, tx: tx, insert: stmt, duplicateInsert: duplicateInsert, rejectedInsert: rejectedInsert,
		preview: make([]model.TransactionRow, 0, streamPreviewLimit),
	}, nil
}

func (s *unifiedStreamStore) Add(txn model.TransactionRow) error {
	s.rowsIn++
	cleanCommonAccountNumbers(txn)
	normalizeCommonTransactionFields(txn)
	if shouldSkipTransaction(txn) {
		reason := transactionRejectReason(txn)
		switch {
		case strings.HasPrefix(reason, "收付标志无法标准化"):
			s.removedBadDirection++
		case strings.HasPrefix(reason, "查询反馈结果"):
			s.removedFailedFeedback++
		default:
			s.removedEmptyRequired++
		}
		if err := s.recordRejected(txn, reason); err != nil {
			return err
		}
		return nil
	}

	dedupType, dedupKey := buildDedupIdentity(txn)
	args := make([]interface{}, 0, len(unifiedStorageColumns)+1)
	args = append(args, dedupKey)
	for _, column := range unifiedStorageColumns {
		args = append(args, txn[column])
	}
	result, err := s.insert.Exec(args...)
	if err != nil {
		return fmt.Errorf("insert streaming transaction: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count streaming insert: %w", err)
	}
	if affected == 0 {
		s.removedDuplicates++
		var keptID int64
		var keptSourceID string
		sourceColumn := transactionSQLiteColumn("来源记录ID")
		query := fmt.Sprintf("SELECT id, %s FROM transactions WHERE dedup_key = ?", sourceColumn)
		if err := s.tx.QueryRow(query, dedupKey).Scan(&keptID, &keptSourceID); err != nil {
			return fmt.Errorf("find retained duplicate: %w", err)
		}
		if _, err := s.duplicateInsert.Exec(
			dedupType, dedupKey, keptID, keptSourceID, txn["来源记录ID"],
			txn["来源类型"], txn["来源文件"], txn["来源Sheet"], txn["原始行号"],
			txn["交易时间"], txn["交易金额"], txn["收付标志"],
			firstNonEmptyTransactionValue(txn, "交易账号", "交易卡号"),
			txn["交易对手账卡号"], txn["交易流水号"],
		); err != nil {
			return fmt.Errorf("record duplicate audit: %w", err)
		}
		return nil
	}

	s.rowsOut++
	switch txn["收付标志"] {
	case "进":
		s.inCount++
		s.totalIn += parser.ToNumber(txn["交易金额"])
	case "出":
		s.outCount++
		s.totalOut += parser.ToNumber(txn["交易金额"])
	}
	if len(s.preview) < streamPreviewLimit {
		cloned := make(model.TransactionRow, len(txn))
		for key, value := range txn {
			cloned[key] = value
		}
		s.preview = append(s.preview, cloned)
	}
	return nil
}

func (s *unifiedStreamStore) Commit() error {
	if s.duplicateInsert != nil {
		if err := s.duplicateInsert.Close(); err != nil {
			return fmt.Errorf("close duplicate audit insert: %w", err)
		}
		s.duplicateInsert = nil
	}
	if s.rejectedInsert != nil {
		if err := s.rejectedInsert.Close(); err != nil {
			return fmt.Errorf("close rejected audit insert: %w", err)
		}
		s.rejectedInsert = nil
	}
	if s.insert != nil {
		if err := s.insert.Close(); err != nil {
			return fmt.Errorf("close streaming insert: %w", err)
		}
		s.insert = nil
	}
	if s.tx != nil {
		if err := s.tx.Commit(); err != nil {
			return fmt.Errorf("commit streaming transactions: %w", err)
		}
		s.tx = nil
	}
	return nil
}

func (s *unifiedStreamStore) Close() {
	if s.duplicateInsert != nil {
		_ = s.duplicateInsert.Close()
	}
	if s.rejectedInsert != nil {
		_ = s.rejectedInsert.Close()
	}
	if s.insert != nil {
		_ = s.insert.Close()
	}
	if s.tx != nil {
		_ = s.tx.Rollback()
	}
	if s.db != nil {
		_ = s.db.Close()
	}
}

func (s *unifiedStreamStore) recordRejected(txn model.TransactionRow, reason string) error {
	if reason == "" {
		reason = "未通过必填字段校验"
	}
	_, err := s.rejectedInsert.Exec(
		reason, txn["来源记录ID"], txn["来源类型"], txn["来源文件"], txn["来源Sheet"], txn["原始行号"],
		txn["交易时间"], txn["交易金额"], txn["收付标志"],
		txn["主体判定状态"], txn["主体判定依据"], txn["交易流水号"],
	)
	if err != nil {
		return fmt.Errorf("record rejected audit: %w", err)
	}
	return nil
}

func transactionSQLiteColumn(name string) string {
	for index, column := range unifiedStorageColumns {
		if column == name {
			return fmt.Sprintf("c%d", index)
		}
	}
	return "''"
}

func streamProviderTransactions(group ProviderFiles, outputDir string, options PipelineOptions, emit func(model.TransactionRow) error) error {
	mappingOptions := parser.MappingOptions{
		SourceHashes: options.SourceHashes, SubjectIdentifiers: options.SubjectIdentifiers,
	}
	switch group.Provider {
	case "支付宝":
		_, _, _, err := parser.StreamAlipayFilesWithOptions(group.Paths, "strict", mappingOptions, func(row []string) error {
			return emit(unifiedRowToTransaction(row, parser.UnifiedColumns))
		})
		return err
	case "微信":
		result, err := parser.ProcessWechatFilesWithOptions(group.Paths, outputDir, mappingOptions)
		if err != nil {
			return err
		}
		for _, row := range result.UnifiedData {
			if err := emit(unifiedRowToTransaction(row, parser.UnifiedColumns)); err != nil {
				return err
			}
		}
		result.UnifiedData = nil
		return nil
	default:
		rows, err := processProviderFilesWithOptions(group, "", outputDir, options)
		if err != nil {
			return err
		}
		for _, row := range rows {
			ensureDataSource(row)
			if err := emit(row); err != nil {
				return err
			}
		}
		return nil
	}
}

func ensureDataSource(row model.TransactionRow) {
	if strings.TrimSpace(row["数据来源"]) != "" {
		return
	}
	for _, column := range []string{"来源表", "来源文件", "来源"} {
		if value := strings.TrimSpace(row[column]); value != "" {
			row["数据来源"] = value
			return
		}
	}
}

func unifiedRowToTransaction(row, columns []string) model.TransactionRow {
	txn := make(model.TransactionRow, len(unifiedStorageColumns))
	for i, value := range row {
		if i >= len(columns) {
			break
		}
		column := columns[i]
		if column == "来源表" {
			column = "数据来源"
		}
		txn[column] = value
	}
	return txn
}

func deduplicateInputFiles(paths []string) ([]string, int, error) {
	kept, duplicates, err := deduplicateInputFilesWithAudit(paths)
	return kept, len(duplicates), err
}

func deduplicateInputFilesWithAudit(paths []string) ([]string, []duplicateInputFile, error) {
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	bySize := make(map[int64][]string)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, nil, fmt.Errorf("stat input %s: %w", path, err)
		}
		bySize[info.Size()] = append(bySize[info.Size()], path)
	}

	hashTargets := make([]string, 0)
	for _, sameSize := range bySize {
		if len(sameSize) > 1 {
			hashTargets = append(hashTargets, sameSize...)
		}
	}
	hashes, err := hashFilesParallel(hashTargets, 4)
	if err != nil {
		return nil, nil, err
	}

	keep := make(map[string]bool, len(paths))
	duplicates := make([]duplicateInputFile, 0)
	for _, sameSize := range bySize {
		if len(sameSize) == 1 {
			keep[sameSize[0]] = true
			continue
		}
		seen := make(map[[sha256.Size]byte]string)
		for _, path := range sameSize {
			hash := hashes[path]
			if original, exists := seen[hash]; exists {
				info, _ := os.Stat(path)
				duplicates = append(duplicates, duplicateInputFile{
					KeptPath: original, DuplicatePath: path,
					SHA256: hex.EncodeToString(hash[:]), Size: fileSize(info),
				})
				log.Warn().
					Str("path", path).
					Str("duplicate_of", original).
					Msg("duplicate_input_file_skipped")
				continue
			}
			seen[hash] = path
			keep[path] = true
		}
	}
	result := make([]string, 0, len(keep))
	for _, path := range paths {
		if keep[path] {
			result = append(result, path)
		}
	}
	return result, duplicates, nil
}

func hashFilesParallel(paths []string, workers int) (map[string][sha256.Size]byte, error) {
	if len(paths) == 0 {
		return map[string][sha256.Size]byte{}, nil
	}
	if workers <= 0 {
		workers = 1
	}
	if workers > len(paths) {
		workers = len(paths)
	}
	type hashResult struct {
		path string
		hash [sha256.Size]byte
		err  error
	}
	jobs := make(chan string)
	results := make(chan hashResult, len(paths))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				hash, err := hashFile(path)
				results <- hashResult{path: path, hash: hash, err: err}
			}
		}()
	}
	go func() {
		for _, path := range paths {
			jobs <- path
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	hashes := make(map[string][sha256.Size]byte, len(paths))
	for result := range results {
		if result.err != nil {
			return nil, result.err
		}
		hashes[result.path] = result.hash
	}
	return hashes, nil
}

func hashFile(path string) ([sha256.Size]byte, error) {
	data, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("open input for hash %s: %w", path, err)
	}
	defer data.Close()
	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	if _, err := io.CopyBuffer(hash, data, buffer); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("hash input %s: %w", path, err)
	}
	var sum [sha256.Size]byte
	copy(sum[:], hash.Sum(nil))
	return sum, nil
}

func exportStreamStoreToExcel(db *sql.DB, outputDir, jobID string) (string, int, error) {
	return exportStreamStoreToExcelWithLimitAndProgress(db, outputDir, jobID, excelMaxDataRows, nil)
}

func exportStreamStoreToExcelWithLimit(db *sql.DB, outputDir, jobID string, maxDataRows int) (string, int, error) {
	return exportStreamStoreToExcelWithLimitAndProgress(db, outputDir, jobID, maxDataRows, nil)
}

func exportStreamStoreToExcelWithLimitAndProgress(db *sql.DB, outputDir, jobID string, maxDataRows int, onRow func(int64)) (string, int, error) {
	if maxDataRows <= 0 || maxDataRows > excelMaxDataRows {
		maxDataRows = excelMaxDataRows
	}
	filename := fmt.Sprintf("funds_etl_%s.xlsx", jobID)
	outputPath := filepath.Join(outputDir, filename)
	columnNames := make([]string, len(UnifiedOutputColumns))
	for i := range columnNames {
		columnNames[i] = fmt.Sprintf("c%d", i)
	}
	rows, err := db.Query("SELECT " + strings.Join(columnNames, ", ") + " FROM transactions ORDER BY id")
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()

	f := excelize.NewFile()
	defer f.Close()
	sheetCount := 1
	sheetName := "清洗结果_1"
	if err := f.SetSheetName("Sheet1", sheetName); err != nil {
		return "", 0, err
	}
	writer, err := f.NewStreamWriter(sheetName)
	if err != nil {
		return "", 0, err
	}
	if err := writer.SetRow("A1", stringsToInterfaces(UnifiedOutputColumns)); err != nil {
		return "", 0, err
	}
	dataRowsInSheet := 0
	var totalRows int64

	for rows.Next() {
		values := make([]string, len(UnifiedOutputColumns))
		scanTargets := make([]interface{}, len(values))
		for i := range values {
			scanTargets[i] = &values[i]
		}
		if err := rows.Scan(scanTargets...); err != nil {
			return "", sheetCount, err
		}
		if dataRowsInSheet >= maxDataRows {
			if err := writer.Flush(); err != nil {
				return "", sheetCount, err
			}
			sheetCount++
			sheetName = fmt.Sprintf("清洗结果_%d", sheetCount)
			if _, err := f.NewSheet(sheetName); err != nil {
				return "", sheetCount, err
			}
			writer, err = f.NewStreamWriter(sheetName)
			if err != nil {
				return "", sheetCount, err
			}
			if err := writer.SetRow("A1", stringsToInterfaces(UnifiedOutputColumns)); err != nil {
				return "", sheetCount, err
			}
			dataRowsInSheet = 0
		}
		axis, _ := excelize.CoordinatesToCellName(1, dataRowsInSheet+2)
		if err := writer.SetRow(axis, stringsToInterfaces(values)); err != nil {
			return "", sheetCount, err
		}
		dataRowsInSheet++
		totalRows++
		if onRow != nil {
			onRow(totalRows)
		}
	}
	if err := rows.Err(); err != nil {
		return "", sheetCount, err
	}
	if err := writer.Flush(); err != nil {
		return "", sheetCount, err
	}
	if err := f.SaveAs(outputPath); err != nil {
		return "", sheetCount, err
	}
	return outputPath, sheetCount, nil
}

func stringsToInterfaces(values []string) []interface{} {
	result := make([]interface{}, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}

func roundMoney(value float64) float64 {
	if value >= 0 {
		return float64(int64(value*100+0.5)) / 100
	}
	return float64(int64(value*100-0.5)) / 100
}
