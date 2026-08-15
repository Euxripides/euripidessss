# Phase 5.4.1：真实 Crash Resume Gate + 1K→100K 规模验收

> 基线：Multipart 阈值、Part 去重、Lease Reaper、CANCELLED、Event Bus、Objective Planner 已完成。
>
> 当前唯一 P0：**真实中途 Crash Resume（已上传 parts 后崩溃，再精确恢复 rows/parts）尚未形成完整 PASS 证据。**
>
> 在该 P0 通过前，不进入正式 1K / 10K / 50K / 100K 规模档。

## 1. Gate A：真实中途 Crash Resume

测试必须保证：

```text
至少上传 2 个 committed parts
任务仍 RUNNING
```

建议新增测试开关：

```text
SQD_TEST_CRASH_AFTER_PARTS=2
```

行为：

```text
part-000001 uploaded
part-000002 uploaded
checkpoint V2 persisted
→ process.exit(137)
```

生产默认关闭。

### Crash 前记录

```text
job_id
chunk_id
last_completed_block
rows_committed
parts count
part paths
part sha256
lease expires_at
```

### 恢复链路

```text
Worker crash
→ heartbeat stop
→ lease expires
→ same job_id requeue
→ new lease
→ read checkpoint V2
→ verify existing parts
→ rows_offset = rows_committed
→ resume from last_completed_block + 1
→ continue next part number
→ REMOTE_COMPLETED
```

### 硬验收

```text
same job_id
same chunk_id
old part SHA unchanged
old parts not re-uploaded
new part numbering continuous
sum(parts.rows) == manifest.row_count
remote rows == local validated rows
dup = 0
range violation = 0
```

恢复时：

```text
if part exists + sha matches → reuse
if part exists + sha mismatch → fail hard
```

禁止覆盖 committed part。

新增 Validator：

```text
duplicate_part_sha_count > 0
→ LOCAL_VALIDATION_FAILED
```

## 2. Crash Resume PASS Gate

- [ ] crash 前 ≥2 committed parts
- [ ] 精确触发 crash
- [ ] same job_id requeue
- [ ] checkpoint V2 读取成功
- [ ] rows_committed 恢复
- [ ] part SHA 不变
- [ ] 旧 part 不重传
- [ ] 新 part 编号连续
- [ ] sum(parts.rows)==row_count
- [ ] dup=0
- [ ] range violation=0
- [ ] Local Sync PASS
- [ ] Registry PASS
- [ ] Coverage PASS
- [ ] Investigation 只恢复一次
- [ ] Graph 只增量一次

## 3. Scale 前生产硬化

### Coverage Index

新增：

```text
address_dataset_coverage
```

字段：

```text
chain_id
address
dataset
covered_from
covered_to
row_count
updated_at
```

目标：100K Coverage 不扫描全部 manifest。

### UI 状态

补齐：

```text
WAITING_DATA = 等待链上数据
DATA_READY = 数据已就绪
CANCELLED = 已取消
```

### Graph Viewport

存储图与可视图分离：

```text
Storage Graph
Analysis Graph
Viewport Graph
```

默认前端门槛：

```text
nodes <= 500
edges <= 2,000
```

超限通过 cluster / collapse / filter / expand-on-demand。

### Metrics

至少新增：

```text
requirements_total
coverage_hit_ratio
provider_success_rate
provider_p95_ms
cloud_fallback_ratio
event_lag_ms
investigation_resume_ms
graph_increment_ms
registry_rows
multipart_parts_total
resume_total
cancel_total
local_sync_retry_total
```

### Secret Store

迁移：

```text
SQD_DEPLOY_KEY
R2_ACCESS_KEY_ID
R2_SECRET_ACCESS_KEY
```

到现有 DPAPI + AES-GCM Secret Store。

## 4. Gate B：Scale Ladder

固定顺序：

```text
Stage A = 1K
Stage B = 10K
Stage C = 50K
Stage D = 100K
```

每档 PASS 后才能进入下一档。

### Stage A：1K

建议：

```text
1,000 addresses
50,000 blocks
token_transfer
```

验收：

```text
failed chunks = 0
dup = 0
range violation = 0
event duplicate side effect = 0
graph duplicate edge = 0
```

### Stage B：10K

记录：

```text
plan time
chunk count
queue P50/P95
provider P50/P95
sync throughput
registry commit time
coverage lookup time
event lag
graph increment duration
DuckDB latency
memory
disk growth
open handles
```

### Stage C：50K

任务中途强制一次 backend restart。

验证：

```text
unfinished requirements restored
pending plans restored
remote completed/local pending resumes sync
events not duplicated
Investigation resumes once
Graph increment once
```

### Stage D：100K

目标不是让 Cloud 跑 100K，而是：

```text
100K Requirement
→ Coverage
→ Normal Providers 主处理
→ Cloud 只补 missing chunks
```

记录：

```text
normal_provider_jobs
cloud_jobs
cloud_fallback_ratio
```

## 5. Cloud Fallback Guard

建议警戒线：

```text
cloud_fallback_ratio > 0.20
→ STOP SCALE
```

先分析普通 Provider 稳定性、Coverage、Circuit、Auth，不允许把 Cloud 升成主通道。

## 6. 数据一致性 Gate

每档必须：

```text
manifest rows
=
validated rows
=
registered rows
=
merged distinct rows
```

并且：

```text
dup = 0
out_of_range = 0
unexpected_address = 0
```

任一出现则停止进入下一档。

## 7. Objective Planner Scale 验收

至少选 3 类：

```text
fund_sink
destination_trace
token_profit
```

验证 Objective Matrix 确实减少无关 DataRequirement。

对比：

```text
无 Objective
vs
Objective-Driven
```

记录：

```text
dataset_count
chunk_count
rows
bytes
runtime
cloud eligibility
```

## 8. 自动化回归

继续：

```text
go test ./... -short
go vet ./...
go build
npm run build
```

新增：

```text
crash-after-parts
resume rows_offset
duplicate part SHA
coverage index
event lock
objective cost guard
viewport cap
```

## 9. 最终 PASS Gate

### Runtime
- [ ] 真实中途 Crash Resume PASS
- [ ] committed parts 精确复用
- [ ] rows_committed 精确恢复
- [ ] duplicate part SHA = 0

### Hardening
- [ ] Coverage Index
- [ ] WAITING_DATA / DATA_READY UI
- [ ] Graph Viewport
- [ ] Metrics
- [ ] DPAPI Secret Store

### Scale
- [ ] 1K PASS
- [ ] 10K PASS
- [ ] 50K PASS
- [ ] 100K PASS

### Integrity
- [ ] dup=0
- [ ] range violation=0
- [ ] Coverage 正确
- [ ] Event 幂等
- [ ] Graph 无重复
- [ ] Cloud fallback 受控
- [ ] Secret Audit PASS

## 10. 下一阶段

全部通过后进入：

```text
Phase 5.5：Investigation Intelligence Layer
```

重点：

```text
资金沉淀识别
大额获利分析
Token 获利分析
交易所归集/提现识别
地址聚类
身份线索评分
调证线索生成
```

届时下载、恢复、Registry、Event、Graph 基础设施应基本冻结，只做 bugfix。
