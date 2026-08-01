package intelligence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ── Investigation Memory ──
//
// 保存调查状态：调查状态/已发现地址/已分析路径/已忽略实体/调查结论。
// JSON 文件持久化，避免重复分析。

// MemoryStore 管理调查记忆的读写。
type MemoryStore struct {
	mu       sync.Mutex
	storeDir string
	memories map[string]*InvestigationMemory // investigation_id → memory
}

// NewMemoryStore 创建记忆存储。storeDir 为空则仅内存。
func NewMemoryStore(storeDir string) *MemoryStore {
	s := &MemoryStore{
		storeDir: storeDir,
		memories: make(map[string]*InvestigationMemory),
	}
	if storeDir != "" {
		s.loadAll()
	}
	return s
}

// New 创建新调查记忆。
func (s *MemoryStore) New(investigationID, target string) *InvestigationMemory {
	s.mu.Lock()
	defer s.mu.Unlock()
	mem := &InvestigationMemory{
		InvestigationID: investigationID,
		Target:          target,
		DiscoveredAt:    map[string]time.Time{},
		AnalyzedPaths:   []string{},
		IgnoredEntities: []string{},
		CompletedTasks:  []string{},
		Conclusions:     []string{},
		UpdatedAt:       time.Now().UTC(),
	}
	s.memories[investigationID] = mem
	return mem
}

// Get 读取调查记忆。
func (s *MemoryStore) Get(investigationID string) (*InvestigationMemory, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mem, ok := s.memories[investigationID]
	if !ok {
		return nil, false
	}
	copy := *mem
	copy.DiscoveredAt = cloneMap(mem.DiscoveredAt)
	copy.AnalyzedPaths = append([]string(nil), mem.AnalyzedPaths...)
	copy.IgnoredEntities = append([]string(nil), mem.IgnoredEntities...)
	copy.CompletedTasks = append([]string(nil), mem.CompletedTasks...)
	copy.Conclusions = append([]string(nil), mem.Conclusions...)
	return &copy, true
}

// List 返回全部记忆（按更新时间倒序）。
func (s *MemoryStore) List() []InvestigationMemory {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]InvestigationMemory, 0, len(s.memories))
	for _, m := range s.memories {
		copy := *m
		copy.DiscoveredAt = cloneMap(m.DiscoveredAt)
		copy.AnalyzedPaths = append([]string(nil), m.AnalyzedPaths...)
		copy.IgnoredEntities = append([]string(nil), m.IgnoredEntities...)
		copy.CompletedTasks = append([]string(nil), m.CompletedTasks...)
		copy.Conclusions = append([]string(nil), m.Conclusions...)
		out = append(out, copy)
	}
	return out
}

// RecordDiscovered 记录已发现地址。
func (s *MemoryStore) RecordDiscovered(investigationID, address string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mem, ok := s.memories[investigationID]; ok {
		if mem.DiscoveredAt == nil {
			mem.DiscoveredAt = map[string]time.Time{}
		}
		mem.DiscoveredAt[address] = time.Now().UTC()
		mem.UpdatedAt = time.Now().UTC()
	}
}

// RecordPath 记录已分析路径（签名）。
func (s *MemoryStore) RecordPath(investigationID, pathSignature string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mem, ok := s.memories[investigationID]; ok {
		for _, p := range mem.AnalyzedPaths {
			if p == pathSignature {
				return // 幂等
			}
		}
		mem.AnalyzedPaths = append(mem.AnalyzedPaths, pathSignature)
		mem.UpdatedAt = time.Now().UTC()
	}
}

// RecordIgnored 记录已忽略实体。
func (s *MemoryStore) RecordIgnored(investigationID, address string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mem, ok := s.memories[investigationID]; ok {
		for _, a := range mem.IgnoredEntities {
			if a == address {
				return
			}
		}
		mem.IgnoredEntities = append(mem.IgnoredEntities, address)
		mem.UpdatedAt = time.Now().UTC()
	}
}

// RecordCompletedTask 记录已完成任务（ID 幂等）。
func (s *MemoryStore) RecordCompletedTask(investigationID, taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mem, ok := s.memories[investigationID]; ok {
		for _, id := range mem.CompletedTasks {
			if id == taskID {
				return
			}
		}
		mem.CompletedTasks = append(mem.CompletedTasks, taskID)
		mem.UpdatedAt = time.Now().UTC()
	}
}

// AddConclusion 添加调查结论。
func (s *MemoryStore) AddConclusion(investigationID, conclusion string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mem, ok := s.memories[investigationID]; ok {
		mem.Conclusions = append(mem.Conclusions, conclusion)
		mem.UpdatedAt = time.Now().UTC()
	}
}

// Save 持久化单个调查记忆（原子写）。
func (s *MemoryStore) Save(investigationID string) error {
	if s.storeDir == "" {
		return nil
	}
	s.mu.Lock()
	mem, ok := s.memories[investigationID]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	if err := os.MkdirAll(s.storeDir, 0755); err != nil {
		return err
	}
	path := filepath.Join(s.storeDir, investigationID+".json")
	data, err := json.MarshalIndent(mem, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadAll 启动时加载全部记忆文件。
func (s *MemoryStore) loadAll() {
	entries, err := os.ReadDir(s.storeDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.storeDir, entry.Name()))
		if err != nil {
			continue
		}
		var mem InvestigationMemory
		if err := json.Unmarshal(data, &mem); err != nil {
			continue
		}
		if mem.InvestigationID == "" || mem.Target == "" {
			continue
		}
		if mem.DiscoveredAt == nil {
			mem.DiscoveredAt = map[string]time.Time{}
		}
		if mem.AnalyzedPaths == nil {
			mem.AnalyzedPaths = []string{}
		}
		if mem.IgnoredEntities == nil {
			mem.IgnoredEntities = []string{}
		}
		if mem.CompletedTasks == nil {
			mem.CompletedTasks = []string{}
		}
		s.memories[mem.InvestigationID] = &mem
	}
}

func cloneMap(m map[string]time.Time) map[string]time.Time {
	out := make(map[string]time.Time, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
