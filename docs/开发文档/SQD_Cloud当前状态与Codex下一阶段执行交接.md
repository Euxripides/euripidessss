# SQD Cloud 当前状态与 Codex 下一阶段执行交接

> 日期：2026-08-07  
> 当前阶段：Cloud Processor-only 基础验证已完成  
> 下一阶段：接入 Smart Download Orchestrator，作为最终兜底 Provider

## 1. 旧状态已经失效

以下内容已经过时：

```text
Cloud 部署目前只差认证凭据
sqd whoami Authentication failure
等待 Deployment Key
等待 Org Code
```

当前真实状态：

```text
Deployment Key 认证：已完成
Professional Organization：supreme
Cloud 部署：已完成
Squid：bsc-usdt-smoke
Slot：v1
Dedicated small Processor：已成功创建
BSC Portal：已成功访问
历史追赶：已通过
实时跟链：已通过
Cloud Processor 删除：已通过
遗留 Running Resource：应为 0
```

Codex 不得重新要求用户提供 Deployment Key。

## 2. 已完成的本地验证

项目目录：

```text
E:\Code\Processor-only
```

备份目录：

```text
E:\Code\Processor-only-backup-20260807-113302
```

已完成：

```text
sqd build PASS
lib/main.js exists
```

本地 Processor：

```text
起始块：114423844
约 2.5 分钟追到链头
约 5 万区块
3,539,363 条 USDT Transfer
```

本地测试期间：

```text
429 = 0
500 = 0
503 = 0
timeout = 0
```

输出：

```text
data\status.txt
153 个 transfers.parquet
9 列 schema
3,539,363 rows
```

PyArrow：

```text
Parquet 校验 PASS
Schema 校验 PASS
Row Count 校验 PASS
```

断点恢复：

```text
last processed block = 114474197
重启后仅补约 22 blocks
重新到达链头
```

结论：

```text
Local Smoke Test = PASS
Checkpoint Resume = PASS
```

## 3. SDK 实际适配结果

已确认：

### logIndex

Portal Stream 中 `logIndex` 属于必选字段，不需要显式加入 FieldSelection。

### setForceFlush

当前 file-store SDK 必须使用：

```typescript
ctx.store.setForceFlush(true)
```

不要改回：

```typescript
ctx.store.setForceFlush()
```

## 4. Cloud Build 已解决的问题

Cloud 初次构建曾遇到：

```text
node_modules/lzo
node-gyp rebuild
Cloud image missing Python
```

最终通过：

```text
npm ci --ignore-scripts
```

避免编译未使用的原生 LZO 依赖。

当前使用：

```text
GZIP
```

因此不得随意切换到：

```text
LZO
LZ4
SNAPPY
```

除非后续采用自定义构建环境并安装 Python、make、g++。

## 5. Cloud 部署验证已经完成

使用组织：

```text
supreme
```

成功部署：

```text
supreme/bsc-usdt-smoke@v1
```

Cloud 运行日志已确认：

```text
HTTP 200
x-sqd-data-source = real_time
isHead = true
highestBlock 与 finalized/head 对齐
Processor 持续处理 BSC USDT Transfer
```

因此不要重新进行 Deployment Key 测试、small Processor 冒烟或基础 BSC USDT smoke test。

## 6. Cloud 资源已经删除

已执行：

```powershell
sqd remove `
  --org supreme `
  --name bsc-usdt-smoke `
  --slot v1 `
  --force
```

CLI：

```text
Deleting the squid... √
Done! √
```

并再次执行：

```powershell
sqd list --org supreme
```

Codex 下一阶段第一次调用 Cloud Runtime 前，可再次验证：

```powershell
sqd list --org supreme
```

但不要无意义重新部署。

## 7. 尚未修复的生产问题

冒烟版存在：

```text
isHead == true
→ setForceFlush(true)
```

导致进入链头以后产生大量单块小 Parquet。

正式生产必须改为按文件大小、时间或区块阈值 Flush。

建议：

```text
target parquet file = 64~256 MB
force flush interval = 10~30 min
```

禁止生产版继续“每块 Flush”。

## 8. 下一阶段真实目标

不要把 SQD Cloud 改成默认下载器。

新的目标是：

```text
LOCAL_DATASET
→ Browser / CSV / Direct Download
→ SQD Public
→ RPC
→ AWS
→ Retry / Backoff / Circuit Breaker
→ 所有 Normal Provider Exhausted
→ SQD_CLOUD_EXPORT
```

SQD Cloud 的角色：

```text
FINAL FALLBACK
LAST RESORT PROVIDER
EMERGENCY CAPACITY
```

不是：

```text
DEFAULT PROVIDER
PRIMARY PROVIDER
ALWAYS-ON BACKEND
```

## 9. Codex 下一步应直接读取

主实施文档：

```text
Smart_Download_Orchestrator_V3_SQD_Cloud最终兜底Provider接入设计.md
```

不要继续执行旧的 SQD Cloud CLI Authentication / Cloud Smoke Deployment。

## 10. 第一阶段生产接入范围

第一阶段只实现：

```text
BSC
Token Transfer
```

完整闭环：

```text
Data Requirement
→ Dataset Coverage
→ Normal Provider Router
→ Normal Provider Retry/Fallback
→ All Normal Providers Exhausted
→ Cloud Admission Gate
→ SQD Cloud Warm Worker
→ BSC Portal
→ R2/S3 Parquet
→ Local Sync
→ Validator
→ DuckDB
→ Investigation
→ Graph
```

Transactions、Trace、NFT、Balance、Token Metadata 只保留接口，不在第一阶段同时扩展。

## 11. Codex 必须实现的模块

```text
internal/downloadorchestrator/
internal/providerhealth/
internal/providers/sqdcloud/
internal/cloudruntime/
internal/datasetregistry/
internal/datasetsync/
internal/datasetvalidator/
```

## 12. Provider Tier

必须实现：

```text
LOCAL = Tier 0
NORMAL = Tier 10
FALLBACK = Tier 20
SQD_CLOUD_EXPORT = Tier 100
```

路由顺序：

```text
Tier
→ Capability
→ Health
→ Cost
→ Throughput
```

Cloud Health Score 再高，也不允许越过 Normal Provider。

## 13. Cloud Admission Gate

必须满足：

```text
Local Coverage MISS
AND
CloudEligible = true
AND
所有支持当前 Dataset 的 Normal Provider Exhausted
AND
Cloud Budget 未超限
AND
Cloud Runtime 不在 cooldown
```

才允许：

```text
SQD_CLOUD_EXPORT
```

## 14. Normal Provider Exhausted 定义

只有：

```text
CIRCUIT_OPEN
RATE_LIMITED
RISK_CONTROLLED
AUTH_BLOCKED
UNAVAILABLE
UNSUPPORTED
```

才属于无法继续。

单次 503、429、timeout 不得直接触发 Cloud。

## 15. Cloud 只补缺失 Chunk

例如：

```text
总任务：2000 chunks
普通 Provider 已完成：1850
失败：150
```

Cloud 只能执行 150 个 missing chunks，禁止重新执行全部 2000 chunks。

## 16. Warm Worker

流程：

```text
第一条 Emergency Job
→ EnsureWorker
→ ABSENT
→ sqd deploy
→ READY
→ 多个 Emergency Job 共用
```

全部完成：

```text
BUSY
→ IDLE
→ 20 分钟无新任务
→ sqd remove
```

## 17. 单实例 Deploy 锁

多个 Chunk 同时进入 Cloud 时，只允许一个 goroutine/process 发起 deploy，其余进入：

```text
WAITING_CLOUD_WORKER
```

禁止并发 `sqd deploy x N`。

## 18. Cloud Worker 正式改造

当前：

```text
E:\Code\Processor-only
```

需要从固定 smoke worker 改造成 Job-driven Worker。

推荐：

```text
src/
├── main.ts
├── config.ts
├── job-poller.ts
├── job-runner.ts
├── lease.ts
├── portal-source.ts
├── parquet-writer.ts
├── checkpoint.ts
├── manifest.ts
├── object-store.ts
├── health.ts
└── datasets/
    └── token-transfer.ts
```

## 19. 不能每个 Job 修改 squid.yaml

禁止：

```text
修改地址
修改 from_block
修改 to_block
重新 deploy
```

正确模式：

```text
固定 Worker Release
+
动态 Job Request
```

## 20. Cloud 输出必须迁移到 R2/S3

正式版本不得依赖：

```text
./data
```

必须：

```text
SQD Cloud Processor
→ R2 / S3
```

输出：

```text
Parquet
manifest.json
checkpoint.json
_SUCCESS
```

## 21. Checkpoint 必须外部持久化

Cloud Processor 本地 `status.txt` 不能作为正式恢复依据。

正式使用：

```text
R2 checkpoint.json
```

Worker 重启：

```text
读取 Checkpoint
→ 跳过已完成 Chunk
→ 继续
```

## 22. Dataset Coverage

必须先查：

```text
requested range - existing coverage
```

Cloud 不能下载已有数据。

建议数据库：

```text
dataset_registry
dataset_chunk_coverage
provider_attempts
cloud_usage_audit
```

## 23. Investigation 与 Graph

禁止：

```text
Investigation → SQD Cloud
Graph → SQD Cloud
```

必须：

```text
Investigation / Graph
→ Data Requirement
→ Smart Download Orchestrator
```

Graph Expand 也必须先 Local / Normal Provider，再在全部失败后 Cloud。

## 24. Normal Provider 恢复后自动回切

Cloud 运行期间，如果 SQD Public / RPC / AWS 恢复：

```text
当前 Cloud Chunk 自然完成
新 Chunk 自动返回 Normal Provider
```

禁止强杀正在输出 Parquet 的 Cloud Chunk。

## 25. Cloud 使用审计

每次启用 Cloud 必须记录：

```text
启动原因
Normal Provider 状态
Cloud started_at
Cloud stopped_at
runtime
chunks
rows
bytes
```

长期指标：

```text
cloud_fallback_ratio
```

目标：尽可能低。

## 26. 下一阶段验收

必须证明：

```text
Normal Provider Healthy
→ Cloud calls = 0
```

```text
SQD Public 503
RPC Healthy
→ RPC
→ Cloud calls = 0
```

```text
Public Open
RPC Rate Limited
AWS Healthy
→ AWS
→ Cloud calls = 0
```

```text
All Normal Providers Exhausted
→ Cloud 自动启动
```

```text
Normal Provider Recovery
→ 新 Chunk 回切
```

```text
Cloud Idle 20m
→ 自动删除
```

## 27. 不要用真实攻击方式测试风控

429 / 403 / 503 优先使用 Fault Injection，不要疯狂请求真实服务。

## 28. Codex 当前执行指令

```text
Cloud Processor-only 的本地与云端冒烟验证已经完成。

不要再进行 Deployment Key、Org Code、small Processor smoke test。

当前 Organization = supreme。
此前成功部署并删除 supreme/bsc-usdt-smoke@v1。

现在进入生产接入阶段。

请严格按照
Smart_Download_Orchestrator_V3_SQD_Cloud最终兜底Provider接入设计.md
实施。

SQD Cloud 必须是 Tier 100 Emergency Provider。

LOCAL、Browser、SQD Public、RPC、AWS 等正常 Provider 全部优先。

单次 429、503、timeout 不得触发 Cloud。

只有所有 Normal Provider Exhausted 后，
CloudAdmissionGate 才允许启动 SQD_CLOUD_EXPORT。

第一阶段只做 BSC Token Transfer。

优先实施：
1. Provider Tier
2. Provider State / Health
3. Circuit Breaker
4. AllNormalProvidersExhausted
5. CloudAdmissionGate
6. CloudRuntimeManager
7. Warm Worker
8. R2/S3
9. Job-driven Cloud Worker
10. Checkpoint / Manifest
11. Local Sync
12. Dataset Coverage
13. DuckDB
14. Investigation / Graph 联动
15. Provider Recovery 回切
16. Idle Remove
17. Fault Injection
18. Integration Test

正式 Worker 必须修复链头逐块 Force Flush，
不得继续使用 Cloud 临时 ./data 作为正式数据资产。

完成后输出 IMPLEMENTATION_REPORT.md。
```

## 29. 当前项目状态

```text
基础 SQD Cloud 可行性：PASS
本地 Parquet：PASS
断点恢复：PASS
Cloud Build：PASS
Cloud Portal：PASS
Cloud Runtime：PASS
Cloud Remove：PASS

下一阶段：
生产级 Emergency Provider Integration
```
