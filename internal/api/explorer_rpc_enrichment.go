package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/etl/backend/internal/canonicalapi"
	"github.com/etl/backend/internal/eventdecoder"
	"github.com/etl/backend/internal/explorer"
)

type explorerRPCClient interface {
	Call(context.Context, string, string, any) (json.RawMessage, string, error)
}

type explorerRPCTransaction struct {
	BlockHash        string `json:"blockHash"`
	From             string `json:"from"`
	To               string `json:"to"`
	Gas              string `json:"gas"`
	GasPrice         string `json:"gasPrice"`
	Input            string `json:"input"`
	Nonce            string `json:"nonce"`
	TransactionIndex string `json:"transactionIndex"`
	Type             string `json:"type"`
	Value            string `json:"value"`
}

type explorerRPCLog struct {
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	BlockNumber      string   `json:"blockNumber"`
	BlockTimestamp   string   `json:"blockTimestamp"`
	TransactionHash  string   `json:"transactionHash"`
	TransactionIndex string   `json:"transactionIndex"`
	LogIndex         string   `json:"logIndex"`
	Removed          bool     `json:"removed"`
}

type explorerRPCReceipt struct {
	BlockNumber       string           `json:"blockNumber"`
	ContractAddress   string           `json:"contractAddress"`
	EffectiveGasPrice string           `json:"effectiveGasPrice"`
	GasUsed           string           `json:"gasUsed"`
	Status            string           `json:"status"`
	TransactionIndex  string           `json:"transactionIndex"`
	Logs              []explorerRPCLog `json:"logs"`
}

func enrichExplorerTransactionFromRPC(ctx context.Context, client explorerRPCClient, chainKey string, item *explorer.TransactionDetail) error {
	if client == nil || item == nil {
		return errors.New("RPC enrichment is unavailable")
	}
	txRaw, _, err := client.Call(ctx, chainKey, "eth_getTransactionByHash", []any{item.TransactionHash})
	if err != nil {
		return err
	}
	receiptRaw, _, err := client.Call(ctx, chainKey, "eth_getTransactionReceipt", []any{item.TransactionHash})
	if err != nil {
		return err
	}
	var tx explorerRPCTransaction
	var receipt explorerRPCReceipt
	if json.Unmarshal(txRaw, &tx) != nil || json.Unmarshal(receiptRaw, &receipt) != nil {
		return errors.New("RPC transaction response is invalid")
	}
	gasLimit, err := rpcHexUint64(tx.Gas)
	if err != nil {
		return err
	}
	gasUsed, err := rpcHexUint64(receipt.GasUsed)
	if err != nil {
		return err
	}
	index, err := rpcHexUint64(firstNonEmptyRPC(receipt.TransactionIndex, tx.TransactionIndex))
	if err != nil || index > uint64(^uint32(0)) {
		return errors.New("RPC transaction index is invalid")
	}
	nonce, err := rpcHexUint64(tx.Nonce)
	if err != nil {
		return err
	}
	value, err := rpcHexBig(tx.Value)
	if err != nil {
		return err
	}
	effectivePrice, err := rpcHexBig(firstNonEmptyRPC(receipt.EffectiveGasPrice, tx.GasPrice))
	if err != nil {
		return err
	}
	fee := new(big.Int).Mul(new(big.Int).SetUint64(gasUsed), effectivePrice)
	item.BlockHash = strings.ToLower(tx.BlockHash)
	item.TransactionIndex = uint32(index)
	item.FromAddress = strings.ToLower(tx.From)
	item.ToAddress = strings.ToLower(tx.To)
	item.Nonce = nonce
	item.ValueRaw = value.String()
	item.ValueDecimal = formatRPCDecimal(value, 18)
	item.Input = strings.ToLower(tx.Input)
	item.GasLimit = gasLimit
	item.GasUsed = gasUsed
	item.TransactionFeeNative = formatRPCDecimal(fee, 18)
	item.TransactionType = "Native Transfer"
	if item.Input != "" && item.Input != "0x" {
		item.TransactionType = "Contract Call"
		if len(item.Input) >= 10 {
			item.MethodID = item.Input[:10]
		}
	}
	status, err := rpcHexUint64(receipt.Status)
	if err != nil {
		return err
	}
	if status == 1 {
		item.Status = "SUCCESS"
	} else {
		item.Status = "FAILED"
	}
	if receipt.ContractAddress != "" {
		item.ContractCreation = true
		item.CreatedContract = strings.ToLower(receipt.ContractAddress)
	}
	item.SourceProvider = firstNonEmptyRPC(item.SourceProvider, "unknown") + "+rpc_receipt"
	return nil
}

func rawLogsFromRPCReceipt(ctx context.Context, client explorerRPCClient, chainKey string, chainID uint32, txHash string, limit int) (canonicalapi.RawLogPage, error) {
	page := canonicalapi.RawLogPage{ChainID: chainID, TransactionHash: txHash, Items: []canonicalapi.CanonicalRawLog{}}
	if client == nil {
		return page, errors.New("RPC receipt is unavailable")
	}
	raw, source, err := client.Call(ctx, chainKey, "eth_getTransactionReceipt", []any{txHash})
	if err != nil {
		return page, err
	}
	var receipt explorerRPCReceipt
	if json.Unmarshal(raw, &receipt) != nil {
		return page, errors.New("RPC receipt response is invalid")
	}
	blockTime, err := rpcReceiptBlockTime(ctx, client, chainKey, receipt)
	if err != nil {
		return page, err
	}
	page.TotalCount = uint64(len(receipt.Logs))
	if limit <= 0 || limit > len(receipt.Logs) {
		limit = len(receipt.Logs)
	}
	decoder := eventdecoder.New(nil)
	for _, value := range receipt.Logs[:limit] {
		if len(value.Topics) == 0 || !canonicalAddressPattern.MatchString(strings.ToLower(value.Address)) {
			continue
		}
		logIndex, indexErr := rpcHexUint64(value.LogIndex)
		transactionIndex, txIndexErr := rpcHexUint64(value.TransactionIndex)
		blockNumber, blockErr := rpcHexUint64(firstNonEmptyRPC(value.BlockNumber, receipt.BlockNumber))
		if indexErr != nil || txIndexErr != nil || blockErr != nil || logIndex > uint64(^uint32(0)) || transactionIndex > uint64(^uint32(0)) {
			return page, errors.New("RPC log index is invalid")
		}
		decoded, decodeErr := decoder.Decode(ctx, eventdecoder.Log{ChainID: uint64(chainID), Contract: value.Address, TransactionHash: txHash, LogIndex: uint32(logIndex), Topics: value.Topics, Data: value.Data})
		if decodeErr != nil {
			return page, decodeErr
		}
		item := canonicalapi.CanonicalRawLog{
			BlockNumber: blockNumber, BlockTime: blockTime, TransactionIndex: uint32(transactionIndex), LogIndex: uint32(logIndex),
			Contract: canonicalapi.AddressDTO{Address: strings.ToLower(value.Address)}, Data: strings.ToLower(value.Data), Removed: value.Removed,
			EventName: decoded.EventName, EventSignature: decoded.EventSignature, Decoded: json.RawMessage(decoded.DecodedFieldsJSON()),
			Provenance: canonicalapi.ProvenanceDTO{SourceProvider: firstNonEmptyRPC(source, "rpc_receipt"), SourceType: "RPC_RECEIPT_LOG", ParserVersion: "eventdecoder-v1", NormalizerVersion: "rpc-receipt-v1", SchemaVersion: 2, IngestedAt: time.Now().UTC()},
		}
		topicTargets := []**string{&item.Topic0, &item.Topic1, &item.Topic2, &item.Topic3}
		for index, topic := range value.Topics {
			if index >= len(topicTargets) {
				break
			}
			normalized := strings.ToLower(topic)
			*topicTargets[index] = &normalized
		}
		page.Items = append(page.Items, item)
	}
	page.Count = len(page.Items)
	return page, nil
}

func rpcReceiptBlockTime(ctx context.Context, client explorerRPCClient, chainKey string, receipt explorerRPCReceipt) (time.Time, error) {
	if len(receipt.Logs) > 0 && receipt.Logs[0].BlockTimestamp != "" {
		seconds, err := rpcHexUint64(receipt.Logs[0].BlockTimestamp)
		if err == nil {
			return time.Unix(int64(seconds), 0).UTC(), nil
		}
	}
	raw, _, err := client.Call(ctx, chainKey, "eth_getBlockByNumber", []any{receipt.BlockNumber, false})
	if err != nil {
		return time.Time{}, err
	}
	var block struct {
		Timestamp string `json:"timestamp"`
	}
	if json.Unmarshal(raw, &block) != nil {
		return time.Time{}, errors.New("RPC block response is invalid")
	}
	seconds, err := rpcHexUint64(block.Timestamp)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(int64(seconds), 0).UTC(), nil
}

func rpcHexUint64(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(value), "0x"), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid RPC hex quantity: %w", err)
	}
	return parsed, nil
}

func rpcHexBig(value string) (*big.Int, error) {
	parsed := new(big.Int)
	if _, ok := parsed.SetString(strings.TrimPrefix(strings.TrimSpace(value), "0x"), 16); !ok {
		return nil, errors.New("invalid RPC big quantity")
	}
	return parsed, nil
}

func formatRPCDecimal(value *big.Int, decimals int) string {
	if value == nil {
		return "0"
	}
	digits := new(big.Int).Abs(value).String()
	for len(digits) <= decimals {
		digits = "0" + digits
	}
	point := len(digits) - decimals
	formatted := strings.TrimRight(digits[:point]+"."+digits[point:], "0")
	formatted = strings.TrimSuffix(formatted, ".")
	if value.Sign() < 0 && formatted != "0" {
		formatted = "-" + formatted
	}
	return formatted
}

func firstNonEmptyRPC(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
