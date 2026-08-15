# 链上数据资产数据库与 Explorer 前端 V1.0
## On-chain Data Asset Warehouse + Explorer UI + Export Center

> 目标：把现有“下载 → Parquet/DuckDB → 分析”的数据链路，升级为“下载 → 解析标准化 → 数据库持久化 → Explorer 查询 / 统计 / 资金图谱 / 调查 / 导出”的长期数据资产体系。  
> 核心原则：**最终可用数据以数据库为唯一事实源（Source of Truth）**。Parquet 只允许作为历史迁移源、临时交换格式或短期 Staging，不再作为业务查询主存储。

---

# 1. 版本目标

## 1.1 最终形态

系统最终需要具备五层能力：

1. **采集层**
   - Browser / CSV / 直链 / 网页抓取
   - SQD
   - RPC
   - 未来 AWS / 第三方 API
   - 继续由 Smart Download Orchestrator 自动选择数据源

2. **解析标准化层**
   - 原始交易解析
   - Receipt / Log 解码
   - ERC20/BEP20 Transfer 解析
   - ERC721 / ERC1155 Transfer 解析
   - Internal Transaction / Trace 解析
   - Contract Creation 识别
   - Method Signature 解码
   - Token Metadata 富化
   - Address / Contract 类型识别
   - 标签、交易所、Bridge、DEX 等实体富化

3. **数据资产层**
   - ClickHouse：链上事实数据、地址流水、聚合统计
   - PostgreSQL/现有业务数据库：任务、配置、标签管理、调查案件、导出任务等事务型数据
   - 不再让业务 API 直接读取 Parquet

4. **应用层**
   - Explorer
   - 地址详情
   - 交易详情
   - Token 详情
   - Contract 详情
   - 数据资产查询
   - 智能调查
   - 地址关系图
   - 数据资产导出

5. **分析层**
   - 地址资金流
   - 交易对手统计
   - Token 统计
   - 时间序列
   - 净流入/净流出
   - 沉淀资金
   - 大额交易
   - CEX/DEX/Bridge 交互
   - 实时余额
   - 标签画像
   - 风险/调查指标

---

# 2. 核心架构结论

## 2.1 不建议把所有链上数据直接塞进 PostgreSQL

链上数据具有：

- 追加写为主
- 数千万到数亿行
- 大量时间范围过滤
- 大量 address / token / tx_hash 条件过滤
- Top-N、SUM、COUNT、GROUP BY 很多
- 数据导出量大
- 地址分析经常需要扫描长时间窗口

因此建议：

```text
                    ┌──────────────────────────┐
                    │ Smart Download           │
                    │ Orchestrator              │
                    └────────────┬─────────────┘
                                 │
             ┌───────────────────┼───────────────────┐
             │                   │                   │
          Browser               SQD                 RPC
             │                   │                   │
             └───────────────────┴───────────────────┘
                                 │
                                 ▼
                    ┌──────────────────────────┐
                    │ Normalize / Decode Layer │
                    │ 标准化 + 解析 + 富化       │
                    └────────────┬─────────────┘
                                 │
              ┌──────────────────┼──────────────────┐
              │                  │                  │
              ▼                  ▼                  ▼
        ClickHouse Facts    PostgreSQL Meta    Temporary Stage
        链上事实数据库       任务/配置/标签        临时文件/缓存
              │
              ├─────────────┬──────────────┬──────────────┐
              ▼             ▼              ▼              ▼
         Explorer API   Analytics API   Graph API      Export API
              │             │              │              │
              └─────────────┴──────────────┴──────────────┘
                                 │
                                 ▼
                         React Explorer UI
```

### ClickHouse 保存

永久保存：

- block
- transaction
- token transfer
- internal transaction
- NFT transfer
- contract creation
- parsed event
- address activity
- token balance snapshot
- address daily aggregate
- token flow aggregate
- counterparty aggregate

### PostgreSQL 保存

事务型数据：

- download_job
- investigation
- case
- address_label
- custom_label
- provider
- export_job
- export_template
- user configuration
- token metadata override
- entity registry

---

# 3. 数据生命周期

## 3.1 新标准流程

旧流程：

```text
Provider
  ↓
Parquet
  ↓
DuckDB
  ↓
分析
```

新流程：

```text
Provider
  ↓
Raw Response / Chunk
  ↓
Parser
  ↓
Normalizer
  ↓
Enricher
  ↓
Validator
  ↓
Database Writer
  ↓
ClickHouse
  ↓
Materialized Views
  ↓
Explorer / Investigation / Graph / Export
```

## 3.2 Parquet 的新定位

Parquet 不再是最终数据资产。

仅允许用于：

1. 旧历史数据迁移
2. 超大下载任务的临时落盘
3. Provider 原始文件格式必须为 Parquet 时作为输入
4. 数据库不可用时的短时 WAL/Spool
5. 调试或灾备导出

成功入库并完成校验后：

```text
TEMP_PARQUET
    ↓
DB INGESTED
    ↓
VALIDATED
    ↓
DELETE / ARCHIVE
```

最终业务层禁止：

```text
Frontend → DuckDB → Parquet
```

统一改成：

```text
Frontend → Explorer API → ClickHouse
```

---

# 4. 数据模型总览

建议至少建立以下核心数据资产。

```text
blocks
transactions
transaction_receipts
token_transfers
nft_transfers
internal_transactions
contract_creations
contracts
tokens
parsed_events

address_activity
address_balance_snapshots

address_daily_stats
address_token_daily_stats
address_counterparty_stats
token_daily_stats

entity_labels
address_labels
```

---

# 5. Blocks

表：

```text
chain_blocks
```

建议字段：

```sql
chain_id UInt32
block_number UInt64
block_hash FixedString(66)
parent_hash FixedString(66)
block_time DateTime64(3)

miner_address String

gas_limit UInt64
gas_used UInt64
base_fee_per_gas Nullable(UInt256)

tx_count UInt32

size_bytes UInt64

source_provider LowCardinality(String)
ingested_at DateTime64(3)
```

建议：

```text
PARTITION BY toYYYYMM(block_time)
ORDER BY (chain_id, block_number)
```

---

# 6. Transactions

表：

```text
chain_transactions
```

这是 Explorer 最核心的交易事实表。

建议字段：

```text
chain_id
block_number
block_hash
block_time

transaction_index
tx_hash

from_address
to_address

nonce

value_raw
value_decimal
native_symbol

input
method_id
method_name

tx_type

gas_limit
gas_price
max_fee_per_gas
max_priority_fee_per_gas
effective_gas_price

gas_used
transaction_fee_native
transaction_fee_usd

status

is_contract_creation
created_contract_address

error_message

source_provider
ingested_at
```

### 必须支持的 Explorer 查询

```text
tx_hash
block_number
from_address
to_address
address = from OR to
method_id
method_name
status
tx_type
time range
value range
fee range
contract creation
```

---

# 7. Token Transfers

表：

```text
token_transfers
```

这是资金追踪最重要的一张表之一。

字段：

```text
chain_id
block_number
block_time

tx_hash
transaction_index
log_index

token_address

token_name
token_symbol
token_decimals

token_standard
event_signature

from_address
to_address

raw_value
value_decimal

usd_price
usd_value

from_entity_id
to_entity_id

source_provider
ingested_at
```

唯一键逻辑：

```text
chain_id
+ block_number
+ tx_hash
+ log_index
```

对于 ERC1155：

```text
+ token_id
+ batch_index
```

### Token Standard

```text
ERC20
BEP20
ERC721
ERC1155
NATIVE
UNKNOWN
```

---

# 8. Token Metadata

表：

```text
tokens
```

字段：

```text
chain_id
contract_address

name
symbol
decimals

token_standard

logo_uri
logo_source
logo_hash

official_website

is_verified
is_spam

first_seen_block
first_seen_time

last_metadata_refresh_at
```

## 8.1 Token 符号和图标

前端不能使用：

```text
USDT
USDC
WBNB
```

这种纯文字替代。

统一组件：

```text
<TokenIdentity />
```

展示：

```text
[Token Icon] USDT
             Tether USD
```

Token Icon 获取优先级：

```text
1. 本地 Token Registry
2. 官方 Token List
3. 已验证第三方 Token Metadata
4. 项目官网 Logo
5. 合约地址 Identicon
```

### 必须按 `chain_id + contract_address` 绑定

禁止仅按 symbol 绑定图标。

原因：

```text
symbol = USDT
```

可能存在多个假币。

正确键：

```text
56:0x55d398326f99059ff775485246999027b3197955
```

---

# 9. Internal Transactions / Traces

表：

```text
internal_transactions
```

字段：

```text
chain_id
block_number
block_time
tx_hash

trace_address
trace_index

call_type

from_address
to_address

value_raw
value_decimal

gas
gas_used

input
output

success
error

depth
parent_trace_index
```

Call Type：

```text
CALL
STATICCALL
DELEGATECALL
CALLCODE
CREATE
CREATE2
SELFDESTRUCT
```

---

# 10. Contract Creation

不能只在 transaction 中保存一个布尔值。

必须独立建立：

```text
contract_creations
```

字段：

```text
chain_id
block_number
block_time

tx_hash

creator_address
contract_address

creation_type
CREATE / CREATE2

factory_address

init_code_hash
runtime_code_hash

deployer_nonce

token_detected
token_standard

contract_name
compiler_version

is_proxy
proxy_type
implementation_address

source_verified

created_at
```

用途：

- 查询某地址创建了多少合约
- 查询 Factory 批量创建行为
- 找 Token 创建者
- 找同 Runtime Bytecode 合约族
- 调查诈骗项目部署关系

---

# 11. Contracts

表：

```text
contracts
```

建议字段：

```text
chain_id
contract_address

creator_address
creation_tx_hash
creation_block
creation_time

bytecode_hash
runtime_bytecode_hash

contract_name

is_verified
is_proxy
proxy_type
implementation_address

abi_json

token_standard

first_seen
last_seen

risk_flags
```

---

# 12. Parsed Events

表：

```text
parsed_events
```

专门保存非 Transfer 事件。

字段：

```text
chain_id
block_number
block_time
tx_hash
log_index

contract_address

topic0
event_signature
event_name

decoded_json

source_provider
```

典型：

```text
Swap
Mint
Burn
Deposit
Withdraw
Approval
OwnershipTransferred
PairCreated
PoolCreated
```

后期可以继续拆出：

```text
dex_swaps
liquidity_events
bridge_events
approval_events
```

---

# 13. NFT Transfers

表：

```text
nft_transfers
```

字段：

```text
chain_id
block_number
block_time
tx_hash
log_index

collection_address
collection_name
collection_symbol

standard

token_id

from_address
to_address

amount

operator_address
```

---

# 14. Address Activity：最重要的查询加速层

单纯查询：

```sql
WHERE from_address = ?
   OR to_address = ?
```

在海量表上效率不够理想。

因此需要专门建立：

```text
address_activity
```

将交易按“地址视角”展开。

例如：

```text
A → B 100 USDT
```

产生：

```text
A OUT B 100 USDT
B IN  A 100 USDT
```

字段：

```text
chain_id

address
counterparty_address

direction

activity_type

block_number
block_time

tx_hash
event_index

token_address
token_symbol

amount
usd_value

method_id
method_name

status

counterparty_entity_type
counterparty_label
```

direction：

```text
IN
OUT
SELF
```

activity_type：

```text
TRANSACTION
TOKEN_TRANSFER
INTERNAL_TRANSFER
NFT_TRANSFER
CONTRACT_CREATE
DEX_SWAP
BRIDGE
```

建议排序：

```text
ORDER BY
(
    chain_id,
    address,
    block_time,
    tx_hash,
    event_index
)
```

地址 Explorer 页面绝大多数列表都直接读取这张表或相关事实表。

---

# 15. Address Summary

物化/聚合表：

```text
address_summary
```

核心字段：

```text
chain_id
address

address_type

first_seen_time
last_seen_time

tx_count
in_tx_count
out_tx_count

token_transfer_count
internal_tx_count
nft_transfer_count

contract_created_count

unique_counterparty_count

native_received
native_sent
native_netflow

usd_received
usd_sent
usd_netflow

active_days

max_single_in_usd
max_single_out_usd

top_counterparty

cex_interaction_count
dex_interaction_count
bridge_interaction_count

risk_score
```

---

# 16. Address Daily Stats

表：

```text
address_daily_stats
```

字段：

```text
chain_id
address
date

tx_count
in_count
out_count

unique_counterparties

native_in
native_out

token_in_usd
token_out_usd

netflow_usd

gas_fee_usd

cex_in_usd
cex_out_usd

dex_volume_usd
bridge_volume_usd
```

用途：

- 趋势图
- 地址活跃度
- 资金流曲线
- 交易次数变化
- 调查时间线

---

# 17. Counterparty Stats

表：

```text
address_counterparty_stats
```

字段：

```text
chain_id
address
counterparty

first_interaction
last_interaction

interaction_count

in_count
out_count

in_usd
out_usd

netflow_usd

token_count

counterparty_label
counterparty_type
```

前端可直接展示：

```text
Top 资金来源
Top 资金去向
Top 高频对手方
Top CEX
Top DEX
Top Bridge
```

---

# 18. Explorer 前端

## 18.1 总体视觉

目标：

```text
OKLink 信息密度
+
更强筛选
+
更强统计
+
调查系统联动
```

不再把“下载器”作为用户查看数据的主要入口。

新的一级模块：

```text
Explorer
Investigation
Fund Flow
Data Assets
Download Center
Data Sources
Settings
```

---

# 19. Explorer 首页

顶部：

```text
链选择器
+
全局搜索框
```

搜索支持：

```text
Address
Transaction Hash
Block Number
Block Hash
Token
Contract
Label
Entity
```

首页卡片：

```text
Latest Block
Transactions
Token Transfers
Active Addresses
Gas
Downloaded Coverage
Database Coverage
```

---

# 20. Address 页面

Header：

```text
Address
0x....

[Copy]

EOA / Contract

标签：
Binance
OKX
Bridge
DEX
Custom Label
```

资产：

```text
BNB Holdings
USDT Holdings
USDC Holdings
Total Token Value
```

统计：

```text
总交易数
转入次数
转出次数
内部交易
Token Transfer
NFT Transfer
交互地址数
创建合约数
首次活动
最后活动
```

---

# 21. Address Tabs

必须至少包括：

```text
Overview
Transactions
Internal Txns
Token Transfers
NFT Transfers
Assets
Contract
Events
Analytics
Fund Flow
Export
```

如果是 EOA：

```text
Contract
Events
```

可以隐藏或置灰。

---

# 22. Transactions Tab

表格：

```text
Txn Hash
Method
Block
Age / Time
From
Direction
To
Value
Txn Fee
Status
```

筛选：

```text
时间
区块范围

交易状态
Success / Failed

方向
IN / OUT / SELF

Method

From
To
Counterparty

Value Min
Value Max

Fee Min
Fee Max

Contract Call
Native Transfer
Contract Creation
```

---

# 23. Token Transfers Tab

表格：

```text
Txn Hash
Method
Block
Age
From
Direction
To
Amount
Token
USD Value
```

Token：

```text
[Logo] 100,000 USDT
       Tether USD
```

筛选：

```text
Token
Token Contract
Direction
From
To
Amount
USD Value
Time
Method
Entity Type
Entity Label
```

---

# 24. Internal Transactions

表格：

```text
Parent Txn Hash
Trace
Type
Block
Time
From
Direction
To
Value
Status
```

支持：

```text
CALL
DELEGATECALL
CREATE
CREATE2
SELFDESTRUCT
```

筛选。

---

# 25. Contract Creation 页面

新增独立标签：

```text
Contract Creations
```

字段：

```text
Txn Hash
Time
Creator
Contract
CREATE / CREATE2
Factory
Contract Type
Token
Verified
Proxy
Implementation
```

---

# 26. 比 OKLink 更完整的 Statistics Panel

地址顶部增加可折叠：

```text
Show Analytics
```

展开后：

## 26.1 基础统计

```text
Total Transactions
Inbound Transactions
Outbound Transactions

Total Received USD
Total Sent USD
Net Flow USD

Unique Counterparties
Active Days
First Seen
Last Seen
```

## 26.2 资金统计

```text
最大单笔转入
最大单笔转出

平均转入
平均转出

中位数转入
中位数转出

24H
7D
30D
90D
ALL
```

## 26.3 对手方

```text
Top 10 Sources
Top 10 Destinations
Top 10 Interaction Addresses

Top CEX
Top DEX
Top Bridge
```

## 26.4 集中度

```text
Top1 Inflow Concentration
Top5 Inflow Concentration
Top10 Inflow Concentration

Top1 Outflow Concentration
Top5 Outflow Concentration
Top10 Outflow Concentration
```

## 26.5 Token

```text
Token Count
Stablecoin Volume
Non-Stablecoin Volume

Top Tokens Received
Top Tokens Sent

USDT In
USDT Out
USDT Net Flow
```

## 26.6 行为

```text
Active Hour Distribution
Active Weekday Distribution
Transaction Frequency

Average Holding Duration
Fast Pass-Through Ratio
Settlement Ratio
```

## 26.7 实体交互

```text
CEX Deposit Count
CEX Withdrawal Count

DEX Swap Count
DEX Volume

Bridge In
Bridge Out
```

---

# 27. 资金调查统计增强

这部分作为你的系统相对普通 Explorer 的核心优势。

## 27.1 沉淀指标

```text
Received
→
X hours/days 未继续转出
```

统计：

```text
1H retention
6H retention
24H retention
7D retention
30D retention
```

---

# 28. Fast Pass-Through

识别：

```text
A → X → B
```

X 收到资金后短时间继续转出。

指标：

```text
5 min
30 min
1 hour
6 hour
24 hour
```

用于识别：

- 中转钱包
- 归集钱包
- 洗钱中间层
- 自动化分发地址

---

# 29. Exchange / Bridge / DEX Statistics

结合标签数据库：

```text
ADDRESS
→ ENTITY
```

统计：

```text
CEX Deposit
CEX Withdrawal
DEX Interaction
Bridge Interaction
Mixer Interaction
Contract Interaction
```

---

# 30. 数据资产导出中心

新增一级功能：

```text
Data Assets
    ├── Query
    ├── Saved Filters
    ├── Exports
    └── Export Templates
```

---

# 31. Data Assets Query

数据集：

```text
Transactions
Token Transfers
Internal Transactions
NFT Transfers
Contract Creations
Contracts
Events
Address Activity
Address Stats
Counterparty Stats
```

---

# 32. 查询构建器

用户无需写 SQL。

Filter Builder：

```text
Chain = BSC

AND

Time >= 2026-01-01

AND

Address IN [...]

AND

Token = USDT

AND

Direction = OUT

AND

USD Value >= 100000
```

支持：

```text
AND
OR
NOT
IN
NOT IN
BETWEEN
>
>=
<
<=
=
!=
CONTAINS
```

---

# 33. 导出字段选择

支持用户自行勾选：

```text
✓ tx_hash
✓ block_time
✓ from
✓ to
✓ token_symbol
✓ value
✓ usd_value

□ block_hash
□ gas
□ input
```

可以拖动字段顺序。

---

# 34. Export Preview

执行前显示：

```text
Matched Rows
Estimated Size
Selected Columns
Time Range
Filters
```

Preview：

```text
前 100 行
```

---

# 35. Export Formats

V1：

```text
CSV
XLSX
JSON
NDJSON
```

XLSX 必须处理 Excel 单 Sheet 最大行数限制。

超过限制时：

```text
Sheet1
Sheet2
Sheet3
```

或者：

```text
export_part_001.xlsx
export_part_002.xlsx
```

超大数据默认建议 CSV。

---

# 36. Export Job

表：

```text
export_jobs
```

字段：

```text
id
dataset

filter_json
columns_json
sort_json

format

status

estimated_rows
exported_rows

file_count
output_path

started_at
completed_at

error
```

状态：

```text
PENDING
RUNNING
COMPLETED
FAILED
CANCELLED
```

---

# 37. Export API

```http
POST /api/v1/data-assets/query/preview

POST /api/v1/exports

GET /api/v1/exports/:id

POST /api/v1/exports/:id/cancel

GET /api/v1/exports/:id/files
```

---

# 38. Explorer API

## Address

```http
GET /api/v1/explorer/:chain/address/:address/summary
GET /api/v1/explorer/:chain/address/:address/transactions
GET /api/v1/explorer/:chain/address/:address/token-transfers
GET /api/v1/explorer/:chain/address/:address/internal-transactions
GET /api/v1/explorer/:chain/address/:address/nft-transfers
GET /api/v1/explorer/:chain/address/:address/assets
GET /api/v1/explorer/:chain/address/:address/contracts
GET /api/v1/explorer/:chain/address/:address/events
GET /api/v1/explorer/:chain/address/:address/analytics
GET /api/v1/explorer/:chain/address/:address/counterparties
```

## Transaction

```http
GET /api/v1/explorer/:chain/tx/:hash
GET /api/v1/explorer/:chain/tx/:hash/internal-transactions
GET /api/v1/explorer/:chain/tx/:hash/token-transfers
GET /api/v1/explorer/:chain/tx/:hash/events
```

## Token

```http
GET /api/v1/explorer/:chain/token/:contract
GET /api/v1/explorer/:chain/token/:contract/transfers
GET /api/v1/explorer/:chain/token/:contract/holders
GET /api/v1/explorer/:chain/token/:contract/analytics
```

---

# 39. Pagination

禁止海量数据使用：

```sql
OFFSET 1000000
```

统一改成 Cursor Pagination：

```text
block_time
block_number
transaction_index
log_index
```

前端：

```text
Next
Previous
```

API：

```json
{
  "data": [],
  "page": {
    "next_cursor": "...",
    "has_more": true
  }
}
```

---

# 40. 全局查询 DSL

前端所有筛选统一生成：

```json
{
  "dataset": "token_transfers",
  "chain_id": 56,
  "filters": [
    {
      "field": "address",
      "op": "eq",
      "value": "0x..."
    },
    {
      "field": "token_symbol",
      "op": "eq",
      "value": "USDT"
    },
    {
      "field": "usd_value",
      "op": "gte",
      "value": 100000
    }
  ],
  "sort": [
    {
      "field": "block_time",
      "direction": "desc"
    }
  ],
  "limit": 100
}
```

后端转换成安全 Query AST。

禁止把用户输入直接拼接 SQL。

---

# 41. 数据覆盖状态

Explorer 页面必须区分：

```text
NO_DATA
PARTIAL
COMPLETE
STALE
DOWNLOADING
```

页面顶部显示：

```text
Data Coverage

2024-01-01
→
2026-08-08

COMPLETE
```

如果缺失：

```text
2024-01-01 → 2025-05-01 COMPLETE
2025-05-01 → 2025-06-01 MISSING
2025-06-01 → NOW COMPLETE
```

---

# 42. Explorer 自动触发下载

用户搜索一个地址：

```text
Explorer Search
```

系统先查询：

```text
Coverage Registry
```

如果：

```text
COMPLETE
```

直接查数据库。

如果：

```text
PARTIAL
```

显示已有数据，同时后台调用：

```text
Smart Download Orchestrator
```

补缺口。

如果：

```text
NO_DATA
```

自动创建 AddressJob。

数据逐步入库后：

```text
Explorer API
```

即可查询新增数据。

---

# 43. 与智能调查联动

Investigation 不再直接解析 Parquet。

统一：

```text
Investigation
   ↓
Data Asset Query API
   ↓
ClickHouse
```

如果缺数据：

```text
Investigation
   ↓
Coverage Service
   ↓
Smart Download Orchestrator
   ↓
Parser
   ↓
Database
   ↓
Investigation Resume
```

---

# 44. 与资金流向图联动

Graph 查询：

```text
address_activity
token_transfers
internal_transactions
entity_labels
```

节点展开：

```text
Expand Upstream
Expand Downstream
```

先查数据库。

若目标区间缺失：

```text
Graph
→ Coverage
→ Download Orchestrator
→ DB
→ Graph Refresh
```

---

# 45. 查询性能目标

目标数据规模：

```text
10M rows
50M rows
100M rows
```

P95：

```text
Address Summary          < 300 ms
Address Tx first page    < 500 ms
Token Transfer first page< 500 ms
Counterparty Top10       < 800 ms
30D Stats                < 1 s
ALL Stats                < 3 s
```

Export：

```text
1M rows
```

允许进入异步 Export Job。

---

# 46. 数据完整性

每个下载任务必须记录：

```text
downloaded
parsed
normalized
inserted
deduplicated
invalid
```

最终：

```text
parsed_valid
=
inserted_unique
```

或有明确 reject 原因。

---

# 47. 数据版本

增加：

```text
parser_version
schema_version
normalizer_version
```

例如：

```text
parser_version = evm-parser-2.1.0
schema_version = 3
```

后续解析器升级：

```text
reparse
```

时可追踪。

---

# 48. 幂等写入

必须确保：

```text
重复下载
重复重试
Provider 切换
Job 恢复
```

不会造成重复数据。

建议每类数据拥有稳定 Event ID。

Transaction：

```text
chain_id + tx_hash
```

Token Transfer：

```text
chain_id + tx_hash + log_index
```

Trace：

```text
chain_id + tx_hash + trace_address
```

NFT：

```text
chain_id + tx_hash + log_index + token_id + batch_index
```

---

# 49. Database Writer

新增模块：

```text
internal/datawarehouse/
```

建议：

```text
datawarehouse/
├── writer.go
├── batch_writer.go
├── validator.go
├── deduplicator.go
├── checkpoint.go
├── schema/
│   ├── transaction.go
│   ├── token_transfer.go
│   ├── internal_tx.go
│   ├── nft_transfer.go
│   └── contract.go
├── clickhouse/
│   ├── client.go
│   ├── migrations.go
│   └── repository.go
└── postgres/
    └── metadata_repository.go
```

---

# 50. Parser Pipeline

新增统一数据接口：

```go
type ParsedBatch struct {
    Blocks              []Block
    Transactions        []Transaction
    TokenTransfers      []TokenTransfer
    InternalTxs         []InternalTransaction
    NFTTransfers        []NFTTransfer
    ContractCreations   []ContractCreation
    Events              []ParsedEvent
}
```

执行：

```go
rawBatch
    ↓
Parse()
    ↓
Normalize()
    ↓
Enrich()
    ↓
Validate()
    ↓
Warehouse.Write()
```

---

# 51. Metadata Enrichment

Token Transfer 解析后：

```text
token_address
```

查询：

```text
Token Registry
```

补：

```text
name
symbol
decimals
logo
verified
```

如果不存在：

```text
RPC eth_call
name()
symbol()
decimals()
```

并缓存。

---

# 52. Entity Enrichment

地址加入：

```text
address_labels
```

例如：

```text
0x...
→ Binance
→ CEX
→ Deposit
```

Token Transfer 同时冗余：

```text
from_entity_type
to_entity_type
```

便于统计。

---

# 53. Frontend Token Component

建议：

```tsx
<TokenIdentity
  chainId={56}
  address="0x..."
  symbol="USDT"
  name="Tether USD"
  logoUrl="..."
/>
```

显示：

```text
┌────┐
│LOGO│ USDT
└────┘ Tether USD
```

Token 地址 hover：

```text
Contract
Name
Symbol
Decimals
Verified
```

---

# 54. Address Component

```tsx
<AddressIdentity />
```

显示：

```text
[Icon] Binance 14
       0x28c6...d60
```

或者：

```text
[Icon] 0xabc...123
       EOA
```

---

# 55. 数据资产页面

一级页面：

```text
数据资产
```

Dashboard：

```text
Transactions
Token Transfers
Internal Txns
NFT Transfers
Contracts
Addresses
Tokens
```

显示：

```text
Total Rows
Storage
Earliest
Latest
Chains
Coverage
Last Updated
```

---

# 56. Dataset Detail

例如：

```text
Token Transfers
```

顶部：

```text
48,215,773 rows

BSC
2023-01-01 → 2026-08-08
```

中间：

```text
Filter Builder
```

下方：

```text
Data Grid
```

右上角：

```text
Export
Save Filter
Open in Investigation
Open in Fund Flow
```

---

# 57. 不要继续把下载中心当数据中心

未来：

```text
Download Center
```

只负责：

```text
采集状态
Provider
Job
Chunk
Retry
Coverage
Errors
```

而不是：

```text
数据查看
数据统计
数据导出
```

数据使用统一进入：

```text
Data Assets
Explorer
Investigation
Fund Flow
```

---

# 58. 数据迁移方案

## Phase 1：创建 ClickHouse

部署：

```text
ClickHouse
```

先建立：

```text
transactions
token_transfers
internal_transactions
contract_creations
tokens
address_activity
```

---

# 59. Phase 2：现有 Parquet 一次性 Backfill

流程：

```text
Existing Parquet
      ↓
Migration Reader
      ↓
Normalize
      ↓
Validate
      ↓
ClickHouse
```

记录：

```text
migration_manifest
```

必须校验：

```text
source_rows
parsed_rows
unique_rows
inserted_rows
db_rows
```

---

# 60. Phase 3：新任务改为直接入库

新下载：

```text
Provider
→ Parser
→ DB
```

Parquet 改为：

```text
optional_spool
```

---

# 61. Phase 4：Dual Read Verification

短期：

```text
DuckDB Result
vs
ClickHouse Result
```

测试：

```text
COUNT
SUM
Unique
Address Tx
Token Transfer
Counterparty
```

全部通过后停止业务读取 Parquet。

---

# 62. Phase 5：Explorer V2

前端切换：

```text
Explorer API
→ ClickHouse
```

上线：

```text
Address
Transaction
Token Transfer
Internal Tx
Contract Creation
```

---

# 63. Phase 6：Data Export Center

开发：

```text
Filter DSL
Query Preview
Export Jobs
CSV
XLSX
JSON
```

---

# 64. Phase 7：Advanced Analytics

增加：

```text
Counterparty
Net Flow
Concentration
Retention
Fast Pass Through
CEX
DEX
Bridge
Token Stats
```

---

# 65. P0 必须完成

## P0-1

数据库成为最终 Source of Truth。

验收：

```text
Explorer 页面完全关闭 Parquet 后仍正常工作
```

## P0-2

Transactions 入库。

## P0-3

Token Transfers 入库。

## P0-4

Internal Txns 入库。

## P0-5

Contract Creations 入库。

## P0-6

Token Metadata + Logo。

## P0-7

Address Activity。

## P0-8

Explorer Address 页面。

## P0-9

数据资产筛选。

## P0-10

CSV/XLSX 导出。

---

# 66. P1

```text
NFT
Events
Contracts
Token 页面
Transaction Detail
Counterparty Statistics
Address Analytics
Entity Label
```

---

# 67. P2

```text
资金沉淀
快速中转
DEX Swap
Bridge
CEX
地址聚类
高级图谱
调查指标
风险规则
```

---

# 68. P0 验收 Case

## Case A：普通交易

输入：

```text
tx_hash
```

必须看到：

```text
Block
Time
From
To
Value
Method
Status
Gas
Fee
```

---

# 69. Case B：Token Transfer

交易：

```text
A → B
100 USDT
```

数据库必须：

```text
token_transfers = 1
address_activity = 2
```

Explorer A：

```text
OUT 100 USDT → B
```

Explorer B：

```text
IN 100 USDT ← A
```

---

# 70. Case C：Contract Creation

交易：

```text
to = null
```

必须：

```text
chain_transactions.is_contract_creation = true

contract_creations = 1

contracts = 1
```

Explorer：

```text
Contract Creation
```

可直接跳到新 Contract。

---

# 71. Case D：数据导出

筛选：

```text
BSC
USDT
OUT
>= 100,000 USD
2026-01-01 → 2026-08-01
```

点击：

```text
Export CSV
```

文件必须完全等于查询结果。

---

# 72. Case E：统计

选择地址：

```text
0x...
```

必须一次返回：

```text
tx count
in count
out count
total in
total out
netflow
unique counterparties
top sources
top destinations
first seen
last seen
```

---

# 73. Case F：Coverage

数据库只有：

```text
2026-01-01 → 2026-06-01
```

用户查询：

```text
2026-01-01 → 2026-08-01
```

系统必须：

```text
先返回已有数据
+
显示 PARTIAL
+
自动下载缺失 2026-06-01 → 2026-08-01
```

---

# 74. Case G：图谱联动

地址页面点击：

```text
View Fund Flow
```

资金图直接使用数据库结果。

点击节点：

```text
Expand Downstream
```

优先查询本地数据库。

本地缺数据时：

```text
自动 Smart Download
```

然后增量刷新图。

---

# 75. 推荐工程目录

```text
internal/
├── downloadorchestrator/
├── ingestion/
├── parser/
├── normalizer/
├── enrichment/
├── datawarehouse/
├── explorer/
├── analytics/
├── export/
├── coverage/
├── investigation/
└── graph/
```

Frontend：

```text
src/
├── pages/
│   ├── explorer/
│   ├── data-assets/
│   ├── investigations/
│   └── fund-flow/
│
├── components/
│   ├── AddressIdentity/
│   ├── TokenIdentity/
│   ├── TransactionTable/
│   ├── TokenTransferTable/
│   ├── FilterBuilder/
│   ├── AnalyticsPanel/
│   └── ExportDialog/
```

---

# 76. 最终产品结构

```text
┌──────────────────────────────────────────────┐
│ Explorer                                     │
│  Address / Tx / Block / Token / Contract     │
├──────────────────────────────────────────────┤
│ Data Assets                                  │
│  Query / Filter / Export                     │
├──────────────────────────────────────────────┤
│ Investigation                                │
│  Agent / Timeline / Evidence                 │
├──────────────────────────────────────────────┤
│ Fund Flow                                    │
│  Graph / Expand / Trace                      │
├──────────────────────────────────────────────┤
│ Download Center                              │
│  Jobs / Providers / Coverage                 │
└──────────────────────────────────────────────┘

                        │

                 Unified Data API

                        │

                    ClickHouse

                        │

    Transactions / Transfers / Trace / Contracts
        / Address Activity / Aggregates
```

---

# 77. 本版本最重要的架构原则

### 原则 1

**下载结果不是数据资产。**

下载只是采集。

---

### 原则 2

**解析完成、标准化、验证并成功入库后，才叫数据资产。**

---

### 原则 3

**数据库是业务唯一事实源。**

Explorer、调查、资金图谱、导出全部查数据库。

---

### 原则 4

**Parquet 降级为临时媒介，不再承担业务数据库职责。**

---

### 原则 5

**地址页面必须有专门的 Address Activity 查询模型。**

否则后面数据量大后：

```text
from = A OR to = A
```

会成为长期性能瓶颈。

---

### 原则 6

**Explorer 前端负责“看数据”，Data Assets 负责“查数据/导数据”，Download Center 只负责“拿数据”。**

三者必须彻底解耦。

---

### 原则 7

**Token 必须按 chain_id + contract_address 建立身份。**

symbol、name、decimals、logo 全部从 Token Registry 统一输出。

---

### 原则 8

**统计能力必须建立在预聚合与物化视图之上，而不是每次临时扫描所有明细。**

这样才能在数据量增长后仍保持 Explorer 级体验。

---

# 78. 下一阶段建议名称

本阶段建议正式命名：

```text
On-chain Data Asset Warehouse V1.0
链上数据资产数据库 V1.0
```

下一阶段：

```text
Explorer Intelligence UI V1.0
```

重点完成：

```text
OKLink 风格 Address / Transaction / Token 页面
+
Token Logo Registry
+
高级筛选
+
Address Analytics Panel
+
Data Export Center
```

---

# 79. 最终验收目标

最终用户不需要知道：

```text
SQD
RPC
Browser
Parquet
DuckDB
Chunk
Provider
```

用户只需要：

```text
输入地址
```

然后系统自动：

```text
搜索数据库
↓
判断 Coverage
↓
自动补数据
↓
解析
↓
入库
↓
刷新 Explorer
↓
统计
↓
生成资金图
↓
支持调查
↓
按筛选条件导出
```

最终体验应当从：

```text
“链上下载工具”
```

升级为：

```text
“本地化 OKLink + 链上调查分析平台”
```

并且统计、资金追踪、调查联动和数据资产导出能力明显强于普通区块浏览器。
