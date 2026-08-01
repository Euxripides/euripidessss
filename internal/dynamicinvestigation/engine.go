package dynamicinvestigation

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/logger"
)

// ── 分析信号源 ──

// FlowSignal 是一条资金关系信号（用于发现关联地址）。
type FlowSignal struct {
	Counterparty string // 对手地址
	Token        string // Token 合约地址
	Amount       string // 金额（raw decimal）
	Direction    string // incoming / outgoing
}

// ProfileSignal 是地址画像信号（用于评分/识别）。
type ProfileSignal struct {
	IsContract bool    // 是否合约
	TxCount    int64   // 交易笔数
	InCount    int64   // 入边数
	OutCount   int64   // 出边数
	RiskScore  float64 // 风险评分 0-100
	Degree     int     // 图度（活跃度补充）
}

// DiscoverySource 提供链上分析信号。真实实现可包装 analyticsapi.Service
// 或 graphintel.Builder；测试用内存实现。
type DiscoverySource interface {
	// Flows 返回地址的资金流（incoming/outgoing 对手）。
	Flows(ctx context.Context, address string) ([]FlowSignal, error)
	// Profile 返回地址画像信号。
	Profile(ctx context.Context, address string) (*ProfileSignal, error)
}

// ── 采集执行器 ──

// AcquisitionExecutor 执行采集任务。真实实现包装 parquetdownload.Manager
// （CSV 直链）与 sqd.Client（增量流式）；测试用 fake 实现。
type AcquisitionExecutor interface {
	Execute(ctx context.Context, task *AcquisitionTask) error
}

// ── 引擎 ──

// Engine 是 Dynamic Investigation Engine 主流程：
// 目标地址 → 分析 → 发现关联地址 → 评分 → 实体识别 → 采集路由 → 任务生成 → 执行。
type Engine struct {
	queue      *Queue
	recognizer *Recognizer
	source     DiscoverySource
	executor   AcquisitionExecutor
	config     ExpansionConfig

	mu      sync.Mutex
	tasks   []*AcquisitionTask
	lastRun *time.Time

	// csvBatchByAddr 记录已并入 CSV 批量任务的地址及其状态快照（防 N×N 重复下载，
	// 值拷贝避免共享 task 指针的数据竞争）
	csvBatchByAddr map[string]csvBatchSnapshot
}

// csvBatchSnapshot 是 CSV 批量任务在去重映射中的状态快照（值类型，线程安全）。
type csvBatchSnapshot struct {
	TaskID string
	Status string
	JobID  string
	Error  string
}

// NewEngine 创建引擎。
func NewEngine(queue *Queue, recognizer *Recognizer, source DiscoverySource, executor AcquisitionExecutor, cfg ExpansionConfig) *Engine {
	if recognizer == nil {
		recognizer = NewRecognizer()
	}
	return &Engine{
		queue:          queue,
		recognizer:     recognizer,
		source:         source,
		executor:       executor,
		config:         cfg,
		csvBatchByAddr: make(map[string]csvBatchSnapshot),
	}
}

// Config 返回当前配置（副本，线程安全）。
func (e *Engine) Config() ExpansionConfig {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.config
}

// UpdateConfig 更新配置（线程安全）。
func (e *Engine) UpdateConfig(cfg ExpansionConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.config = cfg
}

// Queue 暴露队列（API/测试用）。
func (e *Engine) Queue() *Queue { return e.queue }

// Tasks 返回生成的任务列表（线程安全只读视图）。
func (e *Engine) Tasks() []TaskView {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]TaskView, len(e.tasks))
	for i, t := range e.tasks {
		out[i] = t.View()
	}
	return out
}

// Stats 返回引擎统计。
func (e *Engine) Stats() EngineStats {
	counts := e.queue.Count()
	stats := EngineStats{
		TotalDiscovered: counts[StatusDiscovered] + counts[StatusScoring],
		TotalApproved:   counts[StatusApproved] + counts[StatusAcquiring],
		TotalCompleted:  counts[StatusCompleted],
		TotalIgnored:    counts[StatusIgnored],
		TotalTasks:      len(e.Tasks()),
		ByEntity:        e.queue.CountByEntity(),
		ByAcquisition:   e.queue.CountByAcquisition(),
	}
	e.mu.Lock()
	stats.Config = e.config
	if e.lastRun != nil {
		t := *e.lastRun
		stats.LastRun = &t
	}
	e.mu.Unlock()
	return stats
}

// Start 从目标地址开始一轮动态扩展：发现 → 评分 → 识别 → 路由 → 任务生成 → 执行。
// 先分析目标地址的关联（发现下一跳），再评分/采集，与方案流程一致：
// 目标地址 → 分析 → 发现关联地址 → 评分 → 决策。
func (e *Engine) Start(ctx context.Context, target string) error {
	target = normalizeAddress(target)
	if target == "" {
		return fmt.Errorf("目标地址不能为空")
	}
	_, _ = e.queue.Add(target, "root", "", "", 0)

	// 发现目标地址的关联地址（depth=1）
	if err := e.discoverFrom(ctx, target); err != nil {
		logger.Log.Warn().Str("address", target).Err(err).Msg("dynamic_investigation_initial_discovery_failed")
	}

	if err := e.RunOnce(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	e.mu.Lock()
	e.lastRun = &now
	e.mu.Unlock()
	return nil
}

// RunOnce 执行一轮扩展：
// 1. 所有 DISCOVERED 地址评分（评分中）
// 2. 评分通过的批准，未通过忽略
// 3. 为 APPROVED 地址生成采集任务并执行
// 4. 从任务结果/资金流中发现下一跳地址（深度+1），受 maxDepth/maxAddresses/金额阈值约束
func (e *Engine) RunOnce(ctx context.Context) error {
	// 阶段 1：评分待处理地址
	pending := e.queue.PendingScoring(0)
	for _, addr := range pending {
		if err := e.scoreOne(ctx, addr); err != nil {
			logger.Log.Warn().Str("address", addr).Err(err).Msg("dynamic_investigation_score_failed")
			continue
		}
	}

	// 阶段 2：批准并生成任务
	approved := e.queue.PendingAcquisition(0)
	for _, addr := range approved {
		if err := e.generateAndExecute(ctx, addr); err != nil {
			logger.Log.Warn().Str("address", addr).Err(err).Msg("dynamic_investigation_acquire_failed")
			_ = e.queue.Transition(addr, StatusIgnored)
		}
	}

	// 阶段 3：发现下一跳
	if err := e.discoverNextLevel(ctx); err != nil {
		return err
	}
	return e.queue.Save()
}

// scoreOne 对单个地址完成评分+识别+路由决策。
func (e *Engine) scoreOne(ctx context.Context, address string) error {
	cfg := e.Config() // 快照配置，避免热路径无锁读
	if err := e.queue.Transition(address, StatusScoring); err != nil {
		return err
	}

	item, _ := e.queue.Get(address)
	signal, err := e.profileSignal(ctx, address)
	if err != nil {
		return err
	}

	// 实体识别
	entity, label := e.recognizer.Recognize(EntityHints{
		Address:    address,
		IsContract: signal.IsContract,
		InCount:    signal.InCount,
		OutCount:   signal.OutCount,
		TxCount:    signal.TxCount,
	})
	e.queue.SetEntity(address, entity, label)

	// 评分
	result := Score(ScoreInput{
		Address:       address,
		Entity:        entity,
		Amount:        item.Amount,
		RiskScore:     signal.RiskScore,
		SharedCounter: 0,
		RelationScore: 0.5, // 有资金关系即视为中等关联
		TxCount:       signal.TxCount,
		Degree:        signal.Degree,
	}, cfg)
	e.queue.SetScore(address, result.Score, result.Breakdown)

	// 路由决策
	route := Route(RouteInput{
		Entity:       entity,
		Decision:     result.Decision,
		Score:        result.Score,
		CurrentLevel: item.DataLevel,
		Depth:        item.Depth,
	}, cfg)
	e.queue.SetAcquisition(address, route.Mode, route.TargetLevel)

	// 决策落库：ACQUIRE 与 HOLD 都批准采集（HOLD 优先级由评分排序体现），
	// IGNORE 仅保存关系。
	switch result.Decision {
	case DecisionAcquire, DecisionHold:
		_ = e.queue.Transition(address, StatusApproved)
	default:
		e.queue.SetStatus(address, StatusIgnored)
		e.setIgnoredReason(address, result.Reason)
	}
	return nil
}

// generateAndExecute 生成采集任务并执行。
// CSV 直链按实体簇批量：簇内第一个地址创建任务并包含全部成员，后续成员复用同一任务
// （通过 csvBatchByAddr 去重，避免 N×N 重复下载）。
func (e *Engine) generateAndExecute(ctx context.Context, address string) error {
	cfg := e.Config() // 快照配置
	item, ok := e.queue.Get(address)
	if !ok {
		return fmt.Errorf("地址 %s 不在队列", address)
	}
	if item.Acquisition == "" {
		return fmt.Errorf("地址 %s 未设置采集方式", address)
	}

	// 状态：ACQUIRING（先置位，失败再回退 IGNORED）
	_ = e.queue.Transition(address, StatusAcquiring)

	task := &AcquisitionTask{
		TaskID:      fmt.Sprintf("task-%s-%d", shortID(address), time.Now().UnixNano()),
		Address:     address,
		ChainID:     cfg.ChainID,
		Entity:      item.Entity,
		Mode:        item.Acquisition,
		FromLevel:   item.DataLevel,
		TargetLevel: item.TargetLevel,
		Priority:    priorityFor(item),
		status:      "pending",
		updatedAt:   time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
	}

	// CSV 批量去重：检查已并入任务 + 注册簇成员在同一 e.mu 临界区内完成，
	// 消除 check-then-register 的 TOCTOU 竞争（并发 /start 不会生成重复簇任务）。
	if task.Mode == AcquisitionCSVDirect {
		e.mu.Lock()
		if snap, already := e.csvBatchByAddr[address]; already {
			e.mu.Unlock()
			e.queue.SetJob(address, snap.JobID)
			switch snap.Status {
			case "done":
				_ = e.queue.Transition(address, StatusCompleted)
				e.queue.SetDataLevel(address, item.TargetLevel)
			case "failed":
				// 簇任务失败：成员回退 IGNORED 并记录原因，不留孤儿
				e.queue.SetStatus(address, StatusIgnored)
				e.queue.SetIgnoredReason(address, "簇采集任务失败: "+snap.Error)
			}
			return nil
		}
		cluster := e.sameClusterAddresses(address)
		if len(cluster) == 0 {
			cluster = []string{address}
		}
		task.Addresses = cluster
		for _, member := range cluster {
			e.csvBatchByAddr[member] = csvBatchSnapshot{
				TaskID: task.TaskID,
				Status: task.Status(),
				JobID:  task.JobID(),
			}
		}
		e.mu.Unlock()
	} else {
		task.Addresses = []string{address}
	}

	e.mu.Lock()
	e.tasks = append(e.tasks, task)
	e.mu.Unlock()

	if task.Mode == AcquisitionRelationsOnly {
		// 仅保存关系：直接完成
		_ = e.queue.Transition(address, StatusCompleted)
		return nil
	}

	if e.executor == nil {
		_ = e.queue.Transition(address, StatusCompleted)
		return nil // 无执行器时视为完成（测试/仅规划场景）
	}

	task.SetStatus("running")
	task.Touch()
	runErr := e.executor.Execute(ctx, task)
	task.Touch()
	if runErr != nil {
		sanitized := sanitizeError(runErr.Error())
		task.SetStatus("failed")
		task.SetError(sanitized)
		e.updateCSVSnapshot(task) // 锁内回写簇失败状态快照
		e.queue.SetJob(address, task.JobID())
		e.queue.SetIgnoredReason(address, "采集执行失败: "+sanitized)
		return runErr
	}
	// 异步执行器（如真实 parquetdownload.Manager）启动任务后保持 running，
	// 地址停留在 ACQUIRING 并关联 JobID，由下载引擎推进；同步执行器标 done。
	if task.Status() == "running" {
		if task.Mode == AcquisitionCSVDirect {
			for _, member := range task.Addresses {
				e.queue.SetJob(member, task.JobID())
			}
		} else {
			e.queue.SetJob(address, task.JobID())
		}
		return nil
	}
	task.SetStatus("done")
	e.updateCSVSnapshot(task)
	// 批量任务：全部成员关联 JobID 并完成
	if task.Mode == AcquisitionCSVDirect {
		for _, member := range task.Addresses {
			e.queue.SetJob(member, task.JobID())
			_ = e.queue.Transition(member, StatusCompleted)
			e.queue.SetDataLevel(member, task.TargetLevel)
		}
		return nil
	}
	e.queue.SetJob(address, task.JobID())
	e.queue.SetDataLevel(address, task.TargetLevel)
	_ = e.queue.Transition(address, StatusCompleted)
	return nil
}

// discoverFrom 从单个地址的资金流中发现下一跳地址（受深度/数量/金额阈值约束）。
func (e *Engine) discoverFrom(ctx context.Context, address string) error {
	cfg := e.Config() // 快照配置
	item, ok := e.queue.Get(address)
	if !ok {
		return fmt.Errorf("地址 %s 不在队列", address)
	}
	if item.Depth >= cfg.MaxDepth {
		return nil // 已达最大深度
	}
	flows, err := e.source.Flows(ctx, address)
	if err != nil {
		return err
	}
	limit := cfg.RelationsPerAddress
	if limit <= 0 {
		limit = 20
	}
	count := 0
	limitHit := false
	for _, f := range flows {
		if f.Counterparty == "" || strings.EqualFold(f.Counterparty, address) {
			continue
		}
		if !IsValidEVMAddress(f.Counterparty) {
			continue // 纵深防御：跳过非法地址，防止脏数据扩散
		}
		if !e.amountAboveThreshold(f.Amount) {
			continue // 金额阈值过滤
		}
		if e.queue.Total() >= cfg.MaxAddresses {
			// 硬上限：队列已满立即停止，不再添加新地址
			limitHit = true
			break
		}
		if count >= limit {
			limitHit = true
			break // 每地址最多发现 relations_per_address 个新关系
		}
		_, added := e.queue.Add(f.Counterparty, address, f.Amount, f.Token, item.Depth+1)
		if added {
			count++
		}
	}
	if limitHit {
		logger.Log.Info().Str("address", address).Int("max", cfg.MaxAddresses).
			Int("relations", count).Msg("dynamic_investigation_discovery_limited")
	}
	return nil
}

// discoverNextLevel 从已完成的地址资金流中发现下一跳地址（BFS 一层）。
func (e *Engine) discoverNextLevel(ctx context.Context) error {
	cfg := e.Config() // 快照配置
	completed := e.queue.List(StatusCompleted, "", -1)
	for _, item := range completed {
		if item.Depth >= cfg.MaxDepth {
			continue
		}
		if err := e.discoverFrom(ctx, item.Address); err != nil {
			logger.Log.Warn().Str("address", item.Address).Err(err).Msg("dynamic_investigation_flows_failed")
		}
	}
	return nil
}

// shortID 生成地址短标识（防切片越界）。
func shortID(address string) string {
	s := strings.TrimPrefix(address, "0x")
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// sanitizeError 脱敏错误信息：剥离 Windows/Unix 绝对路径，防止服务端路径泄露到客户端。
func sanitizeError(msg string) string {
	if msg == "" {
		return msg
	}
	// 剥离形如 E:\... 或 /... 的路径段
	re := regexp.MustCompile(`(?:[A-Za-z]:[\\/]|/)[^\s"']+`)
	msg = re.ReplaceAllString(msg, "[path]")
	return msg
}

// updateCSVSnapshot 在锁内将任务最新状态回写到 csvBatchByAddr 快照。
func (e *Engine) updateCSVSnapshot(task *AcquisitionTask) {
	if task.Mode != AcquisitionCSVDirect {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	snap := csvBatchSnapshot{
		TaskID: task.TaskID,
		Status: task.Status(),
		JobID:  task.JobID(),
		Error:  task.Error(),
	}
	for _, member := range task.Addresses {
		if cur, ok := e.csvBatchByAddr[member]; ok && cur.TaskID == task.TaskID {
			e.csvBatchByAddr[member] = snap
		}
	}
}

// sameClusterAddresses 收集同一深度且同实体类型的 APPROVED/ACQUIRING 地址
// （CSV 直链批量下载用）。
func (e *Engine) sameClusterAddresses(address string) []string {
	item, _ := e.queue.Get(address)
	var out []string
	for _, other := range e.queue.List("", item.Entity, item.Depth) {
		if other.Status == StatusApproved || other.Status == StatusAcquiring {
			if other.Acquisition == AcquisitionCSVDirect {
				out = append(out, other.Address)
			}
		}
	}
	return out
}

// setIgnoredReason 记录忽略原因。
func (e *Engine) setIgnoredReason(address, reason string) {
	e.queue.SetIgnoredReason(address, reason)
}

// profileSignal 从数据源获取画像信号。
func (e *Engine) profileSignal(ctx context.Context, address string) (*ProfileSignal, error) {
	if e.source == nil {
		return &ProfileSignal{}, nil
	}
	p, err := e.source.Profile(ctx, address)
	if err != nil {
		// 画像失败不阻断：返回空信号
		logger.Log.Warn().Str("address", address).Err(err).Msg("dynamic_investigation_profile_failed")
		return &ProfileSignal{}, nil
	}
	return p, nil
}

// amountAboveThreshold 判断金额是否高于配置阈值（原始数值比较）。
func (e *Engine) amountAboveThreshold(amount string) bool {
	threshold := strings.TrimSpace(e.Config().AmountThreshold)
	if threshold == "" || threshold == "0" {
		return true
	}
	a, okA := parseAmountBig(amount)
	b, okB := parseAmountBig(threshold)
	if !okA || !okB {
		return false
	}
	return a.Cmp(b) >= 0
}

// parseAmountBig 解析 raw decimal/hex 金额为 big.Float（比较用）。
func parseAmountBig(s string) (*big.Float, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	if strings.HasPrefix(s, "0x") {
		n, ok := new(big.Int).SetString(strings.TrimPrefix(s, "0x"), 16)
		if !ok {
			return nil, false
		}
		return new(big.Float).SetInt(n), true
	}
	if n, ok := new(big.Int).SetString(s, 10); ok {
		return new(big.Float).SetInt(n), true
	}
	return nil, false
}

// priorityFor 按实体类型分配任务优先级（0=CRITICAL, 4=BACKGROUND）。
func priorityFor(item *DiscoveredAddress) int {
	switch item.Entity {
	case EntityExchange:
		return 1
	case EntityWallet, EntityRouter:
		return 2
	case EntityContract:
		return 3
	default:
		return 4
	}
}

// floatEq 辅助：评分结果浮点比较（测试用）。
func floatEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}
