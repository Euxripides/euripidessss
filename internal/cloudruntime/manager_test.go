package cloudruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	// 模拟远端 Worker 完成
	completedDir := completedChunkDir(id, "chunk-1")
	manifest, _ := json.Marshal(map[string]any{"job_id": id, "chunk_id": "chunk-1", "row_count": 7})
	_ = store.Put(context.Background(), completedDir+"/manifest.json", manifest)
	_ = store.Put(context.Background(), completedDir+"/_SUCCESS", []byte(`{"completed":true}`))
	job, err = m.JobStatus(id)
	if err != nil || job.State != "done" || job.Rows != 7 {
		t.Fatalf("completed remote status = %+v, %v", job, err)
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
}

func TestRemoveWorkerBlockedByPending(t *testing.T) {
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	m := New(Config{
		Mode: ModeCloud, DeployKey: "k", Store: store, JobsRoot: root,
		CommandRunner: fakeSqdRunner("bsc-emergency-worker"),
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
		CommandRunner: fakeSqdRunner("bsc-emergency-worker"),
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
		CommandRunner: fakeSqdRunner("bsc-emergency-worker"),
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
		CommandRunner: fakeSqdRunner("bsc-emergency-worker"),
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
	completedDir := completedChunkDir(id, "chunk-1")
	manifest, _ := json.Marshal(map[string]any{"job_id": id, "chunk_id": "chunk-1", "row_count": 7})
	_ = store.Put(ctx, completedDir+"/manifest.json", manifest)
	_ = store.Put(ctx, completedDir+"/_SUCCESS", []byte(`{"completed":true}`))
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
		CommandRunner: fakeSqdRunner("bsc-emergency-worker")})
	id, chunk := "job-materialize", "chunk-1"
	body := []byte("parquet-fixture")
	sum := sha256.Sum256(body)
	files := []FileInfo{{Path: "token_transfers/part.parquet", Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}}
	manifest, _ := json.Marshal(map[string]any{"job_id": id, "chunk_id": chunk, "row_count": 1, "files": files})
	ctx := context.Background()
	_ = store.Put(ctx, completedChunkDir(id, chunk)+"/manifest.json", manifest)
	_ = store.Put(ctx, completedChunkDir(id, chunk)+"/_SUCCESS", []byte(`{"completed":true}`))
	_ = store.Put(ctx, leasedChunkDir(id, chunk)+"/token_transfers/part.parquet", body)
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
	m.Reconcile(context.Background())
	time.Sleep(350 * time.Millisecond)
	if m.Status().State == WorkerAbsent {
		t.Fatal("idle reaper must not remove worker while pending job exists")
	}
}
