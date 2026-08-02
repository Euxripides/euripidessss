package intelligence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── Legacy 迁移测试（V1 Storage Layer）──

// writeLegacyRequest 写旧格式请求文件（raw JSON，无 envelope）。
func writeLegacyRequest(t *testing.T, dir, id string) {
	t.Helper()
	req := &InvestigationRequest{ID: id, Address: testAddress, Objective: "旧请求", Mode: ModeFundTrace, Status: RequestStarted}
	data, _ := json.Marshal(req)
	if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0644); err != nil {
		t.Fatalf("写旧请求: %v", err)
	}
}

func TestMigrateLegacyRequests(t *testing.T) {
	dataRoot := t.TempDir()
	srcDir := filepath.Join(dataRoot, "investigation_requests")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeLegacyRequest(t, srcDir, "req-1")
	writeLegacyRequest(t, srcDir, "req-2")

	migrated, err := MigrateLegacyInvestigationData(dataRoot)
	if err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if migrated != 2 {
		t.Fatalf("应迁移 2 条请求, got %d", migrated)
	}
	// 新布局可读
	store := NewRequestStore(filepath.Join(dataRoot, "investigation", "requests"))
	if !store.Exists("req-1") || !store.Exists("req-2") {
		t.Fatal("迁移后请求不可读")
	}
	got, ok := store.Get("req-1")
	if !ok || got.Objective != "旧请求" || got.Status != RequestStarted {
		t.Fatalf("迁移内容不一致: %+v", got)
	}
	// 幂等：再次迁移不重复
	migrated2, _ := MigrateLegacyInvestigationData(dataRoot)
	if migrated2 != 0 {
		t.Fatalf("幂等迁移应跳过, got %d", migrated2)
	}
}

func TestMigrateLegacyEvidence(t *testing.T) {
	dataRoot := t.TempDir()
	srcDir := filepath.Join(dataRoot, "investigation_evidence")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	evs := []Evidence{
		{ID: "ev-1", Type: EvTransaction, Address: testAddress, TxHash: "0xold", Detail: "旧证据", Confidence: 0.8},
		{ID: "ev-2", Type: EvRisk, Detail: "风险", Confidence: 0.7},
	}
	data, _ := json.Marshal(evs)
	if err := os.WriteFile(filepath.Join(srcDir, "inv-1.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	migrated, err := MigrateLegacyInvestigationData(dataRoot)
	if err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if migrated != 2 {
		t.Fatalf("应迁移 2 条证据, got %d", migrated)
	}
	store := NewEvidenceStore(filepath.Join(dataRoot, "investigation", "evidence"))
	list := store.List("inv-1")
	if len(list) != 2 {
		t.Fatalf("迁移后证据 = %d 条, want 2", len(list))
	}
	if list[0].Detail != "旧证据" || list[0].InvestigationID != "inv-1" {
		t.Fatalf("迁移内容不一致: %+v", list[0])
	}
	// 索引已重建
	if got := store.IndexByAddress(testAddress); len(got) != 1 || got[0] != "ev-1" {
		t.Fatalf("迁移后索引 = %v", got)
	}
}

func TestMigrateLegacyMemory(t *testing.T) {
	dataRoot := t.TempDir()
	memDir := filepath.Join(dataRoot, "investigation_memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}
	rels := []MemoryRelation{
		{ID: "rel-1", Type: RelCaseAddress, From: "inv-1", To: testAddress, InvestigationID: "inv-1", CreatedAt: time.Now().UTC()},
		{ID: "rel-2", Type: RelAddressEntity, From: testAddress, To: "exchange", InvestigationID: "inv-1", CreatedAt: time.Now().UTC()},
		{ID: "rel-3", Type: RelAddressLink, From: testAddress, To: "0x00000000000000000000000000000000000000b1", InvestigationID: "inv-1", CreatedAt: time.Now().UTC()},
	}
	data, _ := json.Marshal(rels)
	if err := os.WriteFile(filepath.Join(memDir, "knowledge.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	migrated, err := MigrateLegacyInvestigationData(dataRoot)
	if err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if migrated != 3 {
		t.Fatalf("应迁移 3 条关系, got %d", migrated)
	}
	store := NewInvestigationMemoryStore(filepath.Join(dataRoot, "investigation", "memory"))
	got := store.Search(testAddress)
	if len(got) != 3 {
		t.Fatalf("迁移后 Search = %d 条, want 3: %+v", len(got), got)
	}
	// 分目录文件存在
	for _, f := range []string{
		filepath.Join("memory", "address", testAddress+".json"),
		filepath.Join("memory", "entity", "exchange.json"),
		filepath.Join("memory", "case", "inv-1.json"),
	} {
		if _, err := os.Stat(filepath.Join(dataRoot, "investigation", f)); err != nil {
			t.Fatalf("分目录文件缺失 %s: %v", f, err)
		}
	}
	// 幂等：knowledge.json 中的 from/to 改为大写/带空格重跑，
	// normKey 规范化后应全部命中已有记录（migrated == 0）
	rels[0].To = strings.ToUpper(testAddress) + " "
	rels[1].From = strings.ToUpper(testAddress) + " "
	data2, _ := json.Marshal(rels)
	if err := os.WriteFile(filepath.Join(memDir, "knowledge.json"), data2, 0644); err != nil {
		t.Fatal(err)
	}
	migrated2, err := MigrateLegacyInvestigationData(dataRoot)
	if err != nil {
		t.Fatalf("二次迁移失败: %v", err)
	}
	if migrated2 != 0 {
		t.Fatalf("幂等迁移应跳过（规范化后命中）, got %d", migrated2)
	}
}

func TestMigrateLegacyNoOldData(t *testing.T) {
	dataRoot := t.TempDir() // 空目录：无旧数据
	migrated, err := MigrateLegacyInvestigationData(dataRoot)
	if err != nil {
		t.Fatalf("空目录迁移应无错误: %v", err)
	}
	if migrated != 0 {
		t.Fatalf("空目录应迁移 0, got %d", migrated)
	}
}

func TestMigrateLegacyEvidenceIDMismatchSkipped(t *testing.T) {
	dataRoot := t.TempDir()
	srcDir := filepath.Join(dataRoot, "investigation_evidence")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	// 无 ID 的证据应跳过
	evs := []Evidence{{Type: EvTransaction, Detail: "无 ID"}}
	data, _ := json.Marshal(evs)
	if err := os.WriteFile(filepath.Join(srcDir, "inv-1.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	migrated, err := MigrateLegacyInvestigationData(dataRoot)
	if err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if migrated != 0 {
		t.Fatalf("无 ID 证据应跳过, got %d", migrated)
	}
}
