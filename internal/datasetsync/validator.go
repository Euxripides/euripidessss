package datasetsync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/etl/backend/internal/analysis/duckdb"
)

var requiredColumns = []string{
	"chain_id", "block_number", "block_timestamp", "transaction_hash",
	"log_index", "token_address", "from_address", "to_address", "value_raw",
}

// DuckDBValidator 基于 DuckDB 的 Parquet 校验器（Phase 4 §28）。
type DuckDBValidator struct {
	engine *duckdb.Engine
}

// NewDuckDBValidator 创建校验器。
func NewDuckDBValidator(engine *duckdb.Engine) *DuckDBValidator {
	return &DuckDBValidator{engine: engine}
}

func pathListSQL(paths []string) string {
	items := make([]string, 0, len(paths))
	for _, p := range paths {
		items = append(items, "'"+strings.ReplaceAll(strings.ReplaceAll(p, "\\", "/"), "'", "''")+"'")
	}
	return "[" + strings.Join(items, ", ") + "]"
}

// Validate 校验 Schema / Row Count / Unique Key / Duplicate / Min-Max Block（无范围约束）。
func (v *DuckDBValidator) Validate(ctx context.Context, paths []string, expectedRows int64) (Validation, error) {
	return v.ValidateRangeForChain(ctx, paths, expectedRows, 0, 0, 0, nil)
}

// ValidateRange 在 Validate 基础上校验严格区块边界与地址归属（Phase 5.2 §3/§10）。
func (v *DuckDBValidator) ValidateRange(ctx context.Context, paths []string, expectedRows int64,
	fromBlock, toBlock uint64, addresses []string) (Validation, error) {
	return v.ValidateRangeForChain(ctx, paths, expectedRows, fromBlock, toBlock, 0, addresses)
}

// ValidateRangeForChain validates the complete canonical token-transfer
// contract. HTTP/object-store success is not sufficient: the physical schema,
// required values, event key, requested chain/range and wallet participation
// must all agree before a chunk can become authoritative.
func (v *DuckDBValidator) ValidateRangeForChain(ctx context.Context, paths []string, expectedRows int64,
	fromBlock, toBlock uint64, chainID int64, addresses []string) (Validation, error) {
	var out Validation
	if v == nil || v.engine == nil || !v.engine.Available() {
		return out, fmt.Errorf("DuckDB 校验器不可用")
	}
	if len(paths) == 0 {
		if expectedRows <= 0 {
			return Validation{SchemaOK: true}, nil // 0 行 Chunk 无文件也合法
		}
		return out, fmt.Errorf("expected %d rows but no parquet files", expectedRows)
	}
	// Schema 检查
	descSQL := fmt.Sprintf("DESCRIBE SELECT * FROM read_parquet(%s)", pathListSQL(paths))
	desc, err := v.engine.ExecSQLJSON(ctx, descSQL)
	if err != nil {
		return out, fmt.Errorf("parquet schema 读取失败: %w", err)
	}
	got := map[string]bool{}
	types := map[string]string{}
	for _, row := range desc {
		if name, ok := row["column_name"].(string); ok {
			got[name] = true
			types[name] = strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", row["column_type"])))
		}
	}
	out.SchemaOK = true
	for _, col := range requiredColumns {
		if !got[col] {
			out.SchemaOK = false
			out.MissingColumns = append(out.MissingColumns, col)
		}
	}
	for _, col := range []string{"chain_id", "block_number", "block_timestamp", "log_index"} {
		typ := types[col]
		if got[col] && !strings.Contains(typ, "INT") {
			out.InvalidTypes = append(out.InvalidTypes, fmt.Sprintf("%s:%s", col, typ))
		}
	}
	for _, col := range []string{"transaction_hash", "token_address", "from_address", "to_address", "value_raw"} {
		typ := types[col]
		if got[col] && typ != "VARCHAR" {
			out.InvalidTypes = append(out.InvalidTypes, fmt.Sprintf("%s:%s", col, typ))
		}
	}
	if len(out.MissingColumns) > 0 || len(out.InvalidTypes) > 0 {
		out.SchemaOK = false
		return out, fmt.Errorf("parquet schema 不符合 canonical 契约: missing=%v invalid_types=%v",
			out.MissingColumns, out.InvalidTypes)
	}
	// 统计
	rangeCond := "1=1"
	if fromBlock > 0 || toBlock > 0 {
		rangeCond = fmt.Sprintf("(CAST(block_number AS BIGINT) >= %d AND CAST(block_number AS BIGINT) <= %d)", fromBlock, toBlock)
	}
	addrCond := "1=1"
	var inList string
	if len(addresses) > 0 {
		items := make([]string, 0, len(addresses))
		for _, a := range addresses {
			items = append(items, "'"+strings.ReplaceAll(strings.ToLower(strings.TrimSpace(a)), "'", "''")+"'")
		}
		inList = strings.Join(items, ", ")
		addrCond = fmt.Sprintf(
			"(LOWER(CAST(from_address AS VARCHAR)) IN (%s) OR LOWER(CAST(to_address AS VARCHAR)) IN (%s))",
			inList, inList)
	}
	chainCond := "1=1"
	if chainID > 0 {
		chainCond = fmt.Sprintf("CAST(chain_id AS BIGINT) = %d", chainID)
	}
	statsSQL := fmt.Sprintf(
		`SELECT COUNT(*) AS rows,
COUNT(DISTINCT CAST(chain_id AS VARCHAR)||'|'||LOWER(CAST(transaction_hash AS VARCHAR))||'|'||CAST(log_index AS VARCHAR)) AS uniq,
COALESCE(MIN(block_number),0) AS min_block, COALESCE(MAX(block_number),0) AS max_block,
COUNT(*) FILTER (WHERE NOT (%s)) AS range_violations,
COUNT(*) FILTER (WHERE NOT (%s)) AS unexpected_addrs,
COUNT(*) FILTER (WHERE NOT (%s)) AS chain_violations,
COUNT(*) FILTER (WHERE chain_id IS NULL OR block_number IS NULL OR block_timestamp IS NULL OR log_index IS NULL
 OR NULLIF(TRIM(CAST(transaction_hash AS VARCHAR)), '') IS NULL
 OR NULLIF(TRIM(CAST(token_address AS VARCHAR)), '') IS NULL
 OR NULLIF(TRIM(CAST(from_address AS VARCHAR)), '') IS NULL
 OR NULLIF(TRIM(CAST(to_address AS VARCHAR)), '') IS NULL
 OR NULLIF(TRIM(CAST(value_raw AS VARCHAR)), '') IS NULL) AS required_nulls,
COUNT(*) FILTER (WHERE NOT regexp_full_match(LOWER(CAST(transaction_hash AS VARCHAR)), '^0x[0-9a-f]{64}$')) AS invalid_hashes,
COUNT(*) FILTER (WHERE NOT regexp_full_match(LOWER(CAST(token_address AS VARCHAR)), '^0x[0-9a-f]{40}$')
 OR NOT regexp_full_match(LOWER(CAST(from_address AS VARCHAR)), '^0x[0-9a-f]{40}$')
 OR NOT regexp_full_match(LOWER(CAST(to_address AS VARCHAR)), '^0x[0-9a-f]{40}$')) AS invalid_addresses,
COUNT(*) FILTER (WHERE NOT regexp_full_match(CAST(value_raw AS VARCHAR), '^[0-9]+$')) AS invalid_values,
COUNT(*) FILTER (WHERE CAST(block_timestamp AS BIGINT) <= 0) AS invalid_timestamps
FROM read_parquet(%s)`,
		rangeCond, addrCond, chainCond, pathListSQL(paths),
	)
	rows, err := v.engine.ExecSQLJSON(ctx, statsSQL)
	if err != nil {
		return out, fmt.Errorf("parquet 统计失败: %w", err)
	}
	if len(rows) > 0 {
		out.Rows = int64(num(rows[0]["rows"]))
		out.UniqueKeyCount = int64(num(rows[0]["uniq"]))
		out.MinBlock = uint64(num(rows[0]["min_block"]))
		out.MaxBlock = uint64(num(rows[0]["max_block"]))
		out.DuplicateCount = out.Rows - out.UniqueKeyCount
		out.RangeViolations = int64(num(rows[0]["range_violations"]))
		out.UnexpectedAddresses = int64(num(rows[0]["unexpected_addrs"]))
		out.ChainViolations = int64(num(rows[0]["chain_violations"]))
		out.RequiredNulls = int64(num(rows[0]["required_nulls"]))
		out.InvalidHashes = int64(num(rows[0]["invalid_hashes"]))
		out.InvalidAddresses = int64(num(rows[0]["invalid_addresses"]))
		out.InvalidValues = int64(num(rows[0]["invalid_values"]))
		out.InvalidTimestamps = int64(num(rows[0]["invalid_timestamps"]))
	}
	if expectedRows >= 0 && out.Rows != expectedRows {
		return out, fmt.Errorf("行数不匹配：manifest %d vs parquet %d", expectedRows, out.Rows)
	}
	if len(addresses) > 0 && out.Rows > 0 {
		addressSQL := fmt.Sprintf(`SELECT address, COUNT(*) AS rows FROM (
SELECT LOWER(CAST(from_address AS VARCHAR)) AS address FROM read_parquet(%s)
 WHERE LOWER(CAST(from_address AS VARCHAR)) IN (%s)
UNION ALL
SELECT LOWER(CAST(to_address AS VARCHAR)) AS address FROM read_parquet(%s)
 WHERE LOWER(CAST(to_address AS VARCHAR)) IN (%s)
   AND LOWER(CAST(to_address AS VARCHAR)) <> LOWER(CAST(from_address AS VARCHAR))
) GROUP BY address`, pathListSQL(paths), inList, pathListSQL(paths), inList)
		addressRows, err := v.engine.ExecSQLJSON(ctx, addressSQL)
		if err != nil {
			return out, fmt.Errorf("parquet 地址行数统计失败: %w", err)
		}
		out.AddressRowCounts = make(map[string]int64, len(addresses))
		for _, address := range addresses {
			out.AddressRowCounts[strings.ToLower(strings.TrimSpace(address))] = 0
		}
		for _, row := range addressRows {
			address := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", row["address"])))
			if address != "" {
				out.AddressRowCounts[address] = int64(num(row["rows"]))
			}
		}
	}
	return out, nil
}

// PartRows 返回每个 parquet 分片的行数（Manifest V2 校验：sum(parts.rows)==row_count）。
func (v *DuckDBValidator) PartRows(ctx context.Context, paths []string) ([]int64, error) {
	out := make([]int64, 0, len(paths))
	for _, p := range paths {
		escaped := strings.ReplaceAll(strings.ReplaceAll(p, "\\", "/"), "'", "''")
		rows, err := v.engine.ExecSQLJSON(ctx,
			fmt.Sprintf("SELECT COUNT(*) AS n FROM read_parquet('%s')", escaped))
		if err != nil {
			return nil, fmt.Errorf("part rows %s: %w", p, err)
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("part rows empty: %s", p)
		}
		out = append(out, int64(num(rows[0]["n"])))
	}
	return out, nil
}

// Merge 合并 parquet 到统一查询层（Phase 4 §44/§46：同一 DuckDB 查询层）。
// 按唯一键去重并原子替换，避免跨 chunk 重复或写入中途被查询读到半成品。
func (v *DuckDBValidator) Merge(ctx context.Context, paths []string, outDir string) (string, error) {
	if v == nil || v.engine == nil || !v.engine.Available() {
		return "", fmt.Errorf("DuckDB 不可用")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	merged := filepath.Join(outDir, "merged.parquet")
	tmp := merged + ".tmp"
	_ = os.Remove(tmp)
	uniqueKey := `CAST(chain_id AS VARCHAR)||'|'||LOWER(CAST(transaction_hash AS VARCHAR))||'|'||CAST(log_index AS VARCHAR)`
	sql := fmt.Sprintf(`
COPY (
  SELECT chain_id, block_number, block_timestamp, transaction_hash, log_index,
         token_address, from_address, to_address, value_raw
  FROM (
    SELECT *, ROW_NUMBER() OVER (PARTITION BY %s ORDER BY transaction_hash, log_index) AS __rn
    FROM read_parquet(%s)
  ) WHERE __rn = 1
) TO '%s' (FORMAT PARQUET, COMPRESSION GZIP)`, uniqueKey, pathListSQL(paths), strings.ReplaceAll(tmp, "\\", "/"))
	if _, err := v.engine.ExecSQL(ctx, sql); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, merged); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return merged, nil
}

func num(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}
