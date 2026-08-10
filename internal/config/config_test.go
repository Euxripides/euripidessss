package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadClickHouseConfigFileAndEnvironmentOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clickhouse.env")
	contents := "CLICKHOUSE_HOST=file-host\n" +
		"CLICKHOUSE_HTTP_PORT=18123\n" +
		"CLICKHOUSE_DATABASE=file_db\n" +
		"CLICKHOUSE_USER=file_user\n" +
		"CLICKHOUSE_PASSWORD=file-secret\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLICKHOUSE_CREDENTIAL_FILE", path)
	t.Setenv("CLICKHOUSE_ENABLED", "true")
	t.Setenv("CLICKHOUSE_REQUIRED", "true")
	t.Setenv("CLICKHOUSE_HOST", "env-host")
	t.Setenv("CLICKHOUSE_CONNECT_TIMEOUT", "750ms")
	t.Setenv("CLICKHOUSE_REQUEST_TIMEOUT", "12s")
	t.Setenv("CLICKHOUSE_MAX_CONNECTIONS", "23")

	cfg := Load().ClickHouse
	if !cfg.Enabled || !cfg.Required {
		t.Fatalf("expected enabled and required: %+v", cfg)
	}
	if cfg.Host != "env-host" || cfg.HTTPPort != 18123 || cfg.Database != "file_db" || cfg.User != "file_user" {
		t.Fatalf("unexpected ClickHouse config: host=%q port=%d database=%q user=%q", cfg.Host, cfg.HTTPPort, cfg.Database, cfg.User)
	}
	if cfg.Password != "file-secret" {
		t.Fatal("password was not loaded from credential file")
	}
	if cfg.ConnectTimeout != 750*time.Millisecond || cfg.RequestTimeout != 12*time.Second || cfg.MaxConnections != 23 {
		t.Fatalf("unexpected limits: connect=%v request=%v max=%d", cfg.ConnectTimeout, cfg.RequestTimeout, cfg.MaxConnections)
	}
}

func TestLoadClickHouseBOMCredentialFileEnablesConfiguredClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clickhouse.env")
	contents := "\ufeffCLICKHOUSE_HOST=127.0.0.1\nCLICKHOUSE_DATABASE=onchain\nCLICKHOUSE_USER=etl\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLICKHOUSE_CREDENTIAL_FILE", path)
	if cfg := Load().ClickHouse; !cfg.Enabled {
		t.Fatal("credential file with UTF-8 BOM should enable ClickHouse")
	}
}

func TestLoadClickHouseInvalidLimitsUseSafeDefaults(t *testing.T) {
	t.Setenv("CLICKHOUSE_CREDENTIAL_FILE", filepath.Join(t.TempDir(), "missing.env"))
	t.Setenv("CLICKHOUSE_HTTP_PORT", "invalid")
	t.Setenv("CLICKHOUSE_CONNECT_TIMEOUT", "0")
	t.Setenv("CLICKHOUSE_REQUEST_TIMEOUT", "invalid")
	t.Setenv("CLICKHOUSE_MAX_CONNECTIONS", "-2")

	cfg := Load().ClickHouse
	if cfg.HTTPPort != 8123 || cfg.ConnectTimeout != 5*time.Second || cfg.RequestTimeout != 30*time.Second || cfg.MaxConnections != 10 {
		t.Fatalf("unsafe defaults: port=%d connect=%v request=%v max=%d", cfg.HTTPPort, cfg.ConnectTimeout, cfg.RequestTimeout, cfg.MaxConnections)
	}
}

func TestDataPlaneDefaultsAndReaderDisable(t *testing.T) {
	t.Setenv("EXPLORER_DATASOURCE", "invalid")
	t.Setenv("DUCKDB_READER_ENABLED", "false")
	cfg := Load().Analytics
	if cfg.DataSource != "clickhouse" || cfg.DuckDBReaderEnabled {
		t.Fatalf("unexpected data plane config: %+v", cfg)
	}
}
