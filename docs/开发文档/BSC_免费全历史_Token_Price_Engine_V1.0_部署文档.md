# BSC 免费全历史 Token Price Engine V1.0
## 部署与实施文档

> 目标：在现有 BSC 链上分析系统中部署一个 **0 元 API 数据成本** 的历史代币价格数据层，支持分钟级价格重建、小时/日级聚合、历史 USD/USDT 估值，并接入 ClickHouse、智能调查、资金流向图和智能下载调度器。

---

# 1. 项目目标

本模块解决以下问题：

1. 获取 BSC 代币的历史价格；
2. 主流币尽量直接使用免费分钟 K 线；
3. 冷门币、土狗币、下架币、只在 DEX 交易的代币，通过链上 Swap 重建历史价格；
4. 支持 1 分钟粒度；
5. 支持任意时间点查询 Token/USD 价格；
6. 自动生成 5m / 15m / 1h / 4h / 1d 聚合；
7. 为 token_transfer、资金流图、地址画像、盈亏分析提供历史美元估值；
8. 不依赖必须付费的历史价格 API；
9. 数据最终全部进入 ClickHouse；
10. 对缺失价格、低流动性、异常池、路径价格进行置信度标记。

---

# 2. 非目标

V1.0 暂不追求：

- 所有链同时上线；
- 高频交易级撮合回放；
- Tick-by-Tick 永久保存；
- 跨 5 跳以上复杂路由；
- 商业级 CEX 全市场聚合；
- 绝对实时毫秒级价格；
- 对完全没有历史交易的 Token 人工创造价格。

---

# 3. 总体架构

```text
                         ┌──────────────────────────┐
                         │ Historical Price Engine  │
                         └────────────┬─────────────┘
                                      │
           ┌──────────────────────────┼──────────────────────────┐
           │                          │                          │
           ▼                          ▼                          ▼
┌──────────────────┐       ┌──────────────────┐       ┌──────────────────┐
│ Binance Public   │       │ SQD Public       │       │ AWS Public BNB   │
│ 1m Kline         │       │ DEX Swap         │       │ Historical Data  │
└────────┬─────────┘       └────────┬─────────┘       └────────┬─────────┘
         │                          │                          │
         └──────────────────────────┼──────────────────────────┘
                                    ▼
                          ┌────────────────────┐
                          │ Download Router    │
                          │ / Orchestrator     │
                          └─────────┬──────────┘
                                    ▼
                          ┌────────────────────┐
                          │ Swap Normalizer    │
                          └─────────┬──────────┘
                                    ▼
                          ┌────────────────────┐
                          │ Pool Resolver      │
                          └─────────┬──────────┘
                                    ▼
                          ┌────────────────────┐
                          │ Price Router       │
                          └─────────┬──────────┘
                                    ▼
                          ┌────────────────────┐
                          │ USD Resolver       │
                          └─────────┬──────────┘
                                    ▼
                          ┌────────────────────┐
                          │ ClickHouse         │
                          └─────────┬──────────┘
                                    ▼
             ┌──────────────────────┼──────────────────────┐
             │                      │                      │
             ▼                      ▼                      ▼
      Smart Investigation       Flow Graph          Profit Engine
```

---

# 4. 数据源设计

## 4.1 Binance Public Data

用途：

- BNB/USDT
- ETH/USDT
- BTC/USDT
- 其他 Binance 已上市资产

建议保存最小周期：

```text
1m
```

其他周期全部由 ClickHouse 聚合。

优先处理：

```text
BNBUSDT
ETHUSDT
BTCUSDT
USDCUSDT
FDUSDUSDT
```

可继续扩展 Binance 上市 Token。

---

## 4.2 SQD Public Portal

用途：

- 按合约 / Topic 获取 PancakeSwap 历史 Swap；
- 获取 Pool 创建事件；
- 获取历史 Logs；
- 获取目标 Token 相关池交易。

新增 Dataset 类型：

```text
DEX_SWAP
DEX_POOL
DEX_FACTORY
```

V1.0 优先支持：

```text
PancakeSwap V2
PancakeSwap V3
```

后续可增加：

```text
Uniswap V3 BSC
Biswap
Thena
MDEX
其他 BSC DEX
```

---

## 4.3 AWS Public BNB Chain Data

定位：

```text
大窗口回填
批量历史扫描
SQD 503 时补充
完整日期级数据导入
```

不建议每次调查都临时重新扫描 AWS。

建议：

```text
AWS -> 本地阶段缓存 -> 解析 -> ClickHouse -> 删除原始大文件
```

---

## 4.4 RPC

RPC 只作为：

```text
小范围缺口修复
最新区块补齐
Pool metadata 获取
decimals / symbol 查询
eth_getLogs 小窗口恢复
```

不要让 RPC 承担全历史回填。

---

# 5. 存储目录

ClickHouse 继续使用固定根目录：

```text
E:\database\clickhouse
```

Price Engine 建议：

```text
E:\database\price_engine\
├─ config\
├─ cache\
│  ├─ binance\
│  ├─ sqd\
│  ├─ aws\
│  └─ rpc\
├─ staging\
│  ├─ swap\
│  ├─ kline\
│  └─ normalized\
├─ checkpoint\
├─ manifests\
├─ logs\
├─ export\
└─ temp\
```

禁止将历史行情缓存、临时 Parquet、大文件、中间结果默认写入 C 盘。

---

# 6. ClickHouse 数据模型

---

## 6.1 `price_anchor_1m`

用于保存 BNB/USDT、ETH/USDT 等锚定资产 1m 行情。

```sql
CREATE TABLE IF NOT EXISTS price_anchor_1m
(
    chain_id UInt32,
    symbol LowCardinality(String),
    quote_symbol LowCardinality(String),

    minute DateTime64(3, 'UTC'),

    open Decimal(38, 18),
    high Decimal(38, 18),
    low Decimal(38, 18),
    close Decimal(38, 18),

    volume_base Decimal(38, 18),
    volume_quote Decimal(38, 18),

    source LowCardinality(String),
    ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY toYYYYMM(minute)
ORDER BY (symbol, quote_symbol, minute);
```

---

## 6.2 `dex_pools`

```sql
CREATE TABLE IF NOT EXISTS dex_pools
(
    chain_id UInt32,
    dex LowCardinality(String),
    version LowCardinality(String),

    factory_address FixedString(42),
    pool_address FixedString(42),

    token0_address FixedString(42),
    token1_address FixedString(42),

    token0_symbol String,
    token1_symbol String,

    token0_decimals UInt8,
    token1_decimals UInt8,

    fee_bps UInt32,

    created_block UInt64,
    created_at DateTime64(3, 'UTC'),

    is_active UInt8 DEFAULT 1,

    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (chain_id, pool_address);
```

---

## 6.3 `dex_swaps`

这是整个价格重建系统的核心事实表。

```sql
CREATE TABLE IF NOT EXISTS dex_swaps
(
    chain_id UInt32,

    block_number UInt64,
    block_time DateTime64(3, 'UTC'),

    tx_hash FixedString(66),
    log_index UInt32,

    dex LowCardinality(String),
    version LowCardinality(String),
    pool_address FixedString(42),

    token0_address FixedString(42),
    token1_address FixedString(42),

    amount0_raw Int256,
    amount1_raw Int256,

    amount0 Decimal(76, 30),
    amount1 Decimal(76, 30),

    token0_per_token1 Decimal(76, 30),
    token1_per_token0 Decimal(76, 30),

    token0_usd Nullable(Decimal(38, 18)),
    token1_usd Nullable(Decimal(38, 18)),

    volume_usd Nullable(Decimal(38, 18)),

    source LowCardinality(String),
    source_job_id String,

    inserted_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(inserted_at)
PARTITION BY toYYYYMM(block_time)
ORDER BY
(
    chain_id,
    pool_address,
    block_time,
    block_number,
    tx_hash,
    log_index
);
```

唯一事件键：

```text
chain_id
+ block_number
+ tx_hash
+ log_index
```

---

## 6.4 `token_price_1m`

```sql
CREATE TABLE IF NOT EXISTS token_price_1m
(
    chain_id UInt32,
    token_address FixedString(42),

    minute DateTime64(3, 'UTC'),

    open Decimal(38, 18),
    high Decimal(38, 18),
    low Decimal(38, 18),
    close Decimal(38, 18),

    vwap Decimal(38, 18),

    volume_token Decimal(76, 30),
    volume_usd Decimal(38, 18),

    trade_count UInt64,
    pool_count UInt32,

    liquidity_usd Nullable(Decimal(38, 18)),

    price_source LowCardinality(String),

    confidence Float32,

    is_interpolated UInt8 DEFAULT 0,
    is_last_known UInt8 DEFAULT 0,

    updated_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY toYYYYMM(minute)
ORDER BY (chain_id, token_address, minute);
```

---

## 6.5 `token_price_resolution_log`

用于审计“某个价格到底是怎么算出来的”。

```sql
CREATE TABLE IF NOT EXISTS token_price_resolution_log
(
    chain_id UInt32,
    token_address FixedString(42),
    timestamp DateTime64(3, 'UTC'),

    resolved_price Nullable(Decimal(38, 18)),

    route String,
    source_pool String,

    hop_count UInt8,

    confidence Float32,

    status LowCardinality(String),
    reason String,

    created_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(timestamp)
ORDER BY (chain_id, token_address, timestamp);
```

---

# 7. Price Router 规则

价格解析优先级建议：

```text
P0  TOKEN/USDT
P1  TOKEN/USDC
P2  TOKEN/BUSD
P3  TOKEN/FDUSD
P4  TOKEN/WBNB × BNB/USDT
P5  TOKEN/WETH × ETH/USDT
P6  两跳稳定路径
P7  LAST_KNOWN_PRICE
P8  UNKNOWN
```

最大允许：

```text
max_hops = 2
```

V1.0 不建议默认超过两跳。

---

# 8. Pool 选择规则

一个 Token 可能有多个交易池。

不能简单使用第一个池。

建议评分：

```text
pool_score =
    liquidity_score  * 0.45
  + volume_score     * 0.30
  + trade_score      * 0.15
  + age_score        * 0.05
  + dex_score        * 0.05
```

默认过滤：

```text
liquidity_usd < 1000
```

的池不作为主要价格源。

阈值应配置化：

```yaml
price:
  pool:
    min_liquidity_usd: 1000
    min_trade_count_1h: 2
    max_price_deviation_pct: 25
```

---

# 9. 异常价格过滤

必须处理：

- 闪电贷；
- 极低流动性池；
- 恶意价格操纵；
- 单笔极端成交；
- Honeypot；
- 税费 Token；
- decimals 错误；
- rebasing Token；
- fee-on-transfer Token。

建议：

```text
median_price
± deviation threshold
```

默认：

```text
25%
```

超出即：

```text
OUTLIER
```

不直接进入 Canonical Price。

---

# 10. 1 分钟 OHLCV 重建

对一分钟内的有效成交：

```text
OPEN  = 第一笔有效成交
HIGH  = 最大有效价格
LOW   = 最小有效价格
CLOSE = 最后一笔有效成交
```

VWAP：

```text
VWAP = Σ(price × volume_usd) / Σ(volume_usd)
```

如果 USD volume 不可用，可先使用 quote volume。

---

# 11. 无交易分钟处理

严禁把“没有成交”伪装成真实成交价。

例如：

```text
12:00 有成交 $0.10
12:01 无成交
12:02 无成交
12:03 有成交 $0.11
```

允许查询层返回：

```text
12:01 = $0.10
12:02 = $0.10
```

但必须：

```text
is_last_known = 1
```

并保存：

```text
price_age_seconds
```

建议 API 最终返回：

```json
{
  "price_usd": 0.10,
  "price_type": "LAST_KNOWN",
  "age_seconds": 120,
  "confidence": 0.61
}
```

---

# 12. Binance Kline 导入

新增模块：

```text
internal/price/binance
```

职责：

```text
symbol discovery
download
checksum
decompress
normalize
insert ClickHouse
checkpoint
```

建议首批 Symbol：

```text
BNBUSDT
ETHUSDT
BTCUSDT
```

后续：

```text
USDCUSDT
FDUSDUSDT
```

对于 Binance 已上市 Token，可以自动扩展。

---

# 13. DEX Swap Decoder

新增：

```text
internal/price/dex
```

结构：

```text
dex/
├─ interface.go
├─ registry.go
├─ pancakeswap_v2.go
├─ pancakeswap_v3.go
├─ pool_resolver.go
├─ token_metadata.go
└─ normalize.go
```

接口：

```go
type SwapDecoder interface {
    Match(log LogRecord) bool
    Decode(log LogRecord) (*NormalizedSwap, error)
}
```

标准输出：

```go
type NormalizedSwap struct {
    ChainID       uint32
    BlockNumber   uint64
    BlockTime     time.Time
    TxHash        string
    LogIndex      uint32

    DEX            string
    Version        string
    PoolAddress    string

    Token0         string
    Token1         string

    Amount0        *big.Rat
    Amount1        *big.Rat
}
```

---

# 14. Download Orchestrator 接入

增加任务类型：

```text
PRICE_ANCHOR
PRICE_POOL_DISCOVERY
PRICE_SWAP_BACKFILL
PRICE_GAP_REPAIR
PRICE_REBUILD
```

路由规则：

```text
PRICE_ANCHOR
→ Binance Public Data

PRICE_POOL_DISCOVERY
→ SQD
→ RPC fallback

PRICE_SWAP_BACKFILL
→ SQD
→ AWS
→ RPC small-window fallback

PRICE_GAP_REPAIR
→ RPC
→ SQD

PRICE_REBUILD
→ ClickHouse only
```

---

# 15. 回填状态机

```text
CREATED
  ↓
DISCOVERING_POOLS
  ↓
PLANNING
  ↓
DOWNLOADING
  ↓
DECODING
  ↓
NORMALIZING
  ↓
RESOLVING_USD
  ↓
AGGREGATING_1M
  ↓
VALIDATING
  ↓
COMPLETED
```

失败：

```text
WAITING_RETRY
FAILED
PARTIAL
```

---

# 16. Checkpoint

建议：

```json
{
  "job_id": "price-backfill-xxx",
  "token": "0x...",
  "from_block": 30000000,
  "to_block": 40000000,
  "last_completed_block": 35500000,
  "rows_committed": 1835221,
  "parts": 128,
  "updated_at": "2026-08-09T00:00:00Z"
}
```

要求：

- 可 Kill 后继续；
- 已完成区块不重复下载；
- ClickHouse 使用事件唯一键幂等；
- Manifest 保存来源和 checksum。

---

# 17. Price Service

新增：

```text
internal/price/service
```

核心接口：

```go
GetPrice(token, timestamp)
GetPriceRange(token, start, end, interval)
GetLatestPrice(token)
GetPriceWithConfidence(token, timestamp)
ResolveTransferValue(token, amount, timestamp)
```

---

# 18. HTTP API

---

## 18.1 查询指定时间价格

```http
GET /api/price/history/point
```

参数：

```text
chain=bsc
token=0x...
timestamp=2023-05-13T14:37:26Z
```

返回：

```json
{
  "chain": "bsc",
  "token": "0x...",
  "timestamp": "2023-05-13T14:37:26Z",
  "price_usd": "0.01284",
  "source": "DEX_RECONSTRUCTED",
  "confidence": 0.97,
  "price_type": "TRADED",
  "pool_count": 3
}
```

---

## 18.2 查询 K 线

```http
GET /api/price/history/candles
```

参数：

```text
token
start
end
interval=1m|5m|15m|1h|4h|1d
```

---

## 18.3 批量估值

```http
POST /api/price/value/batch
```

请求：

```json
{
  "items": [
    {
      "token": "0x...",
      "amount": "13826391",
      "timestamp": "2023-05-13T14:37:26Z"
    }
  ]
}
```

---

# 19. Token Transfer 联动

对已有 token transfer：

```text
amount
token_address
block_time
```

新增逻辑字段：

```text
historical_price_usd
historical_value_usd
price_confidence
price_source
```

不要强制把价格写回原始事实表。

建议查询时 JOIN：

```text
token_transfer
LEFT ASOF JOIN
token_price_1m
```

或者建立独立 enrichment 表。

---

# 20. 资金流向图联动

边：

```text
A
→
B
13,826,391 FIST
```

升级为：

```text
A
→
B

13,826,391 FIST
$177,528.85 @ tx time
```

Edge metadata：

```json
{
  "token": "FIST",
  "amount": "13826391",
  "price_usd": "0.01284",
  "value_usd": "177528.85",
  "price_timestamp": "2023-05-13T14:37:00Z",
  "price_confidence": 0.97
}
```

---

# 21. 智能调查联动

Agent 新增工具：

```text
get_historical_token_price
get_transfer_historical_value
get_token_price_range
get_token_peak_price
get_token_low_price
get_token_profit_estimate
```

Agent 可以回答：

```text
该地址收到 FIST 时价值多少？
出售时价值多少？
获利多少？
峰值期间资产价值多少？
大额转账实际价值多少？
```

---

# 22. 盈亏分析

对于地址买入：

```text
TOKEN_IN
```

计算：

```text
cost_basis_usd
```

对于卖出：

```text
TOKEN_OUT
```

计算：

```text
proceeds_usd
```

V1.0 支持：

```text
FIFO
```

后续支持：

```text
LIFO
WAC
```

必须注意：

```text
transfer ≠ buy
transfer ≠ sell
```

只有 Swap 或明确兑换才计入实现盈亏。

---

# 23. 聚合周期

以 `token_price_1m` 为唯一基础价格层。

查询：

```text
1m
5m
15m
1h
4h
1d
```

推荐先实时 SQL 聚合。

数据量非常大后再增加 Materialized View。

---

# 24. 1h 聚合示例

```sql
SELECT
    chain_id,
    token_address,
    toStartOfHour(minute) AS hour,

    argMin(open, minute) AS open,
    max(high) AS high,
    min(low) AS low,
    argMax(close, minute) AS close,

    sum(volume_usd) AS volume_usd,
    sum(trade_count) AS trade_count
FROM token_price_1m
WHERE token_address = lower('0x...')
GROUP BY
    chain_id,
    token_address,
    hour
ORDER BY hour;
```

---

# 25. 数据完整性校验

每次回填必须执行：

```text
raw_logs
decoded_swaps
unique_swaps
price_rows
```

校验：

```text
duplicate_event_key = 0
invalid_decimal = 0
negative_volume = 0
nan_price = 0
inf_price = 0
```

并验证：

```text
high >= open
high >= close
low <= open
low <= close
high >= low
```

---

# 26. 价格置信度

建议范围：

```text
0.00 – 1.00
```

示例：

```text
0.95+  高流动性稳定币直连池
0.85   WBNB 一跳
0.70   多池但成交较少
0.55   Last Known
0.30   低流动性池
0.00   无法解析
```

建议组成：

```text
confidence =
    liquidity_weight
  + volume_weight
  + pool_consensus_weight
  + freshness_weight
  + route_weight
```

---

# 27. 配置文件

建议：

```yaml
price_engine:
  enabled: true

  chain:
    bsc:
      chain_id: 56

  interval:
    base: 1m

  routing:
    max_hops: 2

  pool:
    min_liquidity_usd: 1000
    max_deviation_pct: 25

  fill:
    last_known_enabled: true
    max_last_known_age: 24h

  providers:
    binance:
      enabled: true

    sqd:
      enabled: true

    aws:
      enabled: true

    rpc:
      enabled: true

  clickhouse:
    database: analytics

  paths:
    root: 'E:\database\price_engine'
    cache: 'E:\database\price_engine\cache'
    staging: 'E:\database\price_engine\staging'
    checkpoint: 'E:\database\price_engine\checkpoint'
    logs: 'E:\database\price_engine\logs'
```

---

# 28. 部署阶段

---

## Phase P0 — 基础表和服务骨架

实施：

- ClickHouse 表；
- Price Service；
- Config；
- Repository；
- API 空实现；
- health check。

验收：

```text
服务启动成功
ClickHouse 连接成功
所有表存在
/api/price/health = 200
```

---

## Phase P1 — Binance 免费 1m Anchor

实施：

```text
BNBUSDT
ETHUSDT
BTCUSDT
```

目标：

```text
历史 1m 导入
断点续传
去重
增量同步
```

验收：

```text
BNB 历史 1m 查询成功
任意已存在分钟可返回 OHLC
重复导入不增加重复数据
```

---

## Phase P2 — PancakeSwap V2

实现：

```text
Factory PairCreated
Pair metadata
Swap decoder
TOKEN/Stable
TOKEN/WBNB
```

验收：

选择至少 10 个 Token：

```text
主流币
中流动性币
低流动性币
历史已停止交易币
```

重建 1m 价格。

---

## Phase P3 — PancakeSwap V3

支持：

```text
PoolCreated
Swap
sqrtPriceX96
tick
liquidity
```

注意：

V3 价格不能直接照搬 V2 reserve 算法。

---

## Phase P4 — USD Resolver

实现：

```text
TOKEN/USDT
TOKEN/USDC
TOKEN/BUSD
TOKEN/FDUSD
TOKEN/WBNB
TOKEN/WETH
```

验收：

任意支持 Token：

```text
GetPrice(token, timestamp)
```

可以返回：

```text
price_usd
route
confidence
```

---

## Phase P5 — Orchestrator

接入现有智能下载器。

实现：

```text
自动选择 SQD/AWS/RPC
失败自动降级
Checkpoint
Retry
Circuit Breaker
```

---

## Phase P6 — 全系统联动

接入：

```text
Token Transfer
Flow Graph
Investigation
Profit Analysis
Export
```

---

# 29. 回填策略

不要一开始下载整个 BSC 所有 DEX。

采用：

```text
ON_DEMAND + HOT_TOKEN + BACKGROUND_BACKFILL
```

### ON_DEMAND

调查出现某 Token：

```text
检查 price coverage
```

没有：

```text
自动创建 PRICE_BACKFILL
```

---

### HOT_TOKEN

高频调查 Token：

```text
永久缓存
```

---

### BACKGROUND_BACKFILL

空闲时：

```text
逐步补全热门池
```

---

# 30. Coverage Index

新增：

```text
token_price_coverage
```

字段：

```text
chain_id
token_address
first_price_at
last_price_at
first_block
last_block
minute_count
trade_count
coverage_ratio
updated_at
```

Price Router 查询前先看 coverage。

禁止每次请求都重新下载。

---

# 31. 防重复下载

Key：

```text
chain_id
token_address
start
end
dataset=PRICE
```

如果已有：

```text
FULL_COVERAGE
```

直接返回。

如果：

```text
PARTIAL
```

只补 gap。

---

# 32. Gap Repair

例如已有：

```text
2023-01-01
→
2023-04-01

缺：
2023-02-12
```

不要全量重跑。

生成：

```text
PRICE_GAP_REPAIR
2023-02-12 only
```

---

# 33. 最新价格更新

历史：

```text
SQD/AWS
```

最新：

```text
RPC
```

建议每：

```text
1 minute
```

更新正在活跃调查的 Token。

但不要求对所有 Token 永久实时轮询。

---

# 34. 性能建议

ClickHouse 查询尽量：

```text
WHERE chain_id = 56
AND token_address = ...
AND minute >= ...
AND minute < ...
```

不要：

```text
WHERE lower(token_address) = lower(...)
```

写入前统一规范地址：

```text
lowercase
```

---

# 35. Decimal 精度

严禁价格核心计算使用：

```text
float64
```

优先：

```text
big.Int
big.Rat
Decimal
```

最终 ClickHouse：

```text
Decimal(38,18)
Decimal(76,30)
```

---

# 36. 时间统一

内部统一：

```text
UTC
```

前端根据用户时区展示。

所有分钟桶使用：

```text
UTC minute
```

---

# 37. 可观测性

Metrics：

```text
price_download_jobs_total
price_download_failed_total
price_swap_rows_total
price_1m_rows_total
price_gap_count
price_resolution_success_rate
price_resolution_latency_ms
price_low_confidence_total
provider_sqd_503_total
provider_rpc_error_total
```

日志：

```text
price-engine.log
price-provider.log
price-resolution.log
price-validation.log
```

---

# 38. Health API

```http
GET /api/price/health
```

返回：

```json
{
  "status": "ok",
  "clickhouse": "ok",
  "binance": "ok",
  "sqd": "degraded",
  "aws": "ok",
  "rpc": "ok"
}
```

价格服务不能因为：

```text
SQD 503
```

整体判定 DOWN。

---

# 39. 前端

新增：

```text
数据管理
→ 历史价格
```

页面：

```text
Price Overview
Coverage
Backfill Jobs
Provider Health
Token Search
Candlestick
Gap Viewer
Confidence Viewer
```

Token 页面显示：

```text
历史覆盖
首次价格
最后价格
1m 行数
成交数
价格源
置信度
数据缺口
```

---

# 40. 调查自动触发规则

当 Investigation/Graph 出现：

```text
Token Transfer
```

且：

```text
historical_price missing
```

执行：

```text
Price Coverage Check
```

如果：

```text
covered
```

直接查询。

否则：

```text
Smart Download Orchestrator
→ PRICE_BACKFILL
```

无需用户确认。

---

# 41. 失败降级

价格来源失败：

```text
DEX_RECONSTRUCTED
↓
CEX_1M
↓
LAST_KNOWN
↓
UNKNOWN
```

不要：

```text
UNKNOWN → 0
```

无法定价必须是：

```text
NULL
```

而不是：

```text
0 USD
```

---

# 42. 安全约束

- API Key 不写日志；
- RPC Key 加密存储；
- 不信任 Token symbol；
- 所有地址校验；
- 限制最大时间窗口；
- 限制单次批量 Token 数；
- SQL 全部参数化；
- 下载文件 checksum；
- staging 数据需校验后进入正式表。

---

# 43. 验收测试

必须覆盖：

### Case A — BNB

```text
BNBUSDT
1m
```

结果：

```text
PASS
```

---

### Case B — TOKEN/USDT

直接稳定币池：

```text
DEX price reconstructed
```

---

### Case C — TOKEN/WBNB

验证：

```text
TOKEN/WBNB × BNB/USDT
```

---

### Case D — 无成交分钟

必须：

```text
is_last_known = 1
```

不能伪造 Trade。

---

### Case E — SQD 503

自动：

```text
retry
fallback
resume
```

任务不能丢失。

---

### Case F — Kill Recovery

中途终止服务。

重新启动：

```text
从 checkpoint 继续
```

---

### Case G — Duplicate

重复回填同一窗口：

```text
duplicate = 0
```

---

### Case H — 资金图

任意 Token Transfer Edge 显示：

```text
Token Amount
Historical USD Value
Price Confidence
```

---

# 44. 最终验收指标

P0：

```text
ClickHouse tables       PASS
Health API              PASS
Binance BNB 1m          PASS
```

P1：

```text
PancakeSwap V2          PASS
TOKEN/USDT              PASS
TOKEN/WBNB              PASS
```

P2：

```text
PancakeSwap V3          PASS
USD Resolver            PASS
```

P3：

```text
Orchestrator            PASS
Checkpoint              PASS
503 Recovery            PASS
```

P4：

```text
Investigation           PASS
Flow Graph              PASS
Transfer Valuation      PASS
Profit Analysis         PASS
```

---

# 45. Codex 实施顺序

建议严格按以下顺序：

```text
01. 建 ClickHouse 表
02. 建 Price Engine package
03. Binance Anchor 1m
04. Price Repository
05. Price API
06. Pool Registry
07. PancakeSwap V2 Decoder
08. Swap Normalizer
09. Stablecoin Direct Price
10. WBNB Route
11. Price 1m Aggregator
12. Confidence Engine
13. Coverage Index
14. Smart Download Orchestrator 接入
15. SQD/AWS/RPC fallback
16. PancakeSwap V3
17. Transfer Enrichment
18. Flow Graph
19. Investigation Agent
20. Profit Engine
21. 全量测试
22. 压力测试
23. 回归测试
```

---

# 46. 最终目标

部署完成后：

输入：

```text
Token:
0x...

Time:
2023-05-13 14:37:26 UTC
```

系统自动：

```text
检查本地价格
↓
有数据
→ ClickHouse 查询

无数据
↓
Pool Discovery
↓
Smart Download
↓
DEX Swap Backfill
↓
Price Reconstruction
↓
USD Resolution
↓
写入 token_price_1m
↓
返回结果
```

最终输出：

```json
{
  "token": "0x...",
  "timestamp": "2023-05-13T14:37:26Z",
  "price_usd": "0.01284",
  "value_source": "DEX_RECONSTRUCTED",
  "route": "TOKEN/WBNB -> BNB/USDT",
  "confidence": 0.94
}
```

并自动用于：

```text
资金流向图
地址画像
大额资金识别
历史资产价值
买卖成本
盈利计算
智能调查
案件报告
```

---

# 47. V1.0 部署结论

本方案的关键原则：

```text
不购买历史价格 API
不依赖单一第三方价格服务
主流币直接使用免费分钟 K 线
冷门币通过历史 DEX Swap 自建价格
所有结果进入 ClickHouse
所有价格保留来源和置信度
无成交不伪造
无价格不写 0
已有数据不重复下载
缺口只补 Gap
```

这使历史价格从“外部 API 能不能查到”升级为系统自己的长期数据资产。
