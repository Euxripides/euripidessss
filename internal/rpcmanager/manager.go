package rpcmanager

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/etl/backend/internal/chain"
)

const healthCheckInterval = 30 * time.Minute

type Manager struct {
	store       *store
	secure      *secureStore
	client      *http.Client
	runtimesMu  sync.Mutex
	runtimes    map[string]*endpointRuntime
	cacheHits   atomic.Int64
	cacheMisses atomic.Int64
	turboCursor atomic.Uint64
	close       chan struct{}
	closed      chan struct{}
	jobMu       sync.Mutex
	jobCancel   map[string]context.CancelFunc
	jobWG       sync.WaitGroup
}

type endpointRuntime struct {
	mu             sync.Mutex
	preflightMu    sync.Mutex
	nextAllowed    time.Time
	currentRPS     float64
	maxWorkers     int
	workerLimit    int
	currentWorkers int
	workerNotify   chan struct{}
	latencies      []float64
	outcomes       []bool
	successCount   int64
	failureCount   int64
	rateLimitCount int64
	timeoutCount   int64
	circuitOpen    time.Time
	halfOpenActive bool
}

type rpcWireRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcWireResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type callError struct {
	class       string
	code        string
	message     string
	retryable   bool
	rateLimited bool
	timeout     bool
	auth        bool
}

func (e *callError) Error() string { return e.message }

func New(dataRoot string) (*Manager, error) {
	if strings.TrimSpace(dataRoot) == "" {
		return nil, errors.New("RPC data root is required")
	}
	dataRoot, err := validateDataRoot(dataRoot)
	if err != nil {
		return nil, err
	}
	secure, err := openSecureStore(filepath.Join(dataRoot, "config", "secure"))
	if err != nil {
		return nil, fmt.Errorf("初始化 RPC 安全存储: %w", err)
	}
	db, err := openStore(dataRoot)
	if err != nil {
		return nil, fmt.Errorf("初始化 RPC 控制库: %w", err)
	}
	manager := &Manager{
		store: db, secure: secure,
		client: &http.Client{Transport: &http.Transport{
			Proxy:        http.ProxyFromEnvironment,
			DialContext:  (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns: 32, MaxIdleConnsPerHost: 8, IdleConnTimeout: 60 * time.Second,
		}},
		runtimes: make(map[string]*endpointRuntime),
		close:    make(chan struct{}), closed: make(chan struct{}),
		jobCancel: make(map[string]context.CancelFunc),
	}
	go manager.healthLoop()
	return manager, nil
}

func (m *Manager) Close() error {
	m.jobMu.Lock()
	for _, cancel := range m.jobCancel {
		cancel()
	}
	m.jobMu.Unlock()
	select {
	case <-m.close:
	default:
		close(m.close)
	}
	<-m.closed
	m.jobWG.Wait()
	m.client.CloseIdleConnections()
	return m.store.close()
}

func (m *Manager) Create(ctx context.Context, input EndpointInput) (Endpoint, error) {
	input, network, endpointURL, err := validateEndpointInput(input)
	if err != nil {
		return Endpoint{}, err
	}
	testEndpointURL, err := validateOptionalEndpointURL(input.TestEndpointURL)
	if err != nil {
		return Endpoint{}, err
	}
	testURL, endpointRole := endpointURLForTest(endpointURL, testEndpointURL)
	test := m.testURL(ctx, input.Provider, network, testURL, time.Duration(input.RequestTimeoutMS)*time.Millisecond)
	test.EndpointRole = endpointRole
	if !test.Success {
		return Endpoint{}, fmt.Errorf("%s：%s", test.ErrorClass, test.ErrorMessage)
	}
	encrypted, err := m.secure.encrypt(endpointURL)
	if err != nil {
		return Endpoint{}, err
	}
	encryptedTest, err := m.encryptOptionalURL(testEndpointURL)
	if err != nil {
		return Endpoint{}, err
	}
	now := time.Now().UTC()
	item := endpointRecord{Endpoint: Endpoint{
		ID: newID("rpc"), Provider: input.Provider, ChainKey: network.Key, ChainID: network.ID,
		DisplayName: input.DisplayName, EndpointHost: endpointHost(endpointURL),
		EndpointMasked: maskEndpoint(endpointURL), SecretConfigured: true,
		TestEndpointMasked: maskOptionalEndpoint(testEndpointURL), TestEndpointConfigured: testEndpointURL != "",
		Priority: input.Priority, Enabled: input.Enabled, MaxRPS: input.MaxRPS,
		CurrentRPS: input.MaxRPS, MaxConcurrency: input.MaxConcurrency,
		RequestTimeoutMS: input.RequestTimeoutMS, CreatedAt: now, UpdatedAt: now,
		SupportedMethods: input.SupportedMethods, ArchiveCapability: input.ArchiveCapability,
		TraceCapability: input.TraceCapability,
	}, EncryptedURL: encrypted, EncryptedTestURL: encryptedTest,
		CapabilitiesConfigured: capabilitiesConfigured(input.SupportedMethods, input.ArchiveCapability, input.TraceCapability)}
	if err := m.store.insertEndpoint(item); err != nil {
		return Endpoint{}, err
	}
	if endpointRole == "PRIMARY" {
		health := healthFromTest(item.ID, test)
		if !item.Enabled {
			health.Status = StatusDisabled
		}
		_ = m.store.saveHealth(health)
	}
	return m.publicEndpoint(item), nil
}

func (m *Manager) Update(ctx context.Context, id string, patch EndpointPatch) (Endpoint, error) {
	item, err := m.store.endpoint(id)
	if err != nil {
		return Endpoint{}, err
	}
	input := EndpointInput{
		Provider: item.Provider, ChainKey: item.ChainKey, DisplayName: item.DisplayName,
		Priority: item.Priority, Enabled: item.Enabled, MaxRPS: item.MaxRPS,
		MaxConcurrency: item.MaxConcurrency, RequestTimeoutMS: item.RequestTimeoutMS,
		SupportedMethods:  append([]string(nil), item.SupportedMethods...),
		ArchiveCapability: item.ArchiveCapability, TraceCapability: item.TraceCapability,
	}
	if patch.Provider != nil {
		input.Provider = *patch.Provider
	}
	if patch.ChainKey != nil {
		input.ChainKey = *patch.ChainKey
	}
	if patch.DisplayName != nil {
		input.DisplayName = *patch.DisplayName
	}
	if patch.Priority != nil {
		input.Priority = *patch.Priority
	}
	if patch.Enabled != nil {
		input.Enabled = *patch.Enabled
	}
	if patch.MaxRPS != nil {
		input.MaxRPS = *patch.MaxRPS
	}
	if patch.MaxConcurrency != nil {
		input.MaxConcurrency = *patch.MaxConcurrency
	}
	if patch.RequestTimeoutMS != nil {
		input.RequestTimeoutMS = *patch.RequestTimeoutMS
	}
	if patch.SupportedMethods != nil {
		input.SupportedMethods = append([]string{}, (*patch.SupportedMethods)...)
	}
	if patch.ArchiveCapability != nil {
		input.ArchiveCapability = *patch.ArchiveCapability
	}
	if patch.TraceCapability != nil {
		input.TraceCapability = *patch.TraceCapability
	}
	existingURL, err := m.secure.decrypt(item.EncryptedURL)
	if err != nil {
		return Endpoint{}, err
	}
	input.EndpointURL = existingURL
	if len(item.EncryptedTestURL) > 0 {
		input.TestEndpointURL, err = m.secure.decrypt(item.EncryptedTestURL)
		if err != nil {
			return Endpoint{}, err
		}
	}
	if patch.EndpointURL != nil && strings.TrimSpace(*patch.EndpointURL) != "" {
		input.EndpointURL = *patch.EndpointURL
	}
	if patch.TestEndpointURL != nil {
		input.TestEndpointURL = *patch.TestEndpointURL
	}
	input, network, endpointURL, err := validateEndpointInput(input)
	if err != nil {
		return Endpoint{}, err
	}
	testEndpointURL, err := validateOptionalEndpointURL(input.TestEndpointURL)
	if err != nil {
		return Endpoint{}, err
	}
	if patch.EndpointURL != nil || patch.TestEndpointURL != nil || input.ChainKey != item.ChainKey || (patch.Enabled != nil && *patch.Enabled) {
		testURL, endpointRole := endpointURLForTest(endpointURL, testEndpointURL)
		test := m.testURL(ctx, input.Provider, network, testURL, time.Duration(input.RequestTimeoutMS)*time.Millisecond)
		test.EndpointRole = endpointRole
		if !test.Success {
			return Endpoint{}, fmt.Errorf("%s：%s", test.ErrorClass, test.ErrorMessage)
		}
		if endpointRole == "PRIMARY" {
			_ = m.store.saveHealth(healthFromTest(item.ID, test))
		}
	}
	encrypted, err := m.secure.encrypt(endpointURL)
	if err != nil {
		return Endpoint{}, err
	}
	encryptedTest, err := m.encryptOptionalURL(testEndpointURL)
	if err != nil {
		return Endpoint{}, err
	}
	item.Provider, item.ChainKey, item.ChainID = input.Provider, network.Key, network.ID
	item.DisplayName, item.EndpointHost = input.DisplayName, endpointHost(endpointURL)
	item.EndpointMasked, item.EncryptedURL = maskEndpoint(endpointURL), encrypted
	item.TestEndpointMasked, item.TestEndpointConfigured = maskOptionalEndpoint(testEndpointURL), testEndpointURL != ""
	item.EncryptedTestURL = encryptedTest
	item.Priority, item.Enabled, item.MaxRPS = input.Priority, input.Enabled, input.MaxRPS
	item.CurrentRPS, item.MaxConcurrency = input.MaxRPS, input.MaxConcurrency
	item.RequestTimeoutMS, item.UpdatedAt = input.RequestTimeoutMS, time.Now().UTC()
	item.SupportedMethods = append([]string(nil), input.SupportedMethods...)
	item.ArchiveCapability, item.TraceCapability = input.ArchiveCapability, input.TraceCapability
	if patch.SupportedMethods != nil || patch.ArchiveCapability != nil || patch.TraceCapability != nil {
		item.CapabilitiesConfigured = true
	}
	if err := m.store.updateEndpoint(item); err != nil {
		return Endpoint{}, err
	}
	if !item.Enabled {
		health := m.store.health(item.ID)
		health.Status = StatusDisabled
		_ = m.store.saveHealth(health)
	}
	m.resetRuntime(id)
	return m.publicEndpoint(item), nil
}

func (m *Manager) Delete(id string) error {
	m.resetRuntime(id)
	return m.store.deleteEndpoint(id)
}

func (m *Manager) Endpoints() ([]Endpoint, error) {
	records, err := m.store.endpoints("", false)
	if err != nil {
		return nil, err
	}
	result := make([]Endpoint, 0, len(records))
	for _, item := range records {
		result = append(result, m.publicEndpoint(item))
	}
	m.applyBlockLag(result)
	return result, nil
}

func (m *Manager) HasConfigured(chainKey string) bool {
	items, err := m.store.endpoints(chainKey, true)
	return err == nil && len(items) > 0
}

// HasAnyConfigured reports whether a chain has any persisted endpoint. Turbo
// uses this without changing the endpoint's normal enabled state.
func (m *Manager) HasAnyConfigured(chainKey string) bool {
	items, err := m.store.endpoints(chainKey, false)
	return err == nil && len(items) > 0
}

// SealSecret encrypts a non-RPC provider secret with the same machine-bound
// master key used by RPC endpoints. The returned value is safe to persist.
func (m *Manager) SealSecret(value string) (string, error) {
	encrypted, err := m.secure.encrypt(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func (m *Manager) OpenSecret(value string) (string, error) {
	encrypted, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", errors.New("数据源密钥密文格式错误")
	}
	return m.secure.decrypt(encrypted)
}

func (m *Manager) TestInput(ctx context.Context, input EndpointInput) TestResult {
	input, network, endpointURL, err := validateEndpointInput(input)
	if err != nil {
		return failedTest(input.Provider, input.ChainKey, "VALIDATION", err.Error())
	}
	testEndpointURL, err := validateOptionalEndpointURL(input.TestEndpointURL)
	if err != nil {
		return failedTest(input.Provider, input.ChainKey, "VALIDATION", err.Error())
	}
	testURL, endpointRole := endpointURLForTest(endpointURL, testEndpointURL)
	result := m.testURL(ctx, input.Provider, network, testURL, time.Duration(input.RequestTimeoutMS)*time.Millisecond)
	result.EndpointRole = endpointRole
	return result
}

func (m *Manager) TestEndpoint(ctx context.Context, id string) (TestResult, error) {
	item, err := m.store.endpoint(id)
	if err != nil {
		return TestResult{}, err
	}
	result, err := m.testStoredEndpoint(ctx, item, true)
	if err != nil {
		return TestResult{}, err
	}
	if result.EndpointRole == "PRIMARY" {
		health := healthFromTest(id, result)
		if !item.Enabled {
			health.Status = StatusDisabled
		}
		_ = m.store.saveHealth(health)
	}
	return result, nil
}

func (m *Manager) testStoredEndpoint(ctx context.Context, item endpointRecord, useConfiguredTest bool) (TestResult, error) {
	encryptedURL := item.EncryptedURL
	endpointRole := "PRIMARY"
	if useConfiguredTest && len(item.EncryptedTestURL) > 0 {
		encryptedURL = item.EncryptedTestURL
		endpointRole = "TEST"
	}
	endpointURL, err := m.secure.decrypt(encryptedURL)
	if err != nil {
		return TestResult{}, err
	}
	network, _ := chain.Resolve(item.ChainKey)
	result := m.testURL(ctx, item.Provider, network, endpointURL, time.Duration(item.RequestTimeoutMS)*time.Millisecond)
	result.EndpointRole = endpointRole
	return result, nil
}

func (m *Manager) encryptOptionalURL(endpointURL string) ([]byte, error) {
	if endpointURL == "" {
		return nil, nil
	}
	return m.secure.encrypt(endpointURL)
}

func (m *Manager) UpdateRouting(chainKey string, input RoutingInput) error {
	if _, err := chain.Resolve(chainKey); err != nil {
		return err
	}
	if len(input.EndpointIDs) == 0 {
		return errors.New("路由节点列表不能为空")
	}
	seen := make(map[string]bool, len(input.EndpointIDs))
	for _, id := range input.EndpointIDs {
		if seen[id] {
			return errors.New("路由节点不能重复")
		}
		seen[id] = true
	}
	return m.store.updateRouting(chainKey, input.EndpointIDs)
}

func (m *Manager) HealthResponse() (HealthResponse, error) {
	endpoints, err := m.Endpoints()
	if err != nil {
		return HealthResponse{}, err
	}
	routing := make(map[string][]Endpoint)
	for _, endpoint := range endpoints {
		routing[endpoint.ChainKey] = append(routing[endpoint.ChainKey], endpoint)
	}
	return HealthResponse{
		Overview:  m.store.overview(m.cacheHits.Load(), m.cacheMisses.Load()),
		Endpoints: endpoints, Routing: routing,
	}, nil
}

// PoolSnapshot returns a non-secret view of the per-endpoint adaptive pool.
// Disabled endpoints are included because Turbo may route to them, while the
// Enabled field preserves the distinction needed by normal scheduling.
func (m *Manager) PoolSnapshot(chainKey string) (PoolSnapshot, error) {
	network, err := chain.Resolve(chainKey)
	if err != nil {
		return PoolSnapshot{}, err
	}
	items, err := m.store.endpoints(network.Key, false)
	if err != nil {
		return PoolSnapshot{}, err
	}
	result := PoolSnapshot{ChainKey: network.Key, EndpointCount: len(items), Endpoints: make([]EndpointPoolSnapshot, 0, len(items))}
	methodSet := make(map[string]struct{})
	var latencies []float64
	var totalOutcomes int64
	for _, item := range items {
		runtime := m.runtime(item)
		runtime.mu.Lock()
		p50, p95 := percentiles(runtime.latencies)
		rate := successRate(runtime.outcomes)
		outcomes := runtime.successCount + runtime.failureCount
		endpoint := EndpointPoolSnapshot{
			EndpointID: item.ID, Provider: item.Provider, Enabled: item.Enabled,
			CurrentWorkers: runtime.currentWorkers, WorkerLimit: runtime.workerLimit,
			MaxWorkers: runtime.maxWorkers, CurrentRPS: runtime.currentRPS,
			LatencyMS: average(runtime.latencies), LatencyP50MS: p50, LatencyP95MS: p95, SuccessRate: rate,
			SuccessCount: runtime.successCount, RateLimitedCount: runtime.rateLimitCount,
			TimeoutCount: runtime.timeoutCount, SupportedMethods: append([]string(nil), item.SupportedMethods...),
			ArchiveCapability: item.ArchiveCapability, TraceCapability: item.TraceCapability,
			LegacyCompatibility: !item.CapabilitiesConfigured, TodayRequests: m.store.endpointRequestsToday(item.ID),
		}
		if outcomes > 0 {
			endpoint.Rate429 = float64(runtime.rateLimitCount) / float64(outcomes) * 100
			endpoint.TimeoutRate = float64(runtime.timeoutCount) / float64(outcomes) * 100
		}
		result.CurrentWorkers += runtime.currentWorkers
		result.WorkerLimit += runtime.workerLimit
		result.SuccessCount += runtime.successCount
		result.RateLimitedCount += runtime.rateLimitCount
		result.TimeoutCount += runtime.timeoutCount
		result.TodayRequests += endpoint.TodayRequests
		totalOutcomes += runtime.successCount + runtime.failureCount
		latencies = append(latencies, runtime.latencies...)
		runtime.mu.Unlock()
		result.ArchiveCapability = result.ArchiveCapability || item.ArchiveCapability
		result.TraceCapability = result.TraceCapability || item.TraceCapability
		for _, method := range item.SupportedMethods {
			methodSet[method] = struct{}{}
		}
		result.Endpoints = append(result.Endpoints, endpoint)
	}
	result.LatencyP50MS, result.LatencyP95MS = percentiles(latencies)
	result.LatencyMS = average(latencies)
	if totalOutcomes > 0 {
		result.SuccessRate = float64(result.SuccessCount) / float64(totalOutcomes) * 100
		result.Rate429 = float64(result.RateLimitedCount) / float64(totalOutcomes) * 100
		result.TimeoutRate = float64(result.TimeoutCount) / float64(totalOutcomes) * 100
	}
	for method := range methodSet {
		result.SupportedMethods = append(result.SupportedMethods, method)
	}
	sort.Strings(result.SupportedMethods)
	return result, nil
}

func (m *Manager) RefreshHealth(ctx context.Context) {
	items, err := m.store.endpoints("", true)
	if err != nil {
		return
	}
	var wait sync.WaitGroup
	for _, item := range items {
		item := item
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, testErr := m.testStoredEndpoint(ctx, item, false)
			if testErr != nil {
				return
			}
			health := healthFromTest(item.ID, result)
			if !item.Enabled {
				health.Status = StatusDisabled
			}
			_ = m.store.saveHealth(health)
		}()
	}
	wait.Wait()
}

func (m *Manager) Call(ctx context.Context, chainKey, method string, params any) (json.RawMessage, string, error) {
	return m.callWithPolicy(ctx, chainKey, method, params, false)
}

// CallTurbo routes over every configured endpoint, including endpoints that
// are disabled for normal traffic. It never persists Enabled=true and still
// enforces each endpoint's rate limit, concurrency, timeout and circuit state.
func (m *Manager) CallTurbo(ctx context.Context, chainKey, method string, params any) (json.RawMessage, string, error) {
	return m.callWithPolicy(ctx, chainKey, method, params, true)
}

func (m *Manager) callWithPolicy(ctx context.Context, chainKey, method string, params any, includeDisabled bool) (json.RawMessage, string, error) {
	items, err := m.store.endpoints(chainKey, !includeDisabled)
	if err != nil {
		return nil, "", err
	}
	if len(items) == 0 {
		if includeDisabled {
			return nil, "", errors.New("该链未配置 RPC 节点")
		}
		return nil, "", errors.New("该链未配置可用 RPC 节点")
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i].Health, items[j].Health
		if routeRank(left.Status) != routeRank(right.Status) {
			return routeRank(left.Status) < routeRank(right.Status)
		}
		return items[i].Priority < items[j].Priority
	})
	if includeDisabled && len(items) > 1 {
		start := int((m.turboCursor.Add(1) - 1) % uint64(len(items)))
		items = append(items[start:], items[:start]...)
	}
	var last error
	compatible := 0
	attempts := 0
	maxAttempts := 5
	if includeDisabled {
		maxAttempts = len(items) * 2
	}
	for _, item := range items {
		if attempts >= maxAttempts {
			break
		}
		if !endpointSupportsMethod(item, method) {
			continue
		}
		compatible++
		if preflightErr := m.ensureEndpointReady(ctx, item); preflightErr != nil {
			last = preflightErr
			continue
		}
		if !m.routeAllowed(item, includeDisabled) {
			continue
		}
		for endpointAttempt := 0; endpointAttempt < 2 && attempts < maxAttempts; endpointAttempt++ {
			attempts++
			result, callErr := m.callEndpoint(ctx, item, method, params)
			if callErr == nil {
				return result, item.ID, nil
			}
			last = callErr
			if !callErr.retryable || callErr.auth {
				break
			}
			backoff := time.Duration(500*(1<<endpointAttempt)) * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	if last == nil {
		if compatible == 0 {
			last = fmt.Errorf("没有 RPC 节点声明支持方法 %s", redactMessage(method))
		} else {
			last = errors.New("当前没有健康的 RPC 节点")
		}
	}
	return nil, "", fmt.Errorf("RPC_UNAVAILABLE: %w", last)
}

func (m *Manager) ensureEndpointReady(ctx context.Context, item endpointRecord) error {
	if healthIsFresh(m.store.health(item.ID), time.Now().UTC()) {
		return nil
	}
	runtime := m.runtime(item)
	runtime.preflightMu.Lock()
	defer runtime.preflightMu.Unlock()
	if healthIsFresh(m.store.health(item.ID), time.Now().UTC()) {
		return nil
	}
	result, err := m.testStoredEndpoint(ctx, item, false)
	if err != nil {
		return err
	}
	health := healthFromTest(item.ID, result)
	if !item.Enabled {
		health.Status = StatusDisabled
	}
	_ = m.store.saveHealth(health)
	if !result.Success {
		return fmt.Errorf("RPC_PREFLIGHT_%s: %s", result.ErrorClass, result.ErrorMessage)
	}
	return nil
}

func healthIsFresh(health Health, now time.Time) bool {
	if health.CheckedAt == nil {
		return false
	}
	age := now.Sub(health.CheckedAt.UTC())
	return age >= 0 && age < healthCheckInterval
}

func (m *Manager) callEndpoint(ctx context.Context, item endpointRecord, method string, params any) (json.RawMessage, *callError) {
	runtime := m.runtime(item)
	if err := runtime.acquire(ctx, item.MaxRPS); err != nil {
		return nil, &callError{class: "CANCELLED", message: err.Error()}
	}
	defer runtime.release()
	endpointURL, err := m.secure.decrypt(item.EncryptedURL)
	if err != nil {
		return nil, &callError{class: "DECRYPT", message: "节点密钥解密失败"}
	}
	timeout := time.Duration(item.RequestTimeoutMS) * time.Millisecond
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	result, callErr := rawRPCCall(callCtx, m.client, endpointURL, method, params)
	latency := time.Since(start)
	if callErr != nil {
		m.onFailure(item, runtime, method, latency, callErr)
		return nil, callErr
	}
	m.onSuccess(item, runtime, method, latency)
	return result, nil
}

func (m *Manager) testURL(ctx context.Context, provider string, network chain.EVM, endpointURL string, timeout time.Duration) TestResult {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	testCtx, cancel := context.WithTimeout(ctx, minDuration(timeout, 5*time.Second))
	defer cancel()
	start := time.Now()
	chainRaw, callErr := rawRPCCall(testCtx, m.client, endpointURL, "eth_chainId", []any{})
	if callErr != nil {
		return failedTest(provider, network.Key, callErr.class, callErr.message)
	}
	chainID, err := decodeHexUint(chainRaw)
	if err != nil {
		return failedTest(provider, network.Key, "INVALID_RESPONSE", "eth_chainId 返回值无法解析")
	}
	if int64(chainID) != network.ID {
		return failedTest(provider, network.Key, "CHAIN_ID_MISMATCH",
			fmt.Sprintf("节点 Chain ID 为 %d，配置链要求 %d", chainID, network.ID))
	}
	blockRaw, callErr := rawRPCCall(testCtx, m.client, endpointURL, "eth_blockNumber", []any{})
	if callErr != nil {
		return failedTest(provider, network.Key, callErr.class, callErr.message)
	}
	block, err := decodeHexUint(blockRaw)
	if err != nil {
		return failedTest(provider, network.Key, "INVALID_RESPONSE", "eth_blockNumber 返回值无法解析")
	}
	return TestResult{
		Success: true, Provider: provider, ChainKey: network.Key, ChainID: network.ID,
		LatestBlock: block, LatencyMS: time.Since(start).Milliseconds(), Status: StatusHealthy,
	}
}

func rawRPCCall(ctx context.Context, client *http.Client, endpointURL, method string, params any) (json.RawMessage, *callError) {
	payload, _ := json.Marshal(rpcWireRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(payload))
	if err != nil {
		return nil, classifyError(err, 0, "")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, classifyError(err, 0, "")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, classifyError(err, response.StatusCode, "")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, classifyError(errors.New(http.StatusText(response.StatusCode)), response.StatusCode, string(body))
	}
	var decoded rpcWireResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, &callError{class: "INVALID_RESPONSE", code: "JSON", message: "RPC 返回内容无法解析"}
	}
	if decoded.Error != nil {
		return nil, classifyRPCError(decoded.Error.Code, decoded.Error.Message)
	}
	if len(decoded.Result) == 0 || string(decoded.Result) == "null" {
		return nil, &callError{class: "EMPTY_RESULT", code: "NULL", message: "RPC 返回空结果"}
	}
	return decoded.Result, nil
}

func (m *Manager) onSuccess(item endpointRecord, runtime *endpointRuntime, method string, latency time.Duration) {
	now := time.Now().UTC()
	runtime.mu.Lock()
	latencyMS := float64(latency.Microseconds()) / 1000
	spike := latencySpike(item, runtime.latencies, latencyMS)
	if spike {
		runtime.multiplyDecrease()
	} else {
		runtime.currentRPS = math.Min(item.MaxRPS, runtime.currentRPS+0.1)
		if runtime.workerLimit < runtime.maxWorkers {
			runtime.workerLimit++
			runtime.signalWorkersLocked()
		}
	}
	runtime.latencies = appendBounded(runtime.latencies, latencyMS, 100)
	runtime.outcomes = appendBounded(runtime.outcomes, true, 100)
	runtime.successCount++
	runtime.circuitOpen = time.Time{}
	runtime.halfOpenActive = false
	p50, p95 := percentiles(runtime.latencies)
	successRate := successRate(runtime.outcomes)
	runtime.mu.Unlock()
	health := m.store.health(item.ID)
	health.Status, health.HealthScore = StatusHealthy, healthScore(successRate, p95)
	if spike {
		health.Status = StatusDegraded
	}
	if !item.Enabled {
		health.Status = StatusDisabled
	}
	health.LatencyP50MS, health.LatencyP95MS, health.SuccessRate5M = p50, p95, successRate
	health.ConsecutiveFailures, health.CircuitState, health.CircuitOpenUntil = 0, CircuitClosed, nil
	health.LastSuccessAt, health.CheckedAt = &now, &now
	health.LastErrorCode, health.LastErrorMessageRedacted = "", ""
	_ = m.store.saveHealth(health)
	m.store.recordMetric(item.ID, item.ChainID, method, true, false, false, latency)
}

func (m *Manager) onFailure(item endpointRecord, runtime *endpointRuntime, method string, latency time.Duration, callErr *callError) {
	now := time.Now().UTC()
	runtime.mu.Lock()
	runtime.outcomes = appendBounded(runtime.outcomes, false, 100)
	runtime.latencies = appendBounded(runtime.latencies, float64(latency.Microseconds())/1000, 100)
	runtime.failureCount++
	if callErr.rateLimited {
		runtime.rateLimitCount++
	}
	if callErr.timeout {
		runtime.timeoutCount++
	}
	if callErr.rateLimited || callErr.timeout || latencySpike(item, runtime.latencies[:len(runtime.latencies)-1], float64(latency.Microseconds())/1000) {
		runtime.multiplyDecrease()
	}
	p50, p95 := percentiles(runtime.latencies)
	rate := successRate(runtime.outcomes)
	outcomeCount := len(runtime.outcomes)
	runtime.mu.Unlock()
	health := m.store.health(item.ID)
	health.ConsecutiveFailures++
	health.LatencyP50MS, health.LatencyP95MS, health.SuccessRate5M = p50, p95, rate
	health.LastFailureAt, health.CheckedAt = &now, &now
	health.LastErrorCode, health.LastErrorMessageRedacted = callErr.code, redactMessage(callErr.message)
	health.HealthScore = healthScore(rate, p95)
	switch {
	case callErr.auth:
		health.Status, health.CircuitState = StatusMisconfigured, CircuitOpen
		open := now.Add(24 * time.Hour)
		health.CircuitOpenUntil = &open
	case callErr.rateLimited:
		health.Status, health.CircuitState = StatusRateLimited, CircuitOpen
		open := now.Add(60 * time.Second)
		health.CircuitOpenUntil = &open
	case health.ConsecutiveFailures >= 5 || (outcomeCount >= 20 && rate < 50):
		health.Status, health.CircuitState = StatusUnavailable, CircuitOpen
		open := now.Add(30 * time.Second)
		health.CircuitOpenUntil = &open
	default:
		health.Status, health.CircuitState = StatusDegraded, CircuitClosed
	}
	if !item.Enabled {
		health.Status = StatusDisabled
	}
	_ = m.store.saveHealth(health)
	m.store.recordMetric(item.ID, item.ChainID, method, false, callErr.rateLimited, callErr.timeout, latency)
}

func (m *Manager) routeAllowed(item endpointRecord, allowDisabled bool) bool {
	health := m.store.health(item.ID)
	if health.Status == StatusMisconfigured || (health.Status == StatusDisabled && !allowDisabled) {
		return false
	}
	if health.CircuitState != CircuitOpen || health.CircuitOpenUntil == nil {
		return true
	}
	if time.Now().UTC().Before(*health.CircuitOpenUntil) {
		return false
	}
	health.CircuitState = CircuitHalfOpen
	_ = m.store.saveHealth(health)
	return true
}

func endpointSupportsMethod(item endpointRecord, method string) bool {
	method = strings.TrimSpace(method)
	if method == "" {
		return false
	}
	if len(item.SupportedMethods) > 0 {
		matched := false
		for _, supported := range item.SupportedMethods {
			if supported == "*" || strings.EqualFold(supported, method) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	// NULL capability columns identify pre-V3.1 endpoints. They keep their
	// historical routing behavior until an operator explicitly configures them.
	if !item.CapabilitiesConfigured {
		return true
	}
	lower := strings.ToLower(method)
	if methodRequiresTrace(lower) && !item.TraceCapability {
		return false
	}
	if methodRequiresArchive(lower) && !item.ArchiveCapability {
		return false
	}
	return true
}

func methodRequiresTrace(method string) bool {
	return strings.HasPrefix(method, "trace_") || strings.HasPrefix(method, "debug_trace")
}

func methodRequiresArchive(method string) bool {
	switch method {
	case "eth_getlogs", "eth_getproof":
		return true
	default:
		return false
	}
}

func (m *Manager) runtime(item endpointRecord) *endpointRuntime {
	m.runtimesMu.Lock()
	defer m.runtimesMu.Unlock()
	if existing := m.runtimes[item.ID]; existing != nil {
		return existing
	}
	runtime := &endpointRuntime{
		currentRPS: item.MaxRPS, maxWorkers: item.MaxConcurrency,
		workerLimit: item.MaxConcurrency, workerNotify: make(chan struct{}),
	}
	m.runtimes[item.ID] = runtime
	return runtime
}

func (m *Manager) resetRuntime(id string) {
	m.runtimesMu.Lock()
	delete(m.runtimes, id)
	m.runtimesMu.Unlock()
}

func (r *endpointRuntime) acquire(ctx context.Context, maxRPS float64) error {
	for {
		r.mu.Lock()
		if r.workerNotify == nil {
			r.workerNotify = make(chan struct{})
		}
		if r.currentWorkers < r.workerLimit {
			r.currentWorkers++
			r.mu.Unlock()
			break
		}
		notify := r.workerNotify
		r.mu.Unlock()
		select {
		case <-notify:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.mu.Lock()
	rps := r.currentRPS
	if rps <= 0 {
		rps = maxRPS
	}
	wait := time.Until(r.nextAllowed)
	next := time.Now().Add(time.Duration(float64(time.Second) / math.Max(rps, 0.25)))
	if r.nextAllowed.After(time.Now()) {
		next = r.nextAllowed.Add(time.Duration(float64(time.Second) / math.Max(rps, 0.25)))
	}
	r.nextAllowed = next
	r.mu.Unlock()
	if wait > 0 {
		select {
		case <-ctx.Done():
			r.release()
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil
}

func (r *endpointRuntime) release() {
	r.mu.Lock()
	if r.currentWorkers > 0 {
		r.currentWorkers--
	}
	r.signalWorkersLocked()
	r.mu.Unlock()
}

func (r *endpointRuntime) multiplyDecrease() {
	r.currentRPS = math.Max(0.25, r.currentRPS/2)
	r.workerLimit = maxInt(1, (r.workerLimit+1)/2)
}

func (r *endpointRuntime) signalWorkersLocked() {
	close(r.workerNotify)
	r.workerNotify = make(chan struct{})
}

func latencySpike(item endpointRecord, previous []float64, latencyMS float64) bool {
	if item.RequestTimeoutMS > 0 && latencyMS >= float64(item.RequestTimeoutMS)*0.8 {
		return true
	}
	if len(previous) < 3 {
		return false
	}
	_, p95 := percentiles(previous)
	return latencyMS >= 500 && p95 > 0 && latencyMS > p95*2
}

func (m *Manager) publicEndpoint(item endpointRecord) Endpoint {
	item.EndpointMasked = maskEndpointFromHost(item.EndpointHost)
	if endpointURL, err := m.secure.decrypt(item.EncryptedURL); err == nil {
		item.EndpointMasked = maskEndpoint(endpointURL)
	}
	item.SecretConfigured = true
	item.TestEndpointMasked = ""
	item.TestEndpointConfigured = len(item.EncryptedTestURL) > 0
	if item.TestEndpointConfigured {
		if endpointURL, err := m.secure.decrypt(item.EncryptedTestURL); err == nil {
			item.TestEndpointMasked = maskEndpoint(endpointURL)
		}
	}
	item.EncryptedURL = nil
	item.EncryptedTestURL = nil
	item.Health = m.store.health(item.ID)
	runtime := m.runtime(item)
	runtime.mu.Lock()
	item.CurrentRPS = runtime.currentRPS
	runtime.mu.Unlock()
	return item.Endpoint
}

func (m *Manager) applyBlockLag(items []Endpoint) {
	highest := make(map[string]uint64)
	for _, item := range items {
		if item.Health.LatestBlock > highest[item.ChainKey] {
			highest[item.ChainKey] = item.Health.LatestBlock
		}
	}
	for index := range items {
		if highest[items[index].ChainKey] >= items[index].Health.LatestBlock {
			items[index].Health.BlockLag = highest[items[index].ChainKey] - items[index].Health.LatestBlock
			if items[index].Health.BlockLag > 10 && items[index].Health.Status == StatusHealthy {
				items[index].Health.Status = StatusUnavailable
			} else if items[index].Health.BlockLag > 2 && items[index].Health.Status == StatusHealthy {
				items[index].Health.Status = StatusDegraded
			}
		}
	}
}

func (m *Manager) healthLoop() {
	defer close(m.closed)
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.close:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			m.RefreshHealth(ctx)
			cancel()
		}
	}
}

func validateEndpointInput(input EndpointInput) (EndpointInput, chain.EVM, string, error) {
	input.Provider = strings.ToUpper(strings.TrimSpace(input.Provider))
	switch input.Provider {
	case "CHAINSTACK", "ANKR", "NODEREAL", "CUSTOM":
	default:
		return input, chain.EVM{}, "", errors.New("供应商必须是 Chainstack、Ankr、NodeReal 或 Custom")
	}
	network, err := chain.Resolve(input.ChainKey)
	if err != nil {
		return input, chain.EVM{}, "", err
	}
	input.ChainKey = network.Key
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" || len([]rune(input.DisplayName)) > 80 {
		return input, network, "", errors.New("显示名称不能为空且不能超过 80 个字符")
	}
	endpointURL, err := validateRPCURL(input.EndpointURL, "Endpoint")
	if err != nil {
		return input, network, "", err
	}
	input.EndpointURL = endpointURL
	input.TestEndpointURL = strings.TrimSpace(input.TestEndpointURL)
	if input.Priority <= 0 {
		input.Priority = 10
	}
	if input.MaxRPS <= 0 {
		input.MaxRPS = defaultRPS(input.Provider, network.Key)
	}
	if input.MaxRPS < 0.25 || input.MaxRPS > 100 {
		return input, network, "", errors.New("最大 RPS 必须在 0.25～100 之间")
	}
	if input.MaxConcurrency <= 0 {
		input.MaxConcurrency = int(math.Max(1, math.Min(4, input.MaxRPS)))
	}
	if input.MaxConcurrency < 1 || input.MaxConcurrency > 32 {
		return input, network, "", errors.New("最大并发必须在 1～32 之间")
	}
	if input.RequestTimeoutMS <= 0 {
		input.RequestTimeoutMS = 8000
	}
	if input.RequestTimeoutMS < 1000 || input.RequestTimeoutMS > 30000 {
		return input, network, "", errors.New("请求超时必须在 1000～30000 毫秒之间")
	}
	input.SupportedMethods, err = normalizeSupportedMethods(input.SupportedMethods)
	if err != nil {
		return input, network, "", err
	}
	return input, network, endpointURL, nil
}

func normalizeSupportedMethods(methods []string) ([]string, error) {
	if methods == nil {
		return nil, nil
	}
	if len(methods) > 256 {
		return nil, errors.New("supported_methods 不能超过 256 项")
	}
	result := make([]string, 0, len(methods))
	seen := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		method = strings.TrimSpace(method)
		if method == "" || len(method) > 128 {
			return nil, errors.New("supported_methods 包含空值或超长方法名")
		}
		if method != "*" {
			for _, char := range method {
				if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
					(char >= '0' && char <= '9') || char == '_' {
					continue
				}
				return nil, errors.New("supported_methods 包含非法方法名")
			}
		}
		key := strings.ToLower(method)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, method)
	}
	return result, nil
}

func capabilitiesConfigured(methods []string, archive, trace bool) bool {
	return methods != nil || archive || trace
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func validateOptionalEndpointURL(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	return validateRPCURL(value, "测试 Endpoint")
}

func validateRPCURL(value, label string) (string, error) {
	endpointURL := strings.TrimSpace(value)
	parsed, err := url.Parse(endpointURL)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("%s URL 格式错误", label)
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return "", fmt.Errorf("%s 必须使用 HTTPS；仅本机测试允许 HTTP", label)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("%s 不允许 URL Basic Auth，请使用供应商 HTTPS Endpoint", label)
	}
	return endpointURL, nil
}

func endpointURLForTest(primaryURL, testURL string) (string, string) {
	if testURL != "" {
		return testURL, "TEST"
	}
	return primaryURL, "PRIMARY"
}

func maskOptionalEndpoint(endpointURL string) string {
	if endpointURL == "" {
		return ""
	}
	return maskEndpoint(endpointURL)
}

func classifyError(err error, status int, body string) *callError {
	message := strings.ToLower(err.Error() + " " + body)
	result := &callError{class: "UPSTREAM", code: strconv.Itoa(status), message: "RPC 上游请求失败"}
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "timeout"):
		result.class, result.code, result.message, result.retryable, result.timeout = "TIMEOUT", "TIMEOUT", "RPC 请求超时", true, true
	case status == 401 || status == 403:
		result.class, result.message, result.auth = "AUTH", "RPC 认证失败，请检查 API Key", true
	case status == 429 || strings.Contains(message, "rate limit") || strings.Contains(message, "quota"):
		result.class, result.message, result.retryable, result.rateLimited = "RATE_LIMITED", "RPC 节点触发限流", true, true
	case status == 408 || status == 500 || status == 502 || status == 503 || status == 504:
		result.retryable = true
	case status == 0:
		result.class, result.code, result.message, result.retryable = "NETWORK", "NETWORK", "RPC 网络连接失败", true
	}
	return result
}

func classifyRPCError(code int, message string) *callError {
	lower := strings.ToLower(message)
	result := &callError{class: "RPC_ERROR", code: strconv.Itoa(code), message: redactMessage(message)}
	if code == -32005 || strings.Contains(lower, "rate") || strings.Contains(lower, "quota") {
		result.class, result.message, result.retryable, result.rateLimited = "RATE_LIMITED", "RPC 节点触发限流", true, true
	}
	if code == -32602 || code == -32601 || strings.Contains(lower, "revert") {
		result.retryable = false
	}
	return result
}

func healthFromTest(id string, result TestResult) Health {
	now := time.Now().UTC()
	health := Health{
		EndpointID: id, Status: result.Status, CircuitState: CircuitClosed,
		LatestBlock: result.LatestBlock, LatencyP50MS: float64(result.LatencyMS),
		LatencyP95MS: float64(result.LatencyMS), SuccessRate5M: 100, HealthScore: 100,
		CheckedAt: &now,
	}
	if result.Success {
		health.LastSuccessAt = &now
	} else {
		health.LastFailureAt, health.LastErrorCode = &now, result.ErrorClass
		health.LastErrorMessageRedacted, health.HealthScore = result.ErrorMessage, 0
	}
	return health
}

func failedTest(provider, chainKey, class, message string) TestResult {
	return TestResult{
		Success: false, Provider: provider, ChainKey: chainKey, Status: StatusUnavailable,
		ErrorClass: class, ErrorMessage: redactMessage(message), Suggestion: testSuggestion(class),
	}
}

func testSuggestion(class string) string {
	switch class {
	case "AUTH":
		return "检查 API Key，并确认该 Key 已启用当前链。"
	case "CHAIN_ID_MISMATCH":
		return "确认 Endpoint 与所选链一致。"
	case "TIMEOUT", "NETWORK":
		return "检查网络和节点可用性，或稍后重试。"
	default:
		return "检查 Endpoint 配置和供应商控制台状态。"
	}
}

func maskEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "••••••••"
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for index, segment := range segments {
		if segment == "" {
			continue
		}
		if len(segment) <= 4 {
			segments[index] = "••••"
		} else {
			segments[index] = "••••••••" + segment[len(segment)-4:]
		}
	}
	parsed.Path = "/" + strings.Join(segments, "/")
	query := parsed.Query()
	for key := range query {
		query.Set(key, "••••••••")
	}
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String()
}

func maskEndpointFromHost(host string) string {
	if host == "" {
		return "https://••••••••"
	}
	return "https://" + host + "/••••••••"
}

func endpointHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func redactMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 240 {
		message = message[:240]
	}
	for _, prefix := range []string{"http://", "https://"} {
		for {
			start := strings.Index(strings.ToLower(message), prefix)
			if start < 0 {
				break
			}
			end := len(message)
			if space := strings.IndexAny(message[start:], " \t\r\n\"'"); space >= 0 {
				end = start + space
			}
			message = message[:start] + "[REDACTED_ENDPOINT]" + message[end:]
		}
	}
	return message
}

func decodeHexUint(raw json.RawMessage) (uint64, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimPrefix(value, "0x"), 16, 64)
}

func defaultRPS(provider, chainKey string) float64 {
	switch provider {
	case "NODEREAL":
		if chainKey == "bsc" {
			return 4
		}
		return 2
	case "CHAINSTACK":
		if chainKey == "bsc" {
			return 3
		}
		return 4
	default:
		return 2
	}
}

func routeRank(status string) int {
	switch status {
	case StatusHealthy:
		return 0
	case StatusDegraded:
		return 1
	case StatusRateLimited:
		return 2
	case StatusUnavailable:
		return 3
	default:
		return 4
	}
}

func appendBounded[T any](values []T, value T, limit int) []T {
	values = append(values, value)
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	return values
}

func percentiles(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	return copyValues[(len(copyValues)-1)/2], copyValues[int(math.Ceil(float64(len(copyValues))*0.95))-1]
}

func successRate(values []bool) float64 {
	if len(values) == 0 {
		return 0
	}
	var success int
	for _, value := range values {
		if value {
			success++
		}
	}
	return float64(success) / float64(len(values)) * 100
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func healthScore(successRate, p95 float64) float64 {
	latencyPenalty := math.Min(30, p95/100)
	return math.Max(0, math.Min(100, successRate-latencyPenalty))
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func newID(prefix string) string {
	buffer := make([]byte, 10)
	_, _ = rand.Read(buffer)
	return prefix + "-" + hex.EncodeToString(buffer)
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateDataRoot(dataRoot string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(dataRoot))
	if err != nil {
		return "", fmt.Errorf("解析 RPC 数据目录: %w", err)
	}
	volume := strings.ToUpper(filepath.VolumeName(absolute))
	if volume == "C:" {
		return "", errors.New("RPC 控制数据禁止写入系统盘 C:，请使用 E: 数据目录")
	}
	return absolute, nil
}
