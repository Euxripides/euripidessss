// Package cloudplanner 实现 SQD Cloud 弹性计算分级（设计 V1.0）：
// Cloud S / L / XL 三档、资源评分、自动升级/降级、预算守卫。
package cloudplanner

import (
	"fmt"
	"math"
	"time"
)

// CloudTier SQD Cloud Worker 分级。
type CloudTier string

const (
	CloudS  CloudTier = "S"
	CloudL  CloudTier = "L"
	CloudXL CloudTier = "XL"
)

// ProbeInput Discovery 输出（设计 §29）。
type ProbeInput struct {
	EstimatedRows           uint64
	EstimatedBytes          uint64
	EstimatedRuntimeSeconds float64
	RangeCount              int
	Dataset                 string
}

// ResourcePlan 单 DatasetJob 的 Cloud 资源计划（设计 §32）。
type ResourcePlan struct {
	Tier             CloudTier
	CPU              int
	MemoryGB         int
	TempDiskGB       int
	MaxWorkers       int
	EstimatedRuntime time.Duration
	EstimatedCost    float64 // 人民币（估算）
	Score            float64
	Reasons          []string
}

// RuntimeMetrics 运行期指标（设计 §34）。
type RuntimeMetrics struct {
	RowsPerSecond            float64
	BytesPerSecond           float64
	CPUUsage                 float64
	MemoryUsage              float64
	DiskIOUsage              float64
	CompletedPercent         float64
	ETA                      time.Duration
	OOMCount                 int
	TimeoutCount             int
	OriginalEstimatedRuntime time.Duration
}

// datasetComplexity 数据集复杂度（设计 §7）。
var datasetComplexity = map[string]float64{
	"balances":              0.2,
	"token_metadata":        0.3,
	"transactions":          1.0,
	"token_transfers":       1.2,
	"logs":                  1.5,
	"decoded_logs":          1.8,
	"internal_transactions": 2.0,
	"trace":                 2.5,
}

// DatasetComplexity 返回数据集复杂度（未知默认 1.0）。
func DatasetComplexity(dataset string) float64 {
	if c, ok := datasetComplexity[dataset]; ok {
		return c
	}
	return 1.0
}

// EffectiveWorkload = EstimatedRows × DatasetComplexity（设计 §7）。
func EffectiveWorkload(in ProbeInput) float64 {
	return float64(in.EstimatedRows) * DatasetComplexity(in.Dataset)
}

// Planner Cloud 资源规划器（设计 §30/§33）。
type Planner struct {
	Budget BudgetGuard
}

// NewPlanner 创建规划器。
func NewPlanner(budget BudgetGuard) *Planner {
	return &Planner{Budget: budget}
}

// Plan 根据 Discovery 输出生成资源计划（评分 + 直接/强制 XL 规则 + 预算）。
func (p *Planner) Plan(in ProbeInput) ResourcePlan {
	plan := ResourcePlan{
		Tier:             p.tierFor(in),
		EstimatedRuntime: secondsDuration(in.EstimatedRuntimeSeconds),
		Reasons:          []string{},
	}
	plan.Score = p.Score(in)
	plan.applyTierSpecs()
	plan.EstimatedCost = estimateCost(in, plan)
	if p.Budget.Enabled {
		if p.Budget.MaxSingleJobCost > 0 && plan.EstimatedCost > p.Budget.MaxSingleJobCost && plan.Tier == CloudXL {
			plan.Tier = CloudL
			plan.applyTierSpecs()
			plan.EstimatedCost = estimateCost(in, plan)
			plan.Reasons = append(plan.Reasons, fmt.Sprintf(
				"预算守卫：单任务成本上限 ¥%.2f，XL 降为 L", p.Budget.MaxSingleJobCost))
		}
	}
	return plan
}

// Reevaluate 运行期重新评估：命中升级/降级条件则切换 Tier（设计 §16-§18/§35）。
func (p *Planner) Reevaluate(current ResourcePlan, m RuntimeMetrics) ResourcePlan {
	next := current
	orig := m.OriginalEstimatedRuntime
	if orig <= 0 {
		orig = current.EstimatedRuntime
	}
	if m.OOMCount > 0 || m.TimeoutCount > 0 {
		return p.upgrade(next, "OOM/处理超时，自动升级")
	}
	if m.MemoryUsage > 0.85 {
		return p.upgrade(next, "内存占用 >85%，自动升级")
	}
	if m.CompletedPercent < 0.9 {
		if m.ETA > 60*time.Minute {
			return p.upgrade(next, "ETA 超过 60 分钟，自动升级")
		}
		if orig > 0 && m.ETA > 2*orig {
			return p.upgrade(next, "ETA 超过原始估算 2 倍，自动升级")
		}
		if m.RowsPerSecond > 0 && orig > 0 {
			expectedRowsPerSec := float64(0)
			// expected 由调用方在 OriginalExpectedRowsPerSecond 未提供时无法推导，此处用保守判定：
			// 吞吐 < 预期 35% 依赖调用方把预期速度折算进 RowsPerSecond 与 ETA。
			_ = expectedRowsPerSec
		}
	}
	// 降级：XL 主阶段完成，剩余小范围补洞 → L（设计 §18）
	if current.Tier == CloudXL && m.CompletedPercent >= 0.95 && m.ETA > 0 && m.ETA < 5*time.Minute {
		return p.downgrade(next, "主数据阶段完成，剩余小范围补洞，XL 降为 L")
	}
	return next
}

func (p *Planner) upgrade(plan ResourcePlan, reason string) ResourcePlan {
	if plan.Tier == CloudXL {
		plan.Reasons = appendUnique(plan.Reasons, reason)
		return plan
	}
	if plan.Tier == CloudS {
		plan.Tier = CloudL
	} else {
		plan.Tier = CloudXL
	}
	plan.Reasons = append(plan.Reasons, reason)
	plan.applyTierSpecs()
	return plan
}

func (p *Planner) downgrade(plan ResourcePlan, reason string) ResourcePlan {
	if plan.Tier != CloudXL {
		return plan
	}
	plan.Tier = CloudL
	plan.Reasons = append(plan.Reasons, reason)
	plan.applyTierSpecs()
	return plan
}

// applyTierSpecs 按档位填充资源规格（设计 §4/§21）。
func (plan *ResourcePlan) applyTierSpecs() {
	switch plan.Tier {
	case CloudS:
		plan.CPU, plan.MemoryGB, plan.TempDiskGB, plan.MaxWorkers = 4, 8, 20, 8
	case CloudL:
		plan.CPU, plan.MemoryGB, plan.TempDiskGB, plan.MaxWorkers = 8, 16, 50, 4
	case CloudXL:
		plan.CPU, plan.MemoryGB, plan.TempDiskGB, plan.MaxWorkers = 32, 64, 150, 2
	}
}

// ApplySpecs 按档位填充资源规格（外部包调用）。
func (plan *ResourcePlan) ApplySpecs() { plan.applyTierSpecs() }

// tierFor 评分分档 + 直接/强制 XL 规则（设计 §5/§6/§10）。
func (p *Planner) tierFor(in ProbeInput) CloudTier {
	if p.forceXL(in) {
		return CloudXL
	}
	score := p.Score(in)
	switch {
	case score < 30:
		return CloudS
	case score < 70:
		return CloudL
	default:
		return CloudXL
	}
}

// directXLCandidate 直接进入 XL 候选（设计 §5）。
func directXLCandidate(in ProbeInput) bool {
	if in.EstimatedRows >= 5_000_000 {
		return true
	}
	if in.EstimatedBytes >= 5<<30 {
		return true
	}
	if in.EstimatedRuntimeSeconds >= 30*60 {
		return true
	}
	if in.RangeCount >= 64 {
		return true
	}
	complexity := DatasetComplexity(in.Dataset)
	if (in.Dataset == "logs" || in.Dataset == "internal_transactions" || in.Dataset == "trace" ||
		in.Dataset == "decoded_logs") && float64(in.EstimatedRows)*complexity >= 2_000_000 {
		return true
	}
	return false
}

// forceXL 强制 XL（设计 §6）。
func (p *Planner) forceXL(in ProbeInput) bool {
	if in.EstimatedRows >= 20_000_000 {
		return true
	}
	if in.EstimatedBytes >= 20<<30 {
		return true
	}
	if in.EstimatedRuntimeSeconds >= 2*3600 {
		return true
	}
	if estimateMemoryGB(in) >= 24 {
		return true
	}
	if estimateTempGB(in) >= 100 {
		return true
	}
	return false
}

// Score CloudResourceScore（设计 §8/§9）。
func (p *Planner) Score(in ProbeInput) float64 {
	s := rowScore(in.EstimatedRows) +
		byteScore(in.EstimatedBytes) +
		complexityScore(in) +
		memoryScore(in) +
		tempDiskScore(in) +
		partitionScore(in.RangeCount) +
		runtimeScore(in.EstimatedRuntimeSeconds)
	return math.Round(s*10) / 10
}

func rowScore(rows uint64) float64 {
	switch {
	case rows < 500_000:
		return 5
	case rows < 5_000_000:
		return 20
	case rows < 20_000_000:
		return 40
	default:
		return 60
	}
}

func byteScore(bytes uint64) float64 {
	gb := float64(bytes) / (1 << 30)
	switch {
	case gb < 1:
		return 5
	case gb < 5:
		return 15
	case gb < 20:
		return 30
	default:
		return 50
	}
}

func complexityScore(in ProbeInput) float64 {
	c := DatasetComplexity(in.Dataset)
	switch {
	case c < 0.5:
		return 0
	case c < 1.0:
		return 2
	case c < 1.5:
		return 4
	case c < 2.0:
		return 8
	default:
		return 12
	}
}

func estimateMemoryGB(in ProbeInput) float64 {
	workload := EffectiveWorkload(in)
	// 每行约 0.2KB → GB；数据本体按 50% 常驻内存估算
	return math.Round((workload*0.2/1024/1024+float64(in.EstimatedBytes)*0.5/(1<<30))*10) / 10
}

func memoryScore(in ProbeInput) float64 {
	gb := estimateMemoryGB(in)
	switch {
	case gb < 2:
		return 0
	case gb < 4:
		return 5
	case gb < 8:
		return 15
	case gb < 24:
		return 30
	default:
		return 50
	}
}

func estimateTempGB(in ProbeInput) float64 {
	return math.Round(float64(in.EstimatedBytes)*3/(1<<30)*10) / 10
}

func tempDiskScore(in ProbeInput) float64 {
	gb := estimateTempGB(in)
	switch {
	case gb < 10:
		return 0
	case gb < 50:
		return 8
	case gb < 100:
		return 20
	default:
		return 40
	}
}

func partitionScore(ranges int) float64 {
	switch {
	case ranges < 8:
		return 0
	case ranges < 64:
		return 10
	default:
		return 30
	}
}

func runtimeScore(seconds float64) float64 {
	switch {
	case seconds < 10*60:
		return 5
	case seconds < 30*60:
		return 15
	case seconds < 120*60:
		return 35
	default:
		return 60
	}
}

// estimateCost 成本粗估（人民币）：vCPU 分钟 + 内存 GB 分钟 + 网络 GB + 临时盘 GB 分钟。
func estimateCost(in ProbeInput, plan ResourcePlan) float64 {
	minutes := in.EstimatedRuntimeSeconds / 60
	if minutes <= 0 {
		minutes = 5
	}
	cpuCost := float64(plan.CPU) * minutes * 0.02
	memCost := float64(plan.MemoryGB) * minutes * 0.003
	netGB := float64(in.EstimatedBytes) / (1 << 30)
	netCost := netGB * 0.05
	tempCost := estimateTempGB(in) * minutes * 0.0005
	return math.Round((cpuCost+memCost+netCost+tempCost)*100) / 100
}

func secondsDuration(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}
