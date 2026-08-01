package dynamicinvestigation

import "testing"

// ── Expansion Score 评分器测试 ──

func TestAmountScore(t *testing.T) {
	cases := []struct {
		amount string
		want   float64
	}{
		{"", 0},
		{"0", 0},
		{"0x0", 0},
		{"500", 0},        // < 1K
		{"5000", 10},      // ≥ 1K
		{"200000", 25},    // ≥ 100K
		{"5000000", 40},   // ≥ 1M
		{"50000000", 60},  // ≥ 10M
		{"500000000", 80}, // ≥ 100M
		{"2000000000", 100}, // ≥ 1B
		{"0x989680", 60}, // 0x989680 = 10,000,000 → 档 60
	}
	for _, c := range cases {
		got := amountScore(c.amount)
		if got != c.want {
			t.Fatalf("amount %s: want %v, got %v", c.amount, c.want, got)
		}
	}
}

func TestScoreBasic(t *testing.T) {
	cfg := DefaultConfig()

	// 大金额 + 高风险 + 高关联 + 高活跃 → 高评分，ACQUIRE
	r := Score(ScoreInput{
		Address:       "0x1",
		Entity:        EntityWallet,
		Amount:        "100000000", // ≥100M → 80
		RiskScore:     90,
		RelationScore: 0.9,
		TxCount:       5000,
		Degree:        100,
	}, cfg)
	if r.Decision != DecisionAcquire {
		t.Fatalf("高分地址应 ACQUIRE, got %s (%s)", r.Decision, r.Reason)
	}
	if r.Score < 60 {
		t.Fatalf("高分地址评分应 ≥60, got %v", r.Score)
	}

	// 低价值：无金额无交易 → IGNORE
	r2 := Score(ScoreInput{Address: "0x2"}, cfg)
	if r2.Decision != DecisionIgnore {
		t.Fatalf("无数据地址应 IGNORE, got %s", r2.Decision)
	}
}

func TestScoreEntityPenalty(t *testing.T) {
	cfg := DefaultConfig()
	base := Score(ScoreInput{
		Address:       "0x1",
		Entity:        EntityWallet,
		Amount:        "10000000",
		RiskScore:     80,
		RelationScore: 0.8,
		TxCount:       1000,
	}, cfg)

	exch := Score(ScoreInput{
		Address:       "0x1",
		Entity:        EntityExchange,
		Amount:        "10000000",
		RiskScore:     80,
		RelationScore: 0.8,
		TxCount:       1000,
	}, cfg)

	// 交易所实体应有惩罚（评分更低）
	if exch.Score >= base.Score {
		t.Fatalf("交易所实体应受惩罚: base=%v exch=%v", base.Score, exch.Score)
	}
	if _, ok := exch.Breakdown["entity_penalty"]; !ok {
		t.Fatal("评分分项应包含 entity_penalty")
	}
	if exch.Breakdown["entity_penalty"] <= 0 {
		t.Fatalf("交易所惩罚分项应 > 0, got %v", exch.Breakdown["entity_penalty"])
	}
}

func TestScoreMinThreshold(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinScore = 50
	// 中等分数（≈40-50 区间）→ HOLD 或 ACQUIRE 取决于数值
	r := Score(ScoreInput{
		Address:       "0x3",
		Entity:        EntityWallet,
		Amount:        "500000", // ≥100K → 25
		RiskScore:     40,
		RelationScore: 0.5,
		TxCount:       100,
	}, cfg)
	if r.Decision != DecisionHold && r.Decision != DecisionAcquire {
		t.Fatalf("中等分数决策异常: %s", r.Decision)
	}
}

// ── 路由测试 ──

func TestRouteWalletSQD(t *testing.T) {
	cfg := DefaultConfig()
	// 普通钱包 → SQD 增量，等级逐级升级
	r := Route(RouteInput{
		Entity: EntityWallet, Decision: DecisionAcquire,
		CurrentLevel: LevelDiscover, Depth: 1,
	}, cfg)
	if r.Mode != AcquisitionSQDLogs {
		t.Fatalf("钱包应走 SQD_LOGS, got %s", r.Mode)
	}
	if r.TargetLevel != LevelLogs {
		t.Fatalf("钱包目标等级应为 Level 1 Logs, got %v", r.TargetLevel)
	}
}

func TestRouteExchangeCSVDirect(t *testing.T) {
	cfg := DefaultConfig()
	// 大型实体（交易所）→ CSV 直链
	r := Route(RouteInput{
		Entity: EntityExchange, Decision: DecisionAcquire,
		CurrentLevel: LevelDiscover, Depth: 1,
	}, cfg)
	if r.Mode != AcquisitionCSVDirect {
		t.Fatalf("交易所应走 CSV_DIRECT, got %s", r.Mode)
	}
	if r.TargetLevel != LevelTransfer {
		t.Fatalf("CSV 直链目标等级应为 Level 2 Transfer, got %v", r.TargetLevel)
	}

	// CSV 未启用 → 退回 SQD
	cfg2 := DefaultConfig()
	cfg2.UseCSVDirect = false
	r2 := Route(RouteInput{
		Entity: EntityExchange, Decision: DecisionAcquire,
		CurrentLevel: LevelDiscover, Depth: 1,
	}, cfg2)
	if r2.Mode != AcquisitionSQDLogs {
		t.Fatalf("CSV 禁用时应退回 SQD_LOGS, got %s", r2.Mode)
	}
}

func TestRouteIgnoreRelationsOnly(t *testing.T) {
	cfg := DefaultConfig()
	r := Route(RouteInput{
		Entity: EntityWallet, Decision: DecisionIgnore,
		CurrentLevel: LevelDiscover, Depth: 1,
	}, cfg)
	if r.Mode != AcquisitionRelationsOnly {
		t.Fatalf("忽略地址应仅保存关系, got %s", r.Mode)
	}
}

func TestRouteContract(t *testing.T) {
	cfg := DefaultConfig()
	// 低分合约 → 仅保存关系
	r := Route(RouteInput{
		Entity: EntityContract, Decision: DecisionHold,
		Score: 20, CurrentLevel: LevelDiscover, Depth: 1,
	}, cfg)
	if r.Mode != AcquisitionRelationsOnly {
		t.Fatalf("低分合约应仅保存关系, got %s", r.Mode)
	}
	// 高分合约 → SQD Logs
	r2 := Route(RouteInput{
		Entity: EntityContract, Decision: DecisionAcquire,
		Score: 80, CurrentLevel: LevelDiscover, Depth: 1,
	}, cfg)
	if r2.Mode != AcquisitionSQDLogs {
		t.Fatalf("高分合约应采集 Logs, got %s", r2.Mode)
	}
}

func TestShouldUpgrade(t *testing.T) {
	if !ShouldUpgrade(LevelDiscover, LevelLogs) {
		t.Fatal("0→1 应可升级")
	}
	if ShouldUpgrade(LevelTrace, LevelTrace) {
		t.Fatal("已达上限不应升级")
	}
	if ShouldUpgrade(LevelLogs, LevelDiscover) {
		t.Fatal("降级不应允许")
	}
}

// ── 实体识别测试 ──

func TestRecognizeKnownEntity(t *testing.T) {
	r := NewRecognizer()
	r.AddKnown(KnownEntity{
		Address: "0xbinancehot",
		Entity:  EntityExchange,
		Label:   "Binance Hot Wallet",
	})
	entity, label := r.Recognize(EntityHints{Address: "0xBINANCEHOT", TxCount: 10})
	if entity != EntityExchange {
		t.Fatalf("已知实体应识别为 exchange, got %s", entity)
	}
	if label != "Binance Hot Wallet" {
		t.Fatalf("标签错误: %s", label)
	}
}

func TestRecognizePatterns(t *testing.T) {
	r := NewRecognizer()
	// 归集地址：入多出少
	entity, _ := r.Recognize(EntityHints{Address: "0xa", InCount: 50, OutCount: 3, TxCount: 60})
	if entity != EntityExchange {
		t.Fatalf("归集地址应识别为 exchange, got %s", entity)
	}
	// 中转枢纽
	entity2, _ := r.Recognize(EntityHints{Address: "0xb", InCount: 20, OutCount: 25, TxCount: 45})
	if entity2 != EntityRouter {
		t.Fatalf("中转枢纽应识别为 router, got %s", entity2)
	}
	// 合约
	entity3, _ := r.Recognize(EntityHints{Address: "0xc", IsContract: true, TxCount: 5})
	if entity3 != EntityContract {
		t.Fatalf("合约应识别为 contract, got %s", entity3)
	}
	// 普通钱包
	entity4, _ := r.Recognize(EntityHints{Address: "0xd", TxCount: 5, InCount: 2, OutCount: 1})
	if entity4 != EntityWallet {
		t.Fatalf("普通钱包应识别为 wallet, got %s", entity4)
	}
	// 无数据
	entity5, _ := r.Recognize(EntityHints{Address: "0xe"})
	if entity5 != EntityUnknown {
		t.Fatalf("无数据应识别为 unknown, got %s", entity5)
	}
}
