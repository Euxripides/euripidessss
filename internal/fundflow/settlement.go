package fundflow

import (
	"context"
	"math/big"

	"github.com/etl/backend/internal/analyticsapi"
)

// detectSettlements 识别资金沉淀候选（设计 §20-§25）。
func (e *Engine) detectSettlements(ctx context.Context, chainKey, root string, g *EntityAwareFlowGraph, invID string) []*SettlementResult {
	if e.src == nil {
		return nil
	}
	var out []*SettlementResult
	for _, n := range g.Nodes {
		if n.Address == root {
			continue
		}
		st, err := e.src.AddressStats(ctx, n.Address, "")
		if err != nil || st == nil {
			continue
		}
		in := bigFrom(st.TotalIn)
		outf := bigFrom(st.TotalOut)
		retained := new(big.Int).Sub(in, outf)
		retention := 0.0
		if in.Sign() > 0 {
			ratio := new(big.Float).Quo(new(big.Float).SetInt(retained), new(big.Float).SetInt(in))
			f, _ := ratio.Float64()
			retention = clamp01(f)
		}
		inactive := 0.0
		if st.Recent30d == 0 {
			inactive = 1
		} else if st.Recent30d < 3 {
			inactive = 0.6
		}
		terminal := 0.0
		if st.Top1TargetRatio < 0.2 {
			terminal = 0.8
		}
		entityRel := 0.0
		if n.EntityType != "" {
			entityRel = 0.5
		}
		score := 0.30*retention + 0.20*holdingDuration(st) + 0.15*lowOutflow(st) +
			0.15*inactive + 0.10*terminal + 0.10*entityRel
		score = round3(score)
		settleType := "UNKNOWN_SETTLEMENT"
		conf := 0.5
		if score >= 0.65 {
			settleType = "DORMANT_WALLET"
			conf = 0.7
		} else if score >= 0.5 {
			settleType = "CUSTODIAL_SETTLEMENT"
			conf = 0.6
		}
		if score >= 0.5 {
			out = append(out, &SettlementResult{
				Address: n.Address, EntityID: n.EntityID, EntityName: n.EntityName,
				RetainedValue: retained.String(), HoldingDurationSeconds: holdingDuration(st) * 86400,
				LastOutflowBlock: st.LastSeen, SettlementScore: score,
				SettlementType: settleType, Confidence: conf,
				EvidenceIDs: []string{"ev_settlement_" + n.Address},
			})
		}
	}
	return out
}

// detectCashouts 从路径生成兑现候选（设计 §26-§27、Case A/B）。
func (e *Engine) detectCashouts(paths []*Path) []*CashoutResult {
	var out []*CashoutResult
	for _, p := range paths {
		if p.PathType != "DIRECT_CASHOUT" && p.PathType != "MULTI_HOP_CASHOUT" {
			continue
		}
		term := p.Nodes[len(p.Nodes)-1]
		src := p.RootAddress
		if len(p.Nodes) > 1 {
			src = p.Nodes[len(p.Nodes)-2].Address
		}
		out = append(out, &CashoutResult{
			SourceAddress: src, DestinationAddress: term.Address,
			EntityID: term.EntityID, EntityName: term.EntityName,
			TxHash: term.EdgeTxHash, Token: firstToken(p), Amount: term.InAmount,
			PathType: p.PathType, Confidence: p.Confidence,
			EvidenceIDs: []string{"ev_cashout_" + p.ID},
		})
	}
	return out
}

func firstToken(p *Path) string {
	for _, n := range p.Nodes {
		if n.Token != "" {
			return n.Token
		}
	}
	return ""
}

func holdingDuration(st *analyticsapi.AddressStats) float64 {
	first := blockNum(st.FirstSeen)
	last := blockNum(st.LastSeen)
	if first == 0 || last <= first {
		return 0
	}
	// BSC 约 3s/块；保守按 3s 估算
	seconds := float64(last-first) * 3
	days := seconds / 86400
	if days > 3650 {
		days = 3650
	}
	return days / 3650
}

func lowOutflow(st *analyticsapi.AddressStats) float64 {
	// 出向集中度低 → 流出频率低
	if st.Top1TargetRatio < 0.2 {
		return 0.8
	}
	if st.Top1TargetRatio < 0.5 {
		return 0.4
	}
	return 0.1
}

func blockNum(s string) uint64 {
	n := bigFrom(s)
	if n == nil || n.Sign() < 0 {
		return 0
	}
	return n.Uint64()
}

func bigFrom(s string) *big.Int {
	if n, ok := parseBigInt(s); ok {
		return n
	}
	return big.NewInt(0)
}

func round3(v float64) float64 {
	return float64(int64(v*1000+0.5)) / 1000
}
