package intelligence

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInvestigationMemoryStoreRecordAndSearch(t *testing.T) {
	s := NewInvestigationMemoryStore("")
	// 记录三类关系
	_ = s.Record(MemoryRelation{Type: RelCaseAddress, From: "inv-1", To: vTarget, InvestigationID: "inv-1"})
	_ = s.Record(MemoryRelation{Type: RelAddressEntity, From: vTarget, To: "exchange", InvestigationID: "inv-1"})
	_ = s.Record(MemoryRelation{Type: RelAddressLink, From: vTarget, To: vOut, InvestigationID: "inv-1"})
	// 幂等：重复记录不新增
	_ = s.Record(MemoryRelation{Type: RelAddressEntity, From: vTarget, To: "exchange", InvestigationID: "inv-2"})
	if len(s.All()) != 3 {
		t.Fatalf("应 3 条唯一关系, got %d", len(s.All()))
	}
	// 搜索：地址相关关系
	rels := s.Search(vTarget)
	if len(rels) != 3 {
		t.Fatalf("搜索应命中 3 条, got %d", len(rels))
	}
	rels2 := s.Search(vOut)
	if len(rels2) != 1 || rels2[0].Type != RelAddressLink {
		t.Fatalf("vOut 应命中 1 条资金关联: %+v", rels2)
	}
	// 无关地址无命中
	if len(s.Search("0x0000000000000000000000000000000000000001")) != 0 {
		t.Fatal("无关地址不应命中")
	}
}

func TestMemoryDedupPreservesCreatedAt(t *testing.T) {
	s := NewInvestigationMemoryStore("")
	rel := MemoryRelation{Type: RelAddressEntity, From: vTarget, To: "exchange", InvestigationID: "inv-1", CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	_ = s.Record(rel)
	// 幂等去重更新：来源变化，但 created_at 应保留首次记录时间
	_ = s.Record(MemoryRelation{Type: RelAddressEntity, From: vTarget, To: "exchange", InvestigationID: "inv-9", CreatedAt: time.Now().UTC()})
	all := s.All()
	if len(all) != 1 {
		t.Fatalf("应 1 条关系, got %d", len(all))
	}
	if !all[0].CreatedAt.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("去重更新不应改写 created_at: %v", all[0].CreatedAt)
	}
	if all[0].InvestigationID != "inv-9" {
		t.Fatalf("来源应更新为 inv-9: %+v", all[0])
	}
}

func TestMemoryRecordRejectsInvalidKey(t *testing.T) {
	s := NewInvestigationMemoryStore(t.TempDir())
	// 非法端点（路径穿越字符）应被拒绝且不入内存
	err := s.Record(MemoryRelation{Type: RelAddressEntity, From: vTarget, To: "../evil", InvestigationID: "inv-1"})
	if err == nil {
		t.Fatal("非法端点应报错")
	}
	if len(s.All()) != 0 {
		t.Fatalf("非法关系不应入内存: %+v", s.All())
	}
	// 重载同一目录后也不应出现（未落盘）
	s2 := NewInvestigationMemoryStore(s.storeDir)
	if len(s2.All()) != 0 {
		t.Fatal("非法关系不应落盘")
	}
}

func TestMemoryRecordPersistFailureRollsBack(t *testing.T) {
	// 真实落盘失败：storeDir 指向一个文件（MkdirAll 必然失败）→ 触发 L117-122 回滚分支
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	s := NewInvestigationMemoryStore(blocked)
	if err := s.Record(MemoryRelation{Type: RelAddressEntity, From: vTarget, To: "exchange", InvestigationID: "inv-1"}); err == nil {
		t.Fatal("落盘失败应报错")
	}
	if len(s.All()) != 0 {
		t.Fatalf("回滚后内存应无关系: %+v", s.All())
	}
	if s.seq != 1 {
		t.Fatalf("回滚后 seq 应回退为 1, got %d", s.seq)
	}
}

func TestInvestigationMemoryStorePersist(t *testing.T) {
	dir := t.TempDir()
	s := NewInvestigationMemoryStore(dir)
	_ = s.Record(MemoryRelation{Type: RelAddressEntity, From: vTarget, To: "exchange", InvestigationID: "inv-1"})
	// 重载
	s2 := NewInvestigationMemoryStore(dir)
	rels := s2.Search(vTarget)
	if len(rels) != 1 || rels[0].To != "exchange" {
		t.Fatalf("重载后关系丢失: %+v", rels)
	}
	// 序号推进
	_ = s2.Record(MemoryRelation{Type: RelCaseAddress, From: "inv-2", To: vOut})
	if s2.All()[1].ID == s2.All()[0].ID {
		t.Fatal("重启后关系 ID 不应重复")
	}
}

func TestRecordKnowledge(t *testing.T) {
	s := NewInvestigationMemoryStore("")
	inv := &Investigation{
		ID:     "inv-1",
		Target: vTarget,
		Entities: []EntityInfo{
			{Address: vTarget, Entity: "exchange", Label: "Binance"},
			{Address: vOut, Entity: "wallet"},
		},
		Paths: []RankedPath{{Path: FundPath{Nodes: []string{vTarget, vOut, vIn}}}},
	}
	s.recordKnowledge(inv)
	rels := s.All()
	// case×1 + entity×2（exchange+wallet）+ link×2 = 5
	if len(rels) != 5 {
		t.Fatalf("应 5 条关系（case/entity×2/link×2）, got %d: %+v", len(rels), rels)
	}
	// 案件与实体关系存在
	foundCase := false
	foundEntity := false
	for _, r := range rels {
		if r.Type == RelCaseAddress && r.From == "inv-1" && r.To == vTarget {
			foundCase = true
		}
		if r.Type == RelAddressEntity && r.From == vTarget && r.To == "exchange" {
			foundEntity = true
		}
	}
	if !foundCase || !foundEntity {
		t.Fatalf("应包含案件与实体关系: %+v", rels)
	}
}
