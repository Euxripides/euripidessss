package etl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"
	"github.com/etl/backend/internal/rules"
	"github.com/etl/backend/internal/scanner"
	"github.com/xuri/excelize/v2"
)

const sourceTypeColumn = "来源类型"

type sourceMergeSheet struct {
	Provider string
	Columns  []string
	Rows     []model.TransactionRow
}

func runSeparateSourceMerge(scan *scanner.DirectoryScan, outputDir, jobID string) (*model.PipelineResult, error) {
	candidates := separateMergeCandidates(scan)
	candidatesByProvider := make(map[string][]scanner.SheetCandidate)
	for _, candidate := range candidates {
		provider := normalizedProvider(candidate.Provider)
		candidatesByProvider[provider] = append(candidatesByProvider[provider], candidate)
	}

	grouped := make(map[string]*sourceMergeSheet)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errChan := make(chan error, len(candidatesByProvider))
	for provider, providerCandidates := range candidatesByProvider {
		wg.Add(1)
		go func(provider string, providerCandidates []scanner.SheetCandidate) {
			defer wg.Done()
			sheet := &sourceMergeSheet{Provider: provider}
			for _, candidate := range providerCandidates {
				if err := appendSourceFile(sheet, candidate.Path, provider); err != nil {
					errChan <- fmt.Errorf("merge %s file %s: %w", provider, filepath.Base(candidate.Path), err)
					return
				}
			}
			mu.Lock()
			grouped[provider] = sheet
			mu.Unlock()
		}(provider, providerCandidates)
	}
	wg.Wait()
	close(errChan)
	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}

	sheets := orderedSourceSheets(grouped)
	outputPath, err := exportSourceSheets(sheets, outputDir, jobID)
	if err != nil {
		return nil, err
	}

	var previewRows []model.TransactionRow
	sourceSheets := make([]model.SourceSheetSummary, 0, len(sheets))
	providerRows := make(map[string]int)
	for _, sheet := range sheets {
		providerRows[sheet.Provider] = len(sheet.Rows)
		sourceSheets = append(sourceSheets, model.SourceSheetSummary{
			Provider: sheet.Provider,
			Sheet:    sourceSheetName(sheet.Provider),
			Rows:     len(sheet.Rows),
			Columns:  len(sheet.Columns),
		})
		for _, sourceRow := range sheet.Rows {
			previewRow := make(model.TransactionRow, len(sourceRow)+1)
			for column, value := range sourceRow {
				previewRow[column] = value
			}
			previewRow[sourceTypeColumn] = sheet.Provider
			previewRows = append(previewRows, previewRow)
		}
	}

	summary := map[string]interface{}{
		"total_rows":    len(previewRows),
		"merge_mode":    "separate",
		"provider_rows": providerRows,
	}
	return &model.PipelineResult{
		Transactions: previewRows,
		OutputPath:   outputPath,
		Summary:      summary,
		Report: model.QualityReport{
			RowsIn:  len(previewRows),
			RowsOut: len(previewRows),
			Files:   make([]model.FileReport, 0),
		},
		MergeMode:    "separate",
		SourceSheets: sourceSheets,
	}, nil
}

func separateMergeCandidates(scan *scanner.DirectoryScan) []scanner.SheetCandidate {
	candidates := make([]scanner.SheetCandidate, 0, len(scan.Transactions)+len(scan.Unknown))
	hasUploadFolders := false
	for _, candidate := range append(append([]scanner.SheetCandidate(nil), scan.Transactions...), scan.Unknown...) {
		if isTransactionUploadPath(candidate.Path) {
			hasUploadFolders = true
			break
		}
	}
	for _, candidate := range scan.Transactions {
		if !hasUploadFolders || isTransactionUploadPath(candidate.Path) {
			candidates = append(candidates, candidate)
		}
	}
	for _, candidate := range scan.Unknown {
		if !hasUploadFolders || isTransactionUploadPath(candidate.Path) {
			candidates = append(candidates, candidate)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Provider == candidates[j].Provider {
			return candidates[i].Path < candidates[j].Path
		}
		return candidates[i].Provider < candidates[j].Provider
	})
	return candidates
}

func isTransactionUploadPath(path string) bool {
	cleaned := strings.ToLower(filepath.Clean(path))
	segment := string(filepath.Separator) + "transactions" + string(filepath.Separator)
	return strings.Contains(cleaned, segment)
}

func normalizedProvider(provider string) string {
	switch strings.TrimSpace(provider) {
	case "支付宝", "微信", "银行":
		return strings.TrimSpace(provider)
	default:
		return "未知来源"
	}
}

func appendSourceFile(sheet *sourceMergeSheet, path, provider string) error {
	fileData, err := parser.ReadFile(path)
	if err != nil {
		return err
	}
	sheetNames := make([]string, 0, len(fileData))
	for sheetName := range fileData {
		sheetNames = append(sheetNames, sheetName)
	}
	sort.Strings(sheetNames)

	columnSet := make(map[string]bool, len(sheet.Columns))
	for _, column := range sheet.Columns {
		columnSet[column] = true
	}
	for _, sheetName := range sheetNames {
		rows := parser.NormalizeEmbeddedCSVRows(parser.TrimRows(fileData[sheetName]))
		headerRow := findSourceHeaderRow(rows, provider)
		if headerRow < 0 || headerRow >= len(rows) {
			continue
		}
		headers := sourceHeaders(rows[headerRow])
		if len(headers) == 0 {
			continue
		}
		for _, header := range headers {
			if !columnSet[header] {
				sheet.Columns = append(sheet.Columns, header)
				columnSet[header] = true
			}
		}
		for _, rawRow := range rows[headerRow+1:] {
			if sourceRowEmpty(rawRow) {
				continue
			}
			row := make(model.TransactionRow)
			for index, header := range headers {
				if index < len(rawRow) {
					row[header] = parser.CellToText(rawRow[index])
				}
			}
			sheet.Rows = append(sheet.Rows, row)
		}
	}
	return nil
}

func findSourceHeaderRow(rows [][]string, provider string) int {
	bestIndex := -1
	bestScore := 0
	for index, row := range rows {
		if index >= 40 {
			break
		}
		score := sourceHeaderScore(row, provider)
		if score > bestScore {
			bestScore = score
			bestIndex = index
		}
	}
	if bestScore >= 3 {
		return bestIndex
	}
	for index, row := range rows {
		if index >= 40 {
			break
		}
		nonEmpty := 0
		for _, cell := range row {
			if parser.NormalizeHeader(cell) != "" {
				nonEmpty++
			}
		}
		if nonEmpty >= 2 {
			return index
		}
	}
	return -1
}

func sourceHeaderScore(row []string, provider string) int {
	best := 0
	tables := []map[string][]string{parser.AlipayStandardTables, parser.WechatTables, rules.BankTables}
	switch provider {
	case "支付宝":
		tables = []map[string][]string{parser.AlipayStandardTables}
	case "微信":
		tables = []map[string][]string{parser.WechatTables}
	case "银行":
		tables = []map[string][]string{rules.BankTables}
	}
	for _, tableSet := range tables {
		for _, expected := range tableSet {
			if score := parser.HeaderScore(row, expected); score > best {
				best = score
			}
		}
	}
	return best
}

func sourceHeaders(raw []string) []string {
	headers := make([]string, len(raw))
	for index, value := range raw {
		headers[index] = parser.NormalizeHeader(value)
	}
	return parser.MakeUnique(headers)
}

func sourceRowEmpty(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

func orderedSourceSheets(grouped map[string]*sourceMergeSheet) []*sourceMergeSheet {
	order := []string{"支付宝", "微信", "银行", "未知来源"}
	result := make([]*sourceMergeSheet, 0, len(grouped))
	for _, provider := range order {
		if sheet := grouped[provider]; sheet != nil && len(sheet.Rows) > 0 {
			result = append(result, sheet)
		}
	}
	return result
}

func exportSourceSheets(sheets []*sourceMergeSheet, outputDir, jobID string) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}
	outputPath := filepath.Join(outputDir, fmt.Sprintf("funds_separate_%s.xlsx", jobID))
	workbook := excelize.NewFile()
	defer workbook.Close()

	if len(sheets) == 0 {
		workbook.SetSheetName("Sheet1", "无可合并数据")
	} else {
		workbook.DeleteSheet("Sheet1")
		for _, sourceSheet := range sheets {
			sheetName := sourceSheetName(sourceSheet.Provider)
			if _, err := workbook.NewSheet(sheetName); err != nil {
				return "", fmt.Errorf("create sheet %s: %w", sheetName, err)
			}
			columns := sourceSheet.Columns
			for columnIndex, column := range columns {
				cell, _ := excelize.CoordinatesToCellName(columnIndex+1, 1)
				workbook.SetCellValue(sheetName, cell, column)
			}
			for rowIndex, row := range sourceSheet.Rows {
				for columnIndex, column := range columns {
					cell, _ := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+2)
					workbook.SetCellValue(sheetName, cell, row[column])
				}
			}
		}
	}
	if err := workbook.SaveAs(outputPath); err != nil {
		return "", fmt.Errorf("save separate source workbook: %w", err)
	}
	return outputPath, nil
}

func sourceSheetName(provider string) string {
	switch provider {
	case "支付宝", "微信", "银行":
		return provider
	default:
		return "未知来源"
	}
}
