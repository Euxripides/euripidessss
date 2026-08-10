package financialanalytics

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidInput = errors.New("invalid financial analytics input")
	ErrQueryFailed  = errors.New("financial analytics query failed")
	ErrInvalidData  = errors.New("invalid financial analytics result")
	addressRE       = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
	decimalRE       = regexp.MustCompile(`^(0|[1-9][0-9]{0,19})(\.[0-9]{1,18})?$`)
)

const (
	defaultThreshold = "100000"
	defaultLimit     = 20
	maxLimit         = 100
	maxRange         = 10 * 366 * 24 * time.Hour
)

var supportedChains = map[uint32]bool{1: true, 56: true, 8453: true, 42161: true}
var confidenceRank = map[string]int{"UNKNOWN": 0, "LOW": 1, "MEDIUM": 2, "HIGH": 3}

func validateQuery(q Query) (Query, error) {
	if !supportedChains[q.ChainID] {
		return q, fmt.Errorf("%w: unsupported chain_id", ErrInvalidInput)
	}
	q.Address = strings.ToLower(strings.TrimSpace(q.Address))
	if !addressRE.MatchString(q.Address) {
		return q, fmt.Errorf("%w: invalid address", ErrInvalidInput)
	}
	if q.Window == "" {
		q.Window = Window30D
	}
	q.To = q.To.UTC()
	if q.To.IsZero() {
		q.To = time.Now().UTC()
	}
	switch q.Window {
	case Window24H:
		q.From = q.To.Add(-24 * time.Hour)
	case Window7D:
		q.From = q.To.Add(-7 * 24 * time.Hour)
	case Window30D:
		q.From = q.To.Add(-30 * 24 * time.Hour)
	case Window90D:
		q.From = q.To.Add(-90 * 24 * time.Hour)
	case Window1Y:
		q.From = q.To.AddDate(-1, 0, 0)
	case WindowAll:
		q.From = time.Unix(0, 0).UTC()
	case WindowCustom:
		q.From = q.From.UTC()
	default:
		return q, fmt.Errorf("%w: unsupported window", ErrInvalidInput)
	}
	if !q.From.Before(q.To) || (q.Window != WindowAll && q.To.Sub(q.From) > maxRange) || q.To.After(time.Now().UTC().Add(5*time.Minute)) {
		return q, fmt.Errorf("%w: invalid time range", ErrInvalidInput)
	}
	q.LargeThresholdUSD = strings.TrimSpace(q.LargeThresholdUSD)
	if q.LargeThresholdUSD == "" {
		q.LargeThresholdUSD = defaultThreshold
	}
	if !decimalRE.MatchString(q.LargeThresholdUSD) {
		return q, fmt.Errorf("%w: invalid large transfer threshold", ErrInvalidInput)
	}
	threshold, err := strconv.ParseFloat(q.LargeThresholdUSD, 64)
	if err != nil || threshold <= 0 {
		return q, fmt.Errorf("%w: invalid large transfer threshold", ErrInvalidInput)
	}
	q.EntityMinConfidence = strings.ToUpper(strings.TrimSpace(q.EntityMinConfidence))
	if q.EntityMinConfidence == "" {
		q.EntityMinConfidence = "HIGH"
	}
	if _, ok := confidenceRank[q.EntityMinConfidence]; !ok {
		return q, fmt.Errorf("%w: invalid entity confidence", ErrInvalidInput)
	}
	if q.Limit == 0 {
		q.Limit = defaultLimit
	}
	if q.Limit < 1 || q.Limit > maxLimit {
		return q, fmt.Errorf("%w: invalid limit", ErrInvalidInput)
	}
	return q, nil
}

func activityWhere(q Query) string {
	return fmt.Sprintf("a.chain_id=%d AND a.address='%s' AND a.block_time>=parseDateTime64BestEffort('%s',3,'UTC') AND a.block_time<parseDateTime64BestEffort('%s',3,'UTC')", q.ChainID, q.Address, q.From.Format(time.RFC3339Nano), q.To.Format(time.RFC3339Nano))
}

func confidencePredicate(column, minimum string) string {
	return fmt.Sprintf("multiIf(upper(%s)='HIGH',3,upper(%s)='MEDIUM',2,upper(%s)='LOW',1,0)>=%d", column, column, column, confidenceRank[minimum])
}
