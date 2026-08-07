package datasetsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/s3store"
)

func writeParquet(t *testing.T, engine *duckdb.Engine, csvPath, parquetPath string) {
	t.Helper()
	sql := fmt.Sprintf(
		"COPY (SELECT * FROM read_csv_auto('%s')) TO '%s' (FORMAT PARQUET, COMPRESSION GZIP)",
		filepath.ToSlash(csvPath), filepath.ToSlash(parquetPath),
	)
	if _, err := engine.ExecSQL(context.Background(), sql); err != nil {
		t.Fatalf("create parquet: %v", err)
	}
}

func testDuckDB(t *testing.T, dataDir string) *duckdb.Engine {
	t.Helper()
	engine := duckdb.Open(`E:\codex\etl`, dataDir, duckdb.AnalyticsConfig{})
	if !engine.Available() {
		t.Skip("duckdb.exe 不可用，跳过真实 parquet 校验")
	}
	return engine
}

func TestRegistryCoverageAndStats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	r, err := NewRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	e := &Entry{
		ChunkKey: "job1/chunk1", JobID: "job1", ChunkID: "chunk1",
		ChainKey: "bsc", ChainID: 56, Dataset: "token_transfer",
		Addresses: []string{"0xaaa", "0xbbb"}, RowCount: 100,
	}
	if err := r.Register(e); err != nil {
		t.Fatal(err)
	}
	if !r.Has("job1/chunk1") {
		t.Fatal("entry not found")
	}
	n, err := r.AddressTxCount(context.Background(), "0xAAA")
	if err != nil || n != 100 {
		t.Fatalf("address count = %d, %v", n, err)
	}
	// 重载验证持久化
	r2, err := NewRegistry(path)
	if err != nil || !r2.Has("job1/chunk1") {
		t.Fatalf("reload failed: %v", err)
	}
}

func TestSyncManifestValidateAndMerge(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	engine := testDuckDB(t, dataDir)
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	registry, err := NewRegistry(filepath.Join(root, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}

	// 构造 parquet
	csv := "chain_id,block_number,block_timestamp,transaction_hash,log_index,token_address,from_address,to_address,value_raw\n" +
		"56,1,1700000000,0xtx1,0,0xtoken,0xaaa,0xbbb,100\n" +
		"56,2,1700000100,0xtx2,0,0xtoken,0xbbb,0xccc,200\n" +
		"56,3,1700000200,0xtx3,0,0xtoken,0xaaa,0xddd,300\n"
	csvPath := filepath.Join(root, "rows.csv")
	parquetPath := filepath.Join(root, "part.parquet")
	if err := os.WriteFile(csvPath, []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	writeParquet(t, engine, csvPath, parquetPath)
	body, err := os.ReadFile(parquetPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)

	// 构造 completed manifest + parquet 到对象存储
	completedDir := "bsc/jobs/completed/job1/chunk1"
	if err := store.Put(context.Background(), completedDir+"/token_transfers/part.parquet", body); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"schema_version": 1, "job_id": "job1", "chunk_id": "chunk1",
		"provider": "SQD_CLOUD_EXPORT", "chain_id": 56, "dataset": "token_transfer",
		"from_block": 1, "to_block": 3, "addresses": []string{"0xaaa", "0xbbb", "0xccc", "0xddd"},
		"row_count": 3,
		"files": []map[string]any{{
			"path": "token_transfers/part.parquet", "bytes": len(body), "sha256": hex.EncodeToString(sum[:]),
		}},
		"completed": true,
	}
	manifestPayload, _ := json.MarshalIndent(manifest, "", "  ")
	if err := store.Put(context.Background(), completedDir+"/manifest.json", manifestPayload); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), completedDir+"/_SUCCESS", []byte(`{"completed":true}`)); err != nil {
		t.Fatal(err)
	}

	validator := NewDuckDBValidator(engine)
	syncer := NewSyncer(store, registry, filepath.Join(root, "local"), validator)
	results, err := syncer.SyncAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ChunkKey != "job1/chunk1" || results[0].Rows != 3 {
		t.Fatalf("sync results = %+v", results)
	}
	e := registry.Get("job1/chunk1")
	if e == nil || e.RowCount != 3 || e.UniqueKeyCount != 3 || e.DuplicateCount != 0 {
		t.Fatalf("entry = %+v", e)
	}
	if e.MergedParquet == "" {
		t.Fatal("merged parquet missing")
	}
	if _, err := os.Stat(e.MergedParquet); err != nil {
		t.Fatalf("merged parquet not on disk: %v", err)
	}
	n, _ := registry.AddressTxCount(context.Background(), "0xAAA")
	if n != 3 {
		t.Fatalf("coverage count = %d, want 3", n)
	}
	// 第二 chunk：1 条重复 + 1 条新行，验证 merged 全量合并且去重
	csv2 := "chain_id,block_number,block_timestamp,transaction_hash,log_index,token_address,from_address,to_address,value_raw\n" +
		"56,3,1700000200,0xtx3,0,0xtoken,0xaaa,0xddd,300\n" +
		"56,4,1700000300,0xtx4,0,0xtoken,0xaaa,0xeee,400\n"
	csvPath2 := filepath.Join(root, "rows2.csv")
	parquetPath2 := filepath.Join(root, "part2.parquet")
	if err := os.WriteFile(csvPath2, []byte(csv2), 0o644); err != nil {
		t.Fatal(err)
	}
	writeParquet(t, engine, csvPath2, parquetPath2)
	body2, err := os.ReadFile(parquetPath2)
	if err != nil {
		t.Fatal(err)
	}
	sum2 := sha256.Sum256(body2)
	completedDir2 := "bsc/jobs/completed/job1/chunk2"
	if err := store.Put(context.Background(), completedDir2+"/token_transfers/part2.parquet", body2); err != nil {
		t.Fatal(err)
	}
	manifest2 := map[string]any{
		"schema_version": 1, "job_id": "job1", "chunk_id": "chunk2",
		"provider": "SQD_CLOUD_EXPORT", "chain_id": 56, "dataset": "token_transfer",
		"from_block": 3, "to_block": 4, "addresses": []string{"0xaaa", "0xddd", "0xeee"},
		"row_count": 2,
		"files": []map[string]any{{
			"path": "token_transfers/part2.parquet", "bytes": len(body2), "sha256": hex.EncodeToString(sum2[:]),
		}},
		"completed": true,
	}
	manifestPayload2, _ := json.MarshalIndent(manifest2, "", "  ")
	if err := store.Put(context.Background(), completedDir2+"/manifest.json", manifestPayload2); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), completedDir2+"/_SUCCESS", []byte(`{"completed":true}`)); err != nil {
		t.Fatal(err)
	}
	results, err = syncer.SyncAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("after chunk2 sync results = %+v", results)
	}
	e2 := registry.Get("job1/chunk2")
	if e2 == nil || e2.RowCount != 2 || e2.DuplicateCount != 0 {
		t.Fatalf("chunk2 entry = %+v", e2)
	}
	mergedRows, err := engine.ExecSQLJSON(context.Background(),
		fmt.Sprintf("SELECT COUNT(*) AS n FROM read_parquet('%s')", strings.ReplaceAll(e2.MergedParquet, "\\", "/")))
	if err != nil || len(mergedRows) == 0 || int64(num(mergedRows[0]["n"])) != 4 {
		t.Fatalf("merged rows = %+v, %v; want 4 (dedup across chunks)", mergedRows, err)
	}
	// 二次同步应跳过
	results2, _ := syncer.SyncAll(context.Background())
	if len(results2) != 2 {
		t.Fatalf("second sync = %+v, want both skipped", results2)
	}
	for _, r := range results2 {
		if !r.Skipped {
			t.Fatalf("second sync result not skipped: %+v", r)
		}
	}
}

func TestSyncBadSHARejected(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	registry, _ := NewRegistry(filepath.Join(root, "registry.json"))
	completedDir := "bsc/jobs/completed/jobX/chunk1"
	_ = store.Put(context.Background(), completedDir+"/token_transfers/a.parquet", []byte("not-parquet"))
	manifest := map[string]any{
		"schema_version": 1, "job_id": "jobX", "chunk_id": "chunk1", "chain_id": 56, "dataset": "token_transfer",
		"row_count": 1, "addresses": []string{"0xaaa"},
		"files": []map[string]any{{
			"path": "token_transfers/a.parquet", "bytes": 12, "sha256": "deadbeef",
		}},
	}
	payload, _ := json.Marshal(manifest)
	_ = store.Put(context.Background(), completedDir+"/manifest.json", payload)
	_ = store.Put(context.Background(), completedDir+"/_SUCCESS", []byte(`{"completed":true}`))
	syncer := NewSyncer(store, registry, filepath.Join(root, "local"), nil)
	results, err := syncer.SyncAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("bad sha chunk must not be indexed, got %+v", results)
	}
	e := registry.Get("jobX/chunk1")
	if e == nil || e.Status != StatusFailed || e.SyncState != SyncLocalFailed {
		t.Fatalf("bad sha chunk must be marked FAILED/LOCAL_SYNC_FAILED for retry, got %+v", e)
	}
}

func TestSyncLegacyManifestGovernance(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	registry, _ := NewRegistry(filepath.Join(root, "registry.json"))
	syncer := NewSyncer(store, registry, filepath.Join(root, "local"), nil)

	// 1) 无 schema_version → legacy_invalid，跳过不登记
	dir1 := "bsc/jobs/completed/legacy1/chunk-1"
	_ = store.Put(context.Background(), dir1+"/_SUCCESS", []byte(`{"completed":true}`))
	_ = store.Put(context.Background(), dir1+"/manifest.json", []byte(`{"job_id":"legacy1","chunk_id":"chunk-1","row_count":0}`))
	// 2) 有 schema_version 但路径缺 token_transfers/ 前缀 → 跳过不登记
	dir2 := "bsc/jobs/completed/legacy2/chunk-1"
	_ = store.Put(context.Background(), dir2+"/_SUCCESS", []byte(`{"completed":true}`))
	_ = store.Put(context.Background(), dir2+"/manifest.json", []byte(`{"schema_version":1,"job_id":"legacy2","chunk_id":"chunk-1","row_count":0,"files":[{"path":"x.parquet"}]}`))
	// 3) 缺少 _SUCCESS → 跳过
	dir3 := "bsc/jobs/completed/legacy3/chunk-1"
	_ = store.Put(context.Background(), dir3+"/manifest.json", []byte(`{"schema_version":1,"job_id":"legacy3","chunk_id":"chunk-1","row_count":0,"files":[]}`))

	results, err := syncer.SyncAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("legacy manifests must be skipped, got %+v", results)
	}
	for _, key := range []string{"legacy1/chunk-1", "legacy2/chunk-1", "legacy3/chunk-1"} {
		if registry.Has(key) {
			t.Fatalf("%s must not be registered", key)
		}
	}
}

func TestQuarantineExcludedFromMergeCoverage(t *testing.T) {
	root := t.TempDir()
	registry, _ := NewRegistry(filepath.Join(root, "registry.json"))
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	_ = os.MkdirAll(filepath.Join(dirA, "token_transfers"), 0o755)
	_ = os.MkdirAll(filepath.Join(dirB, "token_transfers"), 0o755)
	_ = os.WriteFile(filepath.Join(dirA, "token_transfers", "a.parquet"), []byte("a"), 0o644)
	_ = os.WriteFile(filepath.Join(dirB, "token_transfers", "b.parquet"), []byte("b"), 0o644)
	ctx := context.Background()
	if err := registry.Register(&Entry{
		ChunkKey: "active/chunk1", JobID: "active", ChunkID: "chunk1",
		ChainKey: "bsc", Dataset: "token_transfer", FromBlock: 1, ToBlock: 10,
		Addresses: []string{"0xaaa"}, RowCount: 5, SyncState: SyncIndexed, LocalDir: dirA,
		Files: []FileInfo{{Path: "a.parquet", Bytes: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&Entry{
		ChunkKey: "bad/chunk1", JobID: "bad", ChunkID: "chunk1",
		ChainKey: "bsc", Dataset: "token_transfer", FromBlock: 1, ToBlock: 10,
		Addresses: []string{"0xbbb"}, RowCount: 999, SyncState: SyncIndexed, LocalDir: dirB,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Quarantine("bad/chunk1", "INVALID_RANGE_LEGACY: max>to"); err != nil {
		t.Fatal(err)
	}
	if len(registry.Active()) != 1 {
		t.Fatalf("active = %d, want 1", len(registry.Active()))
	}
	rows, files, bytes := registry.Stats()
	if rows != 5 || files != 1 || bytes != 1 {
		t.Fatalf("stats = %d/%d/%d, want 5/1/1", rows, files, bytes)
	}
	if n, _ := registry.AddressTxCount(ctx, "0xbbb"); n != 0 {
		t.Fatalf("quarantined coverage count = %d, want 0", n)
	}
	all := activeLocalParquet(registry)
	if len(all) != 1 || !strings.HasSuffix(all[0], "a.parquet") {
		t.Fatalf("activeLocalParquet = %v, want only a.parquet", all)
	}
	if e := registry.Get("bad/chunk1"); e == nil || e.Status != StatusInvalidRangeLegacy || e.QuarantineReason == "" {
		t.Fatalf("quarantine audit fields missing: %+v", e)
	}
}

func TestSyncRetryAfterLocalFailure(t *testing.T) {
	root := t.TempDir()
	engine := testDuckDB(t, filepath.Join(root, "data"))
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	registry, _ := NewRegistry(filepath.Join(root, "registry.json"))
	csv := "chain_id,block_number,block_timestamp,transaction_hash,log_index,token_address,from_address,to_address,value_raw\n" +
		"56,1,1700000000,0xtx1,0,0xtoken,0xaaa,0xbbb,100\n"
	csvPath := filepath.Join(root, "rows.csv")
	parquetPath := filepath.Join(root, "part.parquet")
	_ = os.WriteFile(csvPath, []byte(csv), 0o644)
	writeParquet(t, engine, csvPath, parquetPath)
	body, _ := os.ReadFile(parquetPath)
	sum := sha256.Sum256(body)
	completedDir := "bsc/jobs/completed/jobY/chunk1"
	manifest := map[string]any{
		"schema_version": 1, "job_id": "jobY", "chunk_id": "chunk1",
		"provider": "SQD_CLOUD_EXPORT", "chain_id": 56, "dataset": "token_transfer",
		"from_block": 1, "to_block": 1, "addresses": []string{"0xaaa", "0xbbb"},
		"row_count": 1,
		"files": []map[string]any{{
			"path": "token_transfers/part.parquet", "bytes": len(body), "sha256": hex.EncodeToString(sum[:]),
		}},
		"completed": true,
	}
	manifestPayload, _ := json.Marshal(manifest)
	_ = store.Put(context.Background(), completedDir+"/manifest.json", manifestPayload)
	_ = store.Put(context.Background(), completedDir+"/_SUCCESS", []byte(`{"completed":true}`))
	validator := NewDuckDBValidator(engine)
	syncer := NewSyncer(store, registry, filepath.Join(root, "local"), validator)
	// 第一次同步：R2 产物缺失 → LOCAL_SYNC_FAILED，可重试
	_, err := syncer.SyncAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e := registry.Get("jobY/chunk1")
	if e == nil || e.SyncState != SyncLocalFailed {
		t.Fatalf("first sync must mark LOCAL_SYNC_FAILED, got %+v", e)
	}
	// 放回 R2 产物后重试成功
	_ = store.Put(context.Background(), completedDir+"/token_transfers/part.parquet", body)
	results, err := syncer.SyncAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ChunkKey != "jobY/chunk1" || results[0].Rows != 1 {
		t.Fatalf("retry sync results = %+v", results)
	}
	e = registry.Get("jobY/chunk1")
	if e == nil || e.Status != StatusCompleted || e.SyncState != SyncIndexed || e.DuplicateCount != 0 {
		t.Fatalf("retry entry = %+v", e)
	}
}

func TestStrictRangeAndUnexpectedAddressValidation(t *testing.T) {
	root := t.TempDir()
	engine := testDuckDB(t, filepath.Join(root, "data"))
	csv := "chain_id,block_number,block_timestamp,transaction_hash,log_index,token_address,from_address,to_address,value_raw\n" +
		"56,1,1700000000,0xtx1,0,0xtoken,0xzzz1,0xzzz2,100\n" +
		"56,4,1700000300,0xtx2,0,0xtoken,0xzzz3,0xzzz4,200\n"
	csvPath := filepath.Join(root, "rows.csv")
	parquetPath := filepath.Join(root, "part.parquet")
	_ = os.WriteFile(csvPath, []byte(csv), 0o644)
	writeParquet(t, engine, csvPath, parquetPath)
	v := NewDuckDBValidator(engine)
	val, err := v.ValidateRange(context.Background(), []string{parquetPath}, 2, 1, 3,
		[]string{"0xzzz1", "0xzzz2"})
	if err != nil {
		t.Fatal(err)
	}
	if val.RangeViolations != 1 {
		t.Fatalf("range violations = %d, want 1", val.RangeViolations)
	}
	if val.UnexpectedAddresses != 1 {
		t.Fatalf("unexpected addresses = %d, want 1", val.UnexpectedAddresses)
	}
}

func TestRegistryMultiInstanceSavePreservesQuarantine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "registry.json")
	a, _ := NewRegistry(path)
	b, _ := NewRegistry(path)
	ctx := context.Background()
	if err := a.Register(&Entry{
		ChunkKey: "bad/chunk1", JobID: "bad", ChunkID: "chunk1",
		ChainKey: "bsc", Dataset: "token_transfer",
		Addresses: []string{"0xaaa"}, RowCount: 10, SyncState: SyncIndexed,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Quarantine("bad/chunk1", "test"); err != nil {
		t.Fatal(err)
	}
	// 实例 B 持有旧缓存（隔离前），注册新 chunk 时不得覆盖 bad/chunk1 的隔离状态
	if err := b.Register(&Entry{
		ChunkKey: "good/chunk1", JobID: "good", ChunkID: "chunk1",
		ChainKey: "bsc", Dataset: "token_transfer",
		Addresses: []string{"0xbbb"}, RowCount: 5, SyncState: SyncIndexed,
	}); err != nil {
		t.Fatal(err)
	}
	if e := a.Get("bad/chunk1"); e == nil || e.Status != StatusInvalidRangeLegacy {
		t.Fatalf("quarantine lost after other instance write: %+v", e)
	}
	if n, _ := a.AddressTxCount(ctx, "0xaaa"); n != 0 {
		t.Fatalf("quarantined coverage = %d, want 0", n)
	}
}
