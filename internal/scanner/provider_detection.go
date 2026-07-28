package scanner

import (
	"github.com/etl/backend/internal/parser"
	"github.com/etl/backend/internal/rules"
)

func detectProviderByColumns(columns []string) string {
	type scoredProvider struct {
		name  string
		score int
	}
	// A table can share generic fields such as 交易时间、交易金额、交易流水号
	// with more than one provider. Select the strongest complete signature
	// instead of returning the first provider that reaches the minimum score.
	// Bank wins exact ties because its standardized tables intentionally overlap
	// with the generic 支付流水汇总 signature.
	candidates := []scoredProvider{
		{name: "银行", score: bestKnownTableScore(columns, rules.BankTables)},
		{name: "微信", score: bestKnownTableScore(columns, parser.WechatTables)},
		{name: "支付宝", score: bestKnownTableScore(columns, parser.AlipayStandardTables)},
	}
	best := scoredProvider{name: "未知"}
	for _, candidate := range candidates {
		if candidate.score >= 3 && candidate.score > best.score {
			best = candidate
		}
	}
	return best.name
}

func matchesKnownTable(columns []string, tables map[string][]string) bool {
	return bestKnownTableScore(columns, tables) >= 3
}

func bestKnownTableScore(columns []string, tables map[string][]string) int {
	best := 0
	for _, expected := range tables {
		if score := parser.HeaderScore(columns, expected); score > best {
			best = score
		}
	}
	return best
}
