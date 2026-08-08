package prefetch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Queue 是持久化预取队列（设计 §59 queue.go）。
// 存储：{root}/queue.json
type Queue struct {
	root string
	mu   sync.Mutex
	jobs map[string]*Job
}

// NewQueue 创建队列并加载已有任务。
func NewQueue(root string) (*Queue, error) {
	q := &Queue{root: root, jobs: map[string]*Job{}}
	if err := q.load(); err != nil {
		return nil, err
	}
	return q, nil
}

// Enqueue 入队；同一地址+Token+范围+数据集只保留一份（设计 §73 Case E）。
func (q *Queue) Enqueue(c Candidate) (*Job, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	c.Address = strings.ToLower(strings.TrimSpace(c.Address))
	c.InvestigationID = strings.TrimSpace(c.InvestigationID)
	now := time.Now().UTC()
	key := candidateKey(c)
	for _, j := range q.jobs {
		if candidateKey(j.Candidate) == key && j.Status != StatusEvicted && j.Status != StatusFailed {
			// 已有任务：若新候选优先级更高则提升
			j.Candidate.RequiredDatasets = unionDatasets(j.Candidate.RequiredDatasets, c.RequiredDatasets)
			if c.Score > j.Candidate.Score || c.Priority == PriorityHOT {
				j.Candidate.Score = c.Score
				j.Candidate.Priority = c.Priority
			}
			j.UpdatedAt = now
			_ = q.saveLocked()
			return j, false, nil
		}
		if candidateKey(j.Candidate) == key {
			// 旧任务已失败/驱逐：原地复用同一任务 ID，更新候选并重置为 PENDING
			j.Candidate = c
			j.Status = StatusPending
			j.BatchID = ""
			j.BatchStatus = ""
			j.UpgradeCount = 0
			j.UsedAt = nil
			j.CreatedAt = now
			j.UpdatedAt = now
			j.StartedAt = nil
			j.FinishedAt = nil
			_ = q.saveLocked()
			return j, false, nil
		}
	}
	job := &Job{
		ID:        uuid.NewString(),
		Candidate: c,
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	q.jobs[job.ID] = job
	if err := q.saveLocked(); err != nil {
		delete(q.jobs, job.ID)
		return nil, false, err
	}
	return job, true, nil
}

// Get 按 ID 读取。
func (q *Queue) Get(id string) *Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return nil
	}
	cp := *j
	return &cp
}

// List 返回全部任务（按 HOT 优先、Score 降序）。
func (q *Queue) List() []*Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*Job, 0, len(q.jobs))
	for _, j := range q.jobs {
		cp := *j
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Candidate.Priority != out[j].Candidate.Priority {
			return priorityRank(out[i].Candidate.Priority) < priorityRank(out[j].Candidate.Priority)
		}
		return out[i].Candidate.Score > out[j].Candidate.Score
	})
	return out
}

// ListByInvestigation 返回指定调查的任务。
func (q *Queue) ListByInvestigation(invID string) []*Job {
	var out []*Job
	for _, j := range q.List() {
		if j.Candidate.InvestigationID == invID {
			out = append(out, j)
		}
	}
	return out
}

// UpdateStatus 更新状态并持久化。
func (q *Queue) UpdateStatus(id string, status Status) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return os.ErrNotExist
	}
	now := time.Now().UTC()
	j.Status = status
	j.UpdatedAt = now
	if status == StatusPrefetching || status == StatusInteractive {
		t := now
		if j.StartedAt == nil {
			j.StartedAt = &t
		}
	}
	if status == StatusReady || status == StatusFailed || status == StatusEvicted {
		t := now
		j.FinishedAt = &t
	}
	return q.saveLocked()
}

// SetBatch 关联 Smart Download Batch。
func (q *Queue) SetBatch(id, batchID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return os.ErrNotExist
	}
	j.BatchID = batchID
	j.UpdatedAt = time.Now().UTC()
	return q.saveLocked()
}

// UpdateBatchStatus 持久化 Smart Download Batch 状态。
func (q *Queue) UpdateBatchStatus(id, status string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return os.ErrNotExist
	}
	j.BatchStatus = status
	j.UpdatedAt = time.Now().UTC()
	return q.saveLocked()
}

// Upgrade 用户点击升级（设计 §53-§54）：优先级 → HOT，任务 ID 不变。
func (q *Queue) Upgrade(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return os.ErrNotExist
	}
	now := time.Now().UTC()
	j.Candidate.Priority = PriorityHOT
	j.Status = StatusInteractive
	j.UpgradeCount++
	j.UpdatedAt = now
	if j.StartedAt == nil {
		j.StartedAt = &now
	}
	return q.saveLocked()
}

// FindByAddress 按链+地址查找任务。
func (q *Queue) FindByAddress(chainKey, address string) []*Job {
	address = strings.ToLower(strings.TrimSpace(address))
	var out []*Job
	for _, j := range q.List() {
		if strings.EqualFold(j.Candidate.ChainKey, chainKey) && strings.EqualFold(j.Candidate.Address, address) {
			out = append(out, j)
		}
	}
	return out
}

// Active 返回进行中任务数。
func (q *Queue) Active() int {
	n := 0
	for _, j := range q.List() {
		if j.Status == StatusPrefetching || j.Status == StatusInteractive {
			n++
		}
	}
	return n
}

// ReadyCount 返回已就绪任务数。
func (q *Queue) ReadyCount() int {
	n := 0
	for _, j := range q.List() {
		if j.Status == StatusReady {
			n++
		}
	}
	return n
}

func (q *Queue) load() error {
	if q.root == "" {
		return nil
	}
	payload, err := os.ReadFile(q.path())
	if err != nil {
		return nil
	}
	var items []*Job
	if err := json.Unmarshal(payload, &items); err != nil {
		return err
	}
	byKey := map[string]*Job{}
	for _, j := range items {
		if j == nil || j.ID == "" {
			continue
		}
		key := candidateKey(j.Candidate)
		if existing, ok := byKey[key]; ok {
			existing.Candidate.RequiredDatasets = unionDatasets(existing.Candidate.RequiredDatasets, j.Candidate.RequiredDatasets)
			existing.UpgradeCount += j.UpgradeCount
			if statusRank(j.Status) < statusRank(existing.Status) {
				existing.Status = j.Status
				existing.BatchID = j.BatchID
				existing.BatchStatus = j.BatchStatus
				existing.CreatedAt = j.CreatedAt
				existing.StartedAt = j.StartedAt
				existing.FinishedAt = j.FinishedAt
			}
			continue
		}
		q.jobs[j.ID] = j
		byKey[key] = j
	}
	return nil
}

func (q *Queue) saveLocked() error {
	if q.root == "" {
		return nil
	}
	if err := os.MkdirAll(q.root, 0o755); err != nil {
		return err
	}
	items := make([]*Job, 0, len(q.jobs))
	for _, j := range q.jobs {
		items = append(items, j)
	}
	payload, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	tmp := q.path() + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, q.path())
}

func (q *Queue) path() string {
	return filepath.Join(q.root, "queue.json")
}

// Root 返回队列根目录。
func (q *Queue) Root() string {
	return q.root
}

func candidateKey(c Candidate) string {
	return strings.Join([]string{
		strings.ToLower(c.ChainKey), strings.ToLower(c.Address),
		strings.ToLower(c.TokenFilter), itoa64(c.FromBlock), itoa64(c.ToBlock),
	}, "|")
}

func priorityRank(p Priority) int {
	switch p {
	case PriorityHOT:
		return 0
	case PriorityWARM:
		return 1
	default:
		return 2
	}
}

func itoa64(v uint64) string {
	return formatUint64(v)
}

func formatUint64(v uint64) string {
	return strings.TrimSpace(jsonNumber(v))
}

func jsonNumber(v uint64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func unionDatasets(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func statusRank(s Status) int {
	switch s {
	case StatusReady:
		return 0
	case StatusPrefetching, StatusInteractive:
		return 1
	case StatusPaused:
		return 2
	case StatusPending:
		return 3
	case StatusFailed:
		return 4
	default:
		return 5
	}
}
