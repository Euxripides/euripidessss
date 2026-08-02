package intelligence

import (
	"context"
	"testing"
)

// ── Re-plan 触发器测试（V2 设计 §9）──

func newReplanContext(t *testing.T) (*InvestigationAgent, *LoopEngine, *TaskQueue, *Investigation, *roundState, *InvestigationPlan) {
	t.Helper()
	agent := newTestAgent()
	src := NewFakeFlowSource()
	// 高价值路径：1000 万 USDT 流入
	src.SetFlows(addrA, []FundEdge{
		edge(addrB, addrA, "USDT", "10000000000000000000000000", 1700000000),
	})
	agent.flowSource = src
	cfg := DefaultConfig()
	cfg.UseAI = false
	agent.tracer = NewFundTracer(src, DefaultPathRanker(), cfg)
	agent.planner = NewPlanner(cfg)
	agent.svc = nil

	e := NewLoopEngine()
	queue := NewTaskQueue()
	inv := &Investigation{ID: "inv-1", Target: addrA}
	plan := &InvestigationPlan{Target: addrA, MaxHops: 2, BeamWidth: 4, Tasks: []PlannedTask{{Type: TaskPathTrace, Priority: 1}}}
	st := &roundState{focus: []string{addrA}, flowsByAddr: map[string][]FundEdge{}}
	return agent, e, queue, inv, st, plan
}

func TestReplanHighValueFund(t *testing.T) {
	agent, e, queue, inv, st, plan := newReplanContext(t)
	// 模拟本轮发现高价值路径（1000 万 USDT = 1e25 wei，超过 1e24 阈值）
	st.newPaths = []FundPath{{
		Nodes: []string{addrB, addrA},
		Edges: []FundEdge{{From: addrB, To: addrA, Token: "USDT", Amount: "10000000000000000000000000", Block: 1}},
	}}
	signals := e.evaluateReplan(context.Background(), agent, agentSnapshot{planner: agent.planner}, queue, inv, st, plan, 1, 50)
	if len(signals) == 0 {
		t.Fatal("高价值资金应触发 Re-plan")
	}
	found := false
	for _, s := range signals {
		if s.Reason == ReplanHighValue {
			found = true
		}
	}
	if !found {
		t.Fatalf("应包含 HIGH_VALUE_FUND 信号: %+v", signals)
	}
	// 新任务应已入队（Re-plan 增量规划产物）
	if queue.TotalCount() == 0 {
		t.Fatal("Re-plan 应追加新任务")
	}
}

func TestAmountAboveThreshold(t *testing.T) {
	cases := []struct {
		amount string
		want   bool
	}{
		{"999999999999999999999999", false},  // 略低于 1e24
		{"1000000000000000000000000", true},  // 恰好 1e24
		{"10000000000000000000000000", true}, // 1e25
		{"0xd3c21bcecceda1000000", true},     // 十六进制 1e24
		{"0x0de0b6b3a7640000", false},        // 十六进制 1e18，低于阈值
		{"100", false},                       // 极小金额不触发
		{"", false},                          // 空
		{"not-a-number", false},              // 非法
	}
	for _, c := range cases {
		if got := amountAboveThreshold(c.amount, highValueThreshold); got != c.want {
			t.Fatalf("amountAboveThreshold(%q) = %v, want %v", c.amount, got, c.want)
		}
	}
	// 低价值路径不触发 HIGH_VALUE_FUND
	agent, e, queue, inv, st, plan := newReplanContext(t)
	st.newPaths = []FundPath{{
		Nodes: []string{addrB, addrA},
		Edges: []FundEdge{{From: addrB, To: addrA, Token: "USDT", Amount: "1000000", Block: 1}}, // 1e6 wei，远低于阈值
	}}
	signals := e.evaluateReplan(context.Background(), agent, agentSnapshot{planner: agent.planner}, queue, inv, st, plan, 1, 50)
	for _, s := range signals {
		if s.Reason == ReplanHighValue {
			t.Fatalf("低价值路径不应触发 HIGH_VALUE_FUND: %+v", signals)
		}
	}
}

func TestReplanNewEntity(t *testing.T) {
	agent, e, queue, inv, st, plan := newReplanContext(t)
	st.newEntities = []EntityInfo{{Address: addrB, Entity: "exchange", Label: "Binance"}}
	signals := e.evaluateReplan(context.Background(), agent, agentSnapshot{planner: agent.planner}, queue, inv, st, plan, 1, 50)
	if len(signals) == 0 {
		t.Fatal("新实体应触发 Re-plan")
	}
	found := false
	for _, s := range signals {
		if s.Reason == ReplanNewEntity {
			found = true
		}
	}
	if !found {
		t.Fatalf("应包含 NEW_ENTITY 信号: %+v", signals)
	}
}

func TestReplanNewPath(t *testing.T) {
	agent, e, queue, inv, st, plan := newReplanContext(t)
	st.newPaths = []FundPath{{Nodes: []string{addrB, addrA}}}
	signals := e.evaluateReplan(context.Background(), agent, agentSnapshot{planner: agent.planner}, queue, inv, st, plan, 1, 50)
	if len(signals) == 0 {
		t.Fatal("新路径应触发 Re-plan")
	}
}

func TestReplanNoTriggerWithoutFindings(t *testing.T) {
	agent, e, queue, inv, st, plan := newReplanContext(t)
	// 无高价值/新实体/新路径：不触发
	signals := e.evaluateReplan(context.Background(), agent, agentSnapshot{planner: agent.planner}, queue, inv, st, plan, 1, 50)
	if len(signals) != 0 {
		t.Fatalf("无发现不应触发 Re-plan: %+v", signals)
	}
}

func TestReplanDedupeAndBudget(t *testing.T) {
	agent, e, queue, inv, st, plan := newReplanContext(t)
	st.newPaths = []FundPath{{Nodes: []string{addrB, addrA}}}
	// 预算上限 1：即使触发也只能追加 ≤ 1 个任务
	signals := e.evaluateReplan(context.Background(), agent, agentSnapshot{planner: agent.planner}, queue, inv, st, plan, 1, 1)
	if len(signals) == 0 {
		t.Fatal("应触发")
	}
	if queue.TotalCount() > 1 {
		t.Fatalf("预算限制应封顶任务数, got %d", queue.TotalCount())
	}
	// 幂等：相同输入再次触发（同预算）→ 同轮同类型同目标已存在则不再追加
	before := queue.TotalCount()
	st.newPaths = []FundPath{{Nodes: []string{addrB, addrA}}}
	_ = e.evaluateReplan(context.Background(), agent, agentSnapshot{planner: agent.planner}, queue, inv, st, plan, 1, 1)
	if queue.TotalCount() != before {
		t.Fatalf("同轮重复触发不应追加新任务: before=%d after=%d", before, queue.TotalCount())
	}
	// 下一轮（round 2）：允许追加新任务
	before = queue.TotalCount()
	st.newPaths = []FundPath{{Nodes: []string{addrB, addrA}}}
	_ = e.evaluateReplan(context.Background(), agent, agentSnapshot{planner: agent.planner}, queue, inv, st, plan, 2, 10)
	if queue.TotalCount() <= before {
		t.Fatal("新轮次应允许追加任务")
	}
}

func TestReplanBudgetZeroMeansUnlimited(t *testing.T) {
	agent, e, queue, inv, st, plan := newReplanContext(t)
	st.newPaths = []FundPath{{Nodes: []string{addrB, addrA}}}
	_ = e.evaluateReplan(context.Background(), agent, agentSnapshot{planner: agent.planner}, queue, inv, st, plan, 1, 0)
	if queue.TotalCount() == 0 {
		t.Fatal("MaxTasks=0 表示无预算限制，应追加任务")
	}
}
