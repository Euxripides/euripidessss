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
	if mode == "" {
		mode = "strict"
	}
	if _, ok := AlipayUnifyTables[mode]; !ok {
		return nil, nil, 0, fmt.Errorf("unknown alipay mode: %s", mode)
	}

	files = append([]string(nil), files...)
	sort.Strings(files)
	sources := make([]AlipaySource, 0, len(files))
	tableRows := make(map[string]int)
	unifiedRows := 0
	var workbookFiles []string

	for _, path := range files {
		if !isDelimitedFile(path) {
			workbookFiles = append(workbookFiles, path)
			continue
		}
		source, emitted, err := streamAlipayCSVFile(path, mode, emit)
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
		result, err := ProcessAlipayFiles(workbookFiles, "", mode)
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

func streamAlipayCSVFile(path, mode string, emit func([]string) error) (AlipaySource, int, error) {
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

	unifyTables := AlipayUnifyTables[mode]
	converter := newAlipayStreamConverter(headers, tableType, path, headerRow)
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
		if unifyTables[tableType] {
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

func newAlipayStreamConverter(headers []string, tableType, path string, headerRow int) func([]string, int) []string {
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
		switch tableType {
		case "账户明细":
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易创建时间", "付款时间", "最近修改时间"))
			row[colIdx("交易金额")] = FloatToStr(getFloat("金额（元）"))
			row[colIdx("收付标志")] = NormalizeDirection(get("收/支"))
			row[colIdx("对手户名")] = get("交易对方信息")
			row[colIdx("摘要说明")] = get("消费名称", "类型")
			row[colIdx("交易流水号")] = get("交易号", "商户订单号")
			row[colIdx("交易发生地")] = get("交易来源地")
			row[colIdx("交易是否成功")] = get("交易状态")
			row[colIdx("备注")] = get("备注")
		case "余额明细":
			income := getFloat("收入金额(+)（元）")
			expense := getFloat("支出金额(-)（元）")
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易发生日期", "银行处理日期"))
			row[colIdx("交易账号")] = get("账户")
			if income > 0 {
				row[colIdx("交易金额")] = FloatToStr(income)
				row[colIdx("收付标志")] = "进"
			} else if expense > 0 {
				row[colIdx("交易金额")] = FloatToStr(expense)
				row[colIdx("收付标志")] = "出"
			}
			row[colIdx("交易余额")] = FloatToStr(getFloat("余额（元）"))
			row[colIdx("交易对手账卡号")] = get("对方帐户")
			row[colIdx("摘要说明")] = get("业务类型")
			row[colIdx("交易发生地")] = get("交易发生地")
			row[colIdx("交易方开户行")] = get("银行名称")
			row[colIdx("备注")] = get("备注")
		case "转账明细":
			row[colIdx("交易金额")] = FloatToStr(getFloat("转账金额（元）"))
			row[colIdx("交易账号")] = get("付款方支付宝账号")
			row[colIdx("交易对手账卡号")] = get("收款方支付宝账号")
			row[colIdx("交易流水号")] = get("交易号")
			row[colIdx("摘要说明")] = get("转账产品名称")
			row[colIdx("交易发生地")] = get("交易发生地")
			row[colIdx("交易时间")] = NormalizeDatetime(get("到账时间"))
			row[colIdx("收付标志")] = "出"
		case "支付流水汇总":
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易时间"))
			row[colIdx("交易金额")] = FloatToStr(getFloat("交易金额"))
			row[colIdx("收付标志")] = NormalizeDirection(get("交易主体的出入账标识"))
			row[colIdx("交易余额")] = FloatToStr(getFloat("交易余额"))
			row[colIdx("交易币种")] = get("币种")
			row[colIdx("交易流水号")] = get("交易流水号", "支付订单号")
			row[colIdx("交易对手账卡号")] = get(
				"收款方的支付帐号", "付款方的支付帐号",
				"收款方银行卡所属银行卡号", "付款方银行卡所属银行卡号",
			)
			row[colIdx("对手户名")] = get("收款方的商户名称")
			row[colIdx("摘要说明")] = get("交易类型", "支付类型")
			row[colIdx("IP地址")] = get("交易支付设备ip")
			row[colIdx("MAC地址")] = get("mac地址")
			row[colIdx("备注")] = get("备注")
		case "个人账单":
			row[colIdx("交易时间")] = NormalizeDatetime(get("交易时间"))
			row[colIdx("交易金额")] = FloatToStr(getFloat("金额"))
			row[colIdx("收付标志")] = NormalizeDirection(get("收/支"))
			row[colIdx("对手户名")] = get("交易对方")
			row[colIdx("摘要说明")] = get("商品说明")
			row[colIdx("交易流水号")] = get("交易订单号", "商家订单号")
			row[colIdx("备注")] = get("收/付款方式")
		case "交易记录":
			row[colIdx("交易金额")] = FloatToStr(getFloat("交易金额（元）"))
			row[colIdx("交易时间")] = NormalizeDatetime(get("创建时间", "收款时间", "最后修改时间"))
			row[colIdx("交易流水号")] = get("交易号", "外部交易号")
			row[colIdx("交易是否成功")] = get("交易状态")
			row[colIdx("交易账号")] = get("买家信息", "买家用户id")
			row[colIdx("交易对手账卡号")] = get("卖家信息", "卖家用户id")
			row[colIdx("摘要说明")] = get("商品名称", "交易类型")
			row[colIdx("交易发生地")] = get("来源地")
		}
		row[colIdx("来源表")] = SourceLocation(path, rowIndex, headerRow)
		return row
	}
}
