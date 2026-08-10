package semanticanalytics

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	defaultLimit = 20
	maxLimit     = 100
	maxRange     = 10 * 366 * 24 * time.Hour
)

var (
	ErrInvalidInput = errors.New("invalid semantic analytics input")
	ErrQueryFailed  = errors.New("semantic analytics query failed")
	ErrInvalidData  = errors.New("invalid semantic analytics result")
	addressPattern  = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
	supportedChains = map[uint32]struct{}{1: {}, 56: {}, 8453: {}, 42161: {}}
)

func validateAddressQuery(q AddressQuery) (AddressQuery, error) {
	address, err := validateAddress(q.ChainID, q.Address)
	if err != nil {
		return q, err
	}
	q.Address = address
	if q.From.IsZero() || q.To.IsZero() {
		return q, fmt.Errorf("%w: from and to are required", ErrInvalidInput)
	}
	q.From, q.To = q.From.UTC(), q.To.UTC()
	if !q.From.Before(q.To) || q.To.Sub(q.From) > maxRange {
		return q, fmt.Errorf("%w: time range must be positive and at most 10 years", ErrInvalidInput)
	}
	if q.To.After(time.Now().UTC().Add(5 * time.Minute)) {
		return q, fmt.Errorf("%w: to cannot be in the future", ErrInvalidInput)
	}
	return q, nil
}

func validateCounterpartyQuery(q CounterpartyQuery) (CounterpartyQuery, error) {
	base, err := validateAddressQuery(q.AddressQuery)
	if err != nil {
		return q, err
	}
	q.AddressQuery = base
	if q.Limit == 0 {
		q.Limit = defaultLimit
	}
	if q.Limit < 1 || q.Limit > maxLimit {
		return q, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maxLimit)
	}
	return q, nil
}

func validateSnapshotQuery(q SnapshotQuery) (SnapshotQuery, error) {
	address, err := validateAddress(q.ChainID, q.Address)
	if err != nil {
		return q, err
	}
	q.Address = address
	if q.From.IsZero() || q.AsOf.IsZero() {
		return q, fmt.Errorf("%w: from and as_of are required", ErrInvalidInput)
	}
	q.From, q.AsOf = q.From.UTC(), q.AsOf.UTC()
	if !q.From.Before(q.AsOf) || q.AsOf.Sub(q.From) > maxRange {
		return q, fmt.Errorf("%w: snapshot range must be positive and at most 10 years", ErrInvalidInput)
	}
	if q.AsOf.After(time.Now().UTC().Add(5 * time.Minute)) {
		return q, fmt.Errorf("%w: as_of cannot be in the future", ErrInvalidInput)
	}
	return q, nil
}

func validateAddress(chainID uint32, address string) (string, error) {
	if _, ok := supportedChains[chainID]; !ok {
		return "", fmt.Errorf("%w: unsupported chain_id", ErrInvalidInput)
	}
	address = strings.ToLower(strings.TrimSpace(address))
	if !addressPattern.MatchString(address) {
		return "", fmt.Errorf("%w: invalid EVM address", ErrInvalidInput)
	}
	return address, nil
}

func where(q AddressQuery) string {
	return fmt.Sprintf("chain_id=%d AND address='%s' AND block_time >= parseDateTime64BestEffort('%s',3,'UTC') AND block_time < parseDateTime64BestEffort('%s',3,'UTC')", q.ChainID, q.Address, q.From.Format(time.RFC3339Nano), q.To.Format(time.RFC3339Nano))
}
