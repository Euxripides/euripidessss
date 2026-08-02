package intelligence

import (
	"testing"

	"github.com/etl/backend/internal/analyticsapi"
)

func TestFundScoreBalanceThresholds(t *testing.T) {
	cases := []struct {
		name    string
		usd     float64
		wantBal float64
	}{
		{"零余额", 0, 0},
		{"10万以下", 50_000, 0},
		{"超过10万", 200_000, 15},
		{"超过100万", 2_000_000, 30},
		{"超过1000万", 50_000_000, 50},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc := NewInvestigationScorer()
			s := sc.Compute(ScoreInput{BalanceUSD: c.usd})
			if s.FundDetail.BalancePoints != c.wantBal {
				t.Fatalf("balance_points = %.0f, want %.0f", s.FundDetail.BalancePoints, c.wantBal)
			}
		})
	}
}

func TestFundScoreProfitAndHolding(t *testing.T) {
	sc := NewInvestigationScorer()
	s := sc.Compute(ScoreInput{ProfitDetected: true, HoldingDetected: true})
	if s.FundDetail.ProfitPoints != 30 || s.FundDetail.HoldingPoints != 20 {
		t.Fatalf("获利/沉淀加分错误: %+v", s.FundDetail)
	}
	if s.Fund != 50 {
		t.Fatalf("fund = %.0f, want 50", s.Fund)
	}
	// 全部叠加应封顶 100
	s2 := sc.Compute(ScoreInput{BalanceUSD: 50_000_000, ProfitDetected: true, HoldingDetected: true})
	if s2.Fund != 100 {
		t.Fatalf("fund 应封顶 100, got %.0f", s2.Fund)
	}
}

func TestBehaviorScore(t *testing.T) {
	sc := NewInvestigationScorer()
	// 10 笔交易 ≈ 20 分；无流量时只算活跃度
	s := sc.Compute(ScoreInput{Profile: &analyticsapi.Profile{TransactionCount: 10}})
	if s.Behavior < 19 || s.Behavior > 22 {
		t.Fatalf("behavior = %.1f, want ≈20", s.Behavior)
	}
	// 大量交易 + 大流量应封顶 100
	s2 := sc.Compute(ScoreInput{Profile: &analyticsapi.Profile{
		TransactionCount: 10_000_000,
		TotalIn:          1_000_000_000,
		TotalOut:         1_000_000_000,
	}})
	if s2.Behavior != 100 {
		t.Fatalf("behavior 应封顶 100, got %.1f", s2.Behavior)
	}
}

func TestEntityAndIdentityScore(t *testing.T) {
	sc := NewInvestigationScorer()
	entities := []EntityInfo{
		{Address: "0x1", Entity: "exchange", Label: "Binance"},
		{Address: "0x2", Entity: "wallet"},
		{Address: "0x3", Entity: "contract"},
	}
	s := sc.Compute(ScoreInput{Entities: entities})
	if s.Entity != 66.7 { // 2/3 已知实体
		t.Fatalf("entity = %.1f, want 66.7", s.Entity)
	}
	if s.Identity != 31.7 { // 1/3 带标签 * 80 + 交易所 5
		t.Fatalf("identity = %.1f, want 31.7", s.Identity)
	}
}

func TestGraphScore(t *testing.T) {
	sc := NewInvestigationScorer()
	paths := []RankedPath{{Score: PathScore{Total: 80}}}
	s := sc.Compute(ScoreInput{Paths: paths})
	if s.Graph != 82 { // 80 + 路径数量加成 2
		t.Fatalf("graph = %.1f, want 82", s.Graph)
	}
	candidates := []ExpansionResult{{Address: "0x1", Score: 90}}
	s2 := sc.Compute(ScoreInput{Paths: paths, Candidates: candidates})
	if s2.Graph != 92 { // max(80,90) + 2
		t.Fatalf("graph = %.1f, want 92", s2.Graph)
	}
}

func TestComputeTotalAverage(t *testing.T) {
	sc := NewInvestigationScorer()
	// 六维各 50 → 总分 50
	s := sc.Compute(ScoreInput{
		Profile:   &analyticsapi.Profile{TransactionCount: 10, TotalIn: 100, TotalOut: 100}, // behavior≈20
		RiskScore: 50,
		Entities:  []EntityInfo{{Address: "0x1", Entity: "wallet"}}, // entity/identity=0
		Paths:     []RankedPath{{Score: PathScore{Total: 50}}},
	})
	if s.Total < 23 || s.Total > 24 {
		t.Fatalf("total = %.1f, 期望六维平均 ≈23.5（behavior≈39/risk=50/graph=52/其余 0）", s.Total)
	}
}

func TestDecisionScoresV2Dimensions(t *testing.T) {
	e := NewDecisionEngine(DefaultConfig())
	dec := e.Decide(DecideInput{
		Round:  1,
		Paths:  []RankedPath{{Score: PathScore{Total: 70}}},
		NewObs: []Observation{{Type: ObsNewAddress}, {Type: ObsRiskEvent}},
		Entities: []EntityInfo{
			{Address: "0x1", Entity: "exchange", Label: "Binance"},
			{Address: "0x2", Entity: "wallet"},
		},
		Candidates: []ExpansionResult{{Address: "0x3", Score: 85}},
	})
	if dec.Scores.BehaviorScore != 20 { // 2 条观察
		t.Fatalf("behavior_score = %.0f, want 20", dec.Scores.BehaviorScore)
	}
	if dec.Scores.GraphScore != 85 {
		t.Fatalf("graph_score = %.0f, want 85", dec.Scores.GraphScore)
	}
	if dec.Scores.IdentityScore != 50 { // 1/2 带标签
		t.Fatalf("identity_score = %.0f, want 50", dec.Scores.IdentityScore)
	}
}

func TestScoreProfileWeights(t *testing.T) {
	sc := NewInvestigationScorer()
	// fund_trace 模式：Fund 权重 0.4 → Fund 高的输入拉高总分
	base := ScoreInput{
		Profile:    &analyticsapi.Profile{TransactionCount: 1000, TotalIn: 1000000, TotalOut: 100000},
		RiskScore:  50,
		Entities:   []EntityInfo{{Address: "0x1", Entity: "exchange"}},
		Paths:      []RankedPath{{Score: PathScore{Total: 80}}},
		BalanceUSD: 5000000,
		Mode:       ModeFundTrace,
	}
	weighted := sc.Compute(base)
	base.Mode = "" // 默认平均
	plain := sc.Compute(base)
	if weighted.Total <= plain.Total {
		t.Fatalf("fund_trace 加权总分应高于平均（Fund 高权重）: weighted=%.1f plain=%.1f", weighted.Total, plain.Total)
	}
	// risk_scan 模式：Risk 高权重
	riskInput := ScoreInput{RiskScore: 90, Mode: ModeRiskScan}
	riskScore := sc.Compute(riskInput).Total
	riskInput.Mode = ""
	if sc.Compute(riskInput).Total >= riskScore {
		t.Fatalf("risk_scan 加权总分应高于平均（Risk 高权重）: %.1f", riskScore)
	}
	// 权重和应为 1.0
	for _, mode := range []InvestigationMode{ModeFundTrace, ModeRiskScan, ModeIdentityLookup} {
		w := scoreProfileWeights(mode)
		sum := 0.0
		for _, v := range w {
			sum += v
		}
		if sum < 0.99 || sum > 1.01 {
			t.Fatalf("模式 %s 权重和应为 1.0, got %v", mode, sum)
		}
	}
}
