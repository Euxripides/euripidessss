package cryptodownload

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

type csvIMAPDialFunc func(context.Context, CSVMailConfig) (*client.Client, error)

type csvMailFolder struct {
	Name  string
	Alias string
}

type csvMailError struct {
	Status csvMailStatus
	Op     string
	Err    error
}

const csvIMAPBaselineAttempts = 3

func (e *csvMailError) Error() string {
	return fmt.Sprintf("csv mail %s (%s): %v", e.Status, e.Op, e.Err)
}

func (e *csvMailError) Unwrap() error { return e.Err }

type csvMailWatcher struct {
	config      CSVMailConfig
	dial        csvIMAPDialFunc
	client      *client.Client
	folders     []csvMailFolder
	folderSkips []csvMailFolderSkip
	baselines   map[string]uint32
	wait        func(context.Context, time.Duration) error
	now         func() time.Time
}

func newCSVMailWatcher(config CSVMailConfig) *csvMailWatcher {
	return &csvMailWatcher{
		config:    config,
		dial:      dialCSVIMAPTLS,
		baselines: make(map[string]uint32),
		wait:      waitCSVIMAPBackoff,
		now:       time.Now,
	}
}

func (w *csvMailWatcher) CaptureBaselines(ctx context.Context) error {
	var lastErr error
	for attempt := 0; attempt < csvIMAPBaselineAttempts; attempt++ {
		w.disconnect()
		err := w.captureBaselinesOnce(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		w.disconnect()
		var mailErr *csvMailError
		if errors.As(err, &mailErr) && mailErr.Status == csvMailLoginConfigFailure {
			return err
		}
		if attempt+1 < csvIMAPBaselineAttempts {
			if waitErr := w.wait(ctx, csvMailReconnectDelay(attempt)); waitErr != nil {
				return waitErr
			}
		}
	}
	return lastErr
}

func (w *csvMailWatcher) captureBaselinesOnce(ctx context.Context) error {
	if err := w.connect(ctx); err != nil {
		return err
	}
	folders, err := w.discoverFolders(ctx)
	if err != nil {
		return fmt.Errorf("discover folders: %w", err)
	}
	baselines := make(map[string]uint32, len(folders))
	w.folders = folders
	for _, folder := range folders {
		var status *imap.MailboxStatus
		if err := w.command(ctx, func() error {
			var selectErr error
			status, selectErr = w.client.Select(folder.Name, true)
			return selectErr
		}); err != nil {
			return fmt.Errorf("examine %s: %w", folder.Alias, err)
		}
		if status.UidNext > 0 {
			baselines[folder.Name] = status.UidNext - 1
		} else {
			baselines[folder.Name] = 0
		}
	}
	w.baselines = baselines
	return nil
}

func (w *csvMailWatcher) KeepAlive(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.connect(ctx); err != nil {
		return err
	}
	if err := w.command(ctx, w.client.Noop); err != nil {
		w.disconnect()
		return fmt.Errorf("imap noop: %w", err)
	}
	return nil
}

func (w *csvMailWatcher) Close() error {
	if w.client == nil {
		return nil
	}
	cli := w.client
	w.client = nil
	if err := cli.Logout(); err != nil {
		_ = cli.Terminate()
		return fmt.Errorf("imap logout: %w", err)
	}
	return nil
}

func (w *csvMailWatcher) disconnect() {
	if w.client != nil {
		_ = w.client.Terminate()
		w.client = nil
	}
}

func (w *csvMailWatcher) connect(ctx context.Context) error {
	if w.client != nil {
		return nil
	}
	if strings.TrimSpace(w.config.Host) == "" || strings.TrimSpace(w.config.Username) == "" || w.config.Password == "" {
		return &csvMailError{Status: csvMailLoginConfigFailure, Op: "config", Err: errors.New("incomplete IMAP configuration")}
	}
	cli, err := w.dial(ctx, w.config)
	if err != nil {
		return &csvMailError{Status: csvMailReconnecting, Op: "dial", Err: err}
	}
	cli.Timeout = csvIMAPCommandTimeout
	w.client = cli
	if err := w.command(ctx, func() error { return cli.Login(w.config.Username, w.config.Password) }); err != nil {
		w.disconnect()
		if csvIMAPLoginErrorIsTransient(err) {
			return &csvMailError{Status: csvMailReconnecting, Op: "login", Err: err}
		}
		return &csvMailError{Status: csvMailLoginConfigFailure, Op: "login", Err: err}
	}
	return nil
}

func csvIMAPLoginErrorIsTransient(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(lower, "connection closed") ||
		strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "unexpected eof") ||
		strings.Contains(lower, "i/o timeout")
}

func (w *csvMailWatcher) command(ctx context.Context, operation func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- operation() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		w.disconnect()
		<-done
		return ctx.Err()
	}
}
