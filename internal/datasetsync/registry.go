// Package datasetsync 实现 SQD Cloud Phase 4 本地数据面：
// 扫描 completed Manifest → .partial + SHA256 下载 → Parquet Validator → Dataset Registry/Coverage。
package datasetsync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Registry Entry 状态（Phase 5.2 P0-1：仅 ACTIVE 参与 merged/coverage/统计）。
const (
	StatusCompleted          = "COMPLETED"            // 有效已登记
	StatusQuarantined        = "QUARANTINED"          // 隔离（保留证据，不对外）
	StatusInvalidRangeLegacy = "INVALID_RANGE_LEGACY" // 修复前越界 legacy
	StatusFailed             = "FAILED"               // 校验失败/本地同步失败
	StatusCancelled          = "CANCELLED"            // 取消
)

// SyncState 本地同步状态（Phase 5.2 §9：失败可重试）。
const (
	SyncRemoteCompleted  = "REMOTE_COMPLETED"
	SyncLocalPending     = "LOCAL_SYNC_PENDING"
	SyncLocalFailed      = "LOCAL_SYNC_FAILED"
	SyncLocalSynced      = "LOCAL_SYNCED"
	SyncIndexed          = "INDEXED"
	SyncValidationFailed = "LOCAL_VALIDATION_FAILED"
)

// Entry Dataset Registry 条目（Phase 4 §30/§31）。
type Entry struct {
	ChunkKey       string     `json:"chunk_key"` // job_id/chunk_id
	JobID          string     `json:"job_id"`
	ChunkID        string     `json:"chunk_id"`
	ChainKey       string     `json:"chain_key"`
	ChainID        int64      `json:"chain_id"`
	Dataset        string     `json:"dataset"`
	FromBlock      uint64     `json:"from_block"`
	ToBlock        uint64     `json:"to_block"`
	Addresses      []string   `json:"addresses"`
	Provider       string     `json:"provider"`
	Status         string     `json:"status"`     // COMPLETED/QUARANTINED/INVALID_RANGE_LEGACY/FAILED/CANCELLED
	SyncState      string     `json:"sync_state"` // REMOTE_COMPLETED/LOCAL_SYNC_PENDING/LOCAL_SYNC_FAILED/LOCAL_SYNCED/INDEXED/LOCAL_VALIDATION_FAILED
	RowCount       int64      `json:"row_count"`
	UniqueKeyCount int64      `json:"unique_key_count"`
	DuplicateCount int64      `json:"duplicate_count"`
	MinBlock       uint64     `json:"min_block,omitempty"`
	MaxBlock       uint64     `json:"max_block,omitempty"`
	Files          []FileInfo `json:"files"`
	ManifestPath   string     `json:"manifest_path,omitempty"`
	LocalDir       string     `json:"local_dir,omitempty"`
	MergedParquet  string     `json:"merged_parquet,omitempty"`
	CompletedAt    time.Time  `json:"completed_at"`
	SyncedAt       time.Time  `json:"synced_at"`
	QuarantineReason string   `json:"quarantine_reason,omitempty"`
}

// FileInfo 本地同步后的文件信息。
type FileInfo struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Rows   int64  `json:"rows,omitempty"`
	SHA256 string `json:"sha256"`
}

// Registry Cloud 数据登记表（JSON 文件，Phase 4 §30）。
type Registry struct {
	mu    sync.Mutex
	path  string
	items map[string]*Entry
}

// NewRegistry 创建/加载 Registry。
func NewRegistry(path string) (*Registry, error) {
	r := &Registry{path: path, items: loadRegistryFile(path)}
	return r, nil
}

// loadRegistryFile 读取磁盘 Registry（不存在返回空）。
func loadRegistryFile(path string) map[string]*Entry {
	items := map[string]*Entry{}
	payload, err := os.ReadFile(path)
	if err != nil {
		return items
	}
	var list []*Entry
	if json.Unmarshal(payload, &list) == nil {
		for _, e := range list {
			if e != nil && e.ChunkKey != "" {
				items[e.ChunkKey] = e
			}
		}
	}
	return items
}

// Register 登记一个已完成 Chunk（幂等）。
func (r *Registry) Register(e *Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e.SyncedAt = time.Now()
	if e.Provider == "" {
		e.Provider = "SQD_CLOUD_EXPORT"
	}
	if e.Status == "" {
		e.Status = "COMPLETED"
	}
	r.items[e.ChunkKey] = e
	return r.saveLocked(e)
}

// IsActive 判断条目是否参与 merged/coverage/统计（Phase 5.2 P0-1 §11/§12）。
func (e *Entry) IsActive() bool {
	if e == nil {
		return false
	}
	switch e.Status {
	case "", StatusCompleted:
		return true
	default:
		return false
	}
}

// Quarantine 隔离一个已登记 Chunk（保留证据与审计字段，但排除出对外查询）。
func (r *Registry) Quarantine(chunkKey, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.items[chunkKey]
	if !ok {
		return fmt.Errorf("registry entry not found: %s", chunkKey)
	}
	e.Status = StatusInvalidRangeLegacy
	e.QuarantineReason = reason
	return r.saveLocked(e)
}

// MarkSyncFailed 标记本地同步失败（允许后续重试）。
func (r *Registry) MarkSyncFailed(chunkKey string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.items[chunkKey]
	if !ok {
		return fmt.Errorf("registry entry not found: %s", chunkKey)
	}
	e.SyncState = SyncLocalFailed
	return r.saveLocked(e)
}

// Has 判断 Chunk 是否已登记。
func (r *Registry) Has(chunkKey string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	_, ok := r.items[chunkKey]
	return ok
}

// Get 返回指定条目副本。
func (r *Registry) Get(chunkKey string) *Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	e, ok := r.items[chunkKey]
	if !ok {
		return nil
	}
	cp := *e
	cp.Addresses = append([]string(nil), e.Addresses...)
	cp.Files = append([]FileInfo(nil), e.Files...)
	return &cp
}

// Completed 返回全部已登记条目（新→旧）。
func (r *Registry) Completed() []*Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	out := make([]*Entry, 0, len(r.items))
	for _, e := range r.items {
		cp := *e
		cp.Addresses = append([]string(nil), e.Addresses...)
		cp.Files = append([]FileInfo(nil), e.Files...)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SyncedAt.After(out[j].SyncedAt) })
	return out
}

// Active 返回参与 merged/coverage/统计的有效条目（排除 QUARANTINED/INVALID/FAILED/CANCELLED）。
func (r *Registry) Active() []*Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	out := make([]*Entry, 0, len(r.items))
	for _, e := range r.items {
		if !e.IsActive() {
			continue
		}
		cp := *e
		cp.Addresses = append([]string(nil), e.Addresses...)
		cp.Files = append([]FileInfo(nil), e.Files...)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SyncedAt.After(out[j].SyncedAt) })
	return out
}

// Stats 汇总行数/文件数/字节数。
func (r *Registry) Stats() (rows, files, bytes int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	for _, e := range r.items {
		if !e.IsActive() {
			continue
		}
		rows += e.RowCount
		files += int64(len(e.Files))
		for _, f := range e.Files {
			bytes += f.Bytes
		}
	}
	return
}

// AddressTxCount 覆盖查询：返回包含该地址的已登记 Chunk 行数（Phase 4 §31 登记覆盖）。
func (r *Registry) AddressTxCount(ctx context.Context, address string) (int64, error) {
	if r == nil {
		return 0, nil
	}
	addr := strings.ToLower(strings.TrimSpace(address))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	var total int64
	for _, e := range r.items {
		if !e.IsActive() {
			continue
		}
		for _, a := range e.Addresses {
			if strings.EqualFold(a, addr) {
				total += e.RowCount
				break
			}
		}
	}
	return total, nil
}

// refreshLocked 以磁盘为准刷新缓存（多实例共享 registry.json 时读取最新状态）。
func (r *Registry) refreshLocked() {
	if r.path == "" {
		return
	}
	for key, e := range loadRegistryFile(r.path) {
		r.items[key] = e
	}
}

func (r *Registry) saveLocked(updated ...*Entry) error {
	if r.path == "" {
		return nil
	}
	// 多实例并发（8010 测试 / 8000 生产）共享同一 registry.json：
	// 保存前以磁盘为准刷新未被本次更新的条目，防止旧内存缓存覆盖隔离/取消等终态。
	fresh := loadRegistryFile(r.path)
	for _, e := range updated {
		if e != nil && e.ChunkKey != "" {
			fresh[e.ChunkKey] = e
		}
	}
	list := make([]*Entry, 0, len(fresh))
	for _, e := range fresh {
		list = append(list, e)
	}
	// 内存缓存同步为最新磁盘视图（供后续读方法使用）
	r.items = fresh
	sort.Slice(list, func(i, j int) bool { return list[i].ChunkKey < list[j].ChunkKey })
	payload, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}
