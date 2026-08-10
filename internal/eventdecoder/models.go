package eventdecoder

import (
	"context"
	"encoding/json"
)

const (
	SourceVerifiedABI = "VERIFIED_ABI"
	SourceProtocolABI = "PROTOCOL_ABI"
	SourceLocalABI    = "LOCAL_ABI"
	SourceTopic0      = "TOPIC0_REGISTRY"
	SourceRaw         = "RAW"

	ConfidenceHigh    = "HIGH"
	ConfidenceMedium  = "MEDIUM"
	ConfidenceLow     = "LOW"
	ConfidenceUnknown = "UNKNOWN"
)

// Log is the raw, lossless EVM log input accepted by Decoder.
type Log struct {
	ChainID         uint64   `json:"chain_id"`
	Contract        string   `json:"contract_address"`
	TransactionHash string   `json:"tx_hash"`
	LogIndex        uint32   `json:"log_index"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
}

type Input struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Indexed bool   `json:"indexed"`
}

type EventDefinition struct {
	Name       string  `json:"name"`
	Signature  string  `json:"signature"`
	Topic0     string  `json:"topic0"`
	Inputs     []Input `json:"inputs"`
	Source     string  `json:"source"`
	Confidence string  `json:"confidence"`
}

type Query struct {
	ChainID  uint64
	Contract string
	Topic0   string
}

// Registry is deliberately read-only and injectable. Implementations may be
// backed by verified ABI storage, protocol definitions, local files or topic0
// registries, but must return all matching candidates so conflicts stay visible.
type Registry interface {
	LookupEvent(context.Context, Query) ([]EventDefinition, error)
}

type RegistryFunc func(context.Context, Query) ([]EventDefinition, error)

func (f RegistryFunc) LookupEvent(ctx context.Context, query Query) ([]EventDefinition, error) {
	return f(ctx, query)
}

type Result struct {
	EventName           string         `json:"event_name"`
	EventSignature      string         `json:"event_signature"`
	DecodedFields       map[string]any `json:"decoded_fields"`
	DecoderSource       string         `json:"decoder_source"`
	DecoderConfidence   string         `json:"decoder_confidence"`
	Ambiguous           bool           `json:"ambiguous"`
	CandidateSignatures []string       `json:"candidate_signatures,omitempty"`
	DecodeError         string         `json:"decode_error,omitempty"`
	Raw                 RawEvent       `json:"raw"`
}

type RawEvent struct {
	Topics []string `json:"topics"`
	Data   string   `json:"data"`
}

func (r Result) DecodedFieldsJSON() string {
	b, err := json.Marshal(r.DecodedFields)
	if err != nil {
		return "{}"
	}
	return string(b)
}

type SemanticContext struct {
	Protocol           string `json:"protocol"`
	Router             string `json:"router"`
	Pool               string `json:"pool"`
	Trader             string `json:"trader"`
	Token0             string `json:"token0"`
	Token1             string `json:"token1"`
	Bridge             string `json:"bridge"`
	SourceChain        string `json:"source_chain"`
	DestinationChain   string `json:"destination_chain"`
	SourceAddress      string `json:"source_address"`
	DestinationAddress string `json:"destination_address"`
	Token              string `json:"token"`
	USDValue           string `json:"usd_value,omitempty"`
}

type DEXSwap struct {
	Type      string `json:"type"`
	TxHash    string `json:"tx_hash"`
	Protocol  string `json:"protocol"`
	Router    string `json:"router"`
	Pool      string `json:"pool"`
	Trader    string `json:"trader"`
	TokenIn   string `json:"token_in"`
	AmountIn  string `json:"amount_in"`
	TokenOut  string `json:"token_out"`
	AmountOut string `json:"amount_out"`
	USDValue  string `json:"usd_value,omitempty"`
}

type BridgeEvent struct {
	Type               string `json:"type"`
	TxHash             string `json:"tx_hash"`
	Bridge             string `json:"bridge"`
	SourceChain        string `json:"source_chain"`
	DestinationChain   string `json:"destination_chain"`
	SourceAddress      string `json:"source_address"`
	DestinationAddress string `json:"destination_address"`
	Token              string `json:"token"`
	Amount             string `json:"amount"`
	USDValue           string `json:"usd_value,omitempty"`
}

type SemanticResult struct {
	ActivityType string       `json:"activity_type"`
	DEXSwap      *DEXSwap     `json:"dex_swap,omitempty"`
	Bridge       *BridgeEvent `json:"bridge,omitempty"`
}

// CanonicalCall intentionally keeps ValueRaw="0" calls: call relationships
// remain evidence even when no native asset moves.
type CanonicalCall struct {
	FromAddress string `json:"from_address"`
	ToAddress   string `json:"to_address"`
	ValueRaw    string `json:"value_raw"`
	Input       string `json:"input"`
	CallType    string `json:"call_type"`
	Success     bool   `json:"success"`
}
