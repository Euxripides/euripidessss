package downloadengine

import (
	"context"
	"testing"
)

// ── Mock FirstSeenResolver ──

type mockFirstSeenResolver struct {
	data map[string]*AddressDiscovery
}

func (m *mockFirstSeenResolver) ResolveFirstSeen(_ context.Context, _, address string) (*AddressDiscovery, error) {
	if d, ok := m.data[address]; ok {
		return d, nil
	}
	return &AddressDiscovery{Address: address, Status: FSNotFound}, nil
}

func discoveryPtr(b uint64, t string) *AddressDiscovery {
	return &AddressDiscovery{
		Address:        "0x" + t,
		FirstSeenBlock: &b,
		FirstSeenTime:  &t,
		Status:         FSFound,
		Coverage:       CoverageV2Full,
	}
}

func TestPlanAutoFirstSeen(t *testing.T) {
	block1 := uint64(8000000)
	block2 := uint64(9000000)
	endBlock := uint64(54000000)

	resolver := &mockFirstSeenResolver{data: map[string]*AddressDiscovery{
		"0xa": {Address: "0xa", FirstSeenBlock: &block1, Status: FSFound, Coverage: CoverageV2Full},
		"0xb": {Address: "0xb", FirstSeenBlock: &block2, Status: FSFound, Coverage: CoverageV2Full},
	}}
	engine := NewDiscoveryEngine(resolver)
	planner := NewRangePlanner(engine)

	rng, err := planner.Plan(context.Background(), RangePlanRequest{
		Mode:      RangeAutoFirstSeen,
		ChainID:   "bsc",
		Addresses: []string{"0xa", "0xb"},
		EndBlock:  &endBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rng.StartBlock != block1 {
		t.Errorf("expected min block %d, got %d", block1, rng.StartBlock)
	}
	if rng.EndBlock != endBlock {
		t.Errorf("expected end block %d, got %d", endBlock, rng.EndBlock)
	}
	if rng.RangeSource != "FIRST_SEEN" {
		t.Errorf("expected FIRST_SEEN, got %s", rng.RangeSource)
	}
}

func TestPlanBlockRange(t *testing.T) {
	planner := NewRangePlanner(nil)
	start := uint64(10000000)
	end := uint64(20000000)

	rng, err := planner.Plan(context.Background(), RangePlanRequest{
		Mode:       RangeBlock,
		StartBlock: &start,
		EndBlock:   &end,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rng.RangeSource != "USER_SELECTED" {
		t.Errorf("expected USER_SELECTED, got %s", rng.RangeSource)
	}
}

func TestPlanBlockRangeInvalidatesWhenStartGEQEnd(t *testing.T) {
	planner := NewRangePlanner(nil)
	start := uint64(20000000)
	end := uint64(10000000)

	_, err := planner.Plan(context.Background(), RangePlanRequest{
		Mode:       RangeBlock,
		StartBlock: &start,
		EndBlock:   &end,
	})
	if err == nil {
		t.Fatal("should reject start >= end")
	}
}

func TestPlanAutoFirstSeenAllNotFound(t *testing.T) {
	resolver := &mockFirstSeenResolver{data: map[string]*AddressDiscovery{}}
	engine := NewDiscoveryEngine(resolver)
	planner := NewRangePlanner(engine)

	_, err := planner.Plan(context.Background(), RangePlanRequest{
		Mode:      RangeAutoFirstSeen,
		ChainID:   "bsc",
		Addresses: []string{"0xunknown"},
	})
	if err == nil {
		t.Fatal("should fail when all addresses not found")
	}
}

func TestPlanAutoFirstSeenPartial(t *testing.T) {
	block1 := uint64(8000000)
	endBlock := uint64(54000000)

	resolver := &mockFirstSeenResolver{data: map[string]*AddressDiscovery{
		"0xa": {Address: "0xa", FirstSeenBlock: &block1, Status: FSFound, Coverage: CoverageV2Full},
		"0xb": {Address: "0xb", Status: FSPartial, Coverage: CoverageV2Partial},
		"0xc": {Address: "0xc", Status: FSNotFound},
	}}
	engine := NewDiscoveryEngine(resolver)
	planner := NewRangePlanner(engine)

	rng, err := planner.Plan(context.Background(), RangePlanRequest{
		Mode:      RangeAutoFirstSeen,
		ChainID:   "bsc",
		Addresses: []string{"0xa", "0xb", "0xc"},
		EndBlock:  &endBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rng.CoverageStatus != string(CoverageV2Partial) {
		t.Errorf("expected PARTIAL coverage, got %s", rng.CoverageStatus)
	}
}

func TestPlanGroupsSingleBucket(t *testing.T) {
	d1 := discoveryPtr(8000000, "a")
	d2 := discoveryPtr(9000000, "b")
	groups := PlanGroups([]string{"0xa", "0xb"}, []AddressDiscovery{*d1, *d2}, 100)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
}

func TestPlanGroupsHashBucket(t *testing.T) {
	addrs := make([]string, 6000)
	discs := make([]AddressDiscovery, 6000)
	block := uint64(10000000)
	for i := 0; i < 6000; i++ {
		addrs[i] = "0x" + string(rune('a'+i%26))
		discs[i] = AddressDiscovery{Address: addrs[i], FirstSeenBlock: &block, Status: FSFound}
	}
	groups := PlanGroups(addrs, discs, 500)
	if len(groups) < 2 {
		t.Errorf("6000 addrs should produce multiple groups, got %d", len(groups))
	}
}

func TestDiscoveryResultCounts(t *testing.T) {
	block := uint64(5000000)
	resolver := &mockFirstSeenResolver{data: map[string]*AddressDiscovery{
		"0xa": {Address: "0xa", FirstSeenBlock: &block, Status: FSFound},
		"0xb": {Address: "0xb", Status: FSPartial},
		"0xc": {Address: "0xc", Status: FSNotFound},
		"0xd": {Address: "0xd", Status: FSTemporarilyUnavailable},
	}}
	engine := NewDiscoveryEngine(resolver)
	result := engine.Discover(context.Background(), "bsc", []string{"0xa", "0xb", "0xc", "0xd", "0xe"})

	if result.Total != 5 {
		t.Errorf("total=%d", result.Total)
	}
	if result.Found != 1 {
		t.Errorf("found=%d", result.Found)
	}
	if result.Partial != 1 {
		t.Errorf("partial=%d", result.Partial)
	}
	if result.NotFound != 2 {
		t.Errorf("not_found=%d", result.NotFound)
	}
	if result.TemporarilyUnavailable != 1 {
		t.Errorf("temporarily_unavailable=%d", result.TemporarilyUnavailable)
	}
	if result.Failed != 0 {
		t.Errorf("failed=%d", result.Failed)
	}
}
