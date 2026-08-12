// Package datawarehouse maps certified SmartDownload Parquet assets into the
// onchain ClickHouse schema. ReplacingMergeTree keys make retries logically
// idempotent; callers must not infer physical row uniqueness before merges.
package datawarehouse

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/etl/backend/internal/eventdecoder"
	"github.com/etl/backend/internal/logger"
	"github.com/etl/backend/internal/smartdownload"
)

// ClickHouseSink is the minimal streaming contract required from the concrete
// ClickHouse client. columns is an explicit INSERT column list.
type ClickHouseSink interface {
	InsertCSV(ctx context.Context, table string, columns []string, body io.Reader) error
}

type clickHouseQuery interface {
	QueryJSON(ctx context.Context, query string) ([]map[string]any, error)
}

// DuckDBQuery is satisfied by analysis/duckdb.Engine.
type DuckDBQuery interface {
	ExecSQL(ctx context.Context, sqlText string) ([]byte, error)
}

type AnalyticsRefresher interface {
	RefreshAddressAnalytics(ctx context.Context, chainID uint32, address string) error
}

type Writer struct {
	sink                   ClickHouseSink
	duckdb                 DuckDBQuery
	insertRows             atomic.Uint64
	insertBatches          atomic.Uint64
	insertLatencyNS        atomic.Int64
	writerErrors           atomic.Uint64
	analyticsRefreshErrors atomic.Uint64
	latencyMu              sync.Mutex
	latencyWindowMS        []int64
	refresher              AnalyticsRefresher
	eventDecoder           *eventdecoder.Decoder
}

type Metrics struct {
	InsertRows             uint64 `json:"clickhouse_insert_rows_total"`
	InsertBatches          uint64 `json:"clickhouse_insert_batches_total"`
	InsertLatencyMS        int64  `json:"clickhouse_insert_latency_ms"`
	InsertP95MS            int64  `json:"clickhouse_insert_p95_ms"`
	WriterErrors           uint64 `json:"clickhouse_writer_errors_total"`
	AnalyticsRefreshErrors uint64 `json:"analytics_refresh_errors_total"`
}

func (w *Writer) Metrics() Metrics {
	if w == nil {
		return Metrics{}
	}
	w.latencyMu.Lock()
	window := append([]int64(nil), w.latencyWindowMS...)
	w.latencyMu.Unlock()
	sort.Slice(window, func(i, j int) bool { return window[i] < window[j] })
	p95 := int64(0)
	if len(window) > 0 {
		p95 = window[(len(window)*95+99)/100-1]
	}
	return Metrics{InsertRows: w.insertRows.Load(), InsertBatches: w.insertBatches.Load(),
		InsertLatencyMS: time.Duration(w.insertLatencyNS.Load()).Milliseconds(), InsertP95MS: p95,
		WriterErrors: w.writerErrors.Load(), AnalyticsRefreshErrors: w.analyticsRefreshErrors.Load()}
}

var (
	safeDatasetJobID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	decimalPattern   = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)
	methodIDPattern  = regexp.MustCompile(`^0x[0-9a-f]{8}$`)
)

func NewWriter(sink ClickHouseSink, engine DuckDBQuery) *Writer {
	return &Writer{sink: sink, duckdb: engine, eventDecoder: eventdecoder.New(nil)}
}

func (w *Writer) SetEventRegistry(registry eventdecoder.Registry) {
	if w != nil {
		w.eventDecoder = eventdecoder.New(registry)
	}
}

func (w *Writer) SetAnalyticsRefresher(refresher AnalyticsRefresher) {
	if w != nil {
		w.refresher = refresher
	}
}

var transactionColumns = []string{
	"chain_id", "block_number", "block_hash", "block_time", "transaction_index", "tx_hash",
	"from_address", "to_address", "nonce", "value_raw", "value_decimal", "native_symbol", "input",
	"method_id", "method_name", "method_confidence", "tx_type", "gas_limit", "gas_price", "max_fee_per_gas",
	"max_priority_fee_per_gas", "effective_gas_price", "gas_used", "transaction_fee_native", "transaction_fee_usd",
	"status", "raw_status", "status_source", "is_contract_creation", "created_contract_address", "error_message",
	"source_provider", "parser_version", "normalizer_version", "schema_version", "ingest_job_id", "source_range_id",
}

var tokenTransferColumns = []string{
	"chain_id", "block_number", "block_time", "tx_hash", "log_index",
	"token_address", "token_name", "token_symbol", "token_decimals", "token_standard", "from_address", "to_address", "raw_value",
	"value_decimal", "usd_price", "usd_value", "price_time", "price_source", "price_confidence",
	"source_provider", "parser_version", "normalizer_version", "schema_version", "ingest_job_id", "source_range_id",
}

var internalTransactionColumns = []string{
	"chain_id", "block_number", "block_time", "tx_hash", "trace_address", "trace_index",
	"call_type", "from_address", "to_address", "value_raw", "value_decimal", "gas", "gas_used", "input", "output",
	"success", "error", "depth", "parent_trace_index", "source_provider", "parser_version", "schema_version", "ingest_job_id", "source_range_id",
}

var contractCreationColumns = []string{
	"chain_id", "block_number", "block_time", "tx_hash", "creator_address",
	"contract_address", "creation_type", "factory_address", "init_code_hash", "runtime_code_hash", "token_detected",
	"token_standard", "contract_name", "is_proxy", "proxy_type", "implementation_address", "source_verified",
	"source_provider", "parser_version", "schema_version", "ingest_job_id", "source_range_id",
}

var contractIdentityColumns = []string{
	"chain_id", "contract_address", "creator_address", "factory_address", "creation_tx_hash", "creation_block", "creation_time",
	"bytecode_hash", "runtime_bytecode_hash", "contract_name", "is_verified", "is_proxy", "proxy_type", "implementation_address",
	"abi_source", "source_provider", "parser_version", "schema_version", "ingest_job_id", "source_range_id",
}

var parsedEventColumns = []string{
	"chain_id", "block_number", "block_time", "tx_hash", "log_index", "contract_address", "topic0",
	"event_name", "event_signature", "decoded_fields", "decoder_source", "decoder_confidence", "parser_version",
	"schema_version", "source_provider", "normalizer_version", "ingest_job_id", "source_range_id",
}

var activityColumns = []string{
	"chain_id", "address", "counterparty_address", "direction", "activity_type",
	"block_number", "block_time", "tx_hash", "event_index", "token_address",
	"token_symbol", "amount", "usd_value", "method_id", "method_name", "method_confidence", "status",
	"counterparty_entity_type", "counterparty_label", "counterparty_entity_id", "counterparty_role",
	"price_usd", "price_time", "price_source", "price_confidence", "source_provider",
	"parser_version", "normalizer_version", "schema_version", "ingest_job_id", "source_range_id",
}

// WriteIndexed exports the merged Parquet beside the E-drive asset, maps rows,
// writes the source table followed by address_activity, and reconciles every
// source row as inserted or rejected.
func (w *Writer) WriteIndexed(ctx context.Context, req smartdownload.IndexedWriteRequest) (result smartdownload.IndexedWriteResult, retErr error) {
	started := time.Now()
	defer func() {
		if w == nil {
			return
		}
		elapsed := time.Since(started)
		w.insertLatencyNS.Store(int64(elapsed))
		w.latencyMu.Lock()
		w.latencyWindowMS = append(w.latencyWindowMS, elapsed.Milliseconds())
		if len(w.latencyWindowMS) > 128 {
			w.latencyWindowMS = append([]int64(nil), w.latencyWindowMS[len(w.latencyWindowMS)-128:]...)
		}
		w.latencyMu.Unlock()
		if retErr != nil {
			w.writerErrors.Add(1)
		}
	}()
	if w == nil || w.sink == nil || w.duckdb == nil {
		return result, fmt.Errorf("data warehouse writer is not configured")
	}
	if req.MergedParquet == "" {
		return result, fmt.Errorf("merged parquet is required")
	}
	if !filepath.IsAbs(req.MergedParquet) || !strings.EqualFold(filepath.VolumeName(req.MergedParquet), "E:") {
		return result, fmt.Errorf("warehouse staging must remain on E drive: %s", req.MergedParquet)
	}
	if !safeDatasetJobID.MatchString(req.DatasetJobID) {
		return result, fmt.Errorf("invalid dataset job id")
	}
	table, columns, ok := datasetTarget(req.Dataset)
	if !ok {
		return result, fmt.Errorf("dataset %s is not indexed into onchain warehouse", req.Dataset)
	}

	stageDir := filepath.Join(filepath.Dir(req.MergedParquet), ".clickhouse-stage", req.DatasetJobID)
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return result, err
	}
	defer os.RemoveAll(stageDir)
	canonicalPath := filepath.Join(stageDir, "canonical.csv")
	if err := exportParquetCSV(ctx, w.duckdb, req.MergedParquet, canonicalPath, req.FromBlock, req.ToBlock); err != nil {
		return result, err
	}
	tablePath := filepath.Join(stageDir, table+".csv")
	activityPath := filepath.Join(stageDir, "address_activity.csv")
	contractPath := filepath.Join(stageDir, "contracts.csv")
	result, err := mapCanonicalCSV(ctx, w.eventDecoder, req, canonicalPath, tablePath, activityPath, contractPath)
	if err != nil {
		return result, err
	}
	if result.InputRows != result.InsertedRows+result.RejectedRows {
		return result, fmt.Errorf("writer reconciliation failed: input=%d success=%d reject=%d",
			result.InputRows, result.InsertedRows, result.RejectedRows)
	}
	if result.InsertedRows > 0 {
		if err := insertFile(ctx, w.sink, table, columns, tablePath); err != nil {
			return result, fmt.Errorf("insert %s: %w", table, err)
		}
		w.insertBatches.Add(1)
	}
	if result.ActivityRows > 0 {
		if err := insertFile(ctx, w.sink, "address_activity", activityColumns, activityPath); err != nil {
			return result, fmt.Errorf("insert address_activity: %w", err)
		}
		w.insertBatches.Add(1)
	}
	if req.Dataset == smartdownload.DatasetContractCreations && result.InsertedRows > 0 {
		if err := insertFile(ctx, w.sink, "contracts", contractIdentityColumns, contractPath); err != nil {
			return result, fmt.Errorf("insert contracts: %w", err)
		}
		w.insertBatches.Add(1)
	}
	if verifier, ok := w.sink.(clickHouseQuery); ok && result.InsertedRows > 0 {
		rows, err := verifyLogicalRows(ctx, verifier, table, req)
		if err != nil {
			return result, fmt.Errorf("verify %s logical rows: %w", table, err)
		}
		result.VerifiedRows = rows
		if rows != result.InsertedRows {
			return result, fmt.Errorf("verify %s logical rows: db=%d writer_success=%d", table, rows, result.InsertedRows)
		}
		activityRows, err := verifyIngestRows(ctx, verifier, "address_activity", req.DatasetJobID)
		if err != nil {
			return result, fmt.Errorf("verify address_activity logical rows: %w", err)
		}
		if activityRows != result.ActivityRows {
			return result, fmt.Errorf("verify address_activity logical rows: db=%d writer_activity=%d", activityRows, result.ActivityRows)
		}
		if req.Dataset == smartdownload.DatasetContractCreations {
			contractRows, err := verifyIngestRows(ctx, verifier, "contracts", req.DatasetJobID)
			if err != nil || contractRows != result.InsertedRows {
				return result, fmt.Errorf("verify contracts logical rows: db=%d writer_success=%d: %w", contractRows, result.InsertedRows, err)
			}
		}
	}
	w.insertRows.Add(uint64(result.InsertedRows + result.ActivityRows))
	if w.refresher != nil && req.Address != "" {
		if err := w.refresher.RefreshAddressAnalytics(ctx, uint32(req.ChainID), req.Address); err != nil {
			// Canonical rows have already been inserted and reconciled at this
			// point. A derived Explorer analytics refresh failure must not revoke
			// the certified dataset or cause a duplicate DB-only retry.
			w.analyticsRefreshErrors.Add(1)
			logger.Log.Warn().Err(err).Str("dataset_job", req.DatasetJobID).
				Str("dataset", req.Dataset).Str("address", req.Address).
				Msg("datawarehouse_address_analytics_refresh_failed")
		}
	}
	return result, nil
}

func verifyLogicalRows(ctx context.Context, query clickHouseQuery, table string, req smartdownload.IndexedWriteRequest) (int64, error) {
	return verifyIngestRows(ctx, query, table, req.DatasetJobID)
}

func verifyIngestRows(ctx context.Context, query clickHouseQuery, table, jobID string) (int64, error) {
	jobID = strings.ReplaceAll(jobID, "'", "''")
	sql := fmt.Sprintf("SELECT count() AS n FROM %s FINAL WHERE ingest_job_id='%s'", table, jobID)
	rows, err := query.QueryJSON(ctx, sql)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	switch n := rows[0]["n"].(type) {
	case float64:
		return int64(n), nil
	case int64:
		return n, nil
	case string:
		return strconv.ParseInt(n, 10, 64)
	default:
		return strconv.ParseInt(fmt.Sprint(n), 10, 64)
	}
}

func datasetTarget(dataset string) (string, []string, bool) {
	switch dataset {
	case smartdownload.DatasetTransactions:
		return "chain_transactions", transactionColumns, true
	case smartdownload.DatasetTokenTransfers:
		return "token_transfers", tokenTransferColumns, true
	case smartdownload.DatasetInternalTransactions:
		return "internal_transactions", internalTransactionColumns, true
	case smartdownload.DatasetContractCreations:
		return "contract_creations", contractCreationColumns, true
	case smartdownload.DatasetLogs:
		return "parsed_events", parsedEventColumns, true
	default:
		return "", nil, false
	}
}

func exportParquetCSV(ctx context.Context, engine DuckDBQuery, parquetPath, csvPath string, fromBlock, toBlock uint64) error {
	q := func(path string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(path), "'", "''") + "'" }
	filter := ""
	if toBlock >= fromBlock && (fromBlock != 0 || toBlock != 0) {
		filter = fmt.Sprintf(" WHERE block_number BETWEEN %d AND %d", fromBlock, toBlock)
	}
	sql := fmt.Sprintf("COPY (SELECT * FROM read_parquet(%s)%s) TO %s (HEADER, DELIMITER ',', QUOTE '\"', ESCAPE '\"')",
		q(parquetPath), filter, q(csvPath))
	if out, err := engine.ExecSQL(ctx, sql); err != nil {
		return fmt.Errorf("export certified parquet to E-drive CSV: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func insertFile(ctx context.Context, sink ClickHouseSink, table string, columns []string, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return sink.InsertCSV(ctx, table, columns, f)
}

func mapCanonicalCSV(ctx context.Context, decoder *eventdecoder.Decoder, req smartdownload.IndexedWriteRequest, sourcePath, tablePath, activityPath, contractPath string) (smartdownload.IndexedWriteResult, error) {
	var result smartdownload.IndexedWriteResult
	in, err := os.Open(sourcePath)
	if err != nil {
		return result, err
	}
	defer in.Close()
	out, err := os.Create(tablePath)
	if err != nil {
		return result, err
	}
	defer out.Close()
	activities, err := os.Create(activityPath)
	if err != nil {
		return result, err
	}
	defer activities.Close()
	contracts, err := os.Create(contractPath)
	if err != nil {
		return result, err
	}
	defer contracts.Close()

	r := csv.NewReader(in)
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return result, fmt.Errorf("read canonical header: %w", err)
	}
	index := make(map[string]int, len(header))
	for i, name := range header {
		index[strings.TrimSpace(name)] = i
	}
	tw, aw, cw := csv.NewWriter(out), csv.NewWriter(activities), csv.NewWriter(contracts)
	for {
		record, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		result.InputRows++
		if readErr != nil {
			result.RejectedRows++
			continue
		}
		mapped, activity, mapErr := mapRow(ctx, decoder, req, index, record)
		if mapErr != nil {
			result.RejectedRows++
			continue
		}
		if err := tw.Write(mapped); err != nil {
			return result, err
		}
		for _, row := range activity {
			if err := aw.Write(row); err != nil {
				return result, err
			}
			result.ActivityRows++
		}
		if req.Dataset == smartdownload.DatasetContractCreations {
			if err := cw.Write(contractIdentityRow(mapped)); err != nil {
				return result, err
			}
		}
		result.InsertedRows++
	}
	tw.Flush()
	aw.Flush()
	cw.Flush()
	if err := tw.Error(); err != nil {
		return result, err
	}
	if err := aw.Error(); err != nil {
		return result, err
	}
	if err := cw.Error(); err != nil {
		return result, err
	}
	return result, nil
}

func contractIdentityRow(created []string) []string {
	// contractCreationColumns order is fixed above.
	return []string{
		created[0], created[5], created[4], created[7], created[3], created[1], created[2], created[8], created[9],
		created[12], created[16], created[13], created[14], created[15], "", created[17], created[18], created[19], created[20], created[21],
	}
}

func mapRow(ctx context.Context, decoder *eventdecoder.Decoder, req smartdownload.IndexedWriteRequest, idx map[string]int, row []string) ([]string, [][]string, error) {
	get := func(name string) string {
		if i, ok := idx[name]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	chainID := get("chain_id")
	if chainID == "" {
		chainID = strconv.FormatInt(req.ChainID, 10)
	}
	block := get("block_number")
	txHash := strings.ToLower(get("transaction_hash"))
	blockTime, err := clickHouseTime(get("block_time"))
	if err != nil || chainID == "" || block == "" || txHash == "" {
		return nil, nil, fmt.Errorf("missing logical key or invalid block time")
	}
	from, to := strings.ToLower(get("from_address")), strings.ToLower(get("to_address"))
	provider := get("source_provider")
	if provider == "" {
		provider = req.SourceProvider
	}
	value, err := decimalValue(get("value_raw"))
	if err != nil {
		return nil, nil, err
	}
	if candidate := firstNonEmpty(get("value_decimal"), get("amount")); candidate != "" {
		if parsed, parseErr := decimalNumber(candidate); parseErr == nil {
			value = parsed
		}
	}
	rawStatus := get("status")
	status := statusText(rawStatus)
	methodID := canonicalMethodID(get("method_id"), get("input"))
	methodName := get("method_name")
	methodConfidence := strings.ToUpper(get("method_confidence"))
	parserVersion := firstNonEmpty(get("parser_version"), req.ParserVersion, "smartdownload-v1")
	normalizerVersion := firstNonEmpty(get("normalizer_version"), req.NormalizerVersion, "canonical-writer-v2")
	schemaVersion := uintOrZero(get("schema_version"))
	if schemaVersion == "0" {
		if req.SchemaVersion == 0 {
			req.SchemaVersion = 2
		}
		schemaVersion = strconv.FormatUint(uint64(req.SchemaVersion), 10)
	}
	rangeID := fmt.Sprintf("%s:%d-%d", req.DatasetJobID, req.FromBlock, req.ToBlock)
	baseActivity := []string{chainID, "", "", "", activityType(req.Dataset), block, blockTime, txHash, "", "", "", value,
		nullableDecimal(get("usd_value")), methodID, methodName, methodConfidence, status, "", "", "\\N", "",
		nullableDecimal(get("price_usd")), nullableDateTime(get("price_time")), get("price_source"), strings.ToUpper(get("price_confidence")),
		provider, parserVersion, normalizerVersion, schemaVersion, req.DatasetJobID, rangeID}
	var mapped []string
	switch req.Dataset {
	case smartdownload.DatasetTransactions:
		if from == "" && to == "" {
			return nil, nil, fmt.Errorf("transaction has no participant")
		}
		index := uintOrZero(get("transaction_index"))
		createdContract := strings.ToLower(get("created_contract_address"))
		isCreation := boolValue(get("is_contract_creation"))
		if createdContract != "" {
			isCreation = "1"
		}
		feeNative, feeErr := decimalValue(firstNonEmpty(get("transaction_fee_native"), "0"))
		if feeErr != nil {
			return nil, nil, feeErr
		}
		mapped = []string{chainID, block, get("block_hash"), blockTime, index, txHash, from, to, uintOrZero(get("nonce")),
			get("value_raw"), value, get("native_symbol"), get("input"), methodID, methodName, methodConfidence,
			get("tx_type"), uintOrZero(get("gas_limit")), nullableUInt(get("gas_price")), nullableUInt(get("max_fee_per_gas")),
			nullableUInt(get("max_priority_fee_per_gas")), nullableUInt(get("effective_gas_price")), uintOrZero(get("gas_used")),
			feeNative, nullableDecimal(get("transaction_fee_usd")), status, rawStatus, statusSource(status), isCreation,
			createdContract, get("error_message"), provider, parserVersion, normalizerVersion, schemaVersion, req.DatasetJobID, rangeID}
		baseActivity[8] = "tx:" + index
		if strings.TrimSpace(get("input")) != "" && strings.TrimSpace(get("input")) != "0x" && value == "0" {
			baseActivity[4] = "CONTRACT_CALL"
		}
	case smartdownload.DatasetTokenTransfers:
		if from == "" && to == "" {
			return nil, nil, fmt.Errorf("token transfer has no participant")
		}
		logIndex := uintOrZero(get("log_index"))
		token := strings.ToLower(get("token_address"))
		mapped = []string{chainID, block, blockTime, txHash, logIndex, token, get("token_name"), get("token_symbol"),
			uintOrZero(get("token_decimals")), get("token_standard"), from, to, get("value_raw"), value,
			nullableDecimal(get("usd_price")), nullableDecimal(get("usd_value")), nullableDateTime(get("price_time")),
			get("price_source"), strings.ToUpper(get("price_confidence")), provider, parserVersion, normalizerVersion,
			schemaVersion, req.DatasetJobID, rangeID}
		baseActivity[8], baseActivity[9], baseActivity[10] = "log:"+logIndex, token, get("token_symbol")
	case smartdownload.DatasetInternalTransactions:
		if from == "" && to == "" {
			return nil, nil, fmt.Errorf("internal transaction has no participant")
		}
		traceIndex := uintOrZero(get("trace_index"))
		traceAddress := get("trace_address")
		mapped = []string{chainID, block, blockTime, txHash, traceAddress, traceIndex, get("call_type"), from, to,
			get("value_raw"), value, uintOrZero(get("gas")), uintOrZero(get("gas_used")), get("input"), get("output"),
			boolText(rawStatus), firstNonEmpty(get("error"), errorForStatus(rawStatus)), uintOrZero(get("depth")),
			nullableUInt(get("parent_trace_index")), provider, parserVersion, schemaVersion, req.DatasetJobID, rangeID}
		baseActivity[8] = "trace:" + traceAddress
	case smartdownload.DatasetContractCreations:
		creator := strings.ToLower(get("creator_address"))
		contract := strings.ToLower(get("contract_address"))
		if creator == "" || contract == "" {
			return nil, nil, fmt.Errorf("contract creation logical key is incomplete")
		}
		from, to = creator, contract
		mapped = []string{chainID, block, blockTime, txHash, creator, contract,
			firstNonEmpty(strings.ToUpper(get("creation_type")), "CREATE"), strings.ToLower(get("factory_address")),
			get("init_code_hash"), firstNonEmpty(get("runtime_code_hash"), get("runtime_bytecode_hash")), boolValue(get("token_detected")),
			get("token_standard"), get("contract_name"), boolValue(get("is_proxy")), get("proxy_type"),
			strings.ToLower(get("implementation_address")), boolValue(firstNonEmpty(get("source_verified"), get("is_verified"))),
			provider, parserVersion, schemaVersion, req.DatasetJobID, rangeID}
		baseActivity[8] = "contract_creation"
	case smartdownload.DatasetLogs:
		contract := strings.ToLower(get("contract_address"))
		logIndex := uintOrZero(get("log_index"))
		var topics []string
		if contract == "" || json.Unmarshal([]byte(get("topics")), &topics) != nil || len(topics) == 0 {
			return nil, nil, fmt.Errorf("event logical key or topics are incomplete")
		}
		if decoder == nil {
			decoder = eventdecoder.New(nil)
		}
		decoded, err := decoder.Decode(ctx, eventdecoder.Log{
			ChainID: uint64(req.ChainID), Contract: contract, TransactionHash: txHash,
			LogIndex: uint32Value(logIndex), Topics: topics, Data: get("data"),
		})
		if err != nil {
			return nil, nil, err
		}
		mapped = []string{chainID, block, blockTime, txHash, logIndex, contract, strings.ToLower(topics[0]),
			decoded.EventName, decoded.EventSignature, decoded.DecodedFieldsJSON(), decoded.DecoderSource, decoded.DecoderConfidence,
			parserVersion, schemaVersion, provider, normalizerVersion, req.DatasetJobID, rangeID}
		return mapped, nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported dataset")
	}
	return mapped, activityRows(baseActivity, from, to), nil
}

func activityType(dataset string) string {
	switch dataset {
	case smartdownload.DatasetTransactions:
		return "NATIVE_TRANSFER"
	case smartdownload.DatasetTokenTransfers:
		return "TOKEN_TRANSFER"
	case smartdownload.DatasetInternalTransactions:
		return "INTERNAL_TRANSFER"
	case smartdownload.DatasetContractCreations:
		return "CONTRACT_CREATE"
	default:
		return strings.ToUpper(dataset)
	}
}

func activityRows(base []string, from, to string) [][]string {
	clone := func() []string { return append([]string(nil), base...) }
	if from != "" && from == to {
		row := clone()
		row[1], row[2], row[3] = from, to, "SELF"
		return [][]string{row}
	}
	var rows [][]string
	if from != "" {
		row := clone()
		row[1], row[2], row[3] = from, to, "OUT"
		rows = append(rows, row)
	}
	if to != "" {
		row := clone()
		row[1], row[2], row[3] = to, from, "IN"
		rows = append(rows, row)
	}
	return rows
}

func clickHouseTime(raw string) (string, error) {
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n > 10_000_000_000 {
			n /= 1000
		}
		return time.Unix(n, 0).UTC().Format("2006-01-02 15:04:05.000"), nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC().Format("2006-01-02 15:04:05.000"), nil
		}
	}
	return "", fmt.Errorf("invalid block_time %q", raw)
}

func decimalValue(raw string) (string, error) {
	if raw == "" {
		return "0", nil
	}
	if strings.HasPrefix(raw, "0x") || strings.HasPrefix(raw, "0X") {
		if n, ok := new(big.Int).SetString(raw[2:], 16); ok {
			return n.String(), nil
		}
		return "", fmt.Errorf("invalid value_raw %q", raw)
	}
	if _, ok := new(big.Rat).SetString(raw); ok {
		return raw, nil
	}
	return "", fmt.Errorf("invalid value_raw %q", raw)
}

func decimalNumber(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !decimalPattern.MatchString(raw) {
		return "", fmt.Errorf("invalid decimal value %q", raw)
	}
	return raw, nil
}

func nullableDecimal(raw string) string {
	if value, err := decimalNumber(raw); err == nil {
		return value
	}
	return "\\N"
}

func nullableDateTime(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "\\N"
	}
	if value, err := clickHouseTime(raw); err == nil {
		return value
	}
	return "\\N"
}

func canonicalMethodID(raw, input string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if methodIDPattern.MatchString(raw) {
		return raw
	}
	input = strings.ToLower(strings.TrimSpace(input))
	if len(input) >= 10 && methodIDPattern.MatchString(input[:10]) {
		return input[:10]
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func boolValue(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return "1"
	default:
		return "0"
	}
}

func uintOrZero(raw string) string {
	if _, err := strconv.ParseUint(raw, 10, 64); err == nil {
		return raw
	}
	return "0"
}

func uint32Value(raw string) uint32 {
	value, _ := strconv.ParseUint(raw, 10, 32)
	return uint32(value)
}

func nullableUInt(raw string) string {
	if raw == "" {
		return "\\N"
	}
	if _, ok := new(big.Int).SetString(raw, 10); ok {
		return raw
	}
	return "\\N"
}

func statusText(raw string) string {
	switch strings.ToLower(raw) {
	case "1", "true", "success", "ok":
		return "SUCCESS"
	case "0", "false", "failed", "error":
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

func statusSource(status string) string {
	if status == "SUCCESS" || status == "FAILED" {
		return "RECEIPT"
	}
	return "MISSING"
}

func boolText(raw string) string {
	if statusText(raw) == "SUCCESS" {
		return "1"
	}
	return "0"
}

func errorForStatus(raw string) string {
	if statusText(raw) == "FAILED" {
		return "execution failed"
	}
	return ""
}
