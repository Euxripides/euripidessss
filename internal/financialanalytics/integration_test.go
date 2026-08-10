package financialanalytics

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
)

type diagnosticClient struct {
	client  *clickhouse.Client
	lastErr error
}

func (c *diagnosticClient) QueryJSON(ctx context.Context, query string) ([]map[string]any, error) {
	rows, err := c.client.QueryJSON(ctx, query)
	c.lastErr = err
	return rows, err
}

// This test validates every P0/P1 query against the deployed ClickHouse. It is
// read-only and uses an address that is expected to have no facts.
func TestFinancialAnalyticsClickHouseSyntaxIntegration(t *testing.T) {
	if os.Getenv("CLICKHOUSE_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_INTEGRATION=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := clickhouse.New(config.Load().ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	diagnostic := &diagnosticClient{client: client}
	repo := NewRepository(diagnostic)
	q := Query{ChainID: 56, Address: testAddress, Window: WindowCustom, From: time.Now().UTC().Add(-31 * 24 * time.Hour), To: time.Now().UTC().Add(-time.Minute), Limit: 5, EntityMinConfidence: "HIGH"}
	if _, err = repo.FinancialSummary(ctx, q); err != nil {
		t.Fatalf("summary SQL: %v backend=%v", err, diagnostic.lastErr)
	}
	if _, err = repo.Counterparties(ctx, q); err != nil {
		t.Fatalf("counterparty SQL: %v", err)
	}
	if _, err = repo.EntityStats(ctx, q); err != nil {
		t.Fatalf("entity SQL: %v", err)
	}
	if _, err = repo.CEXStats(ctx, q); err != nil {
		t.Fatalf("CEX SQL: %v", err)
	}
	if _, err = repo.DEXStats(ctx, q); err != nil {
		t.Fatalf("DEX SQL: %v", err)
	}
	if _, err = repo.BridgeStats(ctx, q); err != nil {
		t.Fatalf("bridge SQL: %v backend=%v", err, diagnostic.lastErr)
	}
}
