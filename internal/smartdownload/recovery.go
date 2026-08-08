package smartdownload

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/etl/backend/internal/logger"
)

// RecoverAll 服务启动恢复（实施方案 §19-§20）：
// 扫描 Active Batches → 回放 Range Ledger → 校验 Parts（SHA256）→ 重建 Checkpoint →
// 未完成 Range 重新入队；绝不像旧调度器那样把所有任务标记 FAILED。
//
// 可信度顺序：Committed Part（磁盘 SHA 校验）> Range Ledger > Checkpoint > Task JSON。
func (s *Service) RecoverAll(ctx context.Context) error {
	batches := s.store.ListBatches()
	recovered := 0
	for _, batch := range batches {
		if batch.Status.Terminal() {
			continue
		}
		if err := s.recoverBatch(ctx, batch.ID); err != nil {
			logger.Log.Warn().Str("batch_id", batch.ID).Err(err).Msg("smartdownload_recover_batch_failed")
			continue
		}
		recovered++
	}
	// 恢复后自动重新调度 RUNNING 批次
	for _, batch := range batches {
		if batch.Status.Terminal() || batch.Status == BatchPaused || batch.Status == BatchCanceled {
			continue
		}
		current := s.store.GetBatch(batch.ID)
		if current != nil && current.Status == BatchRunning {
			_ = s.Start(batch.ID)
		}
	}
	logger.Log.Info().Int("batches", len(batches)).Int("recovered", recovered).Msg("smartdownload_recovery_done")
	return nil
}

func (s *Service) recoverBatch(ctx context.Context, batchID string) error {
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		if a.Status.Terminal() {
			continue
		}
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			if ds.Status.Terminal() {
				continue
			}
			if err := s.recoverDataset(ctx, ds.ID); err != nil {
				logger.Log.Warn().Str("dataset_job", ds.ID).Err(err).Msg("smartdownload_recover_dataset_failed")
				continue
			}
			// 校验中任务在重启后重新触发校验（不标记失败）
			if current := s.store.GetDataset(ds.ID); current != nil && current.Status == DatasetValidating {
				go s.validateDatasetAndFinalize(ds.ID)
			}
		}
		// 地址级状态重算
		s.recomputeAddressStatus(a.ID)
	}
	s.recomputeBatchStatus(batchID)
	return nil
}

// recoverDataset 以 Ledger + 磁盘 Parts 为权威重建 Checkpoint，并修复 Range 状态。
func (s *Service) recoverDataset(ctx context.Context, datasetJobID string) error {
	ds := s.store.GetDataset(datasetJobID)
	if ds == nil {
		return fmt.Errorf("dataset %s 不存在", datasetJobID)
	}
	ledger := NewLedger(s.store.Root(), datasetJobID)
	entries, err := ledger.Replay()
	if err != nil {
		return err
	}
	// 磁盘 Parts 真实 SHA（Part 是最终事实）
	diskParts := map[string]string{}
	partsDir := filepath.Join(s.PartsDir(), datasetJobID)
	if items, err := os.ReadDir(partsDir); err == nil {
		for _, e := range items {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			sha, err := fileSHA256(filepath.Join(partsDir, e.Name()))
			if err == nil {
				diskParts[e.Name()] = sha
			}
		}
	}

	cp, cpErr := s.cp.Load(datasetJobID)
	if cpErr != nil {
		// Checkpoint 丢失：用 Range Job 重建 requested 范围
		ranges := s.store.ListRangesByDataset(datasetJobID)
		if len(ranges) == 0 {
			return nil
		}
		cp = &CheckpointV3{
			DatasetJobID:  datasetJobID,
			Address:       ds.Address,
			Dataset:       ds.Dataset,
			RequestedFrom: ranges[0].FromBlock,
			RequestedTo:   ranges[len(ranges)-1].ToBlock,
		}
		for _, r := range ranges {
			cp.PendingRanges = append(cp.PendingRanges, BlockRange{From: r.FromBlock, To: r.ToBlock})
		}
	}

	// 1) Ledger 中 PART_COMMITTED 的 Part 必须在磁盘上且 SHA 一致才算已提交
	validParts := map[string]PartInfo{}
	for _, e := range entries {
		if e.Event != LedgerPartCommitted || e.Part == "" {
			continue
		}
		diskSHA, ok := diskParts[e.Part]
		if !ok || diskSHA != e.SHA256 {
			logger.Log.Warn().Str("dataset_job", datasetJobID).Str("part", e.Part).
				Msg("smartdownload_recover_part_missing_or_sha_mismatch")
			continue
		}
		validParts[e.Part] = PartInfo{
			Name: e.Part, SHA256: diskSHA, Rows: e.Rows,
			RangeFrom: e.FromBlock, RangeTo: e.ToBlock,
		}
	}

	// 2) 以 Ledger 为准重建 completed/empty
	completed := RangeKeySet{}
	empty := RangeKeySet{}
	for _, r := range cp.CompletedRanges {
		completed.Add(r)
	}
	for _, r := range cp.ConfirmedEmptyRanges {
		empty.Add(r)
	}
	for _, e := range entries {
		r := BlockRange{From: e.FromBlock, To: e.ToBlock}
		switch e.Event {
		case LedgerRangeCompleted:
			// 必须有至少一个有效 Part 才算完成（空 Range 由 RANGE_EMPTY 覆盖）
			if _, ok := validParts[e.Part]; ok || e.Part == "" {
				completed.Add(r)
			}
		case LedgerRangeEmpty:
			empty.Add(r)
		}
	}
	cp.CompletedRanges = rangeSetToList(completed)
	cp.ConfirmedEmptyRanges = rangeSetToList(empty)

	// 3) 重建 Parts（只保留磁盘上可验证的）
	cp.Parts = nil
	cp.RowsCommitted = 0
	keys := make([]string, 0, len(validParts))
	for name := range validParts {
		keys = append(keys, name)
	}
	sortStrings(keys)
	for _, name := range keys {
		p := validParts[name]
		cp.Parts = append(cp.Parts, p)
		cp.RowsCommitted += uint64(p.Rows)
	}

	// 4) 重建 pending = requested - completed - empty
	requested := BlockRange{From: cp.RequestedFrom, To: cp.RequestedTo}
	all := SplitBlockRange(requested.From, requested.To, s.opts.RangeChunkSize)
	if len(all) == 0 && len(cp.PendingRanges) > 0 {
		// 请求范围不可推导时保留旧 pending
		all = cp.PendingRanges
	}
	var pending []BlockRange
	for _, r := range all {
		if !completed.Has(r) && !empty.Has(r) {
			pending = append(pending, r)
		}
	}
	cp.PendingRanges = pending
	cp.UpdatedAt = time.Now().UTC()
	if err := s.cp.Save(cp); err != nil {
		return err
	}

	// 5) 修复 Range Job 状态
	for _, rj := range s.store.ListRangesByDataset(datasetJobID) {
		r := BlockRange{From: rj.FromBlock, To: rj.ToBlock}
		now := time.Now().UTC()
		switch {
		case completed.Has(r):
			if rj.Status != RangeCompleted {
				rj.Status = RangeCompleted
				rj.FinishedAt = &now
				rj.UpdatedAt = now
				for _, p := range cp.Parts {
					if p.RangeFrom == r.From && p.RangeTo == r.To {
						rj.RowsCommitted = uint64(p.Rows)
						rj.Bytes = uint64(p.Bytes)
						break
					}
				}
				_ = s.store.SaveRange(rj)
			}
		case empty.Has(r):
			if rj.Status != RangeEmpty {
				rj.Status = RangeEmpty
				rj.FinishedAt = &now
				rj.UpdatedAt = now
				_ = s.store.SaveRange(rj)
			}
		case rj.Status == RangeRunning:
			rj.Status = RangeReady
			rj.UpdatedAt = now
			_ = s.store.SaveRange(rj)
		case rj.Status == RangeFailed && rj.Attempts <= s.opts.RetryLimit:
			rj.Status = RangeReady
			rj.UpdatedAt = now
			_ = s.store.SaveRange(rj)
		}
	}
	return nil
}

// recomputeAddressStatus 按数据集终态重算地址状态（PAUSED/CANCEL 请求优先）。
func (s *Service) recomputeAddressStatus(addressID string) {
	a := s.store.GetAddress(addressID)
	if a == nil || a.Status.Terminal() {
		return
	}
	if a.Status == AddressPaused || a.Status == AddressDownloading {
		return
	}
	datasets := s.store.ListDatasetsByAddress(addressID)
	allDone, anyFailed, anyPartial, anyCanceled := true, false, false, false
	for _, ds := range datasets {
		if !ds.Status.Terminal() {
			allDone = false
		}
		if ds.Status == DatasetFailed {
			anyFailed = true
		}
		if ds.Status == DatasetPartial {
			anyPartial = true
		}
		if ds.Status == DatasetCanceled {
			anyCanceled = true
		}
	}
	now := time.Now().UTC()
	switch {
	case a.CancelRequested || anyCanceled:
		a.Status = AddressCanceled
		a.FinishedAt = &now
	case a.PauseRequested:
		a.Status = AddressPaused
	case allDone && anyFailed:
		a.Status = AddressFailed
		a.FinishedAt = &now
	case allDone && anyPartial:
		a.Status = AddressPartial
		a.FinishedAt = &now
	case allDone:
		a.Status = AddressCompleted
		a.FinishedAt = &now
	default:
		a.Status = AddressDownloading
	}
	a.UpdatedAt = now
	_ = s.store.SaveAddress(a)
}

// recomputeBatchStatus 按地址终态重算批次状态。
func (s *Service) recomputeBatchStatus(batchID string) {
	batch := s.store.GetBatch(batchID)
	if batch == nil || batch.Status.Terminal() {
		return
	}
	addresses := s.store.ListAddressesByBatch(batchID)
	allDone, anyFailed, anyPartial, anyCanceled, anyPaused := true, false, false, false, false
	for _, a := range addresses {
		if !a.Status.Terminal() {
			allDone = false
		}
		if a.Status == AddressFailed {
			anyFailed = true
		}
		if a.Status == AddressPartial {
			anyPartial = true
		}
		if a.Status == AddressCanceled {
			anyCanceled = true
		}
		if a.Status == AddressPaused {
			anyPaused = true
		}
	}
	now := time.Now().UTC()
	switch {
	case batch.CancelRequested || anyCanceled:
		batch.Status = BatchCanceled
		batch.FinishedAt = &now
	case batch.PauseRequested || anyPaused:
		batch.Status = BatchPaused
	case allDone && anyFailed:
		batch.Status = BatchPartial
		batch.FinishedAt = &now
	case allDone && anyPartial:
		batch.Status = BatchPartial
		batch.FinishedAt = &now
	case allDone:
		batch.Status = BatchCompleted
		batch.FinishedAt = &now
	default:
		batch.Status = BatchRunning
		if batch.StartedAt == nil {
			batch.StartedAt = &now
		}
	}
	batch.UpdatedAt = now
	_ = s.store.SaveBatch(batch)
}

func rangeSetToList(set RangeKeySet) []BlockRange {
	out := make([]BlockRange, 0, len(set))
	for _, r := range set {
		out = append(out, r)
	}
	SortRanges(out)
	return out
}

func sortStrings(list []string) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] < list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
