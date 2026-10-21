# SQD 性能证据补全与安全门差距报告

生成时间：2026-08-26 14:50 +08:00  
依据：`SQD_性能证据补全与安全门验证方案_V1.0.md` 第一轮“只做代码检查”  
运行边界：不恢复主批次，不修改并发、Block Span、Provider 路由或 retry policy。

## 结论

当前 Safety Gate 状态为 `BLOCKED: MISSING_REQUEST_TOTAL`。

现有 503、429、timeout、retry 数值来自 SQD 子进程 stderr 文本匹配，只能作为事件线索，不能作为物理请求计数。系统没有 `sqd_requests_total`，所以不能计算 503/429/timeout/retry rate，也不能放行 C2、C4 或 Span Benchmark。

## 1. SQD Request 入口

存在两个入口：

1. `E:\Code\Processor-only\src\main.ts` 的 `getFinalizedHead()` 直接使用全局 `fetch()` 请求 Portal `finalized-head`，30 秒超时；当前没有请求级指标。
2. `DataSourceBuilder.setPortal(PORTAL_URL)` 创建 `@subsquid/portal-client` 的 `PortalClient`。数据流请求最终进入 `@subsquid/http-client` 1.8.1 的 `HttpClient.request()`，包括 `finalized-stream` 页面请求。

`DataSourceBuilder.setPortal()` 支持注入 `PortalClientOptions` 或 `PortalClient`，因此后续可以注入自定义 `HttpClient`，不需要修改 `node_modules`。

## 2. Retry 入口

`@subsquid/portal-client` 0.6.0 在其私有 `request()` 中默认设置 `retryAttempts=6`；底层 `HttpClient` 默认退避序列为 `10ms, 100ms, 500ms, 2s, 10s, 20s`，408、429、502、503、504、521–524、timeout 和连接错误可重试。

另外，Portal `getHead/getFinalizedHead` 对 HTTP 200 但 body 为 `null` 的情况还有独立的有限退避。当前业务代码无法区分传输重试、null-head 重试和逻辑 Job 重试。

## 3. 503/429 计数位置

`E:\Code\Processor-only\src\job-runner.ts` 的 `StderrDiagnosticsCollector` 用正则逐行匹配 `503`、`429`、timeout 和 retry/backoff。结果写入 status、Manifest、Checkpoint 和 `_SUCCESS`。

缺陷：

- 没有物理请求分母；
- 一条日志不保证对应一次物理请求；
- 同一次失败可能出现多条包含状态码的日志；
- 成功响应通常不写 stderr；
- network、DNS、cancelled、retry success、retry exhausted 未独立分类；
- 没有每次物理请求 duration 与 backoff duration。

## 4. Total Requests

不存在 `sqd_requests_total`、success/failed total 或 logical query total。当前 `/api/scheduler/metrics` 仅公开 Worker/Job 阶段、Registry 和少量 Scheduler 指标。

因此以下值全部不可计算：

- `success_rate`；
- `503_rate`；
- `429_rate`；
- `timeout_rate`；
- `retry_rate`；
- request duration p50/p90/p95/p99；
- TTFA p50/p95/p99。

## 5. Job Lifecycle

已经具备：`created_at`、`queued_at`、`leased_at`、`worker_started_at`、SQD query/first byte/finished、Parquet、R2、Manifest、Local Sync、Validation、Coverage 和 completed/failed 时间戳；Go 端只在真实起止端点同时存在时派生耗时。

仍缺：

- `claim_started_at`、`claimed_at`、独立 `lease_acquired_at`；
- `local_sync_queued_at`；
- ClickHouse started/finished；
- `worker_idle_between_jobs_ms`；
- `queue_poll_ms`、`claim_ms`、`lease_acquire_ms`、`job_payload_load_ms`、`pre_fetch_prepare_ms`；
- 每 Job 的 SQD retry backoff 总时长。

## 6. Worker State

R2 slot heartbeat 已公开 `IDLE/BUSY`、当前 Job、配置并发、SQD inflight 上限、完成/失败数和累计 busy 时间。生产实时快照为 configured=2、alive=2、busy=0、idle=2；主批次为 `PAUSED`，当前空闲是正常状态。

仍缺 `idle_reason`，无法自动区分 `NO_PENDING_JOB`、`BATCH_PAUSED`、`GLOBAL_PAUSED`、`CONCURRENCY_LIMIT`、`LEASE_ACQUIRE_FAILED`、`UPLOAD_QUEUE_FULL` 或 R2 请求失败。

## 7. R2 Queue Claim

`E:\Code\Processor-only\src\job-poller.ts` 的 `acquire()` 顺序为：list pending → get request → 检查 completed/lease → put lease → 750ms 选举窗口 → re-read lease owner → delete pending。

这些阶段没有单独时间戳或直方图。此前真实 Job 间空洞不能区分 R2 list、payload get、exists/head、lease put、750ms election、owner re-read 和 pending delete。

## 8. Lease Acquire

当前仅在 lease JSON 写 `leased_at` 和 `lease_expires_at`。没有 `claim_started_at`、lease 请求开始/结束、选举失败次数、租约冲突次数或失败原因 Counter。

## 9. Local Sync Queue

Go `cloudruntime.Manager.MaterializeJobResult()` 会同步执行 remote manifest 读取、校验、逐 Part 下载、SHA256、临时目录写入和原子发布；已经记录 `local_sync_started_at/finished_at`。

当前不存在独立 Local Sync queue、queue depth、queued timestamp、并发上限、bytes in flight、磁盘 backlog 或 backpressure 状态。`local_sync_retry_total` 在 API 中仍固定返回 0，不能作为真实证据。

## 10. 现有 Metrics 与 Debug API

`GET /api/scheduler/metrics` 当前包含：Cloud Worker configured/alive/busy/idle、slot 列表、max SQD inflight、Job queued/leased/running/uploading/syncing/validating、近一小时 completed/failed、Registry rows/parts 等。

方案要求的六个本地/管理员 Debug API 当前全部为 HTTP 404：

- `/api/debug/sqd/metrics`；
- `/api/debug/cloud/workers`；
- `/api/debug/cloud/queue`；
- `/api/debug/cloud/timing`；
- `/api/debug/benchmark/current`；
- `/api/debug/backpressure`。

## 建议实施顺序

1. 在 Processor 新增可注入的 `ObservedHttpClient`，通过 `PortalClient` 注入 DataSource；每个实际响应在 `afterResponseHeaders` 计数，每个无响应传输错误在 retry/final error 边界计数，避免 response 与 retry 日志重复计数。
2. 把直接 `getFinalizedHead()` 也改走同一受观测客户端，避免分母漏掉 head 请求。
3. 每个 Job 持久化 request totals、分类 totals、duration samples/histogram buckets、TTFA、backoff；Prometheus labels 只使用 chain/dataset/provider/worker/job_type/endpoint。
4. 在 JobPoller 精确记录 list、payload load、lease put、election 和 delete 阶段；Worker 记录上一 Job fetch 释放到下一 Job SQD start 的 idle gap 和 reason。
5. Go 端只读取 Processor 持久化的结构化 Counter，计算 rate 与分位数，并增加仅本地可访问的 Debug API。
6. 增加 503、429、timeout、lease failure 故障测试；全部通过后才执行 `BASELINE_C1_50K`。

## 第一轮验收

- [x] 已定位 SQD request 与 retry 真实入口。
- [x] 已定位现有 503/429 文本计数边界。
- [x] 已核对 Job lifecycle、Worker、R2 claim/lease、Local Sync 和 API。
- [x] 已确认主批次保持 `PAUSED`，没有恢复下载。
- [ ] 503/429/timeout/retry 具有物理请求分母。
- [ ] request duration 与 TTFA 分位数可计算。
- [ ] Worker idle reason 与 claim/lease 分段耗时可见。
- [ ] Local Sync queue/backpressure 可见。
- [ ] Safety Gate 可自动判定。

结论：第一轮代码检查完成；Phase 2 Observability 实施尚未开始，Safety Gate 必须保持 `BLOCKED`。

## 2026-08-27 实施与实测补全

前述差距已完成以下修复：

- Processor 通过注入 `ObservedSqdHttpClient` 记录物理请求总数、成功/失败、503、429、timeout、network、DNS、cancelled、retry success/exhausted 和有界 duration samples；直接 finalized-head 请求也使用同一观测客户端。
- Manifest/status/_SUCCESS 持久化 logical query、physical request、request latency、TTFA、retry backoff、CPU/RSS 和完整生命周期；Go Cloud Runtime 只聚合结构化 Counter，缺少分母时保持 null/BLOCKED。
- Job Poller 与 Worker 新增 queue list/read、lease write/acquire、claim、idle gap、idle reason 和累计 claim/lease Counter；修复 4 槽位因 R2 无原子 CAS 而重复领取同一 Job 的竞态，单 Worker 内 claim 串行，fetch/upload 仍并发。
- 新增仅 loopback 可访问的 `/api/debug/sqd/metrics`、`/api/debug/cloud/workers`、`/api/debug/cloud/queue`、`/api/debug/cloud/timing`、`/api/debug/benchmark/current`、`/api/debug/backpressure`。转发头不能绕过本地限制。
- Cloud 镜像不包含 `ps`，资源采样改为 Linux `/proc/<pid>/status` 与 `/proc/<pid>/stat`，Span Benchmark 已获得 100% CPU/RSS 样本覆盖。
- Benchmark 工具只允许 `bsc/benchmarks/` 隔离前缀；入队前读取 SQD finalized head，并强制最高测试块低于链头安全余量，避免等待未来区块。

### 单变量 Benchmark 结论

- `BASELINE_C1_50K`：2,204.87 blocks/s，30/30，Safety Gate PASS。
- `BENCH_C2_50K`：4,051.61 blocks/s，相对 C1 +83.76%，查询 p95 +6.89%，PASS。
- `BENCH_C4_50K_V2`：6,521.03 blocks/s，相对 C2 +60.95%，查询 p95 -2.23%，30/30、失败 0，PASS；最佳安全并发为 4。
- `SPAN_C4_100K`：9,487.04 blocks/s，峰值 RSS 146.76 MB，30/30、失败 0，PASS。
- `SPAN_C4_200K_V2`：14,430.36 blocks/s，峰值 RSS 146.62 MB，30/30、失败 0，PASS。
- `SPAN_C4_500K`：21,510.40 blocks/s，峰值 RSS 139.08 MB，30/30、失败 0，PASS；当前推荐 Span 为 500K。
- `SPAN_C4_1M` 在 5 个完成样本/500 万块后吞吐仅 7,182.64 blocks/s，比 500K 下降 66.61%，查询 p95 增加 249.29%；按有效吞吐门提前停止，2M 未解锁。

C4 首轮暴露重复领取竞态，修复后使用全新队列重跑通过。200K 首轮越过 finalized head，已判无效并新增强制链头预检；历史区间 V2 重跑通过。所有正式报告和比较证据位于 `benchmark_results/`。

### 当前安全边界

- Processor 已恢复生产前缀 `bsc/jobs/`，配置并发 4、SQD inflight 上限 8、R2 upload workers 2/容量 4，并已部署。
- 主批次 `11abc33f-3cbd-4158-a959-a3ccf920c1b5` 继续 `PAUSED`，`running_ranges=0`、`completed_ranges=31`，本轮未恢复。
- `/api/debug/backpressure` 已提供 NORMAL/SOFT/HARD 状态和队列深度，但当前吞吐字段仍为 null 且 `observational_only=true`；Local Sync/Validation/ClickHouse 的合成积压与降载行为尚未完成端到端验证，因此仍不满足恢复主任务的全部条件。
