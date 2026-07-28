package etl

import (
	"testing"

	"github.com/etl/backend/internal/parser"
)

func TestUnifiedRowsToTransactionsMapsSourceLocation(t *testing.T) {
	row := make([]string, len(parser.UnifiedColumns))
	for i, column := range parser.UnifiedColumns {
		if column == "来源表" {
			row[i] = `wechat.xlsx:sheet1:2`
		}
	}
	txns := unifiedRowsToTransactions([][]string{row}, parser.UnifiedColumns)
	if len(txns) != 1 || txns[0]["数据来源"] != `wechat.xlsx:sheet1:2` {
		t.Fatalf("unexpected mapped source: %#v", txns)
	}
}
