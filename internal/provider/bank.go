package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"
	"github.com/etl/backend/internal/rules"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/xuri/excelize/v2"
)

// ProcessBankDirectory processes bank files in a directory
func ProcessBankDirectory(sourceDir, outputDir string) (*Result, error) {
	resolved, err := filepath.Abs(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve source: %w", err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	files, err := scanFiles(resolved)
	if err != nil {
		return nil, err
	}

	var allSources []Source
	tableRows := make(map[string]int)
	var transactionFrames []map[string][]string
	var accountFrames []map[string][]string

	for _, path := range files {
		sheets, err := parser.ReadFile(path)
		if err != nil {
			log.Warn().Err(err).Str("path", path).Msg("skip file")
			continue
		}
		for sheetName, rows := range sheets {
			rows = parser.TrimRows(rows)
			rows = parser.NormalizeEmbeddedCSVRows(rows)

			headerRow := findBankHeaderRow(rows)
			if headerRow < 0 {
				allSources = append(allSources, Source{
					Path: path, SheetName: sheetName,
					TableType: "未识别", HeaderRow: 0, Rows: 0,
					Notes: []string{"前40行未找到银行卡表头"},
				})
				continue
			}

			data, headers := parser.DataFrameFromHeader(rows, headerRow)
			tableType := rules.ClassifyTable(headers, filepath.Base(path), sheetName)
			tableRows[tableType] = tableRows[tableType] + len(data)

			allSources = append(allSources, Source{
				Path: path, SheetName: sheetName,
				TableType: tableType, HeaderRow: headerRow + 1,
				Rows: len(data), Columns: headers,
			})

			if tableType == "未识别" {
				continue
			}

			if rules.TransactionTableTypes[tableType] {
				normalized, err := rules.NormalizeTransaction(headers, data, tableType, path, sheetName)
				if err == nil {
					transactionFrames = append(transactionFrames, normalized)
				}
			} else if rules.AccountTableTypes[tableType] {
				normalized, err := rules.NormalizeAccount(headers, data, tableType, path, sheetName)
				if err == nil {
					accountFrames = append(accountFrames, normalized)
				}
			} else {
				finalName := rules.FinalTableName(tableType)
				if finalName != "" {
					normalized := rules.NormalizeGenericFinal(headers, data, finalName)
					transactionFrames = append(transactionFrames, normalized)
				}
			}
		}
	}

	transactions := mergeColMaps(transactionFrames, rules.BankTransactionColumns)
	accounts := mergeColMaps(accountFrames, rules.BankAccountColumns)

	transactions = rules.CleanTransactions(transactions)
	accounts = rules.CleanAccounts(accounts)
	rules.FillFromAccounts(transactions, accounts)

	txRows := colMapToRows(transactions, rules.BankTransactionColumns)
	acctRows := colMapToRows(accounts, rules.BankAccountColumns)

	finalTxns := addSourceColumn(txRows, "交易明细")
	finalAccts := addSourceColumn(acctRows, "账户信息")

	quality := buildBankQuality(txRows, acctRows, allSources, tableRows)

	outputPath := filepath.Join(outputDir, fmt.Sprintf("bank_etl_%s.xlsx", uuid.New().String()[:12]))
	if err := writeBankOutput(outputPath, finalTxns, finalAccts, allSources, quality); err != nil {
		return nil, fmt.Errorf("write output: %w", err)
	}

	return &Result{
		OutputPath:  outputPath,
		Sources:     allSources,
		TableRows:   tableRows,
		UnifiedRows: len(txRows),
		Quality:     quality,
	}, nil
}

func findBankHeaderRow(rows [][]string) int {
	bestIdx := -1
	bestScore := 0
	tableCandidates := make([][]string, 0)
	for _, cols := range rules.BankTables {
		tableCandidates = append(tableCandidates, cols)
	}
	for i := 0; i < len(rows) && i < 40; i++ {
		cells := make([]string, 0)
		for _, cell := range rows[i] {
			c := parser.NormalizeHeader(cell)
			if c != "" {
				cells = append(cells, c)
			}
		}
		if len(cells) < 3 {
			continue
		}
		for _, expected := range tableCandidates {
			score := parser.HeaderScore(cells, expected)
			if score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}
	}
	if bestScore >= 3 {
		return bestIdx
	}
	return -1
}

func mergeColMaps(frames []map[string][]string, columns []string) map[string][]string {
	result := make(map[string][]string)
	for _, col := range columns {
		result[col] = make([]string, 0)
	}
	for _, frame := range frames {
		// Determine row count
		rowCount := 0
		for _, vals := range frame {
			rowCount = len(vals)
			break
		}
		for _, col := range columns {
			if vals, ok := frame[col]; ok {
				result[col] = append(result[col], vals...)
			} else {
				for i := 0; i < rowCount; i++ {
					result[col] = append(result[col], "")
				}
			}
		}
	}
	return result
}

func colMapToRows(colMap map[string][]string, columns []string) []model.TransactionRow {
	if len(colMap) == 0 {
		return nil
	}
	var rowCount int
	for _, vals := range colMap {
		rowCount = len(vals)
		break
	}
	rows := make([]model.TransactionRow, rowCount)
	for i := 0; i < rowCount; i++ {
		row := make(model.TransactionRow)
		for _, col := range columns {
			if vals, ok := colMap[col]; ok && i < len(vals) {
				row[col] = vals[i]
			}
		}
		rows[i] = row
	}
	return rows
}

func addSourceColumn(rows []model.TransactionRow, source string) []model.TransactionRow {
	for _, row := range rows {
		row["来源"] = source
	}
	return rows
}

func buildBankQuality(transactions, accounts []model.TransactionRow, sources []Source, tableRows map[string]int) map[string]interface{} {
	unknownCount := 0
	var unknownSources []map[string]interface{}
	for _, s := range sources {
		if s.TableType == "未识别" {
			unknownCount++
			unknownSources = append(unknownSources, map[string]interface{}{
				"文件": s.Path, "Sheet": s.SheetName, "备注": strings.Join(s.Notes, "；"),
			})
		}
	}
	keyCols := []string{"交易时间", "交易金额", "收付标志", "交易卡号", "交易对手账卡号", "交易流水号"}
	var nullStats []map[string]interface{}
	for _, col := range keyCols {
		nullCount := 0
		for _, txn := range transactions {
			if txn[col] == "" {
				nullCount++
			}
		}
		ratio := 0.0
		if len(transactions) > 0 {
			ratio = float64(nullCount) / float64(len(transactions))
		}
		nullStats = append(nullStats, map[string]interface{}{
			"字段": col, "空值行数": nullCount, "空值比例": ratio,
		})
	}
	var sourceCounts []map[string]interface{}
	for k, v := range tableRows {
		isTx := rules.TransactionTableTypes[k]
		isAcct := rules.AccountTableTypes[k]
		sourceCounts = append(sourceCounts, map[string]interface{}{
			"表类型": k, "识别行数": v,
			"是否进入交易明细": isTx, "是否进入账户信息": isAcct,
		})
	}
	return map[string]interface{}{
		"summary": map[string]interface{}{
			"统一流水行数":      len(transactions),
			"账户信息行数":      len(accounts),
			"识别文件或Sheet数":  len(sources),
			"未识别文件或Sheet数": unknownCount,
		},
		"source_counts":   sourceCounts,
		"key_nulls":       nullStats,
		"unknown_sources": unknownSources,
	}
}

func writeBankOutput(outputPath string, transactions, accounts []model.TransactionRow, sources []Source, quality map[string]interface{}) error {
	f := excelize.NewFile()
	defer f.Close()

	sheetName := "交易明细信息"
	f.SetSheetName("Sheet1", sheetName)
	txCols := rules.BankFinalTables["交易明细信息"]
	for i, h := range txCols {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheetName, cell, h)
	}
	for i, txn := range transactions {
		for j, h := range txCols {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
			f.SetCellValue(sheetName, cell, txn[h])
		}
	}
	if len(accounts) > 0 {
		acctSheet := "账户信息"
		acctCols := rules.BankFinalTables["账户信息"]
		acctIdx, _ := f.NewSheet(acctSheet)
		for i, h := range acctCols {
			cell, _ := excelize.CoordinatesToCellName(i+1, 1)
			f.SetCellValue(acctSheet, cell, h)
		}
		for i, acct := range accounts {
			for j, h := range acctCols {
				cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
				f.SetCellValue(acctSheet, cell, acct[h])
			}
		}
		_ = acctIdx
	}
	reportSheet := "识别报告"
	f.NewSheet(reportSheet)
	rHeaders := []string{"path", "sheet_name", "table_type", "header_row", "rows"}
	for i, h := range rHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(reportSheet, cell, h)
	}
	for i, s := range sources {
		cell1, _ := excelize.CoordinatesToCellName(1, i+2)
		cell2, _ := excelize.CoordinatesToCellName(2, i+2)
		cell3, _ := excelize.CoordinatesToCellName(3, i+2)
		cell4, _ := excelize.CoordinatesToCellName(4, i+2)
		cell5, _ := excelize.CoordinatesToCellName(5, i+2)
		f.SetCellValue(reportSheet, cell1, s.Path)
		f.SetCellValue(reportSheet, cell2, s.SheetName)
		f.SetCellValue(reportSheet, cell3, s.TableType)
		f.SetCellValue(reportSheet, cell4, s.HeaderRow)
		f.SetCellValue(reportSheet, cell5, s.Rows)
	}
	return f.SaveAs(outputPath)
}
