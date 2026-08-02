package intelligence

import (
	"errors"
	"strings"
	"time"
)

// ── 调查请求（V2 Investigation Request）──
//
// 用户输入模型：目标地址 + 链 + 调查目的 + 期望结果 + 调查模式。
// 请求先于调查创建并持久化（RequestStore），调查启动后回填 investigation_id。
// 对应设计文档 §3/§4：Investigation ├── Request ├── Plan ├── Tasks └── Results。

// 请求校验错误。
var (
	ErrInvalidAddress   = errors.New("目标地址不是合法的 EVM 地址")
	ErrEmptyRequest     = errors.New("调查目的与期望结果至少提供一项")
	ErrInvalidMode      = errors.New("调查模式不合法")
	ErrObjectiveTooLong = errors.New("调查目的超过 500 字符上限")
)

// InvestigationMode 是调查模式（决定规划方向）。
type InvestigationMode string

const (
	ModeAuto           InvestigationMode = "auto"            // 自动推断（默认）
	ModeFundTrace      InvestigationMode = "fund_trace"      // 资金追踪：去向/来源/沉淀
	ModeProfitAnalyze  InvestigationMode = "profit_analyze"  // 获利分析：买卖对账/获利检测
	ModeExchangeEntry  InvestigationMode = "exchange_entry"  // 交易所入口识别
	ModeIdentityLookup InvestigationMode = "identity_lookup" // 身份线索查找
	ModeRiskScan       InvestigationMode = "risk_scan"       // 风险扫描
)

// ValidModes 是合法调查模式集合。
var ValidModes = map[InvestigationMode]bool{
	ModeAuto:           true,
	ModeFundTrace:      true,
	ModeProfitAnalyze:  true,
	ModeExchangeEntry:  true,
	ModeIdentityLookup: true,
	ModeRiskScan:       true,
}

// 调查请求状态。
const (
	RequestCreated  = "created"  // 已创建，等待/已启动调查
	RequestStarted  = "started"  // 调查已启动
	RequestFinished = "finished" // 调查完成
	RequestFailed   = "failed"   // 调查失败
)

// InvestigationRequest 是一次调查请求（用户输入 + 意图分析结果）。
type InvestigationRequest struct {
	ID              string               `json:"id"`
	InvestigationID string               `json:"investigation_id,omitempty"` // 关联的调查 ID
	Address         string               `json:"address"`                    // 目标地址（EVM 校验后，小写）
	ChainID         string               `json:"chain_id"`
	Objective       string               `json:"objective"`        // 调查目的（自然语言）
	ExpectedResult  []string             `json:"expected_result"`  // 期望结果列表
	Mode            InvestigationMode    `json:"mode"`             // 调查模式
	Intent          *InvestigationIntent `json:"intent,omitempty"` // Intent Analyzer 输出
	Status          string               `json:"status"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

// ── 调查意图（Intent Analyzer 输出）──

// 意图目标类型（决定 Planner 任务选择）。
const (
	GoalFundDestination = "fund_destination" // 资金去向/最终沉淀
	GoalFundSource      = "fund_source"      // 资金来源/上游
	GoalExchangeEntry   = "exchange_entry"   // 交易所入口
	GoalRelatedWallets  = "related_wallets"  // 关联钱包
	GoalProfit          = "profit"           // 获利检测
	GoalIdentity        = "identity"         // 身份线索
	GoalRisk            = "risk"             // 风险扫描
	GoalFlowGraph       = "flow_graph"       // 资金流图
)

// InvestigationIntent 是 Intent Analyzer 输出：结构化调查意图。
type InvestigationIntent struct {
	Direction string            `json:"direction"` // in/out/both/unknown — 资金方向偏好
	Goals     []string          `json:"goals"`     // Goal* 目标集合
	Mode      InvestigationMode `json:"mode"`      // 推断出的调查模式
	Summary   string            `json:"summary"`   // 意图摘要（报告/AI 上下文用）
}

// ValidateInvestigationRequest 校验请求输入：
// 地址必须合法；目的与期望结果至少提供一项；模式必须合法（空按 auto）。
// 返回规范化后的 address/chainID/mode 与错误。
func ValidateInvestigationRequest(address, chainID, objective string, expectedResult []string, mode string) (string, string, InvestigationMode, error) {
	address = strings.ToLower(strings.TrimSpace(address))
	if !validEVMAddress(address) {
		return "", "", "", ErrInvalidAddress
	}
	objective = strings.TrimSpace(objective)
	// MEDIUM：Objective 长度限制（防提示注入构造长段落；前端 maxLength 同步 500）
	if len([]rune(objective)) > 500 {
		return "", "", "", ErrObjectiveTooLong
	}
	hasExpected := false
	for i := range expectedResult {
		expectedResult[i] = strings.TrimSpace(expectedResult[i])
		if expectedResult[i] != "" {
			hasExpected = true
		}
	}
	if objective == "" && !hasExpected {
		return "", "", "", ErrEmptyRequest
	}
	if chainID == "" {
		chainID = "bsc"
	}
	m := InvestigationMode(strings.ToLower(strings.TrimSpace(mode)))
	if m == "" {
		m = ModeAuto
	}
	if !ValidModes[m] {
		return "", "", "", ErrInvalidMode
	}
	return address, chainID, m, nil
}
