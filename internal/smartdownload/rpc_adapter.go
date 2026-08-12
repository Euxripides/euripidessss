package smartdownload

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/rpcmanager"
)

// RPCClient 复用 rpcmanager 的最小接口（与 downloadscheduler.RPCClient 一致）。
type RPCClient interface {
	Call(ctx context.Context, chainKey, method string, params any) (json.RawMessage, string, error)
}

type turboRPCClient interface {
	CallTurbo(ctx context.Context, chainKey, method string, params any) (json.RawMessage, string, error)
	HasAnyConfigured(chainKey string) bool
}

type turboRPCContextKey struct{}

// RPCTransferAdapter RPC Adapter（Phase 2）：
// 复用现有 rpcmanager，支持 balances（eth_getBalance）+ token_transfers（eth_getLogs 恢复通道）。
// 不重写下载器；定位为 SQD 不可用时的 Range 级接管。
type RPCTransferAdapter struct {
	client RPCClient
}

var _ RPCPoolMetricsSource = (*RPCTransferAdapter)(nil)
var _ GroupProviderAdapter = (*RPCTransferAdapter)(nil)

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
func (p *RPCTransferAdapter) AvailableForChain(chainKey string) bool {
	if p.client == nil {
		return false
	}
	// Runtime routing health is authoritative when the client exposes it. A
	// merely configured endpoint may be misconfigured, rate-limited, or behind
	// an open circuit and therefore must not make AUTO advertise executable RPC.
	if available, ok := p.client.(interface{ HasAvailable(string) bool }); ok {
		return available.HasAvailable(chainKey)
	}
	if configured, ok := p.client.(interface{ HasConfigured(string) bool }); ok {
		return configured.HasConfigured(chainKey)
	}
	return true
}
func (p *RPCTransferAdapter) AvailableForMode(chainKey string, mode DownloadMode) bool {
	if isTurboMode(mode) {
		if available, ok := p.client.(interface{ HasTurboAvailable(string) bool }); ok {
			return available.HasTurboAvailable(chainKey)
		}
		if configured, ok := p.client.(turboRPCClient); ok {
			return configured.HasAnyConfigured(chainKey)
		}
	}
	return p.AvailableForChain(chainKey)
}

// SmartDownloadRPCPoolSnapshot adapts rpcmanager's non-secret runtime snapshot
// to the narrow V3.1 allocator DTO. Handlers can register this adapter itself
// as Service.SetRPCPoolMetricsSource without exposing endpoint URLs.
func (p *RPCTransferAdapter) SmartDownloadRPCPoolSnapshot(chainKey string) (RPCPoolMetrics, error) {
	source, ok := p.client.(interface {
		PoolSnapshot(string) (rpcmanager.PoolSnapshot, error)
	})
	if !ok {
		return RPCPoolMetrics{}, fmt.Errorf("RPC client does not expose PoolSnapshot")
	}
	snapshot, err := source.PoolSnapshot(chainKey)
	if err != nil {
		return RPCPoolMetrics{}, err
	}
	out := RPCPoolMetrics{Endpoints: make([]RPCEndpointMetrics, 0, len(snapshot.Endpoints))}
	for _, endpoint := range snapshot.Endpoints {
		out.Endpoints = append(out.Endpoints, RPCEndpointMetrics{
			Name: endpoint.Provider, LatencyMillis: endpoint.LatencyMS,
			SuccessRate: endpoint.SuccessRate, Rate429: endpoint.Rate429,
			TimeoutRate: endpoint.TimeoutRate, CurrentWorkers: endpoint.CurrentWorkers,
			SupportedMethods:  append([]string(nil), endpoint.SupportedMethods...),
			ArchiveCapability: endpoint.ArchiveCapability, TraceCapability: endpoint.TraceCapability,
		})
	}
	return out, nil
}
func (p *RPCTransferAdapter) Supports(d string) bool {
	return d == DatasetBalances || d == DatasetTokenTransfers || d == DatasetLogs
}

// MaxAddressGroupSize reports the fail-closed RPC eth_getLogs group limit.
func (p *RPCTransferAdapter) MaxAddressGroupSize(dataset string) int {
	if dataset == DatasetTokenTransfers || dataset == DatasetLogs {
		return 100
	}
	return 0
}

// SupportedDatasetBundles returns defensive copies of the RPC group bundles.
// Token transfers and contract logs use different eth_getLogs semantics, so a
// combined bundle is executed as separate provider queries and merged only
// after each dataset has been assigned to the correct requested address.
func (p *RPCTransferAdapter) SupportedDatasetBundles() [][]string {
	return [][]string{
		{DatasetTokenTransfers},
		{DatasetLogs},
		{DatasetTokenTransfers, DatasetLogs},
	}
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
	case DatasetLogs:
		from, to := probeRange(req)
		logs, err := p.getAllLogsChunk(ctx, req.ChainKey, req.Address, from, to)
		if err != nil {
			return ProbeResult{Confidence: 0}, nil
		}
		return extrapolate(uint64(len(logs)), to-from+1, probeBlockSpan(req), 0.6), nil
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
	if isTurboMode(req.Mode) {
		ctx = context.WithValue(ctx, turboRPCContextKey{}, true)
	}
	switch req.Dataset {
	case DatasetBalances:
		return p.executeBalance(ctx, network, req)
	case DatasetTokenTransfers:
		return p.executeTransfers(ctx, network, req)
	case DatasetLogs:
		return p.executeLogs(ctx, network, req)
	default:
		return nil, fmt.Errorf("RPC Adapter 不支持数据集 %s", req.Dataset)
	}
}

// ExecuteGroupRange scans one normalized address group per block chunk. Token
// transfers match wallet participation through indexed from/to topics, while
// raw logs match contracts through the EVM log address. It intentionally does
// not fall back to per-address calls: invalid bundles fail before provider I/O.
func (p *RPCTransferAdapter) ExecuteGroupRange(ctx context.Context, req GroupRangeRequest) (map[string]map[string]*ProviderResult, error) {
	if p.client == nil {
		return nil, fmt.Errorf("RPC 管理器未装配")
	}
	if req.FromBlock > req.ToBlock {
		return nil, fmt.Errorf("RPC Group Adapter 区块范围非法: %d-%d", req.FromBlock, req.ToBlock)
	}
	addresses, err := normalizeAndValidateRPCLogAddresses(req.Addresses)
	if err != nil {
		return nil, err
	}
	if len(addresses) > p.MaxAddressGroupSize(DatasetLogs) {
		return nil, fmt.Errorf("RPC Group Adapter 地址数 %d 超过上限 %d", len(addresses), p.MaxAddressGroupSize(DatasetLogs))
	}
	datasets, err := normalizeRPCDatasetBundle(req.Datasets)
	if err != nil {
		return nil, err
	}
	network, err := chain.Resolve(req.ChainKey)
	if err != nil {
		return nil, err
	}
	if req.ChainID != 0 && req.ChainID != network.ID {
		return nil, fmt.Errorf("RPC Group Adapter chain_id %d 与 %s(%d) 不匹配", req.ChainID, network.Key, network.ID)
	}
	if isTurboMode(req.Mode) {
		ctx = context.WithValue(ctx, turboRPCContextKey{}, true)
	}
	return p.executeGroupedLogScan(ctx, network, addresses, datasets, req.FromBlock, req.ToBlock)
}

func (p *RPCTransferAdapter) executeLogs(ctx context.Context, network chain.EVM, req RangeRequest) (*ProviderResult, error) {
	var records []Record
	seen := map[string]bool{}
	times := map[uint64]int64{}
	for from := req.FromBlock; from <= req.ToBlock; from += rpcLogChunkBlocks + 1 {
		to := from + rpcLogChunkBlocks
		if to > req.ToBlock {
			to = req.ToBlock
		}
		logs, err := p.getAllLogsChunk(ctx, network.Key, req.Address, from, to)
		if err != nil {
			return nil, fmt.Errorf("RPC eth_getLogs raw（%d-%d）: %w", from, to, err)
		}
		for _, item := range logs {
			block, okBlock := hexToUint64(item.BlockNumber)
			index, okIndex := hexToUint64(item.LogIndex)
			if !okBlock || !okIndex || item.TransactionHash == "" || item.Address == "" || len(item.Topics) == 0 {
				continue
			}
			blockTime, ok := times[block]
			if !ok {
				blockTime, err = p.blockTimestamp(ctx, network.Key, block)
				if err != nil {
					return nil, err
				}
				times[block] = blockTime
			}
			record := Record{ChainID: network.ID, BlockNumber: block, BlockTime: blockTime, TransactionHash: strings.ToLower(item.TransactionHash), LogIndex: index, Dataset: DatasetLogs, Address: strings.ToLower(item.Address), Payload: map[string]any{"contract_address": strings.ToLower(item.Address), "topics": item.Topics, "data": item.Data}}
			key := record.UniqueKey()
			if seen[key] {
				continue
			}
			seen[key] = true
			records = append(records, record)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return &ProviderResult{Records: records, Bytes: uint64(len(records) * 256), CompletedTo: req.ToBlock}, nil
}

func (p *RPCTransferAdapter) blockTimestamp(ctx context.Context, chainKey string, block uint64) (int64, error) {
	raw, _, err := p.call(ctx, chainKey, "eth_getBlockByNumber", []any{fmt.Sprintf("0x%x", block), false})
	if err != nil {
		return 0, fmt.Errorf("eth_getBlockByNumber %d: %w", block, err)
	}
	var result struct {
		Timestamp string `json:"timestamp"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return 0, fmt.Errorf("parse block timestamp")
	}
	value, ok := hexToUint64(result.Timestamp)
	if !ok {
		return 0, fmt.Errorf("invalid block timestamp")
	}
	return int64(value), nil
}

func (p *RPCTransferAdapter) executeBalance(ctx context.Context, network chain.EVM, req RangeRequest) (*ProviderResult, error) {
	raw, _, err := p.call(ctx, network.Key, "eth_getBalance", []any{strings.ToLower(req.Address), "latest"})
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

// executeGroupedLogScan keeps the two address meanings separate: token
// transfers are assigned by indexed participant topics, while raw logs are
// assigned by the emitting contract address. Results are keyed by the original
// requested address and dataset.
func (p *RPCTransferAdapter) executeGroupedLogScan(ctx context.Context, network chain.EVM, addresses, datasets []string, fromBlock, toBlock uint64) (map[string]map[string]*ProviderResult, error) {
	var err error
	addresses, err = normalizeAndValidateRPCLogAddresses(addresses)
	if err != nil {
		return nil, err
	}
	if len(addresses) > p.MaxAddressGroupSize(DatasetLogs) {
		return nil, fmt.Errorf("RPC Group Adapter 地址数 %d 超过上限 %d", len(addresses), p.MaxAddressGroupSize(DatasetLogs))
	}
	datasets, err = normalizeRPCDatasetBundle(datasets)
	if err != nil {
		return nil, err
	}
	wantTransfers := containsRPCGroupDataset(datasets, DatasetTokenTransfers)
	wantLogs := containsRPCGroupDataset(datasets, DatasetLogs)

	results := make(map[string]map[string]*ProviderResult, len(addresses))
	for _, address := range addresses {
		results[address] = make(map[string]*ProviderResult, 2)
		if wantTransfers {
			results[address][DatasetTokenTransfers] = &ProviderResult{CompletedTo: toBlock}
		}
		if wantLogs {
			results[address][DatasetLogs] = &ProviderResult{CompletedTo: toBlock}
		}
	}
	seenTransfers := make(map[string]map[string]struct{}, len(addresses))
	seenLogs := make(map[string]map[string]struct{}, len(addresses))
	times := make(map[uint64]int64)

	for chunkFrom := fromBlock; chunkFrom <= toBlock; {
		chunkTo := toBlock
		if toBlock-chunkFrom > rpcLogChunkBlocks {
			chunkTo = chunkFrom + rpcLogChunkBlocks
		}
		if wantTransfers {
			logs, err := p.getTransferLogsChunkForAddresses(ctx, network.Key, addresses, chunkFrom, chunkTo)
			if err != nil {
				return nil, fmt.Errorf("RPC eth_getLogs transfer group（%d-%d）: %w", chunkFrom, chunkTo, err)
			}
			for _, item := range logs {
				transfer, ok := parseTransferLog(network, item)
				if !ok {
					continue
				}
				fromAddress, _ := transfer.Payload["from_address"].(string)
				toAddress, _ := transfer.Payload["to_address"].(string)
				for _, participant := range []string{fromAddress, toAddress} {
					addressResults, belongsToGroup := results[participant]
					if !belongsToGroup {
						continue
					}
					if appendGroupedRecord(addressResults[DatasetTokenTransfers], transfer, seenTransfers, participant) {
						addressResults[DatasetTokenTransfers].Bytes += 160
					}
				}
			}
		}
		if wantLogs {
			logs, err := p.getAllLogsChunkForAddresses(ctx, network.Key, addresses, chunkFrom, chunkTo)
			if err != nil {
				return nil, fmt.Errorf("RPC eth_getLogs contract group（%d-%d）: %w", chunkFrom, chunkTo, err)
			}
			for _, item := range logs {
				address := strings.ToLower(item.Address)
				addressResults, belongsToGroup := results[address]
				if !belongsToGroup {
					continue
				}
				record, ok, err := p.parseRawLogRecord(ctx, network, item, times)
				if err != nil {
					return nil, err
				}
				if ok && appendGroupedRecord(addressResults[DatasetLogs], record, seenLogs, address) {
					addressResults[DatasetLogs].Bytes += 256
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if chunkTo == toBlock {
			break
		}
		chunkFrom = chunkTo + 1
	}
	return results, nil
}

func normalizeRPCDatasetBundle(datasets []string) ([]string, error) {
	if len(datasets) == 0 {
		return nil, fmt.Errorf("RPC Group Adapter 数据集组为空")
	}
	normalized := make([]string, 0, len(datasets))
	seen := make(map[string]struct{}, len(datasets))
	for _, dataset := range datasets {
		dataset = strings.ToLower(strings.TrimSpace(dataset))
		if dataset != DatasetTokenTransfers && dataset != DatasetLogs {
			return nil, fmt.Errorf("RPC Group Adapter 不支持数据集 %q", dataset)
		}
		if _, exists := seen[dataset]; exists {
			return nil, fmt.Errorf("RPC Group Adapter 数据集重复: %s", dataset)
		}
		seen[dataset] = struct{}{}
		normalized = append(normalized, dataset)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func containsRPCGroupDataset(datasets []string, wanted string) bool {
	index := sort.SearchStrings(datasets, wanted)
	return index < len(datasets) && datasets[index] == wanted
}

func (p *RPCTransferAdapter) parseRawLogRecord(ctx context.Context, network chain.EVM, item rpcLog, times map[uint64]int64) (Record, bool, error) {
	block, okBlock := hexToUint64(item.BlockNumber)
	index, okIndex := hexToUint64(item.LogIndex)
	if !okBlock || !okIndex || item.TransactionHash == "" || item.Address == "" || len(item.Topics) == 0 {
		return Record{}, false, nil
	}
	blockTime, ok := times[block]
	if !ok {
		var err error
		blockTime, err = p.blockTimestamp(ctx, network.Key, block)
		if err != nil {
			return Record{}, false, err
		}
		times[block] = blockTime
	}
	address := strings.ToLower(item.Address)
	return Record{
		ChainID: network.ID, BlockNumber: block, BlockTime: blockTime,
		TransactionHash: strings.ToLower(item.TransactionHash), LogIndex: index,
		Dataset: DatasetLogs, Address: address,
		Payload: map[string]any{"contract_address": address, "topics": item.Topics, "data": item.Data},
	}, true, nil
}

func appendGroupedRecord(result *ProviderResult, record Record, seenByAddress map[string]map[string]struct{}, address string) bool {
	seen := seenByAddress[address]
	if seen == nil {
		seen = make(map[string]struct{})
		seenByAddress[address] = seen
	}
	key := record.UniqueKey()
	if _, exists := seen[key]; exists {
		return false
	}
	seen[key] = struct{}{}
	result.Records = append(result.Records, record)
	return true
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
	return p.getTransferLogsChunkForAddresses(ctx, chainKey, []string{address}, from, to)
}

func (p *RPCTransferAdapter) getTransferLogsChunkForAddresses(ctx context.Context, chainKey string, addresses []string, from, to uint64) ([]rpcLog, error) {
	logs, err := p.getTransferLogsRangeForAddresses(ctx, chainKey, addresses, from, to)
	if err != nil {
		if from == to || !isRPCLogRangeLimitError(err) {
			return nil, err
		}
		mid := from + (to-from)/2
		left, leftErr := p.getTransferLogsChunkForAddresses(ctx, chainKey, addresses, from, mid)
		if leftErr != nil {
			return nil, leftErr
		}
		right, rightErr := p.getTransferLogsChunkForAddresses(ctx, chainKey, addresses, mid+1, to)
		if rightErr != nil {
			return nil, rightErr
		}
		return append(left, right...), nil
	}
	if err != nil || len(logs) < rpcLogResultLimit {
		return logs, err
	}
	if from == to {
		return logs, nil
	}
	mid := from + (to-from)/2
	left, errL := p.getTransferLogsChunkForAddresses(ctx, chainKey, addresses, from, mid)
	if errL != nil {
		return nil, errL
	}
	right, errR := p.getTransferLogsChunkForAddresses(ctx, chainKey, addresses, mid+1, to)
	if errR != nil {
		return nil, errR
	}
	return append(left, right...), nil
}

func (p *RPCTransferAdapter) getLogsRange(ctx context.Context, chainKey, address string, from, to uint64) ([]rpcLog, error) {
	return p.getTransferLogsRangeForAddresses(ctx, chainKey, []string{address}, from, to)
}

// getTransferLogsRangeForAddresses performs two topic-indexed scans: requested
// wallets as Transfer.from and as Transfer.to. It never restricts the log
// emitter address because that field is the token contract, not the wallet.
func (p *RPCTransferAdapter) getTransferLogsRangeForAddresses(ctx context.Context, chainKey string, addresses []string, from, to uint64) ([]rpcLog, error) {
	normalized, err := normalizeAndValidateRPCLogAddresses(addresses)
	if err != nil {
		return nil, err
	}
	padded := make([]string, 0, len(normalized))
	for _, address := range normalized {
		padded = append(padded, "0x"+strings.Repeat("0", 24)+strings.TrimPrefix(address, "0x"))
	}
	base := map[string]any{
		"fromBlock": fmt.Sprintf("0x%x", from),
		"toBlock":   fmt.Sprintf("0x%x", to),
	}
	filters := []map[string]any{
		{"topics": []any{transferTopic, padded}},
		{"topics": []any{transferTopic, nil, padded}},
	}
	logs := make([]rpcLog, 0)
	seen := make(map[string]struct{})
	for _, topicFilter := range filters {
		filter := map[string]any{"fromBlock": base["fromBlock"], "toBlock": base["toBlock"], "topics": topicFilter["topics"]}
		raw, _, callErr := p.call(ctx, chainKey, "eth_getLogs", []any{filter})
		if callErr != nil {
			return nil, callErr
		}
		var directionLogs []rpcLog
		if unmarshalErr := json.Unmarshal(raw, &directionLogs); unmarshalErr != nil {
			return nil, fmt.Errorf("解析 eth_getLogs 响应: %w", unmarshalErr)
		}
		for _, item := range directionLogs {
			key := strings.ToLower(item.TransactionHash) + ":" + strings.ToLower(item.LogIndex) + ":" + strings.ToLower(item.Address)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			logs = append(logs, item)
		}
	}
	return logs, nil
}

func (p *RPCTransferAdapter) getAllLogsChunk(ctx context.Context, chainKey, address string, from, to uint64) ([]rpcLog, error) {
	return p.getAllLogsChunkForAddresses(ctx, chainKey, []string{address}, from, to)
}

// getAllLogsChunkForAddresses queries one unchanged address group and only
// bisects the block range when the provider rejects or saturates a request.
// Address splitting belongs to the V3.3 group scheduler, not the RPC adapter.
func (p *RPCTransferAdapter) getAllLogsChunkForAddresses(ctx context.Context, chainKey string, addresses []string, from, to uint64) ([]rpcLog, error) {
	logs, err := p.getAllLogsRangeForAddresses(ctx, chainKey, addresses, from, to)
	if err != nil {
		if from == to || !isRPCLogRangeLimitError(err) {
			return nil, err
		}
		mid := from + (to-from)/2
		left, leftErr := p.getAllLogsChunkForAddresses(ctx, chainKey, addresses, from, mid)
		if leftErr != nil {
			return nil, leftErr
		}
		right, rightErr := p.getAllLogsChunkForAddresses(ctx, chainKey, addresses, mid+1, to)
		if rightErr != nil {
			return nil, rightErr
		}
		return append(left, right...), nil
	}
	if len(logs) < rpcLogResultLimit {
		return logs, err
	}
	if from == to {
		return logs, nil
	}
	mid := from + (to-from)/2
	left, err := p.getAllLogsChunkForAddresses(ctx, chainKey, addresses, from, mid)
	if err != nil {
		return nil, err
	}
	right, err := p.getAllLogsChunkForAddresses(ctx, chainKey, addresses, mid+1, to)
	if err != nil {
		return nil, err
	}
	return append(left, right...), nil
}

func isRPCLogRangeLimitError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "block range") ||
		(strings.Contains(message, "limited to") && strings.Contains(message, "block")) ||
		strings.Contains(message, "query returned more than") ||
		strings.Contains(message, "too many results") ||
		strings.Contains(message, "response size exceeded")
}

func (p *RPCTransferAdapter) getAllLogsRange(ctx context.Context, chainKey, address string, from, to uint64) ([]rpcLog, error) {
	return p.getAllLogsRangeForAddresses(ctx, chainKey, []string{address}, from, to)
}

func (p *RPCTransferAdapter) getAllLogsRangeForAddresses(ctx context.Context, chainKey string, addresses []string, from, to uint64) ([]rpcLog, error) {
	normalized := normalizeRPCLogAddresses(addresses)
	if len(normalized) == 0 {
		return nil, fmt.Errorf("eth_getLogs 地址组为空")
	}
	filter := map[string]any{"address": normalized, "fromBlock": fmt.Sprintf("0x%x", from), "toBlock": fmt.Sprintf("0x%x", to)}
	raw, _, err := p.call(ctx, chainKey, "eth_getLogs", []any{filter})
	if err != nil {
		return nil, err
	}
	var logs []rpcLog
	if err = json.Unmarshal(raw, &logs); err != nil {
		return nil, fmt.Errorf("解析 eth_getLogs 响应: %w", err)
	}
	return logs, nil
}

func normalizeRPCLogAddresses(addresses []string) []string {
	normalized := make([]string, 0, len(addresses))
	seen := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		address = strings.ToLower(strings.TrimSpace(address))
		if address == "" {
			continue
		}
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		normalized = append(normalized, address)
	}
	sort.Strings(normalized)
	return normalized
}

func normalizeAndValidateRPCLogAddresses(addresses []string) ([]string, error) {
	if len(addresses) == 0 {
		return nil, fmt.Errorf("RPC eth_getLogs 地址组为空")
	}
	for _, raw := range addresses {
		address := strings.ToLower(strings.TrimSpace(raw))
		if !isValidRPCLogAddress(address) {
			return nil, fmt.Errorf("RPC Group Adapter 地址非法: %q", truncate(raw))
		}
	}
	normalized := normalizeRPCLogAddresses(addresses)
	if len(normalized) == 0 {
		return nil, fmt.Errorf("RPC eth_getLogs 地址组为空")
	}
	return normalized, nil
}

func isValidRPCLogAddress(address string) bool {
	if len(address) != 42 || !strings.HasPrefix(address, "0x") {
		return false
	}
	for _, char := range address[2:] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func (p *RPCTransferAdapter) call(ctx context.Context, chainKey, method string, params any) (json.RawMessage, string, error) {
	if turbo, _ := ctx.Value(turboRPCContextKey{}).(bool); turbo {
		if client, ok := p.client.(turboRPCClient); ok {
			return client.CallTurbo(ctx, chainKey, method, params)
		}
	}
	return p.client.Call(ctx, chainKey, method, params)
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
