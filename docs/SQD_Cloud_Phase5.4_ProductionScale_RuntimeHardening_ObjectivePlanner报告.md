# SQD Cloud Phase 5.4：Production Scale + Runtime Hardening + Objective Planner 报告

> 验收日期：2026-08-07  
> 基线：Phase 5.3 PASS  
> 验收结论：**部分 PASS（Runtime Hardening / Objective Planner 通过；P0 Crash 精确 Resume 与 1K-100K 规模档未完成真实验证）**

## 1. Runtime Hardening

### 1.1 Multipart 正式阈值（PASS，含真实验证）

- 阈值 flush：`5,000 blocks` 或 `25,000 rows`（任一满足即 `setForceFlush`），配合心跳增量上传。
- Part 命名固定：`token_transfers/part-000001.parquet …`（immutable，不覆盖已 committed part）。
- Checkpoint V2：`last_completed_block / rows_committed / parts[] / next_part_no`。
- 真实验证：
  - `9aa6aab0…`（25 地址 × 10k）：258 行、2 parts、registry `dup=0`、range 通过。
  - `7d0c4d17…`（25 地址 × 10k，修复后）：2633 行、2 parts、**parts SHA 唯一（无重复）**、registry `dup=0`、`sum(parts.rows)==row_count` 通过。

### 1.2 发现并修复 Part 重复上传 Bug

- 现象：`9cbd19e6…`（25 地址 × 50k）完成时 manifest 出现 18 个 parts，其中多个 part SHA 与 part-1/2 相同 → `LOCAL_VALIDATION_FAILED`（sum 15782 vs row_count 6678，dup 9104）。
- 根因：`uploadOutputToR2` 合并 checkpoint 已上传 parts 后，又把本地同名内容按新编号重复上传。
- 修复：按 `sha256` 跳过已上传分片（uploadNewParts 与 uploadOutputToR2 双侧），`7d0c4d17` 验证无重复。
- 审计：`9cbd19e6` 保留为 FAILED/LOCAL_VALIDATION_FAILED 证据条目，不参与 merged/coverage。

### 1.3 Lease Reaper 硬化（PASS）

- 修复 TS `toISOString()` 毫秒时间（RFC3339Nano）被 Go `time.RFC3339` 解析失败的 bug（真实环境 Lease 过期未回收）。
- Reaper 增加 tick/item/job 诊断日志与 panic recover。
- 真实验证：`9aa6aab0` 的 lease 过期后自动 requeue（`requeue.json` 存在）并由新 Worker 重新领取。

### 1.4 Cancelled 独立终态（PASS）

- 新增 `CANCELLED` / `CANCEL_REQUESTED` 计划状态；`errTaskCancelled` 哨兵；waitSQDJob `StatusCanceled → cancelled`；execute/CloudFallback 不再把用户取消计为失败。
- 单测覆盖 cancelled 分支；Phase 5.3 真实 Cancel 重放已通过（计划终态 canceled、R2 全套证据）。

### 1.5 Event Bus 单节点硬化（PASS）

- 跨进程文件锁（O_CREATE|O_EXCL + 过期清理）、临时文件 + fsync + rename、事件 `seq` 序列、启动损坏扫描（坏文件隔离为 `.corrupt-*`）。
- 单测：`TestBusIdempotentConsumers` 覆盖幂等与重启重放。

### 1.6 Secret Store

- API 仍只返回 `configured: true`（`/cloud/runtime`、`/cloud/usage`），不返回真实值。
- **DPAPI + AES-GCM 迁移未实施**（建议项，不阻塞运行）：当前仍为 Windows User Environment + 组织 secrets。

## 2. Objective-Driven Planner（PASS，含单测）

- 新增 `internal/objectiveplanner`：
  - Objective Contract：`fund_sink / exchange_offramp / profit / token_profit / source_trace / destination_trace / identity_resolution` + constraints（depth/max_addresses/min_amount_usdt）。
  - Objective → Dataset Matrix（目标决定数据；balance/labels 默认不 Cloud eligible）。
  - Cost Guard：估算 address_count/block_span/dataset_count/chunks/local GB；超 cap 拒绝。
  - 扩展：`Scheduler.Submit` 对带 `objective_type` 的 Requirement 自动展开为多个数据集需求（不指定 Provider）。
- API：`POST /api/scheduler/plan` 支持 `objective_type/objective_description/objective_constraints`。
- 单测：`TestFundSinkMatrixAndCostGuard`、`TestUnknownObjective`、`TestSubmitObjectiveExpansion`（fund_sink → token_transfer+transactions+balance）。

## 3. 自动化验证

- `go test ./... -short -count=1`：全绿。
- `go vet ./...`：零告警。
- `go build ./...` / `npm run build`：通过。
- 生产 8000 已更新为新后端（local 模式，health ok）。

## 4. 未完成 / 边界（如实记录）

1. **P0 真实中途 Crash Resume（带 parts）未成功演示**：两次受控重启均在任务完成/重启落地前结束，未能在 part 上传后中断；V2 resume 代码（下载已提交 parts + rows_offset 累计）已就绪，但缺少一次真实“crash after part-000001 → resume → 精确累计”的通过记录。此为本阶段最重要缺口。
2. **规模档未执行**：Stage A 1K / B 10K / C 50K / D 100K 地址未跑（需多小时预算与资源评估）。当前只完成 25 地址 × 10k/50k 的 chunk 级冒烟。
3. Investigation UI 状态语义：行为等价（CREATED/WAITING → NotifyDataReady → 恢复），但未新增 `WAITING_DATA/DATA_READY` UI 文案。
4. Coverage Index（address_dataset_coverage）未单独建表；当前 Registry `AddressTxCount` 逐 entry 求和，规模增大后需索引化。
5. Graph Viewport 性能门槛未改（沿用现有 600 边渲染限制）；Graph 存储层已增量物化。
6. 可观测性：`fallback_ratio`、usage 可查；Prometheus/事件延迟等指标未接入。
7. 成本：planner 有估算；真实 cost_per_1m_rows 等未形成。
8. `tmp_pick` 临时目录残留（策略阻止删除）。

## 5. 关键文件

- `internal/objectiveplanner/{planner,planner_test}.go`
- `internal/downloadscheduler/{types,scheduler}.go`（CANCELLED 终态、Objective 展开、测试）
- `internal/datasetevents/events.go`（锁/fsync/seq/损坏扫描）
- `internal/cloudruntime/manager.go`（RFC3339Nano lease 解析、reaper 诊断/recover）
- `internal/api/download_scheduler_handlers.go`（objective 契约）
- `E:\Code\Processor-only\src\{main,upload}.ts`（阈值 flush、part 命名、sha 去重、checkpoint V2）
