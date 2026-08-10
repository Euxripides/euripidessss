package financialintegration

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

var historicalActivityColumns = []string{
	"chain_id", "address", "counterparty_address", "direction", "activity_type", "block_number", "block_time",
	"tx_hash", "event_index", "token_address", "token_symbol", "amount", "historical_price", "historical_usd",
	"price_time", "price_source", "price_confidence", "entity_id", "entity_name", "entity_type", "entity_role",
}

var historicalEdgeColumns = []string{
	"chain_id", "from_address", "to_address", "transaction_count", "event_count", "first_time", "last_time",
	"token_address", "token_symbol", "token_amount", "historical_usd", "historical_price", "price_time",
	"price_source", "price_confidence", "entity_id", "entity_name", "entity_type", "entity_role",
}

var algorithmColumns = []string{
	"metric", "window", "value_usd", "ratio", "coverage", "confidence", "algorithm_version", "price_version",
	"from", "to", "token_filter",
}

type Exporter struct{ client QueryClient }

func NewExporter(client QueryClient) *Exporter { return &Exporter{client: client} }

// StreamHistoricalCSV emits a fixed, reviewable financial whitelist. Callers
// receive bytes only; this layer never accepts or returns a filesystem path.
func (e *Exporter) StreamHistoricalCSV(ctx context.Context, output io.Writer, request ExportRequest) (int64, error) {
	if output == nil {
		return 0, fmt.Errorf("%w: output is required", ErrInvalidInput)
	}
	address, token, minUSD, from, to, err := validateCommon(request.ChainID, request.Address, request.From, request.To, request.TokenAddress, request.MinUSD)
	if err != nil {
		return 0, err
	}
	if e == nil || e.client == nil {
		return 0, ErrQueryFailed
	}
	limit := request.Limit
	if limit == 0 {
		limit = 100_000
	}
	if limit > maxExportRows {
		return 0, fmt.Errorf("%w: limit exceeds %d", ErrInvalidInput, maxExportRows)
	}

	var query string
	var columns []string
	switch request.Dataset {
	case ExportHistoricalActivity:
		columns = historicalActivityColumns
		conditions := []string{
			fmt.Sprintf("a.chain_id=%d", request.ChainID), "a.address='" + address + "'",
			"a.block_time>=" + quoteTime(from), "a.block_time<" + quoteTime(to),
		}
		if token != "" {
			conditions = append(conditions, "a.token_address='"+token+"'")
		}
		if strings.TrimSpace(request.MinUSD) != "" {
			conditions = append(conditions, "a.usd_value IS NOT NULL", "a.usd_value>=toDecimal256('"+minUSD+"',18)")
		}
		query = fmt.Sprintf(`SELECT a.chain_id,a.address,a.counterparty_address,a.direction,a.activity_type,a.block_number,a.block_time,
a.tx_hash,a.event_index,a.token_address,a.token_symbol,toString(a.amount) AS amount,a.price_usd AS historical_price,
a.usd_value AS historical_usd,a.price_time,a.price_source,a.price_confidence,ifNull(toString(a.counterparty_entity_id),'') AS entity_id,
ifNull(e.entity_name,'') AS entity_name,ifNull(e.entity_type,a.counterparty_entity_type) AS entity_type,a.counterparty_role AS entity_role
FROM onchain.address_activity AS a FINAL LEFT JOIN onchain.entity_registry AS e FINAL ON a.counterparty_entity_id=e.entity_id
WHERE %s ORDER BY a.block_time,a.block_number,a.tx_hash,a.event_index LIMIT %d`, strings.Join(conditions, " AND "), limit)
	case ExportHistoricalEdges:
		columns = historicalEdgeColumns
		tokenClause := ""
		if token != "" {
			tokenClause = " AND token_address='" + token + "'"
		}
		query = fmt.Sprintf(`WITH grouped AS
(
 SELECT chain_id,if(direction='OUT',address,counterparty_address) AS from_address,
  if(direction='OUT',counterparty_address,address) AS to_address,token_address,
  argMax(token_symbol,tuple(block_time,block_number,tx_hash,event_index)) AS token_symbol,toString(sum(amount)) AS token_amount,
  sum(usd_value) AS historical_usd,argMax(price_usd,tuple(block_time,block_number,tx_hash,event_index)) AS historical_price,
  argMax(price_time,tuple(block_time,block_number,tx_hash,event_index)) AS price_time,
  arrayStringConcat(arraySort(groupUniqArray(if(price_source='','MISSING',price_source))),'|') AS price_source,
  arrayStringConcat(arraySort(groupUniqArray(if(price_confidence='','MISSING',price_confidence))),'|') AS price_confidence,
  uniqExact(tx_hash) AS transaction_count,count() AS event_count,min(block_time) AS first_time,max(block_time) AS last_time,
  argMax(counterparty_entity_id,tuple(block_time,block_number,tx_hash,event_index)) AS entity_id,
  argMax(counterparty_entity_type,tuple(block_time,block_number,tx_hash,event_index)) AS entity_type,
  argMax(counterparty_role,tuple(block_time,block_number,tx_hash,event_index)) AS entity_role
 FROM onchain.address_activity FINAL WHERE chain_id=%d AND address='%s' AND counterparty_address!=''
  AND block_time>=%s AND block_time<%s AND usd_value IS NOT NULL%s
 GROUP BY chain_id,from_address,to_address,token_address
), filtered AS
(
 SELECT * FROM grouped WHERE (from_address,to_address) IN
  (SELECT from_address,to_address FROM grouped GROUP BY from_address,to_address HAVING sum(historical_usd)>=toDecimal256('%s',18))
)
SELECT f.chain_id,f.from_address,f.to_address,f.transaction_count,f.event_count,f.first_time,f.last_time,f.token_address,
f.token_symbol,f.token_amount,f.historical_usd,f.historical_price,f.price_time,f.price_source,f.price_confidence,
ifNull(toString(f.entity_id),'') AS entity_id,ifNull(e.entity_name,'') AS entity_name,
ifNull(e.entity_type,f.entity_type) AS entity_type,f.entity_role
FROM filtered AS f LEFT JOIN onchain.entity_registry AS e FINAL ON f.entity_id=e.entity_id
ORDER BY f.first_time,f.from_address,f.to_address,f.token_address LIMIT %d`, request.ChainID, address, quoteTime(from), quoteTime(to), tokenClause, minUSD, limit)
	default:
		return 0, fmt.Errorf("%w: unsupported export dataset", ErrInvalidInput)
	}
	stream, err := e.client.QueryCSV(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrQueryFailed, err)
	}
	defer stream.Close()
	return copyCSVWithHeader(output, stream, columns)
}

func copyCSVWithHeader(output io.Writer, input io.Reader, columns []string) (int64, error) {
	writer := csv.NewWriter(output)
	if err := writer.Write(columns); err != nil {
		return 0, fmt.Errorf("write CSV header: %w", err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return 0, fmt.Errorf("flush CSV header: %w", err)
	}
	written, err := io.Copy(output, input)
	if err != nil {
		return written, fmt.Errorf("stream ClickHouse CSV: %w", err)
	}
	return written, nil
}

// StreamAlgorithmCSV serializes typed derived results through a separate fixed
// whitelist. This prevents an investigation payload from selecting arbitrary
// columns or leaking an internal spool path.
func StreamAlgorithmCSV(output io.Writer, records []AlgorithmRecord) (int64, error) {
	if output == nil {
		return 0, fmt.Errorf("%w: output is required", ErrInvalidInput)
	}
	if uint64(len(records)) > maxExportRows {
		return 0, fmt.Errorf("%w: too many algorithm rows", ErrInvalidInput)
	}
	counter := &countingWriter{writer: output}
	writer := csv.NewWriter(counter)
	if err := writer.Write(algorithmColumns); err != nil {
		return counter.written, err
	}
	for _, record := range records {
		if !metricPattern.MatchString(record.Metric) {
			return counter.written, fmt.Errorf("%w: invalid metric", ErrInvalidInput)
		}
		for _, version := range []string{record.AlgorithmVersion, record.PriceVersion} {
			if !versionPattern.MatchString(version) {
				return counter.written, fmt.Errorf("%w: invalid version", ErrInvalidInput)
			}
		}
		if record.TokenFilter != "" && !addressPattern.MatchString(strings.ToLower(record.TokenFilter)) {
			return counter.written, fmt.Errorf("%w: invalid token filter", ErrInvalidInput)
		}
		row := []string{record.Metric, record.Window, record.ValueUSD, record.Ratio, record.Coverage, record.Confidence,
			record.AlgorithmVersion, record.PriceVersion, record.From.UTC().Format(timeLayout), record.To.UTC().Format(timeLayout),
			strings.ToLower(record.TokenFilter)}
		if err := writer.Write(row); err != nil {
			return counter.written, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return counter.written, err
	}
	return counter.written, nil
}

const timeLayout = "2006-01-02T15:04:05.999999999Z07:00"

type countingWriter struct {
	writer  io.Writer
	written int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.written += int64(n)
	return n, err
}
