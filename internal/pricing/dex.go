package pricing

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"
)

type LogRecord struct {
	ChainID     uint32
	BlockNumber uint64
	BlockTime   time.Time
	TxHash      string
	LogIndex    uint32
	Contract    string
	Topics      []string
	Data        string
	Source      string
	SourceJobID string
}

type PoolMetadata struct {
	ChainID                                               uint32
	DEX, Version, ProtocolID, FactoryAddress, PoolAddress string
	Token0, Token1                                        string
	Token0Decimals, Token1Decimals                        uint8
	FeeBPS                                                uint32
	Verified                                              bool
	LiquidityScore                                        float64
}

type NormalizedSwap struct {
	ChainID                                               uint32
	BlockNumber                                           uint64
	BlockTime                                             time.Time
	TxHash                                                string
	LogIndex                                              uint32
	DEX, Version, ProtocolID, PoolAddress, Token0, Token1 string
	Amount0Raw, Amount1Raw                                *big.Int
	Amount0, Amount1                                      *big.Rat
	SqrtPriceX96, Liquidity                               *big.Int
	Tick                                                  int32
	Source, SourceJobID                                   string
}

type SwapDecoder interface {
	Match(LogRecord) bool
	Decode(LogRecord, PoolMetadata) (*NormalizedSwap, error)
}

type DecoderRegistry struct{ decoders []SwapDecoder }

func NewDecoderRegistry(decoders ...SwapDecoder) *DecoderRegistry {
	return &DecoderRegistry{decoders: decoders}
}
func (r *DecoderRegistry) Decode(log LogRecord, pool PoolMetadata) (*NormalizedSwap, error) {
	for _, d := range r.decoders {
		if d.Match(log) {
			return d.Decode(log, pool)
		}
	}
	return nil, errors.New("unsupported DEX swap event")
}

var (
	pancakeV2SwapTopic = eventTopic("Swap(address,uint256,uint256,uint256,uint256,address)")
	pancakeV3SwapTopic = eventTopic("Swap(address,address,int256,int256,uint160,uint128,int24)")
)

type PancakeV2Decoder struct{}

func (PancakeV2Decoder) Match(log LogRecord) bool {
	return len(log.Topics) > 0 && strings.EqualFold(log.Topics[0], pancakeV2SwapTopic)
}
func (PancakeV2Decoder) Decode(log LogRecord, pool PoolMetadata) (*NormalizedSwap, error) {
	words, err := abiWords(log.Data, 4)
	if err != nil {
		return nil, err
	}
	a0In := new(big.Int).SetBytes(words[0])
	a1In := new(big.Int).SetBytes(words[1])
	a0Out := new(big.Int).SetBytes(words[2])
	a1Out := new(big.Int).SetBytes(words[3])
	a0 := new(big.Int).Sub(a0In, a0Out)
	a1 := new(big.Int).Sub(a1In, a1Out)
	if a0.Sign() == 0 || a1.Sign() == 0 || a0.Sign() == a1.Sign() {
		return nil, fmt.Errorf("invalid V2 swap deltas")
	}
	return normalizedSwap(log, pool, a0, a1, nil, nil, 0), nil
}

type PancakeV3Decoder struct{}

func (PancakeV3Decoder) Match(log LogRecord) bool {
	return len(log.Topics) > 0 && strings.EqualFold(log.Topics[0], pancakeV3SwapTopic)
}
func (PancakeV3Decoder) Decode(log LogRecord, pool PoolMetadata) (*NormalizedSwap, error) {
	words, err := abiWords(log.Data, 5)
	if err != nil {
		return nil, err
	}
	a0 := signedWord(words[0])
	a1 := signedWord(words[1])
	if a0.Sign() == 0 || a1.Sign() == 0 || a0.Sign() == a1.Sign() {
		return nil, fmt.Errorf("invalid V3 swap deltas")
	}
	sqrt := new(big.Int).SetBytes(words[2])
	liq := new(big.Int).SetBytes(words[3])
	tickBig := signedWord(words[4])
	if !tickBig.IsInt64() {
		return nil, fmt.Errorf("V3 tick overflow")
	}
	tick := tickBig.Int64()
	if tick < -(1<<23) || tick >= (1<<23) {
		return nil, fmt.Errorf("V3 tick out of range")
	}
	return normalizedSwap(log, pool, a0, a1, sqrt, liq, int32(tick)), nil
}

func normalizedSwap(log LogRecord, pool PoolMetadata, a0, a1, sqrt, liquidity *big.Int, tick int32) *NormalizedSwap {
	return &NormalizedSwap{ChainID: log.ChainID, BlockNumber: log.BlockNumber, BlockTime: log.BlockTime.UTC(), TxHash: strings.ToLower(log.TxHash), LogIndex: log.LogIndex, DEX: pool.DEX, Version: pool.Version, ProtocolID: pool.ProtocolID, PoolAddress: strings.ToLower(pool.PoolAddress), Token0: strings.ToLower(pool.Token0), Token1: strings.ToLower(pool.Token1), Amount0Raw: new(big.Int).Set(a0), Amount1Raw: new(big.Int).Set(a1), Amount0: scaleInteger(a0, pool.Token0Decimals), Amount1: scaleInteger(a1, pool.Token1Decimals), SqrtPriceX96: cloneInt(sqrt), Liquidity: cloneInt(liquidity), Tick: tick, Source: log.Source, SourceJobID: log.SourceJobID}
}

func (s *NormalizedSwap) Token1PerToken0() (*big.Rat, error) {
	if s == nil || s.Amount0 == nil || s.Amount1 == nil || s.Amount0.Sign() == 0 {
		return nil, ErrInvalidInput
	}
	r := new(big.Rat).Quo(absRat(s.Amount1), absRat(s.Amount0))
	return r, nil
}

// CanonicalFlow converts pool balance deltas into the trader-facing DEX_SWAP
// direction. Positive pool delta is token_in; negative pool delta is token_out.
func (s *NormalizedSwap) CanonicalFlow() (tokenIn string, amountIn *big.Rat, tokenOut string, amountOut *big.Rat, err error) {
	if s == nil || s.Amount0 == nil || s.Amount1 == nil || s.Amount0.Sign() == 0 || s.Amount1.Sign() == 0 || s.Amount0.Sign() == s.Amount1.Sign() {
		return "", nil, "", nil, ErrInvalidInput
	}
	if s.Amount0.Sign() > 0 {
		return s.Token0, absRat(s.Amount0), s.Token1, absRat(s.Amount1), nil
	}
	return s.Token1, absRat(s.Amount1), s.Token0, absRat(s.Amount0), nil
}
func (s *NormalizedSwap) V3SpotToken1PerToken0(decimals0, decimals1 uint8) (*big.Rat, error) {
	if s == nil || s.SqrtPriceX96 == nil || s.SqrtPriceX96.Sign() <= 0 {
		return nil, ErrInvalidInput
	}
	num := new(big.Int).Mul(s.SqrtPriceX96, s.SqrtPriceX96)
	den := new(big.Int).Lsh(big.NewInt(1), 192)
	r := new(big.Rat).SetFrac(num, den)
	delta := int(decimals0) - int(decimals1)
	if delta > 0 {
		r.Mul(r, new(big.Rat).SetInt(pow10(uint8(delta))))
	} else if delta < 0 {
		r.Quo(r, new(big.Rat).SetInt(pow10(uint8(-delta))))
	}
	return r, nil
}

func scaleInteger(value *big.Int, decimals uint8) *big.Rat {
	return new(big.Rat).SetFrac(new(big.Int).Set(value), pow10(decimals))
}
func pow10(n uint8) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }
func absRat(v *big.Rat) *big.Rat {
	r := new(big.Rat).Set(v)
	if r.Sign() < 0 {
		r.Neg(r)
	}
	return r
}
func cloneInt(v *big.Int) *big.Int {
	if v == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(v)
}
func signedWord(word []byte) *big.Int {
	value := new(big.Int).SetBytes(word)
	if len(word) == 32 && word[0]&0x80 != 0 {
		value.Sub(value, new(big.Int).Lsh(big.NewInt(1), 256))
	}
	return value
}
func abiWords(data string, count int) ([][]byte, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(data), "0x"))
	if err != nil || len(raw) != count*32 {
		return nil, fmt.Errorf("invalid ABI data length")
	}
	out := make([][]byte, count)
	for n := 0; n < count; n++ {
		out[n] = raw[n*32 : (n+1)*32]
	}
	return out, nil
}
func eventTopic(signature string) string {
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write([]byte(signature))
	return "0x" + hex.EncodeToString(h.Sum(nil))
}
