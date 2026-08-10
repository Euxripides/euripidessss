package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/etl/backend/internal/clickhouseinvestigation"
	"github.com/etl/backend/internal/dynamicinvestigation"
	"github.com/etl/backend/internal/intelligence"
)

var clickHouseInvestigation *clickhouseinvestigation.Repository

func setupClickHouseInvestigation() {
	if clickHouseClient == nil || cfg == nil || !cfg.ClickHouse.Enabled {
		return
	}
	repository, err := clickhouseinvestigation.New(clickHouseClient, 56)
	if err == nil {
		clickHouseInvestigation = repository
	}
}

func repositoryForInvestigation(chainID uint32) (*clickhouseinvestigation.Repository, error) {
	if clickHouseClient == nil {
		return nil, errors.New("ClickHouse is unavailable")
	}
	return clickhouseinvestigation.New(clickHouseClient, chainID)
}

type clickHouseIntelligenceSource struct {
	repository *clickhouseinvestigation.Repository
}

type clickHouseDiscoverySource struct {
	repository *clickhouseinvestigation.Repository
}

func (s *clickHouseDiscoverySource) Flows(ctx context.Context, address string) ([]dynamicinvestigation.FlowSignal, error) {
	if s == nil || s.repository == nil {
		return nil, nil
	}
	edges, err := s.repository.Flows(ctx, address, "")
	if err != nil {
		return nil, err
	}
	result := make([]dynamicinvestigation.FlowSignal, 0, len(edges))
	for _, edge := range edges {
		result = append(result, dynamicinvestigation.FlowSignal{Counterparty: edge.Counterparty, Token: edge.Token, Amount: edge.Amount, Direction: edge.Direction})
	}
	return result, nil
}

func (s *clickHouseDiscoverySource) Profile(ctx context.Context, address string) (*dynamicinvestigation.ProfileSignal, error) {
	if s == nil || s.repository == nil {
		return &dynamicinvestigation.ProfileSignal{}, nil
	}
	profile, err := s.repository.Profile(ctx, address)
	if err != nil {
		return nil, err
	}
	risk, err := s.repository.Risk(ctx, address)
	if err != nil {
		return nil, err
	}
	return &dynamicinvestigation.ProfileSignal{
		IsContract: profile.ContractCount > 0, TxCount: profile.TransactionCount,
		InCount: profile.TotalIn, OutCount: profile.TotalOut, RiskScore: risk.RiskScore,
		Degree: int(profile.TotalIn + profile.TotalOut),
	}, nil
}

func (s *clickHouseIntelligenceSource) Flows(ctx context.Context, address string) ([]intelligence.FundEdge, error) {
	if s == nil || s.repository == nil {
		return nil, nil
	}
	edges, err := s.repository.Flows(ctx, address, "")
	if err != nil {
		return nil, err
	}
	out := make([]intelligence.FundEdge, 0, len(edges))
	for _, edge := range edges {
		block, _ := strconv.ParseUint(edge.Block, 10, 64)
		from, to := address, edge.Counterparty
		if edge.Direction == "incoming" {
			from, to = edge.Counterparty, address
		}
		out = append(out, intelligence.FundEdge{From: from, To: to, Token: edge.Token, Amount: edge.Amount, TxHash: edge.TxHash, Block: block})
	}
	return out, nil
}

func registerClickHouseInvestigationRoutes(api *gin.RouterGroup) {
	base := "/v1/investigation/:chain/address/:address"
	api.GET(base+"/profile", handleClickHouseInvestigationProfile)
	api.GET(base+"/risk", handleClickHouseInvestigationRisk)
	api.GET(base+"/summary", handleClickHouseInvestigationSummary)
	api.GET(base+"/evidence", handleClickHouseInvestigationEvidence)
	api.GET(base+"/full-evidence", handleClickHouseInvestigationFullEvidence)
}

func clickHouseInvestigationScope(c *gin.Context) (*clickhouseinvestigation.Repository, bool) {
	chainID, ok := explorerScope(c)
	if !ok {
		return nil, false
	}
	repository, err := repositoryForInvestigation(chainID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse investigation is unavailable"})
		return nil, false
	}
	return repository, true
}

func handleClickHouseInvestigationProfile(c *gin.Context) {
	repository, ok := clickHouseInvestigationScope(c)
	if !ok {
		return
	}
	result, err := repository.Profile(c.Request.Context(), c.Param("address"))
	writeInvestigationResult(c, result, err)
}

func handleClickHouseInvestigationRisk(c *gin.Context) {
	repository, ok := clickHouseInvestigationScope(c)
	if !ok {
		return
	}
	result, err := repository.Risk(c.Request.Context(), c.Param("address"))
	writeInvestigationResult(c, result, err)
}

func handleClickHouseInvestigationSummary(c *gin.Context) {
	repository, ok := clickHouseInvestigationScope(c)
	if !ok {
		return
	}
	result, err := repository.Investigate(c.Request.Context(), c.Param("address"))
	writeInvestigationResult(c, result, err)
}

func handleClickHouseInvestigationEvidence(c *gin.Context) {
	repository, ok := clickHouseInvestigationScope(c)
	if !ok {
		return
	}
	limit := 1000
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "limit must be an integer"})
			return
		}
		limit = value
	}
	result, err := repository.AddressEvidence(c.Request.Context(), c.Param("address"), limit)
	writeInvestigationResult(c, result, err)
}

func handleClickHouseInvestigationFullEvidence(c *gin.Context) {
	repository, ok := clickHouseInvestigationScope(c)
	if !ok {
		return
	}
	hops, related := 4, 100
	if value, err := strconv.Atoi(c.DefaultQuery("max_hops", "4")); err == nil {
		hops = value
	}
	if value, err := strconv.Atoi(c.DefaultQuery("related_limit", "100")); err == nil {
		related = value
	}
	result, err := repository.Evidence(c.Request.Context(), c.Param("address"), hops, related)
	writeInvestigationResult(c, result, err)
}

func writeInvestigationResult(c *gin.Context, result any, err error) {
	if err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "valid evm") || strings.Contains(message, "limit") || strings.Contains(message, "hop") {
			c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		} else {
			c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "ClickHouse investigation query failed"})
		}
		return
	}
	c.JSON(http.StatusOK, result)
}
