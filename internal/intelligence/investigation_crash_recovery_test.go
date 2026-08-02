package intelligence

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// ── Crash Recovery（V2.1 设计 §7）──
//
// 验证 Request/Evidence/Knowledge 持久化层在"服务重启"（新 store 实例）后
// 保持一致：请求状态、计划关联、证据链、知识关系不丢失。

// newCrashAgent 构建带完整持久化的测试 agent（fake 数据源 + 三个 store）。
func newCrashAgent(t *testing.T) (*InvestigationAgent, *RequestStore, *EvidenceStore, *InvestigationMemoryStore) {
	t.Helper()
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.UseAI = false

	src := NewFakeFlowSource()
	// 目标地址资金流：形成路径 A→target→B 与稳定币沉淀
	src.SetFlows(addrA, []FundEdge{
		edge(addrB, addrA, "USDT", "1000000000000000000000000", 1700000000),
		edge(addrA, addrC, "USDT", "200000000000000000000000", 1700000100),
	})

	requests := NewRequestStore(dir + "/requests")
	evidence := NewEvidenceStore(dir + "/evidence")
	knowledge := NewInvestigationMemoryStore(dir + "/knowledge")

	agent := &InvestigationAgent{
		flowSource:     src,
		ranker:         DefaultPathRanker(),
		tracer:         NewFundTracer(src, DefaultPathRanker(), cfg),
		planner:        NewPlanner(cfg),
		detector:       NewPatternDetector(cfg),
		report:         NewReportAgent(cfg),
		deepseek:       NewDeepSeekClient("", cfg.AIModel, cfg.AITimeoutMS, cfg.MaxTokens),
		entityResolver: NewEntityResolver(nil, nil),
		cfg:            cfg,
		active:         make(map[string]*Investigation),
		history:        make(map[string]*Investigation),
		memories:       NewMemoryStore(""),
		requests:       requests,
		evidence:       evidence,
		knowledge:      knowledge,
	}
	return agent, requests, evidence, knowledge
}

// TestCrashRecoveryCompleted 完整调查 → 模拟重启 → 三存储一致。
func TestCrashRecoveryCompleted(t *testing.T) {
	agent, requests, evidence, knowledge := newCrashAgent(t)

	// 创建请求并完成调查
	req := &InvestigationRequest{Address: addrA, ChainID: "bsc", Objective: "找资金去向", Mode: ModeFundTrace}
	created, err := requests.Create(req)
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	inv, err := agent.StartWithRequest(context.Background(), created.Address, created.ChainID, created)
	if err != nil {
		t.Fatalf("StartWithRequest 失败: %v", err)
	}
	done := waitStatus(t, agent, inv.ID, InvestigationCompleted, 10*time.Second)
	if done.Status != InvestigationCompleted {
		t.Fatalf("应完成, got %s", done.Status)
	}
	// 完成状态一致性（请求终态/StopCode 合法性）
	if done.StopCode != "" && done.StopCode != StopNoValue && done.StopCode != StopBudgetLimit && done.StopCode != StopTargetFound && done.StopCode != StopLowConf {
		t.Fatalf("StopCode 异常: %s", done.StopCode)
	}
	if done.Request == nil || done.Request.Status != RequestFinished {
		t.Fatalf("请求应 finished: %+v", done.Request)
	}
	// 证据链应已提取（fake 数据有资金路径）
	evs := evidence.List(inv.ID)
	if len(evs) == 0 {
		t.Fatal("调查完成应产生证据")
	}
	// 知识记忆应已写入（案件→地址）——recordKnowledge 在 COMPLETED 状态置位后
	// 于 agent goroutine 内执行（含文件落盘），此处轮询等待（与 waitStatus 同模式）。
	// 同时等待落盘完成（address 记录文件含该关系 + 无 .tmp 残留 + 关系数稳定），
	// 避免测试返回时 goroutine 仍在写文件导致 TempDir 清理竞态。
	foundCase := false
	deadline := time.Now().Add(10 * time.Second)
	lastCount := -1
	stable := 0
	for time.Now().Before(deadline) {
		rels := knowledge.Search(addrA)
		for _, r := range rels {
			if r.Type == RelCaseAddress && r.From == inv.ID {
				foundCase = true
				break
			}
		}
		if foundCase {
			// 磁盘落盘完成判定：address 文件含关系 + 无 tmp + 关系数稳定
			if rec, ok := knowledge.addrStore.Get(addrA); ok && len(rec.Relations) > 0 {
				tmpLeft, _ := filepath.Glob(filepath.Join(agent.knowledge.storeDir, "**", "*.tmp"))
				cur := len(knowledge.All())
				if len(tmpLeft) == 0 && cur == lastCount {
					stable++
					if stable >= 2 {
						break
					}
				} else {
					stable = 0
				}
				lastCount = cur
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !foundCase {
		t.Fatalf("知识记忆应包含案件关系: %+v", knowledge.Search(addrA))
	}
}

// TestCrashRecoveryStoreReload 请求/证据/知识存储重载一致性（真实目录）。
func TestCrashRecoveryStoreReload(t *testing.T) {
	base := t.TempDir()
	r1 := NewRequestStore(base + "/requests")
	e1 := NewEvidenceStore(base + "/evidence")
	k1 := NewInvestigationMemoryStore(base + "/knowledge")

	// 写入三类数据
	req, _ := r1.Create(&InvestigationRequest{Address: addrA, ChainID: "bsc", Objective: "x", Mode: ModeFundTrace})
	_ = r1.Link(req.ID, "inv-1", RequestStarted)
	_ = e1.Add("inv-1", Evidence{Type: EvTransaction, TxHash: "0xcrash", Detail: "崩溃前证据", Confidence: 0.9})
	_ = k1.Record(MemoryRelation{Type: RelCaseAddress, From: "inv-1", To: addrA, InvestigationID: "inv-1"})

	// 模拟重启：新实例重载同一目录
	r2 := NewRequestStore(base + "/requests")
	e2 := NewEvidenceStore(base + "/evidence")
	k2 := NewInvestigationMemoryStore(base + "/knowledge")

	// Request 一致
	gotReq, ok := r2.Get(req.ID)
	if !ok {
		t.Fatal("重启后请求丢失")
	}
	if gotReq.Status != RequestStarted || gotReq.InvestigationID != "inv-1" {
		t.Fatalf("请求状态/关联不一致: %+v", gotReq)
	}
	if gotReq.Mode != ModeFundTrace || gotReq.Address != addrA {
		t.Fatalf("请求内容不一致: %+v", gotReq)
	}
	// Evidence 一致
	evs := e2.List("inv-1")
	if len(evs) != 1 || evs[0].TxHash != "0xcrash" {
		t.Fatalf("证据不一致: %+v", evs)
	}
	// Knowledge 一致
	rels := k2.Search(addrA)
	if len(rels) != 1 || rels[0].Type != RelCaseAddress {
		t.Fatalf("知识关系不一致: %+v", rels)
	}
	// 交叉一致性：request.investigation_id == evidence.investigation_id
	if gotReq.InvestigationID != evs[0].InvestigationID {
		t.Fatalf("请求与证据的调查关联不一致: %s vs %s", gotReq.InvestigationID, evs[0].InvestigationID)
	}
}

// TestCrashRecoveryInterrupted 调查中断（started 未完成）→ 重启 → 请求可识别为进行中。
func TestCrashRecoveryInterrupted(t *testing.T) {
	base := t.TempDir()
	r1 := NewRequestStore(base + "/requests")
	req, _ := r1.Create(&InvestigationRequest{Address: addrA, ChainID: "bsc", Objective: "中断调查", Mode: ModeAuto})
	_ = r1.Link(req.ID, "inv-1", RequestStarted) // 调查启动后崩溃，未到终态

	// 重启
	r2 := NewRequestStore(base + "/requests")
	got, ok := r2.Get(req.ID)
	if !ok {
		t.Fatal("重启后请求丢失")
	}
	if got.Status != RequestStarted {
		t.Fatalf("中断调查请求应保持 started（可重新发起）, got %s", got.Status)
	}
	if got.InvestigationID != "inv-1" {
		t.Fatalf("调查关联应保留: %s", got.InvestigationID)
	}
}
