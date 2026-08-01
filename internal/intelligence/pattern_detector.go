package intelligence

import (
	"sort"
	"strings"
	"time"
)

// ── Risk Pattern Detector ──
//
// 检测模式：快速转移 / 多地址拆分 / 归集 / 大额进入 / 快速清空。

// PatternDetector 检测风险模式。
type PatternDetector struct {
	cfg IntelligenceConfig
}

// NewPatternDetector 创建检测器。
func NewPatternDetector(cfg IntelligenceConfig) *PatternDetector {
	return &PatternDetector{cfg: cfg}
}

// Detect 从资金边中检测风险模式。
func (d *PatternDetector) Detect(address string, edges []FundEdge) []RiskPattern {
	address = strings.ToLower(address)
	var patterns []RiskPattern

	// 按方向分组
	var incoming, outgoing []FundEdge
	for _, e := range edges {
		if strings.ToLower(e.To) == address {
			incoming = append(incoming, e)
		}
		if strings.ToLower(e.From) == address {
			outgoing = append(outgoing, e)
		}
	}

	// 1. 快速转移：收到资金后短时间转出（< 1 小时内转出 ≥ 50% 收到额）
	if len(incoming) > 0 && len(outgoing) > 0 {
		latestIn := maxTimestamp(incoming)
		for _, out := range outgoing {
			if out.Timestamp >= latestIn && out.Timestamp-latestIn <= 3600 {
				outAmt, _ := parseAmountFloat(out.Amount)
				ratio := amountRatio(outAmt, sumAmount(incoming))
				if ratio >= 0.5 {
					patterns = append(patterns, RiskPattern{
						Type:       PatternRapidTransfer,
						Address:    address,
						Severity:   "high",
						Detail:     "收到资金后 1 小时内转出 ≥50%（快速转移）",
						Edges:      []FundEdge{out},
						DetectedAt: time.Now().UTC(),
					})
					break
				}
			}
		}
	}

	// 2. 多地址拆分：单笔大额进入后分散到 ≥3 个对手
	if len(incoming) >= 1 && len(outgoing) >= 3 {
		large := maxAmount(incoming)
		if large > 0 {
			totalOut := sumAmount(outgoing)
			if totalOut >= large*0.6 {
				patterns = append(patterns, RiskPattern{
					Type:       PatternMultiSplit,
					Address:    address,
					Severity:   "high",
					Detail:     "大额进入后分散到多个地址（多地址拆分）",
					Edges:      outgoing[:minInt(len(outgoing), 5)],
					DetectedAt: time.Now().UTC(),
				})
			}
		}
	}

	// 3. 归集：≥3 个地址转入且转出远少于转入
	if len(incoming) >= 3 {
		totalIn := sumAmount(incoming)
		totalOut := sumAmount(outgoing)
		if totalIn > 0 && totalOut < totalIn*0.2 {
			patterns = append(patterns, RiskPattern{
				Type:       PatternConcentration,
				Address:    address,
				Severity:   "medium",
				Detail:     "多地址归集（转入 ≥3 来源且转出 <20%）",
				Edges:      incoming[:minInt(len(incoming), 5)],
				DetectedAt: time.Now().UTC(),
			})
		}
	}

	// 4. 大额进入：单笔 ≥ 全部进入的 P90
	if len(incoming) >= 5 {
		sorted := append([]float64(nil), amounts(incoming)...)
		sort.Float64s(sorted)
		threshold := sorted[int(float64(len(sorted)-1)*0.9)]
		for _, e := range incoming {
			if f, ok := parseAmountFloat(e.Amount); ok && f >= threshold && f > 0 {
				patterns = append(patterns, RiskPattern{
					Type:       PatternLargeInflow,
					Address:    address,
					Severity:   "medium",
					Detail:     "大额资金进入（Top 10% 分位）",
					Edges:      []FundEdge{e},
					DetectedAt: time.Now().UTC(),
				})
				break
			}
		}
	}

	// 5. 快速清空：大额进入后余额大幅流出
	if len(incoming) >= 1 && len(outgoing) >= 1 {
		totalIn := sumAmount(incoming)
		totalOut := sumAmount(outgoing)
		if totalIn > 0 && totalOut >= totalIn*0.8 {
			patterns = append(patterns, RiskPattern{
				Type:       PatternRapidDrain,
				Address:    address,
				Severity:   "high",
				Detail:     "资金快速清空（流出 ≥80% 进入额）",
				Edges:      outgoing[:minInt(len(outgoing), 5)],
				DetectedAt: time.Now().UTC(),
			})
		}
	}

	return patterns
}

// ── 工具 ──

func amounts(edges []FundEdge) []float64 {
	out := make([]float64, 0, len(edges))
	for _, e := range edges {
		if f, ok := parseAmountFloat(e.Amount); ok {
			out = append(out, f)
		}
	}
	return out
}

func sumAmount(edges []FundEdge) float64 {
	var sum float64
	for _, e := range edges {
		if f, ok := parseAmountFloat(e.Amount); ok {
			sum += f
		}
	}
	return sum
}

func maxAmount(edges []FundEdge) float64 {
	var max float64
	for _, e := range edges {
		if f, ok := parseAmountFloat(e.Amount); ok && f > max {
			max = f
		}
	}
	return max
}

func maxTimestamp(edges []FundEdge) int64 {
	var max int64
	for _, e := range edges {
		if e.Timestamp > max {
			max = e.Timestamp
		}
	}
	return max
}

func amountRatio(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return a / b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
