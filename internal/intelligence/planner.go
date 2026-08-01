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
	Target       string
	Profile      map[string]any // analyticsapi.Profile 摘要
	RiskScore    float64
	InCount      int64
	OutCount     int64
	HasFlows     bool
	TopToken     string
}

// Plan 生成调查计划。
func (p *Planner) Plan(input PlanInput) *InvestigationPlan {
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
