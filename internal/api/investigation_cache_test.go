package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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

