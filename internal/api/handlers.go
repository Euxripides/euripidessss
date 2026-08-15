package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/xuri/excelize/v2"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/cloudruntime"
	"github.com/etl/backend/internal/config"
	"github.com/etl/backend/internal/cryptodownload"
	"github.com/etl/backend/internal/datasetevents"
	"github.com/etl/backend/internal/datasetsync"
	"github.com/etl/backend/internal/datasourcemanager"
	"github.com/etl/backend/internal/dbimport"
	"github.com/etl/backend/internal/downloadscheduler"
	"github.com/etl/backend/internal/dynamicinvestigation"
	"github.com/etl/backend/internal/entityintel"
	"github.com/etl/backend/internal/etl"
	"github.com/etl/backend/internal/flow"
	"github.com/etl/backend/internal/fundflow"
	"github.com/etl/backend/internal/graphcache"
	"github.com/etl/backend/internal/graphincrement"
	"github.com/etl/backend/internal/intelligence"
	invcache "github.com/etl/backend/internal/investigation/cache"
	"github.com/etl/backend/internal/investigation/prefetch"
	"github.com/etl/backend/internal/investigationstore"
	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parquetdownload"
	"github.com/etl/backend/internal/parser"
	"github.com/etl/backend/internal/reportengine"
	"github.com/etl/backend/internal/rpcmanager"
	"github.com/etl/backend/internal/rules"
	"github.com/etl/backend/internal/s3store"
	"github.com/etl/backend/internal/scanner"
	"github.com/etl/backend/internal/smartdownload"
	"github.com/etl/backend/internal/smartdownload/cloudplanner"
	"github.com/etl/backend/internal/storage"
	"github.com/etl/backend/internal/storage/control"
)

var (
	cfg                     *config.Config
	store                   *storage.FileStorage
	dbStore                 *dbimport.Store
	dbService               *dbimport.Service
	dbExportManager         *dbimport.ExportManager
	controlStore            *control.Store
	analysisEngine          *duckdb.Engine
	cryptoDownload          http.Handler
	parquetDownload         *parquetdownload.Handler
	rpcManager              *rpcmanager.Manager
	rpcAPI                  http.Handler
	dataSourceManager       *datasourcemanager.Manager
	dataSourceAPI           http.Handler
	analyticsAPI            http.Handler
	dynamicInvestigationAPI http.Handler
	dynamicEngine           *dynamicinvestigation.Engine
	intelligenceAPI         http.Handler
	investigationV2API      http.Handler                     // V2 调查请求入口（/api/investigation/*）
	schedulerAPI            http.Handler                     // Smart Download Orchestrator（/api/scheduler/*）
	investigationAgent      *intelligence.InvestigationAgent // Phase 5：数据索引后自动继续调查
	datasetEventBus         *datasetevents.Bus               // Phase 5.3：Dataset Event Bus
	graphIncrementer        *graphincrement.Incrementer      // Phase 5.3：Graph 增量物化
	smartDownloadAPI        http.Handler                     // 智能下载统一入口（/api/smart-download/*）
	smartDownloadService    *smartdownload.Service           // 智能下载服务（预取管理器桥接）
	graphCache              *graphcache.Cache                // Graph Expansion Cache V1
	investigationCacheStore *invcache.Store                  // Investigation Cache V2
	prefetchManager         *prefetch.Manager                // Smart Prefetch Planner V1
	entityResolver          *entityintel.Resolver            // Entity Intelligence Layer V1
	fundFlowEngine          *fundflow.Engine                 // Fund Flow Intelligence V2
	reportEngine            *reportengine.Engine             // Investigation Report Engine V2
	downloadScheduler       *downloadscheduler.Scheduler     // Legacy 调度器引用（Bridge 用）
	smartCloudRuntime       *cloudruntime.Manager            // SQD Cloud 运行时（Phase 2 Adapter 复用）
	downloadDSRegistry      *datasetsync.Registry            // Cloud Dataset Registry（Smart Download 区间复用）
)

const (
	defaultFlowEdgeLimit = 600
	auditFlowEdgeLimit   = 5000
)

type flowColumnMapping struct {
	SourceCol     string
	SourceAccount string
	SourceName    string
	SourceID      string
	SourceLabel   string
	TargetCol     string
	TargetCard    string
	TargetName    string
	TargetID      string
	TargetLabel   string
	Amount        string
	Time          string
	Direction     string
	Serial        string
	Summary       string
	Remark        string
}

type EdgeDetailPayload struct {
	SessionID       string `json:"session_id"`
	SourceColumn    string `json:"source_column"`
	TargetColumn    string `json:"target_column"`
	AmountColumn    string `json:"amount_column"`
	TimeColumn      string `json:"time_column"`
	DirectionColumn string `json:"direction_column"`
	Source          string `json:"source"`
	Target          string `json:"target"`
	Limit           int    `json:"limit"`

	SourceAccountColumn string        `json:"source_account_column"`
	SourceNameColumn    string        `json:"source_name_column"`
	SourceIDColumn      string        `json:"source_id_column"`
	SourceLabelColumn   string        `json:"source_label_column"`
	TargetCardColumn    string        `json:"target_card_column"`
	TargetNameColumn    string        `json:"target_name_column"`
	TargetIDColumn      string        `json:"target_id_column"`
	TargetLabelColumn   string        `json:"target_label_column"`
	SerialColumn        string        `json:"serial_column"`
	SummaryColumn       string        `json:"summary_column"`
	RemarkColumn        string        `json:"remark_column"`
	SourceFilters       []interface{} `json:"source_filters"`
	TargetFilters       []interface{} `json:"target_filters"`
	DetailFilters       []interface{} `json:"detail_filters"`
	SourceLabelValues   []interface{} `json:"source_label_values"`
	TargetLabelValues   []interface{} `json:"target_label_values"`
	Directions          []interface{} `json:"directions"`
	StartDate           string        `json:"start_date"`
	EndDate             string        `json:"end_date"`
}

func flowColumnMappingFromPayload(payload map[string]interface{}) flowColumnMapping {
	stringValue := func(key string) string {
		value, _ := payload[key].(string)
		return value
	}
	return flowColumnMapping{
		SourceCol:     stringValue("source_column"),
		SourceAccount: stringValue("source_account_column"),
		SourceName:    stringValue("source_name_column"),
		SourceID:      stringValue("source_id_column"),
		SourceLabel:   stringValue("source_label_column"),
		TargetCol:     stringValue("target_column"),
		TargetCard:    stringValue("target_card_column"),
		TargetName:    stringValue("target_name_column"),
		TargetID:      stringValue("target_id_column"),
		TargetLabel:   stringValue("target_label_column"),
		Amount:        stringValue("amount_column"),
		Time:          stringValue("time_column"),
		Direction:     stringValue("direction_column"),
		Serial:        stringValue("serial_column"),
		Summary:       stringValue("summary_column"),
		Remark:        stringValue("remark_column"),
	}
}

// Setup initializes the API package with config
func Setup(c *config.Config) {
	cfg = c
	store = storage.NewFileStorage(c.UploadDir, c.OutputDir)
	dbStore = dbimport.NewStore(filepath.Join(c.RootDir, "backend", "data", "db_import"))
	dbService = dbimport.NewService(dbStore, c.UploadDir)
	dbExportManager = dbimport.NewExportManager(dbService)
	if handler, err := cryptodownload.NewAPIHandler(filepath.Join(c.RootDir, "backend", "data", "crypto_download")); err != nil {
		log.Warn().Err(err).Msg("crypto_download_api_unavailable")
	} else {
		cryptoDownload = http.StripPrefix("/api/crypto/download", handler)
	}

	// Initialize SQLite control store
	dataDir := filepath.Join(c.RootDir, "backend", "data")
	cs, err := control.Open(dataDir)
	if err != nil {
		log.Warn().Err(err).Msg("control_store_open_failed")
	} else {
		controlStore = cs
		log.Info().Str("path", cs.Path()).Msg("control_store_opened")
	}

	// Initialize DuckDB analysis engine
	analysisEngine = duckdb.Open(c.RootDir, dataDir, duckdb.AnalyticsConfig{
		DuckDBPath:     c.Analytics.DuckDBPath,
		DuckDBDatabase: c.Analytics.DuckDBDatabase,
	})
	if analysisEngine.Available() {
		st := analysisEngine.Status()
		log.Info().Str("version", st.Version).Str("db", st.Database).
			Bool("reader_enabled", c.Analytics.DuckDBReaderEnabled).Msg("duckdb_engine_ready")
	} else {
		log.Warn().Str("error", analysisEngine.Status().Error).Msg("duckdb_engine_unavailable")
	}
	setupClickHouse(c)
	if handler, err := parquetdownload.NewHandler(c.RootDir, analysisEngine); err != nil {
		log.Warn().Err(err).Msg("crypto_parquet_api_unavailable")
	} else {
		parquetDownload = handler
	}
	// V2.1 RC2: 分析服务 API（基于 sqd-200k-warehouse Parquet 数据资产）
	if c.Analytics.DuckDBReaderEnabled && analysisEngine.Available() {
		warehouseParquet := `E:\codex\etl\stress-data\bsc_real\sqd-200k-warehouse\logs.parquet`
		if _, err := os.Stat(warehouseParquet); err == nil {
			analyticsAPI = analyticsapi.NewHandler(analysisEngine, warehouseParquet)
			log.Info().Msg("analytics_api_ready")
		} else {
			log.Warn().Err(err).Msg("analytics_api_unavailable_warehouse")
		}
	} else if !c.Analytics.DuckDBReaderEnabled {
		log.Info().Str("datasource", c.Analytics.DataSource).Msg("duckdb_parquet_reader_disabled")
	}
	if manager, err := rpcmanager.New(`E:\codex\bsc_analytics`); err != nil {
		log.Warn().Err(err).Msg("crypto_rpc_api_unavailable")
	} else {
		rpcManager = manager
		rpcAPI = http.StripPrefix("/api/crypto", rpcmanager.NewHandler(manager))
		if parquetDownload != nil {
			parquetDownload.SetRPCManager(manager)
		}
	}
	if rpcManager != nil {
		if manager, err := datasourcemanager.New(`E:\codex\bsc_analytics`, rpcManager); err != nil {
			log.Warn().Err(err).Msg("crypto_datasource_api_unavailable")
		} else {
			dataSourceManager = manager
			dataSourceAPI = http.StripPrefix("/api/crypto/datasource", datasourcemanager.NewHandler(manager))
			if parquetDownload != nil {
				parquetDownload.SetDataSourceManager(manager)
			}
		}
	}
	// V2.0 实时资产服务（Provider Router 复用 rpcManager，设计 §13/§15）
	if rpcManager != nil {
		flowAssetsService = flow.NewAssetService(rpcManager)
		// V2.0 余额快照存储（设计 §8/§31：backend/data/investigation/balance-snapshots/）
		snapDir := filepath.Join(cfg.RootDir, "backend", "data", "investigation", "balance-snapshots")
		if err := flow.EnsureSnapshotDir(cfg.RootDir); err == nil {
			balanceSnapshotStore = flow.NewBalanceSnapshotStore(snapDir)
		}
		log.Info().Str("snapshots", snapDir).Msg("flow_assets_api_ready")
	} else {
		log.Warn().Msg("flow_assets_api_unavailable_no_rpc")
	}
	// V2.1 RC2: 动态地址扩展与智能采集路由引擎
	setupDynamicInvestigation()
	// V2.1 RC2: 全自动链上调查平台 Intelligence Layer
	setupIntelligence()
	// V2.2: Smart Download Orchestrator 智能下载调度（决策引擎 + Provider 评分 + 计划状态机）
	setupDownloadScheduler()
	// V1.1 Phase 1: 智能下载统一入口（四层任务模型 + Checkpoint V3 + Range Ledger + Recovery）
	setupSmartDownload()
	setupEntityIntel()
	setupFundFlow()
	setupReportEngine()
}

// setupDynamicInvestigation 装配动态调查引擎：
// 数据源复用 analyticsapi.Service，执行器复用 parquetdownload.Manager + SQD 客户端。
func setupDynamicInvestigation() {
	var source dynamicinvestigation.DiscoverySource
	if cfg.Analytics.DataSource != "duckdb" && clickHouseInvestigation != nil {
		source = &clickHouseDiscoverySource{repository: clickHouseInvestigation}
	} else if h, ok := analyticsAPI.(*analyticsapi.Handler); ok && h != nil {
		source = dynamicinvestigation.NewAnalyticsSource(h.Service())
	} else {
		// 分析服务不可用时仍提供队列/评分 API（空源）
		source = dynamicinvestigation.NewAnalyticsSource(nil)
	}

	var executor dynamicinvestigation.AcquisitionExecutor
	if parquetDownload != nil {
		manager := parquetDownload.Manager()
		network, err := chain.Resolve("bsc")
		if err != nil {
			network = chain.EVM{Key: "bsc", ID: 56, Name: "BNB Smart Chain", NativeSymbol: "BNB", SQDDataset: "binance-mainnet"}
		}
		executor = dynamicinvestigation.NewRealExecutor(manager, network)
	}

	queueDir := filepath.Join(cfg.RootDir, "backend", "data", "dynamic_investigation")
	queue := dynamicinvestigation.NewQueue(queueDir)
	recognizer := dynamicinvestigation.NewRecognizer()
	engine := dynamicinvestigation.NewEngine(queue, recognizer, source, executor, dynamicinvestigation.DefaultConfig())
	dynamicEngine = engine
	dynamicInvestigationAPI = dynamicinvestigation.NewHandler(engine)
	log.Info().Str("queue", queueDir).Msg("dynamic_investigation_engine_ready")
}

// setupIntelligence 装配全自动链上调查平台（Intelligence Layer）：
// 数据源复用 analyticsapi.Service 与 dynamicinvestigation 扩展引擎。
func setupIntelligence() {
	var svc *analyticsapi.Service
	if h, ok := analyticsAPI.(*analyticsapi.Handler); ok && h != nil {
		svc = h.Service()
	}
	var expansion *intelligence.ExpansionEngine
	if dynamicEngine != nil {
		expansion = intelligence.NewExpansionEngine(dynamicEngine)
	}
	dataRoot := filepath.Join(cfg.RootDir, "backend", "data")
	// ── V1 Storage Layer：一次性迁移旧数据目录（幂等，不删旧文件）──
	invRoot := filepath.Join(dataRoot, "investigation")
	if migrated, err := intelligence.MigrateLegacyInvestigationData(dataRoot); err != nil {
		log.Warn().Err(err).Msg("investigation_legacy_migrate_error")
	} else if migrated > 0 {
		log.Info().Int("migrated", migrated).Msg("investigation_legacy_migrated")
	}
	// ── V1 Storage Layer：新布局存储（requests/plans/tasks/evidence/memory/score-profile/indexes）──
	requests := intelligence.NewRequestStore(filepath.Join(invRoot, "requests"))
	evidenceStore := intelligence.NewEvidenceStore(filepath.Join(invRoot, "evidence"))
	knowledgeStore := intelligence.NewInvestigationMemoryStore(filepath.Join(invRoot, "memory"))
	planStore := investigationstore.NewPlanStore(filepath.Join(invRoot, "plans"))
	taskStore := investigationstore.NewTaskStore(filepath.Join(invRoot, "tasks"))
	profileStore := investigationstore.NewScoreProfileStore(filepath.Join(invRoot, "score-profile", "profiles.json"))
	// ── V1 Lifecycle：启动时归档超出上限的请求（active ≤ 5 / history ≤ 200）──
	if archived, err := requests.Archive(5, 200); err != nil {
		log.Warn().Err(err).Msg("investigation_request_archive_error")
	} else if archived > 0 {
		log.Info().Int("archived", archived).Msg("investigation_requests_archived")
	}
	// ── V2 调查请求存储（先于 agent 创建，供调查终态同步请求状态）──
	memoryDir := filepath.Join(dataRoot, "investigation_memory") // 调查状态记忆（旧目录保留）
	// ── Runtime V2（设计 §13）：运行时事件日志（backend/data/logs/runtime-events.log）──
	eventLog := intelligence.NewRuntimeEventLog(filepath.Join(dataRoot, "logs", "runtime-events.log"))
	agent := intelligence.NewAgent(intelligence.AgentOptions{
		Service: svc,
		FlowSource: func() intelligence.FlowSource {
			if cfg.Analytics.DataSource != "duckdb" && clickHouseInvestigation != nil {
				return &clickHouseIntelligenceSource{repository: clickHouseInvestigation}
			}
			return nil
		}(),
		Expansion:       expansion,
		DeepSeekKey:     "", // 回退环境变量 DEEPSEEK_API_KEY
		MemoryDir:       memoryDir,
		RequestStore:    requests,
		EvidenceStore:   evidenceStore,
		KnowledgeMemory: knowledgeStore,
		Plans:           planStore,
		Tasks:           taskStore,
		Profile:         profileStore,
		EventLog:        eventLog, // Runtime V2：运行时事件日志（backend/data/logs/）
		Config:          intelligence.DefaultConfig(),
	})
	investigationAgent = agent
	intelligenceAPI = intelligence.NewHandler(agent)
	// ── V2 调查请求入口（设计 §14）：请求持久化 + 意图分析 + 启动调查 ──
	intentAnalyzer := intelligence.NewIntentAnalyzer()
	investigationV2API = intelligence.NewInvestigationHandler(agent, requests, intentAnalyzer)
	log.Info().Str("root", invRoot).Msg("intelligence_engine_ready")
}

// setupDownloadScheduler 装配 Smart Download Orchestrator（V2.2 智能下载调度）：
// RPC Provider 复用 rpcmanager；SQD Provider 复用 parquetdownload.Manager；
// 覆盖检查复用 analyticsapi.Service；计划持久化于 backend/data/download_scheduler/plans。
func setupDownloadScheduler() {
	var rpcClient downloadscheduler.RPCClient
	if rpcManager != nil {
		rpcClient = rpcManager
	}
	var sqdEngine downloadscheduler.SQDEngine
	if parquetDownload != nil {
		sqdEngine = parquetDownload.Manager()
	}
	// V1.0 恢复层：RPC 恢复数据落盘/合并器（parquetdownload.Manager 实现 RecoveryWriter，
	// 复用 DuckDB 引擎与 DataRoot；parquetDownload 不可用时为 nil，恢复通道自动不可用）
	var recoveryWriter downloadscheduler.RecoveryWriter
	if parquetDownload != nil {
		recoveryWriter = parquetDownload.Manager()
	}
	var coverageSource downloadscheduler.CoverageSource
	if h, ok := analyticsAPI.(*analyticsapi.Handler); ok && h != nil {
		coverageSource = &analyticsCoverageSource{svc: h.Service()}
	}
	var sqdProvider *downloadscheduler.SQDProvider
	if parquetDownload != nil {
		sqdProvider = downloadscheduler.NewSQDProvider(sqdEngine).WithHealth(&sqdHealthAdapter{parquetDownload.Manager()})
	} else {
		// 降级模式：parquetDownload 不可用时不构造 HealthSource（避免 nil 解引用），SQD/AWS 评分自动不可用
		sqdProvider = downloadscheduler.NewSQDProvider(nil)
	}
	// Phase 4：R2/S3 Job Queue + Local Sync 数据面（无凭据时回退本地文件存储用于开发/测试）
	dataRoot := `E:\codex\bsc_analytics`
	cloudRoot := filepath.Join(dataRoot, "sqd-cloud")
	store, storeErr := s3store.NewFromEnv("")
	cloudMode := strings.EqualFold(strings.TrimSpace(os.Getenv("SQD_CLOUD_MODE")), "cloud")
	if storeErr != nil {
		store = s3store.NewLocalStore(filepath.Join(cloudRoot, "store"))
	}
	cloudRuntime := setupCloudRuntime(cfg, store, cloudMode && storeErr != nil, storeErr == nil)
	smartCloudRuntime = cloudRuntime
	cloudUsage := downloadscheduler.NewCloudUsageStore(filepath.Join(cloudRoot, "cloud_usage.json"))
	dsRegistry, _ := datasetsync.NewRegistry(filepath.Join(cloudRoot, "registry.json"))
	var dsValidator datasetsync.ParquetValidator
	if analysisEngine != nil {
		dsValidator = datasetsync.NewDuckDBValidator(analysisEngine)
	}
	dsSyncer := datasetsync.NewSyncer(store, dsRegistry, filepath.Join(cloudRoot, "sync"), dsValidator)
	// Phase 5.3：Dataset Event Bus + Graph 增量物化
	if bus, err := datasetevents.NewBus(filepath.Join(cloudRoot, "dataset_events.json")); err == nil {
		datasetEventBus = bus
	}
	if analysisEngine != nil {
		if inc, err := graphincrement.NewIncrementer(analysisEngine, filepath.Join(cloudRoot, "graph_state.json")); err == nil {
			graphIncrementer = inc
		}
	}
	// Phase 5 §32-33：Graph 数据源接入 Cloud merged parquet（存在后自动生效）
	if h, ok := analyticsAPI.(*analyticsapi.Handler); ok && h != nil {
		h.Service().AddFlowSource(filepath.Join(cloudRoot, "sync", "warehouse", "sqd_cloud", "token_transfers", "chain=bsc", "merged.parquet"))
	}
	registry := downloadscheduler.NewRegistry(
		downloadscheduler.NewRPCProvider(rpcClient).WithRecovery(recoveryWriter),
		downloadscheduler.NewAWSProvider(sqdEngine),
		sqdProvider,
		downloadscheduler.NewBrowserProvider(),
		downloadscheduler.NewCloudProvider(cloudRuntime),
	)
	planDir := filepath.Join(cfg.RootDir, "backend", "data", "download_scheduler", "plans")
	// 覆盖检查 = 分析快照 + Cloud Dataset Registry（Phase 4 §31）
	coverageSource = &compositeCoverageSource{analytics: coverageSource, registry: dsRegistry}
	downloadDSRegistry = dsRegistry
	scheduler := downloadscheduler.NewScheduler(registry, downloadscheduler.NewCoverageResolver(coverageSource), planDir, downloadscheduler.DefaultBudget())
	downloadScheduler = scheduler
	scheduler.WithRecoveryWriter(recoveryWriter)
	scheduler.WithCloudFallback(cloudRuntime, cloudUsage, downloadscheduler.FaultInjectionFromEnv())
	scheduler.WithDataPlane(dsSyncer, dsRegistry)
	// Phase 5.3 §5/§6/§7：DATASET_INDEXED 事件 → Investigation Resume / Graph Increment（幂等）
	if datasetEventBus != nil {
		datasetEventBus.Subscribe("investigation", func(ctx context.Context, ev datasetevents.Event) error {
			if ev.Type != datasetevents.DatasetIndexed || investigationAgent == nil {
				return nil
			}
			resumed := 0
			for _, addr := range ev.Addresses {
				resumed += investigationAgent.NotifyDataReady(addr)
			}
			if resumed > 0 {
				_ = datasetEventBus.Publish(ctx, datasetevents.Event{
					Type:          datasetevents.InvestigationResumed,
					RequirementID: ev.RequirementID,
					Meta:          map[string]any{"resumed": resumed},
				})
			}
			return nil
		})
		if graphIncrementer != nil {
			datasetEventBus.Subscribe("graph", func(ctx context.Context, ev datasetevents.Event) error {
				if ev.Type != datasetevents.DatasetIndexed {
					return nil
				}
				for _, id := range ev.RegistryEntryIDs {
					e := dsRegistry.Get(id)
					if e == nil {
						continue
					}
					if _, err := graphIncrementer.Apply(ctx, e); err != nil {
						log.Warn().Err(err).Str("chunk", id).Msg("graph_increment_failed")
						return err
					}
				}
				_ = datasetEventBus.Publish(ctx, datasetevents.Event{
					Type:          datasetevents.GraphIncrementApplied,
					RequirementID: ev.RequirementID,
					Meta:          map[string]any{"chunks": ev.RegistryEntryIDs},
				})
				return nil
			})
		}
	}
	// Phase 5 §30/§32：索引完成 → 发布 DATASET_INDEXED（清分析缓存由事件消费者/幂等重放保证）
	scheduler.WithDataIndexedHook(func(entries []*datasetsync.Entry) {
		if h, ok := analyticsAPI.(*analyticsapi.Handler); ok && h != nil {
			h.Service().InvalidateCache()
		}
		if datasetEventBus == nil {
			return
		}
		for _, e := range entries {
			ev := datasetevents.Event{
				ID:               datasetevents.IndexedEventID(e.ChunkKey),
				Type:             datasetevents.DatasetIndexed,
				ChainKey:         e.ChainKey,
				Dataset:          e.Dataset,
				Addresses:        e.Addresses,
				FromBlock:        e.FromBlock,
				ToBlock:          e.ToBlock,
				RegistryEntryIDs: []string{e.ChunkKey},
				RowCount:         e.RowCount,
				CoverageStatus:   "HIT",
				Provider:         e.Provider,
			}
			_ = datasetEventBus.Publish(context.Background(), ev)
		}
	})
	schedulerAPI = http.StripPrefix("/api/scheduler", NewSchedulerHandler(scheduler))
	// Phase 4 §60-61：cloud 模式启动后对账已部署 Worker（sqd list --org）
	if cloudRuntime.Status().Mode == "cloud" {
		go func() {
			time.Sleep(2 * time.Second)
			cloudRuntime.Reconcile(context.Background())
		}()
	}
	// Phase 5.3 §15：重启恢复——已 ACTIVE 条目补发确定性 DATASET_INDEXED + Replay 幂等重放
	if datasetEventBus != nil {
		go func() {
			time.Sleep(3 * time.Second)
			for _, e := range dsRegistry.Active() {
				ev := datasetevents.Event{
					ID:               datasetevents.IndexedEventID(e.ChunkKey),
					Type:             datasetevents.DatasetIndexed,
					ChainKey:         e.ChainKey,
					Dataset:          e.Dataset,
					Addresses:        e.Addresses,
					FromBlock:        e.FromBlock,
					ToBlock:          e.ToBlock,
					RegistryEntryIDs: []string{e.ChunkKey},
					RowCount:         e.RowCount,
					CoverageStatus:   "HIT",
					Provider:         e.Provider,
				}
				_ = datasetEventBus.Publish(context.Background(), ev)
			}
			datasetEventBus.Replay(context.Background())
		}()
	}
	// Phase 5 §23：后台自动同步轮询（事件触发 + polling 双保险）
	scheduler.StartAutoSync(context.Background(), 60*time.Second)
	log.Info().Str("plans", planDir).Msg("download_scheduler_ready")
}

// HandleSmartDownloadAPI 是 /api/smart-download/* 的 Gin 转发入口。
func HandleSmartDownloadAPI(c *gin.Context) {
	if smartDownloadAPI == nil {
		c.JSON(503, map[string]any{"detail": "智能下载统一入口服务不可用"})
		return
	}
	smartDownloadAPI.ServeHTTP(c.Writer, c.Request)
}

// setupSmartDownload 装配智能下载统一入口（实施方案 V1.1 Phase 1）：
// 四层任务模型（Batch/Address/Dataset/Range）+ FS StateStore + Checkpoint V3 +
// Range Ledger + Recovery + Pause/Resume/Cancel。
// Phase 1 生产 Adapter：仅 RPC 余额（复用 rpcmanager）；SQD/AWS/Browser/Cloud 由 Phase 2 接入。
func setupSmartDownload() {
	root := filepath.Join(cfg.RootDir, "backend", "data", "smart_download")
	store, err := smartdownload.NewStore(root)
	if err != nil {
		log.Warn().Err(err).Str("root", root).Msg("smart_download_store_unavailable")
		return
	}
	opts := smartdownload.DefaultOptions()
	opts.AdaptiveRanges = true // Discovery 驱动自适应 Range（可用环境变量关闭）
	if v := strings.TrimSpace(os.Getenv("SMART_DOWNLOAD_ADAPTIVE_RANGES")); v != "" {
		opts.AdaptiveRanges = v != "0" && !strings.EqualFold(v, "false")
	}
	if v := strings.TrimSpace(os.Getenv("SMART_DOWNLOAD_WORKERS")); v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n > 0 && n <= 64 {
			opts.Workers = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("SMART_DOWNLOAD_TURBO_TAIL_BLOCKS")); v != "" {
		if n, convErr := strconv.ParseUint(v, 10, 64); convErr == nil && n > 0 {
			opts.TurboTailBlocks = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("SMART_DOWNLOAD_TURBO_REBALANCE_SECONDS")); v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n >= 10 && n <= 30 {
			opts.AllocatorInterval = time.Duration(n) * time.Second
		}
	}
	if v := strings.TrimSpace(os.Getenv("SMART_DOWNLOAD_CLOUD_BURST_MAX_JOBS")); v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n > 0 && n <= 32 {
			opts.CloudBurstMaxJobs = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("SMART_DOWNLOAD_RPC_HARD_CLAIMS")); v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n > 0 && n <= 128 {
			opts.RPCHardClaims = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("SMART_DOWNLOAD_TARGET_ROWS_PER_SHARD")); v != "" {
		if n, convErr := strconv.ParseUint(v, 10, 64); convErr == nil && n > 0 {
			opts.TargetRowsPerShard = n
		}
	}
	// Smart Download uses an isolated DuckDB file.  Sharing flow.duckdb with
	// graph refresh/import work can hold the engine mutex for minutes and block
	// Cloud artifact materialization even though read_parquet itself is
	// independent of the analytics warehouse.
	smartDuckDB := duckdb.Open(cfg.RootDir, root, duckdb.AnalyticsConfig{
		DuckDBPath:     cfg.Analytics.DuckDBPath,
		DuckDBDatabase: "smart_download.duckdb",
	})
	var partWriter smartdownload.PartWriter = smartdownload.NewJSONLPartWriter(root)
	if smartDuckDB.Available() {
		partWriter = smartdownload.NewParquetPartWriter(root, smartDuckDB)
		log.Info().Str("db", smartDuckDB.Status().Database).Msg("smart_download_duckdb_ready")
	} else {
		log.Warn().Str("error", smartDuckDB.Status().Error).Msg("smart_download_duckdb_unavailable")
	}
	svc := smartdownload.NewService(store, opts, partWriter)
	svc.SetWarehouseRequired(cfg.ClickHouse.Required)
	smartDownloadService = svc
	if err := backfillAddressLibrary(svc); err != nil {
		log.Warn().Err(err).Msg("address_library_backfill_failed")
	}
	svc.SetV32ResourceMetricsSource(&smartDownloadResourceMetrics{root: root, rpcManager: rpcManager})
	// FULL/TIME 模式终点：以链当前高度为准（RPC eth_blockNumber），
	// 避免写死 DefaultEndBlock（50M）导致预检与实际下载只覆盖旧高度。
	svc.SetHeadBlockFunc(func(ctx context.Context, chainKey string) (uint64, error) {
		if rpcManager == nil || !rpcManager.HasConfigured(chainKey) {
			return 0, fmt.Errorf("链 %s 未配置 RPC 节点", chainKey)
		}
		raw, _, err := rpcManager.Call(ctx, chainKey, "eth_blockNumber", []any{})
		if err != nil {
			return 0, err
		}
		var hexNumber string
		if err := json.Unmarshal(raw, &hexNumber); err != nil {
			return 0, fmt.Errorf("解析 eth_blockNumber 响应: %w", err)
		}
		number, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(hexNumber), "0x"), 16, 64)
		if err != nil {
			return 0, fmt.Errorf("eth_blockNumber 返回无效区块号 %q", hexNumber)
		}
		return number, nil
	})
	svc.SetTimeRangeResolver(func(ctx context.Context, chainKey string, start, end time.Time) (uint64, uint64, error) {
		return resolveSmartDownloadTimeRange(ctx, rpcManager, chainKey, start, end)
	})
	dailyCloudBudget := envFloat64("SMART_DOWNLOAD_CLOUD_DAILY_BUDGET")
	monthlyCloudBudget := envFloat64("SMART_DOWNLOAD_CLOUD_MONTHLY_BUDGET")
	maxSingleCloudCost := envFloat64("SMART_DOWNLOAD_CLOUD_MAX_SINGLE_JOB_COST")
	maxXLWorkers := int(envUint64("SMART_DOWNLOAD_CLOUD_MAX_XL_WORKERS"))
	svc.SetCloudBudget(cloudplanner.BudgetGuard{
		Enabled:          dailyCloudBudget > 0 || monthlyCloudBudget > 0 || maxSingleCloudCost > 0 || maxXLWorkers > 0,
		DailyBudget:      dailyCloudBudget,
		MonthlyBudget:    monthlyCloudBudget,
		MaxXLWorkers:     maxXLWorkers,
		MaxSingleJobCost: maxSingleCloudCost,
	})
	if smartDuckDB.Available() {
		svc.SetDuckDB(smartDuckDB)
	}
	configureSmartDownloadWriter(svc, smartDuckDB)
	if rpcManager != nil {
		rpcAdapter := smartdownload.NewRPCTransferAdapter(rpcManager)
		svc.RegisterAdapter(rpcAdapter)
		svc.SetRPCPoolMetricsSource(rpcAdapter)
	}
	if parquetDownload != nil {
		if c := parquetDownload.Manager().SQDClient(); c != nil {
			svc.RegisterAdapter(smartdownload.NewSQDAdapter(c))
		}
	}
	csvConfigDir := filepath.Join(cfg.RootDir, "backend", "data", "crypto_download")
	csvRawRoot := filepath.Join(root, "provider_raw", "csv")
	svc.RegisterAdapter(smartdownload.NewProductionCSVAdapter(csvConfigDir, csvRawRoot,
		func(ctx context.Context, chainKey string, block uint64) (time.Time, error) {
			return resolveSmartDownloadBlockTime(ctx, rpcManager, chainKey, block)
		}))
	if smartCloudRuntime != nil {
		cloudAdapter := smartdownload.NewSQDCloudAdapter(smartCloudRuntime)
		cloudAdapter.SetResultReader(svc.ReadProviderParquetRecords)
		svc.RegisterAdapter(cloudAdapter)
	}
	svc.SetRangeCoverageSource(&smartRangeCoverage{svc: svc, registry: downloadDSRegistry})
	svc.SetOnDatasetIndexed(func(ir *smartdownload.IndexedResult) {
		if ir == nil {
			return
		}
		if h, ok := analyticsAPI.(*analyticsapi.Handler); ok && h != nil {
			h.Service().InvalidateCache()
		}
		if datasetEventBus == nil {
			return
		}
		entry := &datasetsync.Entry{
			ChunkKey:      ir.ChunkKey,
			JobID:         ir.DatasetJobID,
			ChainKey:      ir.ChainKey,
			ChainID:       ir.ChainID,
			Dataset:       ir.Dataset,
			FromBlock:     ir.FromBlock,
			ToBlock:       ir.ToBlock,
			Addresses:     []string{ir.Address},
			Provider:      "SMART_DOWNLOAD",
			RowCount:      ir.RowCount,
			Status:        datasetsync.StatusCompleted,
			SyncState:     datasetsync.SyncIndexed,
			MergedParquet: ir.MergedParquet,
			CompletedAt:   ir.IndexedAt,
			SyncedAt:      ir.IndexedAt,
		}
		if graphIncrementer != nil && ir.MergedParquet != "" {
			if _, err := graphIncrementer.Apply(context.Background(), entry); err != nil {
				log.Warn().Err(err).Str("chunk", ir.ChunkKey).Msg("smart_download_graph_increment_failed")
			}
		}
		if prefetchManager != nil {
			prefetchManager.OnDatasetIndexed(ir.ChainKey, ir.Address, ir.Dataset)
		}
		ev := datasetevents.Event{
			ID:               datasetevents.IndexedEventID(ir.ChunkKey),
			Type:             datasetevents.DatasetIndexed,
			ChainKey:         ir.ChainKey,
			Dataset:          ir.Dataset,
			Addresses:        []string{ir.Address},
			FromBlock:        ir.FromBlock,
			ToBlock:          ir.ToBlock,
			RegistryEntryIDs: []string{ir.ChunkKey},
			RowCount:         ir.RowCount,
			CoverageStatus:   "HIT",
			Provider:         "SMART_DOWNLOAD",
		}
		_ = datasetEventBus.Publish(context.Background(), ev)
		log.Info().Str("chunk", ir.ChunkKey).Str("dataset", ir.Dataset).
			Int64("rows", ir.RowCount).Msg("smart_download_indexed_event_published")
	})
	var planLookup func(string) *downloadscheduler.Plan
	if downloadScheduler != nil {
		planLookup = downloadScheduler.Plan
	}
	smartDownloadAPI = http.StripPrefix("/api/smart-download", smartdownload.NewHandler(svc, planLookup, persistAddressLibrary))
	setupSemanticJobsV2()
	setupInvestigationCacheV2(svc)
	// 启动恢复：回放 Range Ledger → 校验 Parts → 未完成 Range 重新入队
	go func() {
		time.Sleep(2 * time.Second)
		if err := svc.RecoverAll(context.Background()); err != nil {
			log.Warn().Err(err).Msg("smart_download_recovery_failed")
		}
	}()
	log.Info().Str("root", root).Int("workers", opts.Workers).Msg("smart_download_ready")
}

func resolveSmartDownloadTimeRange(ctx context.Context, manager *rpcmanager.Manager, chainKey string, start, end time.Time) (uint64, uint64, error) {
	if manager == nil || !manager.HasConfigured(chainKey) {
		return 0, 0, fmt.Errorf("链 %s 未配置 RPC 节点", chainKey)
	}
	if start.After(end) {
		return 0, 0, fmt.Errorf("开始时间不能晚于结束时间")
	}
	raw, _, err := manager.Call(ctx, chainKey, "eth_blockNumber", []any{})
	if err != nil {
		return 0, 0, err
	}
	var headHex string
	if err := json.Unmarshal(raw, &headHex); err != nil {
		return 0, 0, fmt.Errorf("解析 eth_blockNumber 响应: %w", err)
	}
	head, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(headHex), "0x"), 16, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("eth_blockNumber 返回无效区块号 %q", headHex)
	}
	timestampAt := func(block uint64) (time.Time, error) {
		return resolveSmartDownloadBlockTime(ctx, manager, chainKey, block)
	}
	genesisTime, err := timestampAt(0)
	if err != nil {
		return 0, 0, fmt.Errorf("读取创世区块时间: %w", err)
	}
	headTime, err := timestampAt(head)
	if err != nil {
		return 0, 0, fmt.Errorf("读取链头区块时间: %w", err)
	}
	if end.Before(genesisTime) || start.After(headTime) {
		return 0, 0, fmt.Errorf("时间范围与链上区块范围无交集（%s - %s）", genesisTime.Format(time.RFC3339), headTime.Format(time.RFC3339))
	}
	if start.Before(genesisTime) {
		start = genesisTime
	}
	if end.After(headTime) {
		end = headTime
	}
	lowerBound := func(target time.Time, strict bool) (uint64, error) {
		lo, hi := uint64(0), head
		for lo < hi {
			mid := lo + (hi-lo)/2
			stamp, err := timestampAt(mid)
			if err != nil {
				return 0, err
			}
			matched := stamp.After(target) || (!strict && stamp.Equal(target))
			if matched {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		return lo, nil
	}
	from, err := lowerBound(start, false)
	if err != nil {
		return 0, 0, fmt.Errorf("解析起始区块: %w", err)
	}
	firstAfterEnd, err := lowerBound(end, true)
	if err != nil {
		return 0, 0, fmt.Errorf("解析结束区块: %w", err)
	}
	to := head
	if firstAfterEnd <= head {
		stamp, err := timestampAt(firstAfterEnd)
		if err != nil {
			return 0, 0, err
		}
		if stamp.After(end) {
			if firstAfterEnd == 0 {
				return 0, 0, fmt.Errorf("时间范围内没有区块")
			}
			to = firstAfterEnd - 1
		}
	}
	if to < from {
		return 0, 0, fmt.Errorf("时间范围内没有区块")
	}
	return from, to, nil
}

func resolveSmartDownloadBlockTime(ctx context.Context, manager *rpcmanager.Manager, chainKey string, block uint64) (time.Time, error) {
	if manager == nil || !manager.HasConfigured(chainKey) {
		return time.Time{}, fmt.Errorf("链 %s 未配置 RPC 节点", chainKey)
	}
	payload, _, err := manager.Call(ctx, chainKey, "eth_getBlockByNumber", []any{fmt.Sprintf("0x%x", block), false})
	if err != nil {
		return time.Time{}, err
	}
	var result struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return time.Time{}, fmt.Errorf("解析区块 %d 响应: %w", block, err)
	}
	seconds, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(result.Timestamp), "0x"), 16, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("区块 %d timestamp 无效 %q", block, result.Timestamp)
	}
	return time.Unix(seconds, 0).UTC(), nil
}

// setupCloudRuntime 装配 SQD Cloud 运行时（设计 §27/§31）：
//   - SQD_CLOUD_MODE=auto（默认）：有 SQD_DEPLOY_KEY → cloud；Worker 项目可用 → local；否则禁用
//   - SQD_CLOUD_WORKER_DIR：Cloud Worker 项目目录（默认 E:\Code\Processor-only）
//   - SQD_CLOUD_DATA_ROOT：Job/审计数据根目录（默认 E:\codex\bsc_analytics\sqd-cloud，禁止 C:）
func setupCloudRuntime(cfg *config.Config, store s3store.ObjectStore, cloudWithoutStore bool, r2Configured bool) *cloudruntime.Manager {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("SQD_CLOUD_MODE")))
	workerDir := strings.TrimSpace(os.Getenv("SQD_CLOUD_WORKER_DIR"))
	if workerDir == "" {
		workerDir = `E:\Code\Processor-only`
	}
	root := strings.TrimSpace(os.Getenv("SQD_CLOUD_DATA_ROOT"))
	if root == "" {
		root = filepath.Join(`E:\codex\bsc_analytics`, "sqd-cloud")
	}
	idleMinutes := 20
	if v := strings.TrimSpace(os.Getenv("SQD_CLOUD_IDLE_REMOVE_AFTER_MINUTES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			idleMinutes = n
		}
	}
	if cloudWithoutStore {
		store = nil // 显式 cloud 模式但缺少 R2/S3 凭据 → 运行时 NOT_CONFIGURED（如实暴露）
	}
	m := cloudruntime.New(cloudruntime.Config{
		Mode:                   cloudruntime.Mode(mode),
		WorkerProjectDir:       workerDir,
		JobsRoot:               root,
		Store:                  store,
		R2Configured:           r2Configured,
		IdleRemoveAfter:        time.Duration(idleMinutes) * time.Minute,
		DeployTimeout:          10 * time.Minute,
		RuntimeFailureCooldown: 15 * time.Minute,
		DeployKey:              os.Getenv("SQD_DEPLOY_KEY"),
		Organization:           os.Getenv("SQD_CLOUD_ORG"),
		WorkerName:             os.Getenv("SQD_CLOUD_WORKER_NAME"),
		WorkerSlot:             os.Getenv("SQD_CLOUD_WORKER_SLOT"),
	})
	log.Info().Str("mode", string(m.Status().Mode)).Str("state", string(m.Status().State)).
		Str("jobs_root", root).Msg("sqd_cloud_runtime_ready")
	return m
}

// compositeCoverageSource 覆盖检查 = 分析快照 + Cloud Registry（Phase 4 §31）。
type compositeCoverageSource struct {
	analytics downloadscheduler.CoverageSource
	registry  *datasetsync.Registry
}

func (c *compositeCoverageSource) AddressTxCount(ctx context.Context, address string) (int64, error) {
	var total int64
	if c.analytics != nil {
		n, err := c.analytics.AddressTxCount(ctx, address)
		if err != nil {
			return 0, err
		}
		total += n
	}
	if c.registry != nil {
		if n, err := c.registry.AddressTxCount(ctx, address); err == nil {
			total += n
		}
	}
	return total, nil
}

func (c *compositeCoverageSource) AddressRangeCovered(_ context.Context, chainKey, address string,
	dataset downloadscheduler.Dataset, fromBlock, toBlock uint64) (bool, int64, error) {
	if c == nil || c.registry == nil || toBlock < fromBlock {
		return false, 0, nil
	}
	coverage, ok := c.registry.AddressCoverage(chainKey, address, string(dataset))
	if !ok {
		return false, 0, nil
	}
	cur := fromBlock
	for _, r := range coverage.Ranges {
		if r.To < cur {
			continue
		}
		if r.From > cur {
			return false, coverage.RowCount, nil
		}
		if r.To >= toBlock {
			return true, coverage.RowCount, nil
		}
		if r.To == ^uint64(0) {
			return true, coverage.RowCount, nil
		}
		cur = r.To + 1
	}
	return false, coverage.RowCount, nil
}

// smartRangeCoverage Smart Download 区间覆盖源：本服务 Result Registry + Cloud Dataset Registry。
type smartRangeCoverage struct {
	svc      *smartdownload.Service
	registry *datasetsync.Registry
}

func (c *smartRangeCoverage) CoveredRanges(ctx context.Context, chainKey, address, dataset string, from, to uint64) ([]smartdownload.BlockRange, error) {
	out := c.svc.RegistryCoverage(chainKey, address, dataset, from, to)
	if c.registry != nil {
		for _, e := range c.registry.Active() {
			if e.ChainKey != chainKey || e.Dataset != dataset {
				continue
			}
			if e.SyncState != datasetsync.SyncIndexed && e.SyncState != datasetsync.SyncLocalSynced {
				continue
			}
			matched := false
			for _, a := range e.Addresses {
				if strings.EqualFold(a, address) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			lo, hi := e.FromBlock, e.ToBlock
			if from > lo {
				lo = from
			}
			if to < hi {
				hi = to
			}
			if hi >= lo {
				out = append(out, smartdownload.BlockRange{From: lo, To: hi})
			}
		}
	}
	return out, nil
}

// Shutdown closes the control store
func Shutdown() {
	if semanticJobServiceV2 != nil {
		semanticJobServiceV2.Close()
	}
	if prefetchManager != nil {
		prefetchManager.Stop()
	}
	if parquetDownload != nil {
		parquetDownload.Close()
	}
	if dataSourceManager != nil {
		dataSourceManager.Close()
	}
	if rpcManager != nil {
		if err := rpcManager.Close(); err != nil {
			log.Warn().Err(err).Msg("crypto_rpc_close_error")
		}
	}
	if controlStore != nil {
		if err := controlStore.Close(); err != nil {
			log.Warn().Err(err).Msg("control_store_close_error")
		}
	}
}

// RegisterRoutes registers all API routes on the Gin router
func RegisterRoutes(r *gin.Engine) {
	api := r.Group("/api")
	{
		registerSystemSettingsRoutes(api)
		registerClickHouseRoutes(api)
		api.POST("/process", HandleProcess)
		api.GET("/process/progress/:job_id", HandleProcessProgress)
		api.GET("/process/artifact/:job_id/:artifact_id", HandleProcessArtifact)
		api.GET("/download/:job_id", HandleDownload)
		api.POST("/dune/download", HandleDuneSQLDownload)
		api.GET("/dune/auth", HandleDuneAuthStatus)
		api.POST("/dune/auth", HandleSaveDuneAuth)
		api.POST("/dune/query", HandleDuneSQLQuery)
		api.POST("/dune/results", HandleDuneResultPage)
		api.POST("/dune/export", HandleDuneExportExcel)
		api.GET("/flow/history", HandleFlowHistory)
		api.GET("/flow/history/:job_id", HandleLoadHistoryFlow)
		api.GET("/flow/edge-detail", HandleFlowEdgeDetail)
		api.POST("/flow/edge-detail/imported", HandleImportedFlowEdgeDetail)
		api.POST("/flow/upload", HandleUploadFlowData)
		api.POST("/flow/import", HandleImportFlowData)
		api.POST("/flow/import-paths", HandleImportFlowPaths)
		api.GET("/flow/import-status/:session_id", HandleImportStatus)
		api.POST("/flow/mapping-rules", HandleSaveFlowMapping)
		api.GET("/flow/template", HandleDownloadFlowTemplate)
		api.POST("/flow/build", HandleBuildImportedFlow)
		api.POST("/ai/analyze", HandleAnalyzeFlowWithAI)
		api.POST("/flow/direction-rules", HandleSaveFlowDirectionRules)
		api.POST("/flow/direction-check", HandleCheckFlowDirectionValues)
		api.POST("/flow/values", HandleFlowFieldValues)
		// ── Investigation Cache V2 + Graph Expansion Cache + Smart Prefetch（设计 V1.0 §62-§64）──
		registerInvestigationCacheRoutes(api)
		// ── Entity Intelligence Layer V1（设计 V1.0 §53-§56）──
		registerEntityIntelRoutes(api)
		// ── Fund Flow Intelligence V2（设计 V1.0 §62-§63）──
		registerFundFlowRoutes(api)
		// ── Graph API V3（设计 V1.0 §45-§46）──
		registerGraphV3Routes(api)
		// ── Investigation Report Engine V2（设计 V1.0 §65-§68）──
		registerReportEngineRoutes(api)
		// ── V2.0 实时资产（设计 §15）──
		api.POST("/flow/address-assets", handleAddressAssets)
		api.POST("/flow/address-assets/batch", handleAddressAssetsBatch)
		api.POST("/flow/address-assets/refresh", handleAddressAssetsRefresh)
		// ── V2.0 余额快照（设计 §8）──
		api.POST("/flow/balance-snapshot", handleBalanceSnapshotSave)
		api.GET("/flow/balance-snapshots", handleBalanceSnapshotCompare)
		api.POST("/crypto/address-classify", HandleCryptoAddressClassify)
		api.Any("/crypto/download/*path", HandleCryptoDownload)
		api.Any("/crypto/parquet/*path", HandleCryptoParquet)
		api.Any("/crypto/rpc/*path", HandleCryptoRPC)
		api.Any("/crypto/enrichment/*path", HandleCryptoRPC)
		api.Any("/crypto/datasource/*path", HandleCryptoDataSource)
		api.Any("/crypto/addresses/:chain/:address/first-seen", HandleFirstSeen)
		api.Any("/analytics/*path", HandleAnalyticsAPI)
		api.Any("/dynamic-investigation/*path", HandleDynamicInvestigation)
		api.Any("/intelligence/*path", HandleIntelligence)
		api.Any("/investigation/*path", HandleInvestigationV2)
		api.Any("/scheduler/*path", HandleSchedulerAPI)
		api.Any("/smart-download/*path", HandleSmartDownloadAPI)
		registerAddressLibraryRoutes(api)
		api.GET("/dataset/events", HandleDatasetEvents)
		api.GET("/graph/status", HandleGraphStatus)
		api.GET("/address/*path", HandleAddressAnalytics)
		api.GET("/health", HandleHealth)
		api.GET("/files/current", HandleCurrentFiles)
		api.POST("/rules/analyze", HandleAnalyzeRules)
		api.POST("/rules/confirm", HandleConfirmRules)
		registerDBImportRoutes(api)
	}
}

// HandleProcess handles file upload and ETL pipeline
func HandleProcess(c *gin.Context) {
	log.Info().Msg("process_files_start")

	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(400, gin.H{"detail": "invalid multipart form: " + err.Error()})
		return
	}

	txFiles := form.File["transaction_files"]
	acctFiles := form.File["account_files"]
	labelFiles := form.File["label_file"]

	batchDir := filepath.Join(cfg.UploadDir, "current")
	os.RemoveAll(batchDir)

	for _, f := range txFiles {
		path := filepath.Join(batchDir, "transactions", safeName(f.Filename))
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := c.SaveUploadedFile(f, path); err != nil {
			c.JSON(500, gin.H{"detail": "save upload: " + err.Error()})
			return
		}
	}
	for _, f := range acctFiles {
		path := filepath.Join(batchDir, "accounts", safeName(f.Filename))
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := c.SaveUploadedFile(f, path); err != nil {
			c.JSON(500, gin.H{"detail": "save upload: " + err.Error()})
			return
		}
	}
	for _, f := range labelFiles {
		path := filepath.Join(batchDir, "labels", safeName(f.Filename))
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := c.SaveUploadedFile(f, path); err != nil {
			continue
		}
		break
	}

	rawJobID := strings.TrimSpace(c.PostForm("job_id"))
	jobID := ""
	if rawJobID != "" {
		jobID = safeName(rawJobID)
	}
	if jobID == "" || jobID == "." {
		jobID = etl.GenerateJobID()
	}
	unifySources := true
	if raw := strings.TrimSpace(c.PostForm("unify_sources")); raw != "" {
		unifySources = raw == "true" || raw == "1"
	}
	includeAlipayBalance := false
	if raw := strings.TrimSpace(c.PostForm("include_alipay_balance")); raw != "" {
		includeAlipayBalance = raw == "true" || raw == "1"
	}
	progress := newProcessProgress(jobID, unifySources)
	result, err := etl.RunPipelineWithOptions(batchDir, cfg.OutputDir, jobID, etl.PipelineOptions{
		UnifySources:         unifySources,
		IncludeAlipayBalance: includeAlipayBalance,
		Progress:             progress.update,
	})
	if err != nil {
		progress.finish(err)
		c.JSON(400, gin.H{"detail": err.Error()})
		return
	}
	for index := range result.Artifacts {
		result.Artifacts[index].DownloadURL = fmt.Sprintf("/api/process/artifact/%s/%s", jobID, result.Artifacts[index].ID)
	}
	if err := persistProcessArtifacts(jobID, result.Artifacts); err != nil {
		progress.finish(err)
		c.JSON(500, gin.H{"detail": "保存阶段产物清单失败: " + err.Error()})
		return
	}
	progress.finish(nil)

	preview, columns := etl.BuildPreview(result.Transactions, 100)
	summary := result.Summary
	flowGraph := etl.BuildFlowGraph(nil, 600)
	if result.MergeMode == "unified" {
		flowGraph = etl.BuildFlowGraph(result.Transactions, 600)
	}
	responseRows := result.Report.RowsOut
	if responseRows == 0 && len(result.Transactions) > 0 {
		responseRows = len(result.Transactions)
	}

	resp := model.ProcessResponse{
		JobID:        jobID,
		Rows:         responseRows,
		Columns:      columns,
		Preview:      preview,
		Report:       result.Report,
		Summary:      summary,
		FlowGraph:    flowGraph,
		DownloadURL:  fmt.Sprintf("/api/download/%s", jobID),
		MergeMode:    result.MergeMode,
		SourceSheets: result.SourceSheets,
		Artifacts:    result.Artifacts,
	}

	c.JSON(200, resp)
}

// HandleDownload handles file download
func HandleDownload(c *gin.Context) {
	jobID := c.Param("job_id")
	pattern := filepath.Join(cfg.OutputDir, "*"+jobID+"*")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		c.JSON(404, gin.H{"detail": "导出文件不存在或已被清理。"})
		return
	}
	path := matches[0]
	c.FileAttachment(path, filepath.Base(path))
}

// HandleFlowHistory returns list of flow sessions
func HandleFlowHistory(c *gin.Context) {
	var items []map[string]interface{}

	sessionsDir := filepath.Join(cfg.UploadDir, "flow_sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(200, gin.H{"items": []map[string]interface{}{}})
			return
		}
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		item, err := summarizeFlowSession(entry.Name())
		if err == nil {
			items = append(items, item)
		}
	}
	if items == nil {
		items = []map[string]interface{}{}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["updated_at"].(int64) > items[j]["updated_at"].(int64)
	})
	c.JSON(200, gin.H{"items": items})
}

// HandleLoadHistoryFlow loads a specific flow session
func HandleLoadHistoryFlow(c *gin.Context) {
	jobID := c.Param("job_id")
	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", jobID)
	if _, err := os.Stat(sessionDir); err != nil {
		if os.IsNotExist(err) {
			c.JSON(404, gin.H{"detail": "session not found: " + jobID})
			return
		}
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}

	columns, sample, totalRows := extractFileColumns(sessionDir)
	files := listFlowSessionFiles(sessionDir)

	var signature string
	var mappingRule map[string]interface{}
	if len(columns) > 0 {
		signature = rules.GenerateColumnSignature(columns)
		mappingRule = rules.FlowMappingRule(signature)
	}

	c.JSON(200, gin.H{
		"session_id":   jobID,
		"job_id":       jobID,
		"name":         flowSessionName(jobID, files),
		"rows":         totalRows,
		"columns":      columns,
		"files":        files,
		"sample":       sample,
		"signature":    signature,
		"mapping_rule": mappingRule,
	})
}

func summarizeFlowSession(sessionID string) (map[string]interface{}, error) {
	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", sessionID)
	var size int64
	var updatedAt int64
	files := []string{}

	err := filepath.Walk(sessionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.ModTime().Unix() > updatedAt {
			updatedAt = info.ModTime().Unix()
		}
		if info.IsDir() {
			return nil
		}
		size += info.Size()
		if parser.SupportedSuffixes[strings.ToLower(filepath.Ext(path))] {
			if rel, err := filepath.Rel(sessionDir, path); err == nil {
				files = append(files, rel)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if updatedAt == 0 {
		if info, err := os.Stat(sessionDir); err == nil {
			updatedAt = info.ModTime().Unix()
		}
	}

	return map[string]interface{}{
		"id":         sessionID,
		"job_id":     sessionID,
		"name":       flowSessionName(sessionID, files),
		"size":       size,
		"updated_at": updatedAt,
		"status":     "exists",
	}, nil
}

func listFlowSessionFiles(sessionDir string) []string {
	files := []string{}
	filepath.Walk(sessionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if !parser.SupportedSuffixes[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if rel, err := filepath.Rel(sessionDir, path); err == nil {
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	return files
}

func flowSessionName(sessionID string, files []string) string {
	if len(files) == 0 {
		return sessionID
	}
	return filepath.Base(files[0])
}

// HandleFlowEdgeDetail reads the cleaned output file and returns rows matching source/target
func HandleFlowEdgeDetail(c *gin.Context) {
	jobID := c.Query("job_id")
	source := c.Query("source")
	target := c.Query("target")
	if jobID == "" || source == "" || target == "" {
		c.JSON(400, gin.H{"detail": "job_id, source, target required"})
		return
	}

	pattern := filepath.Join(cfg.OutputDir, "*"+jobID+"*")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		c.JSON(404, gin.H{"detail": "输出文件不存在或已被清理。"})
		return
	}

	path := matches[0]
	ext := strings.ToLower(filepath.Ext(path))
	if !parser.SupportedSuffixes[ext] {
		c.JSON(400, gin.H{"detail": "不支持的文件格式"})
		return
	}

	var rows [][]string
	if parser.ExcelSuffixes[ext] {
		sheets, err := parser.ReadExcelFile(path)
		if err != nil {
			c.JSON(500, gin.H{"detail": "读取Excel文件失败: " + err.Error()})
			return
		}
		for _, s := range sheets {
			rows = append(rows, s...)
		}
	} else {
		rows, err = parser.ReadCSVFile(path)
		if err != nil {
			c.JSON(500, gin.H{"detail": "读取CSV文件失败: " + err.Error()})
			return
		}
	}

	if len(rows) < 2 {
		c.JSON(200, gin.H{"job_id": jobID, "source": source, "target": target, "rows": []map[string]interface{}{}, "columns": []string{}, "total_rows": 0})
		return
	}

	headers := rows[0]
	colIdx := make(map[string]int)
	for i, h := range headers {
		colIdx[parser.NormalizeHeader(h)] = i
	}

	getVal := func(name string, row []string) string {
		if idx, ok := colIdx[parser.NormalizeHeader(name)]; ok && idx < len(row) {
			return row[idx]
		}
		return ""
	}

	var result []map[string]interface{}
	for _, row := range rows[1:] {
		own := getVal("交易卡号", row)
		if own == "" {
			own = getVal("交易账号", row)
		}
		if own == "" {
			own = getVal("交易户名", row)
		}
		if own == "" {
			own = "本方未知"
		}

		counter := getVal("交易对手账卡号", row)
		if counter == "" {
			counter = getVal("对手户名", row)
		}
		if counter == "" {
			counter = "对手未知"
		}

		dir := getVal("收付标志", row)
		var rowSource, rowTarget string
		if dir == "出" {
			rowSource, rowTarget = own, counter
		} else if dir == "进" {
			rowSource, rowTarget = counter, own
		} else {
			continue
		}

		if rowSource != source || rowTarget != target {
			continue
		}

		m := make(map[string]interface{})
		for j, h := range headers {
			if j < len(row) {
				m[h] = row[j]
			}
		}
		result = append(result, m)
	}

	var columns []string
	if len(result) > 0 {
		for k := range result[0] {
			columns = append(columns, k)
		}
	}

	var totalAmount float64
	for _, row := range result {
		if v, ok := row[parser.NormalizeHeader("交易金额")]; ok {
			if s, ok := v.(string); ok {
				totalAmount += parser.ToNumber(s)
			}
		}
	}

	c.JSON(200, gin.H{
		"job_id":        jobID,
		"source":        source,
		"target":        target,
		"total_rows":    len(result),
		"returned_rows": len(result),
		"amount":        totalAmount,
		"columns":       columns,
		"rows":          result,
		"truncated":     false,
	})
}

// applyColumnOriginsRemap applies column_origins.json remapping for database import sessions
func applyColumnOriginsRemap(uploadDir, sessionID string, rows []map[string]interface{}, columns []string) ([]map[string]interface{}, []string) {
	originsPath := filepath.Join(uploadDir, "flow_sessions", sessionID, "column_origins.json")
	data, err := os.ReadFile(originsPath)
	if err != nil || len(rows) == 0 {
		return rows, columns
	}
	var origins struct {
		SourceColumns  []string          `json:"source_columns"`
		TargetToSource map[string]string `json:"target_to_source"`
	}
	if json.Unmarshal(data, &origins) != nil || len(origins.TargetToSource) == 0 {
		return rows, columns
	}

	targetsWithMapping := make(map[string]bool, len(origins.TargetToSource))
	for tgt := range origins.TargetToSource {
		targetsWithMapping[tgt] = true
	}

	displayCols := make([]string, 0, len(columns))
	seen := make(map[string]bool)
	for _, sc := range origins.SourceColumns {
		if !seen[sc] {
			displayCols = append(displayCols, sc)
			seen[sc] = true
		}
	}
	for _, col := range columns {
		if !targetsWithMapping[col] && !seen[col] {
			displayCols = append(displayCols, col)
			seen[col] = true
		}
	}

	for i, row := range rows {
		newRow := make(map[string]interface{}, len(row))
		for k, v := range row {
			if src, ok := origins.TargetToSource[k]; ok {
				newRow[src] = v
			} else {
				newRow[k] = v
			}
		}
		rows[i] = newRow
	}
	return rows, displayCols
}

// HandleImportedFlowEdgeDetail handles edge detail for imported data
func HandleImportedFlowEdgeDetail(c *gin.Context) {
	var payload EdgeDetailPayload
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}
	if payload.SessionID == "" {
		c.JSON(400, gin.H{"detail": "session_id required"})
		return
	}
	if payload.Limit <= 0 || payload.Limit > 100000 {
		payload.Limit = 10000
	}

	// Try DuckDB first if analysis table exists
	mapping := flowColumnMappingFromPayload(map[string]interface{}{
		"source_column":    payload.SourceColumn,
		"target_column":    payload.TargetColumn,
		"amount_column":    payload.AmountColumn,
		"time_column":      payload.TimeColumn,
		"direction_column": payload.DirectionColumn,
	})
	if rows, totalRows, totalAmount, columns, err := queryEdgeDetailFromDuckDB(payload.SessionID, mapping, payload); err == nil && rows != nil {
		// Apply column_origins.json remapping for database import sessions
		rows, columns = applyColumnOriginsRemap(cfg.UploadDir, payload.SessionID, rows, columns)

		resultRows := rows
		returnedRows := len(rows)
		truncated := false
		if payload.Limit > 0 && totalRows > payload.Limit {
			returnedRows = payload.Limit
			truncated = true
			if len(rows) > payload.Limit {
				resultRows = rows[:payload.Limit]
			}
		}
		c.JSON(200, gin.H{
			"job_id":        payload.SessionID,
			"source":        payload.Source,
			"target":        payload.Target,
			"total_rows":    totalRows,
			"returned_rows": returnedRows,
			"amount":        totalAmount,
			"columns":       columns,
			"rows":          resultRows,
			"truncated":     truncated,
			"duckdb":        true,
		})
		return
	}

	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", payload.SessionID)
	rows := queryEdgeRows(sessionDir, payload)
	// Use cached column order (preserves source file ordering)
	columns := getCachedColumnOrder(payload.SessionID)
	if columns == nil && len(rows) > 0 {
		// Fallback: deterministic sort by key name for non-cached data
		for k := range rows[0] {
			columns = append(columns, k)
		}
		sort.Strings(columns)
	}
	// Apply original source column names if available (database import)
	rows, columns = applyColumnOriginsRemap(cfg.UploadDir, payload.SessionID, rows, columns)
	// Calculate total amount (try raw column name first, then normalized)
	var totalAmount float64
	amountRaw := payload.AmountColumn
	amountNorm := parser.NormalizeHeader(payload.AmountColumn)
	for _, row := range rows {
		if v, ok := row[amountRaw]; ok {
			if s, ok := v.(string); ok {
				totalAmount += parser.ToNumber(s)
			}
		} else if v, ok := row[amountNorm]; ok {
			if s, ok := v.(string); ok {
				totalAmount += parser.ToNumber(s)
			}
		}
	}
	// Apply limit
	totalRows := len(rows)
	returnedRows := totalRows
	truncated := false
	if payload.Limit > 0 && totalRows > payload.Limit {
		returnedRows = payload.Limit
		truncated = true
	}
	resultRows := rows
	if truncated {
		resultRows = rows[:returnedRows]
	}
	c.JSON(200, gin.H{
		"job_id":        payload.SessionID,
		"source":        payload.Source,
		"target":        payload.Target,
		"total_rows":    totalRows,
		"returned_rows": returnedRows,
		"amount":        totalAmount,
		"columns":       columns,
		"rows":          resultRows,
		"truncated":     truncated,
	})
}

// HandleUploadFlowData handles flow data upload
func HandleUploadFlowData(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(400, gin.H{"detail": err.Error()})
		return
	}
	files := form.File["files"]
	sessionID := uuid.New().String()[:12]
	for _, f := range files {
		path := filepath.Join(cfg.UploadDir, "flow_sessions", sessionID, safeName(f.Filename))
		os.MkdirAll(filepath.Dir(path), 0755)
		c.SaveUploadedFile(f, path)
	}
	c.JSON(200, gin.H{"session_id": sessionID, "files": len(files)})
}

// HandleImportFlowData imports flow data asynchronously (etl_exe pattern)
func HandleImportFlowData(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(400, gin.H{"detail": err.Error()})
		return
	}
	files := form.File["files"]
	if len(files) == 0 {
		c.JSON(400, gin.H{"detail": "请上传数据文件"})
		return
	}

	// Create session and save files
	sessionID := uuid.New().String()[:12]
	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", sessionID)
	os.MkdirAll(sessionDir, 0755)
	var fileNames []string
	for _, f := range files {
		path := filepath.Join(sessionDir, safeName(f.Filename))
		if err := c.SaveUploadedFile(f, path); err != nil {
			continue
		}
		fileNames = append(fileNames, f.Filename)
	}

	// Start async import — respond immediately with session_id
	prog := &AsyncImportProgress{
		Status:    "parsing",
		SessionID: sessionID,
		Files:     fileNames,
	}
	setImportProgress(sessionID, prog)

	go runAsyncImport(sessionID, sessionDir, fileNames, prog)

	c.JSON(200, gin.H{
		"session_id": sessionID,
		"status":     "parsing",
		"files":      fileNames,
	})
}

// runAsyncImport inspects files and extracts columns/sample in background
func runAsyncImport(sessionID, sessionDir string, fileNames []string, prog *AsyncImportProgress) {
	// Inspect files (sample only)
	columns, sample, totalRows := extractFileColumns(sessionDir)
	if len(columns) == 0 && len(fileNames) > 0 {
		firstPath := filepath.Join(sessionDir, safeName(fileNames[0]))
		columns, sample, totalRows = readFileColumns(firstPath)
	}

	if len(columns) == 0 {
		prog.mu.Lock()
		prog.Status = "error"
		prog.Error = "未能读取到有效数据行"
		prog.mu.Unlock()
		return
	}

	// Check for existing mapping rules
	var mappingRule map[string]interface{}
	signature := rules.GenerateColumnSignature(columns)
	mappingRule = rules.FlowMappingRule(signature)

	prog.mu.Lock()
	prog.Status = "done"
	prog.Columns = columns
	prog.Sample = sample
	prog.Rows = totalRows
	prog.MappingRule = mappingRule
	prog.mu.Unlock()

	// Record in SQLite control store
	if controlStore != nil {
		_ = controlStore.UpsertSession(sessionID, fileNames[0], totalRows, 0, 0, "", "created")
	}

	// Try to load into DuckDB in background
	go ensureSessionDuckDBTable(sessionID, sessionDir)
}

// HandleImportStatus returns async import progress
func HandleImportStatus(c *gin.Context) {
	sessionID := c.Param("session_id")
	prog := getImportProgress(sessionID)
	if prog == nil {
		c.JSON(404, gin.H{"detail": "session not found"})
		return
	}
	prog.mu.Lock()
	defer prog.mu.Unlock()
	c.JSON(200, prog)
}

// HandleImportFlowPaths imports flow data from local file paths (no upload)
func HandleImportFlowPaths(c *gin.Context) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}
	if len(req.Paths) == 0 {
		c.JSON(400, gin.H{"detail": "请提供文件路径"})
		return
	}

	// Validate and filter paths
	var validPaths []string
	var fileNames []string
	for _, p := range req.Paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(p))
		if !parser.SupportedSuffixes[ext] {
			continue
		}
		validPaths = append(validPaths, p)
		fileNames = append(fileNames, filepath.Base(p))
	}
	if len(validPaths) == 0 {
		c.JSON(400, gin.H{"detail": "没有找到有效的数据文件"})
		return
	}

	// Create session and copy/link files
	sessionID := uuid.New().String()[:12]
	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", sessionID)
	os.MkdirAll(sessionDir, 0755)
	for i, src := range validPaths {
		name := fileNames[i]
		// Create symlink or copy
		dst := filepath.Join(sessionDir, safeName(name))
		_ = copyFileToSession(src, dst)
	}

	// Extract columns and sample from files
	columns, sample, totalRows := extractFileColumns(sessionDir)
	if len(columns) == 0 {
		columns, sample, totalRows = readFileColumns(validPaths[0])
	}

	// Check for existing mapping rules
	var mappingRule map[string]interface{}
	if len(columns) > 0 {
		signature := rules.GenerateColumnSignature(columns)
		mappingRule = rules.FlowMappingRule(signature)
	}

	c.JSON(200, gin.H{
		"session_id":   sessionID,
		"rows":         totalRows,
		"columns":      columns,
		"files":        fileNames,
		"sample":       sample,
		"mapping_rule": mappingRule,
	})
}

func copyFileToSession(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()
	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()
	_, err = io.Copy(d, s)
	return err
}
func HandleSaveFlowMapping(c *gin.Context) {
	var payload struct {
		Columns []string               `json:"columns"`
		Mapping map[string]interface{} `json:"mapping"`
	}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}
	if len(payload.Columns) == 0 {
		c.JSON(400, gin.H{"detail": "columns required"})
		return
	}
	if payload.Mapping == nil {
		c.JSON(400, gin.H{"detail": "mapping required"})
		return
	}

	signature := rules.GenerateColumnSignature(payload.Columns)
	rule := map[string]interface{}{
		"signature":      signature,
		"source_columns": payload.Columns,
		"mapping":        payload.Mapping,
		"updated_at":     time.Now().Format("2006-01-02 15:04:05"),
	}
	if _, err := rules.SaveFlowMappingRule(rule); err != nil {
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}

	c.JSON(200, gin.H{"status": "ok", "signature": signature})
}

// HandleDownloadFlowTemplate downloads the flow template
func HandleDownloadFlowTemplate(c *gin.Context) {
	templatePath := cfg.FlowTemplatePath
	if _, err := os.Stat(templatePath); os.IsNotExist(err) {
		os.MkdirAll(filepath.Dir(templatePath), 0755)
		f := excelize.NewFile()
		columns := []string{"交易方户名", "交易方账户", "交易方身份证号", "交易方标签", "交易时间", "交易金额", "收付标志",
			"交易余额", "交易对手账卡号", "对手户名", "对手身份证号", "对手标签", "交易流水号", "摘要说明", "备注"}
		for i, h := range columns {
			cell, _ := excelize.CoordinatesToCellName(i+1, 1)
			f.SetCellValue("Sheet1", cell, h)
		}
		f.SaveAs(templatePath)
		f.Close()
	}
	c.FileAttachment(templatePath, "flow_template.xlsx")
}

// HandleBuildImportedFlow builds flow graph from imported data
func HandleBuildImportedFlow(c *gin.Context) {
	var payload map[string]interface{}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}

	sessionID, _ := payload["session_id"].(string)
	if sessionID == "" {
		c.JSON(400, gin.H{"detail": "session_id required"})
		return
	}

	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", sessionID)
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		c.JSON(404, gin.H{"detail": "session not found"})
		return
	}

	// Extract column mapping from payload
	mapping := flowColumnMappingFromPayload(payload)
	directionValues, _ := payload["direction_values"].([]interface{})

	// Parse direction values mapping
	dirMap := make(map[string]string)
	for _, v := range directionValues {
		if m, ok := v.(map[string]interface{}); ok {
			src, _ := m["source"].(string)
			dst, _ := m["target"].(string)
			if src != "" && dst != "" {
				dirMap[src] = dst
			}
		}
	}

	// Attempt to load session files into DuckDB for faster subsequent queries
	go ensureSessionDuckDBTable(sessionID, sessionDir)

	// Try DuckDB first if analysis table exists
	if graph, summary, err := buildFlowFromDuckDB(sessionID, mapping, payload); err == nil && graph != nil {
		c.JSON(200, gin.H{
			"nodes":      graph.Nodes,
			"edges":      graph.Edges,
			"meta":       graph.Meta,
			"columns":    nil,
			"preview":    nil,
			"rows":       summary["total_rows"],
			"session_id": sessionID,
			"summary":    summary,
		})
		return
	}

	// Read source files and build transaction rows (also preloads edge detail cache)
	txns := readSessionDataWithCache(sessionDir, sessionID, mapping, dirMap)

	// Check for unknown direction values
	unknownDirs := checkUnknownDirections(txns)
	if len(unknownDirs) > 0 {
		c.JSON(400, gin.H{
			"detail": map[string]interface{}{
				"code":    "unknown_flow_directions",
				"message": "\u53d1\u73b0\u672a\u77e5\u6536\u4ed8\u6807\u5fd7\uff1a" + strings.Join(unknownDirs, "\u3001"),
				"values":  unknownDirs,
			},
		})
		return
	}

	// Build flow graph from unfiltered data
	// Apply source/target filters if provided
	filteredTxns := applyFilters(txns, payload)

	// Build preview and flow graph
	preview, columns := etl.BuildPreview(filteredTxns, 200)
	summary := etl.BuildSummary(filteredTxns)
	flowGraph := etl.BuildFlowGraph(filteredTxns, flowEdgeLimit(payload))

	c.JSON(200, gin.H{
		"nodes":      flowGraph.Nodes,
		"edges":      flowGraph.Edges,
		"meta":       flowGraph.Meta,
		"columns":    columns,
		"preview":    preview,
		"rows":       len(filteredTxns),
		"session_id": sessionID,
		"summary":    summary,
	})
}

// HandleAnalyzeFlowWithAI handles AI-powered flow analysis
func HandleAnalyzeFlowWithAI(c *gin.Context) {
	var payload map[string]interface{}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}
	c.JSON(200, gin.H{
		"report":   "AI analysis not configured. Set DEEPSEEK_API_KEY for AI-powered analysis.",
		"filtered": 0, "session_id": payload["session_id"],
	})
}

// HandleSaveFlowDirectionRules saves direction aliases
func HandleSaveFlowDirectionRules(c *gin.Context) {
	var payload map[string]interface{}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}
	aliases, _ := payload["aliases"].(map[string]interface{})
	strAliases := make(map[string]string)
	for k, v := range aliases {
		strAliases[k] = fmt.Sprint(v)
	}
	_, err := rules.SaveDirectionAliases(strAliases)
	if err != nil {
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(200, gin.H{"status": "ok"})
}

// HandleCheckFlowDirectionValues checks direction values
func HandleCheckFlowDirectionValues(c *gin.Context) {
	var payload map[string]interface{}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}

	sessionID, _ := payload["session_id"].(string)
	column, _ := payload["column"].(string)
	if sessionID == "" || column == "" {
		c.JSON(400, gin.H{"detail": "session_id and column required"})
		return
	}

	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", sessionID)
	rawValues := extractColumnValues(sessionDir, column, 500)
	aliases := make(map[string]string)
	for k, v := range rules.LoadDirectionAliases() {
		aliases[strings.TrimSpace(k)] = v
		aliases[parser.NormalizeHeader(k)] = v
	}
	var values []string
	for _, v := range rawValues {
		normalized := normalizeFlowDirection(v, aliases)
		if normalized != "出" && normalized != "进" {
			values = append(values, v)
		}
	}
	c.JSON(200, gin.H{
		"unknown_values": values,
		"session_id":     sessionID,
	})
}

// HandleFlowFieldValues returns field values for a session
func HandleFlowFieldValues(c *gin.Context) {
	var payload struct {
		SessionID string `json:"session_id"`
		Column    string `json:"column"`
		Search    string `json:"search"`
		Limit     int    `json:"limit"`
	}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}
	if payload.SessionID == "" || payload.Column == "" {
		c.JSON(400, gin.H{"detail": "session_id and column required"})
		return
	}
	if payload.Limit <= 0 || payload.Limit > 1000 {
		payload.Limit = 300
	}

	// Try DuckDB first if analysis table exists
	if values, err := queryColumnValuesFromDuckDB(payload.SessionID, payload.Column, payload.Search, payload.Limit); err == nil && values != nil {
		c.JSON(200, gin.H{
			"values":     values,
			"session_id": payload.SessionID,
			"duckdb":     true,
		})
		return
	}

	sessionDir := filepath.Join(cfg.UploadDir, "flow_sessions", payload.SessionID)
	values := extractColumnValues(sessionDir, payload.Column, payload.Limit)
	c.JSON(200, gin.H{
		"values":     values,
		"session_id": payload.SessionID,
	})
}

// HandleDatasetEvents GET /api/dataset/events — Dataset Event Bus 审计列表（Phase 5.3 §5/§14）。
func HandleDatasetEvents(c *gin.Context) {
	if datasetEventBus == nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "dataset event bus 未装配"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": datasetEventBus.Events()})
}

// HandleGraphStatus GET /api/graph/status — Graph 增量状态（Phase 5.3 §7/§18）。
func HandleGraphStatus(c *gin.Context) {
	if graphIncrementer == nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "graph incrementer 未装配"})
		return
	}
	c.JSON(http.StatusOK, graphIncrementer.Status())
}

// HandleHealth returns health status
func HandleHealth(c *gin.Context) {
	resp := gin.H{"status": "ok"}
	if controlStore != nil {
		resp["control_plane"] = controlStore.Status()
	}
	if analysisEngine != nil {
		resp["analysis_plane"] = analysisEngine.Status()
	}
	c.JSON(200, resp)
}

// HandleCurrentFiles lists current uploads and rule samples
func HandleCurrentFiles(c *gin.Context) {
	uploads := listLocalFiles(filepath.Join(cfg.UploadDir, "current"))
	samples := listLocalFiles(filepath.Join(cfg.RuleSamplesDir, "current"))
	c.JSON(200, gin.H{"uploads": uploads, "rule_samples": samples})
}

// HandleAnalyzeRules analyzes rule samples
func HandleAnalyzeRules(c *gin.Context) {
	providerStr := c.PostForm("provider")
	if providerStr != "alipay" && providerStr != "wechat" && providerStr != "bank" {
		c.JSON(400, gin.H{"detail": "provider 必须是 alipay、wechat 或 bank"})
		return
	}
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(400, gin.H{"detail": err.Error()})
		return
	}
	files := form.File["sample_files"]
	batchDir := filepath.Join(cfg.RuleSamplesDir, "current")
	os.RemoveAll(batchDir)
	os.MkdirAll(batchDir, 0755)
	for _, f := range files {
		path := filepath.Join(batchDir, safeName(f.Filename))
		c.SaveUploadedFile(f, path)
	}
	c.JSON(200, gin.H{
		"provider": providerStr, "candidates": []map[string]interface{}{},
		"suggestions": []map[string]interface{}{},
	})
}

// HandleConfirmRules confirms and saves custom rules
func HandleConfirmRules(c *gin.Context) {
	var payload map[string]interface{}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"detail": "invalid json"})
		return
	}
	providerStr, _ := payload["provider"].(string)
	rule, _ := payload["rule"].(map[string]interface{})
	if providerStr == "" || rule == nil {
		c.JSON(400, gin.H{"detail": "provider and rule required"})
		return
	}
	data, err := rules.SaveCustomRule(providerStr, rule)
	if err != nil {
		c.JSON(500, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(200, gin.H{"status": "ok", "rules": data.Providers[providerStr]})
}

func safeName(name string) string {
	return strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(filepath.Base(name))
}

func listLocalFiles(dir string) []map[string]interface{} {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return []map[string]interface{}{}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []map[string]interface{}{}
	}
	var files []map[string]interface{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, _ := e.Info()
		files = append(files, map[string]interface{}{
			"name": e.Name(), "path": e.Name(),
			"size": info.Size(), "updated_at": info.ModTime().Unix(),
		})
	}
	return files
}

// ========== Import/Flow helper functions ==========

// extractFileColumns scans a directory and extracts columns and sample data
func extractFileColumns(sessionDir string) ([]string, []map[string]interface{}, int) {
	scan, err := scanner.ScanDirectory(sessionDir)
	if err != nil || len(scan.Transactions) == 0 {
		return nil, nil, 0
	}

	cand := scan.Transactions[0]
	columns := cand.Columns
	if len(columns) == 0 {
		return nil, nil, 0
	}

	// Read only limited rows (header + sample) to avoid OOM on large files
	rows, totalEst := readFileColumnsLimited(cand.Path, cand.SheetName, 50)
	sample := rowsToSample(rows, columns, 20)
	totalRows := totalEst
	if totalRows == 0 {
		totalRows = cand.RowsSampled
	}
	return columns, sample, totalRows
}

// readFileColumnsLimited reads up to maxRows data rows (+ header) from a file
// Returns the rows (including header) and an estimated total row count
func readFileColumnsLimited(path, sheetName string, maxRows int) ([][]string, int) {
	ext := strings.ToLower(filepath.Ext(path))
	if parser.ExcelSuffixes[ext] {
		totalEst := parser.CountExcelRows(path, sheetName)
		var allRows [][]string
		if sheetName != "" {
			allRows, _ = parser.ReadExcelSheetLimited(path, sheetName, maxRows+1)
		} else {
			sheets, _ := parser.ReadExcelFile(path)
			for _, rows := range sheets {
				if len(rows) > 0 {
					limit := maxRows + 1
					if len(rows) > limit {
						rows = rows[:limit]
					}
					allRows = rows
					break
				}
			}
		}
		return allRows, totalEst
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0
	}
	rows, err := parser.ReadCSVRowsLimited(path, maxRows+1)
	if err != nil {
		return nil, 0
	}
	totalEst := 0
	if len(rows) >= 2 && info.Size() > 0 {
		avgRowBytes := float64(info.Size()) / float64(len(rows))
		totalEst = int(float64(info.Size())/avgRowBytes) - 1
	}
	return rows, totalEst
}

// readFileColumns directly reads a file and extracts columns/sample
func readFileColumns(path string) ([]string, []map[string]interface{}, int) {
	ext := strings.ToLower(filepath.Ext(path))
	if parser.ExcelSuffixes[ext] {
		sheets, err := parser.ReadExcelFile(path)
		if err != nil || len(sheets) == 0 {
			return nil, nil, 0
		}
		for _, rows := range sheets {
			if len(rows) < 2 {
				continue
			}
			columns := make([]string, len(rows[0]))
			for i, c := range rows[0] {
				columns[i] = parser.NormalizeHeader(c)
			}
			totalRows := len(rows) - 1
			sample := rowsToSample(rows, columns, 20)
			return columns, sample, totalRows
		}
	} else {
		rows, err := parser.ReadCSVFile(path)
		if err != nil || len(rows) < 2 {
			return nil, nil, 0
		}
		columns := make([]string, len(rows[0]))
		for i, c := range rows[0] {
			columns[i] = parser.NormalizeHeader(c)
		}
		totalRows := len(rows) - 1
		sample := rowsToSample(rows, columns, 20)
		return columns, sample, totalRows
	}
	return nil, nil, 0
}

// rowsToSample converts raw rows to sample map slice
func rowsToSample(rows [][]string, columns []string, maxRows int) []map[string]interface{} {
	if len(rows) < 2 {
		return nil
	}
	dataRows := rows[1:]
	if len(dataRows) > maxRows {
		dataRows = dataRows[:maxRows]
	}
	sample := make([]map[string]interface{}, len(dataRows))
	for i, row := range dataRows {
		m := make(map[string]interface{})
		for j, col := range columns {
			if j < len(row) {
				m[col] = row[j]
			} else {
				m[col] = ""
			}
		}
		sample[i] = m
	}
	return sample
}

// readSessionData reads session files and builds TransactionRows with column mapping
func readSessionData(sessionDir string, mapping flowColumnMapping, dirMap map[string]string) []model.TransactionRow {
	var txns []model.TransactionRow
	mapping = normalizeFlowColumnMapping(mapping)
	// Also normalize dirMap keys for consistent matching
	normalizedDirMap := make(map[string]string, len(dirMap))
	for k, v := range rules.LoadDirectionAliases() {
		normalizedDirMap[strings.TrimSpace(k)] = v
		normalizedDirMap[parser.NormalizeHeader(k)] = v
	}
	for k, v := range dirMap {
		normalizedDirMap[strings.TrimSpace(k)] = v
		normalizedDirMap[parser.NormalizeHeader(k)] = v
	}

	filepath.Walk(sessionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !parser.SupportedSuffixes[ext] {
			return nil
		}

		var rows [][]string
		if parser.ExcelSuffixes[ext] {
			sheets, err := parser.ReadExcelFile(path)
			if err != nil {
				return nil
			}
			for _, sheet := range sheets {
				rows = append(rows, sheet...)
			}
		} else {
			rows, err = parser.ReadCSVFile(path)
			if err != nil {
				return nil
			}
		}

		if len(rows) < 2 {
			return nil
		}

		headers := rows[0]
		colIdx := make(map[string]int)
		for i, h := range headers {
			colIdx[parser.NormalizeHeader(h)] = i
		}

		for _, row := range rows[1:] {
			txns = append(txns, transactionFromMappedRow(row, colIdx, mapping, normalizedDirMap))
		}
		return nil
	})

	return txns
}

func normalizeFlowColumnMapping(mapping flowColumnMapping) flowColumnMapping {
	mapping.SourceCol = parser.NormalizeHeader(mapping.SourceCol)
	mapping.SourceAccount = parser.NormalizeHeader(mapping.SourceAccount)
	mapping.SourceName = parser.NormalizeHeader(mapping.SourceName)
	mapping.SourceID = parser.NormalizeHeader(mapping.SourceID)
	mapping.SourceLabel = parser.NormalizeHeader(mapping.SourceLabel)
	mapping.TargetCol = parser.NormalizeHeader(mapping.TargetCol)
	mapping.TargetCard = parser.NormalizeHeader(mapping.TargetCard)
	mapping.TargetName = parser.NormalizeHeader(mapping.TargetName)
	mapping.TargetID = parser.NormalizeHeader(mapping.TargetID)
	mapping.TargetLabel = parser.NormalizeHeader(mapping.TargetLabel)
	mapping.Amount = parser.NormalizeHeader(mapping.Amount)
	mapping.Time = parser.NormalizeHeader(mapping.Time)
	mapping.Direction = parser.NormalizeHeader(mapping.Direction)
	mapping.Serial = parser.NormalizeHeader(mapping.Serial)
	mapping.Summary = parser.NormalizeHeader(mapping.Summary)
	mapping.Remark = parser.NormalizeHeader(mapping.Remark)
	return mapping
}

func transactionFromMappedRow(row []string, colIdx map[string]int, mapping flowColumnMapping, dirMap map[string]string) model.TransactionRow {
	txn := make(model.TransactionRow)
	setMappedValue := func(sourceColumn, targetColumn string) {
		if sourceColumn == "" {
			return
		}
		if idx, ok := colIdx[sourceColumn]; ok && idx < len(row) {
			txn[targetColumn] = row[idx]
		}
	}

	setMappedValue(flowNameColumn(mapping.SourceName, mapping.SourceCol), "交易户名")
	setMappedValue(mapping.SourceAccount, "交易账号")
	setMappedValue(mapping.SourceID, "交易方身份证号")
	setMappedValue(mapping.SourceLabel, "交易方标签")
	setMappedValue(flowNameColumn(mapping.TargetName, mapping.TargetCol), "对手户名")
	setMappedValue(mapping.TargetCard, "交易对手账卡号")
	setMappedValue(mapping.TargetID, "对手身份证号")
	setMappedValue(mapping.TargetLabel, "对手标签")
	setMappedValue(mapping.Amount, "交易金额")
	setMappedValue(mapping.Time, "交易时间")
	setMappedValue(mapping.Serial, "交易流水号")
	setMappedValue(mapping.Summary, "摘要说明")
	setMappedValue(mapping.Remark, "备注")
	if mapping.Direction != "" {
		if idx, ok := colIdx[mapping.Direction]; ok && idx < len(row) {
			txn["\u6536\u4ed8\u6807\u5fd7"] = normalizeFlowDirection(row[idx], dirMap)
		}
	}
	if txn["交易时间"] != "" {
		txn["交易时间"] = parser.NormalizeDatetime(txn["交易时间"])
	}
	if txn["交易金额"] != "" {
		txn["交易金额"] = parser.FloatToStr(parser.ToNumber(txn["交易金额"]))
	}
	return txn
}

func normalizeFlowDirection(value string, aliases map[string]string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	if mapped, ok := aliases[raw]; ok {
		return mapped
	}
	normalizedKey := parser.NormalizeHeader(raw)
	if mapped, ok := aliases[normalizedKey]; ok {
		return mapped
	}
	normalized := parser.NormalizeDirection(raw)
	if mapped, ok := aliases[normalized]; ok {
		return mapped
	}
	return normalized
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func flowNameColumn(nameColumn, fallbackColumn string) string {
	if nameColumn != "" {
		return nameColumn
	}
	normalized := parser.NormalizeHeader(fallbackColumn)
	if normalized == "" || strings.Contains(normalized, "银行") || strings.Contains(normalized, "开户行") {
		return ""
	}
	if strings.Contains(normalized, "户名") || strings.Contains(normalized, "姓名") || strings.Contains(normalized, "名称") || normalized == "name" {
		return fallbackColumn
	}
	return ""
}

// checkUnknownDirections checks for direction values that aren't \"\u8fdb\" or \"\u51fa\"
func checkUnknownDirections(txns []model.TransactionRow) []string {
	seen := make(map[string]bool)
	var unknown []string
	for _, txn := range txns {
		dir := txn["\u6536\u4ed8\u6807\u5fd7"]
		if dir != "" && dir != "\u8fdb" && dir != "\u51fa" {
			if !seen[dir] {
				seen[dir] = true
				unknown = append(unknown, dir)
			}
		}
	}
	return unknown
}

// applyFilters applies source/target filters to transactions
func applyFilters(txns []model.TransactionRow, payload map[string]interface{}) []model.TransactionRow {
	filtered := make([]model.TransactionRow, 0)

	for _, txn := range txns {
		if transactionMatchesFilters(txn, payload) {
			filtered = append(filtered, txn)
		}
	}
	return filtered
}

func transactionMatchesFilters(txn model.TransactionRow, payload map[string]interface{}) bool {
	sourceFilters, _ := payload["source_filters"].([]interface{})
	targetFilters, _ := payload["target_filters"].([]interface{})
	detailFilters, _ := payload["detail_filters"].([]interface{})
	directions := stringSet(payload["directions"])
	sourceLabelValues := stringSet(payload["source_label_values"])
	targetLabelValues := stringSet(payload["target_label_values"])
	startDate, _ := payload["start_date"].(string)
	endDate, _ := payload["end_date"].(string)

	return matchesFilterGroups(txn, sourceFilters) &&
		matchesFilterGroups(txn, targetFilters) &&
		matchesFilterGroups(txn, detailFilters) &&
		matchesValueSet(txn["交易方标签"], sourceLabelValues) &&
		matchesValueSet(txn["对手标签"], targetLabelValues) &&
		matchesDirection(txn, directions) &&
		matchesDateRange(txn, startDate, endDate)
}

func flowEdgeLimit(payload map[string]interface{}) int {
	if requested := intPayloadValue(payload["max_edges"]); requested > 0 {
		if requested > auditFlowEdgeLimit {
			return auditFlowEdgeLimit
		}
		return requested
	}
	sourceFilters, _ := payload["source_filters"].([]interface{})
	targetFilters, _ := payload["target_filters"].([]interface{})
	detailFilters, _ := payload["detail_filters"].([]interface{})
	startDate, _ := payload["start_date"].(string)
	endDate, _ := payload["end_date"].(string)
	if hasActiveFilterGroups(sourceFilters) ||
		hasActiveFilterGroups(targetFilters) ||
		hasActiveFilterGroups(detailFilters) ||
		hasActiveValues(payload["source_label_values"]) ||
		hasActiveValues(payload["target_label_values"]) ||
		hasActiveValues(payload["directions"]) ||
		strings.TrimSpace(startDate) != "" ||
		strings.TrimSpace(endDate) != "" {
		return auditFlowEdgeLimit
	}
	return defaultFlowEdgeLimit
}

func intPayloadValue(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		return 0
	}
}

func hasActiveFilterGroups(filters []interface{}) bool {
	for _, item := range filters {
		filter, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		column, _ := filter["column"].(string)
		if column != "" && len(stringSet(filter["values"])) > 0 {
			return true
		}
	}
	return false
}

func hasActiveValues(raw interface{}) bool {
	return len(stringSet(raw)) > 0
}

func matchesFilterGroups(txn model.TransactionRow, filters []interface{}) bool {
	for _, item := range filters {
		filter, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		column, _ := filter["column"].(string)
		values := stringSet(filter["values"])
		if column == "" || len(values) == 0 {
			continue
		}
		if !values[txn[parser.NormalizeHeader(column)]] {
			return false
		}
	}
	return true
}

func matchesDirection(txn model.TransactionRow, directions map[string]bool) bool {
	if len(directions) == 0 {
		return true
	}
	return directions[txn["\u6536\u4ed8\u6807\u5fd7"]]
}

func matchesValueSet(value string, values map[string]bool) bool {
	if len(values) == 0 {
		return true
	}
	return values[value]
}

func matchesDateRange(txn model.TransactionRow, startDate, endDate string) bool {
	if startDate == "" && endDate == "" {
		return true
	}
	tradeTime := parser.NormalizeDatetime(txn["\u4ea4\u6613\u65f6\u95f4"])
	if tradeTime == "" {
		return false
	}
	startDate = normalizeFilterBoundary(startDate, false)
	endDate = normalizeFilterBoundary(endDate, true)
	if startDate != "" && tradeTime < startDate {
		return false
	}
	if endDate != "" && tradeTime > endDate {
		return false
	}
	return true
}

func normalizeFilterBoundary(value string, end bool) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	dateOnly := len(value) == 10 && strings.Count(value, "-") == 2
	normalized := parser.NormalizeDatetime(value)
	if normalized == "" {
		normalized = value
	}
	if dateOnly || len(normalized) == 10 {
		if len(normalized) > 10 {
			normalized = normalized[:10]
		}
		if end {
			return normalized + " 23:59:59"
		}
		return normalized + " 00:00:00"
	}
	return normalized
}

func stringSet(raw interface{}) map[string]bool {
	values := make(map[string]bool)
	items, ok := raw.([]interface{})
	if !ok {
		return values
	}
	for _, item := range items {
		value := strings.TrimSpace(fmt.Sprint(item))
		if value != "" {
			values[value] = true
		}
	}
	return values
}

// extractColumnValues extracts unique values for a given column from session files
func extractColumnValues(sessionDir string, column string, limit int) []string {
	seen := make(map[string]bool)
	var values []string

	filepath.Walk(sessionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !parser.SupportedSuffixes[ext] {
			return nil
		}

		var rows [][]string
		if parser.ExcelSuffixes[ext] {
			sheets, err := parser.ReadExcelFile(path)
			if err != nil {
				return nil
			}
			for _, s := range sheets {
				rows = append(rows, s...)
			}
		} else {
			rows, err = parser.ReadCSVFile(path)
			if err != nil {
				return nil
			}
		}

		if len(rows) < 2 {
			return nil
		}

		headers := rows[0]
		colIdx := -1
		for i, h := range headers {
			if parser.NormalizeHeader(h) == parser.NormalizeHeader(column) {
				colIdx = i
				break
			}
		}
		if colIdx < 0 {
			return nil
		}

		for _, row := range rows[1:] {
			if colIdx < len(row) {
				val := strings.TrimSpace(row[colIdx])
				if val != "" && !seen[val] && len(values) < limit {
					seen[val] = true
					values = append(values, val)
				}
			}
		}
		return nil
	})

	return values
}

// queryEdgeRows queries transaction rows matching source/target
func queryEdgeRows(sessionDir string, p EdgeDetailPayload) []map[string]interface{} {
	// Fast path: use cached session file data (populated during graph build)
	if cache := getCachedFiles(p.SessionID); cache != nil {
		return processCachedRows(cache, p)
	}

	var result []map[string]interface{}
	mapping := normalizeFlowColumnMapping(flowColumnMapping{
		SourceCol:     p.SourceColumn,
		SourceAccount: p.SourceAccountColumn,
		SourceName:    p.SourceNameColumn,
		SourceID:      p.SourceIDColumn,
		SourceLabel:   p.SourceLabelColumn,
		TargetCol:     p.TargetColumn,
		TargetCard:    p.TargetCardColumn,
		TargetName:    p.TargetNameColumn,
		TargetID:      p.TargetIDColumn,
		TargetLabel:   p.TargetLabelColumn,
		Amount:        p.AmountColumn,
		Time:          p.TimeColumn,
		Direction:     p.DirectionColumn,
		Serial:        p.SerialColumn,
		Summary:       p.SummaryColumn,
		Remark:        p.RemarkColumn,
	})
	filterPayload := edgeDetailFilterPayload(p)
	dirMap := make(map[string]string)
	for k, v := range rules.LoadDirectionAliases() {
		dirMap[strings.TrimSpace(k)] = v
		dirMap[parser.NormalizeHeader(k)] = v
	}

	filepath.Walk(sessionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !parser.SupportedSuffixes[ext] {
			return nil
		}

		var rows [][]string
		if parser.ExcelSuffixes[ext] {
			sheets, err := parser.ReadExcelFile(path)
			if err != nil {
				return nil
			}
			for _, s := range sheets {
				rows = append(rows, s...)
			}
		} else {
			rows, err = parser.ReadCSVFile(path)
			if err != nil {
				return nil
			}
		}

		if len(rows) < 2 {
			return nil
		}

		headers := rows[0]
		colIdx := make(map[string]int)
		for i, h := range headers {
			colIdx[parser.NormalizeHeader(h)] = i
		}

		for _, row := range rows[1:] {
			txn := transactionFromMappedRow(row, colIdx, mapping, dirMap)
			if !transactionMatchesFilters(txn, filterPayload) {
				continue
			}
			source, target := flowEndpointsForTransaction(txn)
			if source != p.Source || target != p.Target {
				continue
			}
			m := make(map[string]interface{})
			for j, h := range headers {
				if j < len(row) {
					m[parser.NormalizeHeader(h)] = row[j]
				}
			}
			m["流向源"] = source
			m["流向目标"] = target
			result = append(result, m)
		}
		return nil
	})

	return result
}

func edgeDetailFilterPayload(p EdgeDetailPayload) map[string]interface{} {
	return map[string]interface{}{
		"source_filters":      p.SourceFilters,
		"target_filters":      p.TargetFilters,
		"detail_filters":      p.DetailFilters,
		"source_label_values": p.SourceLabelValues,
		"target_label_values": p.TargetLabelValues,
		"directions":          p.Directions,
		"start_date":          p.StartDate,
		"end_date":            p.EndDate,
	}
}

func flowEndpointsForTransaction(txn model.TransactionRow) (string, string) {
	own := txn["交易卡号"]
	if own == "" {
		own = txn["交易账号"]
	}
	if own == "" {
		own = txn["交易户名"]
	}
	if own == "" {
		own = "本方未知"
	}
	counter := txn["交易对手账卡号"]
	if counter == "" {
		counter = txn["对手户名"]
	}
	if counter == "" {
		counter = "未知主体"
	}
	switch txn["收付标志"] {
	case "出":
		return own, counter
	case "进":
		return counter, own
	default:
		return "", ""
	}
}
