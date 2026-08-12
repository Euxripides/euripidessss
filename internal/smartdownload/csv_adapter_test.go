package smartdownload

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/etl/backend/internal/cryptodownload"
)

type fakeCSVCollector struct {
	data cryptodownload.ExportData
	err  error
}

func (f *fakeCSVCollector) CollectAddress(context.Context, cryptodownload.Config, string) (cryptodownload.ExportData, error) {
	return f.data, f.err
}
func (f *fakeCSVCollector) Close() error { return nil }

func configuredCSVAdapter(data cryptodownload.ExportData) *CSVAdapter {
	p := NewProductionCSVAdapter("config", "raw", func(_ context.Context, _ string, block uint64) (time.Time, error) {
		return time.Unix(1_700_000_000+int64(block), 0).UTC(), nil
	})
	p.loadConfig = func(_, raw string) (cryptodownload.Config, error) {
		if raw == "" {
			return cryptodownload.Config{}, errors.New("raw path is empty")
		}
		return cryptodownload.Config{}, nil
	}
	p.runtimeReady = func(string) error { return nil }
	p.newCollector = func(cryptodownload.Config) csvExportCollector { return &fakeCSVCollector{data: data} }
	return p
}

func TestCSVAdapterProductionAvailabilityIsFailClosed(t *testing.T) {
	if NewCSVAdapter().Available() {
		t.Fatal("zero-config CSV adapter must be unavailable")
	}
	p := configuredCSVAdapter(cryptodownload.ExportData{})
	if !p.AvailableForMode("bsc", DownloadModeAuto) {
		t.Fatal("configured BSC AUTO CSV adapter should be available")
	}
	if p.AvailableForMode("bsc", DownloadModeTurbo) || p.AvailableForChain("unknown") {
		t.Fatal("CSV must remain unavailable for turbo and unsupported chains")
	}
	probe, err := p.Probe(context.Background(), ProbeRequest{ChainKey: "bsc", Dataset: DatasetTransactions, FromBlock: 1, ToBlock: 2})
	if err != nil || probe.Confidence != 0 {
		t.Fatalf("CSV without a real count must not publish probe confidence: %+v err=%v", probe, err)
	}
}

func TestCSVAdapterExecutesTransactionRangeAndFiltersExactBlocks(t *testing.T) {
	address := "0x1111111111111111111111111111111111111111"
	data := cryptodownload.ExportData{
		Transactions: []map[string]any{
			{"height": "9", "txId": "0xoutside", "transactionTime": "1700000009000", "from": address, "to": "0x2222222222222222222222222222222222222222"},
			{"height": "10", "txId": "0xinside", "transactionTime": "1700000010000", "from": address, "to": "0x2222222222222222222222222222222222222222", "amount": "1", "state": "success"},
		},
		RawTransactions:   []map[string]string{{}, {}},
		CSVDownloadChecks: []cryptodownload.CSVDownloadCheck{{Kind: "transactions", Downloaded: 2, Status: "unknown"}},
	}
	got, err := configuredCSVAdapter(data).ExecuteRange(context.Background(), RangeRequest{
		DatasetJobID: "job-1", Mode: DownloadModeAuto, Address: address, Dataset: DatasetTransactions,
		ChainKey: "bsc", ChainID: 56, FromBlock: 10, ToBlock: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.CompletedTo != 12 || len(got.Records) != 1 || got.Records[0].BlockNumber != 10 || got.Records[0].TransactionHash != "0xinside" {
		t.Fatalf("unexpected provider result: %+v", got)
	}
	if got.Records[0].Payload["status"] != 1 {
		t.Fatalf("status was not normalized: %+v", got.Records[0].Payload)
	}
}

func TestCSVAdapterAcceptsTokenWithoutLogIndexWhenCountIsWithinTolerance(t *testing.T) {
	address := "0x1111111111111111111111111111111111111111"
	incomplete := configuredCSVAdapter(cryptodownload.ExportData{
		CSVDownloadChecks: []cryptodownload.CSVDownloadCheck{{Status: "incomplete", Downloaded: 1, ExpectedTotal: 2}},
	})
	request := RangeRequest{DatasetJobID: "job-2", Mode: DownloadModeAuto, Address: address,
		Dataset: DatasetTransactions, ChainKey: "bsc", ChainID: 56, FromBlock: 1, ToBlock: 2}
	if _, err := incomplete.ExecuteRange(context.Background(), request); err == nil {
		t.Fatal("incomplete CSV evidence must fail")
	}

	token := configuredCSVAdapter(cryptodownload.ExportData{
		TokenTransfers: []map[string]any{
			{"height": "1", "txId": "0xtoken", "transactionTime": "1700000001000", "from": address, "to": "0x2222222222222222222222222222222222222222", "amount": "1"},
			{"height": "1", "txId": "0xtoken", "transactionTime": "1700000001000", "from": address, "to": "0x3333333333333333333333333333333333333333", "amount": "2"},
		},
		CSVDownloadChecks: []cryptodownload.CSVDownloadCheck{{Status: "complete", Downloaded: 2, ExpectedTotal: 102}},
	})
	request.Dataset = DatasetTokenTransfers
	got, err := token.ExecuteRange(context.Background(), request)
	if err != nil {
		t.Fatalf("token CSV without log_index should use count tolerance: %v", err)
	}
	if len(got.Records) != 2 || got.Records[0].LogIndex != 0 || got.Records[1].LogIndex != 1 {
		t.Fatalf("token rows were not preserved with surrogate order: %+v", got.Records)
	}
}

func TestCSVAdapterRejectsTokenWhenCountDifferenceExceedsTolerance(t *testing.T) {
	address := "0x1111111111111111111111111111111111111111"
	token := configuredCSVAdapter(cryptodownload.ExportData{
		TokenTransfers: []map[string]any{{"height": "1", "txId": "0xtoken", "transactionTime": "1700000001000",
			"from": address, "to": "0x2222222222222222222222222222222222222222", "amount": "1"}},
		CSVDownloadChecks: []cryptodownload.CSVDownloadCheck{{Status: "unknown", Downloaded: 1, ExpectedTotal: 102}},
	})
	_, err := token.ExecuteRange(context.Background(), RangeRequest{DatasetJobID: "job-count", Mode: DownloadModeAuto,
		Address: address, Dataset: DatasetTokenTransfers, ChainKey: "bsc", ChainID: 56, FromBlock: 1, ToBlock: 2})
	if err == nil {
		t.Fatal("token CSV count difference above 100 must fail")
	}
}

func TestCSVAdapterRejectsUnsafeJobID(t *testing.T) {
	p := configuredCSVAdapter(cryptodownload.ExportData{CSVDownloadChecks: []cryptodownload.CSVDownloadCheck{{Status: "complete"}}})
	_, err := p.ExecuteRange(context.Background(), RangeRequest{DatasetJobID: "../escape", Mode: DownloadModeAuto,
		Address: "0x1111111111111111111111111111111111111111", Dataset: DatasetTransactions,
		ChainKey: "bsc", ChainID: 56, FromBlock: 1, ToBlock: 2})
	if err == nil {
		t.Fatal("unsafe job id must be rejected")
	}
}

func TestCSVTokenSkipsRPCStyleRawFieldValidation(t *testing.T) {
	record := Record{ChainID: 56, BlockNumber: 1,
		TransactionHash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Dataset:         DatasetTokenTransfers,
		Payload: map[string]any{
			"token_address":   "0x1111111111111111111111111111111111111111",
			"from_address":    "0x2222222222222222222222222222222222222222",
			"to_address":      "0x3333333333333333333333333333333333333333",
			"value_raw":       "0.6962182215432539",
			"source_provider": "csv",
		}}
	if !recordFieldsValidForProvider(record) {
		t.Fatal("OKLink CSV row must use its count-based quality contract")
	}
	record.Payload["source_provider"] = "rpc"
	if recordFieldsValidForProvider(record) {
		t.Fatal("RPC/SQD rows must retain the strict canonical raw-value contract")
	}
}
