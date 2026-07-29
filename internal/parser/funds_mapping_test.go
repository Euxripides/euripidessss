package parser

import "testing"

func TestWechatPaymentSummaryMapsPartiesByDirection(t *testing.T) {
	headers := []string{
		"交易主体的出入账标识", "付款方的支付帐号", "付款方银行卡所属银行卡号",
		"付款方银行卡所属银行名称", "收款方的支付帐号", "收款方银行卡所属银行卡号",
		"收款方银行卡所属银行名称", "收款方的商户名称", "交易时间", "交易金额",
	}
	data := [][]string{
		{"出", "payer-pay", "payer-card", "付款银行", "payee-pay", "payee-card", "收款银行", "收款商户", "2026-07-29 10:00:00", "100"},
		{"进", "payer-pay", "payer-card", "付款银行", "payee-pay", "payee-card", "收款银行", "本方商户", "2026-07-29 11:00:00", "200"},
	}
	rows := wechatToUnified(data, headers, SourceAuditContext{
		Provider: "微信", TableType: "支付流水汇总", Path: `C:\input\wechat.csv`,
		Sheet: "流水", FileHash: "abc", HeaderRow: 0,
	})
	out := unifiedSliceToMap(rows[0])
	if out["交易账号"] != "payer-pay" || out["交易对手账卡号"] != "payee-pay" || out["对手户名"] != "收款商户" {
		t.Fatalf("outgoing parties mapped incorrectly: %#v", out)
	}
	in := unifiedSliceToMap(rows[1])
	if in["交易账号"] != "payee-pay" || in["交易对手账卡号"] != "payer-pay" {
		t.Fatalf("incoming parties mapped incorrectly: %#v", in)
	}
	if in["交易户名"] != "本方商户" || in["对手户名"] != "" {
		t.Fatalf("incoming names must follow subject/counterparty roles: %#v", in)
	}
}

func TestAlipayTransferUsesSubjectIdentifiers(t *testing.T) {
	headers := []string{"付款方支付宝账号", "收款方支付宝账号", "转账金额（元）", "到账时间", "交易号", "对应的协查数据"}
	data := [][]string{
		{"payer@example.com", "payee@example.com", "10", "2026-07-29 10:00:00", "T-1", ""},
		{"payer@example.com", "payee@example.com", "20", "2026-07-29 11:00:00", "T-2", ""},
		{"payer@example.com", "payee@example.com", "30", "2026-07-29 12:00:00", "T-3", ""},
	}
	context := SourceAuditContext{
		Provider: "支付宝", TableType: "转账明细", Path: `C:\input\alipay.csv`,
		Sheet: "转账", FileHash: "def", HeaderRow: 1,
	}
	outRows := alipayToUnified(data[:1], headers, context, MappingOptions{
		SubjectIdentifiers: map[string]bool{"payer@example.com": true},
	})
	out := unifiedSliceToMap(outRows[0])
	if out["收付标志"] != "出" || out["交易账号"] != "payer@example.com" || out["交易对手账卡号"] != "payee@example.com" {
		t.Fatalf("payer subject mapping incorrect: %#v", out)
	}
	inRows := alipayToUnified(data[1:2], headers, context, MappingOptions{
		SubjectIdentifiers: map[string]bool{"payee@example.com": true},
	})
	in := unifiedSliceToMap(inRows[0])
	if in["收付标志"] != "进" || in["交易账号"] != "payee@example.com" || in["交易对手账卡号"] != "payer@example.com" {
		t.Fatalf("payee subject mapping incorrect: %#v", in)
	}
	unknownRows := alipayToUnified(data[2:], headers, context, MappingOptions{})
	unknown := unifiedSliceToMap(unknownRows[0])
	if unknown["收付标志"] != "" || unknown["主体判定状态"] != "无法判定" {
		t.Fatalf("unmatched transfer must not be forced outgoing: %#v", unknown)
	}
}

func TestWechatReceivedAmountIsNotCounterpartyBalance(t *testing.T) {
	headers := []string{"交易时间", "交易金额(分)", "账户余额(分)", "借贷类型", "对手方接收金额(分)"}
	rows := wechatToUnified([][]string{{"2026-07-29 10:00:00", "10000", "50000", "借", "9900"}}, headers, SourceAuditContext{
		Provider: "微信", TableType: "交易明细信息", Path: `C:\input\wechat.xlsx`,
		Sheet: "Sheet1", FileHash: "hash", HeaderRow: 2,
	})
	row := unifiedSliceToMap(rows[0])
	if row["对手方接收金额"] != "99.00" || row["对手交易余额"] != "" {
		t.Fatalf("received amount semantics incorrect: %#v", row)
	}
	if row["来源类型"] != "微信" || row["来源Sheet"] != "Sheet1" || row["原始行号"] != "4" ||
		row["来源文件SHA256"] != "hash" || row["来源记录ID"] == "" {
		t.Fatalf("provenance fields missing: %#v", row)
	}
}

func unifiedSliceToMap(values []string) map[string]string {
	result := make(map[string]string, len(UnifiedColumns))
	for index, column := range UnifiedColumns {
		if index < len(values) {
			result[column] = values[index]
		}
	}
	return result
}
