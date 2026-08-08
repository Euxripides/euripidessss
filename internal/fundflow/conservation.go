package fundflow

import (
	"context"
	"math/big"
)

// conservationCheck 资金守恒检查（设计 §42-§43、Case H）。
func (e *Engine) conservationCheck(ctx context.Context, chainKey string, g *EntityAwareFlowGraph, invID string) []*ConservationResult {
	if e.src == nil {
		return nil
	}
	var out []*ConservationResult
	for _, n := range g.Nodes {
		st, err := e.src.AddressStats(ctx, n.Address, "")
		if err != nil || st == nil {
			continue
		}
		in := bigFrom(st.TotalIn)
		outF := bigFrom(st.TotalOut)
		dev := new(big.Float)
		if in.Sign() > 0 {
			diff := new(big.Int).Sub(in, outF)
			dev.Quo(new(big.Float).SetInt(diff), new(big.Float).SetInt(in))
		}
		d, _ := dev.Float64()
		if d < 0 {
			d = -d
		}
		pass := d < 0.5
		reason := ""
		if !pass {
			reason = "流入/流出偏差过大且无余额数据：可能漏 Internal Tx / Token Transfer / Swap 未解析，建议触发 Gap Repair/Revalidation"
		}
		out = append(out, &ConservationResult{
			Address: n.Address, Inflow: st.TotalIn, Outflow: st.TotalOut,
			Deviation: round3(d), Pass: pass, Reason: reason,
		})
	}
	return out
}

