# Smart Download Batch Accelerator V3.3
## 批量地址合并调度 + 跨任务去重 + Range 共用加速

> 前置阶段：
>
> - Turbo Mode V3.0 已生产可用
> - Turbo V3.1 负责双通道与弹性调度
> - Turbo Production Hardening V3.2 负责 ETA、诊断、自恢复、资源保护
> - Predictive Prefetch 暂不进入当前路线
>
> 本阶段目标：
>
> **解决“大量地址同时下载时，任务数量过多、Range 重复、Provider 请求重复、Writer 重复处理”的问题。**

---

# 1. 核心问题

如果用户一次提交：

```text
10,000 addresses
×
3 datasets
×
多个时间范围
```

不能继续简单拆成：

```text
10,000 个独立 AddressJob
```

否则会产生：

```text
重复 Coverage Check
重复 Provider 请求
重复 Range 下载
重复 Parser 开销
重复 Writer Batch
大量 Job/Range 元数据
```

---

# 2. 新目标

从：

```text
Address-centric Download
```

升级成：

```text
Workload-centric Download
```

即：

```text
地址只是筛选条件
真正的调度单位是：
Dataset + Chain + Range + Filter Group
```

---

# 3. Workload Coalescer

新增：

```text
WorkloadCoalescer
```

输入：

```text
AddressJobs
DatasetJobs
Ranges
```

输出：

```text
Merged Download Workloads
```

例如：

```text
Address A
Address B
Address C

Dataset:
Token Transfers

Range:
94,000,000–95,000,000
```

合并成：

```text
1 个 AddressGroup
+
1 个 Range
+
1 个 Provider Workload
```

---

# 4. Address Group

新增：

```text
AddressGroup
```

字段：

```text
group_id
chain_id
dataset
addresses[]
filter_hash
priority
```

建议动态大小：

```text
50
100
250
500
1000
```

根据 Provider 能力自动调整。

---

# 5. Provider-aware Group Size

不同 Provider：

```text
SQD Cloud
RPC
Browser
```

允许不同 group size。

例如：

```text
SQD Cloud
→ 大 AddressGroup

RPC eth_getLogs
→ 小 AddressGroup

Browser CSV
→ 单地址 / 小批量
```

不能所有 Provider 用同一个 batch size。

---

# 6. Range Coalescing

如果任务：

```text
A: 94,000,000–94,500,000
B: 94,300,000–95,000,000
```

不要独立下载。

先合并成：

```text
94,000,000–95,000,000
```

再按需要映射回各 AddressJob。

---

# 7. Overlap Ratio

定义：

```text
range_overlap_ratio
```

超过阈值：

```text
例如 50%
```

允许合并。

防止：

```text
两个几乎完全不同的大范围
```

被强制合并。

---

# 8. Cross-job Dedup

新增：

```text
Active Work Registry
```

Key：

```text
chain
dataset
range
filter fingerprint
```

如果 Job B 请求的 Range 已被 Job A 下载：

```text
Job B 不再创建 Provider Task
```

而是：

```text
JOIN_EXISTING_WORK
```

---

# 9. Shared Work

多个任务可以引用同一个：

```text
SharedWorkID
```

例如：

```text
Batch A
Batch B
Batch C
```

都依赖：

```text
SharedWork 123
```

下载完成后：

```text
结果分别回填
```

---

# 10. Work Fingerprint

统一：

```text
chain_id
dataset
normalized_filter
from_block
to_block
schema_version
parser_version
```

Hash：

```text
work_fingerprint
```

避免重复任务。

---

# 11. Filter Normalization

例如：

```text
[A,B,C]
```

和：

```text
[C,A,B]
```

必须产生：

```text
同一个 fingerprint
```

所以地址排序、去重后再 Hash。

---

# 12. Dataset Coalescing

部分 Provider 支持一次返回多类数据：

```text
Transactions
Logs
Token Transfers
```

如果实际 Provider 能力允许：

```text
一次 Download Work
```

可以产生：

```text
多个 Dataset Result
```

避免重复扫同一 Range。

---

# 13. Multi-Dataset Scan

例如 Cloud Processor：

```text
同一 Block Range
```

可以同时解析：

```text
transactions
logs
token transfers
contract creation
```

则优先：

```text
一次扫块
多 Dataset 输出
```

而不是：

```text
每个 Dataset 扫一次
```

---

# 14. Dataset Fan-out

流程：

```text
Shared Range Scan
↓
Parser
├── Transactions
├── Token Transfers
├── Contract Creation
└── Logs
```

再分别：

```text
ClickHouse Writer
```

---

# 15. Batch Planner V2

输入：

```text
10K addresses
3 datasets
3 years
```

Planner 输出：

```text
Address Groups
×
Merged Ranges
×
Dataset Bundles
```

而不是：

```text
10K × 3 × N ranges
```

---

# 16. Density-aware Address Grouping

高活跃地址：

```text
Group Size 小
```

低活跃地址：

```text
Group Size 大
```

避免一个超活跃地址拖慢整个 group。

---

# 17. Heavy Address Isolation

如果 Probe / 历史数据表明：

```text
Address X
活动量特别高
```

自动：

```text
单独成组
```

状态：

```text
HEAVY_ADDRESS
```

---

# 18. Small Address Packing

低活跃地址：

```text
100~1000 个
```

可打包同组。

目标：

```text
减少 Provider 请求数
```

---

# 19. Hot Dataset Priority

批量下载中优先：

```text
Token Transfers
Transactions
```

如果用户选了多个 Dataset：

先让主要业务数据可用。

次要：

```text
NFT
Event
Metadata
```

可以稍后。

---

# 20. Partial Availability

AddressGroup 中：

```text
部分地址完成
```

就允许：

```text
对应地址先 COMPLETED
```

不要等整个 group 所有地址完全结束。

---

# 21. Fan-out Completion

SharedWork 完成后：

按：

```text
Address
Dataset
Range
```

回填：

```text
AddressJob
DatasetJob
RangeJob
```

保持现有四层状态模型。

---

# 22. Shared Retry

SharedWork 失败：

不能：

```text
每个引用 Job 各自重试一次
```

只允许：

```text
SharedWork 自己重试
```

避免雪崩。

---

# 23. Shared Cancellation

如果 Batch A 取消：

但 Batch B 仍引用：

```text
SharedWork 不取消
```

只有：

```text
ref_count = 0
```

才真正取消 Provider Work。

---

# 24. Reference Count

SharedWork 维护：

```text
ref_count
```

引用：

```text
Batch A
Batch B
AddressJob X
```

---

# 25. Cross-batch Coverage Reuse

如果上午 Batch A 已下载：

```text
94M–95M
```

下午 Batch B 请求相同数据：

Coverage Index 直接：

```text
HIT
```

不创建 Provider Work。

---

# 26. Provider Request Reduction

新增核心指标：

```text
provider_requests_saved
```

统计因为：

```text
Range Merge
SharedWork
Coverage Hit
Dataset Bundle
```

减少了多少请求。

---

# 27. Download Amplification

新增：

```text
download_amplification
```

定义：

```text
实际下载数据量
/
最终需要数据量
```

目标：

```text
越接近 1 越好
```

---

# 28. Duplicate Work Ratio

```text
duplicate_work_avoided
```

统计原本会重复执行多少 Range。

---

# 29. Planner Efficiency

新增：

```text
input_jobs
merged_workloads
reduction_ratio
```

例如：

```text
30,000 DatasetJobs
→ 1,240 SharedWork
```

---

# 30. Frontend

批量任务页面显示：

```text
10,000 addresses
3 datasets

原始任务单元
30,000

合并后 Workloads
1,240

复用 Coverage
38%

预计减少 Provider 请求
71%
```

---

# 31. 运行状态

不要显示 10,000 个任务全部展开。

分层：

```text
Batch
↓
Dataset
↓
Address Groups
↓
Shared Workloads
```

单地址详情按需展开。

---

# 32. Batch Summary

显示：

```text
Addresses
Completed
Running
Failed

Coverage

Rows
Throughput

Shared Work
Coverage Hits
Provider Requests Saved
```

---

# 33. 失败隔离

某个 Heavy Address 失败：

```text
只隔离该地址
```

不能拖整个 AddressGroup FAILED。

---

# 34. Group Split on Failure

AddressGroup 出现异常：

```text
自动二分
```

例如：

```text
100 addresses
```

失败：

```text
50 + 50
```

继续定位。

---

# 35. Poison Address

如果最终发现：

```text
某地址导致 Provider / Parser 异常
```

标记：

```text
POISON_ADDRESS
```

单独处理。

---

# 36. Memory Guard

超大 AddressGroup：

必须限制：

```text
in-memory address map
parser buffer
writer buffer
```

避免地址数增加导致内存线性爆炸。

---

# 37. Backpressure

继续复用 V3.2：

```text
ClickHouse
Parser
Disk
```

任何一层变慢：

```text
Planner 停止继续放大 batch
```

---

# 38. 与 Turbo 联动

TURBO 下：

```text
Merged Workload
```

直接交：

```text
SQD Cloud + RPC
```

而不是每个地址分别走 Turbo。

---

# 39. Emergency Batch

极速任务：

```text
10K addresses
```

先：

```text
Coalesce
```

再：

```text
Turbo
```

顺序不能反。

否则会产生大量 Turbo Job。

---

# 40. Recommended Pipeline

```text
Batch Input
↓
Normalize
↓
Coverage
↓
Coalesce
↓
SharedWork Registry
↓
Turbo / Auto
↓
Parse Once
↓
Fan-out
↓
ClickHouse
↓
Job Completion Mapping
```

---

# 41. P0

必须完成：

```text
Workload Coalescer
AddressGroup
Range Merge
Work Fingerprint
SharedWork Registry
Cross-job Dedup
Reference Count
Fan-out Completion
Heavy Address Isolation
Group Split on Failure
```

---

# 42. P1

```text
Dataset Bundle
Multi-Dataset Scan
Density-aware Grouping
Small Address Packing
Planner Efficiency Metrics
Provider Request Savings
```

---

# 43. P2

```text
Adaptive Group Size
Historical Density Model
Poison Address Detection
Automatic Bundle Selection
```

---

# 44. 验收 Case A

输入：

```text
1,000 addresses
相同 Dataset
相同 Range
```

不能产生：

```text
1,000 个独立 Provider Range Task
```

必须明显合并。

---

# 45. 验收 Case B

两个 Batch 请求重叠 Range。

要求：

```text
SharedWork 复用
```

不能重复下载。

---

# 46. 验收 Case C

同一 Batch：

```text
Transactions
Token Transfers
Logs
```

如果 Provider 支持 Multi-Dataset Scan：

```text
只扫描一次 Range
```

---

# 47. 验收 Case D

某个超活跃地址：

```text
自动隔离
```

不能拖慢所有普通地址。

---

# 48. 验收 Case E

AddressGroup 失败：

```text
自动拆组
```

最终只定位失败地址。

---

# 49. 验收 Case F

Batch A 取消。

Batch B 仍引用 SharedWork：

```text
SharedWork 继续
```

---

# 50. 验收 Case G

相同数据第二次请求：

```text
Coverage Hit
```

Provider 请求 = 0。

---

# 51. 核心目标

V3.3 完成后：

```text
地址数量增加
≠
Provider 请求数量线性增加
```

目标是：

```text
10K 地址
```

依然通过：

```text
少量高效 Shared Workload
```

完成。

---

# 52. 最终定义

这是从：

```text
“很多地址 = 很多任务”
```

升级成：

```text
“很多地址 = 少量合并后的数据工作负载”
```

对于真正的大批量紧急下载，这一层通常比继续单纯增加 worker 更有效。
