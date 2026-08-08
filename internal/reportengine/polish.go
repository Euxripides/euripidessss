package reportengine

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

// NarrativePolisher 是叙事润色器（P2：LLM 只负责语言，不创造事实）。
type NarrativePolisher interface {
	Polish(ctx context.Context, narrative string, findings []Finding) (string, error)
}

// DeepSeekPolisher 调用 DeepSeek Chat Completions（OpenAI 兼容）。
type DeepSeekPolisher struct {
	APIKey   string
	Endpoint string
	Model    string
	HTTP     *http.Client
}

// NewDeepSeekPolisher 从环境变量 DEEPSEEK_API_KEY 创建（未配置返回 nil）。
func NewDeepSeekPolisher() *DeepSeekPolisher {
	key := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if key == "" {
		return nil
	}
	return &DeepSeekPolisher{
		APIKey: key, Endpoint: "https://api.deepseek.com/chat/completions",
		Model: "deepseek-chat", HTTP: &http.Client{Timeout: 60 * time.Second},
	}
}

func (p *DeepSeekPolisher) Polish(ctx context.Context, narrative string, findings []Finding) (string, error) {
	var facts []string
	for _, f := range findings {
		parts := make([]string, 0, len(f.Metrics))
		for k, v := range f.Metrics {
			parts = append(parts, k+"="+v)
		}
		facts = append(facts, f.FindingType+": "+strings.Join(parts, ", "))
	}
	payload := map[string]any{
		"model": p.Model,
		"messages": []map[string]string{
			{"role": "system", "content": "你是调查叙事润色器。只允许优化语言表达，绝对禁止改变任何数字、地址、金额、时间与事实。不要添加新事实。"},
			{"role": "user", "content": "结构化事实：\n" + strings.Join(facts, "\n") + "\n\n原文：\n" + narrative},
		},
		"temperature": 0.1,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("polish: LLM HTTP %d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("polish: 无返回内容")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

