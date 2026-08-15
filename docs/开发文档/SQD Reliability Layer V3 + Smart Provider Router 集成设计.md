# SQD Reliability Layer V3 + Smart Provider Router 集成设计

版本：V3.0\
目标：将 SQD 从单一数据源升级为可治理的数据 Provider，并接入 Smart
Download Orchestrator，实现自动降级、恢复、切换。

------------------------------------------------------------------------

# 1. 背景

当前系统已经完成：

-   SQD 数据采集
-   Chunk 分片下载
-   Retry + Backoff
-   Checkpoint 断点恢复
-   Parquet 写入
-   DuckDB 分析
-   地址关系图分析

但生产环境仍存在：

-   SQD Portal 返回 503
-   Worker 不可用
-   高峰期请求失败
-   大规模地址任务失败率升高

问题本质：

> SQD 是不稳定外部 Provider，需要增加可靠性治理层。

------------------------------------------------------------------------

# 2. 总体架构

                     Smart Download Orchestrator

                              |
                              v

                     Smart Provider Router

                              |
            ----------------------------------

            SQD Reliability Layer

            AWS Dataset Provider

            RPC Enrichment Provider

            ----------------------------------

                              |

                     Dataset Registry

                              |

                  Parquet Lake + DuckDB

------------------------------------------------------------------------

# 3. SQD Reliability Layer V3

目录：

    internal/provider/sqd/

    ├── client.go
    ├── governor.go
    ├── circuit_breaker.go
    ├── health_monitor.go
    ├── failure_classifier.go
    ├── adaptive_worker.go
    ├── retry_policy.go
    ├── metrics.go
    └── events.go

------------------------------------------------------------------------

# 4. Adaptive Worker 自适应并发

不再固定：

    workers=8

改为动态：

    启动:

    workers = 4


    成功率 >98%

    持续30秒

    ↓

    workers +2


    最大:

    16

异常：

    503 >5%

    16
     |
    8
     |
    4
     |
    2
     |
    1

恢复：

    60秒无503

    1
    →2
    →4
    →8

------------------------------------------------------------------------

# 5. SQD Circuit Breaker

状态：

    NORMAL

     |
     | 连续5次失败

     v

    DEGRADED

     |
     | 连续20次失败

     v

    OPEN

     |
     | 冷却120秒

     v

    HALF_OPEN

     |
     | 测试成功

     v

    NORMAL

------------------------------------------------------------------------

# 6. Failure Classifier

错误分类：

  错误             处理
  ---------------- --------------
  503 No Worker    降低并发
  429 Rate Limit   延迟
  Timeout          缩小Chunk
  DNS失败          Provider降级
  Schema错误       停止任务

------------------------------------------------------------------------

# 7. Chunk智能调整

根据地址活跃度动态调整：

低活跃：

    500 addresses/chunk

普通：

    100 addresses/chunk

高活跃：

    20 addresses/chunk

增加：

    Address Activity Score

评分：

    tx数量
    token数量
    log数量
    历史失败率

------------------------------------------------------------------------

# 8. Smart Provider Router

统一接口：

``` go
type Provider interface {

    Health() Score

    Download(job Job)

    Support(dataset Dataset)

}
```

Provider:

    SQD Provider

    AWS Provider

    RPC Provider

------------------------------------------------------------------------

# 9. Provider选择策略

## 历史交易

优先：

    AWS
    >
    SQD
    >
    RPC

------------------------------------------------------------------------

## Logs / Transfers

优先：

    SQD
    >
    AWS
    >
    RPC

------------------------------------------------------------------------

## 实时余额

优先：

    RPC

------------------------------------------------------------------------

## 单地址补充

优先：

    RPC

------------------------------------------------------------------------

# 10. Provider评分模型

Score:

    score =
    success_rate * 40
    +
    latency * 20
    +
    availability * 20
    +
    cost * 10
    +
    freshness * 10

示例：

    SQD

    Success 85
    Latency 70

    Score:

    78

自动降低优先级。

------------------------------------------------------------------------

# 11. SQD健康监控

指标：

    sqd_success_rate

    sqd_503_count

    sqd_timeout_count

    avg_latency

    worker_failure_rate

    adaptive_workers

    chunk_retry_count

日志：

    sqd-events.log

示例：

    2026-08-03

    503 detected

    workers:

    8 -> 4

    reason:

    NO_AVAILABLE_WORKER

------------------------------------------------------------------------

# 12. Agent自治决策

接入：

Smart Download Orchestrator

流程：

    任务创建

    ↓

    Agent分析Dataset

    ↓

    Provider健康评分

    ↓

    选择:

    SQD/AWS/RPC

    ↓

    执行

    ↓

    失败分析

    ↓

    自动调整

无需用户确认。

------------------------------------------------------------------------

# 13. 数据资产缓存层

增加：

    Dataset Registry

结构：

    dataset/

     bsc/

       transactions/

       logs/

       transfers/

       address_index/

规则：

已经存在的数据：

    禁止重复下载

新增地址：

    增量补充

------------------------------------------------------------------------

# 14. 与智能调查联动

流程：

    智能调查

    ↓

    地址集合

    ↓

    Download Orchestrator

    ↓

    Provider Router

    ↓

    生成Parquet

    ↓

    DuckDB索引

    ↓

    关系图生成

    ↓

    资金路径分析

------------------------------------------------------------------------

# 15. 验收标准

## 稳定性

-   503自动恢复
-   无人工干预
-   任务不中断

## 性能

10万地址：

-   自动调节并发
-   最大16 workers

## 数据一致性

必须满足：

    source

    =

    parsed

    =

    unique

    =

    parquet

    =

    duckdb

## 容灾

任意Provider失败：

    任务继续执行

------------------------------------------------------------------------

# 16. 后续扩展

支持：

-   ETH
-   Base
-   Arbitrum
-   Polygon

统一：

    Provider Interface

    Dataset Registry

    Download Orchestrator

形成企业级链上数据采集平台。
