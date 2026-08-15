# Smart Download Turbo Production Hardening V3.2
## 极速下载生产加固 — ETA、瓶颈诊断、资源档位与任务复盘

> 当前明确不做：
>
> ```text
> Predictive Prefetch
> 调查级数据准备
> 自动预取下一跳
> ```
>
> 当前继续强化：
>
> ```text
> Smart Download
> Turbo Mode
> SQD Cloud + RPC
> ClickHouse
> ```

# 1. 本阶段目标

不是继续堆新智能能力。

而是让 Turbo 变成：

```text
能预估
能看懂
能控资源
能定位瓶颈
能快速恢复
能复盘
```

# 2. Preflight Estimate

任务开始前先快速估算：

```text
预计 Block 范围
预计地址数
预计 Dataset 数
预计 Rows
预计数据量
预计 Cloud Job 数
预计 RPC Calls
预计完成时间
预计磁盘增长
```

前端显示：

```text
预计 18.2M rows
预计 3.8 GB
预计 7~12 分钟
```

避免用户盲跑。

# 3. 资源档位

新增：

```text
标准
高性能
极速
```

例如：

```text
标准
→ 低资源
→ 普通任务

高性能
→ 更多 Cloud / RPC
→ 大批量任务

极速
→ Turbo
→ 时间优先
```

不要让用户手动调几十个 workers。

# 4. ETA V2

ETA 不只按：

```text
blocks remaining
```

而要综合：

```text
当前 rows/s
历史 rows/block
Cloud startup latency
RPC throughput
Parser throughput
ClickHouse ingest throughput
```

前端持续更新：

```text
预计剩余 4m 32s
```

# 5. Bottleneck Detector

统一判断当前瓶颈：

```text
SOURCE
NETWORK
RPC
CLOUD
PARSER
CLICKHOUSE
DISK
VALIDATION
```

任务卡直接显示：

```text
当前瓶颈：ClickHouse 写入
```

而不是让用户自己猜。

# 6. Throughput Pipeline

统一显示：

```text
Download
145K rows/s

Parse
141K rows/s

ClickHouse
138K rows/s
```

最慢一层自动标红/警告。

# 7. Stall Detector

如果某阶段：

```text
N 秒无进展
```

自动判断：

```text
Cloud stalled
RPC stalled
Parser stalled
Writer stalled
```

并进入：

```text
SELF_RECOVERY
```

# 8. Self Recovery

例如：

```text
Cloud stalled
→ restart shard

RPC timeout
→ switch endpoint

Parser stopped
→ restart worker

ClickHouse transient failure
→ retry DB stage
```

避免任务整体失败。

# 9. Retry Scope

重试必须做到最小范围：

```text
Range
Dataset
Shard
DB Stage
```

不能：

```text
整个 Batch 重跑
```

# 10. Failure Summary

任务失败时输出：

```text
失败阶段
失败 Dataset
失败 Range
Provider
错误类型
已完成比例
可继续恢复点
推荐操作
```

例如：

```text
FAILED: RPC_RATE_LIMIT
Dataset: receipt
Range: 94,805,000–94,806,000
Completed: 92%
Action: 自动重试 / 切换 RPC
```

# 11. Cancel / Pause / Resume

所有 Turbo Job 必须完整支持：

```text
Pause
Resume
Cancel
```

Pause：

```text
保存 checkpoint
释放资源
```

Resume：

```text
只跑剩余 Range
```

# 12. Restart Recovery

服务重启后：

```text
扫描未完成 Job
恢复 Range Ledger
恢复 Dataset 状态
恢复 Address 状态
恢复 Batch 状态
```

不能出现：

```text
数据库写完
UI 仍 RUNNING
```

# 13. Status Reconciler

新增：

```text
Job Status Reconciler
```

周期检查：

```text
Batch
Address
Dataset
Range
```

状态一致性。

例如：

```text
所有 Range COMPLETED
Dataset 仍 RUNNING
```

自动修复。

# 14. Storage Guard

任务开始前：

```text
预计新增数据量
+
当前 E 盘剩余空间
```

如果风险过高：

```text
拒绝启动
```

或：

```text
警告
```

# 15. ClickHouse Guard

监控：

```text
Merge Queue
Active Parts
Insert Latency
Disk IO
```

进入 Critical 时：

```text
Turbo 自动降档
```

# 16. RPC Quota Guard

每个 RPC：

```text
预计调用量
当前额度
历史消耗
```

避免 Turbo 把额度瞬间打空。

# 17. Cloud Budget Guard

显示：

```text
本任务预计 Cloud 消耗
```

如果用户设置 Hard Limit：

```text
不得超限
```

# 18. Task Template

支持保存：

```text
BSC USDT 全量
BSC Token Transfer 30D
大批量地址 Transactions
大批量地址 Token Transfers
```

下次直接复用。

# 19. Recent Jobs

下载中心显示：

```text
任务
模式
地址数
区间
Rows
耗时
TTFA
平均吞吐
结果
```

# 20. Performance History

记录：

```text
dataset
range size
address count
cloud rows/s
rpc rows/s
db rows/s
total duration
```

用于之后 ETA。

# 21. Job Report

每个任务完成后自动生成：

```text
Batch ID
Mode
Provider
Rows
Coverage
Duplicates
TTFA
Total Time
Peak Throughput
Average Throughput
Retry Count
Gap Repair Count
Certification
```

# 22. Compare Runs

同类型任务支持对比：

```text
Run A
Run B
```

例如：

```text
Turbo V3.0
vs
Turbo V3.2
```

看：

```text
TTFA
总耗时
rows/s
失败率
```

# 23. Frontend

任务卡建议显示：

```text
⚡ Turbo

进度 68%
Coverage 71%

Download 145K/s
Parse 141K/s
DB 138K/s

瓶颈：ClickHouse

ETA 4m 32s
```

# 24. Advanced Detail

点击 Advanced：

```text
Cloud Jobs
RPC Workers
Range Ledger
Retries
Errors
Checkpoints
Gap Repair
```

普通页面不展示这些噪音。

# 25. P0

```text
Preflight Estimate
ETA V2
Bottleneck Detector
Pipeline Throughput
Stall Detector
Self Recovery
Status Reconciler
Storage Guard
RPC Quota Guard
Cloud Budget Guard
```

# 26. P1

```text
Task Templates
Job Report
Performance History
Compare Runs
Resource Profiles
```

# 27. P2

```text
Adaptive ETA
Auto Profile Selection
Historical Benchmark Learning
```

# 28. 验收 Case A

任务开始前：

```text
能够给出预计 rows / size / ETA
```

# 29. 验收 Case B

ClickHouse 比下载慢：

```text
系统正确判断瓶颈 = CLICKHOUSE
```

# 30. 验收 Case C

RPC Endpoint 失效：

```text
只切换 Endpoint
任务不整体失败
```

# 31. 验收 Case D

服务重启：

```text
任务状态正确恢复
已完成 Range 不重做
```

# 32. 验收 Case E

E 盘空间不足：

```text
任务启动前被拦截
```

# 33. 验收 Case F

完成后自动输出任务报告：

```text
Coverage = 100%
Duplicate = 0
Certification = CERTIFIED
TTFA
Total Duration
Throughput
```

# 34. 最终目标

让智能下载从：

```text
“能很快下”
```

升级成：

```text
“我清楚知道要多久，
现在卡在哪，
失败能自己恢复，
不会打爆资源，
完成后还能复盘。”
```

这比当前阶段继续增加预取、Agent 或调查预测更适合生产使用。
