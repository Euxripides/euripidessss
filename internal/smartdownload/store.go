package smartdownload

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Store 文件系统状态存储（FS StateStore，实施方案 Phase 1 §5）。
// 每个 Job 一个原子 JSON 文件（tmp + rename），重启时全量扫描重建索引。
type Store struct {
	mu        sync.Mutex
	root      string
	batches   map[string]*BatchJob
	addresses map[string]*AddressJob
	datasets  map[string]*DatasetJob
	ranges    map[string]*RangeJob
}

// NewStore 创建/加载状态存储。
func NewStore(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("smartdownload store root 不能为空")
	}
	s := &Store{
		root:      root,
		batches:   map[string]*BatchJob{},
		addresses: map[string]*AddressJob{},
		datasets:  map[string]*DatasetJob{},
		ranges:    map[string]*RangeJob{},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Root() string { return s.root }

func (s *Store) load() error {
	if err := s.loadPacks(); err != nil {
		return err
	}
	for _, dir := range []string{"batches", "addresses", "datasets", "ranges"} {
		full := filepath.Join(s.root, dir)
		entries, err := os.ReadDir(full)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			payload, err := os.ReadFile(filepath.Join(full, e.Name()))
			if err != nil {
				continue
			}
			switch dir {
			case "batches":
				var v BatchJob
				if json.Unmarshal(payload, &v) == nil && v.ID != "" {
					s.batches[v.ID] = &v
				}
			case "addresses":
				var v AddressJob
				if json.Unmarshal(payload, &v) == nil && v.ID != "" {
					s.addresses[v.ID] = &v
				}
			case "datasets":
				var v DatasetJob
				if json.Unmarshal(payload, &v) == nil && v.ID != "" {
					s.datasets[v.ID] = &v
				}
			case "ranges":
				var v RangeJob
				if json.Unmarshal(payload, &v) == nil && v.ID != "" {
					s.ranges[v.ID] = &v
				}
			}
		}
	}
	return nil
}

// ── 保存 ──

func (s *Store) SaveBatch(v *BatchJob) error {
	return s.save("batches", v.ID, v)
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

func (s *Store) save(kind, id string, v any) error {
	if id == "" {
		return fmt.Errorf("smartdownload %s id 为空", kind)
	}
	payload, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	switch kind {
	case "batches":
		s.batches[id] = cloneJSON(v).(*BatchJob)
	case "addresses":
		s.addresses[id] = cloneJSON(v).(*AddressJob)
	case "datasets":
		s.datasets[id] = cloneJSON(v).(*DatasetJob)
	case "ranges":
		s.ranges[id] = cloneJSON(v).(*RangeJob)
	}
	return nil
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
	return cloneJSON(v).(*RangeJob)
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
	var out []*RangeJob
	for _, v := range s.ranges {
		if v.DatasetJobID == datasetJobID {
			out = append(out, cloneJSON(v).(*RangeJob))
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
		out = append(out, cloneJSON(v).(*RangeJob))
	}
	return out
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
