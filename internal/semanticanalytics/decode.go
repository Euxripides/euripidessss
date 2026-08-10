package semanticanalytics

import (
	"encoding/json"
	"errors"
	"strconv"
)

func text(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case json.Number:
		return x.String(), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case nil:
		return "", nil
	default:
		return "", errors.New("not text")
	}
}

func count(v any) (uint64, error) {
	s, err := text(v)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(s, 10, 64)
}

func requiredText(row map[string]any, key string) (string, error) {
	v, ok := row[key]
	if !ok {
		return "", ErrInvalidData
	}
	s, err := text(v)
	if err != nil {
		return "", ErrInvalidData
	}
	return s, nil
}

func requiredCount(row map[string]any, key string) (uint64, error) {
	v, ok := row[key]
	if !ok {
		return 0, ErrInvalidData
	}
	n, err := count(v)
	if err != nil {
		return 0, ErrInvalidData
	}
	return n, nil
}
