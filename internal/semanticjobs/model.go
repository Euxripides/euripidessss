// Package semanticjobs manages durable canonical-data reparse and
// re-enrichment jobs. It deliberately has no dependency on any downloader.
package semanticjobs

import "time"

type JobType string

const (
	JobTypeReparse  JobType = "REPARSE"
	JobTypeReenrich JobType = "REENRICH"
)

const (
	EnrichmentTokenMetadata = "token_metadata"
	EnrichmentEntityLabels  = "entity_labels"
	EnrichmentPrices        = "historical_prices"
	EnrichmentContractABI   = "contract_abi"
	EnrichmentContractMeta  = "contract_metadata"
)

type Status string

const (
	StatusQueued    Status = "QUEUED"
	StatusRunning   Status = "RUNNING"
	StatusCompleted Status = "COMPLETED"
	StatusFailed    Status = "FAILED"
	StatusCancelled Status = "CANCELLED"
)

// Request is the immutable semantic scope of a job. Enrichments is only valid
// for REENRICH; ParserVersion is only valid (and required) for REPARSE.
type Request struct {
	Type          JobType  `json:"type"`
	Chain         string   `json:"chain"`
	StartBlock    uint64   `json:"start_block"`
	EndBlock      uint64   `json:"end_block"`
	Dataset       string   `json:"dataset"`
	ParserVersion string   `json:"parser_version,omitempty"`
	Enrichments   []string `json:"enrichments,omitempty"`
}

type Progress struct {
	Completed uint64 `json:"completed"`
	Total     uint64 `json:"total"`
	LastBlock uint64 `json:"last_block,omitempty"`
}

type Job struct {
	ID             string     `json:"id"`
	IdempotencyKey string     `json:"idempotency_key"`
	Request        Request    `json:"request"`
	Status         Status     `json:"status"`
	Progress       Progress   `json:"progress"`
	Error          string     `json:"error,omitempty"`
	Attempts       int        `json:"attempts"`
	RecoveryCount  int        `json:"recovery_count"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

func (j Job) Clone() Job {
	j.Request.Enrichments = append([]string(nil), j.Request.Enrichments...)
	return j
}
