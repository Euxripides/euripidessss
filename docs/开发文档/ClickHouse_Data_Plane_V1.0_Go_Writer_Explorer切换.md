# ClickHouse Data Plane V1.0
## Go Writer + Explorer 数据源切换实施方案

> 当前状态：ClickHouse 基础设施、E 盘部署与核心 Schema 已完成。  
> 当前 ClickHouse 固定根目录：`E:\database\clickhouse`  
> 本阶段目标：**不再只“建库建表”，而是正式把现有 Go 业务的数据写入与查询链路接入 ClickHouse。**

---

# 1. 当前阶段边界

本次已完成：

```text
ClickHouse Infrastructure
+
ClickHouse Schema
+
E:\database\clickhouse 固定存储策略
```

尚未完成：

```text
Go Parser → ClickHouse Writer
Explorer API → ClickHouse Repository
Investigation → ClickHouse
Fund Flow → ClickHouse
Export Center → ClickHouse
```

因此当前系统实际仍然是：

```text
Provider
  ↓
Downloader
  ↓
Parser
  ↓
Parquet / DuckDB
  ↓
现有 Explorer / Analytics
```

目标是逐步变为：

```text
Provider
  ↓
Downloader
  ↓
Parser / Normalizer
  ↓
ClickHouse Writer
  ↓
ClickHouse
  ↓
Explorer / Analytics / Graph / Export
```

---

# 2. 本阶段核心原则

## 原则 1：先接 Writer，再切 Reader

禁止直接先把 Explorer 改成 ClickHouse 查询。

必须顺序：

```text
Writer
↓
真实数据入库
↓
数据完整性验证
↓
Repository
↓
Explorer Read Switch
```

---

# 3. Phase A：建立 Go ClickHouse Client

新增：

```text
internal/clickhouse/
```

建议：

```text
internal/clickhouse/
├── client.go
├── config.go
├── health.go
├── batch.go
├── errors.go
└── metrics.go
```

配置：

```yaml
clickhouse:
  enabled: true

  host: 127.0.0.1
  port: 9000
  database: blockchain

  username: default

  dial_timeout: 5s
  read_timeout: 30s
  write_timeout: 30s

  max_open_conns: 20
  max_idle_conns: 10

  compression: lz4
```

禁止在业务代码中直接创建连接。

统一：

```go
ClickHouseClient
```

---

# 4. Health Check

新增：

```http
GET /api/v1/system/clickhouse
```

返回：

```json
{
  "enabled": true,
  "connected": true,
  "database": "blockchain",
  "version": "xx.xx",
  "latency_ms": 4
}
```

同时加入系统启动检查：

```text
CLICKHOUSE_REQUIRED=true
```

生产模式：

```text
连接失败
→ 禁止进入 INGESTING
```

但 Explorer 可按灰度策略决定是否继续读旧数据源。

---

# 5. Phase B：定义统一 Warehouse Writer

新增：

```text
internal/datawarehouse/
```

目录：

```text
datawarehouse/
├── writer.go
├── batch.go
├── validator.go
├── result.go
│
└── clickhouse/
    ├── writer.go
    ├── transaction_writer.go
    ├── token_transfer_writer.go
    ├── internal_tx_writer.go
    ├── contract_creation_writer.go
    ├── nft_writer.go
    └── event_writer.go
```

接口：

```go
type Writer interface {
    WriteBatch(ctx context.Context, batch *ParsedBatch) (*WriteResult, error)
}
```

---

# 6. ParsedBatch

现有 Parser 输出需要统一到：

```go
type ParsedBatch struct {
    Blocks            []Block
    Transactions      []Transaction
    TokenTransfers    []TokenTransfer
    InternalTxs       []InternalTransaction
    NFTTransfers      []NFTTransfer
    ContractCreations []ContractCreation
    Events            []ParsedEvent
}
```

如果当前 Parser 输出结构不同：

```text
不要直接在 Writer 里做复杂解析
```

应增加：

```text
Normalizer
```

执行：

```text
Provider Raw
↓
Parser
↓
Normalizer
↓
ParsedBatch
↓
Writer
```

---

# 7. Phase C：Transaction Writer

首先接入：

```text
chain_transactions
```

原因：

```text
交易是所有后续数据的基础锚点
```

必须保证字段：

```text
chain_id
block_number
block_time

tx_hash

from_address
to_address

value
input

method_id
method_name

status

gas
gas_price
gas_used
fee

is_contract_creation
created_contract_address
```

---

# 8. Transaction 幂等

逻辑唯一键：

```text
chain_id + tx_hash
```

ClickHouse 不应依赖 OLTP 风格：

```text
INSERT IF NOT EXISTS
```

建议：

```text
ReplacingMergeTree
```

并加入：

```text
ingest_version
```

或：

```text
updated_at
```

排序键至少：

```text
(chain_id, tx_hash)
```

如果现有 Schema 已确定，以现有 Schema 为准，不重复建表。

---

# 9. Phase D：Token Transfer Writer

第二优先级：

```text
token_transfers
```

这是当前资金分析最核心表。

唯一事件：

```text
chain_id
+
tx_hash
+
log_index
```

ERC1155：

```text
+
token_id
+
batch_index
```

写入前必须完成：

```text
token_address
from_address
to_address
raw_value
decimals
value_decimal
symbol
block_time
```

---

# 10. Token Metadata

Writer 不允许因为 Token Metadata 获取失败而阻塞主数据写入。

正确流程：

```text
Transfer
↓
Token Registry Cache
↓
metadata available ?
```

有：

```text
直接写
```

没有：

```text
写基础 transfer
+
异步 metadata enrichment
```

禁止：

```text
RPC symbol() 超时
→ 整批 Token Transfer 写入失败
```

---

# 11. Phase E：Internal Transaction Writer

接入：

```text
internal_transactions
```

逻辑 ID：

```text
chain_id
+
tx_hash
+
trace_address
```

必须保存：

```text
call_type
depth
from
to
value
success
error
```

资金图和实际资金流统计以后必须同时考虑：

```text
token_transfers
+
internal_transactions
+
native transaction
```

---

# 12. Phase F：Contract Creation Writer

接入：

```text
contract_creations
contracts
```

处理：

```text
CREATE
CREATE2
```

如果交易：

```text
to_address = null
```

但 Receipt 有：

```text
contractAddress
```

必须生成：

```text
ContractCreation
```

同时更新：

```text
contracts
```

---

# 13. Phase G：Address Activity Writer

这是本阶段非常关键的一步。

不能让 Explorer 每次都：

```sql
from_address = ?
OR to_address = ?
```

因此每次写入：

```text
Transaction
Token Transfer
Internal Transfer
NFT Transfer
Contract Creation
```

同时生成：

```text
address_activity
```

---

# 14. Address Activity 示例

原事件：

```text
A → B
100 USDT
```

展开：

```text
A | OUT | B | 100 USDT
B | IN  | A | 100 USDT
```

如果：

```text
A → A
```

生成：

```text
A | SELF | A
```

不要重复生成 IN + OUT 两条。

---

# 15. Phase H：Batch Writer

禁止逐行：

```text
INSERT
INSERT
INSERT
```

统一批量：

```text
1,000
5,000
10,000
```

行/批。

初始建议：

```text
Transactions       5,000
Token Transfers   10,000
Internal Tx        5,000
Address Activity  10,000
```

实际按 benchmark 调整。

---

# 16. Writer Checkpoint

现有下载任务必须新增数据库阶段状态：

```text
DOWNLOADED
PARSED
NORMALIZED
DB_WRITING
DB_VALIDATING
COMPLETED
```

Checkpoint：

```json
{
  "last_block": 12345678,
  "transactions_written": 50000,
  "token_transfers_written": 120000,
  "internal_txs_written": 30000
}
```

---

# 17. Writer Result

统一：

```go
type WriteResult struct {
    TransactionsInput int64
    TransactionsWritten int64

    TokenTransfersInput int64
    TokenTransfersWritten int64

    InternalTxInput int64
    InternalTxWritten int64

    Rejected int64
    Duplicates int64
}
```

---

# 18. Phase I：Data Validation

每个 Batch 必须有：

```text
source rows
parsed rows
normalized rows
writer input
writer success
reject
```

必须满足：

```text
writer_input
=
writer_success
+
reject
```

逻辑重复不算数据丢失。

---

# 19. Database Validation

任务完成后：

```text
DB Validator
```

按：

```text
chain_id
block range
job_id
```

抽样或完整验证。

验证：

```text
COUNT
MIN(block)
MAX(block)
DISTINCT tx_hash
NULL rate
invalid address
invalid timestamp
```

---

# 20. Phase J：Dual Write

迁移初期保留：

```text
Parquet Writer
+
ClickHouse Writer
```

即：

```text
Parser
├── Parquet
└── ClickHouse
```

运行一段时间用于验证。

配置：

```yaml
data_plane:
  write_mode: dual
```

支持：

```text
legacy
dual
clickhouse
```

---

# 21. Dual Write 不允许强耦合

建议：

```text
Parquet 成功
ClickHouse 失败
```

任务状态：

```text
DB_WRITE_FAILED
```

不能显示：

```text
COMPLETED
```

因为新的最终 Source of Truth 是 ClickHouse。

---

# 22. Phase K：Explorer Repository

新增：

```text
internal/explorer/repository/
```

接口：

```go
type ExplorerRepository interface {
    GetAddressSummary(...)
    ListTransactions(...)
    ListTokenTransfers(...)
    ListInternalTransactions(...)
    ListContractCreations(...)
}
```

实现：

```text
DuckDBRepository
ClickHouseRepository
```

这样可以灰度切换。

---

# 23. Explorer Data Source Switch

配置：

```yaml
explorer:
  datasource: clickhouse
```

支持：

```text
duckdb
clickhouse
```

短期允许：

```text
auto
```

但正式上线后最终：

```text
clickhouse
```

---

# 24. Phase L：Address Transactions

接口：

```http
GET /api/v1/explorer/:chain/address/:address/transactions
```

改成直接查询：

```text
address_activity
```

或：

```text
chain_transactions
```

根据页面字段需求决定。

列表优先：

```text
address_activity
```

交易详情再查：

```text
chain_transactions
```

---

# 25. Token Transfers

接口：

```http
GET /api/v1/explorer/:chain/address/:address/token-transfers
```

优先直接查：

```text
address_activity
```

并过滤：

```text
activity_type = TOKEN_TRANSFER
```

或者直接查：

```text
token_transfers
```

如果需要大量 Token 专有字段。

---

# 26. Cursor Pagination

禁止：

```sql
OFFSET 500000
```

统一：

```text
cursor
```

例如：

```text
block_time
block_number
transaction_index
log_index
```

排序：

```text
DESC
```

下一页：

```sql
WHERE tuple(...) < tuple(cursor...)
```

---

# 27. Phase M：Address Summary

Explorer 不能每次临时扫完整历史。

必须建立：

```text
address_summary
```

或物化视图。

最少：

```text
tx_count
token_transfer_count
internal_tx_count

received
sent
netflow

first_seen
last_seen

unique_counterparties
```

---

# 28. Phase N：Analytics Repository

新增：

```text
internal/analytics/clickhouse/
```

第一批查询：

```text
Top Sources
Top Destinations
Daily Netflow
Token Distribution
Counterparty Count
In/Out Volume
```

---

# 29. Phase O：Graph 改查 ClickHouse

资金图暂时不要做大改。

先只把数据读取源改成：

```text
ClickHouse
```

查询：

```text
address_activity
token_transfers
internal_transactions
```

保留现有 Graph Builder。

也就是说：

```text
旧：
Parquet
↓
Graph Builder

新：
ClickHouse Repository
↓
Graph Builder
```

---

# 30. Phase P：Export Center 数据源

Export 不再读取 Parquet。

统一：

```text
Filter DSL
↓
Query Compiler
↓
ClickHouse
↓
Streaming Export
```

---

# 31. 大型 Export

例如：

```text
5,000,000 rows
```

禁止：

```text
一次性加载内存
```

必须：

```text
ClickHouse Stream
↓
CSV Writer
↓
E:\database\clickhouse\export_spool
```

---

# 32. E 盘路径

所有导出缓存：

```text
E:\database\clickhouse\export_spool
```

迁移：

```text
E:\database\clickhouse\migration
```

临时：

```text
E:\database\clickhouse\tmp
```

备份：

```text
E:\database\clickhouse\backups
```

---

# 33. Metrics

增加：

```text
clickhouse_insert_rows_total
clickhouse_insert_batches_total

clickhouse_insert_latency_ms

clickhouse_query_total
clickhouse_query_latency_ms

clickhouse_writer_errors_total
clickhouse_query_errors_total
```

---

# 34. Writer Performance Benchmark

至少测试：

```text
10K
100K
1M
```

Transactions。

以及：

```text
10K
100K
1M
```

Token Transfers。

记录：

```text
rows/sec
batch latency
CPU
RAM
disk
merge pressure
```

---

# 35. Query Benchmark

必须测试：

```text
Address Tx First Page
Token Transfer First Page
Address Summary
Top Counterparty
30D Daily Stats
All Time Stats
```

数据规模：

```text
1M
10M
50M
```

---

# 36. P0

P0-1：

```text
Go ClickHouse Client
```

P0-2：

```text
Transaction Writer
```

P0-3：

```text
Token Transfer Writer
```

P0-4：

```text
Internal Transaction Writer
```

P0-5：

```text
Contract Creation Writer
```

P0-6：

```text
Address Activity Writer
```

P0-7：

```text
Write Validation
```

P0-8：

```text
ClickHouse Explorer Repository
```

P0-9：

```text
Address Transactions
```

P0-10：

```text
Address Token Transfers
```

---

# 37. P1

```text
Address Summary
Counterparty Stats
Daily Stats
Token Metadata
Contract Detail
Transaction Detail
```

---

# 38. P2

```text
Graph Data Source
Investigation Data Source
Export Data Source
Advanced Analytics
```

---

# 39. 验收 Case A：Transaction Writer

输入：

```text
10,000 transactions
```

要求：

```text
parsed = 10,000
writer_input = 10,000
db_unique = 10,000
```

重复重跑：

```text
db logical unique = 10,000
```

不能：

```text
20,000
```

---

# 40. 验收 Case B：Token Transfer

输入：

```text
100,000 transfers
```

必须：

```text
token_transfers
+
address_activity
```

正确。

普通：

```text
100,000 transfer
```

理论 Address Activity：

```text
约 200,000
```

SELF 交易除外。

---

# 41. 验收 Case C：Explorer

指定：

```text
address A
```

旧 DuckDB：

```text
N rows
```

新 ClickHouse：

```text
N rows
```

字段：

```text
tx_hash
from
to
time
value
direction
```

一致。

---

# 42. 验收 Case D：Pagination

地址：

```text
100,000+ activity
```

连续翻页：

```text
无重复
无遗漏
排序稳定
```

---

# 43. 验收 Case E：Restart

任务写到：

```text
50%
```

杀进程。

重启：

```text
从 checkpoint 恢复
```

最终：

```text
0 丢失
0 逻辑重复
```

---

# 44. 验收 Case F：ClickHouse Failure

模拟：

```text
ClickHouse unavailable
```

任务不得：

```text
COMPLETED
```

必须：

```text
DB_WRITE_FAILED
```

或：

```text
WAITING_DATABASE
```

恢复数据库后可继续。

---

# 45. 验收 Case G：Dual Read

同一地址：

```text
DuckDB
vs
ClickHouse
```

比较：

```text
Transaction Count
Token Transfer Count
In
Out
Netflow
```

必须一致。

---

# 46. 验收 Case H：完全关闭 Parquet Reader

最终测试：

```text
禁用 DuckDB / Parquet 查询
```

要求：

```text
Explorer Address
Transactions
Token Transfers
Internal Tx
Contract Creation
```

仍正常工作。

这才代表真正完成：

```text
Data Plane Migration
```

---

# 47. 实施顺序

严格按：

```text
1 ClickHouse Client
2 Writer Interface
3 Transaction Writer
4 Token Transfer Writer
5 Internal Transaction Writer
6 Contract Creation Writer
7 Address Activity Writer
8 Writer Validation
9 Dual Write
10 ClickHouse Repository
11 Explorer Address Transactions
12 Explorer Token Transfers
13 Address Summary
14 Analytics
15 Graph
16 Export
17 Investigation
18 Disable Legacy Reader
```

---

# 48. 本阶段不要做的事

暂时不要同时：

```text
重写整个前端
重写智能调查
重写资金图
重做 Downloader
```

否则很容易把数据平面迁移和业务功能改造混在一起。

本阶段只解决：

```text
数据库真正开始承担业务数据
```

---

# 49. 最终完成标志

本阶段结束时必须达到：

```text
Provider
↓
Parser
↓
ClickHouse Writer
↓
ClickHouse
↓
Explorer Repository
↓
Explorer UI
```

并且：

```text
关闭 Parquet / DuckDB Reader
```

后：

```text
Explorer 仍可正常工作
```

这才代表 ClickHouse 已经从：

```text
“安装好了”
```

真正变成：

```text
“系统的数据资产核心”
```
