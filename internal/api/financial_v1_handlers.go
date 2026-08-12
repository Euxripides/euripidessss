package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/etl/backend/internal/financialanalytics"
	"github.com/etl/backend/internal/financialflow"
	"github.com/etl/backend/internal/financialintegration"
	"github.com/etl/backend/internal/financialpnl"
	"github.com/etl/backend/internal/financialquality"
	"github.com/etl/backend/internal/pricing"
)

var (
	priceResolverV1      *pricing.Resolver
	priceGapDetectorV1   *pricing.GapDetector
	financialAnalyticsV1 *financialanalytics.Repository
	financialFlowV1      *financialflow.Repository
	financialPnLV1       *financialpnl.Service
	financialQualityV1   *financialquality.Repository
	financialGraphV1     *financialintegration.GraphRepository
	financialExporterV1  *financialintegration.Exporter
)

func setupFinancialV1() {
	if clickHouseClient == nil {
		return
	}
	priceRepository := pricing.NewRepository(clickHouseClient)
	priceResolverV1 = pricing.NewResolver(priceRepository, pricing.ResolverOptions{})
	priceGapDetectorV1 = pricing.NewGapDetector(priceRepository)
	financialAnalyticsV1 = financialanalytics.NewRepository(clickHouseClient)
	financialFlowV1 = financialflow.NewRepository(clickHouseClient)
	financialPnLV1 = financialpnl.NewService(financialpnl.NewRepository(clickHouseClient), 24*time.Hour)
	financialQualityV1 = financialquality.NewRepository(clickHouseClient)
	financialGraphV1 = financialintegration.NewGraphRepository(clickHouseClient)
	financialExporterV1 = financialintegration.NewExporter(clickHouseClient)
	log.Info().Msg("historical_price_financial_analytics_ready")
}

func registerFinancialV1Routes(api *gin.RouterGroup) {
	base := "/v2/analytics/:chain/address/:address"
	api.GET(base+"/financial-summary", handleFinancialSummaryV1)
	api.GET(base+"/financial-counterparties", handleFinancialCounterpartiesV1)
	api.GET(base+"/financial-entities", handleFinancialEntitiesV1)
	api.GET(base+"/cex", handleFinancialCEXV1)
	api.GET(base+"/dex", handleFinancialDEXV1)
	api.GET(base+"/bridge", handleFinancialBridgeV1)
	api.GET(base+"/fifo-retention", handleFinancialFlowV1)
	api.GET(base+"/fifo-pass-through", handleFinancialFlowV1)
	api.GET(base+"/pnl", handleFinancialPnLV1)
	api.GET(base+"/historical-usd-graph", handleHistoricalUSDGraphV1)
	api.GET("/v2/pricing/:chain/token/:token/resolve", handleResolvePriceV1)
	api.GET("/v2/pricing/:chain/token/:token/gaps", handlePriceGapsV1)
	api.GET("/v2/financial-quality/:chain", handleFinancialQualityV1)
	api.GET("/v2/financial-export/:chain/address/:address.csv", handleFinancialExportV1)
}

func financialScope(c *gin.Context) (uint32, bool) {
	id, ok := chainIDFromKey(c.Param("chain"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "unsupported chain"})
		return 0, false
	}
	if financialAnalyticsV1 == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "financial analytics is unavailable"})
		return 0, false
	}
	return id, true
}

func parseFinancialTime(c *gin.Context, key string, fallback time.Time) (time.Time, bool) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return fallback.UTC(), true
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": key + " must be RFC3339"})
		return time.Time{}, false
	}
	return value.UTC(), true
}

func financialQuery(c *gin.Context, chainID uint32) (financialanalytics.Query, bool) {
	now := time.Now().UTC()
	window := financialanalytics.Window(strings.ToUpper(defaultString(c.Query("window"), "30D")))
	from, ok := parseFinancialTime(c, "from", now.Add(-30*24*time.Hour))
	if !ok {
		return financialanalytics.Query{}, false
	}
	to, ok := parseFinancialTime(c, "to", now)
	if !ok {
		return financialanalytics.Query{}, false
	}
	limit, err := strconv.Atoi(defaultString(c.Query("limit"), "50"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "limit must be an integer"})
		return financialanalytics.Query{}, false
	}
	return financialanalytics.Query{ChainID: chainID, Address: c.Param("address"), Window: window, From: from, To: to,
		LargeThresholdUSD:   defaultString(c.Query("large_threshold_usd"), "100000"),
		EntityMinConfidence: strings.ToUpper(defaultString(c.Query("entity_min_confidence"), "HIGH")), Limit: limit}, true
}

func handleFinancialSummaryV1(c *gin.Context) {
	id, ok := financialScope(c)
	if !ok {
		return
	}
	query, ok := financialQuery(c, id)
	if !ok {
		return
	}
	result, err := financialAnalyticsV1.FinancialSummary(c.Request.Context(), query)
	writeFinancialResult(c, result, err)
}

func handleFinancialCounterpartiesV1(c *gin.Context) {
	id, ok := financialScope(c)
	if !ok {
		return
	}
	query, ok := financialQuery(c, id)
	if !ok {
		return
	}
	result, err := financialAnalyticsV1.Counterparties(c.Request.Context(), query)
	writeFinancialResult(c, result, err)
}

func handleFinancialEntitiesV1(c *gin.Context) {
	id, ok := financialScope(c)
	if !ok {
		return
	}
	query, ok := financialQuery(c, id)
	if !ok {
		return
	}
	result, err := financialAnalyticsV1.EntityStats(c.Request.Context(), query)
	writeFinancialResult(c, result, err)
}

func handleFinancialCEXV1(c *gin.Context) {
	id, ok := financialScope(c)
	if !ok {
		return
	}
	query, ok := financialQuery(c, id)
	if !ok {
		return
	}
	result, err := financialAnalyticsV1.CEXStats(c.Request.Context(), query)
	writeFinancialResult(c, result, err)
}

func handleFinancialDEXV1(c *gin.Context) {
	id, ok := financialScope(c)
	if !ok {
		return
	}
	query, ok := financialQuery(c, id)
	if !ok {
		return
	}
	result, err := financialAnalyticsV1.DEXStats(c.Request.Context(), query)
	writeFinancialResult(c, result, err)
}

func handleFinancialBridgeV1(c *gin.Context) {
	id, ok := financialScope(c)
	if !ok {
		return
	}
	query, ok := financialQuery(c, id)
	if !ok {
		return
	}
	result, err := financialAnalyticsV1.BridgeStats(c.Request.Context(), query)
	writeFinancialResult(c, result, err)
}

func handleFinancialFlowV1(c *gin.Context) {
	id, ok := financialScope(c)
	if !ok {
		return
	}
	if financialFlowV1 == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "FIFO financial flow is unavailable"})
		return
	}
	now := time.Now().UTC()
	from, ok := parseFinancialTime(c, "from", now.Add(-365*24*time.Hour))
	if !ok {
		return
	}
	to, ok := parseFinancialTime(c, "to", now)
	if !ok {
		return
	}
	maxRows, err := strconv.Atoi(defaultString(c.Query("max_rows"), strconv.Itoa(financialflow.DefaultMaxRows)))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "max_rows must be an integer"})
		return
	}
	batch, err := financialFlowV1.Load(c.Request.Context(), financialflow.Query{ChainID: id, Address: c.Param("address"), Token: strings.ToLower(c.Query("token")), From: from, To: to, MaxRows: maxRows})
	if err != nil {
		writeFinancialError(c, err)
		return
	}
	snapshotID := strings.Join([]string{"api", strconv.FormatUint(uint64(id), 10), strings.ToLower(c.Param("address")), strings.ToLower(c.Query("token")), from.Format(time.RFC3339Nano), to.Format(time.RFC3339Nano)}, ":")
	report, err := financialflow.Analyze(batch.Events, financialflow.Snapshot{ID: snapshotID, AsOf: to, PriceVersion: defaultString(c.Query("price_version"), "local-canonical")})
	writeFinancialResult(c, report, err)
}

func handleFinancialPnLV1(c *gin.Context) {
	id, ok := financialScope(c)
	if !ok {
		return
	}
	if financialPnLV1 == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "financial PnL is unavailable"})
		return
	}
	asOf, ok := parseFinancialTime(c, "as_of", time.Now().UTC())
	if !ok {
		return
	}
	token := strings.ToLower(c.Query("token"))
	if token == "" {
		token = fmt.Sprintf("native:%d", id)
	}
	persist := strings.EqualFold(c.Query("persist"), "true")
	result, snapshotID, err := financialPnLV1.Calculate(c.Request.Context(), financialpnl.Query{ChainID: id, Address: c.Param("address"), Token: token, AsOf: asOf}, persist)
	if err != nil {
		writeFinancialError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": result, "snapshot_id": snapshotID})
}

func handleResolvePriceV1(c *gin.Context) {
	id, ok := financialScope(c)
	if !ok {
		return
	}
	at, ok := parseFinancialTime(c, "at", time.Time{})
	if !ok || at.IsZero() {
		if ok {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "at is required"})
		}
		return
	}
	result, err := priceResolverV1.ResolvePrice(c.Request.Context(), uint64(id), c.Param("token"), at)
	if errors.Is(err, pricing.ErrPriceMissing) {
		c.JSON(http.StatusOK, gin.H{"price": nil, "price_status": "MISSING"})
		return
	}
	if err != nil {
		writeFinancialError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"price": result, "price_status": "AVAILABLE"})
}

func handlePriceGapsV1(c *gin.Context) {
	id, ok := financialScope(c)
	if !ok {
		return
	}
	from, ok := parseFinancialTime(c, "from", time.Time{})
	if !ok || from.IsZero() {
		if ok {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "from is required"})
		}
		return
	}
	to, ok := parseFinancialTime(c, "to", time.Time{})
	if !ok || to.IsZero() {
		if ok {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "to is required"})
		}
		return
	}
	result, err := priceGapDetectorV1.Detect(c.Request.Context(), pricing.GapRequest{ChainID: uint64(id), Token: c.Param("token"), From: from, To: to, Resolution: pricing.Resolution(defaultString(c.Query("resolution"), "1h"))})
	writeFinancialResult(c, result, err)
}

func handleFinancialQualityV1(c *gin.Context) {
	id, ok := financialScope(c)
	if !ok {
		return
	}
	window := strings.ToUpper(defaultString(c.Query("window"), "30D"))
	key := fmt.Sprintf("%d:%s", id, window)
	if cached, ok := financialQualityTTL.Get(key); ok {
		writeFinancialResult(c, cached, nil)
		return
	}
	result, err := financialQualityV1.Report(c.Request.Context(), id, financialquality.Filter{Window: window})
	if err == nil {
		financialQualityTTL.Set(key, result)
	}
	writeFinancialResult(c, result, err)
}

func handleHistoricalUSDGraphV1(c *gin.Context) {
	id, ok := financialScope(c)
	if !ok {
		return
	}
	now := time.Now().UTC()
	from, ok := parseFinancialTime(c, "from", now.Add(-30*24*time.Hour))
	if !ok {
		return
	}
	to, ok := parseFinancialTime(c, "to", now)
	if !ok {
		return
	}
	limit, err := strconv.Atoi(defaultString(c.Query("limit"), "200"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "limit must be an integer"})
		return
	}
	result, err := financialGraphV1.HistoricalGraph(c.Request.Context(), financialintegration.GraphQuery{ChainID: id, Address: c.Param("address"), From: from, To: to, MinUSD: defaultString(c.Query("min_usd"), "0"), TokenAddress: strings.ToLower(c.Query("token")), Limit: limit})
	writeFinancialResult(c, result, err)
}

func handleFinancialExportV1(c *gin.Context) {
	id, ok := financialScope(c)
	if !ok {
		return
	}
	now := time.Now().UTC()
	from, ok := parseFinancialTime(c, "from", now.Add(-30*24*time.Hour))
	if !ok {
		return
	}
	to, ok := parseFinancialTime(c, "to", now)
	if !ok {
		return
	}
	limit, err := strconv.ParseUint(defaultString(c.Query("limit"), "100000"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "limit must be an integer"})
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="financial_export.csv"`)
	_, err = financialExporterV1.StreamHistoricalCSV(c.Request.Context(), c.Writer, financialintegration.ExportRequest{Dataset: financialintegration.ExportDataset(defaultString(c.Query("dataset"), "historical_activity")), ChainID: id, Address: c.Param("address"), From: from, To: to, TokenAddress: strings.ToLower(c.Query("token")), MinUSD: defaultString(c.Query("min_usd"), "0"), Limit: limit})
	if err != nil {
		log.Warn().Err(err).Msg("financial_export_failed")
	}
}

func writeFinancialResult(c *gin.Context, result any, err error) {
	if err != nil {
		writeFinancialError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func writeFinancialError(c *gin.Context, err error) {
	if errors.Is(err, financialanalytics.ErrInvalidInput) || errors.Is(err, financialflow.ErrInvalidQuery) || errors.Is(err, financialpnl.ErrInvalidQuery) || errors.Is(err, financialquality.ErrInvalidInput) || errors.Is(err, financialintegration.ErrInvalidInput) || errors.Is(err, pricing.ErrInvalidInput) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid financial query"})
		return
	}
	if errors.Is(err, financialflow.ErrRowLimit) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"detail": "financial query exceeded its bounded row limit"})
		return
	}
	log.Warn().Err(err).Msg("financial_query_failed")
	c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "financial query failed"})
}
