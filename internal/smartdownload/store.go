package smartdownload

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

const maxStoreLoadWorkers = 32

// Store 文件系统状态存储（FS StateStore，实施方案 Phase 1 §5）。
// 每个 Job 一个原子 JSON 文件（tmp + rename），重启时全量扫描重建索引。
type Store struct {
	mu                 sync.Mutex
	snapshotWriteMu    sync.Mutex
	root               string
	batches            map[string]*BatchJob
	addresses          map[string]*AddressJob
	datasets           map[string]*DatasetJob
	ranges             map[string]*RangeJob
	rangeIDsByDataset  map[string]map[string]struct{}
	rangeDatasetByID   map[string]string
	revision           uint64
	activeWrites       int
	loadedFromSnapshot bool
}

// NewStore 创建/加载状态存储。
func NewStore(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("smartdownload store root 不能为空")
	}
	s := &Store{
		root:              root,
		batches:           map[string]*BatchJob{},
		addresses:         map[string]*AddressJob{},
		datasets:          map[string]*DatasetJob{},
		ranges:            map[string]*RangeJob{},
		rangeIDsByDataset: map[string]map[string]struct{}{},
		rangeDatasetByID:  map[string]string{},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Root() string { return s.root }

func (s *Store) load() error {
	if s.loadSnapshot() {
		s.rebuildRangeDatasetIndex()
		s.loadedFromSnapshot = true
		return nil
	}
	if err := s.loadPacks(); err != nil {
		return err
	}
	for _, dir := range []string{"batches", "addresses", "datasets", "ranges"} {
		if err := s.loadDirectory(dir); err != nil {
			return err
		}
	}
	// 快照只是可重建缓存；写入失败不应阻断权威 JSON 状态恢复。
	_ = s.writeSnapshot()
	return nil
}

func (s *Store) rebuildRangeDatasetIndex() {
	s.rangeIDsByDataset = map[string]map[string]struct{}{}
	s.rangeDatasetByID = map[string]string{}
	for id, value := range s.ranges {
		if value == nil || value.DatasetJobID == "" {
			continue
		}
		ids := s.rangeIDsByDataset[value.DatasetJobID]
		if ids == nil {
			ids = map[string]struct{}{}
			s.rangeIDsByDataset[value.DatasetJobID] = ids
		}
		ids[id] = struct{}{}
		s.rangeDatasetByID[id] = value.DatasetJobID
	}
}

// loadDirectory 并行完成独立 JSON 文件的磁盘读取和反序列化，再按 ReadDir
// 的稳定顺序写入内存索引。这样既能加速大量 Range 状态恢复，也保留原有的
// 重复 ID 覆盖语义；Store 尚未发布，不需要在合并阶段加锁。
func (s *Store) loadDirectory(dir string) error {
	full := filepath.Join(s.root, dir)
	entries, err := os.ReadDir(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(full, entry.Name()))
	}
	if len(paths) == 0 {
		return nil
	}

	values := make([]any, len(paths))
	workerCount := storeLoadWorkerCount(len(paths))
	jobs := make(chan int, workerCount)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for index := range jobs {
				payload, readErr := os.ReadFile(paths[index])
				if readErr != nil {
					continue
				}
				values[index] = decodeStoreValue(dir, payload)
			}
		}()
	}
	for index := range paths {
		jobs <- index
	}
	close(jobs)
	workers.Wait()

	for _, value := range values {
		s.mergeLoadedValue(dir, value)
	}
	return nil
}

func storeLoadWorkerCount(fileCount int) int {
	if fileCount <= 0 {
		return 0
	}
	workers := runtime.GOMAXPROCS(0) * 2
	if workers < 4 {
		workers = 4
	}
	if workers > maxStoreLoadWorkers {
		workers = maxStoreLoadWorkers
	}
	if workers > fileCount {
		workers = fileCount
	}
	return workers
}

func decodeStoreValue(dir string, payload []byte) any {
	switch dir {
	case "batches":
		var value BatchJob
		if json.Unmarshal(payload, &value) == nil && value.ID != "" {
			return &value
		}
	case "addresses":
		var value AddressJob
		if json.Unmarshal(payload, &value) == nil && value.ID != "" {
			return &value
		}
	case "datasets":
		var value DatasetJob
		if json.Unmarshal(payload, &value) == nil && value.ID != "" {
			return &value
		}
	case "ranges":
		var value RangeJob
		if json.Unmarshal(payload, &value) == nil && value.ID != "" {
			return &value
		}
	}
	return nil
}

func (s *Store) mergeLoadedValue(dir string, value any) {
	switch typed := value.(type) {
	case *BatchJob:
		if dir == "batches" {
			s.batches[typed.ID] = typed
		}
	case *AddressJob:
		if dir == "addresses" {
			s.addresses[typed.ID] = typed
		}
	case *DatasetJob:
		if dir == "datasets" {
			s.datasets[typed.ID] = typed
		}
	case *RangeJob:
		if dir == "ranges" {
			if previousDataset := s.rangeDatasetByID[typed.ID]; previousDataset != "" && previousDataset != typed.DatasetJobID {
				if ids := s.rangeIDsByDataset[previousDataset]; ids != nil {
					delete(ids, typed.ID)
					if len(ids) == 0 {
						delete(s.rangeIDsByDataset, previousDataset)
					}
				}
			}
			s.ranges[typed.ID] = typed
			s.rangeDatasetByID[typed.ID] = typed.DatasetJobID
			if typed.DatasetJobID != "" {
				ids := s.rangeIDsByDataset[typed.DatasetJobID]
				if ids == nil {
					ids = map[string]struct{}{}
					s.rangeIDsByDataset[typed.DatasetJobID] = ids
				}
				ids[typed.ID] = struct{}{}
			}
		}
	}
}

// ── 保存 ──

func (s *Store) SaveBatch(v *BatchJob) error {
	if err := s.save("batches", v.ID, v); err != nil {
		return err
	}
	if v.Status.Terminal() {
		// 终态批次通常代表一轮密集状态写入结束，此时刷新快照，让下次启动
		// 无需重新扫描数万个 Range 文件。并发写入时 writeSnapshot 会拒绝提交。
		_ = s.writeSnapshot()
	}
	return nil
}

func (s *Store) SaveAddress(v *AddressJob) error {
	return s.save("addresses", v.ID, v)
}

func (s *Store) SaveDataset(v *DatasetJob) error {
	return s.save("datasets", v.ID, v)
}

func (s *Store) SaveRange(v *RangeJob) error {
	return s.save("ranges", v.ID, v)
}

// SaveRanges persists independent Range jobs concurrently. Large TURBO
// batches can legitimately change thousands of range owners in one allocator
// pass; serial file writes while Service.mu is held would otherwise block
// claims and lifecycle operations for minutes.
func (s *Store) SaveRanges(values []*RangeJob) error {
	if len(values) == 0 {
		return nil
	}
	workers := storeLoadWorkerCount(len(values))
	jobs := make(chan *RangeJob, workers)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wg.Done()
			for value := range jobs {
				if value == nil {
					continue
				}
				if err := s.SaveRange(value); err != nil {
					errOnce.Do(func() { firstErr = err })
				}
			}
		}()
	}
	for _, value := range values {
		jobs <- value
	}
	close(jobs)
	wg.Wait()
	return firstErr
}

func (s *Store) save(kind, id string, v any) error {
	if id == "" {
		return fmt.Errorf("smartdownload %s id 为空", kind)
	}
	payload, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	s.beginMutation()
	committed := false
	defer func() { s.finishMutation(kind, id, v, committed) }()
	dir := filepath.Join(s.root, kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, id+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) beginMutation() {
	s.mu.Lock()
	s.revision++
	s.activeWrites++
	s.mu.Unlock()
	// 先移除清单再触碰权威文件；即使进程在写入中崩溃，下次启动也只会
	// 回退逐文件恢复，不会误用旧快照。
	_ = os.Remove(filepath.Join(s.root, storeSnapshotManifestName))
	_ = os.Remove(filepath.Join(s.root, storeSnapshotManifestName+".tmp"))
}

func (s *Store) finishMutation(kind, id string, v any, committed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if committed {
		value := cloneJSON(v)
		if rangeJob, ok := v.(*RangeJob); ok {
			value = cloneRangeJob(rangeJob)
		}
		s.mergeLoadedValue(kind, value)
	}
	s.activeWrites--
}

// ── 读取（深拷贝，防别名修改）──

func (s *Store) GetBatch(id string) *BatchJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.batches[id]
	if !ok {
		return nil
	}
	return cloneJSON(v).(*BatchJob)
}

func (s *Store) GetAddress(id string) *AddressJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.addresses[id]
	if !ok {
		return nil
	}
	return cloneJSON(v).(*AddressJob)
}

func (s *Store) GetDataset(id string) *DatasetJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.datasets[id]
	if !ok {
		return nil
	}
	return cloneJSON(v).(*DatasetJob)
}

func (s *Store) GetRange(id string) *RangeJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.ranges[id]
	if !ok {
		return nil
	}
	return cloneRangeJob(v)
}

// ── 列表 ──

func (s *Store) ListBatches() []*BatchJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*BatchJob, 0, len(s.batches))
	for _, v := range s.batches {
		out = append(out, cloneJSON(v).(*BatchJob))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (s *Store) ListAddressesByBatch(batchID string) []*AddressJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*AddressJob
	for _, v := range s.addresses {
		if v.BatchID == batchID {
			out = append(out, cloneJSON(v).(*AddressJob))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

func (s *Store) ListDatasetsByAddress(addressJobID string) []*DatasetJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*DatasetJob
	for _, v := range s.datasets {
		if v.AddressJobID == addressJobID {
			out = append(out, cloneJSON(v).(*DatasetJob))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dataset < out[j].Dataset })
	return out
}

func (s *Store) ListRangesByDataset(datasetJobID string) []*RangeJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := s.rangeIDsByDataset[datasetJobID]
	out := make([]*RangeJob, 0, len(ids))
	for id := range ids {
		if v := s.ranges[id]; v != nil {
			out = append(out, cloneRangeJob(v))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromBlock == out[j].FromBlock {
			return out[i].ToBlock < out[j].ToBlock
		}
		return out[i].FromBlock < out[j].FromBlock
	})
	return out
}

func (s *Store) ListAddresses() []*AddressJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*AddressJob, 0, len(s.addresses))
	for _, v := range s.addresses {
		out = append(out, cloneJSON(v).(*AddressJob))
	}
	return out
}

func (s *Store) ListDatasets() []*DatasetJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*DatasetJob, 0, len(s.datasets))
	for _, v := range s.datasets {
		out = append(out, cloneJSON(v).(*DatasetJob))
	}
	return out
}

func (s *Store) ListRanges() []*RangeJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*RangeJob, 0, len(s.ranges))
	for _, v := range s.ranges {
		out = append(out, cloneRangeJob(v))
	}
	return out
}

// RunningExecutionCountsByBatch counts active provider executions without
// cloning the complete range store. Followers of one SharedWork represent one
// provider request and therefore consume one execution slot together.
func (s *Store) RunningExecutionCountsByBatch(batchID string, owner RangeOwner) (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	totalRunning, laneRunning := 0, 0
	seenSharedWork := map[string]struct{}{}
	for _, rangeJob := range s.ranges {
		if rangeJob == nil || rangeJob.BatchID != batchID || rangeJob.Status != RangeRunning {
			continue
		}
		if rangeJob.SharedWorkID != "" {
			if _, seen := seenSharedWork[rangeJob.SharedWorkID]; seen {
				continue
			}
			seenSharedWork[rangeJob.SharedWorkID] = struct{}{}
		}
		totalRunning++
		if owner != "" && rangeJob.Owner == owner {
			laneRunning++
		}
	}
	return totalRunning, laneRunning
}

func cloneRangeJob(value *RangeJob) *RangeJob {
	if value == nil {
		return nil
	}
	out := *value
	out.FailedProviders = append([]string(nil), value.FailedProviders...)
	if value.CertifiedAt != nil {
		certifiedAt := *value.CertifiedAt
		out.CertifiedAt = &certifiedAt
	}
	if value.StartedAt != nil {
		startedAt := *value.StartedAt
		out.StartedAt = &startedAt
	}
	if value.FinishedAt != nil {
		finishedAt := *value.FinishedAt
		out.FinishedAt = &finishedAt
	}
	return &out
}

// ── 辅助 ──

// cloneJSON 深拷贝（JSON 往返，简单可靠）。
func cloneJSON(v any) any {
	payload, err := json.Marshal(v)
	if err != nil {
		return v
	}
	out := v
	if json.Unmarshal(payload, &out) != nil {
		return v
	}
	return out
}
