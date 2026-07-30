package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func HandleCryptoDataSource(c *gin.Context) {
	if dataSourceAPI == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "数据源管理服务当前不可用"})
		return
	}
	dataSourceAPI.ServeHTTP(c.Writer, c.Request)
}
