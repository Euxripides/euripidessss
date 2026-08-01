package intelligence

import (
	"fmt"
	"strings"
)

// ── Prompt Builder（设计 §10）──
//
// 多角色提示词设计：
//   Investigator    负责调查方向（生成策略 + 结构化任务）
//   AML Analyst     负责风险模式（生成结构化发现，带置信度与证据）
//   Forensic Analyst 负责证据分析（路径/行为解释）
//   Report Writer   负责报告总结
// 所有角色输出必须为结构化 JSON（§11），版本经 PromptVersion 管理（§17）。

// AIRole 是提示词角色。
type AIRole string

const (
	RoleInvestigator    AIRole = "investigator"
	RoleAMLAnalyst      AIRole = "aml_analyst"
	RoleForensicAnalyst AIRole = "forensic_analyst"
	RoleReportWriter    AIRole = "report_writer"
)

// PromptVersion 是提示词版本（§17 版本管理）。
const PromptVersion = "v1.0"

// PromptBuilder 构建多角色提示词。
type PromptBuilder struct {
	cfg IntelligenceConfig
}

// NewPromptBuilder 创建提示词构建器。
func NewPromptBuilder(cfg IntelligenceConfig) *PromptBuilder {
	return &PromptBuilder{cfg: cfg}
}

// SystemPrompt 返回角色系统提示词。
func (b *PromptBuilder) SystemPrompt(role AIRole) string {
	switch role {
	case RoleInvestigator:
		return "你是链上资金调查的 Investigator，负责制定调查方向与策略。你只输出 JSON，不输出任何解释文字。"
	case RoleAMLAnalyst:
		return "你是 AML（反洗钱）分析师，负责识别资金风险模式。你只输出 JSON，不输出任何解释文字。"
	case RoleForensicAnalyst:
		return "你是链上取证（Forensic）分析师，负责对资金路径与行为进行证据化分析。你只输出 JSON，不输出任何解释文字。"
	case RoleReportWriter:
		return "你是调查报告撰写者，负责将已验证的发现组织为结论。你只输出 JSON，不输出任何解释文字。"
	}
	return "你是链上资金调查专家，只输出 JSON。"
}

// PlanPrompt 构建调查规划提示词（Investigator 角色，输出 AIStrategy）。
// 历史记忆经 ctx.History 注入（§13 AI Memory 摘要）。
func (b *PromptBuilder) PlanPrompt(ctx *AIContext) string {
	var sb strings.Builder
	sb.WriteString("请基于以下调查上下文制定调查策略。\n\n")
	sb.WriteString(b.contextSection(ctx))
	sb.WriteString("\n## 历史调查记忆\n")
	if len(ctx.History) > 0 {
		for _, m := range ctx.History {
			sb.WriteString(fmt.Sprintf("- %s\n", m))
		}
	} else {
		sb.WriteString("- 无历史记忆\n")
	}
	sb.WriteString(fmt.Sprintf(`
## 输出要求（严格 JSON，不要 Markdown 围栏）
{
  "strategy": "trace_outgoing | trace_incoming | entity_focus | risk_scan | deep_probe",
  "rationale": "策略理由（中文，2-3 句）",
  "confidence": 0.0-1.0,
  "tasks": [
    {"type": "PATH_TRACE | FLOW_ANALYSIS | ENTITY_CHECK | RISK_SCAN | EXPAND_ADDRESS | ADDRESS_PROFILE",
     "priority": 0.0-1.0,
     "target": "0x地址（可选）",
     "reason": "任务理由（中文）"}
  ]
}
最多 5 个任务，priority 高的排前面。`))
	return sb.String()
}

// HypothesisPrompt 构建假设生成提示词（AML Analyst 角色，输出 AIHypothesis 列表）。
func (b *PromptBuilder) HypothesisPrompt(ctx *AIContext, observations []Observation) string {
	var sb strings.Builder
	sb.WriteString("请基于以下观察结果提出资金调查假设。\n\n")
	sb.WriteString(b.contextSection(ctx))
	sb.WriteString("\n## 本次观察\n")
	if len(observations) > 0 {
		for i, o := range observations {
			if i >= 10 {
				sb.WriteString(fmt.Sprintf("- ... 共 %d 条观察\n", len(observations)))
				break
			}
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", o.Type, o.Detail))
		}
	} else {
		sb.WriteString("- 无新观察\n")
	}
	sb.WriteString("\n## 历史调查记忆\n")
	if len(ctx.History) > 0 {
		for _, m := range ctx.History {
			sb.WriteString(fmt.Sprintf("- %s\n", m))
		}
	}
	sb.WriteString(`
## 输出要求（严格 JSON 数组，不要 Markdown 围栏）
[
  {"title": "假设标题",
   "description": "假设描述（中文，2-3 句）",
   "confidence": 0.0-1.0,
   "tasks": [
     {"type": "PATH_TRACE | FLOW_ANALYSIS | ENTITY_CHECK | RISK_SCAN",
      "priority": 0.0-1.0,
      "target": "0x地址（可选）",
      "reason": "验证理由"}
   ]}
]
最多 3 个假设。`)
	return sb.String()
}

// DeepAnalysisPrompt 构建深入分析提示词（AML + Forensic 角色，输出 AIFinding 列表）。
func (b *PromptBuilder) DeepAnalysisPrompt(ctx *AIContext, focus string) string {
	var sb strings.Builder
	sb.WriteString("请对以下调查结果做深入分析（重点：风险模式与行为解释）。\n\n")
	if focus != "" {
		sb.WriteString(fmt.Sprintf("## 重点分析地址\n%s\n\n", focus))
	}
	sb.WriteString(b.contextSection(ctx))
	sb.WriteString(`
## 输出要求（严格 JSON 数组，不要 Markdown 围栏）
[
  {"type": "rapid_transfer | layering | smurfing | concentration | exchange_flow | suspicious_source",
   "address": "0x地址",
   "detail": "发现描述（中文，2-3 句）",
   "confidence": 0.0-1.0,
   "evidence": ["tx_hash 或 block_number 引用，至少 1 条"]
}]
最多 5 个发现。证据必须引用真实交易哈希或区块号。`)
	return sb.String()
}

// SuggestionPrompt 构建下一步建议提示词（Investigator 角色，输出 AISuggestion）。
func (b *PromptBuilder) SuggestionPrompt(ctx *AIContext, decision Decision) string {
	var sb strings.Builder
	sb.WriteString("请根据当前调查状态给出下一步动作建议。\n\n")
	sb.WriteString(b.contextSection(ctx))
	sb.WriteString(fmt.Sprintf("\n## 规则决策引擎当前判定\n- action: %s\n", decision.Action))
	if len(decision.Reasons) > 0 {
		sb.WriteString(fmt.Sprintf("- reasons: %s\n", strings.Join(decision.Reasons, "；")))
	}
	sb.WriteString(`
## 输出要求（严格 JSON，不要 Markdown 围栏）
{
  "action": "EXPAND | STOP | DEEP_ANALYSIS | VERIFY",
  "target": "0x地址（可选）",
  "reasons": ["理由 1", "理由 2"],
  "confidence": 0.0-1.0
}`)
	return sb.String()
}

// contextSection 输出上下文公共部分。
func (b *PromptBuilder) contextSection(ctx *AIContext) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## 目标地址\n%s\n", ctx.Target))
	sb.WriteString("\n## 地址画像\n")
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
	if len(ctx.History) > 0 {
		sb.WriteString("\n## 调查历史（AI 记忆）\n")
		for _, h := range ctx.History {
			sb.WriteString(fmt.Sprintf("- %s\n", h))
		}
	}
	return sb.String()
}
