package intelligence

import (
	"fmt"
	"strings"
)

// ── Observation Engine（设计 §8）──
//
// 负责发现：新地址 / 新路径 / 新交易 / 风险事件，生成调查观察结果。
// 去重依据调查记忆（已发现地址、已分析路径）与引擎内签名去重，
// 避免重复调查（设计 §11 智能停止机制：已分析路径 / 重复关系）。

// ObservationEngine 从各任务结果中收集观察结果。
// 每个调查闭环实例独立使用一个引擎（含内部去重集合）。
type ObservationEngine struct {
	seq         int
	seenTx      map[string]bool // address|tx_hash → 已观察交易
	seenRisk    map[string]bool // type|address → 已观察风险
	seenPath    map[string]bool // 路径签名 → 已观察路径
	seenAddress map[string]bool // 本次调查内已观察地址（含记忆内地址）
}

// NewObservationEngine 创建观察引擎。
func NewObservationEngine() *ObservationEngine {
	return &ObservationEngine{
		seenTx:      map[string]bool{},
		seenRisk:    map[string]bool{},
		seenPath:    map[string]bool{},
		seenAddress: map[string]bool{},
	}
}

// ObservePaths 从追踪结果中发现新路径与新地址。
func (e *ObservationEngine) ObservePaths(round int, source string, paths []FundPath, mem *InvestigationMemory) []Observation {
	var out []Observation
	for _, p := range paths {
		sig := pathSignature(p)
		if e.seenPath[sig] {
			continue
		}
		e.seenPath[sig] = true
		if mem != nil && containsStr(mem.AnalyzedPaths, sig) {
			continue // 已分析路径，不再观察
		}
		out = append(out, e.new(round, source, ObsNewPath, "", sig, pathTotalAmount(p), 0))
		for _, n := range p.Nodes {
			if o := e.observeAddress(round, source, n, mem); o.ID != "" {
				out = append(out, o)
			}
		}
	}
	return out
}

// ObserveFlows 从资金流中发现新交易与新地址。
func (e *ObservationEngine) ObserveFlows(round int, source, address string, flows []FundEdge, mem *InvestigationMemory) []Observation {
	var out []Observation
	address = strings.ToLower(address)
	for _, f := range flows {
		txKey := address + "|" + strings.ToLower(f.TxHash)
		if e.seenTx[txKey] {
			continue
		}
		e.seenTx[txKey] = true
		value, _ := parseAmountFloat(f.Amount)
		out = append(out, e.new(round, source, ObsNewTransaction, f.TxHash,
			fmt.Sprintf("%s %s %s → %s", f.Token, f.Amount, f.From, f.To), value, f.Timestamp))
		// 对手地址（目标地址本身不重复观察）
		counterparty := f.To
		if strings.EqualFold(f.To, address) {
			counterparty = f.From
		}
		if !strings.EqualFold(counterparty, address) {
			if o := e.observeAddress(round, source, counterparty, mem); o.ID != "" {
				out = append(out, o)
			}
		}
	}
	return out
}

// ObservePatterns 从风险检测结果中发现风险事件。
func (e *ObservationEngine) ObservePatterns(round int, source string, patterns []RiskPattern) []Observation {
	var out []Observation
	for _, p := range patterns {
		key := string(p.Type) + "|" + strings.ToLower(p.Address)
		if e.seenRisk[key] {
			continue
		}
		e.seenRisk[key] = true
		out = append(out, e.new(round, source, ObsRiskEvent, p.Address, p.Detail, severityWeight(p.Severity), 0))
	}
	return out
}

// ObserveExpansion 从扩展结果中发现候选地址。
func (e *ObservationEngine) ObserveExpansion(round int, source string, results []ExpansionResult, mem *InvestigationMemory) []Observation {
	var out []Observation
	for _, r := range results {
		if o := e.observeAddress(round, source, r.Address, mem); o.ID != "" {
			out = append(out, o)
		}
	}
	return out
}

// observeAddress 观察新地址（记忆 + 引擎内去重）。
func (e *ObservationEngine) observeAddress(round int, source, address string, mem *InvestigationMemory) Observation {
	address = strings.ToLower(strings.TrimSpace(address))
	if address == "" {
		return Observation{}
	}
	if e.seenAddress[address] {
		return Observation{}
	}
	if mem != nil {
		if t, ok := mem.DiscoveredAt[address]; ok && !t.IsZero() {
			return Observation{} // 已发现地址不再观察
		}
	}
	e.seenAddress[address] = true
	return e.new(round, source, ObsNewAddress, address, address, 0, 0)
}

// new 构造观察结果并分配 ID。
func (e *ObservationEngine) new(round int, source string, typ ObservationType, address, detail string, value float64, ts int64) Observation {
	e.seq++
	return Observation{
		ID:        fmt.Sprintf("o%d", e.seq),
		Type:      typ,
		Address:   address,
		Detail:    detail,
		Source:    fmt.Sprintf("round %d %s", round, source),
		Value:     value,
		Timestamp: ts,
	}
}

// pathSignature 生成路径签名（节点序列）。
func pathSignature(p FundPath) string {
	return strings.Join(p.Nodes, "→")
}

// pathTotalAmount 计算路径总金额。
func pathTotalAmount(p FundPath) float64 {
	var total float64
	for _, e := range p.Edges {
		if f, ok := parseAmountFloat(e.Amount); ok {
			total += f
		}
	}
	return total
}

// severityWeight 风险等级权重（决策评分用）。
func severityWeight(severity string) float64 {
	switch severity {
	case "high":
		return 80
	case "medium":
		return 50
	case "low":
		return 30
	}
	return 0
}

// containsStr 判断字符串切片是否包含目标。
func containsStr(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
