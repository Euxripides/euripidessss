package intelligence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/etl/backend/internal/logger"
)

// ── DeepSeek Client ──
//
// 真实调用 DeepSeek Chat Completions API（api.deepseek.com）。
// 配置：环境变量 DEEPSEEK_API_KEY；模型/超时/输出上限来自 IntelligenceConfig。

// AIChatter 是 AI 对话接口（DeepSeekClient 实现；测试可注入 fake）。
type AIChatter interface {
	// Chat 发送 system+user 提示词，返回模型输出。
	Chat(ctx context.Context, system, user string) (string, error)
	// Configured 返回是否已配置 API Key。
	Configured() bool
}

// DeepSeekClient 是 DeepSeek API 客户端。
type DeepSeekClient struct {
	apiKey     string
	model      string
	maxTokens  int
	httpClient *http.Client
	endpoint   string
}

// NewDeepSeekClient 创建客户端。apiKey 为空时回退读 DEEPSEEK_API_KEY 环境变量。
// maxTokens 可选（默认 2000）。
func NewDeepSeekClient(apiKey, model string, timeoutMS int, maxTokens ...int) *DeepSeekClient {
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	}
	if model == "" {
		model = "deepseek-v4-flash"
	}
	if timeoutMS <= 0 {
		timeoutMS = 60000
	}
	tokens := 2000
	if len(maxTokens) > 0 && maxTokens[0] > 0 {
		tokens = maxTokens[0]
	}
	return &DeepSeekClient{
		apiKey:     apiKey,
		model:      model,
		maxTokens:  tokens,
		httpClient: &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond},
		endpoint:   "https://api.deepseek.com/chat/completions",
	}
}

// Configured 返回是否已配置 API Key。
func (c *DeepSeekClient) Configured() bool {
	return c.apiKey != ""
}

// chatRequest 是 DeepSeek 请求体。
type chatRequest struct {
	Model          string            `json:"model"`
	Messages       []chatMessage     `json:"messages"`
	ResponseFormat map[string]string `json:"response_format,omitempty"`
	Stream         bool              `json:"stream"`
	Temperature    float64           `json:"temperature"`
	MaxTokens      int               `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse 是 DeepSeek 响应体。
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Chat 发送多角色 system + user 提示词，返回模型输出（§17 请求日志）。
func (c *DeepSeekClient) Chat(ctx context.Context, system, user string) (string, error) {
	if !c.Configured() {
		return "", fmt.Errorf("DeepSeek 未配置：请设置 DEEPSEEK_API_KEY")
	}
	start := time.Now()
	payload := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		ResponseFormat: map[string]string{"type": "text"},
		Stream:         false,
		Temperature:    0.3,
		MaxTokens:      c.maxTokens,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("DeepSeek 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("DeepSeek API 返回 HTTP %d", resp.StatusCode)
	}
	var result chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("DeepSeek 响应解析失败: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("DeepSeek 错误: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("DeepSeek 无返回内容")
	}
	content := strings.TrimSpace(result.Choices[0].Message.Content)
	duration := time.Since(start).Milliseconds()
	// 推理模型输出可能被 max_tokens 截断（content 为空/不完整）——记录以便诊断
	if result.Choices[0].FinishReason == "length" || content == "" {
		completionTokens := 0
		if result.Usage != nil {
			completionTokens = result.Usage.CompletionTokens
		}
		logger.Log.Warn().
			Str("model", c.model).
			Str("finish_reason", result.Choices[0].FinishReason).
			Int("completion_tokens", completionTokens).
			Msg("deepseek_output_truncated_or_empty")
	}
	logger.Log.Info().
		Str("model", c.model).
		Int("duration_ms", int(duration)).
		Int("prompt_chars", len(user)).
		Msg("deepseek_chat_ok")
	if result.Usage != nil {
		logger.Log.Info().
			Str("model", c.model).
			Int("prompt_tokens", result.Usage.PromptTokens).
			Int("completion_tokens", result.Usage.CompletionTokens).
			Int("total_tokens", result.Usage.TotalTokens).
			Msg("deepseek_token_usage")
	}
	return content, nil
}

// Analyze 发送分析提示词并返回 AI 分析结果。
// 解析输出中的 4 个部分（总结/洞察/建议/风险评价），解析失败时整体作为 Summary。
func (c *DeepSeekClient) Analyze(ctx context.Context, prompt string) (*AIAnalysis, error) {
	start := time.Now()
	content, err := c.Chat(ctx, "你是链上资金调查专家，输出简洁中文分析。", prompt)
	if err != nil {
		return nil, err
	}
	analysis := &AIAnalysis{
		Summary:    content,
		Model:      c.model,
		DurationMs: time.Since(start).Milliseconds(),
	}
	parseAIAnalysis(content, analysis)
	return analysis, nil
}

// parseAIAnalysis 从模型输出中提取结构化字段（尽力解析，失败保留原文）。
func parseAIAnalysis(content string, analysis *AIAnalysis) {
	lines := strings.Split(content, "\n")
	var current string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "总结") || strings.Contains(lower, "summary"):
			current = "summary"
			continue
		case strings.Contains(lower, "洞察") || strings.Contains(lower, "insight"):
			current = "insights"
			continue
		case strings.Contains(lower, "建议") || strings.Contains(lower, "suggestion") || strings.Contains(lower, "next"):
			current = "suggestions"
			continue
		case strings.Contains(lower, "风险") || strings.Contains(lower, "risk"):
			current = "risk"
			continue
		}
		if line == "" || strings.HasPrefix(line, "1.") || strings.HasPrefix(line, "2.") ||
			strings.HasPrefix(line, "3.") || strings.HasPrefix(line, "4.") {
			continue
		}
		item := strings.TrimPrefix(strings.TrimPrefix(line, "- "), "• ")
		if item == "" {
			continue
		}
		switch current {
		case "insights":
			analysis.Insights = append(analysis.Insights, item)
		case "suggestions":
			analysis.Suggestions = append(analysis.Suggestions, item)
		case "risk":
			analysis.RiskComment = item
		default:
			if analysis.Summary == content {
				analysis.Summary = line
			} else {
				analysis.Summary += "\n" + line
			}
		}
	}
}
