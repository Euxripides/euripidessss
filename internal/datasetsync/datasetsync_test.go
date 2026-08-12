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
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/s3store"
)

func writeParquet(t *testing.T, engine *duckdb.Engine, csvPath, parquetPath string) {
	t.Helper()
	sql := fmt.Sprintf(
		"COPY (SELECT * REPLACE(CAST(value_raw AS VARCHAR) AS value_raw) FROM read_csv_auto('%s')) TO '%s' (FORMAT PARQUET, COMPRESSION GZIP)",
		filepath.ToSlash(csvPath), filepath.ToSlash(parquetPath),
	)
	if _, err := engine.ExecSQL(context.Background(), sql); err != nil {
		t.Fatalf("create parquet: %v", err)
	}
}

func testAddress(digit string) string { return "0x" + strings.Repeat(digit, 40) }
func testHash(digit string) string    { return "0x" + strings.Repeat(digit, 64) }

func registryFixtureFile(t *testing.T, dir, rel string, body []byte) FileInfo {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return FileInfo{Path: rel, Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
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
	root := t.TempDir()
	path := filepath.Join(root, "registry.json")
	r, err := NewRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	localDir := filepath.Join(root, "job1", "chunk1")
	file := registryFixtureFile(t, localDir, "token_transfers/part.parquet", []byte("data"))
	a, b := testAddress("a"), testAddress("b")
	e := &Entry{
		ChunkKey: "job1/chunk1", JobID: "job1", ChunkID: "chunk1",
		ChainKey: "bsc", ChainID: 56, Dataset: "token_transfer",
		FromBlock: 1, ToBlock: 10, MinBlock: 1, MaxBlock: 10,
		Addresses: []string{a, b}, RowCount: 100, UniqueKeyCount: 100,
		AddressRowCounts: map[string]int64{a: 60, b: 40},
		SyncState:        SyncIndexed, LocalDir: localDir,
		Files: []FileInfo{file},
	}
	if err := r.Register(e); err != nil {
		t.Fatal(err)
	}
	if !r.Has("job1/chunk1") {
		t.Fatal("entry not found")
	}
	n, err := r.AddressTxCount(context.Background(), a)
	if err != nil || n != 60 {
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
	a, b, c, d, token := testAddress("a"), testAddress("b"), testAddress("c"), testAddress("d"), testAddress("f")
	csv := "chain_id,block_number,block_timestamp,transaction_hash,log_index,token_address,from_address,to_address,value_raw\n" +
		fmt.Sprintf("56,1,1700000000,%s,0,%s,%s,%s,100\n", testHash("1"), token, a, b) +
		fmt.Sprintf("56,2,1700000100,%s,0,%s,%s,%s,200\n", testHash("2"), token, b, c) +
		fmt.Sprintf("56,3,1700000200,%s,0,%s,%s,%s,300\n", testHash("3"), token, a, d)
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
		"from_block": 1, "to_block": 3, "addresses": []string{a, b, c, d},
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
	n, _ := registry.AddressTxCount(context.Background(), a)
	if n != 2 {
		t.Fatalf("address-specific coverage count = %d, want 2", n)
	}
	// 第二 chunk：1 条重复 + 1 条新行，验证 merged 全量合并且去重
	eAddr := testAddress("e")
	csv2 := "chain_id,block_number,block_timestamp,transaction_hash,log_index,token_address,from_address,to_address,value_raw\n" +
		fmt.Sprintf("56,3,1700000200,%s,0,%s,%s,%s,300\n", testHash("3"), token, a, d) +
		fmt.Sprintf("56,4,1700000300,%s,0,%s,%s,%s,400\n", testHash("4"), token, a, eAddr)
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
		"from_block": 3, "to_block": 4, "addresses": []string{a, d, eAddr},
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
		"row_count": 1, "addresses": []string{testAddress("a")}, "completed": true,
		"files": []map[string]any{{
			"path": "token_transfers/a.parquet", "bytes": 11, "sha256": strings.Repeat("d", 64),
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
	if len(results) != 1 || results[0].Status != "FAILED" || results[0].Error == "" {
		t.Fatalf("bad sha failure must be explicit, got %+v", results)
	}
	e := registry.Get("jobX/chunk1")
	if e == nil || e.Status != StatusFailed || e.SyncState != SyncLocalFailed {
		t.Fatalf("bad sha chunk must be marked FAILED/LOCAL_SYNC_FAILED for retry, got %+v", e)
	}
}

func TestSyncEmptyManifestBecomesIndexedAndIsSkippedOnRetry(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	registry, err := NewRegistry(filepath.Join(root, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := "bsc/jobs/completed/empty-job/chunk-1"
	manifest := map[string]any{
		"schema_version": 1, "job_id": "empty-job", "chunk_id": "chunk-1",
		"chain_id": 56, "dataset": "token_transfer", "from_block": 10, "to_block": 20,
		"addresses": []string{testAddress("a")}, "row_count": 0, "files": []any{}, "completed": true,
	}
	payload, _ := json.Marshal(manifest)
	if err := store.Put(context.Background(), dir+"/manifest.json", payload); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), dir+"/_SUCCESS", []byte(`{"completed":true}`)); err != nil {
		t.Fatal(err)
	}
	syncer := NewSyncer(store, registry, filepath.Join(root, "local"), nil)
	first, err := syncer.SyncAll(context.Background())
	if err != nil || len(first) != 1 || first[0].Skipped || first[0].Status != "INDEXED" {
		t.Fatalf("first empty sync = %+v, err=%v", first, err)
	}
	entry := registry.Get("empty-job/chunk-1")
	if entry == nil || entry.SyncState != SyncIndexed || !entry.IsAuthoritative() {
		t.Fatalf("empty entry must be authoritative INDEXED: %+v", entry)
	}
	second, err := syncer.SyncAll(context.Background())
	if err != nil || len(second) != 1 || !second[0].Skipped || second[0].Status != "SKIPPED" {
		t.Fatalf("second empty sync = %+v, err=%v", second, err)
	}
}

func TestRecentValidationFailureUsesCooldownInsteadOfRescanning(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	registry, err := NewRegistry(filepath.Join(root, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	entry := &Entry{
		ChunkKey: "bad-job/chunk-1", JobID: "bad-job", ChunkID: "chunk-1",
		ChainKey: "bsc", ChainID: 56, Dataset: "token_transfer",
		FromBlock: 1, ToBlock: 2, Addresses: []string{testAddress("a")},
		Status: StatusFailed, SyncState: SyncValidationFailed,
	}
	if err := registry.Register(entry); err != nil {
		t.Fatal(err)
	}
	// 只需让 List 发现 manifest；若冷却未生效，后续解析必然失败。
	if err := store.Put(context.Background(), "bsc/jobs/completed/bad-job/chunk-1/manifest.json", []byte("invalid")); err != nil {
		t.Fatal(err)
	}
	results, err := NewSyncer(store, registry, filepath.Join(root, "local"), nil).SyncAll(context.Background())
	if err != nil || len(results) != 1 || !results[0].Skipped || results[0].Status != "SKIPPED" {
		t.Fatalf("cooldown result = %+v, err=%v", results, err)
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
	if len(results) != 3 {
		t.Fatalf("legacy manifest failures must be explicit, got %+v", results)
	}
	for _, result := range results {
		if result.Status != "FAILED" || result.Error == "" {
			t.Fatalf("legacy failure missing status/error: %+v", result)
		}
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
	fileA := registryFixtureFile(t, dirA, "token_transfers/a.parquet", []byte("a"))
	_ = registryFixtureFile(t, dirB, "token_transfers/b.parquet", []byte("b"))
	a, b := testAddress("a"), testAddress("b")
	ctx := context.Background()
	if err := registry.Register(&Entry{
		ChunkKey: "active/chunk1", JobID: "active", ChunkID: "chunk1",
		ChainKey: "bsc", ChainID: 56, Dataset: "token_transfer", FromBlock: 1, ToBlock: 10,
		Addresses: []string{a}, RowCount: 5, UniqueKeyCount: 5, MinBlock: 1, MaxBlock: 10,
		SyncState: SyncIndexed, LocalDir: dirA, Files: []FileInfo{fileA},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&Entry{
		ChunkKey: "bad/chunk1", JobID: "bad", ChunkID: "chunk1",
		ChainKey: "bsc", Dataset: "token_transfer", FromBlock: 1, ToBlock: 10,
		Addresses: []string{b}, RowCount: 999, SyncState: SyncIndexed, LocalDir: dirB,
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
	if n, _ := registry.AddressTxCount(ctx, b); n != 0 {
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
	a, b, token := testAddress("a"), testAddress("b"), testAddress("f")
	csv := "chain_id,block_number,block_timestamp,transaction_hash,log_index,token_address,from_address,to_address,value_raw\n" +
		fmt.Sprintf("56,1,1700000000,%s,0,%s,%s,%s,100\n", testHash("1"), token, a, b)
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
		"from_block": 1, "to_block": 1, "addresses": []string{a, b},
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
	a, b, c, d, token := testAddress("1"), testAddress("2"), testAddress("3"), testAddress("4"), testAddress("f")
	csv := "chain_id,block_number,block_timestamp,transaction_hash,log_index,token_address,from_address,to_address,value_raw\n" +
		fmt.Sprintf("56,1,1700000000,%s,0,%s,%s,%s,100\n", testHash("1"), token, a, b) +
		fmt.Sprintf("56,4,1700000300,%s,0,%s,%s,%s,200\n", testHash("2"), token, c, d)
	csvPath := filepath.Join(root, "rows.csv")
	parquetPath := filepath.Join(root, "part.parquet")
	_ = os.WriteFile(csvPath, []byte(csv), 0o644)
	writeParquet(t, engine, csvPath, parquetPath)
	v := NewDuckDBValidator(engine)
	val, err := v.ValidateRange(context.Background(), []string{parquetPath}, 2, 1, 3,
		[]string{a, b})
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

func TestRegistryCoverageIndex(t *testing.T) {
	root := t.TempDir()
	registry, _ := NewRegistry(filepath.Join(root, "registry.json"))
	ctx := context.Background()
	dirA := filepath.Join(root, "coverage-a")
	dirB := filepath.Join(root, "coverage-b")
	fileA := registryFixtureFile(t, dirA, "a.parquet", []byte("a"))
	fileB := registryFixtureFile(t, dirB, "b.parquet", []byte("b"))
	a := testAddress("a")
	if err := registry.Register(&Entry{
		ChunkKey: "a/chunk1", JobID: "a", ChunkID: "chunk1",
		ChainKey: "bsc", ChainID: 56, Dataset: "token_transfer",
		FromBlock: 1, ToBlock: 100, Addresses: []string{a}, RowCount: 50, UniqueKeyCount: 50, MinBlock: 1, MaxBlock: 100,
		SyncState: SyncIndexed, LocalDir: dirA,
		Files: []FileInfo{fileA},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&Entry{
		ChunkKey: "b/chunk1", JobID: "b", ChunkID: "chunk1",
		ChainKey: "bsc", ChainID: 56, Dataset: "token_transfer",
		FromBlock: 50, ToBlock: 200, Addresses: []string{a}, RowCount: 30, UniqueKeyCount: 30, MinBlock: 50, MaxBlock: 200,
		SyncState: SyncIndexed, LocalDir: dirB,
		Files: []FileInfo{fileB},
	}); err != nil {
		t.Fatal(err)
	}
	if n, _ := registry.AddressTxCount(ctx, a); n != 80 {
		t.Fatalf("indexed coverage rows = %d, want 80", n)
	}
	ce, ok := registry.AddressCoverage("bsc", a, "token_transfer")
	if !ok || ce.FromBlock != 1 || ce.ToBlock != 200 || ce.RowCount != 80 || len(ce.Ranges) != 1 {
		t.Fatalf("coverage index = %+v ok=%v", ce, ok)
	}
}

func TestRegistryAuthoritativeStatsReconcileVersionsAndQuality(t *testing.T) {
	root := t.TempDir()
	registry, err := NewRegistry(filepath.Join(root, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}

	register := func(chunk string, rows, unique int64, state, status string, files []FileInfo, makeFiles bool) {
		t.Helper()
		dir := filepath.Join(root, strings.ReplaceAll(chunk, "/", "_"))
		if makeFiles {
			for i, f := range files {
				path := filepath.Join(dir, filepath.FromSlash(f.Path))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				body := make([]byte, f.Bytes)
				if err := os.WriteFile(path, body, 0o644); err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(body)
				files[i].SHA256 = hex.EncodeToString(sum[:])
			}
		}
		a, b := testAddress("a"), testAddress("b")
		if err := registry.Register(&Entry{
			ChunkKey: chunk, JobID: chunk, ChunkID: "chunk-1",
			ChainKey: "bsc", ChainID: 56, Dataset: "token_transfer",
			FromBlock: 94_800_000, ToBlock: 94_810_000,
			Addresses: []string{b, a}, AddressRowCounts: map[string]int64{a: unique, b: 0},
			Status: status, SyncState: state, RowCount: rows, UniqueKeyCount: unique,
			MinBlock: 94_800_000, MaxBlock: 94_810_000, LocalDir: dir, Files: files,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 同一业务范围的旧版本：即使行数更大，也不能与新版本重复累计。
	register("old/chunk-1", 626_415, 626_415, SyncIndexed, StatusCompleted,
		[]FileInfo{{Path: "token_transfers/old.parquet", Bytes: 3}}, true)
	time.Sleep(time.Millisecond)
	// 最近的成功版本已通过唯一键校验，权威统计不得容忍条目内重复。
	register("new/chunk-1", 1_135, 1_135, SyncIndexed, StatusCompleted,
		[]FileInfo{{Path: "token_transfers/a.parquet", Bytes: 2}, {Path: "token_transfers/b.parquet", Bytes: 4}}, true)
	// 以下状态都不能进入权威统计。
	register("synced/chunk-1", 900, 900, SyncLocalSynced, StatusCompleted,
		[]FileInfo{{Path: "token_transfers/synced.parquet", Bytes: 5}}, true)
	register("failed/chunk-1", 800, 800, SyncValidationFailed, StatusFailed,
		[]FileInfo{{Path: "token_transfers/failed.parquet", Bytes: 6}}, true)
	register("missing/chunk-1", 700, 700, SyncIndexed, StatusCompleted,
		[]FileInfo{{Path: "token_transfers/missing.parquet", Bytes: 7}}, false)
	// 旧 Cloud 任务即使因为本地补同步而拥有更晚 synced_at，也不能覆盖更新的
	// Cloud 完成版本。
	newEntry := registry.Get("new/chunk-1")
	newEntry.CompletedAt = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	if err := registry.Register(newEntry); err != nil {
		t.Fatal(err)
	}
	oldEntry := registry.Get("old/chunk-1")
	oldEntry.CompletedAt = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	if err := registry.Register(oldEntry); err != nil {
		t.Fatal(err)
	}
	// 同目录中未被 Manifest 声明的历史残留文件不得进入 merged。
	rogue := filepath.Join(root, "new_chunk-1", "token_transfers", "rogue.parquet")
	if err := os.WriteFile(rogue, []byte("rogue"), 0o644); err != nil {
		t.Fatal(err)
	}

	authoritative := registry.Authoritative()
	if len(authoritative) != 1 || authoritative[0].ChunkKey != "new/chunk-1" {
		t.Fatalf("authoritative = %+v, want only new/chunk-1", authoritative)
	}
	rows, files, bytes := registry.Stats()
	if rows != 1_135 || files != 2 || bytes != 6 {
		t.Fatalf("authoritative stats = %d/%d/%d, want 1135/2/6", rows, files, bytes)
	}
	if n, _ := registry.AddressTxCount(context.Background(), testAddress("a")); n != 1_135 {
		t.Fatalf("authoritative address rows = %d, want 1135", n)
	}
	if len(registry.Completed()) != 5 {
		t.Fatalf("raw audit entries = %d, want 5", len(registry.Completed()))
	}
	if paths := activeLocalParquet(registry); len(paths) != 2 {
		t.Fatalf("authoritative parquet paths = %v, want 2 declared files", paths)
	}
}

func TestSyncRejectsDuplicatePartSHA(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	registry, _ := NewRegistry(filepath.Join(root, "registry.json"))
	completedDir := "bsc/jobs/completed/jobD/chunk1"
	// 两个远程路径放同一份 parquet（相同 SHA），下载校验通过后应触发 duplicate_part_sha
	body := []byte("same-parquet-body")
	sum := sha256.Sum256(body)
	_ = store.Put(context.Background(), completedDir+"/token_transfers/a.parquet", body)
	_ = store.Put(context.Background(), completedDir+"/token_transfers/b.parquet", body)
	manifest := map[string]any{
		"schema_version": 2, "job_id": "jobD", "chunk_id": "chunk1",
		"chain_id": 56, "dataset": "token_transfer", "row_count": 2,
		"from_block": 1, "to_block": 2, "addresses": []string{testAddress("a")},
		"parts": []map[string]any{
			{"path": "token_transfers/a.parquet", "bytes": len(body), "sha256": hex.EncodeToString(sum[:])},
			{"path": "token_transfers/b.parquet", "bytes": len(body), "sha256": hex.EncodeToString(sum[:])},
		},
		"completed": true,
	}
	manifestPayload, _ := json.Marshal(manifest)
	_ = store.Put(context.Background(), completedDir+"/manifest.json", manifestPayload)
	_ = store.Put(context.Background(), completedDir+"/_SUCCESS", []byte(`{"completed":true}`))
	syncer := NewSyncer(store, registry, filepath.Join(root, "local"), nil)
	_, _ = syncer.SyncAll(context.Background())
	e := registry.Get("jobD/chunk1")
	if e == nil || e.Status != StatusFailed || e.SyncState != SyncValidationFailed {
		t.Fatalf("duplicate part sha must fail validation, got %+v", e)
	}
}
