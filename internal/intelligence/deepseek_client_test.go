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

// TestDeepSeekChatTruncation 回归（BUG-004）：finish_reason=length 或空 content 时
// 记录截断警告且不 panic（含 Usage 为 nil 路径）。
func TestDeepSeekChatTruncation(t *testing.T) {
	cases := []struct {
		name         string
		finishReason string
		content      string
		withUsage    bool
		wantContent  string
	}{
		{"finish_reason_length_no_usage", "length", `{"partial":true}`, false, `{"partial":true}`},
		{"finish_reason_length_with_usage", "length", "partial", true, "partial"},
		{"empty_content", "stop", "", true, ""},
		{"normal", "stop", "ok output", true, "ok output"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req chatRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if req.MaxTokens <= 0 {
					t.Errorf("max_tokens 应随请求发送，got %d", req.MaxTokens)
				}
				resp := chatResponse{Choices: []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
					FinishReason string `json:"finish_reason"`
				}{{Message: struct {
					Content string `json:"content"`
				}{Content: tc.content}, FinishReason: tc.finishReason}}}
				if tc.withUsage {
					resp.Usage = &struct {
						PromptTokens     int `json:"prompt_tokens"`
						CompletionTokens int `json:"completion_tokens"`
						TotalTokens      int `json:"total_tokens"`
					}{PromptTokens: 10, CompletionTokens: 2001, TotalTokens: 2011}
				}
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			client := NewDeepSeekClient("test-key", "deepseek-v4-flash", 5000, 4096)
			client.endpoint = server.URL // 同包注入测试端点

			got, err := client.Chat(context.Background(), "system", "user")
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if got != tc.wantContent {
				t.Errorf("content = %q, want %q", got, tc.wantContent)
			}
		})
	}
}

// TestDeepSeekChatRetryOnTruncation 回归（#4 优化）：finish_reason=length 时
// 自动提高 max_tokens 重试一次，第二次完整输出直接返回。
func TestDeepSeekChatRetryOnTruncation(t *testing.T) {
	call := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		call++
		if call == 1 {
			if req.MaxTokens != 4096 {
				t.Errorf("首次 max_tokens 应为 4096, got %d", req.MaxTokens)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":1,"completion_tokens":4096,"total_tokens":4097}}`))
			return
		}
		if req.MaxTokens != 8192 {
			t.Errorf("重试 max_tokens 应翻倍为 8192, got %d", req.MaxTokens)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"complete output"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":10,"total_tokens":11}}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test-key", "deepseek-v4-flash", 5000, 4096)
	client.endpoint = server.URL

	got, err := client.Chat(context.Background(), "s", "u")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got != "complete output" {
		t.Errorf("重试后应返回完整输出, got %q", got)
	}
	if call != 2 {
		t.Errorf("应恰好调用 2 次, got %d", call)
	}

	// 二次截断不再重试（防无限循环），仍返回部分内容不报错
	call = 0
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`))
	}))
	defer server2.Close()
	client2 := NewDeepSeekClient("test-key", "deepseek-v4-flash", 5000, 4096)
	client2.endpoint = server2.URL
	got2, err := client2.Chat(context.Background(), "s", "u")
	if err != nil {
		t.Fatalf("Chat2: %v", err)
	}
	if got2 != "partial" || call != 2 {
		t.Errorf("二次截断应重试一次后返回部分内容, got=%q calls=%d", got2, call)
	}
}

// TestDeepSeekChatRetryWithApplyConfig 回归：截断重试（翻倍上限参数传递）与 ApplyConfig
// 并发时互不干扰——重试不覆盖新配置（lost update 回归）。
func TestDeepSeekChatRetryWithApplyConfig(t *testing.T) {
	call := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		call++
		if call == 1 {
			if req.MaxTokens != 4096 {
				t.Errorf("首次 max_tokens 应为 4096, got %d", req.MaxTokens)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":1,"completion_tokens":4096,"total_tokens":4097}}`))
			return
		}
		// 重试：max_tokens 应为翻倍 8192（参数传递，非共享状态）
		if req.MaxTokens != 8192 {
			t.Errorf("重试 max_tokens 应为 8192, got %d", req.MaxTokens)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":10,"total_tokens":11}}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test-key", "deepseek-v4-flash", 5000, 4096)
	client.endpoint = server.URL

	// 重试窗口内 ApplyConfig 修改 max_tokens → 重试不得覆盖新值
	applyDone := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Millisecond)
		client.ApplyConfig("deepseek-v4-flash", 5000, 8192)
		close(applyDone)
	}()

	got, err := client.Chat(context.Background(), "s", "u")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	<-applyDone // 确保 ApplyConfig 已执行
	if got != "ok" {
		t.Errorf("got %q", got)
	}
	// 配置应保持 ApplyConfig 设置的值（未被重试回写覆盖）
	client.cfgMu.Lock()
	maxTokens := client.maxTokens
	client.cfgMu.Unlock()
	if maxTokens != 8192 {
		t.Errorf("ApplyConfig 新值被重试覆盖: max_tokens=%d, want 8192", maxTokens)
	}
}

// TestDeepSeekChatErrors 错误路径：HTTP 非 2xx 与空 choices。
func TestDeepSeekChatErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer server.Close()
	client := NewDeepSeekClient("test-key", "deepseek-v4-flash", 5000, 4096)
	client.endpoint = server.URL
	if _, err := client.Chat(context.Background(), "s", "u"); err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("期望 HTTP 429 错误，got %v", err)
	}

	// 空 choices
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server2.Close()
	client2 := NewDeepSeekClient("test-key", "deepseek-v4-flash", 5000, 4096)
	client2.endpoint = server2.URL
	if _, err := client2.Chat(context.Background(), "s", "u"); err == nil {
		t.Error("空 choices 应返回错误")
	}
}
