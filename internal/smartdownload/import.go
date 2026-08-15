package smartdownload

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"
)

// DetectedColumn 地址列识别结果（实施方案 §27：valid/non_empty 命中率）。
type DetectedColumn struct {
	Name       string  `json:"name"`
	Confidence float64 `json:"confidence"`
	Valid      int     `json:"valid"`
	NonEmpty   int     `json:"non_empty"`
}

// ImportResult 地址导入统计。
type ImportResult struct {
	Rows            int64            `json:"rows"`
	DetectedColumns []DetectedColumn `json:"detected_columns"`
	SelectedColumn  string           `json:"selected_column"`
	Valid           int              `json:"valid"`
	Duplicates      int              `json:"duplicates"`
	Invalid         int              `json:"invalid"`
	FinalAddresses  []string         `json:"final_addresses,omitempty"`
	Persisted       int              `json:"persisted"`
	ChainKey        string           `json:"chain_key,omitempty"`
}

// importMaxBytes 导入文件读取上限（32MB；超大文件建议分批）。
const importMaxBytes = 32 << 20

// importMaxCells XLSX 全工作簿单元格累计上限（约 200 万单元格，防超大工作簿耗尽内存）。
const importMaxCells = 2_000_000

// ImportAddresses 解析 TXT/CSV/XLSX 并自动识别地址列（多列候选返回给前端手动改选）。
func (s *Service) ImportAddresses(filename string, r io.Reader) (*ImportResult, error) {
	if r == nil {
		return nil, fmt.Errorf("缺少上传文件")
	}
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt":
		return importText(r)
	case ".csv":
		return importCSV(r)
	case ".xlsx", ".xlsm":
		return importXLSX(r)
	default:
		return nil, fmt.Errorf("仅支持 TXT/CSV/XLSX，当前 %s", ext)
	}
}

func importText(r io.Reader) (*ImportResult, error) {
	payload, err := io.ReadAll(io.LimitReader(r, importMaxBytes))
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0)
	for _, line := range strings.Split(strings.ReplaceAll(string(payload), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("文件为空")
	}
	if isAddressHeader(lines[0]) {
		lines = lines[1:]
	}
	summary := normalizeAddresses(strings.Join(lines, "\n"))
	confidence := 0.0
	if len(lines) > 0 {
		confidence = float64(summary.Valid) / float64(len(lines))
	}
	col := DetectedColumn{Name: "address", Confidence: confidence, Valid: summary.Valid, NonEmpty: len(lines)}
	return &ImportResult{
		Rows:            int64(len(lines)),
		DetectedColumns: []DetectedColumn{col},
		SelectedColumn:  "address",
		Valid:           summary.Valid,
		Duplicates:      summary.Duplicates,
		Invalid:         summary.Invalid,
		FinalAddresses:  summary.Addresses,
	}, nil
}

func isAddressHeader(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, "\ufeff")))
	switch value {
	case "address", "wallet", "wallet_address", "account_address", "地址", "钱包地址", "账户地址":
		return true
	default:
		return false
	}
}

// importCSV 读入内存（上限 32MB），逐列统计命中率并提取最佳列。
func importCSV(r io.Reader) (*ImportResult, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	all, err := readCSVAll(reader)
	if err != nil {
		return nil, err
	}
	return analyzeColumns(all)
}

// importXLSX 遍历全部工作表：每个 Sheet 独立读取行并识别地址列，
// 无地址列的说明页记录诊断后继续扫描，跨 Sheet 合并地址并统一去重。
func importXLSX(r io.Reader) (*ImportResult, error) {
	workbook, err := excelize.OpenReader(r)
	if err != nil {
		return nil, fmt.Errorf("读取 XLSX: %w", err)
	}
	defer workbook.Close()
	sheets := workbook.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("XLSX 没有工作表")
	}
	var mergedValues []string
	selected := ""
	bestValid := -1
	totalRows := int64(0)
	totalCells := 0
	totalDup := 0
	totalInvalid := 0
	totalValid := 0
	validSheets := 0
	var single *ImportResult // 仅一个有效 Sheet 时保留原单表行为（含完整列统计）
	for _, sheet := range sheets {
		rowsIt, err := workbook.Rows(sheet)
		if err != nil {
			return nil, fmt.Errorf("读取工作表 %s: %w", sheet, err)
		}
		var all [][]string
		for rowsIt.Next() {
			row, err := rowsIt.Columns()
			if err != nil {
				rowsIt.Close()
				return nil, fmt.Errorf("读取工作表 %s: %w", sheet, err)
			}
			totalCells += len(row)
			if totalCells > importMaxCells {
				rowsIt.Close()
				return nil, fmt.Errorf("XLSX 单元格数量超过 %d 上限", importMaxCells)
			}
			all = append(all, row)
		}
		if err := rowsIt.Error(); err != nil {
			rowsIt.Close()
			return nil, fmt.Errorf("读取工作表 %s: %w", sheet, err)
		}
		rowsIt.Close()
		if len(all) == 0 {
			continue
		}
		result, err := analyzeColumns(all)
		if err != nil {
			continue // 说明页/无地址列 Sheet：记录诊断后继续扫描
		}
		// 地址纯度门：说明页中偶发的地址示例（如“0x... 不应被识别”）不算地址 Sheet。
		// 只有大多数数据行是 EVM 地址的 Sheet 才参与合并。
		if result.Rows == 0 || float64(result.Valid)/float64(result.Rows) <= 0.5 {
			continue
		}
		totalRows += result.Rows
		totalValid += result.Valid
		totalDup += result.Duplicates
		totalInvalid += result.Invalid
		if selected == "" || result.Valid > bestValid {
			selected = result.SelectedColumn
			bestValid = result.Valid
		}
		mergedValues = append(mergedValues, result.FinalAddresses...)
		validSheets++
		if single == nil {
			single = result
		}
	}
	if len(mergedValues) == 0 {
		return nil, fmt.Errorf("未识别到地址列（有效 EVM 地址为 0）")
	}
	// 仅一个有效 Sheet：保留 analyzeColumns 的原始统计与列信息（与旧行为一致）。
	if validSheets == 1 {
		return single, nil
	}
	summary := normalizeAddresses(strings.Join(mergedValues, "\n"))
	return &ImportResult{
		Rows:            totalRows,
		DetectedColumns: []DetectedColumn{{Name: selected, Confidence: 1}},
		SelectedColumn:  selected,
		Valid:           totalValid,
		Duplicates:      totalDup + summary.Duplicates,
		Invalid:         totalInvalid,
		FinalAddresses:  summary.Addresses,
	}, nil
}

func readCSVAll(reader *csv.Reader) ([][]string, error) {
	var all [][]string
	totalBytes := 0
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 CSV: %w", err)
		}
		totalBytes += len(bytes.Join([][]byte{[]byte(strings.Join(row, ","))}, nil))
		if totalBytes > importMaxBytes {
			return nil, fmt.Errorf("CSV 超过 %dMB 读取上限", importMaxBytes>>20)
		}
		all = append(all, row)
	}
	return all, nil
}

// looksLikeHeader 启发式：行内大部分单元格不是 EVM 地址且不全是空。
func looksLikeHeader(row []string) bool {
	nonEmpty, evm := 0, 0
	for _, cell := range row {
		if strings.TrimSpace(cell) == "" {
			continue
		}
		nonEmpty++
		if evmAddressRE.MatchString(strings.ToLower(strings.TrimSpace(cell))) {
			evm++
		}
	}
	return nonEmpty > 0 && evm == 0
}

// analyzeColumns 对内存表格做列识别 + 选中列地址统计。
func analyzeColumns(all [][]string) (*ImportResult, error) {
	if len(all) == 0 {
		return nil, fmt.Errorf("文件为空")
	}
	headerIdx := -1
	if looksLikeHeader(all[0]) {
		headerIdx = 0
	}
	start := 0
	if headerIdx >= 0 {
		start = 1
	}
	width := 0
	for _, row := range all {
		if len(row) > width {
			width = len(row)
		}
	}
	cols := make([]*DetectedColumn, width)
	for i := range cols {
		name := fmt.Sprintf("column_%d", i+1)
		if headerIdx >= 0 && i < len(all[0]) {
			if strings.TrimSpace(all[0][i]) != "" {
				name = strings.TrimSpace(all[0][i])
			}
		}
		cols[i] = &DetectedColumn{Name: name}
	}
	rows := int64(0)
	for _, row := range all[start:] {
		if !rowHasData(row) {
			continue
		}
		rows++
		for i, cell := range row {
			if i >= width {
				break
			}
			v := strings.ToLower(strings.TrimSpace(cell))
			if v == "" {
				continue
			}
			cols[i].NonEmpty++
			if evmAddressRE.MatchString(v) {
				cols[i].Valid++
			}
		}
	}
	best := -1
	bestScore := -1.0
	for i, c := range cols {
		if c.NonEmpty == 0 {
			continue
		}
		if rows > 0 {
			c.Confidence = float64(c.Valid) / float64(rows)
		}
		if c.Confidence > bestScore || (c.Confidence == bestScore && c.Valid > 0 && (best < 0 || cols[best].Valid < c.Valid)) {
			best, bestScore = i, c.Confidence
		}
	}
	if best < 0 || cols[best].Valid == 0 {
		return nil, fmt.Errorf("未识别到地址列（有效 EVM 地址为 0）")
	}
	var values []string
	for _, row := range all[start:] {
		if !rowHasData(row) {
			continue
		}
		if best < len(row) {
			values = append(values, row[best])
		}
	}
	summary := normalizeAddresses(strings.Join(values, "\n"))
	out := make([]DetectedColumn, len(cols))
	for i, c := range cols {
		out[i] = *c
	}
	return &ImportResult{
		Rows:            rows,
		DetectedColumns: out,
		SelectedColumn:  cols[best].Name,
		Valid:           summary.Valid,
		Duplicates:      summary.Duplicates,
		Invalid:         summary.Invalid,
		FinalAddresses:  summary.Addresses,
	}, nil
}

func rowHasData(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return true
		}
	}
	return false
}

type importSummary struct {
	Input        int
	Valid        int
	Duplicates   int
	Invalid      int
	Addresses    []string
	InvalidItems []string
}

// normalizeAddresses 从原始文本提取唯一 EVM 地址（与 parquetdownload 行为一致）。
func normalizeAddresses(raw string) importSummary {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(",，;；|", r)
	})
	summary := importSummary{Input: len(fields)}
	seen := map[string]bool{}
	for _, field := range fields {
		v := strings.ToLower(strings.TrimSpace(field))
		if v == "" {
			continue
		}
		if !evmAddressRE.MatchString(v) {
			summary.InvalidItems = append(summary.InvalidItems, v)
			continue
		}
		summary.Valid++
		if seen[v] {
			summary.Duplicates++
			continue
		}
		seen[v] = true
		summary.Addresses = append(summary.Addresses, v)
	}
	summary.Invalid = len(summary.InvalidItems)
	return summary
}
