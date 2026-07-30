package parquetdownload

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func defaultSettings(root string) Settings {
	return Settings{
		DataRoot:            filepath.Join(root, "backend", "data", "crypto_parquet"),
		DownloadConcurrency: 3,
		DuckDBThreads:       minInt(14, maxInt(4, runtime.NumCPU())),
		MemoryLimit:         "20GB",
		MinimumFreeGB:       150,
		KeepSourceFiles:     false,
		ExportCSV:           false,
	}
}

func validateSettings(settings Settings) (Settings, error) {
	settings.DataRoot = filepath.Clean(strings.TrimSpace(settings.DataRoot))
	if settings.DataRoot == "" || !filepath.IsAbs(settings.DataRoot) {
		return settings, errors.New("Parquet 数据根目录必须是明确的绝对路径")
	}
	volume := strings.ToUpper(filepath.VolumeName(settings.DataRoot))
	systemDrive := strings.ToUpper(strings.TrimSpace(os.Getenv("SystemDrive")))
	if systemDrive == "" {
		systemDrive = "C:"
	}
	if volume == systemDrive {
		return settings, fmt.Errorf("禁止将 Parquet 业务数据写入系统盘：%s", settings.DataRoot)
	}
	if settings.DownloadConcurrency < 1 || settings.DownloadConcurrency > 4 {
		return settings, errors.New("下载并发必须在 1 到 4 之间")
	}
	if settings.DuckDBThreads < 1 || settings.DuckDBThreads > 32 {
		return settings, errors.New("DuckDB 线程数必须在 1 到 32 之间")
	}
	if !validMemoryLimit(settings.MemoryLimit) {
		return settings, errors.New("DuckDB 内存限制格式错误，请使用 4GB、20GB 等格式")
	}
	if settings.MinimumFreeGB < 10 {
		return settings, errors.New("最低保留空间不能小于 10 GB")
	}
	return settings, nil
}

func validMemoryLimit(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	if !strings.HasSuffix(value, "GB") {
		return false
	}
	value = strings.TrimSuffix(value, "GB")
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != "0"
}

func ensureDataDirectories(settings Settings) error {
	for _, dir := range []string{
		settings.DataRoot,
		filepath.Join(settings.DataRoot, "jobs"),
		filepath.Join(settings.DataRoot, "staging"),
		filepath.Join(settings.DataRoot, "warehouse"),
		filepath.Join(settings.DataRoot, "checkpoints"),
		filepath.Join(settings.DataRoot, "exports"),
		filepath.Join(settings.DataRoot, "tmp"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("创建 Parquet 目录 %s: %w", dir, err)
		}
	}
	return nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
