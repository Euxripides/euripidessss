package clickhouseexport

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeQueryClient struct {
	mu      sync.Mutex
	query   string
	factory func(context.Context) io.ReadCloser
	err     error
}

func (f *fakeQueryClient) QueryCSV(ctx context.Context, query string) (io.ReadCloser, error) {
	f.mu.Lock()
	f.query = query
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.factory(ctx), nil
}

type repeatedReader struct {
	remaining int64
	pattern   []byte
}

func (r *repeatedReader) Read(buffer []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := len(buffer)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for i := 0; i < n; i++ {
		buffer[i] = r.pattern[i%len(r.pattern)]
	}
	r.remaining -= int64(n)
	return n, nil
}

type readCloser struct{ io.Reader }

func (readCloser) Close() error { return nil }

func waitTerminal(t *testing.T, service *Service, id string) Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if task, ok := service.Get(id); ok && task.Status != StatusQueued && task.Status != StatusRunning {
			return task
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("export did not reach terminal state")
	return Task{}
}

func TestMultiMegabyteExportStreamsAndPublishesAtomically(t *testing.T) {
	const payloadSize = int64(6 << 20)
	client := &fakeQueryClient{factory: func(context.Context) io.ReadCloser {
		return readCloser{&repeatedReader{remaining: payloadSize, pattern: []byte("1,0xabc\n")}}
	}}
	spool := t.TempDir()
	service, err := NewAt(client, spool)
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Start(Request{Dataset: DatasetTransactions, Columns: []string{"chain_id", "tx_hash"}, Filter: Filter{ChainID: 56}})
	if err != nil {
		t.Fatal(err)
	}
	task = waitTerminal(t, service, task.ID)
	if task.Status != StatusCompleted {
		t.Fatalf("unexpected task: %+v", task)
	}
	if task.Bytes < payloadSize {
		t.Fatalf("export too small: %d", task.Bytes)
	}
	entries, err := os.ReadDir(spool)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || strings.HasSuffix(entries[0].Name(), ".part") {
		t.Fatalf("expected one published file, got %v", entries)
	}
	stream, opened, err := service.Open(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	header := make([]byte, len("chain_id,tx_hash\n"))
	if _, err := io.ReadFull(stream, header); err != nil {
		t.Fatal(err)
	}
	if string(header) != "chain_id,tx_hash\n" || opened.FileName != task.FileName {
		t.Fatalf("unexpected published export: header=%q task=%+v", header, opened)
	}
	if err := service.Remove(task.ID); !errors.Is(err, ErrDownloadActive) {
		t.Fatalf("active download was removable: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := service.Remove(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(spool, task.FileName)); !os.IsNotExist(err) {
		t.Fatalf("published file still exists: %v", err)
	}
}

func TestTraversalAndUnknownTaskIDsAreRejected(t *testing.T) {
	client := &fakeQueryClient{factory: func(context.Context) io.ReadCloser { return readCloser{strings.NewReader("")} }}
	service, err := NewAt(client, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../secret", `..\secret`, strings.Repeat("a", 31), strings.Repeat("z", 32)} {
		if _, _, err := service.Open(id); !errors.Is(err, ErrTaskNotFound) {
			t.Fatalf("Open(%q) error = %v", id, err)
		}
		if err := service.Remove(id); !errors.Is(err, ErrTaskNotFound) {
			t.Fatalf("Remove(%q) error = %v", id, err)
		}
	}
	if _, err := service.taskPath("../escape.csv"); err == nil {
		t.Fatal("taskPath accepted traversal")
	}
}

type cancelReader struct{ ctx context.Context }

func (r *cancelReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	case <-time.After(2 * time.Millisecond):
		copy(buffer, "1,tx\n")
		return len("1,tx\n"), nil
	}
}

func (*cancelReader) Close() error { return nil }

func TestCancelRemovesPartialFile(t *testing.T) {
	client := &fakeQueryClient{factory: func(ctx context.Context) io.ReadCloser { return &cancelReader{ctx: ctx} }}
	spool := t.TempDir()
	service, err := NewAt(client, spool)
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Start(Request{Dataset: DatasetTransactions, Filter: Filter{ChainID: 56}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, _ := service.Get(task.ID)
		if current.Status == StatusRunning {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := service.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	task = waitTerminal(t, service, task.ID)
	if task.Status != StatusCancelled {
		t.Fatalf("unexpected status: %+v", task)
	}
	entries, err := os.ReadDir(spool)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial files remained: %v", entries)
	}
}

func TestQueryFailureDoesNotExposeBackendError(t *testing.T) {
	client := &fakeQueryClient{err: errors.New("password=super-secret")}
	service, err := NewAt(client, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Start(Request{Dataset: DatasetBlocks, Filter: Filter{ChainID: 1}})
	if err != nil {
		t.Fatal(err)
	}
	task = waitTerminal(t, service, task.ID)
	if task.Status != StatusFailed || strings.Contains(task.Error, "super-secret") {
		t.Fatalf("backend error leaked: %+v", task)
	}
}
