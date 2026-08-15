// Package clickhouseanalytics provides bounded, ClickHouse-only analytical
// queries. It never falls back to DuckDB or Parquet.
package clickhouseanalytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLimit = 20
	maxLimit     = 500
)

var (
	ErrInvalidInput = errors.New("invalid clickhouse analytics input")
	ErrQueryFailed  = errors.New("clickhouse analytics query failed")
	ErrInvalidData  = errors.New("invalid clickhouse analytics result")
	addressPattern  = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
	supportedChains = map[uint32]struct{}{1: {}, 56: {}, 8453: {}, 42161: {}}
)

type QueryClient interface {
	QueryJSON(ctx context.Context, query string) ([]map[string]any, error)
}

type Repository struct{ client QueryClient }

func NewRepository(client QueryClient) *Repository { return &Repository{client: client} }

func (r *Repository) Dashboard(ctx context.Context, chainID uint32) (Dashboard, error) {
	if err := validateChain(chainID); err != nil {
		return Dashboard{}, err
	}
	if r == nil || r.client == nil {
		return Dashboard{}, ErrQueryFailed
	}
	query := fmt.Sprintf(`SELECT
 uniqExact(address) AS address_count,
 uniqExactIf(token_address, token_address != '') AS token_count,
	 uniqExact(tx_hash) AS transaction_count,
	 uniqExactIf((tx_hash,event_index),activity_type IN ('TOKEN_TRANSFER','ERC20_TRANSFER','ERC721_TRANSFER','ERC1155_TRANSFER')) AS transfer_count,
 count() AS event_count
FROM onchain.address_activity FINAL WHERE chain_id = %d`, chainID)
	rows, err := r.query(ctx, query)
	if err != nil || len(rows) != 1 {
		return Dashboard{}, chooseQueryError(err)
	}
	out := Dashboard{ChainID: chainID}
	out.AddressCount, _ = uint64Value(rows[0]["address_count"])
	out.TokenCount, _ = uint64Value(rows[0]["token_count"])
	out.TransactionCount, _ = uint64Value(rows[0]["transaction_count"])
	out.TransferCount, _ = uint64Value(rows[0]["transfer_count"])

	riskQuery := fmt.Sprintf(`SELECT count() AS risk_addresses FROM (
 SELECT address, count() AS event_count, uniqExact(toDate(block_time)) AS active_days,
	       uniqExact(counterparty_address) AS unique_counterparties
 FROM onchain.address_activity FINAL WHERE chain_id = %d
 GROUP BY address HAVING event_count >= 100 OR (active_days > 0 AND event_count / active_days >= 50)
)`, chainID)
	if riskRows, qerr := r.query(ctx, riskQuery); qerr == nil && len(riskRows) == 1 {
		out.RiskAddresses, _ = uint64Value(riskRows[0]["risk_addresses"])
	} else if qerr != nil {
		return Dashboard{}, qerr
	}

	trendQuery := fmt.Sprintf(`SELECT toString(toDate(block_time)) AS date, count() AS events
FROM onchain.address_activity FINAL
WHERE chain_id = %d AND block_time >= now() - INTERVAL 30 DAY
GROUP BY date ORDER BY date ASC LIMIT 31`, chainID)
	trendRows, err := r.query(ctx, trendQuery)
	if err != nil {
		return Dashboard{}, err
	}
	for _, row := range trendRows {
		date, e1 := stringValue(row["date"])
		events, e2 := uint64Value(row["events"])
		if e1 != nil || e2 != nil {
			return Dashboard{}, ErrInvalidData
		}
		out.Trend = append(out.Trend, TrendPoint{Date: date, Events: events})
	}
	return out, nil
}

func (r *Repository) AddressAnalytics(ctx context.Context, input AddressQuery) (AddressAnalytics, error) {
	input, err := validateAddressQuery(input)
	if err != nil {
		return AddressAnalytics{}, err
	}
	if r == nil || r.client == nil {
		return AddressAnalytics{}, ErrQueryFailed
	}
	allTime, err := r.allTime(ctx, input.ChainID, input.Address)
	if err != nil {
		return AddressAnalytics{}, err
	}
	where := addressWhere(input)
	topRows, err := r.query(ctx, fmt.Sprintf(`SELECT counterparty_address AS address, direction,
 count() AS activity_count, uniqExact(tx_hash) AS transaction_count,
 toString(sum(amount)) AS amount, toString(sum(ifNull(usd_value,0))) AS usd_value,
 toString(min(block_time)) AS first_seen_time, toString(max(block_time)) AS last_seen_time
FROM onchain.address_activity FINAL WHERE %s AND counterparty_address != ''
GROUP BY address, direction ORDER BY activity_count DESC, transaction_count DESC, address ASC, direction ASC LIMIT %d`, where, input.Limit))
	if err != nil {
		return AddressAnalytics{}, err
	}
	tops := make([]Counterparty, 0, len(topRows))
	for _, row := range topRows {
		item := Counterparty{}
		item.Address, _ = stringValue(row["address"])
		item.Direction, _ = stringValue(row["direction"])
		item.ActivityCount, _ = uint64Value(row["activity_count"])
		item.TransactionCount, _ = uint64Value(row["transaction_count"])
		item.Amount, _ = stringValue(row["amount"])
		item.USDValue, _ = stringValue(row["usd_value"])
		item.FirstSeenTime, _ = stringValue(row["first_seen_time"])
		item.LastSeenTime, _ = stringValue(row["last_seen_time"])
		tops = append(tops, item)
	}
	dailyRows, err := r.query(ctx, fmt.Sprintf(`SELECT toString(toDate(block_time)) AS date,
 countIf(direction='IN') AS incoming_count, countIf(direction='OUT') AS outgoing_count,
 toString(sumIf(amount,direction='IN')) AS incoming_amount,
 toString(sumIf(amount,direction='OUT')) AS outgoing_amount,
 toString(sumIf(amount,direction='IN')-sumIf(amount,direction='OUT')) AS netflow,
 toString(sumIf(ifNull(usd_value,0),direction='IN')) AS incoming_usd,
 toString(sumIf(ifNull(usd_value,0),direction='OUT')) AS outgoing_usd,
 toString(sumIf(ifNull(usd_value,0),direction='IN')-sumIf(ifNull(usd_value,0),direction='OUT')) AS netflow_usd,
 uniqExact(counterparty_address) AS unique_counterparties
FROM onchain.address_activity FINAL WHERE %s
GROUP BY date ORDER BY date ASC LIMIT 367`, where))
	if err != nil {
		return AddressAnalytics{}, err
	}
	daily := make([]DailyNetflow, 0, len(dailyRows))
	for _, row := range dailyRows {
		daily = append(daily, decodeDaily(row))
	}
	tokenRows, err := r.query(ctx, fmt.Sprintf(`SELECT token_address, any(token_symbol) AS token_symbol, count() AS activity_count,
 toString(sumIf(amount,direction='IN')) AS incoming,
 toString(sumIf(amount,direction='OUT')) AS outgoing,
 toString(sumIf(amount,direction='IN')-sumIf(amount,direction='OUT')) AS netflow,
 toString(sum(ifNull(usd_value,0))) AS usd_value
FROM onchain.address_activity FINAL WHERE %s AND token_address != ''
GROUP BY token_address ORDER BY activity_count DESC, token_address ASC LIMIT %d`, where, input.Limit))
	if err != nil {
		return AddressAnalytics{}, err
	}
	tokens := make([]TokenDistribution, 0, len(tokenRows))
	for _, row := range tokenRows {
		tokens = append(tokens, decodeToken(row))
	}
	return AddressAnalytics{ChainID: input.ChainID, Address: input.Address, AllTime: allTime, TopCounterparties: tops, DailyNetflow: daily, TokenDistribution: tokens}, nil
}

// TopSources ranks addresses that sent value. TopDestinations ranks addresses
// that received value. address_activity contains both sides, so selecting one
// direction avoids double counting a transfer.
func (r *Repository) TopSources(ctx context.Context, chainID uint32, limit int) ([]VolumeStat, error) {
	return r.topAddresses(ctx, chainID, "OUT", limit)
}

func (r *Repository) TopDestinations(ctx context.Context, chainID uint32, limit int) ([]VolumeStat, error) {
	return r.topAddresses(ctx, chainID, "IN", limit)
}

func (r *Repository) topAddresses(ctx context.Context, chainID uint32, direction string, limit int) ([]VolumeStat, error) {
	if err := validateChain(chainID); err != nil {
		return nil, err
	}
	limit, err := validatedLimit(limit, defaultLimit)
	if err != nil {
		return nil, err
	}
	if r == nil || r.client == nil {
		return nil, ErrQueryFailed
	}
	query := fmt.Sprintf(`SELECT address, uniqExact(counterparty_address) AS counterparty_count,
 uniqExact(tx_hash) AS transaction_count, toString(sum(amount)) AS amount,
 toString(sum(ifNull(usd_value,0))) AS usd_value
FROM onchain.address_activity FINAL
WHERE chain_id=%d AND direction='%s'
GROUP BY address ORDER BY transaction_count DESC,address ASC LIMIT %d`, chainID, direction, limit)
	rows, err := r.query(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]VolumeStat, 0, len(rows))
	for _, row := range rows {
		item := VolumeStat{}
		item.Address, _ = stringValue(row["address"])
		item.CounterpartyCount, _ = uint64Value(row["counterparty_count"])
		item.TransactionCount, _ = uint64Value(row["transaction_count"])
		item.Amount, _ = stringValue(row["amount"])
		item.USDValue, _ = stringValue(row["usd_value"])
		if !addressPattern.MatchString(item.Address) {
			return nil, ErrInvalidData
		}
		out = append(out, item)
	}
	return out, nil
}

// InOutVolume returns volume for the validated address and bounded time range.
func (r *Repository) InOutVolume(ctx context.Context, input AddressQuery) (InOutVolume, error) {
	input, err := validateAddressQuery(input)
	if err != nil {
		return InOutVolume{}, err
	}
	if r == nil || r.client == nil {
		return InOutVolume{}, ErrQueryFailed
	}
	query := fmt.Sprintf(`SELECT countIf(direction='IN') AS incoming_count,countIf(direction='OUT') AS outgoing_count,
 toString(sumIf(amount,direction='IN')) AS incoming_amount,toString(sumIf(amount,direction='OUT')) AS outgoing_amount,
 toString(sumIf(ifNull(usd_value,0),direction='IN')) AS incoming_usd,
 toString(sumIf(ifNull(usd_value,0),direction='OUT')) AS outgoing_usd
FROM onchain.address_activity FINAL WHERE %s`, addressWhere(input))
	rows, err := r.query(ctx, query)
	if err != nil || len(rows) != 1 {
		return InOutVolume{}, chooseQueryError(err)
	}
	x := InOutVolume{}
	x.IncomingCount, _ = uint64Value(rows[0]["incoming_count"])
	x.OutgoingCount, _ = uint64Value(rows[0]["outgoing_count"])
	x.IncomingAmount, _ = stringValue(rows[0]["incoming_amount"])
	x.OutgoingAmount, _ = stringValue(rows[0]["outgoing_amount"])
	x.IncomingUSD, _ = stringValue(rows[0]["incoming_usd"])
	x.OutgoingUSD, _ = stringValue(rows[0]["outgoing_usd"])
	return x, nil
}

func (r *Repository) AllTimeStats(ctx context.Context, chainID uint32, address string) (AllTimeStats, error) {
	address, err := validateAddress(chainID, address)
	if err != nil {
		return AllTimeStats{}, err
	}
	if r == nil || r.client == nil {
		return AllTimeStats{}, ErrQueryFailed
	}
	return r.allTime(ctx, chainID, address)
}

func (r *Repository) Risk(ctx context.Context, chainID uint32, address string) (RiskResult, error) {
	address, err := validateAddress(chainID, address)
	if err != nil {
		return RiskResult{}, err
	}
	if r == nil || r.client == nil {
		return RiskResult{}, ErrQueryFailed
	}
	query := fmt.Sprintf(`SELECT count() AS event_count, uniqExact(toDate(block_time)) AS active_days,
	 uniqExactIf(counterparty_address,counterparty_address!='') AS unique_counterparties
	FROM onchain.address_activity FINAL WHERE chain_id=%d AND address='%s'`, chainID, address)
	rows, err := r.query(ctx, query)
	if err != nil || len(rows) != 1 {
		return RiskResult{}, chooseQueryError(err)
	}
	events, e1 := uint64Value(rows[0]["event_count"])
	days, e2 := uint64Value(rows[0]["active_days"])
	counterparties, e3 := uint64Value(rows[0]["unique_counterparties"])
	if e1 != nil || e2 != nil || e3 != nil {
		return RiskResult{}, ErrInvalidData
	}
	if events == 0 {
		// 没有活动数据时不能得出“低风险”结论：明确区分“未筛查”与“零风险”。
		return RiskResult{Address: address, RiskLevel: "insufficient_data",
			RiskReason: "当前地址没有可用于风险筛查的活动数据", DataSufficient: false,
			Rules: []string{}, TransactionFrequency: 0, CounterpartyConcentration: 0,
			UniqueCounterparties: 0, EventCount: 0, ActiveDays: days,
			Method: "deterministic_clickhouse_screening_v1"}, nil
	}
	concentration := 0.0
	if events > 0 {
		topQuery := fmt.Sprintf(`SELECT count() AS counterparty_events
		FROM onchain.address_activity FINAL WHERE chain_id=%d AND address='%s' AND counterparty_address!=''
		GROUP BY counterparty_address ORDER BY counterparty_events DESC, counterparty_address ASC LIMIT 1`, chainID, address)
		topRows, qerr := r.query(ctx, topQuery)
		if qerr != nil {
			return RiskResult{}, qerr
		}
		if len(topRows) == 1 {
			topEvents, convErr := uint64Value(topRows[0]["counterparty_events"])
			if convErr != nil {
				return RiskResult{}, ErrInvalidData
			}
			concentration = float64(topEvents) / float64(events)
		}
	}
	freq := float64(events)
	if days > 0 {
		freq /= float64(days)
	}
	score, rules := 0.0, []string{}
	if freq >= 100 {
		score += 45
		rules = append(rules, "HIGH_DAILY_FREQUENCY")
	} else if freq >= 25 {
		score += 25
		rules = append(rules, "ELEVATED_DAILY_FREQUENCY")
	}
	if concentration >= .8 && events >= 10 {
		score += 35
		rules = append(rules, "HIGH_COUNTERPARTY_CONCENTRATION")
	} else if concentration >= .5 && events >= 10 {
		score += 20
		rules = append(rules, "ELEVATED_COUNTERPARTY_CONCENTRATION")
	}
	if counterparties >= 100 {
		score += 20
		rules = append(rules, "HIGH_COUNTERPARTY_BREADTH")
	}
	if score > 100 {
		score = 100
	}
	level := "low"
	if score >= 70 {
		level = "high"
	} else if score >= 35 {
		level = "medium"
	}
	reason := "No screening rule triggered"
	if len(rules) > 0 {
		reason = "Rule-based screening: " + strings.Join(rules, ", ")
	}
	scorePtr := score
	return RiskResult{Address: address, RiskScore: &scorePtr, RiskLevel: level, RiskReason: reason, DataSufficient: true, Rules: rules,
		TransactionFrequency: freq, CounterpartyConcentration: concentration, UniqueCounterparties: counterparties,
		EventCount: events, ActiveDays: days, Method: "deterministic_clickhouse_screening_v1"}, nil
}

func (r *Repository) TwoHopPaths(ctx context.Context, input PathQuery) ([]PathItem, error) {
	address, err := validateAddress(input.ChainID, input.Address)
	if err != nil {
		return nil, err
	}
	limit, err := validatedLimit(input.Limit, 50)
	if err != nil {
		return nil, err
	}
	if r == nil || r.client == nil {
		return nil, ErrQueryFailed
	}
	query := fmt.Sprintf(`WITH edges AS (
 SELECT address AS source, counterparty_address AS target, token_address AS token,
        sum(amount) AS amount, uniqExact(tx_hash) AS tx_count
 FROM onchain.address_activity FINAL
 WHERE chain_id=%d AND direction='OUT' AND counterparty_address != ''
 GROUP BY source,target,token)
SELECT e1.source AS a,e1.target AS b,e2.target AS c,
 if(e1.token=e2.token,e1.token,'mixed') AS token,
 toString(least(e1.amount,e2.amount)) AS amount,
 least(e1.tx_count,e2.tx_count) AS tx_count
FROM edges e1 INNER JOIN edges e2 ON e1.target=e2.source
WHERE e1.source='%s' AND e2.target NOT IN ('%s',e1.target)
	ORDER BY least(e1.amount,e2.amount) DESC,b ASC,c ASC,token ASC LIMIT %d`, input.ChainID, address, address, limit)
	rows, err := r.query(ctx, query)
	if err != nil {
		return nil, err
	}
	items := make([]PathItem, 0, len(rows))
	for _, row := range rows {
		item := PathItem{}
		item.A, _ = stringValue(row["a"])
		item.B, _ = stringValue(row["b"])
		item.C, _ = stringValue(row["c"])
		item.Token, _ = stringValue(row["token"])
		item.Amount, _ = stringValue(row["amount"])
		item.TxCount, _ = uint64Value(row["tx_count"])
		items = append(items, item)
	}
	return items, nil
}

func (r *Repository) Graph(ctx context.Context, input GraphQuery) (Graph, error) {
	if err := validateChain(input.ChainID); err != nil {
		return Graph{}, err
	}
	limit, err := validatedLimit(input.Limit, 200)
	if err != nil {
		return Graph{}, err
	}
	if r == nil || r.client == nil {
		return Graph{}, ErrQueryFailed
	}
	query := fmt.Sprintf(`SELECT if(direction='IN',counterparty_address,address) AS source,
 if(direction='IN',address,counterparty_address) AS target,
 activity_type AS kind,token_address AS token,toString(sum(amount)) AS amount,
 toString(sum(if(isNotNull(raw_historical_value_usdt) AND isFinite(ifNull(raw_historical_value_usdt,0)) AND ifNull(raw_historical_value_usdt,0) BETWEEN 0 AND 1e15,raw_historical_value_usdt,0))) AS historical_value_usdt,
 multiIf(countIf(isNotNull(raw_historical_value_usdt) AND isFinite(ifNull(raw_historical_value_usdt,0)) AND ifNull(raw_historical_value_usdt,0) BETWEEN 0 AND 1e15)=count(),'VALUED',countIf(isNotNull(raw_historical_value_usdt) AND isFinite(ifNull(raw_historical_value_usdt,0)) AND ifNull(raw_historical_value_usdt,0) BETWEEN 0 AND 1e15)>0,'PARTIAL','NO_PRICE') AS valuation_status,
 uniqExact(tx_hash) AS tx_count
FROM
(SELECT a.*,multiIf(isNotNull(a.usd_value),toFloat64(a.usd_value),a.token_address='0x55d398326f99059ff775485246999027b3197955',toFloat64(a.amount),p.token_address!='' AND dateDiff('second',p.timestamp_bucket,a.block_time)<=86400,toFloat64(a.amount)*toFloat64(p.price_usd),a.token_address IN ('0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d','0xe9e7cea3dedca5984780bafc599bd69add087d56','0xc5f0f7b66764f6ec8c8dff7ba683102295e16409'),toFloat64(a.amount),CAST(NULL AS Nullable(Float64))) AS raw_historical_value_usdt
FROM onchain.address_activity AS a FINAL ASOF LEFT JOIN (SELECT * FROM onchain.token_prices FINAL WHERE chain_id=%d ORDER BY chain_id,token_address,timestamp_bucket) p ON a.chain_id=p.chain_id AND if(a.token_address='',concat('native:',toString(a.chain_id)),a.token_address)=p.token_address AND a.block_time>=p.timestamp_bucket WHERE a.chain_id=%d)
WHERE chain_id=%d AND direction='OUT' AND counterparty_address != ''
 GROUP BY source,target,kind,token ORDER BY tx_count DESC,source ASC,target ASC,kind ASC,token ASC LIMIT %d`, input.ChainID, input.ChainID, input.ChainID, limit)

	rows, err := r.query(ctx, query)
	if err != nil {
		return Graph{}, err
	}
	edges := make([]GraphEdge, 0, len(rows))
	degrees := map[string]uint64{}
	for _, row := range rows {
		e := GraphEdge{}
		e.Source, _ = stringValue(row["source"])
		e.Target, _ = stringValue(row["target"])
		e.Kind, _ = stringValue(row["kind"])
		e.Token, _ = stringValue(row["token"])
		e.Amount, _ = stringValue(row["amount"])
		e.HistoricalValueUSDT, _ = stringValue(row["historical_value_usdt"])
		e.ValuationStatus, _ = stringValue(row["valuation_status"])
		e.TxCount, _ = uint64Value(row["tx_count"])
		if !addressPattern.MatchString(e.Source) || !addressPattern.MatchString(e.Target) {
			return Graph{}, ErrInvalidData
		}
		edges = append(edges, e)
		degrees[e.Source]++
		degrees[e.Target]++
	}
	ids := make([]string, 0, len(degrees))
	for id := range degrees {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	nodes := make([]GraphNode, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, GraphNode{ID: id, Type: "address", Degree: degrees[id]})
	}
	return Graph{Nodes: nodes, Edges: edges}, nil
}

func (r *Repository) allTime(ctx context.Context, chainID uint32, address string) (AllTimeStats, error) {
	query := fmt.Sprintf(`SELECT toString(min(block_time)) AS first_activity_time,toString(max(block_time)) AS last_activity_time,
 count() AS event_count,uniqExact(tx_hash) AS transaction_count,
 countIf(activity_type IN ('CONTRACT_CREATE','CONTRACT_CREATION')) AS contract_count,
 uniqExactIf(token_address,token_address!='') AS token_count,countIf(direction='IN') AS incoming_count,countIf(direction='OUT') AS outgoing_count,
 toString(sumIf(amount,direction='IN')) AS total_in,toString(sumIf(amount,direction='OUT')) AS total_out,
 toString(sumIf(amount,direction='IN')-sumIf(amount,direction='OUT')) AS netflow,
 uniqExact(toDate(block_time)) AS active_days,uniqExact(counterparty_address) AS unique_counterparties
FROM onchain.address_activity FINAL WHERE chain_id=%d AND address='%s'`, chainID, address)
	rows, err := r.query(ctx, query)
	if err != nil || len(rows) != 1 {
		return AllTimeStats{}, chooseQueryError(err)
	}
	x := AllTimeStats{}
	x.FirstActivityTime, _ = stringValue(rows[0]["first_activity_time"])
	x.LastActivityTime, _ = stringValue(rows[0]["last_activity_time"])
	x.EventCount, _ = uint64Value(rows[0]["event_count"])
	x.TransactionCount, _ = uint64Value(rows[0]["transaction_count"])
	x.ContractCount, _ = uint64Value(rows[0]["contract_count"])
	x.TokenCount, _ = uint64Value(rows[0]["token_count"])
	x.IncomingCount, _ = uint64Value(rows[0]["incoming_count"])
	x.OutgoingCount, _ = uint64Value(rows[0]["outgoing_count"])
	x.TotalIn, _ = stringValue(rows[0]["total_in"])
	x.TotalOut, _ = stringValue(rows[0]["total_out"])
	x.Netflow, _ = stringValue(rows[0]["netflow"])
	x.ActiveDays, _ = uint64Value(rows[0]["active_days"])
	x.UniqueCounterparties, _ = uint64Value(rows[0]["unique_counterparties"])
	return x, nil
}

func (r *Repository) query(ctx context.Context, q string) ([]map[string]any, error) {
	rows, err := r.client.QueryJSON(ctx, q)
	if err != nil {
		return nil, ErrQueryFailed
	}
	return rows, nil
}
func chooseQueryError(err error) error {
	if err != nil {
		return err
	}
	return ErrInvalidData
}
func validateChain(chainID uint32) error {
	if _, ok := supportedChains[chainID]; !ok {
		return fmt.Errorf("%w: unsupported chain_id", ErrInvalidInput)
	}
	return nil
}
func validateAddress(chainID uint32, address string) (string, error) {
	if err := validateChain(chainID); err != nil {
		return "", err
	}
	address = strings.ToLower(strings.TrimSpace(address))
	if !addressPattern.MatchString(address) {
		return "", fmt.Errorf("%w: invalid EVM address", ErrInvalidInput)
	}
	return address, nil
}
func validatedLimit(limit, def int) (int, error) {
	if limit == 0 {
		limit = def
	}
	if limit < 1 || limit > maxLimit {
		return 0, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maxLimit)
	}
	return limit, nil
}
func validateAddressQuery(q AddressQuery) (AddressQuery, error) {
	a, err := validateAddress(q.ChainID, q.Address)
	if err != nil {
		return q, err
	}
	q.Address = a
	q.Limit, err = validatedLimit(q.Limit, defaultLimit)
	if err != nil {
		return q, err
	}
	if q.To.IsZero() {
		q.To = time.Now().UTC()
	}
	if q.From.IsZero() {
		q.From = q.To.AddDate(0, 0, -30)
	}
	q.From = q.From.UTC()
	q.To = q.To.UTC()
	if !q.From.Before(q.To) || q.To.Sub(q.From) > 366*24*time.Hour {
		return q, fmt.Errorf("%w: date range must be positive and at most 366 days", ErrInvalidInput)
	}
	return q, nil
}
func addressWhere(q AddressQuery) string {
	return fmt.Sprintf("chain_id=%d AND address='%s' AND block_time >= parseDateTime64BestEffort('%s',3,'UTC') AND block_time < parseDateTime64BestEffort('%s',3,'UTC')", q.ChainID, q.Address, q.From.Format(time.RFC3339Nano), q.To.Format(time.RFC3339Nano))
}
func stringValue(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case json.Number:
		return x.String(), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case nil:
		return "", nil
	default:
		return "", errors.New("not string")
	}
}
func uint64Value(v any) (uint64, error) {
	s, e := stringValue(v)
	if e != nil {
		return 0, e
	}
	return strconv.ParseUint(s, 10, 64)
}
func floatValue(v any) (float64, error) {
	s, e := stringValue(v)
	if e != nil {
		return 0, e
	}
	f, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, errors.New("not finite")
	}
	return f, nil
}
func decodeDaily(r map[string]any) DailyNetflow {
	x := DailyNetflow{}
	x.Date, _ = stringValue(r["date"])
	x.IncomingCount, _ = uint64Value(r["incoming_count"])
	x.OutgoingCount, _ = uint64Value(r["outgoing_count"])
	x.IncomingAmount, _ = stringValue(r["incoming_amount"])
	x.OutgoingAmount, _ = stringValue(r["outgoing_amount"])
	x.Netflow, _ = stringValue(r["netflow"])
	x.IncomingUSD, _ = stringValue(r["incoming_usd"])
	x.OutgoingUSD, _ = stringValue(r["outgoing_usd"])
	x.NetflowUSD, _ = stringValue(r["netflow_usd"])
	x.UniqueCounterparties, _ = uint64Value(r["unique_counterparties"])
	return x
}
func decodeToken(r map[string]any) TokenDistribution {
	x := TokenDistribution{}
	x.TokenAddress, _ = stringValue(r["token_address"])
	x.TokenSymbol, _ = stringValue(r["token_symbol"])
	x.ActivityCount, _ = uint64Value(r["activity_count"])
	x.Incoming, _ = stringValue(r["incoming"])
	x.Outgoing, _ = stringValue(r["outgoing"])
	x.Netflow, _ = stringValue(r["netflow"])
	x.USDValue, _ = stringValue(r["usd_value"])
	return x
}
