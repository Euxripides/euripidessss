// Package registry 实现 Dataset Registry Coverage Index V2（设计 V1.0）：
// 分片文件存储、区间覆盖查询、FULL/PARTIAL HIT、快照 TTL、Schema 兼容性、热缓存。
package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Interval 区块区间。
type Interval struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
}

// CertifiedRange 已认证覆盖区间。
type CertifiedRange struct {
	FromBlock uint64 `json:"from_block"`
	ToBlock   uint64 `json:"to_block"`
	Rows      int64  `json:"rows"`
}

// SnapshotCoverage 快照类 Dataset（余额等）覆盖（设计 §6/§7）。
type SnapshotCoverage struct {
	Block      uint64    `json:"block"`
	Time       time.Time `json:"time"`
	TTLSeconds int64     `json:"ttl_seconds"`
}

// Fresh 判断快照是否在 TTL 内。
func (s *SnapshotCoverage) Fresh(now time.Time) bool {
	if s == nil || s.Time.IsZero() || s.TTLSeconds <= 0 {
		return false
	}
	return now.Before(s.Time.Add(time.Duration(s.TTLSeconds) * time.Second))
}

// DatasetCoverage 单地址单 Dataset 覆盖。
type DatasetCoverage struct {
	CompatibilityKey string            `json:"compatibility_key"`
	CertifiedRanges  []CertifiedRange  `json:"certified_ranges,omitempty"`
	Snapshot         *SnapshotCoverage `json:"snapshot,omitempty"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// CoverageIndexEntry 单地址覆盖文件（设计 §40）。
type CoverageIndexEntry struct {
	SchemaVersion int                         `json:"schema_version"`
	ChainID       int64                       `json:"chain_id"`
	ChainKey      string                      `json:"chain_key"`
	Address       string                      `json:"address"`
	Datasets      map[string]*DatasetCoverage `json:"datasets"`
	UpdatedAt     time.Time                   `json:"updated_at"`
}

// CoverageResult 区间查询结果（设计 §45）。
type CoverageResult struct {
	CoverageRatio float64    `json:"coverage_ratio"`
	FullHit       bool       `json:"full_hit"`
	Covered       []Interval `json:"covered"`
	Missing       []Interval `json:"missing"`
	Certification string     `json:"certification"`
	Compatible    bool       `json:"compatible"`
}

// RebuildInput Registry 重建输入（来自 Result Processor 条目）。
type RebuildInput struct {
	ChainKey  string
	ChainID   int64
	Address   string
	Dataset   string
	FromBlock uint64
	ToBlock   uint64
	Rows      int64
	Certified bool
	Snapshot  *SnapshotCoverage
	UpdatedAt time.Time
}

// Store Coverage Index V2 分片存储（设计 §38-§40/§48）。
type Store struct {
	mu               sync.Mutex
	root             string
	compatibilityKey string
	cache            map[string]*cacheEntry
	cacheMax         int
	cacheSeq         int64
	OnUpdate         func(chainKey, address, dataset string)
}

type cacheEntry struct {
	entry    *CoverageIndexEntry
	lastUsed int64
}

// NewStore 创建分片覆盖索引。
func NewStore(root, compatibilityKey string) *Store {
	if compatibilityKey == "" {
		compatibilityKey = "1"
	}
	return &Store{
		root:             root,
		compatibilityKey: compatibilityKey,
		cache:            map[string]*cacheEntry{},
		cacheMax:         50_000,
	}
}

// CompatibilityKey 返回当前兼容键（dataset:chain:schema:normalizer）。
func (s *Store) CompatibilityKey() string { return s.compatibilityKey }

func (s *Store) cacheKey(chainKey, address string) string {
	return strings.ToLower(chainKey + "|" + address)
}

func (s *Store) path(chainKey, address string) string {
	shard := "00"
	if len(address) > 4 {
		shard = strings.ToLower(address)[2:4]
	}
	return filepath.Join(s.root, "smart_download", "registry", "coverage",
		strings.ToLower(chainKey), shard, strings.ToLower(address)+".json")
}

// Load 读取（带热缓存）。
func (s *Store) Load(chainKey, address string) *CoverageIndexEntry {
	key := s.cacheKey(chainKey, address)
	s.mu.Lock()
	if c, ok := s.cache[key]; ok {
		s.cacheSeq++
		c.lastUsed = s.cacheSeq
		s.mu.Unlock()
		return c.entry
	}
	s.mu.Unlock()
	payload, err := os.ReadFile(s.path(chainKey, address))
	if err != nil {
		return nil
	}
	var entry CoverageIndexEntry
	if json.Unmarshal(payload, &entry) != nil {
		return nil
	}
	s.cachePut(key, &entry)
	return &entry
}

func (s *Store) cachePut(key string, entry *CoverageIndexEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cacheSeq++
	s.cache[key] = &cacheEntry{entry: entry, lastUsed: s.cacheSeq}
	if len(s.cache) > s.cacheMax {
		// 简单淘汰最旧
		var oldestKey string
		var oldest int64 = 1 << 62
		for k, c := range s.cache {
			if c.lastUsed < oldest {
				oldest = c.lastUsed
				oldestKey = k
			}
		}
		if oldestKey != "" {
			delete(s.cache, oldestKey)
		}
	}
}

// AddCertified 追加/合并认证覆盖并写入分片（设计 §8/§41）。
func (s *Store) AddCertified(chainKey string, chainID int64, address, dataset string,
	ranges []Interval, rows int64, snapshot *SnapshotCoverage) error {
	key := s.cacheKey(chainKey, address)
	entry := s.Load(chainKey, address)
	if entry == nil {
		entry = &CoverageIndexEntry{
			SchemaVersion: 2, ChainID: chainID, ChainKey: strings.ToLower(chainKey),
			Address: strings.ToLower(address), Datasets: map[string]*DatasetCoverage{},
		}
	}
	now := time.Now().UTC()
	dc := entry.Datasets[dataset]
	if dc == nil {
		dc = &DatasetCoverage{CompatibilityKey: s.compatibilityKey}
		entry.Datasets[dataset] = dc
	}
	dc.CompatibilityKey = s.compatibilityKey
	dc.UpdatedAt = now
	if snapshot != nil {
		dc.Snapshot = snapshot
		dc.CertifiedRanges = nil
	} else if len(ranges) > 0 {
		dc.CertifiedRanges = mergeRanges(append(intervalsFrom(dc.CertifiedRanges), ranges...))
	}
	entry.UpdatedAt = now
	if err := s.save(entry); err != nil {
		return err
	}
	s.cachePut(key, entry)
	if s.OnUpdate != nil {
		s.OnUpdate(entry.ChainKey, entry.Address, dataset)
	}
	return nil
}

func (s *Store) save(entry *CoverageIndexEntry) error {
	payload, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	path := s.path(entry.ChainKey, entry.Address)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Resolve 区间覆盖查询（设计 §9/§45）：返回 covered/missing/ratio/full_hit。
func (s *Store) Resolve(chainKey, address, dataset string, from, to uint64, now time.Time) CoverageResult {
	res := CoverageResult{CoverageRatio: 0, Certification: "UNVALIDATED"}
	if to < from {
		return res
	}
	entry := s.Load(chainKey, address)
	if entry == nil {
		res.Missing = []Interval{{From: from, To: to}}
		return res
	}
	dc := entry.Datasets[dataset]
	if dc == nil {
		res.Missing = []Interval{{From: from, To: to}}
		return res
	}
	if dc.CompatibilityKey != s.compatibilityKey {
		res.Compatible = false
		res.Missing = []Interval{{From: from, To: to}}
		res.Certification = "INCOMPATIBLE"
		return res
	}
	res.Compatible = true
	res.Certification = "CERTIFIED"
	if dc.Snapshot != nil {
		// 快照类：TTL 内命中，否则过期需刷新（设计 §6/§61）
		if dc.Snapshot.Fresh(now) {
			res.Covered = []Interval{{From: from, To: to}}
			res.FullHit = true
			res.CoverageRatio = 1
		} else {
			res.Certification = "STALE"
			res.Missing = []Interval{{From: from, To: to}}
		}
		return res
	}
	covered := intersectRanges(intervalsFrom(dc.CertifiedRanges), from, to)
	res.Covered = covered
	res.Missing = subtract(Interval{From: from, To: to}, covered)
	res.FullHit = len(res.Missing) == 0
	total := to - from + 1
	if total > 0 {
		var c uint64
		for _, iv := range covered {
			c += iv.To - iv.From + 1
		}
		res.CoverageRatio = float64(c) / float64(total)
	}
	return res
}

// Rebuild 启动恢复：从结果条目重建索引（设计 §42）。
func (s *Store) Rebuild(inputs []RebuildInput) {
	now := time.Now().UTC()
	for _, in := range inputs {
		if !in.Certified {
			continue
		}
		_ = s.AddCertified(in.ChainKey, in.ChainID, in.Address, in.Dataset,
			[]Interval{{From: in.FromBlock, To: in.ToBlock}}, in.Rows, in.Snapshot)
	}
	_ = now
}

func intervalsFrom(list []CertifiedRange) []Interval {
	out := make([]Interval, 0, len(list))
	for _, r := range list {
		out = append(out, Interval{From: r.FromBlock, To: r.ToBlock})
	}
	return out
}

func mergeRanges(list []Interval) []CertifiedRange {
	if len(list) == 0 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].From < list[j].From })
	out := []Interval{list[0]}
	for _, iv := range list[1:] {
		last := &out[len(out)-1]
		if iv.From <= last.To+1 {
			if iv.To > last.To {
				last.To = iv.To
			}
			continue
		}
		out = append(out, iv)
	}
	cr := make([]CertifiedRange, 0, len(out))
	for _, iv := range out {
		cr = append(cr, CertifiedRange{FromBlock: iv.From, ToBlock: iv.To})
	}
	return cr
}

func intersectRanges(list []Interval, from, to uint64) []Interval {
	var out []Interval
	for _, iv := range list {
		lo, hi := iv.From, iv.To
		if from > lo {
			lo = from
		}
		if to < hi {
			hi = to
		}
		if hi >= lo {
			out = append(out, Interval{From: lo, To: hi})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From < out[j].From })
	return out
}

func subtract(requested Interval, covered []Interval) []Interval {
	var gaps []Interval
	cur := requested.From
	for _, c := range covered {
		if c.To < cur {
			continue
		}
		if c.From > cur {
			end := c.From - 1
			if end > requested.To {
				end = requested.To
			}
			if end >= cur {
				gaps = append(gaps, Interval{From: cur, To: end})
			}
			if c.From > requested.To {
				cur = requested.To + 1
				break
			}
		}
		if c.To >= cur {
			cur = c.To + 1
			if cur > requested.To {
				break
			}
		}
	}
	if cur <= requested.To {
		gaps = append(gaps, Interval{From: cur, To: requested.To})
	}
	return gaps
}
