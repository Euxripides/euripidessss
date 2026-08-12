package financialflow

import (
	"testing"
	"time"
)

func TestStrictFIFORetentionAndPassThrough(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{Address: testAddressA, Token: testToken, Direction: DirectionIn, Kind: EventTransfer, Time: t0, Amount: "100000", USDValue: "100000", TxHash: "0x01"},
		{Address: testAddressA, Token: testToken, Direction: DirectionOut, Kind: EventTransfer, Time: t0.Add(30 * time.Minute), Amount: "20000", USDValue: "20000", TxHash: "0x02"},
		{Address: testAddressA, Token: testToken, Direction: DirectionOut, Kind: EventTransfer, Time: t0.Add(2 * time.Hour), Amount: "50000", USDValue: "50000", TxHash: "0x03"},
	}
	report, err := Analyze(events, Snapshot{ID: "snapshot-1", AsOf: t0.Add(31 * 24 * time.Hour), PriceVersion: "prices-2025-01"})
	if err != nil {
		t.Fatal(err)
	}
	result := report.Results[0]
	if len(result.RetentionWindows) != 5 || len(result.PassThroughWindows) != 5 {
		t.Fatalf("required window sets missing: retention=%d pass-through=%d", len(result.RetentionWindows), len(result.PassThroughWindows))
	}
	oneHour := window(t, result, "1h")
	if oneHour.RetainedAmount != "80000" || oneHour.MatchedTransferAmount != "20000" || oneHour.PassThroughRatio != "0.2" {
		t.Fatalf("unexpected 1h metric: %+v", oneHour)
	}
	twentyFour := window(t, result, "24h")
	if twentyFour.RetainedAmount != "30000" || twentyFour.MatchedTransferAmount != "70000" || twentyFour.PassThroughRatio != "0.7" {
		t.Fatalf("unexpected 24h metric: %+v", twentyFour)
	}
	if result.SettlementRatio30D != "0.3" || result.SettlementRatio30DUSD != "0.3" {
		t.Fatalf("unexpected settlement ratio: %+v", result)
	}
}

func TestAcceptanceThirtyMinutePassThroughIsEightyPercent(t *testing.T) {
	t0 := time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{Address: testAddressA, Token: testToken, Direction: DirectionIn, Time: t0, Amount: "1000000", USDValue: "1000000"},
		{Address: testAddressA, Token: testToken, Direction: DirectionOut, Time: t0.Add(20 * time.Minute), Amount: "800000", USDValue: "800000"},
	}
	report, err := Analyze(events, Snapshot{ID: "pass-through-case", AsOf: t0.Add(time.Hour), PriceVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	metric := window(t, report.Results[0], "30m")
	if metric.MatchedTransferAmount != "800000" || metric.PassThroughRatio != "0.8" {
		t.Fatalf("expected 80%% pass-through, got %+v", metric)
	}
}

func TestFIFOConsumesOldestLotAndDoesNotUseNetFlowShortcut(t *testing.T) {
	t0 := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{Address: testAddressA, Token: testToken, Direction: DirectionOut, Time: t0, Amount: "40"}, // opening balance
		{Address: testAddressA, Token: testToken, Direction: DirectionIn, Time: t0.Add(time.Hour), Amount: "100", USDValue: "200"},
		{Address: testAddressA, Token: testToken, Direction: DirectionIn, Time: t0.Add(2 * time.Hour), Amount: "100", USDValue: "400"},
		{Address: testAddressA, Token: testToken, Direction: DirectionOut, Time: t0.Add(3 * time.Hour), Amount: "150"},
	}
	report, err := Analyze(events, Snapshot{ID: "snapshot-fifo", AsOf: t0.Add(40 * 24 * time.Hour), PriceVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	result := report.Results[0]
	metric := window(t, result, "24h")
	if result.OpeningBalanceOutAmount != "40" {
		t.Fatalf("pre-inflow outgoing must remain opening-balance out, got %s", result.OpeningBalanceOutAmount)
	}
	if metric.RetainedAmount != "50" || metric.RetainedUSD != "200" || metric.MatchedTransferUSD != "400" {
		t.Fatalf("expected first lot fully consumed and half of second lot, got %+v", metric)
	}
}

func TestNativeGasReducesRetentionButNeverPassThrough(t *testing.T) {
	t0 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{Address: testAddressA, Token: NativeAssetID, Direction: DirectionIn, Time: t0, Amount: "10", USDValue: "3000"},
		{Address: testAddressA, Token: NativeAssetID, Direction: DirectionOut, Kind: EventGasFee, Time: t0.Add(10 * time.Minute), Amount: "1", USDValue: "300"},
		{Address: testAddressA, Token: NativeAssetID, Direction: DirectionOut, Kind: EventTransfer, Time: t0.Add(20 * time.Minute), Amount: "4", USDValue: "1200"},
	}
	report, err := Analyze(events, Snapshot{ID: "gas-boundary", AsOf: t0.Add(31 * 24 * time.Hour), PriceVersion: "native-v1"})
	if err != nil {
		t.Fatal(err)
	}
	metric := window(t, report.Results[0], "30m")
	if metric.RetainedAmount != "5" || metric.GasConsumedAmount != "1" || metric.MatchedTransferAmount != "4" {
		t.Fatalf("gas boundary failed: %+v", metric)
	}
	if metric.PassThroughRatio != "0.4" || report.Results[0].GasFeeAmount != "1" {
		t.Fatalf("gas must not be pass-through: %+v", report.Results[0])
	}
}

func TestWindowUsesOnlyMaturedIncomingLots(t *testing.T) {
	t0 := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{{Address: testAddressA, Token: testToken, Direction: DirectionIn, Time: t0, Amount: "100", USDValue: "200"}}
	report, err := Analyze(events, Snapshot{ID: "immature", AsOf: t0.Add(2 * time.Hour), PriceVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if window(t, report.Results[0], "1h").MaturedReceivedAmount != "100" {
		t.Fatal("1h lot should be mature")
	}
	if got := window(t, report.Results[0], "6h"); got.MaturedReceivedAmount != "0" || got.MaturedIncomingLots != 0 {
		t.Fatalf("immature 6h lot must not inflate retention: %+v", got)
	}
}

func TestMissingUSDRemainsCoverageGap(t *testing.T) {
	t0 := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{Address: testAddressA, Token: testToken, Direction: DirectionIn, Time: t0, Amount: "60", USDValue: "120"},
		{Address: testAddressA, Token: testToken, Direction: DirectionIn, Time: t0.Add(time.Minute), Amount: "40"},
	}
	report, err := Analyze(events, Snapshot{ID: "coverage", AsOf: t0.Add(31 * 24 * time.Hour), PriceVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	result := report.Results[0]
	metric := window(t, result, "30d")
	if metric.USDAmountCoverage != "0.6" || metric.USDValuesComplete || result.Coverage.IncomingUSDCoverage != "0.6" {
		t.Fatalf("missing USD must remain explicit: result=%+v metric=%+v", result.Coverage, metric)
	}
}

func TestAnalyzeSeparatesTokensAndIsDeterministic(t *testing.T) {
	t0 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{Address: testAddressA, Token: testToken, Direction: DirectionIn, Time: t0, Amount: "1"},
		{Address: testAddressA, Token: "0x2222222222222222222222222222222222222222", Direction: DirectionIn, Time: t0, Amount: "5"},
	}
	snapshot := Snapshot{ID: "deterministic", AsOf: t0.Add(31 * 24 * time.Hour), PriceVersion: "v1"}
	a, err := Analyze(events, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Analyze(events, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Results) != 2 || a.Results[0].InputDigestSHA256 != b.Results[0].InputDigestSHA256 {
		t.Fatalf("token partition/digest mismatch: %+v %+v", a, b)
	}
}

func TestRejectsGasForNonNativeAsset(t *testing.T) {
	t0 := time.Now().UTC()
	_, err := Analyze([]Event{{Address: testAddressA, Token: testToken, Direction: DirectionOut, Kind: EventGasFee, Time: t0, Amount: "1"}}, Snapshot{ID: "bad", AsOf: t0.Add(time.Hour), PriceVersion: "v1"})
	if err == nil {
		t.Fatal("expected invalid non-native gas event")
	}
}

func TestSkipsZeroAmountEvents(t *testing.T) {
	t0 := time.Now().UTC()
	events := []Event{
		{Address: testAddressA, Token: testToken, Direction: DirectionIn, Time: t0, Amount: "10"},
		{Address: testAddressA, Token: testToken, Direction: DirectionOut, Time: t0.Add(time.Minute), Amount: "0"},
		{Address: testAddressA, Token: testToken, Direction: DirectionOut, Time: t0.Add(2 * time.Minute), Amount: "4"},
	}
	report, err := Analyze(events, Snapshot{ID: "skip-zero", AsOf: t0.Add(3 * time.Hour), PriceVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.SkippedZeroAmountEvents != 1 {
		t.Fatalf("expected 1 skipped zero-amount event, got %d", report.SkippedZeroAmountEvents)
	}
	if len(report.Results) != 1 {
		t.Fatalf("expected 1 token result, got %d", len(report.Results))
	}
}

func window(t *testing.T, result TokenResult, name string) WindowMetric {
	t.Helper()
	all := append(append([]WindowMetric{}, result.RetentionWindows...), result.PassThroughWindows...)
	for _, item := range all {
		if item.Window == name {
			return item
		}
	}
	t.Fatalf("window %s missing", name)
	return WindowMetric{}
}

const (
	testAddressA = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testToken    = "0x1111111111111111111111111111111111111111"
)
