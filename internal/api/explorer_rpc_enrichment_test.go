package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/etl/backend/internal/explorer"
)

const enrichmentTxHash = "0xc0ba1b9fab2a7594125bf0079ac595c6f4f251a9396f07c59867f7b7594547fe"

type explorerRPCFake struct{}

func (explorerRPCFake) Call(_ context.Context, _ string, method string, _ any) (json.RawMessage, string, error) {
	switch method {
	case "eth_getTransactionByHash":
		return json.RawMessage(`{"blockHash":"0xcbf8f6197505076d4bd297a7296c91de445525215ef7f38a0a87e54bc5a1b26b","from":"0xff611b9424989381936f47eb781dab722904cdfb","to":"0x4c040ec7388b0484303ab5f7f1607a146b39d3ab","gas":"0x4c4b40","gasPrice":"0x3b9aca00","input":"0xc6d716f200000000000000000000000055d398326f99059ff775485246999027b3197955","nonce":"0xc","transactionIndex":"0x12","type":"0x0","value":"0x0"}`), "fake", nil
	case "eth_getTransactionReceipt":
		return json.RawMessage(`{"blockNumber":"0x6b17648","effectiveGasPrice":"0x3b9aca00","gasUsed":"0xce69","status":"0x1","transactionIndex":"0x12","logs":[{"address":"0x55d398326f99059ff775485246999027b3197955","topics":["0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef","0x0000000000000000000000004c040ec7388b0484303ab5f7f1607a146b39d3ab","0x000000000000000000000000befa868f1659cca5fde9018ec2b3b45839f5b15d"],"data":"0x000000000000000000000000000000000000000000000480d92cb96f2cbb7059","blockNumber":"0x6b17648","blockTimestamp":"0x6a665716","transactionHash":"` + enrichmentTxHash + `","transactionIndex":"0x12","logIndex":"0x6b","removed":false}]}`), "fake", nil
	default:
		return nil, "fake", fmt.Errorf("unexpected method %s", method)
	}
}

func TestEnrichExplorerTransactionFromRPCFillsCSVGaps(t *testing.T) {
	item := explorer.TransactionDetail{TransactionHash: enrichmentTxHash, BlockNumber: 112293448, SourceProvider: "csv"}
	if err := enrichExplorerTransactionFromRPC(context.Background(), explorerRPCFake{}, "bsc", &item); err != nil {
		t.Fatal(err)
	}
	if item.GasUsed != 52841 || item.GasLimit != 5000000 || item.TransactionIndex != 18 {
		t.Fatalf("gas/index not enriched: %+v", item)
	}
	if item.MethodID != "0xc6d716f2" || item.TransactionType != "Contract Call" || item.TransactionFeeNative != "0.000052841" {
		t.Fatalf("method/fee not enriched: %+v", item)
	}
	if item.Input == "" || item.BlockHash == "" || item.SourceProvider != "csv+rpc_receipt" {
		t.Fatalf("transaction evidence not enriched: %+v", item)
	}
}

func TestRawLogsFromRPCReceiptDecodesTransfer(t *testing.T) {
	page, err := rawLogsFromRPCReceipt(context.Background(), explorerRPCFake{}, "bsc", 56, enrichmentTxHash, 500)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalCount != 1 || page.Count != 1 || len(page.Items) != 1 {
		t.Fatalf("page=%+v", page)
	}
	item := page.Items[0]
	if item.LogIndex != 107 || item.TransactionIndex != 18 || item.EventName != "Transfer" || item.EventSignature != "Transfer(address,address,uint256)" {
		t.Fatalf("decoded log=%+v", item)
	}
	var decoded map[string]any
	if json.Unmarshal(item.Decoded, &decoded) != nil || decoded["from"] != "0x4c040ec7388b0484303ab5f7f1607a146b39d3ab" || decoded["to"] != "0xbefa868f1659cca5fde9018ec2b3b45839f5b15d" {
		t.Fatalf("decoded fields=%s", item.Decoded)
	}
	if item.Provenance.SourceType != "RPC_RECEIPT_LOG" || item.BlockTime.IsZero() {
		t.Fatalf("provenance/time=%+v", item)
	}
}
