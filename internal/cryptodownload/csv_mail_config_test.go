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

func TestGUIPauseMessageDistinguishesIMAPLoginFromNetworkFailure(t *testing.T) {
	message := guiPauseMessage(errors.New("capture baseline: csv mail login_config_failure (login): Lookup failed redacted"))
	if !strings.Contains(message, "登录或授权失败") || !strings.Contains(message, "应用专用密码") {
		t.Fatalf("message=%q, want Gmail authorization guidance", message)
	}
	if strings.Contains(message, "检查 IMAP 主机、端口") {
		t.Fatalf("message=%q must not misclassify an authentication failure as connectivity", message)
	}
}

func TestValidateGUIMailIdentityRejectsWrongGmailUser(t *testing.T) {
	if err := validateGUIMailIdentity("owner@gmail.com", "imap.gmail.com", "postgres"); err == nil {
		t.Fatal("expected non-email Gmail IMAP user to be rejected")
	}
	if err := validateGUIMailIdentity("owner@gmail.com", "imap.gmail.com", "other@gmail.com"); err == nil {
		t.Fatal("expected mismatched Gmail IMAP user to be rejected")
	}
	if err := validateGUIMailIdentity("owner@gmail.com", "imap.gmail.com", "owner@gmail.com"); err != nil {
		t.Fatalf("matching Gmail identity rejected: %v", err)
	}
}

func TestCSVIMAPLoginErrorClassification(t *testing.T) {
	for _, message := range []string{"imap: connection closed", "unexpected EOF", "read: connection reset by peer", "i/o timeout"} {
		if !csvIMAPLoginErrorIsTransient(errors.New(message)) {
			t.Fatalf("%q should be transient", message)
		}
	}
	if csvIMAPLoginErrorIsTransient(errors.New("authentication failed")) {
		t.Fatal("authentication failure must remain a credential error")
	}
}

func TestGUIPauseMessageExplainsPermanentSignature(t *testing.T) {
	message := guiPauseMessage(errors.New(`HTTP 400: {"code":50113,"msg":"incorrect request sign parameters"}`))
	if !strings.Contains(message, "请求签名失效") {
		t.Fatalf("message=%q, want signature guidance", message)
	}
}
