package clickhousegraph

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
)

type liveRecordingClient struct {
	client  QueryClient
	queries []string
}

func (c *liveRecordingClient) QueryJSON(ctx context.Context, query string) ([]map[string]any, error) {
	c.queries = append(c.queries, query)
	return c.client.QueryJSON(ctx, query)
}

func TestClickHouseGraphLive(t *testing.T) {
	if os.Getenv("CLICKHOUSE_GRAPH_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_GRAPH_INTEGRATION=1 for the local ClickHouse check")
	}
	cfg := config.Load()
	client, err := clickhouse.New(cfg.ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	seed, err := client.QueryJSON(ctx, `SELECT chain_id, address
FROM onchain.address_activity FINAL
WHERE address != ''
ORDER BY block_time DESC, address ASC
LIMIT 1`)
	if err != nil {
		t.Fatal(err)
	}
	chainID, address := uint64(56), rootAddress
	if len(seed) > 0 {
		var ok bool
		chainID, ok = uintValue(seed[0]["chain_id"])
		if !ok {
			t.Fatalf("invalid seed chain_id: %#v", seed[0]["chain_id"])
		}
		address, ok = stringValue(seed[0]["address"])
		if !ok {
			t.Fatalf("invalid seed address: %#v", seed[0]["address"])
		}
	}

	recording := &liveRecordingClient{client: client}
	graph, err := NewRepository(recording).GetEgoGraph(ctx, EgoQuery{
		ChainID: uint32(chainID), RootAddress: address, Depth: 1, EdgeLimit: 25, NodeLimit: 26,
	})
	if err != nil {
		t.Fatal(err)
	}
	if graph.RootAddress != address || len(graph.Nodes) == 0 || len(graph.Edges) > 25 || len(graph.Nodes) > 26 {
		t.Fatalf("unexpected bounded graph: %+v", graph)
	}
	if len(recording.queries) != 2 {
		t.Fatalf("expected graph and bounded label enrichment queries, got %d", len(recording.queries))
	}
	for i, query := range recording.queries {
		if _, err = client.QueryJSON(ctx, "EXPLAIN SYNTAX "+query); err != nil {
			t.Fatalf("ClickHouse rejected graph SQL syntax for query %d: %v", i+1, err)
		}
	}
}
