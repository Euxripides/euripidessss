# 下一阶段优化：Fund Flow Intelligence V2
## Path Scoring + Profit Attribution + Settlement Detection + Entity-Aware Fund Tracing

> 前置能力：
>
> - Smart Download 统一数据供应层
> - Dataset Registry Coverage Index V2
> - Cross-Task Reuse / Incremental Download
> - Investigation Data Cache V2
> - Graph Expansion Cache
> - Smart Prefetch
> - Entity Intelligence Layer V1
> - Address Label / Entity Mapping / Evidence Provenance
>
> 下一阶段目标：
>
> **把系统从“知道地址是谁、和谁交易”升级到“知道资金经过了哪些关键路径、谁真正获利、资金最后沉淀在哪里、哪条路径最值得继续追”。**
>
> 本阶段正式建设：
>
> ```text
> Fund Flow Intelligence V2
> +
> Path Scoring Engine V1
> +
> Profit Attribution Engine V1
> +
> Settlement Detection Engine V1
> +
> Entity-Aware Flow Graph V1
> ```
>
> 核心输出：
>
> ```text
> 关键资金路径
> 获利地址
> 净获利金额
> 资金沉淀地址
> 交易所落点
> 中转地址
> 归集地址
> 分发地址
> 路径可信度
> 路径证据
> ```

---

# 1. 核心问题

Entity Intelligence 解决：

```text
这个地址是谁？
```

Fund Flow Intelligence 要解决：

```text
这笔钱从哪里来？
经过谁？
最终到哪里？
谁真正获利？
谁只是中转？
哪里形成沉淀？
哪些路径最值得调查？
```

---

# 2. 资金流模型

最小单位：

```text
FlowEdge
```

建议：

```go
type FlowEdge struct {
    ChainID int64

    FromAddress string
    ToAddress   string

    FromEntityID string
    ToEntityID   string

    TxHash string
    BlockNumber uint64
    BlockTime   time.Time

    AssetAddress string
    Symbol       string

    RawValue        string
    NormalizedValue string
    USDValue        *decimal.Decimal

    EdgeType string

    Dataset string
    EvidenceIDs []string
}
```

---

# 3. Edge Type

建议统一：

```text
TRANSFER
TOKEN_TRANSFER
INTERNAL_TRANSFER
SWAP_IN
SWAP_OUT
BRIDGE_IN
BRIDGE_OUT
DEPOSIT
WITHDRAWAL
SWEEP
COLLECT
DISTRIBUTE
SETTLEMENT
FUNDING
REFUND
SELF_TRANSFER
UNKNOWN
```

---

# 4. Flow Session

为了追踪一笔资金，不建议只按单笔交易看。

增加：

```text
FlowSession
```

表示：

```text
一段连续资金活动
```

例如：

```go
type FlowSession struct {
    ID string

    RootAddress string
    ChainID int64

    FromTime time.Time
    ToTime   time.Time

    AssetScope []string

    EdgeIDs []string
    NodeIDs []string
}
```

---

# 5. 为什么要有 Flow Session

因为真实资金流经常是：

```text
A
↓
B
↓
B 分成 5 笔
↓
C / D / E / F / G
↓
又重新归集
```

只看单笔交易会丢失整体结构。

---

# 6. Entity-Aware Flow Graph

节点：

```text
Address Node
Entity Node
Cluster Node
```

支持折叠：

```text
20 个交易所 Deposit 地址
↓
折叠成
Exchange X
```

这样图不会爆炸。

---

# 7. Flow Direction

每个目标地址 / 实体需要：

```text
IN
OUT
SELF
```

并记录：

```text
Gross Inflow
Gross Outflow
Net Flow
```

---

# 8. Net Flow

```text
NetFlow =
TotalInflow
-
TotalOutflow
```

用于初步识别：

```text
沉淀
获利
分发
归集
```

---

# 9. Profit Attribution

本阶段最重要的新能力之一。

不能简单：

```text
收到的钱 = 获利
```

必须区分：

```text
本金
利润
返还
自转
中转
手续费
兑换
归集
```

---

# 10. Profit Attribution 基础模型

建议：

```text
Attributed Profit
=
Realized Inflow
-
Attributed Cost Basis
-
Returned Principal
-
Known Operational Transfer
```

---

# 11. Cost Basis

需要识别：

```text
初始投入
补仓
Gas
换币成本
项目投入
```

例如某地址：

```text
先投入 100K USDT
最终取回 800K USDT
```

不能把 800K 全算获利。

应：

```text
Profit ≈ 700K
```

---

# 12. Profit Attribution Levels

建议分：

```text
L0 Gross Profit
L1 Net Flow Profit
L2 Cost-Basis Adjusted Profit
L3 Entity-Adjusted Profit
```

---

# 13. L0 Gross Profit

最简单：

```text
累计流入 - 初始显著投入
```

只用于快速筛查。

---

# 14. L1 Net Flow Profit

考虑：

```text
总流入
总流出
余额
```

---

# 15. L2 Cost-Basis Adjusted

加入：

```text
本金
Swap
手续费
返还
```

更接近真实获利。

---

# 16. L3 Entity-Adjusted

进一步排除：

```text
自己控制地址之间的内部转移
同一 Entity 内归集
自有钱包迁移
```

这是最接近“真正获利”的层级。

---

# 17. Self-Controlled Flow

如果：

```text
Address A
↓
Address B
```

且 A/B 高可信属于同一 Entity：

```text
不应把 B 收到的钱算成新的外部收益
```

这种属于：

```text
INTERNAL_ENTITY_TRANSFER
```

---

# 18. Profit Evidence

每一笔利润归因必须能解释：

```text
来源交易
成本基础
归因规则
排除规则
```

生成：

```text
profit-attribution.json
```

---

# 19. Profit Attribution Result

```go
type ProfitAttributionResult struct {
    Address string
    EntityID string

    GrossInflow string
    GrossOutflow string

    CostBasis string
    ReturnedPrincipal string

    NetProfit string

    Confidence float64

    EvidenceIDs []string
}
```

---

# 20. Settlement Detection

定义：

```text
资金经过多跳后，在某地址或实体长期停留
```

就是：

```text
Settlement / Dormancy
```

---

# 21. Settlement Score

建议：

```text
SettlementScore =
  0.30 * NetRetentionScore
+ 0.20 * HoldingDurationScore
+ 0.15 * LowOutflowScore
+ 0.15 * InactivityScore
+ 0.10 * PathTerminalityScore
+ 0.10 * EntityRelevanceScore
```

---

# 22. Net Retention

```text
Retention Ratio =
Current / Historical Inflow
```

例如：

```text
累计流入 1M
累计流出 100K
余额 900K
```

高沉淀。

---

# 23. Holding Duration

计算：

```text
资金首次进入
→
当前 / 主要流出时间
```

时间越长：

```text
SettlementScore 越高
```

---

# 24. Path Terminality

如果地址：

```text
大量资金进入
但很少继续向外
```

说明接近：

```text
路径终点
```

---

# 25. Settlement Type

建议：

```text
DORMANT_WALLET
COLD_STORAGE
TREASURY
EXCHANGE_DEPOSIT
CUSTODIAL_SETTLEMENT
UNKNOWN_SETTLEMENT
```

---

# 26. Cashout Detection

如果路径：

```text
案件地址
↓
中转
↓
交易所 Deposit
```

则生成：

```text
Cashout Candidate
```

---

# 27. Cashout Result

```go
type CashoutResult struct {
    SourceAddress string
    DestinationAddress string

    EntityID string

    TxHash string
    Timestamp time.Time

    Token string
    Amount string

    Confidence float64

    EvidenceIDs []string
}
```

---

# 28. Path Scoring

不能把所有路径都展示给用户。

必须给每条路径评分。

---

# 29. Path Score

建议：

```text
PathScore =
  0.25 * ValueScore
+ 0.20 * ProfitRelevance
+ 0.15 * SettlementLikelihood
+ 0.15 * EntityRelevance
+ 0.10 * TemporalContinuity
+ 0.10 * PathConfidence
+ 0.05 * NoveltyScore
- NoisePenalty
```

---

# 30. ValueScore

不是只看绝对金额。

要考虑：

```text
相对于根地址总流量的占比
```

例如：

```text
100K
```

如果根地址只流出 120K：

```text
很重要
```

如果根地址流出 100M：

```text
相对不重要
```

---

# 31. Temporal Continuity

资金路径时间间隔：

```text
A → B
5 min
B → C
3 min
```

比：

```text
A → B
B 半年后 → C
```

更可能属于同一资金链。

---

# 32. Path Confidence

由：

```text
Edge Certification
Entity Confidence
Coverage
Provider Reliability
```

共同决定。

---

# 33. Noise Penalty

降低：

```text
DEX Router
热门合约
公共资金池
高频服务节点
Gas Funding
Dust
```

这类噪声边对路径的影响。

---

# 34. Path Expansion Policy

建议：

```text
Top K
+
Threshold
+
Budget
```

不是无限 BFS。

例如：

```text
每层保留 Top 10 高分路径
最大深度 6
最大节点 500
```

---

# 35. Adaptive Depth

如果路径进入：

```text
交易所
沉淀地址
已知终点
```

可以提前停止。

如果进入：

```text
中转地址
Collector
Bridge
```

继续展开。

---

# 36. Terminal Node

以下可作为路径终点候选：

```text
Exchange Deposit
Cold Wallet
Settlement Wallet
Dormant Address
Custodian
Known Service
```

---

# 37. Bridge Handling

跨链路径必须生成：

```text
Bridge Exit Candidate
```

例如：

```text
BSC Address
↓
Bridge Contract
↓
Ethereum Address
```

后续交给：

```text
Cross-Chain Continuation
```

---

# 38. Path Type

建议：

```text
DIRECT_CASHOUT
MULTI_HOP_CASHOUT
COLLECT_AND_SETTLE
DISTRIBUTION
LAYERING
BRIDGE_EXIT
RETURN_FLOW
SELF_CONTROLLED_TRANSFER
UNKNOWN
```

---

# 39. Layering Detection

如果资金：

```text
多次拆分
多次归集
快速跳转
金额高度相似
```

可以标记：

```text
LAYERING_PATTERN
```

但只能作为行为模式，不自动推断违法目的。

---

# 40. Return Flow

识别：

```text
A → B → C → A
```

或同 Entity 回流。

用于排除：

```text
伪获利
```

---

# 41. Round Trip Detection

```text
RoundTripScore
```

因素：

```text
回流比例
时间间隔
资产一致性
实体一致性
```

---

# 42. Flow Conservation

每个节点尝试：

```text
Inflow
≈
Outflow
+
Balance Change
+
Fees
+
Swap Difference
```

用于检测：

```text
漏数据
异常归因
```

---

# 43. Flow Conservation 与 Validation 联动

如果：

```text
资金守恒偏差过大
```

触发：

```text
Gap Repair / Revalidation
```

说明可能存在：

```text
Internal Tx 漏失
Token Transfer 漏失
Swap 未解析
```

---

# 44. Multi-Asset Flow

不能只追 USDT。

同一路径可能：

```text
USDT
↓ Swap
BNB
↓ Bridge
ETH
↓ Exchange
```

因此需要：

```text
Asset Transformation
```

---

# 45. Asset Conversion Event

```go
type AssetConversion struct {
    TxHash string

    FromAsset string
    FromAmount string

    ToAsset string
    ToAmount string

    USDValue *decimal.Decimal
}
```

---

# 46. Unified Value

路径评分建议尽量转换为：

```text
USD Equivalent
```

但必须保留：

```text
原始 Token / Amount
价格来源 / 时间
```

---

# 47. Price Provenance

价格换算也需要 Evidence：

```text
price_source
price_timestamp
price_method
```

避免错误估值。

---

# 48. Investigation Goal Awareness

不同调查目标：

```text
找沉淀
找交易所落点
找获利地址
找归集地址
找资金来源
```

Path Score 权重不同。

---

# 49. Goal Profile

例如：

```text
goal = settlement
```

提高：

```text
SettlementLikelihood
```

goal：

```text
cashout
```

提高：

```text
EntityRelevance
ExchangeDeposit
```

---

# 50. Fund Flow Planner

智能调查输入：

```text
调查目标
```

传给：

```text
FundFlowPlanner
```

生成：

```text
PathScoringProfile
```

---

# 51. 前端：资金路径页

建议：

```text
调查工作台
└── 资金路径
```

顶部：

```text
目标地址
调查目标
Token
时间范围
最大深度
```

---

# 52. 路径摘要

显示：

```text
发现关键路径      12
高价值终点         4
交易所落点         2
沉淀地址           3
潜在获利地址       5
```

---

# 53. Path Cards

每条关键路径：

```text
获利地址 A
↓ 1.2M USDT
中转 B
↓ 1.18M USDT
Exchange X Deposit
```

显示：

```text
路径金额
时间
跳数
路径评分
可信度
终点类型
```

---

# 54. 一键定位关系图

点击：

```text
在关系图中查看
```

自动：

```text
只高亮这条路径
```

---

# 55. 结果表：获利地址

建议：

| 地址/实体 | 净获利 | 累计流入 | 累计流出 | 当前沉淀 | 可信度 |
|---|---:|---:|---:|---:|---|
| 0xAAA | 1.2M USDT | 2.1M | 0.9M | 1.1M | 高 |
| Entity B | 820K USDT | 1.6M | 0.78M | 0.7M | 中 |

---

# 56. 结果表：沉淀地址

字段：

```text
Address / Entity
Retained Value
Holding Duration
Last Outflow
Settlement Score
Entity Type
Evidence
```

---

# 57. 结果表：交易所落点

字段：

```text
Exchange
Deposit Address
Amount
Time
Tx Hash
Source Path
Confidence
Evidence
```

---

# 58. Evidence Drawer

点击任意：

```text
获利
沉淀
交易所落点
```

必须能展开：

```text
为什么这么判断？
```

---

# 59. Fund Flow Cache

新增：

```text
fund-flow-cache/
```

缓存：

```text
Path
Profit Attribution
Settlement
Cashout
```

避免重复计算。

---

# 60. Cache Key

```text
root
chain
token_scope
range
goal
depth
scoring_version
```

---

# 61. Incremental Flow Rebuild

如果 Dataset 只新增：

```text
最后 1 天
```

只增量更新：

```text
新边
受影响节点
受影响路径
```

不要全图重算。

---

# 62. 后端目录建议

```text
internal/fundflow/
├── model/
├── graph/
├── session/
├── path/
│   ├── finder.go
│   ├── scorer.go
│   └── terminal.go
├── profit/
│   ├── attribution.go
│   ├── cost_basis.go
│   └── roundtrip.go
├── settlement/
│   ├── detector.go
│   ├── scorer.go
│   └── cashout.go
├── conservation/
├── asset/
├── cache/
└── api/
```

---

# 63. P0 实施范围

必须：

```text
Entity-Aware Flow Graph
Path Finder
Path Scoring
Net Flow
Settlement Score
Cashout Candidate
Profit Attribution L0/L1
Evidence
Frontend Path Cards
```

---

# 64. P1

随后：

```text
Cost Basis
Entity-Adjusted Profit
Round Trip
Flow Conservation
Bridge Continuation
Multi-Asset Conversion
Incremental Flow Cache
```

---

# 65. P2

高级：

```text
Goal-Aware Path Scoring
Cross-Chain Fund Flow
Path Explanation
Automatic Report Generation
Investigation Agent Integration
```

---

# 66. Case A：直接交易所落点

```text
A
↓
Exchange Deposit
```

要求：

```text
DIRECT_CASHOUT
```

并输出：

```text
Exchange
Deposit
Amount
Time
Evidence
```

---

# 67. Case B：多跳交易所落点

```text
A → B → C → Exchange Deposit
```

要求：

```text
MULTI_HOP_CASHOUT
```

---

# 68. Case C：沉淀

```text
A → B
B 长期保留 90%
```

要求：

```text
Settlement Candidate
```

---

# 69. Case D：自有钱包转移

```text
A → B
```

A/B 同 Entity。

要求：

```text
不重复计算为获利
```

---

# 70. Case E：回流

```text
A → B → C → A
```

要求：

```text
Round Trip
```

并降低：

```text
Profit Attribution
```

---

# 71. Case F：拆分与归集

```text
A
→ B/C/D/E
→ F
```

要求识别：

```text
Distribution
+
Collect
```

---

# 72. Case G：跨链

```text
BSC A
↓
Bridge
↓
ETH B
```

要求生成：

```text
Bridge Exit Candidate
```

---

# 73. Case H：守恒异常

某节点：

```text
Inflow 1M
Outflow 300K
Balance 10K
```

差异巨大。

要求：

```text
触发 Revalidation
```

---

# 74. 关键指标

至少：

```text
High-Value Path Count
Settlement Candidate Count
Cashout Candidate Count
Profit Attribution Coverage
Flow Conservation Pass Rate
Path Cache Hit Rate
Entity-Aware Collapse Ratio
```

---

# 75. 目标

初期：

```text
关键路径生成 < 2s（缓存命中）
大图路径分析 < 10s
所有关键路径带 Confidence
所有利润归因带 Evidence
所有沉淀结论带指标
```

---

# 76. 与智能调查联动

Agent 不自己重新算路径。

调用：

```text
Fund Flow Intelligence
```

获得：

```text
Top Paths
Profit
Settlement
Cashout
```

再生成调查结论。

---

# 77. 与关系图联动

关系图直接展示：

```text
Path Score
Entity
Settlement
Cashout
Profit
```

用户可以切换：

```text
普通图
关键路径图
实体图
获利图
沉淀图
```

---

# 78. 下一阶段之后

完成 Fund Flow Intelligence V2 后，下一步推荐：

```text
Investigation Report Engine V2
+
Evidence Timeline
+
Case Narrative
+
Exportable Case Package
```

也就是把：

```text
数据
图
路径
实体
利润
沉淀
```

自动整理成：

```text
完整调查报告
```

并且每一个结论都可追溯到：

```text
Tx
Address
Dataset
Evidence
```

---

# 79. 一句话目标

> **下一阶段要让系统从“会画资金图”升级成“会判断哪些资金路径真正重要”：自动识别获利、沉淀、交易所落点、中转、归集和回流，并给每条路径评分、置信度和证据。**
