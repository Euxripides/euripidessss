// Package datasetsync 实现 SQD Cloud Phase 4 本地数据面：
// 扫描 completed Manifest → .partial + SHA256 下载 → Parquet Validator → Dataset Registry/Coverage。
package datasetsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	ChunkKey         string           `json:"chunk_key"` // job_id/chunk_id
	JobID            string           `json:"job_id"`
	ChunkID          string           `json:"chunk_id"`
	ChainKey         string           `json:"chain_key"`
	ChainID          int64            `json:"chain_id"`
	Dataset          string           `json:"dataset"`
	FromBlock        uint64           `json:"from_block"`
	ToBlock          uint64           `json:"to_block"`
	Addresses        []string         `json:"addresses"`
	Provider         string           `json:"provider"`
	Status           string           `json:"status"`     // COMPLETED/QUARANTINED/INVALID_RANGE_LEGACY/FAILED/CANCELLED
	SyncState        string           `json:"sync_state"` // REMOTE_COMPLETED/LOCAL_SYNC_PENDING/LOCAL_SYNC_FAILED/LOCAL_SYNCED/INDEXED/LOCAL_VALIDATION_FAILED
	RowCount         int64            `json:"row_count"`
	UniqueKeyCount   int64            `json:"unique_key_count"`
	DuplicateCount   int64            `json:"duplicate_count"`
	AddressRowCounts map[string]int64 `json:"address_row_counts,omitempty"`
	MinBlock         uint64           `json:"min_block,omitempty"`
	MaxBlock         uint64           `json:"max_block,omitempty"`
	Files            []FileInfo       `json:"files"`
	ManifestPath     string           `json:"manifest_path,omitempty"`
	LocalDir         string           `json:"local_dir,omitempty"`
	MergedParquet    string           `json:"merged_parquet,omitempty"`
	CompletedAt      time.Time        `json:"completed_at"`
	SyncedAt         time.Time        `json:"synced_at"`
	QuarantineReason string           `json:"quarantine_reason,omitempty"`
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
	mu       sync.Mutex
	path     string
	items    map[string]*Entry
	coverage map[string]*CoverageEntry // address_dataset_coverage 索引（Phase 5.4.1 §3）
}

// CoverageEntry 地址×数据集覆盖索引项。
type CoverageEntry struct {
	ChainID   int64           `json:"chain_id"`
	Dataset   string          `json:"dataset"`
	FromBlock uint64          `json:"covered_from"`
	ToBlock   uint64          `json:"covered_to"`
	RowCount  int64           `json:"row_count"`
	Ranges    []CoverageRange `json:"covered_ranges,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type CoverageRange struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
}

// NewRegistry 创建/加载 Registry。
func NewRegistry(path string) (*Registry, error) {
	r := &Registry{path: path, items: loadRegistryFile(path), coverage: map[string]*CoverageEntry{}}
	r.rebuildCoverageLocked()
	return r, nil
}

func coverageKey(chainKey, address, dataset string) string {
	return strings.ToLower(strings.TrimSpace(chainKey)) + "|" +
		strings.ToLower(strings.TrimSpace(address)) + "|" +
		strings.ToLower(strings.TrimSpace(dataset))
}

// rebuildCoverageLocked 从 ACTIVE 条目重建 address_dataset_coverage 索引。
func (r *Registry) rebuildCoverageLocked() {
	r.coverage = map[string]*CoverageEntry{}
	for _, e := range r.authoritativeEntriesLocked() {
		for _, addr := range e.Addresses {
			addr = strings.ToLower(strings.TrimSpace(addr))
			key := coverageKey(e.ChainKey, addr, e.Dataset)
			ce := r.coverage[key]
			if ce == nil {
				ce = &CoverageEntry{ChainID: e.ChainID, Dataset: e.Dataset}
				r.coverage[key] = ce
			}
			if ce.FromBlock == 0 || e.FromBlock < ce.FromBlock {
				ce.FromBlock = e.FromBlock
			}
			if e.ToBlock > ce.ToBlock {
				ce.ToBlock = e.ToBlock
			}
			ce.RowCount += authoritativeRowsForAddress(e, addr)
			ce.Ranges = appendCoverageRange(ce.Ranges, CoverageRange{From: e.FromBlock, To: e.ToBlock})
			if e.SyncedAt.After(ce.UpdatedAt) {
				ce.UpdatedAt = e.SyncedAt
			}
		}
	}
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
	if err := r.saveLocked(e); err != nil {
		return err
	}
	r.rebuildCoverageLocked()
	return nil
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

// IsAuthoritative 判断条目是否已经完成本地校验、建立索引，且所有声明的
// 本地文件仍然存在。Registry 对外统计、覆盖索引和 merged 数据源只能使用
// 权威条目；LOCAL_SYNCED 只是同步中间态，不能等同于可查询数据。
func (e *Entry) IsAuthoritative() bool {
	if !e.IsActive() || e.SyncState != SyncIndexed {
		return false
	}
	if e.ToBlock < e.FromBlock || e.RowCount < 0 || e.UniqueKeyCount < 0 || e.DuplicateCount != 0 ||
		(e.RowCount > 0 && (e.UniqueKeyCount == 0 || e.UniqueKeyCount != e.RowCount)) {
		return false
	}
	if e.ChainID <= 0 || strings.TrimSpace(e.ChainKey) == "" ||
		(strings.ToLower(strings.TrimSpace(e.Dataset)) != "token_transfer" && strings.ToLower(strings.TrimSpace(e.Dataset)) != "token_transfers") ||
		len(e.Addresses) == 0 {
		return false
	}
	for _, address := range e.Addresses {
		if !manifestEVMAddress.MatchString(strings.TrimSpace(address)) {
			return false
		}
	}
	if e.RowCount > 0 && (e.MinBlock < e.FromBlock || e.MaxBlock > e.ToBlock || e.MaxBlock < e.MinBlock) {
		return false
	}
	if len(e.Addresses) > 1 && len(e.AddressRowCounts) == 0 {
		return false
	}
	if e.RowCount > 0 && len(e.Files) == 0 {
		return false
	}
	for _, f := range e.Files {
		path := f.Path
		if !filepath.IsAbs(path) {
			if strings.TrimSpace(e.LocalDir) == "" {
				return false
			}
			path = filepath.Join(e.LocalDir, filepath.FromSlash(path))
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		if f.Bytes <= 0 || info.Size() != f.Bytes || !validSHA256(f.SHA256) {
			return false
		}
		sha, err := registryFileSHA256(path)
		if err != nil || !strings.EqualFold(sha, f.SHA256) {
			return false
		}
	}
	return true
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
	cp.AddressRowCounts = cloneAddressCounts(e.AddressRowCounts)
	return &cp
}

// Refresh 从磁盘刷新一次 Registry 快照。批量同步应在扫描开始时调用一次，
// 后续使用 GetCached，避免每个 manifest 都重复读取 registry.json、重算覆盖并
// 对全部权威文件重新执行 SHA256 校验。
func (r *Registry) Refresh() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
}

// GetCached 返回当前内存快照中的条目副本，不触发磁盘刷新。
// 仅用于已经显式 Refresh 的单次批处理；普通跨进程读取仍应使用 Get。
func (r *Registry) GetCached(chunkKey string) *Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.items[chunkKey]
	if !ok {
		return nil
	}
	cp := *e
	cp.Addresses = append([]string(nil), e.Addresses...)
	cp.Files = append([]FileInfo(nil), e.Files...)
	cp.AddressRowCounts = cloneAddressCounts(e.AddressRowCounts)
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
		cp.AddressRowCounts = cloneAddressCounts(e.AddressRowCounts)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SyncedAt.After(out[j].SyncedAt) })
	return out
}

// Active 返回参与 merged/coverage/统计的权威条目。
//
// 对相同链、数据集、地址集合和区块范围的重复请求，只保留最近一次成功
// INDEXED 的版本。原始版本仍由 Completed 返回，确保审计证据不丢失。
func (r *Registry) Active() []*Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	selected := r.authoritativeEntriesLocked()
	out := make([]*Entry, 0, len(selected))
	for _, e := range selected {
		cp := *e
		cp.Addresses = append([]string(nil), e.Addresses...)
		cp.Files = append([]FileInfo(nil), e.Files...)
		cp.AddressRowCounts = cloneAddressCounts(e.AddressRowCounts)
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
	for _, e := range r.authoritativeEntriesLocked() {
		// Validator 的事件键为 (chain_id, transaction_hash, log_index)；
		// UniqueKeyCount 是条目内去重后的权威行数。
		rows += authoritativeRows(e)
		files += int64(len(e.Files))
		for _, f := range e.Files {
			bytes += f.Bytes
		}
	}
	return
}

// Authoritative 返回 Registry 的权威可查询视图。它与 Active 等价，名称用于
// API 明确区分 Completed（原始审计记录）和真正可用于统计的条目。
func (r *Registry) Authoritative() []*Entry {
	return r.Active()
}

// authoritativeEntriesLocked 选择每个业务请求的最近成功版本。
// 调用方必须持有 r.mu。
func (r *Registry) authoritativeEntriesLocked() []*Entry {
	selected := make(map[string]*Entry)
	for _, e := range r.items {
		if !e.IsAuthoritative() {
			continue
		}
		key := businessRangeKey(e)
		if current := selected[key]; current == nil || newerAuthoritativeEntry(e, current) {
			selected[key] = e
		}
	}
	out := make([]*Entry, 0, len(selected))
	for _, e := range selected {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SyncedAt.After(out[j].SyncedAt) })
	return out
}

func businessRangeKey(e *Entry) string {
	addresses := make([]string, 0, len(e.Addresses))
	seen := map[string]bool{}
	for _, address := range e.Addresses {
		address = strings.ToLower(strings.TrimSpace(address))
		if address == "" || seen[address] {
			continue
		}
		seen[address] = true
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	dataset := strings.ToLower(strings.TrimSpace(e.Dataset))
	if dataset == "token_transfer" {
		dataset = "token_transfers"
	}
	chain := strings.ToLower(strings.TrimSpace(e.ChainKey))
	if chain == "" && e.ChainID != 0 {
		chain = fmt.Sprintf("chain:%d", e.ChainID)
	}
	return fmt.Sprintf("%s|%s|%d|%d|%s", chain, dataset, e.FromBlock, e.ToBlock, strings.Join(addresses, ","))
}

func newerAuthoritativeEntry(candidate, current *Entry) bool {
	// Cloud 完成时间代表业务版本；本地重试时间不能让旧任务重新夺回权威位。
	// legacy 条目没有 completed_at 时再回退 synced_at。
	if (!candidate.CompletedAt.IsZero() || !current.CompletedAt.IsZero()) &&
		!candidate.CompletedAt.Equal(current.CompletedAt) {
		return candidate.CompletedAt.After(current.CompletedAt)
	}
	if !candidate.SyncedAt.Equal(current.SyncedAt) {
		return candidate.SyncedAt.After(current.SyncedAt)
	}
	return candidate.ChunkKey > current.ChunkKey
}

func authoritativeRows(e *Entry) int64 {
	if e == nil || e.RowCount <= 0 {
		return 0
	}
	if e.UniqueKeyCount > 0 && e.UniqueKeyCount <= e.RowCount {
		return e.UniqueKeyCount
	}
	return e.RowCount
}

func authoritativeRowsForAddress(e *Entry, address string) int64 {
	if e == nil {
		return 0
	}
	address = strings.ToLower(strings.TrimSpace(address))
	if e.AddressRowCounts != nil {
		return e.AddressRowCounts[address]
	}
	if len(e.Addresses) == 1 && strings.EqualFold(strings.TrimSpace(e.Addresses[0]), address) {
		return authoritativeRows(e)
	}
	return 0
}

func cloneAddressCounts(in map[string]int64) map[string]int64 {
	if in == nil {
		return nil
	}
	out := make(map[string]int64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func appendCoverageRange(ranges []CoverageRange, next CoverageRange) []CoverageRange {
	if next.To < next.From {
		return ranges
	}
	ranges = append(ranges, next)
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].From == ranges[j].From {
			return ranges[i].To < ranges[j].To
		}
		return ranges[i].From < ranges[j].From
	})
	out := make([]CoverageRange, 0, len(ranges))
	for _, current := range ranges {
		if len(out) == 0 || (out[len(out)-1].To != ^uint64(0) && current.From > out[len(out)-1].To+1) {
			out = append(out, current)
			continue
		}
		if current.To > out[len(out)-1].To {
			out[len(out)-1].To = current.To
		}
	}
	return out
}

func validSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func registryFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
	match := "|" + addr + "|"
	for key, ce := range r.coverage {
		if strings.Contains(key, match) {
			total += ce.RowCount
		}
	}
	return total, nil
}

// AddressCoverage 返回指定地址×数据集覆盖范围（索引 O(1) 查询）。
func (r *Registry) AddressCoverage(chainKey, address, dataset string) (CoverageEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	ce, ok := r.coverage[coverageKey(chainKey, address, dataset)]
	if !ok {
		return CoverageEntry{}, false
	}
	return *ce, true
}

// refreshLocked 以磁盘为准刷新缓存（多实例共享 registry.json 时读取最新状态）。
func (r *Registry) refreshLocked() {
	if r.path == "" {
		return
	}
	// 磁盘是多实例共享 Registry 的唯一事实源。直接替换而不是增量合并，
	// 否则已被审计清理的旧版本会永久残留在内存并继续污染统计。
	r.items = loadRegistryFile(r.path)
	r.rebuildCoverageLocked()
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
