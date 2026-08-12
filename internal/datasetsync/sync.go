package datasetsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/logger"
	"github.com/etl/backend/internal/s3store"
)

var (
	faultOnceMu       sync.Mutex
	faultOnceConsumed bool
)

// failFirstDownloadOnce 测试/验收用故障注入（Phase 5.2 §9）：
// DATASETSYNC_FAULT_INJECTION=first_download_fail 时，进程内首次下载注入失败一次，
// 用于验证 LOCAL_SYNC_FAILED → 重试成功且不触发 Cloud 重抓。
func failFirstDownloadOnce() error {
	if os.Getenv("DATASETSYNC_FAULT_INJECTION") != "first_download_fail" {
		return nil
	}
	faultOnceMu.Lock()
	defer faultOnceMu.Unlock()
	if faultOnceConsumed {
		return nil
	}
	faultOnceConsumed = true
	return fmt.Errorf("DATASETSYNC_FAULT_INJECTION: 首次下载注入失败")
}

const (
	completedPrefix = "bsc/jobs/completed/"
	leasedPrefix    = "bsc/jobs/leased/"
)

// ObjectStore 对象存储接口（s3store 实现；local/mock 使用本地文件存储）。
type ObjectStore = s3store.ObjectStore

// ParquetValidator Parquet 校验器（duckdb.Engine 实现）。
type ParquetValidator interface {
	Validate(ctx context.Context, paths []string, expectedRows int64) (Validation, error)
}

// Validation 校验结果（Phase 4 §28）。
type Validation struct {
	Rows                int64            `json:"rows"`
	UniqueKeyCount      int64            `json:"unique_key_count"`
	DuplicateCount      int64            `json:"duplicate_count"`
	MinBlock            uint64           `json:"min_block"`
	MaxBlock            uint64           `json:"max_block"`
	SchemaOK            bool             `json:"schema_ok"`
	MissingColumns      []string         `json:"missing_columns,omitempty"`
	RangeViolations     int64            `json:"range_violation_count,omitempty"`
	UnexpectedAddresses int64            `json:"unexpected_address_count,omitempty"`
	ChainViolations     int64            `json:"chain_violation_count,omitempty"`
	RequiredNulls       int64            `json:"required_null_count,omitempty"`
	InvalidHashes       int64            `json:"invalid_hash_count,omitempty"`
	InvalidAddresses    int64            `json:"invalid_address_count,omitempty"`
	InvalidValues       int64            `json:"invalid_value_count,omitempty"`
	InvalidTimestamps   int64            `json:"invalid_timestamp_count,omitempty"`
	InvalidTypes        []string         `json:"invalid_types,omitempty"`
	AddressRowCounts    map[string]int64 `json:"address_row_counts,omitempty"`
}

// Syncer Local Sync Worker（Phase 4 §26-27）。
type Syncer struct {
	store     ObjectStore
	registry  *Registry
	localRoot string
	validator ParquetValidator
}

// NewSyncer 创建同步器。
func NewSyncer(store ObjectStore, registry *Registry, localRoot string, validator ParquetValidator) *Syncer {
	return &Syncer{store: store, registry: registry, localRoot: localRoot, validator: validator}
}

// SyncResult 单个 Chunk 的同步结果。
type SyncResult struct {
	ChunkKey      string `json:"chunk_key"`
	Rows          int64  `json:"rows"`
	Files         int    `json:"files"`
	Skipped       bool   `json:"skipped"`
	MergedParquet string `json:"merged_parquet,omitempty"`
	Status        string `json:"status,omitempty"`
	Error         string `json:"error,omitempty"`
}

var manifestEVMAddress = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// SyncAll 扫描 completed Manifest 并同步未登记 Chunk。
func (s *Syncer) SyncAll(ctx context.Context) ([]SyncResult, error) {
	objs, err := s.store.List(ctx, completedPrefix)
	if err != nil {
		return nil, err
	}
	var manifests []string
	for _, o := range objs {
		if strings.HasSuffix(o.Key, "/manifest.json") {
			manifests = append(manifests, o.Key)
		}
	}
	var out []SyncResult
	if s.registry != nil {
		s.registry.Refresh()
	}
	indexedAny := false
	for _, m := range manifests {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		res, err := s.syncManifest(ctx, m)
		if err != nil {
			logger.Log.Warn().Str("manifest", m).Err(err).Msg("datasetsync_manifest_failed")
			// 失败必须可见且可重试（Phase 5.2 §9）：Registry 已标记 LOCAL_SYNC_FAILED，
			// 这里继续处理其余 manifest，避免单个坏 chunk 阻塞整批同步。
			out = append(out, SyncResult{ChunkKey: chunkKeyFromManifestPath(m), Status: "FAILED", Error: err.Error()})
			continue
		}
		if res.Status == "" {
			if res.Skipped {
				res.Status = "SKIPPED"
			} else {
				res.Status = "INDEXED"
			}
		}
		if res.Status == "INDEXED" && !res.Skipped {
			indexedAny = true
		}
		out = append(out, res)
	}
	// 仅有新索引时才重建统一查询层。无新增时重复全量哈希/合并会让手动
	// Cloud sync 在数据量增长后超时，且不会产生任何新的查询结果。
	if indexedAny {
		s.rebuildAllMerged(ctx)
	}
	return out, nil
}

// rebuildAllMerged 按 registry 中的链重建 merged.parquet。
func (s *Syncer) rebuildAllMerged(ctx context.Context) {
	if s.validator == nil || s.registry == nil {
		return
	}
	chains := map[string]bool{}
	for _, e := range s.registry.Authoritative() {
		if e != nil && e.ChainKey != "" {
			chains[e.ChainKey] = true
		}
	}
	for chain := range chains {
		all := activeLocalParquetForChain(s.registry, chain)
		if len(all) == 0 {
			continue
		}
		outDir := filepath.Join(s.localRoot, "warehouse", "sqd_cloud", "token_transfers", "chain="+chain)
		if _, err := mergeParquet(ctx, s.validator, all, outDir); err != nil {
			logger.Log.Warn().Str("chain", chain).Err(err).Msg("datasetsync_rebuild_merged_failed")
		} else {
			logger.Log.Info().Str("chain", chain).Int("files", len(all)).Msg("datasetsync_rebuild_merged_ok")
		}
	}
}

func (s *Syncer) syncManifest(ctx context.Context, manifestKey string) (SyncResult, error) {
	// bsc/jobs/completed/{job}/{chunk}/manifest.json
	rest := strings.TrimPrefix(manifestKey, completedPrefix)
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return SyncResult{}, fmt.Errorf("非法 manifest 路径: %s", manifestKey)
	}
	jobID, chunkID := parts[0], parts[1]
	chunkKey := jobID + "/" + chunkID
	if existing := s.registry.GetCached(chunkKey); existing != nil {
		// 隔离/取消为终态：保留证据但绝不重试、不参与 merged/coverage（Phase 5.2 P0-1）
		if existing != nil &&
			(existing.Status == StatusQuarantined ||
				existing.Status == StatusInvalidRangeLegacy ||
				existing.Status == StatusCancelled) {
			return SyncResult{ChunkKey: chunkKey, Skipped: true}, nil
		}
		// 只有 INDEXED 权威条目才能跳过。LOCAL_SYNCED 是可能尚未合并成功的
		// 中间态，必须允许重试，不能制造“已同步但不可查询”的永久孤儿。
		if existing != nil && existing.IsAuthoritative() {
			return SyncResult{ChunkKey: chunkKey, Skipped: true}, nil
		}
		// Parquet 内容/Manifest 契约校验失败通常是确定性的。自动同步每分钟
		// 立即重跑会重复下载、DuckDB 扫描和哈希同一坏产物，拖慢正常任务。
		// 保留周期性重试能力，但在 15 分钟冷却窗口内直接跳过；下载/网络类
		// LOCAL_SYNC_FAILED 不受此限制，仍可在下一轮立即恢复。
		if existing.SyncState == SyncValidationFailed && time.Since(existing.SyncedAt) < 15*time.Minute {
			return SyncResult{ChunkKey: chunkKey, Skipped: true, Status: "SKIPPED"}, nil
		}
	}
	payload, err := s.store.Get(ctx, manifestKey)
	if err != nil {
		return SyncResult{}, err
	}
	// Phase 5 §27-29：legacy manifest 治理——无 schema_version 视为 legacy_invalid，
	// 只告警不登记、不自动删除。
	var rawManifest map[string]any
	if json.Unmarshal(payload, &rawManifest) == nil {
		if _, ok := rawManifest["schema_version"]; !ok {
			logger.Log.Warn().Str("manifest", manifestKey).Msg("datasetsync_legacy_manifest_no_schema_version")
			return SyncResult{}, fmt.Errorf("legacy manifest 缺少 schema_version（registry_skip，不自动删除）")
		}
	}
	// _SUCCESS 前置检查：只有看到 _SUCCESS 才开始正式同步（设计 §23）。
	successKey := strings.TrimSuffix(manifestKey, "manifest.json") + "_SUCCESS"
	if ok, _ := s.store.Exists(ctx, successKey); !ok {
		return SyncResult{}, fmt.Errorf("manifest 存在但缺少 _SUCCESS，跳过同步: %s", manifestKey)
	}
	var m struct {
		JobID         string   `json:"job_id"`
		ChunkID       string   `json:"chunk_id"`
		ChainID       int64    `json:"chain_id"`
		Dataset       string   `json:"dataset"`
		FromBlock     uint64   `json:"from_block"`
		ToBlock       uint64   `json:"to_block"`
		Addresses     []string `json:"addresses"`
		RowCount      int64    `json:"row_count"`
		SchemaVersion int      `json:"schema_version"`
		Files         []struct {
			Path   string `json:"path"`
			Bytes  int64  `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
		Parts []struct {
			Path   string `json:"path"`
			Bytes  int64  `json:"bytes"`
			Rows   int64  `json:"rows,omitempty"`
			SHA256 string `json:"sha256"`
		} `json:"parts,omitempty"`
		CompletedAt string `json:"completed_at"`
		Completed   bool   `json:"completed"`
	}
	if err := json.Unmarshal(payload, &m); err != nil {
		return SyncResult{}, fmt.Errorf("解析 manifest: %w", err)
	}
	if m.JobID != jobID || m.ChunkID != chunkID {
		return SyncResult{}, fmt.Errorf("manifest 路径与内容不一致: %s", manifestKey)
	}
	if err := validateManifestContract(m.SchemaVersion, m.Completed, m.ChainID, m.Dataset,
		m.FromBlock, m.ToBlock, m.RowCount, m.Addresses, len(m.Parts), len(m.Files)); err != nil {
		return SyncResult{}, err
	}
	localDir := filepath.Join(s.localRoot, "jobs", jobID, chunkID)
	entry := &Entry{
		ChunkKey: chunkKey, JobID: jobID, ChunkID: chunkID,
		ChainKey: chainKeyForID(m.ChainID), ChainID: m.ChainID,
		Dataset: m.Dataset, FromBlock: m.FromBlock, ToBlock: m.ToBlock,
		Addresses: m.Addresses, Provider: "SQD_CLOUD_EXPORT", SyncState: SyncLocalSynced,
		ManifestPath: manifestKey, LocalDir: localDir,
	}
	if completedAt, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(m.CompletedAt)); parseErr == nil {
		entry.CompletedAt = completedAt
	}
	type manifestFile struct {
		Path   string
		Bytes  int64
		Rows   int64
		SHA256 string
	}
	var manifestFiles []manifestFile
	if len(m.Parts) > 0 {
		for _, p := range m.Parts {
			manifestFiles = append(manifestFiles, manifestFile{Path: p.Path, Bytes: p.Bytes, Rows: p.Rows, SHA256: p.SHA256})
		}
	} else {
		for _, f := range m.Files {
			manifestFiles = append(manifestFiles, manifestFile{Path: f.Path, Bytes: f.Bytes, SHA256: f.SHA256})
		}
	}
	var localPaths []string
	seenManifestPaths := map[string]bool{}
	for _, f := range manifestFiles {
		cleanPath, pathErr := safeManifestPartPath(f.Path)
		if pathErr != nil {
			logger.Log.Warn().Str("manifest", manifestKey).Str("path", f.Path).
				Msg("datasetsync_legacy_manifest_path_without_token_transfers")
			return SyncResult{}, pathErr
		}
		f.Path = cleanPath
		pathKey := strings.ToLower(cleanPath)
		if seenManifestPaths[pathKey] {
			return SyncResult{}, fmt.Errorf("manifest Part 路径重复: %s", cleanPath)
		}
		seenManifestPaths[pathKey] = true
		if f.Bytes <= 0 || !validSHA256(f.SHA256) {
			return SyncResult{}, fmt.Errorf("manifest 文件声明无效: path=%s bytes=%d sha256=%q", f.Path, f.Bytes, f.SHA256)
		}
		// 产物上传到 leased/{job}/{chunk}/（设计 §13/§34）；manifest 在 completed/ 只保存元数据。
		remoteKey := leasedPrefix + jobID + "/" + chunkID + "/" + f.Path
		localPath, err := s.downloadVerified(ctx, remoteKey, localDir, f.Path, f.SHA256, f.Bytes)
		if err != nil {
			// 回退：部分产物可能位于 completed 目录
			fallbackKey := manifestKey[:strings.LastIndex(manifestKey, "/")] + "/" + f.Path
			localPath, err = s.downloadVerified(ctx, fallbackKey, localDir, f.Path, f.SHA256, f.Bytes)
			if err != nil {
				entry.Status = StatusFailed
				entry.SyncState = SyncLocalFailed
				_ = s.registry.Register(entry)
				return SyncResult{}, fmt.Errorf("下载 %s: %w", remoteKey, err)
			}
		}
		localPaths = append(localPaths, localPath)
		entry.Files = append(entry.Files, FileInfo{
			Path: filepath.ToSlash(filepath.Join(f.Path)), Bytes: f.Bytes, Rows: f.Rows, SHA256: f.SHA256,
		})
	}
	if s.validator != nil {
		expectedRows := m.RowCount
		if m.SchemaVersion >= 2 {
			// Manifest V2：row_count 由 Validator 按 sum(parts.rows) 权威校正（Phase 5.4.1）
			expectedRows = -1
		}
		validation, err := s.validateChunk(ctx, localPaths, expectedRows, m.FromBlock, m.ToBlock, m.ChainID, m.Addresses)
		if err != nil {
			entry.Status = StatusFailed
			entry.SyncState = SyncValidationFailed
			entry.RowCount = validation.Rows
			entry.UniqueKeyCount = validation.UniqueKeyCount
			entry.DuplicateCount = validation.DuplicateCount
			entry.MinBlock = validation.MinBlock
			entry.MaxBlock = validation.MaxBlock
			entry.AddressRowCounts = validation.AddressRowCounts
			_ = s.registry.Register(entry)
			return SyncResult{}, err
		}
		if validation.RangeViolations > 0 {
			entry.Status = StatusFailed
			entry.SyncState = SyncValidationFailed
			entry.RowCount = validation.Rows
			entry.UniqueKeyCount = validation.UniqueKeyCount
			entry.DuplicateCount = validation.DuplicateCount
			entry.MinBlock = validation.MinBlock
			entry.MaxBlock = validation.MaxBlock
			_ = s.registry.Register(entry)
			return SyncResult{}, fmt.Errorf(
				"LOCAL_VALIDATION_FAILED：越界 %d 行（manifest %d-%d，parquet %d-%d）",
				validation.RangeViolations, m.FromBlock, m.ToBlock, validation.MinBlock, validation.MaxBlock)
		}
		if validation.DuplicateCount > 0 || validation.UnexpectedAddresses > 0 || validation.ChainViolations > 0 ||
			validation.RequiredNulls > 0 || validation.InvalidHashes > 0 || validation.InvalidAddresses > 0 ||
			validation.InvalidValues > 0 || validation.InvalidTimestamps > 0 {
			entry.Status = StatusFailed
			entry.SyncState = SyncValidationFailed
			entry.RowCount = validation.Rows
			entry.UniqueKeyCount = validation.UniqueKeyCount
			entry.DuplicateCount = validation.DuplicateCount
			entry.MinBlock = validation.MinBlock
			entry.MaxBlock = validation.MaxBlock
			entry.AddressRowCounts = validation.AddressRowCounts
			_ = s.registry.Register(entry)
			return SyncResult{}, fmt.Errorf(
				"LOCAL_VALIDATION_FAILED：duplicates=%d unexpected_addresses=%d chain=%d required_nulls=%d invalid_hashes=%d invalid_addresses=%d invalid_values=%d invalid_timestamps=%d",
				validation.DuplicateCount, validation.UnexpectedAddresses, validation.ChainViolations,
				validation.RequiredNulls, validation.InvalidHashes, validation.InvalidAddresses,
				validation.InvalidValues, validation.InvalidTimestamps)
		}
		entry.RowCount = validation.Rows
		entry.UniqueKeyCount = validation.UniqueKeyCount
		entry.DuplicateCount = validation.DuplicateCount
		entry.MinBlock = validation.MinBlock
		entry.MaxBlock = validation.MaxBlock
		entry.AddressRowCounts = validation.AddressRowCounts
		// Manifest V2：sum(parts.rows) == row_count
		if m.SchemaVersion >= 2 && len(localPaths) > 0 {
			if pr, ok := s.validator.(interface {
				PartRows(ctx context.Context, paths []string) ([]int64, error)
			}); ok {
				partRows, perr := pr.PartRows(ctx, localPaths)
				if perr != nil {
					entry.Status = StatusFailed
					entry.SyncState = SyncValidationFailed
					_ = s.registry.Register(entry)
					return SyncResult{}, fmt.Errorf("part rows 统计失败: %w", perr)
				}
				var sum, declaredSum int64
				for i, n := range partRows {
					if i < len(entry.Files) {
						declared := entry.Files[i].Rows
						if declared != n {
							entry.Status = StatusFailed
							entry.SyncState = SyncValidationFailed
							_ = s.registry.Register(entry)
							return SyncResult{}, fmt.Errorf("LOCAL_VALIDATION_FAILED：part %s rows 声明=%d actual=%d",
								entry.Files[i].Path, declared, n)
						}
						entry.Files[i].Rows = n
						declaredSum += declared
					}
					sum += n
				}
				if sum != validation.Rows {
					entry.Status = StatusFailed
					entry.SyncState = SyncValidationFailed
					_ = s.registry.Register(entry)
					return SyncResult{}, fmt.Errorf(
						"LOCAL_VALIDATION_FAILED：sum(parts.rows)=%d != row_count=%d", sum, validation.Rows)
				}
				if declaredSum != m.RowCount || m.RowCount != validation.Rows {
					entry.Status = StatusFailed
					entry.SyncState = SyncValidationFailed
					_ = s.registry.Register(entry)
					return SyncResult{}, fmt.Errorf(
						"LOCAL_VALIDATION_FAILED：sum(parts.rows)=%d manifest=%d actual=%d",
						declaredSum, m.RowCount, validation.Rows)
				}
			}
		}
	} else {
		entry.RowCount = m.RowCount
		entry.UniqueKeyCount = m.RowCount
	}
	// Phase 5.4.1：duplicate part SHA > 0 → LOCAL_VALIDATION_FAILED（禁止覆盖/重复 committed part）
	shaCount := map[string]int{}
	for _, f := range entry.Files {
		shaCount[f.SHA256]++
	}
	for sha, n := range shaCount {
		if n > 1 {
			entry.Status = StatusFailed
			entry.SyncState = SyncValidationFailed
			_ = s.registry.Register(entry)
			return SyncResult{}, fmt.Errorf(
				"LOCAL_VALIDATION_FAILED：duplicate_part_sha_count=%d（sha=%s）", n, sha[:min(len(sha), 12)])
		}
	}
	entry.Status = StatusCompleted
	if err := s.registry.Register(entry); err != nil {
		return SyncResult{}, err
	}
	res := SyncResult{ChunkKey: chunkKey, Rows: entry.RowCount, Files: len(entry.Files)}
	if len(localPaths) == 0 {
		// 0 行是合法且有业务意义的认证结果；即使没有 Parquet 文件，也必须
		// 进入 INDEXED，避免后续每次同步都把它当成未完成中间态反复处理。
		entry.SyncState = SyncIndexed
		if err := s.registry.Register(entry); err != nil {
			return SyncResult{}, err
		}
	}
	if len(localPaths) > 0 {
		// 统一查询层必须包含全部已同步 chunk（含历史），否则新 chunk 会覆盖旧数据。
		allPaths := append(activeLocalParquetForChain(s.registry, entry.ChainKey), localPaths...)
		allPaths = uniquePaths(allPaths)
		merged, err := mergeParquet(ctx, s.validator, allPaths, filepath.Join(s.localRoot, "warehouse", "sqd_cloud", "token_transfers", "chain="+entry.ChainKey))
		if err != nil {
			logger.Log.Warn().Str("chunk", chunkKey).Err(err).Msg("datasetsync_merge_skipped")
			entry.Status = StatusFailed
			entry.SyncState = SyncLocalFailed
			_ = s.registry.Register(entry)
			return SyncResult{}, fmt.Errorf("LOCAL_MERGE_FAILED: %w", err)
		} else {
			entry.MergedParquet = merged
			entry.SyncState = "INDEXED"
			res.MergedParquet = merged
			_ = s.registry.Register(entry)
		}
	}
	return res, nil
}

// correctManifestRows 把 completed manifest 的 row_count 校正为 Validator 实测值（V2 幂等对账）。
func (s *Syncer) correctManifestRows(ctx context.Context, manifestKey string, rows int64) error {
	payload, err := s.store.Get(ctx, manifestKey)
	if err != nil {
		return err
	}
	var m map[string]any
	if json.Unmarshal(payload, &m) != nil {
		return fmt.Errorf("manifest 解析失败: %s", manifestKey)
	}
	m["row_count"] = rows
	fixed, _ := json.MarshalIndent(m, "", "  ")
	keys := []string{manifestKey}
	if idx := strings.LastIndex(manifestKey, "/"); idx > 0 {
		leased := leasedPrefix + manifestKey[len(completedPrefix):idx]
		keys = append(keys, leased+"/manifest.json")
	}
	for _, k := range keys {
		if err := s.store.Put(ctx, k, fixed); err != nil {
			logger.Log.Warn().Err(err).Str("key", k).Msg("datasetsync_manifest_correct_put_failed")
		}
	}
	return nil
}

// validateChunk 调用带范围约束的校验器（无该能力时回退基础校验）。
func (s *Syncer) validateChunk(ctx context.Context, paths []string, expectedRows int64,
	fromBlock, toBlock uint64, chainID int64, addresses []string) (Validation, error) {
	if vr, ok := s.validator.(interface {
		ValidateRangeForChain(ctx context.Context, paths []string, expectedRows int64, fromBlock, toBlock uint64, chainID int64, addresses []string) (Validation, error)
	}); ok {
		return vr.ValidateRangeForChain(ctx, paths, expectedRows, fromBlock, toBlock, chainID, addresses)
	}
	if vr, ok := s.validator.(interface {
		ValidateRange(ctx context.Context, paths []string, expectedRows int64, fromBlock, toBlock uint64, addresses []string) (Validation, error)
	}); ok {
		return vr.ValidateRange(ctx, paths, expectedRows, fromBlock, toBlock, addresses)
	}
	return s.validator.Validate(ctx, paths, expectedRows)
}

// activeLocalParquet 只收集 Registry 权威版本在 Manifest 中明确声明的文件。
// 不能 WalkDir 整个 job 目录，否则失败重试遗留或旧版本文件会再次混入 merged。
func activeLocalParquet(registry *Registry) []string {
	return activeLocalParquetForChain(registry, "")
}

func activeLocalParquetForChain(registry *Registry, chainKey string) []string {
	if registry == nil {
		return nil
	}
	var out []string
	for _, e := range registry.Authoritative() {
		if e == nil || e.LocalDir == "" ||
			(chainKey != "" && !strings.EqualFold(strings.TrimSpace(e.ChainKey), strings.TrimSpace(chainKey))) {
			continue
		}
		for _, f := range e.Files {
			path := f.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(e.LocalDir, filepath.FromSlash(path))
			}
			if strings.EqualFold(filepath.Ext(path), ".parquet") {
				out = append(out, path)
			}
		}
	}
	sort.Strings(out)
	return uniquePaths(out)
}

func uniquePaths(paths []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		key := strings.ToLower(path)
		if path == "." || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func chunkKeyFromManifestPath(manifestKey string) string {
	rest := strings.TrimPrefix(manifestKey, completedPrefix)
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return manifestKey
	}
	return parts[0] + "/" + parts[1]
}

func validateManifestContract(schemaVersion int, completed bool, chainID int64, dataset string,
	fromBlock, toBlock uint64, rowCount int64, addresses []string, partCount, fileCount int) error {
	if schemaVersion < 1 {
		return fmt.Errorf("manifest schema_version 无效: %d", schemaVersion)
	}
	if !completed {
		return fmt.Errorf("manifest completed=false")
	}
	if chainID <= 0 || chainKeyForID(chainID) == fmt.Sprintf("chain-%d", chainID) {
		return fmt.Errorf("manifest chain_id 不支持: %d", chainID)
	}
	dataset = strings.ToLower(strings.TrimSpace(dataset))
	if dataset != "token_transfer" && dataset != "token_transfers" {
		return fmt.Errorf("manifest dataset 不支持: %s", dataset)
	}
	if toBlock < fromBlock {
		return fmt.Errorf("manifest 区块范围非法: %d-%d", fromBlock, toBlock)
	}
	if rowCount < 0 {
		return fmt.Errorf("manifest row_count 不能为负数: %d", rowCount)
	}
	if len(addresses) == 0 {
		return fmt.Errorf("manifest addresses 为空")
	}
	seen := map[string]bool{}
	for _, address := range addresses {
		address = strings.ToLower(strings.TrimSpace(address))
		if !manifestEVMAddress.MatchString(address) {
			return fmt.Errorf("manifest address 非法: %s", address)
		}
		if seen[address] {
			return fmt.Errorf("manifest address 重复: %s", address)
		}
		seen[address] = true
	}
	if rowCount > 0 && partCount+fileCount == 0 {
		return fmt.Errorf("manifest 声明 %d 行但没有 Part 文件", rowCount)
	}
	if schemaVersion >= 2 && partCount == 0 && rowCount > 0 {
		return fmt.Errorf("manifest v2 声明 %d 行但 parts 为空", rowCount)
	}
	return nil
}

func safeManifestPartPath(path string) (string, error) {
	raw := filepath.ToSlash(strings.TrimSpace(path))
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
	if raw == "" || filepath.IsAbs(filepath.FromSlash(raw)) || clean != raw ||
		strings.Contains(raw, ":") || strings.HasPrefix(clean, "../") ||
		!strings.HasPrefix(clean, "token_transfers/") ||
		!strings.EqualFold(filepath.Ext(clean), ".parquet") {
		return "", fmt.Errorf("manifest Part 路径非法: %s", path)
	}
	return clean, nil
}

// downloadVerified 下载 + .partial + SHA256 + 原子重命名（Phase 4 §27/§28）。
func (s *Syncer) downloadVerified(ctx context.Context, remoteKey, localDir, relPath, wantSHA string, wantBytes int64) (string, error) {
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(localDir, filepath.FromSlash(relPath))
	partial := target + ".partial"
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := failFirstDownloadOnce(); err != nil {
		return "", err
	}
	body, err := s.store.Get(ctx, remoteKey)
	if err != nil {
		return "", err
	}
	if wantBytes <= 0 || int64(len(body)) != wantBytes {
		return "", fmt.Errorf("文件大小不匹配：got %d want %d", len(body), wantBytes)
	}
	if err := os.WriteFile(partial, body, 0o644); err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if wantSHA != "" && got != strings.ToLower(wantSHA) {
		_ = os.Remove(partial)
		return "", fmt.Errorf("SHA256 不匹配：got %s want %s", got, wantSHA)
	}
	if err := os.Rename(partial, target); err != nil {
		_ = os.Remove(partial)
		return "", err
	}
	return target, nil
}

func chainKeyForID(id int64) string {
	switch id {
	case 1:
		return "eth"
	case 56:
		return "bsc"
	case 8453:
		return "base"
	case 42161:
		return "arbitrum"
	default:
		return fmt.Sprintf("chain-%d", id)
	}
}

// mergeParquet 用 DuckDB 合并全部 parquet 为统一查询层（Phase 4 §44/§46）。
func mergeParquet(ctx context.Context, validator ParquetValidator, paths []string, outDir string) (string, error) {
	if validator == nil || len(paths) == 0 {
		return "", fmt.Errorf("validator 未装配或无文件")
	}
	merger, ok := validator.(interface {
		Merge(ctx context.Context, paths []string, outDir string) (string, error)
	})
	if !ok {
		return "", fmt.Errorf("validator 不支持合并")
	}
	return merger.Merge(ctx, paths, outDir)
}
