package smartdownload

import (
	"context"
	"testing"
	"time"
)

type fixedRPCPoolSource struct{ snapshot RPCPoolMetrics }

func (f fixedRPCPoolSource) SmartDownloadRPCPoolSnapshot(string) (RPCPoolMetrics, error) {
	return f.snapshot, nil
}

func TestV31CaseATrueDualLaneNoOverlap(t *testing.T) {
	store, svc := newTurboService(t)
	t.Cleanup(svc.Shutdown)
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeTurbo, Addresses: []string{addrA},
		Datasets: []string{DatasetTokenTransfers},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RebalanceBatch(created.Batch.ID); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	cloud, rpc := 0, 0
	for _, r := range store.ListRanges() {
		key := BlockRange{From: r.FromBlock, To: r.ToBlock}.Key()
		if seen[key] {
			t.Fatalf("default lanes overlap at %s", key)
		}
		seen[key] = true
		if r.Owner == RangeOwnerCloud {
			cloud++
		}
		if r.Owner == RangeOwnerRPC {
			rpc++
		}
	}
	if cloud == 0 || rpc == 0 {
		t.Fatalf("true dual lane not active: cloud=%d rpc=%d", cloud, rpc)
	}
}

func TestV31CaseBEndpoint429Isolation(t *testing.T) {
	pool := RPCPoolMetrics{Endpoints: []RPCEndpointMetrics{
		{Name: "rpc-a", Rate429: .8, CurrentWorkers: 8},
		{Name: "rpc-b", SuccessRate: .99, CurrentWorkers: 4},
		{Name: "rpc-c", SuccessRate: .98, CurrentWorkers: 3},
	}}
	if got := healthyRPCWorkers(pool); got != 7 {
		t.Fatalf("429 endpoint was not isolated or healthy peers were reduced: got=%d want=7", got)
	}
	_, svc := newTurboService(t)
	t.Cleanup(svc.Shutdown)
	svc.SetRPCPoolMetricsSource(fixedRPCPoolSource{snapshot: pool})
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeTurbo, Addresses: []string{addrA}, Datasets: []string{DatasetTokenTransfers},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RebalanceBatch(created.Batch.ID); err != nil {
		t.Fatal(err)
	}
	status, _ := svc.TurboStatus(created.Batch.ID)
	if status.RPCClaimsLimit != 7 {
		t.Fatalf("rpc claims=%d want healthy B/C workers 7", status.RPCClaimsLimit)
	}
}

func TestV31CaseCRelevantRangeClaimsBeforeHistory(t *testing.T) {
	_, svc := newTurboService(t)
	t.Cleanup(svc.Shutdown)
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeTurbo, Priority: PriorityHigh,
		Addresses: []string{addrA}, Datasets: []string{DatasetTokenTransfers},
		RelevantRanges: []RangeSpec{{Mode: RangeModeBlock, FromBlock: 0, ToBlock: 199}},
	})
	if err != nil {
		t.Fatal(err)
	}
	claim := svc.claimNext(created.Batch.ID)
	if claim == nil {
		t.Fatal("expected relevant claim")
	}
	r := svc.store.GetRange(claim.rangeID)
	if !r.Relevant || (r.PriorityClass != RangePriorityP0 && r.PriorityClass != RangePriorityP1) {
		t.Fatalf("claimed historical range before relevant: %+v", r)
	}
}

func TestV31CaseDClickHouseBackpressure(t *testing.T) {
	_, svc := newTurboService(t)
	t.Cleanup(svc.Shutdown)
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeEmergency, Priority: PriorityBackground,
		Addresses: []string{addrA}, Datasets: []string{DatasetTokenTransfers},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Batch.Priority != PriorityUrgent {
		t.Fatalf("EMERGENCY must override queue priority to URGENT: %s", created.Batch.Priority)
	}
	svc.UpdateThroughput(created.Batch.ID, ThroughputSnapshot{
		DownloadedRowsPerSecond: 150_000, ParsedRowsPerSecond: 148_000,
		InsertedRowsPerSecond: 60_000, MergeQueue: 10,
	})
	if err := svc.RebalanceBatch(created.Batch.ID); err != nil {
		t.Fatal(err)
	}
	status, _ := svc.TurboStatus(created.Batch.ID)
	if status.Bottleneck != "CLICKHOUSE" || !status.CloudPausedByGovernor || status.ClaimsLimit >= svc.opts.Workers {
		t.Fatalf("governor did not apply ingest backpressure: %+v", status)
	}
}

func TestV31CaseEModeEscalationPreservesCompletedCoverage(t *testing.T) {
	store, svc := newTurboService(t)
	t.Cleanup(svc.Shutdown)
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeAuto, Addresses: []string{addrA}, Datasets: []string{DatasetTokenTransfers},
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := store.ListRanges()[0]
	completed.Status, completed.Provider, completed.RowsCommitted = RangeCompleted, "existing", 9
	if err := store.SaveRange(completed); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetBatchMode(created.Batch.ID, DownloadModeTurbo); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetBatchMode(created.Batch.ID, DownloadModeEmergency); err != nil {
		t.Fatal(err)
	}
	got := store.GetRange(completed.ID)
	if got.Status != RangeCompleted || got.Provider != "existing" || got.RowsCommitted != 9 || got.Owner != "" {
		t.Fatalf("completed coverage was replanned: %+v", got)
	}
}

func TestV31CaseFDensityAwareShardSizing(t *testing.T) {
	dense := PlanDensityAwareShards(0, 999_999, 100, 100_000)
	sparse := PlanDensityAwareShards(0, 999_999, .1, 100_000)
	if len(dense) <= len(sparse) {
		t.Fatalf("dense=%d shards sparse=%d; dense must split smaller", len(dense), len(sparse))
	}
	if rangeBlockCount(&RangeJob{FromBlock: dense[0].From, ToBlock: dense[0].To}) >=
		rangeBlockCount(&RangeJob{FromBlock: sparse[0].From, ToBlock: sparse[0].To}) {
		t.Fatal("dense shard span is not smaller than sparse shard span")
	}
}

func TestV31CaseGRangeCertificationWaitsForDatasetQualityGate(t *testing.T) {
	store, svc := newTurboService(t)
	t.Cleanup(svc.Shutdown)
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeEmergency, Addresses: []string{addrA},
		Datasets:       []string{DatasetTokenTransfers},
		RelevantRanges: []RangeSpec{{Mode: RangeModeBlock, FromBlock: 0, ToBlock: 199}},
	})
	if err != nil {
		t.Fatal(err)
	}
	batch := store.GetBatch(created.Batch.ID)
	started := time.Now().UTC().Add(-2 * time.Second)
	batch.StartedAt = &started
	if err := store.SaveBatch(batch); err != nil {
		t.Fatal(err)
	}
	var relevant *RangeJob
	for _, r := range store.ListRanges() {
		if r.Relevant {
			relevant = r
			break
		}
	}
	if relevant == nil {
		t.Fatal("relevant range missing")
	}
	svc.mu.Lock()
	relevant.Status = RangeCompleted
	svc.certifyRangeLocked(relevant)
	svc.mu.Unlock()
	if got := store.GetBatch(created.Batch.ID).Mode; got != DownloadModeEmergency {
		t.Fatalf("range evidence prematurely downgraded EMERGENCY: %s", got)
	}
	ds := store.GetDataset(relevant.DatasetJobID)
	if ds.RelevantCertified || ds.Certification != CertificationDatasetPartial {
		t.Fatalf("range evidence prematurely certified the dataset: %+v", ds)
	}
	status, _ := svc.TurboStatus(created.Batch.ID)
	if status.TimeToFirstRelevantSecs <= 0 || status.RelevantCertification != "RANGE_CERTIFIED" ||
		status.CompletedRanges == len(store.ListRanges()) {
		t.Fatalf("range-level TTFR evidence was not labeled precisely: %+v", status)
	}
}

func TestV31PriorityPreemptionAndAutoResume(t *testing.T) {
	store, svc := newTurboService(t)
	t.Cleanup(svc.Shutdown)
	normal, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeTurbo, Priority: PriorityNormal,
		Addresses: []string{addrA}, Datasets: []string{DatasetTokenTransfers},
	})
	if err != nil {
		t.Fatal(err)
	}
	high, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeEmergency, Priority: PriorityUrgent,
		Addresses: []string{"0x2222222222222222222222222222222222222222"}, Datasets: []string{DatasetTokenTransfers},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	svc.preemptLowerPriorityLocked(store.GetBatch(high.Batch.ID))
	svc.mu.Unlock()
	if got := store.GetBatch(normal.Batch.ID); got.Status != BatchPausedByPriority || !got.PausedByPriority {
		t.Fatalf("normal batch was not priority-paused: %+v", got)
	}
	svc.mu.Lock()
	svc.resumePreemptedLocked(high.Batch.ID)
	svc.mu.Unlock()
	if got := store.GetBatch(normal.Batch.ID); got.PausedByPriority || got.Status != BatchRunning {
		t.Fatalf("normal batch was not auto-resumed: %+v", got)
	}
}

func TestV31HedgeFirstCertifiedWinnerNoLogicalDuplicate(t *testing.T) {
	store, svc := newTurboService(t)
	t.Cleanup(svc.Shutdown)
	created, err := svc.CreateBatch(context.Background(), CreateBatchRequest{
		ChainKey: "bsc", Mode: DownloadModeTurbo, Priority: PriorityHigh,
		Addresses: []string{addrA}, Datasets: []string{DatasetTokenTransfers},
		RelevantRanges: []RangeSpec{{Mode: RangeModeBlock, FromBlock: 0, ToBlock: 199}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var original *RangeJob
	for _, r := range store.ListRanges() {
		if r.Relevant {
			original = r
			break
		}
	}
	start := time.Now().UTC().Add(-3 * time.Second)
	original.Status, original.Owner, original.StartedAt, original.ETASeconds = RangeRunning, RangeOwnerRPC, &start, 1
	if err := store.SaveRange(original); err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	svc.createDueHedgesLocked(store.GetBatch(created.Batch.ID), store.GetDataset(original.DatasetJobID), time.Now().UTC())
	var hedge *RangeJob
	for _, r := range store.ListRangesByDataset(original.DatasetJobID) {
		if r.HedgeOf == original.ID {
			hedge = r
			break
		}
	}
	if hedge == nil {
		svc.mu.Unlock()
		t.Fatal("hedge was not created after ETA*2")
	}
	if !svc.acceptHedgeWinnerLocked(hedge) {
		svc.mu.Unlock()
		t.Fatal("first hedge should win")
	}
	hedge.Status = RangeCompleted
	svc.certifyRangeLocked(hedge)
	if svc.acceptHedgeWinnerLocked(original) {
		svc.mu.Unlock()
		t.Fatal("second completion must lose")
	}
	svc.mu.Unlock()
	if got := store.GetRange(original.ID); got.Status != RangeCanceled {
		t.Fatalf("loser status=%s want CANCELED", got.Status)
	}
	if got := store.GetRange(hedge.ID); !got.Certified || !got.HedgeWinner {
		t.Fatalf("winner not certified: %+v", got)
	}
}
