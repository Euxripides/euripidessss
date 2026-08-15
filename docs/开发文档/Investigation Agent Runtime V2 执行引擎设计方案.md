# Investigation Agent Runtime V2 执行引擎设计方案

## 1. 背景

当前 Investigation Agent 已具备：

-   Investigation Request
-   Intent Analyzer
-   Planner Agent
-   Task Scheduler
-   Executor 模块
-   Evidence Layer
-   Storage Layer

下一步需要 Runtime 执行引擎，把静态调查计划转换为动态执行过程。

目标：

    用户目标
     ↓
    Planner生成计划
     ↓
    Runtime执行
     ↓
    任务调度
     ↓
    结果分析
     ↓
    证据提取
     ↓
    动态重新规划

------------------------------------------------------------------------

# 2. Runtime V2职责

负责：

-   Plan加载
-   Task创建
-   队列管理
-   执行调度
-   状态管理
-   失败恢复
-   动态扩展
-   Evidence生成

------------------------------------------------------------------------

# 3. 架构

    Investigation Planner

            |

            v

    Runtime Controller

            |

    +----------------+

    | Task Queue     |

    | State Manager  |

    +----------------+

            |

            v

    Executor Pool

            |

            v

    Result Collector

            |

            v

    Evidence Extractor

            |

            v

    Re-plan Trigger

------------------------------------------------------------------------

# 4. Runtime Controller

管理调查生命周期。

状态：

    CREATED
    PLANNED
    RUNNING
    WAITING
    COMPLETED
    FAILED
    STOPPED

------------------------------------------------------------------------

# 5. Task Queue

任务模型：

``` json
{
"id":"task-001",
"type":"FORWARD_TRACE",
"priority":10,
"status":"WAITING"
}
```

支持：

-   优先级
-   依赖
-   重试
-   超时

------------------------------------------------------------------------

# 6. Executor Pool

统一执行器：

    AddressProfileExecutor

    BalanceExecutor

    TokenExecutor

    ProfitExecutor

    ForwardTraceExecutor

    BackwardTraceExecutor

    GraphExecutor

    ExchangeDetectorExecutor

    EntityClusterExecutor

    RiskExecutor

    IdentityExecutor

    ReportExecutor

------------------------------------------------------------------------

# 7. Executor接口

统一：

``` go
type Executor interface {
 Type() string
 Execute(ctx, task)
 Validate()
}
```

返回：

``` json
{
"status":"SUCCESS",
"findings":[],
"evidence":[]
}
```

------------------------------------------------------------------------

# 8. 动态任务追加

发现：

    地址A

    收到500万USDT

    来源地址B

Runtime自动追加：

    BACKWARD_TRACE(B)

要求：

-   幂等
-   去重
-   预算限制

------------------------------------------------------------------------

# 9. Re-plan机制

触发：

-   高价值资金发现
-   新实体发现
-   新资金路径发现

流程：

    Result
     ↓
    Planner
     ↓
    New Task
     ↓
    Runtime Merge

------------------------------------------------------------------------

# 10. 调查预算

限制：

``` json
{
"max_tasks":50,
"max_depth":8,
"max_addresses":500,
"max_runtime":1800
}
```

防止：

-   无限扩展
-   图谱爆炸
-   Agent循环

------------------------------------------------------------------------

# 11. 恢复机制

异常：

    Runtime停止

    ↓

    读取Storage

    ↓

    恢复任务状态

    ↓

    继续执行

RUNNING任务：

通过heartbeat判断是否需要重试。

------------------------------------------------------------------------

# 12. Evidence Pipeline

流程：

    Executor Result

    ↓

    Finding Parser

    ↓

    Evidence Extractor

    ↓

    Evidence Store

    ↓

    Report

------------------------------------------------------------------------

# 13. Runtime日志

保存：

    runtime-events.log

记录：

-   task创建
-   执行
-   retry
-   failure
-   re-plan

------------------------------------------------------------------------

# 14. API

启动：

    POST /api/investigation/{id}/runtime/start

状态：

    GET /api/investigation/{id}/runtime/status

任务：

    GET /api/investigation/{id}/runtime/tasks

------------------------------------------------------------------------

# 15. 与inv融合

最终：

    Investigation

     ├ Request

     ├ Plan

     ├ Runtime

     ├ Task

     ├ Evidence

     └ Report

------------------------------------------------------------------------

# 16. 实施阶段

## Phase 1

Runtime Core

-   Controller
-   Queue
-   State

## Phase 2

Executor Pool

-   Executor接口
-   注册机制

## Phase 3

Dynamic Runtime

-   动态任务
-   Re-plan

## Phase 4

Recovery

-   崩溃恢复
-   日志体系

------------------------------------------------------------------------

# 最终目标

实现：

    用户提出调查目标

    ↓

    Agent生成方案

    ↓

    Runtime自主执行

    ↓

    发现线索

    ↓

    动态扩展

    ↓

    形成证据链

    ↓

    输出案件报告

成为：

AI Chain Investigation Runtime
