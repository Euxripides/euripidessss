package contractintelligence

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	addressRE = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
	hashRE    = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
)

func validateAddress(chainID uint32, address string) (string, error) {
	if chainID == 0 {
		return "", fmt.Errorf("%w: chain_id is required", ErrInvalidInput)
	}
	address = strings.ToLower(strings.TrimSpace(address))
	if !addressRE.MatchString(address) {
		return "", fmt.Errorf("%w: invalid address", ErrInvalidInput)
	}
	return address, nil
}

func validateHash(hash string) (string, error) {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if !hashRE.MatchString(hash) {
		return "", fmt.Errorf("%w: invalid hash", ErrInvalidInput)
	}
	return hash, nil
}

func normalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return 50, nil
	}
	if limit < 1 || limit > 200 {
		return 0, fmt.Errorf("%w: limit out of range", ErrInvalidInput)
	}
	return limit, nil
}

func text(row map[string]any, key string) string {
	value := row[key]
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func uint64Field(row map[string]any, key string) (uint64, error) {
	raw := text(row, key)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, ErrInvalidData
	}
	return value, nil
}

func boolField(row map[string]any, key string) (bool, error) {
	switch strings.ToLower(text(row, key)) {
	case "1", "true":
		return true, nil
	case "", "0", "false":
		return false, nil
	default:
		return false, ErrInvalidData
	}
}

func timeField(row map[string]any, key string) (time.Time, error) {
	raw := text(row, key)
	if raw == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if value, err := time.Parse(layout, raw); err == nil {
			return value.UTC(), nil
		}
	}
	return time.Time{}, ErrInvalidData
}

func optionalAddress(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	if !addressRE.MatchString(raw) {
		return "", ErrInvalidData
	}
	return raw, nil
}

func optionalHash(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	if !hashRE.MatchString(raw) {
		return "", ErrInvalidData
	}
	return raw, nil
}
