# EVM 多链批量资金分析系统 V1.0 设计文档

> 适用设备：Intel Core Ultra 9 185H、32 GB 内存、约 3 TB SSD  
> 当前实际可用空间：约 720 GB（总容量约 2.79 TB，已使用约 2.07 TB）  
> 操作系统：Windows 11 专业版  
> 当前首发链：BNB Smart Chain（BSC）  
> 后续扩展链：Ethereum Mainnet（ETH）及其他 EVM 链  
> 目标：批量处理地址的普通交易、原生币转账、ERC-20/BEP-20 代币转账、合约创建信息，并生成类似 OKLink 的地址流水与汇总结果。

---

## 1. 项目结论

这台电脑可以完成该项目，但 V1 不应直接搭建完整区块链节点，也不应一开始部署复杂的 ClickHouse 集群。

推荐路线：

```text
目标地址 Excel/CSV
        ↓
AWS BNB Chain 公共 Parquet
        ↓
DuckDB 批量筛选和解析
        ↓
本地精简 Parquet 数据仓库
        ↓
DuckDB 查询 API
        ↓
FastAPI 后端
        ↓
网页地址详情页
```

实时或近期增量数据使用：

```text
NodeReal RPC / SQD Portal
        ↓
增量 Parquet
        ↓
合并进入本地数据仓库
```

### 为什么 V1 先用 DuckDB，而不是 ClickHouse

你的主要需求是个人电脑上的批量分析，不是多人同时访问的企业平台。

DuckDB 的优势：

- Windows 原生运行；
- 无需启动数据库服务器；
- 直接查询 Parquet；
- 部署简单；
- 适合千万级到数亿级离线分析；
- 便于 Codex 自动生成项目；
- 后期可以迁移到 ClickHouse。

V1 先完成“能下载、能筛选、能查询、能导出”。当数据持续增长、需要多人并发访问时，再增加 ClickHouse。

---

## 2. V1 功能范围

### 2.1 必须实现

1. 导入批量 BSC 地址。
2. 地址格式校验、统一小写和去重。
3. 普通交易记录。
4. 原生 BNB 转账。
5. BEP-20 代币转账。
6. 顶层合约创建记录。
7. 地址是 EOA 还是合约。
8. 当前 BNB 余额。
9. 指定代币当前余额。
10. 地址交易汇总。
11. 对手方统计。
12. 按时间、方向、代币、交易状态筛选。
13. CSV 和 Parquet 导出。
14. 断点续传。
15. 失败任务重试。
16. 增量更新。

### 2.2 V1 暂不实现

- Trace 内部调用；
- 内部 BNB 转账；
- Opcode Trace；
- NFT 完整解析；
- DEX Swap 精确语义解析；
- 地址风险标签；
- 交易所充值地址聚类；
- 多链支持；
- 完整复制 OKLink。

这些功能留到 V2，避免第一版过度复杂。

---


## 2.3 多链扩展原则

系统从第一天开始按 EVM 多链架构设计，BSC 只是首发链，后续接入 Ethereum 时不重构现有业务模型。

第一阶段启用：

```text
BSC Mainnet
chain_key = bsc
chain_id = 56
native_asset = BNB
token_standard = BEP-20
```

后续接入：

```text
Ethereum Mainnet
chain_key = eth
chain_id = 1
native_asset = ETH
token_standard = ERC-20
```

统一架构：

```text
统一业务模型
    ↑
Chain Adapter 接口
    ↑
BSC Adapter / ETH Adapter / 其他 EVM Adapter
    ↑
各链 Parquet、RPC、SQD 或商业数据源
```

所有业务表、任务、检查点、导出和唯一键必须包含 `chain_key` 或 `chain_id`。

唯一键规范：

```text
交易：(chain_id, tx_hash)
日志：(chain_id, tx_hash, log_index)
地址：(chain_id, address)
Token：(chain_id, token_address)
合约：(chain_id, contract_address)
```

禁止仅使用地址或交易哈希作为跨链全局唯一键。

### Chain Adapter 接口

```python
class ChainAdapter:
    chain_key: str
    chain_id: int
    native_symbol: str
    native_decimals: int

    def discover_source_files(...): ...
    def normalize_transaction_schema(...): ...
    def normalize_log_schema(...): ...
    def build_rpc_client(...): ...
    def get_latest_block(...): ...
    def get_code(...): ...
    def get_native_balance(...): ...
    def get_transaction_receipt(...): ...
    def get_token_metadata(...): ...
```

通用业务层不得写死：

```text
BNB
BSC
chain_id = 56
BEP-20
NodeReal
AWS BNB 固定目录
```

### 多链配置示例

```yaml
chains:
  bsc:
    enabled: true
    chain_id: 56
    native_symbol: BNB
    native_decimals: 18
    rpc_url_env: BSC_RPC_URL
    source_type: aws_parquet
    source_root: "s3://aws-public-blockchain/v1.1/bnb/"
    finality_confirmations: 30

  eth:
    enabled: false
    chain_id: 1
    native_symbol: ETH
    native_decimals: 18
    rpc_url_env: ETH_RPC_URL
    source_type: configurable
    source_root: ""
    finality_confirmations: 64
```

ETH 接入时只需新增 `EthChainAdapter`、配置 ETH 历史数据源和 RPC，不修改 BSC 业务逻辑。


## 3. 数据源设计

## 3.1 历史数据：AWS Public Blockchain Data

AWS Public Blockchain Data 提供压缩后的 Parquet 数据，并按日期分区。BNB Chain 数据路径：

```text
s3://aws-public-blockchain/v1.1/bnb/
```

AWS 区域：

```text
us-east-2
```

匿名列目录：

```powershell
aws s3 ls --no-sign-request s3://aws-public-blockchain/v1.1/bnb/
```

递归查看文件：

```powershell
aws s3 ls --no-sign-request `
  s3://aws-public-blockchain/v1.1/bnb/ `
  --recursive |
Select-Object -First 200
```

注意：

- 不要直接执行整个目录的 `aws s3 sync`；
- 先查看实际子目录和 Schema；
- 每次只下载一个日期或少量文件测试；
- 公开数据的目录结构可能调整，代码不能写死未经验证的字段名。

官方说明：

- 数据是压缩 Parquet；
- 按日期分区；
- 每日更新；
- 可匿名访问；
- BNB Chain 路径为上述 S3 地址。

参考：

- https://registry.opendata.aws/aws-public-blockchain/

## 3.2 当前状态：NodeReal RPC

RPC 仅负责：

- 最新区块高度；
- `eth_getCode`：判断 EOA 或合约；
- `eth_getBalance`：当前 BNB 余额；
- `eth_call balanceOf`：当前代币余额；
- 必要时补充交易回执。

RPC 不负责下载地址全量历史。

配置示例：

```env
BSC_RPC_URL=https://bsc-mainnet.nodereal.io/v1/YOUR_API_KEY
ETH_RPC_URL=https://YOUR_ETH_RPC_ENDPOINT
```

## 3.3 增量数据

优先级：

1. AWS 每日新增 Parquet；
2. SQD Portal 补充当天尚未进入 AWS 的区块；
3. RPC 只补少量缺失数据。

增量更新流程：

```text
读取 checkpoint
      ↓
确定上次完成日期/区块
      ↓
获取新增数据
      ↓
解析并写入临时分区
      ↓
校验
      ↓
原子移动到正式目录
      ↓
更新 checkpoint
```

---

## 4. 存储规划

当前剩余空间约 720 GB，必须保留至少 150 GB 空闲空间，避免 Windows、临时排序和 Parquet 写入失败。

建议最大可用数据空间：

```text
约 500 GB
```

目录设计：

```text
E:\codex\bsc_analytics\
├─ app\
│  ├─ api\
│  ├─ core\
│  ├─ jobs\
│  ├─ parsers\
│  ├─ services\
│  └─ tests\
│
├─ config\
│  ├─ settings.yaml
│  └─ .env
│
├─ data\
│  ├─ input\
│  │  └─ target_addresses.xlsx
│  │
│  ├─ staging\
│  │  ├─ transactions\
│  │  ├─ logs\
│  │  └─ receipts\
│  │
│  ├─ warehouse\
│  │  ├─ transactions\
│  │  ├─ token_transfers\
│  │  ├─ contract_creations\
│  │  ├─ address_activity\
│  │  ├─ address_summary\
│  │  └─ token_metadata\
│  │
│  ├─ exports\
│  └─ checkpoints\
│
├─ logs\
├─ scripts\
├─ sql\
├─ tests\
├─ requirements.txt
├─ pyproject.toml
└─ README.md
```

### 空间分配建议

| 用途 | 建议上限 |
|---|---:|
| staging 临时下载 | 150 GB |
| warehouse 精简数据 | 250 GB |
| 导出文件 | 30 GB |
| DuckDB 临时目录 | 50 GB |
| 日志、检查点、项目文件 | 20 GB |
| 必须保留空闲空间 | 150 GB |

处理完一个 staging 分片并完成校验后，应立即删除原始临时文件。

不要长期同时保存：

```text
原始 JSON
+ 解压 JSON
+ CSV
+ 原始 Parquet
+ 精简 Parquet
```

V1 长期只保留：

```text
精简 Parquet
+ DuckDB 元数据库
+ 必要导出文件
```

---


## 4.1 强制磁盘路径限制：禁止使用 C 盘

本项目所有业务文件、缓存、数据库文件、临时文件、日志、下载文件和导出文件，**一律禁止写入 C 盘**。

允许的默认数据根目录：

```text
E:\codex\bsc_analytics\
```

如果后续增加其他数据盘，也允许配置为：

```text
E:\codex\bsc_analytics\
F:\bsc_analytics\
```

但不得配置为：

```text
C:\bsc_analytics\
C:\Users\...
C:\ProgramData\...
C:\Windows\Temp\...
```


### 项目根目录约束

项目代码、配置、日志、数据库、缓存、临时文件、下载文件、仓库文件和导出文件统一放在：

```text
E:\codex\bsc_analytics\
```

推荐结构：

```text
E:\codex\bsc_analytics\
├─ app\
├─ config\
├─ data\
├─ logs\
├─ scripts\
├─ sql\
├─ tests\
├─ .env
├─ pyproject.toml
└─ README.md
```

不得使用 C 盘、用户目录、系统临时目录或未配置的相对路径作为隐式存储位置。


### 必须禁止写入 C 盘的内容

包括但不限于：

- AWS Parquet 下载文件；
- `.partial` 临时下载文件；
- DuckDB 数据库文件；
- DuckDB 临时排序目录；
- Parquet 中间文件；
- 精简后的 warehouse 数据；
- ClickHouse 数据目录；
- 日志文件；
- checkpoint；
- manifest；
- 导出 CSV、Excel、Parquet；
- Python 程序生成的缓存；
- 浏览器上传后的临时文件；
- FastAPI 上传文件；
- Token metadata 缓存；
- RPC 返回缓存；
- 测试数据；
- 数据校验报告。

### 配置要求

`.env`：

```env
BSC_DATA_ROOT=E:\codex\bsc_analytics
BSC_TEMP_DIR=E:\codex\bsc_analytics\data\tmp
BSC_STAGING_DIR=E:\codex\bsc_analytics\data\staging
BSC_WAREHOUSE_DIR=E:\codex\bsc_analytics\data\warehouse
BSC_EXPORT_DIR=E:\codex\bsc_analytics\data\exports
BSC_LOG_DIR=E:\codex\bsc_analytics\logs
DUCKDB_PATH=E:\codex\bsc_analytics\data\metadata\bsc_analytics.duckdb
```

`settings.yaml`：

```yaml
storage:
  root_dir: "E:/codex/bsc_analytics"
  staging_dir: "E:/codex/bsc_analytics/data/staging"
  warehouse_dir: "E:/codex/bsc_analytics/data/warehouse"
  temp_dir: "E:/codex/bsc_analytics/data/tmp"
  export_dir: "E:/codex/bsc_analytics/data/exports"
  log_dir: "E:/codex/bsc_analytics/logs"
  duckdb_path: "E:/codex/bsc_analytics/data/metadata/bsc_analytics.duckdb"
  forbid_system_drive: true
```

### 启动时强制校验

程序每次启动时必须：

1. 解析所有配置路径；
2. 取得 Windows 系统盘；
3. 如果任意业务路径位于系统盘，立即终止；
4. 输出明确错误；
5. 不允许自动回退到当前目录、用户目录或系统临时目录。

伪代码：

```python
from pathlib import Path
import os

def ensure_not_system_drive(path_value: str) -> Path:
    path = Path(path_value).expanduser().resolve()
    system_drive = os.environ.get("SystemDrive", "C:").upper()

    if path.drive.upper() == system_drive:
        raise RuntimeError(
            f"禁止将业务数据写入系统盘：{path}. "
            "请将路径配置到 D 盘、E 盘或其他非系统盘。"
        )

    return path
```

### 临时目录限制

必须显式设置 DuckDB 临时目录：

```sql
SET temp_directory = 'E:/codex/bsc_analytics/data/tmp';
```

Python 临时目录应在程序启动前设置：

```python
import os

os.environ["TMP"] = r"E:\codex\bsc_analytics\data\tmp"
os.environ["TEMP"] = r"E:\codex\bsc_analytics\data\tmp"
os.environ["TMPDIR"] = r"E:\codex\bsc_analytics\data\tmp"
```

不得使用：

```python
tempfile.gettempdir()
```

除非程序已经确认其返回值不在 C 盘。

### 下载限制

AWS CLI 下载命令必须明确指定 D 盘路径：

```powershell
aws s3 cp --no-sign-request `
  "s3://实际文件路径/file.parquet" `
  "E:\codex\bsc_analytics\data\staging\file.parquet"
```

禁止使用相对路径：

```powershell
aws s3 cp s3://... .\
```

因为相对路径可能落到 C 盘。

### 日志限制

日志必须写入：

```text
E:\codex\bsc_analytics\logs\
```

不得写入：

```text
C:\Users\<用户名>\AppData\
C:\ProgramData\
```

### 测试要求

自动测试必须覆盖：

- 配置为 `C:\bsc_analytics` 时启动失败；
- staging 位于 C 盘时启动失败；
- temp 位于 C 盘时启动失败；
- exports 位于 C 盘时启动失败；
- DuckDB 文件位于 C 盘时启动失败；
- 所有路径位于 D 盘时正常启动；
- 相对路径必须先解析为绝对路径，再检查盘符；
- 路径不存在时可以自动创建，但只能创建在非系统盘。

### Codex 强制要求

Codex 实现时必须遵守：

```text
任何业务文件不得写入 C 盘。
任何默认路径不得指向 C 盘。
任何路径未配置时，程序必须拒绝启动，而不是回退到 C 盘。
必须编写系统盘路径拦截器和自动化测试。
```


## 5. 技术栈

### 核心

- Python 3.12
- DuckDB
- PyArrow
- Polars
- FastAPI
- Uvicorn
- Pydantic
- httpx
- tenacity
- openpyxl
- rich
- typer

### 可选

- React 或 Vue 3：地址查询网页；
- Plotly：资金流和时间序列图；
- NetworkX：对手方关系图；
- ClickHouse：V2 高并发查询；
- Redis：V2 查询缓存。

### requirements.txt 建议

```text
duckdb>=1.4
pyarrow>=18
polars>=1.20
pandas>=2.2
openpyxl>=3.1
fastapi>=0.115
uvicorn[standard]>=0.34
pydantic>=2.10
pydantic-settings>=2.7
httpx>=0.28
tenacity>=9.0
rich>=13.9
typer>=0.15
python-dotenv>=1.0
orjson>=3.10
eth-utils>=5.1
web3>=7.6
```

版本号应在实际创建项目时重新锁定，并生成：

```text
requirements.lock
```

---

## 6. 数据处理总流程

```text
阶段 A：导入地址
        ↓
阶段 B：确定分析时间范围
        ↓
阶段 C：发现并下载 Parquet 文件
        ↓
阶段 D：检查 Schema
        ↓
阶段 E：一次扫描匹配全部目标地址
        ↓
阶段 F：解析 Transactions
        ↓
阶段 G：解析 Transfer Logs
        ↓
阶段 H：识别合约创建
        ↓
阶段 I：生成 Address Activity
        ↓
阶段 J：生成 Address Summary
        ↓
阶段 K：RPC 补充当前状态
        ↓
阶段 L：校验、导出、提供 API
```

关键原则：

> 一个日期分区只扫描一次，同时匹配整批目标地址；绝对不要为每个地址单独扫描完整数据。

---

## 7. 地址导入

输入支持：

- `.xlsx`
- `.csv`
- `.txt`

标准输入字段：

| 字段 | 必需 | 说明 |
|---|---|---|
| address | 是 | BSC 地址 |
| label | 否 | 自定义标签 |
| source | 否 | 地址来源 |
| batch_id | 否 | 批次编号 |

清洗规则：

1. 去除首尾空格；
2. 转为小写；
3. 必须以 `0x` 开头；
4. 总长度必须为 42；
5. 必须为合法十六进制；
6. 去重；
7. 保留原始行号；
8. 无效地址单独输出。

建议内部存储两种形式：

```text
address_text  VARCHAR
address_bytes BLOB
```

展示时使用字符串，批量匹配时优先使用 20 字节二进制地址。

---

## 8. 数据表设计

## 8.1 target_addresses

```sql
CREATE TABLE IF NOT EXISTS target_addresses (
    address VARCHAR PRIMARY KEY,
    address_bytes BLOB,
    label VARCHAR,
    source VARCHAR,
    batch_id VARCHAR,
    imported_at TIMESTAMP
);
```

## 8.2 transactions

保存与目标地址相关的交易，不保存整条 BSC 的所有交易。

```sql
CREATE TABLE IF NOT EXISTS transactions (
    chain VARCHAR,
    chain_id UINTEGER,
    block_number UBIGINT,
    block_time TIMESTAMP,
    tx_hash VARCHAR,
    tx_index UINTEGER,
    from_address VARCHAR,
    to_address VARCHAR,
    value_raw HUGEINT,
    value_bnb DECIMAL(38, 18),
    nonce UBIGINT,
    input VARCHAR,
    method_id VARCHAR,
    gas UBIGINT,
    gas_price_raw HUGEINT,
    gas_used UBIGINT,
    status UTINYINT,
    is_contract_creation BOOLEAN,
    source_date DATE,
    source_file VARCHAR,
    ingested_at TIMESTAMP
);
```

去重键：

```text
(chain, tx_hash)
```

## 8.3 token_transfers

```sql
CREATE TABLE IF NOT EXISTS token_transfers (
    chain VARCHAR,
    chain_id UINTEGER,
    block_number UBIGINT,
    block_time TIMESTAMP,
    tx_hash VARCHAR,
    log_index UINTEGER,
    token_address VARCHAR,
    from_address VARCHAR,
    to_address VARCHAR,
    amount_raw VARCHAR,
    amount_decimal DECIMAL(38, 18),
    token_name VARCHAR,
    token_symbol VARCHAR,
    token_decimals UTINYINT,
    standard VARCHAR,
    status UTINYINT,
    source_date DATE,
    source_file VARCHAR,
    ingested_at TIMESTAMP
);
```

去重键：

```text
(chain, tx_hash, log_index)
```

`amount_raw` 建议先用字符串保存，避免超过常规整数范围。获得 decimals 后再生成 `amount_decimal`。

## 8.4 contract_creations

```sql
CREATE TABLE IF NOT EXISTS contract_creations (
    chain VARCHAR,
    chain_id UINTEGER,
    block_number UBIGINT,
    block_time TIMESTAMP,
    tx_hash VARCHAR,
    creator_address VARCHAR,
    contract_address VARCHAR,
    creation_type VARCHAR,
    init_code_size UINTEGER,
    status UTINYINT,
    source_date DATE,
    ingested_at TIMESTAMP
);
```

V1 的 `creation_type` 固定为：

```text
TOP_LEVEL
```

内部 `CREATE` 和 `CREATE2` 留到 V2 Trace 模块。

## 8.5 token_metadata

```sql
CREATE TABLE IF NOT EXISTS token_metadata (
    chain VARCHAR,
    chain_id UINTEGER,
    token_address VARCHAR,
    token_name VARCHAR,
    token_symbol VARCHAR,
    token_decimals UTINYINT,
    total_supply_raw VARCHAR,
    metadata_status VARCHAR,
    first_seen_block UBIGINT,
    updated_at TIMESTAMP,
    PRIMARY KEY (chain, token_address)
);
```

## 8.6 address_activity

这是网页和分析查询的主表。

```sql
CREATE TABLE IF NOT EXISTS address_activity (
    chain VARCHAR,
    chain_id UINTEGER,
    address VARCHAR,
    counterparty VARCHAR,
    direction VARCHAR,
    activity_type VARCHAR,
    asset_type VARCHAR,
    asset_address VARCHAR,
    asset_symbol VARCHAR,
    amount_raw VARCHAR,
    amount_decimal DECIMAL(38, 18),
    tx_hash VARCHAR,
    event_index UINTEGER,
    block_number UBIGINT,
    block_time TIMESTAMP,
    method_id VARCHAR,
    status UTINYINT,
    source_table VARCHAR,
    source_date DATE
);
```

`activity_type`：

```text
NATIVE_TRANSFER
TOKEN_TRANSFER
CONTRACT_CALL
CONTRACT_CREATE
```

`direction`：

```text
IN
OUT
SELF
CREATE
CALL
```

同一笔 A 向 B 转账，为命中的目标地址生成对应活动行：

```text
A | B | OUT
B | A | IN
```

只有 A 在目标地址列表时，只生成 A 的记录；没有必要保存与目标集合无关的 B 地址活动行。

## 8.7 address_summary

```sql
CREATE TABLE IF NOT EXISTS address_summary (
    chain VARCHAR,
    chain_id UINTEGER,
    address VARCHAR,
    address_type VARCHAR,
    tx_count UBIGINT,
    success_tx_count UBIGINT,
    failed_tx_count UBIGINT,
    native_in_count UBIGINT,
    native_out_count UBIGINT,
    native_in_amount DECIMAL(38, 18),
    native_out_amount DECIMAL(38, 18),
    token_transfer_count UBIGINT,
    token_in_count UBIGINT,
    token_out_count UBIGINT,
    contract_created_count UBIGINT,
    unique_counterparty_count UBIGINT,
    first_active_time TIMESTAMP,
    last_active_time TIMESTAMP,
    latest_indexed_block UBIGINT,
    updated_at TIMESTAMP,
    PRIMARY KEY (chain, address)
);
```

---

## 9. Transactions 处理规则

目标交易匹配条件：

```sql
lower(from_address) IN target_addresses
OR lower(to_address) IN target_addresses
OR (
    to_address IS NULL
    AND lower(from_address) IN target_addresses
)
```

保存规则：

- `value = 0` 也保存，因为可能是合约调用；
- 失败交易也保存；
- `input` 为空或 `0x`：普通转账；
- `input` 长度至少 10：提取前 4 字节方法 ID；
- `to IS NULL`：顶层合约创建候选；
- `status` 从数据源字段或 receipt 补充；
- BNB 数量为 `value_raw / 10^18`。

方法 ID：

```python
method_id = input_data[:10] if input_data and len(input_data) >= 10 else None
```

---

## 10. BEP-20 Transfer 解析

标准事件：

```solidity
Transfer(address indexed from, address indexed to, uint256 value)
```

事件 Topic0：

```text
0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef
```

标准 BEP-20 日志通常满足：

```text
topics[0] = Transfer Topic0
topics[1] = from
topics[2] = to
data      = amount
```

地址提取：

```python
from_address = "0x" + topic1[-40:]
to_address = "0x" + topic2[-40:]
```

金额：

```python
amount_raw = int(data, 16)
amount = amount_raw / (10 ** decimals)
```

必须处理：

- 零地址 Mint；
- 零地址 Burn；
- 自转账；
- 非标准代币；
- ERC-721 与 BEP-20 使用相同 Transfer Topic0 的情况；
- 缺少 decimals；
- metadata RPC 调用失败。

V1 区分策略：

- topics 数量为 3，金额在 data：优先判定为 BEP-20；
- topics 数量为 4：优先判定为 ERC-721，V1 可标记但不完整解析；
- 最终以合约接口和 metadata 探测结果校正。

不要因为 symbol 或 name 获取失败就丢弃 Transfer。应保存原始金额和合约地址。

---

## 11. 合约创建识别

V1 只处理顶层合约创建。

判断条件：

```text
transaction.to IS NULL
AND transaction.status = 1
AND receipt.contract_address IS NOT NULL
```

保存：

- 创建者；
- 新合约地址；
- 交易哈希；
- 区块；
- 时间；
- 初始化代码大小；
- 状态。

如果公开 Parquet 中没有 `contract_address`：

1. 收集创建交易哈希；
2. 使用 RPC 批量调用 `eth_getTransactionReceipt`；
3. 读取 `contractAddress`；
4. 结果写入本地缓存；
5. 不重复查询已经成功补充的交易。

---

## 12. Parquet 数据仓库设计

采用 Hive 分区：

```text
data\warehouse\transactions\
└─ chain=bsc\
   └─ year=2026\
      └─ month=07\
         └─ part-00001.parquet
```

其他表同样按年月分区。

推荐：

```text
压缩：ZSTD
单文件目标大小：256 MB～512 MB
Row Group：100,000～500,000 行
```

DuckDB 写入示例：

```sql
COPY (
    SELECT *
    FROM temp_transactions
)
TO 'E:/codex/bsc_analytics/data/warehouse/transactions/chain=bsc/year=2026/month=07/part-00001.parquet'
(
    FORMAT PARQUET,
    COMPRESSION ZSTD,
    ROW_GROUP_SIZE 250000
);
```

DuckDB 支持 Parquet 的列裁剪和过滤下推，因此查询时只读取需要的列，并尽可能跳过不匹配的 Row Group。

参考：

- https://duckdb.org/docs/stable/data/parquet/overview
- https://duckdb.org/docs/stable/core_extensions/httpfs/overview

---

## 13. 文件发现与下载模块

文件发现器职责：

1. 根据日期范围列出 S3 文件；
2. 识别 transactions、logs、receipts 等数据类别；
3. 获取文件路径、大小和更新时间；
4. 写入 manifest；
5. 避免重复下载。

manifest 表：

```sql
CREATE TABLE IF NOT EXISTS source_manifest (
    source_uri VARCHAR PRIMARY KEY,
    data_type VARCHAR,
    source_date DATE,
    size_bytes UBIGINT,
    etag VARCHAR,
    status VARCHAR,
    local_path VARCHAR,
    retry_count UINTEGER,
    discovered_at TIMESTAMP,
    completed_at TIMESTAMP,
    error_message VARCHAR
);
```

状态：

```text
DISCOVERED
DOWNLOADING
DOWNLOADED
PROCESSING
COMPLETED
FAILED
DELETED
```

下载流程：

```text
下载到 .partial
      ↓
文件大小校验
      ↓
Parquet footer 校验
      ↓
重命名为正式文件
      ↓
写 manifest
```

下载命令可先使用 AWS CLI：

```powershell
aws s3 cp --no-sign-request `
  "s3://实际文件路径/file.parquet" `
  "E:\codex\bsc_analytics\data\staging\file.parquet"
```

后续再实现 Python 下载器。

---

## 14. Schema 自适应

首次读取任何新目录时，必须运行：

```sql
DESCRIBE
SELECT *
FROM read_parquet('E:/codex/bsc_analytics/data/staging/test/*.parquet');
```

保存 Schema 快照：

```text
config/schema_snapshots/
```

代码中建立字段映射，不直接假设源字段名：

```yaml
transactions:
  block_number:
    - block_number
    - blockNumber
    - number
  tx_hash:
    - hash
    - transaction_hash
    - transactionHash
  from_address:
    - from
    - from_address
    - fromAddress
  to_address:
    - to
    - to_address
    - toAddress
```

如果必需字段缺失：

- 停止当前数据类型任务；
- 记录明确错误；
- 不允许静默写入错误结果。

---

## 15. 批量匹配策略

三万个地址不应拼接成超长 `IN (...)` SQL。

先导入 DuckDB 表：

```sql
CREATE OR REPLACE TABLE target_addresses AS
SELECT DISTINCT lower(address) AS address
FROM read_csv_auto('target_addresses.csv');
```

推荐分别匹配 from 和 to，然后 `UNION`，避免带 `OR` 的 JOIN 性能不稳定：

```sql
CREATE TEMP TABLE matched_from AS
SELECT t.*
FROM source_transactions t
SEMI JOIN target_addresses a
ON lower(t.from_address) = a.address;

CREATE TEMP TABLE matched_to AS
SELECT t.*
FROM source_transactions t
SEMI JOIN target_addresses a
ON lower(t.to_address) = a.address;

CREATE TEMP TABLE matched_transactions AS
SELECT * FROM matched_from
UNION
SELECT * FROM matched_to;
```

Token Transfer 同理。

如果源地址列已经是二进制，应直接用二进制匹配，不要在千万行扫描过程中反复执行 `lower()`。

---

## 16. 任务系统

V1 不使用 Celery、Kafka 等重型组件。

使用 SQLite 或 DuckDB 任务表：

```sql
CREATE TABLE IF NOT EXISTS jobs (
    job_id VARCHAR PRIMARY KEY,
    job_type VARCHAR,
    source_uri VARCHAR,
    data_type VARCHAR,
    start_date DATE,
    end_date DATE,
    status VARCHAR,
    progress DOUBLE,
    processed_rows UBIGINT,
    output_rows UBIGINT,
    started_at TIMESTAMP,
    updated_at TIMESTAMP,
    finished_at TIMESTAMP,
    error_message VARCHAR
);
```

任务类型：

```text
DISCOVER_FILES
DOWNLOAD_FILE
INSPECT_SCHEMA
PROCESS_TRANSACTIONS
PROCESS_LOGS
FETCH_RECEIPTS
FETCH_TOKEN_METADATA
BUILD_ACTIVITY
BUILD_SUMMARY
VERIFY_BATCH
EXPORT_RESULT
INCREMENTAL_SYNC
```

每个任务必须：

- 可重复执行；
- 保持幂等；
- 有进度；
- 有日志；
- 有重试；
- 可从 checkpoint 继续；
- 不因单个文件失败而破坏全部结果。

---

## 17. 并发和资源限制

针对 32 GB 内存：

```text
DuckDB memory_limit：20 GB
DuckDB threads：12～16
临时目录：E:\codex\bsc_analytics\data\tmp
下载并发：2～4
Parquet 处理并发：1～2
RPC 并发：5～10
```

DuckDB 初始化：

```sql
SET memory_limit = '20GB';
SET threads = 14;
SET temp_directory = 'E:/codex/bsc_analytics/data/tmp';
SET preserve_insertion_order = false;
```

不要同时启动多个大型 DuckDB 扫描任务，否则每个进程都可能占用大量内存。

推荐架构：

```text
1 个主处理进程
2～4 个下载协程
1 个 Parquet 写入任务
1 个 RPC 补充队列
```

---

## 18. API 设计

FastAPI 只读取本地仓库，不直接在网页请求中扫描远程 S3。

### 地址摘要

```http
GET /api/v1/address/{address}/summary
```

### 地址流水

```http
GET /api/v1/address/{address}/activities
```

参数：

```text
cursor
limit
activity_type
direction
token_address
start_time
end_time
status
```

### 普通交易

```http
GET /api/v1/address/{address}/transactions
```

### Token Transfer

```http
GET /api/v1/address/{address}/token-transfers
```

### 合约创建

```http
GET /api/v1/address/{address}/contracts-created
```

### 对手方排行

```http
GET /api/v1/address/{address}/counterparties
```

### 导出任务

```http
POST /api/v1/exports
GET  /api/v1/exports/{job_id}
```

网页分页必须使用游标，不使用大偏移量：

```text
错误：OFFSET 1000000
正确：block_time + block_number + tx_hash + event_index 游标
```

---

## 19. 查询示例

地址最近流水：

```sql
SELECT *
FROM read_parquet(
    'E:/codex/bsc_analytics/data/warehouse/address_activity/**/*.parquet',
    hive_partitioning = true
)
WHERE address = ?
ORDER BY block_time DESC, block_number DESC, event_index DESC
LIMIT 100;
```

地址代币流水：

```sql
SELECT *
FROM read_parquet(
    'E:/codex/bsc_analytics/data/warehouse/token_transfers/**/*.parquet',
    hive_partitioning = true
)
WHERE from_address = ?
   OR to_address = ?
ORDER BY block_time DESC, log_index DESC
LIMIT 100;
```

主要对手方：

```sql
SELECT
    counterparty,
    count(*) AS activity_count,
    min(block_time) AS first_seen,
    max(block_time) AS last_seen
FROM read_parquet(
    'E:/codex/bsc_analytics/data/warehouse/address_activity/**/*.parquet',
    hive_partitioning = true
)
WHERE address = ?
  AND counterparty IS NOT NULL
GROUP BY counterparty
ORDER BY activity_count DESC
LIMIT 100;
```

---

## 20. 数据校验

每个分片完成后至少执行：

### 行数校验

```text
源文件行数
扫描行数
目标命中行数
输出行数
```

### 唯一性校验

Transactions：

```text
tx_hash 不重复
```

Token Transfers：

```text
(tx_hash, log_index) 不重复
```

### 金额校验

- BNB 原始值不可为负；
- Token 原始值不可为负；
- decimals 必须为 0～255；
- 转换失败时保留 raw，不丢记录。

### 地址校验

- from/to/token/contract 地址格式；
- 零地址允许存在；
- NULL 与零地址不能混淆。

### 抽样核验

每个批次随机抽取：

```text
50～200 笔交易
```

与 BscScan、OKLink 或 RPC 结果人工核对。

### 完整性指标

输出批次报告：

```json
{
  "source_files": 20,
  "completed_files": 20,
  "failed_files": 0,
  "source_rows": 125000000,
  "matched_transactions": 4200000,
  "matched_token_transfers": 8700000,
  "contract_creations": 1260,
  "duplicates_removed": 32,
  "metadata_missing_tokens": 17
}
```

---

## 21. 性能预估

以下是工程估算，不是固定承诺。速度主要取决于：

- 需要扫描多少原始 Parquet；
- 网络实际下载速度；
- AWS 文件分区和字段布局；
- 地址活跃时间是否跨越多年；
- SSD 的实际持续读写速度；
- 当前剩余磁盘空间。

### 已有本地 Parquet 时

| 原始扫描规模 | 预计处理时间 |
|---:|---:|
| 1,000 万行 | 5～20 分钟 |
| 1 亿行 | 20～90 分钟 |
| 5 亿行 | 1～5 小时 |
| 10 亿行 | 3～10 小时 |

包括读取、地址匹配、写精简 Parquet，不包括远程下载。

### 最终命中 1,000 万条记录

如果为得到这些结果需要扫描：

| 原始数据量 | 完整流程估算 |
|---:|---:|
| 5,000 万～1 亿行 | 30 分钟～2 小时 |
| 5 亿行 | 2～8 小时 |
| 10 亿行以上 | 6 小时～数天 |

最终结果数量不是决定时间的唯一因素，真正决定时间的是“必须扫描多少原始数据”。

### 后续查询

完成本地索引后：

| 查询 | 目标速度 |
|---|---:|
| 地址摘要 | 50～500 毫秒 |
| 最近 100 条流水 | 100 毫秒～2 秒 |
| 对手方排行 | 1～10 秒 |
| 百万级记录汇总 | 数秒～数十秒 |
| 导出 100 万行 CSV | 数十秒～数分钟 |

---

## 22. 实施阶段

## 阶段 0：环境与可行性测试

目标：验证公开 BNB Parquet 的实际目录、Schema、文件大小和下载速度。

工作：

1. 安装 Python、DuckDB、AWS CLI；
2. 创建项目目录；
3. 列出 BNB S3 目录；
4. 下载一个小文件；
5. 查看 Schema；
6. 用 100 个测试地址筛选；
7. 记录性能。

验收：

- 可以读取 Parquet；
- 能正确识别交易字段；
- 能匹配目标地址；
- 能写出精简 Parquet；
- 结果与区块浏览器一致。

预计：半天至 1 天。

## 阶段 1：批量交易 V1

工作：

- 地址导入；
- 文件发现；
- 下载器；
- Transactions 筛选；
- BNB 流水；
- checkpoint；
- Parquet 输出；
- 基础查询 CLI。

预计：2～5 天。

## 阶段 2：代币转账

工作：

- Transfer Topic 解析；
- Token metadata RPC 缓存；
- 代币金额换算；
- token_transfers；
- address_activity。

预计：2～5 天。

## 阶段 3：合约创建和汇总

工作：

- 顶层合约创建；
- receipt 补充；
- address_summary；
- 对手方统计；
- 校验报告。

预计：2～4 天。

## 阶段 4：网页和 API

工作：

- FastAPI；
- 地址摘要；
- 流水分页；
- 筛选；
- 导出任务；
- 简单前端。

预计：3～7 天。

## 阶段 5：每日增量

工作：

- 每日 AWS 分区同步；
- SQD/RPC 当日补充；
- 重组和重复处理；
- 定时任务；
- 健康检查。

预计：2～5 天。

完整 V1 合理开发周期：

```text
约 2～4 周
```

使用 Codex 辅助可以缩短代码编写时间，但数据源 Schema 验证、性能调优和结果核验仍需人工完成。

---

## 23. MVP 最小版本

为了避免项目过度复杂，第一版只完成以下命令：

```powershell
python -m app.cli init
python -m app.cli import-addresses .\data\input\target_addresses.xlsx
python -m app.cli discover --start 2026-07-01 --end 2026-07-02
python -m app.cli download
python -m app.cli inspect-schema
python -m app.cli process-transactions
python -m app.cli process-transfers
python -m app.cli fetch-metadata
python -m app.cli build-activity
python -m app.cli build-summary
python -m app.cli verify
python -m app.cli export --address 0x...
python -m app.api
```

第一阶段不开发复杂前端。先确保：

```text
数据正确
可断点续传
查询够快
导出正确
```

然后再做类似 OKLink 的页面。

---

## 24. 什么时候迁移 ClickHouse

满足以下任意两项再迁移：

- 精简数据超过 3 亿行；
- Parquet 总量超过 300 GB；
- API 同时有多名用户；
- DuckDB 查询经常超过 10 秒；
- 每天持续写入大量新增数据；
- 需要复杂聚合看板；
- 需要多进程并发读写。

迁移架构：

```text
Parquet 继续作为归档层
        ↓
ClickHouse 作为在线查询层
        ↓
FastAPI 查询 ClickHouse
```

不要删除 Parquet。Parquet 是可迁移、可恢复的数据资产。

Windows 上 ClickHouse 推荐：

- WSL2；
- 或 Docker Desktop。

官方说明 ClickHouse 原生运行于 Linux/macOS；Windows 环境可使用 WSL。参考：

- https://clickhouse.com/docs/install
- https://clickhouse.com/docs/knowledgebase/install-clickhouse-windows10

---

## 25. 风险与应对

### 风险 1：AWS BNB 数据字段不符合预期

应对：

- 先做 Schema 探测；
- 字段映射配置化；
- 不把字段名写死；
- 缺少 receipt 时用 RPC 补充。

### 风险 2：公开数据只包含原始 logs

应对：

- 自行解析 Transfer；
- metadata 缓存；
- 保留原始 topic/data；
- 不依赖 symbol 作为唯一资产标识。

### 风险 3：磁盘不足

应对：

- staging 分片处理后立即删除；
- 保留至少 150 GB 空闲；
- 不保留重复格式；
- 限制导出文件生命周期；
- 后期增加独立 4 TB NVMe 数据盘。

### 风险 4：公网下载慢

应对：

- 断点续传；
- 2～4 并发；
- 按日期批次；
- 下载与处理流水线并行；
- 优先限定时间范围；
- 必要时使用 SQD Cloud 或商业数据导出。

### 风险 5：同一交易重复

应对：

- Transactions 使用 tx_hash 去重；
- Transfers 使用 tx_hash + log_index 去重；
- 每个任务幂等；
- 输出前执行重复检测。

### 风险 6：链重组

历史数据影响较小。实时增量时：

- 保留最近 100～200 个区块的可重放窗口；
- 每次增量重新处理该窗口；
- 按唯一键覆盖；
- 等待一定确认数后标记 finalized。

---

## 26. Codex 开发要求

将本文件交给 Codex 时，要求它遵守：

1. 不一次生成整个系统，按阶段逐步实现。
2. 每个阶段必须有测试。
3. 所有路径通过配置，不写死用户目录。
4. 所有密钥放在 `.env`。
5. `.env` 必须加入 `.gitignore`。
6. 下载必须支持 `.partial` 和断点续传。
7. 所有任务必须幂等。
8. 所有数据写入前先校验 Schema。
9. 不使用 Pandas 处理千万级主流程。
10. 批量处理优先 DuckDB、Polars、PyArrow。
11. 不把全部数据一次性加载到内存。
12. 输出 Parquet 使用 ZSTD。
13. 保留结构化日志。
14. 错误必须包含文件名、任务 ID 和堆栈。
15. 支持 Windows 路径。
16. 提供 PowerShell 安装和启动脚本。
17. 提供小型模拟数据测试。
18. 提供端到端集成测试。
19. 任何不确定的源字段必须先探测，不允许猜测。
20. 完成每阶段后输出性能报告和数据校验报告。

---

## 27. Codex 第一阶段任务提示词

将下面内容直接发送给 Codex：

```text
请根据项目根目录中的《BSC批量资金分析系统V1设计文档.md》实施阶段 0 和阶段 1。

目标：
1. 创建 Windows 11 可运行的 Python 3.12 项目。
2. 使用 DuckDB、PyArrow、Polars、Typer 和 Rich。
3. 实现项目初始化、地址导入、地址校验、AWS S3 文件发现、单文件下载、Parquet Schema 检查、Transactions 批量地址匹配、精简 Parquet 输出和 checkpoint。
4. 不实现网页、Token Transfer、ClickHouse、Trace。
5. 所有目录、线程数、内存限制和 RPC URL 必须配置化。
6. 支持 XLSX、CSV、TXT 地址输入。
7. 任务必须可断点续传和幂等。
8. 使用结构化日志。
9. 编写单元测试和一个端到端测试。
10. 提供 setup.ps1、run.ps1、README.md 和 .env.example。
11. 首先实现一个 inspect-s3 和 inspect-parquet 命令，用于确认 AWS BNB Chain 公开数据的真实目录结构和 Schema；在确认前，不得假设源字段名。
12. 使用小型本地 Parquet 测试数据完成自动测试，不能让测试依赖公网。
13. 每处理一个文件，输出源行数、命中行数、输出文件、耗时、平均吞吐和错误数。
14. DuckDB 默认 memory_limit=20GB、threads=14，均允许通过配置修改。
15. 不要把整个 Parquet 文件加载到 Python 内存中，应由 DuckDB 扫描和过滤。
16. 所有业务文件必须写入 D 盘或其他非系统盘，禁止写入 C 盘。
17. 实现统一路径校验器；任意配置路径位于系统盘时，程序必须立即终止。
18. DuckDB 临时目录、Python TEMP/TMP、日志、下载、缓存、数据库和导出目录必须显式设置到 D 盘。
19. 编写 C 盘路径拦截测试。
20. 项目根目录固定使用 `E:\codex\bsc_analytics`。
21. 数据模型和代码必须按 EVM 多链架构设计，当前仅启用 BSC。
22. 实现 `ChainAdapter` 抽象接口和 `BscChainAdapter`。
23. 预留 `EthChainAdapter` 目录、接口和配置，但第一阶段不实现 ETH 下载。
24. 所有表、唯一键、任务、checkpoint 和输出路径必须包含 chain_key 或 chain_id。
25. 禁止把 BNB、BEP-20、chain_id=56 或 NodeReal 写死在通用业务层。
```

---

## 28. 最终建议

你的电脑性能足够，当前真正的限制是只有约 720 GB 可用空间。

因此最合理的 V1 是：

```text
AWS Parquet 历史数据
        ↓
按日期临时下载
        ↓
DuckDB 同时匹配全部目标地址
        ↓
只保留命中记录
        ↓
精简 Parquet 数据仓库
        ↓
FastAPI 查询
```

不要做：

```text
完整 BSC 节点
全链永久下载
每个地址单独扫描
大规模 JSON 中转
一开始部署很多服务
```

项目成功标准不是“保存整条 BSC 或 ETH”，而是：

```text
能稳定处理批量地址
能断点续传
能正确解析资金流水
能在本地快速重复查询
能持续增量更新
```
