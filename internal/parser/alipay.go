package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

// AlipayStandardTables defines the known Alipay table structures
var AlipayStandardTables = map[string][]string{
	"交易记录": {
		"交易号", "外部交易号", "交易状态", "合作伙伴ID", "买家用户id", "买家信息",
		"卖家用户id", "卖家信息", "交易金额（元）", "收款时间", "最后修改时间",
		"创建时间", "交易类型", "来源地", "商品名称", "收货人地址", "对应的协查数据",
	},
	"注册信息": {
		"用户ID", "登录邮箱", "登录手机", "账户名称", "证件类型", "证件号",
		"可用余额", "绑定手机", "绑定银行卡", "关联账户", "对应的协查数据",
	},
	"登陆日志": {
		"登陆账号", "支付宝用户ID", "账户名", "客户端ip", "操作发生时间", "对应的协查数据",
	},
	"账户明细": {
		"交易号", "商户订单号", "交易创建时间", "付款时间", "最近修改时间",
		"交易来源地", "类型", "用户信息", "交易对方信息", "消费名称",
		"金额（元）", "收/支", "交易状态", "备注", "对应的协查数据",
	},
	"转账明细": {
		"交易号", "付款方支付宝账号", "收款方支付宝账号", "收款机构信息",
		"到账时间", "转账金额（元）", "转账产品名称", "交易发生地",
		"提现流水号", "对应的协查数据",
	},
	"余额明细": {
		"交易订单号/外部流水号", "账户", "对方帐户", "交易发生日期",
		"银行处理日期", "收入金额(+)（元）", "支出金额(-)（元）",
		"余额（元）", "业务类型", "交易发生地", "银行名称", "备注", "对应的协查数据",
	},
	"支付流水汇总": {
		"序号", "支付订单号", "交易类型", "支付类型", "交易主体的出入账标识",
		"交易时间", "币种", "交易金额", "交易流水号", "交易余额",
		"收款方银行卡所属银行名称", "收款方银行卡所属银行卡号",
		"收款方的支付帐号", "收款方的商户名称",
		"付款方银行卡所属银行名称", "付款方银行卡所属银行卡号",
		"付款方的支付帐号", "交易支付设备ip", "mac地址", "备注",
	},
	"个人账单": {
		"收/支", "交易对方", "商品说明", "收/付款方式", "金额",
		"交易订单号", "商家订单号", "交易时间",
	},
}

// AlipayUnifyTables defines which tables are unified in strict vs wide mode
var AlipayUnifyTables = map[string]map[string]bool{
	"strict": {
		"账户明细": true, "余额明细": true, "转账明细": true,
		"支付流水汇总": true, "个人账单": true,
	},
	"wide": {
		"账户明细": true, "余额明细": true, "转账明细": true,
		"支付流水汇总": true, "个人账单": true, "交易记录": true,
	},
}

// UnifiedColumns for all parsers
var UnifiedColumns = []string{
	"交易卡号", "交易账号", "交易户名", "交易证件号码", "交易方开户行",
	"账户性质", "交易时间", "交易金额", "交易余额", "收付标志",
	"交易对手账卡号", "对手账户性质", "现金标志", "对手户名", "对手身份证号",
	"对手开户银行", "摘要说明", "交易币种", "交易网点名称", "交易发生地",
	"交易是否成功", "传票号", "IP地址", "MAC地址", "对手交易余额",
	"交易流水号", "日志号", "凭证种类", "凭证号", "交易柜员号",
	"备注", "查询反馈结果原因", "数据来源", "来源表", "来源文件",
}

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
	OutputPath  string                `json:"output_path"`
	Sources     []AlipaySource        `json:"sources"`
	TableRows   map[string]int        `json:"table_rows"`
	UnifiedRows int                   `json:"unified_rows"`
	Mode        string                `json:"mode"`
	Quality     map[string]interface{} `json:"quality"`
}

// ProcessAlipayDirectory processes all files in a directory for Alipay
func ProcessAlipayDirectory(sourceDir, outputDir, mode string) (*AlipayResult, error) {
	if mode == "" {
		mode = "strict"
	}
	if _, ok := AlipayUnifyTables[mode]; !ok {
		return nil, fmt.Errorf("unknown alipay mode: %s", mode)
	}

	// Scan files
	files, err := scanFiles(sourceDir)
	if err != nil {
		return nil, err
	}

	type job struct {
		path      string
		sheetName string
		raw       [][]string
		note      string
	}

	jobs := make(chan job, len(files)*2)
	results := make(chan AlipaySource, len(files)*2)
	tableFrames := make(map[string][][]string)
	unifiedFrames := make([][]string, 0)
	var mu sync.Mutex

	// Worker pool
	numWorkers := 4
	if len(files) < numWorkers {
		numWorkers = len(files)
	}
	if numWorkers < 1 {
		numWorkers = 1
	}
	var wg sync.WaitGroup

	// Producer
	go func() {
		for _, path := range files {
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
				tableFrames[tableType] = append(tableFrames[tableType], headers)
				tableFrames[tableType] = append(tableFrames[tableType], data...)

				unifyTables, _ := AlipayUnifyTables[mode]
				if unifyTables[tableType] {
					unified := alipayToUnified(data, headers, tableType, j.path, headerRow)
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

	// Build output
	tableRows := make(map[string]int)
	for k, v := range tableFrames {
		tableRows[k] = len(v) - 1 // minus header
	}

	result := &AlipayResult{
		Sources:     sources,
		TableRows:   tableRows,
		UnifiedRows: len(unifiedFrames),
		Mode:        mode,
		Quality:     buildAlipayQuality(sources, tableFrames, unifiedFrames, tableRows),
	}

	return result, nil
}

func scanFiles(dir string) ([]string, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		info, err := os.Stat(e)
		if err != nil || info.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e))
		if SupportedSuffixes[ext] {
			files = append(files, e)
		}
	}
	sort.Strings(files)
	return files, nil
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

func alipayToUnified(data [][]string, headers []string, tableType, path string, headerRow int) [][]string {
	if len(data) == 0 {
		return nil
	}

	headerMap := make(map[string]int)
	for i, h := range headers {
		headerMap[h] = i
	}

	get := func(names ...string) func(row int) string {
		return func(row int) string {
			return FirstNonEmpty(mapToMap(headerMap, data), names, row)
		}
	}
	getFloat := func(names ...string) func(row int) float64 {
		return func(row int) float64 {
			return ToNumber(FirstNonEmpty(mapToMap(headerMap, data), names, row))
		}
	}

	var result [][]string

	switch tableType {
	case "账户明细":
		for i := 0; i < len(data); i++ {
			row := make([]string, len(UnifiedColumns))
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易创建时间", "付款时间", "最近修改时间")(i))
			row[colIdx("交易金额")] = FloatToStr(getFloat("金额（元）")(i))
			row[colIdx("收付标志")] = NormalizeDirection(get("收/支")(i))
			row[colIdx("对手户名")] = get("交易对方信息")(i)
			row[colIdx("摘要说明")] = get("消费名称", "类型")(i)
			row[colIdx("交易流水号")] = get("交易号", "商户订单号")(i)
			row[colIdx("交易发生地")] = get("交易来源地")(i)
			row[colIdx("交易是否成功")] = get("交易状态")(i)
			row[colIdx("备注")] = get("备注")(i)
			row[colIdx("来源表")] = SourceLocation(path, i, headerRow)
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
			} else if expense > 0 {
				row[colIdx("交易金额")] = FloatToStr(expense)
				row[colIdx("收付标志")] = "出"
			}
			row[colIdx("交易余额")] = FloatToStr(getFloat("余额（元）")(i))
			row[colIdx("交易对手账卡号")] = get("对方帐户")(i)
			row[colIdx("摘要说明")] = get("业务类型")(i)
			row[colIdx("交易发生地")] = get("交易发生地")(i)
			row[colIdx("交易方开户行")] = get("银行名称")(i)
			row[colIdx("备注")] = get("备注")(i)
			row[colIdx("来源表")] = SourceLocation(path, i, headerRow)
			result = append(result, row)
		}

	case "转账明细":
		for i := 0; i < len(data); i++ {
			row := make([]string, len(UnifiedColumns))
			row[colIdx("交易金额")] = FloatToStr(getFloat("转账金额（元）")(i))
			row[colIdx("交易账号")] = get("付款方支付宝账号")(i)
			row[colIdx("交易对手账卡号")] = get("收款方支付宝账号")(i)
			row[colIdx("交易流水号")] = get("交易号")(i)
			row[colIdx("摘要说明")] = get("转账产品名称")(i)
			row[colIdx("交易发生地")] = get("交易发生地")(i)
			row[colIdx("交易时间")] = NormalizeDatetime(get("到账时间")(i))
			row[colIdx("收付标志")] = "出"
			row[colIdx("来源表")] = SourceLocation(path, i, headerRow)
			result = append(result, row)
		}

	case "支付流水汇总":
		for i := 0; i < len(data); i++ {
			row := make([]string, len(UnifiedColumns))
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易时间")(i))
			row[colIdx("交易金额")] = FloatToStr(getFloat("交易金额")(i))
			row[colIdx("收付标志")] = NormalizeDirection(get("交易主体的出入账标识")(i))
			row[colIdx("交易余额")] = FloatToStr(getFloat("交易余额")(i))
			row[colIdx("交易币种")] = get("币种")(i)
			row[colIdx("交易流水号")] = get("交易流水号", "支付订单号")(i)
			row[colIdx("交易对手账卡号")] = get("收款方的支付帐号", "付款方的支付帐号",
				"收款方银行卡所属银行卡号", "付款方银行卡所属银行卡号")(i)
			row[colIdx("对手户名")] = get("收款方的商户名称")(i)
			row[colIdx("摘要说明")] = get("交易类型", "支付类型")(i)
			row[colIdx("IP地址")] = get("交易支付设备ip")(i)
			row[colIdx("MAC地址")] = get("mac地址")(i)
			row[colIdx("备注")] = get("备注")(i)
			row[colIdx("来源表")] = SourceLocation(path, i, headerRow)
			result = append(result, row)
		}

	case "个人账单":
		for i := 0; i < len(data); i++ {
			row := make([]string, len(UnifiedColumns))
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易时间")(i))
			row[colIdx("交易金额")] = FloatToStr(getFloat("金额")(i))
			row[colIdx("收付标志")] = NormalizeDirection(get("收/支")(i))
			row[colIdx("对手户名")] = get("交易对方")(i)
			row[colIdx("摘要说明")] = get("商品说明")(i)
			row[colIdx("交易流水号")] = get("交易订单号", "商家订单号")(i)
			row[colIdx("备注")] = get("收/付款方式")(i)
			row[colIdx("来源表")] = SourceLocation(path, i, headerRow)
			result = append(result, row)
		}

	case "交易记录":
		for i := 0; i < len(data); i++ {
			row := make([]string, len(UnifiedColumns))
			row[colIdx("交易金额")] = FloatToStr(getFloat("交易金额（元）")(i))
			row[colIdx("交易时间")] = NormalizeDatetime(get("创建时间", "收款时间", "最后修改时间")(i))
			row[colIdx("交易流水号")] = get("交易号", "外部交易号")(i)
			row[colIdx("交易是否成功")] = get("交易状态")(i)
			row[colIdx("交易账号")] = get("买家信息", "买家用户id")(i)
			row[colIdx("交易对手账卡号")] = get("卖家信息", "卖家用户id")(i)
			row[colIdx("摘要说明")] = get("商品名称", "交易类型")(i)
			row[colIdx("交易发生地")] = get("来源地")(i)
			row[colIdx("来源表")] = SourceLocation(path, i, headerRow)
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

func buildAlipayQuality(sources []AlipaySource, tableFrames map[string][][]string, unifiedFrames [][]string, tableRows map[string]int) map[string]interface{} {
	unknownCount := 0
	for _, s := range sources {
		if s.TableType == "未识别" {
			unknownCount++
		}
	}
	return map[string]interface{}{
		"summary": map[string]interface{}{
			"统一流水行数":      len(unifiedFrames),
			"识别文件或Sheet数":  len(sources),
			"未识别文件或Sheet数": unknownCount,
		},
	}
}


