package intelligence

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ── Evidence Guard（设计 §12）──
//
// AI 结论必须经过数据验证才能进入报告：
//   AI Finding → Evidence Validator → 链上数据查询 → VERIFIED / REJECTED / UNVERIFIED
// 验证规则：发现引用的证据（tx_hash）必须存在于关联地址的资金流数据中。
// 计算（交易解析/金额/图）由程序完成，AI 只提供假设与解释。

// EvidenceGuard 验证 AI 发现的证据。
type EvidenceGuard struct {
	source FlowSource
}

// NewEvidenceGuard 创建证据守卫。source 可为 nil（此时所有带证据的发现标记 UNVERIFIED）。
func NewEvidenceGuard(source FlowSource) *EvidenceGuard {
	return &EvidenceGuard{source: source}
}

// Validate 验证单个 AI 发现。
func (g *EvidenceGuard) Validate(ctx context.Context, f AIFinding) VerifiedFinding {
	vf := VerifiedFinding{
		Finding:    f,
		Status:     EvidenceUnverified,
		VerifiedAt: time.Now().UTC(),
	}
	// 缺少证据或地址 → 无法验证
	if len(f.Evidence) == 0 {
		vf.Reason = "缺少证据引用（tx_hash/block）"
		return vf
	}
	if f.Address == "" {
		vf.Reason = "缺少关联地址，无法定位资金流"
		return vf
	}
	if g.source == nil {
		vf.Reason = "无数据源，证据未验证"
		return vf
	}
	flows, err := g.source.Flows(ctx, f.Address)
	if err != nil {
		vf.Reason = fmt.Sprintf("资金流查询失败: %s", sanitizeReason(err.Error()))
		return vf
	}
	// 证据必须命中至少一条资金边（按 tx_hash 匹配）
	want := map[string]bool{}
	for _, e := range f.Evidence {
		want[strings.ToLower(strings.TrimSpace(e))] = true
	}
	for _, edge := range flows {
		if want[strings.ToLower(edge.TxHash)] {
			vf.Status = EvidenceVerified
			vf.Reason = fmt.Sprintf("证据命中 %d 条交易", countEvidenceHits(want, flows))
			return vf
		}
	}
	vf.Status = EvidenceRejected
	vf.Reason = "证据未在链上数据中找到（交易哈希不存在于该地址资金流）"
	return vf
}

// ValidateBatch 批量验证发现。
func (g *EvidenceGuard) ValidateBatch(ctx context.Context, findings []AIFinding) []VerifiedFinding {
	out := make([]VerifiedFinding, 0, len(findings))
	for _, f := range findings {
		out = append(out, g.Validate(ctx, f))
	}
	return out
}

// countEvidenceHits 统计证据命中数。
func countEvidenceHits(want map[string]bool, flows []FundEdge) int {
	seen := map[string]bool{}
	for _, edge := range flows {
		hash := strings.ToLower(edge.TxHash)
		if want[hash] && !seen[hash] {
			seen[hash] = true
		}
	}
	return len(seen)
}

// sanitizeReason 剥离绝对路径等敏感信息（与 dynamicinvestigation 一致）。
func sanitizeReason(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	parts := strings.Split(s, " ")
	for i, p := range parts {
		if strings.HasPrefix(p, "E:/") || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "C:/") {
			parts[i] = "[path]"
		}
	}
	return strings.Join(parts, " ")
}
