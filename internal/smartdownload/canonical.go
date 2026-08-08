package smartdownload

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Canonical Schema（实施方案 §14-§17）：
// Provider 输出必须先标准化为统一列 + 统一唯一键，CSV/SQD/RPC/Cloud 才能 Merge。
// 每个 Part 额外带 source_provider / source_range_id / ingested_at 溯源列。

// canonicalColumns 各 Dataset 的 Canonical 列（写 Parquet 用，按序）。
func canonicalColumns(dataset string) []string {
	base := []string{"chain_id", "block_number", "block_time", "transaction_hash",
		"source_provider", "source_range_id", "ingested_at"}
	switch dataset {
	case DatasetTransactions:
		return append(base, "transaction_index", "from_address", "to_address",
			"value_raw", "input", "method_id", "status", "gas_used", "gas_price")
	case DatasetTokenTransfers:
		return append(base, "log_index", "token_address", "from_address", "to_address",
			"value_raw", "token_standard")
	case DatasetInternalTransactions:
		return append(base, "trace_index", "trace_address", "call_type",
			"from_address", "to_address", "value_raw", "status")
	case DatasetLogs:
		return append(base, "log_index", "contract_address", "topics", "data")
	case DatasetBalances:
		return []string{"chain_id", "address", "balance", "symbol",
			"source_provider", "source_range_id", "ingested_at"}
	default:
		return base
	}
}

// CanonicalKey 按 Dataset 返回唯一键（Phase 3：Provider 切换/去重/校验的依据）。
func CanonicalKey(r Record) string {
	switch r.Dataset {
	case DatasetTransactions:
		return fmt.Sprintf("%d|%s", r.ChainID, strings.ToLower(r.TransactionHash))
	case DatasetInternalTransactions:
		return fmt.Sprintf("%d|%s|%s", r.ChainID, strings.ToLower(r.TransactionHash), traceAddressKey(r))
	case DatasetTokenTransfers, DatasetLogs:
		return fmt.Sprintf("%d|%s|%d", r.ChainID, strings.ToLower(r.TransactionHash), r.LogIndex)
	case DatasetBalances:
		return fmt.Sprintf("%d|%s|%s", r.ChainID, strings.ToLower(r.Address), payloadString(r, "symbol"))
	default:
		return r.UniqueKey()
	}
}

func traceAddressKey(r Record) string {
	switch v := r.Payload["trace_address"].(type) {
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprintf("%v", item))
		}
		return strings.Join(parts, ",")
	case []int:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprintf("%d", item))
		}
		return strings.Join(parts, ",")
	default:
		return payloadString(r, "trace_address")
	}
}

func payloadString(r Record, key string) string {
	if v, ok := r.Payload[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// normalizeRecord 把中间 Record 展开为 Canonical 字段（含溯源列）。
func normalizeRecord(r Record, provider, rangeID string) map[string]any {
	out := map[string]any{}
	for k, v := range r.Payload {
		switch k {
		case "standard":
			out["token_standard"] = v
		case "contract_address", "token_address":
			out[k] = v
		default:
			out[k] = v
		}
	}
	out["chain_id"] = r.ChainID
	out["block_number"] = r.BlockNumber
	out["block_time"] = r.BlockTime
	out["transaction_hash"] = r.TransactionHash
	out["source_provider"] = provider
	out["source_range_id"] = rangeID
	out["ingested_at"] = time.Now().UTC().Format(time.RFC3339)
	switch r.Dataset {
	case DatasetTransactions:
		out["transaction_index"] = r.LogIndex
	case DatasetTokenTransfers, DatasetLogs:
		out["log_index"] = r.LogIndex
	case DatasetInternalTransactions:
		out["trace_index"] = r.LogIndex
	}
	return out
}

// canonicalRow 返回按列序排列的字符串行（CSV → DuckDB → Parquet 用）。
func canonicalRow(dataset string, fields map[string]any) []string {
	cols := canonicalColumns(dataset)
	row := make([]string, 0, len(cols))
	for _, c := range cols {
		v, ok := fields[c]
		if !ok || v == nil {
			row = append(row, "")
			continue
		}
		switch t := v.(type) {
		case []string:
			b, _ := json.Marshal(t)
			row = append(row, string(b))
		case []any:
			b, _ := json.Marshal(t)
			row = append(row, string(b))
		case []int:
			b, _ := json.Marshal(t)
			row = append(row, string(b))
		case string:
			row = append(row, t)
		default:
			row = append(row, fmt.Sprintf("%v", t))
		}
	}
	return row
}

// canonicalTypedSQL DuckDB 类型化 SELECT（read_csv 全字符串 → Parquet 数值列）。
func canonicalTypedSQL(dataset string) string {
	cols := canonicalColumns(dataset)
	casts := map[string]string{
		"chain_id": "BIGINT", "block_number": "BIGINT", "block_time": "BIGINT",
		"transaction_index": "BIGINT", "log_index": "BIGINT", "trace_index": "BIGINT",
		"status": "BIGINT", "gas_used": "VARCHAR", "gas_price": "VARCHAR",
	}
	items := make([]string, 0, len(cols))
	for _, c := range cols {
		if typ, ok := casts[c]; ok {
			items = append(items, fmt.Sprintf(
				"CAST(NULLIF(TRIM(%s), '') AS %s) AS %s", quoteIdent(c), typ, quoteIdent(c)))
		} else {
			items = append(items, quoteIdent(c))
		}
	}
	return strings.Join(items, ", ")
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
