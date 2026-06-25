package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

const duneGraphQLPath = "/public/graphql?operationName="

type duneGraphQLRequest struct {
	OperationName string                 `json:"operationName"`
	Variables     map[string]interface{} `json:"variables"`
	Extensions    map[string]interface{} `json:"extensions"`
	Query         string                 `json:"query"`
}

type duneWebUpdateResponse struct {
	Data struct {
		UpdateQuery struct {
			ID int64 `json:"id"`
		} `json:"updateQuery"`
	} `json:"data"`
}

type duneWebExecuteResponse struct {
	Data struct {
		ExecuteQuery struct {
			JobID string `json:"job_id"`
		} `json:"executeQuery"`
	} `json:"data"`
}

type duneWebCreateResponse struct {
	Data struct {
		CreateQuery struct {
			ID int64 `json:"id"`
		} `json:"createQuery"`
	} `json:"data"`
}

const defaultDuneDatasetID = 11

func executeDuneWebQueryWithRetry(ctx context.Context, payload *duneQueryRequest) (duneResultResponse, string, error) {
	webAuth, err := resolveDuneWebAuth(payload.Cookie, payload.Authorization, payload.AccessToken)
	if err != nil {
		return duneResultResponse{}, "", err
	}

	// Auto-refresh tokens if expired (using Playwright browser session)
	// TODO: re-enable after profile is populated with user's login session
	// if refreshed, err := ensureDuneTokensFresh(ctx); err == nil && refreshed {
	// 	if refreshedAuth, reErr := resolveDuneWebAuth(payload.Cookie, payload.Authorization, payload.AccessToken); reErr == nil {
	// 		webAuth = refreshedAuth
	// 	}
	// }

	autoCreated := payload.QueryID <= 0
	if err := resolveDuneWebQueryIDs(ctx, webAuth, payload); err != nil {
		return duneResultResponse{}, "", err
	}
	if err := validateDuneWebQueryRequest(*payload); err != nil {
		return duneResultResponse{}, "", err
	}
	var lastResult duneResultResponse
	var executionID string
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if !autoCreated {
			if attemptErr := updateDuneWebQuery(ctx, webAuth, *payload); attemptErr != nil {
				lastErr = attemptErr
				if isDuneAuthError(attemptErr) {
					break
				}
				log.Warn().Err(attemptErr).Int("attempt", attempt+1).Msg("dune_web_update_failed")
				continue
			}
		}
		executionID, attemptErr := executeDuneWebQuery(ctx, webAuth, *payload)
		if attemptErr == nil {
			timeout := normalizeDurationSeconds(payload.TimeoutSeconds, 900, 30, 7200)
			poll := normalizeDurationSeconds(payload.PollIntervalSeconds, 2, 1, 30)
			lastResult, attemptErr = waitForDunePublicExecution(ctx, webAuth.Cookie, executionID, payload.QueryID, normalizeDuneLimit(payload.Limit), timeout, poll)
			if attemptErr == nil {
				return lastResult, executionID, nil
			}
		}
		lastErr = attemptErr
		if isDuneAuthError(attemptErr) {
			break
		}
	}
	return lastResult, executionID, fmt.Errorf("Dune 官网查询失败，已重试 2 次：%w", lastErr)
}

// ensureDuneTokensFresh checks if stored Dune tokens are expired and refreshes them via Playwright.
func ensureDuneTokensFresh(ctx context.Context) (bool, error) {
	stored, err := loadDuneStoredAuth()
	if err != nil || stored.Cookie == "" {
		return false, nil // no stored tokens, skip
	}

	// Extract auth-id-token from cookie and check expiry
	idToken := duneCookieValue(stored.Cookie, "auth-id-token")
	if idToken == "" {
		return false, nil
	}

	// Parse JWT payload to check expiry
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return false, nil
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false, nil
	}
	var jwtPayload struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payloadBytes, &jwtPayload); err != nil {
		return false, nil
	}

	// Refresh if expired or expiring within 60 seconds
	remaining := time.Until(time.Unix(jwtPayload.Exp, 0))
	if remaining > 60*time.Second {
		return false, nil
	}

	log.Info().Int64("remaining_sec", int64(remaining.Seconds())).Msg("dune_token_expiring_refreshing")
	return true, refreshDuneTokens(ctx)
}

func refreshDuneTokens(ctx context.Context) error {
	scriptPath := dunePlaywrightBridgePath()
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return fmt.Errorf("Playwright bridge not found at %s", scriptPath)
	}

	stored, _ := loadDuneStoredAuth()

	input := map[string]interface{}{
		"mode":      "refresh",
		"timeoutMs": 60000,
	}
	if stored.Cookie != "" {
		input["cookie"] = stored.Cookie
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "node", scriptPath)
	cmd.Stdin = bytes.NewReader(inputJSON)
	cmd.Dir = filepath.Dir(scriptPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		log.Warn().Str("stderr", truncateStr(stderrStr, 200)).Msg("dune_token_refresh_failed")
		return fmt.Errorf("token refresh failed: %s", truncateStr(stderrStr, 200))
	}

	var fresh struct {
		Cookie        string `json:"cookie"`
		Authorization string `json:"authorization"`
		AccessToken   string `json:"access_token"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &fresh); err != nil {
		return fmt.Errorf("token refresh parse failed: %w", err)
	}
	if fresh.Cookie == "" {
		return fmt.Errorf("token refresh returned empty cookie — not logged in to Dune?")
	}

	// Update auth.json
	auth := duneStoredAuth{
		Cookie:        fresh.Cookie,
		Authorization: fresh.Authorization,
		AccessToken:   fresh.AccessToken,
		UpdatedAt:     time.Now().UTC(),
	}
	// Preserve existing team_id and api_key
	if stored, err := loadDuneStoredAuth(); err == nil {
		auth.TeamID = stored.TeamID
		auth.APIKey = stored.APIKey
	}
	if err := saveDuneStoredAuth(auth); err != nil {
		return fmt.Errorf("token refresh save failed: %w", err)
	}

	log.Info().Int("cookie_len", len(auth.Cookie)).Int("token_len", len(auth.AccessToken)).Msg("dune_token_refreshed")
	return nil
}

func resolveDuneWebQueryIDs(ctx context.Context, auth duneWebAuth, payload *duneQueryRequest) error {
	if payload.QueryID > 0 {
		return nil
	}
	if payload.DatasetID <= 0 {
		payload.DatasetID = defaultDuneDatasetID
	}
	if payload.QueryVersion <= 0 {
		payload.QueryVersion = 1
	}
	if payload.TeamID <= 0 {
		// Try stored team_id from auth.json first
		if stored, err := loadDuneStoredAuth(); err == nil && stored.TeamID > 0 {
			payload.TeamID = stored.TeamID
			log.Info().Int64("team_id", stored.TeamID).Msg("dune_use_stored_team_id")
		}
	}
	if payload.TeamID <= 0 {
		if tid, err := fetchDuneWebDefaultTeam(ctx, auth); err == nil {
			payload.TeamID = tid
			log.Info().Int64("team_id", tid).Msg("dune_auto_detect_team_ok")
		} else {
			log.Warn().Err(err).Msg("dune_auto_detect_team_failed")
			if isDuneAuthError(err) {
				return fmt.Errorf("Dune 鉴权校验失败，Cookie/Token 可能已过期，请在左侧面板重新保存：%w", err)
			}
		}
	}
	if payload.TeamID <= 0 {
		return fmt.Errorf("Dune 查询自动创建失败，无法获取团队 ID，请手动填写 team_id")
	}
	err := createDuneWebQuery(ctx, auth, payload)
	if err == nil {
		log.Info().Int64("query_id", payload.QueryID).Msg("dune_auto_create_query_ok")
		return nil
	}
	if isDuneAuthError(err) {
		log.Warn().Err(err).Msg("dune_create_query_auth_failed")
		return fmt.Errorf("Dune 鉴权校验失败，Cookie/Token 可能已过期，请在左侧面板重新保存：%w", err)
	}
	log.Warn().Err(err).Msg("dune_create_query_failed")
	return fmt.Errorf("Dune 查询自动创建失败：%w", err)
}

func fetchDuneWebDefaultTeam(ctx context.Context, auth duneWebAuth) (int64, error) {
	queries := []struct {
		name  string
		query string
	}{
		{"GetTeams", "query GetTeams {\n  teams {\n    edges {\n      node {\n        id\n        name\n        __typename\n      }\n    }\n  }\n}"},
	}
	type teamNode struct {
		ID int64 `json:"id"`
	}
	type teamEdge struct {
		Node teamNode `json:"node"`
	}
	type teamConnection struct {
		Edges []teamEdge `json:"edges"`
	}
	var lastErr error
	allAuthErr := true
	for _, q := range queries {
		body := duneGraphQLRequest{
			OperationName: q.name,
			Extensions:    duneApolloExtensions(),
			Query:         q.query,
		}
		var resp struct {
			Data struct {
				Teams teamConnection `json:"teams"`
			} `json:"data"`
		}
		if err := doDuneWebGraphQL(ctx, q.name, auth, body, &resp); err != nil {
			log.Warn().Str("query", q.name).Err(err).Msg("dune_team_query_attempt_failed")
			lastErr = err
			if !isDuneAuthError(err) {
				allAuthErr = false
			}
			continue
		}
		if len(resp.Data.Teams.Edges) > 0 {
			return resp.Data.Teams.Edges[0].Node.ID, nil
		}
	}
	if allAuthErr && lastErr != nil {
		return 0, fmt.Errorf("Dune 自动获取团队失败，鉴权可能已过期：%w", lastErr)
	}
	return 0, fmt.Errorf("Dune 自动获取团队失败")
}

func createDuneWebQuery(ctx context.Context, auth duneWebAuth, payload *duneQueryRequest) error {
	queryInput := map[string]interface{}{
		"name":        "opencode query",
		"description": "",
		"isTemp":      true,
		"isPrivate":   false,
		"query":       payload.SQL,
		"parameters":  []interface{}{},
		"datasetId":   payload.DatasetID,
		"userId":      nil,
	}
	if payload.TeamID > 0 {
		queryInput["teamId"] = payload.TeamID
	}
	body := duneGraphQLRequest{
		OperationName: "CreateQuery",
		Variables: map[string]interface{}{
			"query": queryInput,
		},
		Extensions: duneApolloExtensions(),
		Query:      "mutation CreateQuery($query: CreateQueryInput!) {\n  createQuery(query: $query) {\n    id\n    __typename\n  }\n}",
	}
	var result duneWebCreateResponse
	if err := doDuneWebGraphQL(ctx, "CreateQuery", auth, body, &result); err != nil {
		return fmt.Errorf("Dune 自动创建查询失败：%w", err)
	}
	if result.Data.CreateQuery.ID <= 0 {
		return fmt.Errorf("Dune CreateQuery 未返回有效 id")
	}
	payload.QueryID = result.Data.CreateQuery.ID
	payload.QueryVersion = 1
	return nil
}

func validateDuneWebQueryRequest(payload duneQueryRequest) error {
	if payload.QueryID <= 0 {
		return fmt.Errorf("官网查询链路需要 query_id")
	}
	if payload.TeamID <= 0 {
		return fmt.Errorf("官网查询链路需要 team_id")
	}
	if payload.DatasetID <= 0 {
		return fmt.Errorf("官网查询链路需要 dataset_id")
	}
	if payload.QueryVersion <= 0 {
		return fmt.Errorf("官网查询链路需要 query_version")
	}
	return nil
}

func updateDuneWebQuery(ctx context.Context, auth duneWebAuth, payload duneQueryRequest) error {
	body := duneGraphQLRequest{
		OperationName: "UpdateQuery",
		Variables: map[string]interface{}{
			"query": map[string]interface{}{
				"id":          payload.QueryID,
				"name":        "New query",
				"description": "",
				"isTemp":      true,
				"isPrivate":   false,
				"isArchived":  false,
				"datasetId":   payload.DatasetID,
				"query":       payload.SQL,
				"parameters":  []interface{}{},
				"tags":        []interface{}{},
				"version":     payload.QueryVersion,
				"userId":      nil,
				"teamId":      payload.TeamID,
			},
		},
		Extensions: duneApolloExtensions(),
		Query:      "mutation UpdateQuery($query: UpdateQueryInput!) {\n  updateQuery(input: $query) {\n    id\n    __typename\n  }\n}",
	}
	var result duneWebUpdateResponse
	if err := doDuneWebGraphQL(ctx, "UpdateQuery", auth, body, &result); err != nil {
		return err
	}
	if result.Data.UpdateQuery.ID != payload.QueryID {
		return fmt.Errorf("Dune UpdateQuery returned unexpected id")
	}
	return nil
}

func executeDuneWebQuery(ctx context.Context, auth duneWebAuth, payload duneQueryRequest) (string, error) {
	body := duneGraphQLRequest{
		OperationName: "ExecuteQuery",
		Variables: map[string]interface{}{
			"query_id":      payload.QueryID,
			"parameters":    []interface{}{},
			"executor":      map[string]interface{}{"id": payload.TeamID, "type": "team"},
			"performance":   normalizeDuneWebPerformance(payload.Performance),
			"executionType": "interactive",
		},
		Extensions: duneApolloExtensions(),
		Query:      "mutation ExecuteQuery($query_id: Int!, $executor: ContextOwner!, $performance: String!, $parameters: [ExecuteQueryParameterInput!]!, $executionType: String!, $metadata: JSON) {\n  executeQuery(input: {queryId: $query_id, executor: $executor, performance: $performance, parameters: $parameters, executionType: $executionType, metadata: $metadata}) {\n    job_id: id\n    __typename\n  }\n}",
	}
	var result duneWebExecuteResponse
	if err := doDuneWebGraphQL(ctx, "ExecuteQuery", auth, body, &result); err != nil {
		return "", err
	}
	if strings.TrimSpace(result.Data.ExecuteQuery.JobID) == "" {
		return "", fmt.Errorf("Dune ExecuteQuery 未返回 job_id")
	}
	return result.Data.ExecuteQuery.JobID, nil
}

func waitForDunePublicExecution(ctx context.Context, cookie, executionID string, queryID int64, limit int, timeout, pollInterval time.Duration) (duneResultResponse, error) {
	deadline := time.Now().Add(timeout)
	for {
		result, err := fetchDunePublicExecutionPage(ctx, cookie, executionID, queryID, 0, limit)
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, errDunePublicExecutionPending) {
			return duneResultResponse{}, err
		}
		if time.Now().After(deadline) {
			return duneResultResponse{}, fmt.Errorf("等待 Dune 官网执行超时，execution_id=%s", executionID)
		}
		select {
		case <-ctx.Done():
			return duneResultResponse{}, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func doDuneWebGraphQL(ctx context.Context, operation string, auth duneWebAuth, body duneGraphQLRequest, out interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, duneWebBaseURL+duneGraphQLPath+operation, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/graphql-response+json,application/json;q=0.9")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://dune.com")
	req.Header.Set("Referer", fmt.Sprintf("https://dune.com/queries/%d", queryIDFromGraphQLBody(body)))
	req.Header.Set("Cookie", auth.Cookie)
	req.Header.Set("Authorization", auth.Authorization)
	req.Header.Set("X-Dune-Access-Token", auth.AccessToken)
	resp, err := duneHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		respStr := strings.TrimSpace(string(respBody))
		isCloudflare := strings.Contains(respStr, "Just a moment") || strings.Contains(respStr, "challenge-platform")
		log.Warn().Str("operation", operation).Int("status", resp.StatusCode).Int("body_len", len(respStr)).Bool("cloudflare", isCloudflare).Str("body", truncateStr(respStr, 200)).Msg("dune_web_graphql_auth_rejected")
		if isCloudflare {
			log.Info().Str("operation", operation).Msg("dune_cloudflare_detected_trying_playwright")
			if pwErr := doDuneWebGraphQLViaPlaywright(ctx, operation, auth, body, out); pwErr == nil {
				return nil
			} else {
				log.Warn().Err(pwErr).Msg("dune_playwright_fallback_failed")
			}
		}
		return fmt.Errorf("%w：Dune 官网拒绝请求 (HTTP %d)，Cookie/Token 可能已过期，请重新保存：%s", errDuneAuthRequired, resp.StatusCode, truncateStr(respStr, 500))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		respStr := strings.TrimSpace(string(respBody))
		return fmt.Errorf("Dune 官网 GraphQL 请求失败（HTTP %d）：%s", resp.StatusCode, truncateStr(respStr, 500))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("Dune 官网 GraphQL 响应解析失败：%w", err)
	}
	return nil
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func doDuneWebGraphQLViaPlaywright(ctx context.Context, operation string, auth duneWebAuth, body duneGraphQLRequest, out interface{}) error {
	return doDuneViaPlaywright(ctx, "graphql", operation, auth, body, out)
}

func doDuneExecutionViaPlaywright(ctx context.Context, execBody interface{}, out interface{}) error {
	return doDuneViaPlaywright(ctx, "execution", "", duneWebAuth{}, execBody, out)
}

// duneExecutionViaPlaywright polls execution via Playwright and returns parsed result
func duneExecutionViaPlaywright(ctx context.Context, reqBody dunePublicExecutionRequest) (duneResultResponse, error) {
	data, _ := json.Marshal(reqBody)
	input := map[string]interface{}{
		"mode":      "execution",
		"body":      json.RawMessage(data),
		"timeoutMs": 60000,
	}

	scriptPath := dunePlaywrightBridgePath()
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return duneResultResponse{}, fmt.Errorf("Playwright bridge script not found at %s", scriptPath)
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return duneResultResponse{}, err
	}

	cmd := exec.CommandContext(ctx, "node", scriptPath)
	cmd.Stdin = bytes.NewReader(inputJSON)
	cmd.Dir = filepath.Dir(scriptPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			var pwErr struct {
				Error string `json:"error"`
			}
			if json.Unmarshal([]byte(stderrStr), &pwErr) == nil && pwErr.Error != "" {
				return duneResultResponse{}, fmt.Errorf("Dune Playwright 请求失败：%s", pwErr.Error)
			}
			return duneResultResponse{}, fmt.Errorf("Dune Playwright 请求失败：%s", truncateStr(stderrStr, 500))
		}
		return duneResultResponse{}, fmt.Errorf("Dune Playwright 请求失败：%w", err)
	}

	var payload dunePublicExecutionResponse
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return duneResultResponse{}, fmt.Errorf("Dune Playwright 响应解析失败：%w", err)
	}
	if payload.ExecutionFailed != nil {
		return duneResultResponse{}, fmt.Errorf("Dune public execution failed")
	}
	if payload.ExecutionSucceeded == nil {
		return duneResultResponse{}, errDunePublicExecutionPending
	}
	return dunePublicExecutionToResult(*payload.ExecutionSucceeded, reqBody.ExecutionID, reqBody.Pagination.Offset, reqBody.Pagination.Limit), nil
}

func doDuneViaPlaywright(ctx context.Context, mode string, operation string, auth duneWebAuth, body interface{}, out interface{}) error {
	scriptPath := dunePlaywrightBridgePath()
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return fmt.Errorf("Playwright bridge script not found at %s", scriptPath)
	}

	input := map[string]interface{}{
		"mode":      mode,
		"timeoutMs": 60000,
	}
	if auth.Cookie != "" {
		input["cookie"] = auth.Cookie
	}
	if auth.Authorization != "" {
		input["authorization"] = auth.Authorization
	}
	if auth.AccessToken != "" {
		input["accessToken"] = auth.AccessToken
	}
	if mode == "graphql" {
		gqlBody, ok := body.(duneGraphQLRequest)
		if !ok {
			return fmt.Errorf("invalid graphql body type")
		}
		input["operationName"] = operation
		input["query"] = gqlBody.Query
		input["variables"] = gqlBody.Variables
		input["extensions"] = gqlBody.Extensions
	} else if mode == "execution" {
		input["body"] = body
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("playwright marshal input: %w", err)
	}

	cmd := exec.CommandContext(ctx, "node", scriptPath)
	cmd.Stdin = bytes.NewReader(inputJSON)
	cmd.Dir = filepath.Dir(scriptPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			var pwErr struct {
				Error string `json:"error"`
			}
			if json.Unmarshal([]byte(stderrStr), &pwErr) == nil && pwErr.Error != "" {
				return fmt.Errorf("Dune Playwright 请求失败：%s", pwErr.Error)
			}
			return fmt.Errorf("Dune Playwright 请求失败：%s", truncateStr(stderrStr, 500))
		}
		return fmt.Errorf("Dune Playwright 请求失败：%w", err)
	}

	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("Dune Playwright 响应解析失败：%w，body=%s", err, truncateStr(stdout.String(), 500))
	}
	return nil
}

func DoDuneWebGraphQLViaPlaywright(ctx context.Context, operation string, auth duneWebAuth, body duneGraphQLRequest, out interface{}) error {
	return doDuneWebGraphQLViaPlaywright(ctx, operation, auth, body, out)
}

func dunePlaywrightBridgePath() string {
	root := "."
	if cfg != nil {
		root = cfg.RootDir
	}
	return filepath.Join(root, "backend", "data", "dune", "playwright_bridge.js")
}

func duneApolloExtensions() map[string]interface{} {
	return map[string]interface{}{
		"clientLibrary": map[string]interface{}{"name": "@apollo/client", "version": "4.1.6"},
	}
}

func queryIDFromGraphQLBody(body duneGraphQLRequest) int64 {
	if id, ok := body.Variables["query_id"].(int64); ok {
		return id
	}
	query, ok := body.Variables["query"].(map[string]interface{})
	if !ok {
		return 0
	}
	if id, ok := query["id"].(int64); ok {
		return id
	}
	return 0
}

func normalizeDuneWebPerformance(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "free", "small", "medium", "large":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "free"
	}
}
