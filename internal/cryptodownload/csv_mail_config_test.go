package cryptodownload

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeCSVMailConfigRepairsGmailAddressInHost(t *testing.T) {
	cfg := normalizeCSVMailConfig(Config{
		CSVEmail:    "owner@gmail.com",
		CSVIMAPHost: "owner@gmail.com",
	})
	if cfg.CSVIMAPHost != "imap.gmail.com" {
		t.Fatalf("CSVIMAPHost=%q, want imap.gmail.com", cfg.CSVIMAPHost)
	}
	if cfg.CSVIMAPPort != 993 {
		t.Fatalf("CSVIMAPPort=%d, want 993", cfg.CSVIMAPPort)
	}
	if cfg.CSVIMAPUser != "owner@gmail.com" {
		t.Fatalf("CSVIMAPUser=%q, want Gmail address", cfg.CSVIMAPUser)
	}
}

func TestNormalizeGUIPersistedSettingsRepairsGmailFields(t *testing.T) {
	settings := normalizeGUIPersistedSettings(GUIPersistedSettings{
		Source:       "csv",
		CSVEmail:     "owner@gmail.com",
		CSVIMAPHost:  "owner@gmail.com",
		CSVIMAPPort:  993,
		CSVIMAPUser:  "",
		OutputPrefix: "wallet_export",
	})
	if settings.CSVIMAPHost != "imap.gmail.com" || settings.CSVIMAPUser != "owner@gmail.com" {
		t.Fatalf("normalized settings host=%q user=%q", settings.CSVIMAPHost, settings.CSVIMAPUser)
	}
}

func TestGUIPauseMessageExplainsIMAPFailureWithoutVPNAdvice(t *testing.T) {
	message := guiPauseMessage(errors.New("capture IMAP UID baseline: dial tcp: lookup owner@gmail.com"))
	if !strings.Contains(message, "IMAP 连接失败") {
		t.Fatalf("message=%q, want IMAP guidance", message)
	}
	if strings.Contains(strings.ToUpper(message), "VPN") {
		t.Fatalf("message=%q must not recommend VPN for an IMAP configuration error", message)
	}
}

func TestGUIPauseMessageExplainsPermanentSignature(t *testing.T) {
	message := guiPauseMessage(errors.New(`HTTP 400: {"code":50113,"msg":"incorrect request sign parameters"}`))
	if !strings.Contains(message, "请求签名失效") {
		t.Fatalf("message=%q, want signature guidance", message)
	}
}
