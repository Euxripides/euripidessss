package smartdownload

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// BlockRange 区块区间（Provider 无关的断点位置）。
type BlockRange struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
}

// Key 区间唯一键（Range Ledger 内引用）。
func (r BlockRange) Key() string { return fmt.Sprintf("%d-%d", r.From, r.To) }

// PartInfo 已提交 Part（文件是最终事实的一部分）。
type PartInfo struct {
	Name      string `json:"name"`
	SHA256    string `json:"sha256"`
	Rows      int64  `json:"rows"`
	Bytes     int64  `json:"bytes"`
	RangeFrom uint64 `json:"range_from"`
	RangeTo   uint64 `json:"range_to"`
}

// CheckpointV3 Universal Checkpoint（实施方案 §4）：
// 主断点是“哪些 Range 已完成 / 哪些 Part 已 commit / 哪些 Range 为空 / 哪些还没完成”，
// Provider 私有状态单独放 provider_state，切换时保留 completed_ranges+parts。
type CheckpointV3 struct {
	Version              int            `json:"version"`
	DatasetJobID         string         `json:"dataset_job_id"`
	Address              string         `json:"address"`
	Dataset              string         `json:"dataset"`
	RequestedFrom        uint64         `json:"requested_from"`
	RequestedTo          uint64         `json:"requested_to"`
	CompletedRanges      []BlockRange   `json:"completed_ranges"`
	ConfirmedEmptyRanges []BlockRange   `json:"confirmed_empty_ranges"`
	PendingRanges        []BlockRange   `json:"pending_ranges"`
	Parts                []PartInfo     `json:"parts"`
	ProviderState        map[string]any `json:"provider_state,omitempty"`
	RowsCommitted        uint64         `json:"rows_committed"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

// CheckpointStore Checkpoint V3 文件存储。
type CheckpointStore struct {
	mu  sync.Mutex
	dir string
}

// NewCheckpointStore 创建 checkpoint 存储（root/smart_download/checkpoints）。
func NewCheckpointStore(root string) *CheckpointStore {
	return &CheckpointStore{dir: filepath.Join(root, "smart_download", "checkpoints")}
}

func (s *CheckpointStore) Dir() string { return s.dir }

func (s *CheckpointStore) Save(cp *CheckpointV3) error {
	if cp == nil || cp.DatasetJobID == "" {
		return errors.New("checkpoint dataset_job_id 为空")
	}
	cp.Version = 3
	cp.UpdatedAt = time.Now().UTC()
	payload, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.dir, cp.DatasetJobID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *CheckpointStore) Load(datasetJobID string) (*CheckpointV3, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, datasetJobID+".json")
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("checkpoint not found: %s", datasetJobID)
		}
		return nil, err
	}
	var cp CheckpointV3
	if err := json.Unmarshal(payload, &cp); err != nil {
		return nil, fmt.Errorf("parse checkpoint %s: %w", datasetJobID, err)
	}
	return &cp, nil
}

func (s *CheckpointStore) Delete(datasetJobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, datasetJobID+".json")
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SplitBlockRange 把区块范围切成固定大小区间（默认 50,000 块）。
func SplitBlockRange(from, to, chunkSize uint64) []BlockRange {
	if from > to || chunkSize == 0 {
		return nil
	}
	var out []BlockRange
	for cur := from; cur <= to; cur += chunkSize {
		end := cur + chunkSize - 1
		if end > to {
			end = to
		}
		out = append(out, BlockRange{From: cur, To: end})
	}
	return out
}

// Init 初始化 checkpoint 并切分 pending ranges。
func (cp *CheckpointV3) Init(datasetJobID, address, dataset string, requested BlockRange, chunkSize uint64) {
	cp.Version = 3
	cp.DatasetJobID = datasetJobID
	cp.Address = address
	cp.Dataset = dataset
	cp.RequestedFrom = requested.From
	cp.RequestedTo = requested.To
	cp.PendingRanges = SplitBlockRange(requested.From, requested.To, chunkSize)
	cp.UpdatedAt = time.Now().UTC()
}

// RangeKeySet 区间集合工具。
type RangeKeySet map[string]BlockRange

func (s RangeKeySet) Add(r BlockRange)      { s[r.Key()] = r }
func (s RangeKeySet) Has(r BlockRange) bool { _, ok := s[r.Key()]; return ok }

// CompleteRange 完成一个 Range 并登记 Part。
func (cp *CheckpointV3) CompleteRange(r BlockRange, part *PartInfo) {
	cp.CompletedRanges = append(cp.CompletedRanges, r)
	cp.PendingRanges = removeRange(cp.PendingRanges, r)
	if part != nil {
		cp.Parts = append(cp.Parts, *part)
		cp.RowsCommitted += uint64(part.Rows)
	}
	cp.UpdatedAt = time.Now().UTC()
}

// ConfirmEmpty 确认某个 Range 为空（合法覆盖，不产生 Part）。
func (cp *CheckpointV3) ConfirmEmpty(r BlockRange) {
	cp.ConfirmedEmptyRanges = append(cp.ConfirmedEmptyRanges, r)
	cp.PendingRanges = removeRange(cp.PendingRanges, r)
	cp.UpdatedAt = time.Now().UTC()
}

// IsRangeDone 判断区间是否已完成或确认空。
func (cp *CheckpointV3) IsRangeDone(r BlockRange) bool {
	for _, c := range cp.CompletedRanges {
		if c.From == r.From && c.To == r.To {
			return true
		}
	}
	for _, c := range cp.ConfirmedEmptyRanges {
		if c.From == r.From && c.To == r.To {
			return true
		}
	}
	return false
}

// Remaining 返回尚未完成/未确认空的区间（按 from 排序）。
func (cp *CheckpointV3) Remaining() []BlockRange {
	done := RangeKeySet{}
	for _, r := range cp.CompletedRanges {
		done.Add(r)
	}
	for _, r := range cp.ConfirmedEmptyRanges {
		done.Add(r)
	}
	var out []BlockRange
	for _, r := range cp.PendingRanges {
		if !done.Has(r) {
			out = append(out, r)
		}
	}
	sortRanges(out)
	return out
}

// NextPartName 返回下一个 part 文件名（单调递增，Provider 无关；扩展名由写入器决定）。
func (cp *CheckpointV3) NextPartName(ext string) string {
	if ext == "" {
		ext = ".jsonl"
	}
	return fmt.Sprintf("part-%06d%s", len(cp.Parts)+1, ext)
}

// SortRanges 按 from 排序（导出给 recovery 用）。
func SortRanges(list []BlockRange) { sortRanges(list) }

func sortRanges(list []BlockRange) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].From == list[j].From {
			return list[i].To < list[j].To
		}
		return list[i].From < list[j].From
	})
}

func removeRange(list []BlockRange, target BlockRange) []BlockRange {
	out := make([]BlockRange, 0, len(list))
	for _, r := range list {
		if r.From == target.From && r.To == target.To {
			continue
		}
		out = append(out, r)
	}
	return out
}

// ValidBlockRange 校验区间合法。
func ValidBlockRange(r BlockRange) bool {
	return r.To >= r.From
}

// KeyFromString 解析 "from-to" 区间键（recovery/ledger 用）。
func KeyFromString(key string) (BlockRange, bool) {
	parts := strings.SplitN(key, "-", 2)
	if len(parts) != 2 {
		return BlockRange{}, false
	}
	var r BlockRange
	if _, err := fmt.Sscanf(parts[0], "%d", &r.From); err != nil {
		return BlockRange{}, false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &r.To); err != nil {
		return BlockRange{}, false
	}
	return r, ValidBlockRange(r)
}
