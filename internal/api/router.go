package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"*"},
		AllowCredentials: true,
	}))

	// Register API routes
	RegisterRoutes(r)
	registerDuneBatchRoutes(r.Group("/api"))
	r.GET("/assets/tokens/:chain/:file", handleTokenAsset)

	// 前端连通性自检页：不依赖 React，用于区分网络/代理与扩展问题
	r.GET("/__test.html", func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<!doctype html><html lang="zh-CN"><head><meta charset="UTF-8"><title>前端自检</title></head>
<body style="font-family:system-ui;padding:24px">
<h2 id="s">正在检查…</h2>
<p id="t"></p>
<script>
document.getElementById('s').textContent = '前端服务正常 200';
document.getElementById('t').textContent = '时间：' + new Date().toISOString() + ' · 地址：' + location.href + ' · JS 执行正常';
</script>
</body></html>`))
	})

	// Serve frontend static files
	staticDir := cfg.FrontendDistDir
	if _, err := os.Stat(staticDir); os.IsNotExist(err) {
		staticDir = filepath.Dir(cfg.RootDir) // fallback
	}
	if _, err := os.Stat(staticDir); err == nil {
		r.NoRoute(func(c *gin.Context) {
			if c.Request.URL.Path == "/api" || strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.JSON(http.StatusNotFound, gin.H{"detail": "api route not found"})
				return
			}
			path := filepath.Join(staticDir, c.Request.URL.Path)
			if _, err := os.Stat(path); err == nil {
				applyStaticCacheHeaders(c, path)
				c.File(path)
				return
			}
			// SPA fallback
			indexPath := filepath.Join(staticDir, "index.html")
			if _, err := os.Stat(indexPath); err == nil {
				c.Header("Cache-Control", "no-cache")
				c.File(indexPath)
				return
			}
			c.Status(http.StatusNotFound)
		})
	}

	return r
}

// applyStaticCacheHeaders 为静态资源设置缓存头：
// index.html 强制 no-cache（防止 Chrome 缓存旧入口），hash 资源长期缓存。
func applyStaticCacheHeaders(c *gin.Context, path string) {
	base := filepath.Base(path)
	if base == "index.html" || c.Request.URL.Path == "/" {
		c.Header("Cache-Control", "no-cache")
		return
	}
	if strings.Contains(c.Request.URL.Path, "/assets/") {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	}
}
