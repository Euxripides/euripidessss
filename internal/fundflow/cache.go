package fundflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CacheKey 是资金流缓存键（设计 §60）。
type CacheKey struct {
	Root           string `json:"root"`
	ChainKey       string `json:"chain_key"`
	TokenScope     string `json:"token_scope,omitempty"`
	FromBlock      uint64 `json:"from_block"`
	ToBlock        uint64 `json:"to_block"`
	Goal           string `json:"goal,omitempty"`
	Depth          int    `json:"depth"`
	ScoringVersion string `json:"scoring_version"`
}

// Hash 返回稳定缓存键。
func (k CacheKey) Hash() string {
	parts := []string{
		strings.ToLower(k.Root), strings.ToLower(k.ChainKey), strings.ToLower(k.TokenScope),
		itoa(k.FromBlock), itoa(k.ToBlock), strings.ToLower(k.Goal),
		itoaInt(k.Depth), k.ScoringVersion,
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:])
}

// Cache 是资金流缓存（设计 §59）。
type Cache struct {
	root string
	mu   sync.Mutex
	ttl  time.Duration
}

// NewCache 创建缓存。
func NewCache(root string) *Cache {
	return &Cache{root: root, ttl: 30 * time.Minute}
}

// Get 读取缓存（过期返回 nil）。
func (c *Cache) Get(k CacheKey) *AnalysisResult {
	if c.root == "" {
		return nil
	}
	path := c.path(k)
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var res AnalysisResult
	if json.Unmarshal(payload, &res) != nil {
		return nil
	}
	if time.Since(res.GeneratedAt) > c.ttl {
		return nil
	}
	res.CacheHit = true
	return &res
}

// Put 写入缓存。
func (c *Cache) Put(k CacheKey, res *AnalysisResult) error {
	if c.root == "" || res == nil {
		return nil
	}
	if res.GeneratedAt.IsZero() {
		res.GeneratedAt = time.Now().UTC()
	}
	path := c.path(k)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c *Cache) path(k CacheKey) string {
	return filepath.Join(c.root, k.ChainKey, k.Hash()+".json")
}

func itoa(v uint64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func itoaInt(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}

