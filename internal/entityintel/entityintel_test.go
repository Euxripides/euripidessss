package entityintel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/analyticsapi"
)

type fakeFeatureSource struct {
	stats   *analyticsapi.AddressStats
	profile *analyticsapi.Profile
	flows   []analyticsapi.FlowEdge
}

func (f *fakeFeatureSource) AddressStats(_ context.Context, _, _ string) (*analyticsapi.AddressStats, error) {
	return f.stats, nil
}

func (f *fakeFeatureSource) Profile(_ context.Context, _ string) (*analyticsapi.Profile, error) {
	return f.profile, nil
}

func (f *fakeFeatureSource) Flows(_ context.Context, _, _ string) ([]analyticsapi.FlowEdge, error) {
	return f.flows, nil
}

func chainID(_ string) (int64, error) { return 56, nil }

func newTestResolver(t *testing.T) (*Resolver, *Store) {
	t.Helper()
	root := t.TempDir()
	store := NewStore(root)
	r, err := NewResolver(store, &fakeFeatureSource{stats: &analyticsapi.AddressStats{}, profile: &analyticsapi.Profile{}},
		chainID, DefaultKnownEntities())
	if err != nil {
		t.Fatal(err)
	}
	return r, store
}

func TestResolveKnownAddress(t *testing.T) {
	r, _ := newTestResolver(t)
	res, err := r.Resolve(context.Background(), "bsc", "0x8894e0a0c962cb723c1976a4421c95949be2d4e3", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Entity == nil || res.Entity.EntityType != EntityExchange {
		t.Fatalf("已知实体未解析: %+v", res.Entity)
	}
	if res.Confidence < 0.95 || res.ConfidenceTier != string(TierConfirmed) {
		t.Fatalf("置信度错误: %v %s", res.Confidence, res.ConfidenceTier)
	}
	if len(res.Evidence) == 0 || res.Evidence[0].SourceType != string(SourcePublicLabel) {
		t.Fatalf("证据缺失: %+v", res.Evidence)
	}
	// 二次解析应缓存命中（<100ms 目标由内存/文件缓存支撑）
	res2, err := r.Resolve(context.Background(), "bsc", "0x8894e0a0c962cb723c1976a4421c95949be2d4e3", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res2.CacheHit {
		t.Fatal("二次解析应缓存命中")
	}
}

func TestResolveDepositPatternAndCluster(t *testing.T) {
	src := &fakeFeatureSource{
		stats: &analyticsapi.AddressStats{
			TxCount: 20, UniqueUpstream: 15, UniqueDownstream: 2,
			TotalIn: "1000000000000000000000", TotalOut: "990000000000000000000",
			NetFlow: "10000000000000000000", Top1TargetRatio: 0.9,
			Recent30d: 10, FirstSeen: "1000000", LastSeen: "11000000",
		},
		profile: &analyticsapi.Profile{TransactionCount: 20},
		flows: []analyticsapi.FlowEdge{
			{Direction: "incoming", Counterparty: "0x1111111111111111111111111111111111111111", Amount: "1000000000000000000000"},
			{Direction: "outgoing", Counterparty: "0x8894e0a0c962cb723c1976a4421c95949be2d4e3", Token: "0xusdt", Amount: "990000000000000000000", Block: "10000000"},
		},
	}
	store := NewStore(t.TempDir())
	r, err := NewResolver(store, src, chainID, DefaultKnownEntities())
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Resolve(context.Background(), "bsc", "0x2222222222222222222222222222222222222222", "inv-1")
	if err != nil {
		t.Fatal(err)
	}
	hasDeposit := false
	for _, l := range res.Labels {
		if l.Label == "cex_deposit" || l.Label == "exchange_deposit_candidate" {
			hasDeposit = true
		}
	}
	if !hasDeposit {
		t.Fatalf("未识别入金模式: %+v", res.Labels)
	}
	if len(res.ClusterIDs) == 0 {
		t.Fatalf("应形成 COMMON_SWEEP 聚类: %+v", res.ClusterIDs)
	}
	leads := r.Leads("inv-1")
	if len(leads) == 0 || leads[0].LeadType != "EXCHANGE_DEPOSIT" {
		t.Fatalf("应生成交易所入金线索: %+v", leads)
	}
}

func TestManualLabelIsolation(t *testing.T) {
	r, _ := newTestResolver(t)
	addr := "0x3333333333333333333333333333333333333333"
	if _, err := r.AddManualLabel("inv-1", "bsc", addr, "核心获利地址", "案件标注"); err != nil {
		t.Fatal(err)
	}
	withInv, _ := r.Resolve(context.Background(), "bsc", addr, "inv-1")
	found := false
	for _, l := range withInv.Labels {
		if l.Label == "核心获利地址" && l.Scope == ScopeInvestigation {
			found = true
		}
	}
	if !found {
		t.Fatalf("调查作用域标签未返回: %+v", withInv.Labels)
	}
	withoutInv, _ := r.Resolve(context.Background(), "bsc", addr, "")
	for _, l := range withoutInv.Labels {
		if l.Label == "核心获利地址" {
			t.Fatal("调查标签不应污染全局解析")
		}
	}
}

func TestDormancyScore(t *testing.T) {
	f := &AddressFeature{NetRetained: "1000000000000000000000", Inflow: "2000000000000000000000", Recent30d: 0, TxCount: 10}
	if DormancyScore(f) < 0.5 {
		t.Fatalf("沉淀分数过低: %v", DormancyScore(f))
	}
}

func TestConflictStoredNotSilentlyOverwritten(t *testing.T) {
	store := NewStore(t.TempDir())
	_ = store.SaveConflict(&ConflictEntry{
		ID: "conflict_x", Address: "0x4444444444444444444444444444444444444444",
		SourceA: "src_a", SourceB: "src_b", EntityA: "entity_a", EntityB: "entity_b",
		CreatedAt: time.Now().UTC(),
	})
	if len(store.ListConflicts("0x4444444444444444444444444444444444444444")) != 1 {
		t.Fatal("冲突未持久化")
	}
}

func TestBatchResolve(t *testing.T) {
	r, _ := newTestResolver(t)
	results := r.ResolveBatch(context.Background(), "bsc",
		[]string{"0x8894e0a0c962cb723c1976a4421c95949be2d4e3", "0xbad", "0x55d398326f99059ff775485246999027b3197955"}, "", 100)
	if len(results) != 3 {
		t.Fatalf("批量解析数量错误: %d", len(results))
	}
	if results[0].Entity == nil || results[2].Entity == nil {
		t.Fatal("已知实体批量解析失败")
	}
	if !strings.EqualFold(results[1].Address, "0xbad") {
		t.Fatalf("非法地址应保留原样: %+v", results[1])
	}
}

func TestEntityGraph(t *testing.T) {
	r, _ := newTestResolver(t)
	res, _ := r.Resolve(context.Background(), "bsc", "0x8894e0a0c962cb723c1976a4421c95949be2d4e3", "")
	out, err := r.EntityGraph(context.Background(), "bsc", res.Entity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out["entity"] == nil || len(out["addresses"].([]map[string]any)) == 0 {
		t.Fatalf("实体图为空: %+v", out)
	}
}

func TestCrossChainMerge(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	_ = store.SaveEntity(&Entity{ID: "entity_x_bsc", Name: "Exchange X", EntityType: EntityExchange,
		ChainIDs: []int64{56}, Addresses: []string{"0xaaa"}, Confidence: 0.9, Version: 1, CreatedAt: now})
	_ = store.SaveEntity(&Entity{ID: "entity_x_eth", Name: "Exchange X", EntityType: EntityExchange,
		ChainIDs: []int64{1}, Addresses: []string{"0xbbb"}, Confidence: 0.9, Version: 1, CreatedAt: now})
	r, err := NewResolver(store, &fakeFeatureSource{stats: &analyticsapi.AddressStats{}, profile: &analyticsapi.Profile{}},
		chainID, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.MergeCrossChainEntities()
	if err != nil {
		t.Fatal(err)
	}
	if out["merged"].(int) != 1 {
		t.Fatalf("跨链合并数量错误: %+v", out)
	}
	first := r.store.GetEntity("entity_x_bsc")
	if len(first.ChainIDs) != 2 || len(first.Addresses) != 2 {
		t.Fatalf("合并后实体未扩展: %+v", first)
	}
}

func TestLabelHistory(t *testing.T) {
	r, _ := newTestResolver(t)
	_, _ = r.Resolve(context.Background(), "bsc", "0x8894e0a0c962cb723c1976a4421c95949be2d4e3", "")
	history := r.LabelHistory(56, "0x8894e0a0c962cb723c1976a4421c95949be2d4e3")
	if len(history) == 0 {
		t.Fatal("标签历史为空")
	}
	if history[0].Label == "" || history[0].RecordedAt.IsZero() {
		t.Fatalf("标签历史记录不完整: %+v", history[0])
	}
}
