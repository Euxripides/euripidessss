// Package clickhouseinvestigation provides bounded, read-only investigation
// queries over the ClickHouse on-chain warehouse.
package clickhouseinvestigation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/investigation"
)

const (
	defaultFlowLimit = 5000
	maxFlowLimit     = 200000
	maxTracePaths    = 50
)

var evmAddressRE = regexp.MustCompile(`^0x[0-9a-f]{40}$`)

// QueryClient is the narrow ClickHouse read contract used by the repository.
type QueryClient interface {
	QueryJSON(context.Context, string) ([]map[string]any, error)
	QueryCSV(context.Context, string) (io.ReadCloser, error)
}

// Repository is scoped to one chain and implements fundflow.FlowSource.
type Repository struct {
	client    QueryClient
	chainID   uint32
	flowLimit int
}

func New(client QueryClient, chainID uint32) (*Repository, error) {
	if client == nil {
		return nil, fmt.Errorf("ClickHouse query client is required")
	}
	if chainID == 0 {
		return nil, fmt.Errorf("chain ID is required")
	}
	return &Repository{client: client, chainID: chainID, flowLimit: defaultFlowLimit}, nil
}

// WithFlowLimit sets the maximum rows returned by each address-flow query.
func (r *Repository) WithFlowLimit(limit int) *Repository {
	if limit > 0 && limit <= maxFlowLimit {
		r.flowLimit = limit
	}
	return r
}

func normalizeAddress(value, field string, optional bool) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if optional && value == "" {
		return "", nil
	}
	if !evmAddressRE.MatchString(value) {
		return "", fmt.Errorf("%s is not a valid EVM address", field)
	}
	return value, nil
}

// Profile returns the same public contract as analyticsapi.Profile without
// reading Parquet or DuckDB.
func (r *Repository) Profile(ctx context.Context, address string) (*analyticsapi.Profile, error) {
	address, err := normalizeAddress(address, "address", false)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT
  '%[2]s' AS address,
  if(count() = 0, '', formatDateTime(min(block_time), '%%Y-%%m-%%d %%H:%%i:%%s', 'UTC')) AS first_activity_time,
  if(count() = 0, '', formatDateTime(max(block_time), '%%Y-%%m-%%d %%H:%%i:%%s', 'UTC')) AS last_activity_time,
  count() AS event_count,
  uniqExact(tx_hash) AS transaction_count,
  (SELECT count() FROM contract_creations FINAL WHERE chain_id = %[1]d AND contract_address = '%[2]s') AS contract_count,
  uniqExactIf(token_address, token_address != '') AS token_count,
  countIf(direction = 'IN') AS total_in,
  countIf(direction = 'OUT') AS total_out,
  uniqExact(toDate(block_time)) AS active_days
FROM address_activity FINAL
WHERE chain_id = %[1]d AND address = '%[2]s'`, r.chainID, address)
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query ClickHouse address profile: %w", err)
	}
	p := &analyticsapi.Profile{Address: address}
	if len(rows) == 0 {
		return p, nil
	}
	row := rows[0]
	p.FirstActivityTime = text(row["first_activity_time"])
	p.LastActivityTime = text(row["last_activity_time"])
	p.EventCount = integer(row["event_count"])
	p.TransactionCount = integer(row["transaction_count"])
	p.ContractCount = integer(row["contract_count"])
	p.TokenCount = integer(row["token_count"])
	p.TotalIn = integer(row["total_in"])
	p.TotalOut = integer(row["total_out"])
	p.ActiveDays = integer(row["active_days"])
	p.RiskScore = riskForProfile(p, 0).RiskScore
	return p, nil
}

// Flows implements fundflow.FlowSource. Results are bounded and ordered
// deterministically; SELF activity is excluded to avoid double attribution.
func (r *Repository) Flows(ctx context.Context, address, token string) ([]analyticsapi.FlowEdge, error) {
	address, err := normalizeAddress(address, "address", false)
	if err != nil {
		return nil, err
	}
	token, err = normalizeAddress(token, "token", true)
	if err != nil {
		return nil, err
	}
	tokenClause := ""
	if token != "" {
		tokenClause = " AND token_address = '" + token + "'"
	}
	query := fmt.Sprintf(`SELECT
  direction, token_address AS token, counterparty_address AS counterparty,
  toString(amount) AS amount, toString(block_number) AS block, tx_hash
FROM address_activity FINAL
WHERE chain_id = %d AND address = '%s' AND direction IN ('IN','OUT')%s
ORDER BY block_time DESC, block_number DESC, tx_hash DESC, event_index DESC
LIMIT %d`, r.chainID, address, tokenClause, r.flowLimit)
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query ClickHouse address flows: %w", err)
	}
	result := make([]analyticsapi.FlowEdge, 0, len(rows))
	for _, row := range rows {
		direction := strings.ToUpper(text(row["direction"]))
		if direction != "IN" && direction != "OUT" {
			continue
		}
		if direction == "IN" {
			direction = "incoming"
		} else {
			direction = "outgoing"
		}
		result = append(result, analyticsapi.FlowEdge{
			Direction: direction, Token: strings.ToLower(text(row["token"])),
			Counterparty: strings.ToLower(text(row["counterparty"])), Amount: text(row["amount"]),
			Block: text(row["block"]), TxHash: strings.ToLower(text(row["tx_hash"])),
		})
	}
	return result, nil
}

// AddressStats implements fundflow.FlowSource using server-side Decimal256
// aggregation and bounded grouped concentration queries.
func (r *Repository) AddressStats(ctx context.Context, address, token string) (*analyticsapi.AddressStats, error) {
	address, err := normalizeAddress(address, "address", false)
	if err != nil {
		return nil, err
	}
	token, err = normalizeAddress(token, "token", true)
	if err != nil {
		return nil, err
	}
	tokenClause := ""
	if token != "" {
		tokenClause = " AND token_address = '" + token + "'"
	}
	query := fmt.Sprintf(`SELECT
  count() AS tx_count, countIf(direction = 'IN') AS in_count,
  countIf(direction = 'OUT') AS out_count,
  uniqExactIf(counterparty_address, direction = 'IN') AS unique_upstream,
  uniqExactIf(counterparty_address, direction = 'OUT') AS unique_downstream,
  uniqExact(toDate(block_time)) AS active_days,
  if(count() = 0, '', formatDateTime(min(block_time), '%%Y-%%m-%%d %%H:%%i:%%s', 'UTC')) AS first_seen,
  if(count() = 0, '', formatDateTime(max(block_time), '%%Y-%%m-%%d %%H:%%i:%%s', 'UTC')) AS last_seen,
  toString(if(count() = 0, 0, avg(amount))) AS avg_amount,
  toString(if(count() = 0, 0, max(amount))) AS max_amount,
  toString(sumIf(amount, direction = 'IN')) AS total_in,
  toString(sumIf(amount, direction = 'OUT')) AS total_out,
  toString(sumIf(amount, direction = 'IN') - sumIf(amount, direction = 'OUT')) AS net_flow,
  countIf(block_time >= now() - INTERVAL 1 DAY) AS recent_24h,
  countIf(block_time >= now() - INTERVAL 7 DAY) AS recent_7d,
  countIf(block_time >= now() - INTERVAL 30 DAY) AS recent_30d,
  argMaxIf(token_address, amount, direction = 'IN') AS dominant_token
FROM address_activity FINAL
WHERE chain_id = %d AND address = '%s' AND direction IN ('IN','OUT')%s`, r.chainID, address, tokenClause)
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query ClickHouse address stats: %w", err)
	}
	st := &analyticsapi.AddressStats{Address: address}
	if len(rows) > 0 {
		row := rows[0]
		st.TxCount, st.InCount, st.OutCount = integer(row["tx_count"]), integer(row["in_count"]), integer(row["out_count"])
		st.UniqueUpstream, st.UniqueDownstream = integer(row["unique_upstream"]), integer(row["unique_downstream"])
		st.ActiveDays, st.FirstSeen, st.LastSeen = integer(row["active_days"]), text(row["first_seen"]), text(row["last_seen"])
		st.AvgAmount, st.MaxAmount = text(row["avg_amount"]), text(row["max_amount"])
		st.TotalIn, st.TotalOut, st.NetFlow = text(row["total_in"]), text(row["total_out"]), text(row["net_flow"])
		st.Recent24h, st.Recent7d, st.Recent30d = integer(row["recent_24h"]), integer(row["recent_7d"]), integer(row["recent_30d"])
		st.DominantToken = strings.ToLower(text(row["dominant_token"]))
	}
	ratios, err := r.concentrationRatios(ctx, address, tokenClause)
	if err != nil {
		return nil, err
	}
	st.Top1SourceRatio, st.Top5SourceRatio = ratios[0], ratios[1]
	st.Top1TargetRatio, st.Top5TargetRatio = ratios[2], ratios[3]
	st.Truncated = st.TxCount > maxFlowLimit
	return st, nil
}

func (r *Repository) concentrationRatios(ctx context.Context, address, tokenClause string) ([4]float64, error) {
	query := fmt.Sprintf(`SELECT direction, counterparty_address, toString(sum(amount)) AS total
FROM address_activity FINAL
WHERE chain_id = %d AND address = '%s' AND direction IN ('IN','OUT')%s
GROUP BY direction, counterparty_address
ORDER BY direction, total DESC
LIMIT %d`, r.chainID, address, tokenClause, maxFlowLimit)
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return [4]float64{}, fmt.Errorf("query ClickHouse counterparty concentration: %w", err)
	}
	in, out := make([]*big.Int, 0), make([]*big.Int, 0)
	for _, row := range rows {
		value, ok := new(big.Int).SetString(text(row["total"]), 10)
		if !ok || value.Sign() < 0 {
			continue
		}
		if strings.EqualFold(text(row["direction"]), "IN") {
			in = append(in, value)
		} else {
			out = append(out, value)
		}
	}
	return [4]float64{topRatio(in, 1), topRatio(in, 5), topRatio(out, 1), topRatio(out, 5)}, nil
}

func topRatio(values []*big.Int, n int) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Cmp(values[j]) > 0 })
	var total, top big.Int
	for index, value := range values {
		total.Add(&total, value)
		if index < n {
			top.Add(&top, value)
		}
	}
	if total.Sign() == 0 {
		return 0
	}
	ratio, _ := new(big.Rat).SetFrac(&top, &total).Float64()
	return ratio
}

func riskForProfile(profile *analyticsapi.Profile, concentration float64) *analyticsapi.Risk {
	frequency := 0.0
	if profile.ActiveDays > 0 {
		frequency = float64(profile.TransactionCount) / float64(profile.ActiveDays)
	}
	score := math.Min(100, 100*(0.6*math.Min(frequency/100, 1)+0.4*concentration))
	score = math.Round(score*100) / 100
	level, reason := "低", "交易频率低、无显著集中关联"
	if score >= 60 {
		level, reason = "高", "高频交易或高集中度关联"
	} else if score >= 30 {
		level, reason = "中", "交易频率中等或存在集中关联"
	}
	return &analyticsapi.Risk{RiskScore: score, RiskLevel: level, RiskReason: reason,
		TransactionFreq: math.Round(frequency*100) / 100, CounterpartyScore: math.Round(concentration*100) / 100}
}

func (r *Repository) Risk(ctx context.Context, address string) (*analyticsapi.Risk, error) {
	profile, err := r.Profile(ctx, address)
	if err != nil {
		return nil, err
	}
	stats, err := r.AddressStats(ctx, address, "")
	if err != nil {
		return nil, err
	}
	concentration := math.Max(math.Max(stats.Top1SourceRatio, stats.Top5SourceRatio), math.Max(stats.Top1TargetRatio, stats.Top5TargetRatio))
	return riskForProfile(profile, concentration), nil
}

// TraceFunds performs a bounded BFS without loading the complete warehouse.
func (r *Repository) TraceFunds(ctx context.Context, address string, maxHops int) ([]investigation.TracePath, error) {
	address, err := normalizeAddress(address, "address", false)
	if err != nil {
		return nil, err
	}
	if maxHops < 1 {
		maxHops = 2
	}
	if maxHops > 4 {
		maxHops = 4
	}
	type frontierNode struct {
		address string
		edges   []investigation.TraceEdge
	}
	frontier := []frontierNode{{address: address}}
	seen := map[string]bool{address: true}
	paths := make([]investigation.TracePath, 0)
	for depth := 0; depth < maxHops && len(frontier) > 0 && len(paths) < maxTracePaths; depth++ {
		next := make([]frontierNode, 0)
		for _, current := range frontier {
			flows, queryErr := r.Flows(ctx, current.address, "")
			if queryErr != nil {
				return nil, queryErr
			}
			for _, flow := range flows {
				to := strings.ToLower(flow.Counterparty)
				if flow.Direction != "outgoing" || !evmAddressRE.MatchString(to) || seen[to] {
					continue
				}
				edge := investigation.TraceEdge{From: current.address, To: to, Token: flow.Token, Amount: flow.Amount, TxHash: flow.TxHash, Block: flow.Block}
				edges := append(append([]investigation.TraceEdge(nil), current.edges...), edge)
				nodes := []string{edges[0].From}
				for _, item := range edges {
					nodes = append(nodes, item.To)
				}
				paths = append(paths, investigation.TracePath{Nodes: nodes, Edges: edges, Hops: len(edges)})
				seen[to] = true
				next = append(next, frontierNode{address: to, edges: edges})
				if len(paths) >= maxTracePaths {
					break
				}
			}
			if len(paths) >= maxTracePaths {
				break
			}
		}
		frontier = next
	}
	return paths, nil
}

func text(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func integer(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case json.Number:
		result, _ := typed.Int64()
		return result
	case string:
		result, _ := strconv.ParseInt(typed, 10, 64)
		return result
	default:
		result, _ := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		return result
	}
}

func parseBlock(value string) uint64 {
	result, _ := strconv.ParseUint(value, 10, 64)
	return result
}

func parseTime(value string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05.000", "2006-01-02 15:04:05", time.RFC3339Nano} {
		if result, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return result
		}
	}
	return time.Time{}
}
