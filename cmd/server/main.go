package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	addr := ":" + cfg.ServerPort
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	// Graceful shutdown
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		logger.Log.Info().Msg("shutting_down")
		// Give in-flight requests up to 3 seconds to complete
		// Short timeout because taskkill /F expects fast exit; port must be released quickly for restart
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Log.Error().Err(err).Msg("shutdown_error")
		}
		logger.Close()
		os.Exit(0)
	}()

	// Start server
	logger.Log.Info().Str("addr", addr).Str("root", cfg.RootDir).Msg("server_start")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Log.Fatal().Err(err).Msg("server_failed")
	}
}
