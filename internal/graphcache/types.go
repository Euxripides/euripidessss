package graphcache

import (
	"strings"
	"time"
)

// Node 是扩展结果中的地址节点（含聚合画像）。
type Node struct {
	Address          string `json:"address"`
	Type             string `json:"type,omitempty"`
	Inflow           string `json:"inflow,omitempty"`
	Outflow          string `json:"outflow,omitempty"`
	TxCount          int64  `json:"tx_count"`
	FirstSeen        string `json:"first_seen,omitempty"`
	LastSeen         string `json:"last_seen,omitempty"`
	Coverage         float64 `json:"coverage,omitempty"`
	PrefetchPriority string `json:"prefetch_priority,omitempty"`
}

// Edge 是对手方聚合边（设计 §44：Address → Counterparty Aggregate）。
type Edge struct {
	Counterparty string  `json:"counterparty"`
	Direction    string  `json:"direction"`
	Token        string  `json:"token,omitempty"`
	Inflow       string  `json:"inflow,omitempty"`
	Outflow      string  `json:"outflow,omitempty"`
	TxCount      int64   `json:"tx_count"`
	FirstSeen    string  `json:"first_seen,omitempty"`
	LastSeen     string  `json:"last_seen,omitempty"`
	PathScore    float64 `json:"path_score,omitempty"`
}

// Result 是图扩展聚合结果（设计 §9）。
type Result struct {
	Key              Key      `json:"key"`
	Nodes            []Node   `json:"nodes,omitempty"`
	Edges            []Edge   `json:"edges,omitempty"`
	TotalInflow      string   `json:"total_inflow,omitempty"`
	TotalOutflow     string   `json:"total_outflow,omitempty"`
	CounterpartyCount int     `json:"counterparty_count"`
	Coverage         float64  `json:"coverage"`
	Certification    string   `json:"certification,omitempty"`
	GeneratedAt      time.Time `json:"generated_at"`
	Source           string   `json:"source"` // cache-hit | rebuilt
}

// CacheEntry 是落盘缓存条目。
type CacheEntry struct {
	Result    Result    `json:"result"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// EdgeKey 返回边去重键。
func (e Edge) EdgeKey() string {
	return strings.ToLower(e.Counterparty + "|" + e.Direction + "|" + e.Token)
}

// NodeKey 返回节点去重键。
func (n Node) NodeKey() string {
	return strings.ToLower(n.Address)
}

