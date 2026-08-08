package validation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ValidationState 状态机（设计 §36）。
type ValidationState string

const (
	StateNotStarted ValidationState = "NOT_STARTED"
	StateRunning    ValidationState = "RUNNING"
	StatePass       ValidationState = "PASS"
	StateWarn       ValidationState = "WARN"
	StateRepairing  ValidationState = "REPAIRING"
	StatePartial    ValidationState = "PARTIAL"
	StateFailed     ValidationState = "FAILED"
)

// Certificate Validation Certificate（设计 §37）。
type Certificate struct {
	DatasetJobID           string        `json:"dataset_job_id"`
	Status                 string        `json:"status"` // PASS / WARN / PARTIAL / FAILED
	RequestedRange         BlockInterval `json:"requested_range"`
	Coverage               float64       `json:"coverage"`
	RowsRaw                int64         `json:"rows_raw"`
	RowsNormalized         int64         `json:"rows_normalized"`
	RowsUnique             int64         `json:"rows_unique"`
	RowsFinal              int64         `json:"rows_final"`
	DuplicatesRemoved      int64         `json:"duplicates_removed"`
	PartsCount             int           `json:"parts_count"`
	DuplicateSHA           int           `json:"duplicate_sha"`
	GapsDetected           int           `json:"gaps_detected"`
	GapsRepaired           int           `json:"gaps_repaired"`
	GapsRemaining          int           `json:"gaps_remaining"`
	CrossCheckSampleRanges int           `json:"cross_check_sample_ranges"`
	CrossCheckMatched      int           `json:"cross_check_matched"`
	ProvidersUsed          []string      `json:"providers_used,omitempty"`
	ProviderSwitches       int           `json:"provider_switches"`
	CertifiedAt            time.Time     `json:"certified_at"`
}

// StateRecord validation-state.json。
type StateRecord struct {
	Status    ValidationState `json:"status"`
	Stage     string          `json:"stage"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// SaveState 写入 validation-state.json。
func (s *GapStore) SaveState(st ValidationState, stage string) error {
	rec := StateRecord{Status: st, Stage: stage, UpdatedAt: time.Now().UTC()}
	payload, _ := json.MarshalIndent(rec, "", "  ")
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.dir, "validation-state.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadState 读取状态（不存在返回 NOT_STARTED）。
func (s *GapStore) LoadState() StateRecord {
	payload, err := os.ReadFile(filepath.Join(s.dir, "validation-state.json"))
	if err != nil {
		return StateRecord{Status: StateNotStarted}
	}
	var rec StateRecord
	if json.Unmarshal(payload, &rec) != nil {
		return StateRecord{Status: StateNotStarted}
	}
	return rec
}

// SaveCertificate 写入 validation-certificate.json。
func (s *GapStore) SaveCertificate(c *Certificate) error {
	payload, _ := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.dir, "validation-certificate.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadCertificate 读取证书。
func (s *GapStore) LoadCertificate() (*Certificate, error) {
	payload, err := os.ReadFile(filepath.Join(s.dir, "validation-certificate.json"))
	if err != nil {
		return nil, err
	}
	var c Certificate
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
