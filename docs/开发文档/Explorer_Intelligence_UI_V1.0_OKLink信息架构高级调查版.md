# Explorer Intelligence UI V1.0
## OKLink 信息架构 + 高级地址画像 + 资金调查一体化前端

> 本阶段定位：**产品化阶段**。
>
> 前置能力已经完成：
>
> - ClickHouse Data Plane 已全面生产化
> - Writer / Explorer / Analytics / Graph / Investigation / Export 已接入 ClickHouse
> - 在线 DuckDB / Parquet Reader 已关闭
> - Canonical Data Asset Layer 已完成/进入稳定基线
> - Entity & Metadata Intelligence 已建立统一身份体系
> - Historical Price & Financial Analytics 已提供历史 USD、资金流、Retention、Pass-through、PnL 等能力
> - ClickHouse 固定根目录：`E:\database\clickhouse`
>
> 本阶段禁止重新设计数据平面。
>
> 本阶段目标：
>
> **把现有数据能力重构成一个以 OKLink 浏览器信息架构为基础、但统计分析明显更强的链上 Explorer。**
>
> 最终体验：
>
> ```text
> OKLink 的浏览效率
> +
> Nansen 类地址画像
> +
> 本系统的 Fund Flow
> +
> Investigation
> +
> Data Assets
> +
> Export
> ```

---

# 1. 核心产品原则

## 1.1 Explorer 是主入口

用户进入系统后首先看到：

```text
搜索
地址
交易
Token
Contract
Block
```

而不是：

```text
Downloader
Provider
Chunk
SQD
RPC
```

数据采集能力全部退到后台。

---

# 2. 一级导航重构

最终建议：

```text
Explorer
资金流向
智能调查
数据资产
导出中心
下载中心
数据源管理
系统设置
```

优先级：

```text
业务使用
↓
分析
↓
数据管理
↓
基础设施
```

---

# 3. Explorer 首页

页面核心结构：

```text
┌────────────────────────────────────────────────────────────┐
│ Chain Selector   Search Address / Tx / Block / Token       │
├────────────────────────────────────────────────────────────┤
│ Data Coverage │ Latest Block │ Tx Count │ Token Transfers │
├────────────────────────────────────────────────────────────┤
│ Latest Transactions                                       │
├────────────────────────────────────────────────────────────┤
│ Large Transfers                                           │
└────────────────────────────────────────────────────────────┘
```

不做营销首页。

这是内部专业分析工具。

---

# 4. Global Search

统一搜索：

```text
Address
Transaction Hash
Block Number
Block Hash
Token Contract
Token Symbol
Token Name
Contract
Entity
Address Label
```

自动识别输入。

---

# 5. 搜索自动判断

```text
0x + 40 hex
→ Address / Contract

0x + 64 hex
→ Tx Hash / Block Hash

纯数字
→ Block Number

其他
→ Token / Entity / Label
```

---

# 6. Search Suggestion

输入：

```text
USDT
```

建议结果：

```text
TOKEN

[USDT LOGO] USDT
Tether USD
BSC
0x55d398...7955
Verified
```

如果存在假 USDT：

```text
[Identicon] USDT
Unknown Token
0xabc...
Unverified
```

禁止仅根据 symbol 共用身份。

---

# 7. Token Identity 强制规范

统一：

```text
chain_id + contract_address
```

前端所有 Token 都使用：

```tsx
<TokenIdentity />
```

禁止各页面自己：

```text
symbol → logo
```

---

# 8. Token 展示

统一：

```text
[Logo] USDT
       Tether USD
```

紧凑模式：

```text
[Logo] USDT
```

Hover：

```text
Tether USD
BEP20
0x55d398...
18 decimals
Verified
```

---

# 9. Token Logo 原则

使用：

```text
Token Registry
```

中的正式 Token Identity。

Logo 优先使用：

```text
项目官方 Logo
可信 Token List
本地 Verified Logo
Identicon Fallback
```

不要抓取 OKLink 页面图标作为系统资产。

目标是：

```text
同一个真实 Token
显示同一个官方身份
```

而不是复制第三方网站资产。

---

# 10. Address 页面定位

Address 是整个 Explorer 的核心。

页面必须同时承担：

```text
浏览
统计
调查入口
资金流入口
导出入口
```

---

# 11. Address Header

建议：

```text
Address

0xd3ae39f05d00654a8c7468ea1845a7f9b7cac735

[Copy] [QR]

EOA

[Binance] [Deposit] [Case Label]

[Open Fund Flow]
[Start Investigation]
[Export]
```

右侧：

```text
DATA COVERAGE

COMPLETE

2023-08-01
→
2026-08-09
```

---

# 12. Address Header 不展示

不要出现：

```text
SQD
RPC
Provider
Downloader
Chunk
503
```

这些进入 Download Center。

---

# 13. Current Assets

顶部资产卡：

```text
BNB
12.4912

USDT
1,235,821.21

USDC
250,000

Estimated Portfolio
$1.49M
```

失败：

```text
--
```

不能：

```text
0
```

伪装成真实余额。

---

# 14. 基础统计卡

第二排：

```text
Transactions
Token Transfers
Internal Txns
NFT Transfers

Counterparties
Active Days

First Seen
Last Seen
```

---

# 15. Address Tabs

最终：

```text
Overview

Transactions
Token Transfers
Internal Txns
NFT Transfers

Assets
Contract Creations

Analytics
Counterparties
Related Wallets

Fund Flow
Investigation

Export
```

Contract Address：

额外：

```text
Contract
Events
```

---

# 16. Overview 页面结构

不要简单堆几十张卡。

采用：

```text
┌─────────────────────────────┬─────────────────────────────┐
│ Financial Summary           │ Activity Trend              │
├─────────────────────────────┼─────────────────────────────┤
│ Top Sources                 │ Top Destinations            │
├─────────────────────────────┼─────────────────────────────┤
│ Token Distribution          │ Entity Distribution         │
├─────────────────────────────┼─────────────────────────────┤
│ Retention                   │ Pass-through                │
└─────────────────────────────┴─────────────────────────────┘
```

---

# 17. Financial Summary

显示：

```text
Total In
$12.83M

Total Out
$11.54M

Net Flow
+$1.29M
```

同时：

```text
Price Coverage
98.7%
```

---

# 18. Financial 时间窗口

全页面统一：

```text
24H
7D
30D
90D
1Y
ALL
Custom
```

一次改变：

```text
Overview
Counterparty
Analytics
Graph Shortcut
Export
```

共同继承。

---

# 19. Transactions

信息结构对齐成熟 EVM Explorer。

列：

```text
Txn Hash
Method
Block
Time
From
Direction
To
Amount
Txn Fee
Status
```

---

# 20. Transactions Column Detail

Txn Hash：

```text
0x732710...abc
```

点击进入 Transaction Detail。

Method：

```text
Transfer
Swap
Approve
Deposit
Withdraw
Multicall
Contract Creation
```

未知：

```text
0x12345678
```

---

# 21. Address Identity

统一：

```tsx
<AddressIdentity />
```

存在 Entity：

```text
Binance
Deposit
0x28c6...d60
```

未知：

```text
0x28c6...d60
EOA
```

---

# 22. Direction

统一：

```text
← IN
→ OUT
↔ SELF
```

不能只依赖颜色。

---

# 23. Transaction Filter

顶部快速筛选：

```text
All
IN
OUT
Failed
Contract
Large Value
```

Advanced：

```text
Time Range
Block Range

From
To
Counterparty

Method

Status

Native Value
USD Value

Fee

Contract Creation
Contract Call
```

---

# 24. Filter Chips

筛选后：

```text
[Direction: OUT ×]
[USD >= 100,000 ×]
[Method: Transfer ×]
[2026-01-01 → 2026-08-09 ×]

Clear All
```

用户始终能看到当前筛选条件。

---

# 25. URL State

所有筛选必须写入 URL。

例如：

```text
/explorer/bsc/address/0x...?tab=transactions&direction=out&min_usd=100000
```

意义：

```text
刷新不丢
复制链接可复现
案件报告可引用
```

---

# 26. Token Transfers

列：

```text
Txn Hash
Method
Block
Time

From
Direction
To

Amount
Token
Historical USD

Entity
```

---

# 27. Token Transfer Amount

例如：

```text
100,000.00
```

Token：

```text
[USDT] USDT
```

USD：

```text
$99,984.20
```

Hover：

```text
Historical price:
$0.999842

Price time:
2025-03-01 12:01

Source:
Historical Price Registry
```

---

# 28. Token Transfer Filters

支持：

```text
Token
Token Contract

IN / OUT / SELF

From
To
Counterparty

Amount Min / Max

USD Min / Max

Entity
Entity Type
Role

Time
```

---

# 29. 调查快捷条件

提供 Preset：

```text
Large Transfers
CEX Deposits
CEX Withdrawals
Stablecoin Flow
USDT Only
Unknown Counterparties
```

---

# 30. Large Transfer

默认：

```text
>= $100,000
```

但 Threshold 用户可改：

```text
10K
50K
100K
500K
1M
Custom
```

---

# 31. Internal Transactions

列：

```text
Parent Txn
Trace
Call Type

Block
Time

From
Direction
To

Value
USD
Depth
Status
```

---

# 32. Call Type

支持：

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

# 33. Zero Value Internal

默认：

```text
Hide Zero Value
```

提供：

```text
Show All Calls
```

用于合约调用调查。

---

# 34. Contract Creations

独立 Tab。

列：

```text
Txn Hash
Time
Creator
Contract

CREATE / CREATE2

Factory

Protocol

Token

Verified

Proxy
Implementation
```

---

# 35. Transaction Detail

页面：

```text
Transaction
0x...
```

Tabs：

```text
Overview
Internal Txns
Token Transfers
Event Logs
Input Data
```

---

# 36. Transaction Header

展示：

```text
Status
Block
Timestamp

From
To

Method

Native Value
Token Movement Value
Total Fee
```

---

# 37. Token Movement Summary

如果：

```text
Native Value = 0 BNB
```

但是发生：

```text
1,000,000 USDT
```

必须明显显示：

```text
Token Movement

1,000,000 USDT
≈ $1.0M
```

避免把：

```text
Value = 0
```

误解成没有资金变化。

---

# 38. Transaction Event Timeline

建议增加：

```text
Execution Timeline
```

例如：

```text
1 CALL Pancake Router
2 Transfer USDT
3 Swap
4 Transfer WBNB
5 Internal BNB
```

比普通 Event Logs 更易分析。

---

# 39. Token Detail

页面 Header：

```text
[Logo] USDT

Tether USD

USDT

BEP20
Verified

0x55d398...7955
```

---

# 40. Token Tabs

```text
Overview
Transfers
Holders
Analytics
Contract
```

只有真实 Holder 数据时才展示 Holders。

不能用不完整数据伪造。

---

# 41. Token Overview

显示：

```text
Current Price
Historical Price Trend

Transfer Count
Transfer Volume

Active Addresses

Largest Transfers

Top Senders
Top Receivers
```

---

# 42. Contract Detail

Tabs：

```text
Overview
Transactions
Token Transfers
Internal Txns

Contract
Events
Analytics
```

---

# 43. Contract Overview

显示：

```text
Creator
Creation Tx
Creation Time

Factory

Protocol

Verified

Proxy Type
Implementation

Runtime Bytecode Hash
Contract Family
```

---

# 44. Contract Intelligence

额外：

```text
Same Creator Contracts
Same Factory Contracts
Same Runtime Hash Contracts
```

用于批量项目调查。

---

# 45. Analytics 页面

这是本系统必须明显超过普通 Explorer 的地方。

---

# 46. Analytics — Financial

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
```

---

# 47. Analytics — Counterparty

```text
Top Sources by USD
Top Destinations by USD

Top Counterparties by Count

Top Net Inflow
Top Net Outflow
```

---

# 48. Counterparty 表

列：

```text
Address / Entity

Role

In Count
In USD

Out Count
Out USD

Net Flow

First Interaction
Last Interaction
```

---

# 49. Entity Grouping

切换：

```text
Address
Entity
```

Address：

```text
0x1
0x2
0x3
```

Entity：

```text
Binance
```

聚合多地址。

---

# 50. Concentration

显示：

```text
Inflow Concentration

Top 1    41.2%
Top 5    72.9%
Top 10   89.1%
```

以及：

```text
Outflow Concentration
```

---

# 51. Token Analytics

```text
Top Tokens Received
Top Tokens Sent

Stablecoin In
Stablecoin Out

USDT In
USDT Out
USDT Net

Token Diversity
```

---

# 52. Entity Analytics

```text
CEX
DEX
Bridge
Protocol
EOA
Unknown
```

显示：

```text
Volume
Count
Share
Netflow
```

---

# 53. CEX Analytics

核心：

```text
CEX Deposit Count
CEX Deposit USD

CEX Withdrawal Count
CEX Withdrawal USD

CEX Net Flow
```

细分：

```text
Binance
OKX
Bybit
Gate
...
```

---

# 54. DEX Analytics

显示：

```text
Swap Count
Swap Volume

Buy Volume
Sell Volume

Top DEX
Top Tokens
```

基于 Canonical Swap。

禁止简单累加所有 Token Transfer 作为 DEX Volume。

---

# 55. Bridge Analytics

```text
Bridge In
Bridge Out
Bridge Net

Top Bridge

Source Chain
Destination Chain
```

---

# 56. Retention

资金沉淀面板：

```text
Received:
$10.0M

Retained after 1H:
$8.3M

6H:
$7.6M

24H:
$6.2M

7D:
$3.1M

30D:
$2.0M
```

---

# 57. Retention Curve

图：

```text
Received Amount Remaining
vs
Elapsed Time
```

直接看资金留存速度。

---

# 58. Pass-through

显示：

```text
5 min     22%
30 min    48%
1 hour    61%
6 hour    79%
24 hour   87%
```

---

# 59. 行为标签

可以基于算法输出：

```text
High Pass-through
High Retention
Highly Concentrated Outflow
High CEX Deposit Ratio
```

但必须标记：

```text
Behavior Signal
```

不能自动当成违法/犯罪结论。

---

# 60. PnL

如果 Cost Basis Coverage 足够：

```text
Realized PnL
Unrealized PnL

Win Rate

Top Profitable Tokens
Top Losing Tokens
```

必须同时显示：

```text
Cost Basis Coverage
```

---

# 61. Historical Balance

新增：

```text
Balance History
```

选择 Token：

```text
USDT
BNB
Custom
```

显示：

```text
Balance
Portfolio Value
```

随时间变化。

---

# 62. Related Wallets

这是比普通 Explorer 更强的一层。

显示：

```text
Related Wallet
Relationship
Confidence
Evidence
First Seen
Last Seen
```

Relationship：

```text
Shared Funding Source
Shared Withdrawal Destination
Same Entity
Frequent Direct Transfer
Same Contract Deployment
Same Factory
Behavior Similarity
Case-linked
```

---

# 63. Related Wallet 不等于同一人

UI 必须明确：

```text
Relationship
≠
Identity
```

例如：

```text
Strong Relationship
```

不能显示：

```text
Same Owner
```

除非有明确证据。

---

# 64. Fund Flow 联动

Address Header：

```text
Open Fund Flow
```

必须继承：

```text
Chain
Address
Time Range
Token
Direction
Min USD
Entity Filter
```

---

# 65. Table → Fund Flow

每条交易 More Menu：

```text
View in Fund Flow
Trace Upstream
Trace Downstream
Expand Counterparty
```

---

# 66. Fund Flow → Explorer

Graph Node Drawer：

```text
Address
Entity
Role

Current Balance

Total In
Total Out
Netflow

Retention
Pass-through
```

按钮：

```text
Open Explorer
Start Investigation
Expand Upstream
Expand Downstream
Export
```

---

# 67. Investigation 联动

Explorer：

```text
Start Investigation
```

传递：

```json
{
  "chain": "bsc",
  "root_address": "0x...",
  "time_range": {},
  "token_filters": [],
  "direction": "OUT",
  "min_usd": 100000,
  "entity_filters": []
}
```

---

# 68. Investigation Evidence

Agent 输出里的：

```text
Address
Tx Hash
Token
Entity
Contract
Block
```

全部可以点击跳回 Explorer。

---

# 69. Data Assets 联动

Explorer 每个 Tab：

```text
Open in Data Assets
```

例如：

```text
USDT
OUT
>= $100K
```

点击后：

Data Assets 自动继承条件。

---

# 70. Export 联动

Export 必须继承：

```text
当前数据集
当前筛选
当前时间
当前排序
当前列
```

不能让用户重新筛一次。

---

# 71. Export Dialog

显示：

```text
Matched Rows

Estimated Size

Selected Columns

CSV
XLSX
JSON
NDJSON
```

超大数据：

```text
Export Job
```

由现有 Export 系统负责。

---

# 72. Saved Views

支持保存：

```text
BSC USDT >= 100K OUT
CEX Large Deposit
Pangu Profit Addresses
High Retention
Fast Pass-through
```

保存：

```text
Dataset
Filters
Columns
Sort
Time Range
```

---

# 73. Workspace Context

建议建立前端：

```text
AnalysisContext
```

统一保存：

```text
chain
root address
time range
tokens
min/max usd
direction
entity
case id
```

Explorer：

```text
→ Fund Flow
→ Investigation
→ Data Assets
→ Export
```

都使用同一个 Context。

---

# 74. 避免上下文丢失

当前用户在：

```text
BSC
Address A
USDT
OUT
>= $100K
2026-01-01 → 2026-08-01
```

打开 Fund Flow 后不能变成：

```text
Address A
ALL TOKEN
ALL TIME
```

这是 P0 验收要求。

---

# 75. Data Coverage

Address Header：

```text
COMPLETE
PARTIAL
NO_DATA
STALE
DOWNLOADING
```

---

# 76. PARTIAL UX

数据部分完整：

```text
PARTIAL

2024-01-01 → 2025-04-01 COMPLETE
2025-04-02 → 2025-05-01 MISSING
2025-05-02 → NOW COMPLETE
```

已有数据继续显示。

后台自动补。

---

# 77. 数据补齐

Explorer：

```text
Coverage Service
↓
Smart Download Orchestrator
↓
Canonical Parser
↓
ClickHouse
↓
Incremental Refresh
```

前端只显示：

```text
Preparing missing data...
```

---

# 78. Advanced Filter Drawer

不要把几十个筛选项全部堆在顶部。

顶部：

```text
Quick Filters
```

右侧：

```text
Advanced Filters
```

Drawer 分组：

```text
Transaction
Amount
Token
Address
Entity
Contract
Time
```

---

# 79. Column Manager

表格支持：

```text
Show / Hide
Drag
Pin Left
Pin Right
Reset
```

---

# 80. 列预设

提供：

```text
Default
Investigation
Financial
Compact
```

例如 Investigation：

```text
Time
From
From Entity
To
To Entity
Token
USD
Tx Hash
```

---

# 81. Density

默认：

```text
Compact
```

Row Height：

```text
44–48px
```

链上 Explorer 不适合大型卡片式表格。

---

# 82. Visual System

按照现有系统方向：

```text
白色背景
浅灰页面底
白色数据面板
细边框
极少阴影
```

资金流图继续白色背景。

---

# 83. 色彩

颜色只用于：

```text
状态
方向
风险
重点
```

不要大量装饰色。

---

# 84. 数字格式

默认：

```text
1,253,812.23 USDT
$1.25M
```

Hover：

```text
完整精度
```

---

# 85. Time

专业调查默认：

```text
Absolute
```

例如：

```text
2026-08-09 05:23:17
```

可切换：

```text
Relative
```

---

# 86. Pagination

后端继续使用 Cursor。

前端不要模拟：

```text
OFFSET
```

支持：

```text
Previous
Next
Page indicator
```

如果现有 501 页逻辑可映射 page cursor，则保留页码体验。

---

# 87. 首屏性能

当前 ClickHouse 后端已具备很高查询性能。

前端重点防：

```text
Request Waterfall
```

Address 打开：

第一批：

```text
Summary
Transactions First Page
Coverage
```

第二批并发：

```text
Balance
Financial Summary
Counterparty
Token Distribution
```

第三批：

```text
Retention
Pass-through
Related Wallets
```

---

# 88. Skeleton

采用：

```text
局部 Skeleton
```

不能全屏 Spin。

---

# 89. Query Cache

推荐：

```text
TanStack Query
```

Cache Key：

```text
chain
address
tab
filters
cursor
```

切回 Tab 不白屏。

---

# 90. Error Isolation

如果：

```text
PnL API Failed
```

不能影响：

```text
Transactions
Token Transfers
```

每个 Analytics Widget 独立 Error Boundary。

---

# 91. Explorer API BFF

建议建立：

```text
Explorer BFF
```

前端不要直接拼十几个底层 API。

例如：

```http
GET /api/v2/explorer/address/:address/header
```

返回：

```text
identity
labels
balances
coverage
summary
```

---

# 92. Address Header API

目标一次返回：

```json
{
  "identity": {},
  "labels": [],
  "balances": {},
  "coverage": {},
  "summary": {}
}
```

减少首屏 waterfall。

---

# 93. Backend Contract 不绑定 ClickHouse 字段

前端使用：

```text
Canonical DTO
```

而不是：

```text
ClickHouse column name
```

防止以后内部表变更影响 UI。

---

# 94. Route

推荐：

```text
/explorer/:chain/address/:address
/explorer/:chain/tx/:txHash
/explorer/:chain/token/:token
/explorer/:chain/contract/:contract
/explorer/:chain/block/:block
```

---

# 95. P0 页面

必须优先：

```text
Explorer Search
Address
Transaction Detail
Token Transfer
Internal Tx
Contract Creation
Token Detail
Contract Detail
```

---

# 96. P0 分析

必须：

```text
Financial Summary
Counterparty
CEX Flow
Token Flow
Historical USD
```

---

# 97. P0 联动

必须：

```text
Explorer ↔ Fund Flow
Explorer ↔ Investigation
Explorer ↔ Data Assets
Explorer ↔ Export
```

上下文完整继承。

---

# 98. P1

```text
Retention
Pass-through
DEX Analytics
Bridge Analytics
Historical Balances
Related Wallets
PnL
```

---

# 99. P2

```text
Entity View
Cross-chain View
Contract Family
Protocol Analytics
Saved Workspaces
Comparison Mode
```

---

# 100. Comparison Mode

后期支持：

```text
Address A
vs
Address B
```

对比：

```text
Total Flow
CEX Ratio
DEX Volume
Counterparties
Retention
Pass-through
PnL
```

---

# 101. 验收 Case A：Address

输入地址。

首屏必须显示：

```text
Identity
EOA / Contract
Labels
Balances
Coverage
Tx Count
Token Transfer Count
Total In
Total Out
Netflow
```

---

# 102. 验收 Case B：USDT

真实 BSC USDT：

```text
0x55d398326f99059ff775485246999027b3197955
```

页面必须显示：

```text
正确 Logo
USDT
Tether USD
18
BEP20
Verified
```

假 USDT：

不得共用 Token Identity。

---

# 103. 验收 Case C：筛选继承

在：

```text
USDT
OUT
>= $100K
30D
```

状态点击：

```text
Fund Flow
```

必须仍是：

```text
USDT
OUT
>= $100K
30D
```

---

# 104. 验收 Case D：Investigation

同样条件启动 Investigation：

Agent Context 必须保留：

```text
Chain
Address
Time
Token
Direction
USD threshold
Entity filters
```

---

# 105. 验收 Case E：Historical USD

交易发生时：

```text
Token = $2
```

今天：

```text
Token = $10
```

Explorer 必须显示当时：

```text
Historical USD = Amount × $2
```

---

# 106. 验收 Case F：Price Missing

没有历史价格：

```text
--
Price unavailable
```

禁止：

```text
$0
```

---

# 107. 验收 Case G：Entity

同一 Binance Entity 10 个地址。

Address View：

显示各地址。

Entity View：

聚合：

```text
Binance
```

统计不能重复。

---

# 108. 验收 Case H：Related Wallet

显示：

```text
relationship
confidence
evidence
```

不能无证据标记：

```text
Same Owner
```

---

# 109. 验收 Case I：500 万行 Export

创建大型导出：

UI 必须：

```text
立即生成 Export Job
显示状态
不中断页面
```

复用现有流式 Export 后端能力。

---

# 110. 验收 Case J：100,001 Activity

前端连续分页：

```text
501 页
0 duplicate
0 missing
```

保持现有后端正确性。

---

# 111. 前端工程目录

建议：

```text
src/
├── pages/
│   └── explorer/
│       ├── AddressPage/
│       ├── TransactionPage/
│       ├── TokenPage/
│       ├── ContractPage/
│       └── BlockPage/
├── features/
│   ├── explorer-search/
│   ├── address-analytics/
│   ├── transaction-table/
│   ├── token-transfer-table/
│   ├── counterparty/
│   ├── retention/
│   ├── pass-through/
│   └── related-wallets/
├── components/
│   ├── AddressIdentity/
│   ├── TokenIdentity/
│   ├── EntityBadge/
│   ├── MethodBadge/
│   ├── Amount/
│   ├── USDValue/
│   ├── DataCoverage/
│   └── FilterBuilder/
└── context/
    └── AnalysisContext/
```

---

# 112. 后端模块

建议：

```text
internal/explorer/v2/
├── address.go
├── transaction.go
├── token.go
├── contract.go
├── block.go
├── search.go
├── dto.go
└── bff.go
```

只负责：

```text
产品 API
```

不重构底层 ClickHouse Repository。

---

# 113. 阶段实施顺序

严格建议：

```text
1 Design Tokens / Layout Shell
2 Global Search
3 Address Header
4 Transactions
5 Token Transfers
6 Internal Transactions
7 Transaction Detail
8 Token Detail
9 Contract Detail
10 Financial Overview
11 Counterparty
12 CEX / DEX / Bridge
13 Retention / Pass-through
14 Related Wallets
15 Fund Flow Context
16 Investigation Context
17 Data Assets Context
18 Export Context
19 Mobile / Responsive
20 Full Regression
```

---

# 114. 当前阶段不做

不要再做：

```text
新的下载器架构
数据库迁移
DuckDB Reader
Parquet Explorer
ClickHouse 重构
```

除非 QA 发现明确的数据缺陷。

---

# 115. 最终产品结构

```text
                    Explorer
                       │
        ┌──────────────┼──────────────┐
        │              │              │
      Address        Token         Transaction
        │
        ├──── Financial Analytics
        ├──── Counterparties
        ├──── Related Wallets
        ├──── Retention
        ├──── Pass-through
        ├──────── Fund Flow
        ├──────── Investigation
        ├──────── Data Assets
        └──────── Export
```

---

# 116. 产品定位

最终不是：

```text
OKLink Clone
```

而是：

```text
Explorer
+
Financial Intelligence
+
Entity Intelligence
+
Investigation
+
Fund Flow
+
Data Asset Platform
```

传统 Explorer 负责：

```text
发生了什么
```

本系统进一步回答：

```text
多少钱？
从哪里来？
去了哪里？
和谁关系最深？
多少进入交易所？
多少资金沉淀？
多少快速中转？
还有哪些相关钱包？
怎样继续往下追？
```

---

# 117. 本阶段完成标准

当用户输入任意已覆盖地址时：

无需进入其他底层工具，就能完成：

```text
看交易
看 Token
看内部交易
看历史 USD
看资金来源
看资金去向
看交易所交互
看沉淀
看中转
看相关地址
打开资金图继续追
启动 Investigation
筛选并导出证据
```

这才表示：

```text
Explorer Intelligence UI V1.0
COMPLETED
```

---

# 118. 下一阶段

Explorer Intelligence UI V1.0 完成后，建议进入：

```text
Investigation Intelligence V3.0
```

重点：

```text
目标驱动调查
自动证据链
资金路径优先级
实体落查
沉淀地址发现
CEX 落地地址识别
相关钱包扩展
调查结论置信度
调查过程可复现
一键生成案件报告
```

这会把已经建立的 Explorer、Graph、Entity、Financial Analytics 真正组织成完整调查工作流。
