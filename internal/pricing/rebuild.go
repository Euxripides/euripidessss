package pricing

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	bscWBNB        = "0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c"
	bscWETH        = "0x2170ed0880ac9a755fd29b2688956bd959f933f8"
	datasetDEXSwap = "DEX_SWAP"
)

type poolFactory struct {
	ProtocolID, DEX, Version, EventName string
	DefaultFeeBPS                       uint32
}

// Official BSC mainnet deployments. Pool discovery accepts factory events only
// from this reviewed registry so a spoofed PairCreated/PoolCreated topic cannot
// be promoted to a verified PancakeSwap pool.
var bscPoolFactories = map[string]poolFactory{
	"0xca143ce32fe78f1f7019d7d551a6402fc5350c73": {ProtocolID: "pancakeswap_v2", DEX: "PANCAKESWAP", Version: "V2", EventName: "PairCreated", DefaultFeeBPS: 25},
	"0x0bfbcf9fa4f9c56b0f40a671ad40e0805a091865": {ProtocolID: "pancakeswap_v3", DEX: "PANCAKESWAP", Version: "V3", EventName: "PoolCreatedV3"},
}

func supportedBSCFactory(address, eventName string) (poolFactory, bool) {
	factory, ok := bscPoolFactories[strings.ToLower(strings.TrimSpace(address))]
	return factory, ok && factory.EventName == strings.TrimSpace(eventName)
}

type DEXRepository struct{ client ClickHouseClient }

func NewDEXRepository(client ClickHouseClient) *DEXRepository { return &DEXRepository{client: client} }

type PoolDiscoveryResult struct {
	Discovered       uint64 `json:"discovered"`
	Written          uint64 `json:"written"`
	SkippedMetadata  uint64 `json:"skipped_metadata"`
	SkippedUntrusted uint64 `json:"skipped_untrusted"`
}

func (r *DEXRepository) DiscoverPools(ctx context.Context, from, to time.Time) (PoolDiscoveryResult, error) {
	factories := make([]string, 0, len(bscPoolFactories))
	for address := range bscPoolFactories {
		factories = append(factories, "'"+address+"'")
	}
	sort.Strings(factories)
	query := fmt.Sprintf(`SELECT block_number,block_time,contract_address,event_name,decoded_fields FROM onchain.parsed_events FINAL WHERE chain_id=56 AND contract_address IN (%s) AND event_name IN ('PairCreated','PoolCreatedV3') AND block_time>=toDateTime64('%s',3,'UTC') AND block_time<=toDateTime64('%s',3,'UTC') ORDER BY block_number`, strings.Join(factories, ","), formatTime(from), formatTime(to))
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return PoolDiscoveryResult{}, err
	}
	result := PoolDiscoveryResult{Discovered: uint64(len(rows))}
	if len(rows) == 0 {
		return result, nil
	}
	type candidate struct {
		pool    PoolMetadata
		block   uint64
		created time.Time
	}
	candidates := make([]candidate, 0, len(rows))
	addresses := map[string]struct{}{}
	for _, row := range rows {
		factory, trusted := supportedBSCFactory(text(row["contract_address"]), text(row["event_name"]))
		if !trusted {
			result.SkippedUntrusted++
			continue
		}
		var fields map[string]any
		if json.Unmarshal([]byte(text(row["decoded_fields"])), &fields) != nil {
			continue
		}
		token0 := strings.ToLower(text(fields["token0"]))
		token1 := strings.ToLower(text(fields["token1"]))
		poolAddress := strings.ToLower(text(fields["pair"]))
		fee := factory.DefaultFeeBPS
		if poolAddress == "" {
			poolAddress = strings.ToLower(text(fields["pool"]))
			parsed, _ := strconv.ParseUint(text(fields["fee"]), 10, 32)
			fee = uint32(parsed / 100)
		}
		if !addressRE.MatchString(token0) || !addressRE.MatchString(token1) || !addressRE.MatchString(poolAddress) {
			continue
		}
		block, _ := strconv.ParseUint(text(row["block_number"]), 10, 64)
		created, e := parseTime(row["block_time"])
		if e != nil {
			continue
		}
		candidates = append(candidates, candidate{pool: PoolMetadata{ChainID: 56, DEX: factory.DEX, Version: factory.Version, ProtocolID: factory.ProtocolID, FactoryAddress: strings.ToLower(text(row["contract_address"])), PoolAddress: poolAddress, Token0: token0, Token1: token1, FeeBPS: fee, Verified: true}, block: block, created: created})
		addresses[token0] = struct{}{}
		addresses[token1] = struct{}{}
	}
	if len(candidates) == 0 {
		return result, nil
	}
	quoted := make([]string, 0, len(addresses))
	for address := range addresses {
		quoted = append(quoted, "'"+address+"'")
	}
	sort.Strings(quoted)
	metadataRows, err := r.client.QueryJSON(ctx, `SELECT contract_address,symbol,decimals FROM onchain.token_metadata_registry FINAL WHERE chain_id=56 AND contract_address IN (`+strings.Join(quoted, ",")+")")
	if err != nil {
		return result, err
	}
	type meta struct {
		symbol   string
		decimals uint8
	}
	metadata := map[string]meta{}
	for _, row := range metadataRows {
		d, e := strconv.ParseUint(text(row["decimals"]), 10, 8)
		if e == nil {
			metadata[strings.ToLower(text(row["contract_address"]))] = meta{text(row["symbol"]), uint8(d)}
		}
	}
	var body bytes.Buffer
	w := csv.NewWriter(&body)
	now := time.Now().UTC()
	for _, c := range candidates {
		m0, ok0 := metadata[c.pool.Token0]
		m1, ok1 := metadata[c.pool.Token1]
		if !ok0 || !ok1 {
			result.SkippedMetadata++
			continue
		}
		c.pool.Token0Decimals = m0.decimals
		c.pool.Token1Decimals = m1.decimals
		row := []string{"56", c.pool.DEX, c.pool.Version, c.pool.ProtocolID, c.pool.FactoryAddress, c.pool.PoolAddress, c.pool.Token0, c.pool.Token1, m0.symbol, m1.symbol, strconv.Itoa(int(m0.decimals)), strconv.Itoa(int(m1.decimals)), strconv.FormatUint(uint64(c.pool.FeeBPS), 10), strconv.FormatUint(c.block, 10), formatTime(c.created), "1", boolCSV(c.pool.Verified), strconv.FormatFloat(c.pool.LiquidityScore, 'f', 6, 64), formatTime(now)}
		if err = w.Write(row); err != nil {
			return result, err
		}
		result.Written++
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return result, err
	}
	if result.Written == 0 {
		return result, nil
	}
	columns := []string{"chain_id", "dex", "version", "protocol_id", "factory_address", "pool_address", "token0_address", "token1_address", "token0_symbol", "token1_symbol", "token0_decimals", "token1_decimals", "fee_bps", "created_block", "created_at", "is_active", "verified", "liquidity_score", "updated_at"}
	return result, r.client.InsertCSV(ctx, "onchain.dex_pools", columns, &body)
}

func (r *DEXRepository) PoolsForToken(ctx context.Context, token string) ([]PoolMetadata, error) {
	tokenID, err := CanonicalTokenID(56, token)
	if err != nil {
		return nil, err
	}
	trustedFactories := make([]string, 0, len(bscPoolFactories))
	for address := range bscPoolFactories {
		trustedFactories = append(trustedFactories, "'"+address+"'")
	}
	sort.Strings(trustedFactories)
	query := fmt.Sprintf(`SELECT chain_id,dex,version,protocol_id,factory_address,pool_address,token0_address,token1_address,token0_decimals,token1_decimals,fee_bps,verified,liquidity_score FROM onchain.dex_pools FINAL WHERE chain_id=56 AND is_active AND factory_address IN (%s) AND (token0_address='%s' OR token1_address='%s') ORDER BY verified DESC,liquidity_score DESC,updated_at DESC LIMIT 100`, strings.Join(trustedFactories, ","), tokenID, tokenID)
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]PoolMetadata, 0, len(rows))
	for _, row := range rows {
		d0, e0 := strconv.ParseUint(text(row["token0_decimals"]), 10, 8)
		d1, e1 := strconv.ParseUint(text(row["token1_decimals"]), 10, 8)
		fee, e2 := strconv.ParseUint(text(row["fee_bps"]), 10, 32)
		if e0 != nil || e1 != nil || e2 != nil {
			continue
		}
		verified, _ := parseBool(row["verified"])
		liquidityScore, _ := strconv.ParseFloat(text(row["liquidity_score"]), 64)
		protocolID := text(row["protocol_id"])
		if factory, trusted := bscPoolFactories[strings.ToLower(text(row["factory_address"]))]; trusted {
			verified = true
			if protocolID == "" {
				protocolID = factory.ProtocolID
			}
		}
		out = append(out, PoolMetadata{ChainID: 56, DEX: text(row["dex"]), Version: text(row["version"]), ProtocolID: protocolID, FactoryAddress: text(row["factory_address"]), PoolAddress: text(row["pool_address"]), Token0: text(row["token0_address"]), Token1: text(row["token1_address"]), Token0Decimals: uint8(d0), Token1Decimals: uint8(d1), FeeBPS: uint32(fee), Verified: verified, LiquidityScore: liquidityScore})
	}
	return out, nil
}

func (r *DEXRepository) Swaps(ctx context.Context, pools []PoolMetadata, from, to time.Time) ([]NormalizedSwap, error) {
	if len(pools) == 0 {
		return nil, nil
	}
	byAddress := map[string]PoolMetadata{}
	quoted := make([]string, 0, len(pools))
	for _, pool := range pools {
		address := strings.ToLower(pool.PoolAddress)
		if !addressRE.MatchString(address) {
			return nil, ErrInvalidInput
		}
		byAddress[address] = pool
		quoted = append(quoted, "'"+address+"'")
	}
	query := fmt.Sprintf(`SELECT block_number,block_time,tx_hash,log_index,contract_address,event_name,decoded_fields,ingest_job_id FROM onchain.parsed_events FINAL WHERE chain_id=56 AND contract_address IN (%s) AND event_name IN ('Swap','SwapV3') AND block_time>=toDateTime64('%s',3,'UTC') AND block_time<=toDateTime64('%s',3,'UTC') ORDER BY block_time,block_number,log_index`, strings.Join(quoted, ","), formatTime(from), formatTime(to))
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]NormalizedSwap, 0, len(rows))
	for _, row := range rows {
		pool := byAddress[strings.ToLower(text(row["contract_address"]))]
		var fields map[string]any
		if json.Unmarshal([]byte(text(row["decoded_fields"])), &fields) != nil {
			continue
		}
		var a0, a1 *big.Int
		if pool.Version == "V2" {
			a0In, _ := new(big.Int).SetString(text(fields["amount0In"]), 10)
			a1In, _ := new(big.Int).SetString(text(fields["amount1In"]), 10)
			a0Out, _ := new(big.Int).SetString(text(fields["amount0Out"]), 10)
			a1Out, _ := new(big.Int).SetString(text(fields["amount1Out"]), 10)
			if a0In == nil || a1In == nil || a0Out == nil || a1Out == nil {
				continue
			}
			a0 = new(big.Int).Sub(a0In, a0Out)
			a1 = new(big.Int).Sub(a1In, a1Out)
		} else {
			a0, _ = new(big.Int).SetString(text(fields["amount0"]), 10)
			a1, _ = new(big.Int).SetString(text(fields["amount1"]), 10)
			if a0 == nil || a1 == nil {
				continue
			}
		}
		if a0.Sign() == 0 || a1.Sign() == 0 || a0.Sign() == a1.Sign() {
			continue
		}
		block, _ := strconv.ParseUint(text(row["block_number"]), 10, 64)
		index, _ := strconv.ParseUint(text(row["log_index"]), 10, 32)
		blockTime, e := parseTime(row["block_time"])
		if e != nil {
			continue
		}
		log := LogRecord{ChainID: 56, BlockNumber: block, BlockTime: blockTime, TxHash: text(row["tx_hash"]), LogIndex: uint32(index), Contract: pool.PoolAddress, Source: "CERTIFIED_PARSED_EVENT", SourceJobID: text(row["ingest_job_id"])}
		swap := normalizedSwap(log, pool, a0, a1, nil, nil, 0)
		if pool.Version == "V3" {
			swap.SqrtPriceX96, _ = new(big.Int).SetString(text(fields["sqrtPriceX96"]), 10)
			swap.Liquidity, _ = new(big.Int).SetString(text(fields["liquidity"]), 10)
			tick, _ := strconv.ParseInt(text(fields["tick"]), 10, 32)
			swap.Tick = int32(tick)
		}
		out = append(out, *swap)
	}
	return out, nil
}

func (r *DEXRepository) PutSwaps(ctx context.Context, swaps []NormalizedSwap) error {
	if len(swaps) == 0 {
		return nil
	}
	var body bytes.Buffer
	w := csv.NewWriter(&body)
	now := time.Now().UTC()
	for _, s := range swaps {
		ratio, err := s.Token1PerToken0()
		if err != nil {
			continue
		}
		tokenIn, amountIn, tokenOut, amountOut, err := s.CanonicalFlow()
		if err != nil {
			continue
		}
		inverse := new(big.Rat).Inv(ratio)
		protocolID := s.ProtocolID
		if protocolID == "" {
			protocolID = strings.ToLower(s.DEX + "_" + s.Version)
		}
		row := []string{strconv.FormatUint(uint64(s.ChainID), 10), strconv.FormatUint(s.BlockNumber, 10), formatTime(s.BlockTime), s.TxHash, strconv.FormatUint(uint64(s.LogIndex), 10), s.DEX, s.Version, protocolID, s.PoolAddress, s.Token0, s.Token1, s.Amount0Raw.String(), s.Amount1Raw.String(), decimalRat(s.Amount0, 30), decimalRat(s.Amount1, 30), decimalRat(ratio, 30), decimalRat(inverse, 30), tokenIn, decimalRat(amountIn, 30), tokenOut, decimalRat(amountOut, 30), decimalRat(ratio, 30), `\N`, `\N`, `\N`, `\N`, s.Liquidity.String(), s.SqrtPriceX96.String(), strconv.FormatInt(int64(s.Tick), 10), s.Source, s.SourceJobID, datasetDEXSwap, formatTime(now)}
		if err = w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	columns := []string{"chain_id", "block_number", "block_time", "tx_hash", "log_index", "dex", "version", "protocol_id", "pool_address", "token0_address", "token1_address", "amount0_raw", "amount1_raw", "amount0", "amount1", "token0_per_token1", "token1_per_token0", "token_in_address", "amount_in", "token_out_address", "amount_out", "price_token0_token1", "token0_usd", "token1_usd", "usd_value", "volume_usd", "liquidity", "sqrt_price_x96", "tick", "source", "source_job_id", "dataset", "inserted_at"}
	return r.client.InsertCSV(ctx, "onchain.dex_swaps", columns, &body)
}

type RebuildResult struct {
	Token string `json:"token"`
	Pools uint64 `json:"pools"`
	Swaps uint64 `json:"swaps"`
	Bars  uint64 `json:"bars"`
}
type RebuildService struct {
	dex          *DEXRepository
	bars         *PriceBarRepository
	resolver     PriceResolver
	maxDeviation *big.Rat
}

func NewRebuildService(dex *DEXRepository, bars *PriceBarRepository, resolver PriceResolver, maxDeviation string) *RebuildService {
	value, ok := new(big.Rat).SetString(maxDeviation)
	if !ok {
		value = big.NewRat(25, 1)
	}
	return &RebuildService{dex: dex, bars: bars, resolver: resolver, maxDeviation: value}
}
func (s *RebuildService) Rebuild(ctx context.Context, token string, from, to time.Time) (RebuildResult, error) {
	tokenID, err := CanonicalTokenID(56, token)
	if err != nil {
		return RebuildResult{}, err
	}
	pools, err := s.dex.PoolsForToken(ctx, tokenID)
	if err != nil {
		return RebuildResult{}, err
	}
	swaps, err := s.dex.Swaps(ctx, pools, from, to)
	if err != nil {
		return RebuildResult{}, err
	}
	if err = s.dex.PutSwaps(ctx, swaps); err != nil {
		return RebuildResult{}, err
	}
	priced := make([]PricedSwap, 0, len(swaps))
	for index := range swaps {
		swap := &swaps[index]
		ratio, err := swap.Token1PerToken0()
		if err != nil {
			continue
		}
		quote := swap.Token1
		tokenPriceQuote := ratio
		volume := absRat(swap.Amount0)
		route := "TOKEN/QUOTE"
		if tokenID == swap.Token1 {
			quote = swap.Token0
			tokenPriceQuote = new(big.Rat).Inv(ratio)
			volume = absRat(swap.Amount1)
		}
		quoteForResolver := quote
		if quote == bscWBNB {
			quoteForResolver = "native:56"
		}
		quotePrice, err := s.resolver.ResolvePrice(ctx, 56, quoteForResolver, swap.BlockTime)
		if err != nil {
			continue
		}
		usd, ok := new(big.Rat).SetString(quotePrice.PriceUSD)
		if !ok {
			continue
		}
		price := new(big.Rat).Mul(tokenPriceQuote, usd)
		route = tokenID + "/" + quote + " -> USD"
		priced = append(priced, PricedSwap{Swap: swap, TokenAddress: tokenID, TokenPriceUSD: price, TokenVolume: volume, Route: route})
	}
	bars, err := AggregateMinute(priced, s.maxDeviation)
	if err != nil {
		return RebuildResult{}, err
	}
	if err = s.bars.Put(ctx, bars); err != nil {
		return RebuildResult{}, err
	}
	return RebuildResult{Token: tokenID, Pools: uint64(len(pools)), Swaps: uint64(len(swaps)), Bars: uint64(len(bars))}, nil
}
