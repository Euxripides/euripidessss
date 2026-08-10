# ClickHouse 13 表 Canonical Data Asset 语义审计

审计日期：2026-08-09  
审计范围：`onchain` 数据库当前实际存在的 13 张生产表、`deploy/clickhouse/schema.sql`、`deploy/clickhouse/p1_schema.sql`、Go Writer 与 ClickHouse Explorer / Analytics / Graph / Investigation / Export 查询路径。  
目标基线：`On-chain Canonical Data Asset Layer V2.0`。

## 1. 审计口径与关键结论

### 1.1 评分口径

本报告对“当前生产资产”评分，而不是只对 DDL 的字段数量评分：

- **L0 RAW**：只有容器/原始记录，或虽有表结构但没有生产写入链路，尚不能形成可验证资产。
- **L1 PARSED**：已拆成结构化字段，但关键链上语义仍缺失或不可靠。
- **L2 NORMALIZED**：身份、金额、时间、方向和逻辑键已基本统一。
- **L3 ENRICHED**：具有稳定的 Token、Method、Contract、Entity、Price 等语义补全和来源。
- **L4 EXPLORER_READY**：Canonical API 可直接稳定展示，不依赖前端猜测、临时 RPC 或当前价格回算。
- **L5 INVESTIGATION_READY**：在 L4 基础上，具备证据来源、历史语义、实体/价格/风险解释和可复算派生指标。

Ready 状态使用：

- **是**：现有生产写入、字段语义和读取链路均足够。
- **部分**：已有接口或字段骨架，但仍存在影响正确性的缺口。
- **否**：缺少生产写入/读取链路或关键语义，不应作为最终资产使用。

### 1.2 线上事实核验

2026-08-09 对运行中的 ClickHouse `system.tables` 和 `system.parts` 进行了只读核验：

- `onchain` 实际存在 **13 张表**，表名与两个 schema 文件一致。
- 13 张表均为 `ReplacingMergeTree`。
- 当次核验中 **13 张表的 `total_rows` 和 `total_bytes` 均为 0**，且没有 active part。
- 因此，本报告能够确认“生产结构与代码路径”，但**不能用当前线上数据证明 status、method、metadata、historical USD、entity 等覆盖率**。
- 先前的 10K/100K/1M、分页、50M 和导出验收证明的是管道/性能能力；临时验收数据已清理，不能替代生产语义完整性验收。

### 1.3 总体结论

当前 13 表是一个性能合格的 Data Plane 骨架，但还不是 Canonical Explorer-Ready Data Asset Layer：

1. Writer 当前只生产 `chain_transactions`、`token_transfers`、`internal_transactions`、`contract_creations` 及其双边 `address_activity`；`chain_blocks`、`contracts`、`tokens`、`data_coverage`、`migration_manifest` 没有生产写入路径。
2. 事实表 DDL 预留了大量 L3/L4 字段，但 Writer 只写其中一部分；未写字段会落为默认空串、0、false 或 NULL。**字段存在不等于语义存在。**
3. Transaction `status` 没有可证明的 Receipt 来源，`method_name` 没有 Registry/置信度；Token 身份、Contract 身份、Entity、历史价格和 USD 来源均未形成闭环。
4. `address_activity` 已是 Explorer、Analytics、Graph、Investigation 的共同事实入口，分页键也已验证；但它继承上游缺失语义，当前只能评为 L2，尚不能达到目标 L5。
5. 三张地址聚合表把缺失 USD 通过 `ifNull(...,0)` 计算成 0，无法区分“真实为 0”和“没有历史价格”，不适合作为可审计美元统计。
6. 现有所有事实/聚合表都依赖 `ReplacingMergeTree` 的后台合并；逻辑唯一由排序键表达，物理行在合并前可能重复。现有 Repository 大多使用 `FINAL`，但任何新增查询也必须遵守这一约束，或改用可证明等价的版本化查询。

### 1.4 总览评分

| 表 | 层级角色 | 当前等级 | Explorer Ready | Analytics Ready | Investigation Ready | Export Ready |
|---|---|---:|---|---|---|---|
| `chain_blocks` | Canonical Fact 骨架 | L0 | 否 | 否 | 否 | 部分 |
| `chain_transactions` | Canonical Fact | L2 | 部分 | 部分 | 否 | 部分 |
| `token_transfers` | Canonical Fact | L2 | 部分 | 部分 | 否 | 部分 |
| `internal_transactions` | Canonical Fact | L1 | 部分 | 部分 | 否 | 部分 |
| `contract_creations` | Canonical Fact | L1 | 部分 | 部分 | 否 | 部分 |
| `contracts` | Dimension 骨架 | L0 | 否 | 否 | 否 | 否 |
| `tokens` | Dimension 骨架 | L0 | 否 | 否 | 否 | 否 |
| `address_activity` | Canonical Derived Fact | L2 | 部分 | 部分 | 部分 | 部分 |
| `address_summary` | Derived Analytics | L2 | 部分 | 部分 | 否 | 部分 |
| `address_counterparty_stats` | Derived Analytics | L2 | 部分 | 部分 | 否 | 否 |
| `address_daily_stats` | Derived Analytics | L2 | 部分 | 部分 | 否 | 否 |
| `data_coverage` | Control / Quality 骨架 | L0 | 否 | 否 | 否 | 否 |
| `migration_manifest` | Control / Provenance 骨架 | L0 | 否 | 否 | 否 | 否 |

> “Export Ready=部分”表示已有受控 CSV 导出查询，但导出的仍是语义不完整或当前为空的数据；不表示已经达到 Canonical Export 标准。

## 2. 逐表审计

### 2.1 `chain_blocks`

**用途**：区块头与链级统计事实；为交易的 block identity、时间、矿工、gas 与 base fee 提供基准。

**主数据来源和当前写入**：DDL 定义完整；Go Writer 的 dataset 路由不包含 blocks，仓库中只发现 Export 查询，没有生产 INSERT 路径。当前线上 0 行。

**分区 / 排序 / 逻辑唯一**：

- 分区：`(chain_id, toYYYYMM(block_time))`
- 排序：`(chain_id, block_number)`
- 版本列：`ingested_at`
- 逻辑唯一：`chain_id + block_number`；重组时由较新 `ingested_at` 版本替换。必须保留 hash 变化证据，不能无痕覆盖 reorg 历史。

**当前字段（完整）**：`chain_id`, `block_number`, `block_hash`, `parent_hash`, `block_time`, `miner_address`, `gas_limit`, `gas_used`, `base_fee_per_gas`, `tx_count`, `size_bytes`, `source_provider`, `parser_version`, `schema_version`, `ingested_at`。

**缺失/风险**：

- 无生产 Writer，当前只是空表结构。
- 缺 `ingest_job_id`, `source_range_id`, `normalizer_version`, `updated_at`。
- 缺 canonical/finalized 状态、reorg/orphan 标记和 provider observation identity。
- `miner_address` 对 PoS/不同链的语义不够稳定，宜规范为 `producer_address` 并保留链特定原始字段。

**Ready / 评分**：Explorer 否；Analytics 否；Investigation 否；Export 部分（导出 allowlist 已存在）；**L0**。

**达到目标的迁移建议**：新增 blocks Writer；通过 `ALTER ADD COLUMN` 增加 `block_status`, `is_canonical`, `finality_status`, `ingest_job_id`, `source_range_id`, `normalizer_version`, `updated_at`；另建 append-only `block_observations` 保存 reorg 证据，`chain_blocks` 保持当前 canonical snapshot。上线时回填后校验连续高度、parent hash、tx count 和时间单调性。

### 2.2 `chain_transactions`

**用途**：交易主事实及 Explorer Transaction Detail 的主表。

**主数据来源和当前写入**：SmartDownload 已认证 Parquet → DuckDB 仅作流式 CSV 交换 → `datawarehouse.Writer`。Writer 实际只写：`chain_id`, `block_number`, `block_time`, `transaction_index`, `tx_hash`, `from_address`, `to_address`, `value_raw`, `value_decimal`, `input`, `method_id`, `gas_used`, `gas_price`, `status`, `source_provider`, `ingest_job_id`, `source_range_id`。Explorer `GetTransactionDetail` 直接读取本表。当前线上 0 行。

**分区 / 排序 / 逻辑唯一**：

- 分区：`(chain_id, toYYYYMM(block_time))`
- 排序：`(chain_id, tx_hash)`
- 版本列：`ingested_at`
- 逻辑唯一：`chain_id + tx_hash`。这是稳定的 canonical transaction identity；物理去重依赖 merge/`FINAL`。

**当前字段（完整）**：`chain_id`, `block_number`, `block_hash`, `block_time`, `transaction_index`, `tx_hash`, `from_address`, `to_address`, `nonce`, `value_raw`, `value_decimal`, `native_symbol`, `input`, `method_id`, `method_name`, `tx_type`, `gas_limit`, `gas_price`, `max_fee_per_gas`, `max_priority_fee_per_gas`, `effective_gas_price`, `gas_used`, `transaction_fee_native`, `transaction_fee_usd`, `status`, `is_contract_creation`, `created_contract_address`, `error_message`, `source_provider`, `ingest_job_id`, `source_range_id`, `parser_version`, `normalizer_version`, `schema_version`, `ingested_at`。

**缺失/风险**：

- Writer 未写 `block_hash`, `nonce`, `native_symbol`, `method_name`, `tx_type`, `gas_limit`, EIP-1559 fee 字段、fee、contract creation 字段、error、版本字段；这些会被默认值伪装成“已有数据”。
- `statusText(source.status)` 并不能证明状态来自 Receipt；尚无 `receipt_status_raw`, `receipt_observed_at`, `receipt_provider`。
- 缺 `method_confidence`, `method_display`, `canonical_signature`, `candidate_signatures`, `method_source`。
- `to_address` 和 `created_contract_address` 使用非 Nullable 空串，API 必须额外猜测 null 语义。
- `transaction_fee_usd` 没有 price timestamp/source/confidence。

**Ready / 评分**：Explorer 部分；Analytics 部分；Investigation 否；Export 部分；**L2**。

**达到 L4 的迁移建议**：保持现有表和排序键；先增加 receipt 与 method provenance 列，并新增 `transaction_receipts` 原始/规范事实表和 `method_registry` 维表。Writer 必须联合 Transaction + Receipt 后才写 `SUCCESS|FAILED|UNKNOWN`，禁止默认 SUCCESS。补写全部 canonical 字段，增加字段非空/枚举质量检查。API 返回 Canonical DTO，地址可空值转换由后端完成。历史 fee USD 必须引用 `token_prices` 的时间桶并附 provenance。

### 2.3 `token_transfers`

**用途**：ERC20/ERC721/ERC1155 等 Token 转移事实。

**主数据来源和当前写入**：SmartDownload Token Transfers Parquet → Writer。Writer 实际只写：`chain_id`, `block_number`, `block_time`, `tx_hash`, `log_index`, `token_address`, `token_standard`, `from_address`, `to_address`, `raw_value`, `value_decimal`, `source_provider`, `ingest_job_id`, `source_range_id`。Token Detail 在 `tokens` 无记录时会从本表 `argMax` 回退。当前线上 0 行。

**分区 / 排序 / 逻辑唯一**：

- 分区：`(chain_id, toYYYYMM(block_time))`
- 排序：`(chain_id, tx_hash, log_index, token_id, batch_index)`
- 版本列：`ingested_at`
- 逻辑唯一：当前定义为 `chain_id + tx_hash + log_index + token_id + batch_index`。ERC20 通常由前三项唯一；ERC721/1155 必须稳定填入 `token_id`/`batch_index`，否则批量事件可能碰撞。

**当前字段（完整）**：`chain_id`, `block_number`, `block_time`, `tx_hash`, `transaction_index`, `log_index`, `token_address`, `token_name`, `token_symbol`, `token_decimals`, `token_standard`, `event_signature`, `from_address`, `to_address`, `raw_value`, `value_decimal`, `usd_price`, `usd_value`, `token_id`, `batch_index`, `from_entity_id`, `to_entity_id`, `source_provider`, `ingest_job_id`, `source_range_id`, `parser_version`, `normalizer_version`, `schema_version`, `ingested_at`。

**缺失/风险**：

- Writer 不写 transaction index、name/symbol/decimals、event signature、token id/batch index、USD、entity 和版本字段。
- `value_decimal` 当前由 `value_raw` 直接做十进制定点解析，并未证明按 token decimals 缩放；这会导致 Token amount 的展示语义错误。
- Token identity 虽有 `chain_id + token_address`，但没有 metadata source/confidence/version；事实表中的 name/symbol 可能随时间漂移。
- 没有 receipt-success 约束。失败交易产生的伪 transfer 必须被拒绝；应以 receipt logs 为事实来源。
- USD 缺 price time/source/confidence，不能判断是否为历史价。

**Ready / 评分**：Explorer 部分；Analytics 部分；Investigation 否；Export 部分；**L2**。

**达到 L4 的迁移建议**：Token 身份只保留 `chain_id + token_address` 外键语义，展示字段通过 Token Registry/Dictionary 解析；事实表增加 `receipt_status`, `price_time`, `price_source`, `price_confidence`, `metadata_version`。Writer 按 standard 严格生成 `token_id` 和 `batch_index`，按 Registry decimals 计算 decimal amount，并保存计算版本。新增约束/质量任务验证同一 `(chain,tx,log,batch)` 不重复且只来自成功 receipt log。

### 2.4 `internal_transactions`

**用途**：EVM trace/call 层级事实，用于内部价值转移、调用关系、代理/Factory 调用和调查路径。

**主数据来源和当前写入**：SmartDownload Internal Transactions Parquet → Writer。Writer 实际写：`chain_id`, `block_number`, `block_time`, `tx_hash`, `trace_address`, `trace_index`, `call_type`, `from_address`, `to_address`, `value_raw`, `value_decimal`, `gas_used`, `success`, `error`, `source_provider`, `ingest_job_id`, `source_range_id`。当前线上 0 行。

**分区 / 排序 / 逻辑唯一**：

- 分区：`(chain_id, toYYYYMM(block_time))`
- 排序：`(chain_id, tx_hash, trace_address)`
- 版本列：`ingested_at`
- 逻辑唯一：`chain_id + tx_hash + canonical trace_address`。`trace_index` 只是便捷序号，不能代替层级路径。

**当前字段（完整）**：`chain_id`, `block_number`, `block_time`, `tx_hash`, `trace_address`, `trace_index`, `call_type`, `from_address`, `to_address`, `value_raw`, `value_decimal`, `gas`, `gas_used`, `input`, `output`, `success`, `error`, `depth`, `parent_trace_index`, `source_provider`, `ingest_job_id`, `source_range_id`, `parser_version`, `schema_version`, `ingested_at`。

**缺失/风险**：

- Writer 不写 `gas`, `input`, `output`, `depth`, `parent_trace_index`, parser/schema version。
- `success` 由通用 source `status` 转换，`error` 由同一字段推导，无法证明是 trace result。
- 未验证 `trace_address` 非空、规范格式和父子完整性；空 trace address 会在 ReplacingMergeTree 中折叠。
- Zero-value call 虽未显式过滤，但缺 input/output/depth 后，其调查价值几乎丢失。
- 活动类型写成 `INTERNAL_TRANSACTION`，与目标固定枚举 `INTERNAL_TRANSFER` 不一致。

**Ready / 评分**：Explorer 部分；Analytics 部分；Investigation 否；Export 部分；**L1**。

**达到 L4/L5 的迁移建议**：建立 trace source contract，强制写全 `trace_address`, `call_type`, `depth`, `parent_trace`, input/output/gas/result；保留 zero-value calls。建议新增字符串 `parent_trace_address`，避免 parent index 在不同 provider 下歧义。统一 success/error 的 provider provenance。新增 trace tree 完整性质量规则，并从 trace 派生 `INTERNAL_TRANSFER` 与 `CONTRACT_CALL` 两类 activity，而不是混用一个类型。

### 2.5 `contract_creations`

**用途**：CREATE/CREATE2/Factory/Proxy/Token 创建事件事实。

**主数据来源和当前写入**：SmartDownload Contract Creations Parquet → Writer。Writer 实际只写：`chain_id`, `block_number`, `block_time`, `tx_hash`, `creator_address`, `contract_address`, `creation_type`, `factory_address`, `source_provider`, `ingest_job_id`, `source_range_id`。当前线上 0 行。

**分区 / 排序 / 逻辑唯一**：

- 分区：`(chain_id, toYYYYMM(block_time))`
- 排序：`(chain_id, contract_address)`
- 版本列：`ingested_at`
- 逻辑唯一：`chain_id + contract_address`；一次地址部署事实。自毁后重建等链特例需额外 incarnation/version 模型，不能被静默覆盖。

**当前字段（完整）**：`chain_id`, `block_number`, `block_time`, `tx_hash`, `creator_address`, `contract_address`, `creation_type`, `factory_address`, `init_code_hash`, `runtime_code_hash`, `deployer_nonce`, `token_detected`, `token_standard`, `contract_name`, `compiler_version`, `is_proxy`, `proxy_type`, `implementation_address`, `source_verified`, `source_provider`, `ingest_job_id`, `source_range_id`, `parser_version`, `schema_version`, `ingested_at`。

**缺失/风险**：

- Writer 不写所有 bytecode、nonce、token、compiler、proxy、verification 和版本字段，DDL 的默认 false/空串会被误读为“已检测且不是 proxy/token”。
- 未证明 CREATE/CREATE2/factory 分类来源；`factory_address` 只透传。
- 与 `chain_transactions.is_contract_creation`、`created_contract_address` 和 `contracts` 尚无一致性写入事务/校验。

**Ready / 评分**：Explorer 部分；Analytics 部分；Investigation 否；Export 部分；**L1**。

**达到 L4 的迁移建议**：区分 `unknown` 与 false，增加 detector/source/confidence/version 字段；基于 receipt contractAddress + trace CREATE/CREATE2 生成 creation fact；异步 Contract Intelligence 写 runtime hash、proxy、implementation 和 token detection。增加四方一致性检查：Transaction、Creation、Contract、Address Activity 必须引用同一 chain/tx/contract。

### 2.6 `contracts`

**用途**：每个合约地址的当前 Canonical Contract Identity/Intelligence 维度。

**主数据来源和当前写入**：没有生产 Writer/refresh 路径。Explorer 首先查本表；没有记录时回退 `contract_creations`。当前线上 0 行。

**分区 / 排序 / 逻辑唯一**：

- 分区：无
- 排序：`(chain_id, contract_address)`
- 版本列：`ingested_at`
- 逻辑唯一：`chain_id + contract_address`，代表当前快照；历史变化应另存 history/observation 表。

**当前字段（完整）**：`chain_id`, `contract_address`, `creator_address`, `creation_tx_hash`, `creation_block`, `creation_time`, `bytecode_hash`, `runtime_bytecode_hash`, `contract_name`, `is_verified`, `is_proxy`, `proxy_type`, `implementation_address`, `abi_json`, `token_standard`, `first_seen`, `last_seen`, `risk_flags`, `ingested_at`。

**缺失/风险**：

- 空表且无生产者；当前 Explorer fallback 会把未检测字段的默认值当结果。
- 缺 `factory_address`, creation type, ABI source/confidence/version, verification source/time, proxy detector version/confidence, implementation observation block。
- ABI 大字符串直接放主快照表，不利于版本管理与复用。
- 无 Contract Family ID、runtime hash family 统计和 metadata provenance。

**Ready / 评分**：四类 Ready 均否；**L0**。

**达到 L4 的迁移建议**：新增 Contract Identity Resolver/Writer；本表保留轻量当前快照，ABI 转入版本化 `abi_registry`，增加 `abi_id` 引用。新增 `contract_observations` 或 `contract_metadata_history` 保存 bytecode/proxy/implementation/verification 演变；通过 runtime hash 派生 `contract_family_id`。填充前禁止将 default false 解释为“已检测为否”。

### 2.7 `tokens`

**用途**：Canonical Token Registry 当前快照；Token identity 必须是 `chain_id + contract_address`。

**主数据来源和当前写入**：没有生产 TokenMetadataResolver/Writer。Explorer 先查本表，空时从 `token_transfers` 回退 name/symbol/decimals/standard。当前线上 0 行。

**分区 / 排序 / 逻辑唯一**：

- 分区：无
- 排序：`(chain_id, contract_address)`
- 版本列：`ingested_at`
- 逻辑唯一：`chain_id + contract_address`；symbol 绝不是键。

**当前字段（完整）**：`chain_id`, `contract_address`, `name`, `symbol`, `decimals`, `token_standard`, `logo_uri`, `logo_source`, `logo_hash`, `official_website`, `is_verified`, `is_spam`, `first_seen_block`, `first_seen_time`, `last_metadata_refresh_at`, `ingested_at`。

**缺失/风险**：

- 空表且无 Resolver；fallback 依赖 transfer 中同样未被 Writer 填充的 metadata。
- 缺 `metadata_source`, `metadata_confidence`, `metadata_updated_at`/version、manual override provenance、conflict state。
- 缺 metadata history；诈骗 Token 改名后不可审计。
- `decimals UInt8` 默认 0，无法区分真实 0 decimals 与 unknown。
- Logo 路径字段存在，但无按 `(chain,contract)` 解析/校验/缓存闭环。

**Ready / 评分**：四类 Ready 均否；**L0**。

**达到 L4 的迁移建议**：实现优先级固定的 TokenMetadataResolver；增加 `metadata_source`, `metadata_confidence`, `metadata_version`, `resolution_status`, `decimals_known`。新增 append-only `token_metadata_history`，本表只保留当前胜出快照。Logo 缓存在 E 盘并按 chain+contract 命名，数据库保存 URI/hash/source；绝不按 symbol 复用。上线验收必须包含真实 BSC USDT 和同 symbol 假 Token。

### 2.8 `address_activity`

**用途**：面向地址的统一活动事实；Explorer 分页、Analytics、Graph、Investigation 的共同核心输入。

**主数据来源和当前写入**：Writer 从四类输入事实逐行生成双边活动：from 为 OUT、to 为 IN、同地址为 SELF。当前映射类型为 `NATIVE_TRANSFER`, `TOKEN_TRANSFER`, `INTERNAL_TRANSACTION`, `CONTRACT_CREATION`。每个输入逻辑事件通常产生两个 activity rows。当前线上 0 行。

**分区 / 排序 / 逻辑唯一**：

- 分区：`(chain_id, toYYYYMM(block_time))`
- 排序：`(chain_id, address, block_time, tx_hash, event_index)`
- 版本列：`ingested_at`
- 逻辑唯一：`chain_id + address + block_time + tx_hash + event_index`。`event_index` 当前采用 `tx:<index>`, `log:<index>`, `trace:<trace_address>`, `contract_creation`；它必须在同一地址/交易内对所有事件全局唯一且版本稳定。

**当前字段（完整）**：`chain_id`, `address`, `counterparty_address`, `direction`, `activity_type`, `block_number`, `block_time`, `tx_hash`, `event_index`, `token_address`, `token_symbol`, `amount`, `usd_value`, `method_id`, `method_name`, `status`, `counterparty_entity_type`, `counterparty_label`, `source_provider`, `ingest_job_id`, `source_range_id`, `ingested_at`。

**缺失/风险**：

- Writer 不写 `token_symbol`, `usd_value`, `method_name`, entity/label；这些字段当前大多为空。
- 缺 raw amount、decimals、token standard/token id、USD price provenance、counterparty entity ID/role、label source/confidence。
- 缺 parser/normalizer/schema/enrichment version；无法从 activity 追到精确语义版本。
- 固定枚举不一致：现有 `INTERNAL_TRANSACTION`/`CONTRACT_CREATION` 与目标 `INTERNAL_TRANSFER`/`CONTRACT_CREATE` 不统一；尚不支持 NFT、CONTRACT_CALL、DEX、BRIDGE、APPROVAL。
- `status` 继承源表，Token Transfer 没有 Receipt 成功约束，存在失败交易语义风险。
- `event_index` 的 token log 只含 log index；ERC1155 batch 内多项会碰撞。
- 当前只在 `req.Address` 非空时刷新该地址聚合，而不是刷新本批次全部受影响地址；其他参与地址的 summary/stats 可能陈旧或缺失。

**Ready / 评分**：Explorer 部分；Analytics 部分；Investigation 部分；Export 部分；**L2**。它是最接近目标的核心表，但还未达到 L5。

**达到 L5 的迁移建议**：不重建表，先扩充 canonical activity columns；统一枚举与 direction，增加 `asset_type`, `amount_raw`, `amount_decimal`, `token_id`, `batch_index`, Method/Token/Entity/Price 引用及各自 provenance。将 event identity 规范为 dataset + tx + event position，必要时因排序键不可原地修改而建立 `address_activity_v2` 影子表并双写迁移。Writer 收集本批全部 distinct participants 并批量刷新受影响地址。建立 source-fact ↔ activity 双向一致性检查，目标必须是 100%。

### 2.9 `address_summary`

**用途**：地址级当前聚合快照，供 Explorer Address Summary 使用。

**主数据来源和当前写入**：`RefreshAddressAnalytics` 从某一地址的 `address_activity FINAL` 聚合插入；Writer 完成认证写入后，仅在请求包含 `req.Address` 时触发。当前线上 0 行。

**分区 / 排序 / 逻辑唯一**：

- 分区：无
- 排序：`(chain_id, address)`
- 版本列：`updated_at`
- 逻辑唯一：`chain_id + address`，表示当前快照。

**当前字段（完整）**：`chain_id`, `address`, `address_type`, `first_seen_time`, `last_seen_time`, `tx_count`, `in_tx_count`, `out_tx_count`, `token_transfer_count`, `internal_tx_count`, `nft_transfer_count`, `contract_created_count`, `unique_counterparty_count`, `native_received`, `native_sent`, `native_netflow`, `usd_received`, `usd_sent`, `usd_netflow`, `active_days`, `max_single_in_usd`, `max_single_out_usd`, `top_counterparty`, `cex_interaction_count`, `dex_interaction_count`, `bridge_interaction_count`, `risk_score`, `updated_at`。

**缺失/风险**：

- `address_type` 固定写 `ADDRESS`，不是 EOA/Contract 判定。
- cex/dex/bridge/risk 当前硬编码为 0；不是“已计算为零”。
- USD 使用 `ifNull(usd_value,0)`，无 price coverage，导致 unknown 被计作 0。
- 缺最大 native 入出、按 token 统计、priced/unpriced counts、数据 as-of block、source/enrichment version。
- `top_counterparty` 是最近时间 `argMax`，并非金额或次数意义上的 Top，字段名具有误导性。

**Ready / 评分**：Explorer 部分；Analytics 部分；Investigation 否；Export 部分；**L2**。

**达到 L5 的迁移建议**：发布 `AddressSummaryV2`，增加 `as_of_block/time`, priced/unpriced coverage、token/internal counts、Entity/DEX/Bridge 真实指标及指标版本。将 USD 字段改为 Nullable 或同时增加 coverage，禁止 unknown→0。把 `top_counterparty` 拆成 `latest_counterparty`, `top_by_amount`, `top_by_count`。采用版本化聚合任务并记录 `source_max_ingested_at` 与 `calculation_version`。

### 2.10 `address_counterparty_stats`

**用途**：按 address + counterparty + direction 的当前聚合，供 Explorer Counterparties 使用。

**主数据来源和当前写入**：`RefreshAddressAnalytics` 从 `address_activity FINAL` 聚合。读取端额外筛选该地址最大 `updated_at`，形成一次刷新快照。当前线上 0 行。

**分区 / 排序 / 逻辑唯一**：

- 分区：`(chain_id, cityHash64(address) % 64)`
- 排序：`(chain_id, address, counterparty_address, direction)`
- 版本列：`updated_at`
- 逻辑唯一：上述四字段；不同刷新版本由 ReplacingMergeTree 替换。

**当前字段（完整）**：`chain_id`, `address`, `counterparty_address`, `direction`, `activity_count`, `tx_count`, `native_amount`, `usd_value`, `first_seen_time`, `last_seen_time`, `updated_at`。

**缺失/风险**：

- 无 entity、role、label、share、netflow、Top rank 和 concentration。
- IN/OUT 分行，不能直接表达同一对手的双向净额。
- native amount 由限定 activity types 计算，但 USD 汇总所有 activity，口径不对称。
- USD unknown→0，无 priced coverage/provenance。
- 只刷新请求 subject；非 subject 参与地址可能没有聚合。

**Ready / 评分**：Explorer 部分；Analytics 部分；Investigation 否；Export 否；**L2**。

**达到 L5 的迁移建议**：增加或新建 Counterparty V2 snapshot：同时保存 in/out/net、count/amount/USD、share、Entity/role/label 引用、priced coverage、Top1/5/10 concentration 与 `as_of`/calculation version。维度更新采用 re-enrichment，不重写大事实表。导出服务增加明确 allowlist 后才可称 Export Ready。

### 2.11 `address_daily_stats`

**用途**：地址日级 IN/OUT/native/USD/对手数趋势聚合。

**主数据来源和当前写入**：`RefreshAddressAnalytics` 从 `address_activity FINAL` 按日期聚合。读取端筛选该地址最大 `updated_at`。当前线上 0 行。

**分区 / 排序 / 逻辑唯一**：

- 分区：`(chain_id, toYYYYMM(activity_date))`
- 排序：`(chain_id, address, activity_date)`
- 版本列：`updated_at`
- 逻辑唯一：`chain_id + address + activity_date`。

**当前字段（完整）**：`chain_id`, `address`, `activity_date`, `in_count`, `out_count`, `in_native_amount`, `out_native_amount`, `native_netflow`, `in_usd_value`, `out_usd_value`, `usd_netflow`, `unique_counterparty_count`, `updated_at`。

**缺失/风险**：

- IN/OUT count 包含所有 activity，而 native amount 只含一部分 activity，字段口径未显式版本化。
- USD unknown→0，缺 priced/unpriced counts 和 historical price provenance。
- 缺 token/internal/NFT/contract/DEX/bridge 分项，无法支撑 AddressSummaryV2 与调查时间线。
- 无 `as_of_block`, source watermark, calculation version。

**Ready / 评分**：Explorer 部分；Analytics 部分；Investigation 否；Export 否；**L2**。

**达到 L5 的迁移建议**：保留当前表用于兼容，新增日统计 V2 或扩字段，明确每项 metric definition；加入分类型 counts/amounts、price coverage、entity interaction、source watermark、calculation version。价格/标签 re-enrichment 后只重算受影响的 address-date 分区。

### 2.12 `data_coverage`

**用途**：声明某 chain/dataset/subject/block range 的采集覆盖、行数、状态与 manifest hash，是数据完整性控制面。

**主数据来源和当前写入**：仓库中未发现生产 INSERT/refresh 代码。当前线上 0 行。

**分区 / 排序 / 逻辑唯一**：

- 分区：无
- 排序：`(chain_id, dataset, subject, from_block, to_block)`
- 版本列：`updated_at`
- 逻辑唯一：上述五字段；相邻/重叠 range 的整体覆盖不能只靠单行键判断，必须有 coverage reconciliation。

**当前字段（完整）**：`chain_id`, `dataset`, `subject`, `from_block`, `to_block`, `from_time`, `to_time`, `row_count`, `status`, `source_provider`, `manifest_sha256`, `updated_at`。

**缺失/风险**：

- 无生产者，不能回答实际覆盖范围。
- 缺 expected rows、parsed/inserted/rejected/duplicate、gap/reorg、finality、download job、parser/schema version。
- `subject` 语义未枚举（global/address/token），容易出现同名不同义。

**Ready / 评分**：四类 Ready 均否；**L0**。

**达到 L5 控制资产的迁移建议**：将 SmartDownload/Writer 成功事务与 coverage upsert 绑定；定义固定 dataset/subject/status 枚举。新增 `expected_rows`, `inserted_rows`, `rejected_rows`, `gap_count`, `is_complete`, `finalized_to_block`, lineage/version 字段。实现 range 合并与 gap 检测作业，并让 Data Quality API 只基于已认证 coverage 出结论。

### 2.13 `migration_manifest`

**用途**：记录一次源文件/迁移的校验和、解析、去重、插入、拒绝、状态与时间，承担批次级 provenance/reconciliation。

**主数据来源和当前写入**：DDL 已定义，但 Go Writer/SmartDownload 当前没有写入本 ClickHouse 表的路径；控制状态主要在其他控制面。当前线上 0 行。

**分区 / 排序 / 逻辑唯一**：

- 分区：无
- 排序：`migration_id`
- 版本列：`updated_at`
- 逻辑唯一：`migration_id`；source hash 可用于检测同一资产重复导入，但当前未设唯一语义。

**当前字段（完整）**：`migration_id`, `source_path`, `source_sha256`, `dataset`, `chain_id`, `source_rows`, `parsed_rows`, `unique_rows`, `inserted_rows`, `rejected_rows`, `parser_version`, `schema_version`, `status`, `error_message`, `started_at`, `completed_at`, `updated_at`。

**缺失/风险**：

- 无生产写入，因此无法将 ClickHouse 行追溯到 manifest。
- 事实表使用 `ingest_job_id`，manifest 使用 `migration_id UUID`，两种 identity 尚未建立外键式契约。
- 缺 `normalizer_version`, source provider/range, target table/partition, activity rows、verification result、reparse parent、enrichment versions。
- source_path 可能暴露本地路径；对外 API/日志需要脱敏。

**Ready / 评分**：四类 Ready 均否；**L0**。

**达到 L5 控制资产的迁移建议**：统一 `ingest_job_id` 与 `migration_id`（或增加稳定映射列）；Writer 开始前写 RUNNING，源表+activity reconciliation 成功后写 COMPLETED，失败写 FAILED/错误分类。增加 target rows、activity rows、verification hash、版本和 parent job。路径只保存在受控内部字段，对 Explorer/Export 返回 artifact ID 与 hash，不暴露绝对路径。

## 3. 当前 Go 读取/写入依赖图

```text
Certified Parquet
  -> DuckDB COPY TO CSV（仅交换，不是在线 Reader）
  -> datawarehouse.Writer
     -> chain_transactions | token_transfers | internal_transactions | contract_creations
     -> address_activity（每个事件按地址生成 IN/OUT/SELF）
     -> RefreshAddressAnalytics(req.Address)
        -> address_summary
        -> address_counterparty_stats
        -> address_daily_stats

Explorer
  -> chain_transactions / token_transfers / internal_transactions / contract_creations
  -> tokens（无数据时回退 token_transfers）
  -> contracts（无数据时回退 contract_creations）
  -> address_activity / address_summary / 两张 P1 聚合表

Analytics / Graph / Investigation
  -> address_activity FINAL

Export allowlist
  -> chain_blocks / chain_transactions / token_transfers / internal_transactions
  -> contract_creations / address_activity / address_summary
```

尚未接入生产写入的表：`chain_blocks`, `contracts`, `tokens`, `data_coverage`, `migration_manifest`。

## 4. 跨表语义缺口与迁移优先级

### P0：先保证事实正确，不增加 UI 猜测

1. **Receipt/Status**：新增 receipt canonical source；交易 status 只由 receipt 生成，保存 raw status 和 provider provenance；失败交易不得形成成功 Token Transfer。
2. **Writer 字段覆盖**：Writer 不得让“未提供”静默落成 0/false/空串。增加 known/status/confidence 或 Nullable 语义，并写入 parser/normalizer/schema version。
3. **Canonical identity**：强制 EVM 地址小写、tx hash、trace address、log/batch/token id 的逻辑键校验；为 batch NFT 修复 event identity。
4. **Address Activity 一致性**：每个 source fact 与 activity 双向 reconciliation；刷新批次中全部受影响地址，不仅是 `req.Address`。
5. **Provenance 闭环**：启用 `migration_manifest` 和 `data_coverage`，使 `ingest_job_id/source_range_id` 可追溯、可对账、可 reparse。

### P0：建立 Explorer 必需维度

6. **Method Registry**：selector、canonical signature、display、source、confidence、候选冲突；API 不随机挑选冲突签名。
7. **Token Registry + History + Logo Cache**：唯一身份 `(chain_id, contract_address)`；metadata/logo 均带来源与置信度；decimals unknown 不能等于 0。
8. **Contract Identity**：creation fact、contract snapshot、ABI Registry、proxy/implementation detector、runtime family 形成可审计链路。
9. **Canonical API DTO**：前端不直接依赖 ClickHouse 列，不自行缩放 raw amount、不 RPC 补字段、不按 symbol 找图标。

### P1：可解释分析和调查

10. **Entity/Label Registry**：address → entity → role，标签带类型、来源、置信度与历史；维度更新通过 re-enrichment，不重写大事实表。
11. **Historical Price**：token/time bucket 价格事实；所有 USD 保存 price/time/source/confidence，稳定币 fallback 也必须标记。
12. **聚合 V2**：Address Summary、Counterparty、Daily 均增加 priced coverage、as-of、calculation version 和 entity/DEX/bridge 指标；禁止 unknown USD 归零。
13. **Semantic Completeness**：按 chain/dataset/date 预计算 status/method/token/price/contract/entity/decode coverage，并在 Data Quality API/Dashboard 展示分母、分子、未知和更新时间。

## 5. 推荐迁移方式

遵守“不推翻现有 13 表”的原则：

1. 对不改变逻辑键的字段优先 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`。
2. Method、Token、ABI、Entity、Price、Metadata History 使用独立 Dimension/Registry/History 表；事实表保存稳定 identity 和必要的 materialized reference/version。
3. 需要改变排序键或事件唯一键时，不原地破坏数据：创建 `*_v2` 影子表，双写、回填、对账、基准测试后再原子切换 Repository。
4. Re-enrichment 只更新维度或重算受影响的聚合分区，不重新下载、不重写 50M 事实。
5. 所有新查询在 50M 基准数据上比较 `FINAL`、Dictionary、JOIN 和预计算方案；只有实测不回退当前首屏目标后才上线。

## 6. Phase 0 验收门槛

本审计完成不代表语义层完成。下一阶段至少同时满足以下证据后，才可把核心表提升等级：

- 线上非空真实样本；按 dataset/chain/date 可复算行数和 logical unique 数。
- Transaction status 由 Receipt 证明，覆盖率 ≥ 99.99%，失败交易案例无错误 Transfer。
- 可解析调用 Method Resolution ≥ 95%，selector 冲突不会随机解析。
- 主流 Token Metadata ≥ 99.9%，decimals ≥ 99.99%；真实/假 USDT identity 和 logo 隔离。
- Contract Creation ≥ 99.99%，Transaction/Creation/Contract/Activity 四处一致。
- Address Activity source↔derived reconciliation = 100%，包含批量 Token 和 zero-value trace 案例。
- Historical USD 达到明确目标，并能区分 historical、fallback、missing。
- 每项覆盖率均可追到 provider、job、parser/normalizer/schema/enrichment version。
- DuckDB 保持 Parquet Writer 交换/质量校验角色，不重新成为在线 Explorer Reader。

## 7. 最终判定

当前生产系统已经具备稳定的 ClickHouse Data Plane 与高性能查询路径，但 **13 张表整体仍处于 L0-L2，尚无 L4/L5 表**。最关键的升级不是继续增加查询页面，而是让现有 Writer 真正填满 Canonical 事实语义，并建立 Receipt、Method、Token、Contract、Entity、Historical Price 和 Provenance 的可审计闭环。

优先升级顺序应为：

```text
Receipt / Status + Writer 完整字段
  -> Canonical Transaction / Transfer / Trace / Creation
  -> Token / Method / Contract Registry
  -> Canonical Address Activity 100% 对账
  -> Entity / Price / Aggregates V2
  -> Semantic Completeness Dashboard
  -> Explorer Intelligence UI
```
