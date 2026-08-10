package clickhousegraph

import "time"

// Direction restricts the first-hop edges relative to the requested root.
// Deeper hops always include both directions so the neighbourhood remains
// connected and useful for tracing.
type Direction string

const (
	DirectionAll Direction = "all"
	DirectionIn  Direction = "in"
	DirectionOut Direction = "out"
)

// EgoQuery describes a bounded address-neighbourhood query.
type EgoQuery struct {
	ChainID       uint32
	RootAddress   string
	Depth         int
	EdgeLimit     int
	NodeLimit     int
	Direction     Direction
	TokenAddress  string
	ActivityTypes []string
}

// CounterpartyQuery requests a deterministic one-hop edge list.
type CounterpartyQuery struct {
	ChainID       uint32
	Address       string
	Limit         int
	Direction     Direction
	TokenAddress  string
	ActivityTypes []string
}

type Node struct {
	Address       string `json:"address"`
	Label         string `json:"label,omitempty"`
	LabelType     string `json:"label_type,omitempty"`
	EntityID      string `json:"entity_id,omitempty"`
	EntityName    string `json:"entity_name,omitempty"`
	EntityType    string `json:"entity_type,omitempty"`
	EntityRole    string `json:"entity_role,omitempty"`
	LabelSource   string `json:"label_source,omitempty"`
	Confidence    string `json:"label_confidence,omitempty"`
	Depth         int    `json:"depth"`
	IncomingEdges uint64 `json:"incoming_edges"`
	OutgoingEdges uint64 `json:"outgoing_edges"`
	EventCount    uint64 `json:"event_count"`
}

// Edge is an aggregation of canonical address_activity events. Amount is kept
// as a string to preserve Decimal256 precision through JSON.
type Edge struct {
	ID               string    `json:"id"`
	FromAddress      string    `json:"from_address"`
	ToAddress        string    `json:"to_address"`
	TokenAddress     string    `json:"token_address,omitempty"`
	ActivityType     string    `json:"activity_type"`
	Amount           string    `json:"amount"`
	EventCount       uint64    `json:"event_count"`
	TransactionCount uint64    `json:"transaction_count"`
	FirstTime        time.Time `json:"first_time"`
	LastTime         time.Time `json:"last_time"`
	SampleTxHash     string    `json:"sample_tx_hash,omitempty"`
}

type Graph struct {
	ChainID        uint32 `json:"chain_id"`
	RootAddress    string `json:"root_address"`
	RequestedDepth int    `json:"requested_depth"`
	ReachedDepth   int    `json:"reached_depth"`
	Nodes          []Node `json:"nodes"`
	Edges          []Edge `json:"edges"`
	Truncated      bool   `json:"truncated"`
}
