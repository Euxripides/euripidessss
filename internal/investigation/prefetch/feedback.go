package prefetch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Feedback 记录预取使用情况并计算命中率（设计 §34-§37、§74）。
type Feedback struct {
	root    string
	mu      sync.Mutex
	records []FeedbackRecord
}

// NewFeedback 创建反馈存储。
func NewFeedback(root string) (*Feedback, error) {
	f := &Feedback{root: root}
	if err := f.load(); err != nil {
		return nil, err
	}
	return f, nil
}

// Record 追加一条反馈。
func (f *Feedback) Record(r FeedbackRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.RecordedAt = time.Now().UTC()
	f.records = append(f.records, r)
	if len(f.records) > 20000 {
		f.records = f.records[len(f.records)-20000:]
	}
	return f.saveLocked()
}

// RecordUse 记录预取数据被用户使用（点击地址 → 秒开）。
func (f *Feedback) RecordUse(invID, address, batchID string, savedWaitSeconds float64) error {
	return f.Record(FeedbackRecord{
		InvestigationID: invID, Address: address, BatchID: batchID,
		Used: true, SavedWaitSeconds: savedWaitSeconds,
	})
}

// RecordUnused 记录预取数据长期未使用。
func (f *Feedback) RecordUnused(invID, address, batchID string, costBytes uint64) error {
	return f.Record(FeedbackRecord{
		InvestigationID: invID, Address: address, BatchID: batchID,
		Used: false, DownloadCostBytes: costBytes,
	})
}

// Stats 返回命中率与节省延迟（设计 §36-§37）。
func (f *Feedback) Stats() FeedbackStats {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := FeedbackStats{}
	totalSaved := 0.0
	for _, r := range f.records {
		if r.Invalidated {
			continue
		}
		st.Total++
		if r.Used {
			st.Used++
			totalSaved += r.SavedWaitSeconds
		} else {
			st.Unused++
			st.WastedBytes += r.DownloadCostBytes
		}
	}
	if st.Total > 0 {
		st.HitRate = float64(st.Used) / float64(st.Total)
	}
	if st.Used > 0 {
		st.SavedLatencyAvg = totalSaved / float64(st.Used)
	}
	return st
}

// ReuseProbability 返回地址的复用概率（设计 §34：未使用 → 下降）。
func (f *Feedback) ReuseProbability(address string) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	total, used := 0, 0
	for _, r := range f.records {
		if r.Invalidated || r.Address != address {
			continue
		}
		total++
		if r.Used {
			used++
		}
	}
	if total == 0 {
		return 0.5
	}
	p := float64(used) / float64(total)
	if p < 0.1 {
		return 0.1 // 连续未使用 → 最低 10%
	}
	return p
}

// UnusedSince 返回早于 cutoff 且未使用的地址（供驱逐降权）。
func (f *Feedback) UnusedSince(cutoff time.Time) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, r := range f.records {
		if r.Invalidated || r.Used || !r.RecordedAt.Before(cutoff) {
			continue
		}
		if !seen[r.Address] {
			seen[r.Address] = true
			out = append(out, r.Address)
		}
	}
	return out
}

// InvalidateUse keeps the historical record for audit while excluding a
// legacy false-positive upgrade from hit-rate and reuse-probability metrics.
func (f *Feedback) InvalidateUse(invID, address, batchID, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	changed := false
	for i := range f.records {
		r := &f.records[i]
		if r.Invalidated || !r.Used || r.InvestigationID != invID || r.Address != address || r.BatchID != batchID {
			continue
		}
		r.Invalidated = true
		r.InvalidatedAt = &now
		r.InvalidReason = reason
		changed = true
	}
	if !changed {
		return nil
	}
	return f.saveLocked()
}

func (f *Feedback) load() error {
	payload, err := os.ReadFile(f.path())
	if err != nil {
		return nil
	}
	return json.Unmarshal(payload, &f.records)
}

func (f *Feedback) saveLocked() error {
	if f.root == "" {
		return nil
	}
	if err := os.MkdirAll(f.root, 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(f.records, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.path() + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f.path())
}

func (f *Feedback) path() string {
	return filepath.Join(f.root, "feedback.json")
}
