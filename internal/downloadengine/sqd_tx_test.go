package downloadengine

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource/sqd"
)

// ── SQD Transactions → Parquet 完整链路 ──

func TestSQDTransactionsToParquet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SQD TX test in short mode")
	}

	client := sqd.New(&http.Client{Timeout: 180 * time.Second})
	network, err := chain.Resolve("bsc")
	if err != nil {
		t.Skipf("chain: %v", err)
	}

	// 1000 blocks 从高活跃区域
	startBlock := uint64(44500000)
	endBlock := startBlock + 1000

	t.Logf("=== SQD Transactions: blocks %d→%d ===", startBlock, endBlock)

	type parsedTX struct {
		hash, from, to, value string
		blockNumber           uint64
		timestamp             int64
	}
	var txs []parsedTX

	streamStart := time.Now()
	err = client.StreamTraces(context.Background(), network,
		sqd.BlockRange{From: startBlock, To: endBlock}, nil,
		func(block sqd.Block) error {
			for _, tx := range block.Transactions {
				txs = append(txs, parsedTX{
					hash:        tx.Hash,
					from:        tx.From,
					to:          tx.To,
					value:       tx.Value,
					blockNumber: block.Header.Number,
					timestamp:   block.Header.Timestamp,
				})
			}
			return nil
		})
	streamDur := time.Since(streamStart)

	if err != nil {
		t.Fatalf("SQD StreamTraces: %v", err)
	}

	t.Logf("  TXs: %d in %v (%.0f tx/s)", len(txs), streamDur, float64(len(txs))/streamDur.Seconds())

	if len(txs) < 10000 {
		t.Logf("  (TX count %d < 10000 — 当前范围数据量不足，扩大搜索)", len(txs))
	}

	// Write to CSV → DuckDB COPY TO PARQUET
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "sqd_txs.csv")
	f, err := os.Create(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("hash,from,to,value,block_number,timestamp\n")
	for _, tx := range txs {
		fmt.Fprintf(f, "%s,%s,%s,%s,%d,%d\n", tx.hash, tx.from, tx.to, tx.value, tx.blockNumber, tx.timestamp)
	}
	f.Close()

	duckdbExe := `E:\codex\etl\tools\duckdb\duckdb.exe`
	if _, err := os.Stat(duckdbExe); err != nil {
		t.Skip("duckdb.exe not found — skipping Parquet phase")
	}

	parquetPath := filepath.Join(dir, "sqd_txs.parquet")
	parquetSlash := strings.ReplaceAll(parquetPath, "\\", "/")
	csvSlash := strings.ReplaceAll(csvPath, "\\", "/")

	parquetStart := time.Now()
	sql := fmt.Sprintf(`COPY (SELECT * FROM read_csv('%s', header=true, all_varchar=true)) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)`, csvSlash, parquetSlash)
	cmd := exec.CommandContext(context.Background(), duckdbExe, "-c", sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("DuckDB COPY TO PARQUET: %v\n%s", err, string(out))
	}
	parquetDur := time.Since(parquetStart)

	info, _ := os.Stat(parquetPath)
	fsize := int64(0)
	if info != nil {
		fsize = info.Size()
	}

	// Verify
	verifySQL := fmt.Sprintf("SELECT count(*) FROM read_parquet('%s')", parquetSlash)
	cmd2 := exec.CommandContext(context.Background(), duckdbExe, "-c", verifySQL)
	verifyOut, _ := cmd2.CombinedOutput()

	t.Logf("  CSV: %d TXs → %s", len(txs), csvPath)
	t.Logf("  Parquet: %s (%.2f MB) in %v (%.0f tx/s)", parquetPath, float64(fsize)/1e6, parquetDur, float64(len(txs))/parquetDur.Seconds())
	t.Logf("  Verify: %s", strings.TrimSpace(string(verifyOut)))
}
