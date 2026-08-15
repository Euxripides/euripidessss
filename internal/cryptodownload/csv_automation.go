package cryptodownload

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// LoadCSVAutomationConfig loads the locally persisted browser-download
// settings for an unattended CSV job. Secrets remain local to the process and
// are never returned through an HTTP response.
func LoadCSVAutomationConfig(configDir, rawDir string) (Config, error) {
	settings, err := loadGUISettingsFromConfigDir(configDir)
	if err != nil {
		return Config{}, err
	}
	settings = normalizeGUIPersistedSettings(settings)
	mailConfigured := strings.TrimSpace(settings.CSVEmail) != "" || strings.TrimSpace(settings.CSVIMAPHost) != "" ||
		strings.TrimSpace(settings.CSVIMAPUser) != "" || strings.TrimSpace(settings.CSVIMAPPassword) != ""
	if mailConfigured {
		missing := make([]string, 0, 5)
		if strings.TrimSpace(settings.CSVEmail) == "" {
			missing = append(missing, "接收邮箱")
		}
		if strings.TrimSpace(settings.CSVIMAPHost) == "" {
			missing = append(missing, "IMAP 主机")
		}
		if settings.CSVIMAPPort <= 0 {
			missing = append(missing, "IMAP 端口")
		}
		if strings.TrimSpace(settings.CSVIMAPUser) == "" {
			missing = append(missing, "IMAP 用户名")
		}
		if strings.TrimSpace(settings.CSVIMAPPassword) == "" {
			missing = append(missing, "IMAP 密码或授权码")
		}
		if len(missing) > 0 {
			return Config{}, fmt.Errorf("CSV 邮件回退配置不完整: %s", strings.Join(missing, "、"))
		}
	}
	if mailConfigured && strings.Contains(settings.CSVIMAPHost, "@") {
		return Config{}, fmt.Errorf("CSV 自动执行配置无效: IMAP 主机不能是邮箱地址")
	}
	if strings.TrimSpace(rawDir) == "" {
		rawDir = filepath.Join(configDir, "smart-download-csv")
	}
	timeout := time.Duration(settings.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return Config{
		Source:          "csv",
		CSVEmail:        settings.CSVEmail,
		CSVIMAPHost:     settings.CSVIMAPHost,
		CSVIMAPPort:     settings.CSVIMAPPort,
		CSVIMAPUser:     settings.CSVIMAPUser,
		CSVIMAPPassword: settings.CSVIMAPPassword,
		CSVMailPoolText: settings.CSVMailPool,
		CSVHTTPProxyPool: settings.CSVProxyPool,
		CSVIMAPProxyPool: settings.CSVIMAPProxyPool,
		CSVProxyPin:      settings.CSVProxyPin - 1,
		RawDir:          rawDir,
		Workers:         1,
		RPS:             settings.RPS,
		PageSize:        100,
		Timeout:         timeout,
		Retries:         1,
	}, nil
}
