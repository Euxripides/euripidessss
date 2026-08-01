# V2.1 RC2 Data Integrity Verification 报告

- 时间: 2026-08-01 17:15:17

## 数据一致性

| 指标 | 值 |
|---|---|
| source_rows | 49031 |
| parsed_rows | 49031 |
| unique_rows | 49031 |
| duplicate_rows | 0 |
| parquet_rows | 0 |
| duckdb_rows | 49031 |
| duckdb_distinct | 49031 |
| **一致性 (unique=parquet=duckdb=distinct)** | **false** |

> 唯一键：`chain_id + block_number + transaction_hash + log_index`

## Parquet 验证

| 指标 | 值 |
|---|---|
| 文件存在 | true |
| 文件大小 | 1552334 bytes |
| SHA256 | 6dcd446209bebe5722b30a8a15ee528a1f287d7d6a02342e82827f7adecd86d3 |
| Checksum | true |
| Schema | [chain_id block_number block_time transaction_hash log_index address topic0 topic1 topic2 topic3 data] |
| **结论** | **❌ FAILED** |
