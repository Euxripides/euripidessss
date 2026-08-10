package pricing

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"time"
)

type ClickHouseClient interface {
	QueryJSON(ctx context.Context, query string) ([]map[string]any, error)
	InsertCSV(ctx context.Context, table string, columns []string, rows io.Reader) error
}

type clickHouseExecutor interface {
	Exec(context.Context, string) error
}

type PriceRepository interface {
	Candidates(ctx context.Context, chainID uint32, tokenID string, at time.Time, radius time.Duration) ([]HistoricalPrice, error)
	Buckets(ctx context.Context, chainID uint32, tokenID string, resolution Resolution, from, to time.Time) ([]time.Time, error)
	PutPrices(ctx context.Context, prices []HistoricalPrice) error
}

type Repository struct{ client ClickHouseClient }

func NewRepository(client ClickHouseClient) *Repository { return &Repository{client: client} }

func (r *Repository) Candidates(ctx context.Context, chainID uint32, tokenID string, at time.Time, radius time.Duration) ([]HistoricalPrice, error) {
	if r == nil || r.client == nil || radius <= 0 {
		return nil, fmt.Errorf("%w: repository unavailable or invalid radius", ErrInvalidInput)
	}
	from, to := at.UTC().Add(-radius), at.UTC().Add(radius)
	query := fmt.Sprintf(`SELECT chain_id, token_address, price_time, toString(price_usd) price_usd, source, confidence,
resolution, toString(liquidity_usd) liquidity_usd, toString(volume_usd) volume_usd,
is_fallback, is_verified, price_version, source_version, source_priority
FROM onchain.token_prices FINAL
WHERE chain_id = %d AND token_address = '%s'
AND price_time BETWEEN parseDateTime64BestEffort('%s', 3, 'UTC') AND parseDateTime64BestEffort('%s', 3, 'UTC')
ORDER BY price_time ASC, source_priority ASC, source ASC`, chainID, tokenID, from.Format(time.RFC3339Nano), to.Format(time.RFC3339Nano))
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query local token prices: %w", err)
	}
	prices := make([]HistoricalPrice, 0, len(rows))
	for _, row := range rows {
		price, decodeErr := decodePrice(row)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode local token price: %w", decodeErr)
		}
		prices = append(prices, price)
	}
	return prices, nil
}

func (r *Repository) Buckets(ctx context.Context, chainID uint32, tokenID string, resolution Resolution, from, to time.Time) ([]time.Time, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("%w: repository unavailable", ErrInvalidInput)
	}
	query := fmt.Sprintf(`SELECT DISTINCT price_time FROM onchain.token_prices FINAL
WHERE chain_id = %d AND token_address = '%s' AND resolution = '%s'
AND price_time BETWEEN parseDateTime64BestEffort('%s', 3, 'UTC') AND parseDateTime64BestEffort('%s', 3, 'UTC')
ORDER BY price_time`, chainID, tokenID, resolution, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query local price buckets: %w", err)
	}
	buckets := make([]time.Time, 0, len(rows))
	for _, row := range rows {
		value, decodeErr := parseTime(row["price_time"])
		if decodeErr != nil {
			return nil, decodeErr
		}
		buckets = append(buckets, value)
	}
	return buckets, nil
}

func (r *Repository) PutPrices(ctx context.Context, prices []HistoricalPrice) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("%w: repository unavailable", ErrInvalidInput)
	}
	if len(prices) == 0 {
		return nil
	}
	var buffer bytes.Buffer
	w := csv.NewWriter(&buffer)
	for _, price := range prices {
		tokenID, err := CanonicalTokenID(uint64(price.ChainID), price.TokenID)
		if err != nil || price.PriceTime.IsZero() || strings.TrimSpace(price.PriceUSD) == "" {
			return fmt.Errorf("%w: incomplete price row", ErrInvalidInput)
		}
		if _, err = price.Resolution.Duration(); err != nil {
			return err
		}
		row := []string{strconv.FormatUint(uint64(price.ChainID), 10), tokenID, formatTime(price.PriceTime), formatTime(price.PriceTime), formatTime(price.PriceTime), price.PriceUSD,
			price.Source, price.Confidence, string(price.Resolution), strconv.Itoa(int(price.SourcePriority)), nullable(price.LiquidityUSD), nullable(price.VolumeUSD), boolCSV(price.IsFallback), boolCSV(price.IsVerified),
			defaultText(price.PriceVersion, "v1"), defaultText(price.SourceVersion, "unknown"), formatTime(time.Now().UTC()), formatTime(time.Now().UTC()), formatTime(time.Now().UTC())}
		if err = w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	columns := []string{"chain_id", "token_address", "timestamp_bucket", "price_time", "time_bucket", "price_usd", "source", "confidence", "resolution", "source_priority", "liquidity_usd", "volume_usd", "is_fallback", "is_verified", "price_version", "source_version", "observed_at", "ingested_at", "updated_at"}
	if err := r.client.InsertCSV(ctx, "onchain.token_prices", columns, &buffer); err != nil {
		return fmt.Errorf("insert local token prices: %w", err)
	}
	return nil
}

type Coverage struct {
	ChainID       uint32    `json:"chain_id"`
	TokenAddress  string    `json:"token_address"`
	FirstPriceAt  time.Time `json:"first_price_at"`
	LastPriceAt   time.Time `json:"last_price_at"`
	MinuteCount   uint64    `json:"minute_count"`
	TradeCount    uint64    `json:"trade_count"`
	CoverageRatio float64   `json:"coverage_ratio"`
}

type ResolutionAudit struct {
	ChainID           uint32
	TokenAddress      string
	Timestamp         time.Time
	ResolvedPrice     *string
	Route, SourcePool string
	HopCount          uint8
	Confidence        float32
	Status, Reason    string
}

func (r *Repository) LogResolution(ctx context.Context, audit ResolutionAudit) error {
	tokenID, err := CanonicalTokenID(uint64(audit.ChainID), audit.TokenAddress)
	if err != nil || audit.Timestamp.IsZero() {
		return fmt.Errorf("%w: invalid resolution audit", ErrInvalidInput)
	}
	price := `\N`
	if audit.ResolvedPrice != nil {
		if _, ok := new(big.Rat).SetString(*audit.ResolvedPrice); !ok {
			return fmt.Errorf("%w: invalid audit price", ErrInvalidInput)
		}
		price = *audit.ResolvedPrice
	}
	var body bytes.Buffer
	w := csv.NewWriter(&body)
	row := []string{strconv.FormatUint(uint64(audit.ChainID), 10), tokenID, formatTime(audit.Timestamp), price, bounded(audit.Route, 2048), bounded(audit.SourcePool, 128), strconv.Itoa(int(audit.HopCount)), strconv.FormatFloat(float64(audit.Confidence), 'f', 6, 32), bounded(audit.Status, 64), bounded(audit.Reason, 4096), formatTime(time.Now().UTC())}
	if err = w.Write(row); err != nil {
		return err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return r.client.InsertCSV(ctx, "onchain.token_price_resolution_log", []string{"chain_id", "token_address", "timestamp", "resolved_price", "route", "source_pool", "hop_count", "confidence", "status", "reason", "created_at"}, &body)
}

func (r *Repository) RefreshCoverage(ctx context.Context, chainID uint32, token string) error {
	tokenID, err := CanonicalTokenID(uint64(chainID), token)
	if err != nil {
		return err
	}
	executor, ok := r.client.(clickHouseExecutor)
	if !ok {
		return nil
	}
	query := fmt.Sprintf(`INSERT INTO onchain.token_price_coverage (chain_id,token_address,first_price_at,last_price_at,first_block,last_block,minute_count,trade_count,coverage_ratio,updated_at)
SELECT chain_id,token_address,min(price_time),max(price_time),0,0,uniqExact(toStartOfMinute(price_time)),
(SELECT sum(trade_count) FROM onchain.token_price_1m FINAL WHERE chain_id=%d AND token_address='%s'),
if(dateDiff('minute',min(price_time),max(price_time))+1=0,0,uniqExact(toStartOfMinute(price_time))/(dateDiff('minute',min(price_time),max(price_time))+1)),now64(3)
FROM onchain.token_prices FINAL WHERE chain_id=%d AND token_address='%s' GROUP BY chain_id,token_address`, chainID, tokenID, chainID, tokenID)
	return executor.Exec(ctx, query)
}

func (r *Repository) Coverage(ctx context.Context, chainID uint32, token string) (*Coverage, error) {
	tokenID, err := CanonicalTokenID(uint64(chainID), token)
	if err != nil {
		return nil, err
	}
	rows, err := r.client.QueryJSON(ctx, fmt.Sprintf(`SELECT chain_id,token_address,first_price_at,last_price_at,minute_count,trade_count,coverage_ratio FROM onchain.token_price_coverage FINAL WHERE chain_id=%d AND token_address='%s' LIMIT 1`, chainID, tokenID))
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	first, err := parseTime(rows[0]["first_price_at"])
	if err != nil {
		return nil, err
	}
	last, err := parseTime(rows[0]["last_price_at"])
	if err != nil {
		return nil, err
	}
	minutes, _ := strconv.ParseUint(text(rows[0]["minute_count"]), 10, 64)
	trades, _ := strconv.ParseUint(text(rows[0]["trade_count"]), 10, 64)
	ratio, _ := strconv.ParseFloat(text(rows[0]["coverage_ratio"]), 64)
	return &Coverage{ChainID: chainID, TokenAddress: tokenID, FirstPriceAt: first, LastPriceAt: last, MinuteCount: minutes, TradeCount: trades, CoverageRatio: ratio}, nil
}

func decodePrice(row map[string]any) (HistoricalPrice, error) {
	chain, err := parseUint(row["chain_id"], 32)
	if err != nil {
		return HistoricalPrice{}, err
	}
	priceTime, err := parseTime(row["price_time"])
	if err != nil {
		return HistoricalPrice{}, err
	}
	fallback, err := parseBool(row["is_fallback"])
	if err != nil {
		return HistoricalPrice{}, err
	}
	verified, err := parseBool(row["is_verified"])
	if err != nil {
		return HistoricalPrice{}, err
	}
	priority, err := parseUint(row["source_priority"], 8)
	if err != nil {
		return HistoricalPrice{}, err
	}
	out := HistoricalPrice{ChainID: uint32(chain), TokenID: text(row["token_address"]), PriceTime: priceTime, PriceUSD: text(row["price_usd"]), Source: text(row["source"]), Confidence: text(row["confidence"]), Resolution: Resolution(text(row["resolution"])), IsFallback: fallback, IsVerified: verified, PriceVersion: text(row["price_version"]), SourceVersion: text(row["source_version"]), SourcePriority: uint8(priority)}
	if value := text(row["liquidity_usd"]); value != "" {
		out.LiquidityUSD = &value
	}
	if value := text(row["volume_usd"]); value != "" {
		out.VolumeUSD = &value
	}
	return out, nil
}

func text(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return ""
	}
}
func parseUint(value any, bits int) (uint64, error) { return strconv.ParseUint(text(value), 10, bits) }
func parseBool(value any) (bool, error) {
	v := text(value)
	if v == "1" {
		return true, nil
	}
	if v == "0" {
		return false, nil
	}
	return strconv.ParseBool(v)
}
func parseTime(value any) (time.Time, error) {
	if t, ok := value.(time.Time); ok {
		return t.UTC(), nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, text(value)); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid ClickHouse time")
}
func formatTime(value time.Time) string { return value.UTC().Format("2006-01-02 15:04:05.000") }
func nullable(value *string) string {
	if value == nil {
		return `\N`
	}
	return *value
}
func boolCSV(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
func defaultText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
