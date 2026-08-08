// Package progress 实现 Progress Aggregator V2 + ETA Engine V1（设计 V1.0）：
// 统一 ProgressEvent、加权进度、EWMA+中位数 ETA、事件序列/回放。
package progress

import (
	"math"
	"sort"
	"sync"
	"time"
)

// ProgressKind 统一进度单位（设计 §5）。
type ProgressKind string

const (
	KindRows          ProgressKind = "ROWS"
	KindBytes         ProgressKind = "BYTES"
	KindBlocks        ProgressKind = "BLOCKS"
	KindPages         ProgressKind = "PAGES"
	KindRanges        ProgressKind = "RANGES"
	KindRequests      ProgressKind = "REQUESTS"
	KindMixed         ProgressKind = "MIXED"
	KindIndeterminate ProgressKind = "INDETERMINATE"
)

// ProgressEvent 统一进度事件（设计 §4）。
type ProgressEvent struct {
	EventID      string       `json:"event_id"`
	BatchID      string       `json:"batch_id,omitempty"`
	AddressJobID string       `json:"address_job_id,omitempty"`
	DatasetJobID string       `json:"dataset_job_id,omitempty"`
	RangeID      string       `json:"range_id,omitempty"`
	Provider     string       `json:"provider,omitempty"`
	Kind         ProgressKind `json:"kind"`

	RowsCurrent     uint64 `json:"rows_current,omitempty"`
	RowsTotal       uint64 `json:"rows_total,omitempty"`
	BytesCurrent    uint64 `json:"bytes_current,omitempty"`
	BytesTotal      uint64 `json:"bytes_total,omitempty"`
	BlocksCurrent   uint64 `json:"blocks_current,omitempty"`
	BlocksTotal     uint64 `json:"blocks_total,omitempty"`
	PagesCurrent    uint64 `json:"pages_current,omitempty"`
	PagesTotal      uint64 `json:"pages_total,omitempty"`
	RangesCurrent   uint64 `json:"ranges_current,omitempty"`
	RangesTotal     uint64 `json:"ranges_total,omitempty"`
	RequestsCurrent uint64 `json:"requests_current,omitempty"`
	RequestsTotal   uint64 `json:"requests_total,omitempty"`

	Stage     string    `json:"stage,omitempty"`
	Status    string    `json:"status,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// ThroughputSnapshot 吞吐快照（设计 §16）。
type ThroughputSnapshot struct {
	RowsPerSecond          float64 `json:"rows_per_second"`
	BytesPerSecond         float64 `json:"bytes_per_second"`
	BlocksPerSecond        float64 `json:"blocks_per_second"`
	RangesPerSecond        float64 `json:"ranges_per_second"`
	RequestsPerSecond      float64 `json:"requests_per_second"`
	SmoothedRowsPerSecond  float64 `json:"smoothed_rows_per_second"`
	SmoothedBytesPerSecond float64 `json:"smoothed_bytes_per_second"`
}

// ETASnapshot ETA 快照（设计 §17）。
type ETASnapshot struct {
	Seconds           int64  `json:"seconds"`
	LowerBoundSeconds int64  `json:"lower_bound_seconds"`
	UpperBoundSeconds int64  `json:"upper_bound_seconds"`
	Confidence        string `json:"confidence"` // HIGH/MEDIUM/LOW/UNKNOWN
	Recalculating     bool   `json:"recalculating"`
	BasedOn           string `json:"based_on,omitempty"`
}

// ProgressSnapshot 层级快照（设计 §7）。
type ProgressSnapshot struct {
	EntityType      string             `json:"entity_type"`
	EntityID        string             `json:"entity_id"`
	Status          string             `json:"status"`
	Stage           string             `json:"stage,omitempty"`
	ProgressPercent float64            `json:"progress_percent"`
	PrimaryUnit     string             `json:"primary_unit,omitempty"`
	Current         uint64             `json:"current,omitempty"`
	Total           uint64             `json:"total,omitempty"`
	RowsCurrent     uint64             `json:"rows_current,omitempty"`
	RowsTotal       uint64             `json:"rows_total,omitempty"`
	BytesCurrent    uint64             `json:"bytes_current,omitempty"`
	BytesTotal      uint64             `json:"bytes_total,omitempty"`
	BlocksCurrent   uint64             `json:"blocks_current,omitempty"`
	BlocksTotal     uint64             `json:"blocks_total,omitempty"`
	RangesCurrent   uint64             `json:"ranges_current,omitempty"`
	RangesTotal     uint64             `json:"ranges_total,omitempty"`
	Provider        string             `json:"provider,omitempty"`
	Throughput      ThroughputSnapshot `json:"throughput"`
	ETA             ETASnapshot        `json:"eta"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

// RangeProgress 单个 Range 的权重与进度。
type RangeProgress struct {
	Weight  float64 `json:"weight"`
	Percent float64 `json:"percent"`
}

// WeightedProgress 加权进度（设计 §9-§11）：Σ(w×p)/Σ(w)。
func WeightedProgress(items []RangeProgress) float64 {
	var wSum, wpSum float64
	for _, it := range items {
		if it.Weight < 0 {
			it.Weight = 0
		}
		wSum += it.Weight
		wpSum += it.Weight * it.Percent
	}
	if wSum <= 0 {
		return 0
	}
	return math.Min(1, math.Max(0, wpSum/wSum))
}

// RangeWeight 权重：优先估算行数，其次区块跨度，最后 1。
func RangeWeight(estimatedRows uint64, blockSpan uint64) float64 {
	if estimatedRows > 0 {
		return float64(estimatedRows)
	}
	if blockSpan > 0 {
		return float64(blockSpan)
	}
	return 1
}

// ── ETA Engine（设计 §14-§22）──

// ETAEngine 每 Dataset 一个：EWMA + 滚动中位数 + 切换重置。
type ETAEngine struct {
	mu         sync.Mutex
	provider   string
	alpha      float64
	ewmaRows   float64
	ewmaBlocks float64
	samples    []float64 // rows/s 滚动窗口
	started    bool
}

// NewETAEngine 创建引擎；alpha 按 Provider 稳定性调整（Direct 更大，其余 0.25）。
func NewETAEngine(provider string) *ETAEngine {
	alpha := 0.25
	if provider == "csv" || provider == "direct" {
		alpha = 0.35
	}
	return &ETAEngine{provider: provider, alpha: alpha}
}

// Update 更新瞬时吞吐并返回平滑值与中位数。
func (e *ETAEngine) Update(rows, blocks uint64, dt time.Duration) (rowsPS, blocksPS, smoothed, median float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	secs := dt.Seconds()
	if secs <= 0 {
		return 0, 0, e.ewmaRows, medianOf(e.samples)
	}
	curRows := float64(rows) / secs
	curBlocks := float64(blocks) / secs
	if !e.started || e.ewmaRows <= 0 {
		e.ewmaRows = curRows
		e.ewmaBlocks = curBlocks
		e.started = true
	} else {
		e.ewmaRows = e.alpha*curRows + (1-e.alpha)*e.ewmaRows
		e.ewmaBlocks = e.alpha*curBlocks + (1-e.alpha)*e.ewmaBlocks
	}
	e.samples = append(e.samples, curRows)
	if len(e.samples) > 20 {
		e.samples = e.samples[len(e.samples)-20:]
	}
	return curRows, curBlocks, e.ewmaRows, medianOf(e.samples)
}

// Reset 切换 Provider / Cloud Tier 时清理短期吞吐窗口（设计 §20/§21）。
func (e *ETAEngine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ewmaRows, e.ewmaBlocks = 0, 0
	e.samples = nil
	e.started = false
}

// SampleCount 有效样本数（ETA 重算判定）。
func (e *ETAEngine) SampleCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.samples)
}

// RowsRate 返回平滑行速度。
func (e *ETAEngine) RowsRate() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ewmaRows
}

// BlocksRate 返回平滑区块速度。
func (e *ETAEngine) BlocksRate() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ewmaBlocks
}

// Provider 返回当前 Provider。
func (e *ETAEngine) Provider() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.provider
}

func medianOf(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// ComputeETA 计算 ETA 快照：剩余行数/平滑速度（无行数则用区块），加上冷却时间，给出上下界与置信度。
func ComputeETA(remainingRows, rate float64, remainingBlocks, blockRate float64,
	recalculating bool, sampleCount int, discoveryConfidence float64, cooldown time.Duration) ETASnapshot {
	if recalculating || rate <= 0 && blockRate <= 0 {
		return ETASnapshot{Confidence: "UNKNOWN", Recalculating: recalculating, BasedOn: "recalculating"}
	}
	var seconds float64
	based := "rows"
	if rate > 0 && remainingRows > 0 {
		seconds = remainingRows / rate
	} else if blockRate > 0 && remainingBlocks > 0 {
		seconds = remainingBlocks / blockRate
		based = "blocks"
	} else {
		return ETASnapshot{Confidence: "UNKNOWN", Recalculating: recalculating, BasedOn: "indeterminate"}
	}
	seconds += cooldown.Seconds()
	lower := seconds * 0.7
	upper := seconds * 1.3
	conf := "LOW"
	switch {
	case sampleCount >= 5 && discoveryConfidence >= 0.7:
		conf = "HIGH"
	case sampleCount >= 3:
		conf = "MEDIUM"
	case cooldown > 0:
		conf = "MEDIUM"
	}
	return ETASnapshot{
		Seconds:           int64(math.Round(seconds)),
		LowerBoundSeconds: int64(math.Round(lower)),
		UpperBoundSeconds: int64(math.Round(upper)),
		Confidence:        conf, Recalculating: false, BasedOn: based,
	}
}

// ── 事件序列与回放缓冲（设计 §28-§32/§55-§57）──

// BufferedEvent 缓冲事件。
type BufferedEvent struct {
	ID      uint64 `json:"id"`
	BatchID string `json:"batch_id,omitempty"`
	Type    string `json:"type"`
	Payload []byte `json:"payload"`
}

// EventBuffer 有界环形缓冲：保留最近 N 条（progress 可被新值覆盖；状态事件优先保留）。
type EventBuffer struct {
	mu      sync.Mutex
	entries []BufferedEvent
	max     int
}

// NewEventBuffer 创建缓冲。
func NewEventBuffer(max int) *EventBuffer {
	if max <= 0 {
		max = 10_000
	}
	return &EventBuffer{max: max}
}

// Append 追加事件，返回是否丢弃了旧事件。
func (b *EventBuffer) Append(ev BufferedEvent) (dropped bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, ev)
	if len(b.entries) > b.max {
		b.entries = b.entries[len(b.entries)-b.max:]
		dropped = true
	}
	return dropped
}

// Replay 回放指定批次、ID 之后的事件；返回 (事件, 是否全部在缓冲内)。
func (b *EventBuffer) Replay(batchID string, afterID uint64) ([]BufferedEvent, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	oldest := uint64(0)
	lastID := uint64(0)
	if len(b.entries) > 0 {
		oldest = b.entries[0].ID
		lastID = b.entries[len(b.entries)-1].ID
	}
	if (afterID > 0 && afterID+1 < oldest) || (afterID > 0 && afterID >= lastID) {
		return nil, false // 已超出缓冲 → resync
	}
	var out []BufferedEvent
	for _, e := range b.entries {
		if e.ID <= afterID {
			continue
		}
		if batchID != "" && e.BatchID != "" && e.BatchID != batchID {
			continue
		}
		out = append(out, e)
	}
	return out, true
}

// Coalescer 事件合并（设计 §35-§36）：状态变化立即发送，普通进度按间隔合并。
type Coalescer struct {
	mu       sync.Mutex
	interval time.Duration
	lastSent map[string]time.Time
}

// NewCoalescer 创建合并器（默认 300ms）。
func NewCoalescer(interval time.Duration) *Coalescer {
	if interval <= 0 {
		interval = 300 * time.Millisecond
	}
	return &Coalescer{interval: interval, lastSent: map[string]time.Time{}}
}

// ShouldSend 判断是否发送：状态事件恒发；progress 按 key 节流。
func (c *Coalescer) ShouldSend(key string, isStateEvent bool) bool {
	if isStateEvent {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if last, ok := c.lastSent[key]; ok && time.Since(last) < c.interval {
		return false
	}
	c.lastSent[key] = time.Now()
	return true
}

// SequenceStore 每批次单调递增序列（设计 §28）。
type SequenceStore struct {
	mu  sync.Mutex
	seq map[string]uint64
}

// NewSequenceStore 创建序列存储。
func NewSequenceStore() *SequenceStore { return &SequenceStore{seq: map[string]uint64{}} }

// Next 返回批次下一个序列号。
func (s *SequenceStore) Next(batchID string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq[batchID]++
	return s.seq[batchID]
}

// Current 返回当前序列号。
func (s *SequenceStore) Current(batchID string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq[batchID]
}
