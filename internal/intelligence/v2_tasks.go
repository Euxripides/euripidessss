package intelligence

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ── V2 任务执行器（设计 §6/§7：BALANCE / TOKEN / PROFIT / EXCHANGE / CLUSTER / IDENTITY）──
//
// 全部复用现有数据源信号（analyticsapi.Profile/Flows、entityResolver），
// 不新增外部依赖；缺数据源时返回 errSkipped 保持调查降级。

// executeBalanceAnalysis 余额与资产规模分析（复用 Profile 画像信号）。
func executeBalanceAnalysis(ctx context.Context, a *InvestigationAgent, target string) (string, error) {
	if a.svc == nil {
		return "", errSkipped("无画像数据源")
	}
	profile, err := a.svc.Profile(ctx, target)
	if err != nil {
		return "", err
	}
	if profile == nil {
		return "无画像数据", nil
	}
	return fmt.Sprintf("交易 %d 笔 / Token %d 种 / 累计流入 %d / 累计流出 %d / 风险分 %.0f",
		profile.TransactionCount, profile.TokenCount, profile.TotalIn, profile.TotalOut, profile.RiskScore), nil
}

// executeTokenAnalysis Token 持仓与分布分析（复用资金流聚合，按 Token 汇总）。
func executeTokenAnalysis(ctx context.Context, snap agentSnapshot, target string) (string, error) {
	if snap.flowSource == nil {
		return "", errSkipped("无资金流数据源")
	}
	flows, err := snap.flowSource.Flows(ctx, target)
	if err != nil {
		return "", err
	}
	type tokenStat struct {
		token  string
		edges  int
		inAmt  float64
		outAmt float64
	}
	stats := map[string]*tokenStat{}
	for _, e := range flows {
		tok := e.Token
		if tok == "" {
			tok = "NATIVE"
		}
		st, ok := stats[tok]
		if !ok {
			st = &tokenStat{token: tok}
			stats[tok] = st
		}
		st.edges++
		if amt, ok := parseAmountFloat(e.Amount); ok {
			if strings.EqualFold(e.From, target) {
				st.outAmt += amt
			} else {
				st.inAmt += amt
			}
		}
	}
	if len(stats) == 0 {
		return "无 Token 数据", nil
	}
	list := make([]*tokenStat, 0, len(stats))
	for _, st := range stats {
		list = append(list, st)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].edges > list[j].edges })
	top := list
	if len(top) > 5 {
		top = top[:5]
	}
	var parts []string
	for _, st := range top {
		parts = append(parts, fmt.Sprintf("%s(%d 笔)", st.token, st.edges))
	}
	return fmt.Sprintf("共 %d 种 Token，Top：%s", len(stats), strings.Join(parts, " / ")), nil
}

// executeProfitDetection 获利/沉淀检测（设计 §10 简化启发式）：
// - 沉淀（holding）：稳定币净流入显著且极少流出；
// - 获利结构（profit）：非稳定币 Token 先流入后流出（买卖对账）。
// 无价格 oracle，输出为结构性判断并标注估算口径。
func executeProfitDetection(ctx context.Context, a *InvestigationAgent, snap agentSnapshot, target string, inv *Investigation) (string, error) {
	if snap.flowSource == nil {
		return "", errSkipped("无资金流数据源")
	}
	flows, err := snap.flowSource.Flows(ctx, target)
	if err != nil {
		return "", err
	}
	report := detectProfitStructure(target, flows)
	if report.Detected {
		a.setField(inv, func(i *Investigation) { i.Profit = report })
	}
	return report.Summary, nil
}

// stableTokens 是稳定币集合（沉淀检测用）。
var stableTokens = map[string]bool{
	"usdt": true, "usdc": true, "dai": true, "busd": true, "usd": true, "tusd": true,
}

// detectProfitStructure 结构性获利/沉淀检测（V2.1：纯函数，可单测）。
// 输出估算金额（稳定币净额）、可信度（依据权重，无 oracle 封顶 0.85）与依据明细。
func detectProfitStructure(target string, flows []FundEdge) *ProfitReport {
	report := &ProfitReport{
		EstimateNote: "无链上历史价格数据：基于买卖对账与沉淀结构的启发式判断（估算口径）；稳定币部分按净额估算，非稳定币部分缺少历史价格",
	}
	type acc struct {
		inAmt  float64
		outAmt float64
		inN    int
		outN   int
		inTs   []int64 // 流入时间戳（时间窗口匹配用）
		outTs  []int64
	}
	accs := map[string]*acc{}
	for _, e := range flows {
		tok := strings.ToLower(e.Token)
		if tok == "" {
			tok = "native"
		}
		ac, ok := accs[tok]
		if !ok {
			ac = &acc{}
			accs[tok] = ac
		}
		amt, ok := parseAmountFloat(e.Amount)
		if !ok {
			continue
		}
		if strings.EqualFold(e.From, target) {
			ac.outAmt += amt
			ac.outN++
			if e.Timestamp > 0 {
				ac.outTs = append(ac.outTs, e.Timestamp)
			}
		} else {
			ac.inAmt += amt
			ac.inN++
			if e.Timestamp > 0 {
				ac.inTs = append(ac.inTs, e.Timestamp)
			}
		}
	}
	var holdings, profits []string
	stableNet := 0.0 // 稳定币净流入（沉淀估算基准）
	for tok, ac := range accs {
		switch {
		case stableTokens[tok] && ac.inAmt > 0 && ac.outN > 0 && ac.outAmt < ac.inAmt*0.1 && ac.inN >= 2:
			// 稳定币大量流入且几乎不流出 → 沉淀
			holdings = append(holdings, tok)
			stableNet += ac.inAmt - ac.outAmt
		case !stableTokens[tok] && ac.inN > 0 && ac.outN > 0 && ac.outAmt >= ac.inAmt*0.5:
			// 非稳定币先买后卖（卖出量 ≥ 买入量一半）→ 买卖对账结构
			profits = append(profits, tok)
		}
	}
	sort.Strings(holdings)
	sort.Strings(profits)
	var kinds []string
	if len(profits) > 0 {
		kinds = append(kinds, "profit")
		report.Tokens = append(report.Tokens, profits...)
	}
	if len(holdings) > 0 {
		kinds = append(kinds, "holding")
		report.Tokens = append(report.Tokens, holdings...)
	}
	if len(kinds) == 0 {
		report.Summary = "未检测到明显获利或沉淀结构（按买卖对账与沉淀启发式）"
		report.Confidence = 0
		return report
	}
	report.Detected = true
	report.Kind = strings.Join(kinds, "+")

	// ── V2.1：估算金额（稳定币净额）──
	if len(holdings) > 0 && stableNet > 0 {
		report.EstimateUSD = stableNet
	}

	// ── V2.1：依据明细与可信度 ──
	hasIn := len(profits) > 0 || len(holdings) > 0
	checklist := []ProfitChecklistItem{
		{OK: hasIn, Present: true, Label: "Token 流入"},
	}
	hasOut := false
	for _, tok := range profits {
		if ac, ok := accs[tok]; ok && ac.outN > 0 {
			hasOut = true
			break
		}
	}
	for _, tok := range holdings {
		if ac, ok := accs[tok]; ok && ac.outN > 0 {
			hasOut = true
			break
		}
	}
	checklist = append(checklist, ProfitChecklistItem{OK: hasOut, Present: true, Label: "Token 流出"})
	// 时间窗口匹配：买卖对账 token 的流入/流出时间差中位数 ≤ 30 天
	timeMatch := false
	for _, tok := range profits {
		ac := accs[tok]
		if len(ac.inTs) == 0 || len(ac.outTs) == 0 {
			continue
		}
		timeMatch = timeWindowMatched(ac.inTs, ac.outTs)
		if timeMatch {
			break
		}
	}
	checklist = append(checklist, ProfitChecklistItem{OK: timeMatch, Present: len(profits) > 0, Label: "时间窗口匹配"})
	checklist = append(checklist, ProfitChecklistItem{OK: false, Present: false, Label: "缺少历史价格"})
	report.Checklist = checklist

	// 可信度：基础 0.5 + 依据权重，无价格 oracle 封顶 0.85
	conf := 0.5
	if hasIn {
		conf += 0.15
	}
	if hasOut {
		conf += 0.1
	}
	if timeMatch {
		conf += 0.1
	}
	if report.EstimateUSD > 0 {
		conf += 0.1 // 稳定币沉淀金额可估，提升可信度
	}
	if conf > 0.85 {
		conf = 0.85
	}
	report.Confidence = round2(conf)

	var parts []string
	if len(profits) > 0 {
		parts = append(parts, fmt.Sprintf("Token %s 存在买入后卖出结构", strings.Join(profits, "/")))
	}
	if len(holdings) > 0 {
		parts = append(parts, fmt.Sprintf("稳定币 %s 大量流入且极少流出（沉淀）", strings.Join(holdings, "/")))
	}
	if report.EstimateUSD > 0 {
		parts = append(parts, fmt.Sprintf("估算沉淀金额 %.0f（稳定币净额）", report.EstimateUSD))
	}
	report.Summary = strings.Join(parts, "；")
	return report
}

// timeWindowMatched 判断两组时间戳是否在 30 天窗口内匹配（中位数差）。
func timeWindowMatched(inTs, outTs []int64) bool {
	if len(inTs) == 0 || len(outTs) == 0 {
		return false
	}
	minIn, maxIn := minMax(inTs)
	minOut, maxOut := minMax(outTs)
	// 区间重叠或相邻（任一端点在对方区间 ±30 天内）
	const window = 30 * 24 * 3600
	return minIn-window <= maxOut && minOut-window <= maxIn
}

func minMax(ts []int64) (min, max int64) {
	min, max = ts[0], ts[0]
	for _, v := range ts[1:] {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	return min, max
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// executeForwardTrace / executeBackwardTrace 单方向资金追踪（去向/来源）。
func executeDirectionTrace(ctx context.Context, snap agentSnapshot, target string, plan *InvestigationPlan, st *roundState, outgoing bool) (string, error) {
	if snap.tracer == nil {
		return "", errSkipped("无追踪器")
	}
	paths, err := snap.tracer.TraceDirection(ctx, target, plan.MaxHops, plan.BeamWidth, outgoing)
	if err != nil {
		return "", err
	}
	st.newPaths = append(st.newPaths, paths...)
	dir := "正向"
	if !outgoing {
		dir = "反向"
	}
	return fmt.Sprintf("%s追踪发现 %d 条候选路径", dir, len(paths)), nil
}

// executeExchangeDetection 交易所入口识别（对手方实体过滤 exchange）。
// 性能保护：对手方按出现频次取 Top 200（大地址数千对手方时避免逐地址画像查询）。
func executeExchangeDetection(ctx context.Context, a *InvestigationAgent, snap agentSnapshot, target string, inv *Investigation, st *roundState) (string, error) {
	if snap.flowSource == nil {
		return "", errSkipped("无资金流数据源")
	}
	flows, err := snap.flowSource.Flows(ctx, target)
	if err != nil {
		return "", err
	}
	freq := map[string]int{}
	var order []string
	for _, e := range flows {
		cp := e.From
		if strings.EqualFold(e.From, target) {
			cp = e.To
		}
		cp = strings.ToLower(strings.TrimSpace(cp))
		if cp == "" || !validEVMAddress(cp) {
			continue
		}
		if freq[cp] == 0 {
			order = append(order, cp)
		}
		freq[cp]++
	}
	// 按频次降序取 Top 200
	sort.Slice(order, func(i, j int) bool { return freq[order[i]] > freq[order[j]] })
	if len(order) > 200 {
		order = order[:200]
	}
	infos := a.resolveNewEntities(ctx, order, inv.Entities)
	st.newEntities = append(st.newEntities, infos...)
	var names []string
	for _, inf := range infos {
		if inf.Entity == "exchange" {
			label := inf.Label
			if label == "" {
				label = shortAddr(inf.Address)
			}
			names = append(names, label)
		}
	}
	if len(names) == 0 {
		return fmt.Sprintf("对手方 %d 个，未发现交易所入口", len(order)), nil
	}
	return fmt.Sprintf("对手方 %d 个，发现 %d 个交易所入口：%s", len(order), len(names), strings.Join(names, " / ")), nil
}

// executeEntityCluster 实体聚类（按实体类型归并已知地址）。
func executeEntityCluster(ctx context.Context, a *InvestigationAgent, inv *Investigation) (string, error) {
	clusters := map[string][]string{}
	for _, e := range inv.Entities {
		entity := e.Entity
		if entity == "" {
			entity = "unknown"
		}
		clusters[entity] = append(clusters[entity], shortAddr(e.Address))
	}
	if len(clusters) == 0 {
		return "暂无实体数据，跳过聚类", nil
	}
	var keys []string
	for k := range clusters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s(%d)", k, len(clusters[k])))
	}
	return fmt.Sprintf("实体聚类 %d 类：%s", len(clusters), strings.Join(parts, " / ")), nil
}

// executeIdentityLookup 身份线索查找（带标签实体）。
func executeIdentityLookup(ctx context.Context, a *InvestigationAgent, inv *Investigation) (string, error) {
	var labels []string
	for _, e := range inv.Entities {
		if e.Label != "" {
			labels = append(labels, fmt.Sprintf("%s(%s)", e.Label, shortAddr(e.Address)))
		}
	}
	if len(labels) == 0 {
		return "未发现身份标签线索", nil
	}
	if len(labels) > 5 {
		labels = labels[:5]
	}
	return fmt.Sprintf("身份线索 %d 条：%s", len(labels), strings.Join(labels, " / ")), nil
}

// executeFlowGraph 资金流图构建（图规模摘要）。
func executeFlowGraph(ctx context.Context, snap agentSnapshot, target string) (string, error) {
	if snap.flowSource == nil {
		return "", errSkipped("无资金流数据源")
	}
	flows, err := snap.flowSource.Flows(ctx, target)
	if err != nil {
		return "", err
	}
	if len(flows) == 0 {
		return "无资金流数据，图为空", nil
	}
	nodes := map[string]bool{strings.ToLower(target): true}
	inN, outN := 0, 0
	for _, e := range flows {
		nodes[strings.ToLower(e.From)] = true
		nodes[strings.ToLower(e.To)] = true
		if strings.EqualFold(e.From, target) {
			outN++
		} else {
			inN++
		}
	}
	return fmt.Sprintf("资金流图：%d 节点 / %d 条边（入 %d / 出 %d）", len(nodes), len(flows), inN, outN), nil
}
