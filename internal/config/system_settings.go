package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/etl/backend/internal/writer"
)

const SystemSettingsFileName = "system_settings.json"

// PersistentSettings contains only non-secret, administrator-controlled
// settings. Credentials, provider URLs and tokens are intentionally excluded.
type PersistentSettings struct {
	ConcurrencyLevel     int    `json:"concurrency_level"`
	MaxFileSizeMB        int    `json:"max_file_size_mb"`
	AnalyticsDataSource  string `json:"analytics_data_source"`
	ClickHouseEnabled    bool   `json:"clickhouse_enabled"`
	ClickHouseRequired   bool   `json:"clickhouse_required"`
	PriceEngineEnabled   bool   `json:"price_engine_enabled"`
	LogRetentionDays     int    `json:"log_retention_days"`
	OutputRetentionDays  int    `json:"output_retention_days"`
	BackupRetentionCount int    `json:"backup_retention_count"`
}

type PersistentSettingsPatch struct {
	ConcurrencyLevel     *int    `json:"concurrency_level,omitempty"`
	MaxFileSizeMB        *int    `json:"max_file_size_mb,omitempty"`
	AnalyticsDataSource  *string `json:"analytics_data_source,omitempty"`
	ClickHouseEnabled    *bool   `json:"clickhouse_enabled,omitempty"`
	ClickHouseRequired   *bool   `json:"clickhouse_required,omitempty"`
	PriceEngineEnabled   *bool   `json:"price_engine_enabled,omitempty"`
	LogRetentionDays     *int    `json:"log_retention_days,omitempty"`
	OutputRetentionDays  *int    `json:"output_retention_days,omitempty"`
	BackupRetentionCount *int    `json:"backup_retention_count,omitempty"`
}

func SettingsFromConfig(c *Config) PersistentSettings {
	return PersistentSettings{
		ConcurrencyLevel: c.ConcurrencyLevel, MaxFileSizeMB: int(c.MaxFileSize / (1024 * 1024)),
		AnalyticsDataSource: c.Analytics.DataSource, ClickHouseEnabled: c.ClickHouse.Enabled,
		ClickHouseRequired: c.ClickHouse.Required, PriceEngineEnabled: c.PriceEngine.Enabled,
		LogRetentionDays: 30, OutputRetentionDays: 30, BackupRetentionCount: 10,
	}
}

func (s PersistentSettings) Validate() error {
	if s.ConcurrencyLevel < 1 || s.ConcurrencyLevel > 256 {
		return fmt.Errorf("concurrency_level 必须在 1-256")
	}
	if s.MaxFileSizeMB < 1 || s.MaxFileSizeMB > 4096 {
		return fmt.Errorf("max_file_size_mb 必须在 1-4096")
	}
	switch strings.ToLower(strings.TrimSpace(s.AnalyticsDataSource)) {
	case "auto", "clickhouse", "duckdb":
	default:
		return fmt.Errorf("analytics_data_source 仅支持 auto/clickhouse/duckdb")
	}
	if s.ClickHouseRequired && !s.ClickHouseEnabled {
		return fmt.Errorf("clickhouse_required 不能在 clickhouse_enabled=false 时启用")
	}
	if s.LogRetentionDays < 1 || s.LogRetentionDays > 365 {
		return fmt.Errorf("log_retention_days 必须在 1-365")
	}
	if s.OutputRetentionDays < 1 || s.OutputRetentionDays > 365 {
		return fmt.Errorf("output_retention_days 必须在 1-365")
	}
	if s.BackupRetentionCount < 1 || s.BackupRetentionCount > 50 {
		return fmt.Errorf("backup_retention_count 必须在 1-50")
	}
	return nil
}

func (s PersistentSettings) ApplyPatch(p PersistentSettingsPatch) (PersistentSettings, []string, error) {
	changed := make([]string, 0, 9)
	set := func(key string, different bool) {
		if different {
			changed = append(changed, key)
		}
	}
	if p.ConcurrencyLevel != nil {
		set("concurrency_level", s.ConcurrencyLevel != *p.ConcurrencyLevel)
		s.ConcurrencyLevel = *p.ConcurrencyLevel
	}
	if p.MaxFileSizeMB != nil {
		set("max_file_size_mb", s.MaxFileSizeMB != *p.MaxFileSizeMB)
		s.MaxFileSizeMB = *p.MaxFileSizeMB
	}
	if p.AnalyticsDataSource != nil {
		v := strings.ToLower(strings.TrimSpace(*p.AnalyticsDataSource))
		set("analytics_data_source", s.AnalyticsDataSource != v)
		s.AnalyticsDataSource = v
	}
	if p.ClickHouseEnabled != nil {
		set("clickhouse_enabled", s.ClickHouseEnabled != *p.ClickHouseEnabled)
		s.ClickHouseEnabled = *p.ClickHouseEnabled
	}
	if p.ClickHouseRequired != nil {
		set("clickhouse_required", s.ClickHouseRequired != *p.ClickHouseRequired)
		s.ClickHouseRequired = *p.ClickHouseRequired
	}
	if p.PriceEngineEnabled != nil {
		set("price_engine_enabled", s.PriceEngineEnabled != *p.PriceEngineEnabled)
		s.PriceEngineEnabled = *p.PriceEngineEnabled
	}
	if p.LogRetentionDays != nil {
		set("log_retention_days", s.LogRetentionDays != *p.LogRetentionDays)
		s.LogRetentionDays = *p.LogRetentionDays
	}
	if p.OutputRetentionDays != nil {
		set("output_retention_days", s.OutputRetentionDays != *p.OutputRetentionDays)
		s.OutputRetentionDays = *p.OutputRetentionDays
	}
	if p.BackupRetentionCount != nil {
		set("backup_retention_count", s.BackupRetentionCount != *p.BackupRetentionCount)
		s.BackupRetentionCount = *p.BackupRetentionCount
	}
	if err := s.Validate(); err != nil {
		return PersistentSettings{}, nil, err
	}
	return s, changed, nil
}

func LoadPersistentSettings(configDir string) (PersistentSettings, bool, error) {
	path := filepath.Join(configDir, SystemSettingsFileName)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return PersistentSettings{}, false, nil
	}
	if err != nil {
		return PersistentSettings{}, false, err
	}
	var settings PersistentSettings
	if err := json.Unmarshal(b, &settings); err != nil {
		return PersistentSettings{}, false, fmt.Errorf("解析系统设置: %w", err)
	}
	if err := settings.Validate(); err != nil {
		return PersistentSettings{}, false, err
	}
	return settings, true, nil
}

func SavePersistentSettings(configDir string, settings PersistentSettings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	return writer.WriteJSONAtomic(filepath.Join(configDir, SystemSettingsFileName), settings)
}
