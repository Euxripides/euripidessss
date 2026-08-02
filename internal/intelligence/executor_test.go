package intelligence

import (
	"context"
	"strings"
	"testing"
)

// ── Executor Registry 测试（V2 设计 §6/§7）──

func TestDefaultExecutorsRegistered(t *testing.T) {
	reg := defaultExecutors()
	// 12 种任务类型应全部注册（含别名：RISK_ANALYSIS / REPORT_GENERATE / GENERATE_REPORT）
	expected := []string{
		TaskAddressProfile, TaskBalanceAnalysis, TaskTokenAnalysis, TaskProfitDetection,
		TaskForwardTrace, TaskBackwardTrace, TaskFlowGraph, TaskExchangeDetect,
		TaskEntityCluster, TaskRiskAnalysis, TaskIdentityLookup, TaskReportGenerate,
		TaskFlowAnalysis, TaskPathTrace, TaskEntityCheck, TaskRiskScan, TaskExpandAddress, TaskGenerateReport,
	}
	for _, typ := range expected {
		if _, ok := reg.Get(typ); !ok {
			t.Fatalf("任务类型 %s 未注册", typ)
		}
	}
	if len(reg.Types()) < 12 {
		t.Fatalf("注册表应含至少 12 种执行器, got %d", len(reg.Types()))
	}
}

func TestExecutorRegistryRegisterOverride(t *testing.T) {
	reg := NewExecutorRegistry()
	mk := func(typ, summary string) Executor {
		return &executorFunc{
			taskType: typ,
			execute: func(ctx context.Context, a *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, plan *InvestigationPlan, inv *Investigation, st *roundState) (ExecutorResult, error) {
				return ExecutorResult{Status: "SUCCESS", Summary: summary}, nil
			},
		}
	}
	reg.Register(mk("A", "v1"))
	reg.Register(mk("A", "v2"))
	e, ok := reg.Get("A")
	if !ok {
		t.Fatal("A 应已注册")
	}
	if e.Type() != "A" {
		t.Fatalf("Type = %s", e.Type())
	}
	// 重复注册以最后为准：执行应返回 v2
	res, err := e.Execute(context.Background(), nil, agentSnapshot{}, &InvestigationTask{Type: "A"}, nil, nil, nil)
	if err != nil || res.Summary != "v2" {
		t.Fatalf("重复注册应以最后为准, got %+v err=%v", res, err)
	}
	// 空类型注册被忽略
	reg.Register(&executorFunc{taskType: ""})
	if _, ok := reg.Get(""); ok {
		t.Fatal("空类型不应注册")
	}
}

func TestLoopEngineDispatchUnknownType(t *testing.T) {
	e := NewLoopEngine()
	_, err := e.executeTask(context.Background(), nil, agentSnapshot{}, &InvestigationTask{Type: "NO_SUCH_TASK"}, nil, nil, nil, DefaultConfig())
	if err == nil || !strings.Contains(err.Error(), "未知任务类型") {
		t.Fatalf("未知类型应报错, got %v", err)
	}
}

func TestLoopEngineDispatchValidateSkip(t *testing.T) {
	// 无数据源：ADDRESS_PROFILE 应因 Validate 失败返回 errSkipped（降级语义）
	e := NewLoopEngine()
	agent := newTestAgent()
	agent.svc = nil // 强制无画像数据源
	_, err := e.executeTask(context.Background(), agent, agentSnapshot{}, &InvestigationTask{Type: TaskAddressProfile, Target: addrA}, nil, &Investigation{ID: "inv-1", Target: addrA}, &roundState{}, DefaultConfig())
	if err == nil {
		t.Fatal("无数据源应返回跳过错误")
	}
	var skip *skipError
	if !errorsAs(err, &skip) {
		t.Fatalf("应为 skipError, got %T", err)
	}
}

func TestLoopEngineDispatchSuccess(t *testing.T) {
	// 有 fake 数据源：TOKEN_ANALYSIS 应成功返回摘要（不依赖 svc）
	agent := newTestAgent()
	e := NewLoopEngine()
	snap := agentSnapshot{flowSource: agent.flowSource, tracer: agent.tracer}
	summary, err := e.executeTask(context.Background(), agent, snap, &InvestigationTask{Type: TaskTokenAnalysis, Target: addrA}, &InvestigationPlan{MaxHops: 2, BeamWidth: 4}, &Investigation{ID: "inv-1", Target: addrA}, &roundState{}, DefaultConfig())
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if summary == "" {
		t.Fatal("成功执行应返回摘要")
	}
}

// errorsAs 已有定义于 v2_tasks_test.go，此处复用。
