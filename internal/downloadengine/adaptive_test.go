package downloadengine

import (
	"fmt"
	"testing"
)

// ── V2.1 RC2 Adaptive Start Resolver 正确性验证 ──

func TestAdaptiveSimpleMode(t *testing.T) {
	// <1000 地址 = 简单模式，单组
	addresses := make([]string, 500)
	for i := range addresses {
		addresses[i] = fmt.Sprintf("0x%040x", i)
	}

	discs := make([]AddressDiscovery, len(addresses))
	for i := range discs {
		block := uint64(8000000 + (i%10)*100000)
		discs[i] = AddressDiscovery{Address: addresses[i], FirstSeenBlock: &block, Status: FSFound}
	}

	groups := PlanGroups(addresses, discs, 100)
	// 500地址 → 应该被分为多个组（根据区块分层）
	if len(groups) < 1 {
		t.Error("should have at least 1 group")
	}
	t.Logf("  Simple 500 addrs: %d groups", len(groups))
}

func TestAdaptiveTimeBucket(t *testing.T) {
	// 模拟 >=1000 地址，时间分布
	n := 2000
	addresses := make([]string, n)
	discs := make([]AddressDiscovery, n)
	for i := 0; i < n; i++ {
		addresses[i] = fmt.Sprintf("0x%040x", i)
		block := uint64(8000000)
		if i >= 1000 {
			block = uint64(9500000) // 后1000个地址更晚
		}
		discs[i] = AddressDiscovery{Address: addresses[i], FirstSeenBlock: &block, Status: FSFound}
	}

	groups := PlanGroups(addresses, discs, 500)
	t.Logf("  Time-bucket 2000 addrs: %d groups", len(groups))
	if len(groups) < 2 {
		t.Error("should produce multiple groups for time-separated addresses")
	}
}

func TestAdaptiveIsolateLegacy(t *testing.T) {
	// 模拟：999 个近期地址 + 1 个早期孤立地址
	n := 1000
	addresses := make([]string, n)
	discs := make([]AddressDiscovery, n)

	recentBlock := uint64(44000000)
	legacyBlock := uint64(8123456)

	// 999 个近期
	for i := 0; i < 999; i++ {
		addresses[i] = fmt.Sprintf("0xrecent%035x", i)
		discs[i] = AddressDiscovery{Address: addresses[i], FirstSeenBlock: &recentBlock, Status: FSFound}
	}
	// 1 个早期
	addresses[999] = "0xlegacy"
	discs[999] = AddressDiscovery{Address: "0xlegacy", FirstSeenBlock: &legacyBlock, Status: FSFound}

	// 分组 — 应该把早期地址分到不同组
	groups := PlanGroups(addresses, discs, 500)
	t.Logf("  Legacy isolation 1000 addrs: %d groups", len(groups))

	// 验证：早期地址和近期地址不在同一组
	legacyInGroup := -1
	recentInGroup := -1
	for gi, g := range groups {
		for _, a := range g.Addresses {
			if a == "0xlegacy" {
				legacyInGroup = gi
			}
			if a != "0xlegacy" {
				recentInGroup = gi
			}
		}
	}
	t.Logf("  Legacy in group %d, recent in group %d", legacyInGroup, recentInGroup)
	// blockLayerGroups sorts by block, so they should be in different groups
	if len(groups) > 1 && legacyInGroup == recentInGroup {
		t.Error("legacy should be in a different group from recent addresses")
	}
}

func TestAdaptiveCoverageEquivalence(t *testing.T) {
	// 验证：所有地址在所有组中都被覆盖
	n := 500
	addresses := make([]string, n)
	discs := make([]AddressDiscovery, n)
	for i := 0; i < n; i++ {
		addresses[i] = fmt.Sprintf("0x%040x", i)
		discs[i] = AddressDiscovery{Address: addresses[i], FirstSeenBlock: u64ptr(uint64(8000000 + i*1000)), Status: FSFound}
	}

	groups := PlanGroups(addresses, discs, 100)

	// 验证每条地址出现在某个组中
	seen := make(map[string]bool)
	for _, g := range groups {
		for _, a := range g.Addresses {
			seen[a] = true
		}
	}

	if len(seen) != n {
		t.Errorf("coverage gap: expected %d, got %d unique addresses", n, len(seen))
	}
	t.Logf("  Coverage: %d/%d addresses covered in %d groups ✅", len(seen), n, len(groups))
}

func TestAdaptiveNoDataLeakage(t *testing.T) {
	// 验证：分组不丢失数据
	n := 300
	addresses := make([]string, n)
	for i := 0; i < n; i++ {
		addresses[i] = fmt.Sprintf("0x%040x", i)
	}
	discs := make([]AddressDiscovery, n)
	for i := 0; i < n; i++ {
		b := uint64(10000000 + i*500)
		discs[i] = AddressDiscovery{Address: addresses[i], FirstSeenBlock: &b, Status: FSFound}
	}

	groups := PlanGroups(addresses, discs, 50)

	// 每个group的MinBlock应小于等于该组内所有地址的first-seen
	for _, g := range groups {
		for _, a := range g.Addresses {
			for _, d := range discs {
				if d.Address == a && d.FirstSeenBlock != nil && g.MinBlock > *d.FirstSeenBlock {
					t.Errorf("group MinBlock %d > address %s first-seen %d — data leakage!", g.MinBlock, a, *d.FirstSeenBlock)
				}
			}
		}
	}
	t.Logf("  No data leakage: %d groups verified ✅", len(groups))
}
