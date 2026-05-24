package parser

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

// WechatTables defines known WeChat table structures
var WechatTables = map[string][]string{
	"交易明细信息": {
		"用户ID", "交易单号", "大单号", "用户侧账号名称", "借贷类型",
		"交易业务类型", "交易用途类型", "交易时间", "交易金额(分)", "账户余额(分)",
		"用户银行卡号", "用户侧银行名称", "用户侧网银联单号", "网联/银联",
		"第三方账户名称", "对手方ID", "对手侧账户名称", "对手方银行卡号",
		"对手侧银行名称", "对手侧网银联单号", "网联/银联.1", "第三方账户名称.1",
		"对手方接收时间", "对手方接收金额(分)", "备注1", "备注2",
	},
	"账户信息": {
		"账户状态", "账号", "注册姓名", "注册时间", "注册身份证号",
		"绑定手机", "绑定状态", "开户行信息", "银行账号",
	},
	"微信账单": {
		"交易单号", "交易时间", "交易类型", "收/支/其他", "交易方式",
		"金额(元)", "交易对方", "商户单号",
	},
	"微信账单明细": {
		"交易时间", "交易类型", "交易对方", "商品", "收/支",
		"金额(元)", "支付方式", "当前状态", "交易单号", "商户单号", "备注",
	},
	"支付流水汇总": {
		"序号", "支付订单号", "交易类型", "支付类型", "交易主体的出入账标识",
		"交易时间", "币种", "交易金额", "交易流水号", "交易余额",
		"收款方银行卡所属银行名称", "收款方银行卡所属银行卡号",
		"收款方的支付帐号", "收款方的商户名称",
		"付款方银行卡所属银行名称", "付款方银行卡所属银行卡号",
		"付款方的支付帐号", "交易支付设备ip", "mac地址", "备注",
	},
}

// WechatUnifyTables defines which WeChat tables are unified
var WechatUnifyTables = map[string]bool{
	"交易明细信息": true, "微信账单": true, "微信账单明细": true, "支付流水汇总": true,
}

type WechatSource struct {
	Path      string   `json:"path"`
	SheetName string   `json:"sheet_name,omitempty"`
	TableType string   `json:"table_type"`
	HeaderRow int      `json:"header_row"`
	Rows      int      `json:"rows"`
	Columns   []string `json:"columns"`
	Notes     []string `json:"notes"`
}

type WechatResult struct {
	OutputPath  string                 `json:"output_path"`
	TableRows   map[string]int         `json:"table_rows"`
	UnifiedRows int                    `json:"unified_rows"`
	Sources     []WechatSource          `json:"sources"`
	Quality     map[string]interface{} `json:"quality"`
}

// ProcessWechatDirectory processes all files in a directory for WeChat
func ProcessWechatDirectory(sourceDir, outputDir string) (*WechatResult, error) {
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
	results := make(chan WechatSource, len(files)*2)
	var mu sync.Mutex
	tableFrames := make(map[string][][]string)
	unifiedFrames := make([][]string, 0)

	numWorkers := 4
	if len(files) < numWorkers {
		numWorkers = len(files)
	}
	if numWorkers < 1 {
		numWorkers = 1
	}
	var wg sync.WaitGroup

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
				jobs <- job{path: path, sheetName: sheetName, raw: rows, note: note}
			}
		}
		close(jobs)
	}()

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				headerRow := findWechatHeaderRow(j.raw)
				if headerRow < 0 {
					results <- WechatSource{
						Path: j.path, SheetName: j.sheetName,
						TableType: "未识别", HeaderRow: 0, Rows: 0,
						Notes: []string{j.note, "前40行未找到微信标准表头"},
					}
					continue
				}
				data, headers := DataFrameFromHeader(j.raw, headerRow)
				tableType := classifyWechatTable(headers, j.path, j.sheetName)
				source := WechatSource{
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

				mu.Lock()
				if WechatUnifyTables[tableType] {
					unified := wechatToUnified(data, headers, tableType, j.path, headerRow)
					unifiedFrames = append(unifiedFrames, unified...)
				}
				tableFrames[tableType] = append(tableFrames[tableType], headers)
				tableFrames[tableType] = append(tableFrames[tableType], data...)
				mu.Unlock()
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var sources []WechatSource
	for r := range results {
		sources = append(sources, r)
	}

	tableRows := make(map[string]int)
	for k, v := range tableFrames {
		tableRows[k] = len(v) - 1
	}

	result := &WechatResult{
		Sources:     sources,
		TableRows:   tableRows,
		UnifiedRows: len(unifiedFrames),
		Quality:     buildWechatQuality(sources, tableFrames, unifiedFrames, tableRows),
	}

	return result, nil
}

func findWechatHeaderRow(rows [][]string) int {
	var allColumns [][]string
	for _, cols := range WechatTables {
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

func classifyWechatTable(headers []string, path, sheet string) string {
	bestName := "未识别"
	bestScore := 0
	for name, cols := range WechatTables {
		score := HeaderScore(headers, cols)
		if score > bestScore {
			bestScore = score
			bestName = name
		}
	}
	if bestScore < 3 {
		// Check for common WeChat text patterns
		text := fmt.Sprintf("%s %s", filepath.Base(path), sheet)
		textLower := strings.ToLower(text)
		if strings.Contains(text, "微信") || strings.Contains(textLower, "wechat") || strings.Contains(text, "财付通") {
			return "微信账单" // best guess
		}
		return "未识别"
	}
	return bestName
}

func wechatToUnified(data [][]string, headers []string, tableType, path string, headerRow int) [][]string {
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
			v := FirstNonEmpty(mapToMap(headerMap, data), names, row)
			if v == "" {
				return 0
			}
			return ToNumber(v)
		}
	}

	var result [][]string

	switch tableType {
	case "交易明细信息":
		for i := 0; i < len(data); i++ {
			row := make([]string, len(UnifiedColumns))
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易时间")(i))

			// 交易金额(分) -> 元
			amountFen := getFloat("交易金额(分)")(i)
			row[colIdx("交易金额")] = FloatToStr(amountFen / 100)

			// 账户余额(分) -> 元
			balanceFen := getFloat("账户余额(分)")(i)
			row[colIdx("交易余额")] = FloatToStr(balanceFen / 100)

			row[colIdx("收付标志")] = NormalizeDirection(get("借贷类型")(i))
			row[colIdx("交易卡号")] = CleanAccountNumber(get("用户银行卡号")(i))
			row[colIdx("交易账号")] = get("用户侧账号名称")(i)
			row[colIdx("交易方开户行")] = get("用户侧银行名称")(i)
			row[colIdx("交易对手账卡号")] = CleanAccountNumber(get("对手方银行卡号")(i))
			row[colIdx("对手户名")] = get("对手侧账户名称")(i)
			row[colIdx("对手开户银行")] = get("对手侧银行名称")(i)
			row[colIdx("对手身份证号")] = get("对手方ID")(i)
			row[colIdx("摘要说明")] = get("交易业务类型", "交易用途类型")(i)
			row[colIdx("交易流水号")] = get("交易单号")(i)
			row[colIdx("备注")] = get("备注1", "备注2")(i)

			// 对手方接收金额(分) -> 元
			recvFen := getFloat("对手方接收金额(分)")(i)
			row[colIdx("对手交易余额")] = FloatToStr(recvFen / 100)

			row[colIdx("来源表")] = SourceLocation(path, i, headerRow)
			result = append(result, row)
		}

	case "微信账单", "微信账单明细":
		for i := 0; i < len(data); i++ {
			row := make([]string, len(UnifiedColumns))
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易时间")(i))
			row[colIdx("交易金额")] = FloatToStr(getFloat("金额(元)")(i))
			row[colIdx("收付标志")] = NormalizeDirection(get("收/支/其他", "收/支")(i))
			row[colIdx("对手户名")] = get("交易对方")(i)
			row[colIdx("摘要说明")] = get("商品", "交易类型", "交易方式")(i)
			row[colIdx("交易是否成功")] = get("当前状态")(i)
			row[colIdx("交易流水号")] = get("交易单号")(i)
			row[colIdx("备注")] = get("备注", "商户单号")(i)
			row[colIdx("来源表")] = SourceLocation(path, i, headerRow)
			result = append(result, row)
		}

	case "支付流水汇总":
		for i := 0; i < len(data); i++ {
			row := make([]string, len(UnifiedColumns))
			row[colIdx("交易账号")] = get("付款方的支付帐号", "收款方的支付帐号")(i)
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易时间")(i))
			row[colIdx("交易金额")] = FloatToStr(getFloat("交易金额")(i))
			row[colIdx("交易余额")] = FloatToStr(getFloat("交易余额")(i))
			row[colIdx("收付标志")] = NormalizeDirection(get("交易主体的出入账标识")(i))
			row[colIdx("交易对手账卡号")] = get("收款方的支付帐号", "付款方的支付帐号",
				"收款方银行卡所属银行卡号", "付款方银行卡所属银行卡号")(i)
			row[colIdx("对手户名")] = get("收款方的商户名称")(i)
			row[colIdx("摘要说明")] = get("交易类型", "支付类型")(i)
			row[colIdx("交易币种")] = get("币种")(i)
			row[colIdx("IP地址")] = get("交易支付设备ip")(i)
			row[colIdx("MAC地址")] = get("mac地址")(i)
			row[colIdx("交易流水号")] = get("交易流水号", "支付订单号")(i)
			row[colIdx("备注")] = get("备注")(i)
			row[colIdx("来源表")] = SourceLocation(path, i, headerRow)
			result = append(result, row)
		}
	}

	return result
}

func buildWechatQuality(sources []WechatSource, tableFrames map[string][][]string, unifiedFrames [][]string, tableRows map[string]int) map[string]interface{} {
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
