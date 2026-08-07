package parquetdownload

// RPC 恢复数据落盘 + 合并去重测试（Token Transfer Recovery Layer V1.0 §9/§10）。
// 使用真实 DuckDB CLI：验证 CSV → 唯一化 Parquet 落盘、仓库合并唯一键去重。

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/normalize"
)

func recoveryTestRow(txHash string, logIndex, blockNumber uint64) normalize.TokenTransfer {
	return normalize.TokenTransfer{
		ChainKey: "bsc", ChainID: 56, TxHash: txHash, LogIndex: logIndex,
		BlockNumber: blockNumber, TokenAddress: "0x55d398326f99059ff775485246999027b3197955",
		FromAddress: "0x57136ea9b2be6cd4ad74c3ca5b24172f87c9cb8d",
		ToAddress:   "0x238a03c0dcb0f0c4c4c5b6b7c8c9d0e1f2a3b4c5",
		AmountRaw:   "1000000000000000000", Standard: "BEP20",
	}
}

// writeTestTokenTransferParquet 将行写入任意路径的 token_transfers parquet（与 SQD 同 schema 转换）。
func writeTestTokenTransferParquet(t *testing.T, manager *Manager, ctx context.Context, path string, rows []normalize.TokenTransfer) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	csvPath := filepath.Join(filepath.Dir(path), "transfers.csv")
	file, err := os.Create(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := csv.NewWriter(file)
	if err := writer.Write(recoveryTokenTransferColumns); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		// 仓库数据带真实 block_time（SQD 行为），恢复数据为空
		blockTime := ""
		if row.BlockNumber%2 == 0 {
			blockTime = "2026-01-01T00:00:00Z"
		}
		if err := writer.Write([]string{
			row.ChainKey, "56", row.TxHash, strconv.FormatUint(row.LogIndex, 10), strconv.FormatUint(row.BlockNumber, 10), blockTime,
			row.TokenAddress, row.FromAddress, row.ToAddress, row.AmountRaw, "", "", "", row.Standard,
		}); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	sqlText := duckDBSettingsSQL(manager.settings) + "; COPY (" + recoveryTokenTransferSelect(csvPath) + ") TO " +
		sqlString(path) + " (FORMAT PARQUET, COMPRESSION ZSTD)"
	if output, err := manager.engine.ExecSQL(ctx, sqlText); err != nil {
		t.Fatalf("write test parquet: %v %s", err, output)
	}
}

func TestRecoveryWriterAndMerge(t *testing.T) {
	root := repositoryRoot(t)
	exe := filepath.Join(root, "tools", "duckdb", "duckdb.exe")
	if _, err := os.Stat(exe); err != nil {
		t.Skip("bundled DuckDB executable not available")
	}
	testRoot := filepath.Join(root, "backend", "data", "crypto_parquet", "test-recovery")
	_ = os.RemoveAll(testRoot)
	t.Cleanup(func() { _ = os.RemoveAll(testRoot) })
	if err := os.MkdirAll(testRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(testRoot, "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	engine := duckdb.Open(root, testRoot, duckdb.AnalyticsConfig{
		DuckDBPath:     exe,
		DuckDBDatabase: filepath.Join(testRoot, "test.duckdb"),
	})
	if !engine.Available() {
		t.Skip("duckdb CLI 不可用")
	}
	settings := defaultSettings(root)
	settings.DataRoot = testRoot
	manager := &Manager{engine: engine, settings: settings}
	network := chain.EVM{Key: "bsc", ID: 56, Name: "BNB Smart Chain", NativeSymbol: "BNB"}
	ctx := context.Background()

	rowA := recoveryTestRow("0xaaa", 1, 100)
	rowB := recoveryTestRow("0xbbb", 2, 101)
	rowC := recoveryTestRow("0xccc", 3, 102)
	rowD := recoveryTestRow("0xddd", 4, 103)

	// 1) RPC 恢复落盘：任务 1（A、B）+ 任务 2（A 跨任务重复、C）
	res1, err := manager.WriteTokenTransfers(ctx, "plan-x-1", network, []normalize.TokenTransfer{rowA, rowB})
	if err != nil {
		t.Fatalf("write task1: %v", err)
	}
	if res1.Rows != 2 {
		t.Fatalf("task1 rows: %d", res1.Rows)
	}
	if _, err := os.Stat(res1.ParquetPath); err != nil {
		t.Fatalf("recovery parquet missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(res1.ParquetPath), "manifest.json")); err != nil {
		t.Fatalf("recovery manifest missing: %v", err)
	}
	if _, err := manager.WriteTokenTransfers(ctx, "plan-x-2", network, []normalize.TokenTransfer{rowA, rowC}); err != nil {
		t.Fatalf("write task2: %v", err)
	}

	// 2) 仓库预置：A（SQD 版，带 block_time）+ D
	warehousePath := filepath.Join(testRoot, "warehouse", "token_transfers", "chain=bsc", "job=abc", "token-transfers.parquet")
	writeTestTokenTransferParquet(t, manager, ctx, warehousePath, []normalize.TokenTransfer{rowA, rowD})

	// 3) MERGING 合并：恢复 4 行（A×2+B+C）+ 仓库 2 行（A+D）→ 唯一 4 行（A/B/C/D），去重 2
	stats, err := manager.MergeTokenTransfers(ctx, "plan-x", network)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if stats.RecoveryRows != 4 {
		t.Fatalf("recovery rows: %d, want 4", stats.RecoveryRows)
	}
	if stats.WarehouseRows != 2 {
		t.Fatalf("warehouse rows: %d, want 2", stats.WarehouseRows)
	}
	if stats.MergedRows != 4 {
		t.Fatalf("merged rows: %d, want 4", stats.MergedRows)
	}
	if stats.DuplicateRows != 2 {
		t.Fatalf("duplicate rows: %d, want 2", stats.DuplicateRows)
	}
	if _, err := os.Stat(stats.MergedParquet); err != nil {
		t.Fatalf("merged parquet missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(stats.MergedParquet), "merge.json")); err != nil {
		t.Fatalf("merge manifest missing: %v", err)
	}

	// 4) 合并产物行数复核（DuckDB 读回）
	count, err := manager.parquetRowCount(ctx, []string{stats.MergedParquet})
	if err != nil {
		t.Fatalf("count merged: %v", err)
	}
	if count != 4 {
		t.Fatalf("merged parquet count: %d, want 4", count)
	}

	// 5) 无恢复数据时合并应明确跳过
	if _, err := manager.MergeTokenTransfers(ctx, "plan-none", network); err == nil {
		t.Fatal("expected skip error for plan without recovery data")
	}
}
