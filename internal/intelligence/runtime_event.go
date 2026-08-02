package intelligence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ── Runtime Event（V2 设计 §13）──
//
// 运行时事件：task_created / executed / retried / failed / replanned，
// 追加写入 runtime-events.log（JSON 行），供运行状态追踪与排障。

// RuntimeEventType 是运行时事件类型。
type RuntimeEventType string

const (
	EventTaskCreated  RuntimeEventType = "task_created" // 任务创建/入队
	EventTaskExecuted RuntimeEventType = "executed"     // 任务执行成功
	EventTaskRetried  RuntimeEventType = "retried"      // 任务失败重试
	EventTaskFailed   RuntimeEventType = "failed"       // 任务失败（终态）
	EventReplanned    RuntimeEventType = "replanned"    // Re-plan 触发
)

// RuntimeEvent 是一条运行时事件记录。
type RuntimeEvent struct {
	Timestamp       int64            `json:"timestamp"`
	InvestigationID string           `json:"investigation_id"`
	Type            RuntimeEventType `json:"type"`
	TaskID          string           `json:"task_id,omitempty"`
	TaskType        string           `json:"task_type,omitempty"`
	Round           int              `json:"round,omitempty"`
	Detail          string           `json:"detail,omitempty"`
}

// RuntimeEventLog 是运行时事件日志追加器（runtime-events.log，JSON 行）。
type RuntimeEventLog struct {
	mu   sync.Mutex
	path string // 空 = 仅内存（测试用）
}

// NewRuntimeEventLog 创建事件日志。path 为空则丢弃（测试用）。
func NewRuntimeEventLog(path string) *RuntimeEventLog {
	return &RuntimeEventLog{path: path}
}

// Log 追加一条事件（JSON 行，原子追加）。
func (l *RuntimeEventLog) Log(ev RuntimeEvent) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().Unix()
	}
	if l.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0755); err != nil {
		return
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

// TaskCreated 记录任务创建事件。
func (l *RuntimeEventLog) TaskCreated(invID string, task *InvestigationTask) {
	l.Log(RuntimeEvent{InvestigationID: invID, Type: EventTaskCreated, TaskID: task.ID, TaskType: task.Type, Round: task.Round})
}

// TaskExecuted 记录任务执行成功事件。
func (l *RuntimeEventLog) TaskExecuted(invID string, task *InvestigationTask, detail string) {
	l.Log(RuntimeEvent{InvestigationID: invID, Type: EventTaskExecuted, TaskID: task.ID, TaskType: task.Type, Round: task.Round, Detail: detail})
}

// TaskRetried 记录任务失败重试事件。
func (l *RuntimeEventLog) TaskRetried(invID string, task *InvestigationTask, attempt int) {
	l.Log(RuntimeEvent{InvestigationID: invID, Type: EventTaskRetried, TaskID: task.ID, TaskType: task.Type, Round: task.Round, Detail: "重试 " + itoa(attempt) + "/" + itoa(task.MaxRetries)})
}

// TaskFailed 记录任务失败事件。
func (l *RuntimeEventLog) TaskFailed(invID string, task *InvestigationTask, detail string) {
	l.Log(RuntimeEvent{InvestigationID: invID, Type: EventTaskFailed, TaskID: task.ID, TaskType: task.Type, Round: task.Round, Detail: detail})
}

// Replanned 记录 Re-plan 触发事件。
func (l *RuntimeEventLog) Replanned(invID string, reason ReplanReason, round, newTasks int) {
	l.Log(RuntimeEvent{InvestigationID: invID, Type: EventReplanned, Round: round, Detail: string(reason) + " 新任务 " + itoa(newTasks)})
}
