package prefetch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// BudgetStore 维护每日预取预算（设计 §33）。
type BudgetStore struct {
	root string
	cfg  Budget
	mu   sync.Mutex
	cur  Counters
}

// NewBudgetStore 创建预算存储。
func NewBudgetStore(root string, cfg Budget) (*BudgetStore, error) {
	b := &BudgetStore{root: root, cfg: cfg}
	if err := b.load(); err != nil {
		return nil, err
	}
	b.rollDay()
	return b, nil
}

// Config 返回预算配置。
func (b *BudgetStore) Config() Budget {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cfg
}

// Counters 返回当日计数。
func (b *BudgetStore) Counters() Counters {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollDayLocked()
	return b.cur
}

// Allow 判断是否允许启动一个新预取任务。
func (b *BudgetStore) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollDayLocked()
	if b.cfg.MaxActivePrefetchJobs > 0 && b.cur.ActiveJobs >= b.cfg.MaxActivePrefetchJobs {
		return fmt.Errorf("prefetch: 活动预取任务已达上限 %d", b.cfg.MaxActivePrefetchJobs)
	}
	if b.cfg.MaxPrefetchAddresses > 0 && b.cur.Addresses >= b.cfg.MaxPrefetchAddresses {
		return fmt.Errorf("prefetch: 当日预取地址数已达上限 %d", b.cfg.MaxPrefetchAddresses)
	}
	if b.cfg.MaxNetworkPerDayGB > 0 && b.cur.NetworkGB >= b.cfg.MaxNetworkPerDayGB {
		return fmt.Errorf("prefetch: 当日网络预算已用尽")
	}
	return nil
}

// Consume 在启动任务时消耗地址配额并占用活动名额。
func (b *BudgetStore) Consume() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollDayLocked()
	b.cur.Addresses++
	b.cur.ActiveJobs++
	return b.saveLocked()
}

// Release 任务终态释放活动名额。
func (b *BudgetStore) Release() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollDayLocked()
	if b.cur.ActiveJobs > 0 {
		b.cur.ActiveJobs--
	}
	return b.saveLocked()
}

// RecordDownload 记录网络/磁盘消耗（字节）。
func (b *BudgetStore) RecordDownload(networkBytes, diskBytes uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollDayLocked()
	b.cur.NetworkGB += float64(networkBytes) / (1024 * 1024 * 1024)
	b.cur.DiskGB += float64(diskBytes) / (1024 * 1024 * 1024)
	return b.saveLocked()
}

func (b *BudgetStore) rollDay() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollDayLocked()
}

func (b *BudgetStore) rollDayLocked() {
	today := time.Now().UTC().Format("2006-01-02")
	if b.cur.Day != today {
		b.cur = Counters{Day: today}
		_ = b.saveLocked()
	}
}

func (b *BudgetStore) load() error {
	payload, err := os.ReadFile(b.path())
	if err != nil {
		return nil
	}
	return json.Unmarshal(payload, &b.cur)
}

func (b *BudgetStore) saveLocked() error {
	if b.root == "" {
		return nil
	}
	if err := os.MkdirAll(b.root, 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(b.cur, "", "  ")
	if err != nil {
		return err
	}
	tmp := b.path() + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, b.path())
}

func (b *BudgetStore) path() string {
	return filepath.Join(b.root, "budget.json")
}

