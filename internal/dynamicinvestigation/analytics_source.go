package dynamicinvestigation

import (
	"context"
	"fmt"

	"github.com/etl/backend/internal/analyticsapi"
)

// ── 真实分析信号源 ──
//
// AnalyticsSource 将现有 analyticsapi.Service（基于 DuckDB/Parquet 数据资产）
// 适配为引擎的 DiscoverySource：Flows（资金流对手）+ Profile（画像/风险/合约）。

// AnalyticsSource 是基于 analyticsapi.Service 的真实数据源。
type AnalyticsSource struct {
	svc *analyticsapi.Service
}

// NewAnalyticsSource 创建适配器。svc 为 nil 时返回可用的空源（信号为零值）。
func NewAnalyticsSource(svc *analyticsapi.Service) *AnalyticsSource {
	return &AnalyticsSource{svc: svc}
}

// Flows 查询地址资金流（incoming/outgoing 对手与金额）。
func (s *AnalyticsSource) Flows(ctx context.Context, address string) ([]FlowSignal, error) {
	if s.svc == nil {
		return nil, nil
	}
	edges, err := s.svc.Flows(ctx, address, "")
	if err != nil {
		return nil, fmt.Errorf("analytics Flows 查询失败: %w", err)
	}
	out := make([]FlowSignal, 0, len(edges))
	for _, e := range edges {
		out = append(out, FlowSignal{
			Counterparty: e.Counterparty,
			Token:        e.Token,
			Amount:       e.Amount,
			Direction:    e.Direction,
		})
	}
	return out, nil
}

// Profile 查询地址画像信号。
func (s *AnalyticsSource) Profile(ctx context.Context, address string) (*ProfileSignal, error) {
	if s.svc == nil {
		return &ProfileSignal{}, nil
	}
	p, err := s.svc.Profile(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("analytics Profile 查询失败: %w", err)
	}
	risk, err := s.svc.Risk(ctx, address)
	riskScore := 0.0
	if err == nil && risk != nil {
		riskScore = risk.RiskScore
	}
	signal := &ProfileSignal{
		IsContract: p.ContractCount > 0,
		TxCount:    p.TransactionCount,
		InCount:    p.TotalIn,
		OutCount:   p.TotalOut,
		RiskScore:  riskScore,
		Degree:     int(p.TotalIn + p.TotalOut),
	}
	return signal, nil
}
