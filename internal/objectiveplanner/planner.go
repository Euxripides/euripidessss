// Package objectiveplanner 实现 Objective-Driven DataRequirement Planner（Phase 5.4 §7-§9）：
// 调查目标 → Dataset Matrix → 成本估算/Guard → 输出 DataRequirement（不指定 Provider）。
package objectiveplanner

import (
	"fmt"
	"strings"
)

// ObjectiveType 调查目标类型（Phase 5.4 §7）。
type ObjectiveType string

const (
	FundSink          ObjectiveType = "fund_sink"
	ExchangeOfframp   ObjectiveType = "exchange_offramp"
	Profit            ObjectiveType = "profit"
	TokenProfit       ObjectiveType = "token_profit"
	SourceTrace       ObjectiveType = "source_trace"
	DestinationTrace  ObjectiveType = "destination_trace"
	IdentityResolution ObjectiveType = "identity_resolution"
)

// Constraints 目标约束。
type Constraints struct {
	Depth        int    `json:"depth,omitempty"`
	MaxAddresses int    `json:"max_addresses,omitempty"`
	MinAmountUSDT string `json:"min_amount_usdt,omitempty"`
}

// Objective 调查目标契约。
type Objective struct {
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Constraints Constraints `json:"constraints,omitempty"`
}

// DatasetNeed 一个数据需求（dataset + 方向 + 优先级）。
type DatasetNeed struct {
	Dataset       string `json:"dataset"`
	Direction     string `json:"direction,omitempty"` // both/in/out
	Priority      int    `json:"priority"`            // 1 最高
	CloudEligible bool   `json:"cloud_eligible"`
}

// CostEstimate 规划成本估算（Phase 5.4 §9）。
type CostEstimate struct {
	AddressCount      int    `json:"address_count"`
	BlockSpan         uint64 `json:"block_span"`
	DatasetCount      int    `json:"dataset_count"`
	EstimatedChunks   int    `json:"estimated_chunks"`
	EstimatedLocalGB  float64 `json:"estimated_local_gb"`
	CloudEligible     bool   `json:"cloud_eligible"`
	Rejected          bool   `json:"rejected,omitempty"`
	RejectReason      string `json:"reject_reason,omitempty"`
}

// Plan 输出：数据集需求 + 成本。
type Plan struct {
	Objective Objective     `json:"objective"`
	Needs     []DatasetNeed `json:"needs"`
	Estimate  CostEstimate  `json:"estimate"`
}

// matrix objective → 优先数据集（Key 与 downloadscheduler.Dataset 对齐；counterparty 由方向/扩展表达）。
var matrix = map[ObjectiveType][]DatasetNeed{
	FundSink: {
		{Dataset: "token_transfer", Direction: "in", Priority: 1, CloudEligible: true},
		{Dataset: "transactions", Direction: "in", Priority: 2, CloudEligible: true},
		{Dataset: "balance", Direction: "", Priority: 3, CloudEligible: false},
	},
	ExchangeOfframp: {
		{Dataset: "token_transfer", Direction: "out", Priority: 1, CloudEligible: true},
		{Dataset: "labels", Direction: "", Priority: 2, CloudEligible: false},
	},
	Profit: {
		{Dataset: "token_transfer", Direction: "both", Priority: 1, CloudEligible: true},
		{Dataset: "balance", Direction: "", Priority: 2, CloudEligible: false},
	},
	TokenProfit: {
		{Dataset: "token_transfer", Direction: "both", Priority: 1, CloudEligible: true},
		{Dataset: "transactions", Direction: "", Priority: 2, CloudEligible: true},
	},
	SourceTrace: {
		{Dataset: "token_transfer", Direction: "in", Priority: 1, CloudEligible: true},
		{Dataset: "transactions", Direction: "in", Priority: 2, CloudEligible: true},
	},
	DestinationTrace: {
		{Dataset: "token_transfer", Direction: "out", Priority: 1, CloudEligible: true},
		{Dataset: "labels", Direction: "", Priority: 2, CloudEligible: false},
	},
	IdentityResolution: {
		{Dataset: "token_transfer", Direction: "both", Priority: 1, CloudEligible: true},
		{Dataset: "labels", Direction: "", Priority: 2, CloudEligible: false},
	},
}

// ValidateObjective 校验目标类型。
func ValidateObjective(o Objective) error {
	if _, ok := matrix[ObjectiveType(strings.ToLower(strings.TrimSpace(o.Type)))]; !ok {
		return fmt.Errorf("未知 objective type: %s", o.Type)
	}
	return nil
}

// Build 生成数据需求与成本估算（Phase 5.4 §8/§9）。
func Build(o Objective, chainKey string, addresses []string, fromBlock, toBlock uint64,
	interactiveCap, backgroundCap, cloudEmergencyCap int) (*Plan, error) {
	typ := ObjectiveType(strings.ToLower(strings.TrimSpace(o.Type)))
	needs, ok := matrix[typ]
	if !ok {
		return nil, fmt.Errorf("未知 objective type: %s", o.Type)
	}
	n := len(addresses)
	if n == 0 {
		return nil, fmt.Errorf("objective 规划缺少地址")
	}
	if o.Constraints.MaxAddresses > 0 && n > o.Constraints.MaxAddresses {
		return nil, fmt.Errorf("地址数 %d 超过 objective 上限 %d", n, o.Constraints.MaxAddresses)
	}
	span := uint64(0)
	if toBlock >= fromBlock {
		span = toBlock - fromBlock + 1
	}
	// 估算 chunk：地址 25/块、区块 50,000/块（与 Orchestrator 策略一致）
	addrChunks := (n + 24) / 25
	blockChunks := 1
	if span > 50_000 {
		blockChunks = int((span + 49_999) / 50_000)
	}
	chunks := addrChunks * blockChunks * len(needs)
	est := CostEstimate{
		AddressCount:     n,
		BlockSpan:        span,
		DatasetCount:     len(needs),
		EstimatedChunks:  chunks,
		EstimatedLocalGB: float64(chunks) * 0.035, // ~35MB/chunk 保守估算（真实值随密度变化）
		CloudEligible:    cloudCapAllowed(needs, cloudEmergencyCap),
	}
	// Cost Guard：interactive/background/cloud 上限
	cap := interactiveCap
	if o.Constraints.MaxAddresses > 500 || len(needs) > 2 {
		cap = backgroundCap
	}
	if est.EstimatedChunks > cap {
		est.Rejected = true
		est.RejectReason = fmt.Sprintf("估算 chunks=%d 超过上限 %d", est.EstimatedChunks, cap)
	}
	return &Plan{Objective: o, Needs: needs, Estimate: est}, nil
}

func cloudCapAllowed(needs []DatasetNeed, cloudCap int) bool {
	if cloudCap <= 0 {
		return true
	}
	cloudNeeds := 0
	for _, n := range needs {
		if n.CloudEligible {
			cloudNeeds++
		}
	}
	return cloudNeeds <= cloudCap
}
