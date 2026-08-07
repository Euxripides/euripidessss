package downloadscheduler

// ProviderTier Provider 优先级分层（设计文档 V3.0 §16）。
// 排序规则：Tier → Capability → Health → Cost → Throughput；Tier 永远优先。
type ProviderTier int

const (
	TierLocal          ProviderTier = 0   // 本地数据（覆盖检查命中后不调用 Provider）
	TierNormal         ProviderTier = 10  // 常规 Provider：Browser/SQD Public/RPC/AWS
	TierFallback       ProviderTier = 20  // 预留：次级常规 Provider
	TierEmergencyCloud ProviderTier = 100 // SQD Cloud：最后兜底，禁止参与常规竞速
)

// ProviderState Provider 运行状态（设计文档 V3.0 §6）。
type ProviderState string

const (
	ProviderHealthy        ProviderState = "HEALTHY"
	ProviderDegraded       ProviderState = "DEGRADED"
	ProviderRateLimited    ProviderState = "RATE_LIMITED"
	ProviderRiskControlled ProviderState = "RISK_CONTROLLED"
	ProviderCircuitOpen    ProviderState = "CIRCUIT_OPEN"
	ProviderAuthBlocked    ProviderState = "AUTH_BLOCKED"
	ProviderUnavailable    ProviderState = "UNAVAILABLE"
	ProviderUnsupported    ProviderState = "UNSUPPORTED"
	ProviderNotConfigured  ProviderState = "NOT_CONFIGURED"
)

// Exhausted 判断该状态是否表示 Provider 当前不可用（设计 §13）。
func (s ProviderState) Exhausted() bool {
	switch s {
	case ProviderCircuitOpen, ProviderRateLimited, ProviderRiskControlled,
		ProviderAuthBlocked, ProviderUnavailable, ProviderUnsupported, ProviderNotConfigured:
		return true
	}
	return false
}

// StateProvider 可选接口：Provider 上报实时运行状态（API/Admission Gate 展示）。
type StateProvider interface {
	State() ProviderState
	StateReasons() []string
}

// providerStateOf 获取 Provider 状态（未实现 StateProvider 视为 HEALTHY）。
func providerStateOf(p Provider) ProviderState {
	if sp, ok := p.(StateProvider); ok {
		return sp.State()
	}
	return ProviderHealthy
}

// providerReasonsOf 获取 Provider 状态原因。
func providerReasonsOf(p Provider) []string {
	if sp, ok := p.(StateProvider); ok {
		return sp.StateReasons()
	}
	return nil
}
