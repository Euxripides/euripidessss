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
)

// ── DeepSeek Client ──
//
// 真实调用 DeepSeek Chat Completions API（api.deepseek.com）。
// 配置：环境变量 DEEPSEEK_API_KEY；模型与超时来自 IntelligenceConfig。

// DeepSeekClient 是 DeepSeek API 客户端。
type DeepSeekClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
	endpoint   string
}

// NewDeepSeekClient 创建客户端。apiKey 为空时回退读 DEEPSEEK_API_KEY 环境变量。
func NewDeepSeekClient(apiKey, model string, timeoutMS int) *DeepSeekClient {
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	}
	if model == "" {
		model = "deepseek-v4-flash"
	}
	if timeoutMS <= 0 {
		timeoutMS = 60000
	}
	return &DeepSeekClient{
		apiKey:     apiKey,
		model:      model,
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
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Analyze 发送分析提示词并返回 AI 分析结果。
// 解析输出中的 4 个部分（总结/洞察/建议/风险评价），解析失败时整体作为 Summary。
func (c *DeepSeekClient) Analyze(ctx context.Context, prompt string) (*AIAnalysis, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("DeepSeek 未配置：请设置 DEEPSEEK_API_KEY")
	}
	start := time.Now()
	payload := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: "你是链上资金调查专家，输出简洁中文分析。"},
			{Role: "user", Content: prompt},
		},
		ResponseFormat: map[string]string{"type": "text"},
		Stream:         false,
		Temperature:    0.3,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DeepSeek 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("DeepSeek API 返回 HTTP %d", resp.StatusCode)
	}
	var result chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("DeepSeek 响应解析失败: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("DeepSeek 错误: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("DeepSeek 无返回内容")
	}
	content := strings.TrimSpace(result.Choices[0].Message.Content)
	analysis := &AIAnalysis{
		Summary:     content,
		Model:       c.model,
		DurationMs:  time.Since(start).Milliseconds(),
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
