package semanticquality

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type qualityStub struct {
	queries []string
	fail    error
	bad     bool
}

func (s *qualityStub) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
	s.queries = append(s.queries, query)
	if s.fail != nil {
		return nil, s.fail
	}
	if s.bad {
		return []map[string]any{{"contracts": "1", "creator_known": "2"}}, nil
	}
	switch {
	case strings.Contains(query, "semanticquality:data"):
		return []map[string]any{{
			"transaction_rows": "100", "token_transfer_rows": "80", "internal_transaction_rows": "10",
			"contract_creation_rows": "4", "contract_rows": "5", "token_rows": "3", "activity_rows": "160",
			"parsed_event_rows": "80", "token_price_rows": "20",
			"status_known": "99", "method_required": "50", "method_known": "45", "entity_required": "20", "entity_known": "2",
			"transaction_updated": "2026-08-09 00:00:00.000", "token_transfer_updated": "2026-08-09 00:01:00.000",
			"internal_transaction_updated": "2026-08-09 00:02:00.000", "contract_creation_updated": "2026-08-09 00:03:00.000",
			"contract_updated": "2026-08-09 00:04:00.000", "token_updated": "2026-08-09 00:05:00.000",
			"activity_updated": "2026-08-09 00:06:00.000", "parsed_event_updated": "2026-08-09 00:07:00.000",
			"token_price_updated": "2026-08-09 00:08:00.000",
		}}, nil
	case strings.Contains(query, "semanticquality:token"):
		return []map[string]any{{"known_tokens": "3", "verified": "2", "spam_tokens": "1", "missing_symbol": "1",
			"missing_logo": "2", "transferred_tokens": "5", "metadata_known": "3", "last_updated": "2026-08-09 00:05:00.000"}}, nil
	case strings.Contains(query, "semanticquality:contract"):
		return []map[string]any{{"contracts": "5", "creator_known": "4", "creation_tx_known": "3", "proxy_detected": "2",
			"implementation_known": "1", "abi_known": "2", "verified": "2", "token_detected": "1", "last_updated": "2026-08-09 00:04:00.000"}}, nil
	case strings.Contains(query, "semanticquality:decoder"):
		return []map[string]any{{"transactions_with_input": "50", "known_method": "45", "indexed_events": "80", "decoded_events": "76",
			"abi_decode_failures": "1", "last_updated": "2026-08-09 00:07:00.000"}}, nil
	case strings.Contains(query, "semanticquality:price"):
		return []map[string]any{{"required": "80", "priced": "70", "historical": "60", "fallback": "5", "last_updated": "2026-08-09 00:01:00.000"}}, nil
	default:
		return nil, errors.New("unexpected query")
	}
}

func TestReportReturnsAuditableCoverage(t *testing.T) {
	stub := &qualityStub{}
	report, err := NewRepository(stub).Report(context.Background(), 56)
	if err != nil {
		t.Fatal(err)
	}
	if report.Data.TotalRows != 462 || len(report.Data.Datasets) != 9 || report.Data.Status.Numerator != 99 || report.Data.Status.Unknown != 1 || report.Data.Status.Percentage != 99 {
		t.Fatalf("unexpected data quality: %+v", report.Data)
	}
	if report.Token.Metadata.Numerator != 3 || report.Token.Metadata.Denominator != 5 || report.Token.MissingDecimals != 2 {
		t.Fatalf("unexpected token quality: %+v", report.Token)
	}
	if report.Contract.Creator.Numerator != 4 || report.Contract.ABI.Percentage != 40 {
		t.Fatalf("unexpected contract quality: %+v", report.Contract)
	}
	if report.Decoder.Scope != "canonical_parsed_events" || report.Decoder.UnknownTopic0 != 4 || report.Decoder.ABIDecodeFailures != 1 {
		t.Fatalf("unexpected decoder quality: %+v", report.Decoder)
	}
	if report.Price.PriceCoverage.Numerator != 70 || report.Price.NoPrice != 10 || !report.Price.HistoricalPriceCoverage.Available || report.Price.HistoricalPriceCoverage.Numerator != 60 || report.Price.HistoricalPriceCoverage.Unknown != 20 {
		t.Fatalf("unexpected historical price quality: %+v", report.Price)
	}
	if report.SemanticCompleteness.Overall.Numerator != 295 || report.SemanticCompleteness.Overall.Denominator != 353 || report.SemanticCompleteness.Overall.Unknown != 58 {
		t.Fatalf("unexpected completeness: %+v", report.SemanticCompleteness.Overall)
	}
	if len(stub.queries) != 5 {
		t.Fatalf("queries=%d", len(stub.queries))
	}
	for _, query := range stub.queries {
		if !strings.Contains(query, "chain_id=56") || !strings.Contains(query, " FINAL") {
			t.Fatalf("query must have a fixed chain and FINAL: %s", query)
		}
	}
	combined := strings.Join(stub.queries, "\n")
	for _, required := range []string{"status_source", "method_confidence", "address_labels FINAL", "parsed_events FINAL", "price_time IS NOT NULL", "price_source"} {
		if !strings.Contains(combined, required) {
			t.Fatalf("missing semantic requirement %q", required)
		}
	}
}

func TestStrictChainValidationPreventsQuery(t *testing.T) {
	for _, chainID := range []uint32{0, 2, 55, ^uint32(0)} {
		stub := &qualityStub{}
		_, err := NewRepository(stub).Report(context.Background(), chainID)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("chain %d err=%v", chainID, err)
		}
		if len(stub.queries) != 0 {
			t.Fatalf("unsupported chain reached ClickHouse")
		}
	}
}

func TestQueryErrorsAreRedacted(t *testing.T) {
	secret := "password=do-not-leak"
	_, err := NewRepository(&qualityStub{fail: errors.New(secret)}).DataQuality(context.Background(), 56)
	if !errors.Is(err, ErrQueryFailed) || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestRejectsImpossibleCoverage(t *testing.T) {
	_, err := NewRepository(&qualityStub{bad: true}).ContractQuality(context.Background(), 56)
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("err=%v", err)
	}
}

func TestNilRepository(t *testing.T) {
	var repository *Repository
	_, err := repository.DataQuality(context.Background(), 56)
	if !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("err=%v", err)
	}
}

func TestCoverageAndTotalsRejectOverflow(t *testing.T) {
	if _, err := coverage(2, 1, "", true); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("coverage err=%v", err)
	}
	if _, err := sumU64(^uint64(0), 1); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("sum err=%v", err)
	}
}
