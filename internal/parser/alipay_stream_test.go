package parser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStreamAlipayFilesEmitsRowsWithoutUnifiedBuffer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "账户明细.csv")
	content := "交易号,商户订单号,交易创建时间,付款时间,最近修改时间,交易来源地,类型,用户信息,交易对方信息,消费名称,金额（元）,收/支,交易状态,支付方式,充值流水号,备注,对应的协查数据\n" +
		"T1,O1,2024-01-01 10:00:00,,,广东,转账,2088242236672211(熊守文),2088532293834122(袁铭璐),测试,10.50,支出,成功,,,,\n" +
		"T2,O2,2024-01-02 10:00:00,,,广东,转账,2088242236672211（熊守文）,（平安银行股份有限公司）6230580000457696578,,20.00,收入,成功,,,,\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var emitted [][]string
	sources, tableRows, unifiedRows, err := StreamAlipayFiles([]string{path}, "strict", func(row []string) error {
		emitted = append(emitted, append([]string(nil), row...))
		return nil
	})
	if err != nil {
		t.Fatalf("stream alipay: %v", err)
	}
	if len(sources) != 1 || sources[0].Rows != 2 || tableRows["账户明细"] != 2 || unifiedRows != 2 {
		t.Fatalf("unexpected stream metadata: sources=%#v tableRows=%#v unifiedRows=%d", sources, tableRows, unifiedRows)
	}
	if len(emitted) != 2 || emitted[0][colIdx("交易金额")] != "10.50" ||
		emitted[0][colIdx("收付标志")] != "出" || emitted[1][colIdx("收付标志")] != "进" {
		t.Fatalf("unexpected emitted rows: %#v", emitted)
	}
	if emitted[0][colIdx("交易流水号")] != "T1" || emitted[0][colIdx("商户流水号")] != "O1" ||
		emitted[1][colIdx("交易流水号")] != "T2" || emitted[1][colIdx("商户流水号")] != "O2" {
		t.Fatalf("支付宝交易号与商户订单号未独立映射：%#v", emitted)
	}
	if emitted[0][colIdx("摘要说明")] != "测试" || emitted[1][colIdx("摘要说明")] != "" {
		t.Fatalf("支付宝消费名称未直接映射摘要说明，或错误使用类型兜底：%#v", emitted)
	}
	for index, row := range emitted {
		if row[colIdx("交易账号")] != "2088242236672211" ||
			row[colIdx("交易卡号")] != "2088242236672211" ||
			row[colIdx("交易户名")] != "熊守文" ||
			row[colIdx("交易方开户行")] != "支付宝" {
			t.Fatalf("第%d行用户信息拆分映射错误：%#v", index+1, row)
		}
	}
	if emitted[0][colIdx("交易对手账卡号")] != "2088532293834122" ||
		emitted[0][colIdx("对手户名")] != "袁铭璐" ||
		emitted[0][colIdx("对手开户银行")] != "" {
		t.Fatalf("支付宝账号型对手映射错误：%#v", emitted[0])
	}
	if emitted[1][colIdx("交易对手账卡号")] != "6230580000457696578" ||
		emitted[1][colIdx("对手户名")] != "" ||
		emitted[1][colIdx("对手开户银行")] != "平安银行股份有限公司" {
		t.Fatalf("银行卡型对手映射错误：%#v", emitted[1])
	}
	if emitted[0][colIdx("来源表")] == "" {
		t.Fatal("expected source location")
	}
}

func TestSplitAlipayUserInfoFallback(t *testing.T) {
	account, name := SplitAlipayUserInfo("2088242236672211(熊守文)")
	if account != "2088242236672211" || name != "熊守文" {
		t.Fatalf("半角括号拆分错误：account=%q name=%q", account, name)
	}
	account, name = SplitAlipayUserInfo("2088242236672211（熊守文）")
	if account != "2088242236672211" || name != "熊守文" {
		t.Fatalf("全角括号拆分错误：account=%q name=%q", account, name)
	}
	account, name = SplitAlipayUserInfo("2088242236672211")
	if account != "2088242236672211" || name != "" {
		t.Fatalf("无括号格式回退错误：account=%q name=%q", account, name)
	}
}

func TestSplitAlipayCounterpartyInfo(t *testing.T) {
	account, name, bank := SplitAlipayCounterpartyInfo("2088532293834122(袁铭璐)")
	if account != "2088532293834122" || name != "袁铭璐" || bank != "" {
		t.Fatalf("支付宝账号型拆分错误：account=%q name=%q bank=%q", account, name, bank)
	}
	account, name, bank = SplitAlipayCounterpartyInfo("(平安银行股份有限公司)6230580000457696578")
	if account != "6230580000457696578" || name != "" || bank != "平安银行股份有限公司" {
		t.Fatalf("银行卡型拆分错误：account=%q name=%q bank=%q", account, name, bank)
	}
	account, name, bank = SplitAlipayCounterpartyInfo("未识别对手")
	if account != "" || name != "未识别对手" || bank != "" {
		t.Fatalf("未识别格式回退错误：account=%q name=%q bank=%q", account, name, bank)
	}
	account, name, bank = SplitAlipayCounterpartyInfo("熊守文(网商银行)(6668447550399326)")
	if account != "6668447550399326" || name != "熊守文" || bank != "网商银行" {
		t.Fatalf("姓名银行银行卡三段格式拆分错误：account=%q name=%q bank=%q", account, name, bank)
	}
	account, name, bank = SplitAlipayCounterpartyInfo("熊守文(熊守文)(6212252007001812085)")
	if account != "6212252007001812085" || name != "熊守文" || bank != "" {
		t.Fatalf("第二段为姓名时不得误填开户银行：account=%q name=%q bank=%q", account, name, bank)
	}
	account, name, bank = SplitAlipayCounterpartyInfo("2088870269965062(支付宝小荷包(熊守文的多人小荷包))")
	if account != "2088870269965062" || name != "支付宝小荷包(熊守文的多人小荷包)" || bank != "" {
		t.Fatalf("支付宝账号嵌套名称拆分错误：account=%q name=%q bank=%q", account, name, bank)
	}
}

func TestCleanAlipayOptionalValue(t *testing.T) {
	for _, value := range []string{"null", "NULL", " <nil> ", "   "} {
		if got := cleanAlipayOptionalValue(value); got != "" {
			t.Fatalf("cleanAlipayOptionalValue(%q)=%q，期望空值", value, got)
		}
	}
	if got := cleanAlipayOptionalValue(" 202607290001 "); got != "202607290001" {
		t.Fatalf("有效商户流水号被错误修改：%q", got)
	}
}

func TestAlipayRecognitionIsLimitedToInvestigationTables(t *testing.T) {
	expected := []string{"账户明细", "余额明细", "登陆日志", "注册信息"}
	if len(AlipayStandardTables) != len(expected) {
		t.Fatalf("支付宝表类型数量=%d，期望仅四类：%v", len(AlipayStandardTables), expected)
	}
	for _, table := range expected {
		if _, ok := AlipayStandardTables[table]; !ok {
			t.Fatalf("缺少支付宝调证表类型 %s", table)
		}
	}
	for _, unsupported := range []string{"个人账单", "转账明细", "支付流水汇总", "交易记录"} {
		if _, ok := AlipayStandardTables[unsupported]; ok {
			t.Fatalf("不应识别非本项目支付宝表类型 %s", unsupported)
		}
	}
}

func TestAlipayBalanceDetailRecognizesNegativeExpense(t *testing.T) {
	headers := AlipayStandardTables["余额明细"]
	converter := newAlipayStreamConverter(headers, SourceAuditContext{
		Provider: "支付宝", TableType: "余额明细", Path: "余额明细.csv",
		Sheet: "sheet1", FileHash: "hash", HeaderRow: 0,
	})
	incoming := converter([]string{
		"I-1", "subject", "counterparty", "2026-07-29 10:00:00", "",
		"500.0", "null", "1000.0", "转账", "", "", "", "",
	}, 0)
	outgoing := converter([]string{
		"O-1", "subject", "counterparty", "2026-07-29 11:00:00", "",
		"null", "-1200.0", "800.0", "转账", "", "", "", "",
	}, 1)
	if incoming[colIdx("收付标志")] != "进" || incoming[colIdx("交易金额")] != "500.00" {
		t.Fatalf("余额明细收入映射错误：%#v", incoming)
	}
	if outgoing[colIdx("收付标志")] != "出" || outgoing[colIdx("交易金额")] != "1200.00" {
		t.Fatalf("余额明细负数支出映射错误：%#v", outgoing)
	}
}

func TestAlipayBalanceDetailIsDisabledByDefaultAndCanBeEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "余额明细.csv")
	content := "交易订单号/外部流水号,账户,对方帐户,交易发生日期,银行处理日期,收入金额(+)（元）,支出金额(-)（元）,余额（元）,业务类型,交易发生地,银行名称,备注,对应的协查数据\n" +
		"I-1,subject,counterparty,2026-07-29 10:00:00,,500.0,null,1000.0,转账,,,,\n" +
		"O-1,subject,counterparty,2026-07-29 11:00:00,,null,-1200.0,800.0,转账,,,,\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	var defaultRows int
	sources, tableRows, unifiedRows, err := StreamAlipayFiles([]string{path}, "strict", func([]string) error {
		defaultRows++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || tableRows["余额明细"] != 2 || unifiedRows != 0 || defaultRows != 0 {
		t.Fatalf("余额明细默认必须只识别不转换：sources=%#v tableRows=%#v unified=%d emitted=%d",
			sources, tableRows, unifiedRows, defaultRows)
	}

	var enabledRows int
	_, _, unifiedRows, err = StreamAlipayFilesWithOptions(
		[]string{path}, "strict", MappingOptions{IncludeAlipayBalance: true},
		func([]string) error {
			enabledRows++
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if unifiedRows != 2 || enabledRows != 2 {
		t.Fatalf("显式启用后余额明细应转换2行：unified=%d emitted=%d", unifiedRows, enabledRows)
	}
}
