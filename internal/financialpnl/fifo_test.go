package financialpnl

import (
	"testing"
	"time"
)

func ptr(value string) *string { return &value }

func TestFIFORealizedPnLDeductsGas(t *testing.T) {
	now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	events := []PositionEvent{
		{Time: now.Add(-2 * time.Hour), BlockNumber: 1, EventIndex: 0, TransactionHash: hash('1'), Type: EventDEXBuy, Amount: "100", USDValue: ptr("100000"), PriceVersion: "historical-v1", DataSnapshotVersion: "facts-v1"},
		{Time: now.Add(-time.Hour), BlockNumber: 2, EventIndex: 0, TransactionHash: hash('2'), Type: EventDEXSell, Amount: "100", USDValue: ptr("150000"), GasUSD: ptr("1000")},
	}
	result, err := (Engine{StaleAfter: time.Minute}).Calculate(validQuery(now), events, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.RealizedPnLUSD != "49000" || result.RealizedCostBasisUSD != "100000" || result.RealizedGasUSD != "1000" {
		t.Fatalf("unexpected realized result: %+v", result)
	}
	if result.KnownCostBasisRatio != "1" || result.PositionAmount != "0" {
		t.Fatalf("unexpected coverage/position: %+v", result)
	}
}

func TestTransferOutNeverCountsAsSell(t *testing.T) {
	now := time.Now().UTC()
	events := []PositionEvent{
		{Time: now.Add(-2 * time.Hour), TransactionHash: hash('1'), Type: EventDEXBuy, Amount: "100", USDValue: ptr("200")},
		{Time: now.Add(-time.Hour), TransactionHash: hash('2'), Type: EventTransferOut, Amount: "40", USDValue: ptr("999999")},
	}
	result, err := (Engine{}).Calculate(validQuery(now), events, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.SoldAmount != "0" || result.RealizedPnLUSD != "0" || result.PositionAmount != "60" || result.RemainingKnownCostUSD != "120" {
		t.Fatalf("transfer was treated as a sale: %+v", result)
	}
}

func TestUnknownCostBasisCoverageAndFIFO(t *testing.T) {
	now := time.Now().UTC()
	events := []PositionEvent{
		{Time: now.Add(-3 * time.Hour), TransactionHash: hash('1'), Type: EventTransferIn, Amount: "40"},
		{Time: now.Add(-2 * time.Hour), TransactionHash: hash('2'), Type: EventKnownBuy, Amount: "60", USDValue: ptr("120")},
		{Time: now.Add(-time.Hour), TransactionHash: hash('3'), Type: EventKnownSell, Amount: "50", USDValue: ptr("200"), GasUSD: ptr("10")},
	}
	result, err := (Engine{}).Calculate(validQuery(now), events, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.KnownSoldAmount != "10" || result.KnownCostBasisRatio != "0.2" {
		t.Fatalf("coverage mismatch: %+v", result)
	}
	// Only 20% of proceeds/gas and $20 known FIFO cost may be asserted: 40 - 20 - 2 = 18.
	if result.RealizedPnLUSD != "18" || result.RealizedProceedsCoveredUSD != "40" || result.RealizedGasUSD != "2" {
		t.Fatalf("unknown basis fabricated pnl: %+v", result)
	}
	if result.PositionAmount != "50" || result.KnownPositionAmount != "50" || result.UnrealizedCoverage != "1" {
		t.Fatalf("remaining position mismatch: %+v", result)
	}
}

func TestUnrealizedUsesCurrentPriceAndMarksStale(t *testing.T) {
	now := time.Now().UTC()
	price := &Price{USD: "3", Time: now.Add(-2 * time.Minute), Source: "LOCAL", Version: "price-v2"}
	result, err := (Engine{StaleAfter: time.Minute}).Calculate(validQuery(now), []PositionEvent{{Time: now.Add(-time.Hour), TransactionHash: hash('1'), Type: EventDEXBuy, Amount: "10", USDValue: ptr("20")}}, price)
	if err != nil {
		t.Fatal(err)
	}
	if result.CurrentPriceStatus != "STALE" || result.PositionMarketValueUSD == nil || *result.PositionMarketValueUSD != "30" || result.KnownUnrealizedPnLUSD == nil || *result.KnownUnrealizedPnLUSD != "10" {
		t.Fatalf("stale unrealized mismatch: %+v", result)
	}

	missing, err := (Engine{}).Calculate(validQuery(now), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if missing.CurrentPriceStatus != "MISSING" || missing.CurrentPriceUSD != nil || missing.PositionMarketValueUSD != nil {
		t.Fatalf("missing price became fake zero: %+v", missing)
	}
}

func TestBuyGasIsCapitalizedIntoFIFO(t *testing.T) {
	now := time.Now().UTC()
	events := []PositionEvent{
		{Time: now.Add(-2 * time.Hour), TransactionHash: hash('1'), Type: EventDEXBuy, Amount: "10", USDValue: ptr("100"), GasUSD: ptr("1")},
		{Time: now.Add(-time.Hour), TransactionHash: hash('2'), Type: EventDEXSell, Amount: "10", USDValue: ptr("150")},
	}
	result, err := (Engine{}).Calculate(validQuery(now), events, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.RealizedCostBasisUSD != "101" || result.RealizedPnLUSD != "49" {
		t.Fatalf("buy gas not capitalized: %+v", result)
	}
}

func TestRejectsInvalidFinancialInputs(t *testing.T) {
	now := time.Now().UTC()
	for _, event := range []PositionEvent{
		{Time: now, Type: EventDEXBuy, Amount: "0", USDValue: ptr("1")},
		{Time: now, Type: EventDEXBuy, Amount: "1"},
		{Time: now, Type: EventDEXSell, Amount: "1", USDValue: ptr("-1")},
		{Time: now, Type: EventType("TRANSFER_SELL"), Amount: "1"},
	} {
		if _, err := (Engine{}).Calculate(validQuery(now), []PositionEvent{event}, nil); err == nil {
			t.Fatalf("accepted invalid event: %+v", event)
		}
	}
}

func validQuery(asOf time.Time) Query {
	return Query{ChainID: 56, Address: "0x1111111111111111111111111111111111111111", Token: "0x2222222222222222222222222222222222222222", AsOf: asOf}
}
func hash(ch byte) string { return "0x" + string(make([]byte, 0)) + repeat(ch, 64) }
func repeat(ch byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}
