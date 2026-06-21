package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

const dunePublicExecutionHMACKey = "public-01K4W0KHXNC30MKZMRZY91HKM6"

var errDunePublicExecutionPending = errors.New("dune public execution pending")

type dunePublicExecutionRequest struct {
	ExecutionID string               `json:"execution_id"`
	QueryID     int64                `json:"query_id"`
	Parameters  []interface{}        `json:"parameters"`
	Pagination  dunePublicPagination `json:"pagination"`
	Timestamp   int64                `json:"ts"`
	Signature   string               `json:"s"`
}

type dunePublicPagination struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type dunePublicExecutionResponse struct {
	ExecutionSucceeded *dunePublicExecutionSucceeded `json:"execution_succeeded"`
	ExecutionQueued    interface{}                   `json:"execution_queued"`
	ExecutionRunning   interface{}                   `json:"execution_running"`
	ExecutionFailed    interface{}                   `json:"execution_failed"`
}

type dunePublicExecutionSucceeded struct {
	ExecutionID   string                   `json:"execution_id"`
	Columns       []string                 `json:"columns"`
	ColumnTypes   []string                 `json:"column_types"`
	Data          []map[string]interface{} `json:"data"`
	TotalRowCount int                      `json:"total_row_count"`
}

func fetchDunePublicExecutionPage(ctx context.Context, cookie, executionID string, queryID int64, offset, limit int) (duneResultResponse, error) {
	executionID = strings.TrimSpace(executionID)
	cookie = strings.TrimSpace(cookie)
	if executionID == "" {
		return duneResultResponse{}, fmt.Errorf("execution_id required")
	}
	if queryID <= 0 {
		return duneResultResponse{}, fmt.Errorf("query_id required")
	}
	if cookie == "" {
		return duneResultResponse{}, errDuneAuthRequired
	}

	body := dunePublicExecutionRequest{
		ExecutionID: executionID,
		QueryID:     queryID,
		Parameters:  []interface{}{},
		Pagination: dunePublicPagination{
			Limit:  normalizeDuneLimit(limit),
			Offset: maxInt(offset, 0),
		},
		Timestamp: time.Now().UnixMilli(),
	}
	body.Signature = signDunePublicExecution(body)

	data, err := json.Marshal(body)
	if err != nil {
		return duneResultResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, duneWebBaseURL+"/public/execution", bytes.NewReader(data))
	if err != nil {
		return duneResultResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://dune.com")
	req.Header.Set("Referer", "https://dune.com/")
	req.Header.Set("Cookie", cookie)

	resp, err := duneHTTPClient.Do(req)
	if err != nil {
		return duneResultResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		respStr := strings.TrimSpace(string(respBody))
		isCloudflare := strings.Contains(respStr, "Just a moment") || strings.Contains(respStr, "challenge-platform")
		if isCloudflare {
			log.Info().Msg("dune_execution_cloudflare_detected_trying_playwright")
			if pwResult, pwErr := duneExecutionViaPlaywright(ctx, body); pwErr == nil {
				return pwResult, nil
			} else {
				log.Warn().Err(pwErr).Msg("dune_playwright_execution_fallback_failed")
			}
		}
		return duneResultResponse{}, errDuneAuthRequired
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return duneResultResponse{}, fmt.Errorf("Dune public execution request failed (HTTP %d): %s", resp.StatusCode, readDuneErrorBody(resp.Body))
	}

	var payload dunePublicExecutionResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return duneResultResponse{}, fmt.Errorf("Dune public execution response parse failed: %w", err)
	}
	if payload.ExecutionFailed != nil {
		return duneResultResponse{}, fmt.Errorf("Dune public execution failed")
	}
	if payload.ExecutionSucceeded == nil {
		return duneResultResponse{}, errDunePublicExecutionPending
	}
	return dunePublicExecutionToResult(*payload.ExecutionSucceeded, executionID, body.Pagination.Offset, body.Pagination.Limit), nil
}

func signDunePublicExecution(body dunePublicExecutionRequest) string {
	message := strconv.FormatInt(body.Timestamp, 10) +
		body.ExecutionID +
		strconv.FormatInt(body.QueryID, 10) +
		strconv.Itoa(body.Pagination.Limit) +
		strconv.Itoa(body.Pagination.Offset)
	mac := hmac.New(sha256.New, []byte(dunePublicExecutionHMACKey))
	_, _ = mac.Write([]byte(message))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func dunePublicExecutionToResult(payload dunePublicExecutionSucceeded, fallbackExecutionID string, offset, limit int) duneResultResponse {
	rowCount := len(payload.Data)
	total := payload.TotalRowCount
	if total < rowCount {
		total = rowCount
	}
	next := offset + rowCount
	var nextOffset *int
	if rowCount > 0 && next < total {
		nextOffset = &next
	}
	result := duneResultResponse{
		ExecutionID: firstDuneNonEmpty(payload.ExecutionID, fallbackExecutionID),
		State:       "QUERY_STATE_COMPLETED",
		NextOffset:  nextOffset,
	}
	if nextOffset != nil {
		result.NextURI = fmt.Sprintf("/public/execution?offset=%d&limit=%d", *nextOffset, limit)
	}
	result.Result.Metadata.ColumnNames = payload.Columns
	result.Result.Metadata.ColumnTypes = payload.ColumnTypes
	result.Result.Metadata.RowCount = rowCount
	result.Result.Metadata.TotalRows = total
	result.Result.Rows = payload.Data
	return result
}
