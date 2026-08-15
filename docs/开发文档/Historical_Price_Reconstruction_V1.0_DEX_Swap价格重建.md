# Historical Price Reconstruction V1.0
## 基于 DEX Swap 的历史价格重建与 USD Enrichment

> 当前基线：Turbo Mode 已完成真实生产验收，SQD Cloud bulk lane、RPC validation/repair、ClickHouse 入库、Coverage、去重、CERTIFIED 均已通过。
>
> 当前最大剩余缺口：SQD Transfer 本身不包含历史 USD 单价，因此需要建立本地历史价格重建层。

## 1. 总体目标

```text
Token Transfer
→ Token Identity
→ Pool Discovery
→ Relevant Pools
→ Swap Download
→ Price Anchor
→ Historical Price
→ historical_usd
→ Explorer / Analytics / Graph / Investigation / Export
```

原则：

- 历史交易必须使用历史价格，不能使用当前价格倒算。
- Explorer 查询时禁止临时访问外部价格 API。
- 历史价格必须先成为本地数据资产。
- 价格缺失必须返回 null / MISSING，不能伪装为 0 USD。

## 2. 价格锚

优先级建议：

```text
P0 Token/USDT
P1 Token/USDC
P2 Token/FDUSD
P3 Token/WBNB
P4 其他高流动性可信锚
P5 Missing
```

直接稳定币池：

```text
100 TOKEN ↔ 200 USDT
→ TOKEN ≈ $2
```

WBNB 间接锚：

```text
1 TOKEN = 0.01 WBNB
WBNB = $600
→ TOKEN = $6
```

## 3. Pool Discovery

新增 `PoolDiscovery`，来源：

```text
Factory Events
PairCreated
PoolCreated
Protocol Registry
Contract Registry
```

优先支持：

```text
PancakeSwap V2
PancakeSwap V3
Uniswap-style V2
Uniswap-style V3
```

新增 `pool_registry`：

```text
chain_id
pool_address
protocol_id
version
token0
token1
fee_tier
factory_address
created_block
created_time
verified
liquidity_score
updated_at
```

同一 Token 多池时，优先：

```text
直接稳定币池
高流动性
高成交量
可信协议
近期活跃
```

V1 先选最高可信主池，不急着做复杂多池融合。

## 4. Canonical Swap

新增统一 Swap 资产：

```text
chain_id
block_number
block_time
tx_hash
log_index
protocol
pool
token_in
amount_in
token_out
amount_out
price_token0_token1
usd_value
source
```

新增 Dataset：

```text
DEX_SWAP
```

下载继续复用 Turbo：

```text
SQD Cloud = 历史 bulk
RPC = tail / repair / validation
```

禁止为了一个 Token 下载全 BSC Swap，应先发现相关池，再只下载这些池。

## 5. Historical Price Builder

新增：

```text
token_prices
```

字段：

```text
chain_id
token_address
price_time
price_usd
resolution
source_pool
source_protocol
source_type
confidence
staleness_seconds
volume_usd
price_version
ingested_at
```

V1 推荐 1 分钟粒度：

```text
token_prices_1m
```

同一分钟多个 Swap：

```text
VWAP
```

无成交分钟允许最近有效价格，但必须限制最大 staleness，例如：

```text
1m bucket: <= 5m
```

超过则：

```text
PRICE_MISSING
```

## 6. Price Confidence

统一：

```text
HIGH
MEDIUM
LOW
FALLBACK
MISSING
```

例如：

- HIGH：直接稳定币主池、高流动性、同分钟成交。
- MEDIUM：WBNB 间接锚或短时间前值。
- LOW：低流动性或较长插值。
- FALLBACK：稳定币 PEG fallback。
- MISSING：没有可信价格。

## 7. Stablecoin

稳定币同样优先使用真实历史市场价格。

仅在价格缺失时：

```text
USDT / USDC = 1
```

并标记：

```text
price_source = PEG_FALLBACK
confidence = FALLBACK
```

## 8. Historical USD Enrichment

Token Transfer：

```text
amount_decimal
×
historical_price_usd
=
historical_usd
```

同时返回：

```text
price_time
price_source
price_confidence
```

不建议对超大事实表做全量 UPDATE，优先评估：

```text
Price Join
Materialized View
Derived Enriched Table
```

## 9. Explorer / Analytics / Graph / Investigation

Explorer Token Transfer 显示：

```text
100,000 USDT
$99,982.31
```

Hover：

```text
Historical Price
Price Time
Source Pool
Protocol
Confidence
```

价格缺失：

```text
--
Price unavailable
```

Analytics 可稳定计算：

```text
Total In USD
Total Out USD
Net Flow USD
Largest Transfer USD
Counterparty USD
CEX Flow USD
DEX Volume USD
Bridge Flow USD
```

Graph Edge 可使用 Historical USD 统一过滤：

```text
Min USD >= 100K
```

Investigation 可直接使用历史 USD 判断大额入金、出金、CEX 落地、沉淀。

## 10. Price Coverage

新增：

```text
Price Coverage
```

例如：

```text
98.41%
```

并建立 Price Quality Dashboard：

```text
Priced Transfers
Missing Price
Fallback Price
Stale Price
Coverage by Token
Coverage by Date
Coverage by Pool
```

## 11. Price Gap Repair

新增：

```text
PRICE_GAP_REPAIR
```

流程：

```text
检测某 Token 某时间缺价
→ 重新检查 Pool
→ 下载缺失 Swap
→ 重算价格桶
→ Financial Re-enrichment
```

价格补齐后禁止重新下载 Token Transfer。

## 12. Turbo 联动

如果当前地址急需历史 USD：

新增：

```text
⚡ 极速补价格
```

只优先补：

```text
当前 Token
当前时间范围
当前调查目标
```

而不是先补整个链所有历史价格。

## 13. BSC RPC Pool

当前生产尚未配置专用 BSC RPC endpoint。

下一步建议配置 RPC Pool，用于：

```text
Tail
Receipt
Pool Discovery
Metadata
Gap Repair
Validation
```

即使配置后，仍保持：

```text
SQD Cloud = Bulk
RPC = Tail / Repair / Validation
```

避免使用 RPC 做大规模全历史主下载。

## 14. thrift Advisory

Worker 上游 Parquet 包当前还有两个 thrift high advisory。

本阶段建议：

```text
记录风险
暂不破坏性降级
```

新增依赖风险记录：

```text
package
version
advisory
severity
upstream_status
mitigation
review_date
```

不要为了消除 advisory 破坏已经通过真实验收的 Worker。

## 15. P0

必须完成：

```text
Pool Registry
Pool Discovery
Canonical Swap
SQD Cloud Swap Download
RPC Swap Validation / Repair
USDT / USDC Anchor
WBNB Anchor
token_prices
Historical USD Resolver
Price Coverage
Explorer USD Integration
```

## 16. P1

```text
Multi Pool Selection
VWAP 1m
Price Gap Repair
Financial Re-enrichment
Price Quality Dashboard
```

## 17. P2

```text
5m / 1h / 1d Aggregates
Multi-pool Weighted Price
Liquidity Confidence
Price Turbo
PnL Integration
```

## 18. 核心验收

Case A：

```text
TOKEN/USDT
100 TOKEN ↔ 200 USDT
→ TOKEN ≈ $2
```

Case B：

```text
TOKEN/WBNB
1 TOKEN = 0.01 WBNB
WBNB = $600
→ TOKEN = $6
```

Case C：

```text
100 TOKEN
历史价格 $6
→ historical_usd = $600
```

Case D：

```text
当前价格 $20
历史价格 $6
→ Explorer 必须显示 $600
不能显示 $2,000
```

Case E：

```text
价格缺失
→ historical_usd = null
→ price_status = MISSING
```

Case F：

```text
价格补齐
→ 不重新下载 Transfer
→ 只 Financial Re-enrichment
```

## 19. 完成标准

完成后系统应稳定回答：

```text
这笔 Transfer 当时值多少钱？
这个地址历史总流入多少 USD？
历史总流出多少 USD？
净流量多少？
最大的资金来源是谁？
多少资金进入 CEX？
多少资金在 24H 后仍沉淀？
```

并且这些金额全部来自可追溯历史价格，而不是当前价格倒算。
