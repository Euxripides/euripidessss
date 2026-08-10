// Package financialanalytics provides bounded, ClickHouse-only financial
// analytics over canonical historical USD facts and semantic registries.
package financialanalytics

import "time"

type Window string

const (
	Window24H    Window = "24H"
	Window7D     Window = "7D"
	Window30D    Window = "30D"
	Window90D    Window = "90D"
	Window1Y     Window = "1Y"
	WindowAll    Window = "ALL"
	WindowCustom Window = "CUSTOM"
)

type Query struct {
	ChainID             uint32
	Address             string
	Window              Window
	From                time.Time
	To                  time.Time
	LargeThresholdUSD   string
	EntityMinConfidence string
	Limit               int
}

// AddressUSDFlow intentionally uses nullable monetary values. A nil value
// means that no historically-priced fact existed for that bucket; it is not 0.
type AddressUSDFlow struct {
	TotalInUSD       *string `json:"total_in_usd"`
	TotalOutUSD      *string `json:"total_out_usd"`
	NetflowUSD       *string `json:"netflow_usd"`
	NativeInUSD      *string `json:"native_in_usd"`
	NativeOutUSD     *string `json:"native_out_usd"`
	StablecoinInUSD  *string `json:"stablecoin_in_usd"`
	StablecoinOutUSD *string `json:"stablecoin_out_usd"`
	TokenInUSD       *string `json:"token_in_usd"`
	TokenOutUSD      *string `json:"token_out_usd"`
}

type PriceCoverage struct {
	ActivityCount       uint64 `json:"activity_count"`
	PricedActivityCount uint64 `json:"priced_activity_count"`
	MissingPriceCount   uint64 `json:"missing_price_count"`
	CoverageRatio       string `json:"coverage_ratio"`
}

type LargeTransferStats struct {
	ThresholdUSD string  `json:"threshold_usd"`
	InCount      uint64  `json:"large_in_count"`
	OutCount     uint64  `json:"large_out_count"`
	InUSD        *string `json:"large_in_usd"`
	OutUSD       *string `json:"large_out_usd"`
}

type FinancialSummary struct {
	ChainID       uint32             `json:"chain_id"`
	Address       string             `json:"address"`
	Window        Window             `json:"window"`
	From          string             `json:"from"`
	To            string             `json:"to"`
	Flow          AddressUSDFlow     `json:"flow"`
	LargestInUSD  *string            `json:"largest_in_usd"`
	LargestOutUSD *string            `json:"largest_out_usd"`
	AverageInUSD  *string            `json:"average_in_usd"`
	AverageOutUSD *string            `json:"average_out_usd"`
	MedianInUSD   *string            `json:"median_in_usd"`
	MedianOutUSD  *string            `json:"median_out_usd"`
	FirstFunding  string             `json:"first_funding"`
	LatestFunding string             `json:"latest_funding"`
	Large         LargeTransferStats `json:"large_transfers"`
	PriceCoverage PriceCoverage      `json:"price_coverage"`
	PriceBasis    string             `json:"price_basis"`
}

type CounterpartyFinancialStat struct {
	Counterparty     string  `json:"counterparty"`
	EntityID         string  `json:"entity_id,omitempty"`
	EntityName       string  `json:"entity_name,omitempty"`
	EntityType       string  `json:"entity_type,omitempty"`
	EntityRole       string  `json:"entity_role,omitempty"`
	InUSD            *string `json:"in_usd"`
	OutUSD           *string `json:"out_usd"`
	NetflowUSD       *string `json:"netflow_usd"`
	InCount          uint64  `json:"in_count"`
	OutCount         uint64  `json:"out_count"`
	PricedCount      uint64  `json:"priced_count"`
	ActivityCount    uint64  `json:"activity_count"`
	FirstInteraction string  `json:"first_interaction"`
	LastInteraction  string  `json:"last_interaction"`
}

type EntityFinancialStat struct {
	EntityID   string  `json:"entity_id"`
	EntityName string  `json:"entity_name"`
	EntityType string  `json:"entity_type"`
	InUSD      *string `json:"in_usd"`
	OutUSD     *string `json:"out_usd"`
	NetflowUSD *string `json:"netflow_usd"`
	Count      uint64  `json:"count"`
}

type CEXFinancialStat struct {
	EntityID         string  `json:"entity_id"`
	EntityName       string  `json:"entity_name"`
	DepositUSD       *string `json:"deposit_usd"`
	WithdrawalUSD    *string `json:"withdrawal_usd"`
	NetflowUSD       *string `json:"netflow_usd"`
	DepositCount     uint64  `json:"deposit_count"`
	WithdrawalCount  uint64  `json:"withdrawal_count"`
	InteractionCount uint64  `json:"interaction_count"`
	Confidence       string  `json:"confidence"`
}

type DEXFinancialStat struct {
	SwapCount     uint64  `json:"swap_count"`
	SwapVolumeUSD *string `json:"swap_volume_usd"`
	TopProtocol   string  `json:"top_protocol"`
	CanonicalUnit string  `json:"canonical_unit"`
}

type BridgeFinancialStat struct {
	BridgeInUSD    *string `json:"bridge_in_usd"`
	BridgeOutUSD   *string `json:"bridge_out_usd"`
	BridgeInCount  uint64  `json:"bridge_in_count"`
	BridgeOutCount uint64  `json:"bridge_out_count"`
	TopBridge      string  `json:"top_bridge"`
}
