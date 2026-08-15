# Investigation Agent Planner V2 设计方案

## 1. 背景

当前链上分析平台已经具备地址画像、资金流分析、风险分析、实体识别、图谱分析和自动调查状态机。

现有模式：

地址输入 -\> 固定规则分析 -\> 风险/扩展评分 -\> 停止

该模式更像扫描器，不是真正的调查 Agent。

V2目标：

用户输入调查目的和期望结果，由 Agent 自动生成调查计划，并驱动执行引擎。

------------------------------------------------------------------------

# 2. 新调查流程

    调查地址
    +
    调查目的
    +
    期望结果

            ↓

    Intent Analyzer

            ↓

    Investigation Planner

            ↓

    Task Scheduler

            ↓

    Analysis Engine

            ↓

    动态调整计划

            ↓

    调查报告

------------------------------------------------------------------------

# 3. 用户输入模型

新增：

-   目标地址
-   链
-   调查目的
-   期望结果
-   调查模式

示例：

    地址:
    0x123


    目的:
    这是一个大额获利地址，寻找最终资金沉淀。


    期望结果:
    1. 找资金去向
    2. 找交易所入口
    3. 找关联钱包
    4. 输出资金流图

------------------------------------------------------------------------

# 4. Investigation Request

数据库：

## investigation_request

字段：

    id
    address
    chain
    objective
    expected_result
    mode
    status
    created_at

JSON：

``` json
{
 "address":"0xabc",
 "chain":"bsc",
 "objective":"寻找资金沉淀地址",
 "expected_result":[
   "资金流图",
   "交易所入口",
   "关联钱包"
 ],
 "mode":"fund_trace"
}
```

------------------------------------------------------------------------

# 5. Agent Planner

Planner负责：

1.  理解调查目标
2.  选择分析模块
3.  生成任务顺序
4.  设置优先级
5.  根据结果重新规划

------------------------------------------------------------------------

# 6. 调查任务类型

    ADDRESS_PROFILE

    BALANCE_ANALYSIS

    TOKEN_ANALYSIS

    PROFIT_DETECTION

    FORWARD_TRACE

    BACKWARD_TRACE

    FLOW_GRAPH

    EXCHANGE_DETECTION

    ENTITY_CLUSTER

    RISK_ANALYSIS

    IDENTITY_LOOKUP

    REPORT_GENERATE

------------------------------------------------------------------------

# 7. Planner输出

示例：

``` json
{
 "plan_id":"plan_001",
 "goal":"寻找最终资金沉淀",
 "tasks":[
   {
    "name":"address_profile",
    "priority":1
   },
   {
    "name":"profit_detection",
    "priority":2
   },
   {
    "name":"forward_trace",
    "depth":8,
    "priority":3
   },
   {
    "name":"exchange_detection",
    "priority":4
   }
 ]
}
```

------------------------------------------------------------------------

# 8. 动态重新规划

例如发现：

    目标地址收到500万USDT

    来源:
    地址A
    地址B

Agent自动追加：

    追踪地址A

    追踪地址B

    分析资金来源

    提高调查优先级

------------------------------------------------------------------------

# 9. 新价值评分模型

替代：

    风险
    实体
    扩展
    路径

升级：

    Investigation Score

    =
    Fund Score
    +
    Behavior Score
    +
    Risk Score
    +
    Entity Score
    +
    Graph Score
    +
    Identity Score

------------------------------------------------------------------------

# 10. Fund Score

资金价值：

余额：

    >$100万 +30

    >$1000万 +50

获利：

    买入Token

    ↓

    上涨

    ↓

    卖出

    ↓

    USDT沉淀

    +30

资金沉淀：

    长期持有大额资产

    +20

------------------------------------------------------------------------

# 11. 前端新增组件

目录：

    components/Investigation/

    InvestigationInput.tsx

    ObjectiveEditor.tsx

    ExpectedResultSelector.tsx

    PlanPreview.tsx

    AgentTimeline.tsx

    ResultSummary.tsx

------------------------------------------------------------------------

# 12. 调查计划展示

用户启动后：

    AI生成调查计划


    目标:
    寻找资金沉淀


    任务:

    ✓ 地址画像

    ✓ 获利检测

    ✓ 资金追踪

    ✓ 交易所识别

    ✓ 地址聚类


    预计时间:
    8分钟

------------------------------------------------------------------------

# 13. Agent执行时间线

展示：

    11:00 完成地址画像

    11:02 发现500万USDT流入

    11:03 新增来源追踪任务

    11:05 发现交易所入口

------------------------------------------------------------------------

# 14. API设计

创建调查：

    POST /api/investigation/create

请求：

``` json
{
 "address":"",
 "objective":"",
 "expected_result":[],
 "mode":""
}
```

查询计划：

    GET /api/investigation/{id}/plan

查询任务：

    GET /api/investigation/{id}/tasks

------------------------------------------------------------------------

# 15. 与现有inv融合

保留：

    inv
    round
    stage
    decision

新增：

    request

    plan

    task

    result

结构：

    Investigation

     ├── Request

     ├── Plan

     ├── Tasks

     └── Results

------------------------------------------------------------------------

# 16. Codex实施阶段

## Phase 1

调查输入模块。

## Phase 2

Planner Agent。

## Phase 3

动态任务调整。

## Phase 4

智能报告和证据链。

------------------------------------------------------------------------

# 最终目标

从：

    自动地址扫描工具

升级为：

    AI驱动链上调查系统

支持：

-   大额获利地址分析
-   上分地址追踪
-   资金沉淀发现
-   交易所入口发现
-   身份线索分析
-   自动生成调查路径
