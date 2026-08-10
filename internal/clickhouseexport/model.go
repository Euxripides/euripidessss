package clickhouseexport

import (
	"errors"
	"time"
)

const DefaultSpoolDirectory = `E:\database\clickhouse\export_spool`

type Dataset string

const (
	DatasetBlocks            Dataset = "blocks"
	DatasetTransactions      Dataset = "transactions"
	DatasetTokenTransfers    Dataset = "token_transfers"
	DatasetInternalTxs       Dataset = "internal_transactions"
	DatasetContractCreations Dataset = "contract_creations"
	DatasetAddressActivity   Dataset = "address_activity"
	DatasetAddressSummary    Dataset = "address_summary"
)

type Filter struct {
	ChainID   uint32  `json:"chain_id"`
	Address   string  `json:"address,omitempty"`
	FromBlock *uint64 `json:"from_block,omitempty"`
	ToBlock   *uint64 `json:"to_block,omitempty"`
}

type Request struct {
	Dataset Dataset  `json:"dataset"`
	Columns []string `json:"columns,omitempty"`
	Filter  Filter   `json:"filter"`
	Limit   uint64   `json:"limit,omitempty"`
}

type Status string

const (
	StatusQueued    Status = "QUEUED"
	StatusRunning   Status = "RUNNING"
	StatusCompleted Status = "COMPLETED"
	StatusFailed    Status = "FAILED"
	StatusCancelled Status = "CANCELLED"
)

type Task struct {
	ID          string     `json:"id"`
	Status      Status     `json:"status"`
	Request     Request    `json:"request"`
	FileName    string     `json:"file_name,omitempty"`
	Bytes       int64      `json:"bytes,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

var (
	ErrTaskNotFound   = errors.New("export task not found")
	ErrTaskRunning    = errors.New("export task is still running")
	ErrDownloadActive = errors.New("export download is active")
	ErrNotReady       = errors.New("export file is not ready")
)
