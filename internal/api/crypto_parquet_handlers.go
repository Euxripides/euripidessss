package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func HandleCryptoParquet(c *gin.Context) {
	if parquetDownload == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "Parquet 下载服务不可用，请检查数据盘配置和 DuckDB CLI"})
		return
	}
	http.StripPrefix("/api/crypto/parquet", parquetDownload).ServeHTTP(c.Writer, c.Request)
}

func HandleAddressAnalytics(c *gin.Context) {
	if parquetDownload == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "链上地址分析服务不可用，请检查数据盘配置和 DuckDB CLI"})
		return
	}
	http.StripPrefix("/api", parquetDownload).ServeHTTP(c.Writer, c.Request)
}

func HandleFirstSeen(c *gin.Context) {
	if parquetDownload == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "链上地址分析服务不可用，请检查数据盘配置和 DuckDB CLI"})
		return
	}
	http.StripPrefix("/api", parquetDownload).ServeHTTP(c.Writer, c.Request)
}

// HandleAnalyticsAPI 转发 /api/analytics/* 到分析服务（V2.1 RC2）。
func HandleAnalyticsAPI(c *gin.Context) {
	if analyticsAPI == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "分析服务不可用：warehouse 数据未就绪"})
		return
	}
	http.StripPrefix("/api", analyticsAPI).ServeHTTP(c.Writer, c.Request)
}

// HandleDynamicInvestigation 转发 /api/dynamic-investigation/* 到动态调查引擎（V2.1 RC2）。
func HandleDynamicInvestigation(c *gin.Context) {
	if dynamicInvestigationAPI == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "动态调查引擎未就绪"})
		return
	}
	http.StripPrefix("/api", dynamicInvestigationAPI).ServeHTTP(c.Writer, c.Request)
}

// HandleIntelligence 转发 /api/intelligence/* 到全自动链上调查平台（V2.1 RC2）。
func HandleIntelligence(c *gin.Context) {
	if intelligenceAPI == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "调查平台未就绪"})
		return
	}
	http.StripPrefix("/api", intelligenceAPI).ServeHTTP(c.Writer, c.Request)
}
