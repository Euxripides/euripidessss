package intelligence

import (
	"testing"
	"time"
)

func TestDecisionStopCodes(t *testing.T) {
	cfg := DefaultConfig()
	e := NewDecisionEngine(cfg)

	// 预算限制：最大轮次
	dec := e.Decide(DecideInput{Round: cfg.MaxRounds + 1})
	if dec.StopCode != StopBudgetLimit {
		t.Fatalf("轮次上限应 BUDGET_LIMIT, got %s", dec.StopCode)
	}
	// 预算限制：运行时间
	dec = e.Decide(DecideInput{Round: 1, Elapsed: time.Duration(cfg.MaxRuntimeMS+1) * time.Millisecond})
	if dec.StopCode != StopBudgetLimit {
		t.Fatalf("超时应 BUDGET_LIMIT, got %s", dec.StopCode)
	}
	// 无新发现
	dec = e.Decide(DecideInput{Round: 2, Memory: &InvestigationMemory{}})
	if dec.StopCode != StopNoValue {
		t.Fatalf("无新发现应 NO_VALUE, got %s", dec.StopCode)
	}
	// 目标达成：候选均为交易所
	dec = e.Decide(DecideInput{
		Round:      1,
		Memory:     &InvestigationMemory{DiscoveredAt: map[string]time.Time{}},
		Candidates: []ExpansionResult{{Address: testAddress, Entity: "exchange", Score: 80}},
	})
	if dec.StopCode != StopTargetFound {
		t.Fatalf("交易所候选应 TARGET_FOUND, got %s", dec.StopCode)
	}
	// 低置信度：候选低于门槛
	dec = e.Decide(DecideInput{
		Round:      1,
		Memory:     &InvestigationMemory{DiscoveredAt: map[string]time.Time{}},
		Candidates: []ExpansionResult{{Address: testAddress, Entity: "wallet", Score: 10}},
	})
	if dec.StopCode != StopLowConf {
		t.Fatalf("低价值候选应 LOW_CONFIDENCE, got %s", dec.StopCode)
	}
}
