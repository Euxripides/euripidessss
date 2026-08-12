package cryptodownload

import "testing"

func TestDedupeExactBrowserRowsKeepsDistinctEventsWithSameHash(t *testing.T) {
	rows := []map[string]any{
		{"交易哈希": "0xabc", "log_index": "1", "数量": "10"},
		{"交易哈希": "0xabc", "log_index": "2", "数量": "10"},
		{"交易哈希": "0xabc", "log_index": "1", "数量": "10"},
	}

	got := dedupeExactBrowserRows(Config{}, "BSC", "token_transfers", rows)
	if len(got) != 2 {
		t.Fatalf("dedupeExactBrowserRows() len = %d, want 2", len(got))
	}
	if got[0]["log_index"] != "1" || got[1]["log_index"] != "2" {
		t.Fatalf("dedupeExactBrowserRows() changed event order: %#v", got)
	}
}

func TestDedupeExactBrowserRowsPreservesUnmarshalableFutureRows(t *testing.T) {
	unsupported := make(chan int)
	rows := []map[string]any{{"value": unsupported}, {"value": unsupported}}

	got := dedupeExactBrowserRows(Config{}, "BSC", "transactions", rows)
	if len(got) != 2 {
		t.Fatalf("unmarshalable rows must be preserved, got %d", len(got))
	}
}
