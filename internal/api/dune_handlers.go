package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

var (
	duneAPIBaseURL = "https://api.dune.com/api/v1"
	duneWebBaseURL = "https://dune.com"
	duneHTTPClient = &http.Client{Timeout: 60 * time.Second}
)

type duneSQLDownloadRequest struct {
	SQL                 string `json:"sql"`
	Performance         string `json:"performance"`
	APIKey              string `json:"api_key"`
	TimeoutSeconds      int    `json:"timeout_seconds"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	AllowPartialResults bool   `json:"allow_partial_results"`
}

type duneExecuteResponse struct {
	ExecutionID string `json:"execution_id"`
	State       string `json:"state"`
}

type duneStatusResponse struct {
	ExecutionID         string           `json:"execution_id"`
	State               string           `json:"state"`
	IsExecutionFinished *bool            `json:"is_execution_finished"`
	Error               *duneStatusError `json:"error"`
}

type duneStatusError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func HandleDuneSQLDownload(c *gin.Context) {
	var payload duneSQLDownloadRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request: " + err.Error()})
		return
	}

	payload.SQL = strings.TrimSpace(payload.SQL)
	if payload.SQL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请输入 Dune SQL"})
		return
	}

	apiKey := strings.TrimSpace(payload.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("DUNE_API_KEY"))
	}
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "缺少 Dune API Key：请在页面填写，或设置服务端环境变量 DUNE_API_KEY"})
		return
	}

	performance := normalizeDunePerformance(payload.Performance)
	timeout := normalizeDurationSeconds(payload.TimeoutSeconds, 900, 30, 7200)
	pollInterval := normalizeDurationSeconds(payload.PollIntervalSeconds, 2, 1, 30)

	execution, err := executeDuneSQL(c.Request.Context(), apiKey, payload.SQL, performance)
	if err != nil {
		log.Warn().Err(err).Msg("dune_sql_execute_failed")
		c.JSON(http.StatusBadGateway, gin.H{"detail": err.Error()})
		return
	}
	if execution.ExecutionID == "" {
		c.JSON(http.StatusBadGateway, gin.H{"detail": "Dune 未返回 execution_id"})
		return
	}

	status, err := waitForDuneExecution(c.Request.Context(), apiKey, execution.ExecutionID, timeout, pollInterval)
	if err != nil {
		log.Warn().Err(err).Str("execution_id", execution.ExecutionID).Msg("dune_execution_wait_failed")
		c.JSON(http.StatusBadGateway, gin.H{"detail": err.Error(), "execution_id": execution.ExecutionID})
		return
	}

	csvResp, err := fetchDuneCSV(c.Request.Context(), apiKey, execution.ExecutionID, payload.AllowPartialResults || isDunePartialState(status.State))
	if err != nil {
		log.Warn().Err(err).Str("execution_id", execution.ExecutionID).Msg("dune_csv_fetch_failed")
		c.JSON(http.StatusBadGateway, gin.H{"detail": err.Error(), "execution_id": execution.ExecutionID})
		return
	}
	defer csvResp.Body.Close()

	contentType := csvResp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/csv; charset=utf-8"
	}
	filename := fmt.Sprintf("dune_%s.csv", safeName(execution.ExecutionID))
	c.Header("X-Dune-Execution-Id", execution.ExecutionID)
	c.DataFromReader(http.StatusOK, csvResp.ContentLength, contentType, csvResp.Body, map[string]string{
		"Content-Disposition": fmt.Sprintf(`attachment; filename="%s"`, filename),
	})
}

func executeDuneSQL(ctx context.Context, apiKey, sql, performance string) (duneExecuteResponse, error) {
	body := map[string]string{
		"sql":         sql,
		"performance": performance,
	}
	var result duneExecuteResponse
	err := doDuneJSON(ctx, http.MethodPost, "/sql/execute", apiKey, body, &result)
	return result, err
}

func waitForDuneExecution(ctx context.Context, apiKey, executionID string, timeout, pollInterval time.Duration) (duneStatusResponse, error) {
	deadline := time.Now().Add(timeout)
	var last duneStatusResponse
	for {
		var status duneStatusResponse
		if err := doDuneJSON(ctx, http.MethodGet, "/execution/"+executionID+"/status", apiKey, nil, &status); err != nil {
			return last, err
		}
		last = status
		if isDuneCompletedState(status.State) {
			return status, nil
		}
		if isDunePartialState(status.State) {
			return status, nil
		}
		if isDuneFailedState(status.State) {
			return status, fmt.Errorf("Dune SQL 执行失败：%s", duneStatusErrorMessage(status))
		}
		if time.Now().After(deadline) {
			return status, fmt.Errorf("等待 Dune SQL 执行超时，execution_id=%s，当前状态=%s", executionID, status.State)
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func fetchDuneCSV(ctx context.Context, apiKey, executionID string, allowPartial bool) (*http.Response, error) {
	path := "/execution/" + executionID + "/results/csv"
	if allowPartial {
		path += "?allow_partial_results=true"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, duneAPIBaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Dune-Api-Key", apiKey)
	resp, err := duneHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, fmt.Errorf("Dune CSV 下载失败（HTTP %d）：%s", resp.StatusCode, readDuneErrorBody(resp.Body))
	}
	return resp, nil
}

func doDuneJSON(ctx context.Context, method, path, apiKey string, body interface{}, out interface{}) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, duneAPIBaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Dune-Api-Key", apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := duneHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Dune API 请求失败（HTTP %d）：%s", resp.StatusCode, readDuneErrorBody(resp.Body))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("Dune API 响应解析失败：%w", err)
	}
	return nil
}

func readDuneErrorBody(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil || len(data) == 0 {
		return "无错误详情"
	}
	return strings.TrimSpace(string(data))
}

func normalizeDunePerformance(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "small", "medium", "large":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "medium"
	}
}

func normalizeDurationSeconds(value, fallback, minValue, maxValue int) time.Duration {
	if value <= 0 {
		value = fallback
	}
	if value < minValue {
		value = minValue
	}
	if value > maxValue {
		value = maxValue
	}
	return time.Duration(value) * time.Second
}

func isDuneCompletedState(state string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(state))
	return normalized == "QUERY_STATE_COMPLETED" || normalized == "COMPLETED" || normalized == "SUCCESS"
}

func isDunePartialState(state string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(state))
	return normalized == "QUERY_STATE_COMPLETED_PARTIAL" || normalized == "PARTIAL" || normalized == "COMPLETED_PARTIAL"
}

func isDuneFailedState(state string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(state))
	return strings.Contains(normalized, "FAILED") ||
		strings.Contains(normalized, "CANCELED") ||
		strings.Contains(normalized, "CANCELLED") ||
		strings.Contains(normalized, "EXPIRED")
}

func duneStatusErrorMessage(status duneStatusResponse) string {
	if status.Error != nil {
		if status.Error.Message != "" {
			return status.Error.Message
		}
		if status.Error.Type != "" {
			return status.Error.Type
		}
	}
	if status.State != "" {
		return status.State
	}
	return "unknown error"
}
