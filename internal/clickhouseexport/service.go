package clickhouseexport

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type QueryClient interface {
	QueryCSV(context.Context, string) (io.ReadCloser, error)
}

type taskState struct {
	task      Task
	cancel    context.CancelFunc
	downloads int
}

type Service struct {
	client QueryClient
	spool  string
	mu     sync.RWMutex
	tasks  map[string]*taskState
}

func New(client QueryClient) (*Service, error) {
	return NewAt(client, DefaultSpoolDirectory)
}

func NewAt(client QueryClient, spool string) (*Service, error) {
	if client == nil {
		return nil, fmt.Errorf("ClickHouse query client is required")
	}
	spool = filepath.Clean(strings.TrimSpace(spool))
	if spool == "." || !filepath.IsAbs(spool) {
		return nil, fmt.Errorf("absolute export spool path is required")
	}
	if err := os.MkdirAll(spool, 0o700); err != nil {
		return nil, fmt.Errorf("create export spool: %w", err)
	}
	return &Service{client: client, spool: spool, tasks: make(map[string]*taskState)}, nil
}

func (s *Service) Start(req Request) (Task, error) {
	if _, err := compile(req); err != nil {
		return Task{}, err
	}
	id, err := randomID()
	if err != nil {
		return Task{}, fmt.Errorf("create export id: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	task := Task{ID: id, Status: StatusQueued, Request: req, CreatedAt: time.Now().UTC()}
	s.mu.Lock()
	s.tasks[id] = &taskState{task: task, cancel: cancel}
	s.mu.Unlock()
	go s.run(ctx, id)
	return task, nil
}

func (s *Service) Get(id string) (Task, bool) {
	if !validTaskID(id) {
		return Task{}, false
	}
	s.mu.RLock()
	state, ok := s.tasks[id]
	if !ok {
		s.mu.RUnlock()
		return Task{}, false
	}
	task := state.task
	s.mu.RUnlock()
	return task, true
}

func (s *Service) List() []Task {
	s.mu.RLock()
	tasks := make([]Task, 0, len(s.tasks))
	for _, state := range s.tasks {
		tasks = append(tasks, state.task)
	}
	s.mu.RUnlock()
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt.After(tasks[j].CreatedAt) })
	return tasks
}

func (s *Service) Cancel(id string) error {
	if !validTaskID(id) {
		return ErrTaskNotFound
	}
	s.mu.RLock()
	state, ok := s.tasks[id]
	if !ok {
		s.mu.RUnlock()
		return ErrTaskNotFound
	}
	cancel := state.cancel
	status := state.task.Status
	s.mu.RUnlock()
	if status == StatusCompleted || status == StatusFailed || status == StatusCancelled {
		return nil
	}
	cancel()
	return nil
}

func (s *Service) Open(id string) (io.ReadCloser, Task, error) {
	if !validTaskID(id) {
		return nil, Task{}, ErrTaskNotFound
	}
	s.mu.Lock()
	state, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return nil, Task{}, ErrTaskNotFound
	}
	task := state.task
	if task.Status != StatusCompleted || task.FileName == "" {
		s.mu.Unlock()
		return nil, task, ErrNotReady
	}
	path, err := s.taskPath(task.FileName)
	if err != nil {
		s.mu.Unlock()
		return nil, task, err
	}
	file, err := os.Open(path)
	if err != nil {
		s.mu.Unlock()
		return nil, task, fmt.Errorf("open export file: %w", err)
	}
	state.downloads++
	s.mu.Unlock()
	return &downloadReader{ReadCloser: file, service: s, taskID: id}, task, nil
}

func (s *Service) Remove(id string) error {
	if !validTaskID(id) {
		return ErrTaskNotFound
	}
	s.mu.Lock()
	state, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return ErrTaskNotFound
	}
	if state.task.Status == StatusQueued || state.task.Status == StatusRunning {
		s.mu.Unlock()
		return ErrTaskRunning
	}
	if state.downloads > 0 {
		s.mu.Unlock()
		return ErrDownloadActive
	}
	fileName := state.task.FileName
	if fileName != "" {
		path, err := s.taskPath(fileName)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			s.mu.Unlock()
			return fmt.Errorf("remove export file: %w", err)
		}
	}
	delete(s.tasks, id)
	s.mu.Unlock()
	return nil
}

type downloadReader struct {
	io.ReadCloser
	service *Service
	taskID  string
	once    sync.Once
}

func (r *downloadReader) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(func() {
		r.service.mu.Lock()
		if state, ok := r.service.tasks[r.taskID]; ok && state.downloads > 0 {
			state.downloads--
		}
		r.service.mu.Unlock()
	})
	return err
}

func (s *Service) run(ctx context.Context, id string) {
	started := time.Now().UTC()
	s.update(id, func(task *Task) {
		task.Status = StatusRunning
		task.StartedAt = &started
	})
	task, ok := s.Get(id)
	if !ok {
		return
	}
	query, err := compile(task.Request)
	if err != nil {
		s.finish(id, StatusFailed, "invalid export request")
		return
	}

	stream, err := s.client.QueryCSV(ctx, query.SQL)
	if err != nil {
		if ctx.Err() != nil {
			s.finish(id, StatusCancelled, "")
		} else {
			s.finish(id, StatusFailed, "ClickHouse export query failed")
		}
		return
	}
	defer stream.Close()

	temp, err := os.CreateTemp(s.spool, ".export-*.part")
	if err != nil {
		s.finish(id, StatusFailed, "create export spool file failed")
		return
	}
	tempName := temp.Name()
	published := false
	defer func() {
		_ = temp.Close()
		if !published {
			_ = os.Remove(tempName)
		}
	}()

	header := csv.NewWriter(temp)
	if err := header.Write(query.Columns); err == nil {
		header.Flush()
		err = header.Error()
	}
	if err != nil {
		s.finish(id, StatusFailed, "write export header failed")
		return
	}
	written, err := copyContext(ctx, temp, stream)
	if err != nil {
		if ctx.Err() != nil {
			s.finish(id, StatusCancelled, "")
		} else {
			s.finish(id, StatusFailed, "stream export failed")
		}
		return
	}
	if err := temp.Sync(); err != nil {
		s.finish(id, StatusFailed, "sync export file failed")
		return
	}
	if err := temp.Close(); err != nil {
		s.finish(id, StatusFailed, "close export file failed")
		return
	}
	fileName := id + "-" + string(task.Request.Dataset) + ".csv"
	destination, err := s.taskPath(fileName)
	if err != nil {
		s.finish(id, StatusFailed, "invalid export file path")
		return
	}
	if err := os.Rename(tempName, destination); err != nil {
		s.finish(id, StatusFailed, "publish export file failed")
		return
	}
	published = true
	info, err := os.Stat(destination)
	if err != nil {
		s.finish(id, StatusFailed, "inspect export file failed")
		return
	}
	_ = written
	completed := time.Now().UTC()
	s.update(id, func(current *Task) {
		current.Status = StatusCompleted
		current.FileName = fileName
		current.Bytes = info.Size()
		current.CompletedAt = &completed
		current.Error = ""
	})
}

func (s *Service) finish(id string, status Status, message string) {
	completed := time.Now().UTC()
	s.update(id, func(task *Task) {
		task.Status = status
		task.Error = message
		task.CompletedAt = &completed
	})
}

func (s *Service) update(id string, fn func(*Task)) {
	s.mu.Lock()
	if state, ok := s.tasks[id]; ok {
		fn(&state.task)
	}
	s.mu.Unlock()
}

func (s *Service) taskPath(fileName string) (string, error) {
	if filepath.Base(fileName) != fileName || fileName == "." || fileName == "" {
		return "", fmt.Errorf("invalid export file name")
	}
	path := filepath.Join(s.spool, fileName)
	rel, err := filepath.Rel(s.spool, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("export path escaped spool directory")
	}
	return path, nil
}

func randomID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func validTaskID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func copyContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 128*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			written, writeErr := destination.Write(buffer[:read])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != read {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}
