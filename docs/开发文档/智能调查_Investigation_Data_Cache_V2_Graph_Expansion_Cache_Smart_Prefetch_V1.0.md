# 智能下载下一阶段优化：Investigation Data Cache V2 + Graph Expansion Cache + Smart Prefetch V1.0
## 面向智能调查与地址关系图的低延迟数据预取与缓存体系

> 前置能力：
>
> - Smart Download 统一入口
> - Batch / Address / Dataset / Range 四层任务模型
> - Universal Checkpoint V3
> - Range Ledger
> - Provider Adapter
> - Discovery / Adaptive Scheduler
> - Validation Pipeline / Gap Repair
> - Progress / ETA / SSE
> - Smart Download Frontend V2
> - Dataset Registry Coverage Index V2
> - Cross-Task Reuse
> - Incremental Download
>
> 下一阶段目标：
>
> **让智能调查与地址关系图不再“点一下、等一次下载”，而是根据当前调查上下文自动预测下一批最可能被展开的地址，并提前低优先级预取数据。**
>
> 核心目标：
>
> ```text
> 用户查看地址 A
> ↓
> 系统分析 A 的高价值对手方
> ↓
> 预测用户下一步可能展开 B / C / D
> ↓
> 后台低优先级预取
> ↓
> 用户点击 B
> ↓
> LOCAL HIT
> ↓
> 秒开关系图 / 秒开调查
> ```
>
> 本阶段正式建设：
>
> ```text
> Investigation Data Cache V2
> +
> Graph Expansion Cache V1
> +
> Smart Prefetch Planner V1
> +
> Background Low-Priority Download Queue V1
> ```

---

# 1. 为什么这是下一步最值得做的优化

Dataset Registry V2 解决的是：

> “已经下载过的数据，不要重复下载。”

下一步应该解决：

> “用户还没点的数据，能不能提前下载一点，让下一步操作秒开？”

尤其在以下场景价值非常高：

```text
资金追踪
地址关系图
智能调查
大额转账追踪
交易所落地身份追踪
沉淀地址识别
上下游扩展
```

当前典型体验：

```text
点击地址 B
↓
发现本地没数据
↓
创建下载任务
↓
等待
↓
生成图
```

下一阶段目标：

```text
查看地址 A 时
↓
系统后台提前准备 B
↓
点击 B
↓
立即展开
```

---

# 2. 总体架构

```text
智能调查 / 地址关系图
        ↓
Context Analyzer
        ↓
Candidate Generator
        ↓
Prefetch Scorer
        ↓
Coverage Index 查询
        ↓
已有数据？
├─ YES → Cache Hit
└─ NO
    ↓
Background Prefetch Queue
    ↓
Smart Download
    ↓
Low Priority Scheduler
    ↓
CSV / SQD / RPC / Cloud
    ↓
Validation
    ↓
Dataset Registry
    ↓
Graph / Investigation 秒级复用
```

---

# 3. 三层缓存结构

建议拆为：

```text
L1 Session Cache
L2 Investigation Cache
L3 Dataset Registry Cache
```

---

# 4. L1 Session Cache

范围：

```text
当前浏览器会话 / 当前调查会话
```

缓存：

- 当前地址摘要
- 当前图节点
- 当前边
- 当前筛选条件
- 最近展开地址
- 当前 Token / 时间范围
- 当前路径
- UI 已加载 Dataset 页

特点：

```text
最快
最短生命周期
可随会话释放
```

---

# 5. L2 Investigation Cache

范围：

```text
案件 / Investigation
```

例如：

```text
investigation_id
```

缓存：

- 已调查地址
- 高优先级对手方
- 资金路径
- 路径节点
- 标签
- 当前证据数据
- 地址画像
- 关系图聚合结果
- 预取状态
- 下一个推荐展开节点

生命周期：

```text
案件存在期间长期保存
```

---

# 6. L3 Dataset Registry Cache

这是已经完成的：

```text
链上原始 / Canonical Dataset
```

例如：

```text
Transactions
Token Transfers
Internal Tx
Logs
Balance Snapshot
```

是真正的数据资产层。

---

# 7. Graph Expansion Cache

关系图不应该每次都从几百万行重新计算。

针对：

```text
Address
+
Direction
+
Token
+
Time Range
+
Depth
```

生成扩展缓存。

---

# 8. GraphExpansionKey

建议：

```go
type GraphExpansionKey struct {
    ChainID int64

    Address string

    Direction string

    DatasetSet []DatasetType

    TokenFilter string

    FromBlock uint64
    ToBlock   uint64

    Depth int

    AggregationVersion int
}
```

---

# 9. Graph Expansion Result

缓存：

```go
type GraphExpansionResult struct {
    Key GraphExpansionKey

    Nodes []GraphNode
    Edges []GraphEdge

    TotalInflow  string
    TotalOutflow string

    CounterpartyCount int

    Coverage float64
    Certification string

    GeneratedAt time.Time
}
```

---

# 10. 为什么 Graph Cache 要独立于 Dataset Cache

Dataset Cache 保存：

```text
原始事实
```

Graph Cache 保存：

```text
聚合结果
```

例如：

```text
1,200,000 Token Transfers
```

经过：

```text
按 counterparty 聚合
```

可能只剩：

```text
1,821 条边
```

用户反复展开图时，直接复用聚合结果会快很多。

---

# 11. Smart Prefetch Planner

Prefetch Planner 负责预测：

```text
下一步最可能被查看哪些地址？
```

不是所有邻居都下载。

必须先评分。

---

# 12. PrefetchCandidate

```go
type PrefetchCandidate struct {
    ChainID int64
    Address string

    ParentAddress string

    Reason string

    Score float64

    EstimatedRows uint64
    EstimatedBytes uint64

    RequiredDatasets []DatasetType

    Priority string
}
```

---

# 13. Candidate 来源

候选地址来自：

```text
Top Inflow Counterparties
Top Outflow Counterparties
大额转账地址
高频交互地址
交易所候选地址
资金沉淀地址
多跳路径关键节点
智能调查 Agent 推荐节点
手工 Pin 节点
```

---

# 14. Prefetch Score

建议：

```text
PrefetchScore =
  0.25 * FlowValueScore
+ 0.20 * InteractionFrequencyScore
+ 0.15 * PathImportanceScore
+ 0.15 * InvestigationRelevanceScore
+ 0.10 * AddressRiskScore
+ 0.10 * UserExpansionProbability
+ 0.05 * CacheReuseProbability
- CostPenalty
- SizePenalty
```

---

# 15. 不要只按金额排序

比如：

```text
地址 B
金额 1M USDT
但只有一次普通归集
```

和：

```text
地址 C
金额 500K
但处于核心资金路径
```

C 可能更值得提前加载。

因此需要：

```text
Flow Value
+
Path Importance
+
Investigation Relevance
```

综合评分。

---

# 16. 预取级别

建议三档：

```text
P0 HOT
P1 WARM
P2 COLD
```

---

# 17. HOT

满足：

```text
用户当前高概率下一步会点
```

例如：

- 当前图前 3 个最大资金对手方
- 当前路径下一跳
- 用户刚选中的节点
- Agent 明确推荐调查的地址

策略：

```text
立即后台下载
```

---

# 18. WARM

例如：

- Top 10 交易对手
- 二级重点路径
- 高额但非主路径

策略：

```text
有资源时预取
```

---

# 19. COLD

例如：

- 大量低价值邻居
- 低频交互
- 非核心 Token

策略：

```text
不主动下载
```

只保留：

```text
Discovery metadata
```

---

# 20. Prefetch 不应该全量下载所有 Dataset

预取首先只取“调查最常用数据”。

建议默认：

```text
Transactions
Token Transfers
Internal Transactions
Balance
```

不默认：

```text
Raw Logs
Receipts
NFT
全部 Metadata
```

除非调查目标要求。

---

# 21. Minimal Investigation Bundle

定义：

```text
Investigation Bundle
```

默认：

```text
transactions
token_transfers
internal_transactions
balances
address_summary
```

---

# 22. Graph Bundle

关系图展开优先：

```text
transactions
token_transfers
internal_transactions
```

如果当前 Token 是 USDT：

```text
只预取相关 Token Transfer
```

进一步减少数据量。

---

# 23. Prefetch Range

不要默认全量。

根据当前调查范围：

```text
Current Investigation Range
```

例如当前调查：

```text
2026-01-01 → 2026-08-08
```

预取 B：

```text
同样 2026-01-01 → 2026-08-08
```

而不是全历史。

---

# 24. Temporal Context Propagation

如果资金路径：

```text
A → B
发生时间：
2026-07-20
```

预取 B 时可以优先：

```text
2026-07-20 ± 30 days
```

而不是直接全量。

---

# 25. Progressive Prefetch

建议分两步：

```text
Stage 1
小时间窗口

Stage 2
如果用户继续关注
扩大时间范围
```

例如：

```text
第一阶段：
交易前后 7 天

第二阶段：
前后 90 天

第三阶段：
全量
```

---

# 26. Prefetch 与 Coverage Index 联动

任何预取前先执行：

```text
Coverage Query
```

例如：

```text
B 已有 80%
```

只补：

```text
20%
```

如果：

```text
FULL HIT
```

预取成本：

```text
0
```

---

# 27. Prefetch 与 Active Task Reuse 联动

如果另一个任务已经在下载 B：

```text
不要再下载
```

直接：

```text
Subscribe
```

---

# 28. 后台队列必须低优先级

预取永远不能抢用户主动任务资源。

建议优先级：

```text
P0 User Interactive
P1 User Batch
P2 Repair / Validation
P3 HOT Prefetch
P4 WARM Prefetch
```

---

# 29. Scheduler Resource Quota

例如：

```text
总 Worker 64

前台任务最多：
56

Prefetch 最多：
8
```

前台任务增加时：

```text
Prefetch 自动让出 Worker
```

---

# 30. Prefetch 可抢占

如果用户主动创建新任务：

```text
Prefetch Worker
↓
Checkpoint
↓
Pause
↓
释放资源
```

以后空闲再继续。

---

# 31. SQD Cloud 不应用于普通 Prefetch

默认：

```text
Prefetch 禁止进入 SQD Cloud XL
```

因为这是后台猜测需求。

只有：

```text
用户已点击
```

任务升级为：

```text
Interactive
```

才允许进入高成本资源。

---

# 32. Prefetch Cloud Policy

建议：

```text
HOT Prefetch:
最多 Cloud S / L

WARM Prefetch:
禁止 Cloud

用户实际点击后：
重新调度
可进入 Cloud XL
```

---

# 33. Prefetch Budget

新增：

```text
PrefetchBudget
```

配置：

```text
max_disk_per_day
max_network_per_day
max_cloud_cost_per_day
max_prefetch_addresses
max_active_prefetch_jobs
```

---

# 34. 自动停止无价值预取

如果：

```text
地址预取完成
但 7 天内没有被用户使用
```

降低：

```text
ReuseProbability
```

以后类似地址少预取。

---

# 35. Prefetch Feedback Loop

记录：

```text
prefetched
used
unused
time_to_use
saved_wait_time
download_cost
```

---

# 36. Prefetch Success Rate

真正有意义指标：

```text
Prefetch Hit Rate =
被用户实际使用的预取数据
/
全部预取数据
```

目标初期：

```text
> 30%
```

成熟：

```text
> 50%
```

否则说明预取过度。

---

# 37. Saved Latency

记录：

```text
用户点击地址
到图加载完成
```

没有预取：

```text
45s
```

有预取：

```text
1.2s
```

保存：

```text
43.8s
```

用于评价预取价值。

---

# 38. Investigation Cache 结构

建议：

```text
investigations/
└── {investigation_id}/
    ├── investigation.json
    ├── context.json
    ├── addresses/
    ├── graph/
    ├── prefetch/
    │   ├── candidates.json
    │   ├── queue.json
    │   └── history.ndjson
    └── summaries/
```

---

# 39. Context Snapshot

保存：

```json
{
  "chain_id": 56,
  "focus_address": "0x...",
  "time_range": {
    "from": "...",
    "to": "..."
  },
  "tokens": ["USDT"],
  "goal": "追踪资金沉淀",
  "current_path": [
    "0xA",
    "0xB"
  ]
}
```

---

# 40. Prefetch Candidate Store

```json
{
  "address": "0xC",
  "score": 87.2,
  "priority": "HOT",
  "reason": [
    "top_outflow_counterparty",
    "path_next_hop",
    "high_value"
  ]
}
```

---

# 41. Graph Cache TTL

历史闭合区间：

```text
长期缓存
```

最近活跃区间：

```text
短 TTL
```

例如：

```text
历史图：
7d / 30d

近实时图：
5min / 15min
```

---

# 42. Graph Cache 失效

以下触发失效：

```text
Dataset Coverage 更新
新增 Token Transfer
Balance Refresh
Schema / Aggregation Version 变化
用户改变筛选条件
```

---

# 43. Incremental Graph Rebuild

Dataset 增量：

```text
50M → 52M
```

不需要重算：

```text
40M → 52M
```

只聚合：

```text
50M → 52M
```

然后和旧图聚合结果 merge。

---

# 44. Graph Edge Cache

可以单独缓存：

```text
Address → Counterparty Aggregate
```

字段：

```text
counterparty
tx_count
inflow
outflow
token
first_seen
last_seen
```

关系图秒开主要依赖这层。

---

# 45. 地址画像缓存

Address Profile 也建议纳入 Investigation Cache。

缓存：

```text
tx_count
token_transfer_count
counterparties
balance
first_seen
last_seen
inflow
outflow
risk flags
```

---

# 46. 页面联动

用户从结果页点击：

```text
关系图
```

传：

```text
address
chain
dataset
range
certification
```

Graph 页面：

```text
先查 Graph Expansion Cache
↓
HIT → 秒开
↓
MISS → 本地 Dataset 聚合
↓
Dataset MISS → Smart Download
```

---

# 47. 图上展开

用户点击：

```text
展开下游
```

执行：

```text
1. Graph Cache Query
2. Dataset Registry Query
3. Active Task Query
4. Smart Download
```

优先级严格：

```text
Graph Cache
>
Local Dataset
>
Active Reuse
>
Network
```

---

# 48. 智能调查联动

Agent Planner 需要访问：

```text
Investigation Cache
```

先看：

```text
已有地址
已有 Coverage
已有图
已有标签
已有摘要
```

只有缺口才触发 Smart Download。

---

# 49. Agent 不直接调用 Provider

保持：

```text
Agent
↓
Smart Download Request
↓
Scheduler
```

Agent 不应该：

```text
自己决定 SQD / RPC
```

---

# 50. Agent Prefetch Recommendation

Agent 可以输出：

```text
建议提前准备：
0xB
0xC
0xD
```

但最终是否预取仍由：

```text
Prefetch Planner
```

根据成本/大小/优先级决定。

---

# 51. 前端增加 Prefetch 状态

不需要主界面大展示。

地址详情可显示：

```text
下一步数据准备

0xB    已缓存
0xC    预取中 68%
0xD    待处理
```

---

# 52. Graph 节点状态

图上节点可增加小状态：

```text
● 已有完整数据
◐ 部分数据
○ 未加载
↻ 后台准备中
```

但颜色不要过多。

Hover：

```text
本地覆盖 82%
```

---

# 53. 用户点击“未加载”节点

立即：

```text
Prefetch → Interactive Upgrade
```

该地址优先级从：

```text
P3
```

升级：

```text
P0
```

并继承已经下载的进度。

---

# 54. Prefetch Upgrade 示例

后台：

```text
C
Token Transfer
已预取 41%
```

用户点击 C：

```text
任务 ID 不变
Priority P3 → P0
继续 41%
```

绝不能重新创建任务。

---

# 55. Storage Policy

预取数据也必须经过：

```text
Validation
```

才能写入 Dataset Registry。

不能因为是缓存数据就降低完整性要求。

---

# 56. 预取数据清理

如果磁盘压力高：

优先删除：

```text
unused prefetch raw
unused staging
```

不要优先删：

```text
Certified Final
```

---

# 57. Cache Eviction Score

建议：

```text
EvictionScore =
Age
+ UnusedPenalty
+ SizePenalty
- InvestigationImportance
- ReuseProbability
```

---

# 58. 磁盘阈值

例如：

```text
Disk > 80%
→ 暂停 WARM Prefetch

Disk > 90%
→ 暂停所有 Prefetch
→ 清理 unused temp

Disk > 95%
→ 禁止新的 Prefetch
```

---

# 59. Prefetch Scheduler

建议新增：

```text
internal/investigation/prefetch/
├── planner.go
├── candidate.go
├── scorer.go
├── queue.go
├── budget.go
├── feedback.go
├── upgrade.go
└── eviction.go
```

---

# 60. Graph Cache 模块

```text
internal/graphcache/
├── key.go
├── cache.go
├── builder.go
├── incremental.go
├── invalidation.go
└── store.go
```

---

# 61. Investigation Cache 模块

```text
internal/investigation/cache/
├── cache.go
├── context.go
├── address.go
├── summary.go
├── coverage.go
└── store.go
```

---

# 62. API

## Graph Expansion

```http
POST /api/graph/expand
```

内部自动：

```text
Graph Cache
→ Dataset Registry
→ Smart Download
```

---

# 63. Prefetch Status

```http
GET /api/investigations/{id}/prefetch
```

---

# 64. 手工 Pin

用户可以：

```text
固定准备该地址
```

例如：

```http
POST /api/investigations/{id}/prefetch/pin
```

Pin 地址进入：

```text
HOT
```

---

# 65. 不建议给普通用户“预取开关”

默认自动。

系统设置可配置：

```text
Smart Prefetch
ON/OFF
```

但日常操作页不需要每次选择。

---

# 66. P0 实施范围

必须：

```text
Investigation Cache V2
Graph Expansion Cache
Prefetch Candidate Generator
Prefetch Score
HOT/WARM 优先级
Coverage Index 联动
Low Priority Queue
Interactive Priority Upgrade
Prefetch Budget
```

---

# 67. P1

随后：

```text
Temporal Context Propagation
Progressive Prefetch
Incremental Graph Rebuild
Agent Prefetch Recommendation
Prefetch Feedback Loop
Cache Eviction
```

---

# 68. P2

进一步：

```text
多案件共享缓存
实体级预取
交易所标签驱动预取
路径概率模型
历史用户行为学习
```

---

# 69. Case A：图展开秒开

A 已加载。

系统预取 B。

用户点击 B。

要求：

```text
network request = 0
graph expansion < 2s
```

---

# 70. Case B：部分预取

B 已预取 40%。

用户点击 B。

要求：

```text
Priority:
P3 → P0

继续 40%
不从 0 重来
```

---

# 71. Case C：前台任务抢占

后台有：

```text
8 Prefetch Jobs
```

用户新建正式下载。

要求：

```text
Prefetch 安全暂停
Checkpoint 保存
释放 Worker
```

---

# 72. Case D：Coverage Hit

候选 C 已有本地完整数据。

要求：

```text
Prefetch network requests = 0
直接进入 Graph Cache build
```

---

# 73. Case E：重复候选

A 和 D 都指向 C。

要求：

```text
C 只预取一次
```

其他任务订阅。

---

# 74. Case F：预取无价值

连续大量预取未被使用。

要求：

```text
ReuseProbability 下降
自动降低预取数量
```

---

# 75. Case G：磁盘压力

磁盘达到：

```text
90%
```

要求：

```text
暂停 WARM
暂停 HOT 新任务
清理 unused temp
不影响 Interactive Task
```

---

# 76. Case H：Graph Cache 增量

本地新增：

```text
50M → 52M
```

要求：

```text
Graph Cache 只聚合增量
不全量重算
```

---

# 77. 关键指标

至少监控：

```text
Prefetch Hit Rate
Prefetch Wasted Bytes
Saved User Wait Time
Graph Cache Hit Rate
Dataset Local Hit Rate
Interactive Upgrade Count
Average Graph Expand Latency
```

---

# 78. 目标值

初期建议：

```text
Graph Cache Hit Rate > 50%
Prefetch Hit Rate > 30%
Local Dataset Hit Rate > 50%
常见图展开 < 2s
预取资源占比 < 15%
```

---

# 79. 对当前系统的直接收益

完成后：

```text
智能下载
```

不再只是一个下载器总入口。

它会成为：

```text
统一数据供应层
```

为：

```text
地址画像
关系图
智能调查
资金路径
风险分析
```

提供共享数据。

---

# 80. 下一个阶段之后的优化方向

完成 Prefetch / Graph Cache 后，下一个推荐方向：

```text
Entity Intelligence Layer V1
+
Address Label Resolution
+
Exchange / Contract / Service Entity Mapping
```

即：

```text
不只知道“这个地址和谁交易”
还要知道“这个地址是谁”
```

把：

```text
地址
```

逐步提升成：

```text
实体
```

为沉淀地址、交易所落地、身份落查提供基础。

---

# 81. 一句话目标

> **下一阶段的目标，是让智能调查和关系图从“按需下载”升级成“预测式数据准备”：系统根据当前资金路径自动提前准备下一跳地址，用户展开图和继续调查时优先命中本地缓存，实现秒级交互。**
