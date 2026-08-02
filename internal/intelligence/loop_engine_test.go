package intelligence

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 闭环测试用地址（A-E 在 fund_tracer_test.go 定义）
const (
	addrF = "0x00000000000000000000000000000000000000f1"
	addrG = "0x00000000000000000000000000000000000000f2"
	addrH = "0x00000000000000000000000000000000000000f3"
	addrK = "0x00000000000000000000000000000000000000f4"
)

// ── Task Queue 测试（设计 §7）──

// hasReason 判断决策原因列表是否包含子串。
func hasReason(reasons []string, sub string) bool {
	for _, r := range reasons {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

func TestTaskQueueOrderAndStatus(t *testing.T) {
	q := NewTaskQueue()
	q.Enqueue(InvestigationTask{Type: TaskRiskScan, Description: "风险扫描", Priority: 2, Round: 1})
	q.Enqueue(InvestigationTask{Type: TaskFlowAnalysis, Description: "资金流", Priority: 1, Target: addrA, Round: 1})
	q.Enqueue(InvestigationTask{Type: TaskAddressProfile, Description: "画像", Priority: 0, Target: addrA, Round: 1})

	// 优先级顺序：画像(0) → 资金流(1) → 风险(2)
	first := q.Next()
	if first.Type != TaskAddressProfile {
		t.Fatalf("首个任务应为 ADDRESS_PROFILE, got %s", first.Type)
	}
	q.Mark(first.ID, TaskDone, "完成", "")
	second := q.Next()
	if second.Type != TaskFlowAnalysis {
		t.Fatalf("第二个任务应为 FLOW_ANALYSIS, got %s", second.Type)
	}
	q.Mark(second.ID, TaskRunning, "", "")
	// running 任务不再被取出
	if got := q.Next(); got.Type != TaskRiskScan {
		t.Fatalf("running 任务应跳过, got %s", got.Type)
	}
	q.Mark(second.ID, TaskDone, "完成", "")
	q.Mark(q.Next().ID, TaskFailed, "", "错误")
	if q.PendingCount() != 0 {
		t.Fatalf("全部任务应结束, pending=%d", q.PendingCount())
	}
	snap := q.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("快照应含 3 个任务, got %d", len(snap))
	}
}

func TestTaskQueueDedupe(t *testing.T) {
	q := NewTaskQueue()
	t1 := q.Enqueue(InvestigationTask{Type: TaskPathTrace, Priority: 1, Target: addrA, Round: 1})
	t2 := q.Enqueue(InvestigationTask{Type: TaskPathTrace, Priority: 1, Target: addrA, Round: 1})
	if t1.ID != t2.ID {
		t.Fatalf("同轮次同类型同目标应去重: %s vs %s", t1.ID, t2.ID)
	}
	// 不同轮次允许重复
	t3 := q.Enqueue(InvestigationTask{Type: TaskPathTrace, Priority: 1, Target: addrA, Round: 2})
	if t3.ID == t1.ID {
		t.Fatal("不同轮次任务不应去重")
	}
	if len(q.Snapshot()) != 2 {
		t.Fatalf("应保留 2 个任务, got %d", len(q.Snapshot()))
	}
}

// ── Runtime V2 Task Queue 测试（设计 §5：依赖/重试/超时）──

func TestTaskQueueDependencyGating(t *testing.T) {
	q := NewTaskQueue()
	dep := q.Enqueue(InvestigationTask{Type: TaskAddressProfile, Priority: 0, Round: 1})
	waiter := q.Enqueue(InvestigationTask{Type: TaskForwardTrace, Priority: 1, Dependencies: []string{dep.ID}, Round: 1})

	// 依赖未完成：等待任务不被取出
	if got := q.Next(); got == nil || got.ID != dep.ID {
		t.Fatalf("依赖未完成时应先执行依赖任务, got %+v", got)
	}
	q.Mark(dep.ID, TaskDone, "画像完成", "")
	// 依赖完成：等待任务可执行
	if got := q.Next(); got == nil || got.ID != waiter.ID {
		t.Fatalf("依赖完成后应执行等待任务, got %+v", got)
	}
}

func TestTaskQueueDependencyFailedBlocks(t *testing.T) {
	q := NewTaskQueue()
	dep := q.Enqueue(InvestigationTask{Type: TaskAddressProfile, Priority: 0, Round: 1})
	waiter := q.Enqueue(InvestigationTask{Type: TaskForwardTrace, Priority: 1, Dependencies: []string{dep.ID}, Round: 1})
	q.Mark(dep.ID, TaskFailed, "", "画像失败")
	// 依赖失败（非 done）：等待任务永久阻塞
	if got := q.Next(); got != nil {
		t.Fatalf("依赖失败时等待任务不应执行, got %+v", got)
	}
	if !q.BlockedByFailedDep(waiter.ID) {
		t.Fatal("被失败依赖阻塞的任务应被 BlockedByFailedDep 识别")
	}
	// 无依赖 / 非 pending 任务不被误判
	independent := q.Enqueue(InvestigationTask{Type: TaskRiskScan, Priority: 1, Round: 1})
	if q.BlockedByFailedDep(independent.ID) {
		t.Fatal("无依赖任务不应被误判阻塞")
	}
}

func TestTaskQueueRetryOnFailure(t *testing.T) {
	q := NewTaskQueue()
	t1 := q.Enqueue(InvestigationTask{Type: TaskPathTrace, MaxRetries: 2, Priority: 1, Round: 1})
	q.Mark(t1.ID, TaskRunning, "", "")
	q.Mark(t1.ID, TaskFailed, "", "第一次失败")
	// 重试 1：回到 pending 且计数 +1
	got, _ := q.Get(t1.ID)
	if got.Status != TaskPending || got.RetryCount != 1 {
		t.Fatalf("第一次失败应重试: status=%s retry=%d", got.Status, got.RetryCount)
	}
	q.Mark(t1.ID, TaskRunning, "", "")
	q.Mark(t1.ID, TaskFailed, "", "第二次失败")
	got, _ = q.Get(t1.ID)
	if got.Status != TaskPending || got.RetryCount != 2 {
		t.Fatalf("第二次失败应重试: status=%s retry=%d", got.Status, got.RetryCount)
	}
	// 达到上限：保持 failed
	q.Mark(t1.ID, TaskRunning, "", "")
	q.Mark(t1.ID, TaskFailed, "", "第三次失败")
	got, _ = q.Get(t1.ID)
	if got.Status != TaskFailed || got.RetryCount != 2 {
		t.Fatalf("超过上限应保持 failed: status=%s retry=%d", got.Status, got.RetryCount)
	}
	if q.PendingCount() != 0 {
		t.Fatalf("无待执行任务, pending=%d", q.PendingCount())
	}
}

func TestTaskQueueRetryDisabled(t *testing.T) {
	q := NewTaskQueue()
	t1 := q.Enqueue(InvestigationTask{Type: TaskPathTrace, Priority: 1, Round: 1}) // MaxRetries=0
	q.Mark(t1.ID, TaskRunning, "", "")
	q.Mark(t1.ID, TaskFailed, "", "失败")
	got, _ := q.Get(t1.ID)
	if got.Status != TaskFailed {
		t.Fatalf("未配置重试应保持 failed, got %s", got.Status)
	}
}

func TestTaskQueueTimeoutExpiry(t *testing.T) {
	q := NewTaskQueue()
	t1 := q.Enqueue(InvestigationTask{Type: TaskPathTrace, TimeoutSec: 10, Priority: 1, Round: 1})
	q.Mark(t1.ID, TaskRunning, "", "")
	start := time.Now().Unix()
	// 未超时
	if q.IsExpired(t1.ID, t1.TimeoutSec, start+5) {
		t.Fatal("5 秒内不应视为超时")
	}
	// 超过超时阈值
	if !q.IsExpired(t1.ID, t1.TimeoutSec, start+11) {
		t.Fatal("超过 10 秒应视为超时")
	}
	// 非 running / 未配置超时
	q.Mark(t1.ID, TaskDone, "完成", "")
	if q.IsExpired(t1.ID, t1.TimeoutSec, start+100) {
		t.Fatal("已完成任务不应视为超时")
	}
	t2 := q.Enqueue(InvestigationTask{Type: TaskPathTrace, Priority: 1, Round: 1}) // 无超时
	q.Mark(t2.ID, TaskRunning, "", "")
	if q.IsExpired(t2.ID, 0, time.Now().Unix()+1000) {
		t.Fatal("未配置超时不应过期")
	}
}

// ── Observation Engine 测试（设计 §8）──

func TestObservationDedupe(t *testing.T) {
	mem := &InvestigationMemory{
		DiscoveredAt:  map[string]time.Time{addrC: time.Now().UTC()},
		AnalyzedPaths: []string{addrA + "→" + addrB},
	}
	obs := NewObservationEngine()
	paths := []FundPath{
		{Nodes: []string{addrA, addrB}, Edges: []FundEdge{edge(addrA, addrB, "USDT", "1000000", 1000)}}, // 已分析 → 跳过
		{Nodes: []string{addrA, addrB, addrC}, Edges: []FundEdge{edge(addrA, addrB, "USDT", "1000000", 1000), edge(addrB, addrC, "USDT", "950000", 2000)}},
	}
	got := obs.ObservePaths(1, "PATH_TRACE", paths, mem)
	if len(got) != 3 { // 1 NEW_PATH + 2 NEW_ADDRESS（A/B 新，C 已发现）
		t.Fatalf("应观察 3 条（1 路径 + 2 地址）, got %d: %+v", len(got), got)
	}
	newPaths := 0
	for _, o := range got {
		if o.Type == ObsNewPath {
			newPaths++
		}
	}
	if newPaths != 1 {
		t.Fatalf("应恰有 1 条新路径观察, got %d", newPaths)
	}
	// 重复路径不再次观察
	got2 := obs.ObservePaths(1, "PATH_TRACE", paths, mem)
	if len(got2) != 0 {
		t.Fatalf("重复路径不应再观察, got %d", len(got2))
	}
}

func TestObservationFlowsAndRisk(t *testing.T) {
	obs := NewObservationEngine()
	flows := []FundEdge{edge(addrA, addrB, "USDT", "100", 1), edge(addrA, addrB, "USDT", "100", 1)}
	got := obs.ObserveFlows(1, "FLOW_ANALYSIS", addrA, flows, nil)
	if len(got) != 2 { // 1 交易 + 1 地址（B）
		t.Fatalf("应观察 2 条, got %d", len(got))
	}
	patterns := []RiskPattern{
		{Type: PatternRapidTransfer, Address: addrA, Severity: "high"},
		{Type: PatternRapidTransfer, Address: addrA, Severity: "high"}, // 重复
	}
	got2 := obs.ObservePatterns(1, "RISK_SCAN", patterns)
	if len(got2) != 1 {
		t.Fatalf("风险事件应去重为 1, got %d", len(got2))
	}
	if got2[0].Value != 80 {
		t.Fatalf("high 风险权重应为 80, got %.0f", got2[0].Value)
	}
}

// ── Decision Engine 测试（设计 §9/§10/§11）──

func newDecideEngine() *DecisionEngine {
	return NewDecisionEngine(DefaultConfig())
}

func TestDecideExpand(t *testing.T) {
	in := DecideInput{
		Target: addrA, Round: 1,
		Paths: []RankedPath{{Path: FundPath{Nodes: []string{addrA, addrB}}, Score: PathScore{Total: 80}}},
		Candidates: []ExpansionResult{
			{Address: addrF, Entity: "wallet", Score: 90},
			{Address: addrG, Entity: "wallet", Score: 70},
			{Address: addrH, Entity: "wallet", Score: 60},
			{Address: addrE, Entity: "wallet", Score: 55}, // Top 3 之外
		},
	}
	dec := newDecideEngine().Decide(in)
	if dec.Action != DecisionExpand {
		t.Fatalf("应 EXPAND, got %s（原因 %v）", dec.Action, dec.Reasons)
	}
	if len(dec.NextTargets) != 3 {
		t.Fatalf("NextTargets 应为 Top 3, got %d", len(dec.NextTargets))
	}
	if dec.NextTargets[0] != addrF {
		t.Fatalf("最高分候选应优先, got %s", dec.NextTargets[0])
	}
	if dec.Scores.ExpansionScore != 90 {
		t.Fatalf("ExpansionScore 应为 90, got %.1f", dec.Scores.ExpansionScore)
	}
}

func TestDecideStopNoCandidates(t *testing.T) {
	dec := newDecideEngine().Decide(DecideInput{Target: addrA, Round: 1})
	if dec.Action != DecisionStop {
		t.Fatalf("无候选应 STOP, got %s", dec.Action)
	}
	if !hasReason(dec.Reasons, "无高价值扩展候选") {
		t.Fatalf("停止原因应为无候选, got %v", dec.Reasons)
	}
}

func TestDecideStopExchangeCandidates(t *testing.T) {
	in := DecideInput{
		Target: addrA, Round: 1,
		Candidates: []ExpansionResult{
			{Address: addrF, Entity: "wallet", Score: 90},
			{Address: addrG, Entity: "exchange", Score: 95},
		},
		Entities: []EntityInfo{
			{Address: addrF, Entity: "exchange"}, // 已识别为交易所
		},
	}
	dec := newDecideEngine().Decide(in)
	if dec.Action != DecisionStop {
		t.Fatalf("全部候选为交易所应 STOP, got %s", dec.Action)
	}
	if !hasReason(dec.Reasons, "交易所地址") {
		t.Fatalf("应提示交易所停止, got %v", dec.Reasons)
	}
}

func TestDecideStopLowValue(t *testing.T) {
	in := DecideInput{
		Target: addrA, Round: 1,
		Candidates: []ExpansionResult{{Address: addrF, Entity: "wallet", Score: 10}}, // 低于门槛 50
	}
	dec := newDecideEngine().Decide(in)
	if dec.Action != DecisionStop {
		t.Fatalf("低价值候选应 STOP, got %s", dec.Action)
	}
	if !hasReason(dec.Reasons, "低价值") {
		t.Fatalf("应提示低价值, got %v", dec.Reasons)
	}
}

func TestDecideStopAnalyzedCandidates(t *testing.T) {
	in := DecideInput{
		Target: addrA, Round: 1,
		Candidates: []ExpansionResult{{Address: addrF, Entity: "wallet", Score: 90}},
		Memory: &InvestigationMemory{
			DiscoveredAt:    map[string]time.Time{addrF: time.Now().UTC()},
			IgnoredEntities: []string{addrG},
		},
	}
	dec := newDecideEngine().Decide(in)
	if dec.Action != DecisionStop {
		t.Fatalf("已分析候选应 STOP, got %s", dec.Action)
	}
	if !hasReason(dec.Reasons, "重复关系") {
		t.Fatalf("应提示重复关系, got %v", dec.Reasons)
	}
}

func TestDecideStopMaxRounds(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxRounds = 2
	dec := NewDecisionEngine(cfg).Decide(DecideInput{
		Target: addrA, Round: 2,
		Candidates: []ExpansionResult{{Address: addrF, Entity: "wallet", Score: 90}},
	})
	if dec.Action != DecisionStop {
		t.Fatalf("达到最大轮次应 STOP, got %s", dec.Action)
	}
	if !hasReason(dec.Reasons, "最大调查轮次") {
		t.Fatalf("应提示最大轮次, got %v", dec.Reasons)
	}
}

func TestDecideStopMaxRuntime(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxRuntimeMS = 100
	dec := NewDecisionEngine(cfg).Decide(DecideInput{
		Target: addrA, Round: 1, Elapsed: 200 * time.Millisecond,
		Candidates: []ExpansionResult{{Address: addrF, Entity: "wallet", Score: 90}},
	})
	if dec.Action != DecisionStop {
		t.Fatalf("超过最长运行时间应 STOP, got %s", dec.Action)
	}
	if !hasReason(dec.Reasons, "最长运行时间") {
		t.Fatalf("应提示运行时间, got %v", dec.Reasons)
	}
}

func TestDecideDeepAnalysis(t *testing.T) {
	in := DecideInput{
		Target: addrA, Round: 1,
		Patterns: []RiskPattern{{Type: PatternRapidTransfer, Address: addrA, Severity: "high"}},
	}
	dec := newDecideEngine().Decide(in)
	if dec.Action != DecisionDeepAnalysis {
		t.Fatalf("高风险无候选应 DEEP_ANALYSIS, got %s", dec.Action)
	}
	if dec.Scores.RiskScore != 80 {
		t.Fatalf("RiskScore 应为 80, got %.1f", dec.Scores.RiskScore)
	}
}

func TestDecideNoNewObservationRound2(t *testing.T) {
	in := DecideInput{Target: addrA, Round: 2, NewObs: nil}
	dec := newDecideEngine().Decide(in)
	if dec.Action != DecisionStop {
		t.Fatalf("第 2 轮无新发现应 STOP, got %s", dec.Action)
	}
	if !hasReason(dec.Reasons, "无新发现") {
		t.Fatalf("应提示无新发现, got %v", dec.Reasons)
	}
}

// ── 调查闭环集成测试（设计 §5/§10/§11/§16）──

// fakeExpander 是 Expander 的测试实现（按目标返回候选）。
type fakeExpander struct {
	results map[string][]ExpansionResult
}

func newFakeExpander() *fakeExpander { return &fakeExpander{results: map[string][]ExpansionResult{}} }

func (f *fakeExpander) set(target string, candidates ...ExpansionResult) {
	f.results[target] = candidates
}

func (f *fakeExpander) Expand(_ context.Context, target string, _ int) ([]ExpansionResult, error) {
	return append([]ExpansionResult(nil), f.results[target]...), nil
}

// newLoopTestAgent 构造闭环测试代理（fake 数据源 + 可选 fake 扩展器）。
func newLoopTestAgent(src *FakeFlowSource, expander Expander, cfg IntelligenceConfig) *InvestigationAgent {
	ranker := DefaultPathRanker()
	agent := &InvestigationAgent{
		flowSource:     src,
		ranker:         ranker,
		tracer:         NewFundTracer(src, ranker, cfg),
		planner:        NewPlanner(cfg),
		detector:       NewPatternDetector(cfg),
		report:         NewReportAgent(cfg),
		contextBuilder: NewAIContextBuilder(cfg),
		deepseek:       NewDeepSeekClient("", cfg.AIModel, cfg.AITimeoutMS),
		entityResolver: NewEntityResolver(nil, nil),
		expansion:      expander,
		cfg:            cfg,
		active:         make(map[string]*Investigation),
		history:        make(map[string]*Investigation),
		memories:       NewMemoryStore(""),
	}
	return agent
}

// TestLoopMultiRoundExpansion 验证多轮闭环：EXPAND → 扩展新地址 → 无候选 STOP。
func TestLoopMultiRoundExpansion(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(addrA, []FundEdge{edge(addrA, addrB, "USDT", "1000000", 1000)})
	src.SetFlows(addrB, []FundEdge{edge(addrB, addrC, "USDT", "950000", 2000)})
	src.SetFlows(addrF, []FundEdge{edge(addrF, addrG, "USDT", "800000", 3000)})

	exp := newFakeExpander()
	exp.set(addrA, ExpansionResult{Address: addrF, Entity: "wallet", Score: 90, Depth: 1})
	exp.set(addrF, ExpansionResult{Address: addrK, Entity: "wallet", Score: 85, Depth: 2})

	cfg := DefaultConfig()
	cfg.UseAI = false
	cfg.MaxRounds = 3

	agent := newLoopTestAgent(src, exp, cfg)
	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	done := waitStatus(t, agent, inv.ID, InvestigationCompleted, 5*time.Second)

	// 多轮闭环
	if len(done.Rounds) != 3 {
		t.Fatalf("应执行 3 轮, got %d: %+v", len(done.Rounds), done.Rounds)
	}
	if done.Rounds[0].Decision != DecisionExpand || done.Rounds[1].Decision != DecisionExpand {
		t.Fatalf("前两轮应 EXPAND, got %v / %v", done.Rounds[0].Decision, done.Rounds[1].Decision)
	}
	if done.Rounds[2].Decision != DecisionStop {
		t.Fatalf("第三轮应 STOP, got %v", done.Rounds[2].Decision)
	}
	if done.Decision == nil || done.Decision.Action != DecisionStop {
		t.Fatalf("最终决策应为 STOP, got %+v", done.Decision)
	}
	if done.StopReason == "" {
		t.Fatal("应记录停止原因")
	}
	// 路径：第二轮应扩展发现 F→G
	found := false
	for _, p := range done.Paths {
		if len(p.Path.Nodes) >= 2 && p.Path.Nodes[0] == addrF && p.Path.Nodes[1] == addrG {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("扩展轮应发现 F→G 路径, got %+v", done.Paths)
	}
	// 观察：新地址/新路径/新交易
	types := map[ObservationType]int{}
	for _, o := range done.Observations {
		types[o.Type]++
	}
	if types[ObsNewAddress] < 4 { // B/C/F/G/K
		t.Fatalf("应观察到至少 4 个新地址, got %d", types[ObsNewAddress])
	}
	if types[ObsNewPath] < 3 { // A→B / A→B→C / F→G
		t.Fatalf("应观察到至少 3 条新路径, got %d", types[ObsNewPath])
	}
	// 任务：EXPAND_ADDRESS 与 GENERATE_REPORT 均 done
	expandTasks, reportTask := 0, 0
	for _, tk := range done.Tasks {
		if tk.Type == TaskExpandAddress && tk.Status == TaskDone {
			expandTasks++
		}
		if tk.Type == TaskGenerateReport && tk.Status == TaskDone {
			reportTask++
		}
	}
	if expandTasks < 2 {
		t.Fatalf("EXPAND_ADDRESS 任务应 ≥2 个 done, got %d", expandTasks)
	}
	if reportTask != 1 {
		t.Fatalf("GENERATE_REPORT 任务应恰 1 个 done, got %d", reportTask)
	}
	// 记忆：已完成任务已记录
	mem, ok := agent.memories.Get(inv.ID)
	if !ok || len(mem.CompletedTasks) == 0 {
		t.Fatal("记忆应记录已完成任务")
	}
	// 报告已生成
	if done.Report == nil || done.Report.Content == "" {
		t.Fatal("调查完成应生成报告")
	}
}

// TestLoopStopNoCandidates 验证无扩展引擎时单轮完成（智能停止）。
func TestLoopStopNoCandidates(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(addrA, []FundEdge{edge(addrA, addrB, "USDT", "1000000", 1000)})

	cfg := DefaultConfig()
	cfg.UseAI = false
	agent := newLoopTestAgent(src, nil, cfg)

	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	done := waitStatus(t, agent, inv.ID, InvestigationCompleted, 5*time.Second)
	if len(done.Rounds) != 1 {
		t.Fatalf("无候选应 1 轮完成, got %d", len(done.Rounds))
	}
	if done.StopReason == "" {
		t.Fatal("应记录停止原因（无高价值扩展候选）")
	}
	if len(done.Paths) == 0 {
		t.Fatal("仍应发现资金路径")
	}
}

// TestLoopMaxRounds 验证达到最大轮次后自动停止。
func TestLoopMaxRounds(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(addrA, []FundEdge{edge(addrA, addrB, "USDT", "1000000", 1000)})

	exp := newFakeExpander()
	exp.set(addrA, ExpansionResult{Address: addrF, Entity: "wallet", Score: 90})
	exp.set(addrF, ExpansionResult{Address: addrG, Entity: "wallet", Score: 80})
	exp.set(addrG, ExpansionResult{Address: addrH, Entity: "wallet", Score: 70})

	cfg := DefaultConfig()
	cfg.UseAI = false
	cfg.MaxRounds = 3
	agent := newLoopTestAgent(src, exp, cfg)

	inv, _ := agent.Start(context.Background(), addrA, "bsc")
	done := waitStatus(t, agent, inv.ID, InvestigationCompleted, 5*time.Second)
	if len(done.Rounds) != 3 {
		t.Fatalf("应恰好 3 轮, got %d", len(done.Rounds))
	}
	if done.Decision == nil || done.Decision.Action != DecisionStop {
		t.Fatalf("最终应为 STOP, got %+v", done.Decision)
	}
	if !strings.Contains(done.StopReason, "最大调查轮次") {
		t.Fatalf("应提示最大轮次, got %s", done.StopReason)
	}
}

// TestLoopTasksSkipped 验证缺少数据源时任务跳过且调查正常完成。
func TestLoopTasksSkipped(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UseAI = false
	// 无数据源（flowSource/svc/expansion 均 nil）
	agent := &InvestigationAgent{
		ranker:         DefaultPathRanker(),
		planner:        NewPlanner(cfg),
		detector:       NewPatternDetector(cfg),
		report:         NewReportAgent(cfg),
		contextBuilder: NewAIContextBuilder(cfg),
		deepseek:       NewDeepSeekClient("", cfg.AIModel, cfg.AITimeoutMS),
		entityResolver: NewEntityResolver(nil, nil),
		cfg:            cfg,
		active:         make(map[string]*Investigation),
		history:        make(map[string]*Investigation),
		memories:       NewMemoryStore(""),
	}
	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	done := waitStatus(t, agent, inv.ID, InvestigationCompleted, 5*time.Second)
	if done.Status != InvestigationCompleted {
		t.Fatalf("应正常完成, got %s", done.Status)
	}
	skipped := 0
	failed := 0
	for _, tk := range done.Tasks {
		switch tk.Status {
		case TaskSkipped:
			skipped++
		case TaskFailed:
			failed++
		}
	}
	// V2 计划驱动：缺依赖任务空成功或跳过，但绝不应失败
	if failed > 0 {
		t.Fatal("缺依赖任务不应失败")
	}
	if len(done.Tasks) == 0 {
		t.Fatal("应生成调查任务")
	}
	_ = skipped
	if done.StopReason == "" {
		t.Fatal("应记录停止原因")
	}
}

// ── 代码审查修复回归测试 ──

// TestTaskQueueTerminalGuard 验证任务终态不可流转（防重复执行）。
func TestTaskQueueTerminalGuard(t *testing.T) {
	q := NewTaskQueue()
	t1 := q.Enqueue(InvestigationTask{Type: TaskPathTrace, Priority: 1, Target: addrA, Round: 1})
	q.Mark(t1.ID, TaskDone, "完成", "")
	q.Mark(t1.ID, TaskRunning, "", "") // 终态不可流转
	got, _ := q.Get(t1.ID)
	if got.Status != TaskDone {
		t.Fatalf("done 任务不应被流转为 running, got %s", got.Status)
	}
}

// TestLoopTasksCarryRound 验证任务队列跨轮去重键含轮次（每轮任务 Round > 0）。
func TestLoopTasksCarryRound(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(addrA, []FundEdge{edge(addrA, addrB, "USDT", "1000000", 1000)})
	exp := newFakeExpander()
	exp.set(addrA, ExpansionResult{Address: addrF, Entity: "wallet", Score: 90})

	cfg := DefaultConfig()
	cfg.UseAI = false
	cfg.MaxRounds = 2
	agent := newLoopTestAgent(src, exp, cfg)

	inv, _ := agent.Start(context.Background(), addrA, "bsc")
	done := waitStatus(t, agent, inv.ID, InvestigationCompleted, 5*time.Second)
	if len(done.Tasks) == 0 {
		t.Fatal("应有任务记录")
	}
	for _, tk := range done.Tasks {
		if tk.Round < 1 {
			t.Fatalf("任务 %s 轮次应为正数, got %d", tk.ID, tk.Round)
		}
	}
	// 跨轮同类型同目标任务应各自独立（Round 不同 → 不去重）
	seen := map[string]bool{}
	for _, tk := range done.Tasks {
		key := fmt.Sprintf("%d-%s-%s", tk.Round, tk.Type, tk.Target)
		if seen[key] {
			t.Fatalf("任务键重复: %s", key)
		}
		seen[key] = true
	}
}

// TestStartConfigOverrideIsolated 验证 POST /start 的 config 仅作用于本次调查，
// 不污染全局配置（且调查按覆盖配置执行）。
func TestStartConfigOverrideIsolated(t *testing.T) {
	agent := newTestAgent()
	h := NewHandler(agent)
	rr := doJSON(h, http.MethodPost, "/intelligence/investigations",
		`{"target":"0x00000000000000000000000000000000000000a1","config":{"max_rounds":1}}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("start 应 202, got %d: %s", rr.Code, rr.Body.String())
	}
	var inv Investigation
	_ = json.Unmarshal(rr.Body.Bytes(), &inv)

	// 全局配置未被污染
	rr2 := doJSON(h, http.MethodGet, "/intelligence/config", "")
	var cfg IntelligenceConfig
	_ = json.Unmarshal(rr2.Body.Bytes(), &cfg)
	if cfg.MaxRounds != 3 {
		t.Fatalf("start 配置不应污染全局 max_rounds, got %d", cfg.MaxRounds)
	}
	// 该调查按覆盖配置（1 轮）执行
	done := waitStatus(t, agent, inv.ID, InvestigationCompleted, 5*time.Second)
	if len(done.Rounds) != 1 {
		t.Fatalf("覆盖 max_rounds=1 应执行 1 轮, got %d", len(done.Rounds))
	}
}

// TestLoopAISuggestionOverridesStop 回归（#5 优化）：规则 STOP + AI 高置信度 EXPAND 建议
// （带合法 target）→ 决策改为 EXPAND 延续一轮；无 target/低置信度不覆盖。
func TestLoopAISuggestionOverridesStop(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(addrA, []FundEdge{edge(addrA, addrB, "USDT", "1000000", 1000)})
	exp := newFakeExpander()
	exp.set(addrA, ExpansionResult{Address: addrF, Entity: "wallet", Score: 60, Depth: 1})

	// fake AI：Suggest 返回 EXPAND 建议（带合法 target 0x...00b1，conf 0.9）
	ai := newFakeAIChatter()
	ai.responses["下一步动作建议"] = `{"action":"EXPAND","target":"0x00000000000000000000000000000000000000b1","reasons":["高价值路径待确认"],"confidence":0.9,"source":"analysis"}`

	cfg := DefaultConfig()
	cfg.UseAI = true
	cfg.MaxRounds = 3
	cfg.MaxAddresses = 10

	agent := newLoopTestAgent(src, exp, cfg)
	agent.aiChatter = ai // Start 的 rebuild 会用 aiChatter 重建 AI Agent
	agent.ai = NewAIAgentWithStore(ai, src, cfg, nil)
	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := waitStatus(t, agent, inv.ID, InvestigationCompleted, 8*time.Second)

	if done.Decision == nil || done.Decision.Action != DecisionStop {
		t.Fatalf("最终决策应为 STOP, got %+v", done.Decision)
	}
	// 第 1 轮规则为 STOP（无候选或低价值），AI 建议应将其改写为 EXPAND → 至少 2 轮
	if len(done.Rounds) < 2 {
		t.Fatalf("AI 建议应延续调查至至少 2 轮, got %d 轮: %+v", len(done.Rounds), done.Rounds)
	}
	if done.Rounds[0].Decision != DecisionExpand {
		t.Fatalf("第 1 轮决策应被 AI 建议改写为 EXPAND, got %v", done.Rounds[0].Decision)
	}
	if done.AISuggestion == nil || done.AISuggestion.Action != "EXPAND" {
		t.Fatalf("应记录 AI 建议, got %+v", done.AISuggestion)
	}
	// 至少一轮理由应含 AI 建议说明（AI 覆盖发生在规则 STOP 的那一轮）
	aiOverride := false
	for _, r := range done.Rounds {
		if strings.Contains(r.Note, "AI 建议继续扩展") {
			aiOverride = true
			break
		}
	}
	if !aiOverride {
		t.Errorf("应至少有一轮含 AI 建议理由, rounds=%+v", done.Rounds)
	}
}

// TestLoopAISuggestionLowConfidenceNoOverride 回归（#5）：低置信度或无 target 的
// AI EXPAND 建议不覆盖规则 STOP（规则为最终裁决）。
func TestLoopAISuggestionLowConfidenceNoOverride(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(addrA, []FundEdge{edge(addrA, addrB, "USDT", "1000000", 1000)})
	exp := newFakeExpander()

	ai := newFakeAIChatter()
	ai.responses["下一步动作建议"] = `{"action":"EXPAND","target":"0x00000000000000000000000000000000000000b1","reasons":["低置信度"],"confidence":0.5,"source":"analysis"}`

	cfg := DefaultConfig()
	cfg.UseAI = true
	cfg.MaxRounds = 3
	cfg.MaxAddresses = 10

	agent := newLoopTestAgent(src, exp, cfg)
	agent.aiChatter = ai // Start 的 rebuild 会用 aiChatter 重建 AI Agent
	agent.ai = NewAIAgentWithStore(ai, src, cfg, nil)
	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := waitStatus(t, agent, inv.ID, InvestigationCompleted, 8*time.Second)

	if done.Decision == nil || done.Decision.Action != DecisionStop {
		t.Fatalf("低置信度建议不应改变最终 STOP, got %+v", done.Decision)
	}
	if len(done.Rounds) != 1 {
		t.Fatalf("低置信度建议应单轮结束, got %d 轮", len(done.Rounds))
	}
}

// TestDecideStopMaxAddresses 验证 max_addresses 上限包含未记入记忆的扩展候选。
func TestDecideStopMaxAddresses(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxAddresses = 5
	dec := NewDecisionEngine(cfg).Decide(DecideInput{
		Target:          addrA,
		Round:           1,
		Candidates:      []ExpansionResult{{Address: addrF, Entity: "wallet", Score: 90}},
		Memory:          &InvestigationMemory{DiscoveredAt: map[string]time.Time{}},
		TotalDiscovered: 5,
	})
	if dec.Action != DecisionStop {
		t.Fatalf("累计发现 5 个地址应 STOP, got %s", dec.Action)
	}
	if !hasReason(dec.Reasons, "最大发现地址数") {
		t.Fatalf("应提示最大地址数, got %v", dec.Reasons)
	}
}

// TestDecideContinueForHypothesisVerification 验证存在待验证假设时
// 决策引擎继续调查而非停止（§7/§6：AI 驱动任务生成，规则引擎验证）。
func TestDecideContinueForHypothesisVerification(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxRounds = 3
	dec := NewDecisionEngine(cfg).Decide(DecideInput{
		Target:               addrA,
		Round:                1,
		PendingVerifications: 2,
		VerifyTargets:        []string{addrB, addrC},
	})
	if dec.Action != DecisionExpand {
		t.Fatalf("有待验证假设应 EXPAND 继续, got %s（原因 %v）", dec.Action, dec.Reasons)
	}
	if len(dec.NextTargets) != 2 || dec.NextTargets[0] != addrB {
		t.Fatalf("下一轮目标应为验证目标, got %v", dec.NextTargets)
	}
	if !hasReason(dec.Reasons, "待验证调查假设") {
		t.Fatalf("应提示假设验证, got %v", dec.Reasons)
	}
	// 最后一轮仍应停止
	dec = NewDecisionEngine(cfg).Decide(DecideInput{
		Target: addrA, Round: 3, PendingVerifications: 2,
	})
	if dec.Action != DecisionStop {
		t.Fatalf("最后一轮应 STOP, got %s", dec.Action)
	}
}
