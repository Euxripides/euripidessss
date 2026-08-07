package downloadengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifestFinalizerAtomic(t *testing.T) {
	dir := t.TempDir()
	f := NewManifestFinalizer(dir)

	m := &ManifestV2{
		Version:        2,
		JobID:          "job-001",
		ChainID:        "bsc",
		DatasetTypes:   []string{"transactions"},
		Status:         "COMPLETED",
		RowsTotal:      1000,
		CoverageStatus: "FULL",
	}

	if err := f.Finalize("job-001", m); err != nil {
		t.Fatal(err)
	}

	// 验证文件存在且无 .tmp 残留
	manifestPath := filepath.Join(dir, "job-001-manifest.json")
	tmpPath := manifestPath + ".tmp"

	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		t.Fatal("manifest.json should exist after Finalize")
	}
	if _, err := os.Stat(tmpPath); err == nil {
		t.Fatal(".tmp file should NOT exist after Finalize")
	}

	// 验证内容
	data, _ := os.ReadFile(manifestPath)
	var loaded ManifestV2
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.JobID != "job-001" {
		t.Errorf("expected job-001, got %s", loaded.JobID)
	}
}

func TestCompletionGateBlocksOnFailedChunk(t *testing.T) {
	gate := NewCompletionGate()
	chunks := []*Chunk{
		{ID: "c1", Status: ChunkSucceeded},
		{ID: "c2", Status: ChunkFailed},
	}

	err := gate.Verify(&Job{ID: "job-001"}, chunks, true, true, true)
	if err == nil {
		t.Fatal("should block when chunk failed")
	}
}

func TestCompletionGatePasses(t *testing.T) {
	gate := NewCompletionGate()
	chunks := []*Chunk{
		{ID: "c1", Status: ChunkSucceeded},
		{ID: "c2", Status: ChunkSkipped},
	}

	err := gate.Verify(&Job{ID: "job-001"}, chunks, true, true, true)
	if err != nil {
		t.Fatalf("should pass all checks: %v", err)
	}
}

func TestDatasetRegistryCRUD(t *testing.T) {
	dir := t.TempDir()
	r := NewDatasetRegistry(dir)

	rec := &DatasetRecord{
		DatasetID:   "ds-001",
		JobID:       "job-001",
		ChainID:     "bsc",
		DatasetType: "transactions",
		StartBlock:  8000000,
		EndBlock:    9000000,
		RowCount:    50000,
		Checksum:    "abc123",
	}

	if err := r.Register(rec); err != nil {
		t.Fatal(err)
	}

	// 查询
	results := r.Query("bsc", "transactions", 7000000, 10000000)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Checksum != "abc123" {
		t.Errorf("expected abc123, got %s", results[0].Checksum)
	}

	// 范围外查询
	empty := r.Query("bsc", "transactions", 9000001, 10000000)
	if len(empty) != 0 {
		t.Errorf("expected 0 results, got %d", len(empty))
	}
}

func TestDatasetRegistryIdempotent(t *testing.T) {
	dir := t.TempDir()
	r := NewDatasetRegistry(dir)

	rec := &DatasetRecord{
		DatasetID: "ds-001", JobID: "job-001", ChainID: "bsc",
		DatasetType: "transactions", StartBlock: 8000000, EndBlock: 9000000,
		Checksum: "abc123",
	}

	if err := r.Register(rec); err != nil {
		t.Fatal(err)
	}
	// 幂等：相同 checksum 不报错
	if err := r.Register(rec); err != nil {
		t.Fatalf("idempotent register should succeed: %v", err)
	}
}

func TestFeatureFlagsDefault(t *testing.T) {
	flags := DefaultFeatureFlags()
	if flags.IsEnabled() {
		t.Fatal("V2 should be disabled by default")
	}
	if !flags.IsChainEnabled("bsc") {
		t.Fatal("bsc should be enabled by default")
	}
	if flags.IsChainEnabled("eth") {
		t.Fatal("eth should NOT be enabled by default")
	}
	if !flags.AutoFirstSeen {
		t.Fatal("auto_first_seen should default to true")
	}
}

func TestFeatureFlagsGrayStrategy(t *testing.T) {
	// 灰度策略：先 BSC 单地址，再逐步扩大
	flags := &FeatureFlags{
		DownloadEngineV2: true,
		EnabledChains:    []string{"bsc"},
		AutoFirstSeen:    true,
	}

	// 模拟灰度第一阶段
	if !flags.IsEnabled() || !flags.IsChainEnabled("bsc") {
		t.Fatal("阶段1：BSC应启用")
	}

	// 模拟第二阶段
	flags.EnabledChains = append(flags.EnabledChains, "eth")
	if !flags.IsChainEnabled("eth") {
		t.Fatal("阶段2：ETH应启用")
	}

	// 验证未启用的链
	if flags.IsChainEnabled("arbitrum") {
		t.Fatal("arbitrum 尚未灰度")
	}
}

func TestRateLimiter(t *testing.T) {
	limiter := NewRateLimiter(2)
	ctx := context.Background()

	if err := limiter.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := limiter.Acquire(ctx); err != nil {
		t.Fatal(err)
	}

	if limiter.Active() != 2 {
		t.Errorf("expected 2 active, got %d", limiter.Active())
	}

	limiter.Release()
	if limiter.Active() != 1 {
		t.Errorf("expected 1 active after release, got %d", limiter.Active())
	}

	limiter.Release()
	if limiter.Active() != 0 {
		t.Errorf("expected 0 active, got %d", limiter.Active())
	}
}

func TestRateLimiterBlocksOnFull(t *testing.T) {
	limiter := NewRateLimiter(1)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := limiter.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	// 第二次应超时
	if err := limiter.Acquire(ctx); err == nil {
		t.Fatal("should timeout when full")
	}
	limiter.Release()
}
