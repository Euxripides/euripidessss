package prefetch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Lease 是预取批处理的租约（设计 P1：Lease Recovery）。
type Lease struct {
	JobID       string    `json:"job_id"`
	BatchID     string    `json:"batch_id"`
	Owner       string    `json:"owner"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// LeaseStore 维护预取任务租约。
type LeaseStore struct {
	root string
	mu   sync.Mutex
	ttl  time.Duration
}

// NewLeaseStore 创建租约存储。
func NewLeaseStore(root string, ttl time.Duration) *LeaseStore {
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	return &LeaseStore{root: filepath.Join(root, "leases"), ttl: ttl}
}

// Acquire 获取租约（已存在则续约）。
func (s *LeaseStore) Acquire(jobID, batchID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	l := &Lease{JobID: jobID, BatchID: batchID, Owner: "prefetch-manager", HeartbeatAt: now, ExpiresAt: now.Add(s.ttl)}
	return s.writeLocked(l)
}

// Renew 续约心跳。
func (s *LeaseStore) Renew(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.getLocked(jobID)
	if l == nil {
		return os.ErrNotExist
	}
	now := time.Now().UTC()
	l.HeartbeatAt = now
	l.ExpiresAt = now.Add(s.ttl)
	return s.writeLocked(l)
}

// Release 释放租约。
func (s *LeaseStore) Release(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.Remove(filepath.Join(s.root, jobID+".json"))
}

// Get 返回租约。
func (s *LeaseStore) Get(jobID string) *Lease {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(jobID)
}

func (s *LeaseStore) getLocked(jobID string) *Lease {
	payload, err := os.ReadFile(filepath.Join(s.root, jobID+".json"))
	if err != nil {
		return nil
	}
	var l Lease
	if json.Unmarshal(payload, &l) != nil {
		return nil
	}
	return &l
}

func (s *LeaseStore) writeLocked(l *Lease) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	payload, _ := json.MarshalIndent(l, "", "  ")
	path := filepath.Join(s.root, l.JobID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

