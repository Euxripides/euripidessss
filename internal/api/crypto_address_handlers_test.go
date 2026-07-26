package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandleCryptoAddressClassifyRecognizesCommonFormats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router)

	body := []byte(`{"addresses":"0x0000000000000000000000000000000000000001\nTQn9Y2khEsLJW1ChVWFMSMeRDow5KcbLSE\nbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080","verify_online":false}`)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/crypto/address-classify", bytes.NewReader(body)))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload cryptoAddressClassifyResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Summary.Valid != 3 || payload.Summary.Invalid != 0 {
		t.Fatalf("summary=%+v, want 3 valid", payload.Summary)
	}
	if payload.Items[0].Family != "EVM" || payload.Items[0].Candidates[0].Chain != "ARBITRUM" {
		t.Fatalf("evm item=%+v", payload.Items[0])
	}
	if payload.Items[1].Family != "TRON" || payload.Items[1].Candidates[0].Chain != "TRON" {
		t.Fatalf("tron item=%+v", payload.Items[1])
	}
	if payload.Items[2].Family != "Bitcoin" || payload.Items[2].Candidates[0].Chain != "BTC" {
		t.Fatalf("btc item=%+v", payload.Items[2])
	}
}

func TestClassifyCryptoAddressHonorsChainFilter(t *testing.T) {
	item := classifyCryptoAddress("0x0000000000000000000000000000000000000001", map[string]struct{}{"BSC": {}})
	if !item.Valid || len(item.Candidates) != 1 || item.Candidates[0].Chain != "BSC" {
		t.Fatalf("filtered item=%+v", item)
	}
}

func TestHandleCryptoAddressClassifySwitchesRPCNodeAfterQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"rate limit"}}`, http.StatusTooManyRequests)
	}))
	defer limited.Close()

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode rpc request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch payload.Method {
		case "eth_getBalance":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
		case "eth_getCode":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x"}`))
		default:
			t.Fatalf("unexpected rpc method %s", payload.Method)
		}
	}))
	defer healthy.Close()

	router := gin.New()
	RegisterRoutes(router)
	request := map[string]any{
		"addresses": "0x0000000000000000000000000000000000000001",
		"chains":    []string{"BSC"},
		"rpc_nodes": []string{"BSC|" + limited.URL, "BSC|" + healthy.URL},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/crypto/address-classify", bytes.NewReader(body)))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload cryptoAddressClassifyResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Items) != 1 || len(payload.Items[0].Candidates) != 1 {
		t.Fatalf("payload=%+v", payload)
	}
	candidate := payload.Items[0].Candidates[0]
	if candidate.Status != "verified" || candidate.Source != "rpc" {
		t.Fatalf("candidate=%+v, want rpc verified", candidate)
	}
	if !strings.Contains(candidate.Detail, "balance") {
		t.Fatalf("detail=%q, want balance evidence", candidate.Detail)
	}
}
