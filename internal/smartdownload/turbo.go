package smartdownload

import (
	"fmt"
	"strings"
	"time"
)

const (
	defaultTurboTailBlocks uint64 = 500_000
	turboBulkPriority             = 1_000
	turboFastPriority             = 1_010
	turboRepairPriority           = 1_020
)

// SetBatchMode changes only unfinished ownership. Running ranges finish their
// current shard and completed coverage is never scheduled again.
func (s *Service) SetBatchMode(batchID string, mode DownloadMode) (*BatchJob, error) {
	if !mode.Valid() {
		return nil, fmt.Errorf("非法下载模式 %q（仅支持 AUTO/TURBO/EMERGENCY）", mode)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := s.store.GetBatch(batchID)
	if batch == nil {
		return nil, fmt.Errorf("批次不存在: %s", batchID)
	}
	if batch.Status.Terminal() {
		return nil, fmt.Errorf("批次已处于终态 %s，不能切换模式", batch.Status)
	}
	if batch.Mode == mode {
		return batch, nil
	}
	if isTurboMode(mode) {
		if err := s.validateTurboLanesLocked(batchID); err != nil {
			return nil, err
		}
	}
	previous := batch.Mode
	if previous == "" {
		previous = DownloadModeAuto
	}
	now := time.Now().UTC()
	batch.Mode = mode
	if mode == DownloadModeEmergency {
		batch.Priority = PriorityUrgent
	} else if batch.Priority == "" {
		batch.Priority = PriorityNormal
	}
	batch.ModeSwitchedAt = &now
	batch.UpdatedAt = now
	if err := s.store.SaveBatch(batch); err != nil {
		return nil, err
	}
	if err := s.applyModePlanLocked(batchID, mode); err != nil {
		return nil, err
	}
	if isTurboMode(mode) {
		_ = s.rebalanceBatchLocked(batchID, now, true)
	}
	s.preemptLowerPriorityLocked(batch)
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
				Event: LedgerModeSwitched, DatasetJobID: ds.ID,
				Error: "previous=" + string(previous) + ", current=" + string(mode),
			})
		}
	}
	return s.store.GetBatch(batchID), nil
}

func (s *Service) validateTurboLanesLocked(batchID string) error {
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			if ds.Status.Terminal() {
				continue
			}
			rpc, cloud := s.adapters["rpc"], s.adapters["sqd_cloud"]
			rpcOK := adapterAvailableForMode(rpc, ds.ChainKey, DownloadModeTurbo) && rpc.Supports(ds.Dataset)
			cloudOK := adapterAvailableForMode(cloud, ds.ChainKey, DownloadModeTurbo) && cloud.Supports(ds.Dataset)
			if !rpcOK && !cloudOK {
				return fmt.Errorf("Turbo 模式没有可用的 SQD Cloud/RPC lane: dataset=%s", ds.Dataset)
			}
		}
	}
	return nil
}

// applyModePlanLocked reserves each unfinished range for exactly one lane.
// The cutoff is aligned to existing range shards, so Cloud and RPC never own
// the same block interval.
func (s *Service) applyModePlanLocked(batchID string, mode DownloadMode) error {
	batch := s.store.GetBatch(batchID)
	if batch == nil {
		return fmt.Errorf("批次不存在: %s", batchID)
	}
	tailBlocks := s.opts.TurboTailBlocks
	if tailBlocks == 0 {
		tailBlocks = defaultTurboTailBlocks
	}
	cloud := s.adapters["sqd_cloud"]
	rpc := s.adapters["rpc"]
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			rpcAvailable := adapterAvailableForMode(rpc, ds.ChainKey, DownloadModeTurbo)
			cloudAvailable := adapterAvailableForMode(cloud, ds.ChainKey, mode)
			ranges := s.store.ListRangesByDataset(ds.ID)
			var maxBlock uint64
			for _, r := range ranges {
				if r.ToBlock > maxBlock {
					maxBlock = r.ToBlock
				}
			}
			cutoff := uint64(0)
			if maxBlock > tailBlocks {
				cutoff = maxBlock - tailBlocks
			}
			for _, r := range ranges {
				if r.Status != RangePending && r.Status != RangeReady {
					continue
				}
				if mode == DownloadModeAuto {
					r.Owner, r.Lane, r.Priority = "", "", 0
				} else {
					useCloud := cloudAvailable && cloud.Supports(ds.Dataset) && r.ToBlock < cutoff
					switch {
					case useCloud:
						r.Owner, r.Lane = RangeOwnerCloud, "bulk"
						r.Priority = numericRangePriority(r.PriorityClass, RangeOwnerCloud, mode)
					case rpcAvailable && rpc.Supports(ds.Dataset):
						r.Owner, r.Lane = RangeOwnerRPC, "fast"
						r.Priority = numericRangePriority(r.PriorityClass, RangeOwnerRPC, mode)
					case cloudAvailable && cloud.Supports(ds.Dataset):
						r.Owner, r.Lane = RangeOwnerCloud, "bulk"
						r.Priority = numericRangePriority(r.PriorityClass, RangeOwnerCloud, mode)
					default:
						return fmt.Errorf("Turbo 模式没有可用的 SQD Cloud/RPC lane: dataset=%s range=%d-%d", ds.Dataset, r.FromBlock, r.ToBlock)
					}
				}
				r.Provider = ""
				r.FailedProviders = nil
				r.UpdatedAt = time.Now().UTC()
				if err := s.store.SaveRange(r); err != nil {
					return err
				}
				_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{
					Event: LedgerRangeAssigned, DatasetJobID: ds.ID,
					RangeID:   BlockRange{From: r.FromBlock, To: r.ToBlock}.Key(),
					FromBlock: r.FromBlock, ToBlock: r.ToBlock, Owner: string(r.Owner),
				})
			}
		}
	}
	return nil
}

func (s *Service) TurboStatus(batchID string) (*TurboStatus, error) {
	batch := s.store.GetBatch(batchID)
	if batch == nil {
		return nil, fmt.Errorf("批次不存在: %s", batchID)
	}
	status := &TurboStatus{BatchID: batchID, Mode: batch.Mode, Priority: batch.Priority}
	if status.Mode == "" {
		status.Mode = DownloadModeAuto
	}
	if a := s.adapters["sqd_cloud"]; a != nil {
		status.CloudAvailable = a.Available()
	}
	var firstData *time.Time
	var firstRelevant *time.Time
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		status.RowsPerSecond += a.Progress.SpeedRowsPerSec
		if a.Progress.ETASeconds > status.ETASeconds {
			status.ETASeconds = a.Progress.ETASeconds
		}
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			if adapterAvailableForMode(s.adapters["rpc"], ds.ChainKey, batch.Mode) {
				status.RPCAvailable = true
			}
			ranges := s.store.ListRangesByDataset(ds.ID)
			for _, r := range ranges {
				if r.ReshardDepth > 0 {
					status.ReshardActive = true
				}
				if r.HedgeOf != "" && !r.Status.Terminal() {
					status.HedgeActive = true
				}
				if r.HedgeOf != "" && !r.HedgeWinner {
					continue
				}
				switch r.Owner {
				case RangeOwnerCloud:
					status.CloudRanges++
					if r.Status == RangeRunning {
						status.CloudRunning++
					}
				case RangeOwnerRPC:
					status.RPCRanges++
					if r.Status == RangeRunning {
						status.RPCRunning++
					}
				}
				if r.Relevant {
					status.RelevantRanges++
					if r.Certified {
						status.RelevantCertifiedRanges++
						if r.CertifiedAt != nil && (firstRelevant == nil || r.CertifiedAt.Before(*firstRelevant)) {
							v := *r.CertifiedAt
							firstRelevant = &v
						}
					}
				}
				switch r.Status {
				case RangePending, RangeReady:
					status.PendingRanges++
				case RangeRunning:
					status.RunningRanges++
				case RangeCompleted, RangeEmpty:
					status.CompletedRanges++
					if r.FinishedAt != nil && (firstData == nil || r.FinishedAt.Before(*firstData)) {
						v := *r.FinishedAt
						firstData = &v
					}
				case RangeFailed:
					status.FailedRanges++
				}
				if r.FinishedAt != nil && r.StartedAt != nil && r.FinishedAt.After(*r.StartedAt) {
					rate := float64(r.RowsCommitted) / r.FinishedAt.Sub(*r.StartedAt).Seconds()
					if r.Owner == RangeOwnerCloud {
						status.CloudRowsPerSecond += rate
					} else if r.Owner == RangeOwnerRPC {
						status.RPCRowsPerSecond += rate
					}
				}
			}
			status.TotalBlocks += logicalBlockCount(ranges, nil)
			status.CoveredBlocks += logicalBlockCount(ranges, func(r *RangeJob) bool {
				return r.Status == RangeCompleted || r.Status == RangeEmpty
			})
		}
	}
	if status.TotalBlocks > 0 {
		status.CoveragePercent = float64(status.CoveredBlocks) * 100 / float64(status.TotalBlocks)
	}
	if firstData != nil && batch.StartedAt != nil {
		status.TimeToFirstDataSecs = firstData.Sub(*batch.StartedAt).Seconds()
		if status.TimeToFirstDataSecs < 0 {
			status.TimeToFirstDataSecs = 0
		}
	}
	if firstRelevant != nil && batch.StartedAt != nil {
		status.TimeToFirstRelevantSecs = firstRelevant.Sub(*batch.StartedAt).Seconds()
		if status.TimeToFirstRelevantSecs < 0 {
			status.TimeToFirstRelevantSecs = 0
		}
	}
	if status.RelevantRanges > 0 && status.RelevantCertifiedRanges == status.RelevantRanges {
		status.RelevantCertification = string(CertificationRange)
	} else if status.RelevantCertifiedRanges > 0 {
		status.RelevantCertification = string(CertificationDatasetPartial)
	}
	s.mu.Lock()
	state := s.v31StateLocked(batchID)
	status.DownloadedRowsPerSecond = state.Pipeline.DownloadedRowsPerSecond
	status.ParsedRowsPerSecond = state.Pipeline.ParsedRowsPerSecond
	status.InsertedRowsPerSecond = state.Pipeline.InsertedRowsPerSecond
	status.ClickHouseInsertP95Millis = state.Pipeline.InsertP95Millis
	status.ClickHouseMergeQueue = state.Pipeline.MergeQueue
	status.ClickHouseActiveParts = state.Pipeline.ActiveParts
	status.Bottleneck = state.Governor.Bottleneck
	status.ClaimsLimit = state.Governor.ClaimsLimit
	status.RPCClaimsLimit = state.Governor.RPCClaimsLimit
	status.CloudClaimsLimit = state.Burst.Jobs
	status.CloudBurstLevel = string(state.Burst.Level)
	status.CloudBurstJobs = state.Burst.Jobs
	status.CloudPausedByGovernor = state.Governor.PauseNewCloud
	status.BurstActive = state.Burst.Jobs > 1
	status.BackpressureActive = state.Governor.PauseNewCloud || state.Governor.Bottleneck == "CLICKHOUSE" || state.Governor.Bottleneck == "PARSER" || state.Governor.Bottleneck == "DISK"
	status.WorkStealingActive = isTurboMode(batch.Mode) && status.CloudRanges > 0 && status.RPCRanges > 0
	for _, other := range s.store.ListBatches() {
		if other.PausedByPriority && other.PreemptedBy == batchID {
			status.PreemptionActive = true
			break
		}
	}
	if !state.LastAllocation.IsZero() {
		v := state.LastAllocation
		status.AllocatorLastRunAt = &v
	}
	s.mu.Unlock()
	return status, nil
}

func rangeBlockCount(r *RangeJob) uint64 {
	if r == nil || r.ToBlock < r.FromBlock {
		return 0
	}
	return r.ToBlock - r.FromBlock + 1
}

func ownerProvider(owner RangeOwner) string {
	switch owner {
	case RangeOwnerCloud:
		return "sqd_cloud"
	case RangeOwnerRPC:
		return "rpc"
	default:
		return strings.ToLower(string(owner))
	}
}

func adapterAvailableForChain(adapter ProviderAdapter, chainKey string) bool {
	if adapter == nil || !adapter.Available() {
		return false
	}
	if scoped, ok := adapter.(interface{ AvailableForChain(string) bool }); ok {
		return scoped.AvailableForChain(chainKey)
	}
	return true
}

func adapterAvailableForMode(adapter ProviderAdapter, chainKey string, mode DownloadMode) bool {
	if adapter == nil || !adapter.Available() {
		return false
	}
	if scoped, ok := adapter.(interface {
		AvailableForMode(string, DownloadMode) bool
	}); ok {
		return scoped.AvailableForMode(chainKey, mode)
	}
	return adapterAvailableForChain(adapter, chainKey)
}
