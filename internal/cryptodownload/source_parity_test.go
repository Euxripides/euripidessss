package cryptodownload

import (
	"encoding/json"
	"testing"
)

func TestOriginalDownloadSourcesRemainExplicitlyRouted(t *testing.T) {
	for _, source := range []string{"rpc", "csv", "browser"} {
		if got := normalizedSource(source); got != source {
			t.Fatalf("normalizedSource(%q)=%q, want %q", source, got, source)
		}
		if !supportedSource(source) {
			t.Fatalf("supportedSource(%q)=false, want true", source)
		}
	}
}

func TestOriginalAddressInputNormalizationRemovesInvisibleUnicode(t *testing.T) {
	const clean = "0x28c6c06298d514db089934071355e5743bf21d60"
	entries := parseGUIAddressChains(GUIStartRequest{
		Addresses: "\u200c" + clean,
		Chains:    "ETH",
	})
	if len(entries) != 1 || entries[0].Address != clean || entries[0].Chain != "ETH" {
		t.Fatalf("entries=%+v, want normalized original address input", entries)
	}
}

func TestOriginalRPCRequestDefaultsToLatestBlock(t *testing.T) {
	var request GUIStartRequest
	if err := json.Unmarshal([]byte(`{"source":"rpc"}`), &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request.EndBlock != -1 {
		t.Fatalf("EndBlock=%d, want original default -1", request.EndBlock)
	}

	if err := json.Unmarshal([]byte(`{"source":"rpc","endBlock":0}`), &request); err != nil {
		t.Fatalf("decode explicit end block: %v", err)
	}
	if request.EndBlock != 0 {
		t.Fatalf("EndBlock=%d, want explicit 0", request.EndBlock)
	}
}

func TestCSVSmallDataFallsBackToBrowserOnlyAfterDirectFailure(t *testing.T) {
	signFailure := ExportData{
		Errors: []string{"csv transactions BSC: code 50113: incorrect request sign parameters"},
	}
	if !csvShouldFallbackToBrowser(signFailure) {
		t.Fatal("expected an empty direct CSV result with a permanent signature failure to fall back to browser")
	}

	partialDirectResult := signFailure
	partialDirectResult.Transactions = []map[string]any{{"hash": "0x1"}}
	if csvShouldFallbackToBrowser(partialDirectResult) {
		t.Fatal("must keep a usable direct CSV result instead of replacing it with browser output")
	}

	if csvShouldFallbackToBrowser(ExportData{}) {
		t.Fatal("must not use browser fallback when direct CSV download has no error")
	}
}
