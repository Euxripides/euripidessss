package intelligence

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/etl/backend/internal/logger"
)

// ── AI Investigation Agent（设计 §3/§16）──
//
// 编排 AI 子代理：Planner Agent（调查规划）/ Hypothesis Agent（调查假设）/
// Analysis Agent（深入分析与证据验证），共享 AI Memory（§13）与调用限额（§17）。
//
// 注：本设计文档中的 internal/intelligence/aiagent/ 子目录以同包文件实现
// （ai_agent.go / planner_agent.go / analysis_agent.go / hypothesis_agent.go /
// prompt_builder.go / response_parser.go / ai_memory.go / evidence_guard.go），
// 避免子包与父包（调查闭环）之间产生 import 环。

// errAIDisabled 表示 AI 未配置。
var errAIDisabled = errors.New("AI 未配置")

// AIAgent 是 AI 驱动调查的编排入口。
type AIAgent struct {
	planner    *PlannerAgent
	analysis   *AnalysisAgent
	hypothesis *HypothesisAgent
	guard      *EvidenceGuard
	context    *AIContextBuilder
	prompts    *PromptBuilder
	parser     *ResponseParser
	memory     *AIMemoryStore
	ai         AIChatter
	cfg        IntelligenceConfig

	mu        sync.Mutex
	callCount map[string]int // investigation_id → AI 调用次数（§17 调用限额）
}

// NewAIAgent 创建 AI 调查 Agent。
// source 供 Evidence Guard 验证证据；memoryDir 为空则记忆仅内存。
func NewAIAgent(ai AIChatter, source FlowSource, cfg IntelligenceConfig, memoryDir string) *AIAgent {
	return NewAIAgentWithStore(ai, source, cfg, NewAIMemoryStore(memoryDir))
}

// NewAIAgentWithStore 使用外部共享记忆存储创建 AI 调查 Agent。
// 共享存储避免并发调查间 last-writer-wins 丢记忆（rebuild 时复用同一实例）；
// store 为 nil 时回退仅内存存储。
func NewAIAgentWithStore(ai AIChatter, source FlowSource, cfg IntelligenceConfig, memory *AIMemoryStore) *AIAgent {
	if memory == nil {
		memory = NewAIMemoryStore("")
	}
	agent := &AIAgent{
		ai:        ai,
		guard:     NewEvidenceGuard(source),
		context:   NewAIContextBuilder(cfg),
		prompts:   NewPromptBuilder(cfg),
		parser:    NewResponseParser(),
		memory:    memory,
		cfg:       cfg,
		callCount: map[string]int{},
	}
	agent.planner = NewPlannerAgent(ai, cfg)
	agent.analysis = NewAnalysisAgent(ai, agent.guard, cfg)
	agent.hypothesis = NewHypothesisAgent(ai, cfg)
	return agent
}

// Configured 返回 AI 是否可用。
func (a *AIAgent) Configured() bool {
	return a.ai != nil && a.ai.Configured()
}

// Memory 返回 AI 记忆存储。
func (a *AIAgent) Memory() *AIMemoryStore { return a.memory }

// allowCall 检查并占用一次 AI 调用额度（每个调查独立计数）。
func (a *AIAgent) allowCall(invID string) bool {
	maxCalls := a.cfg.MaxAICalls
	if maxCalls <= 0 {
		maxCalls = DefaultConfig().MaxAICalls // 与默认配置保持一致，防漂移
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.callCount[invID] >= maxCalls {
		return false
	}
	a.callCount[invID]++
	return true
}

// buildContext 构建 AI 上下文（含 AI 记忆历史，§13/§9）。
func (a *AIAgent) buildContext(inv *Investigation) *AIContext {
	ctx := a.context.Build(inv)
	ctx.History = a.memory.Summarize(inv.Target, 8)
	return ctx
}

// Plan 生成调查计划（AI 优先，规则回退）。strategy 为 nil 表示规则规划。
// 历史记忆经 buildContext 注入 ctx.History（不重复传参）。
func (a *AIAgent) Plan(ctx context.Context, inv *Investigation, input PlanInput) (*InvestigationPlan, *AIStrategy) {
	if a.ai == nil || !a.ai.Configured() || !a.allowCall(inv.ID) {
		return a.planner.Plan(ctx, input, nil)
	}
	aiCtx := a.buildContext(inv)
	return a.planner.Plan(ctx, input, aiCtx)
}

// Hypothesize 生成调查假设（规则触发 + AI 细化）。
func (a *AIAgent) Hypothesize(ctx context.Context, inv *Investigation, roundObs []Observation) []AIHypothesis {
	if a.ai == nil || !a.ai.Configured() || !a.allowCall(inv.ID) {
		return a.hypothesis.Hypothesize(ctx, nil, inv.Patterns, roundObs)
	}
	aiCtx := a.buildContext(inv)
	return a.hypothesis.Hypothesize(ctx, aiCtx, inv.Patterns, roundObs)
}

// DeepAnalyze 深入分析（多角色 + 证据验证）。返回已验证发现与分析结果。
func (a *AIAgent) DeepAnalyze(ctx context.Context, inv *Investigation, focus string) ([]VerifiedFinding, *AIAnalysis) {
	if a.ai == nil || !a.ai.Configured() || !a.allowCall(inv.ID) {
		return nil, nil
	}
	aiCtx := a.buildContext(inv)
	verified, analysis, err := a.analysis.DeepAnalyze(ctx, aiCtx, focus)
	if err != nil {
		logger.Log.Warn().Str("inv", inv.ID).Err(err).Msg("ai_deep_analyze_failed")
		return nil, nil
	}
	return verified, analysis
}

// Suggest 生成 AI 下一步建议（决策输入；规则 Decision Engine 仍为最终裁决）。
func (a *AIAgent) Suggest(ctx context.Context, inv *Investigation, decision Decision) *AISuggestion {
	if a.ai == nil || !a.ai.Configured() || !a.allowCall(inv.ID) {
		return nil
	}
	aiCtx := a.buildContext(inv)
	system := a.prompts.SystemPrompt(RoleInvestigator)
	user := a.prompts.SuggestionPrompt(aiCtx, decision)
	content, err := a.ai.Chat(ctx, system, user)
	if err != nil {
		logger.Log.Warn().Str("inv", inv.ID).Err(err).Msg("ai_suggest_failed")
		return nil
	}
	suggestion, err := a.parser.ParseSuggestion(content)
	if err != nil {
		logger.Log.Warn().Str("inv", inv.ID).Err(err).Msg("ai_suggest_parse_failed")
		return nil
	}
	return suggestion
}

// Remember 调查完成后固化 AI 记忆（§13）：历史调查 / 已验证发现（AI 结论）/ 高风险地址判断。
// 同时清理该调查的调用计数（防 map 无限增长）。
func (a *AIAgent) Remember(inv *Investigation) {
	a.mu.Lock()
	delete(a.callCount, inv.ID)
	a.mu.Unlock()
	a.memory.Record(MemInvestigation, inv.Target, fmt.Sprintf("调查 %s 完成：%d 轮，%d 条路径，%d 个风险模式",
		inv.ID, len(inv.Rounds), len(inv.Paths), len(inv.Patterns)), "ai", 0, nil)
	for _, vf := range inv.Findings {
		if vf.Status != EvidenceVerified {
			continue
		}
		a.memory.Record(MemAIConclusion, vf.Finding.Address, vf.Finding.Detail, "ai", vf.Finding.Confidence, vf.Finding.Evidence)
		a.memory.Record(MemRiskPattern, vf.Finding.Address, string(vf.Finding.Type), "ai", vf.Finding.Confidence, vf.Finding.Evidence)
	}
	for _, ent := range inv.Entities {
		if ent.Risk >= 60 {
			// 规则阈值判定（非 AI 输出），来源标记 rule
			a.memory.Record(MemAddressJudgment, ent.Address, fmt.Sprintf("高风险地址（风险 %.1f，实体 %s）", ent.Risk, ent.Entity), "rule", 0.8, nil)
		}
	}
	if a.ai != nil && a.ai.Configured() && inv.AI != nil && inv.AI.Summary != "" {
		summary := strings.ReplaceAll(inv.AI.Summary, "\n", " ")
		if len(summary) > 200 {
			summary = summary[:200] + "..."
		}
		a.memory.Record(MemAIConclusion, inv.Target, summary, "ai", 0, nil)
	}
}

// SaveMemory 持久化 AI 记忆。
func (a *AIAgent) SaveMemory() {
	if err := a.memory.Save(); err != nil {
		logger.Log.Warn().Err(err).Msg("ai_memory_save_failed")
	}
}

// AIAgentOptions 是 AIAgent 装配选项。
type AIAgentOptions struct {
	Chatter   AIChatter
	Source    FlowSource
	Config    IntelligenceConfig
	MemoryDir string // 空 = 仅内存
}

// NewAIAgentFromOptions 兼容入口。
func NewAIAgentFromOptions(opts AIAgentOptions) *AIAgent {
	return NewAIAgent(opts.Chatter, opts.Source, opts.Config, opts.MemoryDir)
}

// aiMemoryDir 由调查记忆目录推导 AI 记忆目录。
func aiMemoryDir(memoryDir string) string {
	if memoryDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(memoryDir), "ai_memory")
}
