// Package feedback 实现 Execution Feedback Loop V1（设计 V1.0）：
// 运行指标采集 → 触发条件判定 → 动作（KEEP/RETRY/THROTTLE/REDUCE_RANGE/SWITCH/CLOUD/SCALE/FAIL）。
package feedback

import "time"

// Action 重调度动作（设计 §20）。
type Action string

const (
	Keep           Action = "KEEP"
	Retry          Action = "RETRY"
	Throttle       Action = "THROTTLE"
	ReduceRange    Action = "REDUCE_RANGE"
	SwitchProvider Action = "SWITCH_PROVIDER"
	EnterCloud     Action = "ENTER_CLOUD"
	ScaleUpCloud   Action = "SCALE_UP_CLOUD"
	ScaleDownCloud Action = "SCALE_DOWN_CLOUD"
	Fail           Action = "FAIL"
)

// ExecutionMetrics 运行指标（设计 §18）。
type ExecutionMetrics struct {
	Provider         string
	Dataset          string
	RowsPerSecond    float64
	BytesPerSecond   float64
	BlocksPerSecond  float64
	SuccessRate      float64
	HTTP429Rate      float64
	HTTP503Rate      float64
	TimeoutRate      float64
	P50Latency       time.Duration
	P95Latency       time.Duration
	RetryCount       int
	CPUUsage         float64
	MemoryUsage      float64
	CurrentETA       time.Duration
	OriginalETA      time.Duration
	CompletedPercent float64
	CircuitOpen      bool
	SilentGap        bool
	OOMCount         int
	TimeoutCount     int
}

// Decision 判定结果。
type Decision struct {
	Action Action
	Reason string
}

// Reevaluate 触发条件（设计 §19/§35）：先看致命信号，再看限流，最后看 ETA/吞吐。
func Reevaluate(m ExecutionMetrics) Decision {
	if m.SilentGap {
		return Decision{Action: SwitchProvider, Reason: "Validator 检测到静默缺口"}
	}
	if m.OOMCount > 0 {
		return Decision{Action: ScaleUpCloud, Reason: "发生 OOM，Cloud 资源升级"}
	}
	if m.MemoryUsage > 0.85 {
		return Decision{Action: ScaleUpCloud, Reason: "内存占用 >85%"}
	}
	if m.CircuitOpen {
		return Decision{Action: SwitchProvider, Reason: "Provider 熔断 OPEN"}
	}
	if m.HTTP503Rate > 0.2 {
		return Decision{Action: Throttle, Reason: "503 率 >20%，先降并发/冷却，不立即切换"}
	}
	if m.HTTP429Rate > 0.1 {
		return Decision{Action: Throttle, Reason: "429 率 >10%，先降并发/冷却"}
	}
	if m.TimeoutCount >= 3 || m.TimeoutRate > 0.1 {
		return Decision{Action: Retry, Reason: "连续超时，进入退避重试"}
	}
	if m.CompletedPercent < 0.95 {
		if m.CurrentETA > 60*time.Minute {
			if m.Provider == "sqd_cloud" {
				return Decision{Action: ScaleUpCloud, Reason: "Cloud ETA 超过 60 分钟，资源升级"}
			}
			return Decision{Action: ReduceRange, Reason: "ETA 超过 60 分钟，缩小 Range / 重新调度"}
		}
		if m.OriginalETA > 0 && m.CurrentETA > 2*m.OriginalETA {
			if m.Provider == "sqd_cloud" {
				return Decision{Action: ScaleUpCloud, Reason: "ETA 超过原始估算 2 倍，Cloud 资源升级"}
			}
			return Decision{Action: SwitchProvider, Reason: "ETA 超过原始估算 2 倍，切换 Provider"}
		}
		if m.RowsPerSecond > 0 && m.OriginalETA > 0 && m.CompletedPercent > 0 {
			expected := float64(0)
			// 预期速度由调用方折算：ETA 反向推导，此处仅做兜底提示
			_ = expected
		}
	}
	if m.Provider == "sqd_cloud" && m.CompletedPercent >= 0.95 && m.CurrentETA > 0 && m.CurrentETA < 5*time.Minute {
		return Decision{Action: ScaleDownCloud, Reason: "主数据阶段完成，剩余小范围补洞，Cloud 降级"}
	}
	return Decision{Action: Keep, Reason: "运行正常，保持当前调度"}
}

// ClassifyHTTPClass 简易 HTTP 错误分类（429/503/超时/其他）。
func ClassifyHTTPClass(errText string) string {
	lower := errText
	if len(lower) > 200 {
		lower = lower[:200]
	}
	for _, marker := range []string{"429", "rate limit", "too many requests", "quota"} {
		if containsFold(lower, marker) {
			return "429"
		}
	}
	for _, marker := range []string{"503", "service unavailable", "冷却"} {
		if containsFold(lower, marker) {
			return "503"
		}
	}
	for _, marker := range []string{"timeout", "timed out", "deadline"} {
		if containsFold(lower, marker) {
			return "timeout"
		}
	}
	return "other"
}

func containsFold(s, sub string) bool {
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalFold(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
