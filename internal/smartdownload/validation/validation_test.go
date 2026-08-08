package validation

import (
	"testing"
	"time"
)

func TestIntervalSetOperations(t *testing.T) {
	s := NewIntervalSet()
	s.Add(0, 99)
	s.Add(200, 299)
	s.Add(100, 199) // 与前后相邻 → 合并成 0-299
	if len(s.Items()) != 1 || s.Items()[0].To != 299 {
		t.Fatalf("合并失败: %+v", s.Items())
	}
	other := FromIntervals([]BlockInterval{{From: 150, To: 249}})
	sub := s.Subtract(other)
	if len(sub) != 2 || sub[0].To != 149 || sub[1].From != 250 {
		t.Fatalf("减法失败: %+v", sub)
	}
	inter := s.Intersect(other)
	if inter.Blocks() != 100 {
		t.Fatalf("交集块数 %d，期望 100", inter.Blocks())
	}
	gaps := s.FindGaps(0, 499)
	if len(gaps) != 1 || gaps[0].From != 300 || gaps[0].To != 499 {
		t.Fatalf("FindGaps 失败: %+v", gaps)
	}
	if s.CoverageRatio(0, 499) != 300.0/500.0 {
		t.Fatalf("覆盖率 %v", s.CoverageRatio(0, 499))
	}
}

func TestRangeGaps(t *testing.T) {
	requested := BlockInterval{From: 40_000_000, To: 40_500_000}
	valid := []BlockInterval{{40_000_000, 40_099_999}, {40_100_000, 40_199_999}, {40_400_000, 40_500_000}}
	empty := []BlockInterval{{40_300_000, 40_399_999}}
	gaps := RangeGaps(requested, valid, empty)
	if len(gaps) != 1 || gaps[0].FromBlock != 40_200_000 || gaps[0].ToBlock != 40_299_999 {
		t.Fatalf("RANGE_GAP 定位失败: %+v", gaps)
	}
	if gaps[0].Type != GapRangeGap {
		t.Fatalf("类型 %s", gaps[0].Type)
	}
}

func TestSuspiciousEmpty(t *testing.T) {
	empty := []BlockInterval{{40_200_000, 40_249_999}}
	all := []BlockInterval{
		{40_100_000, 40_199_999}, {40_200_000, 40_249_999}, {40_250_000, 40_349_999},
	}
	rows := map[string]int64{
		"40100000_40199999": 12_000,
		"40250000_40349999": 11_500,
	}
	gaps := SuspiciousEmpty(empty, all, rows, 50)
	if len(gaps) != 1 || gaps[0].Type != GapSuspiciousEmpty {
		t.Fatalf("SUSPICIOUS_EMPTY 未识别: %+v", gaps)
	}
	// 邻居不活跃时不标记
	rows["40100000_40199999"] = 10
	gaps = SuspiciousEmpty(empty, all, rows, 50)
	if len(gaps) != 0 {
		t.Fatalf("低活跃邻居不应标记: %+v", gaps)
	}
}

func TestCountBisect(t *testing.T) {
	// 0..99999 每块 1 行，缺失 50000
	countOf := func(from, to uint64) (int64, error) {
		n := int64(to - from + 1)
		if from <= 50_000 && to >= 50_000 {
			n--
		}
		return n, nil
	}
	actual, _ := countOf(0, 99_999)
	gaps := CountBisect(0, 99_999, 100_000, actual, countOf)
	if len(gaps) == 0 {
		t.Fatal("COUNT_GAP 二分未定位")
	}
	found := false
	for _, g := range gaps {
		if g.From <= 50_000 && g.To >= 50_000 {
			found = true
		}
	}
	if !found {
		t.Fatalf("未定位到包含缺失块 50000 的区间: %+v", gaps)
	}
}

func TestRepairPlanner(t *testing.T) {
	// SQD 主任务疑似静默漏数据 → 补洞优先 RPC
	p := NewRepairPlanner([]string{"rpc", "sqd", "sqd_cloud"}, []string{"sqd"}, []string{"sqd"})
	if got := p.Select(); got != "rpc" {
		t.Fatalf("补洞 Provider %s，期望 rpc", got)
	}
	// RPC 也不可用 → SQD Cloud
	p = NewRepairPlanner([]string{"sqd", "sqd_cloud"}, []string{"sqd"}, []string{"sqd"})
	if got := p.Select(); got != "sqd_cloud" {
		t.Fatalf("补洞 Provider %s，期望 sqd_cloud", got)
	}
	// 全部黑名单 → 空
	p = NewRepairPlanner([]string{"rpc", "sqd"}, []string{"rpc", "sqd"}, []string{"rpc", "sqd"})
	if got := p.Select(); got != "" {
		t.Fatalf("应无可用补洞 Provider，实际 %s", got)
	}
}

func TestGapStoreCertificate(t *testing.T) {
	root := t.TempDir()
	store := NewGapStore(root, "ds1")
	if err := store.SaveState(StateRunning, "L3"); err != nil {
		t.Fatal(err)
	}
	if store.LoadState().Status != StateRunning {
		t.Fatal("状态未持久化")
	}
	if err := store.AppendGap(GapRecord{GapID: "gap_1", Type: GapRangeGap, FromBlock: 1, ToBlock: 2, Status: GapDetected, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendRepair(RepairAttempt{GapID: "gap_1", Provider: "rpc", Attempt: 1, StartedAt: time.Now(), FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	gaps, _ := store.LoadGaps()
	if len(gaps) != 1 {
		t.Fatal("gap ledger 读取失败")
	}
	if n, _ := store.RepairCount("gap_1"); n != 1 {
		t.Fatalf("repair attempts=%d，期望 1", n)
	}
	cert := &Certificate{DatasetJobID: "ds1", Status: "PASS", Coverage: 1,
		RowsFinal: 100, CertifiedAt: time.Now()}
	if err := store.SaveCertificate(cert); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadCertificate()
	if err != nil || loaded.RowsFinal != 100 {
		t.Fatal("证书读写失败")
	}
}
