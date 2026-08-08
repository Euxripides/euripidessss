package prefetch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ActiveRange 是正在预取的范围记录。
type ActiveRange struct {
	ChainKey  string    `json:"chain_key"`
	Address   string    `json:"address"`
	FromBlock uint64    `json:"from_block"`
	ToBlock   uint64    `json:"to_block"`
	BatchID   string    `json:"batch_id"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ActiveRegistry 是 Active Coverage Registry（设计 P1：正在运行任务的 Range 所有权）。
type ActiveRegistry struct {
	root  string
	mu    sync.Mutex
	byKey map[string]*ActiveRange
}

// NewActiveRegistry 创建并加载 Active Registry。
func NewActiveRegistry(root string) (*ActiveRegistry, error) {
	r := &ActiveRegistry{root: filepath.Join(root, "active"), byKey: map[string]*ActiveRange{}}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

// Acquire 登记一个正在预取的范围（同一范围不重复）。
func (r *ActiveRegistry) Acquire(chainKey, address, batchID string, from, to uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := activeKey(chainKey, address, from, to)
	if _, ok := r.byKey[key]; ok {
		return false
	}
	now := time.Now().UTC()
	r.byKey[key] = &ActiveRange{
		ChainKey: chainKey, Address: strings.ToLower(address), FromBlock: from, ToBlock: to,
		BatchID: batchID, StartedAt: now, UpdatedAt: now,
	}
	_ = r.saveLocked()
	return true
}

// Release 释放范围。
func (r *ActiveRegistry) Release(chainKey, address, batchID string, from, to uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := activeKey(chainKey, address, from, to)
	if e, ok := r.byKey[key]; ok && (batchID == "" || e.BatchID == batchID) {
		delete(r.byKey, key)
		_ = r.saveLocked()
	}
}

// List 返回全部活动范围。
func (r *ActiveRegistry) List() []ActiveRange {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ActiveRange, 0, len(r.byKey))
	for _, e := range r.byKey {
		out = append(out, *e)
	}
	return out
}

func (r *ActiveRegistry) load() error {
	payload, err := os.ReadFile(filepath.Join(r.root, "active-coverage.json"))
	if err != nil {
		return nil
	}
	var items []*ActiveRange
	if json.Unmarshal(payload, &items) != nil {
		return nil
	}
	for _, e := range items {
		if e != nil && e.Address != "" {
			r.byKey[activeKey(e.ChainKey, e.Address, e.FromBlock, e.ToBlock)] = e
		}
	}
	return nil
}

func (r *ActiveRegistry) saveLocked() error {
	if err := os.MkdirAll(r.root, 0o755); err != nil {
		return err
	}
	items := make([]*ActiveRange, 0, len(r.byKey))
	for _, e := range r.byKey {
		items = append(items, e)
	}
	payload, _ := json.MarshalIndent(items, "", "  ")
	tmp := filepath.Join(r.root, "active-coverage.json.tmp")
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(r.root, "active-coverage.json"))
}

func activeKey(chainKey, address string, from, to uint64) string {
	return strings.ToLower(chainKey) + "|" + strings.ToLower(address) + "|" +
		itoa64(from) + "|" + itoa64(to)
}

