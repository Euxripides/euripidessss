package financialintegration

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
)

type liveRecorder struct {
	client *clickhouse.Client
	query  string
}

func (r *liveRecorder) QueryJSON(ctx context.Context, query string) ([]map[string]any, error) {
	r.query = query
	return r.client.QueryJSON(ctx, query)
}

func (r *liveRecorder) QueryCSV(ctx context.Context, query string) (io.ReadCloser, error) {
	r.query = query
	return r.client.QueryCSV(ctx, query)
}

func TestFinancialIntegrationLiveSQL(t *testing.T) {
	if os.Getenv("CLICKHOUSE_FINANCIAL_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_FINANCIAL_INTEGRATION=1 for the local ClickHouse check")
	}
	client, err := clickhouse.New(config.Load().ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	recorder := &liveRecorder{client: client}
	_, err = NewGraphRepository(recorder).HistoricalGraph(ctx, GraphQuery{
		ChainID: 56, Address: testAddress, From: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		To: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), MinUSD: "100", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.QueryJSON(ctx, "EXPLAIN SYNTAX "+recorder.query); err != nil {
		t.Fatalf("ClickHouse rejected graph SQL: %v\n%s", err, recorder.query)
	}

	var output strings.Builder
	_, err = NewExporter(recorder).StreamHistoricalCSV(ctx, &output, ExportRequest{
		Dataset: ExportHistoricalEdges, ChainID: 56, Address: testAddress,
		From: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		MinUSD: "100", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.QueryJSON(ctx, "EXPLAIN SYNTAX "+recorder.query); err != nil {
		t.Fatalf("ClickHouse rejected export SQL: %v\n%s", err, recorder.query)
	}
}
