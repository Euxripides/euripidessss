package explorer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type executeQueryStub struct {
	queryStub
	execs   []string
	execErr error
}

func (s *executeQueryStub) Exec(_ context.Context, query string) error {
	s.execs = append(s.execs, query)
	return s.execErr
}

func TestRefreshAddressAnalyticsUsesScopedFinalQueries(t *testing.T) {
	stub := &executeQueryStub{}
	repo := NewRepository(stub)
	if err := repo.RefreshAddressAnalytics(context.Background(), 56, strings.ToUpper(testAddress)); err != nil {
		t.Fatal(err)
	}
	if len(stub.execs) != 3 {
		t.Fatalf("refresh statements=%d", len(stub.execs))
	}
	for _, query := range stub.execs {
		if !strings.Contains(query, "FROM onchain.address_activity FINAL") ||
			!strings.Contains(query, "chain_id = 56 AND address = '"+testAddress+"'") {
			t.Fatalf("refresh query is not safely scoped: %s", query)
		}
	}
	if err := repo.RefreshAddressAnalytics(context.Background(), 56, testAddress+"'"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
	if len(stub.execs) != 3 {
		t.Fatal("invalid address reached Exec")
	}
}

func TestAddressSummaryNeverFallsBackToFullHistoryScan(t *testing.T) {
	stub := &queryStub{rows: [][]map[string]any{{}}}
	_, err := NewRepository(stub).GetAddressSummary(context.Background(), 56, testAddress)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected materialized summary miss, got %v", err)
	}
	if len(stub.queries) != 1 || strings.Contains(stub.queries[0], "address_activity") {
		t.Fatalf("summary performed historical fallback scan: %+v", stub.queries)
	}
}

func TestRefreshAddressAnalyticsRedactsExecFailure(t *testing.T) {
	repo := NewRepository(&executeQueryStub{execErr: errors.New("password=secret")})
	err := repo.RefreshAddressAnalytics(context.Background(), 56, testAddress)
	if !errors.Is(err, ErrQueryFailed) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetCounterpartyStatsUsesMaterializedFinalAndBound(t *testing.T) {
	stub := &queryStub{rows: [][]map[string]any{{{
		"chain_id": "56", "address": testAddress,
		"counterparty_address": "0x2222222222222222222222222222222222222222",
		"direction":            "OUT", "activity_count": "3", "tx_count": "2",
		"native_amount_text": "1.234567890123456789", "usd_value_text": "9.9",
		"first_seen_time": "2026-08-01T00:00:00Z", "last_seen_time": "2026-08-08T00:00:00Z",
	}}}}
	items, err := NewRepository(stub).GetCounterpartyStats(context.Background(), 56, testAddress, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].NativeAmount != "1.234567890123456789" {
		t.Fatalf("unexpected items: %+v", items)
	}
	if query := stub.queries[0]; !strings.Contains(query, "address_counterparty_stats FINAL") || !strings.Contains(query, "LIMIT 25") || strings.Contains(strings.ToUpper(query), "OFFSET") {
		t.Fatalf("unexpected query: %s", query)
	}
	if !strings.Contains(stub.queries[0], "updated_at=(SELECT max(updated_at)") {
		t.Fatalf("query did not isolate the latest refresh generation: %s", stub.queries[0])
	}
	if _, err := NewRepository(&queryStub{}).GetCounterpartyStats(context.Background(), 56, testAddress, 367); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected limit rejection, got %v", err)
	}
}

func TestGetDailyStatsUsesFixedDateLiterals(t *testing.T) {
	stub := &queryStub{rows: [][]map[string]any{{{
		"chain_id": "56", "address": testAddress, "activity_date": "2026-08-08",
		"in_count": "2", "out_count": "1", "in_native_amount": "3", "out_native_amount": "1",
		"native_netflow": "2", "in_usd_value": "4", "out_usd_value": "1", "usd_netflow": "3",
		"unique_counterparty_count": "2",
	}}}}
	items, err := NewRepository(stub).GetDailyStats(context.Background(), DailyStatsQuery{
		ChainID: 56, Address: testAddress,
		From: time.Date(2026, 8, 1, 18, 0, 0, 0, time.FixedZone("x", 8*60*60)),
		To:   time.Date(2026, 8, 8, 18, 0, 0, 0, time.FixedZone("x", 8*60*60)), Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Date.Format("2006-01-02") != "2026-08-08" {
		t.Fatalf("unexpected items: %+v", items)
	}
	query := stub.queries[0]
	if !strings.Contains(query, "address_daily_stats FINAL") || !strings.Contains(query, "toDate('2026-08-01')") || !strings.Contains(query, "toDate('2026-08-08')") {
		t.Fatalf("unexpected date query: %s", query)
	}
	if !strings.Contains(query, "updated_at=(SELECT max(updated_at)") {
		t.Fatalf("query did not isolate the latest refresh generation: %s", query)
	}
}

func TestDetailMethodsValidateIdentifiersAndUseFinal(t *testing.T) {
	txStub := &queryStub{rows: [][]map[string]any{{{
		"chain_id": "56", "block_number": "123", "block_hash": "0xbb", "block_time": "2026-08-08T00:00:00Z",
		"transaction_index": "1", "tx_hash": testHash, "from_address": testAddress, "to_address": testAddress,
		"nonce": "2", "value_raw": "10", "value_decimal": "0.1", "native_symbol": "BNB", "input": "0x",
		"method_id": "", "method_name": "", "tx_type": "2", "gas_limit": "21000", "gas_used": "21000",
		"transaction_fee_native": "0.0001", "transaction_fee_usd": nil, "status": "SUCCESS",
		"is_contract_creation": false, "created_contract_address": "", "error_message": "", "source_provider": "sqd",
	}}}}
	tx, err := NewRepository(txStub).GetTransactionDetail(context.Background(), 56, strings.ToUpper(testHash))
	if err != nil || tx.TransactionHash != testHash {
		t.Fatalf("unexpected tx: %+v err=%v", tx, err)
	}
	if !strings.Contains(txStub.queries[0], "chain_transactions FINAL") {
		t.Fatalf("missing FINAL: %s", txStub.queries[0])
	}
	if _, err := NewRepository(&queryStub{}).GetTransactionDetail(context.Background(), 56, testHash+"' OR 1=1"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected hash rejection, got %v", err)
	}

	contractStub := &queryStub{rows: [][]map[string]any{{{
		"chain_id": "56", "contract_address": testAddress, "creator_address": testAddress,
		"creation_tx_hash": testHash, "creation_block": "123", "creation_time": "2026-08-08T00:00:00Z",
		"bytecode_hash": "", "runtime_bytecode_hash": "", "contract_name": "Test", "is_verified": true,
		"is_proxy": false, "proxy_type": "", "implementation_address": "", "abi_json": "[]",
		"token_standard": "ERC20", "first_seen": "2026-08-08T00:00:00Z", "last_seen": "2026-08-08T00:00:00Z",
		"risk_flags": []any{"UNVERIFIED_OWNER"},
	}}}}
	contract, err := NewRepository(contractStub).GetContractDetail(context.Background(), 56, testAddress)
	if err != nil || len(contract.RiskFlags) != 1 {
		t.Fatalf("unexpected contract: %+v err=%v", contract, err)
	}
	if !strings.Contains(contractStub.queries[0], "contracts FINAL") {
		t.Fatalf("missing FINAL: %s", contractStub.queries[0])
	}
}

func TestMetadataAndContractDetailFallbackToCanonicalWriterTables(t *testing.T) {
	tokenAddress := "0x3333333333333333333333333333333333333333"
	tokenStub := &queryStub{rows: [][]map[string]any{
		{},
		{{
			"chain_id": "56", "contract_address": tokenAddress, "name": "USD Tether", "symbol": "USDT",
			"decimals": "18", "token_standard": "ERC20", "logo_uri": "", "logo_source": "",
			"official_website": "", "is_verified": false, "is_spam": false, "first_seen_block": "100",
			"first_seen_time": "2026-08-01T00:00:00Z", "last_metadata_refresh_at": "2026-08-08T00:00:00Z",
		}},
	}}
	metadata, err := NewRepository(tokenStub).GetTokenMetadata(context.Background(), 56, tokenAddress)
	if err != nil || metadata.Symbol != "USDT" {
		t.Fatalf("unexpected metadata: %+v err=%v", metadata, err)
	}
	if len(tokenStub.queries) != 2 || !strings.Contains(tokenStub.queries[1], "token_transfers FINAL") {
		t.Fatalf("missing token fallback: %+v", tokenStub.queries)
	}

	contractStub := &queryStub{rows: [][]map[string]any{
		{},
		{{
			"chain_id": "56", "contract_address": testAddress, "creator_address": testAddress,
			"creation_tx_hash": testHash, "creation_block": "123", "creation_time": "2026-08-08T00:00:00Z",
			"bytecode_hash": "", "runtime_bytecode_hash": "", "contract_name": "", "is_verified": false,
			"is_proxy": false, "proxy_type": "", "implementation_address": "", "abi_json": "",
			"token_standard": "", "first_seen": "2026-08-08T00:00:00Z", "last_seen": "2026-08-08T00:00:00Z",
			"risk_flags": []any{},
		}},
	}}
	_, err = NewRepository(contractStub).GetContractDetail(context.Background(), 56, testAddress)
	if err != nil {
		t.Fatal(err)
	}
	if len(contractStub.queries) != 2 || !strings.Contains(contractStub.queries[1], "contract_creations FINAL") {
		t.Fatalf("missing contract fallback: %+v", contractStub.queries)
	}
}
