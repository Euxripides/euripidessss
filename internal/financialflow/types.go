// Package financialflow implements reproducible, token-scoped FIFO retention
// and pass-through analytics. Results are behavioral metrics only; they do not
// establish ownership, collection, laundering, or any other criminal finding.
package financialflow

import "time"

const (
	RetentionAlgorithmVersion   = "retention_fifo_v1"
	PassThroughAlgorithmVersion = "pass_through_fifo_v1"
	NativeAssetID               = "native"
)

type Direction string

const (
	DirectionIn  Direction = "IN"
	DirectionOut Direction = "OUT"
)

type EventKind string

const (
	EventTransfer EventKind = "TRANSFER"
	EventGasFee   EventKind = "GAS_FEE"
)

type Event struct {
	Address          string
	Token            string
	Direction        Direction
	Kind             EventKind
	Time             time.Time
	BlockNumber      uint64
	TransactionIndex uint32
	TxHash           string
	EventIndex       string
	Amount           string
	USDValue         string
	PriceSource      string
	PriceTime        string
	SchemaVersion    uint16
}

type Snapshot struct {
	ID           string    `json:"id"`
	AsOf         time.Time `json:"as_of"`
	PriceVersion string    `json:"price_version"`
}

type WindowMetric struct {
	Window                string `json:"window"`
	Seconds               int64  `json:"seconds"`
	MaturedReceivedAmount string `json:"matured_received_amount"`
	RetainedAmount        string `json:"retained_amount"`
	MatchedTransferAmount string `json:"matched_transfer_amount"`
	GasConsumedAmount     string `json:"gas_consumed_amount"`
	PassThroughRatio      string `json:"pass_through_ratio"`
	RetentionRatio        string `json:"retention_ratio"`
	MaturedReceivedUSD    string `json:"matured_received_usd,omitempty"`
	RetainedUSD           string `json:"retained_usd,omitempty"`
	MatchedTransferUSD    string `json:"matched_transfer_usd,omitempty"`
	GasConsumedUSD        string `json:"gas_consumed_usd,omitempty"`
	AttributedUSDBasis    string `json:"attributed_usd_basis"`
	USDAmountCoverage     string `json:"usd_amount_coverage"`
	MaturedIncomingLots   uint64 `json:"matured_incoming_lots"`
	USDValuedIncomingLots uint64 `json:"usd_valued_incoming_lots"`
	USDValuesComplete     bool   `json:"usd_values_complete"`
}

type Coverage struct {
	IncomingAmount       string `json:"incoming_amount"`
	IncomingPricedAmount string `json:"incoming_priced_amount"`
	IncomingUSDCoverage  string `json:"incoming_usd_coverage"`
	OutgoingAmount       string `json:"outgoing_amount"`
	OutgoingPricedAmount string `json:"outgoing_priced_amount"`
	OutgoingUSDCoverage  string `json:"outgoing_usd_coverage"`
	IncomingEvents       uint64 `json:"incoming_events"`
	PricedIncomingEvents uint64 `json:"priced_incoming_events"`
	OutgoingEvents       uint64 `json:"outgoing_events"`
	PricedOutgoingEvents uint64 `json:"priced_outgoing_events"`
}

type TokenResult struct {
	Address                     string         `json:"address"`
	Token                       string         `json:"token"`
	NativeAsset                 bool           `json:"native_asset"`
	RetentionWindows            []WindowMetric `json:"retention_windows"`
	PassThroughWindows          []WindowMetric `json:"pass_through_windows"`
	SettlementRatio30D          string         `json:"settlement_ratio_30d"`
	SettlementRatio30DUSD       string         `json:"settlement_ratio_30d_usd,omitempty"`
	OpeningBalanceOutAmount     string         `json:"opening_balance_out_amount"`
	OpeningBalanceGasAmount     string         `json:"opening_balance_gas_amount"`
	GasFeeAmount                string         `json:"gas_fee_amount"`
	GasFeeUSD                   string         `json:"gas_fee_usd,omitempty"`
	Coverage                    Coverage       `json:"coverage"`
	RetentionAlgorithmVersion   string         `json:"retention_algorithm_version"`
	PassThroughAlgorithmVersion string         `json:"pass_through_algorithm_version"`
	Snapshot                    Snapshot       `json:"snapshot"`
	InputDigestSHA256           string         `json:"input_digest_sha256"`
	Interpretation              string         `json:"interpretation"`
}

type Report struct {
	Results []TokenResult `json:"results"`
}

type Query struct {
	ChainID uint32
	Address string
	Token   string
	From    time.Time
	To      time.Time
	MaxRows int
}

type LoadedBatch struct {
	Events         []Event
	RowsRead       int
	InputTruncated bool
}
