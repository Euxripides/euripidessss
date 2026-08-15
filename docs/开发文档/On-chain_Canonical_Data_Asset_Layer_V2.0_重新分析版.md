# On-chain Canonical Data Asset Layer V2.0
## 链上最终数据资产语义层 / OKLink 级数据完整性升级方案

> 当前阶段基线：ClickHouse Data Plane 已全部完成并投入生产。
>
> - Writer / Explorer / Analytics / Graph / Investigation / Export 已全部接入 ClickHouse
> - `datasource=clickhouse`
> - `duckdb_reader_enabled=false`
> - Parquet 仅保留 Writer 合并/临时交换能力，不再承担在线 Reader
> - ClickHouse 26.7.3.19
> - 13 张生产表
> - ClickHouse 固定根目录：`E:\database\clickhouse`
> - Transactions / Token Transfers 10K、100K、1M 写入验收通过
> - 100,001 Activity / 501 页：0 重复、0 遗漏
> - 50M 查询矩阵通过，首屏约 38–94ms
> - 5,000,000 行流式导出约 21.96 秒
> - Dual Read、恢复、失败重试、测试、Vet、构建、安全扫描均通过
>
> **本版本不再解决“数据库能不能用”的问题。**
>
> 本版本解决：
>
> **“数据库里的数据是否已经是可以直接作为 Explorer、调查、统计、导出最终资产的数据。”**

---

# 1. 重新分析后的核心结论

下一阶段不应立即把主要精力投入“照着 OKLink 重画前端”。

当前真正的产品瓶颈已经从：

```text
数据获取
↓
数据库性能
```

转移到：

```text
数据语义完整性
↓
数据身份统一
↓
数据质量
↓
可解释统计
```

也就是说：

```text
ClickHouse 已经解决“存”
ClickHouse API 已经解决“查”

下一步必须解决“数据到底代表什么”
```

最终目标不是：

```text
Raw Blockchain Data in ClickHouse
```

而是：

```text
Canonical Explorer-Ready Data Assets
```

即数据库查询出来后，前端不需要再做二次猜测、RPC 补字段、临时解析、symbol 映射或业务拼接。

---

# 2. 最终目标

任何一笔交易进入系统后，最终至少可以得到：

```text
Transaction
├── 基础交易信息
├── Receipt
├── Status
├── Method
├── Native Value
├── Fee
├── Token Transfers
├── Internal Transactions
├── NFT Transfers
├── Contract Creation
├── Parsed Events
├── Contract Interaction
├── Token Identity
├── Address / Entity Labels
├── Historical USD Value
└── Data Provenance
```

任何一个地址最终可以得到：

```text
Address
├── EOA / Contract
├── First Seen / Last Seen
├── Native Balance
├── Token Balances
├── Transactions
├── Token Transfers
├── Internal Transactions
├── Contract Creations
├── Counterparties
├── Entity Interactions
├── Inflow / Outflow / Netflow
├── Token Statistics
├── Historical Activity
├── Retention
├── Pass-through
└── Investigation Features
```

这时再做 OKLink 风格 UI，前端只是：

```text
Render(Data)
```

而不是：

```text
Render
+ Parse
+ Guess
+ RPC
+ Fix Symbol
+ Calculate
```

---

# 3. 本阶段第一原则：不推翻现有 13 张表

当前 Data Plane 已完成生产验收。

因此禁止为了“数据看起来更完整”直接：

```text
重建 ClickHouse
重构全部事实表
重新开启 DuckDB
重写 Writer
```

正确策略：

```text
审计现有 13 表
↓
识别字段/语义缺口
↓
优先扩字段、Dimension、Registry、Materialized View
↓
必要时才新增表
```

---

# 4. Phase 0：13 张生产表语义审计

先生成：

```text
docs/clickhouse_data_asset_semantic_audit.md
```

逐表记录：

```text
表名
用途
主数据来源
排序键
分区键
唯一事件逻辑
当前字段
缺失字段
是否 Explorer Ready
是否 Analytics Ready
是否 Investigation Ready
是否 Export Ready
```

每张表评分：

```text
L0 RAW
L1 PARSED
L2 NORMALIZED
L3 ENRICHED
L4 EXPLORER_READY
L5 INVESTIGATION_READY
```

目标：

```text
Transactions              >= L4
Token Transfers           >= L4
Internal Transactions     >= L4
Contract Creations        >= L4
Contracts                 >= L4
Address Activity          >= L5

核心聚合数据              >= L5
```

---

# 5. Canonical Transaction

最终 Transaction 对象必须具有稳定语义。

建议统一 API Model：

```go
type CanonicalTransaction struct {
    ChainID uint64

    BlockNumber uint64
    BlockHash string
    BlockTime time.Time

    TxHash string
    TxIndex uint32

    FromAddress string
    ToAddress *string

    Nonce uint64

    ValueRaw string
    ValueDecimal string

    Input string

    MethodID string
    MethodName string
    MethodConfidence string

    TxType uint8

    GasLimit uint64
    GasUsed uint64

    GasPrice string
    EffectiveGasPrice string

    FeeNative string
    FeeUSD *string

    Status string

    IsContractCreation bool
    CreatedContractAddress *string

    ErrorMessage *string

    ParserVersion string
    SchemaVersion uint32
}
```

---

# 6. Transaction Status 必须来自 Receipt

禁止：

```text
拿到 Transaction
→ 默认 Success
```

必须：

```text
Transaction
+
Receipt
```

才能生成最终：

```text
SUCCESS
FAILED
UNKNOWN
```

数据库中：

```text
status
```

不能用：

```text
0 / 1
```

让前端自行猜。

API 可以额外返回 raw status。

---

# 7. Method Intelligence

要达到 Explorer 体验，Method 不能只是：

```text
0xa9059cbb
```

必须提供：

```text
method_id = 0xa9059cbb
method_name = transfer
method_display = Transfer
method_confidence = HIGH
```

解析顺序：

```text
1 已验证 ABI
2 本地 ABI Registry
3 已知 Protocol ABI
4 Method Signature Registry
5 4-byte Signature DB
6 Raw selector
```

如果冲突：

```text
method_name = ambiguous
```

并保存：

```text
candidate_signatures
```

不要随机选择一个名字。

---

# 8. Method Registry

新增逻辑组件：

```text
MethodRegistry
```

建议数据：

```text
method_id
canonical_signature
display_name
source
confidence
updated_at
```

典型：

```text
0xa9059cbb
transfer(address,uint256)
Transfer
ERC20
HIGH
```

---

# 9. Token Identity：本阶段最高优先级之一

Token 永远不能由：

```text
symbol
```

唯一标识。

Canonical Token ID：

```text
chain_id + contract_address
```

例如 BSC USDT：

```text
56
+
0x55d398326f99059ff775485246999027b3197955
```

---

# 10. Token Metadata 最终字段

每个 Token 最少：

```text
chain_id
contract_address

name
symbol
decimals

token_standard

logo_uri
logo_hash
logo_source

verified
spam

official_website

first_seen_block
first_seen_time

metadata_source
metadata_confidence

metadata_updated_at
```

---

# 11. Token Metadata Resolver

统一：

```text
TokenMetadataResolver
```

禁止：

```text
Explorer API
→ 临时 RPC symbol()
```

所有 Token Metadata 必须先进入 Registry。

优先级：

```text
P0 Local Manual Override
P1 Verified Local Registry
P2 Official Token List
P3 Trusted Provider
P4 On-chain name/symbol/decimals
P5 Fallback Address Identity
```

---

# 12. Token Symbol 冲突

例如存在两个：

```text
USDT
```

必须：

```text
USDT
0x55d398...

USDT
0xFake...
```

前端不能把两个 Token 合并。

假币可以显示：

```text
USDT
Unverified
```

---

# 13. Token Logo

目标不是：

```text
“symbol 差不多”
```

而是：

```text
Token Identity 与主流 Explorer 一致
```

Logo Resolver：

```text
contract
↓
Token Registry
↓
Verified Logo
```

禁止：

```text
symbol == USDT
→ 使用 USDT Logo
```

这是错误设计。

必须：

```text
chain_id + contract_address
→ Logo
```

---

# 14. Token Logo Cache

图标不要存 ClickHouse 二进制。

建议：

```text
E:\database\assets\tokens\
```

或项目统一的 E 盘资产目录。

例如：

```text
E:\database\assets\tokens\56\
└── 0x55d398326f99059ff775485246999027b3197955.png
```

数据库：

```text
logo_uri
logo_hash
```

---

# 15. Token Metadata History

为了避免 Metadata 被覆盖后无法审计：

建议：

```text
token_metadata_history
```

或者 PostgreSQL Metadata History。

记录：

```text
old_symbol
new_symbol

old_name
new_name

source
observed_at
```

尤其用于：

```text
诈骗 Token
改名 Token
伪装 Token
```

---

# 16. Contract Creation 语义

Contract Creation 必须区分：

```text
CREATE
CREATE2
Factory Created
Proxy Created
Token Created
```

最终：

```text
creator
factory
contract
creation_tx

creation_type

bytecode_hash
runtime_bytecode_hash
```

---

# 17. Contract Identity

Canonical Contract：

```text
chain_id
contract_address

creator_address
factory_address

creation_tx
creation_block
creation_time

bytecode_hash
runtime_bytecode_hash

contract_name

verified

is_proxy
proxy_type

implementation_address

abi_source
```

---

# 18. Proxy Detection

至少支持：

```text
EIP-1967
Transparent Proxy
UUPS
Beacon Proxy
Minimal Proxy / EIP-1167
```

后续 Explorer：

```text
Contract
Proxy

Implementation:
0x...
```

---

# 19. Contract Family

基于：

```text
runtime_bytecode_hash
```

建立：

```text
Contract Family
```

可直接用于调查：

```text
同一诈骗项目批量部署合约
同一 Factory 生成合约
同一模板 Token
```

统计：

```text
Same Runtime Hash Contracts
Same Creator Contracts
Same Factory Contracts
```

---

# 20. Internal Transaction 语义

内部交易不能只保留：

```text
from
to
value
```

至少：

```text
trace_address
trace_index

call_type
depth

parent_trace

from
to

value

input
output

gas
gas_used

success
error
```

---

# 21. Zero-value Internal Call

Explorer 可默认：

```text
Hide Zero Value
```

但是数据库必须完整保留。

禁止 Writer 阶段删除：

```text
value = 0
```

因为：

```text
合约调用关系
代理调用
Factory 调用
调查调用链
```

仍然有价值。

---

# 22. Parsed Events

只解析 Transfer 不够。

最终需要：

```text
parsed_events
```

至少逐步支持：

```text
Approval
Swap
Mint
Burn
Deposit
Withdraw
OwnershipTransferred
PairCreated
PoolCreated
Sync
Transfer
```

---

# 23. Event Decoder

统一：

```text
EventDecoder
```

优先：

```text
Verified ABI
Protocol ABI
Local ABI Registry
topic0 registry
Raw Event
```

结果：

```text
event_name
event_signature
decoded_fields
decoder_source
decoder_confidence
```

---

# 24. DEX Semantics

如果要让统计真正超过普通 Explorer：

不能长期停留在：

```text
Token Transfer
```

必须把典型 Swap 解析成：

```text
DEX_SWAP
```

Canonical Swap：

```text
tx_hash

protocol
router
pool

trader

token_in
amount_in

token_out
amount_out

usd_value
```

---

# 25. Bridge Semantics

解析：

```text
BRIDGE_DEPOSIT
BRIDGE_WITHDRAW
BRIDGE_SEND
BRIDGE_RECEIVE
```

字段：

```text
bridge
source_chain
destination_chain

source_address
destination_address

token
amount
usd_value
```

这会直接提升跨链资金调查。

---

# 26. CEX 不来自链上 Event

交易所身份不是通过：

```text
Event Decode
```

解决。

需要：

```text
Entity Registry
+
Address Labels
```

Canonical：

```text
Entity
├── Binance
├── OKX
├── Bybit
└── ...
```

地址：

```text
Address
→ Entity
→ Role
```

例如：

```text
0x...
Entity = Binance
Role = Deposit
Confidence = HIGH
```

---

# 27. Address Label 数据模型

至少：

```text
address
chain_id

label_name
label_type

entity_id
entity_role

source

confidence

first_seen
last_verified

evidence
```

label_type：

```text
ENTITY
BEHAVIOR
SYSTEM
USER
CASE
RISK
```

---

# 28. Entity Intelligence

建议下一阶段重点建立：

```text
Entity Registry
```

实体：

```text
Binance
OKX
Bybit
Uniswap
PancakeSwap
Stargate
LayerZero
```

实体下：

```text
多个地址
多个合约
多个角色
```

不能：

```text
一个 label 字符串
```

代替实体模型。

---

# 29. Historical Price：高级统计的基础

如果需要：

```text
USD inflow
USD outflow
PnL
最大单笔美元价值
历史 Token 流量
```

不能用：

```text
当前价格 × 历史数量
```

必须使用：

```text
Historical Price
```

---

# 30. Price Model

建议：

```text
token_prices
```

字段：

```text
chain_id
token_address

timestamp_bucket

price_usd

source
confidence
```

初期粒度：

```text
1 minute
```

或：

```text
5 minute
```

长期冷数据可降：

```text
1 hour
```

---

# 31. Stablecoin

稳定币也不能永久假设：

```text
1 USDT = 1 USD
```

正确：

```text
历史价格优先
```

只有价格缺失时可：

```text
stablecoin fallback = 1
```

但必须标记：

```text
price_source = PEG_FALLBACK
```

---

# 32. USD Value Provenance

Token Transfer：

```text
usd_value
```

必须知道：

```text
price
price_timestamp
price_source
price_confidence
```

避免用户看到：

```text
$100,000
```

却不知道是历史价还是当前价。

---

# 33. Address Balance

Address Balance 分两类：

```text
Current Balance
Historical Balance
```

Current：

```text
RPC / indexed state
```

Historical：

```text
reconstructed
```

不能混用。

API：

```json
{
  "balance": "123",
  "as_of_block": 123456,
  "source": "RPC",
  "freshness": "LIVE"
}
```

---

# 34. Address Activity Canonicalization

现有 `address_activity` 已完成大规模分页验收。

下一步不是重建。

而是保证：

```text
每一条 Activity
```

拥有统一：

```text
activity_type
direction
counterparty
token
amount
usd_value
entity
method
status
```

---

# 35. Activity Type

建议固定 Enum：

```text
NATIVE_TRANSFER
TOKEN_TRANSFER
NFT_TRANSFER
INTERNAL_TRANSFER

CONTRACT_CALL
CONTRACT_CREATE

DEX_SWAP
BRIDGE

APPROVAL
OTHER
```

禁止前端自己根据字段猜：

```text
这是不是 Token Transfer
```

---

# 36. Direction

数据库统一：

```text
IN
OUT
SELF
```

对 Contract Call：

可增加：

```text
CALL
```

但资金方向仍按资产移动解释。

---

# 37. Counterparty Semantics

Counterparty 不只是：

```text
另一个 address
```

最终：

```text
counterparty_address
counterparty_entity
counterparty_role
counterparty_label
```

这样 Analytics 可以直接：

```text
Top Binance
Top CEX
Top DEX
```

---

# 38. Address Summary V2

现有 Analytics 可进一步统一生成：

```text
AddressSummaryV2
```

字段：

```text
tx_count

in_count
out_count

token_transfer_count
internal_transfer_count

unique_counterparties

first_seen
last_seen
active_days

total_in_usd
total_out_usd
netflow_usd

largest_in_usd
largest_out_usd

cex_in_usd
cex_out_usd

dex_volume_usd
bridge_volume_usd

contract_created_count
```

---

# 39. Counterparty Statistics V2

必须至少：

```text
Top Sources By Amount
Top Destinations By Amount
Top Counterparties By Count
Top Netflow Counterparties
```

附：

```text
Entity
Label
Share
```

---

# 40. Concentration

新增：

```text
Top1
Top5
Top10
```

分别：

```text
Inflow Concentration
Outflow Concentration
```

这些是普通 Explorer 很少提供、但调查非常有用的统计。

---

# 41. Retention / 资金沉淀

收到资金后：

```text
1H
6H
24H
7D
30D
```

仍未转出的部分。

输出：

```text
received_usd
retained_1h
retained_6h
retained_24h
retained_7d
retained_30d
```

---

# 42. Fast Pass-Through

识别：

```text
A → X → B
```

X 收到资金后短时间继续转出。

统计：

```text
5m
30m
1h
6h
24h
```

可输出：

```text
pass_through_ratio
```

这对识别：

```text
中转
归集
自动分发
```

很有价值。

注意：

```text
指标 ≠ 犯罪结论
```

UI 只能描述行为。

---

# 43. Historical Snapshot

高级能力建议增加：

```text
as_of
```

例如：

```text
2026-05-01 时
这个地址是什么状态？
```

用于：

```text
案件历史还原
标签历史
余额历史
资金历史
```

这是后期比普通 Explorer 更强的重要方向。

---

# 44. Data Provenance

所有最终数据都必须可追溯。

建议统一字段：

```text
source_provider
source_type

download_job_id

parser_version
normalizer_version
schema_version

ingested_at
updated_at
```

Enrichment：

```text
metadata_source
price_source
label_source
decoder_source
```

---

# 45. Reparse Capability

Parser 升级后不能要求：

```text
重新下载三年数据
```

优先：

```text
已有 Raw / Parsed Source
↓
Reparse
↓
新 Canonical Data
```

建议：

```text
Reparse Job
```

可以按：

```text
chain
block range
dataset
parser version
```

执行。

---

# 46. Re-enrichment

Token Metadata、Label、Price、ABI 更新时：

不能重跑 Downloader。

执行：

```text
Re-enrichment Job
```

例如：

```text
只更新 Entity Labels
```

不重写 Transaction Raw Data。

---

# 47. Semantic Completeness Score

新增系统级指标：

```text
Semantic Completeness
```

例如：

```text
Transactions with Status       100%
Transactions with Method        96%
Transfers with Token Metadata   99.8%
Transfers with Historical Price 91%
Contracts with Creator         100%
Contracts with ABI              41%
Addresses with Entity Label      8%
Events Decoded                  76%
```

这比：

```text
数据库有多少行
```

更能说明产品成熟度。

---

# 48. Data Quality Dashboard

新增后台页面：

```text
Data Quality
```

显示：

```text
Dataset
Rows
Coverage
Semantic Completeness
Invalid
Unknown
Unpriced
Unlabeled
Decode Failed
Last Updated
```

---

# 49. Token Quality Dashboard

显示：

```text
Known Tokens
Verified
Unverified
Missing Symbol
Missing Decimals
Missing Logo
Metadata Conflict
Spam Tokens
```

---

# 50. Contract Quality Dashboard

```text
Contracts

Creator Known
Creation Tx Known

Proxy Detected
Implementation Known

ABI Known
Verified

Token Detected
```

---

# 51. Decoder Quality Dashboard

```text
Transactions With Input
Known Method
Unknown Method

Logs
Decoded Events
Unknown topic0
ABI Decode Failures
```

---

# 52. Price Quality Dashboard

```text
Transfers requiring price
Priced

Historical Price
Fallback Price
No Price

Coverage by date
Coverage by token
```

---

# 53. Explorer API Contract V2

前端不直接依赖 ClickHouse 字段名称。

必须定义：

```text
Canonical API DTO
```

例如：

```json
{
  "tx_hash": "0x...",
  "time": "2026-08-09T00:00:00Z",
  "method": {
    "id": "0xa9059cbb",
    "name": "Transfer",
    "confidence": "HIGH"
  },
  "from": {
    "address": "0x...",
    "label": "Binance",
    "entity_type": "CEX"
  },
  "to": {
    "address": "0x..."
  }
}
```

这样未来 ClickHouse Schema 内部变化：

```text
前端不受影响
```

---

# 54. Token DTO

统一：

```json
{
  "chain_id": 56,
  "contract": "0x55d398...",
  "symbol": "USDT",
  "name": "Tether USD",
  "decimals": 18,
  "standard": "BEP20",
  "verified": true,
  "logo_uri": "/assets/tokens/56/0x55d398....png"
}
```

---

# 55. Amount DTO

禁止前端自己：

```text
raw / 10^decimals
```

后端提供：

```json
{
  "raw": "100000000000000000000",
  "decimal": "100",
  "formatted": "100.00"
}
```

---

# 56. USD DTO

建议：

```json
{
  "usd_value": "100023.12",
  "price_usd": "1.0002312",
  "price_time": "...",
  "price_source": "...",
  "price_confidence": "HIGH"
}
```

---

# 57. Frontend 的真正启动条件

只有以下 P0 达标后，再进行：

```text
Explorer Intelligence UI
```

P0：

```text
Transaction Status            >= 99.99%
Method Resolution             >= 95% 可解析调用
Token Metadata                >= 99.9% 主流资产
Token Decimals                >= 99.99%
Contract Creation             >= 99.99%
Address Activity Consistency  = 100%
Historical USD Coverage       达到设定目标
```

---

# 58. 为什么前端应该后做

如果现在直接照 OKLink 做：

页面可能有：

```text
Method
Token
Logo
USD
Contract
Entity
```

但是后端返回：

```text
unknown
USDT?
logo missing
USD using current price
contract type unknown
no label
```

最终只能得到：

```text
“长得像 OKLink”
```

而不是：

```text
“数据达到 OKLink 级别”
```

---

# 59. 本阶段 P0

## P0-1
13 表 Semantic Audit。

## P0-2
Canonical Transaction。

## P0-3
Receipt / Status 完整性。

## P0-4
Method Registry。

## P0-5
Token Registry。

## P0-6
Token Logo Identity。

## P0-7
Contract Creation / Contract Identity。

## P0-8
Canonical Address Activity。

## P0-9
Data Provenance。

## P0-10
Semantic Completeness Dashboard。

---

# 60. P1

```text
ABI Registry
Event Decoder
Proxy Detection
Contract Family

Entity Registry
Address Label

Historical Price
USD Provenance

Address Summary V2
Counterparty V2
```

---

# 61. P2

```text
DEX Semantic Events
Bridge Semantic Events

Retention
Fast Pass-Through

Historical Snapshot
Reparse
Re-enrichment

Advanced Investigation Features
```

---

# 62. 验收 Case A：USDT

输入真实 BSC USDT：

```text
0x55d398326f99059ff775485246999027b3197955
```

必须稳定返回：

```text
Token Identity
Name
Symbol
Decimals
Logo
Verified
```

随机假 USDT：

```text
不得复用真实 USDT Logo/Identity
```

---

# 63. 验收 Case B：Transfer

ERC20 Transfer：

必须：

```text
tx status
method
token identity
from
to
raw amount
decimal amount
historical USD
```

均可直接由 API 返回。

前端不执行 RPC。

---

# 64. 验收 Case C：Contract Creation

CREATE：

```text
Transaction
Contract Creation
Contract
Address Activity
```

四处语义一致。

---

# 65. 验收 Case D：Failed Transaction

失败交易：

```text
status = FAILED
```

不能：

```text
Token Transfer 被误认成功
```

需要明确区分：

```text
receipt logs
trace results
```

---

# 66. 验收 Case E：Method

已知：

```text
0xa9059cbb
```

返回：

```text
Transfer
HIGH
```

冲突 selector：

```text
不能随机选签名
```

---

# 67. 验收 Case F：Price

历史：

```text
2025-01-01
100 TOKEN
```

USD 必须使用：

```text
2025-01-01 附近历史价格
```

不是：

```text
2026 当前价格
```

---

# 68. 验收 Case G：Re-enrichment

新增 Binance Label 后：

```text
不重新下载交易
```

只执行：

```text
Label Re-enrichment
```

Explorer / Analytics / Graph 均能读取新 Entity。

---

# 69. 验收 Case H：Parser Upgrade

Parser：

```text
v2 → v3
```

允许：

```text
指定 Block Range Reparse
```

并追踪：

```text
parser_version
```

---

# 70. 验收 Case I：Semantic Quality

后台必须能回答：

```text
“现在数据库里有多少 Token Transfer？”
```

同时也能回答：

```text
“其中多少有正确 Token Metadata？”
“多少有历史 USD？”
“多少地址有 Entity？”
“多少 Method 已识别？”
```

---

# 71. 本阶段性能原则

Semantic Enrichment 不得破坏当前已验证的：

```text
50M Query Performance
```

Enrichment 设计优先：

```text
Dimension Join
Dictionary
Materialized View
Precomputed Fields
```

避免每次 Explorer Query：

```text
RPC
HTTP API
复杂外部 Join
```

---

# 72. ClickHouse Dictionary

对于小型高频维表可考虑：

```text
Token Registry
Entity Registry
Method Registry
```

使用 ClickHouse Dictionary 或应用内 Cache。

目的：

```text
避免大事实表反复复杂 JOIN
```

是否使用 Dictionary 必须以 Benchmark 为准。

---

# 73. Source of Truth 分层

正式定义：

```text
Raw Source
    ↓
Canonical Facts
    ↓
Metadata / Dimensions
    ↓
Derived Analytics
```

其中：

```text
Canonical Facts
```

是链上事实。

```text
Metadata
```

允许更新。

例如：

```text
Transaction Hash
```

不会变。

但是：

```text
Address Label
Token Logo
ABI
Entity
```

可能变。

必须分层。

---

# 74. 不要把 Enrichment 全部写死到事实表

如果 Binance 地址标签更新：

不应该重写：

```text
50M Token Transfers
```

正确：

```text
Fact.from_address
↓
Entity Dimension
```

只有明确为性能需要的字段才允许 Materialize。

---

# 75. 推荐最终数据资产架构

```text
                     Provider
                        │
                        ▼
                 Parser / Normalizer
                        │
                        ▼
              Canonical Fact Layer
             ┌──────────┼──────────┐
             │          │          │
             ▼          ▼          ▼
       Transactions Transfers    Traces
             │          │          │
             └──────────┼──────────┘
                        │
                        ▼
                Semantic Enrichment
       ┌────────┬────────┬────────┬────────┐
       │        │        │        │        │
       Token   ABI     Entity   Price   Contract
       Registry Registry Registry History Intelligence
       │        │        │        │        │
       └────────┴────────┴────────┴────────┘
                        │
                        ▼
                Derived Analytics
        ┌───────────────┼────────────────┐
        │               │                │
   Address Stats   Counterparty      Fund Flow
        │               │                │
        └───────────────┼────────────────┘
                        │
                        ▼
              Canonical Explorer API
                        │
         ┌──────────────┼───────────────┐
         │              │               │
      Explorer      Investigation      Export
```

---

# 76. 下一阶段顺序

正确顺序建议：

```text
现在
│
├─ 1. Canonical Data Asset Layer V2.0
│
├─ 2. Entity & Metadata Intelligence
│
├─ 3. Historical Price / Advanced Analytics
│
├─ 4. Explorer API Contract V2
│
└─ 5. Explorer Intelligence UI
```

而不是：

```text
现在
↓
直接重画前端
```

---

# 77. Explorer UI 最终定位

当本阶段完成后，再实现前端：

```text
OKLink 的浏览效率
+
Nansen 的地址画像
+
你自己的调查 / 资金流能力
```

核心优势：

```text
Explorer
↔
Fund Flow
↔
Investigation
↔
Data Assets
↔
Export
```

---

# 78. 当前项目状态重新定义

已经完成：

```text
Stage A Data Acquisition
Stage B Data Plane
Stage C ClickHouse Production
```

当前进入：

```text
Stage D Canonical Data Asset Intelligence
```

之后：

```text
Stage E Explorer Productization
```

---

# 79. 本版本完成标志

不是：

```text
又增加几张表
```

而是：

**任何 Explorer 页面需要展示的核心字段，都能够由统一 Canonical API 直接、稳定、可追溯地返回。**

前端不再：

```text
猜数据
补数据
临时解析
调用 RPC
按 symbol 找图标
用当前价格算历史 USD
```

做到这一点以后，再做 OKLink 风格 UI，才不会返工。

---

# 80. 最终结论

当前最有价值的下一步：

```text
On-chain Canonical Data Asset Layer V2.0
```

优先级高于：

```text
Explorer UI 重构
```

原因非常简单：

```text
你已经有一个非常快的数据库。
下一步要让里面的数据真正“值钱”。
```

当 Canonical / Metadata / Entity / Price / ABI / Contract Intelligence 建立后，
系统才真正从：

```text
“高性能链上数据仓库”
```

升级为：

```text
“可调查、可解释、可直接使用的链上情报数据库”
```
