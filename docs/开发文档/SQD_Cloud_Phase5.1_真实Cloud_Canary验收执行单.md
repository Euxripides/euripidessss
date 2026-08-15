# SQD Cloud Phase 5.1：真实 Cloud Canary 验收执行单

> 目标：在 R2 Canary 已全部 PASS 的基础上，首次启用 `SQD_CLOUD_MODE=cloud`，验证真实 SQD Cloud Worker → R2 → Local Sync → Dataset Registry 的完整闭环。
>
> 原则：**只做小规模 Canary，不跑大任务；SQD Cloud 仍保持 Tier 100 / Last Resort，仅在正常 Provider 全部耗尽后启用。**

## 1. 切换到 Cloud Mode

```powershell
[Environment]::SetEnvironmentVariable("SQD_CLOUD_MODE","cloud","User")
[Environment]::SetEnvironmentVariable("SQD_CLOUD_ORG","supreme","User")
```

关闭旧 PowerShell / Codex / ETL Server，重新打开，使新进程继承环境变量。

检查以下变量仅“存在”，不要打印值：

```text
SQD_CLOUD_MODE
SQD_CLOUD_ORG
SQD_DEPLOY_KEY
R2_ENDPOINT
R2_BUCKET
R2_REGION
R2_ACCESS_KEY_ID
R2_SECRET_ACCESS_KEY
```

预期：

```text
SQD_CLOUD_MODE=cloud
SQD_CLOUD_ORG=supreme
R2_BUCKET=pangu-sqd-cloud
```

## 2. 启动服务但不要立即提交 Cloud Job

```powershell
Set-Location "E:\codex\etl"
.\run.ps1
```

检查：

```text
GET /api/health
GET /api/scheduler/cloud/runtime
GET /api/scheduler/providers/health
GET /api/scheduler/cloud/jobs
GET /api/scheduler/cloud/usage
```

验收：

- `/api/health` = 200
- runtime.mode = `cloud`
- deployment_key_configured = true
- r2_configured = true
- `sqd_cloud` 仍为 Tier 100
- 若无 Worker，状态为 `ABSENT`
- 启动服务本身不得自动部署 Cloud Worker

## 3. SQD CLI 只读认证检查

```powershell
sqd list --org supreme
```

要求命令成功；若无 Worker 应为空或无目标 worker；发现历史 Worker 时由 Reconcile 接管，不重复 deploy。

## 4. 第一条真实 Cloud Canary

规模保持很小：

```text
Chain: BSC
Dataset: token_transfer
Addresses: 1～3
Block range: 500～5,000 blocks
Cloud eligibility: true
```

优先用已知确实有 Transfer 的测试地址和历史窗口。

## 5. 通过 Fault Injection 验证 Last Resort

不要真实攻击或轰炸 Provider。通过测试/故障注入把普通 Provider 模拟为 exhausted。

预期：

```text
DataRequirement
→ Local Coverage MISS
→ Normal Providers exhausted
→ Cloud Admission
→ reason = ALL_NORMAL_PROVIDERS_EXHAUSTED
→ SQD_CLOUD_EXPORT
```

必须确认：

- 单次 429 / 503 / timeout 不直接触发 Cloud
- 还有任一普通 Provider 可用时 Cloud 不进入竞速
- Cloud 只补 missing chunks
- Cloud 仍固定 Tier 100

## 6. EnsureWorker / Deploy

预期状态：

```text
ABSENT
→ DEPLOYING
→ READY/IDLE
→ BUSY
```

检查 single deploy lock、固定 Worker 身份、失败 cooldown、不得重复 deploy。

## 7. Job Queue

R2 应出现：

```text
bsc/jobs/pending/
→ bsc/jobs/leased/
→ bsc/jobs/completed/
```

失败则：

```text
bsc/jobs/failed/
```

检查 lease TTL、heartbeat、幂等、checkpoint。

## 8. 真实 SQD Cloud Processor

Worker：

```text
读取 R2 Job
→ Processor
→ BSC Portal
→ WATCH_ADDRESSES
→ FROM_BLOCK → TO_BLOCK
→ Parquet
```

检查真实数据流、TO_BLOCK 后结束、不产生大量 tiny parquet。

## 9. Cloud Export 到 R2

每个 chunk：

```text
data/*.parquet
checkpoint/progress.json
manifest.json
_SUCCESS
```

`_SUCCESS` 必须最后写。

状态区分：

```text
REMOTE_COMPLETED
LOCAL_SYNC_PENDING
LOCAL_SYNCING
LOCAL_INDEXED
```

Local Sync 失败不得重新跑 Cloud fetch。

## 10. Local Sync

```text
R2
→ .partial
→ SHA256
→ Parquet Validator
→ DuckDB Validation
→ Dataset Registry
→ Coverage Index
```

检查 row_count、schema、unique key、dup、min/max block、0-row chunk、Coverage MISS → HIT。

## 11. Canary 完整闭环

必须证明：

```text
Normal Provider Exhausted
→ Cloud Admission
→ EnsureWorker
→ SQD Cloud Worker
→ BSC Portal
→ R2
→ Local Sync
→ DuckDB
→ Dataset Registry
→ Coverage HIT
```

建议记录 job_id、chunk_id、deploy/processor/sync 耗时、row_count、bytes、validation、cost estimate。

## 12. 普通 Provider 恢复

解除 Fault Injection 后提交新 DataRequirement。

预期：

```text
Normal Provider HEALTHY
→ 新 Chunk 回普通 Provider
→ Cloud 不接新 Chunk
→ Cloud IDLE
```

已 leased 的 Cloud chunk 自然完成，不硬杀。

## 13. Idle Remove

无 pending / leased / running 后进入 idle timer。约 20 分钟后预期：

```text
READY/IDLE
→ DRAINING
→ REMOVING
→ ABSENT
```

用：

```powershell
sqd list --org supreme
```

确认 Worker 已移除。

## 14. Phase 5.1 PASS Gate

- [ ] R2 Canary PASS
- [ ] Cloud mode 生效
- [ ] Reconcile PASS
- [ ] Cloud 启动前 ABSENT
- [ ] Normal Provider 可用时不触发 Cloud
- [ ] 全部 Normal Provider exhausted 后才 Admission
- [ ] single deploy lock PASS
- [ ] Cloud Worker 成功部署
- [ ] pending → leased → completed
- [ ] 真实 BSC Portal 数据
- [ ] Parquet 写入 R2
- [ ] manifest / SHA256 / `_SUCCESS` PASS
- [ ] Local Sync PASS
- [ ] DuckDB PASS
- [ ] Dataset Registry PASS
- [ ] Coverage MISS → HIT
- [ ] Provider 恢复后新 Chunk 回普通 Provider
- [ ] Idle 20 分钟自动 Remove
- [ ] 无 Secret 泄漏

## 15. 后续顺序

```text
Phase 5.1：1～3 地址 / 500～5,000 blocks
→ Phase 5.2：25 地址 × 50,000 blocks
→ Phase 5.3：Investigation 自动等待/恢复 + Graph 增量更新
→ Phase 5.4：1,000～10,000 地址压力验证
→ 50K / 100K 生产级验证
```

SQD Cloud 全程保持：

```text
Tier 100
Emergency Provider
Last Resort
```

不得升级为默认 Provider。
