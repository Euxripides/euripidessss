# SQD Cloud Processor-only 部署实施文档（BSC USDT 稳定性与计费用量测试）

> **目标部署目录：** `E:\Code\Processor-only`  
> **部署形态：** SQD Cloud Professional Organization + Dedicated `small` Processor  
> **数据链：** BNB Smart Chain（`binance-mainnet`，Chain ID 56）  
> **测试数据：** BSC USDT `Transfer` 事件  
> **输出方式：** Parquet 写入 Processor 临时文件系统（仅用于部署、稳定性和用量测试）  
> **不部署：** PostgreSQL、GraphQL/API、RPC Add-on  
> **文档日期：** 2026-08-07

---

## 1. 本次部署要验证什么

本次先做一个低成本的 Processor-only 冒烟测试，不直接建设完整的生产数据导出系统。

需要验证：

1. Professional Organization 是否能创建 Dedicated Processor。
2. SQD Cloud 是否开始记录 Processor 计算用量。
3. Cloud Processor 访问 BSC Portal 时是否稳定。
4. 是否持续推进区块游标，而不是长时间无进度。
5. 是否出现连续的 `429`、`500`、`503`、网络超时或容器重启。
6. Processor 重启后，文件存储的 `status.txt` 是否能支持断点恢复。
7. 停止或删除部署后，是否不再继续产生计算用量。

> **重要说明：**  
> 创建并运行 Processor 可以验证“付费资源是否正常开通、用量是否累计”，但不一定会立即向银行卡发起正式结算扣款。实际扣款时间取决于 SQD Cloud 的账单周期。不要为了测试扣款而制造数百万次 RPC 请求。

---

## 2. 最终部署结构

```text
E:\Code\Processor-only
├── abi\
├── src\
│   ├── abi\
│   └── main.ts
├── data\                   # 仅本地测试时生成
├── commands.json
├── package.json
├── package-lock.json
├── squid.yaml
├── tsconfig.json
├── .gitignore
├── .squidignore
└── logs\
```

Cloud 侧只部署：

```text
SQD Cloud
└── bsc-usdt-smoke
    └── Dedicated small Processor
        ├── BSC Portal
        ├── USDT Transfer 过滤
        └── 临时 Parquet 文件输出
```

明确不部署：

```text
PostgreSQL：关闭
GraphQL API：关闭
Hasura：关闭
RPC Add-on：关闭
```

---

## 3. 官方资料

- SQD Cloud：<https://cloud.sqd.dev/>
- SQD CLI 安装与认证：<https://docs.sqd.dev/en/cloud/reference/cli/installation>
- `sqd deploy`：<https://docs.sqd.dev/en/cloud/reference/cli/deploy>
- Deployment Manifest：<https://docs.sqd.dev/en/cloud/reference/manifest>
- Scale / Dedicated Processor：<https://docs.sqd.dev/en/cloud/reference/scale>
- BNB Smart Chain 数据集：<https://docs.sqd.dev/en/data/evm/binance-mainnet>
- EVM Portal Stream：<https://docs.sqd.dev/en/sdk/squid-sdk/evm/reference/evm-stream>
- Parquet 文件存储：<https://docs.sqd.dev/en/sdk/squid-sdk/reference/store/file/parquet>
- 文件存储与断点状态：<https://docs.sqd.dev/en/sdk/squid-sdk/resources/persisting-data/file>
- S3-compatible 输出：<https://docs.sqd.dev/en/sdk/squid-sdk/reference/store/file/s3-dest>
- 官方 Parquet 示例：<https://github.com/subsquid-labs/file-store-parquet-example>

BSC Portal 地址：

```text
https://portal.sqd.dev/datasets/binance-mainnet
```

BSC USDT 合约：

```text
0x55d398326f99059ff775485246999027b3197955
```

ERC-20 `Transfer(address,address,uint256)` Topic：

```text
0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef
```

---

# 第一阶段：准备 Windows 环境

## 4. 软件要求

建议使用：

| 软件 | 建议版本 |
|---|---|
| Windows | Windows 10/11 x64 |
| Node.js | 22 LTS |
| npm | Node.js 自带的当前版本 |
| Git | 当前稳定版 |
| SQD CLI | `@subsquid/cli@latest` |
| PowerShell | 5.1 或 PowerShell 7 |

本次不使用 PostgreSQL，因此不要求安装 Docker。

---

## 5. 检查环境

以普通 PowerShell 打开终端：

```powershell
node --version
npm --version
git --version
```

Node.js 应为：

```text
v22.x.x
```

安装或更新 SQD CLI：

```powershell
npm install -g @subsquid/cli@latest
sqd --version
```

检查 CLI 帮助：

```powershell
sqd --help
sqd deploy --help
sqd logs --help
sqd remove --help
```

---

# 第二阶段：建立 `E:\Code\Processor-only`

## 6. 保护已有目录

不要直接删除已有项目。

```powershell
$Target = "E:\Code\Processor-only"

if (Test-Path $Target) {
    $items = Get-ChildItem -Force $Target -ErrorAction SilentlyContinue
    if ($items.Count -gt 0) {
        $backup = "E:\Code\Processor-only-backup-$(Get-Date -Format 'yyyyMMdd-HHmmss')"
        Rename-Item -Path $Target -NewName (Split-Path $backup -Leaf)
        Write-Host "原目录已备份到：$backup"
    }
}
```

创建父目录：

```powershell
New-Item -ItemType Directory -Force -Path "E:\Code" | Out-Null
```

---

## 7. 克隆官方 Parquet 示例

```powershell
git clone `
  https://github.com/subsquid-labs/file-store-parquet-example.git `
  "E:\Code\Processor-only"

Set-Location "E:\Code\Processor-only"
npm install
```

确认目录：

```powershell
Get-ChildItem -Force
```

---

# 第三阶段：替换 Processor 代码

## 8. 修改 `src\main.ts`

将：

```text
E:\Code\Processor-only\src\main.ts
```

完整替换为以下内容：

```typescript
import * as erc20abi from './abi/erc20'
import {run} from '@subsquid/batch-processor'
import {augmentBlock} from '@subsquid/evm-objects'
import {
    DataSourceBuilder,
    type FieldSelection,
} from '@subsquid/evm-stream'
import {Database, LocalDest} from '@subsquid/file-store'
import {
    Column,
    Table,
    Types,
} from '@subsquid/file-store-parquet'
import {createLogger} from '@subsquid/logger'

const DEFAULT_PORTAL_URL =
    'https://portal.sqd.dev/datasets/binance-mainnet'

const DEFAULT_TOKEN_CONTRACT =
    '0x55d398326f99059ff775485246999027b3197955'

const PORTAL_URL =
    process.env.PORTAL_URL?.trim() || DEFAULT_PORTAL_URL

const TOKEN_CONTRACT =
    (
        process.env.TOKEN_CONTRACT?.trim() ||
        DEFAULT_TOKEN_CONTRACT
    ).toLowerCase()

const TEST_LOOKBACK_BLOCKS = parsePositiveInteger(
    process.env.TEST_LOOKBACK_BLOCKS,
    50_000,
    'TEST_LOOKBACK_BLOCKS',
)

const FORCE_FLUSH_BLOCKS = parsePositiveInteger(
    process.env.FORCE_FLUSH_BLOCKS,
    5_000,
    'FORCE_FLUSH_BLOCKS',
)

const OUTPUT_DIRECTORY =
    process.env.OUTPUT_DIRECTORY?.trim() || './data'

const logger = createLogger('sqd:bsc-usdt-smoke')

function parsePositiveInteger(
    input: string | undefined,
    fallback: number,
    fieldName: string,
): number {
    if (input == null || input.trim() === '') {
        return fallback
    }

    const value = Number(input)

    if (!Number.isSafeInteger(value) || value <= 0) {
        throw new Error(
            `${fieldName} must be a positive safe integer; received: ${input}`,
        )
    }

    return value
}

function parseOptionalBlock(
    input: string | undefined,
    fieldName: string,
): number | undefined {
    if (input == null || input.trim() === '') {
        return undefined
    }

    const value = Number(input)

    if (!Number.isSafeInteger(value) || value < 0) {
        throw new Error(
            `${fieldName} must be a non-negative safe integer; received: ${input}`,
        )
    }

    return value
}

interface PortalHead {
    number: number
    hash: string
}

async function getFinalizedHead(): Promise<PortalHead> {
    const url = `${PORTAL_URL}/finalized-head`

    const response = await fetch(url, {
        method: 'GET',
        headers: {
            accept: 'application/json',
        },
        signal: AbortSignal.timeout(30_000),
    })

    if (!response.ok) {
        const body = await response.text().catch(() => '')

        throw new Error(
            `Portal finalized-head failed: HTTP ${response.status} ${response.statusText}; body=${body.slice(0, 500)}`,
        )
    }

    const value = (await response.json()) as PortalHead | null

    if (
        value == null ||
        !Number.isSafeInteger(value.number) ||
        value.number < 0 ||
        typeof value.hash !== 'string'
    ) {
        throw new Error(
            `Portal returned an invalid finalized head: ${JSON.stringify(value)}`,
        )
    }

    return value
}

async function resolveStartBlock(): Promise<{
    startBlock: number
    finalizedHead: PortalHead
}> {
    const finalizedHead = await getFinalizedHead()

    const configuredFromBlock = parseOptionalBlock(
        process.env.FROM_BLOCK,
        'FROM_BLOCK',
    )

    const startBlock =
        configuredFromBlock ??
        Math.max(0, finalizedHead.number - TEST_LOOKBACK_BLOCKS)

    if (startBlock > finalizedHead.number) {
        throw new Error(
            `FROM_BLOCK ${startBlock} is above finalized head ${finalizedHead.number}`,
        )
    }

    return {
        startBlock,
        finalizedHead,
    }
}

const fields = {
    block: {
        timestamp: true,
    },
    log: {
        address: true,
        topics: true,
        data: true,
        transactionHash: true,
        logIndex: true,
    },
} satisfies FieldSelection

async function main(): Promise<void> {
    const {startBlock, finalizedHead} = await resolveStartBlock()

    logger.info(
        {
            portalUrl: PORTAL_URL,
            tokenContract: TOKEN_CONTRACT,
            startBlock,
            finalizedHead: finalizedHead.number,
            lookbackBlocks: TEST_LOOKBACK_BLOCKS,
            outputDirectory: OUTPUT_DIRECTORY,
            forceFlushBlocks: FORCE_FLUSH_BLOCKS,
        },
        'starting BSC USDT Processor-only smoke test',
    )

    const dataSource = new DataSourceBuilder()
        .setPortal(PORTAL_URL)
        .setBlockRange({
            from: startBlock,
        })
        .setFields(fields)
        .addLog({
            where: {
                address: [TOKEN_CONTRACT],
                topic0: [erc20abi.events.Transfer.topic],
            },
        })
        .build()

    const database = new Database({
        tables: {
            Transfers: new Table(
                'transfers.parquet',
                {
                    chain_id: Column(Types.Int32()),
                    block_number: Column(Types.Int64()),
                    block_timestamp: Column(Types.Int64()),
                    transaction_hash: Column(Types.String()),
                    log_index: Column(Types.Int32()),
                    token_address: Column(Types.String()),
                    from_address: Column(Types.String()),
                    to_address: Column(Types.String()),
                    value_raw: Column(Types.String()),
                },
                {
                    compression: 'GZIP',
                    rowGroupSize: 32 * 1024 * 1024,
                    pageSize: 8 * 1024,
                },
            ),
        },
        dest: new LocalDest(OUTPUT_DIRECTORY),
        chunkSizeMb: 16,
        syncIntervalBlocks: 20_000,
    })

    let blocksSinceLastForcedFlush = 0
    let totalBlocks = 0
    let totalTransfers = 0
    let highestBlock = startBlock - 1

    await run(dataSource, database, async (ctx) => {
        const blocks = ctx.blocks.map(augmentBlock)
        let batchTransfers = 0

        for (const block of blocks) {
            highestBlock = Math.max(
                highestBlock,
                block.header.number,
            )

            for (const log of block.logs) {
                if (
                    log.address !== TOKEN_CONTRACT ||
                    log.topics[0] !== erc20abi.events.Transfer.topic
                ) {
                    continue
                }

                const decoded =
                    erc20abi.events.Transfer.decode(log)

                ctx.store.Transfers.write({
                    chain_id: 56,
                    block_number: block.header.number,
                    block_timestamp: block.header.timestamp,
                    transaction_hash:
                        log.transactionHash ?? '',
                    log_index: log.logIndex,
                    token_address: TOKEN_CONTRACT,
                    from_address: decoded.from.toLowerCase(),
                    to_address: decoded.to.toLowerCase(),
                    value_raw: decoded.value.toString(),
                })

                batchTransfers += 1
            }
        }

        totalBlocks += blocks.length
        totalTransfers += batchTransfers
        blocksSinceLastForcedFlush += blocks.length

        if (
            blocksSinceLastForcedFlush >= FORCE_FLUSH_BLOCKS ||
            ctx.isHead
        ) {
            ctx.store.setForceFlush()
            blocksSinceLastForcedFlush = 0
        }

        logger.info(
            {
                batchBlocks: blocks.length,
                batchTransfers,
                totalBlocks,
                totalTransfers,
                highestBlock,
                isHead: ctx.isHead,
            },
            'processed BSC batch',
        )
    })
}

main().catch((error: unknown) => {
    const normalized =
        error instanceof Error
            ? {
                  name: error.name,
                  message: error.message,
                  stack: error.stack,
              }
            : {
                  value: String(error),
              }

    logger.fatal(
        {
            error: normalized,
        },
        'processor terminated',
    )

    process.exitCode = 1
})
```

---

## 9. 为什么 `value_raw` 使用字符串

ERC-20 的 `uint256` 最大值远高于 JavaScript 安全整数范围，也可能超过 Parquet `Uint64`。

因此本测试使用：

```typescript
value_raw: Column(Types.String())
```

并写入：

```typescript
value_raw: decoded.value.toString()
```

这样不会因为代币原始数量过大而溢出。

生产阶段可根据 DuckDB、PyArrow 和业务精度要求改为：

```text
DECIMAL(38,0)
DECIMAL(76,0)
VARCHAR
```

---

# 第四阶段：配置跨平台命令

## 10. 检查 `commands.json`

将：

```text
E:\Code\Processor-only\commands.json
```

设置为：

```json
{
  "$schema": "https://cdn.subsquid.io/schemas/commands.json",
  "commands": {
    "clean": {
      "description": "delete all build artifacts",
      "cmd": [
        "npx",
        "--yes",
        "rimraf",
        "lib"
      ]
    },
    "build": {
      "description": "Build the squid project",
      "deps": [
        "clean"
      ],
      "cmd": [
        "tsc"
      ]
    },
    "typegen": {
      "description": "Generate data access classes for ABI files",
      "cmd": [
        "squid-evm-typegen",
        "./src/abi",
        {
          "glob": "./abi/*.json"
        },
        "--multicall"
      ]
    },
    "process": {
      "description": "Load .env and start the squid processor",
      "deps": [
        "build"
      ],
      "cmd": [
        "node",
        "--require=dotenv/config",
        "lib/main.js"
      ]
    },
    "process:prod": {
      "description": "Start the squid processor",
      "cmd": [
        "node",
        "lib/main.js"
      ],
      "hidden": true
    }
  }
}
```

不要优先使用官方示例中的：

```powershell
npm run build
```

因为某些模板的 `package.json` 仍可能使用：

```text
rm -rf lib
```

该命令在纯 Windows PowerShell 环境下可能失败。

本项目统一使用：

```powershell
sqd build
sqd process
```

---

# 第五阶段：配置 Processor-only Manifest

## 11. 替换 `squid.yaml`

将：

```text
E:\Code\Processor-only\squid.yaml
```

完整替换为：

```yaml
manifestVersion: subsquid.io/v0.1
name: bsc-usdt-smoke
version: 1
description: |-
  Dedicated Processor-only smoke test for BSC USDT Transfer indexing.
  No PostgreSQL, API, Hasura or RPC add-on is deployed.

build:
  nodeVersion: 22
  packageManager: npm

deploy:
  env:
    PORTAL_URL: "https://portal.sqd.dev/datasets/binance-mainnet"
    TOKEN_CONTRACT: "0x55d398326f99059ff775485246999027b3197955"
    TEST_LOOKBACK_BLOCKS: "50000"
    FORCE_FLUSH_BLOCKS: "5000"
    OUTPUT_DIRECTORY: "./data"
    SQD_DEBUG: "sqd:*"

  processor:
    cmd:
      - "sqd"
      - "process:prod"

scale:
  dedicated: true

  processor:
    profile: small
```

## 12. Manifest 核心解释

```yaml
deploy:
  processor:
```

只定义 Processor。

文件中没有：

```yaml
deploy:
  addons:
    postgres:
```

也没有：

```yaml
deploy:
  api:
```

因此不会部署 PostgreSQL 和 GraphQL/API。

```yaml
scale:
  dedicated: true
```

表示使用 Dedicated 资源，而不是共享型 Collocated 资源。

```yaml
processor:
  profile: small
```

表示先使用最低 Dedicated Processor 规格做低成本测试。

> SQD 官方当前模板使用 `manifestVersion`、`nodeVersion`、`packageManager`。  
> 如果 CLI 返回 Manifest 字段校验错误，先执行：
>
> ```powershell
> npm update -g @subsquid/cli
> sqd --version
> ```
>
> 不要在未查看错误信息的情况下反复修改 Manifest 字段名。

---

# 第六阶段：本地编译和冒烟测试

## 13. 清理旧数据

第一次测试前：

```powershell
Set-Location "E:\Code\Processor-only"

if (Test-Path ".\data") {
    Remove-Item ".\data" -Recurse -Force
}

if (Test-Path ".\lib") {
    Remove-Item ".\lib" -Recurse -Force
}

New-Item -ItemType Directory -Force -Path ".\logs" | Out-Null
```

---

## 14. 编译

```powershell
sqd build
```

必须满足：

```text
退出码为 0
没有 TypeScript 编译错误
生成 E:\Code\Processor-only\lib
```

检查：

```powershell
Test-Path "E:\Code\Processor-only\lib\main.js"
```

预期：

```text
True
```

---

## 15. 本地运行 3～10 分钟

```powershell
sqd process 2>&1 |
    Tee-Object `
      -FilePath ".\logs\local-smoke-$(Get-Date -Format 'yyyyMMdd-HHmmss').log"
```

正常日志应持续出现：

```text
starting BSC USDT Processor-only smoke test
processed BSC batch
batchBlocks
batchTransfers
totalBlocks
totalTransfers
highestBlock
isHead
```

观察 3～10 分钟后按：

```text
Ctrl + C
```

---

## 16. 验证本地输出

```powershell
Get-ChildItem ".\data" -Recurse -Force
```

应至少看到：

```text
status.txt
```

当累计数据达到分区条件或触发强制 Flush 后，应看到：

```text
transfers.parquet
```

查看断点状态：

```powershell
Get-Content ".\data\status.txt"
```

重新执行：

```powershell
sqd process
```

确认它从 `status.txt` 记录的进度继续，而不是重新扫描全部区块。

---

# 第七阶段：SQD Cloud 认证

## 17. 获取 Deployment Key

打开：

```text
https://cloud.sqd.dev/
```

依次进入：

```text
右上角头像
→ Deployment key
→ 创建或刷新 Deployment Key
→ 复制
```

注意：

```text
Deployment Key ≠ RPC API Token
Deployment Key ≠ Portal API Token
```

部署 CLI 使用的是 **Deployment Key**。

---

## 18. CLI 登录

```powershell
sqd auth -k "你的_DEPLOYMENT_KEY"
```

验证：

```powershell
sqd whoami
```

确认当前账号能够看到已绑定付款方式的 Professional Organization。

---

# 第八阶段：部署到 Professional Organization

## 19. 获取 Organization Code

可在 Cloud 网页的组织设置或 CLI 交互界面查看组织 Code。

记录：

```text
ORG_CODE=<你的组织Code>
```

PowerShell 临时变量：

```powershell
$OrgCode = "你的组织Code"
```

---

## 20. 正式部署

```powershell
Set-Location "E:\Code\Processor-only"

sqd deploy . `
  -o $OrgCode `
  --stream-logs
```

也可以不指定组织，使用交互选择：

```powershell
sqd deploy . --stream-logs
```

必须选择：

```text
Professional Organization
```

不要选择：

```text
Playground
```

部署过程：

```text
上传项目
→ 安装 npm 依赖
→ 编译 TypeScript
→ 创建 Dedicated small Processor
→ 启动 Processor
→ 连接 BSC Portal
→ 持续处理 USDT Transfer
```

---

## 21. 查看部署和 Slot

新开一个 PowerShell：

```powershell
$OrgCode = "你的组织Code"

sqd list -o $OrgCode
```

或：

```powershell
sqd ls -o $OrgCode
```

记录：

```text
Squid name：bsc-usdt-smoke
Slot：<SLOT_ID>
```

设置变量：

```powershell
$SquidName = "bsc-usdt-smoke"
$Slot = "实际Slot ID"
```

---

## 22. 查看实时日志

```powershell
sqd logs `
  -o $OrgCode `
  -n $SquidName `
  -s $Slot `
  -c processor `
  --since 2h `
  --follow
```

保存日志：

```powershell
New-Item -ItemType Directory -Force -Path `
  "E:\Code\Processor-only\logs" | Out-Null

sqd logs `
  -o $OrgCode `
  -n $SquidName `
  -s $Slot `
  -c processor `
  --since 2h `
  --follow 2>&1 |
    Tee-Object `
      -FilePath `
      "E:\Code\Processor-only\logs\cloud-$Slot-$(Get-Date -Format 'yyyyMMdd-HHmmss').log"
```

---

# 第九阶段：稳定性测试

## 23. 第一轮：1 小时低成本测试

建议运行：

```text
至少 60 分钟
```

重点观察：

| 指标 | 通过标准 |
|---|---|
| Build | Success |
| Processor | Running |
| 区块游标 | 持续增长 |
| 最长无进度 | 不超过 10 分钟 |
| 容器重启 | 0 次，或异常后能自动恢复 |
| 最终失败 | 0 |
| 连续 503 | 不应长期持续 |
| 内存 | 不应长期逼近 small 上限 |
| Billing Usage | 出现 Processor 用量 |

---

## 24. 第二轮：6～24 小时稳定性测试

第一轮通过后，再考虑延长。

推荐配置保持：

```text
small Dedicated
50,000 block lookback
1 个 Processor
无 API
无 PostgreSQL
```

建议记录：

```text
test_start_at
test_end_at
processed_blocks
processed_transfers
highest_block
processor_restarts
http_429_count
http_500_count
http_503_count
timeout_count
max_no_progress_seconds
final_status
```

---

## 25. 日志错误分类

不要只统计“SQD 失败”，应区分：

```text
BUILD_ERROR
PROCESSOR_CRASH
PORTAL_HTTP_429
PORTAL_HTTP_500
PORTAL_HTTP_503
PORTAL_TIMEOUT
DNS_ERROR
CONNECTION_RESET
OUT_OF_MEMORY
PARQUET_WRITE_ERROR
DISK_ERROR
MANIFEST_ERROR
AUTH_ERROR
BILLING_OR_ORG_ERROR
```

PowerShell 快速统计：

```powershell
$log = Get-ChildItem `
  "E:\Code\Processor-only\logs\cloud-*.log" |
  Sort-Object LastWriteTime -Descending |
  Select-Object -First 1

Select-String `
  -Path $log.FullName `
  -Pattern "429|500|503|timeout|ECONNRESET|ENOTFOUND|fatal|error|out of memory" `
  -CaseSensitive:$false
```

---

# 第十阶段：验证用量和付款状态

## 26. 检查 Billing / Usage

在 SQD Cloud 中进入：

```text
Organization
→ Billing / Usage
```

检查是否出现：

```text
Dedicated Processor
small profile
运行时长
计算用量
预估费用
```

这里验证的是：

```text
付款方式已绑定
Professional 资源可部署
计费用量可以正常累计
```

不等于：

```text
银行卡已经立即完成正式扣款
```

正式结算仍可能在账单周期结束后发生。

---

# 第十一阶段：停止测试，防止继续计费

## 27. 删除 Cloud 部署

确认 Slot：

```powershell
sqd list -o $OrgCode -n $SquidName
```

删除指定 Slot：

```powershell
sqd remove `
  -o $OrgCode `
  -n $SquidName `
  -s $Slot
```

无交互强制删除：

```powershell
sqd remove `
  -o $OrgCode `
  -n $SquidName `
  -s $Slot `
  --force
```

或者使用完整引用：

```powershell
sqd remove `
  -r "$OrgCode/$SquidName@$Slot"
```

删除后再次检查：

```powershell
sqd list -o $OrgCode
```

同时在 Cloud 网页确认：

```text
没有 Running Processor
没有 Starting Processor
没有旧 Slot 继续运行
```

> 测试结束后不要只关闭 PowerShell。  
> 关闭本地终端不会停止 Cloud Processor，必须在 Cloud 或使用 `sqd remove` 删除部署。

---

# 第十二阶段：故障处理

## 28. Manifest 校验失败

执行：

```powershell
npm update -g @subsquid/cli
sqd --version
sqd deploy --help
```

检查：

```text
name 只能使用小写字母和连字符
name 长度不超过平台限制
version 是整数
processor.cmd 存在
没有 api
没有 postgres
```

当前名称：

```text
bsc-usdt-smoke
```

符合要求。

---

## 29. Windows 编译提示 `rm` 不存在

不要使用：

```powershell
npm run build
```

改用：

```powershell
sqd build
```

因为 `commands.json` 使用：

```text
npx rimraf lib
```

可以跨平台执行。

---

## 30. Portal `finalized-head` 失败

检查：

```powershell
Invoke-RestMethod `
  -Uri "https://portal.sqd.dev/datasets/binance-mainnet/finalized-head" `
  -Method Get
```

若本地失败但 Cloud 成功，可能是：

```text
本地网络
代理
DNS
HTTPS 扫描
防火墙
安全软件
```

若 Cloud 内也持续失败，应记录：

```text
HTTP 状态码
响应正文
发生时间
持续时长
是否自动恢复
```

---

## 31. Processor 一直重启

查看最近错误：

```powershell
sqd logs `
  -o $OrgCode `
  -n $SquidName `
  -s $Slot `
  -c processor `
  --since 1h `
  --level error
```

优先排查：

```text
TypeScript 未编译
lib/main.js 不存在
Manifest cmd 错误
内存不足
Parquet Schema 类型错误
Portal 请求失败
临时磁盘写入失败
```

---

## 32. 没有生成 Parquet 文件

文件存储只有在以下条件之一满足时才会写分区：

1. 缓冲区达到 `chunkSizeMb`。
2. 调用了 `ctx.store.setForceFlush()`。
3. 达到配置的同步区块间隔。

本测试已设置：

```typescript
chunkSizeMb: 16
syncIntervalBlocks: 20_000
FORCE_FLUSH_BLOCKS: 5_000
```

本地检查：

```powershell
Get-ChildItem ".\data" -Recurse
```

Cloud 临时文件系统不会自动下载到本机，因此 Cloud 阶段主要通过日志、游标和 Billing 验证。

---

## 33. small 内存不足

如果出现明显 OOM：

```text
JavaScript heap out of memory
OOMKilled
container restarted
```

再把：

```yaml
scale:
  processor:
    profile: small
```

改为：

```yaml
scale:
  processor:
    profile: medium
```

然后将版本升级：

```yaml
version: 2
```

重新部署。

不要在没有 OOM 或明显 CPU 瓶颈时直接升级，以免增加测试费用。

---

# 第十三阶段：本次测试的限制

## 34. 临时文件不会成为正式数据资产

本测试使用：

```typescript
new LocalDest('./data')
```

Cloud Processor 中的本地目录属于临时运行环境。

因此本部署适合：

```text
验证 Professional 资源
验证 Cloud Processor
验证 Portal 稳定性
验证计费用量
验证断点机制
```

不适合：

```text
长期保存三年 BSC 数据
下载到本地 DuckDB
多案件共享缓存
正式生产数据资产
```

删除部署或更换 Slot 后，临时文件不应视为可靠的长期存储。

---

# 第十四阶段：正式生产版下一步

本次测试通过后，下一阶段改为：

```text
Professional Organization
└── medium Dedicated Processor
    ├── BSC Portal
    ├── 地址分组任务
    ├── Token Transfer / Transaction / Trace
    ├── Parquet
    ├── Cloudflare R2 / S3
    ├── Manifest + Checkpoint
    └── 本地 Smart Download Orchestrator
```

生产输出链路：

```text
SQD Cloud Processor
→ Cloudflare R2 / S3
→ 本地下载器
→ Parquet 校验
→ DuckDB 索引
→ 智能调查
→ 地址关系图
```

生产版必须增加：

```text
S3Dest / R2
任务 ID
地址分组
区块分片
Checkpoint
Manifest
SHA-256
行数校验
Schema 校验
失败分片重试
本地自动同步
Provider Router 容灾
```

---

# 第十五阶段：验收清单

## 35. 本地验收

- [ ] 项目位于 `E:\Code\Processor-only`
- [ ] Node.js 22 可用
- [ ] SQD CLI 已更新
- [ ] `npm install` 成功
- [ ] `sqd build` 成功
- [ ] `lib\main.js` 存在
- [ ] `sqd process` 可以运行
- [ ] 日志持续输出最高区块
- [ ] `data\status.txt` 存在
- [ ] 至少生成一个 Parquet 分区
- [ ] 重启后可以续跑

## 36. Cloud 验收

- [ ] 使用 Professional Organization
- [ ] 部署为 Dedicated
- [ ] Processor profile 为 small
- [ ] 未部署 PostgreSQL
- [ ] 未部署 API
- [ ] 未部署 RPC Add-on
- [ ] Processor 状态为 Running
- [ ] 区块游标持续增长
- [ ] 1 小时内没有持续性 503
- [ ] 没有重复崩溃或 OOM
- [ ] Billing / Usage 出现 Processor 用量
- [ ] 测试结束后已删除 Slot
- [ ] Cloud 中没有遗留 Running 资源

---

# 第十六阶段：Codex 执行要求

将本文件交给 Codex 后，要求其：

```text
严格在 E:\Code\Processor-only 下实施。
不得在 C: 盘创建项目数据目录。
不得加入 PostgreSQL、GraphQL、Hasura 或 RPC Add-on。
先完成本地编译和冒烟测试，再执行 SQD Cloud 部署。
任何删除操作前先备份已有目录。
不得将 Deployment Key、银行卡信息或 API Token 写入代码、日志或 Git。
部署后输出 Squid name、Organization code、Slot、启动时间和停止命令。
测试结束后必须检查并确认 Cloud 中没有继续运行的 Processor。
```

Codex 最终应提交：

```text
1. 修改后的完整项目
2. 本地构建结果
3. 本地冒烟测试日志
4. Cloud 部署命令
5. Cloud Slot 信息
6. 稳定性测试日志
7. 错误统计
8. Billing Usage 截图或记录
9. 删除部署后的资源确认
10. 下一阶段 R2/S3 持久化差距报告
```

---

## 最终执行顺序

```text
1. 安装 Node.js 22、Git、SQD CLI
2. 克隆官方 Parquet 示例到 E:\Code\Processor-only
3. 替换 src\main.ts
4. 替换 commands.json
5. 替换 squid.yaml
6. npm install
7. sqd build
8. sqd process
9. 验证 data\status.txt 和 Parquet
10. sqd auth
11. sqd deploy . -o <ORG_CODE> --stream-logs
12. 运行 1 小时
13. 检查日志和 Billing Usage
14. sqd remove 删除 Slot
15. 确认无 Running 资源
16. 决定是否进入 R2/S3 生产持久化阶段
```
