package intelligence

import (
	"fmt"
	"strings"
	"time"
)

// ── Decision Engine（设计 §9）──
//
// 根据 PathScore / RiskScore / EntityScore / ExpansionScore 决定：
//   EXPAND（继续扩展高价值地址） / STOP（停止低价值路径） / DEEP_ANALYSIS（AI 深入分析）
// 智能停止机制（§11）：交易所地址 / 低价值地址 / 已分析路径 / 重复关系 /
// 轮次上限 / 运行时间上限 / 地址数上限（§10 自动扩展策略）。

// DecisionEngine 作出每轮调查决策。无状态，输入全部来自 DecideInput。
type DecisionEngine struct {
	cfg IntelligenceConfig
}

// NewDecisionEngine 创建决策引擎。
func NewDecisionEngine(cfg IntelligenceConfig) *DecisionEngine {
	return &DecisionEngine{cfg: cfg}
}

// DecideInput 是一轮决策的输入。
type DecideInput struct {
	Target     string
	Round      int
	Elapsed    time.Duration
	Paths      []RankedPath
	Patterns   []RiskPattern
	Entities   []EntityInfo
	Candidates []ExpansionResult // 本轮扩展候选
	NewObs     []Observation     // 本轮新观察
	Memory     *InvestigationMemory
	// TotalDiscovered 累计已发现地址数（含未记入记忆的扩展候选），
	// 用于 max_addresses 上限校验（候选不记入记忆，防止上限被绕过）。
	TotalDiscovered int
}

// Decide 作出本轮决策（EXPAND / STOP / DEEP_ANALYSIS）。
func (e *DecisionEngine) Decide(in DecideInput) Decision {
	cfg := e.cfg
	if cfg.MaxRounds < 1 {
		cfg.MaxRounds = 1
	}
	now := time.Now().UTC()
	dec := Decision{
		Action: DecisionStop,
		Round:  in.Round,
		Scores: e.scores(in),
		MadeAt: now,
	}

	// ── 停止条件（§10 限制 + §11 智能停止）──
	if cfg.MaxRuntimeMS > 0 && in.Elapsed >= time.Duration(cfg.MaxRuntimeMS)*time.Millisecond {
		dec.Reasons = append(dec.Reasons, fmt.Sprintf("超过最长运行时间（%dms）", cfg.MaxRuntimeMS))
		return dec
	}
	if in.Round >= cfg.MaxRounds {
		dec.Reasons = append(dec.Reasons, fmt.Sprintf("达到最大调查轮次 %d", cfg.MaxRounds))
		return dec
	}
	if in.Memory != nil {
		total := len(in.Memory.DiscoveredAt)
		if in.TotalDiscovered > total {
			total = in.TotalDiscovered
		}
		if total >= cfg.MaxAddresses {
			dec.Reasons = append(dec.Reasons, fmt.Sprintf("达到最大发现地址数 %d", cfg.MaxAddresses))
			return dec
		}
	}
	if in.Round > 1 && len(in.NewObs) == 0 {
		dec.Reasons = append(dec.Reasons, "本轮无新发现，停止低价值路径")
		return dec
	}

	// ── 扩展候选分类（§11：低价值 / 交易所 / 已分析 / 重复关系）──
	candidates, exchange, lowValue, analyzed := e.classifyCandidates(in)

	if len(candidates) == 0 {
		switch {
		case len(exchange) > 0:
			dec.Reasons = append(dec.Reasons, fmt.Sprintf("候选均为交易所地址（%d 个），停止扩展", len(exchange)))
		case len(analyzed) > 0:
			dec.Reasons = append(dec.Reasons, "候选均为已分析/已忽略地址（重复关系），停止扩展")
		case len(lowValue) > 0:
			dec.Reasons = append(dec.Reasons, fmt.Sprintf("候选评分低于门槛 %.0f（低价值地址），停止扩展", cfg.ExpansionThreshold))
		default:
			if dec.Scores.RiskScore >= 60 {
				dec.Action = DecisionDeepAnalysis
				dec.Reasons = append(dec.Reasons, fmt.Sprintf("风险较高（%.0f），进入深入分析", dec.Scores.RiskScore))
				return dec
			}
			dec.Reasons = append(dec.Reasons, "无高价值扩展候选")
		}
		return dec
	}

	// ── EXPAND：扩展高价值地址（下一轮目标 = Top N 候选）──
	topN := minInt(3, len(candidates))
	next := make([]string, 0, topN)
	for i := 0; i < topN; i++ {
		next = append(next, strings.ToLower(candidates[i].Address))
	}
	dec.Action = DecisionExpand
	dec.NextTargets = next
	dec.Reasons = append(dec.Reasons, fmt.Sprintf("发现 %d 个高价值扩展候选（最高评分 %.1f）", len(candidates), dec.Scores.ExpansionScore))
	return dec
}

// scores 计算四维评分。
func (e *DecisionEngine) scores(in DecideInput) DecisionScores {
	var s DecisionScores
	// PathScore：Top 路径评分
	for _, p := range in.Paths {
		if p.Score.Total > s.PathScore {
			s.PathScore = p.Score.Total
		}
	}
	// RiskScore：风险模式严重度 / 实体风险分
	for _, p := range in.Patterns {
		if w := severityWeight(p.Severity); w > s.RiskScore {
			s.RiskScore = w
		}
	}
	for _, ent := range in.Entities {
		if ent.Risk > s.RiskScore {
			s.RiskScore = ent.Risk
		}
	}
	// EntityScore：已知实体（exchange/bridge/dex/router/contract）占比
	if len(in.Entities) > 0 {
		known := 0
		for _, ent := range in.Entities {
			switch ent.Entity {
			case "exchange", "bridge", "dex", "router", "contract":
				known++
			}
		}
		s.EntityScore = float64(known) / float64(len(in.Entities)) * 100
	}
	// ExpansionScore：Top 候选评分
	for _, c := range in.Candidates {
		if c.Score > s.ExpansionScore {
			s.ExpansionScore = c.Score
		}
	}
	return s
}

// classifyCandidates 将扩展候选分类：
// candidates（可扩展）/ exchange（交易所）/ lowValue（低价值）/ analyzed（已分析或已忽略）。
func (e *DecisionEngine) classifyCandidates(in DecideInput) (candidates, exchange, lowValue, analyzed []ExpansionResult) {
	threshold := e.cfg.ExpansionThreshold
	for _, c := range in.Candidates {
		addr := strings.ToLower(strings.TrimSpace(c.Address))
		if !validEVMAddress(addr) {
			continue
		}
		if c.Score < threshold {
			lowValue = append(lowValue, c)
			continue
		}
		if in.Memory != nil {
			if t, ok := in.Memory.DiscoveredAt[addr]; ok && !t.IsZero() {
				analyzed = append(analyzed, c)
				continue
			}
			if containsStr(in.Memory.IgnoredEntities, addr) {
				analyzed = append(analyzed, c)
				continue
			}
		}
		entity := c.Entity
		if ent, ok := entityByAddress(in.Entities, addr); ok && ent != "" {
			entity = ent
		}
		if entity == "exchange" {
			exchange = append(exchange, c)
			continue
		}
		candidates = append(candidates, c)
	}
	// 按评分降序（EXPAND 取 Top N）
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].Score > candidates[i].Score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	return candidates, exchange, lowValue, analyzed
}

// entityByAddress 在实体列表中查找地址对应的实体类型。
func entityByAddress(entities []EntityInfo, address string) (string, bool) {
	address = strings.ToLower(address)
	for _, ent := range entities {
		if strings.EqualFold(ent.Address, address) {
			return ent.Entity, true
		}
	}
	return "", false
}
