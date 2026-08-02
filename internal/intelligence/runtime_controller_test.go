package intelligence

import (
	"context"
	"testing"
	"time"
)

// ── Runtime Controller 测试（V2 设计 §4）──

func TestRuntimeControllerStateMachine(t *testing.T) {
	c := NewRuntimeController()
	if c.State() != RuntimeCreated {
		t.Fatalf("初始状态应为 CREATED, got %s", c.State())
	}
	c.SetState(RuntimePlanned, "")
	c.SetState(RuntimeRunning, "")
	if c.State() != RuntimeRunning {
		t.Fatalf("应为 RUNNING, got %s", c.State())
	}
	// WAITING ↔ RUNNING
	c.SetState(RuntimeWaiting, "")
	if c.State() != RuntimeWaiting {
		t.Fatalf("应为 WAITING, got %s", c.State())
	}
	c.SetState(RuntimeRunning, "")
	// 终态
	c.SetState(RuntimeCompleted, StopTargetFound)
	st := c.Status("inv-1")
	if st.State != RuntimeCompleted || st.StopCode != StopTargetFound {
		t.Fatalf("终态应为 COMPLETED 且携带 StopCode: %+v", st)
	}
	// 终态不可回退
	c.SetState(RuntimeRunning, "")
	if c.State() != RuntimeCompleted {
		t.Fatalf("终态不可回退, got %s", c.State())
	}
}

func TestRuntimeControllerStopped(t *testing.T) {
	c := NewRuntimeController()
	c.SetState(RuntimeRunning, "")
	c.SetState(RuntimeStopped, StopUserCancel)
	st := c.Status("inv-1")
	if st.State != RuntimeStopped || st.StopCode != StopUserCancel {
		t.Fatalf("STOPPED 应携带 USER_CANCEL: %+v", st)
	}
	// 终态不可回退
	c.SetState(RuntimeRunning, "")
	if c.State() != RuntimeStopped {
		t.Fatalf("STOPPED 不可回退, got %s", c.State())
	}
}

func TestRuntimeControllerRefreshTasks(t *testing.T) {
	c := NewRuntimeController()
	c.RefreshTasks(1, 2, 3, 4, 10)
	st := c.Status("inv-1")
	if st.WaitingTasks != 1 || st.RunningTasks != 2 || st.CompletedTasks != 3 ||
		st.FailedTasks != 4 || st.TotalTasks != 10 {
		t.Fatalf("任务统计不一致: %+v", st)
	}
}

func TestRuntimeControllerStartedAt(t *testing.T) {
	c := NewRuntimeController()
	// 首次进入 RUNNING 记录 startedAt
	c.SetState(RuntimeRunning, "")
	st := c.Status("inv-1")
	if st.StartedAt.IsZero() {
		t.Fatal("进入 RUNNING 应记录 started_at")
	}
	// 时间应合理（当前时间附近）
	if d := time.Since(st.StartedAt); d < -time.Second || d > 10*time.Second {
		t.Fatalf("started_at 异常: %v", st.StartedAt)
	}
}

func TestStatusToRuntimeMapping(t *testing.T) {
	cases := []struct {
		status InvestigationStatus
		want   RuntimeState
	}{
		{InvestigationCreated, RuntimeCreated},
		{InvestigationPlanning, RuntimePlanned},
		{InvestigationRunning, RuntimeRunning},
		{InvestigationAnalyzing, RuntimeRunning},
		{InvestigationExpanding, RuntimeRunning},
		{InvestigationVerifying, RuntimeRunning},
		{InvestigationReporting, RuntimeRunning},
		{InvestigationWaiting, RuntimeWaiting},
		{InvestigationCompleted, RuntimeCompleted},
		{InvestigationFailed, RuntimeFailed},
		{InvestigationStopped, RuntimeStopped},
	}
	for _, c := range cases {
		if got := statusToRuntime(c.status); got != c.want {
			t.Fatalf("statusToRuntime(%s) = %s, want %s", c.status, got, c.want)
		}
	}
}

func TestAgentRuntimeStatus(t *testing.T) {
	agent := newTestAgent() // 测试辅助（fakes_test.go）
	inv, err := agent.Start(context.Background(), addrA, "bsc")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	st, ok := agent.RuntimeStatus(inv.ID)
	if !ok {
		t.Fatal("RuntimeStatus 应命中")
	}
	if st.InvestigationID != inv.ID {
		t.Fatalf("ID 不一致: %s", st.InvestigationID)
	}
	// 启动即同步控制器（异步 run 前立即可读）
	if st.State != RuntimeCreated && st.State != RuntimePlanned && st.State != RuntimeRunning {
		t.Fatalf("启动后状态应为 CREATED/PLANNED/RUNNING 之一, got %s", st.State)
	}
	// run goroutine 异步推进：轮询等待离开 CREATED
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cur, _ := agent.RuntimeStatus(inv.ID); cur.State != RuntimeCreated {
			return // 状态已推进，通过
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("调查启动后控制器状态应推进（离开 CREATED）")
}
