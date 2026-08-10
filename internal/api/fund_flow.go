package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/fundflow"
)

// setupFundFlow 装配 Fund Flow Intelligence V2。
func setupFundFlow() {
	if fundFlowEngine != nil {
		return
	}
	var analyticsSvc *analyticsapi.Service
	var flowSource fundflow.FlowSource
	if cfg != nil && cfg.Analytics.DataSource != "duckdb" && clickHouseInvestigation != nil {
		flowSource = clickHouseInvestigation
	}
	if h, ok := analyticsAPI.(*analyticsapi.Handler); ok && h != nil {
		analyticsSvc = h.Service()
		if flowSource == nil {
			flowSource = analyticsSvc
		}
	}
	cacheRoot := filepath.Join(cfg.RootDir, "backend", "data", "fund-flow-cache")
	flowConfig := fundflow.DefaultConfig()
	if flowSource == clickHouseInvestigation {
		flowConfig.ScoringVersion = "clickhouse-v1"
	}
	fundFlowEngine = fundflow.NewEngine(flowSource, entityResolver, fundflow.NewCache(cacheRoot), flowConfig)
	log.Info().Str("cache", cacheRoot).Msg("fund_flow_intelligence_v2_ready")
}

// registerFundFlowRoutes 注册资金流智能 API。
func registerFundFlowRoutes(api *gin.RouterGroup) {
	api.POST("/fund-flow/analyze", HandleFundFlowAnalyze)
	api.POST("/fund-flow/continuity", HandleFundFlowContinuity)
}

// HandleFundFlowContinuity POST /api/fund-flow/continuity — 跨资产连续追踪（设计 §17-§20）。
func HandleFundFlowContinuity(c *gin.Context) {
	if fundFlowEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "资金流智能引擎未装配"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		ChainKey        string `json:"chain_key"`
		RootAddress     string `json:"root_address"`
		Token           string `json:"token"`
		FromBlock       uint64 `json:"from_block"`
		ToBlock         uint64 `json:"to_block"`
		Goal            string `json:"goal"`
		MaxDepth        int    `json:"max_depth"`
		InvestigationID string `json:"investigation_id"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if !evmAddressCheck.MatchString(body.RootAddress) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "root_address 非法"})
		return
	}
	chainKey := strings.ToLower(strings.TrimSpace(body.ChainKey))
	if chainKey == "" {
		chainKey = "bsc"
	}
	res, err := fundFlowEngine.Continuity(c.Request.Context(), chainKey, body.RootAddress, body.Token,
		body.FromBlock, body.ToBlock, body.Goal, body.MaxDepth, body.InvestigationID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// HandleFundFlowAnalyze POST /api/fund-flow/analyze — 资金路径/获利/沉淀/兑现/回流/守恒分析。
func HandleFundFlowAnalyze(c *gin.Context) {
	if fundFlowEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "资金流智能引擎未装配"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		ChainKey        string `json:"chain_key"`
		RootAddress     string `json:"root_address"`
		Token           string `json:"token"`
		FromBlock       uint64 `json:"from_block"`
		ToBlock         uint64 `json:"to_block"`
		Goal            string `json:"goal"`
		MaxDepth        int    `json:"max_depth"`
		InvestigationID string `json:"investigation_id"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if !evmAddressCheck.MatchString(body.RootAddress) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "root_address 不是合法 EVM 地址"})
		return
	}
	chainKey := strings.ToLower(strings.TrimSpace(body.ChainKey))
	if chainKey == "" {
		chainKey = "bsc"
	}
	res, err := fundFlowEngine.Analyze(c.Request.Context(), chainKey, body.RootAddress,
		body.Token, body.FromBlock, body.ToBlock, body.Goal, body.MaxDepth, body.InvestigationID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}
