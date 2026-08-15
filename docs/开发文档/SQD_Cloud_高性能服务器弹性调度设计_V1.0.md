# SQD Cloud 高性能服务器弹性调度设计 V1.0
## SQD Cloud Elastic Compute Tiering & High-Performance Worker Policy

> 目标：明确 SQD Cloud 在“智能下载”体系中的定位，以及什么时候使用普通 Cloud Worker、什么时候升级到高性能服务器。  
> 核心原则：**SQD Cloud 是最终兜底通道，高性能服务器是 SQD Cloud 内部的弹性计算层，不是默认下载方式。**

---

# 1. 总体结论

整个下载链路建议保持：

```text
CSV / Browser / Direct / SQD / RPC
                ↓
      普通下载方式失败或不适合
                ↓
             SQD Cloud
          ├─ Cloud S
          ├─ Cloud L
          └─ Cloud XL
```

需要拆成两个独立决策：

```text
决策 1：
是否进入 SQD Cloud？

决策 2：
进入 SQD Cloud 后，用什么规格的服务器？
```

不能把这两个问题混在一起。

---

# 2. SQD Cloud 的系统定位

SQD Cloud 不作为普通默认 Provider。

只有满足以下情况时才进入：

- 普通 CSV / Browser / Direct 不适合；
- SQD 普通节点连续失败；
- RPC 预计时间过长；
- RPC 限流严重；
- Bulk / AWS 不支持对应 Dataset；
- Validator 发现缺口且普通 Provider 无法补齐；
- Dataset 规模极大；
- Trace / Logs 等任务需要大量解析；
- 本地机器资源不足；
- 任务必须在合理时间内完成。

建议 Provider 优先级：

```text
Local Cache
↓
CSV / Browser / Direct
↓
SQD
↓
RPC / Bulk
↓
SQD Cloud
```

SQD Cloud 保持最终兜底。

---

# 3. 为什么不能所有 Cloud 任务都使用高性能服务器

如果只是：

```text
8,000 rows
30 MB CSV
```

使用：

```text
32 CPU
64 GB RAM
NVMe
```

没有意义。

真正需要高性能节点的通常是：

- 超大 Token Transfer；
- 超大 Logs；
- Internal Transaction / Trace；
- 数百万至数千万行；
- 多 Range 并行；
- 大量 JSON / Arrow / Parquet 转换；
- 大规模 Dedup；
- DuckDB 校验；
- 高速落盘；
- 大量临时数据；
- 普通 Worker ETA 过长。

因此调度粒度必须是：

```text
Address × Dataset × Range
```

而不是：

```text
Address
```

不能因为一个地址很大，就让它所有 Dataset 都上高性能服务器。

---

# 4. Cloud Worker 分级

建议分成三档。

## 4.1 Cloud S

适用于：

```text
<= 500,000 rows
<= 1 GB 数据量
预计运行 <= 10 min
```

建议规格：

```text
2～4 vCPU
4～8 GB RAM
普通 SSD
普通网络
```

用途：

- 小型 Cloud fallback；
- 小范围缺口补洞；
- 小 Dataset；
- 普通 CSV/SQD/RPC 都失败后的兜底。

## 4.2 Cloud L

适用于：

```text
500,000 ～ 5,000,000 rows
1 ～ 10 GB
预计运行 10～60 min
```

建议规格：

```text
8 vCPU
16 GB RAM
高速 SSD
较高网络带宽
```

用途：

- 中型 Token Transfer；
- 中型 Logs；
- 大地址局部时间范围；
- 多 Range 并发处理；
- 标准 Parquet 处理。

## 4.3 Cloud XL

高性能服务器。

适用于：

```text
> 5,000,000 rows
或
> 10 GB
或
预计普通 Cloud > 30～60 min
或
重 Trace / Logs
```

建议规格：

```text
16～32 vCPU
32～64 GB RAM
高速 NVMe
高带宽网络
```

特别大的任务可升级：

```text
32～64 vCPU
64～128 GB RAM
```

但不建议一开始固定使用最高规格。

---

# 5. 直接进入高性能服务器的条件

建议满足以下任意条件时直接进入 `Cloud XL Candidate`：

```text
estimated_rows >= 5,000,000
```

或：

```text
estimated_download_bytes >= 5 GB
```

或：

```text
estimated_temp_space >= 20 GB
```

或：

```text
estimated_standard_runtime >= 30 min
```

或：

```text
range_partitions >= 64
```

或：

```text
required_parallel_workers >= 16
```

或：

```text
dataset_type in:
- logs
- internal_transactions
- trace
```

且数据量明显偏大。

---

# 6. 强制 Cloud XL 的条件

下列任务无需先试普通 Cloud。

建议直接使用高性能节点：

```text
estimated_rows >= 20,000,000
```

或：

```text
estimated_data >= 20 GB
```

或：

```text
estimated_standard_runtime >= 2 hours
```

或：

```text
estimated_memory >= 24 GB
```

或：

```text
estimated_temp_space >= 100 GB
```

或：

```text
Cloud L 历史同类任务频繁 OOM / timeout
```

---

# 7. Dataset Complexity

不能只看 rows。

例如：

```text
1,000,000 Transactions
```

和：

```text
1,000,000 Trace
```

资源消耗不同。

建议引入：

```text
DatasetComplexity
```

初始建议：

```text
balances              0.2
token_metadata        0.3
transactions          1.0
token_transfers       1.2
logs                  1.5
decoded_logs          1.8
internal_transactions 2.0
trace                  2.5
```

综合：

```text
EffectiveWorkload =
EstimatedRows
× DatasetComplexity
```

---

# 8. Cloud Resource Score

建议计算：

```text
CloudResourceScore =
    RowScore
  + ByteScore
  + DatasetComplexityScore
  + MemoryScore
  + TempDiskScore
  + PartitionScore
  + RuntimeScore
  + HistoricalFailureScore
```

---

# 9. 初始评分规则

## RowScore

```text
< 500K       = 5
500K～5M     = 20
5M～20M      = 40
>20M         = 60
```

## ByteScore

```text
<1 GB        = 5
1～5 GB      = 15
5～20 GB     = 30
>20 GB       = 50
```

## RuntimeScore

```text
<10 min      = 5
10～30 min   = 15
30～120 min  = 35
>120 min     = 60
```

---

# 10. Worker 选择

建议：

```text
Score < 30
→ Cloud S

30 <= Score < 70
→ Cloud L

Score >= 70
→ Cloud XL
```

如果触发强制规则：

```text
Force XL
```

覆盖评分。

---

# 11. 示例一：小地址

Discovery：

```text
Transactions      8,200
Token Transfer    4,100
Logs             11,700
Internal Tx       1,100
```

调度：

```text
Transactions
→ CSV

Token Transfer
→ CSV

Logs
→ CSV / RPC

Internal Tx
→ RPC
```

不进入 SQD Cloud。

---

# 12. 示例二：大地址

Discovery：

```text
Transactions        680,000
Token Transfer    3,800,000
Logs              9,200,000
Internal Tx         920,000
```

建议：

```text
Transactions
→ SQD

Token Transfer
→ SQD

Logs
→ SQD
→ 如果持续失败则 SQD Cloud XL

Internal Tx
→ RPC / Trace Provider
→ 失败后 Cloud L / XL
```

---

# 13. 示例三：超大地址

Discovery：

```text
Transactions         380,000
Token Transfer     8,200,000
Logs              21,000,000
Internal Tx        3,800,000
Balance                    1
```

调度：

```text
Transactions
→ SQD

Token Transfer
→ SQD
→ SQD失败后 Cloud XL

Logs
→ Cloud XL Candidate

Internal Tx
→ RPC / SQD
→ Cloud XL Candidate

Balance
→ RPC
```

同一个地址不是整体进入 Cloud XL，而是不同 Dataset 独立决策。

---

# 14. 高性能服务器真正承担什么

Cloud XL 不只是“网络下载快”。

它主要用于整条重处理链：

```text
SQD Cloud Fetch
↓
Range Parallelism
↓
Decompression
↓
JSON / Arrow Parse
↓
Canonical Schema Normalize
↓
Trace / Event Decode
↓
Parquet Multipart
↓
SHA256
↓
Dedup
↓
Merge
↓
DuckDB Validation
```

因此高性能节点的核心价值是：

```text
CPU
+
RAM
+
Disk IO
+
Network
```

综合提升。

---

# 15. Cloud 内部也必须支持 Checkpoint V3

Cloud S / L / XL 不是不同任务。

它们只是：

```text
同一个 DatasetJob 的不同执行资源
```

例如：

```text
DatasetJob: ds_123
```

先运行：

```text
Cloud L
```

后来升级：

```text
Cloud XL
```

DatasetJob ID 不变。

---

# 16. Cloud Worker 升级机制

假设：

```text
Cloud L
预计 20 min
```

实际运行：

```text
10 min 后只完成 8%
ETA 变成 115 min
```

应自动触发升级。

流程：

```text
Cloud L
↓
Performance Monitor
↓
检测 ETA 异常
↓
安全 Checkpoint
↓
Pause Current Worker
↓
Release Worker
↓
Allocate Cloud XL
↓
Resume Pending Range
```

不能重新开始整个任务。

---

# 17. 升级条件

运行中满足任一：

```text
actual_throughput < expected_throughput × 0.35
持续 >= 5 min
```

或：

```text
ETA > 2 × original ETA
```

或：

```text
ETA > 60 min
```

或：

```text
Memory > 85%
持续 >= 2 min
```

或：

```text
Disk IO saturation > 90%
```

或：

```text
发生 OOM
```

或：

```text
连续 processing timeout
```

自动：

```text
S → L
L → XL
```

---

# 18. 降级机制

如果 XL 已经完成重任务阶段，剩余只是小范围补洞，可以：

```text
XL → L
```

例如：

```text
主数据 20M rows 已完成
剩余 Repair Range 只有 50K rows
```

没有必要继续占用 XL。

---

# 19. Resource Migration 与 Provider Switch 使用同一套机制

系统统一使用：

```text
ExecutionTarget Switch
```

两种切换：

```text
Provider Switch:
SQD → RPC

Resource Switch:
SQD Cloud L → SQD Cloud XL
```

两者都依赖：

```text
Universal Checkpoint V3
+
Range Ledger
```

---

# 20. Range 级资源调度

对于一个超大 Dataset：

```text
20M rows
```

可以：

```text
Range 1 → Cloud XL Worker A
Range 2 → Cloud XL Worker B
Range 3 → Cloud XL Worker C
Range 4 → Cloud XL Worker D
```

最终：

```text
Merge
Dedup
Validation
```

但并行必须受：

```text
Provider Limit
Cloud Budget
Disk
Memory
```

共同约束。

---

# 21. 建议 Cloud 并发模型

例如：

```text
Cloud S:
max 8 tasks

Cloud L:
max 4 tasks

Cloud XL:
max 2 tasks
```

不是固定值，可以按预算配置。

---

# 22. Cloud Budget Guard

必须增加：

```text
CloudBudgetGuard
```

配置：

```text
daily_budget
monthly_budget
max_xl_workers
max_single_job_cost
```

如果超预算，优先：

```text
降级 Worker
降低并行
继续普通 Provider
延长 ETA
```

而不是直接失败。

---

# 23. 成本估算

Discovery 阶段估算：

```text
estimated_cpu_minutes
estimated_memory_gb_minutes
estimated_network_gb
estimated_temp_storage_gb
estimated_runtime
```

组合：

```text
EstimatedCloudCost
```

管理员页可以展示：

```text
预计 Cloud 资源：
High Performance

预计耗时：
18～26 min

预计成本：
¥xx.xx
```

---

# 24. Scheduler 输出

例如：

```json
{
  "dataset_job_id": "ds_xxx",
  "provider": "sqd_cloud",
  "cloud_tier": "XL",
  "reason": [
    "estimated_rows=21000000",
    "dataset=logs",
    "estimated_standard_runtime=146min"
  ],
  "estimated_runtime_seconds": 1320
}
```

---

# 25. Cloud Worker 状态

建议：

```text
REQUESTED
PROVISIONING
STARTING
RUNNING
SCALING_UP
SCALING_DOWN
DRAINING
RELEASING
RELEASED
FAILED
```

---

# 26. 任务前端展示

普通用户不需要看到 CPU/RAM。

显示：

```text
下载方式：
SQD Cloud

资源：
高性能

当前：
21.2M Logs

速度：
81k rows/s

预计：
18 min
```

如果升级：

```text
资源自动升级
标准 → 高性能

原因：
当前吞吐低于预期
已保存断点
无需重新下载
```

---

# 27. 管理员详情

管理员可以看到：

```text
Worker Tier
CPU
RAM
Disk
Network
CPU Usage
RAM Usage
IO
Throughput
ETA
Cost
Scaling Reason
```

---

# 28. 高性能服务器不要被用户手动选择

普通下载页面不应该提供：

```text
☐ 使用高性能服务器
```

用户只输入：

```text
地址
链
Dataset
时间范围
```

Scheduler 自己决定。

管理员可以保留：

```text
Force Tier
```

用于调试。

---

# 29. 和 Discovery 的关系

Discovery 至少输出：

```text
estimated_rows
estimated_bytes
estimated_temp_space
estimated_cpu_complexity
estimated_runtime
dataset_complexity
range_count
```

这些字段直接给：

```text
Cloud Resource Planner
```

---

# 30. Cloud Resource Planner

建议增加：

```text
internal/smartdownload/cloudplanner/
```

目录：

```text
cloudplanner/
├── planner.go
├── score.go
├── policy.go
├── estimator.go
├── scaler.go
├── budget.go
└── metrics.go
```

---

# 31. 核心接口

```go
type CloudTier string

const (
    CloudS  CloudTier = "S"
    CloudL  CloudTier = "L"
    CloudXL CloudTier = "XL"
)
```

---

# 32. ResourcePlan

```go
type ResourcePlan struct {
    Tier CloudTier

    CPU        int
    MemoryGB   int
    TempDiskGB int

    MaxWorkers int

    EstimatedRuntime time.Duration
    EstimatedCost    float64

    Score   float64
    Reasons []string
}
```

---

# 33. Planner

```go
type CloudResourcePlanner interface {
    Plan(
        ctx context.Context,
        probe ProbeResult,
        dataset DatasetType,
    ) (ResourcePlan, error)

    Reevaluate(
        ctx context.Context,
        current ResourcePlan,
        metrics RuntimeMetrics,
    ) (ResourcePlan, error)
}
```

---

# 34. Runtime Metrics

```go
type RuntimeMetrics struct {
    RowsPerSecond  float64
    BytesPerSecond float64

    CPUUsage    float64
    MemoryUsage float64
    DiskIOUsage float64

    CompletedPercent float64
    ETA              time.Duration

    OOMCount     int
    TimeoutCount int
}
```

---

# 35. 自动升级伪代码

```go
if metrics.OOMCount > 0 {
    return Upgrade()
}

if metrics.MemoryUsage > 0.85 &&
   memoryHighDuration > 2*time.Minute {
    return Upgrade()
}

if metrics.RowsPerSecond <
   expectedRowsPerSecond*0.35 &&
   slowDuration > 5*time.Minute {
    return Upgrade()
}

if metrics.ETA > current.EstimatedRuntime*2 {
    return Upgrade()
}
```

---

# 36. Cloud XL 与 Validation

大任务在 XL 上完成下载后，不一定所有 Validation 都要继续占 XL。

可以：

```text
Heavy Download
→ XL

Heavy Normalize
→ XL

Heavy Dedup
→ XL

最终 Metadata/Manifest
→ L / Local
```

如果：

```text
DuckDB全量聚合
跨 Provider 大规模对账
```

仍然很重，可以继续使用 XL。

---

# 37. 任务生命周期示例

```text
Address A
Logs
21M rows

↓ Discovery

普通 SQD
预计 4h
且历史503高

↓ Scheduler

SQD Cloud XL

↓ Run

0～55%
Cloud XL Worker A

↓ Worker 故障

Checkpoint V3

↓ Replacement

Cloud XL Worker B

↓ Resume

55%～100%

↓ Validation

发现 2 个 Gap

↓ Repair

RPC

↓ Final

COMPLETED
```

整个过程中：

```text
DatasetJob ID 不变
```

---

# 38. 另一种生命周期

```text
Token Transfer
3.2M rows

↓
Cloud L

↓ 运行10分钟

吞吐低
ETA从25min变成95min

↓
Checkpoint

↓
L → XL

↓
继续未完成Range

↓
完成
```

---

# 39. P0 验收

至少：

## Case 1：S 正常完成

```text
100K rows
→ Cloud S
→ COMPLETED
```

## Case 2：L 自动升级 XL

```text
3M rows
→ Cloud L
→ 模拟吞吐下降
→ Cloud XL
→ 不重跑 Completed Range
```

## Case 3：XL 崩溃恢复

```text
20M rows
→ XL
→ kill worker
→ replacement worker
→ checkpoint resume
```

## Case 4：XL OOM

```text
L
→ OOM
→ XL
→ resume
```

## Case 5：XL 降级

```text
大任务主阶段完成
剩余小 Gap
→ XL release
→ L / RPC repair
```

---

# 40. 数据一致性验收

资源切换前后要求：

```text
sum(parts.rows) == final_rows
duplicate_part_sha == 0
duplicate_unique_key == 0
completed_range_repeated == 0
unknown_range == 0
```

---

# 41. 最终调度逻辑

完整流程：

```text
Address × Dataset
↓
Discovery
↓
Local Cache?
├─ HIT
│  → reuse
│
└─ MISS
   ↓
普通 Provider Scheduler
   ├─ CSV
   ├─ Direct
   ├─ SQD
   ├─ RPC
   └─ Bulk
       ↓
普通 Provider 无法完成
       ↓
SQD Cloud
       ↓
Cloud Resource Planner
   ├─ S
   ├─ L
   └─ XL
       ↓
Runtime Monitor
       ├─ 保持
       ├─ Scale Up
       └─ Scale Down
       ↓
Normalize
↓
Validate
↓
Repair
↓
Final Dataset
```

---

# 42. 最终原则

高性能服务器的触发原则不是：

> 地址很大。

而是：

> 当前 `Address × Dataset × Range` 的实际资源需求已经超过普通 Cloud Worker 的合理执行范围。

最终应实现：

```text
普通任务不用 XL
重任务自动使用 XL
估算错误可以动态升级
任务完成后自动释放
资源切换不影响任务身份
资源切换不重新下载已完成 Range
```

这样既能保证大任务速度，又能避免高性能服务器长期空占和成本浪费。
