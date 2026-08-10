package sqd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/etl/backend/internal/chain"
)

func TestStreamLogsContinuesAndUsesAddressTopics(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/metadata"):
			fmt.Fprint(writer, `{"dataset":"binance-mainnet","real_time":true,"start_block":0}`)
		case strings.Contains(request.URL.Path, "/timestamps/"):
			if strings.Contains(request.URL.Path, "1785196800") {
				fmt.Fprint(writer, `{"block_number":100}`)
			} else {
				fmt.Fprint(writer, `{"block_number":103}`)
			}
		default:
			requests++
			if requests == 1 {
				fmt.Fprintln(writer, `{"header":{"number":100,"timestamp":1785196800},"logs":[{"address":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","topics":["`+TransferTopic+`","0x0000000000000000000000001111111111111111111111111111111111111111","0x0000000000000000000000002222222222222222222222222222222222222222"],"data":"0x01","logIndex":1,"transactionIndex":2,"transactionHash":"0xhash"}]}`)
			} else {
				fmt.Fprintln(writer, `{"header":{"number":102,"timestamp":1785283199}}`)
			}
		}
	}))
	defer server.Close()
	client := New(server.Client())
	client.portalRoot = server.URL
	network, _ := chain.Resolve("bsc")
	metadata, err := client.Metadata(context.Background(), network)
	if err != nil || metadata.Dataset != "binance-mainnet" {
		t.Fatalf("metadata: %+v err=%v", metadata, err)
	}
	blockRange, err := client.ResolveDateRange(context.Background(), network, "2026-07-28", "2026-07-28")
	if err != nil || blockRange.From != 100 || blockRange.To != 102 {
		t.Fatalf("range: %+v err=%v", blockRange, err)
	}
	var logs int
	err = client.StreamLogs(context.Background(), network, blockRange, []string{
		"0x1111111111111111111111111111111111111111",
	}, func(block Block) error {
		logs += len(block.Logs)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if logs != 1 || requests != 2 {
		t.Fatalf("unexpected stream: logs=%d requests=%d", logs, requests)
	}
}

func TestStreamContractLogsUsesEmitterAddressFilter(t *testing.T) {
	const pool = "0x703f1c0b4399a51704e798002281bf26d6f9c2e6"
	var captured streamRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(writer, `{"header":{"number":100,"timestamp":1785196800},"logs":[{"address":"`+pool+`","topics":["0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822"],"data":"0x","logIndex":1,"transactionIndex":2,"transactionHash":"0xhash"}]}`)
	}))
	defer server.Close()

	client := NewConfigured(server.Client(), server.URL, "")
	network, _ := chain.Resolve("bsc")
	var logs int
	if err := client.StreamContractLogs(context.Background(), network, BlockRange{From: 100, To: 100}, []string{strings.ToUpper(pool)}, func(block Block) error {
		logs += len(block.Logs)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if logs != 1 || len(captured.Logs) != 1 {
		t.Fatalf("unexpected contract log stream: logs=%d filters=%+v", logs, captured.Logs)
	}
	addresses, ok := captured.Logs[0]["address"].([]any)
	if !ok || len(addresses) != 1 || addresses[0] != pool {
		t.Fatalf("contract filter must use unpadded emitter address: %+v", captured.Logs[0])
	}
	if _, exists := captured.Logs[0]["topic1"]; exists {
		t.Fatalf("contract log filter must not use transfer participant topics: %+v", captured.Logs[0])
	}
}
