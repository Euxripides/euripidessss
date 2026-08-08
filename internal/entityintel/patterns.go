package entityintel

import (
	"context"
	"math"
	"math/big"
	"strings"

	"github.com/etl/backend/internal/analyticsapi"
)

// FeatureSource 是实体特征数据源（analyticsapi.Service 满足）。
type FeatureSource interface {
	AddressStats(ctx context.Context, address string, token string) (*analyticsapi.AddressStats, error)
	Profile(ctx context.Context, address string) (*analyticsapi.Profile, error)
	Flows(ctx context.Context, address string, token string) ([]analyticsapi.FlowEdge, error)
}

// FeatureExtractor 从分析服务提取地址特征（设计 §59 Feature Store）。
type FeatureExtractor struct {
	src FeatureSource
}

// NewFeatureExtractor 创建特征提取器。
func NewFeatureExtractor(src FeatureSource) *FeatureExtractor {
	return &FeatureExtractor{src: src}
}

// Extract 提取地址特征。
func (f *FeatureExtractor) Extract(ctx context.Context, address string) (*AddressFeature, error) {
	feat := &AddressFeature{}
	if f.src == nil {
		return feat, nil
	}
	st, err := f.src.AddressStats(ctx, address, "")
	if err == nil && st != nil {
		feat.TxCount = st.TxCount
		feat.CounterpartyCount = st.UniqueUpstream + st.UniqueDownstream
		feat.Inflow = st.TotalIn
		feat.Outflow = st.TotalOut
		feat.NetRetained = st.NetFlow
		feat.SweepRatio = st.Top1TargetRatio
		feat.FirstSeen = st.FirstSeen
		feat.LastSeen = st.LastSeen
		feat.Recent24h = st.Recent24h
		feat.Recent7d = st.Recent7d
		feat.Recent30d = st.Recent30d
		feat.TokenDiversity = st.TxCount
	}
	if p, err := f.src.Profile(ctx, address); err == nil && p != nil {
		feat.IsContract = p.ContractCount > 0
		feat.RiskScore = p.RiskScore
		feat.TokenDiversity = p.TokenCount
		if p.ContractCount > 0 {
			feat.ContractRatio = 1
		}
	}
	feat.DormancyScore = DormancyScore(feat)
	return feat, nil
}

// SweepDestination 返回地址的稳定归集去向（流出边中笔数/金额最大且占比高者）。
func SweepDestination(ctx context.Context, src FeatureSource, address string) (dest, token, amount, block string, count int64) {
	if src == nil {
		return "", "", "", "", 0
	}
	flows, err := src.Flows(ctx, address, "")
	if err != nil {
		return "", "", "", "", 0
	}
	type agg struct {
		token, amount, block string
		count                int64
		amt                  *big.Int
	}
	by := map[string]*agg{}
	for _, f := range flows {
		if !strings.EqualFold(f.Direction, "outgoing") || strings.TrimSpace(f.Counterparty) == "" {
			continue
		}
		cp := strings.ToLower(f.Counterparty)
		a := by[cp]
		if a == nil {
			a = &agg{amt: big.NewInt(0)}
			by[cp] = a
		}
		a.count++
		a.token = strings.ToLower(f.Token)
		a.block = f.Block
		if n, ok := parseBigAmount(f.Amount); ok {
			a.amt.Add(a.amt, n)
		}
	}
	var best *agg
	bestAddr := ""
	for addr, a := range by {
		if best == nil || a.count > best.count {
			best, bestAddr = a, addr
		}
	}
	if best == nil {
		return "", "", "", "", 0
	}
	return bestAddr, best.token, best.amt.String(), best.block, best.count
}

// TopIncomingSource 返回最大入金来源（用于 COMMON_FUNDER 聚类辅助）。
func TopIncomingSource(ctx context.Context, src FeatureSource, address string) (string, int64) {
	if src == nil {
		return "", 0
	}
	flows, err := src.Flows(ctx, address, "")
	if err != nil {
		return "", 0
	}
	counts := map[string]int64{}
	for _, f := range flows {
		if strings.EqualFold(f.Direction, "incoming") {
			counts[strings.ToLower(f.Counterparty)]++
		}
	}
	best, n := "", int64(0)
	for addr, c := range counts {
		if c > n {
			best, n = addr, c
		}
	}
	return best, n
}

// DormancyScore 计算沉淀分数（设计 §25-§26）：
// NetRetainedValue + HoldingDuration + LowOutflowFrequency + RecentInactivity。
func DormancyScore(f *AddressFeature) float64 {
	if f == nil {
		return 0
	}
	net := netBig(f.NetRetained)
	in := netBig(f.Inflow)
	netRatio := 0.0
	if in.Sign() > 0 {
		quo := new(big.Float).Quo(new(big.Float).SetInt(net), new(big.Float).SetInt(in))
		q, _ := quo.Float64()
		netRatio = q
		if netRatio < 0 {
			netRatio = 0
		}
		if netRatio > 1 {
			netRatio = 1
		}
	}
	// 近 30 天无活动 → 不活跃分
	inactive := 0.0
	if f.Recent30d == 0 {
		inactive = 1
	} else if f.Recent30d < 3 {
		inactive = 0.6
	}
	// 流出频率低 → 沉淀
	outFreq := 0.0
	if f.TxCount > 0 {
		ratio := float64(f.CounterpartyCount) / float64(f.TxCount)
		outFreq = 1 - clamp01(ratio)
	}
	score := 0.4*netRatio + 0.3*inactive + 0.2*outFreq + 0.1*clamp01(f.HoldingDurationHours/24)
	return math.Round(score*1000) / 1000
}

func netBig(s string) *big.Int {
	if n, ok := parseBigAmount(s); ok {
		return n
	}
	return big.NewInt(0)
}

func parseBigAmount(s string) (*big.Int, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "<nil>" {
		return nil, false
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, 10); !ok {
		return nil, false
	}
	return n, true
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
