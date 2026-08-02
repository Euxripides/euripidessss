package investigationstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── JSONStore 单元测试 ──

type testRecord struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestValidKey(t *testing.T) {
	valid := []string{"req-1", "inv-001", "ev-12", "0xabc", "a/b/c", "a_b.c-d"}
	invalid := []string{"", "../evil", "a/../b", "a//b", "/abs", "a b", "a\\b", "a:b", "..", "."}
	for _, k := range valid {
		if !ValidKey(k) {
			t.Errorf("ValidKey(%q) = false, want true", k)
		}
	}
	for _, k := range invalid {
		if ValidKey(k) {
			t.Errorf("ValidKey(%q) = true, want false", k)
		}
	}
}

func TestJSONStoreSaveGetList(t *testing.T) {
	s := NewJSONStore[testRecord]("")
	if err := s.Save("req-1", testRecord{ID: "req-1", Name: "a"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save("req-2", testRecord{ID: "req-2", Name: "b"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok := s.Get("req-1")
	if !ok || got.Name != "a" {
		t.Fatalf("Get = %+v, %v", got, ok)
	}
	if s.Exists("req-3") {
		t.Fatal("req-3 不应存在")
	}
	if !s.Exists("req-2") {
		t.Fatal("req-2 应存在")
	}
	if got := s.Count(); got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
	list := s.List()
	if len(list) != 2 {
		t.Fatalf("List len = %d, want 2", len(list))
	}
	if err := s.Delete("req-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if s.Exists("req-1") {
		t.Fatal("删除后 req-1 不应存在")
	}
}

func TestJSONStoreSaveInvalidKey(t *testing.T) {
	s := NewJSONStore[testRecord](t.TempDir())
	if err := s.Save("../evil", testRecord{}); err == nil {
		t.Fatal("路径穿越 key 应报错")
	}
}

func TestJSONStorePersistReload(t *testing.T) {
	dir := t.TempDir()
	s := NewJSONStore[testRecord](dir)
	if err := s.Save("req-1", testRecord{ID: "req-1", Name: "持久化"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save("req-2", testRecord{ID: "req-2", Name: "重启保留"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// 重载
	s2 := NewJSONStore[testRecord](dir)
	got, ok := s2.Get("req-1")
	if !ok || got.Name != "持久化" {
		t.Fatalf("重载失败: %+v, %v", got, ok)
	}
	// 无 .tmp 残留
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Fatalf("存在 .tmp 残留: %v", matches)
	}
}

func TestJSONStoreSchemaVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	// 写入 schema_version=999 的文件，应被跳过
	bad := envelope[testRecord]{SchemaVersion: 999, Data: testRecord{ID: "req-1", Name: "旧版本"}}
	data, _ := json.Marshal(bad)
	if err := os.WriteFile(filepath.Join(dir, "req-1.json"), data, 0644); err != nil {
		t.Fatalf("写文件: %v", err)
	}
	s := NewJSONStore[testRecord](dir)
	if s.Exists("req-1") {
		t.Fatal("schema_version 不匹配的文件不应载入")
	}
}

func TestJSONStoreLoadSkipsCorruptTmp(t *testing.T) {
	dir := t.TempDir()
	// 崩溃残留：半写 tmp 文件
	if err := os.WriteFile(filepath.Join(dir, "req-1.json.tmp"), []byte("{半写"), 0644); err != nil {
		t.Fatalf("写 tmp: %v", err)
	}
	// 损坏的正式文件
	if err := os.WriteFile(filepath.Join(dir, "req-2.json"), []byte("{corrupt"), 0644); err != nil {
		t.Fatalf("写损坏文件: %v", err)
	}
	s := NewJSONStore[testRecord](dir)
	if s.Exists("req-1") || s.Exists("req-2") {
		t.Fatal("损坏/残留文件不应载入")
	}
}

func TestJSONStoreNestedKey(t *testing.T) {
	dir := t.TempDir()
	s := NewJSONStore[testRecord](dir)
	if err := s.Save("inv-1/ev-1", testRecord{ID: "ev-1", Name: "证据"}); err != nil {
		t.Fatalf("Save 嵌套 key: %v", err)
	}
	// 文件应在子目录
	if _, err := os.Stat(filepath.Join(dir, "inv-1", "ev-1.json")); err != nil {
		t.Fatalf("嵌套文件路径不对: %v", err)
	}
	s2 := NewJSONStore[testRecord](dir)
	got, ok := s2.Get("inv-1/ev-1")
	if !ok || got.Name != "证据" {
		t.Fatalf("嵌套 key 重载失败: %+v", got)
	}
}

func TestJSONStoreWithValidate(t *testing.T) {
	dir := t.TempDir()
	// 合法记录
	good := envelope[testRecord]{SchemaVersion: CurrentSchemaVersion, Data: testRecord{ID: "req-7", Name: "ok"}}
	data, _ := json.Marshal(good)
	if err := os.WriteFile(filepath.Join(dir, "req-7.json"), data, 0644); err != nil {
		t.Fatalf("写文件: %v", err)
	}
	// ID 与文件名不一致（磁盘篡改）
	bad := envelope[testRecord]{SchemaVersion: CurrentSchemaVersion, Data: testRecord{ID: "req-8", Name: "篡改"}}
	data2, _ := json.Marshal(bad)
	if err := os.WriteFile(filepath.Join(dir, "req-9.json"), data2, 0644); err != nil {
		t.Fatalf("写文件: %v", err)
	}
	s := NewJSONStore[testRecord](dir, WithValidate(func(key string, v *testRecord) bool {
		return v.ID == key
	}))
	if _, ok := s.Get("req-7"); !ok {
		t.Fatal("合法记录应载入")
	}
	if s.Exists("req-9") {
		t.Fatal("ID 与文件名不一致的记录不应载入")
	}
}

func TestJSONStoreConcurrentSave(t *testing.T) {
	dir := t.TempDir()
	s := NewJSONStore[testRecord](dir)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "req-" + itoa2(n)
			_ = s.Save(key, testRecord{ID: key, Name: "并发"})
		}(i)
	}
	wg.Wait()
	if s.Count() != 20 {
		t.Fatalf("Count = %d, want 20", s.Count())
	}
	// 重载确认无损坏
	s2 := NewJSONStore[testRecord](dir)
	if s2.Count() != 20 {
		t.Fatalf("重载 Count = %d, want 20", s2.Count())
	}
}

func TestJSONStoreMoveToArchive(t *testing.T) {
	dir := t.TempDir()
	s := NewJSONStore[testRecord](dir)
	_ = s.Save("req-1", testRecord{ID: "req-1", Name: "归档"})
	if err := s.MoveToArchive("req-1"); err != nil {
		t.Fatalf("MoveToArchive: %v", err)
	}
	if s.Exists("req-1") {
		t.Fatal("归档后不应再存在")
	}
	if _, err := os.Stat(filepath.Join(dir, "archive", "req-1.json")); err != nil {
		t.Fatalf("归档文件不存在: %v", err)
	}
	// 重载：archive 下文件不应载入
	s2 := NewJSONStore[testRecord](dir)
	if s2.Exists("req-1") {
		t.Fatal("archive 记录不应被 loadAll 载入")
	}
}

func TestJSONStoreMoveToArchiveNestedKey(t *testing.T) {
	dir := t.TempDir()
	s := NewJSONStore[testRecord](dir)
	_ = s.Save("inv-1/ev-1", testRecord{ID: "ev-1"})
	_ = s.Save("inv-2/ev-1", testRecord{ID: "ev-1"})
	if err := s.MoveToArchive("inv-1/ev-1"); err != nil {
		t.Fatalf("MoveToArchive: %v", err)
	}
	if err := s.MoveToArchive("inv-2/ev-1"); err != nil {
		t.Fatalf("MoveToArchive: %v", err)
	}
	// 嵌套 key 归档应保留子路径，不互相覆盖
	if _, err := os.Stat(filepath.Join(dir, "archive", "inv-1", "ev-1.json")); err != nil {
		t.Fatalf("inv-1 归档缺失: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "archive", "inv-2", "ev-1.json")); err != nil {
		t.Fatalf("inv-2 归档缺失: %v", err)
	}
}

func TestJSONStoreDeepCopyIsolation(t *testing.T) {
	s := NewJSONStore[testRecord]("")
	_ = s.Save("req-1", testRecord{ID: "req-1", Name: "原值"})
	got, _ := s.Get("req-1")
	got.Name = "外部改写"
	got2, _ := s.Get("req-1")
	if got2.Name != "原值" {
		t.Fatalf("Get 应返回拷贝: %s", got2.Name)
	}
}

func itoa2(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// ── Index 单元测试 ──

func TestIndexAddGetPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "indexes", "evidence-index.json")
	ix := NewIndex(path)
	if err := ix.Add("0xabc", "ev-1"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	_ = ix.Add("0xabc", "ev-2")
	_ = ix.Add("0xabc", "ev-1") // 幂等
	_ = ix.Add("0xdef", "ev-3")
	if got := ix.Get("0xabc"); len(got) != 2 {
		t.Fatalf("Get = %v, want 2 条", got)
	}
	// 重载
	ix2 := NewIndex(path)
	if got := ix2.Get("0xabc"); len(got) != 2 {
		t.Fatalf("重载后 Get = %v, want 2 条", got)
	}
	// Remove
	if err := ix2.Remove("0xabc", "ev-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	ix3 := NewIndex(path)
	if got := ix3.Get("0xabc"); len(got) != 1 || got[0] != "ev-2" {
		t.Fatalf("Remove 后 = %v, want [ev-2]", got)
	}
}

func TestIndexBulk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "indexes", "memory-index.json")
	ix := NewIndex(path)
	if err := ix.Bulk(map[string][]string{
		"0xabc": {"rel-1", "rel-2"},
		"0xdef": {"rel-1"},
	}); err != nil {
		t.Fatalf("Bulk: %v", err)
	}
	ix2 := NewIndex(path)
	if got := ix2.Get("0xabc"); len(got) != 2 {
		t.Fatalf("Bulk 重载 = %v", got)
	}
}

// ── Lifecycle 单元测试 ──

func TestLifecycleArchiveHistory(t *testing.T) {
	dir := t.TempDir()
	s := NewJSONStore[testRecord](dir)
	lc := Lifecycle{MaxActive: 2, MaxHistory: 3}
	// 6 条终态记录（isTerminal 恒 true）
	for i := 1; i <= 6; i++ {
		key := "req-" + itoa2(i)
		_ = s.Save(key, testRecord{ID: key})
	}
	archived, err := Archive(s, func(*testRecord) bool { return true }, lc)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if archived != 3 {
		t.Fatalf("应归档 3 条（6-3）, got %d", archived)
	}
	if s.Count() != 3 {
		t.Fatalf("剩余 %d, want 3", s.Count())
	}
	// 最旧（req-1/2/3）应被归档
	for _, k := range []string{"req-1", "req-2", "req-3"} {
		if s.Exists(k) {
			t.Fatalf("%s 应被归档", k)
		}
	}
	// 重载不恢复归档
	s2 := NewJSONStore[testRecord](dir)
	if s2.Count() != 3 {
		t.Fatalf("重载 Count = %d, want 3", s2.Count())
	}
}

func TestLifecycleArchiveActive(t *testing.T) {
	dir := t.TempDir()
	s := NewJSONStore[testRecord](dir)
	lc := Lifecycle{MaxActive: 2, MaxHistory: 10}
	for i := 1; i <= 5; i++ {
		key := "req-" + itoa2(i)
		_ = s.Save(key, testRecord{ID: key})
	}
	archived, _ := Archive(s, func(*testRecord) bool { return false }, lc)
	if archived != 3 {
		t.Fatalf("应归档 3 条活跃（5-2）, got %d", archived)
	}
}

func TestLifecycleDefault(t *testing.T) {
	lc := DefaultLifecycle()
	if lc.MaxActive != 5 || lc.MaxHistory != 200 {
		t.Fatalf("默认策略 = %+v", lc)
	}
}

// ── ScoreProfileStore 单元测试 ──

func TestScoreProfileStoreSetGetPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "score-profile", "profiles.json")
	s := NewScoreProfileStore(path)
	if s.Get("fund_trace") != nil {
		t.Fatal("未配置模式应返回 nil")
	}
	w := map[string]float64{"fund": 0.4, "graph": 0.3, "entity": 0.2, "risk": 0.1}
	if err := s.Set("fund_trace", w); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// 重载
	s2 := NewScoreProfileStore(path)
	got := s2.Get("fund_trace")
	if got == nil || got["fund"] != 0.4 || got["entity"] != 0.2 {
		t.Fatalf("重载后权重 = %+v", got)
	}
	all := s2.All()
	if len(all) != 1 {
		t.Fatalf("All = %+v", all)
	}
}

// ── Plan/Task 记录 ──

func TestPlanTaskStorePersist(t *testing.T) {
	dir := t.TempDir()
	plans := NewPlanStore(filepath.Join(dir, "plans"))
	tasks := NewTaskStore(filepath.Join(dir, "tasks"))
	plan := PlanRecord{
		ID:        "inv-1",
		RequestID: "req-1",
		Target:    "0xabc",
		Mode:      "fund_trace",
		Tasks:     []PlannedTaskRecord{{Type: "FORWARD_TRACE", Priority: 1, Description: "正向追踪"}},
		CreatedAt: time.Now().UTC(),
	}
	if err := plans.Save("inv-1", plan); err != nil {
		t.Fatalf("PlanStore.Save: %v", err)
	}
	task := TaskRecord{ID: "t1", InvestigationID: "inv-1", Type: "FLOW_GRAPH", Status: "done", Output: "ok"}
	if err := tasks.Save("inv-1/t1", task); err != nil {
		t.Fatalf("TaskStore.Save: %v", err)
	}
	// 重载
	plans2 := NewPlanStore(filepath.Join(dir, "plans"))
	p, ok := plans2.Get("inv-1")
	if !ok || len(p.Tasks) != 1 || p.Tasks[0].Type != "FORWARD_TRACE" {
		t.Fatalf("Plan 重载失败: %+v", p)
	}
	tasks2 := NewTaskStore(filepath.Join(dir, "tasks"))
	tk, ok := tasks2.Get("inv-1/t1")
	if !ok || tk.Status != "done" {
		t.Fatalf("Task 重载失败: %+v", tk)
	}
}

// 编译期接口断言：JSONStore 实现 Store 接口。
var (
	_ Store[testRecord] = (*JSONStore[testRecord])(nil)
	_ Store[PlanRecord] = (*PlanStore)(nil)
	_ Store[TaskRecord] = (*TaskStore)(nil)
	_                   = strings.TrimSpace // 保持 strings 导入
)
