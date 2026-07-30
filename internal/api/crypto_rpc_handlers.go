package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func HandleCryptoRPC(c *gin.Context) {
	if rpcAPI == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "RPC 管理服务当前不可用"})
		return
	}
	rpcAPI.ServeHTTP(c.Writer, c.Request)
}
