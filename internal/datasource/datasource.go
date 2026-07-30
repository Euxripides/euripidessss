package datasource

import (
	"context"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/normalize"
)

type Object struct {
	Key          string `json:"key"`
	URI          string `json:"uri"`
	DataType     string `json:"data_type"`
	SourceDate   string `json:"source_date"`
	SizeBytes    int64  `json:"size_bytes"`
	ETag         string `json:"etag"`
	LastModified string `json:"last_modified,omitempty"`
}

type TransactionSource interface {
	DiscoverTransactions(ctx context.Context, network chain.EVM, startDate, endDate string) ([]Object, error)
}

type ReceiptSource interface {
	Probe(ctx context.Context, network chain.EVM, sampleTxHash string) error
	Receipts(ctx context.Context, network chain.EVM, txHashes []string) ([]normalize.TransactionReceipt, error)
}

// LogsSource is the V1.2 extension point. Implementations must probe their
// returned schema before any Transfer log is normalized.
type LogsSource interface {
	ProbeSchema(ctx context.Context, network chain.EVM) error
}
