package cryptodownload

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// NewAPIHandler exposes the wallet exporter job API without the standalone HTML GUI.
func NewAPIHandler(configDir string) (http.Handler, error) {
	manager, err := NewGUIManager(configDir)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/start", manager.handleStart)
	mux.HandleFunc("/resume", manager.handleResume)
	mux.HandleFunc("/job", manager.handleJob)
	mux.HandleFunc("/jobs", manager.handleJobs)
	mux.HandleFunc("/history", manager.handleHistory)
	mux.HandleFunc("/history/import", manager.handleHistoryImport)
	mux.HandleFunc("/history/resume", manager.handleHistoryResume)
	mux.HandleFunc("/cancel", manager.handleCancel)
	mux.HandleFunc("/settings", manager.handleSettings)
	return mux, nil
}

func (m *GUIManager) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := loadGUISettingsFromConfigDir(m.configDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		settings.CSVIMAPPassword = ""
		writeJSON(w, settings)
	case http.MethodPost:
		var settings GUIPersistedSettings
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&settings); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(settings.CSVIMAPPassword) == "" {
			if previous, err := loadGUISettingsFromConfigDir(m.configDir); err == nil {
				settings.CSVIMAPPassword = previous.CSVIMAPPassword
			}
		}
		settings = normalizeGUIPersistedSettings(settings)
		if err := saveGUISettingsToConfigDir(m.configDir, settings); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		settings.CSVIMAPPassword = ""
		writeJSON(w, settings)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func saveGUISettingsToConfigDir(configDir string, settings GUIPersistedSettings) error {
	if configDir == "" {
		return saveGUIPersistedSettings(settings)
	}
	path := filepath.Join(configDir, "wallet-exporter", "gui-settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(normalizeGUIPersistedSettings(settings), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0600)
}
