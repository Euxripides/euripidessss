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
	engine       *duckdb.Engine
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
		engine:       engine,
	}
	if content, err := os.ReadFile(manager.settingsPath); err == nil {
		if err := json.Unmarshal(content, &manager.settings); err != nil {
			return nil, fmt.Errorf("读取 Parquet 设置: %w", err)
		}
	}
	settings, err := validateSettings(manager.settings)
	if err != nil {
		return nil, err
	}
	manager.settings = settings
	if err := ensureDataDirectories(settings); err != nil {
		return nil, err
	}
	manager.loadJobs()
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
	chainKey := strings.ToLower(strings.TrimSpace(request.ChainKey))
	if chainKey == "" {
		chainKey = "bsc"
	}
	addresses := normalizeAddresses(request.Addresses)
	if addresses.Valid == 0 {
		return Preview{}, errors.New("没有可用的 EVM 地址")
	}
	files, err := m.discoverer.discover(ctx, chainKey, request.StartDate, request.EndDate)
	if err != nil {
		return Preview{}, err
	}
	settings := m.Settings()
	free, err := diskFreeBytes(settings.DataRoot)
	if err != nil {
		return Preview{}, err
	}
	warnings := []string{
		"AWS BNB 当前公开目录已核验为 blocks 与 transactions；本任务仅处理 transactions，不宣称包含 Transfer logs、Trace 或交易回执。",
	}
	if len(files) == 0 {
		warnings = append(warnings, "所选日期尚未发现 transactions Parquet，可能是数据尚未发布。")
	}
	return Preview{
		ChainKey:     chainKey,
		ChainID:      56,
		NativeSymbol: "BNB",
		Addresses:    addresses,
		Files:        files,
		TotalBytes:   totalSourceBytes(files),
		FreeBytes:    free,
		Warnings:     warnings,
	}, nil
}

func (m *Manager) Start(ctx context.Context, request StartRequest) (*Job, error) {
	m.mu.RLock()
	for _, existing := range m.jobs {
		if existing.Status == StatusRunning || existing.Status == StatusQueued {
			m.mu.RUnlock()
			return nil, fmt.Errorf("已有 Parquet 任务 %s 正在运行；为控制磁盘和 DuckDB 内存，请等待完成或先取消", existing.ID)
		}
	}
	m.mu.RUnlock()
	preview, err := m.Preview(ctx, request)
	if err != nil {
		return nil, err
	}
	if len(preview.Files) == 0 {
		return nil, errors.New("所选日期没有可下载的 transactions Parquet 文件")
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
		ID:              id,
		ChainKey:        preview.ChainKey,
		ChainID:         preview.ChainID,
		NativeSymbol:    preview.NativeSymbol,
		Status:          StatusQueued,
		Stage:           "queued",
		Addresses:       preview.Addresses,
		StartDate:       request.StartDate,
		EndDate:         request.EndDate,
		TotalBytes:      preview.TotalBytes,
		Warnings:        preview.Warnings,
		KeepSourceFiles: keepSource,
		ExportCSV:       exportCSV,
		CreatedAt:       now,
		UpdatedAt:       now,
		Stages:          defaultStages(),
	}
	for _, source := range preview.Files {
		sourceCopy := source
		job.Files = append(job.Files, &FileTask{SourceObject: sourceCopy, Status: StatusQueued})
	}
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
	cancel := m.cancels[id]
	job := m.jobs[id]
	m.mu.RUnlock()
	if job == nil {
		return nil, errors.New("Parquet 任务不存在")
	}
	if cancel != nil {
		cancel()
	}
	return m.Get(id)
}

func (m *Manager) Retry(id string) (*Job, error) {
	job, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	if job.Status != StatusFailed && job.Status != StatusCanceled {
		return nil, errors.New("仅失败或已取消任务可以重试")
	}
	keepSource := job.KeepSourceFiles
	exportCSV := job.ExportCSV
	return m.Start(context.Background(), StartRequest{
		ChainKey:   job.ChainKey,
		Addresses:  strings.Join(job.Addresses.Addresses, "\n"),
		StartDate:  job.StartDate,
		EndDate:    job.EndDate,
		KeepSource: &keepSource,
		ExportCSV:  &exportCSV,
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
		if job.Status == StatusRunning || job.Status == StatusQueued {
			job.Status = StatusFailed
			job.Stage = "interrupted"
			job.Error = "服务重启中断，保留 .partial 与检查点，可点击重试继续"
		}
		m.jobs[job.ID] = &job
	}
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
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, content, 0644); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err == nil {
		return nil
	}
	_ = os.Remove(path)
	if err := os.Rename(temp, path); err != nil {
		return fmt.Errorf("提交状态文件 %s: %w", path, err)
	}
	return nil
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
		{Key: "output", Label: "Parquet 输出", Status: StatusQueued},
	}
}
