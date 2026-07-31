package downloadengine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── V2.1 RC2 Parquet 数据资产库测试 ──

func TestAssetLibraryPartitionPath(t *testing.T) {
	lib := NewParquetAssetLibrary("E:\\bsc-data", "bsc")
	path := lib.PartitionPath(DSTransactions, 44500000)
	t.Logf("  Partition path: %s", path)
	if filepath.Base(path) == "" {
		t.Error("partition path should not be empty")
	}
}

func TestAssetLibraryManifestRegister(t *testing.T) {
	lib := NewParquetAssetLibrary(t.TempDir(), "bsc")

	m := &AssetManifest{
		Dataset:     DSTransactions,
		Chain:       "bsc",
		StartBlock:  44500000,
		EndBlock:    44501000,
		Rows:        122892,
		SizeBytes:   4_350_000,
		Compression: "zstd",
		FilePath:    lib.FilePath(DSTransactions, 44500000, 44501000),
		Status:      "complete",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	lib.RegisterManifest(m)

	if !lib.HasBlockRange(DSTransactions, 44500000, 44500500) {
		t.Error("should have block range 44500000-44500500")
	}

	// 超出范围的查询
	if lib.HasBlockRange(DSTransactions, 44600000, 44600100) {
		t.Error("should NOT have block range outside registered")
	}
}

func TestAssetLibraryMissingRanges(t *testing.T) {
	lib := NewParquetAssetLibrary(t.TempDir(), "bsc")

	// 只注册 44500000-44501000
	lib.RegisterManifest(&AssetManifest{
		Dataset: DSTransactions, StartBlock: 44500000, EndBlock: 44501000,
		Status: "complete",
	})

	// 请求 44500000-44503000 → 应返回缺失 [44501001, 44503000]
	missing := lib.MissingRanges(DSTransactions, 44500000, 44503000)
	if len(missing) != 1 || missing[0].Start != 44501001 {
		t.Errorf("expected 1 missing range starting at 44501001, got %d: %+v", len(missing), missing)
	}
	t.Logf("  Missing ranges: %+v", missing)
}

func TestAssetLibraryIncrementalPlan(t *testing.T) {
	lib := NewParquetAssetLibrary(t.TempDir(), "bsc")

	// 已下载 50% 的数据
	lib.RegisterManifest(&AssetManifest{Dataset: DSTransactions, StartBlock: 44500000, EndBlock: 44500500, Status: "complete", FilePath: "/data/bsc/tx_half.parquet"})

	download, skip := lib.IncrementalPlan(DSTransactions, 44500000, 44501000)
	t.Logf("  Download: %d ranges, skip: %d", len(download), skip)
	if len(download) == 0 {
		t.Error("should have at least 1 download range for missing data")
	}
}

func TestAssetLibraryDedupKeys(t *testing.T) {
	lib := NewParquetAssetLibrary(t.TempDir(), "bsc")

	if lib.DedupKey(DSTransactions) != "tx_hash" {
		t.Error("transactions dedup key should be tx_hash")
	}
	if lib.DedupKey(DSLogs) != "tx_hash||log_index" {
		t.Error("logs dedup key should be tx_hash||log_index")
	}
	if lib.DedupKey(DSTransfers) != "tx_hash||event_index" {
		t.Error("transfers dedup key should be tx_hash||event_index")
	}
}

func TestAssetLibrarySchemas(t *testing.T) {
	lib := NewParquetAssetLibrary(t.TempDir(), "bsc")

	if len(lib.Schema(DSTransactions)) != 8 {
		t.Errorf("transactions should have 8 fields, got %d", len(lib.Schema(DSTransactions)))
	}
	if len(lib.Schema(DSLogs)) != 7 {
		t.Errorf("logs should have 7 fields, got %d", len(lib.Schema(DSLogs)))
	}
}

func TestAssetLibraryValidateDataset(t *testing.T) {
	lib := NewParquetAssetLibrary(t.TempDir(), "bsc")
	dir := t.TempDir()

	// Invalid file
	err := lib.ValidateDataset(filepath.Join(dir, "nonexistent.parquet"), nil)
	if err == nil {
		t.Error("should fail for nonexistent file")
	}
	t.Logf("  Validation error (expected): %v", err)
}

func TestAssetLibraryTierComputation(t *testing.T) {
	lib := NewParquetAssetLibrary(t.TempDir(), "bsc")

	if lib.ComputeTier(time.Now(), 1e6) != TierHot {
		t.Error("recent file should be HOT")
	}
	if lib.ComputeTier(time.Now().Add(-30*24*time.Hour), 1e6) != TierCold {
		t.Error("old small file should be COLD")
	}
	if lib.ComputeTier(time.Now().Add(-30*24*time.Hour), 20e9) != TierArch {
		t.Error("old large file should be ARCH")
	}
}

func TestAssetLibraryStats(t *testing.T) {
	lib := NewParquetAssetLibrary(t.TempDir(), "bsc")
	lib.RegisterManifest(&AssetManifest{Dataset: DSTransactions, Rows: 122892, SizeBytes: 4_350_000, Status: "complete", FilePath: "/data/bsc/tx.parquet"})
	lib.RegisterManifest(&AssetManifest{Dataset: DSLogs, Rows: 6946, SizeBytes: 500_000, Status: "complete", FilePath: "/data/bsc/log.parquet"})

	stats := lib.Stats()
	if stats["total_files"].(int) != 2 {
		t.Errorf("expected 2 files, got %d", stats["total_files"])
	}
	t.Logf("  Stats: %+v", stats)
}

func TestAssetLibraryRollback(t *testing.T) {
	dir := t.TempDir()
	lib := NewParquetAssetLibrary(dir, "bsc")

	// 创建文件并注册
	path := filepath.Join(dir, "test.parquet")
	_ = os.WriteFile(path, []byte("PAR1"), 0644)
	lib.RegisterManifest(&AssetManifest{Dataset: DSTransactions, StartBlock: 44501000, Status: "complete", FilePath: path})

	// Rollback → 删除
	if err := lib.Rollback(DSTransactions, 44500000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should be deleted after rollback")
	}
}

func TestAssetLibraryQueryOrDownload(t *testing.T) {
	lib := NewParquetAssetLibrary(t.TempDir(), "bsc")

	// 无数据 → 需要下载
	_, need := lib.QueryOrDownload(DSTransactions, 44500000, 44500100)
	if !need {
		t.Error("should need download when no data")
	}

	// 注册数据 → 直接查询
	lib.RegisterManifest(&AssetManifest{Dataset: DSTransactions, StartBlock: 44500000, EndBlock: 44501000, Status: "complete", FilePath: "/data/bsc/tx.parquet"})
	paths, need := lib.QueryOrDownload(DSTransactions, 44500000, 44500500)
	if need {
		t.Error("should NOT need download when data exists")
	}
	if len(paths) == 0 {
		t.Error("should return file paths")
	}
	t.Logf("  Query: %d paths, needDownload=%v", len(paths), need)
}
