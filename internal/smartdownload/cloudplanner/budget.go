package cloudplanner

import "fmt"

// BudgetGuard Cloud 预算守卫（设计 §22）。
type BudgetGuard struct {
	Enabled          bool
	DailyBudget      float64 // 每日 Cloud 成本上限（¥）
	MonthlyBudget    float64 // 每月上限（¥）
	MaxXLWorkers     int     // XL 并发上限
	MaxSingleJobCost float64 // 单任务成本上限（¥）
}

// Allow 预算准入：超限时返回拒绝原因；未启用恒通过。
func (b BudgetGuard) Allow(plan ResourcePlan, todayUsed, monthUsed float64, xlActive int) (bool, string) {
	if !b.Enabled {
		return true, ""
	}
	if b.MonthlyBudget > 0 && monthUsed+plan.EstimatedCost > b.MonthlyBudget {
		return false, fmt.Sprintf("月度 Cloud 预算超限（预计 +¥%.2f）", plan.EstimatedCost)
	}
	if b.DailyBudget > 0 && todayUsed+plan.EstimatedCost > b.DailyBudget {
		return false, fmt.Sprintf("每日 Cloud 预算超限（预计 +¥%.2f）", plan.EstimatedCost)
	}
	if b.MaxXLWorkers > 0 && plan.Tier == CloudXL && xlActive >= b.MaxXLWorkers {
		return false, "XL Worker 并发已达上限"
	}
	return true, ""
}
