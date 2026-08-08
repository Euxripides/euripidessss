// Package entityintel 实现 Entity Intelligence Layer V1（设计 V1.0）：
// Address Label Resolution + Entity Mapping + Cluster Intelligence + Evidence Provenance。
// 核心原则：地址 != 实体，标签 != 事实，推断 != 证明；
// 任何标签都必须带 Confidence、Evidence、Source、UpdatedAt。
package entityintel

import "time"

// EntityType 统一实体类型（设计 §3）。
type EntityType string

const (
	EntityExchange          EntityType = "EXCHANGE"
	EntityDEX               EntityType = "DEX"
	EntityBridge            EntityType = "BRIDGE"
	EntityCEXDeposit        EntityType = "CEX_DEPOSIT"
	EntityCEXHotWallet      EntityType = "CEX_HOT_WALLET"
	EntityCEXColdWallet     EntityType = "CEX_COLD_WALLET"
	EntityPaymentService    EntityType = "PAYMENT_SERVICE"
	EntityCustodian         EntityType = "CUSTODIAN"
	EntityMarketMaker       EntityType = "MARKET_MAKER"
	EntityProjectTreasury   EntityType = "PROJECT_TREASURY"
	EntityProjectDeployer   EntityType = "PROJECT_DEPLOYER"
	EntityContract          EntityType = "CONTRACT"
	EntityTokenContract     EntityType = "TOKEN_CONTRACT"
	EntityRouter            EntityType = "ROUTER"
	EntityMultisig          EntityType = "MULTISIG"
	EntityRelayer           EntityType = "RELAYER"
	EntityBot               EntityType = "BOT"
	EntityMEV               EntityType = "MEV"
	EntityMinerValidator    EntityType = "MINER_VALIDATOR"
	EntityMixer             EntityType = "MIXER"
	EntityScam              EntityType = "SCAM"
	EntityPhishing          EntityType = "PHISHING"
	EntityExploit           EntityType = "EXPLOIT"
	EntityUnknownService    EntityType = "UNKNOWN_SERVICE"
	EntityUnknownEntity     EntityType = "UNKNOWN_ENTITY"
	EntityIndividualUnknown EntityType = "INDIVIDUAL_UNKNOWN"
)

// LabelSource 标签来源标准化（设计 §5）。
type LabelSource string

const (
	SourcePublicLabel      LabelSource = "PUBLIC_LABEL"
	SourceExplorerLabel    LabelSource = "EXPLORER_LABEL"
	SourceProjectOfficial  LabelSource = "PROJECT_OFFICIAL"
	SourceContractMetadata LabelSource = "CONTRACT_METADATA"
	SourceOnchainPattern   LabelSource = "ONCHAIN_PATTERN"
	SourceClusterInference LabelSource = "CLUSTER_INFERENCE"
	SourceUserManual       LabelSource = "USER_MANUAL"
	SourceCaseEvidence     LabelSource = "CASE_EVIDENCE"
	SourceExternalDataset  LabelSource = "EXTERNAL_DATASET"
	SourceLegalResponse    LabelSource = "LEGAL_RESPONSE"
)

// LabelScope 标签作用域（设计 §46）。
type LabelScope string

const (
	ScopeGlobal        LabelScope = "GLOBAL"
	ScopeInvestigation LabelScope = "INVESTIGATION"
	ScopeSession       LabelScope = "SESSION"
)

// ConfidenceTier 可信度等级（设计 §6）。
type ConfidenceTier string

const (
	TierConfirmed  ConfidenceTier = "CONFIRMED"
	TierHigh       ConfidenceTier = "HIGH"
	TierMedium     ConfidenceTier = "MEDIUM"
	TierLow        ConfidenceTier = "LOW"
	TierUnverified ConfidenceTier = "UNVERIFIED"
)

// EvidenceRef 证据引用（设计 §7，本阶段最重要部分）。
type EvidenceRef struct {
	EvidenceID  string     `json:"evidence_id"`
	SourceType  string     `json:"source_type"`
	SourceName  string     `json:"source_name"`
	SourceURI   string     `json:"source_uri,omitempty"`
	Observation string     `json:"observation"`
	CollectedAt time.Time  `json:"collected_at"`
	ValidFrom   *time.Time `json:"valid_from,omitempty"`
	ValidTo     *time.Time `json:"valid_to,omitempty"`
	Confidence  float64    `json:"confidence"`
}

// AddressLabel 地址标签（设计 §8）。
type AddressLabel struct {
	ChainID          int64       `json:"chain_id"`
	Address          string      `json:"address"`
	Label            string      `json:"label"`
	EntityID         string      `json:"entity_id,omitempty"`
	Scope            LabelScope  `json:"scope"`
	Confidence       float64     `json:"confidence"`
	EvidenceIDs      []string    `json:"evidence_ids,omitempty"`
	ResolverVersion  string      `json:"resolver_version"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

// Entity 实体（设计 §9-§10，多对一地址）。
type Entity struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	EntityType  EntityType  `json:"entity_type"`
	ChainIDs    []int64     `json:"chain_ids,omitempty"`
	Addresses   []string    `json:"addresses,omitempty"`
	Confidence  float64     `json:"confidence"`
	EvidenceIDs []string    `json:"evidence_ids,omitempty"`
	Source      LabelSource `json:"source"`
	Version     int         `json:"version"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// AddressCluster 地址聚类（设计 §11-§13；Cluster 不直接等于 Entity）。
type AddressCluster struct {
	ID                string    `json:"id"`
	Addresses         []string  `json:"addresses"`
	ClusterType       string    `json:"cluster_type"`
	Confidence        float64   `json:"confidence"`
	FalsePositiveRisk float64   `json:"false_positive_risk"`
	MinEvidenceCount  int       `json:"min_evidence_count"`
	EvidenceIDs       []string  `json:"evidence_ids,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// InvestigationLead 调证信息结构化（设计 §28-§29）。
type InvestigationLead struct {
	ID              string    `json:"id"`
	InvestigationID string    `json:"investigation_id"`
	Address         string    `json:"address"`
	EntityID        string    `json:"entity_id,omitempty"`
	EntityName      string    `json:"entity_name,omitempty"`
	LeadType        string    `json:"lead_type"`
	TransactionHash string    `json:"transaction_hash,omitempty"`
	BlockNumber     uint64    `json:"block_number,omitempty"`
	Timestamp       time.Time `json:"timestamp,omitempty"`
	Token           string    `json:"token,omitempty"`
	Amount          string    `json:"amount,omitempty"`
	EvidenceIDs     []string  `json:"evidence_ids,omitempty"`
	Confidence      float64   `json:"confidence"`
	CreatedAt       time.Time `json:"created_at"`
}

// AddressFeature 是地址画像特征（设计 §59 Feature Store）。
type AddressFeature struct {
	TxCount             int64   `json:"tx_count"`
	CounterpartyCount   int64   `json:"counterparty_count"`
	Inflow              string  `json:"inflow,omitempty"`
	Outflow             string  `json:"outflow,omitempty"`
	NetRetained         string  `json:"net_retained,omitempty"`
	SweepRatio          float64 `json:"sweep_ratio"`
	HoldingDurationHours float64 `json:"holding_duration_hours,omitempty"`
	ActivityHours       int64   `json:"activity_hours,omitempty"`
	ContractRatio       float64 `json:"contract_ratio"`
	TokenDiversity      int64   `json:"token_diversity,omitempty"`
	IsContract          bool    `json:"is_contract"`
	RiskScore           float64 `json:"risk_score"`
	FirstSeen           string  `json:"first_seen,omitempty"`
	LastSeen            string  `json:"last_seen,omitempty"`
	Recent24h           int64   `json:"recent_24h"`
	Recent7d            int64   `json:"recent_7d"`
	Recent30d           int64   `json:"recent_30d"`
	DormancyScore       float64 `json:"dormancy_score"`
}

// AddressIntelligenceEntry 是地址智能条目（设计 §33）。
type AddressIntelligenceEntry struct {
	ChainID    int64           `json:"chain_id"`
	ChainKey   string          `json:"chain_key"`
	Address    string          `json:"address"`
	Labels     []AddressLabel  `json:"labels,omitempty"`
	ClusterIDs []string        `json:"cluster_ids,omitempty"`
	EntityIDs  []string        `json:"entity_ids,omitempty"`
	Feature    *AddressFeature `json:"feature,omitempty"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// ConflictEntry 标签冲突（设计 §43-§44，不静默覆盖）。
type ConflictEntry struct {
	ID        string    `json:"id"`
	Address   string    `json:"address"`
	SourceA   string    `json:"source_a"`
	SourceB   string    `json:"source_b"`
	EntityA   string    `json:"entity_a"`
	EntityB   string    `json:"entity_b"`
	CreatedAt time.Time `json:"created_at"`
	Resolved  bool      `json:"resolved"`
}

// Resolution 是地址解析结果。
type Resolution struct {
	Address         string              `json:"address"`
	ChainKey        string              `json:"chain_key"`
	ChainID         int64               `json:"chain_id"`
	Entity          *Entity             `json:"entity,omitempty"`
	Labels          []AddressLabel      `json:"labels,omitempty"`
	ClusterIDs      []string            `json:"cluster_ids,omitempty"`
	Confidence      float64             `json:"confidence"`
	ConfidenceTier  string              `json:"confidence_tier"`
	Evidence        []EvidenceRef       `json:"evidence,omitempty"`
	Conflicts       []*ConflictEntry    `json:"conflicts,omitempty"`
	Feature         *AddressFeature     `json:"feature,omitempty"`
	CacheHit        bool                `json:"cache_hit"`
	ResolvedAt      time.Time           `json:"resolved_at"`
}

// ManualLabel 案件自定义标签（设计 §45-§46，不污染全局 Entity）。
type ManualLabel struct {
	ID              string      `json:"id"`
	InvestigationID string      `json:"investigation_id"`
	ChainKey        string      `json:"chain_key"`
	Address         string      `json:"address"`
	Label           string      `json:"label"`
	Reason          string      `json:"reason,omitempty"`
	Source          LabelSource `json:"source"`
	Confidence      float64     `json:"confidence"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

// TierFor 返回置信度等级（设计 §6）。
func TierFor(confidence float64) ConfidenceTier {
	switch {
	case confidence >= 0.95:
		return TierConfirmed
	case confidence >= 0.80:
		return TierHigh
	case confidence >= 0.60:
		return TierMedium
	case confidence >= 0.40:
		return TierLow
	default:
		return TierUnverified
	}
}

// TierLabel 返回中文可信度标签（设计 §6 前端展示）。
func TierLabel(t ConfidenceTier) string {
	switch t {
	case TierConfirmed:
		return "已确认"
	case TierHigh:
		return "高可信"
	case TierMedium:
		return "中等可信"
	case TierLow:
		return "低可信"
	default:
		return "未验证"
	}
}
