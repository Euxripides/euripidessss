package intelligence

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSSEEventsPush 回归（#7 优化）：SSE 端点先推当前快照，状态变更实时推送，终态关闭连接。
func TestSSEEventsPush(t *testing.T) {
	src := NewFakeFlowSource()
	src.SetFlows(addrA, []FundEdge{edge(addrA, addrB, "USDT", "1000000", 1000)})
	exp := newFakeExpander()
	exp.set(addrA, ExpansionResult{Address: addrF, Entity: "wallet", Score: 90, Depth: 1})

	cfg := DefaultConfig()
	cfg.UseAI = false
	cfg.MaxRounds = 1

	agent := newLoopTestAgent(src, exp, cfg)
	handler := &Handler{agent: agent}

	// 先启动调查（后台执行）
	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// SSE 连接
	req := httptest.NewRequest("GET", "/intelligence/events?id="+inv.ID, nil)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.events(w, req)
		close(done)
	}()

	// 等待事件流：应收到 ≥1 条（初始快照），且最终收到 COMPLETED 后连接关闭
	deadline := time.After(10 * time.Second)
	select {
	case <-done:
	case <-deadline:
		t.Fatal("SSE 连接未在超时前关闭")
	}
	// handler 退出后检查完整事件流
	body := w.Body.String()
	if !strings.Contains(body, `"status":"COMPLETED"`) {
		t.Fatalf("SSE 未收到 COMPLETED 事件, body=%q", body)
	}
	// 事件格式校验：event: investigation + data: JSON
	lines := strings.Split(w.Body.String(), "\n")
	hasEvent := false
	hasData := false
	for _, ln := range lines {
		if strings.HasPrefix(ln, "event: investigation") {
			hasEvent = true
		}
		if strings.HasPrefix(ln, "data: ") {
			hasData = true
			var snap Investigation
			if err := json.Unmarshal([]byte(strings.TrimPrefix(ln, "data: ")), &snap); err != nil {
				t.Fatalf("data 行不是合法 JSON: %v", err)
			}
		}
	}
	if !hasEvent || !hasData {
		t.Errorf("SSE 事件格式错误: event=%v data=%v", hasEvent, hasData)
	}
}

// TestSSEEventsValidation 参数校验：缺 id 400、不存在 404。
func TestSSEEventsValidation(t *testing.T) {
	agent := newLoopTestAgent(NewFakeFlowSource(), newFakeExpander(), DefaultConfig())
	handler := &Handler{agent: agent}

	w := httptest.NewRecorder()
	handler.events(w, httptest.NewRequest("GET", "/intelligence/events", nil))
	if w.Code != 400 {
		t.Fatalf("缺 id 应 400, got %d", w.Code)
	}

	w2 := httptest.NewRecorder()
	handler.events(w2, httptest.NewRequest("GET", "/intelligence/events?id=nonexistent", nil))
	if w2.Code != 404 {
		t.Fatalf("不存在调查应 404, got %d", w2.Code)
	}
}

var _ = bufio.NewReader // 保留 bufio import（后续扩展用）
