package intelligence

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ── Response Parser（设计 §11/§17）──
//
// AI 输出必须结构化。解析器负责：
//   1. 从模型输出中提取 JSON（容忍 Markdown 围栏与前缀文本）
//   2. 类型校验：任务类型白名单、置信度钳制 0-1、证据字段非空
//   3. 输出校验失败时返回错误（调用方降级，不信任未校验输出）

// ResponseParser 解析 DeepSeek 结构化输出。
type ResponseParser struct{}

// NewResponseParser 创建解析器。
func NewResponseParser() *ResponseParser { return &ResponseParser{} }

// taskTypeWhitelist 是 AI 可生成的任务类型白名单（对应 TaskQueue 7 类型）。
var taskTypeWhitelist = map[string]bool{
	TaskAddressProfile: true,
	TaskFlowAnalysis:   true,
	TaskPathTrace:      true,
	TaskEntityCheck:    true,
	TaskRiskScan:       true,
	TaskExpandAddress:  true,
	TaskGenerateReport: true,
}

// extractJSON 从模型输出中提取 JSON 文本（去除 Markdown 围栏与前缀）。
func (p *ResponseParser) extractJSON(content string) string {
	content = strings.TrimSpace(content)
	// ```json ... ``` 围栏
	if i := strings.Index(content, "```"); i >= 0 {
		rest := content[i+3:]
		rest = strings.TrimPrefix(rest, "json")
		rest = strings.TrimPrefix(rest, "JSON")
		if j := strings.Index(rest, "```"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	// 纯 JSON：以 { 或 [ 开头（容忍前导文本）
	for i, c := range content {
		if c == '{' || c == '[' {
			return strings.TrimSpace(content[i:])
		}
	}
	return content
}

// ParseStrategy 解析 AI 调查策略并校验。
func (p *ResponseParser) ParseStrategy(content string) (*AIStrategy, error) {
	raw := p.extractJSON(content)
	if !strings.HasPrefix(raw, "{") {
		return nil, fmt.Errorf("策略输出不是 JSON 对象")
	}
	var s AIStrategy
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, fmt.Errorf("策略 JSON 解析失败: %w", err)
	}
	if s.Strategy == "" {
		return nil, fmt.Errorf("策略缺少 strategy 字段")
	}
	s.Confidence = clampConfidence(s.Confidence)
	valid := s.Tasks[:0]
	for _, t := range s.Tasks {
		if !taskTypeWhitelist[t.Type] {
			continue // 白名单外任务丢弃
		}
		t.Priority = clampConfidence(t.Priority)
		valid = append(valid, t)
	}
	s.Tasks = valid
	if len(s.Tasks) == 0 {
		return nil, fmt.Errorf("策略未包含有效任务")
	}
	return &s, nil
}

// ParseFindings 解析 AI 深入分析发现并校验。
func (p *ResponseParser) ParseFindings(content string) ([]AIFinding, error) {
	raw := p.extractJSON(content)
	if !strings.HasPrefix(raw, "[") {
		return nil, fmt.Errorf("发现输出不是 JSON 数组")
	}
	var findings []AIFinding
	if err := json.Unmarshal([]byte(raw), &findings); err != nil {
		return nil, fmt.Errorf("发现 JSON 解析失败: %w", err)
	}
	valid := make([]AIFinding, 0, len(findings))
	for _, f := range findings {
		if f.Type == "" {
			continue
		}
		f.Confidence = clampConfidence(f.Confidence)
		f.Address = strings.ToLower(strings.TrimSpace(f.Address))
		valid = append(valid, f)
	}
	return valid, nil
}

// ParseHypotheses 解析 AI 调查假设并校验。
func (p *ResponseParser) ParseHypotheses(content string) ([]AIHypothesis, error) {
	raw := p.extractJSON(content)
	if !strings.HasPrefix(raw, "[") {
		return nil, fmt.Errorf("假设输出不是 JSON 数组")
	}
	var hyps []AIHypothesis
	if err := json.Unmarshal([]byte(raw), &hyps); err != nil {
		return nil, fmt.Errorf("假设 JSON 解析失败: %w", err)
	}
	valid := make([]AIHypothesis, 0, len(hyps))
	for _, h := range hyps {
		if h.Title == "" {
			continue
		}
		h.Confidence = clampConfidence(h.Confidence)
		tasks := h.Tasks[:0]
		for _, t := range h.Tasks {
			if !taskTypeWhitelist[t.Type] {
				continue
			}
			t.Priority = clampConfidence(t.Priority)
			tasks = append(tasks, t)
		}
		h.Tasks = tasks
		valid = append(valid, h)
	}
	return valid, nil
}

// ParseSuggestion 解析 AI 下一步建议并校验。
func (p *ResponseParser) ParseSuggestion(content string) (*AISuggestion, error) {
	raw := p.extractJSON(content)
	if !strings.HasPrefix(raw, "{") {
		return nil, fmt.Errorf("建议输出不是 JSON 对象")
	}
	var s AISuggestion
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, fmt.Errorf("建议 JSON 解析失败: %w", err)
	}
	switch s.Action {
	case "EXPAND", "STOP", "DEEP_ANALYSIS", "VERIFY":
	default:
		return nil, fmt.Errorf("建议动作非法: %s", s.Action)
	}
	s.Confidence = clampConfidence(s.Confidence)
	return &s, nil
}

// clampConfidence 钳制置信度到 [0,1]。
func clampConfidence(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
