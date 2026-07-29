package parser

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

// AlipayStandardTables defines the known Alipay table structures
var AlipayStandardTables = map[string][]string{
	"注册信息": {
		"用户ID", "登录邮箱", "登录手机", "账户名称", "证件类型", "证件号",
		"可用余额", "绑定手机", "注册时间", "注册时IP", "绑定银行卡",
		"关联账户", "备注", "对应的协查数据",
	},
	"登陆日志": {
		"登陆账号", "支付宝用户ID", "账户名", "客户端ip", "操作发生时间", "对应的协查数据",
	},
	"账户明细": {
		"交易号", "商户订单号", "交易创建时间", "付款时间", "最近修改时间",
		"交易来源地", "类型", "用户信息", "交易对方信息", "消费名称",
		"金额（元）", "收/支", "交易状态", "支付方式", "充值流水号",
		"备注", "对应的协查数据",
	},
	"余额明细": {
		"交易订单号/外部流水号", "账户", "对方帐户", "交易发生日期",
		"银行处理日期", "收入金额(+)（元）", "支出金额(-)（元）",
		"余额（元）", "业务类型", "交易发生地", "银行名称", "备注", "对应的协查数据",
	},
}

// AlipayUnifyTables defines which tables are unified in strict vs wide mode
var AlipayUnifyTables = map[string]map[string]bool{
	"strict": {
		"账户明细": true,
	},
	"wide": {
		"账户明细": true,
	},
}

var alipayUserInfoPattern = regexp.MustCompile(`^\s*([^()（）]+?)\s*[（(]\s*([^()（）]*?)\s*[)）]\s*$`)
var alipayBankCounterpartyPattern = regexp.MustCompile(`^\s*[（(]\s*(.*?)\s*[)）]\s*(\d+)\s*$`)
var alipayAccountCounterpartyPattern = regexp.MustCompile(`^\s*([A-Za-z0-9_.@+*\-]+)\s*[（(]\s*(.*)\s*[)）]\s*$`)
var alipayNamedBankCounterpartyPattern = regexp.MustCompile(
	`^\s*([^()（）]+?)\s*[（(]\s*([^()（）]+?)\s*[)）]\s*[（(]\s*(\d+)\s*[)）]\s*$`,
)

func SplitAlipayUserInfo(value string) (account, name string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	matches := alipayUserInfoPattern.FindStringSubmatch(value)
	if len(matches) != 3 {
		return value, ""
	}
	return strings.TrimSpace(matches[1]), strings.TrimSpace(matches[2])
}

func cleanAlipayOptionalValue(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "null") || strings.EqualFold(value, "<nil>") {
		return ""
	}
	return value
}

func SplitAlipayCounterpartyInfo(value string) (account, name, bank string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", ""
	}
	if matches := alipayNamedBankCounterpartyPattern.FindStringSubmatch(value); len(matches) == 4 {
		bankName := strings.TrimSpace(matches[2])
		if !isAlipayBankName(bankName) {
			bankName = ""
		}
		return strings.TrimSpace(matches[3]), strings.TrimSpace(matches[1]), bankName
	}
	if matches := alipayBankCounterpartyPattern.FindStringSubmatch(value); len(matches) == 3 {
		return strings.TrimSpace(matches[2]), "", strings.TrimSpace(matches[1])
	}
	if matches := alipayAccountCounterpartyPattern.FindStringSubmatch(value); len(matches) == 3 {
		return strings.TrimSpace(matches[1]), strings.TrimSpace(matches[2]), ""
	}
	return "", value, ""
}

func isAlipayBankName(value string) bool {
	for _, keyword := range []string{"银行", "信用社", "农信"} {
		if strings.Contains(value, keyword) {
			return true
		}
	}
	return false
}

func shouldUnifyAlipayTable(mode, tableType string, options MappingOptions) bool {
	if tableType == "余额明细" {
		return options.IncludeAlipayBalance
	}
	return AlipayUnifyTables[mode][tableType]
}

// UnifiedColumns for all parsers
var UnifiedColumns = append([]string{
	"交易卡号", "交易账号", "交易户名", "交易证件号码", "交易方开户行",
	"账户性质", "交易时间", "交易金额", "交易余额", "收付标志",
	"交易对手账卡号", "对手账户性质", "现金标志", "对手户名", "对手身份证号",
	"对手开户银行", "摘要说明", "交易币种", "交易网点名称", "交易发生地",
	"交易是否成功", "传票号", "IP地址", "MAC地址", "对手交易余额",
	"交易流水号", "商户流水号", "日志号", "凭证种类", "凭证号", "交易柜员号",
	"备注", "查询反馈结果原因", "数据来源",
}, append(AuditTransactionColumns, "来源表", "来源")...)

type AlipaySource struct {
	Path      string   `json:"path"`
	SheetName string   `json:"sheet_name,omitempty"`
	TableType string   `json:"table_type"`
	HeaderRow int      `json:"header_row"`
	Rows      int      `json:"rows"`
	Columns   []string `json:"columns"`
	Notes     []string `json:"notes"`
}

type AlipayResult struct {
	OutputPath  string                 `json:"output_path"`
	Sources     []AlipaySource         `json:"sources"`
	TableRows   map[string]int         `json:"table_rows"`
	UnifiedRows int                    `json:"unified_rows"`
	Mode        string                 `json:"mode"`
	Quality     map[string]interface{} `json:"quality"`
	UnifiedData [][]string             `json:"-"`
}

// ProcessAlipayDirectory processes all files in a directory for Alipay
func ProcessAlipayDirectory(sourceDir, outputDir, mode string) (*AlipayResult, error) {
	files, err := scanFiles(sourceDir)
	if err != nil {
		return nil, err
	}
	return ProcessAlipayFiles(files, outputDir, mode)
}

func ProcessAlipayFiles(files []string, outputDir, mode string) (*AlipayResult, error) {
	return ProcessAlipayFilesWithOptions(files, outputDir, mode, MappingOptions{})
}

func ProcessAlipayFilesWithOptions(files []string, outputDir, mode string, options MappingOptions) (*AlipayResult, error) {
	if mode == "" {
		mode = "strict"
	}
	if _, ok := AlipayUnifyTables[mode]; !ok {
		return nil, fmt.Errorf("unknown alipay mode: %s", mode)
	}

	files = append([]string(nil), files...)
	sort.Strings(files)
	EnsureSourceHashes(files, &options)

	type job struct {
		path      string
		sheetName string
		raw       [][]string
		note      string
	}

	numWorkers := 1

	jobs := make(chan job, numWorkers)
	results := make(chan AlipaySource, len(files)*2)
	tableRows := make(map[string]int)
	unifiedFrames := make([][]string, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Producer
	go func() {
		for _, path := range files {
			if isDelimitedFile(path) {
				source, unified, err := processAlipayCSVFile(path, mode, options)
				if err != nil {
					log.Warn().Err(err).Str("path", path).Msg("skip unreadable file")
					continue
				}
				results <- source
				if _, ok := AlipayStandardTables[source.TableType]; ok {
					mu.Lock()
					tableRows[source.TableType] += source.Rows
					unifiedFrames = append(unifiedFrames, unified...)
					mu.Unlock()
				}
				continue
			}
			sheets, err := ReadFile(path)
			if err != nil {
				log.Warn().Err(err).Str("path", path).Msg("skip unreadable file")
				continue
			}
			for sheetName, rows := range sheets {
				rows = TrimRows(rows)
				rows = NormalizeEmbeddedCSVRows(rows)
				note := ""
				if strings.Contains(path, "编码") || strings.Contains(path, "gbk") {
					note = "可能需要处理 GBK 编码"
				}
				jobs <- job{path: path, sheetName: sheetName, raw: rows, note: note}
			}
		}
		close(jobs)
	}()

	// Workers
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				headerRow := findAlipayHeaderRow(j.raw)
				if headerRow < 0 {
					results <- AlipaySource{
						Path: j.path, SheetName: j.sheetName,
						TableType: "未识别", HeaderRow: 0, Rows: 0,
						Notes: []string{j.note, "前40行未找到支付宝标准表头"},
					}
					continue
				}
				data, headers := DataFrameFromHeader(j.raw, headerRow)
				tableType := classifyAlipayTable(headers, j.path, j.sheetName)
				source := AlipaySource{
					Path: j.path, SheetName: j.sheetName,
					TableType: tableType, HeaderRow: headerRow + 1,
					Rows: len(data), Columns: headers,
				}
				if j.note != "" {
					source.Notes = []string{j.note}
				}
				results <- source

				if tableType == "未识别" {
					continue
				}

				// Normalize and store results
				mu.Lock()
				tableRows[tableType] += len(data)

				if shouldUnifyAlipayTable(mode, tableType, options) {
					unified := alipayToUnified(data, headers, SourceAuditContext{
						Provider: "支付宝", TableType: tableType, Path: j.path, Sheet: j.sheetName,
						FileHash: options.SourceHashes[j.path], HeaderRow: headerRow,
					})
					unifiedFrames = append(unifiedFrames, unified...)
				}
				mu.Unlock()
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var sources []AlipaySource
	for r := range results {
		sources = append(sources, r)
	}

	result := &AlipayResult{
		Sources:     sources,
		TableRows:   tableRows,
		UnifiedRows: len(unifiedFrames),
		Mode:        mode,
		Quality:     buildAlipayQuality(sources, unifiedFrames),
		UnifiedData: unifiedFrames,
	}

	return result, nil
}

func scanFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if SupportedSuffixes[ext] {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func isDelimitedFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv", ".tsv", ".txt":
		return true
	default:
		return false
	}
}

func processAlipayCSVFile(path, mode string, options MappingOptions) (AlipaySource, [][]string, error) {
	preview, encoding, sep, err := readCSVPreview(path, 40)
	if err != nil {
		return AlipaySource{}, nil, err
	}
	preview = NormalizeEmbeddedCSVRows(TrimRows(preview))
	headerRow := findAlipayHeaderRow(preview)
	if headerRow < 0 {
		return AlipaySource{
			Path:      path,
			SheetName: "sheet1",
			TableType: "未识别",
			HeaderRow: 0,
			Rows:      0,
			Notes:     []string{"前40行未找到支付宝标准表头"},
		}, nil, nil
	}

	_, headers := DataFrameFromHeader(preview, headerRow)
	tableType := classifyAlipayTable(headers, path, "sheet1")
	source := AlipaySource{
		Path:      path,
		SheetName: "sheet1",
		TableType: tableType,
		HeaderRow: headerRow + 1,
		Columns:   headers,
	}
	if tableType == "未识别" {
		return source, nil, nil
	}

	reader, file, err := openCSVReader(path, sep, encoding)
	if err != nil {
		return AlipaySource{}, nil, err
	}
	defer file.Close()

	var unified [][]string
	rowIndex := 0
	for {
		row, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return AlipaySource{}, nil, err
		}
		if rowIndex <= headerRow {
			rowIndex++
			continue
		}
		row = TrimRows([][]string{row})[0]
		source.Rows++
		if shouldUnifyAlipayTable(mode, tableType, options) {
			unified = append(unified, alipayToUnified([][]string{row}, headers, SourceAuditContext{
				Provider: "支付宝", TableType: tableType, Path: path, Sheet: "sheet1",
				FileHash: options.SourceHashes[path], HeaderRow: rowIndex - 1,
			})...)
		}
		rowIndex++
	}
	return source, unified, nil
}

func readCSVPreview(path string, maxRows int) ([][]string, string, rune, error) {
	sep := ','
	if strings.ToLower(filepath.Ext(path)) == ".tsv" {
		sep = '\t'
	}

	encodings := []string{"utf-8-sig", "gb18030", "utf-8"}
	for _, encoding := range encodings {
		f, err := os.Open(path)
		if err != nil {
			return nil, "", sep, err
		}
		rows, readErr := readCSVRowsLimitedWithEncoding(f, sep, encoding, maxRows)
		closeErr := f.Close()
		if readErr == nil && closeErr == nil && len(rows) > 0 && rowsAreDecodedText(rows) {
			return rows, encoding, sep, nil
		}
	}
	return nil, "", sep, fmt.Errorf("read csv preview: all encodings failed")
}

func openCSVReader(path string, sep rune, encoding string) (*csv.Reader, *os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	reader := csv.NewReader(csvReaderForEncoding(f, encoding))
	reader.Comma = sep
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	return reader, f, nil
}

func findAlipayHeaderRow(rows [][]string) int {
	// Build all known column sets
	var allColumns [][]string
	for _, cols := range AlipayStandardTables {
		allColumns = append(allColumns, cols)
	}

	bestIdx := -1
	bestScore := 0
	for i, row := range rows {
		if i >= 40 {
			break
		}
		var cells []string
		for _, c := range row {
			nc := NormalizeHeader(c)
			if nc != "" {
				cells = append(cells, nc)
			}
		}
		if len(cells) < 3 {
			continue
		}
		for _, cols := range allColumns {
			score := HeaderScore(cells, cols)
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

func classifyAlipayTable(headers []string, path, sheet string) string {
	text := fmt.Sprintf("%s %s %s", filepath.Base(path), sheet, strings.Join(headers, " "))
	textLower := strings.ToLower(text)

	// Detect provider
	if !strings.Contains(text, "支付宝") && !strings.Contains(textLower, "alipay") {
		// Could still be alipay format if columns match
	}

	bestName := "未识别"
	bestScore := 0
	for name, cols := range AlipayStandardTables {
		score := HeaderScore(headers, cols)
		if score > bestScore {
			bestScore = score
			bestName = name
		}
	}
	if bestScore < 3 {
		return "未识别"
	}
	return bestName
}

func alipayToUnified(data [][]string, headers []string, context SourceAuditContext) [][]string {
	if len(data) == 0 {
		return nil
	}

	headerMap := make(map[string]int)
	for i, h := range headers {
		headerMap[h] = i
	}
	get := func(names ...string) func(row int) string {
		return func(row int) string {
			if row >= len(data) {
				return ""
			}
			for _, name := range names {
				idx, ok := headerMap[name]
				if !ok || idx >= len(data[row]) {
					continue
				}
				if v := strings.TrimSpace(data[row][idx]); v != "" {
					return v
				}
			}
			return ""
		}
	}
	getFloat := func(names ...string) func(row int) float64 {
		return func(row int) float64 {
			return ToNumber(get(names...)(row))
		}
	}

	var result [][]string

	switch context.TableType {
	case "账户明细":
		for i := 0; i < len(data); i++ {
			row := make([]string, len(UnifiedColumns))
			account, accountName := SplitAlipayUserInfo(get("用户信息")(i))
			counterAccount, counterName, counterBank := SplitAlipayCounterpartyInfo(get("交易对方信息")(i))
			row[colIdx("交易账号")] = account
			row[colIdx("交易卡号")] = account
			row[colIdx("交易户名")] = accountName
			row[colIdx("交易方开户行")] = "支付宝"
			row[colIdx("交易对手账卡号")] = counterAccount
			row[colIdx("对手户名")] = counterName
			row[colIdx("对手开户银行")] = counterBank
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易创建时间", "付款时间", "最近修改时间")(i))
			row[colIdx("交易金额")] = FloatToStr(getFloat("金额（元）")(i))
			row[colIdx("收付标志")] = NormalizeDirection(get("收/支")(i))
			row[colIdx("摘要说明")] = cleanAlipayOptionalValue(get("消费名称")(i))
			row[colIdx("交易流水号")] = cleanAlipayOptionalValue(get("交易号")(i))
			row[colIdx("商户流水号")] = cleanAlipayOptionalValue(get("商户订单号")(i))
			row[colIdx("交易发生地")] = get("交易来源地")(i)
			row[colIdx("交易是否成功")] = get("交易状态")(i)
			row[colIdx("备注")] = get("备注")(i)
			ApplySubjectCounterpartyRoles(row, row[colIdx("收付标志")],
				PaymentParty{Account: account, Card: account, Name: accountName, Bank: "支付宝"},
				PaymentParty{Account: counterAccount, Name: counterName, Bank: counterBank},
				"支付宝账户明细持有人视角+收支字段",
			)
			ApplySourceAudit(row, context, i)
			result = append(result, row)
		}

	case "余额明细":
		for i := 0; i < len(data); i++ {
			row := make([]string, len(UnifiedColumns))
			income := getFloat("收入金额(+)（元）")(i)
			expense := getFloat("支出金额(-)（元）")(i)
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易发生日期", "银行处理日期")(i))
			row[colIdx("交易账号")] = get("账户")(i)
			if income > 0 {
				row[colIdx("交易金额")] = FloatToStr(income)
				row[colIdx("收付标志")] = "进"
			} else if expense != 0 {
				if expense < 0 {
					expense = -expense
				}
				row[colIdx("交易金额")] = FloatToStr(expense)
				row[colIdx("收付标志")] = "出"
			}
			row[colIdx("交易余额")] = FloatToStr(getFloat("余额（元）")(i))
			row[colIdx("交易对手账卡号")] = get("对方帐户")(i)
			row[colIdx("摘要说明")] = get("业务类型")(i)
			row[colIdx("交易发生地")] = get("交易发生地")(i)
			row[colIdx("交易方开户行")] = get("银行名称")(i)
			row[colIdx("备注")] = get("备注")(i)
			ApplySubjectCounterpartyRoles(row, row[colIdx("收付标志")],
				PaymentParty{Account: get("账户")(i), Bank: get("银行名称")(i)},
				PaymentParty{Account: get("对方帐户")(i)},
				"支付宝余额明细账户+收入/支出金额",
			)
			ApplySourceAudit(row, context, i)
			result = append(result, row)
		}

	}

	return result
}

func colIdx(name string) int {
	for i, c := range UnifiedColumns {
		if c == name {
			return i
		}
	}
	return -1
}

func mapToMap(headerMap map[string]int, data [][]string) map[string][]string {
	result := make(map[string][]string)
	for name, idx := range headerMap {
		col := make([]string, len(data))
		for i, row := range data {
			if idx < len(row) {
				col[i] = row[idx]
			}
		}
		result[name] = col
	}
	return result
}

func buildAlipayQuality(sources []AlipaySource, unifiedFrames [][]string) map[string]interface{} {
	unknownCount := 0
	for _, s := range sources {
		if s.TableType == "未识别" {
			unknownCount++
		}
	}
	return map[string]interface{}{
		"summary": map[string]interface{}{
			"统一流水行数":       len(unifiedFrames),
			"识别文件或Sheet数":  len(sources),
			"未识别文件或Sheet数": unknownCount,
		},
	}
}
