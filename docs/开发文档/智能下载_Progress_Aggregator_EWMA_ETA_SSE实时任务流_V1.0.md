# 智能下载下一阶段：Progress Aggregator + EWMA ETA + SSE 实时任务流 V1.0
## 十万地址级实时进度、统一进度语义、ETA 估算与前端事件总线

> 前置阶段：
>
> - BatchJob → AddressJob → DatasetJob → RangeJob 四层任务模型
> - Universal Checkpoint V3
> - Range Ledger
> - Provider Adapter
> - Discovery / Probe
> - Adaptive Range Planner
> - Scheduler V3
> - Runtime Feedback Loop
> - Validation Pipeline V3
> - Gap Repair Engine
>
> 下一步目标：
>
> **把后端已经具备的真实任务状态、Provider 状态、Range 状态、校验状态，转换成前端可稳定消费的“统一实时进度流”。**
>
> 本阶段解决：
>
> - 每个地址独立实时进度
> - 每个 Dataset 独立进度
> - 不同 Provider 的不同进度单位统一表达
> - ETA 动态估算
> - Provider 切换后 ETA 重算
> - 10 万地址不再依赖 2 秒全量轮询
> - SSE 增量推送
> - 前端只订阅变化，不全量刷新
> - Progress 与任务状态、Checkpoint、Validation 保持一致

---

# 1. 本阶段核心结论

建议正式建设：

```text
Progress Aggregator V2
+
ETA Engine V1
+
Task Event Bus V1
+
SSE Gateway V1
```

完整链路：

```text
Provider / Range Executor
↓
Raw Progress Events
↓
Progress Aggregator
↓
Address / Dataset / Batch Snapshot
↓
ETA Engine
↓
Event Coalescer
↓
SSE Gateway
↓
Frontend Task Center
```

前端不再：

```text
每 2 秒 GET 所有任务
```

而改成：

```text
首次加载 Snapshot
+
SSE 增量事件
```

---

# 2. 为什么现在必须做这一层

前面的任务模型、Checkpoint、Range、Validation 都已经定义了“真实任务”。

但如果前端还继续通过：

```text
2 秒轮询
```

读取几十个、几百个甚至几万个任务，会出现：

- 后端 API 压力大；
- 10 万地址无法扩展；
- UI 进度跳动；
- Provider 切换信息延迟；
- ETA 不稳定；
- 暂停/继续状态不同步；
- Validation / Repair 进度难展示；
- 前端为了展示信息被迫理解不同 Provider 内部语义。

因此需要一个统一的“进度层”。

---

# 3. 设计原则

## 3.1 Provider 不直接向前端发进度

错误：

```text
SQD → Frontend
RPC → Frontend
CSV → Frontend
Cloud → Frontend
```

正确：

```text
SQD
RPC
CSV
Cloud
 ↓
Progress Event
 ↓
Aggregator
 ↓
Unified Snapshot
 ↓
SSE
 ↓
Frontend
```

前端只认识统一模型。

---

# 4. 统一 ProgressEvent

建议：

```go
type ProgressEvent struct {
    EventID string

    BatchID      string
    AddressJobID string
    DatasetJobID string
    RangeID      string

    Provider string

    Kind ProgressKind

    RowsCurrent uint64
    RowsTotal   uint64

    BytesCurrent uint64
    BytesTotal   uint64

    BlocksCurrent uint64
    BlocksTotal   uint64

    PagesCurrent uint64
    PagesTotal   uint64

    RangesCurrent uint64
    RangesTotal   uint64

    RequestsCurrent uint64
    RequestsTotal   uint64

    Stage string
    Status string

    Timestamp time.Time
}
```

---

# 5. ProgressKind

统一：

```text
ROWS
BYTES
BLOCKS
PAGES
RANGES
REQUESTS
MIXED
INDETERMINATE
```

Provider 自己可以上报多个单位。

例如 SQD：

```text
blocks
rows
bytes
```

RPC：

```text
ranges
blocks
requests
rows
```

CSV：

```text
pages
rows
bytes
```

Direct：

```text
bytes
```

SQD Cloud：

```text
partitions
rows
bytes
```

---

# 6. 前端显示原则

统一显示：

```text
总体百分比
+
Provider 原生单位
+
速度
+
ETA
```

例如 SQD：

```text
68%
1.24M / 1.82M rows
12,800 / 18,400 blocks
32.4k rows/s
ETA 02:18
```

RPC：

```text
42%
82 / 160 ranges
24.5 req/s
ETA 05:42
```

Direct：

```text
51%
3.1 GB / 6.0 GB
61 MB/s
ETA 00:48
```

---

# 7. Progress Snapshot

每个层级都有 Snapshot。

```go
type ProgressSnapshot struct {
    EntityType string
    EntityID   string

    Status string
    Stage  string

    ProgressPercent float64

    PrimaryUnit string

    Current uint64
    Total   uint64

    RowsCurrent   uint64
    RowsTotal     uint64

    BytesCurrent  uint64
    BytesTotal    uint64

    BlocksCurrent uint64
    BlocksTotal   uint64

    RangesCurrent uint64
    RangesTotal   uint64

    Provider string

    Throughput ThroughputSnapshot
    ETA        ETASnapshot

    UpdatedAt time.Time
}
```

---

# 8. Dataset Progress 怎么算

优先级：

```text
1. Discovery 有 EstimatedRows
2. Range 有 EstimatedRows
3. 使用 Block Span
4. 使用 Range Count
5. 无法估算 → INDETERMINATE
```

不要简单：

```text
完成 Range 数 / 总 Range 数
```

因为不同 Range 工作量可能差几十倍。

---

# 9. Weighted Progress

建议：

```text
DatasetProgress =
Σ(range_weight × range_progress)
/
Σ(range_weight)
```

Range Weight：

```text
优先 estimated_rows
其次 estimated_bytes
再次 block_span
最后 1
```

---

# 10. Address Progress

一个地址包含多个 Dataset。

不要：

```text
Transactions 100%
Logs 0%

→ 地址 50%
```

如果 Logs 预计 2000 万行，Transactions 只有 1 万行，这个 50% 明显错误。

应：

```text
AddressProgress =
Σ(dataset_weight × dataset_progress)
/
Σ(dataset_weight)
```

Dataset Weight：

```text
EstimatedWorkload
```

可基于：

```text
estimated_rows
× dataset_complexity
```

---

# 11. Batch Progress

同理：

```text
BatchProgress =
Σ(address_weight × address_progress)
/
Σ(address_weight)
```

不要简单：

```text
completed addresses / total addresses
```

但前端可以同时显示：

```text
总体工作量：68%
地址完成：7,182 / 10,000
```

---

# 12. 状态进度与数据进度分离

例如：

```text
下载 100%
但正在 Validation
```

不能直接显示：

```text
100% COMPLETED
```

建议：

```text
Data Progress: 100%
Task Progress: 92%
Stage: VALIDATING
```

用户主界面显示：

```text
92% 校验中
```

详情显示：

```text
下载：100%
处理：100%
校验：72%
```

---

# 13. Stage 权重

初始建议：

```text
DISCOVERY   5%
PLANNING    3%
DOWNLOADING 70%
PROCESSING  10%
VALIDATING  10%
PUBLISHING  2%
```

但 Dataset 内部最好按实际工作量动态调整。

例如大 Trace：

```text
Normalize / Processing
```

可能比下载还重。

因此最终建议：

```text
Discovery 提供 EstimatedStageCost
```

后续动态权重。

---

# 14. ETA Engine

ETA 不能只用：

```text
remaining / current_speed
```

因为瞬时速度波动太大。

建议使用：

```text
EWMA
+
Rolling Median
+
Provider Historical Profile
```

---

# 15. EWMA

公式：

```text
EWMA_t =
α × current_rate
+
(1 - α) × EWMA_(t-1)
```

建议：

```text
α = 0.2 ～ 0.35
```

高波动 Provider：

```text
α 更小
```

稳定 Direct Download：

```text
α 可以更大
```

---

# 16. Throughput Snapshot

```go
type ThroughputSnapshot struct {
    RowsPerSecond   float64
    BytesPerSecond  float64
    BlocksPerSecond float64
    RangesPerSecond float64
    RequestsPerSecond float64

    SmoothedRowsPerSecond  float64
    SmoothedBytesPerSecond float64
}
```

---

# 17. ETA Snapshot

```go
type ETASnapshot struct {
    Seconds int64

    LowerBoundSeconds int64
    UpperBoundSeconds int64

    Confidence string

    Recalculating bool

    BasedOn string
}
```

前端可以显示：

```text
预计剩余：约 18 分钟
置信度：高
```

或：

```text
预计剩余：12～28 分钟
置信度：低
```

---

# 18. ETA Confidence

建议：

```text
HIGH
MEDIUM
LOW
UNKNOWN
```

依据：

```text
样本时间
速度方差
Discovery confidence
Provider 切换次数
Retry 次数
当前阶段
```

---

# 19. 初始 ETA

任务刚开始还没有真实速度。

使用：

```text
Discovery EstimatedRuntime
+
Historical Provider Profile
```

例如：

```text
预计 20～28 min
```

运行 2 分钟以后切到：

```text
Runtime EWMA
```

---

# 20. Provider 切换后的 ETA

例如：

```text
SQD → RPC
```

不能继续使用 SQD 的速度。

切换时：

```text
ETA.Recalculating = true
```

前端：

```text
预计时间重新计算中…
```

等待：

```text
新 Provider 收集 3～5 个有效样本
```

再发布新的 ETA。

---

# 21. Cloud Tier 切换后的 ETA

同理：

```text
Cloud L → Cloud XL
```

清理：

```text
短期 throughput window
```

但保留：

```text
已完成工作量
```

重新计算剩余 ETA。

---

# 22. Retry / Cooldown 对 ETA 的影响

ETA 必须考虑：

```text
WAITING_RETRY
CIRCUIT_COOLDOWN
PROVISIONING
```

例如：

```text
SQD cooldown 60s
```

ETA：

```text
remaining_processing_time
+
cooldown_time
```

不能在等待期间显示速度 0 后 ETA 无限大。

---

# 23. Validation ETA

Validation 也需要 ETA。

基于：

```text
Parquet bytes
row count
历史 DuckDB scan throughput
gap count
repair count
```

例如：

```text
下载完成
校验中 64%
预计 48 秒
```

---

# 24. Repair ETA

如果 Validation 发现 Gap：

```text
原 ETA 已结束
```

任务重新进入：

```text
REPAIRING
```

ETA 增加：

```text
Gap Download
+
Normalize
+
Revalidate
```

前端：

```text
发现 2 个缺口
正在自动补齐
预计额外 1分12秒
```

---

# 25. Task Event Bus

建议内部不要让每个模块直接调用 SSE。

增加：

```text
TaskEventBus
```

事件来源：

```text
Discovery
Scheduler
Executor
Provider Router
Checkpoint
Validation
Repair
Result Processor
```

统一：

```text
Publish(TaskEvent)
```

---

# 26. TaskEvent

```go
type TaskEvent struct {
    ID string

    Type string

    BatchID      string
    AddressJobID string
    DatasetJobID string
    RangeID      string

    Sequence uint64

    Payload any

    CreatedAt time.Time
}
```

---

# 27. 事件类型

建议：

```text
batch.created
batch.updated
batch.completed

address.created
address.updated
address.paused
address.resumed
address.canceled

dataset.updated
dataset.completed

range.started
range.progress
range.completed

provider.selected
provider.retry
provider.switched
provider.circuit_open

checkpoint.saved

validation.started
validation.progress
validation.completed

gap.detected
repair.started
repair.completed

result.ready

error
```

---

# 28. Sequence

每个：

```text
Batch
```

维护单调递增：

```text
sequence
```

例如：

```text
1021
1022
1023
```

前端如果发现：

```text
1021 → 1024
```

说明漏了事件。

立即：

```text
GET Snapshot
```

重新同步。

---

# 29. Event Persistence

SSE 不需要保存无限事件。

但建议短期保存：

```text
recent-events.ndjson
```

例如：

```text
最近 10,000 events
或
最近 30 min
```

用于短线重连。

长期审计仍由：

```text
Range Ledger
Provider Attempt Ledger
Validation Ledger
```

负责。

---

# 30. SSE Gateway

API：

```http
GET /api/smart-download/events
```

支持参数：

```text
batch_id
address_job_id
last_event_id
```

---

# 31. SSE 示例

```text
event: dataset.updated
id: 1024
data: {...}
```

---

# 32. SSE 重连

浏览器断开后：

```text
Last-Event-ID
```

服务端：

```text
如果事件还在 buffer
→ 补发

如果已经超出 buffer
→ 返回 resync_required
```

前端再：

```text
GET snapshot
```

---

# 33. 为什么优先 SSE 而不是 WebSocket

这个场景主要是：

```text
服务器 → 前端
```

单向状态更新。

SSE：

- 实现简单；
- 浏览器原生；
- 自动重连；
- 支持 Last-Event-ID；
- 更适合任务进度；
- 调试简单。

任务控制：

```text
Pause / Resume / Cancel
```

继续走 REST。

所以：

```text
REST + SSE
```

足够。

---

# 34. Event Coalescer

最大问题：

```text
10K 地址
× 高频进度
```

不能每个 Provider 每更新 1 行就 SSE。

建立：

```text
Event Coalescer
```

---

# 35. Coalescing 策略

普通 Progress：

```text
250～500ms
```

合并一次。

状态变化：

```text
立即发送
```

例如：

```text
RUNNING → PAUSED
SQD → RPC
Gap Detected
COMPLETED
```

不能延迟。

---

# 36. Progress Event 去重

同一个 Dataset：

```text
100ms 内收到 20 个 progress
```

只保留：

```text
最新 Snapshot
```

发送一次。

---

# 37. 批量 SSE

对于 10 万地址：

可以发送：

```text
progress.batch
```

Payload：

```json
{
  "updates": [
    {"address_job_id":"a1","progress":0.52},
    {"address_job_id":"a2","progress":0.73}
  ]
}
```

比：

```text
每个地址单独一个 SSE event
```

更高效。

---

# 38. Frontend Snapshot API

首次进入任务中心：

```http
GET /api/smart-download/jobs/{batch_id}/addresses
```

返回分页数据。

然后 SSE 只更新当前可见 / 已加载的任务。

---

# 39. 10 万地址前端策略

必须：

```text
Server-side pagination
+
Virtualized Table
+
SSE Incremental Update
```

不要一次加载：

```text
100,000 AddressJob
```

---

# 40. 前端 Store

建议：

```text
Normalized Store
```

按 ID 保存：

```text
batchById
addressById
datasetById
```

不要把 SSE 事件直接塞进巨大数组。

---

# 41. 可见区域优先

用户当前只看：

```text
50 地址
```

后端仍会处理全部任务。

前端只对：

```text
已加载页
```

实时渲染。

Batch 顶部统计则接收：

```text
batch.updated
```

---

# 42. 新任务中心主表

这一阶段后端完成后，前端就能稳定展示：

| 地址 | 状态 | 总进度 | 当前 Dataset | Provider | 当前速度 | 已下载 | ETA | 操作 |
|---|---|---:|---|---|---:|---:|---|---|
| 0x91…82A | 下载中 | 68% | Token Transfer | SQD | 32.4k rows/s | 1.24M | 02:18 | 暂停 |
| 0x72…998 | 校验中 | 94% | Logs | — | 18.2 MB/s | 392k | 00:34 | 详情 |
| 0xAA…12F | 完成 | 100% | — | CSV | — | 8,421 | — | 查看 |
| 0x13…CC9 | 切换中 | 37% | Internal Tx | SQD → RPC | — | 84k | 重算中 | 详情 |

---

# 43. Provider Badge

统一：

```text
CSV
DIRECT
SQD
RPC
SQD CLOUD
```

Cloud：

```text
SQD CLOUD · S
SQD CLOUD · L
SQD CLOUD · XL
```

---

# 44. 状态显示

统一用户态：

```text
等待
探测
规划
下载中
重试中
切换下载方式
暂停中
已暂停
处理中
校验中
补洞中
完成
部分完成
失败
已取消
```

不要把内部 Go error 直接显示给用户。

---

# 45. Error Presentation

后端返回：

```json
{
  "error_class": "HTTP_503",
  "provider": "sqd",
  "recoverable": true,
  "action": "SWITCH_PROVIDER"
}
```

前端：

```text
SQD 服务暂时不可用
系统正在自动切换下载方式
已完成数据不会重新下载
```

---

# 46. Pause 实时反馈

点击：

```text
暂停
```

REST：

```text
POST pause
```

前端先显示：

```text
暂停中…
```

收到 SSE：

```text
address.paused
```

再：

```text
已暂停
```

避免按钮点了以后状态卡 2 秒。

---

# 47. Cancel 实时反馈

同理：

```text
取消中…
↓
checkpoint.saved
↓
address.canceled
```

用户看到：

```text
已取消
已保存 68% 数据
可后续恢复/重新创建
```

---

# 48. Result Ready

Dataset / Address 最终完成后：

```text
result.ready
```

前端可立即启用：

```text
查看结果
进入画像
生成关系图
加入智能调查
```

---

# 49. 后端代码结构

建议新增：

```text
internal/smartdownload/progress/
├── event.go
├── collector.go
├── aggregator.go
├── snapshot.go
├── weights.go
├── throughput.go
└── persistence.go

internal/smartdownload/eta/
├── engine.go
├── ewma.go
├── confidence.go
├── stage_eta.go
└── historical.go

internal/smartdownload/events/
├── bus.go
├── types.go
├── coalescer.go
├── buffer.go
└── sequence.go

internal/smartdownload/sse/
├── handler.go
├── client.go
├── hub.go
├── replay.go
└── filter.go
```

---

# 50. 不要让 Progress 直接修改任务事实

Progress Aggregator 只能生成：

```text
derived state
```

不能把：

```text
Range COMPLETED
```

改回：

```text
RUNNING
```

任务事实仍来自：

```text
Task State Store
Range Ledger
Checkpoint
Validation
```

Progress 是派生视图。

---

# 51. Progress Snapshot 持久化

普通 progress 可以：

```text
1～2 秒
```

节流保存。

但以下状态立即保存：

```text
Provider Switch
Pause
Resume
Cancel
Range Complete
Validation Complete
Task Complete
```

---

# 52. Restart 后的 Progress

服务重启：

```text
Recovery Manager
↓
Task State
↓
Checkpoint
↓
Range Ledger
↓
重新生成 Progress Snapshot
```

不依赖旧 SSE event。

---

# 53. 性能目标

## 10K 地址

```text
SSE 连接：1 / 浏览器
UI 更新 <= 4 次/秒
后端 progress coalesce <= 500ms
```

## 100K 地址

要求：

```text
不能每地址每秒持久化
不能每地址每秒 SSE
不能每地址一个 goroutine 专门计算 ETA
```

必须集中聚合。

---

# 54. ETA 计算频率

建议：

```text
Dataset ETA：
1～2s 更新

Address ETA：
2s

Batch ETA：
3～5s
```

不用每个 progress event 都计算。

---

# 55. SSE 负载控制

如果事件积压：

```text
优先保留：
状态变化
Provider 切换
错误
完成

可以丢弃：
旧 progress
```

因为 progress 是覆盖型状态。

---

# 56. Slow Client

浏览器消费太慢：

```text
不要阻塞 Task Executor
```

SSE client 使用有界 buffer。

满时：

```text
drop old progress
保留 state events
```

必要时：

```text
resync_required
```

---

# 57. Event Priority

建议：

```text
P0:
completed
failed
canceled
provider.switched
gap.detected

P1:
status.updated

P2:
progress.updated
```

---

# 58. API

新增：

```http
GET /api/smart-download/events
GET /api/smart-download/jobs/{batch_id}/snapshot
GET /api/smart-download/jobs/{batch_id}/addresses
GET /api/smart-download/jobs/{batch_id}/addresses/{address_job_id}
```

控制继续：

```http
POST .../pause
POST .../resume
POST .../cancel
```

---

# 59. 真实验收 Case A

## SQD 实时进度

运行真实 BSC Token Transfer：

要求：

```text
rows/s
blocks
progress %
ETA
```

持续更新。

---

# 60. Case B：Provider 切换

```text
SQD
↓
模拟 503
↓
SQD → RPC
```

前端必须依次显示：

```text
重试中
↓
切换下载方式
↓
RPC
↓
ETA 重新计算
```

且进度不能回到 0。

---

# 61. Case C：Pause

运行到：

```text
47%
```

点击 Pause。

要求：

```text
47.x%
↓
暂停中
↓
checkpoint saved
↓
已暂停
```

Resume：

```text
从 47.x% 继续
```

---

# 62. Case D：Validation + Repair

下载：

```text
100%
```

之后：

```text
校验中
```

发现 Gap：

```text
补洞中
```

修复后：

```text
100% 已验证
```

主进度不能误导用户提前认为任务结束。

---

# 63. Case E：10K 地址

创建：

```text
10,000 AddressJob
```

要求：

```text
前端不卡顿
没有 2s 全量轮询
SSE 连接稳定
后端 CPU 不因进度系统明显异常升高
```

---

# 64. Case F：SSE 断线恢复

人为断开网络：

```text
30s
```

重连：

```text
Last-Event-ID
```

如果事件还在 buffer：

```text
补发
```

否则：

```text
snapshot resync
```

最终状态一致。

---

# 65. P0 验收标准

必须：

```text
[PASS] Provider 统一 ProgressEvent
[PASS] Dataset Weighted Progress
[PASS] Address Weighted Progress
[PASS] Batch Weighted Progress
[PASS] EWMA Throughput
[PASS] ETA + Confidence
[PASS] Provider Switch ETA Reset
[PASS] Validation / Repair ETA
[PASS] Task Event Bus
[PASS] Event Coalescer
[PASS] SSE Gateway
[PASS] Last-Event-ID
[PASS] Snapshot Resync
[PASS] 10K 地址不使用全量轮询
```

---

# 66. P1

随后优化：

```text
Historical ETA Profile
Cloud Resource ETA
Batch Completion Prediction
前端可见任务优先事件
SSE batch compression
10万地址压力测试
```

---

# 67. Codex 开发顺序

建议：

```text
Commit 1
Unified ProgressEvent + Snapshot

Commit 2
Weighted Dataset / Address / Batch Progress

Commit 3
EWMA Throughput Engine

Commit 4
ETA Engine + Confidence

Commit 5
Task Event Bus

Commit 6
Event Coalescer + Sequence

Commit 7
SSE Gateway + Replay

Commit 8
Snapshot API

Commit 9
Provider Switch / Validation / Repair event integration

Commit 10
10K address realtime stress test
```

---

# 68. 本阶段不要做什么

本阶段先不要大规模重写 UI。

只做：

```text
后端实时进度基础
+
一个最小测试页面/调试页面
```

确认：

```text
SSE
Progress
ETA
Switch
Pause
Validation
```

全部可靠后，再正式进入新“智能下载”前端重构。

---

# 69. 本阶段完成后的下一步

完成后，下一阶段正式进入：

```text
Smart Download Frontend V2
```

也就是最初要求的最终用户入口：

```text
创建下载
任务中心
结果数据
```

前端届时不再猜状态，而是直接消费：

```text
Snapshot + SSE
```

后端所有能力已经准备好。

---

# 70. 一句话目标

> **这一阶段的目标，是让“10 万地址 × 多 Dataset × 多 Provider”的真实任务状态，以低开销、低延迟、可恢复的方式实时呈现在前端，并且 ETA、Provider 切换、Validation、Repair 都使用同一套统一进度语义。**
