package downloadscheduler

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/etl/backend/internal/cloudruntime"
)

func TestCloudProviderDoesNotAdvertiseAbsentAsHealthy(t *testing.T) {
	rt := newFakeCloudRuntime()
	rt.status = cloudruntime.Status{
		State: cloudruntime.WorkerAbsent, Mode: cloudruntime.ModeCloud,
		Available: false, DeploymentKeyConfigured: true, R2Configured: true,
	}
	p := NewCloudProvider(rt)
	if p.Available() {
		t.Fatal("ABSENT Cloud Provider must not advertise executable availability")
	}
	if got := p.State(); got != ProviderUnavailable {
		t.Fatalf("ABSENT provider state = %s, want UNAVAILABLE", got)
	}
	if score := p.Score(DatasetTokenTransfer); score.Available || score.State != ProviderUnavailable {
		t.Fatalf("ABSENT provider score = %+v", score)
	}
}

func TestSplitCloudChunksExactCoverageNoOverlap(t *testing.T) {
	req := Requirement{ID: "task", PlanID: "plan", ChainKey: "bsc", Addresses: []string{"0xa", "0xb"}}
	chunks := splitCloudChunks(req, 1, 100_000, 1, 50_000)
	if len(chunks) != 4 {
		t.Fatalf("chunks=%d, want 4", len(chunks))
	}
	want := [][2]uint64{{1, 50_000}, {50_001, 100_000}, {1, 50_000}, {50_001, 100_000}}
	for i, chunk := range chunks {
		if chunk.FromBlock != want[i][0] || chunk.ToBlock != want[i][1] {
			t.Errorf("chunk %d range=%d-%d want=%d-%d", i, chunk.FromBlock, chunk.ToBlock, want[i][0], want[i][1])
		}
		if got := chunk.ToBlock - chunk.FromBlock + 1; got != 50_000 {
			t.Errorf("chunk %d size=%d, want 50000", i, got)
		}
		if len(chunk.Addresses) != 1 {
			t.Errorf("chunk %d addresses=%v", i, chunk.Addresses)
		}
	}
	// MaxUint64 边界不能溢出或死循环。
	edge := splitCloudChunks(req, math.MaxUint64-1, math.MaxUint64, 2, 1)
	if len(edge) != 2 || edge[0].FromBlock != math.MaxUint64-1 || edge[0].ToBlock != math.MaxUint64-1 ||
		edge[1].FromBlock != math.MaxUint64 || edge[1].ToBlock != math.MaxUint64 {
		t.Fatalf("max uint chunks = %+v", edge)
	}
}

func TestCloudProviderProgressTracksAllChunks(t *testing.T) {
	rt := newFakeCloudRuntime()
	p := NewCloudProvider(rt).WithBlockResolver(func(context.Context, Requirement) (uint64, uint64, error) {
		return 1, 100_000, nil
	})
	result, err := p.Execute(context.Background(), Requirement{
		ID: "task-multi", PlanID: "plan", Dataset: DatasetTokenTransfer, ChainKey: "bsc",
		Addresses: []string{"0x1111111111111111111111111111111111111111"},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	ids := append([]string(nil), p.chunkJobs[result.JobID]...)
	p.mu.Unlock()
	if len(ids) != 2 {
		t.Fatalf("job mapping = %v, want both chunks", ids)
	}
	rt.mu.Lock()
	second := rt.jobs[ids[1]]
	second.State = "running"
	rt.jobs[ids[1]] = second
	rt.mu.Unlock()
	progress, status, err := p.JobProgress(context.Background(), result.JobID)
	if err != nil || status != "running" || progress != 0.5 {
		t.Fatalf("partial progress=%v status=%s err=%v, want 0.5/running", progress, status, err)
	}
	rt.mu.Lock()
	second.State = "done"
	rt.jobs[ids[1]] = second
	rt.mu.Unlock()
	progress, status, err = p.JobProgress(context.Background(), result.JobID)
	if err != nil || status != "done" || progress != 1 {
		t.Fatalf("final progress=%v status=%s err=%v", progress, status, err)
	}
}

type partialSubmitRuntime struct {
	mu        sync.Mutex
	status    cloudruntime.Status
	submitted []string
	cancelled []string
}

func (r *partialSubmitRuntime) SubmitJob(_ context.Context, job cloudruntime.Job) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.submitted) == 1 {
		return "", errors.New("fixture R2 enqueue failure")
	}
	r.submitted = append(r.submitted, job.ID)
	return job.ID, nil
}
func (r *partialSubmitRuntime) JobStatus(string) (cloudruntime.Job, error) {
	return cloudruntime.Job{}, errors.New("not used")
}
func (r *partialSubmitRuntime) CancelJob(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelled = append(r.cancelled, id)
	return nil
}
func (r *partialSubmitRuntime) Status() cloudruntime.Status { return r.status }

func TestCloudProviderRollsBackPartialSubmission(t *testing.T) {
	rt := &partialSubmitRuntime{status: cloudruntime.Status{State: cloudruntime.WorkerReady, Available: true}}
	p := NewCloudProvider(rt).WithBlockResolver(func(context.Context, Requirement) (uint64, uint64, error) {
		return 1, 100_000, nil
	})
	_, err := p.Execute(context.Background(), Requirement{
		ID: "task-rollback", Dataset: DatasetTokenTransfer, ChainKey: "bsc",
		Addresses: []string{"0x1111111111111111111111111111111111111111"},
	})
	if err == nil || !strings.Contains(err.Error(), "此前 1 个 Chunk 已请求取消") {
		t.Fatalf("execute error=%v", err)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.submitted) != 1 || len(rt.cancelled) != 1 || rt.cancelled[0] != rt.submitted[0] {
		t.Fatalf("submitted=%v cancelled=%v", rt.submitted, rt.cancelled)
	}
}

func TestCloudProviderRejectsUndeployableAbsentRuntime(t *testing.T) {
	rt := newFakeCloudRuntime()
	rt.status = cloudruntime.Status{State: cloudruntime.WorkerAbsent, Mode: cloudruntime.ModeCloud, Available: false,
		DeploymentKeyConfigured: true, R2Configured: false, Reason: "R2 missing"}
	p := NewCloudProvider(rt).WithBlockResolver(func(context.Context, Requirement) (uint64, uint64, error) { return 1, 2, nil })
	_, err := p.Execute(context.Background(), Requirement{ID: "task", Dataset: DatasetTokenTransfer, ChainKey: "bsc"})
	if err == nil || !strings.Contains(err.Error(), "R2 missing") {
		t.Fatalf("execute error=%v, want undeployable reason", err)
	}
}

func TestCloudProviderStateRequiresReadyRuntime(t *testing.T) {
	rt := newFakeCloudRuntime()
	p := NewCloudProvider(rt)
	cases := []struct {
		state     cloudruntime.WorkerState
		available bool
		wantState ProviderState
	}{
		{cloudruntime.WorkerDeploying, false, ProviderDegraded},
		{cloudruntime.WorkerStarting, false, ProviderDegraded},
		{cloudruntime.WorkerFailed, false, ProviderUnavailable},
		{cloudruntime.WorkerReady, true, ProviderHealthy},
		{cloudruntime.WorkerBusy, true, ProviderHealthy},
		{cloudruntime.WorkerIdle, true, ProviderHealthy},
	}
	for _, tc := range cases {
		rt.status.State = tc.state
		rt.status.Available = tc.available
		if got := p.Available(); got != tc.available {
			t.Errorf("state %s available = %v, want %v", tc.state, got, tc.available)
		}
		if got := p.State(); got != tc.wantState {
			t.Errorf("state %s provider state = %s, want %s", tc.state, got, tc.wantState)
		}
	}
}

func TestCloudProviderExecuteRejectsFailedRuntimeClearly(t *testing.T) {
	rt := newFakeCloudRuntime()
	rt.status = cloudruntime.Status{
		State: cloudruntime.WorkerFailed, Mode: cloudruntime.ModeCloud,
		Reason: "Worker 部署失败: quota exceeded",
	}
	p := NewCloudProvider(rt).WithBlockResolver(func(context.Context, Requirement) (uint64, uint64, error) {
		return 1, 10, nil
	})
	_, err := p.Execute(context.Background(), Requirement{
		ID: "task-1", Dataset: DatasetTokenTransfer, ChainKey: "bsc",
		Addresses: []string{"0x1111111111111111111111111111111111111111"},
	})
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("execute error = %v, want runtime failure reason", err)
	}
	rt.mu.Lock()
	jobs := len(rt.jobs)
	rt.mu.Unlock()
	if jobs != 0 {
		t.Fatalf("failed runtime must not submit jobs, got %d", jobs)
	}
}
