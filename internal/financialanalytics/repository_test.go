package financialanalytics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const testAddress = "0x1111111111111111111111111111111111111111"

type queryStub struct {
	rows  []map[string]any
	err   error
	query string
}

func (s *queryStub) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
	s.query = query
	return s.rows, s.err
}

func validQuery() Query {
	return Query{ChainID: 56, Address: testAddress, Window: WindowCustom, From: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC), LargeThresholdUSD: "250000", EntityMinConfidence: "MEDIUM", Limit: 10}
}

func TestFinancialSummaryPreservesMissingUSDAndUsesHistoricalFacts(t *testing.T) {
	row := map[string]any{
		"total_in_usd": nil, "total_out_usd": nil, "netflow_usd": nil,
		"native_in_usd": nil, "native_out_usd": nil, "stablecoin_in_usd": nil, "stablecoin_out_usd": nil, "token_in_usd": nil, "token_out_usd": nil,
		"largest_in_usd": nil, "largest_out_usd": nil, "average_in_usd": nil, "average_out_usd": nil, "median_in_usd": nil, "median_out_usd": nil,
		"first_funding": "", "latest_funding": "", "large_in_count": "0", "large_out_count": "0", "large_in_usd": nil, "large_out_usd": nil,
		"activity_count": "3", "priced_activity_count": "0", "missing_price_count": "3", "coverage_ratio": "0",
	}
	stub := &queryStub{rows: []map[string]any{row}}
	out, err := NewRepository(stub).FinancialSummary(context.Background(), validQuery())
	if err != nil {
		t.Fatal(err)
	}
	if out.Flow.TotalInUSD != nil || out.LargestInUSD != nil || out.Large.InUSD != nil {
		t.Fatalf("missing prices must remain null: %+v", out)
	}
	if out.PriceCoverage.MissingPriceCount != 3 || out.Large.ThresholdUSD != "250000" {
		t.Fatalf("wrong coverage/threshold: %+v", out)
	}
	for _, required := range []string{"onchain.address_activity AS a FINAL", "usd_value IS NOT NULL", "toDecimal128('250000',18)", "onchain.token_metadata_registry FINAL"} {
		if !strings.Contains(stub.query, required) {
			t.Fatalf("query missing %q", required)
		}
	}
	for _, forbidden := range []string{" OFFSET ", "current_price", "now()"} {
		if strings.Contains(stub.query, forbidden) {
			t.Fatalf("query contains forbidden %q", forbidden)
		}
	}
}

func TestAddressUSDFlowUsesSummaryContract(t *testing.T) {
	row := map[string]any{
		"total_in_usd": "200", "total_out_usd": "50", "netflow_usd": "150", "native_in_usd": nil, "native_out_usd": nil,
		"stablecoin_in_usd": "200", "stablecoin_out_usd": "50", "token_in_usd": nil, "token_out_usd": nil,
		"largest_in_usd": "200", "largest_out_usd": "50", "average_in_usd": "200", "average_out_usd": "50", "median_in_usd": "200", "median_out_usd": "50",
		"first_funding": "2025-01-01 00:00:00", "latest_funding": "2025-01-01 00:00:00", "large_in_count": "0", "large_out_count": "0", "large_in_usd": nil, "large_out_usd": nil,
		"activity_count": "2", "priced_activity_count": "2", "missing_price_count": "0", "coverage_ratio": "1",
	}
	out, err := NewRepository(&queryStub{rows: []map[string]any{row}}).AddressUSDFlow(context.Background(), validQuery())
	if err != nil || out.NetflowUSD == nil || *out.NetflowUSD != "150" {
		t.Fatalf("unexpected flow: %+v err=%v", out, err)
	}
}

func TestCEXRequiresTypeRoleAndConfidence(t *testing.T) {
	stub := &queryStub{rows: []map[string]any{}}
	_, err := NewRepository(stub).CEXStats(context.Background(), validQuery())
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"e.entity_type='CEX'", "l.entity_role IN ('DEPOSIT','COLLECTOR','HOT_WALLET')", "upper(l.confidence)='MEDIUM'", ">=2", "onchain.address_labels FINAL", "onchain.entity_registry FINAL"} {
		if !strings.Contains(stub.query, required) {
			t.Fatalf("CEX query missing %q\n%s", required, stub.query)
		}
	}
}

func TestDEXUsesCanonicalSwapUnitWithoutTransferSum(t *testing.T) {
	stub := &queryStub{rows: []map[string]any{{"swap_count": "2", "swap_volume_usd": "500", "top_protocol": "PancakeSwap"}}}
	out, err := NewRepository(stub).DEXStats(context.Background(), validQuery())
	if err != nil {
		t.Fatal(err)
	}
	if out.SwapCount != 2 || out.CanonicalUnit != "chain_id+tx_hash+pool+trader+token_in+token_out" {
		t.Fatalf("unexpected DEX result: %+v", out)
	}
	if !strings.Contains(stub.query, "GROUP BY tx_hash,protocol,pool,trader,token_in,token_out") || !strings.Contains(stub.query, "max(usd) usd") || !strings.Contains(stub.query, "onchain.parsed_events FINAL") {
		t.Fatalf("canonical grouping missing: %s", stub.query)
	}
	if strings.Contains(stub.query, "token_transfers") || strings.Contains(stub.query, "address_activity") {
		t.Fatalf("DEX volume must not sum transfers: %s", stub.query)
	}
}

func TestQueryErrorIsSanitized(t *testing.T) {
	secret := "password=super-secret"
	stub := &queryStub{err: errors.New(secret)}
	_, err := NewRepository(stub).FinancialSummary(context.Background(), validQuery())
	if !errors.Is(err, ErrQueryFailed) || strings.Contains(err.Error(), secret) {
		t.Fatalf("backend error leaked: %v", err)
	}
}

func TestValidationRejectsUnsafeInputs(t *testing.T) {
	for _, mutate := range []func(*Query){
		func(q *Query) { q.Address = "' OR 1=1 --" },
		func(q *Query) { q.LargeThresholdUSD = "1); DROP TABLE x" },
		func(q *Query) { q.EntityMinConfidence = "TRUST_ME" },
		func(q *Query) { q.Limit = 101 },
	} {
		q := validQuery()
		mutate(&q)
		if _, err := validateQuery(q); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected validation error for %+v: %v", q, err)
		}
	}
}
