package etl

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"
	"github.com/etl/backend/internal/scanner"
	"github.com/xuri/excelize/v2"
)

const progressEmitRows = 2000

type sourcePlan struct {
	Path       string
	Provider   string
	HeaderRows map[string]int
	Headers    map[string][]string
	Rows       int64
}

type stagedProvider struct {
	Provider        string
	Paths           []string
	RawCSV          string
	RawRows         int64
	UnifiedCSV      string
	UnifiedAuditCSV string
	UnifiedRows     int64
	UnifiedColumns  []string
	DuplicateFiles  []duplicateInputFile
}

func emitProgress(options PipelineOptions, event ProgressEvent) {
	if options.Progress != nil {
		options.Progress(event)
	}
}

func runStagedPipeline(uploadDir, outputDir, jobID string, scan *scanner.DirectoryScan, options PipelineOptions, startTime time.Time) (*model.PipelineResult, error) {
	jobDir := filepath.Join(outputDir, "etl_jobs", jobID)
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		return nil, fmt.Errorf("create job artifact directory: %w", err)
	}
	internalDir := filepath.Join(jobDir, ".internal")
	if err := os.RemoveAll(internalDir); err != nil {
		return nil, fmt.Errorf("reset internal stage directory: %w", err)
	}
	defer os.RemoveAll(internalDir)

	artifacts, err := preserveUploadedSources(uploadDir, jobDir, options)
	if err != nil {
		return nil, err
	}
	providers, rawArtifacts, err := buildRawProviderCSVs(scan, jobDir, options)
	if err != nil {
		return nil, err
	}
	artifacts = append(artifacts, rawArtifacts...)

	if !options.UnifySources {
		result, err := buildSeparateStageResult(providers, outputDir, jobID, options)
		if err != nil {
			return nil, err
		}
		result.Artifacts = artifacts
		return result, nil
	}

	unifiedArtifacts, err := buildUnifiedProviderCSVs(providers, jobDir, outputDir, options)
	if err != nil {
		return nil, err
	}
	artifacts = append(artifacts, unifiedArtifacts...)
	result, finalArtifacts, err := mergeUnifiedStageCSVs(providers, outputDir, jobDir, jobID, options, startTime)
	if err != nil {
		return nil, err
	}
	result.Artifacts = append(artifacts, finalArtifacts...)
	return result, nil
}

func preserveUploadedSources(uploadDir, jobDir string, options PipelineOptions) ([]model.PipelineArtifact, error) {
	var files []string
	err := filepath.WalkDir(uploadDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	emitProgress(options, ProgressEvent{Stage: "preserve", Name: "保留源文件", Status: "running", Total: int64(len(files)), Unit: "文件"})
	artifacts := make([]model.PipelineArtifact, 0, len(files))
	for index, path := range files {
		relative, relErr := filepath.Rel(uploadDir, path)
		if relErr != nil {
			return nil, relErr
		}
		target := filepath.Join(jobDir, "01_源文件", relative)
		hash, err := copyStageFileWithHash(path, target)
		if err != nil {
			return nil, fmt.Errorf("preserve source %s: %w", filepath.Base(path), err)
		}
		options.SourceHashes[path] = hash
		info, _ := os.Stat(target)
		artifacts = append(artifacts, model.PipelineArtifact{
			ID: fmt.Sprintf("source-%d", index+1), Stage: "源文件", Name: relative,
			Path: target, Size: fileSize(info),
		})
		emitProgress(options, ProgressEvent{
			Stage: "preserve", Name: "保留源文件", Status: "running",
			Current: int64(index + 1), Total: int64(len(files)), Unit: "文件",
			Message: filepath.Base(path),
		})
	}
	emitProgress(options, ProgressEvent{Stage: "preserve", Name: "保留源文件", Status: "done", Current: int64(len(files)), Total: int64(len(files)), Unit: "文件"})
	return artifacts, nil
}

func buildRawProviderCSVs(scan *scanner.DirectoryScan, jobDir string, options PipelineOptions) ([]*stagedProvider, []model.PipelineArtifact, error) {
	candidates := separateMergeCandidates(scan)
	groupPaths := make(map[string]map[string]bool)
	for _, candidate := range candidates {
		provider := normalizedProvider(candidate.Provider)
		if groupPaths[provider] == nil {
			groupPaths[provider] = make(map[string]bool)
		}
		groupPaths[provider][candidate.Path] = true
	}
	var providers []*stagedProvider
	for provider, pathSet := range groupPaths {
		item := &stagedProvider{Provider: provider}
		for path := range pathSet {
			item.Paths = append(item.Paths, path)
		}
		sort.Strings(item.Paths)
		var err error
		item.Paths, item.DuplicateFiles, err = deduplicateInputFilesWithAudit(item.Paths)
		if err != nil {
			return nil, nil, err
		}
		providers = append(providers, item)
	}
	sort.Slice(providers, func(i, j int) bool {
		return providerOrder(providers[i].Provider) < providerOrder(providers[j].Provider)
	})

	var allPlans []sourcePlan
	providerPlans := make(map[string][]sourcePlan)
	var totalRows int64
	for _, provider := range providers {
		for _, path := range provider.Paths {
			plan, err := inspectSourcePlan(path, provider.Provider)
			if err != nil {
				return nil, nil, fmt.Errorf("inspect %s: %w", filepath.Base(path), err)
			}
			providerPlans[provider.Provider] = append(providerPlans[provider.Provider], plan)
			allPlans = append(allPlans, plan)
			totalRows += plan.Rows
		}
	}
	_ = allPlans
	emitProgress(options, ProgressEvent{Stage: "source_merge", Name: "分类原字段合并", Status: "running", Total: totalRows, Unit: "行"})
	var current atomic.Int64
	artifacts := make([]model.PipelineArtifact, len(providers))
	rawDir := filepath.Join(jobDir, "02_分类原字段CSV")
	if err := os.MkdirAll(rawDir, 0755); err != nil {
		return nil, nil, err
	}
	errChan := make(chan error, len(providers))
	var wg sync.WaitGroup
	for index, provider := range providers {
		wg.Add(1)
		go func(index int, provider *stagedProvider) {
			defer wg.Done()
			plans := providerPlans[provider.Provider]
			columns := unionPlanHeaders(plans)
			path := filepath.Join(rawDir, providerFileName(provider.Provider)+"_原字段合并.csv")
			rows, err := writeRawProviderCSV(path, columns, plans, func(message string) {
				done := current.Add(1)
				if done%progressEmitRows == 0 || done == totalRows {
					emitProgress(options, ProgressEvent{Stage: "source_merge", Name: "分类原字段合并", Status: "running", Current: done, Total: totalRows, Unit: "行", Message: message})
				}
			})
			if err != nil {
				errChan <- err
				return
			}
			provider.RawCSV = path
			provider.RawRows = rows
			info, _ := os.Stat(path)
			artifacts[index] = model.PipelineArtifact{
				ID: "raw-" + providerArtifactID(provider.Provider), Stage: "分类原字段CSV",
				Provider: provider.Provider, Name: filepath.Base(path), Path: path, Rows: rows, Size: fileSize(info),
			}
		}(index, provider)
	}
	wg.Wait()
	close(errChan)
	for err := range errChan {
		if err != nil {
			return nil, nil, err
		}
	}
	done := current.Load()
	emitProgress(options, ProgressEvent{Stage: "source_merge", Name: "分类原字段合并", Status: "done", Current: done, Total: done, Unit: "行"})
	fileAuditPath := filepath.Join(jobDir, "05_审计报告", "重复源文件审计.csv")
	fileAuditRows, err := exportDuplicateFileAudit(fileAuditPath, providers)
	if err != nil {
		return nil, nil, err
	}
	fileAuditInfo, _ := os.Stat(fileAuditPath)
	artifacts = append(artifacts, model.PipelineArtifact{
		ID: "duplicate-file-audit-csv", Stage: "审计报告", Name: filepath.Base(fileAuditPath),
		Path: fileAuditPath, Rows: fileAuditRows, Size: fileSize(fileAuditInfo),
	})
	return providers, artifacts, nil
}

func exportDuplicateFileAudit(path string, providers []*stagedProvider) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return 0, err
	}
	file, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"来源类型", "保留文件", "重复文件", "文件SHA256", "文件大小"}); err != nil {
		return 0, err
	}
	var count int64
	for _, provider := range providers {
		for _, duplicate := range provider.DuplicateFiles {
			if err := writer.Write([]string{
				provider.Provider, duplicate.KeptPath, duplicate.DuplicatePath,
				duplicate.SHA256, fmt.Sprintf("%d", duplicate.Size),
			}); err != nil {
				return count, err
			}
			count++
		}
	}
	writer.Flush()
	return count, writer.Error()
}

func inspectSourcePlan(path, provider string) (sourcePlan, error) {
	previews, err := parser.ReadTabularPreviews(path, 40)
	if err != nil {
		return sourcePlan{}, err
	}
	plan := sourcePlan{Path: path, Provider: provider, HeaderRows: make(map[string]int), Headers: make(map[string][]string)}
	for sheet, rows := range previews {
		rows = parser.NormalizeEmbeddedCSVRows(parser.TrimRows(rows))
		headerRow := findSourceHeaderRow(rows, provider)
		if headerRow < 0 {
			continue
		}
		plan.HeaderRows[sheet] = headerRow
		plan.Headers[sheet] = sourceHeaders(rows[headerRow])
	}
	if len(plan.Headers) == 0 {
		return plan, nil
	}
	if isExcelPath(path) {
		for sheet, headerRow := range plan.HeaderRows {
			rows := parser.CountExcelRows(path, sheet) - headerRow - 1
			if rows > 0 {
				plan.Rows += int64(rows)
			}
		}
	} else {
		rows := countDelimitedRows(path) - 1
		if headerRow, ok := plan.HeaderRows[""]; ok {
			rows -= int64(headerRow)
		}
		if rows > 0 {
			plan.Rows = rows
		}
	}
	return plan, nil
}

func unionPlanHeaders(plans []sourcePlan) []string {
	seen := make(map[string]bool)
	columns := []string{"源文件", "源Sheet", "源行号"}
	for _, column := range columns {
		seen[column] = true
	}
	for _, plan := range plans {
		sheets := sortedHeaderSheets(plan.Headers)
		for _, sheet := range sheets {
			for _, header := range plan.Headers[sheet] {
				if !seen[header] {
					columns = append(columns, header)
					seen[header] = true
				}
			}
		}
	}
	return columns
}

func writeRawProviderCSV(path string, columns []string, plans []sourcePlan, onRow func(string)) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return 0, err
	}
	file, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	if err := writer.Write(columns); err != nil {
		return 0, err
	}
	var rowsWritten int64
	for _, plan := range plans {
		err := parser.StreamTabularFile(plan.Path, func(sheet string, rowIndex int, raw []string) error {
			headerRow, ok := plan.HeaderRows[sheet]
			if !ok || rowIndex <= headerRow || sourceRowEmpty(raw) {
				return nil
			}
			headers := plan.Headers[sheet]
			values := make(map[string]string, len(headers)+3)
			values["源文件"] = plan.Path
			values["源Sheet"] = sheet
			values["源行号"] = fmt.Sprintf("%d", rowIndex+1)
			for index, header := range headers {
				if index < len(raw) {
					values[header] = parser.CellToText(raw[index])
				}
			}
			output := make([]string, len(columns))
			for index, column := range columns {
				output[index] = values[column]
			}
			if err := writer.Write(output); err != nil {
				return err
			}
			rowsWritten++
			onRow(filepath.Base(plan.Path))
			return nil
		})
		if err != nil {
			return rowsWritten, err
		}
	}
	writer.Flush()
	return rowsWritten, writer.Error()
}

func buildUnifiedProviderCSVs(providers []*stagedProvider, jobDir, outputDir string, options PipelineOptions) ([]model.PipelineArtifact, error) {
	var total int64
	for _, provider := range providers {
		total += provider.RawRows
	}
	emitProgress(options, ProgressEvent{Stage: "normalize", Name: "分类字段统一", Status: "running", Total: total, Unit: "行"})
	unifiedDir := filepath.Join(jobDir, "03_分类统一字段CSV")
	if err := os.MkdirAll(unifiedDir, 0755); err != nil {
		return nil, err
	}
	internalDir := filepath.Join(jobDir, ".internal")
	if err := os.MkdirAll(internalDir, 0755); err != nil {
		return nil, err
	}
	var current atomic.Int64
	artifacts := make([]model.PipelineArtifact, len(providers))
	errChan := make(chan error, len(providers))
	var wg sync.WaitGroup
	for providerIndex, provider := range providers {
		wg.Add(1)
		go func(providerIndex int, provider *stagedProvider) {
			defer wg.Done()
			path := filepath.Join(unifiedDir, providerFileName(provider.Provider)+"_统一字段.csv")
			auditPath := filepath.Join(internalDir, providerFileName(provider.Provider)+"_内部审计字段.csv")
			file, err := os.Create(path)
			if err != nil {
				errChan <- err
				return
			}
			writer := csv.NewWriter(file)
			if err := writer.Write(UnifiedOutputColumns); err != nil {
				file.Close()
				errChan <- err
				return
			}
			auditFile, err := os.Create(auditPath)
			if err != nil {
				file.Close()
				errChan <- err
				return
			}
			auditWriter := csv.NewWriter(auditFile)
			if err := auditWriter.Write(unifiedStorageColumns); err != nil {
				auditFile.Close()
				file.Close()
				errChan <- err
				return
			}
			var rows int64
			group := ProviderFiles{Provider: provider.Provider, Paths: provider.Paths}
			err = streamProviderTransactions(group, outputDir, options, func(txn model.TransactionRow) error {
				values := make([]string, len(UnifiedOutputColumns))
				for index, column := range UnifiedOutputColumns {
					values[index] = txn[column]
				}
				if err := writer.Write(values); err != nil {
					return err
				}
				auditValues := make([]string, len(unifiedStorageColumns))
				for index, column := range unifiedStorageColumns {
					auditValues[index] = txn[column]
				}
				if err := auditWriter.Write(auditValues); err != nil {
					return err
				}
				rows++
				done := current.Add(1)
				if done%progressEmitRows == 0 || done == total {
					emitProgress(options, ProgressEvent{Stage: "normalize", Name: "分类字段统一", Status: "running", Current: done, Total: total, Unit: "行", Message: provider.Provider})
				}
				return nil
			})
			writer.Flush()
			writeErr := writer.Error()
			closeErr := file.Close()
			auditWriter.Flush()
			auditWriteErr := auditWriter.Error()
			auditCloseErr := auditFile.Close()
			if err != nil {
				errChan <- err
				return
			}
			if writeErr != nil {
				errChan <- writeErr
				return
			}
			if closeErr != nil {
				errChan <- closeErr
				return
			}
			if auditWriteErr != nil {
				errChan <- auditWriteErr
				return
			}
			if auditCloseErr != nil {
				errChan <- auditCloseErr
				return
			}
			provider.UnifiedCSV = path
			provider.UnifiedAuditCSV = auditPath
			provider.UnifiedRows = rows
			provider.UnifiedColumns = append([]string(nil), UnifiedOutputColumns...)
			info, _ := os.Stat(path)
			artifacts[providerIndex] = model.PipelineArtifact{
				ID: "unified-" + providerArtifactID(provider.Provider), Stage: "分类统一字段CSV",
				Provider: provider.Provider, Name: filepath.Base(path), Path: path, Rows: rows, Size: fileSize(info),
			}
		}(providerIndex, provider)
	}
	wg.Wait()
	close(errChan)
	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}
	done := current.Load()
	emitProgress(options, ProgressEvent{Stage: "normalize", Name: "分类字段统一", Status: "done", Current: done, Total: done, Unit: "行"})
	return artifacts, nil
}

func mergeUnifiedStageCSVs(providers []*stagedProvider, outputDir, jobDir, jobID string, options PipelineOptions, startTime time.Time) (*model.PipelineResult, []model.PipelineArtifact, error) {
	stageDir, err := os.MkdirTemp(outputDir, ".etl_stream_*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(stageDir)
	store, err := newUnifiedStreamStore(filepath.Join(stageDir, "transactions.sqlite"))
	if err != nil {
		return nil, nil, err
	}
	defer store.Close()

	var total int64
	for _, provider := range providers {
		total += provider.UnifiedRows
	}
	emitProgress(options, ProgressEvent{Stage: "final_merge", Name: "跨来源清洗去重合并", Status: "running", Total: total, Unit: "行"})
	var current int64
	for _, provider := range providers {
		err := parser.StreamTabularFile(provider.UnifiedAuditCSV, func(_ string, rowIndex int, row []string) error {
			if rowIndex == 0 {
				return nil
			}
			current++
			if err := store.Add(unifiedRowToTransaction(row, unifiedStorageColumns)); err != nil {
				return err
			}
			if current%progressEmitRows == 0 || current == total {
				emitProgress(options, ProgressEvent{Stage: "final_merge", Name: "跨来源清洗去重合并", Status: "running", Current: current, Total: total, Unit: "行", Message: provider.Provider})
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if err := store.Commit(); err != nil {
		return nil, nil, err
	}
	emitProgress(options, ProgressEvent{Stage: "final_merge", Name: "跨来源清洗去重合并", Status: "done", Current: current, Total: current, Unit: "行"})

	exportTotal := int64(store.rowsOut) * 2
	emitProgress(options, ProgressEvent{Stage: "export", Name: "导出最终结果", Status: "running", Total: exportTotal, Unit: "行"})
	finalCSV := filepath.Join(jobDir, "04_最终合并CSV", "全部来源_统一清洗.csv")
	if err := exportStoreToCSV(store.db, finalCSV, func(done int64) {
		if done%progressEmitRows == 0 || done == int64(store.rowsOut) {
			emitProgress(options, ProgressEvent{Stage: "export", Name: "导出最终结果", Status: "running", Current: done, Total: exportTotal, Unit: "行", Message: "CSV"})
		}
	}); err != nil {
		return nil, nil, err
	}
	outputPath, sheetCount, err := exportStreamStoreToExcelWithLimitAndProgress(store.db, outputDir, jobID, excelMaxDataRows, func(done int64) {
		if done%progressEmitRows == 0 || done == int64(store.rowsOut) {
			emitProgress(options, ProgressEvent{Stage: "export", Name: "导出最终结果", Status: "running", Current: int64(store.rowsOut) + done, Total: exportTotal, Unit: "行", Message: "Excel"})
		}
	})
	if err != nil {
		return nil, nil, err
	}
	emitProgress(options, ProgressEvent{Stage: "export", Name: "导出最终结果", Status: "done", Current: exportTotal, Total: exportTotal, Unit: "行", Message: "CSV + Excel"})

	duration := time.Since(startTime)
	summary := map[string]interface{}{
		"rows_in": store.rowsIn, "rows_out": store.rowsOut, "total_rows": store.rowsOut,
		"duration_ms": duration.Milliseconds(), "columns": UnifiedOutputColumns,
		"in_count": store.inCount, "out_count": store.outCount,
		"total_in": roundMoney(store.totalIn), "total_out": roundMoney(store.totalOut),
		"output_sheets": sheetCount, "streaming": true, "staged_csv": true,
	}
	if store.rowsOut > len(store.preview) {
		summary["preview_rows"] = len(store.preview)
		summary["flow_graph_sampled"] = true
	}
	result := &model.PipelineResult{
		Transactions: store.preview, OutputPath: outputPath, Summary: summary,
		Report: model.QualityReport{
			Files: make([]model.FileReport, 0), RowsIn: store.rowsIn, RowsOut: store.rowsOut,
			RemovedEmptyRequired: store.removedEmptyRequired, RemovedFailedFeedback: store.removedFailedFeedback,
			RemovedBadDirection: store.removedBadDirection,
			RemovedDuplicates:   store.removedDuplicates,
		},
		MergeMode: "unified",
	}
	csvInfo, _ := os.Stat(finalCSV)
	xlsxInfo, _ := os.Stat(outputPath)
	auditDir := filepath.Join(jobDir, "05_审计报告")
	duplicateAudit := filepath.Join(auditDir, "重复记录审计.csv")
	duplicateRows, err := exportSQLiteAuditCSV(store.db, duplicateAudit, "duplicates", []auditExportColumn{
		{"去重键类型", "dedup_type"}, {"去重键", "dedup_key"}, {"保留记录ID", "kept_transaction_id"},
		{"保留来源记录ID", "kept_source_record_id"}, {"重复来源记录ID", "duplicate_source_record_id"},
		{"来源类型", "source_type"}, {"来源文件", "source_file"}, {"来源Sheet", "source_sheet"},
		{"原始行号", "source_row"}, {"交易时间", "transaction_time"}, {"交易金额", "amount"},
		{"收付标志", "direction"}, {"本方账号", "subject_account"}, {"对手账号", "counterparty_account"},
		{"交易流水号", "transaction_serial"},
	})
	if err != nil {
		return nil, nil, err
	}
	rejectedAudit := filepath.Join(auditDir, "未纳入记录审计.csv")
	rejectedRows, err := exportSQLiteAuditCSV(store.db, rejectedAudit, "rejected", []auditExportColumn{
		{"未纳入原因", "reason"}, {"来源记录ID", "source_record_id"}, {"来源类型", "source_type"},
		{"来源文件", "source_file"}, {"来源Sheet", "source_sheet"}, {"原始行号", "source_row"},
		{"交易时间", "transaction_time"}, {"交易金额", "amount"}, {"收付标志", "direction"},
		{"主体判定状态", "subject_status"}, {"主体判定依据", "subject_basis"}, {"交易流水号", "transaction_serial"},
	})
	if err != nil {
		return nil, nil, err
	}
	duplicateInfo, _ := os.Stat(duplicateAudit)
	rejectedInfo, _ := os.Stat(rejectedAudit)
	return result, []model.PipelineArtifact{
		{ID: "final-csv", Stage: "最终合并", Name: filepath.Base(finalCSV), Path: finalCSV, Rows: int64(store.rowsOut), Size: fileSize(csvInfo)},
		{ID: "final-xlsx", Stage: "兼容导出", Name: filepath.Base(outputPath), Path: outputPath, Rows: int64(store.rowsOut), Size: fileSize(xlsxInfo)},
		{ID: "duplicate-audit-csv", Stage: "审计报告", Name: filepath.Base(duplicateAudit), Path: duplicateAudit, Rows: duplicateRows, Size: fileSize(duplicateInfo)},
		{ID: "rejected-audit-csv", Stage: "审计报告", Name: filepath.Base(rejectedAudit), Path: rejectedAudit, Rows: rejectedRows, Size: fileSize(rejectedInfo)},
	}, nil
}

func buildSeparateStageResult(providers []*stagedProvider, outputDir, jobID string, options PipelineOptions) (*model.PipelineResult, error) {
	outputPath := filepath.Join(outputDir, "funds_separate_"+jobID+".xlsx")
	var totalRows int64
	var preview []model.TransactionRow
	var sourceSheets []model.SourceSheetSummary
	for _, provider := range providers {
		totalRows += provider.RawRows
		rows := previewCSV(provider.RawCSV, 100-len(preview))
		for _, row := range rows {
			row[sourceTypeColumn] = provider.Provider
		}
		preview = append(preview, rows...)
		sourceSheets = append(sourceSheets, model.SourceSheetSummary{
			Provider: provider.Provider, Sheet: provider.Provider, Rows: int(provider.RawRows),
			Columns: countCSVColumns(provider.RawCSV),
		})
	}
	emitProgress(options, ProgressEvent{Stage: "export", Name: "导出分类Excel", Status: "running", Total: totalRows, Unit: "行"})
	if err := exportRawProvidersToExcel(providers, outputPath, func(done int64, provider string) {
		if done%progressEmitRows == 0 || done == totalRows {
			emitProgress(options, ProgressEvent{Stage: "export", Name: "导出分类Excel", Status: "running", Current: done, Total: totalRows, Unit: "行", Message: provider})
		}
	}); err != nil {
		return nil, err
	}
	emitProgress(options, ProgressEvent{Stage: "export", Name: "导出分类Excel", Status: "done", Current: totalRows, Total: totalRows, Unit: "行"})
	return &model.PipelineResult{
		Transactions: preview, OutputPath: outputPath,
		Summary:   map[string]interface{}{"total_rows": totalRows, "merge_mode": "separate", "staged_csv": true},
		Report:    model.QualityReport{RowsIn: int(totalRows), RowsOut: int(totalRows), Files: make([]model.FileReport, 0)},
		MergeMode: "separate", SourceSheets: sourceSheets,
	}, nil
}

func exportRawProvidersToExcel(providers []*stagedProvider, outputPath string, onProvider func(int64, string)) error {
	workbook := excelize.NewFile()
	defer workbook.Close()
	workbook.DeleteSheet("Sheet1")
	var totalWritten int64
	for _, provider := range providers {
		input, err := os.Open(provider.RawCSV)
		if err != nil {
			return err
		}
		reader := csv.NewReader(input)
		reader.FieldsPerRecord = -1
		headers, err := reader.Read()
		if err != nil {
			input.Close()
			return err
		}
		part := 1
		sheetName := parser.SafeSheetName(provider.Provider)
		if _, err := workbook.NewSheet(sheetName); err != nil {
			input.Close()
			return err
		}
		writer, err := workbook.NewStreamWriter(sheetName)
		if err != nil {
			input.Close()
			return err
		}
		if err := writer.SetRow("A1", stringsToInterfaces(headers)); err != nil {
			input.Close()
			return err
		}
		dataRows := 0
		for {
			row, readErr := reader.Read()
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				input.Close()
				return readErr
			}
			if dataRows >= excelMaxDataRows {
				if err := writer.Flush(); err != nil {
					input.Close()
					return err
				}
				part++
				sheetName = parser.SafeSheetName(fmt.Sprintf("%s_%d", provider.Provider, part))
				if _, err := workbook.NewSheet(sheetName); err != nil {
					input.Close()
					return err
				}
				writer, err = workbook.NewStreamWriter(sheetName)
				if err != nil {
					input.Close()
					return err
				}
				if err := writer.SetRow("A1", stringsToInterfaces(headers)); err != nil {
					input.Close()
					return err
				}
				dataRows = 0
			}
			axis, _ := excelize.CoordinatesToCellName(1, dataRows+2)
			if err := writer.SetRow(axis, stringsToInterfaces(row)); err != nil {
				input.Close()
				return err
			}
			dataRows++
			totalWritten++
			onProvider(totalWritten, provider.Provider)
		}
		input.Close()
		if err := writer.Flush(); err != nil {
			return err
		}
	}
	if len(providers) == 0 {
		workbook.NewSheet("无可合并数据")
	}
	return workbook.SaveAs(outputPath)
}

func exportStoreToCSV(db *sql.DB, path string, onRow func(int64)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	columns := make([]string, len(UnifiedOutputColumns))
	for index := range columns {
		columns[index] = fmt.Sprintf("c%d", index)
	}
	rows, err := db.Query("SELECT " + strings.Join(columns, ", ") + " FROM transactions ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	if err := writer.Write(UnifiedOutputColumns); err != nil {
		return err
	}
	var done int64
	for rows.Next() {
		values := make([]string, len(UnifiedOutputColumns))
		targets := make([]interface{}, len(values))
		for index := range values {
			targets[index] = &values[index]
		}
		if err := rows.Scan(targets...); err != nil {
			return err
		}
		if err := writer.Write(values); err != nil {
			return err
		}
		done++
		onRow(done)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	return rows.Err()
}

type auditExportColumn struct {
	Header string
	Field  string
}

func exportSQLiteAuditCSV(db *sql.DB, path, table string, columns []auditExportColumn) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return 0, err
	}
	fields := make([]string, len(columns))
	headers := make([]string, len(columns))
	for index, column := range columns {
		fields[index] = column.Field
		headers[index] = column.Header
	}
	rows, err := db.Query("SELECT " + strings.Join(fields, ", ") + " FROM " + table + " ORDER BY id")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	file, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	if err := writer.Write(headers); err != nil {
		return 0, err
	}
	var count int64
	for rows.Next() {
		values := make([]string, len(columns))
		targets := make([]interface{}, len(values))
		for index := range values {
			targets[index] = &values[index]
		}
		if err := rows.Scan(targets...); err != nil {
			return count, err
		}
		if err := writer.Write(values); err != nil {
			return count, err
		}
		count++
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return count, err
	}
	return count, rows.Err()
}

func previewCSV(path string, limit int) []model.TransactionRow {
	if limit <= 0 {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	headers, err := reader.Read()
	if err != nil {
		return nil
	}
	var result []model.TransactionRow
	for len(result) < limit {
		values, err := reader.Read()
		if err != nil {
			break
		}
		row := make(model.TransactionRow)
		for index, header := range headers {
			if index < len(values) {
				row[header] = values[index]
			}
		}
		result = append(result, row)
	}
	return result
}

func countCSVColumns(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	row, err := csv.NewReader(file).Read()
	if err != nil {
		return 0
	}
	return len(row)
}

func copyStageFile(source, target string) error {
	_, err := copyStageFileWithHash(source, target)
	return err
}

func copyStageFileWithHash(source, target string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return "", err
	}
	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	output, err := os.Create(target)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(output, hash), input)
	closeErr := output.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func countDelimitedRows(path string) int64 {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 1024*1024)
	var count int64
	for {
		_, err := reader.ReadString('\n')
		if err == nil {
			count++
			continue
		}
		if err == io.EOF {
			count++
		}
		return count
	}
}

func sortedHeaderSheets(headers map[string][]string) []string {
	result := make([]string, 0, len(headers))
	for sheet := range headers {
		result = append(result, sheet)
	}
	sort.Strings(result)
	return result
}

func isExcelPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".xlsx", ".xlsm", ".xls":
		return true
	default:
		return false
	}
}

func providerOrder(provider string) int {
	switch provider {
	case "支付宝":
		return 0
	case "微信":
		return 1
	case "银行":
		return 2
	default:
		return 3
	}
}

func providerFileName(provider string) string {
	if provider == "未知来源" || strings.TrimSpace(provider) == "" {
		return "未知来源"
	}
	return provider
}

func providerArtifactID(provider string) string {
	switch provider {
	case "支付宝":
		return "alipay"
	case "微信":
		return "wechat"
	case "银行":
		return "bank"
	default:
		return "unknown"
	}
}

func fileSize(info os.FileInfo) int64 {
	if info == nil {
		return 0
	}
	return info.Size()
}
