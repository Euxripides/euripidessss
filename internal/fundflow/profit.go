package fundflow

import (
	"context"
	"math/big"
)

// attributeProfit 计算获利归因（P0：L0 Gross / L1 Net Flow；设计 §12-§16、§19）。
func (e *Engine) attributeProfit(ctx context.Context, chainKey, root string, g *EntityAwareFlowGraph, paths []*Path, invID string) []*ProfitAttribution {
	if e.src == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []*ProfitAttribution
	// 路径终点/节点聚合
	for _, p := range paths {
		for _, n := range p.Nodes {
			addr := n.Address
			if seen[addr] {
				continue
			}
			seen[addr] = true
			st, err := e.src.AddressStats(ctx, addr, "")
			if err != nil || st == nil {
				continue
			}
			// L0：累计流入（快速筛查）；L1：净流 = 流入 - 流出
			l0 := &ProfitAttribution{
				Address: addr, EntityID: n.EntityID, EntityName: n.EntityName,
				GrossInflow: st.TotalIn, GrossOutflow: st.TotalOut,
				NetProfit: st.TotalIn, Level: "L0",
				Confidence: 0.5, EvidenceIDs: []string{"ev_profit_l0_" + addr},
			}
			out = append(out, l0)
			net := netProfit(st.TotalIn, st.TotalOut)
			l1 := &ProfitAttribution{
				Address: addr, EntityID: n.EntityID, EntityName: n.EntityName,
				GrossInflow: st.TotalIn, GrossOutflow: st.TotalOut,
				NetProfit: net, Level: "L1",
				Confidence: 0.6, EvidenceIDs: []string{"ev_profit_l1_" + addr},
			}
			out = append(out, l1)
			// L2：成本基础调整（P1 设计 §10-§11、§15）——初始显著投入近似为
			// Top1 来源占比 × 累计流入 × 0.7（启发式，LOW 置信度，证据明确标注）。
			if st.Top1SourceRatio > 0 {
				costBasis := bigFrom(st.TotalIn)
				costBasis.Mul(costBasis, big.NewInt(int64(st.Top1SourceRatio * 1000)))
				costBasis.Div(costBasis, big.NewInt(1000))
				costBasis.Mul(costBasis, big.NewInt(7))
				costBasis.Div(costBasis, big.NewInt(10))
				netL2 := new(big.Int).Sub(bigFrom(netProfit(st.TotalIn, st.TotalOut)), costBasis)
				out = append(out, &ProfitAttribution{
					Address: addr, EntityID: n.EntityID, EntityName: n.EntityName,
					GrossInflow: st.TotalIn, GrossOutflow: st.TotalOut,
					CostBasis: costBasis.String(), ReturnedPrincipal: "0",
					NetProfit: netL2.String(), Level: "L2",
					Confidence: 0.4, EvidenceIDs: []string{"ev_profit_l2_" + addr},
				})
			}
		}
	}
	// 根地址自身
	if !seen[root] {
		if st, err := e.src.AddressStats(ctx, root, ""); err == nil && st != nil {
			out = append(out,
				&ProfitAttribution{Address: root, GrossInflow: st.TotalIn, GrossOutflow: st.TotalOut,
					NetProfit: st.TotalIn, Level: "L0", Confidence: 0.5, EvidenceIDs: []string{"ev_profit_l0_" + root}},
				&ProfitAttribution{Address: root, GrossInflow: st.TotalIn, GrossOutflow: st.TotalOut,
					NetProfit: netProfit(st.TotalIn, st.TotalOut), Level: "L1", Confidence: 0.6,
					EvidenceIDs: []string{"ev_profit_l1_" + root}})
		}
	}
	return out
}

func netProfit(in, out string) string {
	ai, ok1 := parseBigInt(in)
	bi, ok2 := parseBigInt(out)
	if !ok1 {
		return "0"
	}
	if !ok2 {
		return in
	}
	return new(big.Int).Sub(ai, bi).String()
}
