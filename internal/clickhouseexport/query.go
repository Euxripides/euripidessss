package clickhouseexport

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const maxExportRows uint64 = 50_000_000

var evmAddressRE = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

type datasetSpec struct {
	table          string
	columns        []string
	addressColumns []string
	blockColumn    string
	orderBy        []string
}

var datasetSpecs = map[Dataset]datasetSpec{
	DatasetBlocks: {
		table: "chain_blocks", columns: []string{"chain_id", "block_number", "block_hash", "parent_hash", "block_time", "miner_address", "gas_limit", "gas_used", "base_fee_per_gas", "tx_count", "size_bytes", "source_provider", "ingested_at"},
		blockColumn: "block_number", orderBy: []string{"block_number"},
	},
	DatasetTransactions: {
		table: "chain_transactions", columns: []string{"chain_id", "block_number", "block_hash", "block_time", "transaction_index", "tx_hash", "from_address", "to_address", "nonce", "value_raw", "value_decimal", "native_symbol", "input", "method_id", "method_name", "tx_type", "gas_limit", "gas_price", "gas_used", "transaction_fee_native", "transaction_fee_usd", "status", "is_contract_creation", "created_contract_address", "error_message", "source_provider", "ingest_job_id", "source_range_id", "ingested_at"},
		addressColumns: []string{"from_address", "to_address"}, blockColumn: "block_number", orderBy: []string{"block_number", "transaction_index", "tx_hash"},
	},
	DatasetTokenTransfers: {
		table: "token_transfers", columns: []string{"chain_id", "block_number", "block_time", "tx_hash", "transaction_index", "log_index", "token_address", "token_name", "token_symbol", "token_decimals", "token_standard", "from_address", "to_address", "raw_value", "value_decimal", "usd_price", "usd_value", "token_id", "batch_index", "source_provider", "ingest_job_id", "source_range_id", "ingested_at"},
		addressColumns: []string{"from_address", "to_address"}, blockColumn: "block_number", orderBy: []string{"block_number", "transaction_index", "log_index", "tx_hash"},
	},
	DatasetInternalTxs: {
		table: "internal_transactions", columns: []string{"chain_id", "block_number", "block_time", "tx_hash", "trace_address", "trace_index", "call_type", "from_address", "to_address", "value_raw", "value_decimal", "gas", "gas_used", "input", "output", "success", "error", "depth", "parent_trace_index", "source_provider", "ingest_job_id", "source_range_id", "ingested_at"},
		addressColumns: []string{"from_address", "to_address"}, blockColumn: "block_number", orderBy: []string{"block_number", "tx_hash", "trace_index"},
	},
	DatasetContractCreations: {
		table: "contract_creations", columns: []string{"chain_id", "block_number", "block_time", "tx_hash", "creator_address", "contract_address", "creation_type", "factory_address", "init_code_hash", "runtime_code_hash", "deployer_nonce", "token_detected", "token_standard", "contract_name", "compiler_version", "is_proxy", "proxy_type", "implementation_address", "source_verified", "source_provider", "ingest_job_id", "source_range_id", "ingested_at"},
		addressColumns: []string{"creator_address", "contract_address", "factory_address"}, blockColumn: "block_number", orderBy: []string{"block_number", "tx_hash", "contract_address"},
	},
	DatasetAddressActivity: {
		table: "address_activity", columns: []string{"chain_id", "address", "counterparty_address", "direction", "activity_type", "block_number", "block_time", "tx_hash", "event_index", "token_address", "token_symbol", "amount", "usd_value", "method_id", "method_name", "status", "counterparty_entity_type", "counterparty_label", "source_provider", "ingest_job_id", "source_range_id", "ingested_at"},
		addressColumns: []string{"address"}, blockColumn: "block_number", orderBy: []string{"block_number", "tx_hash", "event_index", "address"},
	},
	DatasetAddressSummary: {
		table: "address_summary", columns: []string{"chain_id", "address", "address_type", "first_seen_time", "last_seen_time", "tx_count", "in_tx_count", "out_tx_count", "token_transfer_count", "internal_tx_count", "nft_transfer_count", "contract_created_count", "unique_counterparty_count", "native_received", "native_sent", "native_netflow", "usd_received", "usd_sent", "usd_netflow", "active_days", "max_single_in_usd", "max_single_out_usd", "top_counterparty", "cex_interaction_count", "dex_interaction_count", "bridge_interaction_count", "risk_score", "updated_at"},
		addressColumns: []string{"address"}, orderBy: []string{"address"},
	},
}

type compiledQuery struct {
	SQL     string
	Columns []string
}

func compile(req Request) (compiledQuery, error) {
	spec, ok := datasetSpecs[req.Dataset]
	if !ok {
		return compiledQuery{}, fmt.Errorf("unsupported export dataset")
	}
	if req.Filter.ChainID == 0 {
		return compiledQuery{}, fmt.Errorf("chain_id is required")
	}
	if req.Filter.FromBlock != nil && req.Filter.ToBlock != nil && *req.Filter.FromBlock > *req.Filter.ToBlock {
		return compiledQuery{}, fmt.Errorf("from_block must not exceed to_block")
	}
	if (req.Filter.FromBlock != nil || req.Filter.ToBlock != nil) && spec.blockColumn == "" {
		return compiledQuery{}, fmt.Errorf("block filters are not supported for this dataset")
	}
	address := strings.ToLower(strings.TrimSpace(req.Filter.Address))
	if address != "" {
		if !evmAddressRE.MatchString(address) {
			return compiledQuery{}, fmt.Errorf("invalid EVM address")
		}
		if len(spec.addressColumns) == 0 {
			return compiledQuery{}, fmt.Errorf("address filter is not supported for this dataset")
		}
	}
	if req.Limit > maxExportRows {
		return compiledQuery{}, fmt.Errorf("limit exceeds %d rows", maxExportRows)
	}

	columns := append([]string(nil), req.Columns...)
	if len(columns) == 0 {
		columns = append(columns, spec.columns...)
	}
	allowed := make(map[string]struct{}, len(spec.columns))
	for _, column := range spec.columns {
		allowed[column] = struct{}{}
	}
	seen := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		if _, ok := allowed[column]; !ok {
			return compiledQuery{}, fmt.Errorf("unsupported column %q", column)
		}
		if _, duplicate := seen[column]; duplicate {
			return compiledQuery{}, fmt.Errorf("duplicate column %q", column)
		}
		seen[column] = struct{}{}
	}

	conditions := []string{"chain_id = " + strconv.FormatUint(uint64(req.Filter.ChainID), 10)}
	if address != "" {
		parts := make([]string, 0, len(spec.addressColumns))
		for _, column := range spec.addressColumns {
			parts = append(parts, column+" = '"+address+"'")
		}
		conditions = append(conditions, "("+strings.Join(parts, " OR ")+")")
	}
	if req.Filter.FromBlock != nil {
		conditions = append(conditions, spec.blockColumn+" >= "+strconv.FormatUint(*req.Filter.FromBlock, 10))
	}
	if req.Filter.ToBlock != nil {
		conditions = append(conditions, spec.blockColumn+" <= "+strconv.FormatUint(*req.Filter.ToBlock, 10))
	}

	query := "SELECT " + strings.Join(columns, ",") + " FROM " + spec.table + " FINAL WHERE " + strings.Join(conditions, " AND ")
	if len(spec.orderBy) > 0 {
		query += " ORDER BY " + strings.Join(spec.orderBy, ",")
	}
	if req.Limit > 0 {
		query += " LIMIT " + strconv.FormatUint(req.Limit, 10)
	}
	return compiledQuery{SQL: query, Columns: columns}, nil
}
