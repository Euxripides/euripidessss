package smartdownload

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
)

// TestValidationCoverageDupProviderCount 验收：coverage=100%、unknown=0、dup=0、provider count 一致。
func TestValidationCoverageDupProviderCount(t *testing.T) {
	sqd := NewMockNamedProvider("sqd").SetFailFrom(200)
	rpc := NewMockNamedProvider("rpc")
	store, svc, batchID := newSwitchService(t, sqd, rpc)
	t.Cleanup(svc.Shutdown)
	if err := svc.Start(batchID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, batchID)
	ds := store.ListDatasets()[0]
	waitFor(t, 30*time.Second, "校验完成", func() bool {
		cur := store.GetDataset(ds.ID)
		return cur != nil && cur.Validation != nil && cur.Status == DatasetCompleted
	})
	report := store.GetDataset(ds.ID).Validation
	if report.Status != "VALIDATED" {
		t.Fatalf("校验状态 %s（score=%.0f）: %v", report.Status, report.Score, report.Errors)
	}
	if report.Coverage != 1.0 || len(report.UnknownRanges) != 0 {
		t.Fatalf("coverage=%v unknown=%v", report.Coverage, report.UnknownRanges)
	}
	if report.DuplicateCount != 0 {
		t.Fatalf("dup=%d", report.DuplicateCount)
	}
	if report.ExpectedCount != report.ActualCount || report.ExpectedCount != report.UniqueKeyCount {
		t.Fatalf("provider count 不一致: expected=%d actual=%d unique=%d",
			report.ExpectedCount, report.ActualCount, report.UniqueKeyCount)
	}
	if !report.LevelFile || !report.LevelRecord || !report.LevelCoverage || !report.LevelProviderCnt {
		t.Fatalf("L1-L4 未全过: %+v", report)
	}
}

// TestGapRepair 验收：L5 缺口补洞——手工取消的 Range 自动重建并补下载，最终 coverage=100%。
func TestGapRepair(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 1
	opts.DefaultEndBlock = 399
	opts.RangeChunkSize = 200
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	svc.RegisterAdapter(NewMockProvider())
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:  "bsc",
		Addresses: []string{addrA},
		Datasets:  []string{DatasetTransactions},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟缺口：启动前手工取消第二个 Range（无数据）
	ds := store.ListDatasets()[0]
	ranges := store.ListRangesByDataset(ds.ID)
	if len(ranges) != 2 {
		t.Fatalf("期望 2 个 Range，实际 %d", len(ranges))
	}
	ranges[1].Status = RangeCanceled
	now := time.Now().UTC()
	ranges[1].FinishedAt = &now
	ranges[1].UpdatedAt = now
	_ = store.SaveRange(ranges[1])
	if err := svc.Start(resp.Batch.ID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, resp.Batch.ID)
	waitFor(t, 30*time.Second, "补洞后校验完成", func() bool {
		cur := store.GetDataset(ds.ID)
		return cur != nil && cur.Validation != nil && cur.Status == DatasetCompleted
	})
	report := store.GetDataset(ds.ID).Validation
	if report.Status != "VALIDATED" || report.Coverage != 1.0 {
		t.Fatalf("补洞后校验 %s coverage=%v errors=%v", report.Status, report.Coverage, report.Errors)
	}
	ds = store.GetDataset(ds.ID)
	if ds.RepairRounds < 1 {
		t.Fatalf("期望至少 1 轮补洞，实际 %d", ds.RepairRounds)
	}
	done := 0
	for _, r := range store.ListRangesByDataset(ds.ID) {
		if r.Status == RangeCompleted {
			done++
		}
	}
	if done != 2 {
		t.Fatalf("补洞后完成 Range=%d，期望 2", done)
	}
}

// TestEWMAETA 验收：EWMA 速度与 ETA 在运行中出现且置信度正常。
func TestEWMAETA(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 1
	opts.DefaultEndBlock = 1999
	opts.RangeChunkSize = 200 // 10 ranges
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	svc.RegisterAdapter(NewSlowMockProvider(300 * time.Millisecond))
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:  "bsc",
		Addresses: []string{addrA},
		Datasets:  []string{DatasetTransactions},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(resp.Batch.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Shutdown)
	var sawETA bool
	waitFor(t, 60*time.Second, "ETA 出现", func() bool {
		ds := store.ListDatasets()[0]
		p := ds.Progress
		if p.Percent >= 0.2 && p.Percent <= 0.9 && p.SpeedRowsPerSec > 0 && p.ETASeconds > 0 && p.ETAConfidence > 0 {
			sawETA = true
			return true
		}
		return false
	})
	if !sawETA {
		t.Fatal("运行中未观察到 EWMA ETA")
	}
}

// TestSSEEvents 验收：事件总线订阅（dataset.updated 合并；range.completed/result.ready 直推）。
func TestSSEEvents(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(dir))
	id, ch := svc.Events().Subscribe()
	defer svc.Events().Unsubscribe(id)
	svc.Events().Publish(Event{Type: EventDatasetUpdated, DatasetJobID: "d1"})
	svc.Events().Publish(Event{Type: EventDatasetUpdated, DatasetJobID: "d1"}) // 300ms 内合并丢弃
	svc.Events().Publish(Event{Type: EventRangeCompleted, DatasetJobID: "d1", Status: "COMPLETED"})
	select {
	case ev := <-ch:
		if ev.Type != EventDatasetUpdated {
			t.Fatalf("首个事件类型 %s", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 dataset.updated")
	}
	select {
	case ev := <-ch:
		if ev.Type != EventRangeCompleted {
			t.Fatalf("第二个事件类型 %s", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 range.completed")
	}
	// 合并后不应再有 dataset.updated
	select {
	case ev := <-ch:
		t.Fatalf("不应收到被合并的事件: %s", ev.Type)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestParquetPipelineValidation 验收：Canonical Parquet Part + DuckDB 读写校验全链路。
func TestParquetPipelineValidation(t *testing.T) {
	engine := openTestDuckDB(t)
	if engine == nil {
		t.Skip("DuckDB 不可用，跳过 Parquet 链路")
	}
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 1
	opts.DefaultEndBlock = 399
	opts.RangeChunkSize = 200
	svc := NewService(store, opts, NewParquetPartWriter(dir, engine))
	svc.SetDuckDB(engine)
	svc.RegisterAdapter(NewMockProvider())
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:  "bsc",
		Addresses: []string{addrA},
		Datasets:  []string{DatasetTransactions},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Shutdown)
	if err := svc.Start(resp.Batch.ID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, resp.Batch.ID)
	ds := store.ListDatasets()[0]
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		cur := store.GetDataset(ds.ID)
		if cur != nil && cur.Validation != nil && cur.Status == DatasetCompleted {
			break
		}
		if cur != nil && (cur.Status == DatasetFailed || cur.Status == DatasetPartial) {
			t.Fatalf("Parquet 任务失败: status=%s error=%s validation=%+v", cur.Status, cur.Error, cur.Validation)
		}
		time.Sleep(100 * time.Millisecond)
	}
	report := store.GetDataset(ds.ID).Validation
	if report == nil {
		cur := store.GetDataset(ds.ID)
		t.Fatalf("Parquet 任务未完成: status=%s error=%s", cur.Status, cur.Error)
	}
	if report.Status != "VALIDATED" {
		t.Fatalf("Parquet 校验 %s: %v", report.Status, report.Errors)
	}
	partsDir := filepath.Join(svc.PartsDir(), ds.ID)
	parquetCount := 0
	_ = filepath.Walk(partsDir, func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() && strings.HasSuffix(info.Name(), ".parquet") {
			parquetCount++
		}
		return nil
	})
	if parquetCount != 2 {
		t.Fatalf("期望 2 个 Parquet Part，实际 %d", parquetCount)
	}
}

// openTestDuckDB 从仓库根打开 DuckDB（找不到则返回 nil）。
func openTestDuckDB(t *testing.T) *duckdb.Engine {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return nil
	}
	root := dir
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return nil
		}
		root = parent
	}
	engine := duckdb.Open(root, t.TempDir(), duckdb.AnalyticsConfig{})
	if !engine.Available() {
		return nil
	}
	return engine
}
