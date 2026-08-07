# SQD Cloud Phase 4 — Job Worker + R2/S3 生产导出实施报告

> 对应设计文档：`D:\下载文件\SQD_Cloud_Phase4_JobWorker_R2_Production_Export.md`
> 实施日期：2026-08-07

## 1. 结论

Phase 4 生产数据面（Control Plane → Job Queue/Lease → Processor → 输出契约 → Local Sync → Validator → Registry/Coverage → DuckDB）已实施并部署：

- 本地控制面 `E:\codex\etl`：R2/S3 ObjectStore、Job Queue 协议、Cloud Provider Chunk 化、Local Sync + Validator + Registry、Reconcile、API。
- Cloud Worker `E:\Code\Processor-only`：从固定 env smoke worker 升级为 **Job-driven Worker**（轮询 R2/Local pending 队列、Lease、Heartbeat、Checkpoint、Manifest、`_SUCCESS`）。
- **真实 BSC 单 Chunk 兜底闭环已在本机 local 模式完成**：Portal 实际返回数据（finalized head 114,497,629），3 地址 × 500 块窗口产出 435 行真实 USDT Transfer，经 SHA256 + Parquet 校验后登记 Registry 并合并 DuckDB，覆盖检查返回 have=true。
- **真实 SQD Cloud（`cloud` 模式）部署未执行**：本环境无 `SQD_DEPLOY_KEY` 与 R2 凭据；代码路径已实现，凭据就绪后按验收表执行 Phase 4.8。

## 2. 实施清单

| 设计章节 | 实现 | 状态 |
| --- | --- | --- |
| §4-5 Job-driven Worker | `src/worker.ts` + `job-poller.ts` + `job-runner.ts` | ✅（local 验证，cloud 待密钥） |
| §6-10 队列/Lease/幂等 | `bsc/jobs/{pending,leased,completed,failed}`；lease.json TTL 10m；completed/_SUCCESS 跳过 | ✅ |
| §11-12 ObjectStore/Secret | Go `internal/s3store`（SigV4）+ TS `object-store.ts`；R2_LOCAL_ROOT 本地模式；密钥仅环境变量 | ✅ |
| §13-15 目录/状态/Checkpoint | `status.json`（RUNNING/EXPORTING/COMPLETED）、`checkpoint.json`、`manifest.json`、`_SUCCESS` | ✅ |
| §16-17 重启恢复 | Checkpoint 记录 last_completed_block；Worker 重启从 lease/checkpoint 续跑（V1 单进程语义，云端多进程续跑待真实部署验证） | 🟡 |
| §18-19 Parquet/地址过滤 | token_transfer 9 列 schema；WATCH_ADDRESSES 本地过滤 + `filter_mode` 记录 | ✅ |
| §20 Chunk 大小 | 25 地址 × 50,000 区块（配置化常量） | ✅ |
| §21-23 Flush/Manifest/_SUCCESS 顺序 | 移除 isHead 强制 Flush（改 FORCE_FLUSH_BLOCKS）；manifest → `_SUCCESS` 最后写 | ✅ |
| §24-25 校验/0 行 | Parquet footer/行数/唯一键/重复校验；0 行 Chunk 合法完成 | ✅ |
| §26-31 Local Sync/Registry/Coverage | `.partial`+SHA256+原子改名；DuckDB Validator；Registry 含地址；覆盖复合源 | ✅ |
| §32-37 Cloud 生产模式/密钥/组织/Release | cloud 模式 EnsureWorker→`sqd deploy`；org=supreme（可配置）；固定 Worker Release；`.squidignore` 排除构建产物 | ✅（代码） |
| §38-39 Idle 安全 | pending/leased/running 均为 0 才 Remove | ✅ |
| §40-45 Provider Submit/Status/Cancel/Retry | Submit=EnsureWorker+enqueue；Status 读远端；失败保留证据（failed/error.json）；retry 沿用 chunk_id+attempt（V1 提供字段，重试逻辑由任务级 fallback 承担） | 🟡 |
| §46-50 审计/API/前端/密钥 | ProviderAttempts 含 remote job/chunk；`/cloud/jobs`、`/cloud/sync`、`/cloud/usage`；前端状态；deployment_key_configured 仅布尔 | ✅ |
| §51-55 网络阻断与真实验收 | 本机 local 模式完成真实 Portal 单 Chunk；cloud 内 Portal 待密钥部署 | 🟡 |
| §56-58 调查/图联动/回切 | 调度器统一入口已接入；Graph/Investigation 自动消费受固定快照数据源边界限制（既有） | 🟡 |
| §59-61 预算/崩溃恢复/Reconcile | 用量审计按 Job 分钟；启动 Reconcile（sqd list --org） | ✅ |
| §62-70 归属/日志/超时/心跳 | org/name/slot 归属校验；结构化日志；10m 超时；30s Heartbeat；progress.json | ✅ |
| §71-75 V1 范围/目录/配置 | 仅 BSC token_transfer、单 Worker；目录与 env 配置齐全 | ✅ |
| §76-79 测试 | Go 单元/集成（42 包）、故障注入、跨语言协议 E2E、DuckDB 真实校验 | ✅（除真实 R2/SQD Cloud） |

## 3. 真实单 Chunk 验证证据（2026-08-07 14:33-14:41）

1. 临时实例（8010）+ `SCHEDULER_FAULT_INJECTION=all_normal_providers_fail` + `SQD_CLOUD_MODE=local`。
2. `POST /api/scheduler/plan`：token_transfer，3 地址（无本地覆盖），显式区块 114,474,000-114,474,500。
3. Admission：`ALL_NORMAL_PROVIDERS_EXHAUSTED`（故障注入使 RPC/SQD 熔断）→ 批准。
4. Runtime：`ABSENT→READY→BUSY`；`leased_jobs=1`，`running_job=...-1-c1`。
5. Processor（`node lib/main.js`）从 Portal 实拉：finalized head 114,497,629，按 TO_BLOCK 有界执行。
6. 产出 **435 行** USDT Transfer（manifest row_count=435，文件 20499 字节，SHA256 2961703f...）。
7. `POST /api/scheduler/cloud/sync`：下载 → SHA256 通过 → DuckDB 校验（Schema 9 列、435 行、唯一键 435、重复 0、min 114474003/max 114475242）→ Registry 登记 → 合并 parquet。
8. 覆盖检查：地址 `0x0aba...` 由 0 → `have=true, tx_count=435`。
9. 0 行 Chunk 验证：`0d652c2a...`（5k 块，地址无转账）合法完成并登记 0 行。

## 4. 运行模式与凭据

- 生产当前 `SQD_CLOUD_MODE=auto` → 无 `SQD_DEPLOY_KEY` → `local`（本机 Processor + 本地 store）。
- `cloud` 模式需：`SQD_DEPLOY_KEY`、`R2_ENDPOINT/R2_BUCKET/R2_ACCESS_KEY_ID/R2_SECRET_ACCESS_KEY`（或 `R2_BACKEND=local` 仅限测试）。
- 配置：`SQD_CLOUD_ORG`（默认 supreme）、`SQD_CLOUD_WORKER_NAME`（bsc-emergency-worker）、`SQD_CLOUD_WORKER_SLOT`（v2）。
- 测试开关：`SCHEDULER_FAULT_INJECTION=all_normal_providers_fail`（仅测试）；`RUN_TS_WORKER_E2E=1`（跨语言协议测试）。

## 5. 生产验收表（剩余项）

- [ ] `SQD_DEPLOY_KEY` + R2 凭据就绪后：`EnsureWorker → sqd deploy → sqd list 对账 → Reconcile` 真实验收。
- [ ] Cloud Processor 内 Portal 请求（本机网络阻断不阻塞生产）。
- [ ] Investigation/Graph 自动消费（需数据源切换或统一查询层接入 analyticsapi）。
- [ ] 云端多 Worker/重启续跑（Lease 过期恢复）真实验证。
- [ ] Normal Provider 恢复回切 + Cloud Idle Remove（协议已实现，云端实测待部署）。

## 6. 关键文件

- Go：`internal/s3store/store.go`、`internal/cloudruntime/{types,manager,queue}.go`、`internal/datasetsync/{registry,sync,validator}.go`、`internal/downloadscheduler/cloud_provider.go`
- TS：`E:\Code\Processor-only\src\{worker,job-poller,job-runner,object-store,main}.ts`
- 数据：`E:\codex\bsc_analytics\sqd-cloud\`（store/queue、jobs、registry.json、cloud_usage.json、sync/warehouse）
