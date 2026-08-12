package cryptodownload

import "testing"

func TestNormalizeCSVDeliveryMode(t *testing.T) {
	for input, want := range map[string]string{
		"": "auto", "AUTO": "auto", " direct ": "direct", "EMAIL": "email", "unknown": "auto",
	} {
		if got := normalizeCSVDeliveryMode(input); got != want {
			t.Fatalf("normalizeCSVDeliveryMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPersistedRequestKeepsCSVDeliveryMode(t *testing.T) {
	record := guiPersistedRequest(GUIStartRequest{Source: "csv", CSVDeliveryMode: "email"})
	if record.CSVDeliveryMode != "email" {
		t.Fatalf("persisted delivery mode = %q, want email", record.CSVDeliveryMode)
	}
	restored := guiStartRequestFromPersisted(GUIJobRecord{Request: record}, GUIPersistedSettings{})
	if restored.CSVDeliveryMode != "email" {
		t.Fatalf("restored delivery mode = %q, want email", restored.CSVDeliveryMode)
	}
}
