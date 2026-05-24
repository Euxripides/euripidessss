package rules

import (
	"regexp"
	"strings"

	"github.com/etl/backend/internal/parser"
)

// Bank transaction and account column constants
var BankTransactionColumns = append(parser.UnifiedColumns, "来源表", "来源文件", "来源Sheet", "来源")

var BankAccountColumns = []string{
	"账户开户名称", "开户人证件号码", "交易卡号", "交易账号", "账号开户时间",
	"账户余额", "可用余额", "币种", "开户网点代码", "开户网点", "账户状态",
	"销户日期", "账户类型", "备注", "账号开户银行", "销户网点", "最后交易时间", "证件类型",
}

// BankFinalTables defines the output table structure
var BankFinalTables = map[string][]string{
	"交易明细信息": {
		"交易卡号", "交易账号", "交易方户名", "交易方证件号码", "交易时间", "交易金额",
		"交易余额", "收付标志", "交易对手账卡号", "现金标志", "对手户名", "对手身份证号",
		"对手开户银行", "摘要说明", "交易币种", "交易网点名称", "交易发生地", "交易是否成功",
		"传票号", "IP地址", "MAC地址", "对手交易余额", "交易流水号", "日志号", "凭证种类",
		"凭证号", "交易柜员号", "备注", "商户名称", "交易类型", "查询反馈结果原因", "来源",
	},
	"账户信息": {
		"账户开户名称", "开户人证件号码", "交易卡号", "交易账号", "账号开户时间",
		"账户余额", "可用余额", "币种", "开户网点代码", "开户网点", "账户状态",
		"钞汇标志名称", "开户人证件类型", "销户日期", "账户类型", "开户联系方式",
		"通信地址", "联系电话", "代理人", "代理人电话", "备注", "开户省份", "开户城市",
		"账号开户银行", "客户代码", "法人代表", "客户工商执照号码", "法人代表证件号码",
		"住宅地址", "邮政编码", "代办人证件号码", "邮箱地址", "关联资金账户", "地税纳税号",
		"单位电话", "代办人证件类型", "住宅电话", "法人代表证件类型", "国税纳税号",
		"单位地址", "工作单位", "销户网点", "最后交易时间", "账户销户银行", "任务流水号", "来源",
	},
	"子账户信息": {
		"银行名称", "开户账号", "子账户账号", "余额", "可用余额", "子账户类别",
		"子账户序号", "币种", "钞汇标识", "账户状态", "账户人姓名", "账户序号", "备注", "来源",
	},
	"银行人员信息": {
		"客户名称", "证照类型", "证照号码", "单位地址", "联系电话", "联系手机",
		"单位电话", "住宅电话", "工作单位", "邮箱地址", "代办人姓名", "代办人证件类型",
		"代办人证件号码", "国税纳税号", "地税纳税号", "法人代表", "法人代表证件类型",
		"法人代表证件号码", "出生日期", "户籍地址", "客户工商执照号码", "来源",
	},
	"银行人员联系方式": {"开户名称", "证照类型", "证照号码", "联系电话", "来源"},
	"银行人员住址":     {"开户名称", "证照类型", "证照号码", "住宅地址", "住宅电话", "来源"},
	"银行任务信息": {
		"任务流水号", "银行名称", "主体类别", "证账号码", "账卡号", "发送时间",
		"反馈时间", "反馈结果", "反馈非明细结果", "反馈明细结果", "入库时间",
		"入库状态", "请求单号", "查询结果", "来源",
	},
	"银行强制措施": {
		"银行名称", "账号", "冻结措施类型", "冻结措施类型名称", "冻结金额",
		"冻结机关", "冻结开始日期", "冻结截止日期", "措施序号", "备注",
		"任务流水号", "查控主体类别", "来源",
	},
}

// BankTables defines known bank table column signatures
var BankTables = map[string][]string{
	"交易明细":       parser.UnifiedColumns,
	"账户信息":       BankAccountColumns,
	"查询账号流水":     {"查询账号", "对方账号姓名", "对方账号卡号", "金额", "余额", "借贷标志", "交易类型", "交易结果", "交易时间", "交易流水号"},
	"信用卡账单":      {"交易日期", "入账日期", "交易描述", "交易金额", "交易货币", "入账金额", "入账货币", "MCC代码"},
	"招商交易流水":     {"交易日期", "交易时间", "客户名称", "交易卡号", "交易方向(D:借|C:贷)", "交易金额", "联机余额", "对方帐号", "对方客户名称", "交易摘要", "账号"},
	"招商客户资料":     {"客户姓名", "户口号", "开户机构", "开户日期", "户口状态", "证件类型", "证件号码", "活期帐户余额", "户口名称"},
	"任务信息":       {"任务流水号", "银行名称", "主体类别", "证账号码", "账卡号", "反馈结果", "反馈明细结果", "查询结果"},
	"人员信息":       BankFinalTables["银行人员信息"],
	"人员联系方式":     BankFinalTables["银行人员联系方式"],
	"人员住址":       BankFinalTables["银行人员住址"],
	"强制措施":       BankFinalTables["银行强制措施"],
	"子账户信息":      BankFinalTables["子账户信息"],
}

// Transaction table types that go into unified transaction output
var TransactionTableTypes = map[string]bool{
	"交易明细": true, "查询账号流水": true, "信用卡账单": true,
	"招商交易流水": true, "交易明细信息": true,
}

// Account table types that go into account output
var AccountTableTypes = map[string]bool{
	"账户信息": true, "招商客户资料": true,
}

var FailedFeedbackPattern = regexp.MustCompile(`(?i)(查询失败|失败|无记录|无此记录|查无此|no record)`)

// Column aliases for bank transaction mapping
var TxAliases = map[string][]string{
	"交易卡号":     {"交易卡号", "交易卡", "卡号", "银行卡号", "本方卡号", "付款方账号", "收款方账号", "账户", "查询账号"},
	"交易账号":     {"交易账号", "账号", "银行账号", "本方账号", "支付宝账号", "微信号", "商户号"},
	"交易户名":     {"交易户名", "本方户名", "账户名称", "客户名称", "付款方姓名", "收款方姓名"},
	"交易证件号码":   {"交易证件号码", "证件号码", "身份证号", "开户人证件号码"},
	"交易方开户行":   {"交易方开户行", "开户银行", "账号开户银行", "开户行"},
	"交易时间":     {"交易时间", "交易日期", "账务日期", "入账时间", "支付时间", "创建时间", "发生时间", "记账日期", "交易创建时间"},
	"交易金额":     {"交易金额", "金额", "收入金额", "支出金额", "收/支", "收入（+元）", "支出（-元）", "交易额", "付款金额"},
	"交易余额":     {"交易余额", "余额", "账户余额", "可用余额"},
	"收付标志":     {"收付标志", "借贷标志", "借贷方向", "收支类型", "收/支", "收入/支出", "方向", "业务类型"},
	"交易对手账卡号":  {"交易对手账卡号", "对方账号", "对手账号", "对方卡号", "对手卡号", "交易对方账号", "对方账户", "对方帐号"},
	"现金标志":     {"现金标志", "现转标志", "现金/转账"},
	"对手户名":     {"对手户名", "对方户名", "对方姓名", "交易对方", "对方名称", "商户名称", "交易对手", "对方客户名称"},
	"对手身份证号":   {"对手身份证号", "对方证件号", "对手证件号"},
	"对手开户银行":   {"对手开户银行", "对方开户行", "对手开户行", "对方银行"},
	"摘要说明":     {"摘要说明", "摘要", "交易摘要", "商品说明", "交易说明", "备注说明", "用途", "交易类型", "交易摘要"},
	"交易币种":     {"交易币种", "币种", "货币"},
	"交易网点名称":   {"交易网点名称", "交易网点", "网点名称"},
	"交易发生地":    {"交易发生地", "发生地", "交易地点"},
	"交易是否成功":   {"交易是否成功", "交易状态", "状态", "交易结果"},
	"传票号":      {"传票号"},
	"IP地址":     {"IP地址", "IP"},
	"MAC地址":    {"MAC地址", "MAC"},
	"对手交易余额":   {"对手交易余额", "对方余额"},
	"交易流水号":    {"交易流水号", "流水号", "交易号", "商户订单号", "订单号", "微信支付订单号", "支付宝交易号"},
	"日志号":      {"日志号"},
	"凭证种类":     {"凭证种类", "凭证类型"},
	"凭证号":      {"凭证号"},
	"交易柜员号":    {"交易柜员号", "柜员号"},
	"备注":       {"备注", "附言", "说明"},
	"查询反馈结果原因": {"查询反馈结果原因", "反馈原因", "失败原因", "返回原因"},
}

// Account aliases
var AccountAliases = map[string][]string{
	"账户开户名称":   {"账户开户名称", "开户名称", "客户名称", "户名", "姓名", "账户名称"},
	"开户人证件号码":  {"开户人证件号码", "证件号码", "身份证号", "证件号"},
	"交易卡号":     {"交易卡号", "卡号", "银行卡号", "账户"},
	"交易账号":     {"交易账号", "账号", "银行账号", "账户账号"},
	"账号开户时间":   {"账号开户时间", "开户时间", "开户日期"},
	"账户余额":     {"账户余额", "余额", "活期帐户余额"},
	"可用余额":     {"可用余额"},
	"币种":       {"币种", "货币"},
	"开户网点代码":   {"开户网点代码", "网点代码"},
	"开户网点":     {"开户网点", "网点名称", "开户机构"},
	"账户状态":     {"账户状态", "户口状态", "状态", "账户状态"},
	"销户日期":     {"销户日期"},
	"账户类型":     {"账户类型", "户口名称", "账户性质"},
	"备注":       {"备注"},
	"账号开户银行":   {"账号开户银行", "开户银行"},
	"销户网点":     {"销户网点"},
	"最后交易时间":   {"最后交易时间"},
	"证件类型":     {"证件类型", "证照类型"},
}

// ClassifyTable classifies a bank table based on headers
func ClassifyTable(headers []string, filename, sheetName string) string {
	text := filename + " " + sheetName + " " + strings.Join(headers, " ")
	textLower := strings.ToLower(text)

	bestName := "未识别"
	bestScore := 0

	// Custom rules first
	// Then standard tables
	for name, cols := range BankTables {
		score := parser.HeaderScore(headers, cols)
		if score > bestScore {
			bestScore = score
			bestName = name
		}
	}

	if bestScore >= 3 {
		return bestName
	}

	// Provider detection
	if strings.Contains(text, "银行") || strings.Contains(textLower, "bank") {
		return "交易明细"
	}
	return "未识别"
}

// NormalizeTransaction maps bank transaction columns to standard format
func NormalizeTransaction(headers []string, data [][]string, tableType, filename, sheetName string) (map[string][]string, error) {
	result := make(map[string][]string)
	for _, col := range BankTransactionColumns {
		result[col] = make([]string, len(data))
	}

	// Map columns based on table type
	switch tableType {
	case "交易明细":
		mapColumns(headers, data, result, TxAliases)
	case "查询账号流水":
		mapQueryAccountFlow(headers, data, result)
	case "信用卡账单":
		mapCreditCardBill(headers, data, result)
	case "招商交易流水":
		mapCMBTransaction(headers, data, result)
	default:
		// Generic mapping
		mapColumns(headers, data, result, TxAliases)
	}

	// Set source file info
	for i := range data {
		result["来源文件"][i] = filename
		result["来源Sheet"][i] = sheetName
		result["来源"][i] = filename
	}

	return result, nil
}

// NormalizeAccount maps bank account columns to standard format
func NormalizeAccount(headers []string, data [][]string, tableType, filename, sheetName string) (map[string][]string, error) {
	result := make(map[string][]string)
	for _, col := range BankAccountColumns {
		result[col] = make([]string, len(data))
	}

	switch tableType {
	case "账户信息":
		mapColumns(headers, data, result, AccountAliases)
	case "招商客户资料":
		mapCMBAccount(headers, data, result)
	default:
		mapColumns(headers, data, result, AccountAliases)
	}

	for i := range data {
		result["来源文件"][i] = filename
		result["来源Sheet"][i] = sheetName
		result["来源"][i] = filename
	}

	return result, nil
}

// CleanTransactions cleans and deduplicates transaction data
func CleanTransactions(txns map[string][]string) map[string][]string {
	if len(txns) == 0 || len(txns[BankTransactionColumns[0]]) == 0 {
		return txns
	}
	n := len(txns[BankTransactionColumns[0]])

	// Clean account numbers
	for _, col := range []string{"交易卡号", "交易账号", "交易对手账卡号"} {
		if txns[col] != nil {
			for i := 0; i < n; i++ {
				txns[col][i] = parser.CleanAccountNumber(txns[col][i])
			}
		}
	}

	// Normalize amounts (absolute value)
	if txns["交易金额"] != nil {
		for i := 0; i < n; i++ {
			val := parser.ToNumber(txns["交易金额"][i])
			if val < 0 {
				val = -val
			}
			txns["交易金额"][i] = parser.FloatToStr(val)
		}
	}

	// Normalize datetime
	if txns["交易时间"] != nil {
		for i := 0; i < n; i++ {
			txns["交易时间"][i] = parser.NormalizeDatetime(txns["交易时间"][i])
		}
	}

	// Normalize direction
	if txns["收付标志"] != nil {
		for i := 0; i < n; i++ {
			txns["收付标志"][i] = parser.NormalizeDirection(txns["收付标志"][i])
		}
	}

	// Remove rows with failed feedback
	keep := make([]bool, n)
	for i := 0; i < n; i++ {
		keep[i] = true
	}
	if txns["查询反馈结果原因"] != nil {
		for i := 0; i < n; i++ {
			if FailedFeedbackPattern.MatchString(txns["查询反馈结果原因"][i]) {
				keep[i] = false
			}
		}
	}

	// Remove rows missing required fields
	requiredFields := []string{"交易时间", "交易金额"}
	for i := 0; i < n; i++ {
		if !keep[i] {
			continue
		}
		for _, f := range requiredFields {
			if txns[f] == nil || strings.TrimSpace(txns[f][i]) == "" || txns[f][i] == "0" {
				keep[i] = false
				break
			}
		}
	}

	return filterRows(txns, keep, BankTransactionColumns)
}

// CleanAccounts cleans account data
func CleanAccounts(accts map[string][]string) map[string][]string {
	if len(accts) == 0 || len(accts[BankAccountColumns[0]]) == 0 {
		return accts
	}
	n := len(accts[BankAccountColumns[0]])

	for _, col := range []string{"交易卡号", "交易账号"} {
		if accts[col] != nil {
			for i := 0; i < n; i++ {
				accts[col][i] = parser.CleanAccountNumber(accts[col][i])
			}
		}
	}

	// Remove rows where all key fields are empty
	keyFields := []string{"账户开户名称", "开户人证件号码", "交易卡号", "交易账号"}
	keep := make([]bool, n)
	for i := 0; i < n; i++ {
		hasValue := false
		for _, f := range keyFields {
			if accts[f] != nil && strings.TrimSpace(accts[f][i]) != "" {
				hasValue = true
				break
			}
		}
		keep[i] = hasValue
	}

	// Deduplicate by 交易卡号 + 交易账号
	seen := make(map[string]bool)
	for i := 0; i < n; i++ {
		if !keep[i] {
			continue
		}
		key := accts["交易卡号"][i] + "|" + accts["交易账号"][i]
		if seen[key] {
			keep[i] = false
		}
		seen[key] = true
	}

	return filterRows(accts, keep, BankAccountColumns)
}

// FillFromAccounts fills transaction fields from account data
func FillFromAccounts(txns, accts map[string][]string) {
	if len(txns) == 0 || len(accts) == 0 {
		return
	}

	// Build card -> account mapping
	cardMap := make(map[string]map[string]string)
	for i := 0; i < len(accts[BankAccountColumns[0]]); i++ {
		card := accts["交易卡号"][i]
		if card == "" {
			continue
		}
		if _, ok := cardMap[card]; !ok {
			cardMap[card] = make(map[string]string)
		}
		cardMap[card]["账户开户名称"] = accts["账户开户名称"][i]
		cardMap[card]["开户人证件号码"] = accts["开户人证件号码"][i]
		cardMap[card]["账号开户银行"] = accts["账号开户银行"][i]
		cardMap[card]["交易账号"] = accts["交易账号"][i]
	}

	// Fill in missing values
	for i := 0; i < len(txns[BankTransactionColumns[0]]); i++ {
		card := txns["交易卡号"][i]
		acct, ok := cardMap[card]
		if !ok {
			continue
		}
		if txns["交易户名"][i] == "" {
			txns["交易户名"][i] = acct["账户开户名称"]
		}
		if txns["交易证件号码"][i] == "" {
			txns["交易证件号码"][i] = acct["开户人证件号码"]
		}
		if txns["交易方开户行"][i] == "" {
			txns["交易方开户行"][i] = acct["账号开户银行"]
		}
		if txns["交易账号"][i] == "" {
			txns["交易账号"][i] = acct["交易账号"]
		}
	}
}

// ToFinalTable converts normalized data to final table format
func ToFinalTable(data map[string][]string, finalName string) map[string][]string {
	columns, ok := BankFinalTables[finalName]
	if !ok {
		return data
	}
	result := make(map[string][]string)
	n := len(data[BankTransactionColumns[0]])
	for _, col := range columns {
		result[col] = make([]string, n)
	}

	// Map with renames
	renameMap := map[string]string{
		"交易方户名":     "交易户名",
		"交易方证件号码":   "交易证件号码",
		"客户名称":      "账户开户名称",
		"证照号码":      "开户人证件号码",
		"证照类型":      "证件类型",
	}

	for _, col := range columns {
		src := col
		if mapped, ok := renameMap[col]; ok {
			src = mapped
		}
		if data[src] != nil {
			result[col] = data[src]
		}
		// Fill source from 来源文件 if 来源 is empty
		if col == "来源" {
			for i := 0; i < n; i++ {
				if result[col][i] == "" && data["来源文件"] != nil {
					result[col][i] = data["来源文件"][i]
				}
			}
		}
	}
	return result
}

// NormalizeGenericFinal normalizes a generic final table
func NormalizeGenericFinal(headers []string, data [][]string, finalName string) map[string][]string {
	columns := BankFinalTables[finalName]
	result := make(map[string][]string)
	for _, col := range columns {
		result[col] = make([]string, len(data))
	}
	for _, col := range columns {
		if col == "来源" {
			continue
		}
		idx := findColumnIndex(headers, col)
		if idx >= 0 {
			for i := 0; i < len(data); i++ {
				result[col][i] = data[i][idx]
			}
		}
	}
	return result
}

// FinalTableName returns the final table name for intermediate types
func FinalTableName(tableType string) string {
	switch tableType {
	case "任务信息":
		return "银行任务信息"
	case "人员信息":
		return "银行人员信息"
	case "人员联系方式":
		return "银行人员联系方式"
	case "人员住址":
		return "银行人员住址"
	case "强制措施":
		return "银行强制措施"
	case "子账户信息":
		return "子账户信息"
	}
	return ""
}

// mapColumns maps source columns to target using aliases
func mapColumns(headers []string, data [][]string, result map[string][]string, aliases map[string][]string) {
	for target, candidates := range aliases {
		idx := findColumnIndex(headers, candidates...)
		if idx >= 0 {
			for i := 0; i < len(data); i++ {
				if idx < len(data[i]) {
					result[target][i] = data[i][idx]
				}
			}
		}
	}
}

func findColumnIndex(headers []string, candidates ...string) int {
	for _, candidate := range candidates {
		for i, h := range headers {
			if strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(candidate)) {
				return i
			}
		}
	}
	for _, candidate := range candidates {
		cLower := strings.ToLower(strings.TrimSpace(candidate))
		for i, h := range headers {
			hLower := strings.ToLower(strings.TrimSpace(h))
			if hLower == cLower || strings.Contains(hLower, cLower) || strings.Contains(cLower, hLower) {
				return i
			}
		}
	}
	return -1
}

func filterRows(data map[string][]string, keep []bool, columns []string) map[string][]string {
	result := make(map[string][]string)
	for _, col := range columns {
		result[col] = make([]string, 0)
	}
	n := len(columns)

	for i := 0; i < n && i < len(keep); i++ {
		if keep[i] {
			for _, col := range columns {
				if data[col] != nil && i < len(data[col]) {
					result[col] = append(result[col], data[col][i])
				} else {
					result[col] = append(result[col], "")
				}
			}
		}
	}
	return result
}

func mapQueryAccountFlow(headers []string, data [][]string, result map[string][]string) {
	for i, h := range headers {
		switch h {
		case "查询账号":
			copyCol(data, i, result, "交易卡号")
			copyCol(data, i, result, "交易账号")
		case "对方账号卡号":
			copyCol(data, i, result, "交易对手账卡号")
		case "对方账号姓名":
			copyCol(data, i, result, "对手户名")
		case "金额":
			copyCol(data, i, result, "交易金额")
		case "余额":
			copyCol(data, i, result, "交易余额")
		case "借贷标志":
			copyCol(data, i, result, "收付标志")
		case "交易类型":
			copyCol(data, i, result, "摘要说明")
		case "交易结果":
			copyCol(data, i, result, "交易是否成功")
		case "交易时间":
			copyCol(data, i, result, "交易时间")
		case "交易流水号":
			copyCol(data, i, result, "交易流水号")
		}
	}
}

func mapCreditCardBill(headers []string, data [][]string, result map[string][]string) {
	for i, h := range headers {
		switch h {
		case "交易日期", "入账日期":
			copyCol(data, i, result, "交易时间")
		case "交易描述":
			copyCol(data, i, result, "摘要说明")
		case "交易金额", "入账金额":
			copyCol(data, i, result, "交易金额")
		case "交易货币", "入账货币":
			copyCol(data, i, result, "交易币种")
		}
	}
}

func mapCMBTransaction(headers []string, data [][]string, result map[string][]string) {
	for i, h := range headers {
		switch h {
		case "交易日期", "交易时间":
			copyCol(data, i, result, "交易时间")
		case "客户名称":
			copyCol(data, i, result, "交易户名")
		case "交易卡号":
			copyCol(data, i, result, "交易卡号")
		case "交易方向(D:借|C:贷)":
			copyCol(data, i, result, "收付标志")
		case "交易金额":
			copyCol(data, i, result, "交易金额")
		case "联机余额":
			copyCol(data, i, result, "交易余额")
		case "对方帐号":
			copyCol(data, i, result, "交易对手账卡号")
		case "对方客户名称":
			copyCol(data, i, result, "对手户名")
		case "交易摘要":
			copyCol(data, i, result, "摘要说明")
		case "账号":
			copyCol(data, i, result, "交易账号")
		}
	}
}

func mapCMBAccount(headers []string, data [][]string, result map[string][]string) {
	for i, h := range headers {
		switch h {
		case "客户姓名":
			copyCol(data, i, result, "账户开户名称")
		case "户口号":
			copyCol(data, i, result, "交易账号")
		case "开户机构":
			copyCol(data, i, result, "开户网点")
		case "开户日期":
			copyCol(data, i, result, "账号开户时间")
		case "户口状态":
			copyCol(data, i, result, "账户状态")
		case "证件类型":
			copyCol(data, i, result, "证件类型")
		case "证件号码":
			copyCol(data, i, result, "开户人证件号码")
		case "活期帐户余额":
			copyCol(data, i, result, "账户余额")
		case "户口名称":
			copyCol(data, i, result, "账户类型")
		}
	}
}

func copyCol(data [][]string, srcIdx int, result map[string][]string, target string) {
	for i := 0; i < len(data); i++ {
		if srcIdx < len(data[i]) {
			result[target][i] = data[i][srcIdx]
		}
	}
}
