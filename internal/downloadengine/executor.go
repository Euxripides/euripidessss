package downloadengine

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// ── Chunk Executor ──

type Executor struct {
	router       *Router
	maxRetries   int
	backoffBase  time.Duration
	backoffMax   time.Duration
}

func NewExecutor(router *Router) *Executor {
	return &Executor{
		router:      router,
		maxRetries:  3,
		backoffBase: 5 * time.Second,
		backoffMax:  300 * time.Second,
	}
}

func (x *Executor) ExecuteChunk(ctx context.Context, chunk *Chunk) error {
	chunk.Attempt = 0
	chunk.Status = ChunkRunning
	now := time.Now().UTC()
	chunk.StartedAt = &now

	for attempt := 1; attempt <= x.maxRetries+1; attempt++ {
		chunk.Attempt = attempt
		err := x.tryExecute(ctx, chunk)
		if err == nil {
			chunk.Status = ChunkSucceeded
			now := time.Now().UTC()
			chunk.CompletedAt = &now
			return nil
		}

		if attempt <= x.maxRetries {
			backoff := time.Duration(math.Min(
				float64(x.backoffBase)*math.Pow(2, float64(attempt-1)),
				float64(x.backoffMax),
			))
			chunk.Status = ChunkRetryWait
			chunk.ErrorMessage = err.Error()

			select {
			case <-ctx.Done():
				chunk.Status = ChunkCancelled
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}

	chunk.Status = ChunkFailed
	now = time.Now().UTC()
	chunk.CompletedAt = &now
	return fmt.Errorf("%s: chunk %s 所有重试已用尽", ErrValidationFailed, chunk.ID)
}

func (x *Executor) tryExecute(ctx context.Context, chunk *Chunk) error {
	stream, ok := x.router.ResolveStreaming(chunk.DatasetType, chunk.ChainID)
	if !ok {
		return fmt.Errorf("%s: 无可用 StreamingProvider for %s/%s", ErrRPCUnavailable, chunk.ChainID, chunk.DatasetType)
	}

	records, errs := stream.ExecuteStream(ctx, StreamRequest{
		ChainID:     chunk.ChainID,
		DatasetType: chunk.DatasetType,
		StartBlock:  chunk.StartBlock,
		EndBlock:    chunk.EndBlock,
		ChunkSize:   100000,
	})

	var count int64
	for {
		select {
		case rec, open := <-records:
			if !open {
				chunk.RowsWritten = count
				return nil
			}
			count++
			_ = rec // writer integration point
		case err, open := <-errs:
			if !open {
				return nil
			}
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ── Rate Limiter / Budget Manager ──

type RateLimiter struct {
	mu            sync.Mutex
	concurrent    int
	maxConcurrent int
	sem           chan struct{}
}

func NewRateLimiter(maxConcurrent int) *RateLimiter {
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	return &RateLimiter{
		maxConcurrent: maxConcurrent,
		sem:           make(chan struct{}, maxConcurrent),
	}
}

func (r *RateLimiter) Acquire(ctx context.Context) error {
	select {
	case r.sem <- struct{}{}:
		r.mu.Lock()
		r.concurrent++
		r.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *RateLimiter) Release() {
	r.mu.Lock()
	if r.concurrent > 0 {
		r.concurrent--
	}
	r.mu.Unlock()
	// 非阻塞释放，防止 double-release 死锁
	select {
	case <-r.sem:
	default:
	}
}

func (r *RateLimiter) Active() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.concurrent
}

// ── Error Classifier ──
// 将原始错误映射为统一 ErrorCode，用于重试/故障转移决策。

type ErrorClassifier struct{}

func (c *ErrorClassifier) Classify(err error) ErrorCode {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case containsPattern(msg, "503"), containsPattern(msg, "no available workers"):
		return ErrSQDNoWorkers
	case containsPattern(msg, "429"), containsPattern(msg, "rate limit"):
		return ErrSQDRateLimited
	case containsPattern(msg, "circuit open"):
		return ErrSQDCircuitOpen
	case containsPattern(msg, "not found"), containsPattern(msg, "404"):
		return ErrAWSFileNotFound
	case containsPattern(msg, "rpc unavailable"), containsPattern(msg, "connection refused"):
		return ErrRPCUnavailable
	case containsPattern(msg, "disk space"), containsPattern(msg, "insufficient space"):
		return ErrDiskSpaceInsufficient
	case containsPattern(msg, "parquet write"), containsPattern(msg, "write parquet"):
		return ErrParquetWriteFailed
	case containsPattern(msg, "manifest inconsistent"), containsPattern(msg, "manifest mismatch"):
		return ErrManifestInconsistent
	case containsPattern(msg, "duckdb index"), containsPattern(msg, "index failed"):
		return ErrDuckDBIndexFailed
	case containsPattern(msg, "validation failed"), containsPattern(msg, "validate failed"):
		return ErrValidationFailed
	default:
		return ErrValidationFailed
	}
}

func containsPattern(s, pattern string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(pattern))
}

func containsAny(s string, substrs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range substrs {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

// ── Retry Budget ──
// 限制每个 Chunk 的总重试次数和 Provider 级别的重试预算。

type RetryBudget struct {
	mu              sync.Mutex
	perChunkMax     int
	perProviderMax  int
	providerRetries map[string]int
}

func NewRetryBudget(perChunk, perProvider int) *RetryBudget {
	return &RetryBudget{
		perChunkMax:     perChunk,
		perProviderMax:  perProvider,
		providerRetries: make(map[string]int),
	}
}

func (b *RetryBudget) AllowChunkRetry(chunkID string, attempt int) bool {
	return attempt <= b.perChunkMax
}

func (b *RetryBudget) AllowProviderRetry(provider string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.providerRetries[provider] >= b.perProviderMax {
		return false
	}
	b.providerRetries[provider]++
	return true
}

func (b *RetryBudget) ResetProvider(provider string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.providerRetries[provider] = 0
}

// ── Failover Budget ──
// 限制 Provider 故障转移次数，防止无限切换。

type FailoverBudget struct {
	mu           sync.Mutex
	maxFailovers int
	failovers    map[string]int // chunkID → failover count
}

func NewFailoverBudget(max int) *FailoverBudget {
	return &FailoverBudget{
		maxFailovers: max,
		failovers:    make(map[string]int),
	}
}

func (f *FailoverBudget) AllowFailover(chunkID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failovers[chunkID] >= f.maxFailovers {
		return false
	}
	f.failovers[chunkID]++
	return true
}
