# SQD Cloud Phase 5.2：25 地址 × 50,000 Blocks 生产化验收与故障恢复

> 基线：Phase 5.1 真实 Cloud Canary 已 PASS。
> 
> 目标：保持 SQD Cloud = Tier 100 / Last Resort，不改变正常 Provider 优先级；扩大到 25 地址 × 50,000 blocks，并补齐 Lease 过期恢复、Cancel Marker、Worker Crash Recovery、严格区块边界、Local Sync 失败恢复。

## 0. Phase 5.2 前置 P0

### P0-1 隔离修复前越界 Chunk
Phase 5.1 已确认旧 Chunk：
- manifest.to_block = 114474500
- parquet max_block = 114475243

处理要求：
1. 原始 R2 / 本地证据保留；
2. Registry 标记 `QUARANTINED` / `INVALID_RANGE_LEGACY`；
3. active merged.parquet 重建时排除；
4. Coverage 排除；
5. 对外查询不返回越界数据；
6. 审计仍可追溯。

### P0-2 修复普通 SQD Provider from/to 透传
确保：
`DataRequirement.from_block/to_block → Planner → Chunk → Normal SQD Provider → 实际 Portal 查询`
完全一致。

新增测试：
`114474000-114474500 → provider request 仍为 114474000-114474500`

### P0-3 明确 completed 后 leased 语义
Job 进入 completed 后：
- active lease 删除或 tombstone；
- Runtime 不再识别为 running；
- Reconcile 不重复消费；
- completed `_SUCCESS` 作为最终幂等判据。

## 1. 主测试规模

```text
Chain: BSC
Dataset: token_transfer
Addresses: 25
Block Range: 50,000 blocks
Cloud Eligible: true
```

地址应覆盖：
- 高频地址
- 中低频地址
- 可能 0-row 地址

## 2. Cloud 主路径

```text
Coverage MISS
→ Normal Providers Exhausted
→ Cloud Admission
→ EnsureWorker
→ pending
→ leased
→ running
→ completed
→ Local Sync
→ Validation
→ Registry
→ Coverage HIT
```

记录：
`plan_id/job_id/chunk_id/deploy_ms/queue_wait_ms/processor_ms/r2_upload_ms/remote_rows/bytes/sync_ms/local_rows/dup/min_block/max_block`

## 3. 严格区块边界

硬约束：
```text
min_block >= from_block
max_block <= to_block
```

Validator 新增：
`OUT_OF_RANGE_ROWS > 0 → LOCAL_VALIDATION_FAILED`

失败时：
- 不登记 active Registry
- 不更新 Coverage
- 不进入 active merged

## 4. Lease Heartbeat

真实观察至少 3 次续租：
```text
t0
t0+30s
t0+60s
t0+90s
```

验收：
- expires_at 单调后移
- Runtime 始终 REMOTE_RUNNING
- Idle Reaper 不误删
- 同 job 不被重复抢占

## 5. Lease 过期恢复

通过故障注入停止 heartbeat：

```text
RUNNING
→ heartbeat stop
→ lease expired
→ REMOTE_LEASE_EXPIRED
→ requeue same job_id
→ new lease
→ checkpoint resume
→ completed
```

要求：
- 同一 job_id
- 从 checkpoint 恢复
- unique dup=0
- 不生成双 completed
- 不重复 Registry entry

## 6. Cancel Marker

建议协议：
`bsc/jobs/cancel/<job_id>.json`

流程：
```text
RUNNING
→ CANCEL_REQUESTED
→ 完成当前安全写入点
→ 写 checkpoint
→ REMOTE_CANCELLED
```

要求：
- 不写 `_SUCCESS`
- 不标 completed
- partial data 不进入 Registry
- Resume 可从 checkpoint 继续

## 7. Worker Crash / Restart Recovery

受控 kill 一次 Worker：

```text
crash
→ Reconcile
→ lease expires / worker redeploy
→ checkpoint resume
→ completed
```

检查：
- single deploy lock
- 不从 from_block 全量重跑
- checkpoint 可读
- 最终 manifest 唯一

## 8. R2 Job Protocol

终态只能是：
```text
completed
failed
cancelled
```

如果 completed `_SUCCESS` 存在：
- ignore stale pending
- 清理/tombstone active lease
- status = REMOTE_COMPLETED

## 9. Local Sync 失败恢复

故障注入第一次 Sync 失败：

```text
REMOTE_COMPLETED
→ LOCAL_SYNC_PENDING
→ LOCAL_SYNC_FAILED
→ retry sync
→ LOCAL_INDEXED
```

必须确认：
- Cloud Processor 不重新抓数据
- R2 parquet 不重新生成
- remote sha256/row_count 不变

## 10. Validator

至少检查：
- schema
- row_count
- sha256
- unique key
- dup_count
- min/max block
- address coverage
- dataset
- chain_id
- manifest schema_version
- range_violation_count
- unexpected_address_count
- manifest_file_missing_count

## 11. Registry / merged

仅 ACTIVE + VALID entries 参与 Merge。

排除：
`QUARANTINED / INVALID / FAILED / CANCELLED`

要求：
`merged rows == distinct unique keys`，dup=0。

## 12. Coverage

25 地址分别验证：
`MISS/PARTIAL → HIT`

不得由旧越界、cancelled、invalid、sync failed chunk 错误贡献 Coverage。

## 13. Provider Recovery

解除 Fault Injection，再提交未覆盖窗口。

预期：
```text
Normal Provider HEALTHY
→ 新 Chunk 回 Normal Provider
→ Cloud 不接新 Chunk
```

同时验证 Normal SQD Provider 的 from/to 精确透传。

## 14. Idle Remove

无 pending/leased/running 后 20 分钟：

```text
IDLE
→ DRAINING
→ REMOVING
→ ABSENT
```

并用：
`sqd list --org supreme`
确认无残留 Worker。

## 15. 成本与审计

记录：
- cloud_jobs_total
- cloud_rows_total
- cloud_bytes_total
- processor_runtime_seconds
- worker_alive_seconds
- cloud_fallback_ratio
- estimated_cost

核心原则：
`cloud_fallback_ratio` 应长期维持低值，Cloud 不能因“稳定”被自动提升优先级。

## 16. 安全

禁止：
- 日志打印 SQD_DEPLOY_KEY
- 日志打印 R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY
- API 返回 Secret
- 前端显示 Secret
- Git 存 Secret

## 17. 自动化测试

至少：
```text
go test ./... -short -count=1
go vet ./...
go build
npm run build
```

新增测试：
- lease expiry
- checkpoint resume
- cancel marker
- strict block range
- quarantined entry excluded from merge
- normal provider from/to passthrough
- remote completed + local sync retry
- completed ignores stale pending/lease

## 18. Phase 5.2 PASS Gate

- [ ] 旧越界 entry 已隔离
- [ ] Normal SQD Provider from/to 已修复
- [ ] 25 地址 × 50,000 blocks Cloud 完成
- [ ] strict range PASS
- [ ] heartbeat PASS
- [ ] lease expiry → resume PASS
- [ ] cancel marker PASS
- [ ] worker crash recovery PASS
- [ ] completed 后无 active lease 冲突
- [ ] R2 manifest/SHA256/_SUCCESS PASS
- [ ] Local Sync failure 不触发 Cloud 重抓
- [ ] Validator PASS
- [ ] Registry PASS
- [ ] merged dup=0
- [ ] Coverage 精确
- [ ] Provider 恢复后 Cloud 退出
- [ ] Idle Remove PASS
- [ ] Secret Audit PASS
- [ ] cloud_fallback_ratio 可观测

## 19. Phase 5.2 之后

进入 Phase 5.3：

```text
Investigation DataRequirement
→ Orchestrator
→ Coverage MISS
→ Normal Provider / Last Resort Cloud
→ DATASET_INDEXED
→ Investigation 自动恢复
→ Graph incremental update
```

Phase 5.3 的重点不是继续扩大下载，而是让 Investigation / Graph 真正成为 Cloud-aware 消费者，且永远不直接调用 SQD。
