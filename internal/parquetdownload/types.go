package parquetdownload

import (
	"time"

	"github.com/etl/backend/internal/datasource"
	datasetwriter "github.com/etl/backend/internal/writer"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusPausing   = "pausing"
	StatusPaused    = "paused"
	StatusCanceling = "canceling"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"
	StatusCanceled  = "canceled"
)

const (
	CoverageComplete    = "COMPLETE"
	CoveragePartial     = "PARTIAL"
	CoverageDownloading = "DOWNLOADING"
	CoverageFailed      = "FAILED"
	CoverageNotSelected = "NOT_SELECTED"
)

type Settings struct {
	DataRoot            string `json:"data_root"`
	DownloadConcurrency int    `json:"download_concurrency"`
	DuckDBThreads       int    `json:"duckdb_threads"`
	MemoryLimit         string `json:"memory_limit"`
	MinimumFreeGB       int64  `json:"minimum_free_gb"`
	KeepSourceFiles     bool   `json:"keep_source_files"`
	ExportCSV           bool   `json:"export_csv"`
	ReceiptBatchSize    int    `json:"receipt_batch_size"`
}

type StartRequest struct {
	ChainKey        string   `json:"chain_key"`
	Addresses       string   `json:"addresses"`
	StartDate       string   `json:"start_date"`
	EndDate         string   `json:"end_date"`
	FromBlock       uint64   `json:"from_block,omitempty"` // Phase 5.2 P0-2：显式区块范围透传
	ToBlock         uint64   `json:"to_block,omitempty"`
	UseFirstSeen    bool     `json:"use_first_seen"`
	KeepSource      *bool    `json:"keep_source_files,omitempty"`
	ExportCSV       *bool    `json:"export_csv,omitempty"`
	SelectedSource  []string `json:"selected_sources,omitempty"`
	IncludeReceipts bool     `json:"include_receipts"`
}

type AddressSummary struct {
	Input        int      `json:"input"`
	Valid        int      `json:"valid"`
	Invalid      int      `json:"invalid"`
	Duplicates   int      `json:"duplicates"`
	Addresses    []string `json:"addresses,omitempty"`
	InvalidItems []string `json:"invalid_items,omitempty"`
}

type SourceObject = datasource.Object

type SQDBlockRange struct {
	From uint64 `json:"from_block"`
	To   uint64 `json:"to_block"`
}

type Preview struct {
	ChainKey         string         `json:"chain_key"`
	ChainID          int64          `json:"chain_id"`
	NativeSymbol     string         `json:"native_symbol"`
	Addresses        AddressSummary `json:"addresses"`
	SelectedSources  []string       `json:"selected_sources"`
	Files            []SourceObject `json:"files"`
	TotalBytes       int64          `json:"total_bytes"`
	FreeBytes        uint64         `json:"free_bytes"`
	Warnings         []string       `json:"warnings"`
	SQDAvailable     bool           `json:"sqd_available"`
	SQDDataset       string         `json:"sqd_dataset,omitempty"`
	SQDBlockRange    *SQDBlockRange `json:"sqd_block_range,omitempty"`
	ReceiptAvailable bool           `json:"receipt_available"`
	ReceiptRPCEnv    string         `json:"receipt_rpc_env"`
}

type Stage struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	Detail   string  `json:"detail,omitempty"`
}

type FileTask struct {
	SourceObject
	LocalPath       string  `json:"local_path,omitempty"`
	OutputPath      string  `json:"output_path,omitempty"`
	CSVPath         string  `json:"csv_path,omitempty"`
	Status          string  `json:"status"`
	Progress        float64 `json:"progress"`
	DownloadedBytes int64   `json:"downloaded_bytes"`
	SourceRows      int64   `json:"source_rows"`
	MatchedRows     int64   `json:"matched_rows"`
	RetryCount      int     `json:"retry_count"`
	DownloadSHA256  string  `json:"download_sha256,omitempty"`
	ResumedChunks   int     `json:"resumed_chunks,omitempty"`
	TotalChunks     int     `json:"total_chunks,omitempty"`
	Error           string  `json:"error,omitempty"`
}

type DatasetCoverage struct {
	JobID              string    `json:"job_id"`
	ChainID            int64     `json:"chain_id"`
	TransactionsStatus string    `json:"transactions_status"`
	LogsStatus         string    `json:"logs_status"`
	TraceStatus        string    `json:"trace_status"`
	CoveragePercent    float64   `json:"coverage_percent"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type TaskEvent struct {
	Type      string         `json:"type"`
	Message   string         `json:"message"`
	Stage     string         `json:"stage,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type ManifestInfo struct {
	Path          string     `json:"path,omitempty"`
	Status        string     `json:"status"`
	SchemaVersion string     `json:"schema_version"`
	Consistent    bool       `json:"consistent"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

type Job struct {
	SchemaVersion         string                   `json:"schema_version"`
	ID                    string                   `json:"id"`
	ChainKey              string                   `json:"chain_key"`
	ChainID               int64                    `json:"chain_id"`
	NativeSymbol          string                   `json:"native_symbol"`
	Status                string                   `json:"status"`
	Stage                 string                   `json:"stage"`
	Progress              float64                  `json:"progress"`
	Addresses             AddressSummary           `json:"addresses"`
	StartDate             string                   `json:"start_date"`
	EndDate               string                   `json:"end_date"`
	TotalBytes            int64                    `json:"total_bytes"`
	DownloadedBytes       int64                    `json:"downloaded_bytes"`
	DownloadSpeedBPS      float64                  `json:"download_speed_bps"`
	ETASeconds            int64                    `json:"eta_seconds"`
	SourceRows            int64                    `json:"source_rows"`
	MatchedRows           int64                    `json:"matched_rows"`
	ReceiptRows           int64                    `json:"receipt_rows"`
	ContractCreations     int64                    `json:"contract_creations"`
	TransactionRows       int64                    `json:"transaction_rows"`
	LogRows               int64                    `json:"log_rows"`
	TokenMetadataRows     int64                    `json:"token_metadata_rows"`
	TokenTransferRows     int64                    `json:"token_transfer_rows"`
	NFTTransferRows       int64                    `json:"nft_transfer_rows"`
	TraceRows             int64                    `json:"trace_rows"`
	InternalRows          int64                    `json:"internal_rows"`
	ActivityRows          int64                    `json:"activity_rows"`
	SummaryRows           int64                    `json:"summary_rows"`
	BalanceRows           int64                    `json:"balance_rows"`
	FailedFiles           int                      `json:"failed_files"`
	Files                 []*FileTask              `json:"files"`
	Stages                []Stage                  `json:"stages"`
	Outputs               []string                 `json:"outputs"`
	Warnings              []string                 `json:"warnings"`
	Error                 string                   `json:"error,omitempty"`
	CancellationRequested bool                     `json:"cancellation_requested"`
	Coverage              DatasetCoverage          `json:"dataset_coverage"`
	Checksums             []datasetwriter.Checksum `json:"checksums,omitempty"`
	Manifest              ManifestInfo             `json:"manifest"`
	Events                []TaskEvent              `json:"task_events,omitempty"`
	KeepSourceFiles       bool                     `json:"keep_source_files"`
	ExportCSV             bool                     `json:"export_csv"`
	IncludeReceipts       bool                     `json:"include_receipts"`
	UseFirstSeen          bool                     `json:"use_first_seen"`
	SelectedSources       []string                 `json:"selected_sources"`
	SQDDataset            string                   `json:"sqd_dataset,omitempty"`
	SQDBlockRange         *SQDBlockRange           `json:"sqd_block_range,omitempty"`
	CreatedAt             time.Time                `json:"created_at"`
	StartedAt             *time.Time               `json:"started_at,omitempty"`
	UpdatedAt             time.Time                `json:"updated_at"`
	FinishedAt            *time.Time               `json:"finished_at,omitempty"`
}

type UploadAddressResponse struct {
	Raw     string         `json:"raw"`
	Summary AddressSummary `json:"summary"`
}

type checkpoint struct {
	SourceURI   string    `json:"source_uri"`
	ETag        string    `json:"etag"`
	AddressHash string    `json:"address_hash"`
	SizeBytes   int64     `json:"size_bytes"`
	OutputPath  string    `json:"output_path"`
	CSVPath     string    `json:"csv_path,omitempty"`
	SourceRows  int64     `json:"source_rows"`
	Matched     int64     `json:"matched_rows"`
	Completed   time.Time `json:"completed_at"`
}
