package datawarehouse

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/etl/backend/internal/smartdownload"
)

type fakeDuckDB struct{ csv string }

func (f fakeDuckDB) ExecSQL(_ context.Context, sql string) ([]byte, error) {
	re := regexp.MustCompile(`(?i)\sTO\s+'([^']+)'`)
	m := re.FindStringSubmatch(sql)
	if len(m) != 2 {
		return nil, fmt.Errorf("COPY target missing: %s", sql)
	}
	path := filepath.FromSlash(strings.ReplaceAll(m[1], "''", "'"))
	return nil, os.WriteFile(path, []byte(f.csv), 0o644)
}

type capturedInsert struct {
	table   string
	columns []string
	data    string
}

type fakeSink struct {
	failTable string
	inserts   []capturedInsert
}

type failingAnalyticsRefresher struct{}

func (failingAnalyticsRefresher) RefreshAddressAnalytics(context.Context, uint32, string) error {
	return fmt.Errorf("injected analytics refresh failure")
}

type verifyingSink struct {
	fakeSink
	rows int64
}

func (v *verifyingSink) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
	if !strings.Contains(query, "FINAL") || !strings.Contains(query, "ingest_job_id='ds1'") {
		return nil, fmt.Errorf("unexpected verification query: %s", query)
	}
	rows := v.rows
	if strings.Contains(query, "FROM address_activity") {
		rows *= 2
	}
	return []map[string]any{{"n": float64(rows)}}, nil
}

func (f *fakeSink) InsertCSV(_ context.Context, table string, columns []string, body io.Reader) error {
	data, _ := io.ReadAll(body)
	f.inserts = append(f.inserts, capturedInsert{table: table, columns: append([]string(nil), columns...), data: string(data)})
	if table == f.failTable {
		return fmt.Errorf("injected failure")
	}
	return nil
}

func TestCanonicalWriterDerivesMethodStatusAndProvenance(t *testing.T) {
	sink := &fakeSink{}
	csvData := "chain_id,block_number,block_time,transaction_hash,transaction_index,from_address,to_address,value_raw,input,status,source_provider\n" +
		"56,100,1700000000,0xhash,2,0xaaa,0xbbb,123,0xa9059cbb00000000,1,sqd\n"
	req := request(smartdownload.DatasetTransactions, eDriveParquet(t))
	req.ParserVersion, req.NormalizerVersion, req.SchemaVersion = "parser-v3", "normalizer-v2", 2
	if _, err := NewWriter(sink, fakeDuckDB{csv: csvData}).WriteIndexed(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	reader := csv.NewReader(strings.NewReader(sink.inserts[0].data))
	row, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string, len(row))
	for i, column := range sink.inserts[0].columns {
		values[column] = row[i]
	}
	for field, want := range map[string]string{
		"method_id": "0xa9059cbb", "status": "SUCCESS", "raw_status": "1", "status_source": "RECEIPT",
		"parser_version": "parser-v3", "normalizer_version": "normalizer-v2", "schema_version": "2",
	} {
		if values[field] != want {
			t.Fatalf("%s=%q want %q; row=%v", field, values[field], want, values)
		}
	}
	if statusText("") != "UNKNOWN" || statusSource("UNKNOWN") != "MISSING" {
		t.Fatal("missing receipt status must remain UNKNOWN/MISSING")
	}
}

func eDriveParquet(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(`E:\codex\etl`, "warehouse-writer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "merged.parquet")
	if err := os.WriteFile(path, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func request(dataset, path string) smartdownload.IndexedWriteRequest {
	return smartdownload.IndexedWriteRequest{
		DatasetJobID: "ds1", ChainKey: "bsc", ChainID: 56, Dataset: dataset,
		Address: "0xaaa", MergedParquet: path, SourceProvider: "sqd",
	}
}

func TestWriterMapsTransactionsAndSELFActivity(t *testing.T) {
	csvData := strings.Join([]string{
		"chain_id,block_number,block_time,transaction_hash,transaction_index,from_address,to_address,value_raw,input,method_id,status,gas_used,gas_price,source_provider",
		"56,100,1700000000,0xhash1,2,0xaaa,0xbbb,123,,0xa9059cbb,1,21000,5,sqd",
		"56,101,1700000001,0xhash2,3,0xself,0xself,7,,,0,0,,sqd",
		"56,102,invalid,0xreject,4,0xaaa,0xbbb,9,,,1,0,,sqd",
		"56,103,1700000003,0xbadvalue,5,0xaaa,0xbbb,not-a-number,,,1,0,,sqd",
	}, "\n") + "\n"
	sink := &fakeSink{}
	w := NewWriter(sink, fakeDuckDB{csv: csvData})
	result, err := w.WriteIndexed(context.Background(), request(smartdownload.DatasetTransactions, eDriveParquet(t)))
	if err != nil {
		t.Fatal(err)
	}
	if result.InputRows != 4 || result.InsertedRows != 2 || result.RejectedRows != 2 || result.ActivityRows != 3 {
		t.Fatalf("unexpected reconciliation: %+v", result)
	}
	if len(sink.inserts) != 2 || sink.inserts[0].table != "chain_transactions" || sink.inserts[1].table != "address_activity" {
		t.Fatalf("inserts = %+v", sink.inserts)
	}
	if !strings.Contains(sink.inserts[1].data, ",SELF,") {
		t.Fatalf("SELF activity missing: %s", sink.inserts[1].data)
	}
	if got := strings.Count(strings.TrimSpace(sink.inserts[1].data), "\n") + 1; got != 3 {
		t.Fatalf("activity rows = %d, want 3", got)
	}
}

func TestWriterMapsContractCreationAndHexValue(t *testing.T) {
	sink := &fakeSink{}
	csvData := "chain_id,block_number,block_time,transaction_hash,creator_address,contract_address,creation_type,factory_address,source_provider\n" +
		"56,400,1700000000,0xcreate,0xcreator,0xcontract,CREATE,,sqd\n"
	w := NewWriter(sink, fakeDuckDB{csv: csvData})
	result, err := w.WriteIndexed(context.Background(), request(smartdownload.DatasetContractCreations, eDriveParquet(t)))
	if err != nil {
		t.Fatal(err)
	}
	if result.InputRows != 1 || result.InsertedRows != 1 || result.ActivityRows != 2 {
		t.Fatalf("result = %+v", result)
	}
	if len(sink.inserts) != 3 || sink.inserts[0].table != "contract_creations" ||
		sink.inserts[1].table != "address_activity" || sink.inserts[2].table != "contracts" ||
		!strings.Contains(sink.inserts[1].data, "CONTRACT_CREATE") ||
		!strings.Contains(sink.inserts[2].data, "0xcontract,0xcreator") {
		t.Fatalf("inserts = %+v", sink.inserts)
	}
	decimal, err := decimalValue("0x10")
	if err != nil || decimal != "16" {
		t.Fatalf("hex decimal = %q err=%v", decimal, err)
	}
}

func TestWriterDecodesCanonicalEventLog(t *testing.T) {
	sink := &fakeSink{}
	topic0 := "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	from := "0000000000000000000000001111111111111111111111111111111111111111"
	to := "0000000000000000000000002222222222222222222222222222222222222222"
	data := "0x" + strings.Repeat("0", 63) + "1"
	topics := `["` + topic0 + `","0x` + from + `","0x` + to + `"]`
	csvData := "chain_id,block_number,block_time,transaction_hash,log_index,contract_address,topics,data,source_provider\n" +
		"56,500,1700000000,0x" + strings.Repeat("a", 64) + ",7,0x55d398326f99059ff775485246999027b3197955,\"" + strings.ReplaceAll(topics, "\"", "\"\"") + "\"," + data + ",sqd\n"
	w := NewWriter(sink, fakeDuckDB{csv: csvData})
	result, err := w.WriteIndexed(context.Background(), request(smartdownload.DatasetLogs, eDriveParquet(t)))
	if err != nil {
		t.Fatal(err)
	}
	if result.InputRows != 1 || result.InsertedRows != 1 || result.RejectedRows != 0 || result.ActivityRows != 0 {
		t.Fatalf("result = %+v", result)
	}
	if len(sink.inserts) != 2 || sink.inserts[0].table != "raw_logs" || sink.inserts[1].table != "parsed_events" ||
		!strings.Contains(sink.inserts[0].data, topic0) || !strings.Contains(sink.inserts[0].data, data) ||
		!strings.Contains(sink.inserts[1].data, ",Transfer,") || !strings.Contains(sink.inserts[1].data, "TOPIC0_REGISTRY") {
		t.Fatalf("inserts = %+v", sink.inserts)
	}
}

func TestWriterMapsTokenAndInternalLogicalKeys(t *testing.T) {
	tests := []struct {
		dataset string
		header  string
		row     string
		table   string
		index   string
	}{
		{smartdownload.DatasetTokenTransfers,
			"chain_id,block_number,block_time,transaction_hash,log_index,token_address,token_standard,from_address,to_address,value_raw,source_provider",
			"56,200,1700000000,0xtoken,9,0xtkn,ERC20,0xaaa,0xbbb,100,sqd", "token_transfers", "log:9"},
		{smartdownload.DatasetInternalTransactions,
			"chain_id,block_number,block_time,transaction_hash,trace_index,trace_address,call_type,from_address,to_address,value_raw,status,gas_used,source_provider",
			"56,300,1700000000,0xinternal,4,0_1,call,0xaaa,0xbbb,5,1,100,sqd", "internal_transactions", "trace:0_1"},
	}
	for _, tc := range tests {
		t.Run(tc.dataset, func(t *testing.T) {
			sink := &fakeSink{}
			w := NewWriter(sink, fakeDuckDB{csv: tc.header + "\n" + tc.row + "\n"})
			result, err := w.WriteIndexed(context.Background(), request(tc.dataset, eDriveParquet(t)))
			if err != nil {
				t.Fatal(err)
			}
			if result.InputRows != 1 || result.InsertedRows != 1 || result.RejectedRows != 0 || result.ActivityRows != 2 {
				t.Fatalf("result = %+v", result)
			}
			if len(sink.inserts) != 2 || sink.inserts[0].table != tc.table || !bytes.Contains([]byte(sink.inserts[1].data), []byte(tc.index)) {
				t.Fatalf("inserts = %+v", sink.inserts)
			}
		})
	}
}

func TestWriterPreservesTokenTransferBatchIndex(t *testing.T) {
	sink := &fakeSink{}
	csvData := "chain_id,block_number,block_time,transaction_hash,transaction_index,log_index,batch_index,token_id,token_address,token_standard,from_address,to_address,value_raw,source_provider\n" +
		"56,200,1700000000,0xtoken,3,9,2,1001,0xtkn,ERC1155,0xaaa,0xbbb,100,sqd\n"
	result, err := NewWriter(sink, fakeDuckDB{csv: csvData}).WriteIndexed(context.Background(), request(smartdownload.DatasetTokenTransfers, eDriveParquet(t)))
	if err != nil {
		t.Fatal(err)
	}
	if result.InsertedRows != 1 || len(sink.inserts) < 1 {
		t.Fatalf("result=%+v inserts=%+v", result, sink.inserts)
	}
	row, err := csv.NewReader(strings.NewReader(sink.inserts[0].data)).Read()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for i, column := range sink.inserts[0].columns {
		values[column] = row[i]
	}
	for field, want := range map[string]string{"transaction_index": "3", "log_index": "9", "batch_index": "2", "token_id": "1001"} {
		if values[field] != want {
			t.Errorf("%s=%q want %q", field, values[field], want)
		}
	}
}

func TestWriterAssignsCanonicalStablecoinPegToTransferAndActivity(t *testing.T) {
	sink := &fakeSink{}
	csvData := "chain_id,block_number,block_time,transaction_hash,log_index,token_address,token_decimals,token_standard,from_address,to_address,value_raw,value_decimal,source_provider\n" +
		"56,200,1700000000,0xtoken,9,0x55d398326f99059ff775485246999027b3197955,18,BEP20,0xaaa,0xbbb,1250000000000000000,1.25,rpc\n"
	result, err := NewWriter(sink, fakeDuckDB{csv: csvData}).WriteIndexed(context.Background(), request(smartdownload.DatasetTokenTransfers, eDriveParquet(t)))
	if err != nil {
		t.Fatal(err)
	}
	if result.InsertedRows != 1 || result.ActivityRows != 2 || len(sink.inserts) != 2 {
		t.Fatalf("result=%+v inserts=%+v", result, sink.inserts)
	}
	for _, insert := range sink.inserts {
		row, err := csv.NewReader(strings.NewReader(insert.data)).Read()
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]string{}
		for i, column := range insert.columns {
			values[column] = row[i]
		}
		if values["usd_value"] != "1.25" || values["price_source"] != "STABLECOIN_PEG" || values["price_confidence"] != "HIGH" {
			t.Fatalf("table=%s values=%+v", insert.table, values)
		}
	}
}

func TestTokenActivityEventIndexMatchesCrossProviderLogicalKey(t *testing.T) {
	if got := tokenActivityEventIndex("9", "0"); got != "log:9" {
		t.Fatalf("ERC20 activity index=%q want log:9", got)
	}
	if got := tokenActivityEventIndex("9", "2"); got != "log:9:batch:2" {
		t.Fatalf("ERC1155 batch activity index=%q", got)
	}
}

func TestWriterReturnsDatabaseFailureWithReconciliation(t *testing.T) {
	sink := &fakeSink{failTable: "address_activity"}
	w := NewWriter(sink, fakeDuckDB{csv: "chain_id,block_number,block_time,transaction_hash,from_address,to_address,value_raw,status\n56,1,1700000000,0x1,0xa,0xb,1,1\n"})
	result, err := w.WriteIndexed(context.Background(), request(smartdownload.DatasetTransactions, eDriveParquet(t)))
	if err == nil || !strings.Contains(err.Error(), "address_activity") {
		t.Fatalf("err = %v", err)
	}
	if result.InputRows != result.InsertedRows+result.RejectedRows {
		t.Fatalf("unreconciled result: %+v", result)
	}
}

func TestAnalyticsRefreshFailureDoesNotRevokeCanonicalWrite(t *testing.T) {
	sink := &fakeSink{}
	w := NewWriter(sink, fakeDuckDB{csv: "chain_id,block_number,block_time,transaction_hash,transaction_index,from_address,to_address,value_raw,input,status,source_provider\n56,100,1700000000,0xhash,2,0xaaa,0xbbb,123,,1,sqd\n"})
	w.SetAnalyticsRefresher(failingAnalyticsRefresher{})
	req := request(smartdownload.DatasetTransactions, eDriveParquet(t))
	req.Address = "0x1111111111111111111111111111111111111111"
	result, err := w.WriteIndexed(context.Background(), req)
	if err != nil {
		t.Fatalf("derived analytics refresh revoked canonical write: result=%+v err=%v", result, err)
	}
	if result.InputRows != 1 || result.InsertedRows != 1 || result.RejectedRows != 0 {
		t.Fatalf("unexpected reconciliation: %+v", result)
	}
	if metrics := w.Metrics(); metrics.AnalyticsRefreshErrors != 1 || metrics.WriterErrors != 0 {
		t.Fatalf("refresh failure observability mismatch: %+v", metrics)
	}
}

func TestWriterEnforcesDatabaseLogicalRowReconciliation(t *testing.T) {
	sink := &verifyingSink{rows: 0}
	w := NewWriter(sink, fakeDuckDB{csv: "chain_id,block_number,block_time,transaction_hash,from_address,to_address,value_raw,status\n56,1,1700000000,0x1,0xabc,0xb,1,1\n"})
	req := request(smartdownload.DatasetTransactions, eDriveParquet(t))
	req.Address, req.FromBlock, req.ToBlock = "0xabc", 1, 1
	result, err := w.WriteIndexed(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "db=0 writer_success=1") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	sink.rows = 1
	result, err = w.WriteIndexed(context.Background(), req)
	if err != nil || result.VerifiedRows != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestWriterMetricsReportsRollingP95(t *testing.T) {
	w := &Writer{latencyWindowMS: []int64{10, 20, 30, 40, 500}}
	metrics := w.Metrics()
	if metrics.InsertP95MS != 500 {
		t.Fatalf("p95=%d want 500", metrics.InsertP95MS)
	}
}

func TestRawLogDDLIsTransactionIndexedInBothSchemas(t *testing.T) {
	for _, path := range []string{"../../deploy/clickhouse/schema.sql", "../../deploy/clickhouse/v2_semantic_schema.sql"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sql := string(data)
		for _, required := range []string{"raw_logs", "topic0", "topic1", "topic2", "topic3", "data String", "ORDER BY (chain_id, tx_hash, log_index)"} {
			if !strings.Contains(sql, required) {
				t.Errorf("%s missing %q", path, required)
			}
		}
	}
}
