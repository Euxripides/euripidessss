package intelligence

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

const (
	vTarget = "0x00000000000000000000000000000000000000a1" // 目标
	vIn     = "0x00000000000000000000000000000000000000b2"
	vOut    = "0x00000000000000000000000000000000000000c3"
)

func TestDetectProfitStructureHolding(t *testing.T) {
	flows := []FundEdge{
		edge(vIn, vTarget, "USDT", "5000000000000000000000000", 1),  // 500 万 USDT 入
		edge(vOut, vTarget, "USDT", "1000000000000000000000000", 2), // 100 万 USDT 入
		edge(vTarget, vIn, "USDT", "50000000000000000000000", 3),    // 少量出（5%）
	}
	report := detectProfitStructure(vTarget, flows)
	if !report.Detected || !strings.Contains(report.Kind, "holding") {
		t.Fatalf("应检测到沉淀: %+v", report)
	}
	if report.EstimateNote == "" {
		t.Fatal("应标注估算口径")
	}
}

func TestDetectProfitStructureTrade(t *testing.T) {
	flows := []FundEdge{
		edge(vIn, vTarget, "SHIB", "1000000000000000000000", 1), // 买入
		edge(vTarget, vOut, "SHIB", "900000000000000000000", 2), // 卖出 90%
	}
	report := detectProfitStructure(vTarget, flows)
	if !report.Detected || !strings.Contains(report.Kind, "profit") {
		t.Fatalf("应检测到买卖对账结构: %+v", report)
	}
}

func TestDetectProfitStructureNone(t *testing.T) {
	flows := []FundEdge{
		edge(vIn, vTarget, "USDT", "1000000000000000000000", 1),
		edge(vTarget, vOut, "USDT", "900000000000000000000", 2), // 流出 90%，非沉淀
	}
	report := detectProfitStructure(vTarget, flows)
	if report.Detected {
		t.Fatalf("不应误报结构: %+v", report)
	}
	if report.Summary == "" {
		t.Fatal("应返回摘要")
	}
}

func TestExecuteTokenAnalysis(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(vTarget, []FundEdge{
		edge(vIn, vTarget, "USDT", "1000000000000000000000", 1),
		edge(vIn, vTarget, "USDT", "2000000000000000000000", 2),
		edge(vOut, vTarget, "BNB", "1000000000000000000", 3),
		edge(vTarget, vOut, "USDT", "500000000000000000000", 4),
	})
	snap := agentSnapshot{flowSource: src}
	result, err := executeTokenAnalysis(context.Background(), snap, vTarget)
	if err != nil {
		t.Fatalf("Token 分析失败: %v", err)
	}
	if !strings.Contains(result, "2 种 Token") || !strings.Contains(result, "USDT") {
		t.Fatalf("Token 聚合结果异常: %s", result)
	}
}

func TestExecuteDirectionTrace(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(vTarget, []FundEdge{
		edge(vIn, vTarget, "USDT", "1000000000000000000000", 1), // 入
		edge(vTarget, vOut, "USDT", "500000000000000000000", 2), // 出
	})
	cfg := DefaultConfig()
	ranker := DefaultPathRanker()
	tracer := NewFundTracer(src, ranker, cfg)
	snap := agentSnapshot{tracer: tracer}
	plan := &InvestigationPlan{MaxHops: 2, BeamWidth: 4}

	// 正向（去向）应包含 vOut
	out, err := executeDirectionTrace(context.Background(), snap, vTarget, plan, &roundState{newPaths: nil}, true)
	if err != nil {
		t.Fatalf("正向追踪失败: %v", err)
	}
	if !strings.Contains(out, "正向") {
		t.Fatalf("结果应标注方向: %s", out)
	}
	// 反向（来源）应包含 vIn
	_, err = executeDirectionTrace(context.Background(), snap, vTarget, plan, &roundState{newPaths: nil}, false)
	if err != nil {
		t.Fatalf("反向追踪失败: %v", err)
	}
}

func TestExecuteExchangeDetection(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(vTarget, []FundEdge{
		edge("0x00000000000000000000000000000000000000d4", vTarget, "USDT", "1000000000000000000000", 1),
	})
	agent := newTestAgent()
	agent.flowSource = src
	snap := agentSnapshot{flowSource: src}
	st := &roundState{}
	inv := &Investigation{Target: vTarget}
	result, err := executeExchangeDetection(context.Background(), agent, snap, vTarget, inv, st)
	if err != nil {
		t.Fatalf("交易所检测失败: %v", err)
	}
	if result == "" {
		t.Fatal("应返回结果摘要")
	}
}

func TestExecuteBalanceAndIdentity(t *testing.T) {
	agent := newTestAgent()
	// Balance：无 svc → skipped
	_, err := executeBalanceAnalysis(context.Background(), agent, vTarget)
	if err == nil {
		t.Fatal("无数据源应返回错误（skipped）")
	}
	var skip *skipError
	if !errorsAs(err, &skip) {
		t.Fatalf("应为 skipError, got %v", err)
	}
	// Identity：无实体 → 无标签
	inv := &Investigation{Target: vTarget}
	result, err := executeIdentityLookup(context.Background(), agent, inv)
	if err != nil {
		t.Fatalf("身份查找失败: %v", err)
	}
	if result == "" {
		t.Fatal("应返回结果")
	}
	// Cluster：有实体
	inv.Entities = []EntityInfo{
		{Address: vIn, Entity: "exchange", Label: "Binance"},
		{Address: vOut, Entity: "wallet"},
	}
	result, err = executeEntityCluster(context.Background(), agent, inv)
	if err != nil || !strings.Contains(result, "exchange") {
		t.Fatalf("聚类结果异常: %s err=%v", result, err)
	}
}

func errorsAs(err error, target interface{}) bool {
	for err != nil {
		if e, ok := err.(*skipError); ok {
			*(target.(**skipError)) = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ── 动态调整（设计 §8）──

func TestDynamicAppendProfitTrace(t *testing.T) {
	e := &LoopEngine{}
	q := NewTaskQueue()
	inv := &Investigation{Target: vTarget, Profit: &ProfitReport{Detected: true, Kind: "profit", Summary: "检测到买卖结构"}}
	e.dynamicAppend(q, inv, &roundState{}, 1, 0) // maxTasks=0 不限
	snap := q.Snapshot()
	if len(snap) != 1 || snap[0].Type != TaskBackwardTrace {
		t.Fatalf("应追加 BACKWARD_TRACE, got %+v", snap)
	}
	// 幂等：再次调用不重复追加
	e.dynamicAppend(q, inv, &roundState{}, 1, 0)
	if len(q.Snapshot()) != 1 {
		t.Fatalf("重复调用不应重复追加: %+v", q.Snapshot())
	}
	// 预算：maxTasks 达到后不再追加
	q2 := NewTaskQueue()
	q2.Enqueue(InvestigationTask{Type: TaskAddressProfile})
	q2.Enqueue(InvestigationTask{Type: TaskFlowAnalysis})
	q2.Enqueue(InvestigationTask{Type: TaskPathTrace})
	q2.Enqueue(InvestigationTask{Type: TaskEntityCheck})
	q2.Enqueue(InvestigationTask{Type: TaskRiskScan}) // 5 个任务，预算 5
	e.dynamicAppend(q2, inv, &roundState{}, 1, 5)
	if len(q2.Snapshot()) != 5 {
		t.Fatalf("预算内不应追加: %+v", q2.Snapshot())
	}
}

func TestDynamicAppendExchangeIdentity(t *testing.T) {
	e := &LoopEngine{}
	q := NewTaskQueue()
	inv := &Investigation{Target: vTarget}
	st := &roundState{newEntities: []EntityInfo{{Address: vIn, Entity: "exchange", Label: "Binance"}}}
	e.dynamicAppend(q, inv, st, 1, 0)
	snap := q.Snapshot()
	if len(snap) != 1 || snap[0].Type != TaskIdentityLookup {
		t.Fatalf("应追加 IDENTITY_LOOKUP, got %+v", snap)
	}
	// 无信号不追加
	q2 := NewTaskQueue()
	e.dynamicAppend(q2, &Investigation{Target: vTarget}, &roundState{}, 1, 0)
	if len(q2.Snapshot()) != 0 {
		t.Fatalf("无信号不应追加: %+v", q2.Snapshot())
	}
}

func TestExecuteExchangeDetectionTruncation(t *testing.T) {
	src := NewFakeFlowSource()
	// 256 个不同对手方（超过 Top-200 上限；%02x 定长 2 位 hex 保证 42 字符地址）
	var edges []FundEdge
	for i := 0; i < 256; i++ {
		cp := "0x" + strings.Repeat("0", 38) + fmt.Sprintf("%02x", i)
		edges = append(edges, edge(cp, vTarget, "USDT", "1000000", int64(i)))
	}
	src.SetFlows(vTarget, edges)
	agent := newTestAgent()
	agent.flowSource = src
	snap := agentSnapshot{flowSource: src}
	st := &roundState{}
	inv := &Investigation{Target: vTarget}
	result, err := executeExchangeDetection(context.Background(), agent, snap, vTarget, inv, st)
	if err != nil {
		t.Fatalf("交易所检测失败: %v", err)
	}
	if !strings.Contains(result, "对手方 200 个") {
		t.Fatalf("应截断为 200 个对手方: %s", result)
	}
}

func TestDetectProfitStructureV21EstimateAndConfidence(t *testing.T) {
	// 沉淀场景：稳定币净流入 → 估算金额 + 高可信度 + 依据明细
	base := int64(1700000000)
	flows := []FundEdge{
		edge(vIn, vTarget, "USDT", "3000000000000000000000000", base),        // 300 万 USDT 入
		edge(vIn, vTarget, "USDT", "2000000000000000000000000", base+86400),  // 200 万 USDT 入
		edge(vTarget, vOut, "USDT", "100000000000000000000000", base+100000), // 少量出（3.3%）
	}
	report := detectProfitStructure(vTarget, flows)
	if !report.Detected || !strings.Contains(report.Kind, "holding") {
		t.Fatalf("应检测到沉淀: %+v", report)
	}
	// 估算 = 稳定币净额（300万+200万-10万 = 490万）
	if report.EstimateUSD < 4800000000000000000000000 || report.EstimateUSD > 5000000000000000000000000 {
		t.Fatalf("估算金额异常: %v", report.EstimateUSD)
	}
	if report.Confidence < 0.7 || report.Confidence > 0.85 {
		t.Fatalf("置信度应在 0.7-0.85（无 oracle 封顶）, got %v", report.Confidence)
	}
	if len(report.Checklist) < 4 {
		t.Fatalf("依据明细应 ≥4 项, got %d", len(report.Checklist))
	}
	foundMissing := false
	for _, c := range report.Checklist {
		if !c.Present && strings.Contains(c.Label, "历史价格") {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatal("应包含'缺少历史价格'依据项（?）")
	}
}

func TestDetectProfitStructureV21TimeWindow(t *testing.T) {
	// 买卖时间远离（>30 天）→ 时间窗口不匹配，置信度较低
	base := int64(1700000000)
	flows := []FundEdge{
		edge(vIn, vTarget, "SHIB", "1000000000000000000000", base),          // 买入
		edge(vTarget, vOut, "SHIB", "900000000000000000000", base+40*86400), // 40 天后卖出
	}
	report := detectProfitStructure(vTarget, flows)
	if !report.Detected {
		t.Fatalf("应检测到买卖结构: %+v", report)
	}
	timeMatched := false
	for _, c := range report.Checklist {
		if strings.Contains(c.Label, "时间窗口") {
			timeMatched = c.OK
		}
	}
	if timeMatched {
		t.Fatal("40 天间隔不应匹配时间窗口")
	}
	if report.Confidence > 0.85 {
		t.Fatalf("置信度不应超过封顶: %v", report.Confidence)
	}
}
