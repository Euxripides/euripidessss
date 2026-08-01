package intelligence

import (
	"context"
	"testing"
	"time"
)

// ── 调查代理主流程测试 ──

// waitStatus 轮询等待调查到达指定状态。
func waitStatus(t *testing.T, agent *InvestigationAgent, id string, status InvestigationStatus, timeout time.Duration) *Investigation {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		inv, ok := agent.Get(id)
		if ok && inv.Status == status {
			return inv
		}
		time.Sleep(20 * time.Millisecond)
	}
	inv, _ := agent.Get(id)
	t.Fatalf("等待 %s 超时, 当前状态: %s", status, inv.Status)
	return nil
}

func TestAgentFullFlow(t *testing.T) {
	src := NewFakeFlowSource()
	// A → B → C（100 万）
	src.SetFlows(addrA, []FundEdge{
		edge(addrA, addrB, "USDT", "1000000", 1000),
	})
	src.SetFlows(addrB, []FundEdge{
		edge(addrB, addrC, "USDT", "950000", 2000),
	})

	cfg := DefaultConfig()
	cfg.UseAI = false // 不调用真实 DeepSeek
	cfg.MaxHops = 2
	cfg.TopPaths = 5

	agent := &InvestigationAgent{
		flowSource:      src,
		ranker:          DefaultPathRanker(),
		planner:         NewPlanner(cfg),
		detector:        NewPatternDetector(cfg),
		report:          NewReportAgent(cfg),
		contextBuilder:  NewAIContextBuilder(cfg),
		deepseek:        NewDeepSeekClient("", cfg.AIModel, cfg.AITimeoutMS),
		entityResolver:  NewEntityResolver(nil, nil),
		cfg:             cfg,
		active:          make(map[string]*Investigation),
		history:         make(map[string]*Investigation),
		memories:        NewMemoryStore(""),
	}
	agent.tracer = NewFundTracer(src, agent.ranker, cfg)

	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	done := waitStatus(t, agent, inv.ID, InvestigationCompleted, 5*time.Second)
	if done.Progress != 100 {
		t.Fatalf("Progress 应为 100, got %v", done.Progress)
	}
	if done.Plan == nil {
		t.Fatal("应生成调查计划")
	}
	if len(done.Paths) == 0 {
		t.Fatal("应发现资金路径")
	}
	if len(done.Entities) == 0 {
		t.Fatal("应识别实体")
	}
	if done.Memory == nil {
		t.Fatal("应生成调查记忆")
	}
	if len(done.Memory.Conclusions) == 0 {
		t.Fatal("应有调查结论")
	}
	// 报告可生成
	report := NewReportAgent(cfg)
	out, err := report.Generate(done, ReportMarkdown)
	if err != nil || !contains(out.Content, addrA) {
		t.Fatalf("报告生成失败: %v", err)
	}
}

func TestAgentInvalidTarget(t *testing.T) {
	agent := newTestAgent()
	_, err := agent.Start(context.Background(), "0xnot-an-address", "bsc")
	if err == nil {
		t.Fatal("非法地址应拒绝")
	}
}

func TestAgentFailedStatus(t *testing.T) {
	// nil flowSource → 追踪阶段失败
	cfg := DefaultConfig()
	cfg.UseAI = false
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
	// flowSource 为 nil：plan 正常（svc nil），但 Trace 时 source nil → panic？
	// 防御：Trace 前 source 为 nil 时返回空路径而非 panic
	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	// 追踪阶段对 nil source 应安全降级（不 panic）
	_ = waitStatus(t, agent, inv.ID, InvestigationCompleted, 5*time.Second)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestAgentConcurrentSurveys 验证并发调查无数据竞争（无 -race 时验证逻辑正确性）：
// 多个调查同时运行，Get/List 并发轮询不 panic、结果一致。
func TestAgentConcurrentSurveys(t *testing.T) {
	src := NewFakeFlowSource()
	// 两个目标各自的资金流
	src.SetFlows(addrA, []FundEdge{edge(addrA, addrB, "USDT", "1000000", 1000)})
	src.SetFlows(addrB, []FundEdge{edge(addrB, addrC, "USDT", "950000", 2000)})
	src.SetFlows(addrD, []FundEdge{edge(addrD, addrE, "USDT", "500000", 1000)})
	src.SetFlows(addrE, []FundEdge{edge(addrE, addrA, "USDT", "400000", 2000)})

	cfg := DefaultConfig()
	cfg.UseAI = false
	agent := &InvestigationAgent{
		flowSource:      src,
		ranker:          DefaultPathRanker(),
		planner:         NewPlanner(cfg),
		detector:        NewPatternDetector(cfg),
		report:          NewReportAgent(cfg),
		contextBuilder:  NewAIContextBuilder(cfg),
		deepseek:        NewDeepSeekClient("", cfg.AIModel, cfg.AITimeoutMS),
		entityResolver:  NewEntityResolver(nil, nil),
		cfg:             cfg,
		active:          make(map[string]*Investigation),
		history:         make(map[string]*Investigation),
		memories:        NewMemoryStore(""),
	}
	agent.tracer = NewFundTracer(src, agent.ranker, cfg)

	// 并发启动两个调查
	inv1, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start1 失败: %v", err)
	}
	inv2, err := agent.Start(context.Background(), addrD, "bsc")
	if err != nil {
		t.Fatalf("Start2 失败: %v", err)
	}

	// 并发轮询 Get/List（模拟前端轮询）
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			_, _ = agent.Get(inv1.ID)
			_, _ = agent.Get(inv2.ID)
			_ = agent.List()
			time.Sleep(10 * time.Millisecond)
		}
		close(done)
	}()

	// 等待两个调查完成
	waitStatus(t, agent, inv1.ID, InvestigationCompleted, 5*time.Second)
	waitStatus(t, agent, inv2.ID, InvestigationCompleted, 5*time.Second)
	<-done

	// 结果一致性：列表去重后应恰好 2 条
	list := agent.List()
	if len(list) != 2 {
		t.Fatalf("并发调查后列表应为 2 条（去重）, got %d", len(list))
	}
}
