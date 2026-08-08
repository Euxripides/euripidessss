// Package datasetevents 实现 Dataset Event Bus（Phase 5.3 §5/§14）：
// 持久化事件 + 幂等消费者，连接 Orchestrator → Investigation / Graph。
package datasetevents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// EventType 事件类型（Phase 5.3 §5）。
type EventType string

const (
	DataRequirementCreated EventType = "DATA_REQUIREMENT_CREATED"
	DownloadPlanCreated    EventType = "DOWNLOAD_PLAN_CREATED"
	DownloadStarted        EventType = "DOWNLOAD_STARTED"
	RemoteCompleted        EventType = "REMOTE_COMPLETED"
	LocalSyncStarted       EventType = "LOCAL_SYNC_STARTED"
	DatasetValidated       EventType = "DATASET_VALIDATED"
	DatasetIndexed         EventType = "DATASET_INDEXED"
	CoverageUpdated        EventType = "COVERAGE_UPDATED"
	InvestigationResumed   EventType = "INVESTIGATION_RESUMED"
	GraphIncrementApplied  EventType = "GRAPH_INCREMENT_APPLIED"
	InvestigationCancelled EventType = "INVESTIGATION_CANCELLED"
)

// Event 标准事件载荷。
type Event struct {
	ID               string         `json:"event_id"`
	Seq              uint64         `json:"seq,omitempty"`
	Type             EventType      `json:"type"`
	RequirementID    string         `json:"requirement_id,omitempty"`
	ChainKey         string         `json:"chain_key,omitempty"`
	Dataset          string         `json:"dataset,omitempty"`
	Addresses        []string       `json:"addresses,omitempty"`
	FromBlock        uint64         `json:"from_block,omitempty"`
	ToBlock          uint64         `json:"to_block,omitempty"`
	RegistryEntryIDs []string       `json:"registry_entry_ids,omitempty"`
	RowCount         int64          `json:"row_count,omitempty"`
	CoverageStatus   string         `json:"coverage_status,omitempty"`
	Provider         string         `json:"provider,omitempty"`
	IndexedAt        time.Time      `json:"indexed_at,omitempty"`
	Meta             map[string]any `json:"meta,omitempty"`
}

// IndexedEventID 为 DATASET_INDEXED 生成确定性事件 ID（幂等/重启重放基础）。
func IndexedEventID(chunkKey string) string {
	return "idx:" + chunkKey
}

// Consumer 事件消费者。
type Consumer func(ctx context.Context, e Event) error

// Bus 持久化事件总线。
type Bus struct {
	mu        sync.Mutex
	path      string
	events    []Event
	consumers map[string][]Consumer
	processed map[string]map[string]bool // consumer → event id
	seq       uint64
}

// NewBus 创建/加载事件总线。
func NewBus(path string) (*Bus, error) {
	b := &Bus{
		path:      path,
		consumers: map[string][]Consumer{},
		processed: map[string]map[string]bool{},
	}
	if err := b.load(); err != nil {
		return nil, err
	}
	return b, nil
}

// Subscribe 注册消费者（同一 name 幂等去重）。
func (b *Bus) Subscribe(name string, fn Consumer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.processed[name] == nil {
		b.processed[name] = map[string]bool{}
	}
	b.consumers[name] = append(b.consumers[name], fn)
}

// Publish 持久化并广播事件。
func (b *Bus) Publish(ctx context.Context, e Event) error {
	if e.ID == "" {
		e.ID = fmt.Sprintf("%s-%d", strings.ToLower(string(e.Type)), time.Now().UnixNano())
	}
	if e.IndexedAt.IsZero() {
		e.IndexedAt = time.Now()
	}
	b.mu.Lock()
	for _, existing := range b.events {
		if existing.ID == e.ID {
			b.mu.Unlock()
			return nil // 幂等：同 ID 不重复落库
		}
	}
	b.seq++
	e.Seq = b.seq
	b.events = append(b.events, e)
	if err := b.saveLocked(); err != nil {
		b.mu.Unlock()
		return err
	}
	consumers := b.snapshotConsumersLocked()
	b.mu.Unlock()
	for name, fns := range consumers {
		for _, fn := range fns {
			if b.AlreadyProcessed(name, e.ID) {
				continue
			}
			if err := fn(ctx, e); err == nil {
				_ = b.MarkProcessed(name, e.ID)
			}
		}
	}
	return nil
}

// Replay 重放全部已持久化事件（重启恢复：消费者幂等跳过已处理）。
func (b *Bus) Replay(ctx context.Context) {
	b.mu.Lock()
	events := append([]Event(nil), b.events...)
	consumers := b.snapshotConsumersLocked()
	b.mu.Unlock()
	for _, e := range events {
		for name, fns := range consumers {
			for _, fn := range fns {
				if b.AlreadyProcessed(name, e.ID) {
					continue
				}
				if err := fn(ctx, e); err == nil {
					_ = b.MarkProcessed(name, e.ID)
				}
			}
		}
	}
}

// AlreadyProcessed 消费者幂等检查。
func (b *Bus) AlreadyProcessed(consumer, eventID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.processed[consumer][eventID]
}

// MarkProcessed 记录消费者已处理事件并持久化。
func (b *Bus) MarkProcessed(consumer, eventID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.processed[consumer] == nil {
		b.processed[consumer] = map[string]bool{}
	}
	b.processed[consumer][eventID] = true
	return b.saveProcessedLocked()
}

// Events 返回全部事件（新→旧）。
func (b *Bus) Events() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := append([]Event(nil), b.events...)
	sort.Slice(out, func(i, j int) bool { return out[i].IndexedAt.After(out[j].IndexedAt) })
	return out
}

func (b *Bus) snapshotConsumersLocked() map[string][]Consumer {
	out := map[string][]Consumer{}
	for k, v := range b.consumers {
		out[k] = append([]Consumer(nil), v...)
	}
	return out
}

func (b *Bus) load() error {
	if b.path == "" {
		return nil
	}
	payload, err := os.ReadFile(b.path)
	if err == nil {
		var list []Event
		if json.Unmarshal(payload, &list) != nil {
			// 启动损坏扫描：隔离坏文件，不阻塞启动（Phase 5.4 §10）
			_ = os.Rename(b.path, b.path+".corrupt-"+time.Now().Format("20060102T150405"))
		} else {
			b.events = list
			for _, e := range list {
				if e.Seq > b.seq {
					b.seq = e.Seq
				}
			}
		}
	}
	processedPath := b.path + ".processed.json"
	if payload, err := os.ReadFile(processedPath); err == nil {
		var m map[string][]string
		if json.Unmarshal(payload, &m) == nil {
			for name, ids := range m {
				if b.processed[name] == nil {
					b.processed[name] = map[string]bool{}
				}
				for _, id := range ids {
					b.processed[name][id] = true
				}
			}
		}
	}
	return nil
}

func (b *Bus) saveLocked() error {
	if b.path == "" {
		return nil
	}
	unlock, err := acquireFileLock(b.path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return err
	}
	payload, _ := json.MarshalIndent(b.events, "", "  ")
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	if f, err := os.OpenFile(tmp, os.O_RDWR, 0o644); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
	return os.Rename(tmp, b.path)
}

func (b *Bus) saveProcessedLocked() error {
	if b.path == "" {
		return nil
	}
	unlock, err := acquireFileLock(b.path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	path := b.path + ".processed.json"
	m := map[string][]string{}
	for name, ids := range b.processed {
		list := make([]string, 0, len(ids))
		for id := range ids {
			list = append(list, id)
		}
		sort.Strings(list)
		m[name] = list
	}
	payload, _ := json.MarshalIndent(m, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	if f, err := os.OpenFile(tmp, os.O_RDWR, 0o644); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
	return os.Rename(tmp, path)
}

// acquireFileLock 跨进程文件锁（O_CREATE|O_EXCL + 过期清理，Phase 5.4 §10）。
func acquireFileLock(lockPath string) (func(), error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "pid=%d time=%s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > 10*time.Second {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("event store lock busy: %s", lockPath)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
