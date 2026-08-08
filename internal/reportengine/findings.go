package reportengine

import (
	"fmt"
	"strings"

	"github.com/etl/backend/internal/fundflow"
)

// AnalysisInputs 是生成报告所需的结构化分析结果（设计 §63：Finding Builder 输入）。
type AnalysisInputs struct {
	ChainKey      string
	RootAddress   string
	Goal          string
	FundFlow      *fundflow.AnalysisResult
	Entities      map[string]*entityResolution
	Coverage      []DatasetCertification
}

type entityResolution struct {
	EntityID   string
	EntityName string
	EntityType string
	Confidence float64
	Labels     []string
}

// BuildFindings 将各系统结果转成标准 Finding（设计 §63）。
func BuildFindings(in *AnalysisInputs, evidence *EvidenceIndex) []Finding {
	var out []Finding
	if in == nil || in.FundFlow == nil {
		return out
	}
	ff := in.FundFlow
	// 关键地址/实体
	for addr, ent := range in.Entities {
		if addr == in.RootAddress {
			out = append(out, Finding{
				ID: "finding_key_address_" + shortID(addr), FindingType: "KEY_ADDRESS",
				SubjectIDs: []string{addr}, Statement: fmt.Sprintf("关键地址 %s（%s）", addr, ent.EntityName),
				Metrics: map[string]string{"entity": ent.EntityName, "type": ent.EntityType},
				Confidence: ent.Confidence, EvidenceIDs: []string{"ev_entity_" + ent.EntityID},
			})
		} else {
			out = append(out, Finding{
				ID: "finding_key_entity_" + ent.EntityID, FindingType: "KEY_ENTITY",
				SubjectIDs: []string{addr}, Statement: fmt.Sprintf("关键实体 %s（%s）", ent.EntityName, ent.EntityType),
				Metrics: map[string]string{"entity": ent.EntityName, "type": ent.EntityType, "labels": strings.Join(ent.Labels, ",")},
				Confidence: ent.Confidence, EvidenceIDs: []string{"ev_entity_" + ent.EntityID},
			})
		}
	}
	// 关键路径
	for i, p := range ff.Paths {
		if i >= 20 {
			break
		}
		out = append(out, Finding{
			ID: "finding_path_" + p.ID, FindingType: "HIGH_VALUE_PATH",
			SubjectIDs: pathSubjects(p), Statement: fmt.Sprintf("关键路径 %s：%d 跳，评分 %.2f", p.PathType, p.Hops, p.Score),
			Metrics: map[string]string{
				"path_type": p.PathType, "hops": fmt.Sprintf("%d", p.Hops),
				"amount": p.TotalAmount, "score": fmt.Sprintf("%.2f", p.Score),
				"confidence": fmt.Sprintf("%.2f", p.Confidence), "terminal": p.TerminalType,
			},
			Confidence: p.Confidence, EvidenceIDs: []string{"ev_path_" + p.ID},
		})
	}
	// 获利归因（L1）
	for i, pr := range ff.Profit {
		if pr.Level != "L1" || i >= 20 {
			continue
		}
		out = append(out, Finding{
			ID: "finding_profit_" + shortID(pr.Address), FindingType: "PROFIT_ATTRIBUTION",
			SubjectIDs: []string{pr.Address},
			Statement:  fmt.Sprintf("地址 %s 净获利（L1）约 %s", pr.Address, pr.NetProfit),
			Metrics: map[string]string{
				"gross_inflow": pr.GrossInflow, "gross_outflow": pr.GrossOutflow,
				"net_profit": pr.NetProfit, "level": pr.Level,
			},
			Confidence: pr.Confidence, EvidenceIDs: []string{"ev_profit_l1_" + pr.Address},
		})
	}
	// 沉淀
	for i, s := range ff.Settlements {
		if i >= 20 {
			break
		}
		out = append(out, Finding{
			ID: "finding_settlement_" + shortID(s.Address), FindingType: "SETTLEMENT",
			SubjectIDs: []string{s.Address},
			Statement:  fmt.Sprintf("地址 %s 沉淀候选（%s），沉淀分 %.2f", s.Address, s.SettlementType, s.SettlementScore),
			Metrics: map[string]string{
				"retained": s.RetainedValue, "score": fmt.Sprintf("%.2f", s.SettlementScore),
				"type": s.SettlementType, "confidence": fmt.Sprintf("%.2f", s.Confidence),
			},
			Confidence: s.Confidence, EvidenceIDs: []string{"ev_settlement_" + s.Address},
		})
	}
	// 兑现 / 交易所落点
	for i, c := range ff.Cashouts {
		if i >= 20 {
			break
		}
		ft := "CASHOUT"
		if c.PathType == "DIRECT_CASHOUT" || c.PathType == "MULTI_HOP_CASHOUT" {
			ft = "EXCHANGE_DEPOSIT"
		}
		out = append(out, Finding{
			ID: "finding_cashout_" + shortID(c.DestinationAddress), FindingType: ft,
			SubjectIDs: []string{c.SourceAddress, c.DestinationAddress},
			Statement:  fmt.Sprintf("资金从 %s 流向 %s（%s），路径 %s", c.SourceAddress, c.DestinationAddress, c.EntityName, c.PathType),
			Metrics: map[string]string{
				"entity": c.EntityName, "amount": c.Amount, "token": c.Token,
				"path_type": c.PathType, "confidence": fmt.Sprintf("%.2f", c.Confidence),
			},
			Confidence: c.Confidence, EvidenceIDs: []string{"ev_cashout_" + shortID(c.DestinationAddress)},
		})
	}
	// 回流
	for i, rt := range ff.RoundTrips {
		if i >= 10 {
			break
		}
		out = append(out, Finding{
			ID: "finding_roundtrip_" + rt.PathID, FindingType: "ROUND_TRIP",
			SubjectIDs: rt.Cycle, Statement: "检测到资金回流路径",
			Metrics: map[string]string{
				"cycle": strings.Join(rt.Cycle, "→"), "return_ratio": fmt.Sprintf("%.2f", rt.ReturnRatio),
			},
			Confidence: 0.6, EvidenceIDs: []string{"ev_roundtrip_" + rt.PathID},
		})
	}
	// 守恒异常
	for i, c := range ff.Conservation {
		if c.Pass || i >= 20 {
			continue
		}
		out = append(out, Finding{
			ID: "finding_conservation_" + shortID(c.Address), FindingType: "FLOW_CONSERVATION_ANOMALY",
			SubjectIDs: []string{c.Address}, Statement: c.Reason,
			Metrics: map[string]string{
				"inflow": c.Inflow, "outflow": c.Outflow, "deviation": fmt.Sprintf("%.2f", c.Deviation),
			},
			Confidence: 0.5, EvidenceIDs: []string{"ev_conservation_" + c.Address},
		})
	}
	// 数据缺口
	for _, dc := range in.Coverage {
		if dc.Coverage < 1 || dc.GapCount > 0 {
			out = append(out, Finding{
				ID: "finding_gap_" + dc.Dataset, FindingType: "DATA_GAP",
				SubjectIDs: []string{in.RootAddress},
				Statement:  fmt.Sprintf("数据集 %s 覆盖 %.2f%%，认证 %s，存在 %d 个缺口", dc.Dataset, dc.Coverage*100, dc.Certification, dc.GapCount),
				Metrics: map[string]string{
					"dataset": dc.Dataset, "coverage": fmt.Sprintf("%.4f", dc.Coverage),
					"certification": dc.Certification, "gaps": fmt.Sprintf("%d", dc.GapCount),
				},
				Confidence: 0.9, EvidenceIDs: []string{"ev_gap_" + dc.Dataset},
			})
		}
	}
	return out
}

func pathSubjects(p *fundflow.Path) []string {
	var out []string
	for _, n := range p.Nodes {
		out = append(out, n.Address)
	}
	return out
}

// evidenceRefs 为 Findings 生成证据引用并写入索引。
func evidenceRefs(in *AnalysisInputs, findings []Finding, evidence *EvidenceIndex) {
	ff := in.FundFlow
	for _, p := range ff.Paths {
		evidence.Add(&EvidenceRef{
			ID: "ev_path_" + p.ID, Type: "FLOW_PATH", ChainID: 0,
			Address: p.RootAddress, SourcePath: p.ID, SourceProvider: "FUND_FLOW",
			Certification: "COMPUTED",
		})
	}
	for _, pr := range ff.Profit {
		if pr.Level == "L1" {
			evidence.Add(&EvidenceRef{
				ID: "ev_profit_l1_" + pr.Address, Type: "PROFIT_ATTRIBUTION",
				Address: pr.Address, SourceProvider: "FUND_FLOW", Certification: "COMPUTED",
			})
		}
	}
	for _, s := range ff.Settlements {
		evidence.Add(&EvidenceRef{
			ID: "ev_settlement_" + s.Address, Type: "SETTLEMENT_SCORE",
			Address: s.Address, SourceProvider: "FUND_FLOW", Certification: "COMPUTED",
		})
	}
	for _, c := range ff.Cashouts {
		evidence.Add(&EvidenceRef{
			ID: "ev_cashout_" + shortID(c.DestinationAddress), Type: "CASHOUT_RESULT",
			Address: c.DestinationAddress, TxHash: c.TxHash, SourceProvider: "FUND_FLOW",
			Certification: "COMPUTED",
		})
	}
	for _, rt := range ff.RoundTrips {
		evidence.Add(&EvidenceRef{
			ID: "ev_roundtrip_" + rt.PathID, Type: "FLOW_PATH", SourceProvider: "FUND_FLOW",
			Certification: "COMPUTED",
		})
	}
	for _, c := range ff.Conservation {
		if !c.Pass {
			evidence.Add(&EvidenceRef{
				ID: "ev_conservation_" + c.Address, Type: "FLOW_CONSERVATION_ANOMALY",
				Address: c.Address, SourceProvider: "FUND_FLOW", Certification: "COMPUTED",
			})
		}
	}
	for addr, ent := range in.Entities {
		evidence.Add(&EvidenceRef{
			ID: "ev_entity_" + ent.EntityID, Type: "ENTITY_EVIDENCE",
			Address: addr, SourceProvider: "ENTITY_INTEL", Certification: "COMPUTED",
		})
	}
	for _, dc := range in.Coverage {
		if dc.Coverage < 1 || dc.GapCount > 0 {
			evidence.Add(&EvidenceRef{
				ID: "ev_gap_" + dc.Dataset, Type: "DATASET_CERTIFICATE",
				DatasetID: dc.Dataset, SourceProvider: "COVERAGE_INDEX",
				Certification: dc.Certification,
			})
		}
	}
}
