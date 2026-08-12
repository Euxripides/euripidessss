package api

import (
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// cachedValue 保存带时间戳的缓存条目。
type cachedValue struct {
	at    time.Time
	value any
}

// ttlCache 是并发安全的 TTL 内存缓存。
type ttlCache struct {
	mu    sync.Mutex
	items map[string]cachedValue
	ttl   time.Duration
}

func newTTLCache(ttl time.Duration) *ttlCache {
	return &ttlCache{items: make(map[string]cachedValue), ttl: ttl}
}

func (c *ttlCache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok || time.Since(item.at) > c.ttl {
		return nil, false
	}
	return item.value, true
}

func (c *ttlCache) Set(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = cachedValue{at: time.Now(), value: value}
}

// singleflightCache 合并同一 key 的并发计算，并缓存成功结果。
type singleflightCache struct {
	cache    *ttlCache
	mu       sync.Mutex
	inflight map[string]chan struct{}
}

func newSingleflightCache(ttl time.Duration) *singleflightCache {
	return &singleflightCache{cache: newTTLCache(ttl), inflight: make(map[string]chan struct{})}
}

func (s *singleflightCache) Do(key string, fn func() (any, error)) (any, error) {
	if v, ok := s.cache.Get(key); ok {
		return v, nil
	}
	s.mu.Lock()
	if ch, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		<-ch
		if v, ok := s.cache.Get(key); ok {
			return v, nil
		}
	} else {
		ch := make(chan struct{})
		s.inflight[key] = ch
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.inflight, key)
			close(ch)
			s.mu.Unlock()
		}()
	}
	value, err := fn()
	if err == nil {
		s.cache.Set(key, value)
	}
	return value, err
}

var (
	explorerHomeFlight  = newSingleflightCache(30 * time.Second)
	dataQualityCache    = newTTLCache(60 * time.Second)
	financialQualityTTL = newTTLCache(60 * time.Second)
)

// investigationExists 判断调查 ID 是否在任一本地调查存储中出现过。
func investigationExists(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || cfg == nil {
		return false
	}
	// “default” 是案件/报告工作区的默认虚拟调查，视为存在。
	if id == "default" {
		return true
	}
	if investigationCacheStore != nil && investigationCacheStore.Get(id) != nil {
		return true
	}
	base := filepath.Join(cfg.RootDir, "backend", "data")
	patterns := []string{
		filepath.Join(base, "investigation", id),
		filepath.Join(base, "investigation", "plans", id+"*"),
		filepath.Join(base, "investigation", "tasks", id+"*"),
		filepath.Join(base, "investigation", "cache", id+".json"),
		filepath.Join(base, "entity-intelligence", "leads", id),
		filepath.Join(base, "entity-intelligence", "manual", id+".json"),
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err == nil && len(matches) > 0 {
			return true
		}
	}
	return false
}
