package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var ErrRangeUnsupported = errors.New("服务器不支持 Range 并行下载")

func downloadChunk(
	ctx context.Context,
	client *http.Client,
	source Source,
	chunk Chunk,
	path string,
	onProgress func(int64),
) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}
		err := downloadChunkAttempt(ctx, client, source, chunk, path, onProgress)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrRangeUnsupported) || errors.Is(err, context.Canceled) {
			return err
		}
		lastErr = err
		onProgress(0)
	}
	return fmt.Errorf("Chunk %d 下载失败（已重试3次）: %w", chunk.Index, lastErr)
}

func downloadChunkAttempt(
	ctx context.Context,
	client *http.Client,
	source Source,
	chunk Chunk,
	path string,
	onProgress func(int64),
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("Range", "bytes="+strconv.FormatInt(chunk.Start, 10)+"-"+strconv.FormatInt(chunk.End, 10))
	if source.ETag != "" {
		request.Header.Set("If-Match", source.ETag)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		return ErrRangeUnsupported
	}
	if response.StatusCode != http.StatusPartialContent {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	if etag := response.Header.Get("ETag"); source.ETag != "" && etag != "" &&
		strings.TrimSpace(etag) != strings.TrimSpace(source.ETag) {
		return fmt.Errorf("ETag 已变化：得到 %s，预期 %s", etag, source.ETag)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	buffer := make([]byte, 1<<20)
	var written int64
	lastUpdate := time.Time{}
	for {
		count, readErr := response.Body.Read(buffer)
		if count > 0 {
			if _, err := file.Write(buffer[:count]); err != nil {
				return err
			}
			written += int64(count)
			if time.Since(lastUpdate) >= 250*time.Millisecond {
				onProgress(written)
				lastUpdate = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	if written != chunk.Size() {
		return fmt.Errorf("Chunk %d 大小不一致：得到 %d，预期 %d", chunk.Index, written, chunk.Size())
	}
	if err := file.Sync(); err != nil {
		return err
	}
	onProgress(written)
	return nil
}
