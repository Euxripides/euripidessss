package eventdecoder

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestDecodeBuiltinTransfer(t *testing.T) {
	decoder := New(nil)
	from := "1111111111111111111111111111111111111111"
	to := "2222222222222222222222222222222222222222"
	result, err := decoder.Decode(context.Background(), Log{
		Topics: []string{
			"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
			wordAddress(from), wordAddress(to),
		},
		Data: wordUint(125),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.EventName != "Transfer" || result.EventSignature != "Transfer(address,address,uint256)" {
		t.Fatalf("unexpected identity: %+v", result)
	}
	if result.DecoderSource != SourceTopic0 || result.DecoderConfidence != ConfidenceLow {
		t.Fatalf("unexpected provenance: %+v", result)
	}
	if got := result.DecodedFields["from"]; got != "0x"+from {
		t.Fatalf("from = %v", got)
	}
	if got := result.DecodedFields["to"]; got != "0x"+to {
		t.Fatalf("to = %v", got)
	}
	if got := result.DecodedFields["value"]; got != "125" {
		t.Fatalf("value = %v", got)
	}
}

func TestVerifiedABIOverridesBuiltin(t *testing.T) {
	definition := builtin("TransferVerified", "Transfer(address,address,uint256)", []Input{{"sender", "address", true}, {"recipient", "address", true}, {"amount", "uint256", false}})
	definition.Source = SourceVerifiedABI
	definition.Confidence = ConfidenceHigh
	decoder := New(NewMemoryRegistry(definition))
	result, err := decoder.Decode(context.Background(), Log{Topics: []string{definition.Topic0, wordAddress(strings.Repeat("1", 40)), wordAddress(strings.Repeat("2", 40))}, Data: wordUint(1)})
	if err != nil {
		t.Fatal(err)
	}
	if result.EventName != "TransferVerified" || result.DecoderSource != SourceVerifiedABI || result.DecoderConfidence != ConfidenceHigh {
		t.Fatalf("verified ABI did not win: %+v", result)
	}
}

func TestHighestPriorityConflictIsAmbiguousAndSorted(t *testing.T) {
	topic := Topic0("Conflict(uint256)")
	registry := NewMemoryRegistry(
		EventDefinition{Name: "Z", Signature: "Z(uint256)", Topic0: topic, Inputs: []Input{{"value", "uint256", false}}, Source: SourceLocalABI},
		EventDefinition{Name: "A", Signature: "A(uint256)", Topic0: topic, Inputs: []Input{{"value", "uint256", false}}, Source: SourceLocalABI},
		EventDefinition{Name: "Verified", Signature: "Verified(uint256)", Topic0: topic, Inputs: []Input{{"value", "uint256", false}}, Source: SourceVerifiedABI},
		EventDefinition{Name: "VerifiedOther", Signature: "VerifiedOther(uint256)", Topic0: topic, Inputs: []Input{{"value", "uint256", false}}, Source: SourceVerifiedABI},
	)
	result, err := New(registry).Decode(context.Background(), Log{Topics: []string{topic}, Data: wordUint(1)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ambiguous || result.EventName != "ambiguous" || result.DecoderSource != SourceVerifiedABI {
		t.Fatalf("expected verified ambiguity: %+v", result)
	}
	want := []string{"Verified(uint256)", "VerifiedOther(uint256)"}
	if fmt.Sprint(result.CandidateSignatures) != fmt.Sprint(want) {
		t.Fatalf("candidates = %v, want %v", result.CandidateSignatures, want)
	}
}

func TestSameSignatureWithConflictingIndexedLayoutIsAmbiguous(t *testing.T) {
	signature := "Layout(address,uint256)"
	topic := Topic0(signature)
	registry := NewMemoryRegistry(
		EventDefinition{Name: "Layout", Signature: signature, Topic0: topic, Inputs: []Input{{"account", "address", true}, {"value", "uint256", false}}, Source: SourceVerifiedABI},
		EventDefinition{Name: "Layout", Signature: signature, Topic0: topic, Inputs: []Input{{"account", "address", false}, {"value", "uint256", false}}, Source: SourceVerifiedABI},
	)
	result, err := New(registry).Decode(context.Background(), Log{Topics: []string{topic, wordAddress(strings.Repeat("1", 40))}, Data: wordUint(1)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ambiguous || len(result.CandidateSignatures) != 1 || result.CandidateSignatures[0] != signature {
		t.Fatalf("layout conflict must be explicit: %+v", result)
	}
}

func TestUnknownAndDecodeFailureRetainRaw(t *testing.T) {
	unknownTopic := "0x" + strings.Repeat("ab", 32)
	unknown, err := New(nil).Decode(context.Background(), Log{Topics: []string{unknownTopic}, Data: "0x1234"})
	if err != nil {
		t.Fatal(err)
	}
	if unknown.EventName != "raw" || unknown.DecoderSource != SourceRaw || unknown.Raw.Data != "0x1234" {
		t.Fatalf("unknown event not retained: %+v", unknown)
	}

	transfer := Topic0("Transfer(address,address,uint256)")
	failed, err := New(nil).Decode(context.Background(), Log{Topics: []string{transfer}, Data: "0x"})
	if err != nil {
		t.Fatal(err)
	}
	if failed.EventName != "Transfer" || failed.DecodeError == "" || failed.Raw.Topics[0] != transfer {
		t.Fatalf("decode failure lost identity/raw: %+v", failed)
	}
}

func TestMalformedInputIsBounded(t *testing.T) {
	_, err := New(nil).Decode(context.Background(), Log{Topics: []string{"0x123"}})
	if err == nil {
		t.Fatal("expected odd hex error")
	}
	_, err = New(nil).Decode(context.Background(), Log{Topics: make([]string, maxTopics+1)})
	if err == nil {
		t.Fatal("expected topic limit error")
	}
}

func TestBuiltinCoverage(t *testing.T) {
	want := map[string]bool{"Transfer": false, "Approval": false, "OwnershipTransferred": false, "PairCreated": false, "Swap": false, "Mint": false, "Burn": false, "Deposit": false, "Withdrawal": false, "Withdraw": false}
	registry := BuiltinRegistry()
	for _, definition := range registry.definitions {
		if _, ok := want[definition.Name]; ok {
			want[definition.Name] = true
		}
		if definition.Topic0 == "" || len(definition.Topic0) != 66 {
			t.Fatalf("invalid topic0 for %s", definition.Name)
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing builtin %s", name)
		}
	}
}

func wordAddress(address string) string {
	return "0x" + strings.Repeat("0", 24) + address
}

func wordUint(value int) string {
	return fmt.Sprintf("0x%064x", value)
}
