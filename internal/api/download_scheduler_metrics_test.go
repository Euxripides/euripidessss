package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/etl/backend/internal/cloudruntime"
	"github.com/etl/backend/internal/downloadscheduler"
)

type metricsCloudRuntime struct {
	status cloudruntime.Status
	jobs   []cloudruntime.Job
}

func (m *metricsCloudRuntime) SubmitJob(context.Context, cloudruntime.Job) (string, error) {
	return "", nil
}
func (m *metricsCloudRuntime) JobStatus(string) (cloudruntime.Job, error) {
	return cloudruntime.Job{}, nil
}
func (m *metricsCloudRuntime) CancelJob(context.Context, string) error { return nil }
func (m *metricsCloudRuntime) Status() cloudruntime.Status             { return m.status }
func (m *metricsCloudRuntime) Jobs() []cloudruntime.Job                { return m.jobs }

func TestSchedulerMetricsIncludesEvidenceBackedCloudLifecycle(t *testing.T) {
	now := time.Now().UTC()
	completed := now.Add(-30 * time.Minute)
	failed := now.Add(-10 * time.Minute)
	oldCompleted := now.Add(-2 * time.Hour)
	runtime := &metricsCloudRuntime{
		status: cloudruntime.Status{State: cloudruntime.WorkerBusy, Mode: cloudruntime.ModeCloud, Available: true, QueuedJobs: 2, LeasedJobs: 2},
		jobs: []cloudruntime.Job{
			{State: "queued", Stage: "queued"},
			{State: "running", Stage: "leased"},
			{State: "running", Stage: "running"},
			{State: "running", Stage: "uploading"},
			{State: "running", Stage: "syncing"},
			{State: "running", Stage: "validating"},
			{State: "done", Stage: "completed", CompletedAt: &completed},
			{State: "failed", Stage: "failed", FailedAt: &failed},
			{State: "done", Stage: "completed", CompletedAt: &oldCompleted},
		},
	}
	scheduler := downloadscheduler.NewScheduler(nil, nil, "", downloadscheduler.Budget{}).
		WithCloudFallback(runtime, nil, downloadscheduler.FaultInjection{})
	handler := NewSchedulerHandler(scheduler)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		"cloud_workers_total": 1, "cloud_workers_alive": 1, "cloud_workers_busy": 1, "cloud_workers_idle": 0,
		"jobs_queued": 2, "jobs_leased": 2, "jobs_running": 1, "jobs_uploading": 1,
		"jobs_syncing": 1, "jobs_validating": 1, "jobs_completed_1h": 1, "jobs_failed_1h": 1,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s=%v, want %v", key, got[key], value)
		}
	}
}

func TestSchedulerMetricsUsesWorkerSlotTelemetry(t *testing.T) {
	now := time.Now().UTC()
	runtime := &metricsCloudRuntime{status: cloudruntime.Status{
		State: cloudruntime.WorkerBusy, Mode: cloudruntime.ModeCloud, Available: true,
		ConfiguredConcurrency: 2, WorkersTotal: 2, WorkersAlive: 2, WorkersBusy: 1, WorkersIdle: 1,
		MaxSQDInflightRequests: 6,
		WorkerSlots: []cloudruntime.WorkerSlotStatus{
			{WorkerID: "processor-a", Slot: 0, State: "BUSY", JobID: "job-1", Alive: true, UpdatedAt: now},
			{WorkerID: "processor-a", Slot: 1, State: "IDLE", Alive: true, UpdatedAt: now},
		},
	}}
	scheduler := downloadscheduler.NewScheduler(nil, nil, "", downloadscheduler.Budget{}).
		WithCloudFallback(runtime, nil, downloadscheduler.FaultInjection{})
	handler := NewSchedulerHandler(scheduler)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		"cloud_workers_configured": 2, "cloud_workers_total": 2, "cloud_workers_alive": 2,
		"cloud_workers_busy": 1, "cloud_workers_idle": 1, "cloud_max_sqd_inflight_requests": 6,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s=%v, want %v", key, got[key], value)
		}
	}
	if slots, ok := got["cloud_worker_slots"].([]any); !ok || len(slots) != 2 {
		t.Fatalf("cloud_worker_slots=%#v, want 2 entries", got["cloud_worker_slots"])
	}
}
