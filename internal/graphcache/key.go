// Package graphcache 实现 Graph Expansion Cache V1（设计 V1.0 §7-§10、§41-§44）：
// 地址 × 方向 × Token × 时间范围 × 深度 的图扩展聚合结果缓存，
// 关系图展开优先命中该缓存，避免每次从几百万行重新聚合。
package graphcache

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"
	"strings"
)

// Direction 图扩展方向。
type Direction string

const (
	DirectionAll Direction = "ALL"
	DirectionIn  Direction = "IN"
	DirectionOut Direction = "OUT"
)

// Key 是图扩展缓存键（设计 §8）。
type Key struct {
	ChainID            int64    `json:"chain_id"`
	Address            string   `json:"address"`
	Direction          string   `json:"direction"`
	DatasetSet         []string `json:"dataset_set,omitempty"`
	TokenFilter        string   `json:"token_filter,omitempty"`
	FromBlock          uint64   `json:"from_block"`
	ToBlock            uint64   `json:"to_block"`
	Depth              int      `json:"depth"`
	AggregationVersion int      `json:"aggregation_version"`
}

// Normalized 返回规范化的 Key（地址/Token 小写、Dataset 排序去重）。
func (k Key) Normalized() Key {
	out := k
	out.Address = strings.ToLower(strings.TrimSpace(k.Address))
	out.TokenFilter = strings.ToLower(strings.TrimSpace(k.TokenFilter))
	out.Direction = strings.ToUpper(strings.TrimSpace(k.Direction))
	if out.Direction == "" {
		out.Direction = string(DirectionAll)
	}
	seen := map[string]bool{}
	datasets := make([]string, 0, len(k.DatasetSet))
	for _, d := range k.DatasetSet {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		datasets = append(datasets, d)
	}
	sort.Strings(datasets)
	out.DatasetSet = datasets
	return out
}

// Hash 返回稳定缓存键（sha256 hex，前 16 位用于文件命名）。
func (k Key) Hash() string {
	k = k.Normalized()
	h := sha256.Sum256([]byte(k.String()))
	return hex.EncodeToString(h[:])
}

// String 返回键的规范化文本。
func (k Key) String() string {
	k = k.Normalized()
	return strings.Join([]string{
		itoa(k.ChainID), k.Address, k.Direction,
		strings.Join(k.DatasetSet, ","), k.TokenFilter,
		itoa64(k.FromBlock), itoa64(k.ToBlock), itoa(int64(k.Depth)),
		itoa(int64(k.AggregationVersion)),
	}, "|")
}

// FilePath 返回缓存文件路径：{root}/{chain}/{shard}/{address}/{hash}.json。
func (k Key) FilePath(root string) string {
	k = k.Normalized()
	shard := "00"
	if len(k.Address) > 4 {
		shard = k.Address[2:4]
	}
	return filepath.Join(root, itoa(k.ChainID), shard, k.Address, k.Hash()+".json")
}

func itoa(v int64) string {
	return formatInt(v)
}

func itoa64(v uint64) string {
	return formatUint(v)
}
