package intelligence

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ── AI 驱动调查测试（V2.1 RC2 DeepSeek 驱动自主调查 Agent）──

// fakeAIChatter 是 AIChatter 测试实现：按 user 提示词子串路由响应。
type fakeAIChatter struct {
	configured bool
	responses  map[string]string // user 子串 → 输出
	fallback   string
	calls      int
}

func newFakeAIChatter() *fakeAIChatter {
	return &fakeAIChatter{configured: true, responses: map[string]string{}}
}

func (f *fakeAIChatter) Chat(_ context.Context, _ string, user string) (string, error) {
	f.calls++
	if !f.configured {
		return "", fmt.Errorf("not configured")
	}
	for key, resp := range f.responses {
		if strings.Contains(user, key) {
			return resp, nil
		}
	}
	return f.fallback, nil
}

func (f *fakeAIChatter) Configured() bool { return f.configured }

// ── Response Parser（§11/§17）──

func TestResponseParserStrategy(t *testing.T) {
	p := NewResponseParser()
	content := "好的，以下是策略：\n```json\n{\"strategy\":\"trace_outgoing\",\"rationale\":\"追踪去向\",\"confidence\":1.5," +
		"\"tasks\":[{\"type\":\"PATH_TRACE\",\"priority\":0.95,\"target\":\"0x00000000000000000000000000000000000000b1\",\"reason\":\"追踪\"}," +
		"{\"type\":\"BAD_TYPE\",\"priority\":0.9},{\"type\":\"ENTITY_CHECK\",\"priority\":0.5}]}\n```"
	s, err := p.ParseStrategy(content)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if s.Strategy != "trace_outgoing" {
		t.Fatalf("策略错误: %s", s.Strategy)
	}
	if s.Confidence != 1.0 {
		t.Fatalf("置信度应钳制到 1.0, got %v", s.Confidence)
	}
	if len(s.Tasks) != 2 {
		t.Fatalf("白名单外任务应丢弃, got %d", len(s.Tasks))
	}
	if s.Tasks[0].Type != TaskPathTrace {
		t.Fatalf("首个任务应为 PATH_TRACE, got %s", s.Tasks[0].Type)
	}
}

func TestResponseParserInvalid(t *testing.T) {
	p := NewResponseParser()
	if _, err := p.ParseStrategy("不是 JSON"); err == nil {
		t.Fatal("非法策略应报错")
	}
	if _, err := p.ParseStrategy(`{"rationale":"缺 strategy 字段"}`); err == nil {
		t.Fatal("缺 strategy 字段应报错")
	}
	if _, err := p.ParseStrategy(`{"strategy":"x","tasks":[]}`); err == nil {
		t.Fatal("无有效任务应报错")
	}
	if _, err := p.ParseSuggestion(`{"action":"FLY_AWAY"}`); err == nil {
		t.Fatal("非法建议动作应报错")
	}
}

func TestResponseParserFindingsAndHypotheses(t *testing.T) {
	p := NewResponseParser()
	findings, err := p.ParseFindings(`[{"type":"rapid_transfer","address":"0x00000000000000000000000000000000000000B1","detail":"快速转移","confidence":0.91,"evidence":["0xabc"]}]`)
	if err != nil {
		t.Fatalf("发现解析失败: %v", err)
	}
	if len(findings) != 1 || findings[0].Address != strings.ToLower(findings[0].Address) {
		t.Fatalf("发现应归一化地址: %+v", findings)
	}
	hyps, err := p.ParseHypotheses(`[{"title":"资金分层","description":"d","confidence":0.8,"tasks":[{"type":"ENTITY_CHECK","priority":0.9,"reason":"r"}]}]`)
	if err != nil {
		t.Fatalf("假设解析失败: %v", err)
	}
	if len(hyps) != 1 || hyps[0].Tasks[0].Type != TaskEntityCheck {
		t.Fatalf("假设任务解析错误: %+v", hyps)
	}
}

// ── Prompt Builder（§10）──

func TestPromptBuilderRoles(t *testing.T) {
	b := NewPromptBuilder(DefaultConfig())
	cases := []struct {
		role AIRole
		want string
	}{
		{RoleInvestigator, "Investigator"},
		{RoleAMLAnalyst, "AML"},
		{RoleForensicAnalyst, "Forensic"},
		{RoleReportWriter, "报告"},
	}
	for _, c := range cases {
		sp := b.SystemPrompt(c.role)
		if !strings.Contains(sp, c.want) {
			t.Fatalf("角色 %s 系统提示词应包含 %s, got: %s", c.role, c.want, sp)
		}
	}
	if PromptVersion == "" {
		t.Fatal("应定义提示词版本")
	}
	ctx := &AIContext{Target: addrA, Profile: map[string]any{}, TopPaths: []string{"A→B"}, RiskEvents: []string{"[high] RAPID_TRANSFER"}}
	planPrompt := b.PlanPrompt(ctx)
	if !strings.Contains(planPrompt, `"strategy"`) || !strings.Contains(planPrompt, "PATH_TRACE") {
		t.Fatal("规划提示词应包含 JSON 输出结构")
	}
}

// ── Evidence Guard（§12）──

func TestEvidenceGuard(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(addrB, []FundEdge{{From: addrA, To: addrB, Token: "USDT", Amount: "100", TxHash: "0xhit1"}})
	g := NewEvidenceGuard(src)

	// VERIFIED：证据命中
	vf := g.Validate(context.Background(), AIFinding{Type: "x", Address: addrB, Evidence: []string{"0xHIT1"}})
	if vf.Status != EvidenceVerified {
		t.Fatalf("命中证据应 VERIFIED, got %s (%s)", vf.Status, vf.Reason)
	}
	// REJECTED：证据不存在
	vf = g.Validate(context.Background(), AIFinding{Type: "x", Address: addrB, Evidence: []string{"0xmissing"}})
	if vf.Status != EvidenceRejected {
		t.Fatalf("缺失证据应 REJECTED, got %s", vf.Status)
	}
	// UNVERIFIED：无证据 / 无地址
	vf = g.Validate(context.Background(), AIFinding{Type: "x", Address: addrB})
	if vf.Status != EvidenceUnverified {
		t.Fatalf("无证据应 UNVERIFIED, got %s", vf.Status)
	}
	vf = g.Validate(context.Background(), AIFinding{Type: "x", Evidence: []string{"0xhit1"}})
	if vf.Status != EvidenceUnverified {
		t.Fatalf("无地址应 UNVERIFIED, got %s", vf.Status)
	}
	// 无数据源
	g2 := NewEvidenceGuard(nil)
	vf = g2.Validate(context.Background(), AIFinding{Type: "x", Address: addrB, Evidence: []string{"0xhit1"}})
	if vf.Status != EvidenceUnverified {
		t.Fatalf("无数据源应 UNVERIFIED, got %s", vf.Status)
	}
}

// ── AI Memory（§13）──

func TestAIMemoryStore(t *testing.T) {
	store := NewAIMemoryStore("")
	store.Record(MemAIConclusion, addrA, "结论1", "ai", 0.9, []string{"0x1"})
	store.Record(MemAIConclusion, addrA, "结论1", "ai", 0.9, []string{"0x1"}) // 幂等
	store.Record(MemAddressJudgment, addrB, "高风险", "ai", 0.8, nil)
	store.Record(MemRiskPattern, addrB, "rapid_transfer", "ai", 0.7, nil)
	if got := store.List(addrA, "", 0); len(got) != 1 {
		t.Fatalf("addrA 记忆应 1 条, got %d", len(got))
	}
	if got := store.List("", MemRiskPattern, 0); len(got) != 1 {
		t.Fatalf("风险模式记忆应 1 条, got %d", len(got))
	}
	sum := store.Summarize(addrB, 3)
	if len(sum) == 0 {
		t.Fatal("应有记忆摘要")
	}
}

func TestAIMemoryPersistence(t *testing.T) {
	dir := t.TempDir()
	store := NewAIMemoryStore(dir)
	store.Record(MemAIConclusion, addrA, "持久化结论", "ai", 0.9, nil)
	if err := store.Save(); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	store2 := NewAIMemoryStore(dir)
	if got := store2.List(addrA, "", 0); len(got) != 1 || got[0].Content != "持久化结论" {
		t.Fatalf("重载后记忆异常: %+v", got)
	}
}

// ── Planner Agent（§5）──

func TestPlannerAgentAI(t *testing.T) {
	ai := newFakeAIChatter()
	ai.responses["制定调查策略"] = `{"strategy":"entity_focus","rationale":"聚焦实体","confidence":0.8,"tasks":[{"type":"ENTITY_CHECK","priority":0.9,"reason":"识别实体"},{"type":"PATH_TRACE","priority":0.7,"target":"0x00000000000000000000000000000000000000b1","reason":"追踪"}]}`
	p := NewPlannerAgent(ai, DefaultConfig())
	aiCtx := &AIContext{Target: addrA, Profile: map[string]any{}, TopPaths: []string{"A→B"}}
	plan, strategy := p.Plan(context.Background(), PlanInput{Target: addrA}, aiCtx)
	if strategy == nil {
		t.Fatal("AI 应生成策略")
	}
	if len(plan.Tasks) != 2 || plan.Tasks[0].Type != TaskEntityCheck {
		t.Fatalf("AI 任务映射错误: %+v", plan.Tasks)
	}
	if plan.Tasks[0].Priority != 1 {
		t.Fatalf("首任务优先级应为 1, got %d", plan.Tasks[0].Priority)
	}
}

func TestPlannerAgentRuleFallback(t *testing.T) {
	// 未配置 → 规则回退
	p := NewPlannerAgent(nil, DefaultConfig())
	plan, strategy := p.Plan(context.Background(), PlanInput{Target: addrA, HasFlows: true}, &AIContext{Target: addrA})
	if strategy != nil {
		t.Fatal("未配置 AI 时 strategy 应为 nil")
	}
	if plan == nil || len(plan.Tasks) == 0 {
		t.Fatal("规则回退应生成计划")
	}
	// 输出非法 → 规则回退
	ai := newFakeAIChatter()
	ai.fallback = "这不是 JSON"
	p2 := NewPlannerAgent(ai, DefaultConfig())
	plan2, strategy2 := p2.Plan(context.Background(), PlanInput{Target: addrA, HasFlows: true}, &AIContext{Target: addrA})
	if strategy2 != nil || plan2 == nil {
		t.Fatal("非法 AI 输出应规则回退")
	}
}

// ── Hypothesis Agent（§7）──

func TestHypothesisAgentRuleTriggers(t *testing.T) {
	h := NewHypothesisAgent(nil, DefaultConfig())
	patterns := []RiskPattern{
		{Type: PatternRapidTransfer, Address: addrA, Severity: "high"},
		{Type: PatternMultiSplit, Address: addrB, Severity: "high"},
	}
	hyps := h.Hypothesize(context.Background(), &AIContext{Target: addrA}, patterns, nil)
	if len(hyps) != 2 {
		t.Fatalf("应生成 2 条规则假设, got %d", len(hyps))
	}
	if hyps[0].Status != "proposed" || len(hyps[0].Tasks) == 0 {
		t.Fatalf("假设应带验证任务: %+v", hyps[0])
	}
	tasks := verifyTasks(hyps[0], 2)
	if len(tasks) == 0 || tasks[0].Round != 2 {
		t.Fatalf("验证任务应进入下一轮: %+v", tasks)
	}
}

func TestHypothesisAgentAIElaboration(t *testing.T) {
	ai := newFakeAIChatter()
	ai.responses["资金调查假设"] = `[{"title":"AI 假设：共同控制","description":"拆分地址疑似共同控制","confidence":0.85,"tasks":[{"type":"ENTITY_CHECK","priority":0.9,"reason":"检查控制关系"}]}]`
	h := NewHypothesisAgent(ai, DefaultConfig())
	hyps := h.Hypothesize(context.Background(), &AIContext{Target: addrA}, nil, []Observation{{ID: "o1", Type: ObsNewAddress, Detail: addrB}})
	if len(hyps) != 1 || hyps[0].Source != "ai" {
		t.Fatalf("AI 假设应生成: %+v", hyps)
	}
}

// ── Analysis Agent（§8/§12）──

func TestAnalysisAgentDeepAnalyze(t *testing.T) {
	ai := newFakeAIChatter()
	ai.responses["深入分析"] = `[{"type":"rapid_transfer","address":"0x00000000000000000000000000000000000000b1","detail":"快速转移","confidence":0.91,"evidence":["0xhit1"]}]`
	src := NewFakeFlowSource()
	src.SetFlows(addrB, []FundEdge{{From: addrA, To: addrB, TxHash: "0xhit1"}})
	a := NewAnalysisAgent(ai, NewEvidenceGuard(src), DefaultConfig())
	verified, analysis, err := a.DeepAnalyze(context.Background(), &AIContext{Target: addrA}, addrA)
	if err != nil {
		t.Fatalf("DeepAnalyze 失败: %v", err)
	}
	if len(verified) != 1 || verified[0].Status != EvidenceVerified {
		t.Fatalf("证据命中应 VERIFIED: %+v", verified)
	}
	if analysis == nil || len(analysis.Insights) != 1 {
		t.Fatalf("分析应含已验证洞察: %+v", analysis)
	}
}

// ── AI Agent 编排（§3/§17 调用限额）──

func TestAIAgentCallLimit(t *testing.T) {
	ai := newFakeAIChatter()
	ai.responses["制定调查策略"] = `{"strategy":"x","rationale":"r","confidence":0.5,"tasks":[{"type":"PATH_TRACE","priority":0.9,"reason":"r"}]}`
	cfg := DefaultConfig()
	cfg.UseAI = true
	cfg.MaxAICalls = 1
	agent := NewAIAgent(ai, nil, cfg, "")
	inv := &Investigation{ID: "inv-x", Target: addrA}
	plan, strategy := agent.Plan(context.Background(), inv, PlanInput{Target: addrA})
	if strategy == nil {
		t.Fatal("首次调用应允许（AI 规划）")
	}
	_ = plan
	// 后续调用超限 → 降级
	hyps := agent.Hypothesize(context.Background(), inv, nil)
	if len(hyps) != 0 {
		t.Fatalf("超限后假设应降级为空, got %d", len(hyps))
	}
	if v, a := agent.DeepAnalyze(context.Background(), inv, addrA); v != nil || a != nil {
		t.Fatal("超限后深入分析应降级为空")
	}
}

// TestAIAgentCallLimitZeroFallback 验证 MaxAICalls≤0 时回退默认配额（10）：
// 第 10 次调用允许，第 11 次拒绝。
func TestAIAgentCallLimitZeroFallback(t *testing.T) {
	ai := newFakeAIChatter()
	ai.responses["制定调查策略"] = `{"strategy":"x","rationale":"r","confidence":0.5,"tasks":[{"type":"PATH_TRACE","priority":0.9,"reason":"r"}]}`
	cfg := DefaultConfig()
	cfg.MaxAICalls = 0 // 触发默认回退
	agent := NewAIAgent(ai, nil, cfg, "")
	inv := &Investigation{ID: "inv-zero", Target: addrA}
	allowed := 0
	for i := 0; i < 12; i++ {
		if agent.allowCall(inv.ID) {
			allowed++
		}
	}
	if allowed != DefaultConfig().MaxAICalls {
		t.Fatalf("零配置应回退默认配额 %d, got %d", DefaultConfig().MaxAICalls, allowed)
	}
}

func TestAIAgentRemember(t *testing.T) {
	ai := newFakeAIChatter()
	agent := NewAIAgent(ai, nil, DefaultConfig(), "")
	inv := &Investigation{
		ID: "inv-1", Target: addrA,
		Findings: []VerifiedFinding{{
			Finding: AIFinding{Type: "rapid_transfer", Address: addrA, Detail: "快速转移", Confidence: 0.9, Evidence: []string{"0x1"}},
			Status:  EvidenceVerified,
		}},
		Entities: []EntityInfo{{Address: addrB, Entity: "wallet", Risk: 72}},
	}
	agent.Remember(inv)
	if got := agent.memory.List(addrA, MemAIConclusion, 0); len(got) == 0 {
		t.Fatal("应记忆已验证发现")
	}
	if got := agent.memory.List(addrB, MemAddressJudgment, 0); len(got) != 1 {
		t.Fatal("高风险地址应记忆判断")
	}
}

// ── 闭环集成（AI 规划 + 假设验证任务 + 证据验证发现）──

func TestLoopAIAgentIntegration(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(addrA, []FundEdge{edge(addrA, addrB, "USDT", "1000000", 1000)})
	src.SetFlows(addrB, []FundEdge{edge(addrB, addrC, "USDT", "950000", 2000)})

	ai := newFakeAIChatter()
	ai.responses["制定调查策略"] = `{"strategy":"trace_outgoing","rationale":"追踪去向","confidence":0.9,"tasks":[{"type":"PATH_TRACE","priority":0.95,"target":"0x00000000000000000000000000000000000000a1","reason":"追踪去向"}]}`
	ai.responses["资金调查假设"] = `[{"title":"资金分层假设","description":"快速转移分层嫌疑","confidence":0.8,"tasks":[{"type":"ENTITY_CHECK","priority":0.9,"target":"0x00000000000000000000000000000000000000b1","reason":"检查实体"}]}]`
	ai.responses["深入分析"] = `[{"type":"rapid_transfer","address":"0x00000000000000000000000000000000000000b1","detail":"收到后快速转出","confidence":0.91,"evidence":["0x000000000000abc"]}]`
	ai.responses["下一步动作建议"] = `{"action":"DEEP_ANALYSIS","reasons":["建议深入"],"confidence":0.8}`

	cfg := DefaultConfig()
	cfg.UseAI = true
	cfg.MaxAICalls = 8
	cfg.MaxRounds = 2

	ranker := DefaultPathRanker()
	agent := &InvestigationAgent{
		flowSource:      src,
		ranker:          ranker,
		tracer:          NewFundTracer(src, ranker, cfg),
		planner:         NewPlanner(cfg),
		detector:        NewPatternDetector(cfg),
		report:          NewReportAgent(cfg),
		contextBuilder:  NewAIContextBuilder(cfg),
		deepseek:        NewDeepSeekClient("", cfg.AIModel, cfg.AITimeoutMS, cfg.MaxTokens),
		entityResolver:  NewEntityResolver(nil, nil),
		cfg:             cfg,
		active:          make(map[string]*Investigation),
		history:         make(map[string]*Investigation),
		memories:        NewMemoryStore(""),
		aiChatter:       ai,
		ai:              NewAIAgent(ai, src, cfg, ""),
	}
	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	done := waitStatus(t, agent, inv.ID, InvestigationCompleted, 8*time.Second)

	// AI 规划（§5）
	if done.Strategy == nil || done.Strategy.Strategy != "trace_outgoing" {
		t.Fatalf("AI 应生成调查策略: %+v", done.Strategy)
	}
	// AI 假设 + 验证任务（§7）
	if len(done.Hypotheses) == 0 {
		t.Fatal("应生成调查假设")
	}
	foundVerify := false
	for _, tk := range done.Tasks {
		if strings.Contains(tk.Description, "假设验证") && tk.Status == TaskDone {
			foundVerify = true
			break
		}
	}
	if !foundVerify {
		t.Fatalf("假设验证任务应执行: %+v", done.Tasks)
	}
	if done.Hypotheses[0].Status != "evaluated" {
		t.Fatalf("假设应已评估, got %s", done.Hypotheses[0].Status)
	}
	// 证据验证发现（§8/§12）
	if len(done.Findings) == 0 {
		t.Fatal("AI 深入分析应产生发现")
	}
	if done.Findings[0].Status != EvidenceVerified {
		t.Fatalf("证据命中应 VERIFIED, got %s (%s)", done.Findings[0].Status, done.Findings[0].Reason)
	}
	// AI 建议（§6）
	if done.AISuggestion == nil || done.AISuggestion.Action != "DEEP_ANALYSIS" {
		t.Fatalf("应记录 AI 建议: %+v", done.AISuggestion)
	}
	// AI 记忆固化（§13）
	if done.AI == nil {
		t.Fatal("收尾应执行 AI 分析")
	}
	mem := agent.ai.Memory()
	if len(mem.List(addrA, MemInvestigation, 0)) == 0 {
		t.Fatal("应固化历史调查记忆")
	}
	if len(mem.List(addrB, MemAIConclusion, 0)) == 0 {
		t.Fatal("应固化已验证 AI 结论记忆")
	}
}

// TestLoopHypothesisEarlyExit 验证调查提前结束时假设状态如实标记
// （验证任务未进入队列 → note 标记「未执行」，而非虚假的「已执行完毕」）。
func TestLoopHypothesisEarlyExit(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(addrA, []FundEdge{edge(addrA, addrB, "USDT", "1000000", 1000)})

	ai := newFakeAIChatter()
	ai.responses["制定调查策略"] = `{"strategy":"trace_outgoing","rationale":"r","confidence":0.9,"tasks":[{"type":"PATH_TRACE","priority":0.95,"target":"0x00000000000000000000000000000000000000a1","reason":"r"}]}`
	ai.responses["资金调查假设"] = `[{"title":"资金分层假设","description":"d","confidence":0.8,"tasks":[{"type":"ENTITY_CHECK","priority":0.9,"target":"0x00000000000000000000000000000000000000b1","reason":"r"}]}]`

	cfg := DefaultConfig()
	cfg.UseAI = true
	cfg.MaxRounds = 1 // 无下一轮 → 验证任务不会执行
	cfg.MaxAICalls = 10

	ranker := DefaultPathRanker()
	agent := &InvestigationAgent{
		flowSource:      src,
		ranker:          ranker,
		tracer:          NewFundTracer(src, ranker, cfg),
		planner:         NewPlanner(cfg),
		detector:        NewPatternDetector(cfg),
		report:          NewReportAgent(cfg),
		contextBuilder:  NewAIContextBuilder(cfg),
		deepseek:        NewDeepSeekClient("", cfg.AIModel, cfg.AITimeoutMS, cfg.MaxTokens),
		entityResolver:  NewEntityResolver(nil, nil),
		cfg:             cfg,
		active:          make(map[string]*Investigation),
		history:         make(map[string]*Investigation),
		memories:        NewMemoryStore(""),
		aiChatter:       ai,
		ai:              NewAIAgent(ai, src, cfg, ""),
	}
	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	done := waitStatus(t, agent, inv.ID, InvestigationCompleted, 8*time.Second)
	if len(done.Hypotheses) != 1 {
		t.Fatalf("应生成 1 条假设, got %d", len(done.Hypotheses))
	}
	h := done.Hypotheses[0]
	if h.Status != "evaluated" || !strings.Contains(h.Note, "未执行") {
		t.Fatalf("提前结束假设应标记未执行: status=%s note=%s", h.Status, h.Note)
	}
}
