package clickhouseexport

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
)

func TestFiveMillionRowStreamingExport(t *testing.T) {
	if os.Getenv("CLICKHOUSE_LARGE_EXPORT_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_LARGE_EXPORT_INTEGRATION=1 to stream five million rows")
	}
	client, err := clickhouse.New(config.Load().ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	const rowCount = uint64(5_000_000)
	const address = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	jobID := fmt.Sprintf("large_export_%d", time.Now().UnixNano())
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Minute)
		defer stop()
		_ = client.Exec(cleanup, "ALTER TABLE onchain.chain_transactions DELETE WHERE ingest_job_id='"+jobID+"' SETTINGS mutations_sync=2")
	}()
	insert := fmt.Sprintf(`INSERT INTO onchain.chain_transactions
(chain_id,block_number,block_time,transaction_index,tx_hash,from_address,to_address,value_raw,value_decimal,status,source_provider,ingest_job_id,source_range_id)
SELECT 56,920000000+number,toDateTime64('2026-02-01 00:00:00',3,'UTC')+toIntervalSecond(number%%86400),
toUInt32(number%%1000),concat('0x',lower(hex(SHA256(concat('%s',toString(number)))))),
'%s','0xffffffffffffffffffffffffffffffffffffffff','1',toDecimal256(1,38),'SUCCESS','acceptance','%s','large-export'
FROM numbers(%d)`, jobID, address, jobID, rowCount)
	started := time.Now()
	if err := client.Exec(ctx, insert); err != nil {
		t.Fatal(err)
	}
	service, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Start(Request{Dataset: DatasetTransactions, Columns: []string{"block_number", "tx_hash"}, Filter: Filter{ChainID: 56, Address: address}, Limit: rowCount})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Remove(task.ID)
	for task.Status == StatusQueued || task.Status == StatusRunning {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
		task, _ = service.Get(task.ID)
	}
	if task.Status != StatusCompleted {
		t.Fatalf("export status=%s error=%s", task.Status, task.Error)
	}
	reader, openedTask, err := service.Open(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	lines := uint64(0)
	for scanner.Scan() {
		lines++
	}
	closeErr := reader.Close()
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if lines != rowCount+1 {
		t.Fatalf("CSV lines=%d want=%d", lines, rowCount+1)
	}
	t.Logf("streamed rows=%d bytes=%d latency=%s file=%s", rowCount, openedTask.Bytes, time.Since(started), openedTask.FileName)
}
