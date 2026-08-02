// Package analyticsapi 提供基于 Parquet 数据资产的业务查询 API：
// 地址画像 / 资金流 / 资金路径 / 风险评分 / 批量画像。
package analyticsapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/logger"
)

const (
	TransferTopic    = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	TransferSingle   = "0xc3d58168c5ae7397731d063d5bbf3d657854427343f4c083240f7aacaa2d0f62"
	TransferBatch    = "0x4a39dc06d4c0dbc64b70af90fd698a233a518aa5d07e595d983b8c0526c8f7fb"
	transferFilter   = "topic0 IN ('" + TransferTopic + "','" + TransferSingle + "','" + TransferBatch + "')"
	normalizeTopic   = `CASE WHEN length(%[1]s) = 66 THEN '0x' || substr(%[1]s, 27) ELSE %[1]s END`
)

// quoteSQLString 转义 SQL 单引号字符串字面量。
func quoteSQLString(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}

// validEVMLikeAddress 校验 EVM 地址格式（0x + 40 hex，大小写均可）。
func validEVMLikeAddress(addr string) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if len(addr) != 42 || !strings.HasPrefix(addr, "0x") {
		return false
	}
	for _, c := range addr[2:] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// Profile 是地址画像响应。
type Profile struct {
	Address           string  `json:"address"`
	FirstActivityTime string  `json:"first_activity_time"`
	LastActivityTime  string  `json:"last_activity_time"`
	EventCount        int64   `json:"event_count"`
	TransactionCount  int64   `json:"transaction_count"`
	ContractCount     int64   `json:"contract_count"`
	TokenCount        int64   `json:"token_count"`
	TotalIn           int64   `json:"total_in"`
	TotalOut          int64   `json:"total_out"`
	ActiveDays        int64   `json:"active_days"`
	RiskScore         float64 `json:"risk_score"`
}

// FlowEdge 是一条资金流边。
type FlowEdge struct {
	Direction    string `json:"direction"`
	Token        string `json:"token"`
	Counterparty string `json:"counterparty"`
	Amount       string `json:"amount"`
	Block        string `json:"block"`
	TxHash       string `json:"tx_hash"`
}

// PathItem 是一条两跳路径。
type PathItem struct {
	A      string `json:"a"`
	B      string `json:"b"`
	C      string `json:"c"`
	Token  string `json:"token"`
	Amount string `json:"amount"`
}

// Risk 是风险评分响应。
type Risk struct {
	RiskScore          float64 `json:"risk_score"`
	RiskLevel          string  `json:"risk_level"`
	RiskReason         string  `json:"risk_reason"`
	TransactionFreq    float64 `json:"transaction_frequency"`
	TopHolderRatio     float64 `json:"top_holder_ratio"`
	CounterpartyScore  float64 `json:"shared_counterparty_score"`
}

// Service 提供分析查询服务。
type Service struct {
	engine *duckdb.Engine
	parquet string // forward-slash parquet 路径

	mu          sync.Mutex
	cache       map[string]any
	cacheHits   int64
	cacheMisses int64

	// 全局风险基座（惰性计算一次）
	holderRatio float64
	counterScore float64
	globalOnce  sync.Once
}

// New 创建分析服务。
func New(engine *duckdb.Engine, parquetPath string) *Service {
	return &Service{
		engine:  engine,
		parquet: strings.ReplaceAll(parquetPath, "\\", "/"),
		cache:   make(map[string]any),
	}
}

// CacheStats 返回缓存统计。
type CacheStats struct {
	Hits   int64 `json:"hits"`
	Misses int64 `json:"misses"`
}

// CacheHits 返回缓存命中/未命中计数。
func (s *Service) CacheStats() CacheStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return CacheStats{Hits: s.cacheHits, Misses: s.cacheMisses}
}

func (s *Service) cacheGet(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.cache[key]
	if ok {
		s.cacheHits++
	} else {
		s.cacheMisses++
	}
	return v, ok
}

func (s *Service) cacheSet(key string, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[key] = v
}

// ── 查询实现 ──

func (s *Service) profileSQL(addr string) string {
	norm1 := fmt.Sprintf(normalizeTopic, "topic1", "topic1")
	norm2 := fmt.Sprintf(normalizeTopic, "topic2", "topic2")
	return fmt.Sprintf(`
WITH all_events AS (
	SELECT address AS addr, address AS emitter, block_time, transaction_hash, topic0, %[1]s AS topic1, %[2]s AS topic2, 1 AS is_emitter,
		CASE WHEN topic0 IN ('%[3]s','%[4]s','%[5]s') THEN 1 ELSE 0 END AS is_transfer
	FROM read_parquet('%[6]s')
	WHERE address = '%[7]s'
	UNION ALL
	SELECT %[1]s, address, block_time, transaction_hash, topic0, %[1]s, %[2]s, 0,
		CASE WHEN topic0 IN ('%[3]s','%[4]s','%[5]s') THEN 1 ELSE 0 END
	FROM read_parquet('%[6]s')
	WHERE %[1]s = '%[7]s'
	UNION ALL
	SELECT %[2]s, address, block_time, transaction_hash, topic0, %[1]s, %[2]s, 0,
		CASE WHEN topic0 IN ('%[3]s','%[4]s','%[5]s') THEN 1 ELSE 0 END
	FROM read_parquet('%[6]s')
	WHERE %[2]s = '%[7]s'
)
SELECT addr AS address,
	to_timestamp(TRY_CAST(min(block_time) AS UBIGINT))::VARCHAR AS first_activity_time,
	to_timestamp(TRY_CAST(max(block_time) AS UBIGINT))::VARCHAR AS last_activity_time,
	COUNT(*) AS event_count,
	COUNT(DISTINCT transaction_hash) AS transaction_count,
	COUNT(DISTINCT CASE WHEN is_emitter = 1 THEN transaction_hash END) AS contract_count,
	COUNT(DISTINCT CASE WHEN is_transfer = 1 THEN emitter END) AS token_count,
	COUNT(DISTINCT CASE WHEN addr = topic2 THEN transaction_hash END) AS total_in,
	COUNT(DISTINCT CASE WHEN addr = topic1 THEN transaction_hash END) AS total_out,
	COUNT(DISTINCT to_timestamp(TRY_CAST(block_time AS UBIGINT))::DATE) AS active_days
FROM all_events GROUP BY addr`, norm1, norm2, TransferTopic, TransferSingle, TransferBatch, s.parquet, strings.ToLower(addr))
}

// Profile 查询单地址画像。
func (s *Service) Profile(ctx context.Context, address string) (*Profile, error) {
	key := "profile:" + strings.ToLower(address)
	if v, ok := s.cacheGet(key); ok {
		return v.(*Profile), nil
	}
	rows, err := s.engine.ExecSQLJSON(ctx, s.profileSQL(address))
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		p := &Profile{Address: strings.ToLower(address)}
		s.cacheSet(key, p)
		return p, nil
	}
	r := rows[0]
	p := &Profile{
		Address:           fmt.Sprintf("%v", r["address"]),
		FirstActivityTime: fmt.Sprintf("%v", r["first_activity_time"]),
		LastActivityTime:  fmt.Sprintf("%v", r["last_activity_time"]),
		EventCount:        int64(r["event_count"].(float64)),
		TransactionCount:  int64(r["transaction_count"].(float64)),
		ContractCount:     int64(r["contract_count"].(float64)),
		TokenCount:        int64(r["token_count"].(float64)),
		TotalIn:           int64(r["total_in"].(float64)),
		TotalOut:          int64(r["total_out"].(float64)),
		ActiveDays:        int64(r["active_days"].(float64)),
	}
	p.RiskScore = s.riskScoreOf(p)
	s.cacheSet(key, p)
	return p, nil
}

// Flows 查询单地址资金流（incoming/outgoing）。
func (s *Service) Flows(ctx context.Context, address string, token string) ([]FlowEdge, error) {
	addr := strings.ToLower(address)
	key := "flows:" + addr + ":" + token
	if v, ok := s.cacheGet(key); ok {
		return v.([]FlowEdge), nil
	}
	tokenClause := ""
	if token != "" {
		// quoteSQLString 转义：token 来自 URL query，未做格式校验，防止 SQL 注入
		tokenClause = "AND address = " + quoteSQLString(strings.ToLower(token))
	}
	norm1 := fmt.Sprintf(normalizeTopic, "topic1", "topic1")
	norm2 := fmt.Sprintf(normalizeTopic, "topic2", "topic2")
	sqlText := fmt.Sprintf(`SELECT 'outgoing' AS direction, address AS token, %[8]s AS counterparty, data, block_number, transaction_hash
		FROM read_parquet('%[2]s') WHERE %[1]s = '%[3]s' AND topic0 IN ('%[4]s','%[5]s','%[6]s') %[7]s
		UNION ALL
		SELECT 'incoming', address, %[1]s, data, block_number, transaction_hash
		FROM read_parquet('%[2]s') WHERE %[8]s = '%[3]s' AND topic0 IN ('%[4]s','%[5]s','%[6]s') %[7]s`, norm1, s.parquet, addr, TransferTopic, TransferSingle, TransferBatch, tokenClause, norm2)
	rows, err := s.engine.ExecSQLJSON(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	edges := make([]FlowEdge, 0, len(rows))
	for _, r := range rows {
		e := FlowEdge{
			Direction:    fmt.Sprintf("%v", r["direction"]),
			Token:        fmt.Sprintf("%v", r["token"]),
			Counterparty: strings.ToLower(fmt.Sprintf("%v", r["counterparty"])),
			Block:        fmt.Sprintf("%v", r["block_number"]),
			TxHash:       fmt.Sprintf("%v", r["transaction_hash"]),
		}
		if d := fmt.Sprintf("%v", r["data"]); len(d) >= 3 {
			hexPart := strings.TrimPrefix(strings.ToLower(d), "0x")
			if len(hexPart) > 64 {
				hexPart = hexPart[len(hexPart)-64:]
			}
			if n, ok := new(big.Int).SetString(hexPart, 16); ok {
				e.Amount = n.String()
			}
		}
		edges = append(edges, e)
	}
	s.cacheSet(key, edges)
	return edges, nil
}

// Path 查询两跳资金路径（A→B→C）。
func (s *Service) Path(ctx context.Context, address string) ([]PathItem, error) {
	addr := strings.ToLower(address)
	key := "path:" + addr
	if v, ok := s.cacheGet(key); ok {
		return v.([]PathItem), nil
	}
	// 取该地址出发的边（全部，构建图）
	norm1 := fmt.Sprintf(normalizeTopic, "topic1", "topic1")
	norm2 := fmt.Sprintf(normalizeTopic, "topic2", "topic2")
	sqlText := fmt.Sprintf(`SELECT %[1]s AS f, %[2]s AS t, address AS token, data
		FROM read_parquet('%[3]s') WHERE topic0 IN ('%[4]s','%[5]s','%[6]s')`, norm1, norm2, s.parquet, TransferTopic, TransferSingle, TransferBatch)
	rows, err := s.engine.ExecSQLJSON(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	adj := map[string][]struct{ to, token, amount string }{}
	for _, r := range rows {
		f := strings.ToLower(fmt.Sprintf("%v", r["f"]))
		to := strings.ToLower(fmt.Sprintf("%v", r["t"]))
		if f == to {
			continue
		}
		amount := ""
		if d := fmt.Sprintf("%v", r["data"]); len(d) >= 3 {
			hexPart := strings.TrimPrefix(strings.ToLower(d), "0x")
			if len(hexPart) > 64 {
				hexPart = hexPart[len(hexPart)-64:]
			}
			if n, ok := new(big.Int).SetString(hexPart, 16); ok {
				amount = n.String()
			}
		}
		adj[f] = append(adj[f], struct{ to, token, amount string }{to, fmt.Sprintf("%v", r["token"]), amount})
	}
	var paths []PathItem
	for _, hop1 := range adj[addr] {
		for _, hop2 := range adj[hop1.to] {
			if hop2.to == addr || hop2.to == hop1.to {
				continue // 无自环
			}
			paths = append(paths, PathItem{A: addr, B: hop1.to, C: hop2.to, Token: hop2.token, Amount: hop2.amount})
			if len(paths) >= 20 {
				break
			}
		}
		if len(paths) >= 20 {
			break
		}
	}
	s.cacheSet(key, paths)
	return paths, nil
}

// Risk 查询单地址风险评分。
func (s *Service) Risk(ctx context.Context, address string) (*Risk, error) {
	addr := strings.ToLower(address)
	key := "risk:" + addr
	if v, ok := s.cacheGet(key); ok {
		return v.(*Risk), nil
	}
	p, err := s.Profile(ctx, addr)
	if err != nil {
		return nil, err
	}
	r := s.computeRisk(p)
	s.cacheSet(key, r)
	return r, nil
}

// normalizeAddresses 规范化地址集：小写 + trim + 排序（供 batchWantSQL 与 batchCacheKey 共用，
// 保证截断语义一致——先排序再截断，避免同键不同结果）。
func normalizeAddresses(addresses []string) []string {
	out := make([]string, 0, len(addresses))
	for _, a := range addresses {
		out = append(out, strings.ToLower(strings.TrimSpace(a)))
	}
	sort.Strings(out)
	return out
}

// batchWantSQL 构造批量画像的 want 表子查询。
// 优先 addr_file CSV（命令行短，避免 Windows 32K 命令行长度限制），否则内联请求体 addresses（VALUES）；
// 两者都空返回错误——禁止用空路径 read_csv（DuckDB CLI 会把 '' 当作标准输入，导致结果集爆炸 OOM）。
func batchWantSQL(addresses []string, addrFile string) (string, error) {
	if strings.TrimSpace(addrFile) != "" {
		af := strings.ReplaceAll(addrFile, "\\", "/")
		return fmt.Sprintf("SELECT addr FROM read_csv(%s, header=false, columns={'addr':'VARCHAR'})", quoteSQLString(af)), nil
	}
	const maxBatch = 500 // VALUES 内联受命令行长度限制（约 48 字符/地址，500≈24KB 安全）
	norm := normalizeAddresses(addresses)
	if len(norm) > 0 {
		if len(norm) > maxBatch {
			norm = norm[:maxBatch]
		}
		items := make([]string, 0, len(norm))
		for _, addr := range norm {
			if len(addr) > 64 {
				continue // 跳过超长非地址值（防命令行长度 DoS）
			}
			items = append(items, "("+quoteSQLString(addr)+")")
		}
		if len(items) == 0 {
			return "", errors.New("batch profiles: no valid addresses")
		}
		return "SELECT * FROM (VALUES " + strings.Join(items, ",") + ") AS want(addr)", nil
	}
	return "", errors.New("batch profiles: addresses or addr_file required")
}

// BatchProfiles 批量查询画像（SEMI JOIN 一次 SQL，结果按地址集哈希缓存）。
func (s *Service) BatchProfiles(ctx context.Context, addresses []string, addrFile string) ([]Profile, error) {
	key := batchCacheKey(addresses, addrFile)
	if v, ok := s.cacheGet(key); ok {
		return append([]Profile(nil), v.([]Profile)...), nil
	}
	wantSQL, err := batchWantSQL(addresses, addrFile)
	if err != nil {
		return nil, err
	}
	sqlText := fmt.Sprintf(`WITH want AS (%[1]s)
		SELECT a.addr AS address, COUNT(*) AS event_count
		FROM want a
		LEFT JOIN read_parquet('%[2]s') t ON t.address = a.addr
		GROUP BY 1 ORDER BY 2 DESC`, wantSQL, s.parquet)
	rows, err := s.engine.ExecSQLJSON(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	profiles := make([]Profile, 0, len(rows))
	for _, r := range rows {
		profiles = append(profiles, Profile{
			Address:    fmt.Sprintf("%v", r["address"]),
			EventCount: int64(r["event_count"].(float64)),
		})
	}
	s.cacheSetBatch(key, profiles)
	return profiles, nil
}

// maxCachedBatches 批量结果缓存上限（防内存无限增长）。
const maxCachedBatches = 64

// cacheSetBatch 写入批量缓存；超出上限时整体清空（简单 LRU 替代）。
func (s *Service) cacheSetBatch(key string, v []Profile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) >= maxCachedBatches && s.cache[key] == nil {
		s.cache = map[string]any{key: v} // 冷启动清空
		return
	}
	s.cache[key] = v
}

// batchCacheKey 计算批量请求的缓存键（地址集排序哈希 + addr_file 路径+修改时间+大小）。
// 与 batchWantSQL 截断语义保持一致（前 maxBatch 个地址），避免不同入参产生同结果异键。
func batchCacheKey(addresses []string, addrFile string) string {
	h := sha256.New()
	if strings.TrimSpace(addrFile) != "" {
		// 混入文件版本信息：同一路径改写后不返回陈旧画像
		_, _ = io.WriteString(h, "F:"+strings.TrimSpace(addrFile))
		if info, err := os.Stat(addrFile); err == nil {
			_, _ = fmt.Fprintf(h, "|mtime:%d|size:%d", info.ModTime().UnixNano(), info.Size())
		}
	}
	const maxBatch = 500 // 与 batchWantSQL 截断一致
	norm := normalizeAddresses(addresses)
	if len(norm) > maxBatch {
		norm = norm[:maxBatch]
	}
	for _, a := range norm {
		// 0x00 分隔符防拼接碰撞（["ab","cd"] vs ["abc","d"]）
		_, _ = io.WriteString(h, "\x00"+a)
	}
	return "batch:" + hex.EncodeToString(h.Sum(nil))
}

// ── 风险评分 ──

func (s *Service) riskScoreOf(p *Profile) float64 {
	return s.computeRisk(p).RiskScore
}

func (s *Service) computeRisk(p *Profile) *Risk {
	s.globalOnce.Do(func() {
		s.holderRatio, s.counterScore = s.globalRiskBasis()
	})
	freq := 0.0
	if p.ActiveDays > 0 {
		freq = float64(p.TransactionCount) / float64(p.ActiveDays)
	}
	freqNorm := freq / 100
	if freqNorm > 1 {
		freqNorm = 1
	}
	score := 100*(0.6*freqNorm+0.4*s.holderRatio) + 10*s.counterScore
	if score > 100 {
		score = 100
	}
	level := "低"
	reason := "交易频率低、无显著集中关联"
	if score >= 60 {
		level = "高"
		reason = "高频交易或高集中度关联"
	} else if score >= 30 {
		level = "中"
		reason = "交易频率中等"
	}
	return &Risk{
		RiskScore:         round2(score),
		RiskLevel:         level,
		RiskReason:        reason,
		TransactionFreq:   round2(freq),
		TopHolderRatio:    round2(s.holderRatio),
		CounterpartyScore: round2(s.counterScore),
	}
}

// globalRiskBasis 预计算全局风险基座（Top10 接收占比 + 共同对手 Jaccard）。
func (s *Service) globalRiskBasis() (float64, float64) {
	norm2 := fmt.Sprintf(normalizeTopic, "topic2", "topic2")
	sqlText := fmt.Sprintf(`SELECT %[1]s AS to_addr, COUNT(*) AS n FROM read_parquet('%[2]s')
		WHERE topic0 IN ('%[3]s','%[4]s','%[5]s') GROUP BY 1 ORDER BY 2 DESC LIMIT 10`, norm2, s.parquet, TransferTopic, TransferSingle, TransferBatch)
	rows, err := s.engine.ExecSQLJSON(context.Background(), sqlText)
	if err != nil || len(rows) == 0 {
		return 0, 0
	}
	var top10, total int
	_ = top10
	for _, r := range rows {
		top10 += int(r["n"].(float64))
	}
	countRows, err := s.engine.ExecSQLJSON(context.Background(),
		fmt.Sprintf("SELECT COUNT(*) AS n FROM read_parquet('%s') WHERE topic0 IN ('%s','%s','%s')", s.parquet, TransferTopic, TransferSingle, TransferBatch))
	if err == nil && len(countRows) == 1 {
		total = int(countRows[0]["n"].(float64))
	}
	holder := 0.0
	if total > 0 {
		holder = float64(top10) / float64(total)
	}
	// 共同对手 Jaccard（Top5 发送者）
	norm1 := fmt.Sprintf(normalizeTopic, "topic1", "topic1")
	sqlOut := fmt.Sprintf(`SELECT %[1]s AS f, %[2]s AS t FROM read_parquet('%[3]s')
		WHERE topic0 IN ('%[4]s','%[5]s','%[6]s')`, norm1, norm2, s.parquet, TransferTopic, TransferSingle, TransferBatch)
	outRows, err := s.engine.ExecSQLJSON(context.Background(), sqlOut)
	if err != nil {
		return holder, 0
	}
	counter := map[string]map[string]bool{}
	for _, r := range outRows {
		f := strings.ToLower(fmt.Sprintf("%v", r["f"]))
		t := strings.ToLower(fmt.Sprintf("%v", r["t"]))
		if counter[f] == nil {
			counter[f] = map[string]bool{}
		}
		counter[f][t] = true
	}
	type kv struct {
		addr string
		n    int
	}
	var list []kv
	for addr, set := range counter {
		list = append(list, kv{addr, len(set)})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	pairs, sum := 0, 0.0
	for i := 0; i < len(list) && i < 5; i++ {
		for j := i + 1; j < len(list) && j < 5; j++ {
			inter, union := 0, 0
			for k := range counter[list[i].addr] {
				if counter[list[j].addr][k] {
					inter++
				}
			}
			union = len(counter[list[i].addr]) + len(counter[list[j].addr]) - inter
			if union > 0 {
				sum += float64(inter) / float64(union)
				pairs++
			}
		}
	}
	if pairs > 0 {
		return holder, sum / float64(pairs)
	}
	return holder, 0
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// ── HTTP Handler ──

// Handler 是 analyticsapi 的 HTTP 入口。
type Handler struct {
	service *Service
	// allowedDataRoot 允许 addr_file 读取的根目录（数据仓库上级），防任意文件读取。
	allowedDataRoot string
}

// NewHandler 创建 HTTP handler。
func NewHandler(engine *duckdb.Engine, parquetPath string) *Handler {
	// 数据根目录 = parquet 仓库目录上一级（…/data_root/warehouse/logs.parquet）
	root := filepath.Dir(filepath.Dir(parquetPath))
	return &Handler{service: New(engine, parquetPath), allowedDataRoot: root}
}

// Service 暴露 Service（测试用）。
func (h *Handler) Service() *Service { return h.service }

// Dashboard 返回数据资产概览（地址/Token/交易/风险 + 时间趋势）。
type Dashboard struct {
	AddressCount   int64            `json:"address_count"`
	TokenCount     int64            `json:"token_count"`
	TransactionCount int64          `json:"transaction_count"`
	TransferCount  int64            `json:"transfer_count"`
	RiskAddresses  int64            `json:"risk_addresses"`
	Trend          []TrendPoint     `json:"trend"`
}

// TrendPoint 是时间趋势点。
type TrendPoint struct {
	Block  string `json:"block"`
	Events int64  `json:"events"`
}

// DashboardOverview 返回概览统计。
func (s *Service) DashboardOverview(ctx context.Context) (*Dashboard, error) {
	transferIn := fmt.Sprintf("('%s','%s','%s')", TransferTopic, TransferSingle, TransferBatch)
	rows, err := s.engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT
			(SELECT COUNT(DISTINCT address) FROM read_parquet('%s')) AS tokens,
			(SELECT COUNT(*) FROM read_parquet('%s')) AS transactions,
			(SELECT COUNT(*) FROM read_parquet('%s') WHERE topic0 IN %s) AS transfers`,
		s.parquet, s.parquet, s.parquet, transferIn))
	if err != nil {
		return nil, err
	}
	d := &Dashboard{}
	if len(rows) == 1 {
		d.TokenCount = int64(rows[0]["tokens"].(float64))
		d.TransactionCount = int64(rows[0]["transactions"].(float64))
		d.TransferCount = int64(rows[0]["transfers"].(float64))
	}
	// 地址数（emitter + topic1 + topic2 归一化去重）
	norm1 := fmt.Sprintf(normalizeTopic, "topic1", "topic1")
	norm2 := fmt.Sprintf(normalizeTopic, "topic2", "topic2")
	addrRows, err := s.engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT COUNT(*) AS n FROM (
			SELECT address AS a FROM read_parquet('%s')
			UNION SELECT %[2]s FROM read_parquet('%[1]s') WHERE %[2]s != ''
			UNION SELECT %[3]s FROM read_parquet('%[1]s') WHERE %[3]s != ''
		)`, s.parquet, norm1, norm2))
	if err == nil && len(addrRows) == 1 {
		d.AddressCount = int64(addrRows[0]["n"].(float64))
	}
	// 时间趋势（按 block 分桶 10 段）
	trendRows, err := s.engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT TRY_CAST(block_number AS UBIGINT)/1000 AS bucket, COUNT(*) AS n
		 FROM read_parquet('%s') GROUP BY 1 ORDER BY 1`, s.parquet))
	if err == nil {
		for _, r := range trendRows {
			d.Trend = append(d.Trend, TrendPoint{
				Block:  fmt.Sprintf("%v", r["bucket"]),
				Events: int64(r["n"].(float64)),
			})
		}
	}
	// 风险地址数（Top10 接收地址作为风险代理指标——保守估计为高频地址数）
	riskRows, err := s.engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT COUNT(*) AS n FROM (
			SELECT address, COUNT(*) AS c FROM read_parquet('%s') GROUP BY 1 HAVING COUNT(*) >= 100
		)`, s.parquet))
	if err == nil && len(riskRows) == 1 {
		d.RiskAddresses = int64(riskRows[0]["n"].(float64))
	}
	return d, nil
}

// ServeHTTP 路由分发。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/analytics/dashboard" {
		h.json(w, func() (any, error) { return h.service.DashboardOverview(r.Context()) })
		return
	}
	// ── V2.0 统计体系（设计 §19）──
	if r.URL.Path == "/analytics/flow-stats" && r.Method == http.MethodGet {
		chain := r.URL.Query().Get("chain")
		if chain == "" {
			chain = "bsc"
		}
		chainID := int64(56)
		if v := r.URL.Query().Get("chain_id"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				chainID = n
			}
		}
		token := r.URL.Query().Get("token")
		h.json(w, func() (any, error) { return h.service.FlowStats(r.Context(), chain, chainID, token) })
		return
	}
	if r.URL.Path == "/analytics/address-stats" && r.Method == http.MethodGet {
		address := strings.ToLower(r.URL.Query().Get("address"))
		if !validEVMLikeAddress(address) {
			http.Error(w, `{"detail":"address 不是合法的 EVM 地址"}`, http.StatusBadRequest)
			return
		}
		token := r.URL.Query().Get("token")
		h.json(w, func() (any, error) { return h.service.AddressStats(r.Context(), address, token) })
		return
	}
	if r.URL.Path == "/analytics/graph" {
		limit := 500
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 5000 {
				limit = n
			}
		}
		h.serveGraphFile(w, limit)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/analytics/report/") {
		h.serveReportFile(w, strings.TrimPrefix(r.URL.Path, "/analytics/report/"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/analytics")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "address" {
		address := parts[1]
		// 安全校验（对齐 /analytics/address-stats）：防止脏地址进入 SQL 拼接
		if !validEVMLikeAddress(address) {
			http.Error(w, `{"detail":"address 不是合法的 EVM 地址"}`, http.StatusBadRequest)
			return
		}
		switch parts[2] {
		case "profile":
			h.json(w, func() (any, error) { return h.service.Profile(r.Context(), address) })
			return
		case "flows":
			h.json(w, func() (any, error) {
				return h.service.Flows(r.Context(), address, r.URL.Query().Get("token"))
			})
			return
		case "path":
			h.json(w, func() (any, error) { return h.service.Path(r.Context(), address) })
			return
		case "risk":
			h.json(w, func() (any, error) { return h.service.Risk(r.Context(), address) })
			return
		}
	}
	if path == "/addresses/profile" && r.Method == http.MethodPost {
		var req struct {
			Addresses []string `json:"addresses"`
			AddrFile  string   `json:"addr_file"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(req.Addresses) == 0 && strings.TrimSpace(req.AddrFile) == "" {
			http.Error(w, `{"detail":"addresses or addr_file required"}`, http.StatusBadRequest)
			return
		}
		if err := h.validateAddrFile(req.AddrFile); err != nil {
			http.Error(w, `{"detail":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		// 相对路径解析为数据目录内绝对路径（duckdb CLI 的 CWD 不是数据目录）
		addrFile := req.AddrFile
		if strings.TrimSpace(addrFile) != "" {
			addrFile = filepath.Join(h.allowedDataRoot, strings.ReplaceAll(strings.TrimSpace(addrFile), "/", string(filepath.Separator)))
		}
		h.json(w, func() (any, error) {
			return h.service.BatchProfiles(r.Context(), req.Addresses, addrFile)
		})
		return
	}
	http.NotFound(w, r)
}

// validateAddrFile 校验 addr_file 路径安全：仅允许数据根目录内相对路径。
func (h *Handler) validateAddrFile(addrFile string) error {
	if strings.TrimSpace(addrFile) == "" {
		return nil
	}
	af := strings.ReplaceAll(strings.TrimSpace(addrFile), "/", string(filepath.Separator))
	if filepath.IsAbs(af) {
		return errors.New("addr_file 必须是数据目录内的相对路径")
	}
	cleaned := filepath.Clean(af)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return errors.New("addr_file 不允许路径穿越")
	}
	if strings.ContainsAny(cleaned, "*?[") {
		return errors.New("addr_file 不允许通配符")
	}
	joined := filepath.Join(h.allowedDataRoot, cleaned)
	rel, err := filepath.Rel(h.allowedDataRoot, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("addr_file 超出允许的数据目录")
	}
	return nil
}

func (h *Handler) json(w http.ResponseWriter, fn func() (any, error)) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	defer func() {
		if r := recover(); r != nil {
			// 记录服务端日志便于定位，但不向客户端泄露 panic 内部信息（路径/状态等）
			logger.Log.Error().Msgf("analyticsapi handler panic: %v", r)
			w.WriteHeader(http.StatusInternalServerError)
			body, _ := json.Marshal(map[string]string{"detail": "内部错误"})
			_, _ = w.Write(body)
		}
	}()
	v, err := fn()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		msg := err.Error()
		if runes := []rune(msg); len(runes) > 300 {
			msg = string(runes[:300]) + "...(truncated)"
		}
		body, _ := json.Marshal(map[string]string{"detail": msg})
		_, _ = w.Write(body)
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// serveGraphFile 返回已生成的图谱文件（benchmark/snapshots/graph.json），
// 按 degree 裁剪 Top limit 节点及其边（防止前端渲染全图卡死）。
func (h *Handler) serveGraphFile(w http.ResponseWriter, limit int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	data, err := os.ReadFile(graphFileCandidate())
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"graph.json not found"}`))
		return
	}
	var full struct {
		Nodes []struct {
			ID     string  `json:"id"`
			Addr   string  `json:"address"`
			Type   string  `json:"type"`
			Risk   float64 `json:"risk_score"`
			Degree int     `json:"degree"`
		} `json:"nodes"`
		Edges []struct {
			Source string `json:"source"`
			Target string `json:"target"`
			Kind   string `json:"kind"`
			Token  string `json:"token,omitempty"`
			Amount string `json:"amount,omitempty"`
			TxCount int64 `json:"tx_count,omitempty"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"graph.json parse failed"}`))
		return
	}
	if limit <= 0 || limit >= len(full.Nodes) {
		_, _ = w.Write(data) // 全图或图很小
		return
	}
	// 按 degree 降序取 Top limit 节点
	idx := make([]int, len(full.Nodes))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(i, j int) bool {
		return full.Nodes[idx[i]].Degree > full.Nodes[idx[j]].Degree
	})
	keep := make(map[string]bool, limit)
	subNodes := make([]map[string]any, 0, limit)
	for _, i := range idx[:limit] {
		n := full.Nodes[i]
		nid := n.ID
		if nid == "" {
			nid = n.Addr // graph.json 节点用 address 字段
		}
		if nid == "" {
			continue
		}
		keep[nid] = true
		subNodes = append(subNodes, map[string]any{
			"id": nid, "type": n.Type, "risk_score": n.Risk, "degree": n.Degree,
		})
	}
	subEdges := make([]map[string]any, 0)
	for _, e := range full.Edges {
		if keep[e.Source] && keep[e.Target] {
			subEdges = append(subEdges, map[string]any{
				"source": e.Source, "target": e.Target, "kind": e.Kind,
				"token": e.Token, "amount": e.Amount, "tx_count": e.TxCount,
			})
		}
	}
	out, _ := json.Marshal(map[string]any{"nodes": subNodes, "edges": subEdges, "truncated": true})
	_, _ = w.Write(out)
}

func graphFileCandidate() string {
	candidates := []string{
		`E:\codex\etl\benchmark\snapshots\graph.json`,
		`benchmark\snapshots\graph.json`,
		`..\..\benchmark\snapshots\graph.json`,
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[0]
}

// serveReportFile 提供报告产物下载（防路径穿越：仅允许 benchmark 下的已知文件）。
func (h *Handler) serveReportFile(w http.ResponseWriter, name string) {
	cleaned := strings.ReplaceAll(name, "\\", "/")
	cleaned = strings.TrimPrefix(cleaned, "/")
	if strings.Contains(cleaned, "..") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	roots := []string{
		`E:\codex\etl\benchmark`,
		`E:\codex\etl\benchmark\snapshots`,
		`benchmark`,
		`benchmark\snapshots`,
		`..\..\benchmark`,
		`..\..\benchmark\snapshots`,
	}
	var path string
	for _, root := range roots {
		cand := filepath.Join(root, filepath.FromSlash(cleaned))
		if _, err := os.Stat(cand); err == nil {
			path = cand
			break
		}
	}
	if path == "" {
		http.NotFound(w, nil)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "read failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentTypeFor(path))
	_, _ = w.Write(data)
}

func contentTypeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".md":
		return "text/markdown; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
