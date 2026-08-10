// Package semanticquality measures whether ClickHouse data is semantically
// complete enough to be consumed without frontend inference or external RPCs.
package semanticquality

import "time"

// Coverage is an auditable ratio. Unknown is always part of Denominator and
// Percentage is Numerator / Denominator; consumers must not silently exclude
// unknown records from the denominator.
type Coverage struct {
	Numerator   uint64  `json:"numerator"`
	Denominator uint64  `json:"denominator"`
	Percentage  float64 `json:"percentage"`
	Unknown     uint64  `json:"unknown"`
	Available   bool    `json:"available"`
	LastUpdated string  `json:"last_updated,omitempty"`
}

type DatasetQuality struct {
	Dataset     string `json:"dataset"`
	Rows        uint64 `json:"rows"`
	LastUpdated string `json:"last_updated,omitempty"`
}

type SemanticCompleteness struct {
	ChainID         uint32    `json:"chain_id"`
	Overall         Coverage  `json:"overall"`
	Status          Coverage  `json:"transaction_status"`
	Method          Coverage  `json:"transaction_method"`
	TokenMetadata   Coverage  `json:"token_metadata"`
	TokenDecimals   Coverage  `json:"token_decimals"`
	TokenLogo       Coverage  `json:"token_logo"`
	ContractCreator Coverage  `json:"contract_creator"`
	ContractABI     Coverage  `json:"contract_abi"`
	EntityLabel     Coverage  `json:"entity_label"`
	EventDecode     Coverage  `json:"event_decode"`
	HistoricalPrice Coverage  `json:"historical_price"`
	GeneratedAt     time.Time `json:"generated_at"`
}

type DataQuality struct {
	ChainID     uint32           `json:"chain_id"`
	TotalRows   uint64           `json:"total_rows"`
	Datasets    []DatasetQuality `json:"datasets"`
	Status      Coverage         `json:"status"`
	Method      Coverage         `json:"method"`
	EntityLabel Coverage         `json:"entity_label"`
	GeneratedAt time.Time        `json:"generated_at"`
}

type TokenQuality struct {
	ChainID         uint32   `json:"chain_id"`
	KnownTokens     uint64   `json:"known_tokens"`
	Verified        uint64   `json:"verified"`
	Unverified      uint64   `json:"unverified"`
	SpamTokens      uint64   `json:"spam_tokens"`
	MissingSymbol   uint64   `json:"missing_symbol"`
	MissingDecimals uint64   `json:"missing_decimals"`
	MissingLogo     uint64   `json:"missing_logo"`
	Metadata        Coverage `json:"metadata_coverage"`
	Decimals        Coverage `json:"decimals_coverage"`
	Logo            Coverage `json:"logo_coverage"`
	LastUpdated     string   `json:"last_updated,omitempty"`
}

type ContractQuality struct {
	ChainID             uint32   `json:"chain_id"`
	Contracts           uint64   `json:"contracts"`
	Creator             Coverage `json:"creator_coverage"`
	CreationTransaction Coverage `json:"creation_tx_coverage"`
	ProxyDetected       uint64   `json:"proxy_detected"`
	ImplementationKnown uint64   `json:"implementation_known"`
	ABI                 Coverage `json:"abi_coverage"`
	Verified            uint64   `json:"verified"`
	TokenDetected       uint64   `json:"token_detected"`
	LastUpdated         string   `json:"last_updated,omitempty"`
}

type DecoderQuality struct {
	ChainID               uint32   `json:"chain_id"`
	TransactionsWithInput uint64   `json:"transactions_with_input"`
	KnownMethod           uint64   `json:"known_method"`
	UnknownMethod         uint64   `json:"unknown_method"`
	IndexedEvents         uint64   `json:"indexed_events"`
	DecodedEvents         uint64   `json:"decoded_events"`
	UnknownTopic0         uint64   `json:"unknown_topic0"`
	ABIDecodeFailures     uint64   `json:"abi_decode_failures"`
	Method                Coverage `json:"method_coverage"`
	Events                Coverage `json:"event_coverage"`
	Scope                 string   `json:"scope"`
	LastUpdated           string   `json:"last_updated,omitempty"`
}

type PriceQuality struct {
	ChainID                  uint32   `json:"chain_id"`
	TransfersRequiringPrice  uint64   `json:"transfers_requiring_price"`
	Priced                   uint64   `json:"priced"`
	HistoricalPrice          uint64   `json:"historical_price"`
	FallbackPrice            uint64   `json:"fallback_price"`
	NoPrice                  uint64   `json:"no_price"`
	PriceCoverage            Coverage `json:"price_coverage"`
	HistoricalPriceCoverage  Coverage `json:"historical_price_coverage"`
	PriceProvenanceAvailable bool     `json:"price_provenance_available"`
	LastUpdated              string   `json:"last_updated,omitempty"`
}

type Report struct {
	ChainID              uint32               `json:"chain_id"`
	SemanticCompleteness SemanticCompleteness `json:"semantic_completeness"`
	Data                 DataQuality          `json:"data_quality"`
	Token                TokenQuality         `json:"token_quality"`
	Contract             ContractQuality      `json:"contract_quality"`
	Decoder              DecoderQuality       `json:"decoder_quality"`
	Price                PriceQuality         `json:"price_quality"`
	GeneratedAt          time.Time            `json:"generated_at"`
}
