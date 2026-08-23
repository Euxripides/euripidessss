# 地址导入与跨功能复用 UAT 整改及修复方案

> 来源报告：`tmp/qa-evidence-20260815-UAT-01/SUMMARY-UAT.md`  
> 来源批次：`20260815-UAT-01`  
> 编写日期：2026-08-15  
> 当前产品验收：**FAIL**  
> 当前测试完整度：**PARTIAL**  
> 适用对象：产品修复 Agent、测试 Agent、复核 Agent

## 1. 技术摘要

本轮 UAT 已证明真实文件导入、坏文件恢复、风险无数据语义、资金追踪基础交互、白色无点阵画布、跨链修复后选择、移动端和键盘基础路径可用，但尚未满足产品验收门槛。

必须完成以下整改后才能重新申请验收：

1. 修复浏览器后退进入 `about:blank` 的白屏问题。
2. 修复 Explorer 深链接刷新后 URL 与实际页面不一致的问题。
3. 在同一固定构建版本上重新验证跨链地址的鼠标、键盘和快速输入行为。
4. 在调度器和 Provider 确认可用的环境中完成真实暂停、恢复和取消状态流转。
5. 新建一个能够产生非零数据的真实任务，贯通创建、运行、结果、Parquet、ClickHouse、页面和导出文件。
6. 修订 UAT 报告统计方式，禁止重复计算共享断言文件，完整披露全部失败记录。
7. 将离线、500 和非法输入测试改为应用内错误恢复验证，不能只证明浏览器显示系统离线页。

本整改不得通过修改断言条件、复用历史结果或直接调用接口制造业务状态来获得 PASS。

## 2. 范围、证据与判定定义

### 2.1 整改范围

本方案仅处理 `20260815-UAT-01` 暴露的以下问题：

- 产品导航与路由状态。
- 跨链统一地址输入框回归。
- Smart Download 真实任务生命周期。
- 本轮任务与结果数据的端到端关联。
- 应用内错误反馈与恢复。
- UAT 断言、统计和报告审计性。

不在本方案内重构无关模块，不修改 API 路径，不引入新依赖。

### 2.2 证据来源

主要证据：

- `tmp/qa-evidence-20260815-UAT-01/SUMMARY-UAT.md`
- `tmp/qa-evidence-20260815-UAT-01/*-assertions.json`
- `tmp/qa-evidence-20260815-UAT-01/*-console.json`
- `tmp/qa-evidence-20260815-UAT-01/*-network.json`
- `tmp/qa-evidence-20260815-UAT-01/verify-*.json`
- `tmp/qa-evidence-20260815-UAT-01/export-smart_download_41a067d0_99.xlsx`
- `tmp/qa-evidence-20260815-UAT-01/uat-*.cjs`
- `tmp/qa-evidence-20260815-UAT-01/uat-verify.ps1`

验收规范：

- `docs/ADDRESS_LIBRARY_USER_E2E_TEST_FLOW.md`
- `docs/ADDRESS_LIBRARY_FUNCTIONAL_TEST_CASES.md`

### 2.3 状态定义

| 状态 | 定义 |
|---|---|
| PASS | 用户操作、页面结果、控制面状态、数据产物和只读核证全部一致 |
| FAIL | 已执行且任一功能结果、数据语义、恢复行为或证据不满足需求 |
| BLOCKED | 外部条件客观不可用，已保存阻断证据和解除条件；不得记为 PASS |
| PARTIAL | 旅程部分完成，或只证明历史数据、请求接受、按钮存在、页面未崩溃 |

产品验收只有 PASS 或 FAIL。测试覆盖可以是 COMPLETE 或 PARTIAL。

## 3. UAT 报告必须先纠正

### 3.1 正确结论

`SUMMARY-UAT.md` 的开头应修订为：

```text
产品验收：FAIL
测试覆盖：PARTIAL
主旅程断言：114 PASS / 10 FAIL
只读核证：11 PASS / 0 FAIL
全部落盘断言：125 PASS / 10 FAIL
```

产品验收为 FAIL 的原因：

- UJ-01 后退白屏为实际功能失败。
- UJ-04 未完成暂停和恢复终态。
- 本轮不存在非零结果的真实任务闭环。
- UJ-10 刷新后的 URL 与页面功能不一致。

### 3.2 重新归属断言

应根据每条断言的 `check` 前缀归属旅程，而不是把共享文件总数重复写给每个旅程。

| 旅程 | PASS | FAIL | 整改后结论 |
|---|---:|---:|---|
| UJ-01 | 7 | 1 | FAIL |
| UJ-02 | 23 | 0 | PASS |
| UJ-03 | 4 | 0 | PASS，仅证明创建 |
| UJ-04 | 13 | 9 | PARTIAL/BLOCKED |
| UJ-05 | 7 | 0 | PARTIAL，仅证明历史结果查看与导出 |
| UJ-06 | 14 | 0 | 需复测，存在链断言假通过 |
| UJ-07 | 9 | 0 | PASS |
| UJ-08 | 15 | 0 | PASS，过滤效果需补强 |
| UJ-09 | 6 | 0 | 修复后定向 PASS |
| UJ-10 | 4 | 0 | FAIL，断言未检查 URL 与页面一致性 |
| UJ-11 | 5 | 0 | PARTIAL，离线场景未证明应用内反馈 |
| UJ-12 | 7 | 0 | PASS，修复后需做链选择回归 |
| 只读核证 | 11 | 0 | 部分有效，不构成本轮非零下载闭环 |

### 3.3 删除或改写的不实表述

必须修订以下表述：

1. 将“UJ-05 结果数据与导出 PASS”改为“历史结果查看与导出 PASS，本轮任务闭环未完成”。
2. 将“暂停终态受下载时长限制”改为证据实际显示的“任务未进入 RUNNING，调度器/执行条件未满足”。
3. 将“真实数据核对 11/11 PASS”拆分为地址库、历史结果、本轮零行任务三种不同对象。
4. 不得把 `ea594cfb` 与 `41a067d0` 写成同一条结果链。
5. 将 UJ-10 的 URL/页面错位从观察项提升为功能缺陷。
6. 将“UJ-01..12 全部执行”改为“UJ-01..12 均有尝试，UJ-04、UJ-05、UJ-10、UJ-11 未完整满足验收语义”。

### 3.4 报告生成规则

新增一个只读汇总脚本，输入所有 `*-assertions.json`，按 `check` 前缀输出：

- 旅程编号。
- 断言总数。
- PASS 数。
- FAIL 数。
- BLOCKED/SKIPPED 数。
- 失败断言名称和证据文件。

报告中的数字必须由脚本生成，不允许手工抄写共享文件总数。

## 4. UAT-FIX-001：修复浏览器后退白屏

### 4.1 问题

用户从新标签页打开 `/`，进入功能页后点击浏览器后退，页面跳到 `about:blank`，应用完全消失。

当前关联代码：

- `frontend/src/features/explorer-intelligence/analysisContext.tsx`
- `frontend/src/App.tsx`

已知行为：

- Explorer 状态同步使用 `history.replaceState`。
- 菜单切换只修改 React `active` 状态和 Local Storage，不产生浏览器历史项。
- App 初始化优先读取 `etl.app.active.v1`，没有建立完整的 URL 到页面状态映射。

### 4.2 修复要求

建立统一的应用导航函数，至少满足：

1. 首次从 `/` 进入默认页面时，应用历史中保留一个可返回的应用入口。
2. 用户点击一级/二级菜单时，使用明确的应用 URL 和 `pushState` 产生历史项。
3. `popstate` 发生时，根据当前 URL 恢复 `active`、链、地址和 Explorer 查询状态。
4. 不得将 `about:blank` 作为应用内部后退目标。
5. Local Storage 只能作为无明确 URL 时的偏好回退，不能覆盖显式深链接。
6. 后退和前进不得丢失已持久化地址、任务或搜索目标。

推荐结构：

```text
navigateTo(page, routeState, mode = push)
    → 计算 canonical URL
    → setActive(page)
    → 更新对应页面状态
    → pushState/replaceState
    → 按配置记录最后页面

window.popstate
    → parseRoute(location)
    → 恢复 active 和页面状态
```

不要只添加“检测 about:blank 后刷新”的补丁；应用无法控制已经离开的 `about:blank` 文档，而且这不能解决历史栈和深链接语义。

### 4.3 单元与组件测试

至少覆盖：

1. `/` 首次加载后历史仍包含应用入口。
2. Explorer → Smart Download → 后退，返回 Explorer。
3. 再前进，返回 Smart Download。
4. 连续切换三个菜单后逐级后退。
5. 地址详情 URL 后退到 Explorer 首页。
6. `rememberLastPage=true` 不覆盖显式 Explorer 地址 URL。

### 4.4 用户级验收

真实浏览器完成：

1. 新标签打开首页。
2. 点击“下载中心 → 下载任务”。
3. 点击浏览器后退。
4. 页面必须仍在应用内并显示上一个页面。
5. 点击前进，回到下载任务页。
6. 刷新后页面与 URL 保持一致。

不得只断言 `body.length > 0`；必须同时核对 URL、菜单高亮、页面标题和主要控件。

## 5. UAT-FIX-002：修复深链接刷新后页面错位

### 5.1 问题

Explorer 地址详情 URL 刷新后仍为：

```text
/explorer/bsc/address/<address>
```

但 App 根据 Local Storage 恢复为 Smart Download 页面，导致地址栏和页面内容冲突。

### 5.2 修复要求

App 初始化顺序改为：

1. 解析当前 URL。
2. URL 命中已支持路由时，以 URL 为最高优先级。
3. URL 为 `/` 或无业务含义时，再读取最后页面偏好。
4. 解析 Explorer 链、地址、查询参数并初始化 `AnalysisContext`。
5. 页面状态稳定后再写回 Local Storage，不得在初始化阶段反向覆盖显式 URL。

建议建立纯函数：

```ts
type AppRoute = {
  active: string;
  chain?: string;
  address?: string;
  query?: Record<string, string>;
};

parseAppRoute(location): AppRoute | null
buildAppURL(route): string
```

解析和生成必须互为可逆操作，并用表驱动测试覆盖。

### 5.3 用户级验收

1. 在 Explorer 打开 BSC 地址详情。
2. 复制当前 URL。
3. 刷新页面，页面仍为同一链同一地址详情。
4. 新建无痕 Context 直接粘贴 URL，仍进入同一详情。
5. 服务重启后再次打开 URL，仍一致。
6. 验证菜单高亮为 Explorer，而不是 Smart Download。

任一场景 URL、菜单、页面或地址不一致即 FAIL。

## 6. UAT-FIX-003：跨链同地址修复回归

### 6.1 当前状态

`frontend/src/features/address-library/AddressLibraryInput.tsx` 已将 option 唯一值改为：

```text
chain_key:address
```

选择时再把真实地址回写业务输入。这个方向正确，但原 UJ-06 证据来自修复前版本，不能直接继承为修复后 PASS。

### 6.2 必须保留的实现约束

1. React `key` 和 AutoComplete `value` 使用链+地址组合键。
2. 显示内容包含地址、链和状态。
3. 回写业务输入只能写裸地址，不得把 `bsc:` 或 `eth:` 传入 API。
4. `onSelect` 必须使用当前 option 携带的 `item`，不能再按裸地址二次查找。
5. 快速输入请求必须只采用最后一次请求结果。
6. 组件外的跳转必须使用选中 `item.chain_key`。

### 6.3 自动化测试

加入组件测试或最小前端逻辑测试：

- 同地址 BSC/ETH 两个 option 的 key/value 不相同。
- 点击 BSC 返回 `chain_key=bsc`。
- 点击 ETH 返回 `chain_key=eth`。
- 键盘选择和鼠标选择结果相同。
- 输入 A 后快速改为 B，A 的迟到响应不得覆盖 B。
- 清空输入后建议列表关闭。

### 6.4 用户级复测

在同一固定构建版本上重跑：

- UJ-06 全局搜索进入 Explorer。
- UJ-09 跨链同地址。
- UJ-12 键盘选择。

每次选择必须同时断言：

```text
建议项链 == URL 链 == 页面链标签 == 请求 chain 参数
```

## 7. UAT-FIX-004：完成真实暂停、恢复和取消生命周期

### 7.1 当前证据边界

本轮后端接受了部分暂停请求，但测试未观察到完整状态流转：

```text
RUNNING → PAUSE_REQUESTED → PAUSED → RUNNING
```

部分批次长期停留 CREATED。该现象首先视为环境/调度阻断，未进一步证明前不得直接认定为 `PauseBatch` 后端缺陷。

关联代码：

- `frontend/src/features/smart-download/SmartDownloadPage.tsx`
- `internal/smartdownload/service.go`
- `internal/smartdownload/recovery.go`
- `internal/smartdownload/model.go`
- Smart Download API handlers 和路由

### 7.2 测试前环境诊断

在创建生命周期任务前保存：

1. `/api/health`。
2. Smart Download adapters/Provider 可用性。
3. 调度器 worker 数量。
4. 当前活动任务和队列数量。
5. ClickHouse/数据平面状态。
6. Provider 的链与数据集支持矩阵。
7. 后端日志中任务认领、range 启动和失败原因。

只有观察到任务实际进入 RUNNING，才开始暂停/恢复验收。

### 7.3 暂停/恢复任务设计

选择一个预计运行 1～5 分钟、能够生成多个 range 的小规模任务。范围太小会在点击暂停前结束，范围过大会造成不必要副作用。

必须观察和保存：

1. UI 创建任务。
2. CREATED/QUEUED。
3. RUNNING，且出现 `started_at`、当前 Provider 或 range 进度。
4. 用户点击暂停。
5. 页面出现“暂停请求已提交”或等价反馈。
6. 控制面出现 `pause_requested=true`。
7. 当前运行 range 安全落盘后进入 PAUSED。
8. PAUSED 状态刷新和浏览器重开后保持。
9. 用户点击恢复。
10. 状态重新进入 RUNNING。
11. checkpoint/range ledger 没有从头重复下载已完成 range。
12. 最终进入 COMPLETED/PARTIAL，并保留已提交数据。

如果 `pause_requested=true` 超过合理窗口仍不进入 PAUSED，必须保存运行 range、Provider、日志和 ledger，作为后端缺陷处理。

### 7.4 取消任务设计

使用另一个独立任务：

1. 等待进入 RUNNING。
2. 用户点击取消并确认。
3. 页面进入 CANCELED。
4. 刷新、浏览器重开和服务重启后仍为 CANCELED。
5. 不再产生新的 range 或行数增长。
6. 已提交数据按当前产品语义保留，未提交临时文件按规则清理。

暂停任务和取消任务不得使用同一个批次。

### 7.5 后端修复触发条件

只有满足以下任一条件才修改后端：

- RUNNING 批次收到暂停请求后，所有 running range 已结束仍不进入 PAUSED。
- 恢复后状态变为 RUNNING，但 worker 不再认领缺失 range。
- 取消后仍继续产生新数据。
- 服务重启后 pause/cancel 状态丢失。
- checkpoint 恢复导致已完成 range 重复下载。

后端修改后必须补充 `internal/smartdownload` 状态机和恢复测试，不得只改前端按钮可用条件。

## 8. UAT-FIX-005：建立本轮非零数据真实闭环

### 8.1 当前问题

本轮新建完成批次 `ea594cfb` 为零行任务；导出的 99 行 XLSX 属于 2026-08-12 的历史任务 `41a067d0`。二者不能拼接成同一闭环。

### 8.2 测试任务选择

选择具备已知活动的地址和较小区块窗口：

- 预检预计行数必须大于 0。
- 数据集优先使用 `token_transfers` 或当前稳定 Provider 支持的数据集。
- 区块范围控制在能够于合理时间完成的大小。
- 地址必须通过本轮 UI 导入或从地址资产库由用户选择。
- 禁止直接通过 API 创建批次。

### 8.3 强制关联键

从创建开始记录：

| 层级 | 必须记录 |
|---|---|
| 用户输入 | RUN_ID、链、地址、数据集、起止区块 |
| 批次 | batch_id、创建/开始/结束时间、终态 |
| 地址任务 | address_job_id、地址、状态 |
| 数据集任务 | dataset_job_id、Provider、range、row_count |
| 文件 | merged_parquet、大小、实际行数、校验状态 |
| 入仓 | ClickHouse 表、地址、数据集、区块范围、行数 |
| 页面 | 结果页地址、数据集、行数、抽样行 |
| 导出 | 文件名、大小、Sheet、表头、实际行数、抽样行 |

所有层级必须能通过 batch/address/dataset/address/range 中至少一个稳定键连接。不能只证明“某处存在一个完成任务”和“某处存在一个有数据文件”。

### 8.4 数据守恒与质量核对

至少核对：

```text
writer_input
  = writer_success + writer_reject

导出数据行数
  = 结果页声明行数
  = 对应 dataset_job 的可导出行数
```

ClickHouse 因活动双向展开导致行数与逻辑交易行不同的，必须明确展开规则，并按唯一业务键复算，不得直接要求物理行数相等。

必填字段抽检：

- chain_id/chain_key。
- address。
- block_number。
- transaction_hash。
- from_address/to_address。
- dataset。
- source_provider/source_range_id。
- ingested_at。

### 8.5 导出文件验证

不能只检查 XLSX 的 ZIP 头。必须真实打开工作簿并核对：

1. Sheet 数和名称。
2. 使用范围。
3. 表头集合。
4. 实际数据行数。
5. 地址、链、区块范围。
6. 首、中、末抽样行。
7. 与结果页和 dataset job 的一致性。
8. 文件能够由常用表格软件正常打开。

## 9. UAT-FIX-006：修复和补测应用内错误恢复

### 9.1 非法地址反馈

当前只提示“生产预检失败，未创建任务”，没有指出具体输入问题。

修复要求：

- 在用户提交前校验地址格式。
- 显示无效地址数量和至少一个可定位的样例或行号。
- 保留用户其余表单配置。
- 修正后可直接再次提交。
- 不创建批次或后台任务。

### 9.2 离线测试

本轮证据主要是 Chrome 系统离线页面，不足以证明应用内错误处理。

正确测试方式：

1. 保持应用页面已经加载。
2. 对地址建议、预检、Explorer 或风险请求注入离线/超时。
3. 页面必须显示应用自己的错误反馈，而不是整页浏览器恐龙页。
4. 原有数据不应被错误清空或串到其他地址。
5. 恢复网络后点击重试或重新操作，功能恢复。

### 9.3 500 与超时

分别验证：

- HTTP 500。
- 请求超时。
- 响应格式错误。
- 请求 A 失败后请求 B 成功。

错误响应不得残留旧建议、旧风险评分、旧图节点或旧结果详情。

## 10. UAT-FIX-007：整改断言和证据采集

### 10.1 断言必须检查业务语义

错误示例：

```text
URL 包含 /explorer/ 且包含地址 → PASS
```

正确示例：

```text
选中建议链 == URL 链
URL 地址 == 页面地址
页面链标签 == 请求 chain 参数
页面数据属于当前地址且不含上一个地址残留
```

### 10.2 禁止弱断言

以下断言不能单独构成 PASS：

- 按钮存在。
- 按钮被点击。
- 请求返回 200。
- 页面未崩溃。
- 文件非零。
- XLSX 头为 `PK`。
- 任务状态为 COMPLETED。
- 控制台为空数组。
- 点击了缩放或筛选按钮，但结果没有可验证变化。

### 10.3 每条断言结构

建议统一为：

```json
{
  "run_id": "20260815-UAT-R01",
  "journey": "UJ-06",
  "check": "uj06-selected-chain-consistent",
  "status": "PASS",
  "expected": "bsc",
  "actual": "bsc",
  "evidence": [
    "uj06-bsc-selected.png",
    "uj06-network.json"
  ],
  "build_id": "<git-sha-or-binary-hash>",
  "timestamp": "<ISO-8601>"
}
```

不得再用布尔 `pass` 表示 BLOCKED 或 SKIPPED；状态必须为明确枚举。

### 10.4 网络证据

网络文件不得只保存空数组而没有采集说明。至少保存：

- 方法。
- URL。
- 状态码。
- 发起时间和耗时。
- 与当前断言相关的精简响应字段。
- 是否属于受控预期错误。

敏感字段必须脱敏。

### 10.5 固定构建版本

一次完整 UAT 不得在中途修改代码后继续累计为同一结果。

正确方式：

1. 记录初始 build ID。
2. 发现缺陷后结束该版本对应旅程并记 FAIL。
3. 修复、构建、重启，生成新的 build ID。
4. 创建独立 RETEST RUN_ID。
5. 只在复测报告中记录修复后的结果。
6. 最终汇总同时保留“首次发现”和“修复复测”两个事实。

## 11. UAT-FIX-008：补强资金追踪交互证据

现有截图和断言已证明：

- 纯白背景。
- 无点阵 Background。
- 存在真实节点和边。
- 上游、下游、前后方向有不同边数。
- FAILED 地址没有伪造边。
- Inspector 能显示节点信息。

仍需补强：

1. 深度从 1 层切到 2 层时，记录节点/边集合差异，而不是只记录“已点击”。
2. 选择确实能排除部分边的数据集/金额条件，证明过滤前后集合变化。
3. 拖拽节点后记录坐标变化或截图。
4. 点击边并核对交易明细能够解释该边。
5. 保存快照、加入调查和导出至少各执行一次，并核对最终产物。
6. 画布白色和无点阵必须在桌面与移动端各保存一张证据。

## 12. 实施顺序

### Phase 0：冻结和保护证据

1. 复制或锁定 `20260815-UAT-01` 原始证据，不覆盖历史文件。
2. 记录当前 Git SHA、前端构建时间、后端二进制哈希和配置摘要。
3. 将原报告标记为“待整改，不可作为验收通过依据”。
4. 生成纠正后的断言统计。

### Phase 1：修复导航和路由

1. 实现统一 URL/页面状态映射。
2. 修复后退/前进。
3. 修复深链接刷新。
4. 增加前端测试。
5. 构建并通过 UJ-01、UJ-10 定向复测。

### Phase 2：稳定跨链输入

1. 保留组合 option key/value 修复。
2. 增加组件/逻辑测试。
3. 在同一构建上复测 UJ-06、UJ-09、UJ-12。

### Phase 3：恢复真实任务执行条件

1. 核对调度器、worker、Provider、数据平面。
2. 选择合理运行窗口。
3. 完成暂停/恢复任务。
4. 完成独立取消任务。
5. 如状态机确有缺陷，再修改后端并补测试。

修改后端代码后必须执行 `./run.ps1` 重启项目。

### Phase 4：完成非零结果闭环

1. 通过 UI 创建本轮非零任务。
2. 等待终态并保存全链路标识。
3. 核对 Parquet 和 ClickHouse。
4. 在结果页查看并导出。
5. 实际解析导出文件。

### Phase 5：错误恢复与资金图补强

1. 非法地址定位反馈。
2. 应用内离线/超时/500 恢复。
3. 资金图深度、过滤、边详情、快照和导出。

### Phase 6：完整回归

1. 使用新 RUN_ID 和固定构建重跑 UJ-01..UJ-12。
2. 再执行 78 条边界补充用例。
3. 最后执行容量、安全、竞态和受控故障专项。
4. 自动生成统计和最终报告。

## 13. 验证命令

### 13.1 前端

```powershell
Set-Location E:\codex\etl\frontend
npx tsc --noEmit
npm run build
```

如果项目已有适用前端测试命令，应同步执行导航、地址输入和 Smart Download 组件测试。

### 13.2 后端

```powershell
Set-Location E:\codex\etl
go test ./internal/...
go vet ./...
go build -o bin\etl-server.exe .\cmd\server\
```

如修改 Smart Download 生命周期，至少定向执行：

```powershell
go test -v ./internal/smartdownload/...
```

### 13.3 重启与健康检查

```powershell
Set-Location E:\codex\etl
.\run.ps1
Invoke-RestMethod http://127.0.0.1:8000/api/health
```

健康接口只证明服务可访问，不代表业务验收通过。

## 14. 复测证据目录

新一轮不得覆盖原目录，建议使用：

```text
tmp/qa-evidence-20260815-UAT-R01/
├── SUMMARY-UAT-RETEST.md
├── environment.json
├── build.json
├── fixtures/
├── assertions/
├── network/
├── console/
├── screenshots/
├── downloads/
├── data-verification/
└── traces/
```

`build.json` 至少记录：

- RUN_ID。
- Git SHA。
- dirty worktree 文件列表。
- 前端构建时间。
- 后端二进制 SHA256。
- 服务启动时间。
- 数据根目录。
- 浏览器版本和 viewport。

## 15. 最终验收门槛

只有以下条件全部满足才可以将产品验收改为 PASS：

1. UJ-01..UJ-12 在同一固定构建版本完成。
2. 浏览器后退、前进不离开应用或出现白屏。
3. Explorer 深链接刷新、新 Context 和服务重启后 URL 与页面一致。
4. BSC/ETH 同地址的鼠标和键盘选择链完全正确。
5. 至少一个真实任务完成 RUNNING → PAUSED → RUNNING → 终态。
6. 另一个真实任务完成 RUNNING → CANCELED。
7. 至少一个本轮任务产生非零结果并完成 Parquet、ClickHouse、结果页和导出闭环。
8. 导出 XLSX 已实际解析，行数和抽样记录与本轮任务一致。
9. 风险无数据地址显示数据不足，不显示低风险或旧地址分数。
10. 资金追踪方向、深度、过滤、节点、边、Inspector、快照和导出符合真实数据。
11. 资金追踪桌面和移动端画布均为纯白背景且无点阵。
12. 离线、超时、500 和非法输入均显示应用内可恢复反馈。
13. 刷新、浏览器重开、第二 Context 和服务重启后持久化对象存在。
14. 所有断言统计可以从 JSON 自动复算，没有重复计数、隐藏 FAIL 或以 SKIPPED 充当 PASS。
15. Console 和 Network 的非预期错误为零；预期故障单独标记并与注入步骤对应。

任一门槛未满足，产品验收仍为 FAIL；测试执行可以根据实际情况标记 PARTIAL 或 BLOCKED。

## 16. Agent 完成定义

修复 Agent 只有完成以下交付才可声明完成：

- [ ] 产品缺陷代码已修复。
- [ ] 没有修改无关模块或既有 API 路径。
- [ ] 自动化测试覆盖新增行为。
- [ ] 前后端构建和测试通过。
- [ ] 涉及后端时已执行 `run.ps1` 重启。
- [ ] 已用真实浏览器完成定向复测。
- [ ] 已完成本轮非零下载数据闭环。
- [ ] 已保存可复算的断言、截图、网络、Console、文件和数据核证证据。
- [ ] 原始失败证据未删除或覆盖。
- [ ] `docs/AI_HANDOFF.md` 和 `docs/CHANGELOG_AI.md` 已更新。
- [ ] 最终报告明确区分首次 FAIL、修复内容和复测结果。

## 17. 当前未完成事项

截至本文编写时：

- UAT-BUG-01 后退白屏尚未修复。
- Explorer 深链接刷新错位尚未修复。
- 跨链 option 唯一值修复已存在，但尚未在一轮固定构建的完整 UAT 中复验。
- 暂停/恢复终态未完成真实验证。
- 本轮非零下载、入仓和导出闭环尚未完成。
- UAT 原报告数字和结论尚未按本方案修订。

本文仅定义整改与修复要求，不代表上述问题已经修复或复测通过。
