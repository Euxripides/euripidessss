package pricing

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

type PricedSwap struct {
	Swap          *NormalizedSwap
	TokenAddress  string
	TokenPriceUSD *big.Rat
	TokenVolume   *big.Rat
	LiquidityUSD  *big.Rat
	Route         string
}

type PriceBar struct {
	ChainID                      uint32    `json:"chain_id"`
	TokenAddress                 string    `json:"token_address"`
	Minute                       time.Time `json:"minute"`
	Open, High, Low, Close, VWAP string
	VolumeToken, VolumeUSD       string
	TradeCount                   uint64  `json:"trade_count"`
	PoolCount                    uint32  `json:"pool_count"`
	LiquidityUSD                 *string `json:"liquidity_usd,omitempty"`
	PriceSource                  string  `json:"price_source"`
	Confidence                   float32 `json:"confidence"`
	IsInterpolated               bool    `json:"is_interpolated"`
	IsLastKnown                  bool    `json:"is_last_known"`
	PriceAgeSeconds              uint64  `json:"price_age_seconds"`
	Route                        string  `json:"route"`
}

func AggregateMinute(swaps []PricedSwap, maxDeviationPct *big.Rat) ([]PriceBar, error) {
	if maxDeviationPct == nil || maxDeviationPct.Sign() < 0 {
		return nil, fmt.Errorf("%w: invalid deviation", ErrInvalidInput)
	}
	type key struct {
		chain  uint32
		token  string
		minute int64
	}
	groups := map[key][]PricedSwap{}
	for _, item := range swaps {
		if item.Swap == nil || item.TokenPriceUSD == nil || item.TokenVolume == nil || item.TokenPriceUSD.Sign() <= 0 || item.TokenVolume.Sign() <= 0 {
			continue
		}
		token, err := CanonicalTokenID(uint64(item.Swap.ChainID), item.TokenAddress)
		if err != nil {
			return nil, err
		}
		minute := item.Swap.BlockTime.UTC().Truncate(time.Minute)
		groups[key{item.Swap.ChainID, token, minute.Unix()}] = append(groups[key{item.Swap.ChainID, token, minute.Unix()}], item)
	}
	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].token != keys[j].token {
			return keys[i].token < keys[j].token
		}
		return keys[i].minute < keys[j].minute
	})
	out := make([]PriceBar, 0, len(keys))
	for _, k := range keys {
		items := groups[k]
		sort.SliceStable(items, func(i, j int) bool {
			a, b := items[i].Swap, items[j].Swap
			if !a.BlockTime.Equal(b.BlockTime) {
				return a.BlockTime.Before(b.BlockTime)
			}
			if a.BlockNumber != b.BlockNumber {
				return a.BlockNumber < b.BlockNumber
			}
			return a.LogIndex < b.LogIndex
		})
		prices := make([]*big.Rat, 0, len(items))
		for _, i := range items {
			prices = append(prices, new(big.Rat).Set(i.TokenPriceUSD))
		}
		sort.Slice(prices, func(i, j int) bool { return prices[i].Cmp(prices[j]) < 0 })
		median := prices[len(prices)/2]
		threshold := new(big.Rat).Quo(maxDeviationPct, big.NewRat(100, 1))
		valid := make([]PricedSwap, 0, len(items))
		for _, i := range items {
			delta := new(big.Rat).Sub(i.TokenPriceUSD, median)
			if delta.Sign() < 0 {
				delta.Neg(delta)
			}
			if median.Sign() > 0 && new(big.Rat).Quo(delta, median).Cmp(threshold) <= 0 {
				valid = append(valid, i)
			}
		}
		if len(valid) == 0 {
			continue
		}
		open := valid[0].TokenPriceUSD
		closePrice := valid[len(valid)-1].TokenPriceUSD
		high := new(big.Rat).Set(open)
		low := new(big.Rat).Set(open)
		volumeToken := new(big.Rat)
		volumeUSD := new(big.Rat)
		weighted := new(big.Rat)
		pools := map[string]struct{}{}
		routes := map[string]struct{}{}
		var liquidity *big.Rat
		for _, i := range valid {
			if i.TokenPriceUSD.Cmp(high) > 0 {
				high.Set(i.TokenPriceUSD)
			}
			if i.TokenPriceUSD.Cmp(low) < 0 {
				low.Set(i.TokenPriceUSD)
			}
			usd := new(big.Rat).Mul(i.TokenPriceUSD, i.TokenVolume)
			volumeToken.Add(volumeToken, i.TokenVolume)
			volumeUSD.Add(volumeUSD, usd)
			weighted.Add(weighted, new(big.Rat).Mul(i.TokenPriceUSD, usd))
			pools[i.Swap.PoolAddress] = struct{}{}
			routes[i.Route] = struct{}{}
			if i.LiquidityUSD != nil && (liquidity == nil || i.LiquidityUSD.Cmp(liquidity) > 0) {
				liquidity = new(big.Rat).Set(i.LiquidityUSD)
			}
		}
		vwap := new(big.Rat).Set(closePrice)
		if volumeUSD.Sign() > 0 {
			vwap.Quo(weighted, volumeUSD)
		}
		routeParts := make([]string, 0, len(routes))
		for r := range routes {
			routeParts = append(routeParts, r)
		}
		sort.Strings(routeParts)
		confidence := confidenceFor(len(valid), len(pools), liquidity)
		var liq *string
		if liquidity != nil {
			v := decimalRat(liquidity, 18)
			liq = &v
		}
		out = append(out, PriceBar{ChainID: k.chain, TokenAddress: k.token, Minute: time.Unix(k.minute, 0).UTC(), Open: decimalRat(open, 18), High: decimalRat(high, 18), Low: decimalRat(low, 18), Close: decimalRat(closePrice, 18), VWAP: decimalRat(vwap, 18), VolumeToken: decimalRat(volumeToken, 30), VolumeUSD: decimalRat(volumeUSD, 18), TradeCount: uint64(len(valid)), PoolCount: uint32(len(pools)), LiquidityUSD: liq, PriceSource: "DEX_RECONSTRUCTED", Confidence: confidence, Route: strings.Join(routeParts, " | ")})
	}
	return out, nil
}

func confidenceFor(trades, pools int, liquidity *big.Rat) float32 {
	score := float32(0.45)
	if trades >= 10 {
		score += 0.20
	} else if trades >= 2 {
		score += 0.10
	}
	if pools >= 2 {
		score += 0.15
	}
	if liquidity != nil && liquidity.Cmp(big.NewRat(100000, 1)) >= 0 {
		score += 0.20
	} else if liquidity != nil && liquidity.Cmp(big.NewRat(1000, 1)) >= 0 {
		score += 0.10
	}
	if score > 0.99 {
		score = 0.99
	}
	return score
}
func decimalRat(value *big.Rat, places int) string {
	if value == nil {
		return "0"
	}
	return value.FloatString(places)
}

type PriceBarRepository struct{ client ClickHouseClient }

func NewPriceBarRepository(client ClickHouseClient) *PriceBarRepository {
	return &PriceBarRepository{client: client}
}
func (r *PriceBarRepository) Put(ctx context.Context, bars []PriceBar) error {
	if len(bars) == 0 {
		return nil
	}
	var body bytes.Buffer
	w := csv.NewWriter(&body)
	now := time.Now().UTC()
	canonical := make([]HistoricalPrice, 0, len(bars))
	for _, b := range bars {
		token, err := CanonicalTokenID(uint64(b.ChainID), b.TokenAddress)
		if err != nil {
			return err
		}
		liq := `\N`
		if b.LiquidityUSD != nil {
			liq = *b.LiquidityUSD
		}
		row := []string{strconv.FormatUint(uint64(b.ChainID), 10), token, formatTime(b.Minute), b.Open, b.High, b.Low, b.Close, b.VWAP, b.VolumeToken, b.VolumeUSD, strconv.FormatUint(b.TradeCount, 10), strconv.FormatUint(uint64(b.PoolCount), 10), liq, b.PriceSource, strconv.FormatFloat(float64(b.Confidence), 'f', 6, 32), boolCSV(b.IsInterpolated), boolCSV(b.IsLastKnown), strconv.FormatUint(b.PriceAgeSeconds, 10), b.Route, formatTime(now)}
		if err = w.Write(row); err != nil {
			return err
		}
		confidence := "LOW"
		if b.Confidence >= 0.9 {
			confidence = "HIGH"
		} else if b.Confidence >= 0.7 {
			confidence = "MEDIUM"
		}
		canonical = append(canonical, HistoricalPrice{ChainID: b.ChainID, TokenID: token, PriceTime: b.Minute, PriceUSD: b.Close, Source: b.PriceSource, Confidence: confidence, Resolution: Resolution1Minute, LiquidityUSD: b.LiquidityUSD, VolumeUSD: &b.VolumeUSD, IsVerified: b.Confidence >= 0.85, PriceVersion: "dex-1m-v1", SourceVersion: "pancakeswap-v1", SourcePriority: 20})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	columns := []string{"chain_id", "token_address", "minute", "open", "high", "low", "close", "vwap", "volume_token", "volume_usd", "trade_count", "pool_count", "liquidity_usd", "price_source", "confidence", "is_interpolated", "is_last_known", "price_age_seconds", "route", "updated_at"}
	if err := r.client.InsertCSV(ctx, "onchain.token_price_1m", columns, &body); err != nil {
		return err
	}
	repository := NewRepository(r.client)
	if err := repository.PutPrices(ctx, canonical); err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, price := range canonical {
		if _, ok := seen[price.TokenID]; ok {
			continue
		}
		seen[price.TokenID] = struct{}{}
		if err := repository.RefreshCoverage(ctx, price.ChainID, price.TokenID); err != nil {
			return err
		}
	}
	return nil
}

func (r *PriceBarRepository) Candles(ctx context.Context, chainID uint32, token string, from, to time.Time, interval Resolution) ([]Candle, error) {
	tokenID, err := CanonicalTokenID(uint64(chainID), token)
	if err != nil {
		return nil, err
	}
	bucket := map[Resolution]string{Resolution1Minute: "toStartOfMinute(minute)", Resolution5Minute: "toStartOfInterval(minute, INTERVAL 5 MINUTE)", Resolution15Minute: "toStartOfInterval(minute, INTERVAL 15 MINUTE)", Resolution1Hour: "toStartOfHour(minute)", Resolution4Hour: "toStartOfInterval(minute, INTERVAL 4 HOUR)", Resolution1Day: "toStartOfDay(minute)"}[interval]
	if bucket == "" || !to.After(from) {
		return nil, ErrInvalidInput
	}
	query := fmt.Sprintf(`SELECT %s bucket,argMin(open,minute) open,max(high) high,min(low) low,argMax(close,minute) close,sum(volume_usd) volume_usd,sum(trade_count) trade_count,any(price_source) source FROM onchain.token_price_1m FINAL WHERE chain_id=%d AND token_address='%s' AND minute>=toDateTime64('%s',3,'UTC') AND minute<=toDateTime64('%s',3,'UTC') GROUP BY bucket ORDER BY bucket`, bucket, chainID, tokenID, formatTime(from), formatTime(to))
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]Candle, 0, len(rows))
	for _, row := range rows {
		t, e := parseTime(row["bucket"])
		if e != nil {
			return nil, e
		}
		n, _ := strconv.ParseUint(text(row["trade_count"]), 10, 64)
		out = append(out, Candle{Time: t, Open: text(row["open"]), High: text(row["high"]), Low: text(row["low"]), Close: text(row["close"]), VolumeUSD: text(row["volume_usd"]), TradeCount: n, Source: text(row["source"])})
	}
	return out, nil
}
