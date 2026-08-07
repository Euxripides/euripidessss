package downloadscheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/etl/backend/internal/cloudruntime"
)

// fakeCloudRuntime 测试用 Cloud 运行时：提交后立即完成。
type fakeCloudRuntime struct {
	mu     sync.Mutex
	jobs   map[string]cloudruntime.Job
	status cloudruntime.Status
}

func newFakeCloudRuntime() *fakeCloudRuntime {
	return &fakeCloudRuntime{
		jobs:   map[string]cloudruntime.Job{},
		status: cloudruntime.Status{State: cloudruntime.WorkerReady, Mode: cloudruntime.ModeMock, Available: true},
	}
}

func (f *fakeCloudRuntime) SubmitJob(_ context.Context, job cloudruntime.Job) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	job.State = "done"
	job.OutputDir = "mock-output"
	job.Rows = 42
	job.StartedAt = &now
	job.FinishedAt = &now
	f.jobs[job.ID] = job
	return job.ID, nil
}

func (f *fakeCloudRuntime) JobStatus(id string) (cloudruntime.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	job, ok := f.jobs[id]
	if !ok {
		return cloudruntime.Job{}, errors.New("job not found")
	}
	return job, nil
}

func (f *fakeCloudRuntime) CancelJob(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if job, ok := f.jobs[id]; ok {
		job.State = "cancelled"
		f.jobs[id] = job
	}
	return nil
}

func (f *fakeCloudRuntime) Status() cloudruntime.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

// failingProvider 常规 Provider 失败桩。
type failingProvider struct {
	kind ProviderKind
}

func (p *failingProvider) Kind() ProviderKind       { return p.kind }
func (p *failingProvider) Name() string             { return string(p.kind) + " failing" }
func (p *failingProvider) Tier() ProviderTier       { return TierNormal }
func (p *failingProvider) ManualOnly() bool         { return false }
func (p *failingProvider) Available() bool          { return true }
func (p *failingProvider) CanHandle(d Dataset) bool { return d == DatasetTokenTransfer }
func (p *failingProvider) State() ProviderState     { return ProviderHealthy }
func (p *failingProvider) StateReasons() []string   { return nil }
func (p *failingProvider) Score(d Dataset) ProviderScore {
	return ProviderScore{
		Provider: p.kind, Name: p.Name(), Tier: TierNormal, State: ProviderHealthy,
		Coverage: 50, Accuracy: 50, Speed: 50, Cost: 50, Reliability: 50,
		Available: true, Total: 50,
	}
}
func (p *failingProvider) Execute(_ context.Context, _ Requirement) (*TaskResult, error) {
	return nil, errors.New("HTTP 503 service unavailable")
}

// successProvider 常规 Provider 成功桩（验证 Cloud 调用次数为 0）。
type successProvider struct{ calls int }

func (p *successProvider) Kind() ProviderKind       { return ProviderRPC }
func (p *successProvider) Name() string             { return "success rpc" }
func (p *successProvider) Tier() ProviderTier       { return TierNormal }
func (p *successProvider) ManualOnly() bool         { return false }
func (p *successProvider) Available() bool          { return true }
func (p *successProvider) CanHandle(d Dataset) bool { return d == DatasetTokenTransfer }
func (p *successProvider) State() ProviderState     { return ProviderHealthy }
func (p *successProvider) StateReasons() []string   { return nil }
func (p *successProvider) Score(d Dataset) ProviderScore {
	return ProviderScore{
		Provider: ProviderRPC, Name: p.Name(), Tier: TierNormal, State: ProviderHealthy,
		Coverage: 50, Accuracy: 50, Speed: 50, Cost: 50, Reliability: 50,
		Available: true, Total: 60,
	}
}
func (p *successProvider) Execute(_ context.Context, req Requirement) (*TaskResult, error) {
	p.calls++
	return &TaskResult{Summary: "ok", Rows: 1, NewData: true}, nil
}

func testSchedulerCloud(t *testing.T, rt CloudRuntime, fault FaultInjection) *Scheduler {
	t.Helper()
	cloud := NewCloudProvider(rt).WithBlockResolver(func(_ context.Context, _ Requirement) (uint64, uint64, error) {
		return 1, 10, nil
	})
	registry := NewRegistry(
		&failingProvider{kind: ProviderSQD},
		&failingProvider{kind: ProviderRPC},
		cloud,
	)
	s := NewScheduler(registry, NewCoverageResolver(nil), "", DefaultBudget())
	s.WithCloudFallback(rt, NewCloudUsageStore(""), fault)
	return s
}

func waitPlanTerminal(t *testing.T, s *Scheduler, id string) *Plan {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		p := s.Plan(id)
		if p != nil && p.Status.Terminal() {
			return p
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("plan did not reach terminal state")
	return nil
}

func TestSchedulerCloudFallbackWhenAllNormalExhausted(t *testing.T) {
	rt := newFakeCloudRuntime()
	s := testSchedulerCloud(t, rt, FaultInjection{AllNormalProvidersFail: true})
	req := testRequirement(DatasetTokenTransfer)
	req.Addresses = []string{"0x2222222222222222222222222222222222222222"}
	plan, err := s.Submit(context.Background(), []Requirement{req})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	final := waitPlanTerminal(t, s, plan.ID)
	if final.Status != StatusReady {
		t.Fatalf("plan status = %s, want READY_FOR_GRAPH (stage=%s)", final.Status, final.StageDetail)
	}
	task := final.Tasks[0]
	if task.Status != "done" || task.Provider != ProviderSQDCloud {
		t.Fatalf("task status/provider = %s/%s, want done/sqd_cloud", task.Status, task.Provider)
	}
	var cloudAttempt bool
	for _, a := range task.Attempts {
		if a.Provider == ProviderSQDCloud {
			cloudAttempt = true
			if !a.Success {
				t.Fatalf("cloud attempt should be success: %+v", a)
			}
		}
	}
	if !cloudAttempt {
		t.Fatal("missing sqd_cloud attempt in attempts audit")
	}
	if final.Cloud == nil || final.Cloud.AdmittedTasks != 1 {
		t.Fatalf("plan cloud info = %+v, want admitted 1", final.Cloud)
	}
}

func TestSchedulerCloudBudgetBlocks(t *testing.T) {
	rt := newFakeCloudRuntime()
	s := testSchedulerCloud(t, rt, FaultInjection{AllNormalProvidersFail: true})
	s.budget.Cloud.Enabled = false
	s.gate.budget = s.budget.Cloud
	plan, err := s.Submit(context.Background(), []Requirement{testRequirement(DatasetTokenTransfer)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	final := waitPlanTerminal(t, s, plan.ID)
	if final.Status != StatusFailed {
		t.Fatalf("plan status = %s, want FAILED (stage=%s)", final.Status, final.StageDetail)
	}
	if len(final.Cloud.RejectReasons) == 0 || final.Cloud.RejectReasons[0] != "CLOUD_DISABLED：Cloud 预算开关关闭" {
		t.Fatalf("reject reasons = %+v", final.Cloud.RejectReasons)
	}
}

func TestSchedulerNormalProviderNoCloudCall(t *testing.T) {
	rt := newFakeCloudRuntime()
	success := &successProvider{}
	registry := NewRegistry(success, NewCloudProvider(rt))
	s := NewScheduler(registry, NewCoverageResolver(nil), "", DefaultBudget())
	s.WithCloudFallback(rt, NewCloudUsageStore(""), FaultInjection{})
	plan, err := s.Submit(context.Background(), []Requirement{testRequirement(DatasetTokenTransfer)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	final := waitPlanTerminal(t, s, plan.ID)
	if final.Status != StatusReady {
		t.Fatalf("plan status = %s", final.Status)
	}
	if success.calls == 0 {
		t.Fatal("normal provider should be used")
	}
	rt.mu.Lock()
	cloudJobs := len(rt.jobs)
	rt.mu.Unlock()
	if cloudJobs != 0 {
		t.Fatalf("cloud must not be called when normal provider healthy, jobs=%d", cloudJobs)
	}
}
