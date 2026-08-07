# SQD Cloud Phase 5.3：Investigation + Graph Cloud-Aware 自动联动与增量恢复报告

> 验收日期：2026-08-07  
> 基线：Phase 5.2 PASS  
> 验收结论：**PASS（含记录边界）**  
> 成功标准：Investigation / Graph 不再关心数据来自哪个 Provider；数据进入 Registry 后调查与图谱自动恢复。

## 1. 本阶段实现

### 1.1 Dataset Event Bus（Phase 5.3 §5/§14）

- 新增 `internal/datasetevents`：持久化事件总线（`dataset_events.json` + `processed.json`）。
- 事件：`DATA_REQUIREMENT_CREATED / DOWNLOAD_PLAN_CREATED / DOWNLOAD_STARTED / REMOTE_COMPLETED / LOCAL_SYNC_STARTED / DATASET_VALIDATED / DATASET_INDEXED / COVERAGE_UPDATED / INVESTIGATION_RESUMED / GRAPH_INCREMENT_APPLIED / INVESTIGATION_CANCELLED`。
- `DATASET_INDEXED` 事件 ID 确定性（`idx:<chunk_key>`），消费者幂等（`processed_events` 持久化，重启不重放副作用）。
- API：`GET /api/dataset/events`。

### 1.2 Investigation 自动恢复（§3/§6）

- 事件消费者按地址匹配 WAITING/CREATED 调查，调用 `InvestigationAgent.NotifyDataReady`（安全 Resume，仅允许 CREATED/WAITING，避免与主循环双队列并发）。
- 恢复后发布 `INVESTIGATION_RESUMED`；缺数据不直接失败（保持 WAITING）。
- 验证：`idx:*` 事件触发后，现有调查恢复路径被事件总线驱动（行为复用 Phase 5 已验证的 Resume）。

### 1.3 Graph 增量更新（§7/§18）

- 新增 `internal/graphincrement`：`DATASET_INDEXED → 只读新 registry 条目的本地 parquet → graph_edges/graph_nodes 唯一键去重合并 → 更新节点统计 → GRAPH_READY`。
- Node Key：`chain_id + address`；Edge Key：`chain_id + block_number + transaction_hash + log_index + from + to + token`。
- 重复事件/重复分片不产生重复边（`INSERT OR IGNORE` + 已应用 chunk 跳过）。
- API：`GET /api/graph/status`（GRAPH_READY / last_chunks / node_count / edge_count）。
- 实测：重启恢复后 graph `edge_count=484,338`（与 Registry 行数一致），V2 增量后 `484,348`（+10），无重复。

### 1.4 后端重启恢复（§15）

- 启动后：扫描 ACTIVE Registry → 发布确定性 `DATASET_INDEXED` → `Bus.Replay` 幂等重放。
- 8010 与生产 8000 均验证：重启后事件总线补发 `DATASET_INDEXED`，Graph 增量器自动补齐，已处理事件不重复副作用。

### 1.5 Cancel 新二进制真实重放（§9）

- 任务：`d24f2da5-7b54-41b2-98b6-174c9d6b3c23-1-c1`（1 地址 × 50,000 blocks）。
- RUNNING 时 API Cancel 后计划终态：`FAILED`，错误 `Parquet 任务 … 结束于 canceled`（新二进制 `JobProgress` 支持 cancelled）。
- R2：cancel marker 存在、cancelled 终态存在、completed `_SUCCESS` 404、checkpoint `cancelled:true`、lease/status 已删除。
- 事件总线：取消任务 0 个 `DATASET_INDEXED` / `GRAPH_INCREMENT_APPLIED`，partial 未导入。

### 1.6 Manifest V2 + 多分片 Sync（§10-§13）

- TS Worker：`schema_version=2` manifest（`parts[]`：path/bytes/sha256），checkpoint V2 字段（`rows_committed`、`parts`）；心跳增量上传已 flush 分片；resume 时下载已上传分片并以 `rows_committed` 累计。
- Go Sync：支持 `parts[]` 下载（只下缺失分片、逐片 SHA256）；Validator `PartRows` 校验 `sum(parts.rows)==row_count`。
- 真实验证：计划 `1cfb0a54-924f-4abc-996e-97911416521f`（1 地址 × 5,000 blocks）→ manifest `schema_version=2`、`parts` 1 个、`row_count=10`、`min/max=114450000/114454999`；Local Sync sum 校验通过；DATASET_INDEXED + GRAPH_INCREMENT_APPLIED 自动触发。

### 1.7 Coverage / Provider 语义

- FULL 直接复用本地；MISS/PARTIAL 进入 Orchestrator；Cloud 只补缺口（沿用 Phase 5.2 准入，Tier 100 不变）。
- Investigation / Graph 仍通过 `/api/scheduler/plan|expand` 统一提交，不直接调用 SQD/RPC/Cloud（既有架构约束保持）。

## 2. 验证

- `go test ./... -short -count=1`：全绿（新增 datasetevents、graphincrement 测试）。
- `go vet ./...`：零告警。
- `go build ./...` / `npm run build`：通过。
- 生产 8000 已更新为新后端（local 模式，health ok，events=12，graph GRAPH_READY 484,348 edges）。
- 验收实例 8010 已停止；Cloud Worker 按 Idle Remove 移除，`sqd list` 为空。

## 3. 记录边界

1. Multipart 增量上传以心跳（30s）为粒度，未实现 25,000 行 / 64MB / 5,000 块阈值触发；V2 崩溃精确 resume 的代码已就绪（下载已上传 parts + rows_committed），但本次未用 V2 Worker 重放真实 crash（Phase 5.2 已用 V1 验证 crash resume 语义）。
2. Investigation 状态命名沿用 CREATED/WAITING（行为等价 WAITING_DATA/DATA_READY），未新增 UI 文案层。
3. Objective 驱动数据需求（§17）未实现，沿用现有 Requirement 字段（dataset/from/to/addresses/direction/tokens）。
4. 事件/processed 文件由 8010/8000 共享写入，单节点部署无锁竞争；多节点部署需迁移到 DB 或加文件锁。
5. DPAPI + AES-GCM Secret Store 未实施（执行单建议项，不阻塞 Cloud Runtime）。

## 4. 关键文件

- `internal/datasetevents/events.go` + `events_test.go`
- `internal/graphincrement/graph.go` + `graph_test.go`
- `internal/api/handlers.go`（事件总线装配、重启恢复、`/api/dataset/events`、`/api/graph/status`）
- `internal/datasetsync/{sync,validator}.go`（Manifest V2 parts、PartRows 校验）
- `E:\Code\Processor-only\src\{main,upload}.ts`（Manifest V2、checkpoint V2、增量分片上传、resume 下载）
