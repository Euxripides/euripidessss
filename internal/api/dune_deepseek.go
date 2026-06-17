package api

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

var deepseekHTTPClient = &http.Client{Timeout: 30 * time.Second}

type deepseekChatRequest struct {
	Model          string                `json:"model"`
	Messages       []deepseekChatMessage `json:"messages"`
	ResponseFormat map[string]string     `json:"response_format"`
	Stream         bool                  `json:"stream"`
	Temperature    float64               `json:"temperature"`
	Thinking       map[string]string     `json:"thinking"`
}

type deepseekChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type deepseekChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func localizeDuneColumns(ctx context.Context, columns []string) map[string]string {
	labels := fallbackDuneColumnLabels(columns)
	if len(columns) == 0 || strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) == "" {
		return labels
	}
	translated, err := translateDuneHeaders(ctx, columns)
	if err != nil {
		return labels
	}
	for _, column := range columns {
		if value := strings.TrimSpace(translated[column]); value != "" {
			labels[column] = value
		}
	}
	return labels
}

func translateDuneHeaders(ctx context.Context, columns []string) (map[string]string, error) {
	apiKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if apiKey == "" {
		return map[string]string{}, nil
	}
	payload := deepseekChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []deepseekChatMessage{
			{Role: "system", Content: "你是数据表头翻译器。只返回 JSON 对象，key 必须是原始字段名，value 是简短中文表头。"},
			{Role: "user", Content: "把这些 Dune SQL 字段名翻译成中文表头：" + strings.Join(columns, ", ")},
		},
		ResponseFormat: map[string]string{"type": "json_object"},
		Stream:         false,
		Temperature:    0,
		Thinking:       map[string]string{"type": "disabled"},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.deepseek.com/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := deepseekHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("DeepSeek 表头汉化失败：HTTP %d", resp.StatusCode)
	}
	var result deepseekChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Choices) == 0 {
		return map[string]string{}, nil
	}
	var mapping map[string]string
	if err := json.Unmarshal([]byte(result.Choices[0].Message.Content), &mapping); err != nil {
		return nil, err
	}
	return mapping, nil
}

func fallbackDuneColumnLabels(columns []string) map[string]string {
	labels := make(map[string]string, len(columns))
	for _, column := range columns {
		labels[column] = fallbackDuneColumnLabel(column)
	}
	return labels
}

func fallbackDuneColumnLabel(column string) string {
	switch strings.ToLower(strings.TrimSpace(column)) {
	case "tx_hash", "transaction_hash", "hash":
		return "交易哈希"
	case "block_time", "evt_block_time":
		return "区块时间"
	case "block_number", "evt_block_number":
		return "区块高度"
	case "amount", "amount_usd":
		return "金额"
	case "from", "from_address":
		return "发送方"
	case "to", "to_address":
		return "接收方"
	default:
		return strings.ReplaceAll(column, "_", " ")
	}
}
