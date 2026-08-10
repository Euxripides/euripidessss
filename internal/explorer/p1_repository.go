package explorer

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	defaultStatsLimit = 50
	maxStatsLimit     = 366
)

// ExecuteClient is implemented by the ClickHouse client. It is kept separate
// from QueryClient so read-only repository tests and deployments remain valid.
type ExecuteClient interface {
	Exec(ctx context.Context, query string) error
}

// RefreshAddressAnalytics replaces the logical aggregate versions for one
// address. ReplacingMergeTree plus updated_at makes repeated refreshes
// idempotent; all source reads use FINAL so duplicate physical versions cannot
// inflate counts.
func (r *Repository) RefreshAddressAnalytics(ctx context.Context, chainID uint32, address string) error {
	address, err := validateScope(chainID, address)
	if err != nil {
		return err
	}
	executor, ok := r.client.(ExecuteClient)
	if !ok || executor == nil {
		return ErrQueryFailed
	}
	for _, query := range addressRefreshQueries(chainID, address) {
		if err := executor.Exec(ctx, query); err != nil {
			return ErrQueryFailed
		}
	}
	return nil
}

func addressRefreshQueries(chainID uint32, address string) []string {
	scope := fmt.Sprintf("chain_id = %d AND address = '%s'", chainID, address)
	nativeTypes := "('NATIVE_TRANSFER','CONTRACT_CALL','INTERNAL_TRANSFER','INTERNAL_TRANSACTION')"
	return []string{
		fmt.Sprintf(`INSERT INTO onchain.address_summary
(chain_id,address,address_type,first_seen_time,last_seen_time,tx_count,in_tx_count,out_tx_count,
token_transfer_count,internal_tx_count,nft_transfer_count,contract_created_count,unique_counterparty_count,
native_received,native_sent,native_netflow,usd_received,usd_sent,usd_netflow,active_days,max_single_in_usd,
max_single_out_usd,top_counterparty,cex_interaction_count,dex_interaction_count,bridge_interaction_count,risk_score,updated_at)
SELECT chain_id,address,'ADDRESS',min(block_time),max(block_time),
uniqExactIf(tx_hash,activity_type IN ('NATIVE_TRANSFER','CONTRACT_CALL')),
uniqExactIf(tx_hash,direction='IN' AND activity_type IN ('NATIVE_TRANSFER','CONTRACT_CALL')),
uniqExactIf(tx_hash,direction='OUT' AND activity_type IN ('NATIVE_TRANSFER','CONTRACT_CALL')),
countIf(activity_type IN ('TOKEN_TRANSFER','ERC20_TRANSFER','ERC721_TRANSFER','ERC1155_TRANSFER')),
countIf(activity_type IN ('INTERNAL_TRANSFER','INTERNAL_TRANSACTION')),
countIf(activity_type IN ('ERC721_TRANSFER','ERC1155_TRANSFER')),
countIf(activity_type IN ('CONTRACT_CREATE','CONTRACT_CREATION') AND direction='OUT'),
uniqExactIf(counterparty_address,counterparty_address!=''),
sumIf(amount,direction='IN' AND activity_type IN %s),sumIf(amount,direction='OUT' AND activity_type IN %s),
sumIf(amount,direction='IN' AND activity_type IN %s)-sumIf(amount,direction='OUT' AND activity_type IN %s),
sumIf(ifNull(usd_value,0),direction='IN'),sumIf(ifNull(usd_value,0),direction='OUT'),
sumIf(ifNull(usd_value,0),direction='IN')-sumIf(ifNull(usd_value,0),direction='OUT'),
toUInt32(uniqExact(toDate(block_time))),maxIf(ifNull(usd_value,0),direction='IN'),
maxIf(ifNull(usd_value,0),direction='OUT'),argMax(counterparty_address,block_time),0,0,0,0,now64(3)
FROM onchain.address_activity FINAL WHERE %s GROUP BY chain_id,address`, nativeTypes, nativeTypes, nativeTypes, nativeTypes, scope),
		fmt.Sprintf(`INSERT INTO onchain.address_counterparty_stats
(chain_id,address,counterparty_address,direction,activity_count,tx_count,native_amount,usd_value,first_seen_time,last_seen_time,updated_at)
SELECT chain_id,address,counterparty_address,direction,count(),uniqExact(tx_hash),
sumIf(amount,activity_type IN %s),sum(ifNull(usd_value,0)),min(block_time),max(block_time),now64(3)
FROM onchain.address_activity FINAL WHERE %s AND counterparty_address!=''
GROUP BY chain_id,address,counterparty_address,direction`, nativeTypes, scope),
		fmt.Sprintf(`INSERT INTO onchain.address_daily_stats
(chain_id,address,activity_date,in_count,out_count,in_native_amount,out_native_amount,native_netflow,
in_usd_value,out_usd_value,usd_netflow,unique_counterparty_count,updated_at)
SELECT chain_id,address,toDate(block_time) AS activity_date,countIf(direction='IN'),countIf(direction='OUT'),
sumIf(amount,direction='IN' AND activity_type IN %s),sumIf(amount,direction='OUT' AND activity_type IN %s),
sumIf(amount,direction='IN' AND activity_type IN %s)-sumIf(amount,direction='OUT' AND activity_type IN %s),
sumIf(ifNull(usd_value,0),direction='IN'),sumIf(ifNull(usd_value,0),direction='OUT'),
sumIf(ifNull(usd_value,0),direction='IN')-sumIf(ifNull(usd_value,0),direction='OUT'),
uniqExactIf(counterparty_address,counterparty_address!=''),now64(3)
FROM onchain.address_activity FINAL WHERE %s GROUP BY chain_id,address,activity_date`, nativeTypes, nativeTypes, nativeTypes, nativeTypes, scope),
	}
}

func (r *Repository) GetCounterpartyStats(ctx context.Context, chainID uint32, address string, limit int) ([]CounterpartyStat, error) {
	address, err := validateScope(chainID, address)
	if err != nil {
		return nil, err
	}
	limit, err = boundedStatsLimit(limit)
	if err != nil {
		return nil, err
	}
	if r == nil || r.client == nil {
		return nil, ErrQueryFailed
	}
	query := fmt.Sprintf(`SELECT chain_id,address,counterparty_address,direction,activity_count,tx_count,
toString(native_amount) AS native_amount_text,toString(usd_value) AS usd_value_text,first_seen_time,last_seen_time
FROM onchain.address_counterparty_stats FINAL
WHERE chain_id=%d AND address='%s'
AND updated_at=(SELECT max(updated_at) FROM onchain.address_counterparty_stats FINAL WHERE chain_id=%d AND address='%s')
ORDER BY abs(usd_value) DESC, activity_count DESC, counterparty_address ASC, direction ASC LIMIT %d`, chainID, address, chainID, address, limit)
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, ErrQueryFailed
	}
	out := make([]CounterpartyStat, 0, len(rows))
	for _, row := range rows {
		item, err := decodeCounterpartyStat(row)
		if err != nil || item.ChainID != chainID || strings.ToLower(item.Address) != address || !evmAddressPattern.MatchString(strings.ToLower(item.CounterpartyAddress)) {
			return nil, ErrInvalidData
		}
		item.Address = address
		item.CounterpartyAddress = strings.ToLower(item.CounterpartyAddress)
		out = append(out, item)
	}
	return out, nil
}

func (r *Repository) GetDailyStats(ctx context.Context, input DailyStatsQuery) ([]DailyStat, error) {
	address, err := validateScope(input.ChainID, input.Address)
	if err != nil {
		return nil, err
	}
	limit, err := boundedStatsLimit(input.Limit)
	if err != nil {
		return nil, err
	}
	if !input.From.IsZero() && !input.To.IsZero() && input.From.After(input.To) {
		return nil, fmt.Errorf("%w: from must not be after to", ErrInvalidInput)
	}
	where := []string{fmt.Sprintf("chain_id=%d", input.ChainID), fmt.Sprintf("address='%s'", address)}
	if !input.From.IsZero() {
		where = append(where, fmt.Sprintf("activity_date >= toDate('%s')", input.From.UTC().Format("2006-01-02")))
	}
	if !input.To.IsZero() {
		where = append(where, fmt.Sprintf("activity_date <= toDate('%s')", input.To.UTC().Format("2006-01-02")))
	}
	whereSQL := strings.Join(where, " AND ")
	query := fmt.Sprintf(`SELECT chain_id,address,activity_date,in_count,out_count,
toString(in_native_amount) AS in_native_amount,toString(out_native_amount) AS out_native_amount,
toString(native_netflow) AS native_netflow,toString(in_usd_value) AS in_usd_value,
toString(out_usd_value) AS out_usd_value,toString(usd_netflow) AS usd_netflow,unique_counterparty_count
FROM onchain.address_daily_stats FINAL WHERE %s
AND updated_at=(SELECT max(updated_at) FROM onchain.address_daily_stats FINAL WHERE chain_id=%d AND address='%s')
ORDER BY activity_date DESC LIMIT %d`, whereSQL, input.ChainID, address, limit)
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, ErrQueryFailed
	}
	out := make([]DailyStat, 0, len(rows))
	for _, row := range rows {
		item, err := decodeDailyStat(row)
		if err != nil || item.ChainID != input.ChainID || strings.ToLower(item.Address) != address {
			return nil, ErrInvalidData
		}
		item.Address = address
		out = append(out, item)
	}
	return out, nil
}

func (r *Repository) GetTokenMetadata(ctx context.Context, chainID uint32, address string) (TokenMetadata, error) {
	address, err := validateScope(chainID, address)
	if err != nil {
		return TokenMetadata{}, err
	}
	query := fmt.Sprintf(`SELECT chain_id,contract_address,name,symbol,decimals,token_standard,logo_uri,logo_source,
official_website,is_verified,is_spam,first_seen_block,first_seen_time,last_metadata_refresh_at
FROM onchain.tokens FINAL WHERE chain_id=%d AND contract_address='%s' ORDER BY ingested_at DESC LIMIT 1`, chainID, address)
	rows, err := r.query(ctx, query)
	if err != nil {
		return TokenMetadata{}, err
	}
	if len(rows) == 0 {
		query = fmt.Sprintf(`SELECT chain_id,token_address AS contract_address,argMax(token_name,block_time) AS name,
argMax(token_symbol,block_time) AS symbol,argMax(token_decimals,block_time) AS decimals,
argMax(token_standard,block_time) AS token_standard,'' AS logo_uri,'' AS logo_source,'' AS official_website,
false AS is_verified,false AS is_spam,min(block_number) AS first_seen_block,min(block_time) AS first_seen_time,
max(ingested_at) AS last_metadata_refresh_at
FROM onchain.token_transfers FINAL WHERE chain_id=%d AND token_address='%s' GROUP BY chain_id,token_address`, chainID, address)
		rows, err = r.query(ctx, query)
		if err != nil {
			return TokenMetadata{}, err
		}
		if len(rows) == 0 {
			return TokenMetadata{}, ErrNotFound
		}
	}
	out, err := decodeTokenMetadata(rows[0])
	if err != nil || out.ChainID != chainID || strings.ToLower(out.ContractAddress) != address {
		return TokenMetadata{}, ErrInvalidData
	}
	out.ContractAddress = address
	return out, nil
}

func (r *Repository) GetTransactionDetail(ctx context.Context, chainID uint32, txHash string) (TransactionDetail, error) {
	if _, ok := supportedChains[chainID]; !ok {
		return TransactionDetail{}, fmt.Errorf("%w: unsupported chain_id", ErrInvalidInput)
	}
	txHash = strings.ToLower(strings.TrimSpace(txHash))
	if !txHashPattern.MatchString(txHash) {
		return TransactionDetail{}, fmt.Errorf("%w: invalid transaction hash", ErrInvalidInput)
	}
	query := fmt.Sprintf(`SELECT chain_id,block_number,block_hash,block_time,transaction_index,tx_hash,from_address,to_address,
nonce,value_raw,toString(value_decimal) AS value_decimal,native_symbol,input,method_id,method_name,tx_type,
gas_limit,gas_used,toString(transaction_fee_native) AS transaction_fee_native,
if(isNull(transaction_fee_usd),NULL,toString(transaction_fee_usd)) AS transaction_fee_usd,status,
is_contract_creation,created_contract_address,error_message,source_provider
FROM onchain.chain_transactions FINAL WHERE chain_id=%d AND tx_hash='%s' ORDER BY ingested_at DESC LIMIT 1`, chainID, txHash)
	rows, err := r.query(ctx, query)
	if err != nil {
		return TransactionDetail{}, err
	}
	if len(rows) == 0 {
		return TransactionDetail{}, ErrNotFound
	}
	out, err := decodeTransactionDetail(rows[0])
	if err != nil || out.ChainID != chainID || out.TransactionHash != txHash {
		return TransactionDetail{}, ErrInvalidData
	}
	return out, nil
}

func (r *Repository) GetContractDetail(ctx context.Context, chainID uint32, address string) (ContractDetail, error) {
	address, err := validateScope(chainID, address)
	if err != nil {
		return ContractDetail{}, err
	}
	query := fmt.Sprintf(`SELECT chain_id,contract_address,creator_address,creation_tx_hash,creation_block,creation_time,
bytecode_hash,runtime_bytecode_hash,contract_name,is_verified,is_proxy,proxy_type,implementation_address,
abi_json,token_standard,first_seen,last_seen,risk_flags
FROM onchain.contracts FINAL WHERE chain_id=%d AND contract_address='%s' ORDER BY ingested_at DESC LIMIT 1`, chainID, address)
	rows, err := r.query(ctx, query)
	if err != nil {
		return ContractDetail{}, err
	}
	if len(rows) == 0 {
		query = fmt.Sprintf(`SELECT chain_id,contract_address,creator_address,tx_hash AS creation_tx_hash,
block_number AS creation_block,block_time AS creation_time,init_code_hash AS bytecode_hash,runtime_code_hash AS runtime_bytecode_hash,
contract_name,source_verified AS is_verified,is_proxy,proxy_type,implementation_address,'' AS abi_json,token_standard,
block_time AS first_seen,block_time AS last_seen,emptyArrayString() AS risk_flags
FROM onchain.contract_creations FINAL WHERE chain_id=%d AND contract_address='%s' ORDER BY ingested_at DESC LIMIT 1`, chainID, address)
		rows, err = r.query(ctx, query)
		if err != nil {
			return ContractDetail{}, err
		}
		if len(rows) == 0 {
			return ContractDetail{}, ErrNotFound
		}
	}
	out, err := decodeContractDetail(rows[0])
	if err != nil || out.ChainID != chainID || strings.ToLower(out.ContractAddress) != address {
		return ContractDetail{}, ErrInvalidData
	}
	out.ContractAddress = address
	return out, nil
}

func (r *Repository) query(ctx context.Context, query string) ([]map[string]any, error) {
	if r == nil || r.client == nil {
		return nil, ErrQueryFailed
	}
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, ErrQueryFailed
	}
	return rows, nil
}

func boundedStatsLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultStatsLimit, nil
	}
	if limit < 1 || limit > maxStatsLimit {
		return 0, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maxStatsLimit)
	}
	return limit, nil
}

func decodeCounterpartyStat(row map[string]any) (CounterpartyStat, error) {
	var out CounterpartyStat
	var err error
	if out.ChainID, err = asUint32(row["chain_id"]); err != nil {
		return out, err
	}
	if out.Address, err = asString(row["address"]); err != nil {
		return out, err
	}
	if out.CounterpartyAddress, err = asString(row["counterparty_address"]); err != nil {
		return out, err
	}
	out.Direction, _ = asString(row["direction"])
	if out.ActivityCount, err = asUint64(row["activity_count"]); err != nil {
		return out, err
	}
	if out.TransactionCount, err = asUint64(row["tx_count"]); err != nil {
		return out, err
	}
	out.NativeAmount, _ = asString(row["native_amount_text"])
	out.USDValue, _ = asString(row["usd_value_text"])
	if out.FirstSeenTime, err = asTime(row["first_seen_time"]); err != nil {
		return out, err
	}
	if out.LastSeenTime, err = asTime(row["last_seen_time"]); err != nil {
		return out, err
	}
	return out, nil
}

func decodeDailyStat(row map[string]any) (DailyStat, error) {
	var out DailyStat
	var err error
	if out.ChainID, err = asUint32(row["chain_id"]); err != nil {
		return out, err
	}
	if out.Address, err = asString(row["address"]); err != nil {
		return out, err
	}
	if out.Date, err = asTime(row["activity_date"]); err != nil {
		return out, err
	}
	out.IncomingCount, _ = asUint64(row["in_count"])
	out.OutgoingCount, _ = asUint64(row["out_count"])
	out.IncomingNativeAmount, _ = asString(row["in_native_amount"])
	out.OutgoingNativeAmount, _ = asString(row["out_native_amount"])
	out.NativeNetflow, _ = asString(row["native_netflow"])
	out.IncomingUSDValue, _ = asString(row["in_usd_value"])
	out.OutgoingUSDValue, _ = asString(row["out_usd_value"])
	out.USDNetflow, _ = asString(row["usd_netflow"])
	out.UniqueCounterparties, _ = asUint64(row["unique_counterparty_count"])
	return out, nil
}

func decodeTokenMetadata(row map[string]any) (TokenMetadata, error) {
	var out TokenMetadata
	var err error
	if out.ChainID, err = asUint32(row["chain_id"]); err != nil {
		return out, err
	}
	if out.ContractAddress, err = asString(row["contract_address"]); err != nil {
		return out, err
	}
	out.Name, _ = asString(row["name"])
	out.Symbol, _ = asString(row["symbol"])
	decimals, err := asUint64(row["decimals"])
	if err != nil || decimals > 255 {
		return out, errors.New("invalid decimals")
	}
	out.Decimals = uint8(decimals)
	out.TokenStandard, _ = asString(row["token_standard"])
	out.LogoURI, _ = asString(row["logo_uri"])
	out.LogoSource, _ = asString(row["logo_source"])
	out.OfficialWebsite, _ = asString(row["official_website"])
	out.Verified, _ = asBool(row["is_verified"])
	out.Spam, _ = asBool(row["is_spam"])
	out.FirstSeenBlock, _ = asUint64(row["first_seen_block"])
	if out.FirstSeenTime, err = asTime(row["first_seen_time"]); err != nil {
		return out, err
	}
	if out.LastMetadataRefresh, err = asTime(row["last_metadata_refresh_at"]); err != nil {
		return out, err
	}
	return out, nil
}

func decodeTransactionDetail(row map[string]any) (TransactionDetail, error) {
	var out TransactionDetail
	var err error
	if out.ChainID, err = asUint32(row["chain_id"]); err != nil {
		return out, err
	}
	if out.BlockNumber, err = asUint64(row["block_number"]); err != nil {
		return out, err
	}
	out.BlockHash, _ = asString(row["block_hash"])
	if out.BlockTime, err = asTime(row["block_time"]); err != nil {
		return out, err
	}
	out.TransactionIndex, _ = asUint32(row["transaction_index"])
	if out.TransactionHash, err = asString(row["tx_hash"]); err != nil || !txHashPattern.MatchString(strings.ToLower(out.TransactionHash)) {
		return out, errors.New("invalid transaction hash")
	}
	out.TransactionHash = strings.ToLower(out.TransactionHash)
	out.FromAddress, _ = asString(row["from_address"])
	out.ToAddress, _ = asString(row["to_address"])
	out.Nonce, _ = asUint64(row["nonce"])
	out.ValueRaw, _ = asString(row["value_raw"])
	out.ValueDecimal, _ = asString(row["value_decimal"])
	out.NativeSymbol, _ = asString(row["native_symbol"])
	out.Input, _ = asString(row["input"])
	out.MethodID, _ = asString(row["method_id"])
	out.MethodName, _ = asString(row["method_name"])
	out.TransactionType, _ = asString(row["tx_type"])
	out.GasLimit, _ = asUint64(row["gas_limit"])
	out.GasUsed, _ = asUint64(row["gas_used"])
	out.TransactionFeeNative, _ = asString(row["transaction_fee_native"])
	if row["transaction_fee_usd"] != nil {
		value, convErr := asString(row["transaction_fee_usd"])
		if convErr != nil {
			return out, convErr
		}
		out.TransactionFeeUSD = &value
	}
	out.Status, _ = asString(row["status"])
	out.ContractCreation, _ = asBool(row["is_contract_creation"])
	out.CreatedContract, _ = asString(row["created_contract_address"])
	out.ErrorMessage, _ = asString(row["error_message"])
	out.SourceProvider, _ = asString(row["source_provider"])
	return out, nil
}

func decodeContractDetail(row map[string]any) (ContractDetail, error) {
	var out ContractDetail
	var err error
	if out.ChainID, err = asUint32(row["chain_id"]); err != nil {
		return out, err
	}
	if out.ContractAddress, err = asString(row["contract_address"]); err != nil {
		return out, err
	}
	out.CreatorAddress, _ = asString(row["creator_address"])
	out.CreationTxHash, _ = asString(row["creation_tx_hash"])
	out.CreationBlock, _ = asUint64(row["creation_block"])
	if out.CreationTime, err = asTime(row["creation_time"]); err != nil {
		return out, err
	}
	out.BytecodeHash, _ = asString(row["bytecode_hash"])
	out.RuntimeBytecodeHash, _ = asString(row["runtime_bytecode_hash"])
	out.ContractName, _ = asString(row["contract_name"])
	out.Verified, _ = asBool(row["is_verified"])
	out.Proxy, _ = asBool(row["is_proxy"])
	out.ProxyType, _ = asString(row["proxy_type"])
	out.ImplementationAddress, _ = asString(row["implementation_address"])
	out.ABIJSON, _ = asString(row["abi_json"])
	out.TokenStandard, _ = asString(row["token_standard"])
	if out.FirstSeen, err = asTime(row["first_seen"]); err != nil {
		return out, err
	}
	if out.LastSeen, err = asTime(row["last_seen"]); err != nil {
		return out, err
	}
	if values, ok := row["risk_flags"].([]any); ok {
		for _, value := range values {
			if text, convErr := asString(value); convErr == nil {
				out.RiskFlags = append(out.RiskFlags, text)
			}
		}
	} else if values, ok := row["risk_flags"].([]string); ok {
		out.RiskFlags = append([]string(nil), values...)
	}
	return out, nil
}

func asBool(value any) (bool, error) {
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case string:
		switch strings.ToLower(typed) {
		case "1", "true":
			return true, nil
		case "0", "false":
			return false, nil
		}
	case float64:
		if typed == 0 {
			return false, nil
		}
		if typed == 1 {
			return true, nil
		}
	}
	return false, errors.New("value is not bool")
}
