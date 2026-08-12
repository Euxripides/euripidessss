package cryptodownload

import (
	"encoding/json"
	"errors"
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
	mux.HandleFunc("/file", manager.handleResultFile)
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
		if err := validateGUIMailIdentity(settings.CSVEmail, settings.CSVIMAPHost, settings.CSVIMAPUser); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
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

func (m *GUIManager) hydrateCSVStartRequest(req GUIStartRequest) (GUIStartRequest, error) {
	if !strings.EqualFold(strings.TrimSpace(req.Source), "csv") {
		return req, nil
	}
	settings, err := loadGUISettingsFromConfigDir(m.configDir)
	if err != nil {
		return req, err
	}
	req.CSVEmail = firstNonEmpty(strings.TrimSpace(req.CSVEmail), settings.CSVEmail)
	req.CSVIMAPHost = firstNonEmpty(strings.TrimSpace(req.CSVIMAPHost), settings.CSVIMAPHost)
	req.CSVIMAPUser = firstNonEmpty(strings.TrimSpace(req.CSVIMAPUser), settings.CSVIMAPUser)
	req.CSVIMAPPassword = firstNonEmpty(strings.TrimSpace(req.CSVIMAPPassword), settings.CSVIMAPPassword)
	req.CSVDeliveryMode = normalizeCSVDeliveryMode(firstNonEmpty(strings.TrimSpace(req.CSVDeliveryMode), settings.CSVDeliveryMode))
	if req.CSVIMAPPort <= 0 {
		req.CSVIMAPPort = settings.CSVIMAPPort
	}
	cfg := normalizeCSVMailConfig(Config{
		CSVEmail:        req.CSVEmail,
		CSVIMAPHost:     req.CSVIMAPHost,
		CSVIMAPPort:     req.CSVIMAPPort,
		CSVIMAPUser:     req.CSVIMAPUser,
		CSVIMAPPassword: req.CSVIMAPPassword,
	})
	req.CSVEmail = cfg.CSVEmail
	req.CSVIMAPHost = cfg.CSVIMAPHost
	req.CSVIMAPPort = cfg.CSVIMAPPort
	req.CSVIMAPUser = cfg.CSVIMAPUser
	req.CSVIMAPPassword = cfg.CSVIMAPPassword
	if err := validateGUIMailIdentity(req.CSVEmail, req.CSVIMAPHost, req.CSVIMAPUser); err != nil {
		return req, err
	}
	switch {
	case strings.TrimSpace(req.CSVEmail) == "":
		return req, errors.New("CSV 模式缺少接收邮箱")
	case strings.TrimSpace(req.CSVIMAPHost) == "":
		return req, errors.New("CSV 模式缺少 IMAP 主机")
	case strings.Contains(req.CSVIMAPHost, "@"):
		return req, errors.New("IMAP 主机不能填写邮箱地址，请填写例如 imap.gmail.com")
	case req.CSVIMAPPort <= 0:
		return req, errors.New("CSV 模式缺少有效的 IMAP 端口")
	case strings.TrimSpace(req.CSVIMAPUser) == "":
		return req, errors.New("CSV 模式缺少 IMAP 用户名")
	case strings.TrimSpace(req.CSVIMAPPassword) == "":
		return req, errors.New("CSV 模式缺少 IMAP 密码或授权码")
	default:
		return req, nil
	}
}

func validateGUIMailIdentity(email, host, user string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	host = strings.ToLower(strings.TrimSpace(host))
	user = strings.ToLower(strings.TrimSpace(user))
	if host == "imap.gmail.com" {
		if !strings.HasSuffix(email, "@gmail.com") || !strings.HasSuffix(user, "@gmail.com") {
			return errors.New("Gmail IMAP 的接收邮箱和 IMAP 用户都必须是完整的 @gmail.com 地址")
		}
		if email != user {
			return errors.New("Gmail IMAP 用户必须与接收邮箱一致")
		}
	}
	return nil
}
