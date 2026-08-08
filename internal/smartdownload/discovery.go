package smartdownload

import (
	"context"
	"fmt"
	"math"
	"time"
)

// ProbeRequest 单个 Address × Dataset 的探测请求（实施方案 §7）。
type ProbeRequest struct {
	Address   string `json:"address"`
	Dataset   string `json:"dataset"`
	ChainKey  string `json:"chain_key"`
	ChainID   int64  `json:"chain_id"`
	FromBlock uint64 `json:"from_block"`
	ToBlock   uint64 `json:"to_block"`
}

// ProbeResult 探测结果（只做低成本采样，不完整扫描）。
type ProbeResult struct {
	EstimatedRows    uint64  `json:"estimated_rows"`
	EstimatedBytes   uint64  `json:"estimated_bytes"`
	FirstBlock       uint64  `json:"first_block"`
	LastBlock        uint64  `json:"last_block"`
	EstimatedSeconds float64 `json:"estimated_seconds"`
	EstimatedCost    float64 `json:"estimated_cost"`
	Confidence       float64 `json:"confidence"` // 0-1
	ProbeProvider    string  `json:"probe_provider,omitempty"`
}

// probeSampleBlocks 采样区块数（低成本）。
const probeSampleBlocks = 200

// probeTimeout 单次探测超时。
const probeTimeout = 15 * time.Second

// probeRange 计算采样窗口（≤200 块）。
func probeRange(req ProbeRequest) (from, to uint64) {
	from = req.FromBlock
	to = req.ToBlock
	if to-from+1 > probeSampleBlocks {
		to = from + probeSampleBlocks - 1
	}
	return from, to
}

// extrapolate 按采样密度外推整段。
func extrapolate(sampleRows uint64, sampleBlocks, totalBlocks uint64, confidence float64) ProbeResult {
	if sampleBlocks == 0 {
		return ProbeResult{Confidence: confidence, FirstBlock: 0, LastBlock: totalBlocks}
	}
	density := float64(sampleRows) / float64(sampleBlocks)
	rows := uint64(math.Round(density * float64(totalBlocks)))
	return ProbeResult{
		EstimatedRows:  rows,
		EstimatedBytes: rows * 128, // 粗估：每行约 128B
		FirstBlock:     0,
		LastBlock:      totalBlocks,
		Confidence:     confidence,
	}
}

// probeBlockSpan 返回探测窗口块数。
func probeBlockSpan(req ProbeRequest) uint64 {
	if req.ToBlock < req.FromBlock {
		return 0
	}
	return req.ToBlock - req.FromBlock + 1
}

// ProbeWith 用指定 Adapter 探测（适配器不支持时返回 confidence=0）。
func ProbeWith(ctx context.Context, a ProviderAdapter, req ProbeRequest) (ProbeResult, error) {
	if a == nil || !a.Available() || !a.Supports(req.Dataset) {
		return ProbeResult{Confidence: 0}, nil
	}
	prober, ok := a.(interface {
		Probe(ctx context.Context, req ProbeRequest) (ProbeResult, error)
	})
	if !ok {
		return ProbeResult{Confidence: 0}, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	res, err := prober.Probe(probeCtx, req)
	if err != nil {
		return ProbeResult{Confidence: 0}, fmt.Errorf("%s probe: %w", a.Name(), err)
	}
	res.ProbeProvider = a.Name()
	return res, nil
}
