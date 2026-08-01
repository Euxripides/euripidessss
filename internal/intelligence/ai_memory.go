package intelligence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ── AI Memory（设计 §13）──
//
// 保存：历史调查 / 地址判断 / 风险模式 / AI 结论 / 人工反馈。
// JSON 持久化，供后续调查上下文构建使用，避免重复分析。

// AIMemoryKind 是 AI 记忆类型。
type AIMemoryKind string

const (
	MemInvestigation   AIMemoryKind = "investigation"    // 历史调查
	MemAddressJudgment AIMemoryKind = "address_judgment" // 地址判断
	MemRiskPattern     AIMemoryKind = "risk_pattern"     // 风险模式
	MemAIConclusion    AIMemoryKind = "ai_conclusion"    // AI 结论
	MemUserFeedback    AIMemoryKind = "user_feedback"    // 人工反馈
)

// AIMemoryEntry 是一条 AI 记忆。
type AIMemoryEntry struct {
	ID         string       `json:"id"`
	Kind       AIMemoryKind `json:"kind"`
	Target     string       `json:"target,omitempty"`
	Content    string       `json:"content"`
	Confidence float64      `json:"confidence,omitempty"`
	Evidence   []string     `json:"evidence,omitempty"`
	Source     string       `json:"source,omitempty"` // ai / rule / user
	CreatedAt  time.Time    `json:"created_at"`
}

// AIMemoryStore 管理 AI 记忆（JSON 原子持久化）。
type AIMemoryStore struct {
	mu       sync.Mutex
	saveMu   sync.Mutex // 序列化并发 Save（last-writer-wins 保护）
	storeDir string
	entries  []AIMemoryEntry
	seq      int
}

// NewAIMemoryStore 创建记忆存储。storeDir 为空则仅内存。
func NewAIMemoryStore(storeDir string) *AIMemoryStore {
	s := &AIMemoryStore{storeDir: storeDir}
	if storeDir != "" {
		s.load()
	}
	return s
}

// Record 记录一条记忆（同 kind+target+content 幂等去重）。
func (s *AIMemoryStore) Record(kind AIMemoryKind, target, content, source string, confidence float64, evidence []string) *AIMemoryEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.Kind == kind && e.Target == target && e.Content == content {
			return &e
		}
	}
	s.seq++
	entry := &AIMemoryEntry{
		ID:         "m" + itoa(s.seq),
		Kind:       kind,
		Target:     target,
		Content:    content,
		Confidence: clampConfidence(confidence),
		Evidence:   append([]string(nil), evidence...),
		Source:     source,
		CreatedAt:  time.Now().UTC(),
	}
	s.entries = append(s.entries, *entry)
	return entry
}

// List 按目标与类型过滤（按时间倒序，limit<=0 不限）。
func (s *AIMemoryStore) List(target string, kind AIMemoryKind, limit int) []AIMemoryEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []AIMemoryEntry
	for i := len(s.entries) - 1; i >= 0; i-- {
		e := s.entries[i]
		if target != "" && e.Target != target {
			continue
		}
		if kind != "" && e.Kind != kind {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// Summarize 生成上下文用的记忆摘要（最多 n 条，含目标匹配优先）。
func (s *AIMemoryStore) Summarize(target string, n int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	seen := map[string]bool{}
	add := func(e AIMemoryEntry) {
		if seen[e.ID] {
			return
		}
		seen[e.ID] = true
		out = append(out, "["+string(e.Kind)+"] "+e.Content)
	}
	if n <= 0 {
		n = 8
	}
	// 目标相关优先
	for i := len(s.entries) - 1; i >= 0 && len(out) < n; i-- {
		if s.entries[i].Target == target {
			add(s.entries[i])
		}
	}
	for i := len(s.entries) - 1; i >= 0 && len(out) < n; i-- {
		add(s.entries[i])
	}
	return out
}

// Save 持久化（原子写 + 并发序列化，防止多调查并发丢记忆）。
func (s *AIMemoryStore) Save() error {
	if s.storeDir == "" {
		return nil
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	data, err := json.MarshalIndent(s.entries, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.storeDir, 0755); err != nil {
		return err
	}
	path := filepath.Join(s.storeDir, "ai_memory.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// load 启动时加载记忆。
func (s *AIMemoryStore) load() {
	data, err := os.ReadFile(filepath.Join(s.storeDir, "ai_memory.json"))
	if err != nil {
		return
	}
	var entries []AIMemoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}
	s.entries = entries
	for _, e := range entries {
		// 恢复自增序号
		if len(e.ID) > 1 && e.ID[0] == 'm' {
			if n := parseIDNum(e.ID[1:]); n > s.seq {
				s.seq = n
			}
		}
	}
}

// parseIDNum 解析记忆 ID 数字部分。
func parseIDNum(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
