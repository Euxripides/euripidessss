package financialquality

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeClient struct {
	rows  []map[string]any
	err   error
	query string
}

func (f *fakeClient) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
	f.query = query
	return f.rows, f.err
}

func TestReportPreservesUnknownAndMissing(t *testing.T) {
	client := &fakeClient{rows: []map[string]any{{
		"price_required": "10", "priced": "8", "historical_price": "6", "fallback_price": "2", "missing_price": "2",
		"position_events": "5", "known_cost_basis": "3", "dex_candidates": "4", "dex_decoded": "3", "bridge_candidates": "2", "bridge_decoded": "1",
		"counterparties": "10", "known_entity": "4", "last_updated": "2026-08-09 12:00:00.000",
	}}}
	repo := NewRepository(client)
	repo.now = func() time.Time { return time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC) }
	report, err := repo.Report(context.Background(), 56, Filter{Window: "30d"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Price.MissingPrice != 2 || report.Price.Coverage.Percentage == nil || *report.Price.Coverage.Percentage != 80 {
		t.Fatalf("price quality = %+v", report.Price)
	}
	if report.Price.FallbackRatio.Percentage == nil || *report.Price.FallbackRatio.Percentage != 25 {
		t.Fatalf("fallback ratio = %+v", report.Price.FallbackRatio)
	}
	if !report.CostBasis.Coverage.Available || report.CostBasis.Coverage.Percentage == nil || *report.CostBasis.Coverage.Percentage != 60 || report.CostBasis.UnknownCostBasis != 2 {
		t.Fatalf("cost basis coverage mismatch: %+v", report.CostBasis)
	}
	if report.DEXDecode.Missing != 1 || report.BridgeDecode.Missing != 1 || report.Entity.UnknownEntity != 6 {
		t.Fatalf("coverage mismatch: %+v", report)
	}
	for _, required := range []string{"token_transfers FINAL", "address_activity FINAL", "financial_position_events FINAL", "address_labels FINAL", "entity_registry FINAL", "price_time IS NOT NULL", "price_source)!='CURRENT'", "PEG_FALLBACK", "DEX_SWAP", "BRIDGE_DEPOSIT", "entity_id IS NOT NULL"} {
		if !strings.Contains(client.query, required) {
			t.Fatalf("query missing %q", required)
		}
	}
	if !strings.Contains(client.query, "block_time >=") || !strings.Contains(client.query, "2026-07-10") {
		t.Fatalf("window was not bounded: %s", client.query)
	}
}

func TestReportEmptyMetricsAreUnavailableNotZero(t *testing.T) {
	client := &fakeClient{rows: []map[string]any{{
		"price_required": "0", "priced": "0", "historical_price": "0", "fallback_price": "0", "missing_price": "0",
		"position_events": "0", "known_cost_basis": "0", "dex_candidates": "0", "dex_decoded": "0", "bridge_candidates": "0", "bridge_decoded": "0",
		"counterparties": "0", "known_entity": "0", "last_updated": "1970-01-01 00:00:00.000",
	}}}
	report, err := NewRepository(client).Report(context.Background(), 56, Filter{Window: "ALL"})
	if err != nil {
		t.Fatal(err)
	}
	for name, metric := range map[string]Coverage{"price": report.Price.Coverage, "fallback": report.Price.FallbackRatio, "dex": report.DEXDecode.Coverage, "bridge": report.BridgeDecode.Coverage, "entity": report.Entity.Coverage} {
		if metric.Available || metric.Percentage != nil {
			t.Fatalf("%s must be unavailable, got %+v", name, metric)
		}
	}
	if report.LastUpdated != "" || strings.Contains(client.query, "block_time >=") {
		t.Fatalf("ALL window or last updated mismatch: %+v %s", report, client.query)
	}
}

func TestReportRejectsInvalidWindowAndData(t *testing.T) {
	repo := NewRepository(&fakeClient{})
	if _, err := repo.Report(context.Background(), 99, Filter{Window: "30D"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsupported chain error = %v", err)
	}
	if _, err := repo.Report(context.Background(), 56, Filter{Window: "CUSTOM"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("custom window error = %v", err)
	}
	client := &fakeClient{rows: []map[string]any{{"price_required": "1", "priced": "2"}}}
	if _, err := NewRepository(client).Report(context.Background(), 56, Filter{Window: "ALL"}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("invalid row error = %v", err)
	}
}
