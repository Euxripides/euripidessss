# EVM 多链链上数据分析平台 V1.3 开发计划（Codex部署版）

## 当前状态

V1.2 已完成：

-   SQD finalized-stream 接入
-   BSC / Ethereum / Base / Arbitrum 数据流
-   Logs
-   ERC20/BEP20 Transfer
-   ERC721
-   ERC1155
-   Trace
-   Internal Transaction
-   Address Activity基础层
-   多链 chain_key + chain_id
-   Parquet 数据仓库

当前系统已经从 ETL 工具升级为 EVM 数据采集平台。

------------------------------------------------------------------------

# V1.3 总目标

升级方向：

从：

多链数据采集

升级为：

多链地址分析平台。

重点：

1.  Token Metadata 富化
2.  多链 Transaction 统一
3.  Method Signature解析
4.  Address Activity V2
5.  Address Summary
6.  Balance Snapshot
7.  OKLink 类地址页面

------------------------------------------------------------------------

# Phase 1 Token Metadata

新增：

token_metadata

字段：

``` text
chain_id
token_address
name
symbol
decimals
standard
total_supply
logo_url
updated_at
source
```

规则：

-   不猜 symbol
-   不猜 decimals
-   RPC失败保留 UNKNOWN
-   chain_id + token_address 唯一

------------------------------------------------------------------------

# Phase 2 SQD Transaction Adapter

目标：

统一：

BSC

ETH

Base

Arbitrum

交易模型。

新增：

``` text
datasource/sqd/

transactions.go
receipts.go
```

统一字段：

``` text
chain_id
tx_hash
block_number
block_time
from
to
value_raw
input
method_id
status
gas_used
gas_price
```

------------------------------------------------------------------------

# Phase 3 Method Signature

新增：

method_signatures

字段：

``` text
method_id
signature
function_name
category
```

分类：

``` text
TRANSFER
APPROVE
SWAP
STAKE
MINT
BURN
CLAIM
OTHER
```

例如：

``` text
0xa9059cbb

transfer(address,uint256)
```

------------------------------------------------------------------------

# Phase 4 Address Activity V2

目标：

生成类似 OKLink 的统一流水。

字段：

``` text
chain_id
address
counterparty
direction
activity_type
asset_type
asset_address
symbol
amount_raw
amount
tx_hash
block_time
method_id
trace_depth
source
```

类型：

``` text
NATIVE_TRANSFER
TOKEN_TRANSFER
NFT_TRANSFER
INTERNAL_TRANSFER
CONTRACT_CREATE
CONTRACT_CALL
APPROVE
SWAP
```

------------------------------------------------------------------------

# Phase 5 Address Summary

新增：

address_summary

字段：

``` text
chain_id
address
address_type
tx_count
token_count
nft_count
contract_count
first_active_time
last_active_time
total_native_in
total_native_out
unique_counterparty_count
```

用于地址首页展示。

------------------------------------------------------------------------

# Phase 6 Balance Snapshot

新增：

balance_snapshot

字段：

``` text
chain_id
address
asset_type
asset_address
balance_raw
balance
snapshot_time
```

支持：

-   ETH
-   BNB
-   ERC20
-   BEP20

------------------------------------------------------------------------

# Phase 7 API和前端

新增接口：

``` text
/address/{address}/summary

/address/{address}/activity

/address/{address}/tokens

/address/{address}/nfts

/address/{address}/counterparties
```

页面：

-   地址概览
-   Token资产
-   NFT资产
-   流水
-   资金关系

------------------------------------------------------------------------

# 数据仓库

固定目录：

``` text
E:\codex\bsc_analytics
```

结构：

``` text
warehouse/

transactions
receipts
logs
token_transfers
nft_transfers
traces
internal_transactions
token_metadata
address_activity
address_summary
balances
```

所有表必须包含：

``` text
chain_key
chain_id
```

------------------------------------------------------------------------

# Codex开发要求

1.  保留V1.2功能。
2.  不破坏已有API。
3.  模块化开发。
4.  不允许写死BSC。
5.  原始数据和富化数据分离。
6.  不允许猜测Token信息。
7.  所有路径固定：

``` text
E:\codex\bsc_analytics
```

8.  禁止写入C盘。
9.  新增代码必须测试。
10. Schema必须先探测。

------------------------------------------------------------------------

# 完成标准

V1.3完成后：

系统具备：

-   多链交易分析
-   Token识别
-   NFT分析
-   Trace资金路径
-   Internal Transaction
-   地址画像
-   地址资产统计
-   OKLink风格地址详情

下一阶段：

V1.4：

-   标签系统
-   风险评分
-   地址聚类
-   资金路径图
-   ClickHouse在线查询
