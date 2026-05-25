## 2026-05-25 15:13 Handoff

### Task
- 将日期筛选框和日期选择弹层改为中文显示，避免 Ant Design 日期控件出现英文文案。

### Changes
- 更新 `frontend/src/App.tsx`。
- 更新 `frontend/src/features/flow/EdgeStylePanel.tsx`。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- 全局 Ant Design `ConfigProvider` 现在使用中文 locale。
- 全局 dayjs locale 设置为 `zh-cn`，日期面板的月份、星期、按钮等文案按中文显示。
- 线条样式面板中的日期范围框补充中文占位符 `开始时间` / `结束时间`。

### API Changes
- 无。

### Database Changes
- 无。

### Frontend Changes
- `App.tsx` 引入 `antd/locale/zh_CN`、`dayjs` 和 `dayjs/locale/zh-cn`，并在 `ConfigProvider` 上设置 `locale={zhCN}`。
- `EdgeStylePanel.tsx` 的 `DatePicker.RangePicker` 明确设置中文占位符。

### Verified Commands
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `rg -n "�" frontend\src\App.tsx frontend\src\features\flow\EdgeStylePanel.tsx frontend\dist\assets` 无匹配。
- `frontend/dist/index.html` 已引用当前构建产物 `assets/index-B2S0PUmd.js` 和 `assets/index-B-imr4oU.css`。
- `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-B2S0PUmd.js` 和 `assets/index-B-imr4oU.css`。

### Open Items
- 浏览器如果缓存旧资源，需要强制刷新后再查看日期控件。

### Notes
- 本次未新增依赖；`dayjs` 来自现有 Ant Design 依赖树。

## 2026-05-25 当前 Handoff

### Task
- 修复导入交易时间格式与后台标准格式不一致时，时间筛选和审计统计口径不一致的问题。
- 重新进行后端审计统计校验，要求所有筛选条件同时带入后，统计、建图、线条明细一致。
- 修复点击资金流向图线条后，明细弹窗的笔数、金额和真实流向与 Excel 手工统计不一致的问题。
- 修复点击线条后明细数据为空的问题：当实体名来自备用列（如 交易账号 而非 交易户名）时，后端 queryEdgeRows 只匹配主列导致无结果。

### Changes
- 更新 internal/api/handlers.go。
- 更新 internal/api/handlers_test.go。
- 更新 frontend/src/features/flow/flowApi.ts。
- 更新 frontend/src/features/flow/flowTypes.ts。
- 更新 frontend/src/features/flow/useFlowFilters.ts。
- 更新 frontend/src/hooks/useFlowOperations.ts。
- 更新 internal/parser/parser.go。
- 更新 internal/parser/parser_test.go。
- 新增 lowColumnMapping 结构体和 lowColumnMappingFromPayload 函数，统一管理列映射提取。
- queryEdgeRows 匹配逻辑改为遍历所有源端/目标端备用列（source_column, source_account_column, source_name_column, source_id_column），任一匹配即成功。
- HandleImportedFlowEdgeDetail 的 payload 结构体新增 8 个备用列字段，queryEdgeRows 参数结构体对应新增。
- HandleBuildImportedFlow 重构为使用 lowColumnMappingFromPayload。
- matchesDateRange 内部日期过滤逻辑增加了 
ormalizeFilterBoundary 精确时间边界处理。

### New Functionality
- 导入流向图数据时，映射后的 `交易时间` 会先统一归一化为 `YYYY-MM-DD HH:mm:ss`，再参与预览、筛选、统计、建图和明细匹配。
- `parser.NormalizeDatetime` 扩展支持 Excel 序列日期、`YYYYMMDD/YYMMDDHHMMSS` 类紧凑数字、单双位年月日、中文年月日时分秒、点号/斜杠日期、毫秒、RFC3339 时区、Unix 秒/毫秒等常见交易时间格式。
- 任一筛选条件生效时都会使用 5000 条审计关系上限，包括交易方、对手方、双方标签、明细字段、方向、开始时间、结束时间，不再只有交易方/对手方/明细字段触发审计上限。
- 新增后端审计测试：混合时间格式数据 + 交易方筛选 + 对手方筛选 + 双方标签 + 流水号 + 摘要 + 备注 + 方向 + 起止时间全部同时带入后，核对筛选统计、建图边、线条明细的笔数和金额一致。
- 边缘明细数据现在能正确匹配通过备用列（交易账号/交易户名/对方身份证号等）解析的实体名称。
- 边缘明细现在按建图同一套逻辑先生成标准交易行、归一化收付标志、应用当前筛选条件，再按计算出的真实资金流向匹配被点击的边。
- 对 `收付标志=进` 的原始流水，明细查询会按“对手 -> 本方”匹配线条，不再误按“本方 -> 对手”匹配。
- 明细接口现在会应用当前图层的源/目标筛选、标签筛选、明细字段筛选、方向筛选和时间范围。
- 明细返回行新增 `流向源`、`流向目标` 字段，便于核对原始行方向与图上线条方向。
- 明细总笔数和总金额在服务端按全部匹配行统计，再按 limit 截断返回行，不再因为默认 10000 行限制导致合计偏小。

### API Changes
- 无新增/变更端点路径。
- /api/flow/edge-detail/imported 请求体新增可选字段：source_account_column, source_name_column, source_id_column, source_label_column, 	arget_card_column, 	arget_name_column, 	arget_id_column, 	arget_label_column。
- /api/flow/edge-detail/imported 继续兼容原请求体，并补充使用以下已有/新增可选字段：direction_column、source_filters、target_filters、detail_filters、source_label_values、target_label_values、directions、start_date、end_date。
- /api/flow/edge-detail/imported 响应 rows 中新增 `流向源`、`流向目标` 两列。
- /api/flow/build 的请求/响应路径不变；后端现在会对所有活跃筛选条件使用审计上限并用归一化后的交易时间统计。

### Database Changes
- 无。

### Frontend Changes
- 图层的边明细上下文会把源/目标标签筛选值一并传给后端，确保点击线条后的明细口径与当前图一致。
- 前端构建图 payload 的 `max_edges` 判断改为任意筛选条件生效即请求 5000 条审计关系上限，覆盖标签、方向和时间筛选。

### Verified Commands
- go build -o bin\etl-server.exe .\cmd\server\ 通过
- go test ./internal/... — 全部 29 个测试通过
- cd frontend; npm run build — TypeScript + Vite 构建通过
- go test ./internal/api -run "TestQueryEdgeRowsMatchesDirectedGraphEndpointAndFilters|TestFlowFilterEndToEndAuditMatchesGraphAggregates" -count=1 -v 通过
- go test ./internal/api -run "TestFlowEdgeLimitUsesAuditLimitForAnyActiveFilter|TestFlowAuditAllFiltersAndMixedTimeFormatsStayConsistent" -count=1 -v 通过
- go test ./internal/parser -run TestNormalizeDatetime -count=1 -v 通过
- cd E:\codex\etl\frontend; npx tsc --noEmit 通过
- go vet ./internal/... 通过
- 已重启 E:\codex\etl\bin\etl-server.exe，http://127.0.0.1:8000/api/health 返回 {"status":"ok"}。
- http://127.0.0.1:8000 已引用当前构建产物 assets/index-CS-QR2Md.js 和 assets/index-B-imr4oU.css。

### Open Items
- 用户需要用实际 Excel 对照的那条线再次点击验证；浏览器如果缓存旧 JS，需要强制刷新。

### Notes
- 前端 POST /api/flow/edge-detail/imported 会发送 source_account_column 等备用列，但旧的 Go struct 缺少对应字段，JSON 反序列化静默丢弃了这些字段。
- 本次根因是建图会把 `进` 的原始行反向成真实资金流向，但旧的边明细查询只按原始本方列等于线条源、原始对手列等于线条目标匹配，且忽略当前筛选条件。
- 时间格式无法数学意义上覆盖所有可能输入；本次覆盖银行/Excel/CSV 常见格式，无法识别的极端自定义格式仍会原样保留并可能无法进入时间范围筛选。
- HandleFlowEdgeDetail (GET /api/flow/edge-detail, kind: "cleaned" 路径) 仍为占位实现，始终返回空行。

## 2026-05-25 00:01 Handoff

### Task
- Fixed graph image export (PNG/JPEG/WebP/SVG) to capture the full graph (all nodes/edges) instead of only the visible viewport area.

### Changes
- Updated rontend/src/features/flow/flowExport.ts.
- Added expandForFullCapture helper that computes the bounding box of all .react-flow__node elements before capture.
- captureCanvasRaster and captureCanvasSvg now call expandForFullCapture, then capture, then restore original container styles via inally.
- Also updated docs/AI_HANDOFF.md and docs/CHANGELOG_AI.md.

### New Functionality
- PNG, JPEG, WebP, and SVG single-format exports now render the entire graph canvas, not just the viewport.
- ZIP-exported .png and .svg images also use full-graph capture.
- No-op when there are zero nodes on canvas (falls back gracefully).

### API Changes
- None.

### Database Changes
- None.

### Frontend Changes
- New function expandForFullCapture(target) — temporarily expands the ReactFlow container to the full bounding box of all nodes, sets overflow: visible, repositions the viewport, and returns a estore() function plus ounds. The caller captures and then restores.

### Verified Commands
- cd E:\codex\etl\frontend; npm run build
- go test ./internal/...

### Open Items
- None.

### Notes
- The previous edit accidentally duplicated the file contents; this turn cleaned it to a single correct copy with expandForFullCapture.
- Vite still reports the existing large chunk warning; build succeeds.
## 2026-05-24 23:34 Handoff

### Task
- Added missing Flow field mapping entries and filter support for `交易流水号`、`摘要说明`、`备注`.
- Updated Flow time filtering to use Chinese placeholders and second-level datetime precision.
- Replaced the downloadable Flow template with the user-provided `D:\app\桌面\流向图数据模板.xlsx`.
- Performed an end-to-end backend audit for normalization, filtering, and graph aggregation consistency.

### Changes
- Updated `frontend/src/features/flow/flowTypes.ts`.
- Updated `frontend/src/features/flow/flowMapping.ts`.
- Updated `frontend/src/features/flow/FlowMappingModal.tsx`.
- Updated `frontend/src/features/flow/FlowFieldFilters.tsx`.
- Updated `frontend/src/features/flow/useFlowFilters.ts`.
- Updated `frontend/src/features/flow/FlowBuildControls.tsx`.
- Updated `frontend/src/features/flow/FlowPanel.tsx`.
- Updated `frontend/src/features/flow/FlowGraphWorkspace.tsx`.
- Updated `frontend/src/features/flow/FlowInspectorPanel.tsx`.
- Updated `frontend/src/features/flow/flowApi.ts`.
- Updated `frontend/src/hooks/useFlowOperations.ts`.
- Updated `internal/api/handlers.go`.
- Updated `internal/api/handlers_test.go`.
- Replaced `tmp/flow_template.xlsx` with the uploaded workbook.
- Updated `docs/AI_HANDOFF.md`.
- Updated `docs/CHANGELOG_AI.md`.

### New Functionality
- `字段映射 / 模板说明` now includes `交易流水号`、`摘要说明`、`备注`.
- The right-side Flow filter panel now exposes a `明细筛选字段` selector for those three fields only when the imported data has a resolved mapping for that field.
- `/api/flow/build` now reads mapped serial/summary/remark columns into normalized transaction rows and accepts `detail_filters`.
- Source/target label filters are now applied in backend filtering, matching the existing frontend label filter UI.
- Time filtering now supports full `YYYY-MM-DD HH:mm:ss` boundaries; date-only backend inputs still cover the whole selected day for end dates.

### API Changes
- No endpoint paths changed.
- `/api/flow/build` request payload supports optional `serial_column`, `summary_column`, `remark_column`, and `detail_filters`.
- `/api/flow/template` now returns the uploaded 15-column template with `交易流水号` between `对手标签` and `摘要说明`.

### Database Changes
- None.

### Frontend Changes
- Added detail field mapping rows and auto-mapping aliases for serial/summary/remark.
- Added detail-field value loading and multi-select filters.
- Changed time range placeholder text to `开始时间` / `结束时间`.
- Enabled date-time input with hour/minute/second display.

### Verified Commands
- `cd E:\codex\etl\frontend; npx tsc --noEmit`
- `go test ./internal/api -run TestFlowFilterEndToEndAuditMatchesGraphAggregates -count=1 -v`
- `cd E:\codex\etl\frontend; npm run build`
- `go test ./internal/...`
- `go vet ./internal/...`
- `go build -o "$env:TEMP\etl-server-check.exe" .\cmd\server\`
- `go build -o bin\etl-server.exe .\cmd\server\`
- Restarted `E:\codex\etl\bin\etl-server.exe` on port 8000 and verified `http://127.0.0.1:8000/api/health` returned `{"status":"ok"}`.
- Downloaded `http://127.0.0.1:8000/api/flow/template` and inspected the workbook header as `交易方户名, 交易方账户, 交易方身份证号, 交易方标签, 交易时间, 交易金额, 收付标志, 交易余额, 交易对手账卡号, 对手户名, 对手身份证号, 对手标签, 交易流水号, 摘要说明, 备注`.
- Verified `http://127.0.0.1:8000` references current assets `assets/index-Dg-VWM7A.js` and `assets/index-B-imr4oU.css`.

### Open Items
- Browser may need a hard refresh if it cached the previous JS bundle.

### Notes
- The new audit test generates multi-account, multi-counterparty, multi-direction, multi-time, multi-amount data, then directly exercises `readSessionData`, `applyFilters`, and `etl.BuildFlowGraph`.
- The audit checks filtered row counts, amount totals, edge counts/amounts, and node inflow/outflow amounts and counts.
- Vite still reports the existing large chunk warning; build succeeds.

## 2026-05-24 23:02 Handoff

### Task
- Fixed the database import layout still rendering vertically with the object panel below the tree.

### Changes
- Updated `frontend/src/styles/shared.css`.
- Updated `frontend/src/features/flow/DBImportModal.tsx`.
- Updated `frontend/src/features/flow/db-import.css`.
- Updated `docs/AI_HANDOFF.md`.
- Updated `docs/CHANGELOG_AI.md`.

### New Functionality
- None. This was a CSS layout bug fix.

### API Changes
- None.

### Database Changes
- None.

### Frontend Changes
- Removed a stale, incomplete `@media` block at the end of `frontend/src/styles/shared.css`.
- This restores `db-import.css` as top-level CSS instead of being accidentally nested under a media query.
- Database import now keeps the tree on the left and the object panel on the right on desktop widths.

### Verified Commands
- `cd E:\codex\etl\frontend; npm run build`
- Confirmed the built CSS contains top-level `.db-import-shell{...display:grid...}` after the media query closes.
- Scanned touched source files and `frontend/dist/assets` for U+FFFD replacement characters.
- Verified `http://127.0.0.1:8000/api/health` returned `{"status":"ok"}`.
- Verified `http://127.0.0.1:8000` references the current built assets: `index-B-imr4oU.css` and `index-DTwUX0_S.js`.

### Open Items
- Browser may need a hard refresh if an older hashed asset is cached.

### Notes
- Vite still reports the existing large chunk warning; build succeeds.

## 2026-05-24 22:44 Handoff

### Task
- Fixed Flow graph audit filtering problems reported after importing large datasets: graph generation felt slow, old filter state made the canvas show isolated strange accounts with no edges, and subject statistics could show 0 relationships even after selecting an account.

### Changes
- Updated `internal/etl/flow_graph.go`.
- Updated `internal/etl/etl_test.go`.
- Updated `internal/api/handlers.go`.
- Updated `internal/api/handlers_test.go`.
- Updated `frontend/src/features/flow/useFlowGraph.ts`.
- Updated `frontend/src/features/flow/useFlowPanelState.ts`.
- Updated `frontend/src/features/flow/useFlowFilters.ts`.
- Updated `frontend/src/features/flow/FlowGraphFilters.tsx`.
- Updated `docs/AI_HANDOFF.md`.
- Updated `docs/CHANGELOG_AI.md`.

### New Functionality
- Entity-filtered graph builds now request/allow up to 5000 rendered relationships, capped server-side, so audit cases such as "one account + direction=out + no counterparty filter" can include all matching counterpart relationships instead of being cut at the general 600-edge overview limit.
- Flow graph metadata now distinguishes total graph size from rendered graph size with `rendered_edges` and `rendered_nodes`.

### API Changes
- No endpoint paths changed.
- `/api/flow/build` now accepts optional `max_edges`.
- `/api/flow/build` keeps default overview limit at 600 edges, but active source/target filters use the 5000 audit cap unless a lower `max_edges` is supplied.
- `/api/flow/build` meta now includes `rendered_edges` and `rendered_nodes`; `total_nodes` now counts nodes from the untruncated aggregated graph instead of only the rendered subset.

### Database Changes
- None.

### Frontend Changes
- Generating or replacing graph layers clears stale subject, amount, path, and selected-edge filters so an old subject ID or old amount threshold cannot hide all edges in the new graph.
- The amount slider now displays and filters using the current graph's clamped maximum, preventing a previous large threshold from filtering out all relationships after a narrower audit build.
- When amount/time/render filters remove edges, the canvas hides disconnected orphan nodes instead of showing unrelated standalone accounts.
- Entity-filtered build payloads now send `max_edges: 5000`; overview builds send `max_edges: 600`.

### Verified Commands
- `go test ./internal/...`
- `cd E:\codex\etl\frontend; npm run build`
- `go vet ./internal/...`
- `go build -o "$env:TEMP\etl-server-check.exe" .\cmd\server\`
- Rebuilt `bin\etl-server.exe`, restarted the 8000 service, and verified `http://127.0.0.1:8000/api/health` returned `ok`.
- Searched touched Flow/backend files and `frontend/dist/assets` for U+FFFD replacement characters.

### Open Items
- No live browser replay was performed with the user's exact 520k-row dataset in this turn.
- Very large unfiltered overview builds still intentionally render only the highest-amount 600 relationships to protect ReactFlow performance; use source/target filters for audit drill-downs.

### Notes
- Existing working tree already contained unrelated database-import and prior Flow changes; they were not reverted.
- Active backend PID at verification time: `37172`.
- Test URL: `http://127.0.0.1:8000`.
- Vite still reports the existing large chunk warning; build succeeds.

## 2026-05-24 22:29 Handoff

### Task
- Moved database object categories out of the left schema tree and into the right-side object panel.

### Changes
- Updated `frontend/src/features/flow/DBImportModal.tsx`.
- Updated `frontend/src/features/flow/db-import.css`.
- Updated `docs/AI_HANDOFF.md`.
- Updated `docs/CHANGELOG_AI.md`.

### New Functionality
- None. This was a layout correction for the database browser.

### API Changes
- None.

### Database Changes
- None.

### Frontend Changes
- Left database tree now shows connection -> database -> schema -> table directly, without object category folders under schema.
- Right "对象" tab now contains the object category buttons: 表、视图、实体化视图、函数、查询、备份.
- The table object list remains on the right and opens table data on double click.

### Verified Commands
- `cd E:\codex\etl\frontend; npm run build`
- Searched `frontend/src/features/flow/DBImportModal.tsx` and `frontend/src/features/flow/db-import.css` to confirm the left-side `tables:` category node was removed.
- Scanned `frontend/src/features/flow/DBImportModal.tsx`, `frontend/src/features/flow/db-import.css`, and `frontend/dist/assets` for U+FFFD replacement characters.

### Open Items
- Non-table categories remain visible but disabled until matching backend metadata APIs exist.

### Notes
- Vite still reports the existing large chunk warning; build succeeds.

## 2026-05-24 22:19 Handoff

### Task
- Adjusted the database import modal to match the requested database-client style: explicit connection test notifications, tree navigation for connection/database/schema/table, and an object-list main layout.

### Changes
- Updated `frontend/src/features/flow/DBImportModal.tsx`.
- Updated `frontend/src/features/flow/db-import.css`.
- Updated `docs/AI_HANDOFF.md`.
- Updated `docs/CHANGELOG_AI.md`.

### New Functionality
- Clicking "测试连接" now shows an Ant Design notification for both success and failure, including the target database host/port on success and the failure reason on error.
- The database browser now uses a tree structure: connection -> database -> schema -> object groups -> tables.
- The main database import area now starts with an "对象" view, a database-client style toolbar, and an object table with "名 / 行 / 注释" columns.

### API Changes
- None.

### Database Changes
- None.

### Frontend Changes
- Replaced the previous flat database/table list with an Ant Design `Tree` navigator.
- Added object group placeholders for 表、视图、实体化视图、函数、查询、备份 to mirror the requested structure while keeping unsupported physical DDL actions disabled.
- Added controlled tabs so opening a table switches to 表数据 and selecting a schema shows 对象.
- Refined the modal layout to a wider split-pane database-browser style.

### Verified Commands
- `cd E:\codex\etl\frontend; npm run build`
- `cd E:\codex\etl; go test ./internal/...`
- Scanned `frontend/src/features/flow/DBImportModal.tsx`, `frontend/src/features/flow/db-import.css`, and `frontend/dist/assets` for U+FFFD replacement characters.

### Open Items
- Table row counts and comments are displayed as placeholders because the current `/api/db/connections/:id/tables` endpoint only returns table name/type.
- New/delete physical table and export wizard buttons are visible for layout parity but disabled because no backend DDL/export-table API exists.

### Notes
- Vite still reports the existing large chunk warning; build succeeds.

## 2026-05-24 21:46 Handoff

### Task
- Restarted the project on port 8000 so the user can test the current database import build.

### Changes
- Updated `docs/AI_HANDOFF.md`.
- Updated `docs/CHANGELOG_AI.md`.

### New Functionality
- None. This turn was operational startup only.

### API Changes
- None.

### Database Changes
- None.

### Frontend Changes
- None.

### Verified Commands
- Inspected the existing port 8000 listener and command line.
- Stopped the older `E:\codex\etl\bin\etl-server.exe` process.
- Started `E:\codex\etl\bin\etl-server.exe` from `E:\codex\etl`.
- Verified `http://127.0.0.1:8000/api/health` returned `{"status":"ok"}`.
- Verified `http://127.0.0.1:8000/api/db/connections` returned JSON.
- Verified `http://127.0.0.1:8000` returned HTTP 200 and the built frontend assets.

### Open Items
- None.

### Notes
- Active backend PID at verification time: `42084`.
- Test URL: `http://127.0.0.1:8000`.

## 2026-05-24 20:58 Handoff

### Task
- Ran live MySQL functional tests using the provided local MySQL service on `localhost:3306`.

### Changes
- Updated `docs/AI_HANDOFF.md`.
- Updated `docs/CHANGELOG_AI.md`.

### New Functionality
- None. This turn was verification only.

### API Changes
- None.

### Database Changes
- Created temporary MySQL database `codex_mysql_import_test` and table `flow_txn` for testing.
- Dropped temporary database after verification.

### Frontend Changes
- None.

### Verified Commands
- MySQL client connection to MySQL 8.0.39 on `127.0.0.1:3306`.
- Temporary `PORT=8001` server `/api/health`.
- `/api/db/connections` create/list/delete and password-hidden response.
- `/api/db/connections/:id/test`.
- `/api/db/connections/:id/databases`.
- `/api/db/connections/:id/schemas?database=codex_mysql_import_test`.
- `/api/db/connections/:id/tables?database=codex_mysql_import_test`.
- `/api/db/connections/:id/columns?database=codex_mysql_import_test&table=flow_txn`.
- `/api/db/preview`, `/api/db/search`, `/api/db/query`.
- Non-SELECT query blocked by `/api/db/query`.
- `/api/db/table/insert`, `/api/db/table/update`, `/api/db/table/delete`.
- `/api/db/mappings/auto`, `/api/db/mappings/confirm`.
- `/api/db/import/tasks`, `/api/db/import/tasks/:id/start`.
- `/api/flow/build` against the imported database session.

### Results
- Connection test passed.
- Metadata browsing passed.
- Preview returned 2 paged rows with truncation.
- Search returned 1 matching row.
- SELECT query returned 2 rows.
- Non-SELECT query was blocked.
- Insert/update/delete each affected 1 row.
- Auto mapping resolved all required fields.
- Mapping save passed.
- Import task completed with 3 successful rows and 0 failed rows.
- Flow graph build returned 3 nodes and 3 edges.

### Open Items
- None for the MySQL live test.

### Notes
- Temporary database, temporary flow session, test connection config, and temporary 8001 server were cleaned up.
- The running 8000 server was not restarted; live verification used the current rebuilt binary on temporary port 8001.

## 2026-05-24 18:55 Handoff

### Task
- Ran live PostgreSQL functional tests using the provided local PostgreSQL service on `127.0.0.1:5432`.

### Changes
- Updated `docs/AI_HANDOFF.md`.
- Updated `docs/CHANGELOG_AI.md`.

### New Functionality
- None. This turn was verification only.

### API Changes
- None.

### Database Changes
- Created temporary schema `codex_dbimport_test` and table `flow_txn` in PostgreSQL for testing.
- Dropped temporary schema after verification.

### Frontend Changes
- None.

### Verified Commands
- `psql` connection to PostgreSQL 17 on `127.0.0.1:5432`.
- Temporary `PORT=8001` server `/api/health`.
- `/api/db/connections` create/list/delete and password-hidden response.
- `/api/db/connections/:id/test`.
- `/api/db/connections/:id/databases`.
- `/api/db/connections/:id/schemas?database=postgres`.
- `/api/db/connections/:id/tables?database=postgres&schema=codex_dbimport_test`.
- `/api/db/connections/:id/columns?database=postgres&schema=codex_dbimport_test&table=flow_txn`.
- `/api/db/preview`, `/api/db/search`, `/api/db/query`.
- Non-SELECT query blocked by `/api/db/query`.
- `/api/db/table/insert`, `/api/db/table/update`, `/api/db/table/delete`.
- `/api/db/mappings/auto`, `/api/db/mappings/confirm`.
- `/api/db/import/tasks`, `/api/db/import/tasks/:id/start`.
- `/api/flow/build` against the imported database session.

### Results
- Connection test passed.
- Metadata browsing passed.
- Preview returned 2 paged rows with truncation.
- Search returned 1 matching row.
- SELECT query returned 2 rows.
- Non-SELECT query was blocked.
- Insert/update/delete each affected 1 row.
- Auto mapping resolved all required fields.
- Mapping save passed.
- Import task completed with 3 successful rows and 0 failed rows.
- Flow graph build returned 3 nodes and 3 edges.

### Open Items
- The live test used ASCII PostgreSQL column names because direct `psql -c` setup of Chinese identifiers from PowerShell hit client-encoding issues. The application still handles Chinese field names from JSON/API paths; Chinese identifier creation should be verified through a SQL client configured with UTF-8 if needed.

### Notes
- Temporary schema, temporary flow session, and temporary 8001 server were cleaned up.
- The running 8000 server was not restarted because it served the older binary without `/api/db/*`; live verification used the current rebuilt binary on temporary port 8001.

## 2026-05-24 18:28 Handoff

### Task
- Implemented the database import remaining work from `D:\下载文件\数据库导入功能改造需求说明书.md`.

### Changes
- Added `internal/api/db_handlers.go`.
- Added `frontend/src/features/flow/DBImportModal.tsx`.
- Added `frontend/src/features/flow/dbImportApi.ts`.
- Added `frontend/src/features/flow/db-import.css`.
- Added `internal/dbimport/service_test.go`.
- Updated `internal/api/handlers.go`.
- Updated `internal/dbimport/types.go`, `internal/dbimport/store.go`, `internal/dbimport/service.go`.
- Updated `frontend/src/features/flow/FlowSourceModal.tsx`, `FlowPanel.tsx`, `frontend/src/hooks/useFlowOperations.ts`, `frontend/src/App.tsx`.
- Updated `.gitignore`, `go.mod`, `go.sum`.
- Added `数据库导入功能改造完成报告.md`.

### New Functionality
- Removed the visible "清洗的文件" source card from the Flow source selector.
- Added a "数据库导入" source card and modal.
- Added MySQL/PostgreSQL connection management, encrypted local config storage, connection testing, database/schema/table browsing, table preview/search, structure view, SELECT query tab, guarded insert/update/delete tab, forced field mapping confirmation, mapping persistence, and database-to-flow import task creation/start.
- Added `/api/db/*` backend endpoints for connection management, metadata browsing, preview/search/query, table edits, mappings, and import tasks.

### API Changes
- New endpoints:
  - `GET/POST/PUT/DELETE /api/db/connections`
  - `POST /api/db/connections/test`, `POST /api/db/connections/:id/test`
  - `GET /api/db/connections/:id/databases|schemas|tables|columns|indexes`
  - `POST /api/db/preview`, `/api/db/search`, `/api/db/query`, `/api/db/query/cancel`
  - `POST /api/db/table/insert`, `PUT /api/db/table/update`, `DELETE /api/db/table/delete`
  - `GET/POST/PUT/DELETE /api/db/mappings`
  - `POST/GET /api/db/import/tasks`, `GET/POST /api/db/import/tasks/:id/*`
- No existing endpoint paths were changed.

### Database Changes
- No application database was introduced.
- New encrypted local file config is stored under `backend/data/db_import/db_import_config.enc`; the directory is gitignored.

### Frontend Changes
- `FlowSourceModal` no longer exposes the deprecated "清洗的文件" option.
- Added a database import modal with connection, table preview, structure, query, data edit, field mapping, and import task tabs.
- Database import results are loaded into the existing imported-dataset flow so users can generate graphs with the existing mapping/build controls.

### Verified Commands
- `go test ./internal/...`
- `go vet ./internal/...`
- `cd E:\codex\etl\frontend; npm run build`
- `go build -o bin\etl-server.exe .\cmd\server\`
- Temporary `PORT=8001` smoke test: `/api/health`, `/api/db/connections`

### Open Items
- Live MySQL/PostgreSQL integration tests were not run because no database DSN/credentials were provided.
- Import progress is persisted per page and can be cancelled through task status, but the first UI version waits for the start request to finish instead of polling a background task.

### Notes
- Vite still reports the existing large chunk warning; build succeeds.

## 2026-05-24 16:17 Handoff

### Task
- Audited and fixed refactor regressions around Flow graph generation, history loading, filtering, and direction rules.

### Changes
- Updated `internal/api/handlers.go`.
- Added `internal/api/handlers_test.go`.
- Updated `frontend/src/hooks/useFlowOperations.ts`.

### Fixed Bugs
- History list response now includes the frontend-required fields: `job_id`, `name`, `size`, `updated_at`, and `status`.
- History detail now returns an ImportedDataset-compatible payload so historical uploaded data can be reloaded and used to generate graphs.
- Smart analysis no longer crashes when `/api/ai/analyze` returns only the placeholder report and no `flow_graph`.
- Imported Flow filtering now honors `target_filters`, `directions`, `start_date`, and `end_date`, not only source filters.
- Direction normalization now uses built-in aliases plus persisted custom aliases for graph build and unknown-direction checks.

### API Changes
- No endpoint paths changed.
- `/api/flow/history` response fields were expanded for frontend compatibility.
- `/api/flow/history/:job_id` now returns dataset metadata: `session_id`, `job_id`, `name`, `rows`, `columns`, `files`, `sample`, `signature`, `mapping_rule`.

### Database Changes
- None.

### Frontend Changes
- Historical data loading restores `importedDataset` and field mapping instead of assuming a ready-made `flow_graph`.
- Smart analysis applies a graph only when the API actually returns one.

### Verified Commands
- `go vet ./internal/...`
- `go test -count=1 -timeout 60s ./internal/...`
- `cd E:\codex\etl\frontend; npm run build`
- `go build -o bin\etl-server.exe .\cmd\server\`
- Temporary PORT 8001 smoke test: `/api/health`, `/api/flow/history`, `/api/flow/history/70027426-b61`

### Open Items
- Existing port 8000 server was already running as `E:\codex\etl\bin\etl-server.exe` and returned `/api/health` OK. It was not restarted.

### Notes
- Vite still reports the existing large chunk warning; build succeeds.

# AI Handoff Document

> 生成时间: 2026-05-24  
> 项目: 资金数据智能分析平台 (Financial Data ETL Platform)  
> 代码路径: `E:\codex\etl`

---

## Quick Facts

| 项目 | 值 |
|------|-----|
| 语言 | Go 1.25 (后端) + TypeScript 6 / React 19 (前端) |
| 代码规模 | 91 文件 / ~23,500 行 |
| 测试覆盖 | 29 单元测试 + 5 基准测试 (全部通过) |
| 部署形态 | 单二进制 (Go) + 前端 dist 静态文件 |
| 数据库 | 无 — 纯文件系统存储 |
| 启动方式 | `.\run.ps1` (Windows PowerShell) |

## What This Project Does

接收银行/支付宝/微信的资金流水原始文件 → 自动识别来源和表类型 → 清洗/标准化/去重 → 统一导出 → 生成交互式资金流向图 (ReactFlow) → 支持筛选、分析、人工标注 → 多格式导出 (PNG/SVG/Mermaid/GraphML 等)。

## Architecture at a Glance

```
上传文件
    ↓
[Scanner] 自动识别文件类型 (交易/账户/标签) + Provider 分类
    ↓
[Parser/Provider] 按提供商标注并发解析 (支付宝/微信/银行)
    ↓
[ETL Pipeline] Clean → Deduplicate → Export (Excel/CSV)
    ↓
[Flow Graph] 交易行 → 节点 + 边聚合 (截断 600 边)
    ↓
[API] Gin HTTP → JSON 响应
    ↓
[Frontend] React + ReactFlow → 交互式可视化
```

## Key Files to Read First

| 文件 | 用途 | 行数 |
|------|------|------|
| `cmd/server/main.go` | 服务入口 | 53 |
| `internal/api/handlers.go` | 全部 18 个 API 端点 | 1023 |
| `internal/etl/etl.go` | ETL 核心管道 | 664 |
| `internal/etl/flow_graph.go` | 流向图构建逻辑 | 222 |
| `internal/scanner/scanner.go` | 文件类型扫描器 | 405 |
| `internal/parser/alipay.go` | 支付宝解析器 | 483 |
| `internal/parser/wechat.go` | 微信解析器 | 353 |
| `internal/provider/bank.go` | 银行流水处理器 | 309 |
| `internal/rules/bank_rules.go` | 银行规则和表定义 | 630 |
| `internal/rules/custom_rules.go` | 自定义规则 JSON 读写 | 187 |
| `internal/model/model.go` | 数据模型定义 | 134 |
| `frontend/src/App.tsx` | React 根组件 | 474 |
| `frontend/src/hooks/useFlowOperations.ts` | 核心状态管理 (最大文件) | 4212 |
| `frontend/src/features/flow/flowTypes.ts` | Flow 类型 + 常量 | 320 |
| `frontend/src/features/flow/FlowPanel.tsx` | 流图主面板 | 512 |
| `frontend/src/features/flow/flowExport.ts` | 导出引擎 | 341 |
| `frontend/src/features/flow/useFlowGraph.ts` | 图计算 Hook | 402 |
| `frontend/src/features/flow/useFlowFilters.ts` | 过滤器逻辑 | 901 |
| `frontend/src/features/flow/FlowGraphPrimitives.tsx` | 自定义 ReactFlow 节点/边 | 168 |

## API Endpoints

| 方法 | 路径 | 功能 |
|------|------|------|
| POST | `/api/process` | 上传文件 + 运行完整 ETL 管道 |
| GET | `/api/download/:job_id` | 下载处理结果 (Excel) |
| GET | `/api/flow/history` | 列出历史流图会话 |
| GET | `/api/flow/history/:job_id` | 加载特定历史会话 |
| GET | `/api/flow/edge-detail` | 边明细查询 |
| POST | `/api/flow/edge-detail/imported` | 导入数据边明细 |
| POST | `/api/flow/upload` | 上传流图数据文件 |
| POST | `/api/flow/import` | 导入流图数据 (返回列+样本) |
| POST | `/api/flow/mapping-rules` | 保存列映射规则 |
| GET | `/api/flow/template` | 下载流图模板 |
| POST | `/api/flow/build` | 构建导入数据的流图 |
| POST | `/api/ai/analyze` | AI 分析 (占位) |
| POST | `/api/flow/direction-rules` | 保存方向规则 |
| POST | `/api/flow/direction-check` | 检查方向值 |
| POST | `/api/flow/values` | 获取列的唯一值 |
| GET | `/api/health` | 健康检查 |
| GET | `/api/files/current` | 列出当前上传文件 |
| POST | `/api/rules/analyze` | 分析规则样本 |
| POST | `/api/rules/confirm` | 确认/保存规则 |

## Dependency Versions

### Go (go.mod)
```
github.com/gin-gonic/gin v1.12.0
github.com/rs/zerolog v1.35.1
github.com/xuri/excelize/v2 v2.10.1
github.com/google/uuid v1.6.0
github.com/gin-contrib/cors v1.7.7
```

### Frontend (package.json)
```
react 19.2.6, react-dom 19.2.6
antd 5.29.3, @ant-design/icons 6.2.3
@xyflow/react 12.10.2
typescript 6.0.3, vite 8.0.13
html-to-image 1.11.13, jszip 3.10.1
```

## Development Rules

### Backend
- 包结构: `internal/<package>/` — api, etl, parser, provider, scanner, rules, storage, model, config, logger
- 所有 API 错误: `gin.H{"detail": "..."}` 格式
- 日志: 使用 `logger.Log.Info().Str().Msg()` 结构化
- 并发: goroutine + sync.Mutex + errChan
- 配置: `config.Config` 统一管理，环境变量 `PORT`, `DEBUG`
- 测试: Go testing 包, 文件放在 package 目录, 命名 `*_test.go`

### Frontend
- 组件放在 `features/<name>/` 下
- API 调用封装在 `api/client.ts` (getJson/postJson/postForm)
- 类型: 全局 `src/types.ts`, Flow 专用 `features/flow/flowTypes.ts`
- 样式: `*.css` 非 module, 放在对应 feature 目录
- 禁止引入新依赖

### Both
- 保持 API 契约不变
- 修改后运行测试确认基线
- 使用 `patch` 工具编辑 (不用 sed/awk)

## Known Risks & Pitfalls

1. **IPv6 网络**: Go proxy 可能超时 → `set GOPROXY=https://goproxy.cn,direct`
2. **Race Detector**: Windows/386 不支持 `-race`
3. **go mod tidy**: 网络受限时可能失败
4. **AI 分析**: `/api/ai/analyze` 占位 — 需配置 `DEEPSEEK_API_KEY`
5. **微信金额**: 调取数据金额可能是"分" — 检查原始 27 列表头
6. **大文件去重**: 100 万+ 行内存可能有压力
7. **FlowGraph 截断**: 硬限制 600 条边
8. **Excel sheet 名**: 小写 "sheet1" 非 "Sheet1"
9. **BOM + 全角空格**: parser 需要处理 `\ufeff` 和 `\u3000`
10. **Module path**: `github.com/etl/backend` 不能改

## Rollback

Go 后端独立于原 Python 项目。删除 `E:\codex\etl` 即可回滚，不影响原始代码。

## Related Documents

- `AGENTS.md` — 完整项目文档 (长期记忆)
- `重构完成报告.md` — Python → Go 迁移报告 (性能基准、已知问题、打包方式)
- `修复.md` — 本文件的任务描述
- `backend/config/custom_rules.json` — 自定义规则持久化
- `run.ps1` — 启动脚本
## 2026-05-24 16:01 Handoff

### Task
- Fixed the frontend crash after clicking generate graph: `Cannot read properties of undefined (reading 'meta')`.

### Changes
- Updated `frontend/src/hooks/useFlowOperations.ts`.
- Added `normalizeFlowGraphPayload` so the frontend accepts both response shapes for `/api/flow/build`:
  - nested `flow_graph: { nodes, edges, meta }`
  - current top-level `{ nodes, edges, meta }`
- The build action now passes the normalized graph into `applyFlowGraph` and reads `meta` from the normalized object.

### API Changes
- No endpoint path changes.
- No backend response changes.
- Frontend compatibility was expanded for the existing `/api/flow/build` response.

### Database Changes
- None.

### Frontend Changes
- Flow graph generation no longer assumes `payload.flow_graph` exists.
- Empty or malformed graph payloads are normalized to `{ nodes: [], edges: [], meta: {} }`, allowing existing empty-state handling to show a user-facing warning instead of throwing.

### Verified Commands
- `cd E:\codex\etl\frontend; npm run build`
- `cd E:\codex\etl; go test ./internal/...`

### Open Items
- None for this crash.

### Notes
- Existing `AGENTS.md`, `docs/`, and `修复.md` are untracked in git status; they were treated as user/project files and not removed.


## 2026-05-25 02:21 Handoff

### Task
- 修复字段映射阶段已选择 `交易流水号`、`摘要说明`、`备注` 后，右侧数据筛选区没有自动显示对应明细筛选框的问题。
- 同步补齐后端 Flow 明细字段映射/筛选链路，恢复现有 API 测试基线。

### Changes
- 更新 `frontend/src/features/flow/useFlowFilters.ts`。
- 更新 `internal/api/handlers.go`。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- 导入会话中只要当前字段映射能解析到 `交易流水号`、`摘要说明`、`备注`，右侧筛选区会自动显示对应的明细筛选行。
- 后端导入建图会把映射后的流水号、摘要说明、备注归一化进交易行，并支持 `detail_filters` 参与过滤。
- 边明细查询支持用源端/目标端备用列匹配实体值，避免图节点来自账号或证件号时明细为空。
- 流向图模板兜底生成列补齐 `交易流水号`。

### API Changes
- 无新增/删除/重命名端点路径。
- `/api/flow/build` 继续支持可选 `serial_column`、`summary_column`、`remark_column`、`detail_filters`。
- `/api/flow/edge-detail/imported` 继续支持可选备用列字段：`source_account_column`、`source_name_column`、`source_id_column`、`source_label_column`、`target_card_column`、`target_name_column`、`target_id_column`、`target_label_column`。

### Database Changes
- 无。

### Frontend Changes
- `useFlowFilters` 新增已映射明细字段自动补入逻辑；用户在字段映射弹窗选择后，右侧不再需要再次从“明细筛选字段”下拉中手动添加。

### Verified Commands
- `cd E:\codex\etl\frontend; npx tsc --noEmit`
- `cd E:\codex\etl; go test ./internal/...`
- `cd E:\codex\etl\frontend; npm run build`
- `cd E:\codex\etl; go vet ./internal/...`
- `cd E:\codex\etl; go build -o bin\etl-server.exe .\cmd\server\`
- 已重启 `E:\codex\etl\bin\etl-server.exe`，`http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 引用当前构建产物 `assets/index-K4UkElxG.js` 和 `assets/index-B-imr4oU.css`。

### Open Items
- 浏览器如缓存旧资源，需要强制刷新后再验证右侧筛选区。

### Notes
- Vite 仍报告既有的大 chunk warning，构建成功。
- 当前 8000 端口后端 PID 为 `38740`。
- 工作区已有多处先前未提交改动和 `backend/config/custom_rules.json` 修改，本次未回退这些改动。
## 2026-05-25 13:54 Handoff

### Task
- 修复画布过大时图片导出不完整的问题，目标是导出完整资金流向图画布，而不是只导出可视区域或被浏览器截断的局部。

### Changes
- 更新 `frontend/src/features/flow/flowExport.ts`。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- 图片导出现在按 ReactFlow 图坐标计算所有节点的完整包围盒，再临时重排导出视图，避免当前缩放/平移状态影响导出范围。
- PNG/JPEG/WebP 导出会在画布过大时自动降低导出比例，保证图片包含完整画布并避开浏览器 canvas 最大尺寸/面积限制。
- SVG 导出也使用完整包围盒，并在超大图时限制尺寸，避免导出尺寸超过常见浏览器处理范围。

### API Changes
- 无。

### Database Changes
- 无。

### Frontend Changes
- `expandForFullCapture` 改为解析 ReactFlow viewport transform，并基于真实图坐标计算完整导出范围。
- 导出前等待两帧浏览器渲染，确保临时导出布局生效后再交给 `html-to-image` 捕获。
- 保持排除控件、小地图、悬浮面板等 UI 元素的原有导出过滤逻辑。

### Verified Commands
- `cd E:\codex\etl\frontend; npx tsc --noEmit`
- `cd E:\codex\etl\frontend; npm run build`
- `cd E:\codex\etl; go test ./internal/...`
- `rg -n "�" frontend/src/features/flow/flowExport.ts frontend/dist/assets` 无匹配。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-JxTRmcgH.js` 和 `assets/index-B-imr4oU.css`。

### Open Items
- 未用用户的实际超大画布在浏览器中手动导出复现；本次验证覆盖类型检查、构建、Go 测试和资源加载。
- 浏览器如缓存旧资源，需要强制刷新后再测试导出。

### Notes
- Vite 仍报告既有的大 chunk warning，构建成功。
- 工作区已有多处先前未提交改动和 `backend/config/custom_rules.json` 修改，本次未回退这些改动。
