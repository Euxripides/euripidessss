package clickhouseanalytics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	testAddress = "0x1111111111111111111111111111111111111111"
	testB       = "0x2222222222222222222222222222222222222222"
	testC       = "0x3333333333333333333333333333333333333333"
)

type stubClient struct {
	queries []string
	rows    [][]map[string]any
	err     error
}

func (s *stubClient) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
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

func TestInvalidInputNeverReachesDatabase(t *testing.T) {
	stub := &stubClient{}
	repo := NewRepository(stub)
	if _, err := repo.AddressAnalytics(context.Background(), AddressQuery{ChainID: 56, Address: testAddress + "' OR 1=1"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("address: %v", err)
	}
	if _, err := repo.Dashboard(context.Background(), 999); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("chain: %v", err)
	}
	if _, err := repo.Graph(context.Background(), GraphQuery{ChainID: 56, Limit: 501}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("limit: %v", err)
	}
	if _, err := repo.TwoHopPaths(context.Background(), PathQuery{ChainID: 56, Address: testAddress, Limit: -1}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("path limit: %v", err)
	}
	if len(stub.queries) != 0 {
		t.Fatalf("invalid input reached ClickHouse: %d queries", len(stub.queries))
	}
}

func TestAddressAnalyticsUsesFinalAndBoundedDateRange(t *testing.T) {
	stub := &stubClient{rows: [][]map[string]any{
		{{"first_activity_time": "2026-01-01", "last_activity_time": "2026-01-02", "event_count": "1", "transaction_count": "1", "contract_count": "0", "token_count": "1", "incoming_count": "1", "outgoing_count": "0", "total_in": "1.25", "total_out": "0", "netflow": "1.25", "active_days": "1", "unique_counterparties": "1"}},
		{{"address": testB, "direction": "IN", "activity_count": "1", "transaction_count": "1", "amount": "1.25", "usd_value": "1.25", "first_seen_time": "2026-01-01", "last_seen_time": "2026-01-01"}},
		{{"date": "2026-01-01", "incoming_count": "1", "outgoing_count": "0", "incoming_amount": "1.25", "outgoing_amount": "0", "netflow": "1.25", "incoming_usd": "1.25", "outgoing_usd": "0", "netflow_usd": "1.25", "unique_counterparties": "1"}},
		{{"token_address": testC, "token_symbol": "USDT", "activity_count": "1", "incoming": "1.25", "outgoing": "0", "netflow": "1.25", "usd_value": "1.25"}},
	}}
	repo := NewRepository(stub)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	result, err := repo.AddressAnalytics(context.Background(), AddressQuery{ChainID: 56, Address: strings.ToUpper(testAddress), From: from, To: to, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.AllTime.Netflow != "1.25" || len(result.DailyNetflow) != 1 || len(result.TokenDistribution) != 1 {
		t.Fatalf("bad result: %+v", result)
	}
	for _, q := range stub.queries {
		if !strings.Contains(q, "address_activity FINAL") {
			t.Fatalf("missing FINAL: %s", q)
		}
		if strings.Contains(strings.ToUpper(q), "OFFSET") {
			t.Fatalf("OFFSET forbidden: %s", q)
		}
	}
	if !strings.Contains(stub.queries[1], "LIMIT 10") || !strings.Contains(stub.queries[1], testAddress) {
		t.Fatalf("scope missing: %s", stub.queries[1])
	}
}

func TestDateRangeIsBounded(t *testing.T) {
	repo := NewRepository(&stubClient{})
	to := time.Now().UTC()
	from := to.AddDate(-2, 0, 0)
	_, err := repo.AddressAnalytics(context.Background(), AddressQuery{ChainID: 56, Address: testAddress, From: from, To: to})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected bounded date error: %v", err)
	}
}

func TestRiskIsExplicitlyRuleBased(t *testing.T) {
	stub := &stubClient{rows: [][]map[string]any{
		{{"event_count": "1000", "active_days": "5", "unique_counterparties": "120"}},
		{{"counterparty_events": "900"}},
	}}
	result, err := NewRepository(stub).Risk(context.Background(), 56, testAddress)
	if err != nil {
		t.Fatal(err)
	}
	if result.RiskScore != 100 || result.RiskLevel != "high" || result.Method != "deterministic_clickhouse_screening_v1" {
		t.Fatalf("unexpected risk: %+v", result)
	}
	if len(result.Rules) != 3 || !strings.Contains(result.RiskReason, "Rule-based screening") {
		t.Fatalf("rules not explicit: %+v", result)
	}
	if result.CounterpartyConcentration != .9 {
		t.Fatalf("concentration=%v", result.CounterpartyConcentration)
	}
	for _, q := range stub.queries {
		if !strings.Contains(q, "FINAL") {
			t.Fatalf("risk query missing FINAL: %s", q)
		}
	}
}

func TestTwoHopAndGraphQueriesAreBounded(t *testing.T) {
	stub := &stubClient{rows: [][]map[string]any{
		{{"a": testAddress, "b": testB, "c": testC, "token": "native", "amount": "4", "tx_count": "2"}},
		{{"source": testAddress, "target": testB, "kind": "NATIVE_TRANSFER", "token": "", "amount": "4", "tx_count": "2"}},
	}}
	repo := NewRepository(stub)
	paths, err := repo.TwoHopPaths(context.Background(), PathQuery{ChainID: 56, Address: testAddress, Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := repo.Graph(context.Background(), GraphQuery{ChainID: 56, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0].TxCount != 2 || len(graph.Nodes) != 2 || len(graph.Edges) != 1 {
		t.Fatalf("paths=%+v graph=%+v", paths, graph)
	}
	if !strings.Contains(stub.queries[0], "LIMIT 25") || !strings.Contains(stub.queries[1], "LIMIT 100") {
		t.Fatalf("unbounded queries: %v", stub.queries)
	}
	for _, q := range stub.queries {
		if strings.Contains(strings.ToUpper(q), "OFFSET") {
			t.Fatalf("OFFSET forbidden")
		}
	}
}

func TestDashboardAndErrors(t *testing.T) {
	stub := &stubClient{rows: [][]map[string]any{
		{{"address_count": "3", "token_count": "2", "transaction_count": "4", "transfer_count": "5", "event_count": "6"}},
		{{"risk_addresses": "1"}},
		{{"date": "2026-08-08", "events": "6"}},
	}}
	d, err := NewRepository(stub).Dashboard(context.Background(), 56)
	if err != nil {
		t.Fatal(err)
	}
	if d.AddressCount != 3 || d.RiskAddresses != 1 || len(d.Trend) != 1 {
		t.Fatalf("dashboard=%+v", d)
	}
	for _, q := range stub.queries {
		if !strings.Contains(q, "FINAL") {
			t.Fatalf("dashboard query missing FINAL: %s", q)
		}
	}
	secret := "password=super-secret at query 42"
	_, err = NewRepository(&stubClient{err: errors.New(secret)}).Dashboard(context.Background(), 56)
	if !errors.Is(err, ErrQueryFailed) || strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked: %v", err)
	}
}

func TestTopSourcesDestinationsAndVolume(t *testing.T) {
	stub := &stubClient{rows: [][]map[string]any{
		{{"address": testAddress, "counterparty_count": "2", "transaction_count": "3", "amount": "8", "usd_value": "9"}},
		{{"address": testB, "counterparty_count": "4", "transaction_count": "5", "amount": "10", "usd_value": "11"}},
		{{"incoming_count": "6", "outgoing_count": "7", "incoming_amount": "12", "outgoing_amount": "13", "incoming_usd": "14", "outgoing_usd": "15"}},
	}}
	repo := NewRepository(stub)
	sources, err := repo.TopSources(context.Background(), 56, 10)
	if err != nil {
		t.Fatal(err)
	}
	destinations, err := repo.TopDestinations(context.Background(), 56, 10)
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	volume, err := repo.InOutVolume(context.Background(), AddressQuery{ChainID: 56, Address: testAddress, From: from, To: from.Add(time.Hour), Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || len(destinations) != 1 || volume.IncomingCount != 6 || volume.OutgoingAmount != "13" {
		t.Fatalf("sources=%+v dest=%+v volume=%+v", sources, destinations, volume)
	}
	if !strings.Contains(stub.queries[0], "direction='OUT'") || !strings.Contains(stub.queries[1], "direction='IN'") {
		t.Fatalf("wrong directions: %v", stub.queries)
	}
	for _, q := range stub.queries {
		if !strings.Contains(q, "FINAL") || strings.Contains(strings.ToUpper(q), "OFFSET") {
			t.Fatalf("unsafe query: %s", q)
		}
	}
}
