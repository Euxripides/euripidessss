package prefetch

import (
	"context"
	"testing"
	"time"

	"github.com/etl/backend/internal/graphcache"
	invcache "github.com/etl/backend/internal/investigation/cache"
	"github.com/etl/backend/internal/smartdownload"
)

type fakeCoverage struct {
	ratio float64
	full  bool
	cert  string
}

func (f *fakeCoverage) QueryCoverage(_, _, _ string, _, _ uint64) graphcache.CoverageInfo {
	return graphcache.CoverageInfo{Ratio: f.ratio, Full: f.full, Certification: f.cert}
}

func TestScoreFormula(t *testing.T) {
	hot := Score(ScoreInput{
		FlowValueScore: 1, InteractionFrequency: 1, PathImportance: 1,
		InvestigationRelevance: 1, AddressRisk: 100, UserExpansionProbability: 1,
		CacheReuseProbability: 1, DatasetCount: 4,
	})
	if hot < 70 {
		t.Fatalf("高分候选应为 HOT，实际 %.1f", hot)
	}
	if PriorityFor(hot) != PriorityHOT {
		t.Fatalf("优先级错误: %s", PriorityFor(hot))
	}
	cold := Score(ScoreInput{
		FlowValueScore: 0.05, InteractionFrequency: 0.05, PathImportance: 0,
		InvestigationRelevance: 0.1, AddressRisk: 10, UserExpansionProbability: 0.1,
		CacheReuseProbability: 0.1, DatasetCount: 4,
	})
	if cold >= 45 {
		t.Fatalf("低分候选不应为 WARM，实际 %.1f", cold)
	}
	if PriorityFor(cold) != PriorityCOLD {
		t.Fatalf("低分优先级错误: %s", PriorityFor(cold))
	}
}

func TestPlannerSkipsFullHitAndRanks(t *testing.T) {
	p := NewPlanner(&fakeCoverage{ratio: 0.5, cert: "CERTIFIED"})
	res := &graphcache.Result{
		Key: graphcache.Key{Address: "0xaaa"},
		Edges: []graphcache.Edge{
			{Counterparty: "0xbbb", Direction: "OUT", Token: "usdt", Outflow: "1000", TxCount: 10},
			{Counterparty: "0xccc", Direction: "IN", Token: "usdt", Inflow: "100", TxCount: 2},
			{Counterparty: "0xddd", Direction: "OUT", Token: "usdt", Outflow: "1", TxCount: 1},
		},
		TotalOutflow: "1001",
	}
	snap := invcache.ContextSnapshot{FromBlock: 100, ToBlock: 200, Tokens: []string{"usdt"}, CurrentPath: []string{"0xaaa", "0xbbb"}}
	cands, err := p.Plan(context.Background(), "inv-1", "bsc", 56, res, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) == 0 {
		t.Fatal("应生成候选")
	}
	if cands[0].Address != "0xbbb" {
		t.Fatalf("路径下一跳应排第一: %+v", cands[0])
	}
	seen := map[string]bool{}
	for _, c := range cands {
		seen[c.Address] = true
	}
	if !seen["0xddd"] {
		t.Fatal("部分覆盖候选应保留")
	}
}

func TestPlannerFullHitSkips(t *testing.T) {
	p := NewPlanner(&fakeCoverage{ratio: 1, full: true, cert: "CERTIFIED"})
	res := &graphcache.Result{
		Key: graphcache.Key{Address: "0xaaa"},
		Edges: []graphcache.Edge{
			{Counterparty: "0xbbb", Direction: "OUT", Token: "usdt", Outflow: "1000", TxCount: 10},
		},
	}
	cands, err := p.Plan(context.Background(), "inv-1", "bsc", 56, res, invcache.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Fatalf("FULL HIT 不应生成下载候选: %+v", cands)
	}
}

func TestQueueDedupeAndUpgrade(t *testing.T) {
	q, err := NewQueue(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := Candidate{
		ChainID: 56, ChainKey: "bsc", Address: "0xbbb", Priority: PriorityHOT,
		RequiredDatasets: GraphBundle(), FromBlock: 1, ToBlock: 10, InvestigationID: "inv-1",
		Score: 80, CreatedAt: time.Now().UTC(),
	}
	j1, created, err := q.Enqueue(c)
	if err != nil || !created {
		t.Fatalf("首次入队失败: created=%v err=%v", created, err)
	}
	j2, created, err := q.Enqueue(c)
	if err != nil || created {
		t.Fatalf("重复入队应去重: created=%v err=%v", created, err)
	}
	if j1.ID != j2.ID {
		t.Fatalf("去重后任务 ID 应一致: %s != %s", j1.ID, j2.ID)
	}
	if err := q.Upgrade(j1.ID); err != nil {
		t.Fatal(err)
	}
	got := q.Get(j1.ID)
	if got.Status != StatusInteractive || got.UpgradeCount != 1 {
		t.Fatalf("升级状态错误: %+v", got)
	}
	if len(q.FindByAddress("bsc", "0xBBB")) != 1 {
		t.Fatal("按地址查找失败")
	}
	// 同地址不同数据集 Bundle → 去重并合并数据集（设计 §73 Case E）
	c2 := c
	c2.RequiredDatasets = MinimalBundle()
	c2.Score = 95
	j3, created, err := q.Enqueue(c2)
	if err != nil || created {
		t.Fatalf("同地址应去重: created=%v err=%v", created, err)
	}
	if j3.ID != j1.ID {
		t.Fatalf("同地址任务 ID 应一致: %s != %s", j3.ID, j1.ID)
	}
	if len(q.Get(j1.ID).Candidate.RequiredDatasets) != 4 {
		t.Fatalf("数据集应合并为 4 个: %v", q.Get(j1.ID).Candidate.RequiredDatasets)
	}
}

func TestBudgetAllowConsumeRelease(t *testing.T) {
	b, err := NewBudgetStore(t.TempDir(), Budget{MaxActivePrefetchJobs: 1, MaxPrefetchAddresses: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Allow(); err != nil {
		t.Fatal(err)
	}
	_ = b.Consume()
	if err := b.Allow(); err == nil {
		t.Fatal("活动任务超限应拒绝")
	}
	_ = b.Release()
	if err := b.Allow(); err != nil {
		t.Fatalf("释放后应允许: %v", err)
	}
}

func TestFeedbackHitRate(t *testing.T) {
	f, err := NewFeedback(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = f.RecordUse("inv-1", "0xaaa", "b1", 43.8)
	_ = f.RecordUse("inv-1", "0xaaa", "b2", 10)
	_ = f.RecordUnused("inv-1", "0xaaa", "b3", 100)
	st := f.Stats()
	if st.Total != 3 || st.Used != 2 || st.HitRate != 2.0/3.0 {
		t.Fatalf("反馈统计错误: %+v", st)
	}
	if f.ReuseProbability("0xaaa") != 2.0/3.0 {
		t.Fatalf("复用概率错误: %v", f.ReuseProbability("0xaaa"))
	}
}

func TestEvictionPolicy(t *testing.T) {
	p := DefaultDiskPolicy()
	if p.Action(0.5) != DiskNone {
		t.Fatal("50% 不应触发策略")
	}
	if p.Action(0.85) != DiskPauseWarm {
		t.Fatal("85% 应暂停 WARM")
	}
	if p.Action(0.92) != DiskPauseAll {
		t.Fatal("92% 应暂停全部")
	}
	if p.Action(0.97) != DiskBlockNew {
		t.Fatal("97% 应禁止新建")
	}
	if EvictionScore(24*7, 1, 500, 0, 0.1) <= 0 {
		t.Fatal("未使用高分驱逐分应为正")
	}
}

type fakeManagerEnv struct {
	created  []smartdownload.CreateBatchRequest
	started  []string
	paused   []string
	resumed  []string
	statuses map[string]string
	userTasks int
}

func TestManagerLifecycle(t *testing.T) {
	root := t.TempDir()
	q, err := NewQueue(root)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := NewBudgetStore(root, DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	fb, err := NewFeedback(root)
	if err != nil {
		t.Fatal(err)
	}
	invStore := invcache.NewStore(root)
	env := &fakeManagerEnv{statuses: map[string]string{}}
	cb := BatchCallbacks{
		Create: func(_ context.Context, req smartdownload.CreateBatchRequest) (*smartdownload.CreateBatchResponse, error) {
			env.created = append(env.created, req)
			return &smartdownload.CreateBatchResponse{Batch: &smartdownload.BatchJob{ID: "batch-" + req.Addresses[0]}}, nil
		},
		Start: func(id string) error { env.started = append(env.started, id); return nil },
		Pause: func(id string) error { env.paused = append(env.paused, id); return nil },
		Resume: func(id string) error { env.resumed = append(env.resumed, id); return nil },
		BatchStatus: func(id string) (string, bool) {
			s := env.statuses[id]
			return s, s == "COMPLETED" || s == "FAILED"
		},
		CoverageQuery: func(_, _, _ string, _, _ uint64) graphcache.CoverageInfo {
			return graphcache.CoverageInfo{Ratio: 0}
		},
		ChainID: func(_ string) int64 { return 56 },
		ActiveUserTasks: func() int { return env.userTasks },
	}
	cfg := DefaultConfig()
	cfg.Interval = time.Hour // 手动 tick，不用后台循环
	m := NewManager(q, budget, fb, nil, invStore, cb, cfg)
	c := Candidate{
		ChainID: 56, ChainKey: "bsc", Address: "0xbbb", Priority: PriorityHOT,
		RequiredDatasets: MinimalBundle(), FromBlock: 100, ToBlock: 200,
		InvestigationID: "inv-1", Score: 90, CreatedAt: time.Now().UTC(),
	}
	if _, _, err := q.Enqueue(c); err != nil {
		t.Fatal(err)
	}
	if err := m.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(env.started) != 1 {
		t.Fatalf("应启动 1 个批处理: %v", env.started)
	}
	j := q.List()[0]
	if j.Status != StatusPrefetching || j.BatchID == "" {
		t.Fatalf("任务状态错误: %+v", j)
	}
	// 用户点击升级：同一批处理继续
	env.statuses[j.BatchID] = "RUNNING"
	if err := m.Upgrade("inv-1", "bsc", "0xbbb"); err != nil {
		t.Fatal(err)
	}
	if len(env.resumed) != 1 {
		t.Fatalf("升级应恢复批处理: %v", env.resumed)
	}
	// 前台任务占用 → 预取暂停
	env.userTasks = 1
	_ = m.tick(context.Background())
	if q.Get(j.ID).Status != StatusPaused {
		t.Fatalf("前台占用应暂停预取: %s", q.Get(j.ID).Status)
	}
	// 完成后就绪
	env.userTasks = 0
	env.statuses[j.BatchID] = "COMPLETED"
	_ = m.tick(context.Background())
	if q.Get(j.ID).Status != StatusReady {
		t.Fatalf("完成后应 READY: %s", q.Get(j.ID).Status)
	}
	if env.created[0].Prefetch != true || env.created[0].PrefetchPriority != 3 {
		t.Fatalf("预取标记错误: %+v", env.created[0])
	}
}

func TestActiveRegistry(t *testing.T) {
	r, err := NewActiveRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !r.Acquire("bsc", "0xaaa", "b1", 100, 200) {
		t.Fatal("首次 Acquire 应成功")
	}
	if r.Acquire("bsc", "0xaaa", "b2", 100, 200) {
		t.Fatal("同范围不应重复 Acquire")
	}
	if len(r.List()) != 1 {
		t.Fatalf("活动范围数量错误: %d", len(r.List()))
	}
	r.Release("bsc", "0xaaa", "b1", 100, 200)
	if len(r.List()) != 0 {
		t.Fatal("Release 后应清空")
	}
}

func TestLeaseStore(t *testing.T) {
	s := NewLeaseStore(t.TempDir(), time.Minute)
	if err := s.Acquire("job1", "b1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Renew("job1"); err != nil {
		t.Fatal(err)
	}
	if l := s.Get("job1"); l == nil || l.BatchID != "b1" || l.HeartbeatAt.IsZero() {
		t.Fatalf("租约错误: %+v", l)
	}
	s.Release("job1")
	if s.Get("job1") != nil {
		t.Fatal("Release 后租约应不存在")
	}
}

func TestNextStageCandidate(t *testing.T) {
	c := Candidate{ToBlock: 10_000_000, ProgressiveStage: 0}
	next, ok := NextStageCandidate(c, DefaultProgressiveStages())
	if !ok || next.ProgressiveStage != 1 || next.FromBlock != 10_000_000-201600 {
		t.Fatalf("Stage1 错误: %+v", next)
	}
	next2, ok := NextStageCandidate(next, DefaultProgressiveStages())
	if !ok || next2.ProgressiveStage != 2 || next2.FromBlock != 10_000_000-2592000 {
		t.Fatalf("Stage2 错误: %+v", next2)
	}
	if _, ok := NextStageCandidate(next2, DefaultProgressiveStages()); ok {
		t.Fatal("超过最大阶段应返回 false")
	}
}

func TestReorgPolicy(t *testing.T) {
	p := ReorgPolicy{SafetyBlocks: 20}
	if !p.SafeToFinalize(100, 120) {
		t.Fatal("越出安全窗口应可最终化")
	}
	if p.SafeToFinalize(100, 115) {
		t.Fatal("安全窗口内不应最终化")
	}
	if p.SafeToFinalize(100, 0) {
		t.Fatal("未知高度应保守不可最终化")
	}
}

func TestManagerReorgGate(t *testing.T) {
	root := t.TempDir()
	q, _ := NewQueue(root)
	budget, _ := NewBudgetStore(root, DefaultBudget())
	fb, _ := NewFeedback(root)
	env := &fakeManagerEnv{statuses: map[string]string{}}
	cb := BatchCallbacks{
		Create: func(_ context.Context, req smartdownload.CreateBatchRequest) (*smartdownload.CreateBatchResponse, error) {
			return &smartdownload.CreateBatchResponse{Batch: &smartdownload.BatchJob{ID: "b1"}}, nil
		},
		Start:       func(string) error { return nil },
		BatchStatus: func(string) (string, bool) { return "RUNNING", false },
		HeadBlock:   func() uint64 { return 100 },
	}
	m := NewManager(q, budget, fb, nil, invcache.NewStore(root), cb, Config{Interval: time.Hour})
	m.SetReorgPolicy(ReorgPolicy{SafetyBlocks: 20})
	_, _, _ = q.Enqueue(Candidate{
		ChainKey: "bsc", Address: "0xbbb", Priority: PriorityHOT,
		RequiredDatasets: GraphBundle(), FromBlock: 100, ToBlock: 200,
		InvestigationID: "inv-1", Score: 90, CreatedAt: time.Now().UTC(),
	})
	_ = m.tick(context.Background())
	if len(env.created) != 0 {
		t.Fatal("Reorg 窗口内不应启动批处理")
	}
}
