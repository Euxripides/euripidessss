package api

import (
	"sync"
	"time"
)

// AsyncImportProgress tracks import status for async import
type AsyncImportProgress struct {
	Status      string                   `json:"status"` // "parsing", "loading", "done", "error"
	SessionID   string                   `json:"session_id"`
	Rows        int                      `json:"rows"`
	Columns     []string                 `json:"columns,omitempty"`
	Files       []string                 `json:"files,omitempty"`
	Sample      []map[string]interface{} `json:"sample,omitempty"`
	MappingRule map[string]interface{}   `json:"mapping_rule,omitempty"`
	Error       string                   `json:"error,omitempty"`
	StartedAt   time.Time                `json:"-"`
	mu          sync.Mutex
}

var (
	importProgressMu sync.RWMutex
	importProgress   = make(map[string]*AsyncImportProgress)
)

func setImportProgress(sessionID string, p *AsyncImportProgress) {
	importProgressMu.Lock()
	defer importProgressMu.Unlock()
	p.StartedAt = time.Now()
	importProgress[sessionID] = p
}

func getImportProgress(sessionID string) *AsyncImportProgress {
	importProgressMu.RLock()
	defer importProgressMu.RUnlock()
	return importProgress[sessionID]
}
