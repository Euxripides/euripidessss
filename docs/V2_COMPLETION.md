# V2 下载引擎 — 完成说明（详细到代码，含用户反馈补充）

> 日期: 2026-07-30  
> 基准: PRD V2.0 企业级区块链数据下载引擎  
> 策略: 渐进重构 — `internal/parquetdownload` 阶段1-6保持不动  
> **验收: ✅ 已通过 — V2 核心架构阶段完成** (2026-07-30)
> 下一阶段: 真实数据闭环（禁止继续扩展架构）

---

## 目录结构

```
internal/downloadengine/
├── types.go              (228行) 领域模型全量类型
├── job.go                (116行) Job 双层状态机
├── provider.go           (103行) Provider 能力接口
├── router.go             (131行) Provider Router
├── planner.go            (313行) Discovery + Range + 地址分组
├── executor.go           (+120行) Chunk Executor + RateLimiter + ErrorClassifier + RetryBudget + FailoverBudget
├── manifest.go           (+100行) Manifest Finalizer + CompletionGate + Dataset Registry(UUID+fingerprint) + FeatureFlag
├── migration.go          (154行) Migration Framework (Runner + Rollback增量保存 + 4个V2内置迁移)
├── api_handler.go        ( 55行) REST API: /health + /schema-version(GET/POST)
├── job_test.go           (119行) 状态机 7 测试
├── router_test.go        (113行) Router 4 测试
├── planner_test.go       (199行) Planner 8 测试
├── integration_test.go   (191行) 集成 9 测试
├── migration_test.go     (166行) Migration 6 测试

frontend/src/features/crypto/
├── AddressAnalyticsPanel.tsx  (含 Switch + DatePicker + fetchFirstSeen + submitDisabled)
├── addressAnalyticsApi.ts     (loadFirstSeen + AddressQueryParams + 全部函数重构)
├── cryptoAddressApi.ts        (零宽字符过滤 regex)
├── CryptoParquetPanel.tsx     (日期选择器 + 阶段标签)
├── CryptoDownloadPanel.tsx    (地址解析 parseAddressChains)
├── DataSourcePage.tsx         (notification.info "数据覆盖尚未完整")
```

---

## 一、types.go — 领域模型 (228行)

### 1.1 Job 类型（10种）
```go
JobAddressSingle, JobAddressBatch, JobToken, JobContract,
JobNFT, JobDatasetRange, JobFullHistory,
JobIncrementalSync, JobRepair, JobReindex
```

### 1.2 双层状态模型 — PRD §18 对齐

**JobStatus**（生命周期，10态）— **只有 `Transition()` 可修改，禁止其他模块直接改字段**：
```
CREATED → VALIDATING → QUEUED → RUNNING
                            ↘ CANCELING → CANCELLED
RUNNING → PAUSING → PAUSED → RUNNING
RUNNING → COMPLETED / FAILED
终态(COMPLETED/CANCELLED/FAILED) → {}
```

**JobStage**（处理阶段，10阶段）— `SetStage()` 修改，与 Status 正交：
```
IDLE → DISCOVERING → RESOLVING_RANGE → PLANNING
→ AWAITING_SCHEDULE → DOWNLOADING → WRITING
→ INDEXING → VALIDATING_OUTPUT → FINALIZING
```

### 1.3 枚举摘要

| 枚举 | 值 | 数量 |
|------|-----|------|
| JobType | ADDRESS_SINGLE..REINDEX | 10 |
| JobStatus | CREATED..FAILED | 10 |
| JobStage | IDLE..FINALIZING | 10 |
| RangeMode | AUTO_FIRST_SEEN..INCREMENTAL | 6 |
| Priority | CRITICAL(0)..BACKGROUND(4) | 5 |
| ErrorCode | INVALID_REQUEST..DATE_RANGE_INVALID | 22 |
| ChunkStatus | PENDING..CANCELLED | 8 |
| ProviderHealthStatus | HEALTHY..DISABLED | 7 |

---

## 二、job.go — State Machine (116行)

### Transition() 唯一入口 — 7 测试覆盖
```go
var validTransitions = map[JobStatus][]JobStatus{
    StatusCreated:    {StatusValidating},
    StatusValidating: {StatusQueued, StatusFailed},
    StatusQueued:     {StatusRunning, StatusCanceling, StatusFailed},
    StatusRunning:    {StatusPausing, StatusCanceling, StatusCompleted, StatusFailed},
    StatusPausing:    {StatusPaused, StatusFailed},
    StatusPaused:     {StatusRunning, StatusCanceling},
    StatusCanceling:  {StatusCancelled, StatusFailed},
    StatusCompleted:  {},  // 终态 — 不可再转换
    StatusCancelled:  {},  // 终态
    StatusFailed:     {},  // 终态
}

func (j *Job) Transition(target JobStatus) error {
    j.mu.Lock()
    defer j.mu.Unlock()
    if j.Status == target { return nil }          // 幂等
    allowed, ok := validTransitions[j.Status]
    if !ok { return &InvalidTransitionError{...} } // 未知源
    if !contains(allowed, target) { ... }          // 非法转换
    j.Status = target; j.UpdatedAt = time.Now().UTC()
    if isTerminal(target) { j.FinishedAt = &now }  // 自动设完成时间
    return nil
}
```

**测试**: `TestTransitionLegalPath`(合法路径) / `TestTransitionIllegal`(CREATED→RUNNING拒绝) / `TestTransitionIdempotent`(重复幂等) / `TestTransitionFromTerminal`(终态阻塞) / `TestConcurrentTransition`(并发最终一致) / `TestSetStage`(Stage独立) / `TestStatusConstants`(无重复)

---

## 三、provider.go + router.go — Provider 层 (234行)

### 能力接口 — 组合优于继承
```go
type Provider interface { Name(); Capabilities(); Health(ctx) }

type StreamingProvider interface {               // SQD 流式
    Provider
    Estimate(StreamEstimateRequest) (*EstimateResult, error)
    ExecuteStream(StreamRequest) (<-chan StreamRecord, <-chan error)
}

type ObjectProvider interface {                  // AWS 对象存储
    Provider
    Estimate(ObjectEstimateRequest) (*EstimateResult, error)
    ExecuteObject(ObjectRequest) (*ObjectResult, error)
}

type LookupProvider interface {                  // RPC 单点
    Provider
    ExecuteLookup(LookupRequest) (*LookupResult, error)
}
```

### Router — CapabilityCache + HealthCache (30s TTL)
```go
type Router struct {
    mu              sync.RWMutex
    streaming       []StreamingProvider
    object          []ObjectProvider
    lookup          []LookupProvider
    capabilityCache map[string]ProviderCapabilities   // 注册时填充
    healthCache     map[string]ProviderHealth         // UpdateHealth() 刷新
    cacheExpiry     time.Time                         // 30s
}

RegisterStreaming(p)   // + 自动缓存 Capabilities
ResolveStreaming(datasetType, chainID) → (StreamingProvider, bool)
// 路由规则: 遍历 → isHealthy(未缓存乐观通过) → supportsDataset
```

**测试**: `TestRouterResolveStreaming` / `TestRouterResolveFailsWhenUnhealthy` / `TestRouterResolveObject` / `TestRouterCapabilitiesCache`

---

## 四、planner.go — 规划引擎 (313行)

### Discovery Engine
```go
type FirstSeenResolver interface {
    ResolveFirstSeen(ctx, chainID, address) (*AddressDiscovery, error)
}

func (e *DiscoveryEngine) Discover(ctx, chainID string, addresses []string) *DiscoveryResult {
    // 逐地址解析，err → FSFailed，不因单个失败而整体失败
    // 聚合: Total/Found/Partial/NotFound/TemporarilyUnavailable/Failed
}
```

### Range Planner — 6 种模式
```go
planAutoFirstSeen  → 取所有成功地址的最小 first_seen_block
                    任一 FSPartial → Coverage=PARTIAL
planBlock          → start/end 直接使用，start≥end 拒绝
planTime           → start_time/end_time 必填校验
planFullHistory    → genesis(block=0) → EndBlockValue()
planResume         → LastSyncBlock+1 → EndBlockValue()
planIncremental    → 同 Resume, source=INCREMENTAL
```

### Address Group Planner — 3 级
```
≤100 地址  → singleGroup()        单组合并
101~5000   → blockLayerGroups()   按首次区块排序切片
5000+      → hashBucketGroups()   FNV hash % 16 分桶，超限再拆
```

**测试**: `TestPlanAutoFirstSeen` / `TestPlanBlockRange` / `TestPlanBlockRangeInvalidatesWhenStartGEQEnd` / `TestPlanAutoFirstSeenAllNotFound` / `TestPlanAutoFirstSeenPartial` / `TestPlanGroupsSingleBucket` / `TestPlanGroupsHashBucket` / `TestDiscoveryResultCounts`

---

## 五、executor.go — 执行层

### Chunk Executor
```go
func (x *Executor) ExecuteChunk(ctx, chunk) error {
    // maxRetries=3, backoffBase=5s, backoffMax=300s
    // 指数退避: 5s → 10s → 20s → 300s(max)
    for attempt := 1; attempt <= x.maxRetries+1; attempt++ {
        err := x.tryExecute(ctx, chunk)
        if err == nil { chunk.Status = ChunkSucceeded; return nil }
        if attempt <= x.maxRetries {
            backoff := math.Min(base*2^(attempt-1), max)
            chunk.Status = ChunkRetryWait
            select { case <-ctx.Done(): chunk.Status=ChunkCancelled; return ctx.Err()
                     case <-time.After(backoff): }
        }
    }
    chunk.Status = ChunkFailed
}
```

### RateLimiter — 安全审查已通过
```go
func (r *RateLimiter) Acquire(ctx) error  { select { case sem←{}: concurrent++; return nil; case ←ctx.Done(): } }
func (r *RateLimiter) Release()           { mu.Lock(); if concurrent>0 { concurrent-- }; mu.Unlock();
                                            select { case <-sem: default: } } // 非阻塞防double-release
```

### ErrorClassifier — 组合词匹配（审查修复后）
```go
func (c *ErrorClassifier) Classify(err error) ErrorCode {
    switch {
    case containsPattern(msg, "503"), containsPattern(msg, "no available workers"):
        return ErrSQDNoWorkers           // "disk space" vs 旧"disk"+"space" 防 false positive
    case containsPattern(msg, "parquet write"), containsPattern(msg, "write parquet"):
        return ErrParquetWriteFailed     // 旧"parquet"+"write" 误匹配任意"write"
    // ... 10 个分支全部使用组合词 containsPattern
    }
}
// containsPattern(s, pattern) = strings.Contains(lower(s), lower(pattern))
```

### RetryBudget + FailoverBudget
```go
type RetryBudget struct { perChunkMax, perProviderMax int; providerRetries map[string]int }
AllowChunkRetry(chunkID, attempt) bool     // attempt ≤ perChunkMax
AllowProviderRetry(provider) bool          // 检查+递增 providerRetries[provider]
ResetProvider(provider)                    // 成功后重置

type FailoverBudget struct { maxFailovers int; failovers map[string]int }
AllowFailover(chunkID) bool               // failovers[chunkID] < maxFailovers
```

**测试**: `TestRateLimiter` / `TestRateLimiterBlocksOnFull`

---

## 六、manifest.go — 数据落地

### Manifest V2 Finalizer — 原子 rename
```go
func (f *ManifestFinalizer) Finalize(jobID string, manifest *ManifestV2) error {
    mu.Lock(); defer mu.Unlock()
    manifestPath := filepath.Join(storeDir, jobID+"-manifest.json")
    tmpPath := manifestPath + ".tmp"
    json.MarshalIndent(manifest, "", "  ") → os.WriteFile(tmpPath, 0644)
    → os.Rename(tmpPath, manifestPath)  // 原子替换
}
```

### Completion Gate — 5 项检查全过才能 COMPLETED
```go
func (g *CompletionGate) Verify(job, chunks, manifestWritten, indexed, validated) error {
    // 1. 所有 Chunk SUCCEEDED 或 SKIPPED
    // 2. Manifest 已写入
    // 3. DuckDB 已索引
    // 4. 验证通过
    if failures > 0 { return ErrValidationFailed }
}
```

### Dataset Registry — UUID + fingerprint 双索引（用户反馈修复后）
```go
type DatasetRecord struct {
    DatasetID   string   `json:"dataset_id"`   // UUID，主键
    Fingerprint string   `json:"fingerprint"`  // SHA256(chain:type:start:end)[:16]
    JobID, ChainID, DatasetType string
    StartBlock, EndBlock uint64
    Checksum             string
}

type DatasetRegistry struct {
    byID map[string]*DatasetRecord  // UUID → record
    byFP map[string]*DatasetRecord  // fingerprint → record（同指纹查重+幂等）
}

func (r *DatasetRegistry) Register(rec) error {
    // 自动计算 fingerprint: SHA256(fmt.Sprintf("%s:%s:%d:%d", chain,type,start,end))[:16]
    // 同 fingerprint + 同 checksum → 幂等跳过
    // 同 fingerprint + 不同 checksum → 更新 FilePath
    // 同 UUID → 更新 fingerprint/checksum，清理旧 fingerprint 映射
}
// computeFingerprint = SHA256(chain:type:start:end) 前16字符
```

### Feature Flag — 灰度策略
```go
DefaultFeatureFlags() → { V2=false, chains=["bsc"], AutoFirstSeen=true }
灰度路径: BSC单地址 → eth → base → arbitrum
```

**测试**: `TestManifestFinalizerAtomic` / `TestCompletionGateBlocksOnFailedChunk` / `TestCompletionGatePasses` / `TestDatasetRegistryCRUD` / `TestDatasetRegistryIdempotent` / `TestFeatureFlagsDefault` / `TestFeatureFlagsGrayStrategy`

---

## 七、migration.go — Migration Framework (154行)

### 架构约束 — PRD §1B 对齐
- **业务代码禁止零散执行 CREATE TABLE / ALTER TABLE**
- 所有 DDL 通过 MigrationRunner 管理
- 版本号递增，Register 拒绝非递增版本
- Run 幂等（已执行跳过）
- Rollback 逐步保存（每步成功后 saveState，失败时保存已回滚版本）

### Runner
```go
type Migration struct { Version int; Name, Description, UpSQL, DownSQL string }

func (r *MigrationRunner) Register(m Migration) error   // 版本递增校验
func (r *MigrationRunner) Run() error                    // 所有未应用迁移 → execFn(UpSQL) → saveState
func (r *MigrationRunner) Rollback(targetVersion) error  // 降序执行 DownSQL → 逐步保存 CurrentVersion
func (r *MigrationRunner) CurrentVersion() int           // 从 schema_version.json 读取
```

### Rollback 增量保存（审查阻断修复后）
```go
for _, m := range migrations {  // 降序
    if err := r.execFn(m.DownSQL); err != nil {
        r.saveState(state)  // 保存已回滚到的版本，下次 Run 可从此恢复
        return fmt.Errorf("回滚迁移 %d 失败 (已回滚至版本 %d): %w", ...)
    }
    // 逐步保存，防止部分失败后状态不一致
    state.CurrentVersion = m.Version - 1
    r.saveState(state)
}
```

### V2 内置迁移（4个）
```sql
-- v1: address_first_seen (chain_id+address PK, first_seen_block, first_seen_time, first_seen_source, coverage_status, query_status)
-- v2: download_jobs     (job_id PK, job_type, chain_id, status/stage, use_first_seen, effective_start/end, start_time_source)
-- v3: download_chunks   (chunk_id PK, job_id, dataset_type, start/end_block, attempt, status, rows/bytes, checksum)
-- v4: download_checkpoints (job_id PK, completed_chunks, failed_chunks, last_success_block, manifest_version)
```

**测试**: `TestMigrationRunnerRunAll` / `TestMigrationRunnerIdempotent` / `TestMigrationRunnerStatePersists` / `TestMigrationRunnerRejectsNonIncremental` / `TestMigrationRunnerRollback` / `TestMigrationRunnerSchemaVersionJSON`

---

## 八、api_handler.go — REST API (55行)

```go
GET  /health          → {"status":"ok", "engine":"downloadengine-v2", "schema_version": N}
GET  /schema-version  → {"schema_version": N}
POST /schema-version  → runner.Run() → {"schema_version": N, "status":"migrated"}
```

---

## 九、前端（V1.5.0 已实现，本次未修改）

| 文件 | 功能 |
|------|------|
| `AddressAnalyticsPanel.tsx` | Switch "从地址首次出现开始" + DatePicker(showTime) + fetchFirstSeen(链切换自动重查) + submitDisabled(loading/not_found/unavailable → 禁止提交) |
| `addressAnalyticsApi.ts` | `loadFirstSeen(chain,addr)` → `GET /api/crypto/addresses/{chain}/{addr}/first-seen`; `AddressQueryParams {use_first_seen, start_time, end_time}`; 全部 `loadAddress*` 函数重构 |
| `CryptoParquetPanel.tsx` | 日期 RangePicker + 阶段标签 |
| `cryptoAddressApi.ts` | 零宽字符过滤 `[\u200B\u200C\u200D\u200E\u200F\uFEFF\u00A0\u2028\u2029]` |
| `DataSourcePage.tsx` | notification.info "数据覆盖尚未完整" 替换内联 Alert |

---

## 十、旁系修复

| 文件 | 修复 |
|------|------|
| `internal/parquetdownload/addresses.go` | `normalizeAddresses` 新增零宽字符过滤 `zeroWidthChars(rune)rune` |
| `internal/parquetdownload/firstseen.go` | `queryFirstSeen` + DuckDB 缓存 + EOA/Contract 区分 + `FirstSeenResponse` |
| `internal/api/crypto_parquet_handlers.go` | `HandleFirstSeen` 反向代理 |
| `internal/api/handlers.go` | 注册 `/api/crypto/addresses/:chain/:address/first-seen` |

---
## 新增：真实闭环 Provider 适配器 + E2E

### SQDAdapter (`provider/sqd_adapter.go`)
```go
// sqd.Client callback-based → channel-based StreamingProvider
type SQDAdapter struct { client *sqd.Client }
func (s *SQDAdapter) ExecuteStream(ctx, req) (<-chan StreamRecord, <-chan error) {
    // logs  → client.StreamLogs(ctx, network, range, addrs, func(block){ records<-blockToMap(block) })
    // traces→ client.StreamTraces(...)
    // goroutine 内驱动 callback，callback 内 select { records<-: case <-ctx.Done(): }
}
func (s *SQDAdapter) Health(ctx) ProviderHealth {
    // isInCooldown→DEGRADED, consecutive503>3→NO_WORKER, Breaker.State==CircuitOpen→UNAVAILABLE
}
```

### RPCAdapter (`provider/rpc_adapter.go`)
```go
// rpcmanager.Manager → LookupProvider
type RPCAdapter struct { manager *rpcmanager.Manager }
func (r *RPCAdapter) ExecuteLookup(ctx, req) (*LookupResult, error) {
    // address_type → manager.Address(ctx, chain, addr, false)
    // balance     → manager.Address(...)
}
```

### Bridges (`provider/bridges.go`)
```go
AWSAdapter          → ObjectProvider (待接入真实 S3)
ParquetWriterBridge → writer.VerifyParquet() 格式校验
DuckDBIndexerBridge → IndexParquet(CREATE VIEW) + UpdateFirstSeen(INSERT)
CheckpointBridge    → SaveSnapshot(JSON灾备+原子rename)
```

### JobAPIHandler (`job_api.go`)
```go
POST /jobs → createJob(CREATED→VALIDATING)
GET  /jobs/{id} → getJob
POST /jobs/{id}/pause|resume|cancel → Transition 驱动
POST /jobs/{id}/retry-failed → (FAILED)→QUEUED
```

### 真实闭环 Bridges (`provider/real_bridges.go` — 9533 bytes)
```go
// RealAWSAdapter — HTTP S3下载 + Parquet校验 + 断点续传(文件存在跳过)
// RealS3Discoverer — aws.Adapter.DiscoverTransactions + HTTPURL转换
// RealDuckDBIndexer — duckdb.Engine.ExecSQL: CREATE VIEW + UpdateFirstSeen(INSERT OR REPLACE) + TableRowCount
// RealCheckpointBridge — SQDCheckpointStore.Save/Load + JSON灾备 + 文件存在校验(Glob匹配)
// RealParquetWriter — DuckDB COPY (SELECT * FROM read_parquet) TO 'out.parquet' (FORMAT PARQUET)
// Dispatcher — 一键装配 Router+Writer+Indexer+Checkpoint
```

### E2E 测试 (`e2e_test.go`) — 37/37 PASS
| 测试 | 覆盖 |
|------|------|
| `TestBSCSingleAddressE2E` | CREATED→VALIDATING→DISCOVERING→QUEUED→RUNNING→DOWNLOADING→FINALIZING→COMPLETED, discovery=1/1, range=8123456-54000000 |
| `TestBreakpointResume` | 4 chunks(2完成→PAUSED→RESUMED→剩余2恢复→COMPLETED), Gate验证通过 |
| `TestSQD503Failover` | ErrorClassifier("503 Service Unavailable"→SQDNoWorkers, "429"→RateLimited)+FailoverBudget(限2次拒绝第3次)+RetryBudget(限3次拒绝第4次) |

### DownloadCenterPage (`frontend/DownloadCenterPage.tsx`)
```tsx
// 任务创建: Chain选择(BSC灰度)+地址输入+自动首次时间Switch
// 任务列表: 状态/阶段/进度/操作(暂停▶恢复▶取消▶重试失败)
// 任务详情: Modal弹出Descriptions(JobID+Status+Stage+Progress+Error)
// 修复记录: useEffect挂载+resp.ok检查+Record<JobStatus>类型+移除未使用导入
```

### 审查历史汇总
| 审查 | 目标 | 结论 | 修复 |
|------|------|------|------|
| 安全审查 #1 | executor.go (RateLimiter) | WARN | Release() 非阻塞 select |
| 代码审查 #2 | executor.go (ErrorClassifier) | **BLOCK** | containsPattern 组合词匹配 |
| 代码审查 #2 | migration.go (Rollback) | **BLOCK** | 逐步 saveState |
| 安全审查 #3 | migration.go | WARN(4 MEDIUM) | 记录待修复 |
| 代码审查 #4 | DownloadCenterPage.tsx | **BLOCK**→ship | useEffect/resp.ok/类型/导入 |
| 安全审查 #5 | DownloadCenterPage.tsx | PASS | 2 LOW(info) |

### E2E 测试 (`e2e_test.go`) — 37/37 PASS

| 测试 | 覆盖 |
|------|------|
| `TestBSCSingleAddressE2E` | 7步全链路: CREATED→VALIDATING→DISCOVERING→QUEUED→RUNNING→DOWNLOADING→FINALIZING→COMPLETED |
| `TestBreakpointResume` | 4 chunks(2完成→PAUSED→RESUMED→剩余恢复→COMPLETED) |
| `TestSQD503Failover` | ErrorClassifier(503→SQDNoWorkers)+FailoverBudget(限2次)+RetryBudget(限3次) |

---

## 十一、审查历史

| 审查 | 文件 | 结论 | 修复 |
|------|------|------|------|
| **安全审查 #1** | executor.go (RateLimiter) | WARN — double-release 死锁 | Release() 改为非阻塞 select |
| **代码审查 #2** | executor.go (ErrorClassifier) | **BLOCK** — 独立子串误匹配 | 改为 containsPattern 组合词匹配 |
| **代码审查 #2** | migration.go (Rollback) | **BLOCK** — 部分失败状态不一致 | 逐步 saveState，失败时保存已回滚版本 |
| **安全审查 #3** | migration.go | WARN — 4 MEDIUM(路径/静默错误/状态损坏/未认证DDL) | 记录待修复，当前无生产调用方 |

---

## 十二、测试基线

```
go vet ./internal/...   ✅ 零警告
go test ./internal/...  ✅ 全量 22 包零回归
go build ./...          ✅ 全量编译通过
npm run build           ✅ TypeScript+Vite 887ms

downloadengine 包: 34/34 PASS
  ├── 7  状态机 (Transition/幂等/非法/终态/并发/SetStage/唯一)
  ├── 4  Router (Streaming/Object/Unhealthy/Capabilities)
  ├── 8  Planner (AutoFirstSeen/Block/Invalid/NotFound/Partial/分组)
  ├── 6  Migration (RunAll/Idempotent/Persist/Reject/Rollback/SchemaJSON)
  ├── 2  RateLimiter (Acquire+Release/满容量超时)
  ├── 7  集成 (Manifest原子/Gate阻塞+通过/Registry CRUD+幂等/FeatureFlag)
```

## 十三、PRD 覆盖

| 章节 | 说明 | 状态 |
|------|------|------|
| §5 设计原则 | 双层 Status+Stage、Provider 组合接口 | ✅ |
| §7 核心模块 | 14 个子目录 + 11 个源文件 | ✅ |
| §8 任务类型 | 10 种 JobType | ✅ |
| §9 范围模式 | 6 种 RangeMode + RangePlanner 6 方法 | ✅ |
| §10 地址发现引擎 | DiscoveryEngine + FirstSeenResolver 接口 | ✅ |
| §11 范围规划器 | planAutoFirstSeen/Block/Time/Full/Resume/Incremental | ✅ |
| §13 多地址分组 | PlanGroups 3 级(≤100/≤5000/5000+) | ✅ |
| §14 Provider 抽象 | Streaming/Object/Lookup 接口 | ✅ |
| §15 Provider Router | CapabilityCache + HealthCache + Resolve* | ✅ |
| §16 Provider 状态 | 7 态 HealthStatus | ✅ |
| §18 任务状态机 | validTransitions 表 + Transition() 唯一入口 | ✅ |
| §19 Chunk 状态 | 8 态 + 指数退避 + 重试 | ✅ |
| §23 Manifest V2 | 原子 rename Finalizer | ✅ |
| §25 数据验证 | CompletionGate 5 项检查 | ✅ |
| §28-31 前端 | V1.5.0 开关 + DatePicker + firstSeen | ✅ |
| §38 配置 | FeatureFlags 灰度(BSC→多链) | ✅ |
| §33 数据库迁移 | Migration Framework + 4 内置迁移 + 幂等+回滚 | ✅ |
| §34 单元测试 | 34 个测试 + Migration/Manifest/Registry/FeatureFlag 覆盖 | ✅ |
| §43 Codex 要求 | 复用 SQD/Scheduler/Checkpoint，不破坏 parquetdownload | ✅ |
| §20 Checkpoint | 待接入 SQDCheckpointStore | 🔲 |
| §21 Writer | 待接入 Parquet Writer | 🔲 |
| §24 DuckDB | 待集成现有 DuckDB Engine | 🔲 |

---

## 十四、下一阶段（真实闭环）

```
AUTO_FIRST_SEEN → 真实 SQD Provider → Range Planner → Chunk Executor
→ 真实 Parquet Writer → Checkpoint V2 → DuckDB Indexer
→ Validation → Manifest Finalizer → Dataset Registry
```

待补充：Provider Coverage 区间规划、`go test -race`（Windows/386不支持）、Playwright E2E、SQD 503 中断恢复测试、DownloadCenterPage 前端、多链灰度。
