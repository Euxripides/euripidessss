package intelligence

import (
	"context"
	"strings"
	"time"

	"github.com/etl/backend/internal/logger"
)

// ── Analysis Agent（设计 §8 DEEP_ANALYSIS）──
//
// 触发条件 → AI 分析（AML/Forensic 多角色）→ 生成发现 → Evidence Guard 验证 →
// 仅 VERIFIED 发现进入报告与记忆；REJECTED/UNVERIFIED 保留状态供审计。
// 所有结论必须经过数据验证（§12）。

// AnalysisAgent 是深入分析 Agent。
type AnalysisAgent struct {
	ai      AIChatter
	prompts *PromptBuilder
	parser  *ResponseParser
	guard   *EvidenceGuard
	cfg     IntelligenceConfig
}

// NewAnalysisAgent 创建分析 Agent。ai 可为 nil（直接返回空结果）。
func NewAnalysisAgent(ai AIChatter, guard *EvidenceGuard, cfg IntelligenceConfig) *AnalysisAgent {
	return &AnalysisAgent{
		ai:      ai,
		prompts: NewPromptBuilder(cfg),
		parser:  NewResponseParser(),
		guard:   guard,
		cfg:     cfg,
	}
}

// DeepAnalyze 深入分析：调用 AI 生成结构化发现 → 证据验证 → 汇总分析结果。
// AI 未配置/失败时返回 (nil, nil, err)。
func (a *AnalysisAgent) DeepAnalyze(ctx context.Context, aiCtx *AIContext, focus string) ([]VerifiedFinding, *AIAnalysis, error) {
	if a.ai == nil || !a.ai.Configured() {
		return nil, nil, errAIDisabled
	}
	start := time.Now()
	system := a.prompts.SystemPrompt(RoleAMLAnalyst)
	user := a.prompts.DeepAnalysisPrompt(aiCtx, focus)
	content, err := a.ai.Chat(ctx, system, user)
	if err != nil {
		return nil, nil, err
	}
	findings, err := a.parser.ParseFindings(content)
	if err != nil {
		// 输出未通过结构化校验：不信任为发现，仅保留原文摘要
		logger.Log.Warn().Err(err).Msg("ai_deep_analysis_parse_failed")
		analysis := &AIAnalysis{
			Summary:    content,
			Model:      "",
			DurationMs: time.Since(start).Milliseconds(),
		}
		return nil, analysis, nil
	}
	var verified []VerifiedFinding
	if a.guard != nil {
		verified = a.guard.ValidateBatch(ctx, findings)
	} else {
		verified = make([]VerifiedFinding, 0, len(findings))
		for _, f := range findings {
			verified = append(verified, VerifiedFinding{Finding: f, Status: EvidenceUnverified, Reason: "无证据守卫", VerifiedAt: time.Now().UTC()})
		}
	}
	analysis := a.summarize(verified, content, time.Since(start).Milliseconds())
	return verified, analysis, nil
}

// summarize 将已验证发现汇总为 AIAnalysis。
func (a *AnalysisAgent) summarize(verified []VerifiedFinding, raw string, durationMs int64) *AIAnalysis {
	analysis := &AIAnalysis{
		Summary:    raw,
		DurationMs: durationMs,
	}
	var insights, risks []string
	var ev []string
	for _, vf := range verified {
		if vf.Status == EvidenceVerified {
			insights = append(insights, vf.Finding.Detail)
			ev = append(ev, strings.Join(vf.Finding.Evidence, ", "))
			risks = append(risks, string(vf.Finding.Type))
		}
	}
	analysis.Insights = insights
	analysis.RiskComment = strings.Join(risks, " / ")
	_ = ev
	return analysis
}
