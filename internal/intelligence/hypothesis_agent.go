package intelligence

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/etl/backend/internal/logger"
)

// ── Hypothesis Agent（设计 §7）──
//
// 从风险模式与观察结果生成调查假设，并附带验证任务。
// 示例：A 收到 100 万 USDT → 5 分钟后拆 20 个地址 → 假设「资金分层行为」→
// 验证任务：检查共同控制（ENTITY_CHECK）/ 检查归集节点（PATH_TRACE）/ 检查交易所出口（ENTITY_CHECK）。
// 规则触发保证确定性；AI 可用时进一步提出假设（输出经结构化校验）。

// HypothesisAgent 生成调查假设。
type HypothesisAgent struct {
	ai      AIChatter
	prompts *PromptBuilder
	parser  *ResponseParser
	cfg     IntelligenceConfig
}

// NewHypothesisAgent 创建假设 Agent。ai 可为 nil（仅规则假设）。
func NewHypothesisAgent(ai AIChatter, cfg IntelligenceConfig) *HypothesisAgent {
	return &HypothesisAgent{
		ai:      ai,
		prompts: NewPromptBuilder(cfg),
		parser:  NewResponseParser(),
		cfg:     cfg,
	}
}

// Hypothesize 生成假设（规则触发 + AI 细化），按标题去重，最多 5 条。
// 历史记忆由调用方经 aiCtx.History 注入（§13）。
func (h *HypothesisAgent) Hypothesize(ctx context.Context, aiCtx *AIContext, patterns []RiskPattern, observations []Observation) []AIHypothesis {
	var out []AIHypothesis
	seen := map[string]bool{}
	add := func(hy AIHypothesis) {
		if seen[hy.Title] {
			return
		}
		seen[hy.Title] = true
		out = append(out, hy)
	}

	// ── 规则触发（确定性）──
	for _, p := range patterns {
		if len(out) >= 5 {
			break
		}
		switch p.Type {
		case PatternRapidTransfer:
			add(AIHypothesis{
				Title:       "资金快速转移（分层行为嫌疑）",
				Description: "地址在收到资金后短时间内转出大部分金额，可能存在资金分层（layering）行为。",
				Confidence:  0.7,
				Source:      "rule",
				Status:      "proposed",
				Tasks: []AITask{
					{Type: TaskPathTrace, Priority: 0.9, Target: p.Address, Reason: "追踪转移资金的去向路径"},
					{Type: TaskFlowAnalysis, Priority: 0.7, Target: p.Address, Reason: "分析进出时序与金额分布"},
				},
				CreatedAt: time.Now().UTC(),
			})
		case PatternMultiSplit:
			add(AIHypothesis{
				Title:       "多地址拆分（分散洗钱嫌疑）",
				Description: "大额资金进入后分散到多个地址，可能存在分散洗钱（smurfing）行为。",
				Confidence:  0.75,
				Source:      "rule",
				Status:      "proposed",
				Tasks: []AITask{
					{Type: TaskEntityCheck, Priority: 0.9, Target: p.Address, Reason: "检查拆分目标是否为共同控制地址"},
					{Type: TaskPathTrace, Priority: 0.8, Target: p.Address, Reason: "检查拆分目标是否有归集节点"},
				},
				CreatedAt: time.Now().UTC(),
			})
		case PatternConcentration:
			add(AIHypothesis{
				Title:       "资金归集（多来源汇聚嫌疑）",
				Description: "多个地址向同一地址转入且转出极少，可能存在归集行为。",
				Confidence:  0.7,
				Source:      "rule",
				Status:      "proposed",
				Tasks: []AITask{
					{Type: TaskPathTrace, Priority: 0.9, Target: p.Address, Reason: "反向追踪归集资金来源"},
					{Type: TaskEntityCheck, Priority: 0.7, Target: p.Address, Reason: "检查来源地址实体类型"},
				},
				CreatedAt: time.Now().UTC(),
			})
		case PatternLargeInflow:
			add(AIHypothesis{
				Title:       "大额进入来源存疑",
				Description: "地址收到显著高于常规的大额资金，来源渠道需要核实。",
				Confidence:  0.65,
				Source:      "rule",
				Status:      "proposed",
				Tasks: []AITask{
					{Type: TaskPathTrace, Priority: 0.9, Target: p.Address, Reason: "追踪大额资金来源路径"},
					{Type: TaskEntityCheck, Priority: 0.6, Target: p.Address, Reason: "识别资金来源实体"},
				},
				CreatedAt: time.Now().UTC(),
			})
		case PatternRapidDrain:
			add(AIHypothesis{
				Title:       "快速清空（过账账户嫌疑）",
				Description: "资金流入后几乎全部快速流出，可能是中转/过账账户。",
				Confidence:  0.7,
				Source:      "rule",
				Status:      "proposed",
				Tasks: []AITask{
					{Type: TaskFlowAnalysis, Priority: 0.9, Target: p.Address, Reason: "分析流出目标与金额"},
					{Type: TaskPathTrace, Priority: 0.8, Target: p.Address, Reason: "追踪资金最终去向"},
				},
				CreatedAt: time.Now().UTC(),
			})
		}
	}

	// ── AI 细化（可用、已配置且有上下文时）──
	if aiCtx != nil && h.ai != nil && h.ai.Configured() && len(out) < 5 {
		aiHyps, err := h.aiHypotheses(ctx, aiCtx, observations)
		if err != nil {
			logger.Log.Warn().Err(err).Msg("ai_hypothesis_failed_keep_rule")
		} else {
			for _, hy := range aiHyps {
				hy.Source = "ai"
				hy.Status = "proposed"
				if hy.CreatedAt.IsZero() {
					hy.CreatedAt = time.Now().UTC()
				}
				add(hy)
			}
		}
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// aiHypotheses 调用 AI 生成假设（结构化输出解析失败时返回错误）。
func (h *HypothesisAgent) aiHypotheses(ctx context.Context, aiCtx *AIContext, observations []Observation) ([]AIHypothesis, error) {
	system := h.prompts.SystemPrompt(RoleAMLAnalyst)
	user := h.prompts.HypothesisPrompt(aiCtx, observations)
	content, err := h.ai.Chat(ctx, system, user)
	if err != nil {
		return nil, err
	}
	hyps, err := h.parser.ParseHypotheses(content)
	if err != nil {
		return nil, err
	}
	if len(hyps) == 0 {
		return nil, fmt.Errorf("AI 未返回有效假设")
	}
	return hyps, nil
}

// hypothesisTaskTypes 是假设验证任务允许的类型。
func hypothesisTaskTypes() []string {
	return []string{TaskPathTrace, TaskFlowAnalysis, TaskEntityCheck, TaskRiskScan}
}

// verifyTasks 将假设验证任务转为队列任务（类型过滤 + 地址校验）。
func verifyTasks(hyp AIHypothesis, round int) []InvestigationTask {
	allowed := map[string]bool{}
	for _, t := range hypothesisTaskTypes() {
		allowed[t] = true
	}
	var tasks []InvestigationTask
	for i, t := range hyp.Tasks {
		if !allowed[t.Type] {
			continue
		}
		target := strings.ToLower(strings.TrimSpace(t.Target))
		if target != "" && !validEVMAddress(target) {
			continue
		}
		priority := 3
		switch {
		case i == 0:
			priority = 1
		case i < 3:
			priority = 2
		}
		tasks = append(tasks, InvestigationTask{
			Type:        t.Type,
			Description: fmt.Sprintf("假设验证[%s]：%s", truncateStr(hyp.Title, 20), t.Reason),
			Priority:    priority,
			Target:      target,
			Round:       round,
		})
	}
	return tasks
}

// truncateStr 截断字符串。
func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
