package cryptodownload

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type guiHistoryImportRequest struct {
	IDs []string `json:"ids"`
}

func (m *GUIManager) handleHistory(w http.ResponseWriter, r *http.Request) {
	if m.history == nil {
		if r.Method == http.MethodGet {
			writeJSON(w, []GUIJobRecord{})
			return
		}
		http.Error(w, "download history unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		records, err := m.history.LoadAll()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, records)
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if err := m.history.Delete(id); err != nil {
			if errors.Is(err, errGUIDownloadHistoryNotFound) {
				http.Error(w, "history not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *GUIManager) handleHistoryImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if m.history == nil {
		http.Error(w, "download history unavailable", http.StatusServiceUnavailable)
		return
	}
	var request guiHistoryImportRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(request.IDs) == 0 {
		http.Error(w, "请选择至少一条历史记录", http.StatusBadRequest)
		return
	}
	seen := make(map[string]bool, len(request.IDs))
	jobs := make([]*GUIJob, 0, len(request.IDs))
	for _, rawID := range request.IDs {
		id := strings.TrimSpace(rawID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		record, err := m.history.Find(id)
		if err != nil {
			if errors.Is(err, errGUIDownloadHistoryNotFound) {
				http.Error(w, "history not found: "+id, http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		job, err := m.launchHistoryJob(record, false)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		jobs = append(jobs, job.snapshot())
	}
	if len(jobs) == 0 {
		http.Error(w, "请选择至少一条有效历史记录", http.StatusBadRequest)
		return
	}
	writeJSON(w, jobs)
}

func (m *GUIManager) handleHistoryResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if m.history == nil {
		http.Error(w, "download history unavailable", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	record, err := m.history.Find(id)
	if err != nil {
		if errors.Is(err, errGUIDownloadHistoryNotFound) {
			http.Error(w, "history not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	job, err := m.launchHistoryJob(record, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, job.snapshot())
}
