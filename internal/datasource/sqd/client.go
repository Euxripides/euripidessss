package sqd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/etl/backend/internal/chain"
)

const (
	DefaultPortal       = "https://portal.sqd.dev/datasets"
	TransferTopic       = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	TransferSingleTopic = "0xc3d58168c5ae7397731d063d5bbf3d657854427343f4c083240f7aacaa2d0f62"
	TransferBatchTopic  = "0x4a39dc06d4c0dbc64b70af90fd698a233a518aa5d07e595d983b8c0526c8f7fb"

	// DefaultMaxCooldown is the maximum cooldown duration for 503 errors.
	DefaultMaxCooldown = 10 * time.Minute
)

type Client struct {
	httpClient *http.Client
	portalRoot string
	apiKey     string

	cooldownMu     sync.Mutex
	cooldownUntil  time.Time
	consecutive503 int
	maxCooldown    time.Duration

	breaker *CircuitBreaker

	// V3 Reliability：真并发闸门（信号量按 AdaptiveWorkers.Current() 限流）
	semMu    sync.Mutex
	inflight int

	// V3 Reliability：熔断状态变化检测（事件日志接线）
	lastBreakerState int32 // atomic: CircuitState

	// Reliability Layer (V2.1 RC2)
	relConfig ReliabilityConfig
	configMu  sync.Mutex // guards relConfig + breaker (SetReliabilityConfig vs postWithRetry)
	metrics   *ProviderMetrics
	workers   *AdaptiveWorkers
	events    *SQDEventLog
}

type Metadata struct {
	Dataset    string `json:"dataset"`
	RealTime   bool   `json:"real_time"`
	StartBlock uint64 `json:"start_block"`
}

type BlockRange struct {
	From uint64 `json:"from_block"`
	To   uint64 `json:"to_block"`
}

type Block struct {
	Header       Header        `json:"header"`
	Transactions []Transaction `json:"transactions,omitempty"`
	Logs         []Log         `json:"logs,omitempty"`
	Traces       []Trace       `json:"traces,omitempty"`
}

type Header struct {
	Number    uint64 `json:"number"`
	Timestamp int64  `json:"timestamp"`
}

type Transaction struct {
	Hash             string `json:"hash"`
	TransactionIndex uint64 `json:"transactionIndex"`
	From             string `json:"from"`
	To               string `json:"to"`
	Value            string `json:"value"`
	Input            string `json:"input"`
	Sighash          string `json:"sighash"`
	Status           uint64 `json:"status"`
	GasUsed          string `json:"gasUsed"`
	GasPrice         string `json:"gasPrice"`
}

type Log struct {
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	LogIndex         uint64   `json:"logIndex"`
	TransactionIndex uint64   `json:"transactionIndex"`
	TransactionHash  string   `json:"transactionHash"`
}

type Trace struct {
	TransactionIndex uint64       `json:"transactionIndex"`
	TraceAddress     []int        `json:"traceAddress"`
	Type             string       `json:"type"`
	Error            *string      `json:"error"`
	RevertReason     *string      `json:"revertReason"`
	Action           TraceAction  `json:"action"`
	Result           *TraceResult `json:"result"`
}

type TraceAction struct {
	From    string  `json:"from"`
	To      string  `json:"to"`
	Value   *string `json:"value"`
	Gas     string  `json:"gas"`
	Input   string  `json:"input"`
	Sighash string  `json:"sighash"`
}

type TraceResult struct {
	GasUsed string `json:"gasUsed"`
	Output  string `json:"output"`
	Address string `json:"address"`
	Code    string `json:"code"`
}

type streamRequest struct {
	Type         string                     `json:"type"`
	FromBlock    uint64                     `json:"fromBlock"`
	ToBlock      uint64                     `json:"toBlock"`
	Fields       map[string]map[string]bool `json:"fields"`
	Transactions []map[string]any           `json:"transactions,omitempty"`
	Logs         []map[string]any           `json:"logs,omitempty"`
	Traces       []map[string]any           `json:"traces,omitempty"`
}

func New(client *http.Client) *Client {
	return NewConfigured(client, DefaultPortal, "")
}

func NewConfigured(client *http.Client, portalRoot, apiKey string) *Client {
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	ensureTransport(client)
	portalRoot = strings.TrimRight(strings.TrimSpace(portalRoot), "/")
	if portalRoot == "" {
		portalRoot = DefaultPortal
	}
	return &Client{
		httpClient:  client,
		portalRoot:  portalRoot,
		apiKey:      strings.TrimSpace(apiKey),
		maxCooldown: DefaultMaxCooldown,
		breaker:     NewCircuitBreaker(DefaultCircuitBreakerConfig()),
		relConfig:   DefaultReliabilityConfig(),
	}
}

// NewReliable creates a fully reliability-enabled SQD client.
// It wires together retry, backoff, circuit breaker, adaptive workers,
// metrics, and event logging.
func NewReliable(client *http.Client, portalRoot, apiKey string, logDir string) (*Client, error) {
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	ensureTransport(client)
	portalRoot = strings.TrimRight(strings.TrimSpace(portalRoot), "/")
	if portalRoot == "" {
		portalRoot = DefaultPortal
	}
	relConfig := DefaultReliabilityConfig()
	var events *SQDEventLog
	if logDir != "" {
		var err error
		events, err = NewSQDEventLog(logDir)
		if err != nil {
			return nil, fmt.Errorf("create SQD event log: %w", err)
		}
	}
	metrics := NewProviderMetrics()
	workers := NewAdaptiveWorkers(relConfig.Workers)
	c := &Client{
		httpClient:  client,
		portalRoot:  portalRoot,
		apiKey:      strings.TrimSpace(apiKey),
		maxCooldown: DefaultMaxCooldown,
		breaker: NewCircuitBreaker(CircuitBreakerConfig{
			MaxFailures:  relConfig.Circuit.Threshold,
			OpenDuration: relConfig.Circuit.Cooldown,
			MinSuccesses: 1,
		}),
		relConfig: relConfig,
		metrics:   metrics,
		workers:   workers,
		events:    events,
	}
	// Wire adaptive workers to event log
	if events != nil {
		workers.OnScale(func(from, to int, reason string) {
			events.LogWorkerScale(from, to, reason)
		})
	}
	c.lastBreakerState = int32(c.breaker.State())
	return c, nil
}

// acquireWorker 按 AdaptiveWorkers.Current() 实现真并发限流（V3 设计 §4）：
// 在途请求数达到当前 worker 上限时等待（信号量闸门），workers 动态升降立即生效。
func (c *Client) acquireWorker(ctx context.Context) error {
	if c.workers == nil {
		return nil
	}
	for {
		limit := c.workers.Current()
		if limit <= 0 {
			return fmt.Errorf("sqd: no available workers (adaptive pool depleted)")
		}
		c.semMu.Lock()
		if c.inflight < limit {
			c.inflight++
			c.semMu.Unlock()
			return nil
		}
		c.semMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (c *Client) releaseWorker() {
	c.semMu.Lock()
	if c.inflight > 0 {
		c.inflight--
	}
	c.semMu.Unlock()
}

// checkBreakerEvents 检测熔断器状态变化并写入事件日志（V3 设计 §5：熔断可观测）。
// 仅记录 NORMAL/DEGRADED/OPEN/HALF_OPEN 之间的迁移，避免重复日志。
func (c *Client) checkBreakerEvents() {
	if c.events == nil || c.breaker == nil {
		return
	}
	state := c.breaker.State()
	prev := CircuitState(atomic.LoadInt32(&c.lastBreakerState))
	if state == prev {
		return
	}
	// CAS 防并发重复记录同一迁移
	if !atomic.CompareAndSwapInt32(&c.lastBreakerState, int32(prev), int32(state)) {
		return
	}
	c.configMu.Lock()
	cooldown := c.relConfig.Circuit.Cooldown
	c.configMu.Unlock()
	switch state {
	case CircuitOpen:
		c.events.LogCircuitOpen(c.breaker.Stats().Failures, cooldown)
	case CircuitHalfOpen:
		c.events.LogCircuitHalfOpen()
	case CircuitNormal, CircuitDegraded:
		if prev == CircuitOpen || prev == CircuitHalfOpen {
			c.events.LogCircuitRecovery()
		}
	}
}

// ensureTransport ensures the HTTP client has a connection-pooling transport.
func ensureTransport(client *http.Client) {
	if client.Transport == nil {
		client.Transport = &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
			DisableCompression:  false,
		}
	}
}

// ErrSQDCooldown is returned when SQD is in 503 cooldown.
var ErrSQDCooldown = errors.New("sqd: cooling down after 503 No available workers")

// IsInCooldown checks whether the client is in 503 cooldown.
func (c *Client) IsInCooldown() bool {
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()
	return time.Now().Before(c.cooldownUntil)
}

// CooldownUntil returns when the cooldown period ends.
func (c *Client) CooldownUntil() time.Time {
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()
	return c.cooldownUntil
}

// Consecutive503 returns consecutive 503 count.
func (c *Client) Consecutive503() int {
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()
	return c.consecutive503
}

func (c *Client) cooldownDuration() time.Duration {
	switch c.consecutive503 {
	case 0:
		return 30 * time.Second
	case 1:
		return 60 * time.Second
	case 2:
		return 120 * time.Second
	default:
		if c.maxCooldown > 0 {
			return c.maxCooldown
		}
		return DefaultMaxCooldown
	}
}

func (c *Client) setCooldown() time.Duration {
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()
	c.consecutive503++
	d := c.cooldownDuration()
	c.cooldownUntil = time.Now().Add(d)
	return d
}

func (c *Client) resetCooldown() {
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()
	c.consecutive503 = 0
	c.cooldownUntil = time.Time{}
}

func (c *Client) Metadata(ctx context.Context, network chain.EVM) (Metadata, error) {
	var result Metadata
	if err := c.getJSON(ctx, c.datasetURL(network)+"/metadata", &result); err != nil {
		return result, err
	}
	if result.Dataset != network.SQDDataset {
		return result, fmt.Errorf("SQD dataset 不匹配：期望 %s，实际 %s", network.SQDDataset, result.Dataset)
	}
	return result, nil
}

func (c *Client) ResolveDateRange(ctx context.Context, network chain.EVM, startDate, endDate string) (BlockRange, error) {
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return BlockRange{}, fmt.Errorf("开始日期格式错误: %w", err)
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return BlockRange{}, fmt.Errorf("结束日期格式错误: %w", err)
	}
	if end.Before(start) {
		return BlockRange{}, errors.New("结束日期不能早于开始日期")
	}
	from, err := c.timestampBlock(ctx, network, start.Unix())
	if err != nil {
		return BlockRange{}, err
	}
	nextDay, err := c.timestampBlock(ctx, network, end.Add(24*time.Hour).Unix())
	if err != nil {
		return BlockRange{}, err
	}
	if nextDay == 0 || nextDay <= from {
		return BlockRange{}, errors.New("SQD 返回的日期区块范围无效")
	}
	return BlockRange{From: from, To: nextDay - 1}, nil
}

func (c *Client) StreamLogs(
	ctx context.Context,
	network chain.EVM,
	blockRange BlockRange,
	addresses []string,
	handle func(Block) error,
) error {
	padded := padAddresses(addresses)
	filters := make([]map[string]any, 0, 6)
	for _, item := range []struct {
		topic string
		from  string
		to    string
	}{
		{TransferTopic, "topic1", "topic2"},
		{TransferSingleTopic, "topic2", "topic3"},
		{TransferBatchTopic, "topic2", "topic3"},
	} {
		filters = append(filters,
			map[string]any{"topic0": []string{item.topic}, item.from: padded},
			map[string]any{"topic0": []string{item.topic}, item.to: padded},
		)
	}
	request := streamRequest{
		Type:      "evm",
		FromBlock: blockRange.From,
		ToBlock:   blockRange.To,
		Fields: map[string]map[string]bool{
			"block": {"number": true, "timestamp": true},
			"log": {
				"address": true, "topics": true, "data": true, "logIndex": true,
				"transactionIndex": true, "transactionHash": true,
			},
		},
		Logs: filters,
	}
	return c.stream(ctx, network, request, validateLogBlock, handle)
}

func (c *Client) StreamTraces(
	ctx context.Context,
	network chain.EVM,
	blockRange BlockRange,
	addresses []string,
	handle func(Block) error,
) error {
	request := streamRequest{
		Type:      "evm",
		FromBlock: blockRange.From,
		ToBlock:   blockRange.To,
		Fields: map[string]map[string]bool{
			"block":       {"number": true, "timestamp": true},
			"transaction": {"hash": true, "transactionIndex": true},
			"trace": {
				"type": true, "transactionIndex": true, "traceAddress": true, "error": true, "revertReason": true,
				"callFrom": true, "callTo": true, "callValue": true, "callGas": true,
				"callSighash": true, "callInput": true, "callResultGasUsed": true,
				"callResultOutput": true, "createFrom": true, "createValue": true,
				"createResultAddress": true, "createResultCode": true,
			},
		},
		Traces: []map[string]any{
			{"callFrom": addresses, "transaction": true},
			{"callTo": addresses, "transaction": true},
		},
	}
	return c.stream(ctx, network, request, validateTraceBlock, handle)
}

func (c *Client) stream(
	ctx context.Context,
	network chain.EVM,
	request streamRequest,
	validate func(Block) error,
	handle func(Block) error,
) error {
	from := request.FromBlock
	probed := false
	for from <= request.ToBlock {
		request.FromBlock = from
		body, err := json.Marshal(request)
		if err != nil {
			return err
		}
		response, err := c.postWithRetry(ctx, c.datasetURL(network)+"/finalized-stream", body)
		if err != nil {
			return err
		}
		if response.StatusCode == http.StatusNoContent {
			response.Body.Close()
			return nil
		}
		var last uint64
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			var block Block
			if err := json.Unmarshal(scanner.Bytes(), &block); err != nil {
				response.Body.Close()
				return fmt.Errorf("解析 SQD NDJSON: %w", err)
			}
			if !probed {
				if err := validate(block); err != nil {
					// 容错：portal 元消息/心跳（header 为空）跳过，等待第一条有效消息；
					// 若整个流无有效消息，最后 "流未返回可推进的区块" 会如实报错
					continue
				}
				probed = true
			}
			if err := handle(block); err != nil {
				response.Body.Close()
				return err
			}
			if block.Header.Number > 0 {
				last = block.Header.Number
			}
		}
		scanErr := scanner.Err()
		response.Body.Close()
		if scanErr != nil {
			return fmt.Errorf("读取 SQD NDJSON: %w", scanErr)
		}
		if last < from {
			return errors.New("SQD 流未返回可推进的区块")
		}
		if last >= request.ToBlock {
			return nil
		}
		from = last + 1
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(550 * time.Millisecond):
		}
	}
	return nil
}

func (c *Client) postWithRetry(ctx context.Context, endpoint string, body []byte) (*http.Response, error) {
	// Check circuit breaker first
	if err := c.breaker.Allow(); err != nil {
		if c.metrics != nil {
			c.metrics.RecordRequest()
			c.metrics.RecordFailure(ErrorCircuitOpen)
		}
		return nil, err
	}

	// Check cooldown
	if c.IsInCooldown() {
		c.breaker.RecordFailure()
		if c.metrics != nil {
			c.metrics.RecordRequest()
			c.metrics.RecordFailure(ErrorCooldown)
		}
		return nil, fmt.Errorf("%w: available at %s", ErrSQDCooldown, c.CooldownUntil().Format(time.RFC3339))
	}

	// Check adaptive worker availability（V3：信号量闸门真限流）
	if err := c.acquireWorker(ctx); err != nil {
		return nil, err
	}
	defer c.releaseWorker()

	if c.metrics != nil {
		c.metrics.RecordRequest()
	}

	// Get backoff intervals from config (lock-protected read)
	c.configMu.Lock()
	intervals := append([]time.Duration(nil), c.relConfig.Backoff.Interval...)
	maxAttempts := c.relConfig.Retry.MaxAttempts
	c.configMu.Unlock()
	if len(intervals) == 0 {
		intervals = DefaultReliabilityConfig().Backoff.Interval
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	// Total attempts = 1 (initial) + retries
	totalAttempts := maxAttempts
	if totalAttempts > len(intervals) {
		totalAttempts = len(intervals)
	}

	var last error
	for attempt := 0; attempt <= totalAttempts; attempt++ {
		req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if errReq != nil {
			c.breaker.RecordFailure()
			if c.metrics != nil {
				c.metrics.RecordFailure(classifyHTTPError(errReq))
			}
			return nil, errReq
		}
		req.Header.Set("Content-Type", "application/json")
		c.authorize(req)

		start := time.Now()
		response, doErr := c.httpClient.Do(req)
		latency := time.Since(start)

		if doErr == nil && (response.StatusCode == http.StatusOK || response.StatusCode == http.StatusNoContent) {
			c.resetCooldown()
			c.breaker.RecordSuccess()
			c.checkBreakerEvents()
			if c.metrics != nil {
				c.metrics.RecordSuccess(latency)
			}
			if c.workers != nil {
				c.workers.RecordSuccess()
			}
			return response, nil
		}

		// Build error from response or transport error
		var reqErr error
		if doErr == nil {
			message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			response.Body.Close()
			reqErr = fmt.Errorf("SQD HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))

			// 503 No available workers: enter cooldown immediately
			if response.StatusCode == http.StatusServiceUnavailable &&
				(strings.Contains(strings.ToLower(string(message)), "no available worker") ||
					strings.Contains(strings.ToLower(string(message)), "no_available_worker")) {
				d := c.setCooldown()
				c.breaker.RecordFailure()
				c.checkBreakerEvents()
				if c.metrics != nil {
					c.metrics.RecordFailure(Error503)
				}
				if c.workers != nil {
					c.workers.Record503()
				}
				if c.events != nil {
					c.events.Log503(c.workers.Current(), strings.TrimSpace(string(message)))
				}
				return nil, fmt.Errorf("%w: %s (cooldown %v)", ErrSQDCooldown, strings.TrimSpace(string(message)), d)
			}

			// 429 Rate Limited
			if response.StatusCode == http.StatusTooManyRequests {
				if c.metrics != nil {
					c.metrics.RecordFailure(Error429)
				}
				if c.events != nil {
					c.events.Log429(strings.TrimSpace(string(message)))
				}
			}

			// Client errors (4xx except 429) are not retryable
			if response.StatusCode != http.StatusTooManyRequests && response.StatusCode < 500 {
				c.breaker.RecordFailure()
				c.checkBreakerEvents()
				if c.metrics != nil {
					c.metrics.RecordFailure(ErrorOther)
				}
				return nil, reqErr
			}
		} else {
			reqErr = doErr
			errorKind := classifyHTTPError(doErr)
			if c.metrics != nil {
				c.metrics.RecordFailure(errorKind)
			}
			if errorKind == ErrorDNS && c.events != nil {
				c.events.LogDNSFailure(doErr)
			}
			if errorKind == ErrorTimeout && c.events != nil {
				c.events.LogTimeout(latency)
			}
		}

		last = reqErr

		// Don't retry if this was the last attempt
		if attempt >= totalAttempts {
			break
		}

		// Record retry
		if c.metrics != nil {
			c.metrics.RecordRetry()
		}
		c.checkBreakerEvents()

		// Calculate backoff
		backoff := intervals[attempt]
		if c.events != nil {
			c.events.LogRetry(attempt+1, backoff, reqErr)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}
	c.breaker.RecordFailure()
	c.checkBreakerEvents()
	return nil, last
}

// Breaker returns the circuit breaker for health monitoring.
func (c *Client) Breaker() *CircuitBreaker {
	return c.breaker
}

// Metrics returns the provider metrics tracker. May be nil if not initialized.
func (c *Client) Metrics() *ProviderMetrics {
	return c.metrics
}

// Workers returns the adaptive worker pool. May be nil if not initialized.
func (c *Client) Workers() *AdaptiveWorkers {
	return c.workers
}

// EventLog returns the SQD event logger. May be nil if not initialized.
func (c *Client) EventLog() *SQDEventLog {
	return c.events
}

// ReliabilityConfig returns the current reliability configuration.
func (c *Client) ReliabilityConfig() ReliabilityConfig {
	c.configMu.Lock()
	defer c.configMu.Unlock()
	return c.relConfig
}

// Portal returns the configured SQD portal root URL.
func (c *Client) Portal() string {
	return c.portalRoot
}

// SetEventLog wires an external event log into the client (useful when
// the client is created before the log directory is known).
func (c *Client) SetEventLog(events *SQDEventLog) {
	c.events = events
	if events != nil && c.workers != nil {
		c.workers.OnScale(func(from, to int, reason string) {
			events.LogWorkerScale(from, to, reason)
		})
	}
}

// SetReliabilityConfig replaces the reliability config at runtime.
// Useful for tests and dynamic tuning. Note: rebuilding the config also
// rebuilds the circuit breaker (state resets to NORMAL).
func (c *Client) SetReliabilityConfig(cfg ReliabilityConfig) {
	cfg.Validate()
	c.configMu.Lock()
	defer c.configMu.Unlock()
	c.relConfig = cfg
	c.breaker = NewCircuitBreaker(CircuitBreakerConfig{
		MaxFailures:  cfg.Circuit.Threshold,
		OpenDuration: cfg.Circuit.Cooldown,
		MinSuccesses: 1,
	})
}

// Close releases resources held by the client (e.g. the SQD event log).
// Safe to call multiple times.
func (c *Client) Close() {
	if c.events != nil {
		_ = c.events.Close()
		c.events = nil
	}
}

// classifyHTTPError maps a Go HTTP error to an SQDErrorKind.
func classifyHTTPError(err error) SQDErrorKind {
	if err == nil {
		return ErrorNone
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		return ErrorTimeout
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "dns"):
		return ErrorDNS
	case strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "eof"):
		return ErrorNetwork
	default:
		return ErrorOther
	}
}

func (c *Client) timestampBlock(ctx context.Context, network chain.EVM, timestamp int64) (uint64, error) {
	var response struct {
		BlockNumber uint64 `json:"block_number"`
	}
	endpoint := c.datasetURL(network) + "/timestamps/" + strconv.FormatInt(timestamp, 10) + "/block"
	if err := c.getJSON(ctx, endpoint, &response); err != nil {
		return 0, err
	}
	return response.BlockNumber, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, target any) error {
	// Check circuit breaker first (same protection as postWithRetry)
	if err := c.breaker.Allow(); err != nil {
		if c.metrics != nil {
			c.metrics.RecordRequest()
			c.metrics.RecordFailure(ErrorCircuitOpen)
		}
		return err
	}
	if c.metrics != nil {
		c.metrics.RecordRequest()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	c.authorize(request)
	start := time.Now()
	response, err := c.httpClient.Do(request)
	latency := time.Since(start)
	if err != nil {
		if c.metrics != nil {
			c.metrics.RecordFailure(classifyHTTPError(err))
		}
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if c.metrics != nil {
			c.metrics.RecordFailure(ErrorOther)
		}
		return fmt.Errorf("SQD HTTP %d", response.StatusCode)
	}
	if c.metrics != nil {
		c.metrics.RecordSuccess(latency)
	}
	c.breaker.RecordSuccess()
	return json.NewDecoder(response.Body).Decode(target)
}

func (c *Client) authorize(request *http.Request) {
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

func (c *Client) datasetURL(network chain.EVM) string {
	return strings.TrimRight(c.portalRoot, "/") + "/" + network.SQDDataset
}

func padAddresses(addresses []string) []string {
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		address = strings.TrimPrefix(strings.ToLower(address), "0x")
		result = append(result, "0x"+strings.Repeat("0", 24)+address)
	}
	return result
}

func validateLogBlock(block Block) error {
	if block.Header.Number == 0 || block.Header.Timestamp == 0 {
		return errors.New("缺少 header.number/header.timestamp")
	}
	for _, item := range block.Logs {
		if item.Address == "" || item.TransactionHash == "" || len(item.Topics) == 0 {
			return errors.New("Log 缺少 address/transactionHash/topics")
		}
	}
	return nil
}

func validateTraceBlock(block Block) error {
	if block.Header.Number == 0 || block.Header.Timestamp == 0 {
		return errors.New("缺少 header.number/header.timestamp")
	}
	for _, item := range block.Traces {
		if item.Type == "" || (item.Action.From == "" && item.Action.To == "") {
			return errors.New("Trace 缺少 type/action")
		}
	}
	return nil
}
