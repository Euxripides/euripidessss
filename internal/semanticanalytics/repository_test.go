package semanticanalytics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const testAddress = "0x1111111111111111111111111111111111111111"

var testFrom = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
var testTo = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

type queryStub struct {
	queries []string
	rows    [][]map[string]any
	err     error
}

func (s *queryStub) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
	s.queries = append(s.queries, query)
	if s.err != nil {
		return nil, s.err
	}
	if len(s.rows) == 0 {
		return nil, nil
	}
	rows := s.rows[0]
	s.rows = s.rows[1:]
	return rows, nil
}

func summaryRow() map[string]any {
	return map[string]any{
		"tx_count": "2", "in_count": "1", "out_count": "1", "token_transfer_count": "1",
		"internal_transfer_count": "0", "unique_counterparties": "2", "first_seen": "2026-01-01 00:00:00.000",
		"last_seen": "2026-01-02 00:00:00.000", "active_days": "2", "total_in_usd": "12.25",
		"total_out_usd": "3.5", "netflow_usd": "8.75", "largest_in_usd": "12.25", "largest_out_usd": "3.5",
		"cex_in_usd": "0", "cex_out_usd": "3.5", "dex_volume_usd": "0", "bridge_volume_usd": "0",
		"contract_created_count": "0", "usd_valued_activity_count": "2", "activity_count": "2",
	}
}

func counterpartyRow() map[string]any {
	return map[string]any{"address": "0x2222222222222222222222222222222222222222", "entity": "cex", "label": "public-label",
		"activity_count": "2", "transaction_count": "2", "incoming_usd": "10", "outgoing_usd": "3", "netflow_usd": "7",
		"amount_usd": "13", "share": "0.5", "first_seen": "2026-01-01 00:00:00.000", "last_seen": "2026-01-02 00:00:00.000"}
}

func concentrationRow() map[string]any {
	return map[string]any{"in_top1": "0.5", "in_top5": "1", "in_top10": "1", "in_total": "10", "out_top1": "0.6", "out_top5": "1", "out_top10": "1", "out_total": "5"}
}

func retentionRow() map[string]any {
	return map[string]any{"received_usd": "10", "retained_1h": "8", "retained_6h": "7", "retained_24h": "6", "retained_7d": "5", "retained_30d": "4"}
}

func TestAddressSummaryV2SQLShapeAndAmountsRemainStrings(t *testing.T) {
	stub := &queryStub{rows: [][]map[string]any{{summaryRow()}}}
	got, err := NewRepository(stub).AddressSummaryV2(context.Background(), AddressQuery{ChainID: 56, Address: strings.ToUpper(testAddress), From: testFrom, To: testTo})
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalInUSD != "12.25" || got.NetflowUSD != "8.75" || got.PriceBasis != storedHistoricalUSD {
		t.Fatalf("unexpected summary: %+v", got)
	}
	assertSafeShape(t, stub.queries)
	if !strings.Contains(stub.queries[0], "usd_value") || strings.Contains(strings.ToLower(stub.queries[0]), "price") {
		t.Fatalf("summary must use stored usd_value only: %s", stub.queries[0])
	}
}

func TestCounterpartiesV2ProvidesFourBoundedRankings(t *testing.T) {
	stub := &queryStub{rows: [][]map[string]any{{counterpartyRow()}, {counterpartyRow()}, {counterpartyRow()}, {counterpartyRow()}}}
	got, err := NewRepository(stub).CounterpartiesV2(context.Background(), CounterpartyQuery{AddressQuery: AddressQuery{ChainID: 1, Address: testAddress, From: testFrom, To: testTo}, Limit: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.TopSources) != 1 || got.TopSources[0].Entity != "cex" || got.TopSources[0].Share != "0.5" {
		t.Fatalf("unexpected counterparties: %+v", got)
	}
	if len(stub.queries) != 4 {
		t.Fatalf("expected four rankings, got %d", len(stub.queries))
	}
	assertSafeShape(t, stub.queries)
	for _, query := range stub.queries {
		if !strings.Contains(query, "LIMIT 7") || !strings.Contains(query, "address_labels FINAL") || !strings.Contains(query, "a.counterparty_label") {
			t.Fatalf("missing bounded label-aware shape: %s", query)
		}
	}
}

func TestDerivedMetricQueriesDeclareTheirBehavioralBasis(t *testing.T) {
	passRows := []map[string]any{
		{"window": "5m", "matched_out_usd": "2", "received_usd": "10", "pass_through_ratio": "0.2", "in_count": "1", "out_count": "1"},
		{"window": "30m", "matched_out_usd": "4", "received_usd": "10", "pass_through_ratio": "0.4", "in_count": "1", "out_count": "1"},
		{"window": "1h", "matched_out_usd": "5", "received_usd": "10", "pass_through_ratio": "0.5", "in_count": "1", "out_count": "1"},
		{"window": "6h", "matched_out_usd": "6", "received_usd": "10", "pass_through_ratio": "0.6", "in_count": "1", "out_count": "1"},
		{"window": "24h", "matched_out_usd": "7", "received_usd": "10", "pass_through_ratio": "0.7", "in_count": "1", "out_count": "1"},
	}
	stub := &queryStub{rows: [][]map[string]any{{concentrationRow()}, {retentionRow()}, passRows}}
	repo := NewRepository(stub)
	base := AddressQuery{ChainID: 56, Address: testAddress, From: testFrom, To: testTo}
	concentration, err := repo.Concentration(context.Background(), base)
	if err != nil || concentration.Inflow.Top1 != "0.5" {
		t.Fatalf("concentration=%+v err=%v", concentration, err)
	}
	snapshot := SnapshotQuery{ChainID: 56, Address: testAddress, From: testFrom, AsOf: testTo}
	retention, err := repo.Retention(context.Background(), snapshot)
	if err != nil || !strings.Contains(retention.Method, "lower_bound") {
		t.Fatalf("retention=%+v err=%v", retention, err)
	}
	passThrough, err := repo.FastPassThrough(context.Background(), snapshot)
	if err != nil || len(passThrough.Windows) != 5 || !strings.Contains(passThrough.Interpretation, "not evidence of crime") {
		t.Fatalf("pass-through=%+v err=%v", passThrough, err)
	}
	assertSafeShape(t, stub.queries)
	if !strings.Contains(stub.queries[2], "ASOF LEFT JOIN") || !strings.Contains(stub.queries[2], "token_address") {
		t.Fatalf("pass-through must match prior inflow by asset: %s", stub.queries[2])
	}
}

func TestHistoricalSnapshotPinsEveryQueryToAsOf(t *testing.T) {
	stub := &queryStub{rows: [][]map[string]any{{summaryRow()}, {concentrationRow()}, {retentionRow()}}}
	got, err := NewRepository(stub).HistoricalSnapshot(context.Background(), SnapshotQuery{ChainID: 56, Address: testAddress, From: testFrom, AsOf: testTo})
	if err != nil {
		t.Fatal(err)
	}
	if got.AsOf != testTo.Format(time.RFC3339Nano) || !strings.Contains(got.SnapshotBasis, "no live RPC") {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
	assertSafeShape(t, stub.queries)
	for _, query := range stub.queries {
		if !strings.Contains(query, "2026-02-01") {
			t.Fatalf("query is not pinned to as_of: %s", query)
		}
	}
}

func TestStrictValidationRejectsUnsafeInputsBeforeQuery(t *testing.T) {
	cases := []AddressQuery{
		{ChainID: 0, Address: testAddress, From: testFrom, To: testTo},
		{ChainID: 56, Address: "0x1' OR 1=1 --", From: testFrom, To: testTo},
		{ChainID: 56, Address: testAddress, From: time.Time{}, To: testTo},
		{ChainID: 56, Address: testAddress, From: testTo, To: testFrom},
		{ChainID: 56, Address: testAddress, From: testFrom, To: time.Now().Add(time.Hour)},
	}
	stub := &queryStub{}
	for _, tc := range cases {
		if _, err := NewRepository(stub).AddressSummaryV2(context.Background(), tc); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid input for %+v, got %v", tc, err)
		}
	}
	if len(stub.queries) != 0 {
		t.Fatalf("invalid inputs reached ClickHouse: %d queries", len(stub.queries))
	}
	_, err := NewRepository(stub).CounterpartiesV2(context.Background(), CounterpartyQuery{AddressQuery: AddressQuery{ChainID: 56, Address: testAddress, From: testFrom, To: testTo}, Limit: maxLimit + 1})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid limit, got %v", err)
	}
}

func TestQueryErrorsAreStableAndDoNotLeakBackendDetails(t *testing.T) {
	stub := &queryStub{err: errors.New("password=secret internal SQL")}
	_, err := NewRepository(stub).AddressSummaryV2(context.Background(), AddressQuery{ChainID: 56, Address: testAddress, From: testFrom, To: testTo})
	if !errors.Is(err, ErrQueryFailed) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func assertSafeShape(t *testing.T, queries []string) {
	t.Helper()
	for _, query := range queries {
		if !strings.Contains(query, "onchain.address_activity FINAL") {
			t.Fatalf("missing FINAL: %s", query)
		}
		if strings.Contains(strings.ToUpper(query), " OFFSET ") {
			t.Fatalf("OFFSET is forbidden: %s", query)
		}
	}
}
