package canonicalapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidInput = errors.New("invalid canonical query input")
	ErrNotFound     = errors.New("canonical asset not found")
	ErrQueryFailed  = errors.New("canonical data query failed")
	ErrInvalidData  = errors.New("invalid canonical data")

	evmAddressRE = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
	txHashRE     = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
	decimalRE    = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)
	hexAmountRE  = regexp.MustCompile(`^0x[0-9a-f]+$`)
)

const (
	defaultActivityLimit = 50
	maxActivityLimit     = 200
)

type QueryClient interface {
	QueryJSON(ctx context.Context, query string) ([]map[string]any, error)
}

type Repository struct{ client QueryClient }

func NewRepository(client QueryClient) *Repository { return &Repository{client: client} }

// GetTransaction returns one complete, Explorer-ready transaction without RPC
// or HTTP enrichment. Every child collection is loaded from canonical facts and
// dimensions in ClickHouse.
func (r *Repository) GetTransaction(ctx context.Context, chainID uint32, txHash string) (CanonicalTransaction, error) {
	txHash, err := validateTxScope(chainID, txHash)
	if err != nil {
		return CanonicalTransaction{}, err
	}
	if r == nil || r.client == nil {
		return CanonicalTransaction{}, ErrQueryFailed
	}

	rows, err := r.query(ctx, transactionSQL(chainID, txHash))
	if err != nil {
		return CanonicalTransaction{}, err
	}
	if len(rows) == 0 {
		return CanonicalTransaction{}, ErrNotFound
	}
	tx, err := decodeTransaction(rows[0])
	if err != nil || tx.ChainID != chainID || tx.TxHash != txHash {
		return CanonicalTransaction{}, ErrInvalidData
	}

	method, err := r.resolveMethod(ctx, tx.Method.ID)
	if err != nil {
		return CanonicalTransaction{}, err
	}
	tx.Method = method

	transfers, err := r.tokenTransfers(ctx, chainID, txHash)
	if err != nil {
		return CanonicalTransaction{}, fmt.Errorf("token transfers: %w", err)
	}
	internal, err := r.internalTransactions(ctx, chainID, txHash)
	if err != nil {
		return CanonicalTransaction{}, fmt.Errorf("internal transactions: %w", err)
	}
	creation, err := r.contractCreation(ctx, chainID, txHash)
	if err != nil {
		return CanonicalTransaction{}, fmt.Errorf("contract creation: %w", err)
	}
	events, err := r.parsedEvents(ctx, chainID, txHash)
	if err != nil {
		return CanonicalTransaction{}, fmt.Errorf("parsed events: %w", err)
	}

	addresses := transactionAddresses(tx, transfers, internal, creation, events)
	labels, err := r.addresses(ctx, chainID, addresses)
	if err != nil {
		return CanonicalTransaction{}, fmt.Errorf("address labels: %w", err)
	}
	applyLabels(&tx, transfers, internal, creation, events, labels)
	tx.TokenTransfers = transfers
	tx.InternalTransactions = internal
	tx.ContractCreation = creation
	tx.ParsedEvents = events
	return tx, nil
}

func (r *Repository) ListActivity(ctx context.Context, input ActivityQuery) ([]CanonicalActivity, error) {
	address, err := validateAddressScope(input.ChainID, input.Address)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultActivityLimit
	}
	if limit < 1 || limit > maxActivityLimit {
		return nil, fmt.Errorf("%w: limit out of range", ErrInvalidInput)
	}
	rows, err := r.query(ctx, activitySQL(input.ChainID, address, limit))
	if err != nil {
		return nil, err
	}
	out := make([]CanonicalActivity, 0, len(rows))
	addressSet := map[string]struct{}{address: {}}
	for _, row := range rows {
		item, err := decodeActivity(row)
		if err != nil {
			return nil, fmt.Errorf("decode activity: %w", err)
		}
		if item.ChainID != input.ChainID || item.Address.Address != address {
			return nil, ErrInvalidData
		}
		out = append(out, item)
		if item.Counterparty != nil && evmAddressRE.MatchString(item.Counterparty.Address) {
			addressSet[item.Counterparty.Address] = struct{}{}
		}
	}
	keys := make([]string, 0, len(addressSet))
	for key := range addressSet {
		keys = append(keys, key)
	}
	labels, err := r.addresses(ctx, input.ChainID, keys)
	if err != nil {
		return nil, err
	}
	methodIDs := make([]string, 0, len(out))
	for i := range out {
		if out[i].Method.ID != "" {
			methodIDs = append(methodIDs, out[i].Method.ID)
		}
	}
	methods, err := r.resolveMethods(ctx, methodIDs)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Address = labeled(out[i].Address.Address, labels)
		if out[i].Counterparty != nil {
			value := labeled(out[i].Counterparty.Address, labels)
			out[i].Counterparty = &value
		}
		if method, ok := methods[out[i].Method.ID]; ok {
			out[i].Method = method
		}
	}
	return out, nil
}

func (r *Repository) query(ctx context.Context, query string) ([]map[string]any, error) {
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, ErrQueryFailed
	}
	return rows, nil
}

func transactionSQL(chainID uint32, txHash string) string {
	return fmt.Sprintf(`SELECT chain_id,block_number,block_hash,block_time,transaction_index,tx_hash,from_address,to_address,
nonce,value_raw,toString(value_decimal) AS value_decimal,input,method_id,tx_type,gas_limit,gas_used,
toString(ifNull(gas_price,0)) AS gas_price,toString(ifNull(effective_gas_price,0)) AS effective_gas_price,
toString(transaction_fee_native) AS fee_native,toString(transaction_fee_usd) AS fee_usd,status,raw_status,status_source,
is_contract_creation,created_contract_address,error_message,source_provider,ingest_job_id,source_range_id,
parser_version,normalizer_version,schema_version,ingested_at
FROM onchain.chain_transactions FINAL WHERE chain_id=%d AND tx_hash='%s' ORDER BY ingested_at DESC LIMIT 1`, chainID, txHash)
}

func activitySQL(chainID uint32, address string, limit int) string {
	return fmt.Sprintf(`SELECT a.chain_id AS chain_id,a.address AS address,a.counterparty_address AS counterparty_address,
a.direction AS direction,a.activity_type AS activity_type,a.block_number AS block_number,a.block_time AS block_time,
a.tx_hash AS tx_hash,a.event_index AS event_index,a.token_address AS token_address,a.amount AS amount,toString(a.amount) AS amount_text,
toString(coalesce(a.usd_value,CAST(a.amount*coalesce(a.price_usd,p.price_usd) AS Nullable(Decimal(38,18))))) AS usd_value,
a.method_id AS method_id,tx.status AS tx_status,tx.status_source AS status_source,a.source_provider AS source_provider,
a.ingest_job_id AS ingest_job_id,a.source_range_id AS source_range_id,a.parser_version AS parser_version,
a.normalizer_version AS normalizer_version,a.schema_version AS schema_version,a.ingested_at AS ingested_at,
t.name AS token_name,t.symbol AS token_symbol,t.decimals AS token_decimals,t.token_standard,t.logo_uri,t.logo_hash,
t.logo_source,t.is_verified,t.is_spam,t.official_website,t.metadata_source,t.metadata_confidence,t.metadata_updated_at,
toString(coalesce(a.price_usd,p.price_usd)) AS price_usd,coalesce(a.price_time,p.timestamp_bucket) AS price_time,
if(a.price_source!='',a.price_source,p.source) AS price_source,
if(a.price_confidence!='',a.price_confidence,p.confidence) AS price_confidence
FROM onchain.address_activity AS a FINAL
LEFT JOIN onchain.chain_transactions AS tx FINAL ON a.chain_id=tx.chain_id AND a.tx_hash=tx.tx_hash
LEFT JOIN onchain.token_metadata_registry AS t FINAL ON a.chain_id=t.chain_id AND a.token_address=t.contract_address
ASOF LEFT JOIN (SELECT * FROM onchain.token_prices FINAL ORDER BY chain_id,token_address,timestamp_bucket) AS p
ON a.chain_id=p.chain_id AND a.token_address=p.token_address AND a.block_time>=p.timestamp_bucket
WHERE a.chain_id=%d AND a.address='%s' ORDER BY a.block_time DESC,a.tx_hash DESC,a.event_index DESC LIMIT %d`, chainID, address, limit)
}

func (r *Repository) resolveMethods(ctx context.Context, methodIDs []string) (map[string]MethodDTO, error) {
	unique := make(map[string]struct{})
	for _, methodID := range methodIDs {
		methodID = strings.ToLower(strings.TrimSpace(methodID))
		if methodID == "" {
			continue
		}
		if !regexp.MustCompile(`^0x[0-9a-f]{8}$`).MatchString(methodID) {
			return nil, ErrInvalidData
		}
		unique[methodID] = struct{}{}
	}
	out := make(map[string]MethodDTO, len(unique))
	if len(unique) == 0 {
		return out, nil
	}
	values := make([]string, 0, len(unique))
	for methodID := range unique {
		values = append(values, "'"+methodID+"'")
		out[methodID] = MethodDTO{ID: methodID, Name: methodID, Display: methodID, Confidence: "LOW", Source: "RAW_SELECTOR"}
	}
	sort.Strings(values)
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT method_id,canonical_signature,display_name,source,confidence,is_verified,updated_at
FROM onchain.method_registry FINAL WHERE method_id IN (%s) ORDER BY method_id,is_verified DESC,updated_at DESC`, strings.Join(values, ",")))
	if err != nil {
		return nil, err
	}
	grouped := make(map[string][]map[string]any)
	for _, row := range rows {
		methodID := strings.ToLower(textValue(row, "method_id"))
		if _, ok := unique[methodID]; !ok {
			return nil, ErrInvalidData
		}
		grouped[methodID] = append(grouped[methodID], row)
	}
	for methodID, entries := range grouped {
		candidates := make(map[string]struct{})
		for _, row := range entries {
			if signature := textValue(row, "canonical_signature"); signature != "" {
				candidates[signature] = struct{}{}
			}
		}
		if len(candidates) > 1 {
			list := make([]string, 0, len(candidates))
			for signature := range candidates {
				list = append(list, signature)
			}
			sort.Strings(list)
			out[methodID] = MethodDTO{ID: methodID, Name: "ambiguous", Display: "Ambiguous", Confidence: "AMBIGUOUS", Source: "METHOD_REGISTRY", CandidateSignatures: list}
			continue
		}
		row := entries[0]
		name := signatureName(textValue(row, "canonical_signature"))
		display := textValue(row, "display_name")
		if display == "" {
			display = name
		}
		source := textValue(row, "source")
		if source == "" {
			source = "METHOD_REGISTRY"
		}
		out[methodID] = MethodDTO{ID: methodID, Name: name, Display: display, Source: source, Confidence: canonicalConfidence(textValue(row, "confidence"))}
	}
	return out, nil
}

func (r *Repository) resolveMethod(ctx context.Context, methodID string) (MethodDTO, error) {
	methodID = strings.ToLower(strings.TrimSpace(methodID))
	if methodID == "" {
		return MethodDTO{Confidence: "UNKNOWN", Source: "NO_INPUT"}, nil
	}
	if !regexp.MustCompile(`^0x[0-9a-f]{8}$`).MatchString(methodID) {
		return MethodDTO{}, ErrInvalidData
	}
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT method_id,canonical_signature,display_name,source,confidence
FROM onchain.method_registry FINAL WHERE method_id='%s' ORDER BY is_verified DESC,updated_at DESC LIMIT 32`, methodID))
	if err != nil {
		return MethodDTO{}, err
	}
	if len(rows) == 0 {
		return MethodDTO{ID: methodID, Name: methodID, Display: methodID, Confidence: "LOW", Source: "RAW_SELECTOR"}, nil
	}
	candidates := make(map[string]struct{})
	var best MethodDTO
	for i, row := range rows {
		sig := textValue(row, "canonical_signature")
		if sig != "" {
			candidates[sig] = struct{}{}
		}
		if i == 0 {
			best = MethodDTO{ID: methodID, Name: signatureName(sig), Display: textValue(row, "display_name"), Source: textValue(row, "source"), Confidence: canonicalConfidence(textValue(row, "confidence"))}
		}
	}
	if len(candidates) > 1 {
		list := make([]string, 0, len(candidates))
		for value := range candidates {
			list = append(list, value)
		}
		sort.Strings(list)
		return MethodDTO{ID: methodID, Name: "ambiguous", Display: "Ambiguous", Confidence: "AMBIGUOUS", Source: "METHOD_REGISTRY", CandidateSignatures: list}, nil
	}
	if best.Display == "" {
		best.Display = best.Name
	}
	if best.Source == "" {
		best.Source = "METHOD_REGISTRY"
	}
	return best, nil
}

func (r *Repository) tokenTransfers(ctx context.Context, chainID uint32, txHash string) ([]CanonicalTokenTransfer, error) {
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT x.log_index,x.from_address,x.to_address,x.token_address AS token_address,x.raw_value,
toString(x.value_decimal) AS value_decimal,x.token_standard,x.source_provider,x.ingest_job_id,x.source_range_id,
x.parser_version,x.normalizer_version,x.schema_version,x.ingested_at,
	x.token_name AS fact_token_name,x.token_symbol AS fact_token_symbol,x.token_decimals AS fact_token_decimals,
	t.name AS token_name,t.symbol AS token_symbol,t.decimals AS token_decimals,t.token_standard AS registry_standard,
t.logo_uri,t.logo_hash,t.logo_source,t.is_verified,t.is_spam,t.official_website,t.metadata_source,
t.metadata_confidence,t.metadata_updated_at,
toString(coalesce(x.usd_value,CAST(x.value_decimal*coalesce(x.usd_price,p.price_usd) AS Nullable(Decimal(38,18))))) AS usd_value,
toString(coalesce(x.usd_price,p.price_usd)) AS price_usd,coalesce(x.price_time,p.timestamp_bucket) AS price_time,
if(x.price_source!='',x.price_source,p.source) AS price_source,
if(x.price_confidence!='',x.price_confidence,p.confidence) AS price_confidence
FROM onchain.token_transfers AS x FINAL
LEFT JOIN onchain.token_metadata_registry AS t FINAL ON x.chain_id=t.chain_id AND x.token_address=t.contract_address
ASOF LEFT JOIN (SELECT * FROM onchain.token_prices FINAL ORDER BY chain_id,token_address,timestamp_bucket) AS p
ON x.chain_id=p.chain_id AND x.token_address=p.token_address AND x.block_time>=p.timestamp_bucket
WHERE x.chain_id=%d AND x.tx_hash='%s' ORDER BY x.log_index,x.token_address`, chainID, txHash))
	if err != nil {
		return nil, err
	}
	out := make([]CanonicalTokenTransfer, 0, len(rows))
	for _, row := range rows {
		item, e := decodeTokenTransfer(chainID, row)
		if e != nil {
			return nil, fmt.Errorf("decode token transfer: %w", e)
		}
		out = append(out, item)
	}
	return out, nil
}

func (r *Repository) internalTransactions(ctx context.Context, chainID uint32, txHash string) ([]CanonicalInternalTransaction, error) {
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT trace_address,trace_index,call_type,depth,parent_trace_index,from_address,to_address,
value_raw,toString(value_decimal) AS value_decimal,input,output,gas,gas_used,success,error,source_provider,ingest_job_id,
source_range_id,parser_version,schema_version,ingested_at FROM onchain.internal_transactions FINAL
WHERE chain_id=%d AND tx_hash='%s' ORDER BY trace_index,trace_address`, chainID, txHash))
	if err != nil {
		return nil, err
	}
	out := make([]CanonicalInternalTransaction, 0, len(rows))
	for _, row := range rows {
		item, e := decodeInternal(row)
		if e != nil {
			return nil, ErrInvalidData
		}
		out = append(out, item)
	}
	return out, nil
}

func (r *Repository) contractCreation(ctx context.Context, chainID uint32, txHash string) (*CanonicalContractCreation, error) {
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT creator_address,factory_address,contract_address,creation_type,init_code_hash,runtime_code_hash,
is_proxy,proxy_type,implementation_address,token_detected,token_standard,source_provider,ingest_job_id,source_range_id,
parser_version,schema_version,ingested_at FROM onchain.contract_creations FINAL WHERE chain_id=%d AND tx_hash='%s'
ORDER BY ingested_at DESC LIMIT 1`, chainID, txHash))
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	item, e := decodeCreation(rows[0])
	if e != nil {
		return nil, ErrInvalidData
	}
	return &item, nil
}

func (r *Repository) parsedEvents(ctx context.Context, chainID uint32, txHash string) ([]CanonicalParsedEvent, error) {
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT log_index,contract_address,topic0,event_name,event_signature,decoded_fields,
decoder_source,decoder_confidence,parser_version,schema_version FROM onchain.parsed_events FINAL
WHERE chain_id=%d AND tx_hash='%s' ORDER BY log_index`, chainID, txHash))
	if err != nil {
		return nil, err
	}
	out := make([]CanonicalParsedEvent, 0, len(rows))
	for _, row := range rows {
		item, e := decodeEvent(row)
		if e != nil {
			return nil, ErrInvalidData
		}
		out = append(out, item)
	}
	return out, nil
}

func (r *Repository) addresses(ctx context.Context, chainID uint32, addresses []string) (map[string]AddressDTO, error) {
	unique := make(map[string]struct{})
	for _, address := range addresses {
		address = strings.ToLower(address)
		if address == "" {
			continue
		}
		if !evmAddressRE.MatchString(address) {
			return nil, ErrInvalidData
		}
		unique[address] = struct{}{}
	}
	if len(unique) == 0 {
		return map[string]AddressDTO{}, nil
	}
	values := make([]string, 0, len(unique))
	for address := range unique {
		values = append(values, "'"+address+"'")
	}
	sort.Strings(values)
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT l.address,l.label_name,l.label_type,l.entity_id,l.entity_role,l.source,l.confidence,
e.entity_name,e.entity_type FROM onchain.address_labels AS l FINAL LEFT JOIN onchain.entity_registry AS e FINAL
ON l.entity_id=e.entity_id WHERE l.chain_id=%d AND l.address IN (%s) ORDER BY l.address,l.last_verified DESC,l.updated_at DESC`, chainID, strings.Join(values, ",")))
	if err != nil {
		return nil, err
	}
	out := make(map[string]AddressDTO, len(unique))
	for address := range unique {
		out[address] = AddressDTO{Address: address}
	}
	for _, row := range rows {
		address := strings.ToLower(textValue(row, "address"))
		if !evmAddressRE.MatchString(address) {
			return nil, ErrInvalidData
		}
		if current := out[address]; current.Label != "" {
			continue
		}
		out[address] = AddressDTO{Address: address, Label: textValue(row, "label_name"), LabelType: textValue(row, "label_type"), EntityID: textValue(row, "entity_id"), EntityName: textValue(row, "entity_name"), EntityType: textValue(row, "entity_type"), EntityRole: textValue(row, "entity_role"), LabelSource: textValue(row, "source"), LabelConfidence: canonicalConfidence(textValue(row, "confidence"))}
	}
	return out, nil
}

func validateTxScope(chainID uint32, txHash string) (string, error) {
	if chainID == 0 {
		return "", fmt.Errorf("%w: chain_id is required", ErrInvalidInput)
	}
	txHash = strings.ToLower(strings.TrimSpace(txHash))
	if !txHashRE.MatchString(txHash) {
		return "", fmt.Errorf("%w: invalid transaction hash", ErrInvalidInput)
	}
	return txHash, nil
}
func validateAddressScope(chainID uint32, address string) (string, error) {
	if chainID == 0 {
		return "", fmt.Errorf("%w: chain_id is required", ErrInvalidInput)
	}
	address = strings.ToLower(strings.TrimSpace(address))
	if !evmAddressRE.MatchString(address) {
		return "", fmt.Errorf("%w: invalid address", ErrInvalidInput)
	}
	return address, nil
}

func signatureName(signature string) string {
	if index := strings.IndexByte(signature, '('); index > 0 {
		return signature[:index]
	}
	return signature
}
func canonicalConfidence(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "HIGH", "MEDIUM", "LOW", "AMBIGUOUS":
		return strings.ToUpper(strings.TrimSpace(value))
	default:
		return "UNKNOWN"
	}
}
func canonicalStatus(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "SUCCESS":
		return "SUCCESS"
	case "FAILED":
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}
func formattedDecimal(value string) string {
	if !decimalRE.MatchString(value) {
		return value
	}
	if !strings.Contains(value, ".") {
		return value + ".00"
	}
	parts := strings.SplitN(value, ".", 2)
	fraction := strings.TrimRight(parts[1], "0")
	if fraction == "" {
		return parts[0] + ".00"
	}
	if len(fraction) == 1 {
		return parts[0] + "." + fraction + "0"
	}
	return parts[0] + "." + fraction
}
func amount(raw, decimal string) (AmountDTO, error) {
	if raw == "" {
		raw = "0"
	}
	if decimal == "" {
		decimal = "0"
	}
	validRaw := decimalRE.MatchString(raw) || hexAmountRE.MatchString(strings.ToLower(raw))
	if len(raw) > 512 || len(decimal) > 512 || !validRaw || !decimalRE.MatchString(decimal) {
		return AmountDTO{}, ErrInvalidData
	}
	return AmountDTO{Raw: raw, Decimal: decimal, Formatted: formattedDecimal(decimal)}, nil
}

func textValue(row map[string]any, key string) string {
	value, ok := row[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(typed)
	}
}
func uint64Value(row map[string]any, key string) (uint64, error) {
	value := textValue(row, key)
	if value == "" {
		return 0, nil
	}
	result, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, ErrInvalidData
	}
	return result, nil
}
func boolValue(row map[string]any, key string) (bool, error) {
	value := strings.ToLower(textValue(row, key))
	switch value {
	case "true", "1":
		return true, nil
	case "false", "0", "":
		return false, nil
	default:
		return false, ErrInvalidData
	}
}
func timeValue(row map[string]any, key string) (time.Time, error) {
	value := textValue(row, key)
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, ErrInvalidData
}
