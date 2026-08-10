package pricing

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestParseBinanceTimestampMillisecondsAndMicroseconds(t *testing.T) {
	want := time.Date(2025, 1, 1, 0, 0, 0, 123000000, time.UTC)
	for _, raw := range []string{"1735689600123", "1735689600123000"} {
		got, err := parseBinanceTimestamp(raw)
		if err != nil || !got.Equal(want) {
			t.Fatalf("parse %s = %v, %v; want %v", raw, got, err, want)
		}
	}
}

func TestPancakeV2DecoderNormalizesSignedDeltas(t *testing.T) {
	data := abiData(unsignedWord(1000), unsignedWord(0), unsignedWord(0), unsignedWord(2000))
	log := LogRecord{ChainID: 56, BlockTime: time.Now(), Topics: []string{pancakeV2SwapTopic}, Data: data}
	pool := PoolMetadata{DEX: "PANCAKESWAP", Version: "V2", PoolAddress: "0xpool", Token0: "0xt0", Token1: "0xt1", Token0Decimals: 2, Token1Decimals: 2}
	swap, err := (PancakeV2Decoder{}).Decode(log, pool)
	if err != nil {
		t.Fatal(err)
	}
	if swap.Amount0.RatString() != "10" || swap.Amount1.RatString() != "-20" {
		t.Fatalf("unexpected normalized amounts: %s, %s", swap.Amount0.RatString(), swap.Amount1.RatString())
	}
	price, err := swap.Token1PerToken0()
	if err != nil || price.RatString() != "2" {
		t.Fatalf("price=%v err=%v", price, err)
	}
	tokenIn, amountIn, tokenOut, amountOut, err := swap.CanonicalFlow()
	if err != nil || tokenIn != "0xt0" || tokenOut != "0xt1" || amountIn.RatString() != "10" || amountOut.RatString() != "20" {
		t.Fatalf("unexpected canonical flow: %s %v -> %s %v, %v", tokenIn, amountIn, tokenOut, amountOut, err)
	}
}

func TestPancakeV3DecoderHandlesSignedABIAndSpotPrice(t *testing.T) {
	sqrt := new(big.Int).Lsh(big.NewInt(1), 96)
	data := abiData(signedABIWord(big.NewInt(-1_000_000)), signedABIWord(big.NewInt(2_000_000)), unsignedWordBig(sqrt), unsignedWord(50_000), signedABIWord(big.NewInt(-10)))
	log := LogRecord{ChainID: 56, BlockTime: time.Now(), Topics: []string{pancakeV3SwapTopic}, Data: data}
	pool := PoolMetadata{DEX: "PANCAKESWAP", Version: "V3", PoolAddress: "0xpool", Token0: "0xt0", Token1: "0xt1", Token0Decimals: 6, Token1Decimals: 6}
	swap, err := (PancakeV3Decoder{}).Decode(log, pool)
	if err != nil {
		t.Fatal(err)
	}
	if swap.Tick != -10 || swap.Amount0.Sign() >= 0 || swap.Amount1.Sign() <= 0 {
		t.Fatalf("unexpected V3 decode: tick=%d amount0=%s amount1=%s", swap.Tick, swap.Amount0, swap.Amount1)
	}
	spot, err := swap.V3SpotToken1PerToken0(6, 6)
	if err != nil || spot.RatString() != "1" {
		t.Fatalf("spot=%v err=%v", spot, err)
	}
}

func TestAggregateMinuteFiltersOutlierAndPreservesTradeOrder(t *testing.T) {
	minute := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	prices := []int64{100, 102, 1000}
	items := make([]PricedSwap, 0, len(prices))
	for index, value := range prices {
		items = append(items, PricedSwap{Swap: &NormalizedSwap{ChainID: 56, BlockTime: minute.Add(time.Duration(index) * time.Second), BlockNumber: uint64(index), LogIndex: uint32(index), PoolAddress: "0xpool"}, TokenAddress: "0x55d398326f99059ff775485246999027b3197955", TokenPriceUSD: big.NewRat(value, 100), TokenVolume: big.NewRat(10, 1), Route: "TOKEN/USDT"})
	}
	bars, err := AggregateMinute(items, big.NewRat(25, 1))
	if err != nil || len(bars) != 1 {
		t.Fatalf("bars=%d err=%v", len(bars), err)
	}
	bar := bars[0]
	if bar.TradeCount != 2 || bar.Open != "1.000000000000000000" || bar.Close != "1.020000000000000000" || bar.High != "1.020000000000000000" {
		t.Fatalf("unexpected bar: %+v", bar)
	}
}

func TestPoolDiscoveryTrustsOnlyOfficialFactoryAndMatchingEvent(t *testing.T) {
	v2, ok := supportedBSCFactory("0xCA143CE32FE78F1F7019D7D551A6402FC5350C73", "PairCreated")
	if !ok || v2.ProtocolID != "pancakeswap_v2" || v2.Version != "V2" {
		t.Fatalf("official V2 factory rejected: %+v %v", v2, ok)
	}
	if _, ok = supportedBSCFactory("0xca143ce32fe78f1f7019d7d551a6402fc5350c73", "PoolCreatedV3"); ok {
		t.Fatal("mismatched event accepted for V2 factory")
	}
	if _, ok = supportedBSCFactory("0x1111111111111111111111111111111111111111", "PairCreated"); ok {
		t.Fatal("unregistered factory accepted")
	}
}

func abiData(words ...[]byte) string {
	var b strings.Builder
	b.WriteString("0x")
	for _, word := range words {
		b.WriteString(hex.EncodeToString(word))
	}
	return b.String()
}

func unsignedWord(value int64) []byte { return unsignedWordBig(big.NewInt(value)) }
func unsignedWordBig(value *big.Int) []byte {
	out := make([]byte, 32)
	value.FillBytes(out)
	return out
}
func signedABIWord(value *big.Int) []byte {
	if value.Sign() >= 0 {
		return unsignedWordBig(value)
	}
	encoded := new(big.Int).Add(value, new(big.Int).Lsh(big.NewInt(1), 256))
	return unsignedWordBig(encoded)
}
