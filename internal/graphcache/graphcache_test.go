package graphcache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/etl/backend/internal/analyticsapi"
)

type fakeFlowSource struct {
	flows   []analyticsapi.FlowEdge
	profile *analyticsapi.Profile
}

func (f *fakeFlowSource) Flows(_ context.Context, _, _ string) ([]analyticsapi.FlowEdge, error) {
	return f.flows, nil
}

func (f *fakeFlowSource) Profile(_ context.Context, _ string) (*analyticsapi.Profile, error) {
	return f.profile, nil
}

type fakeCoverage struct {
	ratio float64
	full  bool
	cert  string
}

func (f *fakeCoverage) QueryCoverage(_, _, _ string, _, _ uint64) CoverageInfo {
	return CoverageInfo{Ratio: f.ratio, Full: f.full, Certification: f.cert}
}

func TestKeyNormalizedAndHashStable(t *testing.T) {
	k1 := Key{ChainID: 56, Address: "0xABC", Direction: "out", DatasetSet: []string{"token_transfers", "transactions"},
		TokenFilter: "USDT", FromBlock: 100, ToBlock: 200, Depth: 2}
	k2 := Key{ChainID: 56, Address: "0xabc", Direction: "OUT", DatasetSet: []string{"transactions", "token_transfers"},
		TokenFilter: "usdt", FromBlock: 100, ToBlock: 200, Depth: 2}
	if k1.Hash() != k2.Hash() {
		t.Fatalf("hash 不稳定: %s != %s", k1.Hash(), k2.Hash())
	}
	if len(k1.Hash()) != 64 {
		t.Fatalf("hash 长度异常: %d", len(k1.Hash()))
	}
}

func TestStorePutGetTTLAndInvalidate(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root, 1000)
	k := Key{ChainID: 56, Address: "0x8894e0a0c962cb723c1976a4421c95949be2d4e3",
		DatasetSet: []string{"token_transfers"}, FromBlock: 100, ToBlock: 200, Depth: 1}
	res := Result{Key: k, CounterpartyCount: 2}
	if err := s.Put(k, res, time.Hour); err != nil {
		t.Fatal(err)
	}
	e := s.Get(k)
	if e == nil || e.Result.CounterpartyCount != 2 {
		t.Fatalf("缓存未命中: %+v", e)
	}
	if s.TTLFor(k) != s.historicalTTL {
		t.Fatalf("历史区间 TTL 错误: %v", s.TTLFor(k))
	}
	recent := Key{ChainID: 56, Address: "0x8894e0a0c962cb723c1976a4421c95949be2d4e3",
		DatasetSet: []string{"token_transfers"}, FromBlock: 900, ToBlock: 1200}
	if s.TTLFor(recent) != s.recentTTL {
		t.Fatalf("近实时区间 TTL 错误: %v", s.TTLFor(recent))
	}
	if n := s.InvalidateDataset(56, k.Address, "token_transfers"); n != 1 {
		t.Fatalf("按 Dataset 失效应删除 1 条，实际 %d", n)
	}
	if e := s.Get(k); e != nil {
		t.Fatal("失效后仍可命中")
	}
}

func TestStoreExpiry(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root, 1000)
	k := Key{ChainID: 56, Address: "0x0000000000000000000000000000000000000001", FromBlock: 1, ToBlock: 2}
	if err := s.Put(k, Result{Key: k}, -time.Second); err != nil {
		t.Fatal(err)
	}
	if e := s.Get(k); e != nil {
		t.Fatal("过期条目仍返回")
	}
	if _, err := os.Stat(k.FilePath(root)); !os.IsNotExist(err) {
		t.Fatalf("过期文件应被删除: %v", err)
	}
}

func TestBuilderAggregatesFlows(t *testing.T) {
	src := &fakeFlowSource{flows: []analyticsapi.FlowEdge{
		{Direction: "outgoing", Token: "0xusdt", Counterparty: "0xbbb", Amount: "10", Block: "100"},
		{Direction: "outgoing", Token: "0xusdt", Counterparty: "0xbbb", Amount: "5", Block: "101"},
		{Direction: "incoming", Token: "0xusdt", Counterparty: "0xccc", Amount: "7", Block: "90"},
	}}
	b := NewBuilder(src, &fakeCoverage{ratio: 0.8, cert: "CERTIFIED"})
	res, err := b.Build(context.Background(), Key{
		ChainID: 56, Address: "0xaaa", Direction: "ALL",
		DatasetSet: []string{"token_transfers"}, FromBlock: 0, ToBlock: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.CounterpartyCount != 2 || len(res.Edges) != 2 {
		t.Fatalf("对手聚合错误: %+v", res.Edges)
	}
	if res.TotalOutflow != "15" || res.TotalInflow != "7" {
		t.Fatalf("总额错误: in=%s out=%s", res.TotalInflow, res.TotalOutflow)
	}
	if res.Coverage != 0.8 || res.Certification != "CERTIFIED" {
		t.Fatalf("覆盖信息错误: %+v", res)
	}
}

func TestCacheHitThenInvalidate(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, 1000)
	src := &fakeFlowSource{flows: []analyticsapi.FlowEdge{
		{Direction: "outgoing", Token: "0xusdt", Counterparty: "0xbbb", Amount: "3"},
	}}
	c := NewCache(store, NewBuilder(src, nil))
	k := Key{ChainID: 56, Address: "0xaaa", DatasetSet: []string{"token_transfers"}, FromBlock: 0, ToBlock: 10}
	res, hit, err := c.GetOrBuild(context.Background(), k)
	if err != nil {
		t.Fatal(err)
	}
	if hit || res.Source != "rebuilt" {
		t.Fatalf("首次应构建: hit=%v source=%s", hit, res.Source)
	}
	res2, hit2, err := c.GetOrBuild(context.Background(), k)
	if err != nil {
		t.Fatal(err)
	}
	if !hit2 || res2.Source != "cache-hit" {
		t.Fatalf("第二次应命中: hit=%v source=%s", hit2, res2.Source)
	}
	if n := store.InvalidateAddress(56, k.Address); n != 1 {
		t.Fatalf("地址失效应删除 1 条，实际 %d", n)
	}
}

func TestMergeIncremental(t *testing.T) {
	base := &Result{
		TotalInflow: "10", TotalOutflow: "20",
		Edges: []Edge{{Counterparty: "0xb", Direction: "IN", Token: "t", Inflow: "10", TxCount: 1}},
		Nodes: []Node{{Address: "0xa", Inflow: "10"}},
	}
	delta := &Result{
		TotalInflow: "5", TotalOutflow: "0",
		Edges: []Edge{{Counterparty: "0xb", Direction: "IN", Token: "t", Inflow: "5", TxCount: 1}},
		Nodes: []Node{{Address: "0xa", Inflow: "5"}},
	}
	merged := Merge(base, delta)
	if merged.TotalInflow != "15" || merged.TotalOutflow != "20" {
		t.Fatalf("增量合并总额错误: %+v", merged)
	}
	if len(merged.Edges) != 1 || merged.Edges[0].TxCount != 2 || merged.Edges[0].Inflow != "15" {
		t.Fatalf("增量合并边错误: %+v", merged.Edges)
	}
	if merged.Source != "merged" {
		t.Fatalf("合并来源标记错误: %s", merged.Source)
	}
}

func TestFilePathShard(t *testing.T) {
	k := Key{ChainID: 56, Address: "0x8894e0a0c962cb723c1976a4421c95949be2d4e3"}
	p := k.FilePath(filepath.Join("root", "cache"))
	if filepath.Base(filepath.Dir(p)) != "0x8894e0a0c962cb723c1976a4421c95949be2d4e3" {
		t.Fatalf("地址目录错误: %s", p)
	}
	if filepath.Base(filepath.Dir(filepath.Dir(p))) != "88" {
		t.Fatalf("分片目录错误: %s", p)
	}
}

var _ = time.Now

