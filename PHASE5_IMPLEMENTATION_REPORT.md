# SQD Cloud Phase 5 — Cloud Mode 生产激活 + Investigation/Graph 全链路实施报告

> 对应设计文档：`D:\下载文件\SQD_Cloud_Phase5_CloudMode生产激活_Investigation_Graph_E2E.md`
> 实施日期：2026-08-07

## 1. 结论

Phase 5 的**生产激活代码面**已全部落地并部署：

- 凭据安全注入（Environment Only）+ `CREDENTIALS_NOT_CONFIGURED` 显式拒绝（Test B 实测通过）；
- `EnsureWorker` 先 `sqd list --org` 复用已部署 Worker，禁止重复 Deploy（单测覆盖）；
- `Reconcile` 启动对账，且不会覆盖 NOT_CONFIGURED 状态；
- Auto Sync 双保险（Cloud 完成后事件触发 + 60s 后台轮询），Cloud Export 与 Local Sync 状态解耦；
- Legacy Manifest 治理（无 schema_version / 路径缺前缀 → 告警 + registry_skip，不自动删除）；
- Investigation 自动继续钩子（`NotifyDataReady` → 安全 Resume，仅 CREATED/WAITING 状态）；
- Graph 数据源接入（analyticsapi.Flows 联合 Cloud merged parquet，金额按十进制原值解析）+ 索引后缓存失效 + 前端自动刷新；
- `fallback_ratio` / `r2_configured` / `deployment_key_configured` KPI 与状态 API；
- 安全审计：仓库/Worker 无硬编码密钥，日志无密钥值，API 仅返回布尔。

**未完成（外部凭据阻塞）**：真实 SQD Cloud Deploy + 真实 R2 + Cloud 内 Portal Canary。当前环境没有 `SQD_DEPLOY_KEY` 与 R2 凭据，因此无法执行 `sqd deploy` 与 R2 写入；代码路径、验收脚本与状态机已就绪，凭据注入后按 §69 顺序执行。

## 2. 实施清单

| 设计章节 | 实现 | 状态 |
| --- | --- | --- |
| §4 Secret Provisioning | 仅环境变量；run.ps1 校验 cloud 模式必需变量，禁止硬编码 | ✅ |
| §8 EnsureWorker 复用 | 先 `sqd list --org`，已存在托管 Worker 直接复用，无 deploy | ✅（单测） |
| §9/§15 Reconcile | 启动对账；NOT_CONFIGURED 不被覆盖 | ✅ |
| §13 Secret Verification | API 仅返回 `deployment_key_configured` / `r2_configured` 布尔 | ✅ |
| §22-23 Auto Sync | Cloud 完成事件触发 + 60s 轮询；`POST /cloud/sync` 保留为人工恢复 | ✅ |
| §24/§27-29 幂等与 Legacy | Registry 幂等；无 schema_version/路径缺前缀 → 告警+skip，不删除 | ✅ |
| §30-31 Investigation 自动继续 | `InvestigationAgent.NotifyDataReady` → 安全 Resume | ✅（代码） |
| §32-33 Graph 增量消费 | Flows 联合 Cloud merged parquet + InvalidateCache + 前端自动刷新 | ✅ |
| §34-37 Recovery/Drain/Idle | 新任务重新选常规 Provider；Idle 需 pending/leased/running=0 | ✅（既有+回归） |
| §39-42 Usage/KPI | fallback_ratio、rows/bytes/entries、runtime 状态 | ✅ |
| §43-45 失败证据 | failed/error.json 保留 request/status/checkpoint | ✅（Phase 4） |
| §46 Cancel | cancel marker 协议（V1 未完整实现云端优雅取消，标记为边界） | 🟡 |
| §47-48 重启/Worker 恢复 | Backend Reconcile；Worker 续跑依赖 Lease/Checkpoint（云端实测待凭据） | 🟡 |
| §60-63 Monitoring/Security | 状态/KPI API；git/log/API 三层审计，无密钥值泄漏 | ✅ |
| §66 Test A/B | A：常规健康 → Cloud 0（既有回归）；B：凭据缺失 → CREDENTIALS_NOT_CONFIGURED | ✅ |
| §67 E2E | 真实 Cloud Canary（Deploy→Portal→R2） | ⛔ 凭据阻塞 |

## 3. 真实验证证据

### Test B：凭据缺失拒绝（2026-08-07 15:05）

临时实例 `SQD_CLOUD_MODE=cloud`（无 `SQD_DEPLOY_KEY`）+ 故障注入：

```text
runtime: NOT_CONFIGURED / available=false
reason : SQD Cloud 模式缺少 SQD_DEPLOY_KEY（密钥仅允许来自环境变量）
plan   : FAILED
task   : 应急 Cloud 未启用：CREDENTIALS_NOT_CONFIGURED：...
states : rpc=CIRCUIT_OPEN, sqd=CIRCUIT_OPEN（故障注入）
```

未发起 `sqd deploy`，拒绝原因可审计。

### Phase 4 数据面回归（继续有效）

```text
3 addresses × 500 blocks → 435 行真实 USDT Transfer
Local Sync SHA256 PASS → DuckDB 校验（435 行 / 唯一键 435 / 重复 0）
Registry：3 entries / 435 rows / 20499 bytes
Coverage：MISS → HIT（tx_count=435）
```

## 4. 生产激活步骤（凭据就绪后）

```powershell
$env:SQD_CLOUD_MODE = "cloud"      # 或 SQD_RUNTIME_MODE
$env:SQD_CLOUD_ORG  = "supreme"
$env:SQD_DEPLOY_KEY = "<Secret Store 注入>"
$env:R2_ENDPOINT    = "<R2 endpoint>"
$env:R2_BUCKET      = "pangu-sqd-cloud"
$env:R2_ACCESS_KEY_ID     = "<Secret Store 注入>"
$env:R2_SECRET_ACCESS_KEY = "<Secret Store 注入>"
.\run.ps1
```

启动后：`Reconcile → sqd list --org supreme`；Worker 缺失才 `sqd deploy`；Canary 规模 1~3 地址 × 500~5,000 块，然后 25×50k。

## 5. 安全审计结果

- `rg` 仓库与 Worker：无 `SQD_DEPLOY_KEY="..."` / `R2_SECRET_ACCESS_KEY="..."` 等硬编码赋值（exit 1 无匹配）。
- 日志：仅出现变量名（拒绝原因），无密钥值。
- API：`/cloud/usage` 只返回布尔与聚合指标，不返回任何凭据片段。
- 建议：R2 权限限定 `pangu-sqd-cloud` 单 bucket/prefix；定期轮换通过重启注入新 env 完成。

## 6. 未完成与边界

1. ~~真实 SQD Cloud Deploy / R2 / Cloud 内 Portal Canary~~（2026-08-07 已完成，见第 8 节；凭据经环境变量 + 组织 secrets 注入）。
2. 云端 Worker 重启续跑（Lease 过期恢复）与取消（cancel marker 优雅退出）需在真实 Cloud 环境验证。
3. Investigation 自动继续仅对 CREATED/WAITING 状态安全 Resume；RUNNING 中的调查依赖其任务循环自行读取新数据，若 AI 层未配置 DeepSeek，分析阶段仍受限（既有边界）。
4. Graph 增量：Flows 已联合 Cloud 数据并自动刷新；地址画像/风险等其余查询暂未联合 Cloud 数据（仅 Flows 接入）。

## 7. 关键文件

- `internal/cloudruntime/manager.go`（EnsureWorker 复用 / Reconcile / NOT_CONFIGURED 保护）
- `internal/datasetsync/sync.go`（Auto Sync 状态、Legacy 治理、_SUCCESS 前置）
- `internal/downloadscheduler/scheduler.go`（CloudSync 互斥 + StartAutoSync + fallback_ratio）
- `internal/downloadscheduler/admission.go`（CREDENTIALS_NOT_CONFIGURED）
- `internal/analyticsapi/service.go`（Cloud Flows 联合查询 + InvalidateCache）
- `internal/intelligence/investigation_agent.go`（NotifyDataReady）
- `run.ps1`（cloud 模式环境校验）
- `frontend/src/features/analytics/SmartFillPanel.tsx`（Cloud 状态 + 自动刷新）

## 9. Phase 5.2 生产化验收（2026-08-07）

Phase 5.2（25 地址 × 50,000 blocks + 故障恢复）已通过，详见
`docs/SQD_Cloud_Phase5.2_25地址x50000Blocks_生产化验收与故障恢复报告.md`。

要点：

- 主 Canary `c1cd1c88…`：484,250 行 / 632,537ms / 严格边界 / Registry 484,338（dup=0）/ Coverage HIT。
- P0：越界 legacy 隔离（INVALID_RANGE_LEGACY）、普通 SQD from/to 透传、completed 幂等。
- 故障恢复：Cancel、Lease 过期 Resume、Crash Resume、Sync 失败重试、Provider 恢复、Idle Remove 全部 PASS。

## 10. Phase 5.3 Investigation/Graph Cloud-Aware 联动（2026-08-07）

Phase 5.3（事件总线 + Investigation 自动恢复 + Graph 增量 + Cancel 重放 + Manifest V2）已通过，详见
`docs/SQD_Cloud_Phase5.3_Investigation_Graph_CloudAware联动与增量恢复报告.md`。

要点：DATASET_INDEXED 事件幂等驱动调查/图谱；Graph 增量去重（484,348 edges 无重复）；重启恢复；Cancel 新二进制重放；Manifest V2 sum(parts.rows)==row_count 实测。

## 8. Phase 5.1 真实 Cloud Canary 验收结果（2026-08-07）

### PASS Gate

| 验收项 | 结果 |
|--------|------|
| R2 Canary | PASS（PUT/HEAD/GET/LIST/DELETE/HEAD-after 404） |
| Cloud mode 生效 | PASS（mode=cloud，deployment_key/r2_configured=true） |
| Reconcile 复用 | PASS（重启后 IDLE，无重复 deploy） |
| 全部 Normal Provider exhausted 后才 Admission | PASS（ALL_NORMAL_PROVIDERS_EXHAUSTED → sqd_cloud Tier 100） |
| single deploy lock / 不重复 deploy | PASS |
| Cloud Worker 成功部署 | PASS（supreme/bsc-emergency-worker@v2） |
| pending → leased → completed | PASS（R2 对象齐全） |
| 真实 BSC Portal 数据 | PASS（1658 + 58 行 USDT Transfer） |
| Parquet 写入 R2 | PASS（52637 / 4513 bytes） |
| manifest / SHA256 / _SUCCESS | PASS（schema_version=1，最后写 _SUCCESS） |
| Local Sync | PASS（.partial + SHA256 + DuckDB 校验） |
| Dataset Registry | PASS（2151 rows / 5 entries / 3 files / 77649 bytes） |
| Coverage MISS → HIT | PASS（0x2910… 由 MISS 转 HIT；3 地址组 tx_count=6632） |
| Provider 恢复后新 Chunk 回普通 Provider | PASS（故障注入关闭后路由到 sqd，Cloud IDLE） |
| Idle 自动 Remove | PASS（2 分钟空闲 → REMOVING → ABSENT，sqd list 为空） |
| 无 Secret 泄漏 | PASS |

### 关键证据

```text
Plan 831013b2-122e-4731-a8bb-ee760be93d5f（3 addr，修复前代码）
  status=READY_FOR_GRAPH  rows=1658  latency=17494ms
  manifest: schema_version=1, to_block=114474500, sha256=0c4b70…
  实际 parquet max_block=114475243（批次越界，修复前）

Plan 31aaa3b2-fd71-4931-8536-0bf01c379301（1 addr，修复后代码）
  status=READY_FOR_GRAPH  rows=58  latency=17842ms
  manifest: to_block=114474500, sha256=afd17c80…
  实际 parquet min=114474000 max=114474499（严格截止）

merged.parquet（修复全量合并后）：2151 rows / 唯一键 2151 / 87718 bytes
Registry：2151 rows / 5 entries / 3 files / 77649 bytes
```

### 本次修复（部署优化）

1. `internal/cloudruntime`：lease.json 计入 Idle Reaper 计数；remoteJobStatus 将仅有 lease 的远端 Job 视为 running；Job JSON 双写 id/job_id 并补 chain_id/dataset。
2. `Processor-only`：job-poller 字段归一化；main.ts 增加 30s 心跳/续租；TO_BLOCK 改为 head-follow + 过滤，避免批次越界与“流结束即退出导致无法上传”。
3. `internal/datasetsync`：SyncAll 全量重建 merged.parquet（多 chunk 不覆盖）；Merge 按唯一键去重并原子替换。

### 边界与后续

- 修复前 chunk 831013b2 的越界数据保留在 Registry/merged 中作为证据；Phase 5.2 起所有 chunk 均严格截止。
- 本地 SQD 普通 Provider 未透传显式 from/to（恢复测试观察项，已取消），不影响 Cloud 路径。
- 云端 Worker Lease 过期恢复 / cancel marker 优雅退出为 Phase 5.2 验证项。
