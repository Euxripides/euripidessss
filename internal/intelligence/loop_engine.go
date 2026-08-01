package intelligence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
type LoopEngine struct{}

// NewLoopEngine 创建闭环引擎。
func NewLoopEngine() *LoopEngine { return &LoopEngine{} }

// roundState 是单轮执行中累积的状态。
type roundState struct {
	focus        []string
	flowsByAddr  map[string][]FundEdge
	newPaths     []FundPath
	newCandidates []ExpansionResult
	newEntities  []EntityInfo
	newPatterns  []RiskPattern
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
	// 扩展候选跨轮累积（未使用的候选在后续轮次仍可被决策选中）
	var candidates []ExpansionResult

	// 1. 规划（AI 优先，规则回退；后续轮次由决策确定新目标 = 增量规划）
	a.setStage(inv, InvestigationPlanning, "制定调查计划", 5)
	planInput := a.planInput(ctx, inv.Target)
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
			queue.Enqueue(t)
		}
		a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })

		// ── 执行任务 ──
		for {
			t := queue.Next()
			if t == nil {
				break
			}
			queue.Mark(t.ID, TaskRunning, "", "")
			a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
			result, err := e.executeTask(ctx, a, snap, t, plan, inv, st, cfg)
			if err != nil {
				var skip *skipError
				if errors.As(err, &skip) {
					queue.Mark(t.ID, TaskSkipped, skip.reason, "")
				} else {
					queue.Mark(t.ID, TaskFailed, "", err.Error())
					logger.Log.Warn().Str("inv", inv.ID).Str("task", t.Type).Err(err).Msg("intelligence_task_failed")
				}
				a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
				continue // 单任务失败不阻断调查
			}
			queue.Mark(t.ID, TaskDone, result, "")
			a.memories.RecordCompletedTask(inv.ID, t.ID)
			a.setField(inv, func(i *Investigation) { i.Tasks = queue.Snapshot() })
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
		// AI 下一步建议（§6：AI 建议 → Decision Engine 验证；规则引擎为最终裁决）
		if snap.ai != nil && cfg.UseAI {
			if sug := snap.ai.Suggest(ctx, inv, dec); sug != nil {
				a.setField(inv, func(i *Investigation) { i.AISuggestion = sug })
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
			a.setField(inv, func(i *Investigation) { i.StopReason = strings.Join(dec.Reasons, "；") })
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

// buildQueue 构建一轮的任务队列（7 种任务类型，设计 §7）。
func (e *LoopEngine) buildQueue(round int, plan *InvestigationPlan, focus []string, snap agentSnapshot, cfg IntelligenceConfig) []InvestigationTask {
	var tasks []InvestigationTask
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
	return tasks
}

// executeTask 执行单个任务，返回结果摘要。skipError 表示因缺少依赖跳过。
func (e *LoopEngine) executeTask(ctx context.Context, a *InvestigationAgent, snap agentSnapshot, t *InvestigationTask, plan *InvestigationPlan, inv *Investigation, st *roundState, cfg IntelligenceConfig) (string, error) {
	switch t.Type {
	case TaskAddressProfile:
		if a.svc == nil {
			return "", errSkipped("无画像数据源")
		}
		profile, err := a.svc.Profile(ctx, t.Target)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("交易 %d 笔（入 %d / 出 %d）", profile.TransactionCount, profile.TotalIn, profile.TotalOut), nil

	case TaskFlowAnalysis:
		if snap.flowSource == nil {
			return "", errSkipped("无资金流数据源")
		}
		flows, err := snap.flowSource.Flows(ctx, t.Target)
		if err != nil {
			return "", err
		}
		st.flowsByAddr[t.Target] = flows
		return fmt.Sprintf("%d 条资金边", len(flows)), nil

	case TaskPathTrace:
		if snap.tracer == nil {
			return "", errSkipped("无追踪器")
		}
		paths, err := snap.tracer.Trace(ctx, t.Target, plan.MaxHops, plan.BeamWidth)
		if err != nil {
			return "", err
		}
		st.newPaths = append(st.newPaths, paths...)
		return fmt.Sprintf("发现 %d 条候选路径", len(paths)), nil

	case TaskEntityCheck:
		addrSet := map[string]bool{inv.Target: true}
		for _, addr := range st.focus {
			addrSet[strings.ToLower(addr)] = true
		}
		for _, p := range inv.Paths {
			for _, n := range p.Path.Nodes {
				addrSet[n] = true
			}
		}
		addresses := make([]string, 0, len(addrSet))
		for addr := range addrSet {
			addresses = append(addresses, addr)
		}
		infos := a.resolveNewEntities(ctx, addresses, inv.Entities)
		st.newEntities = append(st.newEntities, infos...)
		return fmt.Sprintf("识别 %d 个新实体", len(infos)), nil

	case TaskRiskScan:
		flows := st.flowsByAddr[t.Target]
		if len(flows) == 0 {
			return "无资金流，跳过风险扫描", nil
		}
		patterns := snap.detector.Detect(t.Target, flows)
		seen := map[string]bool{}
		for _, p := range inv.Patterns {
			seen[string(p.Type)+"|"+strings.ToLower(p.Address)] = true
		}
		var fresh []RiskPattern
		for _, p := range patterns {
			key := string(p.Type) + "|" + strings.ToLower(p.Address)
			if !seen[key] {
				seen[key] = true
				fresh = append(fresh, p)
			}
		}
		st.newPatterns = append(st.newPatterns, fresh...)
		return fmt.Sprintf("%d 个风险模式", len(fresh)), nil

	case TaskExpandAddress:
		if snap.expansion == nil {
			return "", errSkipped("无扩展引擎")
		}
		cands, err := snap.expansion.Expand(ctx, t.Target, cfg.MaxExpansion)
		if err != nil {
			return "", err
		}
		st.newCandidates = append(st.newCandidates, cands...)
		// 候选实体即时识别（供决策过滤交易所地址）
		var addrs []string
		for _, c := range cands {
			addrs = append(addrs, c.Address)
		}
		st.newEntities = append(st.newEntities, a.resolveNewEntities(ctx, addrs, inv.Entities)...)
		return fmt.Sprintf("%d 个扩展候选", len(cands)), nil

	case TaskGenerateReport:
		return "", errSkipped("报告在调查收尾阶段生成")
	}
	return "", fmt.Errorf("未知任务类型: %s", t.Type)
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
