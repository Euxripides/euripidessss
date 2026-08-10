package financialquality

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
	ErrInvalidInput = errors.New("invalid financial quality input")
	ErrQueryFailed  = errors.New("financial quality query failed")
	ErrInvalidData  = errors.New("invalid financial quality result")
	supportedChains = map[uint32]struct{}{1: {}, 56: {}, 8453: {}, 42161: {}}
)

type QueryClient interface {
	QueryJSON(ctx context.Context, query string) ([]map[string]any, error)
}

type Filter struct {
	Window string
	Start  *time.Time
	End    *time.Time
}

type Repository struct {
	client QueryClient
	now    func() time.Time
}

func NewRepository(client QueryClient) *Repository {
	return &Repository{client: client, now: time.Now}
}

// Report reads only local ClickHouse facts and registries. It never calls a
// price provider, RPC endpoint, explorer, or downloader in the request path.
func (r *Repository) Report(ctx context.Context, chainID uint32, filter Filter) (Report, error) {
	if r == nil || r.client == nil {
		return Report{}, ErrQueryFailed
	}
	if _, ok := supportedChains[chainID]; !ok {
		return Report{}, fmt.Errorf("%w: unsupported chain_id", ErrInvalidInput)
	}
	window, predicate, err := normalizeWindow(filter, r.now().UTC())
	if err != nil {
		return Report{}, err
	}
	query := qualitySQL(chainID, predicate)
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return Report{}, fmt.Errorf("%w: %v", ErrQueryFailed, err)
	}
	if len(rows) != 1 {
		return Report{}, ErrInvalidData
	}
	row := rows[0]
	values := make(map[string]uint64, 13)
	for _, key := range []string{
		"price_required", "priced", "historical_price", "fallback_price", "missing_price",
		"position_events", "known_cost_basis", "dex_candidates", "dex_decoded", "bridge_candidates", "bridge_decoded",
		"counterparties", "known_entity",
	} {
		value, ok := uintValue(row, key)
		if !ok {
			return Report{}, fmt.Errorf("%w: %s", ErrInvalidData, key)
		}
		values[key] = value
	}
	if values["priced"] > values["price_required"] || values["historical_price"] > values["priced"] ||
		values["fallback_price"] > values["priced"] || values["missing_price"] > values["price_required"] ||
		values["dex_decoded"] > values["dex_candidates"] || values["bridge_decoded"] > values["bridge_candidates"] ||
		values["known_entity"] > values["counterparties"] || values["known_cost_basis"] > values["position_events"] {
		return Report{}, ErrInvalidData
	}

	priceCoverage := coverage(values["priced"], values["price_required"], "canonical_token_transfers", values["price_required"] > 0)
	fallbackRatio := coverage(values["fallback_price"], values["priced"], "priced_transfers", values["priced"] > 0)
	dexCoverage := coverage(values["dex_decoded"], values["dex_candidates"], "canonical_dex_candidates", values["dex_candidates"] > 0)
	bridgeCoverage := coverage(values["bridge_decoded"], values["bridge_candidates"], "canonical_bridge_candidates", values["bridge_candidates"] > 0)
	entityCoverage := coverage(values["known_entity"], values["counterparties"], "unique_counterparties", values["counterparties"] > 0)

	costAvailable := values["position_events"] > 0
	costCoverage := coverage(values["known_cost_basis"], values["position_events"], "canonical_financial_position_events", costAvailable)
	costStatus, costReason := "UNAVAILABLE", "no canonical acquisition events are available; transfers are not inferred as trades"
	if costAvailable {
		costStatus, costReason = "AVAILABLE", "known basis requires a confirmed DEX/known buy with stored historical USD; other acquisitions remain unknown"
	}
	generated := r.now().UTC()
	return Report{
		ChainID: chainID,
		Window:  window,
		Price: PriceQuality{
			TransfersRequiringPrice: values["price_required"], Priced: values["priced"],
			HistoricalPrice: values["historical_price"], FallbackPrice: values["fallback_price"],
			MissingPrice: values["missing_price"], Coverage: priceCoverage, FallbackRatio: fallbackRatio,
		},
		CostBasis: CostBasisQuality{
			PositionEvents: values["position_events"], KnownCostBasis: values["known_cost_basis"],
			UnknownCostBasis: values["position_events"] - values["known_cost_basis"], Coverage: costCoverage, Status: costStatus,
			Reason: costReason,
		},
		DEXDecode:    DecodeQuality{Candidates: values["dex_candidates"], Decoded: values["dex_decoded"], Missing: values["dex_candidates"] - values["dex_decoded"], Coverage: dexCoverage},
		BridgeDecode: DecodeQuality{Candidates: values["bridge_candidates"], Decoded: values["bridge_decoded"], Missing: values["bridge_candidates"] - values["bridge_decoded"], Coverage: bridgeCoverage},
		Entity:       EntityQuality{Counterparties: values["counterparties"], KnownEntity: values["known_entity"], UnknownEntity: values["counterparties"] - values["known_entity"], Coverage: entityCoverage},
		LastUpdated:  stringValue(row, "last_updated"), GeneratedAt: generated,
	}, nil
}

func qualitySQL(chainID uint32, timePredicate string) string {
	activityPredicate := fmt.Sprintf("chain_id=%d%s", chainID, timePredicate)
	transferPredicate := fmt.Sprintf("chain_id=%d%s", chainID, timePredicate)
	positionPredicate := fmt.Sprintf("chain_id=%d%s", chainID, strings.ReplaceAll(timePredicate, "block_time", "event_time"))
	return fmt.Sprintf(`/* financialquality:report */ WITH
activity AS (SELECT * FROM onchain.address_activity FINAL WHERE %s),
transfers AS (SELECT * FROM onchain.token_transfers FINAL WHERE %s),
positions AS (SELECT * FROM onchain.financial_position_events FINAL WHERE %s)
SELECT
(SELECT count() FROM transfers) AS price_required,
(SELECT countIf(usd_value IS NOT NULL AND usd_price IS NOT NULL AND price_time IS NOT NULL AND price_source!='' AND upperUTF8(price_source)!='CURRENT') FROM transfers) AS priced,
(SELECT countIf(usd_value IS NOT NULL AND usd_price IS NOT NULL AND price_time IS NOT NULL AND price_source!='' AND upperUTF8(price_source) NOT IN ('PEG_FALLBACK','FALLBACK','CURRENT')) FROM transfers) AS historical_price,
(SELECT countIf(usd_value IS NOT NULL AND usd_price IS NOT NULL AND price_time IS NOT NULL AND upperUTF8(price_source) IN ('PEG_FALLBACK','FALLBACK')) FROM transfers) AS fallback_price,
(SELECT countIf(usd_value IS NULL OR usd_price IS NULL OR price_time IS NULL OR price_source='' OR upperUTF8(price_source)='CURRENT') FROM transfers) AS missing_price,
(SELECT countIf(event_type IN ('DEX_BUY','KNOWN_BUY','TRANSFER_IN','AIRDROP','MINT','BRIDGE_IN')) FROM positions) AS position_events,
(SELECT countIf(event_type IN ('DEX_BUY','KNOWN_BUY') AND usd_value IS NOT NULL) FROM positions) AS known_cost_basis,
(SELECT countIf(upperUTF8(activity_type)='DEX_SWAP' OR upperUTF8(counterparty_entity_type) IN ('DEX','DEX_ROUTER','ROUTER')) FROM activity) AS dex_candidates,
(SELECT countIf(upperUTF8(activity_type)='DEX_SWAP') FROM activity) AS dex_decoded,
(SELECT countIf(upperUTF8(activity_type) IN ('BRIDGE_DEPOSIT','BRIDGE_WITHDRAW','BRIDGE_SEND','BRIDGE_RECEIVE') OR upperUTF8(counterparty_entity_type)='BRIDGE') FROM activity) AS bridge_candidates,
(SELECT countIf(upperUTF8(activity_type) IN ('BRIDGE_DEPOSIT','BRIDGE_WITHDRAW','BRIDGE_SEND','BRIDGE_RECEIVE')) FROM activity) AS bridge_decoded,
(SELECT uniqExact(counterparty_address) FROM activity WHERE counterparty_address!='') AS counterparties,
(SELECT uniqExact(address) FROM onchain.address_labels FINAL WHERE chain_id=%d AND entity_id IS NOT NULL AND entity_id IN (SELECT entity_id FROM onchain.entity_registry FINAL) AND address IN (SELECT counterparty_address FROM activity WHERE counterparty_address!='')) AS known_entity,
greatest((SELECT toString(max(ingested_at)) FROM transfers),(SELECT toString(max(ingested_at)) FROM activity),(SELECT toString(max(updated_at)) FROM positions)) AS last_updated`, activityPredicate, transferPredicate, positionPredicate, chainID)
}

func normalizeWindow(filter Filter, now time.Time) (Window, string, error) {
	name := strings.ToUpper(strings.TrimSpace(filter.Window))
	if name == "" {
		name = "30D"
	}
	end := now
	var start time.Time
	switch name {
	case "24H":
		start = end.Add(-24 * time.Hour)
	case "7D":
		start = end.AddDate(0, 0, -7)
	case "30D":
		start = end.AddDate(0, 0, -30)
	case "90D":
		start = end.AddDate(0, 0, -90)
	case "1Y":
		start = end.AddDate(-1, 0, 0)
	case "ALL":
		return Window{Name: name}, "", nil
	case "CUSTOM":
		if filter.Start == nil || filter.End == nil {
			return Window{}, "", fmt.Errorf("%w: custom window requires start and end", ErrInvalidInput)
		}
		start, end = filter.Start.UTC(), filter.End.UTC()
		if !start.Before(end) || end.Sub(start) > 10*365*24*time.Hour {
			return Window{}, "", fmt.Errorf("%w: invalid custom window", ErrInvalidInput)
		}
	default:
		return Window{}, "", fmt.Errorf("%w: unsupported window", ErrInvalidInput)
	}
	start = start.UTC().Truncate(time.Millisecond)
	end = end.UTC().Truncate(time.Millisecond)
	return Window{Name: name, Start: &start, End: &end}, fmt.Sprintf(" AND block_time >= parseDateTime64BestEffort('%s',3,'UTC') AND block_time < parseDateTime64BestEffort('%s',3,'UTC')", start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)), nil
}

func coverage(numerator, denominator uint64, scope string, available bool) Coverage {
	metric := Coverage{Numerator: numerator, Denominator: denominator, Unknown: denominator - numerator, Available: available, Scope: scope}
	if available && denominator > 0 {
		value := float64(numerator) * 100 / float64(denominator)
		metric.Percentage = &value
	}
	return metric
}

func uintValue(row map[string]any, key string) (uint64, bool) {
	value, ok := row[key]
	if !ok {
		return 0, false
	}
	var text string
	switch typed := value.(type) {
	case string:
		text = typed
	case json.Number:
		text = typed.String()
	case float64:
		if typed < 0 || math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed {
			return 0, false
		}
		text = strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return 0, false
	}
	result, err := strconv.ParseUint(text, 10, 64)
	return result, err == nil
}

func stringValue(row map[string]any, key string) string {
	value, _ := row[key].(string)
	if strings.HasPrefix(value, "1970-01-01") || strings.HasPrefix(value, "0000-00-00") {
		return ""
	}
	return value
}
