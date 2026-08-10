package clickhouseanalytics

import "time"

type TrendPoint struct {
	Date   string `json:"date"`
	Events uint64 `json:"events"`
}

type Dashboard struct {
	ChainID          uint32       `json:"chain_id"`
	AddressCount     uint64       `json:"address_count"`
	TokenCount       uint64       `json:"token_count"`
	TransactionCount uint64       `json:"transaction_count"`
	TransferCount    uint64       `json:"transfer_count"`
	RiskAddresses    uint64       `json:"risk_addresses"`
	Trend            []TrendPoint `json:"trend"`
}

type Counterparty struct {
	Address          string `json:"address"`
	Direction        string `json:"direction"`
	ActivityCount    uint64 `json:"activity_count"`
	TransactionCount uint64 `json:"transaction_count"`
	Amount           string `json:"amount"`
	USDValue         string `json:"usd_value"`
	FirstSeenTime    string `json:"first_seen_time"`
	LastSeenTime     string `json:"last_seen_time"`
}

type DailyNetflow struct {
	Date                 string `json:"date"`
	IncomingCount        uint64 `json:"incoming_count"`
	OutgoingCount        uint64 `json:"outgoing_count"`
	IncomingAmount       string `json:"incoming_amount"`
	OutgoingAmount       string `json:"outgoing_amount"`
	Netflow              string `json:"netflow"`
	IncomingUSD          string `json:"incoming_usd"`
	OutgoingUSD          string `json:"outgoing_usd"`
	NetflowUSD           string `json:"netflow_usd"`
	UniqueCounterparties uint64 `json:"unique_counterparties"`
}

type TokenDistribution struct {
	TokenAddress  string `json:"token_address"`
	TokenSymbol   string `json:"token_symbol"`
	ActivityCount uint64 `json:"activity_count"`
	Incoming      string `json:"incoming"`
	Outgoing      string `json:"outgoing"`
	Netflow       string `json:"netflow"`
	USDValue      string `json:"usd_value"`
}

type VolumeStat struct {
	Address           string `json:"address"`
	CounterpartyCount uint64 `json:"counterparty_count"`
	TransactionCount  uint64 `json:"transaction_count"`
	Amount            string `json:"amount"`
	USDValue          string `json:"usd_value"`
}

type InOutVolume struct {
	IncomingCount  uint64 `json:"incoming_count"`
	OutgoingCount  uint64 `json:"outgoing_count"`
	IncomingAmount string `json:"incoming_amount"`
	OutgoingAmount string `json:"outgoing_amount"`
	IncomingUSD    string `json:"incoming_usd"`
	OutgoingUSD    string `json:"outgoing_usd"`
}

type AllTimeStats struct {
	FirstActivityTime    string `json:"first_activity_time"`
	LastActivityTime     string `json:"last_activity_time"`
	EventCount           uint64 `json:"event_count"`
	TransactionCount     uint64 `json:"transaction_count"`
	ContractCount        uint64 `json:"contract_count"`
	TokenCount           uint64 `json:"token_count"`
	IncomingCount        uint64 `json:"incoming_count"`
	OutgoingCount        uint64 `json:"outgoing_count"`
	TotalIn              string `json:"total_in"`
	TotalOut             string `json:"total_out"`
	Netflow              string `json:"netflow"`
	ActiveDays           uint64 `json:"active_days"`
	UniqueCounterparties uint64 `json:"unique_counterparties"`
}

type AddressQuery struct {
	ChainID uint32
	Address string
	From    time.Time
	To      time.Time
	Limit   int
}

type AddressAnalytics struct {
	ChainID           uint32              `json:"chain_id"`
	Address           string              `json:"address"`
	AllTime           AllTimeStats        `json:"all_time"`
	TopCounterparties []Counterparty      `json:"top_counterparties"`
	DailyNetflow      []DailyNetflow      `json:"daily_netflow"`
	TokenDistribution []TokenDistribution `json:"token_distribution"`
}

// RiskResult is explicitly a deterministic screening result, not an identity
// label or a finding. Rules documents exactly which observable triggered.
type RiskResult struct {
	Address                   string   `json:"address"`
	RiskScore                 float64  `json:"risk_score"`
	RiskLevel                 string   `json:"risk_level"`
	RiskReason                string   `json:"risk_reason"`
	Rules                     []string `json:"rules"`
	TransactionFrequency      float64  `json:"transaction_frequency"`
	CounterpartyConcentration float64  `json:"counterparty_concentration"`
	UniqueCounterparties      uint64   `json:"unique_counterparties"`
	EventCount                uint64   `json:"event_count"`
	ActiveDays                uint64   `json:"active_days"`
	Method                    string   `json:"method"`
}

type PathQuery struct {
	ChainID uint32
	Address string
	Limit   int
}

type PathItem struct {
	A       string `json:"a"`
	B       string `json:"b"`
	C       string `json:"c"`
	Token   string `json:"token"`
	Amount  string `json:"amount"`
	TxCount uint64 `json:"tx_count"`
}

type GraphQuery struct {
	ChainID uint32
	Limit   int
}

type GraphNode struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	RiskScore float64 `json:"risk_score"`
	Degree    uint64  `json:"degree"`
	PageRank  float64 `json:"pagerank"`
}

type GraphEdge struct {
	Source              string `json:"source"`
	Target              string `json:"target"`
	Kind                string `json:"kind"`
	Token               string `json:"token,omitempty"`
	Amount              string `json:"amount,omitempty"`
	HistoricalValueUSDT string `json:"historical_value_usdt,omitempty"`
	ValuationStatus     string `json:"valuation_status"`
	TxCount             uint64 `json:"tx_count,omitempty"`
}

type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}
