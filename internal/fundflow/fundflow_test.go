package fundflow

import (
	"context"
	"testing"

	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/entityintel"
)

type fakeFlow struct {
	flows map[string][]analyticsapi.FlowEdge
	stats map[string]*analyticsapi.AddressStats
}

func (f *fakeFlow) Flows(_ context.Context, address, _ string) ([]analyticsapi.FlowEdge, error) {
	return f.flows[address], nil
}

func (f *fakeFlow) AddressStats(_ context.Context, address, _ string) (*analyticsapi.AddressStats, error) {
	if s, ok := f.stats[address]; ok {
		return s, nil
	}
	return &analyticsapi.AddressStats{}, nil
}

type fakeEntities struct {
	known map[string]*entityintel.Entity
}

func (f *fakeEntities) Resolve(_ context.Context, _, address, _ string) (*entityintel.Resolution, error) {
	if e, ok := f.known[address]; ok {
		return &entityintel.Resolution{
			Address: address, Entity: e, Confidence: e.Confidence,
			ConfidenceTier: string(entityintel.TierFor(e.Confidence)),
		}, nil
	}
	return &entityintel.Resolution{Address: address, ConfidenceTier: string(entityintel.TierUnverified)}, nil
}

func TestCacheKeyStable(t *testing.T) {
	k1 := CacheKey{Root: "0xAAA", ChainKey: "BSC", TokenScope: "USDT", Goal: "CASHOUT", Depth: 3, ScoringVersion: "v1"}
	k2 := CacheKey{Root: "0xaaa", ChainKey: "bsc", TokenScope: "usdt", Goal: "cashout", Depth: 3, ScoringVersion: "v1"}
	if k1.Hash() != k2.Hash() {
		t.Fatalf("缓存键不稳定: %s != %s", k1.Hash(), k2.Hash())
	}
}

func TestCacheRoundTrip(t *testing.T) {
	c := NewCache(t.TempDir())
	k := CacheKey{Root: "0xaaa", ChainKey: "bsc", Depth: 2, ScoringVersion: "v1"}
	res := &AnalysisResult{RootAddress: "0xaaa"}
	if err := c.Put(k, res); err != nil {
		t.Fatal(err)
	}
	got := c.Get(k)
	if got == nil || !got.CacheHit || got.RootAddress != "0xaaa" {
		t.Fatalf("缓存读取失败: %+v", got)
	}
}

func TestPathScoringCashoutHigher(t *testing.T) {
	cashout := &Path{
		RootAddress: "0xa", Nodes: []PathNode{
			{Address: "0xb", InAmount: "1000000000000000000000", EdgeType: EdgeDeposit,
				EntityType: "CEX_DEPOSIT", EntityID: "e1", EdgeTxHash: "0x1"},
		},
		TerminalType: "CEX_DEPOSIT",
	}
	cashout.PathType, _ = classifyPath(cashout.Nodes)
	cashout.Score, cashout.Confidence = scorePath(cashout, "cashout")
	unknown := &Path{
		RootAddress: "0xa", Nodes: []PathNode{
			{Address: "0xc", InAmount: "1000000000000000000000", EdgeType: EdgeTokenTransfer},
		},
	}
	unknown.PathType, _ = classifyPath(unknown.Nodes)
	unknown.Score, unknown.Confidence = scorePath(unknown, "cashout")
	if cashout.Score <= unknown.Score {
		t.Fatalf("兑现路径应高于普通路径: %v vs %v", cashout.Score, unknown.Score)
	}
}

func TestProfitNetFlow(t *testing.T) {
	if got := netProfit("1000000000000000000000", "400000000000000000000"); got != "600000000000000000000" {
		t.Fatalf("净流计算错误: %s", got)
	}
}

func TestSettlementDetection(t *testing.T) {
	src := &fakeFlow{stats: map[string]*analyticsapi.AddressStats{
		"0xb": {
			TotalIn: "1000000000000000000000", TotalOut: "100000000000000000000",
			NetFlow: "900000000000000000000", Top1TargetRatio: 0.1,
			Recent30d: 0, FirstSeen: "1000000", LastSeen: "11000000",
		},
	}}
	e := NewEngine(src, &fakeEntities{}, NewCache(t.TempDir()), DefaultConfig())
	g := &EntityAwareFlowGraph{
		Root: "0xa",
		Nodes: []EntityAwareNode{
			{Address: "0xb", EntityID: "e_b", EntityType: "UNKNOWN_SERVICE"},
		},
	}
	settlements := e.detectSettlements(context.Background(), "bsc", "0xa", g, "")
	if len(settlements) == 0 || settlements[0].SettlementScore < 0.5 {
		t.Fatalf("应识别沉淀候选: %+v", settlements)
	}
}

func TestRoundTripDetection(t *testing.T) {
	p := &Path{RootAddress: "0xa", Nodes: []PathNode{
		{Address: "0xb"}, {Address: "0xa"},
	}}
	if len(detectRoundTrips([]*Path{p})) != 1 {
		t.Fatal("应检测回流")
	}
}

func TestConservation(t *testing.T) {
	src := &fakeFlow{stats: map[string]*analyticsapi.AddressStats{
		"0xb": {TotalIn: "1000", TotalOut: "100"},
	}}
	e := NewEngine(src, nil, nil, DefaultConfig())
	g := &EntityAwareFlowGraph{Root: "0xa", Nodes: []EntityAwareNode{{Address: "0xb"}}}
	out := e.conservationCheck(context.Background(), "bsc", g, "")
	if len(out) != 1 || out[0].Pass || out[0].Deviation < 0.5 {
		t.Fatalf("守恒异常未标记: %+v", out)
	}
}

func TestEngineAnalyzeAndCache(t *testing.T) {
	src := &fakeFlow{
		flows: map[string][]analyticsapi.FlowEdge{
			"0xa": {{Direction: "outgoing", Counterparty: "0xb", Token: "0xusdt", Amount: "1000", Block: "100"}},
			"0xb": {{Direction: "outgoing", Counterparty: "0xex", Token: "0xusdt", Amount: "990", Block: "101"}},
		},
		stats: map[string]*analyticsapi.AddressStats{
			"0xa":  {TotalIn: "2000", TotalOut: "1000", NetFlow: "1000"},
			"0xb":  {TotalIn: "1000", TotalOut: "990", NetFlow: "10", Recent30d: 5, Top1SourceRatio: 0.5},
			"0xex": {TotalIn: "990", TotalOut: "0", NetFlow: "990", Recent30d: 0},
		},
	}
	ents := &fakeEntities{known: map[string]*entityintel.Entity{
		"0xex": {ID: "entity_ex", Name: "Exchange X", EntityType: entityintel.EntityExchange, Confidence: 0.97},
	}}
	c := NewCache(t.TempDir())
	e := NewEngine(src, ents, c, DefaultConfig())
	res, err := e.Analyze(context.Background(), "bsc", "0xa", "0xusdt", 0, 0, "cashout", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Graph == nil || len(res.Graph.Nodes) == 0 {
		t.Fatal("实体感知图为空")
	}
	if len(res.Paths) == 0 {
		t.Fatal("未发现路径")
	}
	if res.Paths[0].Nodes[0].BlockNumber == 0 {
		t.Fatal("路径节点应带区块号")
	}
	hasCashout := false
	for _, p := range res.Paths {
		if p.PathType == "MULTI_HOP_CASHOUT" {
			hasCashout = true
		}
	}
	if !hasCashout {
		t.Fatalf("应发现多跳兑现路径: %+v", res.Paths)
	}
	if len(res.Cashouts) == 0 {
		t.Fatal("应生成兑现候选")
	}
	if len(res.Profit) == 0 {
		t.Fatal("应生成获利归因")
	}
	hasL2 := false
	for _, p := range res.Profit {
		if p.Level == "L2" {
			hasL2 = true
		}
	}
	if !hasL2 {
		t.Fatal("应生成 L2 成本基础获利归因")
	}
	// 二次分析缓存命中
	res2, err := e.Analyze(context.Background(), "bsc", "0xa", "0xusdt", 0, 0, "cashout", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res2.CacheHit {
		t.Fatal("二次分析应缓存命中")
	}
}

func TestAssetContinuity(t *testing.T) {
	src := &fakeFlow{
		flows: map[string][]analyticsapi.FlowEdge{
			"0xa": {{Direction: "outgoing", Counterparty: "0xr", Token: "0xusdt", Amount: "1000", Block: "100"}},
			"0xr": {
				{Direction: "incoming", Counterparty: "0xa", Token: "0xusdt", Amount: "1000", Block: "100"},
				{Direction: "outgoing", Counterparty: "0xb", Token: "0xwbnb", Amount: "5", Block: "101"},
			},
		},
		stats: map[string]*analyticsapi.AddressStats{},
	}
	ents := &fakeEntities{known: map[string]*entityintel.Entity{
		"0xr": {ID: "entity_router", Name: "Router", EntityType: entityintel.EntityRouter, Confidence: 0.9},
	}}
	e := NewEngine(src, ents, NewCache(t.TempDir()), DefaultConfig())
	res, err := e.Continuity(context.Background(), "bsc", "0xa", "0xusdt", 0, 0, "cashout", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conversions) == 0 || res.Conversions[0].ToAsset != "0xwbnb" {
		t.Fatalf("未检测到资产转换: %+v", res.Conversions)
	}
	if res.Conversions[0].USDValue != "0" || res.Conversions[0].PriceMethod != "NO_PRICE_SOURCE" {
		t.Fatalf("价格溯源错误: %+v", res.Conversions[0])
	}
	if len(res.Segments) == 0 {
		t.Fatal("连续追踪段为空")
	}
}

func TestEstimateUSDPeg(t *testing.T) {
	src := "LOCAL_PEG_ESTIMATE"
	method := "PEG_ASSUMPTION"
	conf := 0.3
	usd := estimateUSD("0x55d398326f99059ff775485246999027b3197955", "1000000000000000000", &src, &method, &conf)
	if usd != "1000000000000000000" || conf != 0.8 || method != "STABLECOIN_PEG_1USD" {
		t.Fatalf("稳定币锚定估算错误: %s %s %.1f", usd, method, conf)
	}
}
