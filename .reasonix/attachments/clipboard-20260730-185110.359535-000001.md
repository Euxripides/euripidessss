# SQD Provider 高可用与任务调度修复方案 V1.4.2（Codex部署版）

项目路径：

`E:\codex\bsc_analytics`

## 背景

当前系统接入 SQD finalized-stream 后出现真实错误：

    503 No available workers

该问题属于 SQD Provider 资源不可用，不是本地解析错误。

本方案目标：

-   增强 SQD 稳定性；
-   避免 503 导致任务失败；
-   支持自动恢复；
-   支持 checkpoint 续跑；
-   增加 Provider 状态管理。

------------------------------------------------------------------------

# 1. 架构升级

原流程：

    Task
     ↓
    SQD Client
     ↓
    SQD API
     ↓
    503
     ↓
    Retry

升级：

    Task
     ↓
    SQD Scheduler
     ↓
    Health Monitor
     ↓
    Circuit Breaker
     ↓
    SQD Client
     ↓
    SQD Stream

------------------------------------------------------------------------

# 2. SQD Provider 状态机

新增状态：

    HEALTHY

    DEGRADED

    NO_AVAILABLE_WORKERS

    RATE_LIMITED

    UNAVAILABLE

    RECOVERING

------------------------------------------------------------------------

# 3. 503处理

禁止：

    503
    ↓
    1秒重试
    ↓
    继续失败

改为：

    503 No available workers

    ↓

    进入异常状态

    ↓

    冷却等待

    ↓

    健康检测

    ↓

    恢复任务

退避：

    30秒

    60秒

    120秒

    最大10分钟

------------------------------------------------------------------------

# 4. SQD Scheduler

新增：

    internal/datasource/sqd/scheduler/

负责：

-   任务排队；
-   并发控制；
-   优先级；
-   资源保护。

默认：

``` yaml
sqd:
  max_parallel_streams: 1
  max_large_jobs: 1
  max_small_jobs: 2
```

------------------------------------------------------------------------

# 5. 大任务拆分

禁止：

    一次请求整个链历史

改：

    Batch 1

    Batch 2

    Batch 3

每个 Batch：

-   独立 checkpoint；
-   独立状态；
-   完成后合并 Parquet。

------------------------------------------------------------------------

# 6. 错误分类

  错误             处理
  ---------------- ----------
  401              配置错误
  400              参数错误
  429              限速退避
  503 No workers   冷却恢复
  Timeout          降低并发
  Network          短重试

------------------------------------------------------------------------

# 7. Checkpoint恢复

保存：

    chain

    dataset

    start_block

    end_block

    current_block

    completed_chunks

    manifest

异常：

    503

    ↓

    读取checkpoint

    ↓

    恢复执行

------------------------------------------------------------------------

# 8. 健康检测

检测：

-   503次数；
-   成功率；
-   延迟；
-   最近成功时间；
-   当前任务数量。

------------------------------------------------------------------------

# 9. 前端

入口：

    虚拟币
     └── 数据源管理
          └── SQD详情

展示：

    SQD Finalized Stream

    状态

    最近503次数

    当前任务

    平均延迟

    最近恢复时间

------------------------------------------------------------------------

# 10. 测试

必须测试：

## 503模拟

验证：

-   状态进入 NO_AVAILABLE_WORKERS；
-   不无限重试。

## 恢复测试

验证：

-   Provider恢复；
-   自动继续任务。

## 断点测试

流程：

    运行50%

    ↓

    503

    ↓

    恢复

    ↓

    继续完成

------------------------------------------------------------------------

# 完成标准

V1.4.2完成：

-   SQD异常可恢复；
-   不重复下载；
-   状态可观察；
-   支持长期运行。

最终：

    Data Source

    ↓

    Provider Manager

    ↓

    Task Scheduler

    ↓

    SQD Stream

    ↓

    Parquet

    ↓

    DuckDB
