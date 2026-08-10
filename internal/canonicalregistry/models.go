package canonicalregistry

import "time"

type MethodRecord struct {
	MethodID           string    `json:"method_id"`
	CanonicalSignature string    `json:"canonical_signature"`
	DisplayName        string    `json:"display_name"`
	Source             string    `json:"source"`
	Confidence         string    `json:"confidence"`
	Verified           bool      `json:"verified"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type MethodResolution struct {
	MethodID            string         `json:"method_id"`
	Name                string         `json:"name"`
	DisplayName         string         `json:"display_name"`
	Confidence          string         `json:"confidence"`
	Ambiguous           bool           `json:"ambiguous"`
	CandidateSignatures []string       `json:"candidate_signatures"`
	Candidates          []MethodRecord `json:"candidates"`
}

type TokenMetadata struct {
	ChainID            uint32    `json:"chain_id"`
	ContractAddress    string    `json:"contract_address"`
	Name               string    `json:"name"`
	Symbol             string    `json:"symbol"`
	Decimals           uint8     `json:"decimals"`
	TokenStandard      string    `json:"token_standard"`
	LogoURI            string    `json:"logo_uri"`
	LogoHash           string    `json:"logo_hash"`
	LogoSource         string    `json:"logo_source"`
	Verified           bool      `json:"verified"`
	Spam               bool      `json:"spam"`
	OfficialWebsite    string    `json:"official_website"`
	FirstSeenBlock     uint64    `json:"first_seen_block"`
	FirstSeenTime      time.Time `json:"first_seen_time"`
	MetadataSource     string    `json:"metadata_source"`
	MetadataConfidence string    `json:"metadata_confidence"`
	MetadataUpdatedAt  time.Time `json:"metadata_updated_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type ABIRecord struct {
	ChainID         uint32    `json:"chain_id"`
	ContractAddress string    `json:"contract_address"`
	ABIHash         string    `json:"abi_hash"`
	ABIJSON         string    `json:"abi_json"`
	Source          string    `json:"source"`
	Verified        bool      `json:"verified"`
	ObservedAt      time.Time `json:"observed_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Entity struct {
	EntityID   string    `json:"entity_id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Website    string    `json:"website"`
	RiskLevel  string    `json:"risk_level"`
	Source     string    `json:"source"`
	Confidence string    `json:"confidence"`
	Verified   bool      `json:"verified"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type AddressLabel struct {
	ChainID      uint32    `json:"chain_id"`
	Address      string    `json:"address"`
	LabelName    string    `json:"label_name"`
	LabelType    string    `json:"label_type"`
	EntityID     *string   `json:"entity_id,omitempty"`
	EntityRole   string    `json:"entity_role"`
	Source       string    `json:"source"`
	Confidence   string    `json:"confidence"`
	Evidence     string    `json:"evidence"`
	FirstSeen    time.Time `json:"first_seen"`
	LastVerified time.Time `json:"last_verified"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type TokenPrice struct {
	ChainID         uint32    `json:"chain_id"`
	TokenAddress    string    `json:"token_address"`
	TimestampBucket time.Time `json:"timestamp_bucket"`
	PriceUSD        string    `json:"price_usd"`
	Source          string    `json:"source"`
	Confidence      string    `json:"confidence"`
	ObservedAt      time.Time `json:"observed_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ParsedEvent struct {
	ChainID           uint32    `json:"chain_id"`
	BlockNumber       uint64    `json:"block_number"`
	BlockTime         time.Time `json:"block_time"`
	TransactionHash   string    `json:"tx_hash"`
	LogIndex          uint32    `json:"log_index"`
	ContractAddress   string    `json:"contract_address"`
	Topic0            string    `json:"topic0"`
	EventName         string    `json:"event_name"`
	EventSignature    string    `json:"event_signature"`
	DecodedFields     string    `json:"decoded_fields"`
	DecoderSource     string    `json:"decoder_source"`
	DecoderConfidence string    `json:"decoder_confidence"`
	ParserVersion     string    `json:"parser_version"`
	SchemaVersion     uint16    `json:"schema_version"`
	IngestedAt        time.Time `json:"ingested_at"`
}

type SemanticJob struct {
	JobID         string     `json:"job_id"`
	JobType       string     `json:"job_type"`
	ChainID       uint32     `json:"chain_id"`
	Dataset       string     `json:"dataset"`
	FromBlock     uint64     `json:"from_block"`
	ToBlock       uint64     `json:"to_block"`
	TargetVersion string     `json:"target_version"`
	Status        string     `json:"status"`
	ProcessedRows uint64     `json:"processed_rows"`
	FailedRows    uint64     `json:"failed_rows"`
	ErrorMessage  string     `json:"error_message"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}
