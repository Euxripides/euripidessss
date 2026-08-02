package intelligence

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func newTestInvestigationHandler() *InvestigationHandler {
	return NewInvestigationHandler(newTestAgent(), NewRequestStore(""), NewIntentAnalyzer())
}

func TestInvestigationHandlerCreate(t *testing.T) {
	h := newTestInvestigationHandler()
	rr := doJSON(h, http.MethodPost, "/investigation/create", `{
		"address":"0x00000000000000000000000000000000000000a1",
		"chain":"bsc",
		"objective":"这是一个大额获利地址，寻找最终资金沉淀",
		"expected_result":["资金流图","交易所入口"],
		"mode":"auto"
	}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("create 应 202, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Request       InvestigationRequest `json:"request"`
		Investigation Investigation        `json:"investigation"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	if out.Request.ID == "" {
		t.Fatal("应返回请求 ID")
	}
	if out.Request.Intent == nil {
		t.Fatal("应返回意图分析结果")
	}
	if out.Request.Mode != ModeProfitAnalyze {
		t.Fatalf("auto 模式应按获利意图推断为 profit_analyze, got %s", out.Request.Mode)
	}
	if !hasGoal(out.Request.Intent.Goals, GoalProfit) || !hasGoal(out.Request.Intent.Goals, GoalFundDestination) {
		t.Fatalf("意图应包含获利与资金沉淀目标: %v", out.Request.Intent.Goals)
	}
	if out.Investigation.ID == "" || out.Investigation.Request == nil {
		t.Fatal("调查应携带请求")
	}
	if out.Request.InvestigationID != out.Investigation.ID {
		t.Fatalf("请求应回填调查 ID: request=%s inv=%s", out.Request.InvestigationID, out.Investigation.ID)
	}
	// 请求应已持久化
	got, ok := h.requests.Get(out.Request.ID)
	if !ok {
		t.Fatal("请求应持久化")
	}
	if got.Status != RequestStarted || got.InvestigationID != out.Investigation.ID {
		t.Fatalf("请求状态应 started 且回填调查: %+v", got)
	}
}

func TestInvestigationHandlerCreateValidation(t *testing.T) {
	h := newTestInvestigationHandler()
	cases := []struct {
		name string
		body string
	}{
		{"非法地址", `{"address":"0xzzz","objective":"找资金去向"}`},
		{"空请求", `{"address":"0x00000000000000000000000000000000000000a1"}`},
		{"非法模式", `{"address":"0x00000000000000000000000000000000000000a1","objective":"x","mode":"hack"}`},
		{"坏 JSON", `{`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := doJSON(h, http.MethodPost, "/investigation/create", c.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("应 400, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestInvestigationHandlerPlanAndTasks(t *testing.T) {
	h := newTestInvestigationHandler()
	rr := doJSON(h, http.MethodPost, "/investigation/create", `{
		"address":"0x00000000000000000000000000000000000000a1",
		"objective":"找资金去向",
		"expected_result":["资金流图"]
	}`)
	var out struct {
		Request       InvestigationRequest `json:"request"`
		Investigation Investigation        `json:"investigation"`
	}
	if rr.Code != http.StatusAccepted {
		t.Fatalf("create 应 202, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("create 响应解析失败: %v", err)
	}
	id := out.Investigation.ID
	if id == "" {
		t.Fatal("create 应返回调查 ID")
	}

	rr = doJSON(h, http.MethodGet, "/investigation/"+id+"/plan", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("plan 应 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var plan struct {
		InvestigationID string             `json:"investigation_id"`
		Status          string             `json:"status"`
		Plan            *InvestigationPlan `json:"plan"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &plan); err != nil {
		t.Fatalf("plan 响应解析失败: %v", err)
	}
	if plan.InvestigationID != id {
		t.Fatalf("plan 应带调查 ID: %s", plan.InvestigationID)
	}

	rr = doJSON(h, http.MethodGet, "/investigation/"+id+"/tasks", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("tasks 应 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var tasks struct {
		Tasks []InvestigationTask `json:"tasks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("tasks 响应解析失败: %v", err)
	}
	if tasks.Tasks == nil {
		t.Fatal("tasks 应为数组")
	}
}

func TestInvestigationHandlerListAndNotFound(t *testing.T) {
	h := newTestInvestigationHandler()
	doJSON(h, http.MethodPost, "/investigation/create", `{
		"address":"0x00000000000000000000000000000000000000a1",
		"objective":"识别交易所入口",
		"mode":"exchange_entry"
	}`)
	rr := doJSON(h, http.MethodGet, "/investigation/requests", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("requests 应 200, got %d", rr.Code)
	}
	var list struct {
		Total int                    `json:"total"`
		Items []InvestigationRequest `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("requests 响应解析失败: %v", err)
	}
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("应返回 1 个请求, got total=%d", list.Total)
	}

	rr = doJSON(h, http.MethodGet, "/investigation/inv-999/plan", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("不存在调查应 404, got %d", rr.Code)
	}
	rr = doJSON(h, http.MethodGet, "/investigation/nope", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("未知路径应 404, got %d", rr.Code)
	}
}

// ── Intent Analyzer 规则测试 ──

func TestIntentAnalyzerGoals(t *testing.T) {
	a := NewIntentAnalyzer()
	cases := []struct {
		name     string
		obj      string
		expect   []string // 至少包含
		wantDir  string
		wantMode InvestigationMode
	}{
		{"资金去向", "寻找最终资金沉淀", []string{GoalFundDestination}, "out", ModeFundTrace},
		{"资金来源", "追踪上游资金来源", []string{GoalFundSource}, "in", ModeFundTrace},
		{"双向", "资金从哪里来又去了哪里", []string{GoalFundSource, GoalFundDestination}, "both", ModeFundTrace},
		{"获利", "这是一个大额获利地址", []string{GoalProfit}, "unknown", ModeProfitAnalyze},
		{"交易所", "找交易所入口与提现路径", []string{GoalExchangeEntry}, "unknown", ModeExchangeEntry},
		{"关联钱包", "找关联钱包与同伙", []string{GoalRelatedWallets}, "unknown", ModeFundTrace},
		{"身份", "查找身份线索", []string{GoalIdentity}, "unknown", ModeIdentityLookup},
		{"风险", "检测洗钱风险", []string{GoalRisk}, "unknown", ModeRiskScan},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			intent := a.Analyze(&InvestigationRequest{Objective: c.obj, Mode: ModeAuto})
			for _, g := range c.expect {
				if !hasGoal(intent.Goals, g) {
					t.Fatalf("意图应包含 %s, got %v", g, intent.Goals)
				}
			}
			if intent.Direction != c.wantDir {
				t.Fatalf("direction = %s, want %s", intent.Direction, c.wantDir)
			}
			if intent.Mode != c.wantMode {
				t.Fatalf("mode = %s, want %s", intent.Mode, c.wantMode)
			}
		})
	}
}

func TestIntentAnalyzerExpectedResult(t *testing.T) {
	a := NewIntentAnalyzer()
	intent := a.Analyze(&InvestigationRequest{
		ExpectedResult: []string{"资金流图", "交易所入口"},
		Mode:           ModeAuto,
	})
	if !hasGoal(intent.Goals, GoalFlowGraph) || !hasGoal(intent.Goals, GoalExchangeEntry) {
		t.Fatalf("期望结果应映射为流图与交易所目标: %v", intent.Goals)
	}
	if intent.Mode != ModeExchangeEntry {
		t.Fatalf("mode = %s, want exchange_entry", intent.Mode)
	}
}

func TestIntentAnalyzerExplicitModeAndFallback(t *testing.T) {
	a := NewIntentAnalyzer()
	// 显式模式不被推断覆盖
	intent := a.Analyze(&InvestigationRequest{Objective: "随便看看", Mode: ModeRiskScan})
	if intent.Mode != ModeRiskScan {
		t.Fatalf("显式模式应保留, got %s", intent.Mode)
	}
	if !hasGoal(intent.Goals, GoalRisk) {
		t.Fatalf("显式 risk_scan 应兜底风险目标: %v", intent.Goals)
	}
	// 无关键词 + auto → 默认资金追踪目标
	intent2 := a.Analyze(&InvestigationRequest{Objective: "调查这个地址", Mode: ModeAuto})
	if !hasGoal(intent2.Goals, GoalFundDestination) || !hasGoal(intent2.Goals, GoalFundSource) {
		t.Fatalf("无关键词应兜底双向资金目标: %v", intent2.Goals)
	}
	if intent2.Direction != "both" {
		t.Fatalf("direction = %s, want both", intent2.Direction)
	}
}

func TestInvestigationHandlerConcurrencyLimit(t *testing.T) {
	agent := newTestAgent()
	// 注入 maxActiveInvestigations 个进行中调查（绕过 Start，直接构造 active）
	for i := 1; i <= maxActiveInvestigations; i++ {
		id := fmt.Sprintf("inv-%d", i)
		agent.active[id] = &Investigation{ID: id, Target: testAddress, Status: InvestigationRunning}
	}
	h := NewInvestigationHandler(agent, NewRequestStore(""), NewIntentAnalyzer())
	rr := doJSON(h, http.MethodPost, "/investigation/create", `{
		"address":"0x00000000000000000000000000000000000000a1",
		"objective":"找资金去向"
	}`)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("并发超限应 429, got %d: %s", rr.Code, rr.Body.String())
	}
	// 终态不计入：全部完成后可继续创建
	for _, inv := range agent.active {
		inv.Status = InvestigationCompleted
	}
	rr2 := doJSON(h, http.MethodPost, "/investigation/create", `{
		"address":"0x00000000000000000000000000000000000000a1",
		"objective":"找资金去向"
	}`)
	if rr2.Code != http.StatusAccepted {
		t.Fatalf("终态后应恢复创建, got %d: %s", rr2.Code, rr2.Body.String())
	}
}

func TestActiveCount(t *testing.T) {
	agent := newTestAgent()
	agent.active["inv-1"] = &Investigation{ID: "inv-1", Status: InvestigationRunning}
	agent.active["inv-2"] = &Investigation{ID: "inv-2", Status: InvestigationCompleted}
	agent.active["inv-3"] = &Investigation{ID: "inv-3", Status: InvestigationFailed}
	if n := agent.ActiveCount(); n != 1 {
		t.Fatalf("ActiveCount = %d, want 1（终态不计入）", n)
	}
}

func TestAgentRetireHistoryCap(t *testing.T) {
	agent := newTestAgent()
	agent.mu.Lock()
	base := time.Now().UTC()
	for i := 0; i < maxHistoryLength+5; i++ {
		id := fmt.Sprintf("inv-%d", i)
		agent.history[id] = &Investigation{ID: id, UpdatedAt: base.Add(time.Duration(i) * time.Second)}
	}
	agent.retireLocked(&Investigation{ID: "inv-new", UpdatedAt: base.Add(time.Hour)})
	agent.mu.Unlock()
	if len(agent.history) != maxHistoryLength {
		t.Fatalf("history 应裁剪到 %d, got %d", maxHistoryLength, len(agent.history))
	}
	if _, ok := agent.history["inv-0"]; ok {
		t.Fatal("最旧记录应被淘汰")
	}
	if _, ok := agent.history["inv-new"]; !ok {
		t.Fatal("新记录应保留")
	}
}

func TestInvestigationHandlerBudget(t *testing.T) {
	h := newTestInvestigationHandler()
	rr := doJSON(h, http.MethodPost, "/investigation/create", `{
		"address":"0x00000000000000000000000000000000000000a1",
		"objective":"找资金去向"
	}`)
	var out struct {
		Investigation Investigation `json:"investigation"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("create 响应解析失败: %v", err)
	}
	id := out.Investigation.ID
	rr = doJSON(h, http.MethodGet, "/investigation/"+id+"/budget", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("budget 应 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Budget map[string]any `json:"budget"`
		Used   map[string]any `json:"used"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("budget 响应解析失败: %v", err)
	}
	if resp.Budget["max_tasks"] != float64(50) {
		t.Fatalf("默认 max_tasks 应为 50: %v", resp.Budget["max_tasks"])
	}
	if _, ok := resp.Used["tasks"]; !ok {
		t.Fatal("应返回已消耗任务数")
	}
	rr = doJSON(h, http.MethodGet, "/investigation/inv-999/budget", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("不存在调查应 404, got %d", rr.Code)
	}
}
