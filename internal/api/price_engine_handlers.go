package api

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/etl/backend/internal/config"
	"github.com/etl/backend/internal/pricing"
	"github.com/etl/backend/internal/smartdownload"
)

var priceEngineV1 struct {
	sync.RWMutex
	config        pricing.EngineConfig
	paths         pricing.EnginePaths
	anchors       *pricing.AnchorRepository
	repository    *pricing.Repository
	bars          *pricing.PriceBarRepository
	importer      *pricing.BinanceArchiveImporter
	rebuild       *pricing.RebuildService
	dex           *pricing.DEXRepository
	jobs          map[string]*priceAnchorJob
	dexJobs       map[string]*priceDEXJob
	backfillSlots chan struct{}
}

type priceAnchorJob struct {
	ID        string                      `json:"id"`
	Symbol    string                      `json:"symbol"`
	Month     string                      `json:"month"`
	Status    string                      `json:"status"`
	Result    *pricing.AnchorImportResult `json:"result,omitempty"`
	Error     string                      `json:"error,omitempty"`
	CreatedAt time.Time                   `json:"created_at"`
	UpdatedAt time.Time                   `json:"updated_at"`
}

type priceDEXJob struct {
	ID        string                 `json:"id"`
	BatchID   string                 `json:"batch_id"`
	Token     string                 `json:"token"`
	Status    string                 `json:"status"`
	Error     string                 `json:"error,omitempty"`
	Pools     []string               `json:"pools"`
	FromBlock uint64                 `json:"from_block"`
	ToBlock   uint64                 `json:"to_block"`
	From      time.Time              `json:"from"`
	To        time.Time              `json:"to"`
	Result    *pricing.RebuildResult `json:"result,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

func setupPriceEngine(c *config.Config) {
	if c == nil || clickHouseClient == nil || !c.PriceEngine.Enabled {
		return
	}
	paths, err := pricing.PrepareEnginePaths(c.PriceEngine.RootDir)
	if err != nil {
		log.Error().Err(err).Msg("price_engine_paths_invalid")
		return
	}
	priceRepository := pricing.NewRepository(clickHouseClient)
	anchors := pricing.NewAnchorRepository(clickHouseClient)
	priceEngineV1.Lock()
	priceEngineV1.config = pricing.EngineConfigFromApp(c.PriceEngine)
	priceEngineV1.paths = paths
	priceEngineV1.anchors = anchors
	priceEngineV1.repository = priceRepository
	priceEngineV1.bars = pricing.NewPriceBarRepository(clickHouseClient)
	priceEngineV1.importer = pricing.NewBinanceArchiveImporter(c.PriceEngine.BinanceBaseURL, paths, anchors, priceRepository, c.PriceEngine.DownloadTimeout)
	priceEngineV1.dex = pricing.NewDEXRepository(clickHouseClient)
	priceEngineV1.rebuild = pricing.NewRebuildService(priceEngineV1.dex, priceEngineV1.bars, priceResolverV1, c.PriceEngine.MaxDeviationPct)
	priceEngineV1.jobs = make(map[string]*priceAnchorJob)
	priceEngineV1.dexJobs = make(map[string]*priceDEXJob)
	priceEngineV1.backfillSlots = make(chan struct{}, 2)
	priceEngineV1.Unlock()
	log.Info().Str("root", paths.Root).Msg("price_engine_ready")
}

func registerPriceEngineRoutes(api *gin.RouterGroup) {
	api.GET("/price/health", handlePriceEngineHealth)
	api.GET("/price/history/point", handlePriceHistoryPoint)
	api.GET("/price/history/candles", handlePriceHistoryCandles)
	api.GET("/price/coverage", handlePriceCoverage)
	api.POST("/price/value/batch", handlePriceValueBatch)
	api.POST("/price/backfill/anchor", handlePriceAnchorBackfill)
	api.GET("/price/backfill/jobs/:id", handlePriceBackfillJob)
	api.POST("/price/backfill/dex", handlePriceDEXBackfill)
	api.GET("/price/backfill/dex/jobs/:id", handlePriceDEXBackfillJob)
	api.POST("/price/gaps/repair", handlePriceGapRepair)
	api.GET("/price/pools", handlePricePools)
	api.POST("/price/pools/discover", handlePricePoolDiscovery)
	api.POST("/price/rebuild", handlePriceRebuild)
}

func handlePriceEngineHealth(c *gin.Context) {
	priceEngineV1.RLock()
	engineReady := priceEngineV1.importer != nil
	root := priceEngineV1.paths.Root
	priceEngineV1.RUnlock()
	if !engineReady || clickHouseClient == nil {
		c.JSON(http.StatusServiceUnavailable, pricing.EngineHealth{Status: "unavailable", ClickHouse: "unavailable", Root: root})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	db := clickHouseClient.Health(ctx)
	status, clickhouseStatus := "ok", "ok"
	if !db.Healthy {
		status, clickhouseStatus = "degraded", "unavailable"
	}
	c.JSON(http.StatusOK, pricing.EngineHealth{Status: status, ClickHouse: clickhouseStatus, Root: root, Providers: []pricing.ProviderState{
		{Name: "binance", Status: "configured"}, {Name: "sqd", Status: "shared_orchestrator"},
		{Name: "aws", Status: "shared_orchestrator"}, {Name: "rpc", Status: "shared_orchestrator"},
	}})
}

func priceChain(c *gin.Context) (uint32, bool) {
	chain := strings.ToLower(strings.TrimSpace(c.Query("chain")))
	if chain == "" || chain == "bsc" || chain == "56" {
		return 56, true
	}
	c.JSON(http.StatusBadRequest, gin.H{"detail": "price engine V1 supports BSC only"})
	return 0, false
}

func handlePriceHistoryPoint(c *gin.Context) {
	chain, ok := priceChain(c)
	if !ok {
		return
	}
	token := strings.TrimSpace(c.Query("token"))
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(c.Query("timestamp")))
	if err != nil || token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "token and RFC3339 timestamp are required"})
		return
	}
	if priceResolverV1 == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "price resolver is unavailable"})
		return
	}
	price, err := priceResolverV1.ResolvePrice(c.Request.Context(), uint64(chain), token, at)
	if errors.Is(err, pricing.ErrPriceMissing) {
		priceEngineV1.RLock()
		repository := priceEngineV1.repository
		priceEngineV1.RUnlock()
		if repository != nil {
			if auditErr := repository.LogResolution(c.Request.Context(), pricing.ResolutionAudit{ChainID: chain, TokenAddress: token, Timestamp: at.UTC(), Status: "UNKNOWN", Reason: "PRICE_MISSING"}); auditErr != nil {
				log.Warn().Err(auditErr).Msg("price_resolution_audit_failed")
			}
		}
		c.JSON(http.StatusOK, gin.H{"chain": "bsc", "token": strings.ToLower(token), "timestamp": at.UTC(), "price_usd": nil, "status": "MISSING", "price_type": "UNKNOWN"})
		return
	}
	if err != nil {
		writeFinancialError(c, err)
		return
	}
	priceType := "TRADED"
	if price.IsFallback {
		priceType = "LAST_KNOWN_OR_FALLBACK"
	}
	priceEngineV1.RLock()
	repository := priceEngineV1.repository
	priceEngineV1.RUnlock()
	if repository != nil {
		resolved := price.PriceUSD
		if auditErr := repository.LogResolution(c.Request.Context(), pricing.ResolutionAudit{ChainID: chain, TokenAddress: price.TokenID, Timestamp: at.UTC(), ResolvedPrice: &resolved, Route: price.Source, Confidence: confidenceValue(price.Confidence), Status: "RESOLVED", Reason: priceType}); auditErr != nil {
			log.Warn().Err(auditErr).Msg("price_resolution_audit_failed")
		}
	}
	c.JSON(http.StatusOK, gin.H{"chain": "bsc", "token": price.TokenID, "timestamp": at.UTC(), "price_usd": price.PriceUSD, "source": price.Source, "confidence": price.Confidence, "price_type": priceType, "price_timestamp": price.PriceTime, "age_seconds": uint64(absSeconds(at.Sub(price.PriceTime)))})
}

func handlePriceHistoryCandles(c *gin.Context) {
	chain, ok := priceChain(c)
	if !ok {
		return
	}
	from, err1 := time.Parse(time.RFC3339, strings.TrimSpace(c.Query("start")))
	to, err2 := time.Parse(time.RFC3339, strings.TrimSpace(c.Query("end")))
	interval := pricing.Resolution(strings.TrimSpace(defaultString(c.Query("interval"), "1m")))
	priceEngineV1.RLock()
	config, anchors, bars := priceEngineV1.config, priceEngineV1.anchors, priceEngineV1.bars
	priceEngineV1.RUnlock()
	if err1 != nil || err2 != nil || !to.After(from) || to.Sub(from) > config.MaxQueryWindow || anchors == nil || bars == nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid or excessive candle range"})
		return
	}
	token := strings.TrimSpace(c.Query("token"))
	symbol := strings.ToUpper(token)
	var result []pricing.Candle
	var err error
	if strings.HasSuffix(symbol, "USDT") && !strings.HasPrefix(symbol, "0X") {
		result, err = anchors.Candles(c.Request.Context(), symbol, from, to, interval)
	} else {
		result, err = bars.Candles(c.Request.Context(), chain, token, from, to, interval)
	}
	if err != nil {
		writeFinancialError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"chain": "bsc", "token": token, "interval": interval, "candles": result})
}

func handlePriceCoverage(c *gin.Context) {
	chain, ok := priceChain(c)
	if !ok {
		return
	}
	token := strings.TrimSpace(c.Query("token"))
	priceEngineV1.RLock()
	repository := priceEngineV1.repository
	priceEngineV1.RUnlock()
	if token == "" || repository == nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "token is required"})
		return
	}
	coverage, err := repository.Coverage(c.Request.Context(), chain, token)
	if err != nil {
		writeFinancialError(c, err)
		return
	}
	if coverage == nil {
		c.JSON(http.StatusOK, gin.H{"token": strings.ToLower(token), "covered": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"covered": true, "coverage": coverage})
}

type priceValueBatchRequest struct {
	Items []struct{ Token, Amount, Timestamp string } `json:"items"`
}

func handlePriceValueBatch(c *gin.Context) {
	chain, ok := priceChain(c)
	if !ok {
		return
	}
	priceEngineV1.RLock()
	limit := priceEngineV1.config.MaxBatchItems
	priceEngineV1.RUnlock()
	var request priceValueBatchRequest
	if err := c.ShouldBindJSON(&request); err != nil || len(request.Items) == 0 || len(request.Items) > limit {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid price batch"})
		return
	}
	results := make([]gin.H, 0, len(request.Items))
	for index, item := range request.Items {
		at, err := time.Parse(time.RFC3339, item.Timestamp)
		amount, ok := new(big.Rat).SetString(item.Amount)
		if err != nil || !ok || amount.Sign() < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid batch item", "index": index})
			return
		}
		price, err := priceResolverV1.ResolvePrice(c.Request.Context(), uint64(chain), item.Token, at)
		if errors.Is(err, pricing.ErrPriceMissing) {
			results = append(results, gin.H{"token": strings.ToLower(item.Token), "amount": item.Amount, "timestamp": at, "price_usd": nil, "value_usd": nil, "status": "MISSING"})
			continue
		}
		if err != nil {
			writeFinancialError(c, err)
			return
		}
		priceRat, ok := new(big.Rat).SetString(price.PriceUSD)
		if !ok {
			c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "stored price is invalid"})
			return
		}
		value := new(big.Rat).Mul(amount, priceRat)
		results = append(results, gin.H{"token": price.TokenID, "amount": item.Amount, "timestamp": at, "price_usd": price.PriceUSD, "value_usd": value.FloatString(18), "source": price.Source, "confidence": price.Confidence, "status": "AVAILABLE"})
	}
	c.JSON(http.StatusOK, gin.H{"items": results})
}

func handlePriceAnchorBackfill(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request struct{ Symbol, Month string }
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid backfill request"})
		return
	}
	symbol := strings.ToUpper(strings.TrimSpace(request.Symbol))
	month, err := time.Parse("2006-01", strings.TrimSpace(request.Month))
	if err != nil || month.Before(time.Date(2017, 7, 1, 0, 0, 0, 0, time.UTC)) || month.After(time.Now().UTC()) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "month must use YYYY-MM"})
		return
	}
	priceEngineV1.Lock()
	if priceEngineV1.importer == nil {
		priceEngineV1.Unlock()
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "price importer is unavailable"})
		return
	}
	for _, job := range priceEngineV1.jobs {
		if job.Symbol == symbol && job.Month == request.Month && job.Status == "RUNNING" {
			response := *job
			priceEngineV1.Unlock()
			c.JSON(http.StatusAccepted, &response)
			return
		}
	}
	select {
	case priceEngineV1.backfillSlots <- struct{}{}:
	default:
		priceEngineV1.Unlock()
		c.JSON(http.StatusTooManyRequests, gin.H{"detail": "price backfill concurrency limit reached"})
		return
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	job := &priceAnchorJob{ID: id, Symbol: symbol, Month: request.Month, Status: "RUNNING", CreatedAt: now, UpdatedAt: now}
	priceEngineV1.jobs[id] = job
	importer := priceEngineV1.importer
	priceEngineV1.Unlock()
	response := *job
	go func() {
		defer func() { <-priceEngineV1.backfillSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		result, runErr := importer.ImportMonth(ctx, symbol, month)
		priceEngineV1.Lock()
		defer priceEngineV1.Unlock()
		stored := priceEngineV1.jobs[id]
		stored.UpdatedAt = time.Now().UTC()
		if runErr != nil {
			stored.Status = "FAILED"
			stored.Error = runErr.Error()
			log.Warn().Err(runErr).Str("symbol", symbol).Str("month", request.Month).Msg("price_anchor_backfill_failed")
			return
		}
		stored.Status = "COMPLETED"
		stored.Result = &result
	}()
	c.JSON(http.StatusAccepted, &response)
}

func handlePriceBackfillJob(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid job id"})
		return
	}
	priceEngineV1.RLock()
	job := priceEngineV1.jobs[id]
	if job == nil {
		priceEngineV1.RUnlock()
		c.JSON(http.StatusNotFound, gin.H{"detail": "job not found"})
		return
	}
	response := *job
	if job.Result != nil {
		result := *job.Result
		response.Result = &result
	}
	priceEngineV1.RUnlock()
	c.JSON(http.StatusOK, &response)
}

type priceDEXBackfillRequest struct {
	Token     string   `json:"token"`
	Pools     []string `json:"pools"`
	FromBlock uint64   `json:"from_block"`
	ToBlock   uint64   `json:"to_block"`
	From      string   `json:"from"`
	To        string   `json:"to"`
	Refresh   bool     `json:"refresh"`
}

func handlePriceDEXBackfill(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request priceDEXBackfillRequest
	if err := c.ShouldBindJSON(&request); err != nil || len(request.Pools) == 0 || len(request.Pools) > 20 || request.ToBlock < request.FromBlock || request.ToBlock-request.FromBlock > 5_000_000 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid DEX backfill request"})
		return
	}
	startPriceDEXBackfill(c, request)
}

func handlePriceGapRepair(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request priceDEXBackfillRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.ToBlock < request.FromBlock || request.ToBlock-request.FromBlock > 5_000_000 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid price gap repair request"})
		return
	}
	token, err := pricing.CanonicalTokenID(56, request.Token)
	if err != nil || strings.HasPrefix(token, "native:") {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid token"})
		return
	}
	priceEngineV1.RLock()
	dex := priceEngineV1.dex
	priceEngineV1.RUnlock()
	if dex == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "price pool registry is unavailable"})
		return
	}
	pools, err := dex.PoolsForToken(c.Request.Context(), token)
	if err != nil {
		writeFinancialError(c, err)
		return
	}
	if len(pools) == 0 {
		c.JSON(http.StatusConflict, gin.H{"detail": "no verified pool is registered for this token; run pool discovery first"})
		return
	}
	if len(pools) > 20 {
		pools = pools[:20]
	}
	request.Token = token
	request.Pools = make([]string, 0, len(pools))
	for _, pool := range pools {
		request.Pools = append(request.Pools, pool.PoolAddress)
	}
	startPriceDEXBackfill(c, request)
}

func startPriceDEXBackfill(c *gin.Context, request priceDEXBackfillRequest) {
	token, err := pricing.CanonicalTokenID(56, request.Token)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid token"})
		return
	}
	from, err1 := time.Parse(time.RFC3339, request.From)
	to, err2 := time.Parse(time.RFC3339, request.To)
	priceEngineV1.RLock()
	window, service := priceEngineV1.config.MaxQueryWindow, priceEngineV1.rebuild
	priceEngineV1.RUnlock()
	if err1 != nil || err2 != nil || !to.After(from) || to.Sub(from) > window || service == nil || smartDownloadService == nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid DEX time range or orchestrator unavailable"})
		return
	}
	pools := make([]string, 0, len(request.Pools))
	seen := map[string]struct{}{}
	for _, pool := range request.Pools {
		address, canonicalErr := pricing.CanonicalTokenID(56, pool)
		if canonicalErr != nil || strings.HasPrefix(address, "native:") {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid pool address"})
			return
		}
		if _, ok := seen[address]; !ok {
			seen[address] = struct{}{}
			pools = append(pools, address)
		}
	}
	skip := !request.Refresh
	batch, err := smartDownloadService.CreateBatch(c.Request.Context(), smartdownload.CreateBatchRequest{ChainKey: "bsc", Addresses: pools, Datasets: []string{smartdownload.DatasetLogs}, DefaultRange: &smartdownload.RangeSpec{Mode: smartdownload.RangeModeBlock, FromBlock: request.FromBlock, ToBlock: request.ToBlock}, SkipCovered: &skip})
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "create DEX log backfill failed"})
		return
	}
	if err = smartDownloadService.Start(batch.Batch.ID); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "start DEX log backfill failed"})
		return
	}
	now := time.Now().UTC()
	job := &priceDEXJob{ID: uuid.NewString(), BatchID: batch.Batch.ID, Token: token, Status: "DOWNLOADING", Pools: pools, FromBlock: request.FromBlock, ToBlock: request.ToBlock, From: from.UTC(), To: to.UTC(), CreatedAt: now, UpdatedAt: now}
	priceEngineV1.Lock()
	priceEngineV1.dexJobs[job.ID] = job
	priceEngineV1.Unlock()
	response := *job
	go monitorPriceDEXJob(job.ID, service)
	c.JSON(http.StatusAccepted, &response)
}

func monitorPriceDEXJob(id string, service *pricing.RebuildService) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(6 * time.Hour)
	defer deadline.Stop()
	for {
		select {
		case <-deadline.C:
			priceEngineV1.Lock()
			if job := priceEngineV1.dexJobs[id]; job != nil {
				job.Status = "FAILED"
				job.Error = "DEX backfill deadline exceeded"
				job.UpdatedAt = time.Now().UTC()
			}
			priceEngineV1.Unlock()
			return
		case <-ticker.C:
			priceEngineV1.RLock()
			job := priceEngineV1.dexJobs[id]
			if job == nil {
				priceEngineV1.RUnlock()
				return
			}
			batchID, token, from, to := job.BatchID, job.Token, job.From, job.To
			priceEngineV1.RUnlock()
			batch := smartDownloadService.GetBatch(batchID)
			if batch == nil {
				continue
			}
			if !batch.Status.Terminal() {
				continue
			}
			if batch.Status != smartdownload.BatchCompleted {
				priceEngineV1.Lock()
				job = priceEngineV1.dexJobs[id]
				job.Status = "FAILED"
				job.Error = priceDEXBatchError(batch.Status, batch.Error)
				job.UpdatedAt = time.Now().UTC()
				priceEngineV1.Unlock()
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			result, err := service.Rebuild(ctx, token, from, to)
			cancel()
			priceEngineV1.Lock()
			job = priceEngineV1.dexJobs[id]
			job.UpdatedAt = time.Now().UTC()
			if err != nil {
				job.Status = "FAILED"
				job.Error = err.Error()
			} else if resultError := priceDEXResultError(result); resultError != "" {
				job.Status = "FAILED"
				job.Error = resultError
				job.Result = &result
			} else {
				job.Status = "COMPLETED"
				job.Result = &result
			}
			priceEngineV1.Unlock()
			return
		}
	}
}

func priceDEXResultError(result pricing.RebuildResult) string {
	if result.Pools == 0 {
		return "DEX backfill found no registered pools"
	}
	if result.Swaps == 0 {
		return "DEX backfill produced no Swap events"
	}
	if result.Bars == 0 {
		return "DEX backfill produced Swap events but no price bars"
	}
	return ""
}

func priceDEXBatchError(status smartdownload.BatchStatus, detail string) string {
	detail = strings.TrimSpace(detail)
	if status == smartdownload.BatchPartial {
		if detail == "" {
			return "DEX log backfill is PARTIAL and failed the data-quality gate"
		}
		return "DEX log backfill is PARTIAL: " + detail
	}
	if detail != "" {
		return detail
	}
	return "DEX log backfill did not complete"
}

func handlePriceDEXBackfillJob(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid job id"})
		return
	}
	priceEngineV1.RLock()
	job := priceEngineV1.dexJobs[id]
	if job == nil {
		priceEngineV1.RUnlock()
		c.JSON(http.StatusNotFound, gin.H{"detail": "job not found"})
		return
	}
	response := *job
	if job.Result != nil {
		result := *job.Result
		response.Result = &result
	}
	priceEngineV1.RUnlock()
	c.JSON(http.StatusOK, &response)
}

func handlePricePools(c *gin.Context) {
	token, err := pricing.CanonicalTokenID(56, c.Query("token"))
	if err != nil || strings.HasPrefix(token, "native:") {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "valid token contract is required"})
		return
	}
	priceEngineV1.RLock()
	dex := priceEngineV1.dex
	priceEngineV1.RUnlock()
	if dex == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "price pool registry is unavailable"})
		return
	}
	pools, err := dex.PoolsForToken(c.Request.Context(), token)
	if err != nil {
		writeFinancialError(c, err)
		return
	}
	items := make([]gin.H, 0, len(pools))
	for _, pool := range pools {
		items = append(items, gin.H{"chain_id": pool.ChainID, "protocol_id": pool.ProtocolID, "dex": pool.DEX, "version": pool.Version, "factory_address": pool.FactoryAddress, "pool_address": pool.PoolAddress, "token0": pool.Token0, "token1": pool.Token1, "fee_bps": pool.FeeBPS, "verified": pool.Verified, "liquidity_score": pool.LiquidityScore})
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "pools": items})
}

func handlePricePoolDiscovery(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request struct{ From, To string }
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid pool discovery request"})
		return
	}
	from, err1 := time.Parse(time.RFC3339, request.From)
	to, err2 := time.Parse(time.RFC3339, request.To)
	priceEngineV1.RLock()
	dex, window := priceEngineV1.dex, priceEngineV1.config.MaxQueryWindow
	priceEngineV1.RUnlock()
	if err1 != nil || err2 != nil || !to.After(from) || to.Sub(from) > window || dex == nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid or excessive discovery range"})
		return
	}
	result, err := dex.DiscoverPools(c.Request.Context(), from, to)
	if err != nil {
		writeFinancialError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func handlePriceRebuild(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var request struct{ Token, From, To string }
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid rebuild request"})
		return
	}
	from, err1 := time.Parse(time.RFC3339, request.From)
	to, err2 := time.Parse(time.RFC3339, request.To)
	priceEngineV1.RLock()
	service, window := priceEngineV1.rebuild, priceEngineV1.config.MaxQueryWindow
	priceEngineV1.RUnlock()
	if err1 != nil || err2 != nil || !to.After(from) || to.Sub(from) > window || service == nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid or excessive rebuild range"})
		return
	}
	result, err := service.Rebuild(c.Request.Context(), request.Token, from, to)
	if err != nil {
		writeFinancialError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
func absSeconds(value time.Duration) int64 {
	seconds := int64(value / time.Second)
	if seconds < 0 {
		return -seconds
	}
	return seconds
}
func confidenceValue(value string) float32 {
	switch strings.ToUpper(value) {
	case "HIGH":
		return .95
	case "MEDIUM":
		return .8
	case "LOW":
		return .6
	case "FALLBACK":
		return .5
	default:
		return 0
	}
}
func parsePositiveInt(value string, fallback int) int {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
