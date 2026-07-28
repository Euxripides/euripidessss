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

func TestHandleCryptoAddressClassifyMatchesOriginalEOAContractKinds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eoaAddress := "0x28c6c06298d514db089934071355e5743bf21d60"
	contractAddress := "0x55d398326f99059fF775485246999027B3197955"
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode rpc request: %v", err)
		}
		if payload.Method != "eth_getCode" {
			t.Fatalf("unexpected rpc method %s", payload.Method)
		}
		address, _ := payload.Params[0].(string)
		result := "0x"
		if strings.EqualFold(address, contractAddress) {
			result = "0x6080604052"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"` + result + `"}`))
	}))
	defer rpc.Close()

	router := gin.New()
	RegisterRoutes(router)
	request := map[string]any{
		"addresses": eoaAddress + "\n" + contractAddress,
		"chains":    []string{"BSC"},
		"rpc_nodes": []string{"BSC|" + rpc.URL},
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
	if len(payload.Items) != 2 {
		t.Fatalf("items=%d, want 2", len(payload.Items))
	}
	if payload.Items[0].Kind != "EOA" || payload.Items[0].Status != "OK" || payload.Items[0].RetryCount != 0 {
		t.Fatalf("EOA item=%+v", payload.Items[0])
	}
	if payload.Items[0].Address != "0x28C6c06298d514Db089934071355E5743bf21d60" {
		t.Fatalf("EOA address=%q, want original checksum form", payload.Items[0].Address)
	}
	if payload.Items[1].Kind != "CONTRACT" || payload.Items[1].Status != "OK" || payload.Items[1].RetryCount != 0 {
		t.Fatalf("contract item=%+v", payload.Items[1])
	}
	if strings.Contains(payload.Items[1].Candidates[0].Detail, "6080604052") {
		t.Fatalf("candidate detail leaked full contract code: %q", payload.Items[1].Candidates[0].Detail)
	}
}

func TestClassifyCryptoAddressUsesOriginalInvalidStatus(t *testing.T) {
	item := classifyCryptoAddress("not-an-address", map[string]struct{}{"BSC": {}})
	if item.Valid || item.Kind != "INVALID" || item.Status != "地址格式错误" || item.Error == "" {
		t.Fatalf("invalid item=%+v", item)
	}
}

func TestHandleCryptoAddressClassifyUsesOriginalRPCErrorStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid request"}}`, http.StatusBadRequest)
	}))
	defer failed.Close()

	router := gin.New()
	RegisterRoutes(router)
	request := map[string]any{
		"addresses": "0x28c6c06298d514db089934071355e5743bf21d60",
		"chains":    []string{"BSC"},
		"rpc_nodes": []string{"BSC|" + failed.URL},
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
	item := payload.Items[0]
	if item.Kind != "ERROR" || item.Status != "RPC失败" || item.RetryCount != cryptoAddressMaxRetries || item.Error == "" {
		t.Fatalf("error item=%+v", item)
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
	if candidate.Detail != "RPC 成功，类型=EOA" {
		t.Fatalf("detail=%q, want EOA evidence", candidate.Detail)
	}
	if payload.Items[0].Kind != "EOA" || payload.Items[0].Status != "OK（曾触发限速）" || payload.Items[0].RetryCount != 1 {
		t.Fatalf("item=%+v, want original retry semantics", payload.Items[0])
	}
}
