package downloadengine

import "testing"

func newTestJob() *Job {
	return &Job{
		ID:     "test-001",
		Type:   JobAddressSingle,
		Status: StatusCreated,
		Stage:  StageIdle,
	}
}

func TestTransitionLegalPath(t *testing.T) {
	j := newTestJob()

	// CREATED → VALIDATING
	if err := j.Transition(StatusValidating); err != nil {
		t.Fatalf("CREATED→VALIDATING should succeed: %v", err)
	}
	// VALIDATING → QUEUED
	if err := j.Transition(StatusQueued); err != nil {
		t.Fatalf("VALIDATING→QUEUED should succeed: %v", err)
	}
	// QUEUED → RUNNING
	if err := j.Transition(StatusRunning); err != nil {
		t.Fatalf("QUEUED→RUNNING should succeed: %v", err)
	}
	// RUNNING → COMPLETED
	if err := j.Transition(StatusCompleted); err != nil {
		t.Fatalf("RUNNING→COMPLETED should succeed: %v", err)
	}
	if j.FinishedAt == nil {
		t.Fatal("COMPLETED should set FinishedAt")
	}
}

func TestTransitionIllegal(t *testing.T) {
	j := newTestJob()

	// CREATED → RUNNING (跳过 VALIDATING)
	err := j.Transition(StatusRunning)
	if err == nil {
		t.Fatal("CREATED→RUNNING should be illegal")
	}
}

func TestTransitionIdempotent(t *testing.T) {
	j := newTestJob()

	if err := j.Transition(StatusValidating); err != nil {
		t.Fatal(err)
	}
	// 重复相同转换应幂等
	if err := j.Transition(StatusValidating); err != nil {
		t.Fatalf("idempotent re-transition should succeed: %v", err)
	}
}

func TestTransitionFromTerminal(t *testing.T) {
	j := newTestJob()

	j.Transition(StatusValidating)
	j.Transition(StatusQueued)
	j.Transition(StatusRunning)
	j.Transition(StatusCompleted)

	// 终态不应再转换
	err := j.Transition(StatusRunning)
	if err == nil {
		t.Fatal("COMPLETED→RUNNING should be illegal")
	}
}

func TestConcurrentTransition(t *testing.T) {
	j := newTestJob()
	j.Transition(StatusValidating)
	j.Transition(StatusQueued)

	done := make(chan bool, 2)
	go func() {
		_ = j.Transition(StatusRunning)
		done <- true
	}()
	go func() {
		_ = j.Transition(StatusCanceling)
		done <- true
	}()
	<-done
	<-done

	final := j.StatusSnapshot()
	if final != StatusRunning && final != StatusCanceling {
		t.Errorf("concurrent transition should end in RUNNING or CANCELING, got %s", final)
	}
}

func TestSetStage(t *testing.T) {
	j := newTestJob()

	j.SetStage(StageDiscovering)
	if j.StageSnapshot() != StageDiscovering {
		t.Errorf("stage should be DISCOVERING, got %s", j.StageSnapshot())
	}
}

func TestStatusConstants(t *testing.T) {
	// 确保常量不重复
	seen := make(map[JobStatus]bool)
	for _, s := range []JobStatus{
		StatusCreated, StatusValidating, StatusQueued, StatusRunning,
		StatusPausing, StatusPaused, StatusCanceling, StatusCancelled,
		StatusCompleted, StatusFailed,
	} {
		if seen[s] {
			t.Errorf("duplicate JobStatus: %s", s)
		}
		seen[s] = true
	}
}
