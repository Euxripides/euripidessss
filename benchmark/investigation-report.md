# V2.1 RC2 调查工作流与资金追踪验证报告

- 目标地址: 0x238a358808379702088667322f80ac48bad5e6c4
- 时间: 2026-08-01 17:01:00

## 调查摘要

| 项 | 值 |
|---|---|
| 地址类型 | 活跃交易方 |
| 交易数 | 1662（in 1519 / out 1758）|
| Token 数 | 69（Top: 0x55d398326f99059ff775485246999027b3197955）|
| 风险 | 72.01（高）|
| 路径数 | 20 |
| 查询耗时 | profile=276ms risk=0ms flows=75ms path=340ms relations=588ms |

## 风险证据

- 模式: 大额转入-快速转出-多地址分散
- 大额转入 Top5: 5
- 快速转出 Top5: 5
- 分散目标 Top5: 5

## 证据文件

- `snapshots/evidence.json`
- `snapshots/paths.csv`
- `snapshots/related_addresses.csv`
