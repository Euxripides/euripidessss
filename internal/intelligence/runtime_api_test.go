package intelligence

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/etl/backend/internal/investigationstore"
)

// ── Runtime V2 API 测试（设计 §14）──

// newRuntimeHandler 构建带 runtime 端点的 handler（fake 数据源 + TaskStore）。
func newRuntimeHandler(t *testing.T) (*InvestigationHandler, *InvestigationAgent) {
	t.Helper()
	dir := t.TempDir()
	agent := newTestAgent()
	agent.tasks = investigationstore.NewTaskStore(filepath.Join(dir, "tasks"))
	handler := NewInvestigationHandler(agent, NewRequestStore(""), NewIntentAnalyzer())
	return handler, agent
}

func TestRuntimeStatusEndpoint(t *testing.T) {
	handler, agent := newRuntimeHandler(t)
	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// 等待调查终态（避免后台 goroutine 在 TempDir 清理时仍写 TaskStore）
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cur, ok := agent.Get(inv.ID); ok && TerminalStatuses[cur.Status] {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	rr := doJSON(handler, http.MethodGet, "/investigation/"+inv.ID+"/runtime/status", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var st struct {
		InvestigationID string `json:"investigation_id"`
		State           string `json:"state"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if st.InvestigationID != inv.ID {
		t.Fatalf("ID 不一致: %s", st.InvestigationID)
	}
	if st.State != "CREATED" && st.State != "PLANNED" && st.State != "RUNNING" && st.State != "COMPLETED" {
		t.Fatalf("状态异常: %s", st.State)
	}
	// 不存在的调查 → 404
	rr = doJSON(handler, http.MethodGet, "/investigation/inv-999/runtime/status", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("不存在调查应 404, got %d", rr.Code)
	}
}

func TestRuntimeTasksEndpoint(t *testing.T) {
	handler, agent := newRuntimeHandler(t)
	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// 等待调查终态（避免后台 goroutine 在 TempDir 清理时仍写文件）
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cur, ok := agent.Get(inv.ID); ok && TerminalStatuses[cur.Status] {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	rr := doJSON(handler, http.MethodGet, "/investigation/"+inv.ID+"/runtime/tasks", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var resp struct {
		State string              `json:"state"`
		Tasks []InvestigationTask `json:"tasks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if resp.State == "" {
		t.Fatal("应返回 state")
	}
	_ = resp.Tasks
}

func TestRuntimeStartEndpoint(t *testing.T) {
	handler, agent := newRuntimeHandler(t)
	// 手工构造带 RUNNING 超时任务的持久化状态（模拟崩溃恢复）
	agent.persistTasks("inv-1", []InvestigationTask{
		{ID: "t1", Type: TaskPathTrace, Status: TaskRunning, Target: addrA, Round: 1, TimeoutSec: 10, StartedAt: time.Now().Unix() - 100},
		{ID: "t2", Type: TaskRiskScan, Status: TaskPending, Target: addrA, Round: 1},
	})
	// 调查需存在（内存注册）
	agent.active["inv-1"] = &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationRunning}

	rr := doJSON(handler, http.MethodPost, "/investigation/inv-1/runtime/start", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		State          string `json:"state"`
		RecoveredTasks int    `json:"recovered_tasks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if resp.RecoveredTasks != 2 {
		t.Fatalf("应恢复 2 个任务, got %d", resp.RecoveredTasks)
	}
	// Resume 已触发后台恢复执行：轮询等待任务终态（t1 超时→重试→终态，t2 pending→执行→终态）
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		inv, _ := agent.Get("inv-1")
		allDone := true
		for _, tk := range inv.Tasks {
			if tk.Status == TaskPending || tk.Status == TaskRunning {
				allDone = false
			}
		}
		if allDone {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// 等待后台 goroutine 落盘完成（无 .tmp 残留且连续稳定），避免 TempDir 清理竞态
	taskDir := filepath.Join(agent.tasks.Dir(), "inv-1")
	stable := 0
	for time.Now().Before(deadline) {
		tmpLeft, _ := filepath.Glob(filepath.Join(taskDir, "*.tmp"))
		entries, _ := os.ReadDir(taskDir)
		if len(tmpLeft) == 0 && len(entries) >= 2 {
			stable++
			if stable >= 3 {
				return
			}
		} else {
			stable = 0
		}
		time.Sleep(50 * time.Millisecond)
	}
	lastInv, _ := agent.Get("inv-1")
	t.Fatalf("恢复任务未在超时内完成: %+v", lastInv)
}

func TestRuntimeStartTerminalConflict(t *testing.T) {
	handler, agent := newRuntimeHandler(t)
	agent.active["inv-1"] = &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationCompleted}
	rr := doJSON(handler, http.MethodPost, "/investigation/inv-1/runtime/start", "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("终态调查应 409, got %d", rr.Code)
	}
}
