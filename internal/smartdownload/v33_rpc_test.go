package smartdownload

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

type v33RPCFake struct {
	normalCalls int
	turboCalls  int
	logCalls    int
	filters     []map[string]any
	logHandler  func(map[string]any) (json.RawMessage, error)
}

func (f *v33RPCFake) Call(_ context.Context, _, method string, params any) (json.RawMessage, string, error) {
	f.normalCalls++
	return f.respond(method, params)
}

func (f *v33RPCFake) CallTurbo(_ context.Context, _, method string, params any) (json.RawMessage, string, error) {
	f.turboCalls++
	return f.respond(method, params)
}

func (f *v33RPCFake) HasAnyConfigured(string) bool { return true }

func (f *v33RPCFake) respond(method string, params any) (json.RawMessage, string, error) {
	switch method {
	case "eth_getBlockByNumber":
		return json.RawMessage(`{"timestamp":"0x65920080"}`), "fake", nil
	case "eth_getLogs":
		f.logCalls++
		filter := params.([]any)[0].(map[string]any)
		f.filters = append(f.filters, filter)
		if f.logHandler == nil {
			return json.RawMessage(`[]`), "fake", nil
		}
		raw, err := f.logHandler(filter)
		return raw, "fake", err
	default:
		return nil, "fake", fmt.Errorf("unexpected method %s", method)
	}
}

func TestV33RPCGroupOneCallFor100AddressesAndDatasetFanout(t *testing.T) {
	addresses := make([]string, 0, 102)
	for i := 100; i >= 1; i-- {
		addresses = append(addresses, fmt.Sprintf("0x%040X", i))
	}
	addresses = append(addresses, addresses[0], "  "+addresses[len(addresses)-1]+"  ")

	first := fmt.Sprintf("0x%040x", 1)
	last := fmt.Sprintf("0x%040x", 100)
	fake := &v33RPCFake{logHandler: func(filter map[string]any) (json.RawMessage, error) {
		if _, filtered := filter["topics"]; filtered {
			return nil, fmt.Errorf("group scan unexpectedly filtered topics")
		}
		return json.RawMessage(fmt.Sprintf(`[
			{"address":%q,"topics":[%q,"0x000000000000000000000000aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","0x000000000000000000000000bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"],"data":"0x01","blockNumber":"0x64","transactionHash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","logIndex":"0x1"},
			{"address":%q,"topics":["0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822"],"data":"0x02","blockNumber":"0x64","transactionHash":"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","logIndex":"0x2"}
		]`, first, transferTopic, last)), nil
	}}

	adapter := NewRPCTransferAdapter(fake)
	results, err := adapter.ExecuteGroupRange(context.Background(), GroupRangeRequest{
		SharedWorkID: "shared-1", Mode: DownloadModeTurbo, Priority: 100,
		ChainKey: "bsc", ChainID: 56, Addresses: addresses,
		Datasets: []string{DatasetLogs, DatasetTokenTransfers}, FromBlock: 100, ToBlock: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.logCalls != 1 {
		t.Fatalf("eth_getLogs provider calls=%d want=1", fake.logCalls)
	}
	if fake.normalCalls != 0 || fake.turboCalls != 2 {
		t.Fatalf("Turbo context lost: normal=%d turbo=%d", fake.normalCalls, fake.turboCalls)
	}
	gotAddresses := fake.filters[0]["address"].([]string)
	if len(gotAddresses) != 100 || gotAddresses[0] != first || gotAddresses[99] != last {
		t.Fatalf("address filter was not lower-case, sorted, and deduplicated: count=%d first=%q last=%q", len(gotAddresses), gotAddresses[0], gotAddresses[len(gotAddresses)-1])
	}
	for i := 1; i < len(gotAddresses); i++ {
		if gotAddresses[i-1] >= gotAddresses[i] {
			t.Fatalf("address filter is not strictly sorted at %d: %q >= %q", i, gotAddresses[i-1], gotAddresses[i])
		}
	}
	if len(results[first][DatasetTokenTransfers].Records) != 1 || len(results[first][DatasetLogs].Records) != 1 {
		t.Fatalf("transfer/log fanout mismatch for first address: %#v", results[first])
	}
	if len(results[last][DatasetTokenTransfers].Records) != 0 || len(results[last][DatasetLogs].Records) != 1 {
		t.Fatalf("log-only fanout mismatch for last address: %#v", results[last])
	}
	if len(results) != 100 {
		t.Fatalf("address result count=%d want=100", len(results))
	}
}

func TestV33RPCGroupFailsClosedBeforeProviderIO(t *testing.T) {
	valid := "0x1111111111111111111111111111111111111111"
	overLimit := make([]string, 101)
	for i := range overLimit {
		overLimit[i] = fmt.Sprintf("0x%040x", i+1)
	}
	tests := []struct {
		name string
		req  GroupRangeRequest
	}{
		{name: "empty addresses", req: GroupRangeRequest{ChainKey: "bsc", Datasets: []string{DatasetLogs}, FromBlock: 1, ToBlock: 1}},
		{name: "invalid address", req: GroupRangeRequest{ChainKey: "bsc", Addresses: []string{"0x1234"}, Datasets: []string{DatasetLogs}, FromBlock: 1, ToBlock: 1}},
		{name: "address group too large", req: GroupRangeRequest{ChainKey: "bsc", Addresses: overLimit, Datasets: []string{DatasetLogs}, FromBlock: 1, ToBlock: 1}},
		{name: "empty datasets", req: GroupRangeRequest{ChainKey: "bsc", Addresses: []string{valid}, FromBlock: 1, ToBlock: 1}},
		{name: "unsupported dataset", req: GroupRangeRequest{ChainKey: "bsc", Addresses: []string{valid}, Datasets: []string{DatasetBalances}, FromBlock: 1, ToBlock: 1}},
		{name: "duplicate dataset", req: GroupRangeRequest{ChainKey: "bsc", Addresses: []string{valid}, Datasets: []string{DatasetLogs, DatasetLogs}, FromBlock: 1, ToBlock: 1}},
		{name: "reversed range", req: GroupRangeRequest{ChainKey: "bsc", Addresses: []string{valid}, Datasets: []string{DatasetLogs}, FromBlock: 2, ToBlock: 1}},
		{name: "chain id mismatch", req: GroupRangeRequest{ChainKey: "bsc", ChainID: 1, Addresses: []string{valid}, Datasets: []string{DatasetLogs}, FromBlock: 1, ToBlock: 1}},
		{name: "unsupported chain", req: GroupRangeRequest{ChainKey: "unknown", Addresses: []string{valid}, Datasets: []string{DatasetLogs}, FromBlock: 1, ToBlock: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &v33RPCFake{}
			if _, err := NewRPCTransferAdapter(fake).ExecuteGroupRange(context.Background(), tt.req); err == nil {
				t.Fatal("invalid group bundle unexpectedly succeeded")
			}
			if fake.normalCalls != 0 || fake.turboCalls != 0 || fake.logCalls != 0 {
				t.Fatalf("invalid group reached provider: normal=%d turbo=%d logs=%d", fake.normalCalls, fake.turboCalls, fake.logCalls)
			}
		})
	}
}

func TestV33RPCGroupRangeLimitAndResultLimitBisectBlocks(t *testing.T) {
	address := "0x1111111111111111111111111111111111111111"
	t.Run("provider range error", func(t *testing.T) {
		fake := &v33RPCFake{logHandler: func(filter map[string]any) (json.RawMessage, error) {
			from := parseV33HexBlock(t, filter["fromBlock"].(string))
			to := parseV33HexBlock(t, filter["toBlock"].(string))
			if from != to {
				return nil, fmt.Errorf("query returned more than provider limit")
			}
			return json.RawMessage(`[]`), nil
		}}
		_, err := NewRPCTransferAdapter(fake).ExecuteGroupRange(context.Background(), GroupRangeRequest{
			ChainKey: "bsc", Addresses: []string{address}, Datasets: []string{DatasetLogs}, FromBlock: 100, ToBlock: 101,
		})
		if err != nil {
			t.Fatal(err)
		}
		if fake.logCalls != 3 {
			t.Fatalf("range-limit bisection calls=%d want=3", fake.logCalls)
		}
	})

	t.Run("provider result saturation", func(t *testing.T) {
		saturated := json.RawMessage("[" + strings.TrimSuffix(strings.Repeat("{},", rpcLogResultLimit), ",") + "]")
		fake := &v33RPCFake{logHandler: func(filter map[string]any) (json.RawMessage, error) {
			from := parseV33HexBlock(t, filter["fromBlock"].(string))
			to := parseV33HexBlock(t, filter["toBlock"].(string))
			if from != to {
				return saturated, nil
			}
			return json.RawMessage(`[]`), nil
		}}
		_, err := NewRPCTransferAdapter(fake).ExecuteGroupRange(context.Background(), GroupRangeRequest{
			ChainKey: "bsc", Addresses: []string{address}, Datasets: []string{DatasetLogs}, FromBlock: 100, ToBlock: 101,
		})
		if err != nil {
			t.Fatal(err)
		}
		if fake.logCalls != 3 {
			t.Fatalf("result-limit bisection calls=%d want=3", fake.logCalls)
		}
	})
}

func TestV33RPCGroupCapabilityContractAndSingleAddressCompatibility(t *testing.T) {
	adapter := NewRPCTransferAdapter(&v33RPCFake{})
	if adapter.MaxAddressGroupSize(DatasetLogs) != 100 || adapter.MaxAddressGroupSize(DatasetTokenTransfers) != 100 {
		t.Fatal("RPC group size must be 100 for log-derived datasets")
	}
	if adapter.MaxAddressGroupSize(DatasetBalances) != 0 {
		t.Fatal("balances must not advertise grouped RPC support")
	}
	bundles := adapter.SupportedDatasetBundles()
	if len(bundles) != 3 || len(bundles[2]) != 2 {
		t.Fatalf("unexpected supported bundles: %#v", bundles)
	}

	address := "0x1111111111111111111111111111111111111111"
	fake := &v33RPCFake{logHandler: func(filter map[string]any) (json.RawMessage, error) {
		if len(filter["address"].([]string)) != 1 || filter["address"].([]string)[0] != address {
			return nil, fmt.Errorf("single-address filter changed: %#v", filter)
		}
		if topics, ok := filter["topics"].([]string); !ok || len(topics) != 1 || topics[0] != transferTopic {
			return nil, fmt.Errorf("single-address transfer topic filter changed: %#v", filter)
		}
		return json.RawMessage(`[]`), nil
	}}
	result, err := NewRPCTransferAdapter(fake).ExecuteRange(context.Background(), RangeRequest{
		ChainKey: "bsc", Address: address, Dataset: DatasetTokenTransfers, FromBlock: 100, ToBlock: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CompletedTo != 100 || fake.logCalls != 1 {
		t.Fatalf("single-address ExecuteRange compatibility failed: result=%#v calls=%d", result, fake.logCalls)
	}
}

func parseV33HexBlock(t *testing.T, value string) uint64 {
	t.Helper()
	block, err := strconv.ParseUint(strings.TrimPrefix(value, "0x"), 16, 64)
	if err != nil {
		t.Fatalf("parse block %q: %v", value, err)
	}
	return block
}
