# Token Transfer Multi-Provider Recovery Layer V1.0

版本：V1.0

## 1. 背景

当前 token_transfer 数据依赖 SQD Provider，但 SQD Portal 存在：

-   503 No available workers
-   Worker 不可用
-   公共资源波动

导致：

    token_transfer
          |
          v
         SQD
          |
          v
        503
          |
          v
        任务阻塞

目标：

将 token_transfer 从 SQD 单点依赖升级为多 Provider 自动恢复体系。

------------------------------------------------------------------------

## 2. 总体架构

    Investigation

        |

    Smart Download Orchestrator

        |

    Token Transfer Recovery Layer

        |

    +-----------+-----------+-----------+
    |           |           |
    AWS        RPC        SQD
    Logs       Logs       Events

        |

    Token Transfer Parser

        |

    Parquet Warehouse

        |

    DuckDB

------------------------------------------------------------------------

## 3. Provider策略

默认优先级：

    AWS
     >
    RPC
     >
    SQD

原因：

Token Transfer 本质是：

    ERC20/BEP20 Transfer Event

    =

    Log Topic解析

AWS适合历史批量分析。

RPC适合小规模补充。

SQD作为高速索引源，不作为唯一来源。

------------------------------------------------------------------------

## 4. Provider接口

``` go
type TokenTransferProvider interface {

    Name() string

    HealthScore() float64

    Download(addresses []Address) Result

    Support(dataset Dataset)

}
```

------------------------------------------------------------------------

## 5. AWS Provider

流程：

    AWS BSC parquet

    ↓

    logs数据

    ↓

    过滤topic0

    0xddf252ad

    ↓

    解析

    from

    to

    value

    token

    ↓

    token_transfer.parquet

优势：

-   稳定
-   大批量
-   不受SQD影响

------------------------------------------------------------------------

## 6. RPC Recovery Provider

使用：

    eth_getLogs

过滤：

    Transfer(address,address,uint256)

输出：

    transaction_hash

    log_index

    token_address

    from

    to

    value

    block_number

适合：

-   971地址
-   增量补充
-   实时查询

------------------------------------------------------------------------

## 7. SQD Provider

定位：

高速索引。

规则：

    健康:

    允许使用


    503率 > 30%

    自动降级


    恢复窗口:

    重新加入

------------------------------------------------------------------------

## 8. Provider评分

公式：

    score=

    success_rate*40

    +

    latency*20

    +

    availability*20

    +

    cost*10

    +

    freshness*10

自动选择最高评分Provider。

------------------------------------------------------------------------

## 9. 自动恢复流程

    创建任务

    ↓

    检测Provider健康

    ↓

    选择Provider

    ↓

    下载

    ↓

    失败分析

    ↓

    自动切换

    ↓

    数据合并

    ↓

    唯一化

    ↓

    Parquet

    ↓

    DuckDB

------------------------------------------------------------------------

## 10. 唯一键设计

    chain_id

    +

    transaction_hash

    +

    log_index

    +

    token_address

保证：

    raw

    =

    parsed

    =

    unique

    =

    parquet

    =

    duckdb

------------------------------------------------------------------------

## 11. 与智能调查联动

流程：

    地址输入

    ↓

    智能调查Agent

    ↓

    Token Transfer Recovery

    ↓

    生成资金流

    ↓

    地址关系图

    ↓

    继续扩展追踪

------------------------------------------------------------------------

## 12. Agent自治决策

输入：

    dataset:

    token_transfer

    addresses:

    971

    objective:

    资金流向分析

Agent自动判断：

    历史数据 -> AWS

    实时数据 -> RPC

    SQD -> 备用

无需用户选择。

------------------------------------------------------------------------

## 13. 状态机

新增：

    WAITING_PROVIDER

    PROVIDER_SWITCHING

    RECOVERING

    MERGING

    VALIDATING

    COMPLETED

------------------------------------------------------------------------

## 14. 验收标准

功能：

-   SQD失败任务不中断
-   自动切换Provider
-   自动恢复

数据：

    source

    =

    parsed

    =

    unique

    =

    parquet

    =

    duckdb

性能：

971地址：

分钟级完成。

10万地址：

AWS批量分析 + RPC增量补充。

------------------------------------------------------------------------

## 15. 后续扩展

V1.1:

-   Token Metadata关联
-   Holder分析
-   风险评分

V2.0:

-   NFT Transfer Recovery
-   Trace Recovery
-   全Dataset Provider自治调度
