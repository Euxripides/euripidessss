package explorer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const testAddress = "0x1111111111111111111111111111111111111111"
const testHash = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type queryStub struct {
	queries []string
	rows    [][]map[string]any
	err     error
}

func (s *queryStub) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
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

func activityRow(block uint64, event string) map[string]any {
	return map[string]any{
		"chain_id": "56", "address": testAddress, "counterparty_address": "0x2222222222222222222222222222222222222222",
		"direction": "OUT", "activity_type": "TOKEN_TRANSFER", "block_number": block,
		"block_time": "2026-08-08T12:00:00.123Z", "tx_hash": testHash, "event_index": event,
		"token_address": "0x3333333333333333333333333333333333333333", "token_name": "Example USD", "token_symbol": "USDT", "amount": "1.25",
		"token_logo_uri": "https://raw.githubusercontent.com/trustwallet/assets/logo.png", "token_logo_source": "TRUST_WALLET", "token_verified": true, "token_spam": false,
		"usd_value": "2.50", "price_usd": "2", "price_time": "2026-08-08T12:00:00Z", "price_source": "DEX_V2", "price_confidence": "HIGH",
		"method_id": "0xa9059cbb", "method_name": "transfer", "status": "SUCCESS",
		"counterparty_entity_type": "", "counterparty_label": "", "source_provider": "sqd",
	}
}

func TestListActivityRejectsInjectionInputs(t *testing.T) {
	stub := &queryStub{}
	repo := NewRepository(stub)
	unsafeCursor, err := encodeCursor(activityCursor{
		Version: 1, ChainID: 56, Address: testAddress, Activity: ActivityAll,
		BlockTime: time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC), BlockNumber: 100,
		TxHash: testHash, EventIndex: "1' OR 1=1 --",
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []ActivityQuery{
		{ChainID: 56, Address: testAddress + "' OR 1=1 --", Activity: ActivityAll},
		{ChainID: 999, Address: testAddress, Activity: ActivityAll},
		{ChainID: 56, Address: testAddress, Activity: ActivityKind("transactions' UNION ALL SELECT")},
		{ChainID: 56, Address: testAddress, Activity: ActivityAll, Cursor: "not-base64!"},
		{ChainID: 56, Address: testAddress, Activity: ActivityAll, Cursor: unsafeCursor},
	}
	for _, input := range tests {
		if _, err := repo.ListActivity(context.Background(), input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid input for %+v, got %v", input, err)
		}
	}
	if len(stub.queries) != 0 {
		t.Fatalf("invalid input reached database: %d queries", len(stub.queries))
	}
}

func TestGetAddressSummaryUsesFinalAndKeepsDecimalsAsStrings(t *testing.T) {
	stub := &queryStub{rows: [][]map[string]any{{{
		"chain_id": "56", "address": testAddress, "address_type": "EOA",
		"first_seen_time": "2026-08-01T00:00:00Z", "last_seen_time": "2026-08-08T00:00:00Z",
		"tx_count": "9", "native_received": "12345678901234567890.123456789",
		"updated_at": "2026-08-08T01:00:00Z",
	}}}}
	repo := NewRepository(stub)
	summary, err := repo.GetAddressSummary(context.Background(), 56, testAddress)
	if err != nil {
		t.Fatal(err)
	}
	if summary.NativeReceived != "12345678901234567890.123456789" || summary.TransactionCount != 9 {
		t.Fatalf("precision or count lost: %+v", summary)
	}
	query := stub.queries[0]
	if !strings.Contains(query, "FROM onchain.address_summary FINAL") || !strings.Contains(query, "toString(native_received)") {
		t.Fatalf("unexpected summary query: %s", query)
	}
}

func TestListActivityUsesStableKeysetCursorWithoutOffset(t *testing.T) {
	stub := &queryStub{rows: [][]map[string]any{
		{activityRow(100, "3"), activityRow(100, "2"), activityRow(100, "1")},
		{activityRow(99, "9")},
	}}
	repo := NewRepository(stub)
	first, err := repo.ListActivity(context.Background(), ActivityQuery{ChainID: 56, Address: testAddress, Activity: ActivityTokenTransfers, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || first.NextCursor == "" || len(first.Items) != 2 {
		t.Fatalf("unexpected first page: %+v", first)
	}
	if first.Items[0].TokenName != "Example USD" || first.Items[0].PriceUSD == nil || *first.Items[0].PriceUSD != "2" || first.Items[0].PriceConfidence != 0.95 || first.Items[0].PriceTime == nil || first.Items[0].ValuationStatus != "VALUED" {
		t.Fatalf("token metadata or historical price evidence lost: %+v", first.Items[0])
	}
	second, err := repo.ListActivity(context.Background(), ActivityQuery{ChainID: 56, Address: testAddress, Activity: ActivityTokenTransfers, PageSize: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 {
		t.Fatalf("unexpected second page: %+v", second)
	}
	for _, prior := range first.Items {
		if prior.BlockTime.Equal(second.Items[0].BlockTime) && prior.BlockNumber == second.Items[0].BlockNumber && prior.TransactionHash == second.Items[0].TransactionHash && prior.EventIndex == second.Items[0].EventIndex {
			t.Fatalf("cursor contract returned duplicate item: %+v", second.Items[0])
		}
	}
	if len(stub.queries) != 2 {
		t.Fatalf("queries=%d", len(stub.queries))
	}
	for _, query := range stub.queries {
		if strings.Contains(strings.ToUpper(query), "OFFSET") {
			t.Fatalf("OFFSET is forbidden: %s", query)
		}
		if !strings.Contains(query, "ORDER BY a.block_time DESC, a.block_number DESC, a.tx_hash DESC, a.event_index DESC") {
			t.Fatalf("unstable order: %s", query)
		}
	}
	if !strings.Contains(stub.queries[1], "(a.block_time, a.block_number, a.tx_hash, a.event_index) <") || !strings.Contains(stub.queries[1], "'2'") {
		t.Fatalf("second query does not resume strictly after last item: %s", stub.queries[1])
	}
}

func TestListActivityQueryShapeAndBoundedPage(t *testing.T) {
	stub := &queryStub{rows: [][]map[string]any{{}}}
	repo := NewRepository(stub)
	_, err := repo.ListActivity(context.Background(), ActivityQuery{ChainID: 56, Address: strings.ToUpper(testAddress), Activity: ActivityInternal, PageSize: 200})
	if err != nil {
		t.Fatal(err)
	}
	query := stub.queries[0]
	for _, want := range []string{"FROM onchain.address_activity AS a FINAL", "a.address = '" + testAddress + "'", "INTERNAL_TRANSFER", "LIMIT 201"} {
		if !strings.Contains(query, want) {
			t.Fatalf("query missing %q: %s", want, query)
		}
	}
	if _, err := repo.ListActivity(context.Background(), ActivityQuery{ChainID: 56, Address: testAddress, Activity: ActivityAll, PageSize: 201}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected bounded page error, got %v", err)
	}
}

func TestListActivityIncludesCanonicalTokenAndHistoricalPriceEvidence(t *testing.T) {
	stub := &queryStub{rows: [][]map[string]any{{}}}
	repo := NewRepository(stub)
	if _, err := repo.ListActivity(context.Background(), ActivityQuery{ChainID: 56, Address: testAddress, Activity: ActivityTokenTransfers, PageSize: 50}); err != nil {
		t.Fatal(err)
	}
	query := stub.queries[0]
	for _, want := range []string{
		"a.chain_id = t.chain_id AND a.token_address = t.contract_address",
		"ASOF LEFT JOIN",
		"if(a.token_address = '', concat('native:', toString(a.chain_id)), a.token_address) = p.token_address",
		"a.block_time >= p.timestamp_bucket",
		"AS token_logo_uri",
		"AS price_usd",
		"AS price_source",
		"AS price_confidence",
		"AS historical_price_usdt",
		"AS historical_value_usdt",
		"AS valuation_status",
		"onchain.token_price_1m FINAL",
		"toFloat64(amount_decimal)",
		"isFinite",
		">1e15",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("canonical evidence query missing %q: %s", want, query)
		}
	}
	if strings.Contains(query, "Decimal(38,18)") {
		t.Fatalf("activity valuation must not narrow arbitrary amounts to Decimal(38,18): %s", query)
	}
	if strings.Contains(strings.ToLower(query), "t.symbol = a.token_symbol") {
		t.Fatalf("token metadata must never join by symbol: %s", query)
	}
}

func TestRepositoryRedactsDatabaseErrors(t *testing.T) {
	secret := "clickhouse password=super-secret query syntax at position 42"
	repo := NewRepository(&queryStub{err: errors.New(secret)})
	_, err := repo.ListActivity(context.Background(), ActivityQuery{ChainID: 56, Address: testAddress, Activity: ActivityAll})
	if !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected query error, got %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "password") {
		t.Fatalf("database error leaked: %v", err)
	}
}

func TestCursorCannotBeReplayedAcrossScope(t *testing.T) {
	stub := &queryStub{rows: [][]map[string]any{{activityRow(100, "1"), activityRow(99, "1")}}}
	repo := NewRepository(stub)
	page, err := repo.ListActivity(context.Background(), ActivityQuery{ChainID: 56, Address: testAddress, Activity: ActivityAll, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.ListActivity(context.Background(), ActivityQuery{ChainID: 56, Address: testAddress, Activity: ActivityTransactions, PageSize: 1, Cursor: page.NextCursor})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected cursor scope rejection, got %v", err)
	}
	if len(stub.queries) != 1 {
		t.Fatalf("scope-mismatched cursor reached database")
	}
}

func TestAsStringSupportsClickHouseUnsignedWidths(t *testing.T) {
	for name, input := range map[string]any{
		"uint":   uint(56),
		"uint8":  uint8(56),
		"uint16": uint16(56),
		"uint32": uint32(56),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := asString(input)
			if err != nil {
				t.Fatal(err)
			}
			if got != "56" {
				t.Fatalf("asString(%T) = %q, want 56", input, got)
			}
		})
	}
}
