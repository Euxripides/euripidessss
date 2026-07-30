package parquetdownload

import (
	"context"
	"encoding/csv"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/etl/backend/internal/chain"
	rpcsource "github.com/etl/backend/internal/datasource/rpc"
	"github.com/etl/backend/internal/datasource/sqd"
	"github.com/etl/backend/internal/normalize"
)

type sqdOutcome struct {
	TransactionRows   int64
	LogRows           int64
	TokenMetadataRows int64
	TokenTransferRows int64
	NFTTransferRows   int64
	TraceRows         int64
	InternalRows      int64
	ActivityRows      int64
	SummaryRows       int64
	BalanceRows       int64
	Outputs           []string
}

type csvSink struct {
	file   *os.File
	writer *csv.Writer
	path   string
}

func newCSVSink(path string, header []string) (*csvSink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	writer := csv.NewWriter(file)
	if err := writer.Write(header); err != nil {
		file.Close()
		return nil, err
	}
	return &csvSink{file: file, writer: writer, path: path}, nil
}

func (s *csvSink) close() error {
	s.writer.Flush()
	if err := s.writer.Error(); err != nil {
		s.file.Close()
		return err
	}
	return s.file.Close()
}

func (m *Manager) ingestSQD(
	ctx context.Context,
	jobID string,
	settings Settings,
	network chain.EVM,
	blockRange SQDBlockRange,
	addresses []string,
	selected []string,
) (sqdOutcome, error) {
	if m.engine == nil || !m.engine.Available() {
		return sqdOutcome{}, fmt.Errorf("DuckDB CLI 不可用，无法写入 SQD Parquet")
	}
	tempDir := filepath.Join(settings.DataRoot, "tmp", "job-"+jobID, "sqd")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return sqdOutcome{}, err
	}
	defer os.RemoveAll(tempDir)

	targets := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		targets[strings.ToLower(address)] = true
	}
	result := sqdOutcome{}
	activitySink, err := newCSVSink(filepath.Join(tempDir, "address_activity.csv"), []string{
		"chain_key", "chain_id", "address", "counterparty", "direction", "activity_type",
		"asset_type", "asset_address", "symbol", "amount_raw", "amount", "tx_hash", "block_time",
		"method_id", "trace_depth", "status", "source",
	})
	if err != nil {
		return result, err
	}
	activityClosed := false
	defer func() {
		if !activityClosed {
			_ = activitySink.close()
		}
	}()

	if hasSelectedSource(selected, "transactions") && network.Key != "bsc" {
		transactionSink, sinkErr := newCSVSink(filepath.Join(tempDir, "transactions.csv"), []string{
			"chain_key", "chain_id", "tx_hash", "block_number", "block_time", "from_address",
			"to_address", "value_raw", "input", "method_id", "status", "gas_used", "gas_price", "source",
		})
		if sinkErr != nil {
			return result, sinkErr
		}
		m.mutate(jobID, func(job *Job) {
			job.Stage = "transactions"
			setStage(job, "transactions", StatusRunning, 0, "SQD 多链交易按地址流式筛选")
		})
		streamErr := m.sqd.StreamTransactions(ctx, network, sqd.BlockRange{From: blockRange.From, To: blockRange.To}, addresses, func(block sqd.Block) error {
			for _, item := range block.Transactions {
				valueRaw, quantityErr := hexQuantityDecimal(item.Value)
				if quantityErr != nil {
					return fmt.Errorf("解析交易 %s value: %w", item.Hash, quantityErr)
				}
				methodID := strings.ToLower(item.Sighash)
				if methodID == "" && len(item.Input) >= 10 {
					methodID = strings.ToLower(item.Input[:10])
				}
				transaction := normalize.UnifiedTransaction{
					ChainKey: network.Key, ChainID: network.ID, TxHash: strings.ToLower(item.Hash),
					BlockNumber: block.Header.Number, BlockTime: time.Unix(block.Header.Timestamp, 0).UTC(),
					FromAddress: strings.ToLower(item.From), ToAddress: strings.ToLower(item.To),
					ValueRaw: valueRaw, Input: item.Input, MethodID: methodID, Status: item.Status,
					GasUsed: item.GasUsed, GasPrice: item.GasPrice, Source: "SQD_TRANSACTION",
				}
				if err := writeUnifiedTransaction(transactionSink.writer, transaction); err != nil {
					return err
				}
				result.TransactionRows++
				activityType := "NATIVE_TRANSFER"
				if item.Input != "" && item.Input != "0x" {
					activityType = normalize.ActivityTypeForMethod(methodID, "CONTRACT_CALL")
				}
				written, activityErr := writeTransferActivities(
					activitySink.writer, targets, network, transaction.FromAddress, transaction.ToAddress,
					"", valueRaw, formatUnits(valueRaw, 18), transaction.TxHash,
					transaction.BlockTime.Format("2006-01-02T15:04:05Z"), activityType, "NATIVE",
					methodID, 0, transactionStatus(transaction.Status), transaction.Source,
				)
				if activityErr != nil {
					return activityErr
				}
				result.ActivityRows += written
			}
			m.updateSQDProgress(jobID, "transactions", block.Header.Number, blockRange, fmt.Sprintf("已统一 %d 条交易", result.TransactionRows))
			return nil
		})
		if closeErr := transactionSink.close(); streamErr == nil && closeErr != nil {
			streamErr = closeErr
		}
		if streamErr != nil {
			return result, streamErr
		}
		output, err := m.writeSQDTransactionParquet(ctx, jobID, settings, network, transactionSink.path)
		if err != nil {
			return result, err
		}
		result.Outputs = append(result.Outputs, output)
		m.mutate(jobID, func(job *Job) {
			setStage(job, "transactions", StatusDone, 100, fmt.Sprintf("%d 条统一交易", result.TransactionRows))
		})
	} else {
		m.mutate(jobID, func(job *Job) {
			detail := "BSC 使用 AWS transactions"
			if !hasSelectedSource(selected, "transactions") {
				detail = "未选择"
			}
			setStage(job, "transactions", StatusDone, 100, detail)
		})
	}

	tokenContracts := map[string]string{}
	var metadataItems []normalize.TokenMetadata
	if hasSelectedSource(selected, "logs") {
		logSink, sinkErr := newCSVSink(filepath.Join(tempDir, "logs.csv"), []string{
			"chain_key", "chain_id", "block_number", "block_time", "tx_hash", "tx_index",
			"log_index", "contract_address", "topic0", "topic1", "topic2", "topic3", "data",
		})
		if sinkErr != nil {
			return result, sinkErr
		}
		tokenSink, sinkErr := newCSVSink(filepath.Join(tempDir, "token_transfers.csv"), []string{
			"chain_key", "chain_id", "tx_hash", "log_index", "block_number", "block_time",
			"token_address", "from_address", "to_address", "amount_raw", "amount", "symbol", "decimals", "standard",
		})
		if sinkErr != nil {
			_ = logSink.close()
			return result, sinkErr
		}
		nftSink, sinkErr := newCSVSink(filepath.Join(tempDir, "nft_transfers.csv"), []string{
			"chain_key", "chain_id", "tx_hash", "log_index", "batch_index", "block_number",
			"block_time", "contract_address", "token_id", "from_address", "to_address", "amount", "standard",
		})
		if sinkErr != nil {
			_ = logSink.close()
			_ = tokenSink.close()
			return result, sinkErr
		}
		m.mutate(jobID, func(job *Job) {
			job.Stage = "logs"
			setStage(job, "logs", StatusRunning, 0, "SQD finalized-stream 连接成功，开始流式筛选")
			setStage(job, "nft", StatusRunning, 0, "等待解析标准事件")
		})
		streamErr := m.sqd.StreamLogs(ctx, network, sqd.BlockRange{From: blockRange.From, To: blockRange.To}, addresses, func(block sqd.Block) error {
			for _, item := range block.Logs {
				topics := make([]string, 4)
				copy(topics, item.Topics)
				if err := logSink.writer.Write([]string{
					network.Key, strconv.FormatInt(network.ID, 10), strconv.FormatUint(block.Header.Number, 10),
					formatUnix(block.Header.Timestamp), strings.ToLower(item.TransactionHash),
					strconv.FormatUint(item.TransactionIndex, 10), strconv.FormatUint(item.LogIndex, 10),
					strings.ToLower(item.Address), topics[0], topics[1], topics[2], topics[3], item.Data,
				}); err != nil {
					return err
				}
				result.LogRows++
				tokens, nfts, decodeErr := normalize.DecodeTransferLog(network, block.Header, item)
				if decodeErr != nil {
					return fmt.Errorf("解析区块 %d Log %d: %w", block.Header.Number, item.LogIndex, decodeErr)
				}
				for _, transfer := range tokens {
					tokenContracts[transfer.TokenAddress] = transfer.Standard
					if err := writeTokenTransfer(tokenSink.writer, transfer); err != nil {
						return err
					}
					result.TokenTransferRows++
					written, activityErr := writeTransferActivities(activitySink.writer, targets, network, transfer.FromAddress, transfer.ToAddress, transfer.TokenAddress, transfer.AmountRaw, "", transfer.TxHash, transfer.BlockTime.Format("2006-01-02T15:04:05Z"), "TOKEN_TRANSFER", "TOKEN", "", 0, "UNKNOWN", "SQD_LOG")
					if activityErr != nil {
						return activityErr
					}
					result.ActivityRows += written
				}
				for _, transfer := range nfts {
					if err := writeNFTTransfer(nftSink.writer, transfer); err != nil {
						return err
					}
					result.NFTTransferRows++
					written, activityErr := writeTransferActivities(activitySink.writer, targets, network, transfer.FromAddress, transfer.ToAddress, transfer.ContractAddress, transfer.Amount, transfer.Amount, transfer.TxHash, transfer.BlockTime.Format("2006-01-02T15:04:05Z"), "NFT_TRANSFER", transfer.Standard, "", 0, "UNKNOWN", "SQD_LOG")
					if activityErr != nil {
						return activityErr
					}
					result.ActivityRows += written
				}
			}
			m.updateSQDProgress(jobID, "logs", block.Header.Number, blockRange, fmt.Sprintf("已读取 %d 条日志", result.LogRows))
			m.updateSQDProgress(jobID, "nft", block.Header.Number, blockRange, fmt.Sprintf("Token %d / NFT %d", result.TokenTransferRows, result.NFTTransferRows))
			return nil
		})
		for _, sink := range []*csvSink{logSink, tokenSink, nftSink} {
			if closeErr := sink.close(); streamErr == nil && closeErr != nil {
				streamErr = closeErr
			}
		}
		if streamErr != nil {
			return result, streamErr
		}
		outputs, err := m.writeSQDLogParquet(ctx, jobID, settings, network, logSink.path, tokenSink.path, nftSink.path)
		if err != nil {
			return result, err
		}
		result.Outputs = append(result.Outputs, outputs...)
		metadataPath, resolvedMetadata, metadataErr := m.enrichTokenMetadata(ctx, jobID, settings, network, tempDir, tokenContracts)
		if metadataErr != nil {
			return result, metadataErr
		}
		metadataItems = resolvedMetadata
		result.TokenMetadataRows = int64(len(metadataItems))
		result.Outputs = append(result.Outputs, metadataPath)
		m.mutate(jobID, func(job *Job) {
			setStage(job, "logs", StatusDone, 100, fmt.Sprintf("%d 条标准事件日志", result.LogRows))
			setStage(job, "metadata", StatusDone, 100, fmt.Sprintf("%d 个 Token 合约", result.TokenMetadataRows))
			setStage(job, "nft", StatusDone, 100, fmt.Sprintf("Token %d / NFT %d", result.TokenTransferRows, result.NFTTransferRows))
		})
	} else {
		m.mutate(jobID, func(job *Job) {
			setStage(job, "logs", StatusDone, 100, "未选择")
			setStage(job, "metadata", StatusDone, 100, "未选择")
			setStage(job, "nft", StatusDone, 100, "未选择")
		})
	}

	if hasSelectedSource(selected, "traces") {
		traceSink, sinkErr := newCSVSink(filepath.Join(tempDir, "traces.csv"), []string{
			"chain_key", "chain_id", "tx_hash", "trace_id", "trace_depth", "block_number", "block_time",
			"from_address", "to_address", "value_raw", "call_type", "input", "output", "status", "error",
		})
		if sinkErr != nil {
			return result, sinkErr
		}
		internalSink, sinkErr := newCSVSink(filepath.Join(tempDir, "internal_transactions.csv"), []string{
			"chain_key", "chain_id", "tx_hash", "trace_id", "block_number", "block_time",
			"from_address", "to_address", "value_raw", "type",
		})
		if sinkErr != nil {
			_ = traceSink.close()
			return result, sinkErr
		}
		m.mutate(jobID, func(job *Job) {
			job.Stage = "traces"
			setStage(job, "traces", StatusRunning, 0, "按目标地址筛选 Trace")
		})
		streamErr := m.sqd.StreamTraces(ctx, network, sqd.BlockRange{From: blockRange.From, To: blockRange.To}, addresses, func(block sqd.Block) error {
			txHashes := make(map[uint64]string, len(block.Transactions))
			for _, transaction := range block.Transactions {
				txHashes[transaction.TransactionIndex] = transaction.Hash
			}
			for index, item := range block.Traces {
				trace, internal, normalizeErr := normalize.NormalizeTrace(network, block.Header, item, txHashes[item.TransactionIndex], index)
				if normalizeErr != nil {
					return fmt.Errorf("解析区块 %d Trace %d: %w", block.Header.Number, index, normalizeErr)
				}
				if err := writeTrace(traceSink.writer, trace); err != nil {
					return err
				}
				result.TraceRows++
				if internal != nil {
					if err := writeInternalTransaction(internalSink.writer, *internal); err != nil {
						return err
					}
					result.InternalRows++
					written, activityErr := writeTransferActivities(activitySink.writer, targets, network, internal.FromAddress, internal.ToAddress, "", internal.ValueRaw, formatUnits(internal.ValueRaw, 18), internal.TxHash, internal.BlockTime.Format("2006-01-02T15:04:05Z"), "INTERNAL_TRANSFER", "NATIVE", "", trace.TraceDepth, trace.Status, "SQD_TRACE")
					if activityErr != nil {
						return activityErr
					}
					result.ActivityRows += written
				}
			}
			m.updateSQDProgress(jobID, "traces", block.Header.Number, blockRange, fmt.Sprintf("Trace %d / 内部交易 %d", result.TraceRows, result.InternalRows))
			return nil
		})
		for _, sink := range []*csvSink{traceSink, internalSink} {
			if closeErr := sink.close(); streamErr == nil && closeErr != nil {
				streamErr = closeErr
			}
		}
		if streamErr != nil {
			return result, streamErr
		}
		outputs, err := m.writeSQDTraceParquet(ctx, jobID, settings, network, traceSink.path, internalSink.path)
		if err != nil {
			return result, err
		}
		result.Outputs = append(result.Outputs, outputs...)
		m.mutate(jobID, func(job *Job) {
			setStage(job, "traces", StatusDone, 100, fmt.Sprintf("Trace %d / 内部交易 %d", result.TraceRows, result.InternalRows))
		})
	} else {
		m.mutate(jobID, func(job *Job) { setStage(job, "traces", StatusDone, 100, "未选择") })
	}

	if err := activitySink.close(); err != nil {
		return result, err
	}
	activityClosed = true
	activityPath, err := m.writeSQDActivityParquet(ctx, jobID, settings, network, activitySink.path)
	if err != nil {
		return result, err
	}
	result.Outputs = append(result.Outputs, activityPath)
	balancePath, balanceRows, balanceErr := m.writeBalanceSnapshots(ctx, jobID, settings, network, tempDir, addresses, metadataItems)
	if balanceErr != nil {
		return result, balanceErr
	}
	if balancePath != "" {
		result.Outputs = append(result.Outputs, balancePath)
	}
	result.BalanceRows = balanceRows
	methodPath, err := m.writeMethodSignatures(ctx, settings, tempDir)
	if err != nil {
		return result, err
	}
	result.Outputs = append(result.Outputs, methodPath)
	return result, nil
}

func (m *Manager) updateSQDProgress(jobID, stage string, current uint64, blockRange SQDBlockRange, detail string) {
	progress := 100.0
	if blockRange.To > blockRange.From && current >= blockRange.From {
		progress = float64(current-blockRange.From) / float64(blockRange.To-blockRange.From) * 100
	}
	m.mutate(jobID, func(job *Job) {
		setStage(job, stage, StatusRunning, minFloat(99, progress), detail)
		job.Progress = maxFloat(job.Progress, 35+progress*0.5)
	})
}

func writeTokenTransfer(writer *csv.Writer, item normalize.TokenTransfer) error {
	decimals := ""
	if item.Symbol != "" || item.Amount != "" {
		decimals = strconv.FormatUint(uint64(item.Decimals), 10)
	}
	return writer.Write([]string{
		item.ChainKey, strconv.FormatInt(item.ChainID, 10), item.TxHash, strconv.FormatUint(item.LogIndex, 10),
		strconv.FormatUint(item.BlockNumber, 10), item.BlockTime.Format("2006-01-02T15:04:05Z"),
		item.TokenAddress, item.FromAddress, item.ToAddress, item.AmountRaw, item.Amount, item.Symbol,
		decimals, item.Standard,
	})
}

func writeNFTTransfer(writer *csv.Writer, item normalize.NFTTransfer) error {
	return writer.Write([]string{
		item.ChainKey, strconv.FormatInt(item.ChainID, 10), item.TxHash, strconv.FormatUint(item.LogIndex, 10),
		strconv.Itoa(item.BatchIndex), strconv.FormatUint(item.BlockNumber, 10), item.BlockTime.Format("2006-01-02T15:04:05Z"),
		item.ContractAddress, item.TokenID, item.FromAddress, item.ToAddress, item.Amount, item.Standard,
	})
}

func writeTrace(writer *csv.Writer, item normalize.Trace) error {
	return writer.Write([]string{
		item.ChainKey, strconv.FormatInt(item.ChainID, 10), item.TxHash, item.TraceID,
		strconv.Itoa(item.TraceDepth), strconv.FormatUint(item.BlockNumber, 10), item.BlockTime.Format("2006-01-02T15:04:05Z"),
		item.FromAddress, item.ToAddress, item.ValueRaw, item.CallType, item.Input, item.Output, item.Status, item.Error,
	})
}

func writeInternalTransaction(writer *csv.Writer, item normalize.InternalTransaction) error {
	return writer.Write([]string{
		item.ChainKey, strconv.FormatInt(item.ChainID, 10), item.TxHash, item.TraceID,
		strconv.FormatUint(item.BlockNumber, 10), item.BlockTime.Format("2006-01-02T15:04:05Z"),
		item.FromAddress, item.ToAddress, item.ValueRaw, item.Type,
	})
}

func writeTransferActivities(
	writer *csv.Writer,
	targets map[string]bool,
	network chain.EVM,
	from, to, assetAddress, amountRaw, amount, txHash, blockTime, activityType, assetType, methodID string,
	traceDepth int,
	status, source string,
) (int64, error) {
	from = strings.ToLower(from)
	to = strings.ToLower(to)
	var written int64
	if targets[from] {
		if err := writer.Write([]string{network.Key, strconv.FormatInt(network.ID, 10), from, to, "OUT", activityType, assetType, assetAddress, symbolForActivity(network, assetType), amountRaw, amount, txHash, blockTime, methodID, strconv.Itoa(traceDepth), status, source}); err != nil {
			return written, err
		}
		written++
	}
	if targets[to] && to != from {
		if err := writer.Write([]string{network.Key, strconv.FormatInt(network.ID, 10), to, from, "IN", activityType, assetType, assetAddress, symbolForActivity(network, assetType), amountRaw, amount, txHash, blockTime, methodID, strconv.Itoa(traceDepth), status, source}); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func symbolForActivity(network chain.EVM, assetType string) string {
	if assetType == "NATIVE" {
		return network.NativeSymbol
	}
	return ""
}

func transactionStatus(status uint64) string {
	if status == 1 {
		return "SUCCESS"
	}
	return "FAILED"
}

func formatUnix(timestamp int64) string {
	return time.Unix(timestamp, 0).UTC().Format("2006-01-02T15:04:05Z")
}

func hexQuantityDecimal(value string) (string, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if value == "" {
		return "0", nil
	}
	number := new(big.Int)
	if _, ok := number.SetString(value, 16); !ok {
		return "", fmt.Errorf("无效十六进制数量")
	}
	return number.String(), nil
}

func formatUnits(value string, decimals int) string {
	number := new(big.Int)
	if _, ok := number.SetString(value, 10); !ok {
		return ""
	}
	if decimals <= 0 {
		return number.String()
	}
	text := number.String()
	if len(text) <= decimals {
		text = strings.Repeat("0", decimals-len(text)+1) + text
	}
	split := len(text) - decimals
	result := text[:split] + "." + text[split:]
	result = strings.TrimRight(strings.TrimRight(result, "0"), ".")
	if result == "" {
		return "0"
	}
	return result
}

func writeUnifiedTransaction(writer *csv.Writer, item normalize.UnifiedTransaction) error {
	return writer.Write([]string{
		item.ChainKey, strconv.FormatInt(item.ChainID, 10), item.TxHash,
		strconv.FormatUint(item.BlockNumber, 10), item.BlockTime.Format("2006-01-02T15:04:05Z"),
		item.FromAddress, item.ToAddress, item.ValueRaw, item.Input, item.MethodID,
		strconv.FormatUint(item.Status, 10), item.GasUsed, item.GasPrice, item.Source,
	})
}

func (m *Manager) enrichTokenMetadata(
	ctx context.Context,
	jobID string,
	settings Settings,
	network chain.EVM,
	tempDir string,
	tokens map[string]string,
) (string, []normalize.TokenMetadata, error) {
	m.mutate(jobID, func(job *Job) {
		job.Stage = "metadata"
		setStage(job, "metadata", StatusRunning, 5, "准备 Token Metadata RPC 队列")
	})
	items := make([]normalize.TokenMetadata, 0, len(tokens))
	m.mu.RLock()
	managedRPC := m.rpcManager
	m.mu.RUnlock()
	if managedRPC != nil && managedRPC.HasConfigured(network.Key) && len(tokens) > 0 {
		for address, standard := range tokens {
			item, enrichErr := managedRPC.Token(ctx, network.Key, address, false)
			if enrichErr != nil {
				continue
			}
			items = append(items, normalize.TokenMetadata{
				ChainKey: network.Key, ChainID: network.ID, TokenAddress: address,
				Name: item.Name, Symbol: item.Symbol, Decimals: item.Decimals,
				Standard: standard, TotalSupply: item.TotalSupply,
				UpdatedAt: item.UpdatedAt, Source: "RPC",
			})
		}
	}
	endpoint := strings.TrimSpace(os.Getenv(network.RPCEnv))
	if len(items) == 0 && endpoint != "" && len(tokens) > 0 {
		client, err := rpcsource.New(endpoint, nil)
		if err == nil {
			items, err = client.TokenMetadata(ctx, network, tokens)
		}
		if err != nil {
			m.mutate(jobID, func(job *Job) {
				job.Warnings = append(job.Warnings, "Token Metadata RPC 失败，已按 UNKNOWN 保留："+err.Error())
			})
			items = nil
		}
	}
	resolvedAddresses := make(map[string]bool, len(items))
	for _, item := range items {
		resolvedAddresses[strings.ToLower(item.TokenAddress)] = true
	}
	for address, standard := range tokens {
		if !resolvedAddresses[strings.ToLower(address)] {
			items = append(items, normalize.TokenMetadata{
				ChainKey: network.Key, ChainID: network.ID, TokenAddress: address,
				Name: "UNKNOWN", Symbol: "UNKNOWN", Standard: standard,
				UpdatedAt: time.Now().UTC(), Source: "UNAVAILABLE",
			})
		}
	}
	sink, err := newCSVSink(filepath.Join(tempDir, "token_metadata.csv"), []string{
		"chain_key", "chain_id", "token_address", "name", "symbol", "decimals",
		"standard", "total_supply", "logo_url", "updated_at", "source",
	})
	if err != nil {
		return "", nil, err
	}
	for _, item := range items {
		decimals := ""
		if item.Decimals != nil {
			decimals = strconv.FormatUint(uint64(*item.Decimals), 10)
		}
		if err := sink.writer.Write([]string{
			item.ChainKey, strconv.FormatInt(item.ChainID, 10), item.TokenAddress,
			item.Name, item.Symbol, decimals, item.Standard, item.TotalSupply,
			item.LogoURL, item.UpdatedAt.Format(time.RFC3339), item.Source,
		}); err != nil {
			_ = sink.close()
			return "", nil, err
		}
	}
	if err := sink.close(); err != nil {
		return "", nil, err
	}
	outputs, err := m.writeSQDParquetSpecs(ctx, jobID, settings, network, []struct {
		table string
		file  string
		name  string
	}{{"token_metadata", sink.path, "token-metadata.parquet"}})
	if err != nil {
		return "", nil, err
	}
	return outputs[0], items, nil
}

func (m *Manager) writeBalanceSnapshots(
	ctx context.Context,
	jobID string,
	settings Settings,
	network chain.EVM,
	tempDir string,
	addresses []string,
	metadata []normalize.TokenMetadata,
) (string, int64, error) {
	endpoint := strings.TrimSpace(os.Getenv(network.RPCEnv))
	if endpoint == "" {
		m.mutate(jobID, func(job *Job) {
			setStage(job, "balances", StatusDone, 100, "未配置 "+network.RPCEnv+"，余额不猜测")
		})
		return "", 0, nil
	}
	m.mutate(jobID, func(job *Job) {
		job.Stage = "balances"
		setStage(job, "balances", StatusRunning, 10, "读取原生币与已识别 Token 最新余额")
	})
	client, err := rpcsource.New(endpoint, nil)
	if err != nil {
		return "", 0, err
	}
	snapshots, err := client.BalanceSnapshots(ctx, network, addresses, metadata)
	if err != nil {
		m.mutate(jobID, func(job *Job) {
			job.Warnings = append(job.Warnings, "Balance Snapshot RPC 失败，未生成余额表："+err.Error())
			setStage(job, "balances", StatusDone, 100, "RPC 失败，未猜测余额")
		})
		return "", 0, nil
	}
	sink, err := newCSVSink(filepath.Join(tempDir, "balance_snapshot.csv"), []string{
		"chain_key", "chain_id", "address", "asset_type", "asset_address",
		"balance_raw", "balance", "snapshot_time", "source",
	})
	if err != nil {
		return "", 0, err
	}
	for _, item := range snapshots {
		if err := sink.writer.Write([]string{
			item.ChainKey, strconv.FormatInt(item.ChainID, 10), item.Address,
			item.AssetType, item.AssetAddress, item.BalanceRaw, item.Balance,
			item.SnapshotTime.Format(time.RFC3339), item.Source,
		}); err != nil {
			_ = sink.close()
			return "", 0, err
		}
	}
	if err := sink.close(); err != nil {
		return "", 0, err
	}
	outputs, err := m.writeSQDParquetSpecs(ctx, jobID, settings, network, []struct {
		table string
		file  string
		name  string
	}{{"balances", sink.path, "balance-snapshot.parquet"}})
	if err != nil {
		return "", 0, err
	}
	m.mutate(jobID, func(job *Job) {
		setStage(job, "balances", StatusDone, 100, fmt.Sprintf("%d 条余额快照", len(snapshots)))
	})
	return outputs[0], int64(len(snapshots)), nil
}

func (m *Manager) writeAddressSummary(
	ctx context.Context,
	jobID string,
	settings Settings,
	network chain.EVM,
	tempDir string,
	activityPaths []string,
	addresses []string,
) (string, int64, error) {
	m.mutate(jobID, func(job *Job) {
		job.Stage = "summary"
		setStage(job, "summary", StatusRunning, 20, "聚合地址画像指标")
	})
	addressTypes := make(map[string]string, len(addresses))
	for _, address := range addresses {
		addressTypes[strings.ToLower(address)] = "UNKNOWN"
	}
	m.mu.RLock()
	managedRPC := m.rpcManager
	m.mu.RUnlock()
	if managedRPC != nil && managedRPC.HasConfigured(network.Key) {
		for _, address := range addresses {
			if enriched, enrichErr := managedRPC.Address(ctx, network.Key, address, false); enrichErr == nil {
				addressTypes[strings.ToLower(address)] = enriched.AddressType
			}
		}
	} else if endpoint := strings.TrimSpace(os.Getenv(network.RPCEnv)); endpoint != "" {
		if client, err := rpcsource.New(endpoint, nil); err == nil {
			if resolved, resolveErr := client.AddressTypes(ctx, network, addresses); resolveErr == nil {
				addressTypes = resolved
			}
		}
	}
	targetSink, err := newCSVSink(filepath.Join(tempDir, "summary_targets.csv"), []string{"address", "address_type"})
	if err != nil {
		return "", 0, err
	}
	for _, address := range addresses {
		address = strings.ToLower(address)
		if err := targetSink.writer.Write([]string{address, addressTypes[address]}); err != nil {
			_ = targetSink.close()
			return "", 0, err
		}
	}
	if err := targetSink.close(); err != nil {
		return "", 0, err
	}
	outputDir := filepath.Join(settings.DataRoot, "warehouse", "address_summary", "chain="+network.Key, "job="+jobID)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", 0, err
	}
	outputPath := filepath.Join(outputDir, "address-summary.parquet")
	sqlText := duckDBSettingsSQL(settings) + `;
COPY (
  WITH activity AS (
    SELECT * FROM read_parquet(` + sqlStringList(activityPaths) + `, union_by_name=true)
  ),
  aggregated AS (
    SELECT
      address,
      COUNT(DISTINCT tx_hash) AS tx_count,
      COUNT(DISTINCT CASE WHEN asset_type = 'TOKEN' THEN asset_address END) AS token_count,
      COUNT(DISTINCT CASE WHEN asset_type IN ('ERC721', 'ERC1155') THEN asset_address END) AS nft_count,
      COUNT(DISTINCT CASE WHEN activity_type IN ('CONTRACT_CALL', 'CONTRACT_CREATE', 'APPROVE', 'SWAP') THEN counterparty END) AS contract_count,
      MIN(block_time) AS first_active_time,
      MAX(block_time) AS last_active_time,
      COALESCE(SUM(CASE WHEN asset_type = 'NATIVE' AND direction = 'IN' THEN TRY_CAST(amount AS DECIMAL(38,18)) ELSE 0 END), 0) AS total_native_in,
      COALESCE(SUM(CASE WHEN asset_type = 'NATIVE' AND direction = 'OUT' THEN TRY_CAST(amount AS DECIMAL(38,18)) ELSE 0 END), 0) AS total_native_out,
      COUNT(DISTINCT nullif(counterparty, '')) AS unique_counterparty_count
    FROM activity
    GROUP BY address
  )
  SELECT
    ` + sqlString(network.Key) + ` AS chain_key,
    ` + strconv.FormatInt(network.ID, 10) + `::UBIGINT AS chain_id,
    lower(t.address) AS address,
    t.address_type,
    COALESCE(a.tx_count, 0)::UBIGINT AS tx_count,
    COALESCE(a.token_count, 0)::UBIGINT AS token_count,
    COALESCE(a.nft_count, 0)::UBIGINT AS nft_count,
    COALESCE(a.contract_count, 0)::UBIGINT AS contract_count,
    a.first_active_time,
    a.last_active_time,
    COALESCE(a.total_native_in, 0)::DECIMAL(38,18) AS total_native_in,
    COALESCE(a.total_native_out, 0)::DECIMAL(38,18) AS total_native_out,
    COALESCE(a.unique_counterparty_count, 0)::UBIGINT AS unique_counterparty_count
  FROM read_csv(` + sqlString(targetSink.path) + `, header=true, all_varchar=true) t
  LEFT JOIN aggregated a ON a.address = lower(t.address)
) TO ` + sqlString(outputPath) + ` (FORMAT PARQUET, COMPRESSION ZSTD);
SELECT COUNT(*) AS row_count FROM read_parquet(` + sqlString(outputPath) + `)`
	rows, err := m.engine.ExecSQLJSON(ctx, sqlText)
	if err != nil {
		return "", 0, fmt.Errorf("生成 address_summary: %w", err)
	}
	rowCount := int64(0)
	if len(rows) > 0 {
		rowCount = numberToInt64(rows[0]["row_count"])
	}
	m.mutate(jobID, func(job *Job) {
		setStage(job, "summary", StatusDone, 100, fmt.Sprintf("%d 个地址画像", rowCount))
	})
	return outputPath, rowCount, nil
}

func (m *Manager) writeSQDTransactionParquet(ctx context.Context, jobID string, settings Settings, network chain.EVM, csvPath string) (string, error) {
	outputs, err := m.writeSQDParquetSpecs(ctx, jobID, settings, network, []struct {
		table string
		file  string
		name  string
	}{{"transactions", csvPath, "transactions-sqd.parquet"}})
	if err != nil {
		return "", err
	}
	return outputs[0], nil
}

func (m *Manager) writeMethodSignatures(ctx context.Context, settings Settings, tempDir string) (string, error) {
	sink, err := newCSVSink(filepath.Join(tempDir, "method_signatures.csv"), []string{
		"chain_key", "chain_id", "method_id", "signature", "function_name", "category",
	})
	if err != nil {
		return "", err
	}
	for _, item := range normalize.MethodSignatures() {
		if err := sink.writer.Write([]string{"global", "0", item.MethodID, item.Signature, item.FunctionName, item.Category}); err != nil {
			_ = sink.close()
			return "", err
		}
	}
	if err := sink.close(); err != nil {
		return "", err
	}
	outputDir := filepath.Join(settings.DataRoot, "warehouse", "method_signatures")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}
	outputPath := filepath.Join(outputDir, "method-signatures.parquet")
	sqlText := duckDBSettingsSQL(settings) + "; COPY (" + sqdTypedSelect("method_signatures", sink.path) + ") TO " +
		sqlString(outputPath) + " (FORMAT PARQUET, COMPRESSION ZSTD)"
	if _, err := m.engine.ExecSQL(ctx, sqlText); err != nil {
		return "", fmt.Errorf("写入 method_signatures: %w", err)
	}
	return outputPath, nil
}

func (m *Manager) writeSQDLogParquet(ctx context.Context, jobID string, settings Settings, network chain.EVM, logsCSV, tokenCSV, nftCSV string) ([]string, error) {
	specs := []struct {
		table string
		file  string
		name  string
	}{
		{"logs", logsCSV, "logs.parquet"},
		{"token_transfers", tokenCSV, "token-transfers.parquet"},
		{"nft_transfers", nftCSV, "nft-transfers.parquet"},
	}
	return m.writeSQDParquetSpecs(ctx, jobID, settings, network, specs)
}

func (m *Manager) writeSQDTraceParquet(ctx context.Context, jobID string, settings Settings, network chain.EVM, traceCSV, internalCSV string) ([]string, error) {
	specs := []struct {
		table string
		file  string
		name  string
	}{
		{"traces", traceCSV, "traces.parquet"},
		{"internal_transactions", internalCSV, "internal-transactions.parquet"},
	}
	return m.writeSQDParquetSpecs(ctx, jobID, settings, network, specs)
}

func (m *Manager) writeSQDActivityParquet(ctx context.Context, jobID string, settings Settings, network chain.EVM, csvPath string) (string, error) {
	outputs, err := m.writeSQDParquetSpecs(ctx, jobID, settings, network, []struct {
		table string
		file  string
		name  string
	}{{"address_activity", csvPath, "address-activity-sqd.parquet"}})
	if err != nil {
		return "", err
	}
	return outputs[0], nil
}

func (m *Manager) writeSQDParquetSpecs(ctx context.Context, jobID string, settings Settings, network chain.EVM, specs []struct {
	table string
	file  string
	name  string
}) ([]string, error) {
	outputs := make([]string, 0, len(specs))
	for _, spec := range specs {
		outputDir := filepath.Join(settings.DataRoot, "warehouse", spec.table, "chain="+network.Key, "job="+jobID)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return nil, err
		}
		outputPath := filepath.Join(outputDir, spec.name)
		sqlText := duckDBSettingsSQL(settings) + "; COPY (" + sqdTypedSelect(spec.table, spec.file) + ") TO " + sqlString(outputPath) +
			" (FORMAT PARQUET, COMPRESSION ZSTD, ROW_GROUP_SIZE 250000)"
		if _, err := m.engine.ExecSQL(ctx, sqlText); err != nil {
			return nil, fmt.Errorf("写入 %s: %w", spec.table, err)
		}
		outputs = append(outputs, outputPath)
	}
	return outputs, nil
}

func sqdTypedSelect(table, csvPath string) string {
	source := "read_csv(" + sqlString(csvPath) + ", header=true, all_varchar=true, auto_detect=true)"
	switch table {
	case "transactions":
		return `SELECT lower(chain_key) AS chain_key, TRY_CAST(chain_id AS UBIGINT) AS chain_id,
lower(tx_hash) AS tx_hash, TRY_CAST(block_number AS UBIGINT) AS block_number,
TRY_CAST(block_time AS TIMESTAMPTZ) AS block_time, lower(from_address) AS from_address,
nullif(lower(to_address), '') AS to_address, value_raw, input, nullif(lower(method_id), '') AS method_id,
TRY_CAST(status AS UTINYINT) AS status, gas_used, gas_price, source FROM ` + source
	case "logs":
		return `SELECT lower(chain_key) AS chain_key, TRY_CAST(chain_id AS UBIGINT) AS chain_id,
TRY_CAST(block_number AS UBIGINT) AS block_number, TRY_CAST(block_time AS TIMESTAMPTZ) AS block_time,
lower(tx_hash) AS tx_hash, TRY_CAST(tx_index AS UINTEGER) AS tx_index,
TRY_CAST(log_index AS UINTEGER) AS log_index, lower(contract_address) AS contract_address,
topic0, topic1, topic2, topic3, data FROM ` + source
	case "token_transfers":
		return `SELECT lower(chain_key) AS chain_key, TRY_CAST(chain_id AS UBIGINT) AS chain_id,
lower(tx_hash) AS tx_hash, TRY_CAST(log_index AS UINTEGER) AS log_index,
TRY_CAST(block_number AS UBIGINT) AS block_number, TRY_CAST(block_time AS TIMESTAMPTZ) AS block_time,
lower(token_address) AS token_address, lower(from_address) AS from_address, lower(to_address) AS to_address,
amount_raw, nullif(amount, '') AS amount, nullif(symbol, '') AS symbol,
TRY_CAST(decimals AS UTINYINT) AS decimals, standard FROM ` + source
	case "token_metadata":
		return `SELECT lower(chain_key) AS chain_key, TRY_CAST(chain_id AS UBIGINT) AS chain_id,
lower(token_address) AS token_address, name, symbol, TRY_CAST(decimals AS UTINYINT) AS decimals,
standard, nullif(total_supply, '') AS total_supply, nullif(logo_url, '') AS logo_url,
TRY_CAST(updated_at AS TIMESTAMPTZ) AS updated_at, source FROM ` + source
	case "balances":
		return `SELECT lower(chain_key) AS chain_key, TRY_CAST(chain_id AS UBIGINT) AS chain_id,
lower(address) AS address, asset_type, nullif(lower(asset_address), '') AS asset_address,
balance_raw, nullif(balance, '') AS balance, TRY_CAST(snapshot_time AS TIMESTAMPTZ) AS snapshot_time,
source FROM ` + source
	case "method_signatures":
		return `SELECT lower(chain_key) AS chain_key, TRY_CAST(chain_id AS UBIGINT) AS chain_id,
lower(method_id) AS method_id, signature, function_name, category FROM ` + source
	case "nft_transfers":
		return `SELECT lower(chain_key) AS chain_key, TRY_CAST(chain_id AS UBIGINT) AS chain_id,
lower(tx_hash) AS tx_hash, TRY_CAST(log_index AS UINTEGER) AS log_index,
TRY_CAST(batch_index AS UINTEGER) AS batch_index, TRY_CAST(block_number AS UBIGINT) AS block_number,
TRY_CAST(block_time AS TIMESTAMPTZ) AS block_time, lower(contract_address) AS contract_address,
token_id, lower(from_address) AS from_address, lower(to_address) AS to_address, amount, standard FROM ` + source
	case "traces":
		return `SELECT lower(chain_key) AS chain_key, TRY_CAST(chain_id AS UBIGINT) AS chain_id,
lower(tx_hash) AS tx_hash, trace_id, TRY_CAST(trace_depth AS UINTEGER) AS trace_depth, TRY_CAST(block_number AS UBIGINT) AS block_number,
TRY_CAST(block_time AS TIMESTAMPTZ) AS block_time, lower(from_address) AS from_address,
lower(to_address) AS to_address, value_raw, call_type, input, output, status, nullif(error, '') AS error FROM ` + source
	case "internal_transactions":
		return `SELECT lower(chain_key) AS chain_key, TRY_CAST(chain_id AS UBIGINT) AS chain_id,
lower(tx_hash) AS tx_hash, trace_id, TRY_CAST(block_number AS UBIGINT) AS block_number,
TRY_CAST(block_time AS TIMESTAMPTZ) AS block_time, lower(from_address) AS from_address,
lower(to_address) AS to_address, value_raw, type FROM ` + source
	case "address_activity":
		return `SELECT lower(chain_key) AS chain_key, TRY_CAST(chain_id AS UBIGINT) AS chain_id,
lower(address) AS address, lower(counterparty) AS counterparty, direction, activity_type, asset_type,
nullif(lower(asset_address), '') AS asset_address, nullif(symbol, '') AS symbol, amount_raw, nullif(amount, '') AS amount,
lower(tx_hash) AS tx_hash, TRY_CAST(block_time AS TIMESTAMPTZ) AS block_time,
nullif(lower(method_id), '') AS method_id, TRY_CAST(trace_depth AS UINTEGER) AS trace_depth,
COALESCE(nullif(upper(status), ''), 'UNKNOWN') AS status, source FROM ` + source
	default:
		return "SELECT * FROM " + source
	}
}
