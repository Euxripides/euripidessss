// Package fundflow 实现 Fund Flow Intelligence V2（设计 V1.0）：
// Path Scoring + Profit Attribution + Settlement Detection + Entity-Aware Fund Tracing。
// 核心原则：路径必须带评分/置信度/证据；获利归因必须区分本金/利润/中转/自转/回流。
package fundflow

import "time"

// EdgeType 边类型（设计 §3）。
type EdgeType string

const (
	EdgeTransfer          EdgeType = "TRANSFER"
	EdgeTokenTransfer     EdgeType = "TOKEN_TRANSFER"
	EdgeInternalTransfer  EdgeType = "INTERNAL_TRANSFER"
	EdgeSwapIn            EdgeType = "SWAP_IN"
	EdgeSwapOut           EdgeType = "SWAP_OUT"
	EdgeBridgeIn          EdgeType = "BRIDGE_IN"
	EdgeBridgeOut         EdgeType = "BRIDGE_OUT"
	EdgeDeposit           EdgeType = "DEPOSIT"
	EdgeWithdrawal        EdgeType = "WITHDRAWAL"
	EdgeSweep             EdgeType = "SWEEP"
	EdgeCollect           EdgeType = "COLLECT"
	EdgeDistribute        EdgeType = "DISTRIBUTE"
	EdgeSettlement        EdgeType = "SETTLEMENT"
	EdgeFunding           EdgeType = "FUNDING"
	EdgeRefund            EdgeType = "REFUND"
	EdgeSelfTransfer      EdgeType = "SELF_TRANSFER"
	EdgeInternalEntity    EdgeType = "INTERNAL_ENTITY_TRANSFER"
	EdgeUnknown           EdgeType = "UNKNOWN"
)

// FlowEdge 是最小资金流单位（设计 §2）。
type FlowEdge struct {
	ChainID          int64     `json:"chain_id"`
	FromAddress      string    `json:"from_address"`
	ToAddress        string    `json:"to_address"`
	FromEntityID     string    `json:"from_entity_id,omitempty"`
	ToEntityID       string    `json:"to_entity_id,omitempty"`
	TxHash           string    `json:"tx_hash,omitempty"`
	BlockNumber      uint64    `json:"block_number,omitempty"`
	BlockTime        time.Time `json:"block_time,omitempty"`
	AssetAddress     string    `json:"asset_address,omitempty"`
	Symbol           string    `json:"symbol,omitempty"`
	RawValue         string    `json:"raw_value,omitempty"`
	NormalizedValue  string    `json:"normalized_value,omitempty"`
	EdgeType         EdgeType  `json:"edge_type"`
	Dataset          string    `json:"dataset,omitempty"`
	EvidenceIDs      []string  `json:"evidence_ids,omitempty"`
}

// PathNode 是路径节点。
type PathNode struct {
	Address      string   `json:"address"`
	EntityID     string   `json:"entity_id,omitempty"`
	EntityName   string   `json:"entity_name,omitempty"`
	EntityType   string   `json:"entity_type,omitempty"`
	InAmount     string   `json:"in_amount,omitempty"`
	OutAmount    string   `json:"out_amount,omitempty"`
	BlockNumber  uint64   `json:"block_number,omitempty"`
	EdgeType     EdgeType `json:"edge_type,omitempty"`
	EdgeTxHash   string   `json:"edge_tx_hash,omitempty"`
	Token        string   `json:"token,omitempty"`
}

// Path 是一条关键资金路径（设计 §28-§38）。
type Path struct {
	ID           string     `json:"id"`
	RootAddress  string     `json:"root_address"`
	ChainKey     string     `json:"chain_key"`
	Goal         string     `json:"goal,omitempty"`
	Nodes        []PathNode `json:"nodes"`
	TotalAmount  string     `json:"total_amount,omitempty"`
	Hops         int        `json:"hops"`
	PathType     string     `json:"path_type"`
	Score        float64    `json:"score"`
	Confidence   float64    `json:"confidence"`
	TerminalType string     `json:"terminal_type,omitempty"`
	Evidence     []EvidenceRefLite `json:"evidence,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// EvidenceRefLite 是路径证据的轻量视图。
type EvidenceRefLite struct {
	SourceType  string  `json:"source_type"`
	SourceName  string  `json:"source_name"`
	Observation string  `json:"observation"`
	Confidence  float64 `json:"confidence"`
}

// ProfitAttribution 是获利归因结果（设计 §19）。
type ProfitAttribution struct {
	Address          string   `json:"address"`
	EntityID         string   `json:"entity_id,omitempty"`
	EntityName       string   `json:"entity_name,omitempty"`
	GrossInflow      string   `json:"gross_inflow"`
	GrossOutflow     string   `json:"gross_outflow"`
	CostBasis        string   `json:"cost_basis,omitempty"`
	ReturnedPrincipal string  `json:"returned_principal,omitempty"`
	NetProfit        string   `json:"net_profit"`
	Level            string   `json:"level"` // L0 / L1
	Confidence       float64  `json:"confidence"`
	EvidenceIDs      []string `json:"evidence_ids,omitempty"`
}

// SettlementResult 是资金沉淀结果（设计 §20-§25）。
type SettlementResult struct {
	Address                 string  `json:"address"`
	EntityID                string  `json:"entity_id,omitempty"`
	EntityName              string  `json:"entity_name,omitempty"`
	RetainedValue           string  `json:"retained_value"`
	HoldingDurationSeconds  float64 `json:"holding_duration_seconds"`
	LastOutflowBlock        string  `json:"last_outflow_block,omitempty"`
	SettlementScore         float64 `json:"settlement_score"`
	SettlementType          string  `json:"settlement_type"`
	Confidence              float64 `json:"confidence"`
	EvidenceIDs             []string `json:"evidence_ids,omitempty"`
}

// CashoutResult 是兑现候选（设计 §26-§27）。
type CashoutResult struct {
	SourceAddress      string    `json:"source_address"`
	DestinationAddress string    `json:"destination_address"`
	EntityID           string    `json:"entity_id,omitempty"`
	EntityName         string    `json:"entity_name,omitempty"`
	TxHash             string    `json:"tx_hash,omitempty"`
	Timestamp          time.Time `json:"timestamp,omitempty"`
	Token              string    `json:"token,omitempty"`
	Amount             string    `json:"amount,omitempty"`
	PathType           string    `json:"path_type"`
	Confidence         float64   `json:"confidence"`
	EvidenceIDs        []string  `json:"evidence_ids,omitempty"`
}

// RoundTripResult 是回流检测结果（设计 §40-§41）。
type RoundTripResult struct {
	PathID             string   `json:"path_id,omitempty"`
	Cycle              []string `json:"cycle"`
	ReturnRatio        float64  `json:"return_ratio"`
	TimeGapSeconds     float64  `json:"time_gap_seconds"`
	AssetConsistency   float64  `json:"asset_consistency"`
	EntityConsistency  float64  `json:"entity_consistency"`
	Score              float64  `json:"score"`
}

// ConservationResult 是资金守恒检查（设计 §42-§43）。
type ConservationResult struct {
	Address   string  `json:"address"`
	Inflow    string  `json:"inflow"`
	Outflow   string  `json:"outflow"`
	Balance   string  `json:"balance,omitempty"`
	Deviation float64 `json:"deviation"`
	Pass      bool    `json:"pass"`
	Reason    string  `json:"reason,omitempty"`
}

// EntityAwareNode 是实体感知图节点（设计 §6-§7）。
type EntityAwareNode struct {
	Address     string  `json:"address"`
	EntityID    string  `json:"entity_id,omitempty"`
	EntityName  string  `json:"entity_name,omitempty"`
	EntityType  string  `json:"entity_type,omitempty"`
	GrossInflow string  `json:"gross_inflow,omitempty"`
	GrossOutflow string `json:"gross_outflow,omitempty"`
	NetFlow     string  `json:"net_flow,omitempty"`
}

// EntityAwareEdge 是实体感知图边（设计 §6、§38）。
type EntityAwareEdge struct {
	From      string   `json:"from"`
	To        string   `json:"to"`
	FromEntity string  `json:"from_entity,omitempty"`
	ToEntity   string  `json:"to_entity,omitempty"`
	Token     string   `json:"token,omitempty"`
	Amount    string   `json:"amount,omitempty"`
	TxHash    string   `json:"tx_hash,omitempty"`
	BlockNumber uint64 `json:"block_number,omitempty"`
	EdgeType  EdgeType `json:"edge_type"`
}

// EntityAwareFlowGraph 是实体感知资金图（设计 §6）。
type EntityAwareFlowGraph struct {
	Root       string             `json:"root"`
	Nodes      []EntityAwareNode  `json:"nodes,omitempty"`
	Edges      []EntityAwareEdge  `json:"edges,omitempty"`
	CollapsedEntities int         `json:"collapsed_entities"`
}

// AnalysisResult 是资金流智能分析结果。
type AnalysisResult struct {
	RootAddress  string                  `json:"root_address"`
	ChainKey     string                  `json:"chain_key"`
	Goal         string                  `json:"goal,omitempty"`
	CacheHit     bool                    `json:"cache_hit"`
	GeneratedAt  time.Time               `json:"generated_at"`
	Paths        []*Path                 `json:"paths,omitempty"`
	Profit       []*ProfitAttribution    `json:"profit,omitempty"`
	Settlements  []*SettlementResult     `json:"settlements,omitempty"`
	Cashouts     []*CashoutResult        `json:"cashouts,omitempty"`
	RoundTrips   []*RoundTripResult      `json:"round_trips,omitempty"`
	Conservation []*ConservationResult   `json:"conservation,omitempty"`
	Graph        *EntityAwareFlowGraph   `json:"graph,omitempty"`
	Summary      map[string]any          `json:"summary,omitempty"`
}
