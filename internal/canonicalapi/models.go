package canonicalapi

import (
	"encoding/json"
	"time"
)

// MethodDTO is registry-backed method intelligence. CandidateSignatures is
// populated when one selector has conflicting registry entries; callers must
// not pick one candidate arbitrarily.
type MethodDTO struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Display             string   `json:"display"`
	Confidence          string   `json:"confidence"`
	Source              string   `json:"source"`
	CandidateSignatures []string `json:"candidate_signatures,omitempty"`
}

type AddressDTO struct {
	Address         string `json:"address"`
	Label           string `json:"label,omitempty"`
	LabelType       string `json:"label_type,omitempty"`
	EntityID        string `json:"entity_id,omitempty"`
	EntityName      string `json:"entity_name,omitempty"`
	EntityType      string `json:"entity_type,omitempty"`
	EntityRole      string `json:"entity_role,omitempty"`
	LabelSource     string `json:"label_source,omitempty"`
	LabelConfidence string `json:"label_confidence,omitempty"`
}

type TokenDTO struct {
	ChainID            uint32    `json:"chain_id"`
	Contract           string    `json:"contract"`
	Name               string    `json:"name"`
	Symbol             string    `json:"symbol"`
	Decimals           uint8     `json:"decimals"`
	Standard           string    `json:"standard"`
	LogoURI            string    `json:"logo_uri,omitempty"`
	LogoHash           string    `json:"logo_hash,omitempty"`
	LogoSource         string    `json:"logo_source,omitempty"`
	Verified           bool      `json:"verified"`
	Spam               bool      `json:"spam"`
	OfficialWebsite    string    `json:"official_website,omitempty"`
	MetadataSource     string    `json:"metadata_source"`
	MetadataConfidence string    `json:"metadata_confidence"`
	MetadataUpdatedAt  time.Time `json:"metadata_updated_at"`
}

type AmountDTO struct {
	Raw       string `json:"raw"`
	Decimal   string `json:"decimal"`
	Formatted string `json:"formatted"`
}

// USDDTO always describes the historical price used for a historical event.
// There is intentionally no field for a current price.
type USDDTO struct {
	USDValue        string    `json:"usd_value"`
	PriceUSD        string    `json:"price_usd,omitempty"`
	PriceTime       time.Time `json:"price_time,omitempty"`
	PriceSource     string    `json:"price_source"`
	PriceConfidence string    `json:"price_confidence"`
}

type ProvenanceDTO struct {
	SourceProvider    string    `json:"source_provider"`
	SourceType        string    `json:"source_type"`
	DownloadJobID     string    `json:"download_job_id,omitempty"`
	SourceRangeID     string    `json:"source_range_id,omitempty"`
	ParserVersion     string    `json:"parser_version,omitempty"`
	NormalizerVersion string    `json:"normalizer_version,omitempty"`
	SchemaVersion     uint32    `json:"schema_version"`
	IngestedAt        time.Time `json:"ingested_at"`
}

type CanonicalTokenTransfer struct {
	LogIndex   uint32        `json:"log_index"`
	From       AddressDTO    `json:"from"`
	To         AddressDTO    `json:"to"`
	Token      TokenDTO      `json:"token"`
	Amount     AmountDTO     `json:"amount"`
	USD        *USDDTO       `json:"usd,omitempty"`
	Standard   string        `json:"standard"`
	Provenance ProvenanceDTO `json:"provenance"`
}

type CanonicalInternalTransaction struct {
	TraceAddress     string        `json:"trace_address"`
	TraceIndex       uint32        `json:"trace_index"`
	CallType         string        `json:"call_type"`
	Depth            uint16        `json:"depth"`
	ParentTraceIndex *uint32       `json:"parent_trace_index,omitempty"`
	From             AddressDTO    `json:"from"`
	To               AddressDTO    `json:"to"`
	Amount           AmountDTO     `json:"amount"`
	Input            string        `json:"input"`
	Output           string        `json:"output"`
	Gas              uint64        `json:"gas"`
	GasUsed          uint64        `json:"gas_used"`
	Success          bool          `json:"success"`
	Error            string        `json:"error,omitempty"`
	Provenance       ProvenanceDTO `json:"provenance"`
}

type CanonicalContractCreation struct {
	Creator               AddressDTO    `json:"creator"`
	Factory               *AddressDTO   `json:"factory,omitempty"`
	Contract              AddressDTO    `json:"contract"`
	CreationType          string        `json:"creation_type"`
	InitCodeHash          string        `json:"init_code_hash,omitempty"`
	RuntimeBytecodeHash   string        `json:"runtime_bytecode_hash,omitempty"`
	IsProxy               bool          `json:"is_proxy"`
	ProxyType             string        `json:"proxy_type,omitempty"`
	ImplementationAddress *AddressDTO   `json:"implementation,omitempty"`
	TokenDetected         bool          `json:"token_detected"`
	TokenStandard         string        `json:"token_standard,omitempty"`
	Provenance            ProvenanceDTO `json:"provenance"`
}

type CanonicalParsedEvent struct {
	LogIndex          uint32          `json:"log_index"`
	Contract          AddressDTO      `json:"contract"`
	Topic0            string          `json:"topic0"`
	Name              string          `json:"name"`
	Signature         string          `json:"signature"`
	DecodedFields     json.RawMessage `json:"decoded_fields,omitempty"`
	DecoderSource     string          `json:"decoder_source"`
	DecoderConfidence string          `json:"decoder_confidence"`
	ParserVersion     string          `json:"parser_version,omitempty"`
	SchemaVersion     uint32          `json:"schema_version"`
}

type CanonicalTransaction struct {
	ChainID              uint32                         `json:"chain_id"`
	BlockNumber          uint64                         `json:"block_number"`
	BlockHash            string                         `json:"block_hash"`
	BlockTime            time.Time                      `json:"block_time"`
	TxHash               string                         `json:"tx_hash"`
	TxIndex              uint32                         `json:"tx_index"`
	From                 AddressDTO                     `json:"from"`
	To                   *AddressDTO                    `json:"to,omitempty"`
	Nonce                uint64                         `json:"nonce"`
	Value                AmountDTO                      `json:"value"`
	Input                string                         `json:"input"`
	Method               MethodDTO                      `json:"method"`
	TxType               string                         `json:"tx_type"`
	GasLimit             uint64                         `json:"gas_limit"`
	GasUsed              uint64                         `json:"gas_used"`
	GasPrice             string                         `json:"gas_price,omitempty"`
	EffectiveGasPrice    string                         `json:"effective_gas_price,omitempty"`
	FeeNative            AmountDTO                      `json:"fee_native"`
	FeeUSD               *USDDTO                        `json:"fee_usd,omitempty"`
	Status               string                         `json:"status"`
	RawStatus            string                         `json:"raw_status,omitempty"`
	StatusSource         string                         `json:"status_source"`
	IsContractCreation   bool                           `json:"is_contract_creation"`
	CreatedContract      *AddressDTO                    `json:"created_contract,omitempty"`
	ErrorMessage         string                         `json:"error_message,omitempty"`
	TokenTransfers       []CanonicalTokenTransfer       `json:"token_transfers"`
	InternalTransactions []CanonicalInternalTransaction `json:"internal_transactions"`
	ContractCreation     *CanonicalContractCreation     `json:"contract_creation,omitempty"`
	ParsedEvents         []CanonicalParsedEvent         `json:"parsed_events"`
	Provenance           ProvenanceDTO                  `json:"provenance"`
}

type CanonicalActivity struct {
	ChainID      uint32        `json:"chain_id"`
	Address      AddressDTO    `json:"address"`
	Counterparty *AddressDTO   `json:"counterparty,omitempty"`
	Direction    string        `json:"direction"`
	ActivityType string        `json:"activity_type"`
	BlockNumber  uint64        `json:"block_number"`
	BlockTime    time.Time     `json:"block_time"`
	TxHash       string        `json:"tx_hash"`
	EventIndex   string        `json:"event_index"`
	Token        *TokenDTO     `json:"token,omitempty"`
	Amount       AmountDTO     `json:"amount"`
	USD          *USDDTO       `json:"usd,omitempty"`
	Method       MethodDTO     `json:"method"`
	Status       string        `json:"status"`
	Provenance   ProvenanceDTO `json:"provenance"`
}

type ActivityQuery struct {
	ChainID uint32
	Address string
	Limit   int
}
