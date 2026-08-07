package dynamicinvestigation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── API handler 测试 ──

func newTestHandler() *Handler {
	src := NewFakeSource()
	exec := NewFakeExecutor()
	src.SetProfile("0x0000000000000000000000000000000000000001", &ProfileSignal{TxCount: 100, InCount: 50, OutCount: 50, RiskScore: 85})
	cfg := DefaultConfig()
	engine := NewEngine(NewQueue(""), NewRecognizer(), src, exec, cfg)
	return NewHandler(engine)
}

func doJSON(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHandlerStart(t *testing.T) {
	h := newTestHandler()
	rr := doJSON(h, http.MethodPost, "/dynamic-investigation/start", `{"target":"0x0000000000000000000000000000000000000001"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("start 应 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var stats EngineStats
	if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	if stats.TotalCompleted != 1 {
		t.Fatalf("TotalCompleted 应为 1, got %d", stats.TotalCompleted)
	}
}

func TestHandlerStartEmptyTarget(t *testing.T) {
	h := newTestHandler()
	rr := doJSON(h, http.MethodPost, "/dynamic-investigation/start", `{"target":""}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("空 target 应 400, got %d", rr.Code)
	}
}

func TestHandlerQueueFlow(t *testing.T) {
	h := newTestHandler()
	// 启动调查
	doJSON(h, http.MethodPost, "/dynamic-investigation/start", `{"target":"0x0000000000000000000000000000000000000001"}`)

	// 队列列表
	rr := doJSON(h, http.MethodGet, "/dynamic-investigation/queue", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("queue 应 200, got %d", rr.Code)
	}
	var list struct {
		Total int                 `json:"total"`
		Items []DiscoveredAddress `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("queue 响应解析失败: %v", err)
	}
	if list.Total != 1 {
		t.Fatalf("队列应有 1 条, got %d", list.Total)
	}

	// 单地址查询
	rr2 := doJSON(h, http.MethodGet, "/dynamic-investigation/queue/0x0000000000000000000000000000000000000001", "")
	if rr2.Code != http.StatusOK {
		t.Fatalf("queue/:address 应 200, got %d", rr2.Code)
	}
	var item DiscoveredAddress
	if err := json.Unmarshal(rr2.Body.Bytes(), &item); err != nil {
		t.Fatalf("地址响应解析失败: %v", err)
	}
	if item.Address != "0x0000000000000000000000000000000000000001" {
		t.Fatalf("地址不匹配: %s", item.Address)
	}

	// 不存在地址
	rr3 := doJSON(h, http.MethodGet, "/dynamic-investigation/queue/0x00000000000000000000000000000000000000ff", "")
	if rr3.Code != http.StatusNotFound {
		t.Fatalf("不存在地址应 404, got %d", rr3.Code)
	}
}

func TestHandlerConfig(t *testing.T) {
	h := newTestHandler()
	// 默认配置
	rr := doJSON(h, http.MethodGet, "/dynamic-investigation/config", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("config 应 200, got %d", rr.Code)
	}
	var cfg ExpansionConfig
	_ = json.Unmarshal(rr.Body.Bytes(), &cfg)
	if cfg.MaxDepth == 0 || cfg.MaxAddresses == 0 {
		t.Fatal("默认配置不应为零值")
	}

	// 更新配置
	rr2 := doJSON(h, http.MethodPost, "/dynamic-investigation/config", `{"max_depth":5,"max_addresses":1000,"amount_threshold":"1000000"}`)
	if rr2.Code != http.StatusOK {
		t.Fatalf("更新 config 应 200, got %d", rr2.Code)
	}
	rr3 := doJSON(h, http.MethodGet, "/dynamic-investigation/config", "")
	var cfg2 ExpansionConfig
	_ = json.Unmarshal(rr3.Body.Bytes(), &cfg2)
	if cfg2.MaxDepth != 5 {
		t.Fatalf("max_depth 应为 5, got %d", cfg2.MaxDepth)
	}
	if cfg2.AmountThreshold != "1000000" {
		t.Fatalf("amount_threshold 应为 1000000, got %s", cfg2.AmountThreshold)
	}
}

func TestHandlerTasksAndStats(t *testing.T) {
	h := newTestHandler()
	doJSON(h, http.MethodPost, "/dynamic-investigation/start", `{"target":"0x0000000000000000000000000000000000000001"}`)

	rr := doJSON(h, http.MethodGet, "/dynamic-investigation/tasks", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("tasks 应 200, got %d", rr.Code)
	}
	var tasks struct {
		Total int        `json:"total"`
		Items []TaskView `json:"items"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &tasks)
	if tasks.Total != 1 {
		t.Fatalf("应有 1 个任务, got %d", tasks.Total)
	}
	if tasks.Items[0].Mode != AcquisitionSQDLogs {
		t.Fatalf("钱包任务应为 SQD_LOGS, got %s", tasks.Items[0].Mode)
	}

	rr2 := doJSON(h, http.MethodGet, "/dynamic-investigation/stats", "")
	if rr2.Code != http.StatusOK {
		t.Fatalf("stats 应 200, got %d", rr2.Code)
	}
}

func TestHandlerEntities(t *testing.T) {
	h := newTestHandler()
	// 添加已知实体
	rr := doJSON(h, http.MethodPost, "/dynamic-investigation/entities", `{"address":"0x0000000000000000000000000000000000b1b1b1","entity":"exchange","label":"Binance"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("添加实体应 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// 列表
	rr2 := doJSON(h, http.MethodGet, "/dynamic-investigation/entities", "")
	if rr2.Code != http.StatusOK {
		t.Fatalf("实体列表应 200, got %d", rr2.Code)
	}
	var list struct {
		Total int           `json:"total"`
		Items []KnownEntity `json:"items"`
	}
	_ = json.Unmarshal(rr2.Body.Bytes(), &list)
	if list.Total != 1 || list.Items[0].Entity != EntityExchange {
		t.Fatalf("实体列表异常: %+v", list)
	}
}

func TestHandlerIgnoreAndApprove(t *testing.T) {
	h := newTestHandler()
	doJSON(h, http.MethodPost, "/dynamic-investigation/start", `{"target":"0x0000000000000000000000000000000000000001"}`)

	// 忽略
	rr := doJSON(h, http.MethodPost, "/dynamic-investigation/queue/0x0000000000000000000000000000000000000001/ignore", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ignore 应 200, got %d", rr.Code)
	}
	item, _ := h.engine.Queue().Get("0x0000000000000000000000000000000000000001")
	if item.Status != StatusIgnored {
		t.Fatalf("忽略后应为 IGNORED, got %s", item.Status)
	}

	// 重新批准（从 IGNORED 直接 SetStatus 支持手动复核）
	rr2 := doJSON(h, http.MethodPost, "/dynamic-investigation/queue/0x0000000000000000000000000000000000000001/approve", "")
	if rr2.Code != http.StatusOK {
		t.Fatalf("approve 应 200, got %d: %s", rr2.Code, rr2.Body.String())
	}
	item2, _ := h.engine.Queue().Get("0x0000000000000000000000000000000000000001")
	if item2.Status != StatusApproved {
		t.Fatalf("批准后应为 APPROVED, got %s", item2.Status)
	}
}

func TestHandlerNotFound(t *testing.T) {
	h := newTestHandler()
	rr := doJSON(h, http.MethodGet, "/dynamic-investigation/nonexistent", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("未知接口应 404, got %d", rr.Code)
	}
}

// TestHandlerStartInvalidAddress 验证 SQL 注入防护：非 EVM 地址被 API 边界拒绝。
func TestHandlerStartInvalidAddress(t *testing.T) {
	h := newTestHandler()
	// 注入载荷：引号/UNION/路径穿越/非法字符
	payloads := []string{
		`{"target":"0x' UNION SELECT 1--"}`,
		`{"target":"0x00000000000000000000000000000000000000' OR '1'='1"}`,
		`{"target":"../../etc/passwd"}`,
		`{"target":"0xZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ"}`,
	}
	for _, p := range payloads {
		rr := doJSON(h, http.MethodPost, "/dynamic-investigation/start", p)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("注入载荷应 400, got %d: %s", rr.Code, p)
		}
	}
}

// TestHandlerQueueItemInvalidAddress 验证 queue/entities 地址校验。
func TestHandlerQueueItemInvalidAddress(t *testing.T) {
	h := newTestHandler()
	rr := doJSON(h, http.MethodGet, "/dynamic-investigation/queue/0xnot-an-address", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("非法地址应 400, got %d", rr.Code)
	}
	rr2 := doJSON(h, http.MethodPost, "/dynamic-investigation/entities", `{"address":"0xzzzz","entity":"exchange"}`)
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("非法实体地址应 400, got %d", rr2.Code)
	}
}

// TestHandlerStartOversizedBody 验证请求体大小限制。
func TestHandlerStartOversizedBody(t *testing.T) {
	h := newTestHandler()
	big := `{"target":"0x0000000000000000000000000000000000000001","config":{"padding":"` +
		strings.Repeat("a", 128<<10) + `"}}`
	rr := doJSON(h, http.MethodPost, "/dynamic-investigation/start", big)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("超大请求体应 400, got %d", rr.Code)
	}
}

var _ = context.Background
