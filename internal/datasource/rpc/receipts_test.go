package rpc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/normalize"
)

func TestReceiptClientProbesSchemaAndNormalizes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var calls []rpcRequest
		if err := json.NewDecoder(request.Body).Decode(&calls); err != nil {
			t.Fatal(err)
		}
		responses := make([]map[string]any, 0, len(calls))
		for _, call := range calls {
			result := any("0x38")
			if call.Method == "eth_getTransactionReceipt" {
				result = map[string]any{
					"transactionHash":   call.Params[0],
					"status":            "0x1",
					"gasUsed":           "0x5208",
					"effectiveGasPrice": "0x3b9aca00",
					"contractAddress":   "0x2222222222222222222222222222222222222222",
					"logs":              []any{map[string]any{"logIndex": "0x0"}},
				}
			}
			responses = append(responses, map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result})
		}
		_ = json.NewEncoder(writer).Encode(responses)
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	network, _ := chain.Resolve("bsc")
	hash := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := client.Probe(context.Background(), network, hash); err != nil {
		t.Fatal(err)
	}
	receipts, err := client.Receipts(context.Background(), network, []string{hash})
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].ChainID != 56 || receipts[0].Status != 1 || receipts[0].LogsCount != 1 {
		t.Fatalf("unexpected receipt: %+v", receipts)
	}
}

func TestTokenMetadataPreservesUnknownFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var calls []rpcRequest
		if err := json.NewDecoder(request.Body).Decode(&calls); err != nil {
			t.Fatal(err)
		}
		responses := make([]map[string]any, 0, len(calls))
		for _, call := range calls {
			result := any("0x38")
			if call.Method == "eth_call" {
				data := call.Params[0].(map[string]any)["data"]
				switch data {
				case nameSelector:
					result = abiStringTest("USD Coin")
				case symbolSelector:
					result = abiStringTest("USDC")
				case decimalsSelector:
					result = "0x12"
				case totalSupplySelector:
					result = "0x3b9aca00"
				}
			}
			responses = append(responses, map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result})
		}
		_ = json.NewEncoder(writer).Encode(responses)
	}))
	defer server.Close()
	client, _ := New(server.URL, server.Client())
	network, _ := chain.Resolve("bsc")
	items, err := client.TokenMetadata(context.Background(), network, map[string]string{
		"0x1111111111111111111111111111111111111111": "BEP20",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "USD Coin" || items[0].Symbol != "USDC" ||
		items[0].Decimals == nil || *items[0].Decimals != 18 || items[0].TotalSupply != "1000000000" {
		t.Fatalf("unexpected metadata: %+v", items)
	}
}

func TestBalanceSnapshotsAndAddressTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var calls []rpcRequest
		if err := json.NewDecoder(request.Body).Decode(&calls); err != nil {
			t.Fatal(err)
		}
		responses := make([]map[string]any, 0, len(calls))
		for _, call := range calls {
			result := any("0x38")
			switch call.Method {
			case "eth_getBalance":
				result = "0xde0b6b3a7640000"
			case "eth_call":
				result = "0x0f4240"
			case "eth_getCode":
				if call.Params[0] == "0x2222222222222222222222222222222222222222" {
					result = "0x6000"
				} else {
					result = "0x"
				}
			}
			responses = append(responses, map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result})
		}
		_ = json.NewEncoder(writer).Encode(responses)
	}))
	defer server.Close()

	client, _ := New(server.URL, server.Client())
	network, _ := chain.Resolve("bsc")
	decimals := uint8(6)
	address := "0x1111111111111111111111111111111111111111"
	snapshots, err := client.BalanceSnapshots(context.Background(), network, []string{address}, []normalize.TokenMetadata{{
		TokenAddress: "0x3333333333333333333333333333333333333333",
		Decimals:     &decimals,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 || snapshots[0].Balance != "1" || snapshots[1].Balance != "1" {
		t.Fatalf("unexpected balances: %+v", snapshots)
	}
	types, err := client.AddressTypes(context.Background(), network, []string{
		address, "0x2222222222222222222222222222222222222222",
	})
	if err != nil {
		t.Fatal(err)
	}
	if types[address] != "EOA" || types["0x2222222222222222222222222222222222222222"] != "CONTRACT" {
		t.Fatalf("unexpected address types: %+v", types)
	}
}

func abiStringTest(value string) string {
	data := []byte(value)
	buffer := make([]byte, 64+((len(data)+31)/32)*32)
	buffer[31] = 32
	new(big.Int).SetInt64(int64(len(data))).FillBytes(buffer[32:64])
	copy(buffer[64:], data)
	return "0x" + hex.EncodeToString(buffer)
}

func TestReceiptClientRejectsWrongChain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode([]map[string]any{{"jsonrpc": "2.0", "id": 1, "result": "0x1"}})
	}))
	defer server.Close()
	client, _ := New(server.URL, server.Client())
	network, _ := chain.Resolve("bsc")
	if err := client.Probe(context.Background(), network, ""); err == nil {
		t.Fatal("expected chain mismatch")
	}
}
