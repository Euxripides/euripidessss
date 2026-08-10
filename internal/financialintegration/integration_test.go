package financialintegration

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

const (
	testAddress = "0x1111111111111111111111111111111111111111"
	testOther   = "0x2222222222222222222222222222222222222222"
	testToken   = "0x3333333333333333333333333333333333333333"
)

type stubClient struct {
	jsonRows []map[string]any
	csv      string
	query    string
}

func (s *stubClient) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
	s.query = query
	return s.jsonRows, nil
}

func (s *stubClient) QueryCSV(_ context.Context, query string) (io.ReadCloser, error) {
	s.query = query
	return io.NopCloser(strings.NewReader(s.csv)), nil
}

func TestHistoricalGraphAggregatesTokenBreakdownAndProvenance(t *testing.T) {
	stub := &stubClient{jsonRows: []map[string]any{
		{"from_address": testAddress, "to_address": testOther, "historical_usd": "200.00", "transaction_count": "2", "event_count": "2", "first_time": "2025-01-01 00:00:00.000", "last_time": "2025-01-01 00:01:00.000", "token_address": testToken, "token_symbol": "TKN", "token_amount": "100", "token_usd": "200", "historical_price": "2", "price_time": "2025-01-01 00:00:00.000", "price_source": "LOCAL_VERIFIED", "price_confidence": "HIGH", "entity_id": "", "entity_name": "", "entity_role": "", "entity_confidence": ""},
	}}
	repo := NewGraphRepository(stub)
	graph, err := repo.HistoricalGraph(context.Background(), GraphQuery{
		ChainID: 56, Address: testAddress, From: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		To: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), MinUSD: "100.00", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Edges) != 1 || graph.Edges[0].HistoricalUSD != "200.00" || len(graph.Edges[0].TokenBreakdown) != 1 {
		t.Fatalf("unexpected graph: %+v", graph)
	}
	breakdown := graph.Edges[0].TokenBreakdown[0]
	if breakdown.HistoricalPrice != "2" || breakdown.PriceSource != "LOCAL_VERIFIED" || breakdown.PriceConfidence != "HIGH" {
		t.Fatalf("lost price provenance: %+v", breakdown)
	}
	for _, required := range []string{"address_activity FINAL", "usd_value IS NOT NULL", "toDecimal256(historical_usd,18)>=", "LIMIT 10000"} {
		if !strings.Contains(stub.query, required) {
			t.Fatalf("query missing %q: %s", required, stub.query)
		}
	}
}

func TestHistoricalGraphRejectsUnsafeBounds(t *testing.T) {
	_, err := NewGraphRepository(&stubClient{}).HistoricalGraph(context.Background(), GraphQuery{
		ChainID: 56, Address: testAddress + "'", MinUSD: "0 OR 1=1",
	})
	if err == nil {
		t.Fatal("expected unsafe input rejection")
	}
}

func TestHistoricalExportFixedWhitelistAndStreaming(t *testing.T) {
	stub := &stubClient{csv: "56," + testAddress + "," + testOther + "\n"}
	var output strings.Builder
	_, err := NewExporter(stub).StreamHistoricalCSV(context.Background(), &output, ExportRequest{
		Dataset: ExportHistoricalActivity, ChainID: 56, Address: testAddress,
		From: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	header := strings.SplitN(output.String(), "\n", 2)[0]
	for _, required := range []string{"historical_price", "historical_usd", "price_source", "price_confidence", "entity_name", "entity_role"} {
		if !strings.Contains(header, required) {
			t.Fatalf("header missing %s: %s", required, header)
		}
	}
	if strings.Contains(strings.ToLower(header), "path") || strings.Contains(strings.ToLower(stub.query), "outfile") {
		t.Fatalf("export exposed a path: header=%s query=%s", header, stub.query)
	}
	if !strings.Contains(stub.query, "address_activity AS a FINAL") || !strings.Contains(stub.query, "entity_registry AS e FINAL") {
		t.Fatalf("expected FINAL ClickHouse query: %s", stub.query)
	}
}

func TestAlgorithmExportWhitelist(t *testing.T) {
	var output strings.Builder
	_, err := StreamAlgorithmCSV(&output, []AlgorithmRecord{{
		Metric: "retained_24h", Window: "24h", ValueUSD: "30000", Ratio: "0.3", Coverage: "1", Confidence: "HIGH",
		AlgorithmVersion: "retention_fifo_v1", PriceVersion: "prices_v1", From: time.Unix(0, 0), To: time.Unix(3600, 0), TokenFilter: testToken,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), strings.Join(algorithmColumns, ",")+"\n") || strings.Contains(output.String(), "path") {
		t.Fatalf("unexpected algorithm export: %s", output.String())
	}
}

type analyticsStub struct{ calls []string }

func (a *analyticsStub) FinancialSummary(context.Context, FinancialQuery) (any, error) {
	a.calls = append(a.calls, "summary")
	return "summary", nil
}
func (a *analyticsStub) Retention(context.Context, FinancialQuery) (any, error) {
	a.calls = append(a.calls, "retention")
	return "retention", nil
}
func (a *analyticsStub) PassThrough(context.Context, FinancialQuery) (any, error) {
	a.calls = append(a.calls, "pass-through")
	return "pass-through", nil
}
func (a *analyticsStub) PnL(context.Context, FinancialQuery) (any, error) {
	a.calls = append(a.calls, "pnl")
	return "pnl", nil
}

func TestInvestigationFacadeCallsDedicatedAnalytics(t *testing.T) {
	analytics := &analyticsStub{}
	query := FinancialQuery{ChainID: 56, Address: testAddress, From: time.Unix(0, 0), To: time.Unix(3600, 0), AsOf: time.Unix(3600, 0)}
	snapshot, err := NewInvestigationFacade(analytics).Snapshot(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PnL != "pnl" || strings.Join(analytics.calls, ",") != "summary,retention,pass-through,pnl" {
		t.Fatalf("unexpected facade calls: %+v / %v", snapshot, analytics.calls)
	}
}
