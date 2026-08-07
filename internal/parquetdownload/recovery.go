package parquetdownload

// RPC 恢复数据落盘与合并（Token Transfer Multi-Provider Recovery Layer V1.0 §6/§9/§10）：
//   - 落盘：{DataRoot}/recovery/token_transfers/chain=<key>/{taskKey}/token-transfers.parquet
//     与 SQD token_transfers 同 schema（14 列），唯一键 chain_id + tx_hash + log_index + token_address
//   - 合并：{DataRoot}/recovery/merge/{planID}/token-transfers.parquet
//     计划内恢复数据 + 仓库既有 token_transfers 按唯一键去重（同日志优先保留带 block_time 的 SQD 行）

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/normalize"
)

// RecoveryWriteResult RPC 恢复数据落盘结果。
type RecoveryWriteResult struct {
	ParquetPath string `json:"parquet_path"`
	Rows        int64  `json:"rows"`
	TaskKey     string `json:"task_key"`
	UniqueKey   string `json:"unique_key"`
}

// RecoveryMergeStats 恢复数据与仓库数据合并统计（设计 §9/§10 唯一化）。
type RecoveryMergeStats struct {
	ChainKey      string `json:"chain_key"`
	RecoveryRows  int64  `json:"recovery_rows"`
	WarehouseRows int64  `json:"warehouse_rows"`
	MergedRows    int64  `json:"merged_rows"`
	DuplicateRows int64  `json:"duplicate_rows"`
	MergedParquet string `json:"merged_parquet"`
}

// recoveryUniqueKey 唯一键描述（设计 §10）。
const recoveryUniqueKey = "chain_id + transaction_hash + log_index + token_address"

// recoveryTokenTransferColumns 与 SQD writeTokenTransfer 对齐的 CSV 列。
var recoveryTokenTransferColumns = []string{
	"chain_key", "chain_id", "tx_hash", "log_index", "block_number", "block_time",
	"token_address", "from_address", "to_address", "amount_raw", "amount", "symbol", "decimals", "standard",
}

// WriteTokenTransfers 将 RPC 恢复的 Token Transfer 行落盘为唯一化 Parquet（与 SQD token_transfers 同 schema）。
// taskKey 为调度器任务标识（格式 {planID}-{taskNo}）。
func (m *Manager) WriteTokenTransfers(ctx context.Context, taskKey string, network chain.EVM, rows []normalize.TokenTransfer) (*RecoveryWriteResult, error) {
	if len(rows) == 0 {
		return nil, errors.New("没有可落盘的 Token Transfer 行")
	}
	if taskKey == "" {
		return nil, errors.New("恢复任务标识为空")
	}
	dir := filepath.Join(m.settings.DataRoot, "recovery", "token_transfers", "chain="+network.Key, taskKey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	csvPath := filepath.Join(dir, "transfers.csv")
	file, err := os.Create(csvPath)
	if err != nil {
		return nil, err
	}
	writer := csv.NewWriter(file)
	if err := writer.Write(recoveryTokenTransferColumns); err != nil {
		_ = file.Close()
		return nil, err
	}
	for _, item := range rows {
		if err := writeRecoveryTokenTransfer(writer, item); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	parquetPath := filepath.Join(dir, "token-transfers.parquet")
	// 复用 SQD 相同的类型转换与列序；DISTINCT 兜底保证表内无重复行。
	// 注意：read_csv 显式指定方言 + strict_mode=false——内置 DuckDB 构建的自动嗅探对
	// 小文件/空字段不可靠（2026-08-03 实测 sniffing 失败），与 CreateTableFromCSVFiles 同策略。
	sqlText := duckDBSettingsSQL(m.settings) + "; COPY (SELECT DISTINCT * FROM (" + recoveryTokenTransferSelect(csvPath) + ")) TO " +
		sqlString(parquetPath) + " (FORMAT PARQUET, COMPRESSION ZSTD)"
	if _, err := m.engine.ExecSQL(ctx, sqlText); err != nil {
		return nil, fmt.Errorf("写入恢复 token_transfers: %w", err)
	}
	manifest := map[string]any{
		"provider":   "rpc",
		"task_key":   taskKey,
		"chain_key":  network.Key,
		"chain_id":   network.ID,
		"rows":       len(rows),
		"unique_key": recoveryUniqueKey,
		"parquet":    parquetPath,
		"written_at": time.Now().UTC().Format(time.RFC3339),
	}
	payload, _ := json.MarshalIndent(manifest, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), payload, 0o644)
	return &RecoveryWriteResult{
		ParquetPath: parquetPath, Rows: int64(len(rows)), TaskKey: taskKey, UniqueKey: recoveryUniqueKey,
	}, nil
}

// writeRecoveryTokenTransfer 与 SQD writeTokenTransfer 同列序；block_time/amount/symbol/decimals 为空
// （RPC eth_getLogs 不含区块时间戳，设计偏差 #3；金额换算需 Token Metadata，留待 V1.1 富化）。
func writeRecoveryTokenTransfer(writer *csv.Writer, item normalize.TokenTransfer) error {
	return writer.Write([]string{
		item.ChainKey, strconv.FormatInt(item.ChainID, 10), item.TxHash, strconv.FormatUint(item.LogIndex, 10),
		strconv.FormatUint(item.BlockNumber, 10), "",
		item.TokenAddress, item.FromAddress, item.ToAddress, item.AmountRaw, "", "", "",
		item.Standard,
	})
}

// MergeTokenTransfers 将计划内 RPC 恢复数据与仓库既有 token_transfers 按唯一键合并去重
// （设计 §9 数据合并 → 唯一化 → Parquet）。同一条日志优先保留带 block_time 的 SQD 行。
func (m *Manager) MergeTokenTransfers(ctx context.Context, planID string, network chain.EVM) (*RecoveryMergeStats, error) {
	if planID == "" {
		return nil, errors.New("合并缺少计划 ID")
	}
	recoveryRoot := filepath.Join(m.settings.DataRoot, "recovery", "token_transfers", "chain="+network.Key)
	recoveryPaths := recoveryParquetFiles(recoveryRoot, planID)
	if len(recoveryPaths) == 0 {
		return nil, fmt.Errorf("计划 %s 无 RPC 恢复数据（chain=%s），跳过合并", planID, network.Key)
	}
	warehouseGlob := filepath.Join(m.settings.DataRoot, "warehouse", "token_transfers", "chain="+network.Key, "job=*", "token-transfers.parquet")
	warehousePaths, _ := filepath.Glob(warehouseGlob)
	sort.Strings(warehousePaths)

	recoveryRows, err := m.parquetRowCount(ctx, recoveryPaths)
	if err != nil {
		return nil, fmt.Errorf("统计恢复数据: %w", err)
	}
	warehouseRows, err := m.parquetRowCount(ctx, warehousePaths)
	if err != nil {
		return nil, fmt.Errorf("统计仓库数据: %w", err)
	}
	mergedDir := filepath.Join(m.settings.DataRoot, "recovery", "merge", planID)
	if err := os.MkdirAll(mergedDir, 0o755); err != nil {
		return nil, err
	}
	mergedPath := filepath.Join(mergedDir, "token-transfers.parquet")
	all := append(append([]string{}, recoveryPaths...), warehousePaths...)
	// 唯一键分区 + ROW_NUMBER 保序：block_time 非空（SQD）优先，均空（RPC 恢复）时稳定取首行
	sqlText := duckDBSettingsSQL(m.settings) + `; COPY (
		SELECT chain_key, chain_id, tx_hash, log_index, block_number, block_time,
			token_address, from_address, to_address, amount_raw, amount, symbol, decimals, standard
		FROM (
			SELECT *, ROW_NUMBER() OVER (
				PARTITION BY chain_id, tx_hash, log_index, token_address
				ORDER BY block_time DESC NULLS LAST
			) AS rn
			FROM read_parquet(` + sqlStringList(all) + `)
		) WHERE rn = 1
	) TO ` + sqlString(mergedPath) + ` (FORMAT PARQUET, COMPRESSION ZSTD, ROW_GROUP_SIZE 250000)`
	if _, err := m.engine.ExecSQL(ctx, sqlText); err != nil {
		return nil, fmt.Errorf("合并恢复数据: %w", err)
	}
	mergedRows, err := m.parquetRowCount(ctx, []string{mergedPath})
	if err != nil {
		return nil, fmt.Errorf("统计合并结果: %w", err)
	}
	stats := &RecoveryMergeStats{
		ChainKey:      network.Key,
		RecoveryRows:  recoveryRows,
		WarehouseRows: warehouseRows,
		MergedRows:    mergedRows,
		DuplicateRows: recoveryRows + warehouseRows - mergedRows,
		MergedParquet: mergedPath,
	}
	payload, _ := json.MarshalIndent(map[string]any{
		"plan_id": planID, "unique_key": recoveryUniqueKey, "stats": stats, "merged_at": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	_ = os.WriteFile(filepath.Join(mergedDir, "merge.json"), payload, 0o644)
	return stats, nil
}

// recoveryTokenTransferSelect 恢复 CSV 读入 + 类型转换（与 sqdTypedSelect("token_transfers") 同列序）。
// read_csv 显式指定方言（delim/quote/escape）+ strict_mode=false，规避内置 DuckDB 构建的自动嗅探缺陷。
func recoveryTokenTransferSelect(csvPath string) string {
	source := "read_csv(" + sqlString(csvPath) + ", header=true, all_varchar=true, strict_mode=false, ignore_errors=true, null_padding=true, delim=',', quote='\"', escape='\"')"
	return `SELECT lower(chain_key) AS chain_key, TRY_CAST(chain_id AS UBIGINT) AS chain_id,
lower(tx_hash) AS tx_hash, TRY_CAST(log_index AS UINTEGER) AS log_index,
TRY_CAST(block_number AS UBIGINT) AS block_number, TRY_CAST(block_time AS TIMESTAMPTZ) AS block_time,
lower(token_address) AS token_address, lower(from_address) AS from_address, lower(to_address) AS to_address,
amount_raw, nullif(amount, '') AS amount, nullif(symbol, '') AS symbol,
TRY_CAST(decimals AS UTINYINT) AS decimals, standard FROM ` + source
}

// recoveryParquetFiles 返回计划（planID）名下全部恢复任务目录的 parquet 文件。
func recoveryParquetFiles(root, planID string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), planID+"-") {
			continue
		}
		path := filepath.Join(root, entry.Name(), "token-transfers.parquet")
		if _, statErr := os.Stat(path); statErr == nil {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// parquetRowCount 统计 parquet 文件集合总行数（空集合返回 0）。
func (m *Manager) parquetRowCount(ctx context.Context, paths []string) (int64, error) {
	if len(paths) == 0 {
		return 0, nil
	}
	rows, err := m.engine.ExecSQLJSON(ctx, "SELECT COUNT(*) AS cnt FROM read_parquet("+sqlStringList(paths)+")")
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	cnt, _ := rows[0]["cnt"].(float64)
	return int64(cnt), nil
}
