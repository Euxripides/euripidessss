package intelligence

import (
	"context"
	"fmt"
	"time"

	"github.com/etl/backend/internal/logger"
)

// ── Planner Agent（设计 §5）──
//
// 职责：根据当前调查状态生成调查策略。
// 输入：目标地址 / 地址画像 / 资金路径 / 风险事件 / 实体信息 / 历史调查记录。
// 输出：结构化策略（strategy + tasks，含优先级与理由）。
// AI 不可用或输出非法时回退规则规划器（Planner），保证调查不中断。

// PlannerAgent 是 AI 驱动的调查规划器。
type PlannerAgent struct {
	ai       AIChatter
	prompts  *PromptBuilder
	parser   *ResponseParser
	fallback *Planner
	cfg      IntelligenceConfig
}

// NewPlannerAgent 创建 AI 规划器。ai 可为 nil（仅规则规划）。
func NewPlannerAgent(ai AIChatter, cfg IntelligenceConfig) *PlannerAgent {
	return &PlannerAgent{
		ai:       ai,
		prompts:  NewPromptBuilder(cfg),
		parser:   NewResponseParser(),
		fallback: NewPlanner(cfg),
		cfg:      cfg,
	}
}

// Plan 生成调查计划。返回 (计划, AI策略)；AI 未配置/失败/无上下文时 strategy 为 nil（规则回退）。
// 历史记忆由调用方经 aiCtx.History 注入（§13）。
func (p *PlannerAgent) Plan(ctx context.Context, input PlanInput, aiCtx *AIContext) (*InvestigationPlan, *AIStrategy) {
	// AI 不可用或未提供上下文 → 规则回退
	if p.ai == nil || !p.ai.Configured() || aiCtx == nil {
		return p.fallback.Plan(input), nil
	}
	system := p.prompts.SystemPrompt(RoleInvestigator)
	user := p.prompts.PlanPrompt(aiCtx)
	content, err := p.ai.Chat(ctx, system, user)
	if err != nil {
		logger.Log.Warn().Str("target", input.Target).Err(err).Msg("ai_plan_failed_fallback_rule")
		return p.fallback.Plan(input), nil
	}
	strategy, err := p.parser.ParseStrategy(content)
	if err != nil {
		logger.Log.Warn().Str("target", input.Target).Err(err).Msg("ai_plan_parse_failed_fallback_rule")
		return p.fallback.Plan(input), nil
	}
	return p.planFromStrategy(input, strategy), strategy
}

// planFromStrategy 将 AI 策略转换为 InvestigationPlan。
func (p *PlannerAgent) planFromStrategy(input PlanInput, s *AIStrategy) *InvestigationPlan {
	plan := &InvestigationPlan{
		Target:      input.Target,
		MaxHops:     p.cfg.MaxHops,
		BeamWidth:   p.cfg.BeamWidth,
		TopPaths:    p.cfg.TopPaths,
		MinAmount:   p.cfg.MinAmount,
		GeneratedAt: time.Now().UTC(),
	}
	plan.Objectives = append(plan.Objectives, fmt.Sprintf("AI 策略：%s（置信度 %.2f）", s.Strategy, s.Confidence))
	if s.Rationale != "" {
		plan.Objectives = append(plan.Objectives, "策略理由："+s.Rationale)
	}
	for i, t := range s.Tasks {
		priority := 3
		switch {
		case i == 0:
			priority = 1
		case i < 3:
			priority = 2
		}
		desc := t.Reason
		if desc == "" {
			desc = "AI 生成任务"
		}
		if t.Target != "" {
			desc = fmt.Sprintf("%s（%s）", desc, shortAddr(t.Target))
		}
		plan.Tasks = append(plan.Tasks, PlannedTask{
			ID:          fmt.Sprintf("ai%d", i+1),
			Type:        t.Type,
			Description: desc,
			Priority:    priority,
		})
	}
	// MEDIUM-1：防御性硬截断（即使 strategy 未经 parser 截断）
	if len(plan.Tasks) > maxAITasks {
		plan.Tasks = plan.Tasks[:maxAITasks]
	}
	if len(plan.Tasks) == 0 {
		return p.fallback.Plan(input)
	}
	return plan
}
