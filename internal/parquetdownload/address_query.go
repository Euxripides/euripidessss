package parquetdownload

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/etl/backend/internal/chain"
)

func (h *Handler) address(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeMethodNotAllowed(writer)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(request.URL.Path, "/address/"), "/"), "/")
	if len(parts) != 2 {
		writeError(writer, http.StatusNotFound, errors.New("地址分析接口不存在"))
		return
	}
	address := strings.ToLower(parts[0])
	if !isEVMAddress(address) {
		writeError(writer, http.StatusBadRequest, errors.New("EVM 地址格式错误"))
		return
	}
	network, err := chain.Resolve(request.URL.Query().Get("chain_key"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	limit := boundedQueryInt(request.URL.Query().Get("limit"), 50, 1, 200)
	offset := boundedQueryInt(request.URL.Query().Get("offset"), 0, 0, 1_000_000)
	var payload any
	switch parts[1] {
	case "summary":
		payload, err = h.manager.queryAddressSummary(request.Context(), network.Key, address)
	case "activity":
		payload, err = h.manager.queryAddressActivity(request.Context(), network.Key, address, limit, offset)
	case "tokens":
		payload, err = h.manager.queryAddressAssets(request.Context(), network.Key, address, "TOKEN", limit, offset)
	case "nfts":
		payload, err = h.manager.queryAddressAssets(request.Context(), network.Key, address, "NFT", limit, offset)
	case "counterparties":
		payload, err = h.manager.queryAddressCounterparties(request.Context(), network.Key, address, limit, offset)
	default:
		writeError(writer, http.StatusNotFound, errors.New("地址分析接口不存在"))
		return
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errNoAddressData) {
			status = http.StatusNotFound
		}
		writeError(writer, status, err)
		return
	}
	writeJSON(writer, http.StatusOK, payload)
}

var errNoAddressData = errors.New("该链地址尚无可查询数据")

func (m *Manager) queryAddressSummary(ctx context.Context, chainKey, address string) (map[string]any, error) {
	paths := m.warehouseFiles("address_summary", chainKey)
	if len(paths) == 0 {
		return nil, errNoAddressData
	}
	rows, err := m.engine.ExecSQLJSON(ctx, `SELECT * FROM read_parquet(`+sqlStringList(paths)+`, union_by_name=true)
WHERE chain_key = `+sqlString(chainKey)+` AND address = `+sqlString(address)+`
ORDER BY last_active_time DESC NULLS LAST LIMIT 1`)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errNoAddressData
	}
	rows[0]["chain_key"] = chainKey
	rows[0]["address"] = address
	network, resolveErr := chain.Resolve(chainKey)
	if resolveErr == nil {
		rpcConfigured := m.rpcConfigured(network.Key, network.RPCEnv)
		rows[0]["rpc_configured"] = rpcConfigured
		rows[0]["rpc_env"] = network.RPCEnv
		rows[0]["address_type_reason"] = addressTypeReason(
			fmt.Sprint(rows[0]["address_type"]), rpcConfigured, network.RPCEnv,
		)
		m.mu.RLock()
		manager := m.rpcManager
		m.mu.RUnlock()
		if manager != nil && manager.HasConfigured(network.Key) {
			if enriched, enrichErr := manager.Address(ctx, network.Key, address, false); enrichErr == nil {
				rows[0]["address_type"] = enriched.AddressType
				rows[0]["address_type_reason"] = enriched.Reason
				rows[0]["native_balance_raw"] = enriched.NativeBalanceRaw
				rows[0]["native_balance"] = enriched.NativeBalance
				rows[0]["native_symbol"] = enriched.NativeSymbol
				rows[0]["rpc_cached"] = enriched.Cached
				rows[0]["rpc_checked_at"] = enriched.CheckedAt
			}
		}
	}
	return rows[0], nil
}

func (m *Manager) queryAddressActivity(ctx context.Context, chainKey, address string, limit, offset int) (map[string]any, error) {
	paths := m.warehouseFiles("address_activity", chainKey)
	if len(paths) == 0 {
		return nil, errNoAddressData
	}
	source := deduplicatedActivitySource(paths)
	return m.queryPage(ctx,
		`SELECT * FROM `+source+` WHERE chain_key = `+sqlString(chainKey)+` AND address = `+sqlString(address)+
			` ORDER BY block_time DESC, tx_hash LIMIT `+strconv.Itoa(limit)+` OFFSET `+strconv.Itoa(offset),
		`SELECT COUNT(*) AS total FROM `+source+` WHERE chain_key = `+sqlString(chainKey)+` AND address = `+sqlString(address),
		limit, offset,
	)
}

func (m *Manager) queryAddressAssets(ctx context.Context, chainKey, address, kind string, limit, offset int) (map[string]any, error) {
	paths := m.warehouseFiles("address_activity", chainKey)
	if len(paths) == 0 {
		return nil, errNoAddressData
	}
	filter := "asset_type = 'TOKEN'"
	if kind == "NFT" {
		filter = "asset_type IN ('ERC721', 'ERC1155')"
	}
	source := deduplicatedActivitySource(paths)
	grouped := `SELECT asset_address, any_value(symbol) AS symbol, any_value(asset_type) AS asset_type,
COUNT(*) AS activity_count, MAX(block_time) AS last_active_time
FROM ` + source + ` WHERE chain_key = ` + sqlString(chainKey) + ` AND address = ` + sqlString(address) + ` AND ` + filter + `
GROUP BY asset_address`
	payload, err := m.queryPage(ctx,
		`SELECT * FROM (`+grouped+`) ORDER BY last_active_time DESC LIMIT `+strconv.Itoa(limit)+` OFFSET `+strconv.Itoa(offset),
		`SELECT COUNT(*) AS total FROM (`+grouped+`)`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	if kind == "TOKEN" {
		m.enrichAssetRows(ctx, chainKey, address, payload["rows"].([]map[string]any))
	}
	return payload, nil
}

func (m *Manager) enrichAssetRows(ctx context.Context, chainKey, address string, rows []map[string]any) {
	metadataPaths := m.warehouseFiles("token_metadata", chainKey)
	balancePaths := m.warehouseFiles("balances", chainKey)
	for _, row := range rows {
		token := strings.ToLower(fmt.Sprint(row["asset_address"]))
		if len(metadataPaths) > 0 {
			values, err := m.engine.ExecSQLJSON(ctx, `SELECT name, symbol, decimals, standard, total_supply, source
FROM read_parquet(`+sqlStringList(metadataPaths)+`, union_by_name=true)
WHERE chain_key = `+sqlString(chainKey)+` AND token_address = `+sqlString(token)+`
ORDER BY updated_at DESC LIMIT 1`)
			if err == nil && len(values) > 0 {
				for key, value := range values[0] {
					row[key] = value
				}
			}
		}
		if len(balancePaths) > 0 {
			values, err := m.engine.ExecSQLJSON(ctx, `SELECT balance_raw, balance, snapshot_time
FROM read_parquet(`+sqlStringList(balancePaths)+`, union_by_name=true)
WHERE chain_key = `+sqlString(chainKey)+` AND address = `+sqlString(address)+` AND asset_address = `+sqlString(token)+`
ORDER BY snapshot_time DESC LIMIT 1`)
			if err == nil && len(values) > 0 {
				for key, value := range values[0] {
					row[key] = value
				}
			}
		}
		if knownMetadataValue(row["source"]) {
			continue
		}
		m.mu.RLock()
		manager := m.rpcManager
		m.mu.RUnlock()
		if manager == nil || !manager.HasConfigured(chainKey) {
			continue
		}
		metadata, err := manager.Token(ctx, chainKey, token, false)
		if err != nil {
			continue
		}
		row["name"], row["symbol"], row["decimals"] = metadata.Name, metadata.Symbol, metadata.Decimals
		row["total_supply"], row["source"] = metadata.TotalSupply, "RPC"
		if metadata.Status == "PARTIAL" || metadata.Status == "UNKNOWN" {
			row["source"] = "RPC_PARTIAL"
		}
	}
}

func knownMetadataValue(value any) bool {
	source := strings.ToUpper(strings.TrimSpace(fmt.Sprint(value)))
	return source == "RPC" || source == "RPC_PARTIAL"
}

func (m *Manager) queryAddressCounterparties(ctx context.Context, chainKey, address string, limit, offset int) (map[string]any, error) {
	paths := m.warehouseFiles("address_activity", chainKey)
	if len(paths) == 0 {
		return nil, errNoAddressData
	}
	source := deduplicatedActivitySource(paths)
	grouped := `SELECT counterparty, COUNT(*) AS activity_count, COUNT(DISTINCT tx_hash) AS tx_count,
CASE
  WHEN COUNT(*) FILTER (WHERE direction = 'IN') > 0 AND COUNT(*) FILTER (WHERE direction = 'OUT') > 0 THEN 'BOTH'
  WHEN COUNT(*) FILTER (WHERE direction = 'IN') > 0 THEN 'IN'
  ELSE 'OUT'
END AS direction,
COUNT(*) FILTER (WHERE asset_type = 'NATIVE' AND direction = 'IN') AS native_in_count,
COUNT(*) FILTER (WHERE asset_type = 'NATIVE' AND direction = 'OUT') AS native_out_count,
COUNT(*) FILTER (WHERE asset_type <> 'NATIVE' AND direction = 'IN') AS token_in_count,
COUNT(*) FILTER (WHERE asset_type <> 'NATIVE' AND direction = 'OUT') AS token_out_count,
MIN(block_time) AS first_active_time, MAX(block_time) AS last_active_time
FROM ` + source + ` WHERE chain_key = ` + sqlString(chainKey) + ` AND address = ` + sqlString(address) + `
AND counterparty IS NOT NULL AND counterparty <> '' GROUP BY counterparty`
	return m.queryPage(ctx,
		`SELECT * FROM (`+grouped+`) ORDER BY activity_count DESC, last_active_time DESC LIMIT `+strconv.Itoa(limit)+` OFFSET `+strconv.Itoa(offset),
		`SELECT COUNT(*) AS total FROM (`+grouped+`)`,
		limit, offset,
	)
}

func (m *Manager) queryPage(ctx context.Context, dataSQL, countSQL string, limit, offset int) (map[string]any, error) {
	rows, err := m.engine.ExecSQLJSON(ctx, dataSQL)
	if err != nil {
		return nil, err
	}
	countRows, err := m.engine.ExecSQLJSON(ctx, countSQL)
	if err != nil {
		return nil, err
	}
	total := int64(0)
	if len(countRows) > 0 {
		total = numberToInt64(countRows[0]["total"])
	}
	return map[string]any{"rows": rows, "total": total, "limit": limit, "offset": offset}, nil
}

func (m *Manager) warehouseFiles(table, chainKey string) []string {
	paths, _ := filepath.Glob(filepath.Join(m.Settings().DataRoot, "warehouse", table, "chain="+chainKey, "job=*", "*.parquet"))
	return paths
}

// deduplicatedActivitySource prevents overlapping or retried jobs from inflating
// address statistics while preserving distinct transfers within the same tx.
func deduplicatedActivitySource(paths []string) string {
	return `(SELECT * EXCLUDE (_activity_row)
FROM (
  SELECT *, ROW_NUMBER() OVER (
    PARTITION BY chain_key, address, tx_hash, direction, activity_type,
      COALESCE(counterparty, ''), COALESCE(asset_type, ''),
      COALESCE(asset_address, ''), COALESCE(amount_raw, ''),
      COALESCE(method_id, ''), COALESCE(trace_depth, 0)
    ORDER BY CASE
      WHEN upper(COALESCE(status, '')) IN ('SUCCESS', 'FAILED') THEN 2
      WHEN upper(COALESCE(status, '')) = 'UNKNOWN' THEN 1
      ELSE 0
    END DESC, block_time DESC NULLS LAST
  ) AS _activity_row
  FROM read_parquet(` + sqlStringList(paths) + `, union_by_name=true)
)
WHERE _activity_row = 1)`
}

func isEVMAddress(address string) bool {
	if len(address) != 42 || !strings.HasPrefix(address, "0x") {
		return false
	}
	for _, char := range address[2:] {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func boundedQueryInt(raw string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func addressTypeReason(addressType string, rpcConfigured bool, rpcEnv string) string {
	switch strings.ToUpper(strings.TrimSpace(addressType)) {
	case "EOA":
		return "RPC eth_getCode 返回空字节码，判定为外部账户"
	case "CONTRACT":
		return "RPC eth_getCode 返回合约字节码，判定为合约地址"
	default:
		if !rpcConfigured {
			return "未配置 " + rpcEnv + "，尚未执行 RPC 地址类型检测"
		}
		return "RPC 已配置，但本次快照未获得有效地址类型结果"
	}
}
