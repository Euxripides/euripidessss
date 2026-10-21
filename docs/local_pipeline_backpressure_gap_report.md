# Local Pipeline / Backpressure 第一轮差距检查报告

检查日期：2026-08-27  
检查范围：Local Sync、Validation、ClickHouse ingest、Coverage、Backpressure 现有调用链与恢复硬门。  
依据：`Local_Sync_Validation_ClickHouse_背压实测与主任务恢复验收方案_V1.0.md` 第 90 节（第一轮只检查）。

## 1. 结论

当前结论为 **NO_GO**。主批次不得恢复。

上游 SQD 的安全参数已有证据，但本地消费链尚未形成方案要求的四条显式队列。当前实际结构是：

```text
Cloud Range Worker
  -> 同步 MaterializeJobResult（Local Sync）
  -> 同步读取并核对 Parquet
  -> Range 完成
  -> 无界 goroutine 执行 Dataset Validation
  -> 无界 goroutine 合并认证 Parquet并同步 WriteIndexed
  -> Dataset Completed
  -> Coverage Index AddCertified
```

因此当前 `/api/debug/backpressure` 的 `NORMAL` 只表示它观察到的 `remote completed + upload outstanding` 未超过阈值，不能证明 Local Sync、Validation 或 ClickHouse 没有瓶颈。

## 2. 实时安全状态

- ETL `/api/health`：`ok`。
- 主批次：`11abc33f-3cbd-4158-a959-a3ccf920c1b5`。
- 主批次状态：`PAUSED`。
- `running_ranges=0`，`completed_ranges=31`。
- `/api/debug/backpressure`：`state=NORMAL`、`observational_only=true`。
- Local Sync、Validation、ClickHouse 吞吐字段仍为 `null`。
- 当前 `127.0.0.1:8123/ping` 连接被拒绝；工作区没有 `config/clickhouse.env`，当前不能取得真实 ClickHouse ingest 证据。
- 最终复核时发现 8000 listener 已意外退出；执行 `run.ps1` 恢复服务（PID 31284），随后 `/api/health=ok`、首页 HTTP 200。重启后主批次仍为 `PAUSED`、running=0、completed=31。
- 本轮未恢复主任务、未创建生产 Range、未执行故障注入。

## 3. 当前调用链

### 3.1 Local Sync

入口位于 `internal/smartdownload/cloud_adapter.go`：Cloud Job 变为 `done` 后，同一个 Provider/Range 调用栈直接调用 `MaterializeJobResult`，完成后继续遍历本地 Parquet并做行数对账，最后才返回 `ProviderResult`。

`internal/cloudruntime/manager.go` 的 `MaterializeJobResult` 当前依次执行：

1. 读取远端 Job/manifest；
2. 校验 published manifest；
3. 逐文件完整下载到内存；
4. 计算 SHA256、核对 bytes；
5. 写入临时目录；
6. 删除旧目标目录；
7. rename 发布新目录。

已有 `LocalSyncStartedAt/FinishedAt` 和总 `LocalSyncMS`，但没有显式 queue、worker pool、queued/failed counter、rows/bytes counter、queue wait 或分阶段耗时。

结论：**Local Sync 真实工作存在，但仍同步阻塞 Cloud Range Worker；不存在方案要求的独立 Local Sync Queue。**

### 3.2 Validation

Cloud Adapter 内的 Parquet 文件读取和 manifest 行数对账仍发生在 Range Worker 调用栈。全部 Range 进入终态后，`finalizeDatasetIfDoneLocked` 通过 `go s.validateDatasetAndFinalize(ds.ID)` 启动完整 Dataset Validation。

`Validator.ValidateDataset` 已执行文件、记录、Coverage、Provider、Gap/交叉验证等质量检查，但每个 Dataset 直接启动 goroutine：

- 没有显式 Validation Queue；
- 没有固定 worker pool 或 queue capacity；
- 没有 queued/started/completed/failed totals；
- 没有 queue wait、吞吐和 p50/p95/p99；
- 错误主要是报告文本/状态，尚未按方案要求形成稳定的失败类型计数。

结论：**Validation 与 Range Fetch 已在调用栈层面部分解耦，但不是有界、可观测、可背压的消费阶段。**

### 3.3 ClickHouse ingest

Validation 通过后，Dataset 状态进入 `INDEXING`，再通过 `go s.indexDataset(dsID)` 执行：

```text
MergeDataset -> writeIndexedDataset -> IndexedWriter.WriteIndexed
```

`WriteIndexed` 是同步调用。当前没有 ClickHouse ingest queue 或固定 worker pool。已有 `datawarehouse.Writer` 累计 insert rows/batches/errors 和 rolling p95；启用 ClickHouse 时 `smartDownloadWarehouseMetrics` 可抽样计算 inserted rows/s，但这些指标没有接入当前 Debug Backpressure 队列模型。

失败处理方面：

- `CLICKHOUSE_REQUIRED=true` 时，写失败保留为 `DB_WRITE_FAILED`，可以通过 `RetryIndexedDataset` 或启动恢复重试，无需重新下载；
- 默认 `CLICKHOUSE_REQUIRED=false`，ClickHouse 不可用或写失败仍允许认证 Parquet完成，Warehouse 状态可为 `SKIPPED_OPTIONAL`/`FAILED_OPTIONAL`；
- 没有自动的持久 ingest queue、退避重试调度、queue wait 或 drain rate。

结论：**Cloud Fetch 不直接等待 ClickHouse，但 ClickHouse 阶段仍是无界 goroutine内的同步写入；当前不满足独立 ingest queue 和自动恢复要求。**

### 3.4 Coverage Commit

当前顺序是：Validation 通过 -> 合并认证 Parquet -> 可选 ClickHouse write -> Dataset 标记 Completed/Certified -> `coverageIndex.AddCertified`。

正面证据：Validation 未通过时不会提交认证 Coverage；当 `CLICKHOUSE_REQUIRED=true` 且写入失败时，流程会在 Coverage 前失败并保留重试状态。

硬门缺口：默认 `CLICKHOUSE_REQUIRED=false`，ClickHouse 缺失/失败时仍可能标记 Dataset Completed 并提交 Coverage，不满足新方案“Validation PASS + ClickHouse ingest PASS 后才 Coverage Commit”的生产恢复要求。此外 Cloud Runtime 虽有 Coverage timestamps 字段，但当前本地流程未找到对应 Begin/Finish 调用，无法形成真实 Coverage queue/duration 指标。

## 4. Queue、Worker、重试、幂等检查

| 阶段 | 显式 Queue | 固定 Worker Pool | 失败后恢复 | 当前幂等边界 | 结论 |
|---|---:|---:|---|---|---|
| Local Sync | 否 | 否 | 依赖上层 Range 重试 | `.partial-*` 失败清理；发布前会删除旧目录，并发重复物化/发布尚无互斥证据 | 不通过 |
| Validation | 否 | 否 | 启动恢复可重建 Dataset；无独立 Validation retry queue | 读取/校验为主，Coverage 前有状态门；崩溃恢复需专项实测 | 不通过 |
| ClickHouse ingest | 否 | 否 | `DB_WRITE_FAILED` 可重试/启动恢复 | ReplacingMergeTree + `FINAL` 提供逻辑幂等，物理重复不能由此排除 | 不通过 |
| Coverage commit | 否 | 否 | Coverage Store 可重建认证数据 | AddCertified 使用区间归并；重复提交尚未完成本轮实测 | 不通过 |

现有单元测试已经覆盖部分重要语义：manifest/SHA fail-closed、required warehouse failure 可重试、DB write recovery、Validation/Certification gate、ClickHouse logical row reconciliation。但这些不是 100 Job、故障注入、backlog drain 的端到端证据。

## 5. 当前 Backpressure 实现

### 5.1 Debug API

`/api/debug/backpressure` 当前只计算：

```text
depth = remote_completed_queue_depth + r2_upload_outstanding
SOFT: depth >= 20
HARD: depth > 100
```

缺少：

- Validation/ClickHouse/Coverage 真实 queue depth；
- 多阶段 bottleneck 和 reason；
- queue wait p95、stage throughput、磁盘/内存/ClickHouse error rate；
- `RECOVERING`、hysteresis、趋势窗口、状态持久化和审计事件；
- effective cloud concurrency、would-action/actual-action；
- queue drain rate 和恢复阶梯。

### 5.2 Smart Download Governor

`internal/smartdownload/v31.go` 已有另一套实际 Governor，可根据 downloaded/parsed/inserted rows/s、ClickHouse p95/merge queue/active parts、Disk IO/free space限制 Claims，并可暂停新 Cloud Claim。该机制已有单元测试，但存在以下证据断层：

- 与 `/api/debug/backpressure` 不是同一状态机；
- 没有 NORMAL/SOFT/HARD/RECOVERING 状态和滞回；
- 输入不是本方案要求的真实 Local Sync/Validation/ClickHouse queue；
- ClickHouse 未运行时真实指标源不可用；
- 尚未通过独立 Batch 的 synthetic backlog、active control 和 recovery ladder 实测。

结论：**已有降载原语，但尚不能把当前 Debug `NORMAL` 或既有 Governor 单测等同为正式 Backpressure 验收通过。**

## 6. Recovery Readiness 当前判定

| Check | 状态 | 证据/原因 |
|---|---:|---|
| sqd_benchmark_pass | PASS | C1/C2/C4 与 100K/200K/500K 已通过 |
| safe_concurrency_selected | PASS | 4 |
| safe_span_selected | PASS | 500K |
| local_sync_metrics_valid | FAIL | throughput null，无 queue/worker pool |
| validation_metrics_valid | FAIL | throughput null，无 queue/worker pool |
| clickhouse_metrics_valid | FAIL | throughput null，无 ingest queue，ClickHouse 当前不可连接 |
| backpressure_observation_pass | FAIL | 只看 remote/upload，未做 bottleneck 对照 |
| backpressure_active_control_pass | FAIL | 未做独立故障注入实测 |
| backlog_recovery_pass | FAIL | 无真实 backlog/drain/recovery ladder 证据 |
| claim_race_regression_pass | PARTIAL | 已有修复和 C4 Benchmark，但尚未按本方案连续 100 Jobs专项记录 |
| data_integrity_pass | PARTIAL | 上游 Benchmark完整性通过；下游五层 row equality 未完成 |
| main_batch_paused | PASS | `PAUSED` |
| running_ranges_zero | PASS | 0 |

自动结论：`NO_GO`。

## 7. 后续最小实施顺序

1. 仅新增统一的 Pipeline Metrics/事件模型，保持 `observational_only=true`，不改变调度行为。
2. 将 Local Sync、Validation、ClickHouse ingest、Coverage 依次改为有界持久队列和固定 worker pool；每次只改一个阶段并保留恢复语义。
3. 把 Debug Backpressure 与实际 Governor 统一为一个状态源，先验证 bottleneck 判断，再启用动作。
4. 在独立 prefix、独立 batch、隔离 ClickHouse 表上执行 C4 + 500K 和 Synthetic Backlog Tests。
5. 所有硬门通过后实现 `/api/debug/recovery-readiness`；即使返回 GO，也必须等待人工明确执行 Canary Resume。

## 8. 本轮边界

本轮严格停在方案第 90 节：只检查并形成报告。没有修改 Local Sync、Validation、ClickHouse、Coverage 或 Backpressure 行为；仅因最终复核发现服务退出而用 `run.ps1` 恢复既有二进制；没有恢复主批次。
