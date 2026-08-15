# Smart Download Orchestrator 智能下载调度系统 V2.0（Agent自治决策版）

## 1. 目标

将 Browser Downloader、SQD Downloader、RPC Provider
三类数据获取能力统一接入智能调度层，由系统自动判断最佳数据来源。

核心原则：

-   用户只提出调查目标。
-   Investigation Agent 理解目标和需要的数据。
-   Smart Download Orchestrator 决定数据获取策略。
-   Downloader 负责执行。
-   Graph 和 Evidence 自动更新。

------------------------------------------------------------------------

# 2. 总体架构

    用户调查目标
            |
            v
    Investigation Agent
            |
            v
    Data Requirement Planner
            |
            v
    Smart Download Orchestrator
            |
    +---------------+---------------+---------------+
    |               |               |
    Browser        SQD             RPC
    Provider       Provider        Provider
    |               |               |
    +---------------+---------------+
                    |
                    v
    Dataset Registry
                    |
                    v
    DuckDB / Parquet
                    |
                    v
    Address Relationship Graph
                    |
                    v
    Evidence / Investigation Report

------------------------------------------------------------------------

# 3. Agent 与调度器职责

## Investigation Agent

负责：

-   理解调查目标
-   判断需要哪些数据
-   生成 Data Requirement

例如：

``` json
{
  "objective": "追踪资金最终沉淀位置",
  "required_data": [
    "transactions",
    "token_transfer",
    "trace",
    "balance",
    "labels"
  ]
}
```

Agent 不直接选择 SQD、RPC 或 Browser。

------------------------------------------------------------------------

# 4. Smart Download Orchestrator

职责：

-   分析数据需求
-   检查本地数据覆盖
-   计算 Provider 得分
-   自动生成 Download Plan
-   调度执行
-   失败切换

------------------------------------------------------------------------

# 5. 三层决策模型

## Layer 1：规则引擎

示例：

余额：

    RPC

最近交易：

    RPC

多年历史资金流：

    SQD

CSV 地址：

    Browser CSV

网页标签：

    Browser Crawl

------------------------------------------------------------------------

## Layer 2：Provider Score

评分：

    Provider Score =
    Coverage
    +
    Accuracy
    +
    Speed
    +
    Cost
    +
    Reliability
    +
    Historical Success

示例：

    BSC Token Transfer 100K 地址

    SQD       96
    RPC       42
    Browser   18

    选择 SQD

------------------------------------------------------------------------

## Layer 3：Agent Arbitration

以下情况调用 Agent：

-   多 Provider 分数接近
-   数据规模超过预算
-   调度失败
-   调查目标不明确

------------------------------------------------------------------------

# 6. Provider 能力

## Browser Downloader

支持：

-   CSV 下载
-   直链下载
-   网页爬取

用途：

-   地址列表
-   标签数据
-   公共页面信息
-   第三方公开资料

## SQD Provider

用途：

-   历史交易
-   Token Transfer
-   Logs
-   Trace
-   大批量地址

输出：

-   Parquet
-   DuckDB 数据资产

## RPC Provider

用途：

-   实时余额
-   合约状态
-   小范围补充
-   最新交易

------------------------------------------------------------------------

# 7. 混合下载计划

系统允许组合：

例如：

调查地址资金沉淀：

    Task 1:
    SQD
    历史 Token Transfer

    Task 2:
    RPC
    当前 USDT 余额

    Task 3:
    Browser
    交易所标签补充

------------------------------------------------------------------------

# 8. 与智能调查联动

流程：

    Investigation Request

    ↓

    Agent Planner

    ↓

    Data Requirement

    ↓

    Smart Download Orchestrator

    ↓

    Download Plan

    ↓

    Analysis

    ↓

    Evidence

    ↓

    Report

新增任务类型：

    DATA_REQUIREMENT_ANALYSIS
    DATA_COVERAGE_CHECK
    DOWNLOAD_PLAN_CREATE
    DATA_DOWNLOAD
    DATA_VALIDATE

------------------------------------------------------------------------

# 9. 与地址关系图联动

图中点击：

    继续追踪

产生：

``` json
{
  "action": "expand_graph",
  "address": "0xabc",
  "direction": "downstream",
  "depth": 3
}
```

流程：

    Graph Expansion
            |
            v
    Coverage Resolver
            |
            v
    Smart Download Orchestrator
            |
            v
    自动选择 Provider
            |
            v
    数据分析
            |
            v
    Graph Patch
            |
            v
    新增节点和边

------------------------------------------------------------------------

# 10. Coverage Resolver

负责判断：

-   是否已有数据
-   缺少什么数据
-   缺少哪个时间范围
-   是否需要下载

避免：

-   重复下载
-   重复分析
-   浪费 RPC

------------------------------------------------------------------------

# 11. Download Plan

示例：

``` json
{
  "investigation_id": "inv-001",
  "tasks": [
    {
      "dataset": "token_transfer",
      "provider": "sqd"
    },
    {
      "dataset": "balance",
      "provider": "rpc"
    }
  ]
}
```

------------------------------------------------------------------------

# 12. Provider Memory

记录：

    任务类型
    +
    链
    +
    数据规模
    +
    Provider
    +
    成功率
    +
    耗时

用于未来自动优化。

------------------------------------------------------------------------

# 13. 状态机

    ANALYZING_REQUIREMENT

    SELECTING_PROVIDER

    BUILDING_PLAN

    EXECUTING

    RETRYING

    FALLBACK

    VALIDATING

    MERGING

    READY_FOR_GRAPH

------------------------------------------------------------------------

# 14. 后端模块

    internal/downloadscheduler/

    scheduler.go

    decision_engine.go

    provider_registry.go

    cost_estimator.go

    coverage_checker.go

    download_plan.go

    browser_provider.go

    sqd_provider.go

    rpc_provider.go

------------------------------------------------------------------------

# 15. 前端展示

用户不看到：

    选择下载器

而看到：

    智能数据补充

    历史交易
    ✓ SQD

    实时余额
    ✓ RPC

    标签信息
    ✓ Browser

    正在分析...

------------------------------------------------------------------------

# 16. 安全和限制

必须：

-   地址校验
-   深度限制
-   地址数量限制
-   下载预算
-   API Key 加密
-   错误脱敏
-   Provider 熔断
-   Retry 控制

------------------------------------------------------------------------

# 17. 最终闭环

    用户目标

    ↓

    Agent 理解

    ↓

    Smart Download Orchestrator 决策

    ↓

    Browser / SQD / RPC 自动执行

    ↓

    数据资产生成

    ↓

    关系图更新

    ↓

    发现新线索

    ↓

    继续自动调查

最终形成：

AI 自治链上调查数据获取系统。
