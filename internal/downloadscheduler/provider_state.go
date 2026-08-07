package downloadscheduler

import (
	"os"
	"strings"
	"sync"
	"time"
)

// ProviderHealthConfig 熔断参数（设计 §11/§82）。
type ProviderHealthConfig struct {
	FailureToDegrade     int
	FailureToOpen        int
	RateLimitOpenAfter   int
	RiskControlOpenAfter int
	CooldownRateLimit    time.Duration
	CooldownService      time.Duration
	CooldownRiskControl  time.Duration
	CooldownAuth         time.Duration
}

// DefaultProviderHealthConfig 返回设计 §11 推荐参数。
func DefaultProviderHealthConfig() ProviderHealthConfig {
	return ProviderHealthConfig{
		FailureToDegrade:     3,
		FailureToOpen:        6,
		RateLimitOpenAfter:   3,
		RiskControlOpenAfter: 1,
		CooldownRateLimit:    120 * time.Second,
		CooldownService:      60 * time.Second,
		CooldownRiskControl:  900 * time.Second,
		CooldownAuth:         300 * time.Second,
	}
}

// ProviderStateInfo Provider 健康快照（API /providers/health 展示）。
type ProviderStateInfo struct {
	Provider            ProviderKind  `json:"provider"`
	State               ProviderState `json:"state"`
	Reasons             []string      `json:"reasons,omitempty"`
	ConsecutiveFailures int           `json:"consecutive_failures"`
	CooldownUntil       string        `json:"cooldown_until,omitempty"`
	LastSuccessAt       string        `json:"last_success_at,omitempty"`
	LastFailureAt       string        `json:"last_failure_at,omitempty"`
}

type providerHealthEntry struct {
	state               ProviderState
	consecutiveFailures int
	lastSuccessAt       time.Time
	lastFailureAt       time.Time
	cooldownUntil       time.Time
	reasons             []string
}

// ProviderHealthTracker 按 Provider 跟踪健康状态与熔断（设计 §10/§11）。
type ProviderHealthTracker struct {
	mu      sync.Mutex
	cfg     ProviderHealthConfig
	entries map[ProviderKind]*providerHealthEntry
}

// NewProviderHealthTracker 创建健康跟踪器。
func NewProviderHealthTracker(cfg ProviderHealthConfig) *ProviderHealthTracker {
	return &ProviderHealthTracker{cfg: cfg, entries: map[ProviderKind]*providerHealthEntry{}}
}

// RecordResult 记录一次调用结果并推进熔断状态机。
func (t *ProviderHealthTracker) RecordResult(kind ProviderKind, success bool, state ProviderState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entry(kind)
	now := time.Now()
	if success {
		e.state = ProviderHealthy
		e.consecutiveFailures = 0
		e.lastSuccessAt = now
		e.cooldownUntil = time.Time{}
		e.reasons = nil
		return
	}
	e.lastFailureAt = now
	e.consecutiveFailures++
	switch state {
	case ProviderRateLimited:
		e.state = ProviderRateLimited
		e.reasons = []string{"HTTP 429 / 配额耗尽"}
		if e.consecutiveFailures >= t.cfg.RateLimitOpenAfter {
			e.state = ProviderCircuitOpen
			e.cooldownUntil = now.Add(t.cfg.CooldownRateLimit)
			e.reasons = []string{"连续限流，熔断 OPEN（冷却 " + t.cfg.CooldownRateLimit.String() + "）"}
		}
	case ProviderRiskControlled:
		e.state = ProviderRiskControlled
		e.cooldownUntil = now.Add(t.cfg.CooldownRiskControl)
		e.reasons = []string{"403/风控/CAPTCHA，长冷却 " + t.cfg.CooldownRiskControl.String() + "（禁止继续轰击）"}
	case ProviderAuthBlocked:
		e.state = ProviderAuthBlocked
		e.cooldownUntil = now.Add(t.cfg.CooldownAuth)
		e.reasons = []string{"认证不可用（401），冷却 " + t.cfg.CooldownAuth.String()}
	case ProviderUnsupported:
		e.state = ProviderUnsupported
		e.reasons = []string{"数据能力不匹配"}
	case ProviderUnavailable:
		e.state = ProviderUnavailable
		e.reasons = []string{"未装配/服务不可用"}
	default: // DEGRADED / 服务错误
		if e.consecutiveFailures >= t.cfg.FailureToOpen {
			e.state = ProviderCircuitOpen
			e.cooldownUntil = now.Add(t.cfg.CooldownService)
			e.reasons = []string{"连续 " + itoa(e.consecutiveFailures) + " 次失败，熔断 OPEN（冷却 " + t.cfg.CooldownService.String() + "）"}
		} else if e.consecutiveFailures >= t.cfg.FailureToDegrade {
			e.state = ProviderDegraded
			e.reasons = []string{"连续 " + itoa(e.consecutiveFailures) + " 次失败，已降级"}
		} else {
			e.state = ProviderDegraded
			e.reasons = []string{"最近一次调用失败（服务错误/超时/网络）"}
		}
	}
}

// State 返回 Provider 当前状态（冷却到期自动恢复）。
func (t *ProviderHealthTracker) State(kind ProviderKind) ProviderState {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entry(kind)
	if !e.cooldownUntil.IsZero() && time.Now().After(e.cooldownUntil) {
		e.state = ProviderHealthy
		e.consecutiveFailures = 0
		e.cooldownUntil = time.Time{}
		e.reasons = nil
	}
	if e.state == "" {
		return ProviderHealthy
	}
	return e.state
}

// Exhausted 判断 Provider 当前是否耗尽（设计 §13）。
func (t *ProviderHealthTracker) Exhausted(kind ProviderKind) bool {
	st := t.State(kind)
	if st.Exhausted() {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entry(kind)
	return !e.cooldownUntil.IsZero() && time.Now().Before(e.cooldownUntil)
}

// Snapshot 返回全部 Provider 健康快照。
func (t *ProviderHealthTracker) Snapshot() map[ProviderKind]ProviderStateInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[ProviderKind]ProviderStateInfo, len(t.entries))
	for kind, e := range t.entries {
		if !e.cooldownUntil.IsZero() && time.Now().After(e.cooldownUntil) {
			e.state = ProviderHealthy
			e.consecutiveFailures = 0
			e.cooldownUntil = time.Time{}
			e.reasons = nil
		}
		info := ProviderStateInfo{
			Provider:            kind,
			State:               e.state,
			Reasons:             append([]string(nil), e.reasons...),
			ConsecutiveFailures: e.consecutiveFailures,
		}
		if !e.cooldownUntil.IsZero() {
			info.CooldownUntil = e.cooldownUntil.Format(time.RFC3339)
		}
		if !e.lastSuccessAt.IsZero() {
			info.LastSuccessAt = e.lastSuccessAt.Format(time.RFC3339)
		}
		if !e.lastFailureAt.IsZero() {
			info.LastFailureAt = e.lastFailureAt.Format(time.RFC3339)
		}
		if info.State == "" {
			info.State = ProviderHealthy
		}
		out[kind] = info
	}
	return out
}

func (t *ProviderHealthTracker) entry(kind ProviderKind) *providerHealthEntry {
	e := t.entries[kind]
	if e == nil {
		e = &providerHealthEntry{state: ProviderHealthy}
		t.entries[kind] = e
	}
	return e
}

// ClassifyProviderError 按错误文本/HTTP 状态分类（设计 §7-9）。
// 429 → RATE_LIMITED；403/CAPTCHA/风控 → RISK_CONTROLLED；401 → AUTH_BLOCKED；
// 5xx/超时/DNS/连接重置 → DEGRADED；能力不匹配 → UNSUPPORTED。
func ClassifyProviderError(err error, httpStatus int) ProviderState {
	if httpStatus == 429 {
		return ProviderRateLimited
	}
	if httpStatus == 401 {
		return ProviderAuthBlocked
	}
	if httpStatus == 403 {
		return ProviderRiskControlled
	}
	if httpStatus >= 500 {
		return ProviderDegraded
	}
	if err == nil {
		return ProviderHealthy
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"rate limit", "quota exhausted", "too many requests", "429", "throttle", "request capacity exceeded",
	} {
		if strings.Contains(msg, marker) {
			return ProviderRateLimited
		}
	}
	for _, marker := range []string{
		"captcha", "cloudflare", "bot detected", "access denied", "ip banned", "security verification",
		"account temporarily restricted", "waf blocked", "anti-abuse", "风控", "风险控制",
	} {
		if strings.Contains(msg, marker) {
			return ProviderRiskControlled
		}
	}
	for _, marker := range []string{"unauthorized", "invalid apikey", "auth", "401", "signature", "签名"} {
		if strings.Contains(msg, marker) {
			return ProviderAuthBlocked
		}
	}
	for _, marker := range []string{"unsupported", "capability", "not support", "不匹配"} {
		if strings.Contains(msg, marker) {
			return ProviderUnsupported
		}
	}
	return ProviderDegraded
}

// NormalProvidersUsable 判断常规 Provider 候选里是否还有可用者（设计 §14 AllNormalProvidersExhausted）。
// ManualOnly（需人工）不算自动可用；Emergency Cloud 不参与。
func NormalProvidersUsable(candidates []ProviderScore, stateOf func(ProviderKind) ProviderState) bool {
	for _, c := range candidates {
		if c.ManualOnly || c.Tier >= TierEmergencyCloud {
			continue
		}
		st := stateOf(c.Provider)
		if st == "" {
			st = ProviderHealthy
		}
		if !st.Exhausted() {
			return true // HEALTHY / DEGRADED 但可用 → 不算耗尽
		}
	}
	return false
}

// FaultInjection 故障注入配置（设计 §95/§96：测试用，生产默认关闭）。
// 仅通过环境变量 SCHEDULER_FAULT_INJECTION=all_normal_providers_fail 启用。
type FaultInjection struct {
	AllNormalProvidersFail bool `json:"all_normal_providers_fail"`
}

// FaultInjectionFromEnv 从环境变量读取故障注入配置（不暴露给 API 调用方修改）。
func FaultInjectionFromEnv() FaultInjection {
	return FaultInjection{
		AllNormalProvidersFail: strings.EqualFold(os.Getenv("SCHEDULER_FAULT_INJECTION"), "all_normal_providers_fail"),
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
