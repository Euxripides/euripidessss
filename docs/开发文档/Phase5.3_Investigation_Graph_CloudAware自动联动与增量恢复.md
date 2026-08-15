# Phase 5.3：Investigation + Graph Cloud-Aware 自动联动与增量恢复设计

> 基线：Phase 5.2 已通过。
>
> 当前已验证：25 地址 × 50,000 blocks 主 Canary 完成；严格 block boundary、dup=0、Coverage MISS→HIT；Lease heartbeat / 过期恢复 / Worker Crash / Cancel / Sync Retry / Provider Recovery / Idle Remove 全部 PASS；SQD Cloud 保持 Tier 100 / Last Resort。
>
> 本阶段目标不是继续扩大下载规模，而是把 **Investigation、Graph、Orchestrator、Dataset Registry、Cloud Runtime** 串成事件驱动闭环，使调查任务和图谱扩展完全不关心数据来自哪个 Provider。

## 1. 核心目标

```text
Investigation / Graph Expand
        ↓
DataRequirement
        ↓
Coverage Resolver
        ↓
Smart Download Orchestrator
        ↓
Normal Provider
        ↓ 不可用/缺口
SQD Cloud Tier 100
        ↓
R2 → Local Sync → Validator → Dataset Registry
        ↓
DATASET_INDEXED Event
        ↓
Investigation 自动恢复
        ↓
Graph Incremental Update
        ↓
READY_FOR_ANALYSIS / GRAPH_READY
```

必须实现：
- Investigation 不直接调用 SQD/RPC/Cloud；
- Graph Expand 不直接调用任何 Provider；
- 所有数据需求统一进入 Orchestrator；
- Coverage FULL 直接复用本地；PARTIAL/MISS 只补缺口；
- Dataset Indexed 后自动恢复 Investigation；
- Graph 只做增量更新，不整图重建；
- 重启后能恢复未完成调查和图谱任务；
- SQD Cloud 始终 Tier 100 / Last Resort。

## 2. 统一 DataRequirement Contract

```json
{
  "requirement_id": "uuid",
  "source": "investigation|graph_expand|manual",
  "case_id": "optional",
  "investigation_id": "optional",
  "graph_id": "optional",
  "chain_key": "bsc",
  "dataset": "token_transfer",
  "addresses": ["0x..."],
  "from_block": 114450000,
  "to_block": 114499999,
  "direction": "both|in|out",
  "tokens": ["0x55d398..."],
  "priority": "interactive|normal|background",
  "cloud_eligible": true,
  "requested_by": "system",
  "continuation_token": "optional"
}
```

硬规则：上层只能描述“需要什么数据”，不能指定 provider。

## 3. Investigation 状态机

```text
PLANNING
→ CHECKING_DATA
→ WAITING_DATA
→ DATA_READY
→ ANALYZING
→ EXPANDING
→ COMPLETED
```

异常：
```text
WAITING_DATA_TIMEOUT
DATA_UNAVAILABLE
ANALYSIS_FAILED
CANCELLED
```

缺数据时不得直接失败：
```text
缺数据 → WAITING_DATA
```

WAITING_DATA 必须持久化 requirement_id、dataset、addresses、block range、coverage、plan_id、pending chunks、continuation state。

## 4. Graph Expand 状态机

```text
GRAPH_EXPAND_REQUESTED
→ GRAPH_DATA_CHECK
→ GRAPH_WAITING_DATA
→ GRAPH_DATA_READY
→ GRAPH_INCREMENTAL_BUILD
→ GRAPH_EXPAND_COMPLETED
```

图内“上游/下游/双向/下一层/前一层/深度 N”操作只生成 DataRequirement。

## 5. Dataset Event Bus

事件：
```text
DATA_REQUIREMENT_CREATED
DOWNLOAD_PLAN_CREATED
DOWNLOAD_STARTED
REMOTE_COMPLETED
LOCAL_SYNC_STARTED
DATASET_VALIDATED
DATASET_INDEXED
COVERAGE_UPDATED
INVESTIGATION_RESUMED
GRAPH_INCREMENT_APPLIED
```

最关键事件：`DATASET_INDEXED`。

```json
{
  "event_id": "uuid",
  "requirement_id": "uuid",
  "chain_key": "bsc",
  "dataset": "token_transfer",
  "addresses": ["0x..."],
  "from_block": 114450000,
  "to_block": 114499999,
  "registry_entry_ids": ["..."],
  "row_count": 484250,
  "coverage_status": "HIT",
  "provider": "SQD_CLOUD_EXPORT",
  "indexed_at": "..."
}
```

## 6. Investigation 自动恢复

订阅 `DATASET_INDEXED`，按 requirement_id 或 chain+dataset+address+range 匹配。

```text
WAITING_DATA
→ DATA_READY
→ ANALYZING
```

必须从 continuation state 恢复，不能重新从头规划整个调查。

## 7. Graph Incremental Update

禁止每次数据到达后全量重建图。

```text
DATASET_INDEXED
→ resolve new registry entries
→ query only new rows
→ extract nodes/edges
→ dedupe identity
→ merge into graph state
→ recompute affected statistics
```

Node Key：`chain_id + address`

Edge Key 建议：`chain_id + transaction_hash + log_index + from_address + to_address + token_address`

## 8. Coverage 与 Graph Expansion

```text
FULL    → 直接查询本地
PARTIAL → 只创建缺口 Requirement
MISS    → 创建完整 Requirement
```

Cloud 仍只补 missing chunks。

## 9. Phase 5.2 Cancel 新二进制重放

Phase 5.3 第一项真实回归：
1. 使用新后端二进制；
2. 提交小 Cloud eligible 任务；
3. RUNNING 后 UI/API Cancel；
4. 验证：

```text
JobProgress = cancelled
R2 cancel marker exists
R2 cancelled terminal exists
no _SUCCESS
checkpoint.cancelled = true
lease removed
Investigation/Graph 收到 CANCELLED
```

## 10. Parquet 增量分片上传

为解决 Worker 容器重启后无法精确累计未上传行，升级为 immutable multipart：

```text
chunk/
  part-000001.parquet
  part-000002.parquet
  checkpoint.json
  manifest.partial.json
  manifest.json
  _SUCCESS
```

建议任一阈值触发 flush：
```text
25,000 rows
或 64 MB
或 5,000 blocks
```

## 11. Checkpoint V2

```json
{
  "job_id": "...",
  "chunk_id": "...",
  "last_processed_block": 114453238,
  "parts": [
    {"path":"part-000001.parquet","row_count":25000,"sha256":"..."}
  ],
  "rows_committed": 25000,
  "updated_at": "..."
}
```

Worker 重启后：验证已上传 parts，从 `last_processed_block + 1` 继续。

## 12. Manifest V2

```json
{
  "schema_version": 2,
  "job_id": "...",
  "chunk_id": "...",
  "provider": "SQD_CLOUD_EXPORT",
  "from_block": 114450000,
  "to_block": 114499999,
  "row_count": 484250,
  "parts": [
    {"path":".../part-000001.parquet","rows":25000,"bytes":123456,"sha256":"..."}
  ],
  "min_block": 114450000,
  "max_block": 114499999,
  "completed": true
}
```

Validator 要求：`sum(parts.rows) == manifest.row_count`。

## 13. Local Sync V2

支持 multi-part：
```text
download missing parts only
→ verify SHA256 each part
→ validate ranges
→ atomic Registry commit
→ merge/query layer update
```

失败只重试失败 part，不重跑 Cloud fetch。

## 14. Event Idempotency

消费者维护 processed_events。重复 DATASET_INDEXED 不得造成：
- Investigation 重跑；
- Graph 重复边；
- 统计翻倍。

## 15. Backend Restart Recovery

重启后：
1. 扫描 Investigation WAITING_DATA；
2. 扫描 Graph GRAPH_WAITING_DATA；
3. 查询 Orchestrator plan；
4. 查询 Registry/Coverage；
5. 已 HIT → 补发内部 DATASET_INDEXED；
6. remote completed/local not indexed → 只恢复 Local Sync。

## 16. Investigation ↔ Graph 双向联动

Investigation 发现高价值地址：
```text
发现候选
→ Graph add node
→ Graph request expansion
→ DataRequirement
→ Orchestrator
```

Graph 新发现高价值 Counterparty：
```text
emit INVESTIGATION_CANDIDATE
→ Planner 判断是否纳入调查
```

避免无限 BFS，受 depth、budget、address cap、risk threshold、objective 控制。

## 17. Objective 驱动数据需求

目标可包括：
- 资金沉淀
- 交易所归集
- 大额获利
- Token 获利
- 上游来源
- 下游去向
- 身份落查

Planner 根据目标决定需要 token_transfer / native tx / trace / balance / counterparty，避免所有调查都全量下载。

## 18. 状态语义

建议拆分：
```text
DATA_READY
ANALYSIS_READY
GRAPH_READY
```

UI 显示“数据下载完成 / 正在分析 / 图谱增量已生成”。

## 19. Phase 5.3 E2E 场景

A. Coverage HIT：无下载直接分析与绘图。

B. Normal Provider：MISS → normal provider → indexed → 自动恢复 → graph incremental。

C. Cloud Last Resort：MISS → normals exhausted → Cloud → R2 → Sync → DATASET_INDEXED → Investigation resume → Graph update。

D. Cancel：WAITING_DATA → cancel → remote cancelled → investigation cancelled → graph 不导入 partial。

E. Backend Restart：WAITING_DATA → restart → reconcile → 自动恢复。

F. Worker Restart：Cloud Worker crash → multipart checkpoint → resume → no duplicate → Investigation 仅恢复一次。

## 20. 性能目标

重点不是极限下载吞吐，而是联动延迟：
```text
DATASET_INDEXED → Investigation Resume < 2s
DATASET_INDEXED → Graph Increment Start < 2s
Graph incremental update < 3s（中等增量）
duplicate event side effect = 0
```

## 21. 安全

继续禁止 Secret 出现在日志/API/前端/Git。建议本阶段开始把 Windows User Environment 迁移至 DPAPI + AES-GCM Secret Store，但不能阻塞 Cloud Runtime。

## 22. Phase 5.3 PASS Gate

- [ ] Investigation 不直接调用 Provider
- [ ] Graph 不直接调用 Provider
- [ ] DataRequirement Contract 统一
- [ ] Coverage FULL/PARTIAL/MISS 正确
- [ ] DATASET_INDEXED Event 可用
- [ ] Investigation WAITING_DATA 自动恢复
- [ ] Graph incremental update
- [ ] Event idempotency
- [ ] Backend restart recovery
- [ ] Cancel 新二进制真实重放 PASS
- [ ] Multipart Parquet
- [ ] Worker restart 精确 rows resume
- [ ] Local Sync multi-part
- [ ] Manifest V2
- [ ] Normal Provider E2E
- [ ] Cloud Last Resort E2E
- [ ] READY_FOR_ANALYSIS / GRAPH_READY 状态清晰
- [ ] 无重复节点/边/统计
- [ ] Cloud Tier 100 不变
- [ ] Secret Audit PASS

## 23. 下一阶段

Phase 5.3 通过后进入 Phase 5.4：Investigation Production Scale + Graph Scale。

建议顺序：
```text
1,000 地址
→ 10,000 地址
→ 50,000 地址
→ 100,000 地址
```

只有联动闭环稳定后才扩大规模。

## 最终原则

Phase 5.3 的成功标准不是“Cloud 能下载更多数据”，而是：

> Investigation 和 Graph 已彻底从数据源实现解耦，任何数据缺口都由 Orchestrator 自动解决；数据一旦进入 Registry，调查和图谱自动恢复，用户无需手工重跑。
