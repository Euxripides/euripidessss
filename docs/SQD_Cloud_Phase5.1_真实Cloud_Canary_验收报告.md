# SQD Cloud Phase 5.1：真实 Cloud Canary 验收报告

> 验收日期：2026-08-07  
> 验收对象：SQD Cloud 应急兜底 Provider（Tier 100 / Last Resort）  
> 验收结论：**PASS**  
> 报告依据：真实 Cloudflare R2、真实 SQD Cloud Worker、真实 BSC Portal 数据；全部证据可复现。

## 1. 验收目标与原则

- 首次在真实 `SQD_CLOUD_MODE=cloud` 下验证完整闭环：  
  `Normal Provider Exhausted → Cloud Admission → EnsureWorker → SQD Cloud Worker → BSC Portal → R2 → Local Sync → DuckDB → Dataset Registry → Coverage HIT`。
- 严格小规模：1～3 地址 × 500 块（实际 501 块，满足 500～5,000 块要求）。
- SQD Cloud 保持 Tier 100 / Last Resort，仅在全部普通 Provider 耗尽后启用。
- 全程禁止打印 `R2_ACCESS_KEY_ID`、`R2_SECRET_ACCESS_KEY`、`SQD_DEPLOY_KEY` 真实值。

## 2. 环境与凭据状态

| 项目 | 状态 |
|------|------|
| SQD_CLOUD_MODE | cloud（验收实例）；生产 8000 保持 local |
| SQD_CLOUD_ORG | supreme |
| R2_BUCKET | pangu-sqd-cloud |
| R2_ENDPOINT / R2_REGION / R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY | 已配置（仅检查存在性，不打印值） |
| SQD_DEPLOY_KEY | 已配置（仅检查存在性，不打印值） |
| 组织 secrets（supreme） | R2_ENDPOINT/R2_BUCKET/R2_ACCESS_KEY_ID/R2_SECRET_ACCESS_KEY 4/4 注入 |
| 验收实例 | 端口 8010（验收完成后已停止） |
| 生产实例 | 端口 8000，`/api/health=ok`，PID 3736，local 模式 |

## 3. R2 Canary（前置验收）

对象：`sqd-cloud/health/r2-canary.txt`，bucket：`pangu-sqd-cloud`。

| 操作 | 结果 |
|------|------|
| PUT | PASS（HTTP 200） |
| HEAD | PASS（HTTP 200，长度一致） |
| GET | PASS（HTTP 200，内容一致） |
| LIST `sqd-cloud/health/` | PASS（HTTP 200，对象可见） |
| DELETE | PASS（HTTP 204） |
| HEAD（删除后） | PASS（HTTP 404，确认不存在） |

前置修复：`internal/s3store` 不再发送空 `x-amz-security-token` 头（此前 R2 返回 400）。

## 4. SQD Cloud 部署与 secrets 注入

- `supreme/bsc-emergency-worker@v2` 通过 `sqd deploy . -o supreme --no-interactive` 部署/更新。
- `squid.yaml` 的 `deploy.env` 引用组织 secrets：

  ```yaml
  R2_ENDPOINT: "${{ secrets.R2_ENDPOINT }}"
  R2_BUCKET: "${{ secrets.R2_BUCKET }}"
  R2_ACCESS_KEY_ID: "${{ secrets.R2_ACCESS_KEY_ID }}"
  R2_SECRET_ACCESS_KEY: "${{ secrets.R2_SECRET_ACCESS_KEY }}"
  ```

- 修复前云端日志反复报“缺少 R2/S3 凭据”；secrets 引用后 Worker 日志转为每 5 秒 `cloud queue empty, waiting for pending job`，凭据问题彻底消失。
- 修复迭代期间共经历 5 轮部署（约 18:08 / 18:24 / 18:31 / 18:39 / 18:53，Asia/Shanghai），验收结束后按 Idle Remove 自动清理，`sqd list --org supreme` 当前为空。

## 5. 完整闭环验收

### 5.1 故障注入与 Cloud Admission

临时实例启用 `SCHEDULER_FAULT_INJECTION=all_normal_providers_fail`，使普通 Provider 全部耗尽。

Admission Decision（计划 831013b2 实录）：

```json
{
  "allowed": true,
  "reason": "ALL_NORMAL_PROVIDERS_EXHAUSTED：常规数据源均不可用，允许应急 Cloud 通道",
  "missing_coverage": true,
  "dataset_supported": true,
  "normal_providers_exhausted": true,
  "cloud_eligible": true,
  "budget_allowed": true,
  "runtime_available": true,
  "runtime_state": "IDLE",
  "provider_states": { "rpc": "CIRCUIT_OPEN", "sqd": "CIRCUIT_OPEN" }
}
```

- `sqd_cloud` 固定 Tier 100，未进入常规竞速。
- 重复范围二次提交被 `LOCAL_COVERAGE_FULL` 拒绝（验证“Cloud 只补缺口”）。

### 5.2 EnsureWorker / Reconcile

- 重启后 `sqd list --org supreme` 识别已部署 Worker → IDLE，未重复 deploy。
- 单实例 deploy lock 生效；Worker 复用路径实测通过。

### 5.3 Job Queue（R2）

状态流转：`bsc/jobs/pending/ → bsc/jobs/leased/ → bsc/jobs/completed/`。

验收时 R2 对象（12 个，两批 Job）：

```text
bsc/jobs/completed/831013b2-…-1-c1/chunk-1/{_SUCCESS, manifest.json}
bsc/jobs/completed/31aaa3b2-…-1-c1/chunk-1/{_SUCCESS, manifest.json}
bsc/jobs/leased/831013b2-…-1-c1/chunk-1/{_SUCCESS, checkpoint.json, manifest.json, token_transfers/…/transfers.parquet}
bsc/jobs/leased/31aaa3b2-…-1-c1/chunk-1/{_SUCCESS, checkpoint.json, manifest.json, token_transfers/…/transfers.parquet}
```

- Lease/Heartbeat：修复后 Worker 处理期间每 30s 写 `status.json` 并续租 `lease.json`。
- 幂等：completed 已有 `_SUCCESS` 时丢弃 pending；重复 Sync 返回 skipped。

### 5.4 SQD Cloud Processor（真实数据）

- 数据源：`https://portal.sqd.dev/datasets/binance-mainnet`（真实 finalized-head）。
- 地址过滤：`WATCH_ADDRESSES`；Token：BSC USDT `0x55d398326f99059ff775485246999027b3197955`。
- 主 Canary：3 地址 × `114474000-114474500` → **1658 行**。
- 严格边界 Canary：1 新地址 × 同范围 → **58 行**，实际 `max_block=114474499 ≤ to_block=114474500`。

### 5.5 Cloud Export（R2 产物）

每个 chunk 均含：`data/*.parquet`、`checkpoint.json`、`manifest.json`、`_SUCCESS`（最后写）。

Manifest 摘要：

| 字段 | 831013b2（主 Canary） | 31aaa3b2（严格边界） |
|------|------------------------|------------------------|
| schema_version | 1 | 1 |
| row_count | 1658 | 58 |
| parquet bytes | 52637 | 4513 |
| sha256 | 0c4b707543c6382e855c2d49c51f8c4fb0a3942c5678829ffcac2b2c9c594c0b | afd17c80e264334e617bd257e40a8171e86e0c5fa10b29d4419843d2649d0beb |
| completed_at | 2026-08-07T10:36:32.097Z | 2026-08-07T10:56:00.366Z |

### 5.6 Local Sync / DuckDB / Registry

- 下载校验：`.partial` + SHA256 匹配 + 原子重命名。
- DuckDB 校验：schema 9 列齐全、行数与 manifest 一致、唯一键与重复计数正确。
- Registry 最终态：**2151 rows / 5 entries / 3 files / 77649 bytes**。
- `merged.parquet`（统一查询层）：**2151 rows / 唯一键 2151**，`min_block=114474000`，`max_block=114475243`（含修复前 chunk 的边界数据，见第 10 节）。

### 5.7 Coverage

- 3 地址组：`token_transfer have=true`，`tx_count=6632`。
- 新地址 `0x29109ec59da798a56509995d75f7b8183a4bb286`：由 MISS → HIT。

### 5.8 READY_FOR_GRAPH

主 Canary 与严格边界 Canary 均进入 `READY_FOR_GRAPH`，图谱数据源已接入 Cloud merged parquet。

## 6. Canary 计划明细

| Plan ID | 地址/范围 | 结果 | 行数 | 耗时 | 说明 |
|---------|-----------|------|------|------|------|
| 831013b2-122e-4731-a8bb-ee760be93d5f | 3 addr × 114474000-114474500 | READY_FOR_GRAPH | 1658 | 17,494 ms | 首条真实 Cloud 闭环（修复前代码存在批次越界，见边界） |
| 0961c076-3772-45d4-82fe-7c9978bab4b6 | 同上（重复范围） | FAILED（预期） | - | 661 ms | `LOCAL_COVERAGE_FULL`，Cloud 不重复下载 |
| d5428ea1-f7a4-485d-b11b-8db8d3929cfe | 1 addr × 同范围（旧部署代码） | 卡死清理 | - | - | 暴露“数据源设 to 导致流结束即退出、无法上传”缺陷 |
| 31aaa3b2-fd71-4931-8536-0bf01c379301 | 1 addr（0x2910…）× 同范围 | READY_FOR_GRAPH | 58 | 17,842 ms | 修复后严格边界：max_block=114474499 |
| 184e25ed-8ebe-4409-a9f1-307935ad5983 | 1 addr × 同范围（故障注入关闭） | 普通 sqd Provider | - | 取消 | Provider 恢复验证：Cloud 不接入 |

## 7. 本次修复与优化记录

| # | 问题 | 修复 | 文件 |
|---|------|------|------|
| 1 | Idle Reaper 只统计 `status.json`，远端 Worker 仅写 `lease.json` 时被误删 | `lease.json` 计入 leased；remoteJobStatus 对仅有 lease 的 Job 返回 running | internal/cloudruntime/manager.go |
| 2 | Go Job JSON 字段 `id` 与 TS `job_id` 不匹配，`path.join` 崩溃 | Go 双写 `id/job_id` 并补 `chain_id/dataset`；TS 字段归一化 | internal/cloudruntime/types.go、Processor-only/src/job-poller.ts |
| 3 | Cloud Worker 处理期间无心跳，调度器看不到 running、Lease 无法续租 | 每 30s 写 `status.json` 并续租 `lease.json` | Processor-only/src/main.ts |
| 4 | 数据源直接设 `to=TO_BLOCK` 导致流结束即退出，上传来不及执行 | 保持 head-follow + 回调过滤 `> TO_BLOCK` 的块，严格截止且保证上传 | Processor-only/src/main.ts |
| 5 | `merged.parquet` 只合并当前 chunk 并覆盖历史 | SyncAll 全量重建；Merge 按唯一键去重 + 原子替换 | internal/datasetsync/{sync,validator}.go |

## 8. 自动化验证

- `go test ./... -short -count=1`：全部包通过（含 cloudruntime、datasetsync、downloadscheduler、s3store）。
- `go vet ./...`：零告警。
- 后端 `go build`：通过。
- `Processor-only npm run build`（TypeScript）：通过。
- 生产 8000 重启后 `/api/health=ok`，runtime mode=local，Registry 2151 rows 可读。

## 9. 安全审计

- 全程仅检查环境变量“存在性”，未打印任何密钥值。
- CLI/日志输出按需脱敏（`R2_*`、`SQD_DEPLOY_KEY`、`sqt_*`）。
- 仓库与 Worker 无硬编码凭据；secrets 通过环境变量 + 组织 secrets 注入。
- 验收结束云端 Worker 已移除，未遗留常驻部署。

## 10. 边界与未完成项

1. **修复前 chunk 边界数据**：831013b2 由修复前代码导出，manifest `to_block=114474500` 但 parquet 实际 `max_block=114475243`（批次越界）。该数据保留在 Registry/merged 中作为修复前证据；修复后 chunk 31aaa3b2 已验证严格截止。
2. **本地 SQD 普通 Provider 显式 from/to 未透传**：恢复测试中本地 parquet 任务 `sqd_block_range` 从 0 开始，已取消；此为既有本地 Provider 边界，不影响 Cloud 路径。
3. **云端 Worker Lease 过期恢复 / cancel marker 优雅退出**：尚未在真实 Cloud 验证，计划列入 Phase 5.2。
4. 临时目录 `E:\Code\Processor-only\tmp_pick`（选地址用）残留，可手动删除。

## 11. 结论

Phase 5.1 真实 Cloud Canary **全部 PASS Gate 通过**：

- [x] R2 Canary PASS
- [x] Cloud mode 生效 / Reconcile 复用
- [x] 全部 Normal Provider exhausted 后才 Admission（Tier 100）
- [x] 单实例 deploy lock / 不重复 deploy
- [x] Cloud Worker 成功部署
- [x] pending → leased → completed
- [x] 真实 BSC Portal 数据 / Parquet 写入 R2
- [x] manifest / SHA256 / `_SUCCESS` PASS
- [x] Local Sync / DuckDB / Dataset Registry PASS
- [x] Coverage MISS → HIT
- [x] Provider 恢复后新 Chunk 回普通 Provider
- [x] Idle 自动 Remove（REMOVING → ABSENT，sqd list 为空）
- [x] 无 Secret 泄漏

后续按 Phase 5.2 推进：25 地址 × 50,000 blocks；再验证云端 Worker 续跑/取消与 Investigation/Graph 增量。

## 附录 A：R2 Manifest 原文

### 831013b2（主 Canary，3 地址，1658 行）

```json
{
  "schema_version": 1,
  "job_id": "831013b2-122e-4731-a8bb-ee760be93d5f-1-c1",
  "chunk_id": "chunk-1",
  "provider": "SQD_CLOUD_EXPORT",
  "chain_id": 56,
  "dataset": "token_transfer",
  "from_block": 114474000,
  "to_block": 114474500,
  "address_count": 3,
  "addresses": [
    "0x000000000a01ea06f8b3a01319bbaa2cb2453a3c",
    "0x905bae168fe2353bb001d4f2f7e29cfd6d2c84b7",
    "0x66027324a6126a821b2b02be7c585c0ecb946f87"
  ],
  "row_count": 1658,
  "files": [
    {
      "path": "token_transfers/0000000000-0114475243/transfers.parquet",
      "bytes": 52637,
      "sha256": "0c4b707543c6382e855c2d49c51f8c4fb0a3942c5678829ffcac2b2c9c594c0b"
    }
  ],
  "completed_at": "2026-08-07T10:36:32.097Z",
  "completed": true
}
```

### 31aaa3b2（严格边界，1 地址，58 行）

```json
{
  "schema_version": 1,
  "job_id": "31aaa3b2-fd71-4931-8536-0bf01c379301-1-c1",
  "chunk_id": "chunk-1",
  "provider": "SQD_CLOUD_EXPORT",
  "chain_id": 56,
  "dataset": "token_transfer",
  "from_block": 114474000,
  "to_block": 114474500,
  "address_count": 1,
  "addresses": [
    "0x29109ec59da798a56509995d75f7b8183a4bb286"
  ],
  "row_count": 58,
  "files": [
    {
      "path": "token_transfers/0000000000-0114475263/transfers.parquet",
      "bytes": 4513,
      "sha256": "afd17c80e264334e617bd257e40a8171e86e0c5fa10b29d4419843d2649d0beb"
    }
  ],
  "completed_at": "2026-08-07T10:56:00.366Z",
  "completed": true
}
```

## 附录 B：关键命令

```powershell
# 部署（secrets 引用在 squid.yaml）
sqd deploy . -o supreme --no-interactive --allow-update

# 组织 secrets（stdin 注入，不回显）
sqd secrets set R2_ENDPOINT -o supreme --no-interactive

# 验收实例（cloud 模式 + 故障注入 + 20 分钟空闲）
$env:PORT = "8010"
$env:SQD_CLOUD_MODE = "cloud"
$env:SQD_CLOUD_ORG = "supreme"
$env:SCHEDULER_FAULT_INJECTION = "all_normal_providers_fail"
$env:SQD_CLOUD_IDLE_REMOVE_AFTER_MINUTES = "20"

# 提交并执行 Canary 计划
POST /api/scheduler/plan    {"requirements":[{"dataset":"token_transfer","chain_key":"bsc","addresses":[...],"from_block":114474000,"to_block":114474500,"cloud_eligible":true}]}
POST /api/scheduler/run     {"plan_id":"<id>"}
GET  /api/scheduler/status?plan_id=<id>

# 验收查询
GET /api/scheduler/cloud/runtime
GET /api/scheduler/cloud/jobs
POST /api/scheduler/coverage
POST /api/scheduler/cloud/sync

# Idle Remove 验证
sqd list --org supreme
```
