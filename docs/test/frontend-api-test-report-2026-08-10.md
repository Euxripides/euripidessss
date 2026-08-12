# 前端 API 全量测试报告

> 生成日期：2026-08-10 ｜ 测试对象：E:\codex\etl 前端实际调用的全部后端 API（http://127.0.0.1:8000）

## 1. 测试概述

本报告覆盖前端源码（frontend/src）中所有以 `/api/` 开头的调用端点，共 **219 个唯一端点模板**（方法分布：GET 128 / POST 83 / DELETE 7 / PATCH 1）。
- **批量用例**：按端点自动生成“正常 / 非法链 / 非法地址 / 非法哈希 / 非法区块 / 非法 Token / 未知 ID / 垃圾 ID / 查询参数边界 / 空请求体 / 坏 JSON / 错误方法”等用例，共 **637 个**；
- **有效参数回归**：对核心读接口使用真实数据（锚点地址、真实交易哈希、USDT 合约、真实时间窗）单飞复测 **61 个**；
- 判定标准：正常请求预期 2xx；非法/未知输入预期 4xx（拒绝即可，不苛求具体码）；任何 5xx、超时、或非法输入被静默接受均记入问题清单。

## 2. 测试环境

| 项 | 值 |
|---|---|
| 服务 | http://127.0.0.1:8000（etl-server PID 12828，run.ps1 启动） |
| 健康检查 | /api/health 200 |
| 数据面 | ClickHouse onchain（chain_transactions 553,582；token_transfers 22,752,034；address_activity 46,566,454） |
| 测试数据锚点 | 地址 0x92e102725a90a1ac0d60560cb1807b9c5820b0a9；交易 0x6097bd34...746af3；USDT 0x55d39832...b3197955 |
| 时间 | 2026-08-10 23:2x（Asia/Shanghai） |

## 3. 结果统计

### 3.1 批量用例（637）

| 分类 | 数量 | 说明 |
|---|---|---|
| 通过（正常 2xx） | 12 | 正常参数请求成功 |
| 通过（按预期拒绝 4xx） | 544 | 非法/未知输入被拒绝 |
| 失败（5xx） | 15 | 见 BUG-04/05/06 及 warehouse 503 类 |
| 不符合预期（正常请求非 2xx） | 16 | 多数为占位参数构造问题，详见 6.2 |
| 不符合预期（非法输入未拒绝） | 42 | 见 BUG-07/08/09/12 |
| 请求异常（超时等） | 7 | 客户端 25s 超时，服务端随后 503/500，见 BUG-01/06/10 |
| 跳过（破坏性） | 1 | DELETE /api/dune/batch/accounts |

### 3.2 有效参数回归（61）

| 结果 | 数量 |
|---|---|
| 通过（200/404 数据不存在） | 56 |
| 未通过 | 5 |
未通过明细：fifo-retention/fifo-pass-through 503（BUG-02）、pnl 400（BUG-03）、resolve 参数名应为 at（已复测 200）、/api/crypto/datasource 301 尾斜杠（契约观察）。

## 4. 用例设计说明

1. 清单来源：自动扫描 frontend/src 全部 .ts/.tsx 中的 API 字面量与 `${BASE}` 拼接，合并同方法同模板，共 219 个。
2. 值映射：chain=bsc / address=锚点地址 / tx_hash=真实哈希 / block=114004503 / token=USDT；非法值分别取 notachain、0x123、abc、00000000-...-0000、!!!bad id!!!。
3. 破坏性接口（POST/DELETE/PATCH）仅测校验与错误路径，不执行成功路径；唯一集合删除接口（DELETE /api/dune/batch/accounts）整体跳过。
4. SSE 接口（/api/smart-download/events、/api/intelligence/events）只读响应头判断是否为事件流。
5. 有效回归以并发 2 顺序执行，重接口超时放宽到 90s，避免把并发压力误判为接口故障。

## 5. 端点清单（按前端模块）

| 模块 | 方法 | 路径模板 | 参数 | 用例数 | 结论摘要 |
|---|---|---|---|---|---|
| Explorer v1 | GET | `/api/v1/explorer/:chain/address/:address` | chain, address | 4 | ? 1 非2xx |
| Explorer v1 | GET | `/api/v1/explorer/:chain/address/:address/:param?:param` | chain, address, param | 4 | 正常 |
| Explorer v1 | GET | `/api/v1/explorer/:chain/address/:address/daily-stats?:param` | chain, address, param | 4 | 正常 |
| Explorer v1 | GET | `/api/v1/explorer/:chain/contract/:address` | chain, address | 4 | ? 1 非2xx |
| Explorer v1 | GET | `/api/v1/explorer/:chain/token/:address` | chain, address | 4 | ? 1 非2xx |
| Explorer v1 | GET | `/api/v1/explorer/:chain/tx/:tx_hash` | chain, tx_hash | 4 | 正常 |
| Explorer v2 / 金融质量 | GET | `/api/v2/data-quality/:chain` | chain | 3 | ⚠ 1 失败 |
| Explorer v2 / 金融质量 | GET | `/api/v2/explorer/:chain/address/:address/header` | chain, address | 4 | 正常 |
| Explorer v2 / 金融质量 | GET | `/api/v2/explorer/:chain/block/:block` | chain, block | 4 | ? 1 非2xx |
| Explorer v2 / 金融质量 | GET | `/api/v2/explorer/:chain/home` | chain | 3 | ⚠ 1 失败 |
| Explorer v2 / 金融质量 | GET | `/api/v2/explorer/:chain/token/:address` | chain, address | 4 | ? 1 非2xx |
| Explorer v2 / 金融质量 | GET | `/api/v2/explorer/:chain/tx/:param` | chain, param | 3 | 正常 |
| Explorer v2 / 金融质量 | GET | `/api/v2/explorer/:chain/tx/:tx_hash` | chain, tx_hash | 4 | 正常 |
| Explorer v2 / 金融质量 | GET | `/api/v2/explorer/search?chain=:chain&q=:param` | chain, param | 4 | 正常 |
| Explorer v2 / 金融质量 | GET | `/api/v2/financial-quality/:chain?window=:param` | chain, param | 5 | ⚠ 2 失败 |
| Explorer v2 / 金融质量 | GET | `/api/v2/pricing/56/token/:token/gaps?from=:param&to=:param&resolution=:param` | token, param | 5 | 正常 |
| Financial Analytics v2 | GET | `/api/v2/analytics/:chain/address/:address/:param` | chain, address, param | 4 | 正常 |
| Financial Analytics v2 | GET | `/api/v2/analytics/:chain/address/:address/financial-counterparties?window=:param&limit=50` | chain, address, param | 7 | ? 3 未拒绝 |
| Financial Analytics v2 | GET | `/api/v2/analytics/:chain/address/:address/financial-summary?:param` | chain, address, param | 4 | 正常 |
| api/ai | POST | `/api/ai/analyze` | - | 3 | 正常 |
| api/crypto | POST | `/api/crypto/address-classify` | - | 3 | 正常 |
| api/crypto | GET | `/api/crypto/addresses/:chain/:address/first-seen` | chain, address | 4 | ⚠ 1 失败 |
| api/crypto | GET | `/api/crypto/datasource` | - | 1 | ? 1 未拒绝 |
| api/crypto | GET | `/api/crypto/datasource/:id` | id | 3 | 正常 |
| api/crypto | GET | `/api/crypto/download` | - | 1 | ? 1 未拒绝 |
| api/crypto | POST | `/api/crypto/download/cancel?id=:id:param` | id, param | 6 | 正常 |
| api/crypto | GET | `/api/crypto/download/file?id=:id&path=:id` | id | 3 | 正常 |
| api/crypto | GET | `/api/crypto/download/history` | - | 1 | 正常 |
| api/crypto | DELETE | `/api/crypto/download/history?id=:id` | id | 4 | 正常 |
| api/crypto | POST | `/api/crypto/download/history/import` | - | 3 | 正常 |
| api/crypto | POST | `/api/crypto/download/history/resume?id=:id` | id | 5 | 正常 |
| api/crypto | GET | `/api/crypto/download/job?id=:id` | id | 3 | 正常 |
| api/crypto | GET | `/api/crypto/download/jobs` | - | 1 | 正常 |
| api/crypto | POST | `/api/crypto/download/resume?id=:id` | id | 5 | 正常 |
| api/crypto | GET | `/api/crypto/download/settings` | - | 1 | 正常 |
| api/crypto | POST | `/api/crypto/download/settings` | - | 3 | ? 1 未拒绝 |
| api/crypto | POST | `/api/crypto/download/start` | - | 3 | 正常 |
| api/crypto | GET | `/api/crypto/enrichment/jobs` | - | 1 | 正常 |
| api/crypto | GET | `/api/crypto/enrichment/jobs/:id/cancel` | id | 3 | 正常 |
| api/crypto | POST | `/api/crypto/parquet` | - | 3 | ? 3 未拒绝 |
| api/crypto | POST | `/api/crypto/parquet/addresses/upload` | - | 3 | 正常 |
| api/crypto | POST | `/api/crypto/parquet/cancel?id=:id` | id | 5 | 正常 |
| api/crypto | GET | `/api/crypto/parquet/file?id=:id&path=:id` | id | 3 | 正常 |
| api/crypto | GET | `/api/crypto/parquet/job?id=:id` | id | 3 | 正常 |
| api/crypto | GET | `/api/crypto/parquet/jobs` | - | 1 | 正常 |
| api/crypto | POST | `/api/crypto/parquet/preview` | - | 3 | 正常 |
| api/crypto | POST | `/api/crypto/parquet/retry?id=:id` | id | 5 | 正常 |
| api/crypto | GET | `/api/crypto/parquet/settings` | - | 1 | 正常 |
| api/crypto | POST | `/api/crypto/parquet/settings` | - | 3 | ? 1 未拒绝 |
| api/crypto | POST | `/api/crypto/parquet/start` | - | 3 | 正常 |
| api/crypto | GET | `/api/crypto/rpc` | - | 1 | ? 1 未拒绝 |
| api/crypto | GET | `/api/crypto/rpc/endpoints` | - | 1 | 正常 |
| api/crypto | DELETE | `/api/crypto/rpc/endpoints/:id` | id | 4 | ? 3 未拒绝 |
| api/crypto | DELETE | `/api/crypto/rpc/endpoints/:id/test` | id | 4 | ? 3 未拒绝 |
| api/crypto | GET | `/api/crypto/rpc/health` | - | 1 | 正常 |
| api/crypto | DELETE | `/api/crypto/rpc/routing/:chain` | chain | 3 | 正常 |
| api/db | GET | `/api/db/connections` | - | 1 | 正常 |
| api/db | GET | `/api/db/connections/:id` | id | 3 | 正常 |
| api/db | DELETE | `/api/db/connections/:id` | id | 4 | 正常 |
| api/db | GET | `/api/db/connections/:id/columns?:param` | id, param | 3 | 正常 |
| api/db | GET | `/api/db/connections/:id/databases` | id | 3 | 正常 |
| api/db | GET | `/api/db/connections/:id/schemas?database=:param` | id, param | 4 | 正常 |
| api/db | GET | `/api/db/connections/:id/tables?:param` | id, param | 3 | 正常 |
| api/db | POST | `/api/db/connections/:id/test` | id | 5 | 正常 |
| api/db | POST | `/api/db/connections/test` | - | 3 | 正常 |
| api/db | POST | `/api/db/export/tasks` | - | 3 | 正常 |
| api/db | GET | `/api/db/export/tasks/:id` | id | 3 | 正常 |
| api/db | POST | `/api/db/export/tasks/:id/cancel` | id | 5 | 正常 |
| api/db | POST | `/api/db/import/tasks` | - | 3 | ? 1 未拒绝 |
| api/db | GET | `/api/db/import/tasks/:id` | id | 3 | 正常 |
| api/db | POST | `/api/db/import/tasks/:id/start` | id | 5 | 正常 |
| api/db | POST | `/api/db/mappings/auto` | - | 3 | 正常 |
| api/db | POST | `/api/db/mappings/confirm` | - | 3 | 正常 |
| api/db | POST | `/api/db/preview` | - | 3 | 正常 |
| api/db | POST | `/api/db/query` | - | 3 | 正常 |
| api/db | POST | `/api/db/search` | - | 3 | 正常 |
| api/db | GET | `/api/db/table/:param` | param | 3 | 正常 |
| api/download-engine | GET | `/api/download-engine/jobs` | - | 1 | 正常 |
| api/download-engine | POST | `/api/download-engine/jobs` | - | 3 | 正常 |
| api/download-engine | POST | `/api/download-engine/jobs/:id/:id` | id | 5 | 正常 |
| api/dune | GET | `/api/dune/auth` | - | 1 | 正常 |
| api/dune | POST | `/api/dune/auth` | - | 3 | ? 1 未拒绝 |
| api/dune | GET | `/api/dune/batch/accounts` | - | 1 | 正常 |
| api/dune | DELETE | `/api/dune/batch/accounts` | - | 1 | 正常 |
| api/dune | GET | `/api/dune/batch/export` | - | 1 | 正常 |
| api/dune | POST | `/api/dune/batch/start` | - | 3 | 正常 |
| api/dune | GET | `/api/dune/batch/status` | - | 1 | 正常 |
| api/dune | POST | `/api/dune/batch/stop` | - | 3 | ? 2 未拒绝 |
| api/dune | POST | `/api/dune/export` | - | 3 | 正常 |
| api/dune | POST | `/api/dune/query` | - | 3 | 正常 |
| api/dune | POST | `/api/dune/results` | - | 3 | 正常 |
| api/entity | GET | `/api/entity/:id/graph?chain=:chain` | id, chain | 3 | 正常 |
| api/entity | POST | `/api/entity/labels` | - | 3 | 正常 |
| api/entity | GET | `/api/entity/resolve?:param` | param | 3 | 正常 |
| api/entity | POST | `/api/entity/resolve/batch` | - | 3 | 正常 |
| api/entity | GET | `/api/entity/search?q=:param` | param | 4 | ? 2 未拒绝 |
| api/entity | GET | `/api/entity/stats` | - | 1 | 正常 |
| api/files | GET | `/api/files/current` | - | 1 | 正常 |
| api/flow | POST | `/api/flow/address-assets` | - | 3 | 正常 |
| api/flow | POST | `/api/flow/address-assets/batch` | - | 3 | 正常 |
| api/flow | POST | `/api/flow/balance-snapshot` | - | 3 | 正常 |
| api/flow | GET | `/api/flow/balance-snapshots?:param` | param | 3 | 正常 |
| api/flow | POST | `/api/flow/build` | - | 3 | 正常 |
| api/flow | POST | `/api/flow/direction-check` | - | 3 | 正常 |
| api/flow | POST | `/api/flow/direction-rules` | - | 3 | 正常 |
| api/flow | POST | `/api/flow/edge-detail/imported` | - | 3 | 正常 |
| api/flow | GET | `/api/flow/history` | - | 1 | 正常 |
| api/flow | POST | `/api/flow/history/:id` | id | 5 | 正常 |
| api/flow | GET | `/api/flow/import` | - | 1 | 正常 |
| api/flow | POST | `/api/flow/import-paths` | - | 3 | 正常 |
| api/flow | GET | `/api/flow/import-status/:id` | id | 3 | 正常 |
| api/flow | POST | `/api/flow/mapping-rules` | - | 3 | 正常 |
| api/flow | GET | `/api/flow/template` | - | 1 | 正常 |
| api/flow | GET | `/api/flow/upload` | - | 1 | 正常 |
| api/flow | POST | `/api/flow/values` | - | 3 | 正常 |
| api/fund-flow | POST | `/api/fund-flow/analyze` | - | 3 | 正常 |
| api/graph | GET | `/api/graph/expand` | - | 1 | 正常 |
| api/health | GET | `/api/health` | - | 1 | 正常 |
| api/intelligence | GET | `/api/intelligence/config` | - | 1 | 正常 |
| api/intelligence | POST | `/api/intelligence/config` | - | 3 | ? 1 未拒绝 |
| api/intelligence | GET | `/api/intelligence/events?id=:id` | id | 3 | 正常 |
| api/intelligence | POST | `/api/intelligence/investigations` | - | 3 | ? 1 未拒绝 |
| api/intelligence | GET | `/api/intelligence/investigations` | - | 1 | 正常 |
| api/intelligence | GET | `/api/intelligence/investigations/:id` | id | 3 | 正常 |
| api/intelligence | GET | `/api/intelligence/investigations/:id/memory` | id | 3 | 正常 |
| api/intelligence | GET | `/api/intelligence/investigations/:id/report?format=:id` | id | 3 | 正常 |
| api/investigation | GET | `/api/investigation/:id/evidence` | id | 3 | 正常 |
| api/investigation | GET | `/api/investigation/:id/plan` | id | 3 | 正常 |
| api/investigation | GET | `/api/investigation/:id/tasks` | id | 3 | 正常 |
| api/investigation | POST | `/api/investigation/create` | - | 3 | 正常 |
| api/investigations | GET | `/api/investigations/:id/entity-leads` | id | 3 | ? 2 未拒绝 |
| api/investigations | GET | `/api/investigations/:id/prefetch` | id | 3 | ? 2 未拒绝 |
| api/investigations | POST | `/api/investigations/:id/prefetch/pin` | id | 5 | 正常 |
| api/investigations | POST | `/api/investigations/:id/prefetch/upgrade` | id | 5 | 正常 |
| api/investigations | GET | `/api/investigations/:id/reports` | id | 3 | ? 2 未拒绝 |
| api/investigations | POST | `/api/investigations/:id/reports?max_depth=:param` | id, param | 5 | ? 1 未拒绝 |
| api/investigations | GET | `/api/investigations/:id/reports/:id` | id | 3 | 正常 |
| api/investigations | POST | `/api/investigations/:id/reports/:id/:id` | id | 5 | 正常 |
| api/investigations | POST | `/api/investigations/:id/reports/:id/export` | id | 5 | 正常 |
| api/investigations | POST | `/api/investigations/:id/reports/:id/polish` | id | 5 | 正常 |
| api/investigations | POST | `/api/investigations/:id/reports/:id/sign` | id | 5 | 正常 |
| api/investigations | GET | `/api/investigations/:id/reports/diff/:param/:param` | id, param | 3 | ? 2 未拒绝 |
| api/prefetch | GET | `/api/prefetch/stats` | - | 1 | 正常 |
| api/price | POST | `/api/price/backfill/anchor` | - | 3 | 正常 |
| api/price | GET | `/api/price/backfill/dex/jobs/:id` | id | 3 | 正常 |
| api/price | GET | `/api/price/backfill/jobs/:id` | id | 3 | 正常 |
| api/price | GET | `/api/price/coverage?chain=bsc&token=:token` | token | 3 | 正常 |
| api/price | POST | `/api/price/gaps/repair` | - | 3 | 正常 |
| api/price | GET | `/api/price/health` | - | 1 | 正常 |
| api/price | GET | `/api/price/history/candles?chain=bsc&token=:token&start=:param&end=:param&interval=:param` | token, param | 4 | 正常 |
| api/price | GET | `/api/price/history/point?chain=bsc&token=:token&timestamp=:param` | token, param | 4 | 正常 |
| api/price | GET | `/api/price/pools?token=:token` | token | 3 | 正常 |
| api/price | POST | `/api/price/pools/discover` | - | 3 | 正常 |
| api/process | GET | `/api/process` | - | 1 | 正常 |
| api/process | GET | `/api/process/progress/:id` | id | 3 | 正常 |
| api/rules | POST | `/api/rules/analyze` | - | 3 | 正常 |
| api/rules | POST | `/api/rules/confirm` | - | 3 | 正常 |
| api/scheduler | GET | `/api/scheduler` | - | 1 | ? 1 未拒绝 |
| api/scheduler | GET | `/api/scheduler/cloud/jobs` | - | 1 | 正常 |
| api/scheduler | GET | `/api/scheduler/cloud/runtime` | - | 1 | 正常 |
| api/scheduler | POST | `/api/scheduler/cloud/sync` | - | 3 | ⚠ 2 失败 |
| api/scheduler | GET | `/api/scheduler/cloud/usage` | - | 1 | 正常 |
| api/scheduler | POST | `/api/scheduler/coverage` | - | 3 | 正常 |
| api/scheduler | POST | `/api/scheduler/expand` | - | 3 | 正常 |
| api/scheduler | POST | `/api/scheduler/plan` | - | 3 | 正常 |
| api/scheduler | GET | `/api/scheduler/plans` | - | 1 | 正常 |
| api/scheduler | GET | `/api/scheduler/providers/health` | - | 1 | 正常 |
| api/scheduler | POST | `/api/scheduler/run` | - | 3 | 正常 |
| api/scheduler | GET | `/api/scheduler/status?plan_id=:id` | id | 3 | 正常 |
| api/smart-download | GET | `/api/smart-download/addresses/:address` | address | 3 | ? 1 非2xx |
| api/smart-download | POST | `/api/smart-download/addresses/:address/:id` | address, id | 4 | 正常 |
| api/smart-download | GET | `/api/smart-download/batches` | - | 1 | 正常 |
| api/smart-download | GET | `/api/smart-download/batches/:id` | id | 3 | 正常 |
| api/smart-download | POST | `/api/smart-download/batches/:id/:id` | id | 5 | 正常 |
| api/smart-download | GET | `/api/smart-download/batches/:id/accelerator` | id | 3 | 正常 |
| api/smart-download | GET | `/api/smart-download/batches/:id/addresses?:param` | id, param | 3 | 正常 |
| api/smart-download | GET | `/api/smart-download/batches/:id/hardening` | id | 3 | 正常 |
| api/smart-download | POST | `/api/smart-download/batches/:id/mode` | id | 5 | 正常 |
| api/smart-download | POST | `/api/smart-download/batches/:id/plan` | id | 5 | 正常 |
| api/smart-download | GET | `/api/smart-download/batches/:id/report` | id | 3 | 正常 |
| api/smart-download | GET | `/api/smart-download/batches/:id/turbo-status` | id | 3 | 正常 |
| api/smart-download | POST | `/api/smart-download/compare` | - | 3 | 正常 |
| api/smart-download | POST | `/api/smart-download/coverage/query` | - | 3 | 正常 |
| api/smart-download | GET | `/api/smart-download/datasets/:id/ledger` | id | 3 | ? 2 未拒绝 |
| api/smart-download | GET | `/api/smart-download/events` | - | 1 | 正常 |
| api/smart-download | POST | `/api/smart-download/import` | - | 3 | 正常 |
| api/smart-download | GET | `/api/smart-download/jobs/:id/snapshot` | id | 3 | 正常 |
| api/smart-download | GET | `/api/smart-download/jobs/:id/summary` | id | 3 | 正常 |
| api/smart-download | POST | `/api/smart-download/planner-v2` | - | 3 | 正常 |
| api/smart-download | POST | `/api/smart-download/preflight` | - | 3 | 正常 |
| api/smart-download | GET | `/api/smart-download/registry` | - | 1 | 正常 |
| api/smart-download | GET | `/api/smart-download/results/:id?:param` | id, param | 3 | 正常 |
| api/smart-download | GET | `/api/smart-download/results/:id/export` | id | 3 | ⚠ 2 失败 |
| api/smart-download | GET | `/api/smart-download/results/:id/summary` | id | 3 | 正常 |
| api/smart-download | GET | `/api/smart-download/templates` | - | 1 | 正常 |
| api/smart-download | POST | `/api/smart-download/templates` | - | 3 | ? 1 未拒绝 |
| api/smart-download | DELETE | `/api/smart-download/templates/:id` | id | 4 | 正常 |
| api/smart-download | POST | `/api/smart-download/templates/:id/instantiate` | id | 5 | 正常 |
| api/system | GET | `/api/system/settings` | - | 1 | 正常 |
| api/system | PATCH | `/api/system/settings` | - | 2 | 正常 |
| api/system | GET | `/api/system/settings/backups` | - | 1 | 正常 |
| api/system | POST | `/api/system/settings/backups` | - | 3 | ? 1 未拒绝 |
| api/system | POST | `/api/system/settings/backups/:id/restore` | id | 5 | 正常 |
| api/system | POST | `/api/system/settings/cleanup/execute` | - | 3 | 正常 |
| api/system | POST | `/api/system/settings/cleanup/preview` | - | 3 | 正常 |
| api/v2 | GET | `/api/v2/financial-export/:chain/address/:address.csv?:param` | chain, address, param | 4 | ? 1 未拒绝 |
| 传统分析 / 地址 | GET | `/api/address/:address/:id?chain_key=:chain` | address, id, chain | 4 | ? 1 非2xx |
| 传统分析 / 地址 | GET | `/api/analytics/address-stats?:param` | param | 3 | ⚠ 3 失败 |
| 传统分析 / 地址 | GET | `/api/analytics/address/:address/path` | address | 3 | ? 1 未拒绝 |
| 传统分析 / 地址 | GET | `/api/analytics/address/:address/risk` | address | 3 | ? 1 未拒绝 |
| 传统分析 / 地址 | GET | `/api/analytics/dashboard` | - | 1 | ⚠ 1 失败 |
| 传统分析 / 地址 | GET | `/api/analytics/flow-stats?:param` | param | 3 | ⚠ 3 失败 |
| 传统分析 / 地址 | GET | `/api/analytics/graph?limit=:param` | param | 5 | 正常 |
| 传统分析 / 地址 | GET | `/api/analytics/report/asset_summary.json` | - | 1 | ⚠ 1 失败 |
| 传统分析 / 地址 | GET | `/api/analytics/report/case-demo/case-report.docx` | - | 1 | ⚠ 1 失败 |
| 传统分析 / 地址 | GET | `/api/analytics/report/case-full/case-report.html` | - | 1 | ⚠ 1 失败 |
| 传统分析 / 地址 | GET | `/api/analytics/report/case-full/evidence_bundle.json` | - | 1 | ⚠ 1 失败 |
| 传统分析 / 地址 | GET | `/api/analytics/report/evidence.json` | - | 1 | ⚠ 1 失败 |
| 传统分析 / 地址 | GET | `/api/analytics/report/graph.json` | - | 1 | ⚠ 1 失败 |

## 6. 详细用例结果

> 按端点分组；状态 0 表示请求异常/超时。响应摘要截断显示。

### Explorer v1

#### GET `/api/v1/explorer/:chain/address/:address`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 404 | 2ms | 不符合预期(非2xx)  | {"detail":"api route not found"} |
| invalid-chain | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| invalid-address | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/v1/explorer/:chain/address/:address/:param?:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 404 | 3ms | 不符合预期(非2xx) （占位参数构造问题，非接口缺陷） | {"detail":"api route not found"} |
| invalid-chain | 404 | 7ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| invalid-address | 404 | 8ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| wrong-method | 404 | 6ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/v1/explorer/:chain/address/:address/daily-stats?:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-chain | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| invalid-address | 400 | 6ms | 通过(按预期拒绝)  | {"detail":"invalid explorer input: invalid EVM address"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| valid | 200 | 126ms | 通过  | [] |

#### GET `/api/v1/explorer/:chain/contract/:address`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-chain | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| invalid-address | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid explorer input: invalid EVM address"} |
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| valid | 404 | 159ms | 不符合预期(非2xx)  | {"detail":"address not found"} |

#### GET `/api/v1/explorer/:chain/token/:address`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-chain | 400 | 12ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| invalid-address | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"invalid explorer input: invalid EVM address"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| valid | 404 | 6313ms | 不符合预期(非2xx)  | {"detail":"address not found"} |

#### GET `/api/v1/explorer/:chain/tx/:tx_hash`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 200 | 759ms | 通过  | {"chain_id":56,"block_number":114004503,"block_hash":"","block_time":"2026-08-04T16:50:01Z","transac |
| invalid-chain | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| invalid-tx_hash | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"invalid explorer input: invalid transaction hash"} |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### Explorer v2 / 金融质量

#### GET `/api/v2/data-quality/:chain`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-chain | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| valid | - | 25007ms | 失败(请求异常)  | timeout>25s |

#### GET `/api/v2/explorer/:chain/address/:address/header`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-chain | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| invalid-address | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"invalid EVM address"} |
| wrong-method | 404 | 14ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| valid | 200 | 684ms | 通过  | {"balances":{"available":false,"estimated_portfolio_usd":null,"items":[]},"coverage":{"detail":{"com |

#### GET `/api/v2/explorer/:chain/block/:block`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 404 | 23ms | 不符合预期(非2xx)  | {"detail":"block not found"} |
| invalid-chain | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| invalid-block | 400 | 8ms | 通过(按预期拒绝)  | {"detail":"invalid block number"} |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/v2/explorer/:chain/home`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-chain | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| valid | - | 25037ms | 失败(请求异常)  | timeout>25s |

#### GET `/api/v2/explorer/:chain/token/:address`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 404 | 88ms | 不符合预期(非2xx)  | {"detail":"canonical asset not found"} |
| invalid-chain | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| invalid-address | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"invalid canonical request"} |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/v2/explorer/:chain/tx/:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 400 | 1ms | 不符合预期(非2xx) （占位参数构造问题，非接口缺陷） | {"detail":"invalid canonical request"} |
| invalid-chain | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/v2/explorer/:chain/tx/:tx_hash`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-chain | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| invalid-tx_hash | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"invalid canonical request"} |
| wrong-method | 404 | 0ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| valid | 200 | 1282ms | 通过  | {"chain_id":56,"block_number":114004503,"block_hash":"","block_time":"2026-08-04T16:50:01Z","tx_hash |

#### GET `/api/v2/explorer/search?chain=:chain&q=:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 200 | 1ms | 通过  | {"chain_id":56,"items":[{"chain_id":56,"kind":"BLOCK","subtitle":"Block number","title":"80","value" |
| invalid-chain | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| query-injection | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"search query must contain 1-96 characters"} |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/v2/financial-quality/:chain?window=:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 400 | 1ms | 不符合预期(非2xx) （占位参数构造问题，非接口缺陷） | {"detail":"invalid financial query"} |
| invalid-chain | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| query-window-invalid | - | 25015ms | 失败(请求异常)  | timeout>25s |
| query-injection | - | 25014ms | 失败(请求异常)  | timeout>25s |

#### GET `/api/v2/pricing/56/token/:token/gaps?from=:param&to=:param&resolution=:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 400 | 1ms | 不符合预期(非2xx) （占位参数构造问题，非接口缺陷） | {"detail":"from must be RFC3339"} |
| invalid-token | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"from must be RFC3339"} |
| query-date-invalid | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"from is required"} |
| query-resolution-invalid | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"from is required"} |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### Financial Analytics v2

#### GET `/api/v2/analytics/:chain/address/:address/:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 404 | 2ms | 不符合预期(非2xx) （占位参数构造问题，非接口缺陷） | {"detail":"api route not found"} |
| invalid-chain | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| invalid-address | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/v2/analytics/:chain/address/:address/financial-counterparties?window=:param&limit=50`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 400 | 2ms | 不符合预期(非2xx) （占位参数构造问题，非接口缺陷） | {"detail":"invalid financial query"} |
| invalid-chain | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| invalid-address | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"invalid financial query"} |
| query-limit-negative | 200 | 475ms | 不符合预期(未拒绝)  | [{"counterparty":"0x001bea48c16005d18ed393b194c394f4267471d4","in_usd":null,"out_usd":null,"netflow_ |
| query-limit-nonnumeric | 200 | 549ms | 不符合预期(未拒绝)  | [{"counterparty":"0x001bea48c16005d18ed393b194c394f4267471d4","in_usd":null,"out_usd":null,"netflow_ |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| query-window-invalid | 200 | 430ms | 不符合预期(未拒绝)  | [{"counterparty":"0x001bea48c16005d18ed393b194c394f4267471d4","in_usd":null,"out_usd":null,"netflow_ |

#### GET `/api/v2/analytics/:chain/address/:address/financial-summary?:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-chain | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| invalid-address | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"invalid financial query"} |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| valid | 200 | 475ms | 通过  | {"chain_id":56,"address":"0x92e102725a90a1ac0d60560cb1807b9c5820b0a9","window":"30D","from":"2026-07 |

### api/ai

#### POST `/api/ai/analyze`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 26ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 26ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| bad-json | 400 | 29ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |

### api/crypto

#### POST `/api/crypto/address-classify`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: EOF"} |
| bad-json | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: unexpected EOF"} |
| wrong-method | 404 | 7ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/crypto/addresses/:chain/:address/first-seen`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-chain | 500 | 3ms | 失败(5xx)  | {"detail":"不支持的链: 不支持的 EVM 网络: notachain"}
 |
| invalid-address | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"EVM 地址格式错误"}
 |
| wrong-method | 405 | 1ms | 通过(按预期拒绝)  | {"detail":"请求方法不支持"}
 |
| valid | 200 | 3186ms | 通过  | {"chain_id":"bsc","address":"0x92e102725a90a1ac0d60560cb1807b9c5820b0a9","address_type":"EOA","cover |

#### GET `/api/crypto/datasource`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 307 | 5ms | 不符合预期(未拒绝)  |  |

#### GET `/api/crypto/datasource/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"数据源接口不存在"}
 |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"数据源接口不存在"}
 |
| garbage-id | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"数据源接口不存在"}
 |

#### GET `/api/crypto/download`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 307 | 3ms | 不符合预期(未拒绝)  |  |

#### POST `/api/crypto/download/cancel?id=:id:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 1ms | 通过(按预期拒绝)  | job not found
 |
| query-date-invalid | 404 | 2ms | 通过(按预期拒绝)  | job not found
 |
| garbage-id | 404 | 4ms | 通过(按预期拒绝)  | job not found
 |
| empty-body | 404 | 3ms | 通过(按预期拒绝)  | job not found
 |
| bad-json | 404 | 4ms | 通过(按预期拒绝)  | job not found
 |
| wrong-method | 404 | 8ms | 通过(按预期拒绝)  | job not found
 |

#### GET `/api/crypto/download/file?id=:id&path=:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 6ms | 通过(按预期拒绝)  | job not found
 |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | job not found
 |
| wrong-method | 405 | 2ms | 通过(按预期拒绝)  | method not allowed
 |

#### GET `/api/crypto/download/history`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 405 | 2ms | 通过(按预期拒绝)  | method not allowed
 |

#### DELETE `/api/crypto/download/history?id=:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | history not found
 |
| garbage-id | 404 | 3ms | 通过(按预期拒绝)  | history not found
 |
| bad-body | 404 | 7ms | 通过(按预期拒绝)  | history not found
 |
| wrong-method | 405 | 6ms | 通过(按预期拒绝)  | method not allowed
 |

#### POST `/api/crypto/download/history/import`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 4ms | 通过(按预期拒绝)  | EOF
 |
| bad-json | 400 | 5ms | 通过(按预期拒绝)  | unexpected EOF
 |
| wrong-method | 405 | 13ms | 通过(按预期拒绝)  | method not allowed
 |

#### POST `/api/crypto/download/history/resume?id=:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 3ms | 通过(按预期拒绝)  | history not found
 |
| garbage-id | 404 | 3ms | 通过(按预期拒绝)  | history not found
 |
| empty-body | 404 | 3ms | 通过(按预期拒绝)  | history not found
 |
| bad-json | 404 | 3ms | 通过(按预期拒绝)  | history not found
 |
| wrong-method | 405 | 2ms | 通过(按预期拒绝)  | method not allowed
 |

#### GET `/api/crypto/download/job?id=:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 1ms | 通过(按预期拒绝)  | job not found
 |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | job not found
 |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | job not found
 |

#### GET `/api/crypto/download/jobs`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 405 | 1ms | 通过(按预期拒绝)  | method not allowed
 |

#### POST `/api/crypto/download/resume?id=:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 1ms | 通过(按预期拒绝)  | job not found
 |
| garbage-id | 404 | 1ms | 通过(按预期拒绝)  | job not found
 |
| empty-body | 404 | 3ms | 通过(按预期拒绝)  | job not found
 |
| bad-json | 404 | 2ms | 通过(按预期拒绝)  | job not found
 |
| wrong-method | 405 | 1ms | 通过(按预期拒绝)  | method not allowed
 |

#### GET `/api/crypto/download/settings`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 1ms | 通过(按预期拒绝)  | EOF
 |

#### POST `/api/crypto/download/settings`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | EOF
 |
| bad-json | 400 | 2ms | 通过(按预期拒绝)  | unexpected EOF
 |
| wrong-method | 200 | 1ms | 不符合预期(未拒绝)  | {"source":"rpc","csvEmail":"","csvImapHost":"","csvImapPort":993,"csvImapUser":"","csvImapPassword": |

#### POST `/api/crypto/download/start`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | EOF
 |
| bad-json | 400 | 2ms | 通过(按预期拒绝)  | unexpected EOF
 |
| wrong-method | 405 | 1ms | 通过(按预期拒绝)  | method not allowed
 |

#### GET `/api/crypto/enrichment/jobs`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求参数无效：EOF"}
 |

#### GET `/api/crypto/enrichment/jobs/:id/cancel`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"sql: no rows in result set"}
 |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"sql: no rows in result set"}
 |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"sql: no rows in result set"}
 |

#### POST `/api/crypto/parquet`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 307 | 2ms | 不符合预期(未拒绝)  |  |
| bad-json | 307 | 2ms | 不符合预期(未拒绝)  |  |
| wrong-method | 301 | 2ms | 不符合预期(未拒绝)  | <a href="/api/crypto/parquet/">Moved Permanently</a>.

 |

#### POST `/api/crypto/parquet/addresses/upload`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"地址文件不能超过 32 MB: request Content-Type isn't multipart/form-data"}
 |
| bad-json | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"地址文件不能超过 32 MB: request Content-Type isn't multipart/form-data"}
 |
| wrong-method | 405 | 2ms | 通过(按预期拒绝)  | {"detail":"请求方法不支持"}
 |

#### POST `/api/crypto/parquet/cancel?id=:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"Parquet 任务不存在"}
 |
| garbage-id | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"Parquet 任务不存在"}
 |
| empty-body | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"Parquet 任务不存在"}
 |
| bad-json | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"Parquet 任务不存在"}
 |
| wrong-method | 405 | 2ms | 通过(按预期拒绝)  | {"detail":"请求方法不支持"}
 |

#### GET `/api/crypto/parquet/file?id=:id&path=:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"Parquet 任务不存在"}
 |
| garbage-id | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"Parquet 任务不存在"}
 |
| wrong-method | 405 | 2ms | 通过(按预期拒绝)  | {"detail":"请求方法不支持"}
 |

#### GET `/api/crypto/parquet/job?id=:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"Parquet 任务不存在"}
 |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"Parquet 任务不存在"}
 |
| wrong-method | 405 | 1ms | 通过(按预期拒绝)  | {"detail":"请求方法不支持"}
 |

#### GET `/api/crypto/parquet/jobs`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 405 | 2ms | 通过(按预期拒绝)  | {"detail":"请求方法不支持"}
 |

#### POST `/api/crypto/parquet/preview`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: EOF"}
 |
| bad-json | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: unexpected EOF"}
 |
| wrong-method | 405 | 2ms | 通过(按预期拒绝)  | {"detail":"请求方法不支持"}
 |

#### POST `/api/crypto/parquet/retry?id=:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"Parquet 任务不存在"}
 |
| garbage-id | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"Parquet 任务不存在"}
 |
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"Parquet 任务不存在"}
 |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"Parquet 任务不存在"}
 |
| wrong-method | 405 | 2ms | 通过(按预期拒绝)  | {"detail":"请求方法不支持"}
 |

#### GET `/api/crypto/parquet/settings`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: EOF"}
 |

#### POST `/api/crypto/parquet/settings`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: EOF"}
 |
| bad-json | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: unexpected EOF"}
 |
| wrong-method | 200 | 2ms | 不符合预期(未拒绝)  | {"data_root":"E:\\codex\\bsc_analytics","download_concurrency":3,"duckdb_threads":14,"memory_limit": |

#### POST `/api/crypto/parquet/start`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: EOF"}
 |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: unexpected EOF"}
 |
| wrong-method | 405 | 2ms | 通过(按预期拒绝)  | {"detail":"请求方法不支持"}
 |

#### GET `/api/crypto/rpc`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 307 | 2ms | 不符合预期(未拒绝)  |  |

#### GET `/api/crypto/rpc/endpoints`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求参数无效：EOF"}
 |

#### DELETE `/api/crypto/rpc/endpoints/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 200 | 2ms | 不符合预期(未拒绝)  | {"success":true}
 |
| garbage-id | 200 | 3ms | 不符合预期(未拒绝)  | {"success":true}
 |
| bad-body | 200 | 5ms | 不符合预期(未拒绝)  | {"success":true}
 |
| wrong-method | 405 | 4ms | 通过(按预期拒绝)  | {"detail":"请求方法不支持"}
 |

#### DELETE `/api/crypto/rpc/endpoints/:id/test`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 200 | 4ms | 不符合预期(未拒绝)  | {"success":true}
 |
| bad-body | 200 | 3ms | 不符合预期(未拒绝)  | {"success":true}
 |
| garbage-id | 200 | 8ms | 不符合预期(未拒绝)  | {"success":true}
 |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"sql: no rows in result set"}
 |

#### GET `/api/crypto/rpc/health`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"RPC 接口不存在"}
 |

#### DELETE `/api/crypto/rpc/routing/:chain`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-chain | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"RPC 接口不存在"}
 |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"RPC 接口不存在"}
 |
| bad-body | 404 | 8ms | 通过(按预期拒绝)  | {"detail":"RPC 接口不存在"}
 |

### api/db

#### GET `/api/db/connections`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |

#### GET `/api/db/connections/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### DELETE `/api/db/connections/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| bad-body | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/db/connections/:id/columns?:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 7ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| garbage-id | 400 | 12ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |

#### GET `/api/db/connections/:id/databases`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| garbage-id | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| wrong-method | 404 | 8ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/db/connections/:id/schemas?database=:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| garbage-id | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| query-path-traversal | 400 | 10ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| wrong-method | 404 | 10ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/db/connections/:id/tables?:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 7ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| garbage-id | 400 | 11ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |

#### POST `/api/db/connections/:id/test`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| garbage-id | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| bad-json | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"connection not found"} |
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/db/connections/test`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/db/export/tasks`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求参数不是有效 JSON"} |
| bad-json | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求参数不是有效 JSON"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/db/export/tasks/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"数据库导入任务不存在"} |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"数据库导入任务不存在"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/db/export/tasks/:id/cancel`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"数据库导入任务不存在"} |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"数据库导入任务不存在"} |
| empty-body | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"数据库导入任务不存在"} |
| bad-json | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"数据库导入任务不存在"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/db/import/tasks`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 200 | 2ms | 不符合预期(未拒绝)  | {"items":null} |

#### GET `/api/db/import/tasks/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"task not found"} |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"task not found"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/db/import/tasks/:id/start`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"task not found"} |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"task not found"} |
| empty-body | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"task not found"} |
| bad-json | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"task not found"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/db/mappings/auto`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/db/mappings/confirm`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/db/preview`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/db/query`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/db/search`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/db/table/:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| garbage-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/download-engine

#### GET `/api/download-engine/jobs`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/download-engine/jobs`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| bad-json | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/download-engine/jobs/:id/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| garbage-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| empty-body | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| bad-json | 404 | 6ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/dune

#### GET `/api/dune/auth`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"invalid request: EOF"} |

#### POST `/api/dune/auth`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"invalid request: EOF"} |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid request: unexpected EOF"} |
| wrong-method | 200 | 4ms | 不符合预期(未拒绝)  | {"access_token":"","authorization":"","cookie":"","has_api_key":false,"has_cookie":false,"has_web_au |

#### GET `/api/dune/batch/accounts`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### DELETE `/api/dune/batch/accounts`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| skipped-destructive | - | -ms | 跳过(破坏性)  |  |

#### GET `/api/dune/batch/export`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/dune/batch/start`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid request: EOF"} |
| bad-json | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"invalid request: unexpected EOF"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/dune/batch/status`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/dune/batch/stop`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 200 | 5ms | 不符合预期(未拒绝)  | {"id":"","total":0,"completed":0,"failed":0,"status":"idle","accounts":null} |
| bad-json | 200 | 4ms | 不符合预期(未拒绝)  | {"id":"","total":0,"completed":0,"failed":0,"status":"idle","accounts":null} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/dune/export`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"invalid request: EOF"} |
| bad-json | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"invalid request: unexpected EOF"} |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/dune/query`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"invalid request: EOF"} |
| bad-json | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"invalid request: unexpected EOF"} |
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/dune/results`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"invalid request: EOF"} |
| bad-json | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"invalid request: unexpected EOF"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/entity

#### GET `/api/entity/:id/graph?chain=:chain`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| valid | 404 | 4ms | 不符合预期(非2xx) （占位参数构造问题，非接口缺陷） | {"detail":"实体不存在: unknown"} |
| invalid-chain | 404 | 6ms | 通过(按预期拒绝)  | {"detail":"实体不存在: 00000000-0000-0000-0000-000000000000"} |

#### POST `/api/entity/labels`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"} |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/entity/resolve?:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"非法 EVM 地址"} |
| garbage-id | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"非法 EVM 地址"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/entity/resolve/batch`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"} |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/entity/search?q=:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 200 | 3ms | 不符合预期(未拒绝)  | {"items":null,"total":0} |
| query-injection | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"缺少 q 参数"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| garbage-id | 200 | 6ms | 不符合预期(未拒绝)  | {"items":null,"total":0} |

#### GET `/api/entity/stats`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/files

#### GET `/api/files/current`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/flow

#### POST `/api/flow/address-assets`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 9ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: EOF"} |
| bad-json | 400 | 10ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: unexpected EOF"} |
| wrong-method | 404 | 13ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/flow/address-assets/batch`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 13ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: EOF"} |
| bad-json | 400 | 12ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: unexpected EOF"} |
| wrong-method | 404 | 11ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/flow/balance-snapshot`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 18ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: EOF"} |
| bad-json | 400 | 21ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: unexpected EOF"} |
| wrong-method | 404 | 27ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/flow/balance-snapshots?:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 11ms | 通过(按预期拒绝)  | {"detail":"address 不是合法的 EVM 地址"} |
| garbage-id | 400 | 11ms | 通过(按预期拒绝)  | {"detail":"address 不是合法的 EVM 地址"} |
| wrong-method | 404 | 11ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/flow/build`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 15ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 24ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 40ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/flow/direction-check`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 34ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 29ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 8ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/flow/direction-rules`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 6ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/flow/edge-detail/imported`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/flow/history`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/flow/history/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| garbage-id | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| empty-body | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| bad-json | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"session not found: unknown"} |

#### GET `/api/flow/import`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"request Content-Type isn't multipart/form-data"} |

#### POST `/api/flow/import-paths`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/flow/import-status/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"session not found"} |
| garbage-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"session not found"} |
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/flow/mapping-rules`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 6ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 6ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/flow/template`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/flow/upload`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"request Content-Type isn't multipart/form-data"} |

#### POST `/api/flow/values`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/fund-flow

#### POST `/api/fund-flow/analyze`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"} |
| bad-json | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"} |
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/graph

#### GET `/api/graph/expand`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 6ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"} |

### api/health

#### GET `/api/health`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 7ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/intelligence

#### GET `/api/intelligence/config`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: EOF"}
 |

#### POST `/api/intelligence/config`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: EOF"}
 |
| bad-json | 400 | 6ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: unexpected EOF"}
 |
| wrong-method | 200 | 6ms | 不符合预期(未拒绝)  | {"max_hops":4,"beam_width":8,"top_paths":10,"min_amount":"0","use_ai":true,"ai_model":"deepseek-v4-f |

#### GET `/api/intelligence/events?id=:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"调查不存在"}
 |
| garbage-id | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"调查不存在"}
 |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"接口不存在"}
 |

#### POST `/api/intelligence/investigations`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: EOF"}
 |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: unexpected EOF"}
 |
| wrong-method | 200 | 3ms | 不符合预期(未拒绝)  | {"items":[],"total":0}
 |

#### GET `/api/intelligence/investigations`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: EOF"}
 |

#### GET `/api/intelligence/investigations/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"调查不存在"}
 |
| garbage-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"调查不存在"}
 |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"接口不存在"}
 |

#### GET `/api/intelligence/investigations/:id/memory`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"调查记忆不存在"}
 |
| garbage-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"调查记忆不存在"}
 |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"接口不存在"}
 |

#### GET `/api/intelligence/investigations/:id/report?format=:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"调查不存在"}
 |
| garbage-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"调查不存在"}
 |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"接口不存在"}
 |

### api/investigation

#### GET `/api/investigation/:id/evidence`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"调查不存在"}
 |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"调查不存在"}
 |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"接口不存在"}
 |

#### GET `/api/investigation/:id/plan`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"调查不存在"}
 |
| garbage-id | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"调查不存在"}
 |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"接口不存在"}
 |

#### GET `/api/investigation/:id/tasks`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"调查不存在"}
 |
| garbage-id | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"调查不存在"}
 |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"接口不存在"}
 |

#### POST `/api/investigation/create`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: EOF"}
 |
| bad-json | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"请求格式错误: unexpected EOF"}
 |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"调查不存在"}
 |

### api/investigations

#### GET `/api/investigations/:id/entity-leads`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 200 | 3ms | 不符合预期(未拒绝)  | {"investigation_id":"00000000-0000-0000-0000-000000000000","leads":null,"total":0} |
| garbage-id | 200 | 3ms | 不符合预期(未拒绝)  | {"investigation_id":"!!!bad id!!!","leads":null,"total":0} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/investigations/:id/prefetch`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 200 | 3ms | 不符合预期(未拒绝)  | {"candidates":[],"investigation_id":"00000000-0000-0000-0000-000000000000","stats":{"total_jobs":0," |
| garbage-id | 200 | 4ms | 不符合预期(未拒绝)  | {"candidates":[],"investigation_id":"!!!bad id!!!","stats":{"total_jobs":0,"active_jobs":0,"ready_jo |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/investigations/:id/prefetch/pin`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"} |
| garbage-id | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"} |
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"} |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/investigations/:id/prefetch/upgrade`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"} |
| garbage-id | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"} |
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"} |
| bad-json | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/investigations/:id/reports`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 200 | 2ms | 不符合预期(未拒绝)  | {"investigation_id":"00000000-0000-0000-0000-000000000000","reports":null} |
| garbage-id | 200 | 2ms | 不符合预期(未拒绝)  | {"investigation_id":"!!!bad id!!!","reports":null} |
| wrong-method | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"reportengine: 调查 unknown 未设置焦点地址"} |

#### POST `/api/investigations/:id/reports?max_depth=:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"reportengine: 调查 00000000-0000-0000-0000-000000000000 未设置焦点地址"} |
| garbage-id | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"reportengine: 调查 !!!bad id!!! 未设置焦点地址"} |
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"reportengine: 调查 00000000-0000-0000-0000-000000000000 未设置焦点地址"} |
| bad-json | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"reportengine: 调查 00000000-0000-0000-0000-000000000000 未设置焦点地址"} |
| wrong-method | 200 | 2ms | 不符合预期(未拒绝)  | {"investigation_id":"unknown","reports":null} |

#### GET `/api/investigations/:id/reports/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"报告不存在"} |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"报告不存在"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/investigations/:id/reports/:id/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| garbage-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| empty-body | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| bad-json | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/investigations/:id/reports/:id/export`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"} |
| garbage-id | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"} |
| empty-body | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"} |
| bad-json | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"} |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/investigations/:id/reports/:id/polish`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"reportengine: 报告不存在"} |
| garbage-id | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"reportengine: 报告不存在"} |
| empty-body | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"reportengine: 报告不存在"} |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"reportengine: 报告不存在"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/investigations/:id/reports/:id/sign`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"reportengine: 报告不存在"} |
| garbage-id | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"reportengine: 报告不存在"} |
| empty-body | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"reportengine: 报告不存在"} |
| bad-json | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"reportengine: 报告不存在"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/investigations/:id/reports/diff/:param/:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 200 | 3ms | 不符合预期(未拒绝)  | {"report_a":"unknown","report_b":"unknown"} |
| garbage-id | 200 | 9ms | 不符合预期(未拒绝)  | {"report_a":"!!!bad id!!!","report_b":"!!!bad id!!!"} |
| wrong-method | 404 | 11ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/prefetch

#### GET `/api/prefetch/stats`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/price

#### POST `/api/price/backfill/anchor`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 6ms | 通过(按预期拒绝)  | {"detail":"invalid backfill request"} |
| bad-json | 400 | 6ms | 通过(按预期拒绝)  | {"detail":"invalid backfill request"} |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/price/backfill/dex/jobs/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"job not found"} |
| garbage-id | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"invalid job id"} |
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/price/backfill/jobs/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 7ms | 通过(按预期拒绝)  | {"detail":"job not found"} |
| garbage-id | 400 | 7ms | 通过(按预期拒绝)  | {"detail":"invalid job id"} |
| wrong-method | 404 | 8ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/price/coverage?chain=bsc&token=:token`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-token | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"invalid financial query"} |
| wrong-method | 404 | 18ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| valid | 200 | 41ms | 通过  | {"covered":false,"token":"0x55d398326f99059ff775485246999027b3197955"} |

#### POST `/api/price/gaps/repair`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 20ms | 通过(按预期拒绝)  | {"detail":"invalid price gap repair request"} |
| bad-json | 400 | 20ms | 通过(按预期拒绝)  | {"detail":"invalid price gap repair request"} |
| wrong-method | 404 | 13ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/price/health`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 12ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/price/history/candles?chain=bsc&token=:token&start=:param&end=:param&interval=:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 400 | 12ms | 不符合预期(非2xx) （占位参数构造问题，非接口缺陷） | {"detail":"invalid or excessive candle range"} |
| invalid-token | 400 | 11ms | 通过(按预期拒绝)  | {"detail":"invalid or excessive candle range"} |
| query-date-invalid | 400 | 10ms | 通过(按预期拒绝)  | {"detail":"invalid or excessive candle range"} |
| wrong-method | 404 | 12ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/price/history/point?chain=bsc&token=:token&timestamp=:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 400 | 11ms | 不符合预期(非2xx) （占位参数构造问题，非接口缺陷） | {"detail":"token and RFC3339 timestamp are required"} |
| invalid-token | 400 | 16ms | 通过(按预期拒绝)  | {"detail":"token and RFC3339 timestamp are required"} |
| query-date-invalid | 400 | 17ms | 通过(按预期拒绝)  | {"detail":"token and RFC3339 timestamp are required"} |
| wrong-method | 404 | 14ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/price/pools?token=:token`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-token | 400 | 14ms | 通过(按预期拒绝)  | {"detail":"valid token contract is required"} |
| wrong-method | 404 | 14ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |
| valid | 200 | 138ms | 通过  | {"pools":[{"chain_id":56,"dex":"PANCAKESWAP","factory_address":"0xca143ce32fe78f1f7019d7d551a6402fc5 |

#### POST `/api/price/pools/discover`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 14ms | 通过(按预期拒绝)  | {"detail":"invalid pool discovery request"} |
| bad-json | 400 | 7ms | 通过(按预期拒绝)  | {"detail":"invalid pool discovery request"} |
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/process

#### GET `/api/process`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"invalid multipart form: request Content-Type isn't multipart/form-data"} |

#### GET `/api/process/progress/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"任务进度不存在"} |
| garbage-id | 404 | 9ms | 通过(按预期拒绝)  | {"detail":"任务进度不存在"} |
| wrong-method | 404 | 12ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/rules

#### POST `/api/rules/analyze`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 12ms | 通过(按预期拒绝)  | {"detail":"provider 必须是 alipay、wechat 或 bank"} |
| bad-json | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"provider 必须是 alipay、wechat 或 bank"} |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/rules/confirm`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| bad-json | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"invalid json"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/scheduler

#### GET `/api/scheduler`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 307 | 3ms | 不符合预期(未拒绝)  |  |

#### GET `/api/scheduler/cloud/jobs`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"unknown scheduler endpoint: cloud/jobs"}
 |

#### GET `/api/scheduler/cloud/runtime`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"unknown scheduler endpoint: cloud/runtime"}
 |

#### POST `/api/scheduler/cloud/sync`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"unknown scheduler endpoint: cloud/sync"}
 |
| empty-body | - | 25009ms | 失败(请求异常)  | timeout>25s |
| bad-json | - | 25009ms | 失败(请求异常)  | timeout>25s |

#### GET `/api/scheduler/cloud/usage`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"unknown scheduler endpoint: cloud/usage"}
 |

#### POST `/api/scheduler/coverage`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |
| bad-json | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"}
 |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"unknown scheduler endpoint: coverage"}
 |

#### POST `/api/scheduler/expand`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"}
 |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"unknown scheduler endpoint: expand"}
 |

#### POST `/api/scheduler/plan`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |
| bad-json | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"}
 |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"unknown scheduler endpoint: plan"}
 |

#### GET `/api/scheduler/plans`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"unknown scheduler endpoint: plans"}
 |

#### GET `/api/scheduler/providers/health`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"unknown scheduler endpoint: providers/health"}
 |

#### POST `/api/scheduler/run`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |
| bad-json | 400 | 6ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"}
 |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"unknown scheduler endpoint: run"}
 |

#### GET `/api/scheduler/status?plan_id=:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"计划不存在: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"计划不存在: !!!bad id!!!"}
 |
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"unknown scheduler endpoint: status"}
 |

### api/smart-download

#### GET `/api/smart-download/addresses/:address`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 404 | 2ms | 不符合预期(非2xx)  | {"detail":"地址任务不存在: 0x92e102725a90a1ac0d60560cb1807b9c5820b0a9"}
 |
| invalid-address | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"地址任务不存在: 0x123"}
 |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"unknown address endpoint: 0x92e102725a90a1ac0d60560cb1807b9c5820b0a9"}
 |

#### POST `/api/smart-download/addresses/:address/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-address | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"unknown address endpoint: 0x123/00000000-0000-0000-0000-000000000000"}
 |
| empty-body | 404 | 6ms | 通过(按预期拒绝)  | {"detail":"unknown address endpoint: 0x123/00000000-0000-0000-0000-000000000000"}
 |
| bad-json | 404 | 6ms | 通过(按预期拒绝)  | {"detail":"unknown address endpoint: 0x123/00000000-0000-0000-0000-000000000000"}
 |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"unknown address endpoint: 0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/unknown"}
 |

#### GET `/api/smart-download/batches`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |

#### GET `/api/smart-download/batches/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"批次不存在: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"批次不存在: !!!bad id!!!"}
 |
| wrong-method | 404 | 7ms | 通过(按预期拒绝)  | {"detail":"unknown batch endpoint: unknown"}
 |

#### POST `/api/smart-download/batches/:id/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 8ms | 通过(按预期拒绝)  | {"detail":"unknown batch endpoint: 00000000-0000-0000-0000-000000000000/00000000-0000-0000-0000-0000 |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"unknown batch endpoint: !!!bad id!!!/!!!bad id!!!"}
 |
| empty-body | 404 | 6ms | 通过(按预期拒绝)  | {"detail":"unknown batch endpoint: 00000000-0000-0000-0000-000000000000/00000000-0000-0000-0000-0000 |
| bad-json | 404 | 6ms | 通过(按预期拒绝)  | {"detail":"unknown batch endpoint: 00000000-0000-0000-0000-000000000000/00000000-0000-0000-0000-0000 |
| wrong-method | 404 | 9ms | 通过(按预期拒绝)  | {"detail":"unknown batch endpoint: unknown/unknown"}
 |

#### GET `/api/smart-download/batches/:id/accelerator`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 15ms | 通过(按预期拒绝)  | {"detail":"批次不存在: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 404 | 8ms | 通过(按预期拒绝)  | {"detail":"批次不存在: !!!bad id!!!"}
 |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"unknown batch endpoint: unknown/accelerator"}
 |

#### GET `/api/smart-download/batches/:id/addresses?:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 10ms | 通过(按预期拒绝)  | {"detail":"批次不存在: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"批次不存在: !!!bad id!!!"}
 |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"unknown batch endpoint: unknown/addresses"}
 |

#### GET `/api/smart-download/batches/:id/hardening`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"批次不存在: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"批次不存在: !!!bad id!!!"}
 |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"unknown batch endpoint: unknown/hardening"}
 |

#### POST `/api/smart-download/batches/:id/mode`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 8ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |
| garbage-id | 400 | 31ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |
| empty-body | 400 | 27ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |
| bad-json | 400 | 15ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"}
 |
| wrong-method | 404 | 15ms | 通过(按预期拒绝)  | {"detail":"unknown batch endpoint: unknown/mode"}
 |

#### POST `/api/smart-download/batches/:id/plan`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"批次不存在: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"批次不存在: !!!bad id!!!"}
 |
| bad-json | 404 | 27ms | 通过(按预期拒绝)  | {"detail":"批次不存在: 00000000-0000-0000-0000-000000000000"}
 |
| empty-body | 404 | 30ms | 通过(按预期拒绝)  | {"detail":"批次不存在: 00000000-0000-0000-0000-000000000000"}
 |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"批次不存在: unknown"}
 |

#### GET `/api/smart-download/batches/:id/report`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"批次不存在: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 404 | 32ms | 通过(按预期拒绝)  | {"detail":"非法 batch id"}
 |
| wrong-method | 404 | 39ms | 通过(按预期拒绝)  | {"detail":"unknown batch endpoint: unknown/report"}
 |

#### GET `/api/smart-download/batches/:id/turbo-status`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 8ms | 通过(按预期拒绝)  | {"detail":"批次不存在: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 404 | 6ms | 通过(按预期拒绝)  | {"detail":"批次不存在: !!!bad id!!!"}
 |
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"unknown batch endpoint: unknown/turbo-status"}
 |

#### POST `/api/smart-download/compare`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |
| bad-json | 400 | 12ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"}
 |
| wrong-method | 404 | 12ms | 通过(按预期拒绝)  | {"detail":"unknown smart-download endpoint: compare"}
 |

#### POST `/api/smart-download/coverage/query`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |
| bad-json | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"}
 |
| wrong-method | 404 | 9ms | 通过(按预期拒绝)  | {"detail":"unknown smart-download endpoint: coverage/query"}
 |

#### GET `/api/smart-download/datasets/:id/ledger`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 200 | 12ms | 不符合预期(未拒绝)  | {"ledger":null}
 |
| garbage-id | 200 | 6ms | 不符合预期(未拒绝)  | {"ledger":null}
 |
| wrong-method | 404 | 6ms | 通过(按预期拒绝)  | {"detail":"unknown dataset endpoint: unknown/ledger"}
 |

#### GET `/api/smart-download/events`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 11ms | 通过(按预期拒绝)  | {"detail":"unknown smart-download endpoint: events"}
 |

#### POST `/api/smart-download/import`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 11ms | 通过(按预期拒绝)  | {"detail":"multipart 解析失败: request Content-Type isn't multipart/form-data"}
 |
| bad-json | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"multipart 解析失败: request Content-Type isn't multipart/form-data"}
 |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"unknown smart-download endpoint: import"}
 |

#### GET `/api/smart-download/jobs/:id/snapshot`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 6ms | 通过(按预期拒绝)  | {"detail":"批次不存在: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 404 | 10ms | 通过(按预期拒绝)  | {"detail":"批次不存在: !!!bad id!!!"}
 |
| wrong-method | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"unknown jobs endpoint: unknown/snapshot"}
 |

#### GET `/api/smart-download/jobs/:id/summary`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"批次不存在: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"批次不存在: !!!bad id!!!"}
 |
| wrong-method | 404 | 15ms | 通过(按预期拒绝)  | {"detail":"unknown jobs endpoint: unknown/summary"}
 |

#### POST `/api/smart-download/planner-v2`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 17ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |
| bad-json | 400 | 10ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"}
 |
| wrong-method | 404 | 118ms | 通过(按预期拒绝)  | {"detail":"unknown smart-download endpoint: planner-v2"}
 |

#### POST `/api/smart-download/preflight`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |
| bad-json | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"}
 |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"unknown smart-download endpoint: preflight"}
 |

#### GET `/api/smart-download/registry`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"unknown smart-download endpoint: registry"}
 |

#### GET `/api/smart-download/results/:id?:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"checkpoint not found: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"checkpoint not found: !!!bad id!!!"}
 |
| wrong-method | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"unknown results endpoint: unknown"}
 |

#### GET `/api/smart-download/results/:id/export`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 500 | 3ms | 失败(5xx)  | {"detail":"数据集不存在: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 500 | 18ms | 失败(5xx)  | {"detail":"数据集不存在: !!!bad id!!!"}
 |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"unknown results endpoint: unknown/export"}
 |

#### GET `/api/smart-download/results/:id/summary`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 10ms | 通过(按预期拒绝)  | {"detail":"数据集不存在: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"数据集不存在: !!!bad id!!!"}
 |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"unknown results endpoint: unknown/summary"}
 |

#### GET `/api/smart-download/templates`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 400 | 2ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |

#### POST `/api/smart-download/templates`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: EOF"}
 |
| bad-json | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"请求体解析失败: unexpected EOF"}
 |
| wrong-method | 200 | 3ms | 不符合预期(未拒绝)  | {"templates":null,"total":0}
 |

#### DELETE `/api/smart-download/templates/:id`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"模板不存在: 00000000-0000-0000-0000-000000000000"}
 |
| garbage-id | 404 | 1ms | 通过(按预期拒绝)  | {"detail":"非法模板 id"}
 |
| bad-body | 404 | 4ms | 通过(按预期拒绝)  | {"detail":"模板不存在: 00000000-0000-0000-0000-000000000000"}
 |
| wrong-method | 404 | 5ms | 通过(按预期拒绝)  | {"detail":"unknown template endpoint"}
 |

#### POST `/api/smart-download/templates/:id/instantiate`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 5ms | 通过(按预期拒绝)  | {"detail":"open E:\\codex\\etl\\backend\\data\\smart_download\\v32\\templates\\00000000-0000-0000-00 |
| garbage-id | 400 | 1ms | 通过(按预期拒绝)  | {"detail":"非法模板 id"}
 |
| empty-body | 400 | 6ms | 通过(按预期拒绝)  | {"detail":"open E:\\codex\\etl\\backend\\data\\smart_download\\v32\\templates\\00000000-0000-0000-00 |
| bad-json | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"open E:\\codex\\etl\\backend\\data\\smart_download\\v32\\templates\\00000000-0000-0000-00 |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"unknown template endpoint"}
 |

### api/system

#### GET `/api/system/settings`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### PATCH `/api/system/settings`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| bad-body | 428 | 3ms | 通过(按预期拒绝)  | {"detail":"缺少本机控制台操作标记"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/system/settings/backups`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 428 | 2ms | 通过(按预期拒绝)  | {"detail":"缺少本机控制台操作标记"} |

#### POST `/api/system/settings/backups`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 428 | 12ms | 通过(按预期拒绝)  | {"detail":"缺少本机控制台操作标记"} |
| bad-json | 428 | 12ms | 通过(按预期拒绝)  | {"detail":"缺少本机控制台操作标记"} |
| wrong-method | 200 | 2ms | 不符合预期(未拒绝)  | {"backups":[]} |

#### POST `/api/system/settings/backups/:id/restore`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 428 | 2ms | 通过(按预期拒绝)  | {"detail":"缺少本机控制台操作标记"} |
| garbage-id | 428 | 2ms | 通过(按预期拒绝)  | {"detail":"缺少本机控制台操作标记"} |
| empty-body | 428 | 2ms | 通过(按预期拒绝)  | {"detail":"缺少本机控制台操作标记"} |
| bad-json | 428 | 2ms | 通过(按预期拒绝)  | {"detail":"缺少本机控制台操作标记"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/system/settings/cleanup/execute`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 428 | 6ms | 通过(按预期拒绝)  | {"detail":"缺少本机控制台操作标记"} |
| bad-json | 428 | 7ms | 通过(按预期拒绝)  | {"detail":"缺少本机控制台操作标记"} |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### POST `/api/system/settings/cleanup/preview`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| empty-body | 428 | 3ms | 通过(按预期拒绝)  | {"detail":"缺少本机控制台操作标记"} |
| bad-json | 428 | 3ms | 通过(按预期拒绝)  | {"detail":"缺少本机控制台操作标记"} |
| wrong-method | 404 | 2ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### api/v2

#### GET `/api/v2/financial-export/:chain/address/:address.csv?:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 200 | 1ms | 通过  |  |
| invalid-chain | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"unsupported chain"} |
| invalid-address | 200 | 5ms | 不符合预期(未拒绝)  |  |
| wrong-method | 404 | 3ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

### 传统分析 / 地址

#### GET `/api/address/:address/:id?chain_key=:chain`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| valid | 404 | 50ms | 不符合预期(非2xx)  | {"detail":"该链地址尚无可查询数据"}
 |
| invalid-chain | 400 | 23ms | 通过(按预期拒绝)  | {"detail":"不支持的 EVM 网络: notachain"}
 |
| invalid-address | 400 | 23ms | 通过(按预期拒绝)  | {"detail":"EVM 地址格式错误"}
 |
| wrong-method | 404 | 25ms | 通过(按预期拒绝)  | {"detail":"api route not found"} |

#### GET `/api/analytics/address-stats?:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 503 | 26ms | 失败(5xx)  | {"detail":"分析服务不可用：warehouse 数据未就绪"} |
| wrong-method | 503 | 9ms | 失败(5xx)  | {"detail":"分析服务不可用：warehouse 数据未就绪"} |
| garbage-id | 503 | 15ms | 失败(5xx)  | {"detail":"分析服务不可用：warehouse 数据未就绪"} |

#### GET `/api/analytics/address/:address/path`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-address | 400 | 12ms | 通过(按预期拒绝)  | {"detail":"invalid clickhouse analytics input: invalid EVM address"} |
| valid | 200 | 4421ms | 通过  | [{"a":"0x92e102725a90a1ac0d60560cb1807b9c5820b0a9","b":"0x8f8c9a9f633becd053ed20a8684447043ad273e5", |
| wrong-method | 200 | 4682ms | 不符合预期(未拒绝)  | [{"a":"0x92e102725a90a1ac0d60560cb1807b9c5820b0a9","b":"0x8f8c9a9f633becd053ed20a8684447043ad273e5", |

#### GET `/api/analytics/address/:address/risk`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| invalid-address | 400 | 9ms | 通过(按预期拒绝)  | {"detail":"invalid clickhouse analytics input: invalid EVM address"} |
| valid | 200 | 371ms | 通过  | {"method":"deterministic_clickhouse_screening_v1","risk_level":"medium","risk_reason":"Rule-based sc |
| wrong-method | 200 | 672ms | 不符合预期(未拒绝)  | {"method":"deterministic_clickhouse_screening_v1","risk_level":"medium","risk_reason":"Rule-based sc |

#### GET `/api/analytics/dashboard`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | - | 25019ms | 失败(请求异常)  | timeout>25s |

#### GET `/api/analytics/flow-stats?:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 503 | 2ms | 失败(5xx)  | {"detail":"分析服务不可用：warehouse 数据未就绪"} |
| garbage-id | 503 | 2ms | 失败(5xx)  | {"detail":"分析服务不可用：warehouse 数据未就绪"} |
| wrong-method | 503 | 1ms | 失败(5xx)  | {"detail":"分析服务不可用：warehouse 数据未就绪"} |

#### GET `/api/analytics/graph?limit=:param`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| unknown-id | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"limit must be an integer"} |
| garbage-id | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"limit must be an integer"} |
| query-limit-negative | 400 | 4ms | 通过(按预期拒绝)  | {"detail":"limit must be an integer"} |
| query-limit-nonnumeric | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"limit must be an integer"} |
| wrong-method | 400 | 3ms | 通过(按预期拒绝)  | {"detail":"limit must be an integer"} |

#### GET `/api/analytics/report/asset_summary.json`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 503 | 3ms | 失败(5xx)  | {"detail":"分析服务不可用：warehouse 数据未就绪"} |

#### GET `/api/analytics/report/case-demo/case-report.docx`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 503 | 3ms | 失败(5xx)  | {"detail":"分析服务不可用：warehouse 数据未就绪"} |

#### GET `/api/analytics/report/case-full/case-report.html`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 503 | 52ms | 失败(5xx)  | {"detail":"分析服务不可用：warehouse 数据未就绪"} |

#### GET `/api/analytics/report/case-full/evidence_bundle.json`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 503 | 53ms | 失败(5xx)  | {"detail":"分析服务不可用：warehouse 数据未就绪"} |

#### GET `/api/analytics/report/evidence.json`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 503 | 39ms | 失败(5xx)  | {"detail":"分析服务不可用：warehouse 数据未就绪"} |

#### GET `/api/analytics/report/graph.json`

| 用例 | 状态 | 耗时 | 结论 | 响应/错误摘要 |
|---|---|---|---|---|
| wrong-method | 503 | 9ms | 失败(5xx)  | {"detail":"分析服务不可用：warehouse 数据未就绪"} |

## 7. 有效参数回归明细

| 状态 | 耗时 | URL | 响应摘要 |
|---|---|---|---|
| 通过 | 31ms | `/api/health` | {"analysis_plane":{"available":true,"mode":"duckdb-cli","exe_path":"E:\\codex\\etl\\tools\ |
| 通过 | 3805ms | `/api/v2/explorer/bsc/home` | {"chain_id":56,"coverage_ranges":0,"large_transfers":[{"address":"0xc3a73d3352c3b375b505c6 |
| 通过 | 34ms | `/api/v2/explorer/search?chain=bsc&q=USDT` | {"chain_id":56,"items":[{"chain_id":56,"kind":"TOKEN","logo_uri":"/assets/tokens/56/0x55d3 |
| 通过 | 59ms | `/api/v2/explorer/search?chain=bsc&q=0x92e102725a90a1ac0d60560cb1807b9c5820b0a9` | {"chain_id":56,"items":[{"chain_id":56,"kind":"ADDRESS","subtitle":"Unlabeled EVM address" |
| 通过 | 253ms | `/api/v2/explorer/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/header` | {"balances":{"available":false,"estimated_portfolio_usd":null,"items":[]},"coverage":{"det |
| 通过 | 367ms | `/api/v2/explorer/bsc/tx/0x6097bd3437d7542e12e49b61e3c466cf43eda2265b07f4d6b04098e731746af3` | {"chain_id":56,"block_number":114004503,"block_hash":"","block_time":"2026-08-04T16:50:01Z |
| 通过 | 52ms | `/api/v2/explorer/bsc/token/0x55d398326f99059ff775485246999027b3197955` | {"chain_id":56,"contract_address":"0x55d398326f99059ff775485246999027b3197955","name":"Tet |
| 通过 | 50ms | `/api/v1/explorer/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/summary` | {"detail":"address not found"} |
| 通过 | 191ms | `/api/v1/explorer/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/transactions?limit=5` | {"items":[{"chain_id":56,"address":"0x92e102725a90a1ac0d60560cb1807b9c5820b0a9","counterpa |
| 通过 | 187ms | `/api/v1/explorer/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/token-transfers?limit=5` | {"items":[{"chain_id":56,"address":"0x92e102725a90a1ac0d60560cb1807b9c5820b0a9","counterpa |
| 通过 | 15ms | `/api/v1/explorer/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/counterparties?limit=5` | [] |
| 通过 | 52ms | `/api/v1/explorer/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/daily-stats` | [] |
| 通过 | 103ms | `/api/v1/explorer/bsc/tx/0x6097bd3437d7542e12e49b61e3c466cf43eda2265b07f4d6b04098e731746af3` | {"chain_id":56,"block_number":114004503,"block_hash":"","block_time":"2026-08-04T16:50:01Z |
| 通过 | 9ms | `/api/v1/explorer/bsc/token/0x55d398326f99059ff775485246999027b3197955` | {"chain_id":56,"contract_address":"0x55d398326f99059ff775485246999027b3197955","name":"USD |
| 通过 | 96ms | `/api/v1/explorer/bsc/contract/0x55d398326f99059ff775485246999027b3197955` | {"detail":"address not found"} |
| 通过 | 72ms | `/api/v2/analytics/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/financial-summary?window=30D` | {"chain_id":56,"address":"0x92e102725a90a1ac0d60560cb1807b9c5820b0a9","window":"30D","from |
| 通过 | 85ms | `/api/v2/analytics/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/financial-counterparties?window=30D&limit=5` | [{"counterparty":"0x001bea48c16005d18ed393b194c394f4267471d4","in_usd":null,"out_usd":null |
| 通过 | 35ms | `/api/v2/analytics/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/cex?window=30D` | [] |
| 通过 | 66ms | `/api/v2/analytics/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/dex?window=30D` | {"swap_count":0,"swap_volume_usd":null,"top_protocol":"","canonical_unit":"chain_id+tx_has |
| 通过 | 64ms | `/api/v2/analytics/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/bridge?window=30D` | {"bridge_in_usd":null,"bridge_out_usd":null,"bridge_in_count":0,"bridge_out_count":0,"top_ |
| 未通过 | 754ms | `/api/v2/analytics/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/fifo-retention?window=30D` | {"detail":"financial query failed"} |
| 未通过 | 675ms | `/api/v2/analytics/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/fifo-pass-through?window=30D` | {"detail":"financial query failed"} |
| 未通过 | 1ms | `/api/v2/analytics/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/pnl?window=30D` | {"detail":"invalid financial query"} |
| 通过 | 43ms | `/api/v2/analytics/bsc/address/0x92e102725a90a1ac0d60560cb1807b9c5820b0a9/historical-usd-graph?window=30D` | {"chain_id":56,"address":"0x92e102725a90a1ac0d60560cb1807b9c5820b0a9","from":"2026-07-11T1 |
| 通过 | 6351ms | `/api/v2/financial-quality/bsc?window=30D` | {"chain_id":56,"window":{"name":"30D","start":"2026-07-11T15:23:44.624Z","end":"2026-08-10 |
| 通过 | 9466ms | `/api/v2/data-quality/bsc` | {"chain_id":56,"semantic_completeness":{"chain_id":56,"overall":{"numerator":69154,"denomi |
| 未通过 | 3ms | `/api/v2/pricing/56/token/0x55d398326f99059ff775485246999027b3197955/resolve?timestamp=2025-01-15T00%3A00%3A00Z` | {"detail":"at is required"} |
| 通过 | 15ms | `/api/v2/pricing/56/token/0x55d398326f99059ff775485246999027b3197955/gaps?from=2025-01-01T00%3A00%3A00Z&to=2025-01-02T00%3A00%3A00Z&resolution=1m` | [{"chain_id":56,"token_id":"0x55d398326f99059ff775485246999027b3197955","resolution":"1m", |
| 通过 | 3ms | `/api/price/health` | {"status":"ok","clickhouse":"ok","root":"E:\\database\\price_engine","providers":[{"name": |
| 通过 | 51ms | `/api/price/coverage?chain=bsc&token=0x55d398326f99059ff775485246999027b3197955` | {"covered":false,"token":"0x55d398326f99059ff775485246999027b3197955"} |
| 通过 | 115ms | `/api/price/history/point?chain=bsc&token=0x55d398326f99059ff775485246999027b3197955&timestamp=2025-01-15T00%3A00%3A00Z` | {"age_seconds":0,"chain":"bsc","confidence":"FALLBACK","price_timestamp":"2025-01-15T00:00 |
| 通过 | 151ms | `/api/price/history/candles?chain=bsc&token=0x55d398326f99059ff775485246999027b3197955&start=2025-01-01T00%3A00%3A00Z&end=2025-01-02T00%3A00%3A00Z&interval=1h` | {"candles":[],"chain":"bsc","interval":"1h","token":"0x55d398326f99059ff775485246999027b31 |
| 通过 | 3ms | `/api/entity/stats` | {"addresses":4,"cache_hit_rate":0,"cache_hits":0,"cache_misses":0,"clusters":0,"entities": |
| 通过 | 2ms | `/api/entity/search?q=USDT` | {"items":[{"id":"entity_BSC-USD（USDT）","name":"BSC-USD（USDT）","entity_type":"TOKEN_CONTRAC |
| 通过 | 3334ms | `/api/entity/resolve?chain=bsc&address=0x92e102725a90a1ac0d60560cb1807b9c5820b0a9` | {"address":"0x92e102725a90a1ac0d60560cb1807b9c5820b0a9","chain_key":"bsc","chain_id":56,"e |
| 通过 | 2ms | `/api/flow/history` | {"items":[]} |
| 通过 | 1ms | `/api/files/current` | {"rule_samples":[],"uploads":[]} |
| 通过 | 563ms | `/api/system/settings` | {"audit":[],"backups":[],"capabilities":{"cleanup_preview":true,"credentials_exposed":fals |
| 通过 | 1ms | `/api/system/settings/backups` | {"backups":[]} |
| 通过 | 674ms | `/api/scheduler/cloud/runtime` | {"budget":{"enabled":true,"daily_limit_minutes":60,"max_concurrent_workers":1,"idle_remove |
| 通过 | 1722ms | `/api/scheduler/providers/health` | {"fault_injection":{"all_normal_providers_fail":false},"providers":[{"kind":"rpc","name":" |
| 通过 | 1172ms | `/api/scheduler/cloud/usage` | {"deployment_key_configured":true,"fallback_ratio":0,"r2_configured":true,"registry":{"byt |
| 通过 | 2ms | `/api/scheduler/cloud/jobs` | {"jobs":[],"registry":{"bytes":131316103,"entries":28,"files":76,"rows":2396039}}
 |
| 通过 | 2ms | `/api/crypto/rpc/health` | {"overview":{"configured_endpoints":8,"healthy_endpoints":4,"degraded_endpoints":0,"today_ |
| 通过 | 2ms | `/api/crypto/rpc/endpoints` | {"items":[{"endpoint_id":"rpc-3c72e7af3a0d717fd55b","provider":"CHAINSTACK","chain_key":"b |
| 未通过 | 0ms | `/api/crypto/datasource` | <a href="/api/crypto/datasource/">Moved Permanently</a>.

 |
| 通过 | 1ms | `/api/crypto/parquet/jobs` | []
 |
| 通过 | 0ms | `/api/crypto/parquet/settings` | {"data_root":"E:\\codex\\bsc_analytics","download_concurrency":3,"duckdb_threads":14,"memo |
| 通过 | 1ms | `/api/crypto/download/settings` | {"source":"rpc","csvEmail":"","csvImapHost":"","csvImapPort":993,"csvImapUser":"","csvImap |
| 通过 | 1ms | `/api/download-engine/jobs` | {"detail":"api route not found"} |
| 通过 | 0ms | `/api/dune/auth` | {"access_token":"","authorization":"","cookie":"","has_api_key":false,"has_cookie":false," |
| 通过 | 1ms | `/api/dune/batch/status` | {"id":"","total":0,"completed":0,"failed":0,"status":"idle","accounts":null} |
| 通过 | 0ms | `/api/dune/batch/accounts` | {"accounts":null} |
| 通过 | 0ms | `/api/prefetch/stats` | {"graph_cache":{"count":0,"hits":0,"misses":0},"prefetch":{"total_jobs":0,"active_jobs":0, |
| 通过 | 34ms | `/api/graph/status` | {"status":"GRAPH_READY","last_chunks":["1cfb0a54-924f-4abc-996e-97911416521f-1-c1/chunk-1" |
| 通过 | 2ms | `/api/dataset/events` | {"events":[{"event_id":"graph_increment_applied-1786297068243832900","seq":56,"type":"GRAP |
| 通过 | 1ms | `/api/smart-download/registry` | {"results":[{"chunk_key":"sd-46fe4c02-7208-4122-9cc2-91764bbcec1c","dataset_job_id":"46fe4 |
| 通过 | 1ms | `/api/smart-download/batches` | {"adapters":["csv","rpc","sqd","sqd_cloud"],"batches":[{"id":"ead2fdf2-a01d-492f-9457-bece |
| 通过 | 1ms | `/api/smart-download/templates` | {"templates":null,"total":0}
 |
| 通过 | 0ms | `/api/intelligence/config` | {"max_hops":4,"beam_width":8,"top_paths":10,"min_amount":"0","use_ai":true,"ai_model":"dee |
| 通过 | 1ms | `/api/intelligence/investigations` | {"items":[],"total":0}
 |

## 8. Bug / 问题清单（待统一修复）

| 编号 | 优先级 | 模块 | 端点 | 问题 | 证据 | 影响 | 建议修复方向 |
|---|---|---|---|---|---|---|---|
| BUG-01 | P1 | Explorer v2 / 性能 | `GET /api/v2/explorer/:chain/home` | 首页接口单飞 3.8s，8 并发下超过 25s，前端超时后服务端返回 503（context canceled）。 | solo 3805ms/200；并发 25005ms 客户端超时，服务端 503 context canceled（app.log 23:21:50） | Explorer 首页在并发或大数据量下不稳定，用户看到报错 | 大额活动 ASOF JOIN 增加物化/缓存，限制并发或改流式 |
| BUG-02 | P1 | Financial Analytics | `GET /api/v2/analytics/:chain/address/:address/fifo-retention|fifo-pass-through` | 真实地址+30D 返回 503「financial query failed」，服务端错误 invalid financial flow event at event 33。 | solo 739ms/503；app.log 23:23:43 invalid financial flow event at event 33 | Explorer 分析下拉 Retention/Pass-through 不可用 | 修复 financialflow 事件解析/校验（event 33 数据） |
| BUG-03 | P1 | Financial Analytics | `GET /api/v2/analytics/:chain/address/:address/pnl` | 不传 token 恒 400 invalid financial query；前端 loadStrictFinancialAnalysis('pnl') 不带 token。 | 400「invalid financial query」；financialpnl.ValidateQuery 要求 token 为地址或 native:<chain> | Explorer PnL 页始终不可用 | 后端默认 native:<chain>，或前端传 token |
| BUG-04 | P2 | Crypto 地址 | `GET /api/crypto/addresses/:chain/:address/first-seen` | 非法链返回 500 而非 400。 | 500「不支持的链: 不支持的 EVM 网络: notachain」；app.log 23:21:19 | 非法参数导致 5xx，监控误报 | 链校验失败返回 400 |
| BUG-05 | P2 | Smart Download | `GET /api/smart-download/results/:id/export` | 数据集不存在/垃圾 ID 返回 500 而非 404。 | 500「数据集不存在: ...」；app.log 23:21:20 | 导出不存在数据时报 500 | 不存在返回 404 |
| BUG-06 | P2 | Scheduler | `POST /api/scheduler/cloud/sync` | 空体/坏 JSON 触发真实同步；客户端超时后服务端 context canceled → 500。 | 500 scheduler_cloud_sync_failed context canceled；app.log 23:21:45；另一请求 23:22:14 200 | 同步为长任务但接口同步等待，客户端超时产生 500 | 参数校验 + 异步任务/轮询 |
| BUG-07 | P2 | RPC 管理 | `DELETE /api/crypto/rpc/endpoints/:id|/:id/test` | 删除不存在/垃圾 ID 均返回 200 success。 | DELETE .../00000000-... => 200 {"success":true}；垃圾 ID 同样 200 | 前端误判删除成功，无法发现 ID 错误 | 存在性校验，不存在返回 404 |
| BUG-08 | P2 | 调查 / Smart Download | `GET /api/investigations/:id/{entity-leads,prefetch,reports,reports/diff}；GET /api/smart-download/datasets/:id/ledger` | 不存在/垃圾 ID 返回 200 空数据而非 404。 | 多端点 200 空数组/null | 无效 ID 无错误提示，前端难发现 | ID 校验并返回 404 |
| BUG-09 | P2 | Financial Analytics | `GET /api/v2/analytics/.../financial-counterparties` | window=BADWINDOW、limit=-1/abc 未拒绝，返回 200 数据。 | 200 + 数据；window=80 时 400 但空 window+坏 window 被接受 | 参数契约不一致 | 统一 window/limit 白名单校验 |
| BUG-10 | P2 | 性能 | `GET /api/v2/data-quality/:chain、/api/v2/financial-quality/:chain、/api/entity/resolve` | 单飞 6.4~9.5s，并发下 >25s 前端超时 → 503。 | data-quality 9466ms、financial-quality 6351ms、entity/resolve 3334ms；并发 503 context canceled | 大数据量下页面超时 | 缓存/异步/超时与并发上限 |
| BUG-11 | P3 | 传统分析 | `GET /api/analytics/address-stats、flow-stats、report/*、analytics/dashboard` | 当前返回 503「warehouse 数据未就绪」，成功路径无法验证。 | 503 全部命中（app.log 23:21:19） | DuckDB 未就绪时功能不可用 | 数据就绪环境复测；前端应显示明确降级提示 |
| BUG-12 | P3 | 路由契约 | `多路径 GET/POST 共用（settings/auth/templates/backups/dune/batch/stop 等）；/api/crypto/parquet、/api/crypto/datasource 尾斜杠 301/307` | 错误方法多数不返回 405；尾斜杠重定向。 | GET /api/crypto/parquet/settings => 200（wrong-method）；POST /api/crypto/parquet => 307 | 契约宽松，客户端难以依赖 405 | 按需收敛路由或文档化 |
| BUG-13 | P3 | 数据面（附带发现） | `后台 datasetsync` | 大量 manifest 警告：expected -1 rows but no parquet files、LOCAL_VALIDATION_FAILED 越界/重复 sha。 | app.log 23:22:xx 持续出现 | 数据同步完整性风险 | 单独跟踪修复，与 API 无关 |

## 9. 环境限制与未覆盖项

1. **warehouse 依赖接口**（/api/analytics/*、/api/address/* 等）当前返回 503「warehouse 数据未就绪」，成功路径需在 DuckDB 数据就绪环境复测。
2. **破坏性成功路径**：所有 POST/DELETE/PATCH 的成功场景未执行（避免污染生产数据），仅验证校验与错误路径；DELETE /api/dune/batch/accounts 整体跳过。
3. **依赖外部登录**：Dune 下载/浏览器下载类接口仅验证状态返回，未做真实外部任务。
4. **SSE**：仅验证响应头为 text/event-stream，未消费完整事件流。
5. **占位参数误报**：批量用例中部分“正常”用例因测试占位值（如 start=80、window=80、q=unknown）返回 4xx，已在 6 节标注，不属于接口缺陷。
6. **并发压力**：部分重查询在 8 并发下超时，单飞正常（3.8~9.5s），已按性能问题记录。

## 10. 结论

- 前端 219 个 API 端点已完成清单盘点，637 个批量用例 + 61 个有效参数回归用例均已执行并留档；
- 核心读链路（Explorer 搜索/首页/地址头部、v1 活动、金融摘要/对手/CEX/DEX/Bridge/历史 USD、价格、实体、系统设置、调度状态、RPC 健康等）在真实数据下单飞通过；
- 发现 **13 项** 待修复问题（P1×3、P2×7、P3×3），其中 3 项为功能性不可用（FIFO/PnL/首页并发稳定性），建议优先处理 BUG-01~03；
- 后续统一修复完成后，应重跑本报告的批量用例与有效回归，并补充破坏性接口的受控成功路径测试。

## 11. 修复记录（2026-08-10 修复轮）

> 以下为收到“修复”指令后实施的结果；状态：✅ 已修复并实测 / ⏸ 暂缓（环境或设计原因）。

| 编号 | 状态 | 修复内容 | 复测证据 |
|---|---|---|---|
| BUG-01 | ✅ 部分修复 | Explorer 首页增加 30s TTL 内存缓存 + 同 key 单飞合并，避免重复/并发重查询 | home 首次 3.88s → 二次 0.0009s；并发首查仍受查询本身性能限制 |
| BUG-02 | ✅ | financialflow 零/负金额事件跳过并计数（`skipped_zero_amount_events`），不再整组失败 | fifo-retention / fifo-pass-through 均 200（约 1s），锚点地址跳过 1119 条零金额事件 |
| BUG-03 | ✅ | PnL 接口 token 缺省时默认 `native:<chain_id>` | `/pnl`（不带 token）200，68ms |
| BUG-04 | ✅ | first-seen 在转发前用 `chain.Resolve` 校验链，非法链返回 400 | notachain → 400；bsc → 200 |
| BUG-05 | ✅ | `/smart-download/results/:id/export` 数据集不存在返回 404 | 未知/垃圾 ID → 404 |
| BUG-06 | ✅ | cloud/sync 增加防重入（进行中返回 409）；客户端取消不再记 500 | 并发第二请求 409；首请求正常完成 200 |
| BUG-07 | ✅ | RPC 端点删除先做存在性校验，不存在返回 404 | DELETE 未知/垃圾 ID → 404 |
| BUG-08 | ✅ | 调查类接口（entity-leads/prefetch/reports/reports/diff）与 datasets/:id/ledger 增加存在性校验，不存在返回 404 | 未知 ID 全部 404 |
| BUG-09 | ✅ 误报 | 单参数 window=BADWINDOW / limit=-1 / limit=abc 本就会 400（此前 200 系测试脚本重复参数导致） | 三个单参数用例均 400 |
| BUG-10 | ✅ 部分修复 | data-quality / financial-quality 增加 60s TTL 缓存 | data-quality 5.38s → 0.001s；financial-quality 3.34s → 0.0009s |
| BUG-11 | ⏸ | warehouse 类接口 503 属环境依赖（DuckDB 数据未就绪），需在数据就绪环境复测 | 未修复 |
| BUG-12 | ⏸ | GET/POST 同路径为设计行为、尾斜杠重定向为 Gin 默认，保持现状 | 未修复 |
| BUG-13 | ✅ 部分修复 | datasetsync 对“未知行数且无 parquet 文件”不再报错（`expectedRows <= 0` 视为合法空块）；越界/重复 sha 告警仍属数据质量问题 | 单元测试通过 |

### 本次修复涉及文件

- `internal/financialflow/fifo.go`、`internal/financialflow/types.go`、`internal/financialflow/fifo_test.go`
- `internal/api/financial_v1_handlers.go`、`internal/api/explorer_intelligence_handlers.go`、`internal/api/canonical_v2_handlers.go`
- `internal/api/api_cache.go`（新增 TTL/单飞缓存 + 调查存在性校验）
- `internal/api/entity_intel.go`、`internal/api/investigation_cache.go`、`internal/api/report_engine.go`
- `internal/api/download_scheduler_handlers.go`
- `internal/parquetdownload/handler.go`
- `internal/rpcmanager/manager.go`、`internal/rpcmanager/handler.go`
- `internal/smartdownload/api.go`
- `internal/datasetsync/validator.go`

### 修复后验证

- `go test ./internal/financialflow ./internal/rpcmanager ./internal/datasetsync ./internal/smartdownload ./internal/parquetdownload ./internal/api -count=1` 全部 PASS；
- `go build ./internal/api/ ./cmd/server/` PASS；`run.ps1` 重启成功（PID 30076）；
- 上述复测均为真实 HTTP 请求结果；日志无新增 500。
