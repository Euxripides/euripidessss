package intelligence

import (
	"math"

	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/investigationstore"
)

// ── Investigation Score（V2 六维调查价值评分，设计 §9-11）──
//
// 替代旧的 4 维（风险/实体/扩展/路径）模型：
//   Investigation Score = Fund + Behavior + Risk + Entity + Graph + Identity
// 各维 0-100，总分 = 六维归一平均（0-100），便于前端统一展示与排序。

// InvestigationScore 是六维调查价值评分。
type InvestigationScore struct {
	Total      float64          `json:"total"`    // 总分 0-100（六维平均）
	Fund       float64          `json:"fund"`     // 资金价值（余额/获利/沉淀）
	Behavior   float64          `json:"behavior"` // 行为价值（活跃度/资金规模）
	Risk       float64          `json:"risk"`     // 风险价值
	Entity     float64          `json:"entity"`   // 实体价值
	Graph      float64          `json:"graph"`    // 图价值（路径/扩展候选）
	Identity   float64          `json:"identity"` // 身份价值（标签/交易所命中）
	FundDetail *FundScoreDetail `json:"fund_detail,omitempty"`
}

// FundScoreDetail 是 Fund Score 分项明细（设计 §10）。
type FundScoreDetail struct {
	BalancePoints float64 `json:"balance_points"` // 余额价值：>1000 万 +50 / >100 万 +30
	ProfitPoints  float64 `json:"profit_points"`  // 获利检测命中 +30
	HoldingPoints float64 `json:"holding_points"` // 资金沉淀命中 +20
	Total         float64 `json:"total"`          // 合计（封顶 100）
}

// ScoreInput 是六维评分输入信号。
type ScoreInput struct {
	Profile         *analyticsapi.Profile // 地址画像（可为 nil）
	RiskScore       float64               // 风险分 0-100
	Entities        []EntityInfo          // 实体信息（实体/标签）
	Paths           []RankedPath          // 排名路径（图价值）
	Candidates      []ExpansionResult     // 扩展候选（图价值）
	BalanceUSD      float64               // 余额美元估值（无价格 oracle 时为 0）
	ProfitDetected  bool                  // 获利检测命中（PROFIT_DETECTION 任务填充）
	HoldingDetected bool                  // 资金沉淀命中
	TxCount         int64                 // 交易笔数（Profile 缺失时兜底）
	TotalInOut      int64                 // 累计流入+流出（Profile 缺失时兜底）
	Mode            InvestigationMode     // V2.1：调查模式（Score Profile 动态权重）
}

// InvestigationScorer 计算六维调查价值评分。无状态，可并发。
type InvestigationScorer struct {
	profile *investigationstore.ScoreProfileStore // 可选：持久化评分权重（V1 设计 §10）
}

// NewInvestigationScorer 创建评分器。
func NewInvestigationScorer() *InvestigationScorer { return &InvestigationScorer{} }

// SetProfileStore 注入持久化评分权重（可空，空时用内置默认权重）。
func (s *InvestigationScorer) SetProfileStore(p *investigationstore.ScoreProfileStore) {
	s.profile = p
}

// profileWeights 返回模式对应的六维权重：优先持久化配置，回退内置默认。
func (s *InvestigationScorer) profileWeights(mode InvestigationMode) map[string]float64 {
	if s != nil && s.profile != nil {
		if w := s.profile.Get(string(mode)); w != nil {
			return w
		}
	}
	return scoreProfileWeights(mode)
}

// Compute 计算六维评分。
// V2.1 Score Profile（设计 §5）：按调查模式动态加权总分，权重未定义时六维平均。
func (s *InvestigationScorer) Compute(in ScoreInput) *InvestigationScore {
	fund, detail := fundScore(in)
	behavior := behaviorScore(in)
	risk := riskScore(in)
	entity := entityScore(in.Entities)
	graph := graphScore(in.Paths, in.Candidates)
	identity := identityScore(in.Entities)

	weights := s.profileWeights(in.Mode)
	var total float64
	if weights == nil {
		total = (fund + behavior + risk + entity + graph + identity) / 6
	} else {
		total = fund*weights["fund"] + behavior*weights["behavior"] +
			risk*weights["risk"] + entity*weights["entity"] +
			graph*weights["graph"] + identity*weights["identity"]
	}
	return &InvestigationScore{
		Total:      math.Round(total*10) / 10,
		Fund:       fund,
		Behavior:   math.Round(behavior*10) / 10,
		Risk:       math.Round(risk*10) / 10,
		Entity:     math.Round(entity*10) / 10,
		Graph:      math.Round(graph*10) / 10,
		Identity:   math.Round(identity*10) / 10,
		FundDetail: detail,
	}
}

// scoreProfileWeights 返回模式对应的六维权重（V2.1 设计 §5）。
// 权重和须为 1.0；返回 nil 表示默认平均。
func scoreProfileWeights(mode InvestigationMode) map[string]float64 {
	switch mode {
	case ModeFundTrace:
		// 资金追踪：资金价值优先
		return map[string]float64{"fund": 0.4, "graph": 0.3, "entity": 0.2, "risk": 0.1, "behavior": 0, "identity": 0}
	case ModeRiskScan:
		// 风险调查：风险价值优先
		return map[string]float64{"risk": 0.4, "graph": 0.3, "entity": 0.2, "fund": 0.1, "behavior": 0, "identity": 0}
	case ModeIdentityLookup:
		// 身份调查：身份价值优先
		return map[string]float64{"identity": 0.4, "entity": 0.3, "graph": 0.2, "risk": 0.1, "fund": 0, "behavior": 0}
	default:
		return nil
	}
}

// fundScore：余额价值 + 获利检测 + 资金沉淀（设计 §10 加分制，封顶 100）。
func fundScore(in ScoreInput) (float64, *FundScoreDetail) {
	detail := &FundScoreDetail{}
	switch {
	case in.BalanceUSD >= 10_000_000:
		detail.BalancePoints = 50 // >1000 万
	case in.BalanceUSD >= 1_000_000:
		detail.BalancePoints = 30 // >100 万
	case in.BalanceUSD >= 100_000:
		detail.BalancePoints = 15 // >10 万（渐进）
	}
	if in.ProfitDetected {
		detail.ProfitPoints = 30 // 获利检测命中
	}
	if in.HoldingDetected {
		detail.HoldingPoints = 20 // 长期持有大额资产（沉淀）
	}
	detail.Total = math.Min(100, detail.BalancePoints+detail.ProfitPoints+detail.HoldingPoints)
	return detail.Total, detail
}

// behaviorScore：活跃度（交易笔数）+ 资金规模（累计流量），对数缩放 0-100。
func behaviorScore(in ScoreInput) float64 {
	tx := in.TxCount
	flow := in.TotalInOut
	if in.Profile != nil {
		tx = in.Profile.TransactionCount
		flow = in.Profile.TotalIn + in.Profile.TotalOut
	}
	activity := math.Min(60, math.Log10(float64(tx)+1)*20) // 10 笔≈20，1 万笔≈60
	scale := math.Min(40, math.Log10(float64(flow)+1)*8)   // 1 万≈32，1000 万≈56（封顶 40）
	return math.Min(100, activity+scale)
}

// riskScore：风险分（调查价值视角：风险越高越值得深入）。
func riskScore(in ScoreInput) float64 {
	r := in.RiskScore
	if in.Profile != nil && in.Profile.RiskScore > r {
		r = in.Profile.RiskScore
	}
	return math.Min(100, math.Max(0, r))
}

// entityScore：已知实体（exchange/bridge/dex/router/contract）占比 * 100。
func entityScore(entities []EntityInfo) float64 {
	if len(entities) == 0 {
		return 0
	}
	known := 0
	for _, ent := range entities {
		switch ent.Entity {
		case "exchange", "bridge", "dex", "router", "contract":
			known++
		}
	}
	return float64(known) / float64(len(entities)) * 100
}

// graphScore：Top 路径评分 / Top 扩展候选评分取高，路径数量加成。
func graphScore(paths []RankedPath, candidates []ExpansionResult) float64 {
	top := 0.0
	for _, p := range paths {
		if p.Score.Total > top {
			top = p.Score.Total
		}
	}
	exp := 0.0
	for _, c := range candidates {
		if c.Score > exp {
			exp = c.Score
		}
	}
	g := math.Max(top, exp)
	g += math.Min(20, float64(len(paths))*2) // 路径数量加成
	return math.Min(100, g)
}

// identityScore：带标签实体占比 + 交易所命中加成。
func identityScore(entities []EntityInfo) float64 {
	if len(entities) == 0 {
		return 0
	}
	labeled := 0
	exchanges := 0
	for _, ent := range entities {
		if ent.Label != "" {
			labeled++
		}
		if ent.Entity == "exchange" {
			exchanges++
		}
	}
	base := float64(labeled) / float64(len(entities)) * 80
	base += math.Min(20, float64(exchanges)*5)
	return math.Min(100, base)
}
