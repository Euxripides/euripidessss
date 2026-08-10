package financialpnl

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidQuery = errors.New("invalid pnl query")
	ErrQueryFailed  = errors.New("pnl query failed")
	addressPattern  = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
	nativePattern   = regexp.MustCompile(`^native:[1-9][0-9]{0,9}$`)
)

type Client interface {
	QueryJSON(context.Context, string) ([]map[string]any, error)
	InsertCSV(context.Context, string, []string, io.Reader) error
}

type Repository struct{ client Client }

func NewRepository(client Client) *Repository { return &Repository{client: client} }

func ValidateQuery(q Query) error {
	if q.ChainID == 0 || !addressPattern.MatchString(strings.ToLower(q.Address)) ||
		(!addressPattern.MatchString(strings.ToLower(q.Token)) && !nativePattern.MatchString(strings.ToLower(q.Token))) || q.AsOf.IsZero() {
		return ErrInvalidQuery
	}
	if strings.HasPrefix(strings.ToLower(q.Token), "native:") && strings.TrimPrefix(strings.ToLower(q.Token), "native:") != strconv.FormatUint(uint64(q.ChainID), 10) {
		return ErrInvalidQuery
	}
	return nil
}

func (r *Repository) LoadEvents(ctx context.Context, q Query) ([]PositionEvent, error) {
	if err := ValidateQuery(q); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT event_time,block_number,tx_hash,event_index,event_type,toString(amount_decimal) amount_decimal,
toString(usd_value) usd_value,toString(gas_usd) gas_usd,semantic_source,semantic_confidence,
price_version,data_snapshot_version
FROM onchain.financial_position_events FINAL
WHERE chain_id=%d AND address='%s' AND token_address='%s' AND event_time<=parseDateTime64BestEffort('%s')
ORDER BY event_time,block_number,tx_hash,event_index`, q.ChainID, strings.ToLower(q.Address), strings.ToLower(q.Token), q.AsOf.UTC().Format(time.RFC3339Nano))
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, ErrQueryFailed
	}
	events := make([]PositionEvent, 0, len(rows))
	for _, row := range rows {
		eventTime, err := parseTime(textValue(row["event_time"]))
		if err != nil {
			return nil, ErrQueryFailed
		}
		amount := textValue(row["amount_decimal"])
		usd, gas := nullableText(row["usd_value"]), nullableText(row["gas_usd"])
		events = append(events, PositionEvent{Time: eventTime, BlockNumber: uintValue(row["block_number"]),
			TransactionHash: textValue(row["tx_hash"]), EventIndex: uint32(uintValue(row["event_index"])),
			Type: EventType(textValue(row["event_type"])), Amount: amount, USDValue: usd, GasUSD: gas,
			SemanticSource: textValue(row["semantic_source"]), SemanticConfidence: textValue(row["semantic_confidence"]),
			PriceVersion: textValue(row["price_version"]), DataSnapshotVersion: textValue(row["data_snapshot_version"])})
	}
	return events, nil
}

func (r *Repository) CurrentPrice(ctx context.Context, q Query) (*Price, error) {
	if err := ValidateQuery(q); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT toString(price_usd) price_usd,price_time,source,confidence,
concat(if(price_version!='',price_version,'v1'),':',if(source_version!='',source_version,source)) price_version
FROM onchain.token_prices FINAL
WHERE chain_id=%d AND token_address='%s' AND price_time<=parseDateTime64BestEffort('%s')
ORDER BY price_time DESC,updated_at DESC LIMIT 1`, q.ChainID, strings.ToLower(q.Token), q.AsOf.UTC().Format(time.RFC3339Nano))
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, ErrQueryFailed
	}
	if len(rows) == 0 {
		return nil, nil
	}
	priceTime, err := parseTime(textValue(rows[0]["price_time"]))
	if err != nil {
		return nil, ErrQueryFailed
	}
	return &Price{USD: textValue(rows[0]["price_usd"]), Time: priceTime, Source: textValue(rows[0]["source"]),
		Confidence: textValue(rows[0]["confidence"]), Version: textValue(rows[0]["price_version"])}, nil
}

func (r *Repository) SaveSnapshot(ctx context.Context, result Result) (string, error) {
	if err := ValidateQuery(Query{ChainID: result.ChainID, Address: result.Address, Token: result.Token, AsOf: result.AsOf}); err != nil {
		return "", err
	}
	key := fmt.Sprintf("%d|%s|%s|%s|%s|%s|%s", result.ChainID, strings.ToLower(result.Address), strings.ToLower(result.Token), result.AsOf.UTC().Format(time.RFC3339Nano), result.AlgorithmVersion, result.PriceVersion, result.DataSnapshotVersion)
	snapshotID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
	computed := time.Now().UTC().Format("2006-01-02 15:04:05.000")
	row := []string{snapshotID, strconv.FormatUint(uint64(result.ChainID), 10), strings.ToLower(result.Address), strings.ToLower(result.Token),
		result.AsOf.UTC().Format("2006-01-02 15:04:05.000"), result.RealizedPnLUSD, result.RealizedProceedsCoveredUSD,
		result.RealizedCostBasisUSD, result.RealizedGasUSD, result.SoldAmount, result.KnownSoldAmount, result.KnownCostBasisRatio,
		result.RealizedPnLStatus, result.RealizedPnLScope, result.FinancialConfidence,
		result.PositionAmount, result.KnownPositionAmount, result.RemainingKnownCostUSD, nullableString(result.PositionMarketValueUSD),
		nullableString(result.KnownUnrealizedPnLUSD), result.UnrealizedCoverage, nullableString(result.CurrentPriceUSD), nullableTime(result.CurrentPriceTime),
		result.CurrentPriceSource, result.CurrentPriceStatus, result.AlgorithmVersion, result.PriceVersion, result.DataSnapshotVersion,
		result.SnapshotVersion, computed}
	columns := []string{"snapshot_id", "chain_id", "address", "token_address", "as_of", "realized_pnl_usd", "realized_proceeds_covered_usd", "realized_cost_basis_usd", "realized_gas_usd", "sold_amount", "known_sold_amount", "known_cost_basis_ratio", "realized_pnl_status", "realized_pnl_scope", "financial_confidence", "position_amount", "known_position_amount", "remaining_known_cost_usd", "position_market_value_usd", "known_unrealized_pnl_usd", "unrealized_coverage", "current_price_usd", "current_price_time", "current_price_source", "current_price_status", "algorithm_version", "price_version", "data_snapshot_version", "snapshot_version", "computed_at"}
	if err := r.insert(ctx, "onchain.financial_pnl_snapshots", columns, [][]string{row}); err != nil {
		return "", err
	}
	if len(result.Lots) > 0 {
		lotRows := make([][]string, 0, len(result.Lots))
		for i, lot := range result.Lots {
			lotRows = append(lotRows, []string{snapshotID, strconv.Itoa(i), strconv.FormatUint(uint64(result.ChainID), 10), strings.ToLower(result.Address), strings.ToLower(result.Token),
				lot.AcquiredTime.UTC().Format("2006-01-02 15:04:05.000"), lot.AcquiredAmount, lot.RemainingAmount, nullableString(lot.CostUSD), nullableString(lot.RemainingCostUSD), lot.SourceTx, string(lot.SourceType), lot.CostBasisStatus, result.AlgorithmVersion, result.PriceVersion, result.DataSnapshotVersion, computed})
		}
		lotColumns := []string{"snapshot_id", "lot_index", "chain_id", "address", "token_address", "acquired_time", "acquired_amount", "remaining_amount", "cost_usd", "remaining_cost_usd", "source_tx", "source_type", "cost_basis_status", "algorithm_version", "price_version", "data_snapshot_version", "computed_at"}
		if err := r.insert(ctx, "onchain.token_position_lots", lotColumns, lotRows); err != nil {
			return "", err
		}
	}
	return snapshotID, nil
}

func (r *Repository) insert(ctx context.Context, table string, columns []string, rows [][]string) error {
	var buffer bytes.Buffer
	w := csv.NewWriter(&buffer)
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return ErrQueryFailed
		}
	}
	w.Flush()
	if w.Error() != nil {
		return ErrQueryFailed
	}
	if err := r.client.InsertCSV(ctx, table, columns, &buffer); err != nil {
		return ErrQueryFailed
	}
	return nil
}

func nullableString(value *string) string {
	if value == nil {
		return `\N`
	}
	return *value
}
func nullableTime(value *time.Time) string {
	if value == nil {
		return `\N`
	}
	return value.UTC().Format("2006-01-02 15:04:05.000")
}
func nullableText(value any) *string {
	if value == nil {
		return nil
	}
	text := textValue(value)
	if text == "" || text == "\\N" || strings.EqualFold(text, "null") {
		return nil
	}
	return &text
}
func textValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
func uintValue(value any) uint64 {
	parsed, _ := strconv.ParseUint(strings.TrimSpace(textValue(value)), 10, 64)
	return parsed
}
func parseTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, ErrQueryFailed
}

type Service struct {
	repo   *Repository
	engine Engine
}

func NewService(repo *Repository, staleAfter time.Duration) *Service {
	return &Service{repo: repo, engine: Engine{StaleAfter: staleAfter}}
}

func (s *Service) Calculate(ctx context.Context, q Query, persist bool) (Result, string, error) {
	if err := ValidateQuery(q); err != nil {
		return Result{}, "", err
	}
	events, err := s.repo.LoadEvents(ctx, q)
	if err != nil {
		return Result{}, "", err
	}
	price, err := s.repo.CurrentPrice(ctx, q)
	if err != nil {
		return Result{}, "", err
	}
	result, err := s.engine.Calculate(q, events, price)
	if err != nil {
		return Result{}, "", err
	}
	if result.DataSnapshotVersion == "" {
		versions := make([]string, 0, len(events))
		for _, event := range events {
			if event.DataSnapshotVersion != "" {
				versions = append(versions, event.DataSnapshotVersion)
			}
		}
		sort.Strings(versions)
		if len(versions) > 0 {
			result.DataSnapshotVersion = versions[len(versions)-1]
		}
	}
	if !persist {
		return result, "", nil
	}
	id, err := s.repo.SaveSnapshot(ctx, result)
	return result, id, err
}
