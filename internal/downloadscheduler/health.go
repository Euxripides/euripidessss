package downloadscheduler

import (
	"fmt"
	"time"
)

// SQDHealthSnapshot 是 SQD 可靠性层的健康快照（V3 设计 §11 健康监控）。
// 由 API 装配层从 parquetdownload.Manager.SQDStatus() 适配而来。
type SQDHealthSnapshot struct {
	CooldownActive bool    `json:"cooldown_active"`
	CooldownUntil  string  `json:"cooldown_until,omitempty"` // RFC3339
	BreakerState   string  `json:"breaker_state"`            // NORMAL/DEGRADED/OPEN/HALF_OPEN
	Workers        int     `json:"workers"`
	WorkerTier     string  `json:"worker_tier"` // NORMAL/DEGRADED/EMERGENCY
	Consecutive503 int     `json:"consecutive_503"`
	SuccessRate    float64 `json:"success_rate"` // 0-1；无样本时为 0
	Requests       int64   `json:"requests"`
	Failures       int64   `json:"failures"`
}

// HealthSource 提供 SQD 可靠性层实时健康（由 API 层注入，避免包循环依赖）。
type HealthSource interface {
	SQDHealth() SQDHealthSnapshot
}

// Healthy 综合健康判定：无冷却、熔断非 OPEN/HALF_OPEN、成功率正常。
func (h SQDHealthSnapshot) Healthy() bool {
	if h.CooldownActive {
		return false
	}
	switch h.BreakerState {
	case "OPEN", "HALF_OPEN":
		return false
	}
	if h.SuccessRate > 0 && h.SuccessRate < 0.85 {
		return false
	}
	return true
}

// DegradeReasons 生成人类可读的降级原因（前端面板展示）。
func (h SQDHealthSnapshot) DegradeReasons() []string {
	var reasons []string
	if h.CooldownActive {
		until := h.CooldownUntil
		if until == "" {
			until = "未知时间"
		}
		reasons = append(reasons, fmt.Sprintf("SQD 503 冷却中（至 %s），已降级", until))
	}
	switch h.BreakerState {
	case "OPEN":
		reasons = append(reasons, "SQD 熔断 OPEN，请求被阻断，已降级")
	case "HALF_OPEN":
		reasons = append(reasons, "SQD 熔断 HALF_OPEN（探测中），已降级")
	case "DEGRADED":
		reasons = append(reasons, "SQD 熔断 DEGRADED（连续失败中），已降级")
	}
	if h.WorkerTier == "EMERGENCY" {
		reasons = append(reasons, "SQD 自适应并发已降至 EMERGENCY（1 worker）")
	} else if h.WorkerTier == "DEGRADED" {
		reasons = append(reasons, "SQD 自适应并发已降至 DEGRADED（4 workers）")
	}
	if h.Consecutive503 > 0 {
		reasons = append(reasons, fmt.Sprintf("SQD 连续 %d 次 503", h.Consecutive503))
	}
	if h.SuccessRate > 0 && h.SuccessRate < 0.85 {
		reasons = append(reasons, fmt.Sprintf("SQD 成功率 %.0f%% 偏低", h.SuccessRate*100))
	}
	return reasons
}

// HealthAge 返回冷却剩余时间（无冷却返回 0）。
func (h SQDHealthSnapshot) HealthAge() time.Duration {
	if !h.CooldownActive || h.CooldownUntil == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, h.CooldownUntil)
	if err != nil {
		return 0
	}
	remaining := time.Until(t)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// providerDegrade 按健康快照计算 SQD Provider 的可靠性衰减（0-100）。
func providerDegrade(h SQDHealthSnapshot) (reliabilityPenalty int, speedPenalty int, reasons []string) {
	switch h.BreakerState {
	case "OPEN":
		reliabilityPenalty += 55
	case "HALF_OPEN":
		reliabilityPenalty += 35
	case "DEGRADED":
		reliabilityPenalty += 15
	}
	if h.CooldownActive {
		reliabilityPenalty += 25
		speedPenalty += 20
	}
	switch h.WorkerTier {
	case "EMERGENCY":
		speedPenalty += 25
	case "DEGRADED":
		speedPenalty += 10
	}
	if h.Consecutive503 >= 3 {
		reliabilityPenalty += 10
	} else if h.Consecutive503 > 0 {
		reliabilityPenalty += 5
	}
	if h.SuccessRate > 0 && h.SuccessRate < 0.85 {
		reliabilityPenalty += 15
	}
	reasons = h.DegradeReasons()
	return
}
