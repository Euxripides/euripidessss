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
