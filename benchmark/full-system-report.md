# V2.1 RC2 全链路真实调查系统验收报告（Full System Real Data Acceptance Test）

- 版本：V2.1 RC2
- 日期：2026-08-02
- 数据：真实 BSC 链数据（stress-data/bsc_real，区块 107153260-107153460）
- 结论：**通过**（DeepSeek 真实调用已验证；无 API Key 时优雅降级）

## 1. 服务环境验收

| 项目 | 标准 | 结果 |
|------|------|------|
| 后端服务 | 启动成功 | ✅ bin/etl-server.exe（windows/386 构建） |
| 健康检查 | HTTP 200 | ✅ `{"status":"ok"}`，analysis_plane duckdb-cli v1.5.3，control_plane sqlite ok |
| 前端 build | 成功 | ✅ tsc + vite build 3908 modules |
| API 可访问 | 正常 | ✅ 40+ 端点冒烟全通 |

## 2. 数据资产链路（§6）

| 检查 | 结果 |
|------|------|
| source=parsed=unique=parquet=duckdb | ✅ 49031 = 49031 = 49031 = 49031 = 49031 |
| 重复写入 | ✅ dup=0（唯一键 block+tx+log_index） |
| Checksum | ✅ sha256=6dcd4462… 有效 |
| Schema | ✅ 11 列正确 |

## 3. DuckDB 分析引擎（§7）

12 场景全 PASS：COUNT 26ms / 单地址画像 33ms / SEMI JOIN 1K-50K 38-44ms（50K 地址 44ms < 1s ✅）/ Token 流向 33ms / 时间窗口 30ms / 聚合 29ms / 并发 1/5/10 连接 52/58/75ms 无错误 / 字段裁剪有效。

## 4. SQD 数据采集可靠性（§4）— 真实网络

TestSQD10KStability：**100/100 chunks 完成率 100%**（3m8.7s），真实 BSC finalized 数据。
- 503 处理：chunk 71 触发 `503 No available workers` → cooldown 1m → attempt 2 重试成功 ✅
- Retry 恢复：request=101 success=100 fail=1，重试后 0 丢失 ✅
- Circuit Breaker：NORMAL；Workers 8 NORMAL ✅
- 数据唯一化：Logs total=52300 unique=45198 dup=7102（SQD 多 filter 双命中，应用层唯一化后 0 重复写入）✅
- sqd-events.log / metrics / checkpoint 均生成 ✅

## 5. 分析模块（§8-13）

| 模块 | 结果 |
|------|------|
| 地址画像 | ✅ 5/5：first/last_activity、event_count、token/contract_count、total_in/out、active_days 字段完整非空、两次结果一致 |
| 资金流+路径 | ✅ Transfer 边 49031、两跳路径无自环、每条含 tx_hash/block/token/amount |
| 图谱 | ✅ 节点 15595/边 21693 可追溯（聚合 45917 == parquet 非自环）、PageRank 最大 0.114、Cluster 796 个、无自环 |
| Token 余额 | ✅ 3/3：balance=in−out 守恒、历史快照 69 Token/时间线 3277、USDT/USDC/WBNB 验证、USDT 风险 High liquidation、性能 50K 地址 25ms |
| 风险识别 | ✅ 分类覆盖率 16411/16411、0xdead 风险 72.01 高 |

## 6. 智能调查闭环（§15-17）— 真实数据

调查 inv-1（0xdead）：CREATED→COMPLETED 100%。
- 规划 5 任务 → 执行 7 任务 done 7/7 → 观察（320 新地址/25 新路径/703 新交易/2 风险事件）→ 决策 STOP（达到最大发现地址数 200）
- 路径 10 条（score 88 最高，4 跳，含 tx_hash/block/token/amount）✅
- 实体 21 个（exchange/contract/wallet 分类）✅
- 风险 CONCENTRATION + LARGE_INFLOW ✅
- 假设 2 条如实标记「验证任务未执行（调查提前结束）」✅
- 报告 MD/HTML/JSON 三格式生成（9 章节闭环追踪）✅

## 7. DeepSeek Agent（§18）— 真实调用已验证 ✅

配置 `DEEPSEEK_API_KEY` 后真实调用成功（deepseek-v4-flash，4 次调用全部 200）：
- **AI 规划**：strategy=deep_probe（confidence 0.65，AI 生成 rationale + 5 个结构化任务 ai1-ai5）
- **AI 假设**：规则触发 + AI 细化
- **AI 建议**：STOP（confidence 0.95，3 条 AI 理由）
- **AI 深入分析**：5 条发现**全部 VERIFIED**（Evidence Guard 以 tx 证据链验证），5 条洞察（归集/分层/大额流入/黑洞地址/洗钱模式），summary 1458 字符
- 输出字段符合验收：finding/type/address/detail/confidence/evidence（tx_hash）✅
- 记忆固化、token 用量日志完整（deepseek_token_usage）

修复：`max_tokens` 默认 2000→4096（推理模型输出额度不足导致 content 截断为空），新增 `deepseek_output_truncated_or_empty` 截断检测日志（BUG-004）。

## 8. API 服务（§14）

| 端点 | 结果 |
|------|------|
| /api/analytics/*（dashboard/graph/address/{a}/profile\|flows\|path\|risk/addresses/profile） | ✅ 200，错误处理 400/404 正确 |
| /api/intelligence/*（investigations/config/memories/report） | ✅ 200，非法调查 404 |
| /api/dynamic-investigation/*（stats/queue/entities/tasks/config） | ✅ 200 |
| /api/crypto/parquet/*（jobs/settings/sqd/status/address） | ✅ 200 |
| /api/crypto/datasource/* | ✅ 200（SQD HEALTHY 95.98、AWS HEALTHY 88.02、RPC DISABLED 按配置） |
| 100 并发压力 | ✅ 100/100 全部 200，错误率 0%，avg 58ms |
| 前端页面 | ✅ index/SPA 路由/静态资源 200 |

## 9. Bug 发现与修复（§19）

| Bug | 严重度 | 根因 | 状态 |
|-----|--------|------|------|
| BUG-001 | critical | BatchProfiles 空 addr_file → read_csv('') 读 stdin → JOIN 爆炸 → 32 位进程 OOM 崩溃（DoS） | ✅ 已修复+回归 |
| BUG-002 | high | VALUES 内联 1000 地址超 Windows 32K 命令行限制 | ✅ 已修复+回归 |
| BUG-003 | high | addr_file 任意文件读取 + 2 个 DoS 面 | ✅ 已修复+回归 |
| BUG-004 | medium | max_tokens=2000 对推理模型不足 → AI 深入分析/假设输出截断为空 → 报告 AI 章节缺失 | ✅ 已修复+回归 |

修复详情见 benchmark/bug-report/BUG-00{1,2,3,4}.json。修复文件：internal/analyticsapi/{service,service_test}.go、internal/intelligence/{types,deepseek_client}.go（新增回归测试 + 截断检测日志）。

## 10. 已知限制与注意事项

1. 真实数据验证测试（标记文件启用）需**串行**运行（`go test -p 1` 或逐包），并行会因 DuckDB 文件锁冲突——测试基建已知限制，默认 `go test ./... -short` 不受影响（38 包零回归）
2. 服务为 windows/386 32 位构建，大结果集 JSON 编码有 ~2GB 地址空间上限（BUG-001 已封死触发路径；批量接口上限 500 地址/64 字符）
3. DeepSeek 真实调用已验证（需配置 DEEPSEEK_API_KEY）；AI 链路由单测覆盖 78.4% + 真实端到端（AI 规划/建议/深入分析全部 VERIFIED）
4. SQD 公共端点约每 300 连续流触发 503 冷却（Reliability Layer 自动恢复，已验证）

## 11. 交付物

- benchmark/full-system-report.md / .json（本报告）
- benchmark/bug-report/BUG-001..004.json（Bug 记录闭环）
- benchmark/integrity-report.json / duckdb-report.json / analytics-model-report.json / balance-report.json / sqd-10k-report.json / api-service-report.json（各模块验证报告）
