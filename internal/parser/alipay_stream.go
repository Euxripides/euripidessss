package parser

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// StreamAlipayFiles parses Alipay files and emits one unified row at a time.
// Delimited files never accumulate their transaction rows in memory.
func StreamAlipayFiles(files []string, mode string, emit func([]string) error) ([]AlipaySource, map[string]int, int, error) {
	return StreamAlipayFilesWithOptions(files, mode, MappingOptions{}, emit)
}

func StreamAlipayFilesWithOptions(files []string, mode string, options MappingOptions, emit func([]string) error) ([]AlipaySource, map[string]int, int, error) {
	if mode == "" {
		mode = "strict"
	}
	if _, ok := AlipayUnifyTables[mode]; !ok {
		return nil, nil, 0, fmt.Errorf("unknown alipay mode: %s", mode)
	}

	files = append([]string(nil), files...)
	sort.Strings(files)
	EnsureSourceHashes(files, &options)
	sources := make([]AlipaySource, 0, len(files))
	tableRows := make(map[string]int)
	unifiedRows := 0
	var workbookFiles []string

	for _, path := range files {
		if !isDelimitedFile(path) {
			workbookFiles = append(workbookFiles, path)
			continue
		}
		source, emitted, err := streamAlipayCSVFile(path, mode, options, emit)
		if err != nil {
			return sources, tableRows, unifiedRows, fmt.Errorf("stream alipay %s: %w", filepath.Base(path), err)
		}
		sources = append(sources, source)
		tableRows[source.TableType] += source.Rows
		unifiedRows += emitted
	}

	// Excel sources are normally much smaller and keep using the established
	// workbook parser. Their unified rows are released immediately after emit.
	if len(workbookFiles) > 0 {
		result, err := ProcessAlipayFilesWithOptions(workbookFiles, "", mode, options)
		if err != nil {
			return sources, tableRows, unifiedRows, err
		}
		sources = append(sources, result.Sources...)
		for tableType, rows := range result.TableRows {
			tableRows[tableType] += rows
		}
		for _, row := range result.UnifiedData {
			if err := emit(row); err != nil {
				return sources, tableRows, unifiedRows, err
			}
			unifiedRows++
		}
		result.UnifiedData = nil
	}

	return sources, tableRows, unifiedRows, nil
}

func streamAlipayCSVFile(path, mode string, options MappingOptions, emit func([]string) error) (AlipaySource, int, error) {
	preview, encoding, sep, err := readCSVPreview(path, 40)
	if err != nil {
		return AlipaySource{}, 0, err
	}
	preview = NormalizeEmbeddedCSVRows(TrimRows(preview))
	headerRow := findAlipayHeaderRow(preview)
	if headerRow < 0 {
		return AlipaySource{
			Path: path, SheetName: "sheet1", TableType: "未识别",
			Notes: []string{"前40行未找到支付宝标准表头"},
		}, 0, nil
	}

	_, headers := DataFrameFromHeader(preview, headerRow)
	tableType := classifyAlipayTable(headers, path, "sheet1")
	source := AlipaySource{
		Path: path, SheetName: "sheet1", TableType: tableType,
		HeaderRow: headerRow + 1, Columns: headers,
	}
	if tableType == "未识别" {
		return source, 0, nil
	}

	reader, file, err := openCSVReader(path, sep, encoding)
	if err != nil {
		return AlipaySource{}, 0, err
	}
	defer file.Close()

	converter := newAlipayStreamConverter(headers, SourceAuditContext{
		Provider: "支付宝", TableType: tableType, Path: path, Sheet: "sheet1",
		FileHash: options.SourceHashes[path], HeaderRow: headerRow,
	})
	rowIndex := 0
	dataRowIndex := 0
	emitted := 0
	for {
		row, readErr := reader.Read()
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return source, emitted, readErr
		}
		if rowIndex <= headerRow {
			rowIndex++
			continue
		}
		row = TrimRows([][]string{row})[0]
		source.Rows++
		if shouldUnifyAlipayTable(mode, tableType, options) {
			unified := converter(row, dataRowIndex)
			if err := emit(unified); err != nil {
				return source, emitted, err
			}
			emitted++
		}
		dataRowIndex++
		rowIndex++
	}
	return source, emitted, nil
}

func newAlipayStreamConverter(headers []string, context SourceAuditContext) func([]string, int) []string {
	headerMap := make(map[string]int, len(headers))
	for i, header := range headers {
		headerMap[header] = i
	}

	return func(data []string, rowIndex int) []string {
		get := func(names ...string) string {
			for _, name := range names {
				idx, ok := headerMap[name]
				if ok && idx < len(data) {
					if value := strings.TrimSpace(data[idx]); value != "" {
						return value
					}
				}
			}
			return ""
		}
		getFloat := func(names ...string) float64 {
			return ToNumber(get(names...))
		}

		row := make([]string, len(UnifiedColumns))
		switch context.TableType {
		case "账户明细":
			account, accountName := SplitAlipayUserInfo(get("用户信息"))
			counterAccount, counterName, counterBank := SplitAlipayCounterpartyInfo(get("交易对方信息"))
			row[colIdx("交易账号")] = account
			row[colIdx("交易卡号")] = account
			row[colIdx("交易户名")] = accountName
			row[colIdx("交易方开户行")] = "支付宝"
			row[colIdx("交易对手账卡号")] = counterAccount
			row[colIdx("对手户名")] = counterName
			row[colIdx("对手开户银行")] = counterBank
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易创建时间", "付款时间", "最近修改时间"))
			row[colIdx("交易金额")] = FloatToStr(getFloat("金额（元）"))
			row[colIdx("收付标志")] = NormalizeDirection(get("收/支"))
			row[colIdx("摘要说明")] = cleanAlipayOptionalValue(get("消费名称"))
			row[colIdx("交易流水号")] = cleanAlipayOptionalValue(get("交易号"))
			row[colIdx("商户流水号")] = cleanAlipayOptionalValue(get("商户订单号"))
			row[colIdx("交易发生地")] = get("交易来源地")
			row[colIdx("交易是否成功")] = get("交易状态")
			row[colIdx("备注")] = get("备注")
			ApplySubjectCounterpartyRoles(row, row[colIdx("收付标志")],
				PaymentParty{Account: account, Card: account, Name: accountName, Bank: "支付宝"},
				PaymentParty{Account: counterAccount, Name: counterName, Bank: counterBank},
				"支付宝账户明细持有人视角+收支字段",
			)
		case "余额明细":
			income := getFloat("收入金额(+)（元）")
			expense := getFloat("支出金额(-)（元）")
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易发生日期", "银行处理日期"))
			row[colIdx("交易账号")] = get("账户")
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
			row[colIdx("交易余额")] = FloatToStr(getFloat("余额（元）"))
			row[colIdx("交易对手账卡号")] = get("对方帐户")
			row[colIdx("摘要说明")] = get("业务类型")
			row[colIdx("交易发生地")] = get("交易发生地")
			row[colIdx("交易方开户行")] = get("银行名称")
			row[colIdx("备注")] = get("备注")
			ApplySubjectCounterpartyRoles(row, row[colIdx("收付标志")],
				PaymentParty{Account: get("账户"), Bank: get("银行名称")},
				PaymentParty{Account: get("对方帐户")},
				"支付宝余额明细账户+收入/支出金额",
			)
		}
		ApplySourceAudit(row, context, rowIndex)
		return row
	}
}
