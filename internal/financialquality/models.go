// Package financialquality reports whether canonical financial analytics have
// enough local evidence to be shown without inventing zero values.
package financialquality

import "time"

type Coverage struct {
	Numerator   uint64   `json:"numerator"`
	Denominator uint64   `json:"denominator"`
	Percentage  *float64 `json:"percentage"`
	Unknown     uint64   `json:"unknown"`
	Available   bool     `json:"available"`
	Scope       string   `json:"scope"`
}

type Window struct {
	Name  string     `json:"name"`
	Start *time.Time `json:"start"`
	End   *time.Time `json:"end"`
}

type PriceQuality struct {
	TransfersRequiringPrice uint64   `json:"transfers_requiring_price"`
	Priced                  uint64   `json:"priced"`
	HistoricalPrice         uint64   `json:"historical_price"`
	FallbackPrice           uint64   `json:"fallback_price"`
	MissingPrice            uint64   `json:"missing_price"`
	Coverage                Coverage `json:"coverage"`
	FallbackRatio           Coverage `json:"fallback_ratio"`
}

type CostBasisQuality struct {
	PositionEvents   uint64   `json:"position_events"`
	KnownCostBasis   uint64   `json:"known_cost_basis"`
	UnknownCostBasis uint64   `json:"unknown_cost_basis"`
	Coverage         Coverage `json:"coverage"`
	Status           string   `json:"status"`
	Reason           string   `json:"reason"`
}

type DecodeQuality struct {
	Candidates uint64   `json:"candidates"`
	Decoded    uint64   `json:"decoded"`
	Missing    uint64   `json:"missing"`
	Coverage   Coverage `json:"coverage"`
}

type EntityQuality struct {
	Counterparties uint64   `json:"counterparties"`
	KnownEntity    uint64   `json:"known_entity"`
	UnknownEntity  uint64   `json:"unknown_entity"`
	Coverage       Coverage `json:"coverage"`
}

type Report struct {
	ChainID      uint32           `json:"chain_id"`
	Window       Window           `json:"window"`
	Price        PriceQuality     `json:"price"`
	CostBasis    CostBasisQuality `json:"cost_basis"`
	DEXDecode    DecodeQuality    `json:"dex_decode"`
	BridgeDecode DecodeQuality    `json:"bridge_decode"`
	Entity       EntityQuality    `json:"entity"`
	LastUpdated  string           `json:"last_updated,omitempty"`
	GeneratedAt  time.Time        `json:"generated_at"`
}
