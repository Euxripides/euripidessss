package fundflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/entityintel"
)

// FlowSource 提供资金流与地址统计（analyticsapi.Service 满足）。
type FlowSource interface {
	Flows(ctx context.Context, address string, token string) ([]analyticsapi.FlowEdge, error)
	AddressStats(ctx context.Context, address string, token string) (*analyticsapi.AddressStats, error)
}

// EntityIntelligence 解析地址实体（entityintel.Resolver 满足）。
type EntityIntelligence interface {
	Resolve(ctx context.Context, chainKey, address, investigationID string) (*entityintel.Resolution, error)
}

// Config 是资金流分析配置。
type Config struct {
	MaxDepth     int
	MaxNodes     int
	TopKPerLayer int
	ScoringVersion string
}

// DefaultConfig 返回默认配置（设计 §34：深度 6、节点 500、每层 Top 10）。
func DefaultConfig() Config {
	return Config{MaxDepth: 6, MaxNodes: 500, TopKPerLayer: 10, ScoringVersion: "v2"}
}

// Engine 是 Fund Flow Intelligence 引擎。
type Engine struct {
	src      FlowSource
	entities EntityIntelligence
	cache    *Cache
	cfg      Config
	now      func() time.Time
}

// NewEngine 创建引擎。
func NewEngine(src FlowSource, entities EntityIntelligence, cache *Cache, cfg Config) *Engine {
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 6
	}
	if cfg.MaxNodes <= 0 {
		cfg.MaxNodes = 500
	}
	if cfg.TopKPerLayer <= 0 {
		cfg.TopKPerLayer = 10
	}
	if cfg.ScoringVersion == "" {
		cfg.ScoringVersion = "v1"
	}
	return &Engine{src: src, entities: entities, cache: cache, cfg: cfg, now: time.Now}
}

// Analyze 运行完整资金流智能分析（缓存命中直接返回）。
func (e *Engine) Analyze(ctx context.Context, chainKey, root, token string, from, to uint64, goal string, depth int, investigationID string) (*AnalysisResult, error) {
	chainKey = strings.ToLower(strings.TrimSpace(chainKey))
	root = strings.ToLower(strings.TrimSpace(root))
	goal = strings.ToLower(strings.TrimSpace(goal))
	if goal == "" {
		goal = "cashout"
	}
	if depth <= 0 {
		depth = e.cfg.MaxDepth
	}
	key := CacheKey{
		Root: root, ChainKey: chainKey, TokenScope: token,
		FromBlock: from, ToBlock: to, Goal: goal, Depth: depth,
		ScoringVersion: e.cfg.ScoringVersion,
	}
	if cached := e.cache.Get(key); cached != nil {
		return cached, nil
	}
	res := &AnalysisResult{
		RootAddress: root, ChainKey: chainKey, Goal: goal,
		GeneratedAt: e.now().UTC(),
	}
	graph, err := e.buildGraph(ctx, chainKey, root, token, depth, investigationID)
	if err != nil {
		return nil, err
	}
	res.Graph = graph
	paths := e.findPaths(ctx, chainKey, root, token, graph, goal, depth, investigationID)
	res.Paths = paths
	res.Profit = e.attributeProfit(ctx, chainKey, root, graph, paths, investigationID)
	res.Settlements = e.detectSettlements(ctx, chainKey, root, graph, investigationID)
	res.Cashouts = e.detectCashouts(paths)
	res.RoundTrips = detectRoundTrips(paths)
	res.Conservation = e.conservationCheck(ctx, chainKey, graph, investigationID)
	res.Summary = summaryOf(res)
	if err := e.cache.Put(key, res); err != nil {
		return nil, fmt.Errorf("fundflow: 缓存写入失败: %w", err)
	}
	return res, nil
}

func summaryOf(res *AnalysisResult) map[string]any {
	cashout := len(res.Cashouts)
	settle := len(res.Settlements)
	profit := len(res.Profit)
	return map[string]any{
		"high_value_paths":       len(res.Paths),
		"cashout_candidates":     cashout,
		"settlement_candidates":  settle,
		"profit_addresses":       profit,
		"round_trips":            len(res.RoundTrips),
		"conservation_pass_rate": conservationPassRate(res.Conservation),
		"collapsed_entities":     res.Graph.CollapsedEntities,
	}
}

func conservationPassRate(items []*ConservationResult) float64 {
	if len(items) == 0 {
		return 0
	}
	pass := 0
	for _, c := range items {
		if c.Pass {
			pass++
		}
	}
	return float64(pass) / float64(len(items))
}
