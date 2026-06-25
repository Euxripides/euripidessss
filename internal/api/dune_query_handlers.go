package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

var errDuneAuthRequired = errors.New("dune auth required")

type duneQueryRequest struct {
	SQL                 string `json:"sql"`
	APIKey              string `json:"api_key"`
	Cookie              string `json:"cookie"`
	Authorization       string `json:"authorization"`
	AccessToken         string `json:"access_token"`
	AccountEmail        string `json:"account_email"`
	WebQuery            bool   `json:"web_query"`
	QueryID             int64  `json:"query_id"`
	TeamID              int64  `json:"team_id"`
	DatasetID           int64  `json:"dataset_id"`
	QueryVersion        int    `json:"query_version"`
	Performance         string `json:"performance"`
	TimeoutSeconds      int    `json:"timeout_seconds"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	AllowPartialResults bool   `json:"allow_partial_results"`
	Limit               int    `json:"limit"`
}

type dunePageRequest struct {
	ExecutionID         string `json:"execution_id"`
	APIKey              string `json:"api_key"`
	Cookie              string `json:"cookie"`
	QueryID             int64  `json:"query_id"`
	Offset              int    `json:"offset"`
	Limit               int    `json:"limit"`
	AllowPartialResults bool   `json:"allow_partial_results"`
}

type duneResultResponse struct {
	ExecutionID string `json:"execution_id"`
	State       string `json:"state"`
	NextOffset  *int   `json:"next_offset"`
	NextURI     string `json:"next_uri"`
	Result      struct {
		Metadata duneResultMetadata       `json:"metadata"`
		Rows     []map[string]interface{} `json:"rows"`
	} `json:"result"`
}

type duneResultMetadata struct {
	ColumnNames []string `json:"column_names"`
	ColumnTypes []string `json:"column_types"`
	RowCount    int      `json:"row_count"`
	TotalRows   int      `json:"total_row_count"`
}

func HandleDuneSQLQuery(c *gin.Context) {
	var payload duneQueryRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request: " + err.Error()})
		return
	}
	payload.SQL = strings.TrimSpace(payload.SQL)
	if payload.SQL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请输入 Dune SQL"})
		return
	}
	if err := applyDuneAccountAuth(c.Request.Context(), &payload); err != nil {
		writeDuneAPIError(c, err)
		return
	}
	if payload.WebQuery {
		result, executionID, err := executeDuneWebQueryWithRetry(c.Request.Context(), &payload)
		if err != nil {
			writeDuneAPIError(c, err)
			return
		}
		writeDuneResult(c, result, executionID, payload.QueryID)
		return
	}
	apiKey, err := resolveDuneAPIKey(payload.APIKey)
	if err != nil {
		writeDuneAuthRequired(c)
		return
	}
	result, status, err := executeDuneQueryWithRetry(c.Request.Context(), apiKey, payload)
	if err != nil {
		writeDuneAPIError(c, err)
		return
	}
	writeDuneResult(c, result, status.ExecutionID, payload.QueryID)
}

func HandleDuneResultPage(c *gin.Context) {
	var payload dunePageRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request: " + err.Error()})
		return
	}
	apiKey, _ := resolveDuneAPIKey(payload.APIKey)
	result, err := fetchDunePreviewPage(c.Request.Context(), apiKey, payload.Cookie, payload.ExecutionID, payload.QueryID, payload.Offset, normalizeDuneLimit(payload.Limit), payload.AllowPartialResults)
	if err != nil {
		writeDuneAPIError(c, err)
		return
	}
	writeDuneResult(c, result, payload.ExecutionID, payload.QueryID)
}

func executeDuneQueryWithRetry(ctx context.Context, apiKey string, payload duneQueryRequest) (duneResultResponse, duneStatusResponse, error) {
	var lastResult duneResultResponse
	var lastStatus duneStatusResponse
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		execution, err := executeDuneSQL(ctx, apiKey, payload.SQL, normalizeDunePerformance(payload.Performance))
		if err == nil {
			timeout := normalizeDurationSeconds(payload.TimeoutSeconds, 900, 30, 7200)
			poll := normalizeDurationSeconds(payload.PollIntervalSeconds, 2, 1, 30)
			lastStatus, err = waitForDuneExecution(ctx, apiKey, execution.ExecutionID, timeout, poll)
			if err == nil {
				lastResult, err = fetchDunePreviewPage(ctx, apiKey, payload.Cookie, execution.ExecutionID, payload.QueryID, 0, normalizeDuneLimit(payload.Limit), payload.AllowPartialResults || isDunePartialState(lastStatus.State))
				if err == nil {
					return lastResult, lastStatus, nil
				}
			}
		}
		lastErr = err
		if isDuneAuthError(err) {
			break
		}
		log.Warn().Err(err).Int("attempt", attempt+1).Msg("dune_query_attempt_failed")
	}
	return lastResult, lastStatus, fmt.Errorf("Dune 查询失败，已重试 2 次：%w", lastErr)
}

func fetchDuneResultPage(ctx context.Context, apiKey, executionID string, offset, limit int, allowPartial bool) (duneResultResponse, error) {
	if strings.TrimSpace(executionID) == "" {
		return duneResultResponse{}, fmt.Errorf("execution_id required")
	}
	values := url.Values{}
	values.Set("offset", strconv.Itoa(maxInt(offset, 0)))
	values.Set("limit", strconv.Itoa(normalizeDuneLimit(limit)))
	if allowPartial {
		values.Set("allow_partial_results", "true")
	}
	var result duneResultResponse
	err := doDuneJSONStatus(ctx, http.MethodGet, "/execution/"+url.PathEscape(executionID)+"/results?"+values.Encode(), apiKey, nil, &result)
	return result, err
}

func fetchDunePreviewPage(ctx context.Context, apiKey, cookie, executionID string, queryID int64, offset, limit int, allowPartial bool) (duneResultResponse, error) {
	if queryID > 0 {
		if resolvedCookie, err := resolveDuneCookie(cookie); err == nil {
			result, publicErr := fetchDunePublicExecutionPage(ctx, resolvedCookie, executionID, queryID, offset, normalizeDuneLimit(limit))
			if publicErr == nil {
				return result, nil
			}
			log.Warn().Err(publicErr).Str("execution_id", executionID).Int64("query_id", queryID).Msg("dune_public_execution_preview_failed")
		}
	}
	if strings.TrimSpace(apiKey) == "" {
		return duneResultResponse{}, fmt.Errorf("%w: Cookie 不可用且未配置 API Key，无法获取 Dune 查询结果", errDuneAuthRequired)
	}
	return fetchDuneResultPage(ctx, apiKey, executionID, offset, limit, allowPartial)
}

func writeDuneResult(c *gin.Context, result duneResultResponse, executionID string, queryID int64) {
	columns := result.Result.Metadata.ColumnNames
	labels := localizeDuneColumns(c.Request.Context(), columns)
	c.JSON(http.StatusOK, gin.H{
		"execution_id":    firstDuneNonEmpty(result.ExecutionID, executionID),
		"query_id":        queryID,
		"state":           result.State,
		"columns":         columns,
		"column_labels":   labels,
		"column_types":    result.Result.Metadata.ColumnTypes,
		"rows":            result.Result.Rows,
		"row_count":       result.Result.Metadata.RowCount,
		"total_row_count": result.Result.Metadata.TotalRows,
		"next_offset":     result.NextOffset,
		"next_uri":        result.NextURI,
	})
}

func doDuneJSONStatus(ctx context.Context, method, path, apiKey string, body interface{}, out interface{}) error {
	err := doDuneJSON(ctx, method, path, apiKey, body, out)
	if err != nil && strings.Contains(err.Error(), "HTTP 401") {
		return errDuneAuthRequired
	}
	if err != nil && strings.Contains(err.Error(), "HTTP 403") {
		return errDuneAuthRequired
	}
	return err
}

func writeDuneAPIError(c *gin.Context, err error) {
	if isDuneAuthError(err) {
		detail := err.Error()
		if detail == errDuneAuthRequired.Error() {
			detail = "Dune 鉴权不可用，请保存 API Key，或保存 Cookie 与官网 Token"
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"detail":        detail,
			"auth_required": true,
			"login_url":     "https://dune.com/",
		})
		return
	}
	c.JSON(http.StatusBadGateway, gin.H{"detail": err.Error()})
}

func writeDuneAuthRequired(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, gin.H{
		"detail":        "Dune 鉴权不可用，请保存 API Key，或保存 Cookie 与官网 Token",
		"auth_required": true,
		"login_url":     "https://dune.com/",
	})
}

func normalizeDuneLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

func isDuneAuthError(err error) bool {
	return errors.Is(err, errDuneAuthRequired)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func firstDuneNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
