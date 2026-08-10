package pricing

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const maxGapBuckets = 1_000_000

type GapRequest struct {
	ChainID    uint64
	Token      string
	From       time.Time
	To         time.Time
	Resolution Resolution
}

type PriceGap struct {
	ChainID        uint32     `json:"chain_id"`
	TokenID        string     `json:"token_id"`
	Resolution     Resolution `json:"resolution"`
	Start          time.Time  `json:"gap_start"`
	End            time.Time  `json:"gap_end"`
	MissingBuckets uint64     `json:"missing_buckets"`
}

type GapDetector struct{ repository PriceRepository }

func NewGapDetector(repository PriceRepository) *GapDetector {
	return &GapDetector{repository: repository}
}

func (d *GapDetector) Detect(ctx context.Context, request GapRequest) ([]PriceGap, error) {
	if d == nil || d.repository == nil || request.From.IsZero() || request.To.IsZero() || request.To.Before(request.From) {
		return nil, fmt.Errorf("%w: invalid gap range", ErrInvalidInput)
	}
	tokenID, err := CanonicalTokenID(request.ChainID, request.Token)
	if err != nil {
		return nil, err
	}
	step, err := request.Resolution.Duration()
	if err != nil {
		return nil, err
	}
	from, to := request.From.UTC().Truncate(step), request.To.UTC().Truncate(step)
	count := int64(to.Sub(from)/step) + 1
	if count <= 0 || count > maxGapBuckets {
		return nil, fmt.Errorf("%w: gap range contains %d buckets", ErrInvalidInput, count)
	}
	buckets, err := d.repository.Buckets(ctx, uint32(request.ChainID), tokenID, request.Resolution, from, to)
	if err != nil {
		return nil, err
	}
	present := make(map[int64]struct{}, len(buckets))
	for _, bucket := range buckets {
		present[bucket.UTC().Truncate(step).UnixNano()] = struct{}{}
	}
	gaps := make([]PriceGap, 0)
	var current *PriceGap
	for bucket := from; !bucket.After(to); bucket = bucket.Add(step) {
		if _, ok := present[bucket.UnixNano()]; ok {
			if current != nil {
				gaps = append(gaps, *current)
				current = nil
			}
			continue
		}
		if current == nil {
			current = &PriceGap{ChainID: uint32(request.ChainID), TokenID: tokenID, Resolution: request.Resolution, Start: bucket, End: bucket, MissingBuckets: 1}
		} else {
			current.End = bucket
			current.MissingBuckets++
		}
	}
	if current != nil {
		gaps = append(gaps, *current)
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].Start.Before(gaps[j].Start) })
	return gaps, nil
}
