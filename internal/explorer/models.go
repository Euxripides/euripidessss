package explorer

import (
	"strings"
	"time"
)

// ActivityKind is a closed set of explorer activity views. Values are API-safe
// and deliberately do not expose ClickHouse table or column names.
type ActivityKind string

const (
	ActivityAll              ActivityKind = "all"
	ActivityTransactions     ActivityKind = "transactions"
	ActivityTokenTransfers   ActivityKind = "token_transfers"
	ActivityInternal         ActivityKind = "internal_transactions"
	ActivityContractCreation ActivityKind = "contract_creations"
)

type AddressSummary struct {
	ChainID                  uint32    `json:"chain_id"`
	Address                  string    `json:"address"`
	DataStatus               string    `json:"data_status,omitempty"`
	AddressType              string    `json:"address_type"`
	FirstSeenTime            time.Time `json:"first_seen_time"`
	LastSeenTime             time.Time `json:"last_seen_time"`
	TransactionCount         uint64    `json:"transaction_count"`
	IncomingTransactionCount uint64    `json:"incoming_transaction_count"`
	OutgoingTransactionCount uint64    `json:"outgoing_transaction_count"`
	TokenTransferCount       uint64    `json:"token_transfer_count"`
	InternalTransactionCount uint64    `json:"internal_transaction_count"`
	NFTTransferCount         uint64    `json:"nft_transfer_count"`
	ContractCreatedCount     uint64    `json:"contract_created_count"`
	UniqueCounterpartyCount  uint64    `json:"unique_counterparty_count"`
	NativeReceived           string    `json:"native_received"`
	NativeSent               string    `json:"native_sent"`
	NativeNetflow            string    `json:"native_netflow"`
	USDReceived              string    `json:"usd_received"`
	USDSent                  string    `json:"usd_sent"`
	USDNetflow               string    `json:"usd_netflow"`
	ActiveDays               uint32    `json:"active_days"`
	MaxSingleInUSD           string    `json:"max_single_in_usd"`
	MaxSingleOutUSD          string    `json:"max_single_out_usd"`
	TopCounterparty          string    `json:"top_counterparty"`
	CEXInteractionCount      uint64    `json:"cex_interaction_count"`
	DEXInteractionCount      uint64    `json:"dex_interaction_count"`
	BridgeInteractionCount   uint64    `json:"bridge_interaction_count"`
	RiskScore                float64   `json:"risk_score"`
	UpdatedAt                time.Time `json:"updated_at"`
}

// DataStatusNoData 表示地址合法但当前无任何链上活动数据。
const DataStatusNoData = "NO_DATA"

// NoDataSummary 返回合法的空 summary（200 语义），避免把“查询成功、结果为空”
// 与资源不存在混为一谈（404 会触发浏览器资源加载错误）。
func NoDataSummary(chainID uint32, address string) AddressSummary {
	return AddressSummary{
		ChainID:          chainID,
		Address:          strings.ToLower(address),
		DataStatus:       DataStatusNoData,
		TransactionCount: 0,
		ActiveDays:       0,
	}
}

type AddressProfile struct {
	ChainID uint32         `json:"chain_id"`
	Address string         `json:"address"`
	Summary AddressSummary `json:"summary"`
}

type Activity struct {
	ChainID                uint32     `json:"chain_id"`
	Address                string     `json:"address"`
	CounterpartyAddress    string     `json:"counterparty_address"`
	Direction              string     `json:"direction"`
	ActivityType           string     `json:"activity_type"`
	BlockNumber            uint64     `json:"block_number"`
	BlockTime              time.Time  `json:"block_time"`
	TransactionHash        string     `json:"transaction_hash"`
	EventIndex             string     `json:"event_index"`
	TokenAddress           string     `json:"token_address"`
	TokenName              string     `json:"token_name,omitempty"`
	TokenSymbol            string     `json:"token_symbol"`
	TokenLogoURI           string     `json:"token_logo_uri,omitempty"`
	TokenLogoSource        string     `json:"token_logo_source,omitempty"`
	TokenVerified          bool       `json:"token_verified"`
	TokenSpam              bool       `json:"token_spam"`
	Amount                 string     `json:"amount"`
	USDValue               *string    `json:"usd_value,omitempty"`
	PriceUSD               *string    `json:"price_usd,omitempty"`
	PriceTime              *time.Time `json:"price_time,omitempty"`
	PriceSource            string     `json:"price_source,omitempty"`
	PriceConfidence        float32    `json:"price_confidence"`
	HistoricalPriceUSDT    *string    `json:"historical_price_usdt"`
	HistoricalValueUSDT    *string    `json:"historical_value_usdt"`
	PriceTimestamp         *time.Time `json:"price_timestamp,omitempty"`
	PriceRoute             string     `json:"price_route"`
	PriceType              string     `json:"price_type"`
	PriceAgeSeconds        int64      `json:"price_age_seconds"`
	ValuationStatus        string     `json:"valuation_status"`
	MethodID               string     `json:"method_id"`
	MethodName             string     `json:"method_name"`
	Status                 string     `json:"status"`
	CounterpartyEntityType string     `json:"counterparty_entity_type"`
	CounterpartyLabel      string     `json:"counterparty_label"`
	SourceProvider         string     `json:"source_provider"`
}

type ActivityQuery struct {
	ChainID  uint32
	Address  string
	Activity ActivityKind
	PageSize int
	Cursor   string
}

type ActivityPage struct {
	Items      []Activity `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
	HasMore    bool       `json:"has_more"`
}

// CounterpartyStat is a pre-aggregated relationship between an address and one
// counterparty. Amounts remain strings so Decimal256 values never lose
// precision while crossing the JSON boundary.
type CounterpartyStat struct {
	ChainID             uint32    `json:"chain_id"`
	Address             string    `json:"address"`
	CounterpartyAddress string    `json:"counterparty_address"`
	Direction           string    `json:"direction"`
	ActivityCount       uint64    `json:"activity_count"`
	TransactionCount    uint64    `json:"transaction_count"`
	NativeAmount        string    `json:"native_amount"`
	USDValue            string    `json:"usd_value"`
	FirstSeenTime       time.Time `json:"first_seen_time"`
	LastSeenTime        time.Time `json:"last_seen_time"`
}

type DailyStat struct {
	ChainID              uint32    `json:"chain_id"`
	Address              string    `json:"address"`
	Date                 time.Time `json:"date"`
	IncomingCount        uint64    `json:"incoming_count"`
	OutgoingCount        uint64    `json:"outgoing_count"`
	IncomingNativeAmount string    `json:"incoming_native_amount"`
	OutgoingNativeAmount string    `json:"outgoing_native_amount"`
	NativeNetflow        string    `json:"native_netflow"`
	IncomingUSDValue     string    `json:"incoming_usd_value"`
	OutgoingUSDValue     string    `json:"outgoing_usd_value"`
	USDNetflow           string    `json:"usd_netflow"`
	UniqueCounterparties uint64    `json:"unique_counterparties"`
}

type DailyStatsQuery struct {
	ChainID uint32
	Address string
	From    time.Time
	To      time.Time
	Limit   int
}

type TokenMetadata struct {
	ChainID             uint32    `json:"chain_id"`
	ContractAddress     string    `json:"contract_address"`
	Name                string    `json:"name"`
	Symbol              string    `json:"symbol"`
	Decimals            uint8     `json:"decimals"`
	TokenStandard       string    `json:"token_standard"`
	LogoURI             string    `json:"logo_uri"`
	LogoSource          string    `json:"logo_source"`
	OfficialWebsite     string    `json:"official_website"`
	Verified            bool      `json:"verified"`
	Spam                bool      `json:"spam"`
	FirstSeenBlock      uint64    `json:"first_seen_block"`
	FirstSeenTime       time.Time `json:"first_seen_time"`
	LastMetadataRefresh time.Time `json:"last_metadata_refresh_at"`
}

type TransactionDetail struct {
	ChainID              uint32    `json:"chain_id"`
	BlockNumber          uint64    `json:"block_number"`
	BlockHash            string    `json:"block_hash"`
	BlockTime            time.Time `json:"block_time"`
	TransactionIndex     uint32    `json:"transaction_index"`
	TransactionHash      string    `json:"transaction_hash"`
	FromAddress          string    `json:"from_address"`
	ToAddress            string    `json:"to_address"`
	Nonce                uint64    `json:"nonce"`
	ValueRaw             string    `json:"value_raw"`
	ValueDecimal         string    `json:"value_decimal"`
	NativeSymbol         string    `json:"native_symbol"`
	Input                string    `json:"input"`
	MethodID             string    `json:"method_id"`
	MethodName           string    `json:"method_name"`
	TransactionType      string    `json:"transaction_type"`
	GasLimit             uint64    `json:"gas_limit"`
	GasUsed              uint64    `json:"gas_used"`
	TransactionFeeNative string    `json:"transaction_fee_native"`
	TransactionFeeUSD    *string   `json:"transaction_fee_usd,omitempty"`
	Status               string    `json:"status"`
	ContractCreation     bool      `json:"is_contract_creation"`
	CreatedContract      string    `json:"created_contract_address"`
	ErrorMessage         string    `json:"error_message"`
	SourceProvider       string    `json:"source_provider"`
}

type ContractDetail struct {
	ChainID               uint32    `json:"chain_id"`
	ContractAddress       string    `json:"contract_address"`
	CreatorAddress        string    `json:"creator_address"`
	CreationTxHash        string    `json:"creation_tx_hash"`
	CreationBlock         uint64    `json:"creation_block"`
	CreationTime          time.Time `json:"creation_time"`
	BytecodeHash          string    `json:"bytecode_hash"`
	RuntimeBytecodeHash   string    `json:"runtime_bytecode_hash"`
	ContractName          string    `json:"contract_name"`
	Verified              bool      `json:"is_verified"`
	Proxy                 bool      `json:"is_proxy"`
	ProxyType             string    `json:"proxy_type"`
	ImplementationAddress string    `json:"implementation_address"`
	ABIJSON               string    `json:"abi_json"`
	TokenStandard         string    `json:"token_standard"`
	FirstSeen             time.Time `json:"first_seen"`
	LastSeen              time.Time `json:"last_seen"`
	RiskFlags             []string  `json:"risk_flags"`
}
