package cryptodownload

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	csvDownloadReadyTimeout  = 4 * time.Minute
	csvStaticTransferTimeout = 10 * time.Minute
	csvStaticRangeThreshold  = 1 << 20
)

func (c *CSVExportClient) downloadLink(ctx context.Context, link string) ([]byte, string, error) {
	return c.downloadLinkWithProgress(ctx, link, nil)
}

func (c *CSVExportClient) downloadLinkWithProgress(ctx context.Context, link string, progress csvDownloadProgress) ([]byte, string, error) {
	client := &http.Client{
		Transport:     c.httpClient.Transport,
		CheckRedirect: c.httpClient.CheckRedirect,
		Jar:           c.httpClient.Jar,
	}
	deadline := time.Now().Add(csvDownloadReadyTimeout)
	for attempt := 0; ; attempt++ {
		body, filename, err := c.downloadCSVStaticAttempt(ctx, client, link)
		if err == nil {
			return body, filename, nil
		}
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		if !csvStaticCanRetry(err) || time.Now().After(deadline) {
			return nil, "", err
		}
		reason := "静态CSV传输暂时失败"
		if errors.Is(err, errCSVStaticNotReady) {
			reason = "CSV文件尚未生成"
		}
		if waitErr := csvWaitDownloadRetry(ctx, progress, reason, attempt, csvDownloadRetryDelay(attempt)); waitErr != nil {
			return nil, "", waitErr
		}
	}
}

func (c *CSVExportClient) downloadCSVStaticAttempt(ctx context.Context, client *http.Client, link string) ([]byte, string, error) {
	transferCtx, cancel := context.WithTimeout(ctx, csvStaticTransferTimeout)
	defer cancel()
	probe, err := probeCSVStaticObject(transferCtx, client, c.timingObserver, link)
	if err != nil {
		return nil, "", err
	}
	dir, err := os.MkdirTemp("", "wallet-exporter-static-*")
	if err != nil {
		return nil, "", fmt.Errorf("create static CSV workspace: %w", err)
	}
	defer os.RemoveAll(dir)
	target := filepath.Join(dir, "published.csv")
	filename, err := downloadCSVStaticToPathWithFilename(transferCtx, client, c.timingObserver, link, probe.object, target, defaultCSVStaticPolicy())
	if err != nil {
		return nil, "", err
	}
	body, err := os.ReadFile(target)
	if err != nil {
		return nil, "", fmt.Errorf("read published static CSV: %w", err)
	}
	if err := validateCSVStaticPayload(body); err != nil {
		return nil, "", err
	}
	return body, filename, nil
}
