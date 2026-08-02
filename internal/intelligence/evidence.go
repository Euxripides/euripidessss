package intelligence

import "time"

// ── Evidence Layer（V2.1 设计 §1）──
//
// 让每个 AI 结论/调查发现都有交易证据、地址证据、时间证据与可信度：
//   Task → Finding → Evidence Extractor → Evidence Store → Report

// EvidenceType 是证据类型。
type EvidenceType string

const (
	EvTransaction EvidenceType = "TRANSACTION" // 交易证据（tx_hash/block/amount/token）
	EvAddress     EvidenceType = "ADDRESS"     // 地址证据（地址关联/实体归属）
	EvTime        EvidenceType = "TIME"        // 时间证据（时间窗口/活跃区间）
	EvPath        EvidenceType = "PATH"        // 路径证据（资金路径结构）
	EvRisk        EvidenceType = "RISK"        // 风险模式证据
	EvProfit      EvidenceType = "PROFIT"      // 获利检测证据
)

// Evidence 是一条调查证据。
type Evidence struct {
	ID              string       `json:"id"`
	InvestigationID string       `json:"investigation_id"`
	TaskID          string       `json:"task_id,omitempty"` // 来源任务（如 PATH_TRACE）
	Type            EvidenceType `json:"evidence_type"`
	Address         string       `json:"address,omitempty"` // 关联地址
	TxHash          string       `json:"tx_hash,omitempty"` // 交易哈希
	BlockNumber     uint64       `json:"block_number,omitempty"`
	Token           string       `json:"token,omitempty"`
	Amount          string       `json:"amount,omitempty"` // raw 金额（原始格式）
	Detail          string       `json:"detail"`           // 证据描述（摘要）
	Confidence      float64      `json:"confidence"`       // 可信度 0-1
	CreatedAt       time.Time    `json:"created_at"`
}
