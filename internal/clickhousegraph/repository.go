// Package clickhousegraph provides bounded, ClickHouse-backed graph reads.
// It deliberately depends on a minimal query interface so callers can wire the
// shared ClickHouse client without coupling this package to its transport.
package clickhousegraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultDepth     = 1
	maxDepth         = 4
	defaultEdgeLimit = 200
	maxEdgeLimit     = 1000
	defaultNodeLimit = 200
	maxNodeLimit     = 500
)

var (
	ErrInvalidInput = errors.New("invalid graph input")
	ErrQueryFailed  = errors.New("graph query failed")
	ErrInvalidData  = errors.New("invalid graph result")

	evmAddressPattern = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
	txHashPattern     = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
	supportedChains   = map[uint32]struct{}{1: {}, 56: {}, 8453: {}, 42161: {}}
	allowedActivities = map[string]struct{}{
		"NATIVE_TRANSFER": {}, "CONTRACT_CALL": {}, "TOKEN_TRANSFER": {},
		"ERC20_TRANSFER": {}, "ERC721_TRANSFER": {}, "ERC1155_TRANSFER": {},
		"INTERNAL_TRANSFER": {}, "INTERNAL_TRANSACTION": {},
		"CONTRACT_CREATE": {}, "CONTRACT_CREATION": {},
	}
)

type QueryClient interface {
	QueryJSON(ctx context.Context, query string) ([]map[string]any, error)
}

type Repository struct{ client QueryClient }

func NewRepository(client QueryClient) *Repository { return &Repository{client: client} }

func (r *Repository) ListCounterpartyEdges(ctx context.Context, input CounterpartyQuery) ([]Edge, error) {
	q := EgoQuery{
		ChainID: input.ChainID, RootAddress: input.Address, Depth: 1,
		EdgeLimit: input.Limit, NodeLimit: maxNodeLimit, Direction: input.Direction,
		TokenAddress: input.TokenAddress, ActivityTypes: input.ActivityTypes,
	}
	normalized, err := normalizeQuery(q)
	if err != nil {
		return nil, err
	}
	if r == nil || r.client == nil {
		return nil, ErrQueryFailed
	}
	rows, _, err := r.queryEdges(ctx, []string{normalized.RootAddress}, nil, normalized, normalized.EdgeLimit)
	return rows, err
}

func (r *Repository) GetEgoGraph(ctx context.Context, input EgoQuery) (Graph, error) {
	q, err := normalizeQuery(input)
	if err != nil {
		return Graph{}, err
	}
	if r == nil || r.client == nil {
		return Graph{}, ErrQueryFailed
	}

	depthByAddress := map[string]int{q.RootAddress: 0}
	expanded := make(map[string]struct{})
	frontier := []string{q.RootAddress}
	edges := make([]Edge, 0, q.EdgeLimit)
	edgeSeen := make(map[string]struct{})
	truncated, reached := false, 0

	for depth := 0; depth < q.Depth && len(frontier) > 0 && len(edges) < q.EdgeLimit; depth++ {
		remaining := q.EdgeLimit - len(edges)
		levelQuery := q
		if depth > 0 {
			levelQuery.Direction = DirectionAll
		}
		levelEdges, levelTruncated, queryErr := r.queryEdges(ctx, frontier, expanded, levelQuery, remaining)
		if queryErr != nil {
			return Graph{}, queryErr
		}
		if levelTruncated {
			truncated = true
		}
		for _, address := range frontier {
			expanded[address] = struct{}{}
		}
		nextSet := make(map[string]struct{})
		for _, edge := range levelEdges {
			if _, ok := edgeSeen[edge.ID]; ok {
				continue
			}
			missing := 0
			for _, address := range []string{edge.FromAddress, edge.ToAddress} {
				if _, exists := depthByAddress[address]; !exists {
					missing++
				}
			}
			if len(depthByAddress)+missing > q.NodeLimit {
				truncated = true
				continue
			}
			edgeSeen[edge.ID] = struct{}{}
			edges = append(edges, edge)
			for _, address := range []string{edge.FromAddress, edge.ToAddress} {
				if _, exists := depthByAddress[address]; exists {
					continue
				}
				if len(depthByAddress) >= q.NodeLimit {
					truncated = true
					continue
				}
				depthByAddress[address] = depth + 1
				nextSet[address] = struct{}{}
			}
		}
		if len(levelEdges) > 0 {
			reached = depth + 1
		}
		frontier = sortedKeys(nextSet)
	}
	if len(frontier) > 0 && reached < q.Depth && (len(edges) >= q.EdgeLimit || len(depthByAddress) >= q.NodeLimit) {
		truncated = true
	}

	nodes := buildNodes(depthByAddress, edges)
	if err := r.enrichNodes(ctx, q.ChainID, nodes); err != nil {
		return Graph{}, err
	}
	sortEdges(edges)
	return Graph{
		ChainID: q.ChainID, RootAddress: q.RootAddress, RequestedDepth: q.Depth,
		ReachedDepth: reached, Nodes: nodes, Edges: edges, Truncated: truncated,
	}, nil
}

func (r *Repository) enrichNodes(ctx context.Context, chainID uint32, nodes []Node) error {
	if len(nodes) == 0 {
		return nil
	}
	addresses := make([]string, len(nodes))
	index := make(map[string]int, len(nodes))
	for i := range nodes {
		addresses[i], index[nodes[i].Address] = nodes[i].Address, i
	}
	query := fmt.Sprintf(`SELECT l.address,l.label_name,l.label_type,toString(l.entity_id) AS entity_id,l.entity_role,
l.source,l.confidence,e.entity_name,e.entity_type
FROM onchain.address_labels AS l FINAL LEFT JOIN onchain.entity_registry AS e FINAL ON l.entity_id=e.entity_id
WHERE l.chain_id=%d AND l.address IN (%s)
ORDER BY l.address,l.last_verified DESC,l.updated_at DESC LIMIT %d`, chainID, quoteAddresses(addresses), len(nodes)*10)
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return ErrQueryFailed
	}
	seen := make(map[string]struct{}, len(nodes))
	for _, row := range rows {
		address, ok := stringValue(row["address"])
		address = strings.ToLower(address)
		position, exists := index[address]
		if !ok || !exists {
			return ErrInvalidData
		}
		if _, duplicate := seen[address]; duplicate {
			continue
		}
		seen[address] = struct{}{}
		nodes[position].Label, _ = stringValue(row["label_name"])
		nodes[position].LabelType, _ = stringValue(row["label_type"])
		nodes[position].EntityID, _ = stringValue(row["entity_id"])
		nodes[position].EntityName, _ = stringValue(row["entity_name"])
		nodes[position].EntityType, _ = stringValue(row["entity_type"])
		nodes[position].EntityRole, _ = stringValue(row["entity_role"])
		nodes[position].LabelSource, _ = stringValue(row["source"])
		nodes[position].Confidence, _ = stringValue(row["confidence"])
	}
	return nil
}

func normalizeQuery(q EgoQuery) (EgoQuery, error) {
	if _, ok := supportedChains[q.ChainID]; !ok {
		return EgoQuery{}, fmt.Errorf("%w: unsupported chain_id", ErrInvalidInput)
	}
	q.RootAddress = strings.ToLower(strings.TrimSpace(q.RootAddress))
	if !evmAddressPattern.MatchString(q.RootAddress) {
		return EgoQuery{}, fmt.Errorf("%w: invalid root address", ErrInvalidInput)
	}
	if q.Depth == 0 {
		q.Depth = defaultDepth
	}
	if q.Depth < 1 || q.Depth > maxDepth {
		return EgoQuery{}, fmt.Errorf("%w: depth must be between 1 and %d", ErrInvalidInput, maxDepth)
	}
	if q.EdgeLimit == 0 {
		q.EdgeLimit = defaultEdgeLimit
	}
	if q.EdgeLimit < 1 || q.EdgeLimit > maxEdgeLimit {
		return EgoQuery{}, fmt.Errorf("%w: edge_limit must be between 1 and %d", ErrInvalidInput, maxEdgeLimit)
	}
	if q.NodeLimit == 0 {
		q.NodeLimit = defaultNodeLimit
	}
	if q.NodeLimit < 1 || q.NodeLimit > maxNodeLimit {
		return EgoQuery{}, fmt.Errorf("%w: node_limit must be between 1 and %d", ErrInvalidInput, maxNodeLimit)
	}
	if q.Direction == "" {
		q.Direction = DirectionAll
	}
	if q.Direction != DirectionAll && q.Direction != DirectionIn && q.Direction != DirectionOut {
		return EgoQuery{}, fmt.Errorf("%w: unsupported direction", ErrInvalidInput)
	}
	q.TokenAddress = strings.ToLower(strings.TrimSpace(q.TokenAddress))
	if q.TokenAddress != "" && !evmAddressPattern.MatchString(q.TokenAddress) {
		return EgoQuery{}, fmt.Errorf("%w: invalid token address", ErrInvalidInput)
	}
	seen := make(map[string]struct{}, len(q.ActivityTypes))
	activities := make([]string, 0, len(q.ActivityTypes))
	for _, activity := range q.ActivityTypes {
		activity = strings.ToUpper(strings.TrimSpace(activity))
		if _, ok := allowedActivities[activity]; !ok {
			return EgoQuery{}, fmt.Errorf("%w: unsupported activity type", ErrInvalidInput)
		}
		if _, duplicate := seen[activity]; duplicate {
			continue
		}
		seen[activity] = struct{}{}
		activities = append(activities, activity)
	}
	sort.Strings(activities)
	q.ActivityTypes = activities
	return q, nil
}

func (r *Repository) queryEdges(ctx context.Context, frontier []string, excluded map[string]struct{}, q EgoQuery, limit int) ([]Edge, bool, error) {
	where := []string{
		fmt.Sprintf("chain_id = %d", q.ChainID),
		"address IN (" + quoteAddresses(frontier) + ")",
		"counterparty_address != ''",
		"counterparty_address != address",
	}
	if len(excluded) > 0 {
		where = append(where, "counterparty_address NOT IN ("+quoteAddresses(sortedKeys(excluded))+")")
	}
	if q.Direction == DirectionIn {
		where = append(where, "direction = 'IN'")
	} else if q.Direction == DirectionOut {
		where = append(where, "direction = 'OUT'")
	}
	if q.TokenAddress != "" {
		where = append(where, "token_address = '"+q.TokenAddress+"'")
	}
	if len(q.ActivityTypes) > 0 {
		where = append(where, "activity_type IN ("+quoteStrings(q.ActivityTypes)+")")
	}

	query := fmt.Sprintf(`SELECT
from_address, to_address, token_address, activity_type,
toString(sum(amount)) AS amount, count() AS event_count,
uniqExact(tx_hash) AS transaction_count, min(block_time) AS first_time,
max(block_time) AS last_time,
argMax(tx_hash, tuple(block_time, block_number, tx_hash, event_index)) AS sample_tx_hash
FROM
(
    SELECT DISTINCT
    if(direction = 'OUT', address, counterparty_address) AS from_address,
    if(direction = 'OUT', counterparty_address, address) AS to_address,
    token_address, activity_type, amount, block_number, block_time, tx_hash, event_index
    FROM onchain.address_activity FINAL
    WHERE %s
)
GROUP BY from_address, to_address, token_address, activity_type
ORDER BY last_time DESC, from_address ASC, to_address ASC, token_address ASC, activity_type ASC
LIMIT %d`, strings.Join(where, " AND "), limit+1)

	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, false, ErrQueryFailed
	}
	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}
	edges := make([]Edge, 0, len(rows))
	for _, row := range rows {
		edge, decodeErr := decodeEdge(row)
		if decodeErr != nil {
			return nil, false, ErrInvalidData
		}
		edges = append(edges, edge)
	}
	return edges, truncated, nil
}

func decodeEdge(row map[string]any) (Edge, error) {
	from, ok := stringValue(row["from_address"])
	from = strings.ToLower(from)
	if !ok || !evmAddressPattern.MatchString(from) {
		return Edge{}, ErrInvalidData
	}
	to, ok := stringValue(row["to_address"])
	to = strings.ToLower(to)
	if !ok || !evmAddressPattern.MatchString(to) {
		return Edge{}, ErrInvalidData
	}
	token, _ := stringValue(row["token_address"])
	token = strings.ToLower(token)
	if token != "" && !evmAddressPattern.MatchString(token) {
		return Edge{}, ErrInvalidData
	}
	activity, ok := stringValue(row["activity_type"])
	activity = strings.ToUpper(activity)
	if !ok {
		return Edge{}, ErrInvalidData
	}
	if _, ok = allowedActivities[activity]; !ok {
		return Edge{}, ErrInvalidData
	}
	amount, ok := stringValue(row["amount"])
	if !ok {
		return Edge{}, ErrInvalidData
	}
	events, ok := uintValue(row["event_count"])
	if !ok {
		return Edge{}, ErrInvalidData
	}
	txs, ok := uintValue(row["transaction_count"])
	if !ok {
		return Edge{}, ErrInvalidData
	}
	first, ok := timeValue(row["first_time"])
	if !ok {
		return Edge{}, ErrInvalidData
	}
	last, ok := timeValue(row["last_time"])
	if !ok {
		return Edge{}, ErrInvalidData
	}
	sample, _ := stringValue(row["sample_tx_hash"])
	sample = strings.ToLower(sample)
	if sample != "" && !txHashPattern.MatchString(sample) {
		return Edge{}, ErrInvalidData
	}
	key := strings.Join([]string{from, to, token, activity}, "|")
	sum := sha256.Sum256([]byte(key))
	return Edge{
		ID: hex.EncodeToString(sum[:12]), FromAddress: from, ToAddress: to,
		TokenAddress: token, ActivityType: activity, Amount: amount,
		EventCount: events, TransactionCount: txs, FirstTime: first,
		LastTime: last, SampleTxHash: sample,
	}, nil
}

func buildNodes(depths map[string]int, edges []Edge) []Node {
	nodes := make(map[string]*Node, len(depths))
	for address, depth := range depths {
		nodes[address] = &Node{Address: address, Depth: depth}
	}
	for _, edge := range edges {
		from := nodes[edge.FromAddress]
		to := nodes[edge.ToAddress]
		if from != nil {
			from.OutgoingEdges++
			from.EventCount += edge.EventCount
		}
		if to != nil {
			to.IncomingEdges++
			to.EventCount += edge.EventCount
		}
	}
	result := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, *node)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Depth != result[j].Depth {
			return result[i].Depth < result[j].Depth
		}
		return result[i].Address < result[j].Address
	})
	return result
}

func sortEdges(edges []Edge) {
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.FromAddress != b.FromAddress {
			return a.FromAddress < b.FromAddress
		}
		if a.ToAddress != b.ToAddress {
			return a.ToAddress < b.ToAddress
		}
		if a.TokenAddress != b.TokenAddress {
			return a.TokenAddress < b.TokenAddress
		}
		return a.ActivityType < b.ActivityType
	})
}

func quoteAddresses(addresses []string) string { return quoteStrings(addresses) }

func quoteStrings(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = "'" + value + "'"
	}
	return strings.Join(quoted, ",")
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func stringValue(value any) (string, bool) {
	valueString, ok := value.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(valueString), true
}

func uintValue(value any) (uint64, bool) {
	switch typed := value.(type) {
	case float64:
		if typed < 0 {
			return 0, false
		}
		return uint64(typed), true
	case string:
		parsed, err := strconv.ParseUint(typed, 10, 64)
		return parsed, err == nil
	default:
		parsed, err := strconv.ParseUint(fmt.Sprint(value), 10, 64)
		return parsed, err == nil
	}
}

func timeValue(value any) (time.Time, bool) {
	text, ok := value.(string)
	if !ok {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		parsed, err := time.Parse(layout, text)
		if err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}
