package config

import (
	"os"
	"path/filepath"
	"runtime"
)

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
}

func Load() *Config {
	root := detectRoot()
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
	}
	return cfg
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
