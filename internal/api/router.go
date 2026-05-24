package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// NewRouter creates a new Gin router with CORS and routes configured
func NewRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// Request logging middleware
	r.Use(func(c *gin.Context) {
		log.Info().Str("method", c.Request.Method).Str("path", c.Request.URL.Path).Msg("request_start")
		c.Next()
		status := c.Writer.Status()
		log.Info().Str("method", c.Request.Method).Str("path", c.Request.URL.Path).Int("status", status).Msg("request_end")
	})

	// CORS
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"*"},
		AllowCredentials: true,
	}))

	// Register API routes
	RegisterRoutes(r)

	// Serve frontend static files
	staticDir := cfg.FrontendDistDir
	if _, err := os.Stat(staticDir); os.IsNotExist(err) {
		staticDir = filepath.Dir(cfg.RootDir) // fallback
	}
	if _, err := os.Stat(staticDir); err == nil {
		r.NoRoute(func(c *gin.Context) {
			path := filepath.Join(staticDir, c.Request.URL.Path)
			if _, err := os.Stat(path); err == nil {
				c.File(path)
				return
			}
			// SPA fallback
			indexPath := filepath.Join(staticDir, "index.html")
			if _, err := os.Stat(indexPath); err == nil {
				c.File(indexPath)
				return
			}
			c.Status(http.StatusNotFound)
		})
	}

	return r
}
