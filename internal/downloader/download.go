package downloader

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

type Source struct {
	URL       string
	ETag      string
	SizeBytes int64
}

type Options struct {
	Client          *http.Client
	Workers         int
	ChunkSize       int64
	CheckpointPath  string
	OnProgress      func(int64)
	OnChunkComplete func(index, total int, resumed bool)
}

type Result struct {
	SHA256        string
	ResumedChunks int
	TotalChunks   int
}

func Download(ctx context.Context, source Source, destination string, options Options) (Result, error) {
	if source.URL == "" || source.SizeBytes <= 0 {
		return Result{}, errors.New("并行下载源参数无效")
	}
	if options.Client == nil {
		options.Client = http.DefaultClient
	}
	if options.Workers < 1 {
		options.Workers = 1
	}
	if options.Workers > 8 {
		options.Workers = 8
	}
	if options.ChunkSize <= 0 {
		options.ChunkSize = DefaultChunkSize
	}
	if options.CheckpointPath == "" {
		options.CheckpointPath = destination + ".download.json"
	}
	if options.OnProgress == nil {
		options.OnProgress = func(int64) {}
	}
	if options.OnChunkComplete == nil {
		options.OnChunkComplete = func(int, int, bool) {}
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(chunksDir(options.CheckpointPath), 0755); err != nil {
		return Result{}, err
	}
	chunks := planChunks(source.SizeBytes, options.ChunkSize)
	store, err := newCheckpointStore(options.CheckpointPath, source, options.ChunkSize)
	if err != nil {
		return Result{}, err
	}
	if err := store.save(); err != nil {
		return Result{}, err
	}
	completed := store.completed(chunks)
	baseBytes := completedBytes(chunks, completed)
	options.OnProgress(baseBytes)
	for index := range completed {
		options.OnChunkComplete(index, len(chunks), true)
	}

	type chunkResult struct {
		chunk Chunk
		err   error
	}
	queue := make(chan Chunk)
	results := make(chan chunkResult)
	active := map[int]int64{}
	var progressMu sync.Mutex
	report := func(index int, value int64) {
		progressMu.Lock()
		active[index] = value
		total := baseBytes
		for _, current := range active {
			total += current
		}
		progressMu.Unlock()
		options.OnProgress(total)
	}
	workerCount := options.Workers
	if workerCount > len(chunks)-len(completed) {
		workerCount = len(chunks) - len(completed)
	}
	if workerCount < 0 {
		workerCount = 0
	}
	var workers sync.WaitGroup
	for index := 0; index < workerCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for chunk := range queue {
				path := chunkPath(options.CheckpointPath, chunk.Index)
				err := downloadChunk(ctx, options.Client, source, chunk, path, func(value int64) {
					report(chunk.Index, value)
				})
				if err == nil {
					err = store.mark(chunk, path)
				}
				results <- chunkResult{chunk: chunk, err: err}
			}
		}()
	}
	go func() {
		for _, chunk := range chunks {
			if completed[chunk.Index] {
				continue
			}
			select {
			case queue <- chunk:
			case <-ctx.Done():
				close(queue)
				workers.Wait()
				close(results)
				return
			}
		}
		close(queue)
		workers.Wait()
		close(results)
	}()

	var firstErr error
	for result := range results {
		progressMu.Lock()
		delete(active, result.chunk.Index)
		if result.err == nil {
			baseBytes += result.chunk.Size()
			options.OnChunkComplete(result.chunk.Index, len(chunks), false)
		}
		progressMu.Unlock()
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
		options.OnProgress(baseBytes)
	}
	if firstErr != nil {
		return Result{ResumedChunks: len(completed), TotalChunks: len(chunks)}, firstErr
	}
	if err := ctx.Err(); err != nil {
		return Result{ResumedChunks: len(completed), TotalChunks: len(chunks)}, err
	}
	hash, err := mergeChunks(destination, sortedChunkPaths(options.CheckpointPath, chunks), source.SizeBytes)
	if err != nil {
		return Result{ResumedChunks: len(completed), TotalChunks: len(chunks)}, fmt.Errorf("合并下载分片: %w", err)
	}
	_ = os.Remove(options.CheckpointPath)
	_ = os.RemoveAll(chunksDir(options.CheckpointPath))
	options.OnProgress(source.SizeBytes)
	return Result{SHA256: hash, ResumedChunks: len(completed), TotalChunks: len(chunks)}, nil
}
