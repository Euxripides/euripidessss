// Package discovery 实现 Smart Discovery Engine V2（设计 V1.0）：
// L0 Metadata → L1 Lightweight Probe → L2 Adaptive Sample → 分段建模 → 置信度 → 缓存。
package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Input Discovery 输入（地址 × 数据集 × 区间）。
type Input struct {
	ChainID     int64
	ChainKey    string
	Address     string
	Dataset     string
	FromBlock   uint64
	ToBlock     uint64
	BytesPerRow uint64 // 每行估算字节（默认 160）
	HeadBlock   uint64 // 当前链头（用于历史闭合区间长缓存）
	Activity    int64  // 本地已知活动量（缓存 TTL 分档用）
}

// ProviderCandidate Provider 候选（Discovery 输出，直接进入 Scheduler）。
type ProviderCandidate struct {
	Provider                string        `json:"provider"`
	Supported               bool          `json:"supported"`
	EstimatedRowsPerSecond  float64       `json:"estimated_rows_per_second"`
	EstimatedBytesPerSecond float64       `json:"estimated_bytes_per_second"`
	EstimatedRuntime        time.Duration `json:"estimated_runtime"`
	EstimatedCost           float64       `json:"estimated_cost"`
	CoverageScore           float64       `json:"coverage_score"`
	HealthScore             float64       `json:"health_score"`
	ResumeSupport           bool          `json:"resume_support"`
	RangeSupport            bool          `json:"range_support"`
	Score                   float64       `json:"score"`
}

// ActivitySegment 活动分段（设计 §11）。
type ActivitySegment struct {
	FromBlock     uint64  `json:"from_block"`
	ToBlock       uint64  `json:"to_block"`
	EstimatedRows uint64  `json:"estimated_rows"`
	Density       float64 `json:"density"`
	Confidence    float64 `json:"confidence"`
}

// DiscoveryResult Discovery 输出（设计 §4）。
type DiscoveryResult struct {
	ChainID                    int64                    `json:"chain_id"`
	Address                    string                   `json:"address"`
	Dataset                    string                   `json:"dataset"`
	FirstBlock                 uint64                   `json:"first_block"`
	LastBlock                  uint64                   `json:"last_block"`
	EstimatedRows              uint64                   `json:"estimated_rows"`
	EstimatedBytes             uint64                   `json:"estimated_bytes"`
	EstimatedTempBytes         uint64                   `json:"estimated_temp_bytes"`
	EstimatedRuntimeByProvider map[string]time.Duration `json:"estimated_runtime_by_provider,omitempty"`
	EstimatedCostByProvider    map[string]float64       `json:"estimated_cost_by_provider,omitempty"`
	ActivityDensity            float64                  `json:"activity_density"`
	SuggestedRangeSpan         uint64                   `json:"suggested_range_span"`
	SupportedProviders         []ProviderCandidate      `json:"supported_providers,omitempty"`
	Segments                   []ActivitySegment        `json:"segments,omitempty"`
	Confidence                 float64                  `json:"confidence"`
	CreatedAt                  time.Time                `json:"created_at"`
}

// MetadataSource L0：无需扫描的 total 来源（Registry/历史/explorer API）。
type MetadataSource interface {
	TotalRows(ctx context.Context, chainKey, address, dataset string, from, to uint64) (uint64, bool, error)
}

// SampleFunc L1/L2：对窗口 [from,to] 返回该窗口行数（由 ProviderAdapter 适配）。
type SampleFunc func(ctx context.Context, from, to uint64) (uint64, error)

// Engine Discovery 引擎。
type Engine struct {
	root              string
	metadata          MetadataSource
	sample            SampleFunc
	probeWindowBlocks uint64
	maxProbeWindows   int
	maxProbeRuntime   time.Duration
}

// NewEngine 创建引擎；sample 为空时仅 Metadata/Cache。
func NewEngine(root string, metadata MetadataSource, sample SampleFunc) *Engine {
	return &Engine{
		root:              root,
		metadata:          metadata,
		sample:            sample,
		probeWindowBlocks: 200,
		maxProbeWindows:   8,
		maxProbeRuntime:   30 * time.Second,
	}
}

// ConfidenceLevel 置信度档位。
func ConfidenceLevel(c float64) string {
	switch {
	case c >= 0.9:
		return "HIGH"
	case c >= 0.7:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// Discover 执行 L0→L1→L2→分段→估算→缓存。
func (e *Engine) Discover(ctx context.Context, in Input) (*DiscoveryResult, error) {
	if in.BytesPerRow == 0 {
		in.BytesPerRow = 160
	}
	if key, ok := e.cacheKey(in); ok {
		if res := e.loadCache(key, in); res != nil {
			return res, nil
		}
	}
	res := &DiscoveryResult{
		ChainID:    in.ChainID,
		Address:    in.Address,
		Dataset:    in.Dataset,
		FirstBlock: in.FromBlock,
		LastBlock:  in.ToBlock,
		CreatedAt:  time.Now().UTC(),
	}
	// L0 Metadata
	if e.metadata != nil {
		if total, ok, err := e.metadata.TotalRows(ctx, in.ChainKey, in.Address, in.Dataset, in.FromBlock, in.ToBlock); err == nil && ok && total > 0 {
			res.EstimatedRows = total
			res.Confidence = 0.95
			e.finalize(res, in, []ActivitySegment{{
				FromBlock: in.FromBlock, ToBlock: in.ToBlock,
				EstimatedRows: total, Density: density(total, in.ToBlock-in.FromBlock+1), Confidence: 0.95,
			}})
			e.saveCache(keyFor(in), res, in)
			return res, nil
		}
	}
	// L1/L2 采样
	segments, conf := e.sampleSegments(ctx, in)
	if len(segments) == 0 {
		// 采样不可用：零置信度，由调用方回退固定 Range
		res.Confidence = 0
		e.saveCache(keyFor(in), res, in)
		return res, nil
	}
	e.finalize(res, in, segments)
	res.Confidence = conf
	e.saveCache(keyFor(in), res, in)
	return res, nil
}

func (e *Engine) finalize(res *DiscoveryResult, in Input, segments []ActivitySegment) {
	var rows uint64
	for _, s := range segments {
		rows += s.EstimatedRows
	}
	res.Segments = segments
	res.EstimatedRows = rows
	res.EstimatedBytes = rows * in.BytesPerRow
	res.EstimatedTempBytes = res.EstimatedBytes * 3
	span := in.ToBlock - in.FromBlock + 1
	if span > 0 {
		res.ActivityDensity = float64(rows) / float64(span)
	}
	res.SuggestedRangeSpan = AdaptiveSpan(res.ActivityDensity, 50_000, 500, 500_000)
}

// sampleSegments L1 首/中/尾 → L2 自适应加密 → 分段建模。
func (e *Engine) sampleSegments(ctx context.Context, in Input) ([]ActivitySegment, float64) {
	if e.sample == nil {
		return nil, 0
	}
	start := time.Now()
	windows := e.initialWindows(in)
	samples, err := e.collectWindows(ctx, in, windows, start)
	if err != nil || len(samples) == 0 {
		return nil, 0
	}
	// L2：方差高且未达上限 → 加密到 5/8 窗口
	if relativeStddev(samples) > 0.5 && len(samples) < e.maxProbeWindows {
		more := evenWindows(in.FromBlock, in.ToBlock, minInt(e.maxProbeWindows, len(samples)+3))
		extra, err2 := e.collectWindows(ctx, in, more[len(samples):], start)
		if err2 == nil {
			samples = append(samples, extra...)
		}
	}
	if relativeStddev(samples) > 0.75 && len(samples) >= 6 {
		// 分段：按窗口所属的 3 等分子区间聚合
		return e.segmentBySamples(in, samples), 0.72
	}
	meanDensity := meanDensity(samples)
	total := uint64(meanDensity * float64(in.ToBlock-in.FromBlock+1))
	return []ActivitySegment{{
		FromBlock: in.FromBlock, ToBlock: in.ToBlock,
		EstimatedRows: total, Density: meanDensity, Confidence: 0.85,
	}}, confidenceFromVariance(relativeStddev(samples))
}

type sampleWindow struct {
	from, to uint64
	rows     uint64
	density  float64
}

func (e *Engine) initialWindows(in Input) []sampleWindow {
	var out []sampleWindow
	span := in.ToBlock - in.FromBlock + 1
	if span <= e.probeWindowBlocks {
		out = append(out, sampleWindow{from: in.FromBlock, to: in.ToBlock})
		return out
	}
	step := span / 4
	for _, f := range []uint64{0, 2 * step, 4*step - e.probeWindowBlocks} {
		from := in.FromBlock + f
		to := from + e.probeWindowBlocks - 1
		if to > in.ToBlock {
			to = in.ToBlock
		}
		if to >= from {
			out = append(out, sampleWindow{from: from, to: to})
		}
	}
	return out
}

func (e *Engine) collectWindows(ctx context.Context, in Input, windows []sampleWindow, start time.Time) ([]sampleWindow, error) {
	var out []sampleWindow
	for _, w := range windows {
		if time.Since(start) > e.maxProbeRuntime {
			break // Probe 成本守卫（设计 §29）
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		rows, err := e.sample(ctx, w.from, w.to)
		if err != nil {
			continue
		}
		w.rows = rows
		w.density = density(rows, w.to-w.from+1)
		out = append(out, w)
	}
	return out, nil
}

// segmentBySamples 把采样窗口按 3 等分子区间聚合为 ActivitySegment。
func (e *Engine) segmentBySamples(in Input, samples []sampleWindow) []ActivitySegment {
	span := in.ToBlock - in.FromBlock + 1
	third := span / 3
	segs := make([]ActivitySegment, 3)
	for i := range segs {
		from := in.FromBlock + uint64(i)*third
		to := in.FromBlock + uint64(i+1)*third - 1
		if i == 2 {
			to = in.ToBlock
		}
		segs[i] = ActivitySegment{FromBlock: from, ToBlock: to}
	}
	for _, s := range samples {
		idx := int((s.from - in.FromBlock) / third)
		if idx < 0 {
			idx = 0
		}
		if idx > 2 {
			idx = 2
		}
		segs[idx].Density += s.density
	}
	counts := make([]int, 3)
	for _, s := range samples {
		idx := int((s.from - in.FromBlock) / third)
		if idx < 0 {
			idx = 0
		}
		if idx > 2 {
			idx = 2
		}
		counts[idx]++
	}
	for i := range segs {
		if counts[i] > 0 {
			segs[i].Density /= float64(counts[i])
		}
		span := segs[i].ToBlock - segs[i].FromBlock + 1
		segs[i].EstimatedRows = uint64(segs[i].Density * float64(span))
		segs[i].Confidence = 0.7
	}
	return segs
}

func evenWindows(from, to uint64, n int) []sampleWindow {
	var out []sampleWindow
	if n <= 1 {
		return []sampleWindow{{from: from, to: to}}
	}
	span := to - from + 1
	for i := 0; i < n; i++ {
		f := from + uint64(i)*(span/uint64(n))
		e := f + span/uint64(n) - 1
		if e > to {
			e = to
		}
		if e >= f {
			out = append(out, sampleWindow{from: f, to: e})
		}
	}
	return out
}

func density(rows, blocks uint64) float64 {
	if blocks == 0 {
		return 0
	}
	return float64(rows) / float64(blocks)
}

func meanDensity(samples []sampleWindow) float64 {
	if len(samples) == 0 {
		return 0
	}
	var total float64
	for _, s := range samples {
		total += s.density
	}
	return total / float64(len(samples))
}

func relativeStddev(samples []sampleWindow) float64 {
	if len(samples) == 0 {
		return 0
	}
	mean := meanDensity(samples)
	if mean <= 0 {
		return 0
	}
	var v float64
	for _, s := range samples {
		d := s.density - mean
		v += d * d
	}
	std := math.Sqrt(v / float64(len(samples)))
	return std / mean
}

func confidenceFromVariance(relStd float64) float64 {
	switch {
	case relStd < 0.25:
		return 0.9
	case relStd < 0.5:
		return 0.8
	default:
		return 0.7
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── 缓存（设计 §27）──

type cacheEntry struct {
	Result    *DiscoveryResult `json:"result"`
	CreatedAt time.Time        `json:"created_at"`
	TTL       time.Duration    `json:"ttl_seconds"`
}

func keyFor(in Input) string {
	sum := sha256.Sum256([]byte(strings.ToLower(in.ChainKey + "|" + in.Address + "|" + in.Dataset +
		"|" + u64(in.FromBlock) + "|" + u64(in.ToBlock))))
	return hex.EncodeToString(sum[:16])
}

func (e *Engine) cacheKey(in Input) (string, bool) {
	key := keyFor(in)
	dir := filepath.Join(e.root, "smart_download", "discovery-cache")
	if _, err := os.Stat(filepath.Join(dir, key+".json")); err == nil {
		return key, true
	}
	return key, false
}

func (e *Engine) loadCache(key string, in Input) *DiscoveryResult {
	path := filepath.Join(e.root, "smart_download", "discovery-cache", key+".json")
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entry cacheEntry
	if json.Unmarshal(payload, &entry) != nil || entry.Result == nil {
		return nil
	}
	if entry.Result.Confidence <= 0 {
		return nil // 失败/零置信度结果不缓存命中，允许重新探测
	}
	ttl := cacheTTL(in)
	if entry.TTL > 0 {
		ttl = entry.TTL
	}
	if ttl > 0 && time.Since(entry.CreatedAt) > ttl {
		return nil
	}
	return entry.Result
}

func (e *Engine) saveCache(key string, res *DiscoveryResult, in Input) {
	if res == nil || res.Confidence <= 0 {
		return
	}
	dir := filepath.Join(e.root, "smart_download", "discovery-cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	payload, _ := json.MarshalIndent(cacheEntry{
		Result: res, CreatedAt: time.Now().UTC(), TTL: cacheTTL(in),
	}, "", "  ")
	path := filepath.Join(dir, key+".json")
	tmp := path + ".tmp"
	if os.WriteFile(tmp, payload, 0o644) != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// cacheTTL 缓存有效期：活跃地址 1h / 普通 6h / 历史闭合区间长缓存（设计 §27）。
func cacheTTL(in Input) time.Duration {
	if in.HeadBlock > 0 && in.ToBlock < in.HeadBlock {
		return 720 * time.Hour // 历史已闭合：近似永久
	}
	if in.Activity > 100 {
		return time.Hour
	}
	return 6 * time.Hour
}

func u64(v uint64) string {
	buf := make([]byte, 0, 20)
	for v > 0 {
		buf = append([]byte{byte('0' + v%10)}, buf...)
		v /= 10
	}
	if len(buf) == 0 {
		return "0"
	}
	return string(buf)
}

// SortSegments 按起始块排序（导出给调用方）。
func SortSegments(segments []ActivitySegment) {
	sort.Slice(segments, func(i, j int) bool { return segments[i].FromBlock < segments[j].FromBlock })
}
