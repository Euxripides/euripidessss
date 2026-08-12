package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/sha3"

	"github.com/etl/backend/internal/canonicalapi"
	"github.com/etl/backend/internal/canonicalregistry"
	"github.com/etl/backend/internal/contractintelligence"
	"github.com/etl/backend/internal/eventdecoder"
	"github.com/etl/backend/internal/pricing"
	"github.com/etl/backend/internal/semanticanalytics"
	"github.com/etl/backend/internal/semanticjobs"
	"github.com/etl/backend/internal/semanticquality"
	"github.com/etl/backend/internal/smartdownload"
)

var (
	canonicalV2          *canonicalapi.Repository
	canonicalRegistryV2  *canonicalregistry.Repository
	semanticQualityV2    *semanticquality.Repository
	semanticAnalyticsV2  *semanticanalytics.Repository
	contractIntelV2      *contractintelligence.Repository
	semanticJobServiceV2 *semanticjobs.Service
)

var tokenAssetFilePattern = regexp.MustCompile(`^0x[0-9a-f]{40}\.png$`)
var canonicalAddressPattern = regexp.MustCompile(`^0x[0-9a-f]{40}$`)

type clickHouseABIRegistry struct {
	client interface {
		QueryJSON(context.Context, string) ([]map[string]any, error)
	}
	cache sync.Map
}

type cachedEventDefinitions struct {
	definitions []eventdecoder.EventDefinition
	expiresAt   time.Time
}

type abiDocumentItem struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Inputs []struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Indexed bool   `json:"indexed"`
	} `json:"inputs"`
}

func (r *clickHouseABIRegistry) LookupEvent(ctx context.Context, q eventdecoder.Query) ([]eventdecoder.EventDefinition, error) {
	contract := strings.ToLower(strings.TrimSpace(q.Contract))
	if r.client == nil || !canonicalAddressPattern.MatchString(contract) || q.ChainID > uint64(^uint32(0)) {
		return nil, errors.New("invalid ABI registry query")
	}
	cacheKey := fmt.Sprintf("%d|%s|%s", q.ChainID, contract, strings.ToLower(q.Topic0))
	if value, ok := r.cache.Load(cacheKey); ok {
		cached := value.(cachedEventDefinitions)
		if time.Now().Before(cached.expiresAt) {
			return append([]eventdecoder.EventDefinition(nil), cached.definitions...), nil
		}
		r.cache.Delete(cacheKey)
	}
	query := fmt.Sprintf("SELECT abi_json,source,is_verified FROM onchain.abi_registry FINAL WHERE chain_id=%d AND contract_address='%s'", q.ChainID, contract)
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, errors.New("ABI registry query failed")
	}
	definitions := make([]eventdecoder.EventDefinition, 0)
	for _, row := range rows {
		var document []abiDocumentItem
		if json.Unmarshal([]byte(fmt.Sprint(row["abi_json"])), &document) != nil {
			continue
		}
		verified := fmt.Sprint(row["is_verified"]) == "true" || fmt.Sprint(row["is_verified"]) == "1"
		sourceText := strings.ToUpper(fmt.Sprint(row["source"]))
		source, confidence := eventdecoder.SourceLocalABI, eventdecoder.ConfidenceLow
		if verified {
			source, confidence = eventdecoder.SourceVerifiedABI, eventdecoder.ConfidenceHigh
		} else if strings.Contains(sourceText, "PROTOCOL") {
			source, confidence = eventdecoder.SourceProtocolABI, eventdecoder.ConfidenceMedium
		}
		for _, item := range document {
			if item.Type != "event" || item.Name == "" {
				continue
			}
			types := make([]string, len(item.Inputs))
			inputs := make([]eventdecoder.Input, len(item.Inputs))
			for i, input := range item.Inputs {
				types[i] = input.Type
				inputs[i] = eventdecoder.Input{Name: input.Name, Type: input.Type, Indexed: input.Indexed}
			}
			signature := item.Name + "(" + strings.Join(types, ",") + ")"
			hasher := sha3.NewLegacyKeccak256()
			_, _ = hasher.Write([]byte(signature))
			topic := "0x" + fmt.Sprintf("%x", hasher.Sum(nil))
			if strings.EqualFold(topic, q.Topic0) {
				definitions = append(definitions, eventdecoder.EventDefinition{Name: item.Name, Signature: signature, Topic0: topic, Inputs: inputs, Source: source, Confidence: confidence})
			}
		}
	}
	r.cache.Store(cacheKey, cachedEventDefinitions{
		definitions: append([]eventdecoder.EventDefinition(nil), definitions...),
		expiresAt:   time.Now().Add(5 * time.Minute),
	})
	return definitions, nil
}

func setupCanonicalV2() {
	if clickHouseClient == nil {
		return
	}
	canonicalV2 = canonicalapi.NewRepository(clickHouseClient)
	canonicalRegistryV2 = canonicalregistry.New(clickHouseClient)
	semanticQualityV2 = semanticquality.NewRepository(clickHouseClient)
	semanticAnalyticsV2 = semanticanalytics.NewRepository(clickHouseClient)
	contractIntelV2 = contractintelligence.NewRepository(clickHouseClient)
	if err := seedCanonicalV2(context.Background()); err != nil {
		log.Warn().Err(err).Msg("canonical_v2_seed_failed")
	}
	log.Info().Msg("canonical_v2_read_plane_ready")
}

func seedCanonicalV2(ctx context.Context) error {
	if canonicalRegistryV2 == nil {
		return errors.New("canonical registry is unavailable")
	}
	seededAt := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	methods := []canonicalregistry.MethodRecord{
		{MethodID: "0xa9059cbb", CanonicalSignature: "transfer(address,uint256)", DisplayName: "Transfer", Source: "ERC20_STANDARD", Confidence: "HIGH", Verified: true, UpdatedAt: seededAt},
		{MethodID: "0x095ea7b3", CanonicalSignature: "approve(address,uint256)", DisplayName: "Approve", Source: "ERC20_STANDARD", Confidence: "HIGH", Verified: true, UpdatedAt: seededAt},
		{MethodID: "0x23b872dd", CanonicalSignature: "transferFrom(address,address,uint256)", DisplayName: "Transfer From", Source: "ERC20_STANDARD", Confidence: "HIGH", Verified: true, UpdatedAt: seededAt},
	}
	for _, method := range methods {
		if err := canonicalRegistryV2.UpsertMethod(ctx, method); err != nil {
			return err
		}
	}
	if err := canonicalRegistryV2.UpsertTokenMetadata(ctx, canonicalregistry.TokenMetadata{
		ChainID: 56, ContractAddress: "0x55d398326f99059ff775485246999027b3197955", Name: "Tether USD", Symbol: "USDT", Decimals: 18,
		TokenStandard: "BEP20", LogoURI: "/assets/tokens/56/0x55d398326f99059ff775485246999027b3197955.png",
		LogoHash: "1c2ecfc8c08a821a4839f2ae0df1d8796a8df233939b537b4e26514fa4f91196", LogoSource: "TRUSTWALLET_ASSETS",
		Verified: true, OfficialWebsite: "https://tether.to", FirstSeenTime: time.Date(2020, 9, 4, 0, 0, 0, 0, time.UTC),
		MetadataSource: "TRUSTWALLET_ASSETS", MetadataConfidence: "HIGH", MetadataUpdatedAt: seededAt, UpdatedAt: seededAt,
	}); err != nil {
		return err
	}
	priceRepository := pricing.NewRepository(clickHouseClient)
	return priceRepository.PutPrices(ctx, []pricing.HistoricalPrice{
		{ChainID: 56, TokenID: "0x55d398326f99059ff775485246999027b3197955", PriceTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), PriceUSD: "1", Source: "PEG_FALLBACK", Confidence: "FALLBACK", Resolution: pricing.Resolution1Day, SourcePriority: 40, IsFallback: true, PriceVersion: "peg-v1", SourceVersion: "static-v1"},
		{ChainID: 56, TokenID: "0x55d398326f99059ff775485246999027b3197955", PriceTime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), PriceUSD: "0.999069126628396", Source: "COINGECKO_HISTORICAL", Confidence: "MEDIUM", Resolution: pricing.Resolution1Day, SourcePriority: 10, IsVerified: true, PriceVersion: "coingecko-v1", SourceVersion: "public-history-v1"},
	})
}

func setupSemanticJobsV2() {
	if cfg == nil || clickHouseClient == nil || smartDownloadService == nil || semanticJobServiceV2 != nil {
		return
	}
	store, err := semanticjobs.NewFileStore(filepath.Join(`E:\database\clickhouse`, "semantic_jobs"))
	if err != nil {
		log.Warn().Err(err).Msg("semantic_jobs_store_unavailable")
		return
	}
	service, err := semanticjobs.NewService(semanticJobStore{file: store, registry: canonicalRegistryV2}, semanticJobRunner{smart: smartDownloadService})
	if err != nil {
		log.Warn().Err(err).Msg("semantic_jobs_service_unavailable")
		return
	}
	semanticJobServiceV2 = service
	if err := service.Recover(); err != nil {
		log.Warn().Err(err).Msg("semantic_jobs_recovery_failed")
	}
	log.Info().Msg("semantic_jobs_ready")
}

type semanticJobStore struct {
	file     semanticjobs.Store
	registry *canonicalregistry.Repository
}

func (s semanticJobStore) Save(job semanticjobs.Job) error {
	if err := s.file.Save(job); err != nil {
		return err
	}
	chainID, ok := chainIDFromKey(job.Request.Chain)
	if !ok || s.registry == nil {
		return errors.New("semantic job registry is unavailable")
	}
	target := job.Request.ParserVersion
	if target == "" {
		target = strings.Join(job.Request.Enrichments, ",")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.registry.UpsertSemanticJob(ctx, canonicalregistry.SemanticJob{
		JobID: job.ID, JobType: string(job.Request.Type), ChainID: chainID, Dataset: job.Request.Dataset,
		FromBlock: job.Request.StartBlock, ToBlock: job.Request.EndBlock, TargetVersion: target, Status: registryJobStatus(job.Status),
		ProcessedRows: job.Progress.Completed, ErrorMessage: job.Error, CreatedAt: job.CreatedAt, StartedAt: job.StartedAt,
		CompletedAt: job.CompletedAt, UpdatedAt: job.UpdatedAt,
	})
}

func registryJobStatus(status semanticjobs.Status) string {
	switch status {
	case semanticjobs.StatusQueued:
		return "PENDING"
	case semanticjobs.StatusCompleted:
		return "SUCCEEDED"
	default:
		return string(status)
	}
}

func (s semanticJobStore) Get(id string) (semanticjobs.Job, error) { return s.file.Get(id) }
func (s semanticJobStore) List() ([]semanticjobs.Job, error)       { return s.file.List() }

type semanticJobRunner struct{ smart *smartdownload.Service }

func (r semanticJobRunner) Reparse(ctx context.Context, job semanticjobs.Job, report semanticjobs.ProgressReporter) error {
	return r.smart.ReparseCertified(ctx, job.Request.Chain, job.Request.Dataset, job.Request.StartBlock, job.Request.EndBlock, job.Request.ParserVersion,
		func(completed, _ uint64, last uint64) error {
			return report(semanticjobs.Progress{Completed: completed, Total: job.Progress.Total, LastBlock: last})
		})
}

func (semanticJobRunner) Reenrich(ctx context.Context, job semanticjobs.Job, report semanticjobs.ProgressReporter) error {
	// Canonical reads resolve registry dimensions live. Re-enrichment therefore
	// validates the immutable fact range and publishes a durable cache-invalidation
	// boundary without invoking a downloader or an external service.
	chainID, ok := chainIDFromKey(job.Request.Chain)
	if !ok || clickHouseClient == nil {
		return errors.New("canonical enrichment source is unavailable")
	}
	query := fmt.Sprintf("SELECT count() AS rows FROM onchain.address_activity FINAL WHERE chain_id=%d AND block_number BETWEEN %d AND %d", chainID, job.Request.StartBlock, job.Request.EndBlock)
	if _, err := clickHouseClient.QueryJSON(ctx, query); err != nil {
		return errors.New("canonical enrichment query failed")
	}
	for _, enrichment := range job.Request.Enrichments {
		if enrichment != semanticjobs.EnrichmentEntityLabels {
			continue
		}
		statement := fmt.Sprintf(`INSERT INTO onchain.address_activity
SELECT a.* REPLACE(
 if(l.label_name!='',l.label_name,a.counterparty_label) AS counterparty_label,
 if(e.entity_type!='',e.entity_type,a.counterparty_entity_type) AS counterparty_entity_type,
 coalesce(l.entity_id,a.counterparty_entity_id) AS counterparty_entity_id,
 if(l.entity_role!='',l.entity_role,a.counterparty_role) AS counterparty_role,
 now64(3) AS ingested_at)
FROM onchain.address_activity AS a FINAL
LEFT JOIN (
 SELECT chain_id,address,
  argMax(label_name,tuple(last_verified,updated_at)) AS label_name,
  argMax(entity_id,tuple(last_verified,updated_at)) AS entity_id,
  argMax(entity_role,tuple(last_verified,updated_at)) AS entity_role
 FROM onchain.address_labels FINAL GROUP BY chain_id,address
) AS l ON a.chain_id=l.chain_id AND a.counterparty_address=l.address
LEFT JOIN onchain.entity_registry AS e FINAL ON l.entity_id=e.entity_id
WHERE a.chain_id=%d AND a.block_number BETWEEN %d AND %d`, chainID, job.Request.StartBlock, job.Request.EndBlock)
		if err := clickHouseClient.Exec(ctx, statement); err != nil {
			return errors.New("entity label re-enrichment failed")
		}
	}
	return report(semanticjobs.Progress{Completed: job.Progress.Total, Total: job.Progress.Total, LastBlock: job.Request.EndBlock})
}

func chainIDFromKey(value string) (uint32, bool) {
	aliases := map[string]uint32{"ethereum": 1, "eth": 1, "bsc": 56, "base": 8453, "arbitrum": 42161}
	value = strings.ToLower(strings.TrimSpace(value))
	if id, ok := aliases[value]; ok {
		return id, true
	}
	n, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, false
	}
	id := uint32(n)
	for _, supported := range aliases {
		if id == supported {
			return id, true
		}
	}
	return 0, false
}

func registerCanonicalV2Routes(api *gin.RouterGroup) {
	api.GET("/v2/explorer/:chain/tx/:tx_hash", handleCanonicalTransaction)
	api.GET("/v2/explorer/:chain/address/:address/activity", handleCanonicalActivity)
	api.GET("/v2/explorer/:chain/token/:address", handleCanonicalToken)
	api.GET("/v2/methods/:method_id", handleCanonicalMethod)
	api.GET("/v2/prices/:chain/token/:address", handleCanonicalPrice)
	api.GET("/v2/data-quality/:chain", handleSemanticQuality)
	api.GET("/v2/contracts/:chain/:address", handleCanonicalContract)
	api.GET("/v2/contracts/:chain/:address/family", handleContractFamily)
	api.GET("/v2/analytics/:chain/address/:address/summary", handleSemanticSummary)
	api.GET("/v2/analytics/:chain/address/:address/counterparties", handleSemanticCounterparties)
	api.GET("/v2/analytics/:chain/address/:address/concentration", handleSemanticConcentration)
	api.GET("/v2/analytics/:chain/address/:address/retention", handleFinancialFlowV1)
	api.GET("/v2/analytics/:chain/address/:address/fast-pass-through", handleFinancialFlowV1)
	api.GET("/v2/analytics/:chain/address/:address/snapshot", handleSemanticSnapshot)
	api.POST("/v2/semantic-jobs", handleSemanticJobSubmit)
	api.GET("/v2/semantic-jobs", handleSemanticJobList)
	api.GET("/v2/semantic-jobs/:id", handleSemanticJobGet)
	api.POST("/v2/semantic-jobs/:id/cancel", handleSemanticJobCancel)
	api.POST("/v2/semantic-jobs/:id/retry", handleSemanticJobRetry)
}

func handleTokenAsset(c *gin.Context) {
	chainID, ok := chainIDFromKey(c.Param("chain"))
	name := strings.ToLower(strings.TrimSpace(c.Param("file")))
	if !ok || !tokenAssetFilePattern.MatchString(name) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid token asset"})
		return
	}
	path := filepath.Join(`E:\database\assets\tokens`, strconv.FormatUint(uint64(chainID), 10), name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		c.JSON(http.StatusNotFound, gin.H{"detail": "token asset not found"})
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("X-Content-Type-Options", "nosniff")
	c.File(path)
}

func canonicalScope(c *gin.Context) (uint32, bool) {
	if canonicalV2 == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "Canonical V2 is unavailable"})
		return 0, false
	}
	id, ok := chainIDFromKey(c.Param("chain"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "unsupported chain"})
	}
	return id, ok
}

func handleCanonicalTransaction(c *gin.Context) {
	id, ok := canonicalScope(c)
	if !ok {
		return
	}
	item, err := canonicalV2.GetTransaction(c.Request.Context(), id, c.Param("tx_hash"))
	if err != nil {
		writeCanonicalError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func handleCanonicalActivity(c *gin.Context) {
	id, ok := canonicalScope(c)
	if !ok {
		return
	}
	limit, err := strconv.Atoi(defaultString(c.Query("limit"), "50"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid limit"})
		return
	}
	items, err := canonicalV2.ListActivity(c.Request.Context(), canonicalapi.ActivityQuery{ChainID: id, Address: c.Param("address"), Limit: limit})
	if err != nil {
		writeCanonicalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func handleCanonicalToken(c *gin.Context) {
	id, ok := canonicalScope(c)
	if !ok {
		return
	}
	item, err := canonicalRegistryV2.GetTokenMetadata(c.Request.Context(), id, c.Param("address"))
	if err != nil {
		writeCanonicalError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func handleCanonicalMethod(c *gin.Context) {
	if canonicalRegistryV2 == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "Canonical registry is unavailable"})
		return
	}
	item, err := canonicalRegistryV2.ResolveMethod(c.Request.Context(), c.Param("method_id"))
	if err != nil {
		writeCanonicalError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func handleCanonicalPrice(c *gin.Context) {
	id, ok := canonicalScope(c)
	if !ok {
		return
	}
	asOf, err := time.Parse(time.RFC3339, c.Query("as_of"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "as_of must be RFC3339"})
		return
	}
	item, err := canonicalRegistryV2.GetTokenPriceAsOf(c.Request.Context(), id, c.Param("address"), asOf)
	if err != nil {
		writeCanonicalError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func handleSemanticQuality(c *gin.Context) {
	id, ok := canonicalScope(c)
	if !ok {
		return
	}
	if semanticQualityV2 == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "Data quality is unavailable"})
		return
	}
	key := strconv.FormatUint(uint64(id), 10)
	if cached, ok := dataQualityCache.Get(key); ok {
		c.JSON(http.StatusOK, cached)
		return
	}
	report, err := semanticQualityV2.Report(c.Request.Context(), id)
	if err != nil {
		writeCanonicalError(c, err)
		return
	}
	dataQualityCache.Set(key, report)
	c.JSON(http.StatusOK, report)
}

func handleCanonicalContract(c *gin.Context) {
	id, ok := canonicalScope(c)
	if !ok {
		return
	}
	item, err := contractIntelV2.GetContract(c.Request.Context(), id, c.Param("address"))
	if err != nil {
		writeCanonicalError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func handleContractFamily(c *gin.Context) {
	id, ok := canonicalScope(c)
	if !ok {
		return
	}
	limit, err := strconv.Atoi(defaultString(c.Query("limit"), "50"))
	if err != nil {
		writeCanonicalError(c, err)
		return
	}
	item, err := contractIntelV2.GetFamily(c.Request.Context(), id, c.Param("address"), limit)
	if err != nil {
		writeCanonicalError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func semanticRange(c *gin.Context) (semanticanalytics.AddressQuery, bool) {
	id, ok := canonicalScope(c)
	if !ok {
		return semanticanalytics.AddressQuery{}, false
	}
	from, err := time.Parse(time.RFC3339, c.Query("from"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "from must be RFC3339"})
		return semanticanalytics.AddressQuery{}, false
	}
	to, err := time.Parse(time.RFC3339, c.Query("to"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "to must be RFC3339"})
		return semanticanalytics.AddressQuery{}, false
	}
	return semanticanalytics.AddressQuery{ChainID: id, Address: c.Param("address"), From: from, To: to}, true
}

func semanticSnapshotRange(c *gin.Context) (semanticanalytics.SnapshotQuery, bool) {
	id, ok := canonicalScope(c)
	if !ok {
		return semanticanalytics.SnapshotQuery{}, false
	}
	from, err := time.Parse(time.RFC3339, c.Query("from"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "from must be RFC3339"})
		return semanticanalytics.SnapshotQuery{}, false
	}
	asOf, err := time.Parse(time.RFC3339, c.Query("as_of"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "as_of must be RFC3339"})
		return semanticanalytics.SnapshotQuery{}, false
	}
	return semanticanalytics.SnapshotQuery{ChainID: id, Address: c.Param("address"), From: from, AsOf: asOf}, true
}

func handleSemanticSummary(c *gin.Context) {
	q, ok := semanticRange(c)
	if !ok {
		return
	}
	item, err := semanticAnalyticsV2.AddressSummaryV2(c, q)
	writeSemanticResult(c, item, err)
}
func handleSemanticConcentration(c *gin.Context) {
	q, ok := semanticRange(c)
	if !ok {
		return
	}
	item, err := semanticAnalyticsV2.Concentration(c, q)
	writeSemanticResult(c, item, err)
}
func handleSemanticCounterparties(c *gin.Context) {
	q, ok := semanticRange(c)
	if !ok {
		return
	}
	limit, err := strconv.Atoi(defaultString(c.Query("limit"), "20"))
	if err != nil {
		writeCanonicalError(c, err)
		return
	}
	item, err := semanticAnalyticsV2.CounterpartiesV2(c, semanticanalytics.CounterpartyQuery{AddressQuery: q, Limit: limit})
	writeSemanticResult(c, item, err)
}
func handleSemanticRetention(c *gin.Context) {
	q, ok := semanticSnapshotRange(c)
	if !ok {
		return
	}
	item, err := semanticAnalyticsV2.Retention(c, q)
	writeSemanticResult(c, item, err)
}
func handleSemanticPassThrough(c *gin.Context) {
	q, ok := semanticSnapshotRange(c)
	if !ok {
		return
	}
	item, err := semanticAnalyticsV2.FastPassThrough(c, q)
	writeSemanticResult(c, item, err)
}
func handleSemanticSnapshot(c *gin.Context) {
	q, ok := semanticSnapshotRange(c)
	if !ok {
		return
	}
	item, err := semanticAnalyticsV2.HistoricalSnapshot(c, q)
	writeSemanticResult(c, item, err)
}

func writeSemanticResult(c *gin.Context, value any, err error) {
	if err != nil {
		writeCanonicalError(c, err)
		return
	}
	c.JSON(http.StatusOK, value)
}
func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func handleSemanticJobSubmit(c *gin.Context) {
	if semanticJobServiceV2 == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "Semantic jobs are unavailable"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var req semanticjobs.Request
	if err := decoder.Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid semantic job request"})
		return
	}
	job, created, err := semanticJobServiceV2.Submit(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	c.JSON(status, job)
}
func handleSemanticJobList(c *gin.Context) {
	if semanticJobServiceV2 == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "Semantic jobs are unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": semanticJobServiceV2.List()})
}
func handleSemanticJobGet(c *gin.Context)    { semanticJobAction(c, semanticJobServiceV2.Get) }
func handleSemanticJobCancel(c *gin.Context) { semanticJobAction(c, semanticJobServiceV2.Cancel) }
func handleSemanticJobRetry(c *gin.Context)  { semanticJobAction(c, semanticJobServiceV2.Retry) }
func semanticJobAction(c *gin.Context, fn func(string) (semanticjobs.Job, error)) {
	if semanticJobServiceV2 == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "Semantic jobs are unavailable"})
		return
	}
	job, err := fn(c.Param("id"))
	if errors.Is(err, semanticjobs.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"detail": "semantic job not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "semantic job operation failed"})
		return
	}
	c.JSON(http.StatusOK, job)
}

func writeCanonicalError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, canonicalapi.ErrInvalidInput), errors.Is(err, canonicalregistry.ErrInvalidInput), errors.Is(err, semanticquality.ErrInvalidInput), errors.Is(err, semanticanalytics.ErrInvalidInput), errors.Is(err, contractintelligence.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid canonical request"})
	case errors.Is(err, canonicalapi.ErrNotFound), errors.Is(err, canonicalregistry.ErrNotFound), errors.Is(err, contractintelligence.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"detail": "canonical asset not found"})
	default:
		log.Warn().Err(err).Msg("canonical_v2_request_failed")
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "Canonical V2 query failed"})
	}
}
