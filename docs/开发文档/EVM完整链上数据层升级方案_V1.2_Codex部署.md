# EVM完整链上数据层升级方案 V1.2（Codex部署版）

## 目标

将当前系统从：

BSC Transaction 数据处理器

升级为：

EVM 多链完整链上数据采集与地址分析平台。

支持：

-   BSC
-   Ethereum
-   Base
-   Arbitrum
-   其他 EVM 链

------------------------------------------------------------------------

# 当前已完成能力

已完成：

-   批量地址导入
-   地址校验
-   地址去重
-   AWS Parquet 数据预检
-   分片下载
-   Range 断点续传
-   ETag 校验
-   DuckDB 批量过滤
-   ZSTD Parquet 输出
-   CSV 导出
-   非系统盘限制
-   任务进度管理
-   前端展示

当前数据：

AWS BNB Public Dataset：

-   blocks
-   transactions

当前支持：

-   普通交易
-   BNB 原生转账
-   顶层合约创建候选

------------------------------------------------------------------------

# 升级目标

补齐 OKLink 类链上数据需要的数据层：

## 必须支持

-   Transaction
-   Receipt
-   Logs
-   ERC20 Transfer
-   BEP20 Transfer
-   NFT Transfer
-   Trace
-   Internal Transaction
-   Contract Creation
-   Address Activity

------------------------------------------------------------------------

# 总体架构

``` text
                 Data Sources

AWS Parquet
    |
    |
SQD Logs / Trace
    |
    |
RPC
    |
    |
Commercial Dataset
    |
    v

Datasource Layer

    |
    v

Normalization Layer

    |
    v

EVM Unified Warehouse

    |
    +-------------+
    |             |
Address Activity  Analytics

    |
    v

API / Frontend
```

------------------------------------------------------------------------

# 数据源设计

## AWS

负责：

-   blocks
-   transactions

## SQD

负责：

-   logs
-   traces

## RPC

负责：

-   receipt
-   balance
-   contract code
-   token metadata

## 商业数据源

可选：

-   Bitquery
-   Allium
-   Goldsky

------------------------------------------------------------------------

# 目录设计

``` text
internal/

├── datasource/
│
│   ├── aws/
│   │    ├── blocks.go
│   │    └── transactions.go
│   │
│   ├── sqd/
│   │    ├── logs.go
│   │    └── traces.go
│   │
│   └── rpc/
│        ├── receipt.go
│        ├── balance.go
│        └── metadata.go
│
├── chain/
│
│   ├── evm.go
│   ├── bsc.go
│   └── eth.go
│
├── normalize/
│
│   ├── transaction.go
│   ├── transfer.go
│   ├── nft.go
│   └── trace.go
│
└── analytics/
```

------------------------------------------------------------------------

# 数据模型

所有表必须包含：

``` text
chain_key
chain_id
```

禁止：

``` text
tx_hash
address
```

单独作为跨链唯一键。

## transactions

保存：

-   外部交易

## receipts

保存：

-   status
-   gasUsed
-   contractAddress

## logs

保存：

-   原始事件

## token_transfers

支持：

-   ERC20
-   BEP20

字段：

``` text
chain_id

tx_hash

log_index

token_address

from_address

to_address

amount_raw

decimals

symbol
```

## nft_transfers

支持：

-   ERC721
-   ERC1155

字段：

``` text
chain_id

tx_hash

contract_address

token_id

from

to

amount
```

## traces

支持：

-   CALL
-   CREATE
-   CREATE2
-   DELEGATECALL
-   STATICCALL

字段：

``` text
chain_id

tx_hash

trace_id

from

to

value

call_type

input

output

status
```

## internal_transactions

由 Trace 生成：

``` text
chain_id

tx_hash

from

to

value

type
```

------------------------------------------------------------------------

# Token Transfer 解析

ERC20/BEP20：

事件：

``` solidity
Transfer(address,address,uint256)
```

Topic:

``` text
0xddf252ad1be2c89b69c2b068fc378daa...
```

解析：

topic1:

from

topic2:

to

data:

amount

------------------------------------------------------------------------

# Trace 解析

Trace 不属于普通交易。

必须独立存储。

用途：

-   内部转账
-   Swap路径
-   合约调用链
-   资金追踪

------------------------------------------------------------------------

# Address Activity

最终生成类似 OKLink 地址流水。

字段：

``` text
chain_id

address

counterparty

direction

activity_type

asset_type

token_address

symbol

amount

tx_hash

block_time
```

类型：

``` text
NATIVE_TRANSFER

TOKEN_TRANSFER

NFT_TRANSFER

INTERNAL_TRANSFER

CONTRACT_CREATE

CONTRACT_CALL
```

------------------------------------------------------------------------

# 多链设计

BSC:

``` yaml
chain_id: 56
native_symbol: BNB
```

ETH:

``` yaml
chain_id: 1
native_symbol: ETH
```

所有业务代码禁止写死：

-   BNB
-   BSC
-   BEP20
-   chain_id=56

必须通过 Chain Adapter。

------------------------------------------------------------------------

# 存储规范

项目目录：

``` text
E:\codex\bsc_analytics
```

禁止：

-   C盘
-   用户目录
-   Windows Temp

数据：

``` text
data/

├── staging

├── warehouse

│   ├── chain=bsc
│   └── chain=eth

├── exports

└── checkpoints
```

------------------------------------------------------------------------

# 开发顺序

## Phase 1

数据源抽象层

完成：

-   datasource interface
-   chain adapter

## Phase 2

Receipt

完成：

-   status
-   gas
-   contractAddress

## Phase 3

Logs

完成：

-   ERC20
-   BEP20

## Phase 4

NFT

完成：

-   ERC721
-   ERC1155

## Phase 5

Trace

完成：

-   Internal Transaction

## Phase 6

Address Activity

生成统一流水。

## Phase 7

Ethereum 接入。

------------------------------------------------------------------------

# Codex要求

1.  不破坏现有下载模块。
2.  不修改已有 API。
3.  新功能必须模块化。
4.  所有数据必须支持 chain_id。
5.  不允许假设字段，必须 Schema 探测。
6.  不允许把 Trace 和 Transaction 混合。
7.  不允许写入 C盘。
8.  所有新增代码必须有测试。
9.  项目路径固定：

``` text
E:\codex\bsc_analytics
```

------------------------------------------------------------------------

# 验收标准

完成后系统具备：

-   BSC完整交易分析
-   Token Transfer
-   NFT Transfer
-   Internal Transaction
-   Trace
-   合约创建
-   地址资金流水
-   ETH扩展能力

成为可扩展 EVM 链上分析平台基础版本。
