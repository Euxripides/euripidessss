# EVM 多链链上数据分析平台 V1.1 开发计划

## 当前状态

已完成：

-   批量地址导入（XLSX/CSV/TXT）
-   地址校验与去重
-   AWS BNB Parquet 真实预检
-   分片下载、Range 续传、ETag 检查
-   DuckDB 批量地址匹配
-   ZSTD Parquet 输出
-   CSV 导出
-   下载与处理流水线并行
-   非系统盘限制
-   前端进度展示
-   后端 API
-   本地端到端验证

当前已确认 AWS BNB 公共数据提供：

-   blocks
-   transactions

当前支持：

-   普通交易
-   原生 BNB 转账
-   顶层合约创建候选

当前不虚假支持：

-   BEP-20 Transfer
-   ERC-20 Transfer
-   NFT Transfer
-   Trace
-   Internal Transaction

原因：当前数据源未提供对应数据。

------------------------------------------------------------------------

# V1.1 目标

将当前工具升级为：

> 可扩展 EVM 多链数据采集与标准化平台。

重点：

1.  引入数据源适配层。
2.  建立统一 EVM 数据模型。
3.  接入 Receipt。
4.  准确识别合约创建。
5.  为 Token Transfer 接入准备。
6.  为 Ethereum 接入预留接口。

------------------------------------------------------------------------

# 一、架构升级

目录：

``` text
internal/

├── datasource/
│   ├── aws/
│   ├── sqd/
│   └── rpc/
│
├── chain/
│   ├── evm.go
│   ├── bsc.go
│   └── eth.go
│
├── normalize/
│   ├── transaction.go
│   ├── contract.go
│   └── transfer.go
│
└── analytics/
```

设计原则：

``` text
数据源
 ↓
Chain Adapter
 ↓
标准化模型
 ↓
分析层
```

------------------------------------------------------------------------

# 二、多链模型

所有表必须包含：

``` text
chain_key
chain_id
```

唯一键：

``` text
交易：
(chain_id, tx_hash)

日志：
(chain_id, tx_hash, log_index)

地址：
(chain_id, address)

Token：
(chain_id, token_address)
```

禁止只使用：

``` text
tx_hash
address
```

作为全局唯一标识。

------------------------------------------------------------------------

# 三、Receipt 接入

新增：

``` text
transaction_receipts
```

字段：

``` text
chain_id

tx_hash

status

gas_used

effective_gas_price

contract_address

logs_count
```

作用：

-   判断交易成功失败
-   获取真实 Gas
-   获取合约创建地址

------------------------------------------------------------------------

# 四、合约创建升级

当前：

``` text
transaction.to == null
```

只是候选。

升级：

``` text
transaction.to == null

AND

receipt.contractAddress != null
```

生成：

``` text
contract_creations
```

字段：

``` text
chain_id

creator

contract_address

tx_hash

block_number

block_time

creation_type

status
```

------------------------------------------------------------------------

# 五、Token Transfer 准备

新增适配：

``` text
SQD Logs Adapter
```

解析：

``` solidity
Transfer(address,address,uint256)
```

支持：

-   ERC20
-   BEP20
-   NFT Transfer

目标表：

``` text
token_transfers
```

字段：

``` text
chain_id

tx_hash

log_index

token_address

from_address

to_address

amount_raw

amount

symbol

decimals
```

------------------------------------------------------------------------

# 六、Address Activity

生成类似 OKLink 的统一流水：

``` text
address_activity
```

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

amount

tx_hash

block_time
```

类型：

``` text
NATIVE_TRANSFER

TOKEN_TRANSFER

CONTRACT_CREATE

CONTRACT_CALL
```

------------------------------------------------------------------------

# 七、ETH 扩展

增加：

``` text
EthChainAdapter
```

配置：

``` yaml
eth:
 enabled: true
 chain_id: 1
 native_symbol: ETH
```

不修改：

-   数据库结构
-   查询接口
-   前端逻辑

------------------------------------------------------------------------

# 八、存储规范

项目目录固定：

``` text
E:\codex\bsc_analytics
```

禁止：

``` text
C盘
用户目录
系统临时目录
```

长期保存：

``` text
标准化 Parquet
```

不保存：

``` text
原始 JSON
临时文件
重复 CSV
```

------------------------------------------------------------------------

# 九、性能目标

设备：

``` text
Core Ultra 9 185H

32GB RAM

3TB SSD
```

目标：

1000万记录本地处理：

``` text
30分钟以内
```

地址查询：

``` text
首页 <1秒

最近流水 <2秒

百万级统计 秒级
```

------------------------------------------------------------------------

# 十、Codex 开发要求

实现 V1.1：

1.  保留现有下载模块。
2.  不破坏现有 API。
3.  增加 datasource 层。
4.  增加 Chain Adapter。
5.  增加 Receipt 模块。
6.  增加准确 Contract Creation。
7.  预留 SQD Logs Adapter。
8.  所有表增加 chain_id。
9.  项目路径固定：

``` text
E:\codex\bsc_analytics
```

10. 禁止写入 C 盘。
11. 所有新增代码必须测试。
12. 不允许猜测数据源字段，必须 Schema 探测。

------------------------------------------------------------------------

# V1.1 完成标准

完成后：

-   BSC 交易分析
-   准确合约创建
-   多链数据模型
-   ETH 接入能力
-   Token Transfer 接入基础
-   OKLink 类地址流水架构

下一阶段：

V1.2：

-   SQD Logs
-   ERC20/BEP20 Transfer
-   Token Metadata
-   Address Asset Summary
