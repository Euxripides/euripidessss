package intelligence

import (
	"testing"
)

func planFor(mode InvestigationMode, direction string) *InvestigationPlan {
	p := NewPlanner(DefaultConfig())
	return p.Plan(PlanInput{
		Target: testAddress,
		Intent: &InvestigationIntent{Mode: mode, Direction: direction, Goals: []string{GoalFundDestination}},
	})
}

func TestPlannerModeRouting(t *testing.T) {
	cases := []struct {
		name      string
		mode      InvestigationMode
		dir       string
		wantFirst string // 第一个任务类型
		wantHas   []string
		wantNot   []string
	}{
		{"fund_trace_out", ModeFundTrace, "out", TaskAddressProfile, []string{TaskForwardTrace, TaskExchangeDetect, TaskFlowGraph}, []string{TaskBackwardTrace}},
		{"fund_trace_in", ModeFundTrace, "in", TaskAddressProfile, []string{TaskBackwardTrace, TaskExchangeDetect, TaskFlowGraph}, []string{TaskForwardTrace}},
		{"fund_trace_both", ModeFundTrace, "both", TaskAddressProfile, []string{TaskBackwardTrace, TaskForwardTrace}, nil},
		{"profit", ModeProfitAnalyze, "unknown", TaskAddressProfile, []string{TaskProfitDetection, TaskTokenAnalysis, TaskBalanceAnalysis, TaskEntityCluster}, nil},
		{"exchange", ModeExchangeEntry, "unknown", TaskAddressProfile, []string{TaskExchangeDetect, TaskIdentityLookup}, nil},
		{"identity", ModeIdentityLookup, "unknown", TaskAddressProfile, []string{TaskIdentityLookup, TaskEntityCluster}, nil},
		{"risk", ModeRiskScan, "unknown", TaskAddressProfile, []string{TaskRiskAnalysis, TaskProfitDetection}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan := planFor(c.mode, c.dir)
			if len(plan.Tasks) == 0 {
				t.Fatal("计划应生成任务")
			}
			if plan.Tasks[0].Type != c.wantFirst {
				t.Fatalf("首个任务 = %s, want %s", plan.Tasks[0].Type, c.wantFirst)
			}
			got := map[string]bool{}
			for _, tk := range plan.Tasks {
				got[tk.Type] = true
			}
			for _, w := range c.wantHas {
				if !got[w] {
					t.Fatalf("计划应包含 %s, got %v", w, planTypes(plan))
				}
			}
			for _, w := range c.wantNot {
				if got[w] {
					t.Fatalf("计划不应包含 %s, got %v", w, planTypes(plan))
				}
			}
			if plan.Mode != c.mode {
				t.Fatalf("计划模式 = %s, want %s", plan.Mode, c.mode)
			}
		})
	}
}

func planTypes(plan *InvestigationPlan) []string {
	var out []string
	for _, tk := range plan.Tasks {
		out = append(out, tk.Type)
	}
	return out
}

func TestPlannerEstimatedMinutesAndPriority(t *testing.T) {
	plan := planFor(ModeFundTrace, "both")
	wantMin := maxInt(3, len(plan.Tasks)*2)
	if plan.EstimatedMinutes != wantMin {
		t.Fatalf("预计时长 = %d, want %d（%d 个任务 ×2）", plan.EstimatedMinutes, wantMin, len(plan.Tasks))
	}
	if plan.Tasks[0].Priority != 1 || plan.Tasks[1].Priority != 1 {
		t.Fatalf("前 2 个任务应为 P1: %d %d", plan.Tasks[0].Priority, plan.Tasks[1].Priority)
	}
}

func TestPlannerLegacyFallback(t *testing.T) {
	p := NewPlanner(DefaultConfig())
	// 无意图 → 旧规则规划（目标/来源/高价值路径/实体）
	plan := p.Plan(PlanInput{Target: testAddress, InCount: 5, OutCount: 5, HasFlows: true})
	if len(plan.Tasks) == 0 {
		t.Fatal("旧规则应生成任务")
	}
	found := false
	for _, tk := range plan.Tasks {
		if tk.Type == "FUND_SOURCE" {
			found = true
		}
	}
	if !found {
		t.Fatalf("旧规则应包含 FUND_SOURCE 任务: %v", planTypes(plan))
	}
}

func TestNormalizeTaskType(t *testing.T) {
	cases := map[string]string{
		"FUND_SOURCE":      TaskBackwardTrace,
		"FUND_FLOW":        TaskForwardTrace,
		"HIGH_VALUE_PATH":  TaskPathTrace,
		"ENTITY_RELATION":  TaskEntityCheck,
		"RISK_CHECK":       TaskRiskScan,
		"RISK_ANALYSIS":    TaskRiskAnalysis,
		"REPORT_GENERATE":  TaskReportGenerate,
		TaskAddressProfile: TaskAddressProfile, // 原值保留
	}
	for in, want := range cases {
		if got := normalizeTaskType(in); got != want {
			t.Fatalf("normalizeTaskType(%s) = %s, want %s", in, got, want)
		}
	}
}
