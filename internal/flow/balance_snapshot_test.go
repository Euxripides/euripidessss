package flow

import (
	"testing"
	"time"
)

// ── 余额快照测试（V2.0 设计 §8/§28：TestBalanceSnapshotContainsBlockNumber）──

func TestBalanceSnapshotSaveAndLatest(t *testing.T) {
	dir := t.TempDir()
	store := NewBalanceSnapshotStore(dir)
	now := time.Now().UTC()

	snap1 := BalanceSnapshot{
		Chain: "bsc", ChainID: 56, Address: "0xabc", BlockNumber: "100",
		CapturedAt: now.Add(-time.Hour), Source: "rpc-1",
		Assets: []AssetBalance{{TokenAddress: "", Symbol: "BNB", Decimals: 18, Balance: "12.5", Status: "success"}},
	}
	snap2 := BalanceSnapshot{
		Chain: "bsc", ChainID: 56, Address: "0xabc", BlockNumber: "150",
		CapturedAt: now, Source: "rpc-2",
		Assets: []AssetBalance{{TokenAddress: "", Symbol: "BNB", Decimals: 18, Balance: "13.5", Status: "success"}},
	}
	key1, err := store.Save(snap1)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	key2, err := store.Save(snap2)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if key1 == key2 {
		t.Fatal("不同时间快照 key 不应相同")
	}
	// 快照必须包含 block number（设计 §8）
	latest, ok := store.Latest("bsc", "0xabc")
	if !ok {
		t.Fatal("Latest 应命中")
	}
	if latest.BlockNumber != "150" {
		t.Fatalf("Latest 应返回最新快照（block=150）, got %s", latest.BlockNumber)
	}
	if latest.Source != "rpc-2" {
		t.Fatalf("Latest 应带 RPC source: %s", latest.Source)
	}
	// 重载（模拟重启）
	store2 := NewBalanceSnapshotStore(dir)
	if _, ok := store2.Latest("bsc", "0xabc"); !ok {
		t.Fatal("重载后 Latest 应命中")
	}
	if got := store2.List("bsc", "0xabc"); len(got) != 2 {
		t.Fatalf("List 应 2 条, got %d", len(got))
	}
}

func TestBalanceSnapshotCompare(t *testing.T) {
	dir := t.TempDir()
	store := NewBalanceSnapshotStore(dir)
	// 快照：BNB 12.5
	_, _ = store.Save(BalanceSnapshot{
		Chain: "bsc", ChainID: 56, Address: "0xabc", BlockNumber: "100",
		CapturedAt: time.Now().UTC().Add(-time.Hour), Source: "rpc-1",
		Assets: []AssetBalance{{Symbol: "BNB", Balance: "12.5", Status: "success"}},
	})
	// 实时：BNB 13.5，USDT 失败（不参与对比）
	current := &AddressAssets{
		Chain: "bsc", ChainID: 56, Address: "0xabc", Source: "rpc-2", Status: AssetFresh,
		Assets: []AssetBalance{
			{Symbol: "BNB", Balance: "13.5", Status: "success"},
			{Symbol: "USDT", Balance: "0", Status: "failed"},
		},
	}
	diffs := store.Compare("bsc", "0xabc", current)
	if len(diffs) != 1 {
		t.Fatalf("应 1 条对比（USDT 失败不参与）, got %d: %+v", len(diffs), diffs)
	}
	d := diffs[0]
	if d.Symbol != "BNB" || d.Change != 1.0 {
		t.Fatalf("BNB 变化量应为 +1.0, got %+v", d)
	}
	if d.ChangePct <= 7.9 || d.ChangePct >= 8.1 {
		t.Fatalf("BNB 变化率应为 ~8%%, got %v", d.ChangePct)
	}
	if d.SnapshotAt == "" {
		t.Fatal("对比应带快照时间")
	}
}

func TestBalanceSnapshotMemoryOnly(t *testing.T) {
	store := NewBalanceSnapshotStore("") // 仅内存
	_, err := store.Save(BalanceSnapshot{Chain: "bsc", Address: "0xabc", CapturedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("内存模式 Save: %v", err)
	}
	if _, ok := store.Latest("bsc", "0xabc"); !ok {
		t.Fatal("内存模式 Latest 应命中")
	}
}
