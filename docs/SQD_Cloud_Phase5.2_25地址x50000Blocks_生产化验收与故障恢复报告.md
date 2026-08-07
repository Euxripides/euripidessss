# SQD Cloud Phase 5.2：25 地址 × 50,000 Blocks 生产化验收与故障恢复报告

> 验收日期：2026-08-07  
> 基线：Phase 5.1 真实 Cloud Canary PASS  
> 验收结论：**PASS（含记录边界）**  
> 原则：SQD Cloud 保持 Tier 100 / Last Resort；仅小规模生产化验证；全程无 Secret 泄漏。

## 1. 验收范围

- 主规模：25 地址 × 50,000 blocks（实际 `114450000-114499999`）。
- P0：隔离修复前越界 chunk；普通 SQD Provider from/to 精确透传；completed 后 lease 幂等语义。
- 故障恢复：Lease 过期恢复、Cancel Marker、Worker Crash/Restart、Local Sync 失败重试、Provider 恢复、Idle Remove。
- Validator / Registry / merged / Coverage 严格性与审计。

## 2. 环境与最终状态

| 项目 | 状态 |
|------|------|
| 验收实例 8010 | cloud 模式，验收完成后已停止 |
| 生产实例 8000 | local 模式，健康（PID 23872） |
| Cloud Worker | 验收结束按 Idle Remove 移除，`sqd list --org supreme` 为空 |
| R2 / secrets | 配置存在性通过，全程未打印值 |
| Registry（ACTIVE） | 7 entries / 484,338 rows / 12 files / 17,044,230 bytes |
| merged.parquet | 484,338 rows / 唯一键 484,338 / dup=0 |

## 3. P0 验收

### P0-1 修复前越界 Chunk 隔离

- 隔离对象（保留原始 R2/本地证据）：
  - `831013b2…`：manifest.to_block=114474500，parquet.max_block=114475243 → `INVALID_RANGE_LEGACY`
  - `8ab1f079…`：manifest.to_block=114474500，parquet.max_block=114475242 → `INVALID_RANGE_LEGACY`
- 隔离后：
  - Registry `Stats()` 只计 ACTIVE（58+484250+30=484,338）；
  - `merged.parquet` 排除隔离 chunk（重建后 484,338 唯一）；
  - Coverage 排除（3 个旧地址 `have=false`）；
  - 审计字段 `quarantine_reason` 保留；R2 manifest 原文保留。
- **多实例并发修复**：测试实例与生产实例共享 `registry.json`，旧内存缓存整文件覆盖会冲掉隔离状态。修复：`Registry.saveLocked` 保存前以磁盘为准刷新未被本次更新的条目，读方法也刷新磁盘视图；新增 `TestRegistryMultiInstanceSavePreservesQuarantine`。

### P0-2 普通 SQD Provider from/to 透传

- `StartRequest` 新增 `FromBlock/ToBlock`；`Preview` 收到显式范围时直接使用；`SQDProvider.Execute` 原样透传。
- 单元测试：`TestSQDProviderFromToPassthrough`（114474000-114474500）。
- 真实验证：计划 `45bfe2d9…`（地址 1 个，from/to=114450000/114499999）选中普通 `sqd`，下游 Parquet 任务 `e980e9be223c60ee` 的 `sqd_block_range = 114450000-114499999`，与需求完全一致。

### P0-3 completed 后 lease 语义

- completed `_SUCCESS` 为最终幂等判据；`remoteJobStatus` 先查 completed，并清理残留 `lease.json/status.json`（tombstone）。
- `acquirePendingJob` 见到 completed 时丢弃 stale pending。
- 单测：`TestCancelMarkerAndCompletedIdempotency`（completed + stale lease/pending → done，pending 被清理）。

## 4. 主 Canary：25 地址 × 50,000 Blocks

### 4.1 任务

| 项 | 值 |
|----|----|
| Plan | `c1cd1c88-44f5-4415-93f1-162a13e91d53` |
| Job | `c1cd1c88-…-1-c1` |
| 地址数 | 25（全部为本地 Coverage MISS） |
| Block Range | 114450000 – 114499999 |
| Admission | `ALL_NORMAL_PROVIDERS_EXHAUSTED`（故障注入） |
| Provider | `sqd_cloud`（Tier 100） |
| 结果 | `READY_FOR_GRAPH` |
| Rows | 484,250 |
| Latency | 632,537 ms（~10.5 分钟） |
| R2 产物 | 10 个 parquet + checkpoint + manifest + `_SUCCESS`（最后写） |

### 4.2 严格区块边界

- Validator 新增 `range_violation_count` / `unexpected_address_count`；越界即 `LOCAL_VALIDATION_FAILED`，不登记 ACTIVE、不进 merged。
- 实测：manifest `to_block=114499999`，parquet `min=114450000, max=114499999`，越界 0；`unique=484,250, dup=0`。

### 4.3 Heartbeat

真实观察 3 次续租（UTC）：

| 采样 | heartbeat_at | lease_expires_at |
|------|--------------|------------------|
| t0 | 12:41:25 | 12:51:28 |
| t0+60s | 12:42:25 | 12:52:28 |
| t0+90s | 12:43:25 | 12:53:27 |

`expires_at` 单调后移；Runtime 保持 `REMOTE_RUNNING`；Idle Reaper 未误删；同 job 未被重复抢占。

### 4.4 R2 / Registry / Coverage

- Manifest：`schema_version=1`，`row_count=484250`，10 个文件 SHA256 逐一记录，`completed_at=2026-08-07T12:51:12.910Z`。
- Registry：新 entry `COMPLETED/INDEXED`，`uniq=484250, dup=0, min=114450000, max=114499999`。
- 25 地址 Coverage：`MISS → HIT`（tx_count 聚合 12,735,784）。
- merged.parquet：484,338 行全唯一（含 5.1 严格 chunk 58 行与恢复 chunk 30 行）。

## 5. 故障恢复验收

### 5.1 Cancel Marker（计划 4073696c…）

- API：`POST /api/scheduler/cloud/jobs/cancel` 写入 `bsc/jobs/cancel/<job_id>.json`。
- Worker 观察到 marker 后：写最终 checkpoint（`cancelled:true`）→ 写 `bsc/jobs/cancelled/<job>/_CANCELLED` + `cancelled.json` → 删除 lease/status → 退出。
- 验证：无 `completed/_SUCCESS`；partial 数据未登记 Registry；checkpoint 可恢复。
- 边界：本次实测运行时使用的二进制尚未包含 `JobProgress` 对 `cancelled` 终态的处理（计划状态停留在 VALIDATING）；该处理已补代码（`cloud_provider.go` 增加 `case "cancelled"`）并有单测路径，建议 Phase 5.3 用新二进制重放一次 UI 终态。

### 5.2 Lease 过期 / Worker Crash 恢复（计划 f76b40f4…）

流程：

```text
运行中（checkpoint 114453237）
→ 受控 sqd restart
→ 注入 lease 过期（heartbeat 停止）
→ 本地 leaseReaper 检测过期 → 删除 lease/status → 以同一 job_id 重新入队 pending
→ 新 Worker 领取 → resumeFromCheckpoint（114453238）→ 完成
```

证据：

- `bsc/jobs/requeued/f76b40f4…/requeue.json`（`reason=LEASE_EXPIRED`）存在；
- Worker 日志：`cloud job resume from checkpoint {"checkpoint":114453237,"resume_from":114453238}`；
- 单 completed manifest/_SUCCESS，`dup=0`；Registry 1 条 entry，`uniq=30`；
- 未从 from_block 全量重跑（resume 起点 114453238）。

边界：Worker 容器重启后本地 parquet 不保留，manifest `row_count` 与实际 parquet 行数一致（30 行，均来自 resume 后区间）；崩溃前已处理但未落 R2 的行不重复计数。若需跨重启行累计，需将 parquet 增量写入 R2（Phase 5.3 建议）。

### 5.3 Local Sync 失败恢复

- 故障注入：`DATASETSYNC_FAULT_INJECTION=first_download_fail`。
- 第一次 Sync：`c1cd1c88…` → `FAILED / LOCAL_SYNC_FAILED`（不对外、不进 merged）。
- 关闭注入后重试：重新下载校验 → `COMPLETED / INDEXED`，484,250 行，dup=0，merged 重建。
- 验证：Cloud Processor 未重新抓取（R2 manifest 未变、无新 Job）；R2 parquet 未重新生成（同一批 SHA256）。

### 5.4 Provider 恢复 + from/to 透传

- 关闭故障注入后提交未覆盖窗口 → 选中普通 `sqd`；Cloud Admission 拒绝（`NORMAL_PROVIDER_AVAILABLE`）；Cloud runtime IDLE。
- 下游 Parquet `sqd_block_range` 与需求完全一致（见 P0-2）。

### 5.5 Idle Remove

- 无 pending/leased/running 后设置 2 分钟空闲：

```text
IDLE → REMOVING → ABSENT
sqd list --org supreme → 空
```

## 6. Validator / Registry / merged 验收

| 检查项 | 结果 |
|--------|------|
| schema（9 列） | PASS |
| row_count 与 manifest 一致 | PASS |
| sha256 逐文件 | PASS |
| unique key / dup | 484,338 / 0 |
| min/max block 严格范围 | PASS（c1cd: 114450000-114499999；31aaa: 114474000-114474499） |
| range_violation_count | 0 |
| unexpected_address_count | 0 |
| 隔离/失败/取消不参与 merged | PASS |
| Coverage 排除隔离 | PASS |

## 7. 自动化验证

- `go test ./... -short -count=1`：全部通过（含新增 lease expiry、cancel、completed 幂等、strict range、quarantine 排除、sync retry、from/to 透传、多实例 registry 保存测试）。
- `go vet ./...`：零告警。
- `go build ./...` / `npm run build`：通过。

## 8. 成本与审计

- `fallback_ratio = 0.114`（Cloud 兜底占比低，未提升优先级）。
- Cloud jobs（mode=cloud）：`831013b2`（1658）、`31aaa3b2`（58）、`c1cd1c88`（484,250）、`f76b40f4`（30）＝ 4 jobs / 485,996 rows。
- ACTIVE Registry：484,338 rows / 17,044,230 bytes。
- `sqd_cloud` 保持 Tier 100；Worker 验收后已移除，无常驻部署。

## 9. 安全

- 全程仅检查环境变量存在性，不打印 `R2_ACCESS_KEY_ID` / `R2_SECRET_ACCESS_KEY` / `SQD_DEPLOY_KEY`。
- CLI/日志输出按需脱敏；API 不返回 Secret。
- 仓库无新增硬编码凭据。

## 10. 边界与后续

1. Cancel 的 UI 终态重放（新二进制）留待 Phase 5.3；R2 协议与单测已 PASS。
2. Worker 容器重启后 parquet 增量不跨重启累计；若生产要求断点精确行数，需增量上传 parquet 分片。
3. 25 地址均含真实 Transfer；0-row chunk 由既有 0-row 条目覆盖（合法）。
4. `E:\Code\Processor-only\tmp_pick` 临时目录残留（工作区策略阻止递归删除），可手动清理。
5. 生产 8000 已更新为新后端（local 模式）；切换 cloud 模式后需按 Idle/Reconcile 流程重演一次。

## 11. 关键文件

- `internal/datasetsync/{registry,sync,validator}.go`（隔离/多实例合并/范围校验/失败重试）
- `internal/cloudruntime/manager.go`（Lease Reaper、Cancel Marker、completed tombstone、JobID 双写）
- `internal/downloadscheduler/{cloud_provider,provider,scheduler}.go`（cancelled 终态、from/to 透传、Cancel API）
- `internal/parquetdownload/{types,manager}.go`（显式区块范围）
- `internal/api/download_scheduler_handlers.go`（`POST /cloud/jobs/cancel`）
- `E:\Code\Processor-only\src\{main,job-poller}.ts`（checkpoint resume、cancel、心跳 checkpoint）
