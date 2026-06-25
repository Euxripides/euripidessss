package api

import (
	"context"
	"encoding/base64"
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

func TestHandleDuneSQLQueryReloginsWhenSavedAccountAuthExpired(t *testing.T) {
	// Given
	gin.SetMode(gin.TestMode)
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("DUNE_API_KEY", "")
	oldCfg := cfg
	cfg = &config.Config{RootDir: t.TempDir()}
	t.Cleanup(func() { cfg = oldCfg })

	expiredToken := duneTestJWT(t, time.Now().Add(-time.Hour))
	freshToken := duneTestJWT(t, time.Now().Add(time.Hour))

	allAccountsMu.Lock()
	previousAccounts := allAccounts
	allAccounts = []dunetools.Account{{
		Email:         "expired@example.com",
		Username:      "expired",
		Password:      "secret",
		Status:        dunetools.AccountStatusDone,
		Cookie:        "csrf=old; auth-id-token=" + expiredToken,
		Authorization: "Bearer " + expiredToken,
		AccessToken:   "old-access",
		TeamID:        55465,
	}}
	allAccountsMu.Unlock()
	t.Cleanup(func() {
		allAccountsMu.Lock()
		allAccounts = previousAccounts
		allAccountsMu.Unlock()
	})

	var loginCalled int
	prevLogin := SetDuneAccountLoginForTest(func(ctx context.Context, account dunetools.Account) (duneStoredAuth, error) {
		loginCalled++
		if account.Email != "expired@example.com" {
			t.Fatalf("login account = %q", account.Email)
		}
		return duneStoredAuth{
			Cookie:        "csrf=fresh; auth-id-token=" + freshToken,
			Authorization: "Bearer " + freshToken,
			AccessToken:   "fresh-access",
			TeamID:        55465,
		}, nil
	})
	t.Cleanup(func() { SetDuneAccountLoginForTest(prevLogin) })

	var sawCreate bool
	var sawExecute bool
	var sawPublicExecution bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/public/graphql":
			if r.Header.Get("Authorization") != "Bearer "+freshToken {
				t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("X-Dune-Access-Token") != "fresh-access" {
				t.Fatalf("access token header = %q", r.Header.Get("X-Dune-Access-Token"))
			}
			if r.Header.Get("Cookie") != "csrf=fresh; auth-id-token="+freshToken {
				t.Fatalf("cookie header = %q", r.Header.Get("Cookie"))
			}
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode graphql request: %v", err)
			}
			switch body["operationName"] {
			case "CreateQuery":
				sawCreate = true
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"createQuery":{"id":7731663,"__typename":"QueryModel"}}}`))
			case "ExecuteQuery":
				sawExecute = true
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"executeQuery":{"job_id":"web-exec","__typename":"ExecutionMetadata"}}}`))
			default:
				t.Fatalf("unexpected operationName: %v", body["operationName"])
			}
		case "/public/execution":
			sawPublicExecution = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"execution_succeeded":{"execution_id":"web-exec","columns":["address"],"data":[{"address":"0x1"}],"total_row_count":1}}`))
		default:
			t.Fatalf("unexpected Dune path: %s", r.URL.String())
		}
	}))
	defer server.Close()
	restoreDuneTestServer(server)
	defer restoreDuneTestServer(nil)

	router := gin.New()
	router.POST("/api/dune/query", HandleDuneSQLQuery)

	// When
	req := httptest.NewRequest(http.MethodPost, "/api/dune/query", strings.NewReader(`{
		"sql":"select * from dune.example limit 10",
		"account_email":"expired@example.com",
		"performance":"free",
		"limit":25,
		"timeout_seconds":30,
		"poll_interval_seconds":1
	}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	// Then
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.Code, resp.Body.String())
	}
	if loginCalled != 1 {
		t.Fatalf("login calls = %d", loginCalled)
	}
	if !sawCreate || !sawExecute || !sawPublicExecution {
		t.Fatalf("expected create/execute/public requests, saw create=%v execute=%v public=%v", sawCreate, sawExecute, sawPublicExecution)
	}
}

func TestFindDuneQueryAccountLoadsPersistedAccountsOnFirstQuery(t *testing.T) {
	// Given
	oldCfg := cfg
	root := t.TempDir()
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

	duneBatchMu.Lock()
	previousManager := duneBatchManager
	duneBatchManager = nil
	duneBatchMu.Unlock()
	t.Cleanup(func() {
		duneBatchMu.Lock()
		duneBatchManager = previousManager
		duneBatchMu.Unlock()
	})

	path := accountsFilePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("mkdir accounts dir: %v", err)
	}
	data, err := json.Marshal([]dunetools.Account{{
		Email:    "persisted@example.com",
		Username: "persisted",
		Password: "secret",
		Status:   dunetools.AccountStatusDone,
	}})
	if err != nil {
		t.Fatalf("marshal account: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write accounts file: %v", err)
	}

	// When
	account, err := findDuneQueryAccount("persisted@example.com")

	// Then
	if err != nil {
		t.Fatalf("find account: %v", err)
	}
	if account.Email != "persisted@example.com" {
		t.Fatalf("account email = %q", account.Email)
	}
}

func duneTestJWT(t *testing.T, expiresAt time.Time) string {
	t.Helper()
	payload, err := json.Marshal(struct {
		Exp int64 `json:"exp"`
	}{Exp: expiresAt.Unix()})
	if err != nil {
		t.Fatalf("marshal jwt payload: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	body := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + body + ".sig"
}
