package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store 是 Investigation Cache V2 的文件存储。
// 目录：{root}/{investigation_id}.json
type Store struct {
	root string
	mu   sync.Mutex
}

// NewStore 创建存储。
func NewStore(root string) *Store {
	return &Store{root: root}
}

// Get 读取调查缓存；不存在返回 nil。
func (s *Store) Get(id string) *InvestigationCache {
	if !validID(id) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil
	}
	var inv InvestigationCache
	if err := json.Unmarshal(payload, &inv); err != nil {
		return nil
	}
	if inv.Addresses == nil {
		inv.Addresses = map[string]*AddressState{}
	}
	if inv.PrefetchCandidates == nil {
		inv.PrefetchCandidates = map[string]*CandidateSummary{}
	}
	return &inv
}

// GetOrCreate 读取或创建（不落盘，仅内存视图）。
func (s *Store) GetOrCreate(id string) *InvestigationCache {
	inv := s.Get(id)
	if inv != nil {
		return inv
	}
	now := time.Now().UTC()
	return &InvestigationCache{
		ID:                id,
		SchemaVersion:     schemaVersion,
		Addresses:         map[string]*AddressState{},
		PrefetchCandidates: map[string]*CandidateSummary{},
		UpdatedAt:         now,
	}
}

// Save 保存调查缓存（原子写）。
func (s *Store) Save(inv *InvestigationCache) error {
	if inv == nil || !validID(inv.ID) {
		return os.ErrInvalid
	}
	inv.SchemaVersion = schemaVersion
	inv.UpdatedAt = time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return err
	}
	path := s.path(inv.ID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// UpsertContext 保存上下文快照。
func (s *Store) UpsertContext(id string, ctx ContextSnapshot) (*InvestigationCache, error) {
	inv := s.GetOrCreate(id)
	ctx.UpdatedAt = time.Now().UTC()
	inv.Context = ctx
	if inv.Addresses == nil {
		inv.Addresses = map[string]*AddressState{}
	}
	if inv.PrefetchCandidates == nil {
		inv.PrefetchCandidates = map[string]*CandidateSummary{}
	}
	if err := s.Save(inv); err != nil {
		return nil, err
	}
	return inv, nil
}

// UpsertAddress 更新地址状态。
func (s *Store) UpsertAddress(id string, st *AddressState) (*InvestigationCache, error) {
	inv := s.GetOrCreate(id)
	st.Address = strings.ToLower(strings.TrimSpace(st.Address))
	st.UpdatedAt = time.Now().UTC()
	inv.Addresses[st.Address] = st
	return inv, s.Save(inv)
}

// UpsertCandidate 更新候选摘要。
func (s *Store) UpsertCandidate(id string, c *CandidateSummary) (*InvestigationCache, error) {
	inv := s.GetOrCreate(id)
	c.Address = strings.ToLower(strings.TrimSpace(c.Address))
	c.UpdatedAt = time.Now().UTC()
	inv.PrefetchCandidates[c.Address] = c
	return inv, s.Save(inv)
}

// AddGraphKey 记录图缓存键。
func (s *Store) AddGraphKey(id, key string) (*InvestigationCache, error) {
	inv := s.GetOrCreate(id)
	key = strings.TrimSpace(key)
	for _, k := range inv.GraphKeys {
		if k == key {
			return inv, nil
		}
	}
	inv.GraphKeys = append(inv.GraphKeys, key)
	return inv, s.Save(inv)
}

// List 返回全部调查缓存（按 ID 排序）。
func (s *Store) List() []*InvestigationCache {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*InvestigationCache
	_ = filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		payload, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var inv InvestigationCache
		if json.Unmarshal(payload, &inv) == nil {
			out = append(out, &inv)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Delete 删除调查缓存。
func (s *Store) Delete(id string) error {
	if !validID(id) {
		return os.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.root, id+".json")
}

func validID(id string) bool {
	if id == "" || strings.ContainsAny(id, `/\:*?"<>|`) || strings.HasPrefix(id, ".") {
		return false
	}
	return true
}

