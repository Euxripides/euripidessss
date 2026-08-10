package semanticanalytics

import (
	"context"
	"fmt"
	"time"
)

const storedHistoricalUSD = "stored_historical_usd_value_only"

type QueryClient interface {
	QueryJSON(ctx context.Context, query string) ([]map[string]any, error)
}

type Repository struct{ client QueryClient }

func NewRepository(client QueryClient) *Repository { return &Repository{client: client} }

func (r *Repository) AddressSummaryV2(ctx context.Context, input AddressQuery) (AddressSummaryV2, error) {
	q, err := validateAddressQuery(input)
	if err != nil {
		return AddressSummaryV2{}, err
	}
	if r == nil || r.client == nil {
		return AddressSummaryV2{}, ErrQueryFailed
	}
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT
 uniqExact(tx_hash) AS tx_count,
 countIf(direction='IN') AS in_count,countIf(direction='OUT') AS out_count,
 countIf(activity_type IN ('TOKEN_TRANSFER','ERC20_TRANSFER','ERC721_TRANSFER','ERC1155_TRANSFER')) AS token_transfer_count,
 countIf(activity_type='INTERNAL_TRANSFER') AS internal_transfer_count,
 uniqExactIf(counterparty_address,counterparty_address!='') AS unique_counterparties,
 toString(min(block_time)) AS first_seen,toString(max(block_time)) AS last_seen,
 uniqExact(toDate(block_time)) AS active_days,
 toString(sumIf(ifNull(usd_value,0),direction='IN')) AS total_in_usd,
 toString(sumIf(ifNull(usd_value,0),direction='OUT')) AS total_out_usd,
 toString(sumIf(ifNull(usd_value,0),direction='IN')-sumIf(ifNull(usd_value,0),direction='OUT')) AS netflow_usd,
 toString(maxIf(ifNull(usd_value,0),direction='IN')) AS largest_in_usd,
 toString(maxIf(ifNull(usd_value,0),direction='OUT')) AS largest_out_usd,
 toString(sumIf(ifNull(usd_value,0),direction='IN' AND lowerUTF8(counterparty_entity_type) IN ('cex','exchange'))) AS cex_in_usd,
 toString(sumIf(ifNull(usd_value,0),direction='OUT' AND lowerUTF8(counterparty_entity_type) IN ('cex','exchange'))) AS cex_out_usd,
 toString(sumIf(ifNull(usd_value,0),lowerUTF8(counterparty_entity_type) IN ('dex','decentralized_exchange'))) AS dex_volume_usd,
 toString(sumIf(ifNull(usd_value,0),lowerUTF8(counterparty_entity_type)='bridge')) AS bridge_volume_usd,
 countIf(activity_type IN ('CONTRACT_CREATE','CONTRACT_CREATION')) AS contract_created_count,
 countIf(usd_value IS NOT NULL) AS usd_valued_activity_count,count() AS activity_count
FROM onchain.address_activity FINAL WHERE %s`, where(q)))
	if err != nil || len(rows) != 1 {
		return AddressSummaryV2{}, resultError(err)
	}
	out := AddressSummaryV2{ChainID: q.ChainID, Address: q.Address, From: q.From.Format(time.RFC3339Nano), To: q.To.Format(time.RFC3339Nano), PriceBasis: storedHistoricalUSD}
	if err := decodeSummary(rows[0], &out); err != nil {
		return AddressSummaryV2{}, err
	}
	return out, nil
}

func (r *Repository) CounterpartiesV2(ctx context.Context, input CounterpartyQuery) (CounterpartyStatisticsV2, error) {
	q, err := validateCounterpartyQuery(input)
	if err != nil {
		return CounterpartyStatisticsV2{}, err
	}
	if r == nil || r.client == nil {
		return CounterpartyStatisticsV2{}, ErrQueryFailed
	}
	types := []struct {
		filter, amount, order string
	}{
		{"direction='IN'", "incoming_value", "incoming_value DESC,address ASC"},
		{"direction='OUT'", "outgoing_value", "outgoing_value DESC,address ASC"},
		{"1", "incoming_value+outgoing_value", "activity_count DESC,address ASC"},
		{"1", "abs(incoming_value-outgoing_value)", "abs(incoming_value-outgoing_value) DESC,address ASC"},
	}
	sets := make([][]CounterpartyV2, 4)
	for i, typ := range types {
		query := fmt.Sprintf(`WITH labels AS (
 SELECT chain_id,address,argMax(label_name,tuple(last_verified,updated_at)) AS label_name,
  argMax(entity_id,tuple(last_verified,updated_at)) AS entity_id
 FROM onchain.address_labels FINAL GROUP BY chain_id,address),
grouped AS (
 SELECT a.counterparty_address AS address,
 argMax(if(e.entity_name!='',e.entity_name,if(e.entity_type!='',e.entity_type,a.counterparty_entity_type)),a.block_time) AS entity,
 argMax(if(l.label_name!='',l.label_name,a.counterparty_label),a.block_time) AS label,
 count() AS activity_count,uniqExact(a.tx_hash) AS transaction_count,
 sumIf(ifNull(a.usd_value,0),a.direction='IN') AS incoming_value,
 sumIf(ifNull(a.usd_value,0),a.direction='OUT') AS outgoing_value,
 min(a.block_time) AS first_seen,max(a.block_time) AS last_seen
 FROM (SELECT * FROM onchain.address_activity FINAL WHERE %s AND counterparty_address!='' AND %s) AS a
 LEFT JOIN labels AS l ON a.chain_id=l.chain_id AND a.counterparty_address=l.address
 LEFT JOIN onchain.entity_registry AS e FINAL ON l.entity_id=e.entity_id GROUP BY address),
 totals AS (SELECT sum(%s) AS total_usd FROM grouped)
SELECT address,entity,label,activity_count,transaction_count,toString(incoming_value) AS incoming_usd,
 toString(outgoing_value) AS outgoing_usd,toString(incoming_value-outgoing_value) AS netflow_usd,
 toString(%s) AS amount_usd,toString(if(total_usd=0,0,toFloat64(%s)/toFloat64(total_usd))) AS share,
 toString(first_seen) AS first_seen,toString(last_seen) AS last_seen
FROM grouped CROSS JOIN totals ORDER BY %s LIMIT %d`, where(q.AddressQuery), typ.filter, typ.amount, typ.amount, typ.amount, typ.order, q.Limit)
		rows, queryErr := r.query(ctx, query)
		if queryErr != nil {
			return CounterpartyStatisticsV2{}, queryErr
		}
		sets[i], err = decodeCounterparties(rows)
		if err != nil {
			return CounterpartyStatisticsV2{}, err
		}
	}
	return CounterpartyStatisticsV2{ChainID: q.ChainID, Address: q.Address, TopSources: sets[0], TopDestinations: sets[1], TopByCount: sets[2], TopByAbsNetflow: sets[3], PriceBasis: storedHistoricalUSD}, nil
}

func (r *Repository) Concentration(ctx context.Context, input AddressQuery) (Concentration, error) {
	q, err := validateAddressQuery(input)
	if err != nil {
		return Concentration{}, err
	}
	if r == nil || r.client == nil {
		return Concentration{}, ErrQueryFailed
	}
	rows, err := r.query(ctx, fmt.Sprintf(`WITH grouped AS (
 SELECT counterparty_address AS address,sumIf(ifNull(usd_value,0),direction='IN') AS incoming,
 sumIf(ifNull(usd_value,0),direction='OUT') AS outgoing
 FROM onchain.address_activity FINAL WHERE %s AND counterparty_address!='' GROUP BY address), ranked AS (
 SELECT incoming,outgoing,row_number() OVER (ORDER BY incoming DESC,address ASC) AS in_rank,
 row_number() OVER (ORDER BY outgoing DESC,address ASC) AS out_rank FROM grouped)
SELECT toString(if(sum(incoming)=0,0,sumIf(incoming,in_rank<=1)/sum(incoming))) AS in_top1,
 toString(if(sum(incoming)=0,0,sumIf(incoming,in_rank<=5)/sum(incoming))) AS in_top5,
 toString(if(sum(incoming)=0,0,sumIf(incoming,in_rank<=10)/sum(incoming))) AS in_top10,
 toString(sum(incoming)) AS in_total,toString(if(sum(outgoing)=0,0,sumIf(outgoing,out_rank<=1)/sum(outgoing))) AS out_top1,
 toString(if(sum(outgoing)=0,0,sumIf(outgoing,out_rank<=5)/sum(outgoing))) AS out_top5,
 toString(if(sum(outgoing)=0,0,sumIf(outgoing,out_rank<=10)/sum(outgoing))) AS out_top10,toString(sum(outgoing)) AS out_total
FROM ranked`, where(q)))
	if err != nil || len(rows) != 1 {
		return Concentration{}, resultError(err)
	}
	return decodeConcentration(rows[0])
}

func (r *Repository) Retention(ctx context.Context, input SnapshotQuery) (Retention, error) {
	q, err := validateSnapshotQuery(input)
	if err != nil {
		return Retention{}, err
	}
	if r == nil || r.client == nil {
		return Retention{}, ErrQueryFailed
	}
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT
 toString(sumIf(ifNull(usd_value,0),direction='IN')) AS received_usd,
 toString(greatest(sumIf(ifNull(usd_value,0),direction='IN' AND block_time<=as_of-INTERVAL 1 HOUR)-sumIf(ifNull(usd_value,0),direction='OUT'),0)) AS retained_1h,
 toString(greatest(sumIf(ifNull(usd_value,0),direction='IN' AND block_time<=as_of-INTERVAL 6 HOUR)-sumIf(ifNull(usd_value,0),direction='OUT'),0)) AS retained_6h,
 toString(greatest(sumIf(ifNull(usd_value,0),direction='IN' AND block_time<=as_of-INTERVAL 24 HOUR)-sumIf(ifNull(usd_value,0),direction='OUT'),0)) AS retained_24h,
 toString(greatest(sumIf(ifNull(usd_value,0),direction='IN' AND block_time<=as_of-INTERVAL 7 DAY)-sumIf(ifNull(usd_value,0),direction='OUT'),0)) AS retained_7d,
 toString(greatest(sumIf(ifNull(usd_value,0),direction='IN' AND block_time<=as_of-INTERVAL 30 DAY)-sumIf(ifNull(usd_value,0),direction='OUT'),0)) AS retained_30d
FROM onchain.address_activity FINAL
CROSS JOIN (SELECT parseDateTime64BestEffort('%s',3,'UTC') AS as_of) AS snapshot
WHERE chain_id=%d AND address='%s' AND block_time>=parseDateTime64BestEffort('%s',3,'UTC') AND block_time<=snapshot.as_of`, q.AsOf.Format(time.RFC3339Nano), q.ChainID, q.Address, q.From.Format(time.RFC3339Nano)))
	if err != nil || len(rows) != 1 {
		return Retention{}, resultError(err)
	}
	out, err := decodeRetention(rows[0])
	if err != nil {
		return Retention{}, err
	}
	out.AsOf = q.AsOf.Format(time.RFC3339Nano)
	out.Method = "bounded_gross_usd_retention_lower_bound; outgoing value offsets oldest received value; opening balance is excluded"
	out.PriceBasis = storedHistoricalUSD
	return out, nil
}

func (r *Repository) FastPassThrough(ctx context.Context, input SnapshotQuery) (FastPassThrough, error) {
	q, err := validateSnapshotQuery(input)
	if err != nil {
		return FastPassThrough{}, err
	}
	if r == nil || r.client == nil {
		return FastPassThrough{}, ErrQueryFailed
	}
	rows, err := r.query(ctx, fmt.Sprintf(`WITH incoming AS (
 SELECT chain_id,address,token_address,block_time AS in_time,tx_hash AS in_tx,toFloat64(usd_value) AS in_usd
 FROM onchain.address_activity FINAL WHERE chain_id=%d AND address='%s' AND direction='IN' AND usd_value IS NOT NULL
 AND block_time>=parseDateTime64BestEffort('%s',3,'UTC') AND block_time<=parseDateTime64BestEffort('%s',3,'UTC')
 ORDER BY chain_id,address,token_address,in_time), outgoing AS (
 SELECT chain_id,address,token_address,block_time AS out_time,tx_hash AS out_tx,toFloat64(usd_value) AS out_usd
 FROM onchain.address_activity FINAL WHERE chain_id=%d AND address='%s' AND direction='OUT' AND usd_value IS NOT NULL
 AND block_time>=parseDateTime64BestEffort('%s',3,'UTC') AND block_time<=parseDateTime64BestEffort('%s',3,'UTC')
 ORDER BY chain_id,address,token_address,out_time), matched AS (
 SELECT o.out_time,o.out_usd,i.in_time FROM outgoing o ASOF LEFT JOIN incoming i
 ON o.chain_id=i.chain_id AND o.address=i.address AND o.token_address=i.token_address AND o.out_time>=i.in_time), totals AS (
 SELECT ifNull((SELECT sum(in_usd) FROM incoming),0) AS received_value,(SELECT count() FROM incoming) AS in_count,(SELECT count() FROM outgoing) AS out_count), windows AS (
 SELECT tuple.1 AS window,tuple.2 AS seconds FROM (
  SELECT arrayJoin([('5m',toUInt32(300)),('30m',toUInt32(1800)),('1h',toUInt32(3600)),('6h',toUInt32(21600)),('24h',toUInt32(86400))]) AS tuple) AS expanded)
SELECT window,toString(least(sumIf(ifNull(out_usd,0),in_time IS NOT NULL AND out_time<=in_time+toIntervalSecond(seconds)),received_value)) AS matched_out_usd,
 toString(received_value) AS received_usd,toString(if(received_value=0,0,least(sumIf(ifNull(out_usd,0),in_time IS NOT NULL AND out_time<=in_time+toIntervalSecond(seconds)),received_value)/received_value)) AS pass_through_ratio,
 in_count,out_count FROM windows CROSS JOIN totals LEFT JOIN matched ON 1=1
GROUP BY window,seconds,received_value,in_count,out_count ORDER BY seconds`, q.ChainID, q.Address, q.From.Format(time.RFC3339Nano), q.AsOf.Format(time.RFC3339Nano), q.ChainID, q.Address, q.From.Format(time.RFC3339Nano), q.AsOf.Format(time.RFC3339Nano)))
	if err != nil {
		return FastPassThrough{}, err
	}
	out, err := decodePassThrough(rows)
	if err != nil {
		return FastPassThrough{}, err
	}
	out.AsOf = q.AsOf.Format(time.RFC3339Nano)
	out.Method = "latest-prior-inflow ASOF match by token; matched outgoing USD is capped by received USD"
	out.Interpretation = "behavioral timing indicator only; it is not evidence of crime, ownership, collection, or automated distribution"
	out.PriceBasis = storedHistoricalUSD
	return out, nil
}

func (r *Repository) HistoricalSnapshot(ctx context.Context, input SnapshotQuery) (HistoricalSnapshot, error) {
	q, err := validateSnapshotQuery(input)
	if err != nil {
		return HistoricalSnapshot{}, err
	}
	base := AddressQuery{ChainID: q.ChainID, Address: q.Address, From: q.From, To: q.AsOf.Add(time.Nanosecond)}
	summary, err := r.AddressSummaryV2(ctx, base)
	if err != nil {
		return HistoricalSnapshot{}, err
	}
	concentration, err := r.Concentration(ctx, base)
	if err != nil {
		return HistoricalSnapshot{}, err
	}
	retention, err := r.Retention(ctx, q)
	if err != nil {
		return HistoricalSnapshot{}, err
	}
	return HistoricalSnapshot{AsOf: q.AsOf.Format(time.RFC3339Nano), Summary: summary, Concentration: concentration, Retention: retention, SnapshotBasis: "canonical events with block_time at or before as_of; no live RPC or current-price substitution"}, nil
}

func (r *Repository) query(ctx context.Context, query string) ([]map[string]any, error) {
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, ErrQueryFailed
	}
	return rows, nil
}

func resultError(err error) error {
	if err != nil {
		return err
	}
	return ErrInvalidData
}

func decodeSummary(row map[string]any, out *AddressSummaryV2) error {
	var err error
	if out.TxCount, err = requiredCount(row, "tx_count"); err != nil {
		return err
	}
	if out.InCount, err = requiredCount(row, "in_count"); err != nil {
		return err
	}
	if out.OutCount, err = requiredCount(row, "out_count"); err != nil {
		return err
	}
	if out.TokenTransferCount, err = requiredCount(row, "token_transfer_count"); err != nil {
		return err
	}
	if out.InternalTransferCount, err = requiredCount(row, "internal_transfer_count"); err != nil {
		return err
	}
	if out.UniqueCounterparties, err = requiredCount(row, "unique_counterparties"); err != nil {
		return err
	}
	if out.FirstSeen, err = requiredText(row, "first_seen"); err != nil {
		return err
	}
	if out.LastSeen, err = requiredText(row, "last_seen"); err != nil {
		return err
	}
	if out.ActiveDays, err = requiredCount(row, "active_days"); err != nil {
		return err
	}
	if out.TotalInUSD, err = requiredText(row, "total_in_usd"); err != nil {
		return err
	}
	if out.TotalOutUSD, err = requiredText(row, "total_out_usd"); err != nil {
		return err
	}
	if out.NetflowUSD, err = requiredText(row, "netflow_usd"); err != nil {
		return err
	}
	if out.LargestInUSD, err = requiredText(row, "largest_in_usd"); err != nil {
		return err
	}
	if out.LargestOutUSD, err = requiredText(row, "largest_out_usd"); err != nil {
		return err
	}
	if out.CEXInUSD, err = requiredText(row, "cex_in_usd"); err != nil {
		return err
	}
	if out.CEXOutUSD, err = requiredText(row, "cex_out_usd"); err != nil {
		return err
	}
	if out.DEXVolumeUSD, err = requiredText(row, "dex_volume_usd"); err != nil {
		return err
	}
	if out.BridgeVolumeUSD, err = requiredText(row, "bridge_volume_usd"); err != nil {
		return err
	}
	if out.ContractCreatedCount, err = requiredCount(row, "contract_created_count"); err != nil {
		return err
	}
	if out.USDValuedActivityCount, err = requiredCount(row, "usd_valued_activity_count"); err != nil {
		return err
	}
	out.ActivityCount, err = requiredCount(row, "activity_count")
	return err
}

func decodeCounterparties(rows []map[string]any) ([]CounterpartyV2, error) {
	out := make([]CounterpartyV2, 0, len(rows))
	for _, row := range rows {
		item := CounterpartyV2{}
		var err error
		if item.Address, err = requiredText(row, "address"); err != nil || !addressPattern.MatchString(item.Address) {
			return nil, ErrInvalidData
		}
		if item.Entity, err = requiredText(row, "entity"); err != nil {
			return nil, err
		}
		if item.Label, err = requiredText(row, "label"); err != nil {
			return nil, err
		}
		if item.ActivityCount, err = requiredCount(row, "activity_count"); err != nil {
			return nil, err
		}
		if item.TransactionCount, err = requiredCount(row, "transaction_count"); err != nil {
			return nil, err
		}
		if item.IncomingUSD, err = requiredText(row, "incoming_usd"); err != nil {
			return nil, err
		}
		if item.OutgoingUSD, err = requiredText(row, "outgoing_usd"); err != nil {
			return nil, err
		}
		if item.NetflowUSD, err = requiredText(row, "netflow_usd"); err != nil {
			return nil, err
		}
		if item.AmountUSD, err = requiredText(row, "amount_usd"); err != nil {
			return nil, err
		}
		if item.Share, err = requiredText(row, "share"); err != nil {
			return nil, err
		}
		if item.FirstSeen, err = requiredText(row, "first_seen"); err != nil {
			return nil, err
		}
		if item.LastSeen, err = requiredText(row, "last_seen"); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func decodeConcentration(row map[string]any) (Concentration, error) {
	var out Concentration
	var err error
	if out.Inflow.Top1, err = requiredText(row, "in_top1"); err != nil {
		return out, err
	}
	if out.Inflow.Top5, err = requiredText(row, "in_top5"); err != nil {
		return out, err
	}
	if out.Inflow.Top10, err = requiredText(row, "in_top10"); err != nil {
		return out, err
	}
	if out.Inflow.Total, err = requiredText(row, "in_total"); err != nil {
		return out, err
	}
	if out.Outflow.Top1, err = requiredText(row, "out_top1"); err != nil {
		return out, err
	}
	if out.Outflow.Top5, err = requiredText(row, "out_top5"); err != nil {
		return out, err
	}
	if out.Outflow.Top10, err = requiredText(row, "out_top10"); err != nil {
		return out, err
	}
	if out.Outflow.Total, err = requiredText(row, "out_total"); err != nil {
		return out, err
	}
	out.PriceBasis = storedHistoricalUSD
	return out, nil
}

func decodeRetention(row map[string]any) (Retention, error) {
	var out Retention
	var err error
	if out.ReceivedUSD, err = requiredText(row, "received_usd"); err != nil {
		return out, err
	}
	if out.Retained1H, err = requiredText(row, "retained_1h"); err != nil {
		return out, err
	}
	if out.Retained6H, err = requiredText(row, "retained_6h"); err != nil {
		return out, err
	}
	if out.Retained24H, err = requiredText(row, "retained_24h"); err != nil {
		return out, err
	}
	if out.Retained7D, err = requiredText(row, "retained_7d"); err != nil {
		return out, err
	}
	if out.Retained30D, err = requiredText(row, "retained_30d"); err != nil {
		return out, err
	}
	return out, nil
}

func decodePassThrough(rows []map[string]any) (FastPassThrough, error) {
	out := FastPassThrough{Windows: make([]PassThroughWindow, 0, len(rows))}
	for i, row := range rows {
		var item PassThroughWindow
		var err error
		if item.Window, err = requiredText(row, "window"); err != nil {
			return out, err
		}
		if item.MatchedOutUSD, err = requiredText(row, "matched_out_usd"); err != nil {
			return out, err
		}
		if item.ReceivedUSD, err = requiredText(row, "received_usd"); err != nil {
			return out, err
		}
		if item.PassThroughRatio, err = requiredText(row, "pass_through_ratio"); err != nil {
			return out, err
		}
		if i == 0 {
			if out.USDValuedIn, err = requiredCount(row, "in_count"); err != nil {
				return out, err
			}
			if out.USDValuedOut, err = requiredCount(row, "out_count"); err != nil {
				return out, err
			}
		}
		out.Windows = append(out.Windows, item)
	}
	return out, nil
}
