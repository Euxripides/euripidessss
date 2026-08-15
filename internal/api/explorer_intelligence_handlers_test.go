package api

import (
	"strings"
	"testing"
	"time"
)

func TestExplorerSearchTextAllowsNamesAndRejectsSQLSyntax(t *testing.T) {
	for _, value := range []string{"USDT", "Binance Deposit", "币安 热钱包", "Pangu_Profit-1"} {
		if got, ok := explorerSearchText(value); !ok || got != value {
			t.Fatalf("valid search rejected: %q got=%q ok=%v", value, got, ok)
		}
	}
	for _, value := range []string{"USDT' OR 1=1", "x;DROP TABLE", "foo\\bar", "a/*b*/"} {
		if _, ok := explorerSearchText(value); ok {
			t.Fatalf("unsafe search accepted: %q", value)
		}
	}
}

func TestExplorerLargeTransfersQueryUsesOverflowSafeFloat(t *testing.T) {
	query := explorerLargeTransfersQuery(56)
	if strings.Contains(query, "Decimal(38,18)") {
		t.Fatal("large-transfer query must not narrow arbitrary activity amounts to Decimal(38,18)")
	}
	for _, required := range []string{"toFloat64(a.amount)", "isFinite(t.historical_value_usdt)", "BETWEEN 100000 AND 1e15", "a.chain_id=56"} {
		if !strings.Contains(query, required) {
			t.Fatalf("missing overflow/data-quality guard %q", required)
		}
	}
}

func TestExplorerSystemAddressesAndZeroTime(t *testing.T) {
	for address, want := range map[string]string{zeroAddress: "Zero Address", deadAddress: "Dead Address"} {
		got, ok := explorerSystemAddress(address)
		if !ok || got != want {
			t.Fatalf("system address %s got=%q ok=%v", address, got, ok)
		}
	}
	if value := explorerTimeValue(time.Time{}); value != nil {
		t.Fatalf("zero time must be null, got %v", value)
	}
	if value := explorerTimeValue(time.Unix(0, 0)); value != nil {
		t.Fatalf("1970 time must be null, got %v", value)
	}
}
