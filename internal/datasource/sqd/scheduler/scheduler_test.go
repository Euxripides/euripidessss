package scheduler

import (
	"context"
	"testing"
	"time"
)

func TestScheduler_SubmitAndDone(t *testing.T) {
	s := New(DefaultConfig())
	defer s.Close()

	ctx := context.Background()
	task, err := s.Submit(ctx, "task-1", KindStream, PriorityNormal)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Task should be granted immediately since no other tasks
	select {
	case <-task.done:
		// good
	case <-time.After(time.Second):
		t.Fatal("task was not granted within 1s")
	}

	stats := s.Stats()
	if stats.ActiveStreams != 1 {
		t.Errorf("expected 1 active, got %d", stats.ActiveStreams)
	}

	s.Done(task)

	stats = s.Stats()
	if stats.ActiveStreams != 0 {
		t.Errorf("expected 0 active after done, got %d", stats.ActiveStreams)
	}
	if stats.Completed != 1 {
		t.Errorf("expected 1 completed, got %d", stats.Completed)
	}
}

func TestScheduler_MaxSmallJobs(t *testing.T) {
	config := Config{
		MaxParallelStreams: 2,
		MaxLargeJobs:       1,
		MaxSmallJobs:       1,
		QueueSize:          10,
	}
	s := New(config)
	defer s.Close()

	ctx := context.Background()

	// Submit 2 small jobs — second should be queued
	task1, err := s.Submit(ctx, "task-1", KindStream, PriorityNormal)
	if err != nil {
		t.Fatalf("Submit task1: %v", err)
	}
	// task1 is granted immediately
	<-task1.done

	task2, err := s.Submit(ctx, "task-2", KindStream, PriorityNormal)
	if err != nil {
		t.Fatalf("Submit task2: %v", err)
	}

	// task2 should NOT be granted yet (max 1 small job)
	select {
	case <-task2.done:
		t.Fatal("task2 should not be granted while task1 is active")
	case <-time.After(100 * time.Millisecond):
		// expected
	}

	// Complete task1 → task2 should be granted
	s.Done(task1)

	select {
	case <-task2.done:
		// task2 now granted
	case <-time.After(time.Second):
		t.Fatal("task2 was not granted after task1 done")
	}

	s.Done(task2)
}

func TestScheduler_PriorityOrder(t *testing.T) {
	config := Config{
		MaxParallelStreams: 1,
		MaxLargeJobs:       1,
		MaxSmallJobs:       1,
		QueueSize:          10,
	}
	s := New(config)
	defer s.Close()

	ctx := context.Background()

	// Occupy the only slot
	holder, _ := s.Submit(ctx, "holder", KindStream, PriorityNormal)
	<-holder.done

	// Submit low first, then high
	low, _ := s.Submit(ctx, "low", KindStream, PriorityLow)
	high, _ := s.Submit(ctx, "high", KindStream, PriorityHigh)

	// Complete holder
	s.Done(holder)

	// High should be granted first
	select {
	case <-high.done:
		// correct
	case <-low.done:
		t.Fatal("low priority task granted before high priority")
	case <-time.After(time.Second):
		t.Fatal("no task granted")
	}

	s.Done(high)

	// Now low should be granted
	select {
	case <-low.done:
	case <-time.After(time.Second):
		t.Fatal("low priority task not granted")
	}

	s.Done(low)
}

func TestScheduler_Cancel(t *testing.T) {
	config := Config{
		MaxParallelStreams: 1,
		MaxLargeJobs:       1,
		MaxSmallJobs:       1,
		QueueSize:          10,
	}
	s := New(config)
	defer s.Close()

	ctx := context.Background()

	// Occupy the slot
	holder, _ := s.Submit(ctx, "holder", KindStream, PriorityNormal)
	<-holder.done

	// Queue a task
	queued, _ := s.Submit(ctx, "queued", KindStream, PriorityNormal)

	// Cancel it
	if !s.Cancel("queued") {
		t.Fatal("Cancel returned false")
	}

	// Verify it's removed from queue via WaitFor
	if err := queued.WaitFor(ctx); err == nil {
		t.Fatal("cancelled task WaitFor should return error")
	}

	s.Done(holder)
}

func TestScheduler_QueueFull(t *testing.T) {
	config := Config{
		MaxParallelStreams: 1,
		MaxLargeJobs:       1,
		MaxSmallJobs:       1,
		QueueSize:          2,
	}
	s := New(config)
	defer s.Close()

	ctx := context.Background()

	// Occupy the only slot
	holder, _ := s.Submit(ctx, "holder", KindStream, PriorityNormal)
	<-holder.done

	// Fill the queue
	s.Submit(ctx, "q1", KindStream, PriorityNormal)
	s.Submit(ctx, "q2", KindStream, PriorityNormal)

	// Third should be rejected
	_, err := s.Submit(ctx, "q3", KindStream, PriorityNormal)
	if err != ErrSchedulerFull {
		t.Errorf("expected ErrSchedulerFull, got %v", err)
	}

	stats := s.Stats()
	if stats.Rejected != 1 {
		t.Errorf("expected 1 rejected, got %d", stats.Rejected)
	}

	s.Cancel("q1")
	s.Cancel("q2")
	s.Done(holder)
}

func TestScheduler_Stats(t *testing.T) {
	s := New(DefaultConfig())
	defer s.Close()

	ctx := context.Background()

	task, _ := s.Submit(ctx, "task-1", KindLargeJob, PriorityHigh)
	<-task.done

	stats := s.Stats()
	if stats.ActiveLargeJobs != 1 {
		t.Errorf("expected 1 active large job, got %d", stats.ActiveLargeJobs)
	}
	if stats.Queued != 0 {
		t.Errorf("expected 0 queued, got %d", stats.Queued)
	}

	s.Done(task)
}
