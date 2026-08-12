package smartdownload

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/etl/backend/internal/logger"
	reg "github.com/etl/backend/internal/smartdownload/registry"
	"github.com/google/uuid"
)

// RangeCoverageSource 本地 (地址 × 数据集 × 区间) 覆盖查询源。
// 返回已覆盖的区块区间（可多段、可重叠）；调用方负责与请求区间求差。
type RangeCoverageSource interface {
	CoveredRanges(ctx context.Context, chainKey, address, dataset string, from, to uint64) ([]BlockRange, error)
}

// SetRangeCoverageSource 注入区间覆盖源（API 层组合：本服务 Registry + Cloud Dataset Registry）。
func (s *Service) SetRangeCoverageSource(src RangeCoverageSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rangeCoverage = src
}

// registryCoverage 默认区间覆盖源：本服务 Result Registry 中 VALIDATED 的条目。
func (s *Service) registryCoverage(chainKey, address, dataset string, from, to uint64) []BlockRange {
	var out []BlockRange
	for _, e := range s.results.List() {
		if e.ChainKey != chainKey || e.Dataset != dataset || !s.results.isReusableIndexedResult(e) {
			continue
		}
		if !strings.EqualFold(e.Address, address) {
			continue
		}
		if e.ToBlock < from || e.FromBlock > to {
			continue
		}
		lo := e.FromBlock
		if from > lo {
			lo = from
		}
		hi := e.ToBlock
		if to < hi {
			hi = to
		}
		out = append(out, BlockRange{From: lo, To: hi})
	}
	return out
}

// RegistryCoverage 导出的本服务 Registry 区间覆盖（API 层组合源使用）。
func (s *Service) RegistryCoverage(chainKey, address, dataset string, from, to uint64) []BlockRange {
	return s.registryCoverage(chainKey, address, dataset, from, to)
}

// coveredRangesFor 获取请求区间内的已覆盖区间（注入源优先，回退本服务 Registry）。
func (s *Service) coveredRangesFor(ctx context.Context, chainKey, address, dataset string, from, to uint64) []BlockRange {
	s.mu.Lock()
	src := s.rangeCoverage
	index := s.coverageIndex
	s.mu.Unlock()
	// Coverage Index V2 优先（分片 + 热缓存 + 兼容性 + 快照 TTL）
	if index != nil {
		res := index.Resolve(chainKey, address, dataset, from, to, time.Now())
		if res.Compatible && len(res.Covered) > 0 {
			// Once this scope has Smart Download result versions, validate the
			// derived coverage index against the authoritative result registry.
			// This prevents legacy zero-row certifications from surviving forever
			// in the append-only coverage shards.
			if s.results.HasAnyForRange(chainKey, address, dataset, from, to) {
				if trusted := s.registryCoverage(chainKey, address, dataset, from, to); len(trusted) > 0 {
					return trusted
				}
			} else {
				out := make([]BlockRange, 0, len(res.Covered))
				for _, iv := range res.Covered {
					out = append(out, BlockRange{From: iv.From, To: iv.To})
				}
				return out
			}
		}
	}
	if src != nil {
		if ranges, err := src.CoveredRanges(ctx, chainKey, address, dataset, from, to); err == nil {
			return ranges
		}
	}
	return s.registryCoverage(chainKey, address, dataset, from, to)
}

// CoverageQuery 覆盖查询（API /coverage/query）。
func (s *Service) CoverageQuery(chainKey, address, dataset string, from, to uint64) reg.CoverageResult {
	if s.coverageIndex == nil {
		return reg.CoverageResult{Missing: []reg.Interval{{From: from, To: to}}}
	}
	return s.coverageIndex.Resolve(chainKey, address, dataset, from, to, time.Now())
}

// planReuse 计算复用计划：请求区间 ∩ 已覆盖 → reused；请求区间 − 已覆盖 → missing（精确缺口）。
func planReuse(requested BlockRange, covered []BlockRange) (reused, missing []BlockRange) {
	for _, c := range covered {
		lo, hi := c.From, c.To
		if requested.From > lo {
			lo = requested.From
		}
		if requested.To < hi {
			hi = requested.To
		}
		if hi >= lo {
			reused = append(reused, BlockRange{From: lo, To: hi})
		}
	}
	reused = mergeIntervals(reused)
	missing = subtractCovered(requested, reused)
	return reused, missing
}

// mergeIntervals 合并重叠/相邻区间（升序）。
func mergeIntervals(list []BlockRange) []BlockRange {
	if len(list) == 0 {
		return nil
	}
	sorted := append([]BlockRange(nil), list...)
	SortRanges(sorted)
	out := []BlockRange{sorted[0]}
	for _, r := range sorted[1:] {
		last := &out[len(out)-1]
		if r.From <= last.To+1 {
			if r.To > last.To {
				last.To = r.To
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// subtractCovered 请求区间减去已覆盖区间，返回精确缺口。
func subtractCovered(requested BlockRange, covered []BlockRange) []BlockRange {
	var gaps []BlockRange
	cur := requested.From
	for _, c := range covered {
		if c.To < cur {
			continue
		}
		if c.From > cur {
			end := c.From - 1
			if end > requested.To {
				end = requested.To
			}
			if end >= cur {
				gaps = append(gaps, BlockRange{From: cur, To: end})
			}
			if c.From > requested.To {
				cur = requested.To + 1
				break
			}
		}
		if c.To >= cur {
			cur = c.To + 1
			if cur > requested.To {
				break
			}
		}
	}
	if cur <= requested.To {
		gaps = append(gaps, BlockRange{From: cur, To: requested.To})
	}
	return gaps
}

// markDatasetReused 全量复用：全部 Range 标记 local_hit 完成，不触发下载。
func (s *Service) markDatasetReused(dsID string, requested BlockRange, ranges []BlockRange, localRows int64) error {
	ds := s.store.GetDataset(dsID)
	if ds == nil {
		return fmt.Errorf("dataset %s 不存在", dsID)
	}
	if len(ranges) == 0 {
		return fmt.Errorf("复用区间为空")
	}
	now := time.Now().UTC()
	cp := &CheckpointV3{}
	cp.Init(dsID, ds.Address, ds.Dataset, requested, s.opts.RangeChunkSize)
	cp.PendingRanges = nil
	for _, r := range ranges {
		cp.CompleteRange(r, nil)
	}
	if err := s.cp.Save(cp); err != nil {
		return err
	}
	ledger := NewLedger(s.store.Root(), dsID)
	for _, r := range ranges {
		rj := &RangeJob{
			ID: uuid.NewString(), DatasetJobID: dsID, BatchID: ds.BatchID,
			AddressJobID: ds.AddressJobID, Address: ds.Address, Dataset: ds.Dataset,
			FromBlock: r.From, ToBlock: r.To, Provider: "local_hit",
			Status: RangeCompleted, CreatedAt: now, UpdatedAt: now,
		}
		fin := now
		rj.FinishedAt = &fin
		if err := s.store.SaveRange(rj); err != nil {
			return err
		}
		_ = ledger.Append(LedgerEntry{
			Event: "RANGE_REUSED", DatasetJobID: dsID, RangeID: r.Key(),
			FromBlock: r.From, ToBlock: r.To, Provider: "local_hit",
			Error: fmt.Sprintf("LOCAL_HIT rows=%d", localRows),
		})
	}
	ds.Status = DatasetCompleted
	ds.CurrentProvider = "local_hit"
	ds.EstimatedRows = uint64(localRows)
	ds.DownloadedRows = uint64(localRows)
	ds.ValidatedRows = uint64(localRows)
	blocks := requested.To - requested.From + 1
	ds.Progress = ProgressSnapshot{Percent: 1, RowsCurrent: uint64(localRows), RowsTotal: uint64(localRows),
		BlocksCurrent: blocks, BlocksTotal: blocks}
	ds.Validation = &ValidationReport{DatasetJobID: dsID, Status: "VALIDATED", Score: 1,
		Coverage: 1, BlockCoverage: 1, Rows: localRows, RawRows: localRows,
		UniqueKeyCount: localRows, ActualCount: localRows, ExpectedCount: localRows,
		LevelFile: true, LevelRecord: true, LevelCoverage: true, LevelProviderCnt: true, LevelCrossCheck: true,
		CrossCheck: CrossCheckReport{Status: "PASS"}, ValidatedAt: now}
	ds.Certification = CertificationDataset
	ds.RelevantCertified = true
	ds.RelevantCertifiedAt = &now
	ds.FinishedAt = &now
	ds.UpdatedAt = now
	if err := s.store.SaveDataset(ds); err != nil {
		return err
	}
	_ = ledger.Append(LedgerEntry{Event: LedgerDatasetCertified, DatasetJobID: dsID,
		Provider: "local_hit", Error: fmt.Sprintf("authoritative_local_reuse rows=%d", localRows)})
	s.finalizeAddressIfDoneLocked(ds.AddressJobID)
	logger.Log.Info().Str("dataset_job", dsID).Int("ranges", len(ranges)).
		Int64("rows", localRows).Msg("smartdownload_local_hit_full_reuse")
	return nil
}

// createReuseDataset 部分复用：已覆盖 chunk 标记完成，缺失 chunk 建 RangeJob 补下载。
func (s *Service) createReuseDataset(dsID string, dsJob *DatasetJob, addrID, batchID, addr, dataset string,
	requested BlockRange, reused, missing []BlockRange, now time.Time) error {
	cp := &CheckpointV3{}
	cp.Init(dsID, addr, dataset, requested, s.opts.RangeChunkSize)
	cp.PendingRanges = nil
	ledger := NewLedger(s.store.Root(), dsID)
	for _, r := range reused {
		cp.CompleteRange(r, nil)
		rj := &RangeJob{
			ID: uuid.NewString(), DatasetJobID: dsID, BatchID: batchID,
			AddressJobID: addrID, Address: addr, Dataset: dataset,
			FromBlock: r.From, ToBlock: r.To, Provider: "local_hit",
			Status: RangeCompleted, CreatedAt: now, UpdatedAt: now,
		}
		fin := now
		rj.FinishedAt = &fin
		if err := s.store.SaveRange(rj); err != nil {
			return err
		}
		_ = ledger.Append(LedgerEntry{
			Event: "RANGE_REUSED", DatasetJobID: dsID, RangeID: r.Key(),
			FromBlock: r.From, ToBlock: r.To, Provider: "local_hit",
		})
	}
	for _, r := range missing {
		cp.PendingRanges = append(cp.PendingRanges, r)
		rj := &RangeJob{
			ID: uuid.NewString(), DatasetJobID: dsID, BatchID: batchID,
			AddressJobID: addrID, Address: addr, Dataset: dataset,
			FromBlock: r.From, ToBlock: r.To, Status: RangePending,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := s.store.SaveRange(rj); err != nil {
			return err
		}
		_ = ledger.Append(LedgerEntry{
			Event: LedgerRangeCreated, DatasetJobID: dsID, RangeID: r.Key(),
			FromBlock: r.From, ToBlock: r.To,
		})
	}
	if err := s.cp.Save(cp); err != nil {
		return err
	}
	logger.Log.Info().Str("dataset_job", dsID).Int("reused", len(reused)).
		Int("missing", len(missing)).Msg("smartdownload_local_hit_partial_reuse")
	return nil
}
