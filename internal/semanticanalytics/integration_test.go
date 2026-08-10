package semanticanalytics

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
)

type integrationClient struct {
	client  *clickhouse.Client
	lastErr error
}

func (c *integrationClient) QueryJSON(ctx context.Context, query string) ([]map[string]any, error) {
	rows, err := c.client.QueryJSON(ctx, query)
	c.lastErr = err
	return rows, err
}

// This validates syntax against the deployed ClickHouse without inserting or
// mutating data. Empty result sets are valid for the synthetic address.
func TestSemanticAnalyticsClickHouseSyntaxIntegration(t *testing.T) {
	if os.Getenv("CLICKHOUSE_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_INTEGRATION=1 to validate against deployed ClickHouse")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := clickhouse.New(config.Load().ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	diagnostic := &integrationClient{client: client}
	repo := NewRepository(diagnostic)
	to := time.Now().UTC().Add(-time.Minute)
	from := to.Add(-31 * 24 * time.Hour)
	base := AddressQuery{ChainID: 56, Address: testAddress, From: from, To: to}
	if _, err := repo.AddressSummaryV2(ctx, base); err != nil {
		t.Fatalf("summary syntax: %v", err)
	}
	if _, err := repo.CounterpartiesV2(ctx, CounterpartyQuery{AddressQuery: base, Limit: 5}); err != nil {
		t.Fatalf("counterparty syntax: %v (backend: %v)", err, diagnostic.lastErr)
	}
	if _, err := repo.Concentration(ctx, base); err != nil {
		t.Fatalf("concentration syntax: %v (backend: %v)", err, diagnostic.lastErr)
	}
	snapshot := SnapshotQuery{ChainID: 56, Address: testAddress, From: from, AsOf: to}
	if _, err := repo.Retention(ctx, snapshot); err != nil {
		t.Fatalf("retention syntax: %v (backend: %v)", err, diagnostic.lastErr)
	}
	passThrough, err := repo.FastPassThrough(ctx, snapshot)
	if err != nil {
		t.Fatalf("pass-through syntax: %v (backend: %v)", err, diagnostic.lastErr)
	}
	if len(passThrough.Windows) != 5 {
		t.Fatalf("pass-through must return all five windows for empty data, got %d", len(passThrough.Windows))
	}
}
