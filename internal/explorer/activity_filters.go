package explorer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxActivityFilterItems = 20

func normalizeActivityFilters(input ActivityFilters) (ActivityFilters, error) {
	out := input
	out.TransactionHash = strings.ToLower(strings.TrimSpace(input.TransactionHash))
	if out.TransactionHash != "" && !txHashPattern.MatchString(out.TransactionHash) {
		return ActivityFilters{}, fmt.Errorf("%w: invalid transaction_hash", ErrInvalidInput)
	}
	out.Direction = strings.ToUpper(strings.TrimSpace(input.Direction))
	if out.Direction != "" && !inSet(out.Direction, "IN", "OUT", "SELF") {
		return ActivityFilters{}, fmt.Errorf("%w: invalid direction", ErrInvalidInput)
	}
	var err error
	if out.Counterparty, err = normalizeOptionalAddress(input.Counterparty, "counterparty"); err != nil {
		return ActivityFilters{}, err
	}
	if out.FromAddress, err = normalizeOptionalAddress(input.FromAddress, "from_address"); err != nil {
		return ActivityFilters{}, err
	}
	if out.ToAddress, err = normalizeOptionalAddress(input.ToAddress, "to_address"); err != nil {
		return ActivityFilters{}, err
	}
	if len(input.TokenAddresses) > maxActivityFilterItems {
		return ActivityFilters{}, fmt.Errorf("%w: too many token filters", ErrInvalidInput)
	}
	out.TokenAddresses = make([]string, 0, len(input.TokenAddresses))
	for _, value := range input.TokenAddresses {
		address, addressErr := normalizeOptionalAddress(value, "token")
		if addressErr != nil {
			return ActivityFilters{}, addressErr
		}
		if address != "" {
			out.TokenAddresses = appendUnique(out.TokenAddresses, address)
		}
	}
	out.TokenSymbol = strings.TrimSpace(input.TokenSymbol)
	if out.TokenSymbol != "" && !validFilterText(out.TokenSymbol, 32) {
		return ActivityFilters{}, fmt.Errorf("%w: invalid token_symbol", ErrInvalidInput)
	}
	if out.MinUSD, err = normalizeUSD(input.MinUSD, "min_usd"); err != nil {
		return ActivityFilters{}, err
	}
	if out.MaxUSD, err = normalizeUSD(input.MaxUSD, "max_usd"); err != nil {
		return ActivityFilters{}, err
	}
	if out.MinUSD != "" && out.MaxUSD != "" {
		minimum, _ := strconv.ParseFloat(out.MinUSD, 64)
		maximum, _ := strconv.ParseFloat(out.MaxUSD, 64)
		if minimum > maximum {
			return ActivityFilters{}, fmt.Errorf("%w: min_usd exceeds max_usd", ErrInvalidInput)
		}
	}
	if out.Methods, err = normalizeTextList(input.Methods, 80, "method"); err != nil {
		return ActivityFilters{}, err
	}
	if out.Statuses, err = normalizeEnumList(input.Statuses, "status", "SUCCESS", "FAILED", "UNKNOWN"); err != nil {
		return ActivityFilters{}, err
	}
	if out.EntityTypes, err = normalizeEnumList(input.EntityTypes, "entity_type", "CEX", "DEX", "BRIDGE", "PROTOCOL", "CONTRACT", "EOA", "UNKNOWN"); err != nil {
		return ActivityFilters{}, err
	}
	out.Entity = strings.TrimSpace(input.Entity)
	if out.Entity != "" && !validFilterText(out.Entity, 80) {
		return ActivityFilters{}, fmt.Errorf("%w: invalid entity", ErrInvalidInput)
	}
	if out.Protocols, err = normalizeEnumList(input.Protocols, "protocol", "DEX", "BRIDGE"); err != nil {
		return ActivityFilters{}, err
	}
	if out.ValuationStatuses, err = normalizeEnumList(input.ValuationStatuses, "valuation_status", "VALUED", "LOW_CONFIDENCE", "NO_PRICE"); err != nil {
		return ActivityFilters{}, err
	}
	if out.From, err = normalizeFilterTime(input.From, "from"); err != nil {
		return ActivityFilters{}, err
	}
	if out.To, err = normalizeFilterTime(input.To, "to"); err != nil {
		return ActivityFilters{}, err
	}
	if out.From != "" && out.To != "" {
		from, _ := time.Parse(time.RFC3339Nano, out.From)
		to, _ := time.Parse(time.RFC3339Nano, out.To)
		if from.After(to) {
			return ActivityFilters{}, fmt.Errorf("%w: from exceeds to", ErrInvalidInput)
		}
	}
	return out, nil
}

func activityFilterClauses(filter ActivityFilters) []string {
	clauses := []string{"1"}
	if filter.TransactionHash != "" {
		clauses = append(clauses, "tx_hash="+quoteActivityFilter(filter.TransactionHash))
	}
	if filter.Direction != "" {
		clauses = append(clauses, "direction="+quoteActivityFilter(filter.Direction))
	}
	if filter.Counterparty != "" {
		clauses = append(clauses, "counterparty_address="+quoteActivityFilter(filter.Counterparty))
	}
	if filter.FromAddress != "" {
		clauses = append(clauses, "if(direction='IN',counterparty_address,address)="+quoteActivityFilter(filter.FromAddress))
	}
	if filter.ToAddress != "" {
		clauses = append(clauses, "if(direction='IN',address,counterparty_address)="+quoteActivityFilter(filter.ToAddress))
	}
	if len(filter.TokenAddresses) != 0 {
		clauses = append(clauses, "token_address IN ("+quotedActivityList(filter.TokenAddresses)+")")
	}
	if filter.TokenSymbol != "" {
		clauses = append(clauses, "lowerUTF8(token_symbol)=lowerUTF8("+quoteActivityFilter(filter.TokenSymbol)+")")
	}
	valueExpr := "toFloat64(amount_decimal)*ifNull(resolved_price,0)"
	validValue := "isNotNull(resolved_price) AND isFinite(" + valueExpr + ") AND abs(" + valueExpr + ")<=1e15"
	if filter.MinUSD != "" {
		clauses = append(clauses, validValue+" AND "+valueExpr+">="+filter.MinUSD)
	}
	if filter.MaxUSD != "" {
		clauses = append(clauses, validValue+" AND "+valueExpr+"<="+filter.MaxUSD)
	}
	if len(filter.Methods) != 0 {
		matches := make([]string, 0, len(filter.Methods))
		for _, value := range filter.Methods {
			matches = append(matches, "positionCaseInsensitive(concat(method_id,' ',method_name),"+quoteActivityFilter(value)+")>0")
		}
		clauses = append(clauses, "("+strings.Join(matches, " OR ")+")")
	}
	if len(filter.Statuses) != 0 {
		clauses = append(clauses, "status IN ("+quotedActivityList(filter.Statuses)+")")
	}
	if len(filter.EntityTypes) != 0 {
		clauses = append(clauses, "upper(counterparty_entity_type) IN ("+quotedActivityList(filter.EntityTypes)+")")
	}
	if filter.Entity != "" {
		clauses = append(clauses, "positionCaseInsensitive(concat(counterparty_label,' ',counterparty_address),"+quoteActivityFilter(filter.Entity)+")>0")
	}
	if len(filter.Protocols) != 0 {
		matches := make([]string, 0, len(filter.Protocols))
		for _, value := range filter.Protocols {
			if value == "BRIDGE" {
				matches = append(matches, "positionCaseInsensitive(concat(activity_type,' ',method_name),'BRIDGE')>0")
			} else {
				matches = append(matches, "multiSearchAnyCaseInsensitive(concat(activity_type,' ',method_name),['SWAP','DEX','PAIR','LIQUIDITY'])")
			}
		}
		clauses = append(clauses, "("+strings.Join(matches, " OR ")+")")
	}
	if len(filter.ValuationStatuses) != 0 {
		valuation := "multiIf(isNull(resolved_price),'NO_PRICE',resolved_confidence<0.35,'LOW_CONFIDENCE','VALUED')"
		clauses = append(clauses, valuation+" IN ("+quotedActivityList(filter.ValuationStatuses)+")")
	}
	if filter.From != "" {
		clauses = append(clauses, "block_time>=parseDateTime64BestEffort("+quoteActivityFilter(filter.From)+",3,'UTC')")
	}
	if filter.To != "" {
		clauses = append(clauses, "block_time<=parseDateTime64BestEffort("+quoteActivityFilter(filter.To)+",3,'UTC')")
	}
	return clauses
}

func activityFilterHash(filter ActivityFilters) string {
	data, _ := json.Marshal(filter)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:8])
}

func normalizeOptionalAddress(value, field string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value != "" && !evmAddressPattern.MatchString(value) {
		return "", fmt.Errorf("%w: invalid %s", ErrInvalidInput, field)
	}
	return value, nil
}

func normalizeUSD(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) || parsed < 0 || parsed > 1e15 {
		return "", fmt.Errorf("%w: invalid %s", ErrInvalidInput, field)
	}
	return strconv.FormatFloat(parsed, 'f', -1, 64), nil
}

func normalizeTextList(values []string, maxRunes int, field string) ([]string, error) {
	if len(values) > maxActivityFilterItems {
		return nil, fmt.Errorf("%w: too many %s filters", ErrInvalidInput, field)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !validFilterText(value, maxRunes) {
			return nil, fmt.Errorf("%w: invalid %s", ErrInvalidInput, field)
		}
		out = appendUnique(out, value)
	}
	return out, nil
}

func normalizeEnumList(values []string, field string, allowed ...string) ([]string, error) {
	if len(values) > maxActivityFilterItems {
		return nil, fmt.Errorf("%w: too many %s filters", ErrInvalidInput, field)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if !inSet(value, allowed...) {
			return nil, fmt.Errorf("%w: invalid %s", ErrInvalidInput, field)
		}
		out = appendUnique(out, value)
	}
	return out, nil
}

func normalizeFilterTime(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", fmt.Errorf("%w: invalid %s", ErrInvalidInput, field)
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}

func validFilterText(value string, maxRunes int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes || strings.ContainsAny(value, "'\\;") {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func quoteActivityFilter(value string) string { return "'" + value + "'" }
func quotedActivityList(values []string) string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = quoteActivityFilter(value)
	}
	return strings.Join(out, ",")
}
func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}
func inSet(value string, allowed ...string) bool {
	for _, current := range allowed {
		if value == current {
			return true
		}
	}
	return false
}
