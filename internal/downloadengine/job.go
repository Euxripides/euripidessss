package downloadengine

import (
	"fmt"
	"sync"
	"time"
)

// ── Job ──

type Job struct {
	mu sync.RWMutex

	ID               string     `json:"job_id"`
	Type             JobType    `json:"job_type"`
	ChainID          string     `json:"chain_id"`
	Status           JobStatus  `json:"status"`
	Stage            JobStage   `json:"stage"`
	Priority         Priority   `json:"priority"`
	RangeMode        RangeMode  `json:"range_mode"`
	EffectiveRange   *EffectiveRange `json:"effective_range,omitempty"`
	Discovery        *DiscoveryResult `json:"discovery,omitempty"`
	Chunks           []*Chunk   `json:"chunks,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	ErrorCode        ErrorCode  `json:"error_code,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
}

// ── 合法状态转换表 ──

var validTransitions = map[JobStatus][]JobStatus{
	StatusCreated:    {StatusValidating},
	StatusValidating: {StatusQueued, StatusFailed},
	StatusQueued:     {StatusRunning, StatusCanceling, StatusFailed},
	StatusRunning:    {StatusPausing, StatusCanceling, StatusCompleted, StatusFailed},
	StatusPausing:    {StatusPaused, StatusFailed},
	StatusPaused:     {StatusRunning, StatusCanceling},
	StatusCanceling:  {StatusCancelled, StatusFailed},
	// 终态
	StatusCompleted:  {},
	StatusCancelled:  {},
	StatusFailed:     {},
}

// ── Transition 是唯一的状态修改入口 ──

func (j *Job) Transition(target JobStatus) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.Status == target {
		return nil // 幂等
	}

	allowed, ok := validTransitions[j.Status]
	if !ok {
		return &InvalidTransitionError{From: j.Status, To: target, Reason: "未知源状态"}
	}
	if !contains(allowed, target) {
		return &InvalidTransitionError{From: j.Status, To: target, Reason: "非法的状态转换"}
	}

	j.Status = target
	j.UpdatedAt = time.Now().UTC()
	if isTerminal(target) {
		now := time.Now().UTC()
		j.FinishedAt = &now
	}
	return nil
}

func (j *Job) SetStage(stage JobStage) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Stage = stage
	j.UpdatedAt = time.Now().UTC()
}

func (j *Job) StatusSnapshot() JobStatus {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.Status
}

func (j *Job) StageSnapshot() JobStage {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.Stage
}

func isTerminal(s JobStatus) bool {
	return s == StatusCompleted || s == StatusCancelled || s == StatusFailed
}

func contains(slice []JobStatus, target JobStatus) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}

// ── 错误类型 ──

type InvalidTransitionError struct {
	From   JobStatus
	To     JobStatus
	Reason string
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("downloadengine: 不允许从 %s 转换到 %s: %s", e.From, e.To, e.Reason)
}
