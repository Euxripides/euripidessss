package pricing

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/etl/backend/internal/config"
)

type EngineConfig struct {
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

func EngineConfigFromApp(value config.PriceEngineConfig) EngineConfig {
	return EngineConfig{
		Enabled: value.Enabled, RootDir: value.RootDir, BinanceBaseURL: value.BinanceBaseURL,
		MaxQueryWindow: value.MaxQueryWindow, MaxBatchItems: value.MaxBatchItems,
		MinLiquidityUSD: value.MinLiquidityUSD, MaxDeviationPct: value.MaxDeviationPct,
		MaxLastKnownAge: value.MaxLastKnownAge, DownloadTimeout: value.DownloadTimeout,
	}
}

type EnginePaths struct {
	Root, Config, Cache, BinanceCache, SQDCache, AWSCache, RPCCache string
	Staging, SwapStaging, KlineStaging, NormalizedStaging           string
	Checkpoint, Manifests, Logs, Export, Temp                       string
}

func PrepareEnginePaths(root string) (EnginePaths, error) {
	clean := filepath.Clean(strings.TrimSpace(root))
	if clean == "." || !filepath.IsAbs(clean) || !strings.EqualFold(filepath.VolumeName(clean), "E:") {
		return EnginePaths{}, fmt.Errorf("%w: price engine root must be an absolute E-drive path", ErrInvalidInput)
	}
	p := EnginePaths{
		Root: clean, Config: filepath.Join(clean, "config"), Cache: filepath.Join(clean, "cache"),
		BinanceCache: filepath.Join(clean, "cache", "binance"), SQDCache: filepath.Join(clean, "cache", "sqd"),
		AWSCache: filepath.Join(clean, "cache", "aws"), RPCCache: filepath.Join(clean, "cache", "rpc"),
		Staging: filepath.Join(clean, "staging"), SwapStaging: filepath.Join(clean, "staging", "swap"),
		KlineStaging: filepath.Join(clean, "staging", "kline"), NormalizedStaging: filepath.Join(clean, "staging", "normalized"),
		Checkpoint: filepath.Join(clean, "checkpoint"), Manifests: filepath.Join(clean, "manifests"),
		Logs: filepath.Join(clean, "logs"), Export: filepath.Join(clean, "export"), Temp: filepath.Join(clean, "temp"),
	}
	for _, dir := range []string{p.Config, p.BinanceCache, p.SQDCache, p.AWSCache, p.RPCCache, p.SwapStaging, p.KlineStaging, p.NormalizedStaging, p.Checkpoint, p.Manifests, p.Logs, p.Export, p.Temp} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return EnginePaths{}, fmt.Errorf("create price engine directory %s: %w", dir, err)
		}
	}
	return p, nil
}

type ProviderState struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type EngineHealth struct {
	Status     string          `json:"status"`
	ClickHouse string          `json:"clickhouse"`
	Root       string          `json:"root"`
	Providers  []ProviderState `json:"providers"`
}
