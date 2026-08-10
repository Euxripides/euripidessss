// Package semanticanalytics provides bounded, ClickHouse-only derived
// behavioral analytics over the canonical address_activity asset.
package semanticanalytics

import "time"

type AddressQuery struct {
	ChainID uint32
	Address string
	From    time.Time
	To      time.Time
}

type CounterpartyQuery struct {
	AddressQuery
	Limit int
}

type SnapshotQuery struct {
	ChainID uint32
	Address string
	From    time.Time
	AsOf    time.Time
}

type AddressSummaryV2 struct {
	ChainID                uint32 `json:"chain_id"`
	Address                string `json:"address"`
	From                   string `json:"from"`
	To                     string `json:"to"`
	TxCount                uint64 `json:"tx_count"`
	InCount                uint64 `json:"in_count"`
	OutCount               uint64 `json:"out_count"`
	TokenTransferCount     uint64 `json:"token_transfer_count"`
	InternalTransferCount  uint64 `json:"internal_transfer_count"`
	UniqueCounterparties   uint64 `json:"unique_counterparties"`
	FirstSeen              string `json:"first_seen"`
	LastSeen               string `json:"last_seen"`
	ActiveDays             uint64 `json:"active_days"`
	TotalInUSD             string `json:"total_in_usd"`
	TotalOutUSD            string `json:"total_out_usd"`
	NetflowUSD             string `json:"netflow_usd"`
	LargestInUSD           string `json:"largest_in_usd"`
	LargestOutUSD          string `json:"largest_out_usd"`
	CEXInUSD               string `json:"cex_in_usd"`
	CEXOutUSD              string `json:"cex_out_usd"`
	DEXVolumeUSD           string `json:"dex_volume_usd"`
	BridgeVolumeUSD        string `json:"bridge_volume_usd"`
	ContractCreatedCount   uint64 `json:"contract_created_count"`
	USDValuedActivityCount uint64 `json:"usd_valued_activity_count"`
	ActivityCount          uint64 `json:"activity_count"`
	PriceBasis             string `json:"price_basis"`
}

type CounterpartyV2 struct {
	Address          string `json:"address"`
	Entity           string `json:"entity"`
	Label            string `json:"label"`
	ActivityCount    uint64 `json:"activity_count"`
	TransactionCount uint64 `json:"transaction_count"`
	IncomingUSD      string `json:"incoming_usd"`
	OutgoingUSD      string `json:"outgoing_usd"`
	NetflowUSD       string `json:"netflow_usd"`
	AmountUSD        string `json:"amount_usd"`
	Share            string `json:"share"`
	FirstSeen        string `json:"first_seen"`
	LastSeen         string `json:"last_seen"`
}

type CounterpartyStatisticsV2 struct {
	ChainID         uint32           `json:"chain_id"`
	Address         string           `json:"address"`
	TopSources      []CounterpartyV2 `json:"top_sources_by_amount"`
	TopDestinations []CounterpartyV2 `json:"top_destinations_by_amount"`
	TopByCount      []CounterpartyV2 `json:"top_counterparties_by_count"`
	TopByAbsNetflow []CounterpartyV2 `json:"top_netflow_counterparties"`
	PriceBasis      string           `json:"price_basis"`
}

type DirectionConcentration struct {
	Top1  string `json:"top1"`
	Top5  string `json:"top5"`
	Top10 string `json:"top10"`
	Total string `json:"total_usd"`
}

type Concentration struct {
	Inflow     DirectionConcentration `json:"inflow"`
	Outflow    DirectionConcentration `json:"outflow"`
	PriceBasis string                 `json:"price_basis"`
}

type Retention struct {
	ReceivedUSD string `json:"received_usd"`
	Retained1H  string `json:"retained_1h"`
	Retained6H  string `json:"retained_6h"`
	Retained24H string `json:"retained_24h"`
	Retained7D  string `json:"retained_7d"`
	Retained30D string `json:"retained_30d"`
	AsOf        string `json:"as_of"`
	Method      string `json:"method"`
	PriceBasis  string `json:"price_basis"`
}

type PassThroughWindow struct {
	Window           string `json:"window"`
	MatchedOutUSD    string `json:"matched_out_usd"`
	ReceivedUSD      string `json:"received_usd"`
	PassThroughRatio string `json:"pass_through_ratio"`
}

type FastPassThrough struct {
	Windows        []PassThroughWindow `json:"windows"`
	USDValuedIn    uint64              `json:"usd_valued_in_count"`
	USDValuedOut   uint64              `json:"usd_valued_out_count"`
	AsOf           string              `json:"as_of"`
	Method         string              `json:"method"`
	Interpretation string              `json:"interpretation"`
	PriceBasis     string              `json:"price_basis"`
}

type HistoricalSnapshot struct {
	AsOf          string           `json:"as_of"`
	Summary       AddressSummaryV2 `json:"summary"`
	Concentration Concentration    `json:"concentration"`
	Retention     Retention        `json:"retention"`
	SnapshotBasis string           `json:"snapshot_basis"`
}
