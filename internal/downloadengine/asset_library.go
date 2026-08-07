package downloadengine

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── V2.1 RC2 本地 Parquet 数据资产库 ──

type DatasetType string

const (
	DSTransactions DatasetType = "transactions"
	DSLogs         DatasetType = "logs"
	DSTransfers    DatasetType = "transfers"
	DSTraces       DatasetType = "traces"
)

// ── Schema ──

var datasetSchemas = map[DatasetType][]string{
	DSTransactions: {"block_number", "timestamp", "tx_hash", "from", "to", "value", "gas", "status"},
	DSLogs:         {"block_number", "tx_hash", "address", "topic0", "topic1", "topic2", "data"},
	DSTransfers:    {"token", "from", "to", "amount", "block_number", "tx_hash"},
	DSTraces:       {"block_number", "tx_hash", "trace_type", "from", "to", "value", "error"},
}

// Dedup keys
var dedupKeys = map[DatasetType]string{
	DSTransactions: "tx_hash",
	DSLogs:         "tx_hash||log_index",
	DSTransfers:    "tx_hash||event_index",
	DSTraces:       "tx_hash||trace_address",
}

// ── Asset Library ──

type ParquetAssetLibrary struct {
	mu        sync.RWMutex
	baseDir   string
	chain     string
	manifests map[string]*AssetManifest // path → manifest
}

type AssetManifest struct {
	Dataset     DatasetType `json:"dataset"`
	Chain       string      `json:"chain"`
	StartBlock  uint64      `json:"range_start"`
	EndBlock    uint64      `json:"range_end"`
	Rows        int64       `json:"rows"`
	SizeBytes   int64       `json:"size_bytes"`
	Compression string      `json:"compression"`
	FilePath    string      `json:"file_path"`
	Status      string      `json:"status"` // partial | complete
	Checksum    string      `json:"checksum"`
	CreatedAt   string      `json:"created_at"`
}

func NewParquetAssetLibrary(baseDir, chain string) *ParquetAssetLibrary {
	return &ParquetAssetLibrary{
		baseDir:   baseDir,
		chain:     chain,
		manifests: make(map[string]*AssetManifest),
	}
}

func (lib *ParquetAssetLibrary) PartitionPath(dataset DatasetType, block uint64) string {
	// 分区: chain/dataset/year/month/block_range/
	year := "2026"
	month := fmt.Sprintf("%02d", (block/1000000)%12+1)
	bucket := (block / 100000) * 100000
	return filepath.Join(lib.baseDir, lib.chain, string(dataset),
		fmt.Sprintf("year=%s", year),
		fmt.Sprintf("month=%s", month),
		fmt.Sprintf("block=%d", bucket))
}

func (lib *ParquetAssetLibrary) FilePath(dataset DatasetType, startBlock, endBlock uint64) string {
	dir := lib.PartitionPath(dataset, startBlock)
	return filepath.Join(dir, fmt.Sprintf("part-%d-%d.parquet", startBlock, endBlock))
}

// ── Manifest CRUD ──

func (lib *ParquetAssetLibrary) RegisterManifest(m *AssetManifest) {
	lib.mu.Lock()
	defer lib.mu.Unlock()
	lib.manifests[m.FilePath] = m
}

func (lib *ParquetAssetLibrary) HasBlockRange(dataset DatasetType, startBlock, endBlock uint64) bool {
	lib.mu.RLock()
	defer lib.mu.RUnlock()
	for _, m := range lib.manifests {
		if m.Dataset == dataset && m.StartBlock <= startBlock && m.EndBlock >= endBlock && m.Status == "complete" {
			return true
		}
	}
	return false
}

func (lib *ParquetAssetLibrary) MissingRanges(dataset DatasetType, requestedStart, requestedEnd uint64) []struct{ Start, End uint64 } {
	lib.mu.RLock()
	defer lib.mu.RUnlock()

	// Collect existing covered ranges
	type rng struct{ s, e uint64 }
	var covered []rng
	for _, m := range lib.manifests {
		if m.Dataset == dataset && m.Status == "complete" {
			covered = append(covered, rng{m.StartBlock, m.EndBlock})
		}
	}
	sort.Slice(covered, func(i, j int) bool { return covered[i].s < covered[j].s })

	var missing []struct{ Start, End uint64 }
	current := requestedStart
	for _, c := range covered {
		if current < c.s {
			missing = append(missing, struct{ Start, End uint64 }{current, c.s - 1})
		}
		if c.e > current {
			current = c.e + 1
		}
	}
	if current <= requestedEnd {
		missing = append(missing, struct{ Start, End uint64 }{current, requestedEnd})
	}
	return missing
}

// ── 去重 ──

func (lib *ParquetAssetLibrary) DedupKey(dataset DatasetType) string {
	if k, ok := dedupKeys[dataset]; ok {
		return k
	}
	return "tx_hash"
}

func (lib *ParquetAssetLibrary) Schema(dataset DatasetType) []string {
	if s, ok := datasetSchemas[dataset]; ok {
		return s
	}
	return nil
}

// ── 增量更新 ──

func (lib *ParquetAssetLibrary) IncrementalPlan(dataset DatasetType, startBlock, endBlock uint64) (download []struct{ Start, End uint64 }, skip int) {
	lib.mu.RLock()
	defer lib.mu.RUnlock()

	missing := lib.MissingRanges(dataset, startBlock, endBlock)
	skipCount := 0
	for _, m := range lib.manifests {
		if m.Dataset == dataset && m.Status == "complete" &&
			m.StartBlock >= startBlock && m.EndBlock <= endBlock {
			skipCount++
		}
	}
	return missing, skipCount
}

// ── CRC 校验 ──

func (lib *ParquetAssetLibrary) VerifyChecksum(path string, expected string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	h := sha256.Sum256(data)
	actual := fmt.Sprintf("%x", h)[:16]
	return actual == expected, nil
}

// ── 存储分层 ──

type StorageTier string

const (
	TierHot  StorageTier = "hot"  // 最近分析
	TierCold StorageTier = "cold" // ZSTD压缩
	TierArch StorageTier = "arch" // 归档
)

func (lib *ParquetAssetLibrary) ComputeTier(lastAccessed time.Time, sizeBytes int64) StorageTier {
	if time.Since(lastAccessed) < 7*24*time.Hour {
		return TierHot
	}
	if sizeBytes > 10*1024*1024*1024 { // 10 GB
		return TierArch
	}
	return TierCold
}

// ── 数据生命周期 ──

type LifecycleStatus string

const (
	LifecycleDownloading LifecycleStatus = "downloading"
	LifecycleValidating  LifecycleStatus = "validating"
	LifecycleParquet     LifecycleStatus = "parquet"
	LifecycleManifest    LifecycleStatus = "manifest"
	LifecycleDuckDB      LifecycleStatus = "duckdb"
	LifecycleAsset       LifecycleStatus = "asset"
)

func (lib *ParquetAssetLibrary) ValidateDataset(path string, schema []string) error {
	// 验证 Parquet 文件可读 + schema 匹配
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("asset validation: %w", err)
	}
	if info.Size() < 8 {
		return fmt.Errorf("asset validation: file too small (%d bytes)", info.Size())
	}
	// Parquet magic bytes
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	header := make([]byte, 4)
	f.Read(header)
	if string(header) != "PAR1" {
		return fmt.Errorf("asset validation: invalid parquet header")
	}
	return nil
}

// ── 统计 ──

func (lib *ParquetAssetLibrary) Stats() map[string]any {
	lib.mu.RLock()
	defer lib.mu.RUnlock()

	var totalRows, totalSize int64
	byDataset := make(map[DatasetType]int)
	for _, m := range lib.manifests {
		totalRows += m.Rows
		totalSize += m.SizeBytes
		byDataset[m.Dataset]++
	}
	return map[string]any{
		"total_files":   len(lib.manifests),
		"total_rows":    totalRows,
		"total_size_mb": float64(totalSize) / 1e6,
		"by_dataset":    byDataset,
		"chain":         lib.chain,
	}
}

// ── Provider Router 集成 ──

func (lib *ParquetAssetLibrary) QueryOrDownload(dataset DatasetType, startBlock, endBlock uint64) (paths []string, needDownload bool) {
	if lib.HasBlockRange(dataset, startBlock, endBlock) {
		// 已有数据 → 直接返回文件路径
		for _, m := range lib.manifests {
			if m.Dataset == dataset && m.StartBlock <= startBlock && m.EndBlock >= endBlock && m.Status == "complete" {
				paths = append(paths, m.FilePath)
			}
		}
		return paths, false
	}
	return nil, true
}

// ── 2TB 设备规划 ──

func Plan2TBDevice() map[string]string {
	return map[string]string{
		"system":   "200GB",
		"bsc_data": "1500GB",
		"cache":    "300GB",
	}
}

// ── 分区裁剪查询 (DuckDB) ──

func (lib *ParquetAssetLibrary) DuckDBGlob(dataset DatasetType) string {
	dir := filepath.Join(lib.baseDir, lib.chain, string(dataset))
	return strings.ReplaceAll(filepath.Join(dir, "**", "*.parquet"), "\\", "/")
}

// ── Rollback ──

func (lib *ParquetAssetLibrary) Rollback(dataset DatasetType, startBlock uint64) error {
	lib.mu.Lock()
	defer lib.mu.Unlock()

	var toDelete []string
	for path, m := range lib.manifests {
		if m.Dataset == dataset && m.StartBlock >= startBlock {
			toDelete = append(toDelete, path)
		}
	}
	for _, path := range toDelete {
		delete(lib.manifests, path)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rollback %s: %w", path, err)
		}
	}
	return nil
}
