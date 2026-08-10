package contractintelligence

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type queryStub struct {
	queries []string
	rows    []map[string]any
	err     error
}

func (s *queryStub) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
	s.queries = append(s.queries, query)
	return s.rows, s.err
}

func validContractRow() map[string]any {
	return map[string]any{
		"chain_id": "56", "contract_address": "0x0000000000000000000000000000000000000011",
		"creator_address":  "0x0000000000000000000000000000000000000022",
		"factory_address":  "0x0000000000000000000000000000000000000033",
		"creation_tx_hash": "0x" + strings.Repeat("a", 64), "creation_block": "42",
		"creation_time": "2026-08-09 01:02:03.000", "bytecode_hash": "0x" + strings.Repeat("b", 64),
		"runtime_bytecode_hash": "0x" + strings.Repeat("c", 64), "contract_name": "Example",
		"is_verified": "1", "is_proxy": "1", "proxy_type": "UUPS",
		"implementation_address": "0x0000000000000000000000000000000000000044",
		"abi_source":             "CONTRACTS_ABI", "creation_type": "CREATE2", "token_detected": "0",
	}
}

func TestGetContractCanonicalDTOAndSQLShape(t *testing.T) {
	stub := &queryStub{rows: []map[string]any{validContractRow()}}
	got, err := NewRepository(stub).GetContract(context.Background(), 56, "0X0000000000000000000000000000000000000011")
	if err != nil {
		t.Fatal(err)
	}
	if got.CreationType != CreationProxy || got.ProxyType != ProxyUUPS || !got.Verified || got.FactoryAddress == "" {
		t.Fatalf("contract=%+v", got)
	}
	query := stub.queries[0]
	for _, required := range []string{"onchain.contracts AS c FINAL", "onchain.contract_creations AS cc FINAL", "c.chain_id=56", "c.contract_address='0x0000000000000000000000000000000000000011'"} {
		if !strings.Contains(query, required) {
			t.Fatalf("query missing %q:\n%s", required, query)
		}
	}
}

func TestFamilyQueriesUseStrictScopeAndFinal(t *testing.T) {
	row := validContractRow()
	row["family_count"] = "7"
	stub := &queryStub{rows: []map[string]any{row}}
	repo := NewRepository(stub)
	hash := "0x" + strings.Repeat("c", 64)
	if _, count, err := repo.FindByRuntimeHash(context.Background(), 56, hash, 20); err != nil || count != 7 {
		t.Fatalf("runtime count=%d err=%v", count, err)
	}
	if _, count, err := repo.FindByCreator(context.Background(), 56, "0x0000000000000000000000000000000000000022", 20); err != nil || count != 7 {
		t.Fatalf("creator count=%d err=%v", count, err)
	}
	if _, count, err := repo.FindByFactory(context.Background(), 56, "0x0000000000000000000000000000000000000033", 20); err != nil || count != 7 {
		t.Fatalf("factory count=%d err=%v", count, err)
	}
	for _, query := range stub.queries {
		if !strings.Contains(query, "onchain.contracts AS c FINAL") || !strings.Contains(query, "onchain.contract_creations AS cc FINAL") || !strings.Contains(query, "c.chain_id=56") {
			t.Fatalf("unsafe family query:\n%s", query)
		}
	}
}

func TestRepositoryRejectsInjectionBeforeQuery(t *testing.T) {
	stub := &queryStub{}
	repo := NewRepository(stub)
	bad := []string{"0x1' OR 1=1 --", "0x" + strings.Repeat("a", 40) + "'", "../contract"}
	for _, value := range bad {
		if _, err := repo.GetContract(context.Background(), 56, value); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("value=%q err=%v", value, err)
		}
	}
	if len(stub.queries) != 0 {
		t.Fatalf("invalid input reached database: %d queries", len(stub.queries))
	}
	if _, _, err := repo.FindByRuntimeHash(context.Background(), 56, "0x' UNION SELECT", 10); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("hash err=%v", err)
	}
}

func TestRepositoryDesensitizesQueryErrorsAndValidatesRows(t *testing.T) {
	secret := errors.New("clickhouse password=top-secret")
	_, err := NewRepository(&queryStub{err: secret}).GetContract(context.Background(), 56, "0x0000000000000000000000000000000000000011")
	if !errors.Is(err, ErrQueryFailed) || errors.Is(err, secret) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("leaked error: %v", err)
	}
	row := validContractRow()
	row["contract_address"] = "not-an-address"
	_, err = NewRepository(&queryStub{rows: []map[string]any{row}}).GetContract(context.Background(), 56, "0x0000000000000000000000000000000000000011")
	if !errors.Is(err, ErrInvalidData) {
		t.Fatalf("invalid row err=%v", err)
	}
}
