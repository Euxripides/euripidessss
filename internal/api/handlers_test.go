package api

import (
	"testing"

	"github.com/etl/backend/internal/model"
)

func TestApplyFiltersHonorsTargetDirectionAndDate(t *testing.T) {
	txns := []model.TransactionRow{
		{
			"交易户名": "Alice", "对手户名": "Bob",
			"交易金额": "100", "收付标志": "出", "交易时间": "2024-01-02 10:00:00",
		},
		{
			"交易户名": "Alice", "对手户名": "Carol",
			"交易金额": "200", "收付标志": "出", "交易时间": "2024-01-02 11:00:00",
		},
		{
			"交易户名": "Alice", "对手户名": "Bob",
			"交易金额": "300", "收付标志": "进", "交易时间": "2024-01-02 12:00:00",
		},
		{
			"交易户名": "Alice", "对手户名": "Bob",
			"交易金额": "400", "收付标志": "出", "交易时间": "2024-02-01 10:00:00",
		},
	}

	filtered := applyFilters(txns, map[string]interface{}{
		"source_filters": []interface{}{
			map[string]interface{}{"column": "交易户名", "values": []interface{}{"Alice"}},
		},
		"target_filters": []interface{}{
			map[string]interface{}{"column": "对手户名", "values": []interface{}{"Bob"}},
		},
		"directions": []interface{}{"出"},
		"start_date": "2024-01-01",
		"end_date":   "2024-01-31",
	})

	if len(filtered) != 1 {
		t.Fatalf("expected 1 row after filters, got %d", len(filtered))
	}
	if filtered[0]["交易金额"] != "100" {
		t.Fatalf("expected amount 100, got %q", filtered[0]["交易金额"])
	}
}

func TestNormalizeFlowDirectionUsesBuiltInsAndAliases(t *testing.T) {
	aliases := map[string]string{"入账": "进", "loan": "出"}

	tests := map[string]string{
		"收入":     "进",
		"支出":     "出",
		"入账":     "进",
		" loan ": "出",
	}

	for input, want := range tests {
		if got := normalizeFlowDirection(input, aliases); got != want {
			t.Fatalf("normalizeFlowDirection(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFlowEdgeLimitUsesAuditLimitForActiveEntityFilters(t *testing.T) {
	limit := flowEdgeLimit(map[string]interface{}{
		"source_filters": []interface{}{
			map[string]interface{}{"column": "account", "values": []interface{}{"1000050001"}},
		},
	})
	if limit != auditFlowEdgeLimit {
		t.Fatalf("expected audit limit, got %d", limit)
	}
}

func TestFlowEdgeLimitCapsExplicitRequest(t *testing.T) {
	limit := flowEdgeLimit(map[string]interface{}{"max_edges": float64(100000)})
	if limit != auditFlowEdgeLimit {
		t.Fatalf("expected capped audit limit, got %d", limit)
	}
}
