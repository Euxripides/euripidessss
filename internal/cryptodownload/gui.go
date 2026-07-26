package cryptodownload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type GUIManager struct {
	mu        sync.Mutex
	jobs      map[string]*GUIJob
	store     *GUIJobStore
	history   *GUIDownloadHistoryStore
	configDir string
	scheduler *GUIBatchScheduler
}

type GUIJob struct {
	mu                 sync.Mutex
	ID                 string                    `json:"id"`
	Status             string                    `json:"status"`
	Message            string                    `json:"message"`
	Progress           int                       `json:"progress"`
	Done               int                       `json:"done"`
	Total              int                       `json:"total"`
	Running            bool                      `json:"running"`
	NeedsCredentials   bool                      `json:"needsCredentials"`
	StartedAt          string                    `json:"startedAt"`
	FinishedAt         string                    `json:"finishedAt"`
	Logs               []string                  `json:"logs"`
	Results            []string                  `json:"results"`
	Errors             []string                  `json:"errors"`
	Addresses          []GUIAddressProgress      `json:"addresses"`
	TaskDir            string                    `json:"taskDir,omitempty"`
	CheckpointSummary  []GUIJobCheckpointSummary `json:"checkpointSummaries,omitempty"`
	Incremental        bool                      `json:"incremental"`
	QueuePosition      int                       `json:"queuePosition"`
	QueueActive        int                       `json:"queueActive"`
	QueueWaiting       int                       `json:"queueWaiting"`
	CooldownUntil      string                    `json:"cooldownUntil,omitempty"`
	request            GUIStartRequest
	entries            []GUIAddressChain
	cancel             context.CancelFunc
	addressCancels     map[int]context.CancelFunc
	cancelledAddresses map[int]bool
	store              *GUIJobStore
	history            *GUIDownloadHistoryStore
	historyID          string
	scheduler          *GUIBatchScheduler
}

type GUIStartRequest struct {
	Source           string            `json:"source"`
	Addresses        string            `json:"addresses"`
	AddressChains    []GUIAddressChain `json:"addressChains"`
	Chains           string            `json:"chains"`
	RPCURL           string            `json:"rpcUrl"`
	RPCConfig        string            `json:"rpcConfig"`
	NativeSymbol     string            `json:"nativeSymbol"`
	CSVEmail         string            `json:"csvEmail"`
	CSVIMAPHost      string            `json:"csvImapHost"`
	CSVIMAPPort      int               `json:"csvImapPort"`
	CSVIMAPUser      string            `json:"csvImapUser"`
	CSVIMAPPassword  string            `json:"csvImapPassword"`
	CSVStartTime     int64             `json:"csvStartTime"`
	CSVEndTime       int64             `json:"csvEndTime"`
	CSVRequestHAR    string            `json:"csvRequestHar"`
	StartBlock       int64             `json:"startBlock"`
	EndBlock         int64             `json:"endBlock"`
	CutoffBlock      int64             `json:"cutoffBlock"`
	TraceMode        string            `json:"traceMode"`
	BlockBatch       uint64            `json:"blockBatch"`
	LogBatch         uint64            `json:"logBatch"`
	Workers          int               `json:"workers"`
	RPS              float64           `json:"rps"`
	TimeoutSeconds   int               `json:"timeoutSeconds"`
	Retries          int               `json:"retries"`
	PageSize         int               `json:"pageSize"`
	RawDir           string            `json:"rawDir"`
	OutputDir        string            `json:"outputDir"`
	OutputPrefix     string            `json:"outputPrefix"`
	AMLKey           string            `json:"amlKey"`
	AMLLabels        bool              `json:"amlLabels"`
	AMLRPS           float64           `json:"amlRps"`
	FilterExchange   bool              `json:"filterExchange"`
	Details          bool              `json:"details"`
	ScanNative       bool              `json:"scanNative"`
	Incremental      bool              `json:"incremental"`
	RiskCooldownSecs int               `json:"riskCooldownSecs"`
}

type GUIAddressChain struct {
	Address string `json:"address"`
	Chain   string `json:"chain"`
}

type GUIAddressProgress struct {
	Index           int                      `json:"index"`
	Address         string                   `json:"address"`
	Chain           string                   `json:"chain"`
	Status          string                   `json:"status"`
	Message         string                   `json:"message"`
	Progress        int                      `json:"progress"`
	Downloaded      int                      `json:"downloaded"`
	Total           int64                    `json:"total"`
	StartedAt       string                   `json:"startedAt"`
	UpdatedAt       string                   `json:"updatedAt"`
	FinishedAt      string                   `json:"finishedAt"`
	Result          string                   `json:"result"`
	Errors          []string                 `json:"errors"`
	Parts           []GUIAddressDownloadPart `json:"parts"`
	CancelRequested bool                     `json:"cancelRequested"`
}

type GUIAddressDownloadPart struct {
	Key              string `json:"key"`
	Chain            string `json:"chain"`
	Kind             string `json:"kind"`
	Downloaded       int    `json:"downloaded"`
	Total            int64  `json:"total"`
	DirectDownloaded int    `json:"directDownloaded"`
	EmailDownloaded  int    `json:"emailDownloaded"`
	Status           string `json:"status"`
}

type GUIPersistedSettings struct {
	Source           string  `json:"source"`
	CSVEmail         string  `json:"csvEmail"`
	CSVIMAPHost      string  `json:"csvImapHost"`
	CSVIMAPPort      int     `json:"csvImapPort"`
	CSVIMAPUser      string  `json:"csvImapUser"`
	CSVIMAPPassword  string  `json:"csvImapPassword"`
	CSVStartTime     int64   `json:"csvStartTime"`
	CSVEndTime       int64   `json:"csvEndTime"`
	Workers          int     `json:"workers"`
	RPS              float64 `json:"rps"`
	TimeoutSeconds   int     `json:"timeoutSeconds"`
	RawDir           string  `json:"rawDir"`
	OutputDir        string  `json:"outputDir"`
	OutputPrefix     string  `json:"outputPrefix"`
	Incremental      bool    `json:"incremental"`
	RiskCooldownSecs int     `json:"riskCooldownSecs"`
}

type GUIChainPreset struct {
	Code         string
	Name         string
	RPCURL       string
	FallbackURLs []string
	NativeSymbol string
	LogBatch     uint64
}

var guiChainPresets = []GUIChainPreset{
	{Code: "ETH", Name: "Ethereum", RPCURL: "https://ethereum-rpc.publicnode.com", FallbackURLs: []string{"https://eth.llamarpc.com", "https://rpc.ankr.com/eth", "https://1rpc.io/eth"}, NativeSymbol: "ETH"},
	{Code: "BSC", Name: "BNB Smart Chain", RPCURL: "https://bsc-mainnet.public.blastapi.io", FallbackURLs: []string{"https://bnb.api.onfinality.io/public", "https://1rpc.io/bnb", "https://bsc-pokt.nodies.app"}, NativeSymbol: "BNB", LogBatch: 10},
	{Code: "POLYGON", Name: "Polygon", RPCURL: "https://polygon-rpc.com", FallbackURLs: []string{"https://polygon-bor-rpc.publicnode.com", "https://rpc.ankr.com/polygon", "https://1rpc.io/matic"}, NativeSymbol: "POL"},
	{Code: "BASE", Name: "Base", RPCURL: "https://mainnet.base.org", FallbackURLs: []string{"https://base.llamarpc.com", "https://base-rpc.publicnode.com", "https://1rpc.io/base"}, NativeSymbol: "ETH"},
	{Code: "ARBITRUM", Name: "Arbitrum One", RPCURL: "https://arb1.arbitrum.io/rpc", FallbackURLs: []string{"https://arbitrum.llamarpc.com", "https://arbitrum-one-rpc.publicnode.com", "https://1rpc.io/arb"}, NativeSymbol: "ETH"},
	{Code: "OP", Name: "Optimism", RPCURL: "https://mainnet.optimism.io", FallbackURLs: []string{"https://optimism.llamarpc.com", "https://optimism-rpc.publicnode.com", "https://1rpc.io/op"}, NativeSymbol: "ETH"},
	{Code: "AVAXC", Name: "Avalanche C-Chain", RPCURL: "https://api.avax.network/ext/bc/C/rpc", FallbackURLs: []string{"https://avalanche-c-chain-rpc.publicnode.com", "https://rpc.ankr.com/avalanche", "https://1rpc.io/avax/c"}, NativeSymbol: "AVAX"},
}

func startGUI(preferredPort int) error {
	manager, err := NewGUIManager("")
	if err != nil {
		return fmt.Errorf("加载历史任务失败: %w", err)
	}
	mux := newGUIHandler(manager)

	ln, port, err := listenLocal(preferredPort)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	fmt.Println("可视化界面已启动:", url)
	_ = openBrowser(url)

	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "界面服务错误:", err)
		}
	}()
	select {}
}

func newGUIHandler(manager *GUIManager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/gui-rebind.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write([]byte(guiPageRebindJS))
	})
	mux.HandleFunc("/gui-history.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write([]byte(guiHistoryJS))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(guiHTML))
	})
	mux.HandleFunc("/api/start", manager.handleStart)
	mux.HandleFunc("/api/resume", manager.handleResume)
	mux.HandleFunc("/api/job", manager.handleJob)
	mux.HandleFunc("/api/jobs", manager.handleJobs)
	mux.HandleFunc("/api/history", manager.handleHistory)
	mux.HandleFunc("/api/history/import", manager.handleHistoryImport)
	mux.HandleFunc("/api/history/resume", manager.handleHistoryResume)
	mux.HandleFunc("/api/cancel", manager.handleCancel)
	mux.HandleFunc("/api/settings", handleGUISettings)
	return mux
}

func listenLocal(preferredPort int) (net.Listener, int, error) {
	if preferredPort <= 0 {
		preferredPort = 8787
	}
	for port := preferredPort; port < preferredPort+20; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return ln, port, nil
		}
	}
	return nil, 0, fmt.Errorf("无法监听 127.0.0.1:%d-%d", preferredPort, preferredPort+19)
}

func openBrowser(url string) error {
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err == nil {
		return nil
	}
	return exec.Command("cmd", "/c", "start", "", url).Start()
}

func handleGUISettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := loadGUIPersistedSettings()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, settings)
	case http.MethodPost:
		var settings GUIPersistedSettings
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&settings); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		settings = normalizeGUIPersistedSettings(settings)
		if err := saveGUIPersistedSettings(settings); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, settings)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func defaultGUIPersistedSettings() GUIPersistedSettings {
	return GUIPersistedSettings{
		Source:           "rpc",
		CSVIMAPPort:      993,
		CSVStartTime:     defaultCSVStartTime,
		Workers:          4,
		RPS:              2,
		TimeoutSeconds:   30,
		RawDir:           "raw",
		OutputDir:        "exports",
		OutputPrefix:     "wallet_export",
		RiskCooldownSecs: int(defaultGUIRiskCooldown.Seconds()),
	}
}

func normalizeGUIPersistedSettings(settings GUIPersistedSettings) GUIPersistedSettings {
	defaults := defaultGUIPersistedSettings()
	settings.Source = strings.ToLower(strings.TrimSpace(settings.Source))
	if !supportedSource(settings.Source) {
		settings.Source = defaults.Source
	}
	settings.CSVEmail = strings.TrimSpace(settings.CSVEmail)
	settings.CSVIMAPHost = strings.TrimSpace(settings.CSVIMAPHost)
	settings.CSVIMAPUser = strings.TrimSpace(settings.CSVIMAPUser)
	settings.CSVIMAPPassword = strings.TrimSpace(settings.CSVIMAPPassword)
	if settings.CSVIMAPPort <= 0 {
		settings.CSVIMAPPort = defaults.CSVIMAPPort
	}
	if settings.CSVStartTime <= 0 {
		settings.CSVStartTime = defaults.CSVStartTime
	}
	if settings.CSVEndTime < 0 {
		settings.CSVEndTime = 0
	}
	if settings.Workers <= 0 {
		settings.Workers = defaults.Workers
	}
	if settings.RPS < 0 {
		settings.RPS = defaults.RPS
	}
	if settings.TimeoutSeconds <= 0 {
		settings.TimeoutSeconds = defaults.TimeoutSeconds
	}
	if settings.RiskCooldownSecs <= 0 {
		settings.RiskCooldownSecs = defaults.RiskCooldownSecs
	}
	settings.RawDir = strings.TrimSpace(firstNonEmpty(settings.RawDir, defaults.RawDir))
	settings.OutputDir = strings.TrimSpace(firstNonEmpty(settings.OutputDir, defaults.OutputDir))
	settings.OutputPrefix = strings.TrimSpace(firstNonEmpty(settings.OutputPrefix, defaults.OutputPrefix))
	return settings
}

func loadGUIPersistedSettings() (GUIPersistedSettings, error) {
	settings := defaultGUIPersistedSettings()
	path, err := guiSettingsPath()
	if err != nil {
		return settings, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return settings, nil
	}
	if err := json.Unmarshal(b, &settings); err != nil {
		return defaultGUIPersistedSettings(), err
	}
	return normalizeGUIPersistedSettings(settings), nil
}

func saveGUIPersistedSettings(settings GUIPersistedSettings) error {
	path, err := guiSettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(normalizeGUIPersistedSettings(settings), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}

func guiSettingsPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "wallet-exporter", "gui-settings.json"), nil
}

func (m *GUIManager) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req GUIStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	entries := parseGUIAddressChains(req)
	if len(entries) == 0 {
		http.Error(w, "请至少输入一个地址", http.StatusBadRequest)
		return
	}
	id := newJobID()
	ctx, cancel := context.WithCancel(context.Background())
	job := &GUIJob{
		ID:                 id,
		Status:             "running",
		Message:            "等待开始",
		Progress:           0,
		Total:              len(entries),
		Running:            true,
		StartedAt:          time.Now().Format("2006-01-02 15:04:05"),
		Addresses:          newGUIAddressProgress(entries),
		request:            req,
		Incremental:        req.Incremental,
		entries:            append([]GUIAddressChain(nil), entries...),
		cancel:             cancel,
		addressCancels:     map[int]context.CancelFunc{},
		cancelledAddresses: map[int]bool{},
		store:              m.store,
		history:            m.history,
		historyID:          id,
	}
	m.mu.Lock()
	if m.scheduler == nil {
		m.scheduler = NewGUIBatchScheduler(1)
	}
	job.scheduler = m.scheduler
	m.jobs[id] = job
	m.mu.Unlock()
	job.persist()

	go runGUIJob(ctx, job, req, entries, 0)
	writeJSON(w, job.snapshot())
}

func (m *GUIManager) handleJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	m.mu.Lock()
	job := m.jobs[id]
	m.mu.Unlock()
	if job == nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	job.persist()
	writeJSON(w, job.snapshot())
}

func (m *GUIManager) handleCancel(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	m.mu.Lock()
	job := m.jobs[id]
	m.mu.Unlock()
	if job == nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	if indexText := strings.TrimSpace(r.URL.Query().Get("index")); indexText != "" {
		index, err := strconv.Atoi(indexText)
		if err != nil {
			http.Error(w, "invalid index", http.StatusBadRequest)
			return
		}
		if !job.requestAddressCancel(index) {
			http.Error(w, "address not found", http.StatusNotFound)
			return
		}
		job.persist()
		writeJSON(w, job.snapshot())
		return
	}
	job.requestCancelAll()
	job.persist()
	writeJSON(w, job.snapshot())
}

func runGUIJob(ctx context.Context, job *GUIJob, req GUIStartRequest, entries []GUIAddressChain, startIndex int) {
	defer func() {
		job.mu.Lock()
		if job.Status == "running" {
			job.Status = "done"
			job.Message = "完成"
			job.Progress = 100
		}
		job.Running = false
		job.FinishedAt = time.Now().Format("2006-01-02 15:04:05")
		job.mu.Unlock()
		job.persist()
	}()

	baseCfg := req.toConfig()
	outputDir := strings.TrimSpace(req.OutputDir)
	if outputDir == "" {
		outputDir = "exports"
	}
	outputDirAbs, _ := filepath.Abs(outputDir)
	taskDir, reportPath, err := prepareGUIOutputDirectories(outputDirAbs, req.OutputPrefix, job.ID, entries)
	if err != nil {
		job.fail(fmt.Errorf("创建任务组输出目录失败: %w", err))
		return
	}
	job.log("任务组输出目录: " + taskDir)
	job.mu.Lock()
	job.TaskDir = taskDir
	job.mu.Unlock()
	job.persist()
	defer func() {
		snapshot := job.snapshot()
		if err := writeGUIDownloadReport(reportPath, snapshot.Addresses); err != nil {
			job.addError(fmt.Errorf("写入下载情况.xlsx 失败: %w", err))
			return
		}
		job.addResult(reportPath)
		job.log("生成下载情况: " + reportPath)
		job.persist()
	}()
	addressCounts := map[string]int{}
	for _, entry := range entries {
		addressCounts[strings.ToLower(strings.TrimSpace(entry.Address))]++
	}

	for i := startIndex; i < len(entries); i++ {
		entry := entries[i]
		address := strings.TrimSpace(entry.Address)
		chainCode := strings.ToUpper(strings.TrimSpace(entry.Chain))
		select {
		case <-ctx.Done():
			job.cancelled()
			return
		default:
		}
		if job.addressCancelRequested(i) {
			job.markAddressCancelled(i, "已取消")
			continue
		}
		addressCtx, addressCancel := context.WithCancel(ctx)
		cfg := baseCfg
		cfg.Address = address
		applyGUIPresetToConfig(&cfg, chainCode, req.RPCURL == "")
		cfg, incrementalMessage := guiIncrementalConfig(cfg, entry, req.Incremental)
		cfg.Out = buildGUIOutputPath(taskDir, req.OutputPrefix, address, chainCode, i, cfg.Source, addressCounts[strings.ToLower(address)] > 1)
		var ticks atomic.Int64
		cfg.Progress = func(msg string) {
			tick := int(ticks.Add(1))
			p := tick
			if p > 99 {
				p = 99
			}
			job.setAddressProgress(i, p, msg)
			job.log(msg)
		}

		if err := validateConfig(cfg); err != nil {
			addressCancel()
			job.failAddress(i, fmt.Errorf("%s 参数错误: %w", address, err))
			continue
		}
		job.markAddressQueued(i, "等待全局队列")
		release, err := job.scheduler.Acquire(addressCtx, job)
		if err != nil {
			addressCancel()
			job.markAddressCancelled(i, "已取消")
			if ctx.Err() != nil {
				job.cancelled()
				return
			}
			continue
		}

		startMessage := fmt.Sprintf("开始地址 %d/%d: %s [%s]", i+1, len(entries), address, firstNonEmpty(chainCode, strings.Join(cfg.Chains, ",")))
		if incrementalMessage != "" {
			startMessage += "，" + incrementalMessage
		}
		if err := job.startAddress(i, addressCancel, startMessage); err != nil {
			release()
			addressCancel()
			job.fail(err)
			return
		}
		job.log(startMessage)

		data := runExportForConfig(addressCtx, cfg)
		release()
		if addressCtx.Err() != nil {
			addressCancel()
			job.markAddressCancelled(i, "已取消")
			if ctx.Err() != nil {
				job.cancelled()
				return
			}
			continue
		}
		job.setAddressDataCounts(i, data)
		if cfg.AMLLabels && cfg.AMLAPIKey != "" {
			cfg.Progress("DeepAML: 查询地址标签并过滤交易所")
			if err := applyAMLLabelsAndFilter(addressCtx, cfg, &data); err != nil {
				data.Errors = append(data.Errors, err.Error())
			}
			if addressCtx.Err() != nil {
				addressCancel()
				job.markAddressCancelled(i, "已取消")
				if ctx.Err() != nil {
					job.cancelled()
					return
				}
				continue
			}
			fillSummaryCounters(&data)
			sortExportData(&data)
		}
		for _, msg := range data.Errors {
			job.addAddressError(i, fmt.Errorf("%s: %s", address, msg))
		}
		if isGUIRateLimitData(data) {
			until := job.scheduler.StartCooldown(guiRiskCooldown(req.RiskCooldownSecs))
			addressCancel()
			job.coolAddress(i, until, "检测到 OKLink 429/风控；全局队列已冷却，冷却结束后请手动继续")
			return
		}
		if err := csvAddressFailureError(cfg, data); err != nil {
			addressCancel()
			job.pauseAddress(i, err)
			return
		}
		useCSVWriter := strings.EqualFold(cfg.Source, "csv")
		resultPath := cfg.Out
		if useCSVWriter {
			files, err := writeGUIRawCSVDeliverables(cfg, data)
			if err != nil {
				addressCancel()
				job.failAddress(i, fmt.Errorf("%s 写入 CSV 失败: %w", address, err))
				continue
			}
			if len(files) > 0 {
				resultPath = strings.Join(files, "; ")
			}
		} else {
			if err := writeWorkbook(cfg, data); err != nil {
				addressCancel()
				job.failAddress(i, fmt.Errorf("%s 写入 Excel 失败: %w", address, err))
				continue
			}
		}
		addressCancel()
		job.completeAddress(i, resultPath, fmt.Sprintf("完成地址 %d/%d: %s", i+1, len(entries), address))
		job.log("生成文件: " + resultPath)
	}
}

func csvAddressFailureError(cfg Config, data ExportData) error {
	if !strings.EqualFold(strings.TrimSpace(cfg.Source), "csv") || len(data.Errors) == 0 {
		return nil
	}
	firstErr := "CSV 下载失败"
	for _, msg := range data.Errors {
		if trimmed := strings.TrimSpace(msg); trimmed != "" {
			firstErr = trimmed
			break
		}
	}
	return fmt.Errorf("%s CSV 下载失败: %s", strings.TrimSpace(cfg.Address), firstErr)
}

func runExportForConfig(ctx context.Context, cfg Config) ExportData {
	return collectForSource(ctx, cfg)
}

func (r GUIStartRequest) toConfig() Config {
	timeout := time.Duration(r.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	workers := r.Workers
	if workers <= 0 {
		workers = 4
	}
	pageSize := r.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	retries := r.Retries
	if retries < 0 {
		retries = 0
	}
	blockBatch := r.BlockBatch
	if blockBatch == 0 {
		blockBatch = 100
	}
	logBatch := r.LogBatch
	if logBatch == 0 {
		logBatch = 50
	}
	source := normalizedSource(r.Source)
	if !supportedSource(source) {
		source = "rpc"
	}
	traceMode := strings.ToLower(strings.TrimSpace(r.TraceMode))
	if traceMode == "" {
		traceMode = "auto"
	}
	amlBaseURL := "https://openapi.deepaml.io"
	return normalizeCSVMailConfig(Config{
		Chains:          splitCSV(firstNonEmpty(r.Chains, defaultChains)),
		Protocols:       splitCSV(defaultProtocols),
		APIKey:          "",
		BaseURL:         guiBaseURL(),
		Source:          source,
		CSVEmail:        strings.TrimSpace(r.CSVEmail),
		CSVIMAPHost:     strings.TrimSpace(r.CSVIMAPHost),
		CSVIMAPPort:     r.CSVIMAPPort,
		CSVIMAPUser:     strings.TrimSpace(r.CSVIMAPUser),
		CSVIMAPPassword: strings.TrimSpace(r.CSVIMAPPassword),
		CSVStartTime:    r.CSVStartTime,
		CSVEndTime:      r.CSVEndTime,
		CSVRequestHAR:   firstNonEmpty(strings.TrimSpace(r.CSVRequestHAR), os.Getenv("OKLINK_CSV_REQUEST_HAR")),
		AMLAPIKey:       firstNonEmpty(strings.TrimSpace(r.AMLKey), os.Getenv("DEEPAML_API_KEY")),
		AMLBaseURL:      amlBaseURL,
		AMLLabels:       r.AMLLabels,
		AMLRPS:          r.AMLRPS,
		FilterExchange:  r.FilterExchange,
		RPCURL:          firstNonEmpty(strings.TrimSpace(r.RPCURL), os.Getenv("RPC_URL")),
		RPCConfig:       strings.TrimSpace(r.RPCConfig),
		RawDir:          strings.TrimSpace(r.RawDir),
		Workers:         workers,
		RPS:             r.RPS,
		PageSize:        pageSize,
		Timeout:         timeout,
		Retries:         retries,
		Details:         r.Details,
		ScanNative:      r.ScanNative,
		StartBlock:      r.StartBlock,
		EndBlock:        r.EndBlock,
		CutoffBlock:     r.CutoffBlock,
		BlockBatch:      blockBatch,
		LogBatch:        logBatch,
		TraceMode:       traceMode,
		NativeSymbol:    strings.TrimSpace(r.NativeSymbol),
	})
}

func parseGUIAddresses(input string) []string {
	parts := strings.FieldsFunc(input, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t' || r == ',' || r == ';' || r == '，' || r == '；' || r == ' '
	})
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		addr := normalizeGUIAddress(part)
		if addr == "" {
			continue
		}
		key := strings.ToLower(addr)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, addr)
	}
	return out
}

func parseGUIAddressChains(req GUIStartRequest) []GUIAddressChain {
	out := make([]GUIAddressChain, 0, len(req.AddressChains))
	seen := map[string]bool{}
	defaultChain := strings.ToUpper(strings.TrimSpace(firstNonEmpty(req.Chains, "ETH")))
	add := func(address, chain string) {
		address = normalizeGUIAddress(address)
		chain = strings.ToUpper(strings.TrimSpace(firstNonEmpty(chain, defaultChain)))
		if address == "" {
			return
		}
		if lookupGUIChainPreset(chain) == nil {
			chain = defaultChain
		}
		key := strings.ToLower(address) + "|" + chain
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, GUIAddressChain{Address: address, Chain: chain})
	}
	for _, item := range req.AddressChains {
		add(item.Address, item.Chain)
	}
	if len(out) == 0 {
		for _, address := range parseGUIAddresses(req.Addresses) {
			add(address, defaultChain)
		}
	}
	return out
}

func applyGUIPresetToConfig(cfg *Config, chainCode string, fillRPC bool) {
	preset := lookupGUIChainPreset(chainCode)
	if preset == nil {
		return
	}
	cfg.Chains = []string{preset.Code}
	if fillRPC || strings.TrimSpace(cfg.RPCURL) == "" {
		cfg.RPCURL = preset.RPCURL
	}
	cfg.RPCFallbacks = preset.FallbackURLs
	if strings.TrimSpace(cfg.NativeSymbol) == "" {
		cfg.NativeSymbol = preset.NativeSymbol
	}
	if preset.LogBatch > 0 {
		cfg.LogBatch = preset.LogBatch
	}
}

func lookupGUIChainPreset(chainCode string) *GUIChainPreset {
	code := strings.ToUpper(strings.TrimSpace(chainCode))
	for i := range guiChainPresets {
		if guiChainPresets[i].Code == code {
			return &guiChainPresets[i]
		}
	}
	return nil
}

func buildGUIOutputPath(outputDir, prefix, address, chain string, idx int, source string, duplicateAddress bool) string {
	addressDir := guiAddressOutputDir(outputDir, address, idx)
	if strings.EqualFold(source, "csv") {
		name := sanitizeFilePart(strings.ToLower(address))
		if name == "" {
			name = fmt.Sprintf("address_%03d", idx+1)
		}
		if duplicateAddress && strings.TrimSpace(chain) != "" {
			name += "_" + sanitizeFilePart(chain)
		}
		return filepath.Join(addressDir, name+".csv")
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "wallet_export"
	}
	stamp := time.Now().Format("20060102_150405")
	short := shortAddressForFile(address)
	name := fmt.Sprintf("%s_%03d_%s_%s_%s.xlsx", limitFilePart(sanitizeFilePart(prefix), 12), idx+1, sanitizeFilePart(chain), short, stamp)
	path := filepath.Join(addressDir, name)
	if len(path) > 200 {
		name = fmt.Sprintf("%03d_%s.xlsx", idx+1, sanitizeFilePart(chain))
		path = filepath.Join(addressDir, name)
	}
	return path
}

func shortAddressForFile(address string) string {
	s := sanitizeFilePart(address)
	if len(s) <= 28 {
		return s
	}
	return s[:14] + "_" + s[len(s)-10:]
}

func newJobID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func (j *GUIJob) snapshot() *GUIJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	snapshot := &GUIJob{
		ID:                j.ID,
		Status:            j.Status,
		Message:           j.Message,
		Progress:          j.Progress,
		Done:              j.Done,
		Total:             j.Total,
		Running:           j.Running,
		NeedsCredentials:  j.NeedsCredentials,
		StartedAt:         j.StartedAt,
		FinishedAt:        j.FinishedAt,
		Logs:              append([]string(nil), j.Logs...),
		Results:           append([]string(nil), j.Results...),
		Errors:            append([]string(nil), j.Errors...),
		Addresses:         cloneGUIAddressProgress(j.Addresses),
		TaskDir:           j.TaskDir,
		CheckpointSummary: append([]GUIJobCheckpointSummary(nil), j.CheckpointSummary...),
		Incremental:       j.Incremental,
		QueuePosition:     j.QueuePosition,
		CooldownUntil:     j.CooldownUntil,
	}
	if j.scheduler != nil {
		queue := j.scheduler.Snapshot()
		snapshot.QueueActive = queue.Active
		snapshot.QueueWaiting = queue.Waiting
		if queue.CooldownUntil.After(time.Now()) {
			snapshot.CooldownUntil = queue.CooldownUntil.Format(time.RFC3339)
		}
	}
	return snapshot
}

func newGUIAddressProgress(entries []GUIAddressChain) []GUIAddressProgress {
	out := make([]GUIAddressProgress, 0, len(entries))
	for i, entry := range entries {
		out = append(out, GUIAddressProgress{
			Index:      i,
			Address:    strings.TrimSpace(entry.Address),
			Chain:      strings.ToUpper(strings.TrimSpace(entry.Chain)),
			Status:     "pending",
			Message:    "等待下载",
			Downloaded: 0,
			Total:      -1,
		})
	}
	return out
}

func cloneGUIAddressProgress(src []GUIAddressProgress) []GUIAddressProgress {
	out := append([]GUIAddressProgress(nil), src...)
	for i := range out {
		out[i].Errors = append([]string(nil), src[i].Errors...)
		out[i].Parts = append([]GUIAddressDownloadPart(nil), src[i].Parts...)
	}
	return out
}

func (j *GUIJob) log(msg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	line := time.Now().Format("15:04:05") + "  " + msg
	j.Logs = append(j.Logs, line)
	if len(j.Logs) > 500 {
		j.Logs = j.Logs[len(j.Logs)-500:]
	}
}

func (j *GUIJob) setProgress(progress int, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	if progress > j.Progress {
		j.Progress = progress
	}
	j.Message = message
}

func (j *GUIJob) setDone(done int, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Done = done
	if j.Total > 0 {
		j.Progress = int(float64(done) / float64(j.Total) * 100)
	}
	j.Message = message
}

func (j *GUIJob) addResult(path string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Results = append(j.Results, path)
}

func (j *GUIJob) addError(err error) {
	if err == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	msg := err.Error()
	j.Errors = append(j.Errors, msg)
	j.Logs = append(j.Logs, time.Now().Format("15:04:05")+"  错误: "+msg)
}

func (j *GUIJob) requestCancelAll() {
	var cancel context.CancelFunc
	j.mu.Lock()
	if j.cancel != nil && j.Running {
		cancel = j.cancel
		j.Message = "正在取消"
		for i := range j.Addresses {
			if isTerminalGUIAddressStatus(j.Addresses[i].Status) {
				continue
			}
			j.Addresses[i].CancelRequested = true
			if j.cancelledAddresses != nil {
				j.cancelledAddresses[i] = true
			}
			if j.Addresses[i].Status == "pending" {
				j.Addresses[i].Status = "cancelled"
				j.Addresses[i].Message = "已取消"
				j.Addresses[i].FinishedAt = time.Now().Format("2006-01-02 15:04:05")
			}
		}
		j.syncOverallProgressLocked()
	}
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (j *GUIJob) requestAddressCancel(index int) bool {
	var cancel context.CancelFunc
	now := time.Now().Format("2006-01-02 15:04:05")
	j.mu.Lock()
	if index < 0 || index >= len(j.Addresses) {
		j.mu.Unlock()
		return false
	}
	if j.cancelledAddresses == nil {
		j.cancelledAddresses = map[int]bool{}
	}
	j.cancelledAddresses[index] = true
	addr := &j.Addresses[index]
	if !isTerminalGUIAddressStatus(addr.Status) {
		addr.CancelRequested = true
		switch addr.Status {
		case "running":
			addr.Message = "正在取消"
			cancel = j.addressCancels[index]
			j.Message = fmt.Sprintf("正在取消地址 %d/%d: %s", index+1, j.Total, addr.Address)
		case "pending":
			addr.Status = "cancelled"
			addr.Message = "已取消"
			addr.FinishedAt = now
			j.Message = fmt.Sprintf("已取消地址 %d/%d: %s", index+1, j.Total, addr.Address)
		default:
			addr.Message = "已取消"
			addr.FinishedAt = now
		}
		j.syncOverallProgressLocked()
	}
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

func (j *GUIJob) addressCancelRequested(index int) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.cancelledAddresses != nil && j.cancelledAddresses[index]
}

func (j *GUIJob) startAddress(index int, cancel context.CancelFunc, message string) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	j.mu.Lock()
	if index < 0 || index >= len(j.Addresses) {
		j.mu.Unlock()
		return nil
	}
	if j.addressCancels == nil {
		j.addressCancels = map[int]context.CancelFunc{}
	}
	j.addressCancels[index] = cancel
	addr := &j.Addresses[index]
	addr.Status = "running"
	addr.Message = message
	addr.StartedAt = now
	addr.UpdatedAt = now
	if addr.Progress < 1 {
		addr.Progress = 1
	}
	j.Message = message
	j.syncOverallProgressLocked()
	j.mu.Unlock()
	return j.save("address start")
}

func (j *GUIJob) setAddressProgress(index, progress int, message string) {
	defer j.persist()
	j.mu.Lock()
	defer j.mu.Unlock()
	if index < 0 || index >= len(j.Addresses) {
		j.Message = message
		return
	}
	addr := &j.Addresses[index]
	if progress < 0 {
		progress = 0
	}
	if progress > 99 && addr.Status == "running" {
		progress = 99
	}
	if progress > 100 {
		progress = 100
	}
	if progress > addr.Progress {
		addr.Progress = progress
	}
	addr.Message = message
	addr.UpdatedAt = nowGUIActivityTime()
	j.Message = message
	if part, hasDownloaded, hasTotal, ok := parseGUIProgressDownloadPart(message); ok {
		j.applyAddressDownloadPartLocked(index, part, hasDownloaded, hasTotal)
	}
	j.syncOverallProgressLocked()
}

func (j *GUIJob) setAddressDataCounts(index int, data ExportData) {
	defer j.persist()
	j.mu.Lock()
	defer j.mu.Unlock()
	if index < 0 || index >= len(j.Addresses) {
		return
	}
	if len(data.CSVDownloadChecks) > 0 {
		parts := make([]GUIAddressDownloadPart, 0, len(data.CSVDownloadChecks))
		for _, check := range data.CSVDownloadChecks {
			part := GUIAddressDownloadPart{
				Key:              guiDownloadPartKey(check.Chain, check.Kind),
				Chain:            strings.ToUpper(strings.TrimSpace(check.Chain)),
				Kind:             strings.TrimSpace(check.Kind),
				Downloaded:       check.Downloaded,
				Total:            check.ExpectedTotal,
				DirectDownloaded: check.DirectDownloaded,
				EmailDownloaded:  check.EmailDownloaded,
				Status:           strings.TrimSpace(check.Status),
			}
			if part.Total < 0 {
				part.Total = -1
			}
			parts = append(parts, part)
		}
		j.Addresses[index].Parts = parts
		j.recalculateAddressDownloadTotalsLocked(index)
		j.syncOverallProgressLocked()
		return
	}
	downloaded := countGUIExportRows(data)
	if downloaded > j.Addresses[index].Downloaded {
		j.Addresses[index].Downloaded = downloaded
	}
	if j.Addresses[index].Total == 0 {
		j.Addresses[index].Total = -1
	}
	j.syncOverallProgressLocked()
}

func (j *GUIJob) addAddressError(index int, err error) {
	if err == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	msg := err.Error()
	j.Errors = append(j.Errors, msg)
	j.Logs = append(j.Logs, time.Now().Format("15:04:05")+"  错误: "+msg)
	if index >= 0 && index < len(j.Addresses) {
		j.Addresses[index].Errors = append(j.Addresses[index].Errors, msg)
	}
}

func (j *GUIJob) failAddress(index int, err error) {
	defer j.persist()
	if err == nil {
		return
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	j.mu.Lock()
	defer j.mu.Unlock()
	msg := err.Error()
	j.Errors = append(j.Errors, msg)
	j.Logs = append(j.Logs, time.Now().Format("15:04:05")+"  错误: "+msg)
	if index >= 0 && index < len(j.Addresses) {
		addr := &j.Addresses[index]
		addr.Status = "failed"
		addr.Message = msg
		addr.FinishedAt = now
		addr.Errors = append(addr.Errors, msg)
		delete(j.addressCancels, index)
	}
	j.Message = msg
	j.syncOverallProgressLocked()
}

func (j *GUIJob) completeAddress(index int, result, message string) {
	defer j.persist()
	now := time.Now().Format("2006-01-02 15:04:05")
	j.mu.Lock()
	defer j.mu.Unlock()
	if index >= 0 && index < len(j.Addresses) {
		addr := &j.Addresses[index]
		addr.Status = "done"
		addr.Message = message
		addr.Progress = 100
		addr.Result = result
		addr.FinishedAt = now
		delete(j.addressCancels, index)
	}
	j.Results = append(j.Results, result)
	j.Message = message
	j.syncOverallProgressLocked()
}

func (j *GUIJob) markAddressCancelled(index int, message string) {
	defer j.persist()
	now := time.Now().Format("2006-01-02 15:04:05")
	j.mu.Lock()
	defer j.mu.Unlock()
	if index >= 0 && index < len(j.Addresses) {
		addr := &j.Addresses[index]
		addr.Status = "cancelled"
		addr.Message = message
		addr.CancelRequested = true
		addr.FinishedAt = now
		delete(j.addressCancels, index)
		if j.cancelledAddresses != nil {
			j.cancelledAddresses[index] = true
		}
	}
	j.Message = message
	j.syncOverallProgressLocked()
}

func (j *GUIJob) syncOverallProgressLocked() {
	if j.Total <= 0 {
		return
	}
	done := 0
	progressUnits := 0
	for _, addr := range j.Addresses {
		if isTerminalGUIAddressStatus(addr.Status) {
			done++
			progressUnits += 100
			continue
		}
		progressUnits += clampPercent(addr.Progress)
	}
	j.Done = done
	progress := progressUnits / j.Total
	if progress > 100 {
		progress = 100
	}
	if progress > j.Progress {
		j.Progress = progress
	}
}

func (j *GUIJob) applyAddressDownloadPartLocked(index int, update GUIAddressDownloadPart, hasDownloaded, hasTotal bool) {
	if index < 0 || index >= len(j.Addresses) {
		return
	}
	addr := &j.Addresses[index]
	partIndex := -1
	for i := range addr.Parts {
		if addr.Parts[i].Key == update.Key {
			partIndex = i
			break
		}
	}
	if partIndex < 0 {
		addr.Parts = append(addr.Parts, GUIAddressDownloadPart{
			Key:    update.Key,
			Chain:  update.Chain,
			Kind:   update.Kind,
			Total:  -1,
			Status: "pending",
		})
		partIndex = len(addr.Parts) - 1
	}
	part := &addr.Parts[partIndex]
	if update.Chain != "" {
		part.Chain = update.Chain
	}
	if update.Kind != "" {
		part.Kind = update.Kind
	}
	if hasDownloaded && update.Downloaded > part.Downloaded {
		part.Downloaded = update.Downloaded
	}
	if update.DirectDownloaded > part.DirectDownloaded {
		part.DirectDownloaded = update.DirectDownloaded
	}
	if update.EmailDownloaded > part.EmailDownloaded {
		part.EmailDownloaded = update.EmailDownloaded
	}
	if hasTotal {
		part.Total = update.Total
	}
	if update.Status != "" {
		part.Status = update.Status
	}
	j.recalculateAddressDownloadTotalsLocked(index)
}

func (j *GUIJob) recalculateAddressDownloadTotalsLocked(index int) {
	if index < 0 || index >= len(j.Addresses) {
		return
	}
	addr := &j.Addresses[index]
	if len(addr.Parts) == 0 {
		return
	}
	downloaded := 0
	total := int64(0)
	totalKnown := true
	for _, part := range addr.Parts {
		downloaded += part.Downloaded
		if part.Total < 0 {
			totalKnown = false
			continue
		}
		total += part.Total
	}
	addr.Downloaded = downloaded
	if totalKnown {
		addr.Total = total
		if total > 0 {
			progress := int(float64(downloaded) / float64(total) * 100)
			if progress > 99 && addr.Status == "running" {
				progress = 99
			}
			if progress < 1 && addr.Status == "running" {
				progress = 1
			}
			addr.Progress = progress
		}
		return
	}
	addr.Total = -1
}

func isTerminalGUIAddressStatus(status string) bool {
	switch status {
	case "done", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func clampPercent(progress int) int {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func countGUIExportRows(data ExportData) int {
	return len(data.Transactions) + len(data.Internals) + len(data.TokenTransfers) + len(data.NFTTransfers) + len(data.Assets) + len(data.Funds)
}

func parseGUIProgressDownloadPart(message string) (GUIAddressDownloadPart, bool, bool, bool) {
	if part, ok := parseGUIProgressTotalMessage(message); ok {
		return part, false, true, true
	}
	if part, hasDownloaded, hasTotal, ok := parseGUIBrowserProgressMessage(message); ok {
		return part, hasDownloaded, hasTotal, true
	}
	if part, ok := parseGUIProgressCountMessage(message); ok {
		return part, true, part.Total >= 0, true
	}
	if part, ok := parseGUIProgressCompleteMessage(message); ok {
		return part, true, true, true
	}
	return GUIAddressDownloadPart{}, false, false, false
}

func parseGUIProgressTotalMessage(message string) (GUIAddressDownloadPart, bool) {
	const prefix = "CSV total "
	if !strings.HasPrefix(message, prefix) || strings.Contains(message, " failed:") {
		return GUIAddressDownloadPart{}, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(message, prefix))
	colon := strings.Index(rest, ":")
	eq := strings.LastIndex(rest, " = ")
	if colon <= 0 || eq <= colon {
		return GUIAddressDownloadPart{}, false
	}
	chain := strings.ToUpper(strings.TrimSpace(rest[:colon]))
	kind := strings.TrimSpace(rest[colon+1 : eq])
	total, err := strconv.ParseInt(firstGUIProgressToken(rest[eq+3:]), 10, 64)
	if err != nil {
		return GUIAddressDownloadPart{}, false
	}
	return GUIAddressDownloadPart{
		Key:    guiDownloadPartKey(chain, kind),
		Chain:  chain,
		Kind:   kind,
		Total:  total,
		Status: "pending",
	}, true
}

func parseGUIProgressCountMessage(message string) (GUIAddressDownloadPart, bool) {
	const prefix = "CSV count "
	if !strings.HasPrefix(message, prefix) {
		return GUIAddressDownloadPart{}, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(message, prefix))
	colon := strings.Index(rest, ":")
	if colon <= 0 {
		return GUIAddressDownloadPart{}, false
	}
	chain := strings.ToUpper(strings.TrimSpace(rest[:colon]))
	afterChain := strings.TrimSpace(rest[colon+1:])
	segmentPos := strings.Index(afterChain, " segment ")
	totalPos := strings.LastIndex(afterChain, " total ")
	if segmentPos <= 0 || totalPos <= segmentPos {
		return GUIAddressDownloadPart{}, false
	}
	kindPart := strings.TrimSpace(afterChain[:segmentPos])
	kind, source := parseGUIProgressCountKindSource(kindPart)
	downloaded, total, hasTotal, ok := parseGUIDownloadCountText(afterChain[totalPos+len(" total "):])
	if !ok {
		return GUIAddressDownloadPart{}, false
	}
	part := GUIAddressDownloadPart{
		Key:        guiDownloadPartKey(chain, kind),
		Chain:      chain,
		Kind:       kind,
		Downloaded: downloaded,
		Total:      total,
		Status:     "running",
	}
	if sourceDownloaded, ok := parseGUISourceDownloaded(afterChain[:totalPos], source); ok {
		switch source {
		case "direct":
			part.DirectDownloaded = sourceDownloaded
		case "email":
			part.EmailDownloaded = sourceDownloaded
		}
	}
	return part, hasTotal || total < 0
}

func parseGUIProgressCountKindSource(text string) (string, string) {
	const marker = " source "
	pos := strings.LastIndex(text, marker)
	if pos <= 0 {
		return strings.TrimSpace(text), ""
	}
	kind := strings.TrimSpace(text[:pos])
	source := strings.ToLower(strings.TrimSpace(text[pos+len(marker):]))
	if source != "direct" && source != "email" {
		return strings.TrimSpace(text), ""
	}
	return kind, source
}

func parseGUISourceDownloaded(text, source string) (int, bool) {
	if source != "direct" && source != "email" {
		return 0, false
	}
	pos := strings.LastIndex(text, " "+source+" ")
	if pos < 0 {
		return 0, false
	}
	value, err := strconv.Atoi(firstGUIProgressToken(text[pos+len(source)+2:]))
	if err != nil {
		return 0, false
	}
	return value, true
}

func parseGUIProgressCompleteMessage(message string) (GUIAddressDownloadPart, bool) {
	const prefix = "CSV complete "
	if !strings.HasPrefix(message, prefix) {
		return GUIAddressDownloadPart{}, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(message, prefix))
	colon := strings.Index(rest, ":")
	if colon <= 0 {
		return GUIAddressDownloadPart{}, false
	}
	chain := strings.ToUpper(strings.TrimSpace(rest[:colon]))
	afterChain := strings.TrimSpace(rest[colon+1:])
	lastSpace := strings.LastIndex(afterChain, " ")
	if lastSpace <= 0 {
		return GUIAddressDownloadPart{}, false
	}
	kind := strings.TrimSpace(afterChain[:lastSpace])
	downloaded, total, hasTotal, ok := parseGUIDownloadCountText(afterChain[lastSpace+1:])
	if !ok || !hasTotal {
		return GUIAddressDownloadPart{}, false
	}
	return GUIAddressDownloadPart{
		Key:        guiDownloadPartKey(chain, kind),
		Chain:      chain,
		Kind:       kind,
		Downloaded: downloaded,
		Total:      total,
		Status:     "complete",
	}, true
}

func parseGUIDownloadCountText(text string) (int, int64, bool, bool) {
	token := firstGUIProgressToken(text)
	if token == "" {
		return 0, -1, false, false
	}
	if strings.Contains(token, "/") {
		parts := strings.SplitN(token, "/", 2)
		downloaded, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return 0, -1, false, false
		}
		total, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			return 0, -1, false, false
		}
		return downloaded, total, true, true
	}
	downloaded, err := strconv.Atoi(strings.TrimSpace(token))
	if err != nil {
		return 0, -1, false, false
	}
	return downloaded, -1, false, true
}

func firstGUIProgressToken(text string) string {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], "，,。;；")
}

func guiDownloadPartKey(chain, kind string) string {
	return strings.ToUpper(strings.TrimSpace(chain)) + "|" + strings.ToLower(strings.TrimSpace(kind))
}

func (j *GUIJob) fail(err error) {
	defer j.persist()
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = "failed"
	j.Running = false
	j.Message = err.Error()
	j.Errors = append(j.Errors, err.Error())
	now := time.Now().Format("2006-01-02 15:04:05")
	for i := range j.Addresses {
		if isTerminalGUIAddressStatus(j.Addresses[i].Status) {
			continue
		}
		j.Addresses[i].Status = "failed"
		j.Addresses[i].Message = err.Error()
		j.Addresses[i].FinishedAt = now
		j.Addresses[i].Errors = append(j.Addresses[i].Errors, err.Error())
	}
	j.syncOverallProgressLocked()
}

func (j *GUIJob) cancelled() {
	defer j.persist()
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = "cancelled"
	j.Running = false
	j.Message = "已取消"
	now := time.Now().Format("2006-01-02 15:04:05")
	for i := range j.Addresses {
		if isTerminalGUIAddressStatus(j.Addresses[i].Status) {
			continue
		}
		j.Addresses[i].Status = "cancelled"
		j.Addresses[i].Message = "已取消"
		j.Addresses[i].CancelRequested = true
		j.Addresses[i].FinishedAt = now
	}
	j.syncOverallProgressLocked()
}

const guiHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>钱包原始节点数据导出</title>
  <style>
    :root { --bg:#f6f7f9; --panel:#ffffff; --line:#d9dde5; --text:#1f2937; --muted:#667085; --blue:#2563eb; --blue2:#1d4ed8; --danger:#b42318; --ok:#047857; --warn:#b54708; }
    * { box-sizing: border-box; }
    body { margin:0; font-family: "Microsoft YaHei", Segoe UI, Arial, sans-serif; background:var(--bg); color:var(--text); }
    header { height:56px; display:flex; align-items:center; justify-content:space-between; padding:0 24px; border-bottom:1px solid var(--line); background:#fff; }
    header h1 { font-size:18px; margin:0; font-weight:650; }
    main { display:grid; grid-template-columns:minmax(460px, 620px) 1fr; gap:16px; padding:16px; max-width:1500px; margin:0 auto; }
    section { background:var(--panel); border:1px solid var(--line); border-radius:8px; padding:16px; }
    h2 { font-size:15px; margin:0 0 12px; }
    label { display:block; font-size:12px; color:var(--muted); margin-bottom:6px; }
    input, textarea, select { width:100%; border:1px solid #cfd5df; border-radius:6px; padding:9px 10px; font-size:14px; background:#fff; color:var(--text); }
    textarea { min-height:132px; resize:vertical; font-family:Consolas, "Microsoft YaHei", monospace; }
    .grid { display:grid; grid-template-columns:1fr 1fr; gap:12px; }
    .grid3 { display:grid; grid-template-columns:1fr 1fr 1fr; gap:12px; }
    .csvCredentials { grid-template-columns:.65fr 1.35fr 1.45fr; }
    .outputPaths { grid-template-columns:1.35fr .9fr 1.35fr; }
    .field { margin-bottom:12px; }
    .checks { display:grid; grid-template-columns:1fr 1fr; gap:8px 12px; margin:8px 0 14px; }
    .check { display:flex; align-items:center; gap:8px; color:var(--text); font-size:14px; }
    .check input { width:16px; height:16px; }
    .sourceSwitch { display:grid; grid-template-columns:1fr 1fr; gap:8px; margin-bottom:12px; }
    .sourceSwitch button { background:#eef2f7; color:var(--text); border:1px solid #cfd5df; }
    .sourceSwitch button.active { background:var(--blue); color:#fff; border-color:var(--blue); }
    .addressRow { display:grid; grid-template-columns:1fr 170px 38px; gap:8px; align-items:center; margin-bottom:8px; }
    .addressRow button { padding:9px 0; background:#667085; }
    .miniActions { display:flex; gap:8px; margin:8px 0 14px; }
    .miniActions button { background:#475467; padding:8px 10px; }
    .subTitle { font-size:15px; margin:18px 0 12px; padding-top:14px; border-top:1px solid #eef2f7; }
    .addressInputActions { display:grid; grid-template-columns:170px auto 1fr; gap:8px; align-items:center; margin:8px 0 14px; }
    .confirmedHeader { display:flex; justify-content:space-between; gap:12px; align-items:center; margin:12px 0 8px; }
    .confirmedHeader strong { font-size:13px; }
    details.advanced { border:1px solid var(--line); border-radius:8px; padding:10px 12px; margin:12px 0; background:#fbfcfe; }
    details.advanced summary { cursor:pointer; font-size:14px; font-weight:600; }
    .presetBox { padding:9px 10px; background:#f8fafc; border:1px solid var(--line); border-radius:6px; font-size:12px; color:var(--muted); margin-bottom:12px; }
    .actions { display:flex; gap:10px; align-items:center; padding-top:4px; }
    button { border:0; border-radius:6px; background:var(--blue); color:#fff; padding:10px 14px; font-size:14px; cursor:pointer; }
    button:hover { background:var(--blue2); }
    button.secondary { background:#475467; }
    button:disabled { background:#9aa4b2; cursor:not-allowed; }
    .statusLine { display:grid; grid-template-columns:1fr; gap:4px; margin-bottom:10px; color:var(--muted); font-size:13px; }
    #statusText { color:#344054; font-weight:650; line-height:1.45; }
    #countText { font-size:12px; }
    .overallProgress { padding:12px; border:1px solid var(--line); border-radius:8px; background:#fbfcfe; margin-bottom:12px; }
    .queueOverview { display:grid; grid-template-columns:repeat(3, minmax(0, 1fr)); gap:8px; margin-top:10px; }
    .queueMetric { padding:8px 9px; border:1px solid #e4e7ec; border-radius:6px; background:#fff; }
    .queueMetricLabel { display:block; color:#667085; font-size:11px; }
    .queueMetricValue { display:block; margin-top:3px; color:#1d2939; font-size:13px; font-weight:650; white-space:nowrap; }
    .queueMetric.cooling { border-color:#fedf89; background:#fffcf5; }
    .queueResume { width:100%; margin-top:9px; background:#475467; }
    .queueResume:disabled { background:#d0d5dd; color:#667085; }
    .progress { width:100%; height:14px; background:#e5e7eb; border-radius:999px; overflow:hidden; border:1px solid #d0d5dd; }
    .bar { height:100%; width:0%; background:linear-gradient(90deg,#2563eb,#059669); transition:width .25s ease; }
    .addressProgressList { display:grid; gap:10px; margin:0 0 12px; }
    .addrCard { border:1px solid #d5dce8; background:#fff; border-radius:8px; padding:12px; box-shadow:0 1px 2px rgba(16,24,40,.04); }
    .addrCard.running { border-color:#93c5fd; box-shadow:0 0 0 3px rgba(37,99,235,.08); }
    .addrCard.done { border-color:#a7f3d0; background:#fbfffd; }
    .addrCard.failed { border-color:#fecaca; background:#fffafa; }
    .addrCard.paused { border-color:#fedf89; background:#fffcf5; }
    .addrCard.queued { border-color:#bfdbfe; background:#f8fbff; }
    .addrCard.cooling { border-color:#fedf89; background:#fffcf5; }
    .addrCard.cancelled { border-color:#d0d5dd; background:#f8fafc; }
    .addrHeader { display:grid; grid-template-columns:1fr auto; gap:10px; align-items:start; margin-bottom:10px; }
    .addrTitle { min-width:0; }
    .addrName { font:12px/1.45 Consolas, "Microsoft YaHei", monospace; word-break:break-all; color:#111827; }
    .addrMeta { display:flex; flex-wrap:wrap; gap:6px; margin-top:6px; align-items:center; }
    .badge { display:inline-flex; align-items:center; height:22px; padding:0 8px; border-radius:999px; background:#eef2f7; color:#475467; font-size:12px; }
    .badge.running { background:#dbeafe; color:#1d4ed8; }
    .badge.done { background:#d1fae5; color:#047857; }
    .badge.failed { background:#fee2e2; color:#b42318; }
    .badge.paused { background:#fef0c7; color:var(--warn); }
    .badge.queued { background:#dbeafe; color:#1d4ed8; }
    .badge.cooling { background:#fef0c7; color:var(--warn); }
    .badge.cancelled { background:#e5e7eb; color:#475467; }
    .addrCancel { background:#fff; color:var(--danger); border:1px solid #fecaca; padding:7px 10px; }
    .addrCancel:hover { background:#fef2f2; }
    .addrCancel:disabled { color:#98a2b3; border-color:#e4e7ec; background:#f2f4f7; }
    .addrProgressTop { display:flex; justify-content:space-between; gap:10px; margin-bottom:6px; color:#475467; font-size:12px; }
    .addrBar { height:10px; }
    .addrMessage { margin-top:8px; color:#667085; font-size:12px; line-height:1.45; word-break:break-word; }
    .addrActivity { display:grid; grid-template-columns:repeat(3, minmax(0, 1fr)); gap:6px; margin-top:8px; }
    .activityItem { min-width:0; padding:7px 8px; border:1px solid #e4e7ec; border-radius:6px; background:#f8fafc; }
    .activityLabel { display:block; color:#667085; font-size:11px; line-height:1.2; }
    .activityValue { display:block; margin-top:3px; color:#344054; font-size:12px; line-height:1.25; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
    .partList { display:flex; flex-wrap:wrap; gap:6px; margin-top:8px; }
    .partItem { display:inline-flex; gap:6px; align-items:center; max-width:100%; padding:5px 7px; border-radius:6px; background:#f2f4f7; color:#344054; font-size:12px; }
    .partItem span { white-space:nowrap; }
    .partSource { color:#667085; }
    .addrErrors { margin-top:8px; color:var(--danger); font-size:12px; line-height:1.45; word-break:break-word; }
    .emptyProgress { border:1px dashed #cfd5df; border-radius:8px; padding:16px; color:var(--muted); text-align:center; font-size:13px; background:#fbfcfe; }
    .logPanel { position:fixed; top:72px; right:18px; z-index:50; width:min(460px, calc(100vw - 36px)); border:1px solid #d0d5dd; border-radius:8px; background:#fff; box-shadow:0 16px 36px rgba(16,24,40,.18); overflow:hidden; }
    .logPanel.collapsed { width:auto; }
    .logHeader { display:flex; align-items:center; justify-content:space-between; gap:12px; padding:10px 12px; background:#f8fafc; border-bottom:1px solid #e4e7ec; }
    .logTitle { display:flex; align-items:center; gap:8px; min-width:0; font-size:13px; font-weight:650; color:#1f2937; }
    .logDot { width:8px; height:8px; border-radius:999px; background:#2563eb; box-shadow:0 0 0 3px rgba(37,99,235,.12); }
    .logCount { color:#667085; font-size:12px; font-weight:400; white-space:nowrap; }
    .logToggle { padding:6px 9px; background:#fff; color:#344054; border:1px solid #d0d5dd; font-size:12px; cursor:pointer; }
    .logToggle:hover { background:#f2f4f7; }
    .logBody { height:min(52vh, 460px); overflow-y:auto; overflow-x:hidden; padding:10px 12px; background:#fff; overscroll-behavior:contain; }
    .logPanel.collapsed .logBody { display:none; }
    .logLine { display:grid; grid-template-columns:56px 1fr; gap:8px; padding:7px 0; border-bottom:1px solid #eef2f7; color:#344054; font-size:12px; line-height:1.45; }
    .logLine:last-child { border-bottom:0; }
    .logTime { color:#98a2b3; font-family:Consolas, monospace; }
    .logText { word-break:break-word; }
    .logEmpty { padding:14px 0; color:#667085; text-align:center; font-size:12px; }
    .results, .errors { margin-top:12px; font-size:13px; }
    .results div { padding:7px 8px; border:1px solid #bbf7d0; background:#f0fdf4; color:#065f46; border-radius:6px; margin-top:6px; word-break:break-all; }
    .errors div { padding:7px 8px; border:1px solid #fecaca; background:#fef2f2; color:var(--danger); border-radius:6px; margin-top:6px; word-break:break-all; }
    .modalBackdrop { position:fixed; inset:0; z-index:80; background:rgba(15,23,42,.38); display:flex; align-items:center; justify-content:center; padding:18px; }
    .modalBackdrop[hidden] { display:none; }
    .modal { width:min(760px, 100%); max-height:86vh; overflow:hidden; border-radius:8px; background:#fff; border:1px solid #d0d5dd; box-shadow:0 24px 60px rgba(16,24,40,.24); display:flex; flex-direction:column; }
    .modalHeader { display:flex; align-items:center; justify-content:space-between; gap:12px; padding:14px 16px; border-bottom:1px solid #e4e7ec; background:#f8fafc; }
    .modalHeader h3 { margin:0; font-size:15px; }
    .modalClose { background:#fff; color:#344054; border:1px solid #d0d5dd; padding:6px 10px; }
    .modalBody { padding:14px 16px; overflow:auto; }
    .confirmAddressList { display:grid; gap:8px; }
    .confirmAddressRow { display:grid; grid-template-columns:1fr 180px; gap:8px; align-items:center; padding:8px; border:1px solid #e4e7ec; border-radius:6px; background:#fbfcfe; }
    .confirmAddressText { font:12px/1.45 Consolas, "Microsoft YaHei", monospace; word-break:break-all; color:#111827; }
    .modalFooter { display:flex; justify-content:flex-end; gap:8px; padding:12px 16px; border-top:1px solid #e4e7ec; background:#fff; }
    .hint { color:var(--muted); font-size:12px; line-height:1.5; }
    @media (max-width: 980px) { main { grid-template-columns:1fr; } .logPanel { top:auto; right:12px; left:12px; bottom:12px; width:auto; } .addressInputActions { grid-template-columns:1fr; } .confirmAddressRow { grid-template-columns:1fr; } }
  </style>
</head>
<body>
  <form id="credentialForm" autocomplete="on" hidden>
    <input type="text" name="username" autocomplete="username">
  </form>
  <header>
    <h1>钱包原始节点数据导出</h1>
    <span class="hint">本地运行：RPC 原始数据 · Excel 导出 · DeepAML 标签过滤</span>
  </header>
  <main>
    <section>
      <h2>1. 输入地址</h2>
      <div class="field">
        <label>地址输入</label>
        <textarea id="addressInput" placeholder="可以输入一个或多个地址，多个地址可用换行、空格、逗号或分号分隔"></textarea>
      </div>
      <div class="addressInputActions">
        <select id="inputDefaultChain"></select>
        <button id="confirmAddressBtn" type="button">确认地址</button>
        <span class="hint">多个地址会先弹窗确认每个地址的链。</span>
      </div>
      <div class="confirmedHeader">
        <strong>已确认地址</strong>
        <span id="addressCountHint" class="hint">0 个地址</span>
      </div>
      <div id="addressRows"></div>

      <h2 class="subTitle">2. 设置</h2>
      <div class="sourceSwitch" role="group" aria-label="下载模式">
        <button id="rpcModeBtn" type="button" class="active" data-source="rpc">RPC 原始节点</button>
        <button id="csvModeBtn" type="button" data-source="csv">CSV纯下载</button>
      </div>
      <div id="csvFields" class="modePanel" hidden>
        <div class="checks">
          <label class="check"><input id="incrementalMode" type="checkbox">增量模式（从本地 CSV 检查点继续）</label>
        </div>
        <div class="grid">
          <div class="field">
            <label>CSV接收邮箱</label>
            <input id="csvEmail" placeholder="name@example.com">
          </div>
          <div class="field">
            <label>IMAP服务器</label>
            <input id="csvImapHost" placeholder="imap.gmail.com">
          </div>
        </div>
        <div class="field">
          <label>429 / 风控冷却（分钟）</label>
          <input id="riskCooldownMinutes" type="number" min="1" value="30">
          <div class="hint">命中 429 或风控后，停止当前地址并冻结所有 CSV 新请求；冷却结束后需手动继续当前任务。</div>
        </div>
        <div class="grid3 csvCredentials">
          <div class="field">
            <label>IMAP端口</label>
            <input id="csvImapPort" type="number" value="993">
          </div>
          <div class="field">
            <label>IMAP用户名</label>
            <input id="csvImapUser" placeholder="通常同邮箱">
          </div>
          <div class="field">
            <label>IMAP密码/授权码</label>
            <input id="csvImapPassword" type="password" form="credentialForm" autocomplete="current-password">
          </div>
        </div>
        <div class="grid">
          <div class="field">
            <label>CSV开始时间（Unix秒，0不限）</label>
            <input id="csvStartTime" type="number" value="1262304000">
          </div>
          <div class="field">
            <label>CSV结束时间（Unix秒，0当前）</label>
            <input id="csvEndTime" type="number" value="0">
          </div>
        </div>
      </div>
      <div id="rpcFields" class="modePanel">
      <div class="presetBox">默认使用内置公共 RPC，自动扫描最近 10 万个区块（BSC 约 3.5 天）。只需输入地址并选择链；快速模式下载余额和代币/NFT logs。如需全量扫描请在高级设置中将起始区块设为 1；完整普通交易/内部交易也需在高级设置里开启。</div>
      </div>
      <div id="amlSettings">
        <div class="field">
          <label>DeepAML Key</label>
          <input id="amlKey" type="password" form="credentialForm" autocomplete="current-password" placeholder="可为空；也可用环境变量 DEEPAML_API_KEY">
        </div>
        <div class="checks">
          <label class="check"><input id="amlLabels" type="checkbox" checked>添加 DeepAML 标签</label>
          <label class="check"><input id="filterExchange" type="checkbox" checked>过滤交易所大地址</label>
        </div>
      </div>
      <div class="grid3 outputPaths">
        <div class="field">
          <label>输出目录</label>
          <input id="outputDir" value="exports">
        </div>
        <div class="field">
          <label>文件名前缀</label>
          <input id="outputPrefix" value="wallet_export">
        </div>
        <div class="field">
          <label>原始 JSON/CSV 目录</label>
          <input id="rawDir" value="raw">
        </div>
      </div>
      <details id="rpcAdvanced" class="advanced">
        <summary>高级设置</summary>
        <div style="height:12px"></div>
        <div class="field">
          <label>自定义 RPC URL（可选；填写后覆盖所选链的内置 RPC）</label>
          <input id="rpcUrl" placeholder="https://你的节点RPC">
        </div>
        <div class="field">
          <label>多链 RPC 配置文件路径（可选）</label>
          <input id="rpcConfig" placeholder="E:\codex\虚拟币\rpc-config.json">
        </div>
        <div class="grid3">
          <div class="field">
            <label>起始区块</label>
            <input id="startBlock" type="number" value="0">
          </div>
          <div class="field">
            <label>结束区块（-1 最新）</label>
            <input id="endBlock" type="number" value="-1">
          </div>
          <div class="field">
            <label>截止区块（不包含，0 关闭）</label>
            <input id="cutoffBlock" type="number" value="0">
          </div>
        </div>
        <div class="grid3">
          <div class="field">
            <label>内部交易模式</label>
            <select id="traceMode">
              <option value="none" selected>none</option>
              <option value="auto">auto</option>
              <option value="trace-filter">trace-filter</option>
              <option value="debug-all">debug-all</option>
            </select>
          </div>
        </div>
        <div class="grid3">
          <div class="field">
            <label>并发数</label>
            <input id="workers" type="number" value="4">
          </div>
          <div class="field">
            <label>RPC 限速 RPS（0 不限）</label>
            <input id="rps" type="number" value="2" step="0.1">
          </div>
          <div class="field">
            <label>超时秒数</label>
            <input id="timeoutSeconds" type="number" value="30">
          </div>
        </div>
        <div class="grid3">
          <div class="field">
            <label>区块批次</label>
            <input id="blockBatch" type="number" value="100">
          </div>
          <div class="field">
            <label>日志批次</label>
            <input id="logBatch" type="number" value="50">
          </div>
          <div class="field">
            <label>DeepAML RPS</label>
            <input id="amlRps" type="number" value="2" step="0.1">
          </div>
        </div>
        <div class="checks">
          <label class="check"><input id="scanNative" type="checkbox" checked>扫描普通原生交易（大范围会优先快扫主动发出的交易）</label>
        </div>
      </details>
      <div class="actions">
        <button id="startBtn">开始下载</button>
        <button id="resumeBtn" class="secondary" disabled hidden>继续下载</button>
        <button id="cancelBtn" class="secondary" disabled>取消</button>
        <span class="hint">批量地址会逐个生成独立 Excel 文件。</span>
      </div>
    </section>
    <section>
      <h2>进度</h2>
      <div class="overallProgress">
        <div class="statusLine">
          <span id="statusText">未开始</span>
          <span id="countText">队列 0 | 已处理 0 | 剩余 0</span>
        </div>
        <div class="progress"><div id="bar" class="bar"></div></div>
        <div class="queueOverview" aria-live="polite">
          <div class="queueMetric"><span class="queueMetricLabel">全局执行</span><span id="queueActive" class="queueMetricValue">0 个地址</span></div>
          <div class="queueMetric"><span class="queueMetricLabel">全局等待地址</span><span id="queueWaiting" class="queueMetricValue">0 个地址</span></div>
          <div id="cooldownMetric" class="queueMetric"><span class="queueMetricLabel">429 冷却</span><span id="cooldownText" class="queueMetricValue">未触发</span></div>
        </div>
        <button id="queueResumeBtn" type="button" class="queueResume" hidden>继续下载</button>
      </div>
      <div id="addressProgressList" class="addressProgressList"></div>
      <div id="results" class="results"></div>
      <div id="errors" class="errors"></div>
    </section>
  </main>
  <div id="addressConfirmModal" class="modalBackdrop" hidden>
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="addressConfirmTitle">
      <div class="modalHeader">
        <h3 id="addressConfirmTitle">确认地址和链</h3>
        <button id="closeAddressModalBtn" type="button" class="modalClose">关闭</button>
      </div>
      <div class="modalBody">
        <div id="confirmAddressList" class="confirmAddressList"></div>
      </div>
      <div class="modalFooter">
        <button id="cancelAddressModalBtn" type="button" class="secondary">取消</button>
        <button id="applyAddressModalBtn" type="button">加入队列</button>
      </div>
    </div>
  </div>
  <div id="logPanel" class="logPanel collapsed">
    <div class="logHeader">
      <div class="logTitle">
        <span class="logDot"></span>
        <span>运行日志</span>
        <span id="logCount" class="logCount">0 条</span>
      </div>
      <button id="logToggle" type="button" class="logToggle">展开</button>
    </div>
    <div id="log" class="logBody"></div>
  </div>
  <script>
    let currentJob = null;
    let timer = null;
    let sourceMode = 'rpc';
    let saveTimer = null;
    let logCollapsed = true;
    let pendingAddressConfirm = [];
    const DEFAULT_CSV_START_TIME = 1262304000;
    const $ = id => document.getElementById(id);
    const CHAIN_PRESETS = [
      {code:'ETH', name:'Ethereum'},
      {code:'BSC', name:'BNB Smart Chain'},
      {code:'POLYGON', name:'Polygon'},
      {code:'BASE', name:'Base'},
      {code:'ARBITRUM', name:'Arbitrum One'},
      {code:'OP', name:'Optimism'},
      {code:'AVAXC', name:'Avalanche C-Chain'}
    ];
    function num(id) { const v = Number($(id).value); return Number.isFinite(v) ? v : 0; }
    function chainOptions(selected) {
      return CHAIN_PRESETS.map(c => '<option value="' + c.code + '"' + (c.code === selected ? ' selected' : '') + '>' + c.name + ' (' + c.code + ')</option>').join('');
    }
    function addAddressRow(address, chain) {
      const wrap = document.createElement('div');
      wrap.className = 'addressRow';
      wrap.innerHTML = '<input class="addrInput" placeholder="0x..." value="' + escapeAttr(address || '') + '">' +
        '<select class="chainSelect">' + chainOptions(chain || 'ETH') + '</select>' +
        '<button type="button" title="删除">×</button>';
      wrap.querySelector('.addrInput').addEventListener('input', updateQueueCount);
      wrap.querySelector('.chainSelect').addEventListener('change', updateQueueCount);
      wrap.querySelector('button').addEventListener('click', () => {
        wrap.remove();
        if (!document.querySelector('.addressRow')) addAddressRow('', 'ETH');
        updateQueueCount();
      });
      $('addressRows').appendChild(wrap);
      updateQueueCount();
    }
    function replaceAddressRows(rows) {
      $('addressRows').innerHTML = '';
      for (const row of rows) addAddressRow(row.address, row.chain);
      updateQueueCount();
    }
    function collectAddressChains() {
      return Array.from(document.querySelectorAll('.addressRow')).map(row => ({
        address: row.querySelector('.addrInput').value.trim(),
        chain: row.querySelector('.chainSelect').value
      })).filter(x => x.address);
    }
    function parseAddressInputText(text) {
      const seen = new Set();
      return String(text || '').split(/[\s,;，；]+/).map(x => x.trim()).filter(Boolean).filter(addr => {
        const key = addr.toLowerCase();
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      });
    }
    function confirmAddressInput() {
      const chain = $('inputDefaultChain').value || 'ETH';
      const parts = parseAddressInputText($('addressInput').value);
      if (!parts.length) return;
      if (parts.length === 1) {
        addAddressRow(parts[0], chain);
        $('addressInput').value = '';
        return;
      }
      openAddressConfirmModal(parts.map(address => ({ address, chain })));
    }
    function openAddressConfirmModal(rows) {
      pendingAddressConfirm = rows;
      $('confirmAddressList').innerHTML = rows.map((row, index) =>
        '<div class="confirmAddressRow">' +
          '<div class="confirmAddressText">' + escapeHTML(row.address) + '</div>' +
          '<select class="confirmChainSelect" data-index="' + index + '">' + chainOptions(row.chain || 'ETH') + '</select>' +
        '</div>'
      ).join('');
      $('addressConfirmModal').hidden = false;
    }
    function closeAddressConfirmModal(clearInput) {
      $('addressConfirmModal').hidden = true;
      $('confirmAddressList').innerHTML = '';
      pendingAddressConfirm = [];
      if (clearInput) $('addressInput').value = '';
    }
    function applyAddressConfirmModal() {
      const rows = Array.from(document.querySelectorAll('.confirmChainSelect')).map(select => {
        const index = Number(select.dataset.index || 0);
        return {
          address: pendingAddressConfirm[index] ? pendingAddressConfirm[index].address : '',
          chain: select.value || 'ETH'
        };
      }).filter(row => row.address);
      for (const row of rows) addAddressRow(row.address, row.chain);
      closeAddressConfirmModal(true);
    }
    function queueText(done, total) {
      done = Number(done || 0);
      total = Number(total || 0);
      const pending = Math.max(total - done, 0);
      return '本任务：队列 ' + total + ' | 已处理 ' + done + ' | 剩余 ' + pending;
    }
    function updateQueueCount() {
      if (currentJob) return;
      const rows = collectAddressChains();
      $('addressCountHint').textContent = rows.length + ' 个地址';
      $('countText').textContent = queueText(0, rows.length);
      renderQueuedAddresses(rows);
    }
    function renderQueuedAddresses(rows) {
      const items = rows.map((row, index) => ({
        index: index,
        address: row.address,
        chain: row.chain,
        status: 'pending',
        message: '等待下载',
        progress: 0,
        downloaded: 0,
        total: -1,
        parts: []
      }));
      renderAddressProgress(items, false);
    }
    function setSourceMode(mode, persist) {
      sourceMode = mode === 'csv' ? 'csv' : 'rpc';
      $('rpcModeBtn').classList.toggle('active', sourceMode === 'rpc');
      $('csvModeBtn').classList.toggle('active', sourceMode === 'csv');
      $('csvFields').hidden = sourceMode !== 'csv';
      $('rpcFields').hidden = sourceMode !== 'rpc';
      $('rpcAdvanced').hidden = sourceMode !== 'rpc';
      $('amlSettings').hidden = sourceMode === 'csv';
      if (persist !== false) scheduleSaveSettings();
    }
    function settingsPayload() {
      return {
        source: sourceMode,
        csvEmail: $('csvEmail').value,
        csvImapHost: $('csvImapHost').value,
        csvImapPort: num('csvImapPort'),
        csvImapUser: $('csvImapUser').value,
        csvImapPassword: $('csvImapPassword').value,
        csvStartTime: num('csvStartTime') || DEFAULT_CSV_START_TIME,
        csvEndTime: num('csvEndTime'),
        incremental: $('incrementalMode').checked,
        riskCooldownSecs: Math.max(60, num('riskCooldownMinutes') * 60),
        workers: num('workers'),
        rps: Number($('rps').value || 0),
        timeoutSeconds: num('timeoutSeconds'),
        rawDir: $('rawDir').value,
        outputDir: $('outputDir').value,
        outputPrefix: $('outputPrefix').value
      };
    }
    function setInputValue(id, value) {
      if (value === undefined || value === null) return;
      $(id).value = value;
    }
    function applySettings(settings) {
      if (!settings) return;
      setSourceMode(settings.source || 'rpc', false);
      setInputValue('csvEmail', settings.csvEmail);
      setInputValue('csvImapHost', settings.csvImapHost);
      setInputValue('csvImapPort', settings.csvImapPort || 993);
      setInputValue('csvImapUser', settings.csvImapUser);
      setInputValue('csvImapPassword', settings.csvImapPassword);
      setInputValue('csvStartTime', settings.csvStartTime || DEFAULT_CSV_START_TIME);
      setInputValue('csvEndTime', settings.csvEndTime || 0);
      $('incrementalMode').checked = !!settings.incremental;
      setInputValue('riskCooldownMinutes', Math.max(1, Math.round((settings.riskCooldownSecs || 1800) / 60)));
      setInputValue('workers', settings.workers || 4);
      setInputValue('rps', settings.rps === undefined ? 2 : settings.rps);
      setInputValue('timeoutSeconds', settings.timeoutSeconds || 30);
      setInputValue('rawDir', settings.rawDir || 'raw');
      setInputValue('outputDir', settings.outputDir || 'exports');
      setInputValue('outputPrefix', settings.outputPrefix || 'wallet_export');
    }
    async function loadSettings() {
      try {
        const res = await fetch('/api/settings');
        if (!res.ok) throw new Error(await res.text());
        applySettings(await res.json());
      } catch (e) {
        if (num('csvStartTime') <= 0) $('csvStartTime').value = DEFAULT_CSV_START_TIME;
      }
    }
    async function saveSettings() {
      try {
        await fetch('/api/settings', { method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify(settingsPayload()) });
      } catch (e) {
      }
    }
    function scheduleSaveSettings() {
      clearTimeout(saveTimer);
      saveTimer = setTimeout(saveSettings, 400);
    }
    function bindAutoSaveSettings() {
      const ids = ['csvEmail','csvImapHost','csvImapPort','csvImapUser','csvImapPassword','csvStartTime','csvEndTime','incrementalMode','riskCooldownMinutes','workers','rps','timeoutSeconds','rawDir','outputDir','outputPrefix'];
      for (const id of ids) {
        const el = $(id);
        if (!el) continue;
        el.addEventListener('input', scheduleSaveSettings);
        el.addEventListener('change', scheduleSaveSettings);
      }
    }
	function payload() {
	  const rows = collectAddressChains();
	  return {
        source: sourceMode,
        addresses: '',
        addressChains: rows,
        chains: rows.length ? rows[0].chain : 'ETH',
        rpcUrl: $('rpcUrl').value,
        rpcConfig: $('rpcConfig').value,
        nativeSymbol: '',
        startBlock: num('startBlock'),
        endBlock: num('endBlock'),
        cutoffBlock: num('cutoffBlock'),
        traceMode: $('traceMode').value,
        blockBatch: num('blockBatch'),
        logBatch: num('logBatch'),
        workers: num('workers'),
        rps: Number($('rps').value || 0),
        timeoutSeconds: num('timeoutSeconds'),
        rawDir: $('rawDir').value,
        outputDir: $('outputDir').value,
        outputPrefix: $('outputPrefix').value,
        csvEmail: $('csvEmail').value,
        csvImapHost: $('csvImapHost').value,
        csvImapPort: num('csvImapPort'),
        csvImapUser: $('csvImapUser').value,
        csvImapPassword: $('csvImapPassword').value,
        csvStartTime: num('csvStartTime') || DEFAULT_CSV_START_TIME,
        csvEndTime: num('csvEndTime'),
        incremental: sourceMode === 'csv' && $('incrementalMode').checked,
        riskCooldownSecs: Math.max(60, num('riskCooldownMinutes') * 60),
        amlKey: $('amlKey').value,
        amlLabels: sourceMode !== 'csv' && $('amlLabels').checked,
        amlRps: Number($('amlRps').value || 0),
        filterExchange: sourceMode !== 'csv' && $('filterExchange').checked,
        details: false,
        scanNative: $('scanNative').checked,
        retries: 4,
	    pageSize: 50
	  };
	}
	function prepareAddressRowsForStart() {
	  let rows = collectAddressChains();
	  const pending = parseAddressInputText($('addressInput').value);
	  const chain = $('inputDefaultChain').value || 'ETH';
	  if (!rows.length && pending.length === 1) {
	    addAddressRow(pending[0], chain);
	    $('addressInput').value = '';
	    rows = collectAddressChains();
	  } else if (pending.length > 0) {
	    openAddressConfirmModal(pending.map(address => ({ address, chain })));
	    $('statusText').textContent = '请先在弹窗确认每个地址的链';
	    return null;
	  }
	  if (!rows.length) {
	    $('statusText').textContent = '请先输入地址';
	    return null;
	  }
	  return rows;
	}
	async function start() {
	  const rows = prepareAddressRowsForStart();
	  if (!rows) return;
	  $('startBtn').disabled = true;
	  $('resumeBtn').hidden = true;
	  $('resumeBtn').disabled = true;
	  $('cancelBtn').disabled = false;
      $('log').textContent = '';
      $('logCount').textContent = '0 条';
      $('results').innerHTML = '';
      $('errors').innerHTML = '';
      renderQueuedAddresses(rows);
      await saveSettings();
      try {
        const res = await fetch('/api/start', { method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify(payload()) });
        if (!res.ok) throw new Error(await res.text());
        const job = await res.json();
        currentJob = job.id;
        render(job);
        timer = setInterval(poll, 1000);
      } catch (e) {
        $('statusText').textContent = e.message;
        $('startBtn').disabled = false;
        $('cancelBtn').disabled = true;
      }
    }
    async function poll() {
      if (!currentJob) return;
      const res = await fetch('/api/job?id=' + encodeURIComponent(currentJob));
      if (!res.ok) return;
      const job = await res.json();
      render(job);
      if (!job.running) {
        clearInterval(timer);
        const resumable = job.status === 'paused' || job.status === 'cooling';
        $('startBtn').disabled = false;
        $('resumeBtn').hidden = !resumable;
        $('resumeBtn').disabled = !resumable || !!job.needsCredentials || cooldownRemainingSeconds(job) > 0;
        $('cancelBtn').disabled = true;
        if (!resumable) currentJob = null;
      }
    }
    async function resumeJob() {
      if (!currentJob) return;
      $('resumeBtn').disabled = true;
      $('startBtn').disabled = true;
      $('cancelBtn').disabled = false;
      try {
        const res = await fetch('/api/resume?id=' + encodeURIComponent(currentJob), { method:'POST' });
        if (!res.ok) throw new Error(await res.text());
        const job = await res.json();
        render(job);
        clearInterval(timer);
        timer = setInterval(poll, 1000);
      } catch (e) {
        $('statusText').textContent = e.message;
        $('resumeBtn').disabled = false;
        $('startBtn').disabled = false;
        $('cancelBtn').disabled = true;
      }
    }
    async function cancelJob() {
      if (!currentJob) return;
      await fetch('/api/cancel?id=' + encodeURIComponent(currentJob), { method:'POST' });
      await poll();
    }
    async function cancelAddress(index) {
      if (!currentJob) return;
      await fetch('/api/cancel?id=' + encodeURIComponent(currentJob) + '&index=' + encodeURIComponent(index), { method:'POST' });
      await poll();
    }
    function render(job) {
      const resumable = job.status === 'paused' || job.status === 'cooling';
      $('statusText').textContent = translateLogText(job.message || job.status);
      $('countText').textContent = queueText(job.done, job.total);
      $('bar').style.width = clampPercent(job.progress || 0) + '%';
      $('startBtn').disabled = !!job.running;
      $('resumeBtn').hidden = !resumable;
      $('resumeBtn').disabled = !resumable || !!job.needsCredentials || cooldownRemainingSeconds(job) > 0;
      $('resumeBtn').title = job.needsCredentials ? '请先在设置中补充邮箱密码' : (cooldownRemainingSeconds(job) > 0 ? '冷却结束后可继续下载' : '');
      $('cancelBtn').disabled = !job.running;
      renderQueueOverview(job);
      renderAddressProgress(job.addresses || [], !!job.running);
      renderLogs(job.logs || []);
      $('results').innerHTML = (job.results || []).map(x => '<div>' + escapeHTML(x) + '</div>').join('');
      $('errors').innerHTML = (job.errors || []).map(x => '<div>' + escapeHTML(x) + '</div>').join('');
    }
    function renderLogs(logs) {
      $('logCount').textContent = logs.length + ' 条';
      if (!logs.length) {
        $('log').innerHTML = '<div class="logEmpty">暂无日志</div>';
        return;
      }
      $('log').innerHTML = logs.map(renderLogLine).join('');
      $('log').scrollTop = $('log').scrollHeight;
    }
    function renderLogLine(line) {
      const text = String(line || '');
      const match = text.match(/^(\d{2}:\d{2}:\d{2})\s+(.*)$/);
      const time = match ? match[1] : '';
      const message = match ? match[2] : text;
      return '<div class="logLine">' +
        '<span class="logTime">' + escapeHTML(time) + '</span>' +
        '<span class="logText">' + escapeHTML(translateLogText(message)) + '</span>' +
      '</div>';
    }
    function toggleLogPanel() {
      logCollapsed = !logCollapsed;
      $('logPanel').classList.toggle('collapsed', logCollapsed);
      $('logToggle').textContent = logCollapsed ? '展开' : '折叠';
    }
    function renderAddressProgress(items, jobRunning) {
      const list = $('addressProgressList');
      if (!items || !items.length) {
        list.innerHTML = '<div class="emptyProgress">添加地址后，这里会显示每个地址的独立下载进度。</div>';
        return;
      }
      list.innerHTML = items.map(item => renderAddressCard(item, jobRunning)).join('');
    }
    function renderAddressCard(item, jobRunning) {
      const status = item.status || 'pending';
      const progress = clampPercent(item.progress || 0);
      const canCancel = !!jobRunning && !isTerminalStatus(status) && !item.cancelRequested;
      const cancelText = item.cancelRequested && !isTerminalStatus(status) ? '取消中' : '取消';
      const parts = (item.parts || []).map(renderDownloadPart).join('');
      const activity = renderAddressActivity(item);
      const errors = (item.errors || []).length ? '<div class="addrErrors">' + (item.errors || []).map(escapeHTML).join('<br>') + '</div>' : '';
      return '<div class="addrCard ' + escapeAttr(status) + '">' +
        '<div class="addrHeader">' +
          '<div class="addrTitle">' +
            '<div class="addrName">' + escapeHTML(item.address || '') + '</div>' +
            '<div class="addrMeta">' +
              '<span class="badge">' + escapeHTML(item.chain || '-') + '</span>' +
              '<span class="badge ' + escapeAttr(status) + '">' + statusLabel(status) + '</span>' +
            '</div>' +
          '</div>' +
          '<button type="button" class="addrCancel" data-cancel-address="' + Number(item.index || 0) + '"' + (canCancel ? '' : ' disabled') + '>' + cancelText + '</button>' +
        '</div>' +
        '<div class="addrProgressTop">' +
          '<span>' + progress + '%</span>' +
          '<span>' + formatDownloadCount(item.downloaded, item.total) + '</span>' +
        '</div>' +
        '<div class="progress addrBar"><div class="bar" style="width:' + progress + '%"></div></div>' +
        '<div class="addrMessage">' + escapeHTML(translateLogText(item.message || statusLabel(status))) + '</div>' +
        activity +
        (parts ? '<div class="partList">' + parts + '</div>' : '') +
        errors +
      '</div>';
    }
    function renderAddressActivity(item) {
      const started = item.startedAt || '';
      const updated = item.updatedAt || item.finishedAt || '';
      const rowsPerMinute = estimateRowsPerMinute(item.downloaded, started, updated);
      const eta = estimateRemainingText(item.downloaded, item.total, rowsPerMinute);
      return '<div class="addrActivity">' +
        renderActivityItem('最近更新', updated || '-') +
        renderActivityItem('估算速度', rowsPerMinute > 0 ? Math.round(rowsPerMinute).toLocaleString() + ' 行/分钟' : '-') +
        renderActivityItem('预计剩余', eta) +
      '</div>';
    }
    function renderActivityItem(label, value) {
      return '<div class="activityItem"><span class="activityLabel">' + escapeHTML(label) + '</span><span class="activityValue" title="' + escapeAttr(value) + '">' + escapeHTML(value) + '</span></div>';
    }
    function renderDownloadPart(part) {
      const label = (part.chain ? part.chain + ' · ' : '') + (part.kind || '数据');
      const source = formatCSVSourceCounts(part);
      return '<div class="partItem"><span>' + escapeHTML(label) + '</span><span>' + formatDownloadCount(part.downloaded, part.total) + '</span>' + source + '</div>';
    }
    function formatCSVSourceCounts(part) {
      const direct = Number(part.directDownloaded || 0);
      const email = Number(part.emailDownloaded || 0);
      if (!direct && !email) return '';
      return '<span class="partSource">直连 ' + direct.toLocaleString() + ' · 邮箱 ' + email.toLocaleString() + '</span>';
    }
    function statusLabel(status) {
      switch (status) {
      case 'running': return '下载中';
      case 'done': return '完成';
      case 'failed': return '失败';
      case 'paused': return '已暂停';
      case 'queued': return '排队中';
      case 'cooling': return '冷却中';
      case 'cancelled': return '已取消';
      default: return '等待';
      }
    }
    function isTerminalStatus(status) {
      return status === 'done' || status === 'failed' || status === 'cancelled';
    }
    function cooldownRemainingSeconds(job) {
      const until = new Date(job && job.cooldownUntil || '').getTime();
      if (!Number.isFinite(until)) return 0;
      return Math.max(0, Math.ceil((until - Date.now()) / 1000));
    }
    function formatDuration(seconds) {
      seconds = Math.max(0, Math.ceil(Number(seconds) || 0));
      if (!seconds) return '未触发';
      const minutes = Math.floor(seconds / 60);
      const rest = seconds % 60;
      return minutes ? minutes + '分' + String(rest).padStart(2, '0') + '秒' : rest + '秒';
    }
    function renderQueueOverview(job) {
      const active = Number(job.queueActive || 0);
      const waiting = Number(job.queueWaiting || 0);
      const cooling = cooldownRemainingSeconds(job);
      $('queueActive').textContent = active + ' 个地址';
      $('queueWaiting').textContent = waiting + ' 个地址';
      $('cooldownText').textContent = cooling ? '剩余 ' + formatDuration(cooling) : '未触发';
      $('cooldownMetric').classList.toggle('cooling', cooling > 0);
      const resumable = job.status === 'paused' || job.status === 'cooling';
      const button = $('queueResumeBtn');
      button.hidden = !resumable;
      button.disabled = !resumable || !!job.needsCredentials || cooling > 0;
      button.textContent = cooling ? '429 冷却中，剩余 ' + formatDuration(cooling) + ' 后可继续下载' : '继续下载';
    }
    function formatDownloadCount(downloaded, total) {
      downloaded = Number(downloaded || 0);
      total = Number(total);
      if (Number.isFinite(total) && total >= 0) {
        return '已下载 ' + downloaded.toLocaleString() + ' / ' + total.toLocaleString();
      }
      return '已下载 ' + downloaded.toLocaleString() + ' / 待统计';
    }
    function estimateRowsPerMinute(downloaded, startedAt, updatedAt) {
      downloaded = Number(downloaded || 0);
      if (!downloaded || !startedAt || !updatedAt) return 0;
      const start = parseLocalTime(startedAt);
      const updated = parseLocalTime(updatedAt);
      const minutes = (updated - start) / 60000;
      if (!Number.isFinite(minutes) || minutes <= 0) return 0;
      return downloaded / minutes;
    }
    function estimateRemainingText(downloaded, total, rowsPerMinute) {
      downloaded = Number(downloaded || 0);
      total = Number(total);
      if (!Number.isFinite(total) || total <= 0 || !rowsPerMinute) return '待统计';
      const remaining = Math.max(0, total - downloaded);
      if (!remaining) return '0 分钟';
      const minutes = Math.ceil(remaining / rowsPerMinute);
      if (minutes < 60) return minutes + ' 分钟';
      const hours = Math.floor(minutes / 60);
      const rest = minutes % 60;
      return hours + ' 小时 ' + rest + ' 分钟';
    }
    function parseLocalTime(text) {
      const normalized = String(text || '').replace(' ', 'T');
      const value = new Date(normalized).getTime();
      return Number.isFinite(value) ? value : 0;
    }
    function clampPercent(value) {
      value = Number(value || 0);
      if (!Number.isFinite(value) || value < 0) return 0;
      if (value > 100) return 100;
      return Math.round(value);
    }
    function translateLogText(text) {
      text = String(text || '');
      if (!text) return '';
      let out = text;
      out = out.replace(/^错误:\s*/i, '错误：');
      out = out.replace(/^CSV total ([A-Z0-9_]+): ([a-z_]+) failed: /, 'CSV统计 $1 $2 失败：');
      out = out.replace(/^CSV total ([A-Z0-9_]+): ([a-z_]+) = (\d+)/, 'CSV统计 $1 $2 总数量：$3');
      out = out.replace(/^CSV count ([A-Z0-9_]+): ([a-z_]+) source (direct|email) segment (\d+) added (\d+) rows, (direct|email) (\d+) rows, total (.+)/, function(_, chain, kind, source, segment, added, _sourceLabel, sourceTotal, total) {
        const label = source === 'direct' ? '直连' : '邮箱';
        return 'CSV下载 ' + chain + ' ' + kind + ' 第 ' + segment + ' 段' + label + '新增 ' + added + ' 行，' + label + '累计 ' + sourceTotal + ' 行，总计 ' + total;
      });
      out = out.replace(/^CSV count ([A-Z0-9_]+): ([a-z_]+) segment (\d+) added (\d+) rows, total (.+)/, 'CSV下载 $1 $2 第 $3 段新增 $4 行，累计 $5');
      out = out.replace(/^CSV complete ([A-Z0-9_]+): ([a-z_]+) (\d+)\/(\d+)/, 'CSV完成 $1 $2：已下载 $3 / $4');
      out = out.replace(/^CSV direct download ([A-Z0-9_]+): ([a-z_]+) segment (\d+) attempt (\d+)\/(\d+)/, 'CSV直连下载 $1 $2 第 $3 段，第 $4/$5 次尝试');
      out = out.replace(/^CSV direct retry ([A-Z0-9_]+): ([a-z_]+) segment (\d+) attempt (\d+)\/(\d+) after (.+)/, 'CSV直连重试 $1 $2 第 $3 段，第 $4/$5 次，等待 $6');
      out = out.replace(/^CSV direct skipped ([A-Z0-9_]+): ([a-z_]+) segment (\d+): (.+)/, 'CSV直连跳过 $1 $2 第 $3 段：$4');
      out = out.replace(/^CSV direct disabled ([A-Z0-9_]+): ([a-z_]+) after segment (\d+): (.+)/, 'CSV直连已停用 $1 $2，第 $3 段后原因：$4');
      out = out.replace(/^CSV direct download failed ([A-Z0-9_]+): ([a-z_]+) segment (\d+) attempt (\d+)\/(\d+): (.+)/, 'CSV直连下载失败 $1 $2 第 $3 段，第 $4/$5 次：$6');
      out = out.replace(/^CSV direct parse failed ([A-Z0-9_]+): ([a-z_]+) segment (\d+) attempt (\d+)\/(\d+): (.+)/, 'CSV直连解析失败 $1 $2 第 $3 段，第 $4/$5 次：$6');
      out = out.replace(/^CSV direct no file ([A-Z0-9_]+): ([a-z_]+) segment (\d+) attempt (\d+)\/(\d+)/, 'CSV直连暂无文件 $1 $2 第 $3 段，第 $4/$5 次');
      out = out.replace(/^CSV direct address mismatch ([A-Z0-9_]+): ([a-z_]+) segment (\d+) attempt (\d+)\/(\d+)/, 'CSV直连地址不匹配 $1 $2 第 $3 段，第 $4/$5 次');
      out = out.replace(/^CSV stale link ([A-Z0-9_]+): ([a-z_]+) segment (\d+) download failed, requesting a new CSV: (.+)/, 'CSV旧链接下载失败 $1 $2 第 $3 段，正在重新请求：$4');
      out = out.replace(/^CSV stale link ([A-Z0-9_]+): ([a-z_]+) segment (\d+) returned NoSuchKey, requesting a new CSV/, 'CSV旧链接文件未就绪 $1 $2 第 $3 段，正在重新请求');
      out = out.replace(/^CSV token cooldown ([A-Z0-9_]+): ([a-z_]+) segment (\d+) wait (.+)/, 'CSV代币下载冷却 $1 $2 第 $3 段，等待 $4');
      out = out.replace(/^CSV token window ([A-Z0-9_]+): ([a-z_]+) segment (\d+) (.+) - (.+)/, 'CSV代币时间窗口 $1 $2 第 $3 段：$4 至 $5');
      out = out.replace(/^CSV纯下载 ([A-Z0-9_]+): ([a-z_]+)$/, 'CSV纯下载 $1：$2');
      out = out.replace(/transactions/g, '普通交易');
      out = out.replace(/token_transfers/g, '代币转账');
      out = out.replace(/normalTransaction/g, '普通交易');
      out = out.replace(/tokenTransfer/g, '代币转账');
      out = out.replace(/segment/g, '分段');
      out = out.replace(/attempt/g, '尝试');
      out = out.replace(/downloaded rows exceed count total; count may have changed during export/g, '已下载行数超过统计总数，导出期间数据可能发生变化');
      out = out.replace(/downloaded (\d+)\/(\d+) rows/g, '已下载 $1/$2 行');
      out = out.replace(/within tolerance (\d+)/g, '在允许误差 $1 内');
      out = out.replace(/address mismatch/g, '地址不匹配');
      out = out.replace(/direct download failed/g, '直连下载失败');
      out = out.replace(/incorrect request sign parameters/g, '请求签名参数错误');
      out = out.replace(/\{"msg":"请求签名参数错误","code":50113\}/g, 'OKLink 直连签名已失效');
      out = out.replace(/^CSV直连下载失败 ([^：]+)：HTTP 400: OKLink 直连签名已失效$/, 'CSV数量允许直连，但OKLink直连签名已失效，已改用邮件CSV：$1');
      out = out.replace(/^CSV直连已停用 ([^，]+)，第 (\d+) 段后原因：HTTP 400: OKLink 直连签名已失效$/, 'CSV数量允许直连，但OKLink直连签名已失效，已改用邮件CSV：$1 第 $2 段');
      out = out.replace(/no file/g, '暂无文件');
      out = out.replace(/timeout waiting for csv download email/g, '等待 CSV 下载邮件超时');
      out = out.replace(/context canceled/g, '任务已取消');
      out = out.replace(/context deadline exceeded/g, '请求超时');
      return out;
    }
    function escapeHTML(s) {
      return String(s).replace(/[&<>"']/g, ch => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#039;'}[ch]));
    }
    function escapeAttr(s) {
      return escapeHTML(s).replace(/"/g, '&quot;');
    }
    function initChains() {
      $('inputDefaultChain').innerHTML = chainOptions('ETH');
      updateQueueCount();
    }
    $('startBtn').addEventListener('click', start);
    $('resumeBtn').addEventListener('click', resumeJob);
    $('queueResumeBtn').addEventListener('click', resumeJob);
    $('cancelBtn').addEventListener('click', cancelJob);
    $('logToggle').addEventListener('click', toggleLogPanel);
    $('rpcModeBtn').addEventListener('click', () => setSourceMode('rpc'));
    $('csvModeBtn').addEventListener('click', () => setSourceMode('csv'));
    $('confirmAddressBtn').addEventListener('click', confirmAddressInput);
    $('addressInput').addEventListener('keydown', e => {
      if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        confirmAddressInput();
      }
    });
    $('addressInput').addEventListener('paste', () => {
      setTimeout(() => {
        if (!$('addressConfirmModal').hidden) return;
        const parts = parseAddressInputText($('addressInput').value);
        if (parts.length > 1) {
          openAddressConfirmModal(parts.map(address => ({ address, chain: $('inputDefaultChain').value || 'ETH' })));
        }
      }, 80);
    });
    $('closeAddressModalBtn').addEventListener('click', () => closeAddressConfirmModal(false));
    $('cancelAddressModalBtn').addEventListener('click', () => closeAddressConfirmModal(false));
    $('applyAddressModalBtn').addEventListener('click', applyAddressConfirmModal);
    $('addressConfirmModal').addEventListener('click', e => {
      if (e.target === $('addressConfirmModal')) closeAddressConfirmModal(false);
    });
    $('addressProgressList').addEventListener('click', e => {
      const btn = e.target.closest('[data-cancel-address]');
      if (!btn || btn.disabled) return;
      cancelAddress(Number(btn.dataset.cancelAddress));
    });
    initChains();
    renderLogs([]);
    setSourceMode('rpc', false);
    bindAutoSaveSettings();
  </script>
  <script src="/gui-rebind.js"></script>
  <script src="/gui-history.js"></script>
</body>
</html>`
