package fundflow

import (
	"context"
	"math/big"
	"strings"
	"time"
)

// AssetConversionEvent 是资产转换事件（设计 §44-§47）。
type AssetConversionEvent struct {
	TxHash        string    `json:"tx_hash,omitempty"`
	FromAsset     string    `json:"from_asset"`
	FromAmount    string    `json:"from_amount"`
	ToAsset       string    `json:"to_asset"`
	ToAmount      string    `json:"to_amount"`
	USDValue      string    `json:"usd_value"`
	PriceSource   string    `json:"price_source"`
	PriceMethod   string    `json:"price_method"`
	PriceTime     time.Time `json:"price_time,omitempty"`
	Confidence    float64   `json:"confidence"`
	BlockNumber   uint64    `json:"block_number,omitempty"`
	EvidenceIDs   []string  `json:"evidence_ids,omitempty"`
}

// ContinuitySegment 是连续追踪中的一段（资产/金额/USD 估值）。
type ContinuitySegment struct {
	Address      string  `json:"address"`
	Asset        string  `json:"asset"`
	Amount       string  `json:"amount"`
	USDValue     string  `json:"usd_value"`
	PriceSource  string  `json:"price_source"`
	PriceMethod  string  `json:"price_method"`
	Confidence   float64 `json:"confidence"`
}

// AssetContinuityResult 是跨资产连续追踪结果（设计 §17-§20）。
type AssetContinuityResult struct {
	RootAddress string                  `json:"root_address"`
	ChainKey    string                  `json:"chain_key"`
	Segments    []ContinuitySegment     `json:"segments,omitempty"`
	Conversions []AssetConversionEvent  `json:"conversions,omitempty"`
	Notes       []string                `json:"notes,omitempty"`
}

// Continuity 分析根地址的跨资产连续路径（Token Mode 基础上叠加 Value Mode）。
func (e *Engine) Continuity(ctx context.Context, chainKey, root, token string, from, to uint64, goal string, depth int, invID string) (*AssetContinuityResult, error) {
	res, err := e.Analyze(ctx, chainKey, root, token, from, to, goal, depth, invID)
	if err != nil {
		return nil, err
	}
	out := &AssetContinuityResult{RootAddress: root, ChainKey: chainKey}
	if e.src == nil {
		out.Notes = append(out.Notes, "数据源不可用，无法解析 Swap/Bridge 连续性")
		return out, nil
	}
	segments := []ContinuitySegment{}
	for _, p := range res.Paths {
		prevAsset := ""
		for _, n := range p.Nodes {
			entType := strings.ToUpper(n.EntityType)
			asset := n.Token
			if asset == "" {
				asset = prevAsset
			}
			seg := ContinuitySegment{
				Address: n.Address, Asset: asset, Amount: n.InAmount,
				PriceSource: "LOCAL_PEG_ESTIMATE", PriceMethod: "PEG_ASSUMPTION", Confidence: 0.3,
			}
			seg.USDValue = estimateUSD(asset, n.InAmount, &seg.PriceSource, &seg.PriceMethod, &seg.Confidence)
			segments = append(segments, seg)
			// Swap / Bridge 节点：探测同区块窗口内不同资产出边 → 转换事件
			if entType == "ROUTER" || entType == "DEX" || entType == "BRIDGE" || n.EdgeType == "SWAP_OUT" || n.EdgeType == "BRIDGE_OUT" {
				if ev := e.detectConversion(ctx, n.Address, asset, n.BlockNumber, n.EdgeTxHash); ev != nil {
					out.Conversions = append(out.Conversions, *ev)
					prevAsset = ev.ToAsset
					segments = append(segments, ContinuitySegment{
						Address: n.Address, Asset: ev.ToAsset, Amount: ev.ToAmount,
						USDValue: ev.USDValue, PriceSource: ev.PriceSource,
						PriceMethod: ev.PriceMethod, Confidence: ev.Confidence,
					})
				}
			}
			prevAsset = asset
		}
	}
	out.Segments = segments
	if len(out.Conversions) == 0 {
		out.Notes = append(out.Notes, "未检测到 Swap/Bridge 资产转换；如需跨链续追需接入目标链地址解析")
	}
	return out, nil
}

func (e *Engine) detectConversion(ctx context.Context, addr, fromAsset string, block uint64, txHash string) *AssetConversionEvent {
	flows, err := e.src.Flows(ctx, addr, "")
	if err != nil {
		return nil
	}
	type cand struct {
		token, amount, hash string
		block               uint64
	}
	var best cand
	bestAmount := big.NewInt(0)
	for _, f := range flows {
		if !strings.EqualFold(f.Direction, "outgoing") {
			continue
		}
		t := strings.ToLower(f.Token)
		if t == "" || t == strings.ToLower(fromAsset) {
			continue
		}
		b := blockNum(f.Block)
		if block > 0 && (b == 0 || diffBlock(b, block) > 20) {
			continue
		}
		amt, ok := parseBigInt(f.Amount)
		if !ok {
			continue
		}
		if amt.Cmp(bestAmount) > 0 {
			bestAmount = amt
			best = cand{token: t, amount: f.Amount, hash: f.TxHash, block: b}
		}
	}
	if best.token == "" {
		return nil
	}
	src := "LOCAL_PEG_ESTIMATE"
	method := "PEG_ASSUMPTION"
	conf := 0.3
	usd := estimateUSD(best.token, best.amount, &src, &method, &conf)
	return &AssetConversionEvent{
		TxHash: best.hash, FromAsset: fromAsset, FromAmount: "",
		ToAsset: best.token, ToAmount: best.amount, USDValue: usd,
		PriceSource: src, PriceMethod: method, PriceTime: time.Now().UTC(),
		Confidence: conf, BlockNumber: best.block,
		EvidenceIDs: []string{"ev_conversion_" + shortAddr(addr)},
	}
}

func shortAddr(addr string) string {
	if len(addr) <= 10 {
		return addr
	}
	return addr[2:10]
}

func diffBlock(a, b uint64) uint64 {
	if a > b {
		return a - b
	}
	return b - a
}

// estimateUSD 对已知稳定币按 1 USD 锚定估算；其余资产标记 UNKNOWN 与低置信度。
func estimateUSD(token, amount string, src, method *string, conf *float64) string {
	token = strings.ToLower(strings.TrimSpace(token))
	known := map[string]bool{
		"0x55d398326f99059ff775485246999027b3197955": true, // BSC USDT
		"0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d": true, // BSC USDC
		"0xe9e7cea3dedca5984780bafc599bd69add087d56": true, // BSC BUSD
	}
	amt, ok := parseBigInt(amount)
	if !ok {
		return "0"
	}
	if known[token] {
		*src = "LOCAL_PEG_ESTIMATE"
		*method = "STABLECOIN_PEG_1USD"
		*conf = 0.8
		return amt.String()
	}
	*src = "UNKNOWN"
	*method = "NO_PRICE_SOURCE"
	*conf = 0.1
	return "0"
}
