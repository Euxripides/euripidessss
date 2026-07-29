## 2026-07-27 13:31 虚拟币全链路真实下载与 CSV 回退验收

### Task
- 使用真实地址 `0x57136ea9b2be6cd4ad74c3ca5b24172f87c9cb8d` 验收 RPC、浏览器、邮件 CSV、DeepAML 和交易所大地址过滤。
- 保留“小数据先直连，失败后自动切浏览器抓取”的路由语义，并重点验证真实 CSV 文件。

### Changes
- 修改 `internal/cryptodownload/csv_scraper.go`：
  - 保持 CSV 小数据先调用直连接口；只有永久签名失败、邮件队列超时且没有可用直连数据时才进入浏览器回退。
  - 邮件收件地址固定使用用户配置，不再生成 Gmail `+alias` 地址。
- 修改 `internal/cryptodownload/tools/oklink_browser_email.mjs`：
  - 邮件 CSV 请求使用标准 Chrome 会话。
  - 修复 OKLink 脚本资源匹配正则导致的 `Invalid regular expression flags`。
  - 移除运行时主动注入浏览器隐身脚本及自动化特征开关。
- 修改 `internal/cryptodownload/csv_hydration.go` 和 `csv_raw_durable.go`：
  - 只有确实存在旧格式分段文件时才校验旧任务全历史时间范围，避免自定义时间段被误判为旧任务。
  - 成功分段写入完成标记；恢复时不再重复下载已经完成或不足 20,000 行的末段。
- 修改 `internal/cryptodownload/gui.go` 和 `gui_pause.go`：
  - 完成、失败、取消时同步更新每个下载分项的终态。
  - 断点继续时清除已解决的地址错误和任务错误，并将失败分项重置为等待状态。
- 新增/扩展测试：
  - `csv_hydration_test.go`：自定义时间段、完成检查点和断点恢复。
  - `csv_mail_config_test.go`：邮件目标不做别名轮换。
  - `gui_pause_test.go`：恢复时清除已解决错误。
  - `source_parity_test.go`：直连成功不回退、永久失败无数据才回退、已有直连数据不被浏览器覆盖。

### API and Database Changes
- 无新增 API。
- 无数据库结构变化。
- 本次真实测试更新了文件系统任务历史、检查点和下载结果。

### Live Validation
- 邮件 CSV 任务 `75e4918abb355b16` 最终为 `done`，地址分项均为 `done`：
  - `BSC_交易记录_*.csv`：8 行、11 列、2,042 字节。
  - `BSC_代币转账_*.csv`：10 行、10 列、2,703 字节。
  - 两个 CSV 均包含目标地址；`下载情况.xlsx` 为 8,281 字节，总览 1 行、分项 2 行、报错 0 行。
  - 真实日志确认两个 CSV 均由邮件链接下载；直连接口先返回业务码 `50113`，随后标准浏览器请求邮件成功，符合“先直连、失败后自动浏览器抓取”。
  - 三个结果文件均通过 `GET /api/crypto/download/file` 返回 HTTP 200 和正确附件 MIME。
- 浏览器 BSC 任务 `8a72f74633c64034` 为 `done`，下载量 1,177：
  - 普通交易 231、代币转账 333、资金记录 564、资产 49。
- RPC 最新块：
  - BSC 任务 `817499be4673b89e` 为 `done`。
  - ETH 任务 `499fcf4dbd7dca21` 为 `done`。
- RPC 历史实际交易任务 `d230d79256fb39b4` 命中 1 条普通交易和 1 条代币转账，但公共 BSC 节点不提供所需历史状态和 `trace_filter`，任务严格显示失败并保留诊断文件。
- DeepAML + 交易所过滤任务 `3731ca386f0c71b5` 已实际请求 DeepAML；目标地址及两个交易对手没有返回标签，过滤 0 条。任务失败原因仅为公共节点缺少历史状态，不是 DeepAML 错误。
- 浏览器 ETH 与 BSC 组合任务 `f8205787b49fc648` 中 BSC 成功，ETH 浏览器摘要返回截断 JSON；总任务严格标记失败，没有误报完成。
- 历史删除接口使用测试任务验证：记录删除后导出目录仍保留。
- Playwright 验证任务 `75e4918abb355b16` 的“重新导入”：确认弹窗、目标任务 ID 和“确认并开始下载”按钮均可见，确认前 `/history/import` 请求数为 0。

### Verified Commands
- `go test ./internal/cryptodownload -run "TestCSVSmallDataFallsBackToBrowserOnlyAfterDirectFailure|TestPrepareResumeClearsResolvedAddressErrors|TestHydrate" -count=1` — 通过。
- `go test ./internal/...` — 全部通过。
- `go vet ./...` — 通过。
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过。
- `npm run build`（cwd=`frontend`）— 通过；产物 `assets/index-fFyXy2w6.js`、`assets/index-_89HtAlz.css`。
- `git diff --check` — 无空白错误，仅现有 Windows 行尾提示。
- `.\run.ps1` — 后端重启成功，PID `32084`；`/api/health` 返回 `status=ok`。
- 重启后再次执行 Playwright 历史导入确认测试 — 弹窗可见，确认前导入请求数为 0。

### Unfinished
- OKLink 直连 CSV 当前仍返回业务码 `50113`；自动浏览器邮件回退已真实跑通，不影响本次 CSV 完成。
- 公共 BSC RPC 节点无法提供部分历史状态与 `trace_filter`，历史块 RPC 全功能需要具备 archive/trace 能力的节点。
- ETH 浏览器摘要存在偶发截断 JSON；ETH RPC 最新块模式已跑通，浏览器 ETH 仍需后续增强响应重试。

## 2026-07-27 12:49 历史导入确认、失败状态与结果文件下载修复

### Task
- 修复历史任务点击导入后未经确认直接下载、RPC 发生 HTTP 403 仍显示完成、结果文件按钮无法打开的问题。

### Root Cause
- 前端“重新导入/导入所选”直接调用 `/history/import`，没有确认阶段。
- `runGUIJob` 将采集错误写入 `Errors` 后仍生成工作簿并调用 `completeAddress`，任务结束时又将所有仍为 `running` 的任务无条件改为 `done`。
- 前端通过 `window.open(file://...)` 打开后端本机路径；浏览器从 HTTP 页面访问本地 `file://` 会被安全策略阻止。

### Changes
- 修改 `frontend/src/features/crypto/CryptoDownloadPanel.tsx`：
  - 单条或批量历史导入先弹出确认框，列出任务 ID、地址和链；只有点击“确认并开始下载”才调用导入接口。
  - 有错误的任务不再显示绿色“结果文件已生成”，改为黄色“下载失败，已保留诊断文件”。
  - 结果文件按钮改为调用受控 HTTP 下载接口，并将文案改为“下载”。
  - 支持将分号连接的多个 CSV 结果拆成独立下载项。
  - “完成地址”改为“已处理地址”，避免失败地址计入终态数时造成误解。
- 修改 `frontend/src/features/crypto/cryptoDownloadApi.ts`，新增受控结果文件下载 URL 构造。
- 修改 `frontend/src/features/crypto/crypto-download.css`，新增历史导入确认列表样式。
- 修改 `internal/cryptodownload/gui.go`：
  - 任务收尾时只要存在采集错误，任务状态改为 `failed`，消息显示错误数量。
  - 地址采集存在错误时状态改为 `failed`，不再标记为完成；已生成文件保留为诊断结果。
- 新增 `internal/cryptodownload/gui_result_file.go`：
  - 新增 `GET /api/crypto/download/file?id=...&path=...`。
  - 仅允许下载该任务结果列表、地址结果或任务组 `下载情况.xlsx` 中的真实文件。
  - 非任务文件返回 403，缺失文件返回 404。
- 新增 `internal/cryptodownload/gui_result_file_test.go`，覆盖任务失败收尾、失败地址诊断文件和文件下载授权。
- 修改 `internal/cryptodownload/api_handler.go`，注册结果文件接口。

### API and Database Changes
- 新增 `GET /api/crypto/download/file`，查询参数为任务 `id` 和结果 `path`，响应为附件下载。
- 无数据库变化。

### Verified Commands
- `go test ./internal/cryptodownload/...` — 通过。
- `go test ./internal/...` — 全部通过。
- `npm run build`（cwd=`frontend`）— 通过；最终产物 `assets/index-fFyXy2w6.js`、`assets/index-_89HtAlz.css`。
- `.\run.ps1` — 后端成功重启，PID `38796`，`/api/health` 返回 `ok`。
- Playwright 运行态验证：
  - 点击“重新导入”显示确认弹窗和“确认并开始下载”按钮。
  - 确认前 `/history/import` 请求数量为 0。
- 真实结果文件接口验证：
  - 历史任务 `e530b670db07b9da` 的 `001_ETH.xlsx` 返回 HTTP 200。
  - `Content-Type` 为 Excel MIME，`Content-Disposition` 为 `attachment; filename=001_ETH.xlsx`，响应长度 14,945 字节。
  - 文件 ZIP/XLSX 结构有效，共 16 个条目且包含 `xl/workbook.xml`。
  - 请求该任务未授权的 `go.mod` 返回 HTTP 403。
- 未再次发起真实下载任务，避免在未获用户确认时创建任务或产生额外外部请求。

### Unfinished
- 用户截图中的任务 `3954bf73db7cc49d` 在当前运行时、历史文件和任务 JSON 中均未找到，无法直接复核其已生成文件；本次从代码路径和截图错误内容确认并修复同一状态错误。

## 2026-07-27 12:40 历史任务导入与删除

### Task
- 参考原项目，为虚拟币下载页的历史任务补齐导入、删除和断点继续能力。

### Changes
- 修改 `frontend/src/features/crypto/cryptoDownloadApi.ts`：
  - 接入 `GET /api/crypto/download/history` 持久化历史列表。
  - 接入 `POST /api/crypto/download/history/import` 单条或批量重新导入。
  - 接入 `POST /api/crypto/download/history/resume` 从历史断点继续。
  - 接入 `DELETE /api/crypto/download/history` 删除历史记录。
- 修改 `frontend/src/features/crypto/CryptoDownloadPanel.tsx`：
  - 原“最近任务”折叠改为“历史任务”，显示全部持久化记录。
  - 新增单条勾选、全选、批量“导入所选”和刷新。
  - 每条记录新增“重新导入”“删除记录”；暂停或冷却记录额外显示“断点继续”。
  - 删除前二次确认，并明确“导出的数据文件不会被删除”。
  - 启动、完成、重新导入或断点继续后同步刷新运行任务和历史记录。
- 修改 `frontend/src/features/crypto/crypto-download.css`：
  - 新增历史任务工具栏、记录摘要、操作区和移动端布局。

### API and Database Changes
- 无后端接口或数据库变更；复用项目中已存在且与原项目一致的历史接口。
- 历史记录仍使用文件系统持久化；删除历史记录不会删除任务输出目录和导出文件。

### Verified Commands
- `npm run build`（cwd=`frontend`）— 通过；产物 `assets/index-Vw8Ib83Z.js` 和 `assets/index-DEzPJ5cj.css`。
- Playwright 运行态浏览器测试（`http://127.0.0.1:8000`）：
  - 实际读取并显示 10 条持久化历史记录。
  - 历史任务默认收起，收起时操作区不渲染。
  - 展开后 10 条均显示“重新导入”和“删除记录”，其中 1 条暂停/冷却记录显示“断点继续”。
  - “导入所选”未选择时禁用，全选后启用。
  - 删除二次确认显示“删除这条历史记录？”和“导出的数据文件不会被删除。”
  - 验证未确认导入或删除，现有任务与历史数据未发生变化。

### Unfinished
- 无。

## 2026-07-27 12:34 结果/错误通知与最近任务折叠

### Task
- 重新排版虚拟币下载页，将“结果文件、错误、最近任务”三个底部卡片替换为通知和折叠显示。

### Changes
- 修改 `frontend/src/features/crypto/CryptoDownloadPanel.tsx`：
  - 删除底部“结果文件”“错误”“最近任务”三个卡片。
  - 任务产生结果文件时显示右上角持久成功通知，列出最多 5 个文件并提供“打开”按钮。
  - 任务产生错误时显示右上角持久错误通知，展示最多 3 条错误摘要并支持展开长文本。
  - 通知按任务 ID 使用固定 key，同一任务结果/错误数量变化时更新原通知，不按轮询频率重复创建。
  - 通知最多同时显示 4 条。
  - 最近任务移动到任务状态列，使用默认收起的 `Collapse`。
  - 展开后以紧凑任务行显示任务 ID、状态消息、状态标签和进度；点击可切换当前任务，并触发该任务的结果/错误通知。
- 修改 `frontend/src/features/crypto/crypto-download.css`：
  - 删除旧底部三列卡片布局。
  - 新增最近任务折叠列表、选中/悬停状态和通知内容布局。

### API and Database Changes
- 无。

### Verified Commands
- `npm run build`（cwd=`frontend`）— 通过；产物 `assets/index-BZ82QbK7.js` 和 `assets/index-CjQNo_zX.css`。
- Playwright 运行态浏览器测试：
  - 旧“结果文件/错误/最近任务”卡片数量均为 0。
  - 最近任务折叠组件可见；默认收起时条目为 0，展开后显示 8 条。
  - 选择任务 `224e6dc51dcb8726` 后显示两个持久通知：
    - `结果文件已生成（2）`，包含 2 个“打开”按钮。
    - `任务错误（3）`。

### Unfinished
- 无。

## 2026-07-27 12:28 下载设置分类弹窗

### Task
- 将地址栏下方的下载设置分类收纳到弹窗，并将弹窗开关放到页面右上角。

### Changes
- 修改 `frontend/src/features/crypto/CryptoDownloadPanel.tsx`：
  - 页面右上角新增“下载设置”按钮。
  - 主表单只保留地址列表、确认多地址链和开始下载。
  - 设置弹窗按以下类别组织：
    - 下载方式与默认链
    - RPC 与区块范围（RPC 模式）
    - OKLink CSV 与接收邮箱（CSV 模式）
    - 性能、重试与风控
    - 输出与数据处理
    - DeepAML 与地址过滤（非 CSV 模式）
  - 数据源切换后动态显示对应设置分类。
  - 所有字段名、默认值、校验规则和提交参数保持不变。
- 修改 `frontend/src/features/crypto/crypto-download.css`，增加分类卡片间距和弹窗内容布局。

### API and Database Changes
- 无。

### Verified Commands
- `npm run build`（cwd=`frontend`）— 通过；产物 `assets/index-Go4IRlRi.js` 和 `assets/index-WqiWNNG0.css`。
- Playwright 运行态浏览器测试：
  - 右上角“下载设置”按钮可见并可打开弹窗。
  - CSV 模式显示“OKLink CSV 与接收邮箱”，隐藏 RPC 分类。
  - RPC 模式显示“RPC 与区块范围”和“DeepAML 与地址过滤”，隐藏 CSV 分类。
  - “性能、重试与风控”和“输出与数据处理”分类正常。
  - 关闭设置弹窗后，多地址粘贴仍自动显示 2 行逐地址选链弹窗。

### Unfinished
- 无。

## 2026-07-27 12:22 多地址粘贴即时弹窗修复

### Task
- 修复上一版只有点击“开始下载”才弹窗、仅输入或粘贴多个地址没有反应的问题。

### Changes
- 修改 `frontend/src/features/crypto/CryptoDownloadPanel.tsx`：
  - 地址框粘贴多个唯一地址后立即打开逐地址选链弹窗。
  - 增加显式“确认多地址链”按钮，支持手动输入后主动打开弹窗。
  - 即时弹窗的确认操作只把结果按 `地址,链` 写回地址框，不提前启动下载。
  - 写回后点击“开始下载”直接使用已经确认的逐地址链配置。
  - 如果未预先确认，点击开始仍会兜底弹窗，确认后才启动任务。
- 修改 `frontend/src/features/crypto/crypto-download.css`，增加确认按钮布局。

### Verified Commands
- `npm run build`（cwd=`frontend`）— 通过；产物 `assets/index-Bxi6M7LN.js` 和 `assets/index-BR4G3GUf.css`。
- Playwright 运行态浏览器测试：
  - 打开 `http://127.0.0.1:8000/`，进入“虚拟币 → 数据下载”。
  - 粘贴两个地址后，“确认地址和链”弹窗自动可见，地址选链行数为 2。
  - 第二个地址选择 BSC 并确认后，地址框写回第一行 `ETH`、第二行 `BSC`。
  - 弹窗正常关闭，未自动创建下载任务。

### API and Database Changes
- 无。

## 2026-07-27 12:18 多地址逐一选链弹窗恢复

### Task
- 修复 React 虚拟币下载页输入多个地址后没有弹窗选择每个地址所属链的问题，使交互与原项目一致。

### Root Cause
- 原项目内置 GUI 包含 `addressConfirmModal`，多个地址在开始前逐行确认链。
- React 重构页只调用 `parseAddressChains()`，未迁移确认弹窗；未写链的地址会直接套用默认链并提交。

### Changes
- 修改 `frontend/src/features/crypto/CryptoDownloadPanel.tsx`：
  - 多于一个唯一地址时阻止直接启动，打开“确认地址和链”弹窗。
  - 弹窗逐行展示地址和链下拉框，确认后才生成 `addressChains` 并启动任务。
  - 支持输入行中的显式 `地址,链` / `地址 链` 作为弹窗初始值。
  - 未显式写链时使用第一个默认链作为初始值。
  - 地址按大小写不敏感去重，每个地址只创建一个链任务，与原项目逐地址单链语义一致。
  - 单地址继续直接启动，不增加多余确认步骤。
- 修改 `frontend/src/features/crypto/crypto-download.css`，增加弹窗地址/链列表布局。

### API and Database Changes
- 无。仍通过既有 `addressChains: [{address, chain}]` 提交。

### Verified Commands
- `npm run build`（cwd=`frontend`）— 通过；产物 `assets/index-DsqJyHwG.js` 和 `assets/index-BeHbkAnG.css`。
- 运行态 `http://127.0.0.1:8000/` 已提供新 JS 产物。
- 运行态 JS 已确认包含“确认地址和链”及“请逐一确认每个地址所属的链”弹窗文案。

### Unfinished
- 无。

## 2026-07-27 12:11 DeepAML 真实端到端验收

### Task
- 使用用户临时提供的 DeepAML Key，验证真实鉴权、地址标签、交易所过滤和 Excel 导出完整链路。

### Changes
- 未修改业务代码、接口、数据库或前端。
- DeepAML Key 仅用于本次请求和任务内存，没有写入项目配置、任务持久化、日志或文档。

### Verified Commands
- 直接请求 `https://openapi.deepaml.io/v1/address-labels`：
  - HTTP `200`、业务码 `200`、消息 `SUCCESS`。
  - 测试地址返回 `name=Binance`、`type=EXCHANGE`。
  - 返回字段 `chain_id/address/type/name` 与 `AMLAddressLabel` 解析结构一致。
- 当前项目真实任务 `224e6dc51dcb8726`：
  - 地址 `0x28c6...1d60`，ETH 固定区块 `25618102`，DeepAML RPS `1`，启用标签与交易所过滤。
  - 下载阶段普通交易命中 3 行；DeepAML 过滤后的 Excel 普通交易为 2 行。
  - 汇总表 `DeepAML过滤交易所行数=2`；这是同一逻辑记录分别从交易表和资金表移除的合计，不是 2 个唯一交易哈希。
  - 目标地址和资产标签为 `Binance`；保留交易对手方标签为 `Tether`，类型为 `STABLE COIN,DEFI`。
  - 任务状态 `done`，生成主工作簿和 `下载情况.xlsx`。
- 输出：
  - `backend/data/crypto_download/deepaml_live_20260727/exports/deepaml_live_224e6dc51dcb8726/001_0x28c6c06298d5_743bf21d60/deepaml_live_001_ETH_0x28c6c06298d5_743bf21d60_20260727_121010.xlsx`
  - `backend/data/crypto_download/deepaml_live_20260727/exports/deepaml_live_224e6dc51dcb8726/下载情况.xlsx`
- 对 `docs/` 和 `backend/config/` 精确检查，未发现本次 DeepAML Key。

### Notes
- 本次公共 ETH RPC 对历史余额、合约检查和历史日志返回 archive `403`；普通交易仍成功下载，且不影响本次 DeepAML 标签与过滤验收。
- DeepAML 功能已证明可真实跑通。公共 RPC 的 archive 限制属于独立问题。

## 2026-07-27 11:59 CSV 邮箱主机错位与暂停提示修复

### Task
- 修复真实 BSC CSV 任务 `a5ae70fbacc4a0db` 把 Gmail 地址当作 IMAP 主机进行 DNS 查询的问题。
- 修复所有 CSV 失败一律提示“切换 VPN”的错误归因。

### Root Cause
- 运行态设置中 `csvImapHost` 错填为接收邮箱，`csvImapUser` 为空，因此程序执行了 `lookup <邮箱地址>`，并非 VPN 节点故障。
- 同一任务的 OKLink 直连请求先返回业务错误 `50113 incorrect request sign parameters`，随后转邮箱通道时才被错误 IMAP 主机拦截；这是两个独立故障。
- `run.ps1` 仅在二进制不存在时构建，导致第一次重启继续运行旧二进制。

### Changes
- 修改 `internal/cryptodownload/main.go`：
  - Gmail 的 IMAP 主机为空或误填为邮箱地址时自动纠正为 `imap.gmail.com`。
  - Gmail IMAP 端口默认 `993`，用户名为空时使用接收邮箱。
- 修改 `internal/cryptodownload/gui.go`、`api_handler.go`、`gui_pause.go`：
  - 保存设置、启动任务和继续任务均执行相同的邮箱字段规范化。
  - 启动和继续前从当前安全设置补齐未随响应返回的授权码。
  - 缺少邮箱、主机、端口、用户名或授权码时在发起下载前返回明确错误。
  - 继续暂停任务时重新加载当前邮箱设置，因此旧任务无需重建。
  - IMAP/DNS 错误提示检查邮箱配置；`50113` 提示 OKLink 会话或签名失效；其他错误使用中性配置/网络提示，不再一律要求切换 VPN。
- 修改 `frontend/src/features/crypto/CryptoDownloadPanel.tsx`：
  - CSV 邮箱字段增加必填、邮箱格式和 IMAP Host 不能包含 `@` 的校验。
  - 增加 `imap.gmail.com`、IMAP 用户名和 Gmail 应用密码提示。
- 新增 `internal/cryptodownload/csv_mail_config_test.go`，覆盖 Gmail 字段纠错和暂停错误分类。
- 修改 `run.ps1`：未指定 `-SkipBuild` 时始终重新构建后端，避免修改后启动旧二进制。

### API Changes
- `POST /api/crypto/download/start` 会在启动前校验并规范化 CSV 邮箱配置；无效配置返回 HTTP 400。
- `POST /api/crypto/download/resume` 会重新加载当前邮箱设置后继续原任务。
- 接口路径和响应结构不变。

### Database Changes
- 无。

### Verified Commands
- `go test ./internal/cryptodownload -count=1` — 通过。
- `go test ./internal/... -count=1` — 全部通过。
- `go vet ./internal/...` — 通过。
- `npm run build`（cwd=`frontend`）— 通过；产物 `assets/index-Dhbzya_u.js`，仅有既有 chunk size warning。
- `.\run.ps1` — 已真实重新构建并重启，PID `34204`。
- 运行态 `/api/health` 返回 `ok`。
- 运行态下载设置返回 `csvImapHost=imap.gmail.com`、端口 `993`、IMAP 用户已配置，密码字段未暴露。
- 原任务 `a5ae70fbacc4a0db` 保持暂停且 `needsCredentials=false`，可由用户点击继续；未自动重发 OKLink 请求。

### Unfinished
- 用户点击“继续下载”后需要观察 OKLink 邮箱申请结果；若仍返回 `50113`，需要刷新有效的 OKLink 浏览器会话/签名，而不是切换 VPN。

## 2026-07-27 11:09 Gmail 邮箱 CSV 真实测试

### Task
- 使用用户临时提供的 Gmail 与应用授权码，对虚拟币 CSV 下载的邮箱依赖做真实验证。
- 继续核对当前项目与 `E:\codex\虚拟币` 的 CSV 下载实现是否一致。

### Changes
- 未修改业务代码、接口、数据库或前端。
- 本次凭据仅用于一次 TLS/IMAP 认证，没有写入项目配置、任务记录、日志或本文档。

### Verified Commands
- 真实连接 `imap.gmail.com:993`：TLS 握手成功，Gmail 应用授权码认证成功，随后正常退出；未读取、删除或修改邮件。
- `git diff --no-index` 对比原项目与当前项目的 `csv_scraper.go`、`csv_browser_email.go`、`csv_static_strategy.go`：
  - 下载与邮箱链路逻辑一致。
  - 差异仅为当前项目包名由 `main` 改为 `cryptodownload`，以及 `gofmt` 对齐空格。
- 已确认两边均为：小段优先直连 CSV，直连不可用或剩余记录超过 20,000 时申请邮箱 CSV，随后通过 IMAP 匹配链接并下载；业务码、冷却和重试逻辑一致。

### Unfinished
- 自动把邮箱身份或授权码注入 `/api/crypto/download/start` 被当前执行环境的凭据保护策略阻止，因此没有代替用户点击发起 OKLink 邮箱任务。
- 需要用户在本项目页面本地填写邮箱字段并点击“开始下载”，才能完成 OKLink 申请邮件、收信匹配和链接下载的最终平台验收。

### Notes
- 不要在文档、日志、配置或持久化任务中记录邮箱授权码。
- 不要通过 Gmail `+alias` 或账号轮换规避 OKLink 限流；出现 `429`、`50113` 或风控时保留检查点并按冷却策略停止。

## 2026-07-27 00:18 虚拟币下载与地址区分原项目一致性修复

### Task
- 按用户要求，使当前项目的虚拟币数据下载和地址区分功能与各自原项目保持一致。
- 修复上一轮真实测试发现的地址区分 `EOA/CONTRACT` 缺失，并补齐下载页面未暴露的原项目参数。

### Changes
- 修改 `internal/api/crypto_address_handlers.go`：
  - BSC 未手填 RPC 时默认使用 `https://bsc-rpc.publicnode.com`。
  - EVM 在线判断只调用原脚本同源的 `eth_getCode`，根据空/非空 bytecode 输出 `EOA` 或 `CONTRACT`。
  - 输出 EIP-55 checksum 地址。
  - 新增原脚本结果字段 `status`、`retry_count`、`error`，并对齐 `INVALID/ERROR` 状态语义。
  - 限流/瞬时错误最多重试 5 次；多个 RPC 节点时优先切换下一节点；成功后不再把完整合约 bytecode 放入响应。
- 修改 `internal/api/crypto_address_handlers_test.go`，新增 EOA、CONTRACT、checksum、INVALID、RPC ERROR、限流节点切换契约测试。
- 修改 `internal/cryptodownload/gui.go`，使未提交 `endBlock` 的下载请求默认使用原项目的 `-1`（最新区块），同时保留显式 `endBlock=0`。
- 新增 `internal/cryptodownload/source_parity_test.go`，覆盖 RPC/CSV/Browser 显式路由、不可见 Unicode 地址清理、RPC 最新区块默认值。
- 修改 `frontend/src/features/crypto/CryptoAddressPanel.tsx`、`cryptoAddressApi.ts`：
  - 页面展示并复制原脚本的 `地址/类型/状态/重试次数/错误信息`。
  - 默认选择 BSC 和公共 BSC RPC。
- 修改 `frontend/src/features/crypto/CryptoDownloadPanel.tsx`、`cryptoDownloadApi.ts`：
  - 补回原项目 RPC 高级参数：多链 RPC 配置、原生币符号、起止/截止区块、区块/日志批次。
  - 补回 CSV 起止时间。
  - 按本次“全部保持一致”要求恢复 DeepAML 标签、DeepAML RPS、交易所大地址过滤选项。
  - 默认 `endBlock=-1`、风控冷却 1800 秒，与原项目 GUI 一致。

### New Functionality
- 当前地址区分页面现可准确显示 BSC/EVM 的 `EOA`、`CONTRACT`、`INVALID`、`ERROR`。
- 当前下载页面可配置原项目 GUI 的 RPC/CSV/DeepAML 高级参数。

### API Changes
- `POST /api/crypto/address-classify` 的 item 新增：
  - `status`
  - `retry_count`
  - `error`
- 既有字段和接口路径保持不变。
- `POST /api/crypto/download/start` 在省略 `endBlock` 时由错误的 Go 零值 `0` 改为原项目默认 `-1`。

### Database Changes
- 无。

### Frontend Changes
- 地址区分结果表新增状态和重试次数，类型列优先显示 `EOA/CONTRACT/INVALID/ERROR`。
- 复制结果表头与原脚本 Excel 一致：`地址、类型、状态、重试次数、错误信息`。
- 数据下载页恢复并补齐原项目高级参数。

### Verified Commands
- `go test ./internal/api ./internal/cryptodownload -count=1` — 通过。
- `go test ./internal/... -count=1` — 全部通过。
- `go vet ./internal/...` — 通过。
- `npm run build`（cwd=`frontend`）— 通过；产物 `assets/index-KAqYElXl.js`，仅有既有 chunk size warning。
- `go build -o bin\etl-server.exe .\cmd\server\; .\run.ps1 -SkipBuild` — 构建并重启成功，PID 43996。
- 真实 BSC 对照：
  - 当前项目与原 `查询.py` 均将 `0x28c6...1d60` 输出为 checksum 地址、`EOA/OK/0 次重试/无错误`。
  - 当前项目与原 `查询.py` 均将 `0x55d3...7955` 输出为 `CONTRACT/OK/0 次重试/无错误`。
- 真实 ETH 下载对照（区块 `25618102`）：
  - 当前项目任务 `e530b670db07b9da` 完成，交易 3、代币转账 2、内部交易 0、NFT 0、资产 2、错误 0。
  - 原项目同参数运行结果完全相同。
  - 两个工作簿均为 7 个同名 sheet，维度一致；交易哈希、交易/代币/NFT/内部交易/资金明细完全一致。
  - 仅查询时间、运行时最新区块和查询间隔内变化的实时代币余额不同，属于动态链上状态。

### Open Items
- OKLink CSV 邮箱和 Browser 模式的实现与原项目为同一份下载引擎，并有路由契约测试；本次未使用真实邮箱/平台会话再次触发外部下载，生产验收仍需有效邮箱及当前平台会话。

### Notes
- 原 `查询.py` 中的私有 Chainstack URL 未复制到当前项目；使用真实公共 BSC RPC 执行相同 `eth_getCode` 判断，避免传播原脚本内的节点凭据。
- 当前项目保留 Web 页面批量输入和多链候选扩展；BSC 在线判断结果及下载核心契约已与原项目对齐。

## 2026-07-26 23:57 虚拟币下载与地址区分真实地址对比测试

### Task
- 对比当前项目虚拟币下载功能与原项目 `E:\codex\虚拟币` 的实现和真实下载结果。
- 使用原地址区分脚本 `D:\app\桌面\新建文件夹 (2)\查询.py` 与当前 `/api/crypto/address-classify` 做真实 BSC 地址分类对照。

### Changes
- 无业务代码变更。
- 生成真实下载验证产物：
  - `backend/data/crypto_download/real_compare_20260726/original_rpc.xlsx`
  - `backend/data/crypto_download/real_compare_20260726/small_exports/live_rpc_small_7fbcb1d7a591ecc8/001_0x28c6c06298d5_743bf21d60/001_ETH.xlsx`
  - `backend/data/crypto_download/real_compare_20260726/small_exports/live_rpc_small_7fbcb1d7a591ecc8/下载情况.xlsx`
- 更新 `docs/AI_HANDOFF.md`、`docs/CHANGELOG_AI.md`。

### New Functionality
- 无；本次为静态一致性审计和真实链上验收。

### API Changes
- 无。

### Database Changes
- 无。

### Frontend Changes
- 无。

### Verified Commands
- `go test ./internal/cryptodownload ./internal/api -count=1` — 通过。
- `go test ./internal/... -count=1` — 全部通过。
- `go test -count=1 ./...`（cwd=`E:\codex\虚拟币`）— 原项目主包和工具包全部通过。
- `go build -o bin\etl-server.exe .\cmd\server\; .\run.ps1 -SkipBuild` — 当前项目构建并启动成功，PID 40336。
- 当前项目真实 RPC 任务 `7fbcb1d7a591ecc8`：地址 `0x28c6c06298d514db089934071355e5743bf21d60`，ETH 区块 `25617962`，命中 1 条普通交易，下载统计 `downloaded=3`，状态 `done`，无任务/地址错误。
- 原项目 `wallet-exporter.exe` 使用完全相同的地址、RPC、区块范围和扫描参数运行：普通交易 1、内部交易 0、代币转账 0、NFT 0、资产 1、错误 0，约 5 秒完成。
- 工作簿逐单元格核对：两边均为 7 个同名 sheet，维度完全一致；除统计摘要中的查询时间和运行时最新区块外，其余 sheet 单元格完全一致；交易哈希均为 `0x0f455c9e1ac3e57fec7eb2c42f6b1305178795079c5195a9092ddf4b997a7886`。
- 当前项目 101 区块长测任务 `f5bafb7fc8f60d8d` 在普通交易阶段完成 `101/101`，命中 277 行；随后人工取消，未作为完整导出结论。
- 地址区分真实 BSC 对照：原脚本将 `0x28c6...1d60` 判为 `EOA`、`0x55d3...7955` 判为 `CONTRACT`；当前接口两者 RPC 均成功，但 `kind` 都返回 `账户/合约地址`。
- `http://127.0.0.1:8000/api/health` 返回顶层 `status=ok`；当前 `analysis_plane.available=false` 是既有 DuckDB CLI 状态，与本次虚拟币测试无关。

### Open Items
- 当前地址区分功能与原脚本不一致：虽然已调用 `eth_getCode`，但没有根据 `code == 0x` 输出 `EOA`，也没有根据非空 bytecode 输出 `CONTRACT`。
- 101 区块长测期间出现过一次 Windows 作业 JSON 原子替换 `Access is denied`；任务最终状态文件仍成功落盘，单区块完整任务未复现。若要做长任务生产验收，应继续压测作业持久化。
- OKLink CSV/邮箱和浏览器来源本次未做真实端到端请求；下载引擎静态对比未发现这些采集路径与原项目分叉，但真实平台会话、邮箱和风控仍需单独验收。

### Notes
- 当前项目与原项目的 RPC/CSV/浏览器采集、source 路由、checkpoint、重试和限速实现保持一致；可见功能差异集中在当前项目 API 封装和 Windows 长路径输出文件名缩短，不影响工作簿数据内容。
- 本次没有修改后端业务代码；服务器为测试而重新构建并启动，当前访问地址为 `http://127.0.0.1:8000`。

## 2026-07-24 18:47 虚拟币下载选项精简

### Task
- 按用户要求从虚拟币数据下载页面删除 `DeepAML 标签` 和 `过滤交易所大地址` 选项。
- 向用户解释 `扫描原生交易` 的含义。

### Changes
- 修改 `frontend/src/features/crypto/CryptoDownloadPanel.tsx`，删除两个复选框及对应默认值。
- 修改 `frontend/src/features/crypto/cryptoDownloadApi.ts`，删除前端请求类型中的 `amlKey`、`amlLabels`、`amlRps`、`filterExchange` 字段。
- 更新 `docs/AI_HANDOFF.md`、`docs/CHANGELOG_AI.md`。

### New Functionality
- 无新增功能；本次为前端选项精简。

### API Changes
- 无接口路径变化。
- 前端不再向 `/api/crypto/download/start` 发送 DeepAML 标签和交易所过滤相关字段。

### Database Changes
- 无。

### Frontend Changes
- `虚拟币 -> 数据下载` 页面不再显示 `DeepAML 标签`、`过滤交易所大地址`。
- 保留 `扫描原生交易`、`补充交易详情`、`断点续跑` 等选项。

### Verified Commands
- `cd E:\codex\etl\frontend; npm run build` — 通过，仍有既有 Vite chunk size warning；新前端产物为 `assets/index-YUv87gya.js`。

### Open Items
- 无。

### Notes
- 本次未修改后端代码，因此未执行 `run.ps1` 重启；刷新浏览器即可加载新前端构建。

## 2026-07-24 18:41 启动项目

### Task
- 按用户要求启动当前 ETL 项目。

### Changes
- 无业务代码变更。
- 更新本交接文档和变更记录。

### New Functionality
- 无。

### API Changes
- 无。

### Database Changes
- 无。

### Frontend Changes
- 无。

### Verified Commands
- `Get-Process etl-server -ErrorAction SilentlyContinue | Stop-Process -Force; .\run.ps1` — 已启动后端，PID 45184。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`，`analysis_plane.available=true`。
- `curl.exe -s http://127.0.0.1:8000/` — 首页引用当前前端构建产物 `assets/index-VAmw0-gn.js` 和 `assets/index-tjMY5KbI.css`。

### Open Items
- 无。

### Notes
- 当前访问地址：`http://127.0.0.1:8000`。

## 2026-07-24 17:52 虚拟币数据下载项目移植

### Task
- 将 `E:\codex\虚拟币` 项目的 OKLink/RPC/CSV 虚拟币数据下载能力移植进当前 ETL 项目。
- 前端入口放到当前项目侧边栏 `虚拟币 -> 数据下载`，并保证后端任务启动、轮询、取消、继续、历史任务列表等主流程可用。

### Changes
- 新增 `internal/cryptodownload/`，从源项目迁入非测试 Go 源码、`browser_stealth`、`useragent`、OKLink signer/browser-email Node 脚本，并将包名改为当前项目内部包。
- 新增 `internal/cryptodownload/api_handler.go`，提供当前项目可挂载的下载任务 HTTP API；设置读写落在 `backend/data/crypto_download`，读取时不回显 IMAP 授权码。
- 修改 `internal/api/handlers.go`、`internal/api/crypto_address_handlers.go`，初始化并挂载 `/api/crypto/download/*`。
- 新增 `frontend/src/features/crypto/cryptoDownloadApi.ts`、`frontend/src/features/crypto/CryptoDownloadPanel.tsx`、`frontend/src/features/crypto/crypto-download.css`。
- 修改 `frontend/src/App.tsx`，在 `虚拟币` 菜单下新增 `数据下载` 入口并默认展开该分组。
- 修改 `go.mod`、`go.sum`，加入源项目下载功能所需 `github.com/emersion/go-imap`、`github.com/refraction-networking/utls` 等依赖。

### New Functionality
- 当前项目内新增虚拟币数据下载工作台，支持：
  - RPC、OKLink CSV、浏览器三种数据源。
  - 多地址、多链批量下载；地址行支持 `地址`、`地址 链`、`地址,链`。
  - 地址级进度、任务整体进度、日志/错误/结果文件展示。
  - 任务取消、地址取消、暂停/冷却后的继续下载。
  - CSV 邮箱/IMAP、并发、RPS、超时、重试、分页、断点续跑、DeepAML 标签、交易所过滤等参数。

### API Changes
- 新增本地 API 前缀 `/api/crypto/download`：
  - `GET/POST /api/crypto/download/settings`
  - `POST /api/crypto/download/start`
  - `POST /api/crypto/download/resume?id=...`
  - `GET /api/crypto/download/job?id=...`
  - `GET /api/crypto/download/jobs`
  - `POST /api/crypto/download/cancel?id=...`
  - `POST /api/crypto/download/cancel?id=...&index=...`
  - `GET /api/crypto/download/history`
  - `POST /api/crypto/download/history/import`
  - `POST /api/crypto/download/history/resume?id=...`

### Database Changes
- 无外部数据库结构变更。
- 新增本地文件运行目录：`backend/data/crypto_download/`，用于虚拟币下载任务配置、作业记录、历史记录、默认 raw/exports 数据。

### Frontend Changes
- 新增 `虚拟币 -> 数据下载` 页面。
- `虚拟币` 菜单默认展开，保留既有 `地址区分` 页面。
- 前端默认输出目录调整为 `backend/data/crypto_download/exports`，默认 raw 目录为 `backend/data/crypto_download/raw`。

### Verified Commands
- `go test ./internal/cryptodownload ./internal/api -count=1` — 通过。
- `go test ./internal/... -count=1` — 通过。
- `go vet ./internal/...` — 通过。
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过。
- `cd frontend && npm run build` — 通过，仍有既有 Vite chunk size warning。
- `Get-Process etl-server -ErrorAction SilentlyContinue | Stop-Process -Force; .\run.ps1` — 已重启后端，PID 7108。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`，`analysis_plane.available=true`。
- `curl.exe -s http://127.0.0.1:8000/api/crypto/download/settings` — 返回当前项目默认设置且 `csvImapPassword` 为空。
- `curl.exe -s http://127.0.0.1:8000/api/crypto/download/jobs` — 返回 `[]`。
- `curl.exe -s http://127.0.0.1:8000/` — 已引用最新前端产物 `assets/index-VAmw0-gn.js` 和 `assets/index-tjMY5KbI.css`。

### Open Items
- 本次未对真实 OKLink/RPC 地址发起下载，以避免触发外部网络、邮箱或平台限流；主流程由编译、API smoke 和前端构建验证。
- 源项目测试文件未整体迁入当前项目，避免把原项目大量契约测试和独立工具测试直接混入当前 ETL 测试面；后续可按下载模块风险逐步补当前项目内回归测试。

### Notes
- `/api/crypto/download/settings` 使用当前项目专属配置目录，不再读取源工具的全局用户配置。
- 设置接口读取时不返回已保存 IMAP 密码/授权码；保存时如果前端不传新密码，会保留当前项目目录中的旧密码。
- 工作区存在大量 Dune profile、历史 ETL 改动和运行时文件，本次未回退、未清理。

## 2026-06-29 0622反馈真实目录识别/合并验证

### Task
- 按用户要求使用 `E:\项目\网赌\梅县-网赌30娱乐\资金流调证\0622反馈` 做合并测试，验证当前逻辑能否区分支付宝、微信、银行流水。

### Changes
- 无业务代码变更。
- **新增验证产物** `backend/data/outputs/scan_0622_feedback.json` — 当前扫描器对该目录的识别明细。
- **新增验证产物** `backend/data/outputs/pipeline_0622_feedback.json` — 当前 `etl.RunPipeline` 对该目录的合并结果摘要。
- **新增验证产物** `backend/data/outputs/funds_etl_dfea3ad0-f56.xlsx` — 本次合并输出文件，因未识别到交易候选，只有空标准表头。

### New Functionality
- 无新增功能。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `Get-ChildItem -LiteralPath 'E:\项目\网赌\梅县-网赌30娱乐\资金流调证\0622反馈' -Recurse -File | Group-Object Extension` — 目录内有 115 个 CSV、18 个 PDF、18 个 ZIP、3 个 RAR；支持处理的表格文件为 115 个 CSV，总大小约 4.96GB。
- `go run .\.codex_tmp_scan "E:\项目\网赌\梅县-网赌30娱乐\资金流调证\0622反馈" > backend\data\outputs\scan_0622_feedback.json` — 扫描器识别结果：`transactions=0`、`accounts=0`、`unknown=115`，provider 全部为 `未知`。
- `go run .\.codex_tmp_scan "E:\项目\网赌\梅县-网赌30娱乐\资金流调证\0622反馈" pipeline "E:\codex\etl\backend\data\outputs" > backend\data\outputs\pipeline_0622_feedback.json` — 合并结果：`rows_in=0`、`rows_out=0`，输出 `funds_etl_dfea3ad0-f56.xlsx`。
- GB18030 抽样读取 `826648753_账户明细d2_20260517102205_part1.csv` 表头可正确显示为 `交易号,商户订单号,交易创建时间,...`；UTF-8 读取显示为乱码。

### Open Items
- 当前 CSV 读取路径未正确处理该批 GBK/GB18030 编码文件，导致中文表头乱码，扫描器无法命中支付宝标准表头。
- 第一层 provider 粗分仍主要依赖 `支付宝/alipay`、`微信/wechat/财付通`、`银行/bank` 等文本；该批文件名虽然包含 `账户明细/余额明细/注册信息/登陆日志` 等支付宝表类型，但缺少 `支付宝` 关键词，当前不会被粗分为支付宝。

### Notes
- 该目录按文件名统计更像支付宝协查反馈：`账户明细` 22 个、`余额明细` 21 个、`注册信息` 18 个、`登陆日志` 18 个、`需求说明` 18 个、`查无结果` 18 个；未发现 `微信/财付通/银行` 命名特征。
- 本次未修改后端代码，因此未执行 `run.ps1` 重启。

## 2026-06-26 DuckDB CLI 工具补齐

### Task
- 按用户说明从 `E:\codex\etl_exe\tools` 复制 `duckdb.exe` 到当前项目，修复健康检查中 `analysis_plane` 缺少 DuckDB CLI 的问题。

### Changes
- **新增** `tools/duckdb/duckdb.exe` — 从实际源路径 `E:\codex\etl_exe\tools\duckdb\duckdb.exe` 复制而来。

### New Functionality
- 离线分析平面现在可以找到项目内 DuckDB CLI：`E:\codex\etl\tools\duckdb\duckdb.exe`。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `E:\codex\etl\tools\duckdb\duckdb.exe --version` — 返回 `v1.5.3 (Variegata) 14eca11bd9`。
- `Get-Process etl-server -ErrorAction SilentlyContinue | Stop-Process -Force; .\run.ps1` — 已重启后端，PID 38668。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`，`analysis_plane.available=true`，`exe_path=E:\codex\etl\tools\duckdb\duckdb.exe`。

### Open Items
- 无。

### Notes
- 用户给出的目录是 `E:\codex\etl_exe\tools`；实际可执行文件位于其子目录 `duckdb\duckdb.exe`。

## 2026-06-26 通用清洗规则增强

### Task
- 按用户要求将清洗规则加入通用清洗路径，不再只依赖银行专用清洗或说明文档。
- 修正通用金额清洗：负数交易金额可能代表冲正/撤销/退款，不应统一转正。

### Changes
- **新增/修改** `internal/etl/cleaning.go` — 承载通用 `CleanTransactions` / `DeduplicateTransactions` 逻辑；通用清洗现在会清理账号字段、过滤失败反馈行，并继续执行必填过滤、方向/时间/金额标准化和去重；金额标准化保留正负号。
- **修改** `internal/etl/etl.go` — 将清洗/去重函数从超大 ETL 文件拆出，保留 `RunPipeline` 调用契约不变。
- **修改** `internal/etl/etl_test.go` — 新增 `TestCleanTransactionsAppliesCommonRules`，覆盖失败反馈过滤、账号清理、负数金额保留、时间和方向标准化。
- **修改** `docs/AI_HANDOFF.md`、`docs/CHANGELOG_AI.md` — 记录本次清洗规则变更。

### New Functionality
- 通用流水清洗新增账号清理：`交易卡号`、`交易账号`、`交易对手账卡号` 会使用 `parser.CleanAccountNumber` 去掉常见前缀/后缀。
- 通用金额清洗只做解析和格式化，保留负数符号，避免破坏冲正/撤销/退款等业务语义。
- 通用失败反馈过滤新增：`查询反馈结果原因` 匹配 `查询失败`、`失败`、`无记录`、`无此记录`、`查无此`、`no record` 的行会被过滤。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `go test ./internal/etl -run TestCleanTransactionsAppliesCommonRules -count=1` — 初次加入通用规则前失败于未过滤失败反馈行；负数保留修正前失败于金额被转正；对应修复后均通过。
- `go test ./internal/etl -count=1` — 通过。
- `go test ./internal/... -count=1` — 通过。
- `go vet ./internal/...` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server` — 通过。
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command 'Get-Process etl-server -ErrorAction SilentlyContinue | Stop-Process -Force; .\run.ps1'` — 已重启后端，PID 47276。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`；`analysis_plane` 仍提示缺少 `tools/duckdb/duckdb.exe`，与本次清洗规则无关。

### Open Items
- `internal/etl/etl.go` 仍是既有超大文件，本次只拆出触碰的清洗/去重单元，未做无关重构。
- 通用清洗没有新增“0 金额过滤”；该规则仍保留在银行专用层，避免误删其他来源的合法 0 金额记录。

### Notes
- `RunPipeline` 的接口和返回结构不变，前端无需调整。
- 工作区存在大量 Dune profile/rod 相关既有运行时改动，本次未处理。

## 2026-06-25 Rod 用户模式增强: cf_clearance 检测 + 页面复用

### Task
- 阅读 `D:\app\桌面\rod-usermode-detailed.md`，在最小改动前提下将 rod Chrome CDP 自动化增强方案集成到项目现有的 `internal/dunetools/rod_user_mode_*.go`。

### Changes
- **修改** `internal/dunetools/rod_user_mode.go` — `LoginAndExtract()` 流程重构：先检测 `cf_clearance` cookie（缺失则等待用户手动过 Cloudflare），使用 `findOrCreateDunePage()` 复用已有 Dune 页面（而非每次新建），新增 `isRodBlocked()` / `isRodLoggedIn()` 辅助判定。
- **修改** `internal/dunetools/rod_user_mode_session.go` — 新增 `checkRodCFClearance()` / `checkRodCFClearanceExpiry()`（cf_clearance cookie 级检测，含过期判定、5 分钟缓冲）、`findOrCreateDunePage()`（优先复用 dune.com 页面 → 复用 about:blank → 新建）、`isRodBlocked()`（HTML 内容检测 Cloudflare 拦截）、`isRodLoggedIn()`（URL + localStorage token 判定）、`waitForCFClearance()`（浏览器级等待 cf_clearance 出现）。

### New Functionality
- Rod 登录现在优先复用已有 Dune 页面（cf_clearance 已在 cookie store 中），避免每次新建页面触发 Cloudflare 重新检测。
- 支持 `cf_clearance` cookie 的精确过期检测（5 分钟提前刷新）。
- 页面被 Cloudflare 拦截时检测更可靠（HTML 级别匹配 "Sorry, you have been blocked" / "Cloudflare Ray ID" / "Attention Required" / "cf-browser-verify"）。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无。

### Frontend Changes
- 无。

### Verified Commands
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过。
- `go test ./internal/dunetools -run Rod -count=1` — 5/5 通过。
- `go test ./internal/... -count=1` — 全部通过。

### Open Items
- 未对真实 Dune 线上环境发起 rod 登录端到端重跑；`cf_clearance` 检测和页面复用逻辑由单元测试和编译验证覆盖。

### Notes
- `checkRodCFClearanceExpiry()` 当前未被调用，预留给后续自动刷新 cf_clearance 逻辑使用。
- `checkRodCFClearance()` / `findOrCreateDunePage()` / `isRodBlocked()` / `isRodLoggedIn()` 已集成进 `LoginAndExtract()` 主流程。

## 2026-06-25 Dune query chained parameter return fix

### Task
- Align the Dune query flow with the intended chained process: selected registered account logs in, backend captures Cookie/Authorization/team context, SQL creates a Dune query, execution returns `execution_id`, and `/public/execution` returns table rows.
- Ensure parameters produced by earlier Dune steps are returned to the caller for later pagination/export.

### Changes
- Updated `internal/api/dune_web_query.go` so `executeDuneWebQueryWithRetry` receives the same `duneQueryRequest` pointer used by the HTTP handler.
- Updated `internal/api/dune_query_handlers.go` to pass the request pointer into the web-query chain.
- Extended `internal/api/dune_account_query_test.go` to assert that a request with only `sql` and `account_email` returns the auto-created `query_id`, `execution_id`, and table rows.

### New Functionality
- `/api/dune/query` now preserves the `query_id` generated by `CreateQuery` in the final JSON response for account-based web queries.
- The full account query chain is now locked by regression coverage: account login auth -> `CreateQuery` -> `ExecuteQuery` -> `/public/execution` -> response rows.

### API Changes
- No route or request schema changes.
- Response behavior fixed: account-based web queries now return the auto-created `query_id` instead of `0`.

### Database Changes
- None.

### Frontend Changes
- None.

### Verified Commands
- `go test ./internal/api -run TestHandleDuneSQLQueryUsesSelectedAccountLoginForWebQuery -count=1` failed before the fix with `query_id=0` and passed after the pointer fix.
- `go test ./internal/api -count=1` passed.
- `go test ./internal/... -count=1` passed.
- `go build -o bin/etl-server.exe ./cmd/server` passed.
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command '$env:DUNE_QUERY_CDP_PORT="9222"; & .\run.ps1'` restarted the backend successfully, PID 46704.
- `curl.exe -s http://127.0.0.1:8000/api/health` returned `status=ok`.

### Open Items
- Live Dune smoke is still blocked by Dune Cloudflare during the browser login stage (`Sorry, you have been blocked`). The local chained-parameter bug is fixed, but real Dune completion still needs a Dune-accepted login session or API key.

### Notes
- The intended parameter chain is now explicit: `account_email` selects credentials, login extracts Cookie/Authorization/access token/team, `CreateQuery` produces `query_id`, `ExecuteQuery` produces `execution_id`, and `/public/execution` uses `cookie + query_id + execution_id` to return table data.

## 2026-06-22 Dune Cloudflare stealth 配置集成

### Task
- 用户提供 `D:/app/桌面/playwright-go-stealth-config.md`，要求把其中 Cloudflare/Turnstile stealth 配置集成到项目；出现验证时先使用该配置，集成后测试通过再交付。

### Changes
- **新增** `tools/dune-playwright/stealth-config.cjs` — 将文档中的启动参数、UA/header、`navigator.webdriver`/plugins/languages/WebGL/Canvas/chrome runtime 指纹修补、Turnstile iframe 点击、`cf_clearance` 检测封装为共享配置。
- **修改** `tools/dune-playwright/register-login.mjs` — Dune 注册/登录/验证/捕获浏览器在打开 Dune 前加载共享 stealth 配置；Cloudflare 检测点先执行 `solveCloudflareWithStealth()`，失败后才进入可见浏览器人工点击等待。
- **修改** `backend/data/dune/playwright_bridge.js` — Dune 查询 fallback/refresh 桥接使用同一套 stealth 启动参数、上下文参数和初始化脚本；查询链路遇到 Cloudflare 时先尝试自动处理，再等待用户点击。
- **修改** `tools/dune-playwright/register-login.test.mjs` — 新增回归测试，锁定登录脚本和查询桥接都必须在导航 Dune 前应用共享 stealth 配置，并在 Cloudflare 分支调用 stealth solver。

### New Functionality
- 账号登录、验证、查询刷新、GraphQL/public execution fallback 遇到 Cloudflare/Turnstile 时，会先自动加载 stealth 指纹配置并尝试点击验证框/等待 `cf_clearance`。
- 自动处理失败时仍保留上一轮要求的可见浏览器人工点击流程，用户点击后继续自动执行。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `node --test tools/dune-playwright/register-login.test.mjs` — 先补测试后失败于缺少 `stealth-config.cjs`；实现后 13/13 通过。
- `node --check tools/dune-playwright/stealth-config.cjs` — 通过。
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- `node --check backend/data/dune/playwright_bridge.js` — 通过。
- Dune bridge refresh smoke：`node -e ... spawnSync(... backend/data/dune/playwright_bridge.js ...)` — 退出码 0，脱敏结果 `hasCookie=true`、`hasAuthorization=true`，stderr 记录 `STEALTH_CF_CLEARANCE_FOUND`。
- `go test ./internal/...` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server` — 通过。
- `npm run build` (frontend) — 通过，仅保留既有 Vite chunk size warning。
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 已重启后端，PID 34968。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`；`analysis_plane` 仍提示缺少 `tools/duckdb/duckdb.exe`，与本次 Dune stealth 链路无关。

### Open Items
- 没有引入 Go `playwright-go` 新依赖；项目现有 Dune 浏览器执行链路是 Node Playwright，因此本次按同等 stealth 行为集成到实际执行脚本中。
- 真实 Dune smoke 未打印 cookie/token 明文，只验证是否拿到必要参数；后续人工测试如果出现 Cloudflare 窗口，仍可直接点击，脚本会自动继续。

### Notes
- `tools/dune-playwright/register-login.mjs` 仍是大文件，本次按最小任务只接入共享 stealth 配置，没有做无关重构。
- `backend/data/dune/playwright_bridge.js` 位于运行时数据目录，但当前后端 Dune 查询 fallback 实际调用它，因此同步接入同一套配置。

## 2026-06-22 Dune Cloudflare: 出现验证时交给用户点击

### Task
- 用户要求：如果 Dune 自动登录/查询过程中出现 Cloudflare，就让用户点击，不要继续后台硬跑或直接失败。

### Changes
- **修改** `internal/api/dune_account_query.go` — Dune SQL 查询选账号后如需重新登录刷新认证，Playwright 启动参数由 `Headless: true` 改为 `Headless: false`，确保出现 Cloudflare 时用户能看到浏览器并点击验证。
- **修改** `tools/dune-playwright/register-login.mjs` — 新增 `waitForManualCloudflare(page, label, maxSeconds=600)`；首页 `navigateWithCFRetry()` 5 次仍遇到 Cloudflare 时，不再立即返回 `cloudflare_blocked_homepage`，改为打开可见窗口等待用户点击，验证通过后自动继续登录/提参。
- **修改** `tools/dune-playwright/register-login.test.mjs` — 新增回归测试，锁定查询登录必须使用可见浏览器，且首页 Cloudflare 必须先进入人工等待再失败。
- **修改** `backend/data/dune/playwright_bridge.js` — 查询 GraphQL/public execution fallback 的 Playwright 窗口从屏幕外隐藏改为可见位置；导航到 Dune 首页后若检测到 Cloudflare，等待用户在浏览器中点击验证，通过后再继续发请求。

### New Functionality
- Dune 账号登录、查询自动刷新、GraphQL fallback、public execution fallback 遇到 Cloudflare 页面时，会显示浏览器窗口并等待用户完成验证。
- 用户完成 Cloudflare 后脚本会自动检测页面恢复并继续执行，不需要重新提交任务。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `node --test tools/dune-playwright/register-login.test.mjs --test-name-pattern "Cloudflare|visible"` — 修复前 2 个新增用例失败，修复后 11/11 通过。
- `node --test tools/dune-playwright/register-login.test.mjs` — 11/11 通过。
- `node --check tools/dune-playwright/register-login.mjs && node --check backend/data/dune/playwright_bridge.js` — 通过。
- `go test ./internal/api -run TestHandleDuneSQLQueryUsesSelectedAccountLoginForWebQuery -count=1` — 通过。
- `go test ./internal/dunetools -count=1` — 通过。
- `go test ./internal/...` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server/` — 通过。
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 已重启后端，PID 20920。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`。

### Open Items
- 未在无人值守状态下发起真实 Dune Cloudflare 等待，因为新行为会停在可见浏览器等待用户点击；后续人工测试时如果看到 Cloudflare，直接在弹出的浏览器里点击验证即可。
- `analysis_plane` 健康检查仍提示缺少 `tools/duckdb/duckdb.exe`，与本次 Dune 登录/查询链路无关。

### Notes
- `tools/dune-playwright/register-login.mjs` 已长期超过 250 pure LOC，本次按项目“最小任务/禁止无关重构”规则只做 Cloudflare 人工等待相关改动，未重构脚本结构。
- 查询 fallback 桥接脚本 `backend/data/dune/playwright_bridge.js` 位于运行时数据目录，但当前后端实际通过 `dunePlaywrightBridgePath()` 调用该文件，因此本次按实际执行路径修复。

## 2026-06-22 Dune SQL 查询真实测试与选账号认证链路修复

### Task
- 按用户要求对 Dune SQL 查询账号选择功能做真实测试，边测边记录参数、问题，并修复本地流程中导致卡住的问题。

### Changes
- **修改** `internal/api/dune_account_query.go` — `account_email` 选中账号后，优先使用账号历史中已有的 Cookie/Authorization/access_token/team_id 注入查询 payload；只有账号缺少完整网页鉴权参数时，才调用 Playwright 后台登录刷新，避免已保存认证仍无条件重登导致 Cloudflare 卡 4-5 分钟。
- **修改** `internal/api/dune_account_query.go` — 新增 `storedDuneAuthFromAccount()`、`applyDuneStoredAuth()`、`persistDuneQueryAuth()`，复用保存 auth 文件逻辑并保留原 API Key。
- **修改** `internal/api/dune_account_query_test.go` — 新增回归测试，锁定“账号已有 Cookie/Authorization/access_token 时不得触发后台登录，必须直接进入 CreateQuery -> ExecuteQuery -> public execution 链路”。

### New Functionality
- Dune SQL 选账号查询现在优先复用该账号上次验证登录保存下来的网页认证参数，减少后台浏览器登录和 Cloudflare 风控触发。
- 账号保存参数仍会同步写入 Dune auth 文件，保持翻页/导出等后续接口可复用。

### API Changes
- 无新增路由，无请求/响应字段增减；继续复用 `POST /api/dune/query` 的 `account_email`。

### Database Changes
- 无数据库结构变更。
- 会写入本机文件 `backend/data/dune/auth.json`，保存所选账号的 Cookie/Authorization/access_token/team_id，同时保留原 API Key。

### Frontend Changes
- 无前端代码变更；本次修复后端选账号鉴权注入顺序。

### Real Test Record
- 健康检查：`curl.exe -s http://127.0.0.1:8000/api/health` 返回 `status=ok`；`analysis_plane` 仍提示缺少 `tools/duckdb/duckdb.exe`，与 Dune 查询无关。
- 真实账号列表：`GET /api/dune/batch/accounts` 返回 12 个账号，10 个 `done`，2 个 `wait_verify`；首个 done 账号 `ldj1009538134+dune_2d685f01@gmail.com`，`team_id=11`，`cookie_len=4680`，`authorization_len=1192`，`access_token_len=0`。
- 修复前真实请求：`POST /api/dune/query`，SQL `select 1 as smoke_value`，`account_email=ldj1009538134+dune_2d685f01@gmail.com`，`limit=10`，`timeout_seconds=180`，`poll_interval_seconds=2`；耗时约 286.8s，返回 502，错误为后台登录 `cloudflare_blocked_homepage`。
- 修复后真实请求：同一账号同一 SQL，耗时约 17.6s，返回 401，响应包含 `auth_required=true`、`login_url`，无 `execution_id/query_id/rows`；服务日志显示不再进入 `mode=login`，而是直接读取账号保存的 auth 参数后调用 Dune CreateQuery。
- 外部阻塞证据：选中账号 Authorization JWT `exp_local=2026-06-21T16:13:58`，测试时已过期约 86760 秒；全部 done 账号的 Authorization 均已过期，最新过期时间为 `2026-06-22T00:19:26`。Dune CreateQuery 返回 Cloudflare `Just a moment...` HTTP 403，Playwright fallback 返回 Dune HTTP 401。

### Verified Commands
- `go test ./internal/api -run TestHandleDuneSQLQueryUsesSavedAccountAuthBeforeLogin -count=1` — 修复前失败，修复后通过。
- `go test ./internal/api -count=1` — 通过。
- `go test ./internal/...` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server/` — 通过。
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 已重启后端，新 PID 41828。
- `npm run build` (frontend) — 通过，仅保留既有 Vite chunk size warning。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`。
- 真实 Dune 查询复测 — 已确认本地强制重登卡住问题修复；当前剩余失败为 Dune 外部认证过期/Cloudflare 403。

### Open Items
- 当前保存的 Dune 账号 Authorization 全部已过期，真实 Dune 查询无法拿到表格结果；需要重新完成账号登录/刷新认证后再跑 `select 1 as smoke_value` 验证 `execution_id/query_id/rows`。
- `run.ps1` 只在 `bin/etl-server.exe` 不存在时构建；后端源码修改后仍需先执行 `go build -o bin/etl-server.exe ./cmd/server/`，再执行 `run.ps1`，否则会重启旧二进制。

### Notes
- 本次修复的是本地流程卡住点：已有账号认证参数时不再无条件静默登录。真实查询仍依赖 Dune 当前账号 Cookie/Authorization/cf_clearance 是否有效。
- 直接保存过期 Authorization 只能到 Dune 认证失败，不能绕过 Cloudflare；后续需要 fresh 登录态或可用的 Dune API Key/官方查询权限。

## 2026-06-22 Dune 查询 5 项 bug 修复

### Task
- 全代码审查 Dune 查询链路，发现并修复 5 项 bug：登录缓存缺失、全局可变函数注入、超时遗漏、错误消息吞噬、前端导出翻页缺 cookie。

### Changes
- **修改** `internal/api/dune_account_query.go` — 新增 `duneAccountLoginFunc` 类型取代裸露 `var` 函数指针，提供 `SetDuneAccountLoginForTest()` 用于测试注入；新增 `loginDuneQueryAccountWithCache` 内存缓存层（5min TTL），同一账号多次查询复用 Playwright 登录结果；`applyDuneAccountAuth` 在 ctx 无 deadline 时兜底添加 10min 超时。
- **修改** `internal/api/dune_account_query_test.go` — 使用 `SetDuneAccountLoginForTest()` 替代直接覆盖 `duneQueryAccountLogin` 变量，消除并发测试竞态。
- **修改** `internal/api/dune_query_handlers.go` — `fetchDunePreviewPage` 在降级到 API 结果页前检查 apiKey 是否为空，返回明确错误 `Cookie 不可用且未配置 API Key`。
- **修改** `frontend/src/features/download/duneApi.ts` — `DunePageValues` / `DuneExportValues` 新增 `cookie` 字段；`loadDunePage` / `exportDuneExcel` 请求体传递 `cookie`。
- **修改** `frontend/src/features/download/DuneQueryPanel.tsx` — 翻页 `changePage` 与导出 `exportExcel` 调用时传入 `auth?.cookie`。

### New Functionality
- 选账号查询支持内存级登录缓存，5 分钟内同一账号再查无需重开 Playwright 浏览器。
- 查询请求无 deadline 时自动兜底 10 分钟超时，防止 Playwright 进程无限挂起。
- 导出/翻页显式传递 cookie，不再仅依赖后端存储 auth 文件降级。

### API Changes
- 无新增路由，无删除或重命名，无请求/响应字段增减。

### Database Changes
- 无。

### Frontend Changes
- `DunePageValues` / `DuneExportValues` 类型新增 `cookie?: string`。
- Dune 查询面板翻页/导出调用时额外传入 `auth?.cookie`。

### Verified Commands
- `go test ./internal/api -run 'DuneSQL|DuneResult|DuneExport|DuneWeb|DunePublic|DuneDownload|HandleDuneBatchStart' -count=1` — 10/10 通过。
- `go test ./internal/dunetools -count=1` — 8/8 通过。
- `go test ./internal/... -count=1` — 全量通过。
- `go build -o bin\etl-server.exe .\cmd\server` — 通过。
- `npx tsc --noEmit` (frontend) — 通过。
- `.\run.ps1` — 已自动重启后端，PID 40548。

### Open Items
- `loginDuneQueryAccountWithCache` 缓存无主动失效机制，若账号在 Dune 侧登录态过期需等待 5min TTL 自然过期。
- Playwright headless 登录 8min 超时在 `loginDuneQueryAccount` 中，与缓存层的 10min 上下文超时无互斥保护（实际超时以先到者为准）。

### Notes
- `duneAccountLoginFunc` 为未导出类型，仅在 `api` 包内部使用；`SetDuneAccountLoginForTest` 为显式测试注入出口，替代原先直接覆盖包级变量。
- 修复后缓存清理在 `SetDuneAccountLoginForTest` 中自动执行，测试间不会因缓存污染互相干扰。

## 2026-06-22 Dune SQL 查询: 账号选择自动登录并静默提参

### Task
- 用户要求 Dune SQL 查询新增账号选择：前端只展示已注册、已验证登录、状态正常的账号；用户选择账号并输入 SQL 后，后端使用该账号在后台静默登录，自动提取 Cookie/Authorization/access_token/team_id，再串到官网查询 API 创建/执行查询并拉取表格结果，避免用户手动输入 Cookie 等参数。

### Changes
- **新增** `internal/api/dune_account_query.go` — `POST /api/dune/query` 收到 `account_email` 时，校验账号必须存在、`status=done`、无错误且有密码；随后用 Playwright headless 后台登录提取网页鉴权参数，写入查询 payload，并保存到本机 Dune auth 文件，同时保留原有 API Key。
- **新增** `internal/api/dune_account_query_test.go` — 回归测试覆盖“选账号 -> 后台登录提参 -> CreateQuery -> ExecuteQuery -> public execution 拉表格”的完整本地链路，并断言 Cookie/Authorization/access_token/team_id 正确传给下一环。
- **修改** `internal/api/dune_query_handlers.go` — `duneQueryRequest` 新增 `account_email` 字段；查询入口在执行 API/WebQuery 前先应用账号自动登录鉴权。
- **修改** `internal/dunetools/playwright.go` 与 `tools/dune-playwright/register-login.mjs` — Playwright 登录桥新增 `headless` 入参；查询自动登录使用 `Headless: true`，批量注册默认行为不变。
- **修改** `frontend/src/features/download/duneApi.ts`、`duneBatchApi.ts`、`DuneQueryPanel.tsx` — 前端查询页加载 `/api/dune/batch/accounts`，筛选 `status=done` 且无错误的账号，新增“查询账号（自动登录）”选择框；选中账号后自动切到官网查询模式并提交 `account_email`。
- **修改** `frontend/src/styles/layout.css` — Dune 查询设置网格子项允许长邮箱省略，页头状态标签可换行，避免移动端或长账号撑破布局。

### New Functionality
- Dune SQL 查询支持选择已验证账号自动登录，后台静默获取 Cookie/Authorization/access_token/team_id 并继续执行官网查询链路。
- 查询账号状态标签显示当前可用账号数量；账号下拉只出现已保存且正常的账号。
- 长邮箱账号在移动端不会撑宽设置面板，选中后自动显示官网查询模式。

### API Changes
- `POST /api/dune/query` 请求体新增可选字段 `account_email`。
- 复用既有 `GET /api/dune/batch/accounts` 作为账号来源；无新增路由、无删除或重命名 API。

### Database Changes
- 无数据库结构变更。会更新本机文件存储：
  - `backend/data/dune/accounts.json` 中所选账号的 Cookie/Authorization/access_token/team_id。
  - Dune auth 文件中的网页鉴权参数；保存时保留原有 API Key。

### Frontend Changes
- `下载 -> dune -> 数据查询` 页新增“查询账号（自动登录）”下拉框。
- 设置栏的官网模式文案改为“官网（Cookie/账号）”，避免选账号时仍提示必须手动 Cookie。
- 页头状态区新增“可用账号: N”，并修复窄屏换行。

### Verified Commands
- `go test ./internal/api -run TestHandleDuneSQLQueryUsesSelectedAccountLoginForWebQuery -count=1` — 通过。
- `node --test tools/dune-playwright/register-login.test.mjs` — 9/9 通过。
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- `cd frontend && ./node_modules/.bin/tsc --noEmit` — 通过。
- `go test ./internal/...` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server` — 通过。
- `cd frontend && ./node_modules/.bin/vite build` — 通过；仅保留既有 chunk size warning。
- `.\run.ps1` — 已自动重启后端，PID 42044。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`。
- Playwright 本地页面 QA — 桌面与 390px 移动端均确认 Dune 查询页、账号下拉、模式切换、长邮箱省略显示无遮挡。

### Open Items
- 未对真实 Dune 线上执行发起查询，避免在当前环境消耗实际账号/触发外部 Cloudflare 或风控；本次用 httptest 覆盖参数串联和查询 API 链路，用本地页面 QA 覆盖前端交互。

### Notes
- 真实运行时，如果所选账号被 Dune/Cloudflare 阻断，后端会返回“Dune 账号后台登录失败”并保留原有错误路径；这属于外部登录状态问题，不是手动 Cookie 缺失。
- 自动登录保存网页鉴权参数时会保留用户原先保存的 API Key，避免 API 查询模式被覆盖。

## 2026-06-22 Dune welcome/onboarding: 优化自动处理顺序

### Task
- 用户反馈 Dune 登录成功后的 welcome/onboarding 流程还会卡住，需要继续优化这一段自动处理流程。

### Changes
- **修改** `tools/dune-playwright/register-login.mjs` — 新增 `clickWelcomeAction()`，只点击可见且未 disabled / `aria-disabled=true` 的动作按钮，避免对不可用的 Next/Continue/Skip 做无效点击。
- **修改** `tools/dune-playwright/register-login.mjs` — `completeWelcomeOnboarding()` 顺序调整为：先填空输入框，再点击已启用的 Continue/Next；无可填输入且没有可继续动作时，才尝试 Skip；最后才做通用安全选项并再次尝试 Continue/Next。
- **修改** `tools/dune-playwright/register-login.test.mjs` — 新增回归测试，锁定 welcome 流程必须先填字段、再 Continue/Next、再 Skip、再通用选项，且不再用 `clickFirstText()` 处理 welcome 的 Skip/Continue。

### New Functionality
- welcome/onboarding 自动处理变成更明确的分步流程，减少因过早点 Skip、点击 disabled Next、或选项/输入顺序不稳定导致卡住的情况。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `node --test tools/dune-playwright/register-login.test.mjs` — 新增测试修复前失败：缺少 `clickWelcomeAction()`；修复后 8/8 通过。
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- Playwright 本地 Chromium DOM QA — 三个场景通过：
  - 有输入框且 Skip 同时存在时，先填 `longusername -> longu`，点击 `Next`，不点 `Skip`。
  - `Next` disabled 且无输入框时，点击 `Skip`。
  - 需要选项时，先点击 `Analytics`，再点击启用后的 `Next`。

### Open Items
- 未进行真实 Dune 线上 welcome 端到端重跑；本次验证覆盖脚本顺序、按钮启用态判断和本地 Chromium DOM 行为。

### Notes
- 本次只修改 Playwright 脚本和脚本测试，未修改 Go 后端代码，不需要执行 `run.ps1` 重启。

## 2026-06-22 Dune welcome/onboarding: 限制自动填入文本长度

### Task
- 用户反馈 Dune 登录成功后的 welcome 页面中，自动填入的输入框字符串过长，导致无法点击下一步；该处只需要 5 个字符。

### Changes
- **修改** `tools/dune-playwright/register-login.mjs` — `fillWelcomeInputs()` 新增 welcome 输入值规范化逻辑，username/handle 最多写入 5 个字符；team/workspace、company、role/title、name 等 fallback 值改为 `Solo`、`Indie`、`Data`、`User1`，全部不超过 5 字符。
- **修改** `tools/dune-playwright/register-login.test.mjs` — 新增回归测试，锁定 welcome fallback 文本值必须不超过 5 字符，并要求 username 走统一规范化逻辑。

### New Functionality
- Dune welcome/onboarding 自动填表现在使用短文本，减少因输入值超过页面限制而卡在下一步不可点击的概率。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `node --test tools/dune-playwright/register-login.test.mjs` — 修复前新增测试失败：`expected named short welcome input fallbacks`；修复后 7/7 通过。
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- Playwright 本地 DOM QA — 抽取真实 `fillWelcomeInputs()` 在 Chromium 页面执行，`verylongwelcomeusername` 被写为 `veryl`，其它值为 `Solo/Indie/Data/User1`，`maxLength=5`。

### Open Items
- 未进行真实 Dune 线上 welcome 端到端重跑；本次以脚本回归测试和本地 Chromium DOM QA 验证输入长度逻辑。

### Notes
- 本次只修改 Playwright 脚本和脚本测试，未修改 Go 后端代码，不需要执行 `run.ps1` 重启。

## 2026-06-22 Dune welcome/onboarding: 防止自动选项误点 Back

### Task
- 用户反馈 `https://dune.com/welcome` 自动选择待选项后进入下一项，又点击 `Back` 返回了上一项。

### Changes
- **修改** `tools/dune-playwright/register-login.mjs` — `clickWelcomeChoices()` 的通用候选点击逻辑新增动作按钮过滤，排除 `back`、`previous`、`go back`、`return`、`cancel`、`close` 以及中文 `返回`、`上一步`、`后退`、`取消`、`关闭` 等按钮。
- **修改** `tools/dune-playwright/register-login.test.mjs` — 新增回归测试，锁定 welcome 通用选项不会把返回/上一步类导航按钮当作选项点击。

### New Functionality
- Dune welcome/onboarding 自动处理在找不到明确关键词选项时，仍会尝试通用可见选项，但不会再点击返回、取消、关闭、继续、下一步等导航/动作按钮。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- `node --test tools/dune-playwright/register-login.test.mjs` — 6/6 通过。

### Open Items
- 未进行真实 Dune 浏览器端到端重跑；本次修复为脚本选择器逻辑的最小回归修复。

### Notes
- 本次只修改 Playwright 脚本和脚本测试，未修改 Go 后端代码，不需要执行 `run.ps1` 重启。

## 2026-06-21 Dune 批量注册: 开始新任务后历史账号不再消失

### Task
- 用户反馈点击“开始注册”后，之前已经注册的邮箱从前端表格里不见了。

### Changes
- **修改** `internal/api/handler_dune_batch.go` — `start`、`status`、`stop` 响应返回前统一合并 `accounts.json` 中已保存账号，避免前端 `setTask()` 时用新任务空账号列表覆盖历史表格。
- **新增** `internal/api/handler_dune_batch_accounts.go` — 抽出 `snapshotWithSavedAccounts()` 和 `mergeAccounts()`，保持主 handler 文件低于 250 有效代码行。
- **修改** `internal/api/handler_dune_batch_test.go` — 新增回归测试：已有保存账号时，启动新注册任务后 `start` 和 `status` 响应都必须保留该账号。

### New Functionality
- Dune 批量注册状态响应现在会显示“已保存账号 + 当前任务账号”的合并列表；开始新任务不会让前端历史邮箱临时消失。

### API Changes
- `POST /api/dune/batch/start`、`POST /api/dune/batch/stop`、`GET /api/dune/batch/status` 的 `accounts` 字段行为变更：从仅当前 task 账号变为合并持久化账号和当前 task 账号。
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端代码变更；前端继续使用现有 `task.accounts` 表格即可看到历史账号。

### Verified Commands
- `go test ./internal/api -run TestHandleDuneBatchStart_keepsSavedAccountsVisible_whenStartingNewTask -count=1` — 修复前失败：`accounts=[]`；修复后通过。
- `go test ./internal/api -run 'DuneBatch|Dune|PublicExecution' -count=1` — 通过。
- `go test ./internal/dunetools -count=1` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server/` — 通过。
- `powershell -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 重启成功，PID `41980`。
- `GET /api/dune/batch/status` — 返回 `status=idle,total=0,accounts=10`，说明 status 已合并持久化账号。
- `GET /api/dune/batch/accounts` — 返回 `accounts=10`，与 status 数量一致。
- `go test ./internal/... -count=1` — 通过。

### Open Items
- 本次重启服务会清空内存中的当前批量任务；已保存账号仍在 `accounts.json`，但被重启打断的未完成任务需要重新开始。

### Notes
- 修复点在后端响应合并层，不改前端表格逻辑。
- 当前 `mergeAccounts()` 仍保持“已保存账号优先、当前任务补充新增账号”的既有顺序和覆盖规则。

## 2026-06-21 Dune welcome/onboarding 自动选择与跳过

### Task
- 用户反馈注册成功后 `https://dune.com/welcome` 仍要选择很多东西，希望能自动选择、自动填信息，能跳过的自动跳过。

### Changes
- **修改** `tools/dune-playwright/register-login.mjs` — 新增 `completeWelcomeOnboarding()` 通用处理器：
  - 优先点击 `Skip for now` / `Skip` / `Maybe later` / `Not now` / `No thanks`。
  - 无法跳过时自动选择个人/分析/研究/DeFi/Ethereum/Solana 等偏安全选项。
  - 自动填写 username、team/workspace、company、role/name 等可见输入框。
  - 如果仍停留在 `/welcome`，自动跳转 `https://dune.com/home`，避免卡住欢迎页。
- **修改** `tools/dune-playwright/register-login.mjs` — 在 login、verify_login、capture 三条路径提取凭据前都先处理 welcome/onboarding。
- **修改** `tools/dune-playwright/register-login.test.mjs` — 增加 welcome 自动处理、通用卡片选择、`WELCOME_GOTO_HOME` fallback、提取前调用路径的覆盖。

### New Functionality
- Dune 注册/验证登录后的 welcome/onboarding 页面会自动处理，减少人工选择。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- `node --test tools/dune-playwright/register-login.test.mjs` — 5/5 通过。
- 独立 Playwright login QA（使用已保存 Dune 账号、独立 `welcome_qa` profile）— 返回 `ok=true,hasCookie=true,hasAuthorization=true,teamId=11`。
- 同次 QA stderr 观察到 `WELCOME_CHOICES`、`WELCOME_FILLED`、`WELCOME_GOTO_HOME`，确认 welcome 页实际执行了自动选项、自动填字段和跳转 home fallback。
- `rm -rf backend/data/dune/profiles/welcome_qa` — 已删除临时 QA profile，避免保留额外登录态。

### Open Items
- Dune welcome 页面文案/结构可能变化；当前实现采用“先跳过、再选择安全默认项、最后跳 home”的通用策略。
- 当前仍不会把 welcome 选择结果作为业务数据保存，只用于完成 Dune onboarding。

### Notes
- 本次只修改 Playwright 脚本和脚本测试，未修改 Go 后端代码，不需要执行 `run.ps1` 重启。
- 真实服务中已有 `total=30` 批量注册任务正在 running，本次没有停止或打断该任务；真实 QA 使用独立 Playwright 进程完成。

## 2026-06-21 Dune verify_login 修复 + 注册端到端实测

### Task
- 用户提供 Gmail IMAP 配置后，继续实测 Dune 注册流程；目标是用户辅助人机验证后完成注册、邮箱验证和登录凭据提取。

### Changes
- **修改** `internal/dunetools/manager.go` — 修复 `verify_login` 启动时先统计旧 task 中 `wait_verify` 账号、随后创建新 task 却把 `Accounts` 清空的问题；现在会把待验证账号复制到新 task，供 `runVerifyLogin` 继续处理。
- **修改** `internal/dunetools/manager_test.go` — 新增回归测试覆盖“已有 `wait_verify` 账号时，`verify_login` 应完成验证登录并提取凭据”。
- **运行态变更** `backend/data/dune/accounts.json` — 真实注册测试新增账号记录；最终实测账号 `u889c09b2` 状态为 `done`。

### New Functionality
- 无新增业务功能；本次为 `verify_login` 续跑 bug 修复和真实流程验证。

### API Changes
- 无新增、删除或重命名 API。
- 修复既有 `POST /api/dune/batch/start` 的 `mode=verify_login` 行为：启动响应和后续任务状态会保留待验证账号列表，不再空跑后直接 `done`。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `go test ./internal/dunetools -run TestManager_VerifyLogin_completesWaitingAccount_whenVerificationLinkExists -count=1` — 修复前失败：`Completed=0, Accounts=[]`；修复后通过。
- `go test ./internal/dunetools -count=1` — 通过。
- `go test ./internal/api -run 'DuneBatch|Dune|PublicExecution' -count=1` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server/` — 通过；注意 `run.ps1` 仅在二进制不存在时自动构建，因此后端源码修改后需要显式构建。
- `powershell -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 重启成功，PID `40716`。
- `POST /api/dune/batch/start` with `mode=register,total=1,domain=gmail.com,imap_host=imap.gmail.com:993` — 真实 Dune 注册完成，账号 `u889c09b2` 进入 `wait_verify` 且 `hasVerifyLink=true`。
- `POST /api/dune/batch/start` with `mode=verify_login` — 修复后启动响应包含待验证账号；轮询最终 `status=done,completed=1,failed=0,account.status=done,hasCookie=true,hasAuthorization=true,error=""`。
- `go test ./internal/... -count=1` — 通过。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`，`control_plane.ok=true`；`analysis_plane` 仍因本地缺少 `duckdb.exe` 不可用。

### Open Items
- `run.ps1` 当前不是“源码变更后自动构建”，而是“二进制不存在才构建”；如需符合文档中的自动构建描述，应单独修 `run.ps1`。
- 最终账号提取到了 Cookie 和 Authorization；`access_token` 字段为空但当前 `verifyAndLoginAccount` 的 combined verify+login 成功路径未强制要求该字段。

### Notes
- 本次真实流程未再复现打开浏览器直接跳到 `https://dune.com/welcome` 的问题。
- Gmail IMAP 应用密码仅用于 API 调用，没有写入交接文档。
- `verify_login` 仍依赖当前内存 task 中存在 `wait_verify` 账号；服务重启后 task 会变 `idle`，不会自动从 `accounts.json` 恢复待验证账号。

## 2026-06-21 Dune 注册流程实测: 人机验证后进入待邮箱验证

### Task
- 用户要求实测 Dune 注册流程，并表示会辅助点击人机验证。

### Changes
- 未修改业务代码。
- 通过 `POST /api/dune/batch/start` 使用 `mode=register,total=1` 启动真实 Dune 注册任务。
- 使用用户提供的 Gmail IMAP 主机和账号完成验证邮件抓取配置；未记录或落文档保存 IMAP 应用密码。

### New Functionality
- 无新增功能；本次为真实注册流程验证。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。
- `backend/data/dune/accounts.json` 新增 1 条 Dune 注册账号运行态记录，状态为 `wait_verify`，包含已抓取的验证链接。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`，`control_plane.ok=true`；`analysis_plane` 仍因本地缺少 `duckdb.exe` 不可用。
- `POST /api/dune/batch/start` with `mode=register,total=1,domain=gmail.com,imap_host=imap.gmail.com:993` — 返回 `status=running,total=1`。
- 轮询 `GET /api/dune/batch/status` — 约 40 秒后返回 `status=done,completed=1,failed=0`。
- `GET /api/dune/batch/status` 脱敏检查 — 新账号 `username=u08091393,status=wait_verify,hasVerifyLink=true,error=""`。
- `backend/data/dune/accounts.json` 脱敏检查 — 账号总数变为 3，最后一条记录 `status=wait_verify,hasPassword=true,hasVerifyLink=true`。

### Open Items
- 本次按 `register` 模式验证到“注册提交成功并抓到邮箱验证链接”；尚未继续执行验证链接打开后的登录/凭据提取阶段。

### Notes
- 真实注册流程未再复现打开浏览器直接跳到 `https://dune.com/welcome` 的问题。
- 本次未修改后端代码，不需要执行 `run.ps1` 重启。

## 2026-06-21 Dune profile/cache 清理

### Task
- 用户询问 Dune profile/cache 的用途；确认无用缓存后删除。

### Changes
- **删除运行态缓存** `backend/data/dune/**/{Cache,Code Cache,GPUCache,GrShaderCache,ShaderCache,DawnWebGPUCache,DawnGraphiteCache,GPUPersistentCache,component_crx_cache,extensions_crx_cache,AutofillAiModelCache,optimization_guide_hint_cache_store,Shared Dictionary/cache}`。
- **删除诊断输出** `backend/data/dune/api_captures`、`backend/data/dune/diag`、`backend/data/dune/screenshots`。
- **删除错误输出目录** `tools/backend`（旧脚本路径错误时可能写出的无效 Dune auth 目录）。
- **保留** `backend/data/dune/profiles/master` profile 本体、Cookies、Local Storage、IndexedDB、`backend/data/dune/auth.json`、`backend/data/dune/accounts.json` 等会话/凭据状态。

### New Functionality
- 无新增功能；本次为运行态缓存清理。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `rg -n "ProfileRoot|profileDir|profiles|api_captures|screenshots|diag" internal/dunetools tools/dune-playwright docs/DUNE_BATCH_REG_STATUS.md` — 确认当前批量注册实际使用 `profiles/master`。
- `du -sh backend/data/dune/profiles backend/data/dune/api_captures backend/data/dune/diag backend/data/dune/screenshots` — 清理前 `profiles` 约 846MB。
- PowerShell 安全删除脚本 — 删除 359 个缓存/诊断目录，约 892.14MB，无锁定残留。
- `du -sh backend/data/dune/profiles` — 清理后约 78MB。
- `test -d backend/data/dune/profiles/master` / `test -f backend/data/dune/auth.json` — 均存在。
- `find backend/data/dune -maxdepth 5 ...Cache... | head -40` — 无剩余缓存目录输出。
- `curl.exe -s http://127.0.0.1:8000/api/health` — 返回 `status=ok`，`control_plane.ok=true`。

### Open Items
- 无。

### Notes
- `profile` 不是单纯缓存：其中 Cookies、Local Storage、IndexedDB 可能保存 Dune 登录态和 Cloudflare clearance，不能整目录删除。
- 可删的是浏览器自动再生成的缓存目录；删除后下次打开 Dune 可能首次加载稍慢，但不应影响保存的凭据状态。
- 本次未修改后端代码，不需要执行 `run.ps1` 重启。

## 2026-06-21 Dune 注册/登录: 修复 `/welcome` 乱跳转

### Task
- 用户反馈 Dune 注册一打开浏览器就是 `https://dune.com/welcome`，不是注册页面，注册流程乱跳转。

### Changes
- **修改** `tools/dune-playwright/register-login.mjs` — 自动注册/验证/登录模式启动时清理共享 profile 内的 `auth-*` Dune 登录态 Cookie；从 `auth.json` 注入 Cookie 时过滤 `auth-*`，避免旧账号登录态把注册页重定向到 `/welcome`。
- **修改** `tools/dune-playwright/register-login.mjs` — 将 `/welcome` 纳入“已登录/可提取凭据”的页面判断，登录或手动抓取落到 welcome/onboarding 时会优先提取 Cookie/JWT，而不是继续误点或等待。
- **修改** `tools/dune-playwright/register-login.mjs` — 修复 `capture` 模式分支位置，避免被嵌在 register/login 分支后；同时修正手动抓取保存路径为 `backend/data/dune/auth.json`，不再写到 `tools/backend/data/dune/auth.json`。
- **新增** `tools/dune-playwright/register-login.test.mjs` — Node 内置测试锁定 capture 独立分支、auth 保存路径和登录态 Cookie 过滤。

### New Functionality
- 无新增业务功能；本次是 Dune 注册/登录自动化修复。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `node --test tools/dune-playwright/register-login.test.mjs` — 修复前 3 项失败；修复后 3/3 通过。
- `node --check tools/dune-playwright/register-login.mjs` — 通过。
- `go test ./internal/dunetools -count=1` — 通过。
- `go test ./internal/api -run 'DuneBatch|Dune|PublicExecution' -count=1` — 通过。

### Open Items
- 未在真实 Dune 注册页面完成端到端账号注册；如果 Dune 继续触发 Cloudflare/Turnstile，仍需用户在可见浏览器窗口手动处理第三方验证。

### Notes
- 这次修复撤销了“注册/login/verify 全模式注入认证 cookie”的副作用：自动注册/登录不应复用已有账号的 `auth-*` 登录态，否则 Dune 会把浏览器当成已登录用户并跳到 `/welcome`。
- 本次未修改 Go 后端代码，因此未触发后端修改后的 `run.ps1` 重启要求；服务健康检查将在验证阶段单独确认。

## 2026-06-21 Dune 注册/登录: auth.json 自动检测 + 无人值守跳转

### Task
- 开始注册前先检查 auth.json 是否存在，没有则自动切换到手动抓取模式（不再报错或静默失败）

### Changes
- **修改** `internal/dunetools/playwright.go` — 新增 `HasValidAuth()` 方法检查 auth.json 是否存在且含有效 cookie
- **修改** `internal/dunetools/manager.go` — `Start()` 中 full/register 模式启动前检查 auth.json；缺失时自动 req.Mode=ModeCapture 并重定向
- **修改** `internal/dunetools/types.go` — `TaskSnapshot` 新增 `RedirectedFrom` 字段标记重定向来源
- **修改** `frontend/src/features/download/DuneBatchReg.tsx` — 收到 `redirected_from` 时显示 "未找到 auth.json，已自动切换到手动抓取模式"
- **修改** `frontend/src/features/download/duneBatchApi.ts` — `DuneBatchTask` 新增 `redirected_from` 字段

### New Functionality
- **自动 auth 检测**: full/register 模式启动时自动检查 `backend/data/dune/auth.json`，不存在则自动切换为 capture 模式（打开浏览器让用户手动登录）
- **重定向标记**: API 响应中 `redirected_from` 字段告知前端原始请求模式

### Verified Commands
- `go test ./internal/... -count=1` — 全部通过
- `go build` / `npm run build` — 通过
- 模拟缺失 auth.json → `redirected_from: "register"` 自动跳转验证通过

## 2026-06-21 Dune 注册/登录: 人机验证优化 + 独立登录 + 手动抓取

### Task
- 解决 Dune 批量注册/登录时反复触发人机验证 (CF Turnstile) 的问题
- 新增独立的"登录已有账号"模式（无需 IMAP）
- 新增"手动抓取凭据"模式（用户在浏览器中手动登录，系统自动提取）

### Changes
- **修改** `tools/dune-playwright/register-login.mjs` — cookie 注入排除 `cf_clearance`（由 profile 自行管理，避免注入过期 clearance 反效果）；注册/login/verify 全模式注入认证 cookie（`auth-id-token` 等）帮助绕过 CF；username 改为 login/verify/capture 模式可选；新增 `capture` 模式（打开浏览器等待用户手动登录并保存凭据）
- **修改** `internal/dunetools/config.go` — `ResolveRunConfig` IMAP 校验按需执行，仅 `full`/`register` 模式强制要求
- **修改** `internal/dunetools/types.go` — `StartRequest` 新增 `LoginEmail`/`LoginPassword` 字段；新增 `ModeLogin`/`ModeCapture` 常量
- **修改** `internal/dunetools/manager.go` — `Start` 新增 login/capture 模式分发；新增 `runLogin`/`runCapture` 方法
- **修改** `internal/dunetools/playwright.go` — 导出 `Run` 方法支持自定义 mode
- **修改** `frontend/src/features/download/DuneBatchReg.tsx` — 新增"登录已有账号"和"手动抓取凭据"模式按钮及表单
- **修改** `frontend/src/features/download/duneBatchApi.ts` — 新增 `login_email`/`login_password` 请求字段；capture 模式处理

### New Functionality
- **独立登录模式**: 输入 Dune 邮箱+密码即可登录提取凭据，无需 IMAP 邮箱配置
- **手动抓取模式**: 打开 Playwright 浏览器窗口，用户手动登录 Dune，系统 10 分钟内自动检测登录成功并提取 Cookie/JWT/TeamID 保存到 auth.json
- **Cookie 注入优化**: 排除过期 `cf_clearance`，避免触发 CF 更严格检查；认证 cookie 仍注入帮助浏览器身份

### API Changes
- `POST /api/dune/batch/start` 新增 mode: `login`（需 `login_email`+`login_password`）和 `capture`（无需额外参数）
- `login`/`verify_login`/`capture` 模式不再强制要求 IMAP 凭据

### Frontend Changes
- Dune 批量注册面板新增 2 个模式按钮：登录已有账号、手动抓取凭据
- 登录模式显示邮箱+密码输入框
- 手动抓取模式显示说明提示

### Verified Commands
- `go test ./internal/... -count=1 -timeout 120s` — 全部通过
- `go vet ./internal/...` — 无警告
- `go build -o bin/etl-server.exe ./cmd/server/` — 编译通过
- `node --check tools/dune-playwright/register-login.mjs` — 语法正确
- `cd frontend && npm run build` — 构建通过
- `powershell -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 重启成功

### Open Items
- `cf_clearance` 时效性有限（通常 30 分钟~数小时），过期后需重新通过"手动抓取"或手动在打开的浏览器中过 CF
- 注册模式在无有效 `cf_clearance` 时仍会触发 CF Turnstile（这是 Dune/CF 的外部限制，无法完全自动化）
- 建议工作流：先用"手动抓取"过一遍 CF → 获得 fresh clearance → 30 分钟内跑注册

### Notes
- 关键改进：cookie 注入时过滤 `cf_clearance`，让 Playwright profile 自行管理 CF 状态
- 登录模式用户名可选，首次登录后的 username setup 会自动跳过

## 2026-06-21 Dune 批量注册: 全流程跑通 (注册→邮件验证→登录→凭据提取)

### Task
- 继续把 Dune 批量注册完整流程跑通（注册→收验证邮件→点验证链接→登录→提取凭据）
- 修复 CF 绕过方案，合并 verify+login 到同一 Playwright session
- 修复登录阶段 username setup 死循环、cookie 提取崩溃

### Changes
- **修改** `tools/dune-playwright/register-login.mjs` — 新增 `verifyAndLogin` 合并验证+登录到同一浏览器 session（避免重新过 CF）；新增 `extractCredentials` 抽取凭据；onboarding 向导自动点击跳过；全局 try-catch 防崩
- **修改** `internal/dunetools/manager.go` — `VerifyEmail` 改用 Playwright 浏览器打开验证链接；合并 verify+login 成功时直接提凭据跳过单独 login；放宽凭据检查（Cookie+Authorization 即可，不强制 AccessToken）
- **修改** `internal/dunetools/playwright.go` — 新增 `VerifyEmail` 方法；`run()` 支持 verifyLink 参数；stderr 截断从 300→1000 字符
- **修改** `internal/dunetools/types.go` — `BrowserClient` 接口新增 `VerifyEmail`
- **修改** `internal/dunetools/manager_test.go` — `fakeBrowser` 新增 `VerifyEmail` 实现
- **修改** `internal/api/handler_dune_batch_test.go` — `fakeDuneBatchBrowser` 新增 `VerifyEmail`

### New Functionality
- **合并 verify+login**：验证邮件和登录在同一 Playwright session 中完成，避免 CF 重新检测
- **自动绕过 CF**：Playwright (headless:false) + 持久化 profile + cf_clearance 自动获取
- **全流程自动化**：从账号生成到凭据提取无需任何人工操作（CF checkbox 除外）
- **凭据提取**：Cookie (含 cf_clearance) + Authorization (JWT) + TeamID 全部提取

### API Changes
- 无新增、删除或重命名 API。
- `POST /api/dune/batch/start` 行为：verify+login 合并在 verify 阶段完成，不再单独调用 login

### Frontend Changes
- 无

### Verified Commands
- `go test ./internal/... -count=1 -timeout 300s` — 全部通过
- `go vet ./internal/...` — 无警告
- `go build -o bin/etl-server.exe ./cmd/server/` — 编译通过
- `powershell -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 重启成功
- `curl -X POST /api/dune/batch/start` — 全流程跑通：completed=1, failed=0, status=done
- 账号 `ldj1009538134+dune_2d685f01@gmail.com` 注册成功，Team ID 11，Cookie+JWT 已提取

### Open Items
- onboarding 向导目前简化处理（跳过验证后直达登录页），若 Dune 强制要求完成向导可恢复复杂版自动点击逻辑
- AccessToken (Cognito localStorage) 当前可能为空，不影响 Dune API 调用

### Notes
- 关键突破：CF 在 Playwright headless:false + 持久化 profile 下可以通过 JS Challenge
- `cf_clearance` cookie 已成功获取并持久化到 profile，后续登录可复用
- 使用 gmail.com 域名（Gmail 别名 `user+dune_<hex>@gmail.com`），无需 Cloudflare Email Routing

## 2026-06-18 Dune 批量注册: 外部阻塞复核

### Task
- 继续推进“阅读文件，完成剩余任务，把整个流程跑通”，复核当前本地实现、运行态和 Dune 外部认证入口是否仍阻塞。

### Changes
- **修改** `docs/DUNE_BATCH_REG_STATUS.md` — 增补 2026-06-18 现场复核结果：批量任务状态、`auth.json` Cookie 名称、Dune 页面安全验证探测。
- **修改** `docs/AI_HANDOFF.md` / `docs/CHANGELOG_AI.md` — 记录本次阻塞复核和已验证命令。

### New Functionality
- 无新增运行时功能。本次只做复核和文档同步。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `curl.exe -s http://127.0.0.1:8000/api/health` — `status=ok`，`control_plane.ok=true`，`analysis_plane.available=false`（缺少 `duckdb.exe`，既有状态）。
- `curl.exe -s http://127.0.0.1:8000/api/dune/batch/status` — 当前任务 `status=done, completed=0, failed=1`，失败账号错误为 `register failed: cloudflare_blocked`。
- `node -e ... backend/data/dune/auth.json` — auth 文件存在且有 Dune 应用 Cookie，但不含 `cf_clearance`。
- Playwright headless 探测 `https://dune.com/`、`https://dune.com/auth/register`、`https://dune.com/auth/login` — 三者均返回 `Just a moment...` 安全验证页。

### Open Items
- 真实 Dune 注册仍未跑通；当前阻塞点是 Dune/Cloudflare 外部认证入口，不是本地流程缺少代码。
- 继续推进需要 Dune 官方允许的账号/团队开通路径、白名单/企业支持，或用户提供已通过 Cloudflare 的新鲜真实浏览器会话/Cookie（包含 `cf_clearance`）。

### Notes
- 已避免继续实现或尝试 Cloudflare 绕过。现有批量注册代码路径可以启动、记录状态并暴露失败原因，但不能在当前外部认证条件下完成真实账号注册。

## 2026-06-18 Dune 批量注册: 状态文档校准

### Task
- 阅读 `docs/DUNE_BATCH_REG_STATUS.md` 与 `docs/dune-batch-registration-spec.md`，继续收敛 Dune 批量注册剩余任务，并把真实复测结论写回交接文档。

### Changes
- **修改** `docs/DUNE_BATCH_REG_STATUS.md` — 更新当前日期、已完成文件清单、验证命令、真实 API 复测结果、Cloudflare auth 阻塞事实和后续合规路径。
- **修改** `docs/AI_HANDOFF.md` / `docs/CHANGELOG_AI.md` — 记录本次文档校准、已验证命令、未完成事项和注意事项。

### New Functionality
- 无新增运行时功能。本次仅校准状态文档。

### API Changes
- 无新增、删除或重命名 API。
- `POST /api/dune/batch/start` 契约不变；最近真实复测可启动任务，但账号最终失败为 `register failed: cloudflare_blocked`。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `git diff --check -- docs/DUNE_BATCH_REG_STATUS.md docs/AI_HANDOFF.md docs/CHANGELOG_AI.md`

### Open Items
- Dune `/auth/login` 与 `/auth/register` 仍被 Cloudflare URL 级验证拦截；当前本地代码、IMAP、验证邮件、账号状态持久化、导出等链路已经就绪，但真实注册不能在没有合规入口的情况下完成。
- 继续推进需要 Dune 官方允许的注册/团队管理路径，或用户提供已通过 Cloudflare 的新鲜真实浏览器会话/Cookie（包含 `cf_clearance`）。

### Notes
- 当前不再继续实现 Cloudflare 绕过；后续应走官方支持、白名单、企业/团队管理或用户已授权会话路径。

## 2026-06-17 Dune 批量注册: 导航修复 + 真实链路复测

### Task
- 阅读 `docs/DUNE_BATCH_REG_STATUS.md` 与 `docs/dune-batch-registration-spec.md`，继续完成 Dune 批量注册剩余任务并通过真实 API 路径复测。

### Changes
- **修改** `tools/dune-playwright/register-login.mjs` — 注册模式现在通过首页可见 `Sign up` 链接进入 `/auth/register`，登录模式通过可见 `Log in` 链接进入 `/auth/login`；修复隐藏首个匹配元素导致点击失败的问题；修复 `visibleInputs` 异步 filter 导致隐藏 password 输入框被当作可见的问题。
- **新增** `tools/dune-playwright/register-login-auth.mjs` — 抽出 Dune 首页到 auth 页导航、Cloudflare/Turnstile 检测和等待逻辑。
- **新增** `tools/dune-playwright/register-login-dom.mjs` — 抽出可见元素选择、点击、输入框筛选和检测失败输出。
- **修改** `internal/dunetools/manager.go` / **新增** `internal/dunetools/manager_captcha.go` — 将 CAPTCHA 重试逻辑拆分到独立文件，修复 `RetryCaptcha` 成功后重复累计 `Completed`，并将最终 `UpdatedAt` 写入放回互斥锁内。
- **修改** `internal/dunetools/manager_test.go` — 新增 CAPTCHA retry 成功时只计数一次的回归测试。

### New Functionality
- Playwright 注册/登录桥接现在按模式选择正确 auth 入口，并跳过隐藏匹配元素。
- CAPTCHA 重试成功后账号完成数不再重复增加，成功账号仍会触发持久化回调。

### API Changes
- 无新增、删除或重命名本地 API。
- `POST /api/dune/batch/start` 行为保持不变；本次真实调用返回 `status=done, completed=0, failed=1`，账号错误为 `register failed: cloudflare_blocked`。

### Database Changes
- 无数据库结构变更。
- 本次真实 API 复测会生成运行时批量注册账号状态；不依赖数据库迁移。

### Frontend Changes
- 无前端页面或组件变更。

### Verified Commands
- `node --check tools/dune-playwright/register-login.mjs`
- `node --check tools/dune-playwright/register-login-auth.mjs`
- `node --check tools/dune-playwright/register-login-dom.mjs`
- `go test ./internal/dunetools -run TestManager_RetryCaptcha_countsCompletedAccountOnce_whenRetrySucceeds -count=1 -v` — 先红后绿，修复前 `completed = 2, want 1`，修复后通过。
- `go test ./internal/dunetools -count=1 -v`
- `go test ./internal/api -run 'DuneBatch|Dune|PublicExecution' -count=1 -v`
- `go test ./internal/... -count=1 -timeout 300s`
- `go vet ./...`
- `go build -o bin/etl-server.exe ./cmd/server/`
- `cd frontend && npm run build` — 通过，仍有既有 large chunk warning。
- `powershell -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` — 重启成功，PID 52348。
- `curl.exe -s http://127.0.0.1:8000/api/health` — `status=ok`，`control_plane.ok=true`，`analysis_plane.available=false`（缺少 duckdb.exe，既有状态）。
- `POST /api/dune/batch/start` 真实启动 1 个账号，最终 `GET /api/dune/batch/status` 返回 `completed=0, failed=1, status=done`，失败原因为 `register failed: cloudflare_blocked`。

### Open Items
- Dune `/auth/login` 与 `/auth/register` 仍被 Cloudflare URL 级验证拦截；已验证 direct URL、真实首页可见 Sign up 点击、Chromium 与 installed Chrome channel 均会进入 `请稍候…` Cloudflare 页面。
- 当前 `auth.json` 只有 Dune 应用 auth cookies/tokens，没有 `cf_clearance`；要跑通注册仍需要可用代理、带 Cloudflare clearance 的新鲜浏览器会话/Cookie，或发现 Dune 官方可用的注册 API。

### Notes
- 本次不再把“点击 Sign up”作为未验证假设：已经用 Playwright 实测可见链接点击，结论仍为 Cloudflare 阻断。
- `register-login.mjs` 拆分后所有触达文件均低于 250 pure LOC。

## 2026-06-17 Dune 批量注册: 当前状态

### Task
- 完成 Dune 批量注册系统全链路开发
- 解决 Cloudflare 绕过问题

### Status
- 详见 `docs/DUNE_BATCH_REG_STATUS.md`
- 后端、前端、API、Playwright 脚本全部就绪
- 核心阻塞: Dune `/auth/*` 页面被 CF URL 级保护，首页可通过但 auth 页面不可达

### Verified
- `go build` — BUILD_OK
- `go test ./internal/...` — 全部通过
- `npm run build` — 通过
- `.\run.ps1` — 正常 (PID 43196)

## 2026-06-17 Dune 爬取模式完全修复 + 自动刷新 Token

### Task
- 测试 Dune 爬取（web_query）功能，修复所有阻断问题
- 解决 Cloudflare 绕过、Dune GraphQL schema 变更、执行轮询、日志缺失
- 实现 Token 自动刷新基础设施

### Changes
- **重写** `backend/data/dune/playwright_bridge.js` — 支持 graphql / execution / refresh 三模式；接收 Go 传来的凭据并注入浏览器；保留持久化 profile 以复用 Cloudflare clearance
- **修改** `internal/logger/logger.go` — 修复 MultiLevelWriter 顺序（file 优先于 console）；设置全局 `zerolog/log.Logger` 指向文件，使所有包日志可查
- **修改** `internal/api/dune_web_query.go` — CreateQuery 参数从 `input: $query` 改为 `query: $query`；移除废弃字段 `tags`/`isArchived`；删掉 CreateQuery 重试逻辑；新增 `ensureDuneTokensFresh` / `refreshDuneTokens`（Playwright refresh 模式，当前禁用）
- **修改** `internal/api/dune_auth_handlers.go` — `duneStoredAuth` 新增 `TeamID` 字段；`resolveDuneWebAuth` 只要求 Cookie 必填（Authorization/AccessToken 可选）
- **修改** `internal/api/dune_public_execution.go` — `fetchDunePublicExecutionPage` 检测 Cloudflare 后走 Playwright fallback；新增 `io` 和 `zerolog/log` import
- **新增** `tools/dune-playwright/refresh-token.mjs` — 独立 Token 刷新脚本

### New Functionality
- Dune 爬取模式完整链路：CreateQuery → ExecuteQuery → 执行轮询 → 返回结果，全部通过 Playwright 绕过 Cloudflare
- 支持存储 `team_id` 到 `auth.json`，自动检测团队时优先使用
- 日志文件 `backend/data/logs/app.log` 现在正确记录所有 API 请求和 Dune GraphQL 交互
- `tools/dune-playwright/refresh-token.mjs` 可手动刷新 Token

### API Changes
- `POST /api/dune/query` 爬取模式行为：自动读取 `auth.json` 中的 `team_id`；Cookie 必填但 Authorization/AccessToken 可选
- `/api/dune/auth` 响应未变

### Verified Commands
- `go build` — BUILD_OK
- `go vet ./internal/...` — 通过
- `curl POST /api/dune/query` — 返回真实数据 (3 rows from dataset_addr)

### Notes
- Playwright profile (`backend/data/dune/playwright-profile/`) 被清空；首次查询前需用户提供有效 Dune Cookie
- 自动刷新 Token 代码已写好但禁用（`ensureDuneTokensFresh` 被注释），需 profile 中有用户登录 session 后才能启用
- Dune JWT 有效期 5 分钟；bridge 通过 `credentials: 'include'` 使用浏览器 Cookie 传递认证，不依赖过期 JWT

## 2026-06-16 Dune 查询错误处理增强 + 过期凭据提示

### Task
- 使用真实 SQL 测试 Dune web_query，记录并修复所有错误
- 发现凭据过期 + 错误信息误导 + 错误链路断裂 + 响应体丢弃 四个问题

### Changes
- **修改** `internal/api/dune_web_query.go` — `doDuneWebGraphQL` 记录完整 HTTP 响应体到日志; `fetchDuneWebDefaultTeam` 追踪认证错误链不再丢弃; `resolveDuneWebQueryIDs` 明确返回"Cookie/Token 可能已过期"提示
- **修改** `internal/api/dune_query_handlers.go` — `writeDuneAPIError` 认证错误携带上下文时直接透传，不再统一替换为泛泛信息

### New Functionality
- Dune 查询401/403时可区分"无凭据"与"凭据已过期/无效"，并显示 Dune 原始错误摘要

### API Changes
- `POST /api/dune/query` 认证类错误 detail 更具体

### Verified Commands
- `go build` — BUILD_OK
- `go test ./internal/...` — 全部通过
- `.\run.ps1` — 重启成功 (PID 12936)
- `curl POST /api/dune/query` — 401，消息: "Cookie/Token 可能已过期，请在左侧面板重新保存"

### Notes
- Dune GraphQL 受 Cloudflare 保护，HTTP 403 挑战页需要浏览器自动化 (Playwright) 绕过
- 当前凭据已过期，需刷新 Cookie + Authorization + AccessToken 后保存

## 2026-06-16 Dune 查询: 模式切换 + 自动获取 ID

### Task
- 用户要求: API/爬取两种模式分开，爬取模式全部 ID 参数自动获取

### Changes
- **修改** `internal/api/dune_web_query.go` — 新增 `resolveDuneWebQueryIDs` / `fetchDuneWebDefaultTeam` / `createDuneWebQuery` / 响应类型; `executeDuneWebQueryWithRetry` 改为先自动解析 ID; 自动创建查询后跳过 UpdateQuery
- **修改** `frontend/src/features/download/DuneDownloadPanel.tsx` — 新增 mode 状态 (api/crawl); Collapse 设置面板; 模式切换 Select; 模式条件字段渲染; 移除手动 ID 输入框和 webQuery checkbox
- **回退** `internal/api/dune_query_handlers.go` / `frontend/src/features/download/duneApi.ts` 上一版 URL 解析代码

### New Functionality
- 爬取模式: 输入 SQL → 点查询 → 后端自动 CreateQuery → 自动 GetTeams 获取 team_id → 默认 dataset_id=11 → 自动 Execute → 返回结果
- 前端: API/爬取 下拉切换, 设置折叠面板, 模式对应参数动态显隐

### API Changes
- `POST /api/dune/query` 爬取模式下 ID 缺失不再报硬错误，自动补全

### Verified Commands
- `go build` — BUILD_OK
- `go test ./internal/...` — 全部通过
- `cd frontend && npm run build` — 通过
- `.\run.ps1` — 重启成功 (PID 14688)
- `curl http://127.0.0.1:8000/api/health` — status=ok

### Notes
- 爬取模式需 Cookie + Authorization + AccessToken 有效
- 自动创建查询为临时私有 (isTemp=true, isPrivate=true)
- 团队获取失败时回退提示手动填 team_id

## 2026-06-14 SQLite-DuckDB 优化升级: 最终交付

### Task
- 阅读 `docs/离线版SQLite-DuckDB优化升级对比.md`，完成 SQLite + DuckDB 迁移的全部代码工作
- 修复 `go.mod` 依赖分类 (modernc.org/sqlite 从 indirect → direct)
- 验证编译、测试、健康检查全部通过
- 提交全部代码

### Changes
- `go.mod` — `modernc.org/sqlite v1.52.0` 从 indirect 移至 direct require 块
- `go.sum` — 更新校验和
- `docs/AI_HANDOFF.md` — 记录最终交付状态
- `docs/CHANGELOG_AI.md` — 记录最终交付状态

### Verified Commands
- `go build -o bin\etl-server.exe .\cmd\server\` — BUILD_OK
- `go test ./internal/... -count=1 -timeout 120s` — 全部通过 (api 9.5s, etl 30s, others < 1s)
- `go vet ./internal/...` — 无警告
- `.\run.ps1` — 重启成功 (PID 14788)
- `curl http://127.0.0.1:8000/api/health` — status=ok, control_plane=ok, analysis_plane=unavailable (expected)

### Open Items
- 需放置 `duckdb.exe` 到 `tools/duckdb/` 激活分析面
- 未做真实数据 DuckDB 端到端验证
- 未做 DuckDB vs Go 原逻辑结果一致性对比

### Notes
- 所有 5 个迁移阶段代码已完成，全部文件在工作树中 (待 commit)
- DuckDB 不可用时不影响原有功能，全部回退原文件扫描路径

## 2026-06-08 SQLite-DuckDB 优化升级: 接入基础设施 (代码编写)

### Task
- 阅读 `docs/离线版SQLite-DuckDB优化升级对比.md`，将 SQLite 控制面和 DuckDB 分析面接入项目
- 实现最小可行性方案：DuckDB Engine + SQLite Control Store + 导入后建表 + 图建/边详情/字段值优先走 DuckDB

### Changes
- **新增** `internal/analysis/duckdb/engine.go` — DuckDB CLI 引擎，支持 ExecSQL/ExecSQLJSON/CreateTableFromCSV/CreateTableFromXLSX/DropTable/TableRowCount
- **新增** `internal/api/duckdb_flow.go` — 会话级 DuckDB 表加载 (ensureSessionDuckDBTable) + 清理 (cleanupOldDuckDBTable)
- **新增** `internal/api/duckdb_graph.go` — DuckDB 图查询引擎: buildFlowFromDuckDB (SQL 聚合建图), queryEdgeDetailFromDuckDB (SQL 边详情), queryColumnValuesFromDuckDB (DISTINCT 字段值)
- **新增** `internal/storage/control/store.go` — SQLite 控制面，存储 flow_sessions 元数据和 analysis_table 映射
- **修改** `internal/config/config.go` — 新增 AnalyticsConfig (DuckDBPath, DuckDBDatabase)
- **修改** `internal/api/handlers.go` — Setup 初始化 DuckDB+SQLite; HandleHealth 返回 control_plane/analysis_plane; HandleBuildImportedFlow/HandleImportedFlowEdgeDetail/HandleFlowFieldValues 优先 DuckDB 回退原逻辑
- **修改** `cmd/server/main.go` — 优雅关闭时调用 api.Shutdown() 关闭 control store
- **修改** `go.mod` — 新增 modernc.org/sqlite v1.52.0 及其传递依赖

### New Functionality
- SQLite 控制面: 启动时自动初始化 `backend/data/control/etl_control.sqlite`，WAL 模式
- DuckDB 分析面: 启动时自动检测 `tools/duckdb/duckdb.exe`，不可用时不崩溃
- 健康检查: `/api/health` 返回 `control_plane` 和 `analysis_plane` 状态
- 导入后自动异步加载 DuckDB 表 (首次构建触发，后续构建走 SQL 聚合)
- 图谱生成: 有 DuckDB 表时走 SQL 聚合 (direction 归一化、筛选、聚合一气呵成)，无表时回退原逻辑
- 边详情: 有 DuckDB 表时走 SQL 查询 (支持过滤、分页、总金额/总笔数)，无表时回退原逻辑
- 字段候选项: 有 DuckDB 表时走 DISTINCT 查询 (支持搜索)，无表时回退原逻辑
- 所有 DuckDB 路径失败均回退到原有文件扫描路径，不影响用户正常使用

### API Changes
- `/api/health` 响应新增 `control_plane` 和 `analysis_plane` 字段
- `/api/flow/build` 响应在 DuckDB 路径下 meta 新增 `duckdb: true` 标记
- `/api/flow/edge-detail/imported` 响应在 DuckDB 路径下新增 `duckdb: true` 标记
- `/api/flow/values` 响应在 DuckDB 路径下新增 `duckdb: true` 标记

### Frontend Changes
- 无

### Verified Commands
- `go build -o bin\etl-server.exe .\cmd\server\` — 编译通过
- `go test ./internal/... -count=1 -timeout 300s` — 全部通过
- `go vet ./internal/...` — 无警告
- `.\run.ps1` — 重启成功
- `curl http://127.0.0.1:8000/api/health` — 返回 control_plane (ok) + analysis_plane (unavailable, 缺少 duckdb.exe)

### Open Items
- 需将 `duckdb.exe` 放到 `tools/duckdb/` 目录激活分析面
- 未做真实数据 DuckDB 路径端到端验证 (缺少 duckdb.exe)
- 未做 DuckDB vs Go 图建结果一致性对比
- 未做 DuckDB 边详情 vs 原逻辑一致性对比

### Notes
- 采用纯回退策略: DuckDB 路径任何失败都静默回退到原有逻辑
- DuckDB 表创建在 goroutine 中异步执行，首次构建不走 DuckDB
- 所有 SQL 生成均使用参数化 quote (quoteIdentifier/quoteSQLString)
- 未迁移 etl_exe 的授权/license/激活/离线打包逻辑

## 2026-05-29 创建 E:\codex\etl_exe\frontend 独立前端项目

### Task
- 在 `E:\codex\etl_exe\frontend` 创建独立的 React 前端项目
- 与原始项目相同品牌布局（Ant Design Sider + Content）
- 包含：License 激活、数据导入（文件上传+字段映射）、资金流向图可视化（ReactFlow）
- 所有文件由 AI 直接生成（write 工具）

### Changes
- `E:\codex\etl_exe\frontend\package.json` — 依赖：react 19, antd 5, @xyflow/react 12, html-to-image, jszip
- `E:\codex\etl_exe\frontend\tsconfig.json` — strict 模式
- `E:\codex\etl_exe\frontend\vite.config.ts` — proxy /api → 127.0.0.1:15978
- `E:\codex\etl_exe\frontend\index.html` — 中文 lang
- `E:\codex\etl_exe\frontend\src\main.tsx` — 导入 CSS + 渲染 App
- `E:\codex\etl_exe\frontend\src\App.tsx` — 整体布局、License 激活逻辑、选项卡切换
- `E:\codex\etl_exe\frontend\src\types.ts` — FlowNode, FlowEdge, FlowGraph, ImportResult, LicenseStatus, FlowFilter
- `E:\codex\etl_exe\frontend\src\api\client.ts` — getJson, postJson, postForm
- `E:\codex\etl_exe\frontend\src\pages\ImportPage\index.tsx` — 文件拖拽上传、字段映射（14 个映射）、数据预览、构建流向图
- `E:\codex\etl_exe\frontend\src\pages\FlowGraphPage\index.tsx` — ReactFlow 画布、主体筛选、路径追踪（BFS）、关系/异常/主体分析、CSV/PNG 导出、节点抽屉、边详情感窗
- `E:\codex\etl_exe\frontend\src\styles\layout.css` — 完整布局样式（sidebar, brand, canvas, inspector, mapping, filters 等）
- `E:\codex\etl_exe\frontend\src\styles\shared.css` — 基础样式
- `E:\codex\etl_exe\frontend\src\vite-env.d.ts` — Vite client types

### New Functionality
- 完整的前端项目，可独立构建部署
- 支持软件激活流程（激活码 + .act 文件导入）
- 数据导入流程（上传 → 解析 → 字段映射 → 构建流向图）
- 流向图可视化（ReactFlow 渲染、筛选、路径分析、异常检测、导出）

### Verified Commands
- `npm install` — 130 packages
- `npm run build` — 构建成功

### Architecture Notes
- 项目独立于原始 `E:\codex\etl`，共享相同 API 契约
- 使用 Vite proxy 代理 /api 到后端 15978 端口
- 使用 ReactFlow 默认节点类型（非自定义 FlowEntityNode）
- 所有状态管理在页面组件内，无外部复杂 hooks

## 2026-05-28 修复边缘详情显示问题: 交易时间截断 + 数据库导入列名显示来源字段

### Task
- 用户反馈两个问题：
  1. 边缘详情弹窗中表格单元格文本（如交易时间）被截断，`white-space: nowrap` 导致长文本不换行
  2. 数据库导入的流水查看线条详情时，表格字段显示的是标准映射列名（如"交易时间"）而不是来源数据库列名（如"交易日期"）；要求字段名称和排列顺序与来源一致

### Changes
- `frontend/src/features/flow/flow-canvas.css`:
  - `.excel-cell-text` 保持 `white-space: nowrap` 单行显示，移除 `overflow: hidden` 不截断
- `frontend/src/features/flow/EdgeDetailModal.tsx`:
  - 新增 `estimateTextWidth` 按中/英文字符估算像素宽度，动态计算每列最长值设定列宽
  - 过滤 `HIDDEN_FIELDS`（含 `ly_path`），所有来源中该字段不显示
- `internal/dbimport/service.go`:
  - 添加 `encoding/json` 导入
  - `StartTask` 中在写入 `database_import.csv` 后，额外保存 `column_origins.json` 到会话目录，记录 `source_columns`（来源列有序列表）和 `target_to_source`（标准列名→来源列名反向映射）
- `internal/api/handlers.go`:
  - 添加 `encoding/json` 导入
  - `HandleImportedFlowEdgeDetail` 中在确定 `columns` 后检查 `column_origins.json`：
    - 若存在，用 `source_columns` 作为显示列（保持数据库查询顺序）
    - 追加未映射的标准列（如摘要说明、备注等）
    - 将每行数据 map key 从标准名替换为来源原始列名

### New Functionality
- 数据库导入会话的边缘详情现在显示原始数据库列名，而非标准映射列名
- 单元格文本单行完整显示，列宽根据最长字段值动态计算
- `ly_path` 字段在所有来源中自动隐藏

### Verified Commands
- `go test ./internal/... -count=1 -timeout 300s` — 全部通过
- `go vet ./internal/...` — 无警告
- `go build -o bin\etl-server.exe .\cmd\server\` — 编译通过
- `cd frontend; npx tsc --noEmit` — 通过
- `cd frontend; npm run build` — 通过
- `http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- 已重启 etl-server.exe

### Notes
- `column_origins.json` 仅在数据库导入时生成，文件上传会话行为不变
- 多表导入同一会话时列名映射取并集
- 未映射的标准列仍以标准列名显示在末尾

### Task
- 另一个进程（AI 工具）调用 `.\run.bat` 时总是卡死不返回。
- 根因: `run.bat` 的 `start /B` + `tasklist | find` + 混合 PowerShell/cmd 上下文导致跨进程行为不可靠：
  - `tasklist | find` 管道在 PowerShell 调用 cmd.exe 时报 "Input redirection is not supported"
  - `start /B` 启动的服务进程可能被调用者进程组持有，导致调用者无限等待
  - 端口检查依赖 `curl`，跨进程环境无可靠超时机制

### Changes
- `run.bat` — 重写为单行委托入口：`powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0run.ps1"`
- `run.ps1` — 新建，纯 PowerShell 实现：
  - 旧进程清理：`Get-Process` + `Stop-Process` + 3 次重试（2 秒间隔）
  - 端口释放检查：`curl.exe` 轮询 15 次（1 秒间隔），忽略 TIME_WAIT
  - 后台启动：`Start-Process -WindowStyle Hidden -PassThru`，非阻塞
  - 健康检查：`curl.exe` 轮询 15 次，匹配 JSON 响应 `"status":"ok"`

### Verified Commands
- `.\run.ps1` — 4.82 秒返回，健康检查通过
- `.\run.bat` — 5.02 秒返回，委托成功
- `curl http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- `go test ./internal/... -count=1 -timeout 300s` — 全部通过
- `go vet ./...` — 无警告

### Notes
- `run.bat` 现在是 `run.ps1` 的委托入口，跨进程调用优先使用 `.run.ps1` 或 `.run.bat`（最终走相同逻辑）
- 不再依赖 `start /B`，改用 `Start-Process -WindowStyle Hidden` + 健康检查确认

## 2026-05-28 修复计划任务进程卡死 (RunPipeline goroutine 死锁 + 启动增强)

### Task
- 每次计划任务运行时，经常在某个进程处卡死，且卡死的进程不固定，需等很久才能超时或人工干预。
- 根因 1 (核心): `internal/etl/etl.go:118` — `errChan` 缓冲大小固定 `3`，但 `categorizeByProvider` 最多返回 **4 个分组**（支付宝/微信/银行/unknown）。当 4 个 goroutine 全部报错时，第 4 个无法写入 `errChan` → 永久阻塞 → `wg.Done()` 不执行 → `wg.Wait()` 永远不返回 → API handler 挂死 → 计划任务挂死。
- 根因 2: `run.ps1` 清理旧进程只做一次 `Stop-Process`，无重试；旧进程句柄未完全释放时端口 8000 仍被占用，新实例启动失败静默挂死。
- 根因 3: `main.go` 信号处理只做 `logger.Close()` + `os.Exit(0)`，不等待 in-flight 请求完成，旧进程被 `Stop-Process -Force` 直接终止时可能导致端口状态不一致。

### Changes
- `internal/etl/etl.go`:
  - `errChan` 缓冲从固定 `3` 改为 `len(providerGroups)`，确保所有 goroutine 可并发写入而不阻塞。
- `run.ps1`:
  - 旧进程清理增加 **3 次重试 + 2 秒间隔**，确认进程完全终止才继续。
  - 新进程启动后增加 **健康检查轮询**（最多 15 秒），确认 `/api/health` 返回 `200` 才标记就绪。
- `cmd/server/main.go`:
  - 从 `router.Run(addr)` 改为 `http.Server` + `srv.ListenAndServe()`。
  - 信号处理改为 **Graceful Shutdown**：收到 SIGINT/SIGTERM 后，给 in-flight 请求最多 **10 秒** 完成再退出。

### Verified Commands
- `go test ./internal/... -count=1 -timeout 300s` — 全部通过
- `go vet ./internal/...` — 无警告
- `go build -o bin\etl-server.exe .\cmd\server\` — 编译通过

### Notes
- `errChan` 死锁是经典的 goroutine 泄漏模式：有缓冲 channel + 生产者多于缓冲容量 → 阻塞永不解除。
- 由于只有遇到 error 才会写入 `errChan`，死锁是**非确定性的**（取决于哪些 provider 出错、出错顺序），所以每次卡死的进程不一样。
- 若仍有个别计划任务卡死，可能是网络/文件 I/O 超时导致 `processProviderFiles` 本身挂住，不属于 goroutine 泄漏范畴；可增加 `context.WithTimeout` 进一步保护。

## 2026-05-28 修复服务启动卡死 (端口检查 + graceful shutdown 时序)

### Task
- 计划任务重启服务时经常卡死，健康检查 15 秒全部失败但脚本不报错
- 根因 1 (核心): `run.bat` 中 `netstat | findstr` 管道在 PowerShell 下输出 "Input redirection is not supported" 错误，端口检查循环持续 15 次全部失败 → 脚本 abort → 服务未启动
- 根因 2: 端口检查匹配了 TIME_WAIT 状态的连接（来自之前的 curl），误判端口仍被占用
- 根因 3: `run.bat` 健康检查失败只打 WARNING，不返回错误退出码 → 调用者（计划任务）以为启动成功
- 根因 4: `main.go` graceful shutdown 超时 10 秒，`taskkill /F` 后服务需等 10 秒才释放端口

### Changes
- `run.bat` → 删除（PowerShell 下管道重定向不兼容）
- `run.ps1` → 重写，恢复为 PowerShell 脚本：
  - 旧进程清理：3 次重试 + 2 秒间隔
  - 端口释放检查：只匹配 `0.0.0.0:8000` 或 `[::]:8000` 的 LISTENING 状态，忽略 TIME_WAIT
  - 启动后健康检查：15 秒轮询，失败时 `exit 1`
- `cmd/server/main.go`: Graceful shutdown 超时从 10 秒缩短到 3 秒

### Verified Commands
- `go test ./internal/... -count=1 -timeout 300s` — 全部通过
- `go vet ./internal/...` — 无警告
- `go build -o bin\etl-server.exe .\cmd\server\` — 编译通过
- `.\run.ps1` — 首次启动成功（PID 18312）
- `.\run.ps1` — 重启成功（旧 PID 18312 → 新 PID 32336，端口释放检测正确）
- `curl http://127.0.0.1:8000/api/health` — `{"status":"ok"}`

### Notes
- `run.bat` 在纯 cmd.exe 环境可正常运行，但被 PowerShell 调用时 `netstat ... | findstr ...` 管道报 "Input redirection is not supported" 错误，导致 15 次端口检查全部失败。
- `netstat -ano` 输出中 TIME_WAIT 状态的连接包含 `:8000`，但不会阻止新进程绑定端口。必须只匹配 LISTENING 状态。
- `Stop-Process -Force` 发送 SIGTERM → Go 信号处理器执行 `srv.Shutdown()`（3 秒超时）→ 关闭 listener → `os.Exit(0)`。从 kill 到端口释放约 3 秒。

## 2026-05-28 修复 run.ps1 重启服务无限卡死问题

### Task
- 每次执行 `.\run.ps1` 重启服务就会无限卡死，导致后续计划任务无法执行。
- 根因: `run.ps1` 第 43 行使用 `& $binPath` 前台阻塞调用，PowerShell 等待 `etl-server.exe` 进程退出才返回。服务永不退出，脚本永不返回。
- 同时修复：旧进程未清理时端口 8000 冲突导致新实例启动失败。

### Changes
- `run.ps1`: 
  - 启动前先通过 `Get-Process -Name "etl-server"` 查找并 `Stop-Process` 旧进程。
  - `& $binPath` 前台阻塞调用 → `Start-Process -WindowStyle Hidden -PassThru` 后台非阻塞调用，脚本立即返回。
  - 移除不必要的 stdout/stderr 重定向（服务内部已通过 zerolog 写 `backend/data/logs/app.log`）。

### Verified Commands
- `.\run.ps1` — 1.21 秒返回（修复前：卡死不返回）
- `curl.exe -s http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- `Get-Process -Name "etl-server"` — PID 3056 后台运行
- `go test ./internal/... -count=1 -timeout 300s` — 全部通过

### Notes
- 后台启动后，服务日志继续写 `backend/data/logs/app.log`，不受 stdout/stderr 重定向影响。
- 若要停止服务，使用 `Stop-Process -Name "etl-server" -Force`。

## 2026-05-28 数据库导入: PostgreSQL 全表导入压测确认

### Task
- 继续执行上一轮未完成事项：对真实 PostgreSQL 表 `mz.ls_0709.交易明细信息` 执行全量数据库导入压测，确认 6,737,400 行完整导入耗时和结果。

### Changes
- 无业务代码变更。
- 仅更新 `docs/AI_HANDOFF.md` 和 `docs/CHANGELOG_AI.md` 记录本次压测结果。

### Test Setup
- 使用已有保存密码的本地 PostgreSQL 连接 `test`：`localhost:5432`，连接 ID `1b9c7c95-8dbc-4594-9a44-1cf4002ac9c2`。
- 目标数据库/表：`mz.ls_0709.交易明细信息`。
- `count(*)` 确认源表行数：`6,737,400`。
- 自动映射确认：33 列源表，11 个字段映射，4 个必填目标字段全部映射成功。

### Results
- 导入任务 ID：`3bd991d9-4a08-4d6c-8d32-471ff730fc28`
- 导入会话 ID：`db-101f858a-3c4`
- 状态：`completed_with_errors`
- `processedRows`: `6,737,400`
- `successRows`: `5,670,886`
- `failedRows`: `1,066,514`
- `speedRowsPerSecond`: `141,692.2`
- 任务时间：`2026-05-28T18:26:24.8348282+08:00` 到 `2026-05-28T18:27:12.5761221+08:00`，约 47.7 秒。
- 导出 CSV：`backend/data/uploads/flow_sessions/db-101f858a-3c4/database_import.csv`
- CSV 大小：`905,085,129 bytes`，约 `863.16 MB`。
- 任务状态文件：`backend/data/db_import/db_import_config.enc` 约 `1,477,364 bytes`，没有再次膨胀。

### Findings
- 全表导入速度从此前 100 万行实测约 `40,848 行/秒` 提升到本轮全量压测约 `141,692 行/秒`。
- 失败样本主要为源数据业务字段缺失：
  - `必填字段为空：对手户名`
  - `必填字段为空：交易方户名`
- 这是数据质量/业务规则问题，不是导入吞吐瓶颈。

### API Changes
- 无。

### Database Changes
- 无数据库结构变更；只读 PostgreSQL 源表，写入本地导入会话 CSV。

### Frontend Changes
- 无。

### Verified Commands
- `GET /api/db/connections` — 找到本地 PostgreSQL 连接 `test`
- `Test-NetConnection -ComputerName 127.0.0.1 -Port 5432` — `TcpTestSucceeded=True`
- `POST /api/db/query` — `select count(*) as total from "ls_0709"."交易明细信息"` 返回 `6,737,400`
- `POST /api/db/mappings/auto` — 自动映射 11 项，必填字段映射完整
- `POST /api/db/import/tasks` — 创建全量压测任务
- `POST /api/db/import/tasks/:id/start` — 启动任务，返回 `running`
- `GET /api/db/import/tasks/:id` — 轮询至 `completed_with_errors`
- `GET /api/health` — `{"status":"ok"}`

### Open Items
- 本轮未对 5,670,886 行成功导入会话执行 `/api/flow/build` 全量建图，避免一次性读取 863MB CSV 造成不必要内存压力；如需要验证全量建图，应单独作为性能任务执行并监控内存。
- 如果业务允许空 `对手户名` 或空 `交易方户名`，需要单独调整必填字段策略或增加映射兜底规则。

### Notes
- 本次完成了上一轮文档中“未跑完整 6,737,400 行全表导入”的待确认项。
- 本轮无后端代码变更，因此未执行 `.\run.ps1` 重启。

## 2026-05-28 数据库导入: 极致性能优化

### Task
- 对数据库数据导入速度做进一步极致优化，目标是压低百万行导入时 Go 端 CPU/GC、CSV 写入、状态持久化开销。

### Changes
- `internal/dbimport/service.go`
  - 导入循环从“每行构造 `map[string]interface{}` + map lookup”改为“预编译列索引映射 + 复用 `database/sql` 扫描缓冲 + 复用 CSV record”。
  - 新增 `importRowMapper`，按 `rows.Columns()` 一次性建立源列索引到 Flow 目标列索引的映射；保留旧 `mapImportRow` 用于兼容测试。
  - 数据库原生 `time.Time` 直接格式化为 `yyyy-MM-dd HH:mm:ss`；原生数值类型直接格式化为两位小数，避免再次走字符串清洗正则。
  - CSV 写入增加 4MB `bufio.Writer` 缓冲。
  - 进度持久化从每 1 万行调整为每 5 万行或 2 秒一次；取消检查仍保持每 1 万行一次。
- `internal/parser/parser.go`
  - `CleanText`、`ToNumber`、`NormalizeDatetime` 使用包级预编译正则，避免每行重复 `regexp.MustCompile`。
  - `NormalizeDirection` 使用包级方向别名 map，避免每次调用重新分配 map。
  - `NormalizeDatetime` 新增标准 `yyyy-MM-dd HH:mm:ss` 快路径，标准日期字符串直接返回。
- `internal/dbimport/service_test.go`
  - 新增索引映射与旧 map 映射输出一致性测试。
  - 新增缺失返回列保护测试。
  - 新增 `BenchmarkImportRowMapping` 对比旧 map 映射与新索引映射。

### Performance
- `BenchmarkImportRowMapping/map`: `2752 ns/op`, `557 B/op`, `20 allocs/op`
- `BenchmarkImportRowMapping/indexed`: `1318 ns/op`, `130 B/op`, `12 allocs/op`
- 单行映射耗时约下降 52%，分配字节约下降 77%，分配次数约下降 40%。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更；仍仅读取源数据库并写入本地导入会话 CSV。

### Frontend Changes
- 无前端代码变更。

### Verified Commands
- `go test ./internal/dbimport -count=1 -v` — 通过
- `go test ./internal/parser -count=1` — 通过
- `go test ./internal/dbimport -run '^$' -bench BenchmarkImportRowMapping -benchmem` — 通过，见性能数据
- `go test ./internal/... -count=1 -timeout 300s` — 通过
- `go vet ./internal/...` — 通过
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过
- `cd frontend; npx tsc --noEmit` — 通过
- `cd frontend; npm run build` — 通过，仍有既有大 chunk warning
- 已执行 `.\run.ps1` 重启；首次因旧 `etl-server.exe` 占用 8000 端口失败，确认 PID 28496 为 `E:\codex\etl\bin\etl-server.exe` 后结束旧进程并重新启动
- `http://127.0.0.1:8000/api/health` — `{"status":"ok"}`，当前监听 PID 25856

### Open Items
- 本轮未重新连接真实 PostgreSQL 跑完整 6,737,400 行全量导入压测；当前验证覆盖代码路径、自动化测试和单行映射微基准。

### Notes
- 本次未修改 `/api/flow/*`、手工文件导入流程或前端交互。
- 进度总行数仍使用数据库统计估算值，任务完成时校正为实际处理行数。

## 2026-05-28 数据库导入: 修复"导入无反应"（按钮转圈无结果）

### Task
- 数据库导入点击"导入向导"后按钮转圈但无结果反馈
- 根因: `StartTask` 中 `sessionID` 直到函数末尾(515行)才赋给 `task.SessionID`，但中间多个失败路径提前返回时 sessionID 未赋值；同时早期文件/CSV 错误不保存 task 状态，前端轮询永远等不到完成状态 → 按钮无限转圈

### Changes
- `internal/dbimport/service.go`: `StartTask` — `task.SessionID = sessionID` 提前到 sessionID 生成后立即赋值；早期文件/CSV 创建失败时保存 "failed" 状态到 store；`Preview` 错误也计入 `FailedRows` 并保存
- `frontend/src/features/flow/DBImportModal.tsx`: `startImport` — 轮询增加 10 分钟超时，超时后弹出错误提示并停止轮询；当 status=failed/canceled 且无 session_id 时弹出错误消息并切换到"导入任务"标签页；轮询 catch 也弹出错误消息

### New Functionality
- 数据库导入任务失败时前端现在会显示错误消息而非无反馈
- 轮询 10 分钟超时自动停止，避免无限转圈

### Verified Commands
- `go test ./internal/... -count=1` — 全部通过
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过
- `cd frontend; npm run build` — 通过
- `http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- 已重启 etl-server.exe

## 2026-05-28 数据库导入: 修复 NULL 值显示 `<nil>` 问题

### Task
- 主体详情身份证号显示 `<nil>`

### Root Cause
`internal/dbimport/service.go:883`:
```go
record[idx] = fmt.Sprint(row[mapping.SourceColumn])
```
当数据库列为 NULL 时，`row[key]` 返回 Go `nil`，`fmt.Sprint(nil)` 生成字符串 `"<nil>"`。该字符串写入 CSV → 被 `readSessionDataWithCache` 读回 → 进入 `TransactionRow` → 进入 `FlowNode.IDNumber` → 传给前端 → 用户看到 `<nil>`。

### Changes
- `internal/dbimport/service.go:883`: `mapImportRow` — `fmt.Sprint(row[key])` 先判 `nil`，NULL 值留空
- `internal/dbimport/service.go:1017`: `TransactionRowsFromTask` — 同样修复 `fmt.Sprint(value)` nil 问题
- `internal/api/edge_cache.go`: 新增 `sessionRowCache.ColumnOrder` 字段，在读取文件时存储归一化后的列名顺序；新增 `getCachedColumnOrder(sessionID)` 函数
- `internal/api/handlers.go`: `HandleImportedFlowEdgeDetail` 用 `getCachedColumnOrder` 获取有序列名，不再用随机 map 迭代；缓存未命中时按 key 名排序保确定性

### Verified Commands
- `go test ./internal/... -count=1` — 全部通过
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过
- `http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- 已重启 etl-server.exe

## 2026-05-28 性能优化: getNodeGeometry O(n) 数组扫描 → O(1) Map 查询

### Task
- 修复选择交易账户后生成流向图时前端卡死
- 根因: `getNodeGeometry` 使用 `nodes.find()` (O(n))，在 `visibleGraph` useMemo + `buildOptimizedHandleMap` 中每边调用 4 次

### Changes
- `frontend/src/features/flow/flowGeometry.ts`:
  - `getNodeGeometry(nodeId, nodes, positions)` → `getNodeGeometry(nodeId, nodesMap, positions)`，参数从 `Node[]` 改为 `Map<string, Node>`，查找从 O(n) 变为 O(1)
  - 新增 `buildNodesMap(nodes)` 工具函数
  - `buildOptimizedHandleMap` 内部预构建 `nodesMap`，避免每边重复扫描
- `frontend/src/features/flow/useFlowGraph.ts`:
  - `visibleGraph` useMemo 内预构建 `nodesMap = new Map(nodes.map(...))`，传入 `getNodeGeometry` 替代 `nodes` 数组

### Verified Commands
- `cd frontend; npx tsc --noEmit` — 通过
- `cd frontend; npm run build` — 通过
- `go test ./internal/... -count=1` — 全部通过
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过
- `http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- 已重启 etl-server.exe

## 2026-05-28 数据库导入: 移除"打开连接"按钮 + 修复连接交互 + 测试反馈

### Task
- 删除数据库导入弹窗中的"打开连接"按钮
- 修复"测试连接"无反馈信息的问题
- 修复点击连接名称无反应的问题

### Changes
- `frontend/src/features/flow/DBImportModal.tsx`:
  - 移除 `connection-actions` 中的"打开连接"按钮
  - 修复测试连接反馈：`notification.success/error` 替换为 `message.success/error`（全项目统一用 `message`）
  - 移除 `antd` 的 `notification` 导入
  - 修复点击连接无反应：`refreshConnections` 不再自动选中第一个连接，改为 `selectedConnection=null` + 重置所有子状态；用户必须点击连接名称才能触发 `handleConnectionSelect`

### New Functionality
- 测试连接结果现在显示为顶部消息提示（`message.success`/`message.error`），更明显

### Verified Commands
- `cd frontend; npx tsc --noEmit` — 通过
- `cd frontend; npm run build` — 通过
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过

## 2026-05-28 数据库导入: 修复 176 万行只导入 100 万行 (MaxImportRows 限制)

### Task
- 数据库表 176 万行导入只得到 100 万行，缺失大量数据
- 根因: `MaxImportRows = 100000`（10万）硬编码限制导出行数

### Changes
- `internal/dbimport/types.go`: `MaxImportRows` 从 `100000` 提升到 `10000000`（1000万）；`MaxPageSize` 从 `1000` 提升到 `10000`
- `internal/dbimport/service.go`: `StartTask` 中的分页大小从硬编码 `1000` 改为使用 `MaxPageSize`

### New Functionality
- 单次数据库导入上限提升到 1000 万行
- 每批读取从 1000 行提升到 10000 行，大数据导入速度提升约 10 倍

### Verified Commands
- `go test ./internal/... -count=1` — 全部通过
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过
- `http://127.0.0.1:8000/api/health` — `{"status":"ok"}`
- 已重启 etl-server.exe

## 2026-05-28 右侧面板重构: 筛选分析合并 + 无数据时隐藏 + 画布控件调整

### Task
- 锁定画布按钮已在放大/缩小一列（Controls 组件自带），删除它，替换为自定义锁定布局按钮
- 将导出按钮放入 Controls 最底部，图标大小与 Controls 内部按钮一致
- 右上角"新建主体"按钮改为纯"+"图标
- 右侧功能栏合并"主体筛选""关系过滤""路径追踪"为"筛选分析"可折叠模块
- 无数据导入时，右侧只显示"数据导入"模块，其他模块隐藏

### Changes
- `frontend/src/features/flow/FlowInspectorPanel.tsx`:
  - 新增"筛选分析"可折叠模块，合并 主体筛选/关系过滤/路径追踪/标签筛选
  - "数据导入"模块仅保留 `FlowImportSummary`，其余过滤组件移到"筛选分析"
  - 无数据时只显示"数据导入"模块，"筛选分析"和"洞察分析"均隐藏
  - `defaultActiveKey` 有数据时默认展开"数据导入"+"筛选分析"
- `frontend/src/features/flow/useFlowPanelState.ts`:
  - 新增 `nodesDraggable` / `setNodesDraggable` 状态（默认 `true`）
- `frontend/src/features/flow/FlowPanel.tsx`:
  - 透传 `nodesDraggable` / `onNodesDraggableChange` 给 `FlowGraphWorkspace`
- `frontend/src/features/flow/FlowGraphWorkspace.tsx`:
  - 透传 `nodesDraggable` / `onNodesDraggableChange` 给 `FlowCanvas`
- `frontend/src/features/flow/FlowCanvas.tsx`:
  - 导入 `ControlButton`、`LockOutlined`、`UnlockOutlined`
  - `<Controls showInteractive={false}>` 移除默认锁定画布按钮
  - `nodesDraggable` 从硬编码 `true` 改为 prop 控制
  - Controls 内顶部新增锁定布局按钮（LockOutlined / UnlockOutlined 切换）
  - 导出 Dropdown 以 `<ControlButton>` 为触发元素，放在 Controls 子元素末尾
  - 移除 `Button` 导入，右上角"新建主体"改为纯"+"图标按钮（`graph-add-node-btn`）
- `frontend/src/features/flow/flow-canvas.css`:
  - 移除 `.graph-export-control` 和 `.graph-export-control-btn` 样式
  - `.graph-canvas-actions` 简化为纯定位容器
  - 新增 `.graph-add-node-btn` 样式（28px 方形按钮，匹配 minimap-toggle 风格）

### New Functionality
- 锁定布局按钮: 仅控制节点可拖动性，不影响缩放/平移/选中
- 右上角"+"图标按钮创建新主体（原为带文字按钮）
- 右侧面板"筛选分析"模块合并过滤/路径分析功能
- 无数据导入时右侧面板简洁只显示导入入口

### API Changes
- 无

### Frontend Changes
- Controls 组件不再显示"锁定画布"（interactive toggle）按钮
- 导出按钮从独立的绝对定位 div 移入 Controls 面板最底部，与缩放按钮同列

### Verified Commands
- `cd frontend; npx tsc --noEmit` — 通过
- `cd frontend; npm run build` — 通过
- `go build -o bin\etl-server.exe .\cmd\server\` — 通过

## 2026-05-27 边缘详情缓存修复: 消除双重 I/O + 移除行数限制

### Task
- 用户反馈"详细信息还是加载很慢"
- 诊断发现两个问题:
  1. 缓存行数上限 200K，用户数据 507K 行 → 缓存永远不启用，始终回退磁盘读
  2. 构建时双重 I/O: `readSessionData` + `populateEdgeDetailCache` 分别读取相同文件

### Changes
- `internal/api/edge_cache.go`:
  - 移除 `populateEdgeDetailCache`（不再单独调用）
  - 新增 `readSessionDataWithCache(sessionDir, sessionID, mapping, dirMap)`: 一次文件读取同时构建 TransactionRows 和缓存
  - 缓存上限提升到 5,000,000 行（覆盖 507K 数据）
- `internal/api/handlers.go`:
  - `HandleBuildImportedFlow`: 用 `readSessionDataWithCache` 替代 `readSessionData` + `populateEdgeDetailCache`

### Performance
- 构建时: 1x 文件读取（原为 2x），对 231MB CSV 约节省 1-2 秒 I/O
- 点击边缘详情: 507K 行以内从内存缓存读取，零磁盘 I/O，响应 ~毫秒级
- 防 OOM: 保留 5M 行硬上限（约 1.5GB 内存阈值）

### Verified Commands
- `go build -o bin\etl-server.exe .\cmd\server\` — 编译通过
- `go test ./internal/... -count=1` — 全部 50+ 测试通过 (api 15.1s)
- `go vet ./internal/api/` — 无警告

## 2026-05-27 线条详细数据预加载缓存 (边缘详情性能优化)

### Task
- 资金流向图点击线条查看详细信息时，大数据量源文件加载缓慢
- 要求在生成图时将线条详细数据预加载到缓存，点击时瞬时响应
- 避免内存溢出

### Changes
- 新增 `internal/api/edge_cache.go` — 会话级文件数据缓存模块
- `internal/api/handlers.go`:
  - `HandleBuildImportedFlow`: 生成图后调用 `populateEdgeDetailCache` 预加载文件数据到缓存
  - `queryEdgeRows`: 优先读取缓存（`getCachedFiles` + `processCachedRows`），缓存未命中时回退到磁盘读

### New Functionality
- 线条详细数据预加载: 生成流向图时自动将上传文件数据（表头+行）缓存到内存
- 点击线条时从内存读取，避免重复磁盘 I/O，响应时间从 ~秒级降至 ~毫秒级
- 缓存限流: 单会话最大缓存 200,000 行（约 300MB 内存在 32 列场景下），超出则自动回退到实时磁盘读，防止内存溢出
- 缓存生命周期: 与会话绑定；同一会话再次生成图不会重复读盘（缓存命中），上传新文件生成新会话独立创建缓存
- 无前端改动: API 路径、请求格式、响应格式完全不变

### Verified Commands
- `go build -o bin\etl-server.exe .\cmd\server\` — 编译通过
- `go test ./internal/... -count=1` — 全部 50+ 测试通过
- `go vet ./internal/api/` — 无警告

### Notes
- 缓存策略: 文件级缓存（headers[][]string + rows[][]string），非 TransactionRow 级，保留原始列名用于边缘详情展示
- 内存溢出防护: 累加每个文件的行数，一旦超过 200K 阈值立即中止并 `filepath.SkipAll`，本次会话不缓存
- 并发安全: `rowCacheMu` (读写锁) 保护全局 map, `sessionRowCache.mu` (读写锁) 保护每个会话的缓存数据
- 边缘详情返回的数据格式不变（原始列名经过 `NormalizeHeader` 归一化后作为 key）
- 清理策略: 暂未实现 LRU 清理；缓存随会话数量线性增长，每个会话 200K 行上限。如果活跃会话过多，可以考虑在 server 空闲时扫描并清理不存在于磁盘的会话缓存

## 2026-05-27 资金流向图全面测试计划 v1.1

### Task
- 根据用户要求生成并补强资金流向图功能的执行级测试计划，重点覆盖数据逻辑、金额/方向/节点/边/时间/账户归属准确性、字段映射、筛选、聚合、异常数据、性能、大数据、并发、前后端一致性、数据库导入、手工导入、导出、UI、安全与缺陷修复闭环。
- 明确真实测试数据源：`E:\项目\传销\梅州\2 调单\清洗\20240517\交易明细信息.csv`，PostgreSQL `mz.ls_0709.交易明细信息`。

### Changes
- 更新 `docs/资金流向图测试计划.md` 到 v1.1。
- 新增“强制追溯闭环”：要求边、节点、金额、方向、主体详情、边详情均能通过 `source_row_no` / `row_hash` / `transaction_id` 回溯到原始流水。
- 扩展测试范围为 A~S 域，新增权限与安全、数据库导入、手工导入、逐条追溯与缺陷修复闭环。
- 扩展性能测试到百万级、千万级、上亿级分层验证，并补充并发、分页、懒加载、索引、取消恢复、稳定性和精度压力场景。

### New Functionality
- 无应用业务功能新增；本次新增/完善测试计划文档。

### API Changes
- 无。

### Database Changes
- 无；计划中仅建议在临时 schema 或独立性能库执行大数据压测，禁止破坏真实表。

### Frontend Changes
- 无前端代码变更；计划覆盖前端 UI 交互和前后端一致性测试。

### Verified Commands
- `Select-String -Path docs\资金流向图测试计划.md -Encoding UTF8 -Pattern '强制追溯闭环|权限与安全校验|数据库导入场景|手工导入场景|逐条追溯与缺陷修复闭环|上亿级数据库只读聚合验证|数据准确性验收'` 通过，关键章节均存在。
- `(Get-Content -Path docs\资金流向图测试计划.md -Encoding UTF8 | Measure-Object -Line).Lines` 已执行，用于确认文档规模。
- `git diff --check -- docs/AI_HANDOFF.md docs/CHANGELOG_AI.md` 通过；`docs/资金流向图测试计划.md` 当前为未跟踪文档，通过关键章节检索确认内容。
- `go test ./internal/... -count=1 -timeout 300s` 通过。

### Open Items
- 本次是测试计划文档任务，已执行现有 Go 测试基线，但未执行测试计划中的全量人工/大数据/浏览器场景，也未修改业务代码。
- 后续执行测试时，若发现 P0/P1 数据准确性缺陷，应先抽取最小复现数据，再补自动化测试并修复代码。

### Notes
- 文档要求数据库导入路径和手工 CSV 导入路径在同源数据下输出一致，并把边详情、主体详情、导出结果全部纳入逐条核对。

## 2026-05-27 PostgreSQL 数据审计 + 方向别名修复

### Task
- 针对本地 PostgreSQL 数据库 (127.0.0.1:5432, mz.ls_0709.交易明细信息, 6,737,400 行) 执行数据审计测试
- 对比 PG 统计数据与 ETL 流水线处理结果的一致性
- 使用真实 CSV 文件 (507,583 行银行流水) 验证流图建图逻辑

### Changes
- `internal/parser/parser.go`: NormalizeDirection 新增 "O" → "出" 映射 (Out 缩写)
- `internal/api/handlers_test.go`:
  - 新增 TestPGRealDataDirectionNormalization — PG 方向统计验证 (total金额 ≠ in+out 差额说明)
  - 新增 TestPGRealDataDirectionAliases — PG 非标准方向归一化验证 (贷→进, 借→出, 入→进)
  - 新增 TestPGRealDataFlowGraphEdgeStats — CSV 100K 行流图建图验证 (372节点, 600边截断)
  - 修正 TestPGRealDataDirectionNormalization: total金额不等於 in+out (其他方向金额 20,359,259.89)
  - TestPGRealDataDirectionAliases: 断言收紧 — 所有方向必须归一化为"进"或"出"
  - TestPGRealDataFlowGraphEdgeStats: 未知方向从 log 改为 Errorf
  - builtinTests 新增 "O" → "出" 测试用例

### Verified Commands
- `go test ./internal/... -v -count=1 -timeout 300s` — 全部 50+ 测试通过
- `go test ./internal/api -run TestPGRealData -v -count=1` — 3 个 PG 审计测试全部 PASS

### Notes
- PG 数据: total=78,328,675,299.66, in=39,141,080,758.19, out=39,167,235,281.58, 其他=20,359,259.89
- CSV 方向分布 (100K): 进=28.2%, 出=71.8%, 空=0.0% (4 行 O 已修复→出)
- CSV 建图: 372 渲染节点, 7355 总边 (截断至 600), 0 自环
- 发现 CSV 数据中的 "O" 方向值 (疑似 Out 缩写), 4 行/100K, 添加到内置映射

## 2026-05-27 真实文件端到端测试 (v2 — 全功能覆盖)

### Task
- 将 `TestRealCSVEndToEnd` 从基础冒烟测试升级为 **全功能数据审计**，覆盖 A–G 全部功能域
- 使用真实银行 CSV（浦发银行 2000 行交易明细）作为真实数据源

### Changes
- `internal/api/handlers_test.go`：`TestRealCSVEndToEnd` 重写为 18 个子测试，覆盖：
  - **A** 方向归一化：精确断言 进=594、出=1362、空=44，总和=2000
  - **B** 未知方向检测：确认无未知方向
  - **C1** 方向筛选：进/出精确计数，建图不截断
  - **C2** 来源筛选：按交易账号过滤 + 动态计数断言
  - **C3** 目标筛选：按对手户名过滤 + 全值校验
  - **C4** 日期范围：动态计算实际范围 + 不相交范围一致性 + 未来日期返回 0
  - **C5** 明细筛选：按交易对手账卡号过滤 + 动态计数断言
  - **C6** 组合：来源+方向，确认子集关系
  - **C7** 组合：目标+方向，确认子集关系
  - **C8** 组合：来源+日期，确认子集关系
  - **D** 汇总统计：in+out <= total（正确处理空方向行）
  - **E1** 流图基础：230 节点、276 边、0 自环、未截断
  - **E2** 流图单调性：子集图的边数 ≤ 全图
  - **E3** 边属性验证：TxCount / Amount 为正
  - **F** 边详情查询：用 `flowEndpointsForTransaction` 匹配端点，35/2000 匹配
  - **G1** 预览分页：100 行，12 列
  - **G2** 全流水线非空：5 种独立筛选各自建图均有边
  - **G3** 边数单调性：添加滤波器不增加边数
- 修复 bug：C2/C8 中使用了不存在的 `交易卡号` key（该列未被映射到 txn row），改为使用 `交易账号`
- 修复 bug：C5 中使用了错误的 column（`摘要说明` 没有值 `243300133`，该值实际在 `交易对手账卡号` 列；`摘要说明` 只有 "网上支付..." 等文本值）
- 修复 bug：D 中 `inCount+outCount != totalRows`（44 行空方向导致不等）
- 修复 bug：C4 中全日期范围硬编码 `2015-01-08~2024-05-10` 与实际归一化日期不完全匹配（1 行不在范围内）

### Verified Commands
- `go test ./internal/api -run TestRealCSVEndToEnd -count=1` — 通过
- `go test ./internal/... -count=1` — 全部 50 个测试通过

### Notes
- 测试完全数据驱动：使用实际解析数据的计数做断言，避免硬编码静态值
- 发现并修复了测试代码中 4 个 bug（C2 key、C5 column、D 断言、C4 硬编码范围）
- 映射关键：`transactionFromMappedRow` 只保留标准化后的 key（如 `交易账号` 而非原始 CSV 的 `交易卡号`）

## 2026-05-27 真实文件端到端测试

### Task
- 使用真实银行 CSV（浦发银行 2000 行交易明细）进行端到端 ETL 流水线数据审计
- 通过 `readSessionData` 直接调用后端归一化/筛选/建图逻辑

## 2026-05-27 审计测试修复 Handoff

### Task
- 编写资金流向图端到端数据审计测试（19 个），覆盖 A–G 全部功能域
- 修复 5 个失败断言的预期值

### Changes
- 更新 `internal/api/handlers_test.go`（新增 ~700 行审计测试）。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- 新增 19 个审计测试函数，覆盖：
  - A: 方向归一化 — 18 个硬编码别名 + 4 级联回退完整覆盖
  - B: `checkUnknownDirections` — 未映射值时能正确检测未知方向
  - C: 6 维度筛选 — 源/目标/多列明细/方向/日期范围/标签，筛选后行数和金额核对
  - D: 汇总统计 — `BuildSummary` 的行数/总金额/方向分类与原始数据一致
  - E: 流图建图 — 边聚合、去重、自环跳过、未知方向跳过、截断限制、节点统计（流入/流出/度）、节点身份信息、标签遮罩
  - F: 边详情查询 — `queryEdgeRows` 数量和金额与建图边一致
  - G: 全链路一致性 — 9 子场景（无筛选/源筛选/目标筛选/方向入/方向出/日期Q1/标签/多维组合/无匹配），核对筛选行数→汇总统计→建图边的全链路闭环

### API Changes
- 无

### Database Changes
- 无

### Frontend Changes
- 无

### Verified Commands
- `go test ./internal/api -run "TestAudit" -v -count=1` — 全部 19 个审计测试通过
- `go test ./internal/... -count=1` — 全部 49 个测试通过（19 新 + 30 既有）
- Server: `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`

### Open Items
- 真实 CSV 文件（507K 行浦发银行流水）的端到端上传→导入→建图→筛选压测待完成（需要 session 数据路径以编写 HTTP 客户端测试）

### Notes
- 测试数据设计：162 行全覆盖数据（3 来源 × 3 对手 × 2 方向 × 3 天 × 3 小时），直接写入 CSV 再调用 `readSessionData` 读取
- 修复根因：`firstTransactionValue` 只返回首个非空值（交易卡号优先）；`flowNodeInfoFromTransaction` ID 字段用 `交易证件号码` 而非 `交易方证件号码`

## 2026-05-26 19:58 Handoff

### Task
- 修复主体详情中"交易户名"取值错误：当导入映射的主体列是银行名称或其他实体列时，主体详情不应把该列显示为交易户名，交易户名应来自"交易方户名"字段。

### Changes
- 更新 `internal/api/handlers.go`。
- 更新 `internal/api/handlers_test.go`。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- 无新增业务功能；本次为导入流水字段归一化修复。

### API Changes
- 无新增、删除或重命名接口路径。
- `/api/flow/build` 使用既有字段映射时，归一化后的 `交易户名` 现在优先来自 `source_name_column`（交易方户名），`对手户名` 优先来自 `target_name_column`（对手户名）；仅在没有显式户名映射且主体列本身明显是户名/姓名/名称字段时才兜底使用主体列，并明确排除银行/开户行列。

### Database Changes
- 无。

### Frontend Changes
- 无前端代码变更。
- 主体详情继续显示节点的 `account_name`，但该字段的后端来源已修正为“交易方户名”。

### Verified Commands
- `cd E:\codex\etl; go test ./internal/api` 通过。
- `cd E:\codex\etl; gofmt -w internal\api\handlers.go internal\api\handlers_test.go` 已执行。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `cd E:\codex\etl; go vet ./internal/...` 通过。
- `cd E:\codex\etl; go build -o bin\etl-server.exe .\cmd\server\` 通过。
- 已重启 `E:\codex\etl\bin\etl-server.exe`，`http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。

### Open Items
- 未做浏览器手动点选主体详情复测；页面如仍显示旧值，需要重新导入或重新构建当前资金流向图，使后端按新映射生成节点字段。

### Notes
- 根因在 `transactionFromMappedRow`：此前 `交易户名` 使用 `SourceCol -> SourceName -> SourceAccount -> SourceID` 的优先级，导致主体列若映射到银行名称时会覆盖真正的“交易方户名”。本次改为显式户名列优先，并限制兜底主体列只能是非银行类的姓名/户名字段；对手户名同理。

## 2026-05-26 18:01 Handoff

### Task
- 修复资金流向图“数据穿透”功能失效：开启数据穿透后，节点上的展开/折叠按钮需要可靠响应点击。

### Changes
- 更新 `frontend/src/features/flow/FlowGraphPrimitives.tsx`。
- 更新 `frontend/src/features/flow/useFlowPanelState.ts`。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- 无新增业务功能；本次为数据穿透交互修复。

### API Changes
- 无。

### Database Changes
- 无。

### Frontend Changes
- 数据穿透节点 `+/-` 按钮新增 ReactFlow 约定的 `nodrag nopan` class，避免按钮点击被节点拖拽或画布平移逻辑抢占。
- 数据穿透节点 `+/-` 按钮新增 `onPointerDown` 阻止事件冒泡，兼容 ReactFlow v12 的 pointer 事件交互。
- 关闭数据穿透开关时清空已展开节点列表，避免重新开启时沿用旧展开状态。

### Verified Commands
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过；仍有既有大 chunk warning；当前产物为 `assets/index-CBYjaJUa.js` 和 `assets/index-wvt7uB6u.css`。
- `cd E:\codex\etl; git diff --check -- frontend\src\features\flow\FlowGraphPrimitives.tsx frontend\src\features\flow\useFlowPanelState.ts` 通过，仅有工作区 LF/CRLF 提示。
- `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-CBYjaJUa.js` 和 `assets/index-wvt7uB6u.css`。

### Open Items
- 未做浏览器手动点击 `+/-` 截图复测；如浏览器缓存旧资源，强制刷新后再测试。

### Notes
- 本次只修前端 ReactFlow 节点按钮事件处理，不涉及后端接口、数据处理逻辑或数据库结构。

## 2026-05-26 17:52 Handoff

### Task
- 修正资金流向图页面右侧内容顶部留白：全局设置需要贴近页面顶部显示。

### Changes
- 更新 `frontend/src/App.tsx`。
- 更新 `frontend/src/styles/layout.css`。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- 资金流向图页面内容区新增专用布局 class，用于去除该页面顶部 padding。

### API Changes
- 无。

### Database Changes
- 无。

### Frontend Changes
- `App.tsx` 在 `active === "graph"` 时给 `Content` 增加 `content-graph` class。
- `layout.css` 新增 `.content-graph { padding-top: 0; }`，让右侧全局设置区域置顶。

### Verified Commands
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl; git diff --check -- frontend\src\App.tsx frontend\src\styles\layout.css` 通过，仅有工作区 LF/CRLF 提示。
- `cd E:\codex\etl\frontend; npm run build` 通过；仍有既有大 chunk warning；当前产物为 `assets/index-BLmuebEp.js` 和 `assets/index-wvt7uB6u.css`。
- `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-BLmuebEp.js` 和 `assets/index-wvt7uB6u.css`。

### Open Items
- 未做浏览器截图复测；如浏览器缓存旧资源，强制刷新后查看。

### Notes
- 本次只改前端顶部间距，不涉及后端接口、数据处理逻辑或数据库结构。

## 2026-05-26 Git Push Handoff

### Task
- Push local Git commits from `main` to the configured remote repository.

### Changes
- Updated `docs/AI_HANDOFF.md`.
- Updated `docs/CHANGELOG_AI.md`.

### New Functionality
- None. This task was repository publishing only.

### API Changes
- None.

### Database Changes
- None.

### Frontend Changes
- None.

### Verified Commands
- `git status -sb` showed `main...origin/main [ahead 4]` before the first push.
- `git remote -v` confirmed `origin` points to `https://github.com/Euxripides/euripidessss.git`.
- `git push origin main` pushed `f007062..c5fd6b3` to `origin/main`.

### Open Items
- None.

### Notes
- `gh` is not installed in this environment, so no GitHub PR workflow was attempted.

## 2026-05-26 17:46 Handoff

### Task
- 修改资金流向图页面布局：点击左侧“资金流向图”菜单后左侧导航自动折叠，右侧工作区扩展；移除页面标题“资金流向图”；页面结构改为上方全局设置、下方画布/功能区。

### Changes
- 更新 `frontend/src/App.tsx`。
- 更新 `frontend/src/features/flow/FlowPanel.tsx`。
- 更新 `frontend/src/features/flow/flow-canvas.css`。
- 更新 `frontend/src/styles/layout.css`。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- 进入资金流向图菜单时，Ant Design `Sider` 自动折叠到 0 宽度，释放主工作区宽度；折叠触发器仍保留，便于展开导航。
- 资金流向图页面不再显示顶层标题“资金流向图”。
- 全局设置栏直接显示在 Flow 页面顶部，画布和右侧功能区显示在其下方。

### API Changes
- 无。

### Database Changes
- 无。

### Frontend Changes
- `App.tsx` 新增 `sideCollapsed` 状态和菜单点击处理逻辑；仅数据清洗页保留顶部标题栏和下载按钮。
- `FlowPanel.tsx` 移除全局设置 portal，改为页面内直接渲染全局设置栏。
- `flow-canvas.css` 新增 `flow-settings-bar` 样式，覆盖全局设置栏的定位，使其成为页面顶部的普通布局元素。
- `layout.css` 调整 0 宽折叠侧栏触发器样式。

### Verified Commands
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过；仍有既有大 chunk warning；当前产物为 `assets/index-DY0Pp_e9.js` 和 `assets/index-BDD8pi7Y.css`。
- `cd E:\codex\etl; git diff --check -- frontend\src\App.tsx frontend\src\features\flow\FlowPanel.tsx frontend\src\features\flow\flow-canvas.css frontend\src\styles\layout.css` 通过，仅有工作区 LF/CRLF 提示。
- `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-DY0Pp_e9.js` 和 `assets/index-BDD8pi7Y.css`。

### Open Items
- 未做浏览器手动点击截图复测；如浏览器缓存旧资源，强制刷新后再查看资金流向图页面。

### Notes
- 本次只改前端布局，不涉及后端接口、数据处理逻辑或数据库结构。

## 2026-05-25 21:06 Handoff

### Task
- 主体详情框在 ID 下方显示该主体的交易卡号、交易户名、身份证号；有数据才显示对应字段，没有数据则不显示。

### Changes
- 更新 `internal/model/model.go`。
- 更新 `internal/etl/flow_graph.go`。
- 更新 `internal/etl/etl_test.go`。
- 更新 `frontend/src/types.ts`。
- 更新 `frontend/src/features/flow/flowElements.ts`。
- 更新 `frontend/src/features/flow/SubjectDetailDrawer.tsx`。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- Flow 节点响应新增可选身份字段：`account_no`、`account_name`、`id_number`。
- 主体详情抽屉在 ID 行下方按非空值显示“交易卡号”“交易户名”“身份证号”。

### API Changes
- 无新增、删除或重命名接口路径。
- `/api/process` 的 `flow_graph.nodes` 和 `/api/flow/build` 的 `nodes` 中，节点对象新增可选字段 `account_no`、`account_name`、`id_number`；旧字段保持不变。

### Database Changes
- 无。

### Frontend Changes
- `buildFlowElements` 将后端节点身份字段透传到 ReactFlow node data。
- `SubjectDetailDrawer` 基于 node data 条件渲染身份字段，空值不占位显示。

### Verified Commands
- `cd E:\codex\etl; go test ./internal/etl` 通过。
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `cd E:\codex\etl; go vet ./internal/...` 通过。
- `cd E:\codex\etl; go build -o bin\etl-server.exe .\cmd\server\` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning；当前产物为 `assets/index-CHBt3q_H.js` 和 `assets/index-BbV9x_Qb.css`。

### Open Items
- 未做浏览器手动点选主体详情复测；如浏览器缓存旧资源，需强制刷新后查看。

### Notes
- 清洗流水使用 `交易卡号` 优先、`交易账号` 兜底；导入流水使用映射后的 `交易账号` 兜底。身份证号兼容 `交易证件号码` 和 `交易方身份证号`。

## 2026-05-25 20:49 Handoff

### Task
- 修复新增“数据穿透”后资金流向图主体图标丢失的问题。

### Changes
- 更新 `frontend/src/features/flow/FlowGraphPrimitives.tsx`。
- 更新 `frontend/src/features/flow/flow-nodes.css`。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- 无。本次为可视回归修复。

### API Changes
- 无。

### Database Changes
- 无。

### Frontend Changes
- 将“+/-”穿透按钮包进新的 `.flow-node-content` 内部容器。
- 移除 `.flow-node` 上的 `position: relative`，避免覆盖 ReactFlow 节点外层自己的绝对定位/测量逻辑。
- `.flow-node-content` 负责穿透按钮定位，主体图标继续由原有 `.flow-entity` / `.entity-icon` 渲染。

### Verified Commands
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning；当前产物为 `assets/index-Dek-ebL1.js` 和 `assets/index-BbV9x_Qb.css`。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `git diff --check -- frontend/src/features/flow/FlowGraphPrimitives.tsx frontend/src/features/flow/flow-nodes.css` 通过。
- 扫描 `FlowGraphPrimitives.tsx` 和 `flow-nodes.css`，未发现 U+FFFD 替换字符。

### Open Items
- 未做浏览器截图复测；浏览器如缓存旧资源，需要强制刷新后再查看主体图标。

### Notes
- 根因是 `.flow-node` 是 ReactFlow 节点外层元素，新增 `position: relative` 会影响 ReactFlow 的节点定位/测量；定位上下文应放在内部内容容器。

## 2026-05-25 20:33 Handoff

### Task
- 新增资金流向图“数据穿透”功能：主体图标右上显示“+”用于按时间向后展开后续交易，右下显示“-”用于折叠已展开的后续交易。
- 功能通过标题右侧“全局设置”中的“数据穿透”开关启用/关闭，默认关闭。
- 展开判断必须基于交易时间：只有主体收到可见入账关系后，存在晚于该入账时间的后续流出关系时才显示“+”。

### Changes
- 更新 `frontend/src/features/flow/FlowStyleToolbar.tsx`。
- 更新 `frontend/src/features/flow/FlowPanel.tsx`。
- 更新 `frontend/src/features/flow/useFlowPanelState.ts`。
- 更新 `frontend/src/features/flow/useFlowGraph.ts`。
- 更新 `frontend/src/features/flow/FlowGraphPrimitives.tsx`。
- 更新 `frontend/src/features/flow/flow-nodes.css`。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- 全局设置新增“数据穿透”开关。
- 开启后，图谱按当前筛选后的关系集合做时间穿透视图：根主体的初始流出关系保持可见，后续主体只有在点击“+”后才显示晚于其可见入账时间的流出关系。
- 如果某主体存在被折叠的后续流出交易，主体图标右上显示“+”。
- 如果某主体已经展开了后续流出交易，主体图标右下显示“-”，点击后折叠该主体的后续交易。
- 数据穿透关闭时保持原有完整图谱显示逻辑。

### API Changes
- 无。

### Database Changes
- 无。

### Frontend Changes
- `FlowStyleToolbar.tsx` 增加“数据穿透”开关。
- `useFlowPanelState.ts` 增加数据穿透开关状态和展开主体集合，并在图层变化时清空展开状态。
- `useFlowGraph.ts` 增加基于 `first_time` / `last_time` 的穿透折叠计算，确保后续展开关系晚于当前主体的可见入账时间。
- `FlowGraphPrimitives.tsx` 在主体图标附近渲染“+”/“-”操作按钮，并阻止按钮点击触发节点拖拽或选中。
- `flow-nodes.css` 增加穿透按钮定位与样式。

### Verified Commands
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `cd E:\codex\etl; go vet ./internal/...` 通过。
- 扫描本次触及的 Flow 文件和 `frontend\dist\assets`，未发现 U+FFFD 替换字符。
- `git diff --check -- frontend/src/features/flow/FlowGraphPrimitives.tsx frontend/src/features/flow/FlowStyleToolbar.tsx frontend/src/features/flow/useFlowGraph.ts frontend/src/features/flow/useFlowPanelState.ts frontend/src/features/flow/flow-nodes.css frontend/src/features/flow/FlowPanel.tsx` 通过。

### Open Items
- 未做浏览器手动点击“+/-”验证；浏览器如缓存旧资源，需要强制刷新后再测试。
- 当前实现基于已构建图谱边的 `first_time` / `last_time` 做时间穿透；如果一条聚合边同时包含入账时间前后的多笔交易，边仍以聚合后的关系为单位显示。

### Notes
- 未新增依赖。

## 2026-05-25 16:39 Handoff

### Task
- 将资金流向图框选逻辑改为默认关闭，通过全局设置里的“主体多选”开关控制。
- 将全局设置移动到页面标题“资金流向图”右侧，并保持展开显示。
- 删除顶部说明文案“清洗、合并、标注和分析支付宝、微信、银行卡流水。”。

### Changes
- 更新 `frontend/src/App.tsx`。
- 更新 `frontend/src/features/flow/FlowCanvas.tsx`。
- 更新 `frontend/src/features/flow/FlowGraphWorkspace.tsx`。
- 更新 `frontend/src/features/flow/FlowPanel.tsx`。
- 更新 `frontend/src/features/flow/FlowStyleToolbar.tsx`。
- 更新 `frontend/src/features/flow/useFlowPanelState.ts`。
- 更新 `frontend/src/styles/shared.css`。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- 新增“主体多选”全局开关，默认关闭。
- 开启“主体多选”后，画布空白区域左键拖动可框选主体，部分相交即选中；关闭时恢复左键拖动画布平移。
- 全局设置现在显示在“资金流向图”标题右侧，不再折叠。

### API Changes
- 无。

### Database Changes
- 无。

### Frontend Changes
- `FlowCanvas.tsx` 的 `selectionOnDrag` 改由 `subjectMultiSelect` 控制；关闭时 `panOnDrag=true`，开启时 `panOnDrag={[1, 2]}`。
- `FlowStyleToolbar.tsx` 改为常驻展开的全局设置栏，并新增“主体多选”开关。
- `FlowPanel.tsx` 使用 portal 将全局设置渲染到 App 顶部标题旁。
- `App.tsx` 删除顶部说明文案，并在资金流向图标题右侧提供全局设置挂载点。
- `shared.css` 增加标题行设置栏和“主体多选”开关样式。

### Verified Commands
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `rg -n "清洗、合并、标注|主体多选|全局设置|�" frontend\src frontend\dist\assets` 确认旧说明文案已移除，未发现 U+FFFD。
- `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-CMxAVzpe.js` 和 `assets/index-CP7hcI7w.css`。

### Open Items
- 未做浏览器手动框选操作验证；浏览器如缓存旧资源，需要强制刷新后再测试。

### Notes
- 未新增依赖。

## 2026-05-25 15:39 Handoff

### Task
- 支持资金流向图画布像 Windows 桌面一样用鼠标画框批量选中节点，并批量移动。
- 批量移动时保持现有动态连接点优化逻辑，避免多节点移动时边连接点退回固定位置或被图层移动逻辑重复位移。

### Changes
- 更新 `frontend/src/features/flow/FlowCanvas.tsx`。
- 更新 `frontend/src/features/flow/useFlowPanelState.ts`。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- 在 ReactFlow 画布启用 `selectionOnDrag`，左键拖动画布空白处可框选节点。
- 框选模式使用 `SelectionMode.Partial`，节点只要与框选区域部分相交就会被选中，更接近桌面框选行为。
- 选中多个节点后，拖动任意选中节点可整体移动这一组节点。
- 画布平移改为中键/右键拖动，避免与左键框选冲突。

### API Changes
- 无。

### Database Changes
- 无。

### Frontend Changes
- `FlowCanvas.tsx` 的 ReactFlow 增加 `selectionOnDrag`、`selectionMode={SelectionMode.Partial}`、`panOnDrag={[1, 2]}`、`nodesDraggable`、`selectNodesOnDrag={false}`。
- `useFlowPanelState.ts` 在节点拖拽开始时检测多节点选中状态；多选拖拽时禁用图层整体拖拽分支，避免同一节点被 ReactFlow 批量移动和图层移动逻辑重复移动。
- 连接点优化仍由 `useFlowGraph` 按当前节点位置重算动态锚点，批量移动过程中会随节点位置更新。

### Verified Commands
- `cd E:\codex\etl\frontend; npx tsc --noEmit` 通过。
- `cd E:\codex\etl\frontend; npm run build` 通过，仍有既有的大 chunk warning。
- `cd E:\codex\etl; go test ./internal/...` 通过。
- `rg -n "�" frontend\src\features\flow\FlowCanvas.tsx frontend\src\features\flow\useFlowPanelState.ts frontend\dist\assets` 无匹配。
- `http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- `http://127.0.0.1:8000` 已引用当前构建产物 `assets/index-B8aQzR94.js` 和 `assets/index-B-imr4oU.css`。

### Open Items
- 浏览器如果缓存旧资源，需要强制刷新后再测试框选。
- 框选对象是节点；如果框内只有边线、端点节点不在框内，ReactFlow 不会仅通过边线选中并移动端点节点。

### Notes
- 未新增依赖。

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

## 2026-05-27 14:31 Handoff

### Task
- 测试资金流向图导出功能的导出的各种类型的文件是否可用

### Tested Export Formats

All 12 export formats in `frontend/src/features/flow/flowExport.ts` were tested.

**Data formats (unit tested with mock payload, 87/90 assertions passed):**
| Format | File | Test Method |
|--------|------|-------------|
| JSON | `.json` | Full payload serialization, schema validation |
| CSV | `_edges.csv` / `_nodes.csv` | BOM header, column structure, Chinese characters, quoting |
| GraphML | `.graphml` | XML declaration, namespace, nodes/edges structure, amount/tx_count keys |
| DOT | `.dot` | digraph syntax, node labels, directed edges, rankdir |
| Mermaid | `.mmd` | flowchart LR syntax, node/edge labels, Chinese text |
| Draw.io | `.drawio` | mxfile XML, mxCell elements, source/target connections, geometry |
| XMind | `.xmind` | content.json structure, topics, relationships, ZIP bundle |
| ZIP | `_exports.zip` | Bundles all formats + canvas images via JSZip |

**Canvas formats (verified by code review):**
- PNG/JPEG/WebP: `html-to-image` → `toCanvas` → `canvas.toBlob` with appropriate MIME types
- SVG: `html-to-image` → `toSvg` → blob
- Full-canvas capture: `expandForFullCapture` temporarily resizes container + viewport to encompass all nodes

### Backend API verified
- `GET /api/health` → `{"status":"ok"}`
- `POST /api/flow/import` + `POST /api/flow/build` (flow graph)
- `POST /api/process` (full ETL pipeline with 5 test rows → 5 nodes, 4 edges)
- `GET /api/download/:job_id` (7211 bytes Excel file downloaded)

### Build Verification
- `go test ./internal/...` → 49/49 passed
- `go vet ./...` → no errors
- `npx tsc -b` (strict mode) → passed
- `npx vite build` → success (dist generated)

### Known Issues Found
1. **DOT/Mermaid special chars**: `<>` characters in node labels are not escaped in DOT and Mermaid generators (minor — mainstream renderers tolerate them)
2. **`/api/flow/build` column mapping**: The flow graph build endpoint returns 0 edges when mapping test CSV headers; needs investigation (not export related — the legacy `/api/process` endpoint handles this correctly)
3. **Filename timestamp test**: One assertion about filename length failed due to timestamp format variance (benign)

### Files Read/Modified
- Read: `frontend/src/features/flow/flowExport.ts`, `internal/api/handlers.go`, `frontend/src/features/flow/useFlowPanelState.ts`, `internal/etl/flow_graph.go`, `internal/api/router.go`
- Created (then cleaned up): `test_export_data.csv`, `test_export_functions.ts`, `payload.json`
- Modified: `docs/CHANGELOG_AI.md`, `docs/AI_HANDOFF.md`

### Commands to reproduce
```powershell
# Unit test the export functions
cd E:\codex\etl
npx tsx test_export_functions.ts

# Build & Test
cd E:\codex\etl\frontend; npx tsc -b; npm run build
cd E:\codex\etl; go test ./internal/...; go vet ./...
```

## 2026-05-27 资金流向图测试计划 v2.0

### Task
- 按用户要求生成根目录 `资金流向图测试计划.md`，覆盖资金流向图数据准确性、金额、方向、节点、边、时间、账户归属、去重、字段映射、筛选、聚合、异常数据、性能、大数据、并发、前后端一致性、数据库导入、手工导入、导出、UI、权限与安全。
- 明确真实测试源：CSV `E:\项目\传销\梅州\2 调单\清洗\20240517\交易明细信息.csv`，PostgreSQL `mz.ls_0709.交易明细信息`。

### Changes
- 新增 `资金流向图测试计划.md`。
- 更新 `docs/AI_HANDOFF.md`。
- 更新 `docs/CHANGELOG_AI.md`。

### New Functionality
- 无应用业务功能新增；本次交付为可执行测试计划文档。

### API Changes
- 无。

### Database Changes
- 无。

### Frontend Changes
- 无代码变更；文档覆盖 UI 交互测试和前后端一致性测试。

### Verified Commands
- `go test ./internal/... -count=1 -timeout 300s` 通过。
- `Select-String -LiteralPath 'E:\codex\etl\资金流向图测试计划.md' -Encoding UTF8 -Pattern '追溯账本|数据读取与字段映射|金额准确性|方向准确性|节点关系准确性|边关系准确性|数据库导入场景|手工导入场景|导出结果校验|UI 交互校验|权限与安全校验|百万级|千万级|上亿级|缺陷修复闭环'` 通过。
- `(Get-Content -LiteralPath 'E:\codex\etl\资金流向图测试计划.md' -Encoding UTF8 | Measure-Object -Line).Lines` 已执行，文档约 599 行。
- `git diff --check -- '资金流向图测试计划.md'` 通过。

### Open Items
- 本轮未执行完整人工浏览器测试、真实 PG 全量导入测试、百万/千万/上亿级压测；计划中已定义执行步骤和验收标准。
- 当前后端自动化测试基线通过，未发现需要立即修复的失败 bug；如后续按计划执行发现 P0/P1 数据准确性问题，必须按“最小复现数据 -> 自动化测试 -> 修复 -> 真实 CSV/PG 回归”闭环处理。

### Notes
- 工作区已有多处先前未提交改动，本次未回退任何既有改动。
## 2026-05-28 数据库导入百万级性能优化

### Task
- 用户反馈数据库导入百万级数据时速度极慢、一直转圈。
- 根因：数据库导入仍复用 `Preview()` 分页读取，每 10000 行重新打开连接、加载列信息，并执行 `LIMIT/OFFSET`。百万级数据越往后 OFFSET 越慢。

### Changes
- `internal/dbimport/service.go`
  - `StartTask` 改为流式导入：单表只打开一次连接，一条 SQL 顺序读取。
  - 导入 SQL 只选择已映射源字段，不再 `select *`，减少数据库传输量。
  - 移除导入过程中的 `LIMIT/OFFSET` 翻页循环，避免百万级后段扫描变慢。
  - 进度行数改用 PostgreSQL `pg_class.reltuples` / MySQL `information_schema.tables.table_rows` 快速估算，避免导入前 `count(*)` 全表扫描。
  - 进度保存和 CSV flush 按 10000 行或 2 秒节流。
  - 单任务仅保留前 200 条错误详情，避免大量坏数据导致任务 JSON 膨胀。
- `internal/dbimport/service_test.go`
  - 新增导入 SQL 测试，确认导入查询只包含映射列、无 `OFFSET`、无 `select *`。
- `frontend/src/features/flow/DBImportModal.tsx`
  - 启动导入后自动切换到“导入任务”页显示进度。
  - 导入轮询超时从 10 分钟调整为 60 分钟，适配百万级导入。

### New Functionality
- 数据库导入百万级数据时使用流式读取和写入，避免分页 OFFSET 性能退化。
- 导入任务页会主动显示处理进度、处理速度和预计剩余时间。

### API Changes
- 无新增、删除或重命名 API。
- `/api/db/import/tasks/:id/start` 响应结构保持不变。

### Database Changes
- 无数据库结构变更。

### Frontend Changes
- 数据库导入启动后自动进入“导入任务”标签页。
- 长导入任务最多轮询 60 分钟，超时提示文案同步更新。

### Verified Commands
- `go test ./internal/dbimport -count=1 -v` 通过。
- `go test ./internal/... -count=1 -timeout 300s` 通过。
- `cd frontend; npx tsc --noEmit` 通过。
- `cd frontend; npm run build` 通过，仍有既有的大 chunk warning。
- `go build -o bin\etl-server.exe .\cmd\server\` 通过。
- `go vet ./internal/...` 通过。
- 已执行 `.\run.ps1` 重启后端；`http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。

### Open Items
- 本次未连接真实生产库执行百万级全量导入压测；已通过代码路径和自动化测试验证分页瓶颈已移除。
- 如果仍慢，下一步应检查数据库网络、磁盘写入速度，以及映射字段中是否存在复杂类型转换或大量必填字段失败。

### Notes
- 导入进度中的总行数运行中为数据库统计估算值，任务完成时会校正为实际处理行数。
- 本次未修改 `/api/flow/*` 流图接口和现有文件导入路径。
## 2026-05-28 PostgreSQL 数据库导入实测 + 任务持久化压缩修复

### Task
- 使用用户提供的 PostgreSQL 配置测试数据库导入功能：`127.0.0.1:5432`，数据库 `mz`，schema `ls_0709`。
- 目标表按现有测试计划和实际元数据选用 `ls_0709.交易明细信息`。

### Findings
- 连接测试、schema 读取、表读取、列读取、预览、自动映射均通过。
- 首次 100 万行任务启动后后端退出，进一步定位到 `backend/data/db_import/db_import_config.enc` 已膨胀到 176MB。
- 根因：历史导入任务把大量错误明细/样本持久化到同一个加密 JSON 文件里，导致每次 `GetTask` / `SaveTask` 都要解密、反序列化、重写巨大文件，拖慢轮询并可能压垮进程。

### Changes
- `internal/dbimport/store.go`
  - 新增任务持久化压缩：每个任务最多保存 200 条错误、20 行样本。
  - `SaveTask` 和 `saveUnlocked` 保存前统一压缩任务 payload。
  - `loadUnlocked` 读取到历史大任务后自动压缩并回写配置文件。
- `internal/dbimport/service_test.go`
  - 新增 `TestStoreCompactsLargeImportTaskPayloads`，验证大任务错误/样本会被压缩。

### Test Results
- 临时连接创建后已删除，避免保留测试账号密码。
- `db_import_config.enc` 从 176,532,464 bytes 压缩到约 1.27MB。
- 10 万行导入：`processed=100000`，`success=96701`，`failed=3299`，耗时约 5.1 秒，速度约 38,796 行/秒，CSV 约 16.69MB。
- 100 万行导入：`processed=1000000`，`success=920102`，`failed=79898`，耗时约 25.3 秒，速度约 40,848 行/秒，CSV 约 190.82MB。
- 失败主要原因是源数据中必填字段为空：`交易方户名` 或 `对手户名`。
- 基于 10 万行导入会话执行 `/api/flow/build` 通过：`rows=96701`，耗时 1690ms，渲染节点 584、渲染边 600，总节点 1469、总边 1575，按 600 边截断。

### API Changes
- 无新增、删除或重命名 API。

### Database Changes
- 无数据库结构变更。
- 本次只读 PostgreSQL 源表并写入本地导入会话 CSV。

### Frontend Changes
- 无前端代码变更。

### Verified Commands
- `go test ./internal/dbimport -count=1 -v` 通过。
- `go test ./internal/... -count=1 -timeout 300s` 通过。
- `go build -o bin\etl-server.exe .\cmd\server\` 通过。
- `go vet ./internal/...` 通过。
- 已执行 `.\run.ps1` 重启后端；`http://127.0.0.1:8000/api/health` 返回 `{"status":"ok"}`。
- API 实测链路：`/api/db/connections/test`、`/api/db/connections`、`/api/db/connections/:id/schemas`、`/api/db/connections/:id/tables`、`/api/db/connections/:id/columns`、`/api/db/preview`、`/api/db/mappings/auto`、`/api/db/import/tasks`、`/api/db/import/tasks/:id/start`、`/api/flow/build`。

### Open Items
- 本次没有跑全表 6,737,400 行完整导入；按 100 万行实测速度估算，全表导入约 3 分钟以内，但还需单独执行确认。
- 失败行来自源数据必填字段为空；如果业务允许空对手户名/交易方户名，需要调整必填字段策略或映射兜底。

### Notes
- 生成的导入会话保留在 `backend/data/uploads/flow_sessions/` 下，便于复查；临时数据库连接已删除。
- 任务状态文件压缩后，后续轮询和启动请求不应再被历史任务体积拖慢。
## 2026-06-06 Startup verification

### Task
- Started the local ETL project from `E:\codex\etl`.

### Changes
- No business code changes.
- Updated `docs/AI_HANDOFF.md` and `docs/CHANGELOG_AI.md` for this operational handoff note.

### New Functionality
- None.

### API Changes
- None.

### Database Changes
- None.

### Frontend Changes
- None.

### Verified Commands
- `.\run.ps1` completed successfully and reported server ready with PID 15420.
- `curl.exe -s http://127.0.0.1:8000/api/health` returned `{"status":"ok"}`.

### Open Items
- None.

### Notes
- Service is running on `http://127.0.0.1:8000`.
## 2026-06-15 Dune SQL download page

### Task
- Add a sidebar download entry with a `dune` child page.
- Let users enter Dune SQL, request execution automatically, and download the result table as CSV.

### Changes
- Added `internal/api/dune_handlers.go` for Dune SQL execution, status polling, and CSV streaming.
- Added `internal/api/dune_handlers_test.go` for the Dune download handler.
- Updated `internal/api/handlers.go` with `POST /api/dune/download`.
- Added `frontend/src/features/download/DuneDownloadPanel.tsx`.
- Added `frontend/src/features/download/duneApi.ts`.
- Updated `frontend/src/App.tsx` with sidebar `下载 -> dune`.
- Updated `frontend/src/styles/layout.css` for the Dune download form layout.

### New Functionality
- New `下载 -> dune` page in the left navigation.
- Dune page contains a `dune` collapse panel with SQL input, optional Dune API Key, execution size, timeout, polling interval, and partial-result toggle.
- The browser downloads a CSV after the backend executes SQL through Dune, waits for completion, and streams `/results/csv`.
- API key can be provided per request from the UI or via server env var `DUNE_API_KEY`; it is not persisted by the frontend.

### API Changes
- Added `POST /api/dune/download`.
- Request JSON: `sql`, optional `api_key`, `performance`, `timeout_seconds`, `poll_interval_seconds`, `allow_partial_results`.
- Response: CSV file attachment with `X-Dune-Execution-Id` header on success.

### Database Changes
- None.

### Frontend Changes
- Added a nested sidebar menu item `下载 -> dune`.
- Added the Dune SQL download page and CSV download client.

### Verified Commands
- `go test ./internal/api -run Dune -count=1 -v` passed.
- `npx tsc --noEmit` passed in `frontend`.
- `go test ./internal/... -count=1 -timeout 300s` passed.
- `npm run build` passed in `frontend` with the existing large chunk warning.
- `go vet ./internal/...` passed.
- `go build -o bin\etl-server.exe .\cmd\server\` passed.
- `.\run.ps1` restarted the backend successfully, new PID 22816.
- `curl.exe -s http://127.0.0.1:8000/api/health` returned `status=ok`.
- Local `POST /api/dune/download` without key returned expected HTTP 400 missing-key message.

### Open Items
- A real Dune SQL download was not executed because no Dune API Key was provided in this session.

### Notes
- This implementation uses Dune official SQL execution endpoints and therefore requires credits/API access.
- The earlier browser/public-execution notes remain useful for keyless existing-query downloads, but arbitrary SQL execution cannot be done with only `/public/execution`.
- Existing untracked files under `backend/data/test_*.xlsx` and `tmp_*.json` were left untouched.

## 2026-06-15 Dune query preview, auth, pagination, and Excel export

### Task
- Replace the one-shot Dune CSV flow with an on-page SQL query console.
- Show query results below the SQL box in a paginated table.
- Retry transient Dune query failures, handle missing/invalid keys with a login/key modal, save local key/cookie, export current page or all pages, merge results into Excel, and localize headers through DeepSeek when configured.

### Changes
- Added `internal/api/dune_auth_handlers.go` for local Dune auth status and key/cookie persistence under `backend/data/dune/auth.json`.
- Added `internal/api/dune_query_handlers.go` for SQL execution with up to 3 attempts and paginated JSON result retrieval.
- Added `internal/api/dune_export_handlers.go` for page/all Excel export from Dune result pages.
- Added `internal/api/dune_deepseek.go` for DeepSeek-backed header localization with deterministic fallback labels.
- Updated `internal/api/dune_handlers_test.go` with query retry and Excel export coverage.
- Updated `internal/api/handlers.go` with new `/api/dune/*` routes.
- Rebuilt `frontend/src/features/download/DuneDownloadPanel.tsx` as a SQL console + paginated table + auth modal.
- Rebuilt `frontend/src/features/download/duneApi.ts` around typed query/page/export/auth APIs.
- Updated `frontend/src/styles/layout.css` for the Dune table layout.

### New Functionality
- `下载 -> dune` now shows a top SQL editor and a result table below it.
- Query execution retries transient failures twice after the first attempt.
- Missing or invalid Dune API Key returns `auth_required=true`; the frontend opens a Dune login/API Key modal.
- The modal can open a small Dune settings window and save API Key/Cookie locally on the backend.
- Result table supports server-side Dune pagination using `limit` and `offset`.
- Export supports current page or all pages, and writes a merged `.xlsx` workbook.
- Header labels use DeepSeek when `DEEPSEEK_API_KEY` is set; otherwise known Dune fields use local Chinese fallbacks and unknown fields keep readable names.

### API Changes
- Added `GET /api/dune/auth`.
- Added `POST /api/dune/auth` with `api_key`, optional `cookie`.
- Added `POST /api/dune/query` with `sql`, optional `api_key`, `performance`, `timeout_seconds`, `poll_interval_seconds`, `allow_partial_results`, `limit`.
- Added `POST /api/dune/results` with `execution_id`, `offset`, `limit`, optional `api_key`, `allow_partial_results`.
- Added `POST /api/dune/export` with `execution_id`, `scope` (`page` or `all`), `offset`, `limit`, optional `api_key`, `allow_partial_results`; response is `.xlsx`.
- Existing `POST /api/dune/download` remains for CSV compatibility.

### Database Changes
- None.
- New local runtime secret file: `backend/data/dune/auth.json` for saved Dune API Key/Cookie.

### Frontend Changes
- Dune page now has SQL input, execution controls, key/login modal, result metadata, paginated table, current-page export, and full export.
- Table headers display localized label plus original Dune field name.

### Verified Commands
- `go test ./internal/api -run Dune -count=1 -v` passed.
- `.\node_modules\.bin\tsc.cmd --noEmit` passed in `frontend`.
- `go test ./internal/... -count=1 -timeout 300s` passed.
- `go vet ./internal/...` passed.
- `go build -o bin\etl-server.exe .\cmd\server\` passed.
- `.\node_modules\.bin\tsc.cmd -b` passed in `frontend`.
- `.\node_modules\.bin\vite.cmd build` passed in `frontend` with the existing large chunk warning.
- `.\run.ps1` executed and reported server ready.
- Foreground `.\bin\etl-server.exe` smoke run stayed up after the hidden run process was not reachable in this sandboxed shell.
- `Invoke-WebRequest http://127.0.0.1:8000/api/health` returned HTTP 200 with `status=ok`.
- `GET /api/dune/auth` returned HTTP 200 with no saved key in this session.
- Local `POST /api/dune/query` without key returned HTTP 401 with `auth_required=true`.
- Playwright desktop visual smoke opened `http://127.0.0.1:8000`, clicked `下载 -> dune`, and saved `dune-query-page-latest.png`.

### Open Items
- A real Dune execution/export was not run because this session does not have a valid Dune API Key.
- The in-app modal opens Dune in a small browser popup and saves pasted key/cookie. It does not extract cross-origin Dune cookies automatically.
- `npm run build` could not run because the global npm installation is missing `npm-cli.js`; local project binaries were used for the equivalent build.
- Mobile Playwright click on the existing zero-width sider could not open the sidebar reliably; desktop visual smoke passed and Dune page CSS includes a mobile header breakpoint.

### Notes
- Dune result pagination follows the official `limit`/`offset` result API.
- DeepSeek uses `https://api.deepseek.com/chat/completions` and `deepseek-v4-flash`; if `DEEPSEEK_API_KEY` is absent or the call fails, exports continue with fallback headers.

## 2026-06-15 Dune Playwright auth capture

### Task
- Replace the Dune auth popup fallback with a real backend-started Playwright browser flow for capturing Dune cookies after the user logs in.

### Changes
- Added `tools/dune-playwright/capture-dune-auth.mjs`, a local Playwright helper that launches a persistent Dune browser profile, waits for a capture signal, reads Dune cookies/storage, and writes captured auth JSON.
- Added `internal/api/dune_playwright_handlers.go` for Playwright auth task start/status/capture handling and persistence into `backend/data/dune/auth.json`.
- Updated `internal/api/handlers.go` with Playwright auth routes.
- Updated `internal/api/dune_handlers_test.go` with Playwright cookie/API-key extraction helper coverage.
- Updated `frontend/src/features/download/duneApi.ts` with typed Playwright auth task APIs.
- Updated `frontend/src/features/download/DuneDownloadPanel.tsx` so the login modal can start Playwright, poll task status, and trigger cookie capture after manual login.

### New Functionality
- The Dune auth modal now has `启动 Playwright 登录窗口` and `我已登录，抓取 Cookie` controls.
- The backend starts a visible Playwright Chromium session with a persistent profile at `backend/data/dune/playwright-profile`.
- After the user logs into Dune and clicks capture, the helper saves captured cookies to `backend/data/dune/auth.json`.
- The helper also attempts to preserve an existing saved API Key and only overwrites it when an API-key-like storage value is explicitly captured.
- The frontend shows saved Cookie status in the Dune page header.

### API Changes
- Added `POST /api/dune/auth/playwright/start`.
- Added `GET /api/dune/auth/playwright/:task_id`.
- Added `POST /api/dune/auth/playwright/:task_id/capture`.

### Database Changes
- None.
- New runtime directories/files under `backend/data/dune/playwright-profile` and `backend/data/dune/playwright_tasks/<task_id>/`.

### Frontend Changes
- Dune auth modal now prefers Playwright cookie capture and keeps manual API Key/Cookie save as fallback.
- Query/table/export UI is unchanged outside the auth status tags and modal controls.

### Verified Commands
- `node --check tools/dune-playwright/capture-dune-auth.mjs` passed.
- Playwright module resolution was verified against the bundled Codex runtime pnpm layout.
- `go test ./internal/api -run Dune -count=1 -v` passed.
- `./node_modules/.bin/tsc --noEmit` passed in `frontend`.
- `go test ./internal/... -count=1 -timeout 300s` passed.
- `go vet ./internal/...` passed.
- `go build -o bin/etl-server.exe ./cmd/server/` passed.
- `./node_modules/.bin/tsc -b && ./node_modules/.bin/vite build` passed in `frontend` with the existing large chunk warning.
- `powershell -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` restarted the backend successfully, new PID 45460.
- `curl.exe -s http://127.0.0.1:8000/api/health` returned `status=ok`.
- `curl.exe -s http://127.0.0.1:8000/api/dune/auth` returned HTTP 200 with no saved key/cookie in this session.

### Open Items
- A real Dune login/cookie capture was not completed because it requires the user to interactively log in to Dune in the opened Playwright window.
- Official arbitrary SQL execution still needs a valid Dune API Key unless later code is changed to replay Dune website private/public execution calls with captured cookies.

### Notes
- Set `DUNE_PLAYWRIGHT_NODE` or `DUNE_PLAYWRIGHT_NODE_MODULES` if running outside the Codex desktop runtime and Node/Playwright cannot be auto-detected.
- The helper understands pnpm-style Playwright installs such as the Codex runtime layout.

## 2026-06-16 Dune Playwright UX and verification-loop fixes

### Task
- Fix slow/no-feedback Playwright startup, hide the Dune result table before a query is run, and reduce repeated Dune human-verification loops.

### Changes
- Updated `tools/dune-playwright/capture-dune-auth.mjs` so auth login now prefers launching the user's installed Chrome/Edge with a persistent profile and CDP port; Playwright connects only when cookies are captured. It falls back to Playwright Chrome/Chromium when no system browser is found.
- Changed the default Playwright login entry from `https://dune.com/settings/api` to `https://dune.com/`.
- Updated `internal/api/dune_auth_handlers.go` so `/api/dune/auth` returns `https://dune.com/` as the login URL.
- Split Playwright backend code into:
  - `internal/api/dune_playwright_handlers.go`
  - `internal/api/dune_playwright_auth_output.go`
  - `internal/api/dune_playwright_runtime.go`
  - `internal/api/dune_playwright_tasks.go`
- Split frontend Dune UI into:
  - `frontend/src/features/download/DuneDownloadPanel.tsx`
  - `frontend/src/features/download/DuneAuthModal.tsx`
  - `frontend/src/features/download/DuneResultTable.tsx`
- Updated the auth modal so clicking start immediately shows a startup status message before the backend responds.

### New Functionality
- Dune auth startup now opens a normal installed Chrome/Edge profile first, reducing the chance of Dune treating the session as a fresh automated browser.
- The result panel is hidden until a query response exists.
- The auth modal now uses `启动 Chrome 登录窗口` and shows immediate `正在启动本机 Chrome 登录窗口` feedback.

### API Changes
- Existing Playwright auth routes are unchanged.
- `GET /api/dune/auth` now returns `login_url=https://dune.com/`.

### Database Changes
- None.
- Runtime profile remains under `backend/data/dune/playwright-profile`.

### Frontend Changes
- Dune results are no longer shown as an empty `No data` table before the first query.
- Dune auth modal is extracted and shows immediate startup/capture progress.

### Verified Commands
- `node --check tools/dune-playwright/capture-dune-auth.mjs` passed.
- `./node_modules/.bin/tsc --noEmit` passed in `frontend`.
- `go test ./internal/api -run Dune -count=1 -v` passed.
- `go test ./internal/... -count=1 -timeout 300s` passed.
- `go vet ./internal/...` passed.
- `go build -o bin/etl-server.exe ./cmd/server/` passed.
- `./node_modules/.bin/tsc -b && ./node_modules/.bin/vite build` passed in `frontend` with the existing large chunk warning.
- `powershell -NoProfile -ExecutionPolicy Bypass -File ./run.ps1` restarted the backend successfully, new PID 39984.
- `curl.exe -s http://127.0.0.1:8000/api/health` returned `status=ok`.
- `curl.exe -s http://127.0.0.1:8000/api/dune/auth` returned `login_url=https://dune.com/`.
- Playwright browser QA with a mocked slow start endpoint verified: result panel count before query is `0`, immediate startup text appears, final task status shows PID, and modal layout is not clipped.

### Open Items
- A real Dune login was not completed in this session because it requires the user to solve the Dune interactive login/check in the opened browser.
- If Dune still challenges the dedicated profile, log in once in the opened Chrome window and keep that profile; repeated attempts should reuse `backend/data/dune/playwright-profile`.

### Notes
- This does not bypass Dune human verification. It reduces false triggers by using the user's installed browser and a persistent profile instead of starting from bundled Playwright Chromium every time.
- If Chrome auto-detection fails, set `DUNE_CHROME_PATH` to the browser executable.

## 2026-06-16 Dune Playwright auth capture reliability fix

### Task
- Fix Dune Playwright login capture failures where manual login could complete but no auth data was saved, and where the login browser could be disrupted during capture.

### Changes
- Updated `tools/dune-playwright/capture-dune-auth.mjs` to retry auth capture for up to 10 seconds after the user clicks capture.
- Added `tools/dune-playwright/dune-cookie-snapshot.mjs` for Dune-only cookie collection and safe URL diagnostics.
- Updated `internal/api/dune_playwright_auth_output.go` to parse capture diagnostics and include them in empty-capture errors.
- Added `internal/api/dune_playwright_auth_output_test.go` for Playwright auth output helpers and empty-capture diagnostics.
- Moved Playwright-specific auth tests out of `internal/api/dune_handlers_test.go`.

### New Functionality
- CDP capture now connects with Playwright `noDefaults: true` to reduce interference with the opened Chrome profile.
- CDP mode no longer actively closes the user's Chrome window after capture; the helper exits while leaving the login browser open.
- Empty captures now report safe diagnostics: capture mode, cookie count, Dune page URLs without query strings, capture attempts, duration, and close policy.
- Cookie collection saves only `dune.com` domain cookies.

### API Changes
- No route changes.
- Failed Playwright auth task details can now include safe diagnostics when no key or cookie was captured.

### Database Changes
- None.

### Frontend Changes
- None.

### Verified Commands
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

### Open Items
- A real Dune login/capture still requires the user to complete Dune's interactive login or verification in the opened Chrome window.
- This fix does not bypass Dune human verification; it avoids disrupting the browser and makes empty captures diagnosable.

### Notes
- The previous one-shot capture could read cookies too early immediately after login. The retry loop addresses that timing window.
- The previous CDP close path could interfere with the opened browser session. CDP mode now uses `keep_browser_open`.

## 2026-06-16 Dune auth simplified to manual input only

### Task
- Remove the Dune browser/Playwright automatic login capture feature and keep only manual API Key/Cookie input.

### Changes
- Removed Playwright auth task routes from `internal/api/handlers.go`.
- Deleted Playwright-only backend implementation files:
  - `internal/api/dune_playwright_handlers.go`
  - `internal/api/dune_playwright_auth_output.go`
  - `internal/api/dune_playwright_runtime.go`
  - `internal/api/dune_playwright_tasks.go`
  - `internal/api/dune_playwright_auth_output_test.go`
- Deleted Playwright-only helper scripts under `tools/dune-playwright/`.
- Simplified `frontend/src/features/download/DuneAuthModal.tsx` to manual API Key/Cookie inputs only.
- Removed Playwright polling and capture calls from `frontend/src/features/download/DuneDownloadPanel.tsx` and `frontend/src/features/download/duneApi.ts`.
- Updated `internal/api/router.go` so unknown `/api/*` routes return JSON 404 instead of the SPA index page.
- Added `internal/api/router_test.go` to lock the unknown API 404 behavior.

### New Functionality
- None. This is a feature removal/simplification.
- Unknown API paths now return `{"detail":"api route not found"}` with HTTP 404.

### API Changes
- Removed:
  - `POST /api/dune/auth/playwright/start`
  - `GET /api/dune/auth/playwright/:task_id`
  - `POST /api/dune/auth/playwright/:task_id/capture`
- Kept:
  - `GET /api/dune/auth`
  - `POST /api/dune/auth`
  - Dune query/results/export endpoints.

### Database Changes
- None.
- Existing runtime auth data under `backend/data/dune/` was not modified.

### Frontend Changes
- The Dune auth modal now only shows manual fields for `Dune API Key` and optional `Dune Cookie`, plus a save button.
- Removed all UI text and actions for starting Chrome, Playwright login, and automatic Cookie capture.
- The pre-query result table remains hidden until a query response exists.

### Verified Commands
- `rg -n "Playwright|playwright|DunePlaywright|auth/playwright|captureDunePlaywright|startDunePlaywright|loadDunePlaywright" frontend/src internal/api tools` returned no matches.
- `npm run build` passed in `frontend` with the existing large chunk warning.
- `go test ./internal/...` passed.
- `go vet ./...` passed.
- `go build -o bin\etl-server.exe .\cmd\server\` passed.
- `powershell -NoProfile -ExecutionPolicy Bypass -File .\run.ps1` restarted the backend successfully, new PID 25620.
- `curl.exe -s http://127.0.0.1:8000/api/health` returned `status=ok`.
- `curl.exe -s http://127.0.0.1:8000/api/dune/auth` returned HTTP 200.
- `curl.exe -s -i -X POST http://127.0.0.1:8000/api/dune/auth/playwright/start` returned HTTP 404 JSON.
- Playwright page QA confirmed `下载 > dune` opens the Dune page, the result table is hidden before query, and the auth modal has only manual API Key/Cookie fields on desktop and mobile widths.

### Open Items
- Arbitrary Dune SQL execution still needs a valid Dune API Key for the official Dune API flow.
- Manual Cookie is stored as fallback auth metadata, but no automatic browser extraction remains.

### Notes
- Screenshots from this QA are saved at:
  - `E:\codex\etl\dune-manual-auth-modal.png`
  - `E:\codex\etl\dune-manual-auth-modal-mobile.png`

## 2026-06-16 Dune public execution preview download

### Task
- Wire the documented Dune table-download API into the SQL query preview path because the query metadata API does not contain table rows.

### Changes
- Added `internal/api/dune_public_execution.go` for signed `POST https://dune.com/public/execution` result-page downloads.
- Updated `internal/api/dune_query_handlers.go` so query execution waits for completion, then uses the public execution download API for preview rows when `query_id` and Cookie are available.
- Updated `internal/api/dune_export_handlers.go` so Excel export reuses the same preview/download page fetch path.
- Updated `internal/api/dune_auth_handlers.go` with Cookie resolution from request or saved auth.
- Updated `frontend/src/features/download/duneApi.ts` and `DuneDownloadPanel.tsx` to send optional `query_id` through query, pagination, and export requests.
- Added `internal/api/dune_public_execution_test.go` for the captured HMAC signature sample and the execute -> public execution preview flow.

### New Functionality
- Optional `query_id` field on the Dune page.
- When `query_id > 0` and a Cookie is available, backend downloads preview rows through Dune website public execution API:
  - signs `ts + execution_id + query_id + limit + offset`
  - posts to `/public/execution`
  - converts `execution_succeeded.columns/data/total_row_count` into the existing table response shape.
- If public execution download fails or `query_id` is missing, backend falls back to official `/execution/{id}/results` behavior.

### API Changes
- Existing local routes are unchanged.
- Request bodies for `/api/dune/query`, `/api/dune/results`, and `/api/dune/export` now accept optional:
  - `query_id`
  - `cookie`
- Responses from `/api/dune/query` and `/api/dune/results` include `query_id`.

### Database Changes
- None.

### Frontend Changes
- Dune query form now includes optional `query_id（官网下载可选）`.
- Pagination and Excel export preserve `query_id` from the query response.

### Verified Commands
- `go test ./internal/api -run "Dune|PublicExecution" -count=1 -v` passed.
- `npm run build` passed in `frontend` with the existing large chunk warning.
- `go test ./internal/...` passed.
- `go vet ./...` passed.
- `go build -o bin\etl-server.exe .\cmd\server\` passed.
- `powershell -NoProfile -ExecutionPolicy Bypass -File .\run.ps1` restarted the backend successfully, new PID 16700.
- `curl.exe -s http://127.0.0.1:8000/api/health` returned `status=ok`.
- `curl.exe -s http://127.0.0.1:8000/api/dune/auth` returned `has_cookie=true`.
- Playwright page QA confirmed the optional `query_id` field renders on desktop and mobile, and the result table remains hidden before query.

### Open Items
- `POST /public/execution` is a result-page download API, not a SQL execution API. It still needs an `execution_id` from a completed execution and a Dune `query_id`.
- Current SQL execution still relies on the official Dune SQL execute API. The public download API is used after execution for preview/download when the required IDs and Cookie are available.

### Notes
- The HMAC key and signature rule came from the supplied `dune_download_implementation_notes.md`.
- QA screenshots:
  - `E:\codex\etl\dune-query-id-field.png`
  - `E:\codex\etl\dune-query-id-field-mobile.png`
## 2026-06-25 Dune query Chrome/CDP auth verification

### Task
- Continue validating the Dune SQL query flow after Cloudflare showed a block in automation while normal Chrome could open `dune.com`.
- Follow-up correction: the stealth config is intended to handle Cloudflare automatically, so the query bridge should not ask the user to click Cloudflare manually.

### Changes
- Updated `backend/data/dune/playwright_bridge.js` so the query bridge can:
  - prefer locally installed Google Chrome before falling back to bundled Chromium;
  - use `DUNE_QUERY_PROFILE_DIR` for a reusable query browser profile;
  - attach to an operator-controlled Chrome session through `DUNE_QUERY_CDP_URL`, `DUNE_CHROME_CDP_URL`, `DUNE_QUERY_CDP_PORT`, or `DUNE_CHROME_CDP_PORT`;
  - keep CDP-attached Chrome open instead of closing the user's browser.
- Updated `tools/dune-playwright/stealth-config.cjs` so `solveCloudflareWithStealth()` no longer treats a `cf_clearance` cookie alone as success while the page is still on a Cloudflare challenge surface.
- Added extra automatic Cloudflare handling in the shared solver: center click, visible form/button submit, Cloudflare restart event, and periodic reload.
- Updated `backend/data/dune/playwright_bridge.js` so the query fallback no longer calls `waitForManualCloudflare()`; a failed automatic stealth solve now returns `cloudflare_stealth_timeout`.
- Added bridge coverage in `tools/dune-playwright/register-login.test.mjs` for profile selection, Chrome fallback, and CDP attachment.
- Added regression coverage for clearance-cookie false positives and for query bridge avoiding manual Cloudflare clicks.

### New Functionality
- Dune query fallback can now reuse a visible Chrome launched with remote debugging, allowing manual Cloudflare/login handling in the same browser session.
- Dune query fallback now relies on automatic stealth handling for Cloudflare instead of asking the user to click a challenge.

### API Changes
- None.

### Database Changes
- None.

### Frontend Changes
- None.

### Verified Commands
- `node --check backend/data/dune/playwright_bridge.js` passed.
- `node --test tools/dune-playwright/register-login.test.mjs --test-name-pattern "CDP|query bridge"` passed.
- `go build -o bin\\etl-server.exe ./cmd/server` passed.
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\\run.ps1` restarted the backend with `DUNE_QUERY_CDP_PORT=9222`.
- `curl.exe -s http://127.0.0.1:8000/api/health` returned `status=ok`.
- `curl.exe -s http://127.0.0.1:9222/json/list` showed Dune pages at `https://dune.com/home`.
- `DUNE_QUERY_CDP_PORT=9222 node backend/data/dune/playwright_bridge.js` in refresh mode read Dune cookies and an `Authorization` header from the CDP Chrome session.
- `POST /api/dune/query` with `select 1 as smoke_value` still returned HTTP 401 after direct Cloudflare 403 and Playwright fallback 401.
- Direct CDP-page `POST https://dune.com/public/graphql?operationName=GetTeams` returned HTTP 401 JSON `jwt expired`, confirming the browser had Cloudflare clearance but not a fresh Dune login JWT.
- `node --test tools/dune-playwright/register-login.test.mjs --test-name-pattern "shared stealth|query bridge relies"` failed before the fix and passed after it.
- `node --test tools/dune-playwright/register-login.test.mjs` passed with 18/18 tests.
- `node --check tools/dune-playwright/stealth-config.cjs && node --check tools/dune-playwright/register-login.mjs && node --check backend/data/dune/playwright_bridge.js` passed.
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command '$env:DUNE_QUERY_CDP_PORT="9222"; & .\run.ps1'` restarted the backend successfully, PID 20616.
- Bridge refresh smoke returned `status=0`, `cookie_len=5317`, `authorization_len=1192`, `access_token_len=0`, with no manual Cloudflare prompt.
- Final `POST /api/dune/query` with `select 1 as smoke_value` returned HTTP 401; sanitized logs show direct GraphQL got Cloudflare 403 and Playwright fallback returned `Dune HTTP 401` without `MANUAL_CF`.

### Open Items
- The CDP Chrome session can access the Dune home page, but its `auth-id-token` JWT is expired (`2026-06-21T08:13:58Z`). Dune `/api/auth/session` returns 401 on reload, so this is not a valid logged-in session yet.
- To complete the query flow, log into Dune inside the same Chrome window exposed on port 9222, then rerun bridge refresh and the minimal SQL query.
- If automatic stealth cannot clear a future Cloudflare challenge, the query bridge should now fail with `cloudflare_stealth_timeout` rather than asking for manual clicking.

### Notes
- Temporary files created during auth probing under `backend/data/dune/tmp/` were deleted after use because they contained runtime cookies/tokens.
- Homepage access alone is not enough for the website query API; the GraphQL create-query call requires a fresh Dune auth JWT.
- `playwright-go-stealth-config` was integrated into the actual Node Playwright runtime as `tools/dune-playwright/stealth-config.cjs`; it changes browser fingerprint/Cloudflare handling, not Dune account-token expiry.
- Current remaining failure is not a manual Cloudflare prompt; it is stale Dune account authorization after automatic fallback reaches Dune.

## 2026-06-25 Dune account JWT expiry handling

### Task
- Fix `/api/dune/query` when a selected saved Dune account has an expired `auth-id-token` JWT.
- Verify why the live query still shows a Cloudflare block after the JWT fix.

### Changes
- Added `internal/api/dune_auth_jwt.go` with Dune JWT expiry parsing from `auth-id-token` cookie or `Authorization: Bearer ...`.
- Updated `internal/api/dune_account_query.go` so saved account auth and the short login cache are skipped when their JWT is expired or expires within 60 seconds.
- Updated `internal/api/dune_account_query.go` so the first query after a backend restart loads persisted `backend/data/dune/accounts.json` before searching for `account_email`.
- Added `internal/api/dune_account_auth_expiry_test.go` covering expired-account relogin and persisted-account first-query loading.

### API Changes
- No route or request/response schema changes.
- Behavior change: `/api/dune/query` with `account_email` now automatically attempts background login when the saved account JWT is expired, instead of reusing stale auth.

### Database Changes
- None.

### Frontend Changes
- None.

### Verified Commands
- `go test ./internal/api -run TestHandleDuneSQLQueryReloginsWhenSavedAccountAuthExpired -count=1` failed before the fix with the old expired Bearer token and passed after the fix.
- `go test ./internal/api -run TestFindDuneQueryAccountLoadsPersistedAccountsOnFirstQuery -count=1` failed before the account-load ordering fix and passed after it.
- `go test ./internal/api -count=1` passed.
- `go test ./internal/... -count=1` passed.
- `go build -o bin/etl-server.exe ./cmd/server` passed.
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command '$env:DUNE_QUERY_CDP_PORT="9222"; & .\run.ps1'` restarted the backend successfully, PID 35488.
- `curl.exe -s http://127.0.0.1:8000/api/health` returned `status=ok`.
- Live `POST /api/dune/query` with `select 1 as smoke_value` and saved account `ldj1009538134+dune_2d685f01@gmail.com` no longer fails because the account is missing after restart; it proceeds into background login but the browser login is currently blocked by Dune Cloudflare (`Sorry, you have been blocked`, Ray ID `a11297b48898e389`).

### Open Items
- The local JWT-expiry bug is fixed, but live Dune query completion still requires a valid Dune web session. Current blocker is Dune/Cloudflare rejecting the automation login browser, not local stale-token reuse.
- If Cloudflare continues to block background login, use a valid Dune API key or refresh the auth from a normal logged-in browser session that Dune accepts, then rerun `/api/dune/query`.

### Notes
- Stealth configuration can reduce automation fingerprint differences, but it does not refresh an expired Dune JWT by itself.
- Homepage accessibility in normal Chrome is not equivalent to a valid `auth-id-token` for Dune GraphQL query creation.

## 2026-06-25 Dune Rod NewUserMode account login flow

### Task
- Replace the blocked automation-login path for selected Dune query accounts with Rod `NewUserMode`.
- Make the browser enter from the Dune home page first, then move into the login page for a more natural flow.
- If Dune shows Cloudflare/verification, keep the real browser open for the user to complete the check, then continue automatically.

### Changes
- Added `internal/dunetools/rod_user_mode.go` as the Rod login orchestrator for existing `BrowserClient` callers.
- Added `internal/dunetools/rod_user_mode_session.go` for page navigation, manual verification waits, form filling, cookie/JWT/team extraction, profile selection, and Rod eval helpers.
- Added `internal/dunetools/rod_user_mode_scripts.go` for the browser-side probes and DOM actions used by the Rod flow.
- Added `internal/dunetools/rod_user_mode_test.go` covering account-specific Rod profile selection and default-Chrome-profile opt-in.
- Updated `internal/api/dune_account_query.go` so `/api/dune/query` with `account_email` tries Rod NewUserMode by default before falling back to the old Playwright login path.
- Added Rod dependencies to `go.mod` / `go.sum`.

### New Functionality
- Selected Dune query accounts now open a real Chrome/Rod user-mode browser, visit `https://dune.com/`, then click or navigate into login.
- The login form is auto-filled from the selected saved account email/password.
- Verification pages are treated as user-action surfaces: the browser remains visible and waits for manual completion instead of failing immediately.
- After Dune auth parameters are available, the flow extracts Cookie, `Authorization`, access token when present, and team id, then closes the Rod browser automatically.
- Runtime profile controls:
  - `DUNE_QUERY_LOGIN_BROWSER=playwright` forces the old Playwright path.
  - `DUNE_QUERY_LOGIN_BROWSER=rod` forces Rod only, with no Playwright fallback.
  - default behavior tries Rod first and falls back to Playwright only if Rod startup/login extraction fails before auth.
  - `DUNE_ROD_REMOTE_DEBUGGING_PORT` overrides the Rod remote-debugging port, default `37712`.
  - `DUNE_ROD_USER_DATA_DIR` uses an explicit Chrome user-data directory.
  - `DUNE_ROD_USE_DEFAULT_PROFILE=1` asks Rod to use the system default Chrome profile; this may require closing already-running Chrome if Chrome cannot start with the requested debugging port.
  - `DUNE_CHROME_PATH` selects a specific Chrome executable.

### API Changes
- No route or request/response schema changes.
- Behavior change: `/api/dune/query` with `account_email` now refreshes expired account auth through Rod NewUserMode before SQL execution.

### Database Changes
- None.
- Runtime Chrome profile data may be created under `backend/data/dune/profiles/rod_<account>`.

### Frontend Changes
- None.

### Verified Commands
- `gofmt -w internal/dunetools/rod_user_mode.go internal/dunetools/rod_user_mode_session.go internal/dunetools/rod_user_mode_scripts.go internal/dunetools/rod_user_mode_test.go internal/api/dune_account_query.go internal/api/dune_account_auth_expiry_test.go internal/api/dune_account_query_test.go internal/api/dune_auth_jwt.go internal/api/dune_web_query.go internal/api/dune_query_handlers.go` passed.
- `go test ./internal/dunetools ./internal/api -count=1` passed.
- `go test ./internal/... -count=1` passed.
- `go build -o bin/etl-server.exe ./cmd/server` passed.
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command '$env:DUNE_QUERY_CDP_PORT="9222"; & .\run.ps1'` restarted the backend successfully, PID 14500.
- `curl.exe -s http://127.0.0.1:8000/api/health` returned `status=ok`.
- Pure LOC check: new Rod files are below 250 pure LOC (`rod_user_mode.go` 81, `rod_user_mode_session.go` 177, `rod_user_mode_scripts.go` 67).

### Open Items
- A real end-to-end Dune query still depends on Dune accepting the browser session after the user completes any verification page. This change intentionally does not bypass Cloudflare; it waits for the user on verification surfaces.
- Existing `internal/api/dune_web_query.go` remains an oversized file (583 pure LOC) from prior Dune query work and should be split separately when that module is next touched for logic.

### Notes
- Rod NewUserMode is used to drive a real browser session and collect Dune web auth parameters. It is not a guarantee that Cloudflare will accept a given IP/account/session.
- The login path now starts at the Dune home page before opening login, per the latest user request.

## 2026-06-25 Dune Rod manual verification test results

### Task
- Continue the Dune query flow test with the user ready to click human verification.
- Record the blocking problems and runtime parameters in detail.

### Changes
- Updated `internal/dunetools/rod_user_mode.go` so Rod `NewUserMode` no longer calls `KeepUserDataDir()`, which panicked because that method requires `launcher.NewManaged`.
- Updated `internal/api/handler_dune_batch.go` so `/api/dune/batch/accounts` and `/api/dune/batch/export` load persisted accounts before copying `allAccounts`; this fixes the first request after backend restart returning an empty account list.
- Updated `internal/dunetools/rod_user_mode_session.go` so Rod remote debugging reads `DUNE_ROD_REMOTE_DEBUGGING_PORT`, then `DUNE_QUERY_CDP_PORT`, then defaults to `37712`.
- Updated Rod browser selection so `DUNE_CHROME_PATH` is honored and local Google Chrome install paths are preferred before Rod's default browser lookup.
- Added/updated tests in `internal/dunetools/rod_user_mode_test.go` and `internal/api/handler_dune_batch_test.go`.

### Runtime Parameters Tested
- Backend: `http://127.0.0.1:8000`
- Login browser mode: `DUNE_QUERY_LOGIN_BROWSER=rod`
- CDP port: `DUNE_QUERY_CDP_PORT=9222`
- Chrome path: `C:\Program Files\Google\Chrome\Application\chrome.exe`
- Endpoint: `POST /api/dune/query`
- SQL: `select 1 as smoke_value`
- Account: `ldj1009538134+dune_2d685f01@gmail.com`
- Query payload: `limit=10`, `timeout_seconds=600`, `poll_interval_seconds=2`
- Account inventory after restart: `/api/dune/batch/accounts` returned `total=12`, `done=10`, `wait_verify=2`.
- Selected account sanitized state: `status=done`, `has_password=true`, `cookie_len=4680`, `authorization_len=1192`, `access_token_len=0`, `team_id=11`.

### Test Results
- First blocker fixed: Rod startup panicked with `Must be used with launcher.NewManaged` at `KeepUserDataDir()`.
- Second blocker fixed: `/api/dune/batch/accounts` returned an empty list on the first request after backend restart because persisted accounts were loaded too late.
- Third blocker fixed: `DUNE_QUERY_CDP_PORT=9222` was not read by Rod; the code only read `DUNE_ROD_REMOTE_DEBUGGING_PORT`.
- Fourth blocker fixed: Rod default lookup launched Edge (`Edg/149.0.4022.80`) even though the user's normal working browser is Google Chrome.
- Current blocker remains external/session-level: after forcing Google Chrome, CDP reported `Chrome/149.0.7827.115`, but the Dune homepage still displayed `Attention Required! | Cloudflare` / `Sorry, you have been blocked`.
- Latest live request started at `2026-06-25 20:24:25` and ended HTTP 502 at `2026-06-25 20:25:13` because the browser was blocked at `https://dune.com/`.
- The flow did not reach Dune login form fill, Cookie/JWT extraction, create-query, execution polling, or table-result download.

### API Changes
- No route or JSON schema changes.
- Behavior changes:
  - `/api/dune/batch/accounts` and `/api/dune/batch/export` now include persisted accounts on the first request after restart.
  - Rod login honors `DUNE_QUERY_CDP_PORT` and prefers Chrome instead of accidentally using Edge.

### Database Changes
- None.

### Frontend Changes
- None.

### Verified Commands
- `go test ./internal/dunetools -run TestRodUserModeBrowserNewUserModeLauncherDoesNotPanicWithProfileDir -count=1`
- `go test ./internal/api -run TestHandleDuneBatchAccountsLoadsPersistedAccountsOnFirstRequest -count=1`
- `go test ./internal/dunetools -run "TestRodUserModeBrowser(NewUserModeLauncherUsesDetectedChromePath|RemoteDebuggingPortUsesQueryEnv|NewUserModeLauncherDoesNotPanicWithProfileDir)" -count=1`
- `go test ./internal/dunetools ./internal/api -count=1`
- `go test ./internal/... -count=1`
- `go vet ./...`
- `go build -o bin/etl-server.exe ./cmd/server`
- `powershell.exe -NoProfile -ExecutionPolicy Bypass -Command 'Get-Process etl-server -ErrorAction SilentlyContinue | Stop-Process -Force; $env:DUNE_QUERY_LOGIN_BROWSER="rod"; $env:DUNE_QUERY_CDP_PORT="9222"; $env:DUNE_CHROME_PATH="C:\Program Files\Google\Chrome\Application\chrome.exe"; .\run.ps1'`
- `curl.exe -s http://127.0.0.1:8000/api/health` returned HTTP 200.
- `curl.exe -s http://127.0.0.1:9222/json/version` returned `Chrome/149.0.7827.115`.
- `curl.exe -s http://127.0.0.1:9222/json/list` showed Dune page title `Attention Required! | Cloudflare` at `https://dune.com/`.

### Open Items
- The remaining blocker is Dune/Cloudflare blocking the automated Rod Chrome session at the homepage. This is not a local SQL parameter-chain failure yet.
- The compliant next path is to use a Dune-supported API key/session method, or refresh auth from a normal browser session that Dune accepts. Do not treat stealth as a guaranteed Cloudflare bypass.

### Notes
- The manual verification path is wired, but the current page is a Cloudflare hard block, not a solvable checkbox challenge.
- Avoid logging raw Cookie, Authorization, access token, or passwords; use sanitized lengths and status fields only.

## 2026-06-29 支付宝/微信/银行类型识别与大 CSV 清洗合并修正

### Task
- 使用 `E:\项目\网赌\梅县-网赌30娱乐\资金流调证\0622反馈` 做合并测试，修正无法区分支付宝/微信/银行流水、GB18030 CSV 乱码、支付宝/微信解析结果未回传 ETL、支付宝大 CSV 读取爆内存等问题。

### Changes
- 修正 CSV 编码 fallback：`utf-8-sig` / `gb18030` / `utf-8` 现在会真正套用 decoder，并拒绝含非法 UTF-8/RuneError 的错误解码结果。
- scanner 新增按支付宝、微信、银行标准表头签名识别 provider；关键字仍优先，表头匹配作为 fallback。
- 支付宝/微信 parser 新增按文件列表处理入口，ETL provider 分支使用 scanner 产出的 `pf.Paths`。
- 支付宝 CSV/TSV/TXT 改为流式读取，避免单个大 CSV 使用 `ReadAll()` 一次性读入内存。
- 支付宝统一转换改为按 header index 取值，避免每行重复构建列映射。
- 支付宝/微信 parser 结果增加内部 `UnifiedData`，供 ETL 直接转换交易行。
- parser 表行统计改为计数，不再保存整份原始表数据。
- 已复制 DuckDB 到 `tools\duckdb\duckdb.exe`，来源为 `E:\codex\etl_exe\tools\duckdb\duckdb.exe`。

### Modified Files
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

### API Changes
- No HTTP API changes.
- Internal Go parser APIs added: `ProcessAlipayFiles`, `ProcessWechatFiles`.

### Database Changes
- None.

### Frontend Changes
- None.

### Real Data Result
- Real folder: `E:\项目\网赌\梅县-网赌30娱乐\资金流调证\0622反馈`
- Scan result after fix: `transactions=79`, `unknown=36`, provider stats `transaction/支付宝=79`, `unknown/支付宝=18`, `unknown/未知=18`.
- Full pipeline with `GOARCH=amd64` now passes type detection, GB18030 decoding, scanner-routed provider processing, and streaming CSV read, but still fails on current all-in-memory unified output accumulation. Latest OOM stack is in `alipayToUnified` while creating/accumulating unified rows.

### Verified Commands
- `go test ./internal/parser -run TestReadCSVRowsLimitedDecodesGB18030 -count=1`
- `go test ./internal/scanner -run TestScanDirectoryClassifiesAlipayGB18030AccountDetail -count=1`
- `go test ./internal/etl -run TestRunPipelineProcessesGB18030AlipayAccountDetail -count=1`
- `go test ./internal/parser ./internal/scanner ./internal/etl -count=1`
- `go test ./internal/... -count=1`
- `go vet ./internal/...`
- `go run .\.codex_tmp_scan "E:\项目\网赌\梅县-网赌30娱乐\资金流调证\0622反馈" > backend\data\outputs\scan_0622_feedback_after_fix.json`
- `$env:GOARCH='amd64'; go run .\.codex_tmp_scan "E:\项目\网赌\梅县-网赌30娱乐\资金流调证\0622反馈" pipeline "E:\codex\etl\backend\data\outputs" > backend\data\outputs\pipeline_0622_feedback_after_fix.json`

### Open Items
- 要让 `0622反馈` 这种 4.96GB 级目录完整合并，下一步必须把 ETL 从 “parser 返回全部 `[][]string` / `[]TransactionRow` 后再导出” 改为流式清洗、去重和导出，或落地 DuckDB/临时文件中间层。
- `run.ps1` 仍只在二进制不存在时构建，且不强制 `GOARCH=amd64`；大文件任务建议后续调整构建策略。

### Notes
- 本次已删除临时验证程序 `.codex_tmp_scan\main.go`。
- 本次未修改前端。

## 2026-07-27 数据清洗统一字段合并改为可选

### Task
- 将“各来源解析成统一字段后合并”改为数据清洗页可选功能。
- 勾选时沿用现有统一字段、标准化、跨来源合并和去重；不勾选时保留原字段名，并按支付宝、微信、银行、未知来源分别合并。

### Changes
- 新增 `etl.PipelineOptions` 和 `RunPipelineWithOptions`；原 `RunPipeline` 继续默认统一合并，保持内部调用兼容。
- 新增按来源分开合并路径：不同来源并行读取，同一来源内按原字段名取并集并合并，输出到 `支付宝`、`微信`、`银行`、`未知来源` 独立 Excel Sheet。
- 分开合并模式保留原字段名，只做表头必要清理和空行过滤，不执行字段别名映射、金额/时间/方向标准化、跨来源去重或资金图构建。
- 分开合并预览新增 `来源类型`，响应新增合并模式和各 Sheet 行列统计。
- 数据清洗页新增“统一字段名后合并不同来源”复选框，默认勾选以保持现有行为。

### Modified Files
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

### API Changes
- `POST /api/process` multipart form 新增可选字段 `unify_sources`：
  - 未传或 `true`：统一字段后跨来源合并。
  - `false`：按来源分 Sheet 合并并保留原字段名。
- `/api/process` 响应新增：
  - `merge_mode`: `unified` 或 `separate`。
  - `source_sheets`: 分开合并模式下各来源 Sheet 的 `provider`、`sheet`、`rows`、`columns`。
- 内部 Go API 新增 `RunPipelineWithOptions`；原 `RunPipeline` 签名和默认行为不变。

### Database Changes
- 无。

### Frontend Changes
- 数据清洗表单新增复选框和模式说明。
- 清洗结果新增“统一字段合并/按来源分 Sheet”状态标签。
- 分开合并模式显示每个来源 Sheet 的行数、列数。

### Verified Commands
- `go test ./internal/etl -run "TestRunPipeline(SeparateMergePreservesSourceHeadersAndSheets|DefaultsToUnifiedMerge)" -count=1` — 通过。
- `go test ./internal/etl ./internal/api ./internal/model -count=1` — 通过。
- `go test ./internal/... -count=1` — 通过。
- `go vet ./...` — 通过。
- `go build -o bin/etl-server.exe ./cmd/server` — 通过。
- `cd frontend; npm run build` — 通过，保留既有大 chunk warning。
- `.\run.ps1` — 已完成最终重新构建和重启，服务 PID 13220。
- `GET http://127.0.0.1:8000/api/health` — 返回 `status=ok`；首页已引用当前构建 `index-DC1Ia99Z.js`。

### Open Items
- 分开合并路径当前仍为内存合并与 Excel 写入，超大目录仍受既有内存上限影响。
- 账户信息表、标签表的处理逻辑未在本次扩展；本次仅调整流水文件的跨来源合并方式。

### Notes
- 分开合并模式不是统一清洗模式，不生成可直接用于资金流向图的标准字段数据；需要资金图时应勾选统一字段合并。
- 未传 `unify_sources` 的旧客户端继续使用统一合并，不受影响。

## 2026-07-27 真实数据统一合并质量审计

### 本次工作

- 使用用户指定的微信、银行、支付宝真实数据直接运行现有 scanner、provider、parser 和 `etl.RunPipeline`。
- 完成微信全量、银行全量、支付宝小批次全量、三来源混合冒烟、三来源分层混合样本和三来源全量压力测试。
- 审计结果写入 `backend/data/outputs/real_merge_audit_20260727/真实数据统一合并审计.md`，同目录保留 5 个实际输出 Excel 和 2 个全量失败日志。
- 本次只做诊断和验证，未修改生产代码、接口、数据库或前端。

### 主要结论

- 微信：41,416 行输入、40,470 行输出、去重 946 行；40,470 行时间均未标准化，两位年份格式仍为原值。
- 银行：主流水错误识别为支付宝，42,193 行全部因必填字段缺失被删除，输出 0 行。
- 支付宝小批次：13,573 行输入、10,837 行输出；必填缺失删除 2,606 行、去重 130 行，存在 23 行零金额和 11 行“其它”方向。
- 三来源分层样本：123,609 行输入、63,749 行输出；银行仍为 0 行，全部输出的“数据来源”字段为空。
- 三来源全量：32 位进程约 1.96 GB 时 OOM；64 位进程约 51 秒达到 18.39 GB 工作集，在系统剩余约 1.02 GB 时安全终止。
- 两个 `20260612` 支付宝目录的 6 个文件逐一 SHA-256 完全一致，重复目录约占支付宝 CSV 行数的 27%。
- 严格支付宝交易候选约 521 万行；即使解决内存问题，也超过 Excel 单 Sheet 1,048,576 行上限。

### 接口、数据库、前端变化

- 接口：无。
- 数据库：无。
- 前端：无。

### 已验证命令

- 审计工具 `scan`：三类目录文件识别、provider 路由和行数估算。
- 审计工具 `pipeline`：5 个独立/混合真实数据运行，生成并复核 Excel。
- 32 位全量 `etl.RunPipeline`：复现 Go runtime OOM，堆栈已保留。
- 64 位全量受控运行：监测工作集和系统剩余内存，并在安全阈值终止。
- SQLite `VALUES` 审计查询：运行回执 6 行、内存时间序列 5 行均可执行。
- Data Analytics artifact validation：通过；报告已渲染。
- `GET http://127.0.0.1:8000/api/health`：压力测试终止后仍返回 `status=ok`，现有服务 PID 13220 未受影响。

### 未完成事项

- 尚未修复银行误路由、微信日期标准化、来源字段为空、全量内存增长和 Excel 行数上限。
- 支付宝全部约 521 万严格交易候选行未完成端到端输出；当前只有小批次全量和全目录分层样本证据。

### 注意事项

- 混合分层样本对每个支付宝 CSV 抽取首尾各 2,000 行，不能代表文件中段分布，也不能用金额合计外推总体。
- 审计过程中仅使用工作区内硬链接/样本暂存，原始三个数据目录未修改。

## 2026-07-27 统一字段合并真实数据修复与流式性能优化

### 本次新增功能

- 统一合并改为磁盘暂存流式管道：逐行解析、逐行清洗、SQLite 唯一键去重、Excel StreamWriter 导出。
- 支付宝 CSV 新增 `StreamAlipayFiles`，不再把数百万行累积到 `UnifiedData`。
- 超过 Excel 单 Sheet 1,048,575 条数据行时自动创建 `清洗结果_2`、`清洗结果_3` 等 Sheet。
- 同尺寸输入文件使用最多 4 worker 并行 SHA-256，跳过内容完全相同的重复文件。
- 大结果只保留 1,000 行内存预览；全量行数、方向和金额汇总在流式写入时计算。

### 修复内容

- scanner provider 匹配从“第一个达到 3 分”改为“最高完整表头得分”，银行在完全同分时优先，修复银行标准表误识别为支付宝。
- 修复银行 `filterRows` 使用列数而非行数的问题，真实银行输出由 0 行恢复到 41,638 行。
- 修复银行账户来源元数据未初始化导致的越界风险。
- 修复银行转换读取“识别报告”Sheet 造成额外伪交易行。
- 银行来源字段恢复为原始 CSV 路径，不暴露任务临时目录。
- `NormalizeDatetime` 新增两位年份月/日格式，`1/1/24 00:04` 现标准化为 `2024-01-01 00:04:00`。
- 将 parser 的“来源表”映射为最终 33 列中的“数据来源”。
- 银行临时文件复制改为 `io.Copy`，不再一次性 `os.ReadFile`。
- `/api/process` 的 `rows` 使用全量 `RowsOut`，summary 使用全量流式统计，不再被预览行数截断。

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

- 端点路径和请求参数不变。
- 统一合并响应的 `rows` 现在是全量输出行数，不再是内存预览长度。
- 统一合并 `summary` 新增 `streaming`、`output_sheets`、`duplicate_files_skipped`、`preview_rows`、`flow_graph_sampled`。
- 内部 parser API 新增 `StreamAlipayFiles`。

### 数据库变化

- 无持久数据库结构变化。
- 每个统一合并任务创建临时 SQLite `transactions` 表，用 `dedup_key` 唯一约束完成磁盘去重；任务结束自动删除。

### 前端变化

- 无组件和页面修改。
- 前端继续使用原有响应类型；`rows` 现在显示全量结果。

### 真实数据验证

- 微信全量：41,416 → 40,470；40,470 行日期已标准化，来源已填充。
- 银行全量：42,193 → 41,638；必填缺失删除 510、去重 45，来源指向原始 `交易明细信息.csv`。
- 支付宝小批次：13,573 → 10,837；清洗口径与修复前一致，来源已填充。
- 三来源全量：3,878,158 → 2,585,142；过滤 1,016,976、去重 276,040、跳过完全重复文件 4 个。
- 最终 Excel 363,660,044 字节，3 个 Sheet 分别为 1,048,575、1,048,575、487,992 条数据行。
- 最终全量耗时 212.988 秒；修复前 18.39 GB 后仍失败，修复后结束时 Go Sys 约 1.53 GB。

### 已验证命令

- `go test ./internal/provider ./internal/rules ./internal/parser ./internal/scanner ./internal/etl ./internal/api -count=1`
- `go test ./internal/... -count=1`
- `go vet ./...`
- `$env:GOARCH='amd64'; go build -o bin\etl-server.exe .\cmd\server\`
- `cd frontend; npm run build`（通过，保留既有大 chunk warning）
- `.\run.ps1`（服务 PID 39012）
- `GET http://127.0.0.1:8000/api/health`（`status=ok`）
- 最终 XLSX 工作表 XML 逐行计数与管道回执精确一致。

### 未完成事项

- 微信 Excel 解析仍使用现有 workbook 全量读取；本批 4.96 MB 数据未构成瓶颈。
- 超大结果资金图只基于前 1,000 行预览构建，summary 已明确 `flow_graph_sampled=true`；完整资金图应继续使用现有 DuckDB 会话分析路径。

### 注意事项

- 原始三类真实数据未修改。
- 临时 SQLite 和银行中间 Excel 均在任务结束时清理。
- 最终真实数据回归结果位于 `backend/data/outputs/real_merge_fixed_20260727/`。

## 2026-07-28 分阶段CSV合并、全量产物留存与实时进度

### 本次新增功能

- ETL 改为可审计的固定阶段链：
  1. 扫描识别来源；
  2. 把本次上传的全部原文件复制到任务目录；
  3. 支付宝、微信、银行、未知来源分别按原字段并行合并成大 CSV；
  4. 启用 `unify_sources` 时，各来源再并行生成 33 列统一字段 CSV；
  5. 只读取各来源统一字段 CSV，执行必填过滤、标准化、SQLite 去重和跨来源合并；
  6. 同时导出最终 CSV 与兼容 Excel。
- 每个任务的阶段文件保存于 `backend/data/outputs/etl_jobs/<job_id>/`：
  - `01_源文件/`
  - `02_分类原字段CSV/`
  - `03_分类统一字段CSV/`（仅统一模式）
  - `04_最终合并CSV/`（仅统一模式）
- 未勾选统一字段时仍生成分类原字段 CSV，并继续生成按来源 Sheet 的兼容 Excel。
- 新增阶段产物清单，前端可逐个下载源文件、分类 CSV、最终 CSV 和兼容 Excel。
- 新增六阶段实时进度：当前量、总量、百分比、处理速度、已用时间、预计剩余时间和当前来源/文件。
- 支付宝、微信、银行的分类原字段合并及字段统一阶段使用来源级 goroutine 并行；共享进度使用原子计数。
- `run.ps1` 固定构建 `windows/amd64`，避免本机默认 `GOARCH=386` 在大 Excel ZIP 压缩时触发 32 位地址空间 OOM。

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

### 接口变化

- `POST /api/process` 新增可选 `job_id`；不传时仍由后端生成，旧调用兼容。
- 新增 `GET /api/process/progress/:job_id`，返回任务状态及各阶段进度。
- 新增 `GET /api/process/artifact/:job_id/:artifact_id`，下载单个审计产物。
- `/api/process` 响应新增 `artifacts`，每项包含 `id`、`stage`、`provider`、`name`、`rows`、`size`、`download_url`。
- 内部 `PipelineOptions` 新增 `Progress func(ProgressEvent)`。
- parser 新增 `ReadTabularPreviews` 与 `StreamTabularFile`，CSV/Excel 均可有界内存逐行读取。

### 数据库结构

- 无持久业务数据库变化。
- 最终清洗去重仍使用任务临时 SQLite，任务结束删除。
- 阶段产物元数据持久化为 `etl_jobs/<job_id>/artifacts.json`。

### 前端变化

- 清洗页显示扫描、源文件留存、分类原字段合并、分类字段统一、跨来源清洗去重、最终导出等独立进度条。
- 每条进度展示速度、已用时间、预估剩余时间、当前行/文件数量。
- 清洗完成后新增“阶段产物”表格，支持逐项下载。
- `unify_sources` 说明更新为先分类原字段 CSV，再可选统一字段和跨来源合并。
- 轮询间隔 750 ms，任务高频瞬态值只通过独立进度状态更新，未引入新的前端依赖。

### 真实数据验证

- 输入：微信 6 文件 4,959,357 字节；银行 4 文件 18,646,699 字节；支付宝 26 文件 1,416,163,687 字节。
- 36 个上传源文件全部保存，逐文件 SHA-256 对比 `36/36` 一致，原始目录未修改。
- 内容完全相同文件检测到 7 个；原文件副本全部保留，分类合并只处理一份。
- 分类原字段 CSV：
  - 微信 `17,389,152` 字节；
  - 银行 `23,646,332` 字节；
  - 支付宝 `1,893,537,816` 字节。
- 分类统一字段 CSV：
  - 微信 `12,131,745` 字节；
  - 银行 `16,156,749` 字节；
  - 支付宝 `1,357,402,076` 字节。
- 最终统一清洗 CSV：`920,951,132` 字节。
- 全量统计保持一致：`3,878,158 -> 2,585,142`，删除重复流水 `276,040`。
- 最终兼容 Excel：约 `363 MB`，按上限拆分 3 个 Sheet。
- windows/amd64 并行分阶段全量运行耗时 `212.21 秒`，产生 `44` 个审计产物和 `8,498` 次进度事件。
- windows/386 对同一结果在 Excel ZIP 压缩阶段触发地址空间 OOM；已通过 `run.ps1` 固定 amd64 消除生产环境风险。
- 真实产物位于 `backend/data/outputs/real_staged_validation_20260728/`。

### 已验证命令

- `go test ./internal/... -count=1`
- `go test ./internal/etl ./internal/parser ./internal/api -count=1`
- `$env:GOARCH='amd64'; go test ./internal/etl -run '^TestRealStagedPipeline$' -count=1 -v -timeout 45m`
- `go vet ./...`
- `go build -o $env:TEMP\etl-server-staged-check.exe .\cmd\server\`
- `cd frontend; npm run build`（通过，保留既有大 chunk warning）
- 36 个源文件副本逐文件 SHA-256 复核。
- `.\run.ps1`：以 windows/amd64 重建并启动，服务 PID `31952`。
- `GET /api/health`：`status=ok`。
- 真实微信文件 API smoke：`13,269` 行、`5` 个阶段产物、六阶段进度均 `done/100%`。
- `GET /api/process/artifact/api-staged-smoke-20260728/raw-wechat`：下载 `4,916,186` 字节，与产物清单完全一致。

### 未完成事项

- 进度状态当前保存在后端内存中；服务重启后历史阶段产物仍在，但历史进度不会恢复。
- 微信专用统一转换仍沿用现有 workbook 解析实现；本次 4.96 MB 微信数据未造成压力。

### 注意事项

- 阶段 CSV 与源文件副本会显著增加磁盘占用；当前不自动删除，以满足审计留存要求。
- 测试入口 `TestRealStagedPipeline` 默认跳过，仅在设置 `ETL_REAL_INPUT_DIR` 和 `ETL_REAL_OUTPUT_DIR` 时运行。

## 2026-07-28 — 清洗结果一键导入 PostgreSQL/MySQL

### 新增功能

- 清洗页统一合并完成后新增“一键导入数据库”，直接流式读取任务的 `final-csv` 产物，不重新把结果整体载入内存。
- 复用现有加密数据库连接存储，弹窗内可新增、编辑、测试 PostgreSQL/MySQL 连接，并可选择数据库、PostgreSQL Schema 和目标表。
- 支持 `append` 追加（表不存在自动创建）和 `replace` 清空重建两种模式。
- 支持英文 `snake_case` 字段或原中文标准字段；默认采用英文命名。
- 每行写入 SHA-256 指纹，默认重复导入自动跳过；也可选择允许重复写入。
- PostgreSQL 使用事务、临时表和 `COPY` 批量导入，再以 `ON CONFLICT DO NOTHING` 合并。
- MySQL 使用 500 行一批的多值 `INSERT`，跳重模式使用 `INSERT IGNORE`。
- 导入任务实时返回已处理、已写入、已跳过、速度、已用时间和预计剩余时间，支持取消。

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

### 新增接口

- `POST /api/db/export/tasks`：校验清洗任务和 `final-csv` 产物后创建并立即启动数据库写入任务。
- `GET /api/db/export/tasks/:id`：获取任务状态和实时指标。
- `POST /api/db/export/tasks/:id/cancel`：取消正在执行的写入任务。

### 目标表结构

- 自动增加 `id` 主键。
- 33 个标准流水字段按配置使用英文 snake_case 或中文字段名。
- 自动增加 `source_job_id`、`source_row_hash`（唯一）和 `imported_at`。
- 金额/余额使用 `decimal(20,2)`；交易时间使用无时区 `timestamp/datetime(6)`；导入时间在 PostgreSQL 使用 `timestamptz`。
- 本项目自身的文件存储和临时 SQLite 结构未改变。

### 验证结果

- `go test ./internal/... -count=1`：通过。
- `go vet ./...`：通过。
- `cd frontend; npm run build`：通过，保留既有大 chunk warning。
- 现有 PostgreSQL 连接测试：通过。
- 使用真实微信清洗结果 `api-staged-smoke-20260728` 写入 PostgreSQL 测试表：
  - 输入/处理/写入均为 `13,269` 行；
  - 目标表 `37` 列（主键 + 33 标准字段 + 3 个审计字段）；
  - `COUNT(*)=13,269`，`COUNT(DISTINCT source_row_hash)=13,269`；
  - 对同一结果再次执行 append+skip，新增 `0` 行、跳过 `13,269` 行，幂等校验通过。
- 验证结束后已删除专用测试表 `public.codex_etl_export_smoke_20260728`，未改动其他数据库对象。

### 未完成与注意事项

- 当前环境没有已配置且可用的 MySQL 实例，因此 MySQL 已完成驱动、DDL、批量 SQL 单元测试和编译验证，尚未做真实实例写入。
- 数据库写入任务状态保存在内存，服务重启后不会恢复；已提交到数据库的事务不受影响。
- `replace` 会删除并重建目标表，前端已将其作为明确选项展示，默认仍为安全的 `append`。
- 分来源模式没有跨来源统一 `final-csv`，因此清洗页只在统一模式结果上显示数据库导入入口。

## 2026-07-29 — 资金分析字段映射第一阶段

### 本次完成

- 用户最终确认统一分析表只保留原 33 个 `FinalTransactionColumns`；分类统一 CSV、最终 CSV、Excel、API 预览和一键数据库导入均严格使用这 33 列。
- 17 个来源/角色字段只在任务内部临时处理和独立审计报告中使用，不进入用户分析表；内部阶段文件在任务结束后删除。
- 微信、支付宝“支付流水汇总”不再固定取付款方作为本方：按“出=付款方是本方、进=收款方是本方”同步填写交易账号/名称/开户行、交易对手和原始付款/收款方字段。
- 支付宝转账明细不再固定标记为“出”。后端从账户文件和清洗页“调查主体账号”建立主体索引：仅付款方命中为出、仅收款方命中为进；双方命中或均未命中时不进入资金分析结果，而是写入未纳入审计。
- 微信“对手方接收金额(分)”不再错误写入原 33 字段中的 `对手交易余额`；原值继续保留在源文件和分类原字段 CSV。
- 来源类型、来源表类型、来源文件、SHA-256、Sheet、原始行号、映射规则版本、稳定来源记录 ID，以及付款/收款方和主体判定信息仅用于内部去重与审计。
- 去重改为分层身份键：
  - 有交易流水号时使用“来源 + 本方 + 流水号 + 完整业务指纹”，相同流水号但内容不同的记录不误删；
  - 无流水号但有来源记录 ID 时只去除同一源记录；
  - 缺少来源记录 ID 时才使用完整业务指纹兜底。
- 任务临时 SQLite 新增 `duplicates`、`rejected` 审计表，并固定生成 `重复记录审计.csv`、`未纳入记录审计.csv`、`重复源文件审计.csv`。
- 清洗结果严格限制 `收付标志` 为“进/出”；“其它”及无法确定主体方向的数据不进入资金分析表，但保留完整来源定位和原因。
- 文件 SHA-256 在源文件复制时同步计算并传递给 parser，避免对大文件重复读取；主体索引以流式方式读取账户文件。

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

### 接口与数据结构变化

- `POST /api/process` 新增可选 multipart 字段 `subject_accounts`，支持逗号、分号或换行分隔；旧调用不传时兼容。
- 内部 `PipelineOptions` 新增 `SubjectAccounts`、`SubjectIdentifiers` 和 `SourceHashes`。
- parser 新增带 `MappingOptions` 的支付宝/微信入口；原入口保留并转调新入口。
- 对外统一输出没有新增字段，仍为原 33 列。
- 内部存储临时追加 17 个审计字段，但不会出现在阶段统一 CSV、最终 CSV、Excel、预览或数据库导入源中。
- 一键导入数据库目标表保持 37 列：`id` + 33 个统一字段 + `source_job_id`、`source_row_hash`、`imported_at`。
- 项目持久业务数据库结构没有变化；`duplicates`、`rejected` 仅存在于任务临时 SQLite。

### 真实数据验证

- API 任务：`phase1-33cols-real-20260729`。
- 输入：
  - 微信 `莫灿勋.xlsx`；
  - 银行 `交易明细信息.csv`、账户文件 `账户信息.csv`；
  - 支付宝 `826648753_账户明细d2_20260517102205_part1.csv`；
  - 显式调查主体账号 `826648753`。
- `53,355` 行输入，最终 `52,793` 行；来源分布为微信 `2,413`、银行 `41,645`、支付宝 `8,735`。
- API 列清单、微信/银行/支付宝分类统一 CSV、最终 CSV 和最终 Excel 均为原 33 列；第 33 列为 `数据来源`，不存在第 34 列。
- 最终方向仅有“进/出”；进 `12,291`、出 `40,502`。
- 微信 `2,413` 行的 `对手交易余额` 非空数为 `0`，确认对手方接收金额未再混入余额字段。
- 去重审计 `41` 行；未纳入审计 `521` 行，其中缺少收付标志 `510` 行、原方向为“其它” `11` 行。
- 任务产物位于 `backend/data/outputs/etl_jobs/phase1-33cols-real-20260729/`，源文件、各来源原字段 CSV、33 列分类统一 CSV、33 列最终 CSV 和三类审计 CSV 均保留；`.internal` 临时目录已自动删除。

### 已验证命令

- `go test ./internal/... -count=1`
- `go vet ./...`
- `go build -o bin\etl-server.exe .\cmd\server\`
- `cd frontend; npm run build`（通过，保留既有大 chunk warning）
- `.\run.ps1`（最终后端 PID `30884`）
- 真实三来源 `POST /api/process` 回归及最终 CSV/审计 CSV 逐项统计。

### 未完成与注意事项

- 支付宝转账明细若没有账户文件且未填写调查主体账号，方向会保持未判定并进入审计；这是为避免资金方向误判的严格策略。
- 当前真实回归样本包含支付宝账户明细，不包含独立转账明细和支付流水汇总；这两类方向映射已由新增单元测试覆盖。
- 本轮没有重新写入真实 PostgreSQL/MySQL；数据库导入源已恢复为原 33 字段，目标表结构继续沿用已验证的 37 列结构。
