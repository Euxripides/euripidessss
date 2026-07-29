package parser

import "strings"

type PaymentParty struct {
	Account string
	Card    string
	Name    string
	Bank    string
}

func ApplyDirectionalParties(row []string, direction string, payer, payee PaymentParty, basis string) {
	setUnifiedValue(row, "付款方账号", firstValue(payer.Account, payer.Card))
	setUnifiedValue(row, "付款方户名", payer.Name)
	setUnifiedValue(row, "付款方开户行", payer.Bank)
	setUnifiedValue(row, "收款方账号", firstValue(payee.Account, payee.Card))
	setUnifiedValue(row, "收款方户名", payee.Name)
	setUnifiedValue(row, "收款方开户行", payee.Bank)
	setUnifiedValue(row, "主体判定依据", basis)

	var subject PaymentParty
	var counterparty PaymentParty
	switch NormalizeDirection(direction) {
	case "出":
		subject, counterparty = payer, payee
		setUnifiedValue(row, "主体判定状态", "已判定")
	case "进":
		subject, counterparty = payee, payer
		setUnifiedValue(row, "主体判定状态", "已判定")
	default:
		setUnifiedValue(row, "主体判定状态", "无法判定")
		return
	}
	setUnifiedValue(row, "交易账号", firstValue(subject.Account, subject.Card))
	setUnifiedValue(row, "交易卡号", subject.Card)
	setUnifiedValue(row, "交易户名", subject.Name)
	setUnifiedValue(row, "交易方开户行", subject.Bank)
	setUnifiedValue(row, "交易对手账卡号", firstValue(counterparty.Account, counterparty.Card))
	setUnifiedValue(row, "对手户名", counterparty.Name)
	setUnifiedValue(row, "对手开户银行", counterparty.Bank)
}

func ApplySubjectCounterpartyRoles(row []string, direction string, subject, counterparty PaymentParty, basis string) {
	switch NormalizeDirection(direction) {
	case "出":
		ApplyDirectionalParties(row, direction, subject, counterparty, basis)
	case "进":
		ApplyDirectionalParties(row, direction, counterparty, subject, basis)
	default:
		setUnifiedValue(row, "主体判定依据", basis)
		setUnifiedValue(row, "主体判定状态", "无法判定")
	}
}

func firstValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
