package pricing

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ResolverOptions struct {
	MaxSearchRadius time.Duration
	Stablecoins     map[uint32]map[string]struct{}
	SourcePriority  map[string]uint8
	Now             func() time.Time
}

type Resolver struct {
	repository PriceRepository
	options    ResolverOptions
}

func NewResolver(repository PriceRepository, options ResolverOptions) *Resolver {
	if options.MaxSearchRadius <= 0 {
		options.MaxSearchRadius = 36 * time.Hour
	}
	if options.Stablecoins == nil {
		options.Stablecoins = DefaultStablecoins()
	}
	if options.SourcePriority == nil {
		options.SourcePriority = DefaultSourcePriority()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Resolver{repository: repository, options: options}
}

func DefaultSourcePriority() map[string]uint8 {
	return map[string]uint8{
		"LOCAL_VERIFIED":           0,
		"COINGECKO_HISTORICAL":     10,
		"COINMARKETCAP_HISTORICAL": 10,
		"CENTRALIZED_MARKET":       10,
		"DEX_TWAP":                 20,
		"AGGREGATOR":               30,
		"PEG_FALLBACK":             40,
	}
}

func DefaultStablecoins() map[uint32]map[string]struct{} {
	return map[uint32]map[string]struct{}{
		1: {
			"0xdac17f958d2ee523a2206206994597c13d831ec7": {},
			"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48": {},
		},
		56: {
			"0x55d398326f99059ff775485246999027b3197955": {},
			"0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d": {},
		},
	}
}

func (r *Resolver) ResolvePrice(ctx context.Context, chainID uint64, token string, timestamp time.Time) (*HistoricalPrice, error) {
	if r == nil || r.repository == nil || timestamp.IsZero() {
		return nil, fmt.Errorf("%w: resolver unavailable or timestamp missing", ErrInvalidInput)
	}
	tokenID, err := CanonicalTokenID(chainID, token)
	if err != nil {
		return nil, err
	}
	if timestamp.After(r.options.Now().UTC().Add(5 * time.Minute)) {
		return nil, fmt.Errorf("%w: future timestamp", ErrInvalidInput)
	}
	prices, err := r.repository.Candidates(ctx, uint32(chainID), tokenID, timestamp.UTC(), r.options.MaxSearchRadius)
	if err != nil {
		return nil, err
	}
	valid := make([]HistoricalPrice, 0, len(prices))
	for _, candidate := range prices {
		tolerance := candidate.Resolution.MaxTolerance()
		if tolerance == 0 {
			continue
		}
		candidate.Distance = absDuration(candidate.PriceTime.Sub(timestamp.UTC()))
		if candidate.Distance > tolerance {
			continue
		}
		candidate.SourcePriority = r.sourceRank(candidate)
		valid = append(valid, candidate)
	}
	if len(valid) > 0 {
		sort.SliceStable(valid, func(i, j int) bool {
			if valid[i].SourcePriority != valid[j].SourcePriority {
				return valid[i].SourcePriority < valid[j].SourcePriority
			}
			if valid[i].Distance != valid[j].Distance {
				return valid[i].Distance < valid[j].Distance
			}
			if confidenceRank(valid[i].Confidence) != confidenceRank(valid[j].Confidence) {
				return confidenceRank(valid[i].Confidence) > confidenceRank(valid[j].Confidence)
			}
			return valid[i].Source < valid[j].Source
		})
		selected := valid[0]
		return &selected, nil
	}
	if r.isStablecoin(uint32(chainID), tokenID) {
		return &HistoricalPrice{ChainID: uint32(chainID), TokenID: tokenID, PriceTime: timestamp.UTC(), PriceUSD: "1", Source: "PEG_FALLBACK", Confidence: "FALLBACK", Resolution: Resolution1Minute, IsFallback: true, IsVerified: false, PriceVersion: "peg-v1", SourceVersion: "static-v1", SourcePriority: 40}, nil
	}
	return nil, ErrPriceMissing
}

func (r *Resolver) sourceRank(price HistoricalPrice) uint8 {
	if price.SourcePriority < 100 {
		return price.SourcePriority
	}
	if price.IsVerified && !price.IsFallback {
		return 0
	}
	if priority, ok := r.options.SourcePriority[strings.ToUpper(price.Source)]; ok {
		return priority
	}
	return 100
}

func (r *Resolver) isStablecoin(chain uint32, token string) bool {
	_, ok := r.options.Stablecoins[chain][token]
	return ok
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
func confidenceRank(value string) int {
	switch strings.ToUpper(value) {
	case "HIGH":
		return 4
	case "MEDIUM":
		return 3
	case "LOW":
		return 2
	case "FALLBACK":
		return 1
	default:
		return 0
	}
}
