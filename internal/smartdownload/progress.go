package smartdownload

import (
	"sync"
	"time"
)

// EWMA ETA（实施方案 §23）：speed = α*current + (1-α)*previous。
// 切换 Provider 后重新计算（reset 由调用方触发）。
const etaAlpha = 0.3

type etaState struct {
	prevSpeedRows float64
	prevSpeedBlk  float64
	lastRows      uint64
	lastBlocks    uint64
	lastTime      time.Time
	started       bool
}

// ewmaSpeed 计算平滑速度。
func ewmaSpeed(prev, current float64, started bool) float64 {
	if !started || prev <= 0 {
		return current
	}
	return etaAlpha*current + (1-etaAlpha)*prev
}

// ── SSE 事件（实施方案 §24：Progress Aggregator 250-500ms 合并后推送）──

const (
	EventAddressUpdated    = "address.updated"
	EventDatasetUpdated    = "dataset.updated"
	EventProviderSwitched  = "provider.switched"
	EventRangeCompleted    = "range.completed"
	EventValidationUpdated = "validation.updated"
	EventResultReady       = "result.ready"
	EventError             = "error"
)

// Event 统一 SSE 事件。
type Event struct {
	Type         string         `json:"type"`
	BatchID      string         `json:"batch_id,omitempty"`
	AddressJobID string         `json:"address_job_id,omitempty"`
	DatasetJobID string         `json:"dataset_job_id,omitempty"`
	RangeID      string         `json:"range_id,omitempty"`
	Provider     string         `json:"provider,omitempty"`
	Status       string         `json:"status,omitempty"`
	Message      string         `json:"message,omitempty"`
	TS           time.Time      `json:"ts"`
	Payload      map[string]any `json:"payload,omitempty"`
}

// EventBus 轻量事件总线（SSE 订阅；dataset/address 更新按 throttle 合并）。
type EventBus struct {
	mu          sync.Mutex
	nextID      int
	subscribers map[int]chan Event
	throttle    time.Duration
	lastSent    map[string]time.Time
}

// NewEventBus 创建事件总线（默认合并窗口 300ms）。
func NewEventBus(throttle time.Duration) *EventBus {
	if throttle <= 0 {
		throttle = 300 * time.Millisecond
	}
	return &EventBus{
		subscribers: map[int]chan Event{},
		throttle:    throttle,
		lastSent:    map[string]time.Time{},
	}
}

// Subscribe 订阅事件，返回订阅 ID 与接收 channel（缓冲 64）。
func (b *EventBus) Subscribe() (int, chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	ch := make(chan Event, 64)
	b.subscribers[b.nextID] = ch
	return b.nextID, ch
}

func (b *EventBus) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subscribers[id]; ok {
		delete(b.subscribers, id)
		close(ch)
	}
}

// Publish 发布事件；dataset.updated/address.updated 按 key 300ms 合并，其余直接推送。
func (b *EventBus) Publish(e Event) {
	b.mu.Lock()
	key := e.Type + "|" + e.DatasetJobID + e.AddressJobID + e.BatchID
	if e.Type == EventDatasetUpdated || e.Type == EventAddressUpdated {
		if last, ok := b.lastSent[key]; ok && time.Since(last) < b.throttle {
			b.mu.Unlock()
			return
		}
		b.lastSent[key] = time.Now()
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	subs := make([]chan Event, 0, len(b.subscribers))
	for _, ch := range b.subscribers {
		subs = append(subs, ch)
	}
	b.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- e:
		default: // 订阅者慢则丢弃（SSE 有快照兜底）
		}
	}
}

// Events 返回事件总线（API 注册用）。
func (s *Service) Events() *EventBus { return s.events }
