package canonicalapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const (
	defaultTxCollectionLimit = 500
	maxTxCollectionLimit     = 500
	txCursorTokenTransfers   = "token_transfers"
	txCursorInternal         = "internal_transactions"
	txCursorRawLogs          = "raw_logs"
)

var (
	traceAddressRE = regexp.MustCompile(`^(?:[0-9]+(?:[,_][0-9]+)*)?$`)
	topicRE        = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
	hexDataRE      = regexp.MustCompile(`^0x(?:[0-9a-f]{2})*$`)
)

type txCursor struct {
	Version   uint8  `json:"v"`
	Kind      string `json:"kind"`
	ChainID   uint32 `json:"chain_id"`
	TxHash    string `json:"tx_hash"`
	Primary   uint32 `json:"primary"`
	Secondary string `json:"secondary,omitempty"`
}

type normalizedTxCollectionQuery struct {
	ChainID uint32
	TxHash  string
	Limit   int
	Cursor  *txCursor
}

func normalizeTxCollectionQuery(input TxCollectionQuery, kind string) (normalizedTxCollectionQuery, error) {
	txHash, err := validateTxScope(input.ChainID, input.TxHash)
	if err != nil {
		return normalizedTxCollectionQuery{}, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultTxCollectionLimit
	}
	if limit < 1 || limit > maxTxCollectionLimit {
		return normalizedTxCollectionQuery{}, fmt.Errorf("%w: limit out of range", ErrInvalidInput)
	}
	out := normalizedTxCollectionQuery{ChainID: input.ChainID, TxHash: txHash, Limit: limit}
	if strings.TrimSpace(input.Cursor) == "" {
		return out, nil
	}
	cursor, err := decodeTxCursor(input.Cursor)
	if err != nil || cursor.Version != 1 || cursor.Kind != kind || cursor.ChainID != input.ChainID || cursor.TxHash != txHash {
		return normalizedTxCollectionQuery{}, fmt.Errorf("%w: cursor scope or values do not match", ErrInvalidInput)
	}
	switch kind {
	case txCursorTokenTransfers:
		if _, err := strconv.ParseUint(cursor.Secondary, 10, 32); err != nil {
			return normalizedTxCollectionQuery{}, fmt.Errorf("%w: invalid token cursor", ErrInvalidInput)
		}
	case txCursorInternal:
		if !traceAddressRE.MatchString(cursor.Secondary) {
			return normalizedTxCollectionQuery{}, fmt.Errorf("%w: invalid internal cursor", ErrInvalidInput)
		}
	case txCursorRawLogs:
		if cursor.Secondary != "" {
			return normalizedTxCollectionQuery{}, fmt.Errorf("%w: invalid log cursor", ErrInvalidInput)
		}
	default:
		return normalizedTxCollectionQuery{}, fmt.Errorf("%w: invalid cursor kind", ErrInvalidInput)
	}
	out.Cursor = &cursor
	return out, nil
}

func encodeTxCursor(cursor txCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeTxCursor(raw string) (txCursor, error) {
	if len(raw) == 0 || len(raw) > 512 {
		return txCursor{}, fmt.Errorf("%w: invalid cursor", ErrInvalidInput)
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return txCursor{}, fmt.Errorf("%w: invalid cursor", ErrInvalidInput)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cursor txCursor
	if err := decoder.Decode(&cursor); err != nil {
		return txCursor{}, fmt.Errorf("%w: invalid cursor", ErrInvalidInput)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return txCursor{}, fmt.Errorf("%w: invalid cursor", ErrInvalidInput)
	}
	return cursor, nil
}

func (r *Repository) ListTokenTransfersByTx(ctx context.Context, input TxCollectionQuery) (TokenTransferPage, error) {
	q, err := normalizeTxCollectionQuery(input, txCursorTokenTransfers)
	if err != nil {
		return TokenTransferPage{}, err
	}
	if r == nil || r.client == nil {
		return TokenTransferPage{}, ErrQueryFailed
	}
	cursorWhere := ""
	if q.Cursor != nil {
		batch, _ := strconv.ParseUint(q.Cursor.Secondary, 10, 32)
		cursorWhere = fmt.Sprintf(" AND (x.log_index,x.batch_index)>(%d,%d)", q.Cursor.Primary, batch)
	}
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT
(SELECT count() FROM onchain.token_transfers FINAL WHERE chain_id=%d AND tx_hash='%s') AS total_count,
x.log_index AS log_index,x.batch_index AS batch_index,x.from_address AS from_address,x.to_address AS to_address,
x.token_address AS token_address,x.raw_value AS raw_value,toString(x.value_decimal) AS value_decimal,
x.token_standard AS token_standard,x.source_provider AS source_provider,x.ingest_job_id AS ingest_job_id,
x.source_range_id AS source_range_id,x.parser_version AS parser_version,x.normalizer_version AS normalizer_version,
x.schema_version AS schema_version,x.ingested_at AS ingested_at,
x.token_name AS fact_token_name,x.token_symbol AS fact_token_symbol,x.token_decimals AS fact_token_decimals,
t.name AS token_name,t.symbol AS token_symbol,t.decimals AS token_decimals,t.token_standard AS registry_standard,
t.logo_uri,t.logo_hash,t.logo_source,t.is_verified,t.is_spam,t.official_website,t.metadata_source,t.metadata_confidence,t.metadata_updated_at,
toString(coalesce(x.usd_value,CAST(x.value_decimal*coalesce(x.usd_price,if(p.token_address!='',toNullable(p.price_usd),CAST(NULL AS Nullable(Decimal(38,18))))) AS Nullable(Decimal(38,18))))) AS usd_value,
toString(coalesce(x.usd_price,if(p.token_address!='',toNullable(p.price_usd),CAST(NULL AS Nullable(Decimal(38,18)))))) AS price_usd,
coalesce(x.price_time,if(p.token_address!='',toNullable(p.timestamp_bucket),CAST(NULL AS Nullable(DateTime64(3,'UTC'))))) AS price_time,
if(x.price_source!='',x.price_source,if(p.token_address!='',p.source,'')) AS price_source,
if(x.price_confidence!='',x.price_confidence,if(p.token_address!='',p.confidence,'')) AS price_confidence
FROM onchain.token_transfers AS x FINAL
LEFT JOIN onchain.token_metadata_registry AS t FINAL ON x.chain_id=t.chain_id AND x.token_address=t.contract_address
ASOF LEFT JOIN (SELECT * FROM onchain.token_prices FINAL ORDER BY chain_id,token_address,timestamp_bucket) AS p
ON x.chain_id=p.chain_id AND x.token_address=p.token_address AND x.block_time>=p.timestamp_bucket
WHERE x.chain_id=%d AND x.tx_hash='%s'%s ORDER BY x.log_index ASC,x.batch_index ASC LIMIT %d`,
		q.ChainID, q.TxHash, q.ChainID, q.TxHash, cursorWhere, q.Limit+1))
	if err != nil {
		return TokenTransferPage{}, err
	}
	page := TokenTransferPage{ChainID: q.ChainID, TransactionHash: q.TxHash, Items: []TxTokenTransfer{}}
	if len(rows) > 0 {
		page.TotalCount, err = uint64Value(rows[0], "total_count")
		if err != nil {
			return TokenTransferPage{}, ErrInvalidData
		}
	}
	hasMore := len(rows) > q.Limit
	if hasMore {
		rows = rows[:q.Limit]
	}
	for _, row := range rows {
		item, decodeErr := decodeTokenTransfer(q.ChainID, row)
		if decodeErr != nil {
			return TokenTransferPage{}, fmt.Errorf("decode token transfer: %w", decodeErr)
		}
		page.Items = append(page.Items, TxTokenTransfer{
			LogIndex: item.LogIndex, BatchIndex: item.BatchIndex, From: item.From, To: item.To,
			Token: item.Token, Amount: item.Amount, USD: item.USD, Standard: item.Standard, Provenance: item.Provenance,
		})
	}
	if err := r.labelTokenTransfers(ctx, q.ChainID, page.Items); err != nil {
		return TokenTransferPage{}, err
	}
	page.Count = len(page.Items)
	if hasMore {
		last := page.Items[len(page.Items)-1]
		page.NextCursor, err = encodeTxCursor(txCursor{Version: 1, Kind: txCursorTokenTransfers, ChainID: q.ChainID, TxHash: q.TxHash, Primary: last.LogIndex, Secondary: strconv.FormatUint(uint64(last.BatchIndex), 10)})
	}
	return page, err
}

func (r *Repository) ListInternalTransactionsByTx(ctx context.Context, input TxCollectionQuery) (InternalTransactionPage, error) {
	q, err := normalizeTxCollectionQuery(input, txCursorInternal)
	if err != nil {
		return InternalTransactionPage{}, err
	}
	if r == nil || r.client == nil {
		return InternalTransactionPage{}, ErrQueryFailed
	}
	cursorWhere := ""
	if q.Cursor != nil {
		cursorWhere = fmt.Sprintf(" AND (trace_index,trace_address)>(%d,'%s')", q.Cursor.Primary, q.Cursor.Secondary)
	}
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT
(SELECT count() FROM onchain.internal_transactions FINAL WHERE chain_id=%d AND tx_hash='%s') AS total_count,
trace_address,trace_index,call_type,depth,parent_trace_index,from_address,to_address,value_raw,toString(value_decimal) AS value_decimal,
input,output,gas,gas_used,success,error,source_provider,ingest_job_id,source_range_id,parser_version,schema_version,ingested_at
FROM onchain.internal_transactions FINAL WHERE chain_id=%d AND tx_hash='%s'%s
ORDER BY trace_index ASC,trace_address ASC LIMIT %d`, q.ChainID, q.TxHash, q.ChainID, q.TxHash, cursorWhere, q.Limit+1))
	if err != nil {
		return InternalTransactionPage{}, err
	}
	page := InternalTransactionPage{ChainID: q.ChainID, TransactionHash: q.TxHash, Items: []CanonicalInternalTransaction{}}
	if len(rows) > 0 {
		page.TotalCount, err = uint64Value(rows[0], "total_count")
		if err != nil {
			return InternalTransactionPage{}, ErrInvalidData
		}
	}
	hasMore := len(rows) > q.Limit
	if hasMore {
		rows = rows[:q.Limit]
	}
	for _, row := range rows {
		item, decodeErr := decodeInternal(row)
		if decodeErr != nil {
			return InternalTransactionPage{}, ErrInvalidData
		}
		page.Items = append(page.Items, item)
	}
	if err := r.labelInternalTransactions(ctx, q.ChainID, page.Items); err != nil {
		return InternalTransactionPage{}, err
	}
	page.Count = len(page.Items)
	if hasMore {
		last := page.Items[len(page.Items)-1]
		page.NextCursor, err = encodeTxCursor(txCursor{Version: 1, Kind: txCursorInternal, ChainID: q.ChainID, TxHash: q.TxHash, Primary: last.TraceIndex, Secondary: last.TraceAddress})
	}
	return page, err
}

func (r *Repository) ListRawLogsByTx(ctx context.Context, input TxCollectionQuery) (RawLogPage, error) {
	q, err := normalizeTxCollectionQuery(input, txCursorRawLogs)
	if err != nil {
		return RawLogPage{}, err
	}
	if r == nil || r.client == nil {
		return RawLogPage{}, ErrQueryFailed
	}
	cursorWhere := ""
	if q.Cursor != nil {
		cursorWhere = fmt.Sprintf(" AND r.log_index>%d", q.Cursor.Primary)
	}
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT
(SELECT count() FROM onchain.raw_logs FINAL WHERE chain_id=%d AND tx_hash='%s') AS total_count,
r.block_number AS block_number,r.block_time AS block_time,r.transaction_index AS transaction_index,r.log_index AS log_index,
r.contract_address AS contract_address,r.topic0 AS topic0,r.topic1 AS topic1,r.topic2 AS topic2,r.topic3 AS topic3,
r.data AS data,r.removed AS removed,p.event_name AS event_name,p.event_signature AS event_signature,
p.decoded_fields AS decoded_fields,r.source_provider AS source_provider,r.parser_version AS parser_version,
r.normalizer_version AS normalizer_version,r.schema_version AS schema_version,r.ingested_at AS ingested_at
FROM onchain.raw_logs AS r FINAL LEFT JOIN onchain.parsed_events AS p FINAL
ON r.chain_id=p.chain_id AND r.tx_hash=p.tx_hash AND r.log_index=p.log_index
WHERE r.chain_id=%d AND r.tx_hash='%s'%s ORDER BY r.log_index ASC LIMIT %d`,
		q.ChainID, q.TxHash, q.ChainID, q.TxHash, cursorWhere, q.Limit+1))
	if err != nil {
		return RawLogPage{}, err
	}
	page := RawLogPage{ChainID: q.ChainID, TransactionHash: q.TxHash, Items: []CanonicalRawLog{}}
	if len(rows) > 0 {
		page.TotalCount, err = uint64Value(rows[0], "total_count")
		if err != nil {
			return RawLogPage{}, ErrInvalidData
		}
	}
	hasMore := len(rows) > q.Limit
	if hasMore {
		rows = rows[:q.Limit]
	}
	for _, row := range rows {
		item, decodeErr := decodeRawLog(row)
		if decodeErr != nil {
			return RawLogPage{}, decodeErr
		}
		page.Items = append(page.Items, item)
	}
	addresses := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		addresses = append(addresses, item.Contract.Address)
	}
	labels, err := r.addresses(ctx, q.ChainID, addresses)
	if err != nil {
		return RawLogPage{}, err
	}
	for i := range page.Items {
		page.Items[i].Contract = labeled(page.Items[i].Contract.Address, labels)
	}
	page.Count = len(page.Items)
	if hasMore {
		last := page.Items[len(page.Items)-1]
		page.NextCursor, err = encodeTxCursor(txCursor{Version: 1, Kind: txCursorRawLogs, ChainID: q.ChainID, TxHash: q.TxHash, Primary: last.LogIndex})
	}
	return page, err
}

func (r *Repository) labelTokenTransfers(ctx context.Context, chainID uint32, items []TxTokenTransfer) error {
	addresses := make([]string, 0, len(items)*2)
	for _, item := range items {
		addresses = append(addresses, item.From.Address, item.To.Address)
	}
	labels, err := r.addresses(ctx, chainID, addresses)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].From = labeled(items[i].From.Address, labels)
		items[i].To = labeled(items[i].To.Address, labels)
	}
	return nil
}

func (r *Repository) labelInternalTransactions(ctx context.Context, chainID uint32, items []CanonicalInternalTransaction) error {
	addresses := make([]string, 0, len(items)*2)
	for _, item := range items {
		addresses = append(addresses, item.From.Address, item.To.Address)
	}
	labels, err := r.addresses(ctx, chainID, addresses)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].From = labeled(items[i].From.Address, labels)
		items[i].To = labeled(items[i].To.Address, labels)
	}
	return nil
}

func decodeRawLog(row map[string]any) (CanonicalRawLog, error) {
	var out CanonicalRawLog
	var err error
	if out.BlockNumber, err = uint64Value(row, "block_number"); err != nil {
		return out, ErrInvalidData
	}
	if out.BlockTime, err = timeValue(row, "block_time"); err != nil {
		return out, ErrInvalidData
	}
	if value, e := uint64Value(row, "transaction_index"); e != nil || value > uint64(^uint32(0)) {
		return out, ErrInvalidData
	} else {
		out.TransactionIndex = uint32(value)
	}
	if value, e := uint64Value(row, "log_index"); e != nil || value > uint64(^uint32(0)) {
		return out, ErrInvalidData
	} else {
		out.LogIndex = uint32(value)
	}
	contract := strings.ToLower(textValue(row, "contract_address"))
	if !evmAddressRE.MatchString(contract) {
		return out, ErrInvalidData
	}
	out.Contract = AddressDTO{Address: contract}
	for key, target := range map[string]**string{"topic0": &out.Topic0, "topic1": &out.Topic1, "topic2": &out.Topic2, "topic3": &out.Topic3} {
		value := strings.ToLower(textValue(row, key))
		if value == "" {
			continue
		}
		if !topicRE.MatchString(value) {
			return out, ErrInvalidData
		}
		copyValue := value
		*target = &copyValue
	}
	out.Data = strings.ToLower(textValue(row, "data"))
	if !hexDataRE.MatchString(out.Data) {
		return out, ErrInvalidData
	}
	if out.Removed, err = boolValue(row, "removed"); err != nil {
		return out, ErrInvalidData
	}
	out.EventName = textValue(row, "event_name")
	out.EventSignature = textValue(row, "event_signature")
	if decoded := textValue(row, "decoded_fields"); decoded != "" {
		var object map[string]any
		if json.Unmarshal([]byte(decoded), &object) != nil || object == nil {
			return out, ErrInvalidData
		}
		out.Decoded = json.RawMessage(decoded)
	}
	out.Provenance, err = decodeProvenance(row, "RAW_LOG")
	return out, err
}
