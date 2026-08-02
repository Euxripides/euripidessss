package intelligence

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"
)

// ── Report Agent ──
//
// 生成：Markdown 报告 / HTML 报告 / JSON 报告。
// 内容：地址画像、资金路径、风险分析、图谱关系、证据列表。

// ReportAgent 生成调查报告。
type ReportAgent struct {
	cfg IntelligenceConfig
}

// NewReportAgent 创建报告代理。
func NewReportAgent(cfg IntelligenceConfig) *ReportAgent {
	return &ReportAgent{cfg: cfg}
}

// Generate 生成指定格式报告。
func (r *ReportAgent) Generate(inv *Investigation, format ReportFormat) (*ReportOutput, error) {
	switch format {
	case ReportMarkdown:
		return &ReportOutput{Format: format, Content: r.generateMarkdown(inv), Filename: inv.ID + "-report.md"}, nil
	case ReportHTML:
		return &ReportOutput{Format: format, Content: r.generateHTML(inv), Filename: inv.ID + "-report.html"}, nil
	case ReportJSON:
		data, err := json.MarshalIndent(inv, "", "  ")
		if err != nil {
			return nil, err
		}
		return &ReportOutput{Format: format, Content: string(data), Filename: inv.ID + "-report.json"}, nil
	default:
		return nil, fmt.Errorf("不支持的报告格式: %s", format)
	}
}

// generateMarkdown 生成 Markdown 报告。
func (r *ReportAgent) generateMarkdown(inv *Investigation) string {
	var b strings.Builder
	b.WriteString("# 链上自动调查报告\n\n")
	b.WriteString(fmt.Sprintf("- 调查编号：%s\n", inv.ID))
	b.WriteString(fmt.Sprintf("- 目标地址：`%s`\n", inv.Target))
	b.WriteString(fmt.Sprintf("- 链：%s\n", inv.ChainID))
	b.WriteString(fmt.Sprintf("- 时间：%s\n", inv.CreatedAt.Format("2006-01-02 15:04:05")))
	b.WriteString(fmt.Sprintf("- 状态：%s\n\n", inv.Status))

	// ── V2 调查请求（设计 §3/§4）──
	if inv.Request != nil {
		b.WriteString("## 调查请求\n\n")
		if inv.Request.Objective != "" {
			b.WriteString(fmt.Sprintf("- 调查目的：%s\n", inv.Request.Objective))
		}
		if len(inv.Request.ExpectedResult) > 0 {
			b.WriteString(fmt.Sprintf("- 期望结果：%s\n", strings.Join(inv.Request.ExpectedResult, "、")))
		}
		b.WriteString(fmt.Sprintf("- 调查模式：%s\n", inv.Request.Mode))
		if inv.Request.Intent != nil {
			b.WriteString(fmt.Sprintf("- 意图分析：%s\n\n", inv.Request.Intent.Summary))
		} else {
			b.WriteString("\n")
		}
	}

	// ── V2 六维调查价值评分（设计 §9-11）──
	if inv.Score != nil {
		b.WriteString("## 调查价值评分\n\n")
		b.WriteString(fmt.Sprintf("- 总分：**%.1f**\n", inv.Score.Total))
		b.WriteString(fmt.Sprintf("- 资金价值 %.1f / 行为价值 %.1f / 风险价值 %.1f\n", inv.Score.Fund, inv.Score.Behavior, inv.Score.Risk))
		b.WriteString(fmt.Sprintf("- 实体价值 %.1f / 图价值 %.1f / 身份价值 %.1f\n", inv.Score.Entity, inv.Score.Graph, inv.Score.Identity))
		if inv.Score.FundDetail != nil {
			b.WriteString(fmt.Sprintf("- 资金分项：余额 %v + 获利 %v + 沉淀 %v = %v\n", inv.Score.FundDetail.BalancePoints, inv.Score.FundDetail.ProfitPoints, inv.Score.FundDetail.HoldingPoints, inv.Score.FundDetail.Total))
		}
		b.WriteString("\n")
	}

	// 一、调查计划
	b.WriteString("## 一、调查计划\n\n")
	if inv.Plan != nil {
		if inv.Strategy != nil {
			b.WriteString(fmt.Sprintf("- AI 策略：%s（置信度 %.2f）\n", inv.Strategy.Strategy, inv.Strategy.Confidence))
			if inv.Strategy.Rationale != "" {
				b.WriteString(fmt.Sprintf("- 策略理由：%s\n", inv.Strategy.Rationale))
			}
		}
		if inv.Plan.Mode != "" {
			b.WriteString(fmt.Sprintf("- 计划模式：%s", inv.Plan.Mode))
			if inv.Plan.EstimatedMinutes > 0 {
				b.WriteString(fmt.Sprintf("（预计 %d 分钟）", inv.Plan.EstimatedMinutes))
			}
			b.WriteString("\n")
		}
		for _, t := range inv.Plan.Tasks {
			b.WriteString(fmt.Sprintf("- [%s] %s\n", t.Type, t.Description))
		}
	} else {
		b.WriteString("- 无计划\n")
	}

	// 二、地址画像与实体
	b.WriteString("\n## 二、地址画像与实体\n\n")
	for _, e := range inv.Entities {
		b.WriteString(fmt.Sprintf("- `%s`：%s", e.Address, e.Entity))
		if e.Label != "" {
			b.WriteString(fmt.Sprintf("（%s）", e.Label))
		}
		b.WriteString(fmt.Sprintf("，风险 %.1f，交易 %d 笔\n", e.Risk, e.TxCount))
	}

	// 三、资金路径（Top）
	b.WriteString("\n## 三、资金路径（Top）\n\n")
	if len(inv.Paths) > 0 {
		for i, p := range inv.Paths {
			if i >= r.cfg.TopPaths {
				break
			}
			b.WriteString(fmt.Sprintf("%d. `%s`（%.1f 分）\n", i+1, strings.Join(p.Path.Nodes, " → "), p.Score.Total))
			for _, e := range p.Path.Edges {
				b.WriteString(fmt.Sprintf("   - %s %s %s（tx `%s`，block %d）\n",
					e.Token, e.Amount, timeFmt(e.Timestamp), e.TxHash, e.Block))
			}
		}
	} else {
		b.WriteString("- 未发现资金路径\n")
	}

	// 四、风险分析
	b.WriteString("\n## 四、风险分析\n\n")
	if len(inv.Patterns) > 0 {
		for _, p := range inv.Patterns {
			b.WriteString(fmt.Sprintf("- [%s] %s：%s\n", p.Severity, p.Type, p.Detail))
		}
	} else {
		b.WriteString("- 未发现显著风险模式\n")
	}

	// ── V2.1 获利/沉淀检测（设计 §10/V2.1 §2：估算 + 可信度 + 依据）──
	if inv.Profit != nil {
		b.WriteString("\n## 获利与沉淀检测\n\n")
		b.WriteString(fmt.Sprintf("- 检测结果：%s\n", inv.Profit.Summary))
		if inv.Profit.EstimateUSD > 0 {
			b.WriteString(fmt.Sprintf("- 估算金额：**%.0f**（稳定币净额估算）\n", inv.Profit.EstimateUSD))
		}
		if inv.Profit.Confidence > 0 {
			b.WriteString(fmt.Sprintf("- 可信度：**%.0f%%**\n", inv.Profit.Confidence*100))
		}
		if len(inv.Profit.Checklist) > 0 {
			b.WriteString("- 依据明细：\n")
			for _, c := range inv.Profit.Checklist {
				mark := "✗"
				if !c.Present {
					mark = "?"
				} else if c.OK {
					mark = "✓"
				}
				b.WriteString(fmt.Sprintf("  - %s %s\n", mark, c.Label))
			}
		}
		if inv.Profit.Tokens != nil && len(inv.Profit.Tokens) > 0 {
			b.WriteString(fmt.Sprintf("- 涉及 Token：%s\n", strings.Join(inv.Profit.Tokens, "、")))
		}
		b.WriteString(fmt.Sprintf("- 估算口径：%s\n", inv.Profit.EstimateNote))
	}

	// 五、地址扩展
	b.WriteString("\n## 五、地址扩展\n\n")
	if len(inv.Expansions) > 0 {
		count := 0
		for _, e := range inv.Expansions {
			if e.Depth == 0 {
				continue
			}
			b.WriteString(fmt.Sprintf("- `%s`：%s（%s，score %.1f）\n", e.Address, e.Entity, e.Acquisition, e.Score))
			count++
			if count >= 20 {
				b.WriteString(fmt.Sprintf("- ... 共 %d 个扩展地址\n", len(inv.Expansions)-1))
				break
			}
		}
	}

	// 六、调查过程（闭环追踪，§18 全过程可追踪）
	b.WriteString("\n## 六、调查过程（闭环追踪）\n\n")
	if inv.Round > 0 {
		b.WriteString(fmt.Sprintf("- 调查轮次：%d\n", inv.Round))
	}
	if len(inv.Rounds) > 0 {
		for _, rc := range inv.Rounds {
			b.WriteString(fmt.Sprintf("- 第 %d 轮决策：%s — %s\n", rc.Round, rc.Decision, rc.Note))
		}
	}
	if inv.StopReason != "" {
		if inv.StopCode != "" {
			b.WriteString(fmt.Sprintf("- 停止原因：[%s] %s\n", inv.StopCode, inv.StopReason))
		} else {
			b.WriteString(fmt.Sprintf("- 停止原因：%s\n", inv.StopReason))
		}
	}
	if len(inv.Tasks) > 0 {
		taskCounts := map[string]int{}
		for _, tk := range inv.Tasks {
			taskCounts[tk.Status]++
		}
		b.WriteString(fmt.Sprintf("- 任务执行：done %d / skipped %d / failed %d（共 %d 个任务）\n",
			taskCounts[TaskDone], taskCounts[TaskSkipped], taskCounts[TaskFailed], len(inv.Tasks)))
		if len(inv.Tasks) <= 20 {
			for _, tk := range inv.Tasks {
				b.WriteString(fmt.Sprintf("  - [%s] %s %s %s\n", tk.Status, tk.Type, shortAddr(tk.Target), tk.Result))
			}
		}
	}
	if len(inv.Observations) > 0 {
		obsCounts := map[ObservationType]int{}
		for _, o := range inv.Observations {
			obsCounts[o.Type]++
		}
		b.WriteString(fmt.Sprintf("- 观察结果：新地址 %d / 新路径 %d / 新交易 %d / 风险事件 %d\n",
			obsCounts[ObsNewAddress], obsCounts[ObsNewPath], obsCounts[ObsNewTransaction], obsCounts[ObsRiskEvent]))
	}

	// 七、调查假设与已验证发现（§7/§12）
	b.WriteString("\n## 七、调查假设与已验证发现\n\n")
	if len(inv.Hypotheses) > 0 {
		for _, h := range inv.Hypotheses {
			b.WriteString(fmt.Sprintf("- [%s] %s（置信度 %.2f，来源 %s）\n", h.Status, h.Title, h.Confidence, h.Source))
			if h.Description != "" {
				b.WriteString(fmt.Sprintf("  - %s\n", h.Description))
			}
			if h.Note != "" {
				b.WriteString(fmt.Sprintf("  - 状态说明：%s\n", h.Note))
			}
		}
	} else {
		b.WriteString("- 未生成调查假设\n")
	}
	if len(inv.Findings) > 0 {
		b.WriteString("\n已验证发现（Evidence Guard）：\n")
		for _, vf := range inv.Findings {
			b.WriteString(fmt.Sprintf("- [%s] %s（%s，置信度 %.2f）：%s\n", vf.Status, vf.Finding.Type, shortAddr(vf.Finding.Address), vf.Finding.Confidence, vf.Finding.Detail))
			if len(vf.Finding.Evidence) > 0 {
				b.WriteString(fmt.Sprintf("  - 证据：%s\n", strings.Join(vf.Finding.Evidence, ", ")))
			}
		}
	}

	// 八、AI 分析
	b.WriteString("\n## 八、AI 分析\n\n")
	if inv.AI != nil {
		b.WriteString(fmt.Sprintf("### 总结\n%s\n\n", inv.AI.Summary))
		if len(inv.AI.Insights) > 0 {
			b.WriteString("### 洞察\n")
			for _, ins := range inv.AI.Insights {
				b.WriteString(fmt.Sprintf("- %s\n", ins))
			}
		}
		if len(inv.AI.Suggestions) > 0 {
			b.WriteString("\n### 下一步建议\n")
			for _, s := range inv.AI.Suggestions {
				b.WriteString(fmt.Sprintf("- %s\n", s))
			}
		}
		if inv.AI.RiskComment != "" {
			b.WriteString(fmt.Sprintf("\n### 风险评价\n%s\n", inv.AI.RiskComment))
		}
		b.WriteString(fmt.Sprintf("\n> 模型：%s，耗时 %dms\n", inv.AI.Model, inv.AI.DurationMs))
	} else {
		b.WriteString("- 未启用 AI 分析\n")
	}

	// 九、证据与结论
	b.WriteString("\n## 九、调查结论\n\n")
	if inv.Memory != nil && len(inv.Memory.Conclusions) > 0 {
		for _, c := range inv.Memory.Conclusions {
			b.WriteString(fmt.Sprintf("- %s\n", c))
		}
	}
	if inv.Memory != nil && len(inv.Memory.AnalyzedPaths) > 0 {
		b.WriteString(fmt.Sprintf("\n已分析路径：%d 条\n", len(inv.Memory.AnalyzedPaths)))
	}
	return b.String()
}

// generateHTML 生成自包含 HTML 报告。
func (r *ReportAgent) generateHTML(inv *Investigation) string {
	md := r.generateMarkdown(inv)
	body := html.EscapeString(md)
	// 简单 md → html：代码块与换行
	body = strings.ReplaceAll(body, "`", "<code>")
	lines := strings.Split(body, "\n")
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html lang=\"zh\"><head><meta charset=\"utf-8\">")
	sb.WriteString("<title>链上自动调查报告 " + html.EscapeString(inv.ID) + "</title>")
	sb.WriteString("<style>body{font-family:system-ui,sans-serif;max-width:900px;margin:2rem auto;padding:0 1rem;line-height:1.7;color:#1f2328}")
	sb.WriteString("h1{color:#1664ff}h2{border-bottom:1px solid #e2e2e2;padding-bottom:.3rem;margin-top:2rem}code{background:#f3f4f6;padding:1px 6px;border-radius:4px}")
	sb.WriteString("table{border-collapse:collapse}td,th{border:1px solid #ddd;padding:6px 10px}</style></head><body>")
	sb.WriteString("<h1>链上自动调查报告</h1>")
	sb.WriteString("<p>调查编号：<code>" + html.EscapeString(inv.ID) + "</code></p>")
	sb.WriteString("<p>目标地址：<code>" + html.EscapeString(inv.Target) + "</code></p>")
	sb.WriteString("<p>时间：" + inv.CreatedAt.Format("2006-01-02 15:04:05") + "</p>")
	sb.WriteString("<p>状态：" + html.EscapeString(string(inv.Status)) + "</p>")
	inTable := false
	for _, line := range lines {
		line = html.UnescapeString(line)
		line = html.EscapeString(line)
		switch {
		case strings.HasPrefix(line, "# "):
			sb.WriteString("<h1>" + line[2:] + "</h1>")
		case strings.HasPrefix(line, "## "):
			sb.WriteString("<h2>" + line[3:] + "</h2>")
		case strings.HasPrefix(line, "### "):
			sb.WriteString("<h3>" + line[4:] + "</h3>")
		case strings.HasPrefix(line, "- "):
			sb.WriteString("<li>" + line[2:] + "</li>")
		case strings.HasPrefix(line, "1. ") || strings.HasPrefix(line, "2. ") ||
			strings.HasPrefix(line, "3. ") || strings.HasPrefix(line, "4. ") ||
			strings.HasPrefix(line, "5. ") || strings.HasPrefix(line, "6. ") ||
			strings.HasPrefix(line, "7. "):
			sb.WriteString("<li>" + line[3:] + "</li>")
		case strings.HasPrefix(line, "| "):
			if !inTable {
				sb.WriteString("<table>")
				inTable = true
			}
			cells := strings.Split(strings.Trim(line, "| "), "|")
			sb.WriteString("<tr>")
			for _, cell := range cells {
				sb.WriteString("<td>" + strings.TrimSpace(cell) + "</td>")
			}
			sb.WriteString("</tr>")
		case strings.HasPrefix(line, "> "):
			sb.WriteString("<blockquote>" + line[2:] + "</blockquote>")
		case line == "":
			if inTable {
				sb.WriteString("</table>")
				inTable = false
			}
			sb.WriteString("<br>")
		default:
			sb.WriteString("<p>" + line + "</p>")
		}
	}
	if inTable {
		sb.WriteString("</table>")
	}
	sb.WriteString("</body></html>")
	return sb.String()
}

// timeFmt 格式化时间戳（缺失时返回 "?"）。
func timeFmt(ts int64) string {
	if ts <= 0 {
		return "?"
	}
	return time.Unix(ts, 0).UTC().Format("2006-01-02 15:04")
}
