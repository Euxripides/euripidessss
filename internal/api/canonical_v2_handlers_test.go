package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/etl/backend/internal/canonicalapi"
	"github.com/etl/backend/internal/eventdecoder"
	"github.com/gin-gonic/gin"
)

type abiRegistryQueryFake struct {
	calls int
}

func (f *abiRegistryQueryFake) QueryJSON(context.Context, string) ([]map[string]any, error) {
	f.calls++
	return []map[string]any{{
		"abi_json": `[{"type":"event","name":"Swap","inputs":[{"name":"sender","type":"address","indexed":true}]}]`,
		"source":   "LOCAL", "is_verified": false,
	}}, nil
}

func TestClickHouseABIRegistryCachesTopicLookup(t *testing.T) {
	fake := &abiRegistryQueryFake{}
	registry := &clickHouseABIRegistry{client: fake}
	query := eventdecoder.Query{
		ChainID: 56, Contract: "0x1111111111111111111111111111111111111111",
		Topic0: eventdecoder.Topic0("Swap(address)"),
	}
	for i := 0; i < 2; i++ {
		definitions, err := registry.LookupEvent(context.Background(), query)
		if err != nil || len(definitions) != 1 {
			t.Fatalf("lookup %d: definitions=%d err=%v", i, len(definitions), err)
		}
	}
	if fake.calls != 1 {
		t.Fatalf("ClickHouse ABI lookup calls=%d want=1", fake.calls)
	}
}

type emptyCanonicalQuery struct{ calls int }

func (f *emptyCanonicalQuery) QueryJSON(context.Context, string) ([]map[string]any, error) {
	f.calls++
	return []map[string]any{}, nil
}

func TestCanonicalTxCollectionRoutesValidateAndPreserveEmptyCollections(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := canonicalV2
	fake := &emptyCanonicalQuery{}
	canonicalV2 = canonicalapi.NewRepository(fake)
	t.Cleanup(func() { canonicalV2 = original })
	router := gin.New()
	registerCanonicalV2Routes(router.Group("/api"))
	hash := "0x" + strings.Repeat("a", 64)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/explorer/bsc/tx/invalid/logs", nil))
	if response.Code != http.StatusBadRequest || fake.calls != 0 {
		t.Fatalf("invalid hash: status=%d calls=%d body=%s", response.Code, fake.calls, response.Body.String())
	}
	for _, path := range []string{
		"/api/v2/explorer/999/tx/" + hash + "/logs",
		"/api/v2/explorer/bsc/tx/" + hash + "/logs?limit=501",
	} {
		response = httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest || fake.calls != 0 {
			t.Fatalf("invalid scope %s: status=%d calls=%d body=%s", path, response.Code, fake.calls, response.Body.String())
		}
	}

	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/explorer/bsc/tx/"+hash+"/logs?cursor=invalid", nil))
	if response.Code != http.StatusBadRequest || fake.calls != 0 {
		t.Fatalf("invalid cursor: status=%d calls=%d body=%s", response.Code, fake.calls, response.Body.String())
	}

	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/explorer/bsc/tx/"+hash+"/logs", nil))
	if response.Code != http.StatusOK || fake.calls != 1 {
		t.Fatalf("empty logs: status=%d calls=%d body=%s", response.Code, fake.calls, response.Body.String())
	}
	var page struct {
		Count      int    `json:"count"`
		TotalCount uint64 `json:"total_count"`
		Items      []any  `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || page.Count != 0 || page.TotalCount != 0 || page.Items == nil {
		t.Fatalf("empty payload=%s err=%v decoded=%+v", response.Body.String(), err, page)
	}
}
