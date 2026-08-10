package eventdecoder

import "testing"

func TestClassifyDEXSwapOnlyWithCompleteFields(t *testing.T) {
	log := Log{TransactionHash: "0xtx"}
	event := Result{EventName: "Swap", DecodedFields: map[string]any{
		"sender": "0xtrader", "amount0In": "100", "amount1In": "0", "amount0Out": "0", "amount1Out": "200",
	}}
	context := SemanticContext{Protocol: "PancakeSwap", Pool: "0xpool", Token0: "0xtoken0", Token1: "0xtoken1"}
	result := Classify(log, event, context)
	if result.ActivityType != ActivityDEXSwap || result.DEXSwap == nil {
		t.Fatalf("expected DEX swap: %+v", result)
	}
	if result.DEXSwap.TokenIn != "0xtoken0" || result.DEXSwap.AmountIn != "100" || result.DEXSwap.TokenOut != "0xtoken1" || result.DEXSwap.AmountOut != "200" {
		t.Fatalf("wrong swap direction: %+v", result.DEXSwap)
	}

	context.Token1 = ""
	if partial := Classify(log, event, context); partial.ActivityType != ActivityRaw {
		t.Fatalf("partial context must stay raw: %+v", partial)
	}
}

func TestClassifyDEXSwapRejectsAmbiguousFlows(t *testing.T) {
	event := Result{EventName: "Swap", DecodedFields: map[string]any{
		"sender": "0xtrader", "amount0In": "100", "amount1In": "50", "amount0Out": "0", "amount1Out": "200",
	}}
	context := SemanticContext{Protocol: "DEX", Pool: "0xpool", Token0: "0xt0", Token1: "0xt1"}
	if got := Classify(Log{}, event, context); got.ActivityType != ActivityRaw {
		t.Fatalf("ambiguous swap must stay raw: %+v", got)
	}
}

func TestClassifyBridgeRequiresExplicitBridgeContext(t *testing.T) {
	event := Result{EventName: "Deposit", DecodedFields: map[string]any{"wad": "0"}}
	if got := Classify(Log{}, event, SemanticContext{}); got.ActivityType != ActivityRaw {
		t.Fatalf("WETH-like deposit must not be guessed as bridge: %+v", got)
	}
	context := SemanticContext{
		Bridge: "Stargate", SourceChain: "56", DestinationChain: "1",
		SourceAddress: "0xsource", DestinationAddress: "0xdestination", Token: "0xtoken",
	}
	got := Classify(Log{TransactionHash: "0xtx"}, event, context)
	if got.ActivityType != ActivityBridgeDeposit || got.Bridge == nil || got.Bridge.Amount != "0" {
		t.Fatalf("complete bridge deposit not classified: %+v", got)
	}
}

func TestAmbiguousOrFailedEventStaysRaw(t *testing.T) {
	context := SemanticContext{Protocol: "DEX", Pool: "0xpool", Token0: "0xt0", Token1: "0xt1"}
	for _, event := range []Result{
		{EventName: "Swap", Ambiguous: true},
		{EventName: "Swap", DecodeError: "bad ABI"},
	} {
		if got := Classify(Log{}, event, context); got.ActivityType != ActivityRaw {
			t.Fatalf("unreliable event must stay raw: %+v", got)
		}
	}
}

func TestPreserveCallsKeepsZeroValue(t *testing.T) {
	calls := []CanonicalCall{{FromAddress: "0xa", ToAddress: "0xb", ValueRaw: "0", Input: "0x1234", CallType: "CALL", Success: true}}
	got := PreserveCalls(calls)
	if len(got) != 1 || got[0].ValueRaw != "0" || got[0].Input != "0x1234" {
		t.Fatalf("zero-value call was filtered or changed: %+v", got)
	}
	got[0].ValueRaw = "1"
	if calls[0].ValueRaw != "0" {
		t.Fatal("preserved call slice aliases input")
	}
}
