package clickhouseinvestigation

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/etl/backend/internal/investigation"
)

// AddressEvidenceRow is an auditable warehouse observation. Lineage fields
// make every row traceable to the ingest job and original source range.
type AddressEvidenceRow struct {
	ChainID             uint32 `json:"chain_id"`
	Address             string `json:"address"`
	CounterpartyAddress string `json:"counterparty_address"`
	Direction           string `json:"direction"`
	ActivityType        string `json:"activity_type"`
	BlockNumber         uint64 `json:"block_number"`
	BlockTime           string `json:"block_time"`
	TxHash              string `json:"tx_hash"`
	EventIndex          string `json:"event_index"`
	TokenAddress        string `json:"token_address"`
	TokenSymbol         string `json:"token_symbol"`
	Amount              string `json:"amount"`
	Status              string `json:"status"`
	SourceProvider      string `json:"source_provider"`
	IngestJobID         string `json:"ingest_job_id"`
	SourceRangeID       string `json:"source_range_id"`
}

// AddressEvidence returns transaction-level, lineage-preserving evidence in a
// stable order. It deliberately has no unbounded mode.
func (r *Repository) AddressEvidence(ctx context.Context, address string, limit int) ([]AddressEvidenceRow, error) {
	address, err := normalizeAddress(address, "address", false)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1000
	}
	if limit > maxFlowLimit {
		return nil, fmt.Errorf("evidence limit exceeds %d rows", maxFlowLimit)
	}
	query := fmt.Sprintf(`SELECT chain_id, address, counterparty_address, direction, activity_type,
  block_number, formatDateTime(block_time, '%%Y-%%m-%%d %%H:%%i:%%s.%%f', 'UTC') AS block_time,
  tx_hash, event_index, token_address, token_symbol, toString(amount) AS amount, status,
  source_provider, ingest_job_id, source_range_id
FROM address_activity FINAL
WHERE chain_id = %d AND address = '%s'
ORDER BY block_time DESC, block_number DESC, tx_hash DESC, event_index DESC
LIMIT %d`, r.chainID, address, limit)
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query ClickHouse address evidence: %w", err)
	}
	result := make([]AddressEvidenceRow, 0, len(rows))
	for _, row := range rows {
		result = append(result, AddressEvidenceRow{
			ChainID: uint32(integer(row["chain_id"])), Address: strings.ToLower(text(row["address"])),
			CounterpartyAddress: strings.ToLower(text(row["counterparty_address"])), Direction: text(row["direction"]),
			ActivityType: text(row["activity_type"]), BlockNumber: uint64(integer(row["block_number"])), BlockTime: text(row["block_time"]),
			TxHash: strings.ToLower(text(row["tx_hash"])), EventIndex: text(row["event_index"]), TokenAddress: strings.ToLower(text(row["token_address"])),
			TokenSymbol: text(row["token_symbol"]), Amount: text(row["amount"]), Status: text(row["status"]),
			SourceProvider: text(row["source_provider"]), IngestJobID: text(row["ingest_job_id"]), SourceRangeID: text(row["source_range_id"]),
		})
	}
	return result, nil
}

// Investigate returns a ClickHouse-backed replacement for the legacy
// investigation summary path.
func (r *Repository) Investigate(ctx context.Context, address string) (*investigation.Summary, error) {
	started := time.Now()
	profile, err := r.Profile(ctx, address)
	if err != nil {
		return nil, err
	}
	summary := &investigation.Summary{Address: profile.Address, Profile: profile, QueryDuration: map[string]string{}}
	summary.QueryDuration["profile_ms"] = fmt.Sprint(time.Since(started).Milliseconds())
	started = time.Now()
	risk, err := r.Risk(ctx, address)
	if err != nil {
		return nil, err
	}
	summary.Risk = risk
	summary.QueryDuration["risk_ms"] = fmt.Sprint(time.Since(started).Milliseconds())
	switch {
	case profile.ContractCount > 0:
		summary.AddressType = "合约"
	case profile.TransactionCount >= 10:
		summary.AddressType = "活跃交易方"
	default:
		summary.AddressType = "低频"
	}
	started = time.Now()
	flows, err := r.Flows(ctx, address, "")
	if err != nil {
		return nil, err
	}
	summary.QueryDuration["flows_ms"] = fmt.Sprint(time.Since(started).Milliseconds())
	tokens := map[string]int{}
	for _, flow := range flows {
		if flow.Direction == "incoming" {
			summary.InCount++
		} else if flow.Direction == "outgoing" {
			summary.OutCount++
		}
		tokens[flow.Token]++
	}
	for token, count := range tokens {
		if count > tokens[summary.TopToken] {
			summary.TopToken = token
		}
	}
	started = time.Now()
	paths, err := r.TraceFunds(ctx, address, 2)
	if err != nil {
		return nil, err
	}
	summary.PathCount = len(paths)
	summary.QueryDuration["path_ms"] = fmt.Sprint(time.Since(started).Milliseconds())
	started = time.Now()
	related, err := r.DiscoverRelations(ctx, []string{address}, 5)
	if err != nil {
		return nil, err
	}
	summary.Related, summary.RelatedCount = related, len(related)
	summary.QueryDuration["relations_ms"] = fmt.Sprint(time.Since(started).Milliseconds())
	return summary, nil
}

// DiscoverRelations calculates Jaccard similarity in ClickHouse and returns a
// bounded candidate list.
func (r *Repository) DiscoverRelations(ctx context.Context, addresses []string, limit int) ([]investigation.RelatedAddress, error) {
	if len(addresses) == 0 || len(addresses) > 50 {
		return nil, fmt.Errorf("between 1 and 50 addresses are required")
	}
	values := make([]string, 0, len(addresses))
	for _, address := range addresses {
		normalized, err := normalizeAddress(address, "address", false)
		if err != nil {
			return nil, err
		}
		values = append(values, "'"+normalized+"'")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 1000 {
		limit = 1000
	}
	list := strings.Join(values, ",")
	query := fmt.Sprintf(`WITH
targets AS (SELECT address, counterparty_address FROM address_activity FINAL WHERE chain_id = %[1]d AND address IN (%[2]s) AND direction IN ('IN','OUT')),
target_counterparties AS (SELECT DISTINCT counterparty_address FROM targets),
candidates AS (SELECT address, groupUniqArray(counterparty_address) AS cps FROM address_activity FINAL WHERE chain_id = %[1]d AND direction IN ('IN','OUT') AND address NOT IN (%[2]s) AND counterparty_address IN target_counterparties GROUP BY address LIMIT 10000),
target_set AS (SELECT groupUniqArray(counterparty_address) AS cps FROM target_counterparties)
SELECT address, length(arrayIntersect(candidates.cps, target_set.cps)) AS shared_counterparties,
  shared_counterparties / greatest(1, length(arrayDistinct(arrayConcat(candidates.cps, target_set.cps)))) AS shared_counterparty_score
FROM candidates CROSS JOIN target_set
WHERE shared_counterparties > 0
ORDER BY shared_counterparty_score DESC, address ASC
LIMIT %[3]d`, r.chainID, list, limit)
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query ClickHouse related addresses: %w", err)
	}
	result := make([]investigation.RelatedAddress, 0, len(rows))
	for _, row := range rows {
		result = append(result, investigation.RelatedAddress{Address: strings.ToLower(text(row["address"])), Score: decimal(row["shared_counterparty_score"]), SharedCounterparties: int(integer(row["shared_counterparties"]))})
	}
	return result, nil
}

// RiskScenario preserves transaction-level evidence rather than promoting the
// aggregate risk score itself to a finding.
func (r *Repository) RiskScenario(ctx context.Context, address string) (*investigation.RiskEvidence, error) {
	address, err := normalizeAddress(address, "address", false)
	if err != nil {
		return nil, err
	}
	risk, err := r.Risk(ctx, address)
	if err != nil {
		return nil, err
	}
	flows, err := r.Flows(ctx, address, "")
	if err != nil {
		return nil, err
	}
	inflows, outflows := make([]investigation.TraceEdge, 0), make([]investigation.TraceEdge, 0)
	for _, flow := range flows {
		edge := investigation.TraceEdge{Token: flow.Token, Amount: flow.Amount, TxHash: flow.TxHash, Block: flow.Block}
		if flow.Direction == "incoming" {
			edge.From, edge.To = flow.Counterparty, address
			inflows = append(inflows, edge)
		} else {
			edge.From, edge.To = address, flow.Counterparty
			outflows = append(outflows, edge)
		}
	}
	sort.Slice(inflows, func(i, j int) bool { return bigValue(inflows[i].Amount).Cmp(bigValue(inflows[j].Amount)) > 0 })
	sort.Slice(outflows, func(i, j int) bool { return parseBlock(outflows[i].Block) < parseBlock(outflows[j].Block) })
	if len(inflows) > 5 {
		inflows = inflows[:5]
	}
	if len(outflows) > 5 {
		outflows = outflows[:5]
	}
	targets := map[string]*investigation.SpreadTarget{}
	for _, flow := range flows {
		if flow.Direction != "outgoing" {
			continue
		}
		target := targets[flow.Counterparty]
		if target == nil {
			target = &investigation.SpreadTarget{Address: flow.Counterparty}
			targets[flow.Counterparty] = target
		}
		target.Count++
		target.Total = new(big.Int).Add(bigValue(target.Total), bigValue(flow.Amount)).String()
	}
	spread := make([]investigation.SpreadTarget, 0, len(targets))
	for _, target := range targets {
		spread = append(spread, *target)
	}
	sort.Slice(spread, func(i, j int) bool {
		if spread[i].Count == spread[j].Count {
			return spread[i].Address < spread[j].Address
		}
		return spread[i].Count > spread[j].Count
	})
	if len(spread) > 5 {
		spread = spread[:5]
	}
	pattern := "常规"
	if len(inflows) > 0 && len(outflows) > 0 {
		pattern = "大额转入-快速转出"
	}
	if len(spread) >= 3 {
		pattern += "-多地址分散"
	}
	return &investigation.RiskEvidence{Address: address, Risk: risk, LargeInflows: inflows, RapidOutflows: outflows, SpreadTargets: spread, Pattern: pattern}, nil
}

func (r *Repository) Evidence(ctx context.Context, address string, maxHops, relatedLimit int) (*investigation.Evidence, error) {
	summary, err := r.Investigate(ctx, address)
	if err != nil {
		return nil, err
	}
	paths, err := r.TraceFunds(ctx, address, maxHops)
	if err != nil {
		return nil, err
	}
	risk, err := r.RiskScenario(ctx, address)
	if err != nil {
		return nil, err
	}
	related, err := r.DiscoverRelations(ctx, []string{address}, relatedLimit)
	if err != nil {
		return nil, err
	}
	return &investigation.Evidence{Timestamp: time.Now().UTC(), Target: summary.Address, Summary: summary, TracePaths: paths, Risk: risk, Related: related}, nil
}

func decimal(value any) float64 {
	result, _ := new(big.Float).SetString(text(value))
	if result == nil {
		return 0
	}
	number, _ := result.Float64()
	return number
}
func bigValue(value string) *big.Int {
	result, ok := new(big.Int).SetString(strings.TrimSpace(value), 10)
	if !ok {
		return new(big.Int)
	}
	return result
}
