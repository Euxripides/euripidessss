package investigationstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// ── Index：索引存储（V1 设计 §12）──
//
// 快速定位：如地址 → 证据 ID 列表（evidence-index.json）、
// 地址 → 记忆关系 ID 列表（memory-index.json）。
// 单文件原子写 + 互斥锁，与 JSONStore 同模式。

// Index 是 map[key][]id 形式的索引，原子持久化到 dir/name。
type Index struct {
	mu    sync.Mutex
	path  string // 持久化路径（空 = 仅内存，测试用）
	index map[string][]string
}

// NewIndex 创建索引。path 非空时启动加载。
func NewIndex(path string) *Index {
	ix := &Index{
		path:  path,
		index: make(map[string][]string),
	}
	if path != "" {
		ix.load()
	}
	return ix
}

// Add 向 key 追加 id（幂等去重）并原子持久化。
func (ix *Index) Add(key, id string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ids := ix.index[key]
	for _, existing := range ids {
		if existing == id {
			return nil
		}
	}
	ix.index[key] = append(ids, id)
	return ix.saveLocked()
}

// Remove 从 key 移除 id 并原子持久化。
func (ix *Index) Remove(key, id string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ids := ix.index[key]
	out := ids[:0]
	for _, existing := range ids {
		if existing != id {
			out = append(out, existing)
		}
	}
	if len(out) == 0 {
		delete(ix.index, key)
	} else {
		ix.index[key] = out
	}
	return ix.saveLocked()
}

// Get 返回 key 关联的 id 列表（防御性拷贝）。
func (ix *Index) Get(key string) []string {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return append([]string(nil), ix.index[key]...)
}

// Keys 返回全部索引 key。
func (ix *Index) Keys() []string {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	keys := make([]string, 0, len(ix.index))
	for k := range ix.index {
		keys = append(keys, k)
	}
	return keys
}

// Save 立即持久化（供外部批量更新后调用）。
func (ix *Index) Save() error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.saveLocked()
}

// Bulk 批量添加（key → ids，幂等去重合并），一次原子持久化。
// 用于启动时从数据重建索引（自愈）。
func (ix *Index) Bulk(entries map[string][]string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for key, ids := range entries {
		for _, id := range ids {
			found := false
			for _, existing := range ix.index[key] {
				if existing == id {
					found = true
					break
				}
			}
			if !found {
				ix.index[key] = append(ix.index[key], id)
			}
		}
	}
	return ix.saveLocked()
}

// saveLocked 原子写索引文件，必须在持锁状态调用。
func (ix *Index) saveLocked() error {
	if ix.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(ix.path), 0755); err != nil {
		return err
	}
	env := envelope[map[string][]string]{
		SchemaVersion: CurrentSchemaVersion,
		Data:          ix.index,
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(ix.path, data)
}

// load 启动时加载索引文件；损坏/版本不匹配则忽略（下次 Save 重建）。
func (ix *Index) load() {
	data, err := os.ReadFile(ix.path)
	if err != nil {
		return
	}
	var env envelope[map[string][]string]
	if err := json.Unmarshal(data, &env); err != nil {
		return
	}
	if env.SchemaVersion != CurrentSchemaVersion {
		return
	}
	if env.Data != nil {
		ix.index = env.Data
	}
}
