// Package financialintegration exposes ClickHouse-only integration surfaces
// for historical-USD graph, investigation and export consumers.
package financialintegration

import (
	"context"
	"io"
	"time"
)

const (
	maxGraphEdges             = 500
	maxTokenBreakdowns        = 10_000
	maxExportRows      uint64 = 1_000_000
)

// QueryClient is deliberately transport-neutral and is implemented by the
// shared ClickHouse client.
type QueryClient interface {
	QueryJSON(ctx context.Context, query string) ([]map[string]any, error)
	QueryCSV(ctx context.Context, query string) (io.ReadCloser, error)
}

type GraphQuery struct {
	ChainID      uint32
	Address      string
	From         time.Time
	To           time.Time
	MinUSD       string
	TokenAddress string
	Limit        int
}

type TokenBreakdown struct {
	TokenAddress    string `json:"token_address,omitempty"`
	TokenSymbol     string `json:"token_symbol,omitempty"`
	Amount          string `json:"amount"`
	HistoricalUSD   string `json:"historical_usd"`
	HistoricalPrice string `json:"historical_price,omitempty"`
	PriceTime       string `json:"price_time,omitempty"`
	PriceSource     string `json:"price_source,omitempty"`
	PriceConfidence string `json:"price_confidence,omitempty"`
}

type HistoricalUSDEdge struct {
	FromAddress      string           `json:"from_address"`
	ToAddress        string           `json:"to_address"`
	HistoricalUSD    string           `json:"historical_usd"`
	TransactionCount uint64           `json:"transaction_count"`
	EventCount       uint64           `json:"event_count"`
	FirstTime        string           `json:"first_time"`
	LastTime         string           `json:"last_time"`
	EntityID         string           `json:"entity_id,omitempty"`
	EntityName       string           `json:"entity_name,omitempty"`
	EntityRole       string           `json:"entity_role,omitempty"`
	EntityConfidence string           `json:"entity_confidence,omitempty"`
	TokenBreakdown   []TokenBreakdown `json:"token_breakdown"`
}

type HistoricalUSDGraph struct {
	ChainID    uint32              `json:"chain_id"`
	Address    string              `json:"address"`
	From       string              `json:"from"`
	To         string              `json:"to"`
	MinUSD     string              `json:"min_usd"`
	Edges      []HistoricalUSDEdge `json:"edges"`
	Truncated  bool                `json:"truncated"`
	PriceBasis string              `json:"price_basis"`
}

type FinancialQuery struct {
	ChainID      uint32    `json:"chain_id"`
	Address      string    `json:"address"`
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	AsOf         time.Time `json:"as_of"`
	TokenAddress string    `json:"token_address,omitempty"`
}

// FinancialAnalytics is the small, strongly-bounded integration contract used
// by an investigation planner. Implementations may wrap the dedicated summary,
// FIFO retention/pass-through and PnL repositories.
type FinancialAnalytics interface {
	FinancialSummary(ctx context.Context, query FinancialQuery) (any, error)
	Retention(ctx context.Context, query FinancialQuery) (any, error)
	PassThrough(ctx context.Context, query FinancialQuery) (any, error)
	PnL(ctx context.Context, query FinancialQuery) (any, error)
}

type InvestigationSnapshot struct {
	Query       FinancialQuery `json:"query"`
	Summary     any            `json:"financial_summary,omitempty"`
	Retention   any            `json:"retention,omitempty"`
	PassThrough any            `json:"pass_through,omitempty"`
	PnL         any            `json:"pnl,omitempty"`
}

type ExportDataset string

const (
	ExportHistoricalActivity ExportDataset = "historical_activity"
	ExportHistoricalEdges    ExportDataset = "historical_edges"
)

type ExportRequest struct {
	Dataset      ExportDataset
	ChainID      uint32
	Address      string
	From         time.Time
	To           time.Time
	TokenAddress string
	MinUSD       string
	Limit        uint64
}

// AlgorithmRecord is the complete fixed whitelist for derived investigation
// exports. It intentionally contains no local or remote path field.
type AlgorithmRecord struct {
	Metric           string
	Window           string
	ValueUSD         string
	Ratio            string
	Coverage         string
	Confidence       string
	AlgorithmVersion string
	PriceVersion     string
	From             time.Time
	To               time.Time
	TokenFilter      string
}
