package downloadscheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/parquetdownload"
)

const (
	addrA = "0x57136ea9b2be6cd4ad74c3ca5b24172f87c9cb8d"
	addrB = "0x238a03c0dcb0f0c4c4c5b6b7c8c9d0e1f2a3b4c5"
)

// ── mocks ──

type mockRPCClient struct {
	balance string
	err     error
	calls   int
}

func (m *mockRPCClient) Call(ctx context.Context, chainKey, method string, params any) (json.RawMessage, string, error) {
	m.calls++
	if m.err != nil {
		return nil, "", m.err
	}
	return json.RawMessage(`"0xde0b6b3a7640000"`), "mock", nil // 1e18
}

type mockSQDEngine struct {
	jobStatus string
	progress  float64
	startErr  error
	getErr    error
	started   bool
	lastFromBlock uint64
	lastToBlock   uint64
}

func (m *mockSQDEngine) Start(ctx context.Context, req parquetdownload.StartRequest) (*parquetdownload.Job, error) {
	if m.startErr != nil {
		return nil, m.startErr
	}
	m.started = true
	m.lastFromBlock = req.FromBlock
	m.lastToBlock = req.ToBlock
	return &parquetdownload.Job{
		ID:        "mock-job-1",
		ChainKey:  req.ChainKey,
		Status:    parquetdownload.StatusQueued,
		Addresses: parquetdownload.AddressSummary{Valid: len(strings.Split(req.Addresses, ","))},
	}, nil
}

func (m *mockSQDEngine) Get(id string) (*parquetdownload.Job, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return &parquetdownload.Job{ID: id, Status: m.jobStatus, Progress: m.progress}, nil
}

type mockCoverageSource struct {
	counts map[string]int64
}

func (m *mockCoverageSource) AddressTxCount(ctx context.Context, address string) (int64, error) {
	return m.counts[address], nil
}

// ── helpers ──

func newTestScheduler(rpcErr error, sqdStatus string) *Scheduler {
	rpc := &mockRPCClient{balance: "1e18", err: rpcErr}
	sqd := &mockSQDEngine{jobStatus: sqdStatus, progress: 1}
	registry := NewRegistry(
		NewRPCProvider(rpc),
		NewAWSProvider(sqd),
		NewSQDProvider(sqd),
		NewBrowserProvider(),
	)
	coverage := NewCoverageResolver(&mockCoverageSource{counts: map[string]int64{addrA: 42}})
	return NewScheduler(registry, coverage, "", DefaultBudget())
}

func req(d Dataset, addrs ...string) Requirement {
	return Requirement{Dataset: d, ChainKey: "bsc", Addresses: addrs}
}

// ── tests ──

func TestTrimAddresses(t *testing.T) {
	got := trimAddresses([]string{
		addrA,
		"  " + strings.ToUpper(addrA) + " ", // 大小写+空格 → 去重
		"not-an-address",                    // 非法
		"0x1234",                            // 短地址
		addrB,
	}, 100)
	if len(got) != 2 {
		t.Fatalf("期望 2 个地址，得到 %d: %v", len(got), got)
	}
	if got[0] != addrA || got[1] != addrB {
		t.Fatalf("地址校验/去重结果错误: %v", got)
	}
	// 预算裁剪
	cut := trimAddresses([]string{addrA, addrB}, 1)
	if len(cut) != 1 {
		t.Fatalf("预算裁剪失败: %v", cut)
	}
}

func TestRegistrySelect(t *testing.T) {
	s := newTestScheduler(nil, parquetdownload.StatusDone)
	// balance → RPC（自动可用）
	score, provider := s.Registry().Select(DatasetBalance)
	if provider == nil || score.Provider != ProviderRPC || provider.Kind() != ProviderRPC {
		t.Fatalf("balance 应选择 RPC，得到 %+v", score)
	}
	// transactions → AWS（V3 Router：历史交易优先 AWS > SQD，AWS 89 > SQD 79）
	score, provider = s.Registry().Select(DatasetTransactions)
	if provider == nil || score.Provider != ProviderAWS || provider.Kind() != ProviderAWS {
		t.Fatalf("transactions 应选择 AWS，得到 %+v", score)
	}
	// token_transfer → SQD（Logs/Transfers 优先 SQD > AWS）
	score, provider = s.Registry().Select(DatasetTokenTransfer)
	if provider == nil || score.Provider != ProviderSQD || provider.Kind() != ProviderSQD {
		t.Fatalf("token_transfer 应选择 SQD，得到 %+v", score)
	}
	// labels → Browser（手动）
	score, provider = s.Registry().Select(DatasetLabels)
	if provider == nil || !score.ManualOnly || provider.Kind() != ProviderBrowser {
		t.Fatalf("labels 应选择 Browser(manual)，得到 %+v", score)
	}
}

func TestCoverageCheck(t *testing.T) {
	s := newTestScheduler(nil, parquetdownload.StatusDone)
	res, err := s.Coverage().Check(context.Background(), "bsc", []string{addrA, addrB},
		[]Dataset{DatasetTransactions, DatasetBalance})
	if err != nil {
		t.Fatal(err)
	}
	if res.ChainKey != "bsc" {
		t.Fatalf("chain 错误: %s", res.ChainKey)
	}
	var txItem, balanceItem *Coverage
	for i := range res.Items {
		if res.Items[i].Dataset == DatasetTransactions {
			txItem = &res.Items[i]
		}
		if res.Items[i].Dataset == DatasetBalance {
			balanceItem = &res.Items[i]
		}
	}
	if txItem == nil || !txItem.Have || txItem.TxCount != 42 {
		t.Fatalf("transactions 覆盖应为 42 笔: %+v", txItem)
	}
	if balanceItem == nil || balanceItem.Have {
		t.Fatalf("balance 不应检查本地覆盖: %+v", balanceItem)
	}
}

func TestSQDProviderFromToPassthrough(t *testing.T) {
	engine := &mockSQDEngine{}
	p := NewSQDProvider(engine)
	_, err := p.Execute(context.Background(), Requirement{
		Dataset: DatasetTokenTransfer, ChainKey: "bsc",
		Addresses: []string{"0xaaa"}, FromBlock: 114474000, ToBlock: 114474500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if engine.lastFromBlock != 114474000 || engine.lastToBlock != 114474500 {
		t.Fatalf("from/to passthrough = %d-%d, want 114474000-114474500",
			engine.lastFromBlock, engine.lastToBlock)
	}
}

func TestCoverageNilSource(t *testing.T) {
	s := NewScheduler(NewRegistry(), NewCoverageResolver(nil), "", DefaultBudget())
	res, err := s.Coverage().Check(context.Background(), "bsc", []string{addrA}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 4 {
		t.Fatalf("默认数据集应检查 4 类，得到 %d", len(res.Items))
	}
	for _, item := range res.Items {
		if item.Have {
			t.Fatalf("无数据源时不应报告有覆盖: %+v", item)
		}
	}
}

func TestSubmitBudgetAndDedup(t *testing.T) {
	s := newTestScheduler(nil, parquetdownload.StatusDone)
	plan, err := s.Submit(context.Background(), []Requirement{
		req(DatasetTransactions, addrA, addrA, addrB), // 去重后 2 地址
		req(DatasetTransactions, addrA),               // 与上同 key → 丢弃
		req(DatasetBalance, addrA),
		req("bogus", addrA), // 非法数据集 → 丢弃
		req(DatasetLabels, addrA),
		req(DatasetTokenTransfer, addrA),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) != 5 {
		t.Fatalf("期望 5 个任务（transactions 按活跃度拆 2 + balance + labels + token_transfer），得到 %d", len(plan.Tasks))
	}
	// V3 §7：transactions 按活跃度分桶——addrA（42 笔，普通）与 addrB（0 笔，低活跃）应拆为不同任务
	txNotes := map[string]bool{}
	for _, task := range plan.Tasks {
		if task.Requirement.Dataset == DatasetTransactions {
			txNotes[task.Requirement.Note] = true
			if task.Requirement.Note == "" {
				t.Fatalf("transactions 任务应带活跃度说明")
			}
		}
	}
	if len(txNotes) != 2 {
		t.Fatalf("transactions 应按活跃度拆 2 个任务，得到 %d 种 note: %v", len(txNotes), txNotes)
	}
	// labels 应被标记 skipped（manual）
	found := false
	for _, task := range plan.Tasks {
		if task.Requirement.Dataset == DatasetLabels {
			found = true
			if task.Status != "skipped" {
				t.Fatalf("labels 任务应 skipped，得到 %s", task.Status)
			}
		}
	}
	if !found {
		t.Fatal("缺少 labels 任务")
	}
	// 预算：MaxTasksPerPlan=5 封顶
	plan, err = s.Submit(context.Background(), []Requirement{
		req(DatasetTransactions, addrA), req(DatasetBalance, addrA),
		req(DatasetTokenTransfer, addrA), req(DatasetTransactions, addrB),
		req(DatasetBalance, addrB), req(DatasetTokenTransfer, addrB),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) > 5 {
		t.Fatalf("任务数应 ≤5，得到 %d", len(plan.Tasks))
	}
}

func TestExecuteSuccess(t *testing.T) {
	s := newTestScheduler(nil, parquetdownload.StatusDone)
	plan, err := s.Submit(context.Background(), []Requirement{req(DatasetTransactions, addrA), req(DatasetBalance, addrA)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		p := s.Plan(plan.ID)
		if p.Status.Terminal() {
			if p.Status != StatusReady {
				t.Fatalf("期望 READY_FOR_GRAPH，得到 %s: %s", p.Status, p.StageDetail)
			}
			for _, task := range p.Tasks {
				if task.Status != "done" {
					t.Fatalf("任务 %s 状态 %s（error=%s）", task.ID, task.Status, task.Error)
				}
				if task.Requirement.Dataset == DatasetTransactions && task.Result == nil {
					t.Fatal("SQD 任务应返回结果")
				}
				if task.Requirement.Dataset == DatasetBalance && task.Result == nil {
					t.Fatal("RPC 任务应返回结果")
				}
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("计划未在期限内到达终态")
}

func TestExecuteRPCRetryThenFailed(t *testing.T) {
	s := newTestScheduler(errors.New("RPC_UNAVAILABLE: 没有健康的 RPC 节点"), parquetdownload.StatusDone)
	plan, err := s.Submit(context.Background(), []Requirement{req(DatasetBalance, addrA)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		p := s.Plan(plan.ID)
		if p.Status.Terminal() {
			if p.Status != StatusFailed {
				t.Fatalf("RPC 全失败应 FAILED，得到 %s", p.Status)
			}
			task := p.Tasks[0]
			if task.Retries != 1 {
				t.Fatalf("期望 1 次重试，得到 %d", task.Retries)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("计划未在期限内到达终态")
}

func TestExecuteSQDFailureFallbackToFailed(t *testing.T) {
	s := newTestScheduler(nil, parquetdownload.StatusFailed)
	plan, err := s.Submit(context.Background(), []Requirement{req(DatasetTransactions, addrA)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		p := s.Plan(plan.ID)
		if p.Status.Terminal() {
			if p.Status != StatusFailed {
				t.Fatalf("SQD 任务失败应 FAILED，得到 %s", p.Status)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("计划未在期限内到达终态")
}

func TestSanitizeError(t *testing.T) {
	msg := sanitizeError(errors.New("dial tcp: lookup https://rpc.example.com:443 timeout"))
	if strings.Contains(msg, "https://") {
		t.Fatalf("错误未脱敏: %s", msg)
	}
	msg = sanitizeError(errors.New(strings.Repeat("x", 1000)))
	if len(msg) > 304 { // 300 字节 + "…"（3 字节）+ 保险余量
		t.Fatalf("错误未截断: %d", len(msg))
	}
}

func TestCoverageRejectsInvalidAddress(t *testing.T) {
	s := newTestScheduler(nil, parquetdownload.StatusDone)
	// SQL 注入 payload 必须被拒绝（防注入 analyticsapi.Flows）
	_, err := s.Coverage().Check(context.Background(), "bsc",
		[]string{"0x' UNION SELECT 1 FROM read_parquet('C:/x.parquet') --"}, nil)
	if err == nil {
		t.Fatal("非法地址应被拒绝")
	}
}

func TestSubmitRejectsUnknownChain(t *testing.T) {
	s := newTestScheduler(nil, parquetdownload.StatusDone)
	r := Requirement{Dataset: DatasetBalance, ChainKey: "bogus-chain", Addresses: []string{addrA}}
	plan, err := s.Submit(context.Background(), []Requirement{r})
	if err == nil {
		t.Fatalf("全部任务被丢弃时应报错，得到计划 %+v", plan)
	}
}

func TestActivityBucketingAndChunk(t *testing.T) {
	// 620 个地址：10 个高活跃（200 笔）、110 个普通（50 笔）、500 个低活跃（0 笔）
	counts := map[string]int64{}
	var addrs []string
	for i := 0; i < 620; i++ {
		addr := fmt.Sprintf("0x%040x", i)
		addrs = append(addrs, addr)
		switch {
		case i < 10:
			counts[addr] = 200
		case i < 120:
			counts[addr] = 50
		default:
			counts[addr] = 0
		}
	}
	buckets := bucketByActivity(func(a string) int64 { return counts[a] }, addrs)
	// 高活跃 10 → chunk 20 → 1 任务
	// 普通 110 → chunk 100 → 2 任务
	// 低活跃 500 → chunk 500 → 1 任务
	total := 0
	for _, b := range buckets {
		chunk := ChunkSizeFor(b.level)
		want := (len(b.addrs) + chunk - 1) / chunk
		if b.chunks != want {
			t.Fatalf("桶 %s chunk 数期望 %d 得到 %d", b.level, want, b.chunks)
		}
		total += b.chunks
		if len(b.addrs) == 0 {
			t.Fatalf("桶 %s 不应为空", b.level)
		}
	}
	if total != 4 {
		t.Fatalf("期望 4 个 chunk 任务，得到 %d", total)
	}
	// 地址总数守恒
	sum := 0
	for _, b := range buckets {
		sum += len(b.addrs)
	}
	if sum != 620 {
		t.Fatalf("地址总数不守恒: %d", sum)
	}
}

func TestRunRejectsDuplicateExecution(t *testing.T) {
	s := newTestScheduler(nil, parquetdownload.StatusDone)
	plan, err := s.Submit(context.Background(), []Requirement{req(DatasetBalance, addrA)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	// 立即二次 Run（模拟双击/并发请求）必须被拒绝，不能重复执行
	if err := s.Run(context.Background(), plan.ID); err == nil {
		t.Fatal("重复执行应被拒绝")
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		p := s.Plan(plan.ID)
		if p.Status.Terminal() {
			if p.Status != StatusReady {
				t.Fatalf("期望 READY_FOR_GRAPH，得到 %s: %s", p.Status, p.StageDetail)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("计划未在期限内到达终态")
}

func TestLoadPlansMarksInterruptedFailed(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	interrupted := &Plan{
		ID:          "interrupted-plan",
		Status:      StatusExecuting,
		StageDetail: "开始执行",
		Budget:      DefaultBudget(),
		CreatedAt:   now,
		StartedAt:   &now,
		Tasks: []*PlanTask{{
			ID: "interrupted-plan-1", Status: "running", Provider: ProviderSQD,
			Requirement: Requirement{Dataset: DatasetTransactions, ChainKey: "bsc", Addresses: []string{addrA}},
		}},
	}
	payload, err := json.Marshal(interrupted)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "interrupted-plan.json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(NewRegistry(), NewCoverageResolver(nil), dir, DefaultBudget())
	p := s.Plan("interrupted-plan")
	if p == nil {
		t.Fatal("计划未加载")
	}
	if p.Status != StatusFailed {
		t.Fatalf("中断计划应标记 FAILED，得到 %s", p.Status)
	}
	if p.Tasks[0].Status != "failed" {
		t.Fatalf("中断任务应标记 failed，得到 %s", p.Tasks[0].Status)
	}
}

func TestDynamicScoreDegradesOnSQDHealth(t *testing.T) {
	sqd := &mockSQDEngine{jobStatus: parquetdownload.StatusDone, progress: 1}
	health := &mockHealthSource{snapshot: SQDHealthSnapshot{
		CooldownActive: true,
		CooldownUntil:  time.Now().Add(60 * time.Second).Format(time.RFC3339),
		BreakerState:   "OPEN",
		WorkerTier:     "EMERGENCY",
		Consecutive503: 4,
		SuccessRate:    0.5,
	}}
	registry := NewRegistry(NewAWSProvider(sqd), NewSQDProvider(sqd).WithHealth(health))
	scores := registry.Candidates(DatasetTransactions)
	if len(scores) != 2 {
		t.Fatalf("期望 2 个候选，得到 %d", len(scores))
	}
	if scores[0].Provider != ProviderAWS {
		t.Fatalf("SQD 不健康时 transactions 应选 AWS，得到 %+v", scores[0])
	}
	sqdScore := scores[1]
	if sqdScore.Reliability >= 80 {
		t.Fatalf("SQD 可靠性分应大幅下降，得到 %d", sqdScore.Reliability)
	}
	found := false
	for _, r := range sqdScore.Reasons {
		if r == "SQD 熔断 OPEN，请求被阻断，已降级" {
			found = true
		}
	}
	if !found {
		t.Fatalf("降级原因缺失: %v", sqdScore.Reasons)
	}
	// token_transfer：SQD 不健康但无 AWS 候选（AWS 仅 transactions）→ 仍选 SQD（降级提示）
	ts := registry.Candidates(DatasetTokenTransfer)
	if len(ts) != 1 || ts[0].Provider != ProviderSQD {
		t.Fatalf("token_transfer 应仍有 SQD 候选: %+v", ts)
	}
}

// mockHealthSource 固定健康快照。
type mockHealthSource struct {
	snapshot SQDHealthSnapshot
}

func (m *mockHealthSource) SQDHealth() SQDHealthSnapshot { return m.snapshot }

func TestRunSurvivesCallerCancel(t *testing.T) {
	// 执行生命周期独立于调用方 ctx：Run 传入已取消的 ctx，任务仍应完成
	s := newTestScheduler(nil, parquetdownload.StatusDone)
	plan, err := s.Submit(context.Background(), []Requirement{req(DatasetTransactions, addrA)})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	if err := s.Run(cancelled, plan.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		p := s.Plan(plan.ID)
		if p.Status.Terminal() {
			if p.Status != StatusReady {
				t.Fatalf("调用方 ctx 取消不应中断执行，得到 %s: %s", p.Status, p.StageDetail)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("计划未在期限内到达终态")
}
