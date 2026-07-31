# V2.1 RC2 SQD 200K 地址生产验证报告

- 时间: 2026-07-31 11:32:36
- 地址数: 200000（2000 chunks，已完成 2000）
- 区块范围: 107153260 → 107153460
- 耗时: 31m36.616s（checkpoint 恢复: false）

## 数据完整性

| 指标 | 值 |
|---|---|
| raw logs | 62805 |
| unique logs | 49031 |
| duplicate logs | 13774（in-chunk 0 / cross-chunk 13774）|
| dedup ratio | 0.781 |
| Parquet rows | 49031 |
| DuckDB 验证 | true |
| **结论** | **✅ 0丢失 0重复** |

> 唯一键：`chain_id + block_number + transaction_hash + log_index`。

## SQD Reliability 状态

| 指标 | 值 |
|---|---|
| 请求数 | 2006 |
| 成功数 | 2000 |
| 失败数 | 6 |
| 重试数 | 0 |
| 503 | 6 |
| 429 | 0 |
| 平均延迟 | 713 ms |
| Worker 数 | 8 (NORMAL) |
| Circuit Breaker | NORMAL |
