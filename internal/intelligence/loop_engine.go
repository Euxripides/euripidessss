package intelligence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/logger"
)

// ── Investigation Loop（设计 §5/§16）──
//
// 核心：生成计划 → 执行任务 → 收集结果（观察）→ 分析结果 → 决定下一步，
// 持续循环直到满足完成条件（§18：自动多轮调查 / 自动扩展高价值地址 /
// 自动停止无价值路径 / 全过程可追踪）。

// Expander 提供地址扩展能力（ExpansionEngine 实现；测试可注入 fake）。
type Expander interface {
	Expand(ctx context.Context, target string, maxAddresses int) ([]ExpansionResult, error)
}

// skipError 表示任务因缺少依赖被跳过（非失败）。
type skipError struct{ reason string }

func (e *skipError) Error() string { return e.reason }

// errSkipped 构造跳过错误。
func errSkipped(reason string) error { return &skipError{reason: reason} }

// LoopEngine 执行多轮调查闭环。无状态（依赖全部经参数传入）。
type LoopEngine struct {
	executors *ExecutorRegistry // 执行器注册表（懒加载，设计 §6/§7）
}

// NewLoopEngine 创建闭环引擎。
func NewLoopEngine() *LoopEngine { return &LoopEngine{} }

// registry 返回执行器注册表（首次调用时构建）。
func (e *LoopEngine) registry() *ExecutorRegistry {
	if e.executors == nil {
		e.executors = defaultExecutors()
	}
	return e.executors
}

// roundState 是单轮执行中累积的状态。
type roundState struct {
	focus         []string
	flowsByAddr   map[string][]FundEdge
	newPaths      []FundPath
	newCandidates []ExpansionResult
	newEntities   []EntityInfo
	newPatterns   []RiskPattern
}

// Run 执行完整调查闭环：
// PLANNING → RUNNING(任务队列) → ANALYZING(观察/评分) → EXPANDING(决策) → …循环…
// → VERIFYING(固化记忆) → REPORTING(报告) → COMPLETED。
func (e *LoopEngine) Run(ctx context.Context, a *InvestigationAgent, inv *Investigation, snap agentSnapshot) error {
	cfg := a.Config()
	if inv.cfgOverride != nil {
		cfg = *inv.cfgOverride // 仅本调查生效的启动配置覆盖
	}
	maxRounds := cfg.MaxRounds
	if maxRounds < 1 {
		maxRounds = 3
	}
	started := time.Now().UTC()
	queue := NewTaskQueue()
	obsEngine := NewObservationEngine()
	decision := NewDecisionEngine(cfg)
	scorer := NewInvestigationScorer() // V2 六维评分（设计 §9）
	if a.profile != nil {
		scorer.SetProfileStore(a.profile) // V1 Storage Layer：持久化评分权重
	}
	// 扩展候选跨轮累积（未使用的候选在后续轮次仍可被决策选中）
	var candidates []ExpansionResult

	// 1. 规划（AI 优先，规则回退；后续轮次由决策确定新目标 = 增量规划）
	a.setStage(inv, InvestigationPlanning, "制定调查计划", 5)
	// Runtime V2 安全（should-fix 修复）：规划前立即检查让位/终态——若 setStage(Planning)
	// 被降级为 WAITING（resumeRun 接管执行权）或调查已终态（resumeRun 收尾完成），
	// 立即中止主循环，避免在 AI 规划期间与 resumeRun 并行写 active（last-writer-wins
	// 导致 Tasks/Strategy 快照丢失）或复活终态调查重复执行任务
	if isYielding(a.Controller(inv.ID).State()) {
		return nil
	}
	planInput := a.planInput(ctx, inv.Target)
	// V2：注入调查意图（objective/expected_result/mode 分析结果），驱动模式化任务序列
	if inv.Request != nil && inv.Request.Intent != nil {
		planInput.Intent = inv.Request.Intent
	}
	plan := snap.planner.Plan(planInput)
	if snap.ai != nil && cfg.UseAI {
		if aiPlan, strategy := snap.ai.Plan(ctx, inv, planInput); aiPlan != nil {
			plan = aiPlan
			if strategy != nil {
				a.setField(inv, func(i *Investigation) { i.Strategy = strategy })
			}
		}
	}
	a.setField(inv, func(i *Investigation) { i.Plan = plan })
	a.persistPlan(inv, plan) // V1 Storage Layer：计划落盘

	focus := []string{inv.Target}
	segment := 70.0 / float64(maxRounds)
	// 上一轮假设生成的验证任务（进入本轮队列，§7）；记录归属假设索引供状态门控
	type pendingVerify struct {
		task   InvestigationTask
		hypIdx int // 指向 inv.Hypotheses 的索引（-1 = 无归属）
	}
	var pendingTasks []pendingVerify

	for round := 1; round <= maxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Runtime V2 安全（MEDIUM 修复）：主循环被让位（调查进入 WAITING 等待
		// 恢复执行完成）或已终态（resumeRun 收尾完成）时中止主循环，执行权归
		// resumeRun，防双队列并发执行与终态复活
		if isYielding(a.Controller(inv.ID).State()) {
			a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
			return nil
		}
		roundStarted := time.Now().UTC()
		done := false
		a.setField(inv, func(i *Investigation) { i.Round = round })
		base := 10 + float64(round-1)*segment
		a.setStage(inv, InvestigationRunning, fmt.Sprintf("第 %d 轮：执行调查任务", round), base)

		// ── 构建本轮任务队列（含上一轮假设验证任务）──
		st := &roundState{
			focus:       focus,
			flowsByAddr: map[string][]FundEdge{},
		}
		for _, pv := range pendingTasks {
			// V2.1 预算：累计任务数达到上限不再入队（防动态任务无限扩张）
			if cfg.MaxTasks > 0 && queue.TotalCount() >= cfg.MaxTasks {
				break
			}
			t := queue.Enqueue(pv.task)
			if pv.hypIdx >= 0 {
				// 记录验证任务 ID（供假设状态门控：任务真实执行完毕才算 evaluated）
				a.setField(inv, func(i *Investigation) {
					if pv.hypIdx < len(i.Hypotheses) {
						h := i.Hypotheses[pv.hypIdx]
						h.TaskIDs = append(h.TaskIDs, t.ID)
						i.Hypotheses[pv.hypIdx] = h
					}
				})
			}
		}
		pendingTasks = nil
		for _, t := range e.buildQueue(round, plan, focus, snap, cfg) {
			// V2.1 预算：累计任务数达到上限不再入队
			if cfg.MaxTasks > 0 && queue.TotalCount() >= cfg.MaxTasks {
				break
			}
			queue.Enqueue(t)
		}
		a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
		a.persistTasks(inv.ID, queue.Snapshot()) // V1 Storage Layer：任务落盘

		// ── 执行任务 ──
		for {
			// Runtime V2 安全（should-fix 修复）：任务循环内轮内被让位
			// （setStage(Running) 降级 WAITING，resumeRun 接管）或调查已终态时
			// 立即中止，防主循环执行完本轮剩余任务与 resumeRun 双队列并发/终态复活
			if isYielding(a.Controller(inv.ID).State()) {
				a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
				return nil
			}
			t := queue.Next()
			if t == nil {
				// Runtime V2（设计 §5）：依赖失败/跳过的等待任务标记 skipped，
				// 避免调查完成时残留永久阻塞的 pending 任务
				blocked := queue.Snapshot()
				changed := false
				for i := range blocked {
					if blocked[i].Status == TaskPending && queue.BlockedByFailedDep(blocked[i].ID) {
						queue.Mark(blocked[i].ID, TaskSkipped, "依赖失败，任务跳过", "")
						changed = true
					}
				}
				if changed {
					a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
					a.persistTasks(inv.ID, queue.Snapshot()) // V1 Storage Layer：任务落盘
					continue                                 // 重新取任务（可能有依赖已满足的新任务）
				}
				break
			}
			a.eventLog.TaskCreated(inv.ID, t) // Runtime V2：任务创建事件
			queue.Mark(t.ID, TaskRunning, "", "")
			a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
			a.persistTasks(inv.ID, queue.Snapshot()) // V1 Storage Layer：任务落盘
			result, err := e.executeTask(ctx, a, snap, t, plan, inv, st, cfg)
			// ── Runtime V2（设计 §11）：运行期 heartbeat watchdog ──
			// 执行器返回后检查是否超过 TimeoutSec（以执行耗时计），超时视为失败（可重试）
			if err == nil && t.TimeoutSec > 0 && time.Since(time.Unix(t.StartedAt, 0)).Seconds() > float64(t.TimeoutSec) {
				markRes := queue.Mark(t.ID, TaskFailed, "", "执行超时（超过 "+itoa(t.TimeoutSec)+"s）")
				logger.Log.Warn().Str("inv", inv.ID).Str("task", t.Type).Msg("intelligence_task_timeout")
				if markRes != nil && markRes.Status == TaskPending {
					a.eventLog.TaskRetried(inv.ID, markRes, markRes.RetryCount)
				} else if markRes != nil {
					a.eventLog.TaskFailed(inv.ID, markRes, "执行超时")
				}
				a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
				a.persistTasks(inv.ID, queue.Snapshot()) // V1 Storage Layer：任务落盘
				continue
			}
			if err != nil {
				var skip *skipError
				if errors.As(err, &skip) {
					queue.Mark(t.ID, TaskSkipped, skip.reason, "")
				} else {
					markRes := queue.Mark(t.ID, TaskFailed, "", err.Error())
					logger.Log.Warn().Str("inv", inv.ID).Str("task", t.Type).Err(err).Msg("intelligence_task_failed")
					// Runtime V2：失败→重试判定（Mark 内部 RetryCount<MaxRetries 时回到 pending）
					if markRes != nil && markRes.Status == TaskPending {
						a.eventLog.TaskRetried(inv.ID, markRes, markRes.RetryCount)
					} else if markRes != nil {
						a.eventLog.TaskFailed(inv.ID, markRes, err.Error())
					}
				}
				a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
				a.persistTasks(inv.ID, queue.Snapshot()) // V1 Storage Layer：任务落盘
				continue                                 // 单任务失败不阻断调查
			}
			queue.Mark(t.ID, TaskDone, result, "")
			a.memories.RecordCompletedTask(inv.ID, t.ID)
			a.eventLog.TaskExecuted(inv.ID, t, result) // Runtime V2：执行成功事件
			a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
			a.persistTasks(inv.ID, queue.Snapshot()) // V1 Storage Layer：任务落盘
			// ── V2 动态调整（设计 §8）：任务结果触发追加任务（获利→来源追踪；交易所→身份线索）──
			e.dynamicAppend(queue, inv, st, round, cfg.MaxTasks)
		}

		// ── 合并本轮结果 ──
		mem, _ := a.memories.Get(inv.ID)
		merged := a.mergePaths(inv, st.newPaths, snap.ranker, cfg.TopPaths, mem)
		if len(st.newEntities) > 0 {
			a.setField(inv, func(i *Investigation) { i.Entities = append(i.Entities, st.newEntities...) })
		}
		if len(st.newPatterns) > 0 {
			a.setField(inv, func(i *Investigation) { i.Patterns = append(i.Patterns, st.newPatterns...) })
		}
		candidates = append(candidates, st.newCandidates...)
		if len(st.newCandidates) > 0 {
			a.setField(inv, func(i *Investigation) { i.Expansions = append(i.Expansions, st.newCandidates...) })
		}
		a.setField(inv, func(i *Investigation) { i.Paths = merged })

		// ── Runtime V2（设计 §9）：Re-plan 触发器（高价值资金/新实体/新路径 → 增量规划合并）──
		if signals := e.evaluateReplan(ctx, a, snap, queue, inv, st, plan, round, cfg.MaxTasks); len(signals) > 0 {
			a.setField(inv, func(i *Investigation) { i.Replans = append(i.Replans, signals...) })
			a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
			a.persistTasks(inv.ID, queue.Snapshot()) // V1 Storage Layer：Re-plan 新任务落盘
			for _, s := range signals {
				a.eventLog.Replanned(inv.ID, s.Reason, s.Round, s.NewTasks) // Runtime V2：事件日志
				logger.Log.Info().Str("inv", inv.ID).Int("round", s.Round).
					Str("reason", string(s.Reason)).Int("new_tasks", s.NewTasks).
					Msg("intelligence_replanned")
			}
		}

		// ── 观察（§8：新地址/新路径/新交易/风险事件）──
		a.setStage(inv, InvestigationAnalyzing, fmt.Sprintf("第 %d 轮：观察与评分", round), base+8)
		newObs := e.collectObservations(obsEngine, round, st, mem)
		if len(newObs) > 0 {
			a.setField(inv, func(i *Investigation) { i.Observations = append(i.Observations, newObs...) })
			for _, o := range newObs {
				// 扩展候选仅作观察展示，不记入已分析地址（否则决策会将其判为重复关系）
				if o.Type == ObsNewAddress && !strings.Contains(o.Source, TaskExpandAddress) {
					a.memories.RecordDiscovered(inv.ID, o.Address)
				}
			}
		}
		mem, _ = a.memories.Get(inv.ID)

		// ── V2.1 Evidence Layer（设计 §1）：从本轮结果提取证据（路径/风险/观察/获利）──
		// 锁内读取 inv 字段并追加（避免与 setField/Get 的锁内读写竞争）
		a.setField(inv, func(i *Investigation) {
			evs := extractEvidence(i.Evidence, i.ID, merged, i.Patterns, newObs, i.Profit, 100)
			if len(evs) > 0 {
				if a.evidence != nil {
					_ = a.evidence.Add(i.ID, evs...)
				}
				i.Evidence = append(i.Evidence, evs...)
			}
		})

		// ── 调查假设（§7：规则触发 + AI 细化 → 验证任务进入下一轮）──
		var verifyTargets []string
		if snap.ai != nil && cfg.UseAI && (len(newObs) > 0 || len(st.newPatterns) > 0) {
			hyps := snap.ai.Hypothesize(ctx, inv, newObs)
			if len(hyps) > 0 {
				existing := map[string]bool{}
				for _, h := range inv.Hypotheses {
					existing[h.Title] = true
				}
				var fresh []AIHypothesis
				for _, h := range hyps {
					if existing[h.Title] {
						continue
					}
					vt := verifyTasks(h, round+1)
					if len(vt) > 0 {
						h.Status = "verifying" // 验证任务将在下一轮执行
						for _, tk := range vt {
							pendingTasks = append(pendingTasks, pendingVerify{task: tk, hypIdx: len(inv.Hypotheses) + len(fresh)})
							if tk.Target != "" {
								verifyTargets = append(verifyTargets, tk.Target)
							}
						}
					} else {
						h.Status = "evaluated"
						h.Note = "无有效验证任务"
					}
					fresh = append(fresh, h)
				}
				if len(fresh) > 0 {
					a.setField(inv, func(i *Investigation) { i.Hypotheses = append(i.Hypotheses, fresh...) })
				}
			}
		}

		// ── 决策（§9：EXPAND / STOP / DEEP_ANALYSIS）──
		dec := decision.Decide(DecideInput{
			Target:               inv.Target,
			Round:                round,
			Elapsed:              time.Since(started),
			Paths:                merged,
			Patterns:             inv.Patterns,
			Entities:             inv.Entities,
			Candidates:           candidates,
			NewObs:               newObs,
			Memory:               mem,
			TotalDiscovered:      len(mem.DiscoveredAt) + len(candidates),
			PendingVerifications: len(pendingTasks),
			VerifyTargets:        verifyTargets,
		})
		a.setField(inv, func(i *Investigation) { i.Decision = &dec })
		a.setStage(inv, InvestigationExpanding, fmt.Sprintf("第 %d 轮：决策 %s", round, dec.Action), base+16)

		// ── V2 六维调查评分（设计 §9）：每轮决策后刷新 inv.Score ──
		var prof *analyticsapi.Profile
		if a.svc != nil {
			prof, _ = a.svc.Profile(ctx, inv.Target) // svc 有缓存，轮询成本低
		}
		profitDetected := inv.Profit != nil && (strings.Contains(inv.Profit.Kind, "profit") || inv.Profit.Detected)
		holdingDetected := inv.Profit != nil && strings.Contains(inv.Profit.Kind, "holding")
		var mode InvestigationMode
		if inv.Request != nil {
			mode = inv.Request.Mode // V2.1 Score Profile：按模式加权
		}
		score := scorer.Compute(ScoreInput{
			Profile:         prof,
			RiskScore:       dec.Scores.RiskScore,
			Entities:        inv.Entities,
			Paths:           merged,
			Candidates:      candidates,
			ProfitDetected:  profitDetected,
			HoldingDetected: holdingDetected,
			Mode:            mode,
		})
		a.setField(inv, func(i *Investigation) { i.Score = score })
		// AI 下一步建议（§6：AI 建议 → Decision Engine 验证；规则引擎为最终裁决）
		if snap.ai != nil && cfg.UseAI {
			if sug := snap.ai.Suggest(ctx, inv, dec); sug != nil {
				a.setField(inv, func(i *Investigation) { i.AISuggestion = sug })
			}
		}
		// AI 建议参与决策（#5 优化：防止规则过早停止；规则仍为最终裁决，仅高置信度 EXPAND 建议可延续调查）
		if inv.AISuggestion != nil {
			sug := inv.AISuggestion
			// 资源上限类 STOP（最大轮次/最长运行/最大地址数）不可被 AI 覆盖——防无限循环
			resourceLimitStop := hasStopReason(dec.Reasons, "最大") || hasStopReason(dec.Reasons, "最长")
			if dec.Action == DecisionStop && !resourceLimitStop &&
				strings.EqualFold(sug.Action, "EXPAND") &&
				sug.Confidence >= 0.8 && strings.TrimSpace(sug.Target) != "" &&
				validEVMAddress(sug.Target) {
				dec.Action = DecisionExpand
				dec.NextTargets = append(dec.NextTargets, strings.ToLower(sug.Target))
				dec.Reasons = append(dec.Reasons, fmt.Sprintf("AI 建议继续扩展 %s（置信度 %.2f）", shortAddr(sug.Target), sug.Confidence))
				a.setField(inv, func(i *Investigation) { i.Decision = &dec })
				logger.Log.Info().Str("inv", inv.ID).Str("target", sug.Target).Float64("conf", sug.Confidence).
					Msg("ai_suggestion_overrides_stop")
			} else if dec.Action == DecisionExpand && strings.EqualFold(sug.Action, "STOP") && sug.Confidence >= 0.9 {
				// AI 高置信度建议停止：记录但不覆盖规则（规则为最终裁决）
				dec.Reasons = append(dec.Reasons, "AI 建议停止（规则仍裁决继续）")
				a.setField(inv, func(i *Investigation) { i.Decision = &dec })
			}
		}

		rec := RoundRecord{
			Round:      round,
			Decision:   dec.Action,
			Note:       strings.Join(dec.Reasons, "；"),
			StartedAt:  roundStarted,
			FinishedAt: time.Now().UTC(),
		}
		a.setField(inv, func(i *Investigation) { i.Rounds = append(i.Rounds, rec) })

		switch dec.Action {
		case DecisionStop:
			a.setField(inv, func(i *Investigation) {
				i.StopReason = strings.Join(dec.Reasons, "；")
				i.StopCode = dec.StopCode // V2.1 Stop Strategy 枚举
			})
			done = true
		case DecisionDeepAnalysis:
			a.runAI(ctx, inv, snap, cfg) // AI 深入分析后结束
			done = true
		case DecisionExpand:
			if len(dec.NextTargets) > 0 {
				focus = dec.NextTargets // 下一轮扩展高价值地址 / 验证假设目标
			}
		}
		if done {
			break
		}
	}

	// ── VERIFYING：AI 最终分析（DEEP_ANALYSIS 未触发时）→ 验证结果并固化记忆 ──
	a.setStage(inv, InvestigationVerifying, "验证结果并固化记忆", 85)
	if cfg.UseAI && inv.AI == nil {
		a.runAI(ctx, inv, snap, cfg)
	}
	// 假设状态收尾：按验证任务真实执行结果门控（任务全部 done → 执行完毕；否则未执行）
	taskStatus := map[string]string{}
	for _, tk := range queue.Snapshot() {
		taskStatus[tk.ID] = tk.Status
	}
	a.setField(inv, func(i *Investigation) {
		for idx := range i.Hypotheses {
			h := i.Hypotheses[idx]
			if h.Status != "verifying" {
				continue
			}
			h.Status = "evaluated"
			switch {
			case len(h.TaskIDs) == 0:
				h.Note = "验证任务未执行（调查提前结束）"
			default:
				allDone := true
				for _, id := range h.TaskIDs {
					if taskStatus[id] != TaskDone {
						allDone = false
						break
					}
				}
				if allDone {
					h.Note = "验证任务已执行完毕"
				} else {
					h.Note = "验证任务未完成（调查提前结束）"
				}
			}
			i.Hypotheses[idx] = h
		}
	})
	a.addConclusions(inv)

	// ── REPORTING：GENERATE_REPORT 任务 ──
	a.setStage(inv, InvestigationReporting, "生成报告", 92)
	genTask := queue.Enqueue(InvestigationTask{
		Type:        TaskGenerateReport,
		Description: "生成调查报告（Markdown）",
		Priority:    4,
		Target:      inv.Target,
		Round:       inv.Round,
	})
	queue.Mark(genTask.ID, TaskRunning, "", "")
	a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
	a.persistTasks(inv.ID, queue.Snapshot()) // V1 Storage Layer：任务落盘
	cur, ok := a.Get(inv.ID)
	if !ok {
		return fmt.Errorf("调查已不存在: %s", inv.ID)
	}
	out, err := snap.report.Generate(cur, ReportMarkdown)
	if err != nil {
		queue.Mark(genTask.ID, TaskFailed, "", err.Error())
	} else {
		queue.Mark(genTask.ID, TaskDone, fmt.Sprintf("报告 %d 字节", len(out.Content)), "")
		a.setField(inv, func(i *Investigation) { i.Report = out })
	}
	a.memories.RecordCompletedTask(inv.ID, genTask.ID)
	a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
	a.persistTasks(inv.ID, queue.Snapshot()) // V1 Storage Layer：任务落盘

	// ── COMPLETED ──
	a.setField(inv, func(i *Investigation) {
		i.Status = InvestigationCompleted
		i.Progress = 100
		i.StageDetail = "调查完成"
		i.UpdatedAt = time.Now().UTC()
		i.CompletedAt = time.Now().UTC()
	})
	// AI 记忆固化（§13）
	if snap.ai != nil {
		snap.ai.Remember(inv)
		snap.ai.SaveMemory()
	}
	return nil
}

// buildQueue 构建一轮的任务队列。
// V2：第 1 轮按计划任务序列执行（意图/AI 驱动，设计 §7 Task Scheduler），
// 无计划任务时回退旧固定队列。
func (e *LoopEngine) buildQueue(round int, plan *InvestigationPlan, focus []string, snap agentSnapshot, cfg IntelligenceConfig) []InvestigationTask {
	var tasks []InvestigationTask
	if round == 1 && plan != nil && len(plan.Tasks) > 0 {
		// ── V2：计划驱动（归一化类型，优先级按计划）──
		for i, pt := range plan.Tasks {
			tasks = append(tasks, InvestigationTask{
				ID:          "p" + itoa(i+1),
				Type:        normalizeTaskType(pt.Type),
				Description: pt.Description,
				Priority:    pt.Priority,
				Target:      focus[0],
				Round:       round,
			})
		}
		// 地址扩展始终保留（决策引擎需要扩展候选）
		if snap.expansion != nil {
			tasks = append(tasks, InvestigationTask{
				Type:        TaskExpandAddress,
				Description: fmt.Sprintf("地址扩展（上限 %d）", cfg.MaxExpansion),
				Priority:    3,
				Target:      focus[0],
				Round:       round,
			})
		}
		return applyRuntimeDefaults(tasks, cfg)
	}
	// ── 旧固定队列（无计划任务 / 后续轮次）──
	if round == 1 {
		tasks = append(tasks, InvestigationTask{
			Type:        TaskAddressProfile,
			Description: "目标地址画像",
			Priority:    0,
			Target:      focus[0],
			Round:       round,
		})
	}
	for _, addr := range focus {
		tasks = append(tasks,
			InvestigationTask{
				Type:        TaskFlowAnalysis,
				Description: fmt.Sprintf("分析 %s 资金流", shortAddr(addr)),
				Priority:    1,
				Target:      addr,
				Round:       round,
			},
			InvestigationTask{
				Type:        TaskPathTrace,
				Description: fmt.Sprintf("Beam Search 追踪 %s（最多 %d 跳）", shortAddr(addr), plan.MaxHops),
				Priority:    1,
				Target:      addr,
				Round:       round,
			},
			InvestigationTask{
				Type:        TaskRiskScan,
				Description: fmt.Sprintf("风险模式扫描 %s", shortAddr(addr)),
				Priority:    2,
				Target:      addr,
				Round:       round,
			},
		)
	}
	tasks = append(tasks, InvestigationTask{
		Type:        TaskEntityCheck,
		Description: "实体识别",
		Priority:    2,
		Round:       round,
	})
	if snap.expansion != nil {
		tasks = append(tasks, InvestigationTask{
			Type:        TaskExpandAddress,
			Description: fmt.Sprintf("地址扩展（上限 %d）", cfg.MaxExpansion),
			Priority:    3,
			Target:      focus[0],
			Round:       round,
		})
	}
	return applyRuntimeDefaults(tasks, cfg)
}

// isYielding 判断主循环是否应让位中止：调查处于 WAITING（resumeRun 接管执行权）
// 或已终态（resumeRun 收尾完成，防主循环复活终态调查重复执行任务）。
func isYielding(state RuntimeState) bool {
	return state == RuntimeWaiting || isRuntimeTerminal(state)
}

// applyRuntimeDefaults 为任务注入 Runtime V2 默认超时/重试配置（设计 §5/§11）。
// 显式配置了 TimeoutSec/MaxRetries 的任务保持原值。
func applyRuntimeDefaults(tasks []InvestigationTask, cfg IntelligenceConfig) []InvestigationTask {
	for i := range tasks {
		if tasks[i].TimeoutSec == 0 {
			tasks[i].TimeoutSec = cfg.TaskTimeoutSec
		}
		if tasks[i].MaxRetries == 0 {
			tasks[i].MaxRetries = cfg.TaskMaxRetries
		}
	}
	return tasks
}

// executeTask 执行单个任务，返回结果摘要。skipError 表示因缺少依赖跳过。
// Runtime V2（设计 §7）：经 ExecutorRegistry 分发（Validate 前置校验 + Execute）。
func (e *LoopEngine) executeTask(ctx context.Context, a *InvestigationAgent, snap agentSnapshot, t *InvestigationTask, plan *InvestigationPlan, inv *Investigation, st *roundState, cfg IntelligenceConfig) (string, error) {
	exec, ok := e.registry().Get(t.Type)
	if !ok {
		return "", fmt.Errorf("未知任务类型: %s", t.Type)
	}
	// Validate 前置校验：数据源缺失 → 跳过（errSkipped 保持降级语义）
	if err := exec.Validate(a, snap); err != nil {
		return "", err
	}
	res, err := exec.Execute(ctx, a, snap, t, plan, inv, st)
	if err != nil {
		return "", err
	}
	if res.Status == "SKIPPED" {
		return "", errSkipped(res.Summary)
	}
	return res.Summary, nil
}

// dynamicAppend 按任务结果动态追加任务（V2 设计 §8 动态重新规划）：
// - 获利/沉淀检测命中 → 追加来源追踪（追查获利资金源头）；
// - 发现交易所实体 → 追加身份线索（识别交易所归属）。
// 幂等：按任务类型全局去重——依赖 TaskQueue.Snapshot 返回全部任务（含 done/skipped/failed
// 终态任务），因此同轮与跨轮都不会重复追加同一类型任务。
// V2.1 预算：累计任务数达到 maxTasks 上限时不再追加。
func (e *LoopEngine) dynamicAppend(q *TaskQueue, inv *Investigation, st *roundState, round, maxTasks int) {
	if inv == nil || q == nil {
		return
	}
	if maxTasks > 0 && q.TotalCount() >= maxTasks {
		return
	}
	existing := map[string]bool{}
	for _, tk := range q.Snapshot() {
		existing[tk.Type] = true
	}
	if inv.Profit != nil && inv.Profit.Detected && !existing[TaskBackwardTrace] {
		q.Enqueue(InvestigationTask{
			Type:        TaskBackwardTrace,
			Description: "动态追加：追查获利/沉淀资金源头",
			Priority:    2,
			Target:      inv.Target,
			Round:       round,
		})
	}
	if !existing[TaskIdentityLookup] {
		for _, ent := range st.newEntities {
			if ent.Entity == "exchange" {
				q.Enqueue(InvestigationTask{
					Type:        TaskIdentityLookup,
					Description: "动态追加：交易所实体身份线索",
					Priority:    2,
					Target:      inv.Target,
					Round:       round,
				})
				break
			}
		}
	}
}

// mergePaths 合并新路径（按签名去重 + 记忆去重），重排并保留 Top K。
func (a *InvestigationAgent) mergePaths(inv *Investigation, newPaths []FundPath, ranker *PathRanker, topK int, mem *InvestigationMemory) []RankedPath {
	seen := map[string]bool{}
	for _, p := range inv.Paths {
		seen[pathSignature(p.Path)] = true
	}
	merged := append([]RankedPath(nil), inv.Paths...)
	for _, p := range newPaths {
		sig := pathSignature(p)
		if seen[sig] {
			continue
		}
		if mem != nil && containsStr(mem.AnalyzedPaths, sig) {
			continue // 已分析路径（§11）
		}
		seen[sig] = true
		score := ranker.RankPath(p, nil)
		merged = append(merged, RankedPath{Path: p, Score: score, Summary: summarizePath(p)})
		a.memories.RecordPath(inv.ID, sig)
	}
	// 按总分降序
	for i := 0; i < len(merged); i++ {
		for j := i + 1; j < len(merged); j++ {
			if merged[j].Score.Total > merged[i].Score.Total {
				merged[i], merged[j] = merged[j], merged[i]
			}
		}
	}
	if len(merged) > topK {
		merged = merged[:topK]
	}
	return merged
}

// collectObservations 收集本轮观察结果（按记忆与引擎内签名去重）。
func (e *LoopEngine) collectObservations(obs *ObservationEngine, round int, st *roundState, mem *InvestigationMemory) []Observation {
	var out []Observation
	for _, p := range st.newPaths {
		out = append(out, obs.ObservePaths(round, "PATH_TRACE", []FundPath{p}, mem)...)
	}
	for _, addr := range st.focus {
		if flows := st.flowsByAddr[addr]; len(flows) > 0 {
			out = append(out, obs.ObserveFlows(round, "FLOW_ANALYSIS", addr, flows, mem)...)
		}
	}
	for _, p := range st.newPatterns {
		out = append(out, obs.ObservePatterns(round, "RISK_SCAN", []RiskPattern{p})...)
	}
	if len(st.newCandidates) > 0 {
		out = append(out, obs.ObserveExpansion(round, "EXPAND_ADDRESS", st.newCandidates, mem)...)
	}
	// 过滤去重占位（空 ID）
	filtered := out[:0]
	for _, o := range out {
		if o.ID != "" {
			filtered = append(filtered, o)
		}
	}
	return filtered
}

// runAI 运行 AI 深入分析（DEEP_ANALYSIS 决策或收尾阶段）。
// AIAgent 可用时走多角色深入分析 + Evidence Guard（§8/§12）；否则回退单次分析。
func (a *InvestigationAgent) runAI(ctx context.Context, inv *Investigation, snap agentSnapshot, cfg IntelligenceConfig) bool {
	if !cfg.UseAI {
		return false
	}
	if snap.ai != nil {
		a.setStage(inv, InvestigationAnalyzing, "DeepSeek AI 深入分析", 80)
		verified, analysis := snap.ai.DeepAnalyze(ctx, inv, inv.Target)
		if analysis == nil {
			return false
		}
		a.setField(inv, func(i *Investigation) { i.AI = analysis })
		if len(verified) > 0 {
			a.setField(inv, func(i *Investigation) { i.Findings = append(i.Findings, verified...) })
		}
		return true
	}
	if !snap.deepseek.Configured() {
		return false
	}
	a.setStage(inv, InvestigationAnalyzing, "DeepSeek AI 分析", 80)
	// 使用锁内副本构建上下文，避免与轮询读竞争
	cur, ok := a.Get(inv.ID)
	if !ok {
		return false
	}
	aiCtx := snap.contextBuilder.Build(cur)
	prompt := snap.contextBuilder.ToPrompt(aiCtx)
	analysis, err := snap.deepseek.Analyze(ctx, prompt)
	if err != nil {
		logger.Log.Warn().Str("inv", inv.ID).Err(err).Msg("intelligence_ai_failed")
		return false
	}
	a.setField(inv, func(i *Investigation) { i.AI = analysis })
	return true
}

// resolveNewEntities 解析缺失的地址实体并返回（已有实体不重复解析）。
func (a *InvestigationAgent) resolveNewEntities(ctx context.Context, addresses []string, existing []EntityInfo) []EntityInfo {
	known := map[string]bool{}
	for _, ent := range existing {
		known[strings.ToLower(ent.Address)] = true
	}
	var missing []string
	for _, addr := range addresses {
		addr = strings.ToLower(strings.TrimSpace(addr))
		if addr == "" || known[addr] {
			continue
		}
		known[addr] = true
		missing = append(missing, addr)
	}
	if len(missing) == 0 {
		return nil
	}
	return a.entityResolver.ResolveBatch(ctx, missing)
}

// shortAddr 缩写地址用于任务描述（0x1234…abcd）。
func shortAddr(addr string) string {
	addr = strings.ToLower(addr)
	if len(addr) <= 12 {
		return addr
	}
	return addr[:6] + "…" + addr[len(addr)-4:]
}
