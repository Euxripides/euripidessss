package etl

import (
	"archive/zip"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/xuri/excelize/v2"

	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"
	"github.com/etl/backend/internal/provider"
	"github.com/etl/backend/internal/scanner"
)

// FinalTransactionColumns defines output columns for cleaned data
var FinalTransactionColumns = []string{
	"交易卡号", "交易账号", "交易户名", "交易证件号码", "交易方开户行",
	"账户性质", "交易时间", "交易金额", "交易余额", "收付标志",
	"交易对手账卡号", "对手账户性质", "现金标志", "对手户名", "对手身份证号",
	"对手开户银行", "摘要说明", "交易币种", "交易网点名称", "交易发生地",
	"交易是否成功", "传票号", "IP地址", "MAC地址", "对手交易余额",
	"交易流水号", "日志号", "凭证种类", "凭证号", "交易柜员号",
	"备注", "查询反馈结果原因", "数据来源",
}

// RequiredTransactionColumns must be non-empty after processing
var RequiredTransactionColumns = []string{"交易时间", "交易金额", "收付标志"}

// ALIASES maps source column names to canonical column names
var ALIASES = map[string][]string{
	"交易卡号":     {"交易卡号", "交易卡", "卡号", "银行卡号", "本方卡号", "付款方账号", "收款方账号", "账户"},
	"交易账号":     {"交易账号", "账号", "银行账号", "本方账号", "支付宝账号", "微信号", "商户号"},
	"交易户名":     {"交易户名", "本方户名", "账户名称", "客户名称", "付款方姓名", "收款方姓名"},
	"交易证件号码":  {"交易证件号码", "证件号码", "身份证号", "开户人证件号码"},
	"交易方开户行":  {"交易方开户行", "开户银行", "账号开户银行", "开户行"},
	"交易时间":     {"交易时间", "交易日期", "账务日期", "入账时间", "支付时间", "创建时间", "发生时间", "记账日期", "交易创建时间"},
	"交易金额":     {"交易金额", "金额", "收入金额", "支出金额", "收/支", "收入（+元）", "支出（-元）", "交易额", "付款金额"},
	"交易余额":     {"交易余额", "余额", "账户余额", "可用余额"},
	"收付标志":     {"收付标志", "借贷标志", "借贷方向", "收支类型", "收/支", "收入/支出", "方向", "业务类型"},
	"交易对手账卡号": {"交易对手账卡号", "对方账号", "对手账号", "对方卡号", "对手卡号", "交易对方账号", "对方账户"},
	"现金标志":     {"现金标志", "现转标志", "现金/转账"},
	"对手户名":     {"对手户名", "对方户名", "对方姓名", "交易对方", "对方名称", "商户名称", "交易对手"},
	"对手身份证号":  {"对手身份证号", "对方证件号", "对手证件号"},
	"对手开户银行":  {"对手开户银行", "对方开户行", "对手开户行", "对方银行"},
	"摘要说明":     {"摘要说明", "摘要", "交易摘要", "商品说明", "交易说明", "备注说明", "用途", "交易类型"},
	"交易币种":     {"交易币种", "币种", "货币"},
	"交易网点名称":  {"交易网点名称", "交易网点", "网点名称"},
	"交易发生地":    {"交易发生地", "发生地", "交易地点"},
	"交易是否成功":  {"交易是否成功", "交易状态", "状态", "交易结果"},
	"传票号":      {"传票号"},
	"IP地址":     {"IP地址", "IP"},
	"MAC地址":    {"MAC地址", "MAC"},
	"对手交易余额":  {"对手交易余额", "对方余额"},
	"交易流水号":    {"交易流水号", "流水号", "交易号", "商户订单号", "订单号", "微信支付订单号", "支付宝交易号"},
	"日志号":      {"日志号"},
	"凭证种类":     {"凭证种类", "凭证类型"},
	"凭证号":      {"凭证号"},
	"交易柜员号":    {"交易柜员号", "柜员号"},
	"备注":       {"备注", "附言", "说明"},
	"查询反馈结果原因": {"查询反馈结果原因", "反馈原因", "失败原因", "返回原因"},
}

// ACCOUNT_ALIASES maps source column names to account canonical names
var ACCOUNT_ALIASES = map[string][]string{
	"账户开户名称":  {"账户开户名称", "开户名称", "客户名称", "户名", "姓名", "账户名称"},
	"开户人证件号码": {"开户人证件号码", "证件号码", "身份证号", "证件号"},
	"交易卡号":    {"交易卡号", "卡号", "银行卡号", "账户"},
	"交易账号":    {"交易账号", "账号", "银行账号", "账户账号"},
	"账号开户时间":  {"账号开户时间", "开户时间", "开户日期"},
	"账户余额":    {"账户余额"},
	"可用余额":    {"可用余额"},
	"币种":      {"币种"},
	"开户网点代码":  {"开户网点代码"},
	"开户网点":    {"开户网点", "开户机构"},
	"账户状态":    {"账户状态", "状态"},
	"销户日期":    {"销户日期"},
	"账户类型":    {"账户类型"},
	"备注":      {"备注"},
	"账号开户银行":  {"账号开户银行", "开户银行"},
	"销户网点":    {"销户网点"},
	"最后交易时间":  {"最后交易时间"},
	"证件类型":    {"证件类型", "证件种类"},
}

// RunPipeline runs the full ETL pipeline on uploaded files
func RunPipeline(uploadDir string, outputDir string, jobID string) (*model.PipelineResult, error) {
	startTime := time.Now()
	log.Info().Str("uploadDir", uploadDir).Str("jobID", jobID).Msg("pipeline_start")

	result := &model.PipelineResult{
		Summary: make(map[string]interface{}),
		Report: model.QualityReport{
			Files: make([]model.FileReport, 0),
		},
	}

	// Scan directory
	scan, err := scanner.ScanDirectory(uploadDir)
	if err != nil {
		return nil, fmt.Errorf("scan directory: %w", err)
	}

	// Process by provider
	var allTransactions []model.TransactionRow
	var mu sync.Mutex
	var wg sync.WaitGroup

	providerGroups := categorizeByProvider(scan)
	// errChan must have buffer >= number of goroutines to prevent deadlock
	// when all provider goroutines error simultaneously
	errChan := make(chan error, len(providerGroups))

	// Process in parallel by provider category
	for _, pfItem := range providerGroups {
		wg.Add(1)
		go func(pf ProviderFiles) {
			defer wg.Done()
			txns, err := processProviderFiles(pf, uploadDir, outputDir)
			if err != nil {
				errChan <- err
				return
			}
			mu.Lock()
			allTransactions = append(allTransactions, txns...)
			mu.Unlock()
		}(pfItem)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}

	// Clean and deduplicate
	cleaned := CleanTransactions(allTransactions)
	deduped := DeduplicateTransactions(cleaned)

	// Build report
	totalIn := len(allTransactions)
	removedEmptyRequired := totalIn - len(cleaned)
	removedDuplicates := len(cleaned) - len(deduped)
	totalOut := len(deduped)
	result.Report.RowsIn = totalIn
	result.Report.RowsOut = totalOut
	result.Report.RemovedEmptyRequired = removedEmptyRequired
	result.Report.RemovedDuplicates = removedDuplicates

	// Export result
	outputPath, err := ExportToExcel(deduped, outputDir, jobID)
	if err != nil {
		return nil, fmt.Errorf("export: %w", err)
	}
	result.OutputPath = outputPath
	result.Transactions = deduped

	duration := time.Since(startTime)
	result.Summary["rows_in"] = totalIn
	result.Summary["rows_out"] = totalOut
	result.Summary["duration_ms"] = duration.Milliseconds()
	result.Summary["columns"] = FinalTransactionColumns

	log.Info().Int("rows_in", totalIn).Int("rows_out", totalOut).
		Int64("duration_ms", duration.Milliseconds()).
		Str("output", outputPath).Msg("pipeline_done")

	return result, nil
}


type ProviderFiles struct {
	Provider string
	Paths    []string
}

func categorizeByProvider(scan *scanner.DirectoryScan) []ProviderFiles {
	groups := make(map[string][]string)
	for _, c := range scan.Transactions {
		provider := c.Provider
		if provider == "" {
			provider = "unknown"
		}
		groups[provider] = append(groups[provider], c.Path)
	}
	var result []ProviderFiles
	for provider, paths := range groups {
		result = append(result, ProviderFiles{Provider: provider, Paths: paths})
	}
	return result
}

func processProviderFiles(pf ProviderFiles, baseDir string, outputDir string) ([]model.TransactionRow, error) {
	switch pf.Provider {
	case "支付宝":
		alipayResult, err := parser.ProcessAlipayDirectory(baseDir, "", "strict")
		if err != nil {
			return nil, err
		}
		return convertAlipayToRows(alipayResult), nil
	case "微信":
		tempDir, err := os.MkdirTemp("", "wechat_*")
		if err != nil {
			return nil, fmt.Errorf("create temp dir for wechat: %w", err)
		}
		defer os.RemoveAll(tempDir)
		for _, p := range pf.Paths {
			copyFile(p, filepath.Join(tempDir, filepath.Base(p)))
		}
		wechatResult, err := parser.ProcessWechatDirectory(tempDir, outputDir)
		if err != nil {
			return nil, err
		}
		return convertWechatToRows(wechatResult), nil
	case "银行":
		tempDir, err := os.MkdirTemp("", "bank_*")
		if err != nil {
			return nil, fmt.Errorf("create temp dir for bank: %w", err)
		}
		defer os.RemoveAll(tempDir)
		for _, p := range pf.Paths {
			copyFile(p, filepath.Join(tempDir, filepath.Base(p)))
		}
		bankResult, err := provider.ProcessBankDirectory(tempDir, outputDir)
		if err != nil {
			return nil, err
		}
		return convertBankToRows(bankResult), nil
	default:
		// For unknown providers, just read and normalize
		return processGenericFiles(pf.Paths)
	}
}

func convertAlipayToRows(result *parser.AlipayResult) []model.TransactionRow {
	// Output path contains the Excel file with unified data
	// We need to read it back and convert to TransactionRows
	rows := make([]model.TransactionRow, 0)
	if result.OutputPath == "" {
		return rows
	}
	// Read the Excel output
	f, err := excelize.OpenFile(result.OutputPath)
	if err != nil {
		log.Warn().Err(err).Msg("cannot read alipay output for conversion")
		return rows
	}
	defer f.Close()

	for _, sheet := range f.GetSheetList() {
		excelRows, err := f.GetRows(sheet)
		if err != nil || len(excelRows) < 2 {
			continue
		}
		headers := excelRows[0]
		for _, row := range excelRows[1:] {
			txn := make(model.TransactionRow)
			for j, cell := range row {
				if j < len(headers) {
					txn[headers[j]] = cell
				}
			}
			rows = append(rows, txn)
		}
	}
	return rows
}

func convertWechatToRows(result *parser.WechatResult) []model.TransactionRow {
	rows := make([]model.TransactionRow, 0)
	if result.OutputPath == "" {
		return rows
	}
	f, err := excelize.OpenFile(result.OutputPath)
	if err != nil {
		log.Warn().Err(err).Msg("cannot read wechat output for conversion")
		return rows
	}
	defer f.Close()

	for _, sheet := range f.GetSheetList() {
		excelRows, err := f.GetRows(sheet)
		if err != nil || len(excelRows) < 2 {
			continue
		}
		headers := excelRows[0]
		for _, row := range excelRows[1:] {
			txn := make(model.TransactionRow)
			for j, cell := range row {
				if j < len(headers) {
					txn[headers[j]] = cell
				}
			}
			rows = append(rows, txn)
		}
	}
	return rows
}

func convertBankToRows(result *provider.Result) []model.TransactionRow {
	rows := make([]model.TransactionRow, 0)
	if result.OutputPath == "" {
		return rows
	}
	f, err := excelize.OpenFile(result.OutputPath)
	if err != nil {
		log.Warn().Err(err).Msg("cannot read bank output for conversion")
		return rows
	}
	defer f.Close()

	for _, sheet := range f.GetSheetList() {
		excelRows, err := f.GetRows(sheet)
		if err != nil || len(excelRows) < 2 {
			continue
		}
		headers := excelRows[0]
		for _, row := range excelRows[1:] {
			txn := make(model.TransactionRow)
			for j, cell := range row {
				if j < len(headers) {
					txn[headers[j]] = cell
				}
			}
			rows = append(rows, txn)
		}
	}
	return rows
}

func processGenericFiles(paths []string) ([]model.TransactionRow, error) {
	var result []model.TransactionRow
	for _, path := range paths {
		fileData, err := parser.ReadFile(path)
		if err != nil {
			log.Warn().Err(err).Str("path", path).Msg("skip unreadable file")
			continue
		}
		for _, rows := range fileData {
			if len(rows) < 2 {
				continue
			}
			headers := make([]string, len(rows[0]))
			for i, h := range rows[0] {
				headers[i] = parser.NormalizeHeader(h)
			}
			for _, row := range rows[1:] {
				txn := make(model.TransactionRow)
				for j, cell := range row {
					if j < len(headers) {
						txn[headers[j]] = cell
					}
				}
				txn["数据来源"] = filepath.Base(path)
				result = append(result, txn)
			}
		}
	}
	return result, nil
}

// CleanTransactions applies cleaning rules to transaction rows
func CleanTransactions(txns []model.TransactionRow) []model.TransactionRow {
	var cleaned []model.TransactionRow
	for _, txn := range txns {
		// Skip rows missing required fields
		skip := false
		for _, req := range RequiredTransactionColumns {
			if val, ok := txn[req]; !ok || strings.TrimSpace(val) == "" {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		// Normalize direction
		if dir, ok := txn["收付标志"]; ok {
			txn["收付标志"] = parser.NormalizeDirection(dir)
		}
		// Normalize datetime
		if dt, ok := txn["交易时间"]; ok {
			txn["交易时间"] = parser.NormalizeDatetime(dt)
		}
		// Normalize amount
		if amt, ok := txn["交易金额"]; ok {
			txn["交易金额"] = parser.FloatToStr(parser.ToNumber(amt))
		}
		cleaned = append(cleaned, txn)
	}
	return cleaned
}

// DeduplicateTransactions removes duplicate rows
func DeduplicateTransactions(txns []model.TransactionRow) []model.TransactionRow {
	seen := make(map[string]bool)
	var result []model.TransactionRow
	for _, txn := range txns {
		key := buildDedupKey(txn)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, txn)
	}
	return result
}

func buildDedupKey(txn model.TransactionRow) string {
	parts := []string{
		txn["交易时间"], txn["交易金额"], txn["收付标志"],
		txn["交易卡号"], txn["交易对手账卡号"],
	}
	return strings.Join(parts, "|")
}

// ExportToExcel writes transaction rows to an Excel file
func ExportToExcel(txns []model.TransactionRow, outputDir, jobID string) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}
	filename := fmt.Sprintf("%s_etl_%s.xlsx", "funds", jobID)
	outputPath := filepath.Join(outputDir, filename)

	f := excelize.NewFile()
	defer f.Close()

	f.SetSheetName("Sheet1", "清洗结果")

	// Write headers
	headers := FinalTransactionColumns
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("清洗结果", cell, h)
	}

	// Write data with streaming for large datasets
	style, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{WrapText: true},
	})

	for i, txn := range txns {
		rowNum := i + 2
		for j, h := range headers {
			cell, _ := excelize.CoordinatesToCellName(j+1, rowNum)
			val := txn[h]
			f.SetCellValue("清洗结果", cell, val)
			if rowNum%100 == 0 {
				f.SetCellStyle("清洗结果", cell, cell, style)
			}
		}
		if i%1000 == 0 && i > 0 {
			log.Debug().Int("written", i).Msg("export_progress")
		}
	}


	if err := f.SaveAs(outputPath); err != nil {
		return "", fmt.Errorf("save excel: %w", err)
	}
	return outputPath, nil
}

// ExportToCSV writes transaction rows to CSV
func ExportToCSV(txns []model.TransactionRow, outputDir, jobID string) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}
	filename := fmt.Sprintf("%s_etl_%s.csv", "funds", jobID)
	outputPath := filepath.Join(outputDir, filename)

	f, err := os.Create(outputPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	writer := csv.NewWriter(f)
	defer writer.Flush()

	// Write BOM for Excel compatibility
	f.WriteString("\xef\xbb\xbf")

	headers := FinalTransactionColumns
	writer.Write(headers)
	for _, txn := range txns {
		row := make([]string, len(headers))
		for j, h := range headers {
			row[j] = txn[h]
		}
		writer.Write(row)
	}
	return outputPath, nil
}

// BuildPreview creates preview data from transactions
func BuildPreview(txns []model.TransactionRow, limit int) ([]map[string]interface{}, []string) {
	if len(txns) == 0 {
		return nil, FinalTransactionColumns
	}
	// Get all column names
	colSet := make(map[string]bool)
	for _, txn := range txns {
		for k := range txn {
			colSet[k] = true
		}
	}
	var columns []string
	for k := range colSet {
		columns = append(columns, k)
	}
	sort.Strings(columns)

	if limit <= 0 {
		limit = 100
	}
	if limit > len(txns) {
		limit = len(txns)
	}

	preview := make([]map[string]interface{}, limit)
	for i := 0; i < limit; i++ {
		row := make(map[string]interface{})
		for _, col := range columns {
			row[col] = txns[i][col]
		}
		preview[i] = row
	}
	return preview, columns
}

// BuildSummary creates summary statistics
func BuildSummary(txns []model.TransactionRow) map[string]interface{} {
	summary := make(map[string]interface{})
	summary["total_rows"] = len(txns)

	// Count by direction
	inCount := 0
	outCount := 0
	for _, txn := range txns {
		switch txn["收付标志"] {
		case "进":
			inCount++
		case "出":
			outCount++
		}
	}
	summary["in_count"] = inCount
	summary["out_count"] = outCount

	// Calculate total amount
	var totalIn, totalOut float64
	for _, txn := range txns {
		amt := parser.ToNumber(txn["交易金额"])
		switch txn["收付标志"] {
		case "进":
			totalIn += amt
		case "出":
			totalOut += amt
		}
	}
	summary["total_in"] = math.Round(totalIn*100) / 100
	summary["total_out"] = math.Round(totalOut*100) / 100

	return summary
}

// ZipOutput creates a zip file from output
func ZipOutput(sourcePath, outputDir, jobID string) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}
	zipPath := filepath.Join(outputDir, fmt.Sprintf("清洗结果_%s.zip", jobID))
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return "", fmt.Errorf("create zip file: %w", err)
	}
	defer zipFile.Close()
	zw := zip.NewWriter(zipFile)
	defer zw.Close()
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return "", fmt.Errorf("stat source: %w", err)
	}
	if sourceInfo.IsDir() {
		err = filepath.Walk(sourcePath, func(filePath string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			relPath, err := filepath.Rel(sourcePath, filePath)
			if err != nil {
				return err
			}
			w, err := zw.Create(relPath)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(filePath)
			if err != nil {
				return err
			}
			_, err = w.Write(data)
			return err
		})
		if err != nil {
			return "", fmt.Errorf("zip directory: %w", err)
		}
	} else {
		w, err := zw.Create(filepath.Base(sourcePath))
		if err != nil {
			return "", fmt.Errorf("create zip entry: %w", err)
		}
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return "", fmt.Errorf("read source: %w", err)
		}
		_, err = w.Write(data)
		if err != nil {
			return "", fmt.Errorf("write zip entry: %w", err)
		}
	}
	log.Info().Str("zipPath", zipPath).Msg("zip_created")
	return zipPath, nil
}

// GenerateJobID creates a unique job ID
func GenerateJobID() string {
	return uuid.New().String()[:12]
}

// ParseQueryParam parses integer from query params
func ParseQueryParam(val string, defaultVal int) int {
	if val == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return n
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
