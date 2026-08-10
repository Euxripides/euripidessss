package canonicalregistry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	ErrInvalidInput = errors.New("invalid canonical registry input")
	ErrNotFound     = errors.New("canonical registry record not found")
	ErrQueryFailed  = errors.New("canonical registry query failed")

	addressRE  = regexp.MustCompile(`^0x[0-9a-f]{40}$`)
	selectorRE = regexp.MustCompile(`^0x[0-9a-f]{8}$`)
	hashRE     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	txHashRE   = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
	topicRE    = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
	uuidRE     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	decimalRE  = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,18})?$`)
	versionRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

var confidenceValues = map[string]bool{"UNKNOWN": true, "LOW": true, "MEDIUM": true, "HIGH": true}
var labelTypes = map[string]bool{"ENTITY": true, "BEHAVIOR": true, "SYSTEM": true, "USER": true, "CASE": true, "RISK": true}
var jobStatuses = map[string]bool{"PENDING": true, "RUNNING": true, "SUCCEEDED": true, "FAILED": true, "CANCELLED": true}

func normalizeAddress(chainID uint32, value string) (string, error) {
	if chainID == 0 {
		return "", fmt.Errorf("%w: chain_id must be positive", ErrInvalidInput)
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if !addressRE.MatchString(value) {
		return "", fmt.Errorf("%w: invalid EVM address", ErrInvalidInput)
	}
	return value, nil
}

func normalizeConfidence(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if !confidenceValues[value] {
		return "", fmt.Errorf("%w: invalid confidence", ErrInvalidInput)
	}
	return value, nil
}

func requiredText(name, value string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > max || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("%w: invalid %s", ErrInvalidInput, name)
	}
	return value, nil
}

func optionalURL(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > 2048 {
		return "", fmt.Errorf("%w: invalid %s", ErrInvalidInput, name)
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("%w: invalid %s", ErrInvalidInput, name)
	}
	return value, nil
}

func optionalLogoURI(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "/assets/tokens/") && len(value) <= 256 && !strings.Contains(value, "..") && !strings.ContainsAny(value, "\\?#\x00") {
		return value, nil
	}
	return optionalURL("logo_uri", value)
}

func requireTime(name string, value time.Time) (time.Time, error) {
	if value.IsZero() || value.Year() < 1970 || value.Year() > 9999 {
		return time.Time{}, fmt.Errorf("%w: invalid %s", ErrInvalidInput, name)
	}
	return value.UTC(), nil
}

func requireJSON(name, value string) (string, error) {
	if len(value) == 0 || len(value) > 8<<20 || !json.Valid([]byte(value)) {
		return "", fmt.Errorf("%w: invalid %s JSON", ErrInvalidInput, name)
	}
	return value, nil
}

func deterministicUUID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	bytes := sum[:16]
	bytes[6] = (bytes[6] & 0x0f) | 0x50
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(bytes)
	return hexValue[0:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:32]
}

func formatTime(value time.Time) string { return value.UTC().Format("2006-01-02 15:04:05.000") }
