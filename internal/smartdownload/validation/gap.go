package validation

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// GapType 缺口类型（设计 §18）。
type GapType string

const (
	GapRangeGap           GapType = "RANGE_GAP"
	GapCountGap           GapType = "COUNT_GAP"
	GapSequenceGap        GapType = "SEQUENCE_GAP"
	GapSuspiciousEmpty    GapType = "SUSPICIOUS_EMPTY"
	GapProviderDivergence GapType = "PROVIDER_DIVERGENCE"
	GapPartGap            GapType = "PART_GAP"
	GapTimeGap            GapType = "TIME_GAP"
)

// GapStatus 缺口状态。
type GapStatus string

const (
	GapDetected  GapStatus = "DETECTED"
	GapRepairing GapStatus = "REPAIRING"
	GapRepaired  GapStatus = "REPAIRED"
	GapFailed    GapStatus = "FAILED"
)

// GapRecord Gap Ledger 条目（设计 §51）。
type GapRecord struct {
	GapID      string     `json:"gap_id"`
	Type       GapType    `json:"type"`
	FromBlock  uint64     `json:"from_block"`
	ToBlock    uint64     `json:"to_block"`
	Status     GapStatus  `json:"status"`
	Provider   string     `json:"provider,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	Rows       int64      `json:"rows,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	RepairedAt *time.Time `json:"repaired_at,omitempty"`
}

// RepairAttempt 补洞尝试记录（设计 §28/§50）。
type RepairAttempt struct {
	GapID      string    `json:"gap_id"`
	Provider   string    `json:"provider"`
	Attempt    int       `json:"attempt"`
	Success    bool      `json:"success"`
	Rows       int64     `json:"rows,omitempty"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// RangeCount 单个 Range 的行数（COUNT_GAP 二分输入）。
type RangeCount struct {
	FromBlock uint64
	ToBlock   uint64
	Rows      int64
}

// RangeGaps 用 IntervalSet 找出 RANGE_GAP。
func RangeGaps(requested BlockInterval, valid, empty []BlockInterval) []GapRecord {
	covered := FromIntervals(valid)
	for _, e := range empty {
		covered.Add(e.From, e.To)
	}
	var out []GapRecord
	for _, g := range covered.FindGaps(requested.From, requested.To) {
		out = append(out, GapRecord{
			GapID: fmt.Sprintf("%d_%d", g.From, g.To),
			Type:  GapRangeGap, FromBlock: g.From, ToBlock: g.To,
			Status: GapDetected, CreatedAt: time.Now().UTC(),
		})
	}
	return out
}

// SuspiciousEmpty 空区间被两侧高密度邻居夹住 → SUSPICIOUS_EMPTY（设计 §13/§22）。
// allRanges 为全部分区（含空区间）；rowsByRange 键为 "from_to"。
func SuspiciousEmpty(empty []BlockInterval, allRanges []BlockInterval, rowsByRange map[string]int64, threshold int64) []GapRecord {
	sorted := append([]BlockInterval(nil), allRanges...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].From < sorted[j].From })
	key := func(iv BlockInterval) string { return fmt.Sprintf("%d_%d", iv.From, iv.To) }
	var out []GapRecord
	for _, e := range empty {
		var left, right int64
		for _, r := range sorted {
			if r.To+1 == e.From {
				left = rowsByRange[key(r)]
			}
			if r.From == e.To+1 {
				right = rowsByRange[key(r)]
			}
		}
		if left <= threshold || right <= threshold {
			continue
		}
		out = append(out, GapRecord{
			GapID: fmt.Sprintf("sus_empty_%d_%d", e.From, e.To),
			Type:  GapSuspiciousEmpty, FromBlock: e.From, ToBlock: e.To,
			Status: GapDetected, Reason: fmt.Sprintf("两侧邻居均有大量数据（左 %d / 右 %d），空区间需第二 Provider 确认", left, right),
			CreatedAt: time.Now().UTC(),
		})
	}
	return out
}

// CountBisect 二分定位 COUNT_GAP（设计 §21/§58）：
// providerTotal 为 Provider 声称总数，countOf 返回某区间实际数；递归二分找异常子区间。
func CountBisect(from, to uint64, providerTotal, actualTotal int64, countOf func(from, to uint64) (int64, error)) []BlockInterval {
	if providerTotal == actualTotal {
		return nil
	}
	const smallSpan = 1024 // 收敛到小区间后交给第二 Provider 重查（设计 §21）
	var out []BlockInterval
	var walk func(f, t uint64, expected int64)
	walk = func(f, t uint64, expected int64) {
		blocks := t - f + 1
		if blocks <= smallSpan {
			if actual, err := countOf(f, t); err == nil && actual != expected {
				out = append(out, BlockInterval{From: f, To: t})
			}
			return
		}
		mid := f + (t-f)/2
		leftActual, err := countOf(f, mid)
		if err != nil {
			return
		}
		rightActual, err := countOf(mid+1, t)
		if err != nil {
			return
		}
		leftBlocks := mid - f + 1
		leftExpected := int64(math.Round(float64(expected) * float64(leftBlocks) / float64(blocks)))
		rightExpected := expected - leftExpected
		if leftActual != leftExpected {
			walk(f, mid, leftExpected)
		}
		if rightActual != rightExpected {
			walk(mid+1, t, rightExpected)
		}
	}
	walk(from, to, providerTotal)
	return out
}

// GapStore 每 Dataset 的 Validation 文件存储（设计 §50）。
type GapStore struct {
	dir string
}

// NewGapStore 创建存储（{root}/smart_download/validation/{dsID}/）。
func NewGapStore(root, dsID string) *GapStore {
	return &GapStore{dir: filepath.Join(root, "smart_download", "validation", dsID)}
}

func (s *GapStore) Dir() string { return s.dir }

func (s *GapStore) append(path string, v any) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, path), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(payload, '\n'))
	return err
}

func (s *GapStore) AppendGap(g GapRecord) error {
	if g.GapID == "" {
		g.GapID = fmt.Sprintf("gap_%d_%d", g.FromBlock, g.ToBlock)
	}
	return s.append("gap-ledger.ndjson", g)
}

func (s *GapStore) AppendRepair(r RepairAttempt) error {
	return s.append("repair-attempts.ndjson", r)
}

func (s *GapStore) LoadGaps() ([]GapRecord, error) {
	return loadJSONL[GapRecord](filepath.Join(s.dir, "gap-ledger.ndjson"))
}

func (s *GapStore) LoadRepairs() ([]RepairAttempt, error) {
	return loadJSONL[RepairAttempt](filepath.Join(s.dir, "repair-attempts.ndjson"))
}

// RepairCount 返回某 gap 的尝试次数。
func (s *GapStore) RepairCount(gapID string) (int, error) {
	repairs, err := s.LoadRepairs()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range repairs {
		if r.GapID == gapID {
			n++
		}
	}
	return n, nil
}

func loadJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, sc.Err()
}

// SortGaps 按 From 排序。
func SortGaps(gaps []GapRecord) {
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].FromBlock < gaps[j].FromBlock })
}
