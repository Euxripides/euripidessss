package downloadengine

import (
	"sync"
	"time"
)

// ── V2.1 RC2 SQD容灾设计 ──
// 架构约束：不修改 Job / Provider / Dataset Registry

// ── Provider Health Score ──

type ProviderScore struct {
	Provider    string    `json:"provider"`
	SuccessRate float64   `json:"success_rate"`
	AvgLatency  int64     `json:"avg_latency_ms"`
	ErrorCount  int       `json:"error_count"`
	LastError   string    `json:"last_error,omitempty"`
	LastSuccess time.Time `json:"last_success"`
	Status      string    `json:"status"` // healthy / degraded / unavailable
}

type ProviderScorer struct {
	mu     sync.RWMutex
	scores map[string]*providerStats
}

type providerStats struct {
	name        string
	success     int64
	fail        int64
	latencySum  int64
	errorCount  int
	lastError   string
	lastSuccess time.Time
	status503   int
	status429   int
	consecutive int
}

func NewProviderScorer() *ProviderScorer {
	return &ProviderScorer{scores: make(map[string]*providerStats)}
}

func (s *ProviderScorer) RecordSuccess(provider string, latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.ensure(provider)
	st.success++
	st.latencySum += latency.Milliseconds()
	st.lastSuccess = time.Now()
	st.consecutive = 0
}

func (s *ProviderScorer) RecordFailure(provider string, err error, statusCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.ensure(provider)
	st.fail++
	st.errorCount++
	st.lastError = err.Error()
	st.consecutive++
	if statusCode == 503 {
		st.status503++
	}
	if statusCode == 429 {
		st.status429++
	}
}

func (s *ProviderScorer) Score(provider string) ProviderScore {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.ensureR(provider)
	total := st.success + st.fail
	rate := 1.0
	if total > 0 {
		rate = float64(st.success) / float64(total)
	}
	avgLat := int64(0)
	if st.success > 0 {
		avgLat = st.latencySum / st.success
	}
	status := "healthy"
	if st.consecutive >= 3 {
		status = "degraded"
	}
	if st.consecutive >= 5 {
		status = "unavailable"
	}
	return ProviderScore{
		Provider:    provider,
		SuccessRate: rate,
		AvgLatency:  avgLat,
		ErrorCount:  st.errorCount,
		LastError:   st.lastError,
		LastSuccess: st.lastSuccess,
		Status:      status,
	}
}

func (s *ProviderScorer) ensure(name string) *providerStats {
	if st, ok := s.scores[name]; ok {
		return st
	}
	st := &providerStats{name: name}
	s.scores[name] = st
	return st
}

func (s *ProviderScorer) ensureR(name string) *providerStats {
	if st, ok := s.scores[name]; ok {
		return st
	}
	return &providerStats{name: name}
}

// ── Chunk-level Failover ──

type ChunkFailover struct {
	mu      sync.Mutex
	history map[string][]failoverEntry // chunkID → failover log
}

type failoverEntry struct {
	FromProvider string
	ToProvider   string
	Reason       string
	At           time.Time
}

func NewChunkFailover() *ChunkFailover {
	return &ChunkFailover{history: make(map[string][]failoverEntry)}
}

func (f *ChunkFailover) RecordFailover(chunkID, from, to, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.history[chunkID] = append(f.history[chunkID], failoverEntry{
		FromProvider: from, ToProvider: to, Reason: reason, At: time.Now(),
	})
}

func (f *ChunkFailover) FailoverCount(chunkID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.history[chunkID])
}

// ── Retry Strategy with Backoff ──

type RetryStrategy struct{}

func (r *RetryStrategy) ShouldRetry(statusCode int, attempt int) (bool, time.Duration) {
	switch {
	case statusCode == 400:
		return false, 0 // 不重试
	case statusCode == 429:
		return true, time.Duration(30+attempt*10) * time.Second
	case statusCode == 503:
		cooldowns := []time.Duration{30, 60, 120, 300} // seconds
		if attempt < len(cooldowns) {
			return true, cooldowns[attempt] * time.Second
		}
		return true, 600 * time.Second
	default:
		// network errors: exponential backoff
		delays := []time.Duration{1, 2, 5, 10, 30} // seconds
		if attempt < len(delays) {
			return true, delays[attempt] * time.Second
		}
		return attempt < 5, 60 * time.Second
	}
}

// ── Provider Priority Resolver ──

type PriorityResolver struct {
	scorer *ProviderScorer
	order  []string // Local Dataset Cache → SQD → RPC Backup
}

func NewPriorityResolver(scorer *ProviderScorer) *PriorityResolver {
	return &PriorityResolver{
		scorer: scorer,
		order:  []string{"LOCAL_CACHE", "SQD", "RPC"},
	}
}

func (p *PriorityResolver) Resolve() string {
	for _, name := range p.order {
		score := p.scorer.Score(name)
		if score.Status == "healthy" || score.Status == "degraded" {
			return name
		}
	}
	return "RPC" // last resort
}

func (p *PriorityResolver) ShouldFailover(current string) (string, bool) {
	score := p.scorer.Score(current)
	if score.Status == "unavailable" {
		return p.Resolve(), true
	}
	return "", false
}

// ── Dataset Recovery Manifest ──

type RecoveryManifest struct {
	Dataset      string `json:"dataset"`
	TotalChunks  int    `json:"chunks"`
	Completed    int    `json:"completed"`
	FailedChunks []int  `json:"failed"`
	Provider     string `json:"provider"`
	UpdatedAt    string `json:"updated_at"`
}

func NewRecoveryManifest(dataset string) *RecoveryManifest {
	return &RecoveryManifest{Dataset: dataset}
}

func (m *RecoveryManifest) RecordCompleted(chunkIndex int) {
	m.Completed++
	m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

func (m *RecoveryManifest) RecordFailed(chunkIndex int) {
	m.FailedChunks = append(m.FailedChunks, chunkIndex)
	m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

func (m *RecoveryManifest) RecoverOnly() []int {
	return m.FailedChunks
}

// ── Metrics ──

type FailoverMetrics struct {
	mu            sync.Mutex
	TotalRequests int64
	SuccessTotal  int64
	FailTotal     int64
	Status503     int64
	Status429     int64
	LatencySum    int64
	FailoverFrom  map[string]int
	FailoverTo    map[string]int
	RecoveryCount int64
}

func NewFailoverMetrics() *FailoverMetrics {
	return &FailoverMetrics{
		FailoverFrom: make(map[string]int),
		FailoverTo:   make(map[string]int),
	}
}

func (fm *FailoverMetrics) RecordRequest(success bool, code int, latency time.Duration) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fm.TotalRequests++
	if success {
		fm.SuccessTotal++
	} else {
		fm.FailTotal++
	}
	if code == 503 {
		fm.Status503++
	}
	if code == 429 {
		fm.Status429++
	}
	fm.LatencySum += latency.Milliseconds()
}

func (fm *FailoverMetrics) RecordFailover(from, to string) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fm.FailoverFrom[from]++
	fm.FailoverTo[to]++
}

func (fm *FailoverMetrics) RecordRecovery() {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fm.RecoveryCount++
}

func (fm *FailoverMetrics) Snapshot() map[string]any {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return map[string]any{
		"total_requests": fm.TotalRequests,
		"success_total":  fm.SuccessTotal,
		"fail_total":     fm.FailTotal,
		"status_503":     fm.Status503,
		"status_429":     fm.Status429,
		"avg_latency_ms": safeDiv(fm.LatencySum, fm.TotalRequests),
		"failover_from":  fm.FailoverFrom,
		"failover_to":    fm.FailoverTo,
		"recovery_count": fm.RecoveryCount,
	}
}

func safeDiv(a, b int64) int64 {
	if b == 0 {
		return 0
	}
	return a / b
}

// ── Weighted Provider Score (PRD §6) ──

type WeightedScore struct {
	Total           float64 `json:"total"`
	SuccessWeight   float64 `json:"success_weight"`
	LatencyWeight   float64 `json:"latency_weight"`
	StabilityWeight float64 `json:"stability_weight"`
}

func (s *ProviderScorer) WeightedScore(provider string) WeightedScore {
	base := s.Score(provider)
	// 成功率 50% + 延迟 30% + 稳定性 20%
	latencyScore := 1.0
	if base.AvgLatency > 0 {
		latencyScore = 1000.0 / float64(base.AvgLatency+100)
	}
	stabilityScore := 1.0
	if base.ErrorCount > 0 {
		stabilityScore = 1.0 / float64(base.ErrorCount+1)
	}
	total := base.SuccessRate*0.5 + latencyScore*0.3 + stabilityScore*0.2
	return WeightedScore{Total: total, SuccessWeight: 0.5, LatencyWeight: 0.3, StabilityWeight: 0.2}
}

// ── Load Balancing Strategies (PRD §10) ──

type LoadBalanceMode string

const (
	BalanceSpeed  LoadBalanceMode = "speed"  // 最高score
	BalanceStable LoadBalanceMode = "stable" // 成功率最高
	BalanceCost   LoadBalanceMode = "cost"   // Cache > Free > Paid
)

func (p *PriorityResolver) ResolveWithMode(mode LoadBalanceMode) string {
	s := p.scorer
	switch mode {
	case BalanceStable:
		best := ""
		bestRate := -1.0
		for _, name := range p.order {
			score := s.Score(name)
			if score.Status != "unavailable" && score.SuccessRate > bestRate {
				bestRate = score.SuccessRate
				best = name
			}
		}
		if best != "" {
			return best
		}
	case BalanceSpeed:
		best := ""
		bestScore := -1.0
		for _, name := range p.order {
			ws := s.WeightedScore(name)
			if ws.Total > bestScore {
				bestScore = ws.Total
				best = name
			}
		}
		if best != "" {
			return best
		}
	case BalanceCost:
		for _, name := range p.order {
			if s.Score(name).Status != "unavailable" {
				return name
			}
		}
	}
	return p.Resolve()
}

// ── Provider Events Log (PRD §11) ──

type ProviderEvent struct {
	Time     time.Time `json:"time"`
	Provider string    `json:"provider"`
	Error    string    `json:"error,omitempty"`
	Action   string    `json:"action"`
}

type EventLogger struct {
	mu     sync.Mutex
	events []ProviderEvent
}

func NewEventLogger() *EventLogger {
	return &EventLogger{}
}

func (e *EventLogger) Log(provider, action, errMsg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ProviderEvent{
		Time: time.Now(), Provider: provider, Error: errMsg, Action: action,
	})
}

func (e *EventLogger) Recent(n int) []ProviderEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n > len(e.events) {
		n = len(e.events)
	}
	return e.events[len(e.events)-n:]
}

// ── Provider Config (PRD §13) ──

type ProviderConfig struct {
	Providers map[string]ProviderEntry `json:"providers"`
}

type ProviderEntry struct {
	Enabled  bool `json:"enabled"`
	Priority int  `json:"priority"`
}

func DefaultProviderConfig() *ProviderConfig {
	return &ProviderConfig{Providers: map[string]ProviderEntry{
		"sqd":   {Enabled: true, Priority: 1},
		"rpc":   {Enabled: true, Priority: 2},
		"cache": {Enabled: true, Priority: 0},
	}}
}

func (pc *ProviderConfig) EnabledProviders() []string {
	var result []string
	for name, entry := range pc.Providers {
		if entry.Enabled {
			result = append(result, name)
		}
	}
	return result
}
