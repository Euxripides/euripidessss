package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/etl/backend/internal/api"
	"github.com/etl/backend/internal/config"
	"github.com/etl/backend/internal/logger"
	"github.com/etl/backend/internal/rules"
)

func main() {
	// Load config
	cfg := config.Load()

	// Setup logger
	if err := logger.Setup(cfg.LogDir); err != nil {
		panic("failed to setup logger: " + err.Error())
	}

	// Set custom rules path
	rules.SetCustomRulesPath(cfg.CustomRulesPath)

	// Setup API
	api.Setup(cfg)

	// Create router
	router := api.NewRouter()

	// Ensure data directories exist
	dirs := []string{cfg.UploadDir, cfg.OutputDir, cfg.LogDir, cfg.RuleSamplesDir, cfg.ConfigDir}
	for _, d := range dirs {
		os.MkdirAll(d, 0755)
	}

	// Graceful shutdown
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		logger.Close()
		os.Exit(0)
	}()

	// Start server
	addr := ":" + cfg.ServerPort
	logger.Log.Info().Str("addr", addr).Str("root", cfg.RootDir).Msg("server_start")
	if err := router.Run(addr); err != nil {
		logger.Log.Fatal().Err(err).Msg("server_failed")
	}
}
