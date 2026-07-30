# 地址首次出现时间开关企业级 PRD

版本：V1.4.3  
文档类型：产品需求文档 / 技术实施规范 / Codex 部署说明  
项目根目录：

```text
E:\codex\bsc_analytics
```

适用系统：

- BSC Analytics
- Ethereum Analytics
- Base Analytics
- Arbitrum Analytics
- 地址分析
- 批量地址分析
- SQD 历史数据任务
- Parquet + DuckDB 本地数据仓库
- 数据源管理中心

---

# 1. 文档目的

本文档用于指导产品、前端、后端、测试与部署人员完成“地址首次出现时间开关”功能。

功能目标：

```text
默认分析时间范围：
地址首次出现时间 → 当前时间
```

同时，用户必须可以在前端关闭该逻辑，并手动选择分析开始时间。

本文档同时作为 Codex 实施依据，要求 Codex 按本文档完成：

- React 前端改造；
- Go 后端改造；
- DuckDB 数据表与迁移；
- SQD 历史查询接入；
- 缓存策略；
- 错误处理；
- 单元测试；
- 集成测试；
- Playwright 端到端测试；
- 构建与验收。

---

# 2. 背景

当前地址分析依赖用户手动选择日期范围。

存在以下问题：

1. 用户可能不知道地址最早活动时间；
2. 用户可能漏选早期交易；
3. 不同用户选择不同开始时间，分析口径不一致；
4. 部分地址需要从首次出现开始分析；
5. 部分用户只希望分析特定时间段；
6. SQD 临时不可用时，自动首次时间逻辑不能阻塞整个分析流程；
7. EOA 地址并不存在严格意义上的链上“创建时间”。

因此系统应统一使用：

```text
First Seen Time
地址首次出现时间
```

而不是：

```text
Create Time
地址创建时间
```

---

# 3. 术语定义

## 3.1 First Seen

地址首次出现在当前链历史数据中的最早时间。

可能来源包括：

- 发起交易；
- 接收交易；
- 接收原生币；
- 发送原生币；
- 出现在 Trace 中；
- 出现在 Token Transfer 的 `from`；
- 出现在 Token Transfer 的 `to`；
- 合约创建交易；
- CREATE / CREATE2 Trace。

## 3.2 EOA

外部账户。

EOA 没有链上创建时间。

EOA 的首次时间定义为：

```text
earliest_activity_time
```

## 3.3 Contract

智能合约地址。

Contract 优先使用：

```text
contract_creation_time
```

若无法获取合约创建记录，则回退到：

```text
earliest_activity_time
```

## 3.4 Effective Date Range

最终真正用于分析任务的时间范围。

字段：

```text
effective_start_time
effective_end_time
start_time_source
```

---

# 4. 产品目标

本版本必须实现：

1. 默认开启“从地址首次出现开始”；
2. 自动查询地址首次出现时间；
3. 自动将首次出现时间作为分析开始时间；
4. 默认结束时间为当前时间；
5. 用户可以关闭首次时间开关；
6. 关闭后允许手动选择开始时间；
7. 开启时禁止手动修改开始时间；
8. 首次时间查询失败时允许用户切换到手动模式；
9. 后端必须自行解析首次时间；
10. 所有分析任务保存最终生效时间范围；
11. 单地址分析与批量地址分析使用同一规则；
12. 多链环境下按 `chain_id + address` 隔离；
13. 不使用 RPC 扫描全历史；
14. SQD 503 不导致无限重试；
15. 不将业务数据写入 C 盘。

---

# 5. 非目标

本版本不包括：

- 重构全部地址分析引擎；
- 用 RPC 下载全链历史；
- 重构全部数据源管理页面；
- 将所有旧任务自动转换为首次时间模式；
- 对地址私钥生成时间做推断；
- 为未上链地址生成虚假创建时间；
- 强制所有用户使用完整历史分析。

---

# 6. 用户角色

## 6.1 数据分析师

希望默认从地址首次活动开始分析完整历史。

## 6.2 资金分析师

希望根据调查区间手动指定开始时间。

## 6.3 系统管理员

希望查看首次时间来源、状态、错误和 Provider 健康状态。

## 6.4 批量任务用户

希望批量地址中的每个地址单独解析首次时间。

---

# 7. 核心用户流程

## 7.1 默认流程

```text
用户进入地址分析页面
        ↓
选择链
        ↓
输入地址
        ↓
系统校验地址格式
        ↓
默认开启“从地址首次出现开始”
        ↓
请求 First Seen API
        ↓
显示首次出现时间
        ↓
开始时间输入框禁用
        ↓
结束时间默认当前时间
        ↓
用户提交
        ↓
后端重新解析首次时间
        ↓
生成 Effective Date Range
        ↓
创建分析任务
```

## 7.2 手动时间流程

```text
用户关闭“从地址首次出现开始”
        ↓
开始时间输入框启用
        ↓
用户选择开始时间
        ↓
用户选择结束时间或保持当前时间
        ↓
前端校验
        ↓
后端再次校验
        ↓
创建分析任务
```

## 7.3 SQD 不可用流程

```text
请求 First Seen
        ↓
SQD 返回 503 No available workers
        ↓
后端返回 TEMPORARILY_UNAVAILABLE
        ↓
前端显示错误
        ↓
用户可点击重试
        ↓
或关闭首次时间开关
        ↓
手动选择开始时间继续分析
```

---

# 8. 前端产品需求

## 8.1 日期区域

地址分析页面增加：

```text
分析时间范围
```

默认界面：

```text
[开启] 从地址首次出现开始

首次出现时间：
正在获取...

开始时间：
[禁用]

结束时间：
[当前时间]
```

获取成功：

```text
首次出现时间：
2021-06-12 10:21:33 UTC

来源：
SQD Activity

数据覆盖：
完整
```

关闭开关：

```text
[关闭] 从地址首次出现开始

开始时间：
[日期时间选择器]

结束时间：
[日期时间选择器]
```

---

# 9. 前端交互规则

## 9.1 默认值

```ts
useFirstSeen = true
startTime = undefined
endTime = now
```

## 9.2 开启状态

开启后：

- 自动查询首次时间；
- 开始时间输入框禁用；
- 不允许用户编辑开始时间；
- 提交时发送 `use_first_seen=true`；
- `start_time` 发送 `null`；
- 显示查询状态；
- 显示首次时间来源；
- 显示覆盖状态。

## 9.3 关闭状态

关闭后：

- 启用开始时间输入框；
- 用户必须选择开始时间；
- 开始时间为空时禁止提交；
- 提交时发送 `use_first_seen=false`；
- 提交手工开始时间；
- 允许保留用户之前选择过的手工时间。

## 9.4 地址变化

地址变化时：

1. 取消旧请求；
2. 清空旧首次时间；
3. 重置查询状态；
4. 校验地址；
5. 重新查询；
6. 防止旧响应覆盖新地址结果。

实现建议：

```text
AbortController
```

或者：

```text
request sequence id
```

## 9.5 链变化

链变化时必须重新查询。

缓存键：

```text
chain_id + normalized_address
```

禁止只按地址缓存。

## 9.6 提交按钮

以下情况禁止提交：

- 地址格式无效；
- `use_first_seen=true` 且首次时间仍在加载；
- `use_first_seen=true` 且状态为 NOT_FOUND；
- `use_first_seen=true` 且状态为 TEMPORARILY_UNAVAILABLE；
- `use_first_seen=false` 且 start_time 为空；
- start_time 大于或等于 end_time；
- end_time 明显晚于当前时间。

PARTIAL 状态允许提交，但必须显示警告。

---

# 10. 前端状态设计

```ts
type FirstSeenStatus =
  | 'idle'
  | 'loading'
  | 'found'
  | 'partial'
  | 'not_found'
  | 'temporarily_unavailable'
  | 'failed'

interface AddressDateRangeState {
  useFirstSeen: boolean
  firstSeenStatus: FirstSeenStatus
  firstSeenTime?: string
  firstSeenBlock?: number
  firstSeenSource?: string
  coverageStatus?: 'FULL' | 'PARTIAL' | 'UNKNOWN'
  startTime?: string
  endTime?: string
  errorCode?: string
  errorMessage?: string
}
```

---

# 11. 前端组件建议

建议拆分：

```text
components/address-analysis/
├── FirstSeenSwitch.tsx
├── FirstSeenStatus.tsx
├── AnalysisDateRangePicker.tsx
├── CoverageWarning.tsx
├── FirstSeenErrorAction.tsx
└── AddressDateRangeSection.tsx
```

组件职责：

## FirstSeenSwitch

负责开关显示和状态切换。

## FirstSeenStatus

负责显示：

- loading；
- found；
- partial；
- not found；
- provider unavailable；
- failed。

## AnalysisDateRangePicker

负责手动日期选择与校验。

## CoverageWarning

负责显示 PARTIAL 覆盖警告。

## FirstSeenErrorAction

负责提供：

- 重试；
- 切换手动模式。

---

# 12. 前端文案

## Loading

```text
正在查询地址首次出现时间...
```

## Found

```text
首次出现：2021-06-12 10:21:33 UTC
```

## Partial

```text
当前首次出现时间基于部分历史数据，可能晚于真实首次活动时间。
```

## Not Found

```text
未发现该地址在当前链上的活动记录。
```

## Temporarily Unavailable

```text
首次出现时间暂时无法获取，请稍后重试，或关闭该功能后手动选择开始时间。
```

## Failed

```text
首次出现时间查询失败。
```

## Manual Required

```text
关闭“从地址首次出现开始”后，请选择开始时间。
```

---

# 13. 后端 API 设计

## 13.1 查询首次出现时间

```http
GET /api/crypto/addresses/{chain}/{address}/first-seen
```

查询参数：

```text
refresh=false
```

成功：

```json
{
  "chain_id": "bsc",
  "address": "0x...",
  "address_type": "EOA",
  "first_seen_block": 12345678,
  "first_seen_time": "2021-06-12T10:21:33Z",
  "first_seen_source": "sqd_activity",
  "coverage_status": "FULL",
  "status": "FOUND",
  "provider": "SQD",
  "cached": true
}
```

部分覆盖：

```json
{
  "chain_id": "bsc",
  "address": "0x...",
  "first_seen_block": 12345678,
  "first_seen_time": "2021-06-12T10:21:33Z",
  "first_seen_source": "local_parquet",
  "coverage_status": "PARTIAL",
  "status": "PARTIAL",
  "provider": "LOCAL"
}
```

暂时不可用：

```json
{
  "chain_id": "bsc",
  "address": "0x...",
  "status": "TEMPORARILY_UNAVAILABLE",
  "error_code": "SQD_NO_AVAILABLE_WORKERS",
  "error_message": "SQD provider has no available workers"
}
```

未找到：

```json
{
  "chain_id": "bsc",
  "address": "0x...",
  "status": "NOT_FOUND"
}
```

---

# 14. 地址分析 API

```http
POST /api/crypto/address-analysis
```

请求：

```json
{
  "chain_id": "bsc",
  "address": "0x...",
  "use_first_seen": true,
  "start_time": null,
  "end_time": null
}
```

响应：

```json
{
  "job_id": "analysis-001",
  "effective_date_range": {
    "start_time": "2021-06-12T10:21:33Z",
    "end_time": "2026-07-30T13:18:00Z",
    "start_time_source": "FIRST_SEEN"
  }
}
```

手动模式：

```json
{
  "chain_id": "bsc",
  "address": "0x...",
  "use_first_seen": false,
  "start_time": "2025-01-01T00:00:00Z",
  "end_time": "2026-07-30T13:18:00Z"
}
```

---

# 15. 后端日期解析规则

统一实现：

```go
func ResolveEffectiveDateRange(
    ctx context.Context,
    chainID string,
    address string,
    req AnalysisDateRequest,
) (*EffectiveDateRange, error)
```

规则：

```text
if use_first_seen == true:
    first_seen = resolve(chain_id, address)
    if status == FOUND or PARTIAL:
        effective_start_time = first_seen_time
    else:
        返回业务错误

if use_first_seen == false:
    start_time 必填
    effective_start_time = start_time

effective_end_time = end_time or now()

validate effective_start_time < effective_end_time
```

后端必须忽略前端传入的：

```text
first_seen_time
```

后端必须重新解析。

---

# 16. 首次时间解析规则

## 16.1 EOA

取以下来源的最小区块：

```text
min(
  tx_from_block,
  tx_to_block,
  native_transfer_block,
  trace_from_block,
  trace_to_block,
  token_transfer_from_block,
  token_transfer_to_block
)
```

然后获取：

```text
block_timestamp
```

## 16.2 Contract

优先级：

```text
1. CREATE Trace
2. CREATE2 Trace
3. Contract Creation Transaction
4. Earliest Activity
```

来源字段：

```text
contract_creation
activity_fallback
```

---

# 17. 数据源优先级

```text
1. address_first_seen 缓存
2. 本地 Parquet + DuckDB
3. SQD finalized stream
4. AWS 历史数据
5. RPC 辅助验证
```

RPC 不允许扫描全历史。

RPC 只用于：

- 获取区块时间；
- 判断地址类型；
- 获取合约代码；
- 验证区块；
- 补充合约创建信息。

---

# 18. 后端目录建议

```text
internal/addressfirstseen/
├── service.go
├── resolver.go
├── repository.go
├── cache.go
├── types.go
├── errors.go
├── coverage.go
├── contract.go
├── activity.go
├── normalizer.go
└── service_test.go
```

API 层：

```text
internal/http/handlers/address_first_seen.go
```

日期解析：

```text
internal/addressanalysis/date_range_resolver.go
```

---

# 19. 数据库设计

建议新增表：

```sql
CREATE TABLE IF NOT EXISTS address_first_seen (
    chain_id                 VARCHAR NOT NULL,
    address                  VARCHAR NOT NULL,
    address_type             VARCHAR,
    first_seen_block         BIGINT,
    first_seen_time          TIMESTAMP,
    first_seen_source        VARCHAR,
    coverage_status          VARCHAR NOT NULL DEFAULT 'UNKNOWN',
    contract_created_block   BIGINT,
    contract_created_time    TIMESTAMP,
    contract_creation_tx     VARCHAR,
    query_status             VARCHAR NOT NULL,
    provider                 VARCHAR,
    error_code               VARCHAR,
    error_message            VARCHAR,
    created_at               TIMESTAMP NOT NULL,
    updated_at               TIMESTAMP NOT NULL,
    PRIMARY KEY (chain_id, address)
);
```

索引：

```sql
CREATE INDEX IF NOT EXISTS idx_address_first_seen_status
ON address_first_seen(query_status);

CREATE INDEX IF NOT EXISTS idx_address_first_seen_updated
ON address_first_seen(updated_at);
```

---

# 20. 任务表改造

地址分析任务增加：

```text
use_first_seen
requested_start_time
requested_end_time
effective_start_time
effective_end_time
start_time_source
first_seen_status
first_seen_source
coverage_status
```

目的：

- 保留用户请求；
- 保留最终执行范围；
- 支持审计；
- 支持重跑；
- 支持问题排查。

---

# 21. 批量分析规则

批量任务级请求：

```json
{
  "use_first_seen": true,
  "end_time": null
}
```

每个地址单独计算：

```text
effective_start_time[address]
```

禁止：

```text
用批量中最早地址的时间覆盖全部地址
```

地址子任务必须保存：

```text
first_seen_status
effective_start_time
effective_end_time
start_time_source
```

---

# 22. 状态枚举

```go
type FirstSeenStatus string

const (
    FirstSeenFound                  FirstSeenStatus = "FOUND"
    FirstSeenNotFound               FirstSeenStatus = "NOT_FOUND"
    FirstSeenPartial                FirstSeenStatus = "PARTIAL"
    FirstSeenTemporarilyUnavailable FirstSeenStatus = "TEMPORARILY_UNAVAILABLE"
    FirstSeenFailed                 FirstSeenStatus = "FAILED"
)
```

覆盖状态：

```text
FULL
PARTIAL
UNKNOWN
```

地址类型：

```text
EOA
CONTRACT
UNKNOWN
```

---

# 23. 缓存策略

## FOUND

```text
永久缓存
```

允许以下场景强制刷新：

- 历史数据覆盖提升；
- Trace 数据补齐；
- 用户点击重新检测；
- 管理员触发重算。

## PARTIAL

```text
TTL = 24小时
```

## TEMPORARILY_UNAVAILABLE

```text
TTL = 1分钟
```

## NOT_FOUND

```text
TTL = 1小时
```

## FAILED

默认不长期缓存。

---

# 24. SQD 高可用联动

必须复用 V1.4.2：

- Circuit Breaker；
- Scheduler；
- Provider Health；
- 503 专用退避；
- Checkpoint。

当 SQD 返回：

```text
503 No available workers
```

首次时间服务必须：

1. 停止快速重试；
2. 记录 Provider 状态；
3. 返回 `TEMPORARILY_UNAVAILABLE`；
4. 不让前端一直 loading；
5. 允许用户切换到手动时间模式。

---

# 25. 错误码

建议：

```text
INVALID_CHAIN
INVALID_ADDRESS
FIRST_SEEN_NOT_FOUND
FIRST_SEEN_PARTIAL
FIRST_SEEN_QUERY_FAILED
SQD_NO_AVAILABLE_WORKERS
SQD_CIRCUIT_OPEN
DATE_RANGE_INVALID
START_TIME_REQUIRED
END_TIME_IN_FUTURE
```

---

# 26. 时间规范

所有后端时间：

```text
UTC
```

API：

```text
ISO 8601
```

示例：

```text
2026-07-30T13:18:00Z
```

前端可以根据浏览器时区显示，但必须明确：

```text
显示时区
```

数据库禁止存储模糊本地时间。

---

# 27. 地址规范化

存储前：

```text
strings.ToLower(address)
```

校验：

- 长度；
- 十六进制；
- `0x` 前缀；
- 链类型；
- 可选 checksum 验证。

缓存键：

```text
chain_id + ":" + normalized_address
```

---

# 28. 日志

新增事件：

```text
ADDRESS_FIRST_SEEN_QUERY_STARTED
ADDRESS_FIRST_SEEN_CACHE_HIT
ADDRESS_FIRST_SEEN_FOUND
ADDRESS_FIRST_SEEN_PARTIAL
ADDRESS_FIRST_SEEN_NOT_FOUND
ADDRESS_FIRST_SEEN_PROVIDER_UNAVAILABLE
ADDRESS_FIRST_SEEN_FAILED
ANALYSIS_DATE_RANGE_RESOLVED
```

记录字段：

```text
request_id
job_id
chain_id
address
use_first_seen
requested_start_time
effective_start_time
effective_end_time
first_seen_source
coverage_status
provider
duration_ms
error_code
```

---

# 29. Metrics

建议增加：

```text
address_first_seen_requests_total
address_first_seen_cache_hits_total
address_first_seen_found_total
address_first_seen_partial_total
address_first_seen_not_found_total
address_first_seen_failed_total
address_first_seen_provider_unavailable_total
address_first_seen_latency_ms
```

---

# 30. 安全要求

1. 后端不能信任前端提交的首次时间；
2. 后端必须验证 chain 和 address；
3. 后端必须限制查询频率；
4. 必须防止重复请求风暴；
5. 必须设置超时；
6. 必须复用 SQD Scheduler；
7. 日志不得记录密钥；
8. API Key 继续使用 DPAPI + AES-GCM；
9. 业务数据不得保存到 C 盘。

---

# 31. 性能要求

目标：

```text
缓存命中：
P95 < 100ms
```

```text
本地 DuckDB 查询：
P95 < 2s
```

```text
SQD 查询：
应受 Provider 实际性能影响
```

前端首次时间请求超时建议：

```text
30秒
```

超过后返回明确状态，禁止无限 loading。

---

# 32. 兼容旧任务

旧任务若没有：

```text
use_first_seen
```

兼容规则：

```text
use_first_seen = false
```

继续使用旧任务原有的：

```text
start_time
end_time
```

禁止升级后重跑旧任务时自动扩大为全历史。

新任务默认：

```text
use_first_seen = true
```

---

# 33. 数据迁移

迁移文件建议：

```text
migrations/xxxx_add_address_first_seen.sql
```

迁移内容：

- 创建 `address_first_seen`；
- 增加任务字段；
- 增加索引；
- 设置旧任务兼容值；
- 不删除旧字段；
- 提供回滚脚本。

---

# 34. 后端单元测试

必须覆盖：

1. 默认开启首次时间；
2. EOA earliest activity；
3. Contract creation 优先；
4. Contract fallback；
5. 多来源取最小区块；
6. 缓存命中；
7. PARTIAL；
8. NOT_FOUND；
9. SQD 503；
10. Circuit Open；
11. 手动时间模式；
12. 手动模式未传 start_time；
13. start >= end；
14. end_time 未来；
15. 地址格式错误；
16. 链不支持；
17. 地址大小写归一化；
18. 多链缓存隔离；
19. 批量任务逐地址解析；
20. 旧任务兼容。

---

# 35. 前端测试

必须覆盖：

1. 默认开关开启；
2. 开启时开始时间禁用；
3. 关闭后开始时间启用；
4. 关闭但未选择开始时间时不可提交；
5. Loading 状态；
6. Found 状态；
7. Partial 警告；
8. Not Found；
9. Temporarily Unavailable；
10. Failed；
11. 重试按钮；
12. 切换手动模式；
13. 地址变化重新请求；
14. 链变化重新请求；
15. 旧请求取消；
16. 请求参数正确；
17. 最终时间显示正确；
18. 时区显示正确。

---

# 36. Playwright E2E

必须覆盖：

## 场景一：EOA 默认分析

```text
输入 BSC EOA
→ 默认开关开启
→ 查询首次时间成功
→ 提交
→ 返回 FIRST_SEEN
```

## 场景二：Contract

```text
输入 Contract
→ 显示合约创建时间
→ 提交
```

## 场景三：手动模式

```text
关闭开关
→ 选择开始时间
→ 提交
→ 返回 USER_SELECTED
```

## 场景四：SQD 503

```text
模拟 503
→ 显示暂不可用
→ 关闭开关
→ 手动选择日期
→ 提交成功
```

## 场景五：PARTIAL

```text
返回 PARTIAL
→ 显示警告
→ 用户确认后提交
```

---

# 37. 构建验证

后端：

```bash
go test ./internal/... -count=1
go vet ./...
go build ./...
```

前端：

```bash
npm run test
npm run build
```

E2E：

```bash
npx playwright test
```

健康检查：

```text
GET /health
```

---

# 38. 部署步骤

## 第一步：数据库迁移

执行迁移并确认：

```text
address_first_seen 表存在
任务字段已增加
索引已创建
```

## 第二步：部署后端

实现：

```text
internal/addressfirstseen
internal/addressanalysis/date_range_resolver.go
```

接入：

```text
SQD Provider
Data Source Manager
DuckDB
Address Analysis
Batch Analysis
```

## 第三步：部署前端

修改：

```text
地址分析页
批量分析页
任务详情页
数据源管理页
```

## 第四步：执行测试

必须全部通过。

## 第五步：灰度验证

建议先在：

```text
BSC
```

开启。

验证稳定后再推广：

```text
Ethereum
Base
Arbitrum
```

---

# 39. 回滚方案

若出现严重问题：

1. 前端关闭首次时间默认开关；
2. 后端配置：

```yaml
address_first_seen:
  enabled: false
  default_use_first_seen: false
```

3. 新任务回退为手动日期；
4. 保留数据库表；
5. 不删除已生成数据；
6. 不影响现有地址分析任务。

---

# 40. 配置项

```yaml
address_first_seen:
  enabled: true
  default_use_first_seen: true
  query_timeout: 30s
  partial_cache_ttl: 24h
  unavailable_cache_ttl: 1m
  not_found_cache_ttl: 1h
  allow_partial_result: true
  max_concurrent_queries: 1
```

前端配置接口：

```http
GET /api/config/address-analysis
```

返回：

```json
{
  "first_seen_enabled": true,
  "default_use_first_seen": true,
  "allow_partial_first_seen": true
}
```

---

# 41. 验收标准

必须全部满足：

- 默认开启首次时间；
- 用户可以关闭；
- 开启时开始时间不可编辑；
- 关闭时开始时间必填；
- EOA 使用 earliest activity；
- Contract 优先使用 creation time；
- 后端不信任前端 first_seen_time；
- 多链隔离；
- 地址变化重新请求；
- SQD 503 不无限重试；
- SQD 异常允许手动模式继续；
- PARTIAL 有明确提示；
- 任务保存 effective date range；
- 批量任务按地址独立计算；
- 旧任务不自动扩大范围；
- 所有时间统一 UTC；
- 不在 C 盘保存业务数据；
- Go 测试通过；
- 前端构建通过；
- Playwright 通过；
- 健康检查通过。

---

# 42. 最终架构

```text
React 地址分析页
        ↓
First Seen Switch
        ↓
First Seen API
        ↓
Address First Seen Service
        ↓
Cache
  ├── DuckDB
  ├── Local Parquet
  ├── SQD
  ├── AWS
  └── RPC Verification
        ↓
Effective Date Range Resolver
        ↓
Address Analysis Job
        ↓
SQD / Parquet / DuckDB
        ↓
地址分析结果
```

---

# 43. Codex 实施要求

Codex 必须：

1. 先分析现有目录结构；
2. 优先复用现有模块；
3. 不破坏 V1.4.2 SQD 熔断器；
4. 不破坏 Scheduler；
5. 不删除现有 API；
6. 不修改项目根路径；
7. 不在 C 盘保存业务数据；
8. 所有新增逻辑必须有测试；
9. 所有新增字段必须有迁移；
10. 所有错误必须返回稳定错误码；
11. 所有时间必须使用 UTC；
12. 前后端字段命名必须一致；
13. 最终输出实施清单；
14. 最终输出修改文件列表；
15. 最终输出测试结果；
16. 最终输出未完成项；
17. 不得声称未实际运行的测试已通过。
