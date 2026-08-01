package parquetdownload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource/sqd"
	"github.com/etl/backend/internal/datasourcemanager"
	"github.com/etl/backend/internal/rpcmanager"
	datasetwriter "github.com/etl/backend/internal/writer"
)

type Manager struct {
	mu           sync.RWMutex
	rootDir      string
	settingsPath string
	settings     Settings
	jobs         map[string]*Job
	cancels      map[string]context.CancelFunc
	lastPersist  map[string]time.Time
	discoverer   *discoverer
	sqd          *sqd.Client
	engine       *duckdb.Engine
	rpcManager   *rpcmanager.Manager
	dataSources  *datasourcemanager.Manager
}

func (m *Manager) SetDataSourceManager(manager *datasourcemanager.Manager) {
	m.mu.Lock()
	m.dataSources = manager
	m.mu.Unlock()
}

func (m *Manager) syncDataSourceConfig() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dataSources == nil {
		return
	}
	sqdConfig := m.dataSources.RuntimeConfig(datasourcemanager.TypeStream)
	if sqdConfig.Endpoint != "" {
		// V2.1 RC2: 数据源变更时重建 Reliability Client（含 event log）
		// 先创建新 client，成功后再关闭旧 client 的 event log 句柄，
		// 避免句柄泄漏，且失败时保留可用旧 client
		logDir := filepath.Join(m.settings.DataRoot, "logs")
		reliable, err := sqd.NewReliable(
			&http.Client{Timeout: time.Duration(sqdConfig.TimeoutMS) * time.Millisecond},
			sqdConfig.Endpoint,
			sqdConfig.APIKey,
			logDir,
		)
		if err == nil {
			if m.sqd != nil {
				m.sqd.Close()
			}
			m.sqd = reliable
		}
	}
	awsConfig := m.dataSources.RuntimeConfig(datasourcemanager.TypeDataset)
	if awsConfig.Endpoint != "" {
		m.discoverer.endpoint = awsConfig.Endpoint
		m.discoverer.adapter.Client.Timeout = time.Duration(awsConfig.TimeoutMS) * time.Millisecond
	}
}

func (m *Manager) SetRPCManager(manager *rpcmanager.Manager) {
	m.mu.Lock()
	m.rpcManager = manager
	m.mu.Unlock()
}

func (m *Manager) rpcConfigured(chainKey, envName string) bool {
	m.mu.RLock()
	manager := m.rpcManager
	m.mu.RUnlock()
	return (manager != nil && manager.HasConfigured(chainKey)) || strings.TrimSpace(os.Getenv(envName)) != ""
}

func NewManager(rootDir string, engine *duckdb.Engine) (*Manager, error) {
	manager := &Manager{
		rootDir:      rootDir,
		settingsPath: filepath.Join(rootDir, "backend", "config", "crypto_parquet.json"),
		settings:     defaultSettings(rootDir),
		jobs:         map[string]*Job{},
		cancels:      map[string]context.CancelFunc{},
		lastPersist:  map[string]time.Time{},
		discoverer:   newDiscoverer(&http.Client{}),
		sqd:          sqd.New(&http.Client{Timeout: 90 * time.Second}),
		engine:       engine,
	}
	if content, err := os.ReadFile(manager.settingsPath); err == nil {
		if err := json.Unmarshal(content, &manager.settings); err != nil {
			return nil, fmt.Errorf("读取 Parquet 设置: %w", err)
		}
	}
	if manager.settings.ReceiptBatchSize == 0 {
		manager.settings.ReceiptBatchSize = 50
	}
	settings, err := validateSettings(manager.settings)
	if err != nil {
		return nil, err
	}
	manager.settings = settings
	if err := ensureDataDirectories(settings); err != nil {
		return nil, err
	}
	// V2.1 RC2: 默认走 Reliability Layer（retry/backoff/circuit breaker/adaptive workers/metrics/event log）
	logDir := filepath.Join(settings.DataRoot, "logs")
	reliable, err := sqd.NewReliable(nil, sqd.DefaultPortal, "", logDir)
	if err != nil {
		return nil, fmt.Errorf("初始化 SQD Reliability Client: %w", err)
	}
	manager.sqd = reliable
	manager.loadJobs()
	manager.reconcileFinalManifests()
	manager.resumePausedJobs()
	return manager, nil
}

func (m *Manager) Settings() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings
}

func (m *Manager) SaveSettings(settings Settings) (Settings, error) {
	checked, err := validateSettings(settings)
	if err != nil {
		return checked, err
	}
	if err := ensureDataDirectories(checked); err != nil {
		return checked, err
	}
	if err := writeJSONAtomic(m.settingsPath, checked); err != nil {
		return checked, err
	}
	m.mu.Lock()
	m.settings = checked
	m.mu.Unlock()
	return checked, nil
}

func (m *Manager) Preview(ctx context.Context, request StartRequest) (Preview, error) {
	m.syncDataSourceConfig()
	chainKey := strings.ToLower(strings.TrimSpace(request.ChainKey))
	network, err := chain.Resolve(chainKey)
	if err != nil {
		return Preview{}, err
	}
	chainKey = network.Key
	addresses := normalizeAddresses(request.Addresses)
	if addresses.Valid == 0 {
		return Preview{}, errors.New("没有可用的 EVM 地址")
	}
	selectedSources, err := normalizeSelectedSources(request.SelectedSource)
	if err != nil {
		return Preview{}, err
	}
	var files []SourceObject
	if hasSelectedSource(selectedSources, "transactions") && network.Key == "bsc" {
		files, err = m.discoverer.discover(ctx, chainKey, request.StartDate, request.EndDate)
		if err != nil {
			return Preview{}, err
		}
	}
	settings := m.Settings()
	free, err := diskFreeBytes(settings.DataRoot)
	if err != nil {
		return Preview{}, err
	}
	warnings := []string{}
	var sqdRange *SQDBlockRange
	sqdAvailable := false
	needsSQD := hasSelectedSource(selectedSources, "logs") ||
		hasSelectedSource(selectedSources, "traces") ||
		(hasSelectedSource(selectedSources, "transactions") && network.Key != "bsc")
	if needsSQD {
		if network.SQDDataset == "" {
			return Preview{}, fmt.Errorf("%s 尚未配置 SQD 数据集", network.Name)
		}
		if _, err := m.sqd.Metadata(ctx, network); err != nil {
			return Preview{}, fmt.Errorf("探测 SQD 数据集: %w", err)
		}
		if resolved, err := m.sqd.ResolveDateRange(ctx, network, request.StartDate, request.EndDate); err != nil {
			return Preview{}, fmt.Errorf("解析 SQD 日期区块范围: %w", err)
		} else {
			if request.UseFirstSeen && addresses.Valid > 0 {
				minBlock := m.resolveMinFirstSeen(ctx, network, addresses.Addresses)
				if minBlock > 0 && minBlock <= resolved.To {
					resolved.From = minBlock
				}
			}
			sqdRange = &SQDBlockRange{From: resolved.From, To: resolved.To}
			sqdAvailable = true
		}
	}
	if hasSelectedSource(selectedSources, "transactions") && network.Key == "bsc" {
		warnings = append(warnings, "原生交易来自 AWS 公共 Parquet；Transfer 事件与 Trace 由 SQD 独立采集，不混写为交易记录。")
	} else if hasSelectedSource(selectedSources, "transactions") {
		warnings = append(warnings, "原生交易由 SQD 按地址过滤并统一为多链 transactions 模型。")
	}
	if hasSelectedSource(selectedSources, "logs") {
		warnings = append(warnings, "Token/NFT 标准事件已解析；当前保留 amount_raw，未配置或未完成代币 metadata RPC 时 symbol、decimals、换算金额保持空值。")
	}
	rpcConfigured := m.rpcConfigured(network.Key, network.RPCEnv)
	if !rpcConfigured {
		warnings = append(warnings, fmt.Sprintf("未配置 %s；Receipt、准确合约创建暂不启用，to_address 为空只保留为候选。", network.RPCEnv))
	}
	if hasSelectedSource(selectedSources, "transactions") && network.Key == "bsc" && len(files) == 0 {
		warnings = append(warnings, "所选日期尚未发现 transactions Parquet，可能是数据尚未发布。")
	}
	return Preview{
		ChainKey:         chainKey,
		ChainID:          network.ID,
		NativeSymbol:     network.NativeSymbol,
		Addresses:        addresses,
		SelectedSources:  selectedSources,
		Files:            files,
		TotalBytes:       totalSourceBytes(files),
		FreeBytes:        free,
		Warnings:         warnings,
		SQDAvailable:     sqdAvailable,
		SQDDataset:       network.SQDDataset,
		SQDBlockRange:    sqdRange,
		ReceiptAvailable: rpcConfigured,
		ReceiptRPCEnv:    network.RPCEnv,
	}, nil
}

func (m *Manager) Start(ctx context.Context, request StartRequest) (*Job, error) {
	m.mu.RLock()
	for _, existing := range m.jobs {
		if existing.Status == StatusRunning || existing.Status == StatusQueued || existing.Status == StatusCanceling {
			m.mu.RUnlock()
			return nil, fmt.Errorf("已有 Parquet 任务 %s 正在运行；为控制磁盘和 DuckDB 内存，请等待完成或先取消", existing.ID)
		}
	}
	m.mu.RUnlock()
	preview, err := m.Preview(ctx, request)
	if err != nil {
		return nil, err
	}
	if len(preview.Files) == 0 && !preview.SQDAvailable {
		return nil, errors.New("所选日期没有可下载的 transactions Parquet 文件")
	}
	if request.IncludeReceipts && !preview.ReceiptAvailable {
		return nil, fmt.Errorf("Receipt 富化已勾选，但环境变量 %s 未配置", preview.ReceiptRPCEnv)
	}
	settings := m.Settings()
	keepSource := settings.KeepSourceFiles
	if request.KeepSource != nil {
		keepSource = *request.KeepSource
	}
	exportCSV := settings.ExportCSV
	if request.ExportCSV != nil {
		exportCSV = *request.ExportCSV
	}
	id := newJobID()
	now := time.Now()
	job := &Job{
		SchemaVersion:   datasetwriter.ManifestSchemaVersion,
		ID:              id,
		ChainKey:        preview.ChainKey,
		ChainID:         preview.ChainID,
		NativeSymbol:    preview.NativeSymbol,
		Status:          StatusQueued,
		Stage:           "queued",
		Addresses:       preview.Addresses,
		StartDate:       request.StartDate,
		EndDate:         request.EndDate,
		UseFirstSeen:    request.UseFirstSeen,
		TotalBytes:      preview.TotalBytes,
		Warnings:        preview.Warnings,
		KeepSourceFiles: keepSource,
		ExportCSV:       exportCSV,
		IncludeReceipts: request.IncludeReceipts,
		SelectedSources: append([]string(nil), preview.SelectedSources...),
		SQDDataset:      preview.SQDDataset,
		SQDBlockRange:   preview.SQDBlockRange,
		CreatedAt:       now,
		UpdatedAt:       now,
		Stages:          defaultStages(),
		Manifest: ManifestInfo{
			Status:        StatusQueued,
			SchemaVersion: datasetwriter.ManifestSchemaVersion,
		},
	}
	for _, source := range preview.Files {
		sourceCopy := source
		job.Files = append(job.Files, &FileTask{SourceObject: sourceCopy, Status: StatusQueued})
	}
	updateCoverage(job)
	addTaskEvent(job, "JOB_CREATED", "任务已创建", "queued", map[string]any{
		"selected_sources": append([]string(nil), job.SelectedSources...),
		"address_count":    job.Addresses.Valid,
	})
	m.mu.Lock()
	m.jobs[id] = job
	m.mu.Unlock()
	if err := m.persistJob(job, true); err != nil {
		return nil, err
	}
	runContext, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancels[id] = cancel
	m.mu.Unlock()
	go m.runJob(runContext, id, settings)
	return m.Get(id)
}

func (m *Manager) Get(id string) (*Job, error) {
	m.mu.RLock()
	job := m.jobs[id]
	if job == nil {
		m.mu.RUnlock()
		return nil, errors.New("Parquet 任务不存在")
	}
	cloned, err := cloneJob(job)
	m.mu.RUnlock()
	return cloned, err
}

func (m *Manager) List() []*Job {
	m.mu.RLock()
	items := make([]*Job, 0, len(m.jobs))
	for _, job := range m.jobs {
		if cloned, err := cloneJob(job); err == nil {
			items = append(items, cloned)
		}
	}
	m.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if len(items) > 50 {
		items = items[:50]
	}
	return items
}

func (m *Manager) Cancel(id string) (*Job, error) {
	m.mu.RLock()
	job := m.jobs[id]
	m.mu.RUnlock()
	if job == nil {
		return nil, errors.New("Parquet 任务不存在")
	}
	if job.Status == StatusDone || job.Status == StatusFailed || job.Status == StatusCanceled {
		return m.Get(id)
	}
	m.mutate(id, func(item *Job) {
		item.Status = StatusCanceling
		item.Stage = "canceling"
		item.CancellationRequested = true
		addTaskEvent(item, "CANCEL_REQUESTED", "用户请求取消任务", item.Stage, nil)
		updateCoverage(item)
	})
	m.mu.RLock()
	cancel := m.cancels[id]
	m.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	if current, getErr := m.Get(id); getErr == nil {
		_ = m.persistJob(current, true)
	}
	return m.Get(id)
}

func (m *Manager) Retry(id string) (*Job, error) {
	job, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	if job.Status != StatusFailed && job.Status != StatusCanceled && job.Status != StatusPaused {
		return nil, errors.New("仅失败、已取消或已暂停任务可以重试")
	}
	keepSource := job.KeepSourceFiles
	exportCSV := job.ExportCSV
	return m.Start(context.Background(), StartRequest{
		ChainKey:        job.ChainKey,
		Addresses:       strings.Join(job.Addresses.Addresses, "\n"),
		StartDate:       job.StartDate,
		EndDate:         job.EndDate,
		KeepSource:      &keepSource,
		ExportCSV:       &exportCSV,
		UseFirstSeen:    job.UseFirstSeen,
		IncludeReceipts: job.IncludeReceipts,
		SelectedSource:  append([]string(nil), job.SelectedSources...),
	})
}

func (m *Manager) Close() {
	m.mu.Lock()
	for _, cancel := range m.cancels {
		cancel()
	}
	m.cancels = map[string]context.CancelFunc{}
	m.mu.Unlock()
}

func (m *Manager) loadJobs() {
	files, _ := filepath.Glob(filepath.Join(m.settings.DataRoot, "jobs", "*.json"))
	for _, path := range files {
		var job Job
		content, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(content, &job) != nil || job.ID == "" {
			continue
		}
		if job.Status == StatusCanceling || job.CancellationRequested {
			job.Status = StatusCanceled
			job.Stage = "canceled"
			job.Error = "服务在取消过程中重启，已按取消请求收敛"
			finished := time.Now()
			job.FinishedAt = &finished
			settleStages(&job, StatusCanceled)
			addTaskEvent(&job, "CANCEL_RECOVERED", job.Error, "canceled", nil)
		} else if job.Status == StatusRunning || job.Status == StatusQueued || job.Status == StatusPausing {
			job.Status = StatusPaused
			job.Stage = "paused"
			job.Error = ""
			for index := range job.Stages {
				if job.Stages[index].Status == StatusRunning {
					job.Stages[index].Status = StatusQueued
					job.Stages[index].Detail = "服务重启后等待从 Chunk/.partial 检查点恢复"
				}
			}
			addTaskEvent(&job, "WORKER_PAUSED", "服务重启中断，任务将自动从检查点恢复", "paused", nil)
		}
		if job.SchemaVersion == "" {
			job.SchemaVersion = datasetwriter.ManifestSchemaVersion
		}
		if job.Manifest.SchemaVersion == "" {
			job.Manifest.SchemaVersion = datasetwriter.ManifestSchemaVersion
		}
		updateCoverage(&job)
		m.jobs[job.ID] = &job
	}
}

func (m *Manager) resumePausedJobs() {
	var selected *Job
	for _, job := range m.List() {
		if job.Status == StatusPaused {
			selected = job
			break
		}
	}
	if selected == nil {
		return
	}
	runContext, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancels[selected.ID] = cancel
	if current := m.jobs[selected.ID]; current != nil {
		current.Status = StatusQueued
		current.Stage = "queued"
		addTaskEvent(current, "WORKER_RESUMED", "服务启动后从下载检查点自动恢复", "download", nil)
		current.UpdatedAt = time.Now()
		updateCoverage(current)
	}
	m.mu.Unlock()
	if current, err := m.Get(selected.ID); err == nil {
		_ = m.persistJob(current, true)
	}
	go m.runJob(runContext, selected.ID, m.Settings())
}

func (m *Manager) persistJob(job *Job, force bool) error {
	m.mu.Lock()
	if !force && time.Since(m.lastPersist[job.ID]) < time.Second {
		m.mu.Unlock()
		return nil
	}
	m.lastPersist[job.ID] = time.Now()
	m.mu.Unlock()
	m.mu.RLock()
	current := m.jobs[job.ID]
	if current == nil {
		m.mu.RUnlock()
		return nil
	}
	cloned, err := cloneJob(current)
	m.mu.RUnlock()
	if err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(m.settings.DataRoot, "jobs", job.ID+".json"), cloned)
}

func (m *Manager) mutate(id string, mutate func(*Job)) {
	m.mu.Lock()
	job := m.jobs[id]
	if job != nil {
		mutate(job)
		job.UpdatedAt = time.Now()
		updateCoverage(job)
	}
	m.mu.Unlock()
	if job != nil {
		_ = m.persistJob(job, false)
	}
}

func cloneJob(job *Job) (*Job, error) {
	content, err := json.Marshal(job)
	if err != nil {
		return nil, err
	}
	var cloned Job
	if err := json.Unmarshal(content, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}

func writeJSONAtomic(path string, value any) error {
	return datasetwriter.WriteJSONAtomic(path, value)
}

func newJobID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

func defaultStages() []Stage {
	return []Stage{
		{Key: "addresses", Label: "地址校验", Status: StatusDone, Progress: 100},
		{Key: "discover", Label: "文件发现", Status: StatusDone, Progress: 100},
		{Key: "download", Label: "分片下载", Status: StatusQueued},
		{Key: "schema", Label: "Schema 探测", Status: StatusQueued},
		{Key: "match", Label: "批量匹配", Status: StatusQueued},
		{Key: "transactions", Label: "多链交易统一", Status: StatusQueued},
		{Key: "logs", Label: "Transfer 日志", Status: StatusQueued},
		{Key: "metadata", Label: "Token Metadata", Status: StatusQueued},
		{Key: "nft", Label: "Token / NFT 解析", Status: StatusQueued},
		{Key: "traces", Label: "Trace / 内部交易", Status: StatusQueued},
		{Key: "receipts", Label: "Receipt 富化", Status: StatusQueued},
		{Key: "normalize", Label: "准确合约创建", Status: StatusQueued},
		{Key: "activity", Label: "地址统一流水", Status: StatusQueued},
		{Key: "summary", Label: "地址画像", Status: StatusQueued},
		{Key: "balances", Label: "余额快照", Status: StatusQueued},
		{Key: "output", Label: "Parquet 输出", Status: StatusQueued},
	}
}

func normalizeSelectedSources(items []string) ([]string, error) {
	if len(items) == 0 {
		return []string{"transactions"}, nil
	}
	allowed := map[string]bool{"transactions": true, "logs": true, "traces": true}
	seen := map[string]bool{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.ToLower(strings.TrimSpace(item))
		if !allowed[item] {
			return nil, fmt.Errorf("不支持的数据源 %q", item)
		}
		if item != "" && !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("至少选择一个数据源")
	}
	return result, nil
}

func hasSelectedSource(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
