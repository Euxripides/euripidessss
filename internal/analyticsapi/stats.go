package analyticsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// ── 统计体系（V2.0 设计 §9/§18/§19）──
//
// 后端负责全量统计：图统计（节点/边/交易/资金流/实体/完整性）与
// 地址统计（交易/资金/Top-N 集中度/活跃度）。前端只做可见视图的
// 简单 Top-N 与完成率（设计 §18 计算边界）。

// FlowStatsScope 是统计范围声明（设计 §6.7/§10）。
type FlowStatsScope struct {
	Chain     string `json:"chain"`
	ChainID   int64  `json:"chain_id"`
	Token     string `json:"token"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

// FlowGraphStats 是图统计。
type FlowGraphStats struct {
	NodeCount int64 `json:"node_count"`
	EdgeCount int64 `json:"edge_count"`
	TxCount   int64 `json:"tx_count"`
}

// FlowFlowStats 是资金流统计。
type FlowFlowStats struct {
	TotalIn  string `json:"total_in"`
	TotalOut string `json:"total_out"`
	Net      string `json:"net"`
}

// FlowEntityStats 是实体统计。
type FlowEntityStats struct {
	Exchange int64 `json:"exchange"`
	Contract int64 `json:"contract"`
	EOA      int64 `json:"eoa"`
	Risk     int64 `json:"risk"`
}

// FlowCompleteness 是完整性声明（设计 §10/§27）。
type FlowCompleteness struct {
	Truncated bool `json:"truncated"`
	Complete  bool `json:"complete"`
}

// FlowStats 是图统计响应（设计 §19.1）。
type FlowStats struct {
	Scope        FlowStatsScope   `json:"scope"`
	Graph        FlowGraphStats   `json:"graph"`
	Flow         FlowFlowStats    `json:"flow"`
	Entities     FlowEntityStats  `json:"entities"`
	Completeness FlowCompleteness `json:"completeness"`
}

// FlowStats 计算全局图统计（全量，非截断前统计；完整性由调用方结合
// 前端可见图声明）。token 为空表示全部 Token。
func (s *Service) FlowStats(ctx context.Context, chain string, chainID int64, token string) (*FlowStats, error) {
	transferIn := fmt.Sprintf("('%s','%s','%s')", TransferTopic, TransferSingle, TransferBatch)
	tokenClause := ""
	if token != "" {
		// 安全（review blocking 修复）：token 必须为 EVM 地址（SQL 注入防护）
		if !validEVMLikeAddress(token) {
			return nil, fmt.Errorf("token 不是合法的 EVM 地址")
		}
		tokenClause = "AND address = " + quoteSQLString(strings.ToLower(token))
	}
	norm1 := fmt.Sprintf(normalizeTopic, "topic1", "topic1")
	norm2 := fmt.Sprintf(normalizeTopic, "topic2", "topic2")

	// 图/交易统计（Transfer 事件中的唯一 emitter/topic 地址 = 节点）
	graphRows, err := s.engine.ExecSQLJSON(ctx, fmt.Sprintf(`
		SELECT
			(SELECT COUNT(DISTINCT a) FROM (
				SELECT address AS a FROM read_parquet('%[1]s') WHERE topic0 IN %[2]s %[3]s
				UNION SELECT %[4]s FROM read_parquet('%[1]s') WHERE topic0 IN %[2]s %[3]s AND %[4]s != ''
				UNION SELECT %[5]s FROM read_parquet('%[1]s') WHERE topic0 IN %[2]s %[3]s AND %[5]s != ''
			)) AS node_count,
			(SELECT COUNT(*) FROM read_parquet('%[1]s') WHERE topic0 IN %[2]s %[3]s) AS tx_count,
			(SELECT COUNT(DISTINCT (address || '->' || %[4]s || '->' || %[5]s))
			 FROM read_parquet('%[1]s') WHERE topic0 IN %[2]s %[3]s AND %[4]s != '' AND %[5]s != '') AS edge_count`,
		s.parquet, transferIn, tokenClause, norm1, norm2))
	if err != nil {
		return nil, err
	}
	stats := &FlowStats{
		Scope: FlowStatsScope{Chain: chain, ChainID: chainID, Token: token},
	}
	if len(graphRows) == 1 {
		stats.Graph.NodeCount = numToInt64(graphRows[0]["node_count"])
		stats.Graph.TxCount = numToInt64(graphRows[0]["tx_count"])
		stats.Graph.EdgeCount = numToInt64(graphRows[0]["edge_count"])
	}

	// 资金流统计（从 Transfer data 解析金额，入/出按 emitter 方向）。
	// DuckDB 不支持字符串 hex→int，金额在 Go 侧解析（big.Int），
	// SQL 仅返回方向 + 原始 data。
	// 金额行截断标记（should-fix：LIMIT 静默截断需显式声明，与 AddressStats 一致）
	const flowRowLimit = 200000
	flowRows, err := s.engine.ExecSQLJSON(ctx, fmt.Sprintf(`
		SELECT %[2]s AS src, %[3]s AS dst, data
		FROM read_parquet('%[1]s') WHERE topic0 IN %[4]s %[5]s
		LIMIT %[6]d`,
		s.parquet, norm1, norm2, transferIn, tokenClause, flowRowLimit))
	if err != nil {
		// should-fix：查询失败直接返回（与 AddressStats 一致），
		// 避免前端收到 200 + 空金额 + complete=true 无法区分失败与无数据
		return nil, err
	}
	if len(flowRows) >= flowRowLimit {
		stats.Completeness.Truncated = true
	}
	var totalIn, totalOut big.Int
	for _, r := range flowRows {
		data, _ := r["data"].(string)
		amt := parseDataAmount(data)
		if amt == nil {
			continue
		}
		if src, _ := r["src"].(string); src != "" {
			totalOut.Add(&totalOut, amt)
		}
		if dst, _ := r["dst"].(string); dst != "" {
			totalIn.Add(&totalIn, amt)
		}
	}
	stats.Flow.TotalIn = totalIn.String()
	stats.Flow.TotalOut = totalOut.String()
	stats.Flow.Net = new(big.Int).Sub(&totalIn, &totalOut).String()
	// 完整性：全量计算（调用方结合前端可见图标记截断）
	stats.Completeness.Complete = true // 全量计算（截断标记由金额行 LIMIT 检查设置，不在此重置）

	// 实体统计（近似：address 列出现的唯一 Token 合约数 = contract 代理；
	// 高频接收地址数 = 风险代理，与 DashboardOverview 同口径）
	entRows, err := s.engine.ExecSQLJSON(ctx, fmt.Sprintf(`
		SELECT
			(SELECT COUNT(DISTINCT address) FROM read_parquet('%[1]s') WHERE topic0 IN %[2]s %[3]s) AS contracts,
			(SELECT COUNT(*) FROM (
				SELECT address, COUNT(*) AS c FROM read_parquet('%[1]s') WHERE topic0 IN %[2]s %[3]s GROUP BY 1 HAVING COUNT(*) >= 100
			)) AS risk`,
		s.parquet, transferIn, tokenClause))
	if err == nil && len(entRows) == 1 {
		stats.Entities.Contract = numToInt64(entRows[0]["contracts"])
		stats.Entities.Risk = numToInt64(entRows[0]["risk"])
	}
	return stats, nil
}

// AddressStats 是地址统计（设计 §9.1-§9.4）。
type AddressStats struct {
	Address string `json:"address"`

	// 基础统计（§9.1）
	TxCount          int64  `json:"tx_count"`
	InCount          int64  `json:"in_count"`
	OutCount         int64  `json:"out_count"`
	UniqueUpstream   int64  `json:"unique_upstream"`
	UniqueDownstream int64  `json:"unique_downstream"`
	ActiveDays       int64  `json:"active_days"`
	FirstSeen        string `json:"first_seen"`
	LastSeen         string `json:"last_seen"`
	AvgAmount        string `json:"avg_amount"`
	MaxAmount        string `json:"max_amount"`

	// 资金统计（§9.2）
	TotalIn       string `json:"total_in"`
	TotalOut      string `json:"total_out"`
	NetFlow       string `json:"net_flow"`
	DominantToken string `json:"dominant_token"`

	// 结构统计（§9.3，V2.0 采用 Top-N 占比）
	Top1SourceRatio float64 `json:"top1_source_ratio"`
	Top5SourceRatio float64 `json:"top5_source_ratio"`
	Top1TargetRatio float64 `json:"top1_target_ratio"`
	Top5TargetRatio float64 `json:"top5_target_ratio"`

	// 活跃度（§9.4）
	Recent24h int64 `json:"recent_24h"`
	Recent7d  int64 `json:"recent_7d"`
	Recent30d int64 `json:"recent_30d"`

	// 完整性（§27）：金额统计截断标记（LIMIT 上限）
	Truncated bool `json:"truncated"`
}

// AddressStats 计算单地址统计（从 Transfer 事件全量聚合）。
func (s *Service) AddressStats(ctx context.Context, address string, token string) (*AddressStats, error) {
	addr := strings.ToLower(address)
	key := "addrstats:" + addr + ":" + token
	if v, ok := s.cacheGet(key); ok {
		return v.(*AddressStats), nil
	}
	transferIn := fmt.Sprintf("('%s','%s','%s')", TransferTopic, TransferSingle, TransferBatch)
	tokenClause := ""
	if token != "" {
		// 安全（review blocking 修复）：token 必须为 EVM 地址（SQL 注入防护）
		if !validEVMLikeAddress(token) {
			return nil, fmt.Errorf("token 不是合法的 EVM 地址")
		}
		tokenClause = "AND address = " + quoteSQLString(strings.ToLower(token))
	}
	norm1 := fmt.Sprintf(normalizeTopic, "topic1", "topic1")
	norm2 := fmt.Sprintf(normalizeTopic, "topic2", "topic2")

	rows, err := s.engine.ExecSQLJSON(ctx, fmt.Sprintf(`
		WITH ev AS (
			SELECT address AS token, %[2]s AS src, %[3]s AS dst, data, block_time
			FROM read_parquet('%[1]s')
			WHERE (%[2]s = '%[4]s' OR %[3]s = '%[4]s') AND topic0 IN %[5]s %[6]s
		)
		SELECT
			COUNT(*) AS tx_count,
			COUNT(DISTINCT CASE WHEN dst = '%[4]s' THEN src END) AS unique_upstream,
			COUNT(DISTINCT CASE WHEN src = '%[4]s' THEN dst END) AS unique_downstream,
			COUNT(DISTINCT date_trunc('day', to_timestamp(TRY_CAST(block_time AS BIGINT)))) AS active_days,
			MIN(block_time) AS first_seen,
			MAX(block_time) AS last_seen,
			COALESCE(SUM(CASE WHEN to_timestamp(TRY_CAST(block_time AS BIGINT)) >= now() - INTERVAL 1 DAY THEN 1 ELSE 0 END), 0) AS recent_24h,
			COALESCE(SUM(CASE WHEN to_timestamp(TRY_CAST(block_time AS BIGINT)) >= now() - INTERVAL 7 DAY THEN 1 ELSE 0 END), 0) AS recent_7d,
			COALESCE(SUM(CASE WHEN to_timestamp(TRY_CAST(block_time AS BIGINT)) >= now() - INTERVAL 30 DAY THEN 1 ELSE 0 END), 0) AS recent_30d
		FROM ev`,
		s.parquet, norm1, norm2, addr, transferIn, tokenClause))
	if err != nil {
		return nil, err
	}
	st := &AddressStats{Address: addr}
	if len(rows) == 1 {
		r := rows[0]
		st.TxCount = numToInt64(r["tx_count"])
		st.UniqueUpstream = numToInt64(r["unique_upstream"])
		st.UniqueDownstream = numToInt64(r["unique_downstream"])
		st.ActiveDays = numToInt64(r["active_days"])
		st.FirstSeen = fmt.Sprintf("%v", r["first_seen"])
		st.LastSeen = fmt.Sprintf("%v", r["last_seen"])
		st.Recent24h = numToInt64(r["recent_24h"])
		st.Recent7d = numToInt64(r["recent_7d"])
		st.Recent30d = numToInt64(r["recent_30d"])
	}

	// 金额统计在 Go 侧聚合（DuckDB 不支持字符串 hex→int）：
	// 拉取该地址全部 Transfer 事件（方向 + data），big.Int 解析金额。
	// 限制 20 万行防超大地址拖垮查询。
	flowRows, err := s.engine.ExecSQLJSON(ctx, fmt.Sprintf(`
		SELECT address AS token, %[2]s AS src, %[3]s AS dst, data
		FROM read_parquet('%[1]s')
		WHERE (%[2]s = '%[4]s' OR %[3]s = '%[4]s') AND topic0 IN %[5]s %[6]s
		LIMIT 200000`,
		s.parquet, norm1, norm2, addr, transferIn, tokenClause))
	if err != nil {
		return nil, err
	}
	var totalIn, totalOut big.Int
	var maxAmt big.Int
	var inCount, outCount int64
	tokenTotals := map[string]*big.Int{}
	sourceTotals := map[string]*big.Int{}
	targetTotals := map[string]*big.Int{}
	// 截断标记（should-fix：LIMIT 200000 静默截断需显式声明，设计 §27）
	const amountRowLimit = 200000
	if len(flowRows) >= amountRowLimit {
		st.Truncated = true
	}
	for _, r := range flowRows {
		data, _ := r["data"].(string)
		amt := parseDataAmount(data)
		if amt == nil {
			continue
		}
		src, _ := r["src"].(string)
		dst, _ := r["dst"].(string)
		token, _ := r["token"].(string)
		if dst == addr {
			totalIn.Add(&totalIn, amt)
			inCount++
			if tokenTotals[token] == nil {
				tokenTotals[token] = new(big.Int)
			}
			tokenTotals[token].Add(tokenTotals[token], amt)
			if sourceTotals[src] == nil {
				sourceTotals[src] = new(big.Int)
			}
			sourceTotals[src].Add(sourceTotals[src], amt)
		}
		if src == addr {
			totalOut.Add(&totalOut, amt)
			outCount++
			if targetTotals[dst] == nil {
				targetTotals[dst] = new(big.Int)
			}
			targetTotals[dst].Add(targetTotals[dst], amt)
		}
		if amt.Cmp(&maxAmt) > 0 {
			maxAmt.Set(amt)
		}
	}
	st.InCount = inCount
	st.OutCount = outCount
	st.TotalIn = totalIn.String()
	st.TotalOut = totalOut.String()
	st.NetFlow = new(big.Int).Sub(&totalIn, &totalOut).String()
	// 平均每笔流入金额（nit 修复：分母用流入笔数，避免流出/自转事件稀释）
	if inCount > 0 {
		avg := new(big.Int).Quo(&totalIn, big.NewInt(inCount))
		st.AvgAmount = avg.String()
	}
	st.MaxAmount = maxAmt.String()
	// 主导 Token（流入最大）
	var dominantToken string
	var dominantTotal big.Int
	for token, total := range tokenTotals {
		if token != "" && total.Cmp(&dominantTotal) > 0 {
			dominantToken = token
			dominantTotal.Set(total)
		}
	}
	st.DominantToken = dominantToken
	// Top-N 来源/去向占比（Go 侧排序）
	st.Top1SourceRatio = topNRatio(sourceTotals, 1)
	st.Top5SourceRatio = topNRatio(sourceTotals, 5)
	st.Top1TargetRatio = topNRatio(targetTotals, 1)
	st.Top5TargetRatio = topNRatio(targetTotals, 5)

	s.cacheSet(key, st)
	return st, nil
}

// topNRatio 计算 Top-N 占比（N=0 表示全部）。
func topNRatio(totals map[string]*big.Int, n int) float64 {
	if len(totals) == 0 {
		return 0
	}
	values := make([]*big.Int, 0, len(totals))
	var sum big.Int
	for _, v := range totals {
		values = append(values, v)
		sum.Add(&sum, v)
	}
	// 降序排序
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j].Cmp(values[j-1]) > 0; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	if n <= 0 || n > len(values) {
		n = len(values)
	}
	var top big.Int
	for i := 0; i < n; i++ {
		top.Add(&top, values[i])
	}
	return ratioOf(&top, &sum)
}

// ── 工具 ──

// toBigInt 解析字符串为 big.Int（空/非法返回 0）。
func toBigInt(s string) *big.Int {
	s = strings.TrimSpace(s)
	if s == "" {
		return new(big.Int)
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return new(big.Int)
	}
	return n
}

// numToInt64 安全转换 DuckDB JSON 数值（float64 / string 均可）。
func numToInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

// parseDataAmount 从 Transfer 事件 data 解析金额（尾部 32 字节，hex）。
// data 格式：0x + 64 hex（前 32 字节 tokenId/index，后 32 字节金额）。
// 返回 nil 表示不可解析（非 Transfer 或格式异常）。
func parseDataAmount(data string) *big.Int {
	data = strings.TrimSpace(data)
	hexPart := strings.TrimPrefix(data, "0x")
	if len(hexPart) > 64 {
		hexPart = hexPart[len(hexPart)-64:] // 尾部 32 字节
	}
	if len(hexPart) == 0 {
		return nil
	}
	n, ok := new(big.Int).SetString(hexPart, 16)
	if !ok {
		return nil
	}
	return n
}

// ratioOf 计算占比（0-1，保留 4 位）。
func ratioOf(part, total *big.Int) float64 {
	if total == nil || total.Sign() <= 0 {
		return 0
	}
	partF := new(big.Float).SetPrec(64).SetInt(part)
	totalF := new(big.Float).SetPrec(64).SetInt(total)
	partF.Quo(partF, totalF)
	f, _ := partF.Float64()
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return float64(int64(f*10000)) / 10000
}
