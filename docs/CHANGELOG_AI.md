### 2026-05-24 22:44

#### 本次任务
- 修复大量数据导入后 Flow 生成图卡顿、统计异常、主体筛选后出现孤立账号且没有连线的问题。
- 重点修复审计场景：选择一个交易方账号、收付标志为“出”、不选择对手信息时，应统计并展示该账号所有匹配的流出交易对手关系。

#### 新增功能
- `/api/flow/build` 支持可选 `max_edges`，前端在有交易方/对手筛选的审计构图场景请求 5000 条关系上限，后端也会对主动筛选场景使用 5000 的审计上限。
- Flow graph meta 新增 `rendered_edges`、`rendered_nodes`，用于区分全量聚合规模和当前实际渲染规模。

#### 修改文件
- `internal/etl/flow_graph.go`
- `internal/etl/etl_test.go`
- `internal/api/handlers.go`
- `internal/api/handlers_test.go`
- `frontend/src/features/flow/useFlowGraph.ts`
- `frontend/src/features/flow/useFlowPanelState.ts`
- `frontend/src/features/flow/useFlowFilters.ts`
- `frontend/src/features/flow/FlowGraphFilters.tsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增或删除接口路径。
- `/api/flow/build` 新增可选请求字段 `max_edges`。
- `/api/flow/build` 响应 `meta` 新增 `rendered_edges`、`rendered_nodes`。
- `meta.total_nodes` 修正为未截断聚合图的节点总数，不再使用截断后边集合的节点数。

#### 数据库变化
- 无。

#### 前端变化
- 新图层生成/替换后会清空旧的主体筛选、金额筛选、路径追踪和选中关系，避免旧图状态污染新图。
- 金额滑块按当前图最大金额钳制显示和过滤，避免旧的大额阈值把新图所有边过滤掉。
- 金额/时间/渲染过滤生效时，画布只保留仍有关联边的节点，不再显示无连线的孤立账号。
- 有交易方或对手筛选时，构图 payload 发送 `max_edges: 5000`；无主体筛选的总览构图发送 `max_edges: 600`。

#### 验证结果
- `go test ./internal/...` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有 chunk size warning。
- `go vet ./internal/...` 通过。
- `go build -o "$env:TEMP\etl-server-check.exe" .\cmd\server\` 通过。
- 已重建 `bin\etl-server.exe` 并重启 8000 服务，`http://127.0.0.1:8000/api/health` 返回 `ok`。
- 已扫描本次 touched Flow/后端文件和 `frontend/dist/assets`，未发现 U+FFFD 替换字符。

#### 未完成/待确认
- 本次未用用户的 520k 行原始数据做浏览器端复现。
- 无筛选的大图总览仍保留 600 条最高金额聚合关系的渲染上限；审计明细应通过交易方/对手筛选进入 5000 上限。
- 当前可测试地址：`http://127.0.0.1:8000`；验证时后端 PID 为 `37172`。

# CHANGELOG_AI.md

### 2026-05-24 22:29

#### 本次任务
- 修正数据库导入弹窗中对象分类的位置：对象分类应在右侧对象区，不应挂在左侧模式节点下面。

#### 新增功能
- 无，本次为布局修正。

#### 修改文件
- `frontend/src/features/flow/DBImportModal.tsx`
- `frontend/src/features/flow/db-import.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- 左侧树改为连接 -> 数据库 -> 模式 -> 表，不再在模式下显示“表/视图/实体化视图/函数/查询/备份”分类节点。
- 右侧“对象”页新增对象分类按钮：表、视图、实体化视图、函数、查询、备份。
- 表对象列表保留在右侧，双击表仍会打开表数据页。

#### 验证结果
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有 chunk size warning。
- 已搜索 `frontend/src/features/flow/DBImportModal.tsx` 和 `frontend/src/features/flow/db-import.css`，确认左侧 `tables:` 分类节点已移除。
- 已扫描 `frontend/src/features/flow/DBImportModal.tsx`、`frontend/src/features/flow/db-import.css` 和 `frontend/dist/assets`，未发现 U+FFFD 替换字符。

#### 未完成/待确认
- 视图、实体化视图、函数、查询、备份分类当前仍为禁用展示项，待后端支持对应元数据接口后可启用。

### 2026-05-24 22:19

#### 本次任务
- 调整数据库导入弹窗的连接测试提示、树形结构和整体布局，使其更接近用户提供的数据库客户端截图。

#### 新增功能
- “测试连接”成功或失败时显示通知框，成功展示连接目标，失败展示错误原因。
- 新增连接 -> 数据库 -> 模式 -> 对象分组 -> 表的树形导航结构。
- 新增“对象”主视图，右侧以“名 / 行 / 注释”表格展示当前模式下的表。

#### 修改文件
- `frontend/src/features/flow/DBImportModal.tsx`
- `frontend/src/features/flow/db-import.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- 数据库导入弹窗左侧从平铺列表改为 Ant Design Tree。
- 右侧新增类似数据库客户端的对象工具栏：打开表、设计表、新建表、删除表、导入向导、导出向导。
- 打开表后切换到表数据页；选择模式后默认展示对象页。
- 新建表、删除表、导出向导当前仅作为布局占位且禁用，未新增 DDL 或导出接口。

#### 验证结果
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有 chunk size warning。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- 已扫描 `frontend/src/features/flow/DBImportModal.tsx`、`frontend/src/features/flow/db-import.css` 和 `frontend/dist/assets`，未发现 U+FFFD 替换字符。

#### 未完成/待确认
- 当前表列表接口只返回名称和类型，右侧“行 / 注释”暂为空占位；如需真实行数/注释，需要扩展后端元数据接口。

### 2026-05-24 21:46

#### 本次任务
- 启动项目，供用户测试当前数据库导入版本。

#### 新增功能
- 无，本次仅启动/重启服务。

#### 修改文件
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- 无。

#### 验证结果
- 已检查 8000 端口原有进程及命令行。
- 已停止旧的 `E:\codex\etl\bin\etl-server.exe` 进程。
- 已从 `E:\codex\etl` 启动当前 `bin\etl-server.exe`。
- `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000/api/db/connections` 返回 JSON，确认数据库导入 API 已在 8000 可用。
- `http://127.0.0.1:8000` 返回 HTTP 200，并加载当前前端构建资源。

#### 未完成/待确认
- 无。当前可测试地址为 `http://127.0.0.1:8000`。

### 2026-05-24 20:58

#### 本次任务
- 使用用户提供的本机 MySQL 连接做数据库导入功能真实接口测试。

#### 新增功能
- 无，本次仅测试验证。

#### 修改文件
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 临时创建 MySQL database `codex_mysql_import_test` 和表 `flow_txn`。
- 测试结束后已删除临时 database。

#### 前端变化
- 无。

#### 验证结果
- MySQL 8.0.39 连接成功。
- `/api/db/connections` 连接保存、列表读取、密码隐藏、删除通过。
- `/api/db/connections/:id/test` 通过。
- 数据库、schema、表、字段元数据读取通过。
- `/api/db/preview` 分页预览通过，返回 2 行并标记截断。
- `/api/db/search` 搜索通过，返回 1 行。
- `/api/db/query` SELECT 查询通过，非 SELECT 查询按预期被拦截。
- `/api/db/table/insert`、`/api/db/table/update`、`/api/db/table/delete` 均通过，各影响 1 行。
- `/api/db/mappings/auto` 自动映射通过，必填字段均已匹配。
- `/api/db/mappings/confirm` 映射保存通过。
- `/api/db/import/tasks` 创建和 `/api/db/import/tasks/:id/start` 执行通过，导入 3 行成功、0 行失败。
- `/api/flow/build` 基于数据库导入 session 生成流向图通过，返回 3 个节点、3 条边。

#### 未完成/待确认
- 无。临时 MySQL database、临时 flow session、测试连接配置和临时 8001 服务均已清理。
- 8000 端口未重启；本次测试使用临时 `PORT=8001` 当前二进制完成。

### 2026-05-24 18:55

#### 本次任务
- 使用用户提供的本机 PostgreSQL 连接做数据库导入功能真实接口测试。

#### 新增功能
- 无，本次仅测试验证。

#### 修改文件
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 临时创建 PostgreSQL schema `codex_dbimport_test` 和表 `flow_txn`。
- 测试结束后已删除临时 schema。

#### 前端变化
- 无。

#### 验证结果
- PostgreSQL 17 连接成功。
- `/api/db/connections` 连接保存、列表读取、密码隐藏、删除通过。
- `/api/db/connections/:id/test` 通过。
- 数据库、schema、表、字段元数据读取通过。
- `/api/db/preview` 分页预览通过，返回 2 行并标记截断。
- `/api/db/search` 搜索通过，返回 1 行。
- `/api/db/query` SELECT 查询通过，非 SELECT 查询按预期被拦截。
- `/api/db/table/insert`、`/api/db/table/update`、`/api/db/table/delete` 均通过，各影响 1 行。
- `/api/db/mappings/auto` 自动映射通过，必填字段均已匹配。
- `/api/db/mappings/confirm` 映射保存通过。
- `/api/db/import/tasks` 创建和 `/api/db/import/tasks/:id/start` 执行通过，导入 3 行成功、0 行失败。
- `/api/flow/build` 基于数据库导入 session 生成流向图通过，返回 3 个节点、3 条边。

#### 未完成/待确认
- 本次建表使用 ASCII 字段名，因为 PowerShell 调用 `psql -c` 创建中文标识符遇到客户端编码问题；如需验证中文数据库字段名，应使用 UTF-8 配置正确的 SQL 客户端或从应用 UI 创建/选择已有中文字段表继续测试。
- 8000 端口运行的是较旧二进制，未重启；本次测试使用临时 `PORT=8001` 当前二进制完成，测试后已停止。

### 2026-05-24 18:28

#### 本次任务
- 按数据库导入功能改造需求完成剩余后端、前端、测试和交付文档。

#### 新增功能
- 新增数据库导入入口，支持 MySQL/PostgreSQL 连接配置、测试、浏览、预览、搜索、查询、字段映射确认、映射保存和导入流向图。
- 新增安全表编辑接口和前端编辑页：新增、修改、删除均走参数化接口，修改/删除必须提供主键或唯一条件。
- 新增数据库导入任务、错误记录和报告接口。
- 新增本地 AES-GCM 加密配置存储，密码仅在用户勾选保存密码时写入加密文件。

#### 修改文件
- `.gitignore`
- `go.mod`
- `go.sum`
- `internal/api/handlers.go`
- `internal/api/db_handlers.go`
- `internal/dbimport/types.go`
- `internal/dbimport/store.go`
- `internal/dbimport/service.go`
- `internal/dbimport/service_test.go`
- `frontend/src/App.tsx`
- `frontend/src/hooks/useFlowOperations.ts`
- `frontend/src/features/flow/FlowPanel.tsx`
- `frontend/src/features/flow/FlowSourceModal.tsx`
- `frontend/src/features/flow/DBImportModal.tsx`
- `frontend/src/features/flow/dbImportApi.ts`
- `frontend/src/features/flow/db-import.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`
- `数据库导入功能改造完成报告.md`

#### 接口变化
- 新增 `/api/db/connections` 连接管理接口。
- 新增 `/api/db/connections/:id/databases|schemas|tables|columns|indexes` 元数据接口。
- 新增 `/api/db/preview`、`/api/db/search`、`/api/db/query`、`/api/db/query/cancel`。
- 新增 `/api/db/table/insert`、`/api/db/table/update`、`/api/db/table/delete`。
- 新增 `/api/db/mappings`、`/api/db/mappings/auto`、`/api/db/mappings/confirm`。
- 新增 `/api/db/import/tasks` 及任务 start/cancel/errors/report 接口。
- 未修改既有 `/api/flow/*`、`/api/process` 路径。

#### 数据库变化
- 无外部数据库依赖。
- 新增本地加密配置文件目录 `backend/data/db_import/`，已加入 `.gitignore`。

#### 前端变化
- 数据来源弹窗删除可见的“清洗的文件”入口。
- 新增“数据库导入”卡片和数据库导入弹窗。
- 数据库弹窗包含连接列表、数据库/schema/表浏览、分页预览、表结构、SELECT 查询、数据编辑、字段映射、导入任务页。

#### 验证结果
- `go test ./internal/...` 通过。
- `go vet ./internal/...` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有 chunk size warning。
- `go build -o bin\etl-server.exe .\cmd\server\` 通过。
- 临时 `PORT=8001` 启动二进制，`/api/health` 和 `/api/db/connections` 通过。

#### 未完成/待确认
- 未连接真实 MySQL/PostgreSQL 实例做集成测试，需用户提供可用数据库账号后验证连接、元数据、预览和导入。
- 第一版导入 UI 等待 start 请求完成；后端已按页保存进度并支持 cancel 状态，后续可改成前端轮询后台任务。

### 2026-05-24 16:17

#### 本次任务
- 检查并修复项目重构后的 Flow 图相关 bug。

#### 新增功能
- 新增后端 API 单元测试，覆盖 Flow 筛选和方向归一化。

#### 修改文件
- `internal/api/handlers.go`
- `internal/api/handlers_test.go`
- `frontend/src/hooks/useFlowOperations.ts`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无接口路径变化。
- `/api/flow/history` 扩展返回 `job_id`、`name`、`size`、`updated_at`、`status`。
- `/api/flow/history/:job_id` 改为返回可恢复导入数据集的字段：`session_id`、`job_id`、`name`、`rows`、`columns`、`files`、`sample`、`signature`、`mapping_rule`。

#### 数据库变化
- 无。

#### 前端变化
- 历史数据加载不再假设后端一定返回 `flow_graph`，可恢复历史导入数据并继续生成图。
- 智能分析在占位 API 不返回 `flow_graph` 时只展示报告，不再触发空 graph 崩溃。
- 生成图继续兼容顶层 `nodes/edges/meta` 和嵌套 `flow_graph` 两种响应形状。

#### 后端变化
- 历史列表/详情与前端 `HistoryItem`、`ImportedDataset` 数据形状对齐。
- Flow 构图筛选支持目标字段筛选、方向筛选、开始/结束日期筛选。
- 构图和未知方向检查支持内置方向归一化与持久化方向别名。

#### 验证结果
- `go vet ./internal/...` 通过。
- `go test -count=1 -timeout 60s ./internal/...` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过。
- `go build -o bin\etl-server.exe .\cmd\server\` 通过。
- 临时 `PORT=8001` 启动新二进制，`/api/health`、`/api/flow/history`、`/api/flow/history/70027426-b61` 均通过。

#### 未完成/待确认
- 8000 端口已有 `E:\codex\etl\bin\etl-server.exe` 正在运行且健康检查正常，本次未重启该进程。

### 2026-05-24 16:01

#### 本次任务
- 修复点击“生成图”后前端报错 `Cannot read properties of undefined (reading 'meta')`。

#### 新增功能
- 新增前端 Flow 图响应归一化逻辑，兼容 `/api/flow/build` 的顶层 `nodes/edges/meta` 响应和嵌套 `flow_graph` 响应。

#### 修改文件
- `frontend/src/hooks/useFlowOperations.ts`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增或变更接口。
- 未修改后端 `/api/flow/build` 响应，仅增强前端兼容读取。

#### 数据库变化
- 无。

#### 前端变化
- 生成图流程改为使用归一化后的 graph 对象读取 `meta` 并渲染节点/边。
- 异常或空图 payload 会进入已有空数据提示，不再直接抛 JavaScript 运行时错误。

#### 验证结果
- `cd E:\codex\etl\frontend; npm run build` 通过。
- `cd E:\codex\etl; go test ./internal/...` 通过。

#### 未完成/待确认
- 无。

用于记录 AI/Codex/Hermes 每次完成的功能变更。

## 记录格式

### YYYY-MM-DD HH:mm

#### 本次任务
-

#### 新增功能
-

#### 修改文件
-

#### 接口变化
-

#### 数据库变化
-

#### 前端变化
-

#### 验证结果
-

#### 未完成/待确认
-
