package intelligence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
