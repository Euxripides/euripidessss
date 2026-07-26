package cryptodownload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

type csvStaticTemp struct {
	file   *os.File
	path   string
	target string
	closed bool
}

func newCSVStaticTemp(target string) (*csvStaticTemp, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("create static CSV target directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".static-csv-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create static CSV temp file: %w", err)
	}
	return &csvStaticTemp{file: file, path: file.Name(), target: target}, nil
}

func (t *csvStaticTemp) cleanup() {
	if !t.closed {
		_ = t.file.Close()
		t.closed = true
	}
	_ = os.Remove(t.path)
}

func (t *csvStaticTemp) publish(expectedSize int64, expectedSHA256 string) error {
	if err := t.file.Sync(); err != nil {
		return fmt.Errorf("sync static CSV temp file: %w", err)
	}
	info, err := t.file.Stat()
	if err != nil {
		return fmt.Errorf("stat static CSV temp file: %w", err)
	}
	if expectedSize >= 0 && info.Size() != expectedSize {
		return newCSVStaticError(csvStaticInvalid, 0, fmt.Errorf("static CSV size %d, want %d", info.Size(), expectedSize))
	}
	if expectedSHA256 != "" {
		if _, err := t.file.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("seek static CSV temp file: %w", err)
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, t.file); err != nil {
			return fmt.Errorf("hash static CSV temp file: %w", err)
		}
		if got := hex.EncodeToString(hash.Sum(nil)); got != expectedSHA256 {
			return newCSVStaticError(csvStaticInvalid, 0, fmt.Errorf("static CSV SHA-256 mismatch"))
		}
	}
	if err := t.file.Close(); err != nil {
		return fmt.Errorf("close static CSV temp file: %w", err)
	}
	t.closed = true
	if err := os.Rename(t.path, t.target); err != nil {
		return fmt.Errorf("publish static CSV atomically: %w", err)
	}
	return nil
}

func downloadCSVStaticSingleToPath(ctx context.Context, client *http.Client, observer requestTimingObserver, link, target string, policy csvStaticPolicy) (string, error) {
	var lastErr error
	for attempt := 0; attempt < policy.singleAttempts; attempt++ {
		temp, err := newCSVStaticTemp(target)
		if err != nil {
			return "", err
		}
		filename, err := streamCSVStaticSingle(ctx, client, observer, link, temp)
		temp.cleanup()
		if err == nil {
			return filename, nil
		}
		lastErr = err
		if ctx.Err() != nil || !csvStaticCanRetry(err) || attempt+1 == policy.singleAttempts {
			return "", err
		}
		if err := csvWaitRetry(ctx, csvStaticRangeRetryDelay(attempt)); err != nil {
			return "", err
		}
	}
	return "", lastErr
}

func streamCSVStaticSingle(ctx context.Context, client *http.Client, observer requestTimingObserver, link string, temp *csvStaticTemp) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", newCSVStaticError(csvStaticInvalid, 0, err)
	}
	setCSVStaticHeaders(req)
	resp, err := doHTTPRequest(client, req, observer)
	if err != nil {
		return "", newCSVStaticError(csvStaticRetryable, 0, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		prefix, _ := io.ReadAll(io.LimitReader(resp.Body, csvStaticProbeLimit))
		resp.Body.Close()
		if isNoSuchKeyPayload(prefix) {
			return "", errCSVStaticNotReady
		}
		return "", csvStaticStatusError(resp.StatusCode, prefix)
	}
	_, copyErr := io.Copy(temp.file, resp.Body)
	closeErr := resp.Body.Close()
	if copyErr != nil {
		return "", csvStaticClassifyReadError(copyErr)
	}
	if closeErr != nil {
		return "", csvStaticClassifyReadError(closeErr)
	}
	if err := validateCSVStaticTempPayload(temp.file); err != nil {
		return "", err
	}
	expectedSize := resp.ContentLength
	if expectedSize == 0 && resp.Header.Get("Content-Length") == "" {
		expectedSize = -1
	}
	if err := temp.publish(expectedSize, csvStaticSHA256(resp.Header)); err != nil {
		return "", err
	}
	return csvDownloadFilename(resp.Header), nil
}

func validateCSVStaticTempPayload(file *os.File) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek static CSV payload: %w", err)
	}
	prefix := make([]byte, csvStaticProbeLimit)
	n, err := file.Read(prefix)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read static CSV payload prefix: %w", err)
	}
	return validateCSVStaticPayload(prefix[:n])
}

func validateCSVStaticPayload(body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return newCSVStaticError(csvStaticInvalid, 0, fmt.Errorf("static CSV payload is empty"))
	}
	if isNoSuchKeyPayload(body) {
		return errCSVStaticNotReady
	}
	if csvStaticLooksHTML(body) {
		return newCSVStaticError(csvStaticInvalid, 0, fmt.Errorf("static CSV payload is HTML"))
	}
	return nil
}
