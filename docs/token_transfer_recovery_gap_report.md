# Token Transfer Multi-Provider Recovery Layer — 差距分析报告

日期：2026-08-03
对照设计：`.reasonix/attachments/clipboard-20260803-120210.314217-000005.md`（Token Transfer Multi-Provider Recovery Layer V1.0）
对照代码：`internal/downloadscheduler/`、`internal/datasource/`、`internal/parquetdownload/`、`internal/rpcmanager/`

## 1. 结论摘要

设计文档描述的体系在本仓库**大部分已实现**（SQD 可靠性层、健康评分、Router 自动降级、状态机、Provider 接口）。
**唯一的核心差距**：`token_transfer` 数据集只有 SQD 一个 Provider 候选——SQD Portal 503 时任务直接失败，没有恢复通道，设计文档要解决的就是这个问题。

## 2. 逐条对照

| 设计条目 | 设计要求 | 现有实现 | 差距 |
|---|---|---|---|
| §3 Provider 优先级 | token_transfer: AWS > RPC > SQD | token_transfer 仅 SQD 候选 | **核心差距** |
| §4 Provider 接口 | TokenTransferProvider{Name/HealthScore/Download/Support} | `Provider{Kind/Name/CanHandle/Available/ManualOnly/Score/Execute}` + `jobPoller` | 等效 ✓ |
| §5 AWS Provider | AWS BSC parquet → logs → 过滤 topic0 0xddf252ad → 解析 → token_transfer.parquet | AWSProvider 仅支持 BSC 原生 transactions；**aws-public-blockchain 公共数据集无 logs**（2026-07-30 实盘核验：v1.1/bnb/ 顶层仅 blocks/、transactions/） | **数据源不可行，记录偏差**（见 §4） |
| §6 RPC Recovery Provider | eth_getLogs 过滤 Transfer(address,address,uint256)，输出 tx_hash/log_index/token/from/to/value/block_number | RPCProvider 仅 balance（eth_getBalance） | **核心差距：需实现** |
| §7 SQD 健康/降级/恢复 | 503 率 > 30% 自动降级；恢复窗口重新加入 | 冷却阶梯（30s→10min）、熔断器 OPEN/HALF_OPEN、自适应并发 8→4→1、成功率 <85% 降分 | 等效 ✓（阈值策略更细） |
| §8 Provider 评分公式 | success_rate*40 + latency*20 + availability*20 + cost*10 + freshness*10 | coverage/accuracy/speed/cost/reliability 加权（25/25/15/15/20）+ 健康动态衰减 + clampScore | 等效 ✓（保留现有实现） |
| §9 自动恢复流程 | 失败分析 → 自动切换 → 数据合并 → 唯一化 → Parquet → DuckDB | RETRYING/FALLBACK 状态机存在；但 token_transfer 失败后**无候选可切换**；恢复数据无落盘/合并 | **差距：RPC 通道 + 恢复数据落盘/合并** |
| §10 唯一键 | chain_id + transaction_hash + log_index + token_address | `normalize.TokenTransfer` 字段齐全；SQD token_transfers parquet 含全部列 | 模型 ✓；RPC 输出对齐即可 |
| §12 Agent 自治 | 历史→AWS、实时→RPC、SQD 备用，无需用户选择 | Router 按评分自动选择（Layer 1 规则 + Layer 2 评分 + Layer 3 仲裁） | 等效 ✓ |
| §13 状态机 | 新增 WAITING_PROVIDER/PROVIDER_SWITCHING/RECOVERING/MERGING/VALIDATING/COMPLETED | 现有：RETRYING（=RECOVERING）、FALLBACK（=PROVIDER_SWITCHING）、VALIDATING、MERGING、READY_FOR_GRAPH（=COMPLETED） | 等效 ✓（映射说明，不新增状态） |
| §14 验收 | SQD 失败任务不中断、自动切换、自动恢复 | **token_transfer 无法满足** | **差距：补齐后满足** |

## 3. 设计偏差记录

1. **AWS 不能作为 token_transfer 恢复通道**：设计 §5 假设 AWS BSC parquet 含 logs 数据，
   但 `s3://aws-public-blockchain/v1.1/bnb/` 公共数据集当前只有 blocks/ 与 transactions/（2026-07-30 核验）。
   因此 token_transfer 恢复通道采用设计 §6 的 **RPC eth_getLogs**（设计文档本身定位 RPC 为"增量补充/实时查询"）。
   AWS Provider 维持现状（BSC 原生交易首选）。
2. **RPC 恢复窗口限定近期**：RPC 定位为恢复/增量通道（设计 §12「实时数据 -> RPC」）。
   默认窗口最近 90 天，上限 180 天；更早历史数据由 SQD 承担（SQD 恢复后自动回到首选）。
3. **block_time 不填充**：eth_getLogs 返回不含区块时间戳；为控制 RPC 调用量，恢复数据 block_time 为空（NULL），
   以 block_number 排序。后续 V1.1 可批量 RPC 富化时间戳。
4. **状态机不新增状态**：现有 RETRYING/FALLBACK/VALIDATING/MERGING/READY_FOR_GRAPH 已覆盖设计的
   RECOVERING/PROVIDER_SWITCHING/COMPLETED 语义，避免无效重构。

## 4. 实现清单（本轮补齐）

| # | 变更 | 文件 |
|---|---|---|
| 1 | RPCProvider 支持 token_transfer：eth_blockNumber + eth_getLogs（地址分批 ≤100、区块分块 ≤50,000、超限二分）、Transfer 事件解析（topics[1]/[2]→from/to、data→value）、唯一键去重、BEP20(bsc)/ERC20 | internal/downloadscheduler/provider.go（+ rpc_logs.go 新文件） |
| 2 | RecoveryWriter：RPC 恢复数据落盘 CSV → DuckDB COPY 唯一化 Parquet（与 SQD token_transfers 同 schema）+ manifest.json；MERGING 阶段与仓库既有 token_transfers 按唯一键合并去重 | internal/parquetdownload/recovery.go（新文件，Manager 方法） |
| 3 | Scheduler.MERGING 阶段调用合并，StageDetail 展示统计，Plan 增加 Recovery 字段 | internal/downloadscheduler/{scheduler,types}.go |
| 4 | API 装配：注入 RecoveryWriter（parquetdownload.Manager 实现），RPCProvider.WithRecovery | internal/api/handlers.go |
| 5 | 前端：token_transfer 需求行显示「✓ SQD / ⚡ RPC 恢复」 | frontend/src/features/analytics/SmartFillPanel.tsx |
| 6 | 单元测试：RPC 解析/分块二分/窗口 clamp（mock RPCClient）+ writer 真实 duckdb 落盘/合并 | internal/downloadscheduler/recovery_test.go |
| 7 | 文档归档：本设计文档存入 docs/ | docs/token_transfer_multi_provider_recovery_v1.0.md |

## 5. 验收标准（对齐设计 §14）

- SQD 503 时 token_transfer 任务**不中断**：Router 评分自动降 SQD，RPC 恢复通道接管；
- SQD 任务失败 → 自动切换 RPC → 数据落盘 recovery parquet → MERGING 与仓库数据按唯一键合并去重；
- 唯一键 `chain_id + tx_hash + log_index + token_address` 贯穿 raw → parsed → unique → parquet；
- 合并产物可用 DuckDB 直接查询（与 SQD token_transfers 同 schema）。
