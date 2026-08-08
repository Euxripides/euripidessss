package feedback

import (
	"testing"
	"time"
)

func TestTriggerActions(t *testing.T) {
	cases := []struct {
		name string
		m    ExecutionMetrics
		want Action
	}{
		{"503高", ExecutionMetrics{HTTP503Rate: 0.35}, Throttle},
		{"429高", ExecutionMetrics{HTTP429Rate: 0.15}, Throttle},
		{"熔断", ExecutionMetrics{CircuitOpen: true}, SwitchProvider},
		{"OOM", ExecutionMetrics{OOMCount: 1}, ScaleUpCloud},
		{"内存高", ExecutionMetrics{MemoryUsage: 0.9}, ScaleUpCloud},
		{"连续超时", ExecutionMetrics{TimeoutCount: 3}, Retry},
		{"静默缺口", ExecutionMetrics{SilentGap: true}, SwitchProvider},
		{"普通ETA超60min", ExecutionMetrics{
			Provider: "sqd", CurrentETA: 95 * time.Minute, OriginalETA: 20 * time.Minute,
			CompletedPercent: 0.3,
		}, ReduceRange},
		{"普通ETA翻倍且<60min", ExecutionMetrics{
			Provider: "sqd", CurrentETA: 50 * time.Minute, OriginalETA: 20 * time.Minute,
			CompletedPercent: 0.3,
		}, SwitchProvider},
		{"Cloud ETA超60min", ExecutionMetrics{
			Provider: "sqd_cloud", CurrentETA: 70 * time.Minute, OriginalETA: 20 * time.Minute,
			CompletedPercent: 0.5,
		}, ScaleUpCloud},
		{"Cloud降级", ExecutionMetrics{
			Provider: "sqd_cloud", CompletedPercent: 0.97, CurrentETA: 2 * time.Minute,
		}, ScaleDownCloud},
		{"正常保持", ExecutionMetrics{Provider: "sqd", CompletedPercent: 0.5, CurrentETA: 5 * time.Minute}, Keep},
	}
	for _, c := range cases {
		got := Reevaluate(c.m)
		if got.Action != c.want {
			t.Fatalf("%s: action=%s（期望 %s）reason=%s", c.name, got.Action, c.want, got.Reason)
		}
	}
}

func TestClassifyHTTPClass(t *testing.T) {
	if ClassifyHTTPClass("HTTP 503 Service Unavailable") != "503" {
		t.Fatal("503 分类失败")
	}
	if ClassifyHTTPClass("429 Too Many Requests") != "429" {
		t.Fatal("429 分类失败")
	}
	if ClassifyHTTPClass("context deadline exceeded") != "timeout" {
		t.Fatal("timeout 分类失败")
	}
	if ClassifyHTTPClass("boom") != "other" {
		t.Fatal("other 分类失败")
	}
}

func TestHistoryProfileAndPersistence(t *testing.T) {
	root := t.TempDir()
	h := NewHistory(root)
	for i := 0; i < 3; i++ {
		h.Record(Record{ChainID: 56, Dataset: "token_transfers", Provider: "sqd",
			ScaleBucket: "1M-5M", Rows: 100_000, Runtime: 3 * time.Second,
			Success: true, FinalSuccess: true, HTTPClass: "other"})
	}
	h.Record(Record{ChainID: 56, Dataset: "token_transfers", Provider: "sqd",
		ScaleBucket: "1M-5M", Rows: 100_000, Runtime: 5 * time.Second,
		Success: true, FinalSuccess: false, HTTPClass: "503"})
	p := h.Profile(56, "token_transfers", "sqd", "1M-5M")
	if p == nil || p.Jobs != 4 || p.SuccessCount != 4 || p.FinalSuccessCount != 3 {
		t.Fatalf("画像统计不符: %+v", p)
	}
	if p.RowsPerSecEWMA <= 0 || p.P95() < 3 {
		t.Fatalf("EWMA/P95 异常: ewma=%.1f p95=%.1f", p.RowsPerSecEWMA, p.P95())
	}
	if p.HTTP503Rate <= 0 {
		t.Fatal("503 率未记录")
	}
	// 持久化：新实例可加载
	h2 := NewHistory(root)
	p2 := h2.Profile(56, "token_transfers", "sqd", "1M-5M")
	if p2 == nil || p2.Jobs != 4 {
		t.Fatal("历史画像未持久化")
	}
	if h2.ScoreBonus(56, "token_transfers", "sqd", "1M-5M") <= 0 {
		t.Fatal("历史加成应为正")
	}
}
