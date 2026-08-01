package intelligence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── 调查记忆测试 ──

func TestMemoryStoreCRUD(t *testing.T) {
	store := NewMemoryStore("")
	mem := store.New("inv-1", addrA)
	if mem.Target != addrA {
		t.Fatalf("目标错误: %s", mem.Target)
	}

	store.RecordDiscovered("inv-1", addrB)
	store.RecordPath("inv-1", "A→B→C")
	store.RecordPath("inv-1", "A→B→C") // 幂等
	store.RecordIgnored("inv-1", addrD)
	store.AddConclusion("inv-1", "发现 3 条路径")

	got, ok := store.Get("inv-1")
	if !ok {
		t.Fatal("应存在记忆")
	}
	if len(got.DiscoveredAt) != 1 {
		t.Fatalf("已发现地址应为 1, got %d", len(got.DiscoveredAt))
	}
	if len(got.AnalyzedPaths) != 1 {
		t.Fatalf("已分析路径应为 1（幂等）, got %d", len(got.AnalyzedPaths))
	}
	if len(got.IgnoredEntities) != 1 {
		t.Fatalf("已忽略实体应为 1, got %d", len(got.IgnoredEntities))
	}
	if len(got.Conclusions) != 1 {
		t.Fatalf("结论应为 1, got %d", len(got.Conclusions))
	}
}

func TestMemoryStorePersistence(t *testing.T) {
	dir := t.TempDir()
	store := NewMemoryStore(dir)
	store.New("inv-1", addrA)
	store.AddConclusion("inv-1", "测试结论")
	if err := store.Save("inv-1"); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	store2 := NewMemoryStore(dir)
	got, ok := store2.Get("inv-1")
	if !ok {
		t.Fatal("重载后记忆应存在")
	}
	if len(got.Conclusions) != 1 || got.Conclusions[0] != "测试结论" {
		t.Fatalf("重载后结论异常: %+v", got.Conclusions)
	}
}

// ── AI 上下文构建测试 ──

func TestAIContextBuilder(t *testing.T) {
	cfg := DefaultConfig()
	builder := NewAIContextBuilder(cfg)
	inv := &Investigation{
		ID:     "inv-1",
		Target: addrA,
		Paths: []RankedPath{
			{Path: FundPath{Nodes: []string{addrA, addrB, addrC}, Edges: []FundEdge{
				edge(addrA, addrB, "USDT", "1000000", 1000),
				edge(addrB, addrC, "USDT", "950000", 2000),
			}}, Score: PathScore{Total: 80}, Summary: "A→B→C USDT 195万"},
		},
		Patterns: []RiskPattern{{Type: PatternRapidTransfer, Severity: "high", Detail: "快速转移"}},
		Entities: []EntityInfo{{Address: addrA, Entity: "wallet", Risk: 60, TxCount: 100}},
	}
	ctx := builder.Build(inv)
	if len(ctx.TopPaths) != 1 {
		t.Fatalf("TopPaths 应为 1, got %d", len(ctx.TopPaths))
	}
	if len(ctx.RiskEvents) != 1 {
		t.Fatalf("RiskEvents 应为 1, got %d", len(ctx.RiskEvents))
	}
	prompt := builder.ToPrompt(ctx)
	if !strings.Contains(prompt, addrA) {
		t.Fatalf("提示词应包含目标地址")
	}
	if !strings.Contains(prompt, "快速转移") {
		t.Fatalf("提示词应包含风险事件")
	}
}

// ── DeepSeek 客户端解析测试 ──

func TestParseAIAnalysis(t *testing.T) {
	content := "1. 资金行为总结：该地址收到大额 USDT 后快速转出\n" +
		"2. 洞察：\n- 存在快速转移模式\n- 多地址拆分明显\n" +
		"3. 下一步建议：\n- 追踪交易所关联\n- 检查归集地址\n" +
		"4. 风险评价：高风险，资金快速清空"
	analysis := &AIAnalysis{Summary: content}
	parseAIAnalysis(content, analysis)
	if len(analysis.Insights) != 2 {
		t.Fatalf("Insights 应为 2, got %d: %v", len(analysis.Insights), analysis.Insights)
	}
	if len(analysis.Suggestions) != 2 {
		t.Fatalf("Suggestions 应为 2, got %d", len(analysis.Suggestions))
	}
}

// ── 报告代理测试 ──

func newTestInvestigation() *Investigation {
	return &Investigation{
		ID:        "inv-1",
		Target:    addrA,
		ChainID:   "bsc",
		Status:    InvestigationCompleted,
		CreatedAt: time.Now().UTC(),
		Plan: &InvestigationPlan{Target: addrA, Tasks: []PlannedTask{
			{ID: "t1", Type: "FUND_FLOW", Description: "追踪资金流向", Priority: 1},
		}},
		Paths: []RankedPath{
			{Path: FundPath{Nodes: []string{addrA, addrB}, Edges: []FundEdge{
				edge(addrA, addrB, "USDT", "1000000", 1000),
			}}, Score: PathScore{Total: 75}, Summary: "A→B"},
		},
		Patterns: []RiskPattern{{Type: PatternRapidTransfer, Severity: "high", Detail: "快速转移"}},
		Entities: []EntityInfo{{Address: addrA, Entity: "wallet", Label: "钱包", Risk: 60, TxCount: 100}},
		AI:       &AIAnalysis{Summary: "该地址资金行为可疑", Insights: []string{"快速转移"}, Model: "test-model"},
		Memory:   &InvestigationMemory{Conclusions: []string{"调查完成"}},
		Progress: 100,
	}
}

func TestReportMarkdown(t *testing.T) {
	agent := NewReportAgent(DefaultConfig())
	out, err := agent.Generate(newTestInvestigation(), ReportMarkdown)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	if !strings.Contains(out.Content, "链上自动调查报告") {
		t.Fatal("Markdown 应包含标题")
	}
	if !strings.Contains(out.Content, addrA) {
		t.Fatal("Markdown 应包含目标地址")
	}
	if !strings.Contains(out.Content, "快速转移") {
		t.Fatal("Markdown 应包含风险分析")
	}
	if !strings.Contains(out.Content, "该地址资金行为可疑") {
		t.Fatal("Markdown 应包含 AI 分析")
	}
}

func TestReportHTML(t *testing.T) {
	agent := NewReportAgent(DefaultConfig())
	out, err := agent.Generate(newTestInvestigation(), ReportHTML)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	if !strings.Contains(out.Content, "<html") {
		t.Fatal("HTML 应包含 html 标签")
	}
	if !strings.Contains(out.Content, "链上自动调查报告") {
		t.Fatal("HTML 应包含标题")
	}
}

func TestReportJSON(t *testing.T) {
	agent := NewReportAgent(DefaultConfig())
	out, err := agent.Generate(newTestInvestigation(), ReportJSON)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	var parsed Investigation
	if err := json.Unmarshal([]byte(out.Content), &parsed); err != nil {
		t.Fatalf("JSON 报告应可解析: %v", err)
	}
	if parsed.ID != "inv-1" {
		t.Fatalf("JSON 报告 ID 错误: %s", parsed.ID)
	}
}

// ── API handler 测试 ──

func newTestAgent() *InvestigationAgent {
	src := NewFakeFlowSource()
	cfg := DefaultConfig()
	cfg.UseAI = false // 测试不调用真实 DeepSeek
	ranker := DefaultPathRanker()
	agent := &InvestigationAgent{
		flowSource:      src,
		ranker:          ranker,
		tracer:          NewFundTracer(src, ranker, cfg),
		planner:         NewPlanner(cfg),
		detector:        NewPatternDetector(cfg),
		report:          NewReportAgent(cfg),
		contextBuilder:  NewAIContextBuilder(cfg),
		deepseek:        NewDeepSeekClient("", cfg.AIModel, cfg.AITimeoutMS),
		entityResolver:  NewEntityResolver(nil, nil),
		cfg:             cfg,
		active:          make(map[string]*Investigation),
		history:         make(map[string]*Investigation),
		memories:        NewMemoryStore(""),
	}
	return agent
}

func doJSON(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHandlerStartInvestigation(t *testing.T) {
	h := NewHandler(newTestAgent())
	rr := doJSON(h, http.MethodPost, "/intelligence/investigations", `{"target":"0x00000000000000000000000000000000000000a1"}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("start 应 202, got %d: %s", rr.Code, rr.Body.String())
	}
	var inv Investigation
	if err := json.Unmarshal(rr.Body.Bytes(), &inv); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	if inv.ID == "" {
		t.Fatal("应返回调查 ID")
	}
}

func TestHandlerStartInvalidTarget(t *testing.T) {
	h := NewHandler(newTestAgent())
	rr := doJSON(h, http.MethodPost, "/intelligence/investigations", `{"target":"0x' UNION SELECT 1--"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("注入载荷应 400, got %d", rr.Code)
	}
}

func TestHandlerListAndGet(t *testing.T) {
	h := NewHandler(newTestAgent())
	doJSON(h, http.MethodPost, "/intelligence/investigations", `{"target":"0x00000000000000000000000000000000000000a1"}`)

	rr := doJSON(h, http.MethodGet, "/intelligence/investigations", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list 应 200, got %d", rr.Code)
	}
	var list struct {
		Total int             `json:"total"`
		Items []Investigation `json:"items"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &list)
	if list.Total < 1 {
		t.Fatalf("应有调查记录, got %d", list.Total)
	}
}

func TestHandlerReportFormats(t *testing.T) {
	agent := newTestAgent()
	// 预置一条完成的调查
	agent.mu.Lock()
	inv := newTestInvestigation()
	agent.history["inv-1"] = inv
	agent.mu.Unlock()

	h := NewHandler(agent)
	for _, format := range []string{"markdown", "html", "json"} {
		rr := doJSON(h, http.MethodGet, "/intelligence/investigations/inv-1/report?format="+format, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("report %s 应 200, got %d", format, rr.Code)
		}
		if rr.Body.Len() == 0 {
			t.Fatalf("report %s 内容为空", format)
		}
	}
}

func TestHandlerConfig(t *testing.T) {
	h := NewHandler(newTestAgent())
	rr := doJSON(h, http.MethodGet, "/intelligence/config", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("config 应 200, got %d", rr.Code)
	}
	// 更新配置（钳制）
	rr2 := doJSON(h, http.MethodPost, "/intelligence/config", `{"max_hops":99,"beam_width":0}`)
	if rr2.Code != http.StatusOK {
		t.Fatalf("更新 config 应 200, got %d", rr2.Code)
	}
	var cfg IntelligenceConfig
	_ = json.Unmarshal(rr2.Body.Bytes(), &cfg)
	if cfg.MaxHops != 8 {
		t.Fatalf("max_hops 应钳制为 8, got %d", cfg.MaxHops)
	}
	if cfg.BeamWidth != 1 {
		t.Fatalf("beam_width 应钳制为 1, got %d", cfg.BeamWidth)
	}
}

func TestHandlerNotFound(t *testing.T) {
	h := NewHandler(newTestAgent())
	rr := doJSON(h, http.MethodGet, "/intelligence/nonexistent", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("未知接口应 404, got %d", rr.Code)
	}
}

// TestConfigPropagationToComponents 验证 POST /config 更新传播到子组件：
// 更新 top_paths/max_hops 后新调查的计划与路径数受新配置约束（MEDIUM 修复验证）。
func TestConfigPropagationToComponents(t *testing.T) {
	agent := newTestAgent()
	h := NewHandler(agent)
	rr := doJSON(h, http.MethodPost, "/intelligence/config", `{"top_paths":1,"max_hops":2}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("更新 config 应 200, got %d", rr.Code)
	}
	// 新调查应使用新配置（rebuildSubcomponents 生效）
	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	done := waitStatus(t, agent, inv.ID, InvestigationCompleted, 5*time.Second)
	if done.Plan == nil {
		t.Fatal("应有计划")
	}
	if done.Plan.MaxHops != 2 {
		t.Fatalf("max_hops=2 应传播到计划, got %d", done.Plan.MaxHops)
	}
	if len(done.Paths) > 1 {
		t.Fatalf("TopPaths=1 时路径数应 ≤1, got %d", len(done.Paths))
	}
}
