package pricing

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	ErrInvalidInput = errors.New("invalid pricing input")
	ErrPriceMissing = errors.New("PRICE_MISSING")
	addressRE       = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
)

type Resolution string

const (
	Resolution1Minute  Resolution = "1m"
	Resolution5Minute  Resolution = "5m"
	Resolution15Minute Resolution = "15m"
	Resolution1Hour    Resolution = "1h"
	Resolution4Hour    Resolution = "4h"
	Resolution1Day     Resolution = "1d"
)

func (r Resolution) Duration() (time.Duration, error) {
	switch r {
	case Resolution1Minute:
		return time.Minute, nil
	case Resolution5Minute:
		return 5 * time.Minute, nil
	case Resolution15Minute:
		return 15 * time.Minute, nil
	case Resolution1Hour:
		return time.Hour, nil
	case Resolution4Hour:
		return 4 * time.Hour, nil
	case Resolution1Day:
		return 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("%w: unsupported resolution %q", ErrInvalidInput, r)
	}
}

func (r Resolution) MaxTolerance() time.Duration {
	switch r {
	case Resolution1Minute:
		return 2 * time.Minute
	case Resolution5Minute:
		return 10 * time.Minute
	case Resolution15Minute:
		return 30 * time.Minute
	case Resolution1Hour:
		return 2 * time.Hour
	case Resolution4Hour:
		return 8 * time.Hour
	case Resolution1Day:
		return 36 * time.Hour
	default:
		return 0
	}
}

type HistoricalPrice struct {
	ChainID        uint32        `json:"chain_id"`
	TokenID        string        `json:"token_id"`
	PriceTime      time.Time     `json:"price_time"`
	PriceUSD       string        `json:"price_usd"`
	Source         string        `json:"source"`
	Confidence     string        `json:"confidence"`
	Resolution     Resolution    `json:"resolution"`
	LiquidityUSD   *string       `json:"liquidity_usd,omitempty"`
	VolumeUSD      *string       `json:"volume_usd,omitempty"`
	IsFallback     bool          `json:"is_fallback"`
	IsVerified     bool          `json:"is_verified"`
	PriceVersion   string        `json:"price_version"`
	SourceVersion  string        `json:"source_version"`
	Distance       time.Duration `json:"-"`
	SourcePriority uint8         `json:"-"`
}

type PriceResolver interface {
	ResolvePrice(ctx context.Context, chainID uint64, token string, timestamp time.Time) (*HistoricalPrice, error)
}

var nativeSymbols = map[uint32][]string{
	1: {"eth"}, 56: {"bnb"}, 137: {"matic", "pol"}, 42161: {"eth"}, 10: {"eth"}, 8453: {"eth"},
}

func CanonicalTokenID(chainID uint64, token string) (string, error) {
	if chainID == 0 || chainID > uint64(^uint32(0)) {
		return "", fmt.Errorf("%w: invalid chain_id", ErrInvalidInput)
	}
	chain := uint32(chainID)
	normalized := strings.ToLower(strings.TrimSpace(token))
	if normalized == "native" || normalized == fmt.Sprintf("native:%d", chain) {
		return fmt.Sprintf("native:%d", chain), nil
	}
	for _, symbol := range nativeSymbols[chain] {
		if normalized == symbol {
			return fmt.Sprintf("native:%d", chain), nil
		}
	}
	if !addressRE.MatchString(normalized) {
		return "", fmt.Errorf("%w: token must be a canonical native id or EVM address", ErrInvalidInput)
	}
	return normalized, nil
}
