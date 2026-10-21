# Turbo Cloud Bottleneck Gap Report

生成时间：2026-08-26 03:30 +08:00  
基准批次：`11abc33f-3cbd-4158-a959-a3ccf920c1b5`  
状态：`PAUSED`（不恢复下载）

## 结论

当前 48 天级 ETA 的首要原因不是 SQD 凭据或 Worker 未部署，而是逻辑任务模型与物理执行模型没有分离：21 个地址在相同的 1,242 个 50K 区块窗口上分别生成 Range，形成 26,082 个逻辑 Range，重复窗口倍率为 21。远端 Processor Worker 同时采用单循环串行执行，并把 SQD 子进程、Parquet 完成、R2 上传和 manifest 发布串在同一 Job 临界路径上。

当前批次的实测诊断值：

- `logical_ranges = 26082`
- `unique_block_windows = 1242`
- `duplicate_block_window_ratio = 21`
- `address_fan_in = 21`
- `cloud_ranges = 17366`
- `rpc_ranges = 8716`
- `running_ranges = 0`
- `completed_ranges = 31`

这意味着当前实现尚未发挥 SQD Cloud 的多地址 bulk scan 优势。目标应是保留 26,082 个 Logical Range 用于进度与 Coverage，但把相同 dataset/window 的 21 个逻辑 Range 聚合为少量 Physical Cloud Job。

## 文档首轮 12 项检查结果

| # | 检查项 | 当前实现与证据 | 判定 |
|---|---|---|---|
| 1 | Cloud Job 创建入口 | `internal/downloadscheduler/cloud_provider.go` 的 `CloudProvider.Execute` 调用 `splitCloudChunks` 后逐个 `SubmitJob`；`internal/cloudruntime/manager.go` 的 `SubmitJob` 写 pending queue。 | 已定位 |
| 2 | Range 切分逻辑 | `internal/downloadscheduler/cloud_provider.go` 的 `splitCloudChunks` 按地址组和区块窗口做笛卡尔切分。Smart Download 另由 discovery/range planner生成逻辑 Range。 | 已定位 |
| 3 | 50K 默认值 | `cloudBlocksPerChunk = 50_000`；Processor Runner 还写死 `FORCE_FLUSH_BLOCKS=50000`。 | 确认固定值 |
| 4 | Worker Pool | ETL Smart Download 默认 4 worker，但远端 `E:\Code\Processor-only\src\worker.ts` 只有一个 `while` 消费循环。 | 有效 Cloud 并发约 1 |
| 5 | Lease Claim | `E:\Code\Processor-only\src\job-poller.ts` 的 `acquire()` 每次只返回一个 Job；R2 lease 采用写入后 750ms 重读选主。 | 单 Job/循环 |
| 6 | Cloud 并发限制 | 单部署、单 Node 进程、单消费循环；没有 `WORKER_CONCURRENCY` worker pool。 | P0 缺口 |
| 7 | SQD 请求并发限制 | 没有跨 Job 的全局 `MAX_SQD_INFLIGHT_REQUESTS`；当前由单 Job 串行隐式限制为约 1。 | P0 缺口 |
| 8 | R2 上传链 | `JobRunner.run` 等子进程退出后同步调用 `uploadOutputToR2`。 | 阻塞领取下一 Job |
| 9 | Local Sync 阻塞点 | Go `MaterializeJobResult` 同步下载、SHA256 校验和原子发布；Smart Download Adapter 等待该结果后才完成 Range。 | 与 Cloud Range 完成串行 |
| 10 | Coverage Commit | Range 结果写入、校验、索引和 Coverage 结算在 Smart Download 完成链中执行；没有独立 Coverage commit queue。 | 未解耦 |
| 11 | RPC 路由条件 | TURBO 以历史 bulk/尾部 cutoff 分配 Cloud/RPC，并受总 claim 和 lane limit 约束。 | 已定位 |
| 12 | RPC 为 0 原因 | Cloud burst 槽可占满总 claim budget；当前也缺少基于 backlog、健康度和 small-span eligibility 的独立 spillover 规则。 | P1 缺口；暂不提前改路由 |

## Cloud Job 生命周期可观测性

Phase 2 已完成字段和采集链路。Processor 的生产入口 `lib/main.js` 现从真实事件写入 queued、leased、worker start、SQD query/first byte/finish、Parquet、R2 与 manifest 时间；Go runtime 从 request、lease、status、manifest、error 合并证据，并在本地物化及 Parquet 行数对账边界记录 local sync/validation。缺失端点不推断、不用查询时刻补值，对应耗时保持 `null`。

Job API 可派生：`queue_wait_ms`、`lease_to_start_ms`、`sqd_ttfa_ms`、`sqd_scan_ms`、`parquet_write_ms`、`r2_upload_ms`、`manifest_ms`、`local_sync_ms`、`validation_ms`、`coverage_commit_ms`、`total_job_ms`。`/api/scheduler/metrics` 另提供 Worker total/alive/busy/idle 与 queued/leased/running/uploading/syncing/validating/最近一小时完成失败计数。

已用独立小批次 `a4028c4b-1790-49fe-8c31-fa2d9bfdd00e` 完成真实 Cloud Job `sd-ed012eca-c9ed-45f0-94e1-48fd2bcd2822` 验证：queue wait 14,036ms、lease-to-start 2,462ms、SQD scan 24,950ms、R2 1,186ms、manifest 2,611ms、local sync 364ms、validation 1ms、total 43,677ms。该区间为 0 行，因此没有真实 first byte，`sqd_ttfa_ms=null`；这正是“缺失不推断”的预期结果。主批次始终保持暂停，恢复下载前仍需做受控 1/2/4 并发 benchmark。

## Range Explosion

当前逻辑关系为：

```text
21 addresses × 1 dataset × 1,242 block windows = 26,082 logical ranges
```

而物理扫描所需的唯一 dataset/window 组合仅 1,242 个。当前重复比率：

```text
26,082 / 1,242 = 21.0
```

这已超过方案中 `>20` 的阻止启动阈值。现阶段只增加诊断与警告，不删除 Logical Range，也不迁移现有 Pending Range。

## 当前流水线

```text
poll one job
  → lease
  → spawn SQD processor and wait
  → finish parquet
  → upload all parts to R2
  → checkpoint + manifest + _SUCCESS
  → worker claims next job
  → Go local materialize + SHA256
  → validate/index/coverage
```

SQD fetch 和 R2/manifest 未形成独立流水线，故一个 Job 上传或本地消费慢时无法充分利用下一个 SQD fetch 槽。

## 已完成修复

- 修复 TURBO 重平衡在全局锁内无变化仍全量写 Range 的问题，改为差异批量持久化。
- 增加 Dataset→Range 索引及唯一 SharedWork 执行计数。
- 修复 Cloud-owned Range 被旧 RPC SharedWork 代跑和 Cloud lane 饿死的问题。
- 修复暂停排空期间继续 claim 新 Range 的竞态。
- 修复远端 Cloud 可用时 Worker 只领取 Cloud owner、遗漏普通 RPC fast-lane Range 的问题，并保留至少一个 RPC claim 容量。
- 修复服务恢复时把已持久化 `PAUSED` 批次重算为 `RUNNING` 的问题。
- 新增 `logical_ranges`、`unique_block_windows`、`duplicate_block_window_ratio`、`address_fan_in` 诊断字段。
- 完成 Cloud Job 全生命周期时间戳、严格耗时派生和 Scheduler Worker/Job 阶段指标；更新后的 Processor 已部署到 `supreme/bsc-emergency-worker@v2`。

## 下一阶段边界

下一阶段基于已具备的生命周期指标执行受控 benchmark，不改变：

- 下载结果语义；
- Provider 路由；
- 50K Range 大小；
- Coverage 判定；
- 当前批次 PAUSED 状态。

指标完成并用真实作业验证后，才依次进入 Worker 1/2/4 并发 benchmark、Logical/Physical 聚合、Adaptive Span、RPC small/tail/repair 和 pipeline 解耦。

## 2026-08-26 Worker Pool、端口冲突与 Manifest 修复

- 远端生产入口已从“领取一个 Job 后退出”的 `lib/main.js` 改为常驻 `lib/worker.js`；`WORKER_CONCURRENCY` 支持 1–16，当前保守值为 2。
- 首次并发 2 实测失败已精确定位为两个 SQD 子进程共同监听 Prometheus `:3000`，R2 `runner-stderr.log` 返回 `EADDRINUSE`。每个 slot 现使用独立端口 3000、3001（扩展时依次递增），修复后两条 Cloud Job 可同时进入 SQD query 并持续产出行。
- Runner 非零退出时会把脱敏后的 stderr 尾部持久化到 R2 failed job 目录，解决 Worker 被回收后只有通用错误、无法还原根因的问题。
- 并发 2 验收批次 `c1fec3db-89b6-4e8e-9c83-c365af5e1733` 的 3 个 Cloud Job 全部 done、0 个新失败；但本地严格校验发现旧版 manifest 的 `parts[].rows` 缺失，Go 端按 0 对账后拒绝消费实际 1,635/2,211 行的 Parquet。
- Processor 现直接解析每个 Parquet footer 的 `num_rows`，将精确值写入 manifest/checkpoint 的 `parts[].rows`；多 part 不分摊、不猜测，无法恢复精确行数时拒绝发布 manifest。
- 修复后真实 Cloud Job `sd-cc17d955-a33a-4b0b-87a3-3a9b5ef4ec3d` 完成 2 行，`validation_ms=37`，该 Job 未再产生 `LOCAL_VALIDATION_FAILED`。其 SQD scan 为 129,906ms，明显高于 R2 2,682ms、manifest 2,513ms 和本地校验 37ms，当前主要延迟在 SQD 扫描而非上传或校验。
- 现有实测不是同 workload 的严格 A/B：并发 1 基线为 5 个 Cloud Job/约 226 秒，并发 2 诊断批次为 3 个动态 Job/约 370 秒，不能用 jobs/hour 直接宣称并发 2 更快。并发 4 暂不设为生产默认；在完成相同区块、相同地址、禁用 RPC hedge 的严格 benchmark 前保留并发 2。
- 主批次 `11abc33f-3cbd-4158-a959-a3ccf920c1b5` 全程保持 `PAUSED`，未因本轮部署或验收恢复。

## 回滚

本阶段新增字段均为向后兼容的可选 JSON 字段。回滚时可移除诊断字段和报告，不影响既有 Range、Ledger、Parquet、R2 manifest 或 Coverage 数据。

## 2026-08-26 Logical→Physical Cloud Range 聚合修复

- `sqd_cloud` 已实现多地址 Group Provider：相同 `chain + owner + dataset bundle + block window` 的 Logical Range 最多按 25 地址合并为一个 Physical Cloud Job，Logical Range、Part、Ledger、Coverage 与认证仍逐地址保留。
- 调度顺序改为先完成 TURBO owner 重平衡，再解绑从 RPC lane 改判为 Cloud lane 的旧 SharedWork 引用，最后按最终 owner 重新聚合；避免创建时的 RPC SharedWork 阻止启动时 Cloud 聚合。
- 重复执行 accelerator attach 时会跳过已有 `shared_work_id` 的 Pending/Ready Range，防止同一 Logical Range 重复追加到物理任务。
- Cloud 物理结果按 `from_address`/`to_address` 扇出；同一条双方均为关注地址的 Transfer 可进入两个逻辑结果，但物理 Manifest 行数仍按唯一物理记录对账。

真实受控验收批次 `a8e53b57-5a2f-444e-86c2-358a0728459e`：3 个地址、2 个窗口、6 个 Logical Range。Cloud 窗口的 3 个 Logical Range 聚合为 1 个 SharedWork `e32b7317-ac6e-4100-8059-b1bfa9337b53`，仅提交 1 个物理 Cloud Job `sd-4658ae58-27ab-416d-bced-69a6ab1fd258`，请求携带 3 个地址。物理 Parquet 共 50 行，逻辑扇出分别为 2、12、36 行，合计 50；三个 Cloud Range 均 `COMPLETED`、`certified=true`、各 1 个 Part。整个批次 6/6 Range 完成、Coverage 100%、失败 0。

主批次 `11abc33f-3cbd-4158-a959-a3ccf920c1b5` 未参与验收且继续保持 `PAUSED`；API 与持久化文件一致，`running_ranges=0`、`completed_ranges=31`。剩余工作是相同 workload 的 1/2/4 benchmark、Adaptive Span 与 fetch/upload/local-sync pipeline 解耦。

## 2026-08-26 Worker Slot 遥测与并发校准安全门

- Processor 每个 slot 现在向 R2 写独立心跳，包含 `IDLE/BUSY`、当前 Job、配置并发、全局 SQD inflight 上限、累计完成/失败和 busy 毫秒；ETL 只把 90 秒内的新鲜心跳计为 alive。
- `/api/scheduler/cloud/runtime` 与 `/api/scheduler/metrics` 现返回真实 `configured/total/alive/busy/idle` 和 slot 列表。生产实测为 configured=2、alive=2、busy=0、idle=2、`MAX_SQD_INFLIGHT_REQUESTS=8`。
- Processor 成功和失败 Manifest 均写入 503、429、timeout、retry 事件计数，以及子进程 CPU/RSS 峰值。当前 Cloud 镜像中的 `ps` 不可用，已如实输出 `resource_sampling_supported=false`，不再用 0 值伪装为有效资源采样。
- `JOB_QUEUE_PREFIX` 可配置，默认仍为 `bsc/jobs/`；用于 benchmark 时可切到隔离前缀，避免 Smart Download、RPC、Coverage 和生产任务污染对照。
- 增加跨 Job 的 `MAX_SQD_INFLIGHT_REQUESTS` 异步闸门，生产默认 8；并发 slot 在进入实际 SQD 子进程前统一领取许可。

校准 campaign `turbo-bench-20260826-01` 使用独立 Queue、固定 21 地址、12 个连续且不重叠的 50K 窗口，workload SHA256=`5f20331935c0ec3a0f038a159fe27981b679f7edb9562c83dab403b9bae9ed9a`。并发 1 完成 12/12、最终失败 0，扫描 600,000 唯一区块、98,859 行、4,322,367 bytes；有效执行跨度 505,410ms，Job p50=27,226ms、p95=48,155ms。

该轮同时记录到 7 次 503、2 次 429、3 次 retry。按方案错误安全门，没有继续提高到并发 2/4；由于 SQD error 日志没有完整请求总数，这些事件不能严格换算为错误率。本轮只能作为校准和停止门证据，不能宣称完成正式 1/2/4 benchmark。Worker 已恢复生产 Queue `bsc/jobs/` 和并发 2，远端为 DEPLOYED；主批次仍 `PAUSED/running_ranges=0`。

## 2026-08-26 SQD Fetch / R2 Publish 有界流水线解耦

- Processor `JobRunner` 已拆为 `fetch()` 与 `publish()`：SQD 子进程、Parquet 完成后释放全局 SQD inflight 许可，R2 上传、Checkpoint、Manifest、`_SUCCESS` 在独立发布阶段执行；保留 `run()` 兼容入口。
- Worker 增加独立 `R2_UPLOAD_WORKERS` 信号量和 `R2_UPLOAD_QUEUE_CAPACITY` 容量闸门。生产配置为 fetch slots=2、upload workers=2、outstanding capacity=4；容量许可从领取 Job 后持续到发布结束，磁盘待发布任务不会无限增长。
- Slot 心跳继续只表达 SQD fetch 槽的 IDLE/BUSY，同时附带 upload active/outstanding 观测值；发布失败由原 Poller 写 failed 对象，成功计数只在 `_SUCCESS` 完成后增加。
- 两个隔离 campaign 共执行 8 个 21 地址×50K Job，8/8 `_SUCCESS`、8 个 Manifest、8 个 Parquet Part、24,327 行、最终失败 0。单次 R2 上传耗时 1,892–2,305ms，Manifest diagnostics 与精确行数均存在。
- 默认轮询 campaign 和 250ms 测试轮询 campaign 均未观察到“同一槽下一次 SQD 查询开始早于上一次 R2 上传结束”：R2 上传短于下一次 R2 list/lease/read 路径。因此本轮证明执行边界已解耦且结果完整，但不宣称已经取得线上吞吐提升，也不把不同槽并发误写为 fetch/upload 重叠。
- 测试后已恢复 `POLL_INTERVAL_MS=5000`、`WORKER_CONCURRENCY=2`、`MAX_SQD_INFLIGHT_REQUESTS=8`、`R2_UPLOAD_WORKERS=2`、`R2_UPLOAD_QUEUE_CAPACITY=4`、`JOB_QUEUE_PREFIX=bsc/jobs/` 并重新部署。主批次仍为 `PAUSED`、`running_ranges=0`、`completed_ranges=31`。
