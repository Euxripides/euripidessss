// Package validation 实现 Validation Pipeline V3 + Gap Repair Engine V1.0：
// IntervalSet、Gap Detector、Gap Ledger、Repair Planner、Validation Certificate。
package validation

import "sort"

// BlockInterval 区块区间。
type BlockInterval struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
}

// IntervalSet 区间集合（Add/Merge/Subtract/Intersect/FindGaps，设计 §10）。
type IntervalSet struct {
	items []BlockInterval
}

// NewIntervalSet 创建空集合。
func NewIntervalSet() *IntervalSet { return &IntervalSet{} }

// FromIntervals 从列表创建并合并。
func FromIntervals(list []BlockInterval) *IntervalSet {
	s := NewIntervalSet()
	for _, iv := range list {
		s.Add(iv.From, iv.To)
	}
	return s
}

// Add 加入区间并合并重叠/相邻。
func (s *IntervalSet) Add(from, to uint64) {
	if to < from {
		return
	}
	s.items = append(s.items, BlockInterval{From: from, To: to})
	s.items = merge(s.items)
}

// Items 返回合并后的有序区间。
func (s *IntervalSet) Items() []BlockInterval {
	out := make([]BlockInterval, len(s.items))
	copy(out, s.items)
	return out
}

// Blocks 覆盖总块数。
func (s *IntervalSet) Blocks() uint64 {
	var n uint64
	for _, iv := range s.items {
		n += iv.To - iv.From + 1
	}
	return n
}

// Subtract 返回本集合减去 other 后的剩余区间。
func (s *IntervalSet) Subtract(other *IntervalSet) []BlockInterval {
	var gaps []BlockInterval
	cut := other.Items()
	for _, iv := range s.items {
		cur := iv.From
		for _, c := range cut {
			if c.To < cur {
				continue
			}
			if c.From > cur {
				end := c.From - 1
				if end > iv.To {
					end = iv.To
				}
				if end >= cur {
					gaps = append(gaps, BlockInterval{From: cur, To: end})
				}
				if c.From > iv.To {
					cur = iv.To + 1
					break
				}
			}
			if c.To >= cur {
				cur = c.To + 1
				if cur > iv.To {
					break
				}
			}
		}
		if cur <= iv.To {
			gaps = append(gaps, BlockInterval{From: cur, To: iv.To})
		}
	}
	return gaps
}

// Intersect 返回两集合交集。
func (s *IntervalSet) Intersect(other *IntervalSet) *IntervalSet {
	out := NewIntervalSet()
	for _, a := range s.items {
		for _, b := range other.Items() {
			lo, hi := a.From, a.To
			if b.From > lo {
				lo = b.From
			}
			if b.To < hi {
				hi = b.To
			}
			if hi >= lo {
				out.Add(lo, hi)
			}
		}
	}
	return out
}

// Contains 判断是否完全包含给定区间。
func (s *IntervalSet) Contains(iv BlockInterval) bool {
	for _, item := range s.items {
		if item.From <= iv.From && item.To >= iv.To {
			return true
		}
	}
	return false
}

// FindGaps 在 [from,to] 中找出未被本集合覆盖的缺口。
func (s *IntervalSet) FindGaps(from, to uint64) []BlockInterval {
	if to < from {
		return nil
	}
	requested := FromIntervals([]BlockInterval{{From: from, To: to}})
	return requested.Subtract(s)
}

// CoverageRatio 覆盖率 = 覆盖块数 / 请求块数。
func (s *IntervalSet) CoverageRatio(from, to uint64) float64 {
	total := to - from + 1
	if total == 0 {
		return 1
	}
	covered := s.Intersect(FromIntervals([]BlockInterval{{From: from, To: to}})).Blocks()
	return float64(covered) / float64(total)
}

func merge(list []BlockInterval) []BlockInterval {
	if len(list) == 0 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].From == list[j].From {
			return list[i].To < list[j].To
		}
		return list[i].From < list[j].From
	})
	out := []BlockInterval{list[0]}
	for _, iv := range list[1:] {
		last := &out[len(out)-1]
		if iv.From <= last.To+1 {
			if iv.To > last.To {
				last.To = iv.To
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}
