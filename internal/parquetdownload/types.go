package parquetdownload

import "time"

const (
	StatusQueued   = "queued"
	StatusRunning  = "running"
	StatusDone     = "done"
	StatusFailed   = "failed"
	StatusCanceled = "canceled"
)

type Settings struct {
	DataRoot            string `json:"data_root"`
	DownloadConcurrency int    `json:"download_concurrency"`
	DuckDBThreads       int    `json:"duckdb_threads"`
	MemoryLimit         string `json:"memory_limit"`
	MinimumFreeGB       int64  `json:"minimum_free_gb"`
	KeepSourceFiles     bool   `json:"keep_source_files"`
	ExportCSV           bool   `json:"export_csv"`
}

type StartRequest struct {
	ChainKey       string   `json:"chain_key"`
	Addresses      string   `json:"addresses"`
	StartDate      string   `json:"start_date"`
	EndDate        string   `json:"end_date"`
	KeepSource     *bool    `json:"keep_source_files,omitempty"`
	ExportCSV      *bool    `json:"export_csv,omitempty"`
	SelectedSource []string `json:"selected_sources,omitempty"`
}

type AddressSummary struct {
	Input        int      `json:"input"`
	Valid        int      `json:"valid"`
	Invalid      int      `json:"invalid"`
	Duplicates   int      `json:"duplicates"`
	Addresses    []string `json:"addresses,omitempty"`
	InvalidItems []string `json:"invalid_items,omitempty"`
}

type SourceObject struct {
	Key          string `json:"key"`
	URI          string `json:"uri"`
	DataType     string `json:"data_type"`
	SourceDate   string `json:"source_date"`
	SizeBytes    int64  `json:"size_bytes"`
	ETag         string `json:"etag"`
	LastModified string `json:"last_modified,omitempty"`
}

type Preview struct {
	ChainKey     string         `json:"chain_key"`
	ChainID      int64          `json:"chain_id"`
	NativeSymbol string         `json:"native_symbol"`
	Addresses    AddressSummary `json:"addresses"`
	Files        []SourceObject `json:"files"`
	TotalBytes   int64          `json:"total_bytes"`
	FreeBytes    uint64         `json:"free_bytes"`
	Warnings     []string       `json:"warnings"`
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
	Error           string  `json:"error,omitempty"`
}

type Job struct {
	ID               string         `json:"id"`
	ChainKey         string         `json:"chain_key"`
	ChainID          int64          `json:"chain_id"`
	NativeSymbol     string         `json:"native_symbol"`
	Status           string         `json:"status"`
	Stage            string         `json:"stage"`
	Progress         float64        `json:"progress"`
	Addresses        AddressSummary `json:"addresses"`
	StartDate        string         `json:"start_date"`
	EndDate          string         `json:"end_date"`
	TotalBytes       int64          `json:"total_bytes"`
	DownloadedBytes  int64          `json:"downloaded_bytes"`
	DownloadSpeedBPS float64        `json:"download_speed_bps"`
	ETASeconds       int64          `json:"eta_seconds"`
	SourceRows       int64          `json:"source_rows"`
	MatchedRows      int64          `json:"matched_rows"`
	FailedFiles      int            `json:"failed_files"`
	Files            []*FileTask    `json:"files"`
	Stages           []Stage        `json:"stages"`
	Outputs          []string       `json:"outputs"`
	Warnings         []string       `json:"warnings"`
	Error            string         `json:"error,omitempty"`
	KeepSourceFiles  bool           `json:"keep_source_files"`
	ExportCSV        bool           `json:"export_csv"`
	CreatedAt        time.Time      `json:"created_at"`
	StartedAt        *time.Time     `json:"started_at,omitempty"`
	UpdatedAt        time.Time      `json:"updated_at"`
	FinishedAt       *time.Time     `json:"finished_at,omitempty"`
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
