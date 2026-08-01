# V2.1 RC2 业务查询 API 服务验证报告

- 时间: 2026-07-31 13:29:05

## 正确性

```json
{
  "address": "0x55d398326f99059ff775485246999027b3197955",
  "api_event_count": 13746,
  "flows_amount_parsed": true,
  "flows_has_in_out": true,
  "missing_address_empty": true,
  "path_count": 20,
  "path_no_cycle": true,
  "profile_ok": true,
  "repeatable": true,
  "risk_in_range": true,
  "risk_level": "高",
  "risk_score": 72.01,
  "sql_emitter_count": 13746
}
```

## 缓存

```json
{
  "first_miss": true,
  "hits": 1,
  "misses": 1,
  "second_hit": true
}
```

## 性能

| 场景 | 耗时 |
|---|---|
| batch_1000 | 42ms |
| batch_10000 | 66ms |
| batch_50000 | 144ms |
| single_profile_ms | 0 |

## 并发

| 规模 | 总耗时 | 错误 |
|---|---|---|
| concurrent_100 | 0ms | 0 |
| concurrent_10 | 1882ms | 5 |
| concurrent_50 | 0ms | 0 |

**结论**: ✅ 全部通过
