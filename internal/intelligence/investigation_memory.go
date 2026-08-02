package intelligence

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/investigationstore"
)

// ── Investigation Memory（V1 设计 §9/§10 Knowledge Graph Memory）──
//
// 跨案件知识记忆：地址→实体归属、地址↔地址资金关联、案件→地址历史出现。
// 分目录 JSON 持久化（backend/data/investigation/memory/）：
//
//	memory/address/{addr}.json    地址记录（标签/案件/关系）
//	memory/entity/{entity}.json   实体记录（归属地址）
//	memory/case/{case}.json       案件记录（涉及地址）
//	indexes/memory-index.json     地址 → 关系 ID 索引
//
// 内存保留 master 关系列表（Search/All 快速读取），
// 每次 Record 增量落盘受影响的地址/实体/案件记录（原子写）。

// MemoryRelationType 是知识关系类型。
type MemoryRelationType string

const (
	RelAddressEntity MemoryRelationType = "ADDRESS_ENTITY" // 地址属于实体（exchange/bridge/...）
	RelAddressLink   MemoryRelationType = "ADDRESS_LINK"   // 地址资金关联（同路径相邻）
	RelCaseAddress   MemoryRelationType = "CASE_ADDRESS"   // 案件涉及地址（历史出现）
)

// MemoryRelation 是一条跨案件知识关系。
type MemoryRelation struct {
	ID              string             `json:"id"`
	Type            MemoryRelationType `json:"type"`
	From            string             `json:"from"` // 地址 / 案件 ID
	To              string             `json:"to"`   // 实体 / 地址 / 案件 ID
	Detail          string             `json:"detail,omitempty"`
	InvestigationID string             `json:"investigation_id,omitempty"` // 来源调查
	CreatedAt       time.Time          `json:"created_at"`
}

// InvestigationMemoryStore 管理跨案件知识记忆（原子持久化）。
type InvestigationMemoryStore struct {
	mu          sync.Mutex
	storeDir    string
	seq         int
	relations   []MemoryRelation // master：内存真源（Search/All 读取）
	addrStore   *investigationstore.JSONStore[investigationstore.MemoryAddressRecord]
	entityStore *investigationstore.JSONStore[investigationstore.MemoryEntityRecord]
	caseStore   *investigationstore.JSONStore[investigationstore.MemoryCaseRecord]
	index       *investigationstore.Index // memory-index：地址 → 关系 ID
}

// NewInvestigationMemoryStore 创建记忆存储。storeDir 为空则仅内存。
func NewInvestigationMemoryStore(storeDir string) *InvestigationMemoryStore {
	indexPath := ""
	if storeDir != "" {
		indexPath = filepath.Join(filepath.Dir(storeDir), "indexes", "memory-index.json")
	}
	s := &InvestigationMemoryStore{
		storeDir:    storeDir,
		seq:         1,
		addrStore:   investigationstore.NewJSONStore[investigationstore.MemoryAddressRecord](subDir(storeDir, "address")),
		entityStore: investigationstore.NewJSONStore[investigationstore.MemoryEntityRecord](subDir(storeDir, "entity")),
		caseStore:   investigationstore.NewJSONStore[investigationstore.MemoryCaseRecord](subDir(storeDir, "case")),
		index:       investigationstore.NewIndex(indexPath),
	}
	if storeDir != "" {
		s.load()
	}
	return s
}

// subDir 拼接子目录（storeDir 为空时返回空，保持仅内存模式）。
func subDir(storeDir, name string) string {
	if storeDir == "" {
		return ""
	}
	return filepath.Join(storeDir, name)
}

// Record 记录一条关系（type+from+to 幂等去重；InvestigationID 变化时更新来源）。
// 落盘：ADDRESS_ENTITY → 地址记录 + 实体记录；ADDRESS_LINK → 两端地址记录；
// CASE_ADDRESS → 地址记录 + 案件记录；并更新地址索引。
func (s *InvestigationMemoryStore) Record(rel MemoryRelation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rel.From = strings.ToLower(strings.TrimSpace(rel.From))
	rel.To = strings.ToLower(strings.TrimSpace(rel.To))

	// 安全：From/To 将作为存储 key（文件名），非法字符（"/"、".." 等）
	// 会导致落盘失败；先校验，非法关系直接拒绝（不入内存，保持内存/磁盘一致）
	if !investigationstore.ValidKey(rel.From) || !investigationstore.ValidKey(rel.To) {
		return fmt.Errorf("investigation_memory: 非法关系端点 %q / %q", rel.From, rel.To)
	}

	// 幂等去重：已存在则更新来源（保留原始创建时间，created_at 语义为首次记录时间）
	for i := range s.relations {
		r := &s.relations[i]
		if r.Type == rel.Type && r.From == rel.From && r.To == rel.To {
			r.InvestigationID = rel.InvestigationID
			return s.persistAffectedLocked(&s.relations[i])
		}
	}
	// 新关系
	rel.ID = "rel-" + strconv.Itoa(s.seq)
	s.seq++
	if rel.CreatedAt.IsZero() {
		rel.CreatedAt = time.Now().UTC()
	}
	s.relations = append(s.relations, rel)
	// 落盘失败回滚内存（seq 回退 + 移除刚追加的关系），避免内存/磁盘不一致
	if err := s.persistAffectedLocked(&s.relations[len(s.relations)-1]); err != nil {
		s.relations = s.relations[:len(s.relations)-1]
		s.seq--
		return err
	}
	return nil
}

// persistAffectedLocked 增量落盘受影响记录（地址/实体/案件 + 索引），持锁调用。
func (s *InvestigationMemoryStore) persistAffectedLocked(rel *MemoryRelation) error {
	if s.storeDir == "" {
		return nil // 仅内存模式
	}
	switch rel.Type {
	case RelAddressEntity:
		// 地址 → 实体：更新地址记录 + 实体记录
		if err := s.saveAddrRecordLocked(rel.From); err != nil {
			return err
		}
		if err := s.saveEntityRecordLocked(rel.To); err != nil {
			return err
		}
		return s.index.Add(rel.From, rel.ID)
	case RelAddressLink:
		// 地址 ↔ 地址：更新两端地址记录
		if err := s.saveAddrRecordLocked(rel.From); err != nil {
			return err
		}
		if err := s.saveAddrRecordLocked(rel.To); err != nil {
			return err
		}
		if err := s.index.Add(rel.From, rel.ID); err != nil {
			return err
		}
		return s.index.Add(rel.To, rel.ID)
	case RelCaseAddress:
		// 案件 → 地址：先写地址记录（重启真源），再写案件记录（派生视图）。
		// 崩溃在两写之间时，地址记录（含关系）不丢，案件记录可重建。
		if err := s.saveAddrRecordLocked(rel.To); err != nil {
			return err
		}
		if err := s.saveCaseRecordLocked(rel.From); err != nil {
			return err
		}
		return s.index.Add(rel.To, rel.ID)
	}
	return nil
}

// saveAddrRecordLocked 重建并保存地址记录（该地址相关的全部关系 + 案件）。
func (s *InvestigationMemoryStore) saveAddrRecordLocked(addr string) error {
	rec := investigationstore.MemoryAddressRecord{Address: addr}
	seen := map[string]bool{}
	for i := range s.relations {
		r := &s.relations[i]
		if r.From != addr && r.To != addr {
			continue
		}
		rec.Relations = append(rec.Relations, toRelationRecord(*r))
		if r.Type == RelCaseAddress && r.To == addr && !seen[r.From] {
			seen[r.From] = true
			rec.Cases = append(rec.Cases, r.From)
		}
	}
	return s.addrStore.Save(addr, rec)
}

// saveEntityRecordLocked 重建并保存实体记录（归属该实体的地址列表）。
func (s *InvestigationMemoryStore) saveEntityRecordLocked(entity string) error {
	rec := investigationstore.MemoryEntityRecord{Entity: entity}
	seen := map[string]bool{}
	for i := range s.relations {
		r := &s.relations[i]
		if r.Type == RelAddressEntity && r.To == entity && !seen[r.From] {
			seen[r.From] = true
			rec.Addresses = append(rec.Addresses, r.From)
		}
	}
	return s.entityStore.Save(entity, rec)
}

// saveCaseRecordLocked 重建并保存案件记录（涉及地址列表）。
func (s *InvestigationMemoryStore) saveCaseRecordLocked(caseID string) error {
	rec := investigationstore.MemoryCaseRecord{CaseID: caseID}
	seen := map[string]bool{}
	for i := range s.relations {
		r := &s.relations[i]
		if r.Type == RelCaseAddress && r.From == caseID && !seen[r.To] {
			seen[r.To] = true
			rec.Addresses = append(rec.Addresses, r.To)
		}
	}
	return s.caseStore.Save(caseID, rec)
}

// Search 返回与地址相关的全部历史关系（地址/实体/案件视角）。
func (s *InvestigationMemoryStore) Search(address string) []MemoryRelation {
	s.mu.Lock()
	defer s.mu.Unlock()
	address = strings.ToLower(strings.TrimSpace(address))
	var out []MemoryRelation
	for _, r := range s.relations {
		if r.From == address || r.To == address {
			out = append(out, r)
		}
	}
	return out
}

// All 返回全部关系（供审计/调试）。
func (s *InvestigationMemoryStore) All() []MemoryRelation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]MemoryRelation(nil), s.relations...)
}

// load 启动时从地址记录重建 master 关系列表并推进序号。
func (s *InvestigationMemoryStore) load() {
	seen := map[string]bool{}
	for _, key := range s.addrStore.Keys() {
		rec, ok := s.addrStore.Get(key)
		if !ok {
			continue
		}
		for _, rr := range rec.Relations {
			rel := fromRelationRecord(rr)
			if rel.ID == "" || seen[rel.ID] {
				continue
			}
			seen[rel.ID] = true
			s.relations = append(s.relations, rel)
			if len(rel.ID) > 4 && rel.ID[:4] == "rel-" {
				if n, err := strconv.Atoi(rel.ID[4:]); err == nil && n >= s.seq {
					s.seq = n + 1
				}
			}
		}
	}
}

// UpdatedAt 已移除：原实现修改的是 CreatedAt 且语义误导。
// created_at 语义固定为首次记录时间，去重更新不再改写。

// toRelationRecord 业务关系 → 存储记录。
func toRelationRecord(r MemoryRelation) investigationstore.RelationRecord {
	return investigationstore.RelationRecord{
		ID:              r.ID,
		Type:            string(r.Type),
		From:            r.From,
		To:              r.To,
		Detail:          r.Detail,
		InvestigationID: r.InvestigationID,
		CreatedAt:       r.CreatedAt,
	}
}

// fromRelationRecord 存储记录 → 业务关系。
func fromRelationRecord(r investigationstore.RelationRecord) MemoryRelation {
	return MemoryRelation{
		ID:              r.ID,
		Type:            MemoryRelationType(r.Type),
		From:            r.From,
		To:              r.To,
		Detail:          r.Detail,
		InvestigationID: r.InvestigationID,
		CreatedAt:       r.CreatedAt,
	}
}

// recordKnowledge 从调查结果提取知识关系并写入记忆（V2.1 §9/§10）：
// - 案件→地址（CASE_ADDRESS）
// - 地址→实体（ADDRESS_ENTITY，实体已知时）
// - 地址↔地址资金关联（ADDRESS_LINK，路径相邻节点）
func (s *InvestigationMemoryStore) recordKnowledge(inv *Investigation) {
	if inv == nil || s == nil {
		return
	}
	// 案件→地址
	_ = s.Record(MemoryRelation{
		Type: RelCaseAddress, From: inv.ID, To: inv.Target,
		Detail: "案件涉及地址", InvestigationID: inv.ID,
	})
	// 地址→实体
	seenEntity := map[string]bool{}
	for _, ent := range inv.Entities {
		e := strings.ToLower(strings.TrimSpace(ent.Entity))
		if e == "" || e == "unknown" || seenEntity[ent.Address+"|"+e] {
			continue
		}
		seenEntity[ent.Address+"|"+e] = true
		_ = s.Record(MemoryRelation{
			Type: RelAddressEntity, From: ent.Address, To: e,
			Detail: "地址实体归属", InvestigationID: inv.ID,
		})
	}
	// 地址关联（路径相邻节点）
	seenLink := map[string]bool{}
	for _, p := range inv.Paths {
		nodes := p.Path.Nodes
		for i := 0; i+1 < len(nodes); i++ {
			key := nodes[i] + "|" + nodes[i+1]
			if seenLink[key] {
				continue
			}
			seenLink[key] = true
			_ = s.Record(MemoryRelation{
				Type: RelAddressLink, From: nodes[i], To: nodes[i+1],
				Detail: "资金路径关联", InvestigationID: inv.ID,
			})
		}
	}
}
