package model

import "time"

// --- Core ETL Models ---

type FileReport struct {
	Filename        string            `json:"filename"`
	Provider        string            `json:"provider"`
	RowsIn          int               `json:"rows_in"`
	RowsOut         int               `json:"rows_out"`
	MappedColumns   map[string]string `json:"mapped_columns"`
	MissingRequired []string          `json:"missing_required"`
}

type QualityReport struct {
	RowsIn                int          `json:"rows_in"`
	RowsOut               int          `json:"rows_out"`
	RemovedEmptyRequired  int          `json:"removed_empty_required"`
	RemovedFailedFeedback int          `json:"removed_failed_feedback"`
	RemovedBadHeaders     int          `json:"removed_bad_headers"`
	RemovedBadDirection   int          `json:"removed_bad_direction"`
	RemovedDuplicates     int          `json:"removed_duplicates"`
	UnmatchedAccountRows  int          `json:"unmatched_account_rows"`
	Files                 []FileReport `json:"files"`
	Warnings              []string     `json:"warnings"`
}

type FlowNode struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Role      string   `json:"role"`
	AmountIn  float64  `json:"amount_in"`
	AmountOut float64  `json:"amount_out"`
	TxCount   int      `json:"tx_count"`
	InCount   int      `json:"in_count"`
	OutCount  int      `json:"out_count"`
	Degree    int      `json:"degree"`
	FirstTime *string  `json:"first_time,omitempty"`
	LastTime  *string  `json:"last_time,omitempty"`
	Tags      []string `json:"tags"`
}

type FlowEdge struct {
	ID        string  `json:"id"`
	Source    string  `json:"source"`
	Target    string  `json:"target"`
	Amount    float64 `json:"amount"`
	TxCount   int     `json:"tx_count"`
	Label     string  `json:"label"`
	AvgAmount float64 `json:"avg_amount"`
	MaxAmount float64 `json:"max_amount"`
	FirstTime *string `json:"first_time,omitempty"`
	LastTime  *string `json:"last_time,omitempty"`
}

type FlowGraph struct {
	Nodes []FlowNode            `json:"nodes"`
	Edges []FlowEdge            `json:"edges"`
	Meta  map[string]interface{} `json:"meta"`
}

type ProcessResponse struct {
	JobID       string                   `json:"job_id"`
	Rows        int                      `json:"rows"`
	Columns     []string                 `json:"columns"`
	Preview     []map[string]interface{} `json:"preview"`
	Report      QualityReport            `json:"report"`
	Summary     map[string]interface{}   `json:"summary"`
	FlowGraph   FlowGraph                `json:"flow_graph"`
	DownloadURL string                   `json:"download_url"`
}

type RuleAnalysis struct {
	Provider      string                   `json:"provider"`
	ProviderLabel string                   `json:"provider_label"`
	Candidates    []map[string]interface{} `json:"candidates"`
	Suggestions   []map[string]interface{} `json:"suggestions"`
}

// TransactionRow represents a single cleaned transaction row as key-value map
type TransactionRow map[string]string

// --- ETL Pipeline Result ---

type PipelineResult struct {
	Transactions []TransactionRow
	OutputPath   string
	Summary      map[string]interface{}
	Report       QualityReport
}

// --- Scanner Models ---

type SheetCandidate struct {
	Path        string   `json:"path"`
	SheetName   string   `json:"sheet_name,omitempty"`
	Kind        string   `json:"kind"`
	Confidence  int      `json:"confidence"`
	Provider    string   `json:"provider"`
	Columns     []string `json:"columns"`
	Reason      []string `json:"reason"`
	RowsSampled int      `json:"rows_sampled"`
	SizeBytes   int64    `json:"size_bytes"`
}

type DirectoryScan struct {
	SourceDir    string          `json:"source_dir"`
	Transactions []SheetCandidate `json:"transactions"`
	Accounts     []SheetCandidate `json:"accounts"`
	Labels       []SheetCandidate `json:"labels"`
	Unknown      []SheetCandidate `json:"unknown"`
}

// --- Upload Session ---

type FlowSession struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	FilePath  string    `json:"file_path,omitempty"`
	Status    string    `json:"status"`
}

// Storage interface - for future database integration
type Storage interface {
	CreateSession() (*FlowSession, error)
	GetSession(id string) (*FlowSession, error)
	ListSessions() ([]FlowSession, error)
	SaveTransactions(sessionID string, txns []TransactionRow) error
	LoadTransactions(sessionID string) ([]TransactionRow, error)
	SaveOutput(sessionID string, data []byte, filename string) (string, error)
	ListOutputs() ([]map[string]interface{}, error)
	GetOutput(filename string) ([]byte, string, error)
}
