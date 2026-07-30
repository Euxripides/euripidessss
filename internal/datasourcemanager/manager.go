package datasourcemanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource/aws"
	"github.com/etl/backend/internal/datasource/sqd"
	"github.com/etl/backend/internal/rpcmanager"
)

type storedConfig struct {
	ConfigInput
	APIKeyEncrypted string `json:"api_key_encrypted,omitempty"`
}

type persistedState struct {
	Configs []storedConfig `json:"configs"`
	Events  []Event        `json:"events"`
}

type sourceRuntime struct {
	Status          string
	Latencies       []float64
	Outcomes        []bool
	Today           string
	Requests        int64
	Success         int64
	Failure         int64
	RateLimited     int64
	Timeout         int64
	Worker503       int64
	LastSuccessAt   *time.Time
	LastFailureAt   *time.Time
	LastRecoveryAt  *time.Time
	LastError       string
	CurrentTasks    int64
	CheckedAt       *time.Time
	AverageSpeedBPS float64
}

type Manager struct {
	mu       sync.RWMutex
	path     string
	state    persistedState
	runtimes map[string]*sourceRuntime
	rpc      *rpcmanager.Manager
	client   *http.Client
	close    chan struct{}
	closed   chan struct{}
}

func New(dataRoot string, rpc *rpcmanager.Manager) (*Manager, error) {
	root, err := filepath.Abs(strings.TrimSpace(dataRoot))
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.VolumeName(root), "C:") {
		return nil, errors.New("数据源控制数据禁止写入系统盘 C:")
	}
	manager := &Manager{
		path: filepath.Join(root, "config", "datasources.json"),
		rpc:  rpc, runtimes: make(map[string]*sourceRuntime),
		client: &http.Client{Transport: &http.Transport{
			Proxy:        http.ProxyFromEnvironment,
			DialContext:  (&net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns: 16, MaxIdleConnsPerHost: 4, IdleConnTimeout: 60 * time.Second,
		}},
		close: make(chan struct{}), closed: make(chan struct{}),
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	if err := manager.ensureDefaults(); err != nil {
		return nil, err
	}
	go manager.healthLoop()
	return manager, nil
}

func (m *Manager) Close() {
	select {
	case <-m.close:
	default:
		close(m.close)
	}
	<-m.closed
	m.client.CloseIdleConnections()
}

func (m *Manager) Snapshot() (Snapshot, error) {
	m.mu.RLock()
	configs := append([]storedConfig(nil), m.state.Configs...)
	events := append([]Event(nil), m.state.Events...)
	m.mu.RUnlock()
	sources := make([]Source, 0, len(configs)+4)
	for _, config := range configs {
		sources = append(sources, m.sourceFromConfig(config))
	}
	var rpcOverview rpcmanager.Overview
	if m.rpc != nil {
		response, err := m.rpc.HealthResponse()
		if err == nil {
			rpcOverview = response.Overview
			for _, endpoint := range response.Endpoints {
				sources = append(sources, rpcSource(endpoint))
			}
		}
	}
	sort.SliceStable(sources, func(i, j int) bool {
		order := map[string]int{TypeStream: 0, TypeDataset: 1, TypeRPC: 2}
		if order[sources[i].Type] != order[sources[j].Type] {
			return order[sources[i].Type] < order[sources[j].Type]
		}
		return sources[i].Name < sources[j].Name
	})
	var overview Overview
	overview.SourceCount = len(sources)
	for _, source := range sources {
		overview.TodayRequests += source.TodayRequests
		if source.Status == StatusHealthy {
			overview.HealthyCount++
		} else if source.Status != StatusDisabled && source.Status != StatusUnknown {
			overview.AbnormalCount++
		}
	}
	overview.TodayRequests += rpcOverview.TodayRequests
	overview.CacheHitRate = rpcOverview.CacheHitRate
	if len(events) > 20 {
		events = events[len(events)-20:]
	}
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	return Snapshot{Overview: overview, Sources: sources, Events: events}, nil
}

func (m *Manager) Save(ctx context.Context, input ConfigInput) (Source, error) {
	config, err := m.validateInput(input)
	if err != nil {
		return Source{}, err
	}
	m.mu.RLock()
	for _, item := range m.state.Configs {
		if item.ID == config.ID {
			config.APIKeyEncrypted = item.APIKeyEncrypted
			break
		}
	}
	m.mu.RUnlock()
	if config.Enabled {
		result := m.testConfig(ctx, config)
		if !result.Success {
			return Source{}, errors.New(result.Message)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var existing *storedConfig
	for index := range m.state.Configs {
		if m.state.Configs[index].ID == config.ID {
			existing = &m.state.Configs[index]
			break
		}
	}
	if config.APIKey != "" {
		sealed, sealErr := m.rpc.SealSecret(config.APIKey)
		if sealErr != nil {
			return Source{}, sealErr
		}
		config.APIKeyEncrypted = sealed
		config.APIKey = ""
	} else if existing != nil {
		config.APIKeyEncrypted = existing.APIKeyEncrypted
	}
	if existing == nil {
		m.state.Configs = append(m.state.Configs, config)
	} else {
		*existing = config
	}
	if err := m.persistLocked(); err != nil {
		return Source{}, err
	}
	return m.sourceFromConfigLocked(config), nil
}

func (m *Manager) Delete(id string) error {
	if strings.HasPrefix(id, "rpc:") {
		if m.rpc == nil {
			return errors.New("RPC 管理器不可用")
		}
		return m.rpc.Delete(strings.TrimPrefix(id, "rpc:"))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.state.Configs {
		if m.state.Configs[index].ID == id {
			m.state.Configs = append(m.state.Configs[:index], m.state.Configs[index+1:]...)
			delete(m.runtimes, id)
			return m.persistLocked()
		}
	}
	return errors.New("数据源不存在")
}

func (m *Manager) Test(ctx context.Context, id string) TestResult {
	if strings.HasPrefix(id, "rpc:") {
		if m.rpc == nil {
			return failedResult(id, "RPC 管理器不可用")
		}
		result, err := m.rpc.TestEndpoint(ctx, strings.TrimPrefix(id, "rpc:"))
		if err != nil {
			return failedResult(id, err.Error())
		}
		return TestResult{
			Success: result.Success, SourceID: id, Status: result.Status,
			LatencyMS: result.LatencyMS, LatestBlock: result.LatestBlock,
			CheckedAt: time.Now().UTC(), Message: coalesce(result.ErrorMessage, "RPC 连接与链校验通过"),
		}
	}
	m.mu.RLock()
	var config *storedConfig
	for index := range m.state.Configs {
		if m.state.Configs[index].ID == id {
			copyConfig := m.state.Configs[index]
			config = &copyConfig
			break
		}
	}
	m.mu.RUnlock()
	if config == nil {
		return failedResult(id, "数据源不存在")
	}
	result := m.testConfig(ctx, *config)
	m.recordResult(*config, result)
	return result
}

func (m *Manager) Config(id string) (ConfigInput, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, config := range m.state.Configs {
		if config.ID == id {
			result := config.ConfigInput
			result.APIKey = ""
			return result, nil
		}
	}
	return ConfigInput{}, errors.New("数据源不存在")
}

func (m *Manager) SQDConfig() ConfigInput {
	return m.configByType(TypeStream, sqd.DefaultPortal)
}

// UpdateTaskCount updates the current task count for a data source.
// Called by the download manager when tasks start or complete.
func (m *Manager) UpdateTaskCount(sourceID string, count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.runtimeLocked(sourceID)
	runtime.CurrentTasks = count
}

// Update503Count increments the 503 worker-unavailable counter.
func (m *Manager) Update503Count(sourceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.runtimeLocked(sourceID)
	runtime.Worker503++
}

// RecordRecovery marks a data source as having recovered from a failure.
func (m *Manager) RecordRecovery(sourceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.runtimeLocked(sourceID)
	if runtime.Status == StatusNoAvailableWorkers || runtime.Status == StatusRecovering {
		runtime.Status = StatusHealthy
		now := time.Now().UTC()
		runtime.LastRecoveryAt = &now
	}
}

func (m *Manager) AWSConfig() ConfigInput {
	return m.configByType(TypeDataset, aws.DefaultEndpoint)
}

func (m *Manager) RuntimeConfig(sourceType string) ConfigInput {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, config := range m.state.Configs {
		if config.Type != sourceType || !config.Enabled {
			continue
		}
		result := config.ConfigInput
		result.APIKey = ""
		if config.APIKeyEncrypted != "" && m.rpc != nil {
			result.APIKey, _ = m.rpc.OpenSecret(config.APIKeyEncrypted)
		}
		return result
	}
	return ConfigInput{}
}

func (m *Manager) configByType(sourceType, fallback string) ConfigInput {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, config := range m.state.Configs {
		if config.Type == sourceType && config.Enabled {
			result := config.ConfigInput
			result.APIKey = ""
			return result
		}
	}
	return ConfigInput{Type: sourceType, Endpoint: fallback, Enabled: true}
}

func (m *Manager) testConfig(ctx context.Context, config storedConfig) TestResult {
	timeout := time.Duration(config.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	var dataset string
	var err error
	switch config.Type {
	case TypeStream:
		network, _ := chain.Resolve("bsc")
		dataset = network.SQDDataset
		endpoint := strings.TrimRight(config.Endpoint, "/") + "/" + dataset + "/metadata"
		var payload struct {
			Dataset string `json:"dataset"`
		}
		err = m.getJSON(testCtx, endpoint, config, &payload)
		if err == nil && payload.Dataset != dataset {
			err = fmt.Errorf("SQD dataset不匹配：%s", payload.Dataset)
		}
	case TypeDataset:
		endpoint := strings.TrimRight(config.Endpoint, "/") + "/?list-type=2&max-keys=1&prefix=" + url.QueryEscape(config.Prefix)
		var response *http.Response
		response, err = m.doRequest(testCtx, endpoint, config)
		if err == nil {
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 256<<10))
			if response.StatusCode != http.StatusOK {
				err = fmt.Errorf("AWS Dataset HTTP %d", response.StatusCode)
			}
		}
	default:
		err = errors.New("不支持的数据源类型")
	}
	latency := time.Since(start).Milliseconds()
	now := time.Now().UTC()
	if err != nil {
		return TestResult{SourceID: config.ID, Status: classifyStatus(err), LatencyMS: latency, CheckedAt: now, Message: redactedError(err)}
	}
	return TestResult{Success: true, SourceID: config.ID, Status: StatusHealthy, LatencyMS: latency, Dataset: dataset, CheckedAt: now, Message: "连接测试通过"}
}

func (m *Manager) doRequest(ctx context.Context, endpoint string, config storedConfig) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.APIKey) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(config.APIKey))
	} else if config.APIKeyEncrypted != "" && m.rpc != nil {
		if secret, openErr := m.rpc.OpenSecret(config.APIKeyEncrypted); openErr == nil && secret != "" {
			request.Header.Set("Authorization", "Bearer "+secret)
		}
	}
	return m.client.Do(request)
}

func (m *Manager) getJSON(ctx context.Context, endpoint string, config storedConfig, target any) error {
	response, err := m.doRequest(ctx, endpoint, config)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("上游 HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target)
}

func (m *Manager) recordResult(config storedConfig, result TestResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prevStatus := ""
	runtime := m.runtimeLocked(config.ID)
	prevStatus = runtime.Status
	today := time.Now().UTC().Format("2006-01-02")
	if runtime.Today != today {
		*runtime = sourceRuntime{Today: today, Status: StatusUnknown}
	}
	runtime.Requests++
	runtime.Latencies = appendBounded(runtime.Latencies, float64(result.LatencyMS), 100)
	runtime.Outcomes = appendBounded(runtime.Outcomes, result.Success, 100)
	runtime.Status, runtime.CheckedAt = result.Status, &result.CheckedAt
	if result.Success {
		runtime.Success++
		runtime.LastSuccessAt, runtime.LastError = &result.CheckedAt, ""
		// Transition from NO_AVAILABLE_WORKERS → RECOVERING on first success after failure
		if prevStatus == StatusNoAvailableWorkers {
			runtime.Status = StatusRecovering
		}
		// Record recovery if transitioning from failed states
		if prevStatus == StatusRecovering || runtime.LastRecoveryAt == nil {
			now := result.CheckedAt
			runtime.LastRecoveryAt = &now
		}
	} else {
		runtime.Failure++
		runtime.LastFailureAt, runtime.LastError = &result.CheckedAt, result.Message
		if result.Status == StatusNoAvailableWorkers {
			runtime.Worker503++
		}
		if result.Status == StatusRateLimited {
			runtime.RateLimited++
		}
		if strings.Contains(strings.ToLower(result.Message), "超时") || result.Status == StatusUnavailable {
			runtime.Timeout++
		}
	}
	m.state.Events = append(m.state.Events, Event{
		SourceID: config.ID, SourceName: config.Name, Status: runtime.Status,
		Message: result.Message, OccurredAt: result.CheckedAt,
	})
	if len(m.state.Events) > 100 {
		m.state.Events = m.state.Events[len(m.state.Events)-100:]
	}
	_ = m.persistLocked()
}

func (m *Manager) healthLoop() {
	defer close(m.closed)
	m.refresh(context.Background())
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.close:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			m.refresh(ctx)
			cancel()
		}
	}
}

func (m *Manager) refresh(ctx context.Context) {
	m.mu.RLock()
	configs := append([]storedConfig(nil), m.state.Configs...)
	m.mu.RUnlock()
	var wait sync.WaitGroup
	for _, config := range configs {
		if !config.Enabled {
			continue
		}
		config := config
		wait.Add(1)
		go func() {
			defer wait.Done()
			m.recordResult(config, m.testConfig(ctx, config))
		}()
	}
	wait.Wait()
}

func (m *Manager) sourceFromConfig(config storedConfig) Source {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sourceFromConfigLocked(config)
}

func (m *Manager) sourceFromConfigLocked(config storedConfig) Source {
	runtime := m.runtimeLocked(config.ID)
	p50, p95 := percentiles(runtime.Latencies)
	successRate := outcomeRate(runtime.Outcomes)
	status := runtime.Status
	if !config.Enabled {
		status = StatusDisabled
	} else if status == "" {
		status = StatusUnknown
	}
	description, chains := "历史 finalized-stream 数据", []string{"bsc", "eth", "base", "arbitrum"}
	provider := "SQD"
	if config.Type == TypeDataset {
		description, chains, provider = "公共 BSC Transactions Parquet", []string{"bsc"}, "AWS"
	}
	return Source{
		ID: config.ID, Type: config.Type, Provider: provider, Name: config.Name,
		Description: description, EndpointMasked: maskURL(config.Endpoint),
		SecretConfigured: config.APIKeyEncrypted != "", ChainKeys: chains, Enabled: config.Enabled,
		Status: status, HealthScore: healthScore(successRate, p95), LatencyP50MS: p50,
		LatencyP95MS: p95, SuccessRate: successRate, TodayRequests: runtime.Requests,
		SuccessCount: runtime.Success, FailureCount: runtime.Failure,
		RateLimitedCount: runtime.RateLimited, TimeoutCount: runtime.Timeout,
		Worker503Count: runtime.Worker503, AverageSpeedBPS: runtime.AverageSpeedBPS,
		LastSuccessAt: runtime.LastSuccessAt, LastFailureAt: runtime.LastFailureAt,
		LastRecoveryAt: runtime.LastRecoveryAt, LastError: runtime.LastError,
		CurrentTasks: runtime.CurrentTasks, CheckedAt: runtime.CheckedAt,
		Config: PublicConfig{
			Bucket: config.Bucket, Region: config.Region, Prefix: config.Prefix,
			CacheDirectory: config.CacheDirectory, TimeoutMS: config.TimeoutMS,
			MaxConcurrency: config.MaxConcurrency, RetryCount: config.RetryCount,
		},
	}
}

func rpcSource(endpoint rpcmanager.Endpoint) Source {
	health := endpoint.Health
	return Source{
		ID: "rpc:" + endpoint.ID, Type: TypeRPC, Provider: endpoint.Provider,
		Name: endpoint.DisplayName, Description: "实时 Metadata、Balance、Receipt 补漏与地址类型",
		EndpointMasked: endpoint.EndpointMasked, SecretConfigured: endpoint.SecretConfigured,
		ChainKeys: []string{endpoint.ChainKey}, Enabled: endpoint.Enabled, Status: health.Status,
		HealthScore: health.HealthScore, LatencyP50MS: health.LatencyP50MS,
		LatencyP95MS: health.LatencyP95MS, SuccessRate: health.SuccessRate5M,
		LastSuccessAt: health.LastSuccessAt, LastFailureAt: health.LastFailureAt,
		LastError: health.LastErrorMessageRedacted, CheckedAt: health.CheckedAt,
		Config: PublicConfig{TimeoutMS: endpoint.RequestTimeoutMS, MaxConcurrency: endpoint.MaxConcurrency, RetryCount: 2},
	}
}

func (m *Manager) validateInput(input ConfigInput) (storedConfig, error) {
	input.Type = strings.ToUpper(strings.TrimSpace(input.Type))
	if input.Type != TypeStream && input.Type != TypeDataset {
		return storedConfig{}, errors.New("数据源类型必须是 STREAM 或 DATASET；RPC 请使用节点管理")
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 80 {
		return storedConfig{}, errors.New("数据源名称不能为空且不能超过80个字符")
	}
	parsed, err := url.Parse(strings.TrimSpace(input.Endpoint))
	if err != nil || parsed.Host == "" {
		return storedConfig{}, errors.New("Endpoint格式错误")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopback(parsed.Hostname())) {
		return storedConfig{}, errors.New("Endpoint必须使用HTTPS；仅本机测试允许HTTP")
	}
	input.Endpoint = parsed.String()
	if input.ID == "" {
		input.ID = "source-" + randomID()
	}
	if input.TimeoutMS == 0 {
		input.TimeoutMS = 8000
	}
	if input.TimeoutMS < 1000 || input.TimeoutMS > 60000 {
		return storedConfig{}, errors.New("Timeout必须在1000到60000毫秒之间")
	}
	if input.MaxConcurrency == 0 {
		input.MaxConcurrency = 4
	}
	if input.MaxConcurrency < 1 || input.MaxConcurrency > 64 {
		return storedConfig{}, errors.New("最大并发必须在1到64之间")
	}
	if input.RetryCount < 0 || input.RetryCount > 5 {
		return storedConfig{}, errors.New("Retry次数必须在0到5之间")
	}
	if input.Type == TypeDataset {
		if input.Bucket == "" {
			input.Bucket = "aws-public-blockchain"
		}
		if input.Region == "" {
			input.Region = "us-east-2"
		}
		if input.Prefix == "" {
			input.Prefix = "v1.1/bnb/transactions/"
		}
	}
	return storedConfig{ConfigInput: input}, nil
}

func (m *Manager) load() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0700); err != nil {
		return err
	}
	content, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(content, &m.state)
}

func (m *Manager) ensureDefaults() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	hasStream, hasDataset := false, false
	for _, config := range m.state.Configs {
		hasStream = hasStream || config.Type == TypeStream
		hasDataset = hasDataset || config.Type == TypeDataset
	}
	if !hasStream {
		m.state.Configs = append(m.state.Configs, storedConfig{ConfigInput: ConfigInput{
			ID: "sqd-default", Type: TypeStream, Name: "SQD Finalized Stream",
			Endpoint: sqd.DefaultPortal, TimeoutMS: 12000, MaxConcurrency: 4, RetryCount: 2, Enabled: true,
		}})
	}
	if !hasDataset {
		m.state.Configs = append(m.state.Configs, storedConfig{ConfigInput: ConfigInput{
			ID: "aws-public", Type: TypeDataset, Name: "AWS Public Dataset",
			Endpoint: aws.DefaultEndpoint, Bucket: "aws-public-blockchain", Region: "us-east-2",
			Prefix: "v1.1/bnb/transactions/", CacheDirectory: `E:\codex\bsc_analytics\cache`,
			TimeoutMS: 12000, MaxConcurrency: 6, RetryCount: 3, Enabled: true,
		}})
	}
	return m.persistLocked()
}

func (m *Manager) persistLocked() error {
	content, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	temp := m.path + ".tmp"
	if err := os.WriteFile(temp, content, 0600); err != nil {
		return err
	}
	if err := os.Rename(temp, m.path); err == nil {
		return nil
	}
	_ = os.Remove(m.path)
	return os.Rename(temp, m.path)
}

func (m *Manager) runtimeLocked(id string) *sourceRuntime {
	runtime := m.runtimes[id]
	if runtime == nil {
		runtime = &sourceRuntime{Status: StatusUnknown, Today: time.Now().UTC().Format("2006-01-02")}
		m.runtimes[id] = runtime
	}
	return runtime
}

func maskURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "****"
	}
	masked := parsed.Scheme + "://" + parsed.Host
	if parsed.Path != "" && parsed.Path != "/" {
		masked += "/****"
	}
	return masked
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func classifyStatus(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "timeout"):
		return StatusUnavailable
	case strings.Contains(message, "503") && (strings.Contains(message, "no available worker") ||
		strings.Contains(message, "no_available_worker") || strings.Contains(message, "unavailable")):
		return StatusNoAvailableWorkers
	case strings.Contains(message, "429") || strings.Contains(message, "rate"):
		return StatusRateLimited
	case strings.Contains(message, "eof") || strings.Contains(message, "connection reset") ||
		strings.Contains(message, "broken pipe") || strings.Contains(message, "network"):
		return StatusUnavailable
	default:
		return StatusUnavailable
	}
}

func redactedError(err error) string {
	message := strings.TrimSpace(err.Error())
	for _, prefix := range []string{"http://", "https://"} {
		for {
			start := strings.Index(strings.ToLower(message), prefix)
			if start < 0 {
				break
			}
			end := len(message)
			if offset := strings.IndexAny(message[start:], " \t\r\n\"'"); offset >= 0 {
				end = start + offset
			}
			message = message[:start] + "[REDACTED_ENDPOINT]" + message[end:]
		}
	}
	if len(message) > 240 {
		message = message[:240]
	}
	return message
}

func failedResult(id, message string) TestResult {
	return TestResult{SourceID: id, Status: StatusUnavailable, CheckedAt: time.Now().UTC(), Message: message}
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
	return copyValues[(len(copyValues)-1)/2], copyValues[int(float64(len(copyValues)-1)*0.95)]
}

func outcomeRate(values []bool) float64 {
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

func healthScore(successRate, p95 float64) float64 {
	if successRate == 0 {
		return 0
	}
	score := successRate - min(p95/200, 25)
	if score < 0 {
		return 0
	}
	return score
}

func coalesce(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func randomID() string {
	buffer := make([]byte, 8)
	_, _ = rand.Read(buffer)
	return hex.EncodeToString(buffer)
}
