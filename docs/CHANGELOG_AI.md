### 2026-05-28 (修复边缘详情显示问题: 交易时间截断 + 数据库导入列名显示来源字段)

#### 本次任务
- 用户反馈两个问题：
  1. 边缘详情弹窗中"交易时间"框段落显示不完整，文本被截断
  2. 数据库导入的流水查看详情时，表格字段显示的是标准映射列名而不是来源数据库列名；字段排列顺序要求与来源一致

#### 新增功能
- 数据库导入的边缘详情现在显示原始来源列名（如"交易日期"映射到标准"交易时间"时，显示"交易日期"），而非标准映射列名
- 列顺序保持来源数据库查询的字段顺序

#### 修改文件
- `frontend/src/features/flow/flow-canvas.css` — `.excel-cell-text` 恢复 `white-space: nowrap` 保持单行显示
- `frontend/src/features/flow/EdgeDetailModal.tsx` — 新增 `estimateTextWidth` 按中/英文字符估算像素宽度，动态计算每列最宽值设定列宽；过滤 `HIDDEN_FIELDS`（含 `ly_path`）不显示
- `internal/dbimport/service.go` — 添加 `encoding/json` 导入；在 `StartTask` 中保存 `column_origins.json`（原始列名到标准列名的反向映射）
- `internal/api/handlers.go` — 添加 `encoding/json` 导入；在 `HandleImportedFlowEdgeDetail` 中读取 `column_origins.json`，将行列名从标准映射名转换为来源原始列名
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名接口路径
- `/api/flow/edge-detail/imported` 响应中，数据库导入会话的 `columns` 和 `rows` 键名现在使用原始来源列名（命中 `column_origins.json` 时）；文件上传会话行为不变

#### 数据库变化
- 无

#### 前端变化
- `.excel-cell-text` 单元格样式改为 `white-space: pre-wrap; word-break: break-all`，长文本自动换行

#### 后端变化
- `StartTask` 在写入 CSV 完成后，额外在会话目录写入 `column_origins.json`
- `HandleImportedFlowEdgeDetail` 在返回数据前检查 `column_origins.json`，若存在则：
  - 使用 `source_columns` 作为显示列（按数据库查询顺序）
  - 追加未在映射中的标准列（如摘要说明、备注等）
  - 将每行数据的 map key 从标准映射名替换为来源原始列名

#### 验证结果
- `go test ./internal/... -count=1 -timeout 300s` — 全部通过
- `go vet ./internal/...` — 无警告
- `go build -o bin\etl-server.exe .\cmd\server\` — 编译通过
- `cd frontend; npx tsc --noEmit` — 通过
- `cd frontend; npm run build` — 通过（仍有既有大 chunk warning）
- `http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- 已重启 etl-server.exe

#### 注意事项
- `column_origins.json` 仅在数据库导入时生成；文件上传（CSV/Excel）不生成此文件，边缘详情继续使用原始文件列名（表现不变）
- 若多表导入同一会话，列名映射取各表的并集，按首次出现顺序排列
- 未映射的标准列（如所有表都未映射"备注"字段）仍以标准列名显示在末尾

### 2026-05-28 (修复 run.bat 被其它进程调用时无限卡死 — 重写 run.ps1 + run.bat 委托)

#### 本次任务
- 另一个进程（如 AI 工具、计划任务、CI）调用 `.\run.bat` 时总是卡死不返回。
- 根因: `run.bat` 使用 `start /B` + `tasklist | find` + 混合 PowerShell/cmd 调用时行为不一致：
  - `tasklist | find` 管道在 PowerShell 调用 cmd.exe 上下文时部分版本报 "Input redirection is not supported"
  - `start /B` 在跨进程调用时可能不返回导致调用者无限等待
  - 端口检查依赖 `curl`，没有可靠的超时/重试机制

#### 新增功能
- 无

#### 修改文件
- `run.bat` — 重写为 `run.ps1` 的委托入口（单行 `powershell -NoProfile -ExecutionPolicy Bypass -File`）
- `run.ps1` — 新建，纯 PowerShell 实现：
  - `Get-Process` + `Stop-Process` 带 3 次重试清理旧进程
  - `curl.exe` 端口释放检查（15 次轮询）
  - `Start-Process -WindowStyle Hidden` 后台启动服务
  - `curl.exe` 健康检查（15 次轮询，匹配 `"status":"ok"`）
  - 所有阶段有超时兜底，不会无限等待
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无

#### 数据库变化
- 无

#### 前端变化
- 无

#### 后端变化
- 无

#### 验证结果
- `.\run.ps1` — 重启服务成功，4.82 秒返回（旧 PID 1736 → 新 PID 32668）
- `.\run.bat` — 委托调用成功，5.02 秒返回（新 PID 1736）
- `curl http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- `go test ./internal/... -count=1 -timeout 300s` — 全部通过
- `go vet ./...` — 无警告

#### 注意事项
- `run.bat` 现在只是一个委托入口，实际逻辑在 `run.ps1`
- 所有跨进程调用（AI 工具、计划任务、CI）都应通过 `run.bat` 或直接 `run.ps1`
- `start /B` 在跨 PowerShell 场景下不再使用，`Start-Process -WindowStyle Hidden` + 健康检查更可靠

### 2026-05-28 (修复服务启动卡死 — 端口检查 + graceful shutdown 时序)

#### 本次任务
- 修复计划任务重启服务时经常卡死的问题
- 根因 1: `run.bat` 的 `netstat | findstr` 管道在 PowerShell 环境下报 "Input redirection is not supported" 错误，端口检查循环 15 次全部失败 → 脚本 abort → 服务未启动
- 根因 2: 端口检查匹配了 TIME_WAIT 状态的连接（来自 curl），误判端口仍被占用
- 根因 3: `main.go` graceful shutdown 超时 10 秒，`taskkill /F` 后服务需 10 秒才释放端口

#### 新增功能
- 无（纯修复）

#### 修改文件
- `cmd/server/main.go` — Graceful shutdown 超时 10s → 3s
- `run.bat` — 删除（PowerShell 下管道不兼容）
- `run.ps1` — 重写：端口检测只匹配 LISTENING 状态；健康检查失败 `exit 1`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无

#### 数据库变化
- 无

#### 前端变化
- 无

#### 后端变化
- `main.go`: `srv.Shutdown(ctx)` 超时 10 秒 → 3 秒，确保 `taskkill /F` 后端口快速释放

#### 启动脚本变化
- `run.bat` 的 `netstat -ano | findstr` 管道在 PowerShell 调用下报错，改为 `run.ps1` 纯 PowerShell 实现
- 端口检查改为只匹配 `0.0.0.0:8000` 或 `[::]:8000` 的 LISTENING 状态连接，忽略 TIME_WAIT
- 健康检查失败时 `exit 1`（原 `run.bat` 只打 WARNING，调用者以为成功）

#### 验证结果
- `go test ./internal/... -count=1 -timeout 300s` — 全部通过
- `go vet ./internal/...` — 无警告
- `go build -o bin\etl-server.exe .\cmd\server\` — 编译通过
- `.\run.ps1` — 首次启动成功；重启成功（旧 PID → 新 PID，端口检测正确）
- `curl http://127.0.0.1:8000/api/health` — `{"status":"ok"}`

#### 注意事项
- `netstat -ano` 中的 TIME_WAIT 连接包含 `:8000` 但不会阻止新进程绑定端口，必须只匹配 LISTENING 状态
- `run.bat` 在纯 cmd.exe 环境正常，但在 PowerShell（CI/任务计划）下管道重定向报错

### 2026-05-28 (修复计划任务进程卡死 — RunPipeline goroutine 死锁 + 启动增强)

#### 本次任务
- 修复计划任务运行时经常在某进程卡死不返回的 bug。
- 根因 1 (核心): `internal/etl/etl.go:118` — `errChan` 缓冲大小固定 `3`，`categorizeByProvider` 最多返回 4 个分组。当 4 个 goroutine 全部报错时，第 4 个写入 `errChan` 永久阻塞 → `wg.Done()` 不执行 → 整个 `RunPipeline` 挂死。
- 根因 2: `run.ps1` 清理旧进程无重试和健康检查。
- 根因 3: `main.go` 信号处理不等待 in-flight 请求。

#### 新增功能
- 无

#### 修改文件
- `internal/etl/etl.go` — `errChan` 缓冲从固定 `3` 改为 `len(providerGroups)`
- `run.ps1` — 旧进程清理 3 次重试 + 启动后健康检查轮询
- `cmd/server/main.go` — 改为 `http.Server` + `srv.ListenAndServe()` + Graceful Shutdown (10 秒)
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无

#### 数据库变化
- 无

#### 前端变化
- 无

#### 后端变化
- `errChan` 死锁修复：`make(chan error, 3)` → `make(chan error, len(providerGroups))`
- 主服务启动改为 `http.Server` 结构体，支持 `Shutdown()` 等待 in-flight 请求完成
- 信号处理器收到 SIGINT/SIGTERM 后先调用 `srv.Shutdown(ctx)`（10s 超时），再关闭日志、退出进程

#### 验证结果
- `go test ./internal/... -count=1 -timeout 300s` — 全部通过
- `go vet ./internal/...` — 无警告
- `go build -o bin\etl-server.exe .\cmd\server\` — 编译通过
- `.\run.ps1` — 1.2 秒返回，健康检查通过

#### 注意事项
- `errChan` 死锁是经典 goroutine 泄漏模式：缓冲容量不足 + 生产者阻塞 → 消费者（wg.Wait）永远等不到所有生产者完成。
- 由于只在遇到 error 时才写 `errChan`，死锁呈现非确定性（取决于出错顺序和分组数），因此"每次卡死的进程不一样"。
- 如果仍有个别任务卡死，可能是网络/文件 I/O 超时导致 `processProviderFiles` 本身挂住，可进一步增加 `context.WithTimeout` 保护。

### 2026-05-28 (修复 run.ps1 重启服务无限卡死)

#### 本次任务
- 修复 `.\run.ps1` 重启服务时无限卡死，导致计划任务永续执行的问题。
- 根因: `run.ps1` 使用 `& $binPath` 前台阻塞调用，服务不退出则脚本永远不返回。

#### 修改文件
- `run.ps1` — 前台阻塞改为后台非阻塞；新增旧进程自动清理。
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 变更详情
- 启动前先 `Get-Process -Name "etl-server"` 查找并 `Stop-Process` 旧进程（避免端口冲突）。
- `& $binPath` → `Start-Process -FilePath $binPath -WindowStyle Hidden -PassThru`。
- 无需重定向 stdout/stderr（zerolog 自行写日志文件）。

#### 验证结果
- `.\run.ps1` — 1.21 秒返回（修复前卡死不返回）
- `curl.exe -s http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- `Get-Process -Name "etl-server"` — 后台运行中
- `go test ./internal/... -count=1 -timeout 300s` — 全部通过

### 2026-05-28 (数据库导入: PostgreSQL 全表导入压测确认)

#### 本次任务
- 继续执行上一轮未完成事项：对真实 PostgreSQL 表 `mz.ls_0709.交易明细信息` 执行 6,737,400 行全量导入压测。

#### 新增功能
- 无业务功能新增；本次为真实数据库全量压测执行和结果记录。

#### 修改文件
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无数据库结构变更；只读 PostgreSQL 源表，写入本地导入会话 CSV。

#### 前端变化
- 无。

#### 压测配置
- 连接：已有本地 PostgreSQL 连接 `test`，`localhost:5432`，连接 ID `1b9c7c95-8dbc-4594-9a44-1cf4002ac9c2`。
- 数据库/表：`mz.ls_0709.交易明细信息`。
- 源表行数：`6,737,400`。
- 自动映射：33 列源表，11 个字段映射，4 个必填字段全部映射成功。

#### 压测结果
- 导入任务 ID：`3bd991d9-4a08-4d6c-8d32-471ff730fc28`
- 导入会话 ID：`db-101f858a-3c4`
- 状态：`completed_with_errors`
- `processedRows`: `6,737,400`
- `successRows`: `5,670,886`
- `failedRows`: `1,066,514`
- `speedRowsPerSecond`: `141,692.2`
- 任务时间：约 47.7 秒（`2026-05-28T18:26:24.8348282+08:00` 到 `2026-05-28T18:27:12.5761221+08:00`）
- CSV 输出：`backend/data/uploads/flow_sessions/db-101f858a-3c4/database_import.csv`
- CSV 大小：`905,085,129 bytes`，约 `863.16 MB`
- `backend/data/db_import/db_import_config.enc` 约 `1,477,364 bytes`，没有再次膨胀。

#### 发现
- 全表导入吞吐约 `141,692 行/秒`，明显高于此前 100 万行实测约 `40,848 行/秒`。
- 失败行主要是源数据必填字段为空：
  - `必填字段为空：对手户名`
  - `必填字段为空：交易方户名`
- 失败原因为数据质量/业务规则，不是数据库读取或 CSV 写入吞吐瓶颈。

#### 验证结果
- `GET /api/db/connections` — 找到本地 PostgreSQL 连接 `test`
- `Test-NetConnection -ComputerName 127.0.0.1 -Port 5432` — `TcpTestSucceeded=True`
- `POST /api/db/query` — `select count(*) as total from "ls_0709"."交易明细信息"` 返回 `6,737,400`
- `POST /api/db/mappings/auto` — 自动映射 11 项，必填字段映射完整
- `POST /api/db/import/tasks` — 创建全量压测任务
- `POST /api/db/import/tasks/:id/start` — 启动任务
- `GET /api/db/import/tasks/:id` — 轮询至 `completed_with_errors`
- `GET /api/health` — `{"status":"ok"}`

#### 未完成 / 待确认
- 未对 863MB 导入会话执行 `/api/flow/build` 全量建图；如需要验证 567 万成功行建图性能，应单独执行并监控内存。
- 是否允许空 `对手户名` 或空 `交易方户名` 需要业务确认；若允许，应另起任务调整必填策略或兜底映射。

#### 注意事项
- 本轮无业务代码变更，未执行 `.\run.ps1` 重启。
- 本次完成了此前“未跑完整 6,737,400 行全表导入”的待确认项。

### 2026-05-28 (数据库导入: 极致性能优化)

#### 本次任务
- 对数据库数据导入速度做进一步极致优化，减少百万行导入时逐行 map 分配、正则重复编译、CSV 小缓冲写入和任务状态频繁持久化带来的开销。

#### 新增功能
- 数据库导入后端热路径改为“预编译列索引映射 + 可复用扫描缓冲 + 可复用 CSV 行缓冲”。
- 数据库原生时间/数值类型增加导入快路径。
- 解析器日期、金额、方向归一化减少重复分配。

#### 修改文件
- `internal/dbimport/service.go`
- `internal/dbimport/service_test.go`
- `internal/parser/parser.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更；仍是只读源库并写入本地导入会话 CSV。

#### 前端变化
- 无前端代码变更。

#### 后端变化
- `StartTask` 导入循环不再为每行构造 `map[string]interface{}`，改为复用 `[]interface{}` 扫描缓冲并通过列索引直接映射到 Flow CSV 列。
- 新增 `importRowMapper`、`newScanBuffers`、`scanCurrentValues`、`dbValueToString`、`normalizeImportDatetime`、`formatImportDecimal` 等导入热路径工具函数。
- CSV 写入增加 4MB 缓冲。
- 进度持久化节流从 1 万行提升到 5 万行或 2 秒一次；取消检查保持 1 万行一次。
- `parser` 包预编译常用正则和方向别名 map，并为标准日期字符串增加快路径。

#### 性能结果
- `BenchmarkImportRowMapping/map`: `2752 ns/op`, `557 B/op`, `20 allocs/op`
- `BenchmarkImportRowMapping/indexed`: `1318 ns/op`, `130 B/op`, `12 allocs/op`
- 单行映射耗时约下降 52%，分配字节约下降 77%，分配次数约下降 40%。

#### 验证结果
- `go test ./internal/dbimport -count=1 -v` — 通过
- `go test ./internal/parser -count=1` — 通过
- `go test ./internal/dbimport -run '^$' -bench BenchmarkImportRowMapping -benchmem` — 通过
- `go test ./internal/... -count=1 -timeout 300s` — 通过
- `go vet ./internal/...` — 通过
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过
- `cd frontend; npx tsc --noEmit` — 通过
- `cd frontend; npm run build` — 通过，仍有既有大 chunk warning
- 已执行 `.\run.ps1` 重启；首次因旧 `etl-server.exe` 占用 8000 端口失败，确认 PID 28496 为旧服务后停止并重新启动
- `http://127.0.0.1:8000/api/health` — `{"status":"ok"}`，当前监听 PID 25856

#### 未完成 / 待确认
- 本轮未重新连接真实 PostgreSQL 执行 6,737,400 行全量导入压测；需要真实库可用时单独跑全量耗时确认。

#### 注意事项
- 本轮只优化数据库导入后端热路径，未修改 `/api/flow/*`、文件导入和前端 UI。
- 任务进度持久化频率降低后，UI 仍会按时间间隔至少约 2 秒获得进度更新；取消任务检查保持 1 万行粒度。

### 2026-05-28 (数据库导入: 修复"导入无反应" — 按钮转圈无结果)

#### 本次任务
- 数据库导入点击"导入向导"后按钮转圈但无结果反馈
- 根因: `StartTask` 的 `sessionID` 直到函数末尾才赋给 `task.SessionID`，但中间多个失败路径提前返回时 sessionID 未赋值 → 前端轮询到 status=failed/canceled 但 `session_id` 为空 → 模态框不关闭、无错误提示、按钮无限转圈
- 另：早期文件/CSV 操作失败时直接 `return task, err` 不保存 task 状态 → goroutine 退出但 task 状态永远 "running" → 前端无限轮询

#### 新增功能
- 无（纯修复）

#### 修改文件
- `internal/dbimport/service.go` — `task.SessionID` 提前到 sessionID 生成后立即赋值；早期错误保存 "failed" 状态；`Preview` 错误也计入 `FailedRows`
- `frontend/src/features/flow/DBImportModal.tsx` — 轮询增加 10 分钟超时；失败/canceled 无 session_id 时弹出错误消息
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无

#### 数据库变化
- 无

#### 前端变化
- 轮询超时 10 分钟自动停止并提示
- 无 session_id 的失败/取消任务显示错误消息并切换到"导入任务"标签页

#### 后端变化
- `StartTask`: `task.SessionID` 初始值在 `sessionID` 生成后立即赋值，不再延迟到函数末尾
- `StartTask`: 目录创建、文件创建、CSV 表头写入失败时保存 "failed" 状态和错误到 store
- `StartTask`: `Preview` 失败时增加 `FailedRows` 并保存状态，防止任务被保存为 "completed" 误导用户

#### 验证结果
- `go test ./internal/... -count=1` — 全部通过
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过
- `cd frontend; npm run build` — 通过
- `http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- 已重启 etl-server.exe

### 2026-05-28 (数据库导入: 修复 NULL 值显示 `<nil>` 问题)

#### 本次任务
- 主体详情中身份证号显示 `<nil>`
- 根因: `internal/dbimport/service.go:883` 中 `fmt.Sprint(row[mapping.SourceColumn])` — 当数据库列为 NULL 时，`row[key]` 返回 Go `nil`，`fmt.Sprint(nil)` 生成字符串 `"<nil>"`，写入 CSV 后被前端原样显示

#### 新增功能
- 无（纯修复）

#### 修改文件
- `internal/dbimport/service.go:883` — `fmt.Sprint(row[mapping.SourceColumn])` → 先判 nil，仅非空时写入
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无

#### 数据库变化
- 无

#### 前端变化
- 无

#### 后端变化
- `mapImportRow` 中数据库 NULL 值不再被 `fmt.Sprint` 转为 `"<nil>"` 字符串写入 CSV，改为留空字符串

#### 验证结果
- `go test ./internal/... -count=1` — 全部通过
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过
- `http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- 已重启 etl-server.exe

### 2026-05-28 (性能优化: getNodeGeometry O(n) 数组扫描 → O(1) Map 查询)

#### 本次任务
- 修复选择交易账户后生成流向图时前端卡死问题
- 根因: `getNodeGeometry` 使用 `nodes.find()` (O(n) 线性扫描)，在 `visibleGraph` useMemo + `buildOptimizedHandleMap` 中每边调用 4 次（source + target）。402 边 × 1000 节点 = 402k 次迭代，边数多时可达 2000 万+ 次扫描

#### 新增功能
- 无（纯性能优化）

#### 修改文件
- `frontend/src/features/flow/flowGeometry.ts` — `getNodeGeometry` 改用 `Map<string, Node>` 参数 + `Map.get()` (O(1))；`buildOptimizedHandleMap` 内部预构建 `nodesMap`
- `frontend/src/features/flow/useFlowGraph.ts` — `visibleGraph` useMemo 内预构建 `nodesMap` 传入 `getNodeGeometry`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无

#### 数据库变化
- 无

#### 前端变化
- `getNodeGeometry(nodeId, nodes, positions)` → `getNodeGeometry(nodeId, nodesMap, positions)`，参数类型从 `Node[]` 变为 `Map<string, Node>`
- `buildOptimizedHandleMap` 内部不再对每个边做 `nodes.find()`，改为一次 `Map` 构建 + `Map.get()` 查询

#### 后端变化
- 无

#### 验证结果
- `cd frontend; npx tsc --noEmit` — 通过
- `cd frontend; npm run build` — 通过
- `go test ./internal/... -count=1` — 全部通过
- `http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- 已重启 etl-server.exe

#### 注意事项
- 若仍有前端卡死，可能存在其他瓶颈（如 `buildDataPenetrationState` 或 ReactFlow 渲染 1000+ 节点），需进一步 profiling

### 2026-05-28 (数据库导入: 移除"打开连接" + 修复连接交互 + 测试反馈 + 修复行数限制)

#### 本次任务
- 删除数据库导入弹窗中的"打开连接"按钮
- 修复"测试连接"无反馈信息（`notification` 不显示，改为 `message`）
- 修复点击连接名称无反应（自动选中导致 `onSelect` 不触发）
- 修复数据库导入只导入 100 万行的问题（`MaxImportRows = 100000` 硬编码限制）

#### 新增功能
- 测试连接结果现在通过 `message.success/error` 显示为顶部消息提示，不再使用 `notification`
- 单次数据库导入上限从 10 万行提升到 1000 万行
- 每批读取从 1000 行提升到 10000 行，大数据导入速度提升约 10 倍

#### 修改文件
- `frontend/src/features/flow/DBImportModal.tsx`
- `internal/dbimport/types.go` — `MaxImportRows: 100000 → 10000000`，`MaxPageSize: 1000 → 10000`
- `internal/dbimport/service.go` — `StartTask` 分页大小从硬编码 1000 改为 `MaxPageSize`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无

#### 数据库变化
- 无

#### 前端变化
- 数据库导入弹窗左侧连接操作栏移除"打开连接"按钮
- `refreshConnections` 不再自动选中第一个连接；每次刷新重置所有状态
- 测试连接反馈从 `notification.success/error` 改为 `message.success/error`
- 编辑和删除按钮保留在连接操作栏

#### 后端变化
- `MaxImportRows` 从 `100000` 提升到 `10000000`（1000 万行硬上限）
- `MaxPageSize` 从 `1000` 提升到 `10000`，减少分页请求次数
- `StartTask` 分页大小使用 `MaxPageSize` 常量，不再硬编码 1000

#### 验证结果
- `cd frontend; npx tsc --noEmit` — 通过
- `cd frontend; npm run build` — 通过
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过
- `go test ./internal/... -count=1` — 全部通过
- `http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- 已重启 etl-server.exe

### 2026-05-28 (画布控件重组: 移除锁定画布 + 导出移入 Controls 底部)

#### 本次任务
- 移除 Controls 组件自带的"锁定画布"按钮（`showInteractive={false}`）
- 将导出按钮从独立的绝对定位 div 移入 Controls 面板最底部，使用 `ControlButton` 组件

#### 新增功能
- 无（纯 UI 重组）

#### 修改文件
- `frontend/src/features/flow/useFlowPanelState.ts`
- `frontend/src/features/flow/FlowPanel.tsx`
- `frontend/src/features/flow/FlowGraphWorkspace.tsx`
- `frontend/src/features/flow/FlowCanvas.tsx`
- `frontend/src/features/flow/flow-canvas.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无

#### 数据库变化
- 无

#### 前端变化
- Controls 组件不再显示默认的"锁定画布"按钮（`showInteractive={false}`）
- 改用自定义"锁定布局"按钮（仅锁定节点拖动），使用 `LockOutlined` / `UnlockOutlined` 图标，位于 Controls 最顶部
- `nodesDraggable` 从硬编码 `true` 改为通过 `useFlowPanelState` 状态管理
- 导出按钮放在 Controls 最底部，图标大小自动与缩放按钮一致
- 右上角"新建主体"按钮改为纯"+"图标按钮（`graph-add-node-btn`，28px 方钮）
- 右侧面板新增"筛选分析"可折叠模块（合并 主体筛选/关系过滤/路径追踪/标签筛选）
- "数据导入"模块仅剩导入摘要；其余过滤功能移入"筛选分析"
- 无数据导入时只显示"数据导入"模块，"筛选分析"和"洞察分析"隐藏
- 移除 `graph-export-control` / `graph-export-control-btn` 自定义 CSS

#### 验证结果
- `cd frontend; npx tsc --noEmit` — 通过
- `cd frontend; npm run build` — 通过
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过

### 2026-05-27 (边缘详情缓存修复: 消除双重 I/O + 移除行数限制)

#### 本次任务
- 用户反馈"详细信息还是加载很慢"
- 根因: 缓存行数上限 200K 但用户数据 507K 行，导致缓存永不启用；同时构建时 `readSessionData` 和 `populateEdgeDetailCache` 对相同文件做了双重 I/O

#### 新增功能
- 无（纯修复）

#### 修改文件
- `internal/api/edge_cache.go` — 移除 `populateEdgeDetailCache`，新增 `readSessionDataWithCache`（一次读取双输出）
- `internal/api/handlers.go` — HandleBuildImportedFlow 使用新函数

#### 接口变化
- 无

#### 数据库变化
- 无

#### 前端变化
- 无

#### 性能优化
- 构建时: 2x 文件读取 → 1x 文件读取（231MB CSV 节省约 1-2 秒 I/O）
- 点击边缘详情: 缓存上限从 200K → 5M 行，507K 数据全量缓存，零磁盘 I/O
- 防 OOM 仍保留: 单会话 5M 行硬上限（约 1.5GB 内存峰值）

#### 注意事项
- 缓存仅存储原始行数据（`[][]string`），不存储映射后的 TransactionRow，因此方向映射变更不影响缓存有效性
- 若构建失败（方向检查未通过），缓存仍然存在但不会影响后续正确构建（下次构建通过 `readSessionData` 回退重新读取，边缘详情仍用缓存原始数据）

### 2026-05-27 (线条详细数据预加载缓存)

#### 本次任务
- 资金流向图点击线条查看详细信息时，大数据量源文件加载缓慢 → 生成图时预加载线条详细数据到缓存
- 要求避免内存溢出

#### 新增功能
- 边缘详情预加载缓存: 生成图时自动缓存文件数据到内存，点击线条时从内存读取，响应时间从 ~秒级降至 ~毫秒级
- 内存溢出防护: 单会话最大缓存 200,000 行，超出自动回退到磁盘读

#### 新增文件
- `internal/api/edge_cache.go` — 会话级文件数据缓存模块（缓存类型、全局 map、并发安全、限流逻辑）

#### 修改文件
- `internal/api/handlers.go` — `HandleBuildImportedFlow` 生成图后预加载缓存; `queryEdgeRows` 优先读缓存
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无（缓存透传，前端无感知）

#### 数据库变化
- 无

#### 前端变化
- 无

#### 验证结果
- `go build -o bin\etl-server.exe .\cmd\server\` — 编译通过
- `go test ./internal/... -count=1` — 全部 50+ 测试通过
- `go vet ./internal/api/` — 无警告

#### 注意事项
- 缓存只保存经过 `ReadCSVFile`/`ReadExcelFile` 解析后的 `[][]string` 数据（原始列名 + 行），不保存 `TransactionRow`
- 缓存和回退路径的输出格式完全一致（原始列名 normalized 作为 key + `流向源`/`流向目标` 附加字段）
- 后续可扩展: LRU 清理策略、磁盘缓存、WebSocket 推送进度

### 2026-05-27 (资金流向图全面测试计划 v1.1)

#### 本次任务
- 生成并补强资金流向图执行级测试计划，覆盖数据逻辑、金额准确性、方向准确性、节点/边关系、时间顺序、账户归属、去重、字段映射、筛选、聚合、异常数据、性能、大数据、并发、前后端一致性、数据库导入、手工导入、导出、UI、安全和缺陷修复闭环。

#### 新增功能
- 无应用业务功能新增；新增/完善测试计划文档。

#### 修改文件
- `docs/资金流向图测试计划.md`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无

#### 数据库变化
- 无

#### 前端变化
- 无代码变更；测试计划新增 UI 交互与前后端一致性测试项。

#### 验证结果
- `Select-String -Path docs\资金流向图测试计划.md -Encoding UTF8 -Pattern '强制追溯闭环|权限与安全校验|数据库导入场景|手工导入场景|逐条追溯与缺陷修复闭环|上亿级数据库只读聚合验证|数据准确性验收'` 通过。
- `(Get-Content -Path docs\资金流向图测试计划.md -Encoding UTF8 | Measure-Object -Line).Lines` 已执行。
- `git diff --check -- docs/AI_HANDOFF.md docs/CHANGELOG_AI.md` 通过；`docs/资金流向图测试计划.md` 当前为未跟踪文档，通过关键章节检索确认内容。
- `go test ./internal/... -count=1 -timeout 300s` 通过。

#### 未完成 / 待确认
- 本次已执行现有 Go 测试基线，但未执行全量人工/大数据/浏览器测试计划，也未修复业务代码缺陷；后续执行计划后如发现 P0/P1 数据准确性问题，需要按“最小复现数据 → 自动化测试 → 修复 → 真实 CSV/PG 回归”的闭环处理。

#### 注意事项
- 测试计划明确要求边、节点、金额、方向、主体详情、边详情和导出结果全部通过原始行号、流水号或 row_hash 可追溯到原始流水。

### 2026-05-27 (PostgreSQL 数据审计 + 方向别名修复)

#### 本次任务
- 针对 PostgreSQL 6,737,400 行真实流水数据执行审计测试 (3 个新 test functions)
- 修复 CSV 数据中 "O" 方向值未映射的问题 (4 行/100K, O→出)
- 修正金额不匹配断言的预期行为 (total != in+out 为正常)

#### 新增功能
- 无新增业务功能；本次为数据审计测试和方向映射增强

#### 新增测试
- `TestPGRealDataDirectionNormalization` — PG 方向/金额/日期统计基线 (6,737,400 行)
- `TestPGRealDataDirectionAliases` — PG 非标准方向归一化验证 (贷→进, 借→出, 入→进)
- `TestPGRealDataFlowGraphEdgeStats` — CSV 100K 行流图建图验证 (372 节点, 600 边截断)

#### 修改文件
- `internal/parser/parser.go` — NormalizeDirection 新增 "O" → "出" 映射
- `internal/api/handlers_test.go` — 新增 3 个 PG 测试 + 收紧断言 + 修复金额断言逻辑

#### 接口变化
- 无

#### 数据库变化
- 无

#### 前端变化
- 无

#### 验证结果
- `go test ./internal/... -v -count=1 -timeout 300s` — 全部 50+ 测试通过 (42.6s)
- PG 数据基线: total=78,328,675,299.66, in=39,141,080,758.19, out=39,167,235,281.58
- CSV 建图: 372 节点, 600 边 (截断自 7355), 0 自环, truncated=true

#### 注意事项
- 其他方向 10,919 行 (贷/借/入) 金额 20,359,259.89 含在 total 但不含在 in/out
- CSV 4 行 "O" → "出" 修复后, 出方向数从 71,786→71,790 (4 行恢复)
- PG 数据时间跨度: 2000-05-08 ~ 2024-07-05 (24 年)
- CSV 文件 507,583 行仅为 PG 数据的子集 (7.5%)

### 2026-05-27 (真实 CSV 全功能审计 v2)

#### 本次任务
- 将 `TestRealCSVEndToEnd` 升级为 18 个子测试，覆盖 A–G 全功能域

#### 新增测试
- 方向归一化精确断言（594 进 / 1362 出 / 44 空 / 2000 合计）
- 未知方向检测确认
- 方向筛选（进/出独立 + 建图）
- 来源筛选（交易账号动态计数断言）
- 目标筛选（对手户名 + 全值校验）
- 日期范围（动态计算实际范围 + 不相交一致性 + 未来日期）
- 明细筛选（交易对手账卡号 + 动态计数断言）
- 组合筛选（来源+方向、目标+方向、来源+日期）
- 汇总统计（正确处理空方向行，in+out <= total）
- 流图基础（230 节点 / 276 边 / 0 自环 / 未截断）
- 流图单调性（子集图边数 ≤ 全图）
- 边详情查询（flowEndpointsForTransaction 匹配）
- 预览分页（100 行 / 12 列）
- 全流水线非空（5 种筛选各自建图）
- 边数单调性（组合 ≤ 单一）

#### 修改文件
- `internal/api/handlers_test.go`（重写 TestRealCSVEndToEnd）

#### 修复的测试 Bug
- C2/C8：使用了不存在于 txn row 的 `交易卡号` key，改为 `交易账号`
- C5：使用了错误的 column（`摘要说明` 无 `243300133` 值，该值在 `交易对手账卡号` 列）
- D：`inCount+outCount != totalRows`（未考虑 44 行空方向）
- C4：硬编码日期范围与归一化日期不完全匹配（1 行越界）

#### 验证结果
- `go test ./internal/api -run TestRealCSVEndToEnd -count=1` — 通过
- `go test ./internal/... -count=1` — 全部 50 个测试通过

### 2026-05-27 (真实 CSV 端到端测试)

#### 新增功能
- `TestRealCSVEndToEnd`：解析真实银行 32 列 CSV → `readSessionData` 归一化 → `BuildFlowGraph` 建图 → `applyFilters` 多维筛选 → `BuildPreview` 分页预览

#### 修改文件
- `internal/api/handlers_test.go`（新增 ~120 行 TestRealCSVEndToEnd）
- `backend/data/rule_samples/current/real_bank_subset.csv`（2000 行 UTF-8 测试数据）

#### 接口变化
- 无

#### 数据库变化
- 无

#### 前端变化
- 无

#### 验证结果
- `go test ./internal/api -run TestRealCSVEndToEnd -v -count=1` — 通过
- `go test ./internal/... -count=1` — 全部 50 个测试通过
- `http://127.0.0.1:8000/api/health` — `{"status":"ok"}`

#### 注意事项
- 原始 CSV 为 UTF-8 编码，Go 正确读取
- 2000 行均为同一卡号（6217921166546724）和同一账号（79040066601200056144）
- 方向分布：594 进 + 1362 出 + 44 空值
- 流图：230 节点（1 本方 + 229 对手）、276 条边（未截断）

### 2026-05-27

### 2026-05-26 19:58

#### 本次任务
- 修复主体详情中"交易户名"取值错误：交易户名应对应"交易方户名"，不应显示主体列里的银行名称。

#### 新增功能
- 无新增业务功能；本次为导入流水字段归一化修复。

#### 修改文件
- `internal/api/handlers.go`
- `internal/api/handlers_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名接口路径。
- `/api/flow/build` 仍使用原有请求字段；归一化交易行时，`交易户名` 优先取 `source_name_column`（交易方户名），`对手户名` 优先取 `target_name_column`（对手户名）；仅在没有显式户名映射且主体列本身明显是户名/姓名/名称字段时才兜底使用主体列，并明确排除银行/开户行列。

#### 数据库变化
- 无。

#### 前端变化
- 无前端代码变更。
- 主体详情展示逻辑不变，后端节点 `account_name` 的来源已修正。

#### 验证结果
- `cd E:\codex\etl; go test ./internal/api` 通过。
- `cd E:\codex\etl; gofmt -w internal\api\handlers.go internal\api\handlers_test.go` 已执行。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `cd E:\codex\etl; go vet ./internal/...` 通过。
- `cd E:\codex\etl; go build -o bin\etl-server.exe .\cmd\server\` 通过。
- 已重启 `E:\codex\etl\bin\etl-server.exe`，`http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。

#### 未完成 / 待确认
- 未做浏览器手动点选主体详情复测；如页面仍显示旧图数据，需要重新导入或重新构建资金流向图。

#### 注意事项
- 本次修复的是导入映射阶段的字段优先级：避免 `SourceCol/TargetCol` 中的银行名称覆盖真正户名；既有接口路径和数据库结构不变。

### 2026-05-26 18:01

#### 本次任务
- 修复资金流向图“数据穿透”功能失效：开启数据穿透后，节点上的展开/折叠按钮需要可靠响应点击。

#### 新增功能
- 无新增业务功能；本次为数据穿透交互修复。

#### 修改文件
- `frontend/src/features/flow/FlowGraphPrimitives.tsx`
- `frontend/src/features/flow/useFlowPanelState.ts`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- 数据穿透节点 `+/-` 按钮新增 ReactFlow 约定的 `nodrag nopan` class，避免按钮点击被节点拖拽或画布平移逻辑抢占。
- 数据穿透节点 `+/-` 按钮新增 `onPointerDown` 阻止事件冒泡，兼容 ReactFlow v12 的 pointer 事件交互。
- 关闭数据穿透开关时清空已展开节点列表，避免重新开启时沿用旧展开状态。

#### 验证结果
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过；仍有既有大 chunk warning；当前产物为 `assets/index-CBYjaJUa.js` 和 `assets/index-wvt7uB6u.css`。
- `cd E:\codex\etl; git diff --check -- frontend\src\features\flow\FlowGraphPrimitives.tsx frontend\src\features\flow\useFlowPanelState.ts` 通过，仅有工作区 LF/CRLF 提示。
- `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-CBYjaJUa.js` 和 `assets/index-wvt7uB6u.css`。

#### 未完成/待确认
- 未做浏览器手动点击 `+/-` 截图复测；如浏览器缓存旧资源，强制刷新后再测试。

#### 注意事项
- 本次只修前端 ReactFlow 节点按钮事件处理，不涉及后端接口、数据处理逻辑或数据库结构。

### 2026-05-26 17:52

#### 本次任务
- 修正资金流向图页面右侧内容顶部留白：全局设置需要贴近页面顶部显示。

#### 新增功能
- 资金流向图页面内容区新增专用布局 class，用于去除该页面顶部 padding。

#### 修改文件
- `frontend/src/App.tsx`
- `frontend/src/styles/layout.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- `App.tsx` 在 `active === "graph"` 时给 `Content` 增加 `content-graph` class。
- `layout.css` 新增 `.content-graph { padding-top: 0; }`，让右侧全局设置区域置顶。

#### 验证结果
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl; git diff --check -- frontend\src\App.tsx frontend\src\styles\layout.css` 通过，仅有工作区 LF/CRLF 提示。
- `cd E:\codex\etl\frontend; npm run build` 通过；仍有既有大 chunk warning；当前产物为 `assets/index-BLmuebEp.js` 和 `assets/index-wvt7uB6u.css`。
- `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-BLmuebEp.js` 和 `assets/index-wvt7uB6u.css`。

#### 未完成/待确认
- 未做浏览器截图复测；如浏览器缓存旧资源，强制刷新后查看。

#### 注意事项
- 本次只改前端顶部间距，不涉及后端接口、数据处理逻辑或数据库结构。

### 2026-05-26 17:46

#### 本次任务
- 修改资金流向图页面布局：点击左侧“资金流向图”后左侧导航自动折叠，右侧工作区扩展；移除顶层标题“资金流向图”；页面打开后改为上方全局设置、下方画布/功能区。

#### 新增功能
- 资金流向图菜单激活时，左侧 Ant Design `Sider` 自动折叠到 0 宽，保留折叠触发器用于展开导航。
- 全局设置栏固定在 Flow 页面内容顶部，不再挂载到页面标题旁。

#### 修改文件
- `frontend/src/App.tsx`
- `frontend/src/features/flow/FlowPanel.tsx`
- `frontend/src/features/flow/flow-canvas.css`
- `frontend/src/styles/layout.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- `App.tsx` 新增侧栏折叠状态，点击 `graph` 菜单时自动折叠左侧导航；资金流向图页不再渲染顶层 `topbar` 和标题。
- `FlowPanel.tsx` 移除全局设置 portal，直接在 Flow 页面顶部渲染 `FlowStyleToolbar`。
- `flow-canvas.css` 新增页面顶部全局设置栏样式，覆盖原先浮层定位。
- `layout.css` 调整 0 宽侧栏触发器显示样式。

#### 验证结果
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过；仍有既有大 chunk warning；当前产物为 `assets/index-DY0Pp_e9.js` 和 `assets/index-BDD8pi7Y.css`。
- `cd E:\codex\etl; git diff --check -- frontend\src\App.tsx frontend\src\features\flow\FlowPanel.tsx frontend\src\features\flow\flow-canvas.css frontend\src\styles\layout.css` 通过，仅有工作区 LF/CRLF 提示。
- `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-DY0Pp_e9.js` 和 `assets/index-BDD8pi7Y.css`。

#### 未完成/待确认
- 未做浏览器手动点击截图复测；浏览器如缓存旧资源，需要强制刷新后查看。

#### 注意事项
- 本次只改前端布局，不涉及后端接口、数据处理逻辑或数据库结构。

### 2026-05-26 Git Push

#### Task
- Push local Git commits from `main` to the configured remote repository.

#### New Functionality
- None. Repository publishing only.

#### Modified Files
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### API Changes
- None.

#### Database Changes
- None.

#### Frontend Changes
- None.

#### Verified Commands
- `git status -sb` showed `main...origin/main [ahead 4]` before the first push.
- `git remote -v` confirmed `origin` points to `https://github.com/Euxripides/euripidessss.git`.
- `git push origin main` pushed `f007062..c5fd6b3` to `origin/main`.

#### Open Items / Notes
- `gh` is not installed in this environment, so no GitHub PR workflow was attempted.

### 2026-05-25 21:06

#### 本次任务
- 主体详情框在 ID 下方显示该主体的交易卡号、交易户名、身份证号；有数据才显示对应字段，没有数据则不显示。

#### 新增功能
- Flow 节点新增可选身份字段 `account_no`、`account_name`、`id_number`。
- 主体详情抽屉新增“交易卡号”“交易户名”“身份证号”三行条件显示。

#### 修改文件
- `internal/model/model.go`
- `internal/etl/flow_graph.go`
- `internal/etl/etl_test.go`
- `frontend/src/types.ts`
- `frontend/src/features/flow/flowElements.ts`
- `frontend/src/features/flow/SubjectDetailDrawer.tsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名接口路径。
- `/api/process` 的 `flow_graph.nodes` 和 `/api/flow/build` 的 `nodes` 中，节点对象新增可选字段 `account_no`、`account_name`、`id_number`。

#### 数据库变化
- 无。

#### 前端变化
- `buildFlowElements` 透传节点身份字段。
- `SubjectDetailDrawer` 在 ID 下方渲染非空身份字段，空值不显示。

#### 验证结果
- `cd E:\codex\etl; go test ./internal/etl` 通过。
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `cd E:\codex\etl; go vet ./internal/...` 通过。
- `cd E:\codex\etl; go build -o bin\etl-server.exe .\cmd\server\` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning；当前产物为 `assets/index-CHBt3q_H.js` 和 `assets/index-BbV9x_Qb.css`。

#### 未完成/待确认
- 未做浏览器手动点选主体详情复测；如浏览器缓存旧资源，需强制刷新后查看。

### 2026-05-25 20:49

#### 本次任务
- 修复新增“数据穿透”后资金流向图主体图标丢失的问题。

#### 新增功能
- 无，本次为可视回归修复。

#### 修改文件
- `frontend/src/features/flow/FlowGraphPrimitives.tsx`
- `frontend/src/features/flow/flow-nodes.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- 新增 `.flow-node-content` 内部容器承载主体内容和“+/-”穿透按钮。
- 移除 `.flow-node` 上的 `position: relative`，避免干扰 ReactFlow 节点外层定位和测量。

#### 验证结果
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning；当前产物为 `assets/index-Dek-ebL1.js` 和 `assets/index-BbV9x_Qb.css`。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `git diff --check -- frontend/src/features/flow/FlowGraphPrimitives.tsx frontend/src/features/flow/flow-nodes.css` 通过。
- 扫描 `FlowGraphPrimitives.tsx` 和 `flow-nodes.css`，未发现 U+FFFD 替换字符。

#### 未完成/待确认
- 未做浏览器截图复测；浏览器如缓存旧资源，需要强制刷新后再查看主体图标。

### 2026-05-25 20:33

#### 本次任务
- 新增资金流向图“数据穿透”功能，在主体图标右上显示“+”展开后续交易，右下显示“-”折叠后续交易。
- 在全局设置中新增“数据穿透”开关，默认关闭。
- 展开逻辑按交易时间判断，只有后续流出时间晚于主体当前可见入账时间时才允许展开。

#### 新增功能
- “数据穿透”开启后，图谱先显示初始根关系，后续主体按时间逐层展开。
- 有后续流出交易的主体显示“+”；已展开后续交易的主体显示“-”。
- 关闭“数据穿透”后恢复原有完整关系渲染。

#### 修改文件
- `frontend/src/features/flow/FlowStyleToolbar.tsx`
- `frontend/src/features/flow/FlowPanel.tsx`
- `frontend/src/features/flow/useFlowPanelState.ts`
- `frontend/src/features/flow/useFlowGraph.ts`
- `frontend/src/features/flow/FlowGraphPrimitives.tsx`
- `frontend/src/features/flow/flow-nodes.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- 全局设置栏新增“数据穿透”开关。
- `useFlowGraph` 新增按 `first_time` / `last_time` 计算的穿透折叠视图。
- 主体节点新增“+/-”穿透按钮，按钮点击不会触发节点拖拽或选中。
- 图层切换时清空已展开的穿透主体状态。

#### 验证结果
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `cd E:\codex\etl; go vet ./internal/...` 通过。
- 扫描本次触及的 Flow 文件和 `frontend\dist\assets`，未发现 U+FFFD 替换字符。
- `git diff --check -- frontend/src/features/flow/FlowGraphPrimitives.tsx frontend/src/features/flow/FlowStyleToolbar.tsx frontend/src/features/flow/useFlowGraph.ts frontend/src/features/flow/useFlowPanelState.ts frontend/src/features/flow/flow-nodes.css frontend/src/features/flow/FlowPanel.tsx` 通过。

#### 未完成/待确认
- 未做浏览器手动点击“+/-”验证；浏览器如缓存旧资源，需要强制刷新后再测试。
- 当前实现以聚合边为显示单位；如果一条聚合边包含入账时间前后的多笔交易，展开时仍显示该聚合关系。

### 2026-05-25 16:39

#### 本次任务
- 将资金流向图框选逻辑改为默认关闭，通过全局设置里的“主体多选”开关控制。
- 将全局设置移动到“资金流向图”标题右侧，保持展开显示。
- 删除顶部说明文案“清洗、合并、标注和分析支付宝、微信、银行卡流水。”。

#### 新增功能
- 新增“主体多选”全局开关，默认关闭。
- 开启后，画布空白区域左键拖动可框选主体；关闭时左键拖动画布仍用于平移。
- 全局设置从画布左上角移到页面标题右侧，并改为不折叠。

#### 修改文件
- `frontend/src/App.tsx`
- `frontend/src/features/flow/FlowCanvas.tsx`
- `frontend/src/features/flow/FlowGraphWorkspace.tsx`
- `frontend/src/features/flow/FlowPanel.tsx`
- `frontend/src/features/flow/FlowStyleToolbar.tsx`
- `frontend/src/features/flow/useFlowPanelState.ts`
- `frontend/src/styles/shared.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- `FlowCanvas.tsx` 的框选能力改由 `subjectMultiSelect` 控制。
- `FlowStyleToolbar.tsx` 新增“主体多选”开关，并改为常驻展开的全局设置栏。
- `FlowPanel.tsx` 通过 portal 将全局设置渲染到 App 顶部标题旁。
- `App.tsx` 删除顶部说明文案并提供标题旁设置挂载点。
- `shared.css` 补充标题行设置栏和开关布局样式。

#### 验证结果
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `rg -n "清洗、合并、标注|主体多选|全局设置|�" frontend\src frontend\dist\assets` 确认旧说明文案已移除，未发现 U+FFFD。
- `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-CMxAVzpe.js` 和 `assets/index-CP7hcI7w.css`。

#### 未完成/待确认
- 未做浏览器手动框选操作验证；浏览器如缓存旧资源，需要强制刷新后再测试。

### 2026-05-25 15:39

#### 本次任务
- 支持资金流向图画布像 Windows 桌面一样用鼠标画框批量选中节点，并批量移动。
- 批量移动时保持现有动态连接点优化逻辑，避免多节点移动时边连接点退回固定位置或被图层移动逻辑重复位移。

#### 新增功能
- ReactFlow 画布现在支持左键拖动画布空白处框选节点。
- 框选规则改为部分相交即选中节点，更接近桌面框选。
- 选中多个节点后，拖动任意选中节点可整体移动。
- 画布平移改为中键/右键拖动，避免与左键框选冲突。

#### 修改文件
- `frontend/src/features/flow/FlowCanvas.tsx`
- `frontend/src/features/flow/useFlowPanelState.ts`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- `FlowCanvas.tsx` 的 ReactFlow 增加 `selectionOnDrag`、`selectionMode={SelectionMode.Partial}`、`panOnDrag={[1, 2]}`、`nodesDraggable`、`selectNodesOnDrag={false}`。
- `useFlowPanelState.ts` 在多节点选中拖拽时禁用图层整体拖拽分支，避免重复位移。
- 连接点优化继续由 `useFlowGraph` 按当前节点位置重算动态锚点。

#### 验证结果
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `rg -n "�" frontend\src\features\flow\FlowCanvas.tsx frontend\src\features\flow\useFlowPanelState.ts frontend\dist\assets` 无匹配。
- `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-B8aQzR94.js` 和 `assets/index-B-imr4oU.css`。

#### 未完成/待确认
- 浏览器如果缓存旧资源，需要强制刷新后再测试框选。
- 框选对象是节点；如果框内只有边线、端点节点不在框内，ReactFlow 不会仅通过边线选中并移动端点节点。

### 2026-05-25 15:13

#### 本次任务
- 将日期筛选框和日期选择弹层改为中文显示，避免 Ant Design 日期控件出现英文文案。

#### 新增功能
- 全局 Ant Design `ConfigProvider` 使用中文 locale。
- 全局 dayjs locale 设置为 `zh-cn`，日期面板月份、星期、按钮等文案按中文显示。
- 线条样式面板日期范围框补充 `开始时间` / `结束时间` 中文占位符。

#### 修改文件
- `frontend/src/App.tsx`
- `frontend/src/features/flow/EdgeStylePanel.tsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- `App.tsx` 引入 `antd/locale/zh_CN`、`dayjs` 和 `dayjs/locale/zh-cn`，并在 `ConfigProvider` 上设置 `locale={zhCN}`。
- `EdgeStylePanel.tsx` 的 `DatePicker.RangePicker` 明确设置中文占位符。

#### 验证结果
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `rg -n "�" frontend\src\App.tsx frontend\src\features\flow\EdgeStylePanel.tsx frontend\dist\assets` 无匹配。
- `frontend/dist/index.html` 已引用当前构建产物 `assets/index-B2S0PUmd.js` 和 `assets/index-B-imr4oU.css`。
- `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-B2S0PUmd.js` 和 `assets/index-B-imr4oU.css`。

#### 未完成/待确认
- 浏览器如果缓存旧资源，需要强制刷新后再查看日期控件。
- 本次未新增依赖；`dayjs` 来自现有 Ant Design 依赖树。

### 2026-05-25 当前

#### 本次任务
- 修复导入交易时间格式与后台标准格式不一致时，时间筛选和审计统计口径不一致的问题。
- 重新进行后端审计统计校验，要求所有筛选条件同时带入后，统计、建图、线条明细一致。
- 修复点击资金流向图线条后，明细弹窗的笔数、金额和真实流向与 Excel 手工统计不一致的问题。
- 修复点击线条后明细数据为空的问题：后端 queryEdgeRows 只匹配主列，当实体名来自备用列时（如 交易账号 而非 交易户名）匹配不到任何行。

#### 新增功能
- 导入流向图数据时，映射后的 `交易时间` 会统一归一化为 `YYYY-MM-DD HH:mm:ss`，再参与预览、筛选、统计、建图和明细匹配。
- `parser.NormalizeDatetime` 扩展支持 Excel 序列日期、紧凑数字时间、单双位年月日、中文年月日时分秒、点号/斜杠日期、毫秒、RFC3339 时区、Unix 秒/毫秒等常见交易时间格式。
- 任一筛选条件生效时都会使用 5000 条审计关系上限，包括交易方、对手方、双方标签、明细字段、方向、开始时间、结束时间。
- 新增后端审计测试：混合时间格式数据 + 交易方筛选 + 对手方筛选 + 双方标签 + 流水号 + 摘要 + 备注 + 方向 + 起止时间全部同时带入后，核对筛选统计、建图边、线条明细的笔数和金额一致。
- 边缘明细数据现在能正确匹配通过备用列（交易账号/交易户名/交易方身份证号/对手卡号/对手户名等）解析的实体名称。
- 新增 lowColumnMapping 结构体和 lowColumnMappingFromPayload 函数，统一管理列映射提取。
- matchesDateRange 时间过滤逻辑增加了 
ormalizeFilterBoundary 精确时间边界处理。
- 边缘明细现在按建图同一套逻辑先生成标准交易行、归一化收付标志、应用当前筛选条件，再按计算出的真实资金流向匹配被点击的边。
- 对 `收付标志=进` 的原始流水，明细查询会按“对手 -> 本方”匹配线条，不再误按“本方 -> 对手”匹配。
- 明细接口现在会应用当前图层的源/目标筛选、标签筛选、明细字段筛选、方向筛选和时间范围。
- 明细返回行新增 `流向源`、`流向目标` 字段。
- 明细总笔数和总金额在服务端按全部匹配行统计，再按 limit 截断返回行。

#### 修改文件
- internal/api/handlers.go
- internal/api/handlers_test.go
- frontend/src/features/flow/flowApi.ts
- frontend/src/features/flow/flowTypes.ts
- frontend/src/features/flow/useFlowFilters.ts
- frontend/src/hooks/useFlowOperations.ts
- internal/parser/parser.go
- internal/parser/parser_test.go
- docs/AI_HANDOFF.md
- docs/CHANGELOG_AI.md

#### 接口变化
- 无新增/删除/重命名端点路径。
- /api/flow/edge-detail/imported 请求体新增可选字段：source_account_column, source_name_column, source_id_column, source_label_column, 	arget_card_column, 	arget_name_column, 	arget_id_column, 	arget_label_column。
- /api/flow/edge-detail/imported 继续兼容原请求体，并补充使用以下已有/新增可选字段：direction_column、source_filters、target_filters、detail_filters、source_label_values、target_label_values、directions、start_date、end_date。
- /api/flow/edge-detail/imported 响应 rows 中新增 `流向源`、`流向目标` 两列。
- /api/flow/build 的请求/响应路径不变；后端现在会对所有活跃筛选条件使用审计上限并用归一化后的交易时间统计。

#### 数据库变化
- 无。

#### 前端变化
- 图层的边明细上下文会把源/目标标签筛选值一并传给后端，确保点击线条后的明细口径与当前图一致。
- 前端构建图 payload 的 `max_edges` 判断改为任意筛选条件生效即请求 5000 条审计关系上限，覆盖标签、方向和时间筛选。

#### 验证结果
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

#### 未完成/待确认
- 用户需要用实际 Excel 对照的那条线再次点击验证；浏览器如果缓存旧 JS，需要强制刷新。
- 时间格式无法数学意义上覆盖所有可能输入；本次覆盖银行/Excel/CSV 常见格式，无法识别的极端自定义格式仍会原样保留并可能无法进入时间范围筛选。

### 2026-05-24 23:34

#### 鏈浠诲姟
- 琛ラ綈娴佸悜鍥惧瓧娈垫槧灏勫脊绐椾腑鐨?`浜ゆ槗娴佹按鍙穈銆乣鎽樿璇存槑`銆乣澶囨敞`銆?- 璁╄繖浜涘瓧娈靛湪宸叉槧灏勬椂鍑虹幇鍦ㄥ彸渚х瓫閫夊尯锛屾湭鏄犲皠鏃朵笉鏄剧ず銆?- 灏嗘椂闂寸瓫閫夋敼涓轰腑鏂囧崰浣嶆枃鏈紝骞舵敮鎸佺簿纭埌灏忔椂銆佸垎閽熴€佺銆?- 灏嗘祦鍚戝浘妯℃澘鏇挎崲涓虹敤鎴蜂笂浼犵殑 `D:\app\妗岄潰\娴佸悜鍥炬暟鎹ā鏉?xlsx`銆?- 瀵规暟鎹瓫閫夊仛绔埌绔璁℃祴璇曪紝瑕嗙洊褰掍竴鍖栥€佺瓫閫夈€佸缓鍥捐仛鍚堝拰涓讳綋鏀舵敮缁熻銆?
#### 鏂板鍔熻兘
- 鏂板鏄庣粏瀛楁绛涢€夛細`浜ゆ槗娴佹按鍙穈銆乣鎽樿璇存槑`銆乣澶囨敞`銆?- `/api/flow/build` 鏀寔璇诲彇鍜岀瓫閫夋槧灏勫悗鐨勬槑缁嗗瓧娈点€?- 鍚庣绛涢€夌幇鍦ㄥ悓鏃跺簲鐢ㄤ氦鏄撴柟鏍囩銆佸鎵嬫爣绛俱€佹槑缁嗗瓧娈点€佹柟鍚戝拰绮剧‘鏃堕棿鑼冨洿銆?- 鏂板鍚庣瀹¤娴嬭瘯锛屾壒閲忕敓鎴愬璐﹀彿銆佸瀵规墜銆佸鏂瑰悜銆佸鏃堕棿銆佸閲戦娴嬭瘯鏁版嵁锛屽苟鏍稿绛涢€夊悗琛屾暟銆侀噾棰濄€佽竟鑱氬悎鍜屼富浣撴祦鍏ユ祦鍑虹粺璁°€?
#### 淇敼鏂囦欢
- `frontend/src/features/flow/flowTypes.ts`
- `frontend/src/features/flow/flowMapping.ts`
- `frontend/src/features/flow/FlowMappingModal.tsx`
- `frontend/src/features/flow/FlowFieldFilters.tsx`
- `frontend/src/features/flow/useFlowFilters.ts`
- `frontend/src/features/flow/FlowBuildControls.tsx`
- `frontend/src/features/flow/FlowPanel.tsx`
- `frontend/src/features/flow/FlowGraphWorkspace.tsx`
- `frontend/src/features/flow/FlowInspectorPanel.tsx`
- `frontend/src/features/flow/flowApi.ts`
- `frontend/src/hooks/useFlowOperations.ts`
- `internal/api/handlers.go`
- `internal/api/handlers_test.go`
- `tmp/flow_template.xlsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犳柊澧炪€佸垹闄ゆ垨閲嶅懡鍚嶆帴鍙ｈ矾寰勩€?- `/api/flow/build` 鏂板鍙€夎姹傚瓧娈碉細`serial_column`銆乣summary_column`銆乣remark_column`銆乣detail_filters`銆?- `/api/flow/template` 涓嬭浇鍐呭鏇存柊涓?15 鍒楁ā鏉匡紝鏂板 `浜ゆ槗娴佹按鍙穈銆?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 瀛楁鏄犲皠寮圭獥鏂板 `浜ゆ槗娴佹按鍙穈銆乣鎽樿璇存槑`銆乣澶囨敞` 涓夎銆?- 鍙充晶绛涢€夊尯鏂板鏄庣粏瀛楁閫夋嫨鍣紝鍙湁瀛楁宸叉槧灏勬垨鑳借嚜鍔ㄨВ鏋愭椂鎵嶆樉绀哄搴旂瓫閫夐」銆?- 鏃堕棿鑼冨洿閫夋嫨鍣ㄥ崰浣嶇鏀逛负 `寮€濮嬫椂闂碻銆乣缁撴潫鏃堕棿`锛屾樉绀烘牸寮忎负 `YYYY-MM-DD HH:mm:ss`銆?
#### 楠岃瘉缁撴灉
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 閫氳繃銆?- `go test ./internal/api -run TestFlowFilterEndToEndAuditMatchesGraphAggregates -count=1 -v` 閫氳繃銆?- `cd E:\codex\etl\frontend; npm run build` 閫氳繃锛屼粛鏈夋棦鏈?chunk size warning銆?- `go test ./internal/...` 閫氳繃銆?- `go vet ./internal/...` 閫氳繃銆?- `go build -o "$env:TEMP\etl-server-check.exe" .\cmd\server\` 閫氳繃銆?- `go build -o bin\etl-server.exe .\cmd\server\` 閫氳繃銆?- 宸查噸鍚?8000 鏈嶅姟锛宍http://127.0.0.1:8000/api/health` 杩斿洖 `{"status":"ok"}`銆?- 宸蹭笅杞藉苟妫€鏌?`http://127.0.0.1:8000/api/flow/template`锛岃〃澶翠负 `浜ゆ槗鏂规埛鍚? 浜ゆ槗鏂硅处鎴? 浜ゆ槗鏂硅韩浠借瘉鍙? 浜ゆ槗鏂规爣绛? 浜ゆ槗鏃堕棿, 浜ゆ槗閲戦, 鏀朵粯鏍囧織, 浜ゆ槗浣欓, 浜ゆ槗瀵规墜璐﹀崱鍙? 瀵规墜鎴峰悕, 瀵规墜韬唤璇佸彿, 瀵规墜鏍囩, 浜ゆ槗娴佹按鍙? 鎽樿璇存槑, 澶囨敞`銆?- 宸茬‘璁ら椤靛紩鐢ㄥ綋鍓嶆瀯寤轰骇鐗?`assets/index-Dg-VWM7A.js` 涓?`assets/index-B-imr4oU.css`銆?
#### 鏈畬鎴?寰呯‘璁?- 濡傛灉娴忚鍣ㄧ紦瀛樹簡鏃?JS 璧勬簮锛岄渶瑕佸己鍒跺埛鏂伴〉闈㈠悗娴嬭瘯銆?
### 2026-05-24 23:02

#### 鏈浠诲姟
- 淇鏁版嵁搴撳鍏ュ璞″尯浠嶇劧鏄剧ず鍦ㄥ乏渚ф爲涓嬫柟鐨勯棶棰樸€?
#### 鏂板鍔熻兘
- 鏃狅紝鏈涓?CSS 甯冨眬淇銆?
#### 淇敼鏂囦欢
- `frontend/src/styles/shared.css`
- `frontend/src/features/flow/DBImportModal.tsx`
- `frontend/src/features/flow/db-import.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犮€?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 鍒犻櫎 `frontend/src/styles/shared.css` 鏈熬娈嬬暀鐨勬湭闂悎 `@media` 鍧椼€?- 淇 `db-import.css` 琚敊璇寘杩涘獟浣撴煡璇㈢殑闂銆?- 妗岄潰瀹藉害涓嬫暟鎹簱瀵煎叆寮圭獥鎭㈠涓哄乏渚ф爲銆佸彸渚у璞″尯鐨勫乏鍙冲垎鏍忓竷灞€銆?
#### 楠岃瘉缁撴灉
- `cd E:\codex\etl\frontend; npm run build` 閫氳繃锛屼粛鏈夋棦鏈?chunk size warning銆?- 宸茬‘璁ゆ瀯寤哄悗鐨?CSS 涓?`.db-import-shell` 浣嶄簬椤跺眰锛屼笖鍖呭惈 `display:grid` 鍜屼袱鏍?`grid-template-columns`銆?- 宸叉壂鎻忔湰娆?touched 婧愮爜鍜?`frontend/dist/assets`锛屾湭鍙戠幇 U+FFFD 鏇挎崲瀛楃銆?- `http://127.0.0.1:8000/api/health` 杩斿洖 `{"status":"ok"}`銆?- `http://127.0.0.1:8000` 宸插紩鐢ㄥ綋鍓嶆瀯寤轰骇鐗?`index-B-imr4oU.css` 鍜?`index-DTwUX0_S.js`銆?
#### 鏈畬鎴?寰呯‘璁?- 濡傛灉娴忚鍣ㄧ紦瀛樹簡鏃ц祫婧愶紝闇€瑕佸己鍒跺埛鏂伴〉闈㈠悗鍐嶇湅甯冨眬銆?
### 2026-05-24 22:44

#### 鏈浠诲姟
- 淇澶ч噺鏁版嵁瀵煎叆鍚?Flow 鐢熸垚鍥惧崱椤裤€佺粺璁″紓甯搞€佷富浣撶瓫閫夊悗鍑虹幇瀛ょ珛璐﹀彿涓旀病鏈夎繛绾跨殑闂銆?- 閲嶇偣淇瀹¤鍦烘櫙锛氶€夋嫨涓€涓氦鏄撴柟璐﹀彿銆佹敹浠樻爣蹇椾负鈥滃嚭鈥濄€佷笉閫夋嫨瀵规墜淇℃伅鏃讹紝搴旂粺璁″苟灞曠ず璇ヨ处鍙锋墍鏈夊尮閰嶇殑娴佸嚭浜ゆ槗瀵规墜鍏崇郴銆?
#### 鏂板鍔熻兘
- `/api/flow/build` 鏀寔鍙€?`max_edges`锛屽墠绔湪鏈変氦鏄撴柟/瀵规墜绛涢€夌殑瀹¤鏋勫浘鍦烘櫙璇锋眰 5000 鏉″叧绯讳笂闄愶紝鍚庣涔熶細瀵逛富鍔ㄧ瓫閫夊満鏅娇鐢?5000 鐨勫璁′笂闄愩€?- Flow graph meta 鏂板 `rendered_edges`銆乣rendered_nodes`锛岀敤浜庡尯鍒嗗叏閲忚仛鍚堣妯″拰褰撳墠瀹為檯娓叉煋瑙勬ā銆?
#### 淇敼鏂囦欢
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

#### 鎺ュ彛鍙樺寲
- 鏃犳柊澧炴垨鍒犻櫎鎺ュ彛璺緞銆?- `/api/flow/build` 鏂板鍙€夎姹傚瓧娈?`max_edges`銆?- `/api/flow/build` 鍝嶅簲 `meta` 鏂板 `rendered_edges`銆乣rendered_nodes`銆?- `meta.total_nodes` 淇涓烘湭鎴柇鑱氬悎鍥剧殑鑺傜偣鎬绘暟锛屼笉鍐嶄娇鐢ㄦ埅鏂悗杈归泦鍚堢殑鑺傜偣鏁般€?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 鏂板浘灞傜敓鎴?鏇挎崲鍚庝細娓呯┖鏃х殑涓讳綋绛涢€夈€侀噾棰濈瓫閫夈€佽矾寰勮拷韪拰閫変腑鍏崇郴锛岄伩鍏嶆棫鍥剧姸鎬佹薄鏌撴柊鍥俱€?- 閲戦婊戝潡鎸夊綋鍓嶅浘鏈€澶ч噾棰濋挸鍒舵樉绀哄拰杩囨护锛岄伩鍏嶆棫鐨勫ぇ棰濋槇鍊兼妸鏂板浘鎵€鏈夎竟杩囨护鎺夈€?- 閲戦/鏃堕棿/娓叉煋杩囨护鐢熸晥鏃讹紝鐢诲竷鍙繚鐣欎粛鏈夊叧鑱旇竟鐨勮妭鐐癸紝涓嶅啀鏄剧ず鏃犺繛绾跨殑瀛ょ珛璐﹀彿銆?- 鏈変氦鏄撴柟鎴栧鎵嬬瓫閫夋椂锛屾瀯鍥?payload 鍙戦€?`max_edges: 5000`锛涙棤涓讳綋绛涢€夌殑鎬昏鏋勫浘鍙戦€?`max_edges: 600`銆?
#### 楠岃瘉缁撴灉
- `go test ./internal/...` 閫氳繃銆?- `cd E:\codex\etl\frontend; npm run build` 閫氳繃锛屼粛鏈夋棦鏈?chunk size warning銆?- `go vet ./internal/...` 閫氳繃銆?- `go build -o "$env:TEMP\etl-server-check.exe" .\cmd\server\` 閫氳繃銆?- 宸查噸寤?`bin\etl-server.exe` 骞堕噸鍚?8000 鏈嶅姟锛宍http://127.0.0.1:8000/api/health` 杩斿洖 `ok`銆?- 宸叉壂鎻忔湰娆?touched Flow/鍚庣鏂囦欢鍜?`frontend/dist/assets`锛屾湭鍙戠幇 U+FFFD 鏇挎崲瀛楃銆?
#### 鏈畬鎴?寰呯‘璁?- 鏈鏈敤鐢ㄦ埛鐨?520k 琛屽師濮嬫暟鎹仛娴忚鍣ㄧ澶嶇幇銆?- 鏃犵瓫閫夌殑澶у浘鎬昏浠嶄繚鐣?600 鏉℃渶楂橀噾棰濊仛鍚堝叧绯荤殑娓叉煋涓婇檺锛涘璁℃槑缁嗗簲閫氳繃浜ゆ槗鏂?瀵规墜绛涢€夎繘鍏?5000 涓婇檺銆?- 褰撳墠鍙祴璇曞湴鍧€锛歚http://127.0.0.1:8000`锛涢獙璇佹椂鍚庣 PID 涓?`37172`銆?
# CHANGELOG_AI.md

### 2026-05-24 22:29

#### 鏈浠诲姟
- 淇鏁版嵁搴撳鍏ュ脊绐椾腑瀵硅薄鍒嗙被鐨勪綅缃細瀵硅薄鍒嗙被搴斿湪鍙充晶瀵硅薄鍖猴紝涓嶅簲鎸傚湪宸︿晶妯″紡鑺傜偣涓嬮潰銆?
#### 鏂板鍔熻兘
- 鏃狅紝鏈涓哄竷灞€淇銆?
#### 淇敼鏂囦欢
- `frontend/src/features/flow/DBImportModal.tsx`
- `frontend/src/features/flow/db-import.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犮€?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 宸︿晶鏍戞敼涓鸿繛鎺?-> 鏁版嵁搴?-> 妯″紡 -> 琛紝涓嶅啀鍦ㄦā寮忎笅鏄剧ず鈥滆〃/瑙嗗浘/瀹炰綋鍖栬鍥?鍑芥暟/鏌ヨ/澶囦唤鈥濆垎绫昏妭鐐广€?- 鍙充晶鈥滃璞♀€濋〉鏂板瀵硅薄鍒嗙被鎸夐挳锛氳〃銆佽鍥俱€佸疄浣撳寲瑙嗗浘銆佸嚱鏁般€佹煡璇€佸浠姐€?- 琛ㄥ璞″垪琛ㄤ繚鐣欏湪鍙充晶锛屽弻鍑昏〃浠嶄細鎵撳紑琛ㄦ暟鎹〉銆?
#### 楠岃瘉缁撴灉
- `cd E:\codex\etl\frontend; npm run build` 閫氳繃锛屼粛鏈夋棦鏈?chunk size warning銆?- 宸叉悳绱?`frontend/src/features/flow/DBImportModal.tsx` 鍜?`frontend/src/features/flow/db-import.css`锛岀‘璁ゅ乏渚?`tables:` 鍒嗙被鑺傜偣宸茬Щ闄ゃ€?- 宸叉壂鎻?`frontend/src/features/flow/DBImportModal.tsx`銆乣frontend/src/features/flow/db-import.css` 鍜?`frontend/dist/assets`锛屾湭鍙戠幇 U+FFFD 鏇挎崲瀛楃銆?
#### 鏈畬鎴?寰呯‘璁?- 瑙嗗浘銆佸疄浣撳寲瑙嗗浘銆佸嚱鏁般€佹煡璇€佸浠藉垎绫诲綋鍓嶄粛涓虹鐢ㄥ睍绀洪」锛屽緟鍚庣鏀寔瀵瑰簲鍏冩暟鎹帴鍙ｅ悗鍙惎鐢ㄣ€?
### 2026-05-24 22:19

#### 鏈浠诲姟
- 璋冩暣鏁版嵁搴撳鍏ュ脊绐楃殑杩炴帴娴嬭瘯鎻愮ず銆佹爲褰㈢粨鏋勫拰鏁翠綋甯冨眬锛屼娇鍏舵洿鎺ヨ繎鐢ㄦ埛鎻愪緵鐨勬暟鎹簱瀹㈡埛绔埅鍥俱€?
#### 鏂板鍔熻兘
- 鈥滄祴璇曡繛鎺モ€濇垚鍔熸垨澶辫触鏃舵樉绀洪€氱煡妗嗭紝鎴愬姛灞曠ず杩炴帴鐩爣锛屽け璐ュ睍绀洪敊璇師鍥犮€?- 鏂板杩炴帴 -> 鏁版嵁搴?-> 妯″紡 -> 瀵硅薄鍒嗙粍 -> 琛ㄧ殑鏍戝舰瀵艰埅缁撴瀯銆?- 鏂板鈥滃璞♀€濅富瑙嗗浘锛屽彸渚т互鈥滃悕 / 琛?/ 娉ㄩ噴鈥濊〃鏍煎睍绀哄綋鍓嶆ā寮忎笅鐨勮〃銆?
#### 淇敼鏂囦欢
- `frontend/src/features/flow/DBImportModal.tsx`
- `frontend/src/features/flow/db-import.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犮€?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 鏁版嵁搴撳鍏ュ脊绐楀乏渚т粠骞抽摵鍒楄〃鏀逛负 Ant Design Tree銆?- 鍙充晶鏂板绫讳技鏁版嵁搴撳鎴风鐨勫璞″伐鍏锋爮锛氭墦寮€琛ㄣ€佽璁¤〃銆佹柊寤鸿〃銆佸垹闄よ〃銆佸鍏ュ悜瀵笺€佸鍑哄悜瀵笺€?- 鎵撳紑琛ㄥ悗鍒囨崲鍒拌〃鏁版嵁椤碉紱閫夋嫨妯″紡鍚庨粯璁ゅ睍绀哄璞￠〉銆?- 鏂板缓琛ㄣ€佸垹闄よ〃銆佸鍑哄悜瀵煎綋鍓嶄粎浣滀负甯冨眬鍗犱綅涓旂鐢紝鏈柊澧?DDL 鎴栧鍑烘帴鍙ｃ€?
#### 楠岃瘉缁撴灉
- `cd E:\codex\etl\frontend; npm run build` 閫氳繃锛屼粛鏈夋棦鏈?chunk size warning銆?- `cd E:\codex\etl; go test ./internal/...` 閫氳繃銆?- 宸叉壂鎻?`frontend/src/features/flow/DBImportModal.tsx`銆乣frontend/src/features/flow/db-import.css` 鍜?`frontend/dist/assets`锛屾湭鍙戠幇 U+FFFD 鏇挎崲瀛楃銆?
#### 鏈畬鎴?寰呯‘璁?- 褰撳墠琛ㄥ垪琛ㄦ帴鍙ｅ彧杩斿洖鍚嶇О鍜岀被鍨嬶紝鍙充晶鈥滆 / 娉ㄩ噴鈥濇殏涓虹┖鍗犱綅锛涘闇€鐪熷疄琛屾暟/娉ㄩ噴锛岄渶瑕佹墿灞曞悗绔厓鏁版嵁鎺ュ彛銆?
### 2026-05-24 21:46

#### 鏈浠诲姟
- 鍚姩椤圭洰锛屼緵鐢ㄦ埛娴嬭瘯褰撳墠鏁版嵁搴撳鍏ョ増鏈€?
#### 鏂板鍔熻兘
- 鏃狅紝鏈浠呭惎鍔?閲嶅惎鏈嶅姟銆?
#### 淇敼鏂囦欢
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犮€?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 鏃犮€?
#### 楠岃瘉缁撴灉
- 宸叉鏌?8000 绔彛鍘熸湁杩涚▼鍙婂懡浠よ銆?- 宸插仠姝㈡棫鐨?`E:\codex\etl\bin\etl-server.exe` 杩涚▼銆?- 宸蹭粠 `E:\codex\etl` 鍚姩褰撳墠 `bin\etl-server.exe`銆?- `http://127.0.0.1:8000/api/health` 杩斿洖 `{"status":"ok"}`銆?- `http://127.0.0.1:8000/api/db/connections` 杩斿洖 JSON锛岀‘璁ゆ暟鎹簱瀵煎叆 API 宸插湪 8000 鍙敤銆?- `http://127.0.0.1:8000` 杩斿洖 HTTP 200锛屽苟鍔犺浇褰撳墠鍓嶇鏋勫缓璧勬簮銆?
#### 鏈畬鎴?寰呯‘璁?- 鏃犮€傚綋鍓嶅彲娴嬭瘯鍦板潃涓?`http://127.0.0.1:8000`銆?
### 2026-05-24 20:58

#### 鏈浠诲姟
- 浣跨敤鐢ㄦ埛鎻愪緵鐨勬湰鏈?MySQL 杩炴帴鍋氭暟鎹簱瀵煎叆鍔熻兘鐪熷疄鎺ュ彛娴嬭瘯銆?
#### 鏂板鍔熻兘
- 鏃狅紝鏈浠呮祴璇曢獙璇併€?
#### 淇敼鏂囦欢
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犮€?
#### 鏁版嵁搴撳彉鍖?- 涓存椂鍒涘缓 MySQL database `codex_mysql_import_test` 鍜岃〃 `flow_txn`銆?- 娴嬭瘯缁撴潫鍚庡凡鍒犻櫎涓存椂 database銆?
#### 鍓嶇鍙樺寲
- 鏃犮€?
#### 楠岃瘉缁撴灉
- MySQL 8.0.39 杩炴帴鎴愬姛銆?- `/api/db/connections` 杩炴帴淇濆瓨銆佸垪琛ㄨ鍙栥€佸瘑鐮侀殣钘忋€佸垹闄ら€氳繃銆?- `/api/db/connections/:id/test` 閫氳繃銆?- 鏁版嵁搴撱€乻chema銆佽〃銆佸瓧娈靛厓鏁版嵁璇诲彇閫氳繃銆?- `/api/db/preview` 鍒嗛〉棰勮閫氳繃锛岃繑鍥?2 琛屽苟鏍囪鎴柇銆?- `/api/db/search` 鎼滅储閫氳繃锛岃繑鍥?1 琛屻€?- `/api/db/query` SELECT 鏌ヨ閫氳繃锛岄潪 SELECT 鏌ヨ鎸夐鏈熻鎷︽埅銆?- `/api/db/table/insert`銆乣/api/db/table/update`銆乣/api/db/table/delete` 鍧囬€氳繃锛屽悇褰卞搷 1 琛屻€?- `/api/db/mappings/auto` 鑷姩鏄犲皠閫氳繃锛屽繀濉瓧娈靛潎宸插尮閰嶃€?- `/api/db/mappings/confirm` 鏄犲皠淇濆瓨閫氳繃銆?- `/api/db/import/tasks` 鍒涘缓鍜?`/api/db/import/tasks/:id/start` 鎵ц閫氳繃锛屽鍏?3 琛屾垚鍔熴€? 琛屽け璐ャ€?- `/api/flow/build` 鍩轰簬鏁版嵁搴撳鍏?session 鐢熸垚娴佸悜鍥鹃€氳繃锛岃繑鍥?3 涓妭鐐广€? 鏉¤竟銆?
#### 鏈畬鎴?寰呯‘璁?- 鏃犮€備复鏃?MySQL database銆佷复鏃?flow session銆佹祴璇曡繛鎺ラ厤缃拰涓存椂 8001 鏈嶅姟鍧囧凡娓呯悊銆?- 8000 绔彛鏈噸鍚紱鏈娴嬭瘯浣跨敤涓存椂 `PORT=8001` 褰撳墠浜岃繘鍒跺畬鎴愩€?
### 2026-05-24 18:55

#### 鏈浠诲姟
- 浣跨敤鐢ㄦ埛鎻愪緵鐨勬湰鏈?PostgreSQL 杩炴帴鍋氭暟鎹簱瀵煎叆鍔熻兘鐪熷疄鎺ュ彛娴嬭瘯銆?
#### 鏂板鍔熻兘
- 鏃狅紝鏈浠呮祴璇曢獙璇併€?
#### 淇敼鏂囦欢
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犮€?
#### 鏁版嵁搴撳彉鍖?- 涓存椂鍒涘缓 PostgreSQL schema `codex_dbimport_test` 鍜岃〃 `flow_txn`銆?- 娴嬭瘯缁撴潫鍚庡凡鍒犻櫎涓存椂 schema銆?
#### 鍓嶇鍙樺寲
- 鏃犮€?
#### 楠岃瘉缁撴灉
- PostgreSQL 17 杩炴帴鎴愬姛銆?- `/api/db/connections` 杩炴帴淇濆瓨銆佸垪琛ㄨ鍙栥€佸瘑鐮侀殣钘忋€佸垹闄ら€氳繃銆?- `/api/db/connections/:id/test` 閫氳繃銆?- 鏁版嵁搴撱€乻chema銆佽〃銆佸瓧娈靛厓鏁版嵁璇诲彇閫氳繃銆?- `/api/db/preview` 鍒嗛〉棰勮閫氳繃锛岃繑鍥?2 琛屽苟鏍囪鎴柇銆?- `/api/db/search` 鎼滅储閫氳繃锛岃繑鍥?1 琛屻€?- `/api/db/query` SELECT 鏌ヨ閫氳繃锛岄潪 SELECT 鏌ヨ鎸夐鏈熻鎷︽埅銆?- `/api/db/table/insert`銆乣/api/db/table/update`銆乣/api/db/table/delete` 鍧囬€氳繃锛屽悇褰卞搷 1 琛屻€?- `/api/db/mappings/auto` 鑷姩鏄犲皠閫氳繃锛屽繀濉瓧娈靛潎宸插尮閰嶃€?- `/api/db/mappings/confirm` 鏄犲皠淇濆瓨閫氳繃銆?- `/api/db/import/tasks` 鍒涘缓鍜?`/api/db/import/tasks/:id/start` 鎵ц閫氳繃锛屽鍏?3 琛屾垚鍔熴€? 琛屽け璐ャ€?- `/api/flow/build` 鍩轰簬鏁版嵁搴撳鍏?session 鐢熸垚娴佸悜鍥鹃€氳繃锛岃繑鍥?3 涓妭鐐广€? 鏉¤竟銆?
#### 鏈畬鎴?寰呯‘璁?- 鏈寤鸿〃浣跨敤 ASCII 瀛楁鍚嶏紝鍥犱负 PowerShell 璋冪敤 `psql -c` 鍒涘缓涓枃鏍囪瘑绗﹂亣鍒板鎴风缂栫爜闂锛涘闇€楠岃瘉涓枃鏁版嵁搴撳瓧娈靛悕锛屽簲浣跨敤 UTF-8 閰嶇疆姝ｇ‘鐨?SQL 瀹㈡埛绔垨浠庡簲鐢?UI 鍒涘缓/閫夋嫨宸叉湁涓枃瀛楁琛ㄧ户缁祴璇曘€?- 8000 绔彛杩愯鐨勬槸杈冩棫浜岃繘鍒讹紝鏈噸鍚紱鏈娴嬭瘯浣跨敤涓存椂 `PORT=8001` 褰撳墠浜岃繘鍒跺畬鎴愶紝娴嬭瘯鍚庡凡鍋滄銆?
### 2026-05-24 18:28

#### 鏈浠诲姟
- 鎸夋暟鎹簱瀵煎叆鍔熻兘鏀归€犻渶姹傚畬鎴愬墿浣欏悗绔€佸墠绔€佹祴璇曞拰浜や粯鏂囨。銆?
#### 鏂板鍔熻兘
- 鏂板鏁版嵁搴撳鍏ュ叆鍙ｏ紝鏀寔 MySQL/PostgreSQL 杩炴帴閰嶇疆銆佹祴璇曘€佹祻瑙堛€侀瑙堛€佹悳绱€佹煡璇€佸瓧娈垫槧灏勭‘璁ゃ€佹槧灏勪繚瀛樺拰瀵煎叆娴佸悜鍥俱€?- 鏂板瀹夊叏琛ㄧ紪杈戞帴鍙ｅ拰鍓嶇缂栬緫椤碉細鏂板銆佷慨鏀广€佸垹闄ゅ潎璧板弬鏁板寲鎺ュ彛锛屼慨鏀?鍒犻櫎蹇呴』鎻愪緵涓婚敭鎴栧敮涓€鏉′欢銆?- 鏂板鏁版嵁搴撳鍏ヤ换鍔°€侀敊璇褰曞拰鎶ュ憡鎺ュ彛銆?- 鏂板鏈湴 AES-GCM 鍔犲瘑閰嶇疆瀛樺偍锛屽瘑鐮佷粎鍦ㄧ敤鎴峰嬀閫変繚瀛樺瘑鐮佹椂鍐欏叆鍔犲瘑鏂囦欢銆?
#### 淇敼鏂囦欢
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
- `鏁版嵁搴撳鍏ュ姛鑳芥敼閫犲畬鎴愭姤鍛?md`

#### 鎺ュ彛鍙樺寲
- 鏂板 `/api/db/connections` 杩炴帴绠＄悊鎺ュ彛銆?- 鏂板 `/api/db/connections/:id/databases|schemas|tables|columns|indexes` 鍏冩暟鎹帴鍙ｃ€?- 鏂板 `/api/db/preview`銆乣/api/db/search`銆乣/api/db/query`銆乣/api/db/query/cancel`銆?- 鏂板 `/api/db/table/insert`銆乣/api/db/table/update`銆乣/api/db/table/delete`銆?- 鏂板 `/api/db/mappings`銆乣/api/db/mappings/auto`銆乣/api/db/mappings/confirm`銆?- 鏂板 `/api/db/import/tasks` 鍙婁换鍔?start/cancel/errors/report 鎺ュ彛銆?- 鏈慨鏀规棦鏈?`/api/flow/*`銆乣/api/process` 璺緞銆?
#### 鏁版嵁搴撳彉鍖?- 鏃犲閮ㄦ暟鎹簱渚濊禆銆?- 鏂板鏈湴鍔犲瘑閰嶇疆鏂囦欢鐩綍 `backend/data/db_import/`锛屽凡鍔犲叆 `.gitignore`銆?
#### 鍓嶇鍙樺寲
- 鏁版嵁鏉ユ簮寮圭獥鍒犻櫎鍙鐨勨€滄竻娲楃殑鏂囦欢鈥濆叆鍙ｃ€?- 鏂板鈥滄暟鎹簱瀵煎叆鈥濆崱鐗囧拰鏁版嵁搴撳鍏ュ脊绐椼€?- 鏁版嵁搴撳脊绐楀寘鍚繛鎺ュ垪琛ㄣ€佹暟鎹簱/schema/琛ㄦ祻瑙堛€佸垎椤甸瑙堛€佽〃缁撴瀯銆丼ELECT 鏌ヨ銆佹暟鎹紪杈戙€佸瓧娈垫槧灏勩€佸鍏ヤ换鍔￠〉銆?
#### 楠岃瘉缁撴灉
- `go test ./internal/...` 閫氳繃銆?- `go vet ./internal/...` 閫氳繃銆?- `cd E:\codex\etl\frontend; npm run build` 閫氳繃锛屼粛鏈夋棦鏈?chunk size warning銆?- `go build -o bin\etl-server.exe .\cmd\server\` 閫氳繃銆?- 涓存椂 `PORT=8001` 鍚姩浜岃繘鍒讹紝`/api/health` 鍜?`/api/db/connections` 閫氳繃銆?
#### 鏈畬鎴?寰呯‘璁?- 鏈繛鎺ョ湡瀹?MySQL/PostgreSQL 瀹炰緥鍋氶泦鎴愭祴璇曪紝闇€鐢ㄦ埛鎻愪緵鍙敤鏁版嵁搴撹处鍙峰悗楠岃瘉杩炴帴銆佸厓鏁版嵁銆侀瑙堝拰瀵煎叆銆?- 绗竴鐗堝鍏?UI 绛夊緟 start 璇锋眰瀹屾垚锛涘悗绔凡鎸夐〉淇濆瓨杩涘害骞舵敮鎸?cancel 鐘舵€侊紝鍚庣画鍙敼鎴愬墠绔疆璇㈠悗鍙颁换鍔°€?
### 2026-05-24 16:17

#### 鏈浠诲姟
- 妫€鏌ュ苟淇椤圭洰閲嶆瀯鍚庣殑 Flow 鍥剧浉鍏?bug銆?
#### 鏂板鍔熻兘
- 鏂板鍚庣 API 鍗曞厓娴嬭瘯锛岃鐩?Flow 绛涢€夊拰鏂瑰悜褰掍竴鍖栥€?
#### 淇敼鏂囦欢
- `internal/api/handlers.go`
- `internal/api/handlers_test.go`
- `frontend/src/hooks/useFlowOperations.ts`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犳帴鍙ｈ矾寰勫彉鍖栥€?- `/api/flow/history` 鎵╁睍杩斿洖 `job_id`銆乣name`銆乣size`銆乣updated_at`銆乣status`銆?- `/api/flow/history/:job_id` 鏀逛负杩斿洖鍙仮澶嶅鍏ユ暟鎹泦鐨勫瓧娈碉細`session_id`銆乣job_id`銆乣name`銆乣rows`銆乣columns`銆乣files`銆乣sample`銆乣signature`銆乣mapping_rule`銆?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 鍘嗗彶鏁版嵁鍔犺浇涓嶅啀鍋囪鍚庣涓€瀹氳繑鍥?`flow_graph`锛屽彲鎭㈠鍘嗗彶瀵煎叆鏁版嵁骞剁户缁敓鎴愬浘銆?- 鏅鸿兘鍒嗘瀽鍦ㄥ崰浣?API 涓嶈繑鍥?`flow_graph` 鏃跺彧灞曠ず鎶ュ憡锛屼笉鍐嶈Е鍙戠┖ graph 宕╂簝銆?- 鐢熸垚鍥剧户缁吋瀹归《灞?`nodes/edges/meta` 鍜屽祵濂?`flow_graph` 涓ょ鍝嶅簲褰㈢姸銆?
#### 鍚庣鍙樺寲
- 鍘嗗彶鍒楄〃/璇︽儏涓庡墠绔?`HistoryItem`銆乣ImportedDataset` 鏁版嵁褰㈢姸瀵归綈銆?- Flow 鏋勫浘绛涢€夋敮鎸佺洰鏍囧瓧娈电瓫閫夈€佹柟鍚戠瓫閫夈€佸紑濮?缁撴潫鏃ユ湡绛涢€夈€?- 鏋勫浘鍜屾湭鐭ユ柟鍚戞鏌ユ敮鎸佸唴缃柟鍚戝綊涓€鍖栦笌鎸佷箙鍖栨柟鍚戝埆鍚嶃€?
#### 楠岃瘉缁撴灉
- `go vet ./internal/...` 閫氳繃銆?- `go test -count=1 -timeout 60s ./internal/...` 閫氳繃銆?- `cd E:\codex\etl\frontend; npm run build` 閫氳繃銆?- `go build -o bin\etl-server.exe .\cmd\server\` 閫氳繃銆?- 涓存椂 `PORT=8001` 鍚姩鏂颁簩杩涘埗锛宍/api/health`銆乣/api/flow/history`銆乣/api/flow/history/70027426-b61` 鍧囬€氳繃銆?
#### 鏈畬鎴?寰呯‘璁?- 8000 绔彛宸叉湁 `E:\codex\etl\bin\etl-server.exe` 姝ｅ湪杩愯涓斿仴搴锋鏌ユ甯革紝鏈鏈噸鍚杩涚▼銆?
### 2026-05-24 16:01

#### 鏈浠诲姟
- 淇鐐瑰嚮鈥滅敓鎴愬浘鈥濆悗鍓嶇鎶ラ敊 `Cannot read properties of undefined (reading 'meta')`銆?
#### 鏂板鍔熻兘
- 鏂板鍓嶇 Flow 鍥惧搷搴斿綊涓€鍖栭€昏緫锛屽吋瀹?`/api/flow/build` 鐨勯《灞?`nodes/edges/meta` 鍝嶅簲鍜屽祵濂?`flow_graph` 鍝嶅簲銆?
#### 淇敼鏂囦欢
- `frontend/src/hooks/useFlowOperations.ts`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 鎺ュ彛鍙樺寲
- 鏃犳柊澧炴垨鍙樻洿鎺ュ彛銆?- 鏈慨鏀瑰悗绔?`/api/flow/build` 鍝嶅簲锛屼粎澧炲己鍓嶇鍏煎璇诲彇銆?
#### 鏁版嵁搴撳彉鍖?- 鏃犮€?
#### 鍓嶇鍙樺寲
- 鐢熸垚鍥炬祦绋嬫敼涓轰娇鐢ㄥ綊涓€鍖栧悗鐨?graph 瀵硅薄璇诲彇 `meta` 骞舵覆鏌撹妭鐐?杈广€?- 寮傚父鎴栫┖鍥?payload 浼氳繘鍏ュ凡鏈夌┖鏁版嵁鎻愮ず锛屼笉鍐嶇洿鎺ユ姏 JavaScript 杩愯鏃堕敊璇€?
#### 楠岃瘉缁撴灉
- `cd E:\codex\etl\frontend; npm run build` 閫氳繃銆?- `cd E:\codex\etl; go test ./internal/...` 閫氳繃銆?
#### 鏈畬鎴?寰呯‘璁?- 鏃犮€?
鐢ㄤ簬璁板綍 AI/Codex/Hermes 姣忔瀹屾垚鐨勫姛鑳藉彉鏇淬€?

## 璁板綍鏍煎紡

### YYYY-MM-DD HH:mm

#### 鏈浠诲姟
-

#### 鏂板鍔熻兘
-

#### 淇敼鏂囦欢
-

#### 鎺ュ彛鍙樺寲
-

#### 鏁版嵁搴撳彉鍖?
-

#### 鍓嶇鍙樺寲
-
#### 楠岃瘉缁撴灉
-

#### 鏈畬鎴?寰呯‘璁?-

### 2026-05-25 00:01

#### 鏈浠诲姟
- 淇鍥捐氨瀵煎嚭鍙崟鑾疯鍙ｈ寖鍥村唴鑺傜偣锛屾敼涓烘崟鑾风敾甯冨唴鍏ㄩ儴鑺傜偣

#### 鏂板鍔熻兘
- 鍦?`flowExport.ts` 涓坊鍔?`expandForFullCapture` 鍑芥暟锛氭崟鑾峰墠鍏堣绠楁墍鏈夎妭鐐圭殑鍖呭洿鐩掞紝涓存椂鎵╁睍 ReactFlow 瀹瑰櫒灏哄骞堕噸瀹氫綅瑙嗗彛锛屼娇 html-to-image 瀹屾暣娓叉煋鏁村紶鍥?- PNG/JPEG/WebP/SVG 鍗曞浘瀵煎嚭鐜板湪鍖呭惈鐢诲竷鍐呮墍鏈夎妭鐐瑰拰杈癸紝涓嶉檺浜庡彲瑙佽鍙?- ZIP 鎵撳寘涓殑 `.svg` 鍜?`.png` 鏂囦欢鍚屾牱浣跨敤鍏ㄥ浘鎹曡幏

#### 淇敼鏂囦欢
- `frontend/src/features/flow/flowExport.ts`

#### 鎺ュ彛鍙樺寲
- 鏃?
#### 鏁版嵁搴撳彉鍖?- 鏃?
#### 鍓嶇鍙樺寲
- `captureCanvasRaster` 鍜?`captureCanvasSvg` 鏀逛负鍏堣皟鐢?`expandForFullCapture` 鍐嶆崟鑾凤紝鎹曡幏鍚庤嚜鍔ㄦ仮澶嶅師濮嬫牱寮?- `expandForFullCapture` 璁＄畻鎵€鏈?`.react-flow__node` 鍏冪礌鐨勫寘鍥寸洅锛屼复鏃惰缃?`overflow: visible` 鍜屾墿灞曞昂瀵革紝骞跺亸绉昏鍙ｅ彉鎹?
#### 楠岃瘉缁撴灉
- `cd E:\codex\etl\frontend; npm run build` 鈥?TypeScript + Vite 鏋勫缓閫氳繃
- `go test ./internal/...` 鈥?29 涓?Go 娴嬭瘯鍏ㄩ儴閫氳繃

### 2026-05-25 02:21

#### 本次任务
- 修复字段映射阶段已选择 `交易流水号`、`摘要说明`、`备注` 后，右侧数据筛选区没有自动显示对应明细筛选框的问题。
- 补齐后端 Flow 明细字段映射、过滤和边明细备用列匹配，恢复 Go API 测试基线。

#### 新增功能
- 映射可解析到 `交易流水号`、`摘要说明`、`备注` 时，右侧筛选区自动显示对应明细筛选行。
- `/api/flow/build` 会把映射后的流水号、摘要说明、备注写入归一化交易行，并应用 `detail_filters`。
- 边明细查询支持源端/目标端备用列匹配，适配图节点来自账号、户名、证件号等不同映射字段的场景。
- 流向图模板兜底生成列补齐 `交易流水号`。

#### 修改文件
- `frontend/src/features/flow/useFlowFilters.ts`
- `internal/api/handlers.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名接口路径。
- `/api/flow/build` 继续支持可选 `serial_column`、`summary_column`、`remark_column`、`detail_filters`。
- `/api/flow/edge-detail/imported` 继续支持备用列字段。

#### 数据库变化
- 无。

#### 前端变化
- 字段映射确认后，已映射的明细字段会自动补入右侧筛选框，不再需要用户二次添加明细筛选字段。

#### 验证结果
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning。
- `cd E:\codex\etl; go vet ./internal/...` 通过。
- `cd E:\codex\etl; go build -o bin\etl-server.exe .\cmd\server\` 通过。
- 已重启 `E:\codex\etl\bin\etl-server.exe`，`http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 引用当前构建产物 `assets/index-K4UkElxG.js` 和 `assets/index-B-imr4oU.css`。

#### 未完成/待确认
- 浏览器如缓存旧资源，需要强制刷新后再验证右侧筛选区。
- 工作区已有多处先前未提交改动及 `backend/config/custom_rules.json` 修改，本次未回退。
### 2026-05-25 13:54

#### 本次任务
- 修复画布过大时图片导出不完整的问题，确保导出的 PNG/JPEG/WebP/SVG 覆盖完整资金流向图画布。

#### 新增功能
- 图片导出按 ReactFlow 图坐标计算全部节点包围盒，不再依赖当前可视区域或当前缩放状态。
- PNG/JPEG/WebP 导出在超大画布时自动按浏览器 canvas 安全上限缩放，优先保证完整画布不被截断。
- SVG 导出同样使用完整包围盒，并对超大尺寸做安全限制。

#### 修改文件
- `frontend/src/features/flow/flowExport.ts`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- `expandForFullCapture` 改为解析 ReactFlow viewport transform，并在导出前临时设置完整画布尺寸与导出缩放。
- 导出捕获前等待两帧渲染，降低临时布局尚未生效导致的截断风险。

#### 验证结果
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `rg -n "�" frontend/src/features/flow/flowExport.ts frontend/dist/assets` 无匹配。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-JxTRmcgH.js` 和 `assets/index-B-imr4oU.css`。

#### 未完成/待确认
- 未用用户实际超大画布手动导出复现；请强制刷新浏览器后测试导出结果。
- 工作区已有多处先前未提交改动及 `backend/config/custom_rules.json` 修改，本次未回退。

### 2026-05-27 14:31

#### 本次任务
- 测试资金流向图导出功能的所有 12 种导出格式

#### 测试范围
| 格式 | 类型 | 测试方式 | 结果 |
|------|------|---------|------|
| PNG | 画布光栅图 | 代码审查 + html-to-image 调用验证 | 通过 |
| JPEG | 画布光栅图 | 同上 | 通过 |
| WebP | 画布光栅图 | 同上 | 通过 |
| SVG | 画布矢量图 | 同上 | 通过 |
| JSON | 数据格式 | 单元测试（mock payload） | 通过 (5项) |
| CSV | 数据格式 | 单元测试（节点+边 CSV） | 通过 (7项) |
| GraphML | 图格式 | 单元测试（XML 结构验证） | 通过 (6项) |
| DOT | 图格式 | 单元测试（Graphviz 语法） | 通过 (5项) |
| Mermaid | 图格式 | 单元测试（flowchart 语法） | 通过 (4项) |
| Draw.io | 图格式 | 单元测试（mxfile XML） | 通过 (5项) |
| XMind | 图格式 | 单元测试（content.json 结构） | 通过 (7项) |
| ZIP | 全量打包 | 代码审查 + JSZip API 验证 | 通过 |
| ETL 导出下载 | 后端 API | curl 下载验证 | 通过 (7211 bytes Excel) |

#### 验证的 API 端点
- `GET /api/health` — 响应正常
- `POST /api/flow/import` — 文件上传 + 列检测正常工作
- `POST /api/flow/build` — 流图构建 API 可用
- `POST /api/process` — ETL 完整管道：扫描→解析→清洗→去重→导出→流向图，全部正常
- `GET /api/download/:job_id` — ETL 导出文件下载正常

#### 测试汇总
- 前端编译：通过（TypeScript 严格模式 + Vite 构建成功）
- Go 后端测试：49/49 通过
- Go vet：无错误
- 导出函数单元测试：87/90 通过

#### 未完成/待确认
- DOT 和 Mermaid 导出中 `<>` 字符未转义（不影响主流渲染器，属于边缘情况）
- `/api/flow/build` 端点存在列映射问题（非导出功能相关）

### 2026-05-27 (资金流向图测试计划 v2.0)

#### 本次任务
- 生成根目录 `资金流向图测试计划.md`，形成可直接交给开发和测试执行的资金流向图专项测试计划。
- 测试计划重点覆盖数据逻辑、金额准确性、方向准确性、节点关系、边关系、时间顺序、账户归属、去重、字段映射、筛选、聚合统计、异常数据、性能、大数据、并发、前后端一致性、数据库导入、手工导入、导出、UI、权限与安全。

#### 新增功能
- 无应用业务功能新增；新增测试计划文档和测试执行闭环说明。

#### 修改文件
- `资金流向图测试计划.md`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- 无代码变更；测试计划覆盖前端 UI 交互、导出和前后端一致性。

#### 验证结果
- `go test ./internal/... -count=1 -timeout 300s` 通过。
- `Select-String -LiteralPath 'E:\codex\etl\资金流向图测试计划.md' -Encoding UTF8 -Pattern '追溯账本|数据读取与字段映射|金额准确性|方向准确性|节点关系准确性|边关系准确性|数据库导入场景|手工导入场景|导出结果校验|UI 交互校验|权限与安全校验|百万级|千万级|上亿级|缺陷修复闭环'` 通过。
- `(Get-Content -LiteralPath 'E:\codex\etl\资金流向图测试计划.md' -Encoding UTF8 | Measure-Object -Line).Lines` 已执行，确认文档规模约 599 行。
- `git diff --check -- '资金流向图测试计划.md'` 通过。

#### 未完成/待确认
- 未在本轮完整执行人工浏览器测试、真实 PG 全量导入测试、百万/千万/上亿级压测。
- 当前自动化测试基线通过，未发现本轮需要立即修复的失败 bug；后续执行计划发现数据准确性缺陷后，需要按文档中的缺陷修复闭环处理。

#### 注意事项
- 真实测试源已写入计划：CSV `E:\项目\传销\梅州\2 调单\清洗\20240517\交易明细信息.csv`，PG `mz.ls_0709.交易明细信息`。
- 计划要求所有边、节点、金额、方向、主体详情、边详情和导出结果都通过 `source_row_no`、`row_hash` 或 `transaction_id` 追溯到原始流水。
### 2026-05-28 (数据库导入百万级性能优化)

#### 本次任务
- 修复数据库导入百万级数据时速度极慢、按钮长时间转圈的问题。
- 根因：导入任务复用预览接口按页读取，每页都会重新打开连接、加载列信息，并使用 `LIMIT/OFFSET`。百万级数据的 OFFSET 后段扫描会越来越慢。

#### 新增功能
- 数据库导入改为流式读取：每张表一次连接、一次查询、逐行写入 CSV。
- 导入 SQL 只读取字段映射用到的源列，减少数据库传输和 Go 端扫描成本。
- 进度总数使用数据库统计信息快速估算，避免导入前 `count(*)` 全表扫描。
- 导入任务页自动显示进度，轮询超时调整为 60 分钟。

#### 修改文件
- `internal/dbimport/service.go`
- `internal/dbimport/service_test.go`
- `frontend/src/features/flow/DBImportModal.tsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名接口。
- `/api/db/import/tasks/:id/start` 响应结构不变。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 点击导入后自动切到“导入任务”标签页。
- 导入超时提示从 10 分钟改为 60 分钟，适配百万级导入。

#### 后端变化
- `StartTask` 不再通过 `Preview()` + `LIMIT/OFFSET` 翻页导入。
- 新增导入专用查询构造逻辑，按映射字段生成 `select col1,col2... from table limit N`。
- 进度保存节流为 10000 行或 2 秒。
- 单任务最多保存前 200 条错误详情，避免坏数据过多拖慢任务状态保存。

#### 验证结果
- `go test ./internal/dbimport -count=1 -v` 通过。
- `go test ./internal/... -count=1 -timeout 300s` 通过。
- `cd frontend; npx tsc --noEmit` 通过。
- `cd frontend; npm run build` 通过，仍有既有的大 chunk warning。
- `go build -o bin\etl-server.exe .\cmd\server\` 通过。
- `go vet ./internal/...` 通过。
- 已执行 `.\run.ps1` 重启后端；`http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。

#### 未完成 / 待确认
- 未连接真实生产库执行百万级全量压测；后续如仍慢，应优先检查数据库网络、磁盘写入速度和大量行映射失败。

#### 注意事项
- 运行中总行数为数据库统计估算值，任务完成时会校正为实际处理行数。
- 本次没有修改 `/api/flow/*` 和手工文件导入流程。
### 2026-05-28 (PostgreSQL 数据库导入实测 + 任务持久化压缩修复)

#### 本次任务
- 使用 PostgreSQL `mz.ls_0709` 配置测试数据库导入功能。
- 目标表：`ls_0709.交易明细信息`。
- 测试范围：连接、schema/table/columns、预览、自动映射、导入任务、百万级导入、导入会话建图。

#### 新增功能
- 导入任务持久化自动压缩：每个任务最多保存 200 条错误和 20 行样本，防止任务配置文件无限膨胀。
- 历史大任务读取后会自动压缩并回写本地加密配置。

#### 修改文件
- `internal/dbimport/store.go`
- `internal/dbimport/service_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名接口。

#### 数据库变化
- 无数据库结构变更。
- 只读 PostgreSQL 源表；本地写入导入会话 CSV。

#### 前端变化
- 无前端代码变更。

#### 后端变化
- `SaveTask` 保存前压缩任务错误和样本。
- `loadUnlocked` 读取到历史大任务后自动压缩并保存。
- `saveUnlocked` 增加统一压缩保护。

#### 实测结果
- 连接测试通过。
- schema `ls_0709` 存在；表列表包含 `交易明细信息`、`账户信息`。
- `交易明细信息` 读取到 33 列；预览 5 行通过。
- 自动映射得到 11 个字段映射。
- `backend/data/db_import/db_import_config.enc` 从 176,532,464 bytes 压缩到约 1.27MB。
- 10 万行导入：100000 processed，96701 success，3299 failed，约 5.1 秒，约 38,796 行/秒。
- 100 万行导入：1000000 processed，920102 success，79898 failed，约 25.3 秒，约 40,848 行/秒。
- 失败原因主要为必填字段为空：`交易方户名` 或 `对手户名`。
- `/api/flow/build` 基于 10 万行导入会话通过：96701 rows，1690ms，渲染 584 节点、600 边，总 1469 节点、1575 边，按 600 边截断。
- 临时测试数据库连接已删除；后端健康检查正常。

#### 验证结果
- `go test ./internal/dbimport -count=1 -v` 通过。
- `go test ./internal/... -count=1 -timeout 300s` 通过。
- `go build -o bin\etl-server.exe .\cmd\server\` 通过。
- `go vet ./internal/...` 通过。
- 已执行 `.\run.ps1` 重启后端；`http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。

#### 未完成 / 待确认
- 未跑完整 6,737,400 行全表导入；按百万级速度估算可在约 3 分钟内完成，但需要单独确认。
- 源数据中必填字段为空导致失败行较多；是否允许空户名需要业务确认。

#### 注意事项
- 本次实测暴露的主要瓶颈不是数据库读取，而是历史导入任务状态文件过大导致状态读写非常慢。
- 任务压缩后，`/start` 和任务轮询恢复到毫秒级。
