package intelligence

import (
	"context"
	"math/big"
	"strings"
)

// ── Re-plan 触发器（V2 设计 §9）──
//
// 触发条件（结果合并阶段评估）：
//  1. 高价值资金发现（路径金额超过阈值）
//  2. 新实体发现（未识别的实体类型）
//  3. 新资金路径发现（本轮新增路径）
//
// 流程：Result → Planner 增量规划 → 新任务 → TaskQueue 去重合并。
// 与 dynamicAppend（规则型追加）互补：Re-plan 是事件型通道，共用 MaxTasks 预算。

// ReplanReason 是 Re-plan 触发原因。
type ReplanReason string

const (
	ReplanHighValue ReplanReason = "HIGH_VALUE_FUND" // 高价值资金发现
	ReplanNewEntity ReplanReason = "NEW_ENTITY"      // 新实体发现
	ReplanNewPath   ReplanReason = "NEW_PATH"        // 新资金路径发现
)

// ReplanSignal 是一次 Re-plan 触发的信号记录。
type ReplanSignal struct {
	Reason   ReplanReason `json:"reason"`
	Detail   string       `json:"detail"`
	Round    int          `json:"round"`
	NewTasks int          `json:"new_tasks"` // 实际入队的新任务数
}

// highValueThreshold 是触发 Re-plan 的路径金额阈值：
// 100 万 USDT（1e24 wei，18 位小数 Token；与路径排名 log10 尺度对齐）。
var highValueThreshold = new(big.Int).Exp(big.NewInt(10), big.NewInt(24), nil)

// amountAboveThreshold 精确比较链上金额（十进制或 0x 十六进制）是否达到阈值。
func amountAboveThreshold(amount string, threshold *big.Int) bool {
	amount = strings.TrimSpace(amount)
	if amount == "" || threshold == nil {
		return false
	}
	base := 10
	digits := amount
	if strings.HasPrefix(amount, "0x") || strings.HasPrefix(amount, "0X") {
		base = 16
		digits = amount[2:]
	}
	n, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return false
	}
	return n.Cmp(threshold) >= 0
}

// evaluateReplan 在结果合并阶段评估是否触发增量规划（设计 §9）：
// 高价值资金 / 新实体 / 新路径任一命中则调用 planner 生成增量任务，
// 经 TaskQueue 去重合并（同轮同类型同目标已存在则跳过）。
// 返回触发的信号列表（供日志/API 展示）。
func (e *LoopEngine) evaluateReplan(ctx context.Context, a *InvestigationAgent, snap agentSnapshot,
	queue *TaskQueue, inv *Investigation, st *roundState, plan *InvestigationPlan, round, maxTasks int) []ReplanSignal {
	if snap.planner == nil || inv == nil || queue == nil {
		return nil
	}
	var signals []ReplanSignal

	// 1. 高价值资金发现（本轮新增路径中金额 ≥ 100 万 USDT 阈值，big.Int 精确比较）
	highValue := false
	var highDetail []string
	for _, p := range st.newPaths {
		for _, edge := range p.Edges {
			if amountAboveThreshold(edge.Amount, highValueThreshold) {
				highValue = true
				highDetail = append(highDetail, shortAddr(edge.To))
				break
			}
		}
	}
	if highValue {
		signals = append(signals, ReplanSignal{Reason: ReplanHighValue, Detail: "发现高价值资金路径: " + strings.Join(highDetail, ","), Round: round})
	}

	// 2. 新实体发现（本轮新增实体）
	if len(st.newEntities) > 0 {
		var kinds []string
		for _, ent := range st.newEntities {
			kinds = append(kinds, ent.Entity)
		}
		signals = append(signals, ReplanSignal{Reason: ReplanNewEntity, Detail: "新实体: " + strings.Join(kinds, ","), Round: round})
	}

	// 3. 新资金路径发现（本轮新增路径数量）
	if len(st.newPaths) > 0 {
		signals = append(signals, ReplanSignal{Reason: ReplanNewPath, Detail: "新增路径 " + itoa(len(st.newPaths)) + " 条", Round: round})
	}

	if len(signals) == 0 {
		return nil
	}

	// 增量规划：以当前调查上下文生成新任务（planner.Plan 幂等；AI 规划仅首轮）
	// 后续轮次使用规则规划器（snap.planner），避免 AI 重复调用消耗预算。
	replanInput := a.planInput(ctx, inv.Target)
	replanInput.HasFlows = len(inv.Paths) > 0
	if inv.Request != nil && inv.Request.Intent != nil {
		replanInput.Intent = inv.Request.Intent
	}
	replan := snap.planner.Plan(replanInput)

	// 新任务去重合并（同轮同类型同目标幂等）
	appended := 0
	for _, pt := range replan.Tasks {
		if maxTasks > 0 && queue.TotalCount() >= maxTasks {
			break // 预算限制：不再追加（防无限扩展）
		}
		task := InvestigationTask{
			Type:        normalizeTaskType(pt.Type),
			Description: pt.Description,
			Priority:    pt.Priority,
			Target:      inv.Target,
			Round:       round,
		}
		// 幂等：同轮次同类型同目标已存在则跳过
		dup := false
		for _, existing := range queue.Snapshot() {
			if existing.Round == round && existing.Type == task.Type && existing.Target == task.Target {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		queue.Enqueue(task)
		appended++
	}
	for i := range signals {
		signals[i].NewTasks = appended
	}
	return signals
}
