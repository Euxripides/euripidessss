package intelligence

import (
	"context"
	"fmt"
)

// ── 执行器注册（V2 设计 §6/§7）──
//
// 将 12 种任务执行器注册到统一 ExecutorRegistry。
// 各执行器包装现有逻辑（executeBalanceAnalysis 等包级函数 + 原 executeTask
// switch 分支），不改写执行算法；executeTask 改为查注册表分发。
//
// 注意：部分执行器需要执行期上下文（plan/st/inv），闭包捕获由
// registryExecutor.Execute 传入的参数，包装函数签名统一。

// defaultExecutors 构建默认执行器注册表（12 种任务类型）。
func defaultExecutors() *ExecutorRegistry {
	reg := NewExecutorRegistry()

	// ── 画像 / 余额 / Token / 获利（V2 包级函数包装）──
	reg.Register(&executorFunc{
		taskType: TaskAddressProfile,
		validate: func(a *InvestigationAgent, _ agentSnapshot) error {
			if a.svc == nil {
				return errSkipped("无画像数据源")
			}
			return nil
		},
		execute: func(ctx context.Context, a *InvestigationAgent, _ agentSnapshot, task *InvestigationTask, _ *InvestigationPlan, _ *Investigation, _ *roundState) (ExecutorResult, error) {
			profile, err := a.svc.Profile(ctx, task.Target)
			if err != nil {
				return ExecutorResult{Status: "FAILED"}, err
			}
			return ExecutorResult{Status: "SUCCESS",
				Summary: fmt.Sprintf("交易 %d 笔（入 %d / 出 %d）", profile.TransactionCount, profile.TotalIn, profile.TotalOut)}, nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskBalanceAnalysis,
		validate: func(a *InvestigationAgent, _ agentSnapshot) error {
			if a.svc == nil {
				return errSkipped("无画像数据源")
			}
			return nil
		},
		execute: func(ctx context.Context, a *InvestigationAgent, _ agentSnapshot, task *InvestigationTask, _ *InvestigationPlan, _ *Investigation, _ *roundState) (ExecutorResult, error) {
			summary, err := executeBalanceAnalysis(ctx, a, task.Target)
			if err != nil {
				return ExecutorResult{Status: "FAILED"}, err
			}
			return ExecutorResult{Status: "SUCCESS", Summary: summary}, nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskTokenAnalysis,
		validate: func(_ *InvestigationAgent, snap agentSnapshot) error {
			if snap.flowSource == nil {
				return errSkipped("无资金流数据源")
			}
			return nil
		},
		execute: func(ctx context.Context, _ *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, _ *InvestigationPlan, _ *Investigation, _ *roundState) (ExecutorResult, error) {
			summary, err := executeTokenAnalysis(ctx, snap, task.Target)
			if err != nil {
				return ExecutorResult{Status: "FAILED"}, err
			}
			return ExecutorResult{Status: "SUCCESS", Summary: summary}, nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskProfitDetection,
		validate: func(_ *InvestigationAgent, snap agentSnapshot) error {
			if snap.flowSource == nil {
				return errSkipped("无资金流数据源")
			}
			return nil
		},
		execute: func(ctx context.Context, a *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, _ *InvestigationPlan, inv *Investigation, _ *roundState) (ExecutorResult, error) {
			summary, err := executeProfitDetection(ctx, a, snap, task.Target, inv)
			if err != nil {
				return ExecutorResult{Status: "FAILED"}, err
			}
			return ExecutorResult{Status: "SUCCESS", Summary: summary}, nil
		},
	})

	// ── 资金流 / 路径追踪 / 正反向追踪 / 流图（依赖 flowSource/tracer）──
	reg.Register(&executorFunc{
		taskType: TaskFlowAnalysis,
		validate: func(_ *InvestigationAgent, snap agentSnapshot) error {
			if snap.flowSource == nil {
				return errSkipped("无资金流数据源")
			}
			return nil
		},
		execute: func(ctx context.Context, _ *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, _ *InvestigationPlan, _ *Investigation, st *roundState) (ExecutorResult, error) {
			flows, err := snap.flowSource.Flows(ctx, task.Target)
			if err != nil {
				return ExecutorResult{Status: "FAILED"}, err
			}
			if st != nil {
				st.flowsByAddr[task.Target] = flows
			}
			return ExecutorResult{Status: "SUCCESS", Summary: fmt.Sprintf("%d 条资金边", len(flows))}, nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskPathTrace,
		validate: func(_ *InvestigationAgent, snap agentSnapshot) error {
			if snap.tracer == nil {
				return errSkipped("无追踪器")
			}
			return nil
		},
		execute: func(ctx context.Context, _ *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, plan *InvestigationPlan, _ *Investigation, st *roundState) (ExecutorResult, error) {
			paths, err := snap.tracer.Trace(ctx, task.Target, plan.MaxHops, plan.BeamWidth)
			if err != nil {
				return ExecutorResult{Status: "FAILED"}, err
			}
			if st != nil {
				st.newPaths = append(st.newPaths, paths...)
			}
			return ExecutorResult{Status: "SUCCESS", Summary: fmt.Sprintf("发现 %d 条候选路径", len(paths))}, nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskForwardTrace,
		validate: func(_ *InvestigationAgent, snap agentSnapshot) error {
			if snap.tracer == nil {
				return errSkipped("无追踪器")
			}
			return nil
		},
		execute: func(ctx context.Context, _ *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, plan *InvestigationPlan, _ *Investigation, st *roundState) (ExecutorResult, error) {
			summary, err := executeDirectionTrace(ctx, snap, task.Target, plan, st, true)
			if err != nil {
				return ExecutorResult{Status: "FAILED"}, err
			}
			return ExecutorResult{Status: "SUCCESS", Summary: summary}, nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskBackwardTrace,
		validate: func(_ *InvestigationAgent, snap agentSnapshot) error {
			if snap.tracer == nil {
				return errSkipped("无追踪器")
			}
			return nil
		},
		execute: func(ctx context.Context, _ *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, plan *InvestigationPlan, _ *Investigation, st *roundState) (ExecutorResult, error) {
			summary, err := executeDirectionTrace(ctx, snap, task.Target, plan, st, false)
			if err != nil {
				return ExecutorResult{Status: "FAILED"}, err
			}
			return ExecutorResult{Status: "SUCCESS", Summary: summary}, nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskFlowGraph,
		validate: func(_ *InvestigationAgent, snap agentSnapshot) error {
			if snap.flowSource == nil {
				return errSkipped("无资金流数据源")
			}
			return nil
		},
		execute: func(ctx context.Context, _ *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, _ *InvestigationPlan, _ *Investigation, _ *roundState) (ExecutorResult, error) {
			summary, err := executeFlowGraph(ctx, snap, task.Target)
			if err != nil {
				return ExecutorResult{Status: "FAILED"}, err
			}
			return ExecutorResult{Status: "SUCCESS", Summary: summary}, nil
		},
	})

	// ── 实体 / 风险 / 扩展 / 交易所 / 聚类 / 身份 / 报告 ──
	reg.Register(&executorFunc{
		taskType: TaskEntityCheck,
		validate: func(_ *InvestigationAgent, _ agentSnapshot) error { return nil },
		execute: func(ctx context.Context, a *InvestigationAgent, _ agentSnapshot, _ *InvestigationTask, _ *InvestigationPlan, inv *Investigation, st *roundState) (ExecutorResult, error) {
			addrSet := map[string]bool{inv.Target: true}
			if st != nil {
				for _, addr := range st.focus {
					addrSet[toLower(addr)] = true
				}
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
			if st != nil {
				st.newEntities = append(st.newEntities, infos...)
			}
			return ExecutorResult{Status: "SUCCESS", Summary: fmt.Sprintf("识别 %d 个新实体", len(infos))}, nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskRiskScan,
		validate: func(_ *InvestigationAgent, _ agentSnapshot) error { return nil },
		execute: func(ctx context.Context, _ *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, _ *InvestigationPlan, inv *Investigation, st *roundState) (ExecutorResult, error) {
			return executeRiskDetect(snap, task, inv, st), nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskRiskAnalysis, // RISK_SCAN 别名
		validate: func(_ *InvestigationAgent, _ agentSnapshot) error { return nil },
		execute: func(ctx context.Context, _ *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, _ *InvestigationPlan, inv *Investigation, st *roundState) (ExecutorResult, error) {
			return executeRiskDetect(snap, task, inv, st), nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskExpandAddress,
		validate: func(_ *InvestigationAgent, snap agentSnapshot) error {
			if snap.expansion == nil {
				return errSkipped("无扩展引擎")
			}
			return nil
		},
		execute: func(ctx context.Context, a *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, _ *InvestigationPlan, inv *Investigation, st *roundState) (ExecutorResult, error) {
			cands, err := snap.expansion.Expand(ctx, task.Target, a.Config().MaxExpansion)
			if err != nil {
				return ExecutorResult{Status: "FAILED"}, err
			}
			if st != nil {
				st.newCandidates = append(st.newCandidates, cands...)
				var addrs []string
				for _, c := range cands {
					addrs = append(addrs, c.Address)
				}
				st.newEntities = append(st.newEntities, a.resolveNewEntities(ctx, addrs, inv.Entities)...)
			}
			return ExecutorResult{Status: "SUCCESS", Summary: fmt.Sprintf("%d 个扩展候选", len(cands))}, nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskExchangeDetect,
		validate: func(_ *InvestigationAgent, snap agentSnapshot) error {
			if snap.flowSource == nil {
				return errSkipped("无资金流数据源")
			}
			return nil
		},
		execute: func(ctx context.Context, a *InvestigationAgent, snap agentSnapshot, task *InvestigationTask, _ *InvestigationPlan, inv *Investigation, st *roundState) (ExecutorResult, error) {
			summary, err := executeExchangeDetection(ctx, a, snap, task.Target, inv, st)
			if err != nil {
				return ExecutorResult{Status: "FAILED"}, err
			}
			return ExecutorResult{Status: "SUCCESS", Summary: summary}, nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskEntityCluster,
		validate: func(_ *InvestigationAgent, _ agentSnapshot) error { return nil },
		execute: func(ctx context.Context, a *InvestigationAgent, _ agentSnapshot, _ *InvestigationTask, _ *InvestigationPlan, inv *Investigation, _ *roundState) (ExecutorResult, error) {
			summary, err := executeEntityCluster(ctx, a, inv)
			if err != nil {
				return ExecutorResult{Status: "FAILED"}, err
			}
			return ExecutorResult{Status: "SUCCESS", Summary: summary}, nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskIdentityLookup,
		validate: func(_ *InvestigationAgent, _ agentSnapshot) error { return nil },
		execute: func(ctx context.Context, a *InvestigationAgent, _ agentSnapshot, _ *InvestigationTask, _ *InvestigationPlan, inv *Investigation, _ *roundState) (ExecutorResult, error) {
			summary, err := executeIdentityLookup(ctx, a, inv)
			if err != nil {
				return ExecutorResult{Status: "FAILED"}, err
			}
			return ExecutorResult{Status: "SUCCESS", Summary: summary}, nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskGenerateReport,
		validate: func(_ *InvestigationAgent, _ agentSnapshot) error {
			return errSkipped("报告在调查收尾阶段生成")
		},
		execute: func(ctx context.Context, _ *InvestigationAgent, _ agentSnapshot, _ *InvestigationTask, _ *InvestigationPlan, _ *Investigation, _ *roundState) (ExecutorResult, error) {
			return ExecutorResult{Status: "SKIPPED", Summary: "报告在调查收尾阶段生成"}, nil
		},
	})
	reg.Register(&executorFunc{
		taskType: TaskReportGenerate, // GENERATE_REPORT 别名
		validate: func(_ *InvestigationAgent, _ agentSnapshot) error {
			return errSkipped("报告在调查收尾阶段生成")
		},
		execute: func(ctx context.Context, _ *InvestigationAgent, _ agentSnapshot, _ *InvestigationTask, _ *InvestigationPlan, _ *Investigation, _ *roundState) (ExecutorResult, error) {
			return ExecutorResult{Status: "SKIPPED", Summary: "报告在调查收尾阶段生成"}, nil
		},
	})

	return reg
}

// executeRiskDetect 风险扫描执行（RISK_SCAN / RISK_ANALYSIS 共用）。
func executeRiskDetect(snap agentSnapshot, task *InvestigationTask, inv *Investigation, st *roundState) ExecutorResult {
	flows := st.flowsByAddr[task.Target]
	if len(flows) == 0 {
		return ExecutorResult{Status: "SUCCESS", Summary: "无资金流，跳过风险扫描"}
	}
	patterns := snap.detector.Detect(task.Target, flows)
	seen := map[string]bool{}
	for _, p := range inv.Patterns {
		seen[string(p.Type)+"|"+toLower(p.Address)] = true
	}
	var fresh []RiskPattern
	for _, p := range patterns {
		key := string(p.Type) + "|" + toLower(p.Address)
		if !seen[key] {
			seen[key] = true
			fresh = append(fresh, p)
		}
	}
	st.newPatterns = append(st.newPatterns, fresh...)
	return ExecutorResult{Status: "SUCCESS", Summary: fmt.Sprintf("%d 个风险模式", len(fresh))}
}

// toLower 小写辅助（避免与 strings 导入冲突的局部用法）。
func toLower(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + 32
		}
	}
	return string(out)
}
