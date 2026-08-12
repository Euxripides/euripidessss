package cryptodownload

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCSVAutomationConfigAllowsDirectWithoutMailbox(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadCSVAutomationConfig(dir, filepath.Join(dir, "raw"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != "csv" || cfg.RawDir == "" || cfg.CSVEmail != "" {
		t.Fatalf("unexpected direct config: %+v", cfg)
	}
}

func TestLoadCSVAutomationConfigRejectsPartialMailboxWithoutLeakingSecret(t *testing.T) {
	dir := t.TempDir()
	settingsDir := filepath.Join(dir, "wallet-exporter")
	if err := os.MkdirAll(settingsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := "mail-secret-value"
	content := `{"csvEmail":"owner@example.com","csvImapPassword":"` + secret + `"}`
	if err := os.WriteFile(filepath.Join(settingsDir, "gui-settings.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCSVAutomationConfig(dir, filepath.Join(dir, "raw"))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("expected redacted partial-mail error, got %v", err)
	}
}

func TestEmbeddedCSVSignerRuntimeIsMaterializedAndReady(t *testing.T) {
	path, err := materializeEmbeddedCSVSigner()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "oklink_csv_signer.mjs" {
		t.Fatalf("unexpected signer entry: %s", path)
	}
	for _, name := range embeddedCSVSignerFiles {
		if info, err := os.Stat(filepath.Join(filepath.Dir(path), name)); err != nil || info.Size() == 0 {
			t.Fatalf("embedded signer file %s unavailable: info=%v err=%v", name, info, err)
		}
	}
	if err := ValidateCSVAutomationRuntime(defaultBaseURL); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizeCSVDownloadCheckUsesAbsoluteTolerance(t *testing.T) {
	withinBelow := finalizeCSVDownloadCheck(CSVDownloadCheck{ExpectedTotal: 1000}, 900, true)
	withinAbove := finalizeCSVDownloadCheck(CSVDownloadCheck{ExpectedTotal: 1000}, 1100, true)
	outsideBelow := finalizeCSVDownloadCheck(CSVDownloadCheck{ExpectedTotal: 1000}, 899, true)
	outsideAbove := finalizeCSVDownloadCheck(CSVDownloadCheck{ExpectedTotal: 1000}, 1101, true)
	if withinBelow.Status != "complete" || withinAbove.Status != "complete" {
		t.Fatalf("difference exactly 100 must pass: below=%+v above=%+v", withinBelow, withinAbove)
	}
	if outsideBelow.Status != "incomplete" || outsideAbove.Status != "incomplete" {
		t.Fatalf("difference above 100 must fail: below=%+v above=%+v", outsideBelow, outsideAbove)
	}
}

func TestFinalizeCSVDownloadCheckRejectsZeroForNonEmptySource(t *testing.T) {
	check := finalizeCSVDownloadCheck(CSVDownloadCheck{ExpectedTotal: 99}, 0, true)
	if check.Status != "incomplete" {
		t.Fatalf("status=%q, want incomplete", check.Status)
	}
}

func TestCSVEmailRequestNotSentTransientClassification(t *testing.T) {
	if !csvEmailRequestNotSentIsTransient(errors.New("[WinError 10055] socket queue full")) {
		t.Fatal("WinError 10055 should retry")
	}
	if csvEmailRequestNotSentIsTransient(errors.New("request rejected: invalid address")) {
		t.Fatal("invalid request must fail closed")
	}
}

func TestCSVTokenChineseAddressHeaderIsPreserved(t *testing.T) {
	contract := "0x1111111111111111111111111111111111111111"
	row := csvRecordToExportRow("0x2222222222222222222222222222222222222222", "BSC",
		csvExportKind{Name: "token_transfers", Sheet: "token"}, map[string]string{
			"交易哈希": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"代币地址": contract,
		})
	if got := toString(row["tokenContractAddress"]); got != contract {
		t.Fatalf("token address header was dropped: got=%q want=%q", got, contract)
	}
}
