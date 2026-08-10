package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/etl/backend/internal/clickhouseexport"
)

var clickHouseExport *clickhouseexport.Service

func setupClickHouseExport() {
	if clickHouseClient == nil || cfg == nil || !cfg.ClickHouse.Enabled {
		return
	}
	service, err := clickhouseexport.New(clickHouseClient)
	if err != nil {
		return
	}
	clickHouseExport = service
}

func registerClickHouseExportRoutes(api *gin.RouterGroup) {
	api.POST("/v1/exports", handleClickHouseExportStart)
	api.GET("/v1/exports", handleClickHouseExportList)
	api.GET("/v1/exports/:id", handleClickHouseExportGet)
	api.POST("/v1/exports/:id/cancel", handleClickHouseExportCancel)
	api.GET("/v1/exports/:id/download", handleClickHouseExportDownload)
	api.DELETE("/v1/exports/:id", handleClickHouseExportRemove)
}

func handleClickHouseExportStart(c *gin.Context) {
	if clickHouseExport == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse export service is unavailable"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var request clickhouseexport.Request
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid export request"})
		return
	}
	task, err := clickHouseExport.Start(request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, task)
}

func handleClickHouseExportList(c *gin.Context) {
	if clickHouseExport == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse export service is unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tasks": clickHouseExport.List()})
}

func handleClickHouseExportGet(c *gin.Context) {
	if clickHouseExport == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse export service is unavailable"})
		return
	}
	task, ok := clickHouseExport.Get(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"detail": "export task not found"})
		return
	}
	c.JSON(http.StatusOK, task)
}

func handleClickHouseExportCancel(c *gin.Context) {
	if clickHouseExport == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse export service is unavailable"})
		return
	}
	if err := clickHouseExport.Cancel(c.Param("id")); err != nil {
		writeClickHouseExportError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "cancelling"})
}

func handleClickHouseExportDownload(c *gin.Context) {
	if clickHouseExport == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse export service is unavailable"})
		return
	}
	stream, task, err := clickHouseExport.Open(c.Param("id"))
	if err != nil {
		writeClickHouseExportError(c, err)
		return
	}
	defer stream.Close()
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+task.FileName+`"`)
	if task.Bytes > 0 {
		c.Header("Content-Length", strconv.FormatInt(task.Bytes, 10))
	}
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, stream)
}

func handleClickHouseExportRemove(c *gin.Context) {
	if clickHouseExport == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse export service is unavailable"})
		return
	}
	if err := clickHouseExport.Remove(c.Param("id")); err != nil {
		writeClickHouseExportError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func writeClickHouseExportError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, clickhouseexport.ErrTaskNotFound):
		c.JSON(http.StatusNotFound, gin.H{"detail": err.Error()})
	case errors.Is(err, clickhouseexport.ErrNotReady), errors.Is(err, clickhouseexport.ErrTaskRunning), errors.Is(err, clickhouseexport.ErrDownloadActive):
		c.JSON(http.StatusConflict, gin.H{"detail": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "ClickHouse export operation failed"})
	}
}
