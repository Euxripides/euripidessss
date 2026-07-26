package cryptodownload

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

type csvByteRange struct {
	start int64
	end   int64
}

type csvStaticRangeResult struct {
	err      error
	degraded bool
}

type csvStaticVersionGuard struct {
	mu      sync.Mutex
	version csvStaticObjectVersion
}

func downloadCSVStaticRangesToPath(ctx context.Context, client *http.Client, observer requestTimingObserver, link string, object csvStaticObject, target string, policy csvStaticPolicy) error {
	temp, err := newCSVStaticTemp(target)
	if err != nil {
		return err
	}
	defer temp.cleanup()
	if err := temp.file.Truncate(object.size); err != nil {
		return newCSVStaticError(csvStaticInvalid, 0, fmt.Errorf("size range temp file: %w", err))
	}
	ranges := planCSVStaticRanges(object.size, policy.chunkSize)
	guard := &csvStaticVersionGuard{version: object.version}
	workers := policy.initialWorkers
	for next := 0; next < len(ranges); {
		end := min(next+workers, len(ranges))
		degraded, err := runCSVStaticRangeWave(ctx, client, observer, link, temp.file, ranges[next:end], object.size, guard, policy)
		if err != nil {
			return err
		}
		next = end
		if degraded {
			workers = max(1, workers-1)
		} else {
			workers = min(policy.maxWorkers, workers+1)
		}
	}
	return temp.publish(object.size, object.sha256)
}

func runCSVStaticRangeWave(ctx context.Context, client *http.Client, observer requestTimingObserver, link string, file *os.File, ranges []csvByteRange, total int64, guard *csvStaticVersionGuard, policy csvStaticPolicy) (bool, error) {
	waveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan csvStaticRangeResult, len(ranges))
	var wg sync.WaitGroup
	wg.Add(len(ranges))
	for _, byteRange := range ranges {
		go func() {
			defer wg.Done()
			result := downloadCSVStaticRangeWithRetry(waveCtx, client, observer, link, file, byteRange, total, guard, policy)
			results <- result
			if result.err != nil {
				cancel()
			}
		}()
	}
	wg.Wait()
	close(results)
	var firstErr error
	degraded := false
	for result := range results {
		degraded = degraded || result.degraded
		if result.err != nil && firstErr == nil && result.err != context.Canceled {
			firstErr = result.err
		}
	}
	if firstErr == nil && ctx.Err() != nil {
		firstErr = ctx.Err()
	}
	return degraded, firstErr
}

func downloadCSVStaticRangeWithRetry(ctx context.Context, client *http.Client, observer requestTimingObserver, link string, file *os.File, byteRange csvByteRange, total int64, guard *csvStaticVersionGuard, policy csvStaticPolicy) csvStaticRangeResult {
	degraded := false
	for attempt := 0; attempt < policy.rangeAttempts; attempt++ {
		err := downloadCSVStaticRange(ctx, client, observer, link, file, byteRange, total, guard)
		if err == nil {
			return csvStaticRangeResult{degraded: degraded}
		}
		if ctx.Err() != nil || !csvStaticCanRetry(err) || attempt+1 == policy.rangeAttempts {
			return csvStaticRangeResult{err: err, degraded: degraded}
		}
		degraded = true
		if err := csvWaitRetry(ctx, csvStaticRangeRetryDelay(attempt)); err != nil {
			return csvStaticRangeResult{err: err, degraded: degraded}
		}
	}
	return csvStaticRangeResult{err: newCSVStaticError(csvStaticRetryable, 0, fmt.Errorf("range attempts exhausted")), degraded: degraded}
}

func downloadCSVStaticRange(ctx context.Context, client *http.Client, observer requestTimingObserver, link string, file *os.File, byteRange csvByteRange, total int64, guard *csvStaticVersionGuard) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return newCSVStaticError(csvStaticInvalid, 0, err)
	}
	setCSVStaticHeaders(req)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", byteRange.start, byteRange.end))
	if value := guard.ifRange(); value != "" {
		req.Header.Set("If-Range", value)
	}
	resp, err := doHTTPRequest(client, req, observer)
	if err != nil {
		return newCSVStaticError(csvStaticRetryable, 0, err)
	}
	if resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return newCSVStaticError(csvStaticUnsafeRange, resp.StatusCode, fmt.Errorf("server ignored range request"))
		}
		return csvStaticStatusError(resp.StatusCode, nil)
	}
	if err := guard.check(csvStaticVersion(resp.Header)); err != nil {
		resp.Body.Close()
		return err
	}
	wantRange := fmt.Sprintf("bytes %d-%d/%d", byteRange.start, byteRange.end, total)
	if !strings.EqualFold(strings.TrimSpace(resp.Header.Get("Content-Range")), wantRange) {
		resp.Body.Close()
		return newCSVStaticError(csvStaticUnsafeRange, resp.StatusCode, fmt.Errorf("range %d-%d returned Content-Range %q", byteRange.start, byteRange.end, resp.Header.Get("Content-Range")))
	}
	wantLength := byteRange.end - byteRange.start + 1
	part, readErr := io.ReadAll(io.LimitReader(resp.Body, wantLength+1))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return csvStaticClassifyReadError(readErr)
	}
	if closeErr != nil {
		return csvStaticClassifyReadError(closeErr)
	}
	if int64(len(part)) != wantLength || (resp.ContentLength >= 0 && resp.ContentLength != wantLength) {
		return newCSVStaticError(csvStaticRetryable, resp.StatusCode, fmt.Errorf("range %d-%d returned %d bytes, want %d", byteRange.start, byteRange.end, len(part), wantLength))
	}
	if written, err := file.WriteAt(part, byteRange.start); err != nil || written != len(part) {
		return newCSVStaticError(csvStaticInvalid, 0, errorsOr(err, io.ErrShortWrite))
	}
	return nil
}

func planCSVStaticRanges(size, chunkSize int64) []csvByteRange {
	ranges := make([]csvByteRange, 0, int((size+chunkSize-1)/chunkSize))
	for start := int64(0); start < size; start += chunkSize {
		ranges = append(ranges, csvByteRange{start: start, end: min(start+chunkSize-1, size-1)})
	}
	return ranges
}

func (g *csvStaticVersionGuard) ifRange() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.version.etag != "" {
		return g.version.etag
	}
	return g.version.lastModified
}

func (g *csvStaticVersionGuard) check(version csvStaticObjectVersion) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.version.etag != "" && version.etag != g.version.etag {
		return newCSVStaticError(csvStaticUnsafeRange, 0, fmt.Errorf("range ETag changed"))
	}
	if g.version.lastModified != "" && version.lastModified != g.version.lastModified {
		return newCSVStaticError(csvStaticUnsafeRange, 0, fmt.Errorf("range Last-Modified changed"))
	}
	if g.version.etag == "" {
		g.version.etag = version.etag
	}
	if g.version.lastModified == "" {
		g.version.lastModified = version.lastModified
	}
	return nil
}
