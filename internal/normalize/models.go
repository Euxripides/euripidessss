package normalize

import "time"

type TransactionReceipt struct {
	ChainKey          string `json:"chain_key"`
	ChainID           int64  `json:"chain_id"`
	TxHash            string `json:"tx_hash"`
	Status            uint64 `json:"status"`
	GasUsed           string `json:"gas_used"`
	EffectiveGasPrice string `json:"effective_gas_price"`
	ContractAddress   string `json:"contract_address,omitempty"`
	LogsCount         int    `json:"logs_count"`
}

type UnifiedTransaction struct {
	ChainKey    string    `json:"chain_key"`
	ChainID     int64     `json:"chain_id"`
	TxHash      string    `json:"tx_hash"`
	BlockNumber uint64    `json:"block_number"`
	BlockTime   time.Time `json:"block_time"`
	FromAddress string    `json:"from_address"`
	ToAddress   string    `json:"to_address"`
	ValueRaw    string    `json:"value_raw"`
	Input       string    `json:"input"`
	MethodID    string    `json:"method_id"`
	Status      uint64    `json:"status"`
	GasUsed     string    `json:"gas_used"`
	GasPrice    string    `json:"gas_price"`
	Source      string    `json:"source"`
}

type TokenMetadata struct {
	ChainKey     string    `json:"chain_key"`
	ChainID      int64     `json:"chain_id"`
	TokenAddress string    `json:"token_address"`
	Name         string    `json:"name"`
	Symbol       string    `json:"symbol"`
	Decimals     *uint8    `json:"decimals,omitempty"`
	Standard     string    `json:"standard"`
	TotalSupply  string    `json:"total_supply"`
	LogoURL      string    `json:"logo_url"`
	UpdatedAt    time.Time `json:"updated_at"`
	Source       string    `json:"source"`
}

type MethodSignature struct {
	MethodID     string `json:"method_id"`
	Signature    string `json:"signature"`
	FunctionName string `json:"function_name"`
	Category     string `json:"category"`
}

type ContractCreation struct {
	ChainKey        string    `json:"chain_key"`
	ChainID         int64     `json:"chain_id"`
	Creator         string    `json:"creator"`
	ContractAddress string    `json:"contract_address"`
	TxHash          string    `json:"tx_hash"`
	BlockNumber     uint64    `json:"block_number"`
	BlockTime       time.Time `json:"block_time"`
	CreationType    string    `json:"creation_type"`
	Status          uint64    `json:"status"`
}

type TokenTransfer struct {
	ChainKey     string    `json:"chain_key"`
	ChainID      int64     `json:"chain_id"`
	TxHash       string    `json:"tx_hash"`
	LogIndex     uint64    `json:"log_index"`
	BlockNumber  uint64    `json:"block_number"`
	BlockTime    time.Time `json:"block_time"`
	TokenAddress string    `json:"token_address"`
	FromAddress  string    `json:"from_address"`
	ToAddress    string    `json:"to_address"`
	AmountRaw    string    `json:"amount_raw"`
	Amount       string    `json:"amount"`
	Symbol       string    `json:"symbol"`
	Decimals     uint8     `json:"decimals"`
	Standard     string    `json:"standard"`
}

type NFTTransfer struct {
	ChainKey        string    `json:"chain_key"`
	ChainID         int64     `json:"chain_id"`
	TxHash          string    `json:"tx_hash"`
	LogIndex        uint64    `json:"log_index"`
	BatchIndex      int       `json:"batch_index"`
	BlockNumber     uint64    `json:"block_number"`
	BlockTime       time.Time `json:"block_time"`
	ContractAddress string    `json:"contract_address"`
	TokenID         string    `json:"token_id"`
	FromAddress     string    `json:"from_address"`
	ToAddress       string    `json:"to_address"`
	Amount          string    `json:"amount"`
	Standard        string    `json:"standard"`
}

type Trace struct {
	ChainKey    string    `json:"chain_key"`
	ChainID     int64     `json:"chain_id"`
	TxHash      string    `json:"tx_hash"`
	TraceID     string    `json:"trace_id"`
	TraceDepth  int       `json:"trace_depth"`
	BlockNumber uint64    `json:"block_number"`
	BlockTime   time.Time `json:"block_time"`
	FromAddress string    `json:"from_address"`
	ToAddress   string    `json:"to_address"`
	ValueRaw    string    `json:"value_raw"`
	CallType    string    `json:"call_type"`
	Input       string    `json:"input"`
	Output      string    `json:"output"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
}

type InternalTransaction struct {
	ChainKey    string    `json:"chain_key"`
	ChainID     int64     `json:"chain_id"`
	TxHash      string    `json:"tx_hash"`
	TraceID     string    `json:"trace_id"`
	BlockNumber uint64    `json:"block_number"`
	BlockTime   time.Time `json:"block_time"`
	FromAddress string    `json:"from_address"`
	ToAddress   string    `json:"to_address"`
	ValueRaw    string    `json:"value_raw"`
	Type        string    `json:"type"`
}

type AddressActivity struct {
	ChainKey     string    `json:"chain_key"`
	ChainID      int64     `json:"chain_id"`
	Address      string    `json:"address"`
	Counterparty string    `json:"counterparty"`
	Direction    string    `json:"direction"`
	ActivityType string    `json:"activity_type"`
	AssetType    string    `json:"asset_type"`
	AssetAddress string    `json:"asset_address,omitempty"`
	Symbol       string    `json:"symbol"`
	AmountRaw    string    `json:"amount_raw"`
	Amount       string    `json:"amount"`
	TxHash       string    `json:"tx_hash"`
	BlockTime    time.Time `json:"block_time"`
	MethodID     string    `json:"method_id"`
	TraceDepth   int       `json:"trace_depth"`
	Status       string    `json:"status"`
	Source       string    `json:"source"`
}

type AddressSummary struct {
	ChainKey             string    `json:"chain_key"`
	ChainID              int64     `json:"chain_id"`
	Address              string    `json:"address"`
	AddressType          string    `json:"address_type"`
	TxCount              int64     `json:"tx_count"`
	TokenCount           int64     `json:"token_count"`
	NFTCount             int64     `json:"nft_count"`
	ContractCount        int64     `json:"contract_count"`
	FirstActiveTime      time.Time `json:"first_active_time"`
	LastActiveTime       time.Time `json:"last_active_time"`
	TotalNativeIn        string    `json:"total_native_in"`
	TotalNativeOut       string    `json:"total_native_out"`
	UniqueCounterparties int64     `json:"unique_counterparty_count"`
}

type BalanceSnapshot struct {
	ChainKey     string    `json:"chain_key"`
	ChainID      int64     `json:"chain_id"`
	Address      string    `json:"address"`
	AssetType    string    `json:"asset_type"`
	AssetAddress string    `json:"asset_address"`
	BalanceRaw   string    `json:"balance_raw"`
	Balance      string    `json:"balance"`
	SnapshotTime time.Time `json:"snapshot_time"`
	Source       string    `json:"source"`
}
