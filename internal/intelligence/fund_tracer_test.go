package intelligence

import (
	"context"
	"testing"
)

// ── 资金追踪（Beam Search）测试 ──

const (
	addrA = "0x00000000000000000000000000000000000000a1"
	addrB = "0x00000000000000000000000000000000000000b1"
	addrC = "0x00000000000000000000000000000000000000c1"
	addrD = "0x00000000000000000000000000000000000000d1"
	addrE = "0x00000000000000000000000000000000000000e1"
)

func TestBeamSearchOutgoing(t *testing.T) {
	src := NewFakeFlowSource()
	// A → B → C（100 万），A → D → E（10）
	src.SetFlows(addrA, []FundEdge{
		edge(addrA, addrB, "USDT", "1000000", 1000),
		edge(addrA, addrD, "USDT", "10", 2000),
	})
	src.SetFlows(addrB, []FundEdge{
		edge(addrB, addrC, "USDT", "950000", 2000),
	})
	src.SetFlows(addrD, []FundEdge{
		edge(addrD, addrE, "USDT", "5", 3000),
	})

	cfg := DefaultConfig()
	cfg.MinAmount = "0"
	tracer := NewFundTracer(src, DefaultPathRanker(), cfg)
	paths, err := tracer.Trace(context.Background(), addrA, 2, 4)
	if err != nil {
		t.Fatalf("Trace 失败: %v", err)
	}

	// 出向路径应包含 A→B→C（大额路径）
	foundBig := false
	for _, p := range paths {
		if len(p.Nodes) == 3 && p.Nodes[0] == addrA && p.Nodes[2] == addrC {
			foundBig = true
		}
	}
	if !foundBig {
		t.Fatalf("应发现 A→B→C 路径, got %v", paths)
	}
}

func TestBeamSearchIncoming(t *testing.T) {
	src := NewFakeFlowSource()
	// C → B → A（资金来源）
	src.SetFlows(addrC, []FundEdge{edge(addrC, addrB, "USDT", "5000000", 100)})
	src.SetFlows(addrB, []FundEdge{edge(addrC, addrB, "USDT", "5000000", 100), edge(addrB, addrA, "USDT", "5000000", 200)})
	// A 的入边（反向追踪时查询 A 的 Flows 需含 B→A）
	src.SetFlows(addrA, []FundEdge{edge(addrB, addrA, "USDT", "5000000", 200)})

	cfg := DefaultConfig()
	tracer := NewFundTracer(src, DefaultPathRanker(), cfg)
	paths, err := tracer.Trace(context.Background(), addrA, 2, 4)
	if err != nil {
		t.Fatalf("Trace 失败: %v", err)
	}
	// 入向路径 C→B→A（反向：A 的入边邻居是 B，B 的入边邻居是 C）
	found := false
	for _, p := range paths {
		if len(p.Nodes) >= 2 && p.Nodes[len(p.Nodes)-1] == addrC {
			found = true
		}
	}
	if !found {
		t.Fatalf("应发现资金来源路径（到达 C）, got %v", paths)
	}
}

func TestBeamSearchNoCycle(t *testing.T) {
	src := NewFakeFlowSource()
	// A→B→A 环
	src.SetFlows(addrA, []FundEdge{edge(addrA, addrB, "USDT", "100", 1)})
	src.SetFlows(addrB, []FundEdge{edge(addrB, addrA, "USDT", "100", 2)})

	cfg := DefaultConfig()
	tracer := NewFundTracer(src, DefaultPathRanker(), cfg)
	paths, err := tracer.Trace(context.Background(), addrA, 4, 4)
	if err != nil {
		t.Fatalf("Trace 失败: %v", err)
	}
	for _, p := range paths {
		seen := map[string]bool{}
		for _, n := range p.Nodes {
			if seen[n] {
				t.Fatalf("路径含环: %v", p.Nodes)
			}
			seen[n] = true
		}
	}
}

func TestBeamSearchMinAmount(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(addrA, []FundEdge{
		edge(addrA, addrB, "USDT", "1000000", 1), // ≥ 阈值
		edge(addrA, addrD, "USDT", "10", 2),      // < 阈值
	})

	cfg := DefaultConfig()
	cfg.MinAmount = "100000"
	tracer := NewFundTracer(src, DefaultPathRanker(), cfg)
	paths, err := tracer.Trace(context.Background(), addrA, 1, 4)
	if err != nil {
		t.Fatalf("Trace 失败: %v", err)
	}
	for _, p := range paths {
		for _, e := range p.Edges {
			if e.To == addrD {
				t.Fatalf("低于阈值的边不应出现在路径中: %+v", e)
			}
		}
	}
}

// ── 路径排名测试 ──

func TestPathRankerBigAmount(t *testing.T) {
	r := DefaultPathRanker()
	big := FundPath{Edges: []FundEdge{edge(addrA, addrB, "USDT", "1000000000", 1)}}
	small := FundPath{Edges: []FundEdge{edge(addrA, addrB, "USDT", "100", 1)}}
	s1 := r.RankPath(big, nil)
	s2 := r.RankPath(small, nil)
	if s1.Total <= s2.Total {
		t.Fatalf("大额路径应高于小额路径: %v vs %v", s1.Total, s2.Total)
	}
}

func TestPathRankerTimeContinuity(t *testing.T) {
	r := DefaultPathRanker()
	// 时间连续：1 秒间隔
	cont := FundPath{Edges: []FundEdge{
		edge(addrA, addrB, "USDT", "1000000", 1000),
		edge(addrB, addrC, "USDT", "950000", 1001),
	}}
	// 时间断裂：10 天间隔
	gap := FundPath{Edges: []FundEdge{
		edge(addrA, addrB, "USDT", "1000000", 1000),
		edge(addrB, addrC, "USDT", "950000", 1000+10*86400),
	}}
	s1 := r.RankPath(cont, nil)
	s2 := r.RankPath(gap, nil)
	if s1.TimeContinuity <= s2.TimeContinuity {
		t.Fatalf("时间连续路径应更高: %v vs %v", s1.TimeContinuity, s2.TimeContinuity)
	}
}

func TestPathRankerEntityPenalty(t *testing.T) {
	r := DefaultPathRanker()
	path := FundPath{Edges: []FundEdge{edge(addrA, addrB, "USDT", "1000000", 1)}}
	// A 是交易所 → 惩罚
	entities := map[string]string{addrA: "exchange"}
	s1 := r.RankPath(path, nil)
	s2 := r.RankPath(path, entities)
	if s2.EntityPenalty <= 0 {
		t.Fatalf("交易所实体应有惩罚, got %v", s2.EntityPenalty)
	}
	if s2.Total >= s1.Total {
		t.Fatalf("惩罚后路径应更低: %v vs %v", s1.Total, s2.Total)
	}
}

// ── 调查规划器测试 ──

func TestPlannerWithFlows(t *testing.T) {
	p := NewPlanner(DefaultConfig())
	plan := p.Plan(PlanInput{
		Target:   addrA,
		InCount:  5,
		OutCount: 3,
		HasFlows: true,
	})
	if len(plan.Tasks) < 4 {
		t.Fatalf("有资金流应有 ≥4 个任务, got %d", len(plan.Tasks))
	}
	types := map[string]bool{}
	for _, task := range plan.Tasks {
		types[task.Type] = true
	}
	if !types["FUND_SOURCE"] || !types["FUND_FLOW"] || !types["HIGH_VALUE_PATH"] {
		t.Fatalf("应包含资金来源/流向/高价值路径任务: %v", types)
	}
}

func TestPlannerRiskTask(t *testing.T) {
	p := NewPlanner(DefaultConfig())
	plan := p.Plan(PlanInput{Target: addrA, RiskScore: 80, HasFlows: true})
	found := false
	for _, task := range plan.Tasks {
		if task.Type == "RISK_CHECK" {
			found = true
		}
	}
	if !found {
		t.Fatal("高风险地址应有 RISK_CHECK 任务")
	}
}

func TestPlannerEmptyInput(t *testing.T) {
	p := NewPlanner(DefaultConfig())
	plan := p.Plan(PlanInput{Target: addrA})
	if len(plan.Tasks) == 0 {
		t.Fatal("无信号时应有兜底任务")
	}
}

// ── 模式检测器测试 ──

func TestDetectRapidTransfer(t *testing.T) {
	d := NewPatternDetector(DefaultConfig())
	// 收到 100 万后 1 小时内转出 80 万
	edges := []FundEdge{
		edge(addrX, addrA, "USDT", "1000000", 1000), // 进入
		edge(addrA, addrB, "USDT", "800000", 2000),  // 转出（1 小时内）
	}
	patterns := d.Detect(addrA, edges)
	found := false
	for _, p := range patterns {
		if p.Type == PatternRapidTransfer && p.Severity == "high" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应检测到快速转移, got %+v", patterns)
	}
}

func TestDetectMultiSplit(t *testing.T) {
	d := NewPatternDetector(DefaultConfig())
	edges := []FundEdge{
		edge(addrX, addrA, "USDT", "3000000", 1),
		edge(addrA, addrB, "USDT", "1000000", 2),
		edge(addrA, addrC, "USDT", "1000000", 3),
		edge(addrA, addrD, "USDT", "1000000", 4),
	}
	patterns := d.Detect(addrA, edges)
	found := false
	for _, p := range patterns {
		if p.Type == PatternMultiSplit {
			found = true
		}
	}
	if !found {
		t.Fatalf("应检测到多地址拆分, got %+v", patterns)
	}
}

func TestDetectConcentration(t *testing.T) {
	d := NewPatternDetector(DefaultConfig())
	edges := []FundEdge{
		edge(addrB, addrA, "USDT", "1000000", 1),
		edge(addrC, addrA, "USDT", "1000000", 2),
		edge(addrD, addrA, "USDT", "1000000", 3),
		// 少量转出
		edge(addrA, addrE, "USDT", "100000", 4),
	}
	patterns := d.Detect(addrA, edges)
	found := false
	for _, p := range patterns {
		if p.Type == PatternConcentration {
			found = true
		}
	}
	if !found {
		t.Fatalf("应检测到归集, got %+v", patterns)
	}
}

// 测试用辅助地址
const (
	addrX = "0x00000000000000000000000000000000000000f1"
)
