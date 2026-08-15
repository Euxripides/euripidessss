# Phase 5.4：Production Scale + Runtime Hardening + Objective Planner

> 基线：Phase 5.3 事件驱动 Investigation / Graph Cloud-Aware 联动闭环已完成。
>
> 当前已验证：
> - Dataset Event Bus 持久化，`DATASET_INDEXED` 等事件可幂等消费；
> - `DATASET_INDEXED → Investigation 自动恢复 → Graph 增量更新` 已跑通；
> - Graph 重启恢复后 edge_count 与 Registry 一致，增量追加无重复；
> - 后端重启可扫描 ACTIVE Registry 并补发确定性事件；
> - Cancel 新二进制真实重放通过；
> - Manifest V2 + parts + checkpoint V2 + multi-part Local Sync 已真实验证；
> - SQD Cloud 继续保持 Tier 100 / Last Resort；
> - 生产 8000 已更新为新后端，当前 local mode，Cloud Worker 已 Idle Remove。
>
> Phase 5.4 的目标不是再改架构，而是：
>
> 1. 补齐 Phase 5.3 尚未真实验证的 Crash 精确 Resume；
> 2. 把 Multipart 从“30 秒心跳上传”升级为正式阈值策略；
> 3. 把 Investigation Objective 真正接入 DataRequirement Planner；
> 4. 对 Event Bus / Secret / Cancel 状态做生产硬化；
> 5. 按 1K → 10K → 50K → 100K 地址逐级扩大真实负载；
> 6. 最终形成可用于生产调查的稳定基线。

---

# 1. Phase 5.4 总体原则

保持现有架构不动：

```text
Investigation / Graph
        ↓
DataRequirement
        ↓
Coverage
        ↓
Smart Download Orchestrator
        ↓
Normal Providers
        ↓ 全部耗尽
SQD Cloud Tier 100
        ↓
R2
        ↓
Local Sync / Validator / Registry
        ↓
DATASET_INDEXED
        ↓
Investigation Resume
        ↓
Graph Incremental Update
```

禁止：

```text
Investigation 直接调用 SQD
Graph 直接调用 SQD
把 SQD Cloud 升为普通 Provider
为了压测绕过 Coverage
为了追求速度关闭 Validator
```

---

# 2. Phase 5.4 P0：真实 Crash 精确 Resume

这是进入大规模测试前的最高优先级。

当前：

```text
Manifest V2
parts[]
rows_committed
checkpoint V2
resume 下载已上传 parts
```

代码已就绪，但尚未真实 crash replay。

## 2.1 测试任务

建议：

```text
25 addresses
50,000 blocks
预期 > 100k rows
```

确保至少生成 3 个 parquet parts。

## 2.2 故障点

必须分别在以下阶段 crash：

```text
A. part-000001 上传完成后
B. part-000002 正在生成时
C. manifest.partial 更新后、最终 manifest 前
D. remote completed 后、local sync 前
```

## 2.3 恢复验收

重启后必须：

```text
读取 checkpoint V2
↓
验证已 committed parts
↓
不重新抓已提交 block
↓
从 last_processed_block + 1 恢复
↓
继续新 part
↓
最终 manifest V2
```

硬指标：

```text
sum(parts.rows) == manifest.row_count
local rows == remote rows
dup = 0
range violation = 0
parts sha256 全部一致
```

---

# 3. Multipart 正式阈值

当前是：

```text
每 30 秒 heartbeat 粒度上传
```

Phase 5.4 改成正式阈值策略。

推荐默认：

```text
MAX_ROWS_PER_PART = 25,000
MAX_BYTES_PER_PART = 64 MB
MAX_BLOCKS_PER_PART = 5,000
MAX_AGE_PER_PART = 30s
```

任一条件满足即 flush：

```text
rows >= 25k
OR bytes >= 64MB
OR blocks >= 5k
OR age >= 30s
```

这样兼顾：

```text
高频地址：按 rows/bytes flush
低频地址：按 blocks/time flush
```

---

# 4. Part 命名与幂等

固定：

```text
part-000001.parquet
part-000002.parquet
part-000003.parquet
```

每个 part 必须 immutable。

禁止：

```text
覆盖已经 committed 的 part
```

checkpoint 记录：

```json
{
  "last_processed_block": 114453238,
  "rows_committed": 50000,
  "next_part_no": 3,
  "parts": [...]
}
```

恢复时：

```text
R2 已有 committed part
→ verify
→ skip
```

---

# 5. Cancel 终态语义升级

当前真实协议已经 PASS，但上层计划表现为：

```text
FAILED
reason = canceled
```

Phase 5.4 建议正式增加：

```text
CANCEL_REQUESTED
CANCELLED
```

JobProgress：

```text
created
waiting
running
completed
cancelled
failed
```

UI 不再把用户主动 Cancel 显示为“失败”。

硬规则：

```text
cancelled != failed
```

---

# 6. Investigation UI 状态语义

Phase 5.3 仍沿用 CREATED / WAITING。

Phase 5.4 补齐：

```text
PLANNING
CHECKING_DATA
WAITING_DATA
DATA_READY
ANALYZING
GRAPH_BUILDING
COMPLETED
CANCELLED
FAILED
```

中文 UI：

```text
正在规划
检查本地数据
等待链上数据
数据已就绪
正在分析
正在生成图谱
调查完成
已取消
失败
```

---

# 7. Objective-Driven Planner

这是 Phase 5.4 的核心功能升级之一。

当前 DataRequirement 字段能够工作，但 Investigation Objective 尚未驱动数据规划。

新增：

```json
{
  "objective": {
    "type": "fund_sink|exchange_offramp|profit|token_profit|source_trace|destination_trace|identity_resolution",
    "description": "...",
    "constraints": {
      "depth": 3,
      "max_addresses": 1000,
      "min_amount_usdt": 10000
    }
  }
}
```

---

# 8. Objective → Dataset Matrix

## 8.1 资金沉淀

优先：

```text
token_transfer
native_transfer
balance_snapshot
counterparty
```

必要时：

```text
trace
```

## 8.2 交易所归集 / 提现

优先：

```text
token_transfer
counterparty
address_labels
deposit_cluster
```

## 8.3 大额获利

优先：

```text
token_transfer
historical_balance
price_snapshot
profit_ledger
```

## 8.4 Token 获利

优先：

```text
specific token_transfer
swap events
DEX interactions
price snapshots
```

## 8.5 上游来源

优先：

```text
incoming token/native transfers
counterparty
trace only if needed
```

## 8.6 下游去向

优先：

```text
outgoing transfer
counterparty
exchange label
```

## 8.7 身份落查

优先：

```text
counterparty cluster
exchange deposit
bridge
contract interaction
label evidence
```

原则：

```text
目标决定数据
而不是所有调查全量下载
```

---

# 9. Planner Cost Guard

Objective Planner 输出 DataRequirement 前估算：

```text
address_count
block_span
dataset_count
estimated chunks
estimated local bytes
estimated cloud eligibility
```

设置：

```text
interactive soft cap
background cap
cloud emergency cap
```

如果目标可由已有数据解决：

```text
不得创建下载任务
```

---

# 10. Event Bus 单节点生产硬化

当前 event / processed 为文件型单节点实现。

若 Phase 5.4 仍是单机生产：

至少补：

```text
cross-process file lock
atomic append
fsync
temp + rename
startup corruption scan
event sequence
```

保证两个本地服务实例不能互相覆盖。

---

# 11. Event Bus 多节点准备

暂时不要求马上部署多节点，但接口抽象：

```text
EventStore
  Append()
  ListSince()
  MarkProcessed()
  HasProcessed()
```

实现：

```text
FileEventStore   # 当前
DBEventStore     # 后续
```

后续多节点可迁移：

```text
SQLite / PostgreSQL
```

业务层不改。

---

# 12. Secret Store

Phase 5.4 建议把生产 Secret 从 Windows User Environment 迁移到现有：

```text
DPAPI + AES-GCM
```

存储：

```text
SQD_DEPLOY_KEY
R2_ACCESS_KEY_ID
R2_SECRET_ACCESS_KEY
R2_ENDPOINT
```

API 只能返回：

```json
{
  "configured": true
}
```

禁止返回真实值。

Runtime 启动时：

```text
Secret Store
↓
process env injection
↓
SQD / Worker
```

---

# 13. 生产规模测试梯度

不要直接 100K。

固定梯度：

```text
Stage A: 1,000 addresses
Stage B: 10,000 addresses
Stage C: 50,000 addresses
Stage D: 100,000 addresses
```

每个 Stage PASS 后才能进入下一档。

---

# 14. Stage A：1K 地址

建议：

```text
1,000 addresses
50,000 blocks
token_transfer
```

目标：

```text
Orchestrator chunking
Coverage
Event throughput
Graph incremental merge
Investigation resume
```

验收：

```text
failed chunks = 0
dup = 0
range violation = 0
event duplicate side effect = 0
graph duplicate edge = 0
```

---

# 15. Stage B：10K 地址

重点：

```text
队列长度
并发调度
Registry 写入
merged/query layer
Graph 节点/边增长
内存
磁盘
```

要求记录：

```text
P50/P95 chunk latency
download throughput
sync throughput
event lag
graph increment duration
DuckDB query latency
```

---

# 16. Stage C：50K 地址

重点：

```text
长时间稳定性
断点恢复
多轮 Provider 切换
Cloud fallback ratio
本地磁盘增长
```

必须至少一次：

```text
后端重启
```

并确认：

```text
Investigation / Graph 自动恢复
```

---

# 17. Stage D：100K 地址

仅在前三档全部 PASS 后执行。

目标不是 Cloud 全部跑 100K，而是：

```text
100K DataRequirement
↓
Coverage + Normal Providers 主处理
↓
Cloud 只接缺口
```

必须确认：

```text
cloud_fallback_ratio 仍低
```

如果 Cloud 占比突然升高：

```text
停止扩容
分析 normal provider reliability
```

---

# 18. 100K Chunk Policy

建议继续：

```text
address chunk = 25～100
block chunk = 50,000
```

动态调整依据：

```text
provider
dataset density
P95
rows/chunk
bytes/chunk
```

禁止固定单个巨大 Job。

---

# 19. Dataset Registry Scale

Registry 需要支持：

```text
100K 地址
大量 entries
多 parquet parts
```

建议增加索引：

```text
chain_id
dataset
address
from_block
to_block
status
provider
```

Coverage 不应该扫描所有 manifest。

---

# 20. Coverage Index Scale

构建专用：

```text
address_dataset_coverage
```

字段：

```text
chain_id
address
dataset
covered_from
covered_to
row_count
updated_at
```

查询：

```text
100K 地址 Coverage
```

应优先走索引，而不是逐文件扫描。

---

# 21. Graph Scale

Graph 不允许一次渲染几十万节点。

区分：

```text
Storage Graph
Analysis Graph
Viewport Graph
```

存储层可大；

分析层按目标筛选；

前端只渲染：

```text
Top-N
当前焦点
depth N
threshold
```

---

# 22. Graph UI 性能门槛

建议：

```text
初始节点 <= 500
可视边 <= 2,000
```

更多数据通过：

```text
cluster
collapse
expand
filter
```

按需加载。

---

# 23. Investigation Address Budget

默认：

```text
max_depth = 3
max_addresses = 1,000
```

用户或 Agent 可根据目标调整。

达到 budget：

```text
PAUSED_BUDGET
```

而不是无限递归。

---

# 24. 数据缓存策略

Phase 5.4 规模测试必须复用缓存。

任何已：

```text
Registry ACTIVE + VALID
Coverage HIT
```

的数据：

```text
不得重复下载
```

---

# 25. Provider Recovery / Cloud Ratio

每档都记录：

```text
normal_provider_jobs
cloud_jobs
fallback_ratio
```

警戒：

```text
fallback_ratio > 0.20
```

不直接判失败，但必须停止扩大规模并分析原因。

SQD Cloud 是保险，不是主链路。

---

# 26. 成本预算

记录：

```text
Cloud worker runtime
R2 storage
R2 operations
rows
bytes
download ratio
```

生成：

```text
cost_per_1m_rows
cost_per_1k_addresses
cost_per_cloud_job
```

供 50K / 100K 前决策。

---

# 27. 可观测性

新增/完善：

```text
orchestrator_plans_total
requirements_total
coverage_hit_ratio
provider_success_rate
provider_p95
cloud_fallback_ratio
event_lag_ms
investigation_resume_ms
graph_increment_ms
registry_rows
registry_entries
merged_rows
multipart_parts_total
resume_count
cancel_count
```

---

# 28. Failure Matrix

每个规模至少抽样：

```text
429
503
timeout
provider circuit open
backend restart
worker restart
R2 temporary failure
local sync failure
event replay
cancel
```

不需要每档全做一次重型故障，只要持续回归关键路径。

---

# 29. 数据一致性门槛

每档必须：

```text
source/manifest rows
=
validated rows
=
registered rows
=
merged distinct rows
```

以及：

```text
dup = 0
range_violation = 0
unexpected_address = 0
```

---

# 30. Phase 5.4 PASS Gate

## Runtime Hardening

- [ ] Multipart 阈值正式实现
- [ ] 真实 Crash Resume PASS
- [ ] 多 part 行数精确累计 PASS
- [ ] Cancelled 独立终态
- [ ] WAITING_DATA / DATA_READY UI 状态
- [ ] EventStore 文件锁/原子写
- [ ] Secret Store 迁移完成

## Objective Planner

- [ ] Investigation Objective Contract
- [ ] Objective → Dataset Matrix
- [ ] Planner Cost Guard
- [ ] 不必要数据不下载
- [ ] Objective E2E PASS

## Scale

- [ ] 1K PASS
- [ ] 10K PASS
- [ ] 50K PASS
- [ ] 100K PASS
- [ ] Coverage cache 生效
- [ ] Registry/Coverage 查询稳定
- [ ] Graph 增量无重复
- [ ] Investigation 自动恢复
- [ ] Backend restart recovery
- [ ] cloud_fallback_ratio 可控
- [ ] 无 Secret 泄漏

---

# 31. Phase 5.4 完成后的系统状态

达到：

```text
100K 地址级 DataRequirement
        ↓
自动 Coverage
        ↓
自动 Provider 调度
        ↓
自动故障恢复
        ↓
自动 Registry
        ↓
自动 Investigation Resume
        ↓
自动 Graph Increment
```

并且：

```text
Cloud 仍为 Tier 100
数据不重复下载
调查不因缺数据失败
图谱不因数据到达而全量重建
重启后不丢任务
```

---

# 32. 后续 Phase 5.5

只有 Phase 5.4 通过后，再进入：

```text
Investigation Intelligence Layer
```

重点：

```text
资金沉淀识别
获利分析
交易所归集/提现识别
地址聚类
身份线索评分
调证信息生成
```

也就是从“数据和运行时工程”正式转向“调查 intelligence”本身。
