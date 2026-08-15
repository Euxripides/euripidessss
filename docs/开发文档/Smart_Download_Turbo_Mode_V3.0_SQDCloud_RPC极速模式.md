# Smart Download Turbo Mode V3.0
## 智能下载极速模式 — SQD Cloud + RPC 双通道并行

> 目标：为“大量数据、时间非常紧”的任务提供可手动开启的极速模式。
>
> 极速模式开启后：
>
> ```text
> 本地 Coverage 检查
> → 只计算缺失区间
> → SQD Cloud + RPC 直接并行
> → 流式解析
> → ClickHouse 持续入库
> → Gap Repair
> → 完整性验证
> ```
>
> ClickHouse 仍为最终 Source of Truth。
> 固定根目录：`E:\database\clickhouse`

---

# 1. 新增下载模式

```go
type DownloadMode string

const (
    DownloadModeAuto  DownloadMode = "AUTO"
    DownloadModeTurbo DownloadMode = "TURBO"
)
```

AUTO 保留现有智能调度。

TURBO 直接跳过：

```text
Browser CSV
Browser Direct
Web Crawler
普通 SQD Portal
AWS
其他低优先 Provider
Agent Provider Decision
Provider Probe / Race
```

直接进入：

```text
SQD Cloud
+
RPC
```

---

# 2. Coverage 仍然必须保留

极速模式不能无脑重下。

必须先查：

```text
Coverage Registry
```

例如请求：

```text
2023-01-01 → 2026-08-09
```

本地已有：

```text
2023-01-01 → 2025-12-31
```

Turbo 实际只下载：

```text
2026-01-01 → 2026-08-09
```

---

# 3. 双通道架构

```text
Turbo Job
   ↓
Coverage
   ↓
Missing Ranges
   ↓
Turbo Planner
   ├── SQD Cloud Bulk Lane
   └── RPC Fast Lane
            ↓
      Canonical Parser
            ↓
      ClickHouse Writer
            ↓
       Range Ledger
            ↓
       Gap Detector
            ↓
        Gap Repair
            ↓
        Validator
```

---

# 4. 不是双份重复下载

禁止：

```text
SQD Cloud 下载全部
+
RPC 再下载同样全部
```

正确方式：

```text
按 Dataset / Range / Priority 分工
```

只在少量高优先卡住 Range 上允许 Hedge。

---

# 5. SQD Cloud Bulk Lane

负责：

```text
大历史区间
大量地址
Transactions
Token Transfers
Logs
NFT Transfers
适合 Cloud Processor 的批量数据
```

特点：

```text
长 Range
大 Batch
高吞吐
持续入库
```

---

# 6. RPC Fast Lane

负责：

```text
最新区块 Tail
小缺口
失败 Range
Gap Repair
Receipt
Balance
Metadata
Cloud 不适合的数据集
```

特点：

```text
启动快
Range 小
低延迟
适合抢当前急需数据
```

---

# 7. Turbo Planner

新增：

```text
internal/downloadorchestrator/turbo/
```

建议：

```text
planner.go
policy.go
allocator.go
cloud_lane.go
rpc_lane.go
hedge.go
preemption.go
metrics.go
validator.go
```

动态依据：

```text
地址数量
时间跨度
Dataset
Cloud 吞吐
RPC 健康
最近区块距离
历史 benchmark
```

分配 Cloud / RPC。

不要固定 50/50。

---

# 8. Tail 优先

建议：

```yaml
turbo:
  rpc_tail_blocks: auto
```

最近区块由 RPC 抢先处理，例如：

```text
最近 20K / 50K / 100K blocks
```

动态选择。

目的：

```text
用户先看到最近数据
```

不需要等整个历史任务完成。

---

# 9. Priority Range

优先级顺序：

```text
当前 Explorer 时间区间
当前 Investigation 时间区间
当前 Fund Flow 追踪区间
用户指定重点区间
最近区块
其余历史
```

极速模式优先“先让用户能开始工作”。

---

# 10. Dataset Routing

建议：

```text
Transactions       Cloud 主 / RPC Tail+Repair
Token Transfers    Cloud 主 / RPC eth_getLogs Tail+Repair
Logs               Cloud 主 / RPC Repair
NFT Transfers      Cloud 主 / RPC Repair
Internal/Trace     Cloud 优先 / Trace-capable RPC
Contract Creation  Cloud + Parser / RPC Receipt+Trace Repair
Receipt            RPC 主
Balance            RPC 主
Token Metadata     RPC 主
```

原则：

```text
不要求每个 Dataset 同时使用两者
```

而是：

```text
直接进入 SQD Cloud / RPC 两类极速通道中最适合的一条
```

---

# 11. SQD Cloud Admission 改造

AUTO：

```text
SQD Cloud = Last Resort
```

TURBO：

```text
SQD Cloud = First-class Provider
```

极速模式直接允许 Cloud Dispatch，不需要等待其他 Provider 失败。

---

# 12. 仍然必须保留的 Gate

极速不能关闭：

```text
E 盘空间 Gate
ClickHouse 健康 Gate
Cloud Credentials Gate
RPC Credentials Gate
RPC Hard Quota Gate
Cloud Hard Budget Gate
Dataset Support Gate
Cancel Gate
```

---

# 13. RPC Pool

极速模式应使用所有健康 RPC Endpoint。

按：

```text
latency
success_rate
429_rate
timeout_rate
dataset_capability
```

动态分配。

---

# 14. RPC 自适应并发

```yaml
turbo:
  rpc:
    workers: auto
    max_workers: 64
```

建议 AIMD：

```text
稳定成功
→ 逐步增加

429 / timeout
→ 快速降低
```

例如：

```text
8 → 12 → 16 → 24
```

限流：

```text
24 → 12
```

---

# 15. Cloud Parallel Jobs

```yaml
turbo:
  cloud:
    max_parallel_jobs: auto
```

按：

```text
Dataset
Block Range
Address Group
```

切多个 Cloud Job。

但仍受：

```text
Cloud Budget
Lease
Storage
Writer Capacity
```

约束。

---

# 16. TurboShard

统一极速任务分片：

```text
job_id
dataset
chain_id
address_group
from_block
to_block
priority
assigned_lane
status
attempt
estimated_rows
actual_rows
```

---

# 17. Range Ownership

每个 Range 只能有一个默认 Owner：

```text
CLOUD
RPC
```

例如：

```text
TokenTransfers
45,000,000 → 45,100,000
OWNER=CLOUD
```

RPC 不重复下载。

---

# 18. Range Ledger 状态

```text
PENDING
ASSIGNED_CLOUD
ASSIGNED_RPC
RUNNING
COMPLETED
FAILED
HEDGED
VALIDATED
```

---

# 19. Hedge

只对：

```text
高优先区间
尾部区间
卡住区间
用户当前正在等待的数据
```

允许 Hedge。

例如 Cloud 超过：

```text
30s Stall
```

RPC 可同时抢。

谁先：

```text
VALIDATED
```

谁赢。

另一方取消或丢弃结果。

---

# 20. 极速抢占普通任务

建议：

```yaml
turbo:
  preempt_normal_jobs: true
```

启动 Turbo 后：

```text
NORMAL RUNNING
→ PAUSED_BY_TURBO
```

释放：

```text
RPC Workers
Cloud Slots
Writer Capacity
```

Turbo 结束后自动恢复普通任务。

暂停必须保存 Checkpoint，不允许直接 Kill。

---

# 21. ClickHouse Writer 优先级

新增：

```text
HIGH
NORMAL
BACKGROUND
```

Turbo：

```text
HIGH
```

但仍必须有 Backpressure。

---

# 22. ClickHouse Backpressure

监控：

```text
insert latency
parts count
merge queue
disk IO
free space
```

数据库成为瓶颈时：

```text
Cloud/RPC 自动降速
```

避免“下载很快但数据库堵死”。

---

# 23. Streaming Ingest

极速模式必须：

```text
Shard 下载完成
→ 立即 Parse
→ 立即 ClickHouse Write
→ Explorer 立即可查
```

禁止：

```text
全部下载完
→ 再统一入库
```

---

# 24. Progressive Availability

任务进度 30% 时，也可以：

```text
已有数据立即分析
```

前端显示：

```text
已可用 2.1M rows
Overall 37%
Coverage 逐步扩大
```

---

# 25. 前端模式选择

创建任务：

```text
下载模式

○ 智能模式
● ⚡ 极速模式
```

说明：

```text
极速模式跳过常规下载源，
直接使用 SQD Cloud + RPC。
速度优先，会增加 Cloud / RPC 使用量。
```

---

# 26. 单任务开关，不建议只做全局开关

推荐：

```text
全局默认 = AUTO
+
单任务可选 TURBO
```

避免用户忘记关闭后所有任务都高成本运行。

---

# 27. 运行中 AUTO → TURBO

必须支持。

已完成：

```text
保留
```

正在跑的普通 Provider：

```text
安全停止 / 完成本 Chunk 后停止
```

剩余 Range：

```text
交给 Turbo Planner
```

不重建 Job。

---

# 28. TURBO → AUTO

也支持切回。

完成 Range 不重做。

剩余 Range 回普通 Orchestrator。

---

# 29. 前端 Turbo 状态

任务卡：

```text
⚡ 极速模式

SQD Cloud
3 jobs

RPC
24 workers

Cloud Throughput
120K rows/s

RPC Throughput
32K rows/s

DB Ingest
140K rows/s

Overall Coverage
57%
```

---

# 30. Overall Progress

整体进度必须来自：

```text
Range Ledger / Coverage
```

不能简单平均 Cloud% 和 RPC%。

---

# 31. 新指标：TTFA

增加：

```text
Time To First Available
```

即：

```text
用户点击开始
→ 第一批数据进入 ClickHouse 并可被 Explorer 查询
```

极速模式除了总完成时间，更要优化 TTFA。

---

# 32. Gap Detector

所有 Lane 完成后统一：

```text
Gap Detector
```

检查：

```text
Block Range
Dataset
Address
```

---

# 33. Gap Repair

小 Gap：

```text
RPC 立即修
```

大 Gap：

```text
Cloud re-shard
```

---

# 34. Failure Policy

Cloud Fail：

```text
RPC 支持
→ RPC 接管
```

RPC Fail：

```text
先 RPC Pool reassign
```

仍失败：

```text
Cloud 支持
→ Cloud 接管
```

---

# 35. 默认不偷偷回退第三 Provider

TURBO 下 Cloud + RPC 都失败：

```text
TURBO_DEGRADED
```

默认不静默回：

```text
Browser
AWS
普通 SQD
```

可在高级设置提供：

```text
极速失败后自动回智能模式
```

默认 OFF。

---

# 36. API

创建任务：

```http
POST /api/v1/download/jobs
```

```json
{
  "mode": "TURBO"
}
```

运行中切换：

```http
POST /api/v1/download/jobs/:id/mode
```

```json
{
  "mode": "TURBO"
}
```

---

# 37. Turbo Status API

```http
GET /api/v1/download/jobs/:id/turbo
```

返回：

```text
mode
cloud jobs
cloud rows/sec
rpc workers
rpc rows/sec
429 rate
db rows/sec
coverage
TTFA
ETA
```

---

# 38. 推荐配置

```yaml
download:
  mode_default: AUTO

  turbo:
    enabled: true
    skip_regular_providers: true
    preempt_normal_jobs: true
    fallback_to_auto: false

    cloud:
      enabled: true
      max_parallel_jobs: auto

    rpc:
      enabled: true
      workers: auto
      max_workers: 64

    hedge:
      enabled: true
      stall_threshold: 30s

    stream_to_clickhouse: true
```

---

# 39. Investigation 联动

智能调查里增加：

```text
⚡ 紧急补数据
```

开启后：

```text
mode=TURBO
```

继承：

```text
Case
Address
Time
Dataset
```

---

# 40. Fund Flow 联动

图节点展开增加：

```text
⚡ 极速扩展
```

适用于：

```text
当前正在追资金，马上需要下一层数据
```

创建高优先 Turbo Range。

---

# 41. 大批量地址

例如：

```text
10,000 addresses
```

Turbo Planner 应：

```text
Address Groups
×
Block Ranges
×
Datasets
```

多维分片并行。

不是逐地址串行。

---

# 42. P0

必须完成：

```text
DownloadMode=TURBO
Coverage-first
跳过普通 Provider
SQD Cloud First-class Admission
RPC Fast Lane
Turbo Planner
Range Ownership
Range Ledger
Cloud/RPC Parallel Dispatch
Streaming ClickHouse Ingest
Gap Repair
AUTO→TURBO 运行中切换
前端极速模式选项
```

---

# 43. P1

```text
普通任务抢占
RPC Adaptive Concurrency
Cloud Parallel Jobs
Priority Range
TTFA
Turbo Dashboard
```

---

# 44. P2

```text
Hedged Range
Dynamic Cloud/RPC Allocation
历史 Benchmark 学习
ETA Predictor
Investigation Turbo
Fund Flow Turbo Expand
```

---

# 45. 验收 Case A：确实跳过普通 Provider

TURBO 开启后审计：

```text
Browser = 0
普通 SQD = 0
AWS = 0
Agent Provider Decision = 0
```

只允许：

```text
Coverage
SQD Cloud
RPC
ClickHouse
Validator
```

---

# 46. 验收 Case B：Coverage

本地已有 70%。

Turbo 只能下剩余 30%。

---

# 47. 验收 Case C：双通道同时运行

大历史 Range：

```text
SQD Cloud RUNNING
```

最新 Range：

```text
RPC RUNNING
```

两边同时向 ClickHouse 产生有效数据。

---

# 48. 验收 Case D：无重复

默认 Range 不重叠。

最终：

```text
logical_duplicate = 0
coverage_gap = 0
```

---

# 49. 验收 Case E：中途切极速

AUTO 已完成 32%。

用户开启 Turbo。

要求：

```text
32% 不重做
```

剩余直接进入 Cloud + RPC。

---

# 50. 验收 Case F：Cloud Fail

Cloud Range 失败后，RPC 支持则自动接管。

最终 0 gap。

---

# 51. 验收 Case G：RPC 429

RPC 高并发出现 429：

```text
自动降低 workers
```

任务不能直接 FAILED。

---

# 52. 验收 Case H：ClickHouse Backpressure

如果 DB 写入成为瓶颈：

```text
Cloud/RPC 自动降速
```

内存和待写队列不能无限增长。

---

# 53. 验收 Case I：TTFA

大型任务开始后：

```text
第一批数据尽快进入 ClickHouse
```

不等待全量完成。

Explorer 可以边下边查。

---

# 54. 最终定义

极速模式不是：

```text
把并发调到最大
```

而是：

```text
减少 Provider 决策时间
+
直接启用高吞吐 Cloud/RPC
+
优先当前急需区间
+
双通道并行分工
+
流式入库
+
允许普通任务让路
+
完整性不降低
```

这才是适合“时间很紧 + 大量数据”场景的真正 Turbo Mode。
