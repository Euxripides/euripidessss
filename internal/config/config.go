package config

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const defaultClickHouseCredentialFile = `E:\database\clickhouse\config\clickhouse.env`

type AnalyticsConfig struct {
	DuckDBPath          string
	DuckDBDatabase      string
	DuckDBReaderEnabled bool
	DataSource          string
}

type ClickHouseConfig struct {
	Enabled        bool
	Required       bool
	Host           string
	HTTPPort       int
	Database       string
	User           string
	Password       string
	ConnectTimeout time.Duration
	RequestTimeout time.Duration
	MaxConnections int
}

type PriceEngineConfig struct {
	Enabled         bool
	RootDir         string
	BinanceBaseURL  string
	MaxQueryWindow  time.Duration
	MaxBatchItems   int
	MinLiquidityUSD string
	MaxDeviationPct string
	MaxLastKnownAge time.Duration
	DownloadTimeout time.Duration
}

type Config struct {
	RootDir          string
	BackendDir       string
	UploadDir        string
	OutputDir        string
	LogDir           string
	RuleSamplesDir   string
	ConfigDir        string
	CustomRulesPath  string
	FrontendDistDir  string
	FlowTemplatePath string
	ServerPort       string
	ConcurrencyLevel int
	MaxFileSize      int64 // bytes
	Debug            bool
	Analytics        AnalyticsConfig
	ClickHouse       ClickHouseConfig
	PriceEngine      PriceEngineConfig
	System           PersistentSettings
}

func Load() *Config {
	root := detectRoot()
	clickHouseEnv := loadEnvFile(getEnv("CLICKHOUSE_CREDENTIAL_FILE", defaultClickHouseCredentialFile))
	clickHouseConfigured := strings.TrimSpace(clickHouseEnv["CLICKHOUSE_HOST"]) != "" &&
		strings.TrimSpace(clickHouseEnv["CLICKHOUSE_DATABASE"]) != "" &&
		strings.TrimSpace(clickHouseEnv["CLICKHOUSE_USER"]) != ""
	cfg := &Config{
		RootDir:          root,
		BackendDir:       filepath.Join(root, "backend"),
		UploadDir:        filepath.Join(root, "backend", "data", "uploads"),
		OutputDir:        filepath.Join(root, "backend", "data", "outputs"),
		LogDir:           filepath.Join(root, "backend", "data", "logs"),
		RuleSamplesDir:   filepath.Join(root, "backend", "data", "rule_samples"),
		ConfigDir:        filepath.Join(root, "backend", "config"),
		CustomRulesPath:  filepath.Join(root, "backend", "config", "custom_rules.json"),
		FrontendDistDir:  filepath.Join(root, "frontend", "dist"),
		FlowTemplatePath: filepath.Join(root, "tmp", "flow_template.xlsx"),
		ServerPort:       getEnv("PORT", "8000"),
		ConcurrencyLevel: runtime.NumCPU() * 2,
		MaxFileSize:      500 * 1024 * 1024, // 500MB
		Debug:            os.Getenv("DEBUG") == "1",
		Analytics: AnalyticsConfig{
			DuckDBPath:          getEnv("DUCKDB_PATH", ""),
			DuckDBDatabase:      getEnv("DUCKDB_DATABASE", ""),
			DuckDBReaderEnabled: envBool(nil, "DUCKDB_READER_ENABLED", false),
			DataSource:          normalizedDataSource(getEnv("EXPLORER_DATASOURCE", "clickhouse")),
		},
		ClickHouse: ClickHouseConfig{
			Enabled:        envBool(clickHouseEnv, "CLICKHOUSE_ENABLED", clickHouseConfigured),
			Required:       envBool(clickHouseEnv, "CLICKHOUSE_REQUIRED", false),
			Host:           envValue(clickHouseEnv, "CLICKHOUSE_HOST", "127.0.0.1"),
			HTTPPort:       envInt(clickHouseEnv, "CLICKHOUSE_HTTP_PORT", 8123),
			Database:       envValue(clickHouseEnv, "CLICKHOUSE_DATABASE", "onchain"),
			User:           envValue(clickHouseEnv, "CLICKHOUSE_USER", "default"),
			Password:       envValue(clickHouseEnv, "CLICKHOUSE_PASSWORD", ""),
			ConnectTimeout: envDuration(clickHouseEnv, "CLICKHOUSE_CONNECT_TIMEOUT", 5*time.Second),
			RequestTimeout: envDuration(clickHouseEnv, "CLICKHOUSE_REQUEST_TIMEOUT", 30*time.Second),
			MaxConnections: envInt(clickHouseEnv, "CLICKHOUSE_MAX_CONNECTIONS", 10),
		},
		PriceEngine: PriceEngineConfig{
			Enabled:         envBool(clickHouseEnv, "PRICE_ENGINE_ENABLED", true),
			RootDir:         getEnv("PRICE_ENGINE_ROOT", `E:\database\price_engine`),
			BinanceBaseURL:  getEnv("PRICE_BINANCE_BASE_URL", "https://data.binance.vision"),
			MaxQueryWindow:  envDuration(nil, "PRICE_MAX_QUERY_WINDOW", 31*24*time.Hour),
			MaxBatchItems:   envInt(nil, "PRICE_MAX_BATCH_ITEMS", 500),
			MinLiquidityUSD: getEnv("PRICE_MIN_LIQUIDITY_USD", "1000"),
			MaxDeviationPct: getEnv("PRICE_MAX_DEVIATION_PCT", "25"),
			MaxLastKnownAge: envDuration(nil, "PRICE_MAX_LAST_KNOWN_AGE", 24*time.Hour),
			DownloadTimeout: envDuration(nil, "PRICE_DOWNLOAD_TIMEOUT", 2*time.Minute),
		},
	}
	cfg.System = SettingsFromConfig(cfg)
	if saved, ok, err := LoadPersistentSettings(cfg.ConfigDir); err == nil && ok {
		cfg.System = saved
		cfg.ConcurrencyLevel = saved.ConcurrencyLevel
		cfg.MaxFileSize = int64(saved.MaxFileSizeMB) * 1024 * 1024
		cfg.Analytics.DataSource = saved.AnalyticsDataSource
		cfg.ClickHouse.Enabled = saved.ClickHouseEnabled
		cfg.ClickHouse.Required = saved.ClickHouseRequired
		cfg.PriceEngine.Enabled = saved.PriceEngineEnabled
	}
	return cfg
}

func normalizedDataSource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "duckdb", "auto", "clickhouse":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "clickhouse"
	}
}

func loadEnvFile(path string) map[string]string {
	values := make(map[string]string)
	file, err := os.Open(path)
	if err != nil {
		return values
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values
}

func envValue(fileValues map[string]string, key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	if value := fileValues[key]; value != "" {
		return value
	}
	return fallback
}

func envBool(fileValues map[string]string, key string, fallback bool) bool {
	value := envValue(fileValues, key, "")
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(fileValues map[string]string, key string, fallback int) int {
	value, err := strconv.Atoi(envValue(fileValues, key, ""))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envDuration(fileValues map[string]string, key string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(envValue(fileValues, key, ""))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func detectRoot() string {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	// Walk up to find go.mod or root marker
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, "frontend")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	cwd, _ := os.Getwd()
	return cwd
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
