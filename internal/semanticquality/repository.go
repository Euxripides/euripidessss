package semanticquality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidInput = errors.New("invalid semantic quality input")
	ErrQueryFailed  = errors.New("semantic quality query failed")
	ErrInvalidData  = errors.New("invalid semantic quality result")
	supportedChains = map[uint32]struct{}{1: {}, 56: {}, 8453: {}, 42161: {}}
)

type QueryClient interface {
	QueryJSON(ctx context.Context, query string) ([]map[string]any, error)
}

type Repository struct{ client QueryClient }

func NewRepository(client QueryClient) *Repository { return &Repository{client: client} }

// Report executes only local ClickHouse reads. It performs no RPC, HTTP
// enrichment, or fallback query outside the canonical database.
func (r *Repository) Report(ctx context.Context, chainID uint32) (Report, error) {
	if err := validateChain(chainID); err != nil {
		return Report{}, err
	}
	data, err := r.DataQuality(ctx, chainID)
	if err != nil {
		return Report{}, err
	}
	token, err := r.TokenQuality(ctx, chainID)
	if err != nil {
		return Report{}, err
	}
	contract, err := r.ContractQuality(ctx, chainID)
	if err != nil {
		return Report{}, err
	}
	decoder, err := r.DecoderQuality(ctx, chainID)
	if err != nil {
		return Report{}, err
	}
	price, err := r.PriceQuality(ctx, chainID)
	if err != nil {
		return Report{}, err
	}
	generated := time.Now().UTC()
	semantic, err := completeness(chainID, generated, data, token, contract, decoder, price)
	if err != nil {
		return Report{}, err
	}
	return Report{ChainID: chainID, SemanticCompleteness: semantic, Data: data, Token: token,
		Contract: contract, Decoder: decoder, Price: price, GeneratedAt: generated}, nil
}

func (r *Repository) SemanticCompleteness(ctx context.Context, chainID uint32) (SemanticCompleteness, error) {
	report, err := r.Report(ctx, chainID)
	return report.SemanticCompleteness, err
}

func (r *Repository) DataQuality(ctx context.Context, chainID uint32) (DataQuality, error) {
	if err := r.ready(chainID); err != nil {
		return DataQuality{}, err
	}
	rows, err := r.query(ctx, fmt.Sprintf(`/* semanticquality:data */ SELECT
(SELECT count() FROM onchain.chain_transactions FINAL WHERE chain_id=%d) AS transaction_rows,
(SELECT count() FROM onchain.token_transfers FINAL WHERE chain_id=%d) AS token_transfer_rows,
(SELECT count() FROM onchain.internal_transactions FINAL WHERE chain_id=%d) AS internal_transaction_rows,
(SELECT count() FROM onchain.contract_creations FINAL WHERE chain_id=%d) AS contract_creation_rows,
(SELECT count() FROM onchain.contracts FINAL WHERE chain_id=%d) AS contract_rows,
(SELECT count() FROM onchain.tokens FINAL WHERE chain_id=%d) AS token_rows,
(SELECT count() FROM onchain.address_activity FINAL WHERE chain_id=%d) AS activity_rows,
(SELECT count() FROM onchain.parsed_events FINAL WHERE chain_id=%d) AS parsed_event_rows,
(SELECT count() FROM onchain.token_prices FINAL WHERE chain_id=%d) AS token_price_rows,
(SELECT count() FROM onchain.chain_transactions FINAL WHERE chain_id=%d AND status IN ('SUCCESS','FAILED') AND upperUTF8(status_source) NOT IN ('','MISSING','UNKNOWN')) AS status_known,
(SELECT count() FROM onchain.chain_transactions FINAL WHERE chain_id=%d AND length(trimBoth(input))>2) AS method_required,
(SELECT count() FROM onchain.chain_transactions FINAL WHERE chain_id=%d AND length(trimBoth(input))>2 AND method_name!='' AND lowerUTF8(method_name) NOT IN ('unknown','ambiguous') AND method_confidence!='') AS method_known,
(SELECT uniqExact(address) FROM (SELECT address FROM onchain.address_activity FINAL WHERE chain_id=%d AND address!='' UNION ALL SELECT counterparty_address AS address FROM onchain.address_activity FINAL WHERE chain_id=%d AND counterparty_address!='')) AS entity_required,
(SELECT uniqExact(address) FROM onchain.address_labels FINAL WHERE chain_id=%d AND entity_id IS NOT NULL AND address IN (SELECT address FROM onchain.address_activity FINAL WHERE chain_id=%d UNION ALL SELECT counterparty_address FROM onchain.address_activity FINAL WHERE chain_id=%d)) AS entity_known,
(SELECT toString(max(ingested_at)) FROM onchain.chain_transactions FINAL WHERE chain_id=%d) AS transaction_updated,
(SELECT toString(max(ingested_at)) FROM onchain.token_transfers FINAL WHERE chain_id=%d) AS token_transfer_updated,
(SELECT toString(max(ingested_at)) FROM onchain.internal_transactions FINAL WHERE chain_id=%d) AS internal_transaction_updated,
(SELECT toString(max(ingested_at)) FROM onchain.contract_creations FINAL WHERE chain_id=%d) AS contract_creation_updated,
(SELECT toString(max(ingested_at)) FROM onchain.contracts FINAL WHERE chain_id=%d) AS contract_updated,
(SELECT toString(max(ingested_at)) FROM onchain.tokens FINAL WHERE chain_id=%d) AS token_updated,
(SELECT toString(max(ingested_at)) FROM onchain.address_activity FINAL WHERE chain_id=%d) AS activity_updated,
(SELECT toString(max(ingested_at)) FROM onchain.parsed_events FINAL WHERE chain_id=%d) AS parsed_event_updated,
(SELECT toString(max(updated_at)) FROM onchain.token_prices FINAL WHERE chain_id=%d) AS token_price_updated`,
		chainID, chainID, chainID, chainID, chainID, chainID, chainID, chainID, chainID, chainID, chainID, chainID,
		chainID, chainID, chainID, chainID, chainID, chainID, chainID, chainID, chainID, chainID, chainID, chainID, chainID,
		chainID))
	if err != nil || len(rows) != 1 {
		return DataQuality{}, resultError(err)
	}
	x := rows[0]
	transactions, ok := u64(x, "transaction_rows")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	tokenTransfers, ok := u64(x, "token_transfer_rows")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	internalTransactions, ok := u64(x, "internal_transaction_rows")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	contractCreations, ok := u64(x, "contract_creation_rows")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	contracts, ok := u64(x, "contract_rows")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	tokens, ok := u64(x, "token_rows")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	activity, ok := u64(x, "activity_rows")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	parsedEvents, ok := u64(x, "parsed_event_rows")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	tokenPrices, ok := u64(x, "token_price_rows")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	statusKnown, ok := u64(x, "status_known")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	methodRequired, ok := u64(x, "method_required")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	methodKnown, ok := u64(x, "method_known")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	entityRequired, ok := u64(x, "entity_required")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	entityKnown, ok := u64(x, "entity_known")
	if !ok {
		return DataQuality{}, ErrInvalidData
	}
	status, err := coverage(statusKnown, transactions, str(x, "transaction_updated"), true)
	if err != nil {
		return DataQuality{}, err
	}
	method, err := coverage(methodKnown, methodRequired, str(x, "transaction_updated"), true)
	if err != nil {
		return DataQuality{}, err
	}
	entity, err := coverage(entityKnown, entityRequired, str(x, "activity_updated"), true)
	if err != nil {
		return DataQuality{}, err
	}
	datasets := []DatasetQuality{
		{Dataset: "chain_transactions", Rows: transactions, LastUpdated: str(x, "transaction_updated")},
		{Dataset: "token_transfers", Rows: tokenTransfers, LastUpdated: str(x, "token_transfer_updated")},
		{Dataset: "internal_transactions", Rows: internalTransactions, LastUpdated: str(x, "internal_transaction_updated")},
		{Dataset: "contract_creations", Rows: contractCreations, LastUpdated: str(x, "contract_creation_updated")},
		{Dataset: "contracts", Rows: contracts, LastUpdated: str(x, "contract_updated")},
		{Dataset: "tokens", Rows: tokens, LastUpdated: str(x, "token_updated")},
		{Dataset: "address_activity", Rows: activity, LastUpdated: str(x, "activity_updated")},
		{Dataset: "parsed_events", Rows: parsedEvents, LastUpdated: str(x, "parsed_event_updated")},
		{Dataset: "token_prices", Rows: tokenPrices, LastUpdated: str(x, "token_price_updated")},
	}
	totalRows, err := sumU64(transactions, tokenTransfers, internalTransactions, contractCreations, contracts, tokens, activity, parsedEvents, tokenPrices)
	if err != nil {
		return DataQuality{}, err
	}
	return DataQuality{ChainID: chainID, TotalRows: totalRows,
		Datasets: datasets, Status: status, Method: method, EntityLabel: entity, GeneratedAt: time.Now().UTC()}, nil
}

func (r *Repository) TokenQuality(ctx context.Context, chainID uint32) (TokenQuality, error) {
	if err := r.ready(chainID); err != nil {
		return TokenQuality{}, err
	}
	rows, err := r.query(ctx, fmt.Sprintf(`/* semanticquality:token */ SELECT
(SELECT count() FROM onchain.tokens FINAL WHERE chain_id=%d) AS known_tokens,
(SELECT countIf(is_verified) FROM onchain.tokens FINAL WHERE chain_id=%d) AS verified,
(SELECT countIf(is_spam) FROM onchain.tokens FINAL WHERE chain_id=%d) AS spam_tokens,
(SELECT countIf(symbol='') FROM onchain.tokens FINAL WHERE chain_id=%d) AS missing_symbol,
(SELECT countIf(logo_uri='' OR logo_hash='') FROM onchain.tokens FINAL WHERE chain_id=%d) AS missing_logo,
(SELECT uniqExact(token_address) FROM onchain.token_transfers FINAL WHERE chain_id=%d AND token_address!='') AS transferred_tokens,
(SELECT uniqExact(token_address) FROM onchain.token_transfers FINAL WHERE chain_id=%d AND token_address IN (SELECT contract_address FROM onchain.tokens FINAL WHERE chain_id=%d AND name!='' AND symbol!='')) AS metadata_known,
(SELECT toString(max(ingested_at)) FROM onchain.tokens FINAL WHERE chain_id=%d) AS last_updated`,
		chainID, chainID, chainID, chainID, chainID, chainID, chainID, chainID, chainID))
	if err != nil || len(rows) != 1 {
		return TokenQuality{}, resultError(err)
	}
	x := rows[0]
	known, ok := u64(x, "known_tokens")
	if !ok {
		return TokenQuality{}, ErrInvalidData
	}
	verified, ok := u64(x, "verified")
	if !ok || verified > known {
		return TokenQuality{}, ErrInvalidData
	}
	spam, ok := u64(x, "spam_tokens")
	if !ok {
		return TokenQuality{}, ErrInvalidData
	}
	missingSymbol, ok := u64(x, "missing_symbol")
	if !ok {
		return TokenQuality{}, ErrInvalidData
	}
	missingLogo, ok := u64(x, "missing_logo")
	if !ok {
		return TokenQuality{}, ErrInvalidData
	}
	transferred, ok := u64(x, "transferred_tokens")
	if !ok {
		return TokenQuality{}, ErrInvalidData
	}
	metadataKnown, ok := u64(x, "metadata_known")
	if !ok {
		return TokenQuality{}, ErrInvalidData
	}
	updated := str(x, "last_updated")
	metadata, err := coverage(metadataKnown, transferred, updated, true)
	if err != nil {
		return TokenQuality{}, err
	}
	// decimals is non-nullable UInt8. A registry match proves a decimals value
	// exists, including the valid value zero; absence from the registry is unknown.
	decimals, err := coverage(metadataKnown, transferred, updated, true)
	if err != nil {
		return TokenQuality{}, err
	}
	logo, err := coverage(known-missingLogo, known, updated, true)
	if err != nil {
		return TokenQuality{}, err
	}
	return TokenQuality{ChainID: chainID, KnownTokens: known, Verified: verified, Unverified: known - verified,
		SpamTokens: spam, MissingSymbol: missingSymbol, MissingDecimals: transferred - metadataKnown, MissingLogo: missingLogo,
		Metadata: metadata, Decimals: decimals, Logo: logo, LastUpdated: updated}, nil
}

func (r *Repository) ContractQuality(ctx context.Context, chainID uint32) (ContractQuality, error) {
	if err := r.ready(chainID); err != nil {
		return ContractQuality{}, err
	}
	rows, err := r.query(ctx, fmt.Sprintf(`/* semanticquality:contract */ SELECT count() AS contracts,
countIf(creator_address!='') AS creator_known,countIf(creation_tx_hash!='') AS creation_tx_known,
countIf(is_proxy) AS proxy_detected,countIf(is_proxy AND implementation_address!='') AS implementation_known,
countIf(abi_json!='' AND abi_json!='[]' AND lowerUTF8(abi_json)!='null') AS abi_known,
countIf(is_verified) AS verified,countIf(token_standard!='') AS token_detected,
toString(max(ingested_at)) AS last_updated
FROM onchain.contracts FINAL WHERE chain_id=%d`, chainID))
	if err != nil || len(rows) != 1 {
		return ContractQuality{}, resultError(err)
	}
	x := rows[0]
	total, ok := u64(x, "contracts")
	if !ok {
		return ContractQuality{}, ErrInvalidData
	}
	creator, ok := u64(x, "creator_known")
	if !ok {
		return ContractQuality{}, ErrInvalidData
	}
	creationTx, ok := u64(x, "creation_tx_known")
	if !ok {
		return ContractQuality{}, ErrInvalidData
	}
	proxy, ok := u64(x, "proxy_detected")
	if !ok {
		return ContractQuality{}, ErrInvalidData
	}
	implementation, ok := u64(x, "implementation_known")
	if !ok {
		return ContractQuality{}, ErrInvalidData
	}
	abi, ok := u64(x, "abi_known")
	if !ok {
		return ContractQuality{}, ErrInvalidData
	}
	verified, ok := u64(x, "verified")
	if !ok {
		return ContractQuality{}, ErrInvalidData
	}
	token, ok := u64(x, "token_detected")
	if !ok {
		return ContractQuality{}, ErrInvalidData
	}
	updated := str(x, "last_updated")
	creatorCoverage, e := coverage(creator, total, updated, true)
	if e != nil {
		return ContractQuality{}, e
	}
	creationCoverage, e := coverage(creationTx, total, updated, true)
	if e != nil {
		return ContractQuality{}, e
	}
	abiCoverage, e := coverage(abi, total, updated, true)
	if e != nil {
		return ContractQuality{}, e
	}
	if proxy > total || implementation > proxy || verified > total || token > total {
		return ContractQuality{}, ErrInvalidData
	}
	return ContractQuality{ChainID: chainID, Contracts: total, Creator: creatorCoverage, CreationTransaction: creationCoverage,
		ProxyDetected: proxy, ImplementationKnown: implementation, ABI: abiCoverage, Verified: verified, TokenDetected: token, LastUpdated: updated}, nil
}

func (r *Repository) DecoderQuality(ctx context.Context, chainID uint32) (DecoderQuality, error) {
	if err := r.ready(chainID); err != nil {
		return DecoderQuality{}, err
	}
	rows, err := r.query(ctx, fmt.Sprintf(`/* semanticquality:decoder */ SELECT
(SELECT count() FROM onchain.chain_transactions FINAL WHERE chain_id=%d AND length(trimBoth(input))>2) AS transactions_with_input,
(SELECT count() FROM onchain.chain_transactions FINAL WHERE chain_id=%d AND length(trimBoth(input))>2 AND method_name!='' AND lowerUTF8(method_name) NOT IN ('unknown','ambiguous') AND method_confidence!='') AS known_method,
(SELECT count() FROM onchain.parsed_events FINAL WHERE chain_id=%d) AS indexed_events,
(SELECT countIf(event_name!='' AND event_signature!='' AND decoder_source!='') FROM onchain.parsed_events FINAL WHERE chain_id=%d) AS decoded_events,
(SELECT countIf(lowerUTF8(decoder_confidence)='failed') FROM onchain.parsed_events FINAL WHERE chain_id=%d) AS abi_decode_failures,
greatest((SELECT toString(max(ingested_at)) FROM onchain.chain_transactions FINAL WHERE chain_id=%d),(SELECT toString(max(ingested_at)) FROM onchain.parsed_events FINAL WHERE chain_id=%d)) AS last_updated`, chainID, chainID, chainID, chainID, chainID, chainID, chainID))
	if err != nil || len(rows) != 1 {
		return DecoderQuality{}, resultError(err)
	}
	x := rows[0]
	input, ok := u64(x, "transactions_with_input")
	if !ok {
		return DecoderQuality{}, ErrInvalidData
	}
	known, ok := u64(x, "known_method")
	if !ok {
		return DecoderQuality{}, ErrInvalidData
	}
	events, ok := u64(x, "indexed_events")
	if !ok {
		return DecoderQuality{}, ErrInvalidData
	}
	decoded, ok := u64(x, "decoded_events")
	if !ok {
		return DecoderQuality{}, ErrInvalidData
	}
	decodeFailures, ok := u64(x, "abi_decode_failures")
	if !ok || decodeFailures > events {
		return DecoderQuality{}, ErrInvalidData
	}
	updated := str(x, "last_updated")
	method, e := coverage(known, input, updated, true)
	if e != nil {
		return DecoderQuality{}, e
	}
	eventCoverage, e := coverage(decoded, events, updated, true)
	if e != nil {
		return DecoderQuality{}, e
	}
	return DecoderQuality{ChainID: chainID, TransactionsWithInput: input, KnownMethod: known, UnknownMethod: input - known, IndexedEvents: events, DecodedEvents: decoded, UnknownTopic0: events - decoded, ABIDecodeFailures: decodeFailures, Method: method, Events: eventCoverage, Scope: "canonical_parsed_events", LastUpdated: updated}, nil
}

func (r *Repository) PriceQuality(ctx context.Context, chainID uint32) (PriceQuality, error) {
	if err := r.ready(chainID); err != nil {
		return PriceQuality{}, err
	}
	rows, err := r.query(ctx, fmt.Sprintf(`/* semanticquality:price */ SELECT count() AS required,
countIf(usd_price IS NOT NULL) AS priced,
countIf(usd_price IS NOT NULL AND price_time IS NOT NULL AND price_source!='' AND upperUTF8(price_source) NOT IN ('PEG_FALLBACK','FALLBACK','CURRENT')) AS historical,
countIf(usd_price IS NOT NULL AND upperUTF8(price_source) IN ('PEG_FALLBACK','FALLBACK')) AS fallback,
toString(max(ingested_at)) AS last_updated FROM onchain.token_transfers FINAL WHERE chain_id=%d`, chainID))
	if err != nil || len(rows) != 1 {
		return PriceQuality{}, resultError(err)
	}
	x := rows[0]
	required, ok := u64(x, "required")
	if !ok {
		return PriceQuality{}, ErrInvalidData
	}
	priced, ok := u64(x, "priced")
	if !ok {
		return PriceQuality{}, ErrInvalidData
	}
	historicalCount, ok := u64(x, "historical")
	if !ok {
		return PriceQuality{}, ErrInvalidData
	}
	fallback, ok := u64(x, "fallback")
	if !ok || fallback > priced {
		return PriceQuality{}, ErrInvalidData
	}
	updated := str(x, "last_updated")
	pricedCoverage, e := coverage(priced, required, updated, true)
	if e != nil {
		return PriceQuality{}, e
	}
	historical, e := coverage(historicalCount, required, updated, true)
	if e != nil {
		return PriceQuality{}, e
	}
	return PriceQuality{ChainID: chainID, TransfersRequiringPrice: required, Priced: priced, HistoricalPrice: historicalCount, FallbackPrice: fallback, NoPrice: required - priced, PriceCoverage: pricedCoverage, HistoricalPriceCoverage: historical, PriceProvenanceAvailable: true, LastUpdated: updated}, nil
}

func completeness(chainID uint32, generated time.Time, data DataQuality, token TokenQuality, contract ContractQuality, decoder DecoderQuality, price PriceQuality) (SemanticCompleteness, error) {
	metrics := []Coverage{data.Status, data.Method, token.Metadata, token.Decimals, token.Logo, contract.Creator, contract.ABI, data.EntityLabel, decoder.Events, price.HistoricalPriceCoverage}
	var numerator, denominator uint64
	for _, m := range metrics {
		if math.MaxUint64-denominator < m.Denominator || math.MaxUint64-numerator < m.Numerator {
			return SemanticCompleteness{}, ErrInvalidData
		}
		denominator += m.Denominator
		numerator += m.Numerator
	}
	overall, e := coverage(numerator, denominator, latest(metrics...), true)
	if e != nil {
		return SemanticCompleteness{}, e
	}
	return SemanticCompleteness{ChainID: chainID, Overall: overall, Status: data.Status, Method: data.Method, TokenMetadata: token.Metadata, TokenDecimals: token.Decimals, TokenLogo: token.Logo, ContractCreator: contract.Creator, ContractABI: contract.ABI, EntityLabel: data.EntityLabel, EventDecode: decoder.Events, HistoricalPrice: price.HistoricalPriceCoverage, GeneratedAt: generated}, nil
}

func (r *Repository) ready(chainID uint32) error {
	if err := validateChain(chainID); err != nil {
		return err
	}
	if r == nil || r.client == nil {
		return ErrQueryFailed
	}
	return nil
}
func validateChain(chainID uint32) error {
	if _, ok := supportedChains[chainID]; !ok {
		return fmt.Errorf("%w: unsupported chain_id", ErrInvalidInput)
	}
	return nil
}
func (r *Repository) query(ctx context.Context, q string) ([]map[string]any, error) {
	rows, err := r.client.QueryJSON(ctx, q)
	if err != nil {
		return nil, ErrQueryFailed
	}
	return rows, nil
}
func resultError(err error) error {
	if err != nil {
		return ErrQueryFailed
	}
	return ErrInvalidData
}
func coverage(numerator, denominator uint64, updated string, available bool) (Coverage, error) {
	if numerator > denominator {
		return Coverage{}, ErrInvalidData
	}
	pct := 0.0
	if denominator > 0 {
		pct = float64(numerator) * 100 / float64(denominator)
	}
	return Coverage{Numerator: numerator, Denominator: denominator, Percentage: pct, Unknown: denominator - numerator, Available: available, LastUpdated: updated}, nil
}
func u64(row map[string]any, key string) (uint64, bool) {
	v, ok := row[key]
	if !ok {
		return 0, false
	}
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case json.Number:
		s = x.String()
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || x < 0 || math.Trunc(x) != x {
			return 0, false
		}
		s = strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return 0, false
	}
	n, e := strconv.ParseUint(s, 10, 64)
	return n, e == nil
}
func str(row map[string]any, key string) string {
	v, ok := row[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	if strings.HasPrefix(s, "1970-01-01") || strings.HasPrefix(s, "0000-00-00") {
		return ""
	}
	return s
}
func latest(metrics ...Coverage) string {
	latest := ""
	for _, m := range metrics {
		if m.LastUpdated > latest {
			latest = m.LastUpdated
		}
	}
	return latest
}

func sumU64(values ...uint64) (uint64, error) {
	var total uint64
	for _, value := range values {
		if math.MaxUint64-total < value {
			return 0, ErrInvalidData
		}
		total += value
	}
	return total, nil
}
