package downloadscheduler

// RPC Recovery Provider（Token Transfer Multi-Provider Recovery Layer V1.0 §6）：
//   - eth_getLogs 按地址过滤 Transfer(address,address,uint256)（topic0 0xddf252ad…）
//   - 地址分批 ≤100/次、区块分块 ≤50,000/次，结果超限自动二分收窄
//   - 输出与 SQD token_transfers 对齐：transaction_hash / log_index / token_address / from / to / value / block_number
//   - 定位：增量补充/实时查询（最近窗口），历史大批量由 SQD/AWS 承担（设计 §12）

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/logger"
	"github.com/etl/backend/internal/normalize"
)

const (
	// TransferTopic ERC20/BEP20 Transfer(address,address,uint256) 事件签名。
	TransferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	// rpcAddrBatch 单次 eth_getLogs 地址数上限（公共节点限制）。
	rpcAddrBatch = 100
	// rpcBlockChunk 单次 eth_getLogs 区块范围上限（BSC ≈ 1.7 天）。
	rpcBlockChunk = 50_000
	// rpcLogResultLimit 接近节点 10,000 条结果上限时触发二分。
	rpcLogResultLimit = 9_000
	// rpcDefaultWindowDays 未指定日期时的默认恢复窗口。
	rpcDefaultWindowDays = 90.0
	// rpcMaxWindowDays 恢复窗口上限（历史数据走 SQD/AWS）。
	rpcMaxWindowDays = 180.0
)

// chainBlockTimeSec 各链平均出块时间（秒），用于日期→区块估算（保守取小值放大窗口）。
var chainBlockTimeSec = map[string]float64{
	"bsc": 3, "eth": 12, "base": 2, "arbitrum": 1,
}

// errLogResultLimit 表示单次查询结果触及节点上限，需要二分收窄区块范围。
var errLogResultLimit = errors.New("eth_getLogs 结果超限，需要二分收窄")

// recoveryUniqueKeyDesc 唯一键描述（设计 §10），与 parquetdownload.recoveryUniqueKey 一致。
const recoveryUniqueKeyDesc = "chain_id + transaction_hash + log_index + token_address"

// rpcLog eth_getLogs 响应条目。
type rpcLog struct {
	Address         string   `json:"address"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	BlockNumber     string   `json:"blockNumber"`
	TransactionHash string   `json:"transactionHash"`
	LogIndex        string   `json:"logIndex"`
}

// executeTokenTransfer RPC 恢复通道：按地址分批 + 区块分块查询 Transfer 事件，
// 解析后按唯一键去重并落盘（RecoveryWriter），供 MERGING 阶段合并。
func (p *RPCProvider) executeTokenTransfer(ctx context.Context, req Requirement) (*TaskResult, error) {
	if p.client == nil {
		return nil, errors.New("RPC Provider 未装配（rpcmanager 不可用）")
	}
	if p.writer == nil {
		return nil, errors.New("RPC 恢复写入器未装配（Parquet 下载管理器不可用）")
	}
	if len(req.Addresses) == 0 {
		return nil, errors.New("token_transfer 需求缺少地址")
	}
	network, err := chain.Resolve(req.ChainKey)
	if err != nil {
		return nil, fmt.Errorf("未知链: %w", err)
	}
	latest, err := rpcBlockNumber(ctx, p.client, network.Key)
	if err != nil {
		return nil, fmt.Errorf("获取最新区块: %w", err)
	}
	from, to, windowNote := recoveryWindow(req, latest, network.Key)
	// 逐地址批查询 + 逐批落盘（每批 ≤100 地址），控制内存峰值；
	// 跨批唯一键去重（seen 全局），批目录 {taskKey}-b{n} 由 MERGING 统一合并。
	var totalRows int64
	var totalParts int
	seen := make(map[string]bool)
	for i := 0; i < len(req.Addresses); i += rpcAddrBatch {
		end := min(i+rpcAddrBatch, len(req.Addresses))
		batch := req.Addresses[i:end]
		var rows []normalize.TokenTransfer
		for blockFrom := from; blockFrom <= to; blockFrom += rpcBlockChunk + 1 {
			blockTo := min(blockFrom+rpcBlockChunk, to)
			logs, chunkErr := p.getLogsChunk(ctx, network.Key, batch, blockFrom, blockTo)
			if chunkErr != nil {
				return nil, fmt.Errorf("RPC 恢复查询失败（区块 %d-%d，地址 %d 个）: %w", blockFrom, blockTo, len(batch), chunkErr)
			}
			for _, item := range logs {
				transfer, ok := parseTransferLog(network, item)
				if !ok {
					continue
				}
				key := tokenTransferKey(network.ID, transfer.TxHash, transfer.LogIndex, transfer.TokenAddress)
				if seen[key] {
					continue
				}
				seen[key] = true
				rows = append(rows, transfer)
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if len(rows) > 0 {
			totalParts++
			written, writeErr := p.writer.WriteTokenTransfers(ctx, fmt.Sprintf("%s-b%d", req.ID, totalParts), network, rows)
			if writeErr != nil {
				return nil, fmt.Errorf("RPC 恢复数据落盘失败（批次 %d）: %w", totalParts, writeErr)
			}
			totalRows += written.Rows
			logger.Log.Info().Str("task", req.ID).Str("chain", network.Key).Int("part", totalParts).
				Int64("rows", written.Rows).Str("parquet", written.ParquetPath).Msg("scheduler_rpc_recovery_part_written")
		}
	}
	if totalRows == 0 {
		return nil, fmt.Errorf("RPC 恢复未获取到 Token Transfer 事件（%s 窗口内无数据）", windowNote)
	}
	return &TaskResult{
		Output: fmt.Sprintf("recovery %d parts", totalParts),
		Summary: fmt.Sprintf("RPC 恢复成功：%d 条 Token Transfer（%s，%d 个批次），已落盘唯一化 Parquet（唯一键 %s）",
			totalRows, windowNote, totalParts, recoveryUniqueKeyDesc),
		Rows:    totalRows,
		NewData: true,
	}, nil
}

// getLogsChunk 查询一个区块分块；仅当结果触及节点上限（errLogResultLimit 或 ≥limit 行）时二分收窄。
// 其他错误（节点不可用/超时等）原样返回，不放大失败调用。
func (p *RPCProvider) getLogsChunk(ctx context.Context, chainKey string, addrs []string, from, to uint64) ([]rpcLog, error) {
	logs, err := p.getLogsRange(ctx, chainKey, addrs, from, to)
	overflow := err == errLogResultLimit || (err == nil && len(logs) >= rpcLogResultLimit)
	if !overflow {
		return logs, err
	}
	if from == to {
		return nil, fmt.Errorf("单区块日志仍达 %d 条（节点结果上限），无法收窄", len(logs))
	}
	mid := from + (to-from)/2
	left, errLeft := p.getLogsChunk(ctx, chainKey, addrs, from, mid)
	if errLeft != nil {
		return nil, errLeft
	}
	right, errRight := p.getLogsChunk(ctx, chainKey, addrs, mid+1, to)
	if errRight != nil {
		return nil, errRight
	}
	return append(left, right...), nil
}

// getLogsRange 单次 eth_getLogs 调用。
func (p *RPCProvider) getLogsRange(ctx context.Context, chainKey string, addrs []string, from, to uint64) ([]rpcLog, error) {
	filter := map[string]any{
		"address":   addrs,
		"topics":    []string{TransferTopic},
		"fromBlock": fmt.Sprintf("0x%x", from),
		"toBlock":   fmt.Sprintf("0x%x", to),
	}
	raw, _, err := p.client.Call(ctx, chainKey, "eth_getLogs", []any{filter})
	if err != nil {
		if isResultLimitError(err) {
			return nil, errLogResultLimit
		}
		return nil, err
	}
	var logs []rpcLog
	if err := json.Unmarshal(raw, &logs); err != nil {
		return nil, fmt.Errorf("解析 eth_getLogs 响应: %w", err)
	}
	if len(logs) >= rpcLogResultLimit {
		return logs, errLogResultLimit
	}
	return logs, nil
}

// isResultLimitError 识别节点"结果超限"类错误（文本因节点而异）。
func isResultLimitError(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"10000", "1000 results", "result limit", "too many results", "query returned more than", "logs count exceeds", "limit exceeded"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// parseTransferLog 解析单条 Transfer(address,address,uint256) 日志：
// topics[1]→from、topics[2]→to、data 前 32 字节→value、address→token。
func parseTransferLog(network chain.EVM, item rpcLog) (normalize.TokenTransfer, bool) {
	if len(item.Topics) != 3 || !strings.EqualFold(item.Topics[0], TransferTopic) {
		return normalize.TokenTransfer{}, false
	}
	from, ok := topicAddress(item.Topics[1])
	if !ok {
		return normalize.TokenTransfer{}, false
	}
	to, ok := topicAddress(item.Topics[2])
	if !ok {
		return normalize.TokenTransfer{}, false
	}
	amount, ok := dataWord(item.Data)
	if !ok {
		return normalize.TokenTransfer{}, false
	}
	blockNumber, ok := hexToUint64(item.BlockNumber)
	if !ok {
		return normalize.TokenTransfer{}, false
	}
	// logIndex 解析失败视为坏行（吞错为 0 会让同 tx+token 的不同日志在唯一键上坍缩，造成数据丢失）
	logIndex, ok := hexToUint64(item.LogIndex)
	if !ok {
		return normalize.TokenTransfer{}, false
	}
	if item.TransactionHash == "" || item.Address == "" {
		return normalize.TokenTransfer{}, false
	}
	standard := "ERC20"
	if network.Key == "bsc" {
		standard = "BEP20"
	}
	return normalize.TokenTransfer{
		ChainKey:     network.Key,
		ChainID:      network.ID,
		TxHash:       strings.ToLower(item.TransactionHash),
		LogIndex:     logIndex,
		BlockNumber:  blockNumber,
		TokenAddress: strings.ToLower(item.Address),
		FromAddress:  from,
		ToAddress:    to,
		AmountRaw:    amount,
		Standard:     standard,
	}, true
}

// tokenTransferKey 唯一键（设计 §10）：chain_id + transaction_hash + log_index + token_address。
func tokenTransferKey(chainID int64, txHash string, logIndex uint64, tokenAddress string) string {
	return fmt.Sprintf("%d|%s|%d|%s", chainID, strings.ToLower(txHash), logIndex, strings.ToLower(tokenAddress))
}

// rpcBlockNumber 查询最新区块号。
func rpcBlockNumber(ctx context.Context, client RPCClient, chainKey string) (uint64, error) {
	raw, _, err := client.Call(ctx, chainKey, "eth_blockNumber", []any{})
	if err != nil {
		return 0, err
	}
	var hexNumber string
	if err := json.Unmarshal(raw, &hexNumber); err != nil {
		return 0, fmt.Errorf("解析 eth_blockNumber 响应: %w", err)
	}
	number, ok := hexToUint64(hexNumber)
	if !ok {
		return 0, errors.New("eth_blockNumber 返回无效区块号")
	}
	return number, nil
}

// recoveryWindow 计算恢复窗口：显式日期范围 → 按链出块时间估算区块数；
// 未指定或超上限时收敛到最近窗口（RPC 定位为增量补充，历史数据走 SQD/AWS）。
func recoveryWindow(req Requirement, latest uint64, chainKey string) (from, to uint64, note string) {
	to = latest
	days := rpcDefaultWindowDays
	if req.StartDate != "" && req.EndDate != "" {
		if start, err := time.Parse("2006-01-02", req.StartDate); err == nil {
			if end, err2 := time.Parse("2006-01-02", req.EndDate); err2 == nil && !end.Before(start) {
				days = end.Sub(start).Hours()/24 + 1
			}
		}
	}
	days = math.Min(days, rpcMaxWindowDays)
	blockTime := chainBlockTimeSec[chainKey]
	if blockTime <= 0 {
		blockTime = 12
	}
	span := uint64(days * 86400 / blockTime)
	if span > latest {
		span = latest
	}
	from = latest - span
	note = fmt.Sprintf("区块 %d-%d（约最近 %.0f 天）", from, latest, days)
	return from, to, note
}

// topicAddress 从 32 字节 topic 提取地址（取后 40 个 hex 字符）。
func topicAddress(topic string) (string, bool) {
	clean := strings.TrimPrefix(strings.ToLower(topic), "0x")
	if len(clean) != 64 {
		return "", false
	}
	address := clean[24:]
	return "0x" + address, true
}

// dataWord 解析 data 首字（32 字节 uint256）为十进制字符串。
func dataWord(data string) (string, bool) {
	clean := strings.TrimPrefix(data, "0x")
	if len(clean) < 2 {
		return "", false
	}
	if len(clean) > 64 {
		clean = clean[:64]
	}
	value := new(big.Int)
	if _, ok := value.SetString(clean, 16); !ok {
		return "", false
	}
	return value.String(), true
}

// hexToUint64 解析 0x 前缀十六进制为 uint64。
func hexToUint64(hex string) (uint64, bool) {
	clean := strings.TrimPrefix(strings.ToLower(hex), "0x")
	if clean == "" {
		return 0, false
	}
	value := new(big.Int)
	if _, ok := value.SetString(clean, 16); !ok || !value.IsUint64() {
		return 0, false
	}
	return value.Uint64(), true
}
