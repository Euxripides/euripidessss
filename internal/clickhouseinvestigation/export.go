package clickhouseinvestigation

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const maxExportRows = 1000000

type ExportRequest struct {
	Dataset   string
	Address   string
	Token     string
	FromBlock uint64
	ToBlock   uint64
	Limit     int
}

type exportSpec struct {
	table            string
	columns          []string
	addressPredicate string
	tokenColumn      string
}

var exportSpecs = map[string]exportSpec{
	"activity":              {"address_activity", []string{"chain_id", "address", "counterparty_address", "direction", "activity_type", "block_number", "block_time", "tx_hash", "event_index", "token_address", "token_symbol", "amount", "toString(price_usd) AS historical_price_usdt", "toString(usd_value) AS historical_value_usdt", "price_time AS price_timestamp", "price_source", "'' AS price_route", "price_confidence", "if(isNull(price_usd),'UNKNOWN','TRADED') AS price_type", "if(isNull(price_usd),'NO_PRICE','VALUED') AS valuation_status", "status", "source_provider", "ingest_job_id", "source_range_id"}, "address = %s", "token_address"},
	"transactions":          {"chain_transactions", []string{"chain_id", "block_number", "block_time", "transaction_index", "tx_hash", "from_address", "to_address", "value_raw", "method_id", "status", "source_provider", "ingest_job_id", "source_range_id"}, "(from_address = %s OR to_address = %s)", ""},
	"token_transfers":       {"token_transfers", []string{"chain_id", "block_number", "block_time", "tx_hash", "transaction_index", "log_index", "token_address", "token_symbol", "token_standard", "from_address", "to_address", "raw_value", "toString(usd_price) AS historical_price_usdt", "toString(usd_value) AS historical_value_usdt", "price_time AS price_timestamp", "price_source", "'' AS price_route", "price_confidence", "if(isNull(usd_price),'UNKNOWN','TRADED') AS price_type", "if(isNull(usd_price),'NO_PRICE','VALUED') AS valuation_status", "source_provider", "ingest_job_id", "source_range_id"}, "(from_address = %s OR to_address = %s)", "token_address"},
	"internal_transactions": {"internal_transactions", []string{"chain_id", "block_number", "block_time", "tx_hash", "trace_address", "trace_index", "call_type", "from_address", "to_address", "value_raw", "success", "error", "source_provider", "ingest_job_id", "source_range_id"}, "(from_address = %s OR to_address = %s)", ""},
	"contract_creations":    {"contract_creations", []string{"chain_id", "block_number", "block_time", "tx_hash", "creator_address", "contract_address", "creation_type", "factory_address", "token_detected", "token_standard", "contract_name", "source_provider", "ingest_job_id", "source_range_id"}, "(creator_address = %s OR contract_address = %s)", ""},
}

// ExportCSV streams an address-scoped export directly from ClickHouse. The
// result includes a stable header and never stages a database or Parquet file.
func (r *Repository) ExportCSV(ctx context.Context, output io.Writer, request ExportRequest) (int64, error) {
	if output == nil {
		return 0, fmt.Errorf("CSV output is required")
	}
	spec, ok := exportSpecs[strings.ToLower(strings.TrimSpace(request.Dataset))]
	if !ok {
		return 0, fmt.Errorf("unsupported export dataset")
	}
	address, err := normalizeAddress(request.Address, "address", false)
	if err != nil {
		return 0, err
	}
	token, err := normalizeAddress(request.Token, "token", true)
	if err != nil {
		return 0, err
	}
	if token != "" && spec.tokenColumn == "" {
		return 0, fmt.Errorf("token filter is not supported for this dataset")
	}
	if request.ToBlock > 0 && request.FromBlock > request.ToBlock {
		return 0, fmt.Errorf("from_block must not exceed to_block")
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 100000
	}
	if limit > maxExportRows {
		return 0, fmt.Errorf("export limit exceeds %d rows", maxExportRows)
	}
	quoted := "'" + address + "'"
	predicate := fmt.Sprintf(spec.addressPredicate, quoted)
	if strings.Count(spec.addressPredicate, "%s") == 2 {
		predicate = fmt.Sprintf(spec.addressPredicate, quoted, quoted)
	}
	clauses := []string{"chain_id = " + strconv.FormatUint(uint64(r.chainID), 10), predicate}
	if token != "" {
		clauses = append(clauses, spec.tokenColumn+" = '"+token+"'")
	}
	if request.FromBlock > 0 {
		clauses = append(clauses, "block_number >= "+strconv.FormatUint(request.FromBlock, 10))
	}
	if request.ToBlock > 0 {
		clauses = append(clauses, "block_number <= "+strconv.FormatUint(request.ToBlock, 10))
	}
	query := fmt.Sprintf("SELECT %s FROM %s FINAL WHERE %s ORDER BY block_time ASC, block_number ASC, tx_hash ASC LIMIT %d", strings.Join(spec.columns, ","), spec.table, strings.Join(clauses, " AND "), limit)
	stream, err := r.client.QueryCSV(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("query ClickHouse CSV export: %w", err)
	}
	defer stream.Close()
	header := csv.NewWriter(output)
	if err := header.Write(exportHeaders(spec.columns)); err != nil {
		return 0, fmt.Errorf("write CSV header: %w", err)
	}
	header.Flush()
	if err := header.Error(); err != nil {
		return 0, fmt.Errorf("flush CSV header: %w", err)
	}
	written, err := io.Copy(output, stream)
	if err != nil {
		return written, fmt.Errorf("stream ClickHouse CSV export: %w", err)
	}
	return written, nil
}

func exportHeaders(columns []string) []string {
	headers := make([]string, len(columns))
	for index, column := range columns {
		upper := strings.ToUpper(column)
		if position := strings.LastIndex(upper, " AS "); position >= 0 {
			headers[index] = strings.TrimSpace(column[position+4:])
		} else {
			headers[index] = strings.TrimSpace(column)
		}
	}
	return headers
}
