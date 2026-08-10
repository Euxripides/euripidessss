package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/entityintel"
)

// setupEntityIntel 装配 Entity Intelligence Layer V1。
func setupEntityIntel() {
	if entityResolver != nil {
		return
	}
	var featureSource entityintel.FeatureSource
	if cfg.Analytics.DataSource != "duckdb" && clickHouseInvestigation != nil {
		featureSource = clickHouseInvestigation
	} else if h, ok := analyticsAPI.(*analyticsapi.Handler); ok && h != nil {
		featureSource = h.Service()
	}
	root := filepath.Join(cfg.RootDir, "backend", "data", "entity-intelligence")
	store := entityintel.NewStore(root)
	resolver, err := entityintel.NewResolver(store, featureSource,
		func(chainKey string) (int64, error) {
			n, err := chain.Resolve(chainKey)
			if err != nil {
				return 0, err
			}
			return n.ID, nil
		},
		entityintel.DefaultKnownEntities())
	if err != nil {
		log.Warn().Err(err).Str("root", root).Msg("entity_intel_resolver_unavailable")
		return
	}
	entityResolver = resolver
	log.Info().Str("root", root).Msg("entity_intelligence_v1_ready")
}

// registerEntityIntelRoutes 注册实体智能 API（设计 §53-§56）。
func registerEntityIntelRoutes(api *gin.RouterGroup) {
	api.GET("/entity/resolve", HandleEntityResolve)
	api.POST("/entity/resolve/batch", HandleEntityResolveBatch)
	api.GET("/entity/:id/graph", HandleEntityGraph)
	api.POST("/entity/labels", HandleEntityManualLabel)
	api.GET("/entity/stats", HandleEntityStats)
	api.POST("/entity/labels/import", HandleEntityLabelImport)
	api.GET("/entity/clusters", HandleEntityClusters)
	api.GET("/entity/search", HandleEntitySearch)
	api.GET("/entity/history/:chain/:address", HandleEntityHistory)
	api.POST("/entity/cross-chain/merge", HandleEntityCrossChainMerge)
	api.GET("/investigations/:id/entity-leads", HandleEntityLeads)
}

// HandleEntityResolve GET /api/entity/resolve?chain=&address=（设计 §53 Case A/B）。
func HandleEntityResolve(c *gin.Context) {
	if entityResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "实体解析器未装配"})
		return
	}
	address := strings.TrimSpace(c.Query("address"))
	if !evmAddressCheck.MatchString(address) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "非法 EVM 地址"})
		return
	}
	chainKey := strings.ToLower(strings.TrimSpace(c.Query("chain")))
	if chainKey == "" {
		chainKey = "bsc"
	}
	res, err := entityResolver.Resolve(c.Request.Context(), chainKey, address, c.Query("investigation_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// HandleEntityResolveBatch POST /api/entity/resolve/batch（设计 §54）。
func HandleEntityResolveBatch(c *gin.Context) {
	if entityResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "实体解析器未装配"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
	var body struct {
		ChainKey        string   `json:"chain_key"`
		InvestigationID string   `json:"investigation_id"`
		Addresses       []string `json:"addresses"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if len(body.Addresses) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "addresses 不能为空"})
		return
	}
	valid := make([]string, 0, len(body.Addresses))
	for _, a := range body.Addresses {
		a = strings.ToLower(strings.TrimSpace(a))
		if evmAddressCheck.MatchString(a) {
			valid = append(valid, a)
		}
	}
	if len(valid) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "没有合法 EVM 地址"})
		return
	}
	chainKey := strings.ToLower(strings.TrimSpace(body.ChainKey))
	if chainKey == "" {
		chainKey = "bsc"
	}
	results := entityResolver.ResolveBatch(c.Request.Context(), chainKey, valid, body.InvestigationID, 10000)
	c.JSON(http.StatusOK, gin.H{"total": len(results), "results": results})
}

// HandleEntityGraph GET /api/entity/{id}/graph（设计 §55）。
func HandleEntityGraph(c *gin.Context) {
	if entityResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "实体解析器未装配"})
		return
	}
	chainKey := strings.ToLower(strings.TrimSpace(c.Query("chain")))
	if chainKey == "" {
		chainKey = "bsc"
	}
	out, err := entityResolver.EntityGraph(c.Request.Context(), chainKey, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

// HandleEntityManualLabel POST /api/entity/labels（设计 §45-§46 Case F）。
func HandleEntityManualLabel(c *gin.Context) {
	if entityResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "实体解析器未装配"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		InvestigationID string `json:"investigation_id"`
		ChainKey        string `json:"chain_key"`
		Address         string `json:"address"`
		Label           string `json:"label"`
		Reason          string `json:"reason"`
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
	m, err := entityResolver.AddManualLabel(body.InvestigationID, chainKey, body.Address, body.Label, body.Reason)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"manual_label": m, "detail": "已添加案件标签（不影响全局实体）"})
}

// HandleEntityLeads GET /api/investigations/{id}/entity-leads（设计 §56）。
func HandleEntityLeads(c *gin.Context) {
	if entityResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "实体解析器未装配"})
		return
	}
	leads := entityResolver.Leads(c.Param("id"))
	c.JSON(http.StatusOK, gin.H{"investigation_id": c.Param("id"), "total": len(leads), "leads": leads})
}

// HandleEntityStats GET /api/entity/stats（设计 §71）。
func HandleEntityStats(c *gin.Context) {
	if entityResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "实体解析器未装配"})
		return
	}
	c.JSON(http.StatusOK, entityResolver.Stats())
}

// HandleEntityLabelImport POST /api/entity/labels/import — 外部标签数据集导入（§51）。
func HandleEntityLabelImport(c *gin.Context) {
	if entityResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "实体解析器未装配"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
	var body struct {
		Entries []entityintel.ImportLabelEntry `json:"entries"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	imported, err := entityResolver.ImportLabels(body.Entries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"imported": imported, "total": len(body.Entries)})
}

// HandleEntityClusters GET /api/entity/clusters — 实体聚类列表。
func HandleEntityClusters(c *gin.Context) {
	if entityResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "实体解析器未装配"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"total": len(entityResolver.Clusters()), "clusters": entityResolver.Clusters()})
}

// HandleEntitySearch GET /api/entity/search?q= — 实体名搜索（§29）。
func HandleEntitySearch(c *gin.Context) {
	if entityResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "实体解析器未装配"})
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "缺少 q 参数"})
		return
	}
	items := entityResolver.SearchEntities(q)
	c.JSON(http.StatusOK, gin.H{"total": len(items), "items": items})
}

// HandleEntityHistory GET /api/entity/{chain}/{address}/history — 标签历史版本重放（§49-§50）。
func HandleEntityHistory(c *gin.Context) {
	if entityResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "实体解析器未装配"})
		return
	}
	chainID, err := strconv.ParseInt(c.Param("chain"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "chain 必须是数字链 ID"})
		return
	}
	address := strings.ToLower(strings.TrimSpace(c.Param("address")))
	if !evmAddressCheck.MatchString(address) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "非法 EVM 地址"})
		return
	}
	items := entityResolver.LabelHistory(chainID, address)
	c.JSON(http.StatusOK, gin.H{"chain_id": chainID, "address": address, "total": len(items), "history": items})
}

// HandleEntityCrossChainMerge POST /api/entity/cross-chain/merge — 跨链实体合并。
func HandleEntityCrossChainMerge(c *gin.Context) {
	if entityResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "实体解析器未装配"})
		return
	}
	out, err := entityResolver.MergeCrossChainEntities()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}
