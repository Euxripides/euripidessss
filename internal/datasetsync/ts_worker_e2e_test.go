package datasetsync

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/etl/backend/internal/s3store"
)

// TestTSWorkerProtocolE2E 跨语言协议验证（Phase 4）：
// Go 控制面写入 pending request → TS Job-driven Worker（R2_BACKEND=local）领取并完成
// → Go Local Sync 读取 completed manifest 并登记 Registry。
// 需要真实 Node 与已构建的 E:\Code\Processor-only\lib\worker.js；
// 仅当 RUN_TS_WORKER_E2E=1 时执行（本地开发验证，CI 默认跳过）。
func TestTSWorkerProtocolE2E(t *testing.T) {
	if os.Getenv("RUN_TS_WORKER_E2E") != "1" {
		t.Skip("设置 RUN_TS_WORKER_E2E=1 执行跨语言协议验证")
	}
	workerProject := os.Getenv("SQD_CLOUD_WORKER_DIR")
	if workerProject == "" {
		workerProject = `E:\Code\Processor-only`
	}
	if _, err := os.Stat(filepath.Join(workerProject, "lib", "worker.js")); err != nil {
		t.Fatalf("worker.js 未构建: %v", err)
	}
	root := t.TempDir()
	store := s3store.NewLocalStore(filepath.Join(root, "store"))
	ctx := context.Background()

	request := map[string]any{
		"job_id": "e2e-job-1", "chunk_id": "chunk-1", "chain_id": 56,
		"chain_key": "bsc", "dataset": "token_transfer",
		"addresses":      []string{"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		"token_contract": "0x55d398326f99059ff775485246999027b3197955",
		"from_block":     114000000, "to_block": 114000010,
		"priority": 90, "attempt": 1,
	}
	payload, _ := json.MarshalIndent(request, "", "  ")
	pendingKey := "bsc/jobs/pending/e2e-job-1/chunk-1/request.json"
	if err := store.Put(ctx, pendingKey, payload); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("node", filepath.Join("lib", "worker.js"))
	cmd.Dir = workerProject
	cmd.Env = append(os.Environ(),
		"R2_BACKEND=local",
		"R2_LOCAL_ROOT="+filepath.Join(root, "store"),
		"WORKER_RUNNER_CMD=node "+filepath.Join("scripts", "mock-runner.js"),
		"WORKER_PROJECT_DIR="+workerProject,
		"WORKER_WORK_ROOT="+filepath.Join(root, "worker-data"),
		"POLL_INTERVAL_MS=300",
		"HEARTBEAT_MS=300",
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	deadline := time.Now().Add(90 * time.Second)
	completedKey := "bsc/jobs/completed/e2e-job-1/chunk-1/_SUCCESS"
	for time.Now().Before(deadline) {
		if ok, _ := store.Exists(ctx, completedKey); ok {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if ok, _ := store.Exists(ctx, completedKey); !ok {
		out, _ := cmd.Process.Wait()
		t.Fatalf("TS Worker 未在超时内完成（exit=%v）", out)
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()

	registry, _ := NewRegistry(filepath.Join(root, "registry.json"))
	syncer := NewSyncer(store, registry, filepath.Join(root, "sync"), nil)
	results, err := syncer.SyncAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ChunkKey != "e2e-job-1/chunk-1" {
		t.Fatalf("sync results = %+v", results)
	}
	if !registry.Has("e2e-job-1/chunk-1") {
		t.Fatal("registry entry missing after sync")
	}
}
