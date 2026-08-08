package graphcache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store 是图扩展缓存的文件存储（原子写 + TTL）。
type Store struct {
	root       string
	mu         sync.Mutex
	recentFrom uint64 // >= 该区块视为近实时区间（短 TTL），否则历史区间（长 TTL）

	recentTTL     time.Duration
	historicalTTL time.Duration

	hits   int64
	misses int64
}

// NewStore 创建缓存存储。
// recentFrom：区块号大于等于该值的区间视为“最近活跃区间”。
func NewStore(root string, recentFrom uint64) *Store {
	return &Store{
		root:          root,
		recentFrom:    recentFrom,
		recentTTL:     5 * time.Minute,
		historicalTTL: 7 * 24 * time.Hour,
	}
}

// TTLFor 返回键适用的 TTL（设计 §41：历史闭合区间长期缓存，近实时短 TTL）。
func (s *Store) TTLFor(k Key) time.Duration {
	if s.recentFrom > 0 && k.ToBlock >= s.recentFrom {
		return s.recentTTL
	}
	return s.historicalTTL
}

// Get 读取缓存；未命中或过期返回 nil。
func (s *Store) Get(k Key) *CacheEntry {
	k = k.Normalized()
	path := k.FilePath(s.root)
	payload, err := os.ReadFile(path)
	if err != nil {
		s.mu.Lock()
		s.misses++
		s.mu.Unlock()
		return nil
	}
	var entry CacheEntry
	if err := json.Unmarshal(payload, &entry); err != nil {
		s.mu.Lock()
		s.misses++
		s.mu.Unlock()
		return nil
	}
	if !entry.ExpiresAt.IsZero() && time.Now().After(entry.ExpiresAt) {
		_ = os.Remove(path)
		s.mu.Lock()
		s.misses++
		s.mu.Unlock()
		return nil
	}
	s.mu.Lock()
	s.hits++
	s.mu.Unlock()
	return &entry
}

// Put 写入缓存。
func (s *Store) Put(k Key, res Result, ttl time.Duration) error {
	k = k.Normalized()
	res.Key = k
	entry := CacheEntry{
		Result:    res,
		ExpiresAt: time.Now().Add(ttl),
		CreatedAt: time.Now(),
	}
	path := k.FilePath(s.root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Delete 删除指定键缓存。
func (s *Store) Delete(k Key) error {
	path := k.FilePath(s.root)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// InvalidateAddress 删除某地址的全部图扩展缓存，返回删除数。
func (s *Store) InvalidateAddress(chainID int64, address string) int {
	address = strings.ToLower(strings.TrimSpace(address))
	if len(address) < 4 {
		return 0
	}
	dir := filepath.Join(s.root, itoa(chainID), address[2:4], address)
	return s.removeDirEntries(dir)
}

// InvalidateDataset 删除包含指定 Dataset 的图扩展缓存（设计 §42 失效触发）。
func (s *Store) InvalidateDataset(chainID int64, address, dataset string) int {
	address = strings.ToLower(strings.TrimSpace(address))
	dataset = strings.ToLower(strings.TrimSpace(dataset))
	if len(address) < 4 || dataset == "" {
		return 0
	}
	dir := filepath.Join(s.root, itoa(chainID), address[2:4], address)
	removed := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		payload, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var entry CacheEntry
		if json.Unmarshal(payload, &entry) != nil {
			return nil
		}
		for _, ds := range entry.Result.Key.DatasetSet {
			if ds == dataset {
				if os.Remove(path) == nil {
					removed++
				}
				break
			}
		}
		return nil
	})
	return removed
}

// Stats 返回缓存统计。
func (s *Store) Stats() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"hits":   s.hits,
		"misses": s.misses,
		"count":  s.countLocked(),
	}
}

func (s *Store) countLocked() int {
	count := 0
	_ = filepath.WalkDir(s.root, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			count++
		}
		return nil
	})
	return count
}

func (s *Store) removeDirEntries(dir string) int {
	removed := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		if os.Remove(path) == nil {
			removed++
		}
		return nil
	})
	return removed
}

// List 返回全部缓存条目（测试/管理用）。
func (s *Store) List() []CacheEntry {
	var out []CacheEntry
	_ = filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		payload, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var e CacheEntry
		if json.Unmarshal(payload, &e) == nil {
			out = append(out, e)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Result.Key.String() < out[j].Result.Key.String() })
	return out
}

func formatInt(v int64) string {
	return fmt.Sprintf("%d", v)
}

func formatUint(v uint64) string {
	return fmt.Sprintf("%d", v)
}

