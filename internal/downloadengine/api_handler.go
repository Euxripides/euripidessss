package downloadengine

import (
	"encoding/json"
	"net/http"
)

// ── V2 REST API handler ──

type APIHandler struct {
	runner *MigrationRunner
}

func NewAPIHandler(runner *MigrationRunner) *APIHandler {
	return &APIHandler{runner: runner}
}

func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/schema-version", h.handleSchemaVersion)
	mux.ServeHTTP(w, r)
}

func (h *APIHandler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"engine":         "downloadengine-v2",
		"schema_version": h.runner.CurrentVersion(),
	})
}

func (h *APIHandler) handleSchemaVersion(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"schema_version": h.runner.CurrentVersion(),
		})
	case http.MethodPost:
		if err := h.runner.Run(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"schema_version": h.runner.CurrentVersion(),
			"status":         "migrated",
		})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"detail": "不支持的方法"})
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
