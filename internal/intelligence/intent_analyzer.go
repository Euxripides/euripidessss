package intelligence

import (
	"strings"
)

// ── Intent Analyzer（V2 设计 §2/§5）──
//
// 将自然语言调查目的 + 期望结果解析为结构化调查意图
// （方向偏好 / 目标集合 / 推断模式），供 Planner 选择任务序列。
// 规则引擎实现，无外部依赖；AI 深化由 PlannerAgent 在规划阶段注入 objective 完成。

// objRule 是关键词 → 意图目标的映射规则。
type objRule struct {
	keyword string
	goal    string
}

// IntentAnalyzer 解析调查意图。无状态，可并发。
type IntentAnalyzer struct {
	objRules []objRule
}

// NewIntentAnalyzer 创建意图分析器。
func NewIntentAnalyzer() *IntentAnalyzer {
	return &IntentAnalyzer{
		objRules: []objRule{
			// 资金去向 / 最终沉淀
			{"去向", GoalFundDestination},
			{"沉淀", GoalFundDestination},
			{"最终", GoalFundDestination},
			{"流向", GoalFundDestination},
			{"下落", GoalFundDestination},
			{"转出", GoalFundDestination},
			{"出金", GoalFundDestination},
			// 资金来源 / 上游
			{"来源", GoalFundSource},
			{"上游", GoalFundSource},
			{"入金", GoalFundSource},
			{"充值", GoalFundSource},
			{"收到", GoalFundSource},
			// 交易所入口
			{"交易所", GoalExchangeEntry},
			{"入口", GoalExchangeEntry},
			{"提现", GoalExchangeEntry},
			{"cex", GoalExchangeEntry},
			{"binance", GoalExchangeEntry},
			{"币安", GoalExchangeEntry},
			{"okx", GoalExchangeEntry},
			// 关联钱包
			{"关联", GoalRelatedWallets},
			{"钱包", GoalRelatedWallets},
			{"同伙", GoalRelatedWallets},
			{"相关地址", GoalRelatedWallets},
			{"团伙", GoalRelatedWallets},
			// 获利检测
			{"获利", GoalProfit},
			{"收益", GoalProfit},
			{"盈利", GoalProfit},
			{"盈亏", GoalProfit},
			{"利润", GoalProfit},
			// 身份线索
			{"身份", GoalIdentity},
			{"实名", GoalIdentity},
			{"标签", GoalIdentity},
			{"归属", GoalIdentity},
			// 风险扫描
			{"风险", GoalRisk},
			{"洗钱", GoalRisk},
			{"可疑", GoalRisk},
			{"非法", GoalRisk},
			// 资金流图
			{"资金流图", GoalFlowGraph},
			{"流向图", GoalFlowGraph},
			{"图谱", GoalFlowGraph},
		},
	}
}

// expectedResultGoals 是期望结果 → 意图目标的映射（精确匹配优先）。
var expectedResultGoals = []objRule{
	{"资金流图", GoalFlowGraph},
	{"流向图", GoalFlowGraph},
	{"资金去向", GoalFundDestination},
	{"资金来源", GoalFundSource},
	{"交易所入口", GoalExchangeEntry},
	{"关联钱包", GoalRelatedWallets},
	{"获利", GoalProfit},
	{"身份", GoalIdentity},
	{"风险", GoalRisk},
}

// Analyze 解析调查请求的意图。
func (a *IntentAnalyzer) Analyze(req *InvestigationRequest) *InvestigationIntent {
	intent := &InvestigationIntent{
		Direction: "unknown",
		Goals:     []string{},
		Mode:      req.Mode,
	}
	text := strings.ToLower(req.Objective + " " + strings.Join(req.ExpectedResult, " "))
	addGoal := func(g string) {
		for _, e := range intent.Goals {
			if e == g {
				return
			}
		}
		intent.Goals = append(intent.Goals, g)
	}
	// 目的关键词
	for _, r := range a.objRules {
		if strings.Contains(text, r.keyword) {
			addGoal(r.goal)
		}
	}
	// 期望结果精确映射（目的未命中时保证有目标）
	for _, r := range expectedResultGoals {
		for _, e := range req.ExpectedResult {
			if strings.Contains(strings.ToLower(e), r.keyword) {
				addGoal(r.goal)
			}
		}
	}
	// 兜底：无任何命中时按模式给默认目标
	if len(intent.Goals) == 0 {
		switch req.Mode {
		case ModeProfitAnalyze:
			addGoal(GoalProfit)
		case ModeExchangeEntry:
			addGoal(GoalExchangeEntry)
		case ModeIdentityLookup:
			addGoal(GoalIdentity)
		case ModeRiskScan:
			addGoal(GoalRisk)
		default:
			addGoal(GoalFundDestination)
			addGoal(GoalFundSource)
		}
	}

	// 方向偏好
	hasDest := hasGoal(intent.Goals, GoalFundDestination)
	hasSource := hasGoal(intent.Goals, GoalFundSource)
	switch {
	case hasDest && hasSource:
		intent.Direction = "both"
	case hasDest:
		intent.Direction = "out"
	case hasSource:
		intent.Direction = "in"
	}

	// 模式推断（auto 时）
	if req.Mode == ModeAuto || req.Mode == "" {
		intent.Mode = inferMode(intent.Goals)
	}
	intent.Summary = buildIntentSummary(req, intent)
	return intent
}

// inferMode 按意图目标优先级推断调查模式。
func inferMode(goals []string) InvestigationMode {
	switch {
	case hasGoal(goals, GoalProfit):
		return ModeProfitAnalyze
	case hasGoal(goals, GoalExchangeEntry):
		return ModeExchangeEntry
	case hasGoal(goals, GoalIdentity):
		return ModeIdentityLookup
	case hasGoal(goals, GoalRisk):
		return ModeRiskScan
	case hasGoal(goals, GoalFundDestination), hasGoal(goals, GoalFundSource), hasGoal(goals, GoalRelatedWallets):
		return ModeFundTrace
	default:
		return ModeFundTrace
	}
}

// buildIntentSummary 生成意图摘要（报告/AI 上下文用）。
func buildIntentSummary(req *InvestigationRequest, intent *InvestigationIntent) string {
	var sb strings.Builder
	if req.Objective != "" {
		sb.WriteString(req.Objective)
	}
	if len(intent.Goals) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("；")
		}
		sb.WriteString("目标：")
		sb.WriteString(strings.Join(intent.Goals, "/"))
	}
	sb.WriteString("；方向：")
	sb.WriteString(intent.Direction)
	return sb.String()
}

func hasGoal(goals []string, g string) bool {
	for _, e := range goals {
		if e == g {
			return true
		}
	}
	return false
}
