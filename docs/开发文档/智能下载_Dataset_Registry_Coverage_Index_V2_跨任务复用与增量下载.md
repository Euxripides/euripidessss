# 智能下载下一阶段优化：Dataset Registry Coverage Index V2
## Cross-Task Reuse + Incremental Download + Coverage-Aware Scheduling

> 当前阶段已经具备：
>
> - 统一“智能下载”入口
> - Batch / Address / Dataset / Range 四层任务模型
> - Checkpoint V3
> - Range Ledger
> - Provider Adapter
> - Discovery / Probe
> - Adaptive Scheduler
> - Validation / Gap Repair
> - Progress / ETA / SSE
> - 创建下载 / 任务中心 / 结果数据前端骨架
>
> 下一阶段最值得做的优化不是继续增加新的 Provider，而是：
>
> **让系统知道“本地已经拥有什么数据”，并把已有数据当作第一优先级的数据源。**
>
> 核心目标：
>
> > **已经下载、校验并入库的数据，后续任何任务都不重复下载。**
>
> 本阶段正式建设：
>
> ```text
> Dataset Registry Coverage Index V2
> +
> Cross-Task Reuse Engine V1
> +
> Incremental Download Planner V1
> +
> Active Download Deduplication V1
> ```

---

# 1. 为什么这是下一个最重要的优化方向

当前智能下载已经能做到：

```text
CSV
↓
SQD
↓
RPC
↓
SQD Cloud
```

并且能自动切换和断点续传。

但如果用户今天下载：

```text
地址 A
2024-01-01 → 2026-08-08
Token Transfers
```

明天又创建：

```text
地址 A
2025-01-01 → 2026-08-08
Token Transfers
```

如果系统仍然重新跑 Provider，那么：

- SQD 压力重复；
- RPC 请求重复；
- Cloud 费用重复；
- 本地磁盘重复写；
- Validation 重复；
- 调查等待时间变长。

因此真正成熟的数据系统应该先问：

```text
本地已经有什么？
```

而不是：

```text
这次用哪个 Provider 下载？
```

---

# 2. 调度优先级重新定义

以后 Scheduler 的第一层不再是：

```text
CSV / SQD / RPC
```

而是：

```text
LOCAL CERTIFIED DATA
↓
ACTIVE TASK REUSE
↓
CSV / DIRECT
↓
SQD
↓
RPC
↓
SQD Cloud
```

即：

```text
Local First
```

---

# 3. Coverage Index 的核心语义

索引粒度：

```text
Chain
+
Address
+
Dataset
+
Coverage Range
+
Certification
```

例如：

```text
BSC
0xAAA
token_transfers

Coverage:
40,000,000 → 45,000,000

Certification:
CERTIFIED
```

当用户再次请求：

```text
42,000,000 → 46,000,000
```

系统计算：

```text
Local:
42,000,000 → 45,000,000

Missing:
45,000,001 → 46,000,000
```

只下载 Missing。

---

# 4. CoverageIndexEntry

建议：

```go
type CoverageIndexEntry struct {
    ID string

    ChainID int64
    Address string
    Dataset DatasetType

    FromBlock uint64
    ToBlock   uint64

    FromTime *time.Time
    ToTime   *time.Time

    Certification string

    RowCount uint64

    DatasetPath string
    ManifestPath string

    CreatedAt time.Time
    UpdatedAt time.Time

    SourceBatchIDs []string

    SchemaVersion int
}
```

---

# 5. Certification 必须进入索引

不是所有本地数据都能自动复用。

建议：

```text
CERTIFIED
→ 默认复用

PARTIAL
→ 只复用已确认覆盖区间

UNVALIDATED
→ 不默认复用

FAILED
→ 不复用

STALE
→ 依据 Dataset 决定
```

---

# 6. 余额类 Dataset 特殊处理

Transactions / Logs / Token Transfers 属于历史事实：

```text
历史闭合范围一旦认证
基本不会变化
```

但：

```text
Balance
Address Summary
Token Holding
NFT Holding
```

属于快照数据。

必须带：

```text
snapshot_block
snapshot_time
```

例如：

```go
type SnapshotCoverage struct {
    SnapshotBlock uint64
    SnapshotTime  time.Time

    TTL time.Duration
}
```

---

# 7. Coverage 与 Snapshot 必须分开

历史事件 Dataset：

```text
Range Coverage
```

例如：

```text
40M → 45M
```

余额：

```text
Snapshot
```

例如：

```text
Block 45,000,000
```

不能把：

```text
balances 0-0
```

当作普通 Range。

前端目前截图里：

```text
balances
区间 0 - 0
```

建议后续改成：

```text
快照区块：45,123,456
```

或者：

```text
最新快照
2026-08-08 11:32
```

---

# 8. Coverage Merge

同地址同 Dataset：

```text
40M → 41M
41M+1 → 42M
42M+1 → 43M
```

应该合并为：

```text
40M → 43M
```

索引不应长期保留大量碎片。

但底层 Part 不需要物理合并。

只在逻辑 Coverage 上合并。

---

# 9. Coverage Interval Set

复用 Validation 的：

```text
IntervalSet
```

需要支持：

```text
Union
Subtract
Intersect
Contains
FindMissing
```

例如：

```text
Requested:
40M → 50M

Local:
40M → 42M
45M → 47M

Missing:
42M+1 → 45M-1
47M+1 → 50M
```

---

# 10. 创建任务时先做 Coverage Resolution

原流程：

```text
Create Job
↓
Discovery
↓
Scheduler
```

新流程：

```text
Create Job
↓
Coverage Resolver
↓
Local Hit?
├─ FULL HIT
│  → 直接复用
│
├─ PARTIAL HIT
│  → 只对 Missing Range 做 Discovery
│
└─ MISS
   → 正常 Discovery
```

这样 Discovery 自己也不会重复探测已覆盖数据。

---

# 11. FULL HIT

请求：

```text
40M → 45M
```

本地：

```text
40M → 45M CERTIFIED
```

直接：

```text
DatasetJob = COMPLETED_REUSED
```

不创建网络 RangeJob。

---

# 12. PARTIAL HIT

请求：

```text
40M → 50M
```

本地：

```text
40M → 47M
```

创建：

```text
Reuse Range:
40M → 47M

Download Range:
47M+1 → 50M
```

最终合并成：

```text
40M → 50M
```

---

# 13. Local Reuse 也应该进入 Range Ledger

不要把本地数据复用做成一个旁路。

Range Ledger 应记录：

```json
{
  "event": "RANGE_REUSED",
  "from_block": 40000000,
  "to_block": 47000000,
  "source_dataset_id": "ds_old",
  "certification": "CERTIFIED"
}
```

这样最终 Manifest 能解释：

```text
这部分不是这次下载的
而是复用了本地数据
```

---

# 14. Source Provenance

每个 final Dataset 要保留来源：

```text
LOCAL_REUSE
CSV
SQD
RPC
SQD_CLOUD
```

例如：

```json
{
  "coverage": [
    {
      "from": 40000000,
      "to": 47000000,
      "source": "LOCAL_REUSE"
    },
    {
      "from": 47000001,
      "to": 50000000,
      "source": "SQD"
    }
  ]
}
```

---

# 15. Cross-Task Reuse

除了已完成数据，还要解决：

```text
两个任务同时下载相同数据
```

例如：

```text
Task A:
0xAAA token_transfers 40M-50M

Task B:
0xAAA token_transfers 45M-55M
```

Task A 正在运行。

Task B 不应该重新请求：

```text
45M-50M
```

---

# 16. Active Coverage Registry

新增：

```text
ActiveCoverageRegistry
```

记录：

```text
哪个 DatasetJob
正在获取哪个 Range
```

例如：

```json
{
  "chain_id": 56,
  "address": "0xAAA",
  "dataset": "token_transfers",
  "from_block": 40000000,
  "to_block": 50000000,
  "dataset_job_id": "ds_A",
  "status": "RUNNING"
}
```

---

# 17. Task Subscription

Task B 发现：

```text
45M → 50M
```

已被 Task A 获取。

Task B：

```text
不再创建网络请求
```

而是创建：

```text
Dependency / Subscription
```

例如：

```go
type DatasetDependency struct {
    ConsumerDatasetJobID string
    ProducerDatasetJobID string

    FromBlock uint64
    ToBlock   uint64

    Status string
}
```

---

# 18. Producer 成功

Task A 完成：

```text
45M → 50M CERTIFIED
```

Task B：

```text
自动把该 Range 标记 REUSED
```

然后只下载：

```text
50M+1 → 55M
```

---

# 19. Producer 失败

Task A 如果最终：

```text
PARTIAL / FAILED
```

Task B 不应该一起失败。

Dependency Resolver：

```text
解除依赖
↓
重新计算 Missing Range
↓
自己进入 Scheduler
```

---

# 20. 任务不能互相死锁

如果：

```text
Task A 等 Task B
Task B 又等 Task A
```

会死锁。

因此依赖规则：

```text
只能订阅更早创建的 Producer
```

或：

```text
ActiveCoverageRegistry 为每个 Range 只指定一个 Owner
```

推荐：

```text
Single Range Owner
+
N Consumers
```

---

# 21. Range Ownership

定义：

```go
type RangeOwnership struct {
    Key string

    OwnerDatasetJobID string

    Consumers []string

    LeaseExpiresAt time.Time
}
```

Owner 崩溃：

```text
Lease Reaper
↓
重新选 Owner
```

---

# 22. Incremental Download

这是本阶段最直接的用户收益。

例如第一次：

```text
2024-01-01 → 2026-08-08
```

一周后再次调查：

```text
全量
```

系统不重新跑历史。

只需要：

```text
Last Certified Block + 1
→ Latest Finalized Block
```

---

# 23. FULL 模式的重新定义

现在“全量”可能理解为：

```text
每次从 First Seen 重新下载
```

以后应该改成：

```text
用户逻辑要求全量
但系统物理执行增量
```

即：

```text
FULL logical coverage
+
INCREMENTAL physical download
```

---

# 24. Latest Finalized Block

增量下载必须使用：

```text
finalized / safe block
```

不能直接依赖 latest head。

避免：

```text
链重组
```

导致历史本地数据不稳定。

---

# 25. Reorg Safety Window

建议保留：

```text
ReorgOverlap
```

例如 BSC：

```text
最近 N blocks
```

增量时重新拉最后一小段：

```text
local_max_block - safety_window
→ latest_finalized
```

然后：

```text
unique key dedup
```

这样更稳。

---

# 26. 历史区间冻结

超过安全窗口的历史范围：

```text
FROZEN
```

例如：

```text
block <= finalized - reorg_window
```

Coverage Index 标记：

```text
IMMUTABLE
```

以后可以长期直接复用。

---

# 27. Incremental Plan

例如：

```text
Local:
40M → 50M

Latest Finalized:
52M

Safety Window:
2,000 blocks
```

实际下载：

```text
49,998,001 → 52M
```

最终 Merge：

```text
40M → 52M
```

---

# 28. 本地数据复用前必须 Schema Compatible

不能只看：

```text
chain/address/dataset/range
```

还要检查：

```text
Canonical Schema Version
Parser Version
Normalization Version
```

例如：

```text
Schema 1.4.1
```

本地旧数据是：

```text
1.2
```

需要：

```text
Migration
```

或：

```text
Reprocess
```

不能直接混。

---

# 29. CompatibilityKey

建议：

```text
dataset
+ schema_version
+ normalization_version
+ chain_id
```

例如：

```text
token_transfers:bsc:schema1.4:normalizer3
```

---

# 30. 不兼容时优先 Reprocess，不一定 Re-download

如果原始数据还在：

```text
raw
```

但 canonical schema 旧：

```text
不要重新向 Provider 下载
```

执行：

```text
Raw
↓
New Normalizer
↓
New Canonical
↓
Validation
```

这属于：

```text
LOCAL_REPROCESS
```

---

# 31. 新的数据源优先级

Scheduler 最终候选应变成：

```text
LOCAL_CERTIFIED
LOCAL_REPROCESS
ACTIVE_TASK_REUSE
CSV
DIRECT
SQD
RPC
SQD_CLOUD
```

---

# 32. LocalityScore

Scheduler V3 增加：

```text
LocalityScore
```

并给予非常高权重。

因为：

```text
本地读取
```

通常比网络 Provider：

```text
更快
更便宜
更稳定
```

---

# 33. 前端创建页改造

当前：

```text
□ 本地已有数据直接复用
```

建议删除。

改成默认文案：

```text
✓ 自动复用本地已验证数据
```

高级设置中提供：

```text
□ 强制重新下载所有数据
```

默认关闭。

---

# 34. Discovery 前端反馈

创建任务后：

```text
本地数据检查
```

可以显示：

```text
12,093 个地址

完全命中      6,821
部分命中      3,210
需要新下载    2,062

预计减少下载：
71%
```

这是非常直观的价值反馈。

---

# 35. 任务中心显示 Reuse

Provider Badge 增加：

```text
LOCAL
```

例如：

```text
Token Transfer
100%
LOCAL
已复用
```

不要假装成“下载”。

---

# 36. Address Detail

显示：

```text
数据来源

本地复用        82%
SQD             16%
RPC              2%
```

用户可以清楚知道数据构成。

---

# 37. 结果页 Coverage 视图

建议结果 Dataset 加：

```text
Coverage
```

例如：

```text
40M ━━━━━━━━━━━━━━━━━━━━━━━━━━━ 52M

LOCAL         40M ━━━━━━━ 50M
REFRESH                   ━ 50.0M
SQD                         ━━━ 52M
```

高级详情可以看。

---

# 38. Dataset Registry V2 目录

项目不引入数据库。

建议：

```text
smart-download/
└── registry/
    ├── coverage/
    │   ├── bsc/
    │   │   ├── 00/
    │   │   ├── 01/
    │   │   └── ff/
    │   └── ...
    │
    ├── active/
    │   ├── active-coverage.json
    │   └── leases.ndjson
    │
    ├── indexes/
    │   ├── chain-dataset-index.json
    │   └── address-index/
    │
    └── registry-events.ndjson
```

---

# 39. Registry 文件不能是一个大 JSON

10 万地址以后禁止：

```text
coverage-index.json
```

全部塞一起。

需要：

```text
chain
+
address hash shard
```

分片。

例如：

```text
coverage/bsc/7a/0x7a....json
```

---

# 40. Coverage Entry 文件

例如：

```json
{
  "schema_version": 2,
  "chain_id": 56,
  "address": "0x...",
  "datasets": {
    "token_transfers": {
      "compatibility_key": "token_transfers:bsc:1.4.1:n3",
      "certified_ranges": [
        {
          "from_block": 40000000,
          "to_block": 52000000,
          "rows": 1828112
        }
      ],
      "updated_at": "..."
    },
    "balances": {
      "snapshot": {
        "block": 52000000,
        "time": "...",
        "ttl_seconds": 300
      }
    }
  }
}
```

---

# 41. Registry 写入原则

只有：

```text
Validation Certificate PASS
```

以后才能写入：

```text
certified_ranges
```

PARTIAL：

```text
只写已经确认完整的子区间
```

不能把整个请求 Range 都标记为已覆盖。

---

# 42. Registry 与 Final Dataset 一致性

如果 Registry 说：

```text
40M → 50M CERTIFIED
```

对应：

```text
manifest
parts
certificate
```

必须真实存在。

启动 Recovery 时做：

```text
Registry Reconcile
```

---

# 43. 删除数据时 Registry 必须同步

如果用户删除：

```text
final dataset
```

Registry 不能继续认为 Local Hit。

删除流程：

```text
Remove Dataset
↓
Update Registry
↓
Rebuild Coverage
```

---

# 44. Storage Lifecycle

未来可以加入：

```text
raw 清理
staging 清理
final 保留
```

即使 raw 删除：

```text
Certified Final
```

仍可继续复用。

但如果需要 schema reprocess：

```text
raw 不存在
```

则可能需要重下载。

---

# 45. Coverage Query API

新增：

```http
POST /api/smart-download/coverage/query
```

请求：

```json
{
  "chain_id": 56,
  "address": "0x...",
  "dataset": "token_transfers",
  "from_block": 40000000,
  "to_block": 52000000
}
```

返回：

```json
{
  "coverage_ratio": 0.82,
  "full_hit": false,
  "covered": [
    [40000000, 50000000]
  ],
  "missing": [
    [50000001, 52000000]
  ],
  "certification": "CERTIFIED"
}
```

---

# 46. Batch Coverage Query

批量创建时不要 10K 地址逐个 HTTP。

内部批量：

```text
BatchCoverageResolve
```

一次处理全部地址。

---

# 47. 性能目标

## 10K 地址

Coverage Resolve：

```text
< 2s 目标
```

在索引热缓存下。

## 100K 地址

```text
不能全目录扫描
```

必须索引直达。

---

# 48. Hot Coverage Cache

内存缓存最近：

```text
地址 Coverage
```

例如 LRU：

```text
50K entries
```

减少重复文件读取。

---

# 49. Registry Update Event

Validation 完成：

```text
dataset.certified
↓
CoverageRegistry.Update
```

产生：

```text
coverage.updated
```

其他等待 Task 可以立即复用。

---

# 50. 任务依赖事件

Producer 完成：

```text
producer.coverage.ready
```

Consumers：

```text
dependency.satisfied
```

继续执行。

---

# 51. P0 实施内容

必须：

```text
Coverage Index V2
Interval coverage query
FULL HIT
PARTIAL HIT
Incremental Planner
Range Ledger REUSED event
LOCAL Provider/View
Schema compatibility check
Registry recovery
```

---

# 52. P1 实施内容

随后：

```text
Active Coverage Registry
Cross-Task Reuse
Single Range Owner
Task Subscription
Lease Recovery
Reorg Safety Window
Snapshot TTL
```

---

# 53. P2

进一步：

```text
Local Reprocess
Schema Migration
Storage Lifecycle
Cold Data Archive
Coverage Compaction
```

---

# 54. 真实验收 Case A：完全命中

第一次：

```text
0xAAA
40M → 50M
Token Transfer
```

完成且 CERTIFIED。

第二次请求相同数据。

要求：

```text
network requests = 0
FULL HIT
直接完成
```

---

# 55. Case B：部分命中

本地：

```text
40M → 50M
```

请求：

```text
45M → 55M
```

要求：

```text
Reuse:
45M → 50M

Download:
50M+1 → 55M
```

最终：

```text
45M → 55M CERTIFIED
```

---

# 56. Case C：增量全量

本地：

```text
First Seen → 50M
```

用户再次选择：

```text
全量
```

Latest Finalized：

```text
52M
```

要求：

```text
不重新下载 First Seen → 50M
```

只下载增量。

---

# 57. Case D：两个任务同时请求相同 Range

Task A：

```text
40M → 50M
```

Task B：

```text
45M → 55M
```

要求：

```text
45M → 50M
只由一个 Owner 下载
```

Task B 订阅结果。

---

# 58. Case E：Producer 失败

Task A 是 Owner。

Task A 最终失败。

要求：

```text
Task B 自动解除依赖
重新调度 45M → 50M
```

Task B 不被连带失败。

---

# 59. Case F：PARTIAL 本地数据

本地：

```text
40M → 50M
coverage 99%
```

但只有：

```text
40M → 47M
49M → 50M
```

被认证。

新任务只能复用：

```text
已认证子区间
```

必须下载：

```text
47M+1 → 49M-1
```

---

# 60. Case G：Schema 版本变化

本地：

```text
Schema 1.3
```

当前：

```text
Schema 1.4
```

如果 raw 可用：

```text
LOCAL_REPROCESS
```

如果 raw 不可用且 schema 不兼容：

```text
重新下载必要 Range
```

---

# 61. Case H：余额快照

本地余额：

```text
5 分钟前
```

TTL：

```text
300s
```

如果未过期：

```text
LOCAL HIT
```

过期：

```text
RPC Refresh
```

历史交易数据不受影响。

---

# 62. UI 验收

创建页：

```text
✓ 自动复用本地已验证数据
```

Discovery：

```text
本地命中 71%
预计减少下载 129 GB
```

任务中心：

```text
LOCAL
SQD
RPC
```

能够混合显示。

结果页：

```text
来源：
本地复用 82%
新下载 18%
```

---

# 63. 推荐开发顺序

```text
Commit 1
Coverage Index data model

Commit 2
FS sharded coverage store

Commit 3
Coverage Interval Resolver

Commit 4
FULL/PARTIAL HIT planner

Commit 5
Range REUSED integration

Commit 6
Incremental FULL planner

Commit 7
Schema compatibility

Commit 8
Snapshot dataset TTL model

Commit 9
Active Coverage Registry

Commit 10
Cross-Task Subscription

Commit 11
Lease/recovery

Commit 12
Frontend coverage visualization
```

---

# 64. 本阶段不要做什么

暂时不要：

```text
为了 Local Hit 复制 Final Dataset
```

优先引用已有资产。

也不要：

```text
把每次任务结果重新物理合并成一个超大 Parquet
```

Coverage 是逻辑层。

底层可以继续：

```text
多个 Parquet Parts
```

由 DuckDB/Manifest 统一读取。

---

# 65. 对当前前端最直接的修改

当前创建页：

```text
□ 本地已有数据直接复用（LOCAL HIT，跳过重复下载）
```

建议下一版直接改成：

```text
✓ 自动复用本地已验证数据
```

不可取消。

高级设置：

```text
强制重新下载
```

默认关闭。

当前结果页的：

```text
同地址 + 同 Dataset + 多区间
```

也应该通过 Coverage Index 聚合，不再作为多条独立资产展示。

---

# 66. 下一阶段之后

Coverage / Reuse 做完后，下一步最值得优化的是：

```text
Investigation Data Cache V2
+
Graph Expansion Cache
+
Smart Prefetch
```

即：

```text
智能调查和关系图扩展时
提前预测下一跳地址
并在后台低优先级预取数据
```

这样关系图点开下一层时，可以直接命中本地数据，不再等待下载。

---

# 67. 一句话目标

> **下一阶段要让“本地已验证数据”成为智能下载系统的第一 Provider：相同数据不再重复下载，部分覆盖只补缺口，正在下载的相同 Range 由多个任务共享，真正把智能下载从下载调度器升级成持续积累的数据资产系统。**
