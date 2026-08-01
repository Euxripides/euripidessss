package dynamicinvestigation

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// ── 引擎主流程测试 ──

func TestEngineStartWalletExpansion(t *testing.T) {
	src := NewFakeSource()
	exec := NewFakeExecutor()
	cfg := DefaultConfig()
	cfg.MaxDepth = 1
	cfg.UseCSVDirect = true

	// 目标地址：活跃钱包，流向两个对手
	src.SetProfile("0x0000000000000000000000000000000000000001", &ProfileSignal{TxCount: 100, InCount: 50, OutCount: 50, RiskScore: 60})
	src.SetFlows("0x0000000000000000000000000000000000000001", []FlowSignal{
		{Counterparty: "0x0000000000000000000000000000000000000002", Token: "0xtoken", Amount: "5000000", Direction: "outgoing"}, // 5M
		{Counterparty: "0x0000000000000000000000000000000000000003", Token: "0xtoken", Amount: "200000", Direction: "outgoing"},  // 200K
	})
	// wallet2 是归集地址
	src.SetProfile("0x0000000000000000000000000000000000000002", &ProfileSignal{TxCount: 300, InCount: 200, OutCount: 5, RiskScore: 85})

	engine := NewEngine(NewQueue(""), NewRecognizer(), src, exec, cfg)
	if err := engine.Start(context.Background(), "0x0000000000000000000000000000000000000001"); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	// 目标地址应 COMPLETED
	target, _ := engine.Queue().Get("0x0000000000000000000000000000000000000001")
	if target.Status != StatusCompleted {
		t.Fatalf("目标地址应 COMPLETED, got %s", target.Status)
	}

	// 下一跳：wallet2（大金额）应被批准采集；wallet3（金额低于阈值？阈值0所以也发现）
	w2, ok := engine.Queue().Get("0x0000000000000000000000000000000000000002")
	if !ok {
		t.Fatal("wallet2 应被发现")
	}
	if w2.Depth != 1 {
		t.Fatalf("wallet2 深度应为 1, got %d", w2.Depth)
	}
	// wallet2 归集 → 实体 exchange → CSV 直链（UseCSVDirect=true）
	if w2.Entity != EntityExchange {
		t.Fatalf("wallet2 应识别为 exchange, got %s", w2.Entity)
	}
	// 执行器应收到任务
	tasks := exec.Tasks()
	if len(tasks) == 0 {
		t.Fatal("执行器应收到任务")
	}
}

func TestEngineAmountThresholdFilter(t *testing.T) {
	src := NewFakeSource()
	exec := NewFakeExecutor()
	cfg := DefaultConfig()
	cfg.MaxDepth = 1
	cfg.AmountThreshold = "1000000" // 100 万阈值

	src.SetProfile("0x0000000000000000000000000000000000000001", &ProfileSignal{TxCount: 50, InCount: 25, OutCount: 25})
	src.SetFlows("0x0000000000000000000000000000000000000001", []FlowSignal{
		{Counterparty: "0x00000000000000000000000000000000000000b1", Token: "0xt", Amount: "5000000", Direction: "outgoing"},   // 5M ≥ 阈值
		{Counterparty: "0x00000000000000000000000000000000000000b2", Token: "0xt", Amount: "5000", Direction: "outgoing"},    // 5K < 阈值
	})

	engine := NewEngine(NewQueue(""), NewRecognizer(), src, exec, cfg)
	if err := engine.Start(context.Background(), "0x0000000000000000000000000000000000000001"); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	if _, ok := engine.Queue().Get("0x00000000000000000000000000000000000000b1"); !ok {
		t.Fatal("大金额地址应被发现")
	}
	if _, ok := engine.Queue().Get("0x00000000000000000000000000000000000000b2"); ok {
		t.Fatal("低于金额阈值的地址不应被发现")
	}
}

func TestEngineMaxAddresses(t *testing.T) {
	src := NewFakeSource()
	exec := NewFakeExecutor()
	cfg := DefaultConfig()
	cfg.MaxDepth = 1
	cfg.MaxAddresses = 2 // 队列上限 2（目标 + 1 个发现）

	src.SetProfile("0x0000000000000000000000000000000000000001", &ProfileSignal{TxCount: 100, InCount: 50, OutCount: 50, RiskScore: 80})
	src.SetFlows("0x0000000000000000000000000000000000000001", []FlowSignal{
		{Counterparty: "0x00000000000000000000000000000000000000a1", Token: "0xt", Amount: "10000000", Direction: "outgoing"},
		{Counterparty: "0x00000000000000000000000000000000000000a2", Token: "0xt", Amount: "10000000", Direction: "outgoing"},
		{Counterparty: "0x00000000000000000000000000000000000000a3", Token: "0xt", Amount: "10000000", Direction: "outgoing"},
	})

	engine := NewEngine(NewQueue(""), NewRecognizer(), src, exec, cfg)
	if err := engine.Start(context.Background(), "0x0000000000000000000000000000000000000001"); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	// 硬上限：队列总数严格 ≤ max_addresses（满后不再添加）
	if engine.Queue().Total() > cfg.MaxAddresses {
		t.Fatalf("队列总数应 ≤ max_addresses(%d), got %d", cfg.MaxAddresses, engine.Queue().Total())
	}
	// 目标 + 首个关联地址应在队列中
	if _, ok := engine.Queue().Get("0x0000000000000000000000000000000000000001"); !ok {
		t.Fatal("目标地址应在队列中")
	}
	if _, ok := engine.Queue().Get("0x00000000000000000000000000000000000000a1"); !ok {
		t.Fatal("首个关联地址应被发现")
	}
	// 超限地址（a2/a3）不应入队
	if _, ok := engine.Queue().Get("0x00000000000000000000000000000000000000a2"); ok {
		t.Fatal("超限地址 a2 不应入队")
	}
	if _, ok := engine.Queue().Get("0x00000000000000000000000000000000000000a3"); ok {
		t.Fatal("超限地址 a3 不应入队")
	}
}

func TestEngineMaxDepth(t *testing.T) {
	src := NewFakeSource()
	exec := NewFakeExecutor()
	cfg := DefaultConfig()
	cfg.MaxDepth = 1

	src.SetProfile("0x0000000000000000000000000000000000000001", &ProfileSignal{TxCount: 20, InCount: 10, OutCount: 10})
	src.SetFlows("0x0000000000000000000000000000000000000001", []FlowSignal{
		{Counterparty: "0x00000000000000000000000000000000000000e1", Token: "0xt", Amount: "10000000", Direction: "outgoing"},
	})
	src.SetProfile("0x00000000000000000000000000000000000000e1", &ProfileSignal{TxCount: 30, InCount: 15, OutCount: 15})
	src.SetFlows("0x00000000000000000000000000000000000000e1", []FlowSignal{
		{Counterparty: "0x00000000000000000000000000000000000000e2", Token: "0xt", Amount: "10000000", Direction: "outgoing"},
	})

	engine := NewEngine(NewQueue(""), NewRecognizer(), src, exec, cfg)
	if err := engine.Start(context.Background(), "0x0000000000000000000000000000000000000001"); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	// 深度 2 的地址不应被发现（maxDepth=1）
	if _, ok := engine.Queue().Get("0x00000000000000000000000000000000000000e2"); ok {
		t.Fatal("超过 maxDepth 的地址不应被发现")
	}
	if _, ok := engine.Queue().Get("0x00000000000000000000000000000000000000e1"); !ok {
		t.Fatal("深度 1 地址应被发现")
	}
}

func TestEngineExecutorFailure(t *testing.T) {
	src := NewFakeSource()
	exec := NewFakeExecutor()
	exec.FailOn(AcquisitionSQDLogs, "模拟 SQD 故障")
	cfg := DefaultConfig()
	cfg.MaxDepth = 0 // 只处理目标

	src.SetProfile("0x0000000000000000000000000000000000000001", &ProfileSignal{TxCount: 100, InCount: 50, OutCount: 50, RiskScore: 90})

	engine := NewEngine(NewQueue(""), NewRecognizer(), src, exec, cfg)
	if err := engine.Start(context.Background(), "0x0000000000000000000000000000000000000001"); err != nil {
		t.Fatalf("Start 不应因单地址采集失败而失败: %v", err)
	}

	target, _ := engine.Queue().Get("0x0000000000000000000000000000000000000001")
	if target.Status != StatusIgnored {
		t.Fatalf("采集失败后应回退为 IGNORED, got %s", target.Status)
	}
	if target.IgnoredReason == "" {
		t.Fatal("采集失败应记录原因")
	}
	tasks := engine.Tasks()
	if len(tasks) != 1 {
		t.Fatalf("应有 1 个任务记录, got %d", len(tasks))
	}
	if tasks[0].Status != "failed" {
		t.Fatalf("任务应标记 failed, got %s", tasks[0].Status)
	}
}

func TestEngineRelationsOnly(t *testing.T) {
	src := NewFakeSource()
	exec := NewFakeExecutor()
	cfg := DefaultConfig()
	cfg.UseSQD = false // 禁用 SQD → 仅保存关系
	cfg.UseCSVDirect = false
	cfg.MaxDepth = 0

	// 高信号目标：评分 ACQUIRE，但采集通道全部禁用 → RELATION_ONLY
	src.SetProfile("0x0000000000000000000000000000000000000001", &ProfileSignal{TxCount: 500, InCount: 200, OutCount: 200, RiskScore: 80})

	engine := NewEngine(NewQueue(""), NewRecognizer(), src, exec, cfg)
	if err := engine.Start(context.Background(), "0x0000000000000000000000000000000000000001"); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	target, _ := engine.Queue().Get("0x0000000000000000000000000000000000000001")
	if target.Acquisition != AcquisitionRelationsOnly {
		t.Fatalf("禁用采集时应仅保存关系, got %s", target.Acquisition)
	}
	if target.Status != StatusCompleted {
		t.Fatalf("仅保存关系应直接 COMPLETED, got %s", target.Status)
	}
}

func TestEngineStats(t *testing.T) {
	src := NewFakeSource()
	exec := NewFakeExecutor()
	cfg := DefaultConfig()
	cfg.MaxDepth = 0

	src.SetProfile("0x0000000000000000000000000000000000000001", &ProfileSignal{TxCount: 100, InCount: 50, OutCount: 50, RiskScore: 80})

	engine := NewEngine(NewQueue(""), NewRecognizer(), src, exec, cfg)
	if err := engine.Start(context.Background(), "0x0000000000000000000000000000000000000001"); err != nil {
		t.Fatal(err)
	}
	stats := engine.Stats()
	if stats.TotalCompleted != 1 {
		t.Fatalf("TotalCompleted 应为 1, got %d", stats.TotalCompleted)
	}
	if stats.TotalTasks != 1 {
		t.Fatalf("TotalTasks 应为 1, got %d", stats.TotalTasks)
	}
	if stats.LastRun == nil {
		t.Fatal("LastRun 应被记录")
	}
}

// TestEngineCSVClusterDedup 验证 CSV 直链按实体簇批量去重：同簇 N 个地址只生成 1 个任务。
func TestEngineCSVClusterDedup(t *testing.T) {
	src := NewFakeSource()
	exec := NewFakeExecutor()
	cfg := DefaultConfig()
	cfg.MaxDepth = 1
	cfg.UseCSVDirect = true
	cfg.MinScore = 10 // 归集地址（exchange）评分应达批准线

	// 目标：钱包，流向两个归集地址（同簇 exchange）
	src.SetProfile("0x0000000000000000000000000000000000000001", &ProfileSignal{TxCount: 100, InCount: 50, OutCount: 50, RiskScore: 70})
	src.SetFlows("0x0000000000000000000000000000000000000001", []FlowSignal{
		{Counterparty: "0x00000000000000000000000000000000000000c1", Token: "0xt", Amount: "50000000", Direction: "outgoing"},
		{Counterparty: "0x00000000000000000000000000000000000000c2", Token: "0xt", Amount: "50000000", Direction: "outgoing"},
	})
	// 两个归集地址（入多出少 → exchange）
	src.SetProfile("0x00000000000000000000000000000000000000c1", &ProfileSignal{TxCount: 200, InCount: 180, OutCount: 5, RiskScore: 80})
	src.SetProfile("0x00000000000000000000000000000000000000c2", &ProfileSignal{TxCount: 200, InCount: 190, OutCount: 4, RiskScore: 80})

	engine := NewEngine(NewQueue(""), NewRecognizer(), src, exec, cfg)
	if err := engine.Start(context.Background(), "0x0000000000000000000000000000000000000001"); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	// CSV_DIRECT 任务应只有 1 个，且包含两个归集地址
	csvTasks := 0
	for _, task := range engine.Tasks() {
		if task.Mode == AcquisitionCSVDirect {
			csvTasks++
			if len(task.Addresses) != 2 {
				t.Fatalf("CSV 任务应包含 2 个簇成员, got %v", task.Addresses)
			}
		}
	}
	if csvTasks != 1 {
		t.Fatalf("同簇 CSV 应只生成 1 个任务, got %d", csvTasks)
	}
	// 两个归集地址应 COMPLETED 并关联同一 JobID
	s1, _ := engine.Queue().Get("0x00000000000000000000000000000000000000c1")
	s2, _ := engine.Queue().Get("0x00000000000000000000000000000000000000c2")
	if s1.Status != StatusCompleted || s2.Status != StatusCompleted {
		t.Fatalf("簇成员应 COMPLETED: s1=%s s2=%s", s1.Status, s2.Status)
	}
	if s1.JobID == "" || s1.JobID != s2.JobID {
		t.Fatalf("簇成员应共享 JobID: %s vs %s", s1.JobID, s2.JobID)
	}
}

// TestEngineAsyncExecutorKeepsAcquiring 验证异步执行器（真实 Manager.Start 风格）：
// 任务启动后保持 running，地址停留在 ACQUIRING 并关联 JobID，不误标 COMPLETED。
func TestEngineAsyncExecutorKeepsAcquiring(t *testing.T) {
	src := NewFakeSource()
	exec := &asyncExecutor{}
	cfg := DefaultConfig()
	cfg.MaxDepth = 0

	src.SetProfile("0x0000000000000000000000000000000000000001", &ProfileSignal{TxCount: 100, InCount: 50, OutCount: 50, RiskScore: 80})

	engine := NewEngine(NewQueue(""), NewRecognizer(), src, exec, cfg)
	if err := engine.Start(context.Background(), "0x0000000000000000000000000000000000000001"); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	target, _ := engine.Queue().Get("0x0000000000000000000000000000000000000001")
	if target.Status != StatusAcquiring {
		t.Fatalf("异步执行中地址应保持 ACQUIRING, got %s", target.Status)
	}
	if target.JobID == "" {
		t.Fatal("异步执行应关联 JobID")
	}
	tasks := engine.Tasks()
	if len(tasks) != 1 || tasks[0].Status != "running" {
		t.Fatalf("异步任务应保持 running: %+v", tasks)
	}
}

// asyncExecutor 模拟真实执行器：启动任务后保持 running（异步）。
type asyncExecutor struct{}

func (a *asyncExecutor) Execute(_ context.Context, task *AcquisitionTask) error {
	task.SetStatus("running")
	task.SetJobID("async-job-1")
	return nil
}

// failCSVExecutor 模拟 CSV 批量任务失败（同步失败）。
type failCSVExecutor struct{}

func (f *failCSVExecutor) Execute(_ context.Context, task *AcquisitionTask) error {
	if task.Mode == AcquisitionCSVDirect {
		return &fakeErr{"CSV 直链下载启动失败: 模拟故障"}
	}
	task.SetStatus("done")
	task.SetJobID("fake-job-" + task.TaskID)
	return nil
}

// TestEngineCSVClusterFailure 验证簇任务失败时成员回退 IGNORED（不留 ACQUIRING 孤儿）。
func TestEngineCSVClusterFailure(t *testing.T) {
	src := NewFakeSource()
	exec := &failCSVExecutor{}
	cfg := DefaultConfig()
	cfg.MaxDepth = 1
	cfg.MinScore = 10

	src.SetProfile("0x0000000000000000000000000000000000000001", &ProfileSignal{TxCount: 100, InCount: 50, OutCount: 50, RiskScore: 70})
	src.SetFlows("0x0000000000000000000000000000000000000001", []FlowSignal{
		{Counterparty: "0x00000000000000000000000000000000000000c1", Token: "0xt", Amount: "50000000", Direction: "outgoing"},
		{Counterparty: "0x00000000000000000000000000000000000000c2", Token: "0xt", Amount: "50000000", Direction: "outgoing"},
	})
	src.SetProfile("0x00000000000000000000000000000000000000c1", &ProfileSignal{TxCount: 200, InCount: 180, OutCount: 5, RiskScore: 80})
	src.SetProfile("0x00000000000000000000000000000000000000c2", &ProfileSignal{TxCount: 200, InCount: 190, OutCount: 4, RiskScore: 80})

	engine := NewEngine(NewQueue(""), NewRecognizer(), src, exec, cfg)
	if err := engine.Start(context.Background(), "0x0000000000000000000000000000000000000001"); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	// 簇任务失败 → 所有成员应 IGNORED 且带原因，无 ACQUIRING 孤儿
	for _, addr := range []string{"0x00000000000000000000000000000000000000c1", "0x00000000000000000000000000000000000000c2"} {
		item, ok := engine.Queue().Get(addr)
		if !ok {
			t.Fatalf("%s 应存在", addr)
		}
		if item.Status != StatusIgnored {
			t.Fatalf("%s 簇失败后应为 IGNORED, got %s", addr, item.Status)
		}
		if item.IgnoredReason == "" {
			t.Fatalf("%s 簇失败应有原因", addr)
		}
	}
}

// TestConfigClamping 验证 config 部分更新的钳制：负值/零值/越界值/非法链被规范化。
func TestConfigClamping(t *testing.T) {
	h := newTestHandler()
	rr := doJSON(h, http.MethodPost, "/dynamic-investigation/config",
		`{"max_depth":-1,"max_addresses":0,"relations_per_address":0,"min_score":-5,"risk_weight":-10}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("更新 config 应 200, got %d", rr.Code)
	}
	cfg := h.engine.Config()
	if cfg.MaxDepth != 0 {
		t.Fatalf("max_depth 负值应钳制为 0, got %d", cfg.MaxDepth)
	}
	if cfg.MaxAddresses != 1 {
		t.Fatalf("max_addresses=0 应钳制为 1, got %d", cfg.MaxAddresses)
	}
	if cfg.RelationsPerAddress != 1 {
		t.Fatalf("relations_per_address=0 应钳制为 1, got %d", cfg.RelationsPerAddress)
	}
	if cfg.MinScore < 0 || cfg.RiskWeight < 0 {
		t.Fatalf("负权重/评分应钳制为 0: min_score=%v risk_weight=%v", cfg.MinScore, cfg.RiskWeight)
	}

	// 上界钳制 + chain_id 白名单
	rr2 := doJSON(h, http.MethodPost, "/dynamic-investigation/config",
		`{"max_depth":99,"max_addresses":999999,"relations_per_address":9999,"min_score":500,"risk_weight":999,"chain_id":"0x' UNION SELECT 1--"}`)
	if rr2.Code != http.StatusOK {
		t.Fatalf("更新 config 应 200, got %d", rr2.Code)
	}
	cfg2 := h.engine.Config()
	if cfg2.MaxDepth != 10 {
		t.Fatalf("max_depth 上界应钳制为 10, got %d", cfg2.MaxDepth)
	}
	if cfg2.MaxAddresses != 10000 {
		t.Fatalf("max_addresses 上界应钳制为 10000, got %d", cfg2.MaxAddresses)
	}
	if cfg2.RelationsPerAddress != 500 {
		t.Fatalf("relations_per_address 上界应钳制为 500, got %d", cfg2.RelationsPerAddress)
	}
	if cfg2.MinScore != 100 {
		t.Fatalf("min_score 上界应钳制为 100, got %v", cfg2.MinScore)
	}
	if cfg2.RiskWeight != 100 {
		t.Fatalf("risk_weight 上界应钳制为 100, got %v", cfg2.RiskWeight)
	}
	if cfg2.ChainID != "bsc" {
		t.Fatalf("非法 chain_id 应回退 bsc, got %q", cfg2.ChainID)
	}
}

// TestSanitizeError 验证错误脱敏：绝对路径被剥离。
func TestSanitizeError(t *testing.T) {
	cases := []struct{ in, want string }{
		{"读取文件 E:\\codex\\etl\\backend\\data\\x.json 失败", "读取文件 [path] 失败"},
		{"读取文件 /var/lib/etl/x.parquet 失败", "读取文件 [path] 失败"},
		{"普通错误无路径", "普通错误无路径"},
		{"", ""},
	}
	for _, c := range cases {
		got := sanitizeError(c.in)
		if got != c.want {
			t.Fatalf("sanitizeError(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestEngineAmountThresholdRawCompare 验证金额阈值按原始数值比较（同桶内也正确）。
func TestEngineAmountThresholdRawCompare(t *testing.T) {
	src := NewFakeSource()
	exec := NewFakeExecutor()
	cfg := DefaultConfig()
	cfg.MaxDepth = 1
	cfg.AmountThreshold = "999999" // 阈值 999,999

	src.SetProfile("0x0000000000000000000000000000000000000001", &ProfileSignal{TxCount: 50, InCount: 25, OutCount: 25})
	src.SetFlows("0x0000000000000000000000000000000000000001", []FlowSignal{
		{Counterparty: "0x00000000000000000000000000000000000000e1", Token: "0xt", Amount: "900000", Direction: "outgoing"},  // 900K < 999999 → 应过滤（同桶但原始值低）
		{Counterparty: "0x00000000000000000000000000000000000000e2", Token: "0xt", Amount: "1000000", Direction: "outgoing"}, // 1M ≥ 999999 → 应保留
	})

	engine := NewEngine(NewQueue(""), NewRecognizer(), src, exec, cfg)
	if err := engine.Start(context.Background(), "0x0000000000000000000000000000000000000001"); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	if _, ok := engine.Queue().Get("0x00000000000000000000000000000000000000e2"); !ok {
		t.Fatal("≥ 阈值的地址应被发现")
	}
	if _, ok := engine.Queue().Get("0x00000000000000000000000000000000000000e1"); ok {
		t.Fatal("< 阈值的地址（同桶 100K）不应被发现——原始数值比较")
	}
}

// TestEngineExecutorErrorSanitized 验证执行失败的错误已脱敏后记录。
func TestEngineExecutorErrorSanitized(t *testing.T) {
	src := NewFakeSource()
	exec := &pathErrExecutor{}
	cfg := DefaultConfig()
	cfg.MaxDepth = 0

	src.SetProfile("0x0000000000000000000000000000000000000001", &ProfileSignal{TxCount: 100, InCount: 50, OutCount: 50, RiskScore: 90})

	engine := NewEngine(NewQueue(""), NewRecognizer(), src, exec, cfg)
	if err := engine.Start(context.Background(), "0x0000000000000000000000000000000000000001"); err != nil {
		t.Fatalf("Start 不应失败: %v", err)
	}
	target, _ := engine.Queue().Get("0x0000000000000000000000000000000000000001")
	if target.IgnoredReason == "" {
		t.Fatal("应有忽略原因")
	}
	if strings.Contains(target.IgnoredReason, `E:\codex\etl`) {
		t.Fatalf("忽略原因应脱敏路径: %q", target.IgnoredReason)
	}
	if !strings.Contains(target.IgnoredReason, "[path]") {
		t.Fatalf("忽略原因应含 [path] 占位: %q", target.IgnoredReason)
	}
}

// pathErrExecutor 返回含绝对路径的错误。
type pathErrExecutor struct{}

func (p *pathErrExecutor) Execute(_ context.Context, task *AcquisitionTask) error {
	if task.Mode == AcquisitionRelationsOnly {
		task.SetStatus("done")
		return nil
	}
	return &fakeErr{"读取文件 E:\\codex\\etl\\backend\\data\\config.json 失败"}
}
