package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/etl/backend/internal/clickhousegraph"
)

var clickHouseGraph *clickhousegraph.Repository

func setupClickHouseGraph() {
	if clickHouseClient != nil && cfg != nil && cfg.ClickHouse.Enabled {
		clickHouseGraph = clickhousegraph.NewRepository(clickHouseClient)
	}
}

func registerClickHouseGraphRoutes(api *gin.RouterGroup) {
	api.GET("/v1/graph/:chain/address/:address", handleClickHouseEgoGraph)
	api.GET("/v1/graph/:chain/address/:address/counterparties", handleClickHouseGraphCounterparties)
}

func handleClickHouseEgoGraph(c *gin.Context) {
	if clickHouseGraph == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse graph is unavailable"})
		return
	}
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	depth, ok := integerQuery(c, "depth", 1)
	if !ok {
		return
	}
	edgeLimit, ok := integerQuery(c, "edge_limit", 200)
	if !ok {
		return
	}
	nodeLimit, ok := integerQuery(c, "node_limit", 200)
	if !ok {
		return
	}
	graph, err := clickHouseGraph.GetEgoGraph(c.Request.Context(), clickhousegraph.EgoQuery{
		ChainID: chainID, RootAddress: c.Param("address"), Depth: depth,
		EdgeLimit: edgeLimit, NodeLimit: nodeLimit, Direction: clickhousegraph.Direction(strings.ToLower(c.DefaultQuery("direction", "all"))),
		TokenAddress: c.Query("token"), ActivityTypes: csvQuery(c.Query("activity_types")),
	})
	writeGraphResult(c, graph, err)
}

func handleClickHouseGraphCounterparties(c *gin.Context) {
	if clickHouseGraph == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse graph is unavailable"})
		return
	}
	chainID, ok := explorerScope(c)
	if !ok {
		return
	}
	limit, ok := integerQuery(c, "limit", 200)
	if !ok {
		return
	}
	edges, err := clickHouseGraph.ListCounterpartyEdges(c.Request.Context(), clickhousegraph.CounterpartyQuery{
		ChainID: chainID, Address: c.Param("address"), Limit: limit,
		Direction:    clickhousegraph.Direction(strings.ToLower(c.DefaultQuery("direction", "all"))),
		TokenAddress: c.Query("token"), ActivityTypes: csvQuery(c.Query("activity_types")),
	})
	writeGraphResult(c, edges, err)
}

func integerQuery(c *gin.Context, key string, fallback int) (int, bool) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": key + " must be an integer"})
		return 0, false
	}
	return value, true
}

func csvQuery(raw string) []string {
	var values []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func writeGraphResult(c *gin.Context, result any, err error) {
	if err == nil {
		c.JSON(http.StatusOK, result)
		return
	}
	if errors.Is(err, clickhousegraph.ErrInvalidInput) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse graph query failed"})
}
