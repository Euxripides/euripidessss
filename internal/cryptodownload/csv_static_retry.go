package cryptodownload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type csvDownloadProgress func(reason string, attempt int, delay time.Duration)

type csvStaticFailure uint8

const (
	csvStaticRetryable csvStaticFailure = iota + 1
	csvStaticUnsafeRange
	csvStaticInvalid
	csvStaticTerminal
)

var errCSVStaticNotReady = errors.New("static CSV object not ready")

type csvStaticTransferError struct {
	kind   csvStaticFailure
	status int
	err    error
}

func (e *csvStaticTransferError) Error() string {
	if e.status != 0 {
		return fmt.Sprintf("static CSV transfer HTTP %d: %v", e.status, e.err)
	}
	return fmt.Sprintf("static CSV transfer: %v", e.err)
}

func (e *csvStaticTransferError) Unwrap() error { return e.err }

type csvStaticPolicy struct {
	rangeThreshold int64
	chunkSize      int64
	initialWorkers int
	maxWorkers     int
	rangeAttempts  int
	singleAttempts int
}

func defaultCSVStaticPolicy() csvStaticPolicy {
	return csvStaticPolicy{
		rangeThreshold: csvStaticRangeThreshold,
		chunkSize:      256 << 10,
		initialWorkers: 2,
		maxWorkers:     6,
		rangeAttempts:  4,
		singleAttempts: 3,
	}
}

func newCSVStaticError(kind csvStaticFailure, status int, err error) error {
	return &csvStaticTransferError{kind: kind, status: status, err: err}
}

func csvStaticStatusError(status int, _ []byte) error {
	switch {
	case status == http.StatusTooManyRequests, status >= 500:
		return newCSVStaticError(csvStaticRetryable, status, fmt.Errorf("upstream rejected transfer"))
	case status == http.StatusRequestedRangeNotSatisfiable:
		return newCSVStaticError(csvStaticUnsafeRange, status, fmt.Errorf("range rejected"))
	default:
		return newCSVStaticError(csvStaticTerminal, status, fmt.Errorf("unexpected response"))
	}
}

func csvStaticCanRetry(err error) bool {
	if errors.Is(err, errCSVStaticNotReady) {
		return true
	}
	var transferErr *csvStaticTransferError
	return errors.As(err, &transferErr) && transferErr.kind == csvStaticRetryable
}

func csvStaticFallsBackToSingle(err error) bool {
	var transferErr *csvStaticTransferError
	return errors.As(err, &transferErr) && (transferErr.kind == csvStaticRetryable || transferErr.kind == csvStaticUnsafeRange)
}

func csvStaticClassifyReadError(err error) error {
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return newCSVStaticError(csvStaticRetryable, 0, err)
	}
	return newCSVStaticError(csvStaticRetryable, 0, err)
}

func csvWaitDownloadRetry(ctx context.Context, progress csvDownloadProgress, reason string, attempt int, delay time.Duration) error {
	if progress != nil {
		progress(reason, attempt+1, delay)
	}
	return csvWaitRetry(ctx, delay)
}

func csvWaitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func csvStaticRangeRetryDelay(attempt int) time.Duration {
	delay := 100 * time.Millisecond * time.Duration(1<<min(attempt, 3))
	return min(delay, 800*time.Millisecond)
}

func csvDownloadRetryDelay(attempt int) time.Duration {
	if attempt < 6 {
		return 3 * time.Second
	}
	if attempt < 12 {
		return 5 * time.Second
	}
	return 15 * time.Second
}
