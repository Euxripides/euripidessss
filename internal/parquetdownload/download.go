package parquetdownload

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func downloadSource(
	ctx context.Context,
	client *http.Client,
	source SourceObject,
	localPath string,
	onProgress func(downloaded int64),
) error {
	return downloadSourceFromURL(ctx, client, sourceHTTPURL(source), source, localPath, onProgress)
}

func downloadSourceFromURL(
	ctx context.Context,
	client *http.Client,
	requestURL string,
	source SourceObject,
	localPath string,
	onProgress func(downloaded int64),
) error {
	if info, err := os.Stat(localPath); err == nil && info.Size() == source.SizeBytes {
		if err := verifyParquetFile(localPath); err == nil {
			onProgress(source.SizeBytes)
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return err
	}
	partial := localPath + ".partial"
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}
		if err := downloadAttempt(ctx, client, requestURL, source, partial, onProgress); err != nil {
			lastErr = err
			if errors.Is(err, context.Canceled) {
				return err
			}
			continue
		}
		info, err := os.Stat(partial)
		if err != nil {
			lastErr = err
			continue
		}
		if info.Size() != source.SizeBytes {
			lastErr = fmt.Errorf("文件大小不一致：得到 %d，预期 %d", info.Size(), source.SizeBytes)
			continue
		}
		if err := verifyParquetFile(partial); err != nil {
			lastErr = err
			continue
		}
		_ = os.Remove(localPath)
		if err := os.Rename(partial, localPath); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("下载失败（已重试 3 次）: %w", lastErr)
}

func downloadAttempt(
	ctx context.Context,
	client *http.Client,
	requestURL string,
	source SourceObject,
	partial string,
	onProgress func(downloaded int64),
) error {
	var offset int64
	if info, err := os.Stat(partial); err == nil {
		offset = info.Size()
		if offset > source.SizeBytes {
			_ = os.Remove(partial)
			offset = 0
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		request.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if offset > 0 && response.StatusCode == http.StatusOK {
		response.Body.Close()
		if err := os.Remove(partial); err != nil && !os.IsNotExist(err) {
			return err
		}
		return downloadAttempt(ctx, client, requestURL, source, partial, onProgress)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(partial, flags, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriterSize(file, 1<<20)
	buffer := make([]byte, 1<<20)
	written := offset
	lastUpdate := time.Time{}
	for {
		count, readErr := response.Body.Read(buffer)
		if count > 0 {
			if _, err := writer.Write(buffer[:count]); err != nil {
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
	if err := writer.Flush(); err != nil {
		return err
	}
	onProgress(written)
	return file.Sync()
}

func verifyParquetFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < 8 {
		return errors.New("Parquet 文件过小")
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(file, header); err != nil {
		return err
	}
	if string(header) != "PAR1" {
		return errors.New("Parquet 文件头校验失败")
	}
	if _, err := file.Seek(-4, io.SeekEnd); err != nil {
		return err
	}
	footer := make([]byte, 4)
	if _, err := io.ReadFull(file, footer); err != nil {
		return err
	}
	if string(footer) != "PAR1" {
		return errors.New("Parquet footer 校验失败")
	}
	return nil
}
