# AGENTS.md

# AI 必须执行规则

## 每次任务开始前

必须先阅读以下文件：

1. AGENTS.md
2. docs/AI_HANDOFF.md
3. docs/CHANGELOG_AI.md
4. docs/CLAUDE.md

未阅读前，禁止修改代码。

## 每次任务结束前

必须更新以下文件：

1. docs/AI_HANDOFF.md
2. docs/CHANGELOG_AI.md

更新内容包括：

- 本次新增功能
- 修改了哪些文件
- 新增/变更的接口
- 新增/变更的数据库结构
- 新增/变更的前端页面或组件
- 已验证的命令
- 未完成事项
- 注意事项

## 禁止行为

- 禁止只改代码不更新文档
- 禁止重复扫描整个项目
- 禁止忘记记录新增功能
- 禁止删除历史记录
- 禁止重构无关模块


## 1. 项目概述

**资金数据智能分析平台** — 一款面向金融调查场景的 ETL 工具，用于上传、清洗、合并多来源资金流水（银行/支付宝/微信），将异构数据统一为标准化格式，生成可交互的资金流向图，支持多维筛选、人工标注、路径分析和多格式导出。

项目原始为 Python 实现，已于 2026 年 5 月完整重构为 Go 后端 + React 前端架构。当前代码全部在 `E:\codex\etl`，与原始 Python 项目独立部署。

## 2. 当前技术栈

### 后端
| 组件 | 技术 |
|------|------|
| 语言/版本 | Go 1.25.0 |
| HTTP 框架 | Gin v1.12.0 |
| 日志 | zerolog v1.35.1 |
| Excel 引擎 | excelize v2.10.1 |
| UUID | google/uuid v1.6.0 |
| CORS | gin-contrib/cors v1.7.7 |
| 测试 | Go 内置 testing 包 |

### 前端
| 组件 | 技术 |
|------|------|
| 语言 | TypeScript 6.0.3 |
| 框架 | React 19.2.6 |
| 构建 | Vite 8.0.13 |
| UI 库 | Ant Design 5.29.3 |
| 流程图 | @xyflow/react 12.10.2 |
| 导出 | html-to-image 1.11, JSZip 3.10 |
| API 客户端 | 原生 fetch (无 Axios) |

### 基础设施
- **存储**: 纯文件系统 (无数据库依赖)
- **部署**: 单二进制 + 前端 dist 静态托管
- **启动**: PowerShell 脚本 `run.ps1` (自动构建 + 启服)
- **端口**: 后端 8000, 前端开发服务器 5173 (通过 Vite proxy 到 8000)

## 3. 项目目录结构

```
E:\codex\etl\
├── cmd/
│   └── server/
│       └── main.go                 # 服务入口 (配置加载 → 日志 → API → 启服)
├── internal/
│   ├── api/
│   │   ├── handlers.go             # 18 个 API 处理器 (1023 行)
│   │   └── router.go               # Gin 路由注册 + CORS + SPA 静态文件
│   ├── config/
│   │   └── config.go               # 配置结构、环境变量、自动检测根目录
│   ├── etl/
│   │   ├── etl.go                  # ETL 核心管道 (664 行)
│   │   ├── flow_graph.go           # 资金流向图构建 (222 行)
│   │   ├── etl_test.go             # ETL 单元测试 (8 用例)
│   │   └── benchmark_test.go       # 基准性能测试 (5 场景)
│   ├── logger/
│   │   └── logger.go               # 结构化日志 (zerolog, 控制台+文件, 滚动)
│   ├── model/
│   │   ├── model.go                # 核心数据模型 (FlowNode, FlowEdge, FlowGraph, etc.)
│   │   └── model_test.go           # 模型测试 (3 用例)
│   ├── parser/
│   │   ├── parser.go               # 通用解析器 (CSV/Excel 读取, 列标准化等)
│   │   ├── alipay.go               # 支付宝专用解析器 (6 种标准表)
│   │   ├── wechat.go               # 微信专用解析器 (5 种标准表)
│   │   └── parser_test.go          # 解析器测试 (9 用例)
│   ├── provider/
│   │   ├── provider.go             # Provider 接口 + 工厂函数
│   │   └── bank.go                 # 银行流水专用处理 (309 行)
│   ├── rules/
│   │   ├── bank_rules.go           # 银行规则常量 + 表识别 (630 行)
│   │   ├── custom_rules.go         # 自定义规则 JSON 读写
│   │   └── rules_test.go           # 规则测试 (5 用例)
│   ├── scanner/
│   │   └── scanner.go              # 文件扫描器 (识别交易/账户/标签文件)
│   └── storage/
│       └── storage.go              # 文件存储层 (会话管理, 上传/下载)
├── frontend/
│   ├── index.html                  # SPA 入口
│   ├── package.json                # 前端依赖
│   ├── vite.config.ts              # Vite 配置 (proxy /api → 127.0.0.1:8000)
│   ├── tsconfig.json               # TypeScript 严格模式
│   └── src/
│       ├── main.tsx                # React 入口
│       ├── App.tsx                 # 应用根组件 (474 行, 核心编排)
│       ├── types.ts                # TypeScript 类型定义 (ProcessResponse, FlowGraph 等)
│       ├── api/
│       │   └── client.ts           # HTTP 客户端 (getJson, postJson, postForm)
│       ├── hooks/
│       │   ├── useFlowOperations.ts   # 核心状态管理 Hooks (4212 行, 最大文件)
│       │   └── useFlowModals.ts       # 模态框状态管理
│       ├── features/
│       │   ├── upload/                # 上传面板 (TransferPanel, uploadApi)
│       │   ├── clean/                 # 数据清洗面板 (CleanPanel, RuleExpansionDrawer)
│       │   └── flow/                  # 资金流图模块 (20+ 文件, 核心功能)
│       │       ├── FlowPanel.tsx         # 流图主面板 (512 行)
│       │       ├── FlowCanvas.tsx        # 流图画布
│       │       ├── FlowGraphPrimitives.tsx  # 自定义节点/边渲染
│       │       ├── FlowInspectorPanel.tsx   # 数据检视面板
│       │       ├── FlowGraphWorkspace.tsx   # 图层管理 + 图层面板
│       │       ├── FlowAnalysisPanel.tsx    # 路径分析 + 洞察
│       │       ├── FlowBuildControls.tsx    # 流图构建控制
│       │       ├── SubjectDetailDrawer.tsx  # 主体详情抽屉
│       │       ├── EdgeDetailModal.tsx      # 边详情弹窗
│       │       ├── FlowMappingModal.tsx     # 列映射模态框
│       │       ├── FlowSourceModal.tsx      # 数据源选择
│       │       ├── FlowFieldFilters.tsx     # 源/目标过滤器
│       │       ├── FlowGraphFilters.tsx     # 图过滤器
│       │       ├── FlowLabelFilters.tsx     # 标签过滤器
│       │       ├── FlowLayerPanel.tsx       # 图层面板
│       │       ├── FlowStyleToolbar.tsx     # 样式工具栏
│       │       ├── EdgeStylePanel.tsx       # 边样式面板
│       │       ├── FlowAddNodeModal.tsx     # 手动添加节点
│       │       ├── FlowDirectionRuleModal.tsx  # 方向规则配置
│       │       ├── FlowImportSummary.tsx    # 导入摘要
│       │       ├── flowTypes.ts            # 类型 + 常量 (320 行)
│       │       ├── flowApi.ts              # API 调用封装
│       │       ├── flowExport.ts           # 导出引擎 (8 种格式)
│       │       ├── useFlowGraph.ts         # 图计算 Hook (402 行)
│       │       ├── useFlowFilters.ts       # 过滤器管理 (901 行)
│       │       ├── useFlowPanelState.ts    # 面板状态管理 (464 行)
│       │       ├── flowAggregation.ts      # 时间聚合
│       │       ├── flowAnalysis.ts         # 路径分析 + 洞察计算
│       │       ├── flowEdges.ts            # 边计算 (样式/标签/互计算)
│       │       ├── flowElements.ts         # ReactFlow 元素构建
│       │       ├── flowGeometry.ts         # 几何 + Handle 布局算法
│       │       ├── flowLayout.ts           # 自动布局 (Dagre)
│       │       ├── flowMapping.ts          # 列映射逻辑
│       │       ├── flowNodes.ts            # 节点渲染 (mask/color/icon)
│       │       ├── flowSubject.ts          # 主体统计
│       │       ├── flowManual.ts           # 手动节点/边管理
│       │       └── flowImportFiles.ts      # 导入文件处理
│       └── styles/                    # 全局样式 (layout, shared, responsive)
├── backend/
│   ├── config/
│   │   └── custom_rules.json         # 自定义规则持久化 (列签名映射 + 方向别名)
│   └── data/                         # 运行时数据目录 (uploads/, outputs/, logs/, rule_samples/)
├── go.mod
├── go.sum
├── run.ps1                           # 启动脚本 (自动 go build + 启服)
├── .gitignore
├── 修复.md                           # 本任务描述
└── 重构完成报告.md                    # Python→Go 迁移报告 (含性能/测试/已知问题)
```

## 4. 核心模块说明

### 4.1 Scanner (`internal/scanner/scanner.go`)
- 并发扫描上传目录 (4 worker goroutine 池)
- 自动识别文件类型：交易流水 (transactions)、账户信息 (accounts)、标签文件 (labels)、未知 (unknown)
- 基于关键词和列签名打分 (confidence 0-100)
- 返回 `DirectoryScan` 结果，按种类分类

### 4.2 ETL Pipeline (`internal/etl/etl.go`)
- **RunPipeline**: 主入口 → Scan → 按 provider 分组并发处理 → Clean → Dedup → Export
- **CleanTransactions**: 过滤必填字段缺失的行 (交易时间、交易金额、收付标志)，标准化方向/时间/金额
- **DeduplicateTransactions**: 基于 (时间 + 金额 + 方向 + 本方卡号 + 对手卡号) 去重
- **ExportToExcel/CSV**: 按 33 列标准表头导出，流式写入大文件
- 支持 3 种 provider 路由: 支付宝、微信、银行 (含 unknown fallback 通用处理)

### 4.3 Parser 包 (`internal/parser/`)
- **parser.go**: 通用工具 — NormalizeHeader, ToNumber, NormalizeDatetime, NormalizeDirection, ReadFile, ReadExcelFile, ReadCSVFile
- **alipay.go**: 支付宝专用 — 识别 6 种标准表 (交易记录、账户明细、转账明细、余额明细 等)，自动表头映射，4 worker 并发
- **wechat.go**: 微信专用 — 识别 5 种标准表 (交易明细信息、微信账单、微信账单明细、支付流水汇总 等)，金额单位自动检测 (分/元)

### 4.4 Flow Graph (`internal/etl/flow_graph.go`)
- 基于交易行构建流向图节点+边
- 聚合逻辑: `本方(出)→对手` 和 `对手(进)→本方`
- 自动检测 duplicate 边 (相同 source/target/amount/time)
- 截断默认 600 条边 (受前端性能限制)
- 生成 meta 信息: total_edges, total_nodes, truncated

### 4.5 Rules (`internal/rules/`)
- **bank_rules.go**: 银行流水预定义表结构 — 交易明细、账户信息、信用卡账单、招商交易流水、强制措施 等 10+ 类型
- **custom_rules.go**: 可持久化的自定义规则 — 列映射规则签名 (SHA1) + 方向别名，存储于 `backend/config/custom_rules.json`

### 4.6 前端 Flow 模块
- **FlowPanel**: 流图顶层编排组件，管理所有子面板的渲染和数据流
- **useFlowOperations**: 4212 行的核心 Hooks 文件，集中管理上传/导入/构建/导出/图层/样式/筛选/标注等所有操作
- **flowExport**: 8 种导出格式 — PNG, JPEG, WebP, SVG (canvas), Mermaid, DOT, GraphML, draw.io, XMind, CSV
- **flowLayout**: 使用 Dagre 算法自动布局
- **flowGeometry**: 优化的边连接点 (Handle) 布局算法，减少交叉
- **useFlowGraph**: 实时计算边样式 (颜色/宽度/线型)、标签、箭头

## 5. 已完成的重要功能

1. **多提供商流水自动识别** — 无需人工标注，自动识别支付宝/微信/银行流水表类型
2. **智能列映射** — 支持 33 列标准表头的多别名自动匹配，自定义映射规则持久化
3. **并发 ETL 管道** — 按提供商分组并行处理 (goroutine)，scanner 4 worker 并发扫描
4. **标准化清洗** — 自动处理 BOM、全角空格、金额符号 (￥/¥)、日期格式标准化、方向 (借/贷/D/C/收入/支出→进/出)
5. **去重** — 基于复合键去重
6. **资金流向图可视化** — 基于 ReactFlow 的交互式资金流向图，支持缩放/拖拽/框选
7. **多维筛选** — 按主体、对手、时间窗口、金额范围、方向、标签组合筛选
8. **最短路径分析** — Dijkstra 算法计算两个主体间的资金路径
9. **洞察分析** — 自动生成: 最大入/出边、环形交易检测、高密度节点、时间趋势
10. **图层管理** — 支持多图层叠加，每层独立数据源、筛选器和样式
11. **手动标注** — 添加虚拟节点/边，支持无数据场景的推理标注
12. **人工审核节点** — 修改节点标签、种类 (个人/群体/账户/公司/商户/未知)
13. **8 种图导出** — PNG/JPEG/WebP/SVG + Mermaid/DOT/GraphML/draw.io/XMind + CSV + 完整 ZIP
14. **自定义方向规则** — 方向别名持久化 (如 "借"→"出", "收入"→"进")
15. **列签名智能匹配** — 导入文件时自动匹配已保存的列映射规则
16. **数据预览** — 处理结果前端分页预览 (100 行)，支持质量报告查看
17. **历史会话** — 基于文件系统的上传会话持久化，支持加载历史数据
18. **Excel 流式导出** — 支持大数据集的增量写入导出

## 6. 当前架构决策

| 决策 | 详情 |
|------|------|
| 单进程架构 | Gin 同时服务 API 和前端静态文件 (SPA fallback) |
| 无数据库 | 全部数据以文件形式存储 (uploads/, outputs/, flow_sessions/) |
| 内存处理 | 大文件使用流式读取，但清理后数据全部保留在内存 |
| 去重策略 | 使用 map[string]bool 基于复合键内存去重 |
| 并发模型 | goroutine + sync.Mutex 保护共享状态，errChan 收集错误 |
| 截断限制 | FlowGraph 最多 600 条边 (前端渲染性能) |
| 路径前缀 | 所有 API 以 /api 开头，前端静态路由由 Vite proxy 透传 |
| 上传清理 | 每次新上传覆盖 `uploads/current/` 目录 |
| 规则持久化 | JSON 文件，SHA1 列签名作为唯一标识 |

## 7. 数据库与数据处理说明

- **无外部数据库** — 当前使用文件系统存储，`model.Storage` 接口预留了数据库接入能力
- **数据目录结构**:
  ```
  backend/data/
  ├── uploads/
  │   ├── current/          # 最近一次上传 (每次覆盖)
  │   └── flow_sessions/    # 导入会话 (按 session_id 分目录)
  ├── outputs/              # 导出文件 (etl_*.xlsx, etl_*.csv, funds_etl_*.xlsx)
  ├── logs/
  │   └── app.log           # 运行日志
  └── rule_samples/
      └── current/          # 规则分析样本
  ```
- **支持的文件格式**: .csv, .tsv, .txt, .xlsx, .xlsm, .xls
- **最大文件大小**: 500MB (可通过环境变量覆盖)
- **WebSocket 支持**: 当前版本不支持实时推送，前端使用轮询/一次性 HTTP

## 8. 前端开发规则

1. **状态管理**: 使用 React Hooks + useRef + useState，无 Redux/MobX
2. **API 调用**: 使用 `frontend/src/api/client.ts` 中的 `getJson` / `postJson` / `postForm`
3. **组件结构**: 功能模块放在 `features/` 下，每个 feature 独立目录，hooks 放 `hooks/`
4. **类型定义**: 全局类型在 `src/types.ts`；Flow 专用类型在 `features/flow/flowTypes.ts`
5. **UI 框架**: 严格使用 Ant Design 5 组件，不要引入其他 UI 库
6. **流程图**: 使用 @xyflow/react (ReactFlow v12)，自定义节点在 `FlowGraphPrimitives.tsx`
7. **代码风格**: 
   - 所有导入使用相对路径 (相对于当前文件)
   - 使用 TypeScript 严格模式 (strict: true)
   - 样式文件命名 `*.css` (非 module)，放在对应 feature 目录
8. **不引入新依赖**: 除非绝对必要且经充分评估
9. **useFlowOperations.ts 的修改**: 这是 4212 行的核心文件，修改前充分理解其结构 — 它管理所有上传/导入/构建/导出/图层/样式/筛选/标注的状态和回调

## 9. 后端开发规则

1. **模块结构**: 每个功能包放在 `internal/` 下，`cmd/` 仅放 main.go
2. **错误处理**: 使用 `fmt.Errorf` 包装，API 层统一返回 `gin.H{"detail": "..."}` 格式
3. **日志**: 使用 `logger.Log.Info()/Warn()/Error()` 结构化日志，携带关键字段
4. **配置**: 所有路径和参数通过 `config.Config` 统一管理，环境变量 `PORT`, `DEBUG`
5. **并发**: 使用 goroutine + sync.Mutex，错误通过 errChan 传递
6. **测试**: 使用 Go testing 包，保持当前 29 个测试全部通过
7. **API 契约**: 所有端点保持与 Python 原版一致的 JSON 格式路径
8. **Provider 模式**: `provider.Provider` 接口定义 `ProcessDirectory` / `ProcessFile`，新增数据源需实现此接口
9. **自定义规则**: 使用 `rules.LoadCustomRules()` / `rules.SaveCustomRule()` 读写，JSON 格式

## 10. 性能要求

| 指标 | 基准 (benchmark) | 备注 |
|------|-----------------|------|
| Clean 1000 行 | ~12ms | 单 goroutine |
| Dedup 10000 行 | ~2.2ms | 基于 map 查找 |
| BuildSummary 10000 行 | ~11ms | |
| BuildFlowGraph 1000 行 | ~1.7ms | 截断 600 边 |
| FullPipeline 1000 行 | ~13ms | 不含文件 I/O |
| 并发级别 | runtime.NumCPU() * 2 | Scanner + Parser + Pipeline |
| 最大上传 | 500MB | config.MaxFileSize |

- 流图渲染限制: 最多 600 条边，超出自动截断
- 大数据导出使用流式写入 (每 1000 行记录进度)
- 待优化: CleanTransactions 和 DeduplicateTransactions 可改为行级/分片并发

## 11. 禁止修改事项

1. **禁止引入数据库依赖** — 项目故意保持文件系统存储，除非有明确的用户需求
2. **禁止修改 API 端点路径** — 所有 /api/* 路径与 Python 原版保持一致，前端依赖这些路径
3. **禁止修改 FinalTransactionColumns (33 列标准表头)** — 这是所有提供商的统一输出格式
4. **禁止删除或重命名 `frontend/src/features/flow/flowTypes.ts` 中的常量** — 被整个前端 Flow 模块引用
5. **禁止修改 `useFlowOperations.ts` 的函数签名** — 被 App.tsx 直接调用
6. **禁止更改 `parser.NormalizeHeader` 的行为** — 影响列匹配和规则签名
7. **禁止移除 CORS 的 `*` 通配符** — 前端开发服务器跨域依赖
8. **禁止修改 go.mod 中的模块路径** (`github.com/etl/backend`) — import 引用依赖
9. **禁止直接修改 `backend/config/custom_rules.json`** — 应通过 API 或 `rules` 包
10. **禁止引入新的外部 Go/JS 依赖** 不经评估

## 12. 常用命令

```bash
# === 构建 ===
cd E:\codex\etl

## 后端构建 (Windows)
go build -o bin\etl-server.exe .\cmd\server\

## 前端构建
cd frontend && npm install && npm run build

# === 启动 ===
## 一键启动 (推荐)
.\run.ps1

## 或手动启动后端
.\bin\etl-server.exe

## 前端开发服务器 (独立开发用)
cd frontend && npm run dev

# === 测试 ===
## 全部单元测试
go test ./internal/...

## 带详细输出
go test -v ./internal/...

## 基准测试
go test -bench=. ./internal/etl/ -benchmem

## 覆盖率
go test -coverprofile=coverage.out ./internal/...
go tool cover -html=coverage.out

# === 代码检查 ===
go vet ./...

# === 依赖管理 ===
go mod tidy          # (需要网络)
go mod download
```

## 13. 测试与验证方式

### 当前测试状态
- **29 个单元测试**: 全部通过 (etl: 8, model: 3, parser: 9, rules: 5)
- **5 个基准测试**: 全部通过，覆盖 Clean/Dedup/Summary/FlowGraph/FullPipeline
- **Race Detector**: Windows/386 不支持 -race 标志

### 测试文件位置
| 包 | 测试文件 | 用例数 |
|----|---------|--------|
| internal/etl | etl_test.go, benchmark_test.go | 8 单元 + 5 基准 |
| internal/model | model_test.go | 3 |
| internal/parser | parser_test.go | 9 |
| internal/rules | rules_test.go | 5 |

### 验证清单
- [ ] `go test ./internal/...` 全部通过
- [ ] `go build -o bin\etl-server.exe .\cmd\server\` 无错误
- [ ] `cd frontend && npm run build` 无错误
- [ ] `.\\bin\\etl-server.exe` 启动后 `curl http://localhost:8000/api/health` 返回 `{"status":"ok"}`
- [ ] 浏览器 http://localhost:8000 正常显示前端

## 14. AI 开发工作流

### 接手项目时
1. 先读 `AGENTS.md` 和 `重构完成报告.md` 了解项目全貌
2. 查看 `go.mod` / `package.json` 确认依赖版本
3. 运行 `go test ./internal/...` 确认测试基线
4. 读取要修改的包/文件，了解现有代码风格

### 修改代码时
1. **保持 API 契约不变** — 前端通过固定的 JSON 路径和后端通信
2. **保持包结构不变** — scanner → parser/provider → etl → api 的管道顺序
3. **编辑工具使用 patch** — 不要用 sed/awk，使用 `patch` 工具做精确替换
4. **每次修改后运行测试** — 确保 29 个测试全部通过
5. **不引入新依赖** — 评估必要性，与现有依赖有冲突风险
6. **并发修改要加锁** — 所有共享状态使用 `sync.Mutex`

### 代码审查点
- API handler 是否返回 `gin.H{"detail": "..."}` 格式的错误
- 新的 parser/provider 是否正确实现接口
- 日志是否使用结构化字段 `.Str()/.Int()/.Err()`
- 大文件操作是否使用流式处理

## 15. 后续维护注意事项

### 已知问题
1. **IPv6 网络**: Go 模块代理通过 IPv6 可能超时，需设置 `GOPROXY=https://goproxy.cn,direct` 或 `GOPROXY=off`
2. **Race Detector**: Windows/386 不支持
3. **go mod tidy**: 网络受限时可能失败，部分间接依赖可能未清理
4. **AI 分析端点**: `/api/ai/analyze` 为占位实现，需配置 `DEEPSEEK_API_KEY`
5. **微信金额单位**: 调取数据金额为"分"，需检查原始文件确认单位
6. **大文件去重**: `DeduplicateTransactions` 内存方案，100 万+ 行可能有压力

### 易错点
- 多 sheet Excel 的 SheetName 为小写 "sheet1~n" (非 "Sheet1")
- 处理 BOM (`\ufeff`) 和全角空格 (`\u3000`)
- 微信交易明细信息表头 27 列，需检查原始文件
- `BuildFlowGraph` 的截断参数默认 600，前端可能请求更少的边
- FlowGraph meta 中的 `truncated` 标志前端依赖

### 扩展方向
- 实现 `model.Storage` 接口接入 SQLite/PostgreSQL
- 实现 `provider.Provider` 接口接入更多数据源 (如 银联、数字货币)
- Clean/Dedup/BuildFlowGraph 改为行级并发
- WebSocket 实时推送处理进度
- AI 分析端点接入实际的 DeepSeek API
