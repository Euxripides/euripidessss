package financialpnl

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

type fakeClient struct {
	queries []string
	rows    [][]map[string]any
	inserts []string
}

func (f *fakeClient) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
	f.queries = append(f.queries, query)
	if len(f.rows) == 0 {
		return nil, nil
	}
	rows := f.rows[0]
	f.rows = f.rows[1:]
	return rows, nil
}
func (f *fakeClient) InsertCSV(_ context.Context, table string, _ []string, reader io.Reader) error {
	data, _ := io.ReadAll(reader)
	f.inserts = append(f.inserts, table+"\n"+string(data))
	return nil
}

func TestRepositoryQueriesClickHouseOnlyWithStableOrdering(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	fake := &fakeClient{rows: [][]map[string]any{{{
		"event_time": "2025-01-01 00:00:00", "block_number": "10", "tx_hash": hash('a'), "event_index": "1",
		"event_type": "DEX_BUY", "amount_decimal": "5", "usd_value": "10", "gas_usd": nil,
		"semantic_source": "DEX_SWAP", "semantic_confidence": "HIGH", "price_version": "p1", "data_snapshot_version": "d1",
	}}, {{"price_usd": "3", "price_time": "2025-01-02 03:04:00", "source": "LOCAL", "confidence": "HIGH", "price_version": "p2"}}}}
	repo := NewRepository(fake)
	events, err := repo.LoadEvents(context.Background(), validQuery(now))
	if err != nil || len(events) != 1 || events[0].Type != EventDEXBuy {
		t.Fatalf("load: events=%+v err=%v", events, err)
	}
	price, err := repo.CurrentPrice(context.Background(), validQuery(now))
	if err != nil || price == nil || price.USD != "3" {
		t.Fatalf("price=%+v err=%v", price, err)
	}
	joined := strings.Join(fake.queries, "\n")
	for _, required := range []string{"financial_position_events FINAL", "ORDER BY event_time,block_number,tx_hash,event_index", "token_prices FINAL", "ORDER BY price_time DESC"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing SQL contract %q in %s", required, joined)
		}
	}
	for _, forbidden := range []string{"duckdb", "parquet", "http://", "https://"} {
		if strings.Contains(strings.ToLower(joined), forbidden) {
			t.Fatalf("non-ClickHouse source in query: %s", joined)
		}
	}
}

func TestRepositoryValidationBlocksSQLInjection(t *testing.T) {
	q := validQuery(time.Now().UTC())
	q.Address = "0x' OR 1=1 --"
	if _, err := NewRepository(&fakeClient{}).LoadEvents(context.Background(), q); err != ErrInvalidQuery {
		t.Fatalf("err=%v", err)
	}
	q = validQuery(time.Now().UTC())
	q.Token = "native:1"
	if err := ValidateQuery(q); err != ErrInvalidQuery {
		t.Fatalf("cross-chain native accepted: %v", err)
	}
}

func TestServicePersistsVersionedSnapshotAndLots(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	fake := &fakeClient{rows: [][]map[string]any{{{
		"event_time": "2025-01-01 00:00:00", "block_number": "10", "tx_hash": hash('a'), "event_index": "1", "event_type": "DEX_BUY",
		"amount_decimal": "5", "usd_value": "10", "gas_usd": nil, "semantic_source": "DEX_SWAP", "semantic_confidence": "HIGH", "price_version": "p1", "data_snapshot_version": "d1",
	}}, {{"price_usd": "3", "price_time": "2025-01-02 03:04:00", "source": "LOCAL", "confidence": "HIGH", "price_version": "p2"}}}}
	result, snapshotID, err := NewService(NewRepository(fake), time.Minute).Calculate(context.Background(), validQuery(now), true)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotID == "" || len(fake.inserts) != 2 {
		t.Fatalf("snapshot=%q inserts=%d", snapshotID, len(fake.inserts))
	}
	if result.AlgorithmVersion != AlgorithmVersion || result.PriceVersion != "historical:p1|current:p2" || result.CurrentPriceVersion != "p2" || result.DataSnapshotVersion != "d1" {
		t.Fatalf("versions missing: %+v", result)
	}
	if !strings.HasPrefix(fake.inserts[0], "onchain.financial_pnl_snapshots") || !strings.HasPrefix(fake.inserts[1], "onchain.token_position_lots") {
		t.Fatalf("wrong inserts: %v", fake.inserts)
	}
}
