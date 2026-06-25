package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/config"
	"github.com/etl/backend/internal/dunetools"
	"github.com/gin-gonic/gin"
)

func TestHandleDuneBatchStart_rejectsMissingIMAPPassword_whenNoEnvFallback(t *testing.T) {
	// Given
	gin.SetMode(gin.TestMode)
	t.Setenv("DUNE_BATCH_IMAP_PASSWORD", "")
	t.Setenv("DUNE_BATCH_IMAP_USER", "")
	restore := replaceDuneBatchManagerForTest(dunetools.NewManager(dunetools.ManagerOptions{
		Browser:  fakeDuneBatchBrowser{},
		Mailbox:  fakeDuneBatchMailbox{},
		Verifier: fakeDuneBatchVerifier{},
	}))
	defer restore()
	router := gin.New()
	api := router.Group("/api")
	registerDuneBatchRoutes(api)

	// When
	req := httptest.NewRequest(http.MethodPost, "/api/dune/batch/start", strings.NewReader(`{"total":1,"domain":"aurore.online","imap_user":"ldj1009538134@gmail.com"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	// Then
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "IMAP") {
		t.Fatalf("expected IMAP validation message, body=%s", resp.Body.String())
	}
}

func TestHandleDuneBatchStart_keepsSavedAccountsVisible_whenStartingNewTask(t *testing.T) {
	// Given
	gin.SetMode(gin.TestMode)
	allAccountsMu.Lock()
	previousAccounts := allAccounts
	allAccounts = []dunetools.Account{{
		Email:    "existing@example.com",
		Username: "existing",
		Password: "saved-password",
		Status:   dunetools.AccountStatusDone,
	}}
	allAccountsMu.Unlock()
	t.Cleanup(func() {
		allAccountsMu.Lock()
		allAccounts = previousAccounts
		allAccountsMu.Unlock()
	})
	restore := replaceDuneBatchManagerForTest(dunetools.NewManager(dunetools.ManagerOptions{
		Browser:  fakeDuneBatchBrowser{},
		Mailbox:  fakeDuneBatchMailbox{},
		Verifier: fakeDuneBatchVerifier{},
	}))
	defer restore()
	router := gin.New()
	api := router.Group("/api")
	registerDuneBatchRoutes(api)

	// When
	req := httptest.NewRequest(http.MethodPost, "/api/dune/batch/start", strings.NewReader(`{"total":1,"mode":"register","domain":"aurore.online","imap_user":"ldj1009538134@gmail.com","imap_password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	// Then
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.Code, resp.Body.String())
	}
	var body dunetools.TaskSnapshot
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Accounts) == 0 {
		t.Fatalf("accounts length = %d, want saved account to remain visible; body=%s", len(body.Accounts), resp.Body.String())
	}
	if body.Accounts[0].Email != "existing@example.com" {
		t.Fatalf("first account email = %q, want saved account", body.Accounts[0].Email)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/dune/batch/status", nil)
	statusResp := httptest.NewRecorder()
	router.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("status code = %d, body=%s", statusResp.Code, statusResp.Body.String())
	}
	var statusBody dunetools.TaskSnapshot
	if err := json.Unmarshal(statusResp.Body.Bytes(), &statusBody); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if len(statusBody.Accounts) == 0 || statusBody.Accounts[0].Email != "existing@example.com" {
		t.Fatalf("status accounts lost saved account: body=%s", statusResp.Body.String())
	}
}

func TestHandleDuneBatchAccountsLoadsPersistedAccountsOnFirstRequest(t *testing.T) {
	// Given
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	oldCfg := cfg
	cfg = &config.Config{RootDir: root}
	t.Cleanup(func() { cfg = oldCfg })
	allAccountsMu.Lock()
	previousAccounts := allAccounts
	allAccounts = nil
	allAccountsMu.Unlock()
	t.Cleanup(func() {
		allAccountsMu.Lock()
		allAccounts = previousAccounts
		allAccountsMu.Unlock()
	})
	path := accountsFilePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("mkdir accounts dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`[{"email":"persisted@example.com","status":"done"}]`), 0600); err != nil {
		t.Fatalf("write accounts file: %v", err)
	}
	restore := replaceDuneBatchManagerForTest(nil)
	defer restore()
	router := gin.New()
	api := router.Group("/api")
	registerDuneBatchRoutes(api)

	// When
	req := httptest.NewRequest(http.MethodGet, "/api/dune/batch/accounts", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	// Then
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Accounts []dunetools.Account `json:"accounts"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Accounts) != 1 || body.Accounts[0].Email != "persisted@example.com" {
		t.Fatalf("accounts = %+v, want persisted account on first request", body.Accounts)
	}
}

type fakeDuneBatchBrowser struct{}

func (fakeDuneBatchBrowser) Register(ctx context.Context, account dunetools.Account) (dunetools.BrowserResult, error) {
	return dunetools.BrowserResult{OK: true}, nil
}

func (fakeDuneBatchBrowser) VerifyEmail(ctx context.Context, link string, account dunetools.Account) (dunetools.BrowserResult, error) {
	return dunetools.BrowserResult{OK: true}, nil
}

func (fakeDuneBatchBrowser) LoginAndExtract(ctx context.Context, account dunetools.Account) (dunetools.BrowserResult, error) {
	return dunetools.BrowserResult{OK: true, Cookie: "auth-user=u", Authorization: "Bearer id", AccessToken: "access"}, nil
}

type fakeDuneBatchMailbox struct{}

func (fakeDuneBatchMailbox) WaitForVerificationLink(ctx context.Context, cfg dunetools.MailConfig, accountEmail string, since time.Time) (string, error) {
	return "https://dune.com/verify-email?token=abc", nil
}

type fakeDuneBatchVerifier struct{}

func (fakeDuneBatchVerifier) VerifyEmailLink(ctx context.Context, link string) error {
	return nil
}
