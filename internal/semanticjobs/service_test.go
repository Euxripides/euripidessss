package semanticjobs

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type fakeRunner struct {
	mu            sync.Mutex
	reparseCalls  int
	reenrichCalls int
	last          Job
	block         bool
	fail          error
}

func (r *fakeRunner) Reparse(ctx context.Context, job Job, report ProgressReporter) error {
	r.mu.Lock()
	r.reparseCalls++
	r.last = job
	block, fail := r.block, r.fail
	r.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	if fail != nil {
		return fail
	}
	return report(Progress{Completed: job.Progress.Total, Total: job.Progress.Total, LastBlock: job.Request.EndBlock})
}

func (r *fakeRunner) Reenrich(ctx context.Context, job Job, report ProgressReporter) error {
	r.mu.Lock()
	r.reenrichCalls++
	r.last = job
	block, fail := r.block, r.fail
	r.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	if fail != nil {
		return fail
	}
	return report(Progress{Completed: job.Progress.Total, Total: job.Progress.Total, LastBlock: job.Request.EndBlock})
}

func (r *fakeRunner) calls() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reparseCalls, r.reenrichCalls
}

func newTestService(t *testing.T, runner Runner) (*Service, *FileStore) {
	t.Helper()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, runner)
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}

func waitStatus(t *testing.T, service *Service, id string, want Status) Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, err := service.Get(id)
		if err == nil && job.Status == want {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, _ := service.Get(id)
	t.Fatalf("job %s status = %s, want %s", id, job.Status, want)
	return Job{}
}

func TestValidationStrictWhitelistsAndScopes(t *testing.T) {
	valid := Request{Type: JobTypeReparse, Chain: " BSC ", StartBlock: 10, EndBlock: 20, Dataset: "Transactions", ParserVersion: "v3.1"}
	normalized, err := NormalizeAndValidate(valid)
	if err != nil || normalized.Chain != "bsc" || normalized.Dataset != "transactions" {
		t.Fatalf("valid request was not normalized: %+v, %v", normalized, err)
	}
	cases := []Request{
		{Type: "DOWNLOAD", Chain: "bsc", Dataset: "transactions", StartBlock: 1, EndBlock: 2},
		{Type: JobTypeReparse, Chain: "../bsc", Dataset: "transactions", StartBlock: 1, EndBlock: 2, ParserVersion: "v3"},
		{Type: JobTypeReparse, Chain: "bsc", Dataset: "unknown", StartBlock: 1, EndBlock: 2, ParserVersion: "v3"},
		{Type: JobTypeReparse, Chain: "bsc", Dataset: "transactions", StartBlock: 2, EndBlock: 1, ParserVersion: "v3"},
		{Type: JobTypeReparse, Chain: "bsc", Dataset: "transactions", StartBlock: 1, EndBlock: 2},
		{Type: JobTypeReparse, Chain: "bsc", Dataset: "transactions", StartBlock: 1, EndBlock: 2, ParserVersion: "v3", Enrichments: []string{"entity_labels"}},
		{Type: JobTypeReenrich, Chain: "bsc", Dataset: "transactions", StartBlock: 1, EndBlock: 2},
		{Type: JobTypeReenrich, Chain: "bsc", Dataset: "transactions", StartBlock: 1, EndBlock: 2, ParserVersion: "v3", Enrichments: []string{"entity_labels"}},
		{Type: JobTypeReenrich, Chain: "bsc", Dataset: "transactions", StartBlock: 1, EndBlock: 2, Enrichments: []string{"download_again"}},
	}
	for i, request := range cases {
		if _, err := NormalizeAndValidate(request); err == nil {
			t.Errorf("case %d unexpectedly accepted: %+v", i, request)
		}
	}
}

func TestStateMachineRejectsTerminalAndInvalidTransitions(t *testing.T) {
	allowed := [][2]Status{
		{StatusQueued, StatusRunning}, {StatusQueued, StatusCancelled},
		{StatusRunning, StatusQueued}, {StatusRunning, StatusCompleted},
		{StatusRunning, StatusFailed}, {StatusRunning, StatusCancelled},
		{StatusFailed, StatusQueued},
	}
	for _, pair := range allowed {
		if !CanTransition(pair[0], pair[1]) {
			t.Errorf("expected transition %s -> %s", pair[0], pair[1])
		}
	}
	for _, terminal := range []Status{StatusCompleted, StatusCancelled} {
		for _, target := range []Status{StatusQueued, StatusRunning, StatusFailed, StatusCancelled, StatusCompleted} {
			if CanTransition(terminal, target) {
				t.Errorf("terminal transition unexpectedly allowed: %s -> %s", terminal, target)
			}
		}
	}
	job := Job{Status: StatusQueued}
	if err := transition(&job, StatusCompleted); err == nil || job.Status != StatusQueued {
		t.Fatalf("invalid transition mutated job: %+v, %v", job, err)
	}
}

func TestReenrichmentUsesOnlyInjectedSemanticRunner(t *testing.T) {
	runner := &fakeRunner{}
	service, _ := newTestService(t, runner)
	defer service.Close()
	job, created, err := service.Submit(Request{Type: JobTypeReenrich, Chain: "eth", StartBlock: 100, EndBlock: 105, Dataset: "address_activity", Enrichments: []string{"entity_labels", "entity_labels"}})
	if err != nil || !created {
		t.Fatalf("submit: created=%v err=%v", created, err)
	}
	completed := waitStatus(t, service, job.ID, StatusCompleted)
	reparse, reenrich := runner.calls()
	if reparse != 0 || reenrich != 1 {
		t.Fatalf("runner calls reparse=%d reenrich=%d", reparse, reenrich)
	}
	if len(completed.Request.Enrichments) != 1 || completed.Request.Enrichments[0] != "entity_labels" {
		t.Fatalf("enrichments not canonicalized: %+v", completed.Request.Enrichments)
	}
}

func TestSubmissionIsIdempotentAcrossRestart(t *testing.T) {
	runner := &fakeRunner{}
	service, store := newTestService(t, runner)
	req := Request{Type: JobTypeReparse, Chain: "bsc", StartBlock: 7, EndBlock: 9, Dataset: "transactions", ParserVersion: "v3"}
	first, created, err := service.Submit(req)
	if err != nil || !created {
		t.Fatal(err)
	}
	waitStatus(t, service, first.ID, StatusCompleted)
	second, created, err := service.Submit(req)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("same service idempotency failed: %+v created=%v err=%v", second, created, err)
	}
	service.Close()
	restarted, err := NewService(store, runner)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	third, created, err := restarted.Submit(req)
	if err != nil || created || third.ID != first.ID {
		t.Fatalf("restart idempotency failed: %+v created=%v err=%v", third, created, err)
	}
	reparse, _ := runner.calls()
	if reparse != 1 {
		t.Fatalf("idempotent submission reran parser %d times", reparse)
	}
}

func TestCancelTransitionsAndPersists(t *testing.T) {
	runner := &fakeRunner{block: true}
	service, store := newTestService(t, runner)
	job, _, err := service.Submit(Request{Type: JobTypeReparse, Chain: "bsc", StartBlock: 1, EndBlock: 3, Dataset: "logs", ParserVersion: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, service, job.ID, StatusRunning)
	cancelled, err := service.Cancel(job.ID)
	if err != nil || cancelled.Status != StatusCancelled {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	persisted, err := store.Get(job.ID)
	if err != nil || persisted.Status != StatusCancelled {
		t.Fatalf("persisted cancel: %+v %v", persisted, err)
	}
	service.Close()
}

func TestRecoverInterruptedJobWithoutChangingScope(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Type: JobTypeReparse, Chain: "polygon", StartBlock: 500, EndBlock: 550, Dataset: "token_transfers", ParserVersion: "v3"}
	id, key, _ := identity(req)
	now := time.Now().UTC()
	interrupted := Job{ID: id, IdempotencyKey: key, Request: req, Status: StatusRunning, Progress: Progress{Completed: 10, Total: 51, LastBlock: 509}, Attempts: 1, CreatedAt: now, UpdatedAt: now, StartedAt: &now}
	if err := store.Save(interrupted); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	service, err := NewService(store, runner)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err := service.Recover(); err != nil {
		t.Fatal(err)
	}
	completed := waitStatus(t, service, id, StatusCompleted)
	if completed.RecoveryCount != 1 || completed.Attempts != 2 {
		t.Fatalf("recovery metadata: %+v", completed)
	}
	if !reflect.DeepEqual(completed.Request, req) {
		t.Fatalf("recovery changed immutable scope: got %+v want %+v", completed.Request, req)
	}
}

func TestCloseThenRestartRecoversDurableRunningJob(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	firstRunner := &fakeRunner{block: true}
	first, err := NewService(store, firstRunner)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := first.Submit(Request{Type: JobTypeReparse, Chain: "eth", StartBlock: 800, EndBlock: 810, Dataset: "parsed_events", ParserVersion: "v5"})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, first, job.ID, StatusRunning)
	first.Close()
	persisted, err := store.Get(job.ID)
	if err != nil || persisted.Status != StatusRunning {
		t.Fatalf("shutdown must leave recoverable RUNNING state: %+v, %v", persisted, err)
	}

	secondRunner := &fakeRunner{}
	second, err := NewService(store, secondRunner)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.Recover(); err != nil {
		t.Fatal(err)
	}
	completed := waitStatus(t, second, job.ID, StatusCompleted)
	if completed.RecoveryCount != 1 || completed.Attempts != 2 || completed.Request.ParserVersion != "v5" {
		t.Fatalf("unexpected restarted job: %+v", completed)
	}
	if reparse, reenrich := secondRunner.calls(); reparse != 1 || reenrich != 0 {
		t.Fatalf("restart called wrong runner: reparse=%d reenrich=%d", reparse, reenrich)
	}
}

func TestFailedJobCanRetryWithSameIdentity(t *testing.T) {
	runner := &fakeRunner{fail: errors.New("parser unavailable")}
	service, _ := newTestService(t, runner)
	defer service.Close()
	job, _, err := service.Submit(Request{Type: JobTypeReparse, Chain: "bsc", StartBlock: 1, EndBlock: 1, Dataset: "traces", ParserVersion: "v4"})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, service, job.ID, StatusFailed)
	runner.mu.Lock()
	runner.fail = nil
	runner.mu.Unlock()
	retried, err := service.Retry(job.ID)
	if err != nil || retried.ID != job.ID {
		t.Fatalf("retry: %+v %v", retried, err)
	}
	completed := waitStatus(t, service, job.ID, StatusCompleted)
	if completed.Attempts != 2 {
		t.Fatalf("attempts=%d", completed.Attempts)
	}
}

func TestFileStoreAtomicUpdateAndTamperDetection(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Type: JobTypeReparse, Chain: "bsc", StartBlock: 40, EndBlock: 42, Dataset: "parsed_events", ParserVersion: "v3"}
	id, key, _ := identity(req)
	now := time.Now().UTC()
	job := Job{ID: id, IdempotencyKey: key, Request: req, Status: StatusQueued, Progress: Progress{Total: 3}, CreatedAt: now, UpdatedAt: now}
	if err := store.Save(job); err != nil {
		t.Fatal(err)
	}
	job.Status = StatusRunning
	job.Attempts = 1
	if err := store.Save(job); err != nil {
		t.Fatalf("atomic replacement failed: %v", err)
	}
	loaded, err := store.Get(id)
	if err != nil || loaded.Status != StatusRunning || loaded.Attempts != 1 {
		t.Fatalf("updated state not durable: %+v, %v", loaded, err)
	}
	loaded.Request.EndBlock = 99 // identity no longer matches immutable scope
	if err := store.Save(loaded); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(store, &fakeRunner{}); err == nil {
		t.Fatal("tampered persisted scope was accepted")
	}
}
