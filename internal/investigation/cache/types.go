// Package cache 实现 Investigation Data Cache V2（设计 V1.0 §3-§6、§38-§40、§45）：
// 按 investigation_id 保存调查上下文、地址画像/覆盖状态、图缓存引用与预取候选状态，
// 纯文件系统存储（原子写），不引入数据库。
package cache

import "time"

// ContextSnapshot 是调查上下文快照（设计 §39）。
type ContextSnapshot struct {
	ChainID      int64    `json:"chain_id"`
	ChainKey     string   `json:"chain_key"`
	FocusAddress string   `json:"focus_address"`
	FromBlock    uint64   `json:"from_block"`
	ToBlock      uint64   `json:"to_block"`
	FromTime     string   `json:"from_time,omitempty"`
	ToTime       string   `json:"to_time,omitempty"`
	Tokens       []string `json:"tokens,omitempty"`
	Goal         string   `json:"goal,omitempty"`
	CurrentPath  []string `json:"current_path,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AddressState 是单个地址在调查中的缓存状态（设计 §45 地址画像缓存）。
type AddressState struct {
	Address          string    `json:"address"`
	Coverage         float64   `json:"coverage"`
	Certification    string    `json:"certification,omitempty"`
	TxCount          int64     `json:"tx_count,omitempty"`
	TokenTransferCount int64   `json:"token_transfer_count,omitempty"`
	Counterparties   int       `json:"counterparties,omitempty"`
	Balance          string    `json:"balance,omitempty"`
	FirstSeen        string    `json:"first_seen,omitempty"`
	LastSeen         string    `json:"last_seen,omitempty"`
	Inflow           string    `json:"inflow,omitempty"`
	Outflow          string    `json:"outflow,omitempty"`
	RiskFlags        []string  `json:"risk_flags,omitempty"`
	PrefetchPriority string    `json:"prefetch_priority,omitempty"`
	PrefetchStatus   string    `json:"prefetch_status,omitempty"`
	BatchID          string    `json:"batch_id,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// CandidateSummary 是预取候选的轻量视图（避免 investigation/cache 依赖 prefetch 包）。
type CandidateSummary struct {
	Address          string   `json:"address"`
	ParentAddress    string   `json:"parent_address,omitempty"`
	Score            float64  `json:"score"`
	Priority         string   `json:"priority"`
	Status           string   `json:"status,omitempty"`
	BatchID          string   `json:"batch_id,omitempty"`
	Reasons          []string `json:"reasons,omitempty"`
	RequiredDatasets []string `json:"required_datasets,omitempty"`
	EstimatedRows    uint64   `json:"estimated_rows,omitempty"`
	EstimatedBytes   uint64   `json:"estimated_bytes,omitempty"`
	FromBlock        uint64   `json:"from_block"`
	ToBlock          uint64   `json:"to_block"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// InvestigationCache 是单个调查的缓存记录。
type InvestigationCache struct {
	ID                string                     `json:"id"`
	SchemaVersion     int                        `json:"schema_version"`
	Context           ContextSnapshot            `json:"context"`
	Addresses         map[string]*AddressState   `json:"addresses,omitempty"`
	GraphKeys         []string                   `json:"graph_keys,omitempty"`
	PrefetchCandidates map[string]*CandidateSummary `json:"prefetch_candidates,omitempty"`
	UpdatedAt         time.Time                  `json:"updated_at"`
}

const schemaVersion = 2

