package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/fundflow"
	"github.com/etl/backend/internal/reportengine"
)

// registerGraphV3Routes 注册 Graph API V3（设计 §45-§46）。
func registerGraphV3Routes(api *gin.RouterGroup) {
	api.POST("/graph/path-query", HandleGraphPathQuery)
	api.POST("/graph/multi-root", HandleGraphMultiRoot)
	api.POST("/graph/reduction", HandleGraphReduction)
	api.POST("/graph/hypothesis/test", HandleGraphHypothesisTest)
	api.POST("/graph/agent-reason", HandleGraphAgentReason)
}

// HandleGraphAgentReason POST /api/graph/agent-reason — 多根 + 假设 + Copilot 合并推理（§27、§49-5）。
func HandleGraphAgentReason(c *gin.Context) {
	if fundFlowEngine == nil || entityResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "资金流/实体引擎未装配"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		ChainKey  string   `json:"chain_key"`
		Roots     []string `json:"roots"`
		Addresses []string `json:"addresses"`
		Goal      string   `json:"goal"`
		MaxDepth  int      `json:"max_depth"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	chainKey := strings.ToLower(strings.TrimSpace(body.ChainKey))
	if chainKey == "" {
		chainKey = "bsc"
	}
	roots := body.Roots
	if len(roots) < 2 {
		roots = append(roots, body.Addresses...)
	}
	if len(roots) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "至少需要 2 个地址"})
		return
	}
	// 1) 多根合并
	merged := &fundflow.AnalysisResult{
		RootAddress: strings.Join(roots, ","), ChainKey: chainKey, Goal: body.Goal,
		Paths: []*fundflow.Path{}, Profit: []*fundflow.ProfitAttribution{},
		Settlements: []*fundflow.SettlementResult{}, Cashouts: []*fundflow.CashoutResult{},
		Conservation: []*fundflow.ConservationResult{}, Graph: &fundflow.EntityAwareFlowGraph{},
	}
	for _, root := range roots {
		if !evmAddressCheck.MatchString(root) {
			continue
		}
		res, err := fundFlowEngine.Analyze(c.Request.Context(), chainKey, root, "", 0, 0, body.Goal, body.MaxDepth, "")
		if err != nil {
			continue
		}
		merged.Paths = append(merged.Paths, res.Paths...)
		merged.Profit = append(merged.Profit, res.Profit...)
		merged.Settlements = append(merged.Settlements, res.Settlements...)
		merged.Cashouts = append(merged.Cashouts, res.Cashouts...)
		merged.Conservation = append(merged.Conservation, res.Conservation...)
		if res.Graph != nil {
			merged.Graph.Nodes = append(merged.Graph.Nodes, res.Graph.Nodes...)
			merged.Graph.Edges = append(merged.Graph.Edges, res.Graph.Edges...)
		}
	}
	// 2) 假设验证
	entities := map[string]string{}
	entityNames := map[string]string{}
	for _, addr := range roots {
		if res, err := entityResolver.Resolve(c.Request.Context(), chainKey, addr, ""); err == nil && res != nil && res.Entity != nil {
			entities[addr] = res.Entity.ID
			entityNames[res.Entity.ID] = res.Entity.Name
		}
	}
	entitySet := map[string]int{}
	for _, id := range entities {
		entitySet[id]++
	}
	commonEntity := ""
	commonName := ""
	for id, n := range entitySet {
		if n >= len(roots) {
			commonEntity = id
			commonName = entityNames[id]
			break
		}
	}
	// 3) Copilot 建议
	recommendations := recommendationsOf(merged)
	conclusions := []map[string]any{}
	if commonEntity != "" {
		conclusions = append(conclusions, map[string]any{
			"type": "SUPPORTED", "text": fmt.Sprintf("全部根地址属于同一实体：%s", commonName),
			"confidence": 0.8, "evidence": []string{"Entity Intelligence 实体归属一致"},
		})
	} else {
		conclusions = append(conclusions, map[string]any{
			"type": "WEAK", "text": fmt.Sprintf("根地址分属 %d 个实体，未发现共同实体", len(entitySet)),
			"confidence": 0.3, "evidence": []string{"Entity Intelligence 实体归属不一致"},
		})
	}
	if len(merged.Cashouts) > 0 {
		conclusions = append(conclusions, map[string]any{
			"type": "EXCHANGE_LANDING", "text": fmt.Sprintf("发现 %d 个交易所/服务落点", len(merged.Cashouts)),
			"confidence": 0.7, "evidence": []string{"Fund Flow Cashout Detection"},
		})
	}
	if len(merged.Settlements) > 0 {
		conclusions = append(conclusions, map[string]any{
			"type": "SETTLEMENT", "text": fmt.Sprintf("发现 %d 个沉淀候选", len(merged.Settlements)),
			"confidence": 0.6, "evidence": []string{"Fund Flow Settlement Detection"},
		})
	}
	narrative := fmt.Sprintf(
		"Agent 推理：对 %d 个根地址联合分析，共发现 %d 条路径、%d 个落点、%d 个沉淀候选；%s",
		len(roots), len(merged.Paths), len(merged.Cashouts), len(merged.Settlements), conclusions[0]["text"])
	// 可选 LLM 润色（不改变事实；失败回退规则文本）
	if p := reportengine.NewDeepSeekPolisher(); p != nil {
		if polished, err := p.Polish(c.Request.Context(), narrative, nil); err == nil && polished != "" {
			narrative = polished
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"chain_key": chainKey, "roots": roots, "narrative": narrative,
		"conclusions": conclusions, "recommendations": recommendations,
		"merged_summary": summaryOfAgent(merged),
	})
}

func summaryOfAgent(res *fundflow.AnalysisResult) map[string]int {
	nodes, edges := 0, 0
	if res.Graph != nil {
		nodes = len(res.Graph.Nodes)
		edges = len(res.Graph.Edges)
	}
	return map[string]int{
		"paths": len(res.Paths), "cashouts": len(res.Cashouts),
		"settlements": len(res.Settlements), "profit": len(res.Profit),
		"nodes": nodes, "edges": edges,
	}
}

// HandleGraphPathQuery POST /api/graph/path-query — 路径查询器（设计 §13-§14、§45）。
func HandleGraphPathQuery(c *gin.Context) {
	if fundFlowEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "资金流引擎未装配"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		ChainKey    string  `json:"chain_key"`
		RootAddress string  `json:"root_address"`
		Token       string  `json:"token"`
		FromBlock   uint64  `json:"from_block"`
		ToBlock     uint64  `json:"to_block"`
		Goal        string  `json:"goal"`
		MaxDepth    int     `json:"max_depth"`
		Terminal    string  `json:"terminal"`
		MinAmount   float64 `json:"min_amount"`
		MaxHops     int     `json:"max_hops"`
		MustPass    string  `json:"must_pass"`
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
	res, err := fundFlowEngine.Analyze(c.Request.Context(), chainKey, body.RootAddress, body.Token,
		body.FromBlock, body.ToBlock, body.Goal, body.MaxDepth, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	term := strings.ToUpper(strings.TrimSpace(body.Terminal))
	mustPass := strings.ToLower(strings.TrimSpace(body.MustPass))
	maxHops := body.MaxHops
	if maxHops <= 0 {
		maxHops = 6
	}
	var out []*fundflow.Path
	for _, p := range res.Paths {
		if term != "" && term != "ANY" && !strings.Contains(strings.ToUpper(p.TerminalType), term) {
			continue
		}
		if body.MinAmount > 0 {
			if v, ok := parseFloatPath(p.TotalAmount); !ok || v < body.MinAmount {
				continue
			}
		}
		if p.Hops > maxHops {
			continue
		}
		if mustPass != "" {
			found := false
			for _, n := range p.Nodes {
				if strings.EqualFold(n.Address, mustPass) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	c.JSON(http.StatusOK, gin.H{
		"root_address": body.RootAddress, "chain_key": chainKey,
		"total": len(out), "paths": out,
		"coverage": res.Summary["conservation_pass_rate"],
		"recommendations": recommendationsOf(res),
	})
}

// HandleGraphMultiRoot POST /api/graph/multi-root — 多根联合调查（设计 §22-§23、§45）。
func HandleGraphMultiRoot(c *gin.Context) {
	if fundFlowEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "资金流引擎未装配"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		ChainKey  string   `json:"chain_key"`
		Roots     []string `json:"roots"`
		Goal      string   `json:"goal"`
		MaxDepth  int      `json:"max_depth"`
		Token     string   `json:"token"`
		FromBlock uint64   `json:"from_block"`
		ToBlock   uint64   `json:"to_block"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if len(body.Roots) < 2 || len(body.Roots) > 5 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "roots 需 2-5 个地址"})
		return
	}
	chainKey := strings.ToLower(strings.TrimSpace(body.ChainKey))
	if chainKey == "" {
		chainKey = "bsc"
	}
	merged := &fundflow.AnalysisResult{
		RootAddress: strings.Join(body.Roots, ","), ChainKey: chainKey,
		Goal: body.Goal, Paths: []*fundflow.Path{}, Profit: []*fundflow.ProfitAttribution{},
		Settlements: []*fundflow.SettlementResult{}, Cashouts: []*fundflow.CashoutResult{},
		RoundTrips: []*fundflow.RoundTripResult{}, Conservation: []*fundflow.ConservationResult{},
		Graph: &fundflow.EntityAwareFlowGraph{Root: strings.Join(body.Roots, ",")},
	}
	common := map[string]int{}
	for _, root := range body.Roots {
		if !evmAddressCheck.MatchString(root) {
			continue
		}
		res, err := fundFlowEngine.Analyze(c.Request.Context(), chainKey, root, body.Token,
			body.FromBlock, body.ToBlock, body.Goal, body.MaxDepth, "")
		if err != nil {
			continue
		}
		merged.Paths = append(merged.Paths, res.Paths...)
		merged.Profit = append(merged.Profit, res.Profit...)
		merged.Settlements = append(merged.Settlements, res.Settlements...)
		merged.Cashouts = append(merged.Cashouts, res.Cashouts...)
		merged.RoundTrips = append(merged.RoundTrips, res.RoundTrips...)
		merged.Conservation = append(merged.Conservation, res.Conservation...)
		if res.Graph != nil {
			merged.Graph.Nodes = append(merged.Graph.Nodes, res.Graph.Nodes...)
			merged.Graph.Edges = append(merged.Graph.Edges, res.Graph.Edges...)
			merged.Graph.CollapsedEntities += res.Graph.CollapsedEntities
			for _, n := range res.Graph.Nodes {
				common[strings.ToLower(n.Address)]++
			}
		}
	}
	var commonNodes []string
	for addr, n := range common {
		if n >= len(body.Roots) {
			commonNodes = append(commonNodes, addr)
		}
	}
	sort.Strings(commonNodes)
	c.JSON(http.StatusOK, gin.H{
		"root_addresses": body.Roots, "chain_key": chainKey,
		"merged": merged, "common_nodes": commonNodes,
		"common_count": len(commonNodes),
	})
}

// HandleGraphReduction POST /api/graph/reduction — 价值覆盖减噪（设计 §24-§25、§45）。
func HandleGraphReduction(c *gin.Context) {
	if fundFlowEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "资金流引擎未装配"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		ChainKey       string  `json:"chain_key"`
		RootAddress    string  `json:"root_address"`
		Token          string  `json:"token"`
		Goal           string  `json:"goal"`
		MaxDepth       int     `json:"max_depth"`
		ValueCoverage  float64 `json:"value_coverage"`
		FromBlock      uint64  `json:"from_block"`
		ToBlock        uint64  `json:"to_block"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if body.ValueCoverage <= 0 || body.ValueCoverage > 100 {
		body.ValueCoverage = 80
	}
	chainKey := strings.ToLower(strings.TrimSpace(body.ChainKey))
	if chainKey == "" {
		chainKey = "bsc"
	}
	res, err := fundFlowEngine.Analyze(c.Request.Context(), chainKey, body.RootAddress, body.Token,
		body.FromBlock, body.ToBlock, body.Goal, body.MaxDepth, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	if res.Graph == nil {
		c.JSON(http.StatusOK, gin.H{"reduced": res, "collapsed_edges": 0})
		return
	}
	edges := res.Graph.Edges
	total := 0.0
	for _, e := range edges {
		if v, ok := parseFloatPath(e.Amount); ok {
			total += v
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		ai, _ := parseFloatPath(edges[i].Amount)
		aj, _ := parseFloatPath(edges[j].Amount)
		return ai > aj
	})
	kept := []fundflow.EntityAwareEdge{}
	acc := 0.0
	for _, e := range edges {
		if total <= 0 || (acc/total)*100 >= body.ValueCoverage {
			break
		}
		kept = append(kept, e)
		if v, ok := parseFloatPath(e.Amount); ok {
			acc += v
		}
	}
	res.Graph.Edges = kept
	c.JSON(http.StatusOK, gin.H{
		"reduced": res, "collapsed_edges": len(edges) - len(kept),
		"value_coverage": body.ValueCoverage,
	})
}

// HandleGraphHypothesisTest POST /api/graph/hypothesis/test — 假设验证（设计 §15-§16、§32、§45）。
func HandleGraphHypothesisTest(c *gin.Context) {
	if entityResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "实体解析器未装配"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var body struct {
		ChainKey  string   `json:"chain_key"`
		Addresses []string `json:"addresses"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请求体解析失败: " + err.Error()})
		return
	}
	if len(body.Addresses) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "至少需要 2 个地址"})
		return
	}
	chainKey := strings.ToLower(strings.TrimSpace(body.ChainKey))
	if chainKey == "" {
		chainKey = "bsc"
	}
	entities := map[string]string{}
	entityNames := map[string]string{}
	flows := map[string][]analyticsapi.FlowEdge{}
	var analyticsSvc *analyticsapi.Service
	if h, ok := analyticsAPI.(*analyticsapi.Handler); ok && h != nil {
		analyticsSvc = h.Service()
	}
	for _, addr := range body.Addresses {
		addr = strings.ToLower(strings.TrimSpace(addr))
		if !evmAddressCheck.MatchString(addr) {
			continue
		}
		if res, err := entityResolver.Resolve(c.Request.Context(), chainKey, addr, ""); err == nil && res != nil && res.Entity != nil {
			entities[addr] = res.Entity.ID
			entityNames[res.Entity.ID] = res.Entity.Name
		}
		if analyticsSvc != nil {
			if f, err := analyticsSvc.Flows(c.Request.Context(), addr, ""); err == nil {
				flows[addr] = f
			}
		}
	}
	// 共同 Sweep / 共同 Funder
	commonSweep := commonCounterparty(flows, "outgoing")
	commonFunder := commonCounterparty(flows, "incoming")
	entitySet := map[string]int{}
	for _, id := range entities {
		entitySet[id]++
	}
	commonEntityID := ""
	commonEntityName := ""
	for id, n := range entitySet {
		if n >= len(body.Addresses) {
			commonEntityID = id
			commonEntityName = entityNames[id]
			break
		}
	}
	conf := 0.3
	if commonEntityID != "" {
		conf = 0.8
	} else if len(commonSweep) > 0 || len(commonFunder) > 0 {
		conf = 0.5
	}
	c.JSON(http.StatusOK, gin.H{
		"common_entity_id": commonEntityID, "common_entity_name": commonEntityName,
		"common_sweep": commonSweep, "common_funder": commonFunder,
		"entity_count": len(entitySet), "confidence": conf,
		"evidence": []string{
			"Entity Intelligence 实体归属",
			"链上共同 Sweep / Funder 模式（辅助信号，不单独构成实体结论）",
		},
	})
}

func commonCounterparty(flows map[string][]analyticsapi.FlowEdge, dir string) []string {
	counts := map[string]int{}
	for _, list := range flows {
		seen := map[string]bool{}
		for _, f := range list {
			if !strings.EqualFold(f.Direction, dir) {
				continue
			}
			cp := strings.ToLower(strings.TrimSpace(f.Counterparty))
			if cp == "" || seen[cp] {
				continue
			}
			seen[cp] = true
			counts[cp]++
		}
	}
	var out []string
	for cp, n := range counts {
		if n >= len(flows) && n >= 2 {
			out = append(out, cp)
		}
	}
	sort.Strings(out)
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func recommendationsOf(res *fundflow.AnalysisResult) []string {
	var out []string
	if res == nil {
		return out
	}
	for _, p := range res.Paths {
		if p.Hops >= 2 {
			out = append(out, fmt.Sprintf("路径 %s（%d 跳）建议继续展开下游", p.PathType, p.Hops))
			break
		}
	}
	for _, s := range res.Settlements {
		out = append(out, fmt.Sprintf("%s 沉淀候选，建议查看历史来源", s.Address))
		break
	}
	for _, c := range res.Cashouts {
		out = append(out, fmt.Sprintf("%s 落点可作为证据", c.EntityName))
		break
	}
	for _, c := range res.Conservation {
		if !c.Pass {
			out = append(out, fmt.Sprintf("%s 资金守恒异常，建议补洞/校验", c.Address))
			break
		}
	}
	return out
}

func parseFloatPath(s string) (float64, bool) {
	if strings.TrimSpace(s) == "" {
		return 0, false
	}
	var v float64
	if _, err := fmt.Sscanf(s, "%f", &v); err != nil {
		return 0, false
	}
	return v, true
}
