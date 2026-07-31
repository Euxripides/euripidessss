package downloadengine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ── Manifest V2 Finalizer（原子 rename）──

type ManifestV2 struct {
	Version     int       `json:"version"`
	JobID       string    `json:"job_id"`
	ChainID     string    `json:"chain_id"`
	DatasetTypes []string `json:"dataset_types"`
	Range       struct {
		StartBlock uint64 `json:"start_block"`
		EndBlock   uint64 `json:"end_block"`
	} `json:"range"`
	Status         string    `json:"status"`
	RowsTotal      int64     `json:"rows_total"`
	BytesTotal     int64     `json:"bytes_total"`
	CoverageStatus string    `json:"coverage_status"`
	CreatedAt      string    `json:"created_at"`
	CompletedAt    string    `json:"completed_at"`
}

type ManifestFinalizer struct {
	mu       sync.Mutex
	storeDir string
}

func NewManifestFinalizer(storeDir string) *ManifestFinalizer {
	return &ManifestFinalizer{storeDir: storeDir}
}

// Finalize 原子写入 Manifest: 先写临时文件，再 rename。
func (f *ManifestFinalizer) Finalize(jobID string, manifest *ManifestV2) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	manifestPath := filepath.Join(f.storeDir, jobID+"-manifest.json")
	tmpPath := manifestPath + ".tmp"

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: %w", ErrManifestInconsistent, err)
	}
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("%s: 写入临时文件失败: %w", ErrManifestInconsistent, err)
	}
	if err := os.Rename(tmpPath, manifestPath); err != nil {
		return fmt.Errorf("%s: rename 失败: %w", ErrManifestInconsistent, err)
	}
	return nil
}

// ── Completion Gate ──

type CompletionGate struct {
	requiredChecks []string
}

func NewCompletionGate() *CompletionGate {
	return &CompletionGate{
		requiredChecks: []string{"parquet", "manifest", "metadata", "duckdb_index", "validation"},
	}
}

func (g *CompletionGate) Verify(job *Job, chunks []*Chunk, manifestWritten bool, indexed bool, validated bool) error {
	var failures []string

	// 1. 所有 Chunk 必须成功
	for _, ch := range chunks {
		if ch.Status != ChunkSucceeded && ch.Status != ChunkSkipped {
			failures = append(failures, fmt.Sprintf("chunk %s status=%s", ch.ID, ch.Status))
		}
	}

	// 2. Manifest 已写入
	if !manifestWritten {
		failures = append(failures, "manifest 未写入")
	}

	// 3. DuckDB 已索引
	if !indexed {
		failures = append(failures, "DuckDB 索引未完成")
	}

	// 4. 验证通过
	if !validated {
		failures = append(failures, "数据验证未通过")
	}

	if len(failures) > 0 {
		return fmt.Errorf("%s: %v", ErrValidationFailed, failures)
	}
	return nil
}

// ── Dataset Registry ──

type DatasetRecord struct {
	DatasetID   string    `json:"dataset_id"`   // UUID
	Fingerprint string    `json:"fingerprint"`  // SHA256(json.Marshal({chain,type,start,end}))
	JobID       string    `json:"job_id"`
	ChainID     string    `json:"chain_id"`
	DatasetType string    `json:"dataset_type"`
	StartBlock  uint64    `json:"start_block"`
	EndBlock    uint64    `json:"end_block"`
	RowCount    int64     `json:"row_count"`
	ByteCount   int64     `json:"byte_count"`
	FilePath    string    `json:"file_path"`
	Checksum    string    `json:"checksum"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type DatasetRegistry struct {
	mu        sync.RWMutex
	byID      map[string]*DatasetRecord // UUID → record
	byFP      map[string]*DatasetRecord // fingerprint → record
	storeDir  string
}

func NewDatasetRegistry(storeDir string) *DatasetRegistry {
	r := &DatasetRegistry{
		byID:     make(map[string]*DatasetRecord),
		byFP:     make(map[string]*DatasetRecord),
		storeDir: storeDir,
	}
	r.load()
	return r
}

func (r *DatasetRegistry) Register(rec *DatasetRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if rec.DatasetID == "" {
		return fmt.Errorf("DatasetRecord.DatasetID 不能为空")
	}
	// 自动计算 fingerprint
	if rec.Fingerprint == "" {
		rec.Fingerprint = r.computeFingerprint(rec.ChainID, rec.DatasetType, rec.StartBlock, rec.EndBlock)
	}

	// 同 fingerprint 幂等
	if existing, ok := r.byFP[rec.Fingerprint]; ok {
		if existing.Checksum == rec.Checksum {
			return nil // 幂等
		}
		// checksum 不同则更新
		existing.Checksum = rec.Checksum
		existing.FilePath = rec.FilePath
		existing.UpdatedAt = time.Now().UTC()
		return r.persist()
	}

	// 同 UUID 幂等
	if existing, ok := r.byID[rec.DatasetID]; ok {
		oldFP := existing.Fingerprint
		existing.Fingerprint = rec.Fingerprint
		existing.Checksum = rec.Checksum
		existing.UpdatedAt = time.Now().UTC()
		if oldFP != rec.Fingerprint {
			delete(r.byFP, oldFP)
		}
		r.byFP[rec.Fingerprint] = existing
		return r.persist()
	}

	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	r.byID[rec.DatasetID] = rec
	r.byFP[rec.Fingerprint] = rec
	return r.persist()
}

func (r *DatasetRegistry) Query(chainID, datasetType string, startBlock, endBlock uint64) []*DatasetRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*DatasetRecord
	for _, rec := range r.byID {
		if rec.ChainID == chainID &&
			rec.DatasetType == datasetType &&
			rec.StartBlock >= startBlock &&
			rec.StartBlock <= endBlock {
			result = append(result, rec)
		}
	}
	return result
}

func (r *DatasetRegistry) GetByID(datasetID string) (*DatasetRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.byID[datasetID]
	return rec, ok
}

func (r *DatasetRegistry) GetByFingerprint(fp string) (*DatasetRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.byFP[fp]
	return rec, ok
}

func (r *DatasetRegistry) computeFingerprint(chainID, datasetType string, startBlock, endBlock uint64) string {
	payload := fmt.Sprintf("%s:%s:%d:%d", chainID, datasetType, startBlock, endBlock)
	h := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(h[:])[:16] // 前16字符
}

func (r *DatasetRegistry) load() {
	data, err := os.ReadFile(filepath.Join(r.storeDir, "dataset_registry.json"))
	if err != nil {
		return
	}
	var recs []*DatasetRecord
	if json.Unmarshal(data, &recs) == nil {
		for _, rec := range recs {
			if rec.DatasetID != "" {
				r.byID[rec.DatasetID] = rec
			}
			if rec.Fingerprint != "" {
				r.byFP[rec.Fingerprint] = rec
			}
		}
	}
}

func (r *DatasetRegistry) persist() error {
	recs := make([]*DatasetRecord, 0, len(r.byID))
	for _, rec := range r.byID {
		recs = append(recs, rec)
	}
	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(r.storeDir, "dataset_registry.json.tmp")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(r.storeDir, "dataset_registry.json"))
}

// ── Feature Flag Config ──

type FeatureFlags struct {
	DownloadEngineV2 bool     `json:"download_engine_v2"`
	EnabledChains    []string `json:"enabled_chains"`
	AutoFirstSeen    bool     `json:"auto_first_seen"`
	DefaultRangeMode string   `json:"default_range_mode"` // "AUTO_FIRST_SEEN" or "TIME_RANGE"
}

func DefaultFeatureFlags() *FeatureFlags {
	return &FeatureFlags{
		DownloadEngineV2: false,
		EnabledChains:    []string{"bsc"},
		AutoFirstSeen:    true,
		DefaultRangeMode: "AUTO_FIRST_SEEN",
	}
}

func (f *FeatureFlags) IsEnabled() bool {
	return f.DownloadEngineV2
}

func (f *FeatureFlags) IsChainEnabled(chainID string) bool {
	for _, c := range f.EnabledChains {
		if c == chainID {
			return true
		}
	}
	return false
}
