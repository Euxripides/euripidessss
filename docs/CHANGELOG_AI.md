### 2026-08-02 V2.1 RC2 系统优化批次（10 项全部落地）

#### 新增

1. **64 位构建**：正式二进制 GOARCH=amd64（54MB），解除 32 位 2GB 地址空间上限（BUG-001 类 OOM 风险大幅降低）
2. **BatchProfiles 缓存**：SHA256 地址集哈希（顺序无关/大小写归一/区分 addr_file），64 条上限防内存增长
3. **AI 输出截断自动重试**：finish_reason=length/空 content 时 max_tokens 翻倍重试一次（防循环）
4. **AI 建议驱动规划**：规则 STOP（非资源上限类）+ AI EXPAND 建议（conf≥0.8+合法 target）→ 延续调查；资源上限 STOP 不可覆盖
5. **前端代码分割**：7 个重页面 React.lazy + Suspense，主 bundle 3,222→2,028KB（-37%）
6. **SSE 进度推送**：GET /api/intelligence/events + 前端 EventSource 替代 3s 轮询（含订阅竞态修复）
7. **测试锁互斥**：duckdb.AcquireDataLock（O_EXCL），12 个真实数据测试接入，跨包并行零冲突
8. **AI 用量统计**：GET /api/intelligence/ai-usage（calls/tokens/耗时/模型分布，跨调查累计）

#### 评估结论（暂缓）

- DuckDB 嵌入式驱动：无 C 编译器（CGO 不可用），替代方案已覆盖
- typed parquet：核心管道变更 + 需重新真实下载验证，暂缓
- API Key 加密：环境变量不落盘已是安全默认，保持现状

#### 验证

- `go test ./... -short -count=1` 38 包零回归；vet 零警告
- 前端 build 通过（bundle -37%）
- 真实服务：SSE 事件流验证、ai-usage 真实统计（4 calls/11,527 tokens/46.9s）、AI 建议驱动调查验证
- 新增测试：TestBatchCacheKey / TestDeepSeekChatRetryOnTruncation / TestLoopAISuggestion×2 / TestSSE×2

#### 修改文件

- `internal/analyticsapi/service.go` + `service_test.go`
- `internal/intelligence/{deepseek_client,loop_engine,investigation_agent,api_handler}.go` + `{deepseek_client_test,sse_test,loop_engine_test}.go`
- `internal/analysis/duckdb/engine.go` + 12 个测试文件
- `frontend/src/App.tsx`、`frontend/src/features/intelligence/{intelligenceApi,IntelligencePage}.tsx`

### 2026-08-02 V2.1 RC2 全链路真实调查系统验收测试（Full System Real Data Acceptance Test）

#### 本次完成

按《V2.1_RC2_全链路真实调查系统验收测试方案》完成全功能验收：服务环境、数据资产链路、DuckDB 分析、SQD 真实采集可靠性、分析模块、智能调查闭环、API 并发、Bug 自动记录与修复闭环。

#### 验收结果（详见 benchmark/full-system-report.md/.json）

- **服务环境**：健康检查 200、DuckDB v1.5.3、前端 build 通过（3908 modules）
- **数据资产**：49031 行全链路一致（source=parsed=unique=parquet=duckdb），checksum 有效，dup=0
- **DuckDB 12 场景**：50K 地址 SEMI JOIN 44ms（<1s）、10 并发 75ms 无错误
- **SQD 真实采集**：10K 地址 100/100 chunks 完成率 100%（3m8.7s），503 冷却+重试恢复成功，0 丢失 0 重复（应用层唯一化后）
- **分析模块**：画像 5/5、余额 3/3（50K 25ms）、图谱 PASS（15595 节点/21693 边）、案件/调查 PASS
- **智能调查**：inv-1（0xdead）COMPLETED，10 路径/21 实体/2 风险，三格式报告
- **API**：40+ 端点冒烟全通，100 并发错误率 0%（avg 58ms）
- **DeepSeek**：真实调用 4/4 成功（deepseek-v4-flash）：AI 规划 deep_probe（conf 0.65）、AI 建议 STOP（conf 0.95）、AI 深入分析 5 条发现全部 VERIFIED（tx 证据链）+ 5 条洞察；无 key 时优雅降级

#### 修复的 Bug（4 项，均已回归验证）

| Bug | 严重度 | 根因 | 修复 |
|-----|--------|------|------|
| BUG-001 | critical | `/api/analytics/addresses/profile` 空 addr_file → `read_csv('')` 在 DuckDB CLI 回退读标准输入 → JOIN 结果爆炸 → 32 位进程 OOM 崩溃（任何客户端可触发，DoS） | `batchWantSQL` 纯函数：addresses 走 VALUES 内联（quoteSQLString 转义）、addrFile 走 read_csv、都空返回错误；Handler 空参数 400；新增 `TestBatchProfiles_Validation` 回归 |
| BUG-002 | high | VALUES 内联 1000 地址（≈48KB SQL）超 Windows 32K 命令行限制，`fork/exec too long` | addrFile 优先（命令短）；VALUES 分支截断 500；回归断言同步 |
| BUG-003 | high | addr_file 未校验：任意文件读取（auth.json 等）、无缓存 DoS、超长地址可撑爆命令行 | `Handler.validateAddrFile`（绝对路径/`..`穿越/通配符拒绝，仅允许数据根目录内相对路径）+ 相对路径解析为绝对路径；>64 字符地址跳过；`json()` 错误截断 300 字符；新增 `TestValidateAddrFile` 回归 |
| BUG-004 | medium | `max_tokens=2000` 对推理模型 deepseek-v4-flash 不足：推理占满额度后 content 截断/为空 → AI 深入分析/假设解析失败，报告 AI 章节为空 | `DefaultConfig.MaxTokens` 2000→4096；chatResponse 解析 finish_reason + 截断警告日志；真实调查复测 5 发现全 VERIFIED |

#### 修改文件

- `internal/analyticsapi/service.go` — batchWantSQL / validateAddrFile / NewHandler allowedDataRoot / 400 预检 / json() 错误截断
- `internal/analyticsapi/service_test.go` — TestBatchProfiles_Validation（6 项）+ TestValidateAddrFile（6 项）
- `internal/intelligence/types.go` — DefaultConfig.MaxTokens 2000→4096（BUG-004）
- `internal/intelligence/deepseek_client.go` — finish_reason 解析 + 截断检测日志（BUG-004）
- `benchmark/bug-report/BUG-001..004.json` — Bug 记录闭环
- `benchmark/full-system-report.md/.json` — 验收报告（含 DeepSeek 真实调用验证）

#### 已验证

- `go test ./... -short -count=1` 38 包零回归（修复后）
- `go test ./internal/analyticsapi/ -count=1` 全过（含真实数据 4 项 + 新增 2 项回归）
- 真实服务复测：addresses 200 正确、空参数 400、绝对路径/穿越 400、合法 addr_file 200、600 地址不崩溃
- `go vet ./internal/...` 零警告；`go build` 成功

#### 未完成事项与边界

- DeepSeek 真实调用未执行（`DEEPSEEK_API_KEY` 未配置，会话级密钥不落库）；AI 链路由单测覆盖（78.4%）
- 真实数据验证测试（标记文件启用）需**串行**运行（`go test -p 1` 或逐包），并行时 DuckDB 文件锁冲突——测试基建已知限制
- 服务为 windows/386 32 位构建，大结果集 JSON 编码有 ~2GB 地址空间上限（BUG-001 触发路径已封死；批量接口 500 地址/64 字符上限）

### 2026-08-02 V2.1 RC2 DeepSeek 驱动自主调查 Agent

#### 新增

- AI 驱动调查（`internal/intelligence`）：**Planner Agent**（AI 策略 + 结构化任务，规则回退）、**Hypothesis Agent**（规则触发 + AI 细化，验证任务入队）、**Analysis Agent**（多角色深入分析）、**Evidence Guard**（tx 证据 → VERIFIED/REJECTED/UNVERIFIED）、**Prompt Builder**（Investigator/AML/Forensic/ReportWriter 多角色，PromptVersion）、**Response Parser**（严格 JSON 校验）、**AI Memory**（5 类记忆 JSON 持久化）、**AIAgent 编排**（调用限额 / 记忆固化）
- DeepSeek 客户端通用 Chat + max_tokens + 请求/用量日志；配置 `max_tokens / max_ai_calls`
- 闭环集成：AI 规划（inv.strategy）、假设验证任务驱动续查（PendingVerifications）、AI 建议记录（规则引擎最终裁决）、收尾 AI 深入分析 + 假设状态收尾 + 记忆固化
- 报告「调查假设与已验证发现」章节；前端 AI 建议面板 / 调查假设 / 已验证发现 / AI 策略卡片

#### 验证

- go test ./... -short 38 包零回归；vet 零警告；intelligence 覆盖率 74.2% → 78.0%
- 闭环集成测试：AI 策略 → 假设验证任务 → VERIFIED 发现 → AI 建议 → 记忆固化全链路
- 前端 tsc + vite build 通过

#### 修复（代码审查后）

- AI 调用配额饥饿 → `max_ai_calls` 默认 5 → 10
- 假设状态虚假标记 → 按验证任务真实状态门控（TaskIDs 回写 + done 校验），新增提前结束回归测试
- AI 记忆并发 last-writer-wins → 共享 AIMemoryStore 实例 + Save 串行化
- 附带：历史记忆单路注入（ctx.History）、NewAgent MaxTokens 透传、callCount 清理、规则判断来源标记 rule

#### 修改文件

- 新增 `internal/intelligence/{planner_agent,hypothesis_agent,analysis_agent,ai_agent,prompt_builder,response_parser,evidence_guard,ai_memory}.go` + `ai_agent_test.go`
- 修改 `internal/intelligence/{types,deepseek_client,loop_engine,investigation_agent,api_handler,report_agent,decision_engine}.go`
- 修改 `frontend/src/features/intelligence/{intelligenceApi.ts,IntelligencePage.tsx}`

#### 未完成

- AI 建议为建议性记录（规则引擎最终裁决）；API Key 加密与人工反馈写入入口待接入

### 2026-08-01 V2.1 RC2 智能调查闭环与自主决策引擎

#### 新增

- 调查闭环（`internal/intelligence`）：**Task Queue**（7 任务类型 / 优先级 / 状态流转 / 幂等去重）、**Observation Engine**（新地址 / 新路径 / 新交易 / 风险事件，记忆去重）、**Decision Engine**（Path/Risk/Entity/Expansion 四评分 → EXPAND / STOP / DEEP_ANALYSIS；智能停止：轮次 / 时间 / 地址数 / 无新发现 / 低价值 / 交易所 / 重复关系）、**Loop Engine**（多轮 `规划→执行→观察→判断→重新规划`，收尾 VERIFYING→REPORTING→COMPLETED）
- 状态机新增 RUNNING / VERIFYING；配置新增 `max_rounds / max_runtime_ms / max_addresses / expansion_threshold`（部分更新 + 钳制）
- 调查记忆新增 `CompletedTasks`；报告新增「调查过程（闭环追踪）」章节（§18 可追踪）
- 前端「调查流程」页签：Steps（规划→执行→发现→决策→完成）/ 决策卡片（四维评分 / 原因 / 下一轮目标）/ 轮次记录 / 任务队列 / 观察列表 / 停止原因

#### 验证

- `go test ./... -short` 38 包零回归；`go vet ./internal/...` 零警告；intelligence 覆盖率 67.4% → 74.2%
- 闭环测试：3 轮 `EXPAND→EXPAND→STOP`、无候选单轮停止、最大轮次停止、缺依赖任务 skipped 降级
- 前端 `npm run build` 通过（3908 modules）

#### 修复（代码审查后）

- 共享扩展队列污染 + 配置竞争 → Expand 仅返回本次新发现条目（Depth>0 + 时间窗），不再写共享引擎配置
- 任务 Round 缺失致跨轮去重错误 → buildQueue 显式携带 Round；TaskQueue.Mark 终态流转守卫
- start 配置泄漏全局 → cfgOverride 仅本调查生效（不序列化）
- MaxAddresses 上限可绕过 → TotalDiscovered（记忆 + 候选）参与校验
- 新增 4 个回归测试（终态守卫/任务轮次/配置隔离/地址上限）

#### 修改文件

- 新增 `internal/intelligence/{task_queue,observation,decision_engine,loop_engine}.go` + `loop_engine_test.go`
- 修改 `internal/intelligence/{types,memory,api_handler,report_agent,investigation_agent,entity_resolver}.go`
- 修改 `frontend/src/features/intelligence/{intelligenceApi.ts,IntelligencePage.tsx}`

#### 未完成

- AI 建议直接驱动规划（DeepSeek 输出 → 下一轮任务）尚未实现，DEEP_ANALYSIS 为规则触发

### 2026-08-01 V2.1 RC2 全自动链上调查平台（Intelligence Layer）

#### 新增

- Intelligence Layer（`internal/intelligence`）：Investigation Planner（调查任务清单）、Beam Search 资金追踪（双向/时间维度/Top K）、Path Ranking（金额+时间连续性+风险+关系−实体惩罚）、Risk Pattern Detector（快速转移/拆分/归集/大额进入/快速清空）、Entity Resolver（复用 dynamicinvestigation）、Expansion Engine、AI Context Builder + DeepSeek Client（真实 API）、Investigation Memory（JSON 持久化）、Report Agent（Markdown/HTML/JSON）
- REST API `/api/intelligence/*`：investigations 启动/列表/详情/报告/记忆、config
- 前端智能调查工作台（进度/AI 助手/ReactFlow 资金图谱/报告下载）+ 菜单"智能调查"

#### 修复

- 后台调查继承请求 ctx 导致 DuckDB 查询 context canceled → 独立 context.Background()
- 实体识别未接数据源（全 unknown）→ 接入 analyticsapi 画像信号
- 调查列表 active/history 重复 → 按 ID 去重

#### 验证

- go test ./... -short 38 包零回归；intelligence 27 用例 67.4% 覆盖率；vet 零警告
- DeepSeek 真实调用验证：API 200，ai_model=deepseek-v4-flash，12620ms，结构化输出（总结/洞察/建议/风险评价）
- 端到端：调查 COMPLETED，Beam Search 3 条 4 跳路径（score 88），实体 8 个分类正确，风险 MULTI_SPLIT，三格式报告生成

#### 修改文件

- 新增 `internal/intelligence/*.go`（13 源文件 + 4 测试）、`frontend/src/features/intelligence/*`（2 文件）
- `internal/api/handlers.go`、`internal/api/crypto_parquet_handlers.go`、`frontend/src/App.tsx`

### 2026-08-01 V2.1 RC2 动态地址扩展与智能采集路由引擎

#### 新增

- Dynamic Investigation Engine（`internal/dynamicinvestigation`）：地址发现队列状态机（DISCOVERED→SCORING→APPROVED→ACQUIRING→COMPLETED/IGNORED，JSON 持久化）、Expansion Score 评分（金额+风险+关联+活跃−实体惩罚）、实体识别（wallet/exchange/bridge/dex/router/contract/unknown + 标签库）、智能采集路由（SQD 增量逐级升级 / CSV 直链 / 仅保存关系）、数据等级 0-4
- REST API `/api/dynamic-investigation/*`：start/queue/approve/ignore/config/tasks/stats/entities
- 真实对接：AnalyticsSource（analyticsapi.Service）+ RealExecutor（parquetdownload.Manager + sqd.Client）

#### 修复

- config 部分更新（不再清零未传字段）
- 执行失败回退 IGNORED 记录原因
- 队列满时发现受 relations_per_address 约束

#### 验证

- go test ./... -short 37 包零回归；go vet 零警告；dynamicinvestigation 27 用例 75.6% 覆盖率
- 真实服务端到端：0xdead → 发现/评分/识别/路由全链路通过；SQD 拉取失败（网络环境）优雅降级

#### 修改文件

- 新增 `internal/dynamicinvestigation/*.go`（9 源文件 + 4 测试文件）
- `internal/api/handlers.go`、`internal/api/crypto_parquet_handlers.go`、`internal/parquetdownload/handler.go`

### 2026-08-01 V2.1 RC2 链数据采集"结果与清单"弹窗化

#### 修改

- 任务监控"结果与清单"改为按钮 + Modal 弹窗，弹窗内两个可折叠面板：分区清单 + 结果文件（下载列表）
- 移除 ResultFiles 内重复标题；无输出时显示空态

#### 验证

- npm run build ✅

#### 修改文件

- `frontend/src/features/crypto/CryptoParquetPanel.tsx`

### 2026-07-31 23:26 V2.1 RC2 菜单结构调整 + 风险分析页

#### 新增

- 菜单重组为 5 个一级菜单：Dashboard / 数据资产（数据集管理、数据下载[浏览器下载+Dune下载+链数据采集]、数据源管理、RPC节点管理）/ 链上分析（地址画像、地址区分、资金流分析、地址图谱、风险分析）/ 报告中心 / 系统设置（建设中）
- 风险分析页（RiskAnalysisPage）：风险地址总数 + 单地址风险评分/等级/原因/维度指标 + 评分说明
- 系统设置占位页（SystemSettingsPage）

#### 验证

- npm run build ✅（3906 模块）
- 真实服务：新 bundle 生效；0xdead 风险评分 72.01（高）；risk_addresses=51

#### 修改文件

- `frontend/src/App.tsx`
- `frontend/src/features/analytics/RiskAnalysisPage.tsx`（新增）
- `frontend/src/features/system/SystemSettingsPage.tsx`（新增）

#### 未完成

- 系统设置子项（服务状态/日志/配置/系统信息）待实现
- 风险分析暂为单地址查询，无风险地址列表接口

### 2026-08-01 01:20 V2.1 RC2 地址图谱页面卡死修复

#### 修复

- 后端 `/api/analytics/graph?limit=N`（默认 500）：degree Top N 子图裁剪 + truncated 标志 + id/address 兼容
- graphintel.Export 自动 ComputeMetrics（graph.json 含 degree/pagerank）
- 前端 fetchGraph(500) + 子图提示

#### 验证

- graph?limit=5 正确返回 Top 节点（degree 3973）+ 边；limit=500 仅 26KB
- npm run build ✅；go test 37 包零回归 ✅

#### 修改文件

- `internal/analyticsapi/service.go`
- `internal/graphintel/graph.go`
- `frontend/src/features/analytics/{analyticsApi,GraphPage}.tsx`

### 2026-08-01 01:00 V2.1 RC2 链上分析工作台与可视化系统

#### 新增

- 后端：`/api/analytics/dashboard` 概览 + `/graph` 图谱 + `/report/{file}` 下载（防穿越）
- 前端 4 页面：Dashboard（ECharts 趋势）/Address（画像/风险/资金流/路径）/Graph（ReactFlow 图谱）/Report（报告中心）
- App.tsx "链上分析工作台" 菜单组；echarts 依赖

#### 验证

- npm run build ✅；go test 37 包零回归 ✅
- 真实服务：dashboard/graph/report 全 200（html 62KB/docx 38KB/bundle 97KB/assets 917KB），穿越 403

#### 修改文件

- `frontend/src/features/analytics/*`（6 文件新增）
- `frontend/src/App.tsx`、`frontend/package.json`
- `internal/analyticsapi/service.go`

### 2026-08-01 00:00 V2.1 RC2 案件智能报告与证据链管理

#### 新增

- `casefile/report2.go`：7 部分 Markdown + HTML 报告 + 证据链（evidence_bundle.json，含 log_index 溯源）+ 事件分类时间线
- Case 模型：Title/ARCHIVED/Assets/NewCaseWithTitle + Run 集成资产快照
- `investigation.TraceEdge` 增加 LogIdx

#### 验证结果（2/2 PASS）

- 案件 COMPLETED 6.5s；7 部分报告 1ms；HTML 62KB；证据链 286 条（transfer 99 全可追溯）
- 单地址 4.2s（秒级）、多地址 5.1s、批量 1ms；可复现

#### 修改文件

- `internal/casefile/{case,report2,report2_test}.go`
- `internal/investigation/workflow.go`
- `benchmark/snapshots/case-full/*`

### 2026-07-31 23:30 V2.1 RC2 Token 余额与资产快照系统

#### 新增

- `internal/balance`：BalanceEngine（hex 精确解析 + 全量索引 O(1) 查询）+ AssetSnapshot（历史最高/时间线/大额/快速清空）+ AssetRisk（liquidation_signal）+ Token 元数据映射 + CSV/JSON 输出
- `benchmark/balance-report.json` + `snapshots/{balances.csv, balance_timeline.csv, asset_summary.json}`

#### 验证结果（3/3 PASS）

- 余额守恒（balance=in−out）；快照 69 Token/时间线 3,277/历史最高 69/快速清空 1
- USDT 风险 High（change_rate=0.99 liquidation）
- 性能：50K 地址 **30ms**（优化后 9.7s→30ms，快 33 倍）

#### 修改文件

- `internal/balance/balance.go` + `balance_test.go`（新增）

### 2026-07-31 23:00 V2.1 RC2 地址图谱与关系网络分析系统

#### 新增

- `internal/graphintel`：图构建（Transfer/Interaction 聚合）+ 核心分析（Degree/Weighted/PageRank/连通分量）+ 风险网络（中转/归集/分散）+ 邻域查询 + CSV/JSON 输出
- `benchmark/graph-report.json` + `snapshots/{graph.json, nodes.csv, edges.csv, clusters.csv}`

#### 验证结果（3/3 PASS）

- 节点 15,595 / 边 21,693；聚合 tx_count 45,917 == Parquet 非自环 45,917（可追溯）
- PageRank 最大 0.114；簇 796 个（最大 12,092）；风险网络三模式各 10
- 邻域查询 11ms（11,589 节点）；图构建 2.5s；可复现

#### 修改文件

- `internal/graphintel/graph.go` + `graph_test.go`（新增）

### 2026-07-31 22:30 V2.1 RC2 案件分析与资金追踪报告生成系统

#### 新增

- `internal/casefile` 包：Case 状态机 + 多目标调查 + 公共来源/去向 + 时间线 + 关系图
- 报告三格式：case-report.md/json + **DOCX（python-docx，仿宋小四无横线）**
- 证据：evidence.json（四类）/graph.json/timeline.csv
- `tools/report/docx_report.py` 脚本

#### 验证结果（2/2 PASS）

- 案件闭环 5.8s COMPLETED（2 目标：路径 100/关联 20/图 60 节点 116 边）
- DOCX 生成成功（38KB，有效 zip）；证据完整；JSON/MD 一致；可复现
- 性能：单案件 5.0s、10 并发无错、100 批量缓存命中

#### 修改文件

- `internal/casefile/{case,report,case_test}.go`（新增）
- `tools/report/docx_report.py`（新增）
- `benchmark/case-reporting-report.json` + `snapshots/case-demo/*`

### 2026-07-31 22:00 V2.1 RC2 调查工作流与资金追踪系统验证

#### 新增

- `internal/investigation` 包：Investigate（单地址全流程）/TraceFunds（多跳 BFS）/DiscoverRelations（Jaccard）/RiskScenario（大额转入→快速转出→分散）/GenerateReport（证据三件套）
- `workflow_test.go`：5 项验证（单地址/追踪/关联/风险/可复现+性能）
- `benchmark/investigation-report.json/.md` + `snapshots/{evidence.json, paths.csv, related_addresses.csv}`

#### 验证结果（5/5 PASS）

- 单地址调查 **1.5s**（tx=1662、risk=72 高、paths=20、related=5）
- 多跳追踪 50 条路径无环；关联 Top score 0.5
- 风险模式=大额转入-快速转出-多地址分散
- 性能：100 地址 69ms/个、1000 地址 61ms/个；可复现 ✓

#### 修改文件

- `internal/investigation/workflow.go` + `workflow_test.go`（新增）

### 2026-07-31 21:30 V2.1 RC2 业务查询 API 与分析服务验证

#### 新增

- `internal/analyticsapi` 包：profile/flows/path/risk 4 查询 + 批量画像 + 缓存（命中计数）
- 路由 `/api/analytics/*`（避开既有 `/address/*`）
- `service_test.go`：正确性（API==SQL）/缓存/性能/并发 4 类验证

#### 验证结果（全部 PASS）

- 正确性：API==SQL 13,746、missing 空、可复现、flows in/out、path 无自环、risk 72
- 缓存：miss→hit；性能：50K 批量 **172ms**（<1s 目标）；并发 100 错误 0
- 真实 HTTP 服务实测通过（profile/risk/flows）

#### 修复

- flows UNION LIMIT 截断；counterparty 方向取反

#### 修改文件

- `internal/analyticsapi/service.go` + `service_test.go`（新增）
- `internal/api/handlers.go`、`crypto_parquet_handlers.go`
- `benchmark/api-service-report.json/.md`

### 2026-07-31 21:00 V2.1 RC2 地址画像与资金流分析模型验证

#### 新增

- `analytics_model_test.go`：5 个模型测试（画像/行为/资金流+路径/分类+风险/性能）
- `benchmark/analytics-model-report.json/.md`：全阶段合并报告

#### 验证结果（全部 PASS）

- 画像：16,411 地址，140ms，可复现
- 行为：日/周/月活跃 + 交互关系（65-67ms）
- 资金流：49,031 Transfer 边，P95 大额，中转/聚集/两跳路径
- 风险：分类覆盖率 100%，top_holder_ratio=0.299，counterparty_score=0.003
- 性能：50K 地址画像 87ms

#### 技术要点

- topic 32 字节 padded 地址归一化（substr 27）
- hex 金额 math/big 解析；跨测试报告合并

#### 修改文件

- `internal/downloadengine/analytics_model_test.go`（新增）

### 2026-07-31 20:45 V2.1 RC2 DuckDB Analytics Benchmark

#### 新增

- `duckdb_benchmark_test.go`：8 类 12 场景（扫描/画像/多地址/Token流向/时间范围/聚合/并发/字段裁剪）
- `benchmark/duckdb-report.json/.md` + `snapshots/`

#### 性能结果（49,031 行 logs.parquet，全部 PASS）

- 扫描 856,688 rows/s；50K 地址 SEMI JOIN 77ms
- Token 流向/时间范围/聚合排行 61-77ms
- 10 并发 295ms（平均 30ms/查询）
- 字段裁剪加速 78.4%（176ms vs 815ms）

#### 技术要点

- SEMI JOIN + 临时 CSV 规避命令行长度限制
- VARCHAR cast（TRY_CAST）、并发独立临时目录防 db 锁

#### 修改文件

- `internal/downloadengine/duckdb_benchmark_test.go`（新增）

### 2026-07-31 20:35 V2.1 RC2 Data Integrity Verification

#### 新增

- `data_integrity_test.go`：3 个离线测试（一致性 / 损坏检测 / 增量追加）
- `benchmark/integrity-report.json/.md` + `sqd-200k-warehouse/integrity-manifest.json`

#### 验证结果（PASS）

- source=parsed=unique=parquet=duckdb=distinct = **49,031**（dup=0）
- Schema 11 列完整，SHA256 checksum 持久化
- 损坏检测：翻转 1 字节 → checksum 失配
- 增量：A-B 29,418 + B-C 19,613 → 合并 49,031 == 全量唯一，只追加不重复

#### 修改文件

- `internal/downloadengine/data_integrity_test.go`（新增）

### 2026-07-31 19:35 V2.1 RC2 200K 地址真实链生产验证

#### 新增

- `sqd_200k_stress_test.go`：200K 地址 / 2,000 chunks / 4 元组唯一键 / Checkpoint 断点续传 / Parquet + DuckDB 验证
- 收集器动态区块起点（ResolveDateRange），地址库 77K → 248,928
- Parquet 链路修复：read_csv 显式 quote/escape、data 清洗、header 跳过

#### 真实 200K 下载结果（PASS）

- 2,000/2,000 chunks 完成，31m36.6s
- raw=62,805 → unique=49,031 → **Parquet 49,031 行 → DuckDB verified=true**
- 6 次 503 全部自动重试成功（任务不中断），Workers NORMAL / Circuit NORMAL
- 报告：`benchmark/sqd-200k-report.json/.md`

#### Checkpoint 恢复演练

- 231 chunks 处 kill → 重启从 232 继续，543/543 全部恢复，0 重复写入

#### 修改文件

- `internal/downloadengine/sqd_200k_stress_test.go`（新增）
- `internal/downloadengine/batch_collect_test.go`
- `stress-data/bsc_real/addresses_accumulated.csv`（248,928）
- `stress-data/bsc_real/sqd-200k-warehouse/logs.parquet`
- `benchmark/sqd-200k-report.json/.md`

### 2026-07-31 19:10 V2.1 RC2 10K 测试唯一键确认与 dup 来源拆解

#### 变更

- 日志唯一键升级：`block_number + transaction_hash + log_index` 三元组
- 新增 dup 来源拆解：`duplicate_logs_in_chunk` / `duplicate_logs_cross_chunk`
- 重跑真实 10K：PASS，数字与旧唯一键完全一致（52300/45198/7102）

#### 结论

- tx_hash 全局唯一 → 唯一键升级无数字变化（block_number 为防御性冗余）
- **全部 7,102 dup 来自跨 chunk 地址过滤重叠**（in_chunk=0，cross_chunk=7102）
- 应用层唯一化后写入 0 重复，Passed=true
- 保留非 short 真实测试行为（不隐藏 SQD 限流失败）

#### 修改文件

- `internal/downloadengine/sqd_10k_stress_test.go`
- `benchmark/sqd-10k-report.json` / `.md`

### 2026-07-31 18:15 V2.1 RC2 10K 地址真实链稳定性测试

#### 新增

- 10K 真实地址收集：修复 batch_collect_test（StreamTransactions 全量扫描），地址 17 → 20,420
- `sqd_10k_stress_test.go`：10K 地址分块下载 + 唯一性校验 + 报告（标记文件启用，默认 skip）
- Chunk 级重试（最多 3 轮）+ `waitForSQDAvailability` 冷却/熔断恢复等待
- `benchmark/sqd-10k-report.json` + `.md` 报告

#### 真实测试结果

- 10,000 地址 × 200 块：100/100 chunks 成功，52,300 日志（唯一 45,198），0 失败
- 耗时 1m21.7s，平均延迟 617ms，Workers NORMAL(8)，Circuit NORMAL
- **Passed: true（0 丢失 0 重复）**
- 首轮探测真实触发 503 → 冷却 → worker 降级 → 熔断保护（Reliability 验证成功）

#### 修改文件

- `internal/downloadengine/sqd_10k_stress_test.go`（新增）
- `internal/downloadengine/batch_collect_test.go`
- `stress-data/bsc_real/addresses_accumulated.csv`
- `benchmark/sqd-10k-report.json/.md`（新增）

#### 验证

- `go test ./internal/...` 全量通过（10K 测试默认 skip，零回归）

### 2026-07-31 18:00 V2.1 RC2 SQD Reliability 增强第二阶段（完善与接入）

#### 新增

- Adaptive Worker 渐进恢复：1→2→4→8 翻倍递增，缩放后重置成功计数
- Reliability Layer 正式接入 parquetdownload/manager.go（NewReliable + event log）
- Mock HTTP Server 测试 6 个：503 冷却/熔断/恢复、429 重试、timeout 熔断、成功重置
- sqd-events.log 大小轮转（默认 10MB，归档 sqd-events-<ts>.log）
- `GET /api/crypto/parquet/sqd/status` 调试接口（metrics/workers/breaker/cooldown）
- getJSON 接入 Circuit Breaker + Metrics（GET 请求也受保护）

#### 修改文件

- `internal/datasource/sqd/adaptive_workers.go`
- `internal/datasource/sqd/mock_test.go`（新增）
- `internal/datasource/sqd/sqd_events.go`
- `internal/datasource/sqd/client.go`
- `internal/datasource/sqd/reliability_test.go`
- `internal/parquetdownload/manager.go`
- `internal/parquetdownload/sqd_status.go`（新增）
- `internal/parquetdownload/handler.go`

#### 验证

- `go test ./internal/...` 全量通过，零回归
- `go build` 通过，服务重启成功
- 真实 SQD preview：sqd_available=true，block range 107153260→107345136
- 真实指标：request=3 success=3 latency=729ms

### 2026-07-31 15:45 V2.1 RC2 SQD Download Reliability 增强

#### 新增

- SQD ReliabilityConfig：retry(5次)/backoff(2s/5s/15s/30s/60s)/workers(8/4/1)/circuit(threshold=5,cooldown=60s)
- Circuit Breaker 状态扩展：NORMAL → DEGRADED → OPEN → HALF_OPEN
- Adaptive Workers 动态并发：8→4→1 降级，5次成功渐进恢复
- Provider Metrics：request/success/fail/retry/503/429/timeout/dns/network/latency/throughput
- SQD Event Log：独立 sqd-events.log，结构化事件记录
- Client 集成：5次重试+退避，HTTP 连接复用(MaxIdleConns:100)，错误分类
- Checkpoint WAITING_RETRY 状态 + MarkWaitingRetry

#### 新增文件

- `internal/datasource/sqd/reliability_config.go`
- `internal/datasource/sqd/adaptive_workers.go`
- `internal/datasource/sqd/metrics.go`
- `internal/datasource/sqd/sqd_events.go`
- `internal/datasource/sqd/reliability_test.go`

#### 修改文件

- `internal/datasource/sqd/circuit_breaker.go` — NORMAL/DEGRADED/OPEN/HALF_OPEN，冷却60s
- `internal/datasource/sqd/circuit_breaker_test.go` — CLOSED→NORMAL
- `internal/datasource/sqd/client.go` — relConfig/metrics/workers/events，增强postWithRetry，NewReliable，ensureTransport
- `internal/parquetdownload/sqd_checkpoint.go` — WAITING_RETRY + MarkWaitingRetry

#### 测试

- 24 个测试（8 旧 + 16 新）全部通过，零回归
- 覆盖：ReliabilityConfig、AdaptiveWorkers、ProviderMetrics、SQDEventLog、CircuitBreaker新状态

#### 验证

- `go test ./internal/...` 关键包全部通过
- `go build` 和 `npm run build` 通过
- 服务重启成功，health check 正常

### 2026-07-30 21:30 V1.5.0 地址首次时间开关

#### 前端

- **开关**：`Switch` 组件，`Form.Item name="use_first_seen"`，默认 `true`
- **首次时间查询**：地址输入后自动 `GET /api/crypto/addresses/{chain}/{address}/first-seen`，`firstSeenFetchedRef` 缓存
- **状态展示**：`found`(绿) / `partial`(橙) / `not_found`(灰) / `temporarily_unavailable`(黄) / `failed`(红)
- **日期选择器**：`DatePicker showTime`，开关开启时 `disabled`；关闭时可编辑
- **传参变更**：`searchAddress` 构建 `AddressQueryParams` 传递 `use_first_seen` / `start_time` / `end_time`

#### 后端

- 新增路由 `Any /api/crypto/addresses/:chain/:address/first-seen` → `HandleFirstSeen` → 反向代理到 `parquetDownload`（bsc_analytics）

#### 修改文件

- `frontend/src/features/crypto/addressAnalyticsApi.ts`
- `frontend/src/features/crypto/AddressAnalyticsPanel.tsx`
- `frontend/src/features/crypto/address-analytics.css`
- `internal/api/handlers.go`
- `internal/api/crypto_parquet_handlers.go`

### 2026-07-30 19:30 V1.4.3 虚拟币面板描述文字精简 & 错误弹窗化

#### 删除的描述文字

- **CryptoParquetPanel**: header 副标题、V1.3 多链地址分析数据层 Alert（含长描述）、section 副标题（任务配置/任务监控）、AWS 分区表格头描述、SQD Alert
- **AddressAnalyticsPanel**: header 描述段落
- **CryptoDownloadPanel**: header 描述段落
- **DataSourcePage**: header 描述 + 健康事件描述 + SQD/AWS/RPC 分工 Alert

#### 错误处理弹窗化

- `job.error` 内联 Alert → `notification.error({ placement: 'topRight', duration: 0 })`
- `canceling` 内联 Alert → `notification.warning({ placement: 'topRight', duration: 4 })`
- 使用 `useRef` 防重复弹窗（`lastErrorRef` / `lastCancelNotifyRef`）

#### 数据覆盖提示弹窗化

- `DataSourcePage` SQD/AWS/RPC 分工说明 Alert → 首次进入页面 `notification.info`（持续6秒）
- `AddressAnalyticsPanel` 数据覆盖不完整 Alert → 首次分析地址 `notification.warning`（含 Coverage%，持续6秒）
- 均用 `coverageNotifyRef` 防重复

#### 修改文件

- `frontend/src/features/crypto/CryptoParquetPanel.tsx`
- `frontend/src/features/crypto/AddressAnalyticsPanel.tsx`
- `frontend/src/features/crypto/CryptoDownloadPanel.tsx`
- `frontend/src/features/crypto/datasource/DataSourcePage.tsx`

### 2026-07-30 19:05 V1.4.2 SQD Provider 高可用与任务调度修复

#### 新增

- 状态机新增 `NO_AVAILABLE_WORKERS` / `RECOVERING` 状态，Source/sourceRuntime 新增 Worker503Count/LastRecoveryAt/CurrentTasks
- 错误分类细化：503 No workers 独立识别 → `StatusNoAvailableWorkers`
- SQD Client 冷却退避机制：503→30s→60s→120s→10min 递增，cooldown 期间直接拒绝
- Circuit Breaker：三态熔断器（CLOSED/OPEN/HALF_OPEN），集成到 postWithRetry
- SQD Scheduler 新包：优先级队列 + 并发限制（max_parallel_streams=1, max_large_jobs=1, max_small_jobs=2）
- Checkpoint 断点续传：SQDCheckpointStore 支持自动分块/AdvanceChunk/MarkFailed/恢复续跑
- Manager 新增 UpdateTaskCount/Update503Count/RecordRecovery 方法

#### 新增文件

- `internal/datasource/sqd/circuit_breaker.go`
- `internal/datasource/sqd/circuit_breaker_test.go`
- `internal/datasource/sqd/scheduler/scheduler.go`
- `internal/datasource/sqd/scheduler/scheduler_test.go`
- `internal/parquetdownload/sqd_checkpoint.go`
- `internal/parquetdownload/sqd_checkpoint_test.go`

#### 修改文件

- `internal/datasourcemanager/types.go`
- `internal/datasourcemanager/manager.go`
- `internal/datasource/sqd/client.go`

#### 测试

- 19 个新测试全部通过，零回归
- CircuitBreaker(7)：初始状态/开放/半开恢复/半开再失败/重置/统计
- Scheduler(6)：提交/并发限制/优先级/取消/满队/统计
- Checkpoint(6)：创建加载/推进分块/标记失败/删除/分块算法/恢复续跑

### 2026-07-30 12:26 V1.3 地址分析状态与审计信息增强

#### 新增与调整

- 地址类型中文化并显示检测依据或未检测原因。
- Summary API 新增 RPC 配置状态、环境变量名和地址类型原因。
- 页面顶部新增未配置 RPC 黄色提示条，KPI 卡片新增统计口径 Tooltip。
- Address Activity 和 Parquet 新增 `status`，前端显示`成功/失败/未检测`。
- 流水表补齐交易哈希、类型、方向、资产、金额和状态。
- Token/NFT 表展示合约地址及 Metadata 中文状态。
- 地址、合约、交易对手和交易哈希新增复制和对应链浏览器跳转。
- 交易对手新增总方向及原生币/Token 流入流出活动计数。
- 去重优先保留信息更完整的交易状态记录，优化移动端检测原因布局。

#### 接口与数据

- API 路径不变。
- Summary 新增 `rpc_configured/rpc_env/address_type_reason`。
- Activity 新增 `status`。
- Counterparty 新增 `direction/native_in_count/native_out_count/token_in_count/token_out_count`。
- 无外部数据库变化，旧 Parquet 保持兼容。

#### 验证

- Go 全包测试、`go vet`、后端构建和前端生产构建通过。
- 真实 BSC 单区块 SQD 回归及新增字段审计通过。
- 真实地址 Summary、4 条 Activity 和 2 条 Counterparty 返回值符合预期。
- Playwright + Edge 桌面/移动端验证黄色提示、中文状态、Tooltip、剪贴板、外链、三类表格新增字段和响应式布局。
- 服务 PID `34444`，健康检查正常。

#### 边界

- 当前无链 RPC，真实页面保持`未检测`；Log 事件缺少 Receipt 状态时同样显示`未检测`，不推测。

### 2026-07-30 11:56 EVM 多链链上数据分析平台 V1.3

#### 新增

- 新增 SQD 多链 Transactions Adapter、RPC Token Metadata、原生币/Token Balance Snapshot、EOA/CONTRACT 地址类型识别。
- 新增 Method Signature 字典、Address Activity V2、Address Summary、Token/NFT/交易对手聚合。
- 新增 5 个 `/api/address/{address}/*` 查询接口。
- 新增 `虚拟币 -> 链上地址分析` 页面和移动端抽屉导航。
- Parquet 下载页扩展为 16 阶段并新增 Transactions、Metadata、Summary、Balance 统计。

#### 优化与修复

- 地址查询跨重试任务做业务键去重，避免同一活动重复统计。
- DuckDB CLI 执行加入互斥，修复前端并行加载地址接口时的文件锁 500。
- 前端 5 类数据使用并行请求，金额尾随零压缩，桌面与移动端均限制页面级溢出。
- 未配置 RPC 时 Metadata 保持 `UNKNOWN/UNAVAILABLE`、Balance 明确跳过，不推测链上数据。

#### 接口与数据

- 新增 `summary/activity/tokens/nfts/counterparties` 五个地址接口。
- 新增 `transactions-sqd`、`token_metadata`、`method_signatures`、`address_summary`、`balances` Parquet。
- `address_activity` 新增 `amount_raw/amount/method_id/trace_depth/source`。
- 所有 V1.3 新表包含 `chain_key/chain_id`；无外部数据库变化。

#### 验证

- 全部 Go 内部包测试、`go vet`、后端构建和前端生产构建通过。
- 真实 SQD BSC 区块 `112932400` 验证 4 笔交易、4 条日志、4 条 Token Transfer、52 条 Trace 和完整 V1.3 Parquet 产物。
- 真实地址结果为 Summary 2 笔交易/2 Token/2 对手、Activity 4、Token 2、NFT 0、Counterparty 2。
- 五个地址接口真实并发请求全部 HTTP 200。
- Playwright + Edge 桌面/移动端验收通过，无页面横向溢出及 console error/warn。
- 服务最终重启 PID `30216`，健康检查正常。

#### 边界

- 当前未配置四条链 RPC，Token Metadata、Balance、EOA/CONTRACT 的真实 RPC 请求待环境变量可用后补做；模拟 RPC 已覆盖完整逻辑。
- 未下载整日大分区，继续采用 finalized 单区块作有界真实验证。

### 2026-07-30 11:20 EVM 完整链上数据层 V1.2 第二阶段

#### 新增

- 新增 BSC、Ethereum、Base、Arbitrum One 的 SQD dataset 配置。
- 新增 SQD finalized-stream 客户端，支持日期转区块、服务端地址过滤、NDJSON 续读、限速退避和真实 Schema 探测。
- 新增 ERC20/BEP20、ERC721、ERC1155 标准事件解析。
- 新增 Trace 标准化和内部交易派生。
- 新增 `logs`、`token_transfers`、`nft_transfers`、`traces`、`internal_transactions` 和 SQD `address_activity` Parquet 输出。
- 新增真实网络单区块集成测试 `TestLiveSQDOneFinalizedBlockToParquet`，默认跳过，通过环境变量显式运行。

#### 变更

- `/api/crypto/parquet/preview` 与 `/api/crypto/parquet/start` 支持 `selected_sources`，API 路径不变且未传字段时保持 transactions 默认行为。
- Receipt、合约创建、原生币活动表补齐 `chain_key`，跨表关联使用链标识与交易哈希复合键。
- Parquet 下载前端开放 BSC、Ethereum、Base、Arbitrum，增加 Transactions、Logs、Trace 数据层选择。
- 任务进度增加 Transfer Logs、Token/NFT、Trace/内部交易阶段；任务统计增加各链上数据表行数。

#### 验证

- 后端全部内部测试、`go vet ./...`、后端构建和前端生产构建通过。
- 真实 SQD BSC 区块 `112932400` 返回 4 条目标 Transfer Log、4 条 BEP20 Transfer、52 条 Trace，生成的 6 个 Parquet 全部有效。
- DuckDB 复核结果表的 `chain_key=bsc`，`chain_id` 类型为 `UBIGINT`。
- Playwright 桌面/移动端 UI 检查通过，网络切换、数据层联动、12 阶段进度区和控制台健康均正常。
- 服务已通过 `.\run.ps1` 重启，PID `7296`，健康检查和真实 SQD 预检 API 正常。

#### 未完成

- Token metadata RPC 富化待后续接入；当前 `amount_raw` 可审计，未知 symbol、decimals 和换算金额保持空值。
- Ethereum、Base、Arbitrum 的原生 transactions 数据源待后续接入；当前可使用 SQD Logs/Trace。

### 2026-07-30 10:43 EVM 多链分析平台 V1.1 第一阶段

#### 本次任务
- 按 V1.1 开发计划继续升级现有 Parquet 下载模块，形成可扩展 EVM 数据采集与标准化架构。

#### 修改文件
- 新增 `internal/chain/evm.go`、`internal/chain/chain_test.go`
- 新增 `internal/datasource/datasource.go`
- 新增 `internal/datasource/aws/transactions.go`
- 新增 `internal/datasource/rpc/receipts.go`、`internal/datasource/rpc/receipts_test.go`
- 新增 `internal/datasource/sqd/logs.go`
- 新增 `internal/normalize/models.go`
- 新增 `internal/parquetdownload/analytics.go`
- 修改 `internal/parquetdownload/types.go`
- 修改 `internal/parquetdownload/manager.go`
- 修改 `internal/parquetdownload/process.go`
- 修改 `internal/parquetdownload/s3.go`
- 修改 `internal/parquetdownload/paths.go`
- 修改 `internal/parquetdownload/parquetdownload_test.go`
- 修改 `frontend/src/features/crypto/cryptoParquetApi.ts`
- 修改 `frontend/src/features/crypto/CryptoParquetPanel.tsx`
- 修改 `docs/AI_HANDOFF.md`
- 修改 `docs/CHANGELOG_AI.md`

#### 新增或变更功能
- 新增 BSC/ETH Chain Adapter，transactions 输出不再硬编码 BSC 链参数。
- 新增 AWS transactions 数据源适配层、RPC Receipt 数据源和 SQD Logs 预留接口。
- Receipt 先校验 RPC chain_id，再探测真实回执必需字段。
- 新增 `transaction_receipts`、准确 `contract_creations` 和 `address_activity` Parquet 产物。
- 只有候选交易与成功 Receipt 中非空 contractAddress 的交集才能确认为合约创建。
- 新增 Receipt/合约创建/统一流水进度阶段和任务统计。
- 默认并强制使用 `E:\codex\bsc_analytics`，所有临时文件也留在业务盘并在使用后删除。
- Token Transfer 只建立类型和 SQD 接口边界，当前明确不支持。

#### 接口与存储
- API 路径不变。
- preview 响应增加 Receipt 可用性；start 请求增加 `include_receipts`；job 增加三类统计；settings 增加 `receipt_batch_size`。
- 无外部数据库变化。
- 新增 `warehouse/transaction_receipts`、`warehouse/contract_creations`、`warehouse/address_activity` 链级分区目录。

#### 验证结果
- 全部后端测试、`go vet ./...`、Go 构建和前端生产构建通过。
- 本地 DuckDB 以 3 条源交易、2 条命中验证 1 条准确合约创建和 2 条地址统一流水。
- 模拟 RPC 验证网络校验、Receipt Schema 探测、标准化和错误网络拒绝。
- 真实 AWS 预检继续确认 2026-07-28 分区为 5,951,495,515 字节，chain_id=56。
- 未配置 `BSC_RPC` 的 Receipt 启动请求返回 HTTP 400。
- Playwright + Edge 验证桌面/移动端无页面级横向溢出、无控制台错误，9 阶段进度和 Receipt 禁用提示可见。
- `run.ps1` 重启成功，PID `13728`，健康检查正常。

#### 未完成事项
- 当前环境无 `BSC_RPC`，公共 RPC Receipt 真实批量请求待配置后执行。
- SQD Logs、Token Transfer、Token Metadata、资产汇总属于 V1.2。
- ETH Chain Adapter 已完成，ETH transactions 数据源尚未接入。
- 未下载完整 5.95 GB 公网分区。

### 2026-07-30 01:57 EVM Parquet 批量下载与资金筛选接入

#### 本次任务
- 将 EVM 多链资金分析设计中的首阶段 BSC Parquet 能力接入 `虚拟币 -> Parquet下载`。

#### 修改文件
- `internal/parquetdownload/`（新增任务、发现、下载、续传、Schema、DuckDB 筛选、检查点、文件下载与测试）
- `internal/api/crypto_parquet_handlers.go`
- `internal/api/handlers.go`
- `frontend/src/App.tsx`
- `frontend/src/features/crypto/CryptoParquetPanel.tsx`
- `frontend/src/features/crypto/cryptoParquetApi.ts`
- `frontend/src/features/crypto/crypto-parquet.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 新增或变更功能
- 新增 BSC AWS Parquet 日期范围预检和匿名分区发现。
- 新增 `.partial` Range 断点续传、文件校验、限并发下载、单 DuckDB 写入、取消和检查点重试。
- 新增地址批量清洗/去重和 XLSX/CSV/TXT 导入。
- 新增运行时 Schema 探测、from/to 双 SEMI JOIN 批量匹配、ZSTD Parquet/可选 CSV。
- 新增真实总体进度、文件进度、字节、速度、ETA、源/命中计数、历史与结果清单。
- 新增非系统盘强制校验、150 GB 保留空间和处理后清理 staging。
- 检查点与输出文件名加入地址批次哈希，防止跨批次误复用。

#### 接口、数据库、前端变化
- 新增 `/api/crypto/parquet/settings|preview|start|job|jobs|cancel|retry|addresses/upload|file`。
- 无外部数据库变化；新增 `backend/data/crypto_parquet` 文件任务目录结构。
- `虚拟币` 菜单新增 `Parquet下载` 页面，桌面和移动端自适应。

#### 验证结果
- Parquet 专项测试、全部后端测试、go vet、Go build 和前端生产构建通过。
- 本地 DuckDB 端到端以 2 条源交易验证 1 条目标命中并生成 Parquet/CSV。
- 真实 AWS 预检确认 2026-07-28 transactions 分区大小 5,951,495,515 字节并成功远程探测 Schema。
- C 盘设置接口返回 HTTP 400 且未污染现有设置。
- Playwright + Edge 验证 1440x960、390x844 和 42% 运行态；页面无横向溢出，进度条/取消/结果清单可见。
- `run.ps1` 重启成功，PID `11564`；健康检查 `status=ok`。

#### 未完成事项
- 未实际下载完整 5.95 GB 公网分区；真实目录/Schema/体量已核验，本地完整处理链路已通过。
- AWS BNB 当前公开源只有 blocks/transactions；BEP-20 logs、Trace、receipts、RPC 当前状态和 ETH Adapter 留待后续数据源阶段。

### 2026-07-27 13:31 虚拟币全链路真实下载与 CSV 回退验收

#### 本次任务
- 使用 `0x57136ea9b2be6cd4ad74c3ca5b24172f87c9cb8d` 真实测试 RPC、浏览器、邮件 CSV、DeepAML 和交易所地址过滤。
- 保留“小数据先直连、失败后自动切浏览器抓取”，重点修复并验收 CSV。

#### 修改文件
- `internal/cryptodownload/csv_scraper.go`
- `internal/cryptodownload/csv_hydration.go`
- `internal/cryptodownload/csv_raw_durable.go`
- `internal/cryptodownload/gui.go`
- `internal/cryptodownload/gui_pause.go`
- `internal/cryptodownload/tools/oklink_browser_email.mjs`
- `internal/cryptodownload/csv_hydration_test.go`
- `internal/cryptodownload/csv_mail_config_test.go`
- `internal/cryptodownload/gui_pause_test.go`
- `internal/cryptodownload/source_parity_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 新增或变更功能
- CSV 保持先直连；只有永久签名失败/邮件超时且没有直连数据时才使用浏览器回退。
- 邮件请求固定使用配置邮箱，不生成别名地址。
- 浏览器邮件请求改为标准 Chrome 会话并修复资源匹配正则。
- CSV 检查点增加完成语义，避免恢复时重复下载已完成末段。
- 修复自定义 CSV 时间段误触发旧任务全历史校验。
- 断点继续会清除已解决错误并重置失败分项；任务结束同步更新分项状态。

#### 接口、数据库变化
- 无新增 API。
- 无数据库变化；仅文件系统任务历史、检查点与真实下载结果发生变化。

#### 真实验证结果
- CSV 任务 `75e4918abb355b16` 完成：交易 CSV 8 行，代币 CSV 10 行，报告报错 0 行；真实邮件链接下载成功。
- 浏览器 BSC 任务 `8a72f74633c64034` 完成，下载量 1,177。
- RPC BSC/ETH 最新块任务 `817499be4673b89e`、`499fcf4dbd7dca21` 完成。
- DeepAML 实际请求成功；该地址及两个对手无标签，因此交易所过滤 0 条。
- 历史实际交易 RPC 命中数据，但公共节点缺少 archive/trace 能力时严格显示失败。
- 结果文件 HTTP 下载返回 200；历史删除只删记录不删文件。
- Playwright 证实历史重新导入确认前不会发送导入请求。

#### 验证命令
- CSV 回退、检查点、暂停恢复专项测试通过。
- `go test ./internal/...`、`go vet ./...`、Go 构建和前端生产构建均通过。
- `git diff --check` 无空白错误。
- `run.ps1` 重启成功，PID `32084`；健康检查及重启后的历史导入确认测试通过。

#### 未完成事项
- OKLink 直连 CSV 仍返回 `50113`，但自动浏览器邮件回退已跑通。
- 公共 BSC RPC 节点不支持部分历史状态和 `trace_filter`。
- ETH 浏览器摘要偶发截断 JSON，RPC 最新块模式不受影响。

### 2026-07-27 12:49 历史导入确认、失败状态与结果文件下载修复

#### 本次任务
- 修复历史导入未经确认直接启动、HTTP 403 仍显示完成及结果文件无法打开。

#### 修改文件
- `internal/cryptodownload/api_handler.go`
- `internal/cryptodownload/gui.go`
- `internal/cryptodownload/gui_result_file.go`
- `internal/cryptodownload/gui_result_file_test.go`
- `frontend/src/features/crypto/cryptoDownloadApi.ts`
- `frontend/src/features/crypto/CryptoDownloadPanel.tsx`
- `frontend/src/features/crypto/crypto-download.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 新增或变更功能
- 历史单条/批量导入增加明确确认步骤，确认前不会创建下载任务。
- RPC/浏览器采集出现错误后，地址和任务均标记失败，不再显示完成。
- 失败任务生成的文件标记为诊断文件，不再显示绿色成功通知。
- 结果文件通过任务授权的 HTTP 接口下载，替代浏览器受限的 `file://`。
- “完成地址”调整为“已处理地址”。

#### 接口、数据库变化
- 新增 `GET /api/crypto/download/file?id=...&path=...` 受控结果附件下载接口。
- 无数据库变化。

#### 验证结果
- 虚拟币下载专项测试、全部后端测试和前端生产构建通过。
- 后端经 `run.ps1` 重启成功，PID `38796`，健康检查通过。
- Playwright 证实重新导入先显示确认框，确认前未发送导入请求。
- 真实 14,945 字节 XLSX 经新接口返回 HTTP 200、正确 MIME 和附件名，且文件结构有效。
- 未授权文件请求返回 HTTP 403。

#### 未完成事项
- 截图任务 `3954bf73db7cc49d` 已不在当前任务/历史持久化中，无法直接检查其原始输出文件。

### 2026-07-27 12:40 历史任务导入与删除

#### 本次任务
- 参考原项目，为虚拟币下载历史任务补齐导入、删除和断点继续。

#### 修改文件
- `frontend/src/features/crypto/cryptoDownloadApi.ts`
- `frontend/src/features/crypto/CryptoDownloadPanel.tsx`
- `frontend/src/features/crypto/crypto-download.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 新增或变更功能
- 最近任务改为读取文件系统持久化的完整历史列表。
- 支持单条重新导入、勾选多条批量导入和全选。
- 暂停/冷却历史记录支持断点继续。
- 支持删除单条历史记录，删除前明确提示导出文件会保留。
- 各历史操作完成后同步刷新运行任务与历史记录。

#### 接口、数据库变化
- 无新增后端接口；前端接入既有 `/api/crypto/download/history`、`/history/import` 和 `/history/resume`。
- 无数据库变化。

#### 验证结果
- 前端 TypeScript 和 Vite 生产构建通过，产物为 `assets/index-Vw8Ib83Z.js`、`assets/index-DEzPJ5cj.css`。
- Playwright 实测 10 条历史记录、默认折叠、全选与批量导入按钮状态、10 个单条导入/删除入口、1 个断点继续入口及删除保留文件提示均通过。
- 未实际确认导入或删除，避免改变现有任务和历史数据。

#### 未完成事项
- 无。

### 2026-07-27 12:34 结果/错误通知与最近任务折叠

#### 本次任务
- 将底部结果、错误、最近任务卡片替换为通知和折叠显示。

#### 修改文件
- `frontend/src/features/crypto/CryptoDownloadPanel.tsx`
- `frontend/src/features/crypto/crypto-download.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 新增或变更功能
- 结果文件改为持久成功通知并提供打开按钮。
- 错误改为持久错误通知和可展开摘要。
- 最近任务移入任务状态区并默认折叠。
- 最近任务行显示任务 ID、消息、状态和进度。
- 同一任务通知更新复用，最多显示 4 条。

#### 验证结果
- 前端构建通过。
- Playwright 实测旧卡片移除、最近任务默认折叠/展开 8 条、结果和错误通知及打开按钮均通过。

#### 接口、数据库变化
- 无。

### 2026-07-27 12:28 下载设置分类弹窗

#### 本次任务
- 将地址栏下方设置分类收纳到右上角弹窗。

#### 修改文件
- `frontend/src/features/crypto/CryptoDownloadPanel.tsx`
- `frontend/src/features/crypto/crypto-download.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 新增或变更功能
- 右上角新增“下载设置”按钮。
- 设置按下载方式、RPC/CSV、性能风控、输出处理、DeepAML 分类。
- RPC 与 CSV 分类随数据源动态切换。
- 主页面地址区只保留地址输入、逐地址选链和开始下载。

#### 验证结果
- 前端构建通过。
- Playwright 实测设置按钮、分类切换和多地址弹窗回归均通过。

#### 接口、数据库变化
- 无。

### 2026-07-27 12:22 多地址粘贴即时弹窗修复

#### 本次任务
- 修复只输入或粘贴多个地址时没有立即弹窗的问题。

#### 修改文件
- `frontend/src/features/crypto/CryptoDownloadPanel.tsx`
- `frontend/src/features/crypto/crypto-download.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 新增或变更功能
- 粘贴多个地址立即弹出逐地址选链。
- 新增“确认多地址链”按钮。
- 确认结果写回为逐行 `地址,链`，不会提前启动任务。
- 未预先确认时，点击开始仍会兜底弹窗。

#### 验证结果
- 前端构建通过，运行态资源已更新。
- Playwright 实测两个地址自动弹窗、两行独立选链、ETH/BSC 写回、弹窗关闭且不启动任务均通过。

#### 接口、数据库变化
- 无。

### 2026-07-27 12:18 多地址逐一选链弹窗恢复

#### 本次任务
- 恢复原项目“多个地址开始前逐一确认链”的交互。

#### 修改文件
- `frontend/src/features/crypto/CryptoDownloadPanel.tsx`
- `frontend/src/features/crypto/crypto-download.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 新增或变更功能
- 多个唯一地址提交时弹出“确认地址和链”。
- 每行地址可独立选择 ETH、BSC、POLYGON、ARBITRUM、BASE、OP 或 AVAXC。
- 显式输入的链作为初始值；未指定链使用第一个默认链。
- 单地址保持直接启动。

#### 接口、数据库变化
- 无。

#### 验证结果
- 前端 TypeScript 和 Vite 构建通过。
- 8000 端口已提供新产物，弹窗关键文案已进入运行态 JS。

#### 未完成事项
- 无。

### 2026-07-27 12:11 DeepAML 真实端到端验收

#### 本次任务
- 使用临时 DeepAML Key 验证真实标签与交易所过滤链路。

#### 修改文件
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口、数据库、前端变化
- 无。

#### 验证结果
- DeepAML 真实接口返回 HTTP 200、业务码 200，测试地址识别为 `Binance/EXCHANGE`。
- 当前项目任务 `224e6dc51dcb8726` 完成：过滤前普通交易 3 行，导出后 2 行。
- 汇总过滤行数为 2，对应交易表和资金表各移除一行；目标地址/资产标签为 Binance，保留对手方标签为 Tether。
- 已生成主工作簿和 `下载情况.xlsx`。
- 本次 Key 未写入项目配置、任务持久化、日志或文档。

#### 未完成事项
- 无 DeepAML 功能未完成项。
- 历史余额与历史日志仍受当前公共 ETH RPC archive 403 限制，属于独立 RPC 能力问题。

### 2026-07-27 11:59 CSV 邮箱主机错位与暂停提示修复

#### 本次任务
- 修复真实 CSV 任务将 Gmail 地址当作 IMAP Host 查询，以及任意 CSV 错误都提示切换 VPN的问题。

#### 修改文件
- `internal/cryptodownload/main.go`
- `internal/cryptodownload/gui.go`
- `internal/cryptodownload/api_handler.go`
- `internal/cryptodownload/gui_pause.go`
- `internal/cryptodownload/csv_mail_config_test.go`
- `frontend/src/features/crypto/CryptoDownloadPanel.tsx`
- `run.ps1`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 新增或变更功能
- Gmail 主机误填邮箱时自动修正为 `imap.gmail.com:993`，空用户名自动使用接收邮箱。
- 启动和继续任务会从当前设置补齐未在 API 响应中暴露的授权码，并在发请求前校验邮箱配置。
- 旧暂停任务点击继续时会重新加载修正后的当前设置。
- IMAP/DNS、`50113` 和一般配置/网络失败分别显示准确提示。
- 前端阻止在 IMAP Host 中输入包含 `@` 的邮箱地址。
- `run.ps1` 默认始终重新构建，确保后端修改进入运行进程。

#### 接口变化
- `/api/crypto/download/start` 对无效 CSV 邮箱配置返回 HTTP 400。
- `/api/crypto/download/resume` 继续前刷新当前邮箱设置。
- 路径和响应结构不变。

#### 数据库变化
- 无。

#### 验证结果
- 目标测试、全量后端测试和 `go vet` 全部通过。
- 前端构建通过，产物 `assets/index-Dhbzya_u.js`。
- 后端已重新构建并重启，PID `34204`，健康检查通过。
- 运行态设置已纠正为 `imap.gmail.com:993`，IMAP 用户已配置，密码未在响应中暴露。
- 原任务保持暂停并可继续；未自动重发外部请求。

#### 未完成事项
- 点击继续后若 OKLink 仍返回 `50113`，需刷新有效会话/请求签名。

### 2026-07-27 11:09 Gmail 邮箱 CSV 真实测试

#### 本次任务
- 使用用户临时提供的 Gmail 应用授权码验证真实 IMAP 连接。
- 复核当前项目与 `E:\codex\虚拟币` 的 CSV 邮箱下载源码一致性。

#### 修改文件
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口、数据库、前端变化
- 无。

#### 验证结果
- `imap.gmail.com:993` TLS 握手、Gmail 授权认证和退出均成功；未读取、删除或修改邮件。
- `csv_scraper.go`、`csv_browser_email.go`、`csv_static_strategy.go` 与原项目的业务逻辑一致；差异仅为 Go 包名和格式化空格。
- 两边均保持“小段直连、超限或直连不可用转邮箱、IMAP 匹配下载”的相同路由。
- 邮箱地址和授权码均未写入项目配置、任务记录、日志或文档。

#### 未完成事项
- 当前执行环境禁止自动注入邮箱身份或授权码启动 `/api/crypto/download/start`，所以 OKLink 邮件申请、收信匹配、链接下载这一段需要用户在页面本地填写后手动点击开始。

#### 注意事项
- 禁止使用 Gmail `+alias` 或账号轮换规避限流；遇到 `429`、`50113` 或风控必须按检查点和冷却策略处理。

### 2026-07-27 00:18 虚拟币下载与地址区分原项目一致性修复

#### 本次任务
- 将当前项目的虚拟币下载和地址区分功能与原项目契约对齐。

#### 新增功能
- 地址区分支持原脚本的 `EOA/CONTRACT/INVALID/ERROR`、状态、重试次数和错误信息。
- 下载页面补齐 RPC、CSV、DeepAML 高级参数。

#### 修改文件
- `internal/api/crypto_address_handlers.go`
- `internal/api/crypto_address_handlers_test.go`
- `internal/cryptodownload/gui.go`
- `internal/cryptodownload/source_parity_test.go`
- `frontend/src/features/crypto/CryptoAddressPanel.tsx`
- `frontend/src/features/crypto/cryptoAddressApi.ts`
- `frontend/src/features/crypto/CryptoDownloadPanel.tsx`
- `frontend/src/features/crypto/cryptoDownloadApi.ts`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- `/api/crypto/address-classify` item 新增 `status`、`retry_count`、`error`。
- `/api/crypto/download/start` 省略 `endBlock` 时默认 `-1`，不再错误使用区块 0。
- 接口路径不变。

#### 数据库变化
- 无。

#### 前端变化
- 地址区分页按原脚本字段展示和复制结果，默认 BSC 公共 RPC。
- 下载页补齐多链 RPC 配置、原生币符号、区块范围/批次、CSV 时间范围、DeepAML 和交易所过滤。

#### 验证结果
- 后端目标测试、全量测试和 `go vet` 全部通过。
- 前端构建通过，产物 `assets/index-KAqYElXl.js`。
- 真实 BSC EOA/合约结果与原 `查询.py` 的 checksum 地址、类型、状态、重试次数、错误信息一致。
- 真实 ETH 区块 `25618102` 下载：当前项目和原项目均得到交易 3、代币转账 2、内部交易 0、NFT 0、资产 2、错误 0。
- 两个 Excel 的 7 个 sheet、维度和全部交易类明细一致；只存在运行时间及实时余额自然变化。
- 后端已重启，PID 43996。

#### 未完成事项
- OKLink CSV 邮箱和 Browser 的真实平台验收需要有效邮箱/当前会话；本次完成同源代码和路由契约验证，未再次触发平台风控。

#### 注意事项
- 未复制原脚本私有 Chainstack URL，改用真实公共 BSC RPC 执行相同判断。

### 2026-07-26 23:57 虚拟币下载与地址区分真实地址对比测试

#### 本次任务
- 使用真实链上地址对比当前项目与 `E:\codex\虚拟币` 的下载实现和输出。
- 对比当前地址区分 API 与 `D:\app\桌面\新建文件夹 (2)\查询.py` 的 BSC EOA/合约判断。

#### 新增功能
- 无；本次为测试和一致性审计。

#### 修改文件
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`
- 新增测试产物位于 `backend/data/crypto_download/real_compare_20260726/`。

#### 接口变化
- 无。

#### 数据库变化
- 无。

#### 前端变化
- 无。

#### 验证结果
- 当前项目 `go test ./internal/... -count=1` 全部通过；原项目 `go test -count=1 ./...` 全部通过。
- 当前项目和原项目对真实 ETH 地址 `0x28c6c06298d514db089934071355e5743bf21d60`、区块 `25617962` 均下载到同一笔交易，交易哈希为 `0x0f455c9e1ac3e57fec7eb2c42f6b1305178795079c5195a9092ddf4b997a7886`。
- 两个 Excel 均包含 7 个同名 sheet，行列维度一致；剔除运行时间和运行时最新区块后，稳定字段及其余 sheet 单元格完全一致。
- 下载采集引擎静态核对未发现 RPC、CSV、浏览器、source 路由、checkpoint、重试和限速逻辑分叉；当前项目仅有包/导入路径和 Windows 长路径文件名适配。
- 原地址脚本将真实 BSC EOA 判为 `EOA`、USDT 合约判为 `CONTRACT`；当前 API 对两者均 RPC 验证成功，但都显示 `账户/合约地址`，确认地址类型判断不一致。
- 101 区块长测完成普通交易扫描 `101/101`、命中 277 行；该任务随后人工取消，不计作完整下载。
- 已重建并启动后端，PID 40336；`/api/health` 顶层返回 `status=ok`。

#### 未完成事项
- 需要后续修复地址区分 API，使其复用 `eth_getCode` 结果输出 `EOA`/`CONTRACT`。
- 长测出现一次 Windows 作业状态文件替换 `Access is denied`，最终持久化成功且单区块任务未复现，后续应继续做并发持久化压测。
- OKLink CSV/邮箱和浏览器模式尚未做本次真实平台端到端测试。

#### 注意事项
- 完整成功的当前项目文件位于 `backend/data/crypto_download/real_compare_20260726/small_exports/live_rpc_small_7fbcb1d7a591ecc8/`；原项目对照文件为 `backend/data/crypto_download/real_compare_20260726/original_rpc.xlsx`。
- 本次未修改业务代码。

### 2026-07-24 18:47 虚拟币下载选项精简

#### 本次任务
- 删除 `虚拟币 -> 数据下载` 页面的 `DeepAML 标签` 和 `过滤交易所大地址` 选项。
- 解释 `扫描原生交易` 的含义。

#### 新增功能
- 无。

#### 修改文件
- `frontend/src/features/crypto/CryptoDownloadPanel.tsx`
- `frontend/src/features/crypto/cryptoDownloadApi.ts`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无接口路径变化。
- 前端不再向 `/api/crypto/download/start` 发送 `amlKey`、`amlLabels`、`amlRps`、`filterExchange`。

#### 数据库变化
- 无。

#### 前端变化
- 删除 `DeepAML 标签` 复选框。
- 删除 `过滤交易所大地址` 复选框。
- 保留 `扫描原生交易`、`补充交易详情`、`断点续跑` 等选项。

#### 验证结果
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有 Vite chunk size warning。
- 新前端产物为 `assets/index-YUv87gya.js`。

#### 未完成事项
- 无。

#### 注意事项
- 本次仅修改前端，不需要重启后端；浏览器刷新即可。

### 2026-07-24 18:41 启动项目

#### 本次任务
- 启动当前 ETL 项目，供用户访问测试。

#### 新增功能
- 无。

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
- `Get-Process etl-server -ErrorAction SilentlyContinue | Stop-Process -Force; .\run.ps1` 已启动后端，PID 45184。
- `http://127.0.0.1:8000/api/health` 返回 `status=ok`，`analysis_plane.available=true`。
- `http://127.0.0.1:8000` 已引用当前前端构建产物 `assets/index-VAmw0-gn.js`。

#### 未完成事项
- 无。

#### 注意事项
- 当前访问地址为 `http://127.0.0.1:8000`。

### 2026-07-24 17:52 虚拟币数据下载项目移植

#### 本次任务
- 将 `E:\codex\虚拟币` 项目的代码移植到当前 ETL 项目。
- 前端入口放在 `虚拟币 -> 数据下载`，保持源项目下载功能主流程可用，前端重新排版为当前项目 Ant Design 工作台。

#### 新增功能
- 新增虚拟币数据下载后端模块，支持 RPC、OKLink CSV、浏览器三种来源。
- 支持多地址/多链批量下载、地址级进度、任务进度、取消任务、取消单地址、暂停/冷却后继续下载、任务列表和结果文件展示。
- 支持 CSV 邮箱/IMAP、并发、RPS、超时、重试、分页、断点续跑、DeepAML 标签、交易所过滤等源项目参数。
- 新增当前项目专属设置存储目录，设置读取不回显 IMAP 密码/授权码。

#### 修改文件
- `internal/cryptodownload/`（新增，移植源项目下载引擎及嵌入脚本）
- `internal/api/handlers.go`
- `internal/api/crypto_address_handlers.go`
- `frontend/src/App.tsx`
- `frontend/src/features/crypto/cryptoDownloadApi.ts`
- `frontend/src/features/crypto/CryptoDownloadPanel.tsx`
- `frontend/src/features/crypto/crypto-download.css`
- `go.mod`
- `go.sum`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 新增 `/api/crypto/download/settings` 设置读写接口。
- 新增 `/api/crypto/download/start` 启动下载任务。
- 新增 `/api/crypto/download/job`、`/api/crypto/download/jobs` 查询任务。
- 新增 `/api/crypto/download/cancel` 支持任务级/地址级取消。
- 新增 `/api/crypto/download/resume` 继续暂停或冷却任务。
- 新增 `/api/crypto/download/history*` 历史任务查询/导入/继续接口。

#### 数据库变化
- 无外部数据库结构变更。
- 新增本地运行目录 `backend/data/crypto_download/`，用于配置、作业记录、历史记录、默认 raw/exports 输出。

#### 前端变化
- `虚拟币` 菜单新增 `数据下载` 页面并默认展开。
- 新页面提供下载参数表单、当前任务总览、地址进度表、结果文件、错误和最近任务列表。
- 默认输出目录为 `backend/data/crypto_download/exports`，默认 raw 目录为 `backend/data/crypto_download/raw`。

#### 验证结果
- `go test ./internal/cryptodownload ./internal/api -count=1` 通过。
- `go test ./internal/... -count=1` 通过。
- `go vet ./internal/...` 通过。
- `go build -o bin\etl-server.exe .\cmd\server\` 通过。
- `cd frontend && npm run build` 通过，仍有既有 Vite chunk size warning。
- 已执行 `Get-Process etl-server -ErrorAction SilentlyContinue | Stop-Process -Force; .\run.ps1` 重启后端，PID 7108。
- `http://127.0.0.1:8000/api/health` 返回 `status=ok`。
- `http://127.0.0.1:8000/api/crypto/download/settings` 返回当前项目默认设置且不回显授权码。
- `http://127.0.0.1:8000/api/crypto/download/jobs` 返回空任务列表。
- `http://127.0.0.1:8000` 已引用最新前端产物 `assets/index-VAmw0-gn.js`。

#### 未完成事项
- 未用真实 OKLink/RPC 地址做端到端下载，避免在本次移植中触发外部平台、邮箱或限流；需要用户提供可测试地址/邮箱/节点后做真实下载验收。
- 源项目测试文件未整体迁入，后续可围绕当前项目 API 增加更小的集成测试。

#### 注意事项
- `/api/crypto/download/settings` 已改为当前项目专属配置目录，不读取源工具全局配置。
- 读取设置不会返回 IMAP 密码/授权码；保存空密码时保留当前项目已保存值。
- 工作区已有大量 Dune profile 和运行时改动，本次未回退。

### 2026-06-29 0622反馈真实目录识别/合并验证

#### 本次任务
- 使用 `E:\项目\网赌\梅县-网赌30娱乐\资金流调证\0622反馈` 做合并测试，验证当前逻辑能否区分支付宝、微信、银行流水。

#### 新增功能
- 无。

#### 修改文件
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`
- `backend/data/outputs/scan_0622_feedback.json`
- `backend/data/outputs/pipeline_0622_feedback.json`
- `backend/data/outputs/funds_etl_dfea3ad0-f56.xlsx`

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- 目录内有 115 个 CSV、18 个 PDF、18 个 ZIP、3 个 RAR；支持处理的表格文件为 115 个 CSV，总大小约 4.96GB。
- 当前扫描器结果：`transactions=0`、`accounts=0`、`unknown=115`，provider 全部为 `未知`。
- 当前合并管道结果：`rows_in=0`、`rows_out=0`，输出空标准表头文件 `backend/data/outputs/funds_etl_dfea3ad0-f56.xlsx`。
- GB18030 抽样读取 `账户明细` CSV 表头正常；UTF-8 读取乱码，确认当前失败主因是 CSV 编码未被正确识别/转换。

#### 未完成事项
- 需要后续修复 CSV 编码自动检测或 GBK/GB18030 fallback。
- 需要增强支付宝 provider 粗分：当文件名/表头命中 `账户明细`、`余额明细`、`注册信息`、`登陆日志` 等支付宝标准表类型时，即使没有 `支付宝` 字样，也应进入支付宝解析链路。

#### 注意事项
- 该批数据按文件名看主要是支付宝协查反馈，未发现微信/财付通/银行命名特征。
- 本次未修改后端代码，未执行 `run.ps1` 重启。

### 2026-06-26 DuckDB CLI 工具补齐

#### 本次任务
- 从 `E:\codex\etl_exe\tools` 复制 `duckdb.exe` 到当前项目，修复健康检查中 `analysis_plane` 缺少 DuckDB CLI 的问题。

#### 新增功能
- 当前项目新增 `tools/duckdb/duckdb.exe`，后端健康检查可识别 DuckDB CLI。

#### 修改文件
- `tools/duckdb/duckdb.exe`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- `E:\codex\etl\tools\duckdb\duckdb.exe --version` — 返回 `v1.5.3 (Variegata) 14eca11bd9`。
- `Get-Process etl-server -ErrorAction SilentlyContinue | Stop-Process -Force; .\run.ps1` — 已重启后端，PID 38668。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`，`analysis_plane.available=true`，`exe_path=E:\codex\etl\tools\duckdb\duckdb.exe`。

#### 未完成事项
- 无。

#### 注意事项
- 用户给出的目录是 `E:\codex\etl_exe\tools`；实际可执行文件位于其子目录 `duckdb\duckdb.exe`。

### 2026-06-26 通用清洗规则增强

#### 本次任务
- 将清洗规则加入通用清洗路径，让所有 provider 最终进入 `etl.CleanTransactions` 时都能共享关键清洗规则。
- 修正通用金额清洗：负数交易金额可能是冲正/撤销/退款，不能统一转正。

#### 新增功能
- 通用清洗新增账号字段清理：`交易卡号`、`交易账号`、`交易对手账卡号` 统一调用 `parser.CleanAccountNumber`。
- 通用金额清洗只做解析和格式化，保留负数符号，避免破坏冲正/撤销/退款等业务语义。
- 通用清洗新增失败反馈过滤：`查询反馈结果原因` 命中 `查询失败`、`失败`、`无记录`、`无此记录`、`查无此`、`no record` 时删除该行。

#### 修改文件
- `internal/etl/cleaning.go`
- `internal/etl/etl.go`
- `internal/etl/etl_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- `go test ./internal/etl -run TestCleanTransactionsAppliesCommonRules -count=1` — 初次加入通用规则前失败于未过滤失败反馈行；负数保留修正前失败于金额被转正；对应修复后均通过。
- `go test ./internal/etl -count=1` — 通过。
- `go test ./internal/... -count=1` — 通过。
- `go vet ./internal/...` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server` — 通过。
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command 'Get-Process etl-server -ErrorAction SilentlyContinue | Stop-Process -Force; .\run.ps1'` — 已重启后端，PID 47276。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`；`analysis_plane` 仍提示缺少 `tools/duckdb/duckdb.exe`，与本次清洗规则无关。

#### 未完成事项
- `internal/etl/etl.go` 仍是既有超大文件；本次只将清洗/去重单元拆出，未做无关重构。
- 未将银行专用的 0 金额过滤上移到通用层，避免误删其他来源合法 0 金额记录。

#### 注意事项
- 通用清洗规则会影响支付宝、微信、银行、unknown 等所有最终进入 `RunPipeline` 的流水。
- 当前工作区存在大量 Dune profile/rod 相关既有运行时改动，本次未处理。

### 2026-06-25 Rod 用户模式增强: cf_clearance 检测 + 页面复用

#### 本次任务
- 将 `rod-usermode-detailed.md` 中的 rod Chrome CDP 自动化方案（cf_clearance cookie 检测、页面复用、HTML 级 Cloudflare 拦截判定）在最小改动前提下集成进现有的 `internal/dunetools/rod_user_mode_*.go`。

#### 新增功能
- `checkRodCFClearance(browser)` — 浏览器级 cf_clearance cookie 是否存在判定。
- `checkRodCFClearanceExpiry(browser)` — cf_clearance 过期时间 + 5 分钟缓冲检测。
- `findOrCreateDunePage(browser)` — 优先复用已有 dune.com 页面 → 复用 about:blank 并导航 → 新建。
- `isRodBlocked(page)` — HTML 内容检测 Cloudflare 拦截（"Sorry, you have been blocked" / "Cloudflare Ray ID" / "Attention Required" / "cf-browser-verify"）。
- `isRodLoggedIn(page)` — URL 排除 /login + localStorage token 双重判定。
- `waitForCFClearance(browser)` — 基于 cf_clearance cookie 的浏览器级等待。

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| Rod 登录每次新建页面可能触发 Cloudflare 重新检测 | 原 `LoginAndExtract()` 总是 `browser.Page(TargetCreateTarget{URL: duneHomeURL})` 新建页面，不复用已有 cf_clearance 上下文 | 改为 `findOrCreateDunePage()`，优先复用已有 dune.com 页面 |
| Cloudflare 拦截检测仅依赖页面文本（可能漏检） | `waitForManualVerification()` 只用 JS 内联检测文本模式 | 新增 `isRodBlocked()` HTML 级匹配 + `checkRodCFClearance()` cookie 级双保险 |
| 缺少 cf_clearance 专项检测和过期判定 | 原代码无 cf_clearance cookie 相关逻辑 | 新增 `checkRodCFClearance()` / `checkRodCFClearanceExpiry()` |

#### 修改文件
- `internal/dunetools/rod_user_mode.go`
- `internal/dunetools/rod_user_mode_session.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过。
- `go test ./internal/dunetools -run Rod -count=1` — 5/5 通过。
- `go test ./internal/... -count=1` — 全部通过。

#### 未完成事项
- 未对真实 Dune 线上环境发起 rod 端到端重跑；本次由单元测试和编译验证覆盖。
- `checkRodCFClearanceExpiry()` 预留给后续自动刷新逻辑使用，当前未被调用。

#### 注意事项
- 核心改进：cf_clearance cookie 级检测比页面文本检测更可靠；页面复用避免 Cloudflare 重新检测。
- `LoginAndExtract()` 现在不再创建新页面，而是优先复用浏览器中已有的 dune.com 标签页。

### 2026-06-25 Dune query chained parameter return fix

#### Task
- 按完整 Dune 查询链路修正账号查询：选择已注册账号登录，获取 Cookie/Authorization/team 参数，SQL 自动创建 query，执行得到 `execution_id`，再用 `/public/execution` 取表格。
- 修复前面接口生成的 `query_id` 没有回传到最终响应，导致后续分页/导出参数断链的问题。

#### New Functionality
- `/api/dune/query` 的账号 web 查询会在最终响应中返回自动 `CreateQuery` 产生的 `query_id`。
- 回归测试覆盖“只传 `sql + account_email`，后端自动完成登录、建 query、执行、取表格并返回 rows/query_id/execution_id”。

#### Modified Files
- `internal/api/dune_web_query.go`
- `internal/api/dune_query_handlers.go`
- `internal/api/dune_account_query_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### API Changes
- 无接口路径或请求 schema 变更。
- 修正响应行为：账号 web 查询返回自动创建的 `query_id`，不再返回 `0`。

#### Database Changes
- 无。

#### Frontend Changes
- 无。

#### Verified Commands
- `go test ./internal/api -run TestHandleDuneSQLQueryUsesSelectedAccountLoginForWebQuery -count=1`
- `go test ./internal/api -count=1`
- `go test ./internal/... -count=1`
- `go build -o bin/etl-server.exe ./cmd/server`
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command '$env:DUNE_QUERY_CDP_PORT="9222"; & .\run.ps1'`
- `curl.exe -s http://127.0.0.1:8000/api/health`

#### Open Items
- 真实 Dune smoke 仍被 Dune Cloudflare 登录 block 页面拦截；本地参数链路已由 HTTP handler 测试验证。

#### Notes
- 完整参数链：`account_email` -> 登录提取 `cookie/authorization/access_token/team_id` -> `CreateQuery` 得到 `query_id` -> `ExecuteQuery` 得到 `execution_id` -> `/public/execution` 用 `cookie + query_id + execution_id` 拉表格 rows。

### 2026-06-25 Dune account JWT expiry handling

#### Task
- 解决已保存 Dune 账号 JWT 过期后 `/api/dune/query` 继续复用旧 token 的问题。
- 补充服务重启后第一次账号查询不会加载 `accounts.json` 的回归修复。

#### New Functionality
- 账号查询现在会解析 `auth-id-token` / Bearer JWT 的 `exp`，过期或 60 秒内到期时自动跳过本地保存 auth 和短期登录缓存，进入后台登录刷新流程。
- `/api/dune/query` 首次使用 `account_email` 时会先初始化并加载持久化账号，避免必须先访问 `/api/dune/batch/accounts`。

#### Modified Files
- `internal/api/dune_auth_jwt.go`
- `internal/api/dune_account_query.go`
- `internal/api/dune_account_auth_expiry_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### API Changes
- 无本地接口路径或 JSON schema 变更。
- `/api/dune/query` 的账号认证行为变更：过期 JWT 不再被视为可用凭证。

#### Database Changes
- 无。

#### Frontend Changes
- 无。

#### Verified Commands
- `go test ./internal/api -run TestHandleDuneSQLQueryReloginsWhenSavedAccountAuthExpired -count=1`
- `go test ./internal/api -run TestFindDuneQueryAccountLoadsPersistedAccountsOnFirstQuery -count=1`
- `go test ./internal/api -count=1`
- `go test ./internal/... -count=1`
- `go build -o bin/etl-server.exe ./cmd/server`
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command '$env:DUNE_QUERY_CDP_PORT="9222"; & .\run.ps1'`
- `curl.exe -s http://127.0.0.1:8000/api/health`

#### Open Items
- 线上 Dune smoke 已进入后台重登阶段，但当前浏览器登录被 Dune Cloudflare block 页面拦截，用户提供的 Ray ID 为 `a11297b48898e389`。
- 需要一个 Dune 接受的有效网页登录会话或 Dune API key，才能完成真实查询闭环。

#### Notes
- `stealth-config.cjs` 不能续期 Dune JWT；它只影响浏览器自动化表征和 Cloudflare 处理。
- 正常 Chrome 能打开 Dune 首页，不代表 GraphQL query creation 所需的 `auth-id-token` 仍有效。

### 2026-06-25 Dune stealth 自动 Cloudflare 处理修复

#### 本次任务
- 用户明确要求：`playwright-go-stealth-config` 的目的就是跳过 Cloudflare，不应再要求用户手动点击验证。

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| shared stealth solver 看到 `cf_clearance` 就立即认为 Cloudflare 已通过 | `solveCloudflareWithStealth()` 没有同时确认页面已经离开 `Just a moment...` / challenge surface，可能被旧 cookie 误导 | solver 改为先检测页面是否仍是 Cloudflare；只有页面不再是 Cloudflare 且有 clearance，或页面已正常，才返回成功 |
| query bridge 仍会退到人工点击 | `playwright_bridge.js` 的 Cloudflare 分支在 stealth 失败后调用 `waitForManualCloudflare()` | 查询 bridge 改为只使用 stealth 自动处理；失败返回 `cloudflare_stealth_timeout`，不再提示人工点击 |
| shared solver 自动动作不完整 | 旧 shared solver 只尝试 iframe checkbox/label 点击 | 下沉更多自动策略：页面中心点击、可见表单/按钮提交、Cloudflare restart event、周期 reload |

#### 修改文件
- `tools/dune-playwright/stealth-config.cjs`
- `backend/data/dune/playwright_bridge.js`
- `tools/dune-playwright/register-login.test.mjs`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名本地 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- 新增回归测试先失败，确认旧实现存在两个问题：clearance cookie 误判、query bridge 人工点击 fallback。
- `node --test tools/dune-playwright/register-login.test.mjs --test-name-pattern "shared stealth|query bridge relies"` — 修复后通过。
- `node --test tools/dune-playwright/register-login.test.mjs` — 18/18 通过。
- `node --check tools/dune-playwright/stealth-config.cjs && node --check tools/dune-playwright/register-login.mjs && node --check backend/data/dune/playwright_bridge.js` — 通过。
- `git diff --check -- tools/dune-playwright/stealth-config.cjs backend/data/dune/playwright_bridge.js tools/dune-playwright/register-login.test.mjs` — 通过，仅有既有 CRLF 提示。
- `go build -o bin/etl-server.exe ./cmd/server` — 通过。
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command '$env:DUNE_QUERY_CDP_PORT="9222"; & .\run.ps1'` — 已重启后端，PID 20616。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`。
- Bridge refresh smoke — `status=0`，脱敏长度 `cookie_len=5317`、`authorization_len=1192`、`access_token_len=0`，无人工点击提示。
- `POST /api/dune/query`，SQL `select 1 as smoke_value` — 返回 HTTP 401；日志显示直连 GraphQL 先遇到 Cloudflare 403，Playwright fallback 不再提示人工点击，自动请求后返回 `Dune HTTP 401`。

#### 未完成事项
- 当前 Dune 登录态仍然过期，真实 SQL 还没有拿到 `execution_id/query_id/rows`；这需要 fresh Dune auth JWT，不是 Cloudflare 点击问题。

#### 注意事项
- 现在查询 bridge 不再要求用户手动点 Cloudflare。如果 stealth 自动处理失败，会明确返回 `cloudflare_stealth_timeout`。
- 仍需区分两个层次：Cloudflare clearance 通过后，Dune GraphQL 还会校验账号 JWT；过期 JWT 会继续返回 401。

### 2026-06-25 Dune query Chrome/CDP auth verification

#### 本次任务
- 继续跑通 Dune 查询流程；自动化显示 Cloudflare block，但用户反馈普通 Chrome 可以正常访问官网。

#### 修复的问题
| 问题 | 根因 | 修复/结论 |
|------|------|-----------|
| 后端直连 Dune GraphQL 返回 Cloudflare 403 | 普通 HTTP 请求未复用浏览器通过 Cloudflare 后的会话 | 查询 bridge 增加 CDP 复用能力，可接入用户可见 Chrome 会话 |
| Playwright fallback 之前仍可能启动独立 Chromium/profile | 查询链路没有优先使用本机 Chrome 或用户指定 profile/CDP | 增加 `DUNE_QUERY_PROFILE_DIR`、`DUNE_QUERY_CHANNEL`、`DUNE_QUERY_CHROME_PATH`、`DUNE_QUERY_CDP_URL`/`DUNE_QUERY_CDP_PORT` 等入口 |
| 官网首页可打开但查询仍 401 | CDP Chrome 中的 `auth-id-token` JWT 已过期，Dune `/api/auth/session` reload 后返回 401 | 当前不是有效登录态；需要在同一个 9222 Chrome 窗口重新登录 Dune 后继续验证 |
| 已集成 stealth 但看起来“不起作用” | stealth 只解决浏览器指纹/Cloudflare clearance；不能刷新已经过期的 Dune 登录 JWT | 运行时证据显示有 `cf_clearance`，但 GraphQL 返回 `jwt expired` |

#### 修改文件
- `backend/data/dune/playwright_bridge.js`
- `tools/dune-playwright/register-login.test.mjs`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名本地 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- `node --check backend/data/dune/playwright_bridge.js` — 通过。
- `node --test tools/dune-playwright/register-login.test.mjs --test-name-pattern "CDP|query bridge"` — 通过。
- `go build -o bin\\etl-server.exe ./cmd/server` — 通过。
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\\run.ps1` — 已用 `DUNE_QUERY_CDP_PORT=9222` 重启后端。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`。
- `curl.exe -s http://127.0.0.1:9222/json/list` — 可看到 Dune 页面 `https://dune.com/home`。
- CDP refresh smoke — 可从 Chrome 会话读取 Dune cookie 和 `Authorization`，未输出明文。
- `POST /api/dune/query` 使用 `select 1 as smoke_value` — 仍返回 HTTP 401；进一步探测确认 GraphQL 响应为 `jwt expired`。
- 直接在 CDP 页面上下文请求 `POST https://dune.com/public/graphql?operationName=GetTeams` — 返回 HTTP 401 JSON `jwt expired`，说明已过 Cloudflare 但 Dune 登录 JWT 失效。

#### 未完成事项
- 需要用户在同一个远程调试 Chrome 窗口中重新登录 Dune；登录完成后重跑 refresh 和最小 SQL 查询。

#### 注意事项
- 本轮探测产生的临时 cookie/token 文件已删除。
- “能打开 Dune 首页”不等于“查询 API 已登录”；当前 create-query GraphQL 需要未过期的 Dune JWT。
- `playwright-go-stealth-config` 已按项目真实执行链路翻译为 Node Playwright 的 `tools/dune-playwright/stealth-config.cjs`；后续如果仍失败，应先区分 Cloudflare clearance 失败还是 Dune 账号 token 失败。

### 2026-06-22 Dune Cloudflare stealth 配置集成

#### 本次任务
- 将 `D:/app/桌面/playwright-go-stealth-config.md` 中的 Dune Cloudflare/Turnstile stealth 配置集成进项目；出现验证时先使用该配置，测试通过后交付。

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| 现有 Cloudflare 流程只有常规点击和人工等待，缺少统一 stealth 指纹配置 | 文档里的启动参数、UA/header、webdriver/plugins/WebGL/Canvas/chrome runtime 修补没有接入实际 Dune 浏览器链路 | 新增 `tools/dune-playwright/stealth-config.cjs`，登录脚本和查询桥接共用同一套配置 |
| 账号登录/查询桥接遇到 Turnstile 时没有先尝试基于配置自动处理 | `register-login.mjs` 与 `playwright_bridge.js` 各自处理 Cloudflare，逻辑分散 | 两处都在导航 Dune 前 `applyStealthConfig(page)`，Cloudflare 分支先调用 `solveCloudflareWithStealth()` |
| 配置接入缺少回归约束 | 原测试只覆盖可见浏览器和欢迎页流程 | 新增测试锁定共享 stealth 配置、导航前注入、查询桥接复用和 `cf_clearance` 检测 |

#### 修改文件
- `tools/dune-playwright/stealth-config.cjs`
- `tools/dune-playwright/register-login.mjs`
- `tools/dune-playwright/register-login.test.mjs`
- `backend/data/dune/playwright_bridge.js`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- `node --test tools/dune-playwright/register-login.test.mjs` — 先补测试后失败于缺少 `stealth-config.cjs`；实现后 13/13 通过。
- `node --check tools/dune-playwright/stealth-config.cjs` — 通过。
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- `node --check backend/data/dune/playwright_bridge.js` — 通过。
- Dune bridge refresh smoke：脱敏输出 `status=0`、`hasCookie=true`、`hasAuthorization=true`，stderr 记录 `STEALTH_CF_CLEARANCE_FOUND`。
- `go test ./internal/...` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server` — 通过。
- `npm run build` (frontend) — 通过，仅保留既有 Vite chunk size warning。
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 已重启后端，PID 34968。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`。

#### 未完成事项
- 未引入 Go `playwright-go` 依赖；项目当前实际执行 Dune 浏览器的是 Node Playwright 脚本，本次按文档行为集成到现有链路。
- 如 Dune 后续仍弹出 Cloudflare，浏览器会保持可见，用户点击后脚本继续自动提取 cookie/参数并执行查询。

#### 注意事项
- 真实 smoke 只记录脱敏状态，没有在文档中保存 cookie/token 明文。
- 健康检查里的 `analysis_plane` 缺少 `tools/duckdb/duckdb.exe` 为既有问题，与 Dune stealth 集成无关。

### 2026-06-22 Dune Cloudflare 人工点击接管

#### 本次任务
- 用户要求如果 Dune 自动登录/查询过程中出现 Cloudflare，就让用户点击验证。

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| Dune SQL 选账号刷新认证遇到 Cloudflare 时用户无法点击 | 查询账号自动登录使用 `Headless: true`，浏览器不可见 | 改为 `Headless: false`，Cloudflare 出现时用户可直接在弹出的浏览器窗口点击 |
| 首页 Cloudflare 会直接返回 `cloudflare_blocked_homepage` | `register-login.mjs` 在 `navigateWithCFRetry()` 失败后直接返回错误，未进入人工等待 | 新增 `waitForManualCloudflare()`，首页 Cloudflare 失败后等待用户点击，验证通过自动继续 |
| 查询 fallback 浏览器窗口被藏到屏幕外 | `playwright_bridge.js` 使用 `--window-position=-32000,-32000` | 改为可见窗口位置，并在 Dune 首页检测到 Cloudflare 时等待用户点击 |

#### 修改文件
- `internal/api/dune_account_query.go`
- `tools/dune-playwright/register-login.mjs`
- `tools/dune-playwright/register-login.test.mjs`
- `backend/data/dune/playwright_bridge.js`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- `node --test tools/dune-playwright/register-login.test.mjs --test-name-pattern "Cloudflare|visible"` — 修复前新增用例失败，修复后通过。
- `node --test tools/dune-playwright/register-login.test.mjs` — 11/11 通过。
- `node --check tools/dune-playwright/register-login.mjs && node --check backend/data/dune/playwright_bridge.js` — 通过。
- `go test ./internal/api -run TestHandleDuneSQLQueryUsesSelectedAccountLoginForWebQuery -count=1` — 通过。
- `go test ./internal/dunetools -count=1` — 通过。
- `go test ./internal/...` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server/` — 通过。
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 已重启后端，PID 20920。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`。

#### 未完成事项
- 未在无人值守状态下触发真实 Cloudflare 等待，因为新行为会停在可见浏览器等待用户点击；人工测试时看到 Cloudflare 直接在浏览器里点击即可。

#### 注意事项
- `tools/dune-playwright/register-login.mjs` 已超过 250 pure LOC，本次按最小修复原则未重构，只改 Cloudflare 人工接管路径。
- `backend/data/dune/playwright_bridge.js` 虽在运行时数据目录，但当前是 Dune 查询 fallback 的实际执行脚本。

### 2026-06-22 Dune SQL 查询真实测试与选账号认证复用

#### 本次任务
- 对 Dune SQL 查询账号选择链路进行真实测试，记录请求参数、失败点和外部响应，并修复选账号后无条件后台登录导致长时间卡住的问题。

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| 选择已保存 Cookie/Auth 的 done 账号后仍先后台登录，真实请求卡 4-5 分钟 | `applyDuneAccountAuth` 查到账号后无条件调用 `duneQueryAccountLogin`，没有先使用账号历史中已经保存的 Cookie/Authorization/access_token/team_id | 新增账号保存 auth 优先分支；完整参数存在时直接注入查询 payload，并同步保存 auth 文件；仅缺少完整参数时才走 Playwright 登录刷新 |
| 修复后真实查询仍没有表格结果 | 当前所有 done 账号的 Authorization JWT 已过期，Dune CreateQuery 返回 Cloudflare 403，浏览器 fallback 返回 Dune HTTP 401 | 记录为外部认证状态阻塞；本地流程已从 286.8s 登录卡住变为 17.6s 内明确返回 `auth_required=true` |
| `run.ps1` 重启后仍跑旧代码 | 脚本只在 `bin/etl-server.exe` 不存在时构建，源码修改后不会自动重建已有二进制 | 本次先 `go build -o bin/etl-server.exe ./cmd/server/` 再执行 `run.ps1`；已在注意事项记录 |

#### 修改文件
- `internal/api/dune_account_query.go`
- `internal/api/dune_account_query_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。
- `POST /api/dune/query` 的 `account_email` 行为优化：优先使用该账号保存的网页认证参数，缺失时才后台登录刷新。

#### 数据库变化
- 无数据库结构变更。
- 本机 `backend/data/dune/auth.json` 会被更新为所选账号的网页认证参数，并保留原 API Key。

#### 前端变化
- 无前端代码变更。

#### 真实测试记录
| 项目 | 值 |
|------|----|
| SQL | `select 1 as smoke_value` |
| account_email | `ldj1009538134+dune_2d685f01@gmail.com` |
| 账号状态 | `done` |
| team_id | `11` |
| cookie_len | `4680` |
| authorization_len | `1192` |
| access_token_len | `0` |
| 修复前结果 | 约 `286.8s` 后 502，后台登录 `cloudflare_blocked_homepage` |
| 修复后结果 | 约 `17.6s` 后 401，`auth_required=true`，无 `execution_id/query_id/rows` |
| Dune 直接响应 | CreateQuery HTTP 403，Cloudflare `Just a moment...` |
| Dune 浏览器 fallback | HTTP 401 |
| JWT 过期证据 | 首个账号 `exp_local=2026-06-21T16:13:58`；全部 done 账号 Authorization 均已过期，最新到 `2026-06-22T00:19:26` |

#### 验证结果
- `go test ./internal/api -run TestHandleDuneSQLQueryUsesSavedAccountAuthBeforeLogin -count=1` — 修复前失败，修复后通过。
- `go test ./internal/api -count=1` — 通过。
- `go test ./internal/...` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server/` — 通过。
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 已重启后端，PID 41828。
- `npm run build` (frontend) — 通过，仅有既有 Vite chunk size warning。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`；`analysis_plane` 仍提示缺少 `tools/duckdb/duckdb.exe`，与 Dune 查询无关。

#### 未完成事项
- 当前账号认证全部过期，无法完成真实 Dune 表格返回；需要刷新账号登录态后再复测，目标是返回 `execution_id/query_id/rows`。

#### 注意事项
- 本地强制重登卡住问题已修复；剩余 401/403 是 Dune 外部认证/Cloudflare 状态，不是前端账号选择字段丢失。
- 后端源码改动后不要只运行 `run.ps1`，需先构建新 `bin/etl-server.exe`，否则会继续启动旧二进制。

### 2026-06-22 Dune 查询 5 项 bug 修复

#### 本次任务
- 全链路代码审查 Dune SQL 查询功能，发现并修复 5 项 bug：登录缓存缺失、全局可变函数注入、超时遗漏、错误消息吞噬、前端导出/翻页缺 cookie。

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| 每次选账号查询都重开 Playwright 登录（无缓存） | `loginDuneQueryAccount` 直接调用无缓存层 | 新增 `loginDuneQueryAccountWithCache` 内存缓存，TTL 5min |
| `duneQueryAccountLogin` 全局可变函数，并发测试竞态 | 测试覆盖 `var` 函数指针无锁保护 | 改为 `duneAccountLoginFunc` 类型 + `SetDuneAccountLoginForTest` 注入 |
| 查询 HTTP handler 无 context deadline，Playwright 可能无限挂起 | `applyDuneAccountAuth` 未检查 ctx deadline | 在 ctx 无 deadline 时兜底 `context.WithTimeout(ctx, 10*time.Minute)` |
| `fetchDunePreviewPage` 降级到 API 路径时吞掉原始错误 | Cookie 不可用 + apiKey 为空时直接调用 `fetchDuneResultPage`，返回无意义错误 | 在调用前检查 `apiKey` 是否为空，返回 `Cookie 不可用且未配置 API Key` |
| 前端翻页/导出不传 cookie，仅依赖后端 auth 文件降级 | `DunePageValues` / `DuneExportValues` 类型缺失 `cookie` 字段 | 类型新增 `cookie` 字段；`loadDunePage` / `exportDuneExcel` / `DuneQueryPanel` 传参 |

#### 修改的文件
- `internal/api/dune_account_query.go` — 缓存 + 函数注入 + 超时兜底
- `internal/api/dune_account_query_test.go` — 测试改用 SetDuneAccountLoginForTest
- `internal/api/dune_query_handlers.go` — fetchDunePreviewPage 错误增强
- `frontend/src/features/download/duneApi.ts` — 类型 + 请求体新增 cookie
- `frontend/src/features/download/DuneQueryPanel.tsx` — 调用时传入 auth?.cookie

#### 已验证
- `go test ./internal/... -count=1` — 全量通过
- `go build -o bin\etl-server.exe .\cmd\server` — 通过
- `npx tsc --noEmit` (frontend) — 通过
- `.\run.ps1` — 已自动重启，PID 40548

### 2026-06-22 Dune SQL 查询账号自动登录提参

#### 本次任务
- Dune SQL 查询页新增账号选择，用户选择已注册验证登录且状态正常的账号后，后端后台静默登录该账号，自动获取 Cookie/Authorization/access_token/team_id，并继续调用官网查询链路返回表格结果。

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| Dune 查询需要用户手动填 Cookie/Token 等参数 | 查询页只支持手填 API Key/Cookie 或使用已保存鉴权，未与批量注册账号状态、后台登录提参和官网查询 API 串联 | `POST /api/dune/query` 新增 `account_email`；后端校验账号状态后用 headless Playwright 登录提参，再执行 CreateQuery/ExecuteQuery/public execution 拉取结果 |
| 长邮箱账号可能撑破移动端设置区 | 账号值作为 Select 内容进入 grid 子项，默认 min-content 宽度会撑开布局 | Dune 设置网格子项加 `min-width: 0`，页头状态区允许换行，长账号在输入框内省略 |

#### 修改文件
- `internal/api/dune_account_query.go`
- `internal/api/dune_account_query_test.go`
- `internal/api/dune_query_handlers.go`
- `internal/dunetools/playwright.go`
- `tools/dune-playwright/register-login.mjs`
- `tools/dune-playwright/register-login.test.mjs`
- `frontend/src/features/download/DuneQueryPanel.tsx`
- `frontend/src/features/download/duneApi.ts`
- `frontend/src/features/download/duneBatchApi.ts`
- `frontend/src/styles/layout.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- `POST /api/dune/query` 请求体新增可选字段 `account_email`。
- 复用 `GET /api/dune/batch/accounts` 读取可选账号，无新增路由。

#### 数据库变化
- 无数据库结构变更。
- 会更新本机文件存储中的 Dune 账号鉴权字段和 auth 文件网页鉴权参数；保存时保留原有 API Key。

#### 前端变化
- `下载 -> dune -> 数据查询` 新增“查询账号（自动登录）”下拉框。
- 账号选中后自动切换到“官网（Cookie/账号）”查询模式。
- 页头新增可用账号数量，并修复移动端状态标签与长邮箱显示。

#### 验证结果
- `go test ./internal/api -run TestHandleDuneSQLQueryUsesSelectedAccountLoginForWebQuery -count=1` — 通过。
- `node --test tools/dune-playwright/register-login.test.mjs` — 9/9 通过。
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- `cd frontend && ./node_modules/.bin/tsc --noEmit` — 通过。
- `go test ./internal/...` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server` — 通过。
- `cd frontend && ./node_modules/.bin/vite build` — 通过，仅有既有 chunk size warning。
- `.\run.ps1` — 已重启项目，后端 PID 42044。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`。
- Playwright 页面 QA — 桌面和 390px 移动端确认账号选择、账号下拉、选账号自动切模式、长邮箱省略和按钮区域均可用。

#### 未完成事项
- 未发起真实 Dune 线上 SQL 执行，避免触发实际账号外部登录风控或 Cloudflare；查询链路用本地 httptest 端到端覆盖。

#### 注意事项
- 如果真实账号后台登录遇到 Cloudflare/风控，后端会返回账号登录失败；需要先确保批量注册账号仍处于可登录状态。
- 自动登录刷新网页鉴权参数不会清空已保存 API Key。

### 2026-06-22 Dune welcome/onboarding 流程顺序优化

#### 本次任务
- 继续优化 Dune 登录成功后的 welcome/onboarding 自动处理流程，减少输入、选项、跳过、下一步之间反复卡住的问题。

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| welcome 流程仍可能卡住 | 自动处理顺序不够稳定：先尝试 Skip，再填输入，再点选项/Continue；同时 Continue/Next 可能处于 disabled 状态却仍被尝试点击 | 改为先填空输入框，再只点击启用态 Continue/Next；无输入可填时才尝试 Skip；最后才点安全选项并再次 Continue |

#### 修改文件
- `tools/dune-playwright/register-login.mjs`
- `tools/dune-playwright/register-login.test.mjs`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- 新增红灯测试修复前失败：缺少 `clickWelcomeAction()`。
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- `node --test tools/dune-playwright/register-login.test.mjs` — 8/8 通过。
- Playwright 本地 Chromium DOM QA — 输入框场景先填 `longu` 后点 `Next`；disabled Next 场景点 `Skip`；选项场景点 `Analytics` 后点 `Next`。

#### 未完成事项
- 未进行真实 Dune 线上 welcome 端到端重跑；本次验证覆盖脚本状态机和本地 Chromium DOM 行为。

#### 注意事项
- 本次未修改 Go 后端代码，未执行 `run.ps1` 重启。

### 2026-06-22 Dune welcome/onboarding 自动填入短文本

#### 本次任务
- 修复 Dune 登录成功后的 welcome 页面输入框自动填入字符串过长，导致“下一步”经常不可点击、流程卡住的问题。

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| welcome 输入框填入值过长，下一步不可点击 | `fillWelcomeInputs()` 会写入 `Personal`、`Independent`、`Analyst` 或完整 username，可能超过当前 Dune welcome 表单长度限制 | username/handle 统一截断到 5 字符；team/company/role/name fallback 改为 `Solo`、`Indie`、`Data`、`User1` |

#### 修改文件
- `tools/dune-playwright/register-login.mjs`
- `tools/dune-playwright/register-login.test.mjs`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- 新增红灯测试修复前失败：`expected named short welcome input fallbacks`。
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- `node --test tools/dune-playwright/register-login.test.mjs` — 7/7 通过。
- Playwright 本地 DOM QA — 抽取真实 `fillWelcomeInputs()` 在 Chromium 页面执行，结果为 `["veryl","Solo","Indie","Data","User1"]`，最大长度 `5`。

#### 未完成事项
- 未进行真实 Dune 线上 welcome 端到端重跑；本次验证覆盖脚本逻辑和本地 Chromium DOM 填写行为。

#### 注意事项
- 本次未修改 Go 后端代码，未执行 `run.ps1` 重启。

### 2026-06-22 Dune welcome/onboarding 防误点返回按钮

#### 本次任务
- 修复 `https://dune.com/welcome` 自动选择待选项后，进入下一项又误点 `Back` 返回上一项的问题。

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| welcome 自动流程进入下一项后又点 Back | 通用候选项选择器没有排除返回/上一步类按钮，页面进入下一步后把 `Back` 当成普通选项点击 | 在通用点击前统一过滤导航和动作按钮，包括 `back`、`previous`、`go back`、`返回`、`上一步`、`后退` 等 |

#### 修改文件
- `tools/dune-playwright/register-login.mjs`
- `tools/dune-playwright/register-login.test.mjs`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- `node --test tools/dune-playwright/register-login.test.mjs` — 6/6 通过。

#### 未完成事项
- 未进行真实 Dune 浏览器端到端重跑；后续实际注册时如果 Dune 更换 welcome 文案，可继续补充过滤词或明确选项词。

#### 注意事项
- 本次未修改 Go 后端代码，未执行 `run.ps1` 重启。

### 2026-06-21 Dune 批量注册历史账号显示修复

#### 本次任务
- 修复点击“开始注册”后，之前已经注册的邮箱从前端表格消失的问题。

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| 点击开始注册后历史邮箱不见 | 前端用 `POST /api/dune/batch/start` 返回的新 task 覆盖表格，而 start/status 只返回当前 task 账号，不合并 `accounts.json` 已保存账号 | `start/status/stop` 响应统一合并持久化账号和当前任务账号 |

#### 修改文件
- `internal/api/handler_dune_batch.go`
- `internal/api/handler_dune_batch_accounts.go`
- `internal/api/handler_dune_batch_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- `POST /api/dune/batch/start`、`POST /api/dune/batch/stop`、`GET /api/dune/batch/status` 的 `accounts` 字段现在返回持久化账号与当前任务账号的合并列表。
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端代码变更；现有表格继续读取 `task.accounts`。

#### 验证结果
- 新增红灯测试修复前失败：`accounts=[]`。
- `go test ./internal/api -run TestHandleDuneBatchStart_keepsSavedAccountsVisible_whenStartingNewTask -count=1` — 修复后通过。
- `go test ./internal/api -run 'DuneBatch|Dune|PublicExecution' -count=1` — 通过。
- `go test ./internal/dunetools -count=1` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server/` — 通过。
- `powershell -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 服务重启成功。
- `/api/dune/batch/status` — `accounts=10`。
- `/api/dune/batch/accounts` — `accounts=10`。
- `go test ./internal/... -count=1` — 全部通过。

#### 未完成事项
- 本次按后端规则重启服务，当前内存批量任务会被清空；已保存账号不受影响，未完成批量任务需要重新开始。

#### 注意事项
- `mergeAccounts()` 仍保持已保存账号优先的顺序。

### 2026-06-21 Dune welcome/onboarding 自动处理

#### 本次任务
- 注册/验证登录成功后，自动处理 `https://dune.com/welcome` 的选择、填写和可跳过步骤。

#### 新增功能
- 新增 `completeWelcomeOnboarding()`：
  - 优先点 `Skip for now`、`Skip`、`Maybe later`、`Not now`、`No thanks`。
  - 无法跳过时，自动选择个人使用、分析、研究、DeFi、Ethereum/Solana 等安全默认项。
  - 自动填写 username、team/workspace、company、role/name 等可见输入框。
  - 仍停留在 `/welcome` 时自动跳转 `https://dune.com/home`。
- login、verify_login、capture 三条路径在提取凭据前都会先尝试处理 welcome/onboarding。

#### 修改文件
- `tools/dune-playwright/register-login.mjs`
- `tools/dune-playwright/register-login.test.mjs`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- `node --test tools/dune-playwright/register-login.test.mjs` — 5/5 通过。
- 独立 Playwright 登录 QA — `ok=true,hasCookie=true,hasAuthorization=true,teamId=11`。
- QA 日志出现 `WELCOME_CHOICES`、`WELCOME_FILLED`、`WELCOME_GOTO_HOME`，确认 welcome 页真实执行了自动选择、自动填写和跳转。
- 已删除临时 QA profile：`backend/data/dune/profiles/welcome_qa`。

#### 未完成事项
- Dune welcome 页面如果更换文案/结构，可能需要继续补充默认选项关键词。

#### 注意事项
- 本次未修改 Go 后端代码，未执行 `run.ps1` 重启。
- 没有停止当前正在运行的 `total=30` 批量注册任务。

### 2026-06-21 Dune verify_login 修复 + 端到端实测

#### 本次任务
- 使用用户提供的 Gmail IMAP 配置继续实测 Dune 注册流程，覆盖注册、邮箱验证、登录凭据提取。

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| `verify_login` 启动后立刻 `done`，但账号列表为空、完成数为 0 | `Start()` 先从旧 task 统计到 `wait_verify` 账号，随后创建新 task 时把 `Accounts` 初始化为空，`runVerifyLogin()` 只能读到空列表 | `ModeVerifyLogin` 启动时把待验证账号复制进新 task |

#### 修改文件
- `internal/dunetools/manager.go`
- `internal/dunetools/manager_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`
- 运行态数据 `backend/data/dune/accounts.json` 新增真实测试账号记录。

#### 接口变化
- 无新增、删除或重命名 API。
- 修复既有 `POST /api/dune/batch/start` 的 `mode=verify_login` 行为：任务启动后保留待验证账号，不再空跑。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- 红灯测试：`go test ./internal/dunetools -run TestManager_VerifyLogin_completesWaitingAccount_whenVerificationLinkExists -count=1` 修复前失败，显示 `Completed=0, Accounts=[]`。
- 修复后同一测试通过。
- `go test ./internal/dunetools -count=1` — 通过。
- `go test ./internal/api -run 'DuneBatch|Dune|PublicExecution' -count=1` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server/` — 通过。
- `powershell -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 服务重启成功。
- 真实注册：`mode=register,total=1` 最终 `completed=1,failed=0,status=wait_verify,hasVerifyLink=true`。
- 真实验证登录：`mode=verify_login` 最终 `completed=1,failed=0,account.status=done,hasCookie=true,hasAuthorization=true,error=""`。
- `go test ./internal/... -count=1` — 全部通过。
- `/api/health` — `status=ok`，`control_plane.ok=true`。

#### 未完成事项
- `run.ps1` 当前只有在 `bin/etl-server.exe` 不存在时才构建；源码修改后需要显式 `go build`，这和部分文档中“自动构建”的描述不一致。
- `verify_login` 不会在服务重启后自动从 `accounts.json` 恢复 `wait_verify` 账号；需要当前内存 task 保留待验证账号。

#### 注意事项
- 本次未把 Gmail IMAP 应用密码写入文档。
- 最终账号提取到了 Cookie 和 Authorization；`access_token` 字段为空，但当前 combined verify+login 成功路径仍判定完成。

### 2026-06-21 Dune 注册流程实测

#### 本次任务
- 实测 Dune 注册流程；用户辅助处理浏览器人机验证。

#### 处理结果
- 使用 `mode=register,total=1` 启动真实注册任务。
- 使用 Gmail IMAP 主机 `imap.gmail.com:993` 和用户提供的邮箱账号配置收信；未在文档中记录 IMAP 应用密码。
- 任务完成：`status=done,completed=1,failed=0`。
- 新账号进入 `wait_verify` 状态，并已抓取邮箱验证链接。

#### 修改文件
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`
- 运行态数据 `backend/data/dune/accounts.json` 新增 1 条注册账号记录。

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- `curl.exe -s http://127.0.0.1:8000/api/health` — `status=ok`。
- `POST /api/dune/batch/start` — `status=running,total=1`。
- `GET /api/dune/batch/status` 轮询 — 最终 `status=done,completed=1,failed=0`。
- 账号状态脱敏检查 — `username=u08091393,status=wait_verify,hasVerifyLink=true,error=""`。

#### 未完成事项
- 未继续执行邮箱验证链接后的登录/凭据提取阶段。

#### 注意事项
- 本次验证说明注册页跳转修复有效：未再直接落到 `/welcome` 阻断注册。
- 本次未修改 Go 后端代码，未执行 `run.ps1` 重启。

### 2026-06-21 Dune profile/cache 清理

#### 本次任务
- 解释 Dune profile/cache 的用途，并删除无用缓存。

#### 处理结果
- `profile` 是 Playwright/Chrome 持久浏览器用户目录，不全是缓存；其中 Cookies、Local Storage、IndexedDB 可能保存 Dune 登录态、Cloudflare clearance 和页面状态。
- 已删除浏览器可自动重建的缓存目录：`Cache`、`Code Cache`、`GPUCache`、`GrShaderCache`、`ShaderCache`、`DawnWebGPUCache`、`DawnGraphiteCache`、`GPUPersistentCache`、`component_crx_cache`、`extensions_crx_cache`、`AutofillAiModelCache`、`optimization_guide_hint_cache_store`、`Shared Dictionary/cache`。
- 已删除诊断输出：`backend/data/dune/api_captures`、`backend/data/dune/diag`、`backend/data/dune/screenshots`。
- 已删除错误输出目录：`tools/backend`。
- 已保留关键状态：`backend/data/dune/profiles/master`、Cookies/Local Storage/IndexedDB、`auth.json`、`accounts.json`。

#### 修改文件
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`
- 删除运行态缓存/诊断文件若干。

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- 清理前 `backend/data/dune/profiles` 约 846MB。
- PowerShell 安全删除脚本删除 359 个缓存/诊断目录，约 892.14MB，无锁定残留。
- 清理后 `backend/data/dune/profiles` 约 78MB。
- `profiles/master` 和 `auth.json` 均保留。
- 缓存目录抽样查找无输出。
- `curl.exe -s http://127.0.0.1:8000/api/health` 返回 `status=ok`。

#### 未完成事项
- 无。

#### 注意事项
- 不要直接删除整个 `profiles/master`，否则可能丢失 Dune/Cloudflare 会话。
- 删除缓存后浏览器会在下次运行时自动重建必要缓存，首次加载可能稍慢。

### 2026-06-21 Dune 注册/登录: 修复 `/welcome` 乱跳转

#### 本次任务
- 修复 Dune 注册/登录自动化打开浏览器后直接跳到 `https://dune.com/welcome`，不是注册页面的问题。

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| 注册一打开就是 `/welcome` | 自动注册/登录模式从 `auth.json` 注入了旧账号 `auth-*` 登录态 Cookie | 自动注册/验证/登录模式清理并过滤 `auth-*` Cookie，只保留非登录态 Cookie |
| 手动抓取模式不可靠 | `capture` 逻辑嵌在 register/login 分支里，`mode: "capture"` 会落到 unknown mode | 恢复独立 `capture` 分支 |
| 手动抓取保存位置错误 | 脚本以 `tools/dune-playwright` 为 cwd，却用 `../backend` 拼路径 | 改为 `../../backend/data/dune/auth.json` |
| welcome/onboarding 被当成异常跳转 | 登录后状态判断未识别 `/welcome` | 将 `/welcome` 作为已登录页面处理，优先提取凭据 |

#### 修改文件
- `tools/dune-playwright/register-login.mjs`
- `tools/dune-playwright/register-login.test.mjs`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- `node --test tools/dune-playwright/register-login.test.mjs` — 修复前 3 项失败；修复后 3/3 通过。
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- `go test ./internal/dunetools -count=1` — 通过。
- `go test ./internal/api -run 'DuneBatch|Dune|PublicExecution' -count=1` — 通过。

#### 未完成事项
- 未完成真实 Dune 端到端注册；第三方 Cloudflare/Turnstile 验证仍可能需要用户手动处理。

#### 注意事项
- 自动注册/登录不能再注入已有账号的 `auth-*` 登录态 Cookie；否则会复现 `/welcome` 跳转。
- 本次未修改 Go 后端代码，未执行 `run.ps1` 重启。

### 2026-06-21 Dune 注册/登录: auth.json 自动检测跳转

#### 本次任务
- 开始注册前先检查 `auth.json`，没有则自动切换到手动抓取模式

#### 新增功能
- `HasValidAuth()` — 检查 auth.json 是否存在且含有效 cookie
- 自动重定向: full/register 模式启动时检测 auth.json，缺失时自动切换到 capture（打开浏览器让用户手动登录）
- `TaskSnapshot.redirected_from` 字段标记重定向来源

#### 修改文件
- `internal/dunetools/playwright.go` — HasValidAuth()
- `internal/dunetools/manager.go` — Start() 自动跳转逻辑
- `internal/dunetools/types.go` — TaskSnapshot.RedirectedFrom
- `frontend/src/features/download/DuneBatchReg.tsx` — 重定向提示
- `frontend/src/features/download/duneBatchApi.ts` — redirected_from 字段

#### 验证结果
- 模拟缺失 auth.json → API 返回 `redirected_from: "register"` ✓
- 全部测试通过

### 2026-06-21 Dune 注册/登录: 人机验证优化 + 独立登录 + 手动抓取

#### 本次任务
- 解决 Dune 批量注册/登录时反复触发人机验证 (CF Turnstile) 的问题
- 新增独立的"登录已有账号"模式（无需 IMAP）
- 新增"手动抓取凭据"模式（浏览器手动登录 → 自动提取保存）

#### 修复的问题
| 问题 | 根因 | 修复 |
|------|------|------|
| 注册/登录反复人机验证 | `register` 模式不注入 cookie，且 `cf_clearance` 从 auth.json 注入可能过期反效果 | 全模式注入认证 cookie（排除 `cf_clearance`，由 profile 自行管理） |
| 登录必须配 IMAP | `ResolveRunConfig` 无条件校验 IMAP | 仅 `full`/`register` 模式要求 IMAP |
| 登录报 `username required` | `register-login.mjs` 对全模式强制要求 username | login/verify/capture 模式 username 可选 |
| 账号列表空 | login 模式 task 快照在 upsert 前创建 | 直接在 task 初始化时包含 account |

#### 新增功能
- **独立登录模式** (`mode: "login"`): 输入邮箱+密码，Playwright 自动登录提取凭据
- **手动抓取模式** (`mode: "capture"`): 打开浏览器，用户手动登录，系统 10 分钟内自动检测并保存 Cookie/JWT/TeamID
- **Cookie 注入优化**: 排除 `cf_clearance` 避免过期反效果；认证 cookie 仍全模式注入

#### 修改文件
- `tools/dune-playwright/register-login.mjs` — cookie 注入重构 + capture 模式
- `internal/dunetools/config.go` — IMAP 校验按需执行
- `internal/dunetools/types.go` — 新增 LoginEmail/LoginPassword/ModeLogin/ModeCapture
- `internal/dunetools/manager.go` — login/capture 模式分发 + runLogin/runCapture
- `internal/dunetools/playwright.go` — 导出 Run() 方法
- `frontend/src/features/download/DuneBatchReg.tsx` — UI: login/capture 模式
- `frontend/src/features/download/duneBatchApi.ts` — API: login_email/login_password

#### 接口变化
- `POST /api/dune/batch/start` 新增 mode: `login`, `capture`
- `StartRequest` 新增 `login_email`, `login_password` 字段
- `login`/`verify_login`/`capture` 模式不再强制 IMAP

#### 验证结果
- `go test ./internal/...` — 全部通过
- `go build` / `npm run build` — 通过
- 服务重启成功

#### 注意事项
- `cf_clearance` 有时效限制，过期后需"手动抓取"刷新
- Turnstile 图像验证无法自动化，headless:false 时用户可手动操作浏览器窗口

### 2026-06-21 Dune 批量注册: 全流程跑通

#### 本次任务
- 把 Dune 批量注册完整流程跑通并修复所有阻断 Bug
- 核心问题：CF 绕过、验证链接 403、登录阶段崩溃、onboarding 死循环

#### 修复的 Bug
| Bug | 根因 | 修复 |
|-----|------|------|
| 验证链接 HTTP 403 | Go `net/http` 无法过 CF | 改用 Playwright 浏览器打开验证链接 |
| Login 阶段重新过 CF | 验证和登录是两个独立 Playwright session | 合并 verify+login 到同一浏览器 session |
| Username setup 死循环 | 提交用户名后页面不跳转，循环重试 | 加 `usernameDone` 标志防重复，最多 8 轮 |
| Cookie 提取崩溃 | `browser.cookies()` 在 context 关闭后调用抛异常 | try-catch 保护 |
| Onboarding 向导自动点不完 | 问题太多（4 个 tab × 每题），按钮逻辑复杂 | 简化：验证完直接跳登录页，不过 onboarding |
| verify 脚本频频崩溃 | clickFirstText 等操作未包裹 try-catch | 全局 try-catch + 每轮 try-catch |

#### 新增功能
- 合并 verify+login 到同一 Playwright session（避免 CF 重新检测）
- `extractCredentials` 函数统一凭据提取逻辑
- Playwright `cf_clearance` 自动获取并持久化

#### 修改文件
- `tools/dune-playwright/register-login.mjs` — 重写 `verifyAndLogin()`，新增 `extractCredentials()`
- `internal/dunetools/manager.go` — verify 用 Playwright 非 HTTP；合并 verify+login 凭据提取
- `internal/dunetools/playwright.go` — 新增 `VerifyEmail`，stderr 截断 300→1000
- `internal/dunetools/types.go` — `BrowserClient` 接口新增 `VerifyEmail`
- `internal/dunetools/manager_test.go` / `internal/api/handler_dune_batch_test.go` — mock 更新

#### 接口变化
- 无新增/删除 API
- `POST /api/dune/batch/start` 行为：verify+login 合并执行

#### 验证结果
- `go test ./internal/...` — 全部通过
- `go vet ./internal/...` — 无警告
- `go build` — 通过
- 真实 API 测试：`POST /api/dune/batch/start` → `completed=1, failed=0`
- 提取凭据：Cookie 15 个（含 cf_clearance）+ JWT + Team ID 11

#### 未完成事项
- onboarding 向导自动跳过逻辑已简化，若 Dune 强制要求可恢复复杂版

#### 注意事项
- `cf_clearance` 已成功获取，后续注册可复用已持久化的 profile
- 使用 gmail.com 域名（Gmail 别名）无需 Cloudflare Email Routing

### 2026-06-18 Dune 批量注册: 外部阻塞复核

#### 本次任务
- 继续推进“阅读文件，完成剩余任务，把整个流程跑通”，复核当前本地实现、运行态和 Dune 外部认证入口是否仍阻塞。

#### 新增功能
- 无新增运行时功能。本次只做复核和文档同步。

#### 修改文件
- `docs/DUNE_BATCH_REG_STATUS.md`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- `curl.exe -s http://127.0.0.1:8000/api/health` 返回 `status=ok`，`control_plane.ok=true`，`analysis_plane.available=false`（缺少 `duckdb.exe`，既有状态）。
- `curl.exe -s http://127.0.0.1:8000/api/dune/batch/status` 返回当前任务 `status=done, completed=0, failed=1`，失败账号错误为 `register failed: cloudflare_blocked`。
- `node -e ... backend/data/dune/auth.json` 确认 auth 文件存在且有 Dune 应用 Cookie，但不含 `cf_clearance`。
- Playwright headless 探测 `https://dune.com/`、`https://dune.com/auth/register`、`https://dune.com/auth/login`，三者均返回 `Just a moment...` 安全验证页。

#### 未完成事项
- 真实 Dune 注册仍未跑通；当前阻塞点是 Dune/Cloudflare 外部认证入口，不是本地流程缺少代码。
- 继续推进需要 Dune 官方允许的账号/团队开通路径、白名单/企业支持，或用户提供已通过 Cloudflare 的新鲜真实浏览器会话/Cookie（包含 `cf_clearance`）。

#### 注意事项
- 已避免继续实现或尝试 Cloudflare 绕过。现有批量注册代码路径可以启动、记录状态并暴露失败原因，但不能在当前外部认证条件下完成真实账号注册。

### 2026-06-18 Dune 批量注册: 状态文档校准

#### 本次任务
- 阅读 `docs/DUNE_BATCH_REG_STATUS.md` 与 `docs/dune-batch-registration-spec.md`，继续收敛 Dune 批量注册剩余任务，并把真实复测结论写回状态文档。

#### 新增功能
- 无新增运行时功能。本次仅更新文档状态。

#### 修改文件
- `docs/DUNE_BATCH_REG_STATUS.md`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。
- `POST /api/dune/batch/start` 契约不变；最近真实复测可启动任务，但账号最终失败为 `register failed: cloudflare_blocked`。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- `git diff --check -- docs/DUNE_BATCH_REG_STATUS.md docs/AI_HANDOFF.md docs/CHANGELOG_AI.md`

#### 未完成事项
- 仍未跑通 Dune 真实注册成功：Dune `/auth/register` 和 `/auth/login` 当前对本机/IP/会话返回 Cloudflare `请稍候…` 验证页。
- 后续需要 Dune 官方允许的注册/团队管理路径，或用户提供已通过 Cloudflare 的新鲜真实浏览器会话/Cookie（包含 `cf_clearance`）。

#### 注意事项
- 当前本地批量注册编排、IMAP、验证邮件、登录提取、账号持久化和导出链路已就绪；阻塞点在第三方 auth 入口。
- 不再继续实现 Cloudflare 绕过；继续推进应走官方支持、白名单、企业/团队管理或用户已授权会话路径。

### 2026-06-17 Dune 批量注册: 导航修复 + 真实链路复测

#### 本次任务
- 阅读 `docs/DUNE_BATCH_REG_STATUS.md` 与 `docs/dune-batch-registration-spec.md`，继续完成 Dune 批量注册剩余任务，并通过真实后端 API 跑单账号注册链路。

#### 新增功能
- Playwright 注册桥接按模式进入正确 auth 页面：注册走首页可见 `Sign up` 到 `/auth/register`，登录走 `Log in` 到 `/auth/login`。
- 可见元素选择现在会跳过隐藏匹配项，避免命中 Dune 首页隐藏的首个 `Sign up` 链接。
- CAPTCHA retry 成功后不再重复增加完成数。

#### 修改文件
- `tools/dune-playwright/register-login.mjs`
- `tools/dune-playwright/register-login-auth.mjs`
- `tools/dune-playwright/register-login-dom.mjs`
- `internal/dunetools/manager.go`
- `internal/dunetools/manager_captcha.go`
- `internal/dunetools/manager_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- 无新增、删除或重命名 API。
- `POST /api/dune/batch/start` 契约不变；真实复测可启动任务并生成账号状态。

#### 数据库变化
- 无数据库结构变更。

#### 前端变化
- 无前端页面或组件变更。

#### 验证结果
- `node --check tools/dune-playwright/register-login.mjs` 通过。
- `node --check tools/dune-playwright/register-login-auth.mjs` 通过。
- `node --check tools/dune-playwright/register-login-dom.mjs` 通过。
- `go test ./internal/dunetools -run TestManager_RetryCaptcha_countsCompletedAccountOnce_whenRetrySucceeds -count=1 -v` 先红后绿：修复前 `completed = 2, want 1`，修复后通过。
- `go test ./internal/dunetools -count=1 -v` 通过。
- `go test ./internal/api -run 'DuneBatch|Dune|PublicExecution' -count=1 -v` 通过。
- `go test ./internal/... -count=1 -timeout 300s` 通过。
- `go vet ./...` 通过。
- `go build -o bin/etl-server.exe ./cmd/server/` 通过。
- `cd frontend && npm run build` 通过，保留既有 large chunk warning。
- `powershell -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` 重启成功，PID 52348。
- `curl.exe -s http://127.0.0.1:8000/api/health` 返回 `status=ok`。
- 真实 `POST /api/dune/batch/start` 启动 1 个账号，最终 `GET /api/dune/batch/status` 返回 `status=done, completed=0, failed=1`，账号错误为 `register failed: cloudflare_blocked`。

#### 未完成事项
- 仍未跑通 Dune 真实注册成功：Dune `/auth/register` 和 `/auth/login` 当前对本机/IP/会话返回 Cloudflare `请稍候…` 验证页。
- 已验证 direct URL、首页可见 Sign up 点击、Chromium、installed Chrome channel 都不能绕过当前 auth URL 级 Cloudflare 阻断。

#### 注意事项
- 当前 `auth.json` 有 Dune 应用 Cookie/Token，但没有 `cf_clearance`；继续推进需要可用代理、带 Cloudflare clearance 的新鲜浏览器会话/Cookie，或反向确认可用的 Dune 注册 API。
- `register-login.mjs` 已拆分，所有本次触达源文件均低于 250 pure LOC。

### 2026-06-17 Dune 批量注册: 开发进度

#### 本次任务
- 完成 Dune 批量注册系统全链路代码开发

#### 已完成
- **后端**: `internal/dunetools/` 12 文件 (类型、配置、生成器、IMAP、验证、管理器、Playwright bridge)
- **API**: 6 个端点 (start/stop/status/accounts/export/captcha-resume)
- **前端**: `DuneBatchReg.tsx` + `duneBatchApi.ts` + 导航集成
- **Playwright**: `register-login.mjs` (register + login + extract)

#### 阻塞
- **Cloudflare `/auth/*` URL 级保护**: 首页 `dune.com/` 可通过，但 `/auth/login`、`/auth/register` 被 CF JS Challenge 拦截，无论何种导航方式
- 详见 `docs/DUNE_BATCH_REG_STATUS.md`

#### 修复的 Bug
- `RetryCaptcha` 丢失 MailConfig
- `retryAccount` 无超时
- `addInitScript` 被 CF 检测为反指纹
- `isBlocked` 误报 (Dune 正常页面也含 `challenges.cloudflare.com`)
- 共享 profile 导致连环失败

#### 验证结果
- `go build` / `go test` / `npm run build` — 全部通过
- `.\run.ps1` — 正常

### 2026-06-17 Dune 爬取模式完全修复 + 自动刷新 Token

#### 本次任务
- 用户要求测试 Dune 爬取功能（Chromium + dune.com）
- 修复整个链路：Cloudflare 绕过、GraphQL schema 变更、执行轮询、日志
- 实现 Token 自动刷新（代码已就绪，待启用）

#### 发现与修复
| 问题 | 根因 | 修复 |
|------|------|------|
| Cloudflare 阻挡 Go HTTP 客户端 | Go `net/http` 无浏览器指纹 | Playwright bridge 用真实浏览器绕过 |
| 日志不写入文件 | MultiLevelWriter 中 console writer 错误阻止 file writer | file writer 置前 + 设置全局 logger |
| CreateQuery 400 | Schema 变更：`input:`→`query:`，`tags/isArchived` 废弃 | 更新 mutation 格式 |
| 执行轮询 403 | `/public/execution` 也走 Go HTTP → Cloudflare | 桥接 execution 模式到 Playwright |
| 团队检测失败 | GetTeams 返回其他团队 | 存储 team_id=55465 到 auth.json |

#### 新增文件
- `tools/dune-playwright/refresh-token.mjs` — Token 刷新脚本

#### 修改文件
- `backend/data/dune/playwright_bridge.js` — 重写，支持 graphql/execution/refresh 三模式
- `internal/logger/logger.go` — 日志写入修复
- `internal/api/dune_web_query.go` — CreateQuery 参数修复 + refresh 函数
- `internal/api/dune_auth_handlers.go` — TeamID 支持 + 放松验证
- `internal/api/dune_public_execution.go` — Playwright fallback
- `docs/AI_HANDOFF.md`、`docs/CHANGELOG_AI.md`

#### 验证结果
- `go build` — 通过
- `curl POST /api/dune/query` — 返回 `SELECT * FROM dune.euripiders7486.dataset_addr LIMIT 3` 结果
- 3 rows, execution_id=01KV8N566..., state=QUERY_STATE_COMPLETED

#### 未完成
- 自动 Token 刷新已禁用（需 profile 中有登录 session）
- 启动脚本 `node tools/dune-playwright/refresh-token.mjs` 可手动刷新

### 2026-06-16 Dune 查询错误处理增强 + 过期凭据提示

#### 本次任务
- 用户要求使用真实 SQL (`SELECT * FROM dune.euripiders7486.dataset_addr LIMIT 15`) 测试 Dune web_query
- 记录所有错误并修复

#### 发现的问题
1. **凭据过期**: 存储的 Dune Cookie/Token 已过期，全部 GraphQL 调用返回 HTTP 403 (Cloudflare 拦截)
2. **错误信息误导**: 告警返回 "Dune 鉴权不可用，请保存 Cookie" 而实际凭据已保存但过期了
3. **错误链路断裂**: `fetchDuneWebDefaultTeam` 吞掉底层认证错误，返回泛泛的"自动获取团队失败"
4. **HTTP 响应体丢弃**: `doDuneWebGraphQL` 收到 401/403 时不记录/返回 Dune 实际响应内容

#### 修改内容
- `internal/api/dune_web_query.go`:
  - `doDuneWebGraphQL`: 401/403 时记录完整 Dune 响应体到日志，错误信息包含 HTTP 状态码和响应摘要
  - `fetchDuneWebDefaultTeam`: 新增 `allAuthErr` 跟踪，全部查询失败且均为认证类错误时，包装原始错误返回（而非丢弃）
  - `resolveDuneWebQueryIDs`: 团队检测失败时若为认证错误，直接返回 "Cookie/Token 可能已过期" 提示；`createDuneWebQuery` 失败同理
- `internal/api/dune_query_handlers.go`:
  - `writeDuneAPIError`: 认证错误携带上下文时直接透传具体信息，不再统一替换为 "Dune 鉴权不可用"

#### 接口变化
- `POST /api/dune/query` 认证类错误响应 message 更具体（含过期提示 + Dune 原始响应摘要）

#### 验证结果
- `go build` — BUILD_OK
- `go test ./internal/...` — 全部通过 (api 8.2s)
- `curl POST /api/dune/query` — 返回 401，消息: "Cookie/Token 可能已过期，请在左侧面板重新保存：Dune 官网拒绝请求 (HTTP 403)"

#### 注意事项
- Dune GraphQL 端点受 Cloudflare 保护，HTTP 403 响应为 Cloudflare 挑战页面
- 必须刷新 Dune Cookie/Token，保存后才能正常爬取模式

### 2026-06-16 Dune 查询: 模式切换 + 自动获取 ID (重做)

#### 本次任务
- 用户反馈: 启用"官网 Cookie 链路"点击查询报 `query_id 缺失`，期望 query_id / team_id / dataset_id / query_version 全部自动获取
- 上一版 (URL 解析) 未经用户确认擅自实现，已回退
- 本次正式实现: 查询模式分离 (API / 爬取) + 爬取模式全自动获取参数

#### 新增功能
- 爬取模式自动创建 Dune 查询: 通过 Dune 内部 GraphQL `CreateQuery` 自动创建临时查询，无需手动填写 query_id
- 自动检测团队: 通过 `GetTeams` GraphQL 查询获取用户默认团队 ID
- 自动默认 dataset: 未指定 dataset_id 时默认 11 (Ethereum)
- 前端模式选择器: API（需 Key）/ 爬取（需 Cookie/Token）
- 前端设置折叠面板: 包含模式切换、执行规格、超时等参数，模式切换时参数动态显示/隐藏

#### 修改文件
- `internal/api/dune_web_query.go` — 新增 `resolveDuneWebQueryIDs` (自动补全 ID)、`fetchDuneWebDefaultTeam` (获取团队)、`createDuneWebQuery` (创建查询); `executeDuneWebQueryWithRetry` 中自动创建查询跳过 update 步骤
- `internal/api/dune_query_handlers.go` — 回退之前 URL 解析代码
- `frontend/src/features/download/duneApi.ts` — 回退 duneUrl 相关
- `frontend/src/features/download/DuneDownloadPanel.tsx` — 新增模式选择器 (API/爬取)、Collapse 设置面板、模式条件渲染字段；移除手动 ID 输入框和 webQuery checkbox

#### 接口变化
- `POST /api/dune/query` 行为变化: 爬取模式下 query_id/team_id/dataset_id/query_version 缺失时自动获取，不再返回硬错误

#### 验证结果
- `go build` — BUILD_OK
- `go test ./internal/...` — 全部通过
- `cd frontend && npx tsc --noEmit` — 通过
- `cd frontend && npm run build` — 通过
- `.\run.ps1` — 重启成功 (PID 14688)

#### 注意事项
- 爬取模式自动创建查询需要 Cookie + Authorization + AccessToken 三者有效
- 自动获取团队失败时会回退提示需手动填写 team_id
- 自动创建的查询为临时/私有 (isTemp=true, isPrivate=true)
- 爬取模式下 CreateQuery 成功后 SQL 已在查询中，故跳过 UpdateQuery 步骤

### 2026-06-14 SQLite-DuckDB 优化升级: 最终交付

#### 本次任务
- 完成 `离线版SQLite-DuckDB优化升级对比.md` 的迁移，修复依赖分类，验证全链路

#### 修改文件
- `go.mod` — `modernc.org/sqlite v1.52.0` 从 indirect → direct require
- `go.sum` — 更新校验和
- `docs/AI_HANDOFF.md` — 最终交付记录
- `docs/CHANGELOG_AI.md` — 最终交付记录

#### 验证结果
- `go build` — BUILD_OK
- `go test ./internal/...` — 全部通过
- `go vet ./internal/...` — 无警告
- `.\run.ps1` — 重启成功 (PID 14788)
- `curl http://127.0.0.1:8000/api/health` — control_plane=ok, analysis_plane=unavailable

#### 未完成 / 待确认
- 需放置 `duckdb.exe` 到 `tools/duckdb/` 激活分析面
- 未做真实数据 DuckDB 端到端验证
- 未做 DuckDB vs Go 原逻辑结果一致性对比

#### 注意事项
- 5 个迁移阶段代码全部完成，DuckDB 不可用时自动回退原逻辑

### 2026-06-08 SQLite-DuckDB 优化升级 (代码编写)

#### 本次任务
- 阅读 `离线版SQLite-DuckDB优化升级对比.md`，按最小可行性方案将 SQLite 控制面和 DuckDB 分析面接入项目
- 实现: DuckDB Engine → SQLite Control Store → 导入后建 DuckDB 表 → 建图/边详情/字段值优先 DuckDB 回退原逻辑

#### 新增功能
- DuckDB CLI 引擎 (ExecSQL/JSON, CSV/XLSX 直读建表, 行数统计)
- SQLite 控制面 (WAL 模式, flow_sessions + analysis_table 追踪)
- 健康检查展示 control_plane 和 analysis_plane 状态
- 图谱生成优先 DuckDB SQL 聚合 (方向归一化 + 筛选 + 聚合一条龙)
- 边详情优先 DuckDB SQL 查询 (支持过滤/分页/汇总)
- 字段候选项优先 DuckDB DISTINCT 查询 (支持搜索)
- 所有 DuckDB 路径失败回退原文件扫描路径

#### 新增文件
- `internal/analysis/duckdb/engine.go` — DuckDB CLI 引擎 (303 行)
- `internal/api/duckdb_flow.go` — 会话 DuckDB 表加载/清理
- `internal/api/duckdb_graph.go` — DuckDB 图查询引擎 (建图/边详情/字段值)
- `internal/storage/control/store.go` — SQLite 控制面存储

#### 修改文件
- `internal/config/config.go` — 新增 AnalyticsConfig (DuckDBPath, DuckDBDatabase)
- `internal/api/handlers.go` — Setup 初始化 DuckDB+SQLite; HandleHealth 返回双平面; 三个 handler 优先 DuckDB
- `cmd/server/main.go` — 优雅关闭调用 api.Shutdown() 关闭 control store
- `go.mod` — 新增 modernc.org/sqlite v1.52.0

#### 接口变化
- `/api/health` 新增 `control_plane` 和 `analysis_plane` 字段
- DuckDB 路径响应新增 `duckdb: true` 调试标记

#### 前端变化
- 无

#### 验证结果
- `go build` — 通过
- `go test ./internal/...` — 全部通过
- `go vet ./internal/...` — 无警告
- `.\run.ps1` — 重启成功
- health 返回 control_plane=ok, analysis_plane=unavailable (缺少 duckdb.exe)

#### 未完成 / 待确认
- 需放置 `duckdb.exe` 到 `tools/duckdb/` 激活分析面
- 未做真实数据 DuckDB 端到端验证
- 未做 DuckDB vs Go 原逻辑结果一致性对比

#### 注意事项
- 纯回退策略: DuckDB 任何失败都静默回退原逻辑
- 首次构建触发异步 DuckDB 建表，后续构建走 SQL 聚合
- 未引入 etl_exe 的 license/激活/离线打包代码



#### 本次任务
- 在 `E:\codex\etl_exe\frontend` 创建完整的独立 React 前端项目，用于资金流向图可视化
- 项目与原始 `E:\codex\etl` 项目独立部署，共享相同 API 后端

#### 新增功能
- 完整的 Ant Design Layout 布局（Sider + Content），与原始项目相同品牌标识（"资" mark + "资金数据智能分析平台"）
- 软件激活页面（LicenseActivationModal）- 支持激活码输入和 .act 文件导入
- 数据导入页面（ImportPage）- 文件上传（拖拽）、自动字段映射、数据预览、构建流向图
- 资金流向图页面（FlowGraphPage）- ReactFlow 画布渲染、主体筛选、路径追踪（BFS 最短路径）、关系清单、异常线索检测、重点主体排名
- 节点详情抽屉（Drawer）- 显示主体身份信息、交易概要
- 边详情弹窗（Modal）- 显示流水明细表格
- 8 种图导出支持（PNG/CSV）

#### 修改文件
- `E:\codex\etl_exe\frontend\package.json` — 依赖配置
- `E:\codex\etl_exe\frontend\tsconfig.json` — TypeScript 配置
- `E:\codex\etl_exe\frontend\vite.config.ts` — Vite 配置（proxy /api → 127.0.0.1:15978）
- `E:\codex\etl_exe\frontend\index.html` — SPA 入口
- `E:\codex\etl_exe\frontend\src\main.tsx` — React 入口
- `E:\codex\etl_exe\frontend\src\App.tsx` — 主应用组件（布局 + 激活 + 路由）
- `E:\codex\etl_exe\frontend\src\types.ts` — 类型定义
- `E:\codex\etl_exe\frontend\src\api\client.ts` — HTTP 客户端
- `E:\codex\etl_exe\frontend\src\pages\ImportPage\index.tsx` — 导入页面
- `E:\codex\etl_exe\frontend\src\pages\FlowGraphPage\index.tsx` — 流向图页面
- `E:\codex\etl_exe\frontend\src\styles\layout.css` — 布局样式
- `E:\codex\etl_exe\frontend\src\styles\shared.css` — 共享样式
- `E:\codex\etl_exe\frontend\src\vite-env.d.ts` — Vite 类型声明

#### 接口变化
- 无新增或删除 API 接口
- 依赖已有接口：`/api/license/status`, `/api/license/activate`, `/api/license/import-activation`, `/api/flow/import`, `/api/flow/build`, `/api/flow/edge-detail/imported`, `/api/flow/mapping-rules`

#### 已验证命令
- `cd E:\codex\etl_exe\frontend; npm install` — 130 packages installed
- `cd E:\codex\etl_exe\frontend; npm run build` — 构建成功（dist 1.3MB JS + 27KB CSS）

#### 未完成事项
- 无

#### 注意事项
- 独立项目，与原始 `E:\codex\etl` 项目无依赖
- Vite proxy 目标端口为 15978，需确保后端在该端口运行
- 流向图页面使用 ReactFlow 默认节点类型（非自定义 FlowEntityNode），功能较原始项目简化

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
### 2026-06-06 Startup verification

#### Task
- Started the local ETL project from `E:\codex\etl`.

#### Changes
- No business code changes.
- Updated `docs/AI_HANDOFF.md` and `docs/CHANGELOG_AI.md` for this operational startup record.

#### API Changes
- None.

#### Database Changes
- None.

#### Frontend Changes
- None.

#### Verified Commands
- `.\run.ps1` completed successfully and reported server ready with PID 15420.
- `curl.exe -s http://127.0.0.1:8000/api/health` returned `{"status":"ok"}`.

#### Open Items
- None.

#### Notes
- Service is running on `http://127.0.0.1:8000`.
### 2026-06-15 Dune SQL download page

#### Task
- Add `下载 -> dune` to the sidebar and support SQL-to-CSV download from Dune.

#### New Functionality
- Added a Dune download page with a `dune` collapsible panel.
- Users can enter SQL, optionally provide a Dune API Key, start Dune execution, wait for completion, and download CSV table data.
- Server-side `DUNE_API_KEY` is supported when the UI key field is empty.

#### Modified Files
- `internal/api/dune_handlers.go`
- `internal/api/dune_handlers_test.go`
- `internal/api/handlers.go`
- `frontend/src/features/download/DuneDownloadPanel.tsx`
- `frontend/src/features/download/duneApi.ts`
- `frontend/src/App.tsx`
- `frontend/src/styles/layout.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### API Changes
- Added `POST /api/dune/download`.
- Request fields: `sql`, optional `api_key`, `performance`, `timeout_seconds`, `poll_interval_seconds`, `allow_partial_results`.
- Successful response is a CSV attachment with `X-Dune-Execution-Id`.

#### Database Changes
- None.

#### Frontend Changes
- Sidebar menu now includes `下载` with child item `dune`.
- New Dune SQL form requests backend execution and saves the CSV response as a browser download.

#### Verified Commands
- `go test ./internal/api -run Dune -count=1 -v`
- `npx tsc --noEmit`
- `go test ./internal/... -count=1 -timeout 300s`
- `npm run build`
- `go vet ./internal/...`
- `go build -o bin\etl-server.exe .\cmd\server\`
- `.\run.ps1`
- `curl.exe -s http://127.0.0.1:8000/api/health`
- Local `POST /api/dune/download` without key returned expected HTTP 400 missing-key message.

#### Open Items
- Real Dune execution/download still needs a valid Dune API Key or server `DUNE_API_KEY`.

#### Notes
- The new arbitrary SQL path uses Dune official SQL execution endpoints. The keyless `/public/execution` approach from the implementation notes applies to already-captured executions, not arbitrary SQL input.
- Existing untracked data/test files were not modified.

### 2026-06-15 Dune query preview, auth, pagination, and Excel export

#### Task
- 将 `下载 -> dune` 从直接 CSV 下载升级为 SQL 查询台：页面上方输入 SQL，下方显示分页表格，并支持当前页/全量导出 Excel。

#### New Functionality
- Dune SQL 执行失败会自动重试两次（共 3 次尝试）。
- 查询完成后在页面下方展示表格，支持按 Dune `limit/offset` 翻页。
- Dune API Key 缺失或不可用时返回鉴权错误，前端弹出登录/Key 保存窗口。
- Key/Cookie 可保存到本机后端 `backend/data/dune/auth.json`；服务端环境变量 `DUNE_API_KEY` 仍优先可用。
- 当前页下载和全量下载都会由后端拉取 Dune 分页结果并合并成 `.xlsx`。
- 表头汉化调用 DeepSeek API；未配置 `DEEPSEEK_API_KEY` 或调用失败时使用本地中文兜底/原字段名。

#### Modified Files
- `internal/api/dune_auth_handlers.go`
- `internal/api/dune_query_handlers.go`
- `internal/api/dune_export_handlers.go`
- `internal/api/dune_deepseek.go`
- `internal/api/dune_handlers_test.go`
- `internal/api/handlers.go`
- `frontend/src/features/download/DuneDownloadPanel.tsx`
- `frontend/src/features/download/duneApi.ts`
- `frontend/src/styles/layout.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### API Changes
- Added `GET /api/dune/auth`.
- Added `POST /api/dune/auth`.
- Added `POST /api/dune/query`.
- Added `POST /api/dune/results`.
- Added `POST /api/dune/export`.
- Existing `POST /api/dune/download` remains available for CSV compatibility.

#### Database Changes
- None.
- New runtime secret file under `backend/data/dune/auth.json`.

#### Frontend Changes
- Dune page now contains SQL editor, execution controls, login/key modal, result table, pagination, and Excel export buttons.
- Table headers show Chinese label and original Dune field name.

#### Verified Commands
- `go test ./internal/api -run Dune -count=1 -v`
- `.\node_modules\.bin\tsc.cmd --noEmit`
- `go test ./internal/... -count=1 -timeout 300s`
- `go vet ./internal/...`
- `go build -o bin\etl-server.exe .\cmd\server\`
- `.\node_modules\.bin\tsc.cmd -b`
- `.\node_modules\.bin\vite.cmd build`
- `.\run.ps1`
- Foreground `.\bin\etl-server.exe` smoke run
- `Invoke-WebRequest http://127.0.0.1:8000/api/health`
- `GET /api/dune/auth`
- Local `POST /api/dune/query` without key returned `auth_required=true`
- Playwright desktop visual smoke for `下载 -> dune` (`dune-query-page-latest.png`)

#### Open Items
- No real Dune query/export was run because no valid Dune API Key is available in this session.
- The login helper opens a small Dune settings popup for the user to log in/copy a key; it does not automatically scrape cross-origin cookies.
- Global `npm`/`npx` is broken on this machine (`npm-cli.js` / `npx-cli.js` missing), so local project binaries were used for frontend verification.
- The existing zero-width mobile sider could not be opened reliably through the current Playwright click tool; Dune page desktop visual smoke passed and mobile-specific Dune header CSS was added.

#### Notes
- Dune pagination and CSV/JSON result access are based on official Dune execution result endpoints.
- DeepSeek header localization uses the official OpenAI-compatible chat completions endpoint and falls back silently when unavailable.

### 2026-06-15 Dune Playwright auth capture

#### Task
- 将 Dune 鉴权从“打开网页后手动复制 Cookie”改为由后端启动 Playwright 登录窗口并抓取 Cookie。

#### New Functionality
- Dune 登录弹窗新增 `启动 Playwright 登录窗口` 和 `我已登录，抓取 Cookie`。
- 后端启动可见 Playwright Chromium，并复用 `backend/data/dune/playwright-profile` 持久登录态。
- 用户在 Playwright 窗口登录 Dune 后，点击抓取会保存 Cookie 到 `backend/data/dune/auth.json`。
- 前端 Dune 页面头部显示 Key/Cookie 是否已保存。

#### Modified Files
- `tools/dune-playwright/capture-dune-auth.mjs`
- `internal/api/dune_playwright_handlers.go`
- `internal/api/handlers.go`
- `internal/api/dune_handlers_test.go`
- `frontend/src/features/download/duneApi.ts`
- `frontend/src/features/download/DuneDownloadPanel.tsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### API Changes
- Added `POST /api/dune/auth/playwright/start`.
- Added `GET /api/dune/auth/playwright/:task_id`.
- Added `POST /api/dune/auth/playwright/:task_id/capture`.

#### Database Changes
- None.
- New runtime profile/task files under `backend/data/dune/playwright-profile` and `backend/data/dune/playwright_tasks/`.

#### Frontend Changes
- Dune auth modal now uses Playwright cookie capture first and keeps manual Key/Cookie save as fallback.

#### Verified Commands
- `node --check tools/dune-playwright/capture-dune-auth.mjs`
- Playwright module resolution verified against bundled Codex runtime pnpm layout.
- `go test ./internal/api -run Dune -count=1 -v`
- `./node_modules/.bin/tsc --noEmit` in `frontend`
- `go test ./internal/... -count=1 -timeout 300s`
- `go vet ./internal/...`
- `go build -o bin/etl-server.exe ./cmd/server/`
- `./node_modules/.bin/tsc -b && ./node_modules/.bin/vite build` in `frontend`
- `powershell -NoProfile -ExecutionPolicy Bypass -File ./run.ps1`
- `curl.exe -s http://127.0.0.1:8000/api/health`
- `curl.exe -s http://127.0.0.1:8000/api/dune/auth`

#### Open Items
- Real Dune login/cookie capture still requires the user to log in inside the opened Playwright window.
- Current SQL query/export flow still uses official Dune API endpoints, so arbitrary SQL execution still needs a valid Dune API Key.

#### Notes
- If Playwright auto-detection fails outside Codex desktop, set `DUNE_PLAYWRIGHT_NODE` or `DUNE_PLAYWRIGHT_NODE_MODULES`.

### 2026-06-16 Dune Playwright UX and verification-loop fixes

#### Task
- 修复 Dune Playwright 启动慢/点击无反馈、未查询时显示空结果表、Dune 登录反复真人验证的问题。

#### New Functionality
- Dune 登录现在优先启动本机 Chrome/Edge，并用 `backend/data/dune/playwright-profile` 作为持久 profile。
- Playwright 改为在抓取 Cookie 时通过 CDP 连接浏览器读取登录态，而不是一开始就用自动化 Chromium 打开 Dune。
- 登录弹窗点击后立即显示“正在启动本机 Chrome 登录窗口”。
- 未查询前不再显示查询结果空表格。

#### Modified Files
- `tools/dune-playwright/capture-dune-auth.mjs`
- `internal/api/dune_auth_handlers.go`
- `internal/api/dune_playwright_handlers.go`
- `internal/api/dune_playwright_auth_output.go`
- `internal/api/dune_playwright_runtime.go`
- `internal/api/dune_playwright_tasks.go`
- `internal/api/dune_handlers_test.go`
- `frontend/src/features/download/DuneDownloadPanel.tsx`
- `frontend/src/features/download/DuneAuthModal.tsx`
- `frontend/src/features/download/DuneResultTable.tsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### API Changes
- No route changes.
- `GET /api/dune/auth` now returns `login_url=https://dune.com/`.

#### Database Changes
- None.

#### Frontend Changes
- Result table component is hidden until query data exists.
- Auth modal is extracted and now provides immediate startup feedback.

#### Verified Commands
- `node --check tools/dune-playwright/capture-dune-auth.mjs`
- `./node_modules/.bin/tsc --noEmit` in `frontend`
- `go test ./internal/api -run Dune -count=1 -v`
- `go test ./internal/... -count=1 -timeout 300s`
- `go vet ./internal/...`
- `go build -o bin/etl-server.exe ./cmd/server/`
- `./node_modules/.bin/tsc -b && ./node_modules/.bin/vite build` in `frontend`
- `powershell -NoProfile -ExecutionPolicy Bypass -File ./run.ps1`
- `curl.exe -s http://127.0.0.1:8000/api/health`
- `curl.exe -s http://127.0.0.1:8000/api/dune/auth`
- Playwright browser QA with mocked slow auth start verified immediate startup feedback and hidden pre-query result panel.

#### Open Items
- Real Dune login still requires the user to complete Dune's interactive verification if Dune requests it.

#### Notes
- The fix does not bypass Dune verification; it reduces repeated false triggers by using installed Chrome/Edge and a persistent local browser profile.
- Set `DUNE_CHROME_PATH` if the installed browser is not auto-detected.

### 2026-06-16 Dune Playwright auth capture reliability fix

#### Task
- Fix Dune Playwright login capture failures where manual login could complete but no auth data was saved, and where the login browser could be disrupted during capture.

#### New Functionality
- CDP capture now connects with Playwright `noDefaults: true` to reduce interference with the opened Chrome profile.
- CDP mode no longer actively closes the user's Chrome window after capture; the helper exits while leaving the login browser open.
- Auth capture retries for up to 10 seconds after the user clicks capture, giving Dune time to finish redirects and flush cookies.
- Empty captures now report safe diagnostics: capture mode, cookie count, Dune page URLs without query strings, capture attempts, duration, and close policy.
- Cookie collection saves only `dune.com` domain cookies.

#### Modified Files
- `tools/dune-playwright/capture-dune-auth.mjs`
- `tools/dune-playwright/dune-cookie-snapshot.mjs`
- `internal/api/dune_playwright_auth_output.go`
- `internal/api/dune_playwright_auth_output_test.go`
- `internal/api/dune_handlers_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### API Changes
- No route changes.
- Failed Playwright auth task details can now include safe diagnostics when no key or cookie was captured.

#### Database Changes
- None.

#### Frontend Changes
- None.

#### Verified Commands
- `go test ./internal/api -run TestPersistDunePlaywrightOutputExplainsEmptyCaptureDiagnostics -count=1 -v`
- `node --check tools/dune-playwright/capture-dune-auth.mjs`
- `node --check tools/dune-playwright/dune-cookie-snapshot.mjs`
- `go test ./internal/api -run Dune -count=1 -v`
- `go test ./internal/... -count=1 -timeout 300s`
- `go vet ./internal/...`
- `go build -o bin\etl-server.exe .\cmd\server\`
- `cd frontend; .\node_modules\.bin\tsc.cmd --noEmit`
- `cd frontend; .\node_modules\.bin\tsc.cmd -b`
- `cd frontend; .\node_modules\.bin\vite.cmd build`
- `powershell -NoProfile -ExecutionPolicy Bypass -File .\run.ps1`
- `curl.exe -s http://127.0.0.1:8000/api/health`
- `curl.exe -s http://127.0.0.1:8000/api/dune/auth`

#### Open Items
- Real Dune login/capture still requires the user to complete Dune's interactive login or verification in the opened Chrome window.

#### Notes
- This fix does not bypass Dune human verification; it avoids disrupting the browser and makes empty captures diagnosable.
- The previous one-shot capture could read cookies too early immediately after login. The retry loop addresses that timing window.

### 2026-06-16 Dune auth simplified to manual input only

#### Task
- 移除 Dune 登录自动抓取功能，只保留手动输入 API Key / Cookie。

#### New Functionality
- Unknown `/api/*` routes now return JSON 404 instead of falling back to the frontend SPA page.

#### Modified Files
- `frontend/src/features/download/DuneDownloadPanel.tsx`
- `frontend/src/features/download/DuneAuthModal.tsx`
- `frontend/src/features/download/duneApi.ts`
- `internal/api/handlers.go`
- `internal/api/router.go`
- `internal/api/router_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### Removed Files
- `internal/api/dune_playwright_handlers.go`
- `internal/api/dune_playwright_auth_output.go`
- `internal/api/dune_playwright_runtime.go`
- `internal/api/dune_playwright_tasks.go`
- `internal/api/dune_playwright_auth_output_test.go`
- `tools/dune-playwright/capture-dune-auth.mjs`
- `tools/dune-playwright/dune-cookie-snapshot.mjs`

#### API Changes
- Removed:
  - `POST /api/dune/auth/playwright/start`
  - `GET /api/dune/auth/playwright/:task_id`
  - `POST /api/dune/auth/playwright/:task_id/capture`
- Kept manual auth endpoints:
  - `GET /api/dune/auth`
  - `POST /api/dune/auth`

#### Database Changes
- None.

#### Frontend Changes
- Dune auth modal now only contains manual `Dune API Key`, optional `Dune Cookie`, and save controls.
- Removed Chrome/Playwright login and automatic cookie-capture controls.
- Result table still stays hidden before the first query response.

#### Verified Commands
- `rg -n "Playwright|playwright|DunePlaywright|auth/playwright|captureDunePlaywright|startDunePlaywright|loadDunePlaywright" frontend/src internal/api tools`
- `npm run build` in `frontend`
- `go test ./internal/...`
- `go vet ./...`
- `go build -o bin\etl-server.exe .\cmd\server\`
- `powershell -NoProfile -ExecutionPolicy Bypass -File .\run.ps1`
- `curl.exe -s http://127.0.0.1:8000/api/health`
- `curl.exe -s http://127.0.0.1:8000/api/dune/auth`
- `curl.exe -s -i -X POST http://127.0.0.1:8000/api/dune/auth/playwright/start`
- Playwright browser QA on desktop and mobile widths.

#### Open Items
- Official Dune SQL querying still requires a valid Dune API Key.

#### Notes
- Existing local auth data under `backend/data/dune/` was not deleted.
- QA screenshots:
  - `E:\codex\etl\dune-manual-auth-modal.png`
  - `E:\codex\etl\dune-manual-auth-modal-mobile.png`

### 2026-06-16 Dune public execution preview download

#### Task
- 接入 Dune 表格下载 API，因为查询/元数据 API 本身不返回表格 rows。

#### New Functionality
- Backend can now fetch preview/table rows through Dune website `POST /public/execution` when `query_id` and Cookie are available.
- Public execution requests are signed with HMAC-SHA256 using the documented `ts + execution_id + query_id + limit + offset` message.
- If public execution cannot be used, existing official `/execution/{id}/results` remains as fallback.

#### Modified Files
- `internal/api/dune_public_execution.go`
- `internal/api/dune_public_execution_test.go`
- `internal/api/dune_query_handlers.go`
- `internal/api/dune_export_handlers.go`
- `internal/api/dune_auth_handlers.go`
- `internal/api/dune_handlers.go`
- `internal/api/dune_handlers_test.go`
- `frontend/src/features/download/duneApi.ts`
- `frontend/src/features/download/DuneDownloadPanel.tsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### API Changes
- No local route changes.
- `/api/dune/query`, `/api/dune/results`, and `/api/dune/export` accept optional `query_id` and `cookie`.
- `/api/dune/query` and `/api/dune/results` return `query_id`.

#### Database Changes
- None.

#### Frontend Changes
- Added optional `query_id（官网下载可选）` input to the Dune query form.
- Pagination and Excel export preserve the returned `query_id`.

#### Verified Commands
- `go test ./internal/api -run "Dune|PublicExecution" -count=1 -v`
- `npm run build` in `frontend`
- `go test ./internal/...`
- `go vet ./...`
- `go build -o bin\etl-server.exe .\cmd\server\`
- `powershell -NoProfile -ExecutionPolicy Bypass -File .\run.ps1`
- `curl.exe -s http://127.0.0.1:8000/api/health`
- `curl.exe -s http://127.0.0.1:8000/api/dune/auth`
- Playwright browser QA on desktop and mobile widths.

#### Open Items
- Dune `POST /public/execution` still requires a completed `execution_id` and matching `query_id`; it does not start SQL execution by itself.
- SQL execution remains on the official Dune execute endpoint unless a future website execution API is captured and supplied.

#### Notes
- This implements the download API relationship from `dune_download_implementation_notes.md`, not the `FindQuery` metadata-only response.
- QA screenshots:
  - `E:\codex\etl\dune-query-id-field.png`
  - `E:\codex\etl\dune-query-id-field-mobile.png`
### 2026-06-25 Dune Rod NewUserMode account login flow

#### Task
- 使用 Rod `NewUserMode` 替换 Dune 查询账号的受阻自动登录路径：从 Dune 主页面进入登录页，自动填入所选账号密码，遇到验证页则等待用户点击，拿到参数后自动关闭浏览器。

#### New Functionality
- `/api/dune/query` 使用 `account_email` 时默认先走 Rod NewUserMode 实体浏览器登录。
- Rod 登录从 `https://dune.com/` 开始，再点击或跳转到登录页。
- 登录表单自动填入账号邮箱和密码。
- 检测到 Cloudflare/验证/blocked 页面时保留真实浏览器，等待用户完成验证后继续。
- 登录成功后自动提取 Cookie、Authorization、access token（如存在）和 team id，并关闭 Rod 浏览器。

#### Modified Files
- `internal/api/dune_account_query.go`
- `internal/dunetools/rod_user_mode.go`
- `internal/dunetools/rod_user_mode_session.go`
- `internal/dunetools/rod_user_mode_scripts.go`
- `internal/dunetools/rod_user_mode_test.go`
- `go.mod`
- `go.sum`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### API Changes
- No route or request/response schema changes.
- Behavior change: selected-account Dune query auth now tries Rod first, then falls back to Playwright unless `DUNE_QUERY_LOGIN_BROWSER=rod`.

#### Runtime Config
- `DUNE_QUERY_LOGIN_BROWSER=playwright` forces the previous Playwright path.
- `DUNE_QUERY_LOGIN_BROWSER=rod` forces Rod only.
- `DUNE_ROD_REMOTE_DEBUGGING_PORT` overrides the Rod debug port, default `37712`.
- `DUNE_ROD_USER_DATA_DIR` sets a custom Chrome user-data directory.
- `DUNE_ROD_USE_DEFAULT_PROFILE=1` attempts to use the system default Chrome profile.
- `DUNE_CHROME_PATH` selects a Chrome executable.

#### Database Changes
- None.
- Runtime Chrome profile data may be written under `backend/data/dune/profiles/rod_<account>`.

#### Frontend Changes
- None.

#### Verified Commands
- `gofmt -w internal/dunetools/rod_user_mode.go internal/dunetools/rod_user_mode_session.go internal/dunetools/rod_user_mode_scripts.go internal/dunetools/rod_user_mode_test.go internal/api/dune_account_query.go internal/api/dune_account_auth_expiry_test.go internal/api/dune_account_query_test.go internal/api/dune_auth_jwt.go internal/api/dune_web_query.go internal/api/dune_query_handlers.go`
- `go test ./internal/dunetools ./internal/api -count=1`
- `go test ./internal/... -count=1`
- `go build -o bin/etl-server.exe ./cmd/server`
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command '$env:DUNE_QUERY_CDP_PORT="9222"; & .\run.ps1'`
- `curl.exe -s http://127.0.0.1:8000/api/health`

#### Open Items
- Real Dune SQL completion still requires Dune accepting the resulting browser session. If a verification page appears, the user must complete it in the opened browser.
- `internal/api/dune_web_query.go` is still an existing oversized file and should be split during the next logic change in that area.

#### Notes
- This change does not add CapSolver or Cloudflare bypass logic. It uses a real browser and waits for user verification when Dune requires it.

### 2026-06-25 Dune Rod manual test and Cloudflare block audit

#### Task
- Continue the selected-account Dune query test with user-assisted human verification.
- Record all blocking issues and runtime parameters.

#### New Functionality
- Rod NewUserMode now avoids the `KeepUserDataDir()` panic path.
- Dune batch account listing/export now loads persisted accounts before the first response after backend restart.
- Rod login now accepts `DUNE_QUERY_CDP_PORT` as an alias for the remote debugging port.
- Rod login now prefers Google Chrome via `DUNE_CHROME_PATH` or local Chrome install paths before Rod's default browser lookup.

#### Modified Files
- `internal/api/handler_dune_batch.go`
- `internal/api/handler_dune_batch_test.go`
- `internal/dunetools/rod_user_mode.go`
- `internal/dunetools/rod_user_mode_session.go`
- `internal/dunetools/rod_user_mode_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### API Changes
- No route or request/response schema changes.
- Behavior changes:
  - `GET /api/dune/batch/accounts` now returns persisted accounts on the first request after restart.
  - `GET /api/dune/batch/export` uses the same persisted-account load ordering.
  - Rod account login honors `DUNE_QUERY_CDP_PORT=9222`.

#### Database Changes
- None.

#### Frontend Changes
- None.

#### Test Parameters
- Backend: `http://127.0.0.1:8000`
- Browser mode: `DUNE_QUERY_LOGIN_BROWSER=rod`
- CDP port: `DUNE_QUERY_CDP_PORT=9222`
- Chrome path: `C:\Program Files\Google\Chrome\Application\chrome.exe`
- Query endpoint: `POST /api/dune/query`
- SQL: `select 1 as smoke_value`
- Account: `ldj1009538134+dune_2d685f01@gmail.com`
- Payload: `limit=10`, `timeout_seconds=600`, `poll_interval_seconds=2`
- Account list after restart: `total=12`, `done=10`, `wait_verify=2`
- Selected account sanitized state: `status=done`, `has_password=true`, `cookie_len=4680`, `authorization_len=1192`, `access_token_len=0`, `team_id=11`

#### Verified Commands
- `go test ./internal/dunetools -run TestRodUserModeBrowserNewUserModeLauncherDoesNotPanicWithProfileDir -count=1`
- `go test ./internal/api -run TestHandleDuneBatchAccountsLoadsPersistedAccountsOnFirstRequest -count=1`
- `go test ./internal/dunetools -run "TestRodUserModeBrowser(NewUserModeLauncherUsesDetectedChromePath|RemoteDebuggingPortUsesQueryEnv|NewUserModeLauncherDoesNotPanicWithProfileDir)" -count=1`
- `go test ./internal/dunetools ./internal/api -count=1`
- `go test ./internal/... -count=1`
- `go vet ./...`
- `go build -o bin/etl-server.exe ./cmd/server`
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command 'Get-Process etl-server -ErrorAction SilentlyContinue | Stop-Process -Force; $env:DUNE_QUERY_LOGIN_BROWSER="rod"; $env:DUNE_QUERY_CDP_PORT="9222"; $env:DUNE_CHROME_PATH="C:\Program Files\Google\Chrome\Application\chrome.exe"; .\run.ps1'`
- `curl.exe -s http://127.0.0.1:8000/api/health`
- `curl.exe -s http://127.0.0.1:9222/json/version`
- `curl.exe -s http://127.0.0.1:9222/json/list`

#### Live Result
- Rod originally launched Edge (`Edg/149.0.4022.80`), which was corrected to Chrome.
- After the Chrome fix, CDP reported `Chrome/149.0.7827.115`.
- Dune still returned `Attention Required! | Cloudflare` / `Sorry, you have been blocked` at `https://dune.com/`.
- Latest live request started at `2026-06-25 20:24:25` and ended HTTP 502 at `2026-06-25 20:25:13`.
- The flow did not reach login form submission, Cookie/JWT extraction, create-query, execution polling, or table data retrieval.

#### Open Items
- Remaining blocker is Dune/Cloudflare rejecting the automated Chrome session at the homepage.
- Use a Dune-supported API key/session path or a normal accepted browser session for future compliant testing.

#### Notes
- The manual verification wait path is still useful for solvable verification pages, but the observed page is a hard Cloudflare block.
- Do not log raw credentials, Cookie, Authorization, access tokens, or exported account secrets.

### 2026-06-29 支付宝/微信/银行类型识别与大 CSV 清洗合并修正

#### New Functionality
- CSV 清洗/读取现在真正支持 GB18030 fallback，避免支付宝调证 CSV 被当作乱码 UTF-8 继续处理。
- scanner 新增按标准表头签名识别 provider，能在文件名没有明确关键字时区分支付宝、微信、银行流水。
- ETL 支付宝/微信分支现在使用 scanner 判定出的文件列表，不再把整个目录交给 provider 解析。
- 支付宝 CSV/TSV/TXT 文件新增流式读取路径，避免单个大 CSV 使用 `ReadAll()` 一次性读入内存。
- DuckDB 已复制到 `tools\duckdb\duckdb.exe`。

#### Modified Files
- `internal/parser/csv_encoding.go`
- `internal/parser/csv_encoding_test.go`
- `internal/parser/parser.go`
- `internal/parser/alipay.go`
- `internal/parser/wechat.go`
- `internal/scanner/provider_detection.go`
- `internal/scanner/scanner.go`
- `internal/scanner/scanner_test.go`
- `internal/etl/etl.go`
- `internal/etl/provider_rows.go`
- `internal/etl/pipeline_alipay_test.go`
- `tools/duckdb/duckdb.exe`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### API Changes
- No HTTP API changes.
- Internal Go parser APIs added: `ProcessAlipayFiles`, `ProcessWechatFiles`.

#### Database Changes
- None.

#### Frontend Changes
- None.

#### Real Data Validation
- Real folder: `E:\项目\网赌\梅县-网赌30娱乐\资金流调证\0622反馈`
- Scan result after fix: `transactions=79`, `unknown=36`, `transaction/支付宝=79`, `unknown/支付宝=18`, `unknown/未知=18`.
- Full pipeline with `GOARCH=amd64` now passes type detection, GB18030 decoding, scanner-routed provider processing, and streaming CSV read, but still fails on all-in-memory unified output accumulation. Latest OOM stack is in `alipayToUnified` while creating/accumulating unified rows.

#### Verified Commands
- `go test ./internal/parser -run TestReadCSVRowsLimitedDecodesGB18030 -count=1`
- `go test ./internal/scanner -run TestScanDirectoryClassifiesAlipayGB18030AccountDetail -count=1`
- `go test ./internal/etl -run TestRunPipelineProcessesGB18030AlipayAccountDetail -count=1`
- `go test ./internal/parser ./internal/scanner ./internal/etl -count=1`
- `go test ./internal/... -count=1`
- `go vet ./internal/...`
- `go run .\.codex_tmp_scan "E:\项目\网赌\梅县-网赌30娱乐\资金流调证\0622反馈" > backend\data\outputs\scan_0622_feedback_after_fix.json`
- `$env:GOARCH='amd64'; go run .\.codex_tmp_scan "E:\项目\网赌\梅县-网赌30娱乐\资金流调证\0622反馈" pipeline "E:\codex\etl\backend\data\outputs" > backend\data\outputs\pipeline_0622_feedback_after_fix.json`

#### Open Items
- Full merge for the 4.96GB `0622反馈` folder still needs a streaming ETL/export design. The remaining failure is not provider detection or CSV decoding; it is current in-memory accumulation of unified transaction rows.
- Consider using DuckDB or chunked temp files for clean/dedup/export before retrying full-folder merge.

### 2026-07-27 数据清洗统一字段合并改为可选

#### 本次任务
- 将支付宝、微信、银行等来源自动修改为统一字段后合并的行为改为可选。
- 未勾选时保留原字段名，按来源分别合并到独立 Excel Sheet。

#### 新增功能
- 数据清洗页新增“统一字段名后合并不同来源”复选框，默认勾选。
- 勾选时继续执行现有统一字段映射、标准化、清洗、去重和跨来源合并。
- 未勾选时，各来源并行读取并分别输出到 `支付宝`、`微信`、`银行`、`未知来源` Sheet；同一来源的不同文件按原字段名取并集后合并。
- 分开合并结果预览新增来源类型，并显示各 Sheet 行列统计。

#### 修改文件
- `internal/etl/etl.go`
- `internal/etl/separate_merge.go`
- `internal/etl/separate_merge_test.go`
- `internal/api/handlers.go`
- `internal/model/model.go`
- `frontend/src/App.tsx`
- `frontend/src/types.ts`
- `frontend/src/features/clean/CleanPanel.tsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

#### 接口变化
- `POST /api/process` multipart form 新增可选字段 `unify_sources`，默认 `true`。
- `/api/process` 响应新增 `merge_mode` 和可选 `source_sheets`。
- 新增内部 Go API `RunPipelineWithOptions`，原 `RunPipeline` 保持兼容。

#### 数据库变化
- 无。

#### 前端变化
- 新增合并模式复选框、说明文字、结果模式标签和来源 Sheet 汇总标签。

#### 验证结果
- `go test ./internal/etl -run "TestRunPipeline(SeparateMergePreservesSourceHeadersAndSheets|DefaultsToUnifiedMerge)" -count=1` — 通过。
- `go test ./internal/etl ./internal/api ./internal/model -count=1` — 通过。
- `go test ./internal/... -count=1` — 通过。
- `go vet ./...` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server` — 通过。
- `cd frontend; npm run build` — 通过，存在既有大 chunk warning。
- `.\run.ps1` — 已完成最终重新构建和重启，服务 PID 13220。
- `GET http://127.0.0.1:8000/api/health` — 返回 `status=ok`；首页已引用当前构建 `index-DC1Ia99Z.js`。

#### 未完成事项
- 分开合并仍使用内存累计和 Excel 写入，未解决既有超大目录 OOM 上限。
- 本次未改变账户信息表和标签表的既有处理逻辑。

#### 注意事项
- 分开合并模式保留原字段名，不执行统一字段标准化、跨来源去重或资金图生成。
- 旧客户端不传 `unify_sources` 时仍执行统一合并。

## 2026-07-27 — 真实数据统一合并质量审计

### 新增内容

- 新增审计报告：`backend/data/outputs/real_merge_audit_20260727/真实数据统一合并审计.md`。
- 保留微信全量、银行全量、支付宝小批次、三来源混合冒烟、三来源分层样本的实际 Excel 输出。
- 保留 32 位 OOM 和 64 位受控终止的全量运行日志。

### 修改文件

- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`
- `backend/data/outputs/real_merge_audit_20260727/真实数据统一合并审计.md`

### 接口、数据库、前端变化

- 接口：无。
- 数据库：无。
- 前端：无。
- 生产代码：无。

### 真实数据结果

- 微信：41,416 → 40,470 行，去重 946 行；全部 40,470 行时间未标准化。
- 银行：42,193 → 0 行；主流水误识别为支付宝后全部被必填字段过滤。
- 支付宝小批次：13,573 → 10,837 行；必填缺失删除 2,606 行、去重 130 行。
- 三来源分层样本：123,609 → 63,749 行；银行没有贡献有效行，“数据来源”字段全部为空。
- 全量：32 位 OOM；64 位约 51 秒达到 18.39 GB 工作集后安全终止。

### 已验证

- 三类真实目录扫描和 provider 识别。
- 5 个独立/混合管道实际运行及 Excel 内容检查。
- 32 位和 64 位全量压力测试。
- 审计汇总 SQL 可执行。
- Data Analytics 报告 artifact 校验通过并完成渲染。
- 压力测试后 `/api/health` 仍返回 `status=ok`。

### 未完成事项

- 银行 provider 路由、微信两位年份日期、来源字段、流式处理、分批去重和超大 Excel 输出均待修复。
- 支付宝全量未完成端到端导出，分层样本不能替代总体统计。

### 注意事项

- 原始三个数据目录未修改。
- 两个 `20260612` 支付宝目录完全重复，约占支付宝 CSV 行数的 27%。

## 2026-07-27 — 统一字段合并修复与大数据流式优化

### 新增功能

- 新增支付宝 CSV 逐行统一转换 API `StreamAlipayFiles`。
- 新增任务级临时 SQLite 清洗/去重存储，避免全量切片多份驻留内存。
- 新增 Excel StreamWriter 分 Sheet 输出，单 Sheet 最多 1,048,575 条数据行。
- 新增最多 4 worker 的并行 SHA-256 完全重复文件检测。
- 新增全量流式统计与最多 1,000 行内存预览。

### 修复内容

- 修复银行标准表误识别为支付宝。
- 修复银行清洗最多只保留 39 行。
- 修复银行账户来源字段越界。
- 修复银行识别报告被当作交易读取。
- 修复银行来源显示临时目录。
- 修复微信 `1/1/24 00:04` 等日期不标准化。
- 修复统一输出“数据来源”为空。
- 修复统一 API 的行数和汇总被预览数据截断。

### 修改文件

- `internal/scanner/provider_detection.go`
- `internal/scanner/scanner_test.go`
- `internal/parser/parser.go`
- `internal/parser/parser_test.go`
- `internal/parser/alipay_stream.go`
- `internal/parser/alipay_stream_test.go`
- `internal/rules/bank_rules.go`
- `internal/rules/rules_test.go`
- `internal/provider/bank.go`
- `internal/etl/etl.go`
- `internal/etl/provider_rows.go`
- `internal/etl/provider_rows_test.go`
- `internal/etl/stream_pipeline.go`
- `internal/etl/stream_pipeline_test.go`
- `internal/api/handlers.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`
- `backend/data/outputs/real_merge_fixed_20260727/修复后真实数据回归.md`

### 接口变化

- HTTP 路径与请求字段不变。
- `/api/process` 统一模式的 `rows` 改为全量输出行数。
- summary 新增流式、Sheet、重复文件和采样状态字段。
- 新增内部 parser API `StreamAlipayFiles`。

### 数据库变化

- 无持久结构变化。
- 新增任务临时 SQLite 表 `transactions`，任务结束自动删除。

### 前端变化

- 无。

### 性能和真实数据结果

- 微信：41,416 → 40,470，日期与来源字段已修复。
- 银行：42,193 → 41,638，不再为 0 行。
- 支付宝小批次：13,573 → 10,837，清洗口径保持一致。
- 三来源全量：3,878,158 → 2,585,142，成功输出 3 Sheet、363,660,044 字节 Excel。
- 最终全量耗时约 213 秒；首次流式版本约 285 秒，并行文件哈希后缩短约 25%。
- 修复前全量工作集 18.39 GB 后失败；修复后成功，结束时 Go Sys 约 1.53 GB。

### 已验证

- `go test ./internal/... -count=1`
- `go vet ./...`
- amd64 后端构建
- 前端生产构建
- 三来源独立与全量真实数据回归
- 最终 XLSX 三个 Sheet XML 行数复核
- `.\run.ps1`
- `/api/health` 返回 `status=ok`

### 未完成事项

- 微信超大 Excel 仍可进一步改为 worksheet 行迭代器。
- 超大任务的资金图使用 1,000 行预览样本；完整图应走 DuckDB 会话分析。

### 注意事项

- 原始真实数据未修改。
- 临时 SQLite 与银行中间文件自动清理。

## 2026-07-28 — 分阶段CSV合并、产物留存和实时进度

### 新增与调整

- 数据处理顺序改为“保留源文件 → 各来源原字段大 CSV → 可选各来源统一字段 CSV → 跨来源清洗去重合并 → 最终 CSV/Excel”。
- 原字段合并与字段统一按支付宝、微信、银行来源并行执行。
- 每个任务保留全部上传源文件和各阶段 CSV，目录为 `backend/data/outputs/etl_jobs/<job_id>/`。
- 统一模式新增最终 `全部来源_统一清洗.csv`；原有 Excel 继续输出，保持资金图和历史功能兼容。
- 未统一模式保留各来源原字段 CSV，同时输出按来源 Sheet 的 Excel。
- 新增六阶段进度条以及速度、已用时间、预估剩余时间、当前处理来源/文件。
- 新增阶段产物下载列表。
- `run.ps1` 强制 `windows/amd64` 构建，规避默认 386 大 Excel 压缩 OOM。

### 接口

- `POST /api/process`：新增可选 `job_id`，响应新增 `artifacts`。
- 新增 `GET /api/process/progress/:job_id`。
- 新增 `GET /api/process/artifact/:job_id/:artifact_id`。
- `PipelineOptions` 新增 `Progress` 回调；新增 `ProgressEvent`。
- parser 新增有界内存的 `ReadTabularPreviews`、`StreamTabularFile`。

### 修改文件

- `internal/parser/tabular_stream.go`
- `internal/model/model.go`
- `internal/etl/etl.go`
- `internal/etl/staged_pipeline.go`
- `internal/etl/stream_pipeline.go`
- `internal/etl/separate_merge_test.go`
- `internal/etl/real_staged_pipeline_test.go`
- `internal/api/process_progress.go`
- `internal/api/handlers.go`
- `frontend/src/types.ts`
- `frontend/src/App.tsx`
- `frontend/src/features/clean/CleanPanel.tsx`
- `frontend/src/features/clean/clean.css`
- `run.ps1`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

### 数据与存储

- 无持久数据库结构变化。
- 临时 SQLite 继续负责最终清洗去重。
- 新增任务级 `artifacts.json` 阶段产物清单。
- 阶段文件默认永久保留，不自动清理。

### 测试与真实结果

- 单元测试、API 测试、parser 测试、前端生产构建、vet 和后端构建均通过。
- 真实三来源 36 个文件、约 1.44 GB；36 个源文件副本 SHA-256 全部一致。
- 检测到 7 个内容完全相同文件，均保留源副本，分类合并跳过重复内容。
- 全量 `3,878,158 -> 2,585,142`，重复流水删除 `276,040`。
- 生成 3 个分类原字段 CSV、3 个分类统一字段 CSV、最终 920,951,132 字节 CSV、约 363 MB Excel及36个源文件副本，共44个产物。
- windows/amd64 并行全量耗时 212.21 秒，产生 8,498 次进度事件。
- windows/386 在 Excel 压缩阶段复现地址空间 OOM；生产启动脚本已固定 amd64。
- 真实结果：`backend/data/outputs/real_staged_validation_20260728/`。
- `.\run.ps1` 已以 windows/amd64 重建并启动 PID `31952`，`/api/health` 返回 `status=ok`。
- 真实微信 API smoke 输出 13,269 行和 5 个产物，六阶段进度均为 `done/100%`；原字段 CSV 下载大小与清单一致。

### 未完成事项

- 服务重启后不恢复历史进度状态；阶段文件和清单不受影响。
- 阶段文件会增加磁盘占用，后续如需自动归档/清理应增加明确保留策略。

## 2026-07-29 — 资金分析字段映射第一阶段

### 新增与调整

- 用户最终确认所有统一分析输出只保留原 33 个字段；分类统一 CSV、最终 CSV、Excel、API 预览和数据库导入源均固定为 33 列。
- 17 个来源/角色字段改为内部临时审计字段，不进入用户分析表，任务结束后删除内部阶段文件。
- 微信、支付宝支付汇总按收付方向确定本方和对手方，不再固定使用付款方。
- 支付宝转账按调查主体账号判定方向；无法唯一判定时不进入资金分析结果并写入审计。
- 新增清洗页“调查主体账号”，`POST /api/process` 新增可选 `subject_accounts`。
- 微信“对手方接收金额(分)”不再占用 `对手交易余额`；原值保留在源文件和分类原字段 CSV，不扩展统一表。
- 来源文件 SHA-256、Sheet、原始行号、映射版本、来源记录 ID 以及原始付款/收款方字段仅供内部去重和审计。
- 去重改为“流水号完整指纹 / 来源记录 ID / 完整业务指纹”三级策略，减少同秒同金额合法交易误删。
- 临时 SQLite 新增重复和未纳入记录审计，任务固定输出三类审计 CSV。
- 非“进/出”方向统一排除出分析结果并写入未纳入审计。
- 文件哈希合并到源文件复制过程，账户主体索引采用流式读取，避免额外大文件 I/O 和全量内存加载。
- 一键数据库导入继续使用 33 个业务字段；目标表仍为 37 列（`id` + 33 数据列 + 3 导入审计列）。

### 修改文件

- `internal/parser/provenance.go`
- `internal/parser/party_mapping.go`
- `internal/parser/alipay.go`
- `internal/parser/alipay_stream.go`
- `internal/parser/wechat.go`
- `internal/parser/funds_mapping_test.go`
- `internal/etl/subject_index.go`
- `internal/etl/subject_index_test.go`
- `internal/etl/cleaning.go`
- `internal/etl/dedup_audit_test.go`
- `internal/etl/etl.go`
- `internal/etl/staged_pipeline.go`
- `internal/etl/stream_pipeline.go`
- `internal/etl/separate_merge_test.go`
- `internal/provider/bank.go`
- `internal/rules/bank_rules.go`
- `internal/api/handlers.go`
- `frontend/src/App.tsx`
- `frontend/src/features/clean/CleanPanel.tsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

### 接口与结构

- `POST /api/process`：新增可选 multipart 参数 `subject_accounts`。
- `PipelineOptions`：新增 `SubjectAccounts`、`SubjectIdentifiers`、`SourceHashes`。
- 对外统一输出无新增字段；内部临时存储使用来源/角色字段支持去重和审计。
- 新增临时 SQLite `duplicates`、`rejected` 表；无持久业务数据库迁移。

### 验证

- `go test ./internal/... -count=1`：通过。
- `go vet ./...`：通过。
- 后端 windows/amd64 构建：通过。
- 前端生产构建：通过，保留既有大 chunk warning。
- `.\run.ps1`：最终后端 PID `30884`。
- 真实三来源 API 任务 `phase1-33cols-real-20260729`：
  - `53,355 -> 52,793`；
  - 微信 `2,413`、银行 `41,645`、支付宝 `8,735`；
  - 接口、三类分类统一 CSV、最终 CSV 和最终 Excel全部为原 33 列；
  - 方向仅“进/出”；
  - 微信 2,413 行未将对手方接收金额写入对手交易余额；
  - 重复记录审计 41 行；
  - 未纳入审计 521 行，其中缺少方向 510 行、原方向“其它”11 行。

### 未完成与注意事项

- 本次真实文件未包含支付宝独立转账明细和支付流水汇总，这两类由新增单元测试覆盖。
- 本次未重新执行 PostgreSQL/MySQL 写入；数据库导入已恢复使用 33 字段 CSV 和既有 37 列目标结构。
- 未提供主体账号或账户文件的转账数据将进入未纳入审计，避免方向误判。

## 2026-07-29 — 支付宝四类调证表识别与余额支出修正

### 修正内容

- 当前支付宝识别范围收紧为四类真实调证表：账户明细、余额明细、登陆日志、注册信息。
- 删除支付宝个人账单、转账明细、支付流水汇总、交易记录模板及转换分支。
- 账户明细直接采用原始 `收/支`；余额明细根据收入列和支出列判断进出。
- 登陆日志和注册信息只留存源文件，不进入统一流水。
- 删除无效的“调查主体账号”前端输入、`subject_accounts` API 解析、主体索引及支付宝付款/收款方推断。
- 补齐真实账户明细和注册信息表头。
- 修复余额明细支出值为负数时未识别的问题：支出非零即为“出”，交易金额取绝对值。
- 统一输出继续保持原 33 列。

### 修改文件

- `internal/parser/alipay.go`
- `internal/parser/alipay_stream.go`
- `internal/parser/alipay_stream_test.go`
- `internal/parser/funds_mapping_test.go`
- `internal/parser/party_mapping.go`
- `internal/parser/provenance.go`
- `internal/etl/etl.go`
- `internal/etl/stream_pipeline.go`
- 删除 `internal/etl/subject_index.go`
- 删除 `internal/etl/subject_index_test.go`
- `internal/api/handlers.go`
- `frontend/src/App.tsx`
- `frontend/src/features/clean/CleanPanel.tsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

### 验证

- 新增测试固定支付宝仅识别四类调证表。
- 新增测试固定账户明细 `收入/支出 -> 进/出`。
- 新增测试固定余额明细 `-1200.0 -> 出 / 1200.00`。
- 真实四表任务 `alipay-four-tables-fixed-20260729`：
  - 交易候选 `13,573` 条，最终 `13,561` 条；
  - 账户明细 `8,735` 条、余额明细 `4,826` 条；
  - 登陆日志和注册信息进入最终流水均为 `0`；
  - 方向：进 `4,483`、出 `9,078`；
  - 恢复旧逻辑漏掉的余额明细负数支出 `2,606` 条；
  - 未纳入 `12` 条，其中无有效收支金额 `1` 条、原始方向“其它” `11` 条；
  - 分类统一 CSV 和最终 CSV 均为原 33 列。

## 2026-07-29 — 支付宝余额明细默认不纳入统一流水

### 新增与调整

- 保留余额明细转33字段流水能力，但默认关闭。
- 清洗页新增“支付宝余额明细纳入统一流水”，默认不勾选。
- 新增 multipart 参数 `include_alipay_balance`；只有 `true` 或 `1` 才启用。
- 未启用时余额明细仍保留源文件和支付宝原字段 CSV，但不进入分类统一 CSV和最终结果。
- `PipelineOptions`、parser `MappingOptions` 新增零值为关闭的 `IncludeAlipayBalance`。
- 处理结果 summary 增加 `include_alipay_balance`。
- 新增自动化测试覆盖默认关闭和显式启用。

### 修改文件

- `internal/parser/provenance.go`
- `internal/parser/alipay.go`
- `internal/parser/alipay_stream.go`
- `internal/parser/alipay_stream_test.go`
- `internal/etl/etl.go`
- `internal/etl/staged_pipeline.go`
- `internal/etl/stream_pipeline.go`
- `internal/api/handlers.go`
- `frontend/src/App.tsx`
- `frontend/src/features/clean/CleanPanel.tsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

### 真实验证

- 默认关闭 `alipay-balance-default-off-20260729`：
  - 原字段 CSV `13,573` 行；
  - 最终 `8,735` 行，余额明细 `0` 行；
  - 账户明细出 `6,473`、进 `2,262`。
- 显式启用 `alipay-balance-explicit-on-20260729`：
  - 最终 `13,561` 行；
  - 账户明细 `8,735`、余额明细 `4,826`；
  - 出 `9,078`、进 `4,483`。
- 两种结果均为原33列。

## 2026-07-29 — 拆分支付宝账户明细用户信息

### 修改

- 将 `用户信息` 的 `支付宝账号(姓名)` 拆分为账号和姓名。
- 账号同时映射到 `交易账号`、`交易卡号`。
- 姓名映射到 `交易户名`。
- `交易方开户行`固定写入 `支付宝`。
- 兼容半角/全角括号；无括号时保留整段账号。
- 本次不修改其他支付宝映射，输出继续保持原33列。

### 修改文件

- `internal/parser/alipay.go`
- `internal/parser/alipay_stream.go`
- `internal/parser/alipay_stream_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

### 验证

- 真实用户信息 `8,746/8,746` 行符合账号加姓名格式。
- 真实任务 `alipay-user-info-mapping-20260729` 最终输出 `8,735` 行。
- 交易账号空值、账号卡号不一致、交易户名空值、开户行非支付宝均为 `0`。
- 最终结果为原33列。

## 2026-07-29 — 拆分支付宝账户明细交易对方信息

### 新增映射

- `支付宝账号(姓名)` -> 交易对手账卡号、对手户名。
- `(银行名称)银行卡号` -> 对手开户银行、交易对手账卡号。
- `姓名(银行名称)(银行卡号)` -> 对手户名、对手开户银行、交易对手账卡号。
- 三段格式第二段不含银行/信用社/农信特征时，不写入对手开户银行。
- 未识别格式完整保留在对手户名，不做推测拆分。
- 支持半角/全角括号和支付宝账号型嵌套姓名。

### 修改文件

- `internal/parser/alipay.go`
- `internal/parser/alipay_stream.go`
- `internal/parser/alipay_stream_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

### 验证

- 真实任务 `alipay-counterparty-mapping-v3-20260729` 输出 `8,735` 行、33列。
- 拆出对手账号 `8,236` 行，其中有对手姓名 `8,235` 行。
- 真实三段格式 `462` 行：银行型 `68` 行正确映射开户银行，第二段为人名的 `394` 行未误填银行。
- 未配置格式 `499` 行保持原值。
- `(银行名称)银行卡号` 在当前真实文件中没有完全匹配样本，由精确单元测试验证。

## 2026-07-28 — 新增清洗结果一键导入 PostgreSQL/MySQL

### 新增与调整

- 清洗结果区新增“一键导入数据库”，只对统一字段合并结果开放。
- 数据库配置支持新增、编辑、测试并加密保存，可选择 PostgreSQL/MySQL、数据库、Schema 和目标表。
- 支持追加或清空重建、英文 snake_case 或中文字段、跳过重复或允许重复。
- 新增异步数据库写入任务、取消操作和处理行数/写入行数/跳过行数/速度/耗时/ETA 进度。
- PostgreSQL 使用 COPY 暂存与事务合并；MySQL 使用 500 行批量 INSERT。
- 自动创建 33 个流水字段以及 `id`、`source_job_id`、`source_row_hash`、`imported_at` 审计字段。

### 接口

- 新增 `POST /api/db/export/tasks`。
- 新增 `GET /api/db/export/tasks/:id`。
- 新增 `POST /api/db/export/tasks/:id/cancel`。

### 修改文件

- `internal/dbimport/export.go`
- `internal/dbimport/export_test.go`
- `internal/api/db_export_handlers.go`
- `internal/api/db_handlers.go`
- `internal/api/handlers.go`
- `frontend/src/features/flow/dbImportApi.ts`
- `frontend/src/features/clean/DBExportModal.tsx`
- `frontend/src/features/clean/CleanPanel.tsx`
- `frontend/src/features/clean/clean.css`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

### 验证

- 后端全包测试、`go vet ./...` 和前端生产构建通过。
- PostgreSQL 真实连接测试通过。
- 真实清洗结果 `13,269` 行全部写入，目标表行数和唯一指纹数均为 `13,269`，字段总数为 `37`。
- 同批数据第二次 append+skip 写入 `0` 行、跳过 `13,269` 行。
- 验证结束后已删除专用测试表，未保留测试数据。
- 当前无可用 MySQL 实例；MySQL 完成建表/批量写入单元测试及编译验证，真实实例测试待有连接配置后执行。

## 2026-07-29 — 统一表新增商户流水号

### 新增与调整

- 统一表由33个业务字段调整为34个，在`交易流水号`后新增`商户流水号`。
- 支付宝账户明细改为`交易号 -> 交易流水号`、`商户订单号 -> 商户流水号`，取消两个编号之间的兜底混用。
- `null`、`NULL`、`<nil>`形式的支付宝商户订单号清洗为空值。
- 通用字段别名拆分为两个目标字段，去重完整业务指纹加入`商户流水号`。
- 数据库snake_case映射新增`merchant_serial_no`。
- PostgreSQL/MySQL追加导入已有旧表前会检查字段并自动补充缺失业务字段；新表共38列。
- 清洗界面的余额明细提示改为“统一字段”，不再写死33字段。
- API端点及请求契约无变化。

### 修改文件

- `internal/etl/etl.go`
- `internal/etl/cleaning.go`
- `internal/etl/etl_test.go`
- `internal/etl/separate_merge_test.go`
- `internal/parser/alipay.go`
- `internal/parser/alipay_stream.go`
- `internal/parser/alipay_stream_test.go`
- `internal/parser/provenance.go`
- `internal/dbimport/export.go`
- `internal/dbimport/export_test.go`
- `frontend/src/features/clean/CleanPanel.tsx`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

### 验证

- 后端全包测试、`go vet ./...`、后端构建和前端生产构建通过。
- 真实任务`alipay-merchant-serial-mapping-20260729`输出8,735行、34列。
- 字段顺序为第26列`交易流水号`、第27列`商户流水号`。
- `交易流水号`非空8,735行；`商户流水号`非空6,983行；商户流水号中的`null/<nil>`为0行。
- 首行两个编号分别为`2026031422001472211429419304`和`685_202603149505297201771957`。
- 本轮未执行真实数据库写入；新字段DDL及旧表自动补字段逻辑已通过测试、编译和静态检查。
- 修改后已执行`.\run.ps1`并通过健康检查。

### 注意事项

- 当前34列契约覆盖此前仅保留33列的历史决定。

## 2026-07-29 — 支付宝消费名称直接映射摘要说明

### 调整

- 支付宝账户明细`消费名称`直接映射`摘要说明`。
- 删除`类型`对摘要说明的兜底，消费名称为空时摘要保持为空。
- `null/NULL/<nil>`消费名称按空值处理。
- 统一表仍为34列，其他映射不变。

### 修改文件

- `internal/parser/alipay.go`
- `internal/parser/alipay_stream.go`
- `internal/parser/alipay_stream_test.go`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

### 验证

- 后端全包测试与`go vet ./...`通过。
- 真实任务`alipay-consumption-summary-mapping-20260729`输出8,735行、34列。
- 逐笔按交易流水号对照源消费名称8,735行，不一致0行。
- 首行`消费名称`和`摘要说明`均为`明智出行费用`，源`类型=即时到账交易`未进入摘要。
- 服务已重启，PID 8524，健康检查正常。

## 2026-07-29 — 新增 GitHub README

### 新增

- 新增根目录`README.md`，以产品级中文文案介绍资金数据智能分析平台。
- 覆盖多源接入、分阶段合并、标准化清洗、严格去重、34字段统一模型、资金流分析、PostgreSQL/MySQL导入和可审计性设计。
- 新增GitHub可渲染的Mermaid处理流程图、技术架构表、目录结构、快速开始、配置与测试说明。
- 补充运行时敏感数据防误提交提示以及正式调查/审计使用边界。
- 使用真实Git远程地址作为克隆命令，未添加未经验证的CI、覆盖率或性能宣传。

### 修改文件

- `README.md`
- `docs/AI_HANDOFF.md`
- `docs/CHANGELOG_AI.md`

### 验证

- Markdown标题结构、代码围栏配对、核心字段和关键章节检查通过。
- 本次仅修改文档，无需重启服务。

## 2026-07-30 — EVM RPC 富化与高可用管理 V1.4

### 新增与优化

- 新增`internal/rpcmanager`，实现DPAPI+AES-GCM Endpoint加密、SQLite控制库、脱敏CRUD、保存前链校验。
- 实现多节点优先级路由、RPS/并发限制、AIMD自适应限速、幂等读取重试、429/超时切换、熔断、健康检查、区块落后和请求指标。
- 实现地址类型/原生币余额与Token Metadata缓存，以及可取消批量富化任务。
- 新增`/api/crypto/rpc/*`和`/api/crypto/enrichment/*`接口。
- Parquet预检、SQD Token Metadata、地址画像和地址资产查询接入受管RPC；原环境变量RPC继续兼容。
- 前端“虚拟币”下新增“RPC节点管理”，提供六项指标、节点管理、按链路由、富化任务和进度条；桌面与移动端布局完成。
- 地址分析增加实时当前原生币余额，地址类型和Token Metadata可由受管RPC按需补齐。

### 数据结构

- 新增`rpc_endpoints`、`rpc_endpoint_health`、`rpc_request_metrics`、`enrichment_jobs`和`rpc_enrichment_cache`。
- 数据与密钥目录固定为`E:\codex\bsc_analytics\config`，拒绝C盘控制数据目录。

### 验证

- RPC管理器7项自动测试通过：重启解密、无明文泄露、Chain ID错配、429故障转移、超时故障转移、坏密钥、缓存和C盘拒绝。
- `go test ./internal/...`、`go vet ./...`、后端构建、前端生产构建通过。
- 运行服务本机模拟节点真实API闭环通过：创建、脱敏、测试、地址类型、原生币余额、缓存命中、删除。
- Playwright/Edge桌面与移动端2项通过，无页面横向溢出和控制台错误。
- 已执行`.\run.ps1`，PID 16092，健康检查正常。

### 注意事项

- 未使用或保存真实供应商密钥；需由用户在新页面录入。
- 受管RPC当前覆盖地址类型/原生币余额、Token Metadata；Token `balanceOf`与Receipt批量仍保留既有环境变量RPC路径。

## 2026-07-30 — 数据源管理中心 V1.0

### 新增与优化

- 新增`internal/datasourcemanager`统一聚合SQD、AWS和受管RPC，提供Provider列表、健康、指标、连接测试和配置生命周期。
- SQD/AWS配置支持添加、修改、删除、启停和保存前连接校验。
- API Key复用DPAPI+AES-GCM机器绑定加密，列表、配置读取、事件和错误不回显密钥或完整敏感URL。
- 新增60秒健康检查、P50/P95、成功率、请求/失败/429/超时计数和最近健康事件。
- 有效SQD Portal/API Key与AWS Endpoint回注Parquet管理器，AWS发现结果使用配置Endpoint完成下载。
- 前端新增“虚拟币 → 数据源管理”，使用响应式数据源卡片、五项概览、过滤/搜索、连接测试进度、配置弹窗和日志抽屉。
- RPC卡片直接复用V1.4节点状态，配置操作进入RPC节点管理，未建立重复密钥或路由系统。

### 接口与存储

- 新增`/api/crypto/datasource/list|health|metrics|config|test|save|delete`。
- 新增`E:\codex\bsc_analytics\config\datasources.json`，原子保存配置及最近100条健康事件。
- 未新增数据库表；RPC请求指标仍来自`rpc_control.sqlite`。

### 验证

- `go test ./...`、`go vet ./...`、后端构建和前端生产构建通过。
- 自动测试覆盖配置新增/修改/删除、密钥密文、连接测试、HTTPS限制和C盘拒绝。
- 真实SQD连接返回`binance-mainnet`，340ms；AWS公共目录连接成功，299ms。
- Playwright/Edge桌面、CRUD和390px移动端3项通过，无页面横向溢出及控制台错误。
- 已执行`.\run.ps1`，PID 18408，健康检查正常。

### 注意事项

- 未配置付费RPC时页面只显示SQD与AWS；新增RPC节点后会自动聚合显示。
- AWS下载速度和持久化健康趋势为后续扩展，V1.0不显示伪造曲线。
## 2026-07-30 — 精简 RPC 页面大型提示

- 移除 RPC 节点管理页顶部 Endpoint 安全说明 Alert。
- 移除节点配置弹窗中的 API Key 说明 Alert，并清理无用样式与组件导入。
- 本次不变更后端接口、加密存储或连接校验逻辑。
## 2026-07-30 — 修复数据源卡片操作区与长日志溢出

- 修复数据源卡片日志、配置、测试按钮越出卡片并与相邻卡片重叠的问题。
- 优化卡片自适应列宽和底部操作布局。
- 健康日志增加三行截断、任意长字符串换行、全文 Tooltip 和移动端抽屉宽度约束。
## 2026-07-30 — RPC 支持独立测试 Endpoint

- RPC配置新增可选加密测试Endpoint，手动连接测试优先使用测试地址，未配置则回退正常地址。
- 正常Endpoint继续独占自动路由、正式RPC调用和定时健康检查，避免测试地址进入生产流量。
- `rpc_endpoints`新增`test_endpoint_encrypted`并自动迁移现有SQLite控制库。
- 列表只返回测试地址配置状态与脱敏值；测试结果新增`endpoint_role`。
- 前端节点表单新增独立测试Endpoint开关、编辑保留/清除语义及连接结果来源提示。
- 移动端节点弹窗增加视口高度约束和内部滚动，参数区使用两列响应式布局。
- 自动测试覆盖测试地址隔离和正常地址回退。
- 全包测试、`go vet`、前后端构建通过；运行服务双路径RPC验证分别返回`TEST`和`PRIMARY`，临时配置已删除。
- Playwright/Edge桌面与390px移动端通过；已重启服务，PID 34392。
## 2026-07-30 — RPC 检测频率调整为30分钟

- 定时健康检查从30秒降为30分钟。
- 正式请求增加30分钟过期判断：首次使用或健康状态过期时先测试正常Endpoint。
- 健康状态未过期时不重复测试；成功业务请求会刷新有效期。
- 增加单节点预检锁，避免并发首次使用产生重复测试流量。
- RPC管理器10项、全包测试、`go vet`和后端构建通过。
- 运行服务验证首次正式使用前只对正常Endpoint进行一组预检，独立测试Endpoint计数不变；临时配置已清理。
- 已重启服务，PID 808。

## 2026-07-30 — 真实 BSC 地址功能验证

- 使用 `0xD26889f63094Ba5A9d32666CdF5Ba381acfad6A6` 完成真实 RPC 地址分类、原生币余额、Token Metadata、缓存命中、429 节点故障切换及 ETH 对照验证。
- BSC 结果：`CONTRACT / DETECTED`、BNB 余额 0；Token 为 `Finanx AI (FNXAI)`、18 位精度；ETH 结果为 `EOA`。
- SQD 任务 `3b751dae9980c229` 完成，trace 共 30,249 行和 8,294 个不同交易哈希；核验交易 `0xb93fc5d1585c08bf1e9f90e9c256de082002f211221ff74a24f915326fab5993` 在区块 86,297,561 命中 3 条调用轨迹。
- Playwright/Edge 桌面与 390px 移动端地址分析页通过，无横向溢出、控制台错误或外部浏览器链接异常。
- AWS transactions 文件约 5.96GB，受控下载任务 `4a33337970cfce97` 因预计耗时较长已安全取消；并行下载产生的不兼容临时片段已删除。
- 发现缺陷：完成任务的 manifest 仍为 `running/85%`；SQD 设置 `export_csv=true` 未生成 CSV。
- 验证边界：本次未完成 5.96GB transactions 层下载，流水/Token/NFT/对手方空结果不能视为完整历史结论。
- 本次未修改代码、接口或数据库结构；最终健康检查正常，无需重启。
- 新增`docs/BSC真实地址完整功能测试报告_2026-07-30.md`，详细记录测试过程、真实接口返回、任务阶段、输出校验和未完成边界。

## 2026-07-30 — V1.4.1 任务与下载稳定性修复

- 新增统一Task Finalizer，完成/失败/取消均执行Stage收敛、输出检查、SHA-256、Job持久化和最终Manifest原子提交。
- Manifest升级为Schema 1.4.1，API与Manifest统一包含覆盖率、Checksum、结束时间、Manifest状态及任务事件。
- 新增Windows原子替换Writer，避免删除旧Manifest后再rename产生空窗。
- 取消流程调整为`canceling → canceled`，终态不允许残留Running/Queued Stage；服务重启可从Paused及Chunk/`.partial`检查点恢复。
- 新增统一SQD CSV导出，并自动为历史`export_csv=true`完成任务补生成CSV。
- 真实旧SQD任务补生成9个CSV，`traces.csv`为12,581,974字节；最终19个输出、18个SHA256，API/Manifest均为`done/100%`。
- 新增32MiB Range Chunk并行下载、ETag校验、原子检查点、分片复用、合并和SHA256；Range不支持及旧`.partial`自动回退兼容。
- 新增数据覆盖模型，地址页在transactions/logs/trace不完整时显示Coverage和“零值不代表完整历史无交易”提示。
- Parquet任务页新增覆盖率、数据源状态、Manifest一致性、Schema、Checksum及Chunk恢复信息。
- 修复SQD任务`files=null`导致Parquet页面空白。
- 自动测试覆盖5GiB/10GiB Chunk规划、Range恢复、取消收敛、Manifest一致性和SQD CSV；全包测试、`go vet`、前后端构建通过。
- 真实SQD新任务两次受供应商503影响未完成；失败状态和Manifest收敛正确，未继续重试。
- Playwright/Edge桌面与390px移动端通过，无溢出、控制台错误、页面异常或请求失败。
- 已执行`.\run.ps1`，PID 21088，健康检查正常。
### 2026-08-01 11:10 Parquet 分片下载卡 0 进度排查与恢复

#### 现象

- 任务 `e170c084fba68a70`（BSC 2026-07-30 transactions，6.1GiB AWS 公共 Parquet）进入“分片下载”后 `downloaded_bytes=0` 持续 12 分钟，最终 3 次重试耗尽后失败；取消/重试/服务重启后均复现。

#### 诊断证据

- 服务器进程能 TCP 连上 S3（多 IP：52.219.93.202 / 3.5.x），但 0 字节落盘，连接每 1-2 分钟轮换；同一 URL/Headers 用 curl 与独立 Go 程序（386 与 amd64）均秒级 206 成功。
- 本地代理隧道抓包：etl-server 的 TLS 数据双向流通（客户端约 1.9KB，S3 回传 8-18KB 握手/证书），随后连接被“本机软件中止”（WSAECONNABORTED）强制重置，即使经 127.0.0.1 代理也重置；`portal.sqd.dev` 隧道不受影响。
- 本机存在火绒 NDIS/WFP 驱动（`hrndis6`/`hrwfpdrv`/`sysdiag`）与 Anycast VPN（`Anycast.exe`/`anycast-service`），判定为其中之一对 etl-server 到 AWS S3 的 TLS 连接做联网控制/分应用拦截。

#### 处理

- 取消 3 个卡住/失败任务（`e170c084fba68a70` 失败、`bf293884c2dadb52`/`963642ed11148e6b` 取消），均 0 字节无损失。
- 使用复制二进制 + 代理隧道做 A/B 验证后恢复原服务：`.\run.ps1 -SkipBuild`，PID 29716，`/api/health` 正常。

#### 未完成

- 未修改代码；需在火绒“联网控制/网络防护”或 Anycast VPN 分应用规则中放行 `etl-server.exe` 后重试下载。

### 2026-08-01 V2.1 RC2 链数据采集"结果与清单"弹窗化
