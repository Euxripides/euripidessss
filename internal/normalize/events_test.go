package normalize

import (
	"fmt"
	"strings"
	"testing"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource/sqd"
)

func TestDecodeTransferStandards(t *testing.T) {
	network, _ := chain.Resolve("bsc")
	header := sqd.Header{Number: 100, Timestamp: 1700000000}
	from := "0x0000000000000000000000001111111111111111111111111111111111111111"
	to := "0x0000000000000000000000002222222222222222222222222222222222222222"
	tokens, nfts, err := DecodeTransferLog(network, header, sqd.Log{
		Address: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Topics:  []string{sqd.TransferTopic, from, to},
		Data:    "0x" + strings.Repeat("0", 63) + "a", TransactionHash: "0xhash",
	})
	if err != nil || len(tokens) != 1 || len(nfts) != 0 || tokens[0].AmountRaw != "10" || tokens[0].Standard != "BEP20" {
		t.Fatalf("token: %+v %+v err=%v", tokens, nfts, err)
	}
	tokens, nfts, err = DecodeTransferLog(network, header, sqd.Log{
		Address: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Topics:  []string{sqd.TransferTopic, from, to, "0x" + strings.Repeat("0", 63) + "7"},
		Data:    "0x", TransactionHash: "0xnft",
	})
	if err != nil || len(tokens) != 0 || len(nfts) != 1 || nfts[0].TokenID != "7" || nfts[0].Standard != "ERC721" {
		t.Fatalf("nft: %+v %+v err=%v", tokens, nfts, err)
	}
}

func TestDecodeERC1155Batch(t *testing.T) {
	network, _ := chain.Resolve("eth")
	word := func(value int) string { return fmt.Sprintf("%064x", value) }
	data := "0x" + word(64) + word(160) + word(2) + word(7) + word(8) + word(2) + word(3) + word(4)
	_, nfts, err := DecodeTransferLog(network, sqd.Header{Number: 1, Timestamp: 1}, sqd.Log{
		Address: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Topics: []string{
			sqd.TransferBatchTopic, "0x" + strings.Repeat("0", 64),
			"0x0000000000000000000000001111111111111111111111111111111111111111",
			"0x0000000000000000000000002222222222222222222222222222222222222222",
		},
		Data: data, TransactionHash: "0xhash",
	})
	if err != nil || len(nfts) != 2 || nfts[0].TokenID != "7" || nfts[1].Amount != "4" {
		t.Fatalf("batch: %+v err=%v", nfts, err)
	}
}

func TestNormalizeTraceSeparatesInternalValue(t *testing.T) {
	network, _ := chain.Resolve("bsc")
	value := "0xde0b6b3a7640000"
	trace, internal, err := NormalizeTrace(network, sqd.Header{Number: 10, Timestamp: 20}, sqd.Trace{
		TransactionIndex: 2, Type: "call",
		Action: sqd.TraceAction{
			From: "0x1111111111111111111111111111111111111111",
			To:   "0x2222222222222222222222222222222222222222", Value: &value,
		},
	}, "0xhash", 3)
	if err != nil || internal == nil || trace.ValueRaw != "1000000000000000000" || internal.TraceID != "10-2-3" {
		t.Fatalf("trace=%+v internal=%+v err=%v", trace, internal, err)
	}
}
