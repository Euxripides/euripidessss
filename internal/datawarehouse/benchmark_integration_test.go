package datawarehouse

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
	"github.com/etl/backend/internal/smartdownload"
)

type benchmarkCSVEngine struct {
	dataset string
	rows    int
	base    uint64
}

func (e benchmarkCSVEngine) ExecSQL(_ context.Context, sql string) ([]byte, error) {
	match := regexp.MustCompile(`(?i)\sTO\s+'([^']+)'`).FindStringSubmatch(sql)
	if len(match) != 2 {
		return nil, fmt.Errorf("COPY target missing")
	}
	path := filepath.FromSlash(strings.ReplaceAll(match[1], "''", "'"))
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	w := bufio.NewWriterSize(file, 1<<20)
	from := "0x4444444444444444444444444444444444444444"
	to := "0x5555555555555555555555555555555555555555"
	if e.dataset == smartdownload.DatasetTransactions {
		_, _ = fmt.Fprintln(w, "chain_id,block_number,block_time,transaction_hash,transaction_index,from_address,to_address,value_raw,input,method_id,status,gas_used,gas_price,source_provider")
		for i := 0; i < e.rows; i++ {
			_, _ = fmt.Fprintf(w, "56,%d,%d,0x%064x,%d,%s,%s,%d,,0xa9059cbb,1,21000,5,benchmark\n",
				e.base+uint64(i), 1700000000+int64(i%86400), e.base+uint64(i), i, from, to, i+1)
		}
	} else {
		_, _ = fmt.Fprintln(w, "chain_id,block_number,block_time,transaction_hash,log_index,token_address,token_standard,from_address,to_address,value_raw,source_provider")
		for i := 0; i < e.rows; i++ {
			_, _ = fmt.Fprintf(w, "56,%d,%d,0x%064x,%d,0x6666666666666666666666666666666666666666,ERC20,%s,%s,%d,benchmark\n",
				e.base+uint64(i), 1700000000+int64(i%86400), e.base+uint64(i), i, from, to, i+1)
		}
	}
	return nil, w.Flush()
}

func TestClickHouseWriterPerformanceMatrix(t *testing.T) {
	if os.Getenv("CLICKHOUSE_BENCHMARK") != "1" {
		t.Skip("set CLICKHOUSE_BENCHMARK=1 to run 10K/100K/1M real ClickHouse matrix")
	}
	sizes := []int{10_000, 100_000, 1_000_000}
	if raw := strings.TrimSpace(os.Getenv("CLICKHOUSE_BENCHMARK_SIZES")); raw != "" {
		sizes = nil
		for _, part := range strings.Split(raw, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || n <= 0 || n > 1_000_000 {
				t.Fatalf("invalid benchmark size %q", part)
			}
			sizes = append(sizes, n)
		}
	}
	client, err := clickhouse.New(config.Load().ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	for datasetIndex, dataset := range []string{smartdownload.DatasetTransactions, smartdownload.DatasetTokenTransfers} {
		for sizeIndex, size := range sizes {
			name := fmt.Sprintf("%s/%d", dataset, size)
			t.Run(name, func(t *testing.T) {
				base := uint64(800_000_000 + datasetIndex*20_000_000 + sizeIndex*2_000_000)
				jobID := fmt.Sprintf("bench_%s_%d_%d", dataset, size, time.Now().UnixNano())
				table := map[string]string{smartdownload.DatasetTransactions: "chain_transactions", smartdownload.DatasetTokenTransfers: "token_transfers"}[dataset]
				defer func() {
					for _, target := range [][2]string{{table, jobID}, {"address_activity", jobID}} {
						cleanup, stop := context.WithTimeout(context.Background(), 10*time.Minute)
						_ = client.Exec(cleanup, fmt.Sprintf("ALTER TABLE onchain.%s DELETE WHERE ingest_job_id='%s' SETTINGS mutations_sync=2", target[0], target[1]))
						stop()
					}
				}()
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)
				started := time.Now()
				writer := NewWriter(client, benchmarkCSVEngine{dataset: dataset, rows: size, base: base})
				result, err := writer.WriteIndexed(ctx, smartdownload.IndexedWriteRequest{
					DatasetJobID: jobID, ChainKey: "bsc", ChainID: 56, Dataset: dataset,
					Address:   "0x4444444444444444444444444444444444444444",
					FromBlock: base, ToBlock: base + uint64(size-1), RowCount: int64(size),
					MergedParquet: eDriveParquet(t), SourceProvider: "benchmark",
				})
				elapsed := time.Since(started)
				runtime.ReadMemStats(&after)
				if err != nil {
					t.Fatal(err)
				}
				if result.InputRows != int64(size) || result.InsertedRows != int64(size) || result.RejectedRows != 0 || result.VerifiedRows != int64(size) {
					t.Fatalf("reconciliation: %+v", result)
				}
				logicalRows, err := verifyIngestRows(ctx, client, table, jobID)
				if err != nil || logicalRows != int64(size) {
					t.Fatalf("logical rows=%d err=%v", logicalRows, err)
				}
				if size == 10_000 {
					if _, err := writer.WriteIndexed(ctx, smartdownload.IndexedWriteRequest{
						DatasetJobID: jobID, ChainKey: "bsc", ChainID: 56, Dataset: dataset,
						Address: "0x4444444444444444444444444444444444444444", FromBlock: base,
						ToBlock: base + uint64(size-1), RowCount: int64(size), MergedParquet: eDriveParquet(t), SourceProvider: "benchmark",
					}); err != nil {
						t.Fatal(err)
					}
					logicalRows, _ = verifyIngestRows(ctx, client, table, jobID)
					if logicalRows != int64(size) {
						t.Fatalf("rerun logical rows=%d, want %d", logicalRows, size)
					}
				}
				allocated := uint64(0)
				if after.TotalAlloc >= before.TotalAlloc {
					allocated = after.TotalAlloc - before.TotalAlloc
				}
				t.Logf("rows=%d source_rows_per_sec=%.0f total_rows_with_activity=%d latency=%s heap_allocated_bytes=%d metrics=%+v",
					size, float64(size)/elapsed.Seconds(), result.InsertedRows+result.ActivityRows, elapsed, allocated, writer.Metrics())
			})
		}
	}
}
