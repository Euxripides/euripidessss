package reportengine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// EvidenceIndex 是证据引用层（设计 §8、§11）。
type EvidenceIndex struct {
	items map[string]*EvidenceRef
	order []string
}

// NewEvidenceIndex 创建证据索引。
func NewEvidenceIndex() *EvidenceIndex {
	return &EvidenceIndex{items: map[string]*EvidenceRef{}}
}

// Add 添加证据（重复 ID 跳过）。
func (ix *EvidenceIndex) Add(ev *EvidenceRef) {
	if ev == nil || ev.ID == "" {
		return
	}
	if _, ok := ix.items[ev.ID]; ok {
		return
	}
	if ev.EvidenceHash == "" {
		ev.EvidenceHash = EvidenceHash(ev)
	}
	ix.items[ev.ID] = ev
	ix.order = append(ix.order, ev.ID)
}

// Get 按 ID 获取证据。
func (ix *EvidenceIndex) Get(id string) *EvidenceRef {
	return ix.items[id]
}

// List 返回全部证据。
func (ix *EvidenceIndex) List() []EvidenceRef {
	out := make([]EvidenceRef, 0, len(ix.order))
	for _, id := range ix.order {
		out = append(out, *ix.items[id])
	}
	return out
}

// Merge 合并另一个索引。
func (ix *EvidenceIndex) Merge(other *EvidenceIndex) {
	if other == nil {
		return
	}
	for _, ev := range other.List() {
		ix.Add(&ev)
	}
}

// EvidenceHash 基于 Canonical Record 生成 SHA256（设计 §11）。
func EvidenceHash(ev *EvidenceRef) string {
	canonical, _ := json.Marshal(map[string]any{
		"type": ev.Type, "chain_id": ev.ChainID, "address": strings.ToLower(ev.Address),
		"tx_hash": strings.ToLower(ev.TxHash), "dataset_id": ev.DatasetID,
		"block_number": ev.BlockNumber, "source_path": ev.SourcePath,
		"provider": ev.SourceProvider, "certification": ev.Certification,
	})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// ManifestHash 对证据清单生成清单哈希（Snapshot 用）。
func ManifestHash(items []EvidenceRef) string {
	h := sha256.New()
	for _, ev := range items {
		_, _ = fmt.Fprintf(h, "%s|%s\n", ev.ID, ev.EvidenceHash)
	}
	return hex.EncodeToString(h.Sum(nil))
}

