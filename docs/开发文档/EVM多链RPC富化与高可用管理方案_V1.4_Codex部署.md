# EVM 多链 RPC 富化与高可用管理方案 V1.4（Codex 部署版）

> 项目目录：`E:\codex\bsc_analytics`  
> 主数据链路：SQD / AWS Parquet → 标准化 Parquet → DuckDB → 地址分析  
> 本方案用途：使用 Chainstack、Ankr、NodeReal 对本地历史数据进行低频、异步、可缓存的富化与状态补充。

## 1. 核心原则

RPC 不负责历史批量下载，不替代 SQD / Parquet。

```text
历史事实数据：SQD / AWS Parquet
富化与当前状态：Chainstack / Ankr / NodeReal
本地查询：Parquet + DuckDB
```

RPC 只用于：

- Token Metadata：`name()`、`symbol()`、`decimals()`、`totalSupply()`；
- 地址当前类型：`eth_getCode`；
- 当前原生币余额：`eth_getBalance`；
- 按需 Token 余额：`balanceOf(address)`；
- 少量缺失 Receipt：`eth_getTransactionReceipt`。

禁止用于：

- 地址历史交易批量下载；
- 全历史 `eth_getLogs`；
- Trace 批量下载；
- 对每条 Transfer 重复调用 RPC；
- 页面每次刷新都重新获取已缓存字段。

## 2. 三家供应商定位

### NodeReal

推荐作为 BSC 主节点，Ethereum 备用节点。主要承担 Metadata、地址类型、BNB 余额和少量 Receipt 补漏。

### Chainstack

推荐作为 Ethereum、Base、Arbitrum 主节点，BSC 第二节点。主要承担多链主用和容灾。

### Ankr

推荐作为 BSC、Ethereum、Base、Arbitrum 通用备用节点，在主节点限流、故障或维护时接管。

推荐默认路由：

```yaml
rpc_routing:
  bsc:
    - provider: nodereal
      priority: 10
    - provider: chainstack
      priority: 20
    - provider: ankr
      priority: 30

  eth:
    - provider: chainstack
      priority: 10
    - provider: nodereal
      priority: 20
    - provider: ankr
      priority: 30

  base:
    - provider: chainstack
      priority: 10
    - provider: ankr
      priority: 20

  arbitrum:
    - provider: chainstack
      priority: 10
    - provider: ankr
      priority: 20
```

不得假设供应商一定支持某条链。保存配置前必须真实调用：

```text
eth_chainId
eth_blockNumber
```

并校验：

| 链 | chain_key | chain_id |
|---|---|---:|
| BNB Smart Chain | bsc | 56 |
| Ethereum | eth | 1 |
| Base | base | 8453 |
| Arbitrum One | arbitrum | 42161 |

返回 chain ID 不匹配时，拒绝启用该 endpoint。

## 3. 总体架构

```text
                    历史事实数据

          AWS Parquet          SQD Stream
          Blocks / Tx      Tx / Logs / Trace
                 \            /
                  \          /
                 标准化 Parquet
                         ↓
                       DuckDB
                         ↓
           Address Activity / Summary
                         ↓
                      API / UI

                    RPC 富化层

       NodeReal / Chainstack / Ankr / Custom
                         ↓
      Metadata / Balance / Code / Receipt补漏
                         ↓
                    本地永久缓存
```

RPC 富化失败不得阻塞 SQD / Parquet 主任务。

## 4. 安全设计

### 4.1 前端可配置，但浏览器不能保存密钥

前端负责录入：

- 供应商；
- 链；
- 完整 Endpoint URL 或 API Key；
- 优先级；
- RPS；
- 并发数；
- 超时。

禁止：

- 保存到 `localStorage`；
- 保存到 `sessionStorage`；
- 写入前端环境变量；
- 浏览器直接请求第三方 RPC；
- API 返回已保存的完整密钥；
- 日志输出完整 endpoint。

正确流程：

```text
前端录入
   ↓
提交给本机后端
   ↓
后端测试连接
   ↓
加密保存
   ↓
前端只显示脱敏信息
```

### 4.2 Windows 本地加密

优先使用 Windows DPAPI。推荐：

1. 生成 256-bit master key；
2. master key 使用 DPAPI 加密；
3. endpoint 和 secret 使用 AES-256-GCM；
4. 每条配置独立 nonce；
5. 数据库只保存 ciphertext、nonce 和脱敏 host。

安全文件放在：

```text
E:\codex\bsc_analytics\config\secure\
```

目录 ACL 仅允许运行服务的 Windows 用户访问。

### 4.3 脱敏展示

```text
NodeReal · BSC
https://bsc-mainnet.nodereal.io/v1/••••••••a7F2

Chainstack · Ethereum
https://nd-xxx.p2pify.com/••••••••91bc

Ankr · Base
https://rpc.ankr.com/base/••••••••28e1
```

编辑时不能回填旧密钥，只显示：

```text
API Key：已配置
[替换密钥]
```

## 5. 后端模块

```text
internal/
├── rpcmanager/
│   ├── manager.go
│   ├── router.go
│   ├── provider.go
│   ├── health.go
│   ├── limiter.go
│   ├── breaker.go
│   ├── retry.go
│   ├── batch.go
│   ├── metrics.go
│   ├── redaction.go
│   └── securestore_windows.go
├── enrichment/
│   ├── metadata.go
│   ├── address_type.go
│   ├── native_balance.go
│   ├── token_balance.go
│   └── receipt_patch.go
└── api/
    ├── rpc_config_handlers.go
    ├── rpc_health_handlers.go
    └── enrichment_handlers.go
```

## 6. 数据表

### rpc_endpoints

```sql
CREATE TABLE rpc_endpoints (
    endpoint_id VARCHAR PRIMARY KEY,
    provider VARCHAR NOT NULL,
    chain_key VARCHAR NOT NULL,
    chain_id UBIGINT NOT NULL,
    display_name VARCHAR NOT NULL,
    endpoint_host VARCHAR NOT NULL,
    endpoint_encrypted BLOB NOT NULL,
    secret_encrypted BLOB,
    priority INTEGER NOT NULL,
    enabled BOOLEAN NOT NULL,
    max_rps DOUBLE NOT NULL,
    max_concurrency INTEGER NOT NULL,
    request_timeout_ms INTEGER NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
```

### rpc_endpoint_health

```sql
CREATE TABLE rpc_endpoint_health (
    endpoint_id VARCHAR PRIMARY KEY,
    status VARCHAR NOT NULL,
    health_score DOUBLE NOT NULL,
    latest_block UBIGINT,
    block_lag UBIGINT,
    latency_p50_ms DOUBLE,
    latency_p95_ms DOUBLE,
    success_rate_5m DOUBLE,
    consecutive_failures INTEGER,
    circuit_state VARCHAR,
    circuit_open_until TIMESTAMP,
    last_success_at TIMESTAMP,
    last_failure_at TIMESTAMP,
    last_error_code VARCHAR,
    last_error_message_redacted VARCHAR,
    checked_at TIMESTAMP
);
```

状态：

```text
HEALTHY
DEGRADED
RATE_LIMITED
UNAVAILABLE
MISCONFIGURED
DISABLED
```

### rpc_request_metrics

按分钟聚合，不保存完整请求：

```sql
CREATE TABLE rpc_request_metrics (
    minute TIMESTAMP,
    endpoint_id VARCHAR,
    chain_id UBIGINT,
    method VARCHAR,
    request_count UBIGINT,
    success_count UBIGINT,
    failure_count UBIGINT,
    rate_limited_count UBIGINT,
    timeout_count UBIGINT,
    latency_sum_ms DOUBLE
);
```

### enrichment_jobs

```sql
CREATE TABLE enrichment_jobs (
    job_id VARCHAR PRIMARY KEY,
    job_type VARCHAR,
    chain_key VARCHAR,
    chain_id UBIGINT,
    status VARCHAR,
    total_items UBIGINT,
    completed_items UBIGINT,
    succeeded_items UBIGINT,
    failed_items UBIGINT,
    skipped_items UBIGINT,
    started_at TIMESTAMP,
    updated_at TIMESTAMP,
    finished_at TIMESTAMP,
    cancellation_requested BOOLEAN,
    error_summary VARCHAR
);
```

## 7. 稳定性设计

### 7.1 每链、每供应商独立限速

限速器维度：

```text
provider → chain → method class
```

初始保守配置：

| Provider | 链 | 初始 RPS | 初始并发 |
|---|---|---:|---:|
| NodeReal | BSC | 4 | 4 |
| NodeReal | ETH | 2 | 2 |
| Chainstack | ETH/Base/Arbitrum | 4 | 4 |
| Chainstack | BSC | 3 | 3 |
| Ankr | 任意备用链 | 2 | 2 |

这些是系统初值，不是供应商固定上限。应根据 429、延迟和成功率动态调整。

### 7.2 自适应限速

采用 AIMD：

```text
持续成功 → 小幅提高 RPS
出现 429/CUPS 超限 → RPS 减半、并发降低、延长退避
```

前端可配置：

```text
min_rps
max_rps
min_concurrency
max_concurrency
```

### 7.3 超时

```text
连接超时：3秒
普通请求总超时：8秒
Metadata eth_call：10秒
Receipt：10秒
健康检查：5秒
```

### 7.4 重试

仅重试幂等读取方法。

可重试：

```text
HTTP 408/429/500/502/503/504
连接重置
DNS短暂失败
上游暂无健康节点
```

不可无限重试：

```text
认证失败
参数错误
方法不存在
chain ID不匹配
确定性合约revert
返回值解码失败
```

指数退避：

```text
500ms + jitter
1s + jitter
2s + jitter
4s + jitter
```

优先遵守 `Retry-After`。

单 endpoint 最多重试 2 次，然后切换备用；整个逻辑请求最多 4～5 次尝试。

### 7.5 熔断

每个 endpoint 独立熔断。

打开条件示例：

```text
连续失败 >= 5
最近20次成功率 < 50%
连续3次认证失败
```

状态：

```text
CLOSED
OPEN
HALF_OPEN
```

OPEN 时间：

- 一般错误：30 秒；
- 429：60～180 秒；
- 认证失败：等待用户更新；
- chain ID 错误：禁用配置。

### 7.6 自动故障转移

```text
健康主节点
   ↓失败
同链备用节点
   ↓失败
第三节点
   ↓
返回可解释的 UNAVAILABLE
```

节点选择综合：

- 静态优先级；
- 健康评分；
- P95 延迟；
- 近 5 分钟成功率；
- 限流状态；
- 区块落后高度。

## 8. 健康检查

快速检查每 30 秒：

```text
eth_chainId
eth_blockNumber
```

深度检查每 5 分钟：

```text
eth_getCode(已知稳定合约)
eth_call(decimals)
```

区块落后：

```text
block_lag = 同链最高区块 - 节点当前区块
```

默认：

```text
0～2块：HEALTHY
3～10块：DEGRADED
>10块：退出正常路由
```

阈值按链配置。

## 9. 富化任务

### 9.1 Token Metadata

输入：

```sql
DISTINCT chain_id, token_address
FROM token_transfers
```

查询：

```text
name()
symbol()
decimals()
totalSupply()
```

缓存键：

```text
(chain_id, token_address)
```

状态：

```text
PENDING
SUCCESS
PARTIAL
UNKNOWN
FAILED_TEMPORARY
FAILED_PERMANENT
```

规则：

- 每个 Token 自动查询一次；
- 成功结果长期缓存；
- 未取得 decimals 时不生成格式化 amount；
- 永久保留 `amount_raw`；
- RPC 失败不污染事实数据；
- 支持手动重新富化。

### 9.2 EOA / 合约识别

调用：

```text
eth_getCode(address, latest)
```

结果：

```text
EOA
CONTRACT
UNKNOWN
```

页面显示“当前状态”，不宣称永久身份。

### 9.3 当前原生币余额

调用：

```text
eth_getBalance(address, latest)
```

仅在地址页打开、用户主动刷新或缓存到期时调用。默认 TTL 60～300 秒。

### 9.4 Token 余额

调用：

```text
balanceOf(address)
```

禁止地址 × 全链 Token 笛卡尔积。只查询：

- 该地址历史实际交互过的 Token；
- 默认 Top 20；
- 用户展开时再加载剩余资产。

### 9.5 Receipt 补漏

SQD 数据优先。仅在本地确实缺少字段时调用：

```text
eth_getTransactionReceipt
```

结果永久缓存，不对全部交易重复拉取。

## 10. Batch 策略

建议批量大小：

```text
Metadata：20～50
getCode：20～50
原生余额：10～30
Receipt：20～50
```

不同供应商可单独配置。

Batch 失败：

1. 拆成一半；
2. 重试；
3. 最小拆成单请求；
4. 记录具体失败项。

## 11. 前端设计

入口：

```text
虚拟币
└─ RPC 节点管理
```

也可在“链上地址分析”右上角增加“RPC 富化设置”。

### 11.1 视觉规范

延续现有页面：

- 白色卡片；
- 浅灰蓝背景；
- 深蓝标题；
- 青蓝健康状态；
- 紫色品牌辅助；
- 紧凑表格；
- 桌面双栏、移动单栏；
- 无横向滚动。

### 11.2 顶部概览

显示：

```text
已配置节点
健康节点
降级节点
今日请求
缓存命中率
限流次数
```

### 11.3 节点卡片

```text
NodeReal · BSC
主节点

状态：健康
延迟：128 ms
成功率：99.8%
最新区块：112,932,400
落后：0
当前RPS：3.2 / 4.0

[测试连接] [编辑] [禁用] [查看指标]
```

状态颜色：

- 绿色：HEALTHY；
- 黄色：DEGRADED；
- 橙色：RATE_LIMITED；
- 红色：UNAVAILABLE；
- 灰色：DISABLED。

### 11.4 新增节点弹窗

字段：

```text
供应商
链
显示名称
完整 Endpoint URL
优先级
最大 RPS
最大并发
请求超时
是否启用
```

供应商：

```text
Chainstack
Ankr
NodeReal
Custom
```

用户粘贴完整 endpoint，避免写死三家 URL 模板。

输入体验：

- 密码模式；
- 显示/隐藏；
- 粘贴按钮；
- 自动去空格；
- 保存前测试；
- 验证 HTTPS；
- 校验 `eth_chainId`。

### 11.5 测试结果

成功：

```text
连接成功
供应商：NodeReal
链：BNB Smart Chain
Chain ID：56
最新区块：112,932,400
延迟：132 ms
```

失败：

```text
连接失败
类型：认证失败
建议：检查 API Key，或确认该 Key 已启用当前链。
```

不得显示完整 endpoint 或 secret。

### 11.6 路由策略

按链拖拽排序：

```text
BSC
1. NodeReal
2. Chainstack
3. Ankr
```

支持：

- 拖动优先级；
- 自动故障转移；
- 自动限速；
- 手动主节点；
- 一键恢复推荐配置。

### 11.7 富化任务卡

```text
Token Metadata 富化

总数：3,216
成功：3,108
未知：73
失败：35
缓存命中：2,877
RPC请求：339

[暂停] [继续] [重试失败项]
```

底部安全提示：

```text
API Key 仅提交给本机后端并加密保存。
浏览器不会保存或直接使用 API Key。
历史数据仍由 SQD / Parquet 获取。
```

## 12. 前端组件

```text
frontend/src/features/rpc/
├── RpcSettingsPage.tsx
├── RpcOverviewCards.tsx
├── RpcEndpointCard.tsx
├── RpcEndpointDialog.tsx
├── RpcConnectionTest.tsx
├── RpcRoutingBoard.tsx
├── RpcMetricsChart.tsx
├── EnrichmentJobCard.tsx
├── SecretInput.tsx
├── rpc-api.ts
├── rpc-types.ts
└── rpc-settings.css
```

要求：

- 不使用原生 `alert`；
- 表单错误就近显示；
- 异步操作有 loading；
- 防重复提交；
- 移动端点击区域 ≥ 44px；
- 状态同时使用文字和颜色；
- 支持键盘操作；
- Playwright 覆盖桌面和移动端。

## 13. API 设计

```http
GET    /api/crypto/rpc/endpoints
POST   /api/crypto/rpc/endpoints
PATCH  /api/crypto/rpc/endpoints/{endpoint_id}
DELETE /api/crypto/rpc/endpoints/{endpoint_id}

POST   /api/crypto/rpc/test
POST   /api/crypto/rpc/endpoints/{endpoint_id}/test
GET    /api/crypto/rpc/health
PUT    /api/crypto/rpc/routing/{chain_key}

POST   /api/crypto/enrichment/jobs
GET    /api/crypto/enrichment/jobs/{job_id}
POST   /api/crypto/enrichment/jobs/{job_id}/cancel
```

节点列表 API 只返回脱敏数据。更新接口只有明确提交新 secret 时才替换旧 secret。

## 14. 日志与隐私

日志目录：

```text
E:\codex\bsc_analytics\logs
```

必须脱敏：

- URL 中的 API Key；
- query token；
- Authorization；
- Basic Auth；
- JSON secret；
- 上游错误中回显的 endpoint。

允许记录：

```text
provider
chain_id
endpoint_id
host
method
status
latency
error_class
```

## 15. 缓存策略

| 数据 | TTL |
|---|---|
| Token Metadata | 长期，支持手动刷新 |
| Receipt | 永久 |
| 地址当前类型 | 24小时，记录检测区块 |
| 当前原生币余额 | 1～5分钟 |
| Token余额 | 1～5分钟 |
| 确定性 Metadata 失败 | 7～30天 |
| 429/503/timeout | 短期退避 |

## 16. 数据一致性

分层：

```text
raw facts
normalized facts
enrichment
presentation
```

示例：

```text
amount_raw：事实层，永不修改
decimals：富化层
amount_decimal：展示层计算
```

RPC 富化不能覆盖 SQD / Parquet 的原始事实。

## 17. 验收场景

必须测试：

1. NodeReal 正常时 BSC 优先使用 NodeReal；
2. NodeReal 返回 429 时降速并切 Chainstack；
3. Chainstack 超时时切 Ankr；
4. 三节点全部失败时任务可恢复；
5. API Key 错误时标记 MISCONFIGURED；
6. chain ID 不符时拒绝保存或启用；
7. 服务重启后密钥可解密；
8. 前端拿不到完整密钥；
9. 日志不包含密钥；
10. C 盘路径被拒绝；
11. RPC 失败不影响 SQD / Parquet 下载；
12. 重复 Metadata 富化命中缓存；
13. 取消任务后请求及时停止；
14. Batch 部分失败可拆分重试；
15. DuckDB 并发读写不出现文件锁异常。

## 18. 性能目标

```text
Metadata缓存查询：< 50ms
RPC配置列表：< 300ms
节点测试：通常 < 5秒
健康面板：< 1秒
地址页已缓存富化：< 1秒
```

后台默认：

```text
总并发：8
每节点并发：2～4
DuckDB写入：单写队列
富化CPU目标：< 30%
```

优先级：

```text
用户页面查询
> SQD / Parquet 主任务
> 后台 RPC 富化
```

## 19. Codex 实施阶段

### Phase 1：安全配置与节点管理

- DPAPI / AES-GCM；
- 增删改查；
- 节点管理页；
- 测试连接；
- 密钥脱敏；
- C 盘保护。

### Phase 2：高可用 RPC Manager

- 路由；
- 限速；
- 自适应并发；
- 重试；
- 熔断；
- 健康评分；
- 主备切换；
- 指标。

### Phase 3：Token Metadata

- 唯一 Token 提取；
- Metadata 查询；
- 永久缓存；
- UNKNOWN / PARTIAL；
- 前端进度。

### Phase 4：地址类型与余额

- `eth_getCode`；
- `eth_getBalance`；
- 按需 `balanceOf`；
- TTL；
- 地址页融合。

### Phase 5：Receipt 补漏

- 只补缺失字段；
- SQD 优先；
- 永久缓存；
- 不全量重复请求。

### Phase 6：监控与 UI 完善

- 健康面板；
- 延迟和成功率；
- 429 统计；
- 缓存命中率；
- 路由拖拽；
- Playwright 验证。

## 20. Codex 强制要求

1. 项目路径固定为 `E:\codex\bsc_analytics`。
2. 禁止写入 C 盘。
3. 保留 SQD / Parquet 主架构。
4. RPC 不得用于历史批量下载。
5. API Key 不得保存在浏览器。
6. API Key 必须加密落盘。
7. API 不得返回原始 API Key。
8. 日志不得包含完整 endpoint 或 secret。
9. 所有 RPC 请求必须有超时、限速和取消。
10. 所有读取请求支持故障转移。
11. 认证失败不得无限重试。
12. RPC 失败不得阻塞主 ETL。
13. 所有富化结果必须缓存。
14. 原始事实与富化数据必须分层。
15. 所有新增表包含 `chain_key` 和 `chain_id`。
16. 支持 Chainstack、Ankr、NodeReal、Custom。
17. 不写死供应商 endpoint 模板。
18. 保存前校验 `eth_chainId`。
19. 前端响应式、无横向溢出。
20. 所有新增模块必须通过 Go 测试、`go vet`、后端构建、前端构建和 Playwright。

## 21. 完成标准

完成后系统应达到：

```text
历史数据：SQD / AWS Parquet
富化节点：NodeReal + Chainstack + Ankr
稳定性：限速 + 重试 + 熔断 + 主备切换 + 健康评分
安全性：前端录入 + 后端加密 + 脱敏展示 + 日志清洗
富化能力：Metadata + 地址类型 + 当前余额 + Token余额 + Receipt补漏
用户体验：美观节点管理页 + 连接测试 + 路由配置 + 实时指标
```

该子系统是现有数据平台的可选富化基础设施，不改变或拖慢 SQD / Parquet 历史数据下载和本地分析主链路。
