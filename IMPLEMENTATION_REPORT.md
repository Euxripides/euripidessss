# Smart Download Orchestrator V3.0 — SQD Cloud 最终兜底 Provider 接入实施报告

> 对应设计文档：`D:\下载文件\Smart_Download_Orchestrator_V3_SQD_Cloud最终兜底Provider接入设计.md`（V3.0，2026-08-07）
> 实施日期：2026-08-07

## 1. 结论

已完成 **SQD_CLOUD_EXPORT 作为最后一级兜底 Provider** 的本地编排端接入并部署：

- Provider Tier 分层（EmergencyCloud=100）与 ProviderState 状态模型；
- 错误分类 + 熔断健康跟踪（429/403/5xx/超时/风控）；
- Cloud Admission Gate（覆盖缺口 / 数据集支持 / 常规 Provider 全部耗尽 / 预算 / 运行时）；
- Cloud Runtime Manager（单实例锁、串行 Job 队列、空闲 20 分钟回收、local/cloud/mock 模式）；
- 调度器常规 Provider 耗尽后自动走 Cloud，Provider Attempts 与 Cloud 用量全程审计；
- API `/api/scheduler/providers/health`、`/api/scheduler/cloud/runtime` 与前端状态展示；
- Cloud Worker 支持 TO_BLOCK 有界执行、地址过滤、`_SUCCESS`/manifest 产物契约；
- 单元测试、故障注入测试全部通过；真实 API 链路验证到 Worker 启动；外部 Portal 网络阻断如实记录。

## 2. Provider 优先级

```text
P0   LOCAL_DATASET（覆盖命中直接返回，不调用 Provider）
P1   BROWSER_DOWNLOAD / SQD_PUBLIC / RPC / AWS（按数据集评分排序，Tier=10）
P9   SQD_CLOUD_EXPORT（Tier=100，仅 Admission Gate 批准后进入）
```

排序规则：**Tier 永远优先 → Capability → Health → Cost → Throughput**。Cloud 不参与常规竞速（`Registry.Candidates` 默认排除 Tier≥100）。

## 3. 实现清单

| 设计章节 | 实现 | 状态 |
| --- | --- | --- |
| §16 Provider Tier | `internal/downloadscheduler/tier.go`；`Provider.Tier()` | ✅ |
| §6/§10/§11 ProviderState + 熔断 | `provider_state.go`（分类器 + 跟踪器） | ✅ |
| §15/§80 Cloud Admission Gate | `admission.go`（8 条件 + 拒绝原因） | ✅ |
| §27-31 Cloud Runtime Manager | `internal/cloudruntime/`（状态机/锁/队列/回收/模式） | ✅ |
| §33-34 Job 产物契约 | `request.json/status.json/manifest.json/_SUCCESS + Parquet` | ✅ |
| §21 Provider Attempts | `PlanTask.Attempts` + `provider_attempts` 审计 | ✅ |
| §98-99 用量审计 | `cloud_usage.json`（每日 60 分钟默认上限） | ✅ |
| §63 API | `/api/scheduler/providers/health`、`/api/scheduler/cloud/runtime` | ✅ |
| §62 前端 | SmartFillPanel Provider/Tier/Cloud 状态 | ✅ |
| §95-96 故障注入 | `SCHEDULER_FAULT_INJECTION=all_normal_providers_fail` | ✅ |
| §70-74 Worker Job 化 | TO_BLOCK/地址过滤/产物契约（Env 参数驱动） | 🟡 Phase 4 部分 |
| §35-38 R2/S3 输出 | 未实施（当前本地 Job 输出目录） | ⛔ Phase 5 |
| §41-44 DuckDB 同标准入库 | 未实施（输出由 manifest/_SUCCESS 校验，未进 DuckDB） | ⛔ Phase 6/7 |

## 4. Cloud Admission Gate 规则

```go
if !missingCoverage            → Reject(LOCAL_COVERAGE_FULL)
if !datasetSupported           → Reject(CLOUD_UNSUPPORTED_DATASET)   // V1 仅 token_transfer
if !normalProvidersExhausted   → Reject(NORMAL_PROVIDER_AVAILABLE)   // 单次 503 不算耗尽
if !cloudEligible              → Reject(CLOUD_NOT_ELIGIBLE)
if !budgetAllowed              → Reject(CLOUD_DISABLED / CLOUD_BUDGET_EXCEEDED)
if !runtimeAvailable           → Reject(CLOUD_RUNTIME_UNAVAILABLE)
else                           → Allow()
```

## 5. 运行时模式

- `SQD_CLOUD_MODE=auto`（默认）：有 `SQD_DEPLOY_KEY` → `cloud`；`E:\Code\Processor-only\lib\main.js` 存在 → `local`；否则禁用。
- `local`：本机 Processor（Portal 数据源），按 Job 启动 `node lib/main.js`，有界执行（TO_BLOCK）后写产物退出。**用于开发/验证与无密钥环境。**
- `cloud`：`sqd deploy` 部署 Squid Cloud Worker。V1 需要 `SQD_DEPLOY_KEY`（仅环境变量）与 Worker Job 化（Phase 4）完成后才能参数驱动；未满足时明确拒绝。
- `mock`：测试/故障注入。

Deployment Key 约束：只允许环境变量，不落盘、不入日志、不入前端/Manifest/R2、不提交 Git。

## 6. 配置

| 环境变量 | 默认 | 说明 |
| --- | --- | --- |
| `SQD_CLOUD_MODE` | `auto` | local/cloud/mock/none |
| `SQD_CLOUD_WORKER_DIR` | `E:\Code\Processor-only` | Cloud Worker 项目 |
| `SQD_CLOUD_DATA_ROOT` | `E:\codex\bsc_analytics\sqd-cloud` | Job/审计数据根目录（拒绝 C:） |
| `SQD_DEPLOY_KEY` | 空 | Squid Cloud 部署密钥（cloud 模式） |
| `SCHEDULER_FAULT_INJECTION` | 空 | `all_normal_providers_fail`（仅测试） |

## 7. 测试与验证

### 单元/集成测试

- `internal/downloadscheduler/provider_state_test.go`：错误分类、熔断阈值、冷却恢复、常规 Provider 可用性判定。
- `internal/downloadscheduler/admission_test.go`：设计 §93 关键场景（本地覆盖全命中、常规可用、全耗尽允许、预算超限、不可用运行时、非关键任务、不支持数据集）。
- `internal/downloadscheduler/scheduler_cloud_test.go`：常规 Provider 全部失败 → Cloud 兜底成功；预算关闭 → 拒绝；常规 Provider 健康 → Cloud 调用次数 0。
- `internal/cloudruntime/manager_test.go`：Job 生命周期、单 Worker 串行、空闲回收、Deploy 锁、未配置拒绝。
- `go test ./internal/... -short -count=1`：**41 包全部 ok**；`go vet ./...` 零告警；`go build`、`npm run build` 通过。

### 真实 API 链路验证（2026-08-07）

临时实例（端口 8010，`SCHEDULER_FAULT_INJECTION=all_normal_providers_fail`、`SQD_CLOUD_MODE=local`）：

1. `GET /api/scheduler/providers/health`：5 个 Provider（sqd_cloud Tier 100、ABSENT 待命、available=true）；
2. `GET /api/scheduler/cloud/runtime`：ABSENT/local、预算 enabled 60min、当日用量 0；
3. token_transfer 计划（地址 `0xf977814e90da44bfa03b6295a0616a897441acec`，无本地覆盖，显式区块 114000000-114050000）→ 常规 Provider 故障注入 → **Admission 批准（ALL_NORMAL_PROVIDERS_EXHAUSTED）**；
4. Cloud Provider 提交 Job（request.json/status.json 落盘）→ 本机 Processor 启动；
5. 外部 `portal.sqd.dev` 在当前环境 DNS 解析失败 + TLS 握手被本机网络层阻断（`worker.log: TypeError: fetch failed`）→ 任务如实失败；
6. 计划保留完整证据：`Attempts=[sqd_cloud/DEGRADED/2003ms]`、Cloud Decision、Job 日志路径。

**边界声明**：本次未完成真实 Portal 数据下载（外部网络阻断，非代码缺陷）。此前 2026-07-30/08-03 的 SQD 冒烟数据（`E:\Code\Processor-only\data\`，BSC 全历史 USDT Transfer parquet）证明 Worker+Portal 管线本身可用。

## 8. 未完成事项（Phase 4/5/6/7）

1. **Worker Job 化轮询**：request.json 动态任务 + Lease + 多 Job 复用同一 Warm Worker（当前 local 模式每 Job 一个进程，cloud 模式需密钥后验证）。
2. **R2/S3 输出**：Parquet/Manifest/Checkpoint 上传对象存储，删除 Worker 后数据仍可恢复。
3. **DuckDB 同标准入库**：Cloud 数据与 SQD/AWS/RPC 统一 Dataset Registry，Graph/Investigation 无感知。
4. **SLA/ETA 预测**（§79）：V2 启用 `DEADLINE_UNSATISFIABLE`。
5. **真实 Squid Cloud 部署验证**：需用户提供 `SQD_DEPLOY_KEY`（环境变量方式）与网络环境恢复。

## 9. 维护注意事项

- `Status.Available`：ABSENT = 已配置可按需部署；FAILED/NOT_CONFIGURED/冷却 = 不可用。
- 故障注入仅通过环境变量开启，生产禁止设置 `SCHEDULER_FAULT_INJECTION`。
- Cloud 任务失败不会静默重试（失败冷却 15 分钟），避免重复部署轰击。
- 不要手工修改 `E:\codex\bsc_analytics\sqd-cloud\` 下的 runtime_state.json/job 文件；由 Manager 原子管理。
