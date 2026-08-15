# 地址资产跨功能验收缺陷修复方案

> 来源报告：`tmp/qa-evidence-20260815-A03/SUMMARY.md`  
> 测试批次：`20260815-A03`  
> 编写日期：2026-08-15  
> 当前产品验收：**FAIL**  
> 当前测试覆盖：**PARTIAL**

## 1. 目标

修复地址资产导入与跨功能复用验收发现的 1 个 P0、4 个 P1 缺陷，并修订测试报告的证据统计口径。

完成后必须满足：

1. 无数据地址不得被判定为“低风险”。
2. 资金追踪的上游、下游、前后方向必须显示真实关系。
3. 合法但无活动数据的地址不得产生非预期 404 和浏览器 Console Error。
4. XLSX 地址导入必须扫描全部工作表。
5. 跨链同地址及快速输入不得造成建议重复、残留或错选。
6. 修复后的测试结论必须能够从原始断言和证据文件完整复算。

## 2. 缺陷与优先级

| 编号 | 优先级 | 功能域 | 缺陷 |
|---|---|---|---|
| FIX-001 | P0 | 风险分析 | 无数据地址被显示为“低风险、0 分” |
| FIX-002 | P1 | 资金追踪 | `upstream/downstream/both` 与后端 `IN/OUT/ALL` 不兼容 |
| FIX-003 | P1 | Explorer/画像 | 合法无数据地址的 summary 返回 404 |
| FIX-004 | P1 | Smart Download 导入 | XLSX 只读取第一个工作表 |
| FIX-005 | P1 | 统一地址输入框 | 跨链同地址 option 冲突及异步请求竞态 |
| FIX-006 | 报告修订 | 测试报告 | 汇总数字、严重度、跳过状态和证据映射不一致 |

## 3. FIX-001：无数据地址风险语义

### 3.1 当前问题

风险仓库在没有活动数据时仍使用默认值：

```text
risk_score = 0
risk_level = low
risk_reason = No screening rule triggered
rules = []
```

“没有发现风险”与“没有足够数据进行筛查”不是同一含义。零活动地址不能据此得出低风险结论。

### 3.2 后端修复

主要文件：

- `internal/clickhouseanalytics/repository.go`
- `internal/clickhouseanalytics/models.go`
- `internal/api/clickhouse_analytics_handlers.go`

在风险计算得到 `event_count == 0` 时提前返回数据不足结果。建议采用加法式接口扩展：

```json
{
  "risk_score": null,
  "risk_level": "insufficient_data",
  "risk_reason": "当前地址没有可用于风险筛查的活动数据",
  "data_sufficient": false,
  "event_count": 0,
  "active_days": 0,
  "rules": [],
  "method": "deterministic_clickhouse_screening_v1"
}
```

有数据时：

- `data_sufficient=true`。
- 保持现有 `low/medium/high` 阈值。
- `risk_score` 保持 0–100。
- 继续返回可审计的规则、事件数、活跃天数和筛查方法。

如现有响应类型无法将 `risk_score` 表示为 `null`，应将其改为可空数值；不得用 `0` 同时表示“真实零风险分”和“未计算”。

### 3.3 前端修复

主要文件：

- `frontend/src/features/analytics/RiskAnalysisPage.tsx`
- 对应 API 类型文件

当 `data_sufficient=false` 或 `risk_level=insufficient_data` 时：

- 显示“数据不足”。
- 不显示“低风险”标签。
- 不显示绿色 0 分进度条。
- 不继承前一个地址的分数、等级、规则和画像。
- 展示确定性边界：当前地址没有足够的活动数据，无法完成风险筛查。

### 3.4 测试

后端至少覆盖：

1. 零事件地址返回 `insufficient_data`。
2. 零事件结果不包含 `low`。
3. 有数据地址继续按阈值返回风险等级。
4. 低风险有数据地址与无数据地址能够明确区分。

Playwright 必须验证：

- 先选择一个高风险 AVAILABLE 地址，再选择 FAILED 地址。
- FAILED 地址显示“数据不足”。
- 页面不存在“低风险”和 `0.0/100` 风险结论。
- 上一地址的 80 分等结果完全清除。

## 4. FIX-002：资金追踪方向参数统一

### 4.1 当前问题

前端方向值：

```text
all | upstream | downstream | both
```

graphcache 方向值：

```text
ALL | IN | OUT
```

当前只进行大小写转换，导致 `UPSTREAM/DOWNSTREAM/BOTH` 进入 Builder 后与 `IN/OUT` 均不匹配，所有关系被过滤。

### 4.2 修复原则

在 API 边界统一转换，缓存键和 Builder 内部只使用规范值。

| 请求值 | graphcache 规范值 | 业务含义 |
|---|---|---|
| 空值 | `ALL` | 双向 |
| `all` | `ALL` | 双向 |
| `both` | `ALL` | 双向 |
| `upstream` | `IN` | 进入根地址 |
| `downstream` | `OUT` | 从根地址发出 |
| `ALL/IN/OUT` | 保持对应规范值 | 后端兼容值 |
| 其他值 | HTTP 400 | 不得静默降级 |

主要文件：

- `internal/api/investigation_cache.go`
- `internal/graphcache/key.go`
- `internal/graphcache/builder.go`
- `internal/api/investigation_cache_test.go`
- `internal/graphcache/graphcache_test.go`

建议新增独立函数：

```go
func normalizeGraphDirection(value string) (graphcache.Direction, error)
```

禁止在多个 Handler、Builder 和缓存层分别维护不同映射。

### 4.3 测试

单元测试应覆盖所有映射和非法值。

真实 UI 验收：

1. 聚焦 AVAILABLE 地址。
2. 记录 `all` 基线节点和边。
3. 选择“上游”：只保留进入根地址的边。
4. 选择“下游”：只保留根地址发出的边。
5. 选择“前后”：恢复双向关系。
6. 请求方向、画布箭头、Inspector 方向完全一致。
7. 有真实关系时不得自动打开智能补数。
8. 切换方向后不得只剩根节点或保留上一方向的陈旧边。

## 5. FIX-003：合法无数据地址的 summary 空状态

### 5.1 当前问题

合法 EVM 地址已存在于地址资产库，但没有链上活动数据时，summary 返回：

```http
HTTP 404
{"detail":"address not found"}
```

页面虽然能够降级，但浏览器仍产生资源加载错误。对于“地址合法、查询成功、结果为空”的场景，404 会混淆资源不存在和业务无数据。

### 5.2 建议接口语义

| 场景 | 状态码 | 语义 |
|---|---:|---|
| 地址格式非法 | 400 | 输入错误 |
| 合法地址但无活动数据 | 200 | `data_status=NO_DATA` |
| 有活动数据 | 200 | 正常 summary |
| ClickHouse 不可用 | 503 | 数据源故障 |

建议无数据响应：

```json
{
  "address": "0x...",
  "chain_id": 56,
  "data_status": "NO_DATA",
  "event_count": 0,
  "transaction_count": 0,
  "active_days": 0
}
```

主要文件：

- `internal/explorer/repository.go`
- `internal/api/clickhouse_handlers.go`
- Explorer summary 响应模型
- 前端 Explorer/画像 API 类型与空状态组件

不得把查询异常、超时或 ClickHouse 故障转换成 `NO_DATA`。

### 5.3 测试

1. 非法地址仍返回 400。
2. FAILED/CERTIFIED 且无活动行的合法地址返回 200 + `NO_DATA`。
3. AVAILABLE 地址返回真实 summary。
4. ClickHouse 故障仍返回 503。
5. 页面显示“暂无链上活动数据”。
6. Network 无非预期 404，Console 无资源加载错误。

## 6. FIX-004：XLSX 全工作表扫描

### 6.1 当前问题

`importXLSX` 当前只读取：

```go
workbook.Rows(sheets[0])
```

说明页位于第一个 Sheet、地址位于后续 Sheet 时，导入错误地返回“未识别到地址列”。

### 6.2 正确实现方式

主要文件：

- `internal/smartdownload/import.go`
- Smart Download 导入测试文件

处理流程：

1. 调用 `GetSheetList()` 获取全部工作表。
2. 每个 Sheet 单独读取行。
3. 每个 Sheet 单独执行表头和地址列识别。
4. 无地址列的说明页记录诊断后继续扫描。
5. 合并所有有效 Sheet 的合法地址。
6. 跨 Sheet 统一规范化和去重。
7. 所有 Sheet 均无有效地址时才返回失败。
8. 文件字节、总行数和总单元格数量必须按整个工作簿累计限制。
9. 每个 Rows 迭代器在该 Sheet 处理结束后及时关闭，避免循环内大量 defer。

不要直接把所有 Sheet 的原始行拼成一个二维数组再调用一次 `analyzeColumns`，否则第二个 Sheet 的表头可能被当成普通数据。

### 6.3 测试矩阵

| Fixture | 预期 |
|---|---|
| Sheet1 说明、Sheet2 地址 | 成功导入 Sheet2 地址 |
| 两个有效地址 Sheet | 合并全部地址 |
| 两个 Sheet 含重复地址 | 最终只保留一个 |
| 空 Sheet + 有效 Sheet | 成功 |
| 所有 Sheet 均无地址 | 明确失败 |
| 中间 Sheet 损坏或读取异常 | 明确错误，不得部分持久化 |
| 累计内容超过限制 | 写库前拒绝 |

API 与 UI 均需保存原始响应、导入摘要和刷新后的地址库记录。

## 7. FIX-005：地址建议唯一键与请求竞态

### 7.1 唯一键问题

当前 option 使用裸地址作为 `value`。同一地址分别存在于 BSC 和 Ethereum 时，rc-select/React 可能复用冲突项。

建议 option 使用链与地址组合的唯一值：

```ts
const optionValue = `${item.chain_key}:${item.address}`;
```

```ts
{
  key: optionValue,
  value: optionValue,
  item,
  label: ...
}
```

选择后必须把真实地址而不是组合值写回业务输入：

```ts
onSelect={(_, option) => {
  const item = option.item as AddressLibraryItem;
  setOpen(false);
  onChange(item.address);
  onSelect?.(item.address, item);
}}
```

### 7.2 请求竞态问题

当前新输入要等待 debounce 后才递增请求序号。在这段窗口中，旧请求可能返回并覆盖新查询。

修复要求：

1. 输入值或链变化时立即使旧请求失效。
2. 新结果返回前清除与当前查询不匹配的旧建议。
3. 响应写入前同时核对请求序号、查询词和链。
4. 500、超时或取消后清空建议并关闭下拉。
5. 组件卸载后不得写入状态。
6. 可使用 `AbortController` 取消旧请求，但仍须保留响应序号校验。
7. 空查询聚焦仍要支持加载当前链地址库，不能因“清空旧建议”破坏 INPUT-001。

主要文件：

- `frontend/src/features/address-library/AddressLibraryInput.tsx`
- `frontend/src/features/address-library/addressLibraryApi.ts`

### 7.3 测试

1. BSC 和 Ethereum 存在相同地址时显示两条不同链建议，不重复、不残留。
2. 选择任一建议后输入框只出现 `0x...` 地址。
3. 目标页面和请求链使用所选资产的链。
4. 快速输入三个前缀，只显示最后一个查询结果。
5. 人工延迟第一个请求，旧响应不得覆盖新结果。
6. 注入 500/超时后建议立即清空。
7. 故障恢复后再次聚焦能够重新查询。
8. 鼠标和键盘选择产生相同业务结果。

## 8. FIX-006：修订测试报告

原始报告：`tmp/qa-evidence-20260815-A03/SUMMARY.md`。

### 8.1 必须修订的口径

报告首页应明确分开：

```text
产品验收：FAIL
测试覆盖：PARTIAL
确认缺陷：1 个 P0、4 个 P1
```

### 8.2 严重度

- RISK-003：P0。
- GRAPH-006：P1，不得在正文标为 P0。
- summary 404、XLSX 多 Sheet、地址建议问题：P1。

### 8.3 断言统计

当前矩阵写 268 PASS / 9 FAIL，但现有 19 份 `*-assertions.json` 只能复算出 239 PASS / 8 FAIL。

修订要求：

1. 为每个矩阵数字提供断言文件和检查项映射。
2. 补齐缺失的 30 PASS / 1 FAIL，或者从正式总数删除。
3. 保存 SD-IMP-003 的原始请求、响应状态、响应体和地址库前后快照。
4. 泛化检查名如 `ui-console-no-errors` 必须同时带用例 ID 或功能域，避免跨文件重名难以追踪。

### 8.4 跳过状态

- `snapshot button not found; skip` 必须记为 SKIPPED/BLOCKED，不能记为 PASS。
- ALIB-007 已存在执行证据，应从跳过清单删除。
- UI-003 必须逐页面证明 loading、成功、无数据/失败三类状态；零散截图不能等同完整覆盖。

## 9. 实施顺序

### 第一阶段：P0

1. 修复风险无数据语义。
2. 增加后端单元测试。
3. 完成 RiskAnalysisPage 数据不足状态。
4. 使用 AVAILABLE → FAILED 连续选择进行 Playwright 回归。

P0 未通过前不得申请整体验收。

### 第二阶段：P1 后端

1. 修复资金追踪方向映射。
2. 修复无数据 summary 语义。
3. 修复 XLSX 全 Sheet 扫描。
4. 增加对应 Go 测试。

### 第三阶段：P1 前端

1. 修复地址建议唯一键。
2. 修复 debounce/异步响应竞态。
3. 完成错误清空和恢复行为。
4. 执行桌面端和 390px 移动端回归。

### 第四阶段：报告与整体验收

1. 修订 SUMMARY 和证据映射。
2. 重跑五个缺陷的定向用例。
3. 重跑所有 P0。
4. 核对原始 JSON、Network、Console、截图、数据库/ClickHouse 数据。
5. 给出产品验收与覆盖完整度两个独立结论。

## 10. 验证命令

```powershell
cd E:\codex\etl

go test ./internal/graphcache ./internal/clickhouseanalytics ./internal/explorer ./internal/smartdownload ./internal/api -count=1
go test ./internal/... -count=1
go vet ./...

cd frontend
npm run build
cd ..

.\run.ps1
```

修改后端后必须执行 `run.ps1`。服务启动后不能只检查 `/api/health`，还要完成以下真实验证：

- FAILED 地址风险页显示“数据不足”。
- AVAILABLE 地址按上游/下游/前后显示真实节点和边。
- FAILED/CERTIFIED 无数据地址 summary 返回明确空状态且无 Console Error。
- 多 Sheet XLSX 地址实际进入地址库，刷新后仍存在。
- 跨链同地址建议不重复；快速输入和 500 注入不显示过期项。

## 11. 最终验收门槛

只有同时满足以下条件，才可以将产品验收改为 PASS：

1. RISK-003 及所有 P0 用例通过。
2. 五个缺陷均有修复前失败、修复后通过的成对证据。
3. 资金追踪方向切换后出现真实地址级节点和关系，不能只证明接口 200。
4. FAILED 地址不显示低风险、旧分数或伪造画像。
5. 无非预期 HTTP 4xx/5xx、Console Error、页面白屏或旧状态残留。
6. 地址库、控制面和 ClickHouse 数据与 UI 展示一致。
7. 刷新及服务重启后地址资产和状态保持正确。
8. 测试汇总数字可以由落盘断言逐条复算。
9. 所有跳过项明确列为 SKIPPED/BLOCKED，不得混入 PASS。

## 12. 完成定义

每个开发 Agent 交付时必须记录：

- 修改文件。
- 接口或类型变化。
- 新增测试。
- 验证命令及结果。
- 真实 UI 操作路径。
- 原始请求和响应证据。
- 数据库/ClickHouse 抽样结果。
- 未完成事项和剩余风险。

构建通过、接口返回 200 或页面能够打开，均不能单独作为完成标志。
