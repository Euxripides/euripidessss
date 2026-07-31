package downloadengine

import (
	"testing"
	"time"
)

// ── V2.1 RC2 Dataset Registry 增强测试 ──

func TestBlockCoverageCovers(t *testing.T) {
	bc := NewBlockCoverage()
	bc.Add(44500000, 44501000)

	if !bc.Covers(44500000, 44500500) {
		t.Error("should cover sub-range")
	}
	if bc.Covers(44600000, 44600100) {
		t.Error("should NOT cover outside range")
	}
	t.Logf("  Coverage: covers sub-range ✅, rejects outside ✅")
}

func TestBlockCoverageMissing(t *testing.T) {
	bc := NewBlockCoverage()
	bc.Add(44500000, 44500500)
	bc.Add(44500800, 44501000)

	missing := bc.Missing(44500000, 44501000)
	if len(missing) != 1 || missing[0].start != 44500501 || missing[0].end != 44500799 {
		t.Errorf("expected gap 44500501-44500799, got %+v", missing)
	}
	t.Logf("  Missing ranges: %+v", missing)
}

func TestBlockCoverageMerge(t *testing.T) {
	bc := NewBlockCoverage()
	bc.Add(44500000, 44500500)
	bc.Add(44500400, 44501000) // overlaps

	if len(bc.segments) != 1 || bc.segments[0].start != 44500000 || bc.segments[0].end != 44501000 {
		t.Errorf("expected single merged segment 44500000-44501000, got %+v", bc.segments)
	}
	t.Logf("  Merged: %d segments", len(bc.segments))
}

func TestEnhancedRegistryDuplicateTask(t *testing.T) {
	er := NewEnhancedRegistry()

	ds := &EnhancedDataset{
		ID: "ds-001", Chain: "bsc", Type: DSTransactions,
		StartBlock: 44500000, EndBlock: 44501000,
		Status: DsReady, RowCount: 122892, SizeBytes: 4_350_000,
	}
	er.RegisterDataset(ds)

	// 重复创建 → 缓存命中
	ds2 := &EnhancedDataset{
		ID: "ds-001", Chain: "bsc", Type: DSTransactions,
		StartBlock: 44500000, EndBlock: 44501000,
		Status: DsCreated, RowCount: 0,
	}
	er.RegisterDataset(ds2)

	metrics := er.MetricsSnapshot()
	if metrics["dataset_cache_hit"].(int64) != 1 {
		t.Errorf("expected 1 cache hit, got %d", metrics["dataset_cache_hit"])
	}
	t.Logf("  Duplicate task: cache hit ✅")
}

func TestEnhancedRegistryCoverageCacheHit(t *testing.T) {
	er := NewEnhancedRegistry()

	er.RegisterDataset(&EnhancedDataset{
		ID: "ds-001", Chain: "bsc", Type: DSTransactions,
		StartBlock: 44500000, EndBlock: 44501000, Status: DsReady,
	})

	if !er.HasCoverage("bsc", DSTransactions, 44500000, 44500500) {
		t.Error("should hit coverage cache")
	}
	metrics := er.MetricsSnapshot()
	if metrics["dataset_cache_hit"].(int64) < 1 {
		t.Errorf("expected cache hit, got %d", metrics["dataset_cache_hit"])
	}
	t.Logf("  Coverage cache hit ✅, miss=%d, hit=%d", metrics["dataset_cache_miss"], metrics["dataset_cache_hit"])
}

func TestConsistencyCheckPass(t *testing.T) {
	cc := NewConsistencyCheck(122892, 122892, 122892)
	if err := cc.Validate(); err != nil {
		t.Fatalf("should pass: %v", err)
	}
	t.Logf("  Consistency: source=parquet=duckdb=122892 ✅")
}

func TestConsistencyCheckFail(t *testing.T) {
	cc := NewConsistencyCheck(122892, 100000, 122892)
	if err := cc.Validate(); err == nil {
		t.Fatal("should fail on mismatch")
	}
	t.Logf("  Consistency fail (expected): %v", cc.Validate())
}

func TestEnhancedRegistryFileCorruption(t *testing.T) {
	er := NewEnhancedRegistry()

	er.RegisterFile(&FileEntry{DatasetID: "ds-001", FilePath: "/data/bsc/tx.parquet", Checksum: "abc123"})
	er.MarkValidationFailed("ds-001")

	metrics := er.MetricsSnapshot()
	if metrics["dataset_validation_fail"].(int64) != 1 {
		t.Errorf("expected 1 validation fail, got %d", metrics["dataset_validation_fail"])
	}
	t.Logf("  File corruption: validation_fail=1 ✅")
}

func TestEnhancedRegistryLifecycle(t *testing.T) {
	er := NewEnhancedRegistry()

	// CREATED → DOWNLOADING → VALIDATING → READY
	ds := &EnhancedDataset{ID: "ds-life", Chain: "bsc", Type: DSTransactions, StartBlock: 44500000, EndBlock: 44501000}

	ds.Status = DsCreated
	er.RegisterDataset(ds)

	ds.Status = DsDownloading
	er.RegisterDataset(ds)

	ds.Status = DsValidating
	er.RegisterDataset(ds)

	ds.Status = DsReady
	ds.RowCount = 122892
	er.RegisterDataset(ds)

	snap := er.MetricsSnapshot()
	if snap["dataset_ready_total"].(int64) != 1 {
		t.Errorf("expected 1 ready, got %d", snap["dataset_ready_total"])
	}
	t.Logf("  Lifecycle: CREATED→DOWNLOADING→VALIDATING→READY ✅, metrics=%+v", snap)
}

func TestAddressCoverage(t *testing.T) {
	ac := NewAddressCoverage()
	ac.Mark("0x55d398326f99059ff775485246999027b3197955", &AddressCoverageEntry{
		Address: "0x55d398326f99059ff775485246999027b3197955",
		Chain: "bsc", DatasetID: "ds-001", StartBlock: 44500000, EndBlock: 44501000,
		Status: "covered", UpdatedAt: time.Now(),
	})

	if !ac.IsCovered("bsc", "0x55d398326f99059ff775485246999027b3197955") {
		t.Error("USDT should be covered")
	}
	if ac.IsCovered("bsc", "0xunknown") {
		t.Error("unknown address should NOT be covered")
	}
	t.Logf("  Address coverage: USDT ✅, unknown ❌")
}

func TestSchemaMigration(t *testing.T) {
	if len(SchemaMigrations) < 1 {
		t.Error("should have at least 1 schema migration")
	}
	m := SchemaMigrations[0]
	if m.FromVersion != 1 || m.ToVersion != 2 {
		t.Errorf("expected v1→v2, got v%d→v%d", m.FromVersion, m.ToVersion)
	}
	t.Logf("  Schema migration: %s (v%d→v%d)", m.Description, m.FromVersion, m.ToVersion)
}
