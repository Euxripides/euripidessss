# Explorer 历史 USDT 估值与前端展示 V1.0

## 1. 目标

在现有 BSC Explorer / ClickHouse / Historical Price Engine 基础上，为所有 Token Transfer、Swap、资金流边和大额活动增加“交易发生时的 USDT 价值”。

核心公式：

```text
historical_value_usdt
=
token_amount
×
historical_token_price_usdt_at_tx_time
```

例如：

```text
2023-05-13 14:37:26
13,826,391 FIST

当时价格：
1 FIST = 0.01284 USDT

历史价值：
177,528.85 USDT
```

前端必须展示：

```text
13,826,391 FIST
≈ 177,528.85 USDT
```

注意：不能使用当前 Token 价格代替历史价格。

## 2. 覆盖页面

首期必须覆盖：

1. Explorer 最近交易
2. Explorer 代币转账
3. Explorer 大额活动
4. 地址详情 - Token Transfers
5. Token 详情 - Transfers
6. 智能调查交易列表
7. 资金流向图 Edge
8. 导出结果

统一复用同一字段和同一估值服务。

## 3. API 字段

所有 Token Transfer API 增加：

```json
{
  "token_address": "0x...",
  "token_symbol": "FIST",
  "token_amount": "13826391",
  "block_time": "2023-05-13T14:37:26Z",
  "historical_price_usdt": "0.01284",
  "historical_value_usdt": "177528.85",
  "price_timestamp": "2023-05-13T14:37:00Z",
  "price_source": "DEX_RECONSTRUCTED",
  "price_route": "FIST/WBNB -> BNB/USDT",
  "price_confidence": 0.94,
  "price_type": "TRADED",
  "price_age_seconds": 26,
  "valuation_status": "VALUED"
}
```

无价格时：

```json
{
  "historical_price_usdt": null,
  "historical_value_usdt": null,
  "price_source": "UNKNOWN",
  "price_confidence": 0,
  "valuation_status": "NO_PRICE"
}
```

禁止将 NULL 转成 0 USDT。

## 4. 统一 USDT 估值

第一阶段统一使用 USDT。

- USDT：amount × 1
- USDC / BUSD / FDUSD：优先取当时与 USDT 的真实交叉价格
- 没有交叉价时允许 1.0，但必须 `price_source=STABLECOIN_PEG`
- 普通 Token：走历史分钟价格路由

## 5. 时间规则

交易时间：

```text
2023-05-13 14:37:26
```

映射：

```text
2023-05-13 14:37:00
```

即：

```text
toStartOfMinute(block_time)
```

优先级：

```text
P0 Exact Minute Actual Swap Price
P1 Exact Minute Canonical VWAP
P2 Last Known Price
P3 TOKEN/WBNB × BNB/USDT
P4 TOKEN/WETH × ETH/USDT
P5 Unknown
```

## 6. 后端必须避免 N+1

禁止每行调用一次价格接口。

错误：

```text
100 Token Transfers
→ 100 price API calls
```

正确：

```text
Explorer 查询 100 条
↓
提取唯一 (token_address, minute)
↓
PriceBatchResolver
↓
一次 ClickHouse 批量查询
↓
内存合并
↓
返回前端
```

## 7. Go 结构

```go
type HistoricalValuation struct {
    PriceUSDT      *decimal.Decimal `json:"historical_price_usdt"`
    ValueUSDT      *decimal.Decimal `json:"historical_value_usdt"`
    PriceTimestamp *time.Time       `json:"price_timestamp"`

    Source          string           `json:"price_source"`
    Route           string           `json:"price_route"`
    PriceType       string           `json:"price_type"`
    Status          string           `json:"valuation_status"`

    Confidence      float32          `json:"price_confidence"`
    AgeSeconds      int64            `json:"price_age_seconds"`
}
```

新增：

```text
internal/price/batch_resolver.go
internal/explorer/valuation_service.go
```

## 8. PriceBatchResolver

```go
type PriceKey struct {
    ChainID      uint32
    TokenAddress string
    Minute       time.Time
}

type PricePoint struct {
    PriceUSDT  decimal.Decimal
    Timestamp  time.Time
    Source     string
    Route      string
    PriceType  string
    Confidence float32
    AgeSeconds int64
}

func ResolvePricesBatch(
    ctx context.Context,
    keys []PriceKey,
) (map[PriceKey]PricePoint, error)
```

## 9. ClickHouse 查询

```sql
SELECT
    chain_id,
    token_address,
    minute,
    close,
    vwap,
    price_source,
    confidence,
    is_interpolated,
    is_last_known
FROM token_price_1m
WHERE chain_id = 56
  AND (token_address, minute) IN (...)
```

大量 key 时使用临时表或 VALUES 子查询，避免不受控 SQL 拼接。

## 10. 历史估值计算

```go
valueUSDT := tokenAmount.Mul(priceUSDT)
```

核心金额与价格计算禁止使用 float64。

## 11. Last Known 规则

当分钟无成交允许使用最近成交价，但必须：

```text
price_type = LAST_KNOWN
price_age_seconds > 0
```

配置：

```yaml
price:
  last_known:
    enabled: true
    max_age: 24h
```

超出窗口返回 NO_PRICE。

## 12. Explorer 最近交易 UI

建议显示：

```text
交易哈希 | 时间 | From | To | Token | 数量 | 当时价值
```

示例：

```text
0x7327...8c
0xd3ae...c735 → 0x98aa...1b20

13,826,391 FIST
≈ 177,528.85 USDT
```

## 13. Token Transfer 表格

建议列：

| 时间 | TxHash | From | To | Token | 数量 | 当时价格 | 当时价值 |
|---|---|---|---|---|---:|---:|---:|
| 14:37:26 | 0x7327... | 0xd3... | 0x98... | FIST | 13.83M | 0.01284 | 177.53K USDT |

价格 hover：

```text
当时价格：0.01284 USDT/FIST
价格分钟：14:37 UTC
来源：DEX_RECONSTRUCTED
路径：FIST/WBNB → BNB/USDT
置信度：94%
```

## 14. 大额活动必须改成 USDT 判断

禁止按 Token 数量判断“大额”。

改成：

```text
historical_value_usdt >= threshold
```

推荐：

```yaml
explorer:
  large_activity:
    min_usdt: 100000
```

分级：

```text
≥ 100K USDT
≥ 500K USDT
≥ 1M USDT
≥ 10M USDT
```

## 15. 首页统计升级

当前：

```text
最新区块
交易
代币转账
完整覆盖区间
```

建议追加：

```text
24h 历史资金流量
24h 大额活动
已估值转账比例
历史价格覆盖率
```

## 16. 无价格前端状态

无价格：

```text
—
```

Tooltip：

```text
暂无该时间点历史价格
```

回填中：

```text
价格回填中
```

禁止：

```text
0 USDT
```

## 17. 自动回填

当 Explorer 查到历史价格缺失：

```text
Coverage Check
↓
Missing
↓
PRICE_BACKFILL
↓
Smart Download Orchestrator
↓
SQD / AWS / RPC
↓
DEX Price Reconstruction
↓
ClickHouse
```

当前页面请求不要阻塞等待完整回填。

首次返回：

```text
historical_value_usdt = null
valuation_status = BACKFILLING
```

后续刷新自动补齐。

## 18. 防任务风暴

首页一次出现多个未知 Token 时：

```text
不要创建大量重复任务
```

必须：

```text
Token 去重
+ 时间窗口合并
+ Backfill Batch
```

配置：

```yaml
price:
  backfill:
    max_concurrent_tokens: 8
    dedup_window: 10m
```

## 19. 资金流图

Edge 从：

```text
13.8M FIST
```

升级为：

```text
13.8M FIST
177.5K USDT
```

点击 Edge：

```text
Transfer
13,826,391 FIST

Historical Price
0.01284 USDT

Historical Value
177,528.85 USDT

Price Route
FIST/WBNB → BNB/USDT

Confidence
94%
```

## 20. 地址详情

增加：

```text
历史累计流入价值
历史累计流出价值
历史净流量
最大单笔流入
最大单笔流出
```

全部使用交易发生时的历史 USDT 价值。

## 21. 大额排行榜

按：

```sql
ORDER BY historical_value_usdt DESC
```

不要按原始 token amount 排序。

## 22. 导出

增加字段：

```text
historical_price_usdt
historical_value_usdt
price_source
price_route
price_confidence
price_type
price_timestamp
valuation_status
```

## 23. 前端组件

新增：

```text
HistoricalValue.tsx
HistoricalPrice.tsx
PriceConfidenceBadge.tsx
PriceTooltip.tsx
LargeValueBadge.tsx
```

格式化：

```text
987.25 USDT
12.4K USDT
3.82M USDT
1.24B USDT
```

Hover 始终显示完整数值。

## 24. 性能目标

Explorer 首页 100 条 Transfer + 历史估值：

```text
P50 < 100ms
P95 < 250ms
```

已有价格时价格层不应将页面拖慢到秒级。

## 25. 缓存

缓存 Key：

```text
(chain_id, token_address, minute)
```

历史价格不可变，适合长期缓存。

推荐本地 LRU：

```text
100K ~ 1M entries
```

## 26. 估值状态

统一枚举：

```text
VALUED
BACKFILLING
NO_LIQUIDITY
NO_POOL
NO_PRICE
LOW_CONFIDENCE
```

前端只根据状态渲染，不自行推断。

## 27. Transfer 与 Swap 区别

Token Transfer：

```text
使用当分钟 Canonical Price 做历史价值估算
```

Swap：

```text
优先使用该交易真实成交价
price_source = ACTUAL_SWAP
```

普通 Transfer 不得误判为买卖。

## 28. 验收

### Case A — USDT
100,000 USDT 必须估值为约 100,000 USDT。

### Case B — TOKEN/USDT
amount × 当分钟价格正确。

### Case C — TOKEN/WBNB
TOKEN/WBNB × BNB/USDT 正确。

### Case D — 无成交分钟
使用 LAST_KNOWN，并返回 age。

### Case E — 无价格
显示 `—`，不能显示 0。

### Case F — 大额活动
必须按 USDT 价值而不是 Token 数量排序。

### Case G — 100 行性能
价格查询不能出现 100 次 N+1 调用。

### Case H — Graph
Edge 同时展示 Token Amount + Historical USDT Value。

## 29. Codex 实施顺序

```text
01. 扩展 Token Transfer DTO
02. 实现 PriceBatchResolver
03. 实现 Explorer Valuation Service
04. ClickHouse Batch Price Query
05. Stablecoin Fast Path
06. Last Known Resolver
07. Explorer API 扩展
08. HistoricalValue 前端组件
09. 最近交易接入
10. Token Transfers 接入
11. 大额活动改用 historical_value_usdt
12. 地址详情接入
13. Graph Edge 接入
14. Investigation 接入
15. Export 接入
16. Coverage / Backfill 状态
17. LRU Cache
18. 压测与验收
```

## 30. 最终效果

前端从：

```text
13,826,391 FIST
```

升级成：

```text
13,826,391 FIST
≈ 177,528.85 USDT
```

并且 Explorer、地址详情、资金流图、智能调查、导出全部使用同一个“交易发生时历史 USDT 估值”。
