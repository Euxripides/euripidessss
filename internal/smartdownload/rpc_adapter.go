package smartdownload

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/etl/backend/internal/chain"
)

// RPCClient 复用 rpcmanager 的最小接口（与 downloadscheduler.RPCClient 一致）。
type RPCClient interface {
	Call(ctx context.Context, chainKey, method string, params any) (json.RawMessage, string, error)
}

// RPCTransferAdapter RPC Adapter（Phase 2）：
// 复用现有 rpcmanager，支持 balances（eth_getBalance）+ token_transfers（eth_getLogs 恢复通道）。
// 不重写下载器；定位为 SQD 不可用时的 Range 级接管。
type RPCTransferAdapter struct {
	client RPCClient
}

// NewRPCTransferAdapter 创建 RPC Adapter。
func NewRPCTransferAdapter(client RPCClient) *RPCTransferAdapter {
	return &RPCTransferAdapter{client: client}
}

// NewRPCBalanceAdapter 兼容别名（Phase 1 名称保留）。
func NewRPCBalanceAdapter(client RPCClient) *RPCTransferAdapter {
	return NewRPCTransferAdapter(client)
}

func (p *RPCTransferAdapter) Name() string    { return "rpc" }
func (p *RPCTransferAdapter) Available() bool { return p.client != nil }
func (p *RPCTransferAdapter) Supports(d string) bool {
	return d == DatasetBalances || d == DatasetTokenTransfers
}

// Probe 余额固定 1 行；token_transfers 用 ≤200 块采样外推（低成本，不完整扫描）。
func (p *RPCTransferAdapter) Probe(ctx context.Context, req ProbeRequest) (ProbeResult, error) {
	switch req.Dataset {
	case DatasetBalances:
		return ProbeResult{EstimatedRows: 1, EstimatedBytes: 128, Confidence: 0.9}, nil
	case DatasetTokenTransfers:
		from, to := probeRange(req)
		count, err := p.countLogs(ctx, req.ChainKey, req.Address, from, to)
		if err != nil {
			return ProbeResult{Confidence: 0}, nil // 探测失败不阻断
		}
		return extrapolate(uint64(count), to-from+1, probeBlockSpan(req), 0.6), nil
	default:
		return ProbeResult{Confidence: 0}, nil
	}
}

// ExecuteRange 执行单个 Range。
func (p *RPCTransferAdapter) ExecuteRange(ctx context.Context, req RangeRequest) (*ProviderResult, error) {
	if p.client == nil {
		return nil, fmt.Errorf("RPC 管理器未装配")
	}
	network, err := chain.Resolve(req.ChainKey)
	if err != nil {
		return nil, err
	}
	switch req.Dataset {
	case DatasetBalances:
		return p.executeBalance(ctx, network, req)
	case DatasetTokenTransfers:
		return p.executeTransfers(ctx, network, req)
	default:
		return nil, fmt.Errorf("RPC Adapter 不支持数据集 %s", req.Dataset)
	}
}

func (p *RPCTransferAdapter) executeBalance(ctx context.Context, network chain.EVM, req RangeRequest) (*ProviderResult, error) {
	raw, _, err := p.client.Call(ctx, network.Key, "eth_getBalance", []any{strings.ToLower(req.Address), "latest"})
	if err != nil {
		return nil, fmt.Errorf("eth_getBalance 失败: %w", err)
	}
	var hexBalance string
	if err := json.Unmarshal(raw, &hexBalance); err != nil {
		return nil, fmt.Errorf("解析余额响应: %w", err)
	}
	wei := new(big.Int)
	if _, ok := wei.SetString(strings.TrimPrefix(hexBalance, "0x"), 16); !ok {
		return nil, fmt.Errorf("余额不是合法十六进制: %q", truncate(hexBalance))
	}
	payload := map[string]any{
		"address": strings.ToLower(req.Address),
		"balance": wei.String(),
		"symbol":  network.NativeSymbol,
	}
	return &ProviderResult{
		Records: []Record{{
			ChainID:     network.ID,
			BlockNumber: 0,
			Dataset:     DatasetBalances,
			Address:     strings.ToLower(req.Address),
			Payload:     payload,
		}},
		Bytes:       uint64(len(wei.String()) + 64),
		CompletedTo: req.ToBlock,
	}, nil
}

// ── eth_getLogs Token Transfer 恢复通道 ──

const (
	transferTopic     = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	rpcLogChunkBlocks = 50_000
	rpcLogResultLimit = 9_000
)

type rpcLog struct {
	Address         string   `json:"address"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	BlockNumber     string   `json:"blockNumber"`
	TransactionHash string   `json:"transactionHash"`
	LogIndex        string   `json:"logIndex"`
}

func (p *RPCTransferAdapter) executeTransfers(ctx context.Context, network chain.EVM, req RangeRequest) (*ProviderResult, error) {
	var records []Record
	seen := map[string]bool{}
	for from := req.FromBlock; from <= req.ToBlock; from += rpcLogChunkBlocks + 1 {
		to := from + rpcLogChunkBlocks
		if to > req.ToBlock {
			to = req.ToBlock
		}
		logs, err := p.getLogsChunk(ctx, network.Key, req.Address, from, to)
		if err != nil {
			return nil, fmt.Errorf("RPC eth_getLogs（%d-%d）: %w", from, to, err)
		}
		for _, item := range logs {
			t, ok := parseTransferLog(network, item)
			if !ok {
				continue
			}
			key := t.UniqueKey()
			if seen[key] {
				continue
			}
			seen[key] = true
			records = append(records, t)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	bytes := uint64(len(records) * 160)
	return &ProviderResult{Records: records, Bytes: bytes, CompletedTo: req.ToBlock}, nil
}

func (p *RPCTransferAdapter) countLogs(ctx context.Context, chainKey, address string, from, to uint64) (int, error) {
	logs, err := p.getLogsChunk(ctx, chainKey, address, from, to)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, l := range logs {
		if len(l.Topics) == 3 && strings.EqualFold(l.Topics[0], transferTopic) {
			count++
		}
	}
	return count, nil
}

// getLogsChunk 查询区块分块；结果触顶时二分收窄。
func (p *RPCTransferAdapter) getLogsChunk(ctx context.Context, chainKey, address string, from, to uint64) ([]rpcLog, error) {
	logs, err := p.getLogsRange(ctx, chainKey, address, from, to)
	if err != nil || len(logs) < rpcLogResultLimit {
		return logs, err
	}
	if from == to {
		return logs, nil
	}
	mid := from + (to-from)/2
	left, errL := p.getLogsChunk(ctx, chainKey, address, from, mid)
	if errL != nil {
		return nil, errL
	}
	right, errR := p.getLogsChunk(ctx, chainKey, address, mid+1, to)
	if errR != nil {
		return nil, errR
	}
	return append(left, right...), nil
}

func (p *RPCTransferAdapter) getLogsRange(ctx context.Context, chainKey, address string, from, to uint64) ([]rpcLog, error) {
	filter := map[string]any{
		"address":   []string{strings.ToLower(address)},
		"topics":    []string{transferTopic},
		"fromBlock": fmt.Sprintf("0x%x", from),
		"toBlock":   fmt.Sprintf("0x%x", to),
	}
	raw, _, err := p.client.Call(ctx, chainKey, "eth_getLogs", []any{filter})
	if err != nil {
		return nil, err
	}
	var logs []rpcLog
	if err := json.Unmarshal(raw, &logs); err != nil {
		return nil, fmt.Errorf("解析 eth_getLogs 响应: %w", err)
	}
	return logs, nil
}

// parseTransferLog 解析 Transfer(address,address,uint256)。
func parseTransferLog(network chain.EVM, item rpcLog) (Record, bool) {
	if len(item.Topics) != 3 || !strings.EqualFold(item.Topics[0], transferTopic) {
		return Record{}, false
	}
	from, ok := topicAddress(item.Topics[1])
	if !ok {
		return Record{}, false
	}
	to, ok := topicAddress(item.Topics[2])
	if !ok {
		return Record{}, false
	}
	value, ok := dataWord(item.Data)
	if !ok {
		return Record{}, false
	}
	blockNumber, ok := hexToUint64(item.BlockNumber)
	if !ok {
		return Record{}, false
	}
	logIndex, ok := hexToUint64(item.LogIndex)
	if !ok {
		return Record{}, false
	}
	if item.TransactionHash == "" || item.Address == "" {
		return Record{}, false
	}
	standard := "ERC20"
	if network.Key == "bsc" {
		standard = "BEP20"
	}
	return Record{
		ChainID:         network.ID,
		BlockNumber:     blockNumber,
		TransactionHash: strings.ToLower(item.TransactionHash),
		LogIndex:        logIndex,
		Dataset:         DatasetTokenTransfers,
		Address:         strings.ToLower(item.Address),
		Payload: map[string]any{
			"token_address": strings.ToLower(item.Address),
			"from_address":  from,
			"to_address":    to,
			"value_raw":     value,
			"standard":      standard,
		},
	}, true
}

func topicAddress(topic string) (string, bool) {
	clean := strings.TrimPrefix(strings.ToLower(topic), "0x")
	if len(clean) != 64 {
		return "", false
	}
	return "0x" + clean[24:], true
}

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

func truncate(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
