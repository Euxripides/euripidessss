package ledgerimport

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestFinalInsertSQLIntegration exercises the staging -> dedup -> final insert
// pipeline against the live onchain ClickHouse. It is skipped unless
// CLICKHOUSE_LEDGER_IMPORT_INTEGRATION=1 is set, and cleans up all rows it
// inserts using a dedicated job id.
func TestFinalInsertSQLIntegration(t *testing.T) {
	if os.Getenv("CLICKHOUSE_LEDGER_IMPORT_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_LEDGER_IMPORT_INTEGRATION=1 to run")
	}
	jobID := "ledgerimport-selftest-" + fmt.Sprint(time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cfg := Config{
		JobID:          jobID,
		CredentialFile: `E:\database\clickhouse\config\clickhouse.env`,
		MaxBatchRows:   10,
		RequestTimeout: 5 * time.Minute,
	}
	client, err := newClickHouseClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := resetStaging(ctx, client); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = dropStaging(ctx, client)
		for _, stmt := range []string{
			"ALTER TABLE onchain.token_transfers DELETE WHERE ingest_job_id='" + jobID + "'",
			"ALTER TABLE onchain.chain_transactions DELETE WHERE ingest_job_id='" + jobID + "'",
			"ALTER TABLE onchain.address_activity DELETE WHERE ingest_job_id='" + jobID + "'",
		} {
			_ = client.Exec(ctx, stmt)
		}
	}()

	now := time.Now().UTC().Format("2006-01-02 15:04:05.000")
	// Two transfer rows with the same identity but different log_index, plus a
	// no-logIndex row for a distinct second event in the same tx.
	rows := []TransferRow{
		{ChainID: 56, BlockNumber: 100, BlockTime: "2026-01-01 00:00:00.000", TxHash: "0xaa", LogIndex: int32Ptr(5), TokenAddress: "0xf1st", TokenSymbol: "FIST", TokenDecimals: 6, TokenStandard: "ERC20", EventSignature: TransferEventSignature, FromAddress: "0xa", ToAddress: "0xb", RawValue: "100", ValueDecimal: "0.0001", SourcePriority: 3, SourceProvider: "SQD_FINALIZED", SourceRangeID: "fist-ledger", IngestJobID: jobID, IngestedAt: now},
		{ChainID: 56, BlockNumber: 100, BlockTime: "2026-01-01 00:00:00.000", TxHash: "0xaa", LogIndex: int32Ptr(5), TokenAddress: "0xf1st", TokenSymbol: "FIST", TokenDecimals: 6, TokenStandard: "ERC20", EventSignature: TransferEventSignature, FromAddress: "0xa", ToAddress: "0xb", RawValue: "100", ValueDecimal: "0.0001", SourcePriority: 1, SourceProvider: "WALLET_XLSX_EXPORT", SourceRangeID: "address-tx-xlsx", IngestJobID: jobID, IngestedAt: now},
		{ChainID: 56, BlockNumber: 100, BlockTime: "2026-01-01 00:00:00.000", TxHash: "0xaa", TokenAddress: "0xf1st", TokenSymbol: "FIST", TokenDecimals: 6, TokenStandard: "ERC20", EventSignature: TransferEventSignature, FromAddress: "0xa", ToAddress: "0xc", RawValue: "200", ValueDecimal: "0.0002", SourcePriority: 2, SourceProvider: "ADDRESS_CSV_EXPORT", SourceRangeID: "address-transfer-csv", IngestJobID: jobID, IngestedAt: now},
		{ChainID: 56, BlockNumber: 100, BlockTime: "2026-01-01 00:00:00.000", TxHash: "0xaa", TokenAddress: "0xf1st", TokenSymbol: "FIST", TokenDecimals: 6, TokenStandard: "ERC20", EventSignature: TransferEventSignature, FromAddress: "0xa", ToAddress: "0xc", RawValue: "200", ValueDecimal: "0.0002", SourcePriority: 2, SourceProvider: "ADDRESS_CSV_EXPORT", SourceRangeID: "address-transfer-csv", IngestJobID: jobID, IngestedAt: now},
		// A second real ledger event in the same tx with a distinct log index
		// must be preserved even though its identity equals nothing above.
		{ChainID: 56, BlockNumber: 100, BlockTime: "2026-01-01 00:00:00.000", TxHash: "0xaa", LogIndex: int32Ptr(7), TokenAddress: "0xf1st", TokenSymbol: "FIST", TokenDecimals: 6, TokenStandard: "ERC20", EventSignature: TransferEventSignature, FromAddress: "0xa", ToAddress: "0xb", RawValue: "100", ValueDecimal: "0.0001", SourcePriority: 3, SourceProvider: "SQD_FINALIZED", SourceRangeID: "fist-ledger", IngestJobID: jobID, IngestedAt: now},
	}
	assignSyntheticLogIndices(rows)
	w := newStageWriter(client, transferStageTable, transferStageColumns(), 10)
	for _, row := range rows {
		if err := w.Write(ctx, transferCSVRow(row)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Exec(ctx, finalTransferInsertSQL(jobID)); err != nil {
		t.Fatalf("final insert: %v", err)
	}
	res, err := client.QueryJSON(ctx, fmt.Sprintf(
		"SELECT count() AS c FROM onchain.token_transfers FINAL WHERE ingest_job_id='%s'", jobID))
	if err != nil {
		t.Fatal(err)
	}
	if got := toUint(res[0]["c"]); got != 3 {
		t.Fatalf("expected 3 final rows, got %d", got)
	}
	res, err = client.QueryJSON(ctx, fmt.Sprintf(
		"SELECT log_index, from_address, to_address, source_provider FROM onchain.token_transfers FINAL WHERE ingest_job_id='%s' ORDER BY log_index", jobID))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("rows = %+v", res)
	}
	// First real event: duplicated identity row must keep the highest-priority
	// source and its real log index.
	if res[0]["from_address"] != "0xa" || res[0]["to_address"] != "0xb" ||
		res[0]["source_provider"] != "SQD_FINALIZED" || toUint(res[0]["log_index"]) != 5 {
		t.Fatalf("first event wrong: %+v", res[0])
	}
	// Second real event with the same identity but a different log index must
	// be preserved (FIST-style multi-event transactions).
	if toUint(res[1]["log_index"]) != 7 {
		t.Fatalf("second real log index expected 7, got %v", res[1])
	}
	// Export row without a real log index receives a synthetic index.
	if toUint(res[2]["log_index"]) < uint64(SyntheticLogOffset) {
		t.Fatalf("synthetic log index expected >= %d, got %v", SyntheticLogOffset, res[2]["log_index"])
	}

	// Transaction stage dedup.
	txRows := []TxRow{
		{ChainID: 56, BlockNumber: 200, BlockTime: "2026-01-02 00:00:00.000", TxHash: "0xbb", FromAddress: "0xa", ToAddress: "0xb", ValueRaw: "", ValueDecimal: "0", FeeNative: "0.001", MethodID: "", MethodName: "transfer", Status: "UNKNOWN", RawStatus: "", StatusSource: "MISSING", SourcePriority: 1, SourceProvider: "WALLET_XLSX_EXPORT", SourceRangeID: "address-tx-xlsx", IngestJobID: jobID, IngestedAt: now},
		{ChainID: 56, BlockNumber: 200, BlockTime: "2026-01-02 00:00:00.000", TxHash: "0xbb", FromAddress: "0xa", ToAddress: "0xb", ValueRaw: "", ValueDecimal: "0", FeeNative: "0.001", MethodID: "0x7bf689f4", MethodName: "", Status: "SUCCESS", RawStatus: "SUCCESS", StatusSource: "RECEIPT", SourcePriority: 2, SourceProvider: "ADDRESS_CSV_EXPORT", SourceRangeID: "address-tx-csv", IngestJobID: jobID, IngestedAt: now},
	}
	tw := newStageWriter(client, txStageTable, txStageColumns(), 10)
	for _, row := range txRows {
		if err := tw.Write(ctx, txCSVRow(row)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Exec(ctx, finalTxInsertSQL(jobID)); err != nil {
		t.Fatalf("tx final insert: %v", err)
	}
	res, err = client.QueryJSON(ctx, fmt.Sprintf(
		"SELECT count() AS c, any(method_id) AS m, any(status) AS s FROM onchain.chain_transactions FINAL WHERE ingest_job_id='%s'", jobID))
	if err != nil {
		t.Fatal(err)
	}
	if toUint(res[0]["c"]) != 1 || res[0]["m"] != "0x7bf689f4" || res[0]["s"] != "SUCCESS" {
		t.Fatalf("tx final = %+v", res)
	}
}

func int32Ptr(v int32) *int32 { return &v }
