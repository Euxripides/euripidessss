package normalize

import "testing"

func TestMethodSignatureClassification(t *testing.T) {
	if item := ResolveMethod("0xA9059CBB"); item.FunctionName != "transfer" || item.Category != "TRANSFER" {
		t.Fatalf("unexpected transfer signature: %+v", item)
	}
	if item := ResolveMethod("0xdeadbeef"); item.Category != "OTHER" || item.Signature != "" {
		t.Fatalf("unknown method must remain unresolved: %+v", item)
	}
	if activity := ActivityTypeForMethod("0x38ed1739", "CONTRACT_CALL"); activity != "SWAP" {
		t.Fatalf("unexpected activity: %s", activity)
	}
}
