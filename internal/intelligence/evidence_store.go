package intelligence

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/investigationstore"
)

// ── Evidence Store（V1 设计 §8/§12）──
//
// 调查证据的 JSON 文件持久化（backend/data/investigation/evidence/）。
// 每条证据一个文件（evidence/{inv}/{ev-id}.json），避免单文件无限增长；
// 索引 indexes/evidence-index.json（地址 → 证据 ID）用于快速定位；
// 原子写（tmp + fsync + rename）+ schema_version + 加载时 ID/关联校验。

// EvidenceStore 管理调查证据的读写。
type EvidenceStore struct {
	mu    sync.Mutex // 保护 seq（ID 生成）
	store *investigationstore.JSONStore[Evidence]
	index *investigationstore.Index // evidence-index：地址 → 证据 ID 列表
	seq   int
}

// NewEvidenceStore 创建证据存储。storeDir 为空则仅内存（测试用）。
// 索引路径自动推导：storeDir 同级 indexes/evidence-index.json。
func NewEvidenceStore(storeDir string) *EvidenceStore {
	indexPath := ""
	if storeDir != "" {
		indexPath = filepath.Join(filepath.Dir(storeDir), "indexes", "evidence-index.json")
	}
	s := &EvidenceStore{
		store: investigationstore.NewJSONStore(
			storeDir,
			investigationstore.WithValidate(func(key string, v *Evidence) bool {
				// key 格式 inv-1/ev-1：校验 ID 与调查关联（磁盘篡改防护）
				if v == nil || v.ID == "" {
					return false
				}
				parts := strings.Split(key, "/")
				if len(parts) != 2 {
					return false
				}
				return v.ID == parts[1] && v.InvestigationID == parts[0]
			}),
		),
		index: investigationstore.NewIndex(indexPath),
		seq:   1,
	}
	// 推进证据序号（ev-N 解析）
	for _, ev := range s.store.List() {
		if len(ev.ID) > 3 && ev.ID[:3] == "ev-" {
			if n, err := strconv.Atoi(ev.ID[3:]); err == nil && n >= s.seq {
				s.seq = n + 1
			}
		}
	}
	// 索引自愈：从数据重建（启动时保证索引与数据一致）
	s.rebuildIndex()
	return s
}

// Add 追加证据（按 investigation_id 分组，每条一个文件，持久化失败返回错误）。
func (s *EvidenceStore) Add(investigationID string, evs ...Evidence) error {
	if len(evs) == 0 {
		return nil
	}
	s.mu.Lock()
	now := time.Now().UTC()
	keyed := make([]Evidence, 0, len(evs))
	for i := range evs {
		evs[i].ID = "ev-" + strconv.Itoa(s.seq)
		s.seq++
		evs[i].InvestigationID = investigationID
		if evs[i].CreatedAt.IsZero() {
			evs[i].CreatedAt = now
		}
		keyed = append(keyed, evs[i])
	}
	s.mu.Unlock()

	for i := range keyed {
		key := investigationID + "/" + keyed[i].ID
		if err := s.store.Save(key, keyed[i]); err != nil {
			return err
		}
		if keyed[i].Address != "" {
			_ = s.index.Add(keyed[i].Address, keyed[i].ID)
		}
	}
	return nil
}

// List 返回调查的全部证据（按创建时间排序，防御性深拷贝）。
func (s *EvidenceStore) List(investigationID string) []Evidence {
	prefix := investigationID + "/"
	var out []Evidence
	for _, key := range s.store.Keys() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if ev, ok := s.store.Get(key); ok {
			out = append(out, ev)
		}
	}
	// 稳定排序：旧→新
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Delete 删除单条证据（存储接口 Store[T] 语义，key = inv/ev-id）。
func (s *EvidenceStore) Delete(key string) error {
	// 先查证据的地址再删索引（索引 key 是地址，与 Add 保持一致）
	if ev, ok := s.store.Get(key); ok && ev.Address != "" {
		_ = s.index.Remove(ev.Address, ev.ID)
	}
	return s.store.Delete(key)
}

// Exists 判断证据是否存在（key = inv/ev-id）。
func (s *EvidenceStore) Exists(key string) bool {
	return s.store.Exists(key)
}

// IndexByAddress 返回地址关联的证据 ID 列表（V1 设计 §12 快速定位）。
func (s *EvidenceStore) IndexByAddress(address string) []string {
	return s.index.Get(address)
}

// rebuildIndex 从数据重建地址索引（自愈：索引文件丢失/损坏时恢复）。
func (s *EvidenceStore) rebuildIndex() {
	entries := map[string][]string{}
	for _, key := range s.store.Keys() {
		parts := strings.Split(key, "/")
		if len(parts) != 2 {
			continue
		}
		ev, ok := s.store.Get(key)
		if !ok || ev.Address == "" {
			continue
		}
		entries[ev.Address] = append(entries[ev.Address], ev.ID)
	}
	if len(entries) > 0 {
		_ = s.index.Bulk(entries)
	}
}
