package scanner

import (
	"github.com/etl/backend/internal/parser"
	"github.com/etl/backend/internal/rules"
)

func detectProviderByColumns(columns []string) string {
	if matchesKnownTable(columns, parser.AlipayStandardTables) {
		return "支付宝"
	}
	if matchesKnownTable(columns, parser.WechatTables) {
		return "微信"
	}
	if matchesKnownTable(columns, rules.BankTables) {
		return "银行"
	}
	return "未知"
}

func matchesKnownTable(columns []string, tables map[string][]string) bool {
	for _, expected := range tables {
		if parser.HeaderScore(columns, expected) >= 3 {
			return true
		}
	}
	return false
}
