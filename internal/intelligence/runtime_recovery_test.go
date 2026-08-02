package intelligence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/investigationstore"
)

// ── Runtime 恢复测试（V2 设计 §11）──

func TestTaskStorePersistsRuntimeFields(t *testing.T) {
	dir := t.TempDir()
	taskStore := investigationstore.NewTaskStore(filepath.Join(dir, "tasks"))
	agent := &InvestigationAgent{tasks: taskStore}

	tasks := []InvestigationTask{
		{
			ID: "t1", Type: TaskPathTrace, Status: TaskRunning, Target: addrA, Round: 1,
			Dependencies: []string{"p1"}, MaxRetries: 2, RetryCount: 1, TimeoutSec: 30, StartedAt: time.Now().Unix(),
		},
		{ID: "t2", Type: TaskRiskScan, Status: TaskDone, Result: "ok", Round: 1},
	}
	agent.persistTasks("inv-1", tasks)

	// 重载：新 store 实例（模拟重启）
	agent2 := &InvestigationAgent{tasks: investigationstore.NewTaskStore(filepath.Join(dir, "tasks"))}
	recovered := agent2.RecoverTasks("inv-1")
	if len(recovered) != 2 {
		t.Fatalf("应恢复 2 个任务, got %d", len(recovered))
	}
	// Runtime 字段往返一致
	var t1 *InvestigationTask
	for i := range recovered {
		if recovered[i].ID == "t1" {
			t1 = &recovered[i]
		}
	}
	if t1 == nil {
		t.Fatal("t1 应恢复")
	}
	if t1.Status != TaskRunning || t1.Target != addrA || len(t1.Dependencies) != 1 ||
		t1.MaxRetries != 2 || t1.RetryCount != 1 || t1.TimeoutSec != 30 {
		t.Fatalf("Runtime 字段往返不一致: %+v", t1)
	}
}

func TestRecoverHeartbeatTimeout(t *testing.T) {
	dir := t.TempDir()
	taskStore := investigationstore.NewTaskStore(filepath.Join(dir, "tasks"))
	agent := &InvestigationAgent{tasks: taskStore}

	// RUNNING 且已超时（StartedAt 远早于现在）
	agent.persistTasks("inv-1", []InvestigationTask{
		{ID: "t1", Type: TaskPathTrace, Status: TaskRunning, Target: addrA, Round: 1,
			TimeoutSec: 10, StartedAt: time.Now().Unix() - 100}, // 已运行 100s > 10s
		{ID: "t2", Type: TaskRiskScan, Status: TaskRunning, Target: addrA, Round: 1,
			TimeoutSec: 60, StartedAt: time.Now().Unix() - 5}, // 未超时
	})
	recovered := agent.RecoverTasks("inv-1")
	var t1, t2 *InvestigationTask
	for i := range recovered {
		switch recovered[i].ID {
		case "t1":
			t1 = &recovered[i]
		case "t2":
			t2 = &recovered[i]
		}
	}
	if t1 == nil || t1.Status != TaskFailed {
		t.Fatalf("超时任务应标记 failed: %+v", t1)
	}
	if t1.Error == "" {
		t.Fatal("超时任务应带错误说明")
	}
	if t2 == nil || t2.Status != TaskRunning {
		t.Fatalf("未超时任务应保持 running: %+v", t2)
	}
	// 落盘状态同步更新（重启后读回 failed）
	rec, ok := taskStore.Get("inv-1/t1")
	if !ok || rec.Status != TaskFailed {
		t.Fatalf("超时标记应落盘: %+v", rec)
	}
}

func TestRecoverNoStore(t *testing.T) {
	agent := &InvestigationAgent{} // tasks == nil
	if got := agent.RecoverTasks("inv-1"); got != nil {
		t.Fatalf("无 store 应返回 nil, got %v", got)
	}
}

func TestResumeExecutesRecoveredTasks(t *testing.T) {
	dir := t.TempDir()
	taskStore := investigationstore.NewTaskStore(filepath.Join(dir, "tasks"))
	agent := newTestAgent()
	agent.tasks = taskStore

	// 持久化：一个超时 RUNNING 任务（配置重试，应恢复为 failed 重试）+ 一个 pending 任务
	agent.persistTasks("inv-1", []InvestigationTask{
		{ID: "t1", Type: TaskTokenAnalysis, Status: TaskRunning, Target: addrA, Round: 1, TimeoutSec: 10, MaxRetries: 1, StartedAt: time.Now().Unix() - 100},
		{ID: "t2", Type: TaskRiskScan, Status: TaskPending, Target: addrA, Round: 1},
	})
	agent.active["inv-1"] = &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationRunning}

	n, err := agent.Resume("inv-1")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if n != 2 {
		t.Fatalf("应恢复 2 个任务, got %d", n)
	}
	// 恢复执行后：超时任务重试成功或失败（终态），pending 任务被执行（终态）
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
	// 等待后台 goroutine 落盘完成（无 .tmp 残留且任务文件数稳定），避免 TempDir 清理竞态
	stable := 0
	for time.Now().Before(deadline) {
		tmpLeft, _ := filepath.Glob(filepath.Join(dir, "tasks", "inv-1", "*.tmp"))
		entries, _ := os.ReadDir(filepath.Join(dir, "tasks", "inv-1"))
		if len(tmpLeft) == 0 && len(entries) >= 2 {
			stable++
			if stable >= 2 {
				break
			}
		} else {
			stable = 0
		}
		time.Sleep(50 * time.Millisecond)
	}
	// resumeRun 终态收尾：调查应进入 COMPLETED（防悬挂 RUNNING）
	inv, _ := agent.Get("inv-1")
	if inv.Status != InvestigationCompleted {
		t.Fatalf("恢复完成后调查应 COMPLETED, got %s", inv.Status)
	}
}

func TestResumeRejectsTerminal(t *testing.T) {
	agent := newTestAgent()
	agent.active["inv-1"] = &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationCompleted}
	if _, err := agent.Resume("inv-1"); err == nil {
		t.Fatal("终态调查 Resume 应报错")
	}
	if _, err := agent.Resume("inv-999"); err == nil {
		t.Fatal("不存在调查 Resume 应报错")
	}
}

func TestResumeIdempotentGuard(t *testing.T) {
	dir := t.TempDir()
	taskStore := investigationstore.NewTaskStore(filepath.Join(dir, "tasks"))
	agent := newTestAgent()
	agent.tasks = taskStore
	agent.persistTasks("inv-1", []InvestigationTask{
		{ID: "t1", Type: TaskTokenAnalysis, Status: TaskPending, Target: addrA, Round: 1},
	})
	agent.active["inv-1"] = &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationRunning}
	// 标记为正在恢复（模拟并发触发中）
	agent.mu.Lock()
	if agent.resuming == nil {
		agent.resuming = make(map[string]bool)
	}
	agent.resuming["inv-1"] = true
	agent.mu.Unlock()
	if _, err := agent.Resume("inv-1"); err == nil {
		t.Fatal("正在恢复中的调查应拒绝重复 Resume")
	}
}

func TestResumeRejectsRunningInvestigation(t *testing.T) {
	dir := t.TempDir()
	taskStore := investigationstore.NewTaskStore(filepath.Join(dir, "tasks"))
	agent := newTestAgent()
	agent.tasks = taskStore
	agent.persistTasks("inv-1", []InvestigationTask{
		{ID: "t1", Type: TaskTokenAnalysis, Status: TaskPending, Target: addrA, Round: 1},
	})
	// 调查正在主循环运行中（Controller 已同步 RUNNING）
	agent.active["inv-1"] = &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationRunning}
	agent.Controller("inv-1").SetState(RuntimeRunning, "")
	if _, err := agent.Resume("inv-1"); err == nil {
		t.Fatal("运行中的调查应拒绝 Resume（防双队列并发）")
	}
}

func TestResumeRejectsTerminalInLock(t *testing.T) {
	dir := t.TempDir()
	taskStore := investigationstore.NewTaskStore(filepath.Join(dir, "tasks"))
	agent := newTestAgent()
	agent.tasks = taskStore
	agent.persistTasks("inv-1", []InvestigationTask{
		{ID: "t1", Type: TaskTokenAnalysis, Status: TaskPending, Target: addrA, Round: 1},
	})
	// 锁外 Get 副本仍是运行态，但 Controller 已是终态（模拟主循环在检查窗口内完成）
	agent.active["inv-1"] = &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationRunning}
	agent.Controller("inv-1").SetState(RuntimeCompleted, StopTargetFound)
	if _, err := agent.Resume("inv-1"); err == nil {
		t.Fatal("锁内终态检查应拒绝 Resume（TOCTOU 防护）")
	}
}

func TestResumeRejectsPlanning(t *testing.T) {
	dir := t.TempDir()
	taskStore := investigationstore.NewTaskStore(filepath.Join(dir, "tasks"))
	agent := newTestAgent()
	agent.tasks = taskStore
	agent.persistTasks("inv-1", []InvestigationTask{
		{ID: "t1", Type: TaskTokenAnalysis, Status: TaskPending, Target: addrA, Round: 1},
	})
	// 主循环已进入规划阶段（含 AI 规划耗时窗口）：恢复必须拒绝（防双队列并发）
	agent.active["inv-1"] = &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationPlanning}
	agent.Controller("inv-1").SetState(RuntimePlanned, "")
	if _, err := agent.Resume("inv-1"); err == nil {
		t.Fatal("PLANNED 阶段的调查应拒绝 Resume（主循环持有执行权）")
	}
}

func TestResumeRunYieldsWhenMainLoopTookOver(t *testing.T) {
	dir := t.TempDir()
	taskStore := investigationstore.NewTaskStore(filepath.Join(dir, "tasks"))
	agent := newTestAgent()
	agent.tasks = taskStore
	agent.persistTasks("inv-1", []InvestigationTask{
		{ID: "t1", Type: TaskTokenAnalysis, Status: TaskPending, Target: addrA, Round: 1},
	})
	// 构造 Start 后立即并发 runtime/start 的窗口：controller 已进 PLANNED（主循环接管），
	// Resume 放行（绕过锁内检查的竞态已由 resumeRun 入口复核兜底）
	agent.active["inv-1"] = &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationRunning}
	agent.Controller("inv-1").SetState(RuntimePlanned, "")
	// 直接调用 resumeRun（绕过 Resume 的锁内白名单检查，模拟竞态窗口内已放行）
	recovered := []InvestigationTask{{ID: "t1", Type: TaskTokenAnalysis, Status: TaskPending, Target: addrA, Round: 1}}
	agent.resumeRun("inv-1", recovered, agent.snapshot())
	// resumeRun 应放弃执行：任务状态不被修改、调查状态不变成 COMPLETED
	inv, _ := agent.Get("inv-1")
	if inv.Status == InvestigationCompleted {
		t.Fatal("主循环接管后 resumeRun 不应完成调查")
	}
	// resuming 标记应被 defer 清理
	agent.mu.Lock()
	stillResuming := agent.resuming["inv-1"]
	agent.mu.Unlock()
	if stillResuming {
		t.Fatal("resumeRun 放弃后应清理 resuming 标记")
	}
}

func TestResumeFinishesWhenNoRecoverableTasks(t *testing.T) {
	// 场景：主循环被让位中止（调查 WAITING），但恢复路径无持久化任务 → 必须收尾防悬挂
	agent := newTestAgent()
	agent.active["inv-1"] = &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationWaiting}
	agent.Controller("inv-1").SetState(RuntimeWaiting, "")
	// 无 TaskStore（RecoverTasks 返回 nil）→ Resume 空分支应收尾
	n, err := agent.Resume("inv-1")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if n != 0 {
		t.Fatalf("应恢复 0 个任务, got %d", n)
	}
	inv, _ := agent.Get("inv-1")
	if inv.Status != InvestigationCompleted {
		t.Fatalf("无任务可恢复且调查 WAITING 时应收尾 COMPLETED, got %s", inv.Status)
	}
}

func TestResumeRunFinishesWhenAllTasksTerminal(t *testing.T) {
	// 场景：恢复队列全部终态不可重试（PendingCount==0）→ resumeRun 应收尾防悬挂
	dir := t.TempDir()
	taskStore := investigationstore.NewTaskStore(filepath.Join(dir, "tasks"))
	agent := newTestAgent()
	agent.tasks = taskStore
	agent.persistTasks("inv-1", []InvestigationTask{
		{ID: "t1", Type: TaskTokenAnalysis, Status: TaskDone, Result: "ok", Target: addrA, Round: 1},
	})
	agent.active["inv-1"] = &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationWaiting}
	agent.Controller("inv-1").SetState(RuntimeWaiting, "")
	// 直接调用 resumeRun（任务全为 done，无 pending 可执行）
	recovered := agent.RecoverTasks("inv-1")
	agent.resumeRun("inv-1", recovered, agent.snapshot())
	inv, _ := agent.Get("inv-1")
	if inv.Status != InvestigationCompleted {
		t.Fatalf("全部任务终态时 resumeRun 应收尾 COMPLETED, got %s", inv.Status)
	}
}

func TestLoopEngineYieldsWhenResuming(t *testing.T) {
	// 主循环在 resuming 期间调用 setStage(RUNNING) 应被让位为 WAITING，
	// 且 LoopEngine 轮次循环检测到 WAITING 后中止（不执行任务）
	agent := newTestAgent()
	inv := &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationCreated}
	agent.active["inv-1"] = inv
	// 模拟 resumeRun 持有执行权
	agent.mu.Lock()
	if agent.resuming == nil {
		agent.resuming = make(map[string]bool)
	}
	agent.resuming["inv-1"] = true
	agent.mu.Unlock()
	agent.Controller("inv-1").SetState(RuntimeWaiting, "")

	e := NewLoopEngine()
	cfg := DefaultConfig()
	cfg.UseAI = false
	snap := agent.snapshot()
	err := e.Run(context.Background(), agent, inv, snap)
	if err != nil {
		t.Fatalf("Run 应正常返回（让位中止非错误）: %v", err)
	}
	// 调查应保持 WAITING（主循环未推进到 COMPLETED）
	if inv.Status != InvestigationWaiting && inv.Status != InvestigationCompleted {
		t.Fatalf("让位后调查状态异常: %s", inv.Status)
	}
	// 主循环不应执行任何任务（队列为空）
	if len(inv.Tasks) != 0 {
		t.Fatalf("让位后主循环不应入队任务: %+v", inv.Tasks)
	}
	// 强覆盖（should-fix）：规划前让位 → Plan 不应被设置（若检查在规划后，
	// 此处 inv.Plan 非 nil 即失败，锁定"规划前立即让位"语义）
	if inv.Plan != nil {
		t.Fatalf("让位后主循环不应完成规划: %+v", inv.Plan)
	}
}

func TestLoopEngineYieldsMidRound(t *testing.T) {
	// 场景：主循环启动时 resuming 已置位 → setStage(Planning) 即被降级 WAITING，
	// 规划前检查命中并中止（轮内/轮首检查需阻塞 executor 的真实并发时序，
	// 属并发设计防御层，由 resumeRun 入口锁内复核兜底，此处覆盖最简路径）
	agent := newTestAgent()
	inv := &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationCreated}
	agent.active["inv-1"] = inv
	// 预置 resuming（模拟恢复执行已持有执行权）
	agent.mu.Lock()
	if agent.resuming == nil {
		agent.resuming = make(map[string]bool)
	}
	agent.resuming["inv-1"] = true
	agent.mu.Unlock()

	e := NewLoopEngine()
	cfg := DefaultConfig()
	cfg.UseAI = false
	err := e.Run(context.Background(), agent, inv, agent.snapshot())
	if err != nil {
		t.Fatalf("Run 应正常返回（让位中止非错误）: %v", err)
	}
	// 主循环不应执行任何任务（让位后未入队）
	if len(inv.Tasks) != 0 {
		t.Fatalf("让位后主循环不应执行任务: %+v", inv.Tasks)
	}
	// 状态应为 WAITING（setStage 被降级）
	if inv.Status != InvestigationWaiting {
		t.Fatalf("让位后状态应为 WAITING, got %s", inv.Status)
	}
}

func TestLoopEngineYieldsWhenTerminal(t *testing.T) {
	// 场景：resumeRun 收尾完成（controller 已 COMPLETED）后主循环仍触达检查点，
	// isYielding 终态分支应中止主循环（防终态调查被复活重复执行任务）
	agent := newTestAgent()
	inv := &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationCreated}
	agent.active["inv-1"] = inv
	agent.Controller("inv-1").SetState(RuntimeCompleted, StopTargetFound)
	agent.mu.Lock()
	if agent.resuming == nil {
		agent.resuming = make(map[string]bool)
	}
	agent.resuming["inv-1"] = true
	agent.mu.Unlock()

	e := NewLoopEngine()
	cfg := DefaultConfig()
	cfg.UseAI = false
	err := e.Run(context.Background(), agent, inv, agent.snapshot())
	if err != nil {
		t.Fatalf("Run 应正常返回（终态让位中止非错误）: %v", err)
	}
	// 主循环不应执行任何任务（终态即中止）
	if len(inv.Tasks) != 0 {
		t.Fatalf("终态让位后主循环不应执行任务: %+v", inv.Tasks)
	}
	// controller 应保持终态（权威状态，主循环未复活调查）
	if st := agent.Controller("inv-1").State(); st != RuntimeCompleted {
		t.Fatalf("终态调查不应被复活（controller 权威）, got %s", st)
	}
	// 内存状态不应被主循环改写成执行阶段（setStage 已跳过更新）
	if inv.Status == InvestigationPlanning || inv.Status == InvestigationRunning {
		t.Fatalf("主循环不应推进终态调查: %s", inv.Status)
	}
}

func TestSetStageSkipsWhenControllerTerminal(t *testing.T) {
	// 场景：controller 已终态（resumeRun 收尾完成）且 resuming 已清除——
	// setStage 的终态保护（独立于 resuming 分支）应跳过内存更新，防复活
	agent := newTestAgent()
	inv := &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationCompleted}
	agent.active["inv-1"] = inv
	agent.Controller("inv-1").SetState(RuntimeCompleted, StopTargetFound)

	// 主循环残留的 setStage（模拟 Analyzing→Verifying 无检查点窗口的闪回）
	agent.setStage(inv, InvestigationAnalyzing, "分析", 50)
	if inv.Status != InvestigationCompleted {
		t.Fatalf("终态保护应跳过内存更新, got %s", inv.Status)
	}
	if st := agent.Controller("inv-1").State(); st != RuntimeCompleted {
		t.Fatalf("controller 应保持终态, got %s", st)
	}
}

func TestRunSkipsFinishWhenResumeFinishedFirst(t *testing.T) {
	// 场景：主循环让位返回后、run() 收尾前，resumeRun 抢先完成收尾
	// （controller→COMPLETED、调查已入 history）→ run() 的 isYielding 检查应跳过
	// retireLocked，防终态回退覆盖（已归档 COMPLETED 回退为非终态并悬挂）
	agent := newTestAgent()
	inv := &Investigation{ID: "inv-1", Target: addrA, Status: InvestigationWaiting}
	agent.active["inv-1"] = inv
	agent.Controller("inv-1").SetState(RuntimeWaiting, "")

	// 模拟 resumeRun 抢先完成收尾
	agent.finishInvestigation("inv-1", agent.snapshot())
	if _, ok := agent.Get("inv-1"); !ok {
		t.Fatal("收尾后调查应存在（history）")
	}
	// 主循环 run() 收尾：isYielding(COMPLETED) 应跳过
	agent.run(context.Background(), inv, agent.snapshot())
	cur, ok := agent.Get("inv-1")
	if !ok {
		t.Fatal("调查应仍在（history）")
	}
	if cur.Status != InvestigationCompleted {
		t.Fatalf("终态不应被回退覆盖, got %s", cur.Status)
	}
	if st := agent.Controller("inv-1").State(); st != RuntimeCompleted {
		t.Fatalf("controller 应保持终态, got %s", st)
	}
}

func TestRecoverKeepsRetryExhaustedTerminal(t *testing.T) {
	dir := t.TempDir()
	taskStore := investigationstore.NewTaskStore(filepath.Join(dir, "tasks"))
	agent := &InvestigationAgent{tasks: taskStore}
	// 超时且已达重试上限（RetryCount=MaxRetries=2）的任务
	agent.persistTasks("inv-1", []InvestigationTask{
		{ID: "t1", Type: TaskPathTrace, Status: TaskRunning, Target: addrA, Round: 1,
			TimeoutSec: 10, MaxRetries: 2, RetryCount: 2, StartedAt: time.Now().Unix() - 100},
		{ID: "t2", Type: TaskRiskScan, Status: TaskPending, Target: addrA, Round: 1},
	})
	recovered := agent.RecoverTasks("inv-1")
	var t1 *InvestigationTask
	for i := range recovered {
		if recovered[i].ID == "t1" {
			t1 = &recovered[i]
		}
	}
	if t1 == nil || t1.Status != TaskFailed {
		t.Fatalf("达重试上限的超时任务应保持 failed: %+v", t1)
	}
	if t1.Error == "" || !strings.Contains(t1.Error, "重试上限") {
		t.Fatalf("错误信息应标注重试上限: %s", t1.Error)
	}
}

func TestBuildQueueInjectsRuntimeDefaults(t *testing.T) {
	e := NewLoopEngine()
	cfg := DefaultConfig()
	tasks := e.buildQueue(1, &InvestigationPlan{Target: addrA, Tasks: []PlannedTask{{Type: TaskPathTrace, Priority: 1}}}, []string{addrA}, agentSnapshot{}, cfg)
	if len(tasks) == 0 {
		t.Fatal("应生成任务")
	}
	for _, tk := range tasks {
		if tk.TimeoutSec != cfg.TaskTimeoutSec {
			t.Fatalf("任务应注入超时 %d, got %d", cfg.TaskTimeoutSec, tk.TimeoutSec)
		}
		if tk.MaxRetries != cfg.TaskMaxRetries {
			t.Fatalf("任务应注入重试 %d, got %d", cfg.TaskMaxRetries, tk.MaxRetries)
		}
	}
	// 显式配置的任务保持原值
	explicit := []InvestigationTask{{Type: TaskPathTrace, TimeoutSec: 5, MaxRetries: 3, Round: 1}}
	explicit = applyRuntimeDefaults(explicit, cfg)
	if explicit[0].TimeoutSec != 5 || explicit[0].MaxRetries != 3 {
		t.Fatalf("显式配置不应被覆盖: %+v", explicit[0])
	}
}
