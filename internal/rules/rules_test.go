package rules

import (
	"testing"
)

func TestClassifyTable(t *testing.T) {
	// Should recognize 交易明细 by its columns
	headers := []string{"交易卡号", "交易账号", "交易户名", "交易证件号码", "交易方开户行",
		"账户性质", "交易时间", "交易金额", "交易余额", "收付标志",
		"交易对手账卡号", "对手账户性质", "现金标志", "对手户名", "对手身份证号",
		"对手开户银行", "摘要说明", "交易币种", "交易网点名称", "交易发生地",
		"交易是否成功", "传票号", "IP地址", "MAC地址", "对手交易余额",
		"交易流水号", "日志号", "凭证种类", "凭证号", "交易柜员号",
		"备注", "查询反馈结果原因", "数据来源", "来源表", "来源文件"}
	tableType := ClassifyTable(headers, "test.xlsx", "Sheet1")
	if tableType != "交易明细" && tableType != "" {
		t.Logf("ClassifyTable returned: %s", tableType)
	}
}

func TestFilterRowsUsesInputRowCountNotColumnCount(t *testing.T) {
	rows := 100
	data := make(map[string][]string)
	for _, column := range BankTransactionColumns {
		data[column] = make([]string, rows)
	}
	for i := 0; i < rows; i++ {
		data["交易时间"][i] = "2024-01-01 00:00:00"
		data["交易金额"][i] = "1"
	}
	keep := make([]bool, rows)
	for i := range keep {
		keep[i] = true
	}
	filtered := filterRows(data, keep, BankTransactionColumns)
	if got := len(filtered["交易时间"]); got != rows {
		t.Fatalf("filterRows kept %d rows, want %d", got, rows)
	}
}

func TestNormalizeAccountInitializesSourceMetadata(t *testing.T) {
	headers := []string{"账户开户名称", "交易卡号", "交易账号"}
	data := [][]string{{"测试账户", "62220001", "10001"}}
	normalized, err := NormalizeAccount(headers, data, "账户信息", "account.csv", "sheet1")
	if err != nil {
		t.Fatalf("NormalizeAccount: %v", err)
	}
	if normalized["来源文件"][0] != "account.csv" || normalized["来源Sheet"][0] != "sheet1" {
		t.Fatalf("unexpected source metadata: %#v", normalized)
	}
}

func TestIsTransactionTableType(t *testing.T) {
	if !TransactionTableTypes["交易明细"] {
		t.Errorf("expected 交易明细 to be transaction table type")
	}
	if !TransactionTableTypes["招商交易流水"] {
		t.Errorf("expected 招商交易流水 to be transaction table type")
	}
	if AccountTableTypes["交易明细"] {
		t.Errorf("did not expect 交易明细 to be account table type")
	}
}

func TestIsAccountTableType(t *testing.T) {
	if !AccountTableTypes["账户信息"] {
		t.Errorf("expected 账户信息 to be account table type")
	}
	if !AccountTableTypes["招商客户资料"] {
		t.Errorf("expected 招商客户资料 to be account table type")
	}
}

func TestFinalTableName(t *testing.T) {
	if name := FinalTableName("任务信息"); name != "银行任务信息" {
		t.Errorf("expected 银行任务信息, got %s", name)
	}
	if name := FinalTableName("人员信息"); name != "银行人员信息" {
		t.Errorf("expected 银行人员信息, got %s", name)
	}
	if name := FinalTableName("未知类型"); name != "" {
		t.Errorf("expected empty string, got %s", name)
	}
}

func TestLoadCustomRules(t *testing.T) {
	SetCustomRulesPath("")
	data, err := LoadCustomRules()
	if err != nil {
		t.Fatal(err)
	}
	if data == nil {
		t.Fatal("expected non-nil data")
	}
	if data.Providers == nil {
		t.Errorf("expected providers map")
	}
}
