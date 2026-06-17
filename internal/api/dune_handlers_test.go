package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestHandleDuneSQLDownloadStreamsCSV(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var sawExecute bool
	var sawStatus bool
	var sawCSV bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Dune-Api-Key") != "test-key" {
			t.Fatalf("missing Dune API key header")
		}
		switch r.URL.Path {
		case "/sql/execute":
			sawExecute = true
			if r.Method != http.MethodPost {
				t.Fatalf("execute method = %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"execution_id":"exec-1","state":"QUERY_STATE_PENDING"}`))
		case "/execution/exec-1/status":
			sawStatus = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"execution_id":"exec-1","state":"QUERY_STATE_COMPLETED","is_execution_finished":true}`))
		case "/execution/exec-1/results/csv":
			sawCSV = true
			w.Header().Set("Content-Type", "text/csv")
			_, _ = w.Write([]byte("a,b\n1,2\n"))
		default:
			t.Fatalf("unexpected Dune API path: %s", r.URL.String())
		}
	}))
	defer server.Close()

	oldBaseURL := duneAPIBaseURL
	oldClient := duneHTTPClient
	duneAPIBaseURL = server.URL
	duneHTTPClient = server.Client()
	defer func() {
		duneAPIBaseURL = oldBaseURL
		duneHTTPClient = oldClient
	}()

	r := gin.New()
	r.POST("/api/dune/download", HandleDuneSQLDownload)
	req := httptest.NewRequest(http.MethodPost, "/api/dune/download", strings.NewReader(`{
		"sql":"select 1 as a",
		"api_key":"test-key",
		"performance":"small",
		"timeout_seconds":30,
		"poll_interval_seconds":1
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "a,b\n1,2\n" {
		t.Fatalf("csv body = %q", got)
	}
	if w.Header().Get("X-Dune-Execution-Id") != "exec-1" {
		t.Fatalf("missing execution id header")
	}
	if !sawExecute || !sawStatus || !sawCSV {
		t.Fatalf("expected execute/status/csv requests, saw execute=%v status=%v csv=%v", sawExecute, sawStatus, sawCSV)
	}
}

func TestHandleDuneSQLQueryRetriesAndReturnsRows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEEPSEEK_API_KEY", "")

	executeCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Dune-Api-Key") != "test-key" {
			t.Fatalf("missing Dune API key header")
		}
		switch r.URL.Path {
		case "/sql/execute":
			executeCount++
			if executeCount == 1 {
				http.Error(w, "temporary", http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"execution_id":"exec-query","state":"QUERY_STATE_PENDING"}`))
		case "/execution/exec-query/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"execution_id":"exec-query","state":"QUERY_STATE_COMPLETED","is_execution_finished":true}`))
		case "/execution/exec-query/results":
			if r.URL.Query().Get("offset") != "0" || r.URL.Query().Get("limit") != "50" {
				t.Fatalf("unexpected pagination query: %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"execution_id":"exec-query","state":"QUERY_STATE_COMPLETED","result":{"metadata":{"column_names":["block_time","amount_usd"],"column_types":["timestamp","double"],"row_count":1,"total_row_count":1},"rows":[{"block_time":"2026-01-01","amount_usd":12.5}]}}`))
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
		"sql":"select 1",
		"api_key":"test-key",
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
	if executeCount != 2 {
		t.Fatalf("execute count = %d", executeCount)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["execution_id"] != "exec-query" {
		t.Fatalf("execution_id = %v", body["execution_id"])
	}
	if body["total_row_count"] != float64(1) {
		t.Fatalf("total_row_count = %v", body["total_row_count"])
	}
}

func TestHandleDuneExportExcelReturnsWorkbook(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEEPSEEK_API_KEY", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/execution/exec-export/results" {
			t.Fatalf("unexpected Dune API path: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("offset") {
		case "0":
			_, _ = w.Write([]byte(`{"execution_id":"exec-export","state":"QUERY_STATE_COMPLETED","result":{"metadata":{"column_names":["tx_hash","amount"],"column_types":["varchar","double"],"row_count":2,"total_row_count":3},"rows":[{"tx_hash":"0x1","amount":1},{"tx_hash":"0x2","amount":2}]}}`))
		case "2":
			_, _ = w.Write([]byte(`{"execution_id":"exec-export","state":"QUERY_STATE_COMPLETED","result":{"metadata":{"column_names":["tx_hash","amount"],"column_types":["varchar","double"],"row_count":1,"total_row_count":3},"rows":[{"tx_hash":"0x3","amount":3}]}}`))
		default:
			t.Fatalf("unexpected offset: %s", r.URL.RawQuery)
		}
	}))
	defer server.Close()
	restoreDuneTestServer(server)
	defer restoreDuneTestServer(nil)

	r := gin.New()
	r.POST("/api/dune/export", HandleDuneExportExcel)
	req := httptest.NewRequest(http.MethodPost, "/api/dune/export", strings.NewReader(`{
		"execution_id":"exec-export",
		"api_key":"test-key",
		"scope":"all",
		"limit":2
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.HasPrefix(w.Body.String(), "PK") {
		t.Fatalf("expected xlsx zip payload")
	}
	if w.Header().Get("X-Dune-Export-Rows") != "3" {
		t.Fatalf("exported rows = %s", w.Header().Get("X-Dune-Export-Rows"))
	}
}

func restoreDuneTestServer(server *httptest.Server) {
	if server == nil {
		duneAPIBaseURL = "https://api.dune.com/api/v1"
		duneWebBaseURL = "https://dune.com"
		duneHTTPClient = &http.Client{Timeout: 60 * time.Second}
		return
	}
	duneAPIBaseURL = server.URL
	duneWebBaseURL = server.URL
	duneHTTPClient = server.Client()
}

func TestHandleDuneSQLDownloadRequiresAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DUNE_API_KEY", "")

	r := gin.New()
	r.POST("/api/dune/download", HandleDuneSQLDownload)
	req := httptest.NewRequest(http.MethodPost, "/api/dune/download", strings.NewReader(`{"sql":"select 1"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "DUNE_API_KEY") {
		t.Fatalf("expected missing key message, got %s", w.Body.String())
	}
}
