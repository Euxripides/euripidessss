package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/graphcache"
	invcache "github.com/etl/backend/internal/investigation/cache"
	"github.com/etl/backend/internal/investigation/prefetch"
	"github.com/etl/backend/internal/smartdownload"
)

var evmAddressCheck = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// normalizeGraphDirection 在 API 边界把前端方向值统一为 graphcache 规范值，
// 缓存键与 Builder 内部只使用规范值（ALL/IN/OUT）。
//
//	"" / all / both        → ALL（双向）
//	upstream               → IN（进入根地址）
//	downstream             → OUT（从根地址发出）
//	ALL/IN/OUT（任意大小写）→ 对应规范值
//	其他值                 → error（不得静默降级）
func normalizeGraphDirection(value string) (graphcache.Direction, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "all", "both":
		return graphcache.DirectionAll, nil
	case "in", "upstream":
		return graphcache.DirectionIn, nil
	case "out", "downstream":
		return graphcache.DirectionOut, nil
	default:
		return "", fmt.Errorf("非法方向 %q（支持 all/both/upstream/downstream/IN/OUT）", value)
	}
}

// coverageQueryAdapter 将 smartdownload Coverage Index 适配为 graphcache.CoverageQuerier。
type coverageQueryAdapter struct {
	svc *smartdownload.Service
}

func (a *coverageQueryAdapter) QueryCoverage(chainKey, address, dataset string, from, to uint64) graphcache.CoverageInfo {
	if a == nil || a.svc == nil {
		return graphcache.CoverageInfo{}
	}
	r := a.svc.CoverageQuery(chainKey, address, dataset, from, to)
	return graphcache.CoverageInfo{Ratio: r.CoverageRatio, Full: r.FullHit, Certification: r.Certification}
}

// setupInvestigationCacheV2 装配 Investigation Cache V2 + Graph Expansion Cache + Smart Prefetch。
func setupInvestigationCacheV2(svc *smartdownload.Service) {
	if svc == nil {
		return
	}
	var flowSource graphcache.FlowSource
	if h, ok := analyticsAPI.(*analyticsapi.Handler); ok && h != nil {
		flowSource = h.Service()
	}
	if cfg != nil && cfg.Analytics.DataSource != "duckdb" && clickHouseInvestigation != nil {
		flowSource = clickHouseInvestigation
	}
	dataRoot := filepath.Join(cfg.RootDir, "backend", "data")
	graphStore := graphcache.NewStore(filepath.Join(dataRoot, "investigation", "graphcache"), 114_000_000)
	builder := graphcache.NewBuilder(flowSource, &coverageQueryAdapter{svc: svc})
	graphCache = graphcache.NewCache(graphStore, builder)
	investigationCacheStore = invcache.NewStore(filepath.Join(dataRoot, "investigation", "cache"))

	prefetchRoot := filepath.Join(dataRoot, "investigation", "prefetch")
	queue, err := prefetch.NewQueue(prefetchRoot)
	if err != nil {
		log.Warn().Err(err).Str("root", prefetchRoot).Msg("prefetch_queue_unavailable")
		return
	}
	budget, err := prefetch.NewBudgetStore(prefetchRoot, prefetch.DefaultBudget())
	if err != nil {
		log.Warn().Err(err).Msg("prefetch_budget_unavailable")
		return
	}
	feedback, err := prefetch.NewFeedback(prefetchRoot)
	if err != nil {
		log.Warn().Err(err).Msg("prefetch_feedback_unavailable")
		return
	}
	cb := prefetch.BatchCallbacks{
		Create: func(ctx context.Context, req smartdownload.CreateBatchRequest) (*smartdownload.CreateBatchResponse, error) {
			return svc.CreateBatch(ctx, req)
		},
		Start: svc.Start,
		Pause: func(id string) error {
			_, err := svc.PauseBatch(id)
			return err
		},
		Resume: func(id string) error {
			_, err := svc.ResumeBatch(id)
			return err
		},
		BatchStatus: func(id string) (string, bool) {
			b := svc.GetBatch(id)
			if b == nil {
				return "", true
			}
			return string(b.Status), b.Status.Terminal()
		},
		CoverageQuery: (&coverageQueryAdapter{svc: svc}).QueryCoverage,
		ChainID: func(chainKey string) int64 {
			if n, err := chain.Resolve(chainKey); err == nil {
				return n.ID
			}
			return 0
		},
		ActiveUserTasks: func() int {
			count := 0
			for _, b := range svc.ListBatches() {
				if b.Prefetch {
					continue
				}
				if b.Status == smartdownload.BatchRunning || b.Status == smartdownload.BatchCreated {
					count++
				}
			}
			return count
		},
	}
	prefetchManager = prefetch.NewManager(queue, budget, feedback, graphCache, investigationCacheStore, cb, prefetch.DefaultConfig())
	prefetchManager.SetDataRoot(dataRoot)
	prefetchManager.Start()
	log.Info().Str("graph_cache", graphStoreRoot(dataRoot)).Str("prefetch", prefetchRoot).
		Msg("investigation_cache_v2_ready")
}

func graphStoreRoot(dataRoot string) string {
	return filepath.Join(dataRoot, "investigation", "graphcache")
}

// registerInvestigationCacheRoutes 注册预取与图扩展缓存 API。
func registerInvestigationCacheRoutes(api *gin.RouterGroup) {
	api.POST("/graph/expand", HandleGraphExpand)
	api.GET("/investigations/:id/prefetch", HandleInvestigationPrefetch)
	api.POST("/investigations/:id/prefetch/pin", HandleInvestigationPrefetchPin)
	api.POST("/investigations/:id/prefetch/upgrade", HandleInvestigationPrefetchUpgrade)
	api.POST("/investigations/:id/context", HandleInvestigationContext)
	api.GET("/prefetch/stats", HandlePrefetchStats)
}

// HandleGraphExpand POST /api/graph/expand — 图扩展缓存查询 + 预取规划（设计 §46-§47、§62）。
func HandleGraphExpand(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		InvestigationID string `json:"investigation_id"`
		ChainKey        string `json:"chain_key"`
		Address         string `json:"address"`
		Direction       string `json:"direction"`
		Token           string `json:"token"`
		FromBlock       uint64 `json:"from_block"`
		ToBlock         uint64 `json:"to_block"`
		Depth           int    `json:"depth"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if !evmAddressCheck.MatchString(body.Address) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "非法 EVM 地址"})
		return
	}
	chainKey := strings.ToLower(strings.TrimSpace(body.ChainKey))
	if chainKey == "" {
		chainKey = "bsc"
	}
	network, err := chain.Resolve(chainKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	direction, err := normalizeGraphDirection(body.Direction)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	if graphCache == nil || prefetchManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "图扩展缓存未装配"})
		return
	}
	invID := strings.TrimSpace(body.InvestigationID)
	if invID == "" {
		invID = "default"
	}
	snap := invcache.ContextSnapshot{
		ChainID: network.ID, ChainKey: network.Key, FocusAddress: strings.ToLower(body.Address),
		FromBlock: body.FromBlock, ToBlock: body.ToBlock,
		Tokens: []string{body.Token}, UpdatedAt: time.Now().UTC(),
	}
	if cached := investigationCacheStore.Get(invID); cached != nil {
		if snap.FromBlock == 0 && snap.ToBlock == 0 {
			snap.FromBlock = cached.Context.FromBlock
			snap.ToBlock = cached.Context.ToBlock
			snap.Tokens = cached.Context.Tokens
			snap.CurrentPath = cached.Context.CurrentPath
			snap.Goal = cached.Context.Goal
		}
	}
	depth := body.Depth
	if depth <= 0 {
		depth = 1
	}
	key := graphcache.Key{
		ChainID: network.ID, Address: body.Address, Direction: string(direction),
		DatasetSet: prefetch.GraphBundle(), TokenFilter: body.Token,
		FromBlock: snap.FromBlock, ToBlock: snap.ToBlock,
		Depth: depth, AggregationVersion: 2,
	}
	res, hit, err := graphCache.GetOrBuild(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "图扩展构建失败: " + err.Error()})
		return
	}
	_, _ = investigationCacheStore.AddGraphKey(invID, key.Hash())
	candidates := []invcache.CandidateSummary{}
	prefetchScheduled := false
	if snap.ToBlock > snap.FromBlock {
		planned, err := prefetchManager.Plan(c.Request.Context(), invID, network.Key, network.ID, body.Address, snap)
		if err != nil {
			log.Warn().Err(err).Str("inv", invID).Str("address", body.Address).Msg("prefetch_plan_failed")
		} else {
			prefetchScheduled = true
			for _, p := range planned {
				candidates = append(candidates, invcache.CandidateSummary{
					Address: p.Address, ParentAddress: p.ParentAddress, Score: p.Score,
					Priority: string(p.Priority), Reasons: p.Reason,
					RequiredDatasets: p.RequiredDatasets, FromBlock: p.FromBlock, ToBlock: p.ToBlock,
				})
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"result":             res,
		"cache_hit":          hit,
		"prefetch_scheduled": prefetchScheduled,
		"candidates":         candidates,
		"investigation_id":   invID,
	})
}

// HandleInvestigationPrefetch GET /api/investigations/{id}/prefetch — 预取状态（设计 §63）。
func HandleInvestigationPrefetch(c *gin.Context) {
	if prefetchManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "预取管理器未装配"})
		return
	}
	if !investigationExists(c.Param("id")) {
		c.JSON(http.StatusNotFound, gin.H{"detail": "调查不存在: " + c.Param("id")})
		return
	}
	c.JSON(http.StatusOK, prefetchManager.Status(c.Param("id")))
}

// HandleInvestigationPrefetchPin POST /api/investigations/{id}/prefetch/pin — 手工 Pin（设计 §64）。
func HandleInvestigationPrefetchPin(c *gin.Context) {
	if prefetchManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "预取管理器未装配"})
		return
	}
	if !investigationExists(c.Param("id")) {
		c.JSON(http.StatusNotFound, gin.H{"detail": "调查不存在: " + c.Param("id")})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		ChainKey  string `json:"chain_key"`
		ChainID   int64  `json:"chain_id"`
		Address   string `json:"address"`
		Reason    string `json:"reason"`
		FromBlock uint64 `json:"from_block"`
		ToBlock   uint64 `json:"to_block"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if !evmAddressCheck.MatchString(body.Address) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "非法 EVM 地址"})
		return
	}
	if body.ToBlock <= body.FromBlock {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "预取必须提供有界区块范围 (to_block > from_block)"})
		return
	}
	chainKey := strings.ToLower(strings.TrimSpace(body.ChainKey))
	if chainKey == "" {
		chainKey = "bsc"
	}
	network, err := chain.Resolve(chainKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	if body.ChainID == 0 {
		body.ChainID = network.ID
	}
	if strings.TrimSpace(body.Reason) == "" {
		body.Reason = "manual_pin"
	}
	cand, err := prefetchManager.Pin(c.Param("id"), network.Key, body.ChainID, body.Address, body.Reason, body.FromBlock, body.ToBlock)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"candidate": cand, "detail": "已加入 HOT 预取队列"})
}

// HandleInvestigationPrefetchUpgrade POST /api/investigations/{id}/prefetch/upgrade — 点击升级（设计 §53-§54）。
func HandleInvestigationPrefetchUpgrade(c *gin.Context) {
	if prefetchManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "预取管理器未装配"})
		return
	}
	if !investigationExists(c.Param("id")) {
		c.JSON(http.StatusNotFound, gin.H{"detail": "调查不存在: " + c.Param("id")})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		ChainKey string `json:"chain_key"`
		Address  string `json:"address"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if !evmAddressCheck.MatchString(body.Address) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "非法 EVM 地址"})
		return
	}
	chainKey := strings.ToLower(strings.TrimSpace(body.ChainKey))
	if chainKey == "" {
		chainKey = "bsc"
	}
	if err := prefetchManager.Upgrade(c.Param("id"), chainKey, body.Address); err != nil {
		c.JSON(http.StatusConflict, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"detail": "已升级为 Interactive，任务进度保持不变"})
}

// HandleInvestigationContext POST /api/investigations/{id}/context — 保存调查上下文（设计 §39）。
func HandleInvestigationContext(c *gin.Context) {
	if investigationCacheStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "调查缓存未装配"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var snap invcache.ContextSnapshot
	if err := json.NewDecoder(c.Request.Body).Decode(&snap); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if snap.FocusAddress != "" && !evmAddressCheck.MatchString(snap.FocusAddress) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "focus_address 不是合法 EVM 地址"})
		return
	}
	inv, err := investigationCacheStore.UpsertContext(c.Param("id"), snap)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"investigation": inv, "detail": "上下文已保存"})
}

// HandlePrefetchStats GET /api/prefetch/stats — 预取与图缓存指标（设计 §77）。
func HandlePrefetchStats(c *gin.Context) {
	if prefetchManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "预取管理器未装配"})
		return
	}
	out := gin.H{"prefetch": prefetchManager.Stats()}
	if graphCache != nil {
		out["graph_cache"] = graphCache.Store().Stats()
	}
	c.JSON(http.StatusOK, out)
}
