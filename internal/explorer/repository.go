package explorer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

var (
	ErrInvalidInput = errors.New("invalid explorer input")
	ErrNotFound     = errors.New("address not found")
	ErrQueryFailed  = errors.New("explorer query failed")
	ErrInvalidData  = errors.New("invalid explorer result")

	evmAddressPattern = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
	txHashPattern     = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
	eventIndexPattern = regexp.MustCompile(`^[0-9A-Za-z_.:,/\-]{0,128}$`)
)

var supportedChains = map[uint32]struct{}{
	1: {}, 56: {}, 8453: {}, 42161: {},
}

var activityPredicates = map[ActivityKind]string{
	ActivityAll:              "",
	ActivityTransactions:     "a.activity_type IN ('NATIVE_TRANSFER', 'CONTRACT_CALL')",
	ActivityTokenTransfers:   "a.activity_type IN ('TOKEN_TRANSFER', 'ERC20_TRANSFER', 'ERC721_TRANSFER', 'ERC1155_TRANSFER')",
	ActivityInternal:         "a.activity_type IN ('INTERNAL_TRANSFER', 'INTERNAL_TRANSACTION')",
	ActivityContractCreation: "a.activity_type IN ('CONTRACT_CREATE', 'CONTRACT_CREATION')",
}

// QueryClient is intentionally minimal so the ClickHouse client can satisfy it
// without making the explorer package depend on a concrete transport.
type QueryClient interface {
	QueryJSON(ctx context.Context, query string) ([]map[string]any, error)
}

type Repository struct {
	client QueryClient
}

func NewRepository(client QueryClient) *Repository {
	return &Repository{client: client}
}

func (r *Repository) GetAddressProfile(ctx context.Context, chainID uint32, address string) (AddressProfile, error) {
	summary, err := r.GetAddressSummary(ctx, chainID, address)
	if err != nil {
		return AddressProfile{}, err
	}
	return AddressProfile{ChainID: summary.ChainID, Address: summary.Address, Summary: summary}, nil
}

func (r *Repository) GetAddressSummary(ctx context.Context, chainID uint32, address string) (AddressSummary, error) {
	address, err := validateScope(chainID, address)
	if err != nil {
		return AddressSummary{}, err
	}
	if r == nil || r.client == nil {
		return AddressSummary{}, ErrQueryFailed
	}

	query := fmt.Sprintf(`SELECT
chain_id, address, address_type, first_seen_time, last_seen_time,
tx_count, in_tx_count, out_tx_count, token_transfer_count,
internal_tx_count, nft_transfer_count, contract_created_count,
unique_counterparty_count, toString(native_received) AS native_received,
toString(native_sent) AS native_sent, toString(native_netflow) AS native_netflow,
toString(usd_received) AS usd_received, toString(usd_sent) AS usd_sent,
toString(usd_netflow) AS usd_netflow, active_days,
toString(max_single_in_usd) AS max_single_in_usd,
toString(max_single_out_usd) AS max_single_out_usd, top_counterparty,
cex_interaction_count, dex_interaction_count, bridge_interaction_count,
toFloat64(risk_score) AS risk_score, updated_at
FROM onchain.address_summary FINAL
WHERE chain_id = %d AND address = '%s'
ORDER BY updated_at DESC
LIMIT 1`, chainID, address)

	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return AddressSummary{}, ErrQueryFailed
	}
	if len(rows) == 0 {
		return AddressSummary{}, ErrNotFound
	}
	summary, err := decodeSummary(rows[0])
	if err != nil {
		return AddressSummary{}, ErrInvalidData
	}
	if summary.ChainID != chainID || strings.ToLower(summary.Address) != address || !evmAddressPattern.MatchString(strings.ToLower(summary.Address)) {
		return AddressSummary{}, ErrInvalidData
	}
	summary.Address = address
	return summary, nil
}

func (r *Repository) ListActivity(ctx context.Context, input ActivityQuery) (ActivityPage, error) {
	address, err := validateScope(input.ChainID, input.Address)
	if err != nil {
		return ActivityPage{}, err
	}
	predicate, ok := activityPredicates[input.Activity]
	if !ok {
		return ActivityPage{}, fmt.Errorf("%w: unsupported activity", ErrInvalidInput)
	}
	if r == nil || r.client == nil {
		return ActivityPage{}, ErrQueryFailed
	}
	pageSize := input.PageSize
	if pageSize == 0 {
		pageSize = defaultPageSize
	}
	if pageSize < 1 || pageSize > maxPageSize {
		return ActivityPage{}, fmt.Errorf("%w: page_size must be between 1 and %d", ErrInvalidInput, maxPageSize)
	}

	where := []string{
		fmt.Sprintf("a.chain_id = %d", input.ChainID),
		fmt.Sprintf("a.address = '%s'", address),
	}
	if predicate != "" {
		where = append(where, predicate)
	}
	if input.Cursor != "" {
		cursor, err := decodeCursor(input.Cursor, input.ChainID, address, input.Activity)
		if err != nil {
			return ActivityPage{}, err
		}
		where = append(where, fmt.Sprintf(
			"(a.block_time, a.block_number, a.tx_hash, a.event_index) < (parseDateTime64BestEffort('%s', 3, 'UTC'), %d, '%s', '%s')",
			cursor.BlockTime.UTC().Format(time.RFC3339Nano), cursor.BlockNumber, cursor.TxHash, cursor.EventIndex,
		))
	}

	query := fmt.Sprintf(`SELECT
chain_id,address,counterparty_address,direction,activity_type,block_number,block_time,tx_hash,event_index,
token_address,token_symbol,token_name,token_logo_uri,token_logo_source,token_verified,token_spam,amount,
if(isNull(resolved_price),NULL,toString(resolved_price)) AS historical_price_usdt,
if(isNull(resolved_price),NULL,toString(CAST(amount_decimal * resolved_price AS Nullable(Decimal(38,18))))) AS historical_value_usdt,
if(isNull(resolved_price),NULL,toString(resolved_price)) AS price_usd,
if(isNull(resolved_price),NULL,toString(CAST(amount_decimal * resolved_price AS Nullable(Decimal(38,18))))) AS usd_value,
resolved_time AS price_time,resolved_time AS price_timestamp,resolved_source AS price_source,
resolved_route AS price_route,resolved_type AS price_type,resolved_confidence AS price_confidence,
if(isNull(resolved_time),0,dateDiff('second',resolved_time,block_time)) AS price_age_seconds,
multiIf(isNull(resolved_price),'NO_PRICE',resolved_confidence<0.35,'LOW_CONFIDENCE','VALUED') AS valuation_status,
method_id,method_name,status,counterparty_entity_type,counterparty_label,source_provider
FROM
(
SELECT
a.chain_id AS chain_id, a.address AS address, a.counterparty_address AS counterparty_address,
a.direction AS direction, a.activity_type AS activity_type,
a.block_number AS block_number, a.block_time AS block_time, a.tx_hash AS tx_hash,
a.event_index AS event_index, a.token_address AS token_address,
if(t.symbol != '', t.symbol, a.token_symbol) AS token_symbol, t.name AS token_name,
t.logo_uri AS token_logo_uri, t.logo_source AS token_logo_source,
t.is_verified AS token_verified, t.is_spam AS token_spam,
toString(a.amount) AS amount,a.amount AS amount_decimal,
if(a.token_address='',concat('native:',toString(a.chain_id)),a.token_address) AS token_key,
multiIf(
  isNotNull(a.price_usd),a.price_usd,
  token_key='0x55d398326f99059ff775485246999027b3197955',toDecimal128(1,18),
  q.token_address!='',if(q.vwap>0,q.vwap,q.close),
  p.token_address!='' AND dateDiff('second',p.timestamp_bucket,a.block_time)<=86400,p.price_usd,
  token_key IN ('0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d','0xe9e7cea3dedca5984780bafc599bd69add087d56','0xc5f0f7b66764f6ec8c8dff7ba683102295e16409'),toDecimal128(1,18),
  CAST(NULL AS Nullable(Decimal(38,18)))) AS resolved_price,
multiIf(isNotNull(a.price_time),a.price_time,token_key='0x55d398326f99059ff775485246999027b3197955',toStartOfMinute(a.block_time),q.token_address!='',q.minute,p.token_address!='' AND dateDiff('second',p.timestamp_bucket,a.block_time)<=86400,p.timestamp_bucket,token_key IN ('0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d','0xe9e7cea3dedca5984780bafc599bd69add087d56','0xc5f0f7b66764f6ec8c8dff7ba683102295e16409'),toStartOfMinute(a.block_time),CAST(NULL AS Nullable(DateTime64(3,'UTC')))) AS resolved_time,
multiIf(a.price_source!='',a.price_source,token_key='0x55d398326f99059ff775485246999027b3197955','STABLECOIN_PEG',q.token_address!='',q.price_source,p.token_address!='' AND dateDiff('second',p.timestamp_bucket,a.block_time)<=86400,p.source,token_key IN ('0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d','0xe9e7cea3dedca5984780bafc599bd69add087d56','0xc5f0f7b66764f6ec8c8dff7ba683102295e16409'),'STABLECOIN_PEG','UNKNOWN') AS resolved_source,
multiIf(q.token_address!='',q.route,token_key='0x55d398326f99059ff775485246999027b3197955','USDT/USDT',token_key IN ('0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d','0xe9e7cea3dedca5984780bafc599bd69add087d56','0xc5f0f7b66764f6ec8c8dff7ba683102295e16409'),concat(if(t.symbol!='',t.symbol,a.token_symbol),'/USDT'),p.token_address!='',concat(if(t.symbol!='',t.symbol,a.token_symbol),'/USDT'),'') AS resolved_route,
multiIf(isNotNull(a.price_usd),'TRADED',q.token_address!='' AND q.is_last_known,'LAST_KNOWN',q.token_address!='','TRADED',token_key IN ('0x55d398326f99059ff775485246999027b3197955','0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d','0xe9e7cea3dedca5984780bafc599bd69add087d56','0xc5f0f7b66764f6ec8c8dff7ba683102295e16409'),'PEG',p.token_address!='' AND dateDiff('second',p.timestamp_bucket,a.block_time)<=86400,'LAST_KNOWN','UNKNOWN') AS resolved_type,
multiIf(q.token_address!='',q.confidence,a.price_confidence='HIGH',toFloat32(0.95),a.price_confidence='MEDIUM',toFloat32(0.75),a.price_confidence='LOW',toFloat32(0.5),token_key IN ('0x55d398326f99059ff775485246999027b3197955','0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d','0xe9e7cea3dedca5984780bafc599bd69add087d56','0xc5f0f7b66764f6ec8c8dff7ba683102295e16409'),toFloat32(0.5),p.confidence='HIGH',toFloat32(0.95),p.confidence='MEDIUM',toFloat32(0.75),p.confidence='LOW',toFloat32(0.5),toFloat32(0)) AS resolved_confidence,
a.method_id AS method_id, a.method_name AS method_name, a.status AS status,
a.counterparty_entity_type AS counterparty_entity_type, a.counterparty_label AS counterparty_label,
a.source_provider AS source_provider
FROM onchain.address_activity AS a FINAL
LEFT JOIN onchain.token_metadata_registry AS t FINAL
  ON a.chain_id = t.chain_id AND a.token_address = t.contract_address
LEFT JOIN
  (SELECT chain_id,token_address,minute,vwap,close,price_source,confidence,is_last_known,route FROM onchain.token_price_1m FINAL WHERE chain_id = %d) AS q
  ON a.chain_id=q.chain_id
  AND if(a.token_address='',concat('native:',toString(a.chain_id)),a.token_address)=q.token_address
  AND toStartOfMinute(a.block_time)=q.minute
ASOF LEFT JOIN
  (SELECT * FROM onchain.token_prices FINAL WHERE chain_id = %d ORDER BY chain_id, token_address, timestamp_bucket) AS p
  ON a.chain_id = p.chain_id
  AND if(a.token_address = '', concat('native:', toString(a.chain_id)), a.token_address) = p.token_address
  AND a.block_time >= p.timestamp_bucket
WHERE %s
ORDER BY a.block_time DESC, a.block_number DESC, a.tx_hash DESC, a.event_index DESC
LIMIT %d
)
ORDER BY block_time DESC,block_number DESC,tx_hash DESC,event_index DESC`, input.ChainID, input.ChainID, strings.Join(where, " AND "), pageSize+1)

	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return ActivityPage{}, ErrQueryFailed
	}
	items := make([]Activity, 0, minInt(len(rows), pageSize))
	for i, row := range rows {
		if i == pageSize {
			break
		}
		item, err := decodeActivity(row)
		if err != nil {
			return ActivityPage{}, fmt.Errorf("%w: activity decode failed: %v", ErrInvalidData, err)
		}
		items = append(items, item)
	}

	page := ActivityPage{Items: items, HasMore: len(rows) > pageSize}
	if page.HasMore && len(items) != 0 {
		last := items[len(items)-1]
		page.NextCursor, err = encodeCursor(activityCursor{
			Version: 1, ChainID: input.ChainID, Address: address, Activity: input.Activity,
			BlockTime: last.BlockTime, BlockNumber: last.BlockNumber,
			TxHash: last.TransactionHash, EventIndex: last.EventIndex,
		})
		if err != nil {
			return ActivityPage{}, ErrInvalidData
		}
	}
	return page, nil
}

func validateScope(chainID uint32, address string) (string, error) {
	if _, ok := supportedChains[chainID]; !ok {
		return "", fmt.Errorf("%w: unsupported chain_id", ErrInvalidInput)
	}
	address = strings.ToLower(strings.TrimSpace(address))
	if !evmAddressPattern.MatchString(address) {
		return "", fmt.Errorf("%w: invalid EVM address", ErrInvalidInput)
	}
	return address, nil
}

type activityCursor struct {
	Version     uint8        `json:"v"`
	ChainID     uint32       `json:"c"`
	Address     string       `json:"a"`
	Activity    ActivityKind `json:"k"`
	BlockTime   time.Time    `json:"t"`
	BlockNumber uint64       `json:"b"`
	TxHash      string       `json:"h"`
	EventIndex  string       `json:"e"`
}

func encodeCursor(cursor activityCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(encoded string, chainID uint32, address string, activity ActivityKind) (activityCursor, error) {
	if len(encoded) > 2048 {
		return activityCursor{}, fmt.Errorf("%w: invalid cursor", ErrInvalidInput)
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return activityCursor{}, fmt.Errorf("%w: invalid cursor", ErrInvalidInput)
	}
	var cursor activityCursor
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return activityCursor{}, fmt.Errorf("%w: invalid cursor", ErrInvalidInput)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return activityCursor{}, fmt.Errorf("%w: invalid cursor", ErrInvalidInput)
	}
	if cursor.Version != 1 || cursor.ChainID != chainID || cursor.Address != address || cursor.Activity != activity ||
		cursor.BlockTime.IsZero() || !txHashPattern.MatchString(cursor.TxHash) || !eventIndexPattern.MatchString(cursor.EventIndex) {
		return activityCursor{}, fmt.Errorf("%w: cursor scope or values do not match", ErrInvalidInput)
	}
	return cursor, nil
}

func decodeSummary(row map[string]any) (AddressSummary, error) {
	var out AddressSummary
	var err error
	if out.ChainID, err = asUint32(row["chain_id"]); err != nil {
		return out, err
	}
	if out.Address, err = asString(row["address"]); err != nil {
		return out, err
	}
	out.AddressType, _ = asString(row["address_type"])
	if out.FirstSeenTime, err = asTime(row["first_seen_time"]); err != nil {
		return out, err
	}
	if out.LastSeenTime, err = asTime(row["last_seen_time"]); err != nil {
		return out, err
	}
	if out.TransactionCount, err = asUint64(row["tx_count"]); err != nil {
		return out, err
	}
	out.IncomingTransactionCount, _ = asUint64(row["in_tx_count"])
	out.OutgoingTransactionCount, _ = asUint64(row["out_tx_count"])
	out.TokenTransferCount, _ = asUint64(row["token_transfer_count"])
	out.InternalTransactionCount, _ = asUint64(row["internal_tx_count"])
	out.NFTTransferCount, _ = asUint64(row["nft_transfer_count"])
	out.ContractCreatedCount, _ = asUint64(row["contract_created_count"])
	out.UniqueCounterpartyCount, _ = asUint64(row["unique_counterparty_count"])
	out.NativeReceived, _ = asString(row["native_received"])
	out.NativeSent, _ = asString(row["native_sent"])
	out.NativeNetflow, _ = asString(row["native_netflow"])
	out.USDReceived, _ = asString(row["usd_received"])
	out.USDSent, _ = asString(row["usd_sent"])
	out.USDNetflow, _ = asString(row["usd_netflow"])
	out.ActiveDays, _ = asUint32(row["active_days"])
	out.MaxSingleInUSD, _ = asString(row["max_single_in_usd"])
	out.MaxSingleOutUSD, _ = asString(row["max_single_out_usd"])
	out.TopCounterparty, _ = asString(row["top_counterparty"])
	out.CEXInteractionCount, _ = asUint64(row["cex_interaction_count"])
	out.DEXInteractionCount, _ = asUint64(row["dex_interaction_count"])
	out.BridgeInteractionCount, _ = asUint64(row["bridge_interaction_count"])
	out.RiskScore, _ = asFloat64(row["risk_score"])
	if out.UpdatedAt, err = asTime(row["updated_at"]); err != nil {
		return out, err
	}
	return out, nil
}

func decodeActivity(row map[string]any) (Activity, error) {
	var out Activity
	var err error
	if out.ChainID, err = asUint32(row["chain_id"]); err != nil {
		return out, fmt.Errorf("invalid chain_id type %T", row["chain_id"])
	}
	if out.Address, err = asString(row["address"]); err != nil {
		return out, err
	}
	out.CounterpartyAddress, _ = asString(row["counterparty_address"])
	out.Direction, _ = asString(row["direction"])
	out.ActivityType, _ = asString(row["activity_type"])
	if out.BlockNumber, err = asUint64(row["block_number"]); err != nil {
		return out, err
	}
	if out.BlockTime, err = asTime(row["block_time"]); err != nil {
		return out, err
	}
	if out.TransactionHash, err = asString(row["tx_hash"]); err != nil {
		return out, err
	}
	if !txHashPattern.MatchString(strings.ToLower(out.TransactionHash)) {
		return out, errors.New("invalid transaction hash")
	}
	out.TransactionHash = strings.ToLower(out.TransactionHash)
	if out.EventIndex, err = asString(row["event_index"]); err != nil {
		return out, err
	}
	if !eventIndexPattern.MatchString(out.EventIndex) {
		return out, errors.New("invalid event index")
	}
	out.TokenAddress, _ = asString(row["token_address"])
	out.TokenName, _ = asString(row["token_name"])
	out.TokenSymbol, _ = asString(row["token_symbol"])
	out.TokenLogoURI, _ = asString(row["token_logo_uri"])
	out.TokenLogoSource, _ = asString(row["token_logo_source"])
	out.TokenVerified, _ = asBool(row["token_verified"])
	out.TokenSpam, _ = asBool(row["token_spam"])
	out.Amount, _ = asString(row["amount"])
	if row["usd_value"] != nil {
		value, convErr := asString(row["usd_value"])
		if convErr != nil {
			return out, convErr
		}
		out.USDValue = &value
	}
	if row["price_usd"] != nil {
		value, convErr := asString(row["price_usd"])
		if convErr != nil {
			return out, convErr
		}
		out.PriceUSD = &value
	}
	if row["historical_price_usdt"] != nil {
		value, convErr := asString(row["historical_price_usdt"])
		if convErr != nil {
			return out, convErr
		}
		out.HistoricalPriceUSDT = &value
		out.PriceUSD = &value
	} else {
		out.HistoricalPriceUSDT = out.PriceUSD
	}
	if row["historical_value_usdt"] != nil {
		value, convErr := asString(row["historical_value_usdt"])
		if convErr != nil {
			return out, convErr
		}
		out.HistoricalValueUSDT = &value
		out.USDValue = &value
	} else {
		out.HistoricalValueUSDT = out.USDValue
	}
	if row["price_time"] != nil {
		value, convErr := asTime(row["price_time"])
		if convErr == nil && !value.IsZero() {
			out.PriceTime = &value
		}
	}
	if row["price_timestamp"] != nil {
		value, convErr := asTime(row["price_timestamp"])
		if convErr == nil && !value.IsZero() {
			out.PriceTimestamp = &value
			out.PriceTime = &value
		}
	} else {
		out.PriceTimestamp = out.PriceTime
	}
	out.PriceSource, _ = asString(row["price_source"])
	out.PriceRoute, _ = asString(row["price_route"])
	out.PriceType, _ = asString(row["price_type"])
	out.ValuationStatus, _ = asString(row["valuation_status"])
	out.PriceConfidence = confidenceValue(row["price_confidence"])
	if value, convErr := asString(row["price_age_seconds"]); convErr == nil && value != "" {
		out.PriceAgeSeconds, _ = strconv.ParseInt(value, 10, 64)
	}
	if out.ValuationStatus == "" {
		if out.HistoricalPriceUSDT == nil {
			out.ValuationStatus = "NO_PRICE"
		} else {
			out.ValuationStatus = "VALUED"
		}
	}
	out.MethodID, _ = asString(row["method_id"])
	out.MethodName, _ = asString(row["method_name"])
	out.Status, _ = asString(row["status"])
	out.CounterpartyEntityType, _ = asString(row["counterparty_entity_type"])
	out.CounterpartyLabel, _ = asString(row["counterparty_label"])
	out.SourceProvider, _ = asString(row["source_provider"])
	return out, nil
}

func confidenceValue(value any) float32 {
	text, _ := asString(value)
	switch strings.ToUpper(strings.TrimSpace(text)) {
	case "HIGH":
		return 0.95
	case "MEDIUM":
		return 0.75
	case "LOW":
		return 0.5
	case "FALLBACK":
		return 0.25
	}
	parsed, err := strconv.ParseFloat(text, 32)
	if err != nil || parsed < 0 || parsed > 1 {
		return 0
	}
	return float32(parsed)
}

func asString(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case json.Number:
		return typed.String(), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32), nil
	case int:
		return strconv.Itoa(typed), nil
	case int8:
		return strconv.FormatInt(int64(typed), 10), nil
	case int16:
		return strconv.FormatInt(int64(typed), 10), nil
	case int32:
		return strconv.FormatInt(int64(typed), 10), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case uint:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(typed), 10), nil
	case uint64:
		return strconv.FormatUint(typed, 10), nil
	default:
		return "", errors.New("value is not a string")
	}
}

func asUint64(value any) (uint64, error) {
	text, err := asString(value)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(text, 10, 64)
}

func asUint32(value any) (uint32, error) {
	parsed, err := asUint64(value)
	if err != nil || parsed > math.MaxUint32 {
		return 0, errors.New("value is not uint32")
	}
	return uint32(parsed), nil
}

func asFloat64(value any) (float64, error) {
	text, err := asString(value)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(text, 64)
}

func asTime(value any) (time.Time, error) {
	if typed, ok := value.(time.Time); ok {
		return typed.UTC(), nil
	}
	text, err := asString(value)
	if err != nil {
		return time.Time{}, err
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, parseErr := time.Parse(layout, text); parseErr == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("value is not a timestamp")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
