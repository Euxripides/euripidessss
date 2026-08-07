package datasetsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/etl/backend/internal/logger"
	"github.com/etl/backend/internal/s3store"
)

var (
	faultOnceMu      sync.Mutex
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
	Rows               int64    `json:"rows"`
	UniqueKeyCount     int64    `json:"unique_key_count"`
	DuplicateCount     int64    `json:"duplicate_count"`
	MinBlock           uint64   `json:"min_block"`
	MaxBlock           uint64   `json:"max_block"`
	SchemaOK           bool     `json:"schema_ok"`
	MissingColumns     []string `json:"missing_columns,omitempty"`
	RangeViolations    int64    `json:"range_violation_count,omitempty"`
	UnexpectedAddresses int64   `json:"unexpected_address_count,omitempty"`
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
}

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
	for _, m := range manifests {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		res, err := s.syncManifest(ctx, m)
		if err != nil {
			logger.Log.Warn().Str("manifest", m).Err(err).Msg("datasetsync_manifest_failed")
			// 失败必须可见且可重试（Phase 5.2 §9）：Registry 已标记 LOCAL_SYNC_FAILED，
			// 这里继续处理其余 manifest，避免单个坏 chunk 阻塞整批同步。
			continue
		}
		out = append(out, res)
	}
	// 无论是否有新 chunk，都重建统一查询层：修复历史 merged.parquet
	// 被单个 chunk 覆盖/缺失的问题（全量合并 + 去重 + 原子替换）。
	s.rebuildAllMerged(ctx)
	return out, nil
}

// rebuildAllMerged 按 registry 中的链重建 merged.parquet。
func (s *Syncer) rebuildAllMerged(ctx context.Context) {
	if s.validator == nil || s.registry == nil {
		return
	}
	chains := map[string]bool{}
	for _, e := range s.registry.Active() {
		if e != nil && e.ChainKey != "" {
			chains[e.ChainKey] = true
		}
	}
	for chain := range chains {
		all := activeLocalParquet(s.registry)
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
	if s.registry.Has(chunkKey) {
		existing := s.registry.Get(chunkKey)
		// 隔离/取消为终态：保留证据但绝不重试、不参与 merged/coverage（Phase 5.2 P0-1）
		if existing != nil &&
			(existing.Status == StatusQuarantined ||
				existing.Status == StatusInvalidRangeLegacy ||
				existing.Status == StatusCancelled) {
			return SyncResult{ChunkKey: chunkKey, Skipped: true}, nil
		}
		// 有效且已同步/已索引 → 跳过；FAILED/LOCAL_SYNC_FAILED/隔离 → 允许重试
		if existing != nil && existing.IsActive() &&
			(existing.SyncState == SyncLocalSynced || existing.SyncState == SyncIndexed) {
			return SyncResult{ChunkKey: chunkKey, Skipped: true}, nil
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
	}
	if err := json.Unmarshal(payload, &m); err != nil {
		return SyncResult{}, fmt.Errorf("解析 manifest: %w", err)
	}
	if m.JobID != jobID || m.ChunkID != chunkID {
		return SyncResult{}, fmt.Errorf("manifest 路径与内容不一致: %s", manifestKey)
	}
	localDir := filepath.Join(s.localRoot, "jobs", jobID, chunkID)
	entry := &Entry{
		ChunkKey: chunkKey, JobID: jobID, ChunkID: chunkID,
		ChainKey: chainKeyForID(m.ChainID), ChainID: m.ChainID,
		Dataset: m.Dataset, FromBlock: m.FromBlock, ToBlock: m.ToBlock,
		Addresses: m.Addresses, Provider: "SQD_CLOUD_EXPORT", SyncState: SyncLocalSynced,
		ManifestPath: manifestKey, LocalDir: localDir,
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
	for _, f := range manifestFiles {
		if !strings.HasPrefix(f.Path, "token_transfers/") {
			logger.Log.Warn().Str("manifest", manifestKey).Str("path", f.Path).
				Msg("datasetsync_legacy_manifest_path_without_token_transfers")
			return SyncResult{}, fmt.Errorf("legacy manifest 文件路径缺少 token_transfers/ 前缀（registry_skip，不自动删除）")
		}
		// 产物上传到 leased/{job}/{chunk}/（设计 §13/§34）；manifest 在 completed/ 只保存元数据。
		remoteKey := leasedPrefix + jobID + "/" + chunkID + "/" + f.Path
		localPath, err := s.downloadVerified(ctx, remoteKey, localDir, f.Path, f.SHA256)
		if err != nil {
			// 回退：部分产物可能位于 completed 目录
			fallbackKey := manifestKey[:strings.LastIndex(manifestKey, "/")] + "/" + f.Path
			localPath, err = s.downloadVerified(ctx, fallbackKey, localDir, f.Path, f.SHA256)
			if err != nil {
				entry.Status = StatusFailed
				entry.SyncState = SyncLocalFailed
				_ = s.registry.Register(entry)
				return SyncResult{}, fmt.Errorf("下载 %s: %w", remoteKey, err)
			}
		}
		localPaths = append(localPaths, localPath)
		entry.Files = append(entry.Files, FileInfo{
			Path: filepath.ToSlash(filepath.Join(f.Path)), Bytes: f.Bytes, SHA256: f.SHA256,
		})
	}
	if s.validator != nil {
		validation, err := s.validateChunk(ctx, localPaths, m.RowCount, m.FromBlock, m.ToBlock, m.Addresses)
		if err != nil {
			entry.Status = StatusFailed
			entry.SyncState = SyncValidationFailed
			entry.RowCount = validation.Rows
			entry.UniqueKeyCount = validation.UniqueKeyCount
			entry.DuplicateCount = validation.DuplicateCount
			entry.MinBlock = validation.MinBlock
			entry.MaxBlock = validation.MaxBlock
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
		entry.RowCount = validation.Rows
		entry.UniqueKeyCount = validation.UniqueKeyCount
		entry.DuplicateCount = validation.DuplicateCount
		entry.MinBlock = validation.MinBlock
		entry.MaxBlock = validation.MaxBlock
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
				var sum int64
				for i, n := range partRows {
					if i < len(entry.Files) {
						entry.Files[i].Rows = n
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
			}
		}
	} else {
		entry.RowCount = m.RowCount
		entry.UniqueKeyCount = m.RowCount
	}
	entry.Status = StatusCompleted
	if err := s.registry.Register(entry); err != nil {
		return SyncResult{}, err
	}
	res := SyncResult{ChunkKey: chunkKey, Rows: entry.RowCount, Files: len(entry.Files)}
	if len(localPaths) > 0 {
		// 统一查询层必须包含全部已同步 chunk（含历史），否则新 chunk 会覆盖旧数据。
		allPaths := activeLocalParquet(s.registry)
		if len(allPaths) == 0 {
			allPaths = localPaths
		}
		merged, err := mergeParquet(ctx, s.validator, allPaths, filepath.Join(s.localRoot, "warehouse", "sqd_cloud", "token_transfers", "chain="+entry.ChainKey))
		if err != nil {
			logger.Log.Warn().Str("chunk", chunkKey).Err(err).Msg("datasetsync_merge_skipped")
		} else {
			entry.MergedParquet = merged
			entry.SyncState = "INDEXED"
			res.MergedParquet = merged
			_ = s.registry.Register(entry)
		}
	}
	return res, nil
}

// validateChunk 调用带范围约束的校验器（无该能力时回退基础校验）。
func (s *Syncer) validateChunk(ctx context.Context, paths []string, expectedRows int64,
	fromBlock, toBlock uint64, addresses []string) (Validation, error) {
	if vr, ok := s.validator.(interface {
		ValidateRange(ctx context.Context, paths []string, expectedRows int64, fromBlock, toBlock uint64, addresses []string) (Validation, error)
	}); ok {
		return vr.ValidateRange(ctx, paths, expectedRows, fromBlock, toBlock, addresses)
	}
	return s.validator.Validate(ctx, paths, expectedRows)
}

// activeLocalParquet 收集 registry 中 ACTIVE 条目的本地 parquet（隔离/失败/取消不参与 merged）。
func activeLocalParquet(registry *Registry) []string {
	if registry == nil {
		return nil
	}
	var out []string
	for _, e := range registry.Active() {
		if e == nil || e.LocalDir == "" {
			continue
		}
		_ = filepath.WalkDir(e.LocalDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".parquet") {
				out = append(out, path)
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// downloadVerified 下载 + .partial + SHA256 + 原子重命名（Phase 4 §27/§28）。
func (s *Syncer) downloadVerified(ctx context.Context, remoteKey, localDir, relPath, wantSHA string) (string, error) {
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
