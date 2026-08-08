package cache

import (
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	if _, err := s.UpsertContext("inv-1", ContextSnapshot{
		ChainID: 56, ChainKey: "bsc", FocusAddress: "0xaaa",
		FromBlock: 100, ToBlock: 200, Tokens: []string{"USDT"}, Goal: "追踪资金沉淀",
		CurrentPath: []string{"0xa", "0xb"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAddress("inv-1", &AddressState{
		Address: "0xBBB", Coverage: 0.5, Certification: "CERTIFIED",
		TxCount: 10, PrefetchStatus: "PREFETCHING",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertCandidate("inv-1", &CandidateSummary{
		Address: "0xCCC", Score: 87.2, Priority: "HOT", Reasons: []string{"top_outflow_counterparty"},
	}); err != nil {
		t.Fatal(err)
	}
	inv := s.Get("inv-1")
	if inv == nil {
		t.Fatal("读取失败")
	}
	if inv.Context.ChainKey != "bsc" || inv.Context.FocusAddress != "0xaaa" {
		t.Fatalf("上下文错误: %+v", inv.Context)
	}
	if inv.Addresses["0xbbb"] == nil || inv.Addresses["0xbbb"].TxCount != 10 {
		t.Fatalf("地址状态错误: %+v", inv.Addresses)
	}
	if inv.PrefetchCandidates["0xccc"] == nil || inv.PrefetchCandidates["0xccc"].Score != 87.2 {
		t.Fatalf("候选错误: %+v", inv.PrefetchCandidates)
	}
}

func TestStoreRejectsUnsafeID(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	if _, err := s.UpsertContext("../evil", ContextSnapshot{}); err == nil {
		t.Fatal("非法 ID 应被拒绝")
	}
}

func TestStoreAddGraphKey(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	inv, err := s.AddGraphKey("inv-1", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.GraphKeys) != 1 {
		t.Fatalf("graph keys 错误: %v", inv.GraphKeys)
	}
	if _, err := s.AddGraphKey("inv-1", "k1"); err != nil {
		t.Fatal(err)
	}
	if len(s.Get("inv-1").GraphKeys) != 1 {
		t.Fatal("重复 graph key 不应追加")
	}
}

func TestStoreDelete(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	_, _ = s.UpsertContext("inv-1", ContextSnapshot{})
	if err := s.Delete("inv-1"); err != nil {
		t.Fatal(err)
	}
	if s.Get("inv-1") != nil {
		t.Fatal("删除后仍可读取")
	}
}

var _ = time.Now

