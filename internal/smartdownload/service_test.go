package smartdownload

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	addrA = "0x1111111111111111111111111111111111111111"
	addrB = "0x2222222222222222222222222222222222222222"
	addrC = "0x3333333333333333333333333333333333333333"
)

func testService(t *testing.T) (*Store, *Service, Options) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 2
	opts.DefaultEndBlock = 999
	opts.RangeChunkSize = 200 // 5 ranges per dataset
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	svc.RegisterAdapter(NewMockProvider())
	return store, svc, opts
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("等待超时（%s）", what)
}

func addressID(store *Store, batchID, address string) string {
	for _, a := range store.ListAddressesByBatch(batchID) {
		if a.Address == address {
			return a.ID
		}
	}
	return ""
}

// TestE2EKillRestartResumeNoRedownload 是实施方案 §35 的 Phase 1 真实验证：
// 3 地址并行 → 暂停其中 1 个 → kill（Shutdown）→ restart（新 Service + RecoverAll）→
// 恢复 → 完成区间不重跑（Ledger 每个 RANGE_STARTED/COMPLETED 恰好一次）→ final dup=0。
func TestE2EKillRestartResumeNoRedownload(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 2
	opts.DefaultEndBlock = 999
	opts.RangeChunkSize = 200 // 5 ranges per dataset
	svc1 := NewService(store, opts, NewJSONLPartWriter(dir))
	svc1.RegisterAdapter(NewSlowMockProvider(300 * time.Millisecond))
	t.Cleanup(svc1.Shutdown)
	ctx := context.Background()
	resp, err := svc1.CreateBatch(ctx, CreateBatchRequest{
		ChainKey:  "bsc",
		Addresses: []string{addrA, addrB, addrC},
		Datasets:  []string{DatasetTransactions},
	})
	if err != nil {
		t.Fatal(err)
	}
	batchID := resp.Batch.ID
	if resp.Valid != 3 || resp.DatasetJobs != 3 || resp.RangeJobs != 15 {
		t.Fatalf("创建结构不符: %+v", resp)
	}
	if err := svc1.Start(batchID); err != nil {
		t.Fatal(err)
	}
	// 等待至少 2 个 Range 完成，且地址 B 至少完成 1 个 Range
	waitFor(t, 30*time.Second, "首批 Range 完成", func() bool {
		done := 0
		for _, r := range store.ListRanges() {
			if r.Status == RangeCompleted || r.Status == RangeEmpty {
				done++
			}
		}
		bID := addressID(store, batchID, addrB)
		bDone := 0
		for _, ds := range store.ListDatasetsByAddress(bID) {
			for _, r := range store.ListRangesByDataset(ds.ID) {
				if r.Status == RangeCompleted || r.Status == RangeEmpty {
					bDone++
				}
			}
		}
		return done >= 2 && bDone >= 1
	})
	// 暂停地址 B
	bID := addressID(store, batchID, addrB)
	if _, err := svc1.PauseAddress(bID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "地址 B 进入 PAUSED", func() bool {
		return store.GetAddress(bID).Status == AddressPaused
	})
	// kill：关闭 Service（取消 Worker 并等待退出）
	svc1.Shutdown()
	if store.GetAddress(bID).Status != AddressPaused {
		t.Fatal("暂停状态未持久化")
	}
	// restart：同一目录新建 Service + Recovery
	svc2 := NewService(store, DefaultOptions(), NewJSONLPartWriter(store.Root()))
	svc2.opts = DefaultOptions()
	svc2.opts.Workers = 2
	svc2.opts.DefaultEndBlock = 999
	svc2.opts.RangeChunkSize = 200
	svc2.RegisterAdapter(NewMockProvider())
	t.Cleanup(svc2.Shutdown)
	if err := svc2.RecoverAll(ctx); err != nil {
		t.Fatal(err)
	}
	if st := svc2.GetAddress(bID).Status; st != AddressPaused {
		t.Fatalf("重启后地址 B 应为 PAUSED，实际 %s", st)
	}
	// 恢复地址 B
	if _, err := svc2.ResumeAddress(bID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 60*time.Second, "批次 COMPLETED", func() bool {
		b := svc2.GetBatch(batchID)
		return b != nil && b.Status == BatchCompleted
	})
	// 断言：每个 Range 恰好 1 次 STARTED / 1 次 COMPLETED（或 EMPTY），完成区间不重跑
	for _, ds := range store.ListDatasets() {
		entries, err := NewLedger(store.Root(), ds.ID).Replay()
		if err != nil {
			t.Fatal(err)
		}
		started := map[string]int{}
		completed := map[string]int{}
		for _, e := range entries {
			if e.RangeID == "" {
				continue
			}
			switch e.Event {
			case LedgerRangeStarted:
				started[e.RangeID]++
			case LedgerRangeCompleted:
				completed[e.RangeID]++
			case LedgerRangeEmpty:
				completed[e.RangeID]++
			}
		}
		for _, r := range store.ListRangesByDataset(ds.ID) {
			key := BlockRange{From: r.FromBlock, To: r.ToBlock}.Key()
			if started[key] != 1 {
				t.Fatalf("dataset %s range %s STARTED=%d，期望 1（完成区间被重跑）", ds.ID, key, started[key])
			}
			if completed[key] != 1 {
				t.Fatalf("dataset %s range %s COMPLETED=%d，期望 1", ds.ID, key, completed[key])
			}
		}
	}
	// 断言 final dup=0：所有 Part 记录唯一键无重复
	assertNoDuplicateKeys(t, store.Root())
	// 最终状态
	for _, a := range store.ListAddressesByBatch(batchID) {
		if a.Status != AddressCompleted {
			t.Fatalf("地址 %s 状态 %s，期望 COMPLETED", a.Address, a.Status)
		}
	}
}

// TestCreateBatchPrefetchMarker 验证后台预取任务元数据（Investigation Cache V2 设计 §28-§33）。
func TestCreateBatchPrefetchMarker(t *testing.T) {
	_, svc, _ := testService(t)
	t.Cleanup(svc.Shutdown)
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:         "bsc",
		Addresses:        []string{addrA},
		Datasets:         []string{DatasetTransactions},
		SkipCovered:      boolPtrForTest(true),
		Prefetch:         true,
		PrefetchPriority: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Batch.Prefetch != true || resp.Batch.PrefetchPriority != 3 {
		t.Fatalf("预取标记未持久化: %+v", resp.Batch)
	}
	loaded := svc.GetBatch(resp.Batch.ID)
	if loaded == nil || !loaded.Prefetch || loaded.PrefetchPriority != 3 {
		t.Fatalf("重新读取预取标记失败: %+v", loaded)
	}
}

func boolPtrForTest(v bool) *bool {
	return &v
}

// TestCancelBatchKeepsCommittedParts 取消后保留已提交数据。
func TestCancelBatchKeepsCommittedParts(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 1
	opts.DefaultEndBlock = 399
	opts.RangeChunkSize = 200 // 2 ranges
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	svc.RegisterAdapter(NewSlowMockProvider(500 * time.Millisecond))
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:  "bsc",
		Addresses: []string{addrA},
		Datasets:  []string{DatasetTransactions},
	})
	if err != nil {
		t.Fatal(err)
	}
	batchID := resp.Batch.ID
	if err := svc.Start(batchID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "至少 1 个 Range 完成", func() bool {
		for _, r := range store.ListRanges() {
			if r.Status == RangeCompleted {
				return true
			}
		}
		return false
	})
	if _, err := svc.CancelBatch(batchID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "批次 CANCELED", func() bool {
		b := svc.GetBatch(batchID)
		return b != nil && b.Status == BatchCanceled
	})
	parts := 0
	_ = filepath.Walk(filepath.Join(store.Root(), "smart_download", "parts"), func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() && strings.HasSuffix(info.Name(), ".jsonl") {
			parts++
		}
		return nil
	})
	if parts == 0 {
		t.Fatal("取消后已提交 Part 被丢弃")
	}
	cp, err := svc.Checkpoint(store.ListDatasets()[0].ID)
	if err != nil || cp == nil || len(cp.CompletedRanges) == 0 {
		t.Fatalf("取消后 checkpoint 应保留完成区间: %v %+v", err, cp)
	}
}

// TestRetryOnTransientFailure 重试上限内自动恢复。
func TestRetryOnTransientFailure(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 1
	opts.DefaultEndBlock = 200
	opts.RangeChunkSize = 200
	opts.RetryLimit = 2
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	svc.RegisterAdapter(NewFailingMockProvider(1))
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
	waitFor(t, 30*time.Second, "批次 COMPLETED（重试后）", func() bool {
		b := svc.GetBatch(resp.Batch.ID)
		return b != nil && b.Status == BatchCompleted
	})
	entries, _ := NewLedger(dir, store.ListDatasets()[0].ID).Replay()
	failed := 0
	for _, e := range entries {
		if e.Event == LedgerRangeFailed {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("期望 1 次 Range 失败后重试成功，实际 %d", failed)
	}
}

func assertNoDuplicateKeys(t *testing.T, root string) {
	t.Helper()
	seen := map[string]bool{}
	total := 0
	partsRoot := filepath.Join(root, "smart_download", "parts")
	err := filepath.Walk(partsRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") {
			return nil
		}
		records, err := ReadPartRecords(path)
		if err != nil {
			return err
		}
		for _, r := range records {
			key := r.UniqueKey()
			if seen[key] {
				return fmt.Errorf("重复唯一键: %s", key)
			}
			seen[key] = true
			total++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 {
		t.Fatal("没有任何 Part 记录")
	}
}
