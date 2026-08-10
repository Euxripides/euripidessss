package financialintegration

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidInput = errors.New("invalid financial integration input")
	ErrQueryFailed  = errors.New("financial ClickHouse query failed")
	ErrInvalidData  = errors.New("invalid financial ClickHouse result")

	addressPattern = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
	decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,37})(\.[0-9]{1,18})?$`)
	metricPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

func validateCommon(chainID uint32, address string, from, to time.Time, token, minUSD string) (string, string, string, time.Time, time.Time, error) {
	if chainID == 0 {
		return "", "", "", time.Time{}, time.Time{}, fmt.Errorf("%w: chain_id is required", ErrInvalidInput)
	}
	address = strings.ToLower(strings.TrimSpace(address))
	if !addressPattern.MatchString(address) {
		return "", "", "", time.Time{}, time.Time{}, fmt.Errorf("%w: invalid address", ErrInvalidInput)
	}
	token = strings.ToLower(strings.TrimSpace(token))
	if token != "" && !addressPattern.MatchString(token) {
		return "", "", "", time.Time{}, time.Time{}, fmt.Errorf("%w: invalid token address", ErrInvalidInput)
	}
	if from.IsZero() {
		from = time.Unix(0, 0).UTC()
	} else {
		from = from.UTC()
	}
	if to.IsZero() {
		to = time.Now().UTC()
	} else {
		to = to.UTC()
	}
	if !from.Before(to) {
		return "", "", "", time.Time{}, time.Time{}, fmt.Errorf("%w: from must precede to", ErrInvalidInput)
	}
	minUSD = strings.TrimSpace(minUSD)
	if minUSD == "" {
		minUSD = "0"
	}
	if !decimalPattern.MatchString(minUSD) {
		return "", "", "", time.Time{}, time.Time{}, fmt.Errorf("%w: min_usd must be a non-negative decimal", ErrInvalidInput)
	}
	return address, token, minUSD, from, to, nil
}

func quoteTime(value time.Time) string {
	return "parseDateTime64BestEffort('" + value.UTC().Format(time.RFC3339Nano) + "',3,'UTC')"
}

func requiredString(row map[string]any, key string) (string, error) {
	value, exists := row[key]
	if !exists || value == nil {
		return "", fmt.Errorf("%w: missing %s", ErrInvalidData, key)
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return "", fmt.Errorf("%w: invalid %s", ErrInvalidData, key)
	}
	return text, nil
}

func optionalString(row map[string]any, key string) string {
	if row[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(row[key]))
}

func requiredUint(row map[string]any, key string) (uint64, error) {
	text, err := requiredString(row, key)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid %s", ErrInvalidData, key)
	}
	return value, nil
}
