# SQD Cloud Emergency Provider Phase 5
# Cloud Mode 生产激活 + Investigation / Graph 全链路验收

> **本地控制面**：`E:\codex\etl`  
> **Cloud Worker**：`E:\Code\Processor-only`  
> **当前 Organization**：`supreme`  
> **当前阶段状态**：Phase 4 已完成，Job-driven + R2/S3 协议 + Local Sync + Registry + DuckDB 已通过真实数据验证  
> **本阶段目标**：把运行模式从 `local` 正式切换为 `cloud`，完成 SQD Cloud 内真实 Portal 数据抓取，并打通 Investigation / Graph 自动消费闭环。
>
> 版本：Phase 5 / V1.0  
> 日期：2026-08-07

---

# 1. 当前真实状态

Phase 4 已完成：

```text
Provider Tier 100：PASS
Cloud Admission Gate：PASS
Circuit Breaker：PASS
Cloud Runtime Manager：PASS
Job Queue：PASS
Lease / Heartbeat：PASS
Checkpoint：PASS
Manifest：PASS
_SUCCESS：PASS
R2/S3 协议：PASS
Local Sync：PASS
SHA256：PASS
DuckDB Validator：PASS
Dataset Registry：PASS
Coverage：PASS
Chunk：25 addresses × 50,000 blocks
TS Worker：PASS
Go ↔ TS 协议 E2E：PASS
真实 Portal 数据：PASS（local mode）
```

真实测试结果：

```text
3 addresses
block range: 114,474,000 - 114,474,500
rows: 435
unique keys: 435
duplicates: 0
files: 1
bytes: 20,499
coverage: MISS → HIT
```

因此 Phase 5 不再重做：

```text
Job Queue
Chunking
Lease
Manifest
Local Sync
Validator
Registry
```

---

# 2. 当前唯一核心缺口

当前仍运行：

```text
RUNTIME_MODE=local
```

这意味着：

```text
Local Backend
→ 本机 TS Worker
→ portal.sqd.dev
→ local/R2 compatible store
```

生产最终形态需要：

```text
RUNTIME_MODE=cloud
```

即：

```text
Local Backend
→ CloudRuntimeManager
→ sqd deploy
→ SQD Cloud Processor
→ portal.sqd.dev
→ R2
→ Local Sync
→ Validator
→ DuckDB
→ Investigation / Graph
```

---

# 3. Phase 5 完成标准

只有以下全部通过，才可标记：

```text
Production Cloud Emergency Provider READY
```

要求：

```text
1. SQD_DEPLOY_KEY 安全注入
2. R2 真实凭据安全注入
3. runtime_mode=cloud
4. Cloud Worker 自动 Deploy
5. Cloud 内 Worker 自动 Lease Job
6. Cloud 内真实访问 BSC Portal
7. Parquet 写入真实 R2
8. Local Sync 自动发现
9. Validator PASS
10. Dataset Registry PASS
11. Coverage MISS → HIT
12. Investigation 自动消费
13. Graph Expand 自动消费
14. Normal Provider 恢复后自动回切
15. Worker Idle 20m 自动 Remove
16. Backend 重启可 Reconcile Cloud Worker
17. Cloud Usage Audit 完整
18. Secret 无泄漏
```

---

# 4. Secret Provisioning

不要把 Secret 写进：

```text
appsettings
yaml
json
Git
日志
前端
数据库明文
```

必须通过：

```text
Environment Variables
或
现有 DPAPI + AES-GCM Secret Store
```

需要：

```text
SQD_DEPLOY_KEY

R2_ENDPOINT
R2_BUCKET
R2_ACCESS_KEY_ID
R2_SECRET_ACCESS_KEY
```

可选：

```text
R2_REGION=auto
```

---

# 5. Windows 启动环境

建议由 `run.ps1` 统一注入。

示例：

```powershell
$env:SQD_RUNTIME_MODE = "cloud"

$env:SQD_ORG = "supreme"

$env:SQD_DEPLOY_KEY = "<从 Secret Store 注入>"

$env:R2_ENDPOINT = "<R2 endpoint>"
$env:R2_BUCKET = "<bucket>"
$env:R2_ACCESS_KEY_ID = "<secret>"
$env:R2_SECRET_ACCESS_KEY = "<secret>"

.\run.ps1
```

注意：

```text
不要将真实值硬编码到 run.ps1。
```

如果 run.ps1 当前支持读取加密配置：

```text
优先复用现有 SecretProvider
```

---

# 6. Cloud Worker 固定 Release

不要每 Job 改：

```text
FROM_BLOCK
TO_BLOCK
WATCH_ADDRESSES
```

正式 Worker：

```text
固定 Release
+
R2 Job Queue
```

建议：

```text
name: bsc-emergency-worker
slot: v2
```

或者：

```text
bsc-cloud-emergency@v1
```

但必须固定且唯一，供：

```text
Reconcile
Idle Remove
Usage Audit
```

识别。

---

# 7. Cloud Runtime 配置

推荐：

```yaml
sqd_cloud:
  enabled: true

  runtime_mode: cloud

  organization: supreme

  worker:
    name: bsc-emergency-worker
    slot: v2

  idle_remove_after: 20m

  deploy_timeout: 10m
  ready_timeout: 10m
  no_progress_timeout: 10m

  max_concurrent_workers: 1

  poll_interval: 5s

  cloud_failure_cooldown: 15m
```

---

# 8. Cloud Worker 启动逻辑

`EnsureWorker()`：

```text
1. acquire deploy lock
2. sqd list --org supreme
3. find managed worker
4. if READY/BUSY:
       reuse
5. if ABSENT:
       deploy
6. wait READY
7. release lock
```

禁止：

```text
每个 Chunk deploy 一个 Worker
```

---

# 9. Backend 启动 Reconcile

服务每次启动：

```text
CloudRuntimeManager.Reconcile()
```

执行：

```powershell
sqd list --org supreme
```

恢复：

```text
ABSENT
READY
BUSY
IDLE
FAILED
```

如果：

```text
本地 state = ABSENT
Cloud 实际 = READY
```

应：

```text
恢复 READY
```

而不是：

```text
重复 deploy
```

---

# 10. Managed Worker Identity

必须固定：

```text
organization
name
slot
```

例如：

```text
supreme
bsc-emergency-worker
v2
```

Remove 前必须严格匹配。

禁止：

```text
按模糊前缀删除
```

---

# 11. R2 生产 Bucket 建议

推荐专门 bucket：

```text
pangu-sqd-cloud
```

或复用现有对象存储中的独立 prefix：

```text
sqd-cloud/
```

目录：

```text
sqd-cloud/
└── bsc/
    └── jobs/
        ├── pending/
        ├── leased/
        ├── completed/
        ├── failed/
        └── archive/
```

---

# 12. Bucket 权限

Cloud Worker 需要：

```text
GET
PUT
HEAD
LIST
DELETE（仅限 lease/move/cancel 类对象）
```

Local Backend 需要：

```text
GET
PUT
HEAD
LIST
DELETE
```

权限范围：

```text
仅限指定 bucket/prefix
```

不要给：

```text
全账户所有 bucket
```

---

# 13. Secret Verification

启动 Cloud Mode 前只检查：

```text
configured = true
```

API 可返回：

```json
{
  "deployment_key_configured": true,
  "r2_configured": true
}
```

禁止返回：

```text
key value
last chars
access key id
secret prefix
```

---

# 14. Cloud Build 再确认

Worker Cloud 构建继续保持：

```text
Node 20
npm ci --ignore-scripts
GZIP
```

原因：

```text
lzo / node-gyp
```

已经验证过，不要重新引入。

---

# 15. `.squidignore`

必须确认：

```text
node_modules/
data/
logs/
builds/
lib/
*.parquet
*.tar.gz
.env
.env.*
```

部署包必须：

```text
远小于 52 MB
```

---

# 16. Phase 5 首次 Canary

不要一上来做三年 / 10 万地址。

第一轮：

```text
1~3 addresses
500~5,000 blocks
token_transfer
```

目标：

```text
验证 Cloud Mode 本身
```

不是性能。

---

# 17. Canary 触发

使用 Fault Injection：

```text
SQD Public = CIRCUIT_OPEN
RPC = RATE_LIMITED
AWS = UNAVAILABLE
Browser = RISK_CONTROLLED
```

确保：

```text
ALL_NORMAL_PROVIDERS_EXHAUSTED
```

然后：

```text
Cloud Admission = APPROVED
```

---

# 18. Canary 预期流程

```text
Job Created
→ Coverage MISS
→ Normal Providers Exhausted
→ Cloud Admission
→ Worker ABSENT
→ DEPLOYING
→ READY
→ enqueue
→ pending
→ leased
→ BUSY
→ Portal
→ Parquet
→ R2
→ Manifest
→ _SUCCESS
→ Local Sync
→ Validator
→ Registry
→ Coverage HIT
→ Job COMPLETE
```

---

# 19. Cloud 内 Portal 验收

必须在 Cloud Worker 日志确认：

```text
portal response success
processed blocks
rows
from_block
to_block
```

本地是否能直接访问 Portal：

```text
不再作为 Cloud Mode PASS/FAIL 条件
```

---

# 20. Cloud Worker 真实网络路径

最终证明：

```text
SQD Cloud infrastructure
→ portal.sqd.dev
```

而不是：

```text
Windows local machine
→ portal.sqd.dev
```

这是 Phase 5 最重要的生产验证。

---

# 21. R2 验收

必须看到：

```text
request.json
status.json
checkpoint.json
parquet
manifest.json
_SUCCESS
```

顺序：

```text
parquet
→ checkpoint final
→ manifest
→ _SUCCESS
```

---

# 22. Local Sync

Cloud `_SUCCESS` 后：

```text
Local Sync Worker 自动启动
```

不要要求人工：

```text
POST /cloud/sync
```

手动 API 可以保留为：

```text
debug / recovery
```

但生产必须自动。

---

# 23. Auto Sync Trigger

建议：

```text
Cloud Provider Status
发现 COMPLETED
→ enqueue LocalSync
```

或者：

```text
datasetsync poll completed prefix
```

推荐：

```text
事件 + polling 双保险
```

---

# 24. Local Sync 幂等

如果本地已存在：

```text
same sha256
same manifest
```

则：

```text
SKIP
```

不要重复下载。

---

# 25. Validator

必须继续验证：

```text
Schema
Row Count
Unique Key
Duplicate
Min Block
Max Block
SHA256
```

Token Transfer 唯一键：

```text
chain_id
block_number
transaction_hash
log_index
```

---

# 26. 0-row Chunk

必须保持：

```text
合法完成
```

登记 Coverage：

```text
complete = true
rows = 0
```

避免重复下载空窗口。

---

# 27. Legacy Manifest 治理

当前已知旧记录：

```text
9cf39d91…
```

Manifest 曾缺：

```text
token_transfers/
```

正式策略：

```text
legacy_invalid = true
sync_warning = true
registry_skip = true
```

不要自动删除。

---

# 28. Legacy Archive

建议增加：

```text
archive/legacy-invalid/
```

但是迁移应：

```text
显式工具执行
```

不要在正常 sync 自动移动旧记录。

---

# 29. Manifest Version

从现在开始必须：

```json
{
  "schema_version": 1
}
```

未来协议变化：

```text
schema_version = 2
```

不要靠字段猜测版本。

---

# 30. Investigation 自动消费

目标：

```text
用户创建 Investigation
```

如果：

```text
Coverage MISS
Normal Providers Exhausted
```

则：

```text
Cloud 自动完成数据补齐
```

成功 Sync：

```text
DATASET_INDEXED
```

Investigation：

```text
自动继续 ANALYZING
```

禁止：

```text
要求用户重新点“开始调查”
```

---

# 31. Investigation State

推荐：

```text
WAITING_DATA
→ DOWNLOADING
→ INDEXING
→ ANALYZING
→ COMPLETED
```

Cloud 只是：

```text
DOWNLOADING
```

的一种 Provider。

---

# 32. Graph 自动消费

Graph Expand：

```text
address X downstream depth=1
```

若本地 Coverage MISS：

```text
Orchestrator
→ Cloud fallback
```

Sync 完成：

```text
DATASET_INDEXED
```

Graph：

```text
incremental query
→ add nodes
→ add edges
```

---

# 33. Graph 不等全 Job

每个 Chunk：

```text
INDEXED
```

即可：

```text
增量更新图
```

不要等整个大 Job 完成。

---

# 34. Provider Recovery 回切

测试：

```text
Cloud 正在 BUSY
Normal Provider 恢复
```

规则：

```text
当前 leased Cloud Chunk 完成
Cloud 不领取新的 pending Chunk
新 Chunk → Normal Provider
```

---

# 35. Recovery Probe

Circuit Open：

```text
cooldown
→ HALF_OPEN
→ probe
```

成功：

```text
HEALTHY
```

随后：

```text
router normal tier 恢复
```

---

# 36. Cloud Drain

Normal Provider 恢复后：

```text
Cloud runtime = DRAINING
```

含义：

```text
不领取新任务
完成当前任务
```

然后：

```text
IDLE
```

---

# 37. Idle Remove

条件必须全部满足：

```text
pending == 0
leased == 0
running == 0
draining == false
last_job_finished > 20m
```

才：

```text
sqd remove
```

---

# 38. Remove 验证

Remove 后：

```powershell
sqd list --org supreme
```

必须确认 managed Worker 不存在。

Cloud Runtime：

```text
ABSENT
```

---

# 39. Usage Audit

必须记录：

```text
deploy_started_at
ready_at
first_job_at
last_job_at
idle_started_at
remove_at

total_runtime_seconds
busy_runtime_seconds
idle_runtime_seconds

jobs
chunks
rows
bytes
```

---

# 40. Cost KPI

建议 UI：

```text
Cloud Fallback Ratio
Cloud Runtime Today
Cloud Rows Today
Cloud Chunks Today
```

目标：

```text
Cloud Fallback Ratio 尽量低
```

---

# 41. Cloud Overuse Alert

如果：

```text
cloud_fallback_ratio > 20%
```

或：

```text
cloud runtime > configured threshold
```

记录：

```text
CLOUD_OVERUSE_WARNING
```

不必直接停服务，但应提示：

```text
Normal Provider 层可能存在系统性异常
```

---

# 42. Cloud Budget

Phase 5 建议：

```yaml
cloud_budget:
  max_concurrent_workers: 1
  idle_remove_after: 20m
  daily_runtime_limit: configurable
```

不要写死未经验证的美元预算。

---

# 43. Cloud Deploy Failure

如果：

```text
deploy failed
```

进入：

```text
CLOUD_RUNTIME_FAILED
```

并：

```text
15m cooldown
```

禁止：

```text
无限 deploy loop
```

---

# 44. Cloud Portal Failure

Cloud Worker 内：

```text
Portal 503
```

仍然执行：

```text
retry
backoff
checkpoint
```

如果最终失败：

```text
Chunk = RETRYABLE
```

不要删除 remote artifacts。

---

# 45. Failed Job Evidence

保留：

```text
request.json
status.json
checkpoint.json
error.json
```

方便：

```text
审计
恢复
重试
```

---

# 46. Cancel

如果 Investigation 被取消：

```text
write cancel marker
```

Worker：

```text
batch boundary
→ checkpoint
→ CANCELLED
```

不要强杀到半个 Parquet 文件。

---

# 47. Backend 重启测试

运行 Cloud Job 时：

```text
重启 .\run.ps1
```

预期：

```text
Reconcile
→ 找到 Cloud Worker
→ 找到 leased/running Job
→ 恢复状态
→ 不重复 deploy
→ 不重复 enqueue
```

---

# 48. Worker 重启测试

模拟 Worker restart：

```text
Lease + Checkpoint
```

预期：

```text
continue
```

不是：

```text
from_block 重新开始
```

---

# 49. R2 临时故障

模拟：

```text
PUT timeout
```

预期：

```text
retry
checkpoint preserved
job RETRYABLE
```

---

# 50. Local Sync 故障

模拟：

```text
本地磁盘写失败
```

Cloud Job：

```text
仍保持 completed
```

Local Sync：

```text
RETRY_PENDING
```

不要重新跑 Cloud 数据下载。

---

# 51. Cloud 与 Local Sync 解耦

非常重要：

```text
Cloud Export 成功
≠ Local Sync 成功
```

状态必须分开。

例如：

```text
REMOTE_COMPLETED
LOCAL_SYNC_PENDING
LOCAL_SYNCED
INDEXED
```

---

# 52. Job 总状态

建议：

```text
CLOUD_RUNNING
REMOTE_COMPLETED
LOCAL_SYNCING
LOCAL_VALIDATING
INDEXING
ANALYZING
COMPLETED
```

---

# 53. Frontend 状态

SmartFillPanel：

```text
☁ 应急 Cloud
Worker: BUSY
Queue: 2
Current Chunk: chunk_x
Remote: 80%
Sync: 45%
Index: pending
```

---

# 54. Frontend 不显示底层 Secret / Command

禁止：

```text
Deployment Key
R2 key
sqd deploy command
```

只显示：

```text
configured
runtime state
job state
usage
```

---

# 55. Phase 5 Canary 规模

第一轮：

```text
addresses: 1~3
blocks: 500~5,000
```

第二轮：

```text
addresses: 25
blocks: 50,000
```

第三轮：

```text
100~1,000 addresses
```

不要直接：

```text
100,000 addresses
```

---

# 56. Phase 5 Load Validation

25 地址 × 50,000 blocks：

记录：

```text
deploy latency
ready latency
portal processing duration
rows
R2 upload MB/s
Local Sync MB/s
DuckDB validation duration
total E2E duration
```

---

# 57. 生产规模前提

只有：

```text
单 Chunk Cloud Mode PASS
25×50k PASS
Recovery PASS
Idle Remove PASS
```

才进入：

```text
10k addresses
```

再逐步：

```text
50k
100k
```

---

# 58. 不要因为 Cloud 可用就改变优先级

Phase 5 完成后仍保持：

```text
SQD_CLOUD_EXPORT Tier 100
```

禁止：

```text
提升 Tier
加入 normal race
```

---

# 59. Normal Provider 健康时

必须持续测试：

```text
Cloud calls = 0
```

这是生产最重要回归项之一。

---

# 60. Monitoring

新增或保留：

```text
cloud_worker_state
cloud_queue_depth
cloud_jobs_running
cloud_jobs_completed
cloud_jobs_failed
cloud_rows_total
cloud_bytes_total

cloud_deploy_total
cloud_remove_total

cloud_remote_complete_total
cloud_local_sync_total

cloud_reconcile_total
cloud_orphan_detected_total
```

---

# 61. Alert

建议：

```text
worker DEPLOYING > 10m
worker BUSY no progress > 10m
queue > threshold
R2 sync backlog
Cloud runtime > expected
Cloud fallback ratio high
```

---

# 62. Security 验收

执行：

```text
git grep
log grep
API response grep
```

检查：

```text
SQD_DEPLOY_KEY
R2_SECRET_ACCESS_KEY
```

真实值不得出现。

---

# 63. Log Redaction

统一 Redactor：

```text
Authorization
x-api-key
secret
access key
deployment key
```

---

# 64. R2 Key Rotation

系统应支持：

```text
重启服务
→ 新 env Secret
```

而无需：

```text
改源码
重新 Build
```

---

# 65. SQD Key Rotation

同理：

```text
Deployment Key 更新
→ Secret Store 更新
→ Backend Reload/Restart
```

Worker Release 无需变化。

---

# 66. Phase 5 测试矩阵

## Test A

```text
Normal Provider Healthy
```

预期：

```text
Cloud 0
```

## Test B

```text
All Normal Exhausted
Cloud Credentials Missing
```

预期：

```text
Cloud Admission Rejected
Reason = CREDENTIALS_NOT_CONFIGURED
```

## Test C

```text
All Normal Exhausted
Cloud Credentials Valid
```

预期：

```text
Deploy
```

## Test D

```text
Cloud Worker Existing
```

预期：

```text
Reuse
No second deploy
```

## Test E

```text
Remote Job Complete
```

预期：

```text
Auto Sync
```

## Test F

```text
Local Sync Failure
```

预期：

```text
No Cloud Redownload
```

## Test G

```text
Normal Provider Recovery
```

预期：

```text
Drain Cloud
New Chunk Normal
```

## Test H

```text
Idle 20m
```

预期：

```text
Remove
```

## Test I

```text
Backend Restart
```

预期：

```text
Reconcile
```

---

# 67. Phase 5 E2E

最终必须有一次：

```text
Investigation
→ Fault Injection
→ Cloud Admission
→ Cloud Deploy
→ Cloud Worker
→ Portal
→ R2
→ Local Sync
→ DuckDB
→ Investigation Continue
→ Graph Update
→ Normal Provider Recover
→ Cloud Drain
→ Idle Remove
```

这才是最终生产闭环。

---

# 68. 实施顺序

```text
5.1 Secret Provisioning
```

```text
5.2 Cloud Mode 配置
```

```text
5.3 Managed Worker Release
```

```text
5.4 Reconcile
```

```text
5.5 真实 R2
```

```text
5.6 Cloud Canary
```

```text
5.7 Auto Sync
```

```text
5.8 Investigation E2E
```

```text
5.9 Graph E2E
```

```text
5.10 Recovery / Drain / Idle Remove
```

```text
5.11 Security Audit
```

---

# 69. Codex 执行指令

```text
Phase 4 已完成，不要重做 Job Queue、Lease、Chunk、Manifest、Local Sync、Registry、Validator。

现在执行 Phase 5：Cloud Mode Production Activation。

目标：
将 sqd_cloud runtime 从 local 切换到真实 cloud，
使用已实现的 Job-driven Worker + R2/S3 协议完成真正 SQD Cloud 数据面闭环。

要求：

1. SQD_DEPLOY_KEY、R2 凭据只通过 Environment/现有 Secret Store 注入。
2. Organization 固定使用 supreme。
3. Worker 固定 identity，推荐 bsc-emergency-worker@v2。
4. 启动时实现 CloudRuntimeManager.Reconcile()，通过 sqd list --org supreme 恢复实际 Worker。
5. 不允许每个 Job deploy；Worker 复用 Job Queue。
6. 首次使用真实 R2。
7. 使用 Fault Injection 让所有 Normal Provider Exhausted。
8. 完成真实 Cloud Canary：
   Admission → Deploy → READY → Lease → Portal → R2 → Manifest → _SUCCESS → Local Sync → Validator → Registry → Coverage HIT。
9. Local Sync 必须自动触发；POST /cloud/sync 只保留为人工恢复接口。
10. Cloud Export 与 Local Sync 状态分离，Local Sync 失败不得重新跑 Cloud。
11. Investigation 在 DATASET_INDEXED 后自动继续，不需要用户重新启动。
12. Graph Expand 在 DATASET_INDEXED 后自动增量更新。
13. Normal Provider 恢复后，Cloud 进入 Drain：当前 Chunk 完成，新 Chunk 回切 Normal Provider。
14. Idle Remove 条件必须检查 pending/leased/running 均为 0。
15. Backend 重启必须 Reconcile，不得重复 deploy。
16. Worker restart 必须从 Remote Checkpoint 恢复。
17. 保留旧 9cf39d91… invalid manifest，只告警、不登记；不得自动删除。
18. 所有新 Manifest 必须带 schema_version。
19. 执行 Secret 泄漏审计。
20. 完成后生成 PHASE5_IMPLEMENTATION_REPORT.md。

验收前不要扩大到 10 万地址。
顺序必须：
small canary → 25×50k → recovery → idle remove → Investigation/Graph E2E。
```

---

# 70. Phase 5 最终完成定义

Phase 5 完成后可以正式标记：

```text
SQD Cloud Emergency Provider
Production Ready
```

且系统应满足：

```text
正常情况下：
Cloud = ABSENT
成本 = 0 或极低

常规 Provider 全部失败：
自动 Deploy Cloud Worker
→ 自动获取 BSC 数据
→ 自动写 R2
→ 自动回流本地
→ 自动索引
→ 自动继续调查 / 更新关系图

常规 Provider 恢复：
自动回切
→ Cloud Drain
→ 20m Idle
→ 自动 Remove
```

这是 SQD Cloud 在现有链上分析工具中的最终生产定位。
