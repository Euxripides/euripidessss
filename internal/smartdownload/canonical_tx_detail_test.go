package smartdownload

import (
	"strings"
	"testing"
)

func TestCanonicalTxDetailColumnsPreserveOrderingAndRawLogFields(t *testing.T) {
	transferColumns := strings.Join(canonicalColumns(DatasetTokenTransfers), ",")
	for _, field := range []string{"transaction_index", "log_index", "batch_index", "token_id"} {
		if !strings.Contains(transferColumns, field) {
			t.Errorf("token transfer canonical columns lost %s: %s", field, transferColumns)
		}
	}
	logColumns := strings.Join(canonicalColumns(DatasetLogs), ",")
	for _, field := range []string{"block_hash", "transaction_index", "removed"} {
		if !strings.Contains(logColumns, field) {
			t.Errorf("raw log canonical columns lost %s: %s", field, logColumns)
		}
	}
	transferSQL := canonicalTypedSQL(DatasetTokenTransfers)
	logSQL := canonicalTypedSQL(DatasetLogs)
	if !strings.Contains(transferSQL, `AS "batch_index"`) || !strings.Contains(logSQL, `AS "transaction_index"`) || !strings.Contains(logSQL, `AS "removed"`) {
		t.Fatalf("canonical numeric/bool casts missing: transfer=%s logs=%s", transferSQL, logSQL)
	}
}

func TestCanonicalTokenTransferKeyAndAliasPreserveBatchIndex(t *testing.T) {
	base := Record{ChainID: 56, TransactionHash: "0xABC", LogIndex: 7, Dataset: DatasetTokenTransfers,
		Payload: map[string]any{"event_sub_index": uint64(2)}}
	other := base
	other.Payload = map[string]any{"event_sub_index": uint64(3)}
	if CanonicalKey(base) == CanonicalKey(other) {
		t.Fatal("ERC1155 batch transfers collapsed onto one canonical key")
	}
	fields := normalizeRecord(base, "sqd", "range")
	if fields["batch_index"] != uint64(2) {
		t.Fatalf("event_sub_index alias was not preserved: %+v", fields)
	}
}
