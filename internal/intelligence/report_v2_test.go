package intelligence

import (
	"strings"
	"testing"
)

// TestReportV2Sections 验证报告包含 V2 章节（请求/评分/获利检测）。
func TestReportV2Sections(t *testing.T) {
	r := NewReportAgent(DefaultConfig())
	inv := &Investigation{
		ID:      "inv-v2",
		Target:  testAddress,
		ChainID: "bsc",
		Status:  InvestigationCompleted,
		Request: &InvestigationRequest{
			Objective:      "寻找资金沉淀",
			ExpectedResult: []string{"资金流图", "交易所入口"},
			Mode:           ModeFundTrace,
			Intent:         &InvestigationIntent{Direction: "out", Goals: []string{GoalFundDestination}, Mode: ModeFundTrace, Summary: "寻找资金沉淀；目标：fund_destination；方向：out"},
		},
		Score: &InvestigationScore{
			Total: 66.7, Fund: 50, Behavior: 40, Risk: 60, Entity: 80, Graph: 90, Identity: 80,
			FundDetail: &FundScoreDetail{BalancePoints: 30, ProfitPoints: 30, HoldingPoints: 20, Total: 80},
		},
		Profit: &ProfitReport{Detected: true, Kind: "profit", Tokens: []string{"shib"}, Summary: "检测到买卖结构", EstimateNote: "估算口径"},
		Plan:   &InvestigationPlan{Mode: ModeFundTrace, EstimatedMinutes: 8, Tasks: []PlannedTask{{Type: TaskForwardTrace, Description: "正向追踪", Priority: 1}}},
	}
	out, err := r.Generate(inv, ReportMarkdown)
	if err != nil {
		t.Fatalf("报告生成失败: %v", err)
	}
	for _, want := range []string{"调查请求", "寻找资金沉淀", "资金流图", "调查价值评分", "总分：**66.7**", "获利与沉淀检测", "买卖结构", "估算口径", "计划模式：fund_trace（预计 8 分钟）"} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("报告应包含 %q", want)
		}
	}
	// 无请求时不应出现章节
	plain, _ := r.Generate(&Investigation{ID: "inv-plain", Target: testAddress, ChainID: "bsc", Status: InvestigationCompleted}, ReportMarkdown)
	if strings.Contains(plain.Content, "调查请求") || strings.Contains(plain.Content, "调查价值评分") {
		t.Fatal("无 V2 数据时不应出现 V2 章节")
	}
}
