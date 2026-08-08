package progress

import (
	"testing"
	"time"
)

func TestWeightedProgress(t *testing.T) {
	items := []RangeProgress{
		{Weight: 1000, Percent: 1},
		{Weight: 9000, Percent: 0.5},
	}
	if got := WeightedProgress(items); got != 0.55 {
		t.Fatalf("加权进度 %v，期望 0.55", got)
	}
	if WeightedProgress(nil) != 0 {
		t.Fatal("空列表应为 0")
	}
	if got := WeightedProgress([]RangeProgress{{Weight: 0, Percent: 1}}); got != 0 {
		t.Fatalf("零权重应为 0，实际 %v", got)
	}
}

func TestRangeWeight(t *testing.T) {
	if RangeWeight(0, 0) != 1 {
		t.Fatal("无估算应为 1")
	}
	if RangeWeight(500, 100) != 500 {
		t.Fatal("估算行数应优先")
	}
	if RangeWeight(0, 300) != 300 {
		t.Fatal("区块跨度兜底")
	}
}

func TestETAEngineEWMAAndReset(t *testing.T) {
	e := NewETAEngine("sqd")
	e.Update(100, 10, time.Second)
	e.Update(300, 30, time.Second)
	if e.SampleCount() != 2 {
		t.Fatalf("样本数 %d", e.SampleCount())
	}
	rate := e.RowsRate()
	if rate <= 0 || rate > 300 {
		t.Fatalf("平滑速度异常 %.2f", rate)
	}
	e.Reset()
	if e.SampleCount() != 0 || e.RowsRate() != 0 {
		t.Fatal("Reset 未清空窗口")
	}
	if NewETAEngine("csv").alpha != 0.35 {
		t.Fatal("Direct/CSV alpha 应为 0.35")
	}
}

func TestComputeETA(t *testing.T) {
	eta := ComputeETA(1000, 100, 0, 0, false, 6, 0.9, 0)
	if eta.Seconds != 10 || eta.Confidence != "HIGH" {
		t.Fatalf("ETA %+v", eta)
	}
	if eta.LowerBoundSeconds != 7 || eta.UpperBoundSeconds != 13 {
		t.Fatalf("上下界 %d-%d", eta.LowerBoundSeconds, eta.UpperBoundSeconds)
	}
	eta = ComputeETA(1000, 100, 0, 0, false, 2, 0.9, 0)
	if eta.Confidence != "LOW" {
		t.Fatalf("少样本置信度 %s", eta.Confidence)
	}
	eta = ComputeETA(0, 0, 1000, 10, false, 6, 0.9, 0)
	if eta.Seconds != 100 || eta.BasedOn != "blocks" {
		t.Fatalf("区块 ETA %+v", eta)
	}
	eta = ComputeETA(1000, 100, 0, 0, true, 0, 0, 0)
	if !eta.Recalculating || eta.Confidence != "UNKNOWN" {
		t.Fatalf("重算态 %+v", eta)
	}
	eta = ComputeETA(1000, 100, 0, 0, false, 6, 0.9, 60*time.Second)
	if eta.Seconds != 70 {
		t.Fatalf("冷却叠加 ETA %d", eta.Seconds)
	}
}

func TestEventBufferReplay(t *testing.T) {
	b := NewEventBuffer(4)
	for i := uint64(1); i <= 4; i++ {
		b.Append(BufferedEvent{ID: i, BatchID: "b1", Type: "dataset.updated"})
	}
	events, ok := b.Replay("b1", 2)
	if !ok || len(events) != 2 || events[0].ID != 3 {
		t.Fatalf("回放失败 ok=%v events=%+v", ok, events)
	}
	events, ok = b.Replay("b2", 0)
	if !ok || len(events) != 0 {
		t.Fatalf("批次过滤失败 %+v", events)
	}
	// 超出缓冲 → resync
	events, ok = b.Replay("b1", 100)
	if ok || events != nil {
		t.Fatalf("超出缓冲应 resync ok=%v", ok)
	}
}

func TestSequenceStoreAndCoalescer(t *testing.T) {
	s := NewSequenceStore()
	if s.Next("b1") != 1 || s.Next("b1") != 2 || s.Next("b2") != 1 {
		t.Fatal("序列不单调")
	}
	if s.Current("b1") != 2 {
		t.Fatal("Current 错误")
	}
	c := NewCoalescer(50 * time.Millisecond)
	if !c.ShouldSend("d1", false) {
		t.Fatal("首次应发送")
	}
	if c.ShouldSend("d1", false) {
		t.Fatal("节流内应合并")
	}
	if !c.ShouldSend("d1", true) {
		t.Fatal("状态事件应即时发送")
	}
	time.Sleep(60 * time.Millisecond)
	if !c.ShouldSend("d1", false) {
		t.Fatal("节流到期应恢复发送")
	}
}
