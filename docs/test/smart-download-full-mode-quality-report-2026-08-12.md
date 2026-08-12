# 智能下载全模式、全下载方式与 SQD Cloud 质量验收报告

- 日期：2026-08-12
- 环境：Windows，`http://127.0.0.1:8000`
- 验收规则：HTTP 2xx 只表示请求可达；必须同时核验状态树、请求范围、Provider 路由、实际行数、唯一事件、覆盖、认证、落盘文件、字节数和 SHA256。
- 本轮结论：**通过本轮上线质量门**。历史失败记录仍保留审计，但不进入权威覆盖/统计/复用。

## 1. 覆盖范围

1. 模式：AUTO、TURBO、EMERGENCY，模式持久化、切换、暂停、恢复、取消及恢复回放。
2. 下载方式：local-hit、RPC、SQD、SQD Cloud、CSV/地址导入；AWS legacy 路径按真实实现明确为 UNAVAILABLE。
3. 范围：BLOCK、TIME、FULL、相关范围、单区块、反向/非法范围。
4. 调度：Provider 能力、链级健康、熔断、优选 Provider、Cloud 准入、自动部署、预算和回收状态。
5. 数据质量：L1-L6、跨 Part 去重、范围并集、expected/actual、必填字段、地址参与、链 ID、哈希、结果筛选和导出。
6. 前端：创建下载、任务中心、智能预取、结果数据四页签，桌面 1440px 与移动 390px。

## 2. 本轮新增修复

- Cloud sync 改为一次刷新 Registry、批内缓存查重、无新增不全量合并；合法 0 行 Manifest 进入 INDEXED，确定性校验失败使用 15 分钟冷却。
- Cloud sync 只允许本轮 `INDEXED` 结果触发 dataIndexedHook，FAILED Manifest 不再阻塞 HTTP；真实同步由约 180 秒超时降至 5.29–5.77 秒。
- Cloud 准入改为链 + 地址 + 数据集 + 请求区间的精确覆盖并集；不再把“地址历史上有任意数据”误判为本次范围完整覆盖。
- local-hit Validation 补齐 dataset_job_id、rows/raw_rows、唯一键、score、cross-check 和 validated_at，禁止出现“下载 1135 行但 validation.rows=0”。
- Provider 健康使用真实可路由节点；AWS legacy wrapper 不再伪装健康；Cloud ABSENT 只表示不可执行，但凭据齐全时仍可受控部署。
- 修复 TIME 精确解析、Planner/Preflight/Create 校验一致性、模板 override、Coverage 参数门、结果筛选/排序错误码、XLSX dimension、导入统计一致性、PARTIAL 认证和可选 Warehouse 语义。

## 3. 模式与下载方式实测

| 场景 | 结果 | 数据级断言 |
|---|---|---|
| AUTO local-hit | 通过 | 批次 `ee5c35d9...`；1135 行，validation rows/raw/unique/expected/actual 均 1135，coverage=1，DATASET_CERTIFIED |
| TURBO local-hit | 通过 | 批次 `08a413e8...`；模式保持 TURBO，父子全 COMPLETED，1135 唯一行 |
| EMERGENCY local-hit | 通过 | 批次 `a02d1cc3...`；模式保持 EMERGENCY，父子全 COMPLETED，1135 唯一行 |
| EMERGENCY RPC | 通过 | 批次 `055c6f81...`；范围 114450000–114450500，RPC 71 行，唯一 71，coverage=1，Warehouse=WRITTEN |
| SQD Cloud 自动兜底 | 通过 | 计划 `bde94638...`；ABSENT→DEPLOYING→READY/BUSY→IDLE，Provider=sqd_cloud，Job `...-1-c1` 完成 |
| SQD Cloud 合法空结果 | 通过 | 请求 114500300–114500310，0 行、0 文件，Registry INDEXED；第二次 sync 为 SKIPPED |
| SQD Cloud 非空历史产物 | 通过 | 两 Job 584+564=1148 行；唯一 1148、重复 0、目标地址参与 100%、链/范围/必填均无违规 |
| 地址导入 TXT/CSV/XLSX | 通过 | 三格式统一按非空数据行/有效单元格/重复/无效/最终唯一地址统计；尾换行不多算 |

AUTO/TURBO/EMERGENCY 对已认证精确范围的预检均返回 0 待下载区块、0 RPC、0 Cloud，并明确命中“本地已验证覆盖”。未覆盖的 101 区块范围按模式给出不同 RPC/Cloud 估算。非法 FAST 在 Preflight 与 Planner 均返回 400；反向 BLOCK、反向 TIME、未知链和未知数据集均在执行前拒绝。

## 4. SQD Cloud 文件审计

审计 Job：`sd-e975e6fe-8975-4f91-b7a1-d85a0ad5c9d5` 与 `sd-60b37b70-45d7-432d-8604-0a99a013dbc6`。

- Registry：584 + 564 = 1148 行；UniqueKeyCount 同为 584/564；DuplicateCount=0。
- DuckDB 重算：row_count=1148，unique(chain_id, transaction_hash, log_index)=1148。
- 区块：min=114441016，max=114457474；chain_bad=0、hash_bad=0、address_bad=0、required_nulls=0。
- 5 个 Parquet 的实际字节数与 Manifest 完全一致，5 个 SHA256 全部匹配。
- Cloud Registry 当前权威统计：15 entries、485466 rows、17 files、17086708 bytes；失败、缺文件、重复版本不计入该视图。

## 5. 覆盖、结果与导出

- 精确范围 94800000–94810000：ratio=1、full_hit=true、CERTIFIED。
- 扩展两端各 1 块：ratio=10001/10003，missing 精确为两个单块。
- 反向范围、未知链、未知数据集均 400，不再返回伪 UNVALIDATED 成功。
- 结果 `35cf2d17...`：分页总数 1135；按 block_number 排序的数据在范围内；无匹配过滤返回 `rows=[]/total=0`。
- 非法 sort/filter 返回 400 且只返回受控信息，不泄露 DuckDB Binder SQL。
- XLSX 导出 1135 数据行、13 列，文件 111147 bytes，OOXML dimension=`A1:M1136`，流式读取不会只看到 A1。

## 6. 前端浏览器回归

- 四页签桌面/移动均可加载，未出现 `[object Object]`，模块控制台错误和 page error 均为 0。
- 创建页实际呈现三种模式和 2 个“当前不可用”数据集；任务中心存在刷新与运行对比；结果页可加载真实行。
- 1440px：document/body/viewport 均 1440；390px：三者均 390，无页面级横向溢出。
- 截图：
  - `C:\Users\Euripides\.codex\visualizations\2026\08\11\019ff016-048a-77a1-84fb-ed3858fdefc8\smart-download-desktop-final-2026-08-12.png`
  - `C:\Users\Euripides\.codex\visualizations\2026\08\11\019ff016-048a-77a1-84fb-ed3858fdefc8\smart-download-mobile-final-2026-08-12.png`

## 7. 验证命令与边界

- `go test ./internal/... -count=1 -timeout 10m`：智能下载、调度、Cloud、Registry、API、Parquet 等通过；最后一轮仅无关的 `internal/intelligence/TestResumeExecutesRecoveredTasks` 出现一次恢复竞态，单独 `-count=5` 全通过。此前同一全量命令已完整通过。
- `go test ./internal/smartdownload -count=1 -timeout 5m`：通过（约 30 秒）。
- `go test ./internal/datasetsync ./internal/downloadscheduler ./internal/api -count=1`：通过。
- `go vet ./...`、`git diff --check`：通过；diff-check 仅换行提示。
- `frontend npm run build`：通过；仅保留既有 2.14 MB 主 chunk 警告。
- `run.ps1`：最终正常模式重启成功，PID 19912；fault injection=false，health=ok，Cloud=IDLE/available=true。

边界：真实 Cloud 正向任务为有界 11 区块合法空结果，用来验证部署/提交/终态/同步；非空质量使用同一生产 Worker 生成的 1148 行真实 Parquet 做全文件审计。未运行大范围付费压力任务。历史 DB_WRITE_FAILED/旧空版本保留审计，但被权威视图与复用门隔离。
