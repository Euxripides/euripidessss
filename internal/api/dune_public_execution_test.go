package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSignDunePublicExecutionMatchesCapturedSample(t *testing.T) {
	body := dunePublicExecutionRequest{
		ExecutionID: "01KV3YAC0P8P390TWGVZ9N8KW9",
		QueryID:     7722695,
		Pagination:  dunePublicPagination{Limit: 25, Offset: 35800},
		Timestamp:   1781494014060,
	}

	if got := signDunePublicExecution(body); got != "kGlwMqL6xXl0ostUU1ptLml5C2rKEoBF_qTZ8bDl3JY" {
		t.Fatalf("signature = %s", got)
	}
}

func TestHandleDuneSQLQueryUsesPublicExecutionPreview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEEPSEEK_API_KEY", "")

	var sawPublicExecution bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sql/execute":
			if r.Header.Get("X-Dune-Api-Key") != "test-key" {
				t.Fatalf("missing Dune API key header")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"execution_id":"exec-public","state":"QUERY_STATE_PENDING"}`))
		case "/execution/exec-public/status":
			if r.Header.Get("X-Dune-Api-Key") != "test-key" {
				t.Fatalf("missing Dune API key header")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"execution_id":"exec-public","state":"QUERY_STATE_COMPLETED","is_execution_finished":true}`))
		case "/public/execution":
			sawPublicExecution = true
			if r.Header.Get("Cookie") != "csrf=test; auth-id-token=token" {
				t.Fatalf("cookie header = %q", r.Header.Get("Cookie"))
			}
			var request dunePublicExecutionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode public request: %v", err)
			}
			if request.ExecutionID != "exec-public" || request.QueryID != 7731663 {
				t.Fatalf("public request ids = %s/%d", request.ExecutionID, request.QueryID)
			}
			if request.Pagination.Offset != 0 || request.Pagination.Limit != 50 {
				t.Fatalf("public pagination = %+v", request.Pagination)
			}
			if request.Signature == "" || request.Signature != signDunePublicExecution(request) {
				t.Fatalf("invalid signature = %q", request.Signature)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"execution_succeeded":{"execution_id":"exec-public","columns":["address","amount"],"data":[{"address":"0x1","amount":42}],"total_row_count":120}}`))
		default:
			t.Fatalf("unexpected Dune API path: %s", r.URL.String())
		}
	}))
	defer server.Close()
	restoreDuneTestServer(server)
	defer restoreDuneTestServer(nil)

	r := gin.New()
	r.POST("/api/dune/query", HandleDuneSQLQuery)
	req := httptest.NewRequest(http.MethodPost, "/api/dune/query", strings.NewReader(`{
		"sql":"select * from dune.example",
		"api_key":"test-key",
		"cookie":"csrf=test; auth-id-token=token",
		"query_id":7731663,
		"limit":50,
		"timeout_seconds":30,
		"poll_interval_seconds":1
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !sawPublicExecution {
		t.Fatalf("expected public execution preview request")
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["query_id"] != float64(7731663) {
		t.Fatalf("query_id = %v", body["query_id"])
	}
	if body["total_row_count"] != float64(120) {
		t.Fatalf("total_row_count = %v", body["total_row_count"])
	}
	rows, ok := body["rows"].([]interface{})
	if !ok || len(rows) != 1 {
		t.Fatalf("rows = %#v", body["rows"])
	}
}
