package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/etl/backend/internal/config"
	invcache "github.com/etl/backend/internal/investigation/cache"
	"github.com/etl/backend/internal/investigation/prefetch"
)

func TestPrefetchEndpointsUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api")
	registerInvestigationCacheRoutes(api)
	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/prefetch/stats", ""},
		{http.MethodGet, "/api/investigations/inv-1/prefetch", ""},
		{http.MethodPost, "/api/investigations/inv-1/prefetch/pin",
			`{"chain_key":"bsc","address":"0x8894e0a0c962cb723c1976a4421c95949be2d4e3","from_block":1,"to_block":2}`},
		{http.MethodPost, "/api/investigations/inv-1/prefetch/upgrade",
			`{"chain_key":"bsc","address":"0x8894e0a0c962cb723c1976a4421c95949be2d4e3"}`},
	}
	for _, tc := range cases {
		var req *http.Request
		if tc.body != "" {
			req = httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(tc.method, tc.path, nil)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s 应 503，实际 %d: %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestGraphExpandInvalidAddress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api")
	registerInvestigationCacheRoutes(api)
	body, _ := json.Marshal(map[string]any{"address": "not-an-address"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/graph/expand", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法地址应 400，实际 %d: %s", w.Code, w.Body.String())
	}
}

func TestPrefetchWritesRejectUnknownInvestigationWithoutSideEffects(t *testing.T) {
	root := t.TempDir()
	queue, err := prefetch.NewQueue(root)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := prefetch.NewBudgetStore(root, prefetch.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	feedback, err := prefetch.NewFeedback(root)
	if err != nil {
		t.Fatal(err)
	}
	managerConfig := prefetch.DefaultConfig()
	managerConfig.Interval = time.Hour

	oldManager := prefetchManager
	oldConfig := cfg
	oldCache := investigationCacheStore
	prefetchManager = prefetch.NewManager(
		queue,
		budget,
		feedback,
		nil,
		invcache.NewStore(root),
		prefetch.BatchCallbacks{},
		managerConfig,
	)
	cfg = &config.Config{RootDir: root}
	investigationCacheStore = nil
	t.Cleanup(func() {
		prefetchManager = oldManager
		cfg = oldConfig
		investigationCacheStore = oldCache
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api")
	registerInvestigationCacheRoutes(api)

	before := prefetchManager.Stats()
	cases := []struct {
		path string
		body string
	}{
		{
			path: "/api/investigations/missing-investigation/prefetch/pin",
			body: `{"chain_key":"bsc","address":"0x8894e0a0c962cb723c1976a4421c95949be2d4e3","from_block":1,"to_block":2}`,
		},
		{
			path: "/api/investigations/missing-investigation/prefetch/upgrade",
			body: `{"chain_key":"bsc","address":"0x8894e0a0c962cb723c1976a4421c95949be2d4e3"}`,
		},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(tc.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("POST %s 应 404，实际 %d: %s", tc.path, w.Code, w.Body.String())
		}
	}
	after := prefetchManager.Stats()
	if after.TotalJobs != before.TotalJobs || after.ActiveJobs != before.ActiveJobs || after.ReadyJobs != before.ReadyJobs {
		t.Fatalf("不存在调查的写请求不应改变队列: before=%+v after=%+v", before, after)
	}
	if after.InteractiveUpgrades != before.InteractiveUpgrades || after.Feedback != before.Feedback {
		t.Fatalf("不存在调查的写请求不应改变升级或反馈统计: before=%+v after=%+v", before, after)
	}
}

func TestNormalizeGraphDirection(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{"", "ALL", true},
		{"all", "ALL", true},
		{"ALL", "ALL", true},
		{"both", "ALL", true},
		{"BOTH", "ALL", true},
		{"upstream", "IN", true},
		{"UPSTREAM", "IN", true},
		{"in", "IN", true},
		{"downstream", "OUT", true},
		{"DOWNSTREAM", "OUT", true},
		{"out", "OUT", true},
		{" side ", "", false},
		{"diagonal", "", false},
	}
	for _, tc := range cases {
		got, err := normalizeGraphDirection(tc.input)
		if tc.ok {
			if err != nil {
				t.Fatalf("normalizeGraphDirection(%q) 不应报错: %v", tc.input, err)
			}
			if string(got) != tc.want {
				t.Fatalf("normalizeGraphDirection(%q) = %q，期望 %q", tc.input, got, tc.want)
			}
		} else if err == nil {
			t.Fatalf("normalizeGraphDirection(%q) 应报错，实际 %q", tc.input, got)
		}
	}
}

func TestGraphExpandRejectsUnknownDirection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api")
	registerInvestigationCacheRoutes(api)
	body, _ := json.Marshal(map[string]any{
		"address":   "0x8894e0a0c962cb723c1976a4421c95949be2d4e3",
		"direction": "sideways",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/graph/expand", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法方向应 400，实际 %d: %s", w.Code, w.Body.String())
	}
}
