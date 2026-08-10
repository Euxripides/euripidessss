package clickhousegraph

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const (
	rootAddress = "0x1111111111111111111111111111111111111111"
	addressA    = "0x2222222222222222222222222222222222222222"
	addressB    = "0x3333333333333333333333333333333333333333"
	token       = "0x4444444444444444444444444444444444444444"
	txHash      = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type queryStub struct {
	queries []string
	results [][]map[string]any
	err     error
}

func (s *queryStub) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
	s.queries = append(s.queries, query)
	if s.err != nil {
		return nil, s.err
	}
	index := len(s.queries) - 1
	if index >= len(s.results) {
		return nil, nil
	}
	return s.results[index], nil
}

func edgeRow(from, to string) map[string]any {
	return map[string]any{
		"from_address": from, "to_address": to, "token_address": token,
		"activity_type": "ERC20_TRANSFER", "amount": "12.50000000000000000000000000000000000000",
		"event_count": "2", "transaction_count": float64(1),
		"first_time": "2026-08-01 01:02:03.000", "last_time": "2026-08-02T01:02:03Z",
		"sample_tx_hash": txHash,
	}
}

func TestListCounterpartyEdgesUsesBoundedFinalQuery(t *testing.T) {
	stub := &queryStub{results: [][]map[string]any{{edgeRow(rootAddress, addressA)}}}
	repo := NewRepository(stub)
	edges, err := repo.ListCounterpartyEdges(context.Background(), CounterpartyQuery{
		ChainID: 56, Address: strings.ToUpper(rootAddress), Limit: 12,
		Direction: DirectionOut, TokenAddress: strings.ToUpper(token),
		ActivityTypes: []string{"erc20_transfer"},
	})
	if err != nil || len(edges) != 1 {
		t.Fatalf("edges=%+v err=%v", edges, err)
	}
	query := stub.queries[0]
	for _, expected := range []string{
		"FROM onchain.address_activity FINAL", "SELECT DISTINCT", "chain_id = 56",
		"address IN ('" + rootAddress + "')", "direction = 'OUT'",
		"token_address = '" + token + "'", "activity_type IN ('ERC20_TRANSFER')", "LIMIT 13",
	} {
		if !strings.Contains(query, expected) {
			t.Errorf("query missing %q:\n%s", expected, query)
		}
	}
	if strings.Contains(strings.ToUpper(query), "OFFSET") {
		t.Fatalf("query must not use OFFSET: %s", query)
	}
	if edges[0].ID == "" || edges[0].Amount == "" || edges[0].EventCount != 2 {
		t.Fatalf("decoded edge incomplete: %+v", edges[0])
	}
}

func TestGetEgoGraphBoundedBFSAndDeterministicOrdering(t *testing.T) {
	stub := &queryStub{results: [][]map[string]any{
		{edgeRow(rootAddress, addressA)},
		{edgeRow(addressA, addressB)},
	}}
	repo := NewRepository(stub)
	graph, err := repo.GetEgoGraph(context.Background(), EgoQuery{
		ChainID: 56, RootAddress: rootAddress, Depth: 2, EdgeLimit: 10, NodeLimit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if graph.ReachedDepth != 2 || len(graph.Nodes) != 3 || len(graph.Edges) != 2 || graph.Truncated {
		t.Fatalf("unexpected graph: %+v", graph)
	}
	if graph.Nodes[0].Address != rootAddress || graph.Nodes[0].Depth != 0 ||
		graph.Nodes[1].Address != addressA || graph.Nodes[1].Depth != 1 ||
		graph.Nodes[2].Address != addressB || graph.Nodes[2].Depth != 2 {
		t.Fatalf("nodes are not depth/address ordered: %+v", graph.Nodes)
	}
	if graph.Edges[0].FromAddress != rootAddress || graph.Edges[1].FromAddress != addressA {
		t.Fatalf("edges are not deterministic: %+v", graph.Edges)
	}
	if len(stub.queries) != 3 || !strings.Contains(stub.queries[1], "counterparty_address NOT IN ('"+rootAddress+"')") || !strings.Contains(stub.queries[2], "address_labels") {
		t.Fatalf("second hop did not exclude expanded root: %+v", stub.queries)
	}
}

func TestGetEgoGraphHonorsNodeAndEdgeLimits(t *testing.T) {
	stub := &queryStub{results: [][]map[string]any{{
		edgeRow(rootAddress, addressA), edgeRow(rootAddress, addressB),
	}}}
	repo := NewRepository(stub)
	graph, err := repo.GetEgoGraph(context.Background(), EgoQuery{
		ChainID: 56, RootAddress: rootAddress, Depth: 1, EdgeLimit: 1, NodeLimit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Edges) != 1 || len(graph.Nodes) != 2 || !graph.Truncated {
		t.Fatalf("limits not enforced: %+v", graph)
	}
}

func TestInvalidInputNeverReachesClickHouse(t *testing.T) {
	stub := &queryStub{}
	repo := NewRepository(stub)
	_, err := repo.GetEgoGraph(context.Background(), EgoQuery{
		ChainID: 56, RootAddress: rootAddress + "' OR 1=1 --", Depth: 1,
	})
	if !errors.Is(err, ErrInvalidInput) || len(stub.queries) != 0 {
		t.Fatalf("unsafe input err=%v queries=%d", err, len(stub.queries))
	}
	_, err = repo.ListCounterpartyEdges(context.Background(), CounterpartyQuery{
		ChainID: 56, Address: rootAddress, ActivityTypes: []string{"X') OR 1=1 --"},
	})
	if !errors.Is(err, ErrInvalidInput) || len(stub.queries) != 0 {
		t.Fatalf("unsafe activity err=%v queries=%d", err, len(stub.queries))
	}
}

func TestQueryFailureAndMalformedRowsAreClassified(t *testing.T) {
	repo := NewRepository(&queryStub{err: errors.New("secret backend failure")})
	_, err := repo.ListCounterpartyEdges(context.Background(), CounterpartyQuery{ChainID: 56, Address: rootAddress})
	if !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("query error=%v", err)
	}

	bad := edgeRow(rootAddress, "not-an-address")
	repo = NewRepository(&queryStub{results: [][]map[string]any{{bad}}})
	_, err = repo.ListCounterpartyEdges(context.Background(), CounterpartyQuery{ChainID: 56, Address: rootAddress})
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("decode error=%v", err)
	}
}
