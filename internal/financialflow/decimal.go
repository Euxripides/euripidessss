package financialflow

import (
	"errors"
	"math/big"
	"strings"
)

var errInvalidAmount = errors.New("financial flow amount must be a positive decimal")

func decimal(value string) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errInvalidAmount
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, errInvalidAmount
	}
	return r, nil
}

func positiveDecimal(value string) (*big.Rat, error) {
	r, err := decimal(value)
	if err != nil || r.Sign() <= 0 {
		return nil, errInvalidAmount
	}
	return r, nil
}

func zero() *big.Rat { return new(big.Rat) }

func clone(v *big.Rat) *big.Rat {
	if v == nil {
		return zero()
	}
	return new(big.Rat).Set(v)
}

func minRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return clone(a)
	}
	return clone(b)
}

func ratio(numerator, denominator *big.Rat) string {
	if denominator == nil || denominator.Sign() == 0 {
		return "0"
	}
	return formatRat(new(big.Rat).Quo(numerator, denominator))
}

func formatRat(v *big.Rat) string {
	if v == nil || v.Sign() == 0 {
		return "0"
	}
	s := v.FloatString(18)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "-0" || s == "" {
		return "0"
	}
	return s
}
