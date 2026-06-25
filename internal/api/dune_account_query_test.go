package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/etl/backend/internal/config"
	"github.com/etl/backend/internal/dunetools"
	"github.com/gin-gonic/gin"
)

func TestHandleDuneSQLQueryUsesSelectedAccountLoginForWebQuery(t *testing.T) {
	// Given
	gin.SetMode(gin.TestMode)
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("DUNE_API_KEY", "")
	oldCfg := cfg
	cfg = &config.Config{RootDir: t.TempDir()}
	t.Cleanup(func() { cfg = oldCfg })

	allAccountsMu.Lock()
	previousAccounts := allAccounts
	allAccounts = []dunetools.Account{{
		Email:    "ready@example.com",
		Username: "ready",
		Password: "secret",
		Status:   dunetools.AccountStatusDone,
	}}
	allAccountsMu.Unlock()
	t.Cleanup(func() {
		allAccountsMu.Lock()
		allAccounts = previousAccounts
		allAccountsMu.Unlock()
	})

	var loginEmail string
	prevLogin := SetDuneAccountLoginForTest(func(ctx context.Context, account dunetools.Account) (duneStoredAuth, error) {
		loginEmail = account.Email
		return duneStoredAuth{
			Cookie:        "csrf=test; auth-id-token=id-token",
			Authorization: "Bearer id-token",
			AccessToken:   "access-token",
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
			if r.Header.Get("Authorization") != "Bearer id-token" {
				t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("X-Dune-Access-Token") != "access-token" {
				t.Fatalf("access token header = %q", r.Header.Get("X-Dune-Access-Token"))
			}
			if r.Header.Get("Cookie") != "csrf=test; auth-id-token=id-token" {
				t.Fatalf("cookie header = %q", r.Header.Get("Cookie"))
			}
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode graphql request: %v", err)
			}
			switch body["operationName"] {
			case "CreateQuery":
				sawCreate = true
				variables := body["variables"].(map[string]interface{})
				query := variables["query"].(map[string]interface{})
				if query["query"] != "select * from dune.example limit 10" || query["teamId"] != float64(55465) {
					t.Fatalf("create query payload = %#v", query)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"createQuery":{"id":7731663,"__typename":"QueryModel"}}}`))
			case "ExecuteQuery":
				sawExecute = true
				variables := body["variables"].(map[string]interface{})
				executor := variables["executor"].(map[string]interface{})
				if variables["query_id"] != float64(7731663) || executor["id"] != float64(55465) {
					t.Fatalf("execute variables = %#v", variables)
				}
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
		"account_email":"ready@example.com",
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
	if loginEmail != "ready@example.com" {
		t.Fatalf("login account = %q", loginEmail)
	}
	var response map[string]interface{}
	if err := json.Unmarshal(resp.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["query_id"] != float64(7731663) {
		t.Fatalf("query_id = %v", response["query_id"])
	}
	if response["execution_id"] != "web-exec" {
		t.Fatalf("execution_id = %v", response["execution_id"])
	}
	rows, ok := response["rows"].([]interface{})
	if !ok || len(rows) != 1 {
		t.Fatalf("rows = %#v", response["rows"])
	}
	if !sawCreate || !sawExecute || !sawPublicExecution {
		t.Fatalf("expected create/execute/public requests, saw create=%v execute=%v public=%v", sawCreate, sawExecute, sawPublicExecution)
	}
}

func TestHandleDuneSQLQueryUsesSavedAccountAuthBeforeLogin(t *testing.T) {
	// Given
	gin.SetMode(gin.TestMode)
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("DUNE_API_KEY", "")
	oldCfg := cfg
	cfg = &config.Config{RootDir: t.TempDir()}
	t.Cleanup(func() { cfg = oldCfg })

	allAccountsMu.Lock()
	previousAccounts := allAccounts
	allAccounts = []dunetools.Account{{
		Email:         "saved@example.com",
		Username:      "saved",
		Password:      "secret",
		Status:        dunetools.AccountStatusDone,
		Cookie:        "csrf=saved; auth-id-token=saved-id",
		Authorization: "Bearer saved-id",
		AccessToken:   "saved-access",
		TeamID:        55465,
	}}
	allAccountsMu.Unlock()
	t.Cleanup(func() {
		allAccountsMu.Lock()
		allAccounts = previousAccounts
		allAccountsMu.Unlock()
	})

	var loginCalled bool
	prevLogin := SetDuneAccountLoginForTest(func(ctx context.Context, account dunetools.Account) (duneStoredAuth, error) {
		loginCalled = true
		return duneStoredAuth{}, nil
	})
	t.Cleanup(func() { SetDuneAccountLoginForTest(prevLogin) })

	var sawCreate bool
	var sawExecute bool
	var sawPublicExecution bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/public/graphql":
			if r.Header.Get("Authorization") != "Bearer saved-id" {
				t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("X-Dune-Access-Token") != "saved-access" {
				t.Fatalf("access token header = %q", r.Header.Get("X-Dune-Access-Token"))
			}
			if r.Header.Get("Cookie") != "csrf=saved; auth-id-token=saved-id" {
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
		"account_email":"saved@example.com",
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
	if loginCalled {
		t.Fatal("saved account auth was present, but the handler still called the background login flow")
	}
	if !sawCreate || !sawExecute || !sawPublicExecution {
		t.Fatalf("expected create/execute/public requests, saw create=%v execute=%v public=%v", sawCreate, sawExecute, sawPublicExecution)
	}
}
