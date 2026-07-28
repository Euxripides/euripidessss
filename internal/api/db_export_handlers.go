package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/etl/backend/internal/dbimport"
)

func HandleDBCreateExportTask(c *gin.Context) {
	var request dbimport.ExportRequest
	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求参数不是有效 JSON"})
		return
	}
	sourcePath, totalRows, err := resolveFinalCSVArtifact(request.JobID)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	task, err := dbExportManager.CreateAndStart(request, sourcePath, totalRows)
	if err != nil {
		dbError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusAccepted, task)
}

func HandleDBGetExportTask(c *gin.Context) {
	task, err := dbExportManager.Get(c.Param("id"))
	if err != nil {
		dbError(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, task)
}

func HandleDBCancelExportTask(c *gin.Context) {
	task, err := dbExportManager.Cancel(c.Param("id"))
	if err != nil {
		dbError(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, task)
}

func resolveFinalCSVArtifact(jobID string) (string, int64, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`).MatchString(jobID) {
		return "", 0, fmt.Errorf("清洗任务 ID 无效")
	}
	manifestPath := filepath.Join(cfg.OutputDir, "etl_jobs", jobID, "artifacts.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", 0, err
	}
	var artifacts []persistedArtifact
	if err := json.Unmarshal(data, &artifacts); err != nil {
		return "", 0, err
	}
	outputRoot, err := filepath.Abs(cfg.OutputDir)
	if err != nil {
		return "", 0, err
	}
	for _, artifact := range artifacts {
		if artifact.ID != "final-csv" {
			continue
		}
		path, err := filepath.Abs(artifact.Path)
		if err != nil {
			return "", 0, err
		}
		relative, err := filepath.Rel(outputRoot, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", 0, os.ErrPermission
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			if err != nil {
				return "", 0, err
			}
			return "", 0, os.ErrNotExist
		}
		return path, artifact.Rows, nil
	}
	return "", 0, &finalCSVNotFoundError{}
}

type finalCSVNotFoundError struct{}

func (*finalCSVNotFoundError) Error() string {
	return "该任务没有统一字段后的最终 CSV；请勾选“统一字段名后合并不同来源”并重新清洗"
}
