# 下一阶段优化：Entity Intelligence Layer V1
## Address Label Resolution + Entity Mapping + Cluster Intelligence + Evidence Provenance

> 前置阶段：
>
> - Smart Download 统一下载入口
> - Dataset Registry Coverage Index V2
> - Cross-Task Reuse / Incremental Download
> - Investigation Data Cache V2
> - Graph Expansion Cache
> - Smart Prefetch
>
> 下一阶段目标：
>
> **把系统从“地址级分析”升级到“实体级分析”。**
>
> 当前系统已经能回答：
>
> ```text
> 这个地址和谁发生过交易？
> 资金从哪里来、去了哪里？
> 下一跳是什么？
> ```
>
> 下一阶段要继续回答：
>
> ```text
> 这个地址属于什么类型？
> 是否属于交易所 / 跨链桥 / DEX / 项目方 / 归集钱包？
> 多个地址是否属于同一实体？
> 这个标签来自哪里？
> 可信度多高？
> 有什么证据？
> 哪些信息可以用于进一步调证？
> ```
>
> 本阶段正式建设：
>
> ```text
> Entity Intelligence Layer V1
> +
> Address Label Resolver V2
> +
> Entity Cluster Engine V1
> +
> Evidence Provenance V1
> +
> Entity Graph Overlay V1
> ```

---

# 1. 核心原则

本阶段必须坚持：

```text
地址 != 实体
标签 != 事实
推断 != 证明
```

任何实体识别都必须包含：

```text
Label
Confidence
Evidence
Source
UpdatedAt
```

禁止只返回：

```text
Binance
OKX
项目方
庄家
```

而不给证据来源和可信等级。

---

# 2. 最终数据模型

系统从：

```text
Address
```

升级为：

```text
Address
↓
Address Profile
↓
Labels
↓
Cluster
↓
Entity
↓
Evidence
```

例如：

```text
0xAAA
├─ Label: Exchange Deposit
├─ Entity: Exchange X
├─ Cluster: cluster_00128
├─ Confidence: 0.97
└─ Evidence:
   ├─ Public label source
   ├─ Deposit pattern
   ├─ Sweep destination
   └─ Historical clustering
```

---

# 3. Entity Type

建议统一：

```text
EXCHANGE
DEX
BRIDGE
CEX_DEPOSIT
CEX_HOT_WALLET
CEX_COLD_WALLET
PAYMENT_SERVICE
CUSTODIAN
MARKET_MAKER
PROJECT_TREASURY
PROJECT_DEPLOYER
CONTRACT
TOKEN_CONTRACT
ROUTER
MULTISIG
RELAYER
BOT
MEV
MINER_VALIDATOR
MIXER
SCAM
PHISHING
EXPLOIT
UNKNOWN_SERVICE
UNKNOWN_ENTITY
INDIVIDUAL_UNKNOWN
```

注意：

```text
INDIVIDUAL_UNKNOWN
```

只表示：

```text
更像独立 EOA
```

不能擅自推断现实身份。

---

# 4. Label Type

一个地址可以拥有多个 Label：

```text
exchange
deposit
hot_wallet
cold_wallet
treasury
deployer
router
bridge
contract
bot
whale
collector
distributor
settlement
funding_source
cashout_candidate
high_risk
```

---

# 5. Label Source

必须标准化来源：

```text
PUBLIC_LABEL
EXPLORER_LABEL
PROJECT_OFFICIAL
CONTRACT_METADATA
ONCHAIN_PATTERN
CLUSTER_INFERENCE
USER_MANUAL
CASE_EVIDENCE
EXTERNAL_DATASET
LEGAL_RESPONSE
```

---

# 6. 可信度等级

建议：

```text
CONFIRMED      >= 0.95
HIGH           >= 0.80
MEDIUM         >= 0.60
LOW            >= 0.40
UNVERIFIED     <  0.40
```

前端不要只显示小数。

显示：

```text
已确认
高可信
中等可信
低可信
未验证
```

---

# 7. Provenance 是本阶段最重要的部分

任何 Label 都要带：

```go
type EvidenceRef struct {
    EvidenceID string
    SourceType string
    SourceName string
    SourceURI  string

    Observation string

    CollectedAt time.Time
    ValidFrom   *time.Time
    ValidTo     *time.Time

    Confidence float64
}
```

---

# 8. AddressLabel

```go
type AddressLabel struct {
    ChainID int64
    Address string

    Label string
    EntityID string

    Confidence float64

    EvidenceIDs []string

    ResolverVersion string

    CreatedAt time.Time
    UpdatedAt time.Time
}
```

---

# 9. Entity

```go
type Entity struct {
    ID string

    Name string
    EntityType string

    ChainIDs []int64
    Addresses []string

    Confidence float64

    EvidenceIDs []string

    CreatedAt time.Time
    UpdatedAt time.Time
}
```

---

# 10. Entity 不等于单地址

一个交易所可能有：

```text
Deposit Address
Hot Wallet
Cold Wallet
Sweep Wallet
Settlement Wallet
```

多个地址。

因此：

```text
Entity
└── Addresses
```

是多对一。

---

# 11. Entity Cluster

聚类用于表示：

```text
多个地址可能由同一控制主体或同一服务系统管理
```

但 Cluster 不一定立即等同真实 Entity。

```go
type AddressCluster struct {
    ID string

    Addresses []string

    ClusterType string

    Confidence float64

    EvidenceIDs []string
}
```

---

# 12. Cluster Type

例如：

```text
COMMON_SWEEP
COMMON_FUNDER
DEPOSIT_CLUSTER
HOT_WALLET_CLUSTER
PROJECT_CLUSTER
BOT_CLUSTER
CONTRACT_FACTORY_CLUSTER
```

---

# 13. 聚类不能只靠 Common Funder

例如：

```text
地址 A/B/C 都由 Binance 热钱包打过 gas
```

并不代表 A/B/C 属于同一实体。

因此任何 clustering heuristic 都必须有：

```text
weight
false_positive_risk
minimum_evidence_count
```

---

# 14. 建议的聚类信号

可以综合：

```text
共同 Sweep 地址
共同归集时间模式
共同 Funding 地址
相同 Gas Funding 模式
相同 Contract Factory
相同 Deploy Pattern
相同 Token 分发路径
相同提现路径
相同交易时间节奏
相同 Counterparty 集合
相同资金分层模式
```

---

# 15. 高可信聚类信号

更值得使用：

```text
多个 Deposit 地址最终稳定 Sweep 到同一 Hot Wallet
```

或者：

```text
官方公开地址 + 链上稳定归集关系
```

---

# 16. 低可信聚类信号

只能作为辅助：

```text
同一时间活跃
金额相近
都与同一热门合约交互
```

不能单独形成高可信实体归属。

---

# 17. Entity Resolution Pipeline

建议：

```text
Address
↓
Static Label Lookup
↓
Contract Metadata
↓
Known Entity Mapping
↓
Behavior Feature Extraction
↓
Cluster Candidate
↓
Evidence Aggregation
↓
Confidence Scoring
↓
Entity Resolution
↓
Human Review if needed
```

---

# 18. Static Label Resolver

优先解析：

```text
系统已有标签
公开标签
官方项目地址
区块浏览器标签
合约元数据
已确认案件标签
```

命中即可快速返回。

---

# 19. Contract Resolver

对于合约地址：

```text
bytecode
creator
creation tx
proxy
implementation
verified source
method signatures
token metadata
```

解析：

```text
Router
Pool
Bridge
Token
Multisig
Factory
Proxy
Treasury
```

---

# 20. Service Pattern Resolver

针对交易所等服务地址识别：

```text
大量唯一入金地址
↓
周期性归集
↓
少数 Hot Wallet
↓
统一提现模式
```

输出：

```text
CEX_DEPOSIT
CEX_HOT_WALLET
```

但必须结合已知实体证据才能标记具体服务名称。

---

# 21. Deposit Address Pattern

例如：

```text
Address X
入金来源数量少
几乎不主动与 DeFi 交互
收到资产后短时间 Sweep
Sweep Destination 稳定
余额长期低
```

可输出：

```text
label = exchange_deposit_candidate
confidence = medium/high
```

但不能仅靠行为就直接标记：

```text
Binance
```

除非 Sweep Destination 已经有 Binance 高可信标签。

---

# 22. Hot Wallet Pattern

典型：

```text
高交易频率
大量入金归集
大量出金
与大量 Deposit 地址连接
余额相对高
24h 活跃
```

---

# 23. Treasury Pattern

典型：

```text
与项目 Deploy / Multisig 有强关联
大额长期持仓
资金流向项目运营地址
与 Token Contract / Vesting 关系密切
```

---

# 24. Collector / Settlement 地址

资金调查中非常重要：

```text
大量来源
少数去向
短持有时间
周期性清空
```

可以识别：

```text
Collector
Settlement
Aggregator
```

---

# 25. 沉淀地址识别

这是实际调查非常重要的实体特征。

建议增加：

```text
DormancyScore
```

因素：

```text
最后一次出金时间
当前余额
累计流入
累计流出
净沉淀
地址生命周期
资金停留时间
```

---

# 26. Dormancy / Settlement Score

例如：

```text
NetRetainedValue
+
HoldingDuration
+
LowOutflowFrequency
+
RecentInactivity
```

输出：

```text
资金沉淀候选
```

---

# 27. Cashout Candidate

识别：

```text
资金从案件路径
↓
进入交易所 / 支付服务 / 已知托管服务
```

可以标记：

```text
CASHOUT_CANDIDATE
```

并附：

```text
Target Entity
Tx Hash
Time
Token
Amount
Deposit Address
Sweep Address
```

用于后续合规或依法调证工作流。

---

# 28. 调证信息结构化

对于可落地实体：

```go
type InvestigationLead struct {
    Address string
    EntityID string

    LeadType string

    TransactionHash string
    BlockNumber uint64
    Timestamp time.Time

    Token string
    Amount string

    EvidenceIDs []string

    Confidence float64
}
```

---

# 29. Lead Type

```text
EXCHANGE_DEPOSIT
PAYMENT_SERVICE
CUSTODIAN
BRIDGE_EXIT
PROJECT_CONTROLLED
KNOWN_SERVICE
```

---

# 30. 不自动生成现实个人身份结论

系统应区分：

```text
Entity Attribution
```

和：

```text
Real-world Person Identity
```

只有有明确、合法来源的证据时才可展示后者。

否则：

```text
Unknown Individual / Unknown Controller
```

---

# 31. Entity Intelligence Cache

新增：

```text
entity-intelligence/
├── entities/
├── labels/
├── clusters/
├── evidence/
├── leads/
└── indexes/
```

---

# 32. 文件系统设计

项目不引入数据库时：

```text
entity-intelligence/
├── entities/
│   ├── exchange/
│   ├── contract/
│   └── unknown/
├── addresses/
│   ├── 00/
│   ├── 01/
│   └── ff/
├── evidence/
├── clusters/
└── events.ndjson
```

地址继续按 hash 分片。

---

# 33. Address Intelligence Entry

```json
{
  "chain_id": 56,
  "address": "0x...",
  "labels": [
    {
      "label": "exchange_deposit",
      "confidence": 0.96,
      "entity_id": "entity_x"
    }
  ],
  "cluster_ids": [
    "cluster_123"
  ],
  "updated_at": "..."
}
```

---

# 34. Entity Index

需要：

```text
address → entity
entity → addresses
label → addresses
cluster → addresses
```

---

# 35. Graph Overlay

关系图节点以后不只显示：

```text
0x1234...
```

而是：

```text
Exchange X
Deposit
0x1234…abcd
```

或者：

```text
Unknown Collector
0x1234…abcd
```

---

# 36. Node Card

Hover / Drawer：

```text
实体：Exchange X
类型：CEX Deposit
可信度：高
地址：0x...
当前余额：...
累计流入：...
累计流出：...
First Seen：...
Last Seen：...
证据：4
```

---

# 37. Edge Intelligence

边也要增加语义。

例如：

```text
Deposit
Sweep
Withdrawal
Bridge
Swap
Transfer
Funding
```

而不是只有：

```text
USDT 100,000
```

---

# 38. Edge Classification

规则：

```text
普通转账
入金
归集
提现
跨链
Swap
合约调用
资金分发
资金回流
```

---

# 39. Graph Cluster View

多个属于同一 Entity 的地址可以折叠：

```text
20 Deposit Addresses
↓
[Exchange X]
```

避免图爆炸。

---

# 40. Expand Entity

用户点击：

```text
展开实体
```

再显示：

```text
Deposit
Hot Wallet
Cold Wallet
Other
```

---

# 41. Investigation UI

智能调查页面增加：

```text
实体线索
```

例如：

```text
Exchange X
高可信
涉及 3 个地址
累计流入 4.2M USDT
最近交互 2026-08-07
```

---

# 42. Evidence Drawer

点击实体：

```text
为什么识别为 Exchange X？
```

展示：

```text
1. 官方公开地址
2. Deposit 地址稳定归集至已确认 Hot Wallet
3. 过去 90 天 98.7% 出金进入该 Cluster
4. 公开标签数据一致
```

---

# 43. Label Conflict

如果不同来源冲突：

```text
Source A → Exchange X
Source B → Exchange Y
```

不能直接覆盖。

进入：

```text
CONFLICT
```

---

# 44. Conflict Resolution

规则：

```text
官方来源
>
高可信人工确认
>
多个独立公开来源
>
单一公开标签
>
行为推断
```

---

# 45. Manual Label

允许用户在案件里添加：

```text
案件自定义标签
```

例如：

```text
盘古主出金地址
可疑归集
核心获利地址
```

必须和全局公共 Entity 分离。

---

# 46. Scope

Label Scope：

```text
GLOBAL
INVESTIGATION
SESSION
```

案件标签默认：

```text
INVESTIGATION
```

---

# 47. Entity Confidence

建议：

```text
EntityConfidence =
  SourceAuthorityScore
+ OnchainConsistencyScore
+ ClusterEvidenceScore
+ TemporalConsistencyScore
+ IndependentEvidenceScore
- ConflictPenalty
```

---

# 48. 不使用单一行为规则给出“已确认”

例如：

```text
Sweep Pattern
```

只能：

```text
推测某服务 Deposit
```

不能：

```text
已确认 Binance
```

---

# 49. Historical Validity

地址标签可能随时间变化。

例如：

```text
地址以前属于项目
后来转移控制
```

因此 Evidence 支持：

```text
valid_from
valid_to
```

---

# 50. Entity Versioning

Entity Mapping 也需要版本：

```text
entity_version
resolver_version
```

历史案件要能重现当时使用的识别结果。

---

# 51. Data Sources

Entity Layer 可接：

```text
公开项目列表
区块浏览器标签
官方合约地址
系统人工标签
历史案件确认
链上模式推断
外部标签数据集
```

---

# 52. 数据源优先级

建议：

```text
Official
Verified
Multiple Independent
Public Label
Behavioral Inference
User Heuristic
```

---

# 53. Entity Resolver API

```http
GET /api/entity/resolve?chain=bsc&address=0x...
```

返回：

```json
{
  "entity": {
    "id": "entity_x",
    "name": "Exchange X",
    "type": "EXCHANGE"
  },
  "labels": [
    "CEX_DEPOSIT"
  ],
  "confidence": 0.97,
  "evidence_count": 4
}
```

---

# 54. Batch Resolve

批量：

```http
POST /api/entity/resolve/batch
```

支持：

```text
10K / 100K 地址
```

不能逐地址 HTTP。

---

# 55. Entity Graph API

```http
GET /api/entity/{entity_id}/graph
```

返回：

```text
实体内部地址
实体外部交互
资金流
```

---

# 56. Investigation Leads API

```http
GET /api/investigations/{id}/entity-leads
```

返回：

```text
交易所入金
托管服务
沉淀地址
桥出口
```

---

# 57. Prefetch 与 Entity 联动

Smart Prefetch 下一阶段不只按地址评分。

还可以：

```text
如果某节点命中交易所 Deposit
↓
优先准备 Sweep / Hot Wallet 关联信息
```

---

# 58. Coverage Index 与 Entity 联动

Entity Resolver 不重新下载数据。

优先使用：

```text
Dataset Registry
```

需要更多链上特征时：

```text
Smart Download
```

---

# 59. Address Profile Feature Store

建议抽一层：

```text
Feature Store
```

存：

```text
tx_count
counterparty_count
inflow
outflow
balance
sweep_ratio
holding_time
activity_hours
contract_ratio
token_diversity
```

供：

```text
Entity Resolver
Risk Engine
Graph
Investigation Agent
```

复用。

---

# 60. Feature Store 不能重复计算

如果地址画像已经算过：

```text
直接复用
```

Coverage 更新后：

```text
增量更新 Feature
```

---

# 61. P0 实施范围

必须：

```text
Address Label Resolver
Entity Model
Evidence Provenance
Confidence
Known Entity Mapping
Contract Resolver
Address → Entity Index
Graph Node Label Overlay
Investigation Entity Leads
```

---

# 62. P1

随后：

```text
Entity Cluster Engine
Deposit / Sweep Pattern
Collector / Settlement Pattern
Dormancy Score
Cashout Candidate
Conflict Resolution
Entity Graph Collapse
```

---

# 63. P2

高级能力：

```text
Entity-level Flow Graph
Temporal Entity Ownership
Cross-chain Entity Mapping
Advanced Behavioral Clustering
Historical Entity Versioning
```

---

# 64. Case A：已知交易所 Deposit

输入地址：

```text
0xAAA
```

已有高可信公开实体标签。

要求：

```text
Entity Resolve < 100ms（缓存命中）
Entity = Exchange X
Type = CEX_DEPOSIT
Evidence 可展开
```

---

# 65. Case B：未知 Deposit 地址

地址本身没有公开标签。

链上表现：

```text
收到资金
↓
快速 Sweep
↓
目标为已知 Exchange Hot Wallet
```

要求：

```text
CEX_DEPOSIT_CANDIDATE
Confidence = HIGH / MEDIUM
```

但名称只有在证据足够时绑定具体 Exchange。

---

# 66. Case C：冲突标签

两个来源分别标：

```text
Exchange X
Exchange Y
```

要求：

```text
CONFLICT
```

不得静默覆盖。

---

# 67. Case D：Cluster

20 个地址稳定 Sweep 到同一 Hot Wallet。

要求：

```text
形成 Deposit Cluster
```

关系图默认可折叠。

---

# 68. Case E：沉淀地址

地址：

```text
累计流入巨大
近期无出金
余额长期保留
```

要求：

```text
Dormancy / Settlement Candidate
```

并输出证据指标。

---

# 69. Case F：案件自定义标签

用户添加：

```text
核心获利地址
```

只在当前 Investigation 显示。

不能污染全局 Entity Label。

---

# 70. Case G：Graph

原：

```text
0xAAA → 0xBBB → 0xCCC
```

升级：

```text
获利地址
↓
Exchange X Deposit
↓
Exchange X Hot Wallet
```

---

# 71. 关键指标

至少监控：

```text
Entity Resolve Hit Rate
Known Label Hit Rate
Conflict Rate
Cluster Precision Review Rate
Evidence Coverage
Entity Cache Hit Rate
Investigation Lead Count
```

---

# 72. 目标

初期：

```text
Known Service Label Resolve < 200ms
缓存命中 < 50ms
所有实体标签 100% 带 Provenance
所有推断标签 100% 带 Confidence
Conflict 不静默覆盖
```

---

# 73. 对现有系统的价值

完成后：

```text
智能下载
```

负责：

```text
数据供应
```

```text
Investigation Cache
```

负责：

```text
调查上下文
```

```text
Entity Intelligence
```

负责：

```text
解释地址是谁、属于什么服务、多个地址是否属于同一实体
```

三层开始真正形成调查平台。

---

# 74. 下一个优化方向

Entity Intelligence 完成后，下一步推荐：

```text
Fund Flow Intelligence V2
+
Path Scoring
+
Profit Attribution
+
Settlement Detection
```

也就是从：

```text
“这个地址是谁”
```

进一步升级到：

```text
“这笔钱经过哪些关键实体？”
“最终沉淀在哪里？”
“哪个地址真正获利？”
“哪些路径最值得调查？”
```

---

# 75. 一句话目标

> **下一阶段把系统从“地址级资金追踪”升级为“实体级资金追踪”：每个标签都必须可解释、可追溯、有置信度，关系图可以从地址图升级成实体图，并自动提取交易所入金、归集、沉淀和其他高价值调查线索。**
