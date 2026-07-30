package normalize

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource/sqd"
)

func DecodeTransferLog(network chain.EVM, header sqd.Header, item sqd.Log) ([]TokenTransfer, []NFTTransfer, error) {
	if len(item.Topics) == 0 {
		return nil, nil, errors.New("Transfer Log 缺少 topic0")
	}
	topic0 := strings.ToLower(item.Topics[0])
	blockTime := time.Unix(header.Timestamp, 0).UTC()
	switch topic0 {
	case sqd.TransferTopic:
		if len(item.Topics) == 3 {
			amount, err := hexUint(item.Data)
			if err != nil {
				return nil, nil, err
			}
			standard := "ERC20"
			if network.Key == "bsc" {
				standard = "BEP20"
			}
			return []TokenTransfer{{
				ChainKey: network.Key, ChainID: network.ID, TxHash: strings.ToLower(item.TransactionHash),
				LogIndex: item.LogIndex, BlockNumber: header.Number, BlockTime: blockTime,
				TokenAddress: strings.ToLower(item.Address), FromAddress: topicAddress(item.Topics[1]),
				ToAddress: topicAddress(item.Topics[2]), AmountRaw: amount, Standard: standard,
			}}, nil, nil
		}
		if len(item.Topics) == 4 {
			tokenID, err := hexUint(item.Topics[3])
			if err != nil {
				return nil, nil, err
			}
			return nil, []NFTTransfer{{
				ChainKey: network.Key, ChainID: network.ID, TxHash: strings.ToLower(item.TransactionHash),
				LogIndex: item.LogIndex, BlockNumber: header.Number, BlockTime: blockTime,
				ContractAddress: strings.ToLower(item.Address), TokenID: tokenID,
				FromAddress: topicAddress(item.Topics[1]), ToAddress: topicAddress(item.Topics[2]),
				Amount: "1", Standard: "ERC721",
			}}, nil
		}
		return nil, nil, fmt.Errorf("Transfer topics 数量不支持: %d", len(item.Topics))
	case sqd.TransferSingleTopic:
		if len(item.Topics) != 4 {
			return nil, nil, fmt.Errorf("TransferSingle topics 数量错误: %d", len(item.Topics))
		}
		words, err := fixedWords(item.Data, 2)
		if err != nil {
			return nil, nil, err
		}
		return nil, []NFTTransfer{{
			ChainKey: network.Key, ChainID: network.ID, TxHash: strings.ToLower(item.TransactionHash),
			LogIndex: item.LogIndex, BlockNumber: header.Number, BlockTime: blockTime,
			ContractAddress: strings.ToLower(item.Address), TokenID: words[0],
			FromAddress: topicAddress(item.Topics[2]), ToAddress: topicAddress(item.Topics[3]),
			Amount: words[1], Standard: "ERC1155",
		}}, nil
	case sqd.TransferBatchTopic:
		if len(item.Topics) != 4 {
			return nil, nil, fmt.Errorf("TransferBatch topics 数量错误: %d", len(item.Topics))
		}
		ids, values, err := decodeTransferBatch(item.Data)
		if err != nil {
			return nil, nil, err
		}
		transfers := make([]NFTTransfer, 0, len(ids))
		for index := range ids {
			transfers = append(transfers, NFTTransfer{
				ChainKey: network.Key, ChainID: network.ID, TxHash: strings.ToLower(item.TransactionHash),
				LogIndex: item.LogIndex, BatchIndex: index, BlockNumber: header.Number, BlockTime: blockTime,
				ContractAddress: strings.ToLower(item.Address), TokenID: ids[index],
				FromAddress: topicAddress(item.Topics[2]), ToAddress: topicAddress(item.Topics[3]),
				Amount: values[index], Standard: "ERC1155",
			})
		}
		return nil, transfers, nil
	default:
		return nil, nil, fmt.Errorf("非标准 Transfer topic: %s", topic0)
	}
}

func NormalizeTrace(network chain.EVM, header sqd.Header, item sqd.Trace, txHash string, ordinal int) (Trace, *InternalTransaction, error) {
	if txHash == "" {
		return Trace{}, nil, errors.New("Trace 无法关联父交易哈希")
	}
	traceID := fmt.Sprintf("%d-%d-%d", header.Number, item.TransactionIndex, ordinal)
	value := "0"
	if item.Action.Value != nil && *item.Action.Value != "" {
		parsed, err := hexUint(*item.Action.Value)
		if err != nil {
			return Trace{}, nil, err
		}
		value = parsed
	}
	output := ""
	if item.Result != nil {
		output = item.Result.Output
		if item.Type == "create" && item.Action.To == "" {
			item.Action.To = item.Result.Address
		}
	}
	status := "SUCCESS"
	errorText := ""
	if item.Error != nil && *item.Error != "" {
		status = "FAILED"
		errorText = *item.Error
	}
	result := Trace{
		ChainKey: network.Key, ChainID: network.ID, TxHash: strings.ToLower(txHash), TraceID: traceID,
		TraceDepth:  len(item.TraceAddress),
		BlockNumber: header.Number, BlockTime: time.Unix(header.Timestamp, 0).UTC(),
		FromAddress: strings.ToLower(item.Action.From), ToAddress: strings.ToLower(item.Action.To),
		ValueRaw: value, CallType: strings.ToUpper(item.Type), Input: item.Action.Input,
		Output: output, Status: status, Error: errorText,
	}
	var internal *InternalTransaction
	amount := new(big.Int)
	if _, ok := amount.SetString(value, 10); ok && amount.Sign() > 0 && status == "SUCCESS" &&
		result.FromAddress != "" && result.ToAddress != "" {
		internal = &InternalTransaction{
			ChainKey: network.Key, ChainID: network.ID, TxHash: result.TxHash, TraceID: traceID,
			BlockNumber: header.Number, BlockTime: result.BlockTime,
			FromAddress: result.FromAddress, ToAddress: result.ToAddress, ValueRaw: value,
			Type: result.CallType,
		}
	}
	return result, internal, nil
}

func topicAddress(value string) string {
	value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "0x")
	if len(value) < 40 {
		return ""
	}
	return "0x" + value[len(value)-40:]
}

func fixedWords(value string, count int) ([]string, error) {
	data, err := hexBytes(value)
	if err != nil {
		return nil, err
	}
	if len(data) != count*32 {
		return nil, fmt.Errorf("ABI 数据长度错误: %d", len(data))
	}
	result := make([]string, count)
	for index := 0; index < count; index++ {
		result[index] = new(big.Int).SetBytes(data[index*32 : (index+1)*32]).String()
	}
	return result, nil
}

func decodeTransferBatch(value string) ([]string, []string, error) {
	data, err := hexBytes(value)
	if err != nil {
		return nil, nil, err
	}
	if len(data) < 64 {
		return nil, nil, errors.New("TransferBatch ABI 头长度不足")
	}
	idsOffset, err := wordOffset(data[:32])
	if err != nil {
		return nil, nil, err
	}
	valuesOffset, err := wordOffset(data[32:64])
	if err != nil {
		return nil, nil, err
	}
	ids, err := dynamicUintArray(data, idsOffset)
	if err != nil {
		return nil, nil, err
	}
	values, err := dynamicUintArray(data, valuesOffset)
	if err != nil {
		return nil, nil, err
	}
	if len(ids) != len(values) {
		return nil, nil, errors.New("TransferBatch ids/values 数量不一致")
	}
	return ids, values, nil
}

func wordOffset(word []byte) (int, error) {
	value := new(big.Int).SetBytes(word)
	if !value.IsInt64() {
		return 0, errors.New("ABI offset 超出范围")
	}
	offset := value.Int64()
	if offset < 0 || offset > int64(^uint(0)>>1) {
		return 0, errors.New("ABI offset 无效")
	}
	return int(offset), nil
}

func dynamicUintArray(data []byte, offset int) ([]string, error) {
	if offset < 0 || offset+32 > len(data) {
		return nil, errors.New("ABI 动态数组 offset 越界")
	}
	countValue := new(big.Int).SetBytes(data[offset : offset+32])
	if !countValue.IsUint64() || countValue.Uint64() > uint64((len(data)-offset-32)/32) {
		return nil, errors.New("ABI 动态数组长度越界")
	}
	count := int(countValue.Uint64())
	result := make([]string, count)
	for index := 0; index < count; index++ {
		start := offset + 32 + index*32
		result[index] = new(big.Int).SetBytes(data[start : start+32]).String()
	}
	return result, nil
}

func hexUint(value string) (string, error) {
	data, err := hexBytes(value)
	if err != nil {
		return "", err
	}
	return new(big.Int).SetBytes(data).String(), nil
}

func hexBytes(value string) ([]byte, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if value == "" {
		return []byte{}, nil
	}
	if len(value)%2 != 0 {
		value = "0" + value
	}
	data, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("无效十六进制数据: %w", err)
	}
	return data, nil
}
