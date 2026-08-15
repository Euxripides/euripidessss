package clickhouseinvestigation

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type fakeQueryClient struct {
	queries []string
	rows    [][]map[string]any
	err     error
}

func (f *fakeQueryClient) QueryCSV(_ context.Context, query string) (io.ReadCloser, error) {
	f.queries = append(f.queries, query)
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeQueryClient) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
	f.queries = append(f.queries, query)
	if f.err != nil {
		return nil, f.err
	}
	if len(f.rows) == 0 {
		return nil, nil
	}
	result := f.rows[0]
	f.rows = f.rows[1:]
	return result, nil
}

func TestRejectsAddressAndTokenInjection(t *testing.T) {
	fake := &fakeQueryClient{}
	repo, err := New(fake, 56)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Profile(context.Background(), "0x' OR 1=1 --"); err == nil {
		t.Fatal("expected invalid address error")
	}
	valid := "0x1111111111111111111111111111111111111111"
	if _, err := repo.Flows(context.Background(), valid, "0x' UNION SELECT"); err == nil {
		t.Fatal("expected invalid token error")
	}
	if len(fake.queries) != 0 {
		t.Fatalf("invalid input reached query client: %q", fake.queries)
	}
}

func TestProfileMapsClickHouseValues(t *testing.T) {
	fake := &fakeQueryClient{rows: [][]map[string]any{{{
		"address": "0x1111111111111111111111111111111111111111", "first_activity_time": "2026-01-01 00:00:00",
		"last_activity_time": "2026-01-02 00:00:00", "event_count": "8", "transaction_count": float64(6),
		"contract_count": "1", "token_count": "2", "total_in": "5", "total_out": "3", "active_days": "2",
	}}}}
	repo, _ := New(fake, 56)
	profile, err := repo.Profile(context.Background(), "0x1111111111111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if profile.TransactionCount != 6 || profile.ContractCount != 1 || profile.TotalIn != 5 || profile.TotalOut != 3 {
		t.Fatalf("unexpected profile: %+v", profile)
	}
	if len(fake.queries) != 1 || !strings.Contains(fake.queries[0], "address_activity FINAL") || !strings.Contains(fake.queries[0], "chain_id = 56") {
		t.Fatalf("unexpected query: %q", fake.queries)
	}
}

func TestFlowsAreBoundedAndMapped(t *testing.T) {
	fake := &fakeQueryClient{rows: [][]map[string]any{{
		{"direction": "IN", "token": "0x2222222222222222222222222222222222222222", "counterparty": "0x3333333333333333333333333333333333333333", "amount": "42", "block": "7", "tx_hash": "0xabc"},
		{"direction": "SELF"},
	}}}
	repo, _ := New(fake, 56)
	flows, err := repo.Flows(context.Background(), "0x1111111111111111111111111111111111111111", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 || flows[0].Direction != "incoming" || flows[0].Amount != "42" {
		t.Fatalf("unexpected flows: %+v", flows)
	}
	if !strings.Contains(fake.queries[0], "LIMIT 5000") || strings.Contains(strings.ToUpper(fake.queries[0]), " OFFSET ") {
		t.Fatalf("flow query is not safely bounded: %s", fake.queries[0])
	}
}

func TestFlowStatsUsesFullClickHouseAggregation(t *testing.T) {
	fake := &fakeQueryClient{rows: [][]map[string]any{{{
		"node_count": "3", "edge_count": "2", "tx_count": "4",
		"total_in": "10", "total_out": "8", "net": "2",
	}}}}
	repo, _ := New(fake, 56)
	stats, err := repo.FlowStats(context.Background(), "bsc", "")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Graph.NodeCount != 3 || stats.Graph.EdgeCount != 2 || stats.Flow.Net != "2" || !stats.Completeness.Complete {
		t.Fatalf("unexpected flow stats: %+v", stats)
	}
	query := fake.queries[0]
	for _, want := range []string{"FROM address_activity FINAL", "chain_id=56", "uniqExact(tx_hash)"} {
		if !strings.Contains(query, want) {
			t.Fatalf("flow stats query missing %q: %s", want, query)
		}
	}
	if strings.Contains(strings.ToUpper(query), "LIMIT") {
		t.Fatalf("flow stats aggregation must not be truncated: %s", query)
	}
}

func TestQueryErrorsAreWrapped(t *testing.T) {
	fake := &fakeQueryClient{err: errors.New("database unavailable")}
	repo, _ := New(fake, 56)
	_, err := repo.Profile(context.Background(), "0x1111111111111111111111111111111111111111")
	if err == nil || !strings.Contains(err.Error(), "address profile") {
		t.Fatalf("expected contextual error, got %v", err)
	}
}

func TestAddressEvidencePreservesLineageAndIsBounded(t *testing.T) {
	fake := &fakeQueryClient{rows: [][]map[string]any{{{
		"chain_id": "56", "address": "0x1111111111111111111111111111111111111111",
		"counterparty_address": "0x2222222222222222222222222222222222222222", "direction": "OUT",
		"activity_type": "token_transfer", "block_number": "100", "block_time": "2026-01-01 00:00:00.000",
		"tx_hash": "0xabc", "event_index": "log:7", "amount": "99", "ingest_job_id": "job-1", "source_range_id": "range-1",
	}}}}
	repo, _ := New(fake, 56)
	rows, err := repo.AddressEvidence(context.Background(), "0x1111111111111111111111111111111111111111", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].IngestJobID != "job-1" || rows[0].EventIndex != "log:7" {
		t.Fatalf("lineage was not preserved: %+v", rows)
	}
	if !strings.Contains(fake.queries[0], "LIMIT 25") || !strings.Contains(fake.queries[0], "ingest_job_id") {
		t.Fatalf("unexpected evidence query: %s", fake.queries[0])
	}
	if _, err := repo.AddressEvidence(context.Background(), "0x1111111111111111111111111111111111111111", maxFlowLimit+1); err == nil {
		t.Fatal("expected evidence bound error")
	}
}

type fakeCSVClient struct {
	query, data string
	err         error
}

func (f *fakeCSVClient) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
	f.query = query
	if f.err != nil {
		return nil, f.err
	}
	return nil, nil
}

func (f *fakeCSVClient) QueryCSV(_ context.Context, query string) (io.ReadCloser, error) {
	f.query = query
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(strings.NewReader(f.data)), nil
}

func TestExportCSVUsesWhitelistAndStableHeader(t *testing.T) {
	repo, _ := New(&fakeQueryClient{}, 56)
	client := &fakeCSVClient{data: "56,1\n"}
	var output strings.Builder
	repo.client = client
	_, err := repo.ExportCSV(context.Background(), &output, ExportRequest{Dataset: "activity", Address: "0x1111111111111111111111111111111111111111", FromBlock: 10, ToBlock: 20, Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "chain_id,address,") {
		t.Fatalf("missing stable header: %q", output.String())
	}
	for _, expected := range []string{"FROM address_activity FINAL", "block_number >= 10", "block_number <= 20", "LIMIT 25"} {
		if !strings.Contains(client.query, expected) {
			t.Fatalf("missing %q in %s", expected, client.query)
		}
	}
	if strings.Contains(strings.ToUpper(client.query), " OFFSET ") {
		t.Fatal("export must not use OFFSET")
	}
}

func TestExportRejectsUnsupportedDatasetAndOversizedLimit(t *testing.T) {
	repo, _ := New(&fakeQueryClient{}, 56)
	client := &fakeCSVClient{}
	address := "0x1111111111111111111111111111111111111111"
	repo.client = client
	if _, err := repo.ExportCSV(context.Background(), io.Discard, ExportRequest{Dataset: "system.tables", Address: address}); err == nil {
		t.Fatal("expected dataset whitelist error")
	}
	if _, err := repo.ExportCSV(context.Background(), io.Discard, ExportRequest{Dataset: "activity", Address: address, Limit: maxExportRows + 1}); err == nil {
		t.Fatal("expected bounded limit error")
	}
	if client.query != "" {
		t.Fatalf("invalid export reached ClickHouse: %s", client.query)
	}
}
