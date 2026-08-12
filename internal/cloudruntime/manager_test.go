package cloudruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/etl/backend/internal/s3store"
)

func mockRunJob(_ context.Context, job *Job, outDir string) error {
	_ = os.MkdirAll(filepath.Join(outDir, "0000000000-0000000001"), 0o755)
	_ = os.WriteFile(filepath.Join(outDir, "0000000000-0000000001", "transfers.parquet"), []byte("fake"), 0o644)
	job.Rows = 10
	return writeJobSuccess(outDir, job, 10)
}

func TestManagerMockJobLifecycle(t *testing.T) {
	root := t.TempDir()
	m := New(Config{Mode: ModeMock, JobsRoot: root, RunJob: mockRunJob})
	defer m.Close()
	id, err := m.SubmitJob(context.Background(), Job{
		ChainKey: "bsc", Addresses: []string{"0xaaa"}, FromBlock: 1, ToBlock: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, _ := m.JobStatus(id)
		if job.State == "done" {
			if job.Rows != 10 {
				t.Fatalf("rows = %d, want 10", job.Rows)
			}
			if _, err := os.Stat(filepath.Join(job.OutputDir, "_SUCCESS")); err != nil {
				t.Fatalf("missing _SUCCESS: %v", err)
			}
			if _, err := os.Stat(filepath.Join(job.OutputDir, "manifest.json")); err != nil {
				t.Fatalf("missing manifest: %v", err)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("job did not finish")
}

func TestManagerSingleWorkerSequential(t *testing.T) {
	root := t.TempDir()
	var running atomic.Int32
	var maxConcurrent atomic.Int32
	run := func(_ context.Context, job *Job, outDir string) error {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			cur := maxConcurrent.Load()
			if n <= cur {
				break
			}
			if maxConcurrent.CompareAndSwap(cur, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		return mockRunJob(context.Background(), job, outDir)
	}
	m := New(Config{Mode: ModeMock, JobsRoot: root, RunJob: run})
	defer m.Close()
	for i := 0; i < 3; i++ {
		if _, err := m.SubmitJob(context.Background(), Job{
			ChainKey: "bsc", ChunkID: fmt.Sprintf("chunk-%d", i),
			FromBlock: uint64(i), ToBlock: uint64(i) + 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		pending, leased := m.queueCountsLocked()
		done := pending == 0 && leased == 0 && m.runningJob == ""
		m.mu.Unlock()
		if done {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if maxConcurrent.Load() > 1 {
		t.Fatalf("max concurrent workers = %d, want 1", maxConcurrent.Load())
	}
}

func TestManagerIdleRemove(t *testing.T) {
	root := t.TempDir()
	m := New(Config{
		Mode: ModeMock, JobsRoot: root, RunJob: mockRunJob,
		IdleRemoveAfter: 100 * time.Millisecond,
	})
	defer m.Close()
	if _, err := m.SubmitJob(context.Background(), Job{ChainKey: "bsc", FromBlock: 1, ToBlock: 2}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m.Status().State == WorkerAbsent {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("worker not removed after idle: %s", m.Status().State)
}

func TestManagerDeployLock(t *testing.T) {
	root := t.TempDir()
	lockPath := filepath.Join(root, "deploy.lock")
	_ = os.WriteFile(lockPath, []byte("pid=1"), 0o644)
	m := New(Config{Mode: ModeMock, JobsRoot: root, RunJob: mockRunJob})
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := m.EnsureWorker(ctx); err == nil {
		t.Fatal("EnsureWorker should fail while another instance holds deploy lock")
	}
	_ = os.Remove(lockPath)
	if err := m.EnsureWorker(context.Background()); err != nil {
		t.Fatalf("EnsureWorker after lock release: %v", err)
	}
	if m.Status().State != WorkerReady {
		t.Fatalf("state = %s, want READY", m.Status().State)
	}
}

func TestManagerNotConfigured(t *testing.T) {
	m := New(Config{Mode: ModeNone, JobsRoot: t.TempDir()})
	if m.Status().State != WorkerNotConfigured || m.Status().Available {
		t.Fatalf("state = %s available=%v, want NOT_CONFIGURED/false", m.Status().State, m.Status().Available)
	}
	if _, err := m.SubmitJob(context.Background(), Job{}); err == nil {
		t.Fatal("SubmitJob should fail when not configured")
	}
	m.Reconcile(context.Background())
	if m.Status().State != WorkerNotConfigured {
		t.Fatalf("Reconcile must not overwrite NOT_CONFIGURED, state=%s", m.Status().State)
	}
}

func fakeSqdRunner(outputFor string) func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "list" {
			return []byte(outputFor), nil
		}
		return []byte("ok"), nil
	}
}

func publishTestCompletion(t *testing.T, store ObjectStore, job Job, rows int64, files []FileInfo, bodies map[string][]byte) {
	t.Helper()
	ctx := context.Background()
	for _, file := range files {
		body, ok := bodies[file.Path]
		if !ok {
			t.Fatalf("missing fixture body for %s", file.Path)
		}
		if err := store.Put(ctx, leasedChunkDir(job.ID, job.ChunkID)+"/"+file.Path, body); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := json.Marshal(publishedManifest{
		SchemaVersion: 2, JobID: job.ID, ChunkID: job.ChunkID,
		ChainID: job.ChainID, Dataset: job.Dataset, FromBlock: job.FromBlock, ToBlock: job.ToBlock,
		Addresses: job.Addresses, RowCount: rows, Files: files, Completed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	success, err := json.Marshal(completionMarker{JobID: job.ID, ChunkID: job.ChunkID, Rows: rows, Completed: true})
	if err != nil {
		t.Fatal(err)
	}
	completedDir := completedChunkDir(job.ID, job.ChunkID)
	if err := store.Put(ctx, completedDir+"/manifest.json", manifest); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, completedDir+"/_SUCCESS", success); err != nil {
		t.Fatal(err)
	}
}

func TestCloudModeSubmitAndRemoteStatus(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	m := New(Config{
		Mode: ModeCloud, DeployKey: "test-key", Store: store, JobsRoot: root,
		CommandRunner: fakeSqdRunner("bsc-emergency-worker v2"),
	})
	if m.Status().State == WorkerNotConfigured {
		t.Fatalf("cloud manager should be configured: %s", m.Status().Reason)
	}
	id, err := m.SubmitJob(context.Background(), Job{
		ChainKey: "bsc", ChunkID: "chunk-1", FromBlock: 1, ToBlock: 10,
		Addresses: []string{"0xaaa"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pendingKey := pendingChunkDir(id, "chunk-1") + "/request.json"
	ok, err := store.Exists(context.Background(), pendingKey)
	if err != nil || !ok {
		t.Fatalf("pending request missing: %v", err)
	}
	job, err := m.JobStatus(id)
	if err != nil || job.State != "queued" {
		t.Fatalf("remote status = %+v, %v", job, err)
	}
	// 模拟远端 Worker 完成，必须同时具备请求身份、Parquet 元数据和 _SUCCESS。
	body := []byte("parquet-fixture")
	sum := sha256.Sum256(body)
	files := []FileInfo{{Path: "token_transfers/part.parquet", Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}}
	m.mu.Lock()
	publishedJob := *m.jobs[id]
	m.mu.Unlock()
	publishTestCompletion(t, store, publishedJob, 7, files, map[string][]byte{files[0].Path: body})
	job, err = m.JobStatus(id)
	if err != nil || job.State != "done" || job.Rows != 7 {
		t.Fatalf("completed remote status = %+v, %v", job, err)
	}
	jobs := m.Jobs()
	if len(jobs) != 1 || jobs[0].State != "done" || jobs[0].Rows != 7 || len(jobs[0].Addresses) != 1 {
		t.Fatalf("cloud job list did not reconcile terminal status while preserving request: %+v", jobs)
	}
}

func TestReconcileAdoptsAndAbsents(t *testing.T) {
	root := t.TempDir()
	newM := func(output string) *Manager {
		return New(Config{
			Mode: ModeCloud, DeployKey: "k", Store: s3store.NewLocalStore(filepath.Join(root, "store")),
			JobsRoot: root, CommandRunner: fakeSqdRunner(output),
		})
	}
	m := newM("managed: bsc-emergency-worker slot v2")
	m.Reconcile(context.Background())
	if m.Status().State != WorkerIdle {
		t.Fatalf("reconcile adopted state = %s, want IDLE", m.Status().State)
	}
	m2 := newM("no matching worker")
	m2.Reconcile(context.Background())
	if m2.Status().State != WorkerAbsent {
		t.Fatalf("reconcile absent state = %s, want ABSENT", m2.Status().State)
	}
	if m2.Status().Available {
		t.Fatal("ABSENT runtime must not advertise executable availability")
	}
}

func TestCloudSubmitDeploysAndVerifiesBeforeEnqueue(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	var deployed bool
	var deployCalls int
	runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "list":
			if deployed {
				return []byte("managed: bsc-emergency-worker slot v2"), nil
			}
			return nil, nil
		case "deploy":
			deployCalls++
			deployed = true
			return []byte("deployed"), nil
		default:
			return nil, fmt.Errorf("unexpected sqd command: %v", args)
		}
	}
	m := New(Config{
		Mode: ModeCloud, DeployKey: "test-key", Store: store, R2Configured: true,
		JobsRoot: root, CommandRunner: runner, DeployTimeout: time.Second,
	})
	defer m.Close()
	if st := m.Status(); st.State != WorkerAbsent || st.Available {
		t.Fatalf("initial status = %+v, want ABSENT/unavailable", st)
	}
	id, err := m.SubmitJob(context.Background(), Job{
		ChainKey: "bsc", ChunkID: "chunk-1", FromBlock: 1, ToBlock: 10,
		Addresses: []string{"0xaaa"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if deployCalls != 1 {
		t.Fatalf("deploy calls = %d, want 1", deployCalls)
	}
	if st := m.Status(); st.State != WorkerReady || !st.Available {
		t.Fatalf("post-deploy status = %+v, want READY/available", st)
	}
	if ok, _ := store.Exists(context.Background(), pendingChunkDir(id, "chunk-1")+"/request.json"); !ok {
		t.Fatal("verified deployment must produce pending Cloud job")
	}
}

func TestCloudSubmitDeployFailureDoesNotEnqueue(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "list" {
			return nil, nil
		}
		if args[0] == "deploy" {
			return []byte("deployment rejected for test-key"), errors.New("exit 1")
		}
		return nil, nil
	}
	m := New(Config{
		Mode: ModeCloud, DeployKey: "test-key", Store: store, R2Configured: true,
		JobsRoot: root, CommandRunner: runner, DeployTimeout: time.Second,
		RuntimeFailureCooldown: time.Minute,
	})
	defer m.Close()
	_, err := m.SubmitJob(context.Background(), Job{ChainKey: "bsc", FromBlock: 1, ToBlock: 10})
	if err == nil || !strings.Contains(err.Error(), "sqd deploy") {
		t.Fatalf("submit error = %v, want explicit deploy failure", err)
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Fatalf("deploy key leaked in error: %v", err)
	}
	if st := m.Status(); st.State != WorkerFailed || st.Available || !strings.Contains(st.Reason, "Worker 部署失败") {
		t.Fatalf("failed status = %+v", st)
	}
	if objs, _ := store.List(context.Background(), queuePrefix+"pending/"); len(objs) != 0 {
		t.Fatalf("failed deployment must not enqueue job: %+v", objs)
	}
}

func TestCloudSubmitRejectsUnverifiedDeployment(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "deploy" {
			return []byte("command accepted"), nil
		}
		return nil, nil // sqd list 始终未出现 Worker
	}
	m := New(Config{
		Mode: ModeCloud, DeployKey: "test-key", Store: store, R2Configured: true,
		JobsRoot: root, CommandRunner: runner, DeployTimeout: 20 * time.Millisecond,
		RuntimeFailureCooldown: time.Minute,
	})
	defer m.Close()
	_, err := m.SubmitJob(context.Background(), Job{ChainKey: "bsc", FromBlock: 1, ToBlock: 10})
	if err == nil || !strings.Contains(err.Error(), "未在 sqd list 中出现") {
		t.Fatalf("submit error = %v, want post-deploy verification failure", err)
	}
	if st := m.Status(); st.State != WorkerFailed || st.Available {
		t.Fatalf("unverified deploy status = %+v", st)
	}
	if objs, _ := store.List(context.Background(), queuePrefix+"pending/"); len(objs) != 0 {
		t.Fatalf("unverified deployment must not enqueue job: %+v", objs)
	}
}

func TestCloudSubmitListFailureDegradesRuntime(t *testing.T) {
	root := t.TempDir()
	m := New(Config{
		Mode: ModeCloud, DeployKey: "test-key", R2Configured: true,
		Store: s3store.NewLocalStore(filepath.Join(root, "store")), JobsRoot: root,
		CommandRunner: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("network unavailable")
		},
	})
	defer m.Close()
	_, err := m.SubmitJob(context.Background(), Job{ChainKey: "bsc", FromBlock: 1, ToBlock: 10})
	if err == nil || !strings.Contains(err.Error(), "检查 SQD Cloud Worker 失败") {
		t.Fatalf("submit error = %v", err)
	}
	if st := m.Status(); st.State != WorkerDegraded || st.Available || !strings.Contains(st.Reason, "sqd list") {
		t.Fatalf("list failure status = %+v", st)
	}
}

func TestCancelUnknownJobRejected(t *testing.T) {
	root := t.TempDir()
	m := New(Config{Mode: ModeMock, JobsRoot: root, RunJob: mockRunJob})
	defer m.Close()
	if err := m.CancelJob(context.Background(), "missing-job"); err == nil || !strings.Contains(err.Error(), "未知 Cloud Job") {
		t.Fatalf("unknown cancel error = %v", err)
	}
	if ok, _ := m.store.Exists(context.Background(), cancelPrefix+"missing-job.json"); ok {
		t.Fatal("unknown job must not create cancel marker")
	}
	if err := m.CancelJob(context.Background(), "../escape"); err == nil {
		t.Fatal("path-like job id must be rejected")
	}
}

func TestSubmitRejectsUnsafeIDBeforeDeployment(t *testing.T) {
	root := t.TempDir()
	var commandCalls int
	m := New(Config{
		Mode: ModeCloud, DeployKey: "test-key", R2Configured: true,
		Store: s3store.NewLocalStore(filepath.Join(root, "store")), JobsRoot: root,
		CommandRunner: func(context.Context, string, ...string) ([]byte, error) {
			commandCalls++
			return nil, nil
		},
	})
	defer m.Close()
	if _, err := m.SubmitJob(context.Background(), Job{ID: "../escape", FromBlock: 1, ToBlock: 2}); err == nil {
		t.Fatal("unsafe job id must be rejected")
	}
	if commandCalls != 0 {
		t.Fatalf("invalid input must not call sqd CLI, calls=%d", commandCalls)
	}
}

func TestRemoveWorkerBlockedByPending(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	m := New(Config{
		Mode: ModeCloud, DeployKey: "k", Store: store, JobsRoot: root,
		CommandRunner: fakeSqdRunner("bsc-emergency-worker v2"),
	})
	_, err := m.SubmitJob(context.Background(), Job{ChainKey: "bsc", ChunkID: "chunk-1", FromBlock: 1, ToBlock: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveWorker(context.Background()); err == nil {
		t.Fatal("RemoveWorker must be blocked while pending job exists")
	}
	objs, _ := store.List(context.Background(), queuePrefix+"pending/")
	for _, o := range objs {
		_ = store.Delete(context.Background(), o.Key)
	}
	if err := m.RemoveWorker(context.Background()); err != nil {
		t.Fatalf("RemoveWorker after queue drain: %v", err)
	}
	if m.Status().State != WorkerAbsent {
		t.Fatalf("state = %s, want ABSENT", m.Status().State)
	}
}

func TestRemoteLeaseCountsAsRunningAndBlocksRemove(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	m := New(Config{
		Mode: ModeCloud, DeployKey: "k", Store: store, JobsRoot: root,
		CommandRunner: fakeSqdRunner("bsc-emergency-worker v2"),
	})
	ctx := context.Background()
	m.Reconcile(ctx) // 初始 ABSENT，先对账为 IDLE 才能进入 RemoveWorker 计数检查
	// 远端 Worker 只写 lease.json、尚未写 status.json 时：
	// JobStatus 必须视为 running，且 RemoveWorker 必须被 leased 计数阻止。
	leaseDir := leasedChunkDir("lease-only-job", "chunk-1")
	lease, _ := json.Marshal(map[string]any{
		"job_id": "lease-only-job", "chunk_id": "chunk-1",
		"worker_id": "sqd-cloud-1", "heartbeat_at": "2026-08-07T10:00:00Z",
	})
	if err := store.Put(ctx, leaseDir+"/lease.json", lease); err != nil {
		t.Fatal(err)
	}
	job, err := m.JobStatus("lease-only-job")
	if err != nil || job.State != "running" {
		t.Fatalf("lease-only remote status = %+v, %v; want running", job, err)
	}
	if err := m.RemoveWorker(ctx); err == nil {
		t.Fatal("RemoveWorker must be blocked while lease.json exists")
	}
	if m.Status().State == WorkerAbsent {
		t.Fatal("worker must not be marked ABSENT while lease exists")
	}
}

func TestLeaseExpiryRequeuesSameJob(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	m := New(Config{
		Mode: ModeCloud, DeployKey: "k", Store: store, JobsRoot: root,
		CommandRunner: fakeSqdRunner("bsc-emergency-worker v2"),
	})
	ctx := context.Background()
	m.Reconcile(ctx)
	id, err := m.SubmitJob(ctx, Job{
		ChainKey: "bsc", ChunkID: "chunk-1", FromBlock: 100, ToBlock: 200,
		Addresses: []string{"0xaaa"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟远端 Worker 领取并留下过期 lease（heartbeat 停止）
	lease := map[string]any{
		"job_id": id, "chunk_id": "chunk-1",
		"leased_at":        "2026-08-07T10:00:00.017Z",
		"lease_expires_at": "2026-08-07T10:10:00.017Z", // TS toISOString 带毫秒
		"heartbeat_at":     "2026-08-07T10:00:00.017Z",
	}
	payload, _ := json.Marshal(lease)
	if err := store.Put(ctx, leasedChunkDir(id, "chunk-1")+"/lease.json", payload); err != nil {
		t.Fatal(err)
	}
	// 删掉原 pending（模拟已被领取）
	_ = store.Delete(ctx, pendingChunkDir(id, "chunk-1")+"/request.json")
	if err := m.reclaimExpiredLeases(ctx); err != nil {
		t.Fatal(err)
	}
	pendingKey := pendingChunkDir(id, "chunk-1") + "/request.json"
	ok, _ := store.Exists(ctx, pendingKey)
	if !ok {
		t.Fatal("expired lease must requeue same job_id to pending")
	}
	reqData, err := store.Get(ctx, pendingKey)
	if err != nil {
		t.Fatal(err)
	}
	var req Job
	if json.Unmarshal(reqData, &req) != nil || req.ID != id {
		t.Fatalf("requeued request = %+v", req)
	}
	marker := requeuedPrefix + id + "/chunk-1/requeue.json"
	if ok, _ := store.Exists(ctx, marker); !ok {
		t.Fatal("requeue audit marker missing")
	}
}

func TestCancelMarkerAndCompletedIdempotency(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	m := New(Config{
		Mode: ModeCloud, DeployKey: "k", Store: store, JobsRoot: root,
		CommandRunner: fakeSqdRunner("bsc-emergency-worker v2"),
	})
	ctx := context.Background()
	m.Reconcile(ctx)
	id, err := m.SubmitJob(ctx, Job{
		ChainKey: "bsc", ChunkID: "chunk-1", FromBlock: 1, ToBlock: 10,
		Addresses: []string{"0xaaa"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CancelJob(ctx, id); err != nil {
		t.Fatal(err)
	}
	job, err := m.JobStatus(id)
	if err != nil || job.State != "cancelled" {
		t.Fatalf("cancel status = %+v, %v; want cancelled", job, err)
	}
	// completed 为最终幂等判据：即使残留 lease/pending 也必须返回 done
	body := []byte("parquet-fixture")
	sum := sha256.Sum256(body)
	files := []FileInfo{{Path: "token_transfers/part.parquet", Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}}
	m.mu.Lock()
	publishedJob := *m.jobs[id]
	m.mu.Unlock()
	publishTestCompletion(t, store, publishedJob, 7, files, map[string][]byte{files[0].Path: body})
	_ = store.Put(ctx, leasedChunkDir(id, "chunk-1")+"/lease.json", []byte(`{"job_id":"`+id+`"}`))
	_ = store.Put(ctx, pendingChunkDir(id, "chunk-1")+"/request.json", []byte(`{}`))
	job, err = m.JobStatus(id)
	if err != nil || job.State != "done" || job.Rows != 7 {
		t.Fatalf("completed idempotent status = %+v, %v", job, err)
	}
	if got, _ := m.acquirePendingJob(ctx); got != nil {
		t.Fatal("acquire must not consume job after completed")
	}
	if ok, _ := store.Exists(ctx, pendingChunkDir(id, "chunk-1")+"/request.json"); ok {
		t.Fatal("stale pending must be cleaned after completed")
	}
}

func TestMaterializeJobResultVerifiesManifestAndSHA(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	m := New(Config{Mode: ModeCloud, DeployKey: "k", Store: store, JobsRoot: root,
		CommandRunner: fakeSqdRunner("bsc-emergency-worker v2")})
	id, chunk := "job-materialize", "chunk-1"
	body := []byte("parquet-fixture")
	sum := sha256.Sum256(body)
	files := []FileInfo{{Path: "token_transfers/part.parquet", Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}}
	ctx := context.Background()
	publishTestCompletion(t, store, Job{ID: id, ChunkID: chunk, ChainID: 56, Dataset: "token_transfer", FromBlock: 1, ToBlock: 2}, 1, files, map[string][]byte{files[0].Path: body})
	dir, err := m.MaterializeJobResult(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "token_transfers", "part.parquet"))
	if err != nil || string(got) != string(body) {
		t.Fatalf("materialized artifact = %q, %v", got, err)
	}
}

func TestEnsureWorkerReusesExisting(t *testing.T) {
	root := t.TempDir()
	var calls []string
	runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, args[0])
		if args[0] == "deploy" {
			return nil, fmt.Errorf("deploy must not be called")
		}
		return []byte("managed: bsc-emergency-worker slot v2"), nil
	}
	m := New(Config{
		Mode: ModeCloud, DeployKey: "k", Store: s3store.NewLocalStore(filepath.Join(root, "store")),
		JobsRoot: root, CommandRunner: runner,
	})
	if err := m.EnsureWorker(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if c == "deploy" {
			t.Fatal("EnsureWorker must reuse existing worker without deploy")
		}
	}
	if m.Status().State != WorkerIdle {
		t.Fatalf("state = %s, want IDLE (reused)", m.Status().State)
	}
}

func TestCloudIdleReaperRemoves(t *testing.T) {
	root := t.TempDir()
	var calls []string
	runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, args[0])
		if args[0] == "list" {
			return []byte("managed: bsc-emergency-worker slot v2"), nil
		}
		return []byte("ok"), nil
	}
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	m := New(Config{
		Mode: ModeCloud, DeployKey: "k", Store: store, JobsRoot: root,
		CommandRunner: runner, IdleRemoveAfter: 150 * time.Millisecond, IdleReapInterval: 50 * time.Millisecond,
	})
	m.Reconcile(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m.Status().State == WorkerAbsent {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if m.Status().State != WorkerAbsent {
		t.Fatalf("idle reaper did not remove worker: %s", m.Status().State)
	}
	found := false
	for _, c := range calls {
		if c == "remove" {
			found = true
		}
	}
	if !found {
		t.Fatal("remove command was not issued")
	}
}

func TestCloudIdleReaperSkipsWhenPending(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	_ = store.Put(context.Background(), "bsc/jobs/pending/jobX/chunk-1/request.json", []byte(`{}`))
	runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "list" {
			return []byte("managed: bsc-emergency-worker slot v2"), nil
		}
		return []byte("ok"), nil
	}
	m := New(Config{
		Mode: ModeCloud, DeployKey: "k", Store: store, JobsRoot: root,
		CommandRunner: runner, IdleRemoveAfter: 100 * time.Millisecond, IdleReapInterval: 40 * time.Millisecond,
	})
	defer m.Close()
	m.Reconcile(context.Background())
	time.Sleep(350 * time.Millisecond)
	if m.Status().State == WorkerAbsent {
		t.Fatal("idle reaper must not remove worker while pending job exists")
	}
}

func TestWorkerLifecycleConvergesToIdleAndFailed(t *testing.T) {
	t.Run("success_to_idle", func(t *testing.T) {
		m := New(Config{Mode: ModeMock, JobsRoot: t.TempDir(), RunJob: mockRunJob, IdleRemoveAfter: time.Hour})
		defer m.Close()
		id, err := m.SubmitJob(context.Background(), Job{ChainKey: "bsc", FromBlock: 1, ToBlock: 2})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			job, _ := m.JobStatus(id)
			if job.State == "done" {
				st := m.Status()
				if st.State != WorkerIdle || !st.Available || st.RunningJob != "" {
					t.Fatalf("completed worker status = %+v, want IDLE/available/no running job", st)
				}
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("job did not complete")
	})

	t.Run("failure_to_failed", func(t *testing.T) {
		m := New(Config{
			Mode: ModeMock, JobsRoot: t.TempDir(), IdleRemoveAfter: time.Hour,
			RuntimeFailureCooldown: time.Minute,
			RunJob:                 func(context.Context, *Job, string) error { return errors.New("fixture worker failure") },
		})
		defer m.Close()
		id, err := m.SubmitJob(context.Background(), Job{ChainKey: "bsc", FromBlock: 1, ToBlock: 2})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			job, _ := m.JobStatus(id)
			if job.State == "failed" {
				st := m.Status()
				if st.State != WorkerFailed || st.Available || st.FailureCooldownUntil == nil || !strings.Contains(st.Reason, "fixture worker failure") {
					t.Fatalf("failed worker status = %+v", st)
				}
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("job did not fail")
	})
}

func TestCloudStatusDerivesQueueStateAndDeduplicatesLease(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	m := New(Config{Mode: ModeCloud, DeployKey: "k", R2Configured: true, Store: store, JobsRoot: root,
		CommandRunner: fakeSqdRunner("bsc-emergency-worker v2")})
	defer m.Close()
	m.Reconcile(context.Background())
	if st := m.Status(); st.State != WorkerIdle || st.QueuedJobs != 0 || st.LeasedJobs != 0 {
		t.Fatalf("empty queue status = %+v", st)
	}
	_ = store.Put(context.Background(), pendingChunkDir("job-q", "chunk-1")+"/request.json", []byte(`{}`))
	if st := m.Status(); st.State != WorkerReady || st.QueuedJobs != 1 || st.LeasedJobs != 0 {
		t.Fatalf("pending queue status = %+v, want READY queued=1", st)
	}
	_ = store.Delete(context.Background(), pendingChunkDir("job-q", "chunk-1")+"/request.json")
	leased := leasedChunkDir("job-q", "chunk-1")
	_ = store.Put(context.Background(), leased+"/lease.json", []byte(`{}`))
	_ = store.Put(context.Background(), leased+"/status.json", []byte(`{}`))
	if st := m.Status(); st.State != WorkerBusy || st.QueuedJobs != 0 || st.LeasedJobs != 1 {
		t.Fatalf("leased queue status = %+v, want BUSY leased=1 (not double counted)", st)
	}
}

func TestCloudWorkerMatchRequiresExactSlot(t *testing.T) {
	if cloudWorkerMatches("supreme/bsc-emergency-worker@v1 DEPLOYED", "bsc-emergency-worker", "v2") {
		t.Fatal("worker from slot v1 must not satisfy requested v2")
	}
	for _, output := range []string{
		"supreme/bsc-emergency-worker@v2 DEPLOYED",
		"managed: bsc-emergency-worker slot v2",
		"bsc-emergency-worker/v2 READY",
	} {
		if !cloudWorkerMatches(output, "bsc-emergency-worker", "v2") {
			t.Fatalf("expected worker/slot match for %q", output)
		}
	}
}

func TestRemoteCompletionFailsClosedOnInvalidPublication(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	m := New(Config{Mode: ModeCloud, DeployKey: "k", Store: store, JobsRoot: root,
		CommandRunner: fakeSqdRunner("bsc-emergency-worker v2")})
	defer m.Close()
	id, chunk := "job-invalid-complete", "chunk-1"
	body := []byte("parquet-fixture")
	sum := sha256.Sum256(body)
	file := FileInfo{Path: "token_transfers/part.parquet", Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
	manifest, _ := json.Marshal(publishedManifest{
		SchemaVersion: 2, JobID: id, ChunkID: chunk, ChainID: 56, Dataset: "token_transfer",
		FromBlock: 1, ToBlock: 2, RowCount: 7, Files: []FileInfo{file}, Completed: true,
	})
	_ = store.Put(context.Background(), leasedChunkDir(id, chunk)+"/"+file.Path, body)
	_ = store.Put(context.Background(), completedChunkDir(id, chunk)+"/manifest.json", manifest)
	_ = store.Put(context.Background(), completedChunkDir(id, chunk)+"/_SUCCESS", []byte(`{"job_id":"job-invalid-complete","chunk_id":"chunk-1","rows":6,"completed":true}`))
	job, err := m.JobStatus(id)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "failed" || !strings.Contains(job.Error, "_SUCCESS 与 manifest 不一致") {
		t.Fatalf("invalid publication status = %+v, want failed quality gate", job)
	}
}

func TestMaterializeIsAtomicAndRequiresBytesAndSHA(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	m := New(Config{Mode: ModeCloud, DeployKey: "k", Store: store, JobsRoot: root,
		CommandRunner: fakeSqdRunner("bsc-emergency-worker v2")})
	defer m.Close()
	id, chunk := "job-atomic", "chunk-1"
	body1, body2 := []byte("first-parquet"), []byte("second-parquet")
	sum1, sum2 := sha256.Sum256(body1), sha256.Sum256(body2)
	files := []FileInfo{
		{Path: "token_transfers/part-1.parquet", Bytes: int64(len(body1)), SHA256: hex.EncodeToString(sum1[:])},
		{Path: "token_transfers/part-2.parquet", Bytes: int64(len(body2)) + 1, SHA256: hex.EncodeToString(sum2[:])},
	}
	publishTestCompletion(t, store, Job{ID: id, ChunkID: chunk, ChainID: 56, Dataset: "token_transfer", FromBlock: 1, ToBlock: 2}, 2, files,
		map[string][]byte{files[0].Path: body1, files[1].Path: body2})
	if _, err := m.MaterializeJobResult(context.Background(), id); err == nil || !strings.Contains(err.Error(), "字节数不匹配") {
		t.Fatalf("materialize error = %v, want byte mismatch", err)
	}
	dest := filepath.Join(root, "jobs", id, chunk, "remote-result")
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("failed materialization must not publish partial directory: %v", err)
	}
}

func TestQueueIDRejectsFilesystemAmbiguity(t *testing.T) {
	for _, id := range []string{".", "..", ".hidden", "trailing.", "CON", "nul.txt", "COM1"} {
		if err := validateQueueID("job id", id); err == nil {
			t.Errorf("id %q must be rejected", id)
		}
	}
	m := New(Config{Mode: ModeMock, JobsRoot: t.TempDir(), RunJob: mockRunJob})
	defer m.Close()
	if _, err := m.JobStatus(".."); err == nil {
		t.Fatal("JobStatus must reject path-like id")
	}
}

func TestRemoveFailureDoesNotClaimAbsent(t *testing.T) {
	root := t.TempDir()
	runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "list" {
			return []byte("supreme/bsc-emergency-worker@v2 DEPLOYED"), nil
		}
		if args[0] == "remove" {
			return []byte("permission denied for key secret-key"), errors.New("exit 1")
		}
		return nil, nil
	}
	m := New(Config{Mode: ModeCloud, DeployKey: "secret-key", Store: s3store.NewLocalStore(filepath.Join(root, "store")),
		JobsRoot: root, CommandRunner: runner})
	m.Reconcile(context.Background())
	err := m.RemoveWorker(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("remove error = %v, want redacted failure", err)
	}
	if st := m.Status(); st.State != WorkerDegraded || st.Available || !strings.Contains(st.Reason, "移除失败") {
		t.Fatalf("failed removal status = %+v, must not claim ABSENT", st)
	}
}
