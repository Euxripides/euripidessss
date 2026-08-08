package discovery

import (
	"context"
	"testing"
	"time"
)

type fakeMetadata struct {
	total uint64
	ok    bool
}

func (m fakeMetadata) TotalRows(_ context.Context, _, _, _ string, _, _ uint64) (uint64, bool, error) {
	return m.total, m.ok, nil
}

// Case A：L0 Metadata 命中 → confidence 0.95，不采样。
func TestL0MetadataTotal(t *testing.T) {
	sampled := 0
	e := NewEngine(t.TempDir(), fakeMetadata{total: 8_243_211, ok: true},
		func(_ context.Context, _, _ uint64) (uint64, error) {
			sampled++
			return 0, nil
		})
	res, err := e.Discover(context.Background(), Input{
		ChainKey: "bsc", Address: "0xabc", Dataset: "token_transfers",
		FromBlock: 40_000_000, ToBlock: 50_000_000, HeadBlock: 114_500_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.EstimatedRows != 8_243_211 || res.Confidence != 0.95 {
		t.Fatalf("L0 估算不符: rows=%d conf=%v", res.EstimatedRows, res.Confidence)
	}
	if sampled != 0 {
		t.Fatalf("L0 命中不应采样，实际 %d 次", sampled)
	}
}

// Case B：L1 均匀密度 → 估算误差 < ±30%。
func TestL1UniformEstimate(t *testing.T) {
	// 真实：10M 块，每 200 块窗口返回 200 行（密度 1/块）
	e := NewEngine(t.TempDir(), nil, func(_ context.Context, from, to uint64) (uint64, error) {
		return to - from + 1, nil
	})
	res, err := e.Discover(context.Background(), Input{
		ChainKey: "bsc", Address: "0xabc", Dataset: "transactions",
		FromBlock: 0, ToBlock: 9_999_999, HeadBlock: 114_500_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.EstimatedRows == 0 || res.ActivityDensity < 0.9 {
		t.Fatalf("均匀估算异常: rows=%d density=%.3f", res.EstimatedRows, res.ActivityDensity)
	}
	if res.Confidence < 0.7 {
		t.Fatalf("均匀密度置信度应中高: %.2f", res.Confidence)
	}
}

// Case C：L2 自适应采样：尾部高密度 → 方差高 → 分段建模。
func TestAdaptiveSamplingSegments(t *testing.T) {
	e := NewEngine(t.TempDir(), nil, func(_ context.Context, from, to uint64) (uint64, error) {
		// 最后 1/4 区域密度高 50 倍
		if from > 9_000_000 {
			return (to - from + 1) * 50, nil
		}
		return to - from + 1, nil
	})
	res, err := e.Discover(context.Background(), Input{
		ChainKey: "bsc", Address: "0xabc", Dataset: "logs",
		FromBlock: 0, ToBlock: 11_999_999, HeadBlock: 114_500_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Segments) < 3 {
		t.Fatalf("高方差应分段建模，实际 %d 段", len(res.Segments))
	}
	if res.Confidence > 0.9 {
		t.Fatalf("高方差置信度应受限: %.2f", res.Confidence)
	}
	if res.EstimatedRows <= 12_000_000 {
		t.Fatalf("尾部高密度应被放大估算: %d", res.EstimatedRows)
	}
}

// Cache：同一输入命中缓存，不再采样。
func TestDiscoveryCache(t *testing.T) {
	root := t.TempDir()
	sampled := 0
	sample := func(_ context.Context, from, to uint64) (uint64, error) {
		sampled++
		return to - from + 1, nil
	}
	in := Input{ChainKey: "bsc", Address: "0xabc", Dataset: "transactions",
		FromBlock: 0, ToBlock: 999_999, HeadBlock: 114_500_000}
	e1 := NewEngine(root, nil, sample)
	if _, err := e1.Discover(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	first := sampled
	e2 := NewEngine(root, nil, sample)
	if _, err := e2.Discover(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if sampled != first {
		t.Fatalf("缓存未命中：采样 %d→%d", first, sampled)
	}
}

// Probe 成本守卫：超过 30s 停止采样。
func TestProbeCostGuard(t *testing.T) {
	e := NewEngine(t.TempDir(), nil, func(_ context.Context, from, to uint64) (uint64, error) {
		time.Sleep(20 * time.Millisecond)
		return to - from + 1, nil
	})
	e.maxProbeRuntime = 15 * time.Millisecond
	res, err := e.Discover(context.Background(), Input{
		ChainKey: "bsc", Address: "0xabc", Dataset: "transactions",
		FromBlock: 0, ToBlock: 9_999_999, HeadBlock: 114_500_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Segments) == 0 {
		t.Fatal("成本守卫后仍应有部分估算")
	}
}

// Range Planner：低密度 → 大跨度；高密度 → 小跨度。
func TestAdaptiveSpan(t *testing.T) {
	if got := AdaptiveSpan(0.000001, 50_000, 500, 500_000); got != 500_000 {
		t.Fatalf("低密度跨度 %d，期望 500000", got)
	}
	if got := AdaptiveSpan(10, 50_000, 500, 500_000); got != 5_000 {
		t.Fatalf("高密度跨度 %d，期望 5000（目标行数/密度）", got)
	}
	if got := AdaptiveSpan(1, 50_000, 500, 500_000); got != 50_000 {
		t.Fatalf("中密度跨度 %d，期望 50000", got)
	}
}

func TestPlanSegmentsCoverage(t *testing.T) {
	segments := []ActivitySegment{
		{FromBlock: 0, ToBlock: 9_999_999, Density: 0.0001},
		{FromBlock: 10_000_000, ToBlock: 11_999_999, Density: 10},
	}
	ranges := PlanSegments(0, 11_999_999, segments, 50_000)
	if len(ranges) == 0 || ranges[0].From != 0 || ranges[len(ranges)-1].To != 11_999_999 {
		t.Fatalf("分段范围未覆盖完整区间: %+v", ranges)
	}
	// 低密度段应为大跨度（范围更少），高密度段跨度小（范围更多）
	low, high := 0, 0
	for _, r := range ranges {
		if r.To <= 9_999_999 {
			low++
		} else {
			high++
		}
	}
	if low >= high {
		t.Fatalf("低密度段应比高密度段跨度更大: low=%d high=%d", low, high)
	}
}
