package downloadscheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/logger"
	"github.com/etl/backend/internal/normalize"
	"github.com/etl/backend/internal/parquetdownload"
)

// RPCClient 实时链上查询接口（由 rpcmanager.Manager 实现）。
type RPCClient interface {
	Call(ctx context.Context, chainKey, method string, params any) (json.RawMessage, string, error)
}

// SQDEngine 历史数据下载接口（由 parquetdownload.Manager 实现）。
type SQDEngine interface {
	Start(ctx context.Context, request parquetdownload.StartRequest) (*parquetdownload.Job, error)
	Get(id string) (*parquetdownload.Job, error)
}

// RecoveryWriter 恢复数据落盘与合并接口（Token Transfer Recovery Layer §9/§10，
// 由 parquetdownload.Manager 实现：同 schema 唯一化 Parquet + MERGING 阶段合并）。
type RecoveryWriter interface {
	// WriteTokenTransfers 将 RPC 恢复的 Token Transfer 行落盘为唯一化 Parquet。
	WriteTokenTransfers(ctx context.Context, taskKey string, network chain.EVM, rows []normalize.TokenTransfer) (*parquetdownload.RecoveryWriteResult, error)
	// MergeTokenTransfers 将计划内恢复数据与仓库既有数据按唯一键合并去重。
	MergeTokenTransfers(ctx context.Context, planID string, network chain.EVM) (*parquetdownload.RecoveryMergeStats, error)
}

// Provider 数据获取能力（设计文档 §6）。
type Provider interface {
	Kind() ProviderKind
	Name() string
	// Tier Provider 分层（Tier 永远优先于评分，设计 §16）。
	Tier() ProviderTier
	// CanHandle 是否支持该数据集类型。
	CanHandle(d Dataset) bool
	// Available 当前是否已装配可用（rpc 管理器/SQD 引擎）。
	Available() bool
	// ManualOnly 是否只能人工执行（如浏览器登录态采集）。
	ManualOnly() bool
	// Score 计算 Layer 2 Provider 评分。
	Score(d Dataset) ProviderScore
	// Execute 执行采集。SQD 任务异步启动后返回 jobID，由调度器轮询。
	Execute(ctx context.Context, req Requirement) (*TaskResult, error)
}

// ── RPC Provider（实时余额/合约状态 + Token Transfer 恢复通道）──

type RPCProvider struct {
	client RPCClient
	writer RecoveryWriter // Token Transfer 恢复数据落盘器（可为 nil：仅余额场景）
}

func NewRPCProvider(client RPCClient) *RPCProvider { return &RPCProvider{client: client} }

// WithRecovery 注入 Token Transfer 恢复数据写入器（V1.0 恢复层 §6/§9）。
func (p *RPCProvider) WithRecovery(w RecoveryWriter) *RPCProvider {
	p.writer = w
	return p
}

func (p *RPCProvider) Kind() ProviderKind { return ProviderRPC }
func (p *RPCProvider) Name() string       { return "RPC Provider" }
func (p *RPCProvider) Tier() ProviderTier { return TierNormal }
func (p *RPCProvider) ManualOnly() bool   { return false }
func (p *RPCProvider) Available() bool {
	if p.client == nil {
		return false
	}
	if health, ok := p.client.(interface{ HasAnyAvailable() bool }); ok {
		return health.HasAnyAvailable()
	}
	return true
}
func (p *RPCProvider) CanHandle(d Dataset) bool {
	return d == DatasetBalance || d == DatasetTokenTransfer
}

func (p *RPCProvider) State() ProviderState {
	if !p.Available() {
		return ProviderUnavailable
	}
	return ProviderHealthy
}

func (p *RPCProvider) StateReasons() []string {
	if !p.Available() {
		if p.client == nil {
			return []string{"RPC 管理器未装配"}
		}
		return []string{"没有可路由的 RPC 节点"}
	}
	return nil
}

func (p *RPCProvider) Score(d Dataset) ProviderScore {
	if d == DatasetTokenTransfer {
		// 恢复通道（设计 §6/§12）：实时增量（最近窗口），SQD 不可用时自动接管
		s := ProviderScore{
			Provider:    ProviderRPC,
			Name:        "RPC Provider",
			Tier:        TierNormal,
			State:       p.State(),
			Coverage:    40, // 仅近期窗口，无全量历史
			Accuracy:    90, // 链上原始事件，无解析损耗
			Speed:       75, // 实时查询，串行分块
			Cost:        50, // 公共节点限流/付费节点成本
			Reliability: 75, // 依赖节点健康，有重试/轮换
			Available:   p.Available(),
			Reasons:     []string{"RPC 恢复通道：eth_getLogs 实时增量（最近窗口）", "SQD 不可用时自动接管，避免任务中断；历史数据仍由 SQD/AWS 承担"},
		}
		s.Total = weightedTotal(s)
		return s
	}
	s := ProviderScore{
		Provider:    ProviderRPC,
		Name:        "RPC Provider",
		Tier:        TierNormal,
		State:       p.State(),
		Coverage:    60, // 仅实时态，无历史
		Accuracy:    100,
		Speed:       90,
		Cost:        40, // 公共节点限流/付费节点成本
		Reliability: 85,
		Available:   p.Available(),
		Reasons:     []string{"实时余额/合约状态必须走链上 RPC", "无法提供历史资金流"},
	}
	s.Total = weightedTotal(s)
	return s
}

func (p *RPCProvider) Execute(ctx context.Context, req Requirement) (*TaskResult, error) {
	if req.Dataset == DatasetTokenTransfer {
		return p.executeTokenTransfer(ctx, req)
	}
	if p.client == nil {
		return nil, errors.New("RPC Provider 未装配（rpcmanager 不可用）")
	}
	if len(req.Addresses) == 0 {
		return nil, errors.New("balance 需求缺少地址")
	}
	network, err := chain.Resolve(req.ChainKey)
	if err != nil {
		return nil, fmt.Errorf("未知链: %w", err)
	}
	type balanceLine struct {
		Address string `json:"address"`
		Balance string `json:"balance"`
		Symbol  string `json:"symbol"`
	}
	lines := make([]balanceLine, 0, len(req.Addresses))
	var failed int
	for _, addr := range req.Addresses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, _, callErr := p.client.Call(ctx, network.Key, "eth_getBalance", []any{strings.ToLower(addr), "latest"})
		if callErr != nil {
			logger.Log.Warn().Str("address", addr).Err(callErr).Msg("scheduler_rpc_balance_failed")
			failed++
			continue // 单地址失败不中断整批
		}
		var hexBalance string
		if err := json.Unmarshal(raw, &hexBalance); err != nil {
			logger.Log.Warn().Str("address", addr).Err(err).Msg("scheduler_rpc_balance_parse")
			failed++
			continue
		}
		wei := new(big.Int)
		if _, ok := wei.SetString(strings.TrimPrefix(hexBalance, "0x"), 16); !ok {
			failed++
			continue
		}
		lines = append(lines, balanceLine{Address: addr, Balance: wei.String(), Symbol: network.NativeSymbol})
	}
	if len(lines) == 0 {
		return nil, errors.New("RPC 余额查询全部失败（节点不可用或未配置）")
	}
	payload, _ := json.Marshal(lines)
	return &TaskResult{
		Output:  string(payload),
		Summary: fmt.Sprintf("%s 原生余额查询成功 %d/%d 个地址（失败 %d，RPC 错误已脱敏）", network.NativeSymbol, len(lines), len(req.Addresses), failed),
		Rows:    int64(len(lines)),
		NewData: false, // 余额为实时信息，不写入数据集
	}, nil
}

// ── SQD Provider（历史交易/Token Transfer/Logs）──

// SQDProvider 历史数据下载 Provider（V3：评分随可靠性层健康动态调整）。
type SQDProvider struct {
	engine SQDEngine
	health HealthSource // 可为 nil（评分退化为静态）
}

func NewSQDProvider(engine SQDEngine) *SQDProvider { return &SQDProvider{engine: engine} }

// WithHealth 注入 SQD 可靠性层健康快照源（V3 动态评分）。
func (p *SQDProvider) WithHealth(h HealthSource) *SQDProvider {
	p.health = h
	return p
}

func (p *SQDProvider) Kind() ProviderKind { return ProviderSQD }
func (p *SQDProvider) Name() string       { return "SQD Provider" }
func (p *SQDProvider) Tier() ProviderTier { return TierNormal }
func (p *SQDProvider) ManualOnly() bool   { return false }
func (p *SQDProvider) Available() bool    { return p.engine != nil }
func (p *SQDProvider) CanHandle(d Dataset) bool {
	return d == DatasetTransactions || d == DatasetTokenTransfer
}

// State 按 SQD 可靠性层健康快照映射 ProviderState（设计 §6/§10）。
func (p *SQDProvider) State() ProviderState {
	if !p.Available() {
		return ProviderUnavailable
	}
	if p.health == nil {
		return ProviderHealthy
	}
	h := p.health.SQDHealth()
	switch h.BreakerState {
	case "OPEN":
		return ProviderCircuitOpen
	case "HALF_OPEN", "DEGRADED":
		return ProviderDegraded
	}
	if h.CooldownActive {
		return ProviderRateLimited
	}
	if h.SuccessRate > 0 && h.SuccessRate < 0.85 {
		return ProviderDegraded
	}
	return ProviderHealthy
}

func (p *SQDProvider) StateReasons() []string {
	if !p.Available() {
		return []string{"Parquet 下载管理器未装配"}
	}
	if p.health == nil {
		return nil
	}
	return p.health.SQDHealth().DegradeReasons()
}

func (p *SQDProvider) Score(d Dataset) ProviderScore {
	s := ProviderScore{
		Provider:    ProviderSQD,
		Name:        "SQD Provider",
		Tier:        TierNormal,
		State:       p.State(),
		Coverage:    90, // 多年历史全量
		Accuracy:    90,
		Speed:       50, // 批量下载较慢
		Cost:        70,
		Reliability: 80,
		Available:   p.Available(),
		Reasons:     []string{"历史交易/Token Transfer 首选：多年历史、全量覆盖", "落盘 Parquet/DuckDB 数据资产，供图谱直接消费"},
	}
	// V3 动态健康评分（设计 §10/§11）：SQD 不健康时自动降分，Router 改选 AWS/RPC
	if p.health != nil {
		h := p.health.SQDHealth()
		reliabilityPenalty, speedPenalty, degradeReasons := providerDegrade(h)
		s.Reliability -= reliabilityPenalty
		s.Speed -= speedPenalty
		// 分量钳制 0-100（同一 503 事件的多种惩罚不叠加到负数）
		s.Reliability = clampScore(s.Reliability)
		s.Speed = clampScore(s.Speed)
		if len(degradeReasons) > 0 {
			s.Reasons = append(s.Reasons, degradeReasons...)
		}
		if !h.Healthy() {
			s.Reasons = append(s.Reasons, "Router 已自动降低 SQD 优先级")
		}
	}
	s.Total = weightedTotal(s)
	return s
}

// clampScore 将评分分量钳制在 [0,100]。
func clampScore(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func (p *SQDProvider) Execute(ctx context.Context, req Requirement) (*TaskResult, error) {
	if p.engine == nil {
		return nil, errors.New("SQD Provider 未装配（Parquet 下载管理器不可用）")
	}
	if len(req.Addresses) == 0 {
		return nil, errors.New("历史数据需求缺少地址")
	}
	dataset := "transactions"
	if req.Dataset == DatasetTokenTransfer {
		dataset = "logs"
	}
	startDate := req.StartDate
	if startDate == "" {
		startDate = "2020-01-01"
	}
	endDate := req.EndDate
	if endDate == "" {
		endDate = time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	}
	job, err := p.engine.Start(ctx, parquetdownload.StartRequest{
		ChainKey:       req.ChainKey,
		Addresses:      strings.Join(req.Addresses, ","),
		StartDate:      startDate,
		EndDate:        endDate,
		FromBlock:      req.FromBlock,
		ToBlock:        req.ToBlock,
		UseFirstSeen:   true,
		ExportCSV:      boolPtr(true),
		SelectedSource: []string{dataset},
	})
	if err != nil {
		return nil, fmt.Errorf("SQD 下载启动失败: %w", err)
	}
	logger.Log.Info().Str("job_id", job.ID).Str("dataset", dataset).
		Str("chain", job.ChainKey).Int("addresses", job.Addresses.Valid).
		Msg("scheduler_sqd_job_started")
	return &TaskResult{
		JobID:   job.ID,
		Output:  strings.Join(job.Outputs, "; "),
		Summary: fmt.Sprintf("SQD %s 任务已启动（job=%s），落盘 Parquet 数据资产", dataset, job.ID),
		Rows:    0,
		NewData: true,
	}, nil
}

// JobProgress 查询下游 SQD 任务进度（调度器轮询用）。
func (p *SQDProvider) JobProgress(ctx context.Context, jobID string) (progress float64, status string, err error) {
	return pollEngineJob(ctx, p.engine, jobID)
}

// jobPoller 是支持任务进度轮询的 Provider（SQD/AWS 均实现，调度器统一轮询 parquetdownload job）。
type jobPoller interface {
	JobProgress(ctx context.Context, jobID string) (float64, string, error)
}

// pollEngineJob 轮询 parquetdownload 下游任务进度。
func pollEngineJob(ctx context.Context, engine SQDEngine, jobID string) (float64, string, error) {
	if engine == nil {
		return 0, "", errors.New("下载引擎未装配")
	}
	job, err := engine.Get(jobID)
	if err != nil {
		return 0, "", err
	}
	return job.Progress, job.Status, nil
}

// ── Browser Provider（标签/公开资料，当前需人工执行）──

type BrowserProvider struct{}

func NewBrowserProvider() *BrowserProvider { return &BrowserProvider{} }

func (p *BrowserProvider) Kind() ProviderKind       { return ProviderBrowser }
func (p *BrowserProvider) Name() string             { return "Browser Provider" }
func (p *BrowserProvider) Tier() ProviderTier       { return TierNormal }
func (p *BrowserProvider) ManualOnly() bool         { return true }
func (p *BrowserProvider) Available() bool          { return true }
func (p *BrowserProvider) CanHandle(d Dataset) bool { return d == DatasetLabels }

func (p *BrowserProvider) State() ProviderState   { return ProviderHealthy }
func (p *BrowserProvider) StateReasons() []string { return nil }

func (p *BrowserProvider) Score(d Dataset) ProviderScore {
	s := ProviderScore{
		Provider:    ProviderBrowser,
		Name:        "Browser Provider",
		Tier:        TierNormal,
		State:       p.State(),
		Coverage:    70,
		Accuracy:    70,
		Speed:       30,
		Cost:        50,
		Reliability: 45,
		Available:   true,
		ManualOnly:  true,
		Reasons:     []string{"标签/公开资料需要浏览器登录态或人工确认", "当前版本请在「虚拟币-数据下载」页手动采集"},
	}
	s.Total = weightedTotal(s)
	return s
}

func (p *BrowserProvider) Execute(ctx context.Context, req Requirement) (*TaskResult, error) {
	return nil, errors.New("浏览器采集需要登录态，请在「虚拟币-数据下载」页手动执行（当前版本调度器不自动驱动浏览器）")
}

// ── 注册表与评分 ──

// Registry 管理全部 Provider（设计文档 §14 provider_registry.go）。
type Registry struct {
	providers []Provider
}

func NewRegistry(providers ...Provider) *Registry { return &Registry{providers: providers} }

// All 返回全部 Provider。
func (r *Registry) All() []Provider { return r.providers }

// Candidates 按 Tier 升序 + 总分降序返回常规 Provider（不含 Emergency Cloud，设计 §17）。
func (r *Registry) Candidates(d Dataset) []ProviderScore {
	return r.CandidatesWithTier(d, false)
}

// CandidatesWithTier 按 Tier 过滤返回候选（includeEmergency=true 仅测试/展示用；
// 调度器执行路径始终用 Candidates，Cloud 只能经 Admission Gate 进入）。
func (r *Registry) CandidatesWithTier(d Dataset, includeEmergency bool) []ProviderScore {
	scores := make([]ProviderScore, 0, len(r.providers))
	for _, p := range r.providers {
		if p.CanHandle(d) && (includeEmergency || p.Tier() < TierEmergencyCloud) {
			scores = append(scores, p.Score(d))
		}
	}
	// 稳定排序：Tier 升序（永远优先）→ 总分降序；同分时自动可用 > 手动
	for i := 0; i < len(scores); i++ {
		for j := i + 1; j < len(scores); j++ {
			swap := scores[j].Tier < scores[i].Tier
			if scores[j].Tier == scores[i].Tier && scores[j].Total > scores[i].Total {
				swap = true
			}
			if scores[j].Tier == scores[i].Tier && scores[j].Total == scores[i].Total && !scores[j].ManualOnly && scores[i].ManualOnly {
				swap = true
			}
			if swap {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}
	return scores
}

// Emergency 返回可用的应急 Cloud Provider（Admission Gate 批准后由调度器调用）。
func (r *Registry) Emergency() (ProviderScore, Provider) {
	for _, c := range r.CandidatesWithTier(DatasetTokenTransfer, true) {
		if c.Tier >= TierEmergencyCloud && c.Provider == ProviderSQDCloud {
			return c, r.byKind(c.Provider)
		}
	}
	return ProviderScore{}, nil
}

// Select 选择首个可用候选；无可用时返回总分最高的候选（调度器将标记 skipped/manual）。
func (r *Registry) Select(d Dataset) (ProviderScore, Provider) {
	return r.SelectFrom(r.Candidates(d))
}

// SelectFrom 从给定候选列表中选首个可用 Provider（V3：支持调用方预过滤，如非 BSC 剔除 AWS）。
func (r *Registry) SelectFrom(candidates []ProviderScore) (ProviderScore, Provider) {
	for _, c := range candidates {
		if !c.ManualOnly && c.Available {
			return c, r.byKind(c.Provider)
		}
	}
	if len(candidates) > 0 {
		return candidates[0], r.byKind(candidates[0].Provider)
	}
	return ProviderScore{}, nil
}

func (r *Registry) byKind(k ProviderKind) Provider {
	for _, p := range r.providers {
		if p.Kind() == k {
			return p
		}
	}
	return nil
}

// weightedTotal 加权求和（coverage 25% / accuracy 25% / speed 15% / cost 15% / reliability 20%）。
func weightedTotal(s ProviderScore) int {
	return int(0.25*float64(s.Coverage) + 0.25*float64(s.Accuracy) + 0.15*float64(s.Speed) + 0.15*float64(s.Cost) + 0.2*float64(s.Reliability) + 0.5)
}

func boolPtr(b bool) *bool { return &b }
