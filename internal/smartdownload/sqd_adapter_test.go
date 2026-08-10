package smartdownload

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/etl/backend/internal/datasource/sqd"
)

func TestSQDAdapterDatasetLogsUsesContractEmitterSemantics(t *testing.T) {
	const pool = "0x703f1c0b4399a51704e798002281bf26d6f9c2e6"
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(writer, `{"header":{"number":100,"timestamp":1785196800},"logs":[{"address":"`+pool+`","topics":["0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822"],"data":"0x","logIndex":1,"transactionIndex":2,"transactionHash":"0xhash"}]}`)
	}))
	defer server.Close()

	adapter := NewSQDAdapter(sqd.NewConfigured(server.Client(), server.URL, ""))
	result, err := adapter.ExecuteRange(context.Background(), RangeRequest{
		Address: pool, Dataset: DatasetLogs, ChainKey: "bsc", FromBlock: 100, ToBlock: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 1 || result.Records[0].Payload["contract_address"] != pool {
		t.Fatalf("unexpected records: %+v", result.Records)
	}
	filters, ok := captured["logs"].([]any)
	if !ok || len(filters) != 1 {
		t.Fatalf("unexpected SQD log filters: %+v", captured["logs"])
	}
	filter, _ := filters[0].(map[string]any)
	if _, ok := filter["address"]; !ok {
		t.Fatalf("DatasetLogs must filter log emitter address: %+v", filter)
	}
}
