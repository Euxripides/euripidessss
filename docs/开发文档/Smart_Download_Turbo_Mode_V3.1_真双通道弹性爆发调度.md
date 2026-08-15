# Smart Download Turbo Mode V3.1
## 真双通道 + 弹性爆发调度

> 前提：
> - Turbo Mode V3.0 已生产可用
> - Historical Price Reconstruction 已部署，不再作为本阶段工作
> - ClickHouse 为最终 Source of Truth
> - 本阶段只优化“紧急大批量下载”的速度、调度和资源利用率

# 1. 本阶段目标

V3.0 已解决：

```text
Coverage-first
→ 跳过普通 Provider
→ SQD Cloud bulk
→ RPC validation / repair
→ ClickHouse
```

V3.1 目标：

```text
SQD Cloud + RPC 真正双 Lane
+
动态 Range 分配
+
高优先任务抢占
+
自适应并发
+
实时重切分
+
更快 TTFA / TTFR
+
更短总完成时间
```

# 2. True Dual-Lane

生产 RPC Pool 配置后：

```text
SQD Cloud
→ 历史 Bulk

RPC
→ Tail
→ Hot Range
→ Small Gap
→ High Priority Range
```

两条 Lane 同时向 ClickHouse 产出。

# 3. 动态 Range Allocator

每 10~30 秒根据：

```text
Cloud rows/s
RPC rows/s
Cloud queue
RPC latency
429 rate
DB ingest rate
remaining blocks
priority
```

重新决定下一批 Range 给谁。

不固定 Cloud 95% / RPC 5%。

# 4. Work Stealing

Cloud Pending 堆积、RPC 空闲时：

```text
RPC 可偷取较小 Pending Range
```

反之亦然。

只允许抢 PENDING，不抢 RUNNING，除非触发 Hedge。

# 5. Dynamic Re-shard

某 Range 明显慢于预估时自动拆分。

例如：

```text
94,000,000–95,000,000
```

拆成多个更小子区间，重新分配给 Cloud / RPC。

# 6. Hot Range First

优先级：

```text
P0 当前 Explorer 区间
P1 当前 Investigation 区间
P2 Fund Flow 下一跳区间
P3 最新 Tail
P4 其余历史
```

目标：

```text
先让用户开始分析
再追求全量完成
```

# 7. TTFR

新增：

```text
TTFR = Time To First Relevant Range
```

它比 TTFA 更重要。

当前正在看的时间范围应优先完成并认证。

# 8. RPC Pool

每个 Endpoint 维护：

```text
latency
success_rate
429_rate
timeout_rate
supported_methods
archive_capability
trace_capability
current_workers
```

# 9. RPC Capability Routing

不同 RPC 按能力分工：

```text
eth_getLogs
receipt
trace
archive
metadata
```

不能所有 RPC 承担同一类任务。

# 10. Endpoint-level AIMD

每个 RPC 独立调整并发。

稳定：

```text
逐步加 worker
```

429 / timeout / latency spike：

```text
workers *= 0.5
```

一个 Endpoint 限流，不影响整个 RPC Lane。

# 11. Endpoint Isolation

状态：

```text
HEALTHY
DEGRADED
OPEN
HALF_OPEN
```

单点故障只隔离该 Endpoint。

# 12. Cloud Burst

SQD Cloud 根据：

```text
剩余 workload
Cloud throughput
预算
ClickHouse ingest
```

动态扩：

```text
1 → N parallel jobs
```

# 13. Burst Levels

建议：

```text
L1 NORMAL_TURBO
L2 HIGH
L3 EMERGENCY
```

具体并发必须 Benchmark 决定，不硬编码为生产默认。

# 14. Emergency Burst

前端可增加：

```text
⚡ 极速模式
[ ] 紧急资源爆发
```

紧急模式允许：

```text
更多 Cloud jobs
更多 RPC workers
更高 Writer priority
暂停 NORMAL / BACKGROUND Jobs
```

# 15. Job Preemption

队列：

```text
URGENT
HIGH
NORMAL
BACKGROUND
```

紧急任务启动：

```text
NORMAL / BACKGROUND
→ CHECKPOINT
→ PAUSED_BY_PRIORITY
```

完成后自动恢复。

# 16. ClickHouse Throughput Governor

总吞吐取决于：

```text
min(
  download throughput,
  parse throughput,
  ClickHouse ingest throughput
)
```

监控：

```text
insert rows/s
insert P95
merge queue
active parts
disk IO
CPU
free disk
```

# 17. Backpressure

如果 DB ingest < download rate：

```text
减少 RPC workers
暂停新 Cloud shard
降低 Cloud burst
```

禁止无限堆积内存队列。

# 18. Parse Worker Pool

单独暴露：

```text
downloaded rows/s
parsed rows/s
inserted rows/s
```

定位瓶颈在：

```text
Network
Parser
Writer
ClickHouse
```

# 19. Range ETA Model

为每种：

```text
chain + dataset + provider
```

记录：

```text
estimated_rows_per_block
blocks_per_sec
rows_per_sec
startup_latency
```

用于预测 Range ETA。

# 20. Historical Performance Model

持续记录：

```text
chain
dataset
provider
block density
address count
range size
rows
duration
throughput
```

下一次 Turbo 直接复用历史数据估算。

# 21. Density-aware Sharding

不再只按固定 blocks 切。

目标：

```text
每 shard 约固定 expected rows
```

例如：

```text
100K rows / shard
```

高密度区间缩小 block range，低密度区间扩大。

# 22. Adaptive Shard Target

根据：

```text
Cloud throughput
RPC latency
Writer speed
```

动态选择：

```text
50K / 100K / 250K rows target
```

# 23. Hedge V2

只对 P0/P1 Range。

触发：

```text
实际耗时 > ETA × 2
```

另一 Lane 开始 Hedge。

Winner = first VALIDATED。

# 24. Gap Repair Priority

Gap 分类：

```text
CRITICAL
HIGH
NORMAL
```

当前 Explorer 区间 Gap = CRITICAL。

历史后台 Gap = NORMAL。

# 25. Progressive Certification

新增：

```text
Range Certified
Dataset Partial Certified
Batch Certified
```

相关区间先认证，用户可先用于调查。

# 26. Turbo Dashboard

显示：

```text
模式：⚡ Turbo

当前可用覆盖：67%
当前相关区间：100% CERTIFIED

SQD Cloud: 3 jobs / 118K rows/s
RPC: 16 workers / 31K rows/s
Parser: 146K rows/s
ClickHouse: 142K rows/s

TTFA: 8.3s
TTFR: 11.2s
ETA: 2m 18s
```

底层 lease/chunk/heartbeat 放 Advanced。

# 27. 一键升档

运行中允许：

```text
AUTO
→ TURBO
→ EMERGENCY
```

已完成 Coverage 永不重做。

# 28. 自动降档

当前相关 Range 已 CERTIFIED 后：

```text
EMERGENCY
→ TURBO
```

自动降低资源消耗。

# 29. Cost Guard

即使 Emergency 也保留：

```text
Cloud hard budget
RPC hard quota
disk guard
ClickHouse guard
```

# 30. P0

```text
Production RPC Pool
True Dual Lane
Dynamic Range Allocator
Endpoint-level AIMD
Cloud Parallel Jobs
ClickHouse Throughput Governor
Priority Range
TTFR
Dynamic Re-shard
```

# 31. P1

```text
Work Stealing
Emergency Burst
Job Preemption
Density-aware Sharding
Historical Performance Model
ETA Model
```

# 32. P2

```text
Hedge V2
Progressive Certification
Auto Downgrade
Predictive Scheduling
```

# 33. 核心验收

Case A：

```text
SQD Cloud 与 RPC 同时有 RUNNING Range
且默认无重叠
```

Case B：

```text
RPC-A 429
→ RPC-A workers 自动下降
→ RPC-B/C 不受影响
```

Case C：

```text
当前 Explorer 区间优先于历史后台区间完成
```

Case D：

```text
ClickHouse 成为瓶颈
→ Cloud/RPC 自动降速
```

Case E：

```text
运行中 AUTO → TURBO → EMERGENCY
已完成数据不重做
```

Case F：

```text
高密度区间自动切更小 shard
低密度区间自动切更大 shard
```

Case G：

```text
相关区间先 CERTIFIED
用户可先分析
整 Batch 后续继续
```

# 34. 完成标准

V3.1 完成后，Turbo 不再只是：

```text
直接走 Cloud + RPC
```

而是：

```text
实时知道哪条 Lane 更快
哪段数据最急
哪里是瓶颈
并自动重新分配资源
```

最终目标：

```text
最短 TTFR
+
最短总完成时间
+
0 gap
+
0 logical duplicate
+
不压垮 ClickHouse
+
不无脑浪费 RPC / Cloud 额度
```
