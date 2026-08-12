package smartdownload

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RPCEndpointMetrics is the narrow Smart Download view of rpcmanager.PoolSnapshot.
// The API layer may adapt the concrete rpcmanager type without coupling this
// package to endpoint implementation details.
type RPCEndpointMetrics struct {
	Name              string   `json:"name"`
	LatencyMillis     float64  `json:"latency_ms"`
	SuccessRate       float64  `json:"success_rate"`
	Rate429           float64  `json:"rate_429"`
	TimeoutRate       float64  `json:"timeout_rate"`
	CurrentWorkers    int      `json:"current_workers"`
	SupportedMethods  []string `json:"supported_methods,omitempty"`
	ArchiveCapability bool     `json:"archive_capability,omitempty"`
	TraceCapability   bool     `json:"trace_capability,omitempty"`
}

type RPCPoolMetrics struct {
	Endpoints []RPCEndpointMetrics `json:"endpoints"`
}

// RPCPoolMetricsSource intentionally uses a Smart Download DTO. A future
// rpcmanager.PoolSnapshot adapter only has to copy fields at the composition edge.
type RPCPoolMetricsSource interface {
	SmartDownloadRPCPoolSnapshot(chainKey string) (RPCPoolMetrics, error)
}

// ThroughputSnapshot is the three-stage pipeline view consumed by the governor.
type ThroughputSnapshot struct {
	DownloadedRowsPerSecond float64 `json:"downloaded_rows_per_second"`
	ParsedRowsPerSecond     float64 `json:"parsed_rows_per_second"`
	InsertedRowsPerSecond   float64 `json:"inserted_rows_per_second"`
	InsertP95Millis         float64 `json:"insert_p95_ms"`
	MergeQueue              int     `json:"merge_queue"`
	ActiveParts             int     `json:"active_parts"`
	DiskIOPercent           float64 `json:"disk_io_percent"`
	CPUPercent              float64 `json:"cpu_percent"`
	FreeDiskBytes           uint64  `json:"free_disk_bytes"`
	CloudBudgetRemaining    float64 `json:"cloud_budget_remaining,omitempty"`
}

type ThroughputMetricsSource interface {
	SmartDownloadThroughput(batchID string) ThroughputSnapshot
}

type GovernorDecision struct {
	Bottleneck     string `json:"bottleneck"`
	ClaimsLimit    int    `json:"claims_limit"`
	RPCClaimsLimit int    `json:"rpc_claims_limit"`
	PauseNewCloud  bool   `json:"pause_new_cloud"`
	Reason         string `json:"reason,omitempty"`
}

// ThroughputGovernor bounds acquisition by the slowest pipeline stage.
type ThroughputGovernor struct{}

func (ThroughputGovernor) Observe(m ThroughputSnapshot, workers, rpcHardLimit int) GovernorDecision {
	if workers < 1 {
		workers = 1
	}
	if rpcHardLimit < 1 {
		rpcHardLimit = workers
	}
	d := GovernorDecision{Bottleneck: "NETWORK", ClaimsLimit: workers, RPCClaimsLimit: rpcHardLimit}
	if m.FreeDiskBytes > 0 && m.FreeDiskBytes < 2<<30 {
		d.Bottleneck, d.ClaimsLimit, d.RPCClaimsLimit, d.PauseNewCloud = "DISK", 1, 1, true
		d.Reason = "free disk guard"
		return d
	}
	if m.CloudBudgetRemaining < 0 {
		d.PauseNewCloud = true
		d.Reason = "cloud hard budget exhausted"
	}
	if m.MergeQueue >= 20 || m.ActiveParts >= 500 || m.InsertP95Millis >= 5_000 || m.DiskIOPercent >= 95 {
		d.Bottleneck, d.ClaimsLimit, d.RPCClaimsLimit, d.PauseNewCloud = "CLICKHOUSE", 1, 1, true
		d.Reason = "clickhouse hard guard"
		return d
	}
	if m.InsertedRowsPerSecond > 0 && m.DownloadedRowsPerSecond > m.InsertedRowsPerSecond*1.10 {
		d.Bottleneck = "CLICKHOUSE"
		d.ClaimsLimit = maxInt(1, workers/2)
		d.RPCClaimsLimit = maxInt(1, minInt(rpcHardLimit, d.ClaimsLimit))
		if m.DownloadedRowsPerSecond > m.InsertedRowsPerSecond*1.5 || m.MergeQueue >= 8 {
			d.PauseNewCloud = true
		}
		d.Reason = "ingest slower than download"
		return d
	}
	if m.ParsedRowsPerSecond > 0 && m.DownloadedRowsPerSecond > m.ParsedRowsPerSecond*1.15 {
		d.Bottleneck = "PARSER"
		d.ClaimsLimit = maxInt(1, workers/2)
		d.RPCClaimsLimit = maxInt(1, minInt(rpcHardLimit, d.ClaimsLimit))
		d.Reason = "parser slower than download"
	}
	return d
}

type CloudBurstLevel string

const (
	CloudBurstL1 CloudBurstLevel = "L1_NORMAL_TURBO"
	CloudBurstL2 CloudBurstLevel = "L2_HIGH"
	CloudBurstL3 CloudBurstLevel = "L3_EMERGENCY"
)

type CloudBurstDecision struct {
	Level CloudBurstLevel `json:"level"`
	Jobs  int             `json:"jobs"`
}

type batchV31State struct {
	LastAllocation time.Time
	Pipeline       ThroughputSnapshot
	Governor       GovernorDecision
	Burst          CloudBurstDecision
}

type v31Runtime struct {
	interval         time.Duration
	cloudBurstMax    int
	rpcHardClaims    int
	targetRows       uint64
	states           map[string]*batchV31State
	rpcSource        RPCPoolMetricsSource
	throughputSource ThroughputMetricsSource
}

func newV31Runtime(opts Options) *v31Runtime {
	return &v31Runtime{interval: opts.AllocatorInterval, cloudBurstMax: opts.CloudBurstMaxJobs,
		rpcHardClaims: opts.RPCHardClaims, targetRows: opts.TargetRowsPerShard,
		states: map[string]*batchV31State{}}
}

func (s *Service) SetRPCPoolMetricsSource(source RPCPoolMetricsSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.v31.rpcSource = source
}

func (s *Service) SetThroughputMetricsSource(source ThroughputMetricsSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.v31.throughputSource = source
}

func (s *Service) UpdateThroughput(batchID string, snapshot ThroughputSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.v31StateLocked(batchID)
	state.Pipeline = snapshot
}

func (s *Service) v31StateLocked(batchID string) *batchV31State {
	state := s.v31.states[batchID]
	if state == nil {
		state = &batchV31State{}
		s.v31.states[batchID] = state
	}
	return state
}

func isTurboMode(mode DownloadMode) bool {
	return mode == DownloadModeTurbo || mode == DownloadModeEmergency
}

func jobPriorityRank(p JobPriority) int {
	switch p {
	case PriorityUrgent:
		return 4
	case PriorityHigh:
		return 3
	case PriorityNormal:
		return 2
	case PriorityBackground:
		return 1
	default:
		return 2
	}
}

func rangePriorityRank(p RangePriority) int {
	switch p {
	case RangePriorityP0:
		return 5
	case RangePriorityP1:
		return 4
	case RangePriorityP2:
		return 3
	case RangePriorityP3:
		return 2
	default:
		return 1
	}
}

func numericRangePriority(p RangePriority, owner RangeOwner, mode DownloadMode) int {
	base := turboBulkPriority + rangePriorityRank(p)*100
	if owner == RangeOwnerRPC {
		base += 10
	}
	if mode == DownloadModeEmergency {
		base += 1_000
	}
	return base
}

func rangeIsRelevant(req CreateBatchRequest, address string, r BlockRange) bool {
	ranges := req.RelevantRanges
	if req.RelevantRange != nil {
		ranges = append(ranges, *req.RelevantRange)
	}
	if v := req.RelevantByAddress[strings.ToLower(address)]; len(v) > 0 {
		ranges = v
	}
	for _, spec := range ranges {
		if spec.ToBlock >= r.From && spec.FromBlock <= r.To {
			return true
		}
	}
	return false
}

func initialRangePriority(req CreateBatchRequest, address string, r, requested BlockRange, batchPriority JobPriority) RangePriority {
	if rangeIsRelevant(req, address, r) {
		if batchPriority == PriorityUrgent {
			return RangePriorityP0
		}
		return RangePriorityP1
	}
	if r.To == requested.To {
		return RangePriorityP3
	}
	return RangePriorityP4
}

// PlanDensityAwareShards targets approximately equal expected row counts.
// Dense segments get smaller block spans and sparse segments get larger spans.
func PlanDensityAwareShards(from, to uint64, rowsPerBlock float64, targetRows uint64) []BlockRange {
	if from > to || targetRows == 0 {
		return nil
	}
	if rowsPerBlock <= 0 {
		return []BlockRange{{From: from, To: to}}
	}
	span := uint64(math.Round(float64(targetRows) / rowsPerBlock))
	if span < 1 {
		span = 1
	}
	return SplitBlockRange(from, to, span)
}

// RebalanceBatch executes one deterministic allocator pass. Production calls
// it at the configured 10-30 second cadence; tests and operators may invoke it.
func (s *Service) RebalanceBatch(batchID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rebalanceBatchLocked(batchID, time.Now().UTC(), true)
}

func (s *Service) rebalanceBatchLocked(batchID string, now time.Time, force bool) error {
	batch := s.store.GetBatch(batchID)
	if batch == nil {
		return fmt.Errorf("批次不存在: %s", batchID)
	}
	if !isTurboMode(batch.Mode) || batch.Status.Terminal() {
		return nil
	}
	state := s.v31StateLocked(batchID)
	if !force && !state.LastAllocation.IsZero() && now.Sub(state.LastAllocation) < s.v31.interval {
		return nil
	}
	if s.v31.throughputSource != nil {
		state.Pipeline = s.v31.throughputSource.SmartDownloadThroughput(batchID)
	}
	observedRate := s.batchRowsPerSecondLocked(batchID)
	if observedRate > 0 {
		state.Pipeline.DownloadedRowsPerSecond = observedRate
		if state.Pipeline.ParsedRowsPerSecond <= 0 {
			state.Pipeline.ParsedRowsPerSecond = observedRate
		}
	}
	state.Governor = (ThroughputGovernor{}).Observe(state.Pipeline, s.opts.Workers, s.v31.rpcHardClaims)
	profile := s.profileConfigForBatch(batchID)
	state.Governor.ClaimsLimit = minInt(state.Governor.ClaimsLimit, profile.Workers)
	state.Governor.RPCClaimsLimit = minInt(state.Governor.RPCClaimsLimit, profile.RPCWorkers)
	state.Burst = s.cloudBurstDecisionLocked(batch, state.Governor)
	rpcClaims := state.Governor.RPCClaimsLimit
	if s.v31.rpcSource != nil {
		if pool, err := s.v31.rpcSource.SmartDownloadRPCPoolSnapshot(batch.ChainKey); err == nil {
			rpcClaims = minInt(rpcClaims, healthyRPCWorkers(pool))
		}
	}
	if rpcClaims < 1 {
		rpcClaims = 1
	}
	state.Governor.RPCClaimsLimit = rpcClaims
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			s.reshardDensePendingLocked(ds, state)
			s.allocateDatasetPendingLocked(batch, ds, state)
			s.createDueHedgesLocked(batch, ds, now)
		}
	}
	state.LastAllocation = now
	return nil
}

func (s *Service) batchRowsPerSecondLocked(batchID string) float64 {
	rate := 0.0
	for _, address := range s.store.ListAddressesByBatch(batchID) {
		if address.Progress.SpeedRowsPerSec > 0 {
			rate += address.Progress.SpeedRowsPerSec
		}
	}
	return rate
}

func healthyRPCWorkers(pool RPCPoolMetrics) int {
	total := 0
	for _, ep := range pool.Endpoints {
		if ep.Rate429 >= .5 || ep.TimeoutRate >= .5 || ep.SuccessRate > 0 && ep.SuccessRate < .5 {
			continue
		}
		total += maxInt(0, ep.CurrentWorkers)
	}
	return total
}

func (s *Service) cloudBurstDecisionLocked(batch *BatchJob, governor GovernorDecision) CloudBurstDecision {
	decision := CloudBurstDecision{Level: CloudBurstL1, Jobs: 1}
	if batch.Priority == PriorityHigh || batch.Mode == DownloadModeTurbo {
		decision.Level, decision.Jobs = CloudBurstL2, 2
	}
	if batch.Priority == PriorityUrgent || batch.Mode == DownloadModeEmergency {
		decision.Level, decision.Jobs = CloudBurstL3, s.v31.cloudBurstMax
	}
	decision.Jobs = minInt(decision.Jobs, s.v31.cloudBurstMax)
	decision.Jobs = minInt(decision.Jobs, s.profileConfigForBatch(batch.ID).CloudJobs)
	if governor.PauseNewCloud {
		decision.Jobs = 0
	} else if governor.ClaimsLimit > 0 {
		decision.Jobs = minInt(decision.Jobs, governor.ClaimsLimit)
	}
	return decision
}

func (s *Service) allocateDatasetPendingLocked(batch *BatchJob, ds *DatasetJob, state *batchV31State) {
	ranges := s.store.ListRangesByDataset(ds.ID)
	pending := make([]*RangeJob, 0, len(ranges))
	for _, r := range ranges {
		if r.Status == RangePending || r.Status == RangeReady {
			pending = append(pending, r)
		}
	}
	sort.SliceStable(pending, func(i, j int) bool {
		if rangePriorityRank(pending[i].PriorityClass) == rangePriorityRank(pending[j].PriorityClass) {
			return rangeBlockCount(pending[i]) < rangeBlockCount(pending[j])
		}
		return rangePriorityRank(pending[i].PriorityClass) > rangePriorityRank(pending[j].PriorityClass)
	})
	cloudOK := !state.Governor.PauseNewCloud && state.Burst.Jobs > 0 && adapterAvailableForMode(s.adapters["sqd_cloud"], ds.ChainKey, batch.Mode) && s.adapters["sqd_cloud"].Supports(ds.Dataset)
	rpcOK := adapterAvailableForMode(s.adapters["rpc"], ds.ChainKey, batch.Mode) && s.adapters["rpc"].Supports(ds.Dataset)
	if !cloudOK && !rpcOK {
		return
	}
	cloudWeight, rpcWeight := float64(maxInt(1, state.Burst.Jobs)), float64(maxInt(1, state.Governor.RPCClaimsLimit))
	if state.Pipeline.InsertedRowsPerSecond > 0 {
		cloudWeight = math.Min(cloudWeight, float64(maxInt(1, state.Governor.ClaimsLimit)))
	}
	cloudAssigned, rpcAssigned := 0.0, 0.0
	for _, r := range pending {
		owner := RangeOwnerCloud
		switch {
		case !cloudOK:
			owner = RangeOwnerRPC
		case !rpcOK:
			owner = RangeOwnerCloud
		case r.PriorityClass == RangePriorityP0 || r.PriorityClass == RangePriorityP1 || r.PriorityClass == RangePriorityP3:
			owner = RangeOwnerRPC
		case rpcAssigned/rpcWeight <= cloudAssigned/cloudWeight:
			owner = RangeOwnerRPC // pending work stealing into an idle RPC lane
		}
		if owner == RangeOwnerRPC {
			rpcAssigned++
			r.Lane = "fast"
		} else {
			cloudAssigned++
			r.Lane = "bulk"
		}
		if r.Owner != owner {
			r.Owner = owner
			r.Provider = ""
			r.UpdatedAt = time.Now().UTC()
		}
		r.Priority = numericRangePriority(r.PriorityClass, owner, batch.Mode)
		_ = s.store.SaveRange(r)
	}
}

func (s *Service) reshardDensePendingLocked(ds *DatasetJob, state *batchV31State) {
	target := s.v31.targetRows
	if state.Governor.Bottleneck == "CLICKHOUSE" || state.Governor.Bottleneck == "PARSER" {
		target = 50_000
	} else if state.Pipeline.InsertedRowsPerSecond > 200_000 {
		target = 250_000
	}
	if target == 0 {
		return
	}
	ranges := s.store.ListRangesByDataset(ds.ID)
	total := totalBlocks(ranges)
	for _, r := range ranges {
		if (r.Status != RangePending && r.Status != RangeReady) || r.HedgeOf != "" || rangeBlockCount(r) < 2 || r.ReshardDepth >= 3 {
			continue
		}
		expected := r.ExpectedRows
		if expected == 0 && ds.EstimatedRows > 0 && total > 0 {
			expected = uint64(float64(ds.EstimatedRows) * float64(rangeBlockCount(r)) / float64(total))
		}
		if expected <= target*3/2 {
			continue
		}
		parts := int((expected + target - 1) / target)
		parts = minInt(parts, 8)
		span := (rangeBlockCount(r) + uint64(parts) - 1) / uint64(parts)
		shards := SplitBlockRange(r.FromBlock, r.ToBlock, span)
		if len(shards) < 2 {
			continue
		}
		original := BlockRange{From: r.FromBlock, To: r.ToBlock}
		r.FromBlock, r.ToBlock = shards[0].From, shards[0].To
		r.ExpectedRows = expected / uint64(len(shards))
		r.ParentRangeID = r.ID
		r.ReshardDepth++
		_ = s.store.SaveRange(r)
		for _, shard := range shards[1:] {
			child := *r
			child.ID = uuid.NewString()
			child.FromBlock, child.ToBlock = shard.From, shard.To
			child.CreatedAt, child.UpdatedAt = time.Now().UTC(), time.Now().UTC()
			_ = s.store.SaveRange(&child)
		}
		if cp := s.checkpointLocked(ds.ID); cp != nil {
			cp.PendingRanges = replacePendingRange(cp.PendingRanges, original, shards)
			_ = s.cp.Save(cp)
		}
		_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{Event: LedgerRangeResharded,
			DatasetJobID: ds.ID, RangeID: original.Key(), FromBlock: original.From, ToBlock: original.To,
			Error: fmt.Sprintf("density expected_rows=%d target_rows=%d shards=%d", expected, target, len(shards))})
	}
}

func replacePendingRange(in []BlockRange, old BlockRange, shards []BlockRange) []BlockRange {
	out := make([]BlockRange, 0, len(in)+len(shards)-1)
	for _, r := range in {
		if r.From == old.From && r.To == old.To {
			out = append(out, shards...)
		} else {
			out = append(out, r)
		}
	}
	return out
}

func (s *Service) createDueHedgesLocked(batch *BatchJob, ds *DatasetJob, now time.Time) {
	if !isTurboMode(batch.Mode) {
		return
	}
	ranges := s.store.ListRangesByDataset(ds.ID)
	for _, r := range ranges {
		if r.Status != RangeRunning || r.HedgeOf != "" || (r.PriorityClass != RangePriorityP0 && r.PriorityClass != RangePriorityP1) || r.StartedAt == nil || r.ETASeconds <= 0 {
			continue
		}
		if now.Sub(*r.StartedAt).Seconds() <= r.ETASeconds*2 || hasHedge(ranges, r.ID) {
			continue
		}
		other := RangeOwnerRPC
		if r.Owner == RangeOwnerRPC {
			other = RangeOwnerCloud
		}
		provider := s.adapters[ownerProvider(other)]
		if !adapterAvailableForMode(provider, ds.ChainKey, batch.Mode) || !provider.Supports(ds.Dataset) {
			continue
		}
		h := *r
		h.ID = uuid.NewString()
		h.Owner = other
		h.Lane = "hedge"
		h.Provider = ""
		h.Status = RangeReady
		h.HedgeOf = r.ID
		h.HedgeWinner = false
		h.StartedAt, h.FinishedAt = nil, nil
		h.RowsCommitted, h.Bytes, h.Attempts = 0, 0, 0
		h.Priority = numericRangePriority(h.PriorityClass, other, batch.Mode) + 50
		h.CreatedAt, h.UpdatedAt = now, now
		_ = s.store.SaveRange(&h)
		_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{Event: LedgerHedgeStarted,
			DatasetJobID: ds.ID, RangeID: BlockRange{From: r.FromBlock, To: r.ToBlock}.Key(),
			FromBlock: r.FromBlock, ToBlock: r.ToBlock, Owner: string(other)})
	}
}

func hasHedge(ranges []*RangeJob, originalID string) bool {
	for _, r := range ranges {
		if r.HedgeOf == originalID {
			return true
		}
	}
	return false
}

func (s *Service) canClaimLaneLocked(batchID string, owner RangeOwner) bool {
	state := s.v31StateLocked(batchID)
	limit := state.Governor.ClaimsLimit
	if limit <= 0 {
		limit = s.opts.Workers
	}
	laneLimit := limit
	if owner == RangeOwnerCloud {
		laneLimit = state.Burst.Jobs
		if laneLimit == 0 && state.Governor.PauseNewCloud {
			return false
		}
		if laneLimit <= 0 {
			laneLimit = 1
		}
	} else if owner == RangeOwnerRPC && state.Governor.RPCClaimsLimit > 0 {
		laneLimit = state.Governor.RPCClaimsLimit
	}
	totalRunning, laneRunning := 0, 0
	for _, r := range s.store.ListRanges() {
		if r.BatchID == batchID && r.Status == RangeRunning {
			totalRunning++
			if r.Owner == owner {
				laneRunning++
			}
		}
	}
	return totalRunning < limit && laneRunning < laneLimit
}

func (s *Service) sortAddressesByPriority(batchID string, addresses []*AddressJob) {
	best := func(a *AddressJob) int {
		v := 0
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			for _, r := range s.store.ListRangesByDataset(ds.ID) {
				if r.Status == RangePending || r.Status == RangeReady {
					v = maxInt(v, rangePriorityRank(r.PriorityClass))
				}
			}
		}
		return v
	}
	sort.SliceStable(addresses, func(i, j int) bool { return best(addresses[i]) > best(addresses[j]) })
}

// preemptLowerPriorityLocked requests a checkpoint boundary pause. RUNNING
// ranges are never stolen; their batch enters PAUSED_BY_PRIORITY after commit.
func (s *Service) preemptLowerPriorityLocked(active *BatchJob) {
	if active == nil || jobPriorityRank(active.Priority) < jobPriorityRank(PriorityHigh) {
		return
	}
	for _, batch := range s.store.ListBatches() {
		if batch.ID == active.ID || batch.Status.Terminal() || batch.PausedByPriority ||
			jobPriorityRank(batch.Priority) >= jobPriorityRank(active.Priority) {
			continue
		}
		if batch.Priority != PriorityNormal && batch.Priority != PriorityBackground {
			continue
		}
		batch.PauseRequested = true
		batch.PausedByPriority = true
		batch.PreemptedBy = active.ID
		batch.UpdatedAt = time.Now().UTC()
		_ = s.store.SaveBatch(batch)
		for _, a := range s.store.ListAddressesByBatch(batch.ID) {
			if a.Status.Terminal() {
				continue
			}
			a.PauseRequested = true
			_ = s.store.SaveAddress(a)
			for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
				if ds.Status.Terminal() {
					continue
				}
				ds.PauseRequested = true
				_ = s.store.SaveDataset(ds)
				_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{Event: LedgerPausedByPriority,
					DatasetJobID: ds.ID, Error: "preempted_by=" + active.ID})
			}
		}
		if !s.batchHasRunningRange(batch.ID) {
			s.transitionBatchPausedLocked(batch.ID)
		}
	}
}

func (s *Service) resumePreemptedLocked(completedBatchID string) {
	for _, batch := range s.store.ListBatches() {
		if !batch.PausedByPriority || batch.PreemptedBy != completedBatchID || batch.Status.Terminal() {
			continue
		}
		batch.PausedByPriority = false
		batch.PreemptedBy = ""
		batch.PauseRequested = false
		batch.Status = BatchRunning
		batch.UpdatedAt = time.Now().UTC()
		_ = s.store.SaveBatch(batch)
		for _, a := range s.store.ListAddressesByBatch(batch.ID) {
			if a.Status == AddressPaused {
				a.Status = AddressWaiting
			}
			a.PauseRequested = false
			_ = s.store.SaveAddress(a)
			for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
				if ds.Status == DatasetPaused {
					ds.Status = DatasetPending
				}
				ds.PauseRequested = false
				_ = s.store.SaveDataset(ds)
				_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{Event: LedgerAutoResumed,
					DatasetJobID: ds.ID, Error: "completed_preemptor=" + completedBatchID})
			}
		}
		s.wg.Add(1)
		go func(id string) {
			defer s.wg.Done()
			select {
			case <-s.ctx.Done():
				return
			default:
				_ = s.Start(id)
			}
		}(batch.ID)
	}
}

// acceptHedgeWinnerLocked enforces first validated winner. A losing hedge is
// never added to Checkpoint Parts, so canonical merge sees no logical duplicate.
func (s *Service) acceptHedgeWinnerLocked(candidate *RangeJob) bool {
	if candidate == nil {
		return false
	}
	root := candidate.ID
	if candidate.HedgeOf != "" {
		root = candidate.HedgeOf
	}
	for _, r := range s.store.ListRangesByDataset(candidate.DatasetJobID) {
		otherRoot := r.ID
		if r.HedgeOf != "" {
			otherRoot = r.HedgeOf
		}
		if r.ID != candidate.ID && otherRoot == root && (r.HedgeWinner || r.Certified) {
			candidate.Status = RangeCanceled
			candidate.Error = "hedge lost: first validated winner=" + r.ID
			now := time.Now().UTC()
			candidate.FinishedAt, candidate.UpdatedAt = &now, now
			_ = s.store.SaveRange(candidate)
			return false
		}
	}
	candidate.HedgeWinner = candidate.HedgeOf != "" || hasHedge(s.store.ListRangesByDataset(candidate.DatasetJobID), candidate.ID)
	return true
}

func (s *Service) certifyRangeLocked(r *RangeJob) {
	if r == nil || r.Certified {
		return
	}
	now := time.Now().UTC()
	r.Certified = true
	r.CertifiedAt = &now
	if r.HedgeOf != "" || hasHedge(s.store.ListRangesByDataset(r.DatasetJobID), r.ID) {
		r.HedgeWinner = true
	}
	_ = s.store.SaveRange(r)
	event := LedgerRangeCertified
	if r.HedgeWinner {
		event = LedgerHedgeWon
	}
	_ = NewLedger(s.store.Root(), r.DatasetJobID).Append(LedgerEntry{Event: event,
		DatasetJobID: r.DatasetJobID, RangeID: BlockRange{From: r.FromBlock, To: r.ToBlock}.Key(),
		FromBlock: r.FromBlock, ToBlock: r.ToBlock, Provider: r.Provider})
	ds := s.store.GetDataset(r.DatasetJobID)
	if ds == nil {
		return
	}
	// A certified range is transport-level evidence, not an end-to-end dataset
	// certificate. Final DATASET_CERTIFIED is emitted only after validation,
	// canonical merge, and any configured indexed write have all succeeded.
	ds.Certification = CertificationDatasetPartial
	ds.UpdatedAt = now
	_ = s.store.SaveDataset(ds)
	_ = NewLedger(s.store.Root(), ds.ID).Append(LedgerEntry{Event: LedgerDatasetPartialCertified,
		DatasetJobID: ds.ID, Error: "range=" + BlockRange{From: r.FromBlock, To: r.ToBlock}.Key()})
}

func (s *Service) allDatasetRangesCertifiedLocked(datasetID string) bool {
	ds := s.store.GetDataset(datasetID)
	if ds == nil {
		return false
	}
	requested := BlockRange{From: ds.RequestedRange.FromBlock, To: ds.RequestedRange.ToBlock}
	if cp, err := s.cp.Load(datasetID); err == nil {
		requested = BlockRange{From: cp.RequestedFrom, To: cp.RequestedTo}
	}
	certified := make([]BlockRange, 0)
	for _, r := range s.store.ListRangesByDataset(datasetID) {
		if r.Certified {
			certified = append(certified, BlockRange{From: r.FromBlock, To: r.ToBlock})
		}
	}
	if len(certified) == 0 || requested.To < requested.From {
		return false
	}
	_, missing := planReuse(requested, certified)
	return len(missing) == 0
}

func (s *Service) allRelevantRangesCertifiedLocked(datasetID string) bool {
	seen := false
	for _, r := range s.store.ListRangesByDataset(datasetID) {
		if !r.Relevant || r.HedgeOf != "" {
			continue
		}
		seen = true
		if !r.Certified {
			return false
		}
	}
	return seen
}

func (s *Service) autoDowngradeIfRelevantCertifiedLocked(batchID string) {
	batch := s.store.GetBatch(batchID)
	if batch == nil || batch.Mode != DownloadModeEmergency {
		return
	}
	seen := false
	for _, a := range s.store.ListAddressesByBatch(batchID) {
		for _, ds := range s.store.ListDatasetsByAddress(a.ID) {
			for _, r := range s.store.ListRangesByDataset(ds.ID) {
				if r.Relevant && r.HedgeOf == "" {
					seen = true
					if !r.Certified {
						return
					}
				}
			}
		}
	}
	if !seen {
		return
	}
	now := time.Now().UTC()
	batch.Mode = DownloadModeTurbo
	batch.ModeSwitchedAt = &now
	batch.UpdatedAt = now
	_ = s.store.SaveBatch(batch)
	_ = s.applyModePlanLocked(batchID, DownloadModeTurbo)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
