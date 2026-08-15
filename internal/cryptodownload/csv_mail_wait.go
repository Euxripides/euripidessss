package cryptodownload

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type csvMailWaitProgress func(elapsed time.Duration, lastErr error)

func (c *CSVExportClient) prepareCSVEmailRequest(ctx context.Context, watcher **csvMailWatcher) (time.Time, error) {
	if *watcher == nil {
		*watcher = c.newMailWatcher(c.activeMail())
	}
	if err := (*watcher).CaptureBaselines(ctx); err != nil {
		return time.Time{}, fmt.Errorf("capture IMAP UID baseline: %w", err)
	}
	return (*watcher).now(), nil
}

func (c *CSVExportClient) waitForLink(ctx context.Context, watcher *csvMailWatcher, requestedAt time.Time, seenMu *sync.Mutex, seenLinks map[string]bool, kindName, address string, timeout time.Duration, progress csvMailWaitProgress) (string, error) {
	startedAt := watcher.now()
	deadline := startedAt.Add(timeout)
	nextProgress := startedAt.Add(csvEmailWaitProgressEvery)
	lastStatus := csvMailNotArrived
	var lastErr error
	for {
		request := csvMailRequest{
			RequestedAt: requestedAt,
			Kind:        kindName,
			Address:     address,
			RequestSent: true,
			SeenLinks:   snapshotCSVSeenLinks(seenMu, seenLinks),
		}
		observation, err := watcher.Scan(ctx, request)
		lastStatus = observation.Status
		lastErr = err
		if err == nil && observation.Status == csvMailMatched {
			markCSVLinkSeen(seenMu, seenLinks, observation.Link)
			return observation.Link, nil
		}
		var mailErr *csvMailError
		if errors.As(err, &mailErr) && mailErr.Status == csvMailLoginConfigFailure {
			return "", err
		}
		now := watcher.now()
		if progress != nil && !now.Before(nextProgress) {
			progress(now.Sub(startedAt), csvMailProgressError(lastStatus, lastErr))
			nextProgress = now.Add(csvEmailWaitProgressEvery)
		}
		if !now.Before(deadline) {
			return "", &csvMailError{Status: lastStatus, Op: "wait", Err: fmt.Errorf("%w: %s", errCSVEmailTimeout, kindName)}
		}
		if keepaliveErr := watcher.KeepAlive(ctx); keepaliveErr != nil {
			lastStatus = csvMailReconnecting
			lastErr = keepaliveErr
		}
		if err := watcher.wait(ctx, 3*time.Second); err != nil {
			return "", err
		}
	}
}

func snapshotCSVSeenLinks(seenMu *sync.Mutex, seenLinks map[string]bool) map[string]bool {
	seenMu.Lock()
	defer seenMu.Unlock()
	snapshot := make(map[string]bool, len(seenLinks))
	for link := range seenLinks {
		snapshot[link] = true
	}
	return snapshot
}

func csvMailProgressError(status csvMailStatus, err error) error {
	if err != nil {
		return &csvMailError{Status: status, Op: "poll", Err: err}
	}
	return &csvMailError{Status: status, Op: "poll", Err: errors.New("no matching ready link")}
}

func csvMailSearchSince(requestedAt time.Time) time.Time {
	return requestedAt.Add(-15 * time.Minute)
}

func csvMailReceivedAfterRequest(receivedAt, requestedAt time.Time) bool {
	return !receivedAt.IsZero() && !receivedAt.Add(csvMailTimestampTolerance).Before(requestedAt)
}

func isCSVEmailNoLinkTimeout(err error) bool {
	if !errors.Is(err, errCSVEmailTimeout) {
		return false
	}
	var mailErr *csvMailError
	if errors.As(err, &mailErr) {
		switch mailErr.Status {
		case csvMailNotArrived, csvMailNotMatched, csvMailLinkNotReady:
			return true
		case csvMailRequestNotSent, csvMailLoginConfigFailure, csvMailReconnecting, csvMailMatched:
			return false
		}
	}
	lower := strings.ToLower(err.Error())
	return !strings.Contains(lower, "login") && !strings.Contains(lower, "connection")
}

func markCSVLinkSeen(seenMu *sync.Mutex, seenLinks map[string]bool, link string) {
	seenMu.Lock()
	seenLinks[link] = true
	seenMu.Unlock()
}

func csvMailRequestNotSentError(err error) error {
	return &csvMailError{Status: csvMailRequestNotSent, Op: "request", Err: err}
}
