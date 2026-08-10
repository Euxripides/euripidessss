package financialpnl

import "time"

const (
	AlgorithmVersion = "pnl_fifo_v1"
	SnapshotVersion  = "position_snapshot_v1"
)

type EventType string

const (
	EventDEXBuy      EventType = "DEX_BUY"
	EventDEXSell     EventType = "DEX_SELL"
	EventKnownBuy    EventType = "KNOWN_BUY"
	EventKnownSell   EventType = "KNOWN_SELL"
	EventTransferIn  EventType = "TRANSFER_IN"
	EventTransferOut EventType = "TRANSFER_OUT"
	EventAirdrop     EventType = "AIRDROP"
	EventMint        EventType = "MINT"
	EventBurn        EventType = "BURN"
	EventBridgeIn    EventType = "BRIDGE_IN"
	EventBridgeOut   EventType = "BRIDGE_OUT"
	EventUnknown     EventType = "UNKNOWN"
)

type PositionEvent struct {
	Time                time.Time
	BlockNumber         uint64
	TransactionHash     string
	EventIndex          uint32
	Type                EventType
	Amount              string
	USDValue            *string
	GasUSD              *string
	SemanticSource      string
	SemanticConfidence  string
	PriceVersion        string
	DataSnapshotVersion string
}

type Price struct {
	USD        string
	Time       time.Time
	Source     string
	Confidence string
	Version    string
}

type Lot struct {
	AcquiredTime     time.Time `json:"acquired_time"`
	AcquiredAmount   string    `json:"acquired_amount"`
	RemainingAmount  string    `json:"remaining_amount"`
	CostUSD          *string   `json:"cost_usd"`
	RemainingCostUSD *string   `json:"remaining_cost_usd"`
	SourceTx         string    `json:"source_tx"`
	SourceType       EventType `json:"source_type"`
	CostBasisStatus  string    `json:"cost_basis_status"`
}

type Result struct {
	ChainID                    uint32     `json:"chain_id"`
	Address                    string     `json:"address"`
	Token                      string     `json:"token"`
	AsOf                       time.Time  `json:"as_of"`
	RealizedPnLUSD             string     `json:"realized_pnl_usd"`
	RealizedProceedsCoveredUSD string     `json:"realized_proceeds_covered_usd"`
	RealizedCostBasisUSD       string     `json:"realized_cost_basis_usd"`
	RealizedGasUSD             string     `json:"realized_gas_usd"`
	SoldAmount                 string     `json:"sold_amount"`
	KnownSoldAmount            string     `json:"known_sold_amount"`
	KnownCostBasisRatio        string     `json:"known_cost_basis_ratio"`
	RealizedPnLStatus          string     `json:"realized_pnl_status"`
	RealizedPnLScope           string     `json:"realized_pnl_scope"`
	FinancialConfidence        string     `json:"financial_confidence"`
	PositionAmount             string     `json:"position_amount"`
	KnownPositionAmount        string     `json:"known_position_amount"`
	RemainingKnownCostUSD      string     `json:"remaining_known_cost_usd"`
	PositionMarketValueUSD     *string    `json:"position_market_value_usd"`
	KnownUnrealizedPnLUSD      *string    `json:"known_unrealized_pnl_usd"`
	UnrealizedCoverage         string     `json:"unrealized_cost_basis_coverage"`
	CurrentPriceUSD            *string    `json:"current_price_usd"`
	CurrentPriceTime           *time.Time `json:"current_price_time"`
	CurrentPriceSource         string     `json:"current_price_source"`
	CurrentPriceStatus         string     `json:"current_price_status"`
	Lots                       []Lot      `json:"lots"`
	AlgorithmVersion           string     `json:"algorithm_version"`
	PriceVersion               string     `json:"price_version"`
	HistoricalPriceVersions    []string   `json:"historical_price_versions"`
	CurrentPriceVersion        string     `json:"current_price_version"`
	DataSnapshotVersion        string     `json:"data_snapshot_version"`
	SnapshotVersion            string     `json:"snapshot_version"`
}

type Query struct {
	ChainID uint32
	Address string
	Token   string
	AsOf    time.Time
}
