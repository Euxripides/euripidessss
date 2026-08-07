package downloadengine

import (
	"fmt"
	"sync"
	"time"
)

// ── V2.1 RC2 Dataset Registry 增强 ──

// DatasetStatus 数据生命周期
type DatasetStatus string

const (
	DsCreated     DatasetStatus = "CREATED"
	DsDownloading DatasetStatus = "DOWNLOADING"
	DsValidating  DatasetStatus = "VALIDATING"
	DsReady       DatasetStatus = "READY"
	DsArchived    DatasetStatus = "ARCHIVED"
	DsDeleted     DatasetStatus = "DELETED"
)

// EnhancedDataset 扩展模型
type EnhancedDataset struct {
	ID            string        `json:"id"`
	Chain         string        `json:"chain"`
	Type          DatasetType   `json:"type"`
	SchemaVersion int           `json:"schema_version"`
	StartBlock    uint64        `json:"start_block"`
	EndBlock      uint64        `json:"end_block"`
	Status        DatasetStatus `json:"status"`
	RowCount      int64         `json:"row_count"`
	SizeBytes     int64         `json:"size_bytes"`
	Checksum      string        `json:"checksum"`
	FilePath      string        `json:"file_path"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// ── File Registry ──

type FileEntry struct {
	DatasetID   string `json:"dataset_id"`
	FilePath    string `json:"file_path"`
	SizeBytes   int64  `json:"size_bytes"`
	RowCount    int64  `json:"row_count"`
	Checksum    string `json:"checksum"`
	Compression string `json:"compression"`
}

// ── Coverage Index ──

type BlockCoverage struct {
	mu       sync.RWMutex
	segments []blockRange // sorted, non-overlapping
}

type blockRange struct{ start, end uint64 }

func NewBlockCoverage() *BlockCoverage {
	return &BlockCoverage{}
}

func (bc *BlockCoverage) Add(start, end uint64) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.segments = append(bc.segments, blockRange{start, end})
	bc.merge()
}

func (bc *BlockCoverage) Covers(start, end uint64) bool {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	for _, seg := range bc.segments {
		if seg.start <= start && seg.end >= end {
			return true
		}
	}
	return false
}

func (bc *BlockCoverage) Missing(start, end uint64) []blockRange {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	var gaps []blockRange
	current := start
	for _, seg := range bc.segments {
		if seg.end < current {
			continue
		}
		if seg.start > current {
			gaps = append(gaps, blockRange{current, seg.start - 1})
		}
		if seg.end >= end {
			return gaps
		}
		current = seg.end + 1
	}
	if current <= end {
		gaps = append(gaps, blockRange{current, end})
	}
	return gaps
}

func (bc *BlockCoverage) merge() {
	// simple merge of overlapping/nearby segments
	if len(bc.segments) < 2 {
		return
	}
	// sort + merge
	n := len(bc.segments)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if bc.segments[j].start < bc.segments[i].start {
				bc.segments[i], bc.segments[j] = bc.segments[j], bc.segments[i]
			}
		}
	}
	merged := bc.segments[:1]
	for _, seg := range bc.segments[1:] {
		last := &merged[len(merged)-1]
		if seg.start <= last.end+1 {
			if seg.end > last.end {
				last.end = seg.end
			}
		} else {
			merged = append(merged, seg)
		}
	}
	bc.segments = merged
}

// ── Address Coverage ──

type AddressCoverage struct {
	mu      sync.RWMutex
	entries map[string]*AddressCoverageEntry
}

type AddressCoverageEntry struct {
	Address    string    `json:"address"`
	DatasetID  string    `json:"dataset_id"`
	Chain      string    `json:"chain"`
	StartBlock uint64    `json:"start_block"`
	EndBlock   uint64    `json:"end_block"`
	Status     string    `json:"status"` // covered / partial
	UpdatedAt  time.Time `json:"updated_at"`
}

func NewAddressCoverage() *AddressCoverage {
	return &AddressCoverage{entries: make(map[string]*AddressCoverageEntry)}
}

func (ac *AddressCoverage) Mark(address string, entry *AddressCoverageEntry) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.entries[fmt.Sprintf("%s:%s", entry.Chain, address)] = entry
}

func (ac *AddressCoverage) IsCovered(chain, address string) bool {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	_, ok := ac.entries[fmt.Sprintf("%s:%s", chain, address)]
	return ok
}

// ── Enhanced Registry ──

type EnhancedRegistry struct {
	mu       sync.RWMutex
	datasets map[string]*EnhancedDataset // ID → dataset
	coverage map[string]*BlockCoverage   // chain:type → coverage
	addrCov  *AddressCoverage
	files    map[string]*FileEntry // path → file entry

	// Metrics
	CacheHits   int64
	CacheMisses int64
	ReadyTotal  int64
	RowsTotal   int64
	SizeBytes   int64
	ValidFails  int64
}

func NewEnhancedRegistry() *EnhancedRegistry {
	return &EnhancedRegistry{
		datasets: make(map[string]*EnhancedDataset),
		coverage: make(map[string]*BlockCoverage),
		addrCov:  NewAddressCoverage(),
		files:    make(map[string]*FileEntry),
	}
}

func (er *EnhancedRegistry) RegisterDataset(ds *EnhancedDataset) error {
	er.mu.Lock()
	defer er.mu.Unlock()

	if existing, ok := er.datasets[ds.ID]; ok {
		if existing.Status == DsReady && ds.Status == DsCreated {
			er.CacheHits++ // 重复任务 → 缓存命中
			return nil     // 幂等
		}
	}

	ds.CreatedAt = time.Now().UTC()
	ds.UpdatedAt = ds.CreatedAt
	er.datasets[ds.ID] = ds

	// 更新 coverage
	key := fmt.Sprintf("%s:%s", ds.Chain, ds.Type)
	if er.coverage[key] == nil {
		er.coverage[key] = NewBlockCoverage()
	}
	er.coverage[key].Add(ds.StartBlock, ds.EndBlock)

	if ds.Status == DsReady {
		er.ReadyTotal++
		er.RowsTotal += ds.RowCount
		er.SizeBytes += ds.SizeBytes
	}
	return nil
}

func (er *EnhancedRegistry) GetDataset(id string) (*EnhancedDataset, bool) {
	er.mu.RLock()
	defer er.mu.RUnlock()
	ds, ok := er.datasets[id]
	return ds, ok
}

func (er *EnhancedRegistry) HasCoverage(chain string, dsType DatasetType, start, end uint64) bool {
	er.mu.RLock()
	defer er.mu.RUnlock()
	key := fmt.Sprintf("%s:%s", chain, dsType)
	cov, ok := er.coverage[key]
	if !ok {
		er.CacheMisses++
		return false
	}
	if cov.Covers(start, end) {
		er.CacheHits++
		return true
	}
	er.CacheMisses++
	return false
}

func (er *EnhancedRegistry) MissingRanges(chain string, dsType DatasetType, start, end uint64) []blockRange {
	er.mu.RLock()
	defer er.mu.RUnlock()
	key := fmt.Sprintf("%s:%s", chain, dsType)
	cov, ok := er.coverage[key]
	if !ok {
		return []blockRange{{start, end}}
	}
	return cov.Missing(start, end)
}

func (er *EnhancedRegistry) RegisterFile(entry *FileEntry) {
	er.mu.Lock()
	defer er.mu.Unlock()
	er.files[entry.FilePath] = entry
}

func (er *EnhancedRegistry) MarkValidationFailed(id string) {
	er.mu.Lock()
	defer er.mu.Unlock()
	er.ValidFails++
	if ds, ok := er.datasets[id]; ok {
		ds.Status = DsValidating
	}
}

// ── Schema Version Migration ──

type SchemaMigration struct {
	FromVersion int
	ToVersion   int
	DatasetType DatasetType
	Description string
	UpSQL       string
}

var SchemaMigrations = []SchemaMigration{
	{FromVersion: 1, ToVersion: 2, DatasetType: DSTransactions, Description: "增加 gas_used, gas_price 字段", UpSQL: "ALTER transactions ADD COLUMN gas_used BIGINT"},
}

// ── Row一致性校验 ──

type ConsistencyCheck struct {
	SourceRows  int64 `json:"source_rows"`
	ParquetRows int64 `json:"parquet_rows"`
	DuckDBRows  int64 `json:"duckdb_rows"`
	Passed      bool  `json:"passed"`
}

func NewConsistencyCheck(source, parquet, duckdb int64) *ConsistencyCheck {
	return &ConsistencyCheck{
		SourceRows: source, ParquetRows: parquet, DuckDBRows: duckdb,
		Passed: source == parquet && parquet == duckdb,
	}
}

func (c *ConsistencyCheck) Validate() error {
	if !c.Passed {
		return fmt.Errorf("consistency failed: source=%d, parquet=%d, duckdb=%d",
			c.SourceRows, c.ParquetRows, c.DuckDBRows)
	}
	return nil
}

// ── Registry Metrics Snapshot ──

func (er *EnhancedRegistry) MetricsSnapshot() map[string]any {
	er.mu.RLock()
	defer er.mu.RUnlock()
	return map[string]any{
		"dataset_ready_total":     er.ReadyTotal,
		"dataset_rows_total":      er.RowsTotal,
		"dataset_size_bytes":      er.SizeBytes,
		"dataset_cache_hit":       er.CacheHits,
		"dataset_cache_miss":      er.CacheMisses,
		"dataset_validation_fail": er.ValidFails,
		"total_datasets":          len(er.datasets),
		"total_files":             len(er.files),
	}
}
