package intelligence

import (
	"fmt"
	"strings"
)

// ── AI Context Builder ──
//
// 不直接发送百万交易，而是将 DuckDB 分析结果转换为精简摘要上下文：
// 地址画像 / Top 路径 / 风险事件 / 实体类型 / 时间线。

// AIContextBuilder 构建 DeepSeek 上下文。
type AIContextBuilder struct {
	cfg IntelligenceConfig
}

// NewAIContextBuilder 创建上下文构建器。
func NewAIContextBuilder(cfg IntelligenceConfig) *AIContextBuilder {
	return &AIContextBuilder{cfg: cfg}
}

// Build 从调查结果构建 AI 上下文（分析摘要）。
func (b *AIContextBuilder) Build(inv *Investigation) *AIContext {
	ctx := &AIContext{
		Target:     inv.Target,
		Profile:    map[string]any{},
		TopPaths:   []string{},
		RiskEvents: []string{},
		Entities:   []string{},
		Timeline:   []string{},
	}
	// V2：注入调查请求（目的/期望结果/模式），AI 规划围绕用户目标（设计 §3/§4）
	if inv.Request != nil {
		ctx.Objective = inv.Request.Objective
		ctx.ExpectedResult = inv.Request.ExpectedResult
		ctx.Mode = string(inv.Request.Mode)
		if inv.Request.Intent != nil {
			ctx.Profile["intent"] = inv.Request.Intent.Summary
		}
	}

	// 画像摘要
	if inv.Memory != nil {
		ctx.Profile["target"] = inv.Target
	}
	for _, e := range inv.Entities {
		if strings.EqualFold(e.Address, inv.Target) {
			ctx.Profile["entity"] = e.Entity
			ctx.Profile["label"] = e.Label
			ctx.Profile["risk"] = e.Risk
			ctx.Profile["tx_count"] = e.TxCount
		}
	}

	// Top 路径摘要（取前 5）
	for i, p := range inv.Paths {
		if i >= 5 {
			break
		}
		ctx.TopPaths = append(ctx.TopPaths, p.Summary)
	}

	// 风险事件摘要
	for _, p := range inv.Patterns {
		ctx.RiskEvents = append(ctx.RiskEvents, fmt.Sprintf("[%s] %s: %s", p.Severity, p.Type, p.Detail))
	}

	// 实体摘要
	seen := map[string]bool{}
	for _, e := range inv.Entities {
		if seen[e.Entity] || e.Entity == "" || e.Entity == "unknown" {
			continue
		}
		seen[e.Entity] = true
		ctx.Entities = append(ctx.Entities, fmt.Sprintf("%s(%s)", e.Address[:minInt(10, len(e.Address))]+"...", e.Entity))
	}

	// 时间线摘要（路径首尾时间）
	for _, p := range inv.Paths {
		if len(p.Path.Edges) > 0 {
			first := p.Path.Edges[0]
			last := p.Path.Edges[len(p.Path.Edges)-1]
			ctx.Timeline = append(ctx.Timeline, fmt.Sprintf("%s → %s (%s %s, block %d-%d)",
				first.From[:minInt(8, len(first.From))]+"...",
				last.To[:minInt(8, len(last.To))]+"...",
				first.Token, first.Amount, first.Block, last.Block))
		}
	}
	if len(ctx.Timeline) > 10 {
		ctx.Timeline = ctx.Timeline[:10]
	}
	return ctx
}

// ToPrompt 将上下文转换为 DeepSeek 提示词。
func (b *AIContextBuilder) ToPrompt(ctx *AIContext) string {
	var sb strings.Builder
	sb.WriteString("你是链上资金调查专家。请基于以下分析摘要给出调查结论。\n\n")
	sb.WriteString(fmt.Sprintf("目标地址：%s\n\n", ctx.Target))
	sb.WriteString("## 地址画像\n")
	if len(ctx.Profile) > 0 {
		for k, v := range ctx.Profile {
			sb.WriteString(fmt.Sprintf("- %s: %v\n", k, v))
		}
	} else {
		sb.WriteString("- 无画像数据\n")
	}
	sb.WriteString("\n## Top 资金路径\n")
	if len(ctx.TopPaths) > 0 {
		for i, p := range ctx.TopPaths {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, p))
		}
	} else {
		sb.WriteString("- 未发现显著路径\n")
	}
	sb.WriteString("\n## 风险事件\n")
	if len(ctx.RiskEvents) > 0 {
		for _, r := range ctx.RiskEvents {
			sb.WriteString(fmt.Sprintf("- %s\n", r))
		}
	} else {
		sb.WriteString("- 未发现显著风险\n")
	}
	sb.WriteString("\n## 关联实体\n")
	if len(ctx.Entities) > 0 {
		sb.WriteString("- " + strings.Join(ctx.Entities, ", ") + "\n")
	} else {
		sb.WriteString("- 未识别关联实体\n")
	}
	sb.WriteString("\n## 时间线\n")
	if len(ctx.Timeline) > 0 {
		for _, t := range ctx.Timeline {
			sb.WriteString(fmt.Sprintf("- %s\n", t))
		}
	}
	sb.WriteString("\n请输出：\n1. 资金行为总结（2-3 句）\n2. 洞察列表（3-5 条）\n3. 下一步调查建议（2-3 条）\n4. 风险评价（1-2 句）\n")
	return sb.String()
}
