# 企业级区块链数据下载引擎 V2 PRD

版本：V2.0  
文档类型：企业级产品需求文档 / 技术架构规范 / Codex 实施说明  
项目根目录：

```text
E:\codex\bsc_analytics
```

适用链：

- BSC
- Ethereum
- Base
- Arbitrum

适用数据源：

- SQD
- AWS Parquet
- RPC
- 本地 Parquet
- DuckDB

适用任务：

- 单地址数据下载
- 多地址数据下载
- Token 数据下载
- Contract 数据下载
- NFT 数据下载
- 指定时间范围下载
- 指定区块范围下载
- 全历史下载
- 增量同步
- 断点恢复
- 数据补全
- 地址分析前置数据采集

---

# 1. 文档目标

本 PRD 用于将现有 Parquet 下载功能升级为统一的企业级区块链数据下载引擎。

V2 不再以“单次下载文件”为中心，而是以：

```text
任务规划
数据发现
范围解析
数据源调度
分片下载
Parquet 写入
DuckDB 索引
完整性校验
断点恢复
```

为核心能力。

最终目标：

```text
业务请求
    ↓
Discovery Engine
    ↓
Range Planner
    ↓
Download Planner
    ↓
Provider Scheduler
    ↓
Parquet Writer
    ↓
DuckDB Indexer
    ↓
Validation Engine
    ↓
Analysis Engine
```

---

# 2. 建设背景

当前下载链路存在以下风险：

1. 下载逻辑与地址业务逻辑耦合；
2. 首次时间、时间范围和区块范围解析分散；
3. SQD、AWS、RPC 缺少统一 Provider 抽象；
4. 单次任务过大时容易触发 SQD 503；
5. 断点恢复依赖零散状态；
6. 下载完成不代表数据完整；
7. 单地址、多地址、全链下载缺少统一任务模型；
8. 下载文件命名、目录结构和 Manifest 缺少统一标准；
9. 数据源切换与故障转移能力不足；
10. 前端无法清晰展示当前任务处于发现、规划、下载还是索引阶段；
11. 后续扩展 Token、NFT、Contract 数据时容易重复开发；
12. 数据不得写入 C 盘，但现有流程需要进一步强化路径约束。

---

# 3. 产品目标

V2 必须实现：

- 统一下载任务模型；
- 统一范围解析；
- 统一 Provider 调度；
- 统一分片规划；
- 统一 Checkpoint；
- 统一 Manifest；
- 统一 Parquet Writer；
- 统一 DuckDB Indexer；
- 统一数据校验；
- 支持多链；
- 支持多地址；
- 支持自动首次时间发现；
- 支持时间、区块、全历史、增量和恢复模式；
- 支持 SQD、AWS、RPC 多数据源；
- 支持数据源故障转移；
- 支持任务优先级；
- 支持并发控制；
- 支持暂停、继续、取消和重试；
- 支持前端完整可视化；
- 支持审计、日志和指标；
- 支持旧任务兼容；
- 不在 C 盘保存业务数据。

---

# 4. 非目标

V2 暂不包含：

- 自建完整归档节点；
- 用 RPC 替代历史数据源；
- 实时区块流式订阅；
- 链重组深度恢复的完整实时系统；
- 分布式跨机器调度；
- Kubernetes 部署体系；
- 对外商业化计费系统。

这些能力可在 V2.1 或 V3 规划。

---

# 5. 核心设计原则

## 5.1 业务与下载解耦

地址分析服务只负责描述需求：

```text
我要下载哪些地址
需要哪些数据类型
希望使用什么范围
```

下载引擎负责：

```text
如何发现范围
如何分片
使用哪个 Provider
如何写入 Parquet
如何恢复
如何验证完整性
```

## 5.2 时间最终转换为区块范围

前端可以选择时间，但下载引擎最终统一使用：

```text
effective_start_block
effective_end_block
```

## 5.3 Downloader 不负责业务判断

Parquet Downloader 不判断：

- 地址首次出现时间；
- 是否为 Contract；
- 是否属于高风险地址；
- 是否需要全历史。

这些由上层 Planner 决定。

## 5.4 Provider 可替换

SQD、AWS、RPC 必须实现统一接口。

## 5.5 所有任务可恢复

任何阶段都必须具备明确状态和恢复点。

## 5.6 完成必须可验证

任务进入 COMPLETED 前必须完成：

- 文件存在；
- Manifest 一致；
- 行数统计；
- Schema 校验；
- DuckDB 索引；
- 数据覆盖校验。

---

# 6. 总体架构

```text
Frontend
    ↓
Download API
    ↓
Job Service
    ↓
Discovery Engine
    ↓
Range Planner
    ↓
Download Planner
    ↓
Scheduler
    ↓
Provider Router
 ┌───────┬───────┬───────┐
 │ SQD   │ AWS   │ RPC   │
 └───────┴───────┴───────┘
    ↓
Chunk Executor
    ↓
Dataset Writer
    ↓
Parquet
    ↓
DuckDB Indexer
    ↓
Validation Engine
    ↓
Manifest Finalizer
    ↓
Completed Dataset
```

---

# 7. 核心模块

建议新增目录：

```text
internal/downloadengine/
├── job/
├── discovery/
├── rangeplanner/
├── downloadplanner/
├── scheduler/
├── provider/
├── executor/
├── checkpoint/
├── writer/
├── manifest/
├── indexer/
├── validator/
├── storage/
├── metrics/
└── api/
```

---

# 8. 任务类型

```text
ADDRESS_SINGLE
ADDRESS_BATCH
TOKEN
CONTRACT
NFT
DATASET_RANGE
FULL_HISTORY
INCREMENTAL_SYNC
REPAIR
REINDEX
```

---

# 9. 数据范围模式

支持：

```text
AUTO_FIRST_SEEN
TIME_RANGE
BLOCK_RANGE
FULL_HISTORY
RESUME
INCREMENTAL
```

## AUTO_FIRST_SEEN

适用于地址下载。

流程：

```text
Discovery
→ 解析每个地址 first_seen_block
→ 计算下载起点
```

## TIME_RANGE

```text
start_time
end_time
```

由 Range Planner 转换为区块。

## BLOCK_RANGE

直接使用：

```text
start_block
end_block
```

## FULL_HISTORY

```text
genesis/supported_start_block
→ finalized_block
```

必须二次确认。

## RESUME

从 Checkpoint 恢复。

## INCREMENTAL

从上次成功同步区块开始：

```text
last_success_block + 1
→ current_finalized_block
```

---

# 10. 地址发现引擎

Discovery Engine 负责：

- EOA 首次出现；
- Contract 创建时间；
- Token 首次活动；
- 地址类型；
- 数据覆盖状态；
- 首次区块来源；
- 本地缓存；
- SQD 轻量查询；
- AWS 回退；
- RPC 辅助验证。

状态：

```text
FOUND
PARTIAL
NOT_FOUND
TEMPORARILY_UNAVAILABLE
FAILED
```

多地址任务不得因为少量地址失败而整体失败。

示例：

```json
{
  "total": 1000,
  "found": 960,
  "partial": 15,
  "not_found": 10,
  "temporarily_unavailable": 10,
  "failed": 5
}
```

---

# 11. 范围规划器

Range Planner 输入：

```json
{
  "range_mode": "AUTO_FIRST_SEEN",
  "addresses": ["0x..."],
  "end_time": null
}
```

输出：

```json
{
  "effective_start_block": 8123456,
  "effective_end_block": 54321000,
  "effective_start_time": "2020-04-18T00:00:00Z",
  "effective_end_time": "2026-07-30T13:18:00Z",
  "range_source": "FIRST_SEEN",
  "coverage_status": "FULL"
}
```

---

# 12. 下载规划器

Download Planner 负责：

- 分片大小；
- 地址分组；
- 数据类型分组；
- Provider 能力匹配；
- 优先级；
- 并发预算；
- 预计请求数；
- 预计数据量；
- 预计文件数。

建议默认：

```text
block_chunk_size = 100,000
```

但必须根据链、数据类型和 Provider 动态调整。

---

# 13. 多地址分组策略

## 少量地址

建议：

```text
1 ～ 100
```

可合并为一个地址组。

## 中等地址量

建议：

```text
101 ～ 5,000
```

按地址数量和首次区块分组。

## 大量地址

建议：

```text
5,000+
```

使用：

- 地址哈希分桶；
- 首次区块分层；
- 数据类型拆分；
- 多阶段下载。

阈值必须配置化，不能写死。

---

# 14. Provider 抽象

统一接口示例：

```go
type HistoricalProvider interface {
    Name() string
    Capabilities() ProviderCapabilities
    Health(ctx context.Context) ProviderHealth
    Estimate(ctx context.Context, req QueryRequest) (*EstimateResult, error)
    Execute(ctx context.Context, req QueryRequest) (RecordStream, error)
}
```

Provider：

```text
SQDProvider
AWSProvider
RPCProvider
LocalParquetProvider
```

RPC Provider 只允许补充查询，不允许承担全历史扫描。

---

# 15. Provider Router

路由决策依据：

- 数据类型；
- 链；
- 历史范围；
- Provider 健康状态；
- 延迟；
- 成功率；
- 当前并发；
- 成本；
- 数据覆盖；
- 是否支持地址过滤；
- 是否支持 Trace；
- 是否支持断点恢复。

推荐优先级：

```text
Local Parquet
→ SQD
→ AWS
→ RPC verification
```

---

# 16. Provider 状态

```text
HEALTHY
DEGRADED
NO_WORKER
RATE_LIMITED
UNAVAILABLE
RECOVERING
DISABLED
```

SQD 503：

```text
NO_WORKER
```

不得当成普通失败立即重试。

建议退避：

```text
30s
60s
120s
300s
```

---

# 17. Scheduler

Scheduler 必须支持：

- 优先级队列；
- Provider 并发限制；
- 链级并发限制；
- 任务级并发限制；
- 小任务快速通道；
- 大任务公平调度；
- 暂停；
- 恢复；
- 取消；
- 重试；
- 队列容量限制；
- 防止饥饿。

优先级：

```text
CRITICAL
HIGH
NORMAL
LOW
BACKGROUND
```

---

# 18. 任务状态机

```text
CREATED
VALIDATING
DISCOVERING
RESOLVING_RANGE
PLANNING
QUEUED
RUNNING
PAUSING
PAUSED
RETRY_WAIT
WRITING
INDEXING
VALIDATING_OUTPUT
FINALIZING
COMPLETED
PARTIAL_COMPLETED
FAILED
CANCELLED
```

状态转换必须由统一 State Machine 管理。

禁止多个模块自行修改最终状态。

---

# 19. Chunk 状态

```text
PENDING
QUEUED
RUNNING
RETRY_WAIT
SUCCEEDED
FAILED
SKIPPED
CANCELLED
```

每个 Chunk 记录：

```text
chunk_id
job_id
chain_id
address_group_id
dataset_type
start_block
end_block
provider
attempt
status
rows_written
bytes_written
checksum
started_at
completed_at
error_code
```

---

# 20. Checkpoint

Checkpoint 至少包含：

```json
{
  "job_id": "job-001",
  "completed_chunks": ["chunk-1", "chunk-2"],
  "failed_chunks": ["chunk-3"],
  "last_success_block": 9000000,
  "manifest_version": 2,
  "updated_at": "2026-07-30T13:18:00Z"
}
```

建议：

- DuckDB 为主；
- JSON 文件为灾备；
- 写入必须原子化；
- Checkpoint 与 Manifest 分离；
- 恢复时必须校验文件是否真实存在。

---

# 21. Dataset Writer

统一抽象：

```go
type DatasetWriter interface {
    WriteBatch(ctx context.Context, batch RecordBatch) error
    Flush(ctx context.Context) error
    Close(ctx context.Context) (*WriteResult, error)
}
```

支持：

```text
ParquetWriter
CSVWriter
JSONLWriter
```

V2 默认：

```text
Parquet
```

CSV 作为可选导出，不得与主下载流程耦合。

---

# 22. Parquet 文件规范

目录建议：

```text
E:\codex\bsc_analytics\data\
└── {chain_id}\
    └── {dataset_type}\
        └── job={job_id}\
            └── group={group_id}\
                └── part-{start_block}-{end_block}.parquet
```

禁止写入：

```text
C:\
```

文件命名必须可追踪区块范围。

---

# 23. Manifest V2

每个任务生成：

```text
manifest.json
```

示例：

```json
{
  "version": 2,
  "job_id": "job-001",
  "chain_id": "bsc",
  "dataset_types": ["transactions", "logs", "traces"],
  "range": {
    "start_block": 8000000,
    "end_block": 54000000
  },
  "addresses": {
    "count": 100,
    "group_count": 4
  },
  "files": [],
  "coverage_status": "FULL",
  "rows_total": 1234567,
  "bytes_total": 987654321,
  "status": "COMPLETED",
  "created_at": "...",
  "completed_at": "..."
}
```

Manifest 必须由 Finalizer 原子生成。

---

# 24. DuckDB Indexer

下载完成后自动：

- 注册 Parquet；
- 创建视图；
- 更新数据集目录；
- 更新地址索引；
- 更新 first_seen；
- 更新 last_seen；
- 更新覆盖范围；
- 更新统计信息。

不得要求用户手工导入。

---

# 25. 数据验证引擎

验证内容：

- Parquet 可读取；
- Schema 正确；
- 区块范围正确；
- 文件无重复；
- Chunk 无缺失；
- 行数非负；
- 地址过滤有效；
- Manifest 与实际文件一致；
- DuckDB 可查询；
- 时间范围与区块范围一致；
- Checksum 可选校验。

结果：

```text
VALID
VALID_WITH_WARNINGS
INVALID
```

---

# 26. 数据覆盖状态

```text
FULL
PARTIAL
UNKNOWN
```

禁止在覆盖不完整时将空结果简单展示为：

```text
0 transactions
```

必须展示：

```text
当前数据覆盖不完整，结果可能缺失。
```

---

# 27. API 设计

## 创建任务

```http
POST /api/download-engine/jobs
```

请求：

```json
{
  "job_type": "ADDRESS_BATCH",
  "chain_id": "bsc",
  "addresses": ["0x...", "0x..."],
  "datasets": ["transactions", "logs", "traces"],
  "range_mode": "AUTO_FIRST_SEEN",
  "priority": "NORMAL",
  "output_format": "PARQUET"
}
```

## 查询任务

```http
GET /api/download-engine/jobs/{job_id}
```

## 暂停

```http
POST /api/download-engine/jobs/{job_id}/pause
```

## 恢复

```http
POST /api/download-engine/jobs/{job_id}/resume
```

## 取消

```http
POST /api/download-engine/jobs/{job_id}/cancel
```

## 重试失败分片

```http
POST /api/download-engine/jobs/{job_id}/retry-failed
```

---

# 28. 前端下载中心

新增：

```text
虚拟币 → 下载中心
```

页面区域：

- 创建下载任务；
- 任务列表；
- 当前阶段；
- 下载进度；
- 地址发现进度；
- Chunk 进度；
- Provider 状态；
- 速度；
- ETA；
- 文件数量；
- 数据量；
- 错误；
- 重试；
- 暂停；
- 恢复；
- 取消；
- Manifest；
- 数据覆盖；
- DuckDB 索引状态。

---

# 29. 创建任务表单

字段：

```text
任务类型
链
地址输入方式
数据类型
范围模式
时间范围
区块范围
自动首次时间
优先级
输出格式
是否自动索引
```

地址输入支持：

- 单个输入；
- 多行粘贴；
- CSV；
- XLSX；
- 历史地址集。

---

# 30. 前端阶段展示

```text
正在校验地址
正在发现首次出现时间
正在计算下载范围
正在规划分片
等待调度
正在下载
正在写入 Parquet
正在建立 DuckDB 索引
正在验证数据
正在完成任务
```

禁止统一显示为：

```text
下载中
```

---

# 31. 前端进度

必须区分：

```text
overall_progress
discovery_progress
download_progress
index_progress
validation_progress
```

示例：

```json
{
  "overall_progress": 62,
  "stage": "RUNNING",
  "discovery": {
    "completed": 1000,
    "total": 1000
  },
  "chunks": {
    "completed": 620,
    "total": 1000
  }
}
```

---

# 32. 错误码

建议：

```text
INVALID_REQUEST
INVALID_ADDRESS
UNSUPPORTED_CHAIN
UNSUPPORTED_DATASET
FIRST_SEEN_NOT_FOUND
FIRST_SEEN_PARTIAL
SQD_NO_AVAILABLE_WORKERS
SQD_RATE_LIMITED
SQD_CIRCUIT_OPEN
AWS_FILE_NOT_FOUND
RPC_UNAVAILABLE
STORAGE_PATH_INVALID
DISK_SPACE_INSUFFICIENT
CHECKPOINT_CORRUPTED
PARQUET_WRITE_FAILED
MANIFEST_INCONSISTENT
DUCKDB_INDEX_FAILED
VALIDATION_FAILED
JOB_CANCELLED
```

---

# 33. 存储安全

启动时必须检查：

```text
storage_root
temp_root
checkpoint_root
manifest_root
```

所有路径必须位于：

```text
E:\codex\bsc_analytics
```

或明确配置的数据盘。

检测到 C 盘路径时：

```text
拒绝启动任务
```

---

# 34. 磁盘空间管理

创建任务前估算：

```text
estimated_download_bytes
estimated_parquet_bytes
required_temp_bytes
free_disk_bytes
```

空间不足时禁止开始。

建议保留：

```text
20% 安全余量
```

---

# 35. 日志

事件：

```text
DOWNLOAD_JOB_CREATED
DISCOVERY_STARTED
DISCOVERY_COMPLETED
RANGE_RESOLVED
PLAN_CREATED
CHUNK_STARTED
CHUNK_COMPLETED
CHUNK_FAILED
PROVIDER_SWITCHED
CHECKPOINT_SAVED
PARQUET_WRITTEN
DUCKDB_INDEXED
VALIDATION_COMPLETED
MANIFEST_FINALIZED
JOB_COMPLETED
JOB_FAILED
JOB_CANCELLED
```

---

# 36. Metrics

建议：

```text
download_jobs_total
download_jobs_running
download_jobs_failed_total
download_chunks_total
download_chunks_failed_total
download_bytes_total
download_rows_total
download_latency_ms
provider_requests_total
provider_success_total
provider_503_total
provider_switch_total
checkpoint_recovery_total
parquet_write_failures_total
validation_failures_total
```

---

# 37. 配置

```yaml
download_engine:
  enabled: true
  storage_root: "E:\\codex\\bsc_analytics\\data"
  checkpoint_root: "E:\\codex\\bsc_analytics\\checkpoints"
  temp_root: "E:\\codex\\bsc_analytics\\temp"
  default_chunk_size: 100000
  max_active_jobs: 3
  max_chunks_per_job: 4
  max_sqd_streams: 1
  max_aws_downloads: 4
  max_rpc_requests: 10
  disk_safety_ratio: 0.20
  auto_index_duckdb: true
  validate_before_complete: true
```

---

# 38. 数据库表

建议：

```text
download_jobs
download_job_addresses
download_plans
download_chunks
download_checkpoints
download_files
download_manifests
provider_events
dataset_registry
address_first_seen
```

---

# 39. 兼容现有版本

旧下载任务：

- 保留原记录；
- 映射为 Legacy Job；
- 不强制迁移运行中任务；
- 新任务使用 V2；
- V1 与 V2 可短期并存；
- V2 稳定后再移除 V1。

---

# 40. 单元测试

必须覆盖：

- 地址发现；
- 时间转区块；
- 区块分片；
- 多地址分组；
- Provider 路由；
- SQD 503；
- AWS 回退；
- Checkpoint；
- 暂停恢复；
- 取消；
- Manifest；
- Parquet Writer；
- DuckDB Indexer；
- 数据验证；
- 路径安全；
- 磁盘空间不足；
- 状态机；
- 旧任务兼容。

---

# 41. 集成测试

必须覆盖：

1. BSC 单地址；
2. BSC 多地址；
3. Ethereum Contract；
4. Base 时间范围；
5. Arbitrum 区块范围；
6. SQD 503 切换 AWS；
7. 中断后恢复；
8. 暂停后恢复；
9. 取消任务；
10. PARTIAL 覆盖；
11. DuckDB 自动索引；
12. Manifest 一致性；
13. 磁盘不足阻止任务；
14. C 盘路径拒绝。

---

# 42. Playwright E2E

必须覆盖：

- 创建单地址任务；
- 创建批量地址任务；
- 自动首次时间；
- 手动时间范围；
- 指定区块范围；
- 查看 Discovery；
- 查看 Chunk；
- 暂停；
- 恢复；
- 取消；
- 重试失败分片；
- Provider 异常提示；
- Manifest 查看；
- 数据覆盖提示；
- 移动端 390px。

---

# 43. 性能目标

建议目标：

```text
缓存 Discovery：
P95 < 100ms
```

```text
本地 DuckDB Range Resolve：
P95 < 2s
```

```text
任务创建：
P95 < 500ms
```

下载吞吐由 Provider 决定，但必须统计：

```text
rows/s
MB/s
chunks/min
```

---

# 44. 部署步骤

## 第一阶段：基础模型

- 创建数据库表；
- 创建 Job State Machine；
- 创建统一任务 API；
- 创建 Checkpoint；
- 创建 Manifest V2。

## 第二阶段：Provider 抽象

- SQDProvider；
- AWSProvider；
- RPCProvider；
- Provider Router；
- Health 与 Circuit Breaker。

## 第三阶段：Planner

- Discovery Engine；
- Range Planner；
- Download Planner；
- 地址分组；
- Chunk 规划。

## 第四阶段：执行链路

- Scheduler；
- Chunk Executor；
- Parquet Writer；
- DuckDB Indexer；
- Validator。

## 第五阶段：前端

- 下载中心；
- 创建任务；
- 任务详情；
- Provider 状态；
- Manifest；
- 错误处理。

## 第六阶段：灰度

优先：

```text
BSC 单地址
```

然后：

```text
BSC 多地址
Ethereum
Base
Arbitrum
```

---

# 45. 回滚方案

配置：

```yaml
download_engine:
  enabled: false
```

回滚时：

- V2 不再接收新任务；
- 已运行任务允许完成或暂停；
- 前端切回 Legacy；
- 保留 V2 数据库；
- 不删除已生成 Parquet；
- 不删除 Manifest；
- 不破坏 DuckDB 索引。

---

# 46. 验收标准

必须全部满足：

- 支持单地址和多地址；
- 支持自动首次时间；
- 支持时间、区块、全历史、恢复、增量模式；
- Provider 统一抽象；
- SQD 503 不无限重试；
- 支持 AWS 回退；
- 支持暂停、继续、取消；
- Checkpoint 可恢复；
- Manifest 与文件一致；
- Parquet 可读取；
- DuckDB 自动索引；
- 数据覆盖可展示；
- 前端阶段明确；
- 所有路径不写入 C 盘；
- 单元测试通过；
- 集成测试通过；
- Playwright 通过；
- 健康检查通过；
- 未运行的测试不得声明通过。

---

# 47. Codex 实施要求

Codex 必须：

1. 先扫描现有项目结构；
2. 输出复用模块清单；
3. 输出新增文件清单；
4. 不删除 V1.4.2 Scheduler、Circuit Breaker、Checkpoint；
5. 优先复用现有 Data Source Manager；
6. 所有 schema 变更必须提供 migration；
7. 所有时间使用 UTC；
8. 所有地址统一标准化；
9. 所有最终任务必须保存 Effective Range；
10. 所有 Chunk 必须可审计；
11. 所有路径必须进行数据盘校验；
12. 所有测试必须实际执行；
13. 最终输出测试结果；
14. 最终输出未完成项；
15. 不得伪造测试通过结果。
