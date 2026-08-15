package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
	"github.com/etl/backend/internal/datawarehouse"
	"github.com/etl/backend/internal/explorer"
	"github.com/etl/backend/internal/smartdownload"
)

var (
	clickHouseClient   *clickhouse.Client
	clickHouseExplorer *explorer.Repository
	clickHouseWriter   *datawarehouse.Writer
)

type smartDownloadWarehouseMetrics struct {
	mu          sync.Mutex
	writer      *datawarehouse.Writer
	lastRows    uint64
	lastSampled time.Time
}

func (m *smartDownloadWarehouseMetrics) SmartDownloadThroughput(string) smartdownload.ThroughputSnapshot {
	if m == nil || m.writer == nil {
		return smartdownload.ThroughputSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	metrics := m.writer.Metrics()
	result := smartdownload.ThroughputSnapshot{InsertP95Millis: float64(metrics.InsertP95MS)}
	if !m.lastSampled.IsZero() && metrics.InsertRows >= m.lastRows {
		seconds := now.Sub(m.lastSampled).Seconds()
		if seconds > 0 {
			result.InsertedRowsPerSecond = float64(metrics.InsertRows-m.lastRows) / seconds
		}
	}
	m.lastRows = metrics.InsertRows
	m.lastSampled = now
	return result
}

// setupClickHouse assembles the data plane once. A configured but temporarily
// unavailable database remains attached so ingestion fails closed instead of
// silently completing as a Parquet-only job.
func setupClickHouse(c *config.Config) {
	if c == nil {
		return
	}
	client, err := clickhouse.New(c.ClickHouse)
	if err != nil {
		log.Error().Err(err).Msg("clickhouse_client_invalid")
		return
	}
	clickHouseClient = client
	clickHouseExplorer = explorer.NewRepository(client)
	if !c.ClickHouse.Enabled {
		log.Info().Msg("clickhouse_data_plane_disabled")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.ClickHouse.ConnectTimeout)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		log.Warn().Err(err).Msg("clickhouse_data_plane_unavailable")
		return
	}
	if err := ensureClickHouseLineage(ctx, client); err != nil {
		log.Warn().Err(err).Msg("clickhouse_lineage_migration_failed")
		return
	}
	if err := ensureClickHouseSemanticSchema(ctx, client, c.RootDir); err != nil {
		log.Warn().Err(err).Msg("clickhouse_semantic_migration_failed")
		return
	}
	if err := ensureClickHouseFinancialSchema(ctx, client, c.RootDir); err != nil {
		log.Warn().Err(err).Msg("clickhouse_financial_migration_failed")
		return
	}
	log.Info().Str("database", c.ClickHouse.Database).Msg("clickhouse_data_plane_ready")
	setupClickHouseExport()
	setupClickHouseInvestigation()
	setupClickHouseGraph()
	setupClickHouseAnalytics()
	setupCanonicalV2()
	setupFinancialV1()
	setupPriceEngine(c)
}

func ensureClickHouseFinancialSchema(ctx context.Context, client *clickhouse.Client, root string) error {
	for _, name := range []string{"financial_schema.sql", "pnl_schema.sql", "price_engine_schema.sql"} {
		if err := applyClickHouseMigration(ctx, client, filepath.Join(root, "deploy", "clickhouse", name)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}

func ensureClickHouseSemanticSchema(ctx context.Context, client *clickhouse.Client, root string) error {
	return applyClickHouseMigration(ctx, client, filepath.Join(root, "deploy", "clickhouse", "v2_semantic_schema.sql"))
}

func applyClickHouseMigration(ctx context.Context, client *clickhouse.Client, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	for _, statement := range strings.Split(string(data), ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if err := client.Exec(ctx, statement); err != nil {
			return fmt.Errorf("execute migration: %w", err)
		}
	}
	return nil
}

func configureSmartDownloadWriter(svc *smartdownload.Service, parquetEngine *duckdb.Engine) {
	if svc == nil || clickHouseClient == nil || cfg == nil || !cfg.ClickHouse.Enabled || parquetEngine == nil || !parquetEngine.Available() {
		return
	}
	clickHouseWriter = datawarehouse.NewWriter(clickHouseClient, parquetEngine)
	clickHouseWriter.SetAnalyticsRefresher(clickHouseExplorer)
	clickHouseWriter.SetEventRegistry(&clickHouseABIRegistry{client: clickHouseClient})
	svc.SetIndexedWriter(clickHouseWriter)
	svc.SetThroughputMetricsSource(&smartDownloadWarehouseMetrics{writer: clickHouseWriter})
}

func ensureClickHouseLineage(ctx context.Context, client *clickhouse.Client) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS onchain.address_counterparty_stats
(chain_id UInt32,address LowCardinality(String),counterparty_address LowCardinality(String),direction LowCardinality(String),activity_count UInt64,tx_count UInt64,native_amount Decimal256(38),usd_value Decimal(38,18),first_seen_time DateTime64(3,'UTC'),last_seen_time DateTime64(3,'UTC'),updated_at DateTime64(3,'UTC') DEFAULT now64(3))
ENGINE=ReplacingMergeTree(updated_at) PARTITION BY (chain_id,cityHash64(address)%64) ORDER BY (chain_id,address,counterparty_address,direction)`,
		`CREATE TABLE IF NOT EXISTS onchain.address_daily_stats
(chain_id UInt32,address LowCardinality(String),activity_date Date,in_count UInt64,out_count UInt64,in_native_amount Decimal256(38),out_native_amount Decimal256(38),native_netflow Decimal256(38),in_usd_value Decimal(38,18),out_usd_value Decimal(38,18),usd_netflow Decimal(38,18),unique_counterparty_count UInt64,updated_at DateTime64(3,'UTC') DEFAULT now64(3))
ENGINE=ReplacingMergeTree(updated_at) PARTITION BY (chain_id,toYYYYMM(activity_date)) ORDER BY (chain_id,address,activity_date)`,
		"ALTER TABLE onchain.chain_transactions ADD COLUMN IF NOT EXISTS ingest_job_id String DEFAULT ''",
		"ALTER TABLE onchain.chain_transactions ADD COLUMN IF NOT EXISTS source_range_id String DEFAULT ''",
		"ALTER TABLE onchain.token_transfers ADD COLUMN IF NOT EXISTS ingest_job_id String DEFAULT ''",
		"ALTER TABLE onchain.token_transfers ADD COLUMN IF NOT EXISTS source_range_id String DEFAULT ''",
		"ALTER TABLE onchain.internal_transactions ADD COLUMN IF NOT EXISTS ingest_job_id String DEFAULT ''",
		"ALTER TABLE onchain.internal_transactions ADD COLUMN IF NOT EXISTS source_range_id String DEFAULT ''",
		"ALTER TABLE onchain.contract_creations ADD COLUMN IF NOT EXISTS ingest_job_id String DEFAULT ''",
		"ALTER TABLE onchain.contract_creations ADD COLUMN IF NOT EXISTS source_range_id String DEFAULT ''",
		"ALTER TABLE onchain.address_activity ADD COLUMN IF NOT EXISTS ingest_job_id String DEFAULT ''",
		"ALTER TABLE onchain.address_activity ADD COLUMN IF NOT EXISTS source_range_id String DEFAULT ''",
	}
	for _, statement := range statements {
		if err := client.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func registerClickHouseRoutes(api *gin.RouterGroup) {
	registerClickHouseExportRoutes(api)
	registerClickHouseInvestigationRoutes(api)
	registerClickHouseGraphRoutes(api)
	registerClickHouseAnalyticsRoutes(api)
	registerCanonicalV2Routes(api)
	registerFinancialV1Routes(api)
	registerPriceEngineRoutes(api)
	registerExplorerIntelligenceRoutes(api)
	api.GET("/v1/system/clickhouse", handleClickHouseHealth)
	base := "/v1/explorer/:chain/address/:address"
	api.GET(base+"/summary", handleExplorerSummary)
	api.GET(base+"/activity", handleExplorerActivity(explorer.ActivityAll))
	api.GET(base+"/transactions", handleExplorerActivity(explorer.ActivityTransactions))
	api.GET(base+"/token-transfers", handleExplorerActivity(explorer.ActivityTokenTransfers))
	api.GET(base+"/internal-transactions", handleExplorerActivity(explorer.ActivityInternal))
	api.GET(base+"/contract-creations", handleExplorerActivity(explorer.ActivityContractCreation))
	api.GET(base+"/counterparties", handleExplorerCounterparties)
	api.GET(base+"/daily-stats", handleExplorerDailyStats)
	api.GET("/v1/explorer/:chain/tx/:tx_hash", handleExplorerTransactionDetail)
	api.GET("/v1/explorer/:chain/contract/:address", handleExplorerContractDetail)
	api.GET("/v1/explorer/:chain/token/:address", handleExplorerTokenMetadata)
}

func handleExplorerCounterparties(c *gin.Context) {
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	limit, ok := explorerLimit(c, 50)
	if !ok {
		return
	}
	items, err := clickHouseExplorer.GetCounterpartyStats(c.Request.Context(), chainID, c.Param("address"), limit)
	if err != nil {
		writeExplorerError(c, err)
		return
	}
	c.JSON(http.StatusOK, items)
}

func handleExplorerDailyStats(c *gin.Context) {
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	limit, ok := explorerLimit(c, 30)
	if !ok {
		return
	}
	parseDate := func(key string) (time.Time, bool) {
		raw := strings.TrimSpace(c.Query(key))
		if raw == "" {
			return time.Time{}, true
		}
		value, err := time.Parse("2006-01-02", raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"detail": key + " must use YYYY-MM-DD"})
			return time.Time{}, false
		}
		return value, true
	}
	from, ok := parseDate("from")
	if !ok {
		return
	}
	to, ok := parseDate("to")
	if !ok {
		return
	}
	items, err := clickHouseExplorer.GetDailyStats(c.Request.Context(), explorer.DailyStatsQuery{
		ChainID: chainID, Address: c.Param("address"), From: from, To: to, Limit: limit,
	})
	if err != nil {
		writeExplorerError(c, err)
		return
	}
	c.JSON(http.StatusOK, items)
}

func handleExplorerTransactionDetail(c *gin.Context) {
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	item, err := clickHouseExplorer.GetTransactionDetail(c.Request.Context(), chainID, c.Param("tx_hash"))
	if err != nil {
		writeExplorerError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func handleExplorerContractDetail(c *gin.Context) {
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	item, err := clickHouseExplorer.GetContractDetail(c.Request.Context(), chainID, c.Param("address"))
	if err != nil {
		writeExplorerError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func handleExplorerTokenMetadata(c *gin.Context) {
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	item, err := clickHouseExplorer.GetTokenMetadata(c.Request.Context(), chainID, c.Param("address"))
	if err != nil {
		writeExplorerError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func explorerLimit(c *gin.Context, fallback int) (int, bool) {
	raw := strings.TrimSpace(c.Query("limit"))
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "limit must be an integer"})
		return 0, false
	}
	return value, true
}

func handleClickHouseHealth(c *gin.Context) {
	if clickHouseClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"enabled": false, "healthy": false, "error": "ClickHouse client is not configured"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	health := clickHouseClient.Health(ctx)
	status := http.StatusOK
	if !health.Healthy {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{
		"enabled":               health.Enabled,
		"healthy":               health.Healthy,
		"latency":               health.Latency.String(),
		"error":                 health.Error,
		"datasource":            cfg.Analytics.DataSource,
		"duckdb_reader_enabled": cfg.Analytics.DuckDBReaderEnabled,
		"metrics":               clickHouseClient.Metrics(),
		"writer_metrics":        clickHouseWriter.Metrics(),
	})
}

func handleExplorerSummary(c *gin.Context) {
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	address := strings.ToLower(strings.TrimSpace(c.Param("address")))
	result, err := clickHouseExplorer.GetAddressSummary(c.Request.Context(), chainID, address)
	if err != nil {
		if errors.Is(err, explorer.ErrNotFound) {
			// 合法地址但无活动数据：业务空状态，不是资源缺失（避免 404 与浏览器资源加载错误）。
			c.JSON(http.StatusOK, explorer.NoDataSummary(chainID, address))
			return
		}
		writeExplorerError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func handleExplorerActivity(kind explorer.ActivityKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		chainID, ok := explorerScope(c)
		if !ok {
			return
		}
		pageSize := 50
		if raw := strings.TrimSpace(c.Query("page_size")); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"detail": "page_size must be an integer"})
				return
			}
			pageSize = value
		}
		result, err := clickHouseExplorer.ListActivity(c.Request.Context(), explorer.ActivityQuery{
			ChainID: chainID, Address: c.Param("address"), Activity: kind,
			PageSize: pageSize, Cursor: c.Query("cursor"),
		})
		if err != nil {
			writeExplorerError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func explorerScope(c *gin.Context) (uint32, bool) {
	if clickHouseExplorer == nil || cfg == nil || !cfg.ClickHouse.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse Explorer is unavailable"})
		return 0, false
	}
	chains := map[string]uint32{"ethereum": 1, "eth": 1, "bsc": 56, "base": 8453, "arbitrum": 42161}
	raw := strings.ToLower(strings.TrimSpace(c.Param("chain")))
	if chainID, exists := chains[raw]; exists {
		return chainID, true
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "unsupported chain"})
		return 0, false
	}
	return uint32(value), true
}

func writeExplorerError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, explorer.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
	case errors.Is(err, explorer.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"detail": "address not found"})
	default:
		log.Warn().Err(err).Msg("clickhouse_explorer_query_failed")
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse Explorer query failed"})
	}
}
