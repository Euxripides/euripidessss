package cloudplanner

import (
	"testing"
	"time"
)

func input(rows uint64, bytes uint64, runtime float64, ranges int, dataset string) ProbeInput {
	return ProbeInput{
		EstimatedRows:           rows,
		EstimatedBytes:          bytes,
		EstimatedRuntimeSeconds: runtime,
		RangeCount:              ranges,
		Dataset:                 dataset,
	}
}

// Case 1：100K 行小任务 → Cloud S。
func TestPlanCase1SmallGoesS(t *testing.T) {
	p := NewPlanner(BudgetGuard{})
	plan := p.Plan(input(100_000, 30<<20, 5*60, 1, "transactions"))
	if plan.Tier != CloudS {
		t.Fatalf("100K 行应 Cloud S，实际 %s（score=%.1f）", plan.Tier, plan.Score)
	}
	if plan.CPU != 4 || plan.MaxWorkers != 8 {
		t.Fatalf("S 档规格不符: %+v", plan)
	}
}

// Case 2 前半：3M 行 → Cloud L。
func TestPlanCase2MediumGoesL(t *testing.T) {
	p := NewPlanner(BudgetGuard{})
	plan := p.Plan(input(3_000_000, 2<<30, 20*60, 10, "token_transfers"))
	if plan.Tier != CloudL {
		t.Fatalf("3M 行应 Cloud L，实际 %s（score=%.1f）", plan.Tier, plan.Score)
	}
}

// 超大任务 → 强制 XL。
func TestPlanForceXL(t *testing.T) {
	p := NewPlanner(BudgetGuard{})
	plan := p.Plan(input(21_000_000, 25<<30, 3*3600, 100, "logs"))
	if plan.Tier != CloudXL {
		t.Fatalf("21M Logs 应强制 XL，实际 %s", plan.Tier)
	}
	if plan.CPU != 32 || plan.MemoryGB != 64 || plan.MaxWorkers != 2 {
		t.Fatalf("XL 规格不符: %+v", plan)
	}
	if plan.EstimatedCost <= 0 {
		t.Fatal("缺少成本估算")
	}
}

// 直接 XL 候选：5M 行。
func TestPlanDirectXL(t *testing.T) {
	p := NewPlanner(BudgetGuard{})
	plan := p.Plan(input(5_000_000, 1<<30, 20*60, 8, "transactions"))
	if plan.Tier != CloudXL {
		t.Fatalf("5M 行应直接 XL，实际 %s", plan.Tier)
	}
}

// Case 2 后半：L 运行中吞吐下降 → 自动升级 XL。
func TestReevaluateUpgradeLToXL(t *testing.T) {
	p := NewPlanner(BudgetGuard{})
	current := ResourcePlan{Tier: CloudL, EstimatedRuntime: 20 * time.Minute}
	current.applyTierSpecs()
	m := RuntimeMetrics{
		RowsPerSecond:            100,
		CompletedPercent:         0.1,
		ETA:                      95 * time.Minute,
		OriginalEstimatedRuntime: 20 * time.Minute,
	}
	next := p.Reevaluate(current, m)
	if next.Tier != CloudXL {
		t.Fatalf("ETA 95min > 原始 2 倍，应升级 XL，实际 %s", next.Tier)
	}
	// OOM 直接升级
	next = p.Reevaluate(current, RuntimeMetrics{OOMCount: 1})
	if next.Tier != CloudXL {
		t.Fatal("OOM 应升级 XL")
	}
	// 内存 >85% 升级
	next = p.Reevaluate(current, RuntimeMetrics{MemoryUsage: 0.9})
	if next.Tier != CloudXL {
		t.Fatal("内存 90% 应升级 XL")
	}
}

// Case 5：XL 主阶段完成，剩余小 Gap → 降级 L。
func TestReevaluateDowngradeXLToL(t *testing.T) {
	p := NewPlanner(BudgetGuard{})
	current := ResourcePlan{Tier: CloudXL}
	current.applyTierSpecs()
	next := p.Reevaluate(current, RuntimeMetrics{CompletedPercent: 0.97, ETA: 2 * time.Minute})
	if next.Tier != CloudL {
		t.Fatalf("主阶段完成应降级 L，实际 %s", next.Tier)
	}
	// 未完成时不降级
	next = p.Reevaluate(current, RuntimeMetrics{CompletedPercent: 0.5, ETA: 30 * time.Minute})
	if next.Tier != CloudXL {
		t.Fatalf("运行中不应降级，实际 %s", next.Tier)
	}
}

// 预算守卫：单任务成本超限时 XL 降为 L。
func TestBudgetMaxSingleJobDowngrade(t *testing.T) {
	p := NewPlanner(BudgetGuard{Enabled: true, MaxSingleJobCost: 1})
	plan := p.Plan(input(21_000_000, 25<<30, 3*3600, 100, "logs"))
	if plan.Tier != CloudL {
		t.Fatalf("预算超限应降为 L，实际 %s（cost=¥%.2f）", plan.Tier, plan.EstimatedCost)
	}
}

// 预算守卫：日预算超限拒绝。
func TestBudgetAllowDaily(t *testing.T) {
	guard := BudgetGuard{Enabled: true, DailyBudget: 10}
	plan := ResourcePlan{Tier: CloudXL, EstimatedCost: 8}
	if ok, _ := guard.Allow(plan, 5, 0, 0); ok {
		t.Fatal("日预算 5+8>10 应拒绝")
	}
	if ok, _ := guard.Allow(plan, 1, 0, 0); !ok {
		t.Fatal("日预算 1+8<=10 应通过")
	}
	guard2 := BudgetGuard{Enabled: true, MaxXLWorkers: 1}
	if ok, _ := guard2.Allow(plan, 0, 0, 1); ok {
		t.Fatal("XL 并发超限应拒绝")
	}
	guard3 := BudgetGuard{Enabled: true, MaxSingleJobCost: 5}
	if ok, _ := guard3.Allow(plan, 0, 0, 0); ok {
		t.Fatal("单任务成本 8>5 应拒绝")
	}
}

// Case 3/4：崩溃恢复与 OOM 恢复依赖 Checkpoint V3（集成层测试），
// 这里验证 Reevaluate 对 OOM 的升级决策（Case 4 前半）。
func TestReevaluateOOM(t *testing.T) {
	p := NewPlanner(BudgetGuard{})
	current := ResourcePlan{Tier: CloudL}
	current.applyTierSpecs()
	next := p.Reevaluate(current, RuntimeMetrics{OOMCount: 1, CompletedPercent: 0.4})
	if next.Tier != CloudXL {
		t.Fatalf("L 运行中 OOM 应升级 XL，实际 %s", next.Tier)
	}
}
