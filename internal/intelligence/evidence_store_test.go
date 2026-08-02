package intelligence

import (
	"path/filepath"
	"testing"
)

func TestEvidenceStoreAddAndList(t *testing.T) {
	s := NewEvidenceStore("")
	evs := []Evidence{
		{Type: EvTransaction, TxHash: "0xabc", BlockNumber: 100, Token: "USDT", Amount: "1000", Detail: "流入", Confidence: 0.9},
		{Type: EvAddress, Address: testAddress, Detail: "交易所", Confidence: 0.8},
	}
	if err := s.Add("inv-1", evs...); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	list := s.List("inv-1")
	if len(list) != 2 {
		t.Fatalf("应 2 条证据, got %d", len(list))
	}
	if list[0].ID == "" || list[0].InvestigationID != "inv-1" {
		t.Fatalf("证据应带 ID 与调查关联: %+v", list[0])
	}
	if list[0].ID == list[1].ID {
		t.Fatal("证据 ID 不应重复")
	}
	if len(s.List("inv-none")) != 0 {
		t.Fatal("无证据调查应返回空")
	}
}

func TestEvidenceStoreDeleteIndexConsistency(t *testing.T) {
	dir := t.TempDir()
	s := NewEvidenceStore(dir)
	ev := Evidence{Type: EvTransaction, Address: testAddress, Detail: "删除索引一致性", Confidence: 0.9}
	if err := s.Add("inv-1", ev); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := s.IndexByAddress(testAddress); len(got) != 1 {
		t.Fatalf("Add 后索引 = %v", got)
	}
	// 删除证据，索引应同步移除（key 是地址，与 Add 一致）
	if err := s.Delete("inv-1/" + s.List("inv-1")[0].ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := s.IndexByAddress(testAddress); len(got) != 0 {
		t.Fatalf("Delete 后索引应清空, got %v", got)
	}
	if s.Exists("inv-1/" + "ev-1") {
		t.Fatal("证据应已删除")
	}
}

func TestEvidenceStorePersistReload(t *testing.T) {
	dir := t.TempDir()
	s := NewEvidenceStore(dir)
	if err := s.Add("inv-1", Evidence{Type: EvRisk, Detail: "快速转移", Confidence: 0.7}); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	// 重载
	s2 := NewEvidenceStore(dir)
	list := s2.List("inv-1")
	if len(list) != 1 || list[0].Detail != "快速转移" {
		t.Fatalf("重载后证据丢失: %+v", list)
	}
	// ID 自增推进，避免重启覆盖
	if err := s2.Add("inv-1", Evidence{Type: EvPath, Detail: "路径证据"}); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	l2 := s2.List("inv-1")
	if l2[0].ID == l2[1].ID {
		t.Fatalf("重启后证据 ID 不应重复: %s", l2[0].ID)
	}
	if _, err := filepath.Glob(filepath.Join(dir, "*.tmp")); err != nil {
		t.Fatalf("glob 失败: %v", err)
	}
}
