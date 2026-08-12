package smartdownload

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// newSwitchService 创建 2 Range 批次（0-399，chunk 200）的切换测试服务。
func newSwitchService(t *testing.T, adapters ...ProviderAdapter) (*Store, *Service, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 1
	opts.DefaultEndBlock = 399
	opts.RangeChunkSize = 200
	opts.RetryLimit = 2
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	for _, a := range adapters {
		svc.RegisterAdapter(a)
	}
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:  "bsc",
		Addresses: []string{addrA},
		Datasets:  []string{DatasetTransactions},
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, svc, resp.Batch.ID
}

func waitCompleted(t *testing.T, svc *Service, batchID string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		b := svc.GetBatch(batchID)
		if b != nil && b.Status == BatchCompleted {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	b := svc.GetBatch(batchID)
	if b == nil {
		t.Fatal("等待批次 COMPLETED 超时：批次不存在")
	}
	var states []string
	for _, address := range svc.store.ListAddressesByBatch(batchID) {
		for _, dataset := range svc.store.ListDatasetsByAddress(address.ID) {
			states = append(states, fmt.Sprintf("address=%s dataset=%s status=%s validation=%s certification=%s error=%q",
				address.Status, dataset.Dataset, dataset.Status, reportStatus(dataset.Validation), dataset.Certification, dataset.Error))
		}
	}
	t.Fatalf("等待批次 COMPLETED 超时：batch=%s states=%v", b.Status, states)
}

func rangeProvider(store *Store, dsID string, from uint64) string {
	for _, r := range store.ListRangesByDataset(dsID) {
		if r.FromBlock == from {
			return r.Provider
		}
	}
	return ""
}

func ledgerSwitchEvents(t *testing.T, store *Store, dsID string) map[string][]string {
	t.Helper()
	entries, err := NewLedger(store.Root(), dsID).Replay()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, e := range entries {
		if e.Event == LedgerProviderSwitched {
			out[e.RangeID] = append(out[e.RangeID], e.Error)
		}
	}
	return out
}

// Case A：CSV → SQD（CSV 全部失败，SQD 接管；完成区间不重跑，dup=0）。
func TestSwitchCSVToSQD(t *testing.T) {
	csv := NewMockCSVProvider().SetFailAll()
	sqd := NewMockNamedProvider("sqd")
	store, svc, batchID := newSwitchService(t, csv, sqd)
	t.Cleanup(svc.Shutdown)
	if err := svc.Start(batchID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, batchID)
	ds := store.ListDatasets()[0]
	for _, r := range store.ListRangesByDataset(ds.ID) {
		if r.Provider != "sqd" {
			t.Fatalf("range %d-%d provider=%s，期望 sqd（CSV 失败后切换）", r.FromBlock, r.ToBlock, r.Provider)
		}
		if r.Attempts != 1 {
			t.Fatalf("range %d-%d attempts=%d，期望 1（CSV 一次失败即切换）", r.FromBlock, r.ToBlock, r.Attempts)
		}
	}
	switches := ledgerSwitchEvents(t, store, ds.ID)
	for _, r := range store.ListRangesByDataset(ds.ID) {
		key := BlockRange{From: r.FromBlock, To: r.ToBlock}.Key()
		if len(switches[key]) != 1 || !strings.Contains(switches[key][0], "previous=csv") {
			t.Fatalf("range %s 缺少 csv→sqd 切换记录: %v", key, switches[key])
		}
	}
	assertNoDuplicateKeys(t, store.Root())
}

// Case B：SQD → RPC（SQD 从区块 200 起连续失败，熔断后 RPC 接管后续 Range）。
func TestSwitchSQDToRPC(t *testing.T) {
	sqd := NewMockNamedProvider("sqd").SetFailFrom(200)
	rpc := NewMockNamedProvider("rpc")
	store, svc, batchID := newSwitchService(t, sqd, rpc)
	t.Cleanup(svc.Shutdown)
	if err := svc.Start(batchID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, batchID)
	ds := store.ListDatasets()[0]
	if got := rangeProvider(store, ds.ID, 0); got != "sqd" {
		t.Fatalf("range1 provider=%s，期望 sqd（前 55%% 正常）", got)
	}
	if got := rangeProvider(store, ds.ID, 200); got != "rpc" {
		t.Fatalf("range2 provider=%s，期望 rpc（SQD 失败后切换）", got)
	}
	switches := ledgerSwitchEvents(t, store, ds.ID)
	if len(switches[BlockRange{From: 200, To: 399}.Key()]) == 0 {
		t.Fatal("range2 缺少 PROVIDER_SWITCHED 记录")
	}
	assertNoDuplicateKeys(t, store.Root())
}

// Case C：RPC → SQD Cloud（RPC 全部失败，Cloud 只跑缺失 Range）。
func TestSwitchRPCToCloud(t *testing.T) {
	rpc := NewMockNamedProvider("rpc").SetFailAll()
	cloud := NewMockCloudProvider()
	store, svc, batchID := newSwitchService(t, rpc, cloud)
	t.Cleanup(svc.Shutdown)
	// 把数据集改为 token_transfers（Cloud V1 仅支持该数据集）
	for _, ds := range store.ListDatasets() {
		ds.Dataset = DatasetTokenTransfers
		_ = store.SaveDataset(ds)
	}
	if err := svc.Start(batchID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, batchID)
	ds := store.ListDatasets()[0]
	for _, r := range store.ListRangesByDataset(ds.ID) {
		if r.Provider != "sqd_cloud" {
			t.Fatalf("range %d-%d provider=%s，期望 sqd_cloud（RPC 全部失败后兜底）", r.FromBlock, r.ToBlock, r.Provider)
		}
	}
	switches := ledgerSwitchEvents(t, store, ds.ID)
	for _, r := range store.ListRangesByDataset(ds.ID) {
		key := BlockRange{From: r.FromBlock, To: r.ToBlock}.Key()
		if len(switches[key]) == 0 {
			t.Fatalf("range %s 缺少 rpc→sqd_cloud 切换记录", key)
		}
	}
	assertNoDuplicateKeys(t, store.Root())
}

// Discovery：先探测后下载，估算写入 DatasetJob，计划给出候选与规模档。
func TestDiscoveryPlanBatch(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.DefaultEndBlock = 99_999
	opts.RangeChunkSize = 50_000
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	svc.RegisterAdapter(NewMockNamedProvider("sqd"))
	svc.RegisterAdapter(NewMockCSVProvider().SetFailAll())
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:  "bsc",
		Addresses: []string{addrA},
		Datasets:  []string{DatasetTransactions},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := svc.PlanBatch(context.Background(), resp.Batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Datasets) != 1 {
		t.Fatalf("计划数据集数 %d，期望 1", len(plan.Datasets))
	}
	dp := plan.Datasets[0]
	if dp.EstimatedRows == 0 || dp.EstimatedBytes == 0 {
		t.Fatalf("探测估算缺失: %+v", dp)
	}
	if dp.SizeClass != SizeClassS {
		t.Fatalf("规模档 %s，期望 S", dp.SizeClass)
	}
	ds := store.ListDatasets()[0]
	if ds.EstimatedRows == 0 || ds.PreferredProvider == "" || ds.DiscoveryConfidence == 0 {
		t.Fatalf("估算未持久化: %+v", ds)
	}
	// 小数据 S 档：CSV 优先（90 分）> SQD（75 分）
	if len(dp.Candidates) < 2 || dp.Candidates[0].Name != "csv" {
		t.Fatalf("S 档候选顺序错误: %+v", dp.Candidates)
	}
}
