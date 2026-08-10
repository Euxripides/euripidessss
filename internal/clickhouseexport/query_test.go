package clickhouseexport

import (
	"strings"
	"testing"
)

func uint64Pointer(value uint64) *uint64 { return &value }

func TestCompileClosedDSL(t *testing.T) {
	req := Request{
		Dataset: DatasetTokenTransfers,
		Columns: []string{"block_number", "tx_hash", "from_address"},
		Filter: Filter{
			ChainID:   56,
			Address:   "0x55D398326F99059FF775485246999027B3197955",
			FromBlock: uint64Pointer(100),
			ToBlock:   uint64Pointer(200),
		},
		Limit: 500,
	}
	compiled, err := compile(req)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT block_number,tx_hash,from_address FROM token_transfers FINAL WHERE chain_id = 56 AND (from_address = '0x55d398326f99059ff775485246999027b3197955' OR to_address = '0x55d398326f99059ff775485246999027b3197955') AND block_number >= 100 AND block_number <= 200 ORDER BY block_number,transaction_index,log_index,tx_hash LIMIT 500"
	if compiled.SQL != want {
		t.Fatalf("unexpected query:\n%s\nwant:\n%s", compiled.SQL, want)
	}
}

func TestCompileRejectsInjectionAndUnsupportedValues(t *testing.T) {
	tests := []Request{
		{Dataset: Dataset("token_transfers; DROP TABLE tokens"), Filter: Filter{ChainID: 56}},
		{Dataset: DatasetTokenTransfers, Columns: []string{"tx_hash) FROM system.users --"}, Filter: Filter{ChainID: 56}},
		{Dataset: DatasetTokenTransfers, Filter: Filter{ChainID: 56, Address: "0xabc' OR 1=1 --"}},
		{Dataset: DatasetBlocks, Filter: Filter{ChainID: 56, Address: "0x55d398326f99059ff775485246999027b3197955"}},
		{Dataset: DatasetAddressSummary, Filter: Filter{ChainID: 56, FromBlock: uint64Pointer(1)}},
		{Dataset: DatasetTransactions, Filter: Filter{ChainID: 0}},
		{Dataset: DatasetTransactions, Filter: Filter{ChainID: 56, FromBlock: uint64Pointer(10), ToBlock: uint64Pointer(9)}},
		{Dataset: DatasetTransactions, Filter: Filter{ChainID: 56}, Limit: maxExportRows + 1},
	}
	for index, req := range tests {
		if compiled, err := compile(req); err == nil {
			t.Fatalf("case %d accepted: %s", index, compiled.SQL)
		}
	}
}

func TestAllCompiledIdentifiersAreClosed(t *testing.T) {
	for dataset, spec := range datasetSpecs {
		compiled, err := compile(Request{Dataset: dataset, Filter: Filter{ChainID: 1}})
		if err != nil {
			t.Fatalf("%s: %v", dataset, err)
		}
		if !strings.Contains(compiled.SQL, " FROM "+spec.table+" FINAL ") {
			t.Fatalf("dataset %s did not use its table: %s", dataset, compiled.SQL)
		}
	}
}
