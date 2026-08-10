package canonicalapi

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const (
	testTx   = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testFrom = "0x1111111111111111111111111111111111111111"
	testTo   = "0x2222222222222222222222222222222222222222"
)

type queryStub struct {
	queries []string
	fn      func(string) ([]map[string]any, error)
}

func (s *queryStub) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
	s.queries = append(s.queries, query)
	return s.fn(query)
}

func baseTransaction(statusSource string) map[string]any {
	return map[string]any{
		"chain_id": 56, "block_number": 100, "block_hash": testTx, "block_time": "2026-08-09 01:02:03.000",
		"transaction_index": 1, "tx_hash": testTx, "from_address": testFrom, "to_address": testTo,
		"nonce": 2, "value_raw": "1000000000000000000", "value_decimal": "1", "input": "0xa9059cbb",
		"method_id": "0xa9059cbb", "tx_type": "2", "gas_limit": 21000, "gas_used": 20000,
		"gas_price": "1", "effective_gas_price": "1", "fee_native": "0.01", "fee_usd": "",
		"status": "SUCCESS", "raw_status": "0x1", "status_source": statusSource, "is_contract_creation": false,
		"created_contract_address": "", "error_message": "", "source_provider": "sqd", "ingest_job_id": "job",
		"source_range_id": "range", "parser_version": "v2", "normalizer_version": "v2", "schema_version": 2,
		"ingested_at": "2026-08-09 01:03:03.000",
	}
}

func TestGetTransactionReceiptStatusAndSQLShape(t *testing.T) {
	stub := &queryStub{fn: func(query string) ([]map[string]any, error) {
		switch {
		case strings.Contains(query, "chain_transactions"):
			return []map[string]any{baseTransaction("RECEIPT")}, nil
		case strings.Contains(query, "method_registry"):
			return []map[string]any{{"method_id": "0xa9059cbb", "canonical_signature": "transfer(address,uint256)", "display_name": "Transfer", "source": "ERC20", "confidence": "HIGH"}}, nil
		default:
			return []map[string]any{}, nil
		}
	}}
	result, err := NewRepository(stub).GetTransaction(context.Background(), 56, strings.ToUpper(testTx))
	if err != nil {
		t.Fatalf("GetTransaction error: %v", err)
	}
	if result.Status != "SUCCESS" || result.StatusSource != "RECEIPT" || result.RawStatus != "0x1" {
		t.Fatalf("unexpected receipt status: %#v", result)
	}
	if result.Method.Name != "transfer" || result.Method.Confidence != "HIGH" {
		t.Fatalf("unexpected method: %#v", result.Method)
	}
	joined := strings.Join(stub.queries, "\n")
	for _, required := range []string{"chain_transactions FINAL", "status_source", "method_registry FINAL", "token_metadata_registry", "ASOF LEFT JOIN", "token_prices FINAL", "internal_transactions FINAL", "contract_creations FINAL", "parsed_events FINAL", "address_labels"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing SQL contract %q", required)
		}
	}
	for _, forbidden := range []string{"http://", "https://", "rpc", "now()", "symbol="} {
		if strings.Contains(strings.ToLower(joined), forbidden) {
			t.Errorf("forbidden enrichment in SQL: %q", forbidden)
		}
	}
}

func TestStatusIsUnknownWithoutReceiptProvenance(t *testing.T) {
	row := baseTransaction("LEGACY")
	result, err := decodeTransaction(row)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "UNKNOWN" {
		t.Fatalf("status=%q, want UNKNOWN", result.Status)
	}
	row["status_source"] = "RECEIPT"
	row["status"] = "1"
	result, err = decodeTransaction(row)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "UNKNOWN" {
		t.Fatalf("numeric status must not be guessed: %q", result.Status)
	}
}

func TestAmountPreservesHexRawWithCanonicalDecimal(t *testing.T) {
	value, err := amount("0x10", "16")
	if err != nil || value.Raw != "0x10" || value.Decimal != "16" {
		t.Fatalf("value=%+v err=%v", value, err)
	}
}

func TestResolveMethodConflictIsAmbiguous(t *testing.T) {
	stub := &queryStub{fn: func(string) ([]map[string]any, error) {
		return []map[string]any{
			{"canonical_signature": "foo(uint256)", "display_name": "Foo", "source": "A", "confidence": "HIGH"},
			{"canonical_signature": "bar(bytes32)", "display_name": "Bar", "source": "B", "confidence": "HIGH"},
		}, nil
	}}
	method, err := NewRepository(stub).resolveMethod(context.Background(), "0x12345678")
	if err != nil {
		t.Fatal(err)
	}
	if method.Name != "ambiguous" || method.Confidence != "AMBIGUOUS" || len(method.CandidateSignatures) != 2 {
		t.Fatalf("method=%#v", method)
	}
}

func TestStrictValidationStopsSQLInjectionAndRedactsQueryErrors(t *testing.T) {
	stub := &queryStub{fn: func(string) ([]map[string]any, error) { return nil, errors.New("secret ClickHouse detail") }}
	repo := NewRepository(stub)
	if _, err := repo.GetTransaction(context.Background(), 56, testTx+"' OR 1=1"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error=%v", err)
	}
	if len(stub.queries) != 0 {
		t.Fatal("invalid input reached query client")
	}
	if _, err := repo.GetTransaction(context.Background(), 56, testTx); !errors.Is(err, ErrQueryFailed) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error was not sanitized: %v", err)
	}
	if _, err := repo.ListActivity(context.Background(), ActivityQuery{ChainID: 56, Address: testFrom, Limit: 201}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("limit error=%v", err)
	}
}

func TestTokenIdentityNeverUsesSymbolAsJoinKey(t *testing.T) {
	query := activitySQL(56, testFrom, 50)
	if !strings.Contains(query, "a.chain_id=t.chain_id AND a.token_address=t.contract_address") {
		t.Fatalf("canonical token identity missing: %s", query)
	}
	if strings.Contains(strings.ToLower(query), "symbol=") || strings.Contains(strings.ToLower(query), "symbol =") {
		t.Fatalf("symbol identity join detected: %s", query)
	}
	if !strings.Contains(query, "a.block_time>=p.timestamp_bucket") {
		t.Fatalf("historical price boundary missing: %s", query)
	}
}
