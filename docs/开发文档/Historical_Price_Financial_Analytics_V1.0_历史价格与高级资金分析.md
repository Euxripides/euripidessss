# Historical Price & Financial Analytics V1.0
## 历史价格、USD 估值、PnL、资金沉淀与高级资金分析层

> 前置条件：
>
> - ClickHouse Data Plane 已完成
> - Canonical Data Asset Layer 已建立/实施
> - Entity & Metadata Intelligence 已建立/实施
> - Explorer / Analytics / Graph / Investigation / Export 均以 ClickHouse 为在线事实源
> - ClickHouse 固定根目录：`E:\database\clickhouse`
>
> 本阶段目标：
>
> **让系统从“知道发生了多少 Token 转账”，升级为“知道这些资金在当时值多少钱、来自哪里、流向哪里、是否沉淀、是否快速中转、是否实现收益”。**

---

# 1. 本阶段核心价值

没有历史价格时，系统只能回答：

```text
A 收到 100,000 TOKEN
```

有历史价格后才能回答：

```text
A 在 2025-08-01 收到 100,000 TOKEN
当时价值 $283,421
```

进一步才能计算：

```text
总流入 USD
总流出 USD
净流量
历史资产价值
PnL
资金沉淀
大额交易
CEX 入金
DEX 成交额
Bridge 跨链流量
```

---

# 2. 最高优先级原则

## 2.1 历史交易必须使用历史价格

禁止：

```text
历史 Token 数量
×
当前价格
```

正确：

```text
历史 Token 数量
×
交易发生时间附近价格
```

---

# 3. Price Source 分层

价格来源优先：

```text
P0 本地已验证历史价格
P1 高质量中心化行情源
P2 DEX TWAP / Pool Price
P3 Aggregator
P4 Stablecoin Peg Fallback
P5 Missing
```

每条价格必须携带：

```text
source
confidence
timestamp
resolution
```

---

# 4. token_prices

建议核心价格表：

```text
token_prices
```

字段：

```text
chain_id
token_address

price_time
time_bucket

price_usd

source
confidence

resolution

liquidity_usd
volume_usd

is_fallback

ingested_at
updated_at
```

排序建议：

```text
ORDER BY
(
    chain_id,
    token_address,
    price_time
)
```

---

# 5. Price Resolution

建议：

```text
近 30 天：1 minute
30–365 天：5 minute
1 年以上：1 hour
```

也可以统一保存 1m 原始，再通过物化聚合：

```text
5m
1h
1d
```

---

# 6. Stablecoin 价格

不能永远固定：

```text
USDT = 1
USDC = 1
```

正确：

```text
优先真实历史价格
```

价格缺失时：

```text
PEG_FALLBACK = 1
```

并明确：

```text
confidence = FALLBACK
```

---

# 7. Price Resolver

新增：

```text
internal/pricing/
```

建议：

```text
pricing/
├── resolver.go
├── repository.go
├── cache.go
├── source.go
├── stablecoin.go
├── twap.go
├── confidence.go
└── backfill.go
```

接口：

```go
type PriceResolver interface {
    ResolvePrice(
        ctx context.Context,
        chainID uint64,
        token string,
        timestamp time.Time,
    ) (*HistoricalPrice, error)
}
```

---

# 8. Price Lookup 容差

如果交易时间：

```text
12:01:37
```

价格粒度：

```text
1 minute
```

优先：

```text
12:01
```

若缺：

```text
12:00
12:02
```

允许最近邻，但必须设置最大容差。

建议：

```text
1m source: <= 2 minutes
5m source: <= 10 minutes
1h source: <= 2 hours
```

超过：

```text
PRICE_MISSING
```

不能无限找最近价格。

---

# 9. USD Enrichment

Token Transfer 最终应能返回：

```text
amount_decimal
price_usd
usd_value

price_time
price_source
price_confidence
```

注意：

不一定需要把所有 enrichment 永久写回事实表。

优先评估：

```text
Price Join
Materialized View
Derived Table
```

避免频繁重写大事实表。

---

# 10. Native Coin Price

BNB / ETH 等 Native Coin 也必须进入统一 Price Registry。

不要单独写一套：

```text
BNBPriceService
```

统一使用：

```text
Native Asset Token ID
```

---

# 11. Historical USD Flow

新增统一分析结果：

```text
AddressUSDFlow
```

字段：

```text
total_in_usd
total_out_usd
netflow_usd

native_in_usd
native_out_usd

stablecoin_in_usd
stablecoin_out_usd

token_in_usd
token_out_usd
```

---

# 12. 时间窗口

所有资金统计统一：

```text
24H
7D
30D
90D
1Y
ALL
CUSTOM
```

同一查询窗口必须贯穿：

```text
Explorer
Analytics
Graph
Investigation
Export
```

---

# 13. Daily Financial Stats

新增/升级：

```text
address_daily_financial_stats
```

字段：

```text
chain_id
address
date

in_usd
out_usd
netflow_usd

native_in_usd
native_out_usd

stablecoin_in_usd
stablecoin_out_usd

token_in_usd
token_out_usd

cex_in_usd
cex_out_usd

dex_volume_usd
bridge_in_usd
bridge_out_usd

large_in_count
large_out_count
```

---

# 14. Materialized Views

优先预计算：

```text
Daily Address Flow
Daily Token Flow
Daily Entity Flow
Daily Protocol Flow
```

避免每次 Analytics：

```text
扫描全部明细
```

---

# 15. Address Financial Summary

统一：

```text
AddressFinancialSummary
```

包括：

```text
Total In
Total Out
Net Flow

Largest In
Largest Out

Average In
Average Out

Median In
Median Out

First Funding
Latest Funding
```

---

# 16. 大额资金定义

用户可配置：

```text
Large Transfer Threshold
```

例如：

```text
$10K
$100K
$1M
Custom
```

默认调查视角建议：

```text
$100,000
```

但不得硬编码。

---

# 17. Large Transfer Stats

输出：

```text
large_in_count
large_out_count

large_in_usd
large_out_usd

largest_in
largest_out
```

---

# 18. Counterparty Financial Stats

升级：

```text
address_counterparty_financial_stats
```

字段：

```text
address
counterparty

in_usd
out_usd
netflow_usd

in_count
out_count

first_interaction
last_interaction

entity_id
entity_role
```

---

# 19. Entity Counterparty Stats

新增：

```text
address_entity_stats
```

例如：

```text
Address A
→ Binance
```

把 Binance 多地址聚合。

输出：

```text
entity
in_usd
out_usd
netflow_usd
count
```

---

# 20. Inflow Concentration

统计：

```text
Top1
Top5
Top10
```

公式：

```text
Top N Sources Inflow
/
Total Inflow
```

---

# 21. Outflow Concentration

同样：

```text
Top1
Top5
Top10
```

用途：

```text
资金来源集中
归集
集中提现
分散转出
```

只描述行为，不自动定性。

---

# 22. Retention / 资金沉淀

定义：

```text
资金流入后
在指定时间窗口内
尚未被对应转出的部分
```

时间窗口：

```text
1H
6H
24H
7D
30D
```

输出：

```text
retained_1h_usd
retained_6h_usd
retained_24h_usd
retained_7d_usd
retained_30d_usd
```

---

# 23. Retention Matching V1

初期建议使用：

```text
FIFO
```

按：

```text
Token + Address
```

匹配：

```text
Incoming Lots
→
Outgoing Lots
```

避免简单：

```text
总流入 - 总流出
```

因为那无法判断某笔资金是否真正沉淀。

---

# 24. Retention Lot

逻辑：

```text
Incoming Lot

tx_hash
token
amount
usd_value
time

remaining_amount
```

后续 Outgoing 按 FIFO 消耗。

---

# 25. Stablecoin Retention

USDT/USDC 等可以直接：

```text
Token-level FIFO
```

非常适合调查资金沉淀。

---

# 26. Native Coin Retention

BNB：

```text
需要考虑 gas 消耗
```

可分：

```text
transfer_out
gas_fee
```

避免错误地把 Gas 当作资金转出到 Counterparty。

---

# 27. Fast Pass-Through

识别：

```text
收到资金
↓
短时间内继续向其他地址转出
```

窗口：

```text
5m
30m
1h
6h
24h
```

---

# 28. Pass-through Ratio

定义：

```text
Matched Outgoing Amount
within window
/
Incoming Amount
```

输出：

```text
pass_5m
pass_30m
pass_1h
pass_6h
pass_24h
```

---

# 29. Pass-through Example

收到：

```text
$1,000,000 USDT
```

1 小时内转出：

```text
$850,000
```

则：

```text
1H Pass-through = 85%
```

---

# 30. Pass-through Interpretation

系统只输出：

```text
HIGH_PASS_THROUGH
```

作为行为指标。

不能自动：

```text
洗钱
犯罪
```

---

# 31. Settlement Ratio

新增：

```text
Settlement Ratio
```

表示地址长期保留资金比例。

例如：

```text
30D Retention
/
Total Received
```

---

# 32. CEX Financial Analytics

基于 Entity Registry：

```text
CEX Deposit Count
CEX Deposit USD

CEX Withdrawal Count
CEX Withdrawal USD

CEX Net Flow
```

---

# 33. Exchange Breakdown

例如：

```text
Binance
OKX
Bybit
Gate
```

分别：

```text
deposit
withdrawal
netflow
count
```

---

# 34. CEX Deposit Role

只有：

```text
Entity Type = CEX
Role = DEPOSIT / COLLECTOR / HOT_WALLET
```

达到设定置信度时才做对应分类。

低置信度：

```text
CEX Interaction
```

不要强行定为 Deposit。

---

# 35. DEX Financial Analytics

如果已解析 DEX_SWAP：

统计：

```text
swap_count
swap_volume_usd

buy_volume
sell_volume

top_protocol
top_pair
top_token
```

---

# 36. Swap Semantics

推荐统一：

```text
token_in
amount_in
usd_in

token_out
amount_out
usd_out

protocol
router
pool
```

---

# 37. DEX Volume

避免把 Swap 中：

```text
多个内部 Transfer
```

全部加总成 Volume。

应该以：

```text
Canonical Swap
```

为单位。

否则会重复计算。

---

# 38. Bridge Analytics

统计：

```text
bridge_in_usd
bridge_out_usd

bridge_in_count
bridge_out_count

top_bridge
```

---

# 39. Cross-chain Flow

未来：

```text
BSC
↓
Bridge
↓
Ethereum
```

构造：

```text
Cross-chain Edge
```

本阶段先定义数据模型，不强制全部支持。

---

# 40. PnL：定义边界

PnL 必须区分：

```text
Token Trading PnL
Wallet Net Flow
Portfolio PnL
```

不要混成一个指标。

---

# 41. Realized PnL

V1 建议：

```text
FIFO Cost Basis
```

针对：

```text
DEX 买入
DEX 卖出
```

计算：

```text
Sale Proceeds
-
FIFO Cost Basis
-
Gas
=
Realized PnL
```

---

# 42. Unrealized PnL

计算：

```text
Current Position Value
-
Remaining Cost Basis
```

需要：

```text
Current Price
```

---

# 43. PnL 不能把转账当买卖

例如：

```text
A → B 100 USDT
```

不是：

```text
Sell
```

PnL 只基于：

```text
Swap
Trade
Known Buy/Sell Semantic
```

---

# 44. Cost Basis

建议支持：

```text
FIFO
```

后续可以：

```text
LIFO
Weighted Average
```

但 V1 不要同时做。

---

# 45. Token Position Ledger

建议：

```text
token_position_lots
```

字段：

```text
address
token

acquired_time
acquired_amount

cost_usd

remaining_amount
remaining_cost

source_tx
source_type
```

---

# 46. Position Events

来源：

```text
DEX_BUY
DEX_SELL
TRANSFER_IN
TRANSFER_OUT
AIRDROP
MINT
BURN
BRIDGE
UNKNOWN
```

PnL 逻辑必须区分。

---

# 47. Transfer In Cost Basis

如果从其他地址收到 Token：

无法自动知道成本。

标记：

```text
cost_basis = UNKNOWN
```

除非：

```text
同实体地址迁移
```

或者用户/调查上下文明确。

---

# 48. Unknown Cost Basis

PnL 输出：

```text
known_cost_basis_ratio
```

例如：

```text
Realized PnL: $123K
Cost Basis Coverage: 72%
```

避免给出虚假的精确结果。

---

# 49. Financial Confidence

每个高级金融指标建议：

```text
confidence
coverage
```

例如：

```text
Historical USD Coverage = 97.2%
PnL Cost Basis Coverage = 74.1%
Entity Coverage = 12.8%
```

---

# 50. Financial Data Quality

新增：

```text
Financial Data Quality
```

显示：

```text
Price Coverage
Stablecoin Fallback Ratio
Unknown Price
Unknown Cost Basis
DEX Decode Coverage
Bridge Decode Coverage
Entity Coverage
```

---

# 51. Price Backfill

新增：

```text
Price Backfill Job
```

参数：

```text
chain
token
time range
resolution
source priority
```

---

# 52. Price Gap Repair

自动检测：

```text
Missing Buckets
```

例如：

```text
2025-01-01 10:00
→
2025-01-01 11:00
```

缺失。

任务：

```text
PRICE_GAP_REPAIR
```

---

# 53. Price Provider 不应阻塞 Explorer

Explorer 查询：

```text
本地 Price DB
```

禁止：

```text
Explorer
→ 外部行情 API
```

所有外部价格数据：

```text
先入本地
```

---

# 54. Current Price

实时/准实时：

```text
CurrentPriceCache
```

TTL：

```text
30s~60s
```

失败：

```text
显示 stale
```

不能显示假 0。

---

# 55. Historical Price Cache

历史价格：

```text
immutable / long TTL
```

除非：

```text
quality correction
```

---

# 56. Price Versioning

保存：

```text
price_version
source_version
```

修正价格后：

```text
Financial Re-enrichment
```

而不是重新下载链上交易。

---

# 57. Financial Re-enrichment

新增任务：

```text
FINANCIAL_REENRICH
```

用途：

```text
新价格数据
价格修正
实体修正
DEX decode 修正
```

重新计算：

```text
USD
Stats
PnL
Retention
```

不触发 Downloader。

---

# 58. Analytics API V2

新增：

```http
GET /api/v2/analytics/address/:address/financial-summary
GET /api/v2/analytics/address/:address/counterparties
GET /api/v2/analytics/address/:address/retention
GET /api/v2/analytics/address/:address/pass-through
GET /api/v2/analytics/address/:address/cex
GET /api/v2/analytics/address/:address/dex
GET /api/v2/analytics/address/:address/bridge
GET /api/v2/analytics/address/:address/pnl
```

---

# 59. Financial Summary API

返回：

```json
{
  "time_range": {},
  "total_in_usd": "1250000.12",
  "total_out_usd": "980000.21",
  "netflow_usd": "269999.91",
  "largest_in_usd": "500000",
  "largest_out_usd": "300000",
  "price_coverage": 0.982
}
```

---

# 60. Explorer Integration

Address Overview：

```text
Total In
Total Out
Net Flow
```

点击：

```text
View Financial Analytics
```

进入完整统计。

---

# 61. Graph Integration

资金图 Edge：

```text
Amount
USD Value
Historical Price
```

过滤：

```text
Min USD
```

必须基于：

```text
Historical USD
```

而不是当前价格。

---

# 62. Graph Edge Aggregation

如果时间范围内：

```text
A → B
```

发生 500 次：

聚合：

```text
Tx Count
Total USD
Token Breakdown
First
Last
```

---

# 63. Investigation Integration

Agent 可读取：

```text
Historical USD
CEX Flows
Retention
Pass-through
PnL
Concentration
```

例如：

```text
目标：
找资金沉淀
```

Planner 直接调用：

```text
Retention Analytics
```

而不是自己扫描所有交易推算。

---

# 64. Export Integration

支持导出：

```text
historical_price
historical_usd
price_source
price_confidence

entity
role

retention
pass_through
```

---

# 65. Saved Financial Filters

Data Assets：

```text
USDT OUT >= $100K
CEX Deposit >= $500K
1H Pass-through >= 80%
30D Retention >= $1M
```

支持保存。

---

# 66. Materialized Analytics

建议预聚合：

```text
address_daily_financial_stats
address_entity_daily_stats
token_daily_flow_stats
protocol_daily_stats
```

---

# 67. 不建议预计算所有地址所有指标

例如：

```text
30D Retention
```

计算成本高。

可：

```text
按需计算
+
缓存结果
```

热点地址再 Materialize。

---

# 68. Investigation Cache

案件分析：

```text
Case ID
+
Address
+
Time Range
+
Token Filter
+
Algorithm Version
```

缓存：

```text
Retention
Pass-through
PnL
```

---

# 69. Algorithm Version

高级指标必须记录：

```text
algorithm_version
```

例如：

```text
retention_fifo_v1
pass_through_v1
pnl_fifo_v1
```

---

# 70. 可复现性

调查报告：

```text
Retention 24H = $1.2M
```

必须能追溯：

```text
data snapshot
price version
algorithm version
time range
token filter
```

---

# 71. Benchmark

必须重新做：

```text
50M facts
+
price join
```

测试：

```text
Address 30D USD Flow
Address ALL USD Flow
Top Counterparty
CEX Stats
Retention
Pass-through
```

---

# 72. 性能目标

建议：

```text
Financial Summary 30D     < 500ms P95
Financial Summary ALL     < 2s P95
Counterparty Top10        < 1s P95
CEX Summary               < 1s P95
DEX Summary               < 1s P95
```

Retention：

```text
热点缓存 < 1s
冷计算允许数秒
```

---

# 73. P0

必须完成：

```text
token_prices

Price Resolver
Stablecoin fallback
Native price

Historical USD enrichment
Address Financial Summary

Counterparty USD Stats
CEX Financial Stats

Price Coverage
Financial Quality Dashboard
```

---

# 74. P1

```text
Retention FIFO
Pass-through

DEX Financial Analytics
Bridge Financial Analytics

Daily Financial Stats
Entity Flow Stats

Financial Re-enrichment
```

---

# 75. P2

```text
PnL FIFO

Token Position Lots
Realized PnL
Unrealized PnL

Cross-chain Financial Flow

Historical Portfolio Snapshot
```

---

# 76. 验收 Case A：Historical USD

交易：

```text
2025-01-01
100 TOKEN
```

当时价格：

```text
$2
```

当前价格：

```text
$10
```

必须：

```text
Historical USD = $200
```

不能：

```text
$1,000
```

---

# 77. 验收 Case B：Stablecoin Depeg

如果 USDT 历史价格：

```text
$0.98
```

必须优先：

```text
$0.98
```

不能固定：

```text
$1
```

---

# 78. 验收 Case C：Price Missing

缺价格：

```text
usd_value = null
```

并：

```text
price_status = MISSING
```

禁止：

```text
0 USD
```

---

# 79. 验收 Case D：CEX Flow

地址：

```text
A → Binance Deposit
$500K
```

必须：

```text
CEX Deposit USD += 500K
```

Entity 低置信度：

不得强制计为：

```text
Verified CEX Deposit
```

---

# 80. 验收 Case E：Retention

收到：

```text
100K USDT
```

1H 内转出：

```text
20K
```

24H 内再转：

```text
50K
```

则：

```text
1H Retained = 80K
24H Retained = 30K
```

---

# 81. 验收 Case F：Pass-through

收到：

```text
1M
```

30m 内转出：

```text
800K
```

则：

```text
30m Pass-through = 80%
```

---

# 82. 验收 Case G：PnL

买入：

```text
$100K
```

卖出：

```text
$150K
```

Gas：

```text
$1K
```

则已知成本部分：

```text
Realized PnL = $49K
```

---

# 83. 验收 Case H：Transfer 不计卖出

```text
A → B TOKEN
```

不能自动生成：

```text
Sell
```

---

# 84. 验收 Case I：Re-enrichment

更新价格源后：

```text
不重新下载链数据
```

只触发：

```text
Financial Re-enrichment
```

---

# 85. 完成标志

本阶段完成后，系统必须能够直接回答：

```text
这个地址历史总共流入多少钱？
流出多少钱？
净流量是多少？

资金主要来自谁？
主要去了谁？

多少进入交易所？
多少从交易所出来？

多少资金在 24H 后仍沉淀？
多少资金收到后 1H 内被继续转出？

这个 Token 在当时值多少钱？

这个地址实际交易收益是多少？
PnL 的成本覆盖率是多少？
```

---

# 86. 下一阶段

本阶段完成后，建议进入：

```text
Explorer Intelligence UI V1.0
```

因为届时前端所需的：

```text
Token Identity
Entity
Method
Contract
Historical USD
CEX/DEX/Bridge
Retention
Pass-through
PnL
```

都已经成为稳定后端资产。

Explorer UI 可以直接围绕：

```text
OKLink 信息密度
+
更强统计
+
资金调查
+
Graph
+
Investigation
+
Export
```

进行最终产品化，而不再反复返工数据层。
