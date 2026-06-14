# E:\codex\etl 与 E:\codex\etl_exe SQLite / DuckDB 优化升级对比

生成时间：2026-06-07

## 目标

将 `E:\codex\etl_exe` 作为 `E:\codex\etl` 的离线升级参考，梳理可以回迁到 `E:\codex\etl` 的数据层和图谱计算优化。

本文件只覆盖 SQLite / DuckDB / 大数据导入 / 图谱查询相关能力，不纳入验证码、激活、授权、打包发布等功能。

## 结论

`E:\codex\etl` 当前资金流图主路径仍以“上传文件落盘 -> 每次请求重读文件 -> Go 内存过滤 -> Go 聚合建图/边详情”为主。这个结构在小数据下简单直接，但对百万级 Excel/CSV 会出现重复扫描、生成图慢、边详情慢、字段候选项慢、进度感知弱等问题。

`E:\codex\etl_exe` 已经形成两层存储/计算结构：

- SQLite 控制面：保存会话、导入文件、字段值、查询缓存等元数据。
- DuckDB 分析面：将导入后的流水数据建成分析表，字段候选、图谱聚合、边详情优先走 SQL 查询，失败时再回退文件扫描。

建议 `E:\codex\etl` 优先回迁 DuckDB 分析面，其次回迁 SQLite 控制面。不要一次性搬离线版全部代码，尤其不要迁移授权、启动器、验证码激活和离线打包逻辑。

## 项目现状对比

| 模块 | E:\codex\etl 当前状态 | E:\codex\etl_exe 当前状态 | 建议 |
| --- | --- | --- | --- |
| SQLite | 源码未发现业务 SQLite 控制面 | `backend/internal/storage/control/store.go`，使用 `modernc.org/sqlite`，保存 flow session、import files、field values、query cache | 回迁，作为元数据和缓存索引 |
| DuckDB | 源码未发现 DuckDB 分析面 | `backend/internal/analysis/duckdb/engine.go`，通过 `duckdb.exe` CLI 操作 `flow.duckdb` | 优先回迁，解决大数据建图/详情性能 |
| 导入会话 | `internal/api/handlers.go` 中 session 文件目录为核心 | `FlowSession` 增加 `AnalysisTable`，导入完成后异步/后台加载 DuckDB | 增加 `AnalysisTable`，保留原文件路径作为回退 |
| 字段候选项 | `extractColumnValues()` 遍历 session 文件 | `queryColumnValueOptionsFromDuckDB()` SQL 去重、搜索、限制数量 | 字段候选优先 DuckDB |
| 图谱生成 | `readSessionData()` + `applyFilters()` + `BuildFlowGraph()` | `buildFlowFromDuckDB()` + `duckDBGraphQuery.executeEdgesWithSummary()` SQL 聚合 | 图谱生成优先 DuckDB，失败回退原逻辑 |
| 边详情 | `queryEdgeRows()` 重读文件或读缓存 | `queryEdgeDetailFromDuckDB()` SQL 查询明细、总金额、总笔数 | 边详情优先 DuckDB |
| 健康检查 | 常规健康信息 | 返回 `control_plane` 和 `analysis_plane` 状态 | 回迁分析面状态展示，方便排查 |
| 大 Excel | 依赖 Go 读取/标准化 | DuckDB 可直接 `read_xlsx`，CSV 可直接 `read_csv`；失败时回退逐行加载 | 谨慎回迁，不要破坏现有导入兼容性 |

## E:\codex\etl 当前关键路径

当前资金流图相关代码主要在：

- `E:\codex\etl\internal\api\handlers.go`
- `E:\codex\etl\internal\etl\flow_graph.go`
- `E:\codex\etl\internal\etl\etl.go`
- `E:\codex\etl\frontend\src\features\flow\flowApi.ts`

主要流程：

1. `/api/flow/build` 进入 `HandleBuildImportedFlow`。
2. `readSessionData(sessionDir, mapping, dirMap)` 读取 session 文件并转成标准交易行。
3. `applyFilters(txns, payload)` 在 Go 内存中过滤。
4. `etl.BuildSummary(filteredTxns)` 计算汇总。
5. `etl.BuildFlowGraph(filteredTxns, maxEdges)` 聚合边和节点。
6. `/api/flow/values` 通过 `extractColumnValues()` 遍历文件提取候选值。
7. `/api/flow/edge-detail/imported` 通过 `queryEdgeRows()` 查询边详情。

这个路径的主要问题不是单个函数慢，而是多个接口都重复读取和标准化同一批大文件。

## E:\codex\etl_exe 可回迁能力

### 1. SQLite 控制面

参考文件：

- `E:\codex\etl_exe\backend\internal\storage\control\store.go`
- `E:\codex\etl_exe\backend\internal\app\routes.go`

核心能力：

- `flow_sessions`：保存 `session_id`、导入名称、总行数、节点数、边数、`analysis_table`、状态。
- `flow_import_files`：保存原始导入文件路径、文件名、大小。
- `flow_field_values`：保存字段候选值及出现次数。
- `flow_query_cache`：预留查询缓存。
- SQLite PRAGMA：`WAL`、`synchronous=NORMAL`、`foreign_keys=ON`、`busy_timeout=5000`。

迁移到 `E:\codex\etl` 时建议：

- 新增 `internal/storage/control/store.go`。
- `go.mod` 增加 `modernc.org/sqlite`。
- 在服务启动时初始化 `control.Open(dataDir)`。
- 健康检查返回 `control_plane`。
- 先只落库 session / analysis_table / import files，不急着启用 query cache。

### 2. DuckDB 分析面

参考文件：

- `E:\codex\etl_exe\backend\internal\analysis\duckdb\engine.go`
- `E:\codex\etl_exe\backend\internal\app\handlers_flow.go`
- `E:\codex\etl_exe\backend\internal\app\flow_stream.go`
- `E:\codex\etl_exe\backend\config\config.json`

核心能力：

- 配置项：
  - `analytics.duckdb_path`
  - `analytics.duckdb_database`
- 默认数据库路径：
  - `data/analysis/flow.duckdb`
- DuckDB CLI 模式：
  - `ExecSQL()`
  - `ExecSQLJSON()`
  - `CreateTableFromCSVFiles()`
  - `CreateTableFromXLSXFiles()`
  - `DropTable()`
  - `TableRowCount()`
- CSV 直接建表：
  - `read_csv(..., header=true, all_varchar=true, ignore_errors=true, strict_mode=false, null_padding=true)`
- Excel 直接建表：
  - `LOAD excel`
  - `read_xlsx(..., header=true, all_varchar=true, stop_at_empty=false)`

迁移到 `E:\codex\etl` 时建议：

- 新增 `internal/analysis/duckdb/engine.go`。
- 配置中新增 `AnalyticsConfig`，但不要引入离线授权配置。
- 将 `duckdb.exe` 放到项目运行目录的 `tools/duckdb/duckdb.exe`，或支持绝对路径。
- 健康检查返回 `analysis_plane`，包含可用性、版本、数据库路径。

### 3. 导入后加载 DuckDB

`E:\codex\etl_exe` 在导入完成后会把当前 session 的数据加载到 DuckDB，并把表名写到 `FlowSession.AnalysisTable`。

参考点：

- `FlowSession.AnalysisTable`
- `standardizeAndLoadToDuckDB()`
- `cleanupOldDuckDBTables()`
- `finalizeImportProgress()`

建议在 `E:\codex\etl` 中使用保守策略：

1. 保留现有上传/导入/session 文件目录逻辑。
2. 导入完成后尝试创建 DuckDB 分析表。
3. 成功则记录 `AnalysisTable`。
4. 失败则保留现有文件扫描路径，不影响用户继续使用。
5. 每个 session 使用唯一表名，例如 `flow_<sessionID清洗后>`。
6. 清理旧 session 时同步 `DROP TABLE`。

### 4. 字段候选项优化

当前 `E:\codex\etl`：

- `/api/flow/values` 调 `extractColumnValues()`。
- 每次打开筛选框都可能遍历 session 文件。

`E:\codex\etl_exe`：

- 如果 `AnalysisTable` 可用，走 `queryColumnValueOptionsFromDuckDB()`。
- 支持搜索、limit、显示列组合。
- 返回值可包含 `options`，例如卡号 + 户名展示。

建议迁移逻辑：

```text
if DuckDB 可用 && session.AnalysisTable != "":
    从 DuckDB 查询 distinct 候选项
else:
    走 extractColumnValues() 原逻辑
```

验收标准：

- 大文件字段下拉搜索不触发整文件扫描。
- 候选项可按搜索词实时缩小。
- 后端响应中标记 `duckdb: true` 便于调试。

### 5. 图谱生成优化

当前 `E:\codex\etl`：

- `readSessionData()` 全量读取。
- `applyFilters()` 内存过滤。
- `BuildFlowGraph()` 内存聚合。

`E:\codex\etl_exe`：

- `buildFlowFromDuckDB()` 构造 `duckDBGraphQuery`。
- SQL 中完成：
  - 方向归一化。
  - 来源/目标端点计算。
  - 金额 `TRY_CAST`。
  - 时间范围过滤。
  - 主体/卡号/户名/流水号/摘要/备注等筛选。
  - 边聚合。
  - 汇总统计。
- 失败时回退到文件读取。

建议迁移逻辑：

```text
HandleBuildImportedFlow:
    if DuckDB 可用 && session.AnalysisTable != "":
        result = buildFlowFromDuckDB(...)
        if result != nil:
            return result
    return 原 readSessionData + applyFilters + BuildFlowGraph
```

保留回退非常重要。这样可以先把性能路径上线，不会因为某个 Excel 格式或 SQL 表达式异常导致建图不可用。

### 6. 边详情优化

当前 `E:\codex\etl`：

- `queryEdgeRows()` 优先读缓存，否则遍历 session 文件。

`E:\codex\etl_exe`：

- `queryEdgeDetailFromDuckDB()` 根据同一套过滤条件查询 DuckDB。
- 返回：
  - 明细 rows。
  - `totalAmount`。
  - `totalRows`。
  - limit 截断信息。
- SQL 失败再回退文件扫描。

建议迁移逻辑：

```text
HandleImportedFlowEdgeDetail:
    if DuckDB 可用 && session.AnalysisTable != "":
        rows, totalAmount, totalRows = queryEdgeDetailFromDuckDB(...)
        if totalRows > 0 or rows != nil:
            return DuckDB 结果
    return 原 queryEdgeRows()
```

验收标准：

- 图上边金额/笔数与边详情总金额/总笔数一致。
- 筛选条件一致时，图谱聚合和详情穿透来自同一组原始行。
- 边详情支持百万级数据下快速返回前 N 条和总计。

## 不建议回迁的内容

本轮升级不建议迁移：

- 验证码、激活、授权状态、license middleware。
- 离线打包脚本、`pythonw` 启动器、独立激活工具。
- 已删除的历史数据导入 UI。
- 与 DuckDB/SQLite 无关的前端样式重构。
- `E:\codex\etl_exe` 当前仍在调试中的穿透/主体详情 UI 改动，除非单独确认稳定。

## 推荐实施阶段

### 阶段 1：接入基础设施

改动：

- 新增 `internal/analysis/duckdb`。
- 新增 `internal/storage/control`。
- 配置新增 `analytics`。
- 健康检查展示 `control_plane` 和 `analysis_plane`。

验收：

- 不导入数据也能启动。
- 缺少 `duckdb.exe` 时服务不崩溃，只提示分析面不可用。
- 原有上传、建图、边详情仍可用。

### 阶段 2：导入后建 DuckDB 表

改动：

- `FlowSession` 增加 `AnalysisTable`。
- 导入成功后尝试加载 DuckDB。
- SQLite 记录 session 和分析表名。
- 旧 session 清理时删除 DuckDB 表。

验收：

- CSV 和 Excel 均能导入。
- DuckDB 表行数与导入行数一致。
- DuckDB 失败时原功能不受影响。

### 阶段 3：字段候选项走 DuckDB

改动：

- `/api/flow/values` 优先使用 DuckDB distinct 查询。
- 支持搜索和 limit。
- 保留 `extractColumnValues()` 回退。

验收：

- 大文件筛选框打开不再长时间卡住。
- 搜索卡号、户名、流水号、摘要、备注能快速返回。

### 阶段 4：图谱生成走 DuckDB

改动：

- 回迁 `duckDBGraphQuery`。
- `/api/flow/build` 优先 SQL 聚合。
- 保留 `readSessionData()` 回退。

验收：

- 同一数据、同一筛选条件下，DuckDB 图谱与原 Go 内存图谱节点/边/金额/笔数一致。
- 百万级数据建图不再重新读取 Excel 全量文件。

### 阶段 5：边详情走 DuckDB

改动：

- `/api/flow/edge-detail/imported` 优先 SQL 查询。
- 图谱边与详情共用同一套 `whereClause` / filter 解析。

验收：

- 点击线条能返回明细。
- 明细总金额、总笔数与边标签一致。
- 筛选卡号/户名/流水号/摘要/备注后，图和详情一致。

## 风险点

1. 字段映射必须统一

`E:\codex\etl` 当前有自己的 `flowColumnMapping` 和 `parser.NormalizeHeader()` 逻辑；`E:\codex\etl_exe` 使用 `flow.FieldMapping`。迁移时不要简单复制函数名，需要先统一映射结构，否则会出现“图和数据对不上”。

2. 方向归一化必须一致

Go 侧 `NormalizeDirection`、前端方向筛选、DuckDB SQL 中的 `CASE WHEN` 必须保持一致。建议把方向别名规则作为单一数据源，然后 SQL 查询从同一份规则生成。

3. 卡号和户名一对多

筛选逻辑应明确：

- 选择卡号：以卡号为准。
- 选择户名：匹配该户名包含的所有卡号。
- 同时选择卡号和户名：以卡号为准。

DuckDB 查询里要把这个规则写进 where 条件，否则生成图会和筛选框不一致。

4. Excel 直读不要一次性替换原导入

DuckDB `read_xlsx` 很适合大 Excel，但不同 Excel 格式、合并单元格、空行、非首行表头可能失败。建议先作为优先路径，失败回退原 Go 读取路径。

5. SQL 注入和列名转义

所有列名必须走 identifier quote，所有用户值必须走 string quote。`E:\codex\etl_exe` 已有 `quoteDuckDBString()`、列名 quote 相关实现，迁移时要保留。

## 代码迁移清单

优先迁移：

- `E:\codex\etl_exe\backend\internal\analysis\duckdb\engine.go`
- `E:\codex\etl_exe\backend\internal\storage\control\store.go`
- `E:\codex\etl_exe\backend\internal\config\config.go` 中 `AnalyticsConfig` 部分
- `E:\codex\etl_exe\backend\internal\app\handlers_flow.go` 中 DuckDB 导入、字段值、边详情相关函数
- `E:\codex\etl_exe\backend\internal\app\flow_stream.go` 中 `duckDBGraphQuery` 和 `buildFlowFromDuckDB`

谨慎迁移：

- `runAsyncImport()` 和进度逻辑，可以参考但需适配 `E:\codex\etl` 当前上传/session 结构。
- 前端 `flowApi.ts` 可只增加调试字段，不需要大改 UI。

不要迁移：

- license 相关目录和接口。
- `start.bat`、`stop.bat`、`launch_etl.pyw`。
- 离线发布打包脚本。

## 最小可行升级方案

如果要最快验证收益，建议只做以下最小闭环：

1. `E:\codex\etl` 新增 DuckDB Engine。
2. 导入完成后，把原始 CSV/XLSX 建成 DuckDB 表。
3. `/api/flow/build` 对有 `AnalysisTable` 的 session 优先走 DuckDB SQL 聚合。
4. `/api/flow/edge-detail/imported` 对同一 session 优先走 DuckDB SQL 明细。
5. 所有 DuckDB 失败场景回退原逻辑。

这个闭环能直接解决“建图慢、线条详情慢、重复读大文件”的核心问题，SQLite 控制面可以在第二轮补齐。

## 建议验证数据

使用同一批真实流水分别在原逻辑和 DuckDB 逻辑下验证：

- 导入总行数。
- 筛选后总行数。
- 汇总总金额。
- 流入金额 / 流出金额。
- 节点数。
- 边数。
- 每条边金额、笔数。
- 点击边后的明细总金额、总笔数。
- 典型筛选：
  - 交易方卡号。
  - 交易方户名。
  - 交易对手卡号。
  - 交易对手户名。
  - 交易流水号。
  - 摘要说明模糊/精确。
  - 备注模糊/精确。
  - 时间范围。
  - 进出标志。

验收口径：任一边、任一节点、任一金额都必须能追溯到原始行。
