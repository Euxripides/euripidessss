package financialintegration

import (
	"context"
	"fmt"
	"strings"
)

type GraphRepository struct{ client QueryClient }

func NewGraphRepository(client QueryClient) *GraphRepository { return &GraphRepository{client: client} }

// HistoricalGraph aggregates only locally stored historical USD values. A
// missing price remains excluded from USD totals; it is never converted to 0
// and never substituted with a current price.
func (r *GraphRepository) HistoricalGraph(ctx context.Context, input GraphQuery) (HistoricalUSDGraph, error) {
	address, token, minUSD, from, to, err := validateCommon(input.ChainID, input.Address, input.From, input.To, input.TokenAddress, input.MinUSD)
	if err != nil {
		return HistoricalUSDGraph{}, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > maxGraphEdges {
		return HistoricalUSDGraph{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maxGraphEdges)
	}
	if r == nil || r.client == nil {
		return HistoricalUSDGraph{}, ErrQueryFailed
	}

	tokenClause := ""
	if token != "" {
		tokenClause = " AND token_address='" + token + "'"
	}
	query := fmt.Sprintf(`WITH token_edges AS
(
 SELECT
  if(direction='OUT',address,counterparty_address) AS source_address,
  if(direction='OUT',counterparty_address,address) AS target_address,
  token_address,argMax(token_symbol,tuple(block_time,block_number,tx_hash,event_index)) AS token_symbol,
  toString(sum(amount)) AS token_amount,
  sum(usd_value) AS token_usd,
  argMax(price_usd,tuple(block_time,block_number,tx_hash,event_index)) AS historical_price,
  argMax(price_time,tuple(block_time,block_number,tx_hash,event_index)) AS price_time,
  arrayStringConcat(arraySort(groupUniqArray(if(price_source='','MISSING',price_source))),'|') AS price_source,
  arrayStringConcat(arraySort(groupUniqArray(if(price_confidence='','MISSING',price_confidence))),'|') AS price_confidence,
  uniqExactState(tx_hash) AS transaction_state,count() AS event_count,min(block_time) AS token_first_time,max(block_time) AS token_last_time,
  argMax(counterparty_entity_id,tuple(block_time,block_number,tx_hash,event_index)) AS entity_id,
  argMax(counterparty_label,tuple(block_time,block_number,tx_hash,event_index)) AS entity_name,
  argMax(counterparty_role,tuple(block_time,block_number,tx_hash,event_index)) AS entity_role
 FROM onchain.address_activity FINAL
 WHERE chain_id=%d AND address='%s' AND counterparty_address!='' AND counterparty_address!=address
  AND block_time>=%s AND block_time<%s AND usd_value IS NOT NULL%s
 GROUP BY source_address,target_address,token_address
), valued AS
(
 SELECT source_address AS from_address,target_address AS to_address,
  toString(sum(token_usd) OVER (PARTITION BY source_address,target_address)) AS historical_usd,
  uniqExactMerge(transaction_state) OVER (PARTITION BY source_address,target_address) AS transaction_count,
  sum(event_count) OVER (PARTITION BY source_address,target_address) AS event_count,
  toString(min(token_first_time) OVER (PARTITION BY source_address,target_address)) AS first_time,
  toString(max(token_last_time) OVER (PARTITION BY source_address,target_address)) AS last_time,
  token_address,token_symbol,token_amount,toString(token_usd) AS token_historical_usd,
  ifNull(toString(historical_price),'') AS historical_price,ifNull(toString(price_time),'') AS price_time,
  price_source,price_confidence,ifNull(toString(entity_id),'') AS entity_id,entity_name,entity_role,
  '' AS entity_confidence
 FROM token_edges
)
SELECT * FROM valued WHERE toDecimal256(historical_usd,18)>=toDecimal256('%s',18)
ORDER BY toDecimal256(historical_usd,18) DESC,from_address,to_address,toDecimal256(token_historical_usd,18) DESC,token_address
LIMIT %d`, input.ChainID, address, quoteTime(from), quoteTime(to), tokenClause, minUSD, maxTokenBreakdowns)
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return HistoricalUSDGraph{}, fmt.Errorf("%w: %v", ErrQueryFailed, err)
	}
	edges, truncated, err := decodeGraphRows(rows, limit)
	if err != nil {
		return HistoricalUSDGraph{}, err
	}
	return HistoricalUSDGraph{
		ChainID: input.ChainID, Address: address, From: from.Format("2006-01-02T15:04:05.999999999Z07:00"),
		To: to.Format("2006-01-02T15:04:05.999999999Z07:00"), MinUSD: minUSD, Edges: edges,
		Truncated: truncated || len(rows) == maxTokenBreakdowns, PriceBasis: "stored_historical_usd_only",
	}, nil
}

func decodeGraphRows(rows []map[string]any, limit int) ([]HistoricalUSDEdge, bool, error) {
	byKey := make(map[string]*HistoricalUSDEdge)
	order := make([]string, 0)
	truncated := false
	for _, row := range rows {
		from, err := requiredString(row, "from_address")
		if err != nil || !addressPattern.MatchString(strings.ToLower(from)) {
			return nil, false, ErrInvalidData
		}
		to, err := requiredString(row, "to_address")
		if err != nil || !addressPattern.MatchString(strings.ToLower(to)) {
			return nil, false, ErrInvalidData
		}
		from, to = strings.ToLower(from), strings.ToLower(to)
		key := from + "|" + to
		edge := byKey[key]
		if edge == nil {
			if len(order) >= limit {
				truncated = true
				continue
			}
			txCount, countErr := requiredUint(row, "transaction_count")
			if countErr != nil {
				return nil, false, countErr
			}
			eventCount, countErr := requiredUint(row, "event_count")
			if countErr != nil {
				return nil, false, countErr
			}
			edge = &HistoricalUSDEdge{
				FromAddress: from, ToAddress: to, HistoricalUSD: optionalString(row, "historical_usd"),
				TransactionCount: txCount, EventCount: eventCount, FirstTime: optionalString(row, "first_time"),
				LastTime: optionalString(row, "last_time"), EntityID: optionalString(row, "entity_id"),
				EntityName: optionalString(row, "entity_name"), EntityRole: optionalString(row, "entity_role"),
				EntityConfidence: optionalString(row, "entity_confidence"), TokenBreakdown: []TokenBreakdown{},
			}
			byKey[key], order = edge, append(order, key)
		}
		edge.TokenBreakdown = append(edge.TokenBreakdown, TokenBreakdown{
			TokenAddress: strings.ToLower(optionalString(row, "token_address")), TokenSymbol: optionalString(row, "token_symbol"),
			Amount: optionalString(row, "token_amount"), HistoricalUSD: firstPresent(row, "token_historical_usd", "token_usd"),
			HistoricalPrice: optionalString(row, "historical_price"), PriceTime: optionalString(row, "price_time"),
			PriceSource: optionalString(row, "price_source"), PriceConfidence: optionalString(row, "price_confidence"),
		})
	}
	edges := make([]HistoricalUSDEdge, 0, len(order))
	for _, key := range order {
		edges = append(edges, *byKey[key])
	}
	return edges, truncated, nil
}

func firstPresent(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := optionalString(row, key); value != "" {
			return value
		}
	}
	return ""
}
