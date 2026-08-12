# 智能下载前端全功能与数据质量测试报告

- 测试日期：2026-08-11
- 测试环境：Windows，`http://127.0.0.1:8000`
- 测试结论：**不通过，当前版本不满足核心功能上线验收要求**
- 验收原则：HTTP 200 仅代表请求可达；只有界面行为、任务状态、返回数据、落盘文件、认证结论及真实链上事实相互一致，才计为通过。

## 1. 测试范围与方法

本次覆盖智能下载的四个前端页签，以及页面中可操作的按钮、选择器、开关、弹窗和结果动作：

1. 创建下载：地址输入、CSV/TXT/XLSX 导入、网络、数据集、优先级、资源档位、紧急模式、高级策略、区块/时间范围、相关范围复用、强制重下、预检、模板保存/删除、创建任务。
2. 任务中心：列表、分页、刷新、状态筛选、任务选择、摘要、日志、地址明细、暂停、恢复、取消、一键升档、任务对比。
3. 智能预取：统计、候选列表、固定地址、刷新、立即展开、升级交互、底层批次结果。
4. 结果数据：结果分组、展开/收起、查看、过滤、空态、复制交易哈希、导出 Excel、地址画像、关系图、调查台跳转。
5. 数据质量：注册表与 Parquet 一致性、唯一性、必填字段、范围边界、版本重复、认证状态、Excel 内容及可读性、真实 BSC RPC 逐笔交叉核验。
6. 兼容性：桌面端及 390×844 移动端布局。

测试采用 Playwright 操作真实前端，配合前端实际调用的 API、DuckDB/Parquet 查询、导出 XLSX 解析和 BSC RPC 回执核验。未将单纯的 2xx 响应计为通过。

## 2. 总体结果

| 验收域 | 结果 | 说明 |
|---|---|---|
| 页面与基础交互 | 部分通过 | 四页签、主要表单、弹窗、导入、分页、复制和跳转可用 |
| 任务生命周期 | **不通过** | 已取消任务会反转为 COMPLETED，父子状态不一致 |
| 下载与缓存正确性 | **不通过** | 已知 1135 行的相同范围可被命中为 0 行并认证通过 |
| 全数据集能力 | **不通过** | 7 个可选数据集中多个无 Provider、RPC 不可用或写入失败 |
| 结果聚合与版本 | **不通过** | 相同业务数据的版本被重复相加，查看动作可落到旧的空结果 |
| 认证与质量语义 | **不通过** | CERTIFIED/PARTIAL_CERTIFIED 与实际失败、空结果、取消状态矛盾 |
| 导出数据内容 | 通过 | 抽检 XLSX 1135 行，事件唯一、必填字段完整、范围正确 |
| 导出可审计性 | 部分通过 | 数据值完整，但默认列宽使哈希、地址和表头大面积截断 |
| 桌面端显示 | 部分通过 | 核心页面可操作，但时间显示为公元 58287 年 |
| 移动端显示 | **不通过** | 预取页宽 875px，结果页宽 411px，超过 390px 视口并发生裁切 |

## 3. 严重缺陷

### P0-1：取消任务最终反转为完成

- 任务 `d2853681-6b94-446d-a839-2ab9f227d7e2`：前端执行恢复、暂停、取消后，地址及数据集保持 `CANCELED`，但批次最终变成 `COMPLETED`。
- 取消请求约 18 秒后才进入取消态，随后又被完成态覆盖。
- 报告同时给出 `DATASET_PARTIAL_CERTIFIED`、20% coverage、0 rows，与取消事实冲突。
- 任务 `d99393bf-372b-46c5-9ea3-d5e0ed3ed19e` 也出现批次 `COMPLETED`，但地址为 `CANCELED`，子数据集包含 FAILED/CANCELED/COMPLETED 的混合状态。

影响：任务中心无法作为可信的审计入口，用户会把未完成或已取消任务误判为完成。

### P0-2：本地命中把已知 1135 行结果变成 0 行并认证通过

相同地址、数据集与区块范围已存在 1135 行有效 `token_transfers`，新任务却以 `local_hit` 完成并得到 0 行，同时出现 `BATCH_CERTIFIED`/完成状态。相同精确范围在结果注册表中共 6 个版本，行数同时存在 0 和 1135，但均被标记为 CERTIFIED。

影响：缓存复用会静默丢数据；认证标签不能证明数据完整。

### P0-3：版本聚合重复计算，1135 行显示为 2270 行

地址 `0xc988...`、数据集 `token_transfers` 的两个非空版本各 1135 行。排除 `source_range_id` 和 `ingested_at` 后，两个 Parquet 的业务数据完全相同；结果页却将版本行数相加显示为 2270，而不是选择一个有效版本或去重。

影响：结果统计被翻倍，后续分析、导出选择和审计口径均不可靠。

### P0-4：结果“查看”可能打开旧的空版本

分组中 `latest` 由注册表遍历顺序覆盖，并非按创建时间/成功质量选择。点击“查看”会落到旧的 0 行版本，而同范围存在 1135 行有效版本。

影响：用户从汇总进入详情后可能看到空数据，且无法判断哪个版本是权威结果。

### P0-5：前端提供的 7 个数据集并未形成可用能力

对 7 个数据集、11 个区块的真实预检可通过，但执行结果包括：

- `balances`：FAILED，`RPC_UNAVAILABLE`，Group Adapter 不支持；
- `internal_transactions`：FAILED，DuckDB Parquet 写入失败；
- `logs`：出现 `DB_WRITE_FAILED`，虽有 155 rows 且标记 VALIDATED/PARTIAL_CERTIFIED；
- `nft_transfers`：FAILED，无可用 Provider；
- `token_metadata`：FAILED，无可用 Provider；
- `token_transfers`、`transactions`：local_hit 完成但均为 0 rows，无有效验证/认证信息。

影响：数据集复选框表达的是“可执行能力”，实际却允许用户创建必然失败或语义不完整的任务。

## 4. 重要缺陷与交互问题

### P1

1. 结果时间错误：原始 `block_time=1777212313000`（毫秒）被再次乘以 1000，显示为 `+058287-08-13 22:56`。
2. 任务状态筛选器没有过滤批次列表，而是影响下级地址请求；选择 CREATED 后仍显示其他状态任务，并可能隐藏任务明细。
3. 批次暂停动作在账本中已经记录 `PAUSED`，前端仍显示 RUNNING；单地址 `9b2f84c1-2905-4dbd-9574-29b0cf75de02` 处于 WAITING 时暂停按钮可用，点击并等待约 8 秒后仍为 WAITING，导致地址级“继续”无法进入可操作态。取消状态也存在明显延迟。
4. 预取候选底层任务为 PARTIAL、地址 FAILED，点击“升级交互”后仍变成 `INTERACTIVE`，并显示 `hit_rate=1`、节省延迟 43.8，成功指标与底层失败矛盾。
5. 结果过滤接口本身可返回 132 条且 `from_address` 全部精确匹配，但浏览器快速切换过滤条件时曾渲染旧的混合结果，存在异步响应覆盖/陈旧状态风险。
6. 选中终态 COMPLETED 任务后，摘要卡上的“暂停全部 / 继续全部 / 取消任务”三个按钮仍全部可用，没有按任务终态禁用；任务表格行内按钮与摘要卡的保护规则不一致。

### P2

1. XLSX 导出内容正确，但 13 列均使用默认窄列宽，哈希、地址、表头大面积截断，人工审计体验不合格。
2. 移动端 390px 视口下，预取页文档宽度 875px，结果页 411px，标签页与表格发生横向裁切。
3. 必填数据集提示为英文 `Please enter 数据`，中文产品体验不一致。
4. TIME 模式未在前端要求起止时间；提交后才由后端以 400 返回“必须提供 start_time 和 end_time”。

## 5. 已通过的功能

以下仅表示对应交互及其数据断言通过，不代表模块整体通过：

- 四个页签均可进入；网络、优先级、资源档位及 AUTO/TURBO/EMERGENCY 选择可用；紧急开关仅在 EMERGENCY 档位启用。
- 空地址、反向区块范围、固定地址空表单能阻止提交并显示提示。
- CSV 导入：4 条原始记录，识别 2 个有效地址、1 个重复、1 个无效；TXT 与 XLSX 导入统计符合解析规则。
- 预检可返回估算区块、规划器及安全阈值；预检后模板保存、应用、删除可用。
- 任务列表、分页、刷新、展开明细、任务比较可操作。
- 任务 `307e9f73-ab20-4222-8526-64eb74168472` 在 RUNNING 状态下通过前端完成 AUTO → TURBO 一键升档，后端持久化结果为 TURBO；单地址按需展开与“详情”抽屉可用。
- 地址级取消任务 `d8c80f8f-b24c-4a56-ae7a-0ecb65188fd2` 从 WAITING 通过前端按钮收敛为 CANCELED；地址级暂停失败，因此地址级继续无法形成有效生命周期闭环，按不通过记录。
- 结果无匹配时显示空态；交易哈希复制得到完整 66 字符哈希；调查台、地址画像和关系图跳转可用。
- XLSX 导出含 1135 条数据、13 列；事件键 `transaction_hash:log_index` 1135 条均唯一，关键字段无空值，区块范围为 94800000–94809988。
- 对交易 `0xd12fe7587f141255309842b61d0ca3fff564e91df2e94c0ab6f01ba9e7a17ee2` 的 BSC RPC 回执逐字段核验通过：区块、日志序号、合约、from、to、value 与 Parquet 完全一致。

## 6. 证据标识与测试残留

- 生命周期任务：`d2853681-6b94-446d-a839-2ab9f227d7e2`
- 七数据集任务：`d99393bf-372b-46c5-9ea3-d5e0ed3ed19e`
- 一键升档与地址详情任务：`307e9f73-ab20-4222-8526-64eb74168472`
- 地址级暂停失败任务：`9b2f84c1-2905-4dbd-9574-29b0cf75de02`
- 地址级取消通过任务：`d8c80f8f-b24c-4a56-ae7a-0ecb65188fd2`
- 预取候选：`be1928af-0c24-45fa-8440-c927d106f340`
- 预取底层批次：`65ba0be0-52d7-4c12-825e-e21c7f6accfb`
- 有效 1135 行结果版本：`46fe4c02...`、`35cf2d17...`
- 测试模板已删除。任务/结果/预取候选没有安全删除接口，因此保留为可审计证据，未直接删除运行目录文件。

## 7. 验证命令

```powershell
go test -timeout 240s ./internal/smartdownload/... ./internal/api -count=1
go vet ./internal/smartdownload/... ./internal/api
Set-Location frontend
npm run build
```

结果：Go 测试通过、Go vet 通过、前端构建通过；构建仍有约 2.13 MB 大 chunk 的既有警告。单元测试通过不改变上述真实前端与数据质量验收失败结论。

## 8. 上线门槛与修复顺序

1. 先修复批次/地址/数据集状态聚合，禁止 CANCELED/FAILED 子项生成 COMPLETED 父状态和成功认证。
2. 重做 local-hit 的完整性判断：必须核对范围覆盖、实际行数、落盘文件和内容哈希；空命中不得覆盖已有非空权威版本。
3. 明确定义结果版本策略：同一业务范围只选择一个权威版本；重复业务数据不得求和；“查看”必须按时间、质量和完整性选中正确版本。
4. 对未配置 Provider 或当前不可用的数据集禁用选择，并在预检阶段给出明确原因；修复 DuckDB 写入与 RPC 可用性后再开放。
5. 修复时间单位、状态筛选、预取成功指标、过滤请求竞态、移动端溢出和 Excel 可读性。
6. 修复后必须以本报告中的任务场景做完整回归，并增加以下硬断言：父子终态一致、取消不可反转、同范围行数一致、认证必须对应可读文件、重复版本业务行不重复累计、链上抽样完全匹配。

## 9. 修复回归（2026-08-11）

上述阻断项已完成修复并按“界面行为 + 状态语义 + 落盘证据 + 返回数据质量”重新验收，当前结论改为：**智能下载模块通过本轮修复范围验收**。

| 原问题 | 修复结果 | 验收证据 |
|---|---|---|
| 取消任务反转为 COMPLETED | 通过 | CANCELED 成为不可逆终态；完成与取消混合的批次聚合为 PARTIAL；新增状态回归测试 |
| 1135 行缓存被复用为 0 行 | 通过 | local-hit 必须同时满足认证覆盖、文件存在、大小、哈希和行数校验；复用后仍为 1135 行，且不新增重复结果版本 |
| 同一结果版本累计为 2270 行 | 通过 | 前端按业务范围选择单一权威版本，不再把多个版本行数求和；浏览器显示 1135 行 |
| “查看”进入旧空结果 | 通过 | 权威版本按数据质量优先、创建时间次序选择；查看与摘要使用同一版本 |
| 不可执行数据集仍可选择 | 通过 | 状态接口返回当前链和模式的 `available_datasets`；BSC/AUTO 仅开放 balances、internal_transactions、logs、token_transfers、transactions，token_metadata 与 nft_transfers 前端禁用；无 Provider 的预检直接 400 |
| 失败/部分结果被认证成功 | 通过 | 只有可验证的完整文件或经认证的真实空范围可以复用；部分验证不再授予 CERTIFIED；完整数据集使用 DATASET_CERTIFIED，批次仅在全部完成且覆盖率 100% 时 BATCH_CERTIFIED |
| WAITING 地址暂停无效 | 通过 | 地址暂停同步进入 PAUSED；终态批次的暂停、继续和取消按钮统一禁用 |
| 预取失败仍计入命中 | 通过 | 升级要求批次 COMPLETED 且每个必需数据集均 Full、Ratio>=1、CERTIFIED；旧 PARTIAL 误命中保留审计记录并标记 invalidated，统计恢复为 total=0、used=0、hit_rate=0 |
| 时间显示为公元 58287 年 | 通过 | 秒、毫秒、数字字符串和日期字符串统一识别；浏览器断言无异常年份 |
| 状态筛选与快速过滤竞态 | 通过 | 批次状态在批次层过滤；请求序列号阻止陈旧响应覆盖新条件 |
| XLSX 难以人工审计 | 通过 | 导出列宽按哈希、地址、时间和普通字段设置可读宽度 |
| 390px 移动端溢出 | 通过 | 四页签均为 390px 文档宽度，宽表改为容器内滚动，无页面级横向溢出 |

### 真实任务与数据质量回归

- `internal_transactions` 小范围真实任务 `560d65b7-d85b-4ed0-b863-08a916ee8982`：COMPLETED；链上范围确认为空，coverage=1 且通过空范围认证。
- 同范围复用任务 `c5b467ab-22fc-4717-a106-76a850e95831`：`local_full_hits=1`，无重复下载，地址进度为 100%。
- `logs` 真实重下任务 `309448ba-2207-40f5-9ed0-06257eefe622`：批次、地址、数据集均 COMPLETED；155 行；VALIDATED；coverage=1；DATASET_CERTIFIED；无错误。
- 预取旧问题批次 `65ba0be0-52d7-4c12-825e-e21c7f6accfb` 保持 PARTIAL。再次升级返回 409，`interactive_upgrades=0`；旧反馈记录未删除，已写入 `invalidated=true` 与原因。
- 历史上的空认证、重复版本和 DB_WRITE_FAILED 记录保留作为审计历史，但已被权威结果选择、覆盖查询和复用检查隔离，不再进入有效结果与覆盖统计。

### 前端逐功能回归

因当前环境没有 Browser 插件，本轮使用项目捆绑的 Playwright 执行真实浏览器回归。创建下载、任务中心、智能预取、结果数据四页签的表单、校验、筛选、分页、展开、详情、复制、导出入口、跳转和终态按钮均执行数据断言；结果为 PASS，页面错误和模块内控制台错误均为 0。抽检断言包括：

- CANCELED 筛选后仅显示 2 个取消批次；
- token_transfers 权威结果显示 1135 行而非 2270 行；
- TIME 模式缺少时间范围时前端阻止提交；
- 不可用数据集被禁用；终态摘要控制按钮被禁用；
- 桌面和 390px 移动端无智能下载模块页面级溢出。

截图：`smart-download-desktop-fixed.png`、`smart-download-mobile-fixed.png`，位于本次 Codex visualizations 目录。

### 最终验证

```powershell
go test ./internal/investigation/prefetch -count=1
go test ./internal/datawarehouse ./internal/smartdownload ./internal/rpcmanager ./internal/api -count=1
go vet ./...
Set-Location frontend
npm run build
.\run.ps1
```

以上命令全部通过，服务重启后 PID 为 2424，`/api/health` 返回 `status=ok`。能力查询返回 5 个实际可用数据集；历史误命中统计为 0。

边界说明：额外执行全量 `go test ./internal/...` 时，既有的真实网络压力用例 `internal/downloadengine/TestBatchCollect500KAddresses` 因外部 SQD HTTP/2 流长时间无进展而达到 10 分钟超时；本次所有受影响包均单独通过。前端构建仍只有既有约 2.13 MB chunk 警告。进入智能下载前出现的 `/api/v2/explorer/bsc/home` 503 属于既有 Explorer/warehouse 环境问题，不是本模块请求。

## 10. SQD / SQD Cloud 再次严格复测（2026-08-11 22:00 后）

本节是在上一节“通过修复范围验收”之后，用真实 SQD、SQD Cloud、前端创建任务和落盘 Parquet 再次执行的独立回归。验收标准仍为：接口成功不代表通过，必须同时满足任务范围、Provider 路由、覆盖、认证、实际行数、唯一性、哈希及最终可用性。**本次总体结论：不通过；上一节的模块整体通过结论不适用于 SQD / SQD Cloud 全链路。**

### P0：SQD 结果重复且部分覆盖仍被认证

- AUTO 批次 `ffc14613-e0f5-4d0e-804b-c1d71dcb217f` 请求 BSC `114455000-114459999` 的 `token_transfers`。
- SQD 三个 Parquet 文件均存在，文件大小与 checkpoint 一致，SHA256 全部匹配；物理共 6 行，但按 `transaction_hash + log_index` 只有 3 个唯一事件，重复 3 行，重复率 50%。重复来自一个全范围修复文件与两个子范围文件重叠。
- 数据集校验状态为 `PARTIAL`、coverage=0、block_coverage=0、missing/unknown 含全部请求范围，批次也是 `PARTIAL`；但数据集字段仍为 `DATASET_CERTIFIED`。这是认证 fail-open，不能进入复用或调查证据链。
- 进度统计同样重复计算：请求 5000 区块却记录 `blocks_current=10000`、`blocks_total=15000`。

### P0：SQD Cloud 表面健康但真实任务不可执行

- Cloud runtime 为 `state=ABSENT`，`sqd list` 无托管 Worker，Cloud jobs 列表为空；同时 Provider health 把 `sqd_cloud` 标为 `HEALTHY / available=true`，状态语义矛盾。
- 真实 EMERGENCY 批次 `7aa98f25-9e98-4e91-932c-d8773a89aa40` 请求 1,000,001 区块（`94000000-95000000`）。预检预计 2 个 Cloud jobs，计划初始分配了 14 个 Cloud ranges，但运行中没有创建任何 Cloud Job，也没有部署 Worker。
- 任务最终 `PARTIAL`、数据集 `FAILED`、0 行、coverage=0；共出现 65 个失败范围。进度 `total_blocks=5,000,005`，是实际请求范围的 5 倍，说明重分片/修复范围被重复累计。
- 小范围 EMERGENCY 批次 `8e1b6fbe-e468-4329-843c-536f0f3cd497` 预检预计 1 个 Cloud job，实际只运行 RPC range，Cloud 估算与真实路由不一致。
- 对不存在的 Cloud Job 调用取消接口返回 200 并写入 cancel marker，而不是拒绝未知 job；不能以该 200 作为取消成功证据。

### SQD Cloud Registry / Parquet 质量审计

- 手动 Cloud sync 耗时约 11.4 秒，返回 26 个 `skipped=true`，Registry 统计前后均为 28 entries、76 files、2,396,039 rows，未产生新可用数据。
- 对 Registry 28 个条目引用的全部文件做审计：登记 85 个文件，其中 7 个缺失；所有实际存在文件的字节数、SHA256、文件内行数均与登记值一致。
- 状态分布：19 个 `COMPLETED/INDEXED`、7 个 `COMPLETED/LOCAL_SYNCED`、1 个 `LOCAL_SYNC_FAILED`、1 个 `LOCAL_VALIDATION_FAILED`。
- 条目 `831013b2...` 请求 `114474000-114474500`，实际最大区块 `114475243`，存在 993 行越界数据，已正确被本地校验拒绝。
- 业务请求存在重复版本：同一无地址过滤的 `94800000-94810000` 范围有 10 个条目，行数在 1,135 与 626,415 之间分裂；另一个相同地址集合和范围有 3 个条目，行数分别为 7,351、7,042、7,217。
- Registry 累计报告 2,396,039 行，而合并仓库按 `transaction_hash + log_index` 的实际唯一行数为 1,122,140。累计统计比可用唯一数据多 1,273,899 行，不能直接作为下载量或调查数据量。

### 前端再次回归

- 环境未安装 Browser 插件，按前端测试技能回退到本地 Playwright；截图和 JSON 证据位于 Codex visualizations 目录，不写入前端仓库。
- 通过：四页签、不可用数据集禁用、空地址/反向区块校验、资源档位与模式联动、Emergency 开关、相关区间、强制重下、模板保存/删除、预检展示、刷新、终态按钮禁用、预取 Pin 必填校验、预取 PARTIAL 升级 409 且指标不变、权威结果不重复求和、真实 XLSX 导出、390px 页面无横向溢出。
- 失败：任务比较接口返回 400；结果筛选无匹配时返回 404 而非 200 空列表，并产生前端控制台错误。
- 截图人工复核确认：上述请求错误会在页面顶部留下红色全局错误条，文案为 `前端错误：[object Object]`，既没有用户可理解的原因，也不会在清空筛选后自动消失；桌面与 390px 移动端均可见。
- TXT 导入文件含 1 个有效地址、1 个重复和 1 个无效值，界面却报告无效 2，疑似把表头或额外行计入无效数。
- 前端创建的 1 区块任务地址层保存了 `114460000-114460000`，但批次预检和数据集计划变成从 0 到链头的全量任务，生成 463 个范围；说明相关区间/默认区间在地址层与下载计划之间语义分裂。
- 四个受控测试任务已请求取消；三个延迟约数十秒收敛，一个在约 4 分钟后仍为 RUNNING，只有重启服务才收敛为 CANCELED。取消接口的成功响应不能代表后台下载已停止。

### 本轮通过项与验证边界

- 智能预取 fail-closed 修复在新进程上通过真实负向测试：PARTIAL 底层批次升级返回 409，候选保持 FAILED，upgrade_count、interactive_upgrades 和 hit_rate 均不变。
- 前端生产构建通过，`go vet ./...` 通过，SQD/Cloud/调度/智能下载相关单元测试通过。
- 全量 `go test ./internal/... -count=1` 中，`internal/downloadengine` 真实大规模用例本次在 355.204 秒完成并通过；唯一失败为非智能下载包 `internal/intelligence/TestResumeExecutesRecoveredTasks` 的一次恢复竞态（期望 COMPLETED、实际瞬时 RUNNING，且 TempDir 清理冲突）。随后单独重跑该用例在 0.08 秒通过，按非确定性测试缺陷记录，不影响本报告对 SQD/Cloud 的失败判断。
- `/api/health` 在最终重启后返回 `status=ok`；这只证明服务存活，不代表 SQD Cloud 可执行。

### 新的修复门槛

1. Cloud `available/HEALTHY` 必须与 Worker/Job 可执行状态一致；ABSENT 且无法部署时预检必须阻止或明确降级，不能计划 Cloud ranges。
2. 修复 1 区块请求扩张为全链、1,000,001 区块累计为 5,000,005 的范围膨胀；任何重分片、修复、hedge 均不得重复计入请求覆盖和进度分母。
3. 数据集认证必须要求校验 `VALIDATED`、覆盖 100%、事件键无重复、文件可读且范围不越界；PARTIAL/FAILED 永远不得出现 `DATASET_CERTIFIED`。
4. 取消必须中断正在运行的 SQD/Cloud 子进程，并在有界时间内收敛到 CANCELED；返回体需反映真实终态或明确 `cancel_requested`。
5. Registry 指标改为权威、去重后的业务行数；重复请求版本和 LOCAL_SYNC/VALIDATION_FAILED 不得进入有效统计。
6. 结果无匹配返回 200 空结果；比较按钮只允许选择后端可比较批次，或在界面给出明确原因。

## 11. 缺陷修复后的 SQD Cloud 实链验收（2026-08-12）

### 修复结果

- Cloud runtime 的 `ABSENT` 不再伪装为健康；只有 `READY/BUSY/IDLE` 才可执行。具备 Deploy Key 与 R2 配置的 `ABSENT` 可进入受控部署，但健康状态仍明确为不可执行，直至 `sqd list` 对账确认 Worker。
- Worker 部署前后二次执行 `sqd list` 对账；Windows 上 `sqd deploy` 在 Worker 已远端部署但 CLI 不退出时，确认 Worker 存在后终止准确的 deploy 进程树，不再永久停在 `DEPLOYING`。
- Cloud Job 的地址参数使用实际钱包地址，不再误用 Token Contract；未知/终态 Job 取消、非法 Job/Chunk ID 与路径穿越全部拒绝。
- Cloud Job 状态从远端 manifest 与 `_SUCCESS` 对账，`/cloud/jobs` 不再长期显示假 `queued`；完成 Job 返回真实地址、区块范围、行数和终态。
- SQD Worker 同时按 Transfer `topic1/from` 与 `topic2/to` 过滤钱包，输出真实 `token_address`；本地 RPC 同步改为相同的钱包双向 Transfer 语义并按事件键去重。
- V3.3 加速器不再接管 Cloud-owned Range，且分组 Provider 在预览、挂载与领取时都必须通过链/模式能力门。Turbo RPC 进一步排除 `MISCONFIGURED` 与仍处于熔断期的节点，避免只因存在配置就预留不可执行 RPC lane。
- Validation、Merge 与最终认证全部 fail closed：只有 `VALIDATED + coverage=1 + block_coverage=1 + unique + 文件/哈希/范围通过 + 数仓写入对账成功` 才可产生 `DATASET_CERTIFIED`；PARTIAL、DB_WRITE_FAILED 与缺口任务保持 `PENDING`。
- Registry 只统计 `COMPLETED + INDEXED + 文件存在且尺寸匹配` 的权威版本；同业务范围只选一个版本，行数使用唯一事件数。历史真实 Registry 从原始 28 条、2,396,039 行纠正为初始权威 8 个业务范围、495,591 行、28 文件、17,698,395 字节。
- 前端修复了比较候选、400 详情、空结果 404 合法空态、`[object Object]`、TXT 精确统计、陈旧请求覆盖、时间单位、批次筛选、终态按钮、能力禁用、权威结果选择和移动端溢出。

### 真实 SQD Cloud 证据

受控批次 `36a32223-cb7b-4e5d-8c50-d80fabee3c17` 请求 BSC 地址 `0xf43ba0b50028b8873fd4d6daac4bb7c4d5523906`、`token_transfers`、区块 `114400000-115000000`。本次不是按 HTTP 状态判定，而是逐层核验：

- Cloud runtime 为 `IDLE/available=true`，原因是 `sqd list` 已确认 `bsc-emergency-worker/v2`；两个真实 Cloud Job 均为 `done`。
- Job `sd-88e6c4ac-8ebd-4aad-b131-8123750d9e07` 覆盖 `114400000-114449999`，返回 584 行；Job `sd-1be831b6-d0e2-4763-9fc8-1be4a8c61e34` 覆盖 `114450000-114499999`，返回 564 行。
- 两个本地 Parquet 文件分别为 23,541 与 22,077 字节；SHA256 为 `828875e5...85bcab` 与 `414dbdf1...d8d11`，和 Ledger `PART_COMMITTED` 完全一致。
- 物理共 1,148 行；`transaction_hash + log_index` 唯一键也是 1,148，重复 0。所有记录 `chain_id=56`、`source_provider=sqd_cloud`，目标地址全部出现在 `from_address` 或 `to_address`，交易哈希、Token/收发地址格式及必填字段均合格，区块全部落在请求范围内。
- 数据集报告 `downloaded_rows=validated_rows=unique_key_count=1148`，两个 Cloud Range 均 `COMPLETED/RANGE_CERTIFIED`。后续 500,001 区块因本机没有健康 RPC 保持缺口，因此批次正确为 `PARTIAL`、coverage `0.166666...`、认证 `PENDING`，Ledger 没有错误的 `DATASET_CERTIFIED`。
- Cloud sync 后权威 Registry 增至 9 个业务范围、496,709 行、32 文件、17,738,936 字节；同步客户端因远端枚举迟滞在约 5 分钟被停止等待，但服务保持健康且权威数据已经原子更新，故只把数据更新判定为通过，不把同步响应时延判定为通过。

第二个受控 Cloud 闭环批次 `7ab758cc-e3ef-4326-847a-b68db87bb581` 请求同一地址的 `114400000-114499999`：RPC 不可执行后第二个 Range 被安全转移到 Cloud，两个 Cloud Range 最终分别提交 584/564 行，Validation 为 `VALIDATED`、score=100、coverage=1、block_coverage=1、unique=1,148、duplicate=0。最终本机 ClickHouse `127.0.0.1:8123` 拒绝连接，因此状态严格停在 `DB_WRITE_FAILED/PENDING`，没有伪造 `DATASET_CERTIFIED`。取消操作 2 秒内使 batch/address/dataset 全部收敛为 `CANCELED`。

### 最终结论与环境边界

- SQD Cloud 的部署、Worker 对账、Job 提交、钱包过滤、远端完成、Parquet 下载、哈希、行数、唯一性、覆盖与取消链路已用真实数据通过。
- 在当前机器上不能宣称智能下载“所有成功路径全部通过”：RPC Manager 没有可执行的 BSC 节点，ClickHouse 8123 未运行，所以混合 Provider 大范围任务只能 PARTIAL，完整 Cloud 数据也无法完成最终数仓认证。这两项属于当前运行环境阻断，系统现在均按 fail-closed 暴露真实状态。
- Cloud sync 的远端枚举响应时延仍需在网络恢复后复测；本轮仅确认同步产生了正确的权威 Registry 变化。

### 最终自动验证

```powershell
go test ./internal/cloudruntime ./internal/smartdownload ./internal/datasetsync ./internal/downloadscheduler ./internal/investigation/prefetch -count=1
go test ./internal/rpcmanager ./internal/smartdownload -run "TestTurbo|TestV33" -count=1
go vet ./...
Set-Location frontend
npm run build
.\run.ps1
```

以上受影响包测试、全仓 vet、前端生产构建均通过；最终服务 PID `36256`，`/api/health` 返回 `status=ok`。前端构建仅保留既有约 2.14 MB chunk 警告。
