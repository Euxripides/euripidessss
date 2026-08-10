package canonicalregistry

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type Client interface {
	InsertCSV(ctx context.Context, table string, columns []string, rows io.Reader) error
	QueryJSON(ctx context.Context, query string) ([]map[string]any, error)
}

type Repository struct{ client Client }

func New(client Client) *Repository { return &Repository{client: client} }

func (r *Repository) insert(ctx context.Context, table string, columns, values []string) error {
	if r == nil || r.client == nil || len(columns) != len(values) {
		return ErrQueryFailed
	}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write(values); err != nil {
		return fmt.Errorf("%w: encode CSV", ErrQueryFailed)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("%w: encode CSV", ErrQueryFailed)
	}
	if err := r.client.InsertCSV(ctx, table, columns, &buffer); err != nil {
		return fmt.Errorf("%w: insert failed", ErrQueryFailed)
	}
	return nil
}

func (r *Repository) query(ctx context.Context, query string) ([]map[string]any, error) {
	if r == nil || r.client == nil {
		return nil, ErrQueryFailed
	}
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%w: query failed", ErrQueryFailed)
	}
	return rows, nil
}

func stringValue(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case json.Number:
		return typed.String(), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(typed), nil
	case nil:
		return "", nil
	default:
		return "", errors.New("unexpected ClickHouse value")
	}
}

func uintValue(value any, bits int) (uint64, error) {
	text, err := stringValue(value)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(text, 10, bits)
}

func boolValue(value any) (bool, error) {
	if typed, ok := value.(bool); ok {
		return typed, nil
	}
	text, err := stringValue(value)
	if err != nil {
		return false, err
	}
	if text == "1" {
		return true, nil
	}
	if text == "0" {
		return false, nil
	}
	return strconv.ParseBool(text)
}

func timeValue(value any) (time.Time, error) {
	if typed, ok := value.(time.Time); ok {
		return typed.UTC(), nil
	}
	text, err := stringValue(value)
	if err != nil || text == "" {
		return time.Time{}, errors.New("unexpected ClickHouse time")
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, parseErr := time.Parse(layout, text); parseErr == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("unexpected ClickHouse time")
}

func boolCSV(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func nullableCSV(value *string) string {
	if value == nil {
		return `\N`
	}
	return *value
}

func nullableTimeCSV(value *time.Time) string {
	if value == nil {
		return `\N`
	}
	return formatTime(*value)
}

func canonicalMethodName(signature string) string {
	if index := strings.IndexByte(signature, '('); index > 0 {
		return signature[:index]
	}
	return signature
}
