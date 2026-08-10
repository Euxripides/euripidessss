package smartdownload

import (
	"context"
	"fmt"
	"strings"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource/sqd"
)

// SQDAdapter 真实 SQD Provider Adapter（Phase 2）：
// 复用现有 datasource/sqd 可靠性客户端（熔断/冷却/自适应并发），不重写下载器。
// 支持 transactions / token_transfers / logs / internal_transactions。
type SQDAdapter struct {
	client *sqd.Client
}

// NewSQDAdapter 创建 SQD Adapter。
func NewSQDAdapter(client *sqd.Client) *SQDAdapter {
	return &SQDAdapter{client: client}
}

func (p *SQDAdapter) Name() string    { return "sqd" }
func (p *SQDAdapter) Available() bool { return p.client != nil }
func (p *SQDAdapter) Supports(d string) bool {
	switch d {
	case DatasetTransactions, DatasetTokenTransfers, DatasetLogs, DatasetInternalTransactions:
		return true
	default:
		return false
	}
}

// Probe 低成本采样：只拉 ≤200 块数行数，按密度外推。
func (p *SQDAdapter) Probe(ctx context.Context, req ProbeRequest) (ProbeResult, error) {
	if p.client == nil {
		return ProbeResult{Confidence: 0}, nil
	}
	network, err := chain.Resolve(req.ChainKey)
	if err != nil {
		return ProbeResult{Confidence: 0}, nil
	}
	from, to := probeRange(req)
	count, err := p.sampleCount(ctx, network, req.Dataset, req.Address, from, to)
	if err != nil {
		return ProbeResult{Confidence: 0}, err // 失败上抛，便于调度层记录
	}
	return extrapolate(count, to-from+1, probeBlockSpan(req), 0.7), nil
}

func (p *SQDAdapter) sampleCount(ctx context.Context, network chain.EVM, dataset, address string, from, to uint64) (uint64, error) {
	var count uint64
	rng := sqd.BlockRange{From: from, To: to}
	addrs := []string{address}
	var err error
	switch dataset {
	case DatasetTransactions:
		err = p.client.StreamTransactions(ctx, network, rng, addrs, func(b sqd.Block) error {
			count += uint64(len(b.Transactions))
			return nil
		})
	case DatasetTokenTransfers:
		err = p.client.StreamLogs(ctx, network, rng, addrs, func(b sqd.Block) error {
			count += uint64(len(b.Logs))
			return nil
		})
	case DatasetLogs:
		err = p.client.StreamContractLogs(ctx, network, rng, addrs, func(b sqd.Block) error {
			count += uint64(len(b.Logs))
			return nil
		})
	case DatasetInternalTransactions:
		err = p.client.StreamTraces(ctx, network, rng, addrs, func(b sqd.Block) error {
			count += uint64(len(b.Traces))
			return nil
		})
	default:
		return 0, fmt.Errorf("SQD 不支持数据集 %s", dataset)
	}
	return count, err
}

// ExecuteRange 拉取单个 Range 并转中间 Record（Canonical Schema 属 Phase 3）。
func (p *SQDAdapter) ExecuteRange(ctx context.Context, req RangeRequest) (*ProviderResult, error) {
	if p.client == nil {
		return nil, fmt.Errorf("SQD 客户端未装配")
	}
	network, err := chain.Resolve(req.ChainKey)
	if err != nil {
		return nil, err
	}
	rng := sqd.BlockRange{From: req.FromBlock, To: req.ToBlock}
	addrs := []string{req.Address}
	var records []Record
	switch req.Dataset {
	case DatasetTransactions:
		err = p.client.StreamTransactions(ctx, network, rng, addrs, func(b sqd.Block) error {
			for _, tx := range b.Transactions {
				records = append(records, Record{
					ChainID:         network.ID,
					BlockNumber:     b.Header.Number,
					BlockTime:       b.Header.Timestamp,
					TransactionHash: strings.ToLower(tx.Hash),
					LogIndex:        tx.TransactionIndex,
					Dataset:         DatasetTransactions,
					Address:         req.Address,
					Payload: map[string]any{
						"from_address": strings.ToLower(tx.From),
						"to_address":   strings.ToLower(tx.To),
						"value_raw":    tx.Value,
						"input":        tx.Input,
						"method_id":    tx.Sighash,
						"status":       tx.Status,
						"gas_used":     tx.GasUsed,
						"gas_price":    tx.GasPrice,
					},
				})
			}
			return nil
		})
	case DatasetTokenTransfers:
		err = p.client.StreamLogs(ctx, network, rng, addrs, func(b sqd.Block) error {
			for _, l := range b.Logs {
				t, ok := parseSQDTransfer(network, b, l)
				if ok {
					records = append(records, t)
				}
			}
			return nil
		})
	case DatasetLogs:
		err = p.client.StreamContractLogs(ctx, network, rng, addrs, func(b sqd.Block) error {
			for _, l := range b.Logs {
				records = append(records, Record{
					ChainID:         network.ID,
					BlockNumber:     b.Header.Number,
					BlockTime:       b.Header.Timestamp,
					TransactionHash: strings.ToLower(l.TransactionHash),
					LogIndex:        l.LogIndex,
					Dataset:         DatasetLogs,
					Address:         req.Address,
					Payload: map[string]any{
						"contract_address": strings.ToLower(l.Address),
						"topics":           l.Topics,
						"data":             l.Data,
					},
				})
			}
			return nil
		})
	case DatasetInternalTransactions:
		err = p.client.StreamTraces(ctx, network, rng, addrs, func(b sqd.Block) error {
			hashByTxIndex := map[uint64]string{}
			for _, tx := range b.Transactions {
				hashByTxIndex[tx.TransactionIndex] = tx.Hash
			}
			for i, tr := range b.Traces {
				hash := hashByTxIndex[tr.TransactionIndex]
				if hash == "" {
					continue
				}
				status := "ok"
				if tr.Error != nil {
					status = "failed"
				}
				value := ""
				if tr.Action.Value != nil {
					value = *tr.Action.Value
				}
				records = append(records, Record{
					ChainID:         network.ID,
					BlockNumber:     b.Header.Number,
					BlockTime:       b.Header.Timestamp,
					TransactionHash: strings.ToLower(hash),
					LogIndex:        uint64(i),
					Dataset:         DatasetInternalTransactions,
					Address:         req.Address,
					Payload: map[string]any{
						"trace_address": tr.TraceAddress,
						"call_type":     tr.Type,
						"from_address":  strings.ToLower(tr.Action.From),
						"to_address":    strings.ToLower(tr.Action.To),
						"value_raw":     value,
						"status":        status,
					},
				})
			}
			return nil
		})
	default:
		return nil, fmt.Errorf("SQD Adapter 不支持数据集 %s", req.Dataset)
	}
	if err != nil {
		return nil, err
	}
	bytes := uint64(len(records) * 128)
	return &ProviderResult{Records: records, Bytes: bytes, CompletedTo: req.ToBlock}, nil
}

// parseSQDTransfer 从 SQD Log 解析 ERC20/BEP20 Transfer（唯一键 = tx_hash + log_index）。
func parseSQDTransfer(network chain.EVM, b sqd.Block, l sqd.Log) (Record, bool) {
	if len(l.Topics) != 3 || !strings.EqualFold(l.Topics[0], sqd.TransferTopic) {
		return Record{}, false
	}
	from, ok := topicAddress(l.Topics[1])
	if !ok {
		return Record{}, false
	}
	to, ok := topicAddress(l.Topics[2])
	if !ok {
		return Record{}, false
	}
	value, ok := dataWord(l.Data)
	if !ok {
		return Record{}, false
	}
	standard := "ERC20"
	if network.Key == "bsc" {
		standard = "BEP20"
	}
	return Record{
		ChainID:         network.ID,
		BlockNumber:     b.Header.Number,
		BlockTime:       b.Header.Timestamp,
		TransactionHash: strings.ToLower(l.TransactionHash),
		LogIndex:        l.LogIndex,
		Dataset:         DatasetTokenTransfers,
		Address:         strings.ToLower(l.Address),
		Payload: map[string]any{
			"token_address": strings.ToLower(l.Address),
			"from_address":  from,
			"to_address":    to,
			"value_raw":     value,
			"standard":      standard,
		},
	}, true
}
