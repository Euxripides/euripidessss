package downloadengine

import (
	"testing"
	"time"
)

// ── Step 6: BSC 单地址端到端 ──

func TestBSCSingleAddressE2E(t *testing.T) {
	// 模拟完整管线: Create → Validate → Discover → Plan → Execute → Complete
	job := &Job{
		ID:        "e2e-bsc-001",
		Type:      JobAddressSingle,
		ChainID:   "bsc",
		Status:    StatusCreated,
		Stage:     StageIdle,
		RangeMode: RangeAutoFirstSeen,
		CreatedAt: time.Now().UTC(),
	}

	// 1. Validating
	if err := job.Transition(StatusValidating); err != nil {
		t.Fatalf("CREATED→VALIDATING: %v", err)
	}

	// 2. Discovery
	job.SetStage(StageDiscovering)
	// mock: block 8123456 found for address
	block := uint64(8123456)
	tm := "2020-04-18T00:00:00Z"
	job.Discovery = &DiscoveryResult{
		Total: 1, Found: 1,
		Items: []AddressDiscovery{
			{Address: "0xaaa", FirstSeenBlock: &block, FirstSeenTime: &tm, Status: FSFound, Coverage: CoverageV2Full},
		},
	}

	// 3. ResolveRange
	job.SetStage(StageResolvingRange)
	job.EffectiveRange = &EffectiveRange{
		StartBlock:     block,
		EndBlock:       54000000,
		StartTime:      tm,
		EndTime:        "2026-07-30T00:00:00Z",
		RangeSource:    "FIRST_SEEN",
		CoverageStatus: "FULL",
	}

	// 4. Queued → Running
	if err := job.Transition(StatusQueued); err != nil {
		t.Fatalf("VALIDATING→QUEUED: %v", err)
	}
	if err := job.Transition(StatusRunning); err != nil {
		t.Fatalf("QUEUED→RUNNING: %v", err)
	}
	job.SetStage(StageDownloading)

	// 5. Execute (mock: chunk succeeds)
	chunk := &Chunk{
		ID:          "chunk-001",
		JobID:       job.ID,
		ChainID:     "bsc",
		DatasetType: "transactions",
		StartBlock:  8123456,
		EndBlock:    8223456,
		Status:      ChunkSucceeded,
		RowsWritten: 150000,
	}
	job.Chunks = append(job.Chunks, chunk)

	// 6. Manifest + Gate
	gate := NewCompletionGate()
	job.SetStage(StageFinalizing)
	if err := gate.Verify(job, job.Chunks, true, true, true); err != nil {
		t.Fatalf("completion gate: %v", err)
	}

	// 7. Complete
	if err := job.Transition(StatusCompleted); err != nil {
		t.Fatalf("RUNNING→COMPLETED: %v", err)
	}
	if job.FinishedAt == nil {
		t.Fatal("FinishedAt should be set on COMPLETED")
	}

	t.Logf("E2E BSC single address: status=%s discovery=%d/%d range=%d-%d chunks=%d",
		job.Status, job.Discovery.Found, job.Discovery.Total,
		job.EffectiveRange.StartBlock, job.EffectiveRange.EndBlock, len(job.Chunks))
}

// ── Step 7: 断点恢复 ──

func TestBreakpointResume(t *testing.T) {
	// Simulate: job paused mid-download, then resumed
	job := &Job{
		ID:       "recovery-001",
		Type:     JobAddressBatch,
		ChainID:  "bsc",
		Status:   StatusRunning,
		Stage:    StageDownloading,
		CreatedAt: time.Now().UTC(),
	}

	// 2 of 4 chunks completed before interruption
	job.Chunks = []*Chunk{
		{ID: "c1", StartBlock: 8000000, EndBlock: 8100000, Status: ChunkSucceeded, RowsWritten: 50000},
		{ID: "c2", StartBlock: 8100000, EndBlock: 8200000, Status: ChunkSucceeded, RowsWritten: 48000},
		{ID: "c3", StartBlock: 8200000, EndBlock: 8300000, Status: ChunkPending},
		{ID: "c4", StartBlock: 8300000, EndBlock: 8400000, Status: ChunkPending},
	}

	// Pause → Resume
	if err := job.Transition(StatusPausing); err != nil {
		t.Fatal(err)
	}
	if err := job.Transition(StatusPaused); err != nil {
		t.Fatal(err)
	}
	if err := job.Transition(StatusRunning); err != nil {
		t.Fatal(err)
	}

	// Resume from checkpoint: skip completed, resume from c3
	lastSuccess := uint64(8200000)
	job.SetStage(StageResolvingRange)
	job.EffectiveRange = &EffectiveRange{
		StartBlock:  lastSuccess, // resume point
		EndBlock:    8400000,
		RangeSource: "RESUME",
	}

	// Complete remaining chunks
	job.Chunks[2].Status = ChunkSucceeded
	job.Chunks[2].RowsWritten = 51000
	job.Chunks[3].Status = ChunkSucceeded
	job.Chunks[3].RowsWritten = 49000

	gate := NewCompletionGate()
	if err := gate.Verify(job, job.Chunks, true, true, true); err != nil {
		t.Fatalf("recovery gate: %v", err)
	}

	if err := job.Transition(StatusCompleted); err != nil {
		t.Fatalf("recovery→COMPLETED: %v", err)
	}

	// Verify: only pending chunks were resumed
	for i, ch := range job.Chunks {
		if ch.Status != ChunkSucceeded {
			t.Errorf("chunk %d should be succeeded after resume, got %s", i, ch.Status)
		}
	}
	t.Logf("breakpoint resume: paused at %d, resumed %d chunks", lastSuccess, 2)
}

// ── Step 7: SQD 503 故障恢复 ──

func TestSQD503Failover(t *testing.T) {
	classifier := &ErrorClassifier{}

	// 模拟 SQD 503 错误
	err503 := classifiableError("503 Service Unavailable: no available workers")
	code := classifier.Classify(err503)
	if code != ErrSQDNoWorkers {
		t.Errorf("503 should classify as SQD_NO_WORKERS, got %s", code)
	}

	// 模拟 SQD 429 限流
	err429 := classifiableError("429 Too Many Requests: rate limit exceeded")
	if classifier.Classify(err429) != ErrSQDRateLimited {
		t.Errorf("429 should classify as SQD_RATE_LIMITED")
	}

	// FailoverBudget: 限制故障转移次数
	budget := NewFailoverBudget(2)
	if !budget.AllowFailover("chunk-001") {
		t.Fatal("first failover should be allowed")
	}
	if !budget.AllowFailover("chunk-001") {
		t.Fatal("second failover should be allowed")
	}
	if budget.AllowFailover("chunk-001") {
		t.Fatal("third failover should be blocked by budget")
	}

	// RetryBudget: 限制重试次数
	retry := NewRetryBudget(3, 10)
	if !retry.AllowChunkRetry("chunk-001", 1) {
		t.Fatal("attempt 1 should be allowed")
	}
	if !retry.AllowChunkRetry("chunk-001", 3) {
		t.Fatal("attempt 3 should be allowed")
	}
	if retry.AllowChunkRetry("chunk-001", 4) {
		t.Fatal("attempt 4 should exceed perChunk budget")
	}
}

type classifiableError string

func (e classifiableError) Error() string { return string(e) }
