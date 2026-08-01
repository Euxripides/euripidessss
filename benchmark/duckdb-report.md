# V2.1 RC2 DuckDB Analytics Benchmark 报告

- 时间: 2026-08-01 16:58:22
- 数据: E:/codex/etl/stress-data/bsc_real/sqd-200k-warehouse/logs.parquet（49031 行）

## 场景结果

| 场景 | 耗时 | 结果行 | 备注 |
|---|---|---|---|
| 1. 基础扫描 COUNT(*) | 26ms | 1 | 1912576 rows/s |
| 2. 地址画像（单地址事件） | 33ms | 100 | COUNT 查询另耗时 26ms |
| 3. 多地址 SEMI JOIN（1000 地址） | 38ms | 28549 | 命中 28549/49031 行 |
| 3. 多地址 SEMI JOIN（10000 地址） | 40ms | 34537 | 命中 34537/49031 行 |
| 3. 多地址 SEMI JOIN（50000 地址） | 44ms | 43584 | 命中 43584/49031 行 |
| 4. Token 流向（Top 发送方） | 33ms | 10 | Top 接收方 32ms，Holder 34ms |
| 5. 时间范围（Block 窗口） | 30ms | 20 | 每日聚合另耗时 33ms |
| 6. 聚合排行（地址活跃） | 29ms | 10 | Holder 排行另耗时 31ms |
| 7. 并发查询（1 连接） | 52ms | 0 | 平均单查询 52ms |
| 7. 并发查询（5 连接） | 58ms | 0 | 平均单查询 12ms |
| 7. 并发查询（10 连接） | 75ms | 0 | 平均单查询 8ms |
| 8. 字段裁剪（SELECT 子集 vs SELECT *） | 111ms | 0 | SELECT * 耗时 575ms（裁剪加速 80.8%） |

**结论**: ✅ 全部场景通过
