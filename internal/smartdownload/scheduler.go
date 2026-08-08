package smartdownload

import (
	"context"
	"sort"
	"sync"
	"time"
)

// 规模分级（实施方案 §9：足够好地分类 S/M/L/XL）。
const (
	SizeClassS  = "S"  // <= 10k
	SizeClassM  = "M"  // 10k ~ 500k
	SizeClassL  = "L"  // 500k ~ 5m
	SizeClassXL = "XL" // > 5m
)

func sizeClass(rows uint64) string {
	switch {
	case rows <= 10_000:
		return SizeClassS
	case rows <= 500_000:
		return SizeClassM
	case rows <= 5_000_000:
		return SizeClassL
	default:
		return SizeClassXL
	}
}

// ProviderScore Provider 候选评分（Phase 2：规则 + 简单加权；Phase 3 加历史成功率学习）。
type ProviderScore struct {
	Name       string   `json:"name"`
	Available  bool     `json:"available"`
	ManualOnly bool     `json:"manual_only"`
	Score      int      `json:"score"`
	SizeClass  string   `json:"size_class,omitempty"`
	Reasons    []string `json:"reasons,omitempty"`
}

// DatasetPlan 单个 Address × Dataset 的执行计划。
type DatasetPlan struct {
	Dataset           string          `json:"dataset"`
	Address           string          `json:"address"`
	ChainKey          string          `json:"chain_key"`
	EstimatedRows     uint64          `json:"estimated_rows"`
	EstimatedBytes    uint64          `json:"estimated_bytes"`
	SizeClass         string          `json:"size_class"`
	PreferredProvider string          `json:"preferred_provider,omitempty"`
	Candidates        []ProviderScore `json:"candidates"`
}

// ExecutionPlan 批次执行计划。
type ExecutionPlan struct {
	BatchID  string         `json:"batch_id"`
	Datasets []*DatasetPlan `json:"datasets"`
}

// ProviderHealthEntry Provider 健康状态（熔断：连续 2 次失败 → 冷却 60s）。
type ProviderHealthEntry struct {
	ConsecutiveFailures int       `json:"consecutive_failures"`
	CooldownUntil       time.Time `json:"cooldown_until,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
}

// ProviderHealthTracker 简单熔断跟踪（Phase 3 对接现有 SQD 健康快照）。
type ProviderHealthTracker struct {
	mu      sync.Mutex
	entries map[string]*ProviderHealthEntry
}

func NewProviderHealthTracker() *ProviderHealthTracker {
	return &ProviderHealthTracker{entries: map[string]*ProviderHealthEntry{}}
}

func (t *ProviderHealthTracker) RecordFailure(name string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entry(name)
	e.ConsecutiveFailures++
	if err != nil {
		e.LastError = sanitizeText(err.Error())
	}
	if e.ConsecutiveFailures >= 2 {
		e.CooldownUntil = time.Now().Add(60 * time.Second)
	}
}

func (t *ProviderHealthTracker) RecordSuccess(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entry(name)
	e.ConsecutiveFailures = 0
	e.CooldownUntil = time.Time{}
	e.LastError = ""
}

func (t *ProviderHealthTracker) Exhausted(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entry(name)
	if !e.CooldownUntil.IsZero() && time.Now().Before(e.CooldownUntil) {
		return true
	}
	if !e.CooldownUntil.IsZero() && !time.Now().Before(e.CooldownUntil) {
		e.ConsecutiveFailures = 0
		e.CooldownUntil = time.Time{}
	}
	return false
}

func (t *ProviderHealthTracker) Snapshot() map[string]ProviderHealthEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]ProviderHealthEntry, len(t.entries))
	for name, e := range t.entries {
		cp := *e
		if !cp.CooldownUntil.IsZero() && !time.Now().Before(cp.CooldownUntil) {
			cp.ConsecutiveFailures = 0
			cp.CooldownUntil = time.Time{}
		}
		out[name] = cp
	}
	return out
}

func (t *ProviderHealthTracker) entry(name string) *ProviderHealthEntry {
	e := t.entries[name]
	if e == nil {
		e = &ProviderHealthEntry{}
		t.entries[name] = e
	}
	return e
}

func sanitizeText(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// SmartScheduler 智能调度器：规则 + 评分选择 Provider（实施方案 §9-§10）。
type SmartScheduler struct {
	mu       sync.Mutex
	adapters map[string]ProviderAdapter
	health   *ProviderHealthTracker
}

func NewSmartScheduler() *SmartScheduler {
	return &SmartScheduler{
		adapters: map[string]ProviderAdapter{},
		health:   NewProviderHealthTracker(),
	}
}

func (s *SmartScheduler) Register(a ProviderAdapter) {
	if a == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adapters[a.Name()] = a
}

func (s *SmartScheduler) Health() *ProviderHealthTracker { return s.health }

// Candidates 返回支持该 Dataset 的候选（排序：可用 > 手动 > 分数）。
func (s *SmartScheduler) Candidates(dataset string) []ProviderScore {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ProviderScore
	for name, a := range s.adapters {
		if !a.Supports(dataset) {
			continue
		}
		out = append(out, ProviderScore{
			Name:       name,
			Available:  a.Available(),
			ManualOnly: manualOnly(a),
			Score:      baseScore(name, dataset, 0),
			Reasons:    []string{"Provider Adapter 已注册"},
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Available != out[j].Available {
			return out[i].Available
		}
		if out[i].ManualOnly != out[j].ManualOnly {
			return !out[i].ManualOnly
		}
		return out[i].Score > out[j].Score
	})
	return out
}

// PlanDataset 探测 + 评分，生成单 Dataset 执行计划。
func (s *SmartScheduler) PlanDataset(ctx context.Context, req ProbeRequest) (*DatasetPlan, error) {
	cands := s.Candidates(req.Dataset)
	best := ProbeResult{Confidence: 0}
	for _, c := range cands {
		if c.ManualOnly || !c.Available {
			continue
		}
		s.mu.Lock()
		a := s.adapters[c.Name]
		s.mu.Unlock()
		res, err := ProbeWith(ctx, a, req)
		if err != nil {
			continue
		}
		if res.Confidence > best.Confidence {
			best = res
		}
	}
	cls := sizeClass(best.EstimatedRows)
	scores := make([]ProviderScore, 0, len(cands))
	for _, c := range cands {
		c.Score = baseScore(c.Name, req.Dataset, best.EstimatedRows)
		c.SizeClass = cls
		if s.health.Exhausted(c.Name) {
			c.Score -= 30
			c.Reasons = append(c.Reasons, "健康熔断冷却中")
		}
		scores = append(scores, c)
	}
	preferred := ""
	for _, c := range scores {
		if !c.ManualOnly && c.Available && !s.health.Exhausted(c.Name) {
			preferred = c.Name
			break
		}
	}
	return &DatasetPlan{
		Dataset:           req.Dataset,
		Address:           req.Address,
		ChainKey:          req.ChainKey,
		EstimatedRows:     best.EstimatedRows,
		EstimatedBytes:    best.EstimatedBytes,
		SizeClass:         cls,
		PreferredProvider: preferred,
		Candidates:        scores,
	}, nil
}

// SelectProvider 为单个 Range 选择 Provider（跳过已失败/冷却 Provider，Cloud 最后兜底）。
func (s *SmartScheduler) SelectProvider(dataset string, failed []string) (string, bool) {
	cands := s.Candidates(dataset)
	for _, c := range cands {
		if c.ManualOnly || !c.Available {
			continue
		}
		if containsString(failed, c.Name) {
			continue
		}
		if s.health.Exhausted(c.Name) {
			continue
		}
		return c.Name, true
	}
	// 常规 Provider 耗尽 → Cloud 兜底（candidates 已含 sqd_cloud）
	for _, c := range cands {
		if c.Name != "sqd_cloud" || !c.Available {
			continue
		}
		if containsString(failed, c.Name) {
			continue
		}
		return c.Name, true
	}
	return "", false
}

// HasNextProvider 是否还有未失败的可用 Provider。
func (s *SmartScheduler) HasNextProvider(dataset string, failed []string) bool {
	_, ok := s.SelectProvider(dataset, failed)
	return ok
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// baseScore 按 Provider + 规模档给基础分（简单加权：可用性/覆盖/速度/成本/续传）。
func baseScore(name, dataset string, rows uint64) int {
	cls := sizeClass(rows)
	switch name {
	case "csv":
		// 小数据优先 CSV/官方接口；中等规模 CSD 降权
		switch cls {
		case SizeClassS:
			return 90
		default:
			return 45
		}
	case "sqd":
		score := 75
		if cls == SizeClassM || cls == SizeClassL || cls == SizeClassXL {
			score = 88 // 中大数据首选 SQD
		}
		if dataset == DatasetBalances {
			return 30 // 余额不走 SQD
		}
		return score
	case "rpc":
		switch dataset {
		case DatasetBalances:
			return 95 // 实时余额必须 RPC
		case DatasetTokenTransfers:
			return 72 // RPC 恢复通道
		default:
			return 55
		}
	case "sqd_cloud":
		return 20 // 最后兜底
	default:
		return 40
	}
}

func manualOnly(a ProviderAdapter) bool {
	m, ok := a.(interface{ ManualOnly() bool })
	return ok && m.ManualOnly()
}
