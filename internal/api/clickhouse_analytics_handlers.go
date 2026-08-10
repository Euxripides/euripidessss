package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/etl/backend/internal/clickhouseanalytics"
)

var clickHouseAnalytics *clickhouseanalytics.Repository

func setupClickHouseAnalytics() {
	if clickHouseClient != nil && cfg != nil && cfg.ClickHouse.Enabled {
		clickHouseAnalytics = clickhouseanalytics.NewRepository(clickHouseClient)
	}
}

func registerClickHouseAnalyticsRoutes(api *gin.RouterGroup) {
	api.GET("/v1/analytics/:chain/dashboard", handleClickHouseDashboard)
	api.GET("/v1/analytics/:chain/top-sources", handleClickHouseTopSources)
	api.GET("/v1/analytics/:chain/top-destinations", handleClickHouseTopDestinations)
	api.GET("/v1/analytics/:chain/graph", handleClickHouseGlobalGraph)
	base := "/v1/analytics/:chain/address/:address"
	api.GET(base, handleClickHouseAddressAnalytics)
	api.GET(base+"/all-time", handleClickHouseAllTime)
	api.GET(base+"/in-out", handleClickHouseInOut)
	api.GET(base+"/risk", handleClickHouseRisk)
	api.GET(base+"/paths", handleClickHousePaths)
}

func handleClickHouseDashboard(c *gin.Context) {
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	result, err := clickHouseAnalytics.Dashboard(c.Request.Context(), chainID)
	writeClickHouseAnalyticsResult(c, result, err)
}

func handleClickHouseAddressAnalytics(c *gin.Context) {
	query, ok := clickHouseAddressQuery(c)
	if !ok {
		return
	}
	result, err := clickHouseAnalytics.AddressAnalytics(c.Request.Context(), query)
	writeClickHouseAnalyticsResult(c, result, err)
}

func handleClickHouseAllTime(c *gin.Context) {
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	result, err := clickHouseAnalytics.AllTimeStats(c.Request.Context(), chainID, c.Param("address"))
	writeClickHouseAnalyticsResult(c, result, err)
}

func handleClickHouseInOut(c *gin.Context) {
	query, ok := clickHouseAddressQuery(c)
	if !ok {
		return
	}
	result, err := clickHouseAnalytics.InOutVolume(c.Request.Context(), query)
	writeClickHouseAnalyticsResult(c, result, err)
}

func handleClickHouseTopSources(c *gin.Context)      { handleClickHouseTop(c, true) }
func handleClickHouseTopDestinations(c *gin.Context) { handleClickHouseTop(c, false) }

func handleClickHouseTop(c *gin.Context, sources bool) {
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	limit, ok := integerQuery(c, "limit", 20)
	if !ok {
		return
	}
	var result []clickhouseanalytics.VolumeStat
	var err error
	if sources {
		result, err = clickHouseAnalytics.TopSources(c.Request.Context(), chainID, limit)
	} else {
		result, err = clickHouseAnalytics.TopDestinations(c.Request.Context(), chainID, limit)
	}
	writeClickHouseAnalyticsResult(c, result, err)
}

func handleClickHouseRisk(c *gin.Context) {
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	result, err := clickHouseAnalytics.Risk(c.Request.Context(), chainID, c.Param("address"))
	writeClickHouseAnalyticsResult(c, result, err)
}

func handleClickHousePaths(c *gin.Context) {
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	limit, ok := integerQuery(c, "limit", 100)
	if !ok {
		return
	}
	result, err := clickHouseAnalytics.TwoHopPaths(c.Request.Context(), clickhouseanalytics.PathQuery{ChainID: chainID, Address: c.Param("address"), Limit: limit})
	writeClickHouseAnalyticsResult(c, result, err)
}

func handleClickHouseGlobalGraph(c *gin.Context) {
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	limit, ok := integerQuery(c, "limit", 500)
	if !ok {
		return
	}
	result, err := clickHouseAnalytics.Graph(c.Request.Context(), clickhouseanalytics.GraphQuery{ChainID: chainID, Limit: limit})
	writeClickHouseAnalyticsResult(c, result, err)
}

func clickHouseAddressQuery(c *gin.Context) (clickhouseanalytics.AddressQuery, bool) {
	chainID, ok := explorerScope(c)
	if !ok {
		return clickhouseanalytics.AddressQuery{}, false
	}
	limit, ok := integerQuery(c, "limit", 50)
	if !ok {
		return clickhouseanalytics.AddressQuery{}, false
	}
	parse := func(key string) (time.Time, bool) {
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
	from, ok := parse("from")
	if !ok {
		return clickhouseanalytics.AddressQuery{}, false
	}
	to, ok := parse("to")
	if !ok {
		return clickhouseanalytics.AddressQuery{}, false
	}
	return clickhouseanalytics.AddressQuery{ChainID: chainID, Address: c.Param("address"), From: from, To: to, Limit: limit}, true
}

func writeClickHouseAnalyticsResult(c *gin.Context, result any, err error) {
	if err == nil {
		c.JSON(http.StatusOK, result)
		return
	}
	if errors.Is(err, clickhouseanalytics.ErrInvalidInput) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse analytics query failed"})
}

// handleClickHouseAnalyticsCompatibility preserves the existing frontend API
// while changing its reader to ClickHouse. It returns true when it handled the
// request; callers may use the legacy handler only when it returns false.
func handleClickHouseAnalyticsCompatibility(c *gin.Context) bool {
	if clickHouseAnalytics == nil || cfg == nil || cfg.Analytics.DataSource == "duckdb" {
		return false
	}
	path := strings.Trim(strings.TrimSpace(c.Param("path")), "/")
	chainID := uint32(56)
	if raw := strings.TrimSpace(c.Query("chain_id")); raw != "" {
		value, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "chain_id must be an integer"})
			return true
		}
		chainID = uint32(value)
	}
	ctx := c.Request.Context()
	switch {
	case path == "dashboard":
		result, err := clickHouseAnalytics.Dashboard(ctx, chainID)
		if err != nil {
			writeClickHouseAnalyticsResult(c, nil, err)
			return true
		}
		trend := make([]gin.H, 0, len(result.Trend))
		for _, point := range result.Trend {
			trend = append(trend, gin.H{"block": point.Date, "events": point.Events})
		}
		c.JSON(http.StatusOK, gin.H{"address_count": result.AddressCount, "token_count": result.TokenCount, "transaction_count": result.TransactionCount, "transfer_count": result.TransferCount, "risk_addresses": result.RiskAddresses, "trend": trend})
		return true
	case path == "graph":
		limit, err := strconv.Atoi(c.DefaultQuery("limit", "500"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "limit must be an integer"})
			return true
		}
		if limit > 500 {
			limit = 500
		}
		result, err := clickHouseAnalytics.Graph(ctx, clickhouseanalytics.GraphQuery{ChainID: chainID, Limit: limit})
		writeClickHouseAnalyticsResult(c, result, err)
		return true
	case strings.HasPrefix(path, "address/"):
		parts := strings.Split(path, "/")
		if len(parts) != 3 {
			return false
		}
		address := parts[1]
		switch parts[2] {
		case "risk":
			result, err := clickHouseAnalytics.Risk(ctx, chainID, address)
			if err != nil {
				writeClickHouseAnalyticsResult(c, nil, err)
				return true
			}
			c.JSON(http.StatusOK, gin.H{"risk_score": result.RiskScore, "risk_level": result.RiskLevel, "risk_reason": result.RiskReason, "transaction_frequency": result.TransactionFrequency, "top_holder_ratio": 0, "shared_counterparty_score": result.CounterpartyConcentration, "method": result.Method, "rules": result.Rules})
			return true
		case "path":
			result, err := clickHouseAnalytics.TwoHopPaths(ctx, clickhouseanalytics.PathQuery{ChainID: chainID, Address: address, Limit: 100})
			writeClickHouseAnalyticsResult(c, result, err)
			return true
		}
	}
	return false
}
