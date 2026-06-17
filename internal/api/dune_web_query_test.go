package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandleDuneSQLQueryUsesWebsiteUpdateExecuteAndPublicPreview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("DUNE_API_KEY", "")

	var sawUpdate bool
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
			case "UpdateQuery":
				sawUpdate = true
				variables := body["variables"].(map[string]any)
				query := variables["query"].(map[string]any)
				if query["query"] != "select * from dune.example limit 10" {
					t.Fatalf("updated sql = %v", query["query"])
				}
				if query["id"] != float64(7731663) || query["teamId"] != float64(55465) || query["datasetId"] != float64(11) || query["version"] != float64(12) {
					t.Fatalf("update query metadata = %#v", query)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"updateQuery":{"id":7731663,"__typename":"QueryModel"}}}`))
			case "ExecuteQuery":
				sawExecute = true
				variables := body["variables"].(map[string]any)
				executor := variables["executor"].(map[string]any)
				if variables["query_id"] != float64(7731663) || executor["id"] != float64(55465) || executor["type"] != "team" || variables["performance"] != "free" {
					t.Fatalf("execute variables = %#v", variables)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"executeQuery":{"job_id":"web-exec","__typename":"ExecutionMetadata"}}}`))
			default:
				t.Fatalf("unexpected operationName: %v", body["operationName"])
			}
		case "/public/execution":
			sawPublicExecution = true
			var request dunePublicExecutionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode public request: %v", err)
			}
			if request.ExecutionID != "web-exec" || request.QueryID != 7731663 {
				t.Fatalf("public ids = %s/%d", request.ExecutionID, request.QueryID)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"execution_succeeded":{"execution_id":"web-exec","columns":["address","amount"],"data":[{"address":"0x1","amount":7}],"total_row_count":1}}`))
		default:
			t.Fatalf("unexpected Dune web path: %s", r.URL.String())
		}
	}))
	defer server.Close()
	restoreDuneTestServer(server)
	defer restoreDuneTestServer(nil)

	r := gin.New()
	r.POST("/api/dune/query", HandleDuneSQLQuery)
	req := httptest.NewRequest(http.MethodPost, "/api/dune/query", strings.NewReader(`{
		"sql":"select * from dune.example limit 10",
		"cookie":"csrf=test; auth-id-token=id-token",
		"authorization":"Bearer id-token",
		"access_token":"access-token",
		"web_query":true,
		"query_id":7731663,
		"team_id":55465,
		"dataset_id":11,
		"query_version":12,
		"performance":"free",
		"limit":25,
		"timeout_seconds":30,
		"poll_interval_seconds":1
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !sawUpdate || !sawExecute || !sawPublicExecution {
		t.Fatalf("expected update/execute/public requests, saw update=%v execute=%v public=%v", sawUpdate, sawExecute, sawPublicExecution)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["execution_id"] != "web-exec" || body["query_id"] != float64(7731663) || body["total_row_count"] != float64(1) {
		t.Fatalf("response = %#v", body)
	}
}
