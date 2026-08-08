package reportengine

import (
	"fmt"
	"strings"

	"github.com/etl/backend/internal/fundflow"
)

// narrativeResult 是叙事渲染结果与一致性检查。
type narrativeResult struct {
	Sections []ReportSection
	PassRate float64
}

// renderNarrative 基于模板骨架 + 结构化 Findings 生成叙事（设计 §17-§21、§64）。
// 数字一律来自 Finding.Metrics，不交给 LLM 计算；生成后做事实一致性检查。
func renderNarrative(in *AnalysisInputs, findings []Finding, timeline []TimelineEvent, evidence *EvidenceIndex) narrativeResult {
	return renderNarrativeLang(in, findings, timeline, evidence, "zh")
}

// renderNarrativeLang 支持中/英（P2 多语言报告）。
func renderNarrativeLang(in *AnalysisInputs, findings []Finding, timeline []TimelineEvent, evidence *EvidenceIndex, lang string) narrativeResult {
	en := strings.EqualFold(lang, "en")
	title := func(zh, enT string) string {
		if en {
			return enT
		}
		return zh
	}
	var sections []ReportSection
	checked := 0
	passed := 0

	// 1) 案件摘要
	summaryNarrative := fmt.Sprintf(
		"目标地址 %s 在调查目标「%s」下共发现 %d 条关键路径、%d 个交易所/服务落点、%d 个沉淀候选、%d 条获利归因（L1）。本报告基于当前已认证数据集生成；数据缺口见数据完整性章节。",
		in.RootAddress, in.Goal, len(in.FundFlow.Paths), len(in.FundFlow.Cashouts),
		len(in.FundFlow.Settlements), countL1(in.FundFlow.Profit))
	if en {
		summaryNarrative = fmt.Sprintf(
			"Root %s under goal %q found %d key paths, %d cashout candidates, %d settlement candidates and %d profit attributions (L1). Generated from certified datasets; gaps are disclosed in Data Integrity.",
			in.RootAddress, in.Goal, len(in.FundFlow.Paths), len(in.FundFlow.Cashouts),
			len(in.FundFlow.Settlements), countL1(in.FundFlow.Profit))
	}
	sections = append(sections, ReportSection{
		ID: "summary", Type: "SUMMARY", Title: title("案件摘要", "Summary"), Narrative: summaryNarrative,
		EvidenceIDs: allEvidenceIDs(findings), Confidence: 0.9,
	})

	// 2) 调查目标与数据范围
	scopeNarrative := fmt.Sprintf(
		"调查目标：%s；链：%s；焦点地址：%s；分析引擎版本 %s；模板版本 %s。",
		in.Goal, in.ChainKey, in.RootAddress, cacheState(in.FundFlow), "v1")
	if en {
		scopeNarrative = fmt.Sprintf("Goal: %s; Chain: %s; Root: %s; Engine %s; Template v1.", in.Goal, in.ChainKey, in.RootAddress, cacheState(in.FundFlow))
	}
	sections = append(sections, ReportSection{
		ID: "scope", Type: "SCOPE", Title: title("调查目标与数据范围", "Scope"), Narrative: scopeNarrative, Confidence: 1,
	})

	// 3) 关键地址/实体
	keyEntities := filterFindings(findings, "KEY_ADDRESS", "KEY_ENTITY")
	sections = append(sections, ReportSection{
		ID: "entities", Type: "ENTITIES", Title: title("关键地址 / 实体", "Key Addresses / Entities"),
		Findings: keyEntities, Narrative: narrativeForLang(entitiesNarrative(keyEntities), entitiesNarrativeEn(keyEntities), en),
		EvidenceIDs: findingEvidence(keyEntities), Confidence: sectionConfidence(keyEntities),
	})

	// 4) 关键资金路径
	paths := filterFindings(findings, "HIGH_VALUE_PATH")
	sections = append(sections, ReportSection{
		ID: "paths", Type: "PATHS", Title: title("关键资金路径", "Key Fund Paths"),
		Findings: paths, Narrative: narrativeForLang(pathsNarrative(paths), pathsNarrativeEn(paths), en),
		EvidenceIDs: findingEvidence(paths), Confidence: sectionConfidence(paths),
	})

	// 5) 获利归因
	profits := filterFindings(findings, "PROFIT_ATTRIBUTION")
	sections = append(sections, ReportSection{
		ID: "profit", Type: "PROFIT", Title: title("获利归因（L0/L1 筛查）", "Profit Attribution (L0/L1)"),
		Findings: profits, Narrative: narrativeForLang(profitNarrative(profits), profitNarrativeEn(profits), en),
		EvidenceIDs: findingEvidence(profits), Confidence: sectionConfidence(profits),
	})

	// 6) 资金沉淀
	settles := filterFindings(findings, "SETTLEMENT")
	sections = append(sections, ReportSection{
		ID: "settlement", Type: "SETTLEMENT", Title: title("资金沉淀", "Settlement"),
		Findings: settles, Narrative: narrativeForLang(settlementNarrative(settles), settlementNarrativeEn(settles), en),
		EvidenceIDs: findingEvidence(settles), Confidence: sectionConfidence(settles),
	})

	// 7) 交易所/服务落点
	cashouts := filterFindings(findings, "EXCHANGE_DEPOSIT", "CASHOUT")
	sections = append(sections, ReportSection{
		ID: "cashout", Type: "CASHOUT", Title: title("交易所 / 服务落点", "Exchange / Service Landing"),
		Findings: cashouts, Narrative: narrativeForLang(cashoutNarrative(cashouts), cashoutNarrativeEn(cashouts), en),
		EvidenceIDs: findingEvidence(cashouts), Confidence: sectionConfidence(cashouts),
	})

	// 8) 风险与异常
	anomalies := filterFindings(findings, "ROUND_TRIP", "FLOW_CONSERVATION_ANOMALY")
	sections = append(sections, ReportSection{
		ID: "anomalies", Type: "ANOMALIES", Title: title("风险与异常", "Risks & Anomalies"),
		Findings: anomalies, Narrative: narrativeForLang(anomalyNarrative(anomalies), anomalyNarrativeEn(anomalies), en),
		EvidenceIDs: findingEvidence(anomalies), Confidence: sectionConfidence(anomalies),
	})

	// 9) 数据完整性说明
	gaps := filterFindings(findings, "DATA_GAP")
	sections = append(sections, ReportSection{
		ID: "integrity", Type: "INTEGRITY", Title: title("数据完整性说明", "Data Integrity"),
		Findings: gaps, Narrative: narrativeForLang(integrityNarrative(in.Coverage, gaps), integrityNarrativeEn(in.Coverage, gaps), en),
		EvidenceIDs: findingEvidence(gaps), Confidence: 0.95,
	})

	// 10) 证据清单（附录）
	sections = append(sections, ReportSection{
		ID: "evidence", Type: "EVIDENCE", Title: title("证据清单", "Evidence Index"),
		Findings: nil, Narrative: fmt.Sprintf("本报告引用 %d 条结构化证据；每条结论均可在报告中追溯 Evidence ID 与哈希。", len(evidence.List())),
		EvidenceIDs: allEvidenceIDs(findings), Confidence: 1,
	})
	if en {
		sections[len(sections)-1].Narrative = fmt.Sprintf("This report cites %d structured evidence records; every finding can be traced to Evidence IDs and hashes.", len(evidence.List()))
	}

	// 事实一致性检查：每个 Finding 的 Metrics 值必须出现在所属章节叙事中
	for _, sec := range sections {
		for _, f := range sec.Findings {
			checked++
			if metricsInNarrative(sec.Narrative, f.Metrics) {
				passed++
			}
		}
	}
	passRate := 0.0
	if checked > 0 {
		passRate = float64(passed) / float64(checked)
	}
	return narrativeResult{Sections: sections, PassRate: passRate}
}

func narrativeForLang(zh, en string, useEn bool) string {
	if useEn {
		return en
	}
	return zh
}

func entitiesNarrativeEn(findings []Finding) string {
	if len(findings) == 0 {
		return "No attributable key entities found."
	}
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		parts = append(parts, fmt.Sprintf("%s (%s, confidence %.0f%%)", f.Metrics["entity"], f.Metrics["type"], f.Confidence*100))
	}
	return "Key entities: " + strings.Join(parts, "; ") + "."
}

func pathsNarrativeEn(findings []Finding) string {
	if len(findings) == 0 {
		return "No high-value key paths found."
	}
	parts := make([]string, 0, min(len(findings), 5))
	for _, f := range findings {
		parts = append(parts, fmt.Sprintf("%s path (%s, amount %s, score %s, %s hops, confidence %s)",
			f.Metrics["path_type"], f.Metrics["terminal"], f.Metrics["amount"], f.Metrics["score"],
			f.Metrics["hops"], f.Metrics["confidence"]))
	}
	return "Top key paths: " + strings.Join(parts, "; ") + "."
}

func profitNarrativeEn(findings []Finding) string {
	if len(findings) == 0 {
		return "No positive L1 net-profit addresses found."
	}
	parts := make([]string, 0, min(len(findings), 5))
	for _, f := range findings {
		parts = append(parts, fmt.Sprintf("%s net profit %s (level %s, in %s / out %s, confidence %s)",
			f.SubjectIDs[0], f.Metrics["net_profit"], f.Metrics["level"],
			f.Metrics["gross_inflow"], f.Metrics["gross_outflow"], f.Metrics["confidence"]))
	}
	return "Profit attribution (L1 screening): " + strings.Join(parts, "; ") + "."
}

func settlementNarrativeEn(findings []Finding) string {
	if len(findings) == 0 {
		return "No significant settlement addresses found."
	}
	parts := make([]string, 0, min(len(findings), 5))
	for _, f := range findings {
		parts = append(parts, fmt.Sprintf("%s retained %s (%s, score %s, confidence %s)",
			f.SubjectIDs[0], f.Metrics["retained"], f.Metrics["type"], f.Metrics["score"], f.Metrics["confidence"]))
	}
	return "Settlement candidates: " + strings.Join(parts, "; ") + "."
}

func cashoutNarrativeEn(findings []Finding) string {
	if len(findings) == 0 {
		return "No exchange/service landing found."
	}
	parts := make([]string, 0, min(len(findings), 5))
	for _, f := range findings {
		parts = append(parts, fmt.Sprintf("%s (%s, amount %s %s, path %s, confidence %s)",
			f.Metrics["entity"], f.SubjectIDs[1], f.Metrics["amount"], f.Metrics["token"],
			f.Metrics["path_type"], f.Metrics["confidence"]))
	}
	return "Exchange/service landing: " + strings.Join(parts, "; ") + "."
}

func anomalyNarrativeEn(findings []Finding) string {
	if len(findings) == 0 {
		return "No round trips or conservation anomalies found."
	}
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		switch f.FindingType {
		case "ROUND_TRIP":
			parts = append(parts, fmt.Sprintf("Round trip %s (return ratio %s)", f.Metrics["cycle"], f.Metrics["return_ratio"]))
		case "FLOW_CONSERVATION_ANOMALY":
			parts = append(parts, fmt.Sprintf("Conservation anomaly: %s in %s / out %s / deviation %s", f.SubjectIDs[0],
				f.Metrics["inflow"], f.Metrics["outflow"], f.Metrics["deviation"]))
		default:
			parts = append(parts, f.Statement)
		}
	}
	return strings.Join(parts, "; ") + "."
}

func integrityNarrativeEn(coverage []DatasetCertification, gaps []Finding) string {
	if len(gaps) == 0 {
		return "All datasets are certified within the current scope; conclusions hold only for currently available data."
	}
	parts := make([]string, 0, len(coverage))
	for _, c := range coverage {
		parts = append(parts, fmt.Sprintf("%s coverage %.2f%% (%s)", c.Dataset, c.Coverage*100, c.Certification))
	}
	for _, f := range gaps {
		parts = append(parts, fmt.Sprintf("gap: %s coverage %s%%, certification %s, gaps %s",
			f.Metrics["dataset"], f.Metrics["coverage"], f.Metrics["certification"], f.Metrics["gaps"]))
	}
	return "Data integrity disclosure: " + strings.Join(parts, "; ") + ". Gaps exist; this report does not assert complete confirmation of all fund paths."
}

func entitiesNarrative(findings []Finding) string {
	if len(findings) == 0 {
		return "未发现可归因的关键实体。"
	}
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		parts = append(parts, fmt.Sprintf("%s（%s，置信度 %.0f%%）",
			f.Metrics["entity"], f.Metrics["type"], f.Confidence*100))
		if f.Metrics["labels"] != "" {
			parts[len(parts)-1] += "（标签：" + f.Metrics["labels"] + "）"
		}
	}
	return "关键实体：" + strings.Join(parts, "；") + "。"
}

func pathsNarrative(findings []Finding) string {
	if len(findings) == 0 {
		return "未发现高价值关键路径。"
	}
	parts := make([]string, 0, min(len(findings), 5))
	for _, f := range findings {
		parts = append(parts, fmt.Sprintf("%s 路径（%s，金额 %s，评分 %s，%s 跳，置信度 %s）",
			f.Metrics["path_type"], f.Metrics["terminal"], f.Metrics["amount"], f.Metrics["score"],
			f.Metrics["hops"], f.Metrics["confidence"]))
	}
	return "Top 关键路径：" + strings.Join(parts, "；") + "。"
}

func profitNarrative(findings []Finding) string {
	if len(findings) == 0 {
		return "未发现 L1 净获利为正的地址。"
	}
	parts := make([]string, 0, min(len(findings), 5))
	for _, f := range findings {
		parts = append(parts, fmt.Sprintf("%s 净获利 %s（级别 %s，流入 %s / 流出 %s，置信度 %s）",
			f.SubjectIDs[0], f.Metrics["net_profit"], f.Metrics["level"],
			f.Metrics["gross_inflow"], f.Metrics["gross_outflow"], f.Metrics["confidence"]))
	}
	return "获利归因（L1 筛查）：" + strings.Join(parts, "；") + "。"
}

func settlementNarrative(findings []Finding) string {
	if len(findings) == 0 {
		return "未发现显著资金沉淀地址。"
	}
	parts := make([]string, 0, min(len(findings), 5))
	for _, f := range findings {
		parts = append(parts, fmt.Sprintf("%s 留存 %s（类型 %s，沉淀分 %s，置信度 %s）",
			f.SubjectIDs[0], f.Metrics["retained"], f.Metrics["type"], f.Metrics["score"], f.Metrics["confidence"]))
	}
	return "资金沉淀候选：" + strings.Join(parts, "；") + "。"
}

func cashoutNarrative(findings []Finding) string {
	if len(findings) == 0 {
		return "未发现交易所/服务落点。"
	}
	parts := make([]string, 0, min(len(findings), 5))
	for _, f := range findings {
		parts = append(parts, fmt.Sprintf("%s（%s，金额 %s %s，路径 %s，置信度 %s）",
			f.Metrics["entity"], f.SubjectIDs[1], f.Metrics["amount"], f.Metrics["token"],
			f.Metrics["path_type"], f.Metrics["confidence"]))
	}
	return "交易所/服务落点：" + strings.Join(parts, "；") + "。"
}

func anomalyNarrative(findings []Finding) string {
	if len(findings) == 0 {
		return "未发现回流或资金守恒异常。"
	}
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		switch f.FindingType {
		case "ROUND_TRIP":
			parts = append(parts, fmt.Sprintf("回流路径 %s（回流比 %s）", f.Metrics["cycle"], f.Metrics["return_ratio"]))
		case "FLOW_CONSERVATION_ANOMALY":
			parts = append(parts, fmt.Sprintf("守恒异常：%s 流入 %s / 流出 %s / 偏差 %s", f.SubjectIDs[0],
				f.Metrics["inflow"], f.Metrics["outflow"], f.Metrics["deviation"]))
		default:
			parts = append(parts, f.Statement)
		}
	}
	return strings.Join(parts, "；") + "。"
}

func integrityNarrative(coverage []DatasetCertification, gaps []Finding) string {
	if len(gaps) == 0 {
		return "当前范围内数据集均通过认证，未披露缺口；结论仅在当前可用数据范围内成立。"
	}
	parts := make([]string, 0, len(coverage))
	for _, c := range coverage {
		parts = append(parts, fmt.Sprintf("%s 覆盖 %.2f%%（%s）", c.Dataset, c.Coverage*100, c.Certification))
	}
	for _, f := range gaps {
		parts = append(parts, fmt.Sprintf("缺口：%s 覆盖 %s%%，认证 %s，缺口数 %s",
			f.Metrics["dataset"], f.Metrics["coverage"], f.Metrics["certification"], f.Metrics["gaps"]))
	}
	return "数据完整性披露：" + strings.Join(parts, "；") + "。存在数据缺口，本报告不作出“完整确认全部资金路径”的结论。"
}

func filterFindings(findings []Finding, types ...string) []Finding {
	var out []Finding
	set := map[string]bool{}
	for _, t := range types {
		set[t] = true
	}
	for _, f := range findings {
		if set[f.FindingType] {
			out = append(out, f)
		}
	}
	return out
}

func findingEvidence(findings []Finding) []string {
	var out []string
	for _, f := range findings {
		out = append(out, f.EvidenceIDs...)
	}
	return out
}

func allEvidenceIDs(findings []Finding) []string {
	return findingEvidence(findings)
}

func sectionConfidence(findings []Finding) float64 {
	if len(findings) == 0 {
		return 0
	}
	conf := findings[0].Confidence
	for _, f := range findings {
		if f.Confidence < conf {
			conf = f.Confidence
		}
	}
	return conf
}

func metricsInNarrative(narrative string, metrics map[string]string) bool {
	for _, v := range metrics {
		if v == "" {
			continue
		}
		if !strings.Contains(narrative, v) {
			return false
		}
	}
	return true
}

func countL1(items []*fundflow.ProfitAttribution) int {
	n := 0
	for _, p := range items {
		if p.Level == "L1" {
			n++
		}
	}
	return n
}

func cacheState(res *fundflow.AnalysisResult) string {
	if res == nil {
		return "v2"
	}
	if res.CacheHit {
		return "v2(cache)"
	}
	return "v2"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
