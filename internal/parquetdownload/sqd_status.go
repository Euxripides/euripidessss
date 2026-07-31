package parquetdownload

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/etl/backend/internal/datasource/sqd"
)

// SQDStatusSnapshot is the debug view of the SQD reliability layer.
// Exposed at GET /api/crypto/parquet/sqd/status.
type SQDStatusSnapshot struct {
	Portal      string                       `json:"portal"`
	Metrics     sqd.MetricsSnapshot          `json:"metrics"`
	Workers     sqd.AdaptiveWorkerStats      `json:"workers"`
	Circuit     sqd.CircuitStats             `json:"circuit_breaker"`
	Cooldown    bool                         `json:"cooldown_active"`
	CooldownFor string                       `json:"cooldown_for,omitempty"`
	CheckedAt   time.Time                    `json:"checked_at"`
}

// SQDStatus returns a point-in-time snapshot of the SQD reliability layer.
// Safe for concurrent use.
func (m *Manager) SQDStatus() SQDStatusSnapshot {
	m.mu.RLock()
	client := m.sqd
	m.mu.RUnlock()

	snapshot := SQDStatusSnapshot{CheckedAt: time.Now().UTC()}
	if client == nil {
		return snapshot
	}
	snapshot.Portal = client.Portal()
	if metrics := client.Metrics(); metrics != nil {
		snapshot.Metrics = metrics.Snapshot()
	}
	if workers := client.Workers(); workers != nil {
		snapshot.Workers = workers.Stats()
	}
	snapshot.Circuit = client.Breaker().Stats()
	snapshot.Cooldown = client.IsInCooldown()
	if snapshot.Cooldown {
		snapshot.CooldownFor = client.CooldownUntil().Format(time.RFC3339)
	}
	return snapshot
}

// sqdStatus serves GET /sqd/status on the parquet handler mux.
func (h *Handler) sqdStatus(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(h.manager.SQDStatus())
}
