package intelligence

import (
	"fmt"
	"strings"
	"time"
)

// ── Evidence Extractor（V2.1 设计 §1）──
//
// 从任务结果（路径/风险模式/观察/获利检测）提取结构化证据：
// 交易证据（tx_hash/block/token/amount）、地址证据、时间证据、风险证据、获利证据。
// 纯函数，可单测；全部带可信度。

// extractEvidence 汇总提取证据并去重（key = type|tx_hash|address|detail 截断）。
// existing 是已提取的证据（跨轮去重，避免同证据重复追加）。
func extractEvidence(existing []Evidence, invID string, paths []RankedPath, patterns []RiskPattern, obs []Observation, profit *ProfitReport, limit int) []Evidence {
	seen := map[string]bool{}
	for _, ev := range existing {
		seen[evidenceKey(ev)] = true
	}
	var out []Evidence
	add := func(evs ...Evidence) {
		for _, ev := range evs {
			key := evidenceKey(ev)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, ev)
		}
	}
	add(evidenceFromPaths(invID, paths)...)
	add(evidenceFromPatterns(invID, patterns)...)
	add(evidenceFromObservations(invID, obs)...)
	add(evidenceFromProfit(invID, profit)...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// evidenceKey 生成证据去重键。
func evidenceKey(ev Evidence) string {
	return string(ev.Type) + "|" + ev.TxHash + "|" + ev.Address + "|" + truncate(ev.Detail, 40)
}

// evidenceFromPaths 从资金路径提取交易证据（Top 路径边，每路径最多 5 条边）。
func evidenceFromPaths(invID string, paths []RankedPath) []Evidence {
	var out []Evidence
	pathLimit := 5
	if len(paths) > pathLimit {
		paths = paths[:pathLimit]
	}
	for _, p := range paths {
		edgeLimit := 5
		edges := p.Path.Edges
		if len(edges) > edgeLimit {
			edges = edges[:edgeLimit]
		}
		for _, e := range edges {
			detail := fmt.Sprintf("%s %s %s", e.From, e.Token, e.Amount)
			out = append(out, Evidence{
				InvestigationID: invID,
				Type:            EvTransaction,
				Address:         e.To,
				TxHash:          e.TxHash,
				BlockNumber:     e.Block,
				Token:           e.Token,
				Amount:          e.Amount,
				Detail:          detail,
				Confidence:      0.85, // 链上交易为强证据
				CreatedAt:       time.Now().UTC(),
			})
		}
	}
	return out
}

// evidenceFromPatterns 从风险模式提取证据。
func evidenceFromPatterns(invID string, patterns []RiskPattern) []Evidence {
	var out []Evidence
	for _, p := range patterns {
		conf := 0.7
		switch p.Severity {
		case "high":
			conf = 0.9
		case "medium":
			conf = 0.8
		}
		// 风险模式关联的交易边
		edges := p.Edges
		if len(edges) > 3 {
			edges = edges[:3]
		}
		for _, e := range edges {
			out = append(out, Evidence{
				InvestigationID: invID,
				Type:            EvRisk,
				Address:         p.Address,
				TxHash:          e.TxHash,
				BlockNumber:     e.Block,
				Token:           e.Token,
				Amount:          e.Amount,
				Detail:          fmt.Sprintf("[%s] %s: %s", p.Severity, p.Type, p.Detail),
				Confidence:      conf,
				CreatedAt:       p.DetectedAt,
			})
		}
		if len(edges) == 0 {
			out = append(out, Evidence{
				InvestigationID: invID,
				Type:            EvRisk,
				Address:         p.Address,
				Detail:          fmt.Sprintf("[%s] %s: %s", p.Severity, p.Type, p.Detail),
				Confidence:      conf,
				CreatedAt:       p.DetectedAt,
			})
		}
	}
	return out
}

// evidenceFromObservations 从观察结果提取证据（新交易→交易证据，新地址→地址证据）。
func evidenceFromObservations(invID string, obs []Observation) []Evidence {
	var out []Evidence
	for _, o := range obs {
		switch o.Type {
		case ObsNewTransaction:
			out = append(out, Evidence{
				InvestigationID: invID,
				Type:            EvTransaction,
				Address:         o.Address,
				Detail:          truncate(o.Detail, 80),
				Confidence:      0.75,
				CreatedAt:       time.Unix(o.Timestamp, 0).UTC(),
			})
		case ObsNewAddress:
			out = append(out, Evidence{
				InvestigationID: invID,
				Type:            EvAddress,
				Address:         o.Address,
				Detail:          truncate(o.Detail, 80),
				Confidence:      0.7,
				CreatedAt:       time.Unix(o.Timestamp, 0).UTC(),
			})
		case ObsRiskEvent:
			out = append(out, Evidence{
				InvestigationID: invID,
				Type:            EvRisk,
				Address:         o.Address,
				Detail:          truncate(o.Detail, 80),
				Confidence:      0.8,
				CreatedAt:       time.Unix(o.Timestamp, 0).UTC(),
			})
		}
	}
	return out
}

// evidenceFromProfit 从获利检测结果提取证据。
func evidenceFromProfit(invID string, profit *ProfitReport) []Evidence {
	if profit == nil || !profit.Detected {
		return nil
	}
	conf := 0.6 // 无价格 oracle 的结构性判断，置信度基准较低
	if strings.Contains(profit.Kind, "profit") && strings.Contains(profit.Kind, "holding") {
		conf = 0.7 // 买卖 + 沉淀双结构
	}
	out := []Evidence{{
		InvestigationID: invID,
		Type:            EvProfit,
		Detail:          profit.Summary,
		Confidence:      conf,
		CreatedAt:       time.Now().UTC(),
	}}
	for _, tok := range profit.Tokens {
		out = append(out, Evidence{
			InvestigationID: invID,
			Type:            EvProfit,
			Token:           tok,
			Detail:          "获利/沉淀涉及 Token: " + tok,
			Confidence:      conf,
			CreatedAt:       time.Now().UTC(),
		})
	}
	return out
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
