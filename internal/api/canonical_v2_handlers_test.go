package api

import (
	"context"
	"testing"

	"github.com/etl/backend/internal/eventdecoder"
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
