package dynamicinvestigation

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── 队列状态机测试 ──

func TestQueueAddAndGet(t *testing.T) {
	q := NewQueue("")
	item, added := q.Add("0xABC123", "root", "1000000", "0xtoken", 0)
	if !added {
		t.Fatal("首次添加应为新增")
	}
	if item.Status != StatusDiscovered {
		t.Fatalf("初始状态应为 DISCOVERED, got %s", item.Status)
	}
	if item.Address != "0xabc123" {
		t.Fatalf("地址应归一化为小写, got %s", item.Address)
	}

	// 幂等
	_, added2 := q.Add("0xabc123", "other", "", "", 0)
	if added2 {
		t.Fatal("重复添加应返回 added=false")
	}
	if q.Total() != 1 {
		t.Fatalf("队列总数应为 1, got %d", q.Total())
	}
}

func TestQueueTransitions(t *testing.T) {
	q := NewQueue("")
	q.Add("0xaaa", "root", "", "", 0)

	// 合法路径：DISCOVERED → SCORING → APPROVED → ACQUIRING → COMPLETED
	if err := q.Transition("0xaaa", StatusScoring); err != nil {
		t.Fatalf("DISCOVERED→SCORING 应合法: %v", err)
	}
	if err := q.Transition("0xaaa", StatusApproved); err != nil {
		t.Fatalf("SCORING→APPROVED 应合法: %v", err)
	}
	if err := q.Transition("0xaaa", StatusAcquiring); err != nil {
		t.Fatalf("APPROVED→ACQUIRING 应合法: %v", err)
	}
	if err := q.Transition("0xaaa", StatusCompleted); err != nil {
		t.Fatalf("ACQUIRING→COMPLETED 应合法: %v", err)
	}

	// 终态不可迁移
	if err := q.Transition("0xaaa", StatusScoring); err == nil {
		t.Fatal("COMPLETED 终态迁移应失败")
	}
	// 幂等
	if err := q.Transition("0xaaa", StatusCompleted); err != nil {
		t.Fatalf("同状态迁移应幂等: %v", err)
	}
}

func TestQueueIllegalTransition(t *testing.T) {
	q := NewQueue("")
	q.Add("0xbbb", "root", "", "", 0)
	// DISCOVERED → COMPLETED 非法
	if err := q.Transition("0xbbb", StatusCompleted); err == nil {
		t.Fatal("DISCOVERED→COMPLETED 应非法")
	}
	// 不存在地址
	if err := q.Transition("0xdead", StatusScoring); err == nil {
		t.Fatal("不存在的地址迁移应失败")
	}
}

func TestQueueListAndCount(t *testing.T) {
	q := NewQueue("")
	q.Add("0x1", "root", "", "", 0)
	q.Add("0x2", "root", "", "", 0)
	q.Add("0x3", "root", "", "", 1)
	q.SetStatus("0x1", StatusApproved)
	q.SetStatus("0x2", StatusIgnored)

	if got := q.Count()[StatusApproved]; got != 1 {
		t.Fatalf("APPROVED 计数应为 1, got %d", got)
	}
	if got := q.Count()[StatusIgnored]; got != 1 {
		t.Fatalf("IGNORED 计数应为 1, got %d", got)
	}
	items := q.List(StatusDiscovered, "", -1)
	if len(items) != 1 {
		t.Fatalf("DISCOVERED 列表应为 1, got %d", len(items))
	}
	// 深度过滤
	shallow := q.List("", "", 0)
	if len(shallow) != 2 {
		t.Fatalf("深度≤0 应有 2 条, got %d", len(shallow))
	}
	// 待评分
	if pending := q.PendingScoring(10); len(pending) != 1 {
		t.Fatalf("待评分应有 1 条, got %d", len(pending))
	}
	// 待采集按评分排序
	q.SetScore("0x1", 80, nil)
	acq := q.PendingAcquisition(10)
	if len(acq) != 1 || acq[0] != "0x1" {
		t.Fatalf("待采集应为 [0x1], got %v", acq)
	}
}

func TestQueuePersistence(t *testing.T) {
	dir := t.TempDir()
	addr := "0x0000000000000000000000000000000000000abc"
	q1 := NewQueue(dir)
	q1.Add(addr, "root", "500", "0xtoken", 1)
	q1.SetStatus(addr, StatusApproved)
	q1.SetEntity(addr, EntityWallet, "钱包地址")
	q1.SetScore(addr, 66.5, map[string]float64{"amount": 40})
	if err := q1.Save(); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	// 重新加载
	q2 := NewQueue(dir)
	item, ok := q2.Get(addr)
	if !ok {
		t.Fatal("重载后地址应存在")
	}
	if item.Status != StatusApproved {
		t.Fatalf("重载后状态应为 APPROVED, got %s", item.Status)
	}
	if item.Entity != EntityWallet {
		t.Fatalf("重载后实体应为 wallet, got %s", item.Entity)
	}
	if item.Score != 66.5 {
		t.Fatalf("重载后评分应为 66.5, got %v", item.Score)
	}
	if item.Depth != 1 {
		t.Fatalf("重载后深度应为 1, got %d", item.Depth)
	}
}

func TestQueueSaveTwiceNoDirty(t *testing.T) {
	dir := t.TempDir()
	q := NewQueue(dir)
	q.Add("0x1", "root", "", "", 0)
	if err := q.Save(); err != nil {
		t.Fatal(err)
	}
	// 无变更时第二次 Save 不应重写（dirty=false）
	st1, _ := os.Stat(filepath.Join(dir, "discovery_queue.json"))
	time.Sleep(10 * time.Millisecond)
	if err := q.Save(); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(filepath.Join(dir, "discovery_queue.json"))
	if st1.ModTime() != st2.ModTime() {
		t.Fatal("无变更时不应重写文件")
	}
}
