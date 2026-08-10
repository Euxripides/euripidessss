package financialintegration

import (
	"context"
	"fmt"
)

type InvestigationFacade struct{ analytics FinancialAnalytics }

func NewInvestigationFacade(analytics FinancialAnalytics) *InvestigationFacade {
	return &InvestigationFacade{analytics: analytics}
}

func (f *InvestigationFacade) FinancialSummary(ctx context.Context, query FinancialQuery) (any, error) {
	if err := validateFinancialQuery(query); err != nil {
		return nil, err
	}
	if f == nil || f.analytics == nil {
		return nil, ErrQueryFailed
	}
	return f.analytics.FinancialSummary(ctx, query)
}

func (f *InvestigationFacade) Retention(ctx context.Context, query FinancialQuery) (any, error) {
	if err := validateFinancialQuery(query); err != nil {
		return nil, err
	}
	if f == nil || f.analytics == nil {
		return nil, ErrQueryFailed
	}
	return f.analytics.Retention(ctx, query)
}

func (f *InvestigationFacade) PassThrough(ctx context.Context, query FinancialQuery) (any, error) {
	if err := validateFinancialQuery(query); err != nil {
		return nil, err
	}
	if f == nil || f.analytics == nil {
		return nil, ErrQueryFailed
	}
	return f.analytics.PassThrough(ctx, query)
}

func (f *InvestigationFacade) PnL(ctx context.Context, query FinancialQuery) (any, error) {
	if err := validateFinancialQuery(query); err != nil {
		return nil, err
	}
	if f == nil || f.analytics == nil {
		return nil, ErrQueryFailed
	}
	return f.analytics.PnL(ctx, query)
}

// Snapshot is intentionally explicit rather than a free-form planner query:
// all four algorithms receive the same address, token and time boundary.
func (f *InvestigationFacade) Snapshot(ctx context.Context, query FinancialQuery) (InvestigationSnapshot, error) {
	if err := validateFinancialQuery(query); err != nil {
		return InvestigationSnapshot{}, err
	}
	if f == nil || f.analytics == nil {
		return InvestigationSnapshot{}, ErrQueryFailed
	}
	out := InvestigationSnapshot{Query: query}
	var err error
	if out.Summary, err = f.analytics.FinancialSummary(ctx, query); err != nil {
		return InvestigationSnapshot{}, fmt.Errorf("financial summary: %w", err)
	}
	if out.Retention, err = f.analytics.Retention(ctx, query); err != nil {
		return InvestigationSnapshot{}, fmt.Errorf("retention: %w", err)
	}
	if out.PassThrough, err = f.analytics.PassThrough(ctx, query); err != nil {
		return InvestigationSnapshot{}, fmt.Errorf("pass-through: %w", err)
	}
	if out.PnL, err = f.analytics.PnL(ctx, query); err != nil {
		return InvestigationSnapshot{}, fmt.Errorf("pnl: %w", err)
	}
	return out, nil
}

func validateFinancialQuery(query FinancialQuery) error {
	_, _, _, _, to, err := validateCommon(query.ChainID, query.Address, query.From, query.To, query.TokenAddress, "0")
	if err != nil {
		return err
	}
	if !query.AsOf.IsZero() && query.AsOf.UTC().After(to) {
		return fmt.Errorf("%w: as_of must not exceed to", ErrInvalidInput)
	}
	return nil
}
