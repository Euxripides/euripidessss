package datawarehouse

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	duckdbengine "github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
	"github.com/etl/backend/internal/smartdownload"
)

func TestDuckDBClickHouseDualReadReconciliation(t *testing.T) {
	if os.Getenv("CLICKHOUSE_DUAL_READ_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_DUAL_READ_INTEGRATION=1 to compare DuckDB and ClickHouse")
	}
	app := config.Load()
	client, err := clickhouse.New(app.ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	a := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	b := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	jobID := fmt.Sprintf("dual_read_%d", time.Now().UnixNano())
	header := "chain_id,block_number,block_time,transaction_hash,transaction_index,from_address,to_address,value_raw,input,method_id,status,gas_used,gas_price,source_provider\n"
	rows := []string{
		fmt.Sprintf("56,910000001,1700000101,0x%064x,1,%s,%s,10,,,1,21000,5,dual", 1, a, b),
		fmt.Sprintf("56,910000002,1700000102,0x%064x,2,%s,%s,20,,,1,21000,5,dual", 2, a, b),
		fmt.Sprintf("56,910000003,1700000103,0x%064x,3,%s,%s,7,,,1,21000,5,dual", 3, b, a),
		fmt.Sprintf("56,910000004,1700000104,0x%064x,4,%s,%s,3,,,1,21000,5,dual", 4, a, a),
	}
	csvData := header + strings.Join(rows, "\n") + "\n"
	tempRoot := `E:\database\clickhouse\tmp`
	if err := os.MkdirAll(tempRoot, 0755); err != nil {
		t.Fatal(err)
	}
	csvPath := filepath.Join(tempRoot, jobID+".csv")
	dbPath := filepath.Join(tempRoot, jobID+".duckdb")
	if err := os.WriteFile(csvPath, []byte(csvData), 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(csvPath)
	defer os.Remove(dbPath)
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot := filepath.Clean(filepath.Join(workingDir, "..", ".."))
	duck := duckdbengine.Open(repositoryRoot, tempRoot, duckdbengine.AnalyticsConfig{DuckDBPath: filepath.Join(repositoryRoot, "tools", "duckdb", "duckdb.exe"), DuckDBDatabase: dbPath})
	if !duck.Available() {
		t.Fatalf("DuckDB unavailable: %+v", duck.Status())
	}
	escapedCSV := strings.ReplaceAll(filepath.ToSlash(csvPath), "'", "''")
	duckRows, err := duck.ExecSQLJSON(ctx, fmt.Sprintf(`SELECT
CAST(count(*) AS VARCHAR) transaction_count,
CAST(sum(CASE WHEN lower(from_address)='%[2]s' AND lower(to_address)!='%[2]s' THEN CAST(value_raw AS DECIMAL(38,0)) ELSE 0 END) AS VARCHAR) total_out,
CAST(sum(CASE WHEN lower(to_address)='%[2]s' AND lower(from_address)!='%[2]s' THEN CAST(value_raw AS DECIMAL(38,0)) ELSE 0 END) AS VARCHAR) total_in,
CAST(sum(CASE WHEN lower(to_address)='%[2]s' AND lower(from_address)!='%[2]s' THEN CAST(value_raw AS DECIMAL(38,0)) WHEN lower(from_address)='%[2]s' AND lower(to_address)!='%[2]s' THEN -CAST(value_raw AS DECIMAL(38,0)) ELSE 0 END) AS VARCHAR) netflow
FROM read_csv_auto('%[1]s',header=true,all_varchar=true)`, escapedCSV, a))
	if err != nil || len(duckRows) != 1 {
		t.Fatalf("DuckDB reconciliation: rows=%v err=%v", duckRows, err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()
		for _, table := range []string{"chain_transactions", "address_activity"} {
			_ = client.Exec(cleanup, fmt.Sprintf("ALTER TABLE onchain.%s DELETE WHERE ingest_job_id='%s' SETTINGS mutations_sync=2", table, jobID))
		}
	}()
	writer := NewWriter(client, fakeDuckDB{csv: csvData})
	result, err := writer.WriteIndexed(ctx, smartdownload.IndexedWriteRequest{DatasetJobID: jobID, ChainKey: "bsc", ChainID: 56, Dataset: smartdownload.DatasetTransactions, Address: a, FromBlock: 910000001, ToBlock: 910000004, RowCount: 4, MergedParquet: eDriveParquet(t), SourceProvider: "dual"})
	if err != nil || result.VerifiedRows != 4 {
		t.Fatalf("ClickHouse write: result=%+v err=%v", result, err)
	}
	clickRows, err := client.QueryJSON(ctx, fmt.Sprintf(`SELECT
toString(uniqExact(tx_hash)) transaction_count,
toString(sumIf(amount,direction='OUT')) total_out,
toString(sumIf(amount,direction='IN')) total_in,
toString(sumIf(amount,direction='IN')-sumIf(amount,direction='OUT')) netflow
FROM onchain.address_activity FINAL WHERE chain_id=56 AND address='%s' AND ingest_job_id='%s'`, a, jobID))
	if err != nil || len(clickRows) != 1 {
		t.Fatalf("ClickHouse reconciliation: rows=%v err=%v", clickRows, err)
	}
	for _, field := range []string{"transaction_count", "total_in", "total_out", "netflow"} {
		duckValue := fmt.Sprint(duckRows[0][field])
		clickValue := fmt.Sprint(clickRows[0][field])
		if duckValue != clickValue {
			t.Fatalf("%s differs: DuckDB=%s ClickHouse=%s", field, duckValue, clickValue)
		}
	}
	t.Logf("dual read matched: transaction_count=%v total_in=%v total_out=%v netflow=%v", clickRows[0]["transaction_count"], clickRows[0]["total_in"], clickRows[0]["total_out"], clickRows[0]["netflow"])
}
