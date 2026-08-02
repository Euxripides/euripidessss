package intelligence

import (
	"fmt"
	"time"
)

// ── Investigation Planner ──
//
// 输入：目标地址、地址画像、风险指标、资金概览
// 输出：调查任务清单（分析资金来源/主要流向/高价值路径/实体关系）

// Planner 制定调查计划。
type Planner struct {
	cfg IntelligenceConfig
}

// NewPlanner 创建规划器。
func NewPlanner(cfg IntelligenceConfig) *Planner {
	return &Planner{cfg: cfg}
}

// PlanInput 是规划输入信号。
type PlanInput struct {
	Target    string
	Profile   map[string]any // analyticsapi.Profile 摘要
	RiskScore float64
	InCount   int64
	OutCount  int64
	HasFlows  bool
	TopToken  string
	// V2：调查意图（objective/expected_result/mode 分析结果，可为 nil）
	Intent *InvestigationIntent
}

// taskDescriptions 是 12 种任务类型的展示描述。
var taskDescriptions = map[string]string{
	TaskAddressProfile:  "目标地址画像",
	TaskBalanceAnalysis: "余额与资产规模分析",
	TaskTokenAnalysis:   "Token 持仓与分布分析",
	TaskProfitDetection: "获利检测（买卖对账/沉淀识别）",
	TaskForwardTrace:    "正向资金追踪（去向）",
	TaskBackwardTrace:   "反向资金追踪（来源）",
	TaskFlowGraph:       "资金流图构建",
	TaskExchangeDetect:  "交易所入口识别",
	TaskEntityCluster:   "实体聚类",
	TaskRiskAnalysis:    "风险模式扫描",
	TaskIdentityLookup:  "身份线索查找",
	TaskFlowAnalysis:    "资金流分析",
	TaskPathTrace:       "高价值路径追踪",
	TaskEntityCheck:     "实体关系检查",
	TaskRiskScan:        "风险检查",
	TaskExpandAddress:   "地址扩展",
	TaskGenerateReport:  "报告生成",
}

// normalizeTaskType 将规划/旧类型归一化为可执行任务类型
// （旧 7 种类型保持原值，V2 别名与语义等价类型合并）。
func normalizeTaskType(t string) string {
	switch t {
	case "FUND_SOURCE":
		return TaskBackwardTrace
	case "FUND_FLOW":
		return TaskForwardTrace
	case "HIGH_VALUE_PATH":
		return TaskPathTrace
	case "ENTITY_RELATION":
		return TaskEntityCheck
	case "RISK_CHECK":
		return TaskRiskScan
	case "RISK_ANALYSIS":
		return TaskRiskAnalysis
	case "REPORT_GENERATE":
		return TaskReportGenerate
	default:
		return t
	}
}

// Plan 生成调查计划。携带意图时按模式路由任务序列（V2），否则使用旧规则规划。
func (p *Planner) Plan(input PlanInput) *InvestigationPlan {
	if input.Intent != nil {
		return p.planByIntent(input)
	}
	return p.planLegacy(input)
}

// planByIntent 按意图（模式/方向/目标）生成任务序列（V2 设计 §6/§7/§12）。
func (p *Planner) planByIntent(input PlanInput) *InvestigationPlan {
	intent := input.Intent
	plan := &InvestigationPlan{
		Target:      input.Target,
		MaxHops:     p.cfg.MaxHops,
		BeamWidth:   p.cfg.BeamWidth,
		TopPaths:    p.cfg.TopPaths,
		MinAmount:   p.cfg.MinAmount,
		GeneratedAt: time.Now().UTC(),
		Mode:        intent.Mode,
	}
	// 目标描述（来自意图）
	plan.Objectives = append(plan.Objectives, intent.Summary)
	for _, g := range intent.Goals {
		plan.Objectives = append(plan.Objectives, "意图目标："+g)
	}

	// 模式 → 任务序列
	var seq []string
	switch intent.Mode {
	case ModeProfitAnalyze:
		seq = []string{TaskAddressProfile, TaskProfitDetection, TaskTokenAnalysis, TaskBalanceAnalysis, TaskEntityCluster, TaskFlowGraph, TaskRiskAnalysis}
	case ModeExchangeEntry:
		seq = []string{TaskAddressProfile, TaskExchangeDetect, TaskBackwardTrace, TaskForwardTrace, TaskIdentityLookup, TaskFlowGraph}
	case ModeIdentityLookup:
		seq = []string{TaskAddressProfile, TaskIdentityLookup, TaskEntityCluster, TaskExchangeDetect, TaskBackwardTrace, TaskFlowGraph}
	case ModeRiskScan:
		seq = []string{TaskAddressProfile, TaskRiskAnalysis, TaskProfitDetection, TaskFlowGraph, TaskBackwardTrace}
	default: // ModeFundTrace / auto：按方向偏好
		switch intent.Direction {
		case "in":
			seq = []string{TaskAddressProfile, TaskBackwardTrace, TaskExchangeDetect, TaskFlowGraph, TaskRiskAnalysis}
		case "out":
			seq = []string{TaskAddressProfile, TaskForwardTrace, TaskExchangeDetect, TaskFlowGraph, TaskRiskAnalysis}
		default:
			seq = []string{TaskAddressProfile, TaskBackwardTrace, TaskForwardTrace, TaskExchangeDetect, TaskFlowGraph, TaskRiskAnalysis}
		}
	}

	// 生成任务（优先级：前 2 个 P1，中间 P2，其余 P3；预计时长 ≈ 任务数 × 2 分钟）
	for i, t := range seq {
		priority := 3
		switch {
		case i < 2:
			priority = 1
		case i < 5:
			priority = 2
		}
		desc := taskDescriptions[t]
		if desc == "" {
			desc = t
		}
		if intent.Direction == "in" && t == TaskForwardTrace {
			continue // 方向偏好过滤：只追踪来源时跳过正向追踪
		}
		if intent.Direction == "out" && t == TaskBackwardTrace {
			continue
		}
		plan.Tasks = append(plan.Tasks, PlannedTask{
			ID:          fmt.Sprintf("t%d", i+1),
			Type:        t,
			Description: desc,
			Priority:    priority,
		})
	}
	plan.EstimatedMinutes = maxInt(3, len(plan.Tasks)*2)
	if len(plan.Tasks) == 0 {
		plan.Tasks = append(plan.Tasks, PlannedTask{
			ID:          "t1",
			Type:        TaskFlowAnalysis,
			Description: "分析目标地址资金流（双向追踪）",
			Priority:    1,
		})
	}
	return plan
}

// planLegacy 是旧规则规划（无意图时兜底，保持原行为）。
func (p *Planner) planLegacy(input PlanInput) *InvestigationPlan {
	plan := &InvestigationPlan{
		Target:      input.Target,
		MaxHops:     p.cfg.MaxHops,
		BeamWidth:   p.cfg.BeamWidth,
		TopPaths:    p.cfg.TopPaths,
		MinAmount:   p.cfg.MinAmount,
		GeneratedAt: time.Now().UTC(),
	}

	// 目标 1：分析资金来源（有入账时）
	if input.InCount > 0 || input.HasFlows {
		plan.Objectives = append(plan.Objectives, "分析资金来源")
		plan.Tasks = append(plan.Tasks, PlannedTask{
			ID:          "t1",
			Type:        "FUND_SOURCE",
			Description: fmt.Sprintf("沿入边反向追踪资金来源（最多 %d 跳）", p.cfg.MaxHops),
			Priority:    1,
		})
	}

	// 目标 2：分析主要流向（有出账时）
	if input.OutCount > 0 || input.HasFlows {
		plan.Objectives = append(plan.Objectives, "分析主要流向")
		plan.Tasks = append(plan.Tasks, PlannedTask{
			ID:          "t2",
			Type:        "FUND_FLOW",
			Description: fmt.Sprintf("沿出边正向追踪资金去向（最多 %d 跳）", p.cfg.MaxHops),
			Priority:    1,
		})
	}

	// 目标 3：追踪高价值路径（大额/连续）
	plan.Objectives = append(plan.Objectives, "追踪高价值路径")
	plan.Tasks = append(plan.Tasks, PlannedTask{
		ID:          "t3",
		Type:        "HIGH_VALUE_PATH",
		Description: fmt.Sprintf("Beam Search 保留 Top %d 高价值路径（金额+时间连续性+风险排名）", p.cfg.TopPaths),
		Priority:    2,
	})

	// 目标 4：检查实体关系
	plan.Objectives = append(plan.Objectives, "检查实体关系")
	plan.Tasks = append(plan.Tasks, PlannedTask{
		ID:          "t4",
		Type:        "ENTITY_RELATION",
		Description: "识别路径中交易所/桥/DEX/归集地址及其关系",
		Priority:    2,
	})

	// 目标 5：风险检查（风险分高或资金异常时）
	if input.RiskScore >= 60 {
		plan.Objectives = append(plan.Objectives, "风险检查")
		plan.Tasks = append(plan.Tasks, PlannedTask{
			ID:          "t5",
			Type:        "RISK_CHECK",
			Description: "检测快速转移/多地址拆分/归集/大额进入等风险模式",
			Priority:    3,
		})
	}

	// 兜底：无信号时至少追踪
	if len(plan.Tasks) == 0 {
		plan.Tasks = append(plan.Tasks, PlannedTask{
			ID:          "t1",
			Type:        "FUND_FLOW",
			Description: "分析目标地址资金流（双向追踪）",
			Priority:    1,
		})
	}
	return plan
}

// maxInt 返回两数较大值。
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
