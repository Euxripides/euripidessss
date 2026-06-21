package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
