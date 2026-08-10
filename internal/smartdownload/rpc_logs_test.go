package smartdownload

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/etl/backend/internal/chain"
)

type turboPolicyRPCFake struct {
	normalCalls int
	turboCalls  int
}

func (f *turboPolicyRPCFake) Call(_ context.Context, _, _ string, _ any) (json.RawMessage, string, error) {
	f.normalCalls++
	return json.RawMessage(`"0x1"`), "normal", nil
}

func (f *turboPolicyRPCFake) CallTurbo(_ context.Context, _, _ string, _ any) (json.RawMessage, string, error) {
	f.turboCalls++
	return json.RawMessage(`"0x1"`), "turbo", nil
}

func (f *turboPolicyRPCFake) HasAnyConfigured(string) bool { return true }
func (f *turboPolicyRPCFake) HasConfigured(string) bool    { return false }

type priceRPCFake struct{}

func (priceRPCFake) Call(_ context.Context, _, method string, _ any) (json.RawMessage, string, error) {
	if method == "eth_getBlockByNumber" {
		return json.RawMessage(`{"timestamp":"0x65920080"}`), "fake", nil
	}
	return json.RawMessage(`[{"address":"0x1111111111111111111111111111111111111111","topics":["0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822"],"data":"0x01","blockNumber":"0x64","transactionHash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","logIndex":"0x1"}]`), "fake", nil
}

func TestRPCAdapterSupportsRawLogsWithBlockTime(t *testing.T) {
	adapter := NewRPCTransferAdapter(priceRPCFake{})
	if !adapter.Supports(DatasetLogs) {
		t.Fatal("logs dataset is not supported")
	}
	result, err := adapter.ExecuteRange(context.Background(), RangeRequest{Dataset: DatasetLogs, Address: "0x1111111111111111111111111111111111111111", ChainKey: "bsc", FromBlock: 100, ToBlock: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("records=%d", len(result.Records))
	}
	record := result.Records[0]
	if record.BlockTime == 0 || record.Dataset != DatasetLogs || record.Payload["contract_address"] != "0x1111111111111111111111111111111111111111" {
		t.Fatalf("unexpected record: %+v", record)
	}
}

func TestRPCAdapterUsesTurboPolicyForDisabledResources(t *testing.T) {
	fake := &turboPolicyRPCFake{}
	adapter := NewRPCTransferAdapter(fake)
	if adapter.AvailableForChain("bsc") {
		t.Fatal("normal mode unexpectedly sees an enabled endpoint")
	}
	if !adapter.AvailableForMode("bsc", DownloadModeTurbo) {
		t.Fatal("Turbo mode must see all configured endpoints")
	}
	if _, err := adapter.ExecuteRange(context.Background(), RangeRequest{
		Mode: DownloadModeTurbo, Dataset: DatasetBalances, ChainKey: "bsc",
		Address: "0x1111111111111111111111111111111111111111",
	}); err != nil {
		t.Fatal(err)
	}
	if fake.turboCalls != 1 || fake.normalCalls != 0 {
		t.Fatalf("unexpected RPC policy calls: turbo=%d normal=%d", fake.turboCalls, fake.normalCalls)
	}
}

type rangeLimitedRPCFake struct {
	calls int
}

func (f *rangeLimitedRPCFake) Call(_ context.Context, _, method string, params any) (json.RawMessage, string, error) {
	if method != "eth_getLogs" {
		return nil, "fake", fmt.Errorf("unexpected method %s", method)
	}
	f.calls++
	filter := params.([]any)[0].(map[string]any)
	from, _ := strconv.ParseUint(filter["fromBlock"].(string)[2:], 16, 64)
	to, _ := strconv.ParseUint(filter["toBlock"].(string)[2:], 16, 64)
	if to-from+1 > 50 {
		return nil, "fake", fmt.Errorf("eth_getLogs is limited to 0 - 50 blocks range")
	}
	return json.RawMessage(`[]`), "fake", nil
}

func TestRPCLogProbeSplitsProviderRangeLimit(t *testing.T) {
	fake := &rangeLimitedRPCFake{}
	result, err := NewRPCTransferAdapter(fake).Probe(context.Background(), ProbeRequest{
		Dataset: DatasetLogs, Address: "0x1111111111111111111111111111111111111111",
		ChainKey: "bsc", FromBlock: 100, ToBlock: 199,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Confidence == 0 {
		t.Fatal("range-limited RPC probe must remain valid after adaptive splitting")
	}
	if fake.calls != 3 {
		t.Fatalf("calls=%d want=3 (one rejected range plus two 50-block ranges)", fake.calls)
	}
}

type groupLogsRPCFake struct {
	logCalls  int
	addresses [][]string
}

func (f *groupLogsRPCFake) Call(_ context.Context, _, method string, params any) (json.RawMessage, string, error) {
	switch method {
	case "eth_getBlockByNumber":
		return json.RawMessage(`{"timestamp":"0x65920080"}`), "fake", nil
	case "eth_getLogs":
		f.logCalls++
		filter := params.([]any)[0].(map[string]any)
		addresses := append([]string(nil), filter["address"].([]string)...)
		f.addresses = append(f.addresses, addresses)
		if _, hasTopics := filter["topics"]; hasTopics {
			return nil, "fake", fmt.Errorf("group scan must not filter topics")
		}
		return json.RawMessage(`[
			{"address":"0x1111111111111111111111111111111111111111","topics":["0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef","0x000000000000000000000000aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","0x000000000000000000000000bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"],"data":"0x01","blockNumber":"0x64","transactionHash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","logIndex":"0x1"},
			{"address":"0x2222222222222222222222222222222222222222","topics":["0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822"],"data":"0x02","blockNumber":"0x64","transactionHash":"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","logIndex":"0x2"}
		]`), "fake", nil
	default:
		return nil, "fake", fmt.Errorf("unexpected method %s", method)
	}
}

func TestRPCGroupedLogScanUsesAddressArrayOnceAndFansOutDatasets(t *testing.T) {
	fake := &groupLogsRPCFake{}
	adapter := NewRPCTransferAdapter(fake)
	network, err := chain.Resolve("bsc")
	if err != nil {
		t.Fatal(err)
	}
	results, err := adapter.executeGroupedLogScan(context.Background(), network, []string{
		"0x1111111111111111111111111111111111111111",
		"0x2222222222222222222222222222222222222222",
	}, []string{DatasetTokenTransfers, DatasetLogs}, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if fake.logCalls != 1 {
		t.Fatalf("eth_getLogs calls=%d want=1", fake.logCalls)
	}
	if len(fake.addresses) != 1 || len(fake.addresses[0]) != 2 {
		t.Fatalf("unexpected address filters: %#v", fake.addresses)
	}
	first := results["0x1111111111111111111111111111111111111111"]
	second := results["0x2222222222222222222222222222222222222222"]
	if len(first[DatasetTokenTransfers].Records) != 1 || len(first[DatasetLogs].Records) != 1 {
		t.Fatalf("first fan-out mismatch: %#v", first)
	}
	if len(second[DatasetTokenTransfers].Records) != 0 || len(second[DatasetLogs].Records) != 1 {
		t.Fatalf("second fan-out mismatch: %#v", second)
	}
	if first[DatasetTokenTransfers].Records[0].Address != "0x1111111111111111111111111111111111111111" ||
		second[DatasetLogs].Records[0].Address != "0x2222222222222222222222222222222222222222" {
		t.Fatal("records were not fanned out by contract address")
	}
}

type groupRangeLimitedRPCFake struct {
	calls     int
	addresses [][]string
}

func (f *groupRangeLimitedRPCFake) Call(_ context.Context, _, method string, params any) (json.RawMessage, string, error) {
	if method != "eth_getLogs" {
		return nil, "fake", fmt.Errorf("unexpected method %s", method)
	}
	f.calls++
	filter := params.([]any)[0].(map[string]any)
	f.addresses = append(f.addresses, append([]string(nil), filter["address"].([]string)...))
	from, _ := strconv.ParseUint(filter["fromBlock"].(string)[2:], 16, 64)
	to, _ := strconv.ParseUint(filter["toBlock"].(string)[2:], 16, 64)
	if to-from+1 > 50 {
		return nil, "fake", fmt.Errorf("eth_getLogs is limited to 0 - 50 blocks range")
	}
	return json.RawMessage(`[]`), "fake", nil
}

func TestRPCGroupedLogScanSplitsRangeWithoutSplittingAddresses(t *testing.T) {
	fake := &groupRangeLimitedRPCFake{}
	addresses := []string{
		"0x1111111111111111111111111111111111111111",
		"0x2222222222222222222222222222222222222222",
	}
	logs, err := NewRPCTransferAdapter(fake).getAllLogsChunkForAddresses(context.Background(), "bsc", addresses, 100, 199)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 0 || fake.calls != 3 {
		t.Fatalf("logs=%d calls=%d want logs=0 calls=3", len(logs), fake.calls)
	}
	for call, got := range fake.addresses {
		if len(got) != len(addresses) || got[0] != addresses[0] || got[1] != addresses[1] {
			t.Fatalf("call %d split the address group: %#v", call+1, got)
		}
	}
}
