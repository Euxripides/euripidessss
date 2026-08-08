# 智能下载统一入口与自适应调度系统 V1.0 — 完整差距分析

> 日期：2026-08-08
> 输入规格：`D:\下载文件\智能下载统一入口与自适应调度系统_V1.0.md`
> 分析对象：`E:\codex\etl` 当前实现（Go 后端 + React 前端）
> 性质：只读差距分析，未修改任何功能代码

## 1. 结论摘要

当前项目已有 **Smart Download Orchestrator V2.2 的骨架**：Provider 评分路由、覆盖检查、计划状态机、熔断/健康跟踪、SQD Cloud 最后兜底、Parquet/DuckDB 数据面、Cloud Dataset Registry。但距离规格 V1.0 的“智能下载统一入口”还有 **9 项结构性差距**，且 P0 验收（Case A–E）当前全部无法满足。

核心结论：

1. **任务模型不对**：现网只有 `Plan → PlanTask(dataset+chain+地址组)`，没有 `BatchJob → AddressJob → DatasetJob → Range` 四层模型，无法做到“每个地址独立任务/进度/暂停/继续”。
2. **Checkpoint 不是 Provider 无关的**：现有 Checkpoint V2 是 job 级（SQD chunk 或文件分片），服务重启后调度器把未完成计划直接标记 `FAILED`，不续跑；切换 Provider 也没有 `completed_ranges` 跳过机制。
3. **没有 Range Ledger**：Cloud 侧有 chunk 级 Registry，但不是按 `address × dataset × range` 记账、可补洞、可切换 Provider 的账本。
4. **没有 Discovery/Probe 阶段**：创建任务时不估算数据量/体积/成本/耗时，只有活跃度分桶（20/100/500 地址）。
5. **没有地址级暂停/恢复/取消 API**：`downloadengine` 的 Job API 是内存 stub 且未接线（页面孤立）；`parquetdownload` 只有 Cancel/Retry。
6. **没有 SSE/WebSocket**：前端 2s 轮询，无法支撑 10 万地址任务中心的实时性要求。
7. **没有统一前端**：菜单无“智能下载/下载任务/结果数据”；`DownloadCenterPage.tsx` 已存在但未挂载，且后端未注册 `/api/download-engine/jobs`（当前访问会 404）。
8. **地址上传无列自动识别**：现有实现把 CSV/XLSX 所有单元格拼在一起抽地址，不做列扫描/命中率/候选列。
9. **Validation 只有 L1–L2（部分 L3/L4）**：缺 L5 缺口补洞、L6 抽样交叉验证、Validation Score。

---

## 2. 现有实现地图（证据）

### 2.1 后端

| 模块 | 文件 | 现状 |
|---|---|---|
| 智能调度器 | `internal/downloadscheduler/{types,scheduler,provider,coverage,health,admission,cloud_provider,aws_provider,provider_state,activity,rpc_logs,tier}.go` | V2.2：评分路由 + 计划状态机 + 重试/切换 + Cloud 兜底 |
| Parquet 下载 | `internal/parquetdownload/{manager,process,download,sqd_ingest,sqd_checkpoint,task_finalizer,recovery,addresses}.go` | 流式下载、分片 checkpoint、manifest、恢复合并、16 阶段任务 |
| Cloud 数据面 | `internal/datasetsync/{registry,sync,validator}.go` | Cloud chunk 登记、SHA256 同步、DuckDB 校验、merged.parquet |
| SQD Cloud 运行时 | `internal/cloudruntime/{manager,types,queue}.go` | Worker 状态机、Job 队列、Lease、Cancel Marker、事件总线 |
| SQD 可靠性 | `internal/datasource/sqd/{client,adaptive_workers,circuit_breaker,metrics}.go` | 熔断、冷却、自适应并发、健康快照 |
| RPC | `internal/downloadscheduler/rpc_logs.go` + `internal/rpcmanager` | 余额 + eth_getLogs Token Transfer 恢复通道 |
| 链配置 | `internal/chain/evm.go` | BSC/Ethereum/Base/Arbitrum 四链 |
| 调度 API | `internal/api/download_scheduler_handlers.go` | `/api/scheduler/{coverage,plan,run,expand,status,plans,budget,providers/health,cloud/*,metrics}` |
| 路由 | `internal/api/handlers.go` `RegisterRoutes` | `/api/scheduler/*`、`/api/crypto/parquet/*` 等已注册；**`/api/download-engine/jobs` 未注册** |
| V2 下载引擎 | `internal/downloadengine/{job,job_api,types}.go` | Job/Chunk/Discovery/范围状态机定义完整，但 `JobAPIHandler` 为内存 stub，**未接入生产路由与执行器** |
| 目标规划 | `internal/objectiveplanner` | Objective 驱动数据集展开 |

### 2.2 前端

| 页面/组件 | 位置 | 现状 |
|---|---|---|
| 菜单 | `frontend/src/App.tsx` | “数据资产”下只有：数据集管理、Parquet 数据、浏览器下载、Dune 下载、数据源管理；**无“智能下载/下载任务/结果数据”** |
| 智能补充面板 | `frontend/src/features/analytics/SmartFillPanel.tsx` | 单地址、4 数据集、覆盖检查、2s 轮询、Provider/Cloud 状态展示 |
| 调度 API 封装 | `frontend/src/features/analytics/schedulerApi.ts` | coverage/plan/run/expand/status/plans/budget/cloud 全套 |
| Parquet 下载 | `frontend/src/features/crypto/CryptoParquetPanel.tsx` | 16 阶段网格主视图（规格 §53 点名不建议的模式） |
| 浏览器下载 | `frontend/src/features/crypto/CryptoDownloadPanel.tsx` | CSV/浏览器采集 |
| 下载中心 | `frontend/src/features/crypto/DownloadCenterPage.tsx` | 调用 `/api/download-engine/jobs`；**未挂载到菜单/路由** |
| Dune | `frontend/src/features/download/Dune*` | 独立高级入口 |

---

## 3. 逐条差距矩阵（规格章节 → 现状）

图例：✅ 已具备 / 🔶 部分具备（有替代或降级实现）/ ❌ 缺失

| 规格章节 | 状态 | 现有实现 | 差距 / 所需改动 |
|---|---|---|---|
| §1.1 单一主入口 | ❌ | 无智能下载页；Parquet/浏览器/Dune 并列在菜单 | 新增“智能下载”入口，旧入口降级为高级工具 |
| §1.2 地址一级任务 | ❌ | `PlanTask` 是 dataset+chain+地址组；地址只是 `[]string` | 新增 `BatchJob/AddressJob/DatasetJob/Range` 四层模型 |
| §1.3 下载方式不是任务身份 | 🔶 | Provider 切换不重建 Task | 切换仍重跑整个 task；需 Range 级 `completed_ranges` 跳过 |
| §1.4 先探测后下载 | ❌ | `Submit` 直接 ANALYZING→SELECTING→BUILDING；`Preview` 只查 SQD 元数据/区块范围 | 新增 DISCOVERY 阶段：每条 `Address×Dataset` 估算行数/体积/成本/耗时 |
| §1.5 完整性优先 | 🔶 | `datasetsync` 有 schema/唯一键/行数/范围校验；Cloud `_SUCCESS` 门控 | 缺 L3 覆盖等式、L4 Provider Count 对账、L5 补洞、L6 抽样交叉验证 |
| §1.6 Canonical Schema | 🔶 | transactions/logs/traces 统一模型；token_transfer 14 列 | 缺 `source_provider/source_task_id/source_range_id/ingested_at` 等溯源列；无统一 schema 注册表 |
| §2 导航结构 | ❌ | 无 | 新增智能下载/下载任务/结果数据/调度器状态 |
| §3 首页布局 | ❌ | 无 | 三页签 + 顶部全局状态（运行任务数/速度/错误数/磁盘） |
| §4.1-4.3 地址输入 | 🔶 | 文本批量 ✅；文件上传走 `parquetdownload/addresses.go`，全单元格拼接抽地址 | 需列扫描、命中率、候选列、原始行/有效/重复/无效/最终统计 |
| §5 链选择 | ✅ | `chain.Resolve` 四链；前端有链选择 | 无需改动（调度器仍应由系统选 Provider） |
| §6 数据类型 | ❌ | 调度器仅 4 类（balance/transactions/token_transfer/labels）；parquetdownload 内部支持 logs/traces/receipts/metadata/nft/balance/summary | 扩展 Dataset 枚举：internal_transactions、logs、receipts、token metadata、historical balance、address type 等 |
| §7 时间范围 | 🔶 | `StartDate/EndDate/FromBlock/ToBlock` 整批生效 | 需批量默认全量 + 单地址覆盖 + 大地址单独范围 |
| §8 智能预估 | ❌ | 无 | 新增 Discovery/Estimator（First/Last Seen、预估条数、体积、耗时、成本） |
| §9 任务层级 | ❌ | Plan→PlanTask | 需 Batch/Address/Dataset/Range 四层 |
| §10 Provider Adapter | 🔶 | `Provider{Kind,Name,Tier,CanHandle,Available,ManualOnly,Score,Execute}` | 需 `Probe/Plan/Start/Pause/Resume/Cancel/Validate/Capabilities/Health` |
| §11 Provider 评分 | 🔶 | Tier 优先 + coverage25/accuracy25/speed15/cost15/reliability20 + 健康惩罚 | 规格建议含 Availability/Stability/ResumeCapability/History 与 Failure/RateLimit 惩罚；P1 再学习历史成功率 |
| §12 规模分级 | ❌ | 仅活跃度分桶（500/100/20） | 需 S/M/L/XL 分级、阈值与对应路由 |
| §13 SQD Cloud 最后使用 | ✅ | `CloudAdmissionGate`：覆盖缺口/数据集/常规耗尽/eligible/预算/运行时全条件；Tier 100 | 保持；V1 仅 token_transfer 是明确边界 |
| §14 并行策略 | ❌ | `MaxConcurrentPlans=1`；`parquetdownload` 单任务互斥；Cloud 串行队列 | 需不同 Dataset 并行 + 同 Dataset 分区并行（Range 独立 checkpoint） |
| §15-16 切换兼容/Canonical | 🔶 | RPC 恢复与 SQD 同 schema 合并 ✅；但切换后 SQD 任务重新 Start | 需 Provider Normalizer + Staging + Merge 的显式管道；checkpoint 按唯一键续跑 |
| §17 Universal Checkpoint V3 | ❌ | `SQDCheckpointStore` 存在（job 级 pending/completed chunks），但 `sqd_ingest.go` 主路径用内存 `lastHandled` 续拉；Cloud job 有独立 checkpoint；无 provider 无关位置 | 需 `completed_ranges + active_range + parts(sha/rows)` + 独立 `provider_state` |
| §18 Range Ledger | ❌ | `datasetsync.Registry` 是 chunk 级登记（from/to/rows/files/status），无 attempts/provider 历史 | 需独立 `download_range_ledger`（range_id/task/address/dataset/from/to/status/provider/attempt/rows/sha/validation） |
| §19 Provider 切换 | 🔶 | `executeTaskWithFallback`：重试→切换候选→Cloud；错误分类完整 | 切换不是 Range 级；需“从未完成 Range 继续”，并展示切换原因/冷却 |
| §20 状态机 | 🔶 | Plan 状态机完整（含 CANCELLED） | 需 Batch/Address/Dataset 三级状态机 |
| §21 暂停/恢复/取消 | ❌ | parquetdownload 只有 Cancel/Retry；重启自动恢复单个 paused job；Cloud Cancel Marker ✅；downloadengine 的 pause/resume 是未接线 stub | 需地址/Dataset/Batch 级 pause/resume/cancel + 保留/删除临时数据选项 |
| §22 任务中心 | ❌ | 仅 SmartFillPanel（单地址单计划） | 需地址级表格：状态/进度/当前 Dataset/Provider/速度/已下载/ETA/操作 |
| §23 地址详情抽屉 | ❌ | 无 | 需 Dataset 进度、Provider History、Validation 状态 |
| §24 进度单位 | ❌ | 调度器只给百分比；parquetdownload 有 bytes/speed/ETA | 需 Provider 原生单位（pages/rows/bytes/blocks/ranges/req/s、Cloud partitions） |
| §25 ETA | 🔶 | `process.go`：`ETA = remaining/平均速度`（非 EWMA） | 需 EWMA + 冷却/重试/切换/校验时间 + 置信度 |
| §26 调度实时信息 | 🔶 | `stage_detail` + `/providers/health` + SmartFillPanel 显示候选原因/降级 | 需在任务详情中显示候选评分与调度决策（不占主界面） |
| §27 Validation Pipeline | ❌ | L1 文件/CRC ✅；L2 schema/唯一键/格式 ✅；L3 只做“边界不越界” | 需 L3 覆盖等式、L4 count 对账、L5 缺口补洞、L6 抽样交叉验证 |
| §28 Validation Score | ❌ | 只有 PASS/FAIL | 需完整性/唯一性/区间覆盖/Provider 对账/交叉验证分数 |
| §29 下载后自动处理 | 🔶 | Cloud 路径已 raw→sync→validate→merge→index；本地 SQD 也写 parquet+manifest | 需统一 `raw/staging/canonical/final/manifest` 目录与最终态语义 |
| §30-31 结果数据页 | ❌ | analytics 页可查仓库数据；无按任务的结果页 | 需结果 Tabs + 摘要 + 查询/导出 |
| §33 地址结果摘要 | 🔶 | 地址画像页有摘要 | 需与下载结果联动（进入画像/生成关系图/加入调查） |
| §34 创建 API | ❌ | `/api/scheduler/plan+run+expand`（需求驱动） | 需 `/api/smart-download/jobs`（batch/address/dataset/range/overrides） |
| §35 地址识别 API | ❌ | `/crypto/parquet/addresses/upload` 仅全表拼串 | 需 `/api/smart-download/import` 列识别 |
| §36 实时状态 | ❌ | 无 WebSocket/SSE；前端 2s 轮询 | 需 SSE + 事件（batch/address/dataset/range/provider.switched/checkpoint/validation/result） |
| §37 任务控制 API | ❌ | 仅 Cloud job cancel | 需 jobs/{id}/addresses/{addr}/datasets/{id} pause/resume/cancel |
| §38 Provider 强制切换 | ❌ | 无 | 详情页高级“重新调度” + 管理员 Force Provider |
| §39-42 数据库模型 | 🔶 | 文件系统 JSON：`plans/{id}.json`、`registry.json`、`jobs/*.json`、`cloud_usage.json` | 项目规则禁止数据库 → 用文件等价物实现 addresses/datasets/ranges/attempts/checkpoints/validations/events |
| §43 Provider Health | ✅ | `ProviderHealthTracker` + `/providers/health` + SQD 健康适配 | 可补 p50/p95/timeout_rate/current_concurrency 展示 |
| §44 错误分类 | ✅ | `ClassifyProviderError` 覆盖 429/403/401/5xx/超时/DNS/网络/能力；中文展示 | 旧页面个别路径仍可能暴露 `context canceled`，需统一错误呈现 |
| §45 Manifest | 🔶 | V2：schema_version/parts(sha,rows)/row_count/`_SUCCESS` | 需 V3：providers_used/completed_ranges/validation score |
| §46 Part 规则 | 🔶 | Cloud part-NNNNNN 单调 + SHA 去重 + 已提交不重写 | 跨 Provider 继续编号、Provider 不得决定命名，需 Range Ledger 落地 |
| §47 调度器恢复 | 🔶 | parquetdownload 重启自动恢复 1 个 paused job；Cloud lease requeue ✅；**scheduler 重启把未完成计划标记 FAILED** | 需 Recovery Scanner 恢复全部 Active Jobs + 重新选 Provider |
| §48 并发控制 | ❌ | 仅 MaxConcurrentPlans=1 + parquetdownload 单任务 | 需 Global/Chain/Provider/Address 四层限流 |
| §49 大批量 UI | ❌ | 无 | 需服务端分页、虚拟列表、SSE 合并、250-500ms 节流 |
| §50 决策例子 | 🔶 | 评分路由方向一致 | 无规模分级导致超大地址不会主动分区/Cloud 兜底链不完整 |
| §51-52 视觉/颜色 | ❌ | SmartFillPanel 有 tag/状态色 | 需新页面设计，颜色+文字 Tag 并用 |
| §53 不建议模式 | 🔶 | CryptoParquetPanel 仍是 16 阶段网格主视图 | 新任务中心不再展示 16 阶段；移到详情执行日志 |
| §54-55 整合边界 | ✅ | 规格描述的就是复用现有下载器；现有结构与之吻合 | 无需推倒重来 |
| §56-57 目录结构 | ❌ | 无 `internal/smartdownload/*` | 建议在 `internal/downloadscheduler` 上扩展或新建分层包 |
| §58 P0 清单 | 🔶 | 约 30% 已具备（链选择/覆盖检查/自动调度/错误切换/Cloud 兜底/Parquet 落盘） | 任务模型/Checkpoint V3/Range Ledger/地址控制/SSE/前端/导入识别/结果页为 P0 缺口 |
| §59-61 P1/P2/数据复用 | 🔶 | Cloud Registry 有地址×数据集覆盖索引；`CoverageResolver` 判断已有数据 | 无 Range 级 diff（如 2024 已有，只补 2025-2026）；无跨任务复用 |
| §62-65 验收 | ❌ | 未执行；现有结构无法通过 Case A–E | 需按 §63-65 重建后做真实容灾测试 |
| §66 用户体验 | ❌ | 用户仍需在多个页面理解 Provider | 未达“只输入地址+链+类型+范围” |
| §67 实施顺序 | ✅ | 规格建议顺序合理 | 直接采用，见第 5 节 |

---

## 4. 九项结构性差距详解

### 4.1 任务模型：Plan/Task → Batch/Address/Dataset/Range

现状：

```text
Plan
└── PlanTask { dataset, chain, addresses[] }   ← 地址只是列表字段
```

目标：

```text
BatchJob
└── AddressJob            ← 独立状态/进度/暂停/继续/取消
    ├── DatasetJob: transactions
    ├── DatasetJob: internal_transactions
    ├── DatasetJob: token_transfers
    └── DatasetJob: logs
        └── Range { from_block, to_block, status, provider, attempt }
```

影响面：`internal/downloadscheduler/types.go`、`scheduler.go`、`internal/api/download_scheduler_handlers.go`、前端 `schedulerApi.ts`、全部 UI。

### 4.2 Universal Checkpoint V3 缺失

现有证据：

- `sqd_checkpoint.go` 的 `SQDCheckpoint{JobID, Chain, Dataset, Start/End/CurrentBlock, CompletedChunks, PendingChunks}` 是 job 级、SQD 专属；
- `sqd_ingest.go` 主路径 `streamChunked` 只用内存 `lastHandled` 续拉，**不落盘 completed_chunks**（服务重启后从分片头重试；好在下游去重）；
- `scheduler.loadPlans()` 将未完成计划直接置为 `FAILED`，注释明确“无断点续跑”；
- Cloud job checkpoint（`cloudruntime`）只覆盖 Cloud 通道。

规格 V3 要求：`completed_ranges[] + active_range + parts[{part,sha256,rows}] + provider_state{}`，切换 Provider 时保留 `completed_ranges`、丢弃 `provider_state`。这是 Case A/B/C/D 能否通过的根因之一。

### 4.3 Range Ledger 缺失

`datasetsync.Registry` 是“已登记 Cloud chunk”索引（含 from/to、rows、sha256、status），不是可调度的账本：没有 `attempt`、没有 provider 切换记录、没有 `validation_status`、没有“PENDING→RUNNING→SWITCHING_PROVIDER→VALID”的 range 状态机。补洞、恢复、跨任务复用都需要它。

### 4.4 Discovery/Probe 缺失

现状 `Preview` 只做：SQD 数据集可探测 + 日期→区块 + FirstSeen。没有：

- First Seen / Last Seen（有 `parquetdownload/firstseen.go` 可复用，未接入创建流程）
- 预计条数/体积/耗时/成本
- `Address × Dataset` 级别的 DISCOVERY 阶段

### 4.5 地址级控制 API 缺失

- `parquetdownload.Manager`：`Cancel(id)` / `Retry(id)`，无 `Pause`；
- `StatusPausing/StatusPaused` 仅作为重启恢复的中间态；
- `downloadengine.JobAPIHandler` 有 pause/resume/cancel 但**未注册路由、未接执行器、页面未挂载**；
- 调度器 `cancel` 字段“保留供将来 Cancel API 使用”，尚无公开入口。

### 4.6 实时通道缺失

全仓无 `text/event-stream` / WebSocket；前端 `SmartFillPanel` 2s `setInterval` 轮询。10 万地址、地址级进度、Provider 切换事件无法靠轮询承载。

### 4.7 统一前端缺失

菜单只有旧入口；`DownloadCenterPage` 是孤页（访问 `/api/download-engine/jobs` 会 404）。规格要求的创建页/任务中心/结果页三页签全部需要新建。

### 4.8 地址上传无列识别

`parseAddressUpload`（`internal/parquetdownload/addresses.go`）：

- 把 CSV 每行所有列、XLSX 所有 sheet 所有单元格拼接后统一 `normalizeAddresses`；
- 无列扫描、无地址命中率、无候选列、无“原始行数/最终任务地址数”分列统计。

规格 §4 的统计模型（`wallet_address 12,411 valid / 318 dup / 107 invalid / 12,093 final`）需要新实现。

### 4.9 Validation Pipeline 只有 L1–L2

`datasetsync.DuckDBValidator` 已覆盖：Schema、行数、唯一键、重复、Min/Max Block、范围越界、地址归属、sum(parts.rows)。缺：

- L3：`requested range == valid completed ranges + confirmed empty ranges`（覆盖等式）；
- L4：Provider 有 total count 时 `expected vs normalized_unique` 对账；
- L5：缺口区间自动补洞（原 Provider 重试→次优 Provider→RPC 精确→SQD Cloud）；
- L6：抽样交叉验证（随机 Block 区间用第二 Provider 比对）。

---

## 5. 建议实施顺序（对齐规格 §67）

> 原则：不重写下载器；在 `internal/downloadscheduler` / `parquetdownload` / `datasetsync` 之上加统一任务模型、统一 Schema、统一 Checkpoint、智能调度、兼容切换与统一前端。项目规则禁止数据库，规格中的表一律用文件系统 JSON 等价物。

### 阶段 1：统一模型与账本（后端地基，不改下载器）

1. Canonical Schema Registry：为 transactions / internal_transactions / token_transfers / logs / balances / metadata 定义唯一键与列，并补齐溯源列（provider、task_id、range_id、ingested_at）。
2. 四层任务模型：`BatchJob → AddressJob → DatasetJob → Range`，文件持久化（`backend/data/smart_download/`）。
3. Universal Checkpoint V3：`completed_ranges + parts(sha/rows) + provider_state`。
4. Range Ledger：range_id/task/address/dataset/from/to/status/provider/attempt/rows/sha/validation。

验证：`go test ./internal/... -short`、`go vet`、单测覆盖状态机/账本/checkpoint 幂等。

### 阶段 2：Provider Adapter 与调度升级

5. Provider Adapter 扩展 `Probe/Plan/Start/Pause/Resume/Cancel/Validate/Capabilities/Health`（RPC/SQD/AWS/Browser/Cloud 各自实现，兼容现有 Execute）。
6. Discovery / Size Estimator：复用 `firstseen.go`、SQD metadata、覆盖索引，输出 `Address×Dataset` 预估。
7. Smart Scheduler 升级：规模分级 S/M/L/XL、Range 级调度、Range 级切换（保留 completed_ranges）、四层并发控制。
8. Provider Switching at Range 级：错误分类→冷却/重试→切换→从未完成 Range 继续。

验证：单元测试 + 真实小批量（1 地址 × 短区块窗口）切换注入。

### 阶段 3：校验、进度与实时 API

9. Validation Pipeline L3–L6 + Validation Score。
10. Progress Aggregator + EWMA ETA（含冷却/切换/校验时间）+ Provider 原生进度单位。
11. REST：`/api/smart-download/jobs`（创建）、`/import`（列识别）、`pause/resume/cancel`（batch/address/dataset 三级）。
12. SSE：`batch.updated/address.updated/dataset.updated/range.updated/provider.switched/checkpoint.saved/validation.updated/result.ready/error`，250–500ms 聚合。

验证：接口单测 + 一次真实 3 地址多数据集任务的事件流验收。

### 阶段 4：新前端

13. 导航“数据资产 → 智能下载” + 创建页（地址输入/文件列识别/链/数据集/范围/预估）。
14. 任务中心（地址级表格：状态/进度/Provider/速度/ETA/操作；虚拟滚动 + 服务端分页）。
15. 地址详情抽屉（Dataset 进度、Provider History、Validation、执行日志）。
16. 结果数据页（Tabs + 摘要 + 查询/导出 + 进入画像/关系图/调查）。

验证：`npm run build` + 10K 地址创建接口性能 + 100K 虚拟滚动。

### 阶段 5：数据复用与真实容灾验收

17. Dataset Registry 范围级复用：`LOCAL HIT` / 差量补下载。
18. 验收 Case A–E（CSV→SQD、SQD→RPC、RPC→SQD Cloud、暂停重启、10K 地址）+ 数据完整性等式（raw≥normalized≥unique、coverage=100%、unknown_ranges=0）。

---

## 6. 风险与注意事项

- **并发模型重排是最大风险**：`parquetdownload.Manager.Start` 目前单任务互斥，地址级并行必须先在下载引擎层放开并发（DuckDB 内存/磁盘预算要同步控制）。
- **切换 Provider 的“已完成区间”语义**：SQD 流式按块、RPC 按窗口、Cloud 按 chunk，统一到 Range 需要各 Provider 汇报“最后完整落盘的块”，不能只看“请求结束”。
- **Project 规则**：禁止引入数据库；规格 §39-42 的表模型用 JSON 文件实现，需保证原子写 + 启动恢复扫描。
- **兼容性**：`/api/scheduler/*` 已被 SmartFillPanel 使用，新增 `/api/smart-download/*` 时应保留旧端点或做薄适配层，避免前端回归。
- **地址级任务的数据去重**：多 Dataset 并行下唯一键去重必须全局，避免重复写入 merged.parquet。
- **10 万地址创建**：要求 <3s 且不一次性创建 Worker，Range Ledger 需批量写盘（合并 JSON + 原子替换），不能逐条 fsync。

---

## 7. 下一步

建议先做 **阶段 1（统一模型 + Checkpoint V3 + Range Ledger）**，因为它决定后面所有 Provider/UI/验收的基础；实现后跑一遍规格 §63 Case D（暂停重启）作为第一个真实验收门槛。
