package smartdownload

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/cloudruntime"
	"github.com/etl/backend/internal/smartdownload/feedback"
	v3 "github.com/etl/backend/internal/smartdownload/validation"
	"github.com/xuri/excelize/v2"
)

// TestAddressImportColumnDetection 验收：CSV 地址列自动识别 + 统计。
func TestAddressImportColumnDetection(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(dir))
	csvPath := filepath.Join(dir, "addresses.csv")
	content := "wallet_address,memo,other\n" +
		"0x1111111111111111111111111111111111111111,aaa,1\n" +
		"0x2222222222222222222222222222222222222222,bbb,2\n" +
		"0x1111111111111111111111111111111111111111,ccc,3\n" +
		"not-an-address,ddd,4\n" +
		"0x3333333333333333333333333333333333333333,eee,5\n"
	if err := os.WriteFile(csvPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res, err := svc.ImportAddresses("addresses.csv", f)
	if err != nil {
		t.Fatal(err)
	}
	if res.SelectedColumn != "wallet_address" {
		t.Fatalf("选中列 %s，期望 wallet_address（列候选: %+v）", res.SelectedColumn, res.DetectedColumns)
	}
	if res.Rows != 5 || res.Valid != 3 || res.Duplicates != 1 || res.Invalid != 1 {
		t.Fatalf("统计不符: rows=%d valid=%d dup=%d invalid=%d", res.Rows, res.Valid, res.Duplicates, res.Invalid)
	}
	if len(res.FinalAddresses) != 3 {
		t.Fatalf("最终地址数 %d，期望 3", len(res.FinalAddresses))
	}
	for _, c := range res.DetectedColumns {
		if c.Name == "wallet_address" && c.Confidence < 0.7 {
			t.Fatalf("wallet_address 命中率 %v 过低", c.Confidence)
		}
	}
}

// TestPackCreation10K 验收（§65）：10K 地址逻辑任务创建 <3s，Worker 数固定。
func TestPackCreation10K(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 4
	opts.DefaultEndBlock = 999
	opts.RangeChunkSize = 1000 // 每数据集 1 个 Range
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	addrs := make([]string, 10_000)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("0x%040x", i+1)
	}
	start := time.Now()
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:  "bsc",
		Addresses: addrs,
		Datasets:  []string{DatasetTransactions},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Valid != 10_000 || resp.RangeJobs != 10_000 {
		t.Fatalf("创建结构不符: valid=%d ranges=%d", resp.Valid, resp.RangeJobs)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("10K 逻辑任务创建耗时 %v，超 3s 目标", elapsed)
	}
	t.Logf("10K 创建耗时 %v", elapsed)
	// Pack 模式：不产生 3 万个独立文件
	fileCount := 0
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() && strings.HasSuffix(info.Name(), ".json") {
			fileCount++
		}
		return nil
	})
	if fileCount > 20 {
		t.Fatalf("Pack 模式文件数 %d，期望 ≤20（避免 30K 小文件）", fileCount)
	}
	// 重启后可从 Pack 恢复全部任务
	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.ListAddressesByBatch(resp.Batch.ID)) != 10_000 {
		t.Fatalf("重启后地址数 %d，期望 10000", len(reloaded.ListAddressesByBatch(resp.Batch.ID)))
	}
	if len(reloaded.ListRanges()) != 10_000 {
		t.Fatalf("重启后 Range 数 %d，期望 10000", len(reloaded.ListRanges()))
	}
	if svc.Options().Workers != 4 {
		t.Fatalf("Worker 数 %d，期望固定 4（不随地址数增长）", svc.Options().Workers)
	}
}

// TestLocalHitReuse 验收（§61）：本地已覆盖地址直接复用，不触发下载。
func TestLocalHitReuse(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(dir))
	svc.SetRangeCoverageSource(mockRangeCoverage{intervals: []BlockRange{{From: 0, To: 50_000_000}}})
	skip := true
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:    "bsc",
		Addresses:   []string{addrA},
		Datasets:    []string{DatasetTransactions},
		SkipCovered: &skip,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(resp.Batch.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Shutdown)
	waitCompleted(t, svc, resp.Batch.ID)
	ds := store.ListDatasets()[0]
	if ds.Status != DatasetCompleted || ds.CurrentProvider != "local_hit" {
		t.Fatalf("复用数据集状态 %s provider=%s", ds.Status, ds.CurrentProvider)
	}
	for _, r := range store.ListRangesByDataset(ds.ID) {
		if r.Provider != "local_hit" || r.Status != RangeCompleted {
			t.Fatalf("复用 Range %d-%d provider=%s status=%s", r.FromBlock, r.ToBlock, r.Provider, r.Status)
		}
	}
	entries, _ := NewLedger(store.Root(), ds.ID).Replay()
	reused := false
	for _, e := range entries {
		if e.Event == "RANGE_REUSED" {
			reused = true
		}
	}
	if !reused {
		t.Fatal("缺少 RANGE_REUSED 账本记录")
	}
}

type mockRangeCoverage struct {
	intervals []BlockRange
}

func (m mockRangeCoverage) CoveredRanges(_ context.Context, _, _, _ string, from, to uint64) ([]BlockRange, error) {
	var out []BlockRange
	for _, iv := range m.intervals {
		lo, hi := iv.From, iv.To
		if from > lo {
			lo = from
		}
		if to < hi {
			hi = to
		}
		if hi >= lo {
			out = append(out, BlockRange{From: lo, To: hi})
		}
	}
	return out, nil
}

// TestRangeDiffPartialReuse 验收：部分覆盖只补下载缺失区间，已覆盖区间不重下。
func TestRangeDiffPartialReuse(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	opts := DefaultOptions()
	opts.Workers = 1
	opts.DefaultEndBlock = 799
	opts.RangeChunkSize = 200 // chunks: 0-199 / 200-399 / 400-599 / 600-799
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	svc.RegisterAdapter(NewMockProvider())
	svc.SetRangeCoverageSource(mockRangeCoverage{intervals: []BlockRange{{From: 200, To: 599}}})
	skip := true
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:    "bsc",
		Addresses:   []string{addrA},
		Datasets:    []string{DatasetTransactions},
		SkipCovered: &skip,
	})
	if err != nil {
		t.Fatal(err)
	}
	ds := store.ListDatasets()[0]
	ranges := store.ListRangesByDataset(ds.ID)
	if len(ranges) != 3 {
		t.Fatalf("部分复用后应 1 个复用 + 2 个缺失 Range，实际 %d", len(ranges))
	}
	pending, reused := 0, 0
	for _, r := range ranges {
		if r.Status == RangePending {
			pending++
		}
		if r.Status == RangeCompleted && r.Provider == "local_hit" {
			reused++
		}
	}
	if pending != 2 || reused != 1 {
		t.Fatalf("缺失/复用分布不符: pending=%d reused=%d", pending, reused)
	}
	cp, _ := svc.Checkpoint(ds.ID)
	if len(cp.CompletedRanges) != 1 || len(cp.PendingRanges) != 2 {
		t.Fatalf("checkpoint 复用不符: completed=%d pending=%d", len(cp.CompletedRanges), len(cp.PendingRanges))
	}
	entries, _ := NewLedger(store.Root(), ds.ID).Replay()
	reused, created := 0, 0
	for _, e := range entries {
		if e.Event == "RANGE_REUSED" {
			reused++
		}
		if e.Event == LedgerRangeCreated {
			created++
		}
	}
	if reused != 1 || created != 2 {
		t.Fatalf("账本不符: reused=%d created=%d", reused, created)
	}
	t.Cleanup(svc.Shutdown)
	if err := svc.Start(resp.Batch.ID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, resp.Batch.ID)
	waitFor(t, 30*time.Second, "差量校验完成", func() bool {
		cur := store.GetDataset(ds.ID)
		return cur != nil && cur.Validation != nil && cur.Status == DatasetCompleted
	})
	report := store.GetDataset(ds.ID).Validation
	if report.Status != "VALIDATED" || report.Coverage != 1.0 {
		t.Fatalf("差量校验 %s coverage=%v errors=%v", report.Status, report.Coverage, report.Errors)
	}
	parts := 0
	_ = filepath.Walk(filepath.Join(svc.PartsDir(), ds.ID), func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() &&
			(strings.HasSuffix(info.Name(), ".parquet") || strings.HasSuffix(info.Name(), ".jsonl")) {
			parts++
		}
		return nil
	})
	if parts != 2 {
		t.Fatalf("差量下载应只产生 2 个 Part，实际 %d", parts)
	}
	done, downloaded := 0, 0
	for _, r := range store.ListRangesByDataset(ds.ID) {
		if r.Status == RangeCompleted {
			done++
			if r.Provider != "local_hit" {
				downloaded++
			}
		}
	}
	if done != 3 || downloaded != 2 {
		t.Fatalf("完成后 Range: 总计=%d 实下载=%d，期望 3/2", done, downloaded)
	}
}

// TestMultiChainAddressOverride 验收：批量地址可逐地址指定不同链。
func TestMultiChainAddressOverride(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(dir))
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:  "bsc",
		Addresses: []string{addrA, addrB, addrC},
		Datasets:  []string{DatasetBalances},
		AddressChainOverrides: map[string]string{
			addrB: "eth",
			addrC: "base",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Batch.ChainKey != "multi" || resp.Batch.ChainID != 0 {
		t.Fatalf("混合链批次 chain=%s id=%d，期望 multi/0", resp.Batch.ChainKey, resp.Batch.ChainID)
	}
	want := map[string]struct {
		key string
		id  int64
	}{
		addrA: {"bsc", 56},
		addrB: {"eth", 1},
		addrC: {"base", 8453},
	}
	for _, a := range store.ListAddressesByBatch(resp.Batch.ID) {
		w := want[a.Address]
		if a.ChainKey != w.key || a.ChainID != w.id {
			t.Fatalf("地址 %s 链 %s/%d，期望 %s/%d", a.Address, a.ChainKey, a.ChainID, w.key, w.id)
		}
		for _, ds := range store.ListDatasetsByAddress(a.ID) {
			if ds.ChainKey != w.key {
				t.Fatalf("数据集 %s 链 %s，期望 %s", ds.ID, ds.ChainKey, w.key)
			}
		}
	}
	// 未知链覆盖必须报错
	if _, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:              "bsc",
		Addresses:             []string{addrA},
		Datasets:              []string{DatasetBalances},
		AddressChainOverrides: map[string]string{addrA: "solana"},
	}); err == nil {
		t.Fatal("未知链覆盖应报错")
	}
}

// TestResultExportXLSXAndCSV 验收：≤30 万行导出 XLSX，>30 万行导出 CSV（阈值可注入）。
func TestResultExportXLSXAndCSV(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
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
	t.Cleanup(svc.Shutdown)
	if err := svc.Start(resp.Batch.ID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, resp.Batch.ID)
	ds := store.ListDatasets()[0]
	waitFor(t, 30*time.Second, "导出前入库完成", func() bool {
		cur := store.GetDataset(ds.ID)
		return cur != nil && cur.Validation != nil && cur.Status == DatasetCompleted
	})
	ctx := context.Background()
	// XLSX 分支（默认阈值 30 万）
	xlsxPath, format, rows, err := svc.Results().ExportDataset(ctx, ds.ID)
	if err != nil {
		t.Fatal(err)
	}
	if format != "xlsx" || rows == 0 {
		t.Fatalf("导出格式 %s rows=%d，期望 xlsx/行数>0", format, rows)
	}
	book, err := excelize.OpenFile(xlsxPath)
	if err != nil {
		t.Fatalf("XLSX 无法打开: %v", err)
	}
	sheetRows, err := book.GetRows("Sheet1")
	book.Close()
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(sheetRows)) != rows+1 {
		t.Fatalf("XLSX 行数 %d，期望 %d（含表头）", len(sheetRows), rows+1)
	}
	// CSV 分支（阈值 0 → 全部 CSV）
	svc.Results().SetXLSXThreshold(0)
	csvPath, format, rows2, err := svc.Results().ExportDataset(ctx, ds.ID)
	if err != nil {
		t.Fatal(err)
	}
	if format != "csv" || rows2 != rows {
		t.Fatalf("CSV 导出 format=%s rows=%d，期望 csv/%d", format, rows2, rows)
	}
	payload, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(payload, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("CSV 缺少 UTF-8 BOM")
	}
	if int64(bytes.Count(payload, []byte("\n"))) != rows+1 {
		t.Fatalf("CSV 行数不符: %d", bytes.Count(payload, []byte("\n")))
	}
}

// TestExportNoScientificNotation 验收：导出文件不允许科学计数法（长数字按文本保护）。
func TestExportNoScientificNotation(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.csv")
	dst := filepath.Join(dir, "dst.csv")
	if err := os.WriteFile(src, []byte(
		"value_raw,gas_price,chain_id,block_number\n"+
			"62890000000000000000,1500000000000,56,114474218\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFinalCSV(src, dst); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(payload, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("CSV 缺少 BOM")
	}
	// 语义校验：Excel 解析后长数字单元格必须是 ="..." 文本公式，且不得出现科学计数法
	if bytes.Contains(payload, []byte("e+")) || bytes.Contains(payload, []byte("E+")) {
		t.Fatalf("导出内容出现科学计数法: %s", payload)
	}
	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csvReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("CSV 行数 %d，期望 2", len(rows))
	}
	if rows[1][0] != `="62890000000000000000"` || rows[1][1] != `="1500000000000"` {
		t.Fatalf("长数字未按文本保护: %v", rows[1])
	}
	if rows[1][2] != "56" || rows[1][3] != "114474218" {
		t.Fatalf("短数字不应被包裹: %v", rows[1])
	}
	// XLSX 分支：长数字单元格必须是字符串类型（t="s"/t="inlineStr"），禁止存成数值
	xlsxPath := filepath.Join(dir, "long.xlsx")
	if err := csvToXLSX(dst, xlsxPath); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(xlsxPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var sheetXML string
	for _, zf := range zr.File {
		if zf.Name == "xl/worksheets/sheet1.xml" {
			rc, _ := zf.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			sheetXML = string(b)
			break
		}
	}
	if sheetXML == "" {
		t.Fatal("sheet1.xml 不存在")
	}
	if !strings.Contains(sheetXML, `r="A2" t="s"`) && !strings.Contains(sheetXML, `r="A2" t="inlineStr"`) {
		t.Fatalf("XLSX 长数字未按字符串写入（可能出现科学计数法）: %s", sheetXML)
	}
}

// TestCloudTierAssignAndUpgrade 验收（设计 P0 Case 2）：Cloud L 运行中吞吐低 → 自动升级 XL，
// 已完成 Range 不重跑；账本记录 ASSIGNED/UPGRADED。
func TestCloudTierAssignAndUpgrade(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	opts := DefaultOptions()
	opts.Workers = 1
	opts.DefaultEndBlock = 399
	opts.RangeChunkSize = 200
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	cloud := NewMockCloudProvider()
	cloud.delay = 500 * time.Millisecond
	svc.RegisterAdapter(cloud)
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:  "bsc",
		Addresses: []string{addrA},
		Datasets:  []string{DatasetTokenTransfers},
	})
	if err != nil {
		t.Fatal(err)
	}
	ds := store.ListDatasets()[0]
	ds.EstimatedRows = 1_000_000
	ds.EstimatedBytes = 2 << 30
	ds.CloudEstimatedRuntimeSeconds = 5 * 60 // 原始估算 5 分钟（L 档）
	_ = store.SaveDataset(ds)
	t.Cleanup(svc.Shutdown)
	if err := svc.Start(resp.Batch.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		cur := store.GetDataset(ds.ID)
		if cur != nil && cur.CloudTier == "XL" {
			break
		}
		if cur != nil && cur.Status.Terminal() && cur.CloudTier != "XL" {
			t.Fatalf("任务结束但未升级: tier=%s rows=%d progress=%+v reasons=%v",
				cur.CloudTier, cur.EstimatedRows, cur.Progress, cur.CloudReasons)
		}
		time.Sleep(100 * time.Millisecond)
	}
	waitCompleted(t, svc, resp.Batch.ID)
	cur := store.GetDataset(ds.ID)
	if cur.CloudTier != "XL" || cur.CloudScore <= 0 || len(cur.CloudReasons) == 0 {
		t.Fatalf("Cloud 分档信息缺失: %+v", cur)
	}
	entries, _ := NewLedger(store.Root(), ds.ID).Replay()
	assigned, upgraded := false, false
	started := map[string]int{}
	completed := map[string]int{}
	for _, e := range entries {
		switch e.Event {
		case LedgerCloudTierAssigned:
			assigned = true
		case LedgerCloudTierUpgraded:
			upgraded = true
		case LedgerRangeStarted:
			started[e.RangeID]++
		case LedgerRangeCompleted:
			completed[e.RangeID]++
		}
	}
	if !assigned || !upgraded {
		t.Fatalf("账本缺少 ASSIGNED(%v)/UPGRADED(%v)", assigned, upgraded)
	}
	for _, r := range store.ListRangesByDataset(ds.ID) {
		key := BlockRange{From: r.FromBlock, To: r.ToBlock}.Key()
		if started[key] != 1 || completed[key] != 1 {
			t.Fatalf("Range %s 被重跑: started=%d completed=%d", key, started[key], completed[key])
		}
	}
}

type fakeCloudRuntime struct {
	submitted *cloudruntime.Job
}

func (f *fakeCloudRuntime) SubmitJob(_ context.Context, job cloudruntime.Job) (string, error) {
	cp := job
	f.submitted = &cp
	return job.ID, nil
}

func (f *fakeCloudRuntime) JobStatus(id string) (cloudruntime.Job, error) {
	return cloudruntime.Job{ID: id, State: "done"}, nil
}

func (f *fakeCloudRuntime) CancelJob(_ context.Context, _ string) error { return nil }

func (f *fakeCloudRuntime) Status() cloudruntime.Status {
	return cloudruntime.Status{State: cloudruntime.WorkerReady, Available: true}
}

// TestCloudAdapterCarriesTier 验收：Cloud Job 携带 S/L/XL 资源档。
func TestCloudAdapterCarriesTier(t *testing.T) {
	rt := &fakeCloudRuntime{}
	a := NewSQDCloudAdapter(rt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = a.ExecuteRange(ctx, RangeRequest{
		DatasetJobID: "d1", Address: addrA, Dataset: DatasetTokenTransfers,
		ChainKey: "bsc", ChainID: 56, FromBlock: 1, ToBlock: 10, CloudTier: "XL",
	})
	if rt.submitted == nil {
		t.Fatal("Cloud Job 未提交")
	}
	if rt.submitted.Tier != "XL" {
		t.Fatalf("Cloud Job tier=%s，期望 XL", rt.submitted.Tier)
	}
	if rt.submitted.FromBlock != 1 || rt.submitted.ToBlock != 10 || len(rt.submitted.Addresses) != 1 {
		t.Fatalf("Cloud Job 参数不符: %+v", rt.submitted)
	}
}

// TestBatchSnapshotWeighted 验收（设计 §9-§11）：Address/Batch 按工作量加权，不是简单平均。
func TestBatchSnapshotWeighted(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(dir))
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:  "bsc",
		Addresses: []string{addrA},
		Datasets:  []string{DatasetTransactions, DatasetLogs},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 手工构造：transactions 10k 行已完成，logs 2M 行未开始 → 地址进度应接近 0.5%（加权）
	datasets := store.ListDatasetsByAddress(store.ListAddressesByBatch(resp.Batch.ID)[0].ID)
	for _, d := range datasets {
		switch d.Dataset {
		case DatasetTransactions:
			d.EstimatedRows = 10_000
			d.Progress.Percent = 1
		case DatasetLogs:
			d.EstimatedRows = 2_000_000
			d.Progress.Percent = 0
		}
		_ = store.SaveDataset(d)
	}
	a := store.ListAddressesByBatch(resp.Batch.ID)[0]
	w, p := svc.addressWeightedProgress(a)
	if w <= 0 || p > 0.02 || p <= 0 {
		t.Fatalf("加权地址进度异常 w=%.0f p=%v（期望 ~0.5%%）", w, p)
	}
	snap := svc.BatchSnapshot(resp.Batch.ID)
	if snap == nil || snap.ProgressPercent != p {
		t.Fatalf("BatchSnapshot 不符: %+v", snap)
	}
	// Logs 也完成 → 100%
	for _, d := range datasets {
		if d.Dataset == DatasetLogs {
			d.Progress.Percent = 1
			_ = store.SaveDataset(d)
		}
	}
	if snap := svc.BatchSnapshot(resp.Batch.ID); snap.ProgressPercent != 1 {
		t.Fatalf("全部完成后进度 %v，期望 1", snap.ProgressPercent)
	}
}

// TestSnapshot10KPerformance 验收（设计 §65）：10K 地址 Snapshot 计算不随规模劣化。
func TestSnapshot10KPerformance(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	opts := DefaultOptions()
	opts.DefaultEndBlock = 999
	opts.RangeChunkSize = 1000
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	addrs := make([]string, 10_000)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("0x%040x", i+1)
	}
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:  "bsc",
		Addresses: addrs,
		Datasets:  []string{DatasetTransactions},
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	snap := svc.BatchSnapshot(resp.Batch.ID)
	elapsed := time.Since(start)
	if snap == nil {
		t.Fatal("snapshot 为空")
	}
	if snap.RangesTotal != 10_000 {
		t.Fatalf("地址总数 %d", snap.RangesTotal)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("10K snapshot 耗时 %v，超 2s", elapsed)
	}
	t.Logf("10K snapshot 耗时 %v", elapsed)
}

// TestResetETAOnProviderSwitch 验收（设计 §20）：Provider 切换后 ETA 重算。
func TestResetETAOnProviderSwitch(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(dir))
	ds := &DatasetJob{ID: "d1", CurrentProvider: "sqd"}
	ds.Progress = ProgressSnapshot{ETASeconds: 3600, ETAConfidence: 0.9}
	ds.CurrentProvider = "rpc"
	svc.resetETA(ds)
	if !ds.Progress.ETARecalculating || ds.Progress.ETASeconds != 0 {
		t.Fatalf("切换后 ETA 未重算: %+v", ds.Progress)
	}
	if e := svc.etaEngines["d1"]; e == nil || e.SampleCount() != 0 || e.Provider() != "rpc" {
		t.Fatalf("ETA 引擎未重置: %+v", e)
	}
}

// TestCoverageIndexFullHitAndQuery 验收（设计 Case A/B + §45）：认证后 FULL HIT，二次任务零网络复用。
func TestCoverageIndexFullHitAndQuery(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	opts := DefaultOptions()
	opts.Workers = 1
	opts.DefaultEndBlock = 399
	opts.RangeChunkSize = 200
	svc := NewService(store, opts, NewJSONLPartWriter(dir))
	svc.RegisterAdapter(NewMockProvider())
	resp, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:  "bsc",
		Addresses: []string{addrA},
		Datasets:  []string{DatasetTokenTransfers},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Shutdown)
	if err := svc.Start(resp.Batch.ID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, resp.Batch.ID)
	waitFor(t, 30*time.Second, "入库+覆盖索引", func() bool {
		for _, d := range store.ListDatasets() {
			if d.BatchID != resp.Batch.ID {
				continue
			}
			entry := svc.Results().find(d.ID)
			if entry != nil && entry.Certification == "CERTIFIED" {
				return true
			}
		}
		return false
	})
	// Coverage Query：同范围 FULL HIT
	query := svc.CoverageQuery("bsc", addrA, DatasetTokenTransfers, 0, 399)
	if !query.FullHit || query.CoverageRatio != 1 || len(query.Missing) != 0 {
		t.Fatalf("覆盖查询应 FULL HIT: %+v", query)
	}
	// 二次任务：零网络复用
	skip := true
	resp2, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:    "bsc",
		Addresses:   []string{addrA},
		Datasets:    []string{DatasetTokenTransfers},
		SkipCovered: &skip,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp2.LocalFullHits < 1 || resp2.ReusedRanges < 1 || resp2.LocalMisses != 0 {
		t.Fatalf("复用统计不符: %+v", resp2)
	}
	var ds2 *DatasetJob
	for _, d := range store.ListDatasets() {
		if d.BatchID == resp2.Batch.ID {
			ds2 = d
			break
		}
	}
	if ds2 == nil {
		t.Fatal("二次任务数据集不存在")
	}
	ranges2 := store.ListRangesByDataset(ds2.ID)
	for _, r := range ranges2 {
		if r.Provider != "local_hit" {
			t.Fatalf("二次任务应全部 LOCAL_REUSE，实际 %s", r.Provider)
		}
	}
	// 部分命中：请求更大范围 → 仅补缺口
	resp3, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey:     "bsc",
		Addresses:    []string{addrA},
		Datasets:     []string{DatasetTokenTransfers},
		DefaultRange: &RangeSpec{Mode: RangeModeBlock, FromBlock: 0, ToBlock: 599},
		SkipCovered:  &skip,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp3.LocalPartialHits < 1 || resp3.ReusedRanges < 1 {
		t.Fatalf("部分命中统计不符: %+v", resp3)
	}
}

// TestAdaptiveRangesCreation 验收（Phase B）：Discovery 驱动自适应 Range，均匀低密度 → 大跨度单 Range。
func TestAdaptiveRangesCreation(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	opts := DefaultOptions()
	opts.Workers = 1
	opts.DefaultEndBlock = 999
	opts.RangeChunkSize = 200
	opts.AdaptiveRanges = true
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
	ds := store.ListDatasets()[0]
	ranges := store.ListRangesByDataset(ds.ID)
	if len(ranges) != 1 {
		t.Fatalf("均匀低密度应合并为 1 个自适应 Range，实际 %d", len(ranges))
	}
	if ds.DiscoveryConfidence <= 0 || ds.SuggestedRangeSpan == 0 || len(ds.ActivitySegments) == 0 {
		t.Fatalf("Discovery 结果未落盘: conf=%v span=%d seg=%d",
			ds.DiscoveryConfidence, ds.SuggestedRangeSpan, len(ds.ActivitySegments))
	}
	t.Cleanup(svc.Shutdown)
	if err := svc.Start(resp.Batch.ID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, resp.Batch.ID)
	waitFor(t, 30*time.Second, "自适应批次校验完成", func() bool {
		cur := store.GetDataset(ds.ID)
		return cur != nil && cur.Validation != nil && cur.Status == DatasetCompleted
	})
	if report := store.GetDataset(ds.ID).Validation; report.Status != "VALIDATED" {
		t.Fatalf("自适应 Range 校验失败: %v", report.Errors)
	}
}

// TestHistoryRecordedAfterBatch 验收（Phase E）：任务完成后写入 Provider 历史画像。
func TestHistoryRecordedAfterBatch(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
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
	t.Cleanup(svc.Shutdown)
	if err := svc.Start(resp.Batch.ID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, resp.Batch.ID)
	ds := store.ListDatasets()[0]
	waitFor(t, 30*time.Second, "校验完成", func() bool {
		cur := store.GetDataset(ds.ID)
		return cur != nil && cur.Validation != nil
	})
	profile := svc.history.Profile(56, DatasetTransactions, "mock",
		feedback.ScaleBucket(store.GetDataset(ds.ID).EstimatedRows))
	if profile == nil || profile.Jobs == 0 {
		t.Fatal("历史画像未记录")
	}
	if profile.FinalSuccessCount == 0 {
		t.Fatal("最终验证成功未计入画像")
	}
}

// TestFeedbackActionLogged 验收（Phase D）：反馈动作写入 Ledger。
func TestFeedbackActionLogged(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(dir))
	svc.emitFeedbackAction(&DatasetJob{ID: "d1", CurrentProvider: "sqd"},
		feedback.Decision{Action: feedback.Throttle, Reason: "503 率高，先降并发"})
	entries, _ := NewLedger(dir, "d1").Replay()
	found := false
	for _, e := range entries {
		if e.Event == LedgerFeedbackAction {
			found = true
		}
	}
	if !found {
		t.Fatal("Ledger 缺少 FEEDBACK_ACTION")
	}
}

// TestValidationV3CertificateAndRepair 验收（设计 P0）：
// 缺口 → Gap Ledger/Repair Attempts → 补洞 → Validation Certificate（PASS，gaps repaired）→ CERTIFIED 发布。
func TestValidationV3CertificateAndRepair(t *testing.T) {
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
	// 模拟静默缺口：启动前取消第二个 Range（无数据）
	ds := store.ListDatasets()[0]
	ranges := store.ListRangesByDataset(ds.ID)
	ranges[1].Status = RangeCanceled
	now := time.Now().UTC()
	ranges[1].FinishedAt = &now
	ranges[1].UpdatedAt = now
	_ = store.SaveRange(ranges[1])
	t.Cleanup(svc.Shutdown)
	if err := svc.Start(resp.Batch.ID); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, svc, resp.Batch.ID)
	waitFor(t, 30*time.Second, "补洞后校验完成", func() bool {
		cur := store.GetDataset(ds.ID)
		return cur != nil && cur.Validation != nil && cur.Status == DatasetCompleted
	})
	gapStore := v3.NewGapStore(dir, ds.ID)
	cert, err := gapStore.LoadCertificate()
	if err != nil {
		t.Fatalf("Validation Certificate 未生成: %v", err)
	}
	if cert.Status != "PASS" || cert.Coverage != 1.0 {
		t.Fatalf("证书不符: status=%s coverage=%v", cert.Status, cert.Coverage)
	}
	if cert.GapsDetected < 1 || cert.GapsRepaired < 1 || cert.GapsRemaining != 0 {
		t.Fatalf("缺口统计不符: detected=%d repaired=%d remaining=%d",
			cert.GapsDetected, cert.GapsRepaired, cert.GapsRemaining)
	}
	if cert.DuplicateSHA != 0 || cert.DuplicatesRemoved != 0 {
		t.Fatalf("证书重复统计异常: dup_sha=%d dup_rows=%d", cert.DuplicateSHA, cert.DuplicatesRemoved)
	}
	if cert.RowsRaw != cert.RowsFinal || cert.RowsRaw == 0 {
		t.Fatalf("证书行数不符: raw=%d final=%d", cert.RowsRaw, cert.RowsFinal)
	}
	if state := gapStore.LoadState(); state.Status != v3.StatePass {
		t.Fatalf("Validation State=%s，期望 PASS", state.Status)
	}
	gaps, _ := gapStore.LoadGaps()
	if len(gaps) < 2 {
		t.Fatalf("Gap Ledger 应有 DETECTED+REPAIRED 两条，实际 %d", len(gaps))
	}
	repairs, _ := gapStore.LoadRepairs()
	successRepair := false
	for _, r := range repairs {
		if r.Success {
			successRepair = true
		}
	}
	if !successRepair {
		t.Fatal("Repair Attempts 缺少成功记录")
	}
	entry := svc.Results().find(ds.ID)
	if entry == nil || entry.Certification != "CERTIFIED" {
		t.Fatalf("Registry 未认证发布: %+v", entry)
	}
}

func csvReadAll(r io.Reader) ([][]string, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	return cr.ReadAll()
}

// TestResultProcessorMergeAndQuery 验收：结果合并仓库 + Registry + 服务端分页。
func TestResultProcessorMergeAndQuery(t *testing.T) {
	engine := openTestDuckDB(t)
	if engine == nil {
		t.Skip("DuckDB 不可用")
	}
	dir := t.TempDir()
	store, _ := NewStore(dir)
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
	waitFor(t, 30*time.Second, "结果入库", func() bool {
		entry := svc.Results().find(ds.ID)
		return entry != nil && entry.MergedParquet != "" && entry.RowCount > 0
	})
	entry := svc.Results().find(ds.ID)
	if entry.Dataset != DatasetTransactions || entry.Address != addrA {
		t.Fatalf("Registry 条目不符: %+v", entry)
	}
	rows, total, err := svc.Results().QueryResults(context.Background(), ds.ID, 1, 10, "block_number", "")
	if err != nil {
		t.Fatal(err)
	}
	if total != entry.RowCount || len(rows) > 10 || len(rows) == 0 {
		t.Fatalf("分页结果不符: total=%d rows=%d", total, len(rows))
	}
	if _, ok := rows[0]["source_provider"]; !ok {
		t.Fatalf("结果缺少溯源列: %v", rows[0])
	}
	if _, ok := rows[0]["chain"]; ok {
		t.Fatalf("merged 结果混入多余 chain 列: %v", rows[0])
	}
}
