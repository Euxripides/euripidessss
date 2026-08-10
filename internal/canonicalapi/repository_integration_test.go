package canonicalapi

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
)

// TestCanonicalSQLExplain is opt-in because it requires the production-shaped
// V2 schema. It executes EXPLAIN only and never reads or mutates fact data.
func TestCanonicalSQLExplain(t *testing.T) {
	if os.Getenv("CANONICAL_CLICKHOUSE_INTEGRATION") != "1" {
		t.Skip("set CANONICAL_CLICKHOUSE_INTEGRATION=1 for real ClickHouse SQL validation")
	}
	cfg := config.Load()
	client, err := clickhouse.New(cfg.ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	capture := &queryStub{fn: func(query string) ([]map[string]any, error) {
		switch {
		case strings.Contains(query, "chain_transactions FINAL") && strings.Contains(query, "LIMIT 1"):
			return []map[string]any{baseTransaction("RECEIPT")}, nil
		case strings.Contains(query, "method_registry"):
			return []map[string]any{{"method_id": "0xa9059cbb", "canonical_signature": "transfer(address,uint256)", "display_name": "Transfer", "source": "ERC20", "confidence": "HIGH"}}, nil
		default:
			return []map[string]any{}, nil
		}
	}}
	if _, err := NewRepository(capture).GetTransaction(ctx, 56, testTx); err != nil {
		t.Fatalf("capture transaction queries: %v", err)
	}
	activityCapture := &queryStub{fn: func(string) ([]map[string]any, error) { return []map[string]any{}, nil }}
	if _, err := NewRepository(activityCapture).ListActivity(ctx, ActivityQuery{ChainID: 56, Address: testFrom, Limit: 10}); err != nil {
		t.Fatalf("capture activity query: %v", err)
	}
	capture.queries = append(capture.queries, activityCapture.queries...)

	for index, query := range capture.queries {
		if err := client.Exec(ctx, "EXPLAIN SYNTAX "+query); err != nil {
			t.Fatalf("query %d failed ClickHouse EXPLAIN: %v\n%s", index+1, err, query)
		}
		if _, err := client.QueryJSON(ctx, query); err != nil {
			t.Fatalf("query %d failed real ClickHouse execution: %v\n%s", index+1, err, query)
		}
	}
}
