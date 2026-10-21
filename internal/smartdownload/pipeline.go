package smartdownload

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultLocalSyncQueueCapacity  = 16
	defaultValidationQueueCapacity = 16
	defaultClickHouseQueueCapacity = 16
	defaultLocalSyncWorkers        = 2
	defaultValidationWorkers       = 2
	defaultClickHouseWorkers       = 2
	pipelineSampleLimit            = 2048
)

// PipelineStageSnapshot is a point-in-time view of one bounded downstream
// stage. Totals are monotonic for the lifetime of the Service.
type PipelineStageSnapshot struct {
	Depth          int     `json:"depth"`
	Capacity       int     `json:"capacity"`
	QueuedTotal    uint64  `json:"queued_total"`
	StartedTotal   uint64  `json:"started_total"`
	CompletedTotal uint64  `json:"completed_total"`
	FailedTotal    uint64  `json:"failed_total"`
	Running        int     `json:"running"`
	Workers        int     `json:"workers"`
	RowsTotal      uint64  `json:"rows_total"`
	BytesTotal     uint64  `json:"bytes_total"`
	JobsPerSecond  float64 `json:"jobs_per_second"`
	RowsPerSecond  float64 `json:"rows_per_second"`
	BytesPerSecond float64 `json:"bytes_per_second"`
	QueueWaitP95MS float64 `json:"queue_wait_p95_ms"`
	DurationP95MS  float64 `json:"duration_p95_ms"`
}

type PipelineSnapshot struct {
	CapturedAt time.Time             `json:"captured_at"`
	LocalSync  PipelineStageSnapshot `json:"local_sync"`
	Validation PipelineStageSnapshot `json:"validation"`
	ClickHouse PipelineStageSnapshot `json:"clickhouse"`
}

type pipelineOutcome struct {
	rows  uint64
	bytes uint64
	err   error
}

type pipelineTask struct {
	key        string
	enqueuedAt time.Time
	run        func(context.Context) pipelineOutcome
}

type pipelineStage struct {
	ctx       context.Context
	queue     chan pipelineTask
	workers   int
	startedAt time.Time
	wg        *sync.WaitGroup

	queued   atomic.Uint64
	started  atomic.Uint64
	complete atomic.Uint64
	failed   atomic.Uint64
	running  atomic.Int64
	rows     atomic.Uint64
	bytes    atomic.Uint64

	sampleMu    sync.Mutex
	waitsMS     []float64
	durationsMS []float64
	throughput  []pipelineThroughputSample
	pending     sync.Map
}

type pipelineThroughputSample struct {
	at    time.Time
	rows  uint64
	bytes uint64
}

func newPipelineStage(ctx context.Context, capacity, workers int, wg *sync.WaitGroup) *pipelineStage {
	if capacity < 1 {
		capacity = 1
	}
	if workers < 1 {
		workers = 1
	}
	p := &pipelineStage{ctx: ctx, queue: make(chan pipelineTask, capacity), workers: workers, startedAt: time.Now(), wg: wg}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go p.worker()
	}
	return p
}

func (p *pipelineStage) enqueueBlocking(key string, run func(context.Context) pipelineOutcome) error {
	if run == nil {
		return errors.New("下游任务为空")
	}
	if _, loaded := p.pending.LoadOrStore(key, struct{}{}); loaded {
		return nil
	}
	task := pipelineTask{key: key, enqueuedAt: time.Now(), run: run}
	select {
	case <-p.ctx.Done():
		p.pending.Delete(key)
		return p.ctx.Err()
	case p.queue <- task:
		p.queued.Add(1)
		return nil
	}
}

func (p *pipelineStage) tryEnqueue(key string, run func(context.Context) pipelineOutcome) bool {
	if run == nil {
		return false
	}
	if _, loaded := p.pending.LoadOrStore(key, struct{}{}); loaded {
		return true
	}
	select {
	case <-p.ctx.Done():
		p.pending.Delete(key)
		return false
	case p.queue <- pipelineTask{key: key, enqueuedAt: time.Now(), run: run}:
		p.queued.Add(1)
		return true
	default:
		p.pending.Delete(key)
		return false
	}
}

func (p *pipelineStage) worker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case task := <-p.queue:
			started := time.Now()
			p.started.Add(1)
			p.running.Add(1)
			out := task.run(p.ctx)
			p.running.Add(-1)
			if out.err != nil {
				p.failed.Add(1)
			} else {
				p.complete.Add(1)
			}
			p.rows.Add(out.rows)
			p.bytes.Add(out.bytes)
			p.recordSamples(started.Sub(task.enqueuedAt), time.Since(started), out)
			p.pending.Delete(task.key)
		}
	}
}

func (p *pipelineStage) recordSamples(wait, duration time.Duration, out pipelineOutcome) {
	p.sampleMu.Lock()
	defer p.sampleMu.Unlock()
	p.waitsMS = appendBounded(p.waitsMS, float64(wait.Microseconds())/1000)
	p.durationsMS = appendBounded(p.durationsMS, float64(duration.Microseconds())/1000)
	if out.err == nil {
		p.throughput = append(p.throughput, pipelineThroughputSample{at: time.Now(), rows: out.rows, bytes: out.bytes})
		cutoff := time.Now().Add(-60 * time.Second)
		first := 0
		for first < len(p.throughput) && p.throughput[first].at.Before(cutoff) {
			first++
		}
		if first > 0 {
			p.throughput = append([]pipelineThroughputSample(nil), p.throughput[first:]...)
		}
	}
}

func appendBounded(values []float64, value float64) []float64 {
	if len(values) >= pipelineSampleLimit {
		copy(values, values[len(values)-pipelineSampleLimit+1:])
		values = values[:pipelineSampleLimit-1]
	}
	return append(values, value)
}

func p95(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	index := (95*len(copyValues)+99)/100 - 1
	if index < 0 {
		index = 0
	}
	return copyValues[index]
}

func (p *pipelineStage) snapshot() PipelineStageSnapshot {
	p.sampleMu.Lock()
	waitP95, durationP95 := p95(p.waitsMS), p95(p.durationsMS)
	cutoff := time.Now().Add(-60 * time.Second)
	var recentJobs, recentRows, recentBytes uint64
	for _, sample := range p.throughput {
		if sample.at.Before(cutoff) {
			continue
		}
		recentJobs++
		recentRows += sample.rows
		recentBytes += sample.bytes
	}
	p.sampleMu.Unlock()
	windowSeconds := time.Since(p.startedAt).Seconds()
	if windowSeconds > 60 {
		windowSeconds = 60
	}
	if windowSeconds <= 0 {
		windowSeconds = 1
	}
	completed := p.complete.Load()
	rows, bytes := p.rows.Load(), p.bytes.Load()
	return PipelineStageSnapshot{
		Depth: len(p.queue), Capacity: cap(p.queue), QueuedTotal: p.queued.Load(), StartedTotal: p.started.Load(),
		CompletedTotal: completed, FailedTotal: p.failed.Load(), Running: int(p.running.Load()), Workers: p.workers,
		RowsTotal: rows, BytesTotal: bytes, JobsPerSecond: float64(recentJobs) / windowSeconds,
		RowsPerSecond: float64(recentRows) / windowSeconds, BytesPerSecond: float64(recentBytes) / windowSeconds,
		QueueWaitP95MS: waitP95, DurationP95MS: durationP95,
	}
}

type downstreamPipeline struct {
	localSync  *pipelineStage
	validation *pipelineStage
	clickhouse *pipelineStage
	reindex    sync.Map
}

func boundedPipelineCapacity(value int) int {
	if value <= 0 {
		return defaultLocalSyncQueueCapacity
	}
	if value > 4096 {
		return 4096
	}
	return value
}

func boundedPipelineWorkers(value int) int {
	if value <= 0 {
		return defaultLocalSyncWorkers
	}
	if value > 64 {
		return 64
	}
	return value
}

func newDownstreamPipeline(ctx context.Context, wg *sync.WaitGroup, opts Options) *downstreamPipeline {
	return &downstreamPipeline{
		localSync:  newPipelineStage(ctx, opts.LocalSyncQueueCapacity, opts.LocalSyncWorkers, wg),
		validation: newPipelineStage(ctx, opts.ValidationQueueCapacity, opts.ValidationWorkers, wg),
		clickhouse: newPipelineStage(ctx, opts.ClickHouseQueueCapacity, opts.ClickHouseWorkers, wg),
	}
}

func (s *Service) PipelineSnapshot() PipelineSnapshot {
	if s == nil || s.pipeline == nil {
		return PipelineSnapshot{CapturedAt: time.Now().UTC()}
	}
	return PipelineSnapshot{CapturedAt: time.Now().UTC(), LocalSync: s.pipeline.localSync.snapshot(), Validation: s.pipeline.validation.snapshot(), ClickHouse: s.pipeline.clickhouse.snapshot()}
}

// startPipelineDispatcher is the only retry loop for persisted downstream
// states. It prevents one goroutine per dataset while ensuring a full bounded
// queue never loses VALIDATING/INDEXING work.
func (s *Service) startPipelineDispatcher() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				for _, ds := range s.store.ListDatasets() {
					if ds == nil || ds.CancelRequested {
						continue
					}
					switch ds.Status {
					case DatasetValidating:
						s.enqueueValidation(ds.ID)
					case DatasetIndexing:
						reindex := false
						if entry := s.results.find(ds.ID); entry != nil && entry.Certification == "CERTIFIED" && entry.MergedParquet != "" {
							reindex = true
						}
						s.enqueueClickHouse(ds.ID, reindex)
					}
				}
			}
		}
	}()
}

type cloudRemoteResult struct {
	jobID string
	rows  uint64
	bytes uint64
	sync  func(context.Context) (*ProviderResult, error)
}

type deferredCloudRangeAdapter interface {
	ExecuteRemoteRange(context.Context, RangeRequest) (*cloudRemoteResult, error)
}

type deferredCloudGroupAdapter interface {
	ExecuteRemoteGroupRange(context.Context, GroupRangeRequest) (*cloudRemoteResult, error)
}
