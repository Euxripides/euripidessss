# SQD Provider V1.4.2 — 测试详细说明

> 测试套件：19 个用例，覆盖 CircuitBreaker(7) + Scheduler(6) + Checkpoint(6)，全部通过，零回归。

---

## 一、Circuit Breaker 熔断器测试（7 个）

**文件**：`internal/datasource/sqd/circuit_breaker_test.go`

### 1. `TestCircuitBreaker_InitialState`

验证熔断器创建后的默认状态。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | `NewCircuitBreaker()` | `State() == CircuitClosed` |

```
NewCircuitBreaker → state == CLOSED
```

---

### 2. `TestCircuitBreaker_Allow_Closed`

验证闭合状态下请求正常放行。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | 创建熔断器 | `state == CLOSED` |
| 2 | `Allow()` | 返回 `nil`（允许通行） |

```
CLOSED → Allow() → nil
```

---

### 3. `TestCircuitBreaker_OpenAfterFailures`

验证连续失败达到阈值后熔断器自动开路。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | 创建熔断器 `MaxFailures=3` | `state == CLOSED` |
| 2 | `RecordFailure()` × 3 | |
| 3 | 检查状态 | `state == OPEN` |
| 4 | `Allow()` | 返回 `ErrCircuitOpen` |

```
Config: MaxFailures=3
RecordFailure × 3 → OPEN → Allow() == ErrCircuitOpen
```

---

### 4. `TestCircuitBreaker_HalfOpen_ThenClose`

验证完整的故障→恢复周期：开路 → 等待 → 半开探测 → 成功 → 闭合。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | 创建熔断器 `MaxFailures=2, OpenDuration=50ms, MinSuccesses=1` | `CLOSED` |
| 2 | `RecordFailure()` × 2 | `OPEN` |
| 3 | `time.Sleep(60ms)` | 超过 openDuration |
| 4 | 第 1 次 `Allow()` | 进入 `HALF_OPEN`，probe 被消费，返回 `nil` |
| 5 | 第 2 次 `Allow()` | 返回 `ErrCircuitOpen`（probe 已用完） |
| 6 | `RecordSuccess()` | `state == CLOSED` |

```
OPEN ──(60ms)──► HALF_OPEN(probe) ──RecordSuccess──► CLOSED
                      │
                      └── 第2次Allow() → ErrCircuitOpen
```

---

### 5. `TestCircuitBreaker_HalfOpen_FailAgain`

验证半开状态探测失败后重新开路。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | 创建熔断器 `MaxFailures=2, OpenDuration=50ms, MinSuccesses=2` | `CLOSED` |
| 2 | `RecordFailure()` × 2 | `OPEN` |
| 3 | `time.Sleep(60ms)` | 超过 openDuration |
| 4 | `Allow()` | 返回 `nil`（probe 通过） |
| 5 | `RecordFailure()` | `state == OPEN`（再次开路） |

```
HALF_OPEN ──RecordFailure──► OPEN（探测失败，再次熔断）
```

---

### 6. `TestCircuitBreaker_Reset`

验证强制重置功能。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | 创建熔断器 `MaxFailures=2` | `CLOSED` |
| 2 | `RecordFailure()` × 2 | `OPEN` |
| 3 | `Reset()` | `state == CLOSED` |
| 4 | `Allow()` | 返回 `nil` |

```
OPEN ──Reset()──► CLOSED → Allow() == nil
```

---

### 7. `TestCircuitBreaker_Stats`

验证统计信息正确归零。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | `RecordFailure()` | |
| 2 | `RecordSuccess()` | `Stats.Failures == 0` |
| 3 | 检查状态字符串 | `Stats.State == "CLOSED"` |

```
RecordFailure + RecordSuccess → Failures=0, State="CLOSED"
```

---

## 二、Scheduler 调度器测试（6 个）

**文件**：`internal/datasource/sqd/scheduler/scheduler_test.go`

### 1. `TestScheduler_SubmitAndDone`

验证基本的提交→授予→完成流程。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | `Submit("task-1", KindStream, PriorityNormal)` | `done` channel 在 1s 内关闭 |
| 2 | 检查 `Stats()` | `ActiveStreams == 1` |
| 3 | `Done(task)` | |
| 4 | 检查 `Stats()` | `ActiveStreams == 0, Completed == 1` |

```
Submit → done关闭 → Active=1 → Done → Active=0, Completed=1
```

---

### 2. `TestScheduler_MaxSmallJobs`

验证并发限制：`MaxSmallJobs=1` 时第二个任务排队等待。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | `Submit("task-1", KindStream)` | 立即授予（`done` 关闭） |
| 2 | `Submit("task-2", KindStream)` | 100ms 内**不**授予 |
| 3 | `Done(task-1)` | |
| 4 | 等待 task-2 | 1s 内被授予 |

```
Config: MaxSmallJobs=1
task1 → done; task2 → blocked; Done(task1) → task2.done
```

---

### 3. `TestScheduler_PriorityOrder`

验证优先级调度：高优先级任务先于低优先级。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | 提交 `holder`（占槽位） | 立即授予 |
| 2 | `Submit("low", PriorityLow)` | 排队 |
| 3 | `Submit("high", PriorityHigh)` | 排队 |
| 4 | `Done(holder)` | |
| 5 | 检查谁先被授予 | `high` 先于 `low` |

```
holder占槽 → Submit(LOW) → Submit(HIGH) → Done(holder) → HIGH先, LOW后
```

---

### 4. `TestScheduler_Cancel`

验证取消排队中的任务。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | 提交 `holder`（占槽位） | 立即授予 |
| 2 | `Submit("queued")` | 排队 |
| 3 | `Cancel("queued")` | 返回 `true` |
| 4 | `queued.WaitFor()` | 返回 error（已取消） |

```
holder占槽 → Submit(queued) → Cancel(queued)=true → WaitFor 返回 error
```

---

### 5. `TestScheduler_QueueFull`

验证队列满时拒绝新任务。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | 配置 `QueueSize=2`，占满槽位 | |
| 2 | 提交 2 个任务填满队列 | 成功 |
| 3 | 提交第 3 个任务 | 返回 `ErrSchedulerFull` |
| 4 | `Stats().Rejected` | `== 1` |

```
QueueSize=2 → 2个排队成功 → 第3个 ErrSchedulerFull → Rejected=1
```

---

### 6. `TestScheduler_Stats`

验证 `KindLargeJob` 类型统计正确。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | `Submit(task, KindLargeJob, PriorityHigh)` | `done` 关闭 |
| 2 | `Stats()` | `ActiveLargeJobs == 1, Queued == 0` |

```
Submit(KindLargeJob) → ActiveLargeJobs=1, Queued=0
```

---

## 三、Checkpoint 断点续传测试（6 个）

**文件**：`internal/parquetdownload/sqd_checkpoint_test.go`

### 1. `TestSQDCheckpointStore_CreateAndLoad`

验证创建和加载 checkpoint。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | `Create("job-001", "bsc", 1000000, 1100000, 25000)` | |
| 2 | 检查状态 | `Status == "pending"` |
| 3 | 检查分块 | `PendingChunks` 共 5 个 |
| 4 | 检查统计 | `TotalBlocks == 100001` |
| 5 | `Load("job-001")` | JobID 一致 |

```
Create(1M→1.1M, chunk=25k) → 5 chunks, Total=100001 → Load 一致
```

**分块明细**：

| Chunk | From | To | 块大小 |
|-------|------|----|--------|
| 1 | 1,000,000 | 1,024,999 | 25,000 |
| 2 | 1,025,000 | 1,049,999 | 25,000 |
| 3 | 1,050,000 | 1,074,999 | 25,000 |
| 4 | 1,075,000 | 1,099,999 | 25,000 |
| 5 | 1,100,000 | 1,100,000 | 1 |

---

### 2. `TestSQDCheckpointStore_AdvanceChunk`

验证分步推进 chunk。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | `Create("job-002", 100, 200, 50)` | 3 chunks |
| 2 | `AdvanceChunk({100,149})` | `CurrentBlock=149, Status=in_progress, CompletedBlocks=50` |
| 3 | `AdvanceChunk({150,199})` | |
| 4 | `AdvanceChunk({200,200})` | `Status=completed, PendingChunks=[], CompletedBlocks=101` |

```
全部3个chunk推进 → Status: pending → in_progress → completed
```

---

### 3. `TestSQDCheckpointStore_MarkFailed`

验证失败标记。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | `Create("job-err", 1, 100, 50)` | |
| 2 | `MarkFailed("503 No available workers")` | |
| 3 | `Load("job-err")` | `Status == "failed"`, `Error == "503 No available workers"` |

```
MarkFailed("503...") → Status=failed, Error 保留完整消息
```

---

### 4. `TestSQDCheckpointStore_Delete`

验证删除 checkpoint。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | `Create("job-del", 1, 100, 50)` | |
| 2 | 检查文件 | JSON 文件存在 |
| 3 | `Delete("job-del")` | |
| 4 | 检查文件 | JSON 文件不存在 |

```
Create → 文件存在 → Delete → 文件不存在
```

---

### 5. `TestSplitBlockRange`

验证分块算法边界情况。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | `splitBlockRange(1, 10, 3)` | 4 个 chunks |

| Chunk | From | To |
|-------|------|----|
| 0 | 1 | 3 |
| 1 | 4 | 6 |
| 2 | 7 | 9 |
| 3 | 10 | 10 |

```
splitBlockRange(1,10,3) → [{1,3},{4,6},{7,9},{10,10}]
```

- 验证最后一个不满 chunkSize 的边界处理正确
- 验证总 chunk 数 = `ceil((10-1+1)/3)` = 4

---

### 6. `TestSQDCheckpointStore_Recovery` 🔥

**核心场景**：模拟 503 中断后从断点恢复续跑。

| 步骤 | 操作 | 预期结果 |
|------|------|----------|
| 1 | `Create("recovery-1", 1000, 1249, 50)` | 5 chunks: `[1000-1049],[1050-1099],[1100-1149],[1150-1199],[1200-1249]` |
| 2 | `AdvanceChunk({1000,1049})` | 完成第 1 个 |
| 3 | `AdvanceChunk({1050,1099})` | 完成第 2 个 |
| 4 | `MarkFailed("503 No available workers")` | 🔴 模拟 SQD 503 中断 |
| 5 | `Load("recovery-1")` | 读取断点 |
| 6 | 检查 `PendingChunks` | 剩余 3 个 |
| 7 | 检查首个 pending chunk | `From == 1100`（正确续跑位置） |
| 8 | 逐项 `AdvanceChunk` 剩余 3 个 | |
| 9 | `Load("recovery-1")` | `Status == "completed"` |

```
中断前: ████████░░░░░░░░░░  (2/5 chunks done)
   ↓ 503
恢复后: ████████░░░░░░░░░░  Load → Pending[0].From=1100
   ↓ 续跑
完成:  ████████████████████  (5/5, Status=completed)
```

---

## 汇总

| 测试类别 | 用例数 | 覆盖场景 |
|----------|--------|----------|
| CircuitBreaker | 7 | 初始态、闭合通行、开路拒绝、半开恢复、半开再失败、强制重置、统计归零 |
| Scheduler | 6 | 提交完成、并发限制、优先级调度、取消任务、队列满拒绝、类型统计 |
| Checkpoint | 6 | 创建加载、分步推进、失败标记、删除清理、分块算法、503 恢复续跑 |
| **合计** | **19** | |

### 验证命令

```bash
# 全部测试
go test ./internal/... -count=1

# 仅新增测试
go test ./internal/datasource/sqd/... ./internal/parquetdownload/... -v -count=1
```

### 结果

```
ok  github.com/etl/backend/internal/datasource/sqd              ✓ (含 CircuitBreaker × 7)
ok  github.com/etl/backend/internal/datasource/sqd/scheduler    ✓ (含 Scheduler × 6)
ok  github.com/etl/backend/internal/parquetdownload              ✓ (含 Checkpoint × 6)

全部 19 个新增用例 PASS，原有测试零回归
```
