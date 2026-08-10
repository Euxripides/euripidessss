package financialanalytics

import (
	"context"
	"fmt"
	"strings"
)

const priceBasis = "stored_historical_usd_only; missing_price_is_null"

type QueryClient interface {
	QueryJSON(context.Context, string) ([]map[string]any, error)
}

type Repository struct{ client QueryClient }

func NewRepository(client QueryClient) *Repository { return &Repository{client: client} }

// AddressUSDFlow is the compact flow contract used by Explorer and Graph.
// FinancialSummary remains the authoritative response when coverage context is
// required by an investigator.
func (r *Repository) AddressUSDFlow(ctx context.Context, input Query) (AddressUSDFlow, error) {
	summary, err := r.FinancialSummary(ctx, input)
	if err != nil {
		return AddressUSDFlow{}, err
	}
	return summary.Flow, nil
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

func (r *Repository) FinancialSummary(ctx context.Context, input Query) (FinancialSummary, error) {
	q, err := validateQuery(input)
	if err != nil {
		return FinancialSummary{}, err
	}
	where := activityWhere(q)
	rows, err := r.query(ctx, fmt.Sprintf(`WITH latest_tokens AS (
 SELECT chain_id,contract_address,argMax(symbol,updated_at) symbol,argMax(is_verified,updated_at) verified,argMax(metadata_confidence,updated_at) confidence
 FROM onchain.token_metadata_registry FINAL GROUP BY chain_id,contract_address), facts AS (
 SELECT a.*,if(t.verified AND upper(t.confidence)='HIGH' AND upper(t.symbol) IN ('USDT','USDC','DAI','FDUSD','TUSD'),1,0) stable
	 FROM onchain.address_activity AS a FINAL LEFT JOIN latest_tokens t ON a.chain_id=t.chain_id AND a.token_address=t.contract_address WHERE %s AND a.direction IN ('IN','OUT') AND a.amount!=0)
SELECT
 if(countIf(direction='IN' AND usd_value IS NOT NULL)=0,NULL,toString(sumIf(usd_value,direction='IN' AND usd_value IS NOT NULL))) total_in_usd,
 if(countIf(direction='OUT' AND usd_value IS NOT NULL)=0,NULL,toString(sumIf(usd_value,direction='OUT' AND usd_value IS NOT NULL))) total_out_usd,
 if(countIf(usd_value IS NOT NULL)=0,NULL,toString(sumIf(usd_value,direction='IN' AND usd_value IS NOT NULL)-sumIf(usd_value,direction='OUT' AND usd_value IS NOT NULL))) netflow_usd,
 if(countIf(direction='IN' AND token_address='' AND usd_value IS NOT NULL)=0,NULL,toString(sumIf(usd_value,direction='IN' AND token_address='' AND usd_value IS NOT NULL))) native_in_usd,
 if(countIf(direction='OUT' AND token_address='' AND usd_value IS NOT NULL)=0,NULL,toString(sumIf(usd_value,direction='OUT' AND token_address='' AND usd_value IS NOT NULL))) native_out_usd,
 if(countIf(direction='IN' AND stable=1 AND usd_value IS NOT NULL)=0,NULL,toString(sumIf(usd_value,direction='IN' AND stable=1 AND usd_value IS NOT NULL))) stablecoin_in_usd,
 if(countIf(direction='OUT' AND stable=1 AND usd_value IS NOT NULL)=0,NULL,toString(sumIf(usd_value,direction='OUT' AND stable=1 AND usd_value IS NOT NULL))) stablecoin_out_usd,
 if(countIf(direction='IN' AND token_address!='' AND stable=0 AND usd_value IS NOT NULL)=0,NULL,toString(sumIf(usd_value,direction='IN' AND token_address!='' AND stable=0 AND usd_value IS NOT NULL))) token_in_usd,
 if(countIf(direction='OUT' AND token_address!='' AND stable=0 AND usd_value IS NOT NULL)=0,NULL,toString(sumIf(usd_value,direction='OUT' AND token_address!='' AND stable=0 AND usd_value IS NOT NULL))) token_out_usd,
 if(countIf(direction='IN' AND usd_value IS NOT NULL)=0,NULL,toString(maxIf(usd_value,direction='IN' AND usd_value IS NOT NULL))) largest_in_usd,
 if(countIf(direction='OUT' AND usd_value IS NOT NULL)=0,NULL,toString(maxIf(usd_value,direction='OUT' AND usd_value IS NOT NULL))) largest_out_usd,
 if(countIf(direction='IN' AND usd_value IS NOT NULL)=0,NULL,toString(avgIf(usd_value,direction='IN' AND usd_value IS NOT NULL))) average_in_usd,
 if(countIf(direction='OUT' AND usd_value IS NOT NULL)=0,NULL,toString(avgIf(usd_value,direction='OUT' AND usd_value IS NOT NULL))) average_out_usd,
 if(countIf(direction='IN' AND usd_value IS NOT NULL)=0,NULL,toString(quantileExactIf(0.5)(usd_value,direction='IN' AND usd_value IS NOT NULL))) median_in_usd,
 if(countIf(direction='OUT' AND usd_value IS NOT NULL)=0,NULL,toString(quantileExactIf(0.5)(usd_value,direction='OUT' AND usd_value IS NOT NULL))) median_out_usd,
 if(countIf(direction='IN')=0,'',toString(minIf(block_time,direction='IN'))) first_funding,
 if(countIf(direction='IN')=0,'',toString(maxIf(block_time,direction='IN'))) latest_funding,
 countIf(direction='IN' AND usd_value>=toDecimal128('%s',18)) large_in_count,
 countIf(direction='OUT' AND usd_value>=toDecimal128('%s',18)) large_out_count,
 if(countIf(direction='IN' AND usd_value>=toDecimal128('%s',18))=0,NULL,toString(sumIf(usd_value,direction='IN' AND usd_value>=toDecimal128('%s',18)))) large_in_usd,
 if(countIf(direction='OUT' AND usd_value>=toDecimal128('%s',18))=0,NULL,toString(sumIf(usd_value,direction='OUT' AND usd_value>=toDecimal128('%s',18)))) large_out_usd,
 count() activity_count,countIf(usd_value IS NOT NULL) priced_activity_count,countIf(usd_value IS NULL) missing_price_count,
 toString(if(count()=0,0,toFloat64(countIf(usd_value IS NOT NULL))/count())) coverage_ratio
FROM facts`, where, q.LargeThresholdUSD, q.LargeThresholdUSD, q.LargeThresholdUSD, q.LargeThresholdUSD, q.LargeThresholdUSD, q.LargeThresholdUSD))
	if err != nil || len(rows) != 1 {
		return FinancialSummary{}, ErrQueryFailed
	}
	row := rows[0]
	flow, err := decodeFlow(row)
	if err != nil {
		return FinancialSummary{}, err
	}
	out := FinancialSummary{ChainID: q.ChainID, Address: q.Address, Window: q.Window, From: q.From.Format("2006-01-02T15:04:05.999999999Z07:00"), To: q.To.Format("2006-01-02T15:04:05.999999999Z07:00"), Flow: flow, PriceBasis: priceBasis}
	for key, target := range map[string]**string{"largest_in_usd": &out.LargestInUSD, "largest_out_usd": &out.LargestOutUSD, "average_in_usd": &out.AverageInUSD, "average_out_usd": &out.AverageOutUSD, "median_in_usd": &out.MedianInUSD, "median_out_usd": &out.MedianOutUSD} {
		if *target, err = nullableText(row, key); err != nil {
			return FinancialSummary{}, err
		}
	}
	out.FirstFunding, _ = text(row, "first_funding")
	out.LatestFunding, _ = text(row, "latest_funding")
	out.Large.ThresholdUSD = q.LargeThresholdUSD
	if out.Large.InCount, err = count(row, "large_in_count"); err != nil {
		return FinancialSummary{}, err
	}
	if out.Large.OutCount, err = count(row, "large_out_count"); err != nil {
		return FinancialSummary{}, err
	}
	out.Large.InUSD, err = nullableText(row, "large_in_usd")
	if err != nil {
		return FinancialSummary{}, err
	}
	out.Large.OutUSD, err = nullableText(row, "large_out_usd")
	if err != nil {
		return FinancialSummary{}, err
	}
	out.PriceCoverage.ActivityCount, err = count(row, "activity_count")
	if err != nil {
		return FinancialSummary{}, err
	}
	out.PriceCoverage.PricedActivityCount, err = count(row, "priced_activity_count")
	if err != nil {
		return FinancialSummary{}, err
	}
	out.PriceCoverage.MissingPriceCount, err = count(row, "missing_price_count")
	if err != nil {
		return FinancialSummary{}, err
	}
	out.PriceCoverage.CoverageRatio, err = text(row, "coverage_ratio")
	return out, err
}

func (r *Repository) Counterparties(ctx context.Context, input Query) ([]CounterpartyFinancialStat, error) {
	q, err := validateQuery(input)
	if err != nil {
		return nil, err
	}
	rows, err := r.query(ctx, fmt.Sprintf(`WITH labels AS (SELECT chain_id,address,argMax(toString(entity_id),updated_at) entity_id,argMax(entity_role,updated_at) entity_role FROM onchain.address_labels FINAL GROUP BY chain_id,address), entities AS (SELECT toString(entity_id) entity_id,argMax(entity_name,updated_at) entity_name,argMax(entity_type,updated_at) entity_type FROM onchain.entity_registry FINAL GROUP BY entity_id)
SELECT a.counterparty_address counterparty,any(l.entity_id) entity_id,any(e.entity_name) entity_name,any(e.entity_type) entity_type,any(l.entity_role) entity_role,
 if(countIf(a.direction='IN' AND a.usd_value IS NOT NULL)=0,NULL,toString(sumIf(a.usd_value,a.direction='IN' AND a.usd_value IS NOT NULL))) in_usd,
 if(countIf(a.direction='OUT' AND a.usd_value IS NOT NULL)=0,NULL,toString(sumIf(a.usd_value,a.direction='OUT' AND a.usd_value IS NOT NULL))) out_usd,
 if(countIf(a.usd_value IS NOT NULL)=0,NULL,toString(sumIf(a.usd_value,a.direction='IN' AND a.usd_value IS NOT NULL)-sumIf(a.usd_value,a.direction='OUT' AND a.usd_value IS NOT NULL))) netflow_usd,
 countIf(a.direction='IN') in_count,countIf(a.direction='OUT') out_count,countIf(a.usd_value IS NOT NULL) priced_count,count() activity_count,toString(min(a.block_time)) first_interaction,toString(max(a.block_time)) last_interaction
FROM onchain.address_activity AS a FINAL LEFT JOIN labels l ON a.chain_id=l.chain_id AND a.counterparty_address=l.address LEFT JOIN entities e ON l.entity_id=e.entity_id WHERE %s AND a.counterparty_address!='' GROUP BY a.counterparty_address ORDER BY sum(abs(ifNull(a.usd_value,0))) DESC,a.counterparty_address LIMIT %d`, activityWhere(q), q.Limit))
	if err != nil {
		return nil, err
	}
	return decodeCounterparties(rows)
}

func (r *Repository) EntityStats(ctx context.Context, input Query) ([]EntityFinancialStat, error) {
	q, err := validateQuery(input)
	if err != nil {
		return nil, err
	}
	conf := confidencePredicate("l.confidence", q.EntityMinConfidence)
	rows, err := r.query(ctx, fmt.Sprintf(`WITH labels AS (SELECT chain_id,address,argMax(toString(entity_id),updated_at) entity_id,argMax(confidence,updated_at) confidence FROM onchain.address_labels FINAL GROUP BY chain_id,address), entities AS (SELECT toString(entity_id) entity_id,argMax(entity_name,updated_at) entity_name,argMax(entity_type,updated_at) entity_type FROM onchain.entity_registry FINAL GROUP BY entity_id)
SELECT l.entity_id,any(e.entity_name) entity_name,any(e.entity_type) entity_type,if(countIf(a.direction='IN' AND a.usd_value IS NOT NULL)=0,NULL,toString(sumIf(a.usd_value,a.direction='IN' AND a.usd_value IS NOT NULL))) in_usd,if(countIf(a.direction='OUT' AND a.usd_value IS NOT NULL)=0,NULL,toString(sumIf(a.usd_value,a.direction='OUT' AND a.usd_value IS NOT NULL))) out_usd,if(countIf(a.usd_value IS NOT NULL)=0,NULL,toString(sumIf(a.usd_value,a.direction='IN' AND a.usd_value IS NOT NULL)-sumIf(a.usd_value,a.direction='OUT' AND a.usd_value IS NOT NULL))) netflow_usd,count() count
FROM onchain.address_activity AS a FINAL INNER JOIN labels l ON a.chain_id=l.chain_id AND a.counterparty_address=l.address INNER JOIN entities e ON l.entity_id=e.entity_id WHERE %s AND l.entity_id!='' AND %s GROUP BY l.entity_id ORDER BY sum(abs(ifNull(a.usd_value,0))) DESC,l.entity_id LIMIT %d`, activityWhere(q), conf, q.Limit))
	if err != nil {
		return nil, err
	}
	return decodeEntities(rows)
}

func (r *Repository) CEXStats(ctx context.Context, input Query) ([]CEXFinancialStat, error) {
	q, err := validateQuery(input)
	if err != nil {
		return nil, err
	}
	conf := confidencePredicate("l.confidence", q.EntityMinConfidence)
	rows, err := r.query(ctx, fmt.Sprintf(`WITH labels AS (SELECT chain_id,address,argMax(toString(entity_id),updated_at) entity_id,argMax(upper(entity_role),updated_at) entity_role,argMax(confidence,updated_at) confidence FROM onchain.address_labels FINAL GROUP BY chain_id,address), entities AS (SELECT toString(entity_id) entity_id,argMax(entity_name,updated_at) entity_name,argMax(upper(entity_type),updated_at) entity_type FROM onchain.entity_registry FINAL GROUP BY entity_id)
SELECT l.entity_id,any(e.entity_name) entity_name,any(l.confidence) confidence,
 if(countIf(a.direction='OUT' AND l.entity_role IN ('DEPOSIT','COLLECTOR','HOT_WALLET') AND a.usd_value IS NOT NULL)=0,NULL,toString(sumIf(a.usd_value,a.direction='OUT' AND l.entity_role IN ('DEPOSIT','COLLECTOR','HOT_WALLET') AND a.usd_value IS NOT NULL))) deposit_usd,
 if(countIf(a.direction='IN' AND l.entity_role IN ('HOT_WALLET','WITHDRAWAL') AND a.usd_value IS NOT NULL)=0,NULL,toString(sumIf(a.usd_value,a.direction='IN' AND l.entity_role IN ('HOT_WALLET','WITHDRAWAL') AND a.usd_value IS NOT NULL))) withdrawal_usd,
 if(countIf(a.usd_value IS NOT NULL)=0,NULL,toString(sumIf(a.usd_value,a.direction='OUT' AND l.entity_role IN ('DEPOSIT','COLLECTOR','HOT_WALLET') AND a.usd_value IS NOT NULL)-sumIf(a.usd_value,a.direction='IN' AND l.entity_role IN ('HOT_WALLET','WITHDRAWAL') AND a.usd_value IS NOT NULL))) netflow_usd,
 countIf(a.direction='OUT' AND l.entity_role IN ('DEPOSIT','COLLECTOR','HOT_WALLET')) deposit_count,countIf(a.direction='IN' AND l.entity_role IN ('HOT_WALLET','WITHDRAWAL')) withdrawal_count,count() interaction_count
FROM onchain.address_activity AS a FINAL INNER JOIN labels l ON a.chain_id=l.chain_id AND a.counterparty_address=l.address INNER JOIN entities e ON l.entity_id=e.entity_id WHERE %s AND e.entity_type='CEX' AND %s GROUP BY l.entity_id ORDER BY sum(abs(ifNull(a.usd_value,0))) DESC,l.entity_id LIMIT %d`, activityWhere(q), conf, q.Limit))
	if err != nil {
		return nil, err
	}
	return decodeCEX(rows)
}

func (r *Repository) DEXStats(ctx context.Context, input Query) (DEXFinancialStat, error) {
	q, err := validateQuery(input)
	if err != nil {
		return DEXFinancialStat{}, err
	}
	rows, err := r.query(ctx, fmt.Sprintf(`WITH raw AS (SELECT tx_hash,JSONExtractString(decoded_fields,'protocol') protocol,JSONExtractString(decoded_fields,'pool') pool,JSONExtractString(decoded_fields,'trader') trader,JSONExtractString(decoded_fields,'token_in') token_in,JSONExtractString(decoded_fields,'token_out') token_out,toDecimal128OrNull(JSONExtractString(decoded_fields,'usd_value'),18) usd FROM onchain.parsed_events FINAL WHERE chain_id=%d AND block_time>=parseDateTime64BestEffort('%s',3,'UTC') AND block_time<parseDateTime64BestEffort('%s',3,'UTC') AND (event_name='DEX_SWAP' OR JSONExtractString(decoded_fields,'type')='DEX_SWAP') AND lower(JSONExtractString(decoded_fields,'trader'))='%s'), swaps AS (SELECT tx_hash,protocol,pool,trader,token_in,token_out,max(usd) usd FROM raw GROUP BY tx_hash,protocol,pool,trader,token_in,token_out), ranked AS (SELECT protocol,count() n FROM swaps GROUP BY protocol ORDER BY n DESC,protocol LIMIT 1)
SELECT count() swap_count,if(countIf(usd IS NOT NULL)=0,NULL,toString(sum(usd))) swap_volume_usd,ifNull((SELECT protocol FROM ranked),'') top_protocol FROM swaps`, q.ChainID, q.From.Format("2006-01-02T15:04:05.999999999Z07:00"), q.To.Format("2006-01-02T15:04:05.999999999Z07:00"), q.Address))
	if err != nil || len(rows) != 1 {
		return DEXFinancialStat{}, ErrQueryFailed
	}
	out := DEXFinancialStat{CanonicalUnit: "chain_id+tx_hash+pool+trader+token_in+token_out"}
	out.SwapCount, err = count(rows[0], "swap_count")
	if err != nil {
		return out, err
	}
	out.SwapVolumeUSD, err = nullableText(rows[0], "swap_volume_usd")
	if err != nil {
		return out, err
	}
	out.TopProtocol, err = text(rows[0], "top_protocol")
	return out, err
}

func (r *Repository) BridgeStats(ctx context.Context, input Query) (BridgeFinancialStat, error) {
	q, err := validateQuery(input)
	if err != nil {
		return BridgeFinancialStat{}, err
	}
	rows, err := r.query(ctx, fmt.Sprintf(`WITH raw AS (SELECT tx_hash,JSONExtractString(decoded_fields,'bridge') bridge,JSONExtractString(decoded_fields,'type') type,toDecimal128OrNull(JSONExtractString(decoded_fields,'usd_value'),18) usd FROM onchain.parsed_events FINAL WHERE chain_id=%d AND block_time>=parseDateTime64BestEffort('%s',3,'UTC') AND block_time<parseDateTime64BestEffort('%s',3,'UTC') AND JSONExtractString(decoded_fields,'type') IN ('BRIDGE_DEPOSIT','BRIDGE_WITHDRAW','BRIDGE_SEND','BRIDGE_RECEIVE') AND (lower(JSONExtractString(decoded_fields,'source_address'))='%s' OR lower(JSONExtractString(decoded_fields,'destination_address'))='%s')), events AS (SELECT tx_hash,bridge,type,max(usd) usd FROM raw GROUP BY tx_hash,bridge,type), ranked AS (SELECT bridge,count() n FROM events GROUP BY bridge ORDER BY n DESC,bridge LIMIT 1)
SELECT if(countIf(type IN ('BRIDGE_WITHDRAW','BRIDGE_RECEIVE') AND usd IS NOT NULL)=0,NULL,toString(sumIf(usd,type IN ('BRIDGE_WITHDRAW','BRIDGE_RECEIVE') AND usd IS NOT NULL))) bridge_in_usd,if(countIf(type IN ('BRIDGE_DEPOSIT','BRIDGE_SEND') AND usd IS NOT NULL)=0,NULL,toString(sumIf(usd,type IN ('BRIDGE_DEPOSIT','BRIDGE_SEND') AND usd IS NOT NULL))) bridge_out_usd,countIf(type IN ('BRIDGE_WITHDRAW','BRIDGE_RECEIVE')) bridge_in_count,countIf(type IN ('BRIDGE_DEPOSIT','BRIDGE_SEND')) bridge_out_count,ifNull((SELECT bridge FROM ranked),'') top_bridge FROM events`, q.ChainID, q.From.Format("2006-01-02T15:04:05.999999999Z07:00"), q.To.Format("2006-01-02T15:04:05.999999999Z07:00"), q.Address, q.Address))
	if err != nil || len(rows) != 1 {
		return BridgeFinancialStat{}, ErrQueryFailed
	}
	var out BridgeFinancialStat
	out.BridgeInUSD, err = nullableText(rows[0], "bridge_in_usd")
	if err != nil {
		return out, err
	}
	out.BridgeOutUSD, err = nullableText(rows[0], "bridge_out_usd")
	if err != nil {
		return out, err
	}
	out.BridgeInCount, err = count(rows[0], "bridge_in_count")
	if err != nil {
		return out, err
	}
	out.BridgeOutCount, err = count(rows[0], "bridge_out_count")
	if err != nil {
		return out, err
	}
	out.TopBridge, err = text(rows[0], "top_bridge")
	return out, err
}

func decodeCounterparties(rows []map[string]any) ([]CounterpartyFinancialStat, error) {
	out := make([]CounterpartyFinancialStat, 0, len(rows))
	for _, row := range rows {
		var x CounterpartyFinancialStat
		var err error
		x.Counterparty, err = text(row, "counterparty")
		if err != nil || !addressRE.MatchString(strings.ToLower(x.Counterparty)) {
			return nil, ErrInvalidData
		}
		x.EntityID, _ = text(row, "entity_id")
		x.EntityName, _ = text(row, "entity_name")
		x.EntityType, _ = text(row, "entity_type")
		x.EntityRole, _ = text(row, "entity_role")
		x.InUSD, err = nullableText(row, "in_usd")
		if err != nil {
			return nil, err
		}
		x.OutUSD, err = nullableText(row, "out_usd")
		if err != nil {
			return nil, err
		}
		x.NetflowUSD, err = nullableText(row, "netflow_usd")
		if err != nil {
			return nil, err
		}
		x.InCount, err = count(row, "in_count")
		if err != nil {
			return nil, err
		}
		x.OutCount, err = count(row, "out_count")
		if err != nil {
			return nil, err
		}
		x.PricedCount, err = count(row, "priced_count")
		if err != nil {
			return nil, err
		}
		x.ActivityCount, err = count(row, "activity_count")
		if err != nil {
			return nil, err
		}
		x.FirstInteraction, _ = text(row, "first_interaction")
		x.LastInteraction, _ = text(row, "last_interaction")
		out = append(out, x)
	}
	return out, nil
}
func decodeEntities(rows []map[string]any) ([]EntityFinancialStat, error) {
	out := make([]EntityFinancialStat, 0, len(rows))
	for _, row := range rows {
		var x EntityFinancialStat
		var err error
		x.EntityID, err = text(row, "entity_id")
		if err != nil {
			return nil, err
		}
		x.EntityName, _ = text(row, "entity_name")
		x.EntityType, _ = text(row, "entity_type")
		x.InUSD, err = nullableText(row, "in_usd")
		if err != nil {
			return nil, err
		}
		x.OutUSD, err = nullableText(row, "out_usd")
		if err != nil {
			return nil, err
		}
		x.NetflowUSD, err = nullableText(row, "netflow_usd")
		if err != nil {
			return nil, err
		}
		x.Count, err = count(row, "count")
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}
func decodeCEX(rows []map[string]any) ([]CEXFinancialStat, error) {
	out := make([]CEXFinancialStat, 0, len(rows))
	for _, row := range rows {
		var x CEXFinancialStat
		var err error
		x.EntityID, err = text(row, "entity_id")
		if err != nil {
			return nil, err
		}
		x.EntityName, _ = text(row, "entity_name")
		x.Confidence, _ = text(row, "confidence")
		x.DepositUSD, err = nullableText(row, "deposit_usd")
		if err != nil {
			return nil, err
		}
		x.WithdrawalUSD, err = nullableText(row, "withdrawal_usd")
		if err != nil {
			return nil, err
		}
		x.NetflowUSD, err = nullableText(row, "netflow_usd")
		if err != nil {
			return nil, err
		}
		x.DepositCount, err = count(row, "deposit_count")
		if err != nil {
			return nil, err
		}
		x.WithdrawalCount, err = count(row, "withdrawal_count")
		if err != nil {
			return nil, err
		}
		x.InteractionCount, err = count(row, "interaction_count")
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}
